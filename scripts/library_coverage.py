#!/usr/bin/env python3
"""library_coverage.py — 语料库函数的**符号级**绑定覆盖账。

为什么不是“库级”：商业仓 corpus_runtime_sweep.py 的 complete 只要求
“这个 provider 至少有一条绑定”。绑一个函数就把整库算成完备，会让
未绑函数在运行期以 RBINDING_MISSING 变成失败，账面上却看不见。
本工具的判据是：**文件调用的每个 (library, function) 都有绑定**才算完备。

判据（可红）：
  C1 每个被调用的符号必须能在 capability header 里找到（L1 缺口）
  C2 每个被调用的符号必须有绑定（L2 缺口）
  C3 保真度分级：real / partial / stub，文件级取所用符号的最差值
  C4 基线棘轮：complete_exec / complete_real 不得下降，unbound 不得上升；
     stub 是「能链接、调用即显式失败」的占位实现，只能被 real/partial 取代，
     因此 stub 符号数不得上升（C6）
读不到语料 / 头文件 / 绑定表 => rc 3（判不了 ≠ 通过）。

用法：
  python3 scripts/library_coverage.py                 # 表格 + 汇总
  python3 scripts/library_coverage.py --json          # 机器可读
  python3 scripts/library_coverage.py --check         # 对基线判红
  python3 scripts/library_coverage.py --write-baseline # 重定基线（需 --reason）
"""
from __future__ import annotations

import argparse
import collections
import glob
import json
import os
import re
import sys

try:
    import yaml
except Exception as e:  # pragma: no cover
    print(f"[library-coverage] 判不了：PyYAML 不可用（{e}）", file=sys.stderr)
    sys.exit(3)

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_CORPUS = os.path.abspath(os.path.join(ROOT, "..", "ncoda-corpus", "corpus"))
HEADERS = os.path.join(ROOT, "libraries")
BINDINGS = os.path.join(ROOT, "pkg", "localtools", "bindings.yaml")
BASELINE = os.path.join(ROOT, "docs", "library-coverage-baseline.json")
PLUGINS = os.path.join(ROOT, "plugins")

RE_IMPORT = re.compile(r'^\s*import\s+"([A-Za-z0-9_]+)"\s*;', re.M)
RE_LIBRARY = re.compile(r"(?m)^library\s+([A-Za-z_][A-Za-z0-9_]*)\s*@")
RE_FUNCTION = re.compile(r"(?m)^function\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(")


class CannotJudge(RuntimeError):
    pass


def read(path: str) -> str:
    with open(path, encoding="utf-8", errors="replace") as fh:
        return fh.read()


def load_headers(headers_dir: str = HEADERS) -> dict[str, set[str]]:
    out: dict[str, set[str]] = {}
    for path in sorted(glob.glob(os.path.join(headers_dir, "*.ncoda"))):
        body = read(path)
        m = RE_LIBRARY.search(body)
        if not m:
            raise CannotJudge(f"{path}: no `library <name> @` declaration")
        name = m.group(1)
        if name != os.path.basename(path)[:-6]:
            raise CannotJudge(f"{path}: library {name} != file stem")
        out[name] = {x.group(1) for x in RE_FUNCTION.finditer(body)}
    if not out:
        raise CannotJudge(f"{headers_dir}: no capability headers")
    return out


def load_bindings(bindings_path: str = BINDINGS) -> dict[tuple[str, str], dict]:
    if not os.path.isfile(bindings_path):
        raise CannotJudge(f"bindings not readable: {bindings_path}")
    doc = yaml.safe_load(read(bindings_path)) or {}
    rows = doc.get("bindings")
    if not isinstance(rows, list) or not rows:
        raise CannotJudge(f"{bindings_path}: bindings is not a non-empty list")
    out = {}
    for row in rows:
        key = (str(row.get("provider")), str(row.get("operation")))
        if key in out:
            raise CannotJudge(f"{bindings_path}: duplicate binding {key[0]}/{key[1]}")
        out[key] = row
    return out


def load_plugins(plugin_root: str = PLUGINS) -> dict[tuple[str, str], dict]:
    """进程外插件（plugins/<id>/ncoda-plugin.yaml）声明的符号。

    只读清单，不启动进程：清单是加载期校验过的输入，符号面由它唯一决定。
    插件与内建绑定声明同一符号 = 运行期注册冲突，这里直接判红（C5）。
    """
    if not os.path.isdir(plugin_root):
        return {}
    out: dict[tuple[str, str], dict] = {}
    for manifest in sorted(glob.glob(os.path.join(plugin_root, "*", "ncoda-plugin.yaml"))):
        doc = yaml.safe_load(read(manifest)) or {}
        pid = str(doc.get("id") or os.path.basename(os.path.dirname(manifest)))
        for lib in doc.get("libraries") or []:
            name = str(lib.get("library") or "")
            base_fid = str(lib.get("fidelity") or "real")
            base_loss = str(lib.get("loss") or "")
            if base_fid not in ("real", "partial", "stub"):
                raise CannotJudge(f"{manifest}: fidelity {base_fid!r} is not real/partial/stub")
            # loss 可以写在库级，也可以逐函数覆盖；有效性按**函数**判定（与 Go 端一致）。
            for item in lib.get("functions") or []:
                # 函数级 fidelity：字符串继承库级；映射可覆盖（与 Go 端同一条规则）。
                if isinstance(item, str):
                    fn, fid, loss = item, base_fid, base_loss
                elif isinstance(item, dict):
                    fn = str(item.get("name") or "")
                    if not fn.strip():
                        raise CannotJudge(f"{manifest}: {name}: functions entry without name")
                    fid = str(item.get("fidelity") or base_fid)
                    loss = str(item.get("loss") or base_loss)
                    if fid not in ("real", "partial", "stub"):
                        raise CannotJudge(f"{manifest}: {name}.{fn}: fidelity {fid!r} invalid")
                    if fid in ("partial", "stub") and not loss.strip():
                        raise CannotJudge(f"{manifest}: {name}.{fn}: fidelity {fid} requires loss")
                else:
                    raise CannotJudge(f"{manifest}: {name}: functions entry must be str or mapping")
                if fid in ("partial", "stub") and not loss.strip():
                    raise CannotJudge(f"{manifest}: {name}.{fn}: fidelity {fid} requires loss")
                key = (name, fn)
                if key in out:
                    raise CannotJudge(
                        f"{manifest}: duplicate plugin declaration {name}.{fn} "
                        f"(already from {out[key]['plugin']})")
                out[key] = {"implementation": fid, "plugin": pid, "loss": loss}
    return out


def load_corpus(corpus: str) -> dict[str, tuple[set[str], set[tuple[str, str]]]]:
    if not os.path.isdir(corpus):
        raise CannotJudge(f"corpus not readable: {corpus}")
    files = sorted(glob.glob(os.path.join(corpus, "*.ncoda")))
    if not files:
        raise CannotJudge(f"corpus has no *.ncoda: {corpus}")
    out = {}
    for path in files:
        body = read(path)
        libs = set(RE_IMPORT.findall(body))
        calls = set()
        for lib in libs:
            for m in re.finditer(r"\b" + re.escape(lib) + r"\.([A-Za-z_][A-Za-z0-9_]*)\s*\(", body):
                calls.add((lib, m.group(1)))
        out[os.path.basename(path)] = (libs, calls)
    return out


def fidelity(binding: dict) -> str:
    impl = str(binding.get("implementation") or "missing")
    return impl if impl in ("real", "partial", "stub") else "missing"


def worst(values: list[str]) -> str:
    order = {"real": 0, "partial": 1, "stub": 2, "missing": 3}
    return max(values, key=lambda v: order[v]) if values else "real"


def analyze(corpus: str, headers_dir: str = HEADERS, bindings_path: str = BINDINGS,
            plugin_root: str = PLUGINS) -> dict:
    headers = load_headers(headers_dir)
    bindings = load_bindings(bindings_path)
    plugins = load_plugins(plugin_root)
    shadowed = sorted(set(bindings) & set(plugins))
    files = load_corpus(corpus)

    # C1: every called symbol must be declared in some header.
    undeclared = sorted({
        (lib, fn) for _, calls in files.values() for (lib, fn) in calls
        if fn not in headers.get(lib, set())
    })

    bound = set(bindings) | set(plugins)

    def fid_of(sym: tuple[str, str]) -> str:
        vals = []
        if sym in bindings:
            vals.append(fidelity(bindings[sym]))
        if sym in plugins:
            vals.append(fidelity(plugins[sym]))
        return worst(vals)
    per_file = []
    unbound_symbols: set[tuple[str, str]] = set()
    for name, (libs, calls) in files.items():
        missing = sorted(c for c in calls if c not in bound)
        unbound_symbols.update(missing)
        fids = [fid_of(c) for c in calls if c in bound]
        per_file.append({
            "file": name,
            "imports": sorted(libs),
            "calls": len(calls),
            "missing": missing,
            "fidelity": worst(fids) if calls else "real",
            "complete": not missing,
        })

    complete = [f for f in per_file if f["complete"]]
    complete_real = [f for f in complete if f["fidelity"] == "real"]
    complete_exec = [f for f in complete if f["fidelity"] in ("real", "partial")]
    # stub 文件：所有符号都能解析，但没有一个真实现。它「能跑」，跑出来是
    # not-implemented，所以既不算 complete_exec，也不许记成 real。
    complete_stub = [f for f in complete if f["fidelity"] == "stub"]

    # 库级表：调用面 / 绑定面 / 缺口。
    stats = {}
    for lib in sorted({lib for libs, _ in files.values() for lib in libs}):
        called = sorted({fn for _, calls in files.values() for (l, fn) in calls if l == lib})
        bound_fns = sorted(fn for (l, fn) in bound if l == lib)
        stats[lib] = {
            "files": sum(1 for libs, _ in files.values() if lib in libs),
            "declared": len(headers.get(lib, ())),
            "called": called,
            "bound": [fn for fn in called if (lib, fn) in bound],
            "missing": [fn for fn in called if (lib, fn) not in bound],
            "fidelity": (
                "missing" if any((lib, fn) not in bound for fn in called)
                else worst([fid_of((lib, fn)) for fn in called])
            ),
            "unused_bound": [fn for fn in bound_fns if fn not in called],
        }

    # 下一个绑定：绑一个未绑符号能让多少个文件变完备。
    potential: dict[tuple[str, str], int] = collections.Counter()
    for f in per_file:
        if len(f["missing"]) == 1:
            potential[tuple(f["missing"][0])] += 1

    return {
        "schema": "ncoda.library-coverage/v1",
        "corpus": os.path.abspath(corpus),
        "corpus_size": len(files),
        "files_with_imports": sum(1 for libs, _ in files.values() if libs),
        "libraries_used": len(stats),
        "symbols_declared": sum(len(v) for v in headers.values()),
        "symbols_called": len({c for _, calls in files.values() for c in calls}),
        "symbols_bound": len(bound),
        "symbols_bound_local": len(bindings),
        "symbols_bound_plugin": len(plugins),
        "symbols_bound_stub": len([k for k in plugins if fidelity(plugins[k]) == "stub"]),
        "symbols_declared_twice": [f"{l}.{fn}" for l, fn in shadowed],
        "symbols_unbound": len(unbound_symbols),
        "undeclared_symbols": [f"{l}.{f}" for l, f in undeclared],
        "complete_exec": len(complete_exec),
        "complete_real": len(complete_real),
        "complete_any": len(complete),
        "complete_stub": len(complete_stub),
        "files": len(files),
        "next_bindings": [
            {"symbol": f"{l}.{fn}", "unlocks": n}
            for (l, fn), n in potential.most_common(20)
        ],
        "libraries": stats,
        "samples": {
            "complete_exec": [f["file"] for f in complete_exec[:10]],
            "complete_real": [f["file"] for f in complete_real[:10]],
        },
    }


def check(report: dict) -> list[str]:
    if not os.path.isfile(BASELINE):
        raise CannotJudge(f"baseline not readable: {BASELINE}")
    base = json.loads(read(BASELINE))
    problems = []
    if report["corpus_size"] != base.get("corpus_size"):
        raise CannotJudge(
            f"corpus size {report['corpus_size']} != baseline {base.get('corpus_size')}; "
            "re-pin the corpus and rebaseline with evidence")
    for key in ("complete_exec", "complete_real"):
        if report[key] < base.get(key, 0):
            problems.append(f"{key} 下降：{report[key]} < 基线 {base.get(key, 0)}")
    if report["symbols_unbound"] > base.get("symbols_unbound", 0):
        problems.append(f"未绑符号增加：{report['symbols_unbound']} > 基线 {base.get('symbols_unbound', 0)}")
    if report.get("symbols_declared_twice"):
        problems.append(
            f"符号同时被内建与插件声明（注册冲突）：{report['symbols_declared_twice'][:5]}")
    if report.get("symbols_bound_plugin", 0) < base.get("symbols_bound_plugin", 0):
        problems.append(
            f"插件符号减少：{report.get('symbols_bound_plugin')} < 基线 {base.get('symbols_bound_plugin', 0)}")
    # stub 只是占位：增加 = 把已实现的符号退回占位（或新增了未实现的绑定），
    # 这正是「把 unbound 改写成绿」的反向防护。
    if report.get("symbols_bound_stub", 0) > base.get("symbols_bound_stub", 0):
        problems.append(
            f"stub 符号增加：{report.get('symbols_bound_stub')} > 基线 {base.get('symbols_bound_stub', 0)}")
    if report["undeclared_symbols"]:
        problems.append(f"未声明符号：{report['undeclared_symbols'][:5]}")
    return problems


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--corpus", default=os.environ.get("NCODA_CORPUS", DEFAULT_CORPUS))
    ap.add_argument("--headers", default=HEADERS)
    ap.add_argument("--bindings", default=BINDINGS)
    ap.add_argument("--plugins", default=PLUGINS, help="进程外插件根目录（默认 ./plugins）")
    ap.add_argument("--json", action="store_true")
    ap.add_argument("--check", action="store_true", help="对 docs/library-coverage-baseline.json 判红")
    ap.add_argument("--write-baseline", action="store_true")
    ap.add_argument("--reason", default="")
    args = ap.parse_args(argv)

    try:
        report = analyze(args.corpus, args.headers, args.bindings, args.plugins)
    except CannotJudge as e:
        print(f"[library-coverage] 判不了：{e}", file=sys.stderr)
        return 3

    if args.write_baseline:
        if not args.reason.strip():
            print("[library-coverage] --write-baseline 需要 --reason", file=sys.stderr)
            return 2
        base = {
            "schema": report["schema"],
            "corpus_size": report["corpus_size"],
            "complete_exec": report["complete_exec"],
            "complete_real": report["complete_real"],
            "symbols_unbound": report["symbols_unbound"],
            "symbols_bound_plugin": report["symbols_bound_plugin"],
            "symbols_bound_stub": report["symbols_bound_stub"],
            "complete_stub": report["complete_stub"],
            "reason": args.reason.strip(),
        }
        with open(BASELINE, "w", encoding="utf-8") as fh:
            json.dump(base, fh, ensure_ascii=False, indent=1)
            fh.write("\n")
        print(f"[library-coverage] baseline 已写入 {BASELINE}")
        return 0

    if args.check:
        try:
            problems = check(report)
        except CannotJudge as e:
            print(f"[library-coverage] 判不了：{e}", file=sys.stderr)
            return 3
        if problems:
            for p in problems:
                print(f"[library-coverage] ✗ {p}", file=sys.stderr)
            return 1
        print("[library-coverage] ✓ 基线未退化"
              f"（complete_exec={report['complete_exec']} · complete_real={report['complete_real']}"
              f" · unbound_symbols={report['symbols_unbound']}"
              f" · plugin_bound={report.get('symbols_bound_plugin', 0)}）")
        return 0

    if args.json:
        json.dump(report, sys.stdout, ensure_ascii=False, indent=1)
        sys.stdout.write("\n")
        return 0

    print(f"corpus      : {report['corpus_size']} 例（有 import {report['files_with_imports']}）")
    print(f"libraries   : {report['libraries_used']} 被 import")
    print(f"symbols     : 调用 {report['symbols_called']} · 已绑 {report['symbols_bound']} · 未绑 {report['symbols_unbound']}")
    print(f"complete    : exec {report['complete_exec']} · real {report['complete_real']} · any {report['complete_any']}")
    if report.get("symbols_declared_twice"):
        print(f"conflict    : {report['symbols_declared_twice']}")
    if report["undeclared_symbols"]:
        print(f"undeclared  : {report['undeclared_symbols']}")
    print()
    print(f"{'library':34s} {'files':>5s} {'called':>6s} {'bound':>5s} {'miss':>4s}  fidelity")
    rows = sorted(report["libraries"].items(), key=lambda kv: -kv[1]["files"])
    for lib, st in rows[:25]:
        print(f"{lib:34s} {st['files']:5d} {len(st['called']):6d} {len(st['bound']):5d} {len(st['missing']):4d}  {st['fidelity']}")
    if len(rows) > 25:
        print(f"... 另有 {len(rows) - 25} 个库（--json 看全量）")
    print()
    print("下一个绑定（单符号解锁文件数）：")
    for item in report["next_bindings"][:10]:
        print(f"  {item['symbol']:44s} +{item['unlocks']} 文件")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
