# ncoda open core — four-beat local gate (no CI dependency).
#
#   check    seconds   build + vet + gofmt + unit tests
#   push     minutes   full tests (uncached) + CLI contract smoke
#   nightly  minutes   race detector + repeat run (full corpus stays private)
#   release  minutes   6-platform binaries (compiler + runtime) + SHA256SUMS + CycloneDX SBOM + SBOM text
#
# The private full corpus (870 workflows) is not redistributable; public tests
# use examples/ and tests/fixtures. Full-corpus round-trip runs in the
# commercial/private CI.

GO       ?= go
VERSION  ?= dev
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS   = -s -w \
            -X github.com/nodecoda/ncoda/pkg/version.Version=$(VERSION) \
            -X github.com/nodecoda/ncoda/pkg/version.Commit=$(COMMIT)
DIST     ?= /tmp/ncoda-dist-$(VERSION)
PLATFORMS = linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: check push nightly release dist dist-check release-smoke sbom audit contracts-check notices-check clean fmt fmt-check test library-coverage library-coverage-check unbound-attribution-check remote-audit remote-audit-check plugin-selftest

# Library coverage is corpus-dependent; keep it out of `check` (a missing corpus
# is rc=3, not a pass). Run it explicitly or in the corpus CI lane.
library-coverage:
	python3 scripts/library_coverage.py

library-coverage-check:
	python3 scripts/library_coverage.py --check
	python3 scripts/gen_stub_plugin.py --check

# Unbound attribution is corpus-dependent too; keep it out of `check`.
unbound-attribution-check:
	python3 scripts/unbound_attribution.py --check
	python3 scripts/remote_audit.py --check

remote-audit:
	python3 scripts/remote_audit.py

remote-audit-check:
	python3 scripts/remote_audit.py --check

# Out-of-process plugin host: Go judgements plus one end-to-end call through a
# real subprocess (Python reference implementation in testdata/).
plugin-selftest:
	$(GO) test -count=1 ./pkg/pluginhost/ ./pkg/runner/
	@command -v python3 >/dev/null || { echo "plugin-selftest: python3 not found (skipped python lane)"; exit 0; }
	@for t in plugins/*/test_server.py; do [ -f "$$t" ] && python3 "$$t" || exit $$?; done
	$(GO) build -o $(CURDIR)/.tmp-ncoda-run ./cmd/ncoda-run
	@set -e; d=$$(mktemp -d); cp pkg/pluginhost/testdata/server.py $$d/; \
	 printf 'schema: ncoda.plugin/v1\nid: test-python\nversion: 0.1.0\nprotocol: ncoda-plugin/1\ncommand: ["python3","server.py"]\nlibraries:\n  - library: array_count\n    fidelity: real\n    functions: [array_count]\n  - library: common_code_block\n    fidelity: real\n    functions: [list_sli_trim, get_one_by_num]\n' > $$d/ncoda-plugin.yaml; \
	 out=$$(./.tmp-ncoda-run --plugin $$d/ncoda-plugin.yaml --tool-path ./libraries --list-symbols); \
	 echo "$$out" | grep -q '"plugin_hosts":1' || { echo "plugin-selftest: symbol listing missing plugin host: $$out"; exit 1; }; \
	 echo "plugin-selftest: ok"; rm -rf $$d .tmp-ncoda-run

check: fmt-check audit contracts-check notices-check
	python3 scripts/test_library_coverage.py
	python3 scripts/test_unbound_attribution.py
	python3 scripts/test_remote_audit.py
	$(GO) build ./...
	$(GO) vet ./...
	$(GO) test ./...

push:
	$(GO) test -count=1 ./...
	@echo "── language-lane smoke ──"
	@# `go run` collapses every non-zero exit into 1, so the rc contract must
	@# be checked against a real binary; otherwise rc=2 is indistinguishable.
	@$(GO) build -o /tmp/nclang-smoke-$(VERSION) ./cmd/nclang-compile
	@printf '' | /tmp/nclang-smoke-$(VERSION) >/dev/null 2>&1; test $$? -eq 1
	@cat examples/nodecoda/00_minimal_start_llm_end.ncoda | /tmp/nclang-smoke-$(VERSION) | grep -q '"schema": "ncoda-ir/v1"'
	@printf '' | /tmp/nclang-smoke-$(VERSION) --target acme-1.0 >/dev/null 2>&1; test $$? -eq 2
	@echo "cli_smoke: ok"
	@echo "── runtime-lane smoke ──"
	@$(GO) build -o /tmp/ncoda-run-smoke-$(VERSION) ./cmd/ncoda-run
	@/tmp/ncoda-run-smoke-$(VERSION) examples/nodecoda/00_minimal_start_llm_end.ncoda | grep -q '"output"'
	@printf 'function main( {' | /tmp/ncoda-run-smoke-$(VERSION) >/dev/null 2>&1; test $$? -eq 1
	@/tmp/ncoda-run-smoke-$(VERSION) --model-factory acme examples/nodecoda/00_minimal_start_llm_end.ncoda >/dev/null 2>&1; test $$? -eq 2
	@# public-library lane: a source that imports only language libraries must run
	@# offline with --local-plugins, and must fail honestly without it.
	@/tmp/ncoda-run-smoke-$(VERSION) --local-plugins --tool-path ./libraries examples/nodecoda/62_local_library_roundtrip.ncoda | grep -q '"k":"hello"'
	@/tmp/ncoda-run-smoke-$(VERSION) --tool-path ./libraries examples/nodecoda/62_local_library_roundtrip.ncoda >/dev/null 2>&1; test $$? -eq 1
	@# distribution layout: headers next to the binary, no --tool-path
	@mkdir -p /tmp/ncoda-dist-smoke-$(VERSION)/libraries
	@cp /tmp/ncoda-run-smoke-$(VERSION) /tmp/ncoda-dist-smoke-$(VERSION)/ncoda-run
	@cp libraries/*.ncoda /tmp/ncoda-dist-smoke-$(VERSION)/libraries/
	@cd /tmp && /tmp/ncoda-dist-smoke-$(VERSION)/ncoda-run --local-plugins $(CURDIR)/examples/nodecoda/62_local_library_roundtrip.ncoda | grep -q '"k":"hello"'
	@echo "runtime_smoke: ok"

nightly:
	$(GO) test -count=1 ./...
	$(GO) test -race -count=1 ./pkg/ir/... ./pkg/passes/... ./pkg/compiler/...
	@echo "note: full-corpus round-trip regression is a private/commercial CI surface"

release: dist-check
	@echo "release artifacts: $(DIST)"

dist:
	@mkdir -p $(DIST)
	@set -e; for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; \
	  echo "build $$os/$$arch"; \
	  for cmd in nclang ncoda-run; do \
	    case $$cmd in nclang) pkg=./cmd/nclang-compile;; *) pkg=./cmd/$$cmd;; esac; \
	    out=$(DIST)/$$cmd-$(VERSION)-$$os-$$arch; \
	    [ "$$os" = windows ] && out=$$out.exe; \
	    GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $$out $$pkg; \
	  done; \
	done
	@# Apache-2.0 §4(d): ship the License and the NOTICE with the binaries.
	@# install (not cp) so a rerun can overwrite read-only LICENSE (0444).
	@# Public capability headers: ncoda-run resolves `import "<lib>"` from
	@# <exe>/libraries, so a distribution that omits them cannot run a source
	@# that imports a public library.
	@install -d $(DIST)/libraries
	@install -m 0644 libraries/*.ncoda $(DIST)/libraries/
	@# Out-of-process plugins: source-only (manifest + reference server). They are
	@# resolved by --plugin-dir <exe>/plugins, so a distribution that omits them
	@# cannot run a source whose library is implemented out of process.
	@set -e; for d in plugins/*/; do \
	  [ -d "$$d" ] || continue; \
	  name=$$(basename "$$d"); \
	  install -d "$(DIST)/plugins/$$name"; \
	  find "$$d" -maxdepth 1 -type f \( -name '*.yaml' -o -name '*.py' -o -name '*.md' -o -name '*.json' \) \
	    -exec install -m 0644 {} "$(DIST)/plugins/$$name/" \;; \
	done
	@install -m 0644 LICENSE NOTICE THIRD-PARTY-NOTICES.md $(DIST)/
	@python3 scripts/gen_sbom.py --version $(VERSION) --package ./cmd/nclang-compile --out $(DIST)/sbom-nclang.cdx.json
	@python3 scripts/gen_sbom.py --version $(VERSION) --package ./cmd/ncoda-run --out $(DIST)/sbom-ncoda-run.cdx.json
	@$(GO) version -m $(DIST)/nclang-$(VERSION)-linux-amd64 > $(DIST)/SBOM.txt
	@$(GO) version -m $(DIST)/ncoda-run-$(VERSION)-linux-amd64 >> $(DIST)/SBOM.txt
	@cd $(DIST) && sha256sum nclang-* ncoda-run-* sbom-*.cdx.json SBOM.txt LICENSE NOTICE THIRD-PARTY-NOTICES.md libraries/*.ncoda plugins/*/* > SHA256SUMS
	@echo "artifacts in $(DIST)"

# A release directory is only publishable if every stated artifact is present
# and the manifest actually verifies. Cheap, and catches "added a file but
# forgot the checksum" before the tag is pushed.
dist-check: dist
	@cd $(DIST) && for f in LICENSE NOTICE THIRD-PARTY-NOTICES.md sbom-nclang.cdx.json sbom-ncoda-run.cdx.json SBOM.txt SHA256SUMS; do \
	  test -s "$$f" || { echo "dist-check: missing $$f"; exit 1; }; done
	@test -d $(DIST)/libraries || { echo "dist-check: missing libraries/"; exit 1; }
	@n=$$(ls $(DIST)/libraries/*.ncoda | wc -l); test "$$n" -eq 174 || { echo "dist-check: libraries/ has $$n headers, want 174"; exit 1; }
	@set -e; for d in plugins/*/; do \
	  [ -d "$$d" ] || continue; \
	  name=$$(basename "$$d"); \
	  test -s "$(DIST)/plugins/$$name/ncoda-plugin.yaml" || { echo "dist-check: missing plugins/$$name/ncoda-plugin.yaml"; exit 1; }; \
	  test -s "$(DIST)/plugins/$$name/server.py" || { echo "dist-check: missing plugins/$$name/server.py"; exit 1; }; \
	done
	@cd $(DIST) && sha256sum -c SHA256SUMS >/dev/null || { echo "dist-check: SHA256SUMS does not verify"; exit 1; }
	@cd $(DIST) && sha256sum -c SHA256SUMS --quiet
	@echo "dist-check: ok ($(DIST))"

# Release-day smoke: exactly the assertions the release workflow makes, runnable
# locally and on every main push. Why a target instead of inline YAML — the
# workflow only runs on a tag, so an assertion that drifts from the dist layout
# stays invisible until release day. v0.7.0 hit both halves of that: it asserted
# a `"status":"COMPILE_ERROR"` JSON envelope the open CLI never prints (it writes
# `nclang-compile: COMPILE_ERROR` to stderr, rc=1) and a singular
# `sbom.cdx.json` the two-binary split no longer produces.
release-smoke: dist-check
	@B=$(DIST)/nclang-$(VERSION)-linux-amd64; \
	  printf '' | $$B >/dev/null 2>&1; test $$? -eq 1 || { echo "release-smoke: empty stdin must be rc=1"; exit 1; }; \
	  printf '' | $$B 2>&1 >/dev/null | grep -q 'COMPILE_ERROR' || { echo "release-smoke: empty stdin must report COMPILE_ERROR"; exit 1; }; \
	  $$B < examples/nodecoda/00_minimal_start_llm_end.ncoda | grep -q '"schema": "ncoda-ir/v1"' || { echo "release-smoke: example must emit ncoda-ir/v1"; exit 1; }; \
	  printf '' | $$B --target acme-1.0 >/dev/null 2>&1; test $$? -eq 2 || { echo "release-smoke: platform target must be rc=2"; exit 1; }
	@R=$(DIST)/ncoda-run-$(VERSION)-linux-amd64; \
	  $$R --local-plugins --tool-path $(DIST)/libraries examples/nodecoda/62_local_library_roundtrip.ncoda | grep -q '"k":"hello"' || { echo "release-smoke: distributed libraries/ must run offline"; exit 1; }
	@# Out-of-process lane: shipped manifest + reference server must run from the dist.
	@# Requires a system python3; without it the assertion is skipped (not failed), and
	@# the lyrics must still be reported so the gap is visible in CI logs.
	@R=$(DIST)/ncoda-run-$(VERSION)-linux-amd64; \
	  if command -v python3 >/dev/null 2>&1; then \
	    n=$$($$R --plugin-dir $(DIST)/plugins --tool-path $(DIST)/libraries --list-symbols | tee /tmp/ncoda-plugin-symbols.json | grep -o '"plugin_hosts":[0-9]*' | cut -d: -f2); \
	    want=$$(find plugins -maxdepth 2 -name ncoda-plugin.yaml | wc -l); \
	    test "$$n" -eq "$$want" || { echo "release-smoke: want $$want plugin hosts from dist, got $$n"; exit 1; }; \
	    printf '@language nodecoda/1\n@mode workflow\nimport "capcut_data";\nfunction main() -> any { let t = capcut_data.timelines({ "duration": 1000000, "num": 2 }); return t; }\n' > $(DIST)/plugin-smoke.ncoda; \
	    $$R --plugin-dir $(DIST)/plugins --tool-path $(DIST)/libraries $(DIST)/plugin-smoke.ncoda | grep -q '"end":1000000' || { echo "release-smoke: distributed plugins/ must run"; exit 1; }; \
	    echo "release-smoke: plugin lane ok ($$n hosts)"; \
	  else \
	    echo "release-smoke: python3 not found — plugin lane skipped"; \
	  fi
	@echo "release-smoke: ok ($(DIST))"

# CycloneDX SBOM only (the artifact needed when publishing an existing build).
sbom:
	@mkdir -p $(DIST)
	@python3 scripts/gen_sbom.py --version $(VERSION) --package ./cmd/nclang-compile --out $(DIST)/sbom-nclang.cdx.json
	@python3 scripts/gen_sbom.py --version $(VERSION) --package ./cmd/ncoda-run --out $(DIST)/sbom-ncoda-run.cdx.json

# Builtin result contracts: declaration source == generated artifact.
contracts-check:
	@python3 scripts/builtin_contract_check.py --check
	@python3 scripts/test_builtin_contract_check.py -q

# Third-party attribution shipped with the binaries (Apache-2.0 §4(d), MIT/BSD notices).
notices-check:
	@python3 scripts/gen_third_party_notices.py --check THIRD-PARTY-NOTICES.md
	@python3 scripts/test_gen_third_party_notices.py -q

# Open-core boundary (O2 split invariants), plus its own negative tests.
audit:
	@python3 scripts/audit_split.py
	@python3 scripts/test_audit_split.py -q

fmt:
	@gofmt -w $$(find . -name '*.go' -not -path './.git/*')

fmt-check:
	@out=$$(gofmt -l $$(find . -name '*.go' -not -path './.git/*')); \
	if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi

clean:
	@python3 -c "import shutil,os; shutil.rmtree('$(DIST)', ignore_errors=True)"
