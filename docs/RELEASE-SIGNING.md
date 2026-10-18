# Release artifacts, SBOM, and signing

`make dist VERSION=vX.Y.Z` produces, under `/tmp/ncoda-dist-vX.Y.Z/`:

| File | What it is |
| --- | --- |
| `nclang-vX.Y.Z-<os>-<arch>[.exe]` | 6-platform `nclang-compile` binaries (linux/darwin/windows × amd64/arm64) |
| `SHA256SUMS` | SHA-256 of every binary, the two SBOM files, `LICENSE`, `NOTICE` and `THIRD-PARTY-NOTICES.md` |
| `LICENSE`, `NOTICE` | Apache-2.0 license and attribution shipped with the binaries (§4(d)) |
| `THIRD-PARTY-NOTICES.md` | generated: linked modules + their verbatim LICENSE/NOTICE texts (MIT/BSD require the notice itself) |
| `sbom.cdx.json` | CycloneDX 1.5 SBOM (one component per Go module) |
| `SBOM.txt` | `go version -m` build info of the linux/amd64 binary |
| `SHA256SUMS.cosign.bundle` | keyless cosign bundle (only when signing is enabled) |

## SBOM

`scripts/gen_sbom.py` converts `go list -m -json all` into CycloneDX 1.5 with no
external tooling and no network access. Regenerate or validate:

```bash
make sbom VERSION=vX.Y.Z                       # -> $DIST/sbom.cdx.json
python3 scripts/gen_sbom.py --check $DIST/sbom.cdx.json
python3 scripts/gen_sbom.py --version vX.Y.Z --timestamp 2026-10-19T00:00:00Z
```

The Go module hash (`h1:…`, `goModSum`) is the module *dirhash*, not the zip's
SHA-256, so it is emitted as the property `ncoda:goModSum` rather than a
CycloneDX `hashes[]` entry.

`go list -m all` also returns test-only modules (yaml.v3 pulls `kr/pretty`,
`kr/text` and `check.v1`). The generator classifies linkage against
`./cmd/nclang-compile` and marks them `scope: excluded` with
`ncoda:linkage=test-or-module-graph`; only modules actually linked appear under
`dependencies[].dependsOn`.

## Signing (opt-in)

Local gates never require network access or a signing key. Release signing is
enabled per repository by setting the Actions variable **`NCODA_SIGN=true`**
(Settings → Secrets and variables → Actions → Variables). The release workflow
then:

1. installs cosign (`sigstore/cosign-installer@v3`);
2. signs `SHA256SUMS` keyless (`cosign sign-blob --bundle`, OIDC identity =
   this repository's workflow);
3. verifies the bundle immediately.

Because `SHA256SUMS` covers the binaries, both SBOM files, and the three
license/attribution files, one signature covers the whole artifact set.
`make dist-check` re-verifies presence and checksums before publication.

Verify a downloaded release:

```bash
sha256sum -c SHA256SUMS
cosign verify-blob --bundle SHA256SUMS.cosign.bundle \
  --certificate-identity-regexp '^https://github.com/nodecoda/ncoda/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  SHA256SUMS
```

When the repository variable is unset, releases are **unsigned** and
`SHA256SUMS.cosign.bundle` is absent — absence is explicit, not a failed
signature. For offline signing, sign `SHA256SUMS` with your own key
(e.g. `minisign -Sm SHA256SUMS`) and publish the public key alongside it.
