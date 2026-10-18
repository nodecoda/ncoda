package binding

// BuildStdlibCalls unit tests (Task 24b), oracle-locked against the Python
// _build_stdlib_calls (binder.py:195-260): rag_answer / fetch_and_summarize
// binding shape, static-argument collection, dynamic BoundValueRef vs
// BoundLiteral argument shapes, two-step expansion shape, unbound cases
// (non-stdlib calls, arg-count mismatch) and cross-run determinism of the
// published StdlibCalls key sequence.

import (
	"reflect"
	"sort"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/types"
)

// bindTypedProgram parses, binds and type-binds a source program, failing on
// any binding error.
func bindTypedProgram(t *testing.T, src string) *TypedBoundProgram {
	t.Helper()
	prog := parseSource(t, src)
	snap, errs := bindProgram(t, prog)
	if len(errs) != 0 {
		t.Fatalf("binding produced errors: %v", errs)
	}
	return TypeBind(prog, snap, nil, false)
}

// stdlibCallIDCollector collects the AstNodeID of every stdlib call site in
// the program (used by the determinism test to lock the AstNodeID-order
// processing guarantee of BuildStdlibCalls).
type stdlibCallIDCollector struct {
	snap *Snapshot
	ids  []AstNodeID
}

func (c *stdlibCallIDCollector) Visit(n ast.Node) ast.Visitor {
	if call, ok := n.(*ast.CallExpr); ok && StdlibCallPath(call) != "" {
		if id, found := c.snap.NodeIDOf(call); found {
			c.ids = append(c.ids, id)
		}
	}
	return c
}

// callFinder collects the first CallExpr whose stdlib path matches wantPath.
type callFinder struct {
	wantPath string
	found    *ast.CallExpr
}

func (f *callFinder) Visit(n ast.Node) ast.Visitor {
	if f.found != nil {
		return nil
	}
	if call, ok := n.(*ast.CallExpr); ok && StdlibCallPath(call) == f.wantPath {
		f.found = call
		return nil
	}
	return f
}

// findStdlibCall locates a call by its qualified stdlib path and returns the
// call node plus its snapshot AstNodeID.
func findStdlibCall(t *testing.T, tbp *TypedBoundProgram, path string) (*ast.CallExpr, AstNodeID) {
	t.Helper()
	finder := &callFinder{wantPath: path}
	ast.Walk(finder, tbp.Program)
	if finder.found == nil {
		t.Fatalf("no stdlib call %q found in program", path)
	}
	id, ok := astIDOf(tbp.Snapshot, finder.found)
	if !ok {
		t.Fatalf("stdlib call %q not found in snapshot ast_nodes", path)
	}
	return finder.found, id
}

// singleStdlibCall builds the typed program for src, asserts exactly one
// bound stdlib call of the given path, and returns its record.
func singleStdlibCall(t *testing.T, src, path string) (*TypedBoundProgram, BoundStdlibCall, *ast.CallExpr, AstNodeID) {
	t.Helper()
	tbp := bindTypedProgram(t, src)
	call, id := findStdlibCall(t, tbp, path)
	if len(tbp.StdlibCalls) != 1 {
		t.Fatalf("len(StdlibCalls) = %d, want 1", len(tbp.StdlibCalls))
	}
	bc, ok := tbp.StdlibCalls[id]
	if !ok {
		t.Fatalf("no BoundStdlibCall published for call node %d", id)
	}
	return tbp, bc, call, id
}

func TestBuildStdlibCallsRagAnswer(t *testing.T) {
	src := `function main(string query) -> string {
    return std.v1.rag_answer(query, "ds-001,ds-002", "openai/gpt-4o");
}`
	_, bc, _, _ := singleStdlibCall(t, src, "std.v1.rag_answer")

	if bc.SpecID != "std.v1.rag_answer" {
		t.Errorf("SpecID = %q, want std.v1.rag_answer", bc.SpecID)
	}
	// 3 supplied args (query ref + 2 static literals), no defaults (v1 all
	// required), static args collected from the literal positions.
	if len(bc.BoundArgs) != 3 {
		t.Fatalf("len(BoundArgs) = %d, want 3", len(bc.BoundArgs))
	}
	if len(bc.ResolvedDefaults) != 0 {
		t.Errorf("len(ResolvedDefaults) = %d, want 0 (v1 all required)", len(bc.ResolvedDefaults))
	}
	if len(bc.ValidatedStaticArgs) != 2 {
		t.Fatalf("ValidatedStaticArgs = %v, want 2 entries", bc.ValidatedStaticArgs)
	}
	if bc.ValidatedStaticArgs["dataset_ids"] != "ds-001,ds-002" {
		t.Errorf("static dataset_ids = %v, want ds-001,ds-002", bc.ValidatedStaticArgs["dataset_ids"])
	}
	if bc.ValidatedStaticArgs["model"] != "openai/gpt-4o" {
		t.Errorf("static model = %v, want openai/gpt-4o", bc.ValidatedStaticArgs["model"])
	}

	// Argument shapes: query is a dynamic reference, dataset_ids/model are
	// static literals.
	if bc.BoundArgs[0].ValueRef == nil {
		t.Fatal("BoundArgs[0].ValueRef = nil, want a BoundValueRef (query ident)")
	}
	if bc.BoundArgs[0].ValueRef.Value.Kind != BoundValueIDSymbol {
		t.Errorf("BoundArgs[0] value kind = %v, want BoundValueIDSymbol", bc.BoundArgs[0].ValueRef.Value.Kind)
	}
	if len(bc.BoundArgs[0].ValueRef.FieldPath) != 0 {
		t.Errorf("BoundArgs[0] field path = %v, want []", bc.BoundArgs[0].ValueRef.FieldPath)
	}
	if bc.BoundArgs[0].TypeInfo.Kind != types.String {
		t.Errorf("BoundArgs[0] type = %s, want string", bc.BoundArgs[0].TypeInfo.Kind)
	}
	if bc.BoundArgs[1].Literal == nil || bc.BoundArgs[1].Literal.Value != "ds-001,ds-002" {
		t.Errorf("BoundArgs[1] literal = %+v, want ds-001,ds-002", bc.BoundArgs[1])
	}
	if bc.BoundArgs[2].Literal == nil || bc.BoundArgs[2].Literal.Value != "openai/gpt-4o" {
		t.Errorf("BoundArgs[2] literal = %+v, want openai/gpt-4o", bc.BoundArgs[2])
	}

	// Expansion: name, one dynamic parameter (query), one dynamic argument,
	// two steps (knowledge + llm), result is the llm result symbol.
	exp := bc.Expansion
	if exp == nil {
		t.Fatal("Expansion = nil, want a two-step plan")
	}
	if exp.Name != "std_v1_rag_answer" {
		t.Errorf("expansion name = %q, want std_v1_rag_answer", exp.Name)
	}
	if len(exp.Parameters) != 1 || exp.Parameters[0].Name != "query" {
		t.Fatalf("expansion parameters = %+v, want [query]", exp.Parameters)
	}
	if exp.Parameters[0].TypeInfo.Kind != types.String {
		t.Errorf("expansion parameter type = %s, want string", exp.Parameters[0].TypeInfo.Kind)
	}
	if exp.Parameters[0].SymbolID == 0 {
		t.Error("expansion parameter symbol = 0, want a minted symbol id")
	}
	if len(exp.Arguments) != 1 || exp.Arguments[0].ValueRef == nil {
		t.Fatalf("expansion arguments = %+v, want [query ref]", exp.Arguments)
	}
	if exp.ReturnType.Kind != types.String {
		t.Errorf("expansion return type = %s, want string", exp.ReturnType.Kind)
	}
	if len(exp.Steps) != 2 {
		t.Fatalf("len(steps) = %d, want 2", len(exp.Steps))
	}

	// Step 1: knowledge with dataset_ids config and the query parameter ref.
	knowledge := exp.Steps[0]
	if knowledge.Kind != "knowledge" {
		t.Errorf("step[0].Kind = %q, want knowledge", knowledge.Kind)
	}
	if knowledge.Config["dataset_ids"] != "ds-001,ds-002" {
		t.Errorf("knowledge config = %v, want dataset_ids ds-001,ds-002", knowledge.Config)
	}
	queryRef, ok := knowledge.Inputs["query"]
	if !ok {
		t.Fatal("knowledge inputs lack query")
	}
	if queryRef.Value.Kind != BoundValueIDSymbol || queryRef.Value.SymbolID != exp.Parameters[0].SymbolID {
		t.Errorf("knowledge query ref = %+v, want parameter symbol %d", queryRef.Value, exp.Parameters[0].SymbolID)
	}
	if knowledge.ResultType.Kind != types.Array || knowledge.ResultType.Elem.Kind != types.Map {
		t.Errorf("knowledge result type = %s, want array(map(string, any))", knowledge.ResultType)
	}
	if knowledge.ResultSymbol == 0 || knowledge.ResultValue == 0 {
		t.Error("knowledge result symbol/value = 0, want minted ids")
	}
	if knowledge.ResultSymbol == exp.Parameters[0].SymbolID {
		t.Error("knowledge result symbol collides with the parameter symbol")
	}

	// Step 2: llm with model config and the two-part prompt.
	llm := exp.Steps[1]
	if llm.Kind != "llm" {
		t.Errorf("step[1].Kind = %q, want llm", llm.Kind)
	}
	if llm.Config["model"] != "openai/gpt-4o" {
		t.Errorf("llm config = %v, want model openai/gpt-4o", llm.Config)
	}
	if llm.ResultType.Kind != types.String {
		t.Errorf("llm result type = %s, want string", llm.ResultType.Kind)
	}
	if len(llm.Messages) != 2 {
		t.Fatalf("len(llm messages) = %d, want 2", len(llm.Messages))
	}
	system := llm.Messages[0]
	if system.Role != "system" || len(system.Parts) != 1 || system.Parts[0].Text != "Answer using the retrieved context." {
		t.Errorf("system message = %+v, want [Answer using the retrieved context.]", system)
	}
	user := llm.Messages[1]
	if user.Role != "user" {
		t.Errorf("user message role = %q, want user", user.Role)
	}
	if len(user.Parts) != 4 {
		t.Fatalf("len(user parts) = %d, want 4", len(user.Parts))
	}
	if user.Parts[0].Text != "Context:\n" {
		t.Errorf("user part[0] = %+v, want text Context:\\n", user.Parts[0])
	}
	if user.Parts[1].Ref == nil || user.Parts[1].Ref.Value.Kind != BoundValueIDSymbol ||
		user.Parts[1].Ref.Value.SymbolID != knowledge.ResultSymbol {
		t.Errorf("user part[1] = %+v, want ref to knowledge result symbol %d", user.Parts[1], knowledge.ResultSymbol)
	}
	if user.Parts[2].Text != "\n\nQuestion:\n" {
		t.Errorf("user part[2] = %+v, want text \\n\\nQuestion:\\n", user.Parts[2])
	}
	if user.Parts[3].Ref == nil || user.Parts[3].Ref.Value.SymbolID != exp.Parameters[0].SymbolID {
		t.Errorf("user part[3] = %+v, want ref to the query parameter symbol", user.Parts[3])
	}
	if exp.Result.Value.Kind != BoundValueIDSymbol || exp.Result.Value.SymbolID != llm.ResultSymbol {
		t.Errorf("expansion result = %+v, want the llm result symbol %d", exp.Result.Value, llm.ResultSymbol)
	}
	if bc.ResultValueID == 0 || bc.ResultValueID == knowledge.ResultValue || bc.ResultValueID == llm.ResultValue {
		t.Errorf("ResultValueID = %d, want a distinct minted value id", bc.ResultValueID)
	}
}

func TestBuildStdlibCallsFetchAndSummarize(t *testing.T) {
	src := `function main() -> string {
    return std.v1.fetch_and_summarize("https://example.com/article", "openai/gpt-4o");
}`
	_, bc, _, _ := singleStdlibCall(t, src, "std.v1.fetch_and_summarize")

	if bc.SpecID != "std.v1.fetch_and_summarize" {
		t.Errorf("SpecID = %q, want std.v1.fetch_and_summarize", bc.SpecID)
	}
	if len(bc.BoundArgs) != 2 {
		t.Fatalf("len(BoundArgs) = %d, want 2", len(bc.BoundArgs))
	}
	if len(bc.ValidatedStaticArgs) != 2 {
		t.Fatalf("ValidatedStaticArgs = %v, want 2 entries", bc.ValidatedStaticArgs)
	}
	if bc.ValidatedStaticArgs["url"] != "https://example.com/article" {
		t.Errorf("static url = %v, want https://example.com/article", bc.ValidatedStaticArgs["url"])
	}
	if bc.ValidatedStaticArgs["model"] != "openai/gpt-4o" {
		t.Errorf("static model = %v, want openai/gpt-4o", bc.ValidatedStaticArgs["model"])
	}

	// Both parameters are static, so the expansion carries no dynamic
	// parameters/arguments.
	exp := bc.Expansion
	if exp == nil {
		t.Fatal("Expansion = nil, want a two-step plan")
	}
	if len(exp.Parameters) != 0 {
		t.Errorf("expansion parameters = %+v, want [] (all static)", exp.Parameters)
	}
	if len(exp.Arguments) != 0 {
		t.Errorf("expansion arguments = %+v, want [] (all static)", exp.Arguments)
	}
	if len(exp.Steps) != 2 {
		t.Fatalf("len(steps) = %d, want 2", len(exp.Steps))
	}

	http := exp.Steps[0]
	if http.Kind != "http" {
		t.Errorf("step[0].Kind = %q, want http", http.Kind)
	}
	if http.Config["method"] != "GET" || http.Config["url"] != "https://example.com/article" {
		t.Errorf("http config = %v, want method GET url https://example.com/article", http.Config)
	}
	if http.ResultType.Kind != types.String {
		t.Errorf("http result type = %s, want string", http.ResultType.Kind)
	}

	llm := exp.Steps[1]
	if llm.Kind != "llm" {
		t.Errorf("step[1].Kind = %q, want llm", llm.Kind)
	}
	if len(llm.Messages) != 1 {
		t.Fatalf("len(llm messages) = %d, want 1", len(llm.Messages))
	}
	user := llm.Messages[0]
	if user.Role != "user" || len(user.Parts) != 2 {
		t.Fatalf("user message = %+v, want role user with 2 parts", user)
	}
	if user.Parts[0].Text != "Summarize this content:\n" {
		t.Errorf("user part[0] = %+v, want text Summarize this content:\\n", user.Parts[0])
	}
	if user.Parts[1].Ref == nil || user.Parts[1].Ref.Value.SymbolID != http.ResultSymbol {
		t.Errorf("user part[1] = %+v, want ref to the http result symbol", user.Parts[1])
	}
	if exp.Result.Value.SymbolID != llm.ResultSymbol {
		t.Errorf("expansion result = %+v, want the llm result symbol", exp.Result.Value)
	}
}

func TestBuildStdlibCallsLiteralArgumentShape(t *testing.T) {
	// The dynamic query passed as a string literal binds a BoundLiteral
	// (not a reference), with the string type.
	src := `function main() -> string {
    return std.v1.rag_answer("hello", "ds-001", "openai/gpt-4o");
}`
	_, bc, _, _ := singleStdlibCall(t, src, "std.v1.rag_answer")
	if bc.BoundArgs[0].Literal == nil {
		t.Fatal("BoundArgs[0].Literal = nil, want a BoundLiteral")
	}
	if bc.BoundArgs[0].Literal.Value != "hello" {
		t.Errorf("BoundArgs[0] literal = %v, want hello", bc.BoundArgs[0].Literal.Value)
	}
	if bc.BoundArgs[0].TypeInfo.Kind != types.String {
		t.Errorf("BoundArgs[0] type = %s, want string", bc.BoundArgs[0].TypeInfo.Kind)
	}
	if len(bc.Expansion.Arguments) != 1 || bc.Expansion.Arguments[0].Literal == nil {
		t.Fatalf("expansion arguments = %+v, want one literal", bc.Expansion.Arguments)
	}
}

func TestBuildStdlibCallsNonStdlibUnbound(t *testing.T) {
	src := `function main(string q) -> string {
    let a = llm("openai/gpt-4o", "hi");
    return a.text;
}`
	tbp := bindTypedProgram(t, src)
	if len(tbp.StdlibCalls) != 0 {
		t.Errorf("StdlibCalls = %v, want empty for non-stdlib calls", tbp.StdlibCalls)
	}
}

func TestBuildStdlibCallsArgCountMismatchUnbound(t *testing.T) {
	// Too few args: required_count=3 > len(args)=1 → skipped.
	short := `function main(string q) -> string {
    return std.v1.rag_answer(q);
}`
	if tbp := bindTypedProgram(t, short); len(tbp.StdlibCalls) != 0 {
		t.Errorf("short-arg StdlibCalls = %v, want empty", tbp.StdlibCalls)
	}
	// Too many args: len(args)=4 > len(params)=3 → skipped.
	long := `function main(string q) -> string {
    return std.v1.rag_answer(q, "d", "m", "x");
}`
	if tbp := bindTypedProgram(t, long); len(tbp.StdlibCalls) != 0 {
		t.Errorf("long-arg StdlibCalls = %v, want empty", tbp.StdlibCalls)
	}
}

func TestBuildStdlibCallsKeySequenceDeterministic(t *testing.T) {
	src := `function main(string q, string r) -> string {
    let a = std.v1.rag_answer(q, "ds-001", "openai/gpt-4o");
    let b = std.v1.fetch_and_summarize("https://example.com/a", "openai/gpt-4o");
    return r;
}`
	first := bindTypedProgram(t, src)
	second := bindTypedProgram(t, src)
	if len(first.StdlibCalls) != 2 || len(second.StdlibCalls) != 2 {
		t.Fatalf("StdlibCalls sizes = %d/%d, want 2/2", len(first.StdlibCalls), len(second.StdlibCalls))
	}
	keysOf := func(m map[AstNodeID]BoundStdlibCall) []int {
		out := make([]int, 0, len(m))
		for id := range m {
			out = append(out, int(id))
		}
		sort.Ints(out)
		return out
	}
	k1, k2 := keysOf(first.StdlibCalls), keysOf(second.StdlibCalls)
	if !reflect.DeepEqual(k1, k2) {
		t.Errorf("key sequences differ: %v vs %v", k1, k2)
	}
	// Lock the AstNodeID-order processing guarantee: the emitted key
	// sequence must equal the sorted AstNodeID list of every stdlib call
	// site in the source program.
	collector := &stdlibCallIDCollector{snap: first.Snapshot}
	ast.Walk(collector, first.Program)
	siteIDs := make([]int, 0, len(collector.ids))
	for _, id := range collector.ids {
		siteIDs = append(siteIDs, int(id))
	}
	sort.Ints(siteIDs)
	if !reflect.DeepEqual(k1, siteIDs) {
		t.Errorf("key sequence = %v, want sorted stdlib call-site ids %v (AstNodeID-order processing)", k1, siteIDs)
	}
	// Full record equality across runs (deterministic minting, no map
	// iteration leaking into output).
	for id, bc1 := range first.StdlibCalls {
		bc2, ok := second.StdlibCalls[id]
		if !ok {
			t.Fatalf("run 2 lacks stdlib call for node %d", id)
		}
		if !reflect.DeepEqual(bc1, bc2) {
			t.Errorf("stdlib call %d differs across runs:\nrun1: %+v\nrun2: %+v", id, bc1, bc2)
		}
	}
}
