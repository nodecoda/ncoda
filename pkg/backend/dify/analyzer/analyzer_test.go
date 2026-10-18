package analyzer

// TDD RED→GREEN for the analyzer port (Task 36). Every expectation is
// oracle-captured from the reference implementation: the Python test suite
// tests/workflow/unit/test_analyzer.py (12 cases) plus combination
// scenarios probed with .venv/bin/python (analyze() on parsed YAML graphs);
// messages, node_id/path and pass ordering are verbatim.

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
)

// ── graph builder helpers ────────────────────────────────────────────

type gb struct {
	ast *dify.WorkflowAST
}

func newGB() *gb {
	return &gb{ast: &dify.WorkflowAST{Graph: dify.GraphNode{}}}
}

func (b *gb) node(id, ntype string, data dify.NodeData) *gb {
	b.ast.Graph.Nodes = append(b.ast.Graph.Nodes, dify.NodeDecl{ID: id, NodeType: ntype, Data: data})
	return b
}

func (b *gb) edge(id, source, target string) *gb {
	b.ast.Graph.Edges = append(b.ast.Graph.Edges, dify.EdgeDecl{ID: id, Source: source, Target: target})
	return b
}

func (b *gb) edgeHandle(id, source, target, handle string) *gb {
	b.ast.Graph.Edges = append(b.ast.Graph.Edges, dify.EdgeDecl{ID: id, Source: source, Target: target, SourceHandle: handle})
	return b
}

func start() *dify.StartDataNode { return &dify.StartDataNode{} }
func endNode() *dify.OutputNode  { return &dify.OutputNode{} }
func llm() *dify.LLMDataNode     { return &dify.LLMDataNode{} }

func codes(result *AnalysisResult) []string {
	var out []string
	for _, d := range result.Diagnostics {
		out = append(out, d.Code)
	}
	return out
}

func codeSet(result *AnalysisResult) map[string]bool {
	out := map[string]bool{}
	for _, d := range result.Diagnostics {
		out[d.Code] = true
	}
	return out
}

func find(result *AnalysisResult, code string) *Diagnostic {
	for i := range result.Diagnostics {
		if result.Diagnostics[i].Code == code {
			return &result.Diagnostics[i]
		}
	}
	return nil
}

// ── TDD cases (Python test_analyzer.py + oracle combination probes) ──

func TestAnalyzeMinimal(t *testing.T) {
	ast := newGB().
		node("s", "start", start()).
		node("e", "end", endNode()).
		edge("e1", "s", "e").ast
	r := Analyze(ast)
	if !r.Ok() {
		t.Fatalf("minimal must pass, got diagnostics: %v", codes(r))
	}
}

func TestAnalyzeNoStartNode(t *testing.T) {
	ast := newGB().node("e", "end", endNode()).ast
	r := Analyze(ast)
	d := find(r, "NO_START_NODE")
	if d == nil {
		t.Fatalf("want NO_START_NODE, got %v", codes(r))
	}
	if d.Severity != "error" || d.Message != "Workflow has no start node" {
		t.Fatalf("diag = %+v", d)
	}
}

func TestAnalyzeMultipleStartNodes(t *testing.T) {
	ast := newGB().
		node("s1", "start", start()).
		node("s2", "start", start()).
		node("e", "end", endNode()).
		edge("e1", "s1", "e").
		edge("e2", "s2", "e").ast
	r := Analyze(ast)
	d := find(r, "MULTIPLE_START_NODES")
	if d == nil {
		t.Fatalf("want MULTIPLE_START_NODES, got %v", codes(r))
	}
	want := "Workflow has 2 start nodes (expected 1)"
	if d.Message != want {
		t.Fatalf("message = %q, want %q", d.Message, want)
	}
	// Oracle: entry = s1, so s2 is unreachable (warning) — pass ordering:
	// MULTIPLE_START_NODES (pass 2) before UNREACHABLE_NODE (pass 3).
	u := find(r, "UNREACHABLE_NODE")
	if u == nil || u.NodeID != "s2" {
		t.Fatalf("want UNREACHABLE_NODE on s2, got %v", codes(r))
	}
	if len(r.Diagnostics) != 2 {
		t.Fatalf("want exactly 2 diagnostics, got %v", codes(r))
	}
	if r.Diagnostics[0].Code != "MULTIPLE_START_NODES" || r.Diagnostics[1].Code != "UNREACHABLE_NODE" {
		t.Fatalf("order = %v", codes(r))
	}
}

func TestAnalyzeUnreachableNode(t *testing.T) {
	ast := newGB().
		node("s", "start", start()).
		node("orphan", "llm", llm()).
		node("e", "end", endNode()).
		edge("e1", "s", "e").ast
	r := Analyze(ast)
	d := find(r, "UNREACHABLE_NODE")
	if d == nil {
		t.Fatalf("want UNREACHABLE_NODE, got %v", codes(r))
	}
	want := "Node 'orphan' (llm) is not reachable from start"
	if d.Message != want || d.NodeID != "orphan" {
		t.Fatalf("diag = %+v, want message %q", d, want)
	}
	if d.Severity != "warning" {
		t.Fatalf("severity = %s, want warning", d.Severity)
	}
	if !r.Ok() {
		t.Fatalf("unreachable is a warning: analysis must be ok")
	}
}

func TestAnalyzeInvalidEdgeSource(t *testing.T) {
	ast := newGB().
		node("s", "start", start()).
		edge("e1", "ghost", "s").ast
	r := Analyze(ast)
	d := find(r, "EDGE_INVALID_SOURCE")
	if d == nil {
		t.Fatalf("want EDGE_INVALID_SOURCE, got %v", codes(r))
	}
	want := "Edge 'e1' references unknown source node 'ghost'"
	if d.Message != want || d.NodeID != "e1" || d.Path != "edge.source" {
		t.Fatalf("diag = %+v, want message %q", d, want)
	}
	// Pass 1 early exit: no later passes run.
	if len(r.Diagnostics) != 1 {
		t.Fatalf("want only the pass-1 error (early exit), got %v", codes(r))
	}
}

func TestAnalyzeInvalidEdgeTarget(t *testing.T) {
	ast := newGB().
		node("s", "start", start()).
		edge("e1", "s", "ghost").ast
	r := Analyze(ast)
	d := find(r, "EDGE_INVALID_TARGET")
	if d == nil {
		t.Fatalf("want EDGE_INVALID_TARGET, got %v", codes(r))
	}
	want := "Edge 'e1' references unknown target node 'ghost'"
	if d.Message != want || d.NodeID != "e1" || d.Path != "edge.target" {
		t.Fatalf("diag = %+v, want message %q", d, want)
	}
}

func TestAnalyzeUnresolvedVariableRef(t *testing.T) {
	ast := newGB().
		node("s", "start", start()).
		node("llm1", "llm", &dify.LLMDataNode{Variables: []dify.VariableSelectorNode{
			dify.VariableSelectorFromList([]string{"nonexistent_node", "output"}),
		}}).
		node("e", "end", endNode()).
		edge("e1", "s", "llm1").
		edge("e2", "llm1", "e").ast
	r := Analyze(ast)
	d := find(r, "UNRESOLVED_REFERENCE")
	if d == nil {
		t.Fatalf("want UNRESOLVED_REFERENCE, got %v", codes(r))
	}
	want := "Cannot resolve variable reference [nonexistent_node, output]"
	if d.Message != want || d.NodeID != "llm1" || d.Path != "variables[0]" {
		t.Fatalf("diag = %+v, want message %q", d, want)
	}
}

func TestAnalyzeStructuredLLMOutputRootResolves(t *testing.T) {
	structured := &dify.LLMDataNode{StructuredOutput: &dify.StructuredOutputConfig{
		Enabled: true,
		SchemaDef: map[string]any{
			"type":       "object",
			"properties": map[string]any{"value": map[string]any{"type": "object"}},
		},
	}}
	consumer := &dify.CodeDataNode{Variables: []dify.VariableSelectorNode{
		func() dify.VariableSelectorNode {
			v := dify.VariableSelectorFromList([]string{"llm1", "structured_output", "value", "answer"})
			v.Variable = "answer"
			v.ValueType = "string"
			return v
		}(),
	}}
	ast := newGB().
		node("s", "start", start()).
		node("llm1", "llm", structured).
		node("code1", "code", consumer).
		node("e", "end", endNode()).
		edge("e1", "s", "llm1").
		edge("e2", "llm1", "code1").
		edge("e3", "code1", "e").ast
	if result := Analyze(ast); !result.Ok() {
		t.Fatalf("structured output ref must resolve: %v", result.Diagnostics)
	}
}

func TestAnalyzeRenderedStructuredLLMMetadataExpression(t *testing.T) {
	source := `@language nodecoda/1
@mode workflow
type Answer = { string answer; };
function main(string prompt) -> string {
  let r = llm<Answer>("model", {"messages": [{"role":"user", "content":prompt}]});
  return r.reasoning_content ?? r.text.answer;
}`
	front := passes.DefaultPipeline().Run(source, "llm-metadata.ncoda", nil)
	if front.HasErrors() {
		t.Fatalf("frontend: %v", front.Diagnostics)
	}
	graph, err := ir.Build(front.Program)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := dify.RenderFromIR(graph, "workflow")
	if err != nil {
		t.Fatal(err)
	}
	if result := Analyze(artifact); !result.Ok() {
		for i := range artifact.Graph.Nodes {
			node := &artifact.Graph.Nodes[i]
			t.Logf("node=%s type=%s outputs=%#v data=%#v", node.ID, node.NodeType, inferNodeOutputs(node), node.Data)
		}
		t.Fatalf("rendered structured LLM refs must resolve: %v", result.Diagnostics)
	}
}

func TestAnalyzeTypeMismatchReportsExpectedAndActual(t *testing.T) {
	ast := newGB().
		node("s", "start", &dify.StartDataNode{Variables: []dify.VariableDef{
			{Variable: "count", Type: "number"},
		}}).
		node("retrieval", "knowledge-retrieval", &dify.KnowledgeRetrievalDataNode{
			QueryVariableSelector: ptr(dify.VariableSelectorFromList([]string{"s", "count"})),
		}).
		node("e", "end", endNode()).
		edge("e1", "s", "retrieval").
		edge("e2", "retrieval", "e").ast
	r := Analyze(ast)
	d := find(r, "E5001")
	if d == nil {
		t.Fatalf("want E5001, got %v", codes(r))
	}
	want := "Variable reference [s, count] expected string, got number. Hint: connect an output of type string or convert the value before this node."
	if d.Message != want || d.NodeID != "retrieval" || d.Path != "query_variable_selector" {
		t.Fatalf("diag = %+v, want message %q", d, want)
	}
	if d.Severity != "error" {
		t.Fatalf("severity = %s, want error", d.Severity)
	}
}

func TestAnalyzeNestedSelectorSkipsRootObjectTypeComparison(t *testing.T) {
	ast := newGB().
		node("s", "start", start()).
		node("http", "http-request", &dify.HTTPRequestDataNode{}).
		node("retrieval", "knowledge-retrieval", &dify.KnowledgeRetrievalDataNode{
			QueryVariableSelector: ptr(dify.VariableSelectorFromList([]string{"http", "response", "query"})),
		}).
		node("e", "end", endNode()).
		edge("e1", "s", "http").
		edge("e2", "http", "retrieval").
		edge("e3", "retrieval", "e").ast
	r := Analyze(ast)
	if d := find(r, "E5001"); d != nil {
		t.Fatalf("nested selector (path>2) must skip type comparison, got E5001: %+v", d)
	}
}

func TestAnalyzeAssignerReportsItemTypeMismatch(t *testing.T) {
	ast := newGB().
		node("s", "start", &dify.StartDataNode{Variables: []dify.VariableDef{
			{Variable: "count", Type: "number"},
			{Variable: "name", Type: "string"},
		}}).
		node("assign", "assigner", &dify.AssignerDataNode{Items: []dify.AssignmentItem{
			{
				VariableSelector: dify.VariableSelectorFromList([]string{"s", "count"}),
				Operation:        "over-write",
				InputType:        "variable",
				Value:            dify.VariableSelectorFromList([]string{"s", "name"}),
			},
		}}).
		node("e", "end", endNode()).
		edge("e1", "s", "assign").
		edge("e2", "assign", "e").ast
	r := Analyze(ast)
	diags := []*Diagnostic{}
	for i := range r.Diagnostics {
		if r.Diagnostics[i].Code == "E5001" {
			diags = append(diags, &r.Diagnostics[i])
		}
	}
	if len(diags) != 1 {
		t.Fatalf("want exactly one E5001, got %v", codes(r))
	}
	want := "Variable reference [s, name] expected number, got string. Hint: connect an output of type number or convert the value before this node."
	if diags[0].Message != want || diags[0].NodeID != "assign" || diags[0].Path != "items[0].value" {
		t.Fatalf("diag = %+v, want message %q", diags[0], want)
	}
}

func TestAnalyzeAssignerClearSkipsSourceCompare(t *testing.T) {
	ast := newGB().
		node("s", "start", &dify.StartDataNode{Variables: []dify.VariableDef{
			{Variable: "count", Type: "number"},
		}}).
		node("assign", "assigner", &dify.AssignerDataNode{Items: []dify.AssignmentItem{
			{
				VariableSelector: dify.VariableSelectorFromList([]string{"s", "count"}),
				Operation:        "clear",
				InputType:        "variable",
				Value:            dify.VariableSelectorFromList([]string{"missing", "name"}),
			},
		}}).
		node("e", "end", endNode()).
		edge("e1", "s", "assign").
		edge("e2", "assign", "e").ast
	r := Analyze(ast)
	for i := range r.Diagnostics {
		if r.Diagnostics[i].NodeID == "assign" && r.Diagnostics[i].Path == "items[0].value" {
			t.Fatalf("clear must not compare source selector: %+v", r.Diagnostics[i])
		}
	}
}

func TestAnalyzeAssignerNestedSourceSkipsRootTypeComparison(t *testing.T) {
	ast := newGB().
		node("s", "start", &dify.StartDataNode{Variables: []dify.VariableDef{
			{Variable: "count", Type: "number"},
			{Variable: "name", Type: "string"},
		}}).
		node("assign", "assigner", &dify.AssignerDataNode{Items: []dify.AssignmentItem{
			{
				VariableSelector: dify.VariableSelectorFromList([]string{"s", "count"}),
				Operation:        "over-write",
				InputType:        "variable",
				Value:            dify.VariableSelectorFromList([]string{"s", "name", "nested"}),
			},
		}}).
		node("e", "end", endNode()).
		edge("e1", "s", "assign").
		edge("e2", "assign", "e").ast
	r := Analyze(ast)
	if d := find(r, "E5001"); d != nil {
		t.Fatalf("nested assigner source (path>2) must skip, got E5001: %+v", d)
	}
}

func TestAnalyzeLoopNoBreak(t *testing.T) {
	ast := newGB().
		node("s", "start", start()).
		node("l1", "loop", &dify.LoopDataNode{LoopCount: 10}).
		node("e", "end", endNode()).
		edge("e1", "s", "l1").
		edge("e2", "l1", "e").ast
	r := Analyze(ast)
	d := find(r, "LOOP_NO_BREAK")
	if d == nil {
		t.Fatalf("want LOOP_NO_BREAK, got %v", codes(r))
	}
	want := "Loop node 'l1' has no break conditions (may loop forever)"
	if d.Message != want || d.NodeID != "l1" || d.Severity != "warning" {
		t.Fatalf("diag = %+v, want message %q", d, want)
	}
}

func TestAnalyzeIfElseNoCases(t *testing.T) {
	ast := newGB().
		node("s", "start", start()).
		node("if1", "if-else", &dify.IfElseDataNode{}).
		node("e", "end", endNode()).
		edge("e1", "s", "if1").
		edgeHandle("e2", "if1", "e", "true").ast
	r := Analyze(ast)
	d := find(r, "IFELSE_NO_CASES")
	if d == nil {
		t.Fatalf("want IFELSE_NO_CASES, got %v", codes(r))
	}
	want := "If-else node 'if1' has no cases"
	if d.Message != want || d.NodeID != "if1" || d.Severity != "warning" {
		t.Fatalf("diag = %+v, want message %q", d, want)
	}
}

func TestAnalyzeIfElseNoBranches(t *testing.T) {
	ast := newGB().
		node("s", "start", &dify.StartDataNode{Variables: []dify.VariableDef{
			{Variable: "q", Type: "string"},
		}}).
		node("if1", "if-else", &dify.IfElseDataNode{Cases: []dify.CaseNode{
			{CaseID: "c1", Conditions: []dify.ConditionNode{
				{VariableSelector: dify.VariableSelectorFromList([]string{"s", "q"}), ComparisonOperator: "==", Value: "x"},
			}},
		}}).
		node("e", "end", endNode()).
		edge("e1", "s", "if1").
		edgeHandle("e2", "if1", "e", "main").ast
	r := Analyze(ast)
	d := find(r, "IFELSE_NO_BRANCHES")
	if d == nil {
		t.Fatalf("want IFELSE_NO_BRANCHES, got %v", codes(r))
	}
	want := "If-else node 'if1' has no true/false branch edges"
	if d.Message != want || d.NodeID != "if1" || d.Severity != "warning" {
		t.Fatalf("diag = %+v, want message %q", d, want)
	}
	// Oracle: the condition [s,q] resolves (start var) — no UNRESOLVED_REFERENCE.
	if u := find(r, "UNRESOLVED_REFERENCE"); u != nil {
		t.Fatalf("condition reference must resolve: %+v", u)
	}
}

func TestAnalyzeToolHint(t *testing.T) {
	ast := newGB().
		node("s", "start", start()).
		node("t1", "tool", &dify.ToolDataNode{ToolName: "google_search"}).
		node("t2", "tool", &dify.ToolDataNode{ToolName: "web"}).
		node("e", "end", endNode()).
		edge("e1", "s", "t1").
		edge("e2", "t1", "t2").
		edge("e3", "t2", "e").ast
	r := Analyze(ast)
	d := find(r, "TOOL_REGISTRY_HINT")
	if d == nil {
		t.Fatalf("want TOOL_REGISTRY_HINT, got %v", codes(r))
	}
	want := "2 tool node(s) found but no --tools registry provided; validation skipped"
	if d.Message != want || d.Severity != "warning" {
		t.Fatalf("diag = %+v, want message %q", d, want)
	}
	// Oracle: node_id/path empty for the registry hint.
	if d.NodeID != "" || d.Path != "" {
		t.Fatalf("hint must carry empty node_id/path, got %q/%q", d.NodeID, d.Path)
	}
}

func TestAnalyzeNoEndNode(t *testing.T) {
	ast := newGB().node("s", "start", start()).ast
	r := Analyze(ast)
	d := find(r, "NO_END_NODE")
	if d == nil {
		t.Fatalf("want NO_END_NODE, got %v", codes(r))
	}
	want := "Workflow has no end or answer node"
	if d.Message != want || d.Severity != "warning" {
		t.Fatalf("diag = %+v, want message %q", d, want)
	}
	if d.NodeID != "" || d.Path != "" {
		t.Fatalf("NO_END_NODE carries empty node_id/path, got %q/%q", d.NodeID, d.Path)
	}
}

func TestAnalyzeConversationRefsSkipped(t *testing.T) {
	ast := newGB().
		node("s", "start", start()).
		node("llm1", "llm", &dify.LLMDataNode{Variables: []dify.VariableSelectorNode{
			dify.VariableSelectorFromList([]string{"conversation", "conv_var"}),
		}}).
		node("e", "end", endNode()).
		edge("e1", "s", "llm1").
		edge("e2", "llm1", "e").ast
	r := Analyze(ast)
	if u := find(r, "UNRESOLVED_REFERENCE"); u != nil {
		t.Fatalf("conversation refs are virtual and must be skipped: %+v", u)
	}
	if !r.Ok() {
		t.Fatalf("must be ok, got %v", codes(r))
	}
}

func TestAnalyzeOrderingMatchesReference(t *testing.T) {
	// Pass ordering: integrity (early return) → entry/exit → connectivity →
	// variable resolution → type compatibility → loop → branch → tool.
	// A graph that triggers multiple passes must emit diagnostics in that
	// order (oracle-verified: UNREACHABLE_NODE(orphan, t1) from pass 3,
	// UNRESOLVED_REFERENCE from pass 4, LOOP_NO_BREAK from pass 6,
	// TOOL_REGISTRY_HINT from pass 8).
	ast := newGB().
		node("s", "start", start()).
		node("orphan", "llm", &dify.LLMDataNode{Variables: []dify.VariableSelectorNode{
			dify.VariableSelectorFromList([]string{"ghost", "x"}),
		}}).
		node("l1", "loop", &dify.LoopDataNode{}).
		node("t1", "tool", &dify.ToolDataNode{ToolName: "x"}).
		node("e", "end", endNode()).
		edge("e1", "s", "l1").
		edge("e2", "l1", "e").ast
	r := Analyze(ast)
	// orphan is unreachable → UNREACHABLE_NODE (pass 3), then its ref
	// [ghost,x] → UNRESOLVED_REFERENCE (pass 4), then LOOP_NO_BREAK
	// (pass 6), then TOOL_REGISTRY_HINT (pass 8). Pass order groups them.
	var got []string
	for _, d := range r.Diagnostics {
		got = append(got, d.Code)
	}
	wantOrder := []string{"UNREACHABLE_NODE", "UNREACHABLE_NODE", "UNRESOLVED_REFERENCE", "LOOP_NO_BREAK", "TOOL_REGISTRY_HINT"}
	if len(got) != len(wantOrder) {
		t.Fatalf("diagnostics = %v, want %v", got, wantOrder)
	}
	for i, w := range wantOrder {
		if got[i] != w {
			t.Fatalf("order = %v, want %v", got, wantOrder)
		}
	}
}

func TestAnalyzeDeterminism(t *testing.T) {
	ast := newGB().
		node("s", "start", &dify.StartDataNode{Variables: []dify.VariableDef{
			{Variable: "count", Type: "number"},
			{Variable: "name", Type: "string"},
		}}).
		node("retrieval", "knowledge-retrieval", &dify.KnowledgeRetrievalDataNode{
			QueryVariableSelector: ptr(dify.VariableSelectorFromList([]string{"s", "count"})),
		}).
		node("orphan", "llm", llm()).
		node("e", "end", endNode()).
		edge("e1", "s", "retrieval").
		edge("e2", "retrieval", "e").ast
	r1 := Analyze(ast)
	r2 := Analyze(ast)
	if len(r1.Diagnostics) != len(r2.Diagnostics) {
		t.Fatalf("nondeterministic: %v vs %v", codes(r1), codes(r2))
	}
	for i := range r1.Diagnostics {
		if r1.Diagnostics[i] != r2.Diagnostics[i] {
			t.Fatalf("nondeterministic at %d: %+v vs %+v", i, r1.Diagnostics[i], r2.Diagnostics[i])
		}
	}
}

func ptr[T any](v T) *T { return &v }
