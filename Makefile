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

.PHONY: check push nightly release dist sbom audit contracts-check clean fmt fmt-check test

check: fmt-check audit contracts-check
	$(GO) build ./...
	$(GO) vet ./...
	$(GO) test ./...

push:
	$(GO) test -count=1 ./...
	@echo "── CLI contract smoke ──"
	@printf '' | $(GO) run ./cmd/nclang-compile | grep -q '"status":"COMPILE_ERROR"'
	@cat examples/nodecoda/00_minimal_start_llm_end.ncoda | $(GO) run ./cmd/nclang-compile | grep -q '"status":"SUCCEEDED"'
	@echo "cli_smoke: ok"

nightly:
	$(GO) test -count=1 ./...
	$(GO) test -race -count=1 ./pkg/ir/... ./pkg/passes/... ./pkg/compiler/... ./platforms/dify/decompile/...
	@echo "note: full-corpus round-trip regression is a private/commercial CI surface"

release: dist
	@echo "release artifacts: $(DIST)"

dist:
	@mkdir -p $(DIST)
	@set -e; for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; out=$(DIST)/nclang-$(VERSION)-$$os-$$arch; \
	  [ "$$os" = windows ] && out=$$out.exe; \
	  echo "build $$os/$$arch"; \
	  GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $$out ./cmd/nclang-compile; \
	done
	@python3 scripts/gen_sbom.py --version $(VERSION) --out $(DIST)/sbom.cdx.json
	@$(GO) version -m $(DIST)/nclang-$(VERSION)-linux-amd64 > $(DIST)/SBOM.txt
	@cd $(DIST) && sha256sum nclang-* sbom.cdx.json SBOM.txt > SHA256SUMS
	@echo "artifacts in $(DIST)"

# CycloneDX SBOM only (the artifact needed when publishing an existing build).
sbom:
	@mkdir -p $(DIST)
	@python3 scripts/gen_sbom.py --version $(VERSION) --out $(DIST)/sbom.cdx.json

# Builtin result contracts: declaration source == generated artifact.
contracts-check:
	@python3 scripts/builtin_contract_check.py --check
	@python3 scripts/test_builtin_contract_check.py -q

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
