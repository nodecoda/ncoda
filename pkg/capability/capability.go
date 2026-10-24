// Package capability declares the axes on which a target platform's native
// node surface is described, plus the conservative default the language lane
// uses.
//
// Boundary (ASSET-BOUNDARY D8): the open kernel owns the *axes* and the
// *default*; each platform adapter declares its own table. Concrete platform
// facts (which node ids exist, which operand shapes a branch node accepts)
// live with the adapter that was written against that platform's sources —
// they are not language semantics and must not be hardcoded here.
//
// Why the default is conservative: a target that has not declared a native
// node must degrade to materialization (a compiler-owned shim), never to an
// assumed-native mapping. Unsafe native mapping fails at the platform; a shim
// fails at the language layer where it can still be diagnosed.
//
// Every axis here must have a production reader, else the decision it should
// drive is hardcoded instead. See docs/five-step/spec/SHIM-ESCAPE-CONTRACT.md
// §5-2 and axis_liveness_test.go (the json builtin bug of 2026-09-27 was
// exactly a table axis nobody read).
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

// Table declares one target's native capabilities. Adapters build these; the
// kernel only reads them.
type Table struct {
	Target       string
	Condition    Condition // branch-node condition operand surface
	TextConcat   bool      // native text-concat node
	TextSplit    bool      // native text-split node
	JSONSerial   bool      // native JSON-serialize node
	JSONDeserial bool      // native JSON-deserialize node

	// LoopReturnShape declares how an early `return` inside a loop body is
	// materialised. Zero = the target carries the shape directly.
	LoopReturnShape LoopReturnShape

	// OnErrorWire maps the language on_error mode ("terminate" /
	// "keep_null" / "remove_failed") to the target's wire spelling. nil =
	// the language spelling passes through unchanged. Platform wire
	// vocabulary lives with the adapter, never in the language.
	OnErrorWire map[string]string

	// LLMEnvelopePlacement declares how a consumed LLMResponse envelope is
	// placed. Zero = the adapter states nothing and no placement is recorded.
	LLMEnvelopePlacement EnvelopePlacement
}

// LoopReturnShape is how a target materialises an early return from a loop.
type LoopReturnShape int

const (
	// LoopReturnNative: the target's graph carries the return directly.
	LoopReturnNative LoopReturnShape = iota
	// LoopReturnStateGate: the target needs a state pair written in the body
	// plus a break and a post-loop gate (compiler-owned statement rewrite).
	LoopReturnStateGate
)

// EnvelopePlacement is where a consumed LLMResponse envelope is hosted.
type EnvelopePlacement int

const (
	// EnvelopeUnspecified: the adapter states nothing.
	EnvelopeUnspecified EnvelopePlacement = iota
	// EnvelopeShim: a compiler-owned shim assembles the envelope.
	EnvelopeShim
	// EnvelopeNative: the target exposes the envelope directly.
	EnvelopeNative
)

// Conservative returns the table for a target that has not declared native
// nodes: ref × literal conditions are native (the one shape every supported
// platform agrees on); everything else materializes.
func Conservative(target string) *Table {
	return &Table{
		Target:    target,
		Condition: Condition{RefLiteral: true},
	}
}

// For is the pre-rename name of Conservative, kept for one release so the
// commercial adapters stay buildable against both kernel versions. New code
// should call Conservative or, better, resolve through an adapter.
//
// Deprecated: use Conservative; platform tables come from adapters.
func For(target string) *Table { return Conservative(target) }
