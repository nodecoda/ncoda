# ncoda open core — four-beat local gate (no CI dependency).
#
#   check    seconds   build + vet + gofmt + unit tests
#   push     minutes   full tests (uncached) + CLI contract smoke
#   nightly  minutes   race detector + repeat run (full corpus stays private)
#   release  minutes   6-platform binaries + SHA256SUMS + CycloneDX SBOM + SBOM text
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

.PHONY: check push nightly release dist dist-check sbom audit contracts-check notices-check clean fmt fmt-check test

check: fmt-check audit contracts-check notices-check
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
	@printf '' | /tmp/nclang-smoke-$(VERSION) --target dify-1.16-graphon-0.6 >/dev/null 2>&1; test $$? -eq 2
	@echo "cli_smoke: ok"

nightly:
	$(GO) test -count=1 ./...
	$(GO) test -race -count=1 ./pkg/ir/... ./pkg/passes/... ./pkg/compiler/...
	@echo "note: full-corpus round-trip regression is a private/commercial CI surface"

release: dist-check
	@echo "release artifacts: $(DIST)"

dist:
	@mkdir -p $(DIST)
	@set -e; for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; out=$(DIST)/nclang-$(VERSION)-$$os-$$arch; \
	  [ "$$os" = windows ] && out=$$out.exe; \
	  echo "build $$os/$$arch"; \
	  GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $$out ./cmd/nclang-compile; \
	done
	@# Apache-2.0 §4(d): ship the License and the NOTICE with the binaries.
	@cp LICENSE NOTICE THIRD-PARTY-NOTICES.md $(DIST)/
	@python3 scripts/gen_sbom.py --version $(VERSION) --out $(DIST)/sbom.cdx.json
	@$(GO) version -m $(DIST)/nclang-$(VERSION)-linux-amd64 > $(DIST)/SBOM.txt
	@cd $(DIST) && sha256sum nclang-* sbom.cdx.json SBOM.txt LICENSE NOTICE THIRD-PARTY-NOTICES.md > SHA256SUMS
	@echo "artifacts in $(DIST)"

# A release directory is only publishable if every stated artifact is present
# and the manifest actually verifies. Cheap, and catches "added a file but
# forgot the checksum" before the tag is pushed.
dist-check: dist
	@cd $(DIST) && for f in LICENSE NOTICE THIRD-PARTY-NOTICES.md sbom.cdx.json SBOM.txt SHA256SUMS; do \
	  test -s "$$f" || { echo "dist-check: missing $$f"; exit 1; }; done
	@cd $(DIST) && sha256sum -c SHA256SUMS --quiet
	@echo "dist-check: ok ($(DIST))"

# CycloneDX SBOM only (the artifact needed when publishing an existing build).
sbom:
	@mkdir -p $(DIST)
	@python3 scripts/gen_sbom.py --version $(VERSION) --out $(DIST)/sbom.cdx.json

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
