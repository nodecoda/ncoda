#!/usr/bin/env python3
"""audit_split.py — enforce the open-core boundary as a repeatable local gate.

The split is only durable if its invariants are checked, not remembered. Each
check below corresponds to an acceptance criterion of the O2 split:

  S1 structure     no `internal/` tree, no `platforms/*` (private backends)
  S2 dependencies  nclang-compile (the open distribution's only binary) never
                   reaches a platform adapter (dify/coze/coze-biz), the closed
                   tree, eino, or ncoda-commercial
  S3 mentions      no Go source references the commercial module
  S4 absolute      no tracked `/home/dev/` path (publishable tree)
  S5 license       LICENSE + NOTICE present
  S6 identity      no platform name in a kernel identifier, except the
                   registered residuals in docs/PLATFORM-IDENTITY-RESIDUALS.yaml
  S7 purge         the platform word is gone from the whole public tree —
                   code, comments, docs and paths (the gate names it itself)

Exit codes (same four-state convention as the governance repo):
  0 pass · 1 violation (named) · 2 usage · 3 cannot judge (go list / git failed)

Usage:
  python3 scripts/audit_split.py [--root .] [--skip-deps]
"""
from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys

# Global needles: any dependency path containing one of these is a violation.
FORBIDDEN_DEPS = ("ncoda-commercial", "eino")
# Module-owned package prefixes that belong to an adapter, not the language.
FORBIDDEN_MODULE_SEGMENTS = ("/internal/", "/platforms/", "/pkg/backend/dify",
                             "/pkg/backend/llmprovider", "/pkg/backend/llmprojection")
MODULE_PREFIX = "github.com/nodecoda/ncoda/"
DEP_PACKAGES = ("./cmd/nclang-compile",)


# Platform tokens that must not appear in an open-kernel identifier.
# Case-insensitive substring match after comments and literals are stripped:
# `cozeOnErrorMode`, `isDifyProfile`, `platformDify` are all hits.
PLATFORM_IDENT_TOKENS = ("dify", "coze")
IDENTITY_LEDGER = os.path.join("docs", "PLATFORM-IDENTITY-RESIDUALS.yaml")

# S7: the platform identity that has been moved out of the open repository.
# Case-insensitive whole-word match: "modify" must not trip it, "Dify-only"
# must. Only the gate itself is exempt — it has to name what it forbids.
PURGED_WORD_RE = re.compile(r"\bdify\b", re.IGNORECASE)
PURGE_GATE_FILES = {os.path.join("scripts", "audit_split.py"),
                    os.path.join("scripts", "test_audit_split.py")}
IDENT_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")


class CannotJudge(Exception):
    pass


def _strip_go_comments_and_literals(src: str) -> str:
    """Drop comments and string/rune literals so only code identifiers remain.

    A platform name inside a comment or a message string is prose, not a
    dependency; an identifier is the dependency (it is what links the kernel
    to a platform's semantics).
    """
    src = re.sub(r"/\*.*?\*/", " ", src, flags=re.S)
    src = re.sub(r"//[^\n]*", " ", src)
    src = re.sub(r'`[^`]*`', '""', src)
    src = re.sub(r'"(?:[^"\\\n]|\\.)*"', '""', src)
    src = re.sub(r"'(?:[^'\\\n]|\\.)*'", "''", src)
    return src


def load_identity_ledger(root: str) -> dict[str, dict]:
    try:
        import yaml  # noqa: PLC0415 — optional at import time, required for S6
    except ImportError as exc:  # pragma: no cover
        raise CannotJudge(f"S6 需要 PyYAML：{exc}") from exc
    path = os.path.join(root, IDENTITY_LEDGER)
    if not os.path.isfile(path):
        raise CannotJudge(f"S6 缺台账 {IDENTITY_LEDGER}")
    with open(path, encoding="utf-8") as fh:
        doc = yaml.safe_load(fh) or {}
    allowed: dict[str, dict] = {}
    for item in doc.get("items") or []:
        for ident in item.get("identifiers") or []:
            allowed[ident] = item
    return allowed


def check_platform_identity(root: str) -> list[str]:
    """S6: the kernel names no platform, except registered residuals.

    Two-way check: an unregistered platform identifier fails (new leak), and a
    registered identifier that no longer exists fails too (stale ledger). The
    ledger therefore shrinks with the code instead of rotting.
    """
    allowed = load_identity_ledger(root)
    found: dict[str, str] = {}
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in (".git", "vendor")]
        for fn in filenames:
            if not fn.endswith(".go"):
                continue
            path = os.path.join(dirpath, fn)
            try:
                with open(path, encoding="utf-8", errors="replace") as fh:
                    code = _strip_go_comments_and_literals(fh.read())
            except OSError:
                continue
            rel = os.path.relpath(path, root)
            for ident in set(IDENT_RE.findall(code)):
                low = ident.lower()
                if any(tok in low for tok in PLATFORM_IDENT_TOKENS):
                    found.setdefault(ident, rel)
    problems = []
    # File and directory names are part of the published surface: a fixture
    # called `acme-x.ncoda` ships the platform name even if no identifier does.
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in (".git", "vendor")]
        for name in dirnames + filenames:
            low = name.lower()
            if any(tok in low for tok in PLATFORM_IDENT_TOKENS):
                rel = os.path.relpath(os.path.join(dirpath, name), root)
                problems.append(f"S6 {rel}: 路径名拼进平台名")
    for ident, rel in sorted(found.items()):
        if ident not in allowed:
            problems.append(f"S6 {rel}: 标识符 {ident} 拼进平台名（未登记）")
    for ident, item in sorted(allowed.items()):
        if ident not in found:
            problems.append(
                f"S6 台账过期：{item.get('id', '?')} 登记的 {ident} 已不在代码里（删掉该条）")
    return problems


def check_purged_words(root: str) -> list[str]:
    """S7: the purged platform word appears nowhere in the public tree.

    S6 only looks at identifiers (a real dependency). Prose is not a
    dependency, but it is how a removed platform quietly becomes the project's
    default vocabulary again — the word comes back first, the code follows.
    This check keeps the whole tree clean: source, comments, docs, fixtures and
    file names. The gate scripts are exempt because they must name the word.
    """
    problems = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames
                       if d not in (".git", "vendor", "__pycache__", "node_modules")]
        for name in dirnames + filenames:
            rel = os.path.relpath(os.path.join(dirpath, name), root)
            if rel in PURGE_GATE_FILES:
                continue
            if PURGED_WORD_RE.search(name):
                problems.append(f"S7 {rel}: 路径名带平台词")
        for fn in filenames:
            rel = os.path.relpath(os.path.join(dirpath, fn), root)
            if rel in PURGE_GATE_FILES:
                continue
            path = os.path.join(dirpath, fn)
            try:
                with open(path, "rb") as fh:
                    blob = fh.read()
            except OSError:
                continue
            if b"\x00" in blob[:8192]:  # binary artifact
                continue
            text = blob.decode("utf-8", errors="replace")
            for lineno, line in enumerate(text.splitlines(), 1):
                if PURGED_WORD_RE.search(line):
                    problems.append(f"S7 {rel}:{lineno}: 平台词仍在公开树里")
    return problems


def check_structure(root: str) -> list[str]:
    problems = []
    if os.path.isdir(os.path.join(root, "internal")):
        problems.append("S1 开放树出现 internal/（闭源源码不应在此）")
    platforms = os.path.join(root, "platforms")
    if os.path.isdir(platforms):
        for name in sorted(os.listdir(platforms)):
            problems.append(f"S1 开放树出现 platforms/{name}（平台适配器）")
    return problems


def check_mentions(root: str) -> list[str]:
    problems = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in (".git", "vendor")]
        for fn in filenames:
            if not fn.endswith(".go"):
                continue
            path = os.path.join(dirpath, fn)
            try:
                with open(path, encoding="utf-8", errors="replace") as fh:
                    for n, line in enumerate(fh, 1):
                        if "ncoda-commercial" in line:
                            rel = os.path.relpath(path, root)
                            problems.append(f"S3 {rel}:{n} 引用闭源模块")
            except OSError:
                continue
    return problems


def check_absolute(root: str) -> list[str]:
    proc = subprocess.run(["git", "grep", "-n", "/home/dev/", "--", ".",
                              ":(exclude)scripts/audit_split.py"],
                          cwd=root, capture_output=True, text=True)
    if proc.returncode == 0:
        hits = [ln for ln in proc.stdout.splitlines() if ln.strip()]
        return [f"S4 绝对路径：{ln}" for ln in hits[:10]] + (
            [f"S4 … 另有 {len(hits) - 10} 处"] if len(hits) > 10 else [])
    if proc.returncode == 1:
        return []
    raise CannotJudge(f"git grep 失败（rc={proc.returncode}）：{proc.stderr.strip()[:200]}")


def check_license(root: str) -> list[str]:
    missing = [f for f in ("LICENSE", "NOTICE") if not os.path.isfile(os.path.join(root, f))]
    return [f"S5 缺 {f}（公开发行必需）" for f in missing]


def go_deps(root: str, package: str) -> set[str]:
    proc = subprocess.run(["go", "list", "-deps", package],
                          cwd=root, capture_output=True, text=True)
    if proc.returncode != 0:
        raise CannotJudge(f"go list -deps {package} 失败：{proc.stderr.strip()[:300]}")
    return set(proc.stdout.split())


def check_deps(root: str) -> list[str]:
    """The open binary must link no platform adapter and no closed tree.

    Stdlib packages legitimately live under `internal/` (internal/abi, …), so
    the segment rules only apply to this module's own packages. A new platform
    adapter in the open repo therefore fails here by construction, not by
    someone remembering to update a list.
    """
    problems = []
    for package in DEP_PACKAGES:
        deps = go_deps(root, package)
        for needle in FORBIDDEN_DEPS:
            hit = sorted(d for d in deps if needle in d)
            if hit:
                problems.append(f"S2 {package} → {needle}：{hit[:3]}")
        owned = sorted(d for d in deps if d.startswith(MODULE_PREFIX))
        for d in owned:
            for seg in FORBIDDEN_MODULE_SEGMENTS:
                if seg in d:
                    problems.append(f"S2 {package} → 平台适配器/闭源树 {d}")
                    break
    return problems


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", default=os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    ap.add_argument("--skip-deps", action="store_true", help="跳过 go list 检查（测试/离线用）")
    args = ap.parse_args()
    try:
        problems = (check_structure(args.root) + check_mentions(args.root)
                    + check_absolute(args.root) + check_license(args.root)
                    + check_platform_identity(args.root)
                    + check_purged_words(args.root))
        if not args.skip_deps:
            problems += check_deps(args.root)
    except CannotJudge as exc:
        print(f"[audit-split] 判不了：{exc}", file=sys.stderr)
        return 3
    if problems:
        print(f"[audit-split] ✗ {len(problems)} 处分包不变量违规：", file=sys.stderr)
        for p in problems:
            print(f"    {p}", file=sys.stderr)
        return 1
    print("[audit-split] ✓ S1 结构 · S2 依赖 · S3 引用 · S4 绝对路径 · S5 许可 · S6 平台身份 · S7 平台词清零")
    return 0


if __name__ == "__main__":
    sys.exit(main())
