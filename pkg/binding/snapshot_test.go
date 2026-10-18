package binding

// Self-built contract tests for the binding layer foundations: immutable
// post-publish snapshot, deterministic 1-based ID counters (mirroring the
// BindingBuilder._next_*_ordinal semantics in bindings.py), and the
// AstNodeID bridge to pkg/ast. The binding-pass suites in
// test_binding_snapshot.py are deferred to the binder task (they drive the
// ScopePass pipeline); see task-9 report.

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/types"
)

// ── ID counters (determinism, Python BindingBuilder semantics) ──

// idScenario exercises one deterministic "compile" of a fixed input shape:
// scope pushes, symbol defines, function registrations. Running it twice on
// fresh Chains/Builders must yield identical ID sequences (same input ->
// same IDs), unlike the process-global ast.NodeID counter.
func idScenario() (scopes []ScopeID, symbols []SymbolID, funcs []FunctionID) {
	c := NewChain()
	scopes = append(scopes, c.Global())
	scopes = append(scopes, c.Enter(ScopeFunction, "f"))
	scopes = append(scopes, c.Enter(ScopeBlock, "if"))
	c.Exit()
	c.Exit()
	scopes = append(scopes, c.Enter(ScopeFunction, "g"))

	sym := c.NewSymbolID()
	c.Define("x", sym)
	symbols = append(symbols, sym)
	sym2 := c.NewSymbolID()
	c.Define("y", sym2)
	symbols = append(symbols, sym2)

	b := NewBuilder()
	funcs = append(funcs, b.NewFunctionID(), b.NewFunctionID(), b.NewFunctionID())
	return scopes, symbols, funcs
}

func TestIDSequencesAreDeterministic(t *testing.T) {
	s1, y1, f1 := idScenario()
	s2, y2, f2 := idScenario()
	for i := range s1 {
		if s1[i] != s2[i] {
			t.Fatalf("scope id %d: first compile %v, second %v", i, s1[i], s2[i])
		}
	}
	for i := range y1 {
		if y1[i] != y2[i] {
			t.Fatalf("symbol id %d: first compile %v, second %v", i, y1[i], y2[i])
		}
	}
	for i := range f1 {
		if f1[i] != f2[i] {
			t.Fatalf("function id %d: first compile %v, second %v", i, f1[i], f2[i])
		}
	}
}

func TestIDCountersStartAtOne(t *testing.T) {
	c := NewChain()
	if c.Global() != 1 {
		t.Errorf("global scope id = %v, want 1 (Python ScopeId(1))", c.Global())
	}
	if got := c.NewSymbolID(); got != 1 {
		t.Errorf("first symbol id = %v, want 1", got)
	}
	if got := c.Enter(ScopeFunction, "f"); got != 2 {
		t.Errorf("first entered scope id = %v, want 2", got)
	}
	b := NewBuilder()
	if got := b.NewFunctionID(); got != 1 {
		t.Errorf("first function id = %v, want 1", got)
	}
}

// ── AstNodeID <-> ast.NodeID bridge ──

func TestAstNodeIDBridge(t *testing.T) {
	// The binding layer keeps its own 1-based ordinal identity per compile
	// (Python AstNodeId), bridged to the ast.NodeID counter for lookups.
	id := FromAST(ast.NodeID(5))
	if id != AstNodeID(5) {
		t.Errorf("FromAST(5) = %v, want 5", id)
	}
	if id.AST() != ast.NodeID(5) {
		t.Errorf("AST() = %v, want 5", id.AST())
	}
}

// ── Snapshot: post-publish read-only ──

func TestSnapshotReadOnlyAfterPublish(t *testing.T) {
	b := NewBuilder()
	node := FromAST(ast.NodeID(1))
	b.AstNodes[node] = NodeInfo{Kind: "IdentExpr"}
	b.Scopes[ScopeID(1)] = ScopeInfo{Decls: []SymbolID{1, 2}}
	b.Declarations[SymbolID(1)] = SymbolDecl{Name: "x", Type: types.StringType(), Scope: ScopeID(1), Kind: SymbolVariable}
	b.ExitTargets[node] = ExitTarget{Kind: ExitReturn, ScopeID: ScopeID(1)}

	snap := b.Publish()

	// Reads work after publish.
	if got, ok := snap.AstNodes().Get(node); !ok || got.Kind != "IdentExpr" {
		t.Fatalf("AstNodes().Get = (%+v, %v), want (IdentExpr, true)", got, ok)
	}
	if got, ok := snap.Scopes().Get(ScopeID(1)); !ok || len(got.Decls) != 2 {
		t.Fatalf("Scopes().Get = (%+v, %v), want 2 decls", got, ok)
	}
	if got, ok := snap.Declarations().Get(SymbolID(1)); !ok || got.Name != "x" || got.Kind != SymbolVariable {
		t.Fatalf("Declarations().Get = (%+v, %v), want symbol x/var", got, ok)
	}
	if got, ok := snap.ExitTargets().Get(node); !ok || got.Kind != ExitReturn || got.ScopeID != 1 {
		t.Fatalf("ExitTargets().Get = (%+v, %v), want return->scope 1", got, ok)
	}
	if snap.AstNodes().Len() != 1 {
		t.Errorf("AstNodes().Len = %d, want 1", snap.AstNodes().Len())
	}

	// Mutation after publish must panic (Python MappingProxyType TypeError).
	assertPanics(t, func() { snap.AstNodes().Set(node, NodeInfo{}) })
	assertPanics(t, func() { snap.Scopes().Set(ScopeID(2), ScopeInfo{}) })
	assertPanics(t, func() { snap.Declarations().Set(SymbolID(9), SymbolDecl{}) })

	// The builder keeps mutating after publish: the snapshot must not see it.
	other := FromAST(ast.NodeID(2))
	b.AstNodes[other] = NodeInfo{Kind: "IntLit"}
	b.Functions[other] = FunctionID(1)
	if _, ok := snap.AstNodes().Get(other); ok {
		t.Error("post-publish builder mutation leaked into snapshot AstNodes")
	}
	if _, ok := snap.Functions().Get(other); ok {
		t.Error("post-publish builder mutation leaked into snapshot Functions")
	}
}

func TestSnapshotEmptyMapsAreReadable(t *testing.T) {
	snap := NewBuilder().Publish()
	if snap.AstNodes().Len() != 0 || snap.Scopes().Len() != 0 || snap.Declarations().Len() != 0 {
		t.Error("fresh snapshot should have empty maps")
	}
}

// ── ID-name tables (repo style: TestDSLTypeNames in types) ──

func TestSymbolKindNames(t *testing.T) {
	cases := map[SymbolKind]string{
		SymbolVariable: "VARIABLE", SymbolFunction: "FUNCTION", SymbolParameter: "PARAMETER",
		SymbolType: "TYPE", SymbolInput: "INPUT", SymbolBuiltinFunction: "BUILTIN_FUNCTION",
		SymbolNamespace: "NAMESPACE",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("SymbolKind(%d).String() = %q, want %q", int(k), got, want)
		}
	}
}

func TestExitKindNames(t *testing.T) {
	cases := map[ExitKind]string{
		ExitReturn: "RETURN", ExitBreak: "BREAK", ExitContinue: "CONTINUE",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("ExitKind(%d).String() = %q, want %q", int(k), got, want)
		}
	}
}

func TestRootMaterializationNames(t *testing.T) {
	cases := map[RootMaterialization]string{
		RootNotNeeded: "NOT_NEEDED", RootRequired: "REQUIRED", RootForbidden: "FORBIDDEN",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("RootMaterialization(%d).String() = %q, want %q", int(k), got, want)
		}
	}
}

func TestCallTargetConstructors(t *testing.T) {
	ft := FunctionCallTarget(FunctionID(3))
	if ft.Kind != CallFunction || ft.Function != 3 || ft.Name != "" {
		t.Errorf("FunctionCallTarget = %+v, want function 3", ft)
	}
	bt := BuiltinCallTarget("llm")
	if bt.Kind != CallBuiltin || bt.Name != "llm" || bt.Function != 0 {
		t.Errorf("BuiltinCallTarget = %+v, want builtin llm", bt)
	}
	st := StdlibCallTarget("stdlib.json.loads")
	if st.Kind != CallStdlib || st.Name != "stdlib.json.loads" {
		t.Errorf("StdlibCallTarget = %+v, want stdlib name", st)
	}
}

func TestLoopStateFactCarriesData(t *testing.T) {
	fact := LoopStateFact{
		SymbolID:      SymbolID(1),
		OwnerScopeID:  ScopeID(2),
		InitializerID: FromAST(ast.NodeID(3)),
		InitialValue:  int64(42),
		TypeInfo:      types.IntType(),
		WriteSites:    []AstNodeID{FromAST(ast.NodeID(4)), FromAST(ast.NodeID(5))},
	}
	if fact.InitializerID != 3 || fact.InitialValue != int64(42) || len(fact.WriteSites) != 2 {
		t.Errorf("LoopStateFact round-trip failed: %+v", fact)
	}
}

// ── R1: slice-value deep copy at publish ──

// TestSnapshotSliceEntriesAreCopiedAtPublish guards against shallow-copy
// aliasing between the builder and the published snapshot for slice-bearing
// values (ScopeInfo.Decls, LoopStateFact.WriteSites,
// BoundCodeSignature.PhysicalPorts + CodePort.FieldPath). Element-level
// mutation (in-place write) and append on the builder's entries after
// publish must never reach the snapshot (Python tuple parity).
func TestSnapshotSliceEntriesAreCopiedAtPublish(t *testing.T) {
	b := NewBuilder()
	node := FromAST(ast.NodeID(1))

	decls := []SymbolID{1, 2}
	b.Scopes[ScopeID(1)] = ScopeInfo{Parent: 0, Decls: decls}

	writes := []AstNodeID{FromAST(ast.NodeID(4)), FromAST(ast.NodeID(5))}
	b.LoopStateFacts[node] = []LoopStateFact{{
		SymbolID: SymbolID(1), OwnerScopeID: ScopeID(2), InitializerID: node,
		InitialValue: int64(42), TypeInfo: types.IntType(), WriteSites: writes,
	}}

	ports := []CodePort{{
		ValueID: ValueDefID(1), PortName: "result", TypeInfo: types.StringType(),
		FieldPath: []string{"a"},
	}}
	b.CodeSignatures[node] = BoundCodeSignature{
		RootValue: ValueDefID(1), PhysicalPorts: ports, RootMaterialization: RootRequired,
	}

	snap := b.Publish()

	// Element-level in-place mutation on the builder's slice entries.
	decls[0] = 99
	b.Scopes[ScopeID(1)].Decls[1] = 88
	decls = append(decls, 3)
	b.LoopStateFacts[node][0].WriteSites[0] = FromAST(ast.NodeID(9))
	b.LoopStateFacts[node] = append(b.LoopStateFacts[node], LoopStateFact{})
	b.CodeSignatures[node].PhysicalPorts[0].FieldPath[0] = "mutated"
	sigEntry := b.CodeSignatures[node]
	sigEntry.PhysicalPorts = append(sigEntry.PhysicalPorts, CodePort{})
	b.CodeSignatures[node] = sigEntry

	// Snapshot must be fully immune.
	scopeInfo, ok := snap.Scopes().Get(ScopeID(1))
	if !ok || len(scopeInfo.Decls) != 2 || scopeInfo.Decls[0] != 1 || scopeInfo.Decls[1] != 2 {
		t.Errorf("Scopes().Get decls leaked: %+v (ok=%v)", scopeInfo.Decls, ok)
	}
	facts, ok := snap.LoopStateFacts().Get(node)
	if !ok || len(facts) != 1 || len(facts[0].WriteSites) != 2 ||
		facts[0].WriteSites[0] != FromAST(ast.NodeID(4)) || facts[0].WriteSites[1] != FromAST(ast.NodeID(5)) {
		t.Errorf("LoopStateFacts leaked: %+v (ok=%v)", facts, ok)
	}
	sig, ok := snap.CodeSignatures().Get(node)
	if !ok || len(sig.PhysicalPorts) != 1 || len(sig.PhysicalPorts[0].FieldPath) != 1 ||
		sig.PhysicalPorts[0].FieldPath[0] != "a" {
		t.Errorf("CodeSignatures leaked: %+v (ok=%v)", sig, ok)
	}
}

// TestCallTargetZeroValueIsExplicitNone locks the CallNone zero-kind
// contract: a zero CallTarget is never a valid binding.
func TestCallTargetZeroValueIsExplicitNone(t *testing.T) {
	var zero CallTarget
	if zero.Kind != CallNone {
		t.Errorf("zero CallTarget kind = %v, want CallNone", zero.Kind)
	}
}
