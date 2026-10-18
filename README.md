# ncoda (open core)

`ncoda` is a compiler and decompiler for the **ncoda** language. This
repository is the **open core**: it contains the language kernel and the
**Dify** compile/decompile backend.

- Compile: `ncoda` source → Dify workflow JSON/YAML
- Decompile: Dify workflow JSON/YAML → `ncoda` source
- Library: `github.com/nodecoda/ncoda/pkg/compiler`,
  `github.com/nodecoda/ncoda/platforms/dify/decompile`
- CLI: `cmd/nclang-compile` (stdin → versioned JSON result on stdout)

## What is not here

Coze, Coze-Studio, Coze-Biz, the workflow runtime and the foundry are
distributed separately under a commercial license. They depend on this module;
this module never depends on them.

## Build and test

```bash
go build ./...
go vet ./...
go test ./...
```

Requirements: Go 1.26.5 or newer.

CLI smoke:

```bash
printf 'workflow w { agent main { } }\n' | go run ./cmd/nclang-compile
```

## Repository layout

| Path | Contents |
| --- | --- |
| `pkg/` | language kernel: lexer, parser, AST, types, binding, passes, IR, code generation, Dify backend, shared ncoda output layer |
| `platforms/dify/` | Dify decompiler |
| `cmd/nclang-compile/` | Dify compile CLI (JSON contract) |
| `tools/` | open developer tools (grammar enumeration, surface/depth/map-key census) |
| `contracts/` | versioned JSON contracts |
| `examples/` | example sources and Dify fixtures used by tests |
| `tests/fixtures/` | shared test fixtures |

## Coverage boundary

The private full corpus is not redistributable. Public tests use the examples
and fixtures in this repository plus synthetic generator matrices; full-corpus
round-trip regression runs in the private/commercial CI.

## License

Apache License 2.0 — see `LICENSE` and `NOTICE`.

The names `ncoda` and `nodecoda` are project trademarks; the license does not
grant trademark rights.
