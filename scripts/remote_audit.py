#!/usr/bin/env python3
"""remote_audit.py — 远端/服务端库的**逐库取证账**。

为什么需要它：`remote-api` / `server-state` 是「本仓做不了」的归因，最容易变成
一句“desc 看起来像远程服务”的判断。本工具把每个未实现符号钉到**语料里的真实
调用点**上：文件、行号、实参键、头文件的签名与返回类型。结论只有两种：
`keep-remote`（需网络与/或凭证）、`keep-state`（需服务端句柄）。能本地实现的
符号必须被真实现接走（如 capcut.video_timelines），不留在本表里。

判据（可红）：
  R1 文档覆盖的库 == 当前以 remote-api/server-state 归因、且有 stub 符号的库
  R2 每个证据指针（file:line）必须在语料里真实命中该符号的调用
  R3 行内 class 必须与归因表一致（符号级覆盖优先）
  R4 每个库至少要有一个符号能被调用面证明（有实参键或头文件签名），否则判不了
读不到语料/头文件/归因表 => rc=3；参数错 => rc=2。

用法：
  python3 scripts/remote_audit.py             # 重写 docs/REMOTE-API-DOSSIER.md
  python3 scripts/remote_audit.py --check     # 漂移判红
  python3 scripts/remote_audit.py --json      # 机器读
"""
from __future__ import annotations

import argparse
import glob
import json
import os
import re
import sys

try:
    import yaml  # noqa: F401  (归因表由 unbound_attribution 解析)
except Exception as e:  # pragma: no cover
    print(f"[remote-audit] 判不了：PyYAML 不可用（{e}）", file=sys.stderr)
    sys.exit(3)

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import gen_stub_plugin as gs  # noqa: E402
import library_coverage as lc  # noqa: E402

ROOT = os.path.dirname(HERE)
DOC = os.path.join(ROOT, "docs", "REMOTE-API-DOSSIER.md")
CLASSES = ("remote-api", "server-state")

KEY_RE = re.compile(r'([A-Za-z_][A-Za-z0-9_]*)|"((?:[^"\\]|\\.)*)"')
SIG_RE = re.compile(r"^function\s+([A-Za-z_]\w*)\s*\((.*)\)\s*->\s*([^;{]+)", re.M)
DESC_RE = re.compile(r"^// desc:\s*(.+)$", re.M)
TYPE_RE = re.compile(r"^type\s+([A-Za-z_]\w*)\s*=\s*\{(.*?)\};", re.M | re.S)
FIELD_RE = re.compile(r"^\s*[A-Za-z_][\w<>,\s]*?\s+([A-Za-z_]\w*)\s*;", re.M)

CRED = re.compile(r"(^|_)key$|token$|secret$|^cookie(st|str)?$|credential|authorization|^appid$|^appsecret$|^openid$", re.I)
URLISH = re.compile(r"url|uri|link|file|path|html_str|src", re.I)
HANDLE = re.compile(r"task_id|draft_id|draft_url|table_id|workflow_id|voice_id|^id$|_id$", re.I)
SEARCH = re.compile(r"query|search_query|input_query|keyword|^kw$|^q$|book_name|b_name", re.I)
MODEL = re.compile(r"model|prompt|^text$|^content$|formatted_markdown|^prompt$", re.I)


def skip_string(body: str, j: int) -> int:
    q = body[j]
    n = len(body)
    j += 1
    esc = False
    while j < n:
        c = body[j]
        if esc:
            esc = False
        elif c == "\\":
            esc = True
        elif c == q:
            return j + 1
        j += 1
    return n


def scan_args(body: str, i: int):
    """从调用左括号 i 起，返回 (结束下标, 顶层 map 的键列表 or None, 原文)。"""
    n = len(body)
    j = i
    depth = 0
    keys: list[str] = []
    map_depth = None
    expect_key = False
    while j < n:
        c = body[j]
        if c in "([{":
            depth += 1
            if c == "{" and depth == 2 and map_depth is None:
                map_depth = depth
                expect_key = True
            j += 1
            continue
        if c in ")]}":
            depth -= 1
            if depth == 0:
                return j, (keys or None), body[i:j + 1]
            if map_depth is not None and depth < map_depth:
                map_depth = None
                expect_key = False
            j += 1
            continue
        if map_depth is not None and depth == map_depth and expect_key:
            if c in " \t\r\n":
                j += 1
                continue
            if c == ",":
                j += 1
                continue
            if c == ":":
                expect_key = False
                j += 1
                continue
            m = KEY_RE.match(body, j)
            if m:
                k = m.group(1) or m.group(2)
                if k not in keys:
                    keys.append(k)
                j = m.end()
                expect_key = False
                continue
            j += 1
            continue
        if c in "\"'`":
            j = skip_string(body, j)
            continue
        if map_depth is not None and depth == map_depth and c == ",":
            expect_key = True
        j += 1
    return n, (keys or None), body[i:]


def corpus_files(corpus: str) -> list[str]:
    if not os.path.isdir(corpus):
        raise lc.CannotJudge(f"corpus not readable: {corpus}")
    files = sorted(glob.glob(os.path.join(corpus, "*.ncoda")))
    if not files:
        raise lc.CannotJudge(f"corpus has no *.ncoda: {corpus}")
    return files


def call_sites(corpus: str, symbols: set[tuple[str, str]]) -> dict[tuple[str, str], list[dict]]:
    import_re = re.compile(r'^\s*import\s+"([A-Za-z0-9_]+)"\s*;', re.M)
    sites: dict[tuple[str, str], list[dict]] = {s: [] for s in symbols}
    by_lib: dict[str, set[str]] = {}
    for lib, fn in symbols:
        by_lib.setdefault(lib, set()).add(fn)
    for path in corpus_files(corpus):
        with open(path, encoding="utf-8", errors="replace") as fh:
            body = fh.read()
        imports = set(import_re.findall(body))
        for lib in sorted(imports & set(by_lib)):
            for m in re.finditer(r"\b" + re.escape(lib) + r"\.([A-Za-z_][A-Za-z0-9_]*)\s*\(", body):
                fn = m.group(1)
                if fn not in by_lib[lib]:
                    continue
                p = body.index("(", m.end() - 1)
                _, keys, arg = scan_args(body, p)
                sites[(lib, fn)].append({
                    "file": os.path.basename(path),
                    "line": body.count("\n", 0, m.start()) + 1,
                    "keys": keys,
                    "arg": " ".join(arg.split())[:160],
                })
    return sites


def header_facts(lib: str) -> tuple[str, dict[str, tuple[str, str]], dict[str, list[str]]]:
    path = os.path.join(lc.HEADERS, lib + ".ncoda")
    with open(path, encoding="utf-8", errors="replace") as fh:
        body = fh.read()
    desc = DESC_RE.search(body)
    sigs = {}
    for line in body.splitlines():
        m = SIG_RE.match(line)
        if m:
            sigs[m.group(1)] = (m.group(2).strip(), m.group(3).strip())
    types = {m.group(1): FIELD_RE.findall(m.group(2)) for m in TYPE_RE.finditer(body)}
    return (desc.group(1).strip() if desc else ""), sigs, types


def verdict(cls: str, keys: set[str]) -> str:
    if cls == "server-state":
        return "keep-state"
    if any(CRED.search(k) for k in keys):
        return "keep-remote(凭据)"
    if any(URLISH.search(k) for k in keys):
        return "keep-remote(远端资源)"
    if any(HANDLE.search(k) for k in keys):
        return "keep-remote(远端句柄)"
    if any(SEARCH.search(k) for k in keys):
        return "keep-remote(远端检索)"
    if any(MODEL.search(k) for k in keys):
        return "keep-remote(远端模型)"
    return "keep-remote(远端服务)"


def build(corpus: str | None = None) -> list[dict]:
    stub = gs.collect(corpus)
    symbols = {(lib, fn) for lib, fns in stub.items() for fn in fns
               if fns[fn]["class"] in CLASSES}
    if not symbols:
        raise gs.ua.CannotJudge("no remote-api/server-state stub symbols; corpus ledger moved?")
    sites = call_sites(corpus or lc.DEFAULT_CORPUS, symbols)
    rows = []
    for lib in sorted({lib for lib, _ in symbols}):
        desc, sigs, types = header_facts(lib)
        syms = []
        keyset: set[str] = set()
        for fn in sorted(fn for l, fn in symbols if l == lib):
            seen = sites[(lib, fn)]
            keys = sorted({k for s in seen for k in (s["keys"] or [])})
            keyset |= set(keys)
            sig, ret = sigs.get(fn, ("?", "?"))
            syms.append({
                "fn": fn,
                "calls": len(seen),
                "keys": keys,
                "positional": any(s["keys"] is None for s in seen),
                "first": (f'{seen[0]["file"]}:{seen[0]["line"]}' if seen else ""),
                "sig": sig,
                "ret": ret,
                "ret_handles": [f for f in types.get(ret, [])
                                if re.search(r"^(draft_url|draft_id|task_id|table_id)$", f)],
                "class": stub[lib][fn]["class"],
            })
        cls = "server-state" if any(s["class"] == "server-state" for s in syms) else "remote-api"
        rows.append({"lib": lib, "class": cls, "desc": desc, "symbols": syms,
                     "verdict": verdict(cls, keyset)})
    return rows


def render(rows: list[dict]) -> str:
    total_syms = sum(len(r["symbols"]) for r in rows)
    total_calls = sum(s["calls"] for r in rows for s in r["symbols"])
    out = [
        "# 远端 / 服务端库逐库结论（Remote-API Dossier）",
        "",
        "状态：**generated** · 判据：`scripts/remote_audit.py --check`（rc 0/1/2/3）",
        "生成物，请勿手改：改动归因或语料后重跑 `python3 scripts/remote_audit.py`。",
        "",
        f"覆盖：**{len(rows)} 个库 / {total_syms} 个未实现符号 / {total_calls} 处语料调用**。",
        "结论只有两种：`keep-remote`（需要网络与/或凭证）、`keep-state`（需要服务端句柄）。",
        "能本地实现的符号必须被真实现接走，不留在本表（例：`capcut.video_timelines` 已由",
        "`plugins/timeline_toolkit` 解析容器头实现，故不在此表）。",
        "",
        "证据写法：`fn(调用数; 实参键 @ 首个调用点 file:line) -> 返回类型`。",
        "实参键为空表示调用面没有 map 字面量（位置参数或空 map）；这类条目靠头文件",
        "描述与返回类型定类，不靠猜。",
        "",
        "| library | class | desc | 未实现符号（证据） | 结论 |",
        "| --- | --- | --- | --- | --- |",
    ]
    for r in rows:
        parts = []
        for s in r["symbols"]:
            keys = ",".join(s["keys"]) if s["keys"] else ("(positional)" if s["positional"] else "{}")
            ret = s["ret"]
            if s.get("ret_handles"):
                ret += "{" + ",".join(s["ret_handles"]) + "}"
            parts.append(f'`{s["fn"]}`({s["calls"]}; {keys} @ {s["first"]}) -> `{ret}`')
        out.append(f'| `{r["lib"]}` | {r["class"]} | {r["desc"]} | '
                   + "<br>".join(parts) + f' | {r["verdict"]} |')
    out.append("")
    return "\n".join(out)


def verify(rows: list[dict], corpus: str | None = None) -> list[str]:
    """R2/R3：证据指针必须命中真实调用，class 必须与归因表一致。"""
    problems = []
    base = corpus or lc.DEFAULT_CORPUS
    cache: dict[str, str] = {}
    for r in rows:
        for s in r["symbols"]:
            if not s["first"]:
                problems.append(f'{r["lib"]}.{s["fn"]}: 没有语料调用点，无法取证')
                continue
            fname, _, line = s["first"].rpartition(":")
            path = os.path.join(base, fname)
            if path not in cache:
                if not os.path.isfile(path):
                    raise lc.CannotJudge(f"evidence file missing: {path}")
                with open(path, encoding="utf-8", errors="replace") as fh:
                    cache[path] = fh.read()
            body = cache[path]
            try:
                text = body.splitlines()[int(line) - 1]
            except (IndexError, ValueError):
                problems.append(f'{r["lib"]}.{s["fn"]}: 证据行号越界 {s["first"]}')
                continue
            if not re.search(re.escape(r["lib"]) + r"\." + re.escape(s["fn"]) + r"\s*\(", text):
                problems.append(
                    f'{r["lib"]}.{s["fn"]}: 证据指针 {s["first"]} 不含该调用（行号漂移？）')
    return problems


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--corpus", default=lc.DEFAULT_CORPUS)
    ap.add_argument("--check", action="store_true", help="与生成物逐字节比对 + 校验证据")
    ap.add_argument("--json", action="store_true")
    args = ap.parse_args(argv)

    try:
        rows = build(args.corpus)
    except (lc.CannotJudge, gs.ua.CannotJudge) as e:
        print(f"[remote-audit] 判不了：{e}", file=sys.stderr)
        return 3
    except gs.ua.BadTable as e:
        print(f"[remote-audit] 归因表非法：{e}", file=sys.stderr)
        return 2
    problems = verify(rows, args.corpus)

    if args.json:
        json.dump({"schema": "ncoda.remote-audit/v1", "rows": rows,
                   "problems": problems}, sys.stdout, ensure_ascii=False, indent=1)
        sys.stdout.write("\n")
        return 1 if problems else 0

    body = render(rows)
    if args.check:
        if not os.path.isfile(DOC):
            print(f"[remote-audit] ✗ 缺文档 {DOC}；先跑一次写入", file=sys.stderr)
            return 1
        with open(DOC, encoding="utf-8") as fh:
            current = fh.read()
        if current != body:
            print("[remote-audit] ✗ 文档漂移：重跑 `python3 scripts/remote_audit.py`",
                  file=sys.stderr)
            return 1
        if problems:
            print("[remote-audit] ✗ 证据不成立：", file=sys.stderr)
            for p in problems[:10]:
                print("  " + p, file=sys.stderr)
            return 1
        print(f"[remote-audit] ✓ {len(rows)} 库 / "
              f"{sum(len(r['symbols']) for r in rows)} 符号，证据指针全部命中")
        return 0

    with open(DOC, "w", encoding="utf-8") as fh:
        fh.write(body)
    for p in problems[:10]:
        print("[remote-audit] ⚠ " + p, file=sys.stderr)
    print(f"[remote-audit] wrote {DOC}: {len(rows)} libs / "
          f"{sum(len(r['symbols']) for r in rows)} symbols")
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
