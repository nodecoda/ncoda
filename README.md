# ncoda (open core)

`ncoda` is a **runnable DSL**: a language with its own syntax, semantics, IR
and conformance gates. This repository is the **open core** — the language
kernel and the language-level verdicts. Platform adapters are not here.

- **L1 spec + language frontend**: lexer → parser → binding → types → passes
- **IR**: the canonical `ncoda-ir/v1` dump (`--emit-ir`)
- **Language CLI**: `cmd/nclang-compile` (stdin → `ncoda-ir/v1` on stdout)
- **Public libraries / capability headers**: `libraries/` (174 headers,
  plus deterministic offline implementations in `pkg/localtools`)
- **Out-of-process plugins**: `plugins/` — a library implementation can live in
  its own process (any language, stdio JSON-RPC) and is loaded with
  `--plugin-dir`; see `docs/PLUGIN-AUTHORING-SPEC.md`
- **Runtime (L2)**: `pkg/runtime` — the target-free executor. It turns
  `spec.v1` into an executable graph (node semantics, loop state, error
  policy, trace) and ships as `cmd/ncoda-run`.
- **Public test corpus**: `examples/nodecoda/` + `tests/fixtures/`

The open repo answers two questions: **"does this program mean something in
the language?"** (L1) and **"does it run without any platform?"** (L2).
Platform representability ("can the target carry it?") belongs to the
adapter, not to the language.

## What is not here

- **Platform adapters**: every platform backend lives in the commercial
  repo; the open module never imports them.
- **Round-trip oracles and platform fixtures**: those are adapter evidence.
- **The private corpus**: the corpus is a separate artifact; the open tests
  use `examples/` and `tests/fixtures/`.

## Invariants

1. **Spec is law.** A language claim is backed by a language-level gate,
   never by a platform's behavior.
2. **Runtime is execution.** "The platform cannot do it" is `rc=3`
   (`UNSUPPORTED_BY_TARGET`), not a language error and not a pass.
3. **Corpus is the exam.** It measures the implementation; it does not define
   the language.
4. **Adapters are plugins.** Moving an adapter in or out must never change a
   language verdict.
5. **No platform spelling in the kernel.** A platform name in a kernel
   identifier or file path is a dependency, not a comment. `scripts/audit_split.py` S6
   enforces it in both directions: an unregistered platform identifier fails,
   and a ledger entry whose identifier is gone fails too. The complete,
   reviewed residue list is `docs/PLATFORM-IDENTITY-RESIDUALS.yaml`.

The qualifier in `import "acme.tool"` is validated by **shape**, never against
a platform list: whether a target can satisfy the binding is an adapter
question answered at build time (`rc=3`), because "the platform is unsupported"
must not surface as a language error.

## Running a workflow (runtime lane)

```bash
# compile + execute one source, no platform, no network
go build -o ncoda-run ./cmd/ncoda-run
./ncoda-run examples/nodecoda/00_minimal_start_llm_end.ncoda
# {"output":"echo:openai_api_compatible/gpt-5.4"}

# entry input, then a failure mode: language error rc=1, usage error rc=2
./ncoda-run examples/nodecoda/03_conditional_branch.ncoda -p x=5
./ncoda-run --model-factory acme examples/nodecoda/01_simple_chat.ncoda   # rc=2

# local language libraries: deterministic, offline, no platform provider
./ncoda-run --local-plugins --tool-path ./libraries \
  examples/nodecoda/62_local_library_roundtrip.ncoda
# {"output":[{"k":"hello"}]}
```

`--local-plugins` registers only the language libraries (`database`, `matting`,
`image_scale`, `image_generate`). Platform tool ids are adapter facts and are
never registered here; a source that needs one returns `RBINDING_MISSING`
instead of pretending the platform exists.

Capability headers are resolved like C headers: `--tool-path` > `NCLANG_TOOL_PATH`
> `<dir of the executable>/libraries` > `~/.nodecoda/tools`.

The default model backend is `echo` (offline, deterministic). Real model
backends are host capabilities and live with the adapters — the language
runtime does not carry provider credentials.

## CLI contract (language lane)

```bash
# valid source -> ncoda-ir/v1, rc=0
cat examples/nodecoda/00_minimal_start_llm_end.ncoda | nclang-compile

# language error -> no IR, diagnostics on stderr, rc=1
printf '' | nclang-compile

# platform target -> usage error, rc=2 (the open build has no adapter)
printf '' | nclang-compile --target acme-1.0
```

`--target` accepts only `__none__` (the language lane). There is no silent
downgrade: a platform target is refused.

## Build and test

```bash
go build ./...
go vet ./...
go test ./...
```

Requirements: Go 1.26.5 or newer, plus Python 3 with PyYAML for the
declaration/artifact gates (`pip install pyyaml`).

The four-beat local gate:

```bash
make check     # gofmt + split audit + builtin contracts + build/vet/test
make push      # uncached tests + language-lane CLI smoke (rc 0/1/2)
make nightly   # race detector + repeat run
make dist      # 6-platform binaries + SBOM + checksums
```

`make dist` writes to `DIST` (default `/tmp/ncoda-dist-<VERSION>`) and builds
both lanes: `nclang` (language) and `ncoda-run` (runtime). It also ships
`libraries/` next to the binaries, so `ncoda-run` resolves `import "<lib>"`
from `<exe>/libraries` without any extra flag, and ships `plugins/`
(source-only, needs a system `python3`) which `--plugin-dir <exe>/plugins`
picks up.

## Repository layout

| Path | Contents |
| --- | --- |
| `pkg/` | language kernel: lexer, parser, AST, types, binding, passes, IR, IR dump, code parse |
| `pkg/target/` | platform-neutral target face (`Shape`/`Loss`/`Codec`); no platform vocabulary |
| `pkg/compiler/` | language lane: frontend → IR (+ wire rules); no render/serialize |
| `pkg/cli/` | `nclang-compile` language-lane CLI |
| `cmd/nclang-compile/` | language-lane entry point |
| `libraries/` | public capability headers (language-level symbols) |
| `plugins/` | out-of-process library implementations (manifest + server) |
| `pkg/runtime/` | L2 executor: spec.v1 → node graph (target-free) |
| `pkg/localtools/` | deterministic offline implementations of the public libraries |
| `pkg/toolbridge/` `pkg/toolpath/` | plugin→semantic projection; capability search path |
| `pkg/runner/` `cmd/ncoda-run/` | runtime lane entry point (`ncoda-run`) |
| `examples/` | example sources, capability headers, language fixtures |
| `contracts/` | language contracts (wire shapes), no platform contracts |
| `tools/` | open developer tools (grammar enumeration, surface/depth/map-key census) |

## Coverage boundary

The private full corpus is not redistributable. Public tests use the examples
and fixtures in this repository plus synthetic generator matrices; the
full-corpus sweep runs in the private/commercial CI and is reported as a
progress bar, not as a substitute for a language gate.

## License

Apache License 2.0 — see `LICENSE` and `NOTICE`.

The names `ncoda` and `nodecoda` are project trademarks; the license does not
grant trademark rights.
