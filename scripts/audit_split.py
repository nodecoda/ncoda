#!/usr/bin/env python3
"""audit_split.py — enforce the open-core boundary as a repeatable local gate.

The split is only durable if its invariants are checked, not remembered. Each
check below corresponds to an acceptance criterion of the O2 split:

  S1 structure     no `internal/` tree, no `platforms/coze*` (private backends)
  S2 dependencies  dify decompile + nclang-compile never reach coze/cozebiz/
                   cozestudio/eino/ncoda-commercial
  S3 mentions      no Go source references the commercial module
  S4 absolute      no tracked `/home/dev/` path (publishable tree)
  S5 license       LICENSE + NOTICE present

Exit codes (same four-state convention as the governance repo):
  0 pass · 1 violation (named) · 2 usage · 3 cannot judge (go list / git failed)

Usage:
  python3 scripts/audit_split.py [--root .] [--skip-deps]
"""
from __future__ import annotations

import argparse
import os
import subprocess
import sys

FORBIDDEN_DEPS = ("ncoda-commercial", "platforms/cozebiz", "platforms/cozestudio", "eino")
DEP_PACKAGES = ("./platforms/dify/decompile", "./cmd/nclang-compile")


class CannotJudge(Exception):
    pass


def check_structure(root: str) -> list[str]:
    problems = []
    if os.path.isdir(os.path.join(root, "internal")):
        problems.append("S1 开放树出现 internal/（闭源源码不应在此）")
    platforms = os.path.join(root, "platforms")
    if os.path.isdir(platforms):
        for name in sorted(os.listdir(platforms)):
            if name.startswith("coze"):
                problems.append(f"S1 开放树出现 platforms/{name}（闭源平台）")
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
    problems = []
    for package in DEP_PACKAGES:
        deps = go_deps(root, package)
        for needle in FORBIDDEN_DEPS:
            hit = sorted(d for d in deps if needle in d)
            if hit:
                problems.append(f"S2 {package} → {needle}：{hit[:3]}")
    return problems


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", default=os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    ap.add_argument("--skip-deps", action="store_true", help="跳过 go list 检查（测试/离线用）")
    args = ap.parse_args()
    try:
        problems = (check_structure(args.root) + check_mentions(args.root)
                    + check_absolute(args.root) + check_license(args.root))
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
    print("[audit-split] ✓ S1 结构 · S2 依赖 · S3 引用 · S4 绝对路径 · S5 许可")
    return 0


if __name__ == "__main__":
    sys.exit(main())
