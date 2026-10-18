package binding

// Ports of lang/tests/lang/integration/test_typed_bound_pipeline.py (the
// subset reachable without the code/stdlib/emit passes — see the task-11
// report) plus self-built coverage: total node_types table with Any
// defaults, entry resolution, BoundFunction assembly, nil exprTypes safety,
// and the requireAggregates completeness guard (RuntimeError analogue).

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/types"
)

// bindProgram binds the already-parsed program (not a fresh parse) so the
// snapshot's node enumeration refers to the same AST node objects the test
// holds — NodeIDOf is pointer-based and exact.
func bindProgram(t *testing.T, prog *ast.Program) (*Snapshot, []diag.Diagnostic) {
	t.Helper()
	snap, diags := Bind(prog)
	var errs []diag.Diagnostic
	for _, d := range diags {
		if d.Severity == diag.Error {
			errs = append(errs, d)
		}
	}
	return snap, errs
}

// userRecord mirrors the `type User = { string name; int age; }` declared
// type. The Go Descriptor carries no record name (Task 7 deviation), so the
// Python assertion fact.type_info.params.name == "User" is dropped; the
// structural shape (record kind + ordered fields) is asserted instead.
func userRecord() *types.Descriptor {
	return types.RecordType("User", []types.RecordField{
		{Name: "name", Type: types.StringType()},
		{Name: "age", Type: types.IntType()},
	})
}

const recordLitSource = `
        type User = { string name; int age; }
        function main() {
            User user = {name: "Ada", age: 36};
        }
    `

// TestLocalRecordLiteralPublishesDeclaredRecordFact ports
// test_local_record_literal_publishes_declared_record_fact: a local record
// literal whose inferred type is the declared record publishes exactly one
// RECORD aggregate fact carrying the record descriptor.
func TestLocalRecordLiteralPublishesDeclaredRecordFact(t *testing.T) {
	prog := parseSource(t, recordLitSource)
	snap, errs := bindProgram(t, prog)
	if len(errs) != 0 {
		t.Fatalf("binding produced errors: %v", errs)
	}

	statement := prog.Entry().Body().Stmts()[0]
	vds, ok := statement.(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("first statement is %T, want *ast.VarDeclStmt", statement)
	}
	ml, ok := vds.Init().(*ast.MapLit)
	if !ok {
		t.Fatalf("initializer is %T, want *ast.MapLit", vds.Init())
	}
	mlID, found := astIDOf(snap, ml)
	if !found {
		t.Fatal("MapLit node not found in snapshot ast_nodes")
	}

	record := userRecord()
	tbp := TypeBind(prog, snap, map[AstNodeID]*types.Descriptor{mlID: record}, false)

	if got := len(tbp.AggregateFacts); got != 1 {
		t.Fatalf("len(AggregateFacts) = %d, want 1", got)
	}
	fact, ok := tbp.AggregateFacts[mlID]
	if !ok {
		t.Fatalf("no aggregate fact published for MapLit node %d", mlID)
	}
	if fact.Kind != AggregateLiteralKindRecord {
		t.Errorf("fact.Kind = %s, want RECORD", fact.Kind)
	}
	if fact.TypeInfo.Kind != types.Record {
		t.Fatalf("fact type base = %s, want record", fact.TypeInfo.Kind)
	}
	if got := len(fact.TypeInfo.Fields); got != 2 {
		t.Fatalf("record fields = %d, want 2", got)
	}
	if f := fact.TypeInfo.Fields[0]; f.Name != "name" || f.Type.Kind != types.String {
		t.Errorf("field[0] = %s:%s, want name:string", f.Name, f.Type.Kind)
	}
	if f := fact.TypeInfo.Fields[1]; f.Name != "age" || f.Type.Kind != types.Int {
		t.Errorf("field[1] = %s:%s, want age:int", f.Name, f.Type.Kind)
	}

	// Total type table: every ast_nodes entry is present and the MapLit
	// entry carries exactly the supplied inferred type.
	if got := len(tbp.NodeTypes); got != snap.AstNodes().Len() {
		t.Errorf("len(NodeTypes) = %d, want %d (all ast_nodes)", got, snap.AstNodes().Len())
	}
	if tbp.NodeTypes[mlID] != record {
		t.Error("NodeTypes[MapLit] does not carry the supplied record descriptor")
	}
}

// TestTypeBindPublishesTotalNodeTypesWithAnyDefaults verifies the total
// node_types table: every ast_nodes node gets an entry and nodes without an
// inferred type default to Any (expression_types.get(node_id, Any)).
func TestTypeBindPublishesTotalNodeTypesWithAnyDefaults(t *testing.T) {
	prog := parseSource(t, recordLitSource)
	snap, _ := bindProgram(t, prog)
	tbp := TypeBind(prog, snap, nil, false)

	if got := len(tbp.NodeTypes); got != snap.AstNodes().Len() {
		t.Fatalf("len(NodeTypes) = %d, want %d", got, snap.AstNodes().Len())
	}
	// Every snapshot ast node must have a type entry.
	snap.AstNodes().Range(func(id AstNodeID, _ NodeInfo) bool {
		if _, ok := tbp.NodeTypes[id]; !ok {
			t.Errorf("missing NodeTypes entry for ast node %d", id)
		}
		return true
	})
	// Uninferred nodes default to Any: the VarDeclStmt and its type ref were
	// never given inferred types.
	vds := prog.Entry().Body().Stmts()[0].(*ast.VarDeclStmt)
	vid, found := astIDOf(snap, vds)
	if !found {
		t.Fatal("VarDeclStmt not found in snapshot ast_nodes")
	}
	if got := tbp.NodeTypes[vid].Kind; got != types.Any {
		t.Errorf("NodeTypes[VarDeclStmt] = %s, want any (default)", got)
	}
	trID, found := astIDOf(snap, vds.TypeRef())
	if !found {
		t.Fatal("VarDeclStmt type ref not found in snapshot ast_nodes")
	}
	if got := tbp.NodeTypes[trID].Kind; got != types.Any {
		t.Errorf("NodeTypes[TypeRef] = %s, want any (default)", got)
	}
	if got := len(tbp.AggregateFacts); got != 0 {
		t.Errorf("len(AggregateFacts) = %d, want 0 (MapLit defaulted to Any publishes no fact)", got)
	}
}

// TestTypeBindResolvesEntryFunction verifies entry resolution: the entry
// FunctionDef's AstNodeID resolves through snapshot function_bindings to the
// published EntryFunction, and that function is present in Functions.
func TestTypeBindResolvesEntryFunction(t *testing.T) {
	prog := parseSource(t, recordLitSource)
	snap, _ := bindProgram(t, prog)
	tbp := TypeBind(prog, snap, nil, false)

	entry := prog.Entry()
	if entry == nil {
		t.Fatal("program has no entry")
	}
	eid, found := astIDOf(snap, entry)
	if !found {
		t.Fatal("entry FunctionDef not found in snapshot ast_nodes")
	}
	want, found := snap.Functions().Get(eid)
	if !found {
		t.Fatal("entry FunctionDef has no function binding")
	}
	if tbp.EntryFunction != want {
		t.Errorf("EntryFunction = %d, want %d", tbp.EntryFunction, want)
	}
	if tbp.EntryFunction == 0 {
		t.Error("EntryFunction is the zero sentinel; expected a real FunctionID")
	}
	if _, ok := tbp.Functions[tbp.EntryFunction]; !ok {
		t.Error("entry function missing from Functions map")
	}
}

// TestTypeBindAssemblesBoundFunctions verifies BoundFunction records are
// assembled from the snapshot (function_bindings + declarations + scope
// bindings): name, ordered params with declared types and symbol IDs, the
// declared return type, the body block, and the function scope.
func TestTypeBindAssemblesBoundFunctions(t *testing.T) {
	source := `
        function add(int a, int b) -> int {
            return a + b;
        }
        function main() -> int {
            return add(1, 2);
        }
    `
	prog := parseSource(t, source)
	snap, errs := bindProgram(t, prog)
	if len(errs) != 0 {
		t.Fatalf("binding produced errors: %v", errs)
	}
	tbp := TypeBind(prog, snap, nil, false)

	// Functions map keys must match the snapshot's function ID set exactly.
	var wantIDs, gotIDs []int
	snap.Functions().Range(func(_ AstNodeID, fid FunctionID) bool {
		wantIDs = append(wantIDs, int(fid))
		return true
	})
	for fid := range tbp.Functions {
		gotIDs = append(gotIDs, int(fid))
	}
	sort.Ints(wantIDs)
	sort.Ints(gotIDs)
	if fmt.Sprint(gotIDs) != fmt.Sprint(wantIDs) {
		t.Fatalf("Functions IDs = %v, want %v", gotIDs, wantIDs)
	}

	fn := func(name string) *BoundFunction {
		t.Helper()
		for _, f := range tbp.Functions {
			if f.Name == name {
				return f
			}
		}
		t.Fatalf("BoundFunction %q not found", name)
		return nil
	}

	add := fn("add")
	if add.ID == 0 {
		t.Error("add.ID is the zero sentinel")
	}
	addNode := progEntryFunction(prog, "add")
	if addNode == nil {
		t.Fatal("add FunctionDef not found in program")
	}
	if got := len(add.Params); got != 2 {
		t.Fatalf("add params = %d, want 2", got)
	}
	for i, want := range []string{"a", "b"} {
		p := add.Params[i]
		if p.Name != want {
			t.Errorf("param[%d].Name = %q, want %q", i, p.Name, want)
		}
		if p.TypeInfo.Kind != types.Int {
			t.Errorf("param[%d].TypeInfo = %s, want int", i, p.TypeInfo.Kind)
		}
		// SymbolID must match the param declaration's snapshot declaration.
		pid, found := astIDOf(snap, addNode.Params()[i])
		if !found {
			t.Fatalf("param %q node not in snapshot", want)
		}
		sid, _ := snap.DeclarationSymbols().Get(pid)
		if p.SymbolID != sid {
			t.Errorf("param[%d].SymbolID = %d, want %d", i, p.SymbolID, sid)
		}
	}
	if add.Return.Kind != types.Int {
		t.Errorf("add.Return = %s, want int", add.Return.Kind)
	}
	if add.Body == nil {
		t.Error("add.Body is nil")
	}
	aID, _ := astIDOf(snap, addNode)
	wantScope, _ := snap.ScopeBindings().Get(aID)
	if add.Scope != wantScope {
		t.Errorf("add.Scope = %d, want %d (function scope)", add.Scope, wantScope)
	}

	mainFn := fn("main")
	if got := len(mainFn.Params); got != 0 {
		t.Errorf("main params = %d, want 0", got)
	}
	if mainFn.Return.Kind != types.Int {
		t.Errorf("main.Return = %s, want int", mainFn.Return.Kind)
	}
	if mainFn.ID != tbp.EntryFunction {
		t.Errorf("main.ID = %d, want EntryFunction %d", mainFn.ID, tbp.EntryFunction)
	}
}

// TestTypeBindEmptyExprTypesDoesNotPanic drives TypeBind with a nil
// exprTypes map: every node defaults to Any, no aggregate fact is published
// (Any carries no record/map shape), and no panic escapes.
func TestTypeBindEmptyExprTypesDoesNotPanic(t *testing.T) {
	prog := parseSource(t, recordLitSource)
	snap, _ := bindProgram(t, prog)
	tbp := TypeBind(prog, snap, nil, false)

	if got := len(tbp.NodeTypes); got != snap.AstNodes().Len() {
		t.Errorf("len(NodeTypes) = %d, want %d", got, snap.AstNodes().Len())
	}
	snap.AstNodes().Range(func(id AstNodeID, _ NodeInfo) bool {
		if got := tbp.NodeTypes[id].Kind; got != types.Any {
			t.Errorf("NodeTypes[%d] = %s, want any", id, got)
		}
		return true
	})
	if got := len(tbp.AggregateFacts); got != 0 {
		t.Errorf("len(AggregateFacts) = %d, want 0", got)
	}
}

// TestTypeBindRequireAggregatesReportsIncompleteFacts verifies the
// requireAggregates guard: when a MapLit has no published fact (because its
// inferred type defaulted to Any), TypeBind fails with the RuntimeError
// analogue (panic) carrying the Python message shape.
func TestTypeBindRequireAggregatesReportsIncompleteFacts(t *testing.T) {
	prog := parseSource(t, recordLitSource)
	snap, _ := bindProgram(t, prog)

	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("TypeBind did not panic on incomplete aggregate facts")
			}
			msg := fmt.Sprint(r)
			if !strings.Contains(msg, "aggregate literal facts are incomplete") {
				t.Errorf("panic message = %q, want aggregate-literal-facts prefix", msg)
			}
			if !strings.Contains(msg, "missing=") {
				t.Errorf("panic message = %q, want missing list", msg)
			}
		}()
		TypeBind(prog, snap, nil, true)
	}()
}

// TestTypeBindRequireAggregatesAcceptsCompleteFacts verifies the guard
// passes when every MapLit publishes a fact: providing the record type for
// the MapLit makes the aggregate set complete, so TypeBind returns normally.
func TestTypeBindRequireAggregatesAcceptsCompleteFacts(t *testing.T) {
	prog := parseSource(t, recordLitSource)
	snap, _ := bindProgram(t, prog)

	ml := prog.Entry().Body().Stmts()[0].(*ast.VarDeclStmt).Init().(*ast.MapLit)
	mlID, found := astIDOf(snap, ml)
	if !found {
		t.Fatal("MapLit node not found in snapshot ast_nodes")
	}
	tbp := TypeBind(prog, snap, map[AstNodeID]*types.Descriptor{mlID: userRecord()}, true)
	if got := len(tbp.AggregateFacts); got != 1 {
		t.Fatalf("len(AggregateFacts) = %d, want 1", got)
	}
	if tbp.AggregateFacts[mlID].Kind != AggregateLiteralKindRecord {
		t.Errorf("fact.Kind = %s, want RECORD", tbp.AggregateFacts[mlID].Kind)
	}
}

// TestTypeBindDeterminism verifies the deterministic-ID contract: two
// independent Bind+TypeBind runs over the same source mint identical
// FunctionID sets and identical sorted NodeTypes/AggregateFacts keys.
func TestTypeBindDeterminism(t *testing.T) {
	prog1 := parseSource(t, recordLitSource)
	snap1, _ := bindProgram(t, prog1)
	prog2 := parseSource(t, recordLitSource)
	snap2, _ := bindProgram(t, prog2)

	ml1 := prog1.Entry().Body().Stmts()[0].(*ast.VarDeclStmt).Init().(*ast.MapLit)
	ml2 := prog2.Entry().Body().Stmts()[0].(*ast.VarDeclStmt).Init().(*ast.MapLit)
	id1, _ := astIDOf(snap1, ml1)
	id2, _ := astIDOf(snap2, ml2)
	rec := userRecord()
	tbp1 := TypeBind(prog1, snap1, map[AstNodeID]*types.Descriptor{id1: rec}, true)
	tbp2 := TypeBind(prog2, snap2, map[AstNodeID]*types.Descriptor{id2: rec}, true)

	// (a) Sorted NodeTypes keys must be identical across runs; a bare
	// map-size check would miss a node-AstNodeID swap.
	nodeKeys := func(m map[AstNodeID]*types.Descriptor) string {
		var ks []int
		for k := range m {
			ks = append(ks, int(k))
		}
		sort.Ints(ks)
		return fmt.Sprint(ks)
	}
	if a, b := nodeKeys(tbp1.NodeTypes), nodeKeys(tbp2.NodeTypes); a != b {
		t.Errorf("sorted NodeTypes keys differ: %s vs %s", a, b)
	}
	// (b) Sorted AggregateFacts keys must be identical.
	aggKeys := func(m map[AstNodeID]AggregateFact) string {
		var ks []int
		for k := range m {
			ks = append(ks, int(k))
		}
		sort.Ints(ks)
		return fmt.Sprint(ks)
	}
	if a, b := aggKeys(tbp1.AggregateFacts), aggKeys(tbp2.AggregateFacts); a != b {
		t.Errorf("sorted AggregateFacts keys differ: %s vs %s", a, b)
	}
	// (c) Entry resolution must be identical.
	if tbp1.EntryFunction != tbp2.EntryFunction {
		t.Errorf("EntryFunction differs: %d vs %d", tbp1.EntryFunction, tbp2.EntryFunction)
	}
	// (d) The name->FunctionID mapping must be identical; asserting only the
	// ID set would pass if two functions swapped FunctionIDs.
	nameIDs := func(m map[FunctionID]*BoundFunction) string {
		var pairs []string
		for _, f := range m {
			pairs = append(pairs, fmt.Sprintf("%s=%d", f.Name, f.ID))
		}
		sort.Strings(pairs)
		return strings.Join(pairs, ",")
	}
	if a, b := nameIDs(tbp1.Functions), nameIDs(tbp2.Functions); a != b {
		t.Errorf("name->FunctionID mappings differ: %s vs %s", a, b)
	}
}

// TestTypeBindMapLiteralPublishesMapFact covers the MAP branch of
// buildAggregateFacts: a map-typed literal publishes an
// AggregateLiteralKindMap fact carrying the map descriptor.
func TestTypeBindMapLiteralPublishesMapFact(t *testing.T) {
	source := `
        function main() {
            map<string, int> m = {"a": 1, "b": 2};
        }
    `
	prog := parseSource(t, source)
	snap, errs := bindProgram(t, prog)
	if len(errs) != 0 {
		t.Fatalf("binding produced errors: %v", errs)
	}
	stmt := prog.Entry().Body().Stmts()[0].(*ast.VarDeclStmt)
	ml, ok := stmt.Init().(*ast.MapLit)
	if !ok {
		t.Fatalf("initializer is %T, want *ast.MapLit", stmt.Init())
	}
	mlID, found := astIDOf(snap, ml)
	if !found {
		t.Fatal("MapLit node not found in snapshot ast_nodes")
	}

	mapType := types.MapType(types.StringType(), types.IntType())
	tbp := TypeBind(prog, snap, map[AstNodeID]*types.Descriptor{mlID: mapType}, false)

	if got := len(tbp.AggregateFacts); got != 1 {
		t.Fatalf("len(AggregateFacts) = %d, want 1", got)
	}
	fact, ok := tbp.AggregateFacts[mlID]
	if !ok {
		t.Fatalf("no aggregate fact published for MapLit node %d", mlID)
	}
	if fact.Kind != AggregateLiteralKindMap {
		t.Errorf("fact.Kind = %s, want MAP", fact.Kind)
	}
	if fact.TypeInfo.Kind != types.Map {
		t.Fatalf("fact type base = %s, want map", fact.TypeInfo.Kind)
	}
	if fact.TypeInfo.Key.Kind != types.String || fact.TypeInfo.Val.Kind != types.Int {
		t.Errorf("fact map shape = %s->%s, want string->int", fact.TypeInfo.Key.Kind, fact.TypeInfo.Val.Kind)
	}
}

// TestNewAggregateFactRejectsKindTypeMismatch covers the AggregateFact
// constructor guard (Python AggregateLiteralFact.__post_init__ ValueError):
// a MAP fact with a non-map type and a RECORD fact with a non-record type
// must panic, while agreeing constructions succeed.
func TestNewAggregateFactRejectsKindTypeMismatch(t *testing.T) {
	mismatches := []struct {
		kind AggregateLiteralKind
		typ  *types.Descriptor
	}{
		{AggregateLiteralKindMap, types.StringType()},
		{AggregateLiteralKindMap, types.RecordType("U", nil)},
		{AggregateLiteralKindRecord, types.MapType(types.StringType(), types.IntType())},
		{AggregateLiteralKindRecord, types.IntType()},
	}
	for _, c := range mismatches {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("NewAggregateFact(%s, %s) did not panic", c.kind, c.typ.Kind)
				}
			}()
			NewAggregateFact(c.kind, c.typ)
		}()
	}
	agreeing := []struct {
		kind AggregateLiteralKind
		typ  *types.Descriptor
	}{
		{AggregateLiteralKindMap, types.MapType(types.StringType(), types.IntType())},
		{AggregateLiteralKindRecord, types.RecordType("U", []types.RecordField{{Name: "x", Type: types.StringType()}})},
	}
	for _, c := range agreeing {
		if f := NewAggregateFact(c.kind, c.typ); f.Kind != c.kind {
			t.Errorf("NewAggregateFact(%s, %s).Kind = %s, want %s", c.kind, c.typ.Kind, f.Kind, c.kind)
		}
	}
}

// progEntryFunction resolves a named function from the program (entry
// included) for test-local node lookups.
func progEntryFunction(prog *ast.Program, name string) *ast.FunctionDef {
	if e := prog.Entry(); e != nil && e.Name() == name {
		return e
	}
	for _, f := range prog.Functions() {
		if f.Name() == name {
			return f
		}
	}
	return nil
}
