// Package passes hosts the DSL frontend pass pipeline, mirroring
// src/nclang/lang/passes/base.py. The pipeline owns the fixed 15-stage
// ordering, error short-circuit semantics and the stage registry; concrete
// pass implementations land with their stage tasks (scope/definite-init/
// effect in Task 14, the remaining passes in Tasks 15+). The 58 diagnostic
// codes and Severity/Diagnostic live in pkg/diag (Task 2) and are
// reused here — passes does not redefine them (task-12 brief correction #1).
package passes

import (
	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/target"
	"github.com/nodecoda/ncoda/pkg/token"
	"github.com/nodecoda/ncoda/pkg/tool"
	"github.com/nodecoda/ncoda/pkg/types"
)

// Operations is a read-only lookup view over the target operation registry,
// mirroring operation_registry.py OperationRegistry.lookup. The spec table
// (type_ops.go, Task 15 T6) replaces the Task 14 name-only set with the
// full OperationSpec face (parameters/required/result_kind/result_type/
// policies) so the type pass can check operation calls; Has keeps the
// EffectPass classification semantics (lookup(name) is not None).
// Task 10 keeps the operation-builtin registration deviation (agent/
// extract_text/extract/classify unregistered, SymbolID offset 4, llm/etc.
// silent-overwrite differences); Task 25 re-runs parity — no "+4"
// compensation anywhere. In Go the schema-extraction/classification type
// argument paths are unreachable until then.
type Operations struct {
	specs map[string]*OperationSpec
}

// Lookup returns the operation spec for name (Python
// OperationRegistry.lookup(name)); nil when name is not a registered
// operation.
func (o *Operations) Lookup(name string) *OperationSpec {
	if o == nil {
		return nil
	}
	return o.specs[name]
}

// Has reports whether name is a registered operation (Python
// OperationRegistry.lookup(name) is not None).
func (o *Operations) Has(name string) bool {
	return o.Lookup(name) != nil
}

// defaultOperations returns the reference default registry
// (operation_registry.py DEFAULT_OPERATION_REGISTRY: llm, http, agent,
// knowledge, extract_text, extract, classify — tool() was deleted from the
// language, TOOL-DIRECT-CALL-FORM decision 5), field-for-field with
// _builtin_specs() (type_ops.go defaultOperationSpecs).
func defaultOperations() *Operations {
	return &Operations{specs: defaultOperationSpecs()}
}

// TargetProfile identifies the lowering target. The full profile
// (representability rules) belongs to the target-validation task; this task
// carries the identity, MaxParallelism (target_profile.py:36 default 10),
// the operation-spec registry so the scope/effect passes classify operation
// builtins and the type pass checks operation calls
// (base.py DSLPassContext.target_profile.operations), and the Phase-0
// capability manifest (target_profile.py TargetProfile.capabilities, Task
// 17C1) the CapabilityGatePass consults. A nil profile means the default
// (dify-1.16-graphon-0.6, see DefaultTargetProfile).
type TargetProfile struct {
	Name           string
	Operations     *Operations
	MaxParallelism int

	// Capabilities mirrors target_profile.py TargetProfile.capabilities:
	// the immutable Phase-0 capability manifest. Profiles built by
	// DefaultTargetProfile always carry it; the gate falls back to the
	// default manifest when nil (defensive — the reference never sees a
	// capabilities-less profile).
	Capabilities *CapabilityManifest

	// AgentProfiles mirrors target_profile.py TargetProfile.agent_profiles
	// (target_profile.py:38-47): the pinned Agent strategy profiles, keyed
	// by profile alias (Python requires mapping keys to equal profile
	// aliases; Go validates that in the binding constructor
	// NewAgentTargetProfile). The default target carries an empty map, so
	// agent(...) calls are rejected by target validation unless a profile
	// is installed — matching the reference's empty MappingProxyType.
	AgentProfiles map[string]*binding.AgentTargetProfile

	// ParameterExtractorFunctionCallModels mirrors target_profile.py:36:
	// the model identities certified for function_call extraction strategy
	// (a frozenset in Python, a set here).
	ParameterExtractorFunctionCallModels map[string]bool

	// ParameterExtractorMemoryMaxTurns mirrors target_profile.py:37: the
	// locked per-turn budget for extraction memory.
	ParameterExtractorMemoryMaxTurns int

	// Codec is the target's physical-type mapping face (I3): the
	// parameter-extractor codec target validation consults for EXACT-loss
	// extraction-field certification. It is platform-neutral (pkg/target);
	// a platform adapter supplies its own implementation through this field.
	// DefaultTargetProfile wires target.DefaultCodec(), the canonical language
	// mapping; a nil Codec falls back to it defensively (the Capabilities
	// nil-guard precedent).
	Codec target.Codec
}

// DefaultTargetProfile returns the identity of the reference default target
// (base.py get_target_profile() default), the analogue of the reference's
// lazy _default_target_profile(). Name mirrors the reference target
// identity DEFAULT_TARGET_NAME ("dify-1.16-graphon-0.6", target_profile.py
// :27) — the Task 25d report records that earlier tasks used the
// placeholder "default"; the name surfaces in the agent profile rejection
// diagnostic, so it is aligned with the oracle.
func DefaultTargetProfile() *TargetProfile {
	return &TargetProfile{
		Name:                                 "dify-1.16-graphon-0.6",
		Operations:                           defaultOperations(),
		MaxParallelism:                       10,
		Capabilities:                         DefaultCapabilityManifest(),
		AgentProfiles:                        map[string]*binding.AgentTargetProfile{},
		ParameterExtractorFunctionCallModels: map[string]bool{"deepseek/deepseek-chat": true},
		ParameterExtractorMemoryMaxTurns:     99,
		Codec:                                target.DefaultCodec(),
	}
}

// Context is the shared state threaded through every pass, mirroring
// base.py DSLPassContext (base.py:200-243). Surfaces available in the Go
// port:
//
//   - Diagnostics: collected diagnostics (DSLPassContext.diagnostics +
//     emit_diagnostic/has_errors/errors);
//   - Bindings: the published binding snapshot, the analogue of
//     publish_bindings() output — produced by the scope phase (ScopePass /
//     Task 10 binder) at its pipeline position;
//   - ExprTypes: per-node inferred types keyed in the snapshot's AstNodeID
//     space, the container TypePass (Task 15) populates and TypeBind
//     consumes (base.py ctx.expr_types);
//   - EffectFacts: the immutable effect publication the EffectPass writes
//     (base.py ctx.effect_facts). Python defaults the field to None
//     (base.py:208); the Go Context uses the shared empty table as a safer
//     default so consumers never observe nil;
//   - Profile: the selected TargetProfile.
//
// Surfaces without a Go counterpart yet (contracts) are deferred to the
// passes that first need them, per the incremental pipeline policy.
type Context struct {
	Diagnostics []diag.Diagnostic
	Bindings    *binding.Snapshot
	ExprTypes   map[binding.AstNodeID]*types.Descriptor
	EffectFacts *binding.EffectFacts
	Profile     *TargetProfile

	// ToolRegistry is the optional semantic Tool capability registry
	// (provider/operation -> declared outputs). When non-nil, TypePass types
	// tool() results from the registry's declared outputs instead of the
	// open-world default {result: any}.
	ToolRegistry *tool.Registry

	// NamedTypes mirrors DSLPassContext.named_types (base.py:207): the
	// named-type environment published by the scope phase (Task 15 T1).
	// ScopePass copies the snapshot's table here after Bind; TypePass
	// resolves type references through types.TypeRefToDescriptor with this
	// lookup.
	NamedTypes map[string]*types.Descriptor

	// VarTypes is the mutable symbol-type surface (Task 15 T2): the Go
	// analogue of Python symbol.type_info mutation (type_pass.py:299-301
	// and friends). TypePass writes inferred types here; reads in the pass
	// and in binding.TypeBind (bound-function parameter assembly) prefer
	// this map over the snapshot's frozen declaration types.
	VarTypes map[binding.SymbolID]*types.Descriptor

	// EnumMembers mirrors DSLPassContext.enum_members (base.py:221): the
	// enum-member facts TypePass publishes from enum field accesses
	// (type_pass.py:855-859). binding.TypeBind merges this into the typed
	// bound program's EnumMembers publication (Python
	// BindingSnapshot.enum_members).
	EnumMembers map[binding.AstNodeID]binding.EnumMemberFact

	// DirectCodeSymbols mirrors DSLPassContext.direct_code_symbols
	// (base.py:288): SymbolIDs whose value is a direct Code output,
	// marked by TypePass via MarkDirectCodeSymbol (type_pass.py:299-301).
	DirectCodeSymbols map[binding.SymbolID]bool

	// ConversationVarIDs mirrors DSLPassContext.conversation_var_ids
	// (base.py:222): the SymbolIDs of conversation variables. ScopePass
	// copies the binder's published set here after Bind (Task 16 D1);
	// LoopStatePass uses it to skip conversation variables, which are not
	// loop state (loop_state_pass.py:105).
	ConversationVarIDs map[binding.SymbolID]bool

	// LoopStates mirrors DSLPassContext.loop_states (base.py:221): the
	// mutable-workflow-state facts LoopStatePass publishes, keyed by the
	// owning while loop's AstNodeID (each value is the owner's fact list in
	// symbol order, mirroring the Python tuple).
	LoopStates map[binding.AstNodeID][]binding.LoopStateFact

	// ExtractionContracts mirrors DSLPassContext.extraction_contracts
	// (base.py:211): the target-neutral ExtractionContract records
	// TypePass resolves for validated extract<T>(...) calls (type_pass.py
	// :1154-1162), keyed by the call's AstNodeID. TargetValidationPass
	// consumes this face to validate the call against the schema.
	ExtractionContracts map[binding.AstNodeID]*binding.ExtractionContract

	// StructuralCodeRoots mirrors DSLPassContext.mark_structural_code_root /
	// bindings.py _structural_code_roots (bindings.py:807-812): the Code
	// expressions whose structural root value must be materialized as a
	// whole. Python's final publish_bindings() consumes this in
	// _build_code_signatures (bindings.py:815,903); the Go codeSignatures
	// skeleton is owned by the lowering tasks (task-11 note), so this map is
	// the handoff input.
	StructuralCodeRoots map[binding.AstNodeID]bool

	// LoopPortTypes mirrors the roundtrip multi-port aggregate contract:
	// for-expr declared symbols (coze loop/batch outputs) → port name → port
	// element type. visitForExpr publishes per-port joins here; field access
	// on the aggregate (agg_<loop>.<port>) resolves through this face.
	LoopPortTypes map[binding.SymbolID]map[string]*types.Descriptor

	// ValidatedHumanInputs mirrors DSLPassContext.validated_human_inputs
	// (base.py:217-219): the closed Human Input contracts TargetValidationPass
	// publishes for validated request_input statements, keyed by the
	// statement's AstNodeID. The lowering tasks consume this face.
	ValidatedHumanInputs map[binding.AstNodeID]*binding.ValidatedHumanInputContract

	// ValidatedAgents mirrors DSLPassContext.validated_agents
	// (base.py:220): the closed Agent call contracts TargetValidationPass
	// publishes for validated agent(...) calls, keyed by the call's
	// AstNodeID. The lowering tasks consume this face through the typed
	// bound program (pipeline post-set).
	ValidatedAgents map[binding.AstNodeID]*binding.AgentCallContract

	// ValidatedClassifications mirrors DSLPassContext.
	// validated_classifications (base.py:220): the closed classification
	// contracts TargetValidationPass publishes for validated classify(...)
	// calls, keyed by the call's AstNodeID. The lowering tasks consume this
	// face through the typed bound program (pipeline post-set).
	ValidatedClassifications map[binding.AstNodeID]*binding.ValidatedClassificationContract

	// ValidatedExtractions mirrors DSLPassContext.validated_extractions
	// (base.py:220): the closed extraction contracts TargetValidationPass
	// publishes for validated extract(...) calls, keyed by the call's
	// AstNodeID. The lowering tasks consume this face through the typed
	// bound program (pipeline post-set).
	ValidatedExtractions map[binding.AstNodeID]*binding.ValidatedExtractionContract
}

// NewContext constructs a Context for one compilation. A nil profile selects
// the default target; EffectFacts starts at EmptyEffectFacts() mirroring
// DSLPassContext.effect_facts's initial value (base.py:200-243).
func NewContext(profile *TargetProfile) *Context {
	if profile == nil {
		profile = DefaultTargetProfile()
	}
	return &Context{
		ExprTypes:                map[binding.AstNodeID]*types.Descriptor{},
		EffectFacts:              binding.EmptyEffectFacts(),
		Profile:                  profile,
		NamedTypes:               map[string]*types.Descriptor{},
		LoopPortTypes:            map[binding.SymbolID]map[string]*types.Descriptor{},
		VarTypes:                 map[binding.SymbolID]*types.Descriptor{},
		EnumMembers:              map[binding.AstNodeID]binding.EnumMemberFact{},
		DirectCodeSymbols:        map[binding.SymbolID]bool{},
		ConversationVarIDs:       map[binding.SymbolID]bool{},
		LoopStates:               map[binding.AstNodeID][]binding.LoopStateFact{},
		ExtractionContracts:      map[binding.AstNodeID]*binding.ExtractionContract{},
		StructuralCodeRoots:      map[binding.AstNodeID]bool{},
		ValidatedHumanInputs:     map[binding.AstNodeID]*binding.ValidatedHumanInputContract{},
		ValidatedAgents:          map[binding.AstNodeID]*binding.AgentCallContract{},
		ValidatedClassifications: map[binding.AstNodeID]*binding.ValidatedClassificationContract{},
		ValidatedExtractions:     map[binding.AstNodeID]*binding.ValidatedExtractionContract{},
	}
}

// MarkDirectCodeSymbol records that symID's value is a direct Code output,
// mirroring DSLPassContext.mark_direct_code_symbol (base.py:282-285,
// type_pass.py:301).
func (c *Context) MarkDirectCodeSymbol(symID binding.SymbolID) {
	c.DirectCodeSymbols[symID] = true
}

// MarkStructuralCodeRoot records that the Code expression at nodeID must be
// materialized as a whole structural root, mirroring
// DSLPassContext.mark_structural_code_root (bindings.py:807-812).
func (c *Context) MarkStructuralCodeRoot(nodeID binding.AstNodeID) {
	c.StructuralCodeRoots[nodeID] = true
}

// Emit appends a diagnostic, mirroring DSLPassContext.emit_diagnostic.
// A zero span leaves the location fields empty (programmatic diagnostics).
func (c *Context) Emit(code diag.Code, severity diag.Severity, message string, span ast.Span) {
	d := diag.Diagnostic{Code: code, Severity: severity, Message: message}
	if span != (ast.Span{}) {
		d.SetSpan(token.Span(span))
	}
	c.Diagnostics = append(c.Diagnostics, d)
}

// EmitClassified 发出带**错误类别**的诊断（D1-D3，2026-09-13 用户拍板）：
// class = language/platform/internal/config，capability = 稳定能力标识，
// pending = 可约未实现。Emit 保持原样（类别缺省即 language）。
func (c *Context) EmitClassified(code diag.Code, severity diag.Severity, message string, span ast.Span, class string, capability diag.CapabilityID, pending bool) {
	d := diag.Diagnostic{Code: code, Severity: severity, Message: message,
		Class: class, Capability: capability, Pending: pending}
	if span != (ast.Span{}) {
		d.SetSpan(token.Span(span))
	}
	c.Diagnostics = append(c.Diagnostics, d)
}

// HasErrors reports whether any collected diagnostic is an error, mirroring
// DSLPassContext.has_errors.
func (c *Context) HasErrors() bool {
	return len(c.Errors()) > 0
}

// Errors returns the collected error diagnostics in emission order, mirroring
// DSLPassContext.errors.
func (c *Context) Errors() []diag.Diagnostic {
	var errs []diag.Diagnostic
	for _, d := range c.Diagnostics {
		if d.Severity == diag.Error {
			errs = append(errs, d)
		}
	}
	return errs
}
