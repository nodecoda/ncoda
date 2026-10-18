package binding

// Semantic identity contracts and the immutable BindingSnapshot, ported from
// src/nclang/lang/bindings.py (read-only oracle). Field parity follows
// DESIGN §5 (AstNodes/Scopes/Declarations/Functions/ExprTypes/
// LoopStateFacts/ExitTargets/EnumFacts/CodeSignatures) plus the Python
// BindingSnapshot fields constructible with the current layer's types
// (declaration_symbols, reference_symbols, scope_bindings, call_targets).
//
// Deferred to later tasks (require types owned by other layers):
// effect_facts, validated_extractions/classifications/human_inputs/agents,
// and the DESIGN "AggregateFacts" item (undefined in the reference; possibly
// a mislabel for effect_facts — see task-9 report).

import (
	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/types"
)

// SymbolKind classifies a declared symbol, mirroring SymbolKind in
// symbol_table.py (VARIABLE/FUNCTION/PARAMETER/TYPE/INPUT/
// BUILTIN_FUNCTION/NAMESPACE). The task-9 brief lists
// var/function/type/enumMember/const/codeFunc; the reference implementation
// uses the symbol_table.py set — deviation recorded in the report, the
// reference wins.
type SymbolKind int

const (
	SymbolVariable SymbolKind = iota
	SymbolFunction
	SymbolParameter
	SymbolType
	SymbolInput
	SymbolBuiltinFunction
	SymbolNamespace
)

// String returns the Python enum name (SymbolKind.X.name).
func (k SymbolKind) String() string {
	switch k {
	case SymbolVariable:
		return "VARIABLE"
	case SymbolFunction:
		return "FUNCTION"
	case SymbolParameter:
		return "PARAMETER"
	case SymbolType:
		return "TYPE"
	case SymbolInput:
		return "INPUT"
	case SymbolBuiltinFunction:
		return "BUILTIN_FUNCTION"
	case SymbolNamespace:
		return "NAMESPACE"
	}
	return "UNKNOWN"
}

// NamespaceKind discriminates SymbolNamespace symbols (I2, post-parity):
// builtin namespaces (std, json) expose record-typed members through field
// access and their qualified calls type through generic function
// application; import namespaces (capability providers) resolve members
// exclusively through the qualified-capability-call interception. The
// explicit kind replaces the earlier Type.Kind == Any heuristic, so the
// binder, type pass and IR share one discriminant instead of three
// independent conventions.
type NamespaceKind int

const (
	// NamespaceNone is the zero value: the symbol is not a namespace.
	NamespaceNone NamespaceKind = iota
	// NamespaceBuiltin marks the compiler-provided namespaces (std, json).
	NamespaceBuiltin
	// NamespaceImport marks namespaces declared by capability imports.
	NamespaceImport
)

// String returns a stable debug name.
func (k NamespaceKind) String() string {
	switch k {
	case NamespaceBuiltin:
		return "BUILTIN"
	case NamespaceImport:
		return "IMPORT"
	}
	return "NONE"
}

// ExitKind classifies a control-flow exit, mirroring ExitKind in
// bindings.py (RETURN/BREAK/CONTINUE).
type ExitKind int

const (
	ExitReturn ExitKind = iota
	ExitBreak
	ExitContinue
)

// String returns the Python enum value ("RETURN"/"BREAK"/"CONTINUE").
func (k ExitKind) String() string {
	switch k {
	case ExitReturn:
		return "RETURN"
	case ExitBreak:
		return "BREAK"
	case ExitContinue:
		return "CONTINUE"
	}
	return "UNKNOWN"
}

// ExitTarget records where an exit statement resolves, mirroring
// ExitTarget{kind, scope_id} in bindings.py. The task-9 brief adds a Value
// bool field; the reference has none — deviation recorded, reference wins.
type ExitTarget struct {
	Kind    ExitKind
	ScopeID ScopeID
}

// LoopState classifies the break/continue affordance of the innermost loop
// surrounding a scope. This is a task-brief-required enum (LoopNone/
// LoopBreakable/LoopContinuable); the reference tracks the equivalent
// information implicitly via the scope pass's loop-scope stack.
type LoopState int

const (
	LoopNone LoopState = iota
	LoopBreakable
	LoopContinuable
)

// LoopStateFact proves that one mutable value is owned by exactly one Loop,
// mirroring LoopStateFact in bindings.py. InitialValue carries the Python
// union str | int | float | bool (Go: any).
type LoopStateFact struct {
	SymbolID      SymbolID
	OwnerScopeID  ScopeID
	InitializerID AstNodeID
	InitialValue  any
	TypeInfo      *types.Descriptor
	WriteSites    []AstNodeID
}

// NodeInfo mirrors AstNodeInfo{span, kind} in bindings.py. The zero ast.Span
// plays the role of the Python None span.
type NodeInfo struct {
	Span ast.Span
	Kind string
}

// ScopeInfo is the snapshot-facing record for one scope (DESIGN §5):
// Parent is the enclosing scope (0 = none, i.e. the global scope) and Decls
// lists the symbol IDs declared directly in this scope.
type ScopeInfo struct {
	Parent ScopeID
	Decls  []SymbolID
}

// SymbolDecl is the snapshot-facing record for one declared symbol,
// mirroring SymbolDeclaration in bindings.py. It carries Name/Type/Scope/
// Kind plus the reference's mutability flag, which the task-9 brief deferred
// to the symbol-table task (this task ports symbol_table.py Symbol, whose
// is_mutable field feeds SymbolDeclaration.mutability).
type SymbolDecl struct {
	Name    string
	Type    *types.Descriptor
	Scope   ScopeID
	Kind    SymbolKind
	Mutable bool
	// NS is meaningful only when Kind == SymbolNamespace: the explicit
	// builtin/import discriminant (I2).
	NS NamespaceKind
}

// ValueDefID identifies one code value definition, mirroring the frozen
// ValueDefId dataclass in bindings.py.
type ValueDefID int

// RootMaterialization mirrors RootMaterialization in bindings.py.
type RootMaterialization int

const (
	RootNotNeeded RootMaterialization = iota
	RootRequired
	RootForbidden
)

// String returns the Python enum value ("NOT_NEEDED"/"REQUIRED"/"FORBIDDEN").
func (m RootMaterialization) String() string {
	switch m {
	case RootNotNeeded:
		return "NOT_NEEDED"
	case RootRequired:
		return "REQUIRED"
	case RootForbidden:
		return "FORBIDDEN"
	}
	return "UNKNOWN"
}

// CodePort mirrors CodePort in bindings.py: one physical port of a bound
// code value.
type CodePort struct {
	ValueID   ValueDefID
	PortName  string
	TypeInfo  *types.Descriptor
	FieldPath []string
}

// BoundCodeSignature mirrors BoundCodeSignature in bindings.py.
type BoundCodeSignature struct {
	RootValue           ValueDefID
	PhysicalPorts       []CodePort
	RootMaterialization RootMaterialization
}

// EnumMemberFact mirrors EnumMemberFact in bindings.py. The reference's
// __post_init__ validation (well-formed enum, member membership, canonical
// wire value) is deferred to the binder task that constructs facts.
type EnumMemberFact struct {
	EnumType  *types.Descriptor
	Member    string
	WireValue string
}

// CallTargetKind discriminates CallTarget, mirroring the Python union
// CallTarget = FunctionId | BuiltinId | StdlibSpecId.
type CallTargetKind int

const (
	// CallNone is the explicit zero value: no target. A zero CallTarget is
	// never a valid binding; always build targets with one of the three
	// constructors below.
	CallNone CallTargetKind = iota
	CallFunction
	CallBuiltin
	CallStdlib
)

// CallTarget is the Go form of the Python CallTarget union: a user function
// (Function), a builtin (Name = builtin id), or a stdlib spec (Name =
// qualified stdlib spec id).
type CallTarget struct {
	Kind     CallTargetKind
	Function FunctionID
	Name     string
}

// FunctionCallTarget targets a user-declared function.
func FunctionCallTarget(id FunctionID) CallTarget {
	return CallTarget{Kind: CallFunction, Function: id}
}

// BuiltinCallTarget targets a builtin by name (Python BuiltinId(value)).
func BuiltinCallTarget(name string) CallTarget { return CallTarget{Kind: CallBuiltin, Name: name} }

// StdlibCallTarget targets a stdlib spec by qualified name (Python
// StdlibSpecId(value)).
func StdlibCallTarget(name string) CallTarget { return CallTarget{Kind: CallStdlib, Name: name} }

// RoMap is a read-only view over a snapshot map, mirroring Python's
// MappingProxyType: lookups and iteration only. Set panics so any mutation
// attempt after publication is loud (the frozen-snapshot contract).
//
// Accessors wrap the Snapshot's internal maps directly (zero-copy, O(1)):
// after Publish defensively clones every map, the Snapshot owns the maps
// exclusively and no setter mutates them in place (SetCodeSignatures
// replaces the whole map reference), so no mutation path survives
// publication and a wrapper can never observe a changing map. newRoMap
// (defensive copy) is only used while a snapshot is still under
// construction, e.g. the effect snapshot build.
type RoMap[K comparable, V any] struct {
	m map[K]V
}

func newRoMap[K comparable, V any](m map[K]V) *RoMap[K, V] {
	cp := make(map[K]V, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return &RoMap[K, V]{m: cp}
}

// Len reports the number of entries.
func (r *RoMap[K, V]) Len() int { return len(r.m) }

// Get returns the value bound to k.
func (r *RoMap[K, V]) Get(k K) (V, bool) {
	v, found := r.m[k]
	return v, found
}

// Has reports whether k is bound.
func (r *RoMap[K, V]) Has(k K) bool {
	_, found := r.m[k]
	return found
}

// Keys returns the bound keys. Order is unspecified (Go map iteration);
// sort when order matters. Python's insertion order is not preserved by
// design — callers that need a canonical order must sort explicitly.
func (r *RoMap[K, V]) Keys() []K {
	out := make([]K, 0, len(r.m))
	for k := range r.m {
		out = append(out, k)
	}
	return out
}

// Range visits every entry; iteration stops when fn returns false.
func (r *RoMap[K, V]) Range(fn func(K, V) bool) {
	for k, v := range r.m {
		if !fn(k, v) {
			return
		}
	}
}

// Set mutates the map; it always panics because snapshot maps are frozen
// after publication (Python MappingProxyType raises TypeError on item
// assignment). There is intentionally no non-panicking mutation path.
func (r *RoMap[K, V]) Set(K, V) {
	panic("binding: snapshot is read-only after publish")
}

// Snapshot is the sole immutable publication of Phase 1 semantic identity,
// mirroring BindingSnapshot in bindings.py. Fields are unexported and read
// back through read-only accessors (DESIGN D6); the zero Snapshot (from
// NewBuilder().Publish()) is valid and empty.
//
// Retention note: the nodeIDs map is keyed by AST node pointers, so a
// published Snapshot keeps the whole source tree alive (each key pins one
// node). This is intentional and harmless for a per-compile lifetime — the
// TypedBoundProgram publication already pairs a Snapshot with its
// *ast.Program — but retaining a Snapshot long after its Program is
// dropped holds the tree too; prefer compile-scoped retention.
type Snapshot struct {
	astNodes           map[AstNodeID]NodeInfo
	nodeIDs            map[ast.Node]AstNodeID
	scopes             map[ScopeID]ScopeInfo
	declarations       map[SymbolID]SymbolDecl
	functions          map[AstNodeID]FunctionID
	exprTypes          map[AstNodeID]*types.Descriptor
	loopStateFacts     map[AstNodeID][]LoopStateFact
	exitTargets        map[AstNodeID]ExitTarget
	enumFacts          map[AstNodeID]EnumMemberFact
	codeSignatures     map[AstNodeID]BoundCodeSignature
	callTargets        map[AstNodeID]CallTarget
	declarationSymbols map[AstNodeID]SymbolID
	referenceSymbols   map[AstNodeID]SymbolID
	scopeBindings      map[AstNodeID]ScopeID
	namedTypes         map[string]*types.Descriptor
	// conversationVarIDs mirrors DSLPassContext.conversation_var_ids (Task 16):
	// the SymbolIDs of conversation variables registered by the binder.
	conversationVarIDs map[SymbolID]bool
}

// AstNodes returns the read-only node-information map (Python ast_nodes).
func (s *Snapshot) AstNodes() *RoMap[AstNodeID, NodeInfo] {
	return &RoMap[AstNodeID, NodeInfo]{m: s.astNodes}
}

// NodeIDOf resolves the AstNodeID the binder assigned to the AST node n
// (Python frozen syntax nodes carry their AstNodeId; the Go snapshot
// publishes the same per-compile enumeration). The lookup is exact and
// deterministic — every node the binder enumerated, including zero-span
// nodes such as ParamDecls, resolves to its unique AstNodeID.
func (s *Snapshot) NodeIDOf(n ast.Node) (AstNodeID, bool) {
	id, found := s.nodeIDs[n]
	return id, found
}

// Scopes returns the read-only scope tree (DESIGN §5 Scopes, Python
// scope_parents refined with per-scope declarations).
func (s *Snapshot) Scopes() *RoMap[ScopeID, ScopeInfo] {
	return &RoMap[ScopeID, ScopeInfo]{m: s.scopes}
}

// Declarations returns the read-only symbol table (Python
// symbol_declarations).
func (s *Snapshot) Declarations() *RoMap[SymbolID, SymbolDecl] {
	return &RoMap[SymbolID, SymbolDecl]{m: s.declarations}
}

// Functions returns the read-only node->function binding (Python
// function_bindings).
func (s *Snapshot) Functions() *RoMap[AstNodeID, FunctionID] {
	return &RoMap[AstNodeID, FunctionID]{m: s.functions}
}

// ExprTypes returns the read-only node->type conclusion map (DESIGN §5 and
// D7; the typed-bound-program layer may expose the same conclusions).
func (s *Snapshot) ExprTypes() *RoMap[AstNodeID, *types.Descriptor] {
	return &RoMap[AstNodeID, *types.Descriptor]{m: s.exprTypes}
}

// LoopStateFacts returns the read-only loop-state facts per owner node
// (Python loop_states).
func (s *Snapshot) LoopStateFacts() *RoMap[AstNodeID, []LoopStateFact] {
	return &RoMap[AstNodeID, []LoopStateFact]{m: s.loopStateFacts}
}

// ExitTargets returns the read-only exit targets (Python exit_targets).
func (s *Snapshot) ExitTargets() *RoMap[AstNodeID, ExitTarget] {
	return &RoMap[AstNodeID, ExitTarget]{m: s.exitTargets}
}

// EnumFacts returns the read-only enum-member facts (DESIGN §5 EnumFacts,
// Python enum_members).
func (s *Snapshot) EnumFacts() *RoMap[AstNodeID, EnumMemberFact] {
	return &RoMap[AstNodeID, EnumMemberFact]{m: s.enumFacts}
}

// CodeSignatures returns the read-only bound code signatures (DESIGN §5
// CodeSignatures, Python code_signatures).
func (s *Snapshot) CodeSignatures() *RoMap[AstNodeID, BoundCodeSignature] {
	return &RoMap[AstNodeID, BoundCodeSignature]{m: s.codeSignatures}
}

// SetCodeSignatures publishes the code signatures onto an already-published
// snapshot (one-shot, before any reader). Python builds code_signatures
// inside BindingBuilder.publish with the pass-final structural_code_roots
// (bindings.py:815,903); the Go snapshot is published before the pass loop
// (scope phase), so the code-binding task computes the signatures at
// TypeBindFull time — the Go analogue of the pass-final publication — and
// installs them here. BuildStdlibCalls and BuildCodeExpressions read them
// through CodeSignatures(), so SetCodeSignatures must run first (TypeBindFull
// enforces the order). The map is defensively cloned like Publish, so later
// caller mutation never reaches the snapshot.
func (s *Snapshot) SetCodeSignatures(signatures map[AstNodeID]BoundCodeSignature) {
	s.codeSignatures = copyMapValues(signatures, cloneCodeSignature)
}

// CallTargets returns the read-only call targets (Python call_targets).
func (s *Snapshot) CallTargets() *RoMap[AstNodeID, CallTarget] {
	return &RoMap[AstNodeID, CallTarget]{m: s.callTargets}
}

// DeclarationSymbols returns the read-only node->declared-symbol map (Python
// declaration_symbols).
func (s *Snapshot) DeclarationSymbols() *RoMap[AstNodeID, SymbolID] {
	return &RoMap[AstNodeID, SymbolID]{m: s.declarationSymbols}
}

// ReferenceSymbols returns the read-only node->referenced-symbol map (Python
// reference_symbols).
func (s *Snapshot) ReferenceSymbols() *RoMap[AstNodeID, SymbolID] {
	return &RoMap[AstNodeID, SymbolID]{m: s.referenceSymbols}
}

// ScopeBindings returns the read-only node->scope map (Python
// scope_bindings).
func (s *Snapshot) ScopeBindings() *RoMap[AstNodeID, ScopeID] {
	return &RoMap[AstNodeID, ScopeID]{m: s.scopeBindings}
}

// NamedTypes returns the read-only named-type environment (Python
// BindingSnapshot.named_types, the projection of TYPE symbol declarations).
// Task 15 T1 publishes the binder's named-type table so later passes
// (TypePass) resolve type references without re-deriving the environment.
func (s *Snapshot) NamedTypes() *RoMap[string, *types.Descriptor] {
	return &RoMap[string, *types.Descriptor]{m: s.namedTypes}
}

// ConversationVarIDs returns the read-only conversation-variable symbol
// set (Python DSLPassContext.conversation_var_ids, base.py:222). The
// binder registers conversation declarations in the global scope and
// records their SymbolIDs here; LoopStatePass uses the set to skip
// conversation variables (loop_state_pass.py:105).
func (s *Snapshot) ConversationVarIDs() *RoMap[SymbolID, bool] {
	return &RoMap[SymbolID, bool]{m: s.conversationVarIDs}
}

// Builder is the private mutable accumulator discarded after publishing a
// Snapshot, mirroring BindingBuilder in bindings.py. Map fields are exported
// for the binder task to fill during the passes; Publish is one-shot and
// defensively copies every map, so later Builder mutation never reaches the
// published snapshot.
type Builder struct {
	AstNodes           map[AstNodeID]NodeInfo
	NodeIDs            map[ast.Node]AstNodeID
	Scopes             map[ScopeID]ScopeInfo
	Declarations       map[SymbolID]SymbolDecl
	Functions          map[AstNodeID]FunctionID
	ExprTypes          map[AstNodeID]*types.Descriptor
	LoopStateFacts     map[AstNodeID][]LoopStateFact
	ExitTargets        map[AstNodeID]ExitTarget
	EnumFacts          map[AstNodeID]EnumMemberFact
	CodeSignatures     map[AstNodeID]BoundCodeSignature
	CallTargets        map[AstNodeID]CallTarget
	DeclarationSymbols map[AstNodeID]SymbolID
	ReferenceSymbols   map[AstNodeID]SymbolID
	ScopeBindings      map[AstNodeID]ScopeID
	// NamedTypes is the named-type environment the binder accumulated while
	// registering enum/record declarations (binder.go namedTypes). Task 15
	// T1 copies it in at the end of binder.run so the published snapshot
	// carries the same environment Python exposes as
	// DSLPassContext.named_types / BindingSnapshot.named_types.
	NamedTypes map[string]*types.Descriptor
	// ConversationVarIDs is the binder's collected conversation-variable
	// symbol set (binder.conversationVarIDs); Task 16 publishes it so the
	// scope pass can copy it into Context.ConversationVarIDs (Python
	// DSLPassContext.conversation_var_ids).
	ConversationVarIDs map[SymbolID]bool

	nextFunctionID FunctionID
}

// NewBuilder returns a Builder with empty maps and a function counter
// starting at 1 (Python BindingBuilder._next_function_ordinal).
func NewBuilder() *Builder {
	return &Builder{
		AstNodes:           map[AstNodeID]NodeInfo{},
		NodeIDs:            map[ast.Node]AstNodeID{},
		Scopes:             map[ScopeID]ScopeInfo{},
		Declarations:       map[SymbolID]SymbolDecl{},
		Functions:          map[AstNodeID]FunctionID{},
		ExprTypes:          map[AstNodeID]*types.Descriptor{},
		LoopStateFacts:     map[AstNodeID][]LoopStateFact{},
		ExitTargets:        map[AstNodeID]ExitTarget{},
		EnumFacts:          map[AstNodeID]EnumMemberFact{},
		CodeSignatures:     map[AstNodeID]BoundCodeSignature{},
		CallTargets:        map[AstNodeID]CallTarget{},
		DeclarationSymbols: map[AstNodeID]SymbolID{},
		ReferenceSymbols:   map[AstNodeID]SymbolID{},
		ScopeBindings:      map[AstNodeID]ScopeID{},
		NamedTypes:         map[string]*types.Descriptor{},
		ConversationVarIDs: map[SymbolID]bool{},
		nextFunctionID:     1,
	}
}

// NewFunctionID mints the next function ID (1-based, deterministic per
// builder), mirroring Python BindingBuilder._next_function_ordinal.
func (b *Builder) NewFunctionID() FunctionID {
	id := b.nextFunctionID
	b.nextFunctionID++
	return id
}

// Publish freezes the accumulated facts into an immutable Snapshot. Every
// map is copied, and every slice-bearing value (ScopeInfo.Decls,
// LoopStateFact.WriteSites, BoundCodeSignature.PhysicalPorts and the nested
// CodePort.FieldPath) is cloned element-wise, so the Builder (and any caller
// holding the same map values) can keep mutating - including in-place
// element writes and appends on slice entries - without affecting the
// published snapshot. This mirrors the Python side, where these sequence
// fields are tuples (immutable).
func (b *Builder) Publish() *Snapshot {
	return &Snapshot{
		astNodes:           copyMap(b.AstNodes),
		nodeIDs:            copyMap(b.NodeIDs),
		scopes:             copyMapValues(b.Scopes, cloneScopeInfo),
		declarations:       copyMap(b.Declarations),
		functions:          copyMap(b.Functions),
		exprTypes:          copyMap(b.ExprTypes),
		loopStateFacts:     copyMapValues(b.LoopStateFacts, cloneLoopStateFacts),
		exitTargets:        copyMap(b.ExitTargets),
		enumFacts:          copyMap(b.EnumFacts),
		codeSignatures:     copyMapValues(b.CodeSignatures, cloneCodeSignature),
		callTargets:        copyMap(b.CallTargets),
		declarationSymbols: copyMap(b.DeclarationSymbols),
		referenceSymbols:   copyMap(b.ReferenceSymbols),
		scopeBindings:      copyMap(b.ScopeBindings),
		namedTypes:         copyMap(b.NamedTypes),
		conversationVarIDs: copyMap(b.ConversationVarIDs),
	}
}

func copyMap[K comparable, V any](m map[K]V) map[K]V {
	cp := make(map[K]V, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// copyMapValues copies m and applies clone to every value, deep-cloning any
// slice fields the value carries.
func copyMapValues[K comparable, V any](m map[K]V, clone func(V) V) map[K]V {
	cp := make(map[K]V, len(m))
	for k, v := range m {
		cp[k] = clone(v)
	}
	return cp
}

// cloneSlice returns a fresh copy of s (nil stays nil), so the snapshot
// never shares a backing array with the builder.
func cloneSlice[T any](s []T) []T {
	if s == nil {
		return nil
	}
	out := make([]T, len(s))
	copy(out, s)
	return out
}

func cloneScopeInfo(v ScopeInfo) ScopeInfo {
	v.Decls = cloneSlice(v.Decls)
	return v
}

func cloneLoopStateFacts(v []LoopStateFact) []LoopStateFact {
	out := cloneSlice(v)
	for i := range out {
		out[i].WriteSites = cloneSlice(out[i].WriteSites)
	}
	return out
}

func cloneCodeSignature(v BoundCodeSignature) BoundCodeSignature {
	v.PhysicalPorts = cloneSlice(v.PhysicalPorts)
	for i := range v.PhysicalPorts {
		v.PhysicalPorts[i].FieldPath = cloneSlice(v.PhysicalPorts[i].FieldPath)
	}
	return v
}
