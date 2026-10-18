package verifier

// Per-rule verifier tests. Each rule gets at least one synthetic violation
// case (constructing a dify.WorkflowAST + facts that the rule must reject
// with a verbatim E1099 message) and one pass case. The corpus regression
// (62 examples + 47 mutations, byte-identical with the Python reference)
// covers the pass side end-to-end; these unit tests pin the synthetic
// failure surface the brief requires.

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
	"github.com/nodecoda/ncoda/pkg/backend/dify/graphfacts"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/target/codec"
	"github.com/nodecoda/ncoda/pkg/types"
)

// ── builders ────────────────────────────────────────────────────────

func vnode(id, nodeType string, data dify.NodeData) dify.NodeDecl {
	return dify.NodeDecl{ID: id, NodeType: nodeType, Data: data}
}

func vnodeParent(id, nodeType string, data dify.NodeData, parent string) dify.NodeDecl {
	n := vnode(id, nodeType, data)
	if parent != "" {
		n.ParentID = &parent
	}
	return n
}

func vedge(id, source, target, handle string) dify.EdgeDecl {
	return dify.EdgeDecl{ID: id, Source: source, Target: target, SourceHandle: handle, Type: "custom"}
}

func mkGraph(nodes []dify.NodeDecl, edges []dify.EdgeDecl) *dify.WorkflowAST {
	return &dify.WorkflowAST{Graph: dify.GraphNode{Nodes: nodes, Edges: edges}}
}

func run(t *testing.T, ast *dify.WorkflowAST, facts *graphfacts.SemanticGraphFacts, profiles map[string]*binding.AgentTargetProfile, typeCodec *codec.Codec) *VerificationResult {
	t.Helper()
	if facts == nil {
		facts = graphfacts.NewSemanticGraphFacts()
	}
	if typeCodec == nil {
		typeCodec = codec.Default()
	}
	return Verify(ast, facts, profiles, typeCodec)
}

func messages(result *VerificationResult) []string {
	out := make([]string, 0, len(result.Diagnostics))
	for _, d := range result.Diagnostics {
		out = append(out, d.Message)
	}
	return out
}

func hasMessage(result *VerificationResult, want string) bool {
	for _, d := range result.Diagnostics {
		if d.Message == want {
			return true
		}
	}
	return false
}

func assertCode(t *testing.T, result *VerificationResult, code diag.Code) {
	t.Helper()
	for _, d := range result.Diagnostics {
		if d.Code != code {
			t.Fatalf("diagnostic code = %v, want %v (message: %s)", d.Code, code, d.Message)
		}
	}
}

func assertOneDiagnostic(t *testing.T, result *VerificationResult, want string) {
	t.Helper()
	if !result.OK() {
		for _, d := range result.Diagnostics {
			if d.Message != want {
				t.Errorf("unexpected diagnostic: %s", d.Message)
			}
		}
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Message != want {
		t.Fatalf("want exactly one diagnostic %q, got %v", want, messages(result))
	}
	if result.Diagnostics[0].Severity != diag.Error {
		t.Errorf("severity = %v, want error", result.Diagnostics[0].Severity)
	}
}

// ── V0: node identity ───────────────────────────────────────────────

func TestVerifyNodeIDs(t *testing.T) {
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "", NodeType: "start"},
		{ID: "a", NodeType: "start"},
		{ID: "a", NodeType: "code"},
	}, nil), nil, nil, nil)
	if !hasMessage(result, "Graph node at index 0 has an empty id") ||
		!hasMessage(result, "Graph node at index 2 has duplicate id 'a'") {
		t.Fatalf("want empty-id and duplicate-id diagnostics, got %v", messages(result))
	}
	assertCode(t, result, diag.LOWERING_INVARIANT)
}

func TestVerifyNodeIDsPass(t *testing.T) {
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "a", NodeType: "start"},
		{ID: "b", NodeType: "code"},
	}, nil), nil, nil, nil)
	if !result.OK() {
		t.Fatalf("want OK, got %v", messages(result))
	}
}

// ── V1/V2: edge endpoints ───────────────────────────────────────────

func TestVerifyEdgeEndpoints(t *testing.T) {
	result := run(t, mkGraph([]dify.NodeDecl{{ID: "a", NodeType: "start"}}, []dify.EdgeDecl{
		{ID: "e1", Source: "missing", Target: "a"},
		{ID: "e2", Source: "a", Target: "gone"},
	}), nil, nil, nil)
	if !hasMessage(result, "Edge source node 'missing' does not exist in graph") ||
		!hasMessage(result, "Edge target node 'gone' does not exist in graph") {
		t.Fatalf("missing endpoint diagnostics: %v", messages(result))
	}
}

// ── V1b: no self loops ──────────────────────────────────────────────

func TestVerifyNoSelfLoops(t *testing.T) {
	result := run(t, mkGraph([]dify.NodeDecl{{ID: "a", NodeType: "start"}}, []dify.EdgeDecl{
		{ID: "e1", Source: "a", Target: "a"},
	}), nil, nil, nil)
	assertOneDiagnostic(t, result, "Edge on node 'a' forms an invalid self-loop")
}

// ── V16: canonical assignment contract ──────────────────────────────

func TestVerifyCanonicalAssignmentContract(t *testing.T) {
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "legacy", NodeType: "variable-assigner", Data: &dify.AssignerDataNode{Items: []dify.AssignmentItem{{}}}},
		{ID: "assigner", NodeType: "assigner", Data: &dify.AssignerDataNode{}},
	}, nil), nil, nil, nil)
	if !hasMessage(result, "Graph node 'legacy' uses legacy node type 'variable-assigner'") {
		t.Fatalf("missing legacy-type diagnostic: %v", messages(result))
	}
	if !hasMessage(result, "Assigner 'assigner' has no operation items") {
		t.Fatalf("missing empty-items diagnostic: %v", messages(result))
	}
}

func TestVerifyCanonicalAssignmentPass(t *testing.T) {
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "assigner", NodeType: "assigner", Data: &dify.AssignerDataNode{Items: []dify.AssignmentItem{{Operation: "set"}}}},
	}, nil), nil, nil, nil)
	if !result.OK() {
		t.Fatalf("want OK, got %v", messages(result))
	}
}

// ── V3/V11: container flags ─────────────────────────────────────────

func TestVerifyContainerFlags(t *testing.T) {
	loop := "loop_1"
	result := run(t, mkGraph([]dify.NodeDecl{
		vnode("loop_1", "loop", &dify.LoopDataNode{}),
		vnodeParent("code_2", "code", &dify.CodeDataNode{DataNode: dify.DataNode{IsInLoop: true, LoopID: "other"}, CodeLanguage: "python3"}, loop),
	}, nil), nil, nil, nil)
	if !hasMessage(result, "Node 'code_2' has inconsistent loop flags for nearest container 'loop_1' (active=True, loop_id='other')") {
		t.Fatalf("missing container-flag diagnostic: %v", messages(result))
	}
}

func TestVerifyContainerFlagsPass(t *testing.T) {
	loop := "loop_1"
	result := run(t, mkGraph([]dify.NodeDecl{
		vnode("loop_1", "loop", &dify.LoopDataNode{LoopCount: 1}),
		vnodeParent("code_2", "code", &dify.CodeDataNode{DataNode: dify.DataNode{IsInLoop: true, LoopID: "loop_1"}, CodeLanguage: "python3"}, loop),
	}, nil), nil, nil, nil)
	if !result.OK() {
		t.Fatalf("want OK, got %v", messages(result))
	}
}

// ── V13: closed capability nodes ────────────────────────────────────

func TestVerifyClosedCapabilityNodes(t *testing.T) {
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "p", NodeType: "parallel-product", Data: &dify.CodeDataNode{CodeLanguage: "python3"}},
	}, nil), nil, nil, nil)
	assertOneDiagnostic(t, result, "Graph node 'p' has unsupported synthetic parallel type 'parallel-product'")
}

// ── V17: agent nodes ────────────────────────────────────────────────

func TestVerifyAgentNodes(t *testing.T) {
	profile := &binding.AgentTargetProfile{
		ProviderName: "langgenius", StrategyName: "function", StrategyLabel: "default",
		PluginUniqueIdentifier: "langgenius/agent:1.0.0", ToolNodeVersion: "2",
	}
	profiles := map[string]*binding.AgentTargetProfile{"default": profile}
	agent := &dify.AgentDataNode{
		DataNode: dify.DataNode{}, AgentStrategyProviderName: "langgenius",
		AgentStrategyName: "function", AgentStrategyLabel: "default",
		PluginUniqueIdentifier: "langgenius/agent:1.0.0", ToolNodeVersion: strPtr("1"),
		AgentParameters: map[string]dify.AgentInputNode{},
	}
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "agent_1", NodeType: "agent", Data: agent},
	}, nil), nil, profiles, nil)
	if !hasMessage(result, "Agent node 'agent_1' must use tool_node_version '2'") {
		t.Fatalf("missing tool version diagnostic: %v", messages(result))
	}
}

func strPtr(s string) *string { return &s }

// ── V12: iteration output ownership ─────────────────────────────────

func TestVerifyIterationOutputOwnership(t *testing.T) {
	iter := &dify.IterationDataNode{OutputSelector: []string{"missing_producer"}}
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "iter_1", NodeType: "iteration", Data: iter},
	}, nil), nil, nil, nil)
	assertOneDiagnostic(t, result, "Iteration 'iter_1' output selector references missing producer 'missing_producer'")
}

// ── V4: condition shapes ────────────────────────────────────────────

func TestVerifyConditionShapes(t *testing.T) {
	ifElse := &dify.IfElseDataNode{Cases: []dify.CaseNode{{
		CaseID: "case_0",
		Conditions: []dify.ConditionNode{{
			VariableSelector:   dify.VariableSelectorNode{},
			ComparisonOperator: "==",
		}},
	}}}
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "if_1", NodeType: "if-else", Data: ifElse},
	}, nil), nil, nil, nil)
	assertOneDiagnostic(t, result, "Node 'if_1' condition 0 has an empty selector")
}

func TestVerifyConditionShapesPass(t *testing.T) {
	ifElse := &dify.IfElseDataNode{Cases: []dify.CaseNode{{
		CaseID: "case_0",
		Conditions: []dify.ConditionNode{{
			VariableSelector:   dify.VariableSelectorNode{NodeID: "a", Key: "result", Path: []string{"a", "result"}},
			ComparisonOperator: "==",
		}},
	}}}
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "start_0", NodeType: "start", Data: &dify.StartDataNode{}},
		{ID: "if_1", NodeType: "if-else", Data: ifElse},
		{ID: "a", NodeType: "code", Data: &dify.CodeDataNode{CodeLanguage: "python3", Outputs: map[string]dify.OutputDefNode{"result": {Type: "number"}}}},
	}, []dify.EdgeDecl{
		vedge("e1", "start_0", "a", ""),
		vedge("e2", "a", "if_1", ""),
	}), nil, nil, nil)
	if !result.OK() {
		t.Fatalf("want OK, got %v", messages(result))
	}
}

// ── V4: selector producers / V4b: selector ports ────────────────────

// TestVerifyOutputMappingSelectorPorts locks the R1 finding-2 fix:
// OutputNode output mappings derive node_id/key from path[:2]
// (ast_nodes.py:60-64), so V4b checks their producer ports exactly like
// code-variable selectors (test_graph_verifier_gaps.py:132/192).
func TestVerifyOutputMappingSelectorPorts(t *testing.T) {
	// Missing producer port (DCVER002 output-mapping case).
	result := run(t, mkGraph([]dify.NodeDecl{
		vnode("producer", "code", &dify.CodeDataNode{CodeLanguage: "python3"}),
		vnode("end", "end", &dify.OutputNode{Outputs: []dify.OutputMapping{
			{Variable: "output", ValueSelector: []string{"producer", "missing_port"}},
		}}),
	}, []dify.EdgeDecl{
		vedge("e1", "producer", "end", ""),
	}), nil, nil, nil)
	if !hasMessage(result, "Node 'end' references Code node 'producer' port 'missing_port' but that port does not exist (available: [])") {
		t.Fatalf("missing output-mapping missing-port diagnostic: %v", messages(result))
	}

	// Declared type must match the producer port type.
	result = run(t, mkGraph([]dify.NodeDecl{
		vnode("producer", "code", &dify.CodeDataNode{CodeLanguage: "python3", Outputs: map[string]dify.OutputDefNode{
			"result": {Type: "boolean"},
		}}),
		vnode("end", "end", &dify.OutputNode{Outputs: []dify.OutputMapping{
			{Variable: "output", ValueType: "string", ValueSelector: []string{"producer", "result"}},
		}}),
	}, []dify.EdgeDecl{
		vedge("e1", "producer", "end", ""),
	}), nil, nil, nil)
	if !hasMessage(result, "Node 'end' selector ['producer', 'result'] declares type 'string' but Code port type is 'boolean'") {
		t.Fatalf("missing output-mapping type-mismatch diagnostic: %v", messages(result))
	}

	// A matching mapping passes (no false positive from Derive).
	result = run(t, mkGraph([]dify.NodeDecl{
		vnode("producer", "code", &dify.CodeDataNode{CodeLanguage: "python3", Outputs: map[string]dify.OutputDefNode{
			"result": {Type: "boolean"},
		}}),
		vnode("end", "end", &dify.OutputNode{Outputs: []dify.OutputMapping{
			{Variable: "output", ValueType: "boolean", ValueSelector: []string{"producer", "result"}},
		}}),
	}, []dify.EdgeDecl{
		vedge("e1", "producer", "end", ""),
	}), nil, nil, nil)
	if !result.OK() {
		t.Fatalf("matching output mapping must pass, got %v", messages(result))
	}
}

func TestVerifySelectorProducers(t *testing.T) {
	llm := &dify.LLMDataNode{Variables: []dify.VariableSelectorNode{
		{NodeID: "ghost", Key: "result", Path: []string{"ghost", "result"}},
	}}
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "llm_1", NodeType: "llm", Data: llm},
	}, nil), nil, nil, nil)
	assertOneDiagnostic(t, result, "Node 'llm_1' references selector path ['ghost', 'result'] but producer node 'ghost' does not exist in graph")
}

func TestVerifySelectorPorts(t *testing.T) {
	code := &dify.CodeDataNode{
		CodeLanguage: "python3",
		Outputs:      map[string]dify.OutputDefNode{"result": {Type: "number"}},
	}
	llm := &dify.LLMDataNode{Variables: []dify.VariableSelectorNode{
		{NodeID: "code_1", Key: "nope", Path: []string{"code_1", "nope"}},
	}}
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "code_1", NodeType: "code", Data: code},
		{ID: "llm_1", NodeType: "llm", Data: llm},
	}, []dify.EdgeDecl{vedge("e1", "code_1", "llm_1", "")}), nil, nil, nil)
	assertOneDiagnostic(t, result, "Node 'llm_1' references Code node 'code_1' port 'nope' but that port does not exist (available: ['result'])")
}

// ── V-: parallel joins ──────────────────────────────────────────────

func TestVerifyParallelJoins(t *testing.T) {
	facts := graphfacts.NewSemanticGraphFacts()
	facts.ParallelJoinGroups[graphfacts.JoinGroupKey([]string{"a", "b"})] = true
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "a", NodeType: "code", Data: &dify.CodeDataNode{CodeLanguage: "python3"}},
		{ID: "b", NodeType: "code", Data: &dify.CodeDataNode{CodeLanguage: "python3"}},
		{ID: "x", NodeType: "code", Data: &dify.CodeDataNode{CodeLanguage: "python3"}},
		{ID: "y", NodeType: "code", Data: &dify.CodeDataNode{CodeLanguage: "python3"}},
	}, []dify.EdgeDecl{
		vedge("e1", "a", "x", ""),
		vedge("e2", "b", "y", ""),
	}), facts, nil, nil)
	assertOneDiagnostic(t, result, "Parallel product producers do not all reach the same continuation: ['a', 'b']")
}

func TestVerifyParallelJoinsPass(t *testing.T) {
	facts := graphfacts.NewSemanticGraphFacts()
	facts.ParallelJoinGroups[graphfacts.JoinGroupKey([]string{"a", "b"})] = true
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "a", NodeType: "code", Data: &dify.CodeDataNode{CodeLanguage: "python3"}},
		{ID: "b", NodeType: "code", Data: &dify.CodeDataNode{CodeLanguage: "python3"}},
		{ID: "join", NodeType: "code", Data: &dify.CodeDataNode{CodeLanguage: "python3"}},
	}, []dify.EdgeDecl{
		vedge("e1", "a", "join", ""),
		vedge("e2", "b", "join", ""),
	}), facts, nil, nil)
	if !result.OK() {
		t.Fatalf("want OK, got %v", messages(result))
	}
}

// ── V8: selector dominance ──────────────────────────────────────────

func TestVerifySelectorDominance(t *testing.T) {
	// consumer selects from a producer that never dominates it and is not
	// a legal container/join exemption.
	consumer := &dify.LLMDataNode{Variables: []dify.VariableSelectorNode{
		{NodeID: "code_1", Key: "result", Path: []string{"code_1", "result"}},
	}}
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "start_1", NodeType: "start", Data: &dify.StartDataNode{}},
		{ID: "code_1", NodeType: "code", Data: &dify.CodeDataNode{CodeLanguage: "python3", Outputs: map[string]dify.OutputDefNode{"result": {Type: "number"}}}},
		{ID: "llm_2", NodeType: "llm", Data: consumer},
	}, []dify.EdgeDecl{
		vedge("e1", "start_1", "llm_2", ""),
	}), nil, nil, nil)
	if !hasMessage(result, "Selector producer 'code_1' does not dominate consumer 'llm_2' for path ['code_1', 'result']; add an explicit legal merge") {
		t.Fatalf("missing dominance diagnostic: %v", messages(result))
	}
}

func TestVerifySelectorDominancePass(t *testing.T) {
	consumer := &dify.LLMDataNode{Variables: []dify.VariableSelectorNode{
		{NodeID: "code_1", Key: "result", Path: []string{"code_1", "result"}},
	}}
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "start_1", NodeType: "start", Data: &dify.StartDataNode{}},
		{ID: "code_1", NodeType: "code", Data: &dify.CodeDataNode{CodeLanguage: "python3", Outputs: map[string]dify.OutputDefNode{"result": {Type: "number"}}}},
		{ID: "llm_2", NodeType: "llm", Data: consumer},
	}, []dify.EdgeDecl{
		vedge("e1", "start_1", "code_1", ""),
		vedge("e2", "code_1", "llm_2", ""),
	}), nil, nil, nil)
	if !result.OK() {
		t.Fatalf("want OK, got %v", messages(result))
	}
}

// ── V5: aggregator completeness ─────────────────────────────────────

func TestVerifyAggregatorCompleteness(t *testing.T) {
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "agg_1", NodeType: "variable-aggregator", Data: &dify.VariableAggregatorDataNode{}},
	}, nil), nil, nil, nil)
	assertOneDiagnostic(t, result, "Variable-aggregator 'agg_1' has no incoming edges; every aggregator must be reached by at least one branch leaf")
}

func TestVerifyAggregatorCompletenessPass(t *testing.T) {
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "agg_1", NodeType: "variable-aggregator", Data: &dify.VariableAggregatorDataNode{}},
		{ID: "code_1", NodeType: "code", Data: &dify.CodeDataNode{CodeLanguage: "python3"}},
	}, []dify.EdgeDecl{
		vedge("e1", "code_1", "agg_1", ""),
	}), nil, nil, nil)
	if !result.OK() {
		t.Fatalf("want OK, got %v", messages(result))
	}
}

// ── V6: branch handles ──────────────────────────────────────────────

func TestVerifyBranchHandles(t *testing.T) {
	ifElse := &dify.IfElseDataNode{Cases: []dify.CaseNode{{CaseID: "case_0"}}}
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "if_1", NodeType: "if-else", Data: ifElse},
		{ID: "code_2", NodeType: "code", Data: &dify.CodeDataNode{CodeLanguage: "python3"}},
	}, []dify.EdgeDecl{
		vedge("e1", "if_1", "code_2", "bogus"),
	}), nil, nil, nil)
	assertOneDiagnostic(t, result, "Edge from 'if_1' uses source_handle 'bogus' which is not a valid case handle (valid: ['case_0', 'false'])")
}

func TestVerifyBranchHandlesPass(t *testing.T) {
	ifElse := &dify.IfElseDataNode{Cases: []dify.CaseNode{{CaseID: "case_0"}}}
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "if_1", NodeType: "if-else", Data: ifElse},
		{ID: "code_2", NodeType: "code", Data: &dify.CodeDataNode{CodeLanguage: "python3"}},
	}, []dify.EdgeDecl{
		vedge("e1", "if_1", "code_2", "case_0"),
	}), nil, nil, nil)
	if !result.OK() {
		t.Fatalf("want OK, got %v", messages(result))
	}
}

// ── V9/V10: consumed exits ──────────────────────────────────────────

func TestVerifyConsumedExitsReturnUndeclaredScope(t *testing.T) {
	facts := graphfacts.NewSemanticGraphFacts()
	facts.ConsumedExits = append(facts.ConsumedExits, graphfacts.ConsumedExitProof{
		Kind:          binding.ExitReturn,
		TargetScopeID: binding.ScopeID(99),
	})
	result := run(t, mkGraph(nil, nil), facts, nil, nil)
	assertOneDiagnostic(t, result, "Consumed RETURN exit: RETURN targets undeclared function scope 99")
}

func TestVerifyConsumedExitsReturnGraphContainer(t *testing.T) {
	facts := graphfacts.NewSemanticGraphFacts()
	owner := "loop_1"
	facts.FunctionScopes[binding.ScopeID(1)] = true
	facts.ConsumedExits = append(facts.ConsumedExits, graphfacts.ConsumedExitProof{
		Kind:             binding.ExitReturn,
		TargetScopeID:    binding.ScopeID(1),
		OwnerContainerID: &owner,
	})
	result := run(t, mkGraph(nil, nil), facts, nil, nil)
	assertOneDiagnostic(t, result, "Consumed RETURN exit: RETURN cannot be consumed by a graph container")
}

func TestVerifyConsumedExitsLoopConsumer(t *testing.T) {
	facts := graphfacts.NewSemanticGraphFacts()
	owner := "loop_1"
	tail := "tail_1"
	consumer := "assigner_2"
	facts.ContainerScopes[owner] = binding.ScopeID(1)
	facts.ConsumedExits = append(facts.ConsumedExits, graphfacts.ConsumedExitProof{
		Kind:             binding.ExitContinue,
		TargetScopeID:    binding.ScopeID(1),
		TailNodeID:       &tail,
		ConsumerNodeID:   &consumer,
		OwnerContainerID: &owner,
	})
	loopNode := vnode("loop_1", "loop", &dify.LoopDataNode{LoopCount: 1})
	assigner := &dify.AssignerDataNode{Items: []dify.AssignmentItem{{Operation: "set"}}}
	result := run(t, mkGraph([]dify.NodeDecl{
		loopNode,
		vnodeParent("tail_1", "code", &dify.CodeDataNode{DataNode: dify.DataNode{IsInLoop: true, LoopID: owner}, CodeLanguage: "python3"}, owner),
		vnodeParent(consumer, "assigner", assigner, owner),
	}, []dify.EdgeDecl{
		vedge("e1", tail, consumer, ""),
	}), facts, nil, nil)
	// assigner does not increment __index → not a legal for-increment
	// consumer; it has an outgoing edge → not a loop-continue sink.
	if !hasMessage(result, "Consumed CONTINUE exit: CONTINUE consumer 'assigner_2' is not a child verified loop continue sink or for increment of loop 'loop_1'") {
		t.Fatalf("missing loop consumer diagnostic: %v", messages(result))
	}
}

func TestVerifyConsumedExitsPass(t *testing.T) {
	facts := graphfacts.NewSemanticGraphFacts()
	tail := "tail_1"
	consumer := "end_2"
	owner := "loop_1"
	facts.ConsumedExits = append(facts.ConsumedExits, graphfacts.ConsumedExitProof{
		Kind:             binding.ExitBreak,
		TargetScopeID:    binding.ScopeID(1),
		TailNodeID:       &tail,
		TailSourceHandle: "loop-break",
		ConsumerNodeID:   &consumer,
		OwnerContainerID: &owner,
	})
	facts.ContainerScopes[owner] = binding.ScopeID(1)
	loopEdge := vedge("e1", "tail_1", "end_2", "loop-break")
	loopEdge.IsInLoop = boolPtr(true)
	loopEdge.LoopID = owner
	result := run(t, mkGraph([]dify.NodeDecl{
		vnode("loop_1", "loop", &dify.LoopDataNode{LoopCount: 1}),
		vnodeParent("end_2", "loop-end", &dify.LoopEndDataNode{DataNode: dify.DataNode{IsInLoop: true, LoopID: owner}}, owner),
		vnodeParent("tail_1", "code", &dify.CodeDataNode{DataNode: dify.DataNode{IsInLoop: true, LoopID: owner}, CodeLanguage: "python3"}, owner),
	}, []dify.EdgeDecl{loopEdge}), facts, nil, nil)
	if !result.OK() {
		t.Fatalf("want OK, got %v", messages(result))
	}
}

// ── V14: no user function calls ─────────────────────────────────────

func TestVerifyNoUserFunctionCalls(t *testing.T) {
	facts := graphfacts.NewSemanticGraphFacts()
	facts.UserFunctionNames["helper"] = true
	facts.CompilerGeneratedCodeNodes["code_1"] = true
	facts.CodeNodeProvenance["code_1"] = "generated-calculation"
	code := &dify.CodeDataNode{
		CodeLanguage: "python3",
		Code:         "def main():\n    return helper(1) + 2",
	}
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "code_1", NodeType: "code", Data: code},
	}, nil), facts, nil, nil)
	assertOneDiagnostic(t, result, "Generated Code node 'code_1' retains NodeCoda user function call 'helper'")
}

func TestVerifyNoUserFunctionCallsPass(t *testing.T) {
	facts := graphfacts.NewSemanticGraphFacts()
	facts.UserFunctionNames["helper"] = true
	facts.CompilerGeneratedCodeNodes["code_1"] = true
	facts.CodeNodeProvenance["code_1"] = "generated-calculation"
	code := &dify.CodeDataNode{
		CodeLanguage: "python3",
		Code:         "def main():\n    return obj.helper(1) + 2  # helper(3) is in a comment\n    text = 'helper(4)'",
	}
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "code_1", NodeType: "code", Data: code},
	}, nil), facts, nil, nil)
	if !result.OK() {
		t.Fatalf("want OK (attribute/comment/string calls are not plain Name calls), got %v", messages(result))
	}
}

// ── V14b: code output contracts ─────────────────────────────────────

func TestVerifyCodeOutputContracts(t *testing.T) {
	code := &dify.CodeDataNode{
		CodeLanguage:           "python3",
		Outputs:                map[string]dify.OutputDefNode{"result": {Type: "number"}},
		DeclaredOutputContract: map[string]*types.Descriptor{"result": types.StringType()},
	}
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "code_1", NodeType: "code", Data: code},
	}, nil), nil, nil, nil)
	assertOneDiagnostic(t, result, "Code node 'code_1' port 'result' shape does not match published type Type(string)")
}

func TestVerifyCodeOutputContractsPass(t *testing.T) {
	code := &dify.CodeDataNode{
		CodeLanguage:           "python3",
		Outputs:                map[string]dify.OutputDefNode{"result": {Type: "number"}},
		DeclaredOutputContract: map[string]*types.Descriptor{"result": types.IntType()},
	}
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "code_1", NodeType: "code", Data: code},
	}, nil), nil, nil, nil)
	if !result.OK() {
		t.Fatalf("want OK, got %v", messages(result))
	}
}

// ── V13-adjacent: closed capability (pass) ──────────────────────────

func TestVerifyClosedCapabilityPass(t *testing.T) {
	result := run(t, mkGraph([]dify.NodeDecl{
		{ID: "a", NodeType: "code", Data: &dify.CodeDataNode{CodeLanguage: "python3"}},
	}, nil), nil, nil, nil)
	if !result.OK() {
		t.Fatalf("want OK, got %v", messages(result))
	}
}

// ── Verify() dispatch order ─────────────────────────────────────────

func TestVerifyNilGraphEarlyExit(t *testing.T) {
	result := run(t, &dify.WorkflowAST{}, nil, nil, nil)
	if !result.OK() {
		t.Fatalf("want OK for nil graph, got %v", messages(result))
	}
}

func TestVerifyNilAST(t *testing.T) {
	result := Verify(nil, nil, nil, nil)
	if !result.OK() {
		t.Fatalf("want OK for nil ast, got %v", messages(result))
	}
}

// ── helpers ─────────────────────────────────────────────────────────

func TestScanUserFunctionCalls(t *testing.T) {
	calls := scanUserFunctionCalls("def main():\n    x = helper(1)\n    y = other(2)\n    z = a.b(3)\n    # helper(4)\n    s = 'helper(5)'", map[string]bool{"helper": true, "other": true, "a": true})
	if strings.Join(calls, ",") != "helper,other" {
		t.Fatalf("calls = %v, want [helper other]", calls)
	}
}

// TestScanUserFunctionCallsFString locks the R1 finding-3 fix: CPython's
// ast.walk descends into JoinedStr/FormattedValue, so a user-function call
// inside an f-string replacement field must be flagged (verifier.py:971-1012).
func TestScanUserFunctionCallsFString(t *testing.T) {
	userFns := map[string]bool{"helper": true, "inner": true, "other": true, "a": true, "b": true, "value": true, "staff": true}
	cases := []struct {
		name string
		src  string
		want []string // sorted
	}{
		{"plain", `x = f"{helper()}"`, []string{"helper"}},
		{"plain-concat", `x = f"val {helper(1)} done"`, []string{"helper"}},
		{"nested-string", `x = f"{helper(f'{inner(2)}')}"`, []string{"helper", "inner"}},
		{"attribute-inside", `x = f"{a.b(3)}"`, nil},
		{"no-call", `x = f"{value}"`, nil},
		{"escaped-braces", `x = f"{{helper()}}"`, nil},
		{"triple", "x = f\"\"\"pre {helper()} post\"\"\"", []string{"helper"}},
		{"rf-prefix", `x = rf"{helper()}"`, []string{"helper"}},
		{"nested-dict", `x = f"{ {'a': helper()} }"`, []string{"helper"}},
		{"nested-string-closing-brace", `x = f"{helper('}') and other()}"`, []string{"helper", "other"}},
		{"nested-string-leading-closing-brace", `x = f"{'}' and helper()}"`, []string{"helper"}},
		{"fr-prefix", `x = fr"{helper()}"`, []string{"helper"}},
		{"not-fstring", `x = "helper()"`, nil},
		{"identifier-not-fstring", `x = staff"helper()"`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scanUserFunctionCalls(tc.src, userFns)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("calls = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClassificationConstantValue(t *testing.T) {
	code := &dify.CodeDataNode{CodeLanguage: "python3", Code: "def main() -> dict:\n    return {\"result\": \"Red\"}"}
	got := classificationConstantValue(code)
	if got == nil || *got != "Red" {
		t.Fatalf("constant = %v, want \"Red\"", got)
	}
	code = &dify.CodeDataNode{CodeLanguage: "python3", Code: "def main() -> dict:\n    return {\"result\": 5}"}
	if classificationConstantValue(code) != nil {
		t.Fatalf("non-string constant should be nil")
	}
	code = &dify.CodeDataNode{CodeLanguage: "python3", Code: "def main(a) -> dict:\n    return {\"result\": \"Red\"}"}
	if classificationConstantValue(code) != nil {
		t.Fatalf("parameterized main should be nil")
	}
}

func boolPtr(b bool) *bool { return &b }
