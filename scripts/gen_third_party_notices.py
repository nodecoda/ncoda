#!/usr/bin/env python3
"""gen_third_party_notices.py — reproducible third-party notices for the release binary.

The open-core binary links third-party modules. Apache-2.0 §4(d) requires
carrying upstream NOTICE files, and MIT/BSD require the copyright notice and
permission text itself. So this file is a compliance artifact, not prose: it is
generated from the actual dependency graph of `./cmd/nclang-compile`.

Usage:
  python3 scripts/gen_third_party_notices.py --out THIRD-PARTY-NOTICES.md
  python3 scripts/gen_third_party_notices.py --check THIRD-PARTY-NOTICES.md

Exit codes: 0 ok · 1 drift / unknown license · 2 usage · 3 cannot judge.
"""
from __future__ import annotations

import argparse
import os
import subprocess
import sys

PACKAGE = "./cmd/nclang-compile"


class CannotJudge(Exception):
    pass


def linked_modules(go: str = "go") -> list[dict]:
    fmt = "{{with .Module}}{{if not .Main}}{{.Path}}|{{.Version}}|{{.Dir}}|{{if .Replace}}yes{{end}}{{end}}{{end}}"
    proc = subprocess.run([go, "list", "-deps", "-f", fmt, PACKAGE],
                          capture_output=True, text=True)
    if proc.returncode != 0:
        raise CannotJudge(f"go list -deps {PACKAGE} 失败：{proc.stderr.strip()[:300]}")
    seen, out = set(), []
    for line in proc.stdout.splitlines():
        if not line.strip():
            continue
        path, version, directory, replaced = (line.split("|") + ["", "", "", ""])[:4]
        if not path or path in seen:
            continue
        seen.add(path)
        out.append({"path": path, "version": version, "dir": directory, "replaced": replaced == "yes"})
    return sorted(out, key=lambda m: m["path"])


def license_file(directory: str) -> str:
    try:
        names = os.listdir(directory)
    except OSError:
        return ""
    for name in sorted(names):
        if name.lower().startswith(("license", "copying", "licence")):
            return os.path.join(directory, name)
    return ""


def classify(path: str) -> str:
    try:
        with open(path, encoding="utf-8", errors="replace") as fh:
            text = fh.read()
    except OSError:
        return "UNKNOWN"
    head = text[:4000]
    has_apache = "Apache License" in head and "Version 2.0" in head
    has_mit = "MIT License" in head or "Permission is hereby granted, free of charge" in head
    if has_apache and has_mit:
        # e.g. gopkg.in/yaml.v3: MIT for the libyaml port, Apache-2.0 elsewhere.
        return "MIT AND Apache-2.0"
    if has_apache:
        return "Apache-2.0"
    if has_mit:
        return "MIT"
    if "BSD 3-Clause" in head or ("Redistribution and use in source and binary forms" in head
                                  and "Neither the name" in head):
        return "BSD-3-Clause"
    if "Redistribution and use in source and binary forms" in head:
        return "BSD-2-Clause"
    if "ISC License" in head or "Permission to use, copy, modify, and/or distribute" in head:
        return "ISC"
    return "UNKNOWN"


def notice_file(directory: str) -> str:
    for name in sorted(os.listdir(directory)) if os.path.isdir(directory) else []:
        if name.lower() == "notice":
            return os.path.join(directory, name)
    return ""


def render(mods: list[dict]) -> tuple[str, list[str]]:
    rows, problems, notices, texts = [], [], [], []
    seen_texts: set[str] = set()
    for m in mods:
        lic_path = license_file(m["dir"])
        lic = classify(lic_path) if lic_path else "UNKNOWN"
        if lic == "UNKNOWN":
            problems.append(f"无法识别许可证：{m['path']}（{m['dir']}）")
        version = m["version"] or ("(local sibling; pinned at release)" if m["replaced"] else "(devel)")
        rows.append(f"| `{m['path']}` | {version} | {lic} |")
        if lic_path:
            try:
                with open(lic_path, encoding="utf-8", errors="replace") as fh:
                    body = fh.read().strip()
            except OSError as exc:
                problems.append(f"LICENSE 读不到：{lic_path}（{exc}）")
                body = ""
            if body and body not in seen_texts:
                seen_texts.add(body)
                texts.append((m["path"], lic, body))
        npath = notice_file(m["dir"])
        if npath:
            try:
                with open(npath, encoding="utf-8", errors="replace") as fh:
                    notices.append((m["path"], lic, fh.read().strip()))
            except OSError as exc:
                problems.append(f"NOTICE 读不到：{npath}（{exc}）")
    text = [
        "# Third-party notices",
        "",
        "The open-core release binary links the modules below. This file is",
        "generated, not hand-written: regenerate it with",
        "`python3 scripts/gen_third_party_notices.py --out THIRD-PARTY-NOTICES.md`.",
        "",
        "The ncoda project itself is licensed under Apache-2.0 (see `LICENSE`).",
        "",
        "| Module | Version | License |",
        "| --- | --- | --- |",
        *rows,
        "",
    ]
    if texts:
        text += ["## License texts", "",
                 "Verbatim from each module's LICENSE file (`$GOMODCACHE`). MIT and BSD",
                 "require the copyright notice itself, not just its name.", ""]
        for path, lic, body in texts:
            text += [f"### `{path}` ({lic})", "", "```text", body, "```", ""]
    if notices:
        text += ["## Upstream NOTICE texts", ""]
        for path, lic, body in notices:
            text += [f"### `{path}` ({lic})", "", "```text", body, "```", ""]
    return "\n".join(text).rstrip() + "\n", problems


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default="")
    ap.add_argument("--check", default="", metavar="PATH")
    ap.add_argument("--go", default="go")
    args = ap.parse_args()
    if not args.out and not args.check:
        ap.print_usage(sys.stderr)
        return 2
    try:
        mods = linked_modules(args.go)
    except CannotJudge as exc:
        print(f"[notices] 判不了：{exc}", file=sys.stderr)
        return 3
    if not mods:
        print("[notices] 判不了：依赖图为空（分母 0 ≠ 通过）", file=sys.stderr)
        return 3
    text, problems = render(mods)
    if problems:
        for p in problems:
            print(f"✗ {p}", file=sys.stderr)
        return 1
    if args.check:
        try:
            with open(args.check, encoding="utf-8") as fh:
                current = fh.read()
        except OSError as exc:
            print(f"✗ 读不到 {args.check}：{exc}", file=sys.stderr)
            return 1
        if current != text:
            print(f"✗ {args.check} 已漂移：重跑 gen_third_party_notices.py --out {args.check}",
                  file=sys.stderr)
            return 1
        print(f"[notices] ✓ {len(mods)} 个链接模块 · {args.check} 未漂移")
        return 0
    with open(args.out, "w", encoding="utf-8") as fh:
        fh.write(text)
    print(f"[notices] ✓ {len(mods)} 个链接模块 → {args.out}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
