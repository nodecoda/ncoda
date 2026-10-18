# Builtin result contracts (open core)

This directory is the **single declaration source** for builtin-node result
contracts in the open core. `pkg/builtin/contracts_dify_gen.go` is generated
from `dify-1.16-graphon-0.6.yaml`; never edit the Go file by hand.

```bash
python3 scripts/builtin_contract_check.py --check
python3 scripts/builtin_contract_check.py --write   # regenerate after editing the YAML
```

Why declaration, not inference: a platform gives physical ports (names and
platform types), while the language layer has its own field names and types
(`status_code` vs `statusCode`; `map<string, any>` vs platform `string`). The
mapping is a fact neither side can derive, so it is declared once here and read
by both lowering and raising.

The closed backends (coze / coze-biz / coze-studio) keep their own declaration
source and inject their contracts through `pkg/builtin.RegisterContracts`; the
open core never links them.
