#!/usr/bin/env python3
"""gen_sbom.py — CycloneDX 1.5 SBOM for the open core, from `go list -m -json all`.

Why hand-rolled: the open core has a deliberately tiny dependency set and the
release path must not require syft/gomod/network. This converter reads only
module path / version / goModSum and emits deterministic JSON.

Usage:
  python3 scripts/gen_sbom.py --version v0.7.0 --out dist/sbom.cdx.json
  python3 scripts/gen_sbom.py --check dist/sbom.cdx.json     # validate shape

Exit codes: 0 ok · 1 invalid SBOM (--check) · 2 usage / go list failure.

Honesty note: `goModSum` (h1:…) is the Go module-dirhash, not the zip's SHA-256,
so it is recorded as a property (`ncoda:goModSum`) instead of a CycloneDX
`hashes[].content` claim.
"""
from __future__ import annotations

import argparse
import datetime
import hashlib
import json
import subprocess
import sys
import urllib.parse
import uuid

NAMESPACE = uuid.UUID("6ba7b812-9dad-11d1-80b4-00c04fd430c8")  # fixed namespace


def go_modules(go: str = "go") -> list[dict]:
    proc = subprocess.run([go, "list", "-m", "-json", "all"],
                          capture_output=True, text=True)
    if proc.returncode != 0:
        raise SystemExit(f"go list -m failed: {proc.stderr.strip()[:400]}")
    dec, text, pos, out = json.JSONDecoder(), proc.stdout, 0, []
    while True:
        while pos < len(text) and text[pos].isspace():
            pos += 1
        if pos >= len(text):
            break
        obj, pos = dec.raw_decode(text, pos)
        out.append(obj)
    return out


def linked_modules(package: str, go: str = "go") -> set[str]:
    """Modules actually linked into the release binary under test.

    `go list -m all` also returns modules that exist only for tests (yaml.v3
    pulls kr/pretty, kr/text and check.v1). Reporting them as `required` would
    overstate the shipped artifact; they are emitted with scope `excluded` and
    linkage `test-or-module-graph`.
    """
    proc = subprocess.run(
        [go, "list", "-deps", "-f",
         "{{with .Module}}{{if not .Main}}{{.Path}}{{end}}{{end}}", package],
        capture_output=True, text=True)
    if proc.returncode != 0:
        raise SystemExit(f"go list -deps {package} failed: {proc.stderr.strip()[:400]}")
    return {line.strip() for line in proc.stdout.splitlines() if line.strip()}


def purl(path: str, version: str) -> str:
    return f"pkg:golang/{urllib.parse.quote(path, safe='/')}@{urllib.parse.quote(version, safe='')}"


def build(mods: list[dict], version: str, timestamp: str, linked: set[str]) -> dict:
    main = next((m for m in mods if m.get("Main")), None)
    if main is None:
        raise SystemExit("go list output has no main module")
    name = main["Path"]
    main_ref = purl(name, version)

    comps = []
    for m in sorted((m for m in mods if not m.get("Main")), key=lambda m: m["Path"]):
        v = m.get("Version") or "unknown"
        ref = purl(m["Path"], v)
        in_binary = m["Path"] in linked
        props = [{"name": "ncoda:goVersion", "value": m.get("GoVersion", "unknown")},
                 {"name": "ncoda:linkage",
                  "value": "binary" if in_binary else "test-or-module-graph"}]
        if m.get("Indirect"):
            props.append({"name": "ncoda:indirect", "value": "true"})
        if m.get("GoModSum"):
            props.append({"name": "ncoda:goModSum", "value": m["GoModSum"]})
        comps.append({
            "type": "library",
            "bom-ref": ref,
            "name": m["Path"],
            "version": v,
            "purl": ref,
            "scope": "required" if in_binary else "excluded",
            "properties": props,
        })

    serial_seed = json.dumps([name, version, [c["bom-ref"] for c in comps]],
                             sort_keys=True).encode()
    serial = "urn:uuid:" + str(uuid.uuid5(NAMESPACE, hashlib.sha256(serial_seed).hexdigest()))

    return {
        "bomFormat": "CycloneDX",
        "specVersion": "1.5",
        "serialNumber": serial,
        "version": 1,
        "metadata": {
            "timestamp": timestamp,
            "tools": [{"vendor": "nodecoda", "name": "gen_sbom.py", "version": "1"}],
            "component": {
                "type": "application",
                "bom-ref": main_ref,
                "name": name,
                "version": version,
                "purl": main_ref,
            },
        },
        "components": comps,
        "dependencies": [{
            "ref": main_ref,
            "dependsOn": [c["bom-ref"] for c in comps if c["scope"] == "required"],
        }],
    }


def check(path: str) -> int:
    try:
        with open(path, encoding="utf-8") as fh:
            doc = json.load(fh)
    except (OSError, json.JSONDecodeError) as exc:
        print(f"✗ SBOM 不可读：{exc}", file=sys.stderr)
        return 1
    problems = []
    if doc.get("bomFormat") != "CycloneDX":
        problems.append("bomFormat != CycloneDX")
    if doc.get("specVersion") != "1.5":
        problems.append("specVersion != 1.5")
    meta = doc.get("metadata") or {}
    if not (meta.get("component") or {}).get("name"):
        problems.append("metadata.component.name missing")
    comps = doc.get("components") or []
    if not comps:
        problems.append("components empty（零依赖也要留空数组，但主模块必须存在）")
    refs = {c.get("bom-ref") for c in comps}
    if len(refs) != len(comps):
        problems.append("duplicate bom-ref")
    deps = (doc.get("dependencies") or [{}])[0].get("dependsOn") or []
    missing = sorted(set(deps) - refs)
    if missing:
        problems.append(f"dependsOn 指向不存在的组件：{missing[:3]}")
    for p in problems:
        print(f"✗ {p}", file=sys.stderr)
    print(f"{'✗' if problems else '✓'} SBOM：{len(comps)} 组件 · {path}")
    return 1 if problems else 0


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--version", default="dev", help="主模块版本（发布时传 tag）")
    ap.add_argument("--out", default="", help="输出文件（默认 stdout）")
    ap.add_argument("--timestamp", default="", help="固定时间戳（可复现构建）")
    ap.add_argument("--check", default="", help="校验既有 SBOM 文件")
    ap.add_argument("--go", default="go")
    ap.add_argument("--package", default="./cmd/nclang-compile",
                    help="用于判定 linkage 的发布包（默认 ./cmd/nclang-compile）")
    args = ap.parse_args()

    if args.check:
        return check(args.check)

    ts = args.timestamp or datetime.datetime.now(datetime.timezone.utc) \
        .replace(microsecond=0).isoformat().replace("+00:00", "Z")
    try:
        mods = go_modules(args.go)
        doc = build(mods, args.version, ts, linked_modules(args.package, args.go))
    except SystemExit as exc:
        print(f"✗ {exc}", file=sys.stderr)
        return 2
    text = json.dumps(doc, ensure_ascii=False, indent=2) + "\n"
    if args.out:
        with open(args.out, "w", encoding="utf-8") as fh:
            fh.write(text)
        print(f"✓ SBOM → {args.out}（{len(doc['components'])} 组件）", file=sys.stderr)
    else:
        sys.stdout.write(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
