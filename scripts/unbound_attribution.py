#!/usr/bin/env python3
"""unbound_attribution.py — 未绑符号的**归因账**。

不变量：每一个 unbound 符号所在的库都必须在 docs/UNBOUND-ATTRIBUTION.md 的
归因表里有一个闭集 class。理由：unbound 不等于失败，但**不许无法解释**。
新增未绑库不写归因 => 判红（rc=1），而不是默默留在“其他”。

判据：
  A1 归因表可解析，schema 精确，class ∈ 闭集
  A2 每个含 unbound 符号的库都有 row（缺失 => unclassified）
  A3 棘轮：unclassified 符号数不得高于基线
  A4 统计与 library_coverage 的 unbound 总数一致（口径漂移 => 判不了）
  A5 符号级覆盖：`symbols: {fn: {class, evidence}}` 优先于库级；覆盖的
     library/function 必须真实存在（写错 => rc=2，不静默失效）
读不到语料/表 => rc 3（判不了 ≠ 通过）；参数/表非法 => rc 2。

用法：
  python3 scripts/unbound_attribution.py            # 人读
  python3 scripts/unbound_attribution.py --json     # 机器读
  python3 scripts/unbound_attribution.py --check    # 对基线判红
  python3 scripts/unbound_attribution.py --write-baseline --reason "…"
"""
from __future__ import annotations

import argparse
import json
import os
import re
import sys

try:
    import yaml
except Exception as e:  # pragma: no cover
    print(f"[unbound-attribution] 判不了：PyYAML 不可用（{e}）", file=sys.stderr)
    sys.exit(3)

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import library_coverage as lc  # noqa: E402

ROOT = os.path.dirname(HERE)
TABLE = os.path.join(ROOT, "docs", "UNBOUND-ATTRIBUTION.md")
BASELINE = os.path.join(ROOT, "docs", "unbound-attribution-baseline.json")

SCHEMA = "ncoda.unbound-attribution/v1"
CLASSES = {
    "local-todo",
    "remote-api",
    "server-state",
    "schema-unknown",
    "env-missing",
}
UNCLASSIFIED = "unclassified"


TEMPLATE_EVIDENCE = ("corpus fallback", "快照未收录")


def require_real_evidence(where: str, ev: str) -> None:
    """证据不能是模板占位符：那不是「凭什么这么判」，只是没判。"""
    low = ev.lower()
    for needle in TEMPLATE_EVIDENCE:
        if needle in low:
            raise BadTable(
                f"{where}: evidence is a template placeholder ({needle!r}); "
                f"cite an actual header/corpus fact")


class BadTable(RuntimeError):
    """归因表非法（rc=2）。"""


class CannotJudge(RuntimeError):
    """读不到输入（rc=3）。"""


def read(path: str) -> str:
    try:
        with open(path, encoding="utf-8") as fh:
            return fh.read()
    except OSError as e:
        raise CannotJudge(f"not readable: {path} ({e})")


def load_table(path: str = TABLE) -> dict[str, dict]:
    if not os.path.isfile(path):
        raise CannotJudge(f"attribution table not readable: {path}")
    body = read(path)
    m = re.search(r"```yaml\n(.*?)\n```", body, re.S)
    if not m:
        raise BadTable(f"{path}: no ```yaml block")
    doc = yaml.safe_load(m.group(1)) or {}
    if doc.get("schema") != SCHEMA:
        raise BadTable(f"{path}: schema = {doc.get('schema')!r}, want {SCHEMA}")
    libs = doc.get("libraries") or {}
    if not isinstance(libs, dict):
        raise BadTable(f"{path}: libraries must be a mapping")
    # 空表是合法的：所有未绑符号都会落进 unclassified，由棘轮判红。
    out = {}
    for lib, row in libs.items():
        if not isinstance(row, dict):
            raise BadTable(f"{path}: {lib}: row must be a mapping")
        cls = str(row.get("class") or "")
        if cls not in CLASSES:
            raise BadTable(f"{path}: {lib}: class {cls!r} not in {sorted(CLASSES)}")
        ev = str(row.get("evidence") or "").strip()
        if not ev:
            raise BadTable(f"{path}: {lib}: evidence is required")
        require_real_evidence(f"{path}: {lib}", ev)
        overrides = {}
        raw = row.get("symbols")
        if raw is not None:
            if not isinstance(raw, dict):
                raise BadTable(f"{path}: {lib}: symbols must be a mapping")
            for fn, sym in raw.items():
                if not isinstance(sym, dict):
                    raise BadTable(f"{path}: {lib}.{fn}: symbol row must be a mapping")
                scls = str(sym.get("class") or "")
                if scls not in CLASSES:
                    raise BadTable(
                        f"{path}: {lib}.{fn}: class {scls!r} not in {sorted(CLASSES)}")
                sev = str(sym.get("evidence") or "").strip()
                if not sev:
                    raise BadTable(f"{path}: {lib}.{fn}: evidence is required")
                require_real_evidence(f"{path}: {lib}.{fn}", sev)
                overrides[str(fn)] = {"class": scls, "evidence": sev}
        out[str(lib)] = {"class": cls, "evidence": ev, "symbols": overrides}
    return out


def validate_overrides(table: dict[str, dict], headers: dict[str, set[str]]) -> None:
    """符号级覆盖必须是真实存在的 library/function（typo 不能静默变成库级）。"""
    for lib, row in table.items():
        for fn in row.get("symbols") or {}:
            if lib not in headers:
                raise BadTable(f"override {lib}.{fn}: library not in capability headers")
            if fn not in headers[lib]:
                raise BadTable(f"override {lib}.{fn}: function not declared in header")


def classify(rep: dict, table: dict[str, dict]) -> dict:
    """把 coverage 报告按归因表分类（纯函数，便于判据用假报告打红）。

    符号级覆盖优先于库级：一个库可以同时贡献多个 class（例如
    `suchuang_video_compose` 的纯函数是 local-todo，状态机是 server-state）。
    """
    per_class: dict[str, dict] = {c: {"libraries": set(), "symbols": 0} for c in sorted(CLASSES)}
    per_class[UNCLASSIFIED] = {"libraries": set(), "symbols": 0}
    rows = []
    symbol_rows = []
    total = 0
    for lib, st in sorted(rep["libraries"].items()):
        if not st["missing"]:
            continue
        row = table.get(lib) or {"class": UNCLASSIFIED, "evidence": "", "symbols": {}}
        overrides = row.get("symbols") or {}
        classes = {}
        for fn in st["missing"]:
            sym = overrides.get(fn)
            cls = sym["class"] if sym else row["class"]
            ev = sym["evidence"] if sym else row["evidence"]
            classes[fn] = cls
            total += 1
            per_class[cls]["symbols"] += 1
            per_class[cls]["libraries"].add(lib)
            symbol_rows.append({
                "library": lib,
                "symbol": fn,
                "class": cls,
                "evidence": ev,
            })
        rows.append({
            "library": lib,
            "class": row["class"],
            "symbols": st["missing"],
            "symbol_classes": classes,
            "files": st["files"],
            "evidence": row["evidence"],
        })
    if total != rep["symbols_unbound"]:
        raise CannotJudge(
            f"unbound symbol count drifted: table sees {total}, coverage says {rep['symbols_unbound']}")
    return {
        "schema": "ncoda.unbound-attribution-report/v1",
        "unbound_symbols": total,
        "libraries_with_unbound": len(rows),
        "by_class": {
            c: {"libraries": len(v["libraries"]), "symbols": v["symbols"]}
            for c, v in per_class.items()
        },
        "unclassified": {
            "libraries": sorted(per_class[UNCLASSIFIED]["libraries"]),
            "symbols": per_class[UNCLASSIFIED]["symbols"],
            "symbol_rows": [s for s in symbol_rows if s["class"] == UNCLASSIFIED],
        },
        "rows": rows,
        "symbol_rows": symbol_rows,
    }


def analyze(corpus: str | None = None, table_path: str = TABLE) -> dict:
    table = load_table(table_path)
    validate_overrides(table, lc.load_headers(lc.HEADERS))
    try:
        cov = lc.analyze(corpus or lc.DEFAULT_CORPUS)
    except lc.CannotJudge as e:  # 语料/header/bindings 读不到 => 判不了（rc=3）
        raise CannotJudge(str(e))
    return classify(cov, table)


def check(report: dict) -> list[str]:
    if not os.path.isfile(BASELINE):
        raise CannotJudge(f"baseline not readable: {BASELINE}")
    base = json.loads(read(BASELINE))
    problems = []
    got = report["unclassified"]["symbols"]
    want = int(base.get("unclassified_symbols", 0))
    if got > want:
        problems.append(f"未归因符号增加：{got} > 基线 {want}")
    for cls, base_n in (base.get("by_class") or {}).items():
        now = report["by_class"].get(cls, {}).get("symbols", 0)
        if now > base_n:
            problems.append(f"{cls} 符号增加：{now} > 基线 {base_n}")
    return problems


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--corpus", default=os.environ.get("NCODA_CORPUS", lc.DEFAULT_CORPUS))
    ap.add_argument("--table", default=TABLE)
    ap.add_argument("--json", action="store_true")
    ap.add_argument("--check", action="store_true")
    ap.add_argument("--write-baseline", action="store_true")
    ap.add_argument("--reason", default="")
    args = ap.parse_args(argv)

    if args.write_baseline and not args.reason.strip():
        print("[unbound-attribution] --write-baseline 需要 --reason", file=sys.stderr)
        return 2

    try:
        report = analyze(args.corpus, args.table)
    except (BadTable, CannotJudge) as e:
        cls = BadTable if isinstance(e, BadTable) else CannotJudge
        print(f"[unbound-attribution] {'表非法' if cls is BadTable else '判不了'}：{e}", file=sys.stderr)
        return 2 if cls is BadTable else 3

    if args.write_baseline:
        # 闸门：未归因符号存在时拒绝写基线。否则一次误删归因行就会把
        # 「判不了」钉成新基线（等于放宽判据），之后 --check 永远绿。
        if report["unclassified"]["symbols"]:
            syms = ", ".join(
                f"{s['library']}.{s['symbol']}"
                for s in report["unclassified"]["symbol_rows"])
            print(f"[unbound-attribution] 拒绝写基线：仍有 "
                  f"{report['unclassified']['symbols']} 个未归因符号（{syms}）",
                  file=sys.stderr)
            return 2
        base = {
            "schema": report["schema"],
            "unbound_symbols": report["unbound_symbols"],
            "unclassified_symbols": report["unclassified"]["symbols"],
            "by_class": {k: v["symbols"] for k, v in report["by_class"].items()},
            "reason": args.reason.strip(),
        }
        with open(BASELINE, "w", encoding="utf-8") as fh:
            json.dump(base, fh, ensure_ascii=False, indent=1)
            fh.write("\n")
        print(f"[unbound-attribution] baseline 已写入 {BASELINE}")
        return 0

    if args.check:
        try:
            problems = check(report)
        except CannotJudge as e:
            print(f"[unbound-attribution] 判不了：{e}", file=sys.stderr)
            return 3
        if problems:
            for p in problems:
                print(f"[unbound-attribution] ✗ {p}", file=sys.stderr)
            return 1
        print("[unbound-attribution] ✓ 归因账未退化"
              f"（unbound={report['unbound_symbols']}"
              f" · unclassified={report['unclassified']['symbols']}）")
        for cls, v in report["by_class"].items():
            print(f"  {cls:15s} libs={v['libraries']:3d} symbols={v['symbols']:3d}")
        return 0

    if args.json:
        json.dump(report, sys.stdout, ensure_ascii=False, indent=1)
        print()
        return 0

    print(f"unbound symbols: {report['unbound_symbols']} in {report['libraries_with_unbound']} libraries")
    for cls, v in report["by_class"].items():
        print(f"  {cls:15s} libs={v['libraries']:3d} symbols={v['symbols']:3d}")
    if report["unclassified"]["libraries"]:
        print("  unclassified:", ", ".join(report["unclassified"]["libraries"]))
    print("\ntop local-todo (可做的下一步):")
    todo = []
    for r in report["rows"]:
        fns = [fn for fn, cls in r["symbol_classes"].items() if cls == "local-todo"]
        if fns:
            todo.append((r["files"], r["library"], fns))
    todo.sort(key=lambda t: (-t[0], t[1]))
    for files, lib, fns in todo[:12]:
        print(f"  {files:4d} files {lib:24s} {', '.join(fns[:4])}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
