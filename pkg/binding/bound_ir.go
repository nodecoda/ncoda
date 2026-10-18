package binding

// Typed-bound-program structures, ported from the Python layer
// src/nclang/lang/bound_ir.py (TypedBoundProgram and friends) plus the
// task-11 brief contract. This is a skeleton task: code_expressions, call
// graph and effect facts belong to later tasks (bound IR / code binding /
// effect passes), so those fields are published as empty containers here and
// filled by their owning tasks. StdlibCalls is filled by Task 24b
// (BuildStdlibCalls, stdlib_bind.go) and carries the expanded bound-stdlib
// records from binder.py _build_stdlib_calls.
//
// The reference's freeze_type deep-copies every embedded descriptor; the Go
// port follows the established immutable-descriptor convention instead
// (types.Descriptor is built once and treated as immutable after
// construction — see types.go and snapshot.go), so the maps here copy the
// map but share descriptor pointers, exactly like Snapshot.Publish. See the
// task-11 report for the recorded deviations (record name, ExprTypes field
// naming, exprTypes key space).

import (
	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/types"
)

// AggregateLiteralKind classifies an aggregate literal fact, mirroring
// AggregateLiteralKind in bound_ir.py (MAP / RECORD).
type AggregateLiteralKind int

const (
	// AggregateLiteralKindMap is a map literal typed as a map.
	AggregateLiteralKindMap AggregateLiteralKind = iota
	// AggregateLiteralKindRecord is a map literal typed as a record.
	AggregateLiteralKindRecord
)

// String returns the Python enum value ("MAP"/"RECORD").
func (k AggregateLiteralKind) String() string {
	switch k {
	case AggregateLiteralKindMap:
		return "MAP"
	case AggregateLiteralKindRecord:
		return "RECORD"
	}
	return "UNKNOWN"
}

// AggregateFact is one published aggregate-literal fact, mirroring
// AggregateLiteralFact in bound_ir.py: the literal's kind and its type
// info. Python validates kind/type agreement in __post_init__ (ValueError);
// the Go constructor NewAggregateFact panics on disagreement (the
// Go-analogue of the reference's hard error, like RoMap.Set).
type AggregateFact struct {
	Kind     AggregateLiteralKind
	TypeInfo *types.Descriptor
}

// NewAggregateFact validates that kind agrees with the descriptor shape
// (MAP kind requires a map base, RECORD kind requires a record base) and
// returns the fact, mirroring AggregateLiteralFact.__post_init__.
func NewAggregateFact(kind AggregateLiteralKind, typeInfo *types.Descriptor) AggregateFact {
	if typeInfo == nil {
		panic("binding: aggregate literal fact requires a non-nil type")
	}
	valid := (kind == AggregateLiteralKindMap && typeInfo.Kind == types.Map) ||
		(kind == AggregateLiteralKindRecord && typeInfo.Kind == types.Record)
	if !valid {
		panic("binding: aggregate literal kind " + kind.String() + " disagrees with " + typeInfo.Kind.String() + " type")
	}
	return AggregateFact{Kind: kind, TypeInfo: typeInfo}
}

// BoundParameter is one bound function parameter, mirroring BoundParameter
// in bound_ir.py (name / type_info / symbol_id). The brief contract names
// the field TypeInfo; the reference names it type_info.
type BoundParameter struct {
	Name     string
	TypeInfo *types.Descriptor
	SymbolID SymbolID
}

// BoundFunction is the Go-side record of one bound user function (the
// reference only carries function_bindings: FunctionId -> AstNodeId; the
// task-11 brief requires a richer record assembled from the snapshot's
// function_bindings + declarations + scope bindings).
type BoundFunction struct {
	ID     FunctionID
	Name   string
	Params []BoundParameter
	Return *types.Descriptor
	Body   *ast.Block
	Scope  ScopeID
	// ParameterSymbols mirrors Python BoundInlineFunction.parameter_symbols
	// (function_inliner.py:35-36): the declaration SymbolID of each
	// ParamDecl, in source order, used by inlineFunction to register the
	// parameter's VarEntry under its stable semantic identity
	// (typed_lowerer.py:108-111). Assembled in assembleBoundFunction from
	// the snapshot's DeclarationSymbols, exactly like the reference.
	ParameterSymbols []SymbolID
	// Span is the FunctionDef's source span (BoundInlineFunction.span,
	// typed_lowerer.py:117), used for the function-level TIE Locations
	// (function_inliner.py:171,176,190). Assembled in assembleBoundFunction
	// from the FunctionDef's Span(); nil when the definition carries no
	// span (synthetic records).
	Span *ast.Span
}

// BoundCodeFunction is the skeleton record for a compiler-analyzable code
// function (ast.py CodeFunctionDef). The reference carries
// code_function_ids (frozenset) and code_function_bodies
// (FunctionId -> body); the code-binding task owns detection, so
// TypeBind publishes an empty CodeFunctions map for now.
type BoundCodeFunction struct {
	ID   FunctionID
	Body *ast.Block
	// ParameterSymbols mirrors BoundInlineFunction.parameter_symbols
	// (typed_lowerer.py:108-111): the declaration SymbolID of each
	// ParamDecl, in source order. The inline code materializer
	// (_inline_code_function, code_materializer.py:239-295) binds each
	// rendered call argument to its parameter symbol in a local render
	// environment, so IdentExpr references inside the body resolve to the
	// already-rendered argument source instead of a workflow selector.
	ParameterSymbols []SymbolID
}

// BoundValueRef mirrors BoundValueRef in bound_ir.py (value / field_path):
// a stable semantic identity plus the physical field path walked off it.
// The Python union SymbolId | ValueDefId is the discriminated
// BoundValueID record below; FieldPath mirrors the Python tuple ("" stays
// nil, treated as the empty tuple by consumers).
type BoundValueRef struct {
	Value     BoundValueID
	FieldPath []string
}

// BoundValueIDKind discriminates BoundValueID, mirroring the Python union
// SymbolId | ValueDefId carried by BoundValueRef.value (bound_ir.py:34-35).
type BoundValueIDKind int

const (
	// BoundValueIDNone is the explicit zero value; never a valid id.
	BoundValueIDNone BoundValueIDKind = iota
	// BoundValueIDSymbol keys a declared/referenced symbol.
	BoundValueIDSymbol
	// BoundValueIDDef keys a code value definition (unreachable while code
	// roots are empty — see stdlib_bind.go; kept for the reference union).
	BoundValueIDDef
)

// BoundValueID is the Go form of the Python SymbolId | ValueDefId union.
type BoundValueID struct {
	Kind     BoundValueIDKind
	SymbolID SymbolID
	ValueDef ValueDefID
}

// SymbolBoundValue builds a symbol-keyed BoundValueID.
func SymbolBoundValue(id SymbolID) BoundValueID {
	return BoundValueID{Kind: BoundValueIDSymbol, SymbolID: id}
}

// ValueDefBoundValue builds a value-def-keyed BoundValueID.
func ValueDefBoundValue(id ValueDefID) BoundValueID {
	return BoundValueID{Kind: BoundValueIDDef, ValueDef: id}
}

// BoundLiteral mirrors BoundLiteral in bound_ir.py (value: the Python union
// str | int | float | bool | None, ported as any — literalDefault already
// follows the same convention).
type BoundLiteral struct {
	Value any
}

// BoundArgument mirrors BoundArgument in bound_ir.py: the value is either a
// BoundValueRef (ValueRef) or a BoundLiteral (Literal); exactly one is set
// (the constructors enforce the invariant). TypeInfo carries the frozen
// reference/literal descriptor.
type BoundArgument struct {
	ValueRef *BoundValueRef
	Literal  *BoundLiteral
	TypeInfo *types.Descriptor
}

// ReferenceArgument builds a reference-valued BoundArgument.
func ReferenceArgument(ref BoundValueRef, typeInfo *types.Descriptor) BoundArgument {
	return BoundArgument{ValueRef: &ref, TypeInfo: typeInfo}
}

// LiteralArgument builds a literal-valued BoundArgument.
func LiteralArgument(literal BoundLiteral, typeInfo *types.Descriptor) BoundArgument {
	return BoundArgument{Literal: &literal, TypeInfo: typeInfo}
}

// BoundStdlibStep mirrors BoundStdlibStep in bound_ir.py: one step of a
// bound stdlib expansion (knowledge/http/tool/llm) with its stable result
// identities and step-specific config/inputs/messages. Config/Inputs mirror
// the Python MappingProxyType copies; Messages carries the interleaved
// literal/reference prompt parts.
type BoundStdlibStep struct {
	NodeID       AstNodeID
	Kind         string
	ResultSymbol SymbolID
	ResultValue  ValueDefID
	ResultType   *types.Descriptor
	Config       map[string]string
	Inputs       map[string]BoundValueRef
	Messages     []StdlibMessage
}

// StdlibMessage is one (role, parts) pair of a BoundStdlibStep message list,
// mirroring the Python tuple[tuple[str, tuple[str | BoundValueRef, ...]],
// ...]: Parts interleaves literal text and bound value references.
type StdlibMessage struct {
	Role  string
	Parts []StdlibMessagePart
}

// StdlibMessagePart is one part of a StdlibMessage: literal Text when Ref is
// nil, otherwise a bound value reference (never both).
type StdlibMessagePart struct {
	Text string
	Ref  *BoundValueRef
}

// StdlibExpansion mirrors BoundStdlibExpansion in bound_ir.py (the ID-list
// fields function_ids/scope_ids/symbol_ids/value_ids are not ported — the
// minted identities live on the parameters/steps/result records themselves
// and the Python lists are never consumed by lowering; see stdlib_bind.go).
type StdlibExpansion struct {
	Name       string
	Parameters []BoundParameter
	Arguments  []BoundArgument
	ReturnType *types.Descriptor
	Steps      []BoundStdlibStep
	Result     BoundValueRef
}

// BoundStdlibCall is one bound stdlib call, mirroring BoundStdlibCall in
// bound_ir.py (spec_id / bound_args / resolved_defaults /
// validated_static_args / result_value_id / expansion). Stdlib binding is
// owned by BuildStdlibCalls (stdlib_bind.go); TypeBindFull publishes the
// per-call records keyed by call AstNodeID. SpecID is the qualified spec
// string ("std.v1.rag_answer").
type BoundStdlibCall struct {
	SpecID              string
	BoundArgs           []BoundArgument
	ResolvedDefaults    []BoundArgument
	ValidatedStaticArgs map[string]any
	ResultValueID       ValueDefID
	Expansion           *StdlibExpansion
}

// TypedBoundProgram is the sole production frontend publication consumed by
// lowering, mirroring TypedBoundProgram in bound_ir.py. Phase 3 type
// inference is not ported yet, so NodeTypes is published from the supplied
// exprTypes with an Any default for every uninferred ast node
// (expression_types.get(node_id, TypeDescriptor.any())).
//
// Maps are read-only by convention (DESIGN D6): TypeBind constructs a fresh
// map per call, so the caller's exprTypes map is never shared, but the
// descriptor pointers inside are shared with the caller per the Task 7
// immutable-descriptor convention (the same sharing Snapshot.Publish uses).
// Map consumers that need a canonical order must sort explicitly (Go map
// iteration order is unspecified).
type TypedBoundProgram struct {
	Program        *ast.Program
	Snapshot       *Snapshot
	NodeTypes      map[AstNodeID]*types.Descriptor
	EntryFunction  FunctionID
	Functions      map[FunctionID]*BoundFunction
	CodeFunctions  map[FunctionID]*BoundCodeFunction
	AggregateFacts map[AstNodeID]AggregateFact
	StdlibCalls    map[AstNodeID]BoundStdlibCall
	// CodeExpressions mirrors BoundProgram.code_expressions (binder.py
	// _build_code_expressions :149-191): one BoundCodeExpr per
	// ForeignCodeExpr, keyed by its AstNodeID. Built by TypeBindFull after
	// BuildCodeSignatures publishes the signatures onto the snapshot.
	CodeExpressions map[AstNodeID]*BoundCodeExpr
	// EnumMembers is the Go analogue of Python
	// TypedBoundProgram.binding.enum_members (bindings.py BindingSnapshot
	// field, populated from ctx.enum_members at publish time): the enum
	// member facts TypePass publishes from enum field accesses. The
	// snapshot's own EnumFacts table stays frozen at its scope-phase
	// publication; TypeBindFull merges the type-pass facts here.
	EnumMembers map[AstNodeID]EnumMemberFact
	// LoopStates is the Go analogue of Python
	// TypedBoundProgram.binding.loop_states (bindings.py BindingSnapshot
	// field, populated from ctx.loop_states at publish time): the
	// mutable-loop-state facts LoopStatePass publishes, keyed by the
	// owning WhileStmt's AstNodeID. The pipeline post-sets it after
	// TypeBindFull (task-23a wiring); the map is copied, never shared.
	LoopStates map[AstNodeID][]LoopStateFact

	// ValidatedAgents mirrors TypedBoundProgram.validated_agents
	// (bound_ir.py:253-255, read from bindings.py BindingSnapshot.
	// validated_agents — published from ctx.validated_agents at
	// publish_bindings time): the closed AgentCallContract records
	// TargetValidationPass published per validated agent(...) call, keyed
	// by the call's AstNodeID. The pipeline post-sets it after TypeBindFull
	// (task-25d wiring, LoopStates precedent); the map is copied, never
	// shared. The lowering backend resolves Agent contracts through this
	// face (context.py:465-472).
	ValidatedAgents map[AstNodeID]*AgentCallContract

	// ValidatedClassifications mirrors TypedBoundProgram.
	// validated_classifications (bound_ir.py:253-255, read from bindings.py
	// BindingSnapshot.validated_classifications — published from
	// ctx.validated_classifications at publish_bindings time): the closed
	// ValidatedClassificationContract records TargetValidationPass
	// published per validated classify(...) call, keyed by the call's
	// AstNodeID. The pipeline post-sets it after TypeBindFull (task-25e
	// wiring, ValidatedAgents precedent); the map is copied, never shared.
	// The lowering backend resolves classification contracts through this
	// face (context.py:444-453).
	ValidatedClassifications map[AstNodeID]*ValidatedClassificationContract

	// ValidatedExtractions mirrors TypedBoundProgram.
	// validated_extractions (bound_ir.py:253-255, read from bindings.py
	// BindingSnapshot.validated_extractions — published from
	// ctx.validated_extractions at publish_bindings time): the closed
	// ValidatedExtractionContract records TargetValidationPass published
	// per validated extract(...) call, keyed by the call's AstNodeID. The
	// pipeline post-sets it after TypeBindFull (task-25f wiring,
	// ValidatedAgents precedent); the map is copied, never shared. The
	// lowering backend resolves extraction contracts through this face
	// (context.py:430-442).
	ValidatedExtractions map[AstNodeID]*ValidatedExtractionContract

	// ValidatedHumanInputs mirrors TypedBoundProgram.
	// validated_human_inputs (bound_ir.py:246-251, read from bindings.py
	// BindingSnapshot.validated_human_inputs — published from
	// ctx.validated_human_inputs at publish_bindings time): the closed
	// ValidatedHumanInputContract records TargetValidationPass published
	// per validated request_input statement, keyed by the statement's
	// AstNodeID. The pipeline post-sets it after TypeBindFull (task-37
	// wiring, ValidatedExtractions precedent); the map is copied, never
	// shared. The lowering backend resolves human-input contracts through
	// this face (context.py:447-453).
	ValidatedHumanInputs map[AstNodeID]*ValidatedHumanInputContract

	// AgentProfiles is the Go analogue of
	// typed_program.target_profile.agent_profiles (context.py:396-404):
	// the pinned Agent strategy profiles the lowerer resolves a validated
	// contract against (agent_lowerer.py:20-24). The Go TypedBoundProgram
	// does not carry a full target profile (task-11 skeleton note), so the
	// pipeline post-sets the pass profile's AgentProfiles here (copied,
	// never shared).
	AgentProfiles map[string]*AgentTargetProfile
}
