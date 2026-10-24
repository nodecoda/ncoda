// Package capability declares what a target platform natively supports.
//
// P0 (native-first) lowering decisions are data-driven: the IR consults the
// target's capability table and maps an expression to the platform's most
// native representation, falling back to Synthetic Python shims only for
// what the platform cannot host. Values are grounded in the platform
// sources, not guessed:
//
//   - coze Selector (type 8) resolves both operands as runtime values
//     (selector/callbacks.go TakeMapValue; clause.go Clause.Resolve), so
//     ref × operator × ref is native — a ref×ref comparison must NOT be
//     materialized into a code shim.
//
//   - coze TextProcessor (type 15) is the native concat node; coze
//     JsonSerialization (type 58) is the native JSON-serialize node (G7).
//
//   - no supported platform defines general variables (VariableAssigner is
//     a session-variable system, not `let x = <any>`), and none has a
//     native object-constructor node — intermediate values must be
//     materialized as producing nodes. (There used to be an `ObjectLit` axis
//     for the latter; it was **deleted** 2026-09-27 — it read `false` on all
//     three targets and had zero consumers, i.e. no information: the fact
//     lives here in prose, and the axis comes back the day a target actually
//     gains such a node.) See docs/five-step/spec/SHIM-ESCAPE-CONTRACT.md §5-2:
//     every axis of this table must have a production reader, else the
//     decision it should drive is hardcoded — which is exactly the `json`
//     builtin bug of 2026-09-27.
//
//   - `Condition.RefLiteral` is read at its decision site
//     (`pkg/ir/build_condition.go::conditionNativeDecision`, wired
//     2026-09-27) — ref × literal conditions were previously assumed native
//     regardless of the table.
package capability

// Condition is the native operand surface of a platform's branch/if node.
type Condition struct {
	// RefLiteral: ref × operator × literal is native.
	RefLiteral bool
	// RefRef: ref × operator × ref is native. Platforms whose branch node
	// only carries a left selector + scalar value must materialize ref×ref;
	// their adapter declares that in its own table.
	RefRef bool
}

// Table declares one target's native capabilities.
type Table struct {
	Target       string
	Condition    Condition // branch-node condition operand surface
	TextConcat   bool      // native text-concat node (coze TextProcessor)
	TextSplit    bool      // native text-split node (coze TextProcessor)
	JSONSerial   bool      // native JSON-serialize node (coze type-58)
	JSONDeserial bool      // native JSON-deserialize node (coze type-59)
}

// For returns the capability table for a target name. Unknown targets get a
// conservative table (native-only where every platform agrees: ref ×
// literal conditions; nothing else assumed) so new targets degrade to
// materialization rather than unsafe native mapping.
func For(target string) *Table {
	switch target {
	case "coze-1.0", "coze-studio-flow-1.0": // both Coze surfaces share one backend
		return &Table{
			Target:       target,
			Condition:    Condition{RefLiteral: true, RefRef: true},
			TextConcat:   true,
			TextSplit:    true,
			JSONSerial:   true,
			JSONDeserial: true,
		}
	case "coze-biz-1.0":
		// Commercial Coze (扣子平台) exposes TextProcessor concat/split as
		// native nodes. Other unsupported expression forms still degrade to
		// compiler-owned code shims.
		return &Table{
			Target:       target,
			Condition:    Condition{RefLiteral: true, RefRef: true},
			TextConcat:   true,
			TextSplit:    true,
			JSONSerial:   true,
			JSONDeserial: true,
		}
	default:
		return &Table{
			Target:    target,
			Condition: Condition{RefLiteral: true},
		}
	}
}
