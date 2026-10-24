# Builtin result contracts (open core)

This directory is the **single declaration source** for the language-level
builtin result contracts. `pkg/builtin/contracts_language_gen.go` is generated
from `ncoda-language.yaml`; never edit the Go file by hand.

```bash
python3 scripts/builtin_contract_check.py --check
python3 scripts/builtin_contract_check.py --write   # regenerate after editing the YAML
```

Why declaration, not inference: the language owns the result prototype and its
field names (`HTTPResponse.body/status_code/headers`). A platform adapter owns
the physical ports (`statusCode`, `response`, …) and declares them in the
commercial repo, injecting them through `pkg/builtin.RegisterContracts`. The
two sides are joined by the language field name; neither side can derive the
other's vocabulary from the wire, so each is declared once.

The open core declares only `ncoda-language`. Platform declarations used to live
here; they now travel with each commercial adapter, which declares its own
source and never links into the open module.
