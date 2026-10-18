# Provenance

This repository is a clean snapshot of the ncoda product source, extracted
from the private governance repository `godcc`.

- Source repository: `godcc` (private)
- Source commit: `ee300b26b7b9fcca196901da3c199e1aca23589b` (2026-10-19)
- Extraction model: **O2 — open core + commercial closed source** (M0 decision,
  2026-10-18)
- History is **not rewritten**: the private repository keeps the full history.
  This repository starts with a snapshot commit.
- `PROVENANCE`-relevant extraction transformations:
  1. Go module path rewritten from `nodecoda.com/lang/compiler` to
     `github.com/nodecoda/ncoda`.
  2. Only the open substrate is included: `pkg/**`, `platforms/dify/**`,
     the Dify-only CLI (`cmd/nclang-compile`) and open developer tools.
     Closed-side packages (`internal/backend/{coze,cozebiz}`, `internal/runtime`,
     `platforms/{cozestudio,cozebiz}`, the multi-target CLI, foundry) are not
     part of this tree.
  3. Closed-side generated contract data (`contracts_coze_gen.go`) is removed;
     the open build registers only the Dify contract.
  4. Internal absolute paths were neutralised; the public tree contains no
     `/home/dev/` references.
  5. Cross-platform census tests skip directories/contracts that do not exist
     in this module (the commercial repository keeps the full scan surface).

The commercial repository (`ncoda-commercial`) depends on this module and adds
the closed backends and runtime. Dependency direction is strictly one-way:
commercial → open core.
