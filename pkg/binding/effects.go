package binding

// Immutable effect classifications published by the NodeCoda frontend,
// ported from src/nclang/lang/effects.py (read-only oracle). EffectFacts
// mirrors the frozen dataclass: both mappings are wrapped in RoMap (the Go
// analogue of MappingProxyType — lookups and iteration only, Set panics),
// and EmptyEffectFacts mirrors the EMPTY_EFFECT_FACTS module constant
// (effects.py:32).

// EffectKind classifies one expression or function, mirroring
// EffectKind(str, Enum) in effects.py:17-19. The zero value "" is never a
// published kind; always construct one of the two constants.
type EffectKind string

const (
	EffectCalculation       EffectKind = "calculation"
	EffectWorkflowOperation EffectKind = "workflow-operation"
)

// EffectFacts mirrors the frozen EffectFacts dataclass (effects.py:22-30):
// per-node effects keyed in the snapshot's AstNodeID space and per-function
// effects keyed by FunctionID, both read-only after construction.
type EffectFacts struct {
	nodeEffects     *RoMap[AstNodeID, EffectKind]
	functionEffects *RoMap[FunctionID, EffectKind]
}

// NewEffectFacts constructs immutable facts. Both maps are defensively
// copied (mirroring the dataclass __post_init__ MappingProxyType wrapping),
// so later caller mutation never reaches the published facts.
func NewEffectFacts(nodeEffects map[AstNodeID]EffectKind, functionEffects map[FunctionID]EffectKind) *EffectFacts {
	return &EffectFacts{
		nodeEffects:     newRoMap(nodeEffects),
		functionEffects: newRoMap(functionEffects),
	}
}

// EmptyEffectFacts returns the empty facts singleton analogue of
// EMPTY_EFFECT_FACTS (effects.py:32). It is the initial value of
// emptyEffectFacts is the shared empty effect table, mirroring the
// module-level EMPTY_EFFECT_FACTS constant (effects.py:32). The RoMap
// values are immutable, so one instance is safely shared by every Context.
var emptyEffectFacts = NewEffectFacts(map[AstNodeID]EffectKind{}, map[FunctionID]EffectKind{})

// EmptyEffectFacts returns the shared empty EffectFacts. Python's
// DSLPassContext.effect_facts defaults to None (base.py:208); the Go
// Context uses the empty table as a safer default so consumers never
// observe a nil pointer (identical semantics at the fixed stage order).
func EmptyEffectFacts() *EffectFacts { return emptyEffectFacts }

// NodeEffects returns the read-only node effect table (Python
// EffectFacts.node_effects).
func (f *EffectFacts) NodeEffects() *RoMap[AstNodeID, EffectKind] { return f.nodeEffects }

// FunctionEffects returns the read-only function effect table (Python
// EffectFacts.function_effects).
func (f *EffectFacts) FunctionEffects() *RoMap[FunctionID, EffectKind] { return f.functionEffects }
