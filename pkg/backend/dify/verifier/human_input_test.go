package verifier

// R1 finding-6/7 test additions: the human-input contract suite (ported
// from tests/lang/unit/test_graph_verifier.py:195-640 by constructing
// facts directly, since the Go lowering has no human-input producer yet)
// plus the missing per-rule cases the reviewer identified
// (test_semantic_graph_verifier.py / test_graph_verifier_gaps.py).

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
	"github.com/nodecoda/ncoda/pkg/backend/dify/graphfacts"
	"github.com/nodecoda/ncoda/pkg/binding"
)

// humanInputContext builds a valid emitted human-input graph plus the
// matching immutable fingerprint, mirroring the Python _human_input_context
// fixture. The continuation is a generated-calculation code node; each of
// the three branch handles (approve/reject/__timeout) falls through to it.
func humanInputContext() (*dify.WorkflowAST, *graphfacts.SemanticGraphFacts) {
	codeNode := vnode("code_2", "code", &dify.CodeDataNode{
		CodeLanguage: "python3",
		Code:         "def main():\n    return 'done'",
	})
	humanNode := vnode("human_1", "human-input", &dify.HumanInputDataNode{
		FormContent: "Review",
		Inputs: []dify.HumanInputField{
			&dify.ParagraphHumanInput{
				OutputVariableName: "comment",
				Type:               "paragraph",
				Default:            &dify.StringSourceNode{Type: "constant", Value: ""},
			},
			&dify.SelectHumanInput{
				OutputVariableName: "priority",
				Type:               "select",
				OptionSource:       dify.StringListSourceNode{Type: "constant", Value: []string{"low", "high"}},
			},
		},
		UserActions: []dify.UserAction{
			{ID: "approve", Title: "Approve", ButtonStyle: "primary"},
			{ID: "reject", Title: "Reject", ButtonStyle: "default"},
		},
		Timeout:     3,
		TimeoutUnit: "day",
	})
	graph := mkGraph([]dify.NodeDecl{humanNode, codeNode}, []dify.EdgeDecl{
		vedge("e1", "human_1", "code_2", "approve"),
		vedge("e2", "human_1", "code_2", "reject"),
		vedge("e3", "human_1", "code_2", "__timeout"),
	})
	facts := graphfacts.NewSemanticGraphFacts()
	facts.CompilerGeneratedHumanInputs["human_1"] = true
	facts.CompilerGeneratedCodeNodes["code_2"] = true
	facts.CodeNodeProvenance["code_2"] = "generated-calculation"
	contID := "code_2"
	facts.HumanInputContracts["human_1"] = graphfacts.HumanInputContractFingerprint{
		Content: "Review",
		Fields: []graphfacts.HumanInputFieldFingerprint{
			{Name: "comment", Kind: "paragraph", Options: []string{}, SourceType: "constant", SourceValue: ""},
			{Name: "priority", Kind: "select", Options: []string{"low", "high"}, SourceType: "constant"},
		},
		Actions: []graphfacts.HumanInputActionFingerprint{
			{Member: "approve", Title: "Approve", Style: "primary"},
			{Member: "reject", Title: "Reject", Style: "default"},
		},
		TimeoutValue: 3,
		TimeoutUnit:  "day",
		Branches: []graphfacts.HumanInputBranchFingerprint{
			{Handle: "approve", FallthroughTails: [][2]any{{"human_1", "approve"}}},
			{Handle: "reject", FallthroughTails: [][2]any{{"human_1", "reject"}}},
			{Handle: "__timeout", FallthroughTails: [][2]any{{"human_1", "__timeout"}}},
		},
		ContinuationNodeID:    &contID,
		ContinuationPayload:   graphfacts.HumanInputContinuationPayload(&codeNode),
		ContinuationOwnership: nodeOwnership(&codeNode),
	}
	return graph, facts
}

func humanInputNode(graph *dify.WorkflowAST) *dify.HumanInputDataNode {
	for i := range graph.Graph.Nodes {
		if data, ok := graph.Graph.Nodes[i].Data.(*dify.HumanInputDataNode); ok {
			return data
		}
	}
	panic("no human-input node in fixture")
}

func TestHumanInputBaseFixturePasses(t *testing.T) {
	graph, facts := humanInputContext()
	result := run(t, graph, facts, nil, nil)
	if !result.OK() {
		t.Fatalf("base human-input fixture must verify clean, got %v", messages(result))
	}
}

// TestHumanInputPayloadMutations ports the parametrized mutation table from
// test_graph_verifier.py:210-254: every payload projection mismatch must
// surface as "payload disagrees with its immutable fingerprint".
func TestHumanInputPayloadMutations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(data *dify.HumanInputDataNode)
	}{
		{"content", func(d *dify.HumanInputDataNode) { d.FormContent = "mutated" }},
		{"paragraph-default-type", func(d *dify.HumanInputDataNode) {
			d.Inputs[0].(*dify.ParagraphHumanInput).Default.Type = "variable"
		}},
		{"paragraph-default-selector", func(d *dify.HumanInputDataNode) {
			d.Inputs[0].(*dify.ParagraphHumanInput).Default.Selector = []string{"foreign"}
		}},
		{"paragraph-default-value", func(d *dify.HumanInputDataNode) {
			d.Inputs[0].(*dify.ParagraphHumanInput).Default.Value = "mutated"
		}},
		{"paragraph-default-none", func(d *dify.HumanInputDataNode) {
			d.Inputs[0].(*dify.ParagraphHumanInput).Default = nil
		}},
		{"select-source-type", func(d *dify.HumanInputDataNode) {
			d.Inputs[1].(*dify.SelectHumanInput).OptionSource.Type = "variable"
		}},
		{"select-source-selector", func(d *dify.HumanInputDataNode) {
			d.Inputs[1].(*dify.SelectHumanInput).OptionSource.Selector = []string{"foreign"}
		}},
		{"select-options", func(d *dify.HumanInputDataNode) {
			d.Inputs[1].(*dify.SelectHumanInput).OptionSource.Value = []string{"low", "high", "urgent"}
		}},
		{"action-metadata", func(d *dify.HumanInputDataNode) { d.UserActions[0].Title = "Mutated" }},
		{"timeout", func(d *dify.HumanInputDataNode) { d.Timeout = 4 }},
		{"timeout-unit", func(d *dify.HumanInputDataNode) { d.TimeoutUnit = "hour" }},
		{"delivery-methods", func(d *dify.HumanInputDataNode) {
			d.DeliveryMethods = []any{map[string]any{"id": "x", "type": "email", "enabled": true}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			graph, facts := humanInputContext()
			tc.mutate(humanInputNode(graph))
			result := run(t, graph, facts, nil, nil)
			if !hasMessage(result, "Human Input 'human_1' payload disagrees with its immutable fingerprint") {
				t.Fatalf("missing payload diagnostic: %v", messages(result))
			}
		})
	}
}

func TestHumanInputDuplicatePayloadIdentifiers(t *testing.T) {
	graph, facts := humanInputContext()
	data := humanInputNode(graph)
	data.Inputs = append(data.Inputs, data.Inputs[0])
	data.UserActions = append(data.UserActions, data.UserActions[0])
	result := run(t, graph, facts, nil, nil)
	if !hasMessage(result, "Human Input 'human_1' has duplicate input output names") {
		t.Fatalf("missing duplicate-names diagnostic: %v", messages(result))
	}
	if !hasMessage(result, "Human Input 'human_1' has duplicate action IDs") {
		t.Fatalf("missing duplicate-actions diagnostic: %v", messages(result))
	}
}

func TestHumanInputGeneratedNodeWithoutIdentityOrFingerprint(t *testing.T) {
	graph, facts := humanInputContext()
	delete(facts.CompilerGeneratedHumanInputs, "human_1")
	delete(facts.HumanInputContracts, "human_1")
	result := run(t, graph, facts, nil, nil)
	if !hasMessage(result, "Emitted Human Input 'human_1' has no compiler-owned identity or fingerprint") {
		t.Fatalf("missing identity-or-fingerprint diagnostic: %v", messages(result))
	}
}

func TestHumanInputOwnedNodeWithoutFingerprint(t *testing.T) {
	graph, facts := humanInputContext()
	delete(facts.HumanInputContracts, "human_1")
	result := run(t, graph, facts, nil, nil)
	if !hasMessage(result, "Compiler-owned Human Input 'human_1' has no immutable fingerprint") {
		t.Fatalf("missing immutable-fingerprint diagnostic: %v", messages(result))
	}
}

func TestHumanInputFingerprintWithoutOwnedIdentity(t *testing.T) {
	graph, facts := humanInputContext()
	delete(facts.CompilerGeneratedHumanInputs, "human_1")
	result := run(t, graph, facts, nil, nil)
	if !hasMessage(result, "Human Input fingerprint 'human_1' has no compiler-owned identity") {
		t.Fatalf("missing compiler-owned-identity diagnostic: %v", messages(result))
	}
}

func TestHumanInputBranchHandleCoverage(t *testing.T) {
	graph, facts := humanInputContext()
	graph.Graph.Edges[0].SourceHandle = "unknown"
	result := run(t, graph, facts, nil, nil)
	if !hasMessage(result, "Human Input 'human_1' outgoing handles ['__timeout', 'reject', 'unknown'] do not exactly match NodeCoda branch handles ['__timeout', 'approve', 'reject']") {
		t.Fatalf("missing handle-coverage diagnostic: %v", messages(result))
	}
}

func TestHumanInputBranchReferencesMissingNodes(t *testing.T) {
	graph, facts := humanInputContext()
	contract := facts.HumanInputContracts["human_1"]
	contract.Branches[0].NodeIDs = []string{"ghost_9"}
	facts.HumanInputContracts["human_1"] = contract
	result := run(t, graph, facts, nil, nil)
	if !hasMessage(result, "Human Input 'human_1' branch 'approve' references missing nodes") {
		t.Fatalf("missing branch-missing-nodes diagnostic: %v", messages(result))
	}
}

func TestHumanInputContinuationMutation(t *testing.T) {
	graph, facts := humanInputContext()
	for i := range graph.Graph.Nodes {
		if graph.Graph.Nodes[i].ID == "code_2" {
			graph.Graph.Nodes[i].Data.(*dify.CodeDataNode).Code = "def main():\n    return 'mutated'"
		}
	}
	result := run(t, graph, facts, nil, nil)
	if !hasMessage(result, "Human Input 'human_1' continuation disagrees with its immutable fingerprint") {
		t.Fatalf("missing continuation diagnostic: %v", messages(result))
	}
}

func TestHumanInputContinuationOwnershipMutation(t *testing.T) {
	graph, facts := humanInputContext()
	for i := range graph.Graph.Nodes {
		if graph.Graph.Nodes[i].ID == "code_2" {
			parent := "human_1"
			graph.Graph.Nodes[i].ParentID = &parent
		}
	}
	result := run(t, graph, facts, nil, nil)
	if !hasMessage(result, "Human Input 'human_1' continuation ownership disagrees with its immutable fingerprint") {
		t.Fatalf("missing continuation-ownership diagnostic: %v", messages(result))
	}
}

// TestHumanInputContinuationSealCannotBeOverwritten ports
// test_graph_verifier.py:586: sealing freezes container ownership; a second
// seal after the graph changed must not overwrite the frozen fingerprint.
func TestHumanInputContinuationSealCannotBeOverwritten(t *testing.T) {
	graph, facts := humanInputContext()
	facts.SealHumanInputContinuationOwnership(&graph.Graph)
	// Move the continuation into a loop and re-seal: the frozen ownership
	// must remain the plain (no container) fingerprint.
	parent := "loop_1"
	for i := range graph.Graph.Nodes {
		if graph.Graph.Nodes[i].ID == "code_2" {
			graph.Graph.Nodes[i].ParentID = &parent
		}
	}
	graph.Graph.Nodes = append(graph.Graph.Nodes, vnode("loop_1", "loop", &dify.LoopDataNode{LoopCount: 1}))
	facts.SealHumanInputContinuationOwnership(&graph.Graph)
	contract := facts.HumanInputContracts["human_1"]
	if contract.ContinuationOwnership == nil || contract.ContinuationOwnership.ParentID != nil {
		t.Fatalf("second seal overwrote frozen ownership: %+v", contract.ContinuationOwnership)
	}
	// And the verification must still pass with the frozen fingerprint.
	result := run(t, graph, facts, nil, nil)
	if !hasMessage(result, "Human Input 'human_1' continuation ownership disagrees with its immutable fingerprint") {
		t.Fatalf("moving the continuation after seal must be flagged: %v", messages(result))
	}
}

// ── finding 7: missing per-rule cases ───────────────────────────────

func TestVerifyLoopContracts(t *testing.T) {
	// Non-positive loop_count (test_semantic_graph_verifier.py:51).
	result := run(t, mkGraph([]dify.NodeDecl{
		vnode("loop_1", "loop", &dify.LoopDataNode{LoopCount: 0}),
	}, nil), nil, nil, nil)
	if !hasMessage(result, "Loop 'loop_1' has non-positive loop_count 0") {
		t.Fatalf("missing loop_count diagnostic: %v", messages(result))
	}
	// Duplicate loop state labels.
	result = run(t, mkGraph([]dify.NodeDecl{
		vnode("loop_1", "loop", &dify.LoopDataNode{LoopCount: 1, LoopVariables: []dify.LoopVariable{
			{Variable: "__index"}, {Variable: "__index"},
		}}),
	}, nil), nil, nil, nil)
	if !hasMessage(result, "Loop 'loop_1' has duplicate loop state labels") {
		t.Fatalf("missing duplicate-labels diagnostic: %v", messages(result))
	}
	// Incomplete function-return escape state (__return_value without
	// __returning).
	result = run(t, mkGraph([]dify.NodeDecl{
		vnode("loop_1", "loop", &dify.LoopDataNode{LoopCount: 1, LoopVariables: []dify.LoopVariable{
			{Variable: "__index"}, {Variable: "__return_value"},
		}}),
	}, nil), nil, nil, nil)
	if !hasMessage(result, "Loop 'loop_1' has an incomplete function-return escape state") {
		t.Fatalf("missing escape-state diagnostic: %v", messages(result))
	}
}

func TestVerifyLoopContractsPass(t *testing.T) {
	result := run(t, mkGraph([]dify.NodeDecl{
		vnode("loop_1", "loop", &dify.LoopDataNode{LoopCount: 1, LoopVariables: []dify.LoopVariable{
			{Variable: "__index"}, {Variable: "__returning"}, {Variable: "__return_value"},
		}}),
	}, nil), nil, nil, nil)
	if !result.OK() {
		t.Fatalf("want OK, got %v", messages(result))
	}
}

func TestVerifyParallelIterations(t *testing.T) {
	result := run(t, mkGraph([]dify.NodeDecl{
		vnode("iter_1", "iteration", &dify.IterationDataNode{IsParallel: true, ParallelNums: 0, ErrorHandleMode: "terminated"}),
	}, nil), nil, nil, nil)
	if !hasMessage(result, "Parallel Iteration 'iter_1' has invalid concurrency or error mode") {
		t.Fatalf("missing concurrency diagnostic: %v", messages(result))
	}
	result = run(t, mkGraph([]dify.NodeDecl{
		vnode("iter_1", "iteration", &dify.IterationDataNode{IsParallel: true, ParallelNums: 4, ErrorHandleMode: "bogus"}),
	}, nil), nil, nil, nil)
	if !hasMessage(result, "Parallel Iteration 'iter_1' has invalid concurrency or error mode") {
		t.Fatalf("missing error-mode diagnostic: %v", messages(result))
	}
	result = run(t, mkGraph([]dify.NodeDecl{
		vnode("iter_1", "iteration", &dify.IterationDataNode{IsParallel: true, ParallelNums: 4, ErrorHandleMode: "terminated"}),
	}, nil), nil, nil, nil)
	if !result.OK() {
		t.Fatalf("valid parallel iteration should pass: %v", messages(result))
	}
}

// TestVerifyCodePassthroughMetadataOracle ports
// test_graph_verifier_gaps.py:476-515: helper-like or empty user Code nodes
// without compiler call metadata must pass (no regex false positive).
func TestVerifyCodePassthroughMetadataOracle(t *testing.T) {
	helper := vnode("code1", "code", &dify.CodeDataNode{
		CodeLanguage: "python3",
		Code:         "import helper\ndef process(x):\n    return helper.transform(x)\n",
	})
	empty := vnode("code2", "code", &dify.CodeDataNode{CodeLanguage: "python3"})
	result := run(t, mkGraph([]dify.NodeDecl{
		vnode("start1", "start", &dify.StartDataNode{}),
		helper, empty,
	}, []dify.EdgeDecl{
		vedge("e1", "start1", "code1", ""),
		vedge("e2", "code1", "code2", ""),
	}), nil, nil, nil)
	if !result.OK() {
		t.Fatalf("helper-like/empty code without metadata must pass: %v", messages(result))
	}
}

func TestVerifyTerminalReturnSuccessor(t *testing.T) {
	facts := graphfacts.NewSemanticGraphFacts()
	facts.FunctionScopes[binding.ScopeID(1)] = true
	tail := "tail_1"
	facts.ConsumedExits = append(facts.ConsumedExits, graphfacts.ConsumedExitProof{
		Kind:          binding.ExitReturn,
		TargetScopeID: binding.ScopeID(1),
		TailNodeID:    &tail,
		Terminal:      true,
	})
	result := run(t, mkGraph([]dify.NodeDecl{
		vnode("tail_1", "end", &dify.OutputNode{}),
		vnode("x_2", "code", &dify.CodeDataNode{CodeLanguage: "python3"}),
	}, []dify.EdgeDecl{
		vedge("e1", "tail_1", "x_2", ""),
	}), facts, nil, nil)
	if !hasMessage(result, "Consumed RETURN exit: terminal return tail 'tail_1' has a successor") {
		t.Fatalf("missing terminal-return diagnostic: %v", messages(result))
	}
}

func TestVerifyParentCycleRejection(t *testing.T) {
	result := run(t, mkGraph([]dify.NodeDecl{
		vnodeParent("a_1", "iteration", &dify.IterationDataNode{}, "b_1"),
		vnodeParent("b_1", "iteration", &dify.IterationDataNode{}, "a_1"),
	}, nil), nil, nil, nil)
	if !hasMessage(result, "Container parent cycle detected among nodes ['a_1', 'b_1']") {
		t.Fatalf("missing parent-cycle diagnostic: %v", messages(result))
	}
}

func TestVerifyParentEdgeCases(t *testing.T) {
	// None / empty-string parent is allowed (test_graph_verifier.py:762-786).
	noParent := vnode("a_1", "iteration", &dify.IterationDataNode{})
	emptyParent := vnode("b_1", "iteration", &dify.IterationDataNode{})
	emptyParent.ParentID = strPtr("")
	result := run(t, mkGraph([]dify.NodeDecl{noParent, emptyParent}, nil), nil, nil, nil)
	if !result.OK() {
		t.Fatalf("nil/empty parent must be allowed: %v", messages(result))
	}
	// Multiple invalid parents produce one error each.
	result = run(t, mkGraph([]dify.NodeDecl{
		vnodeParent("a_1", "iteration", &dify.IterationDataNode{}, "ghost_1"),
		vnodeParent("b_1", "iteration", &dify.IterationDataNode{}, "ghost_2"),
	}, nil), nil, nil, nil)
	if !hasMessage(result, "Node 'a_1' parent_id 'ghost_1' does not reference an existing node") {
		t.Fatalf("missing invalid-parent diagnostic for a_1: %v", messages(result))
	}
	if !hasMessage(result, "Node 'b_1' parent_id 'ghost_2' does not reference an existing node") {
		t.Fatalf("missing invalid-parent diagnostic for b_1: %v", messages(result))
	}
}

// TestVerifyEdgeEmptySourceTargetAllowed ports test_graph_verifier.py:715-726:
// edges with an empty source or target are not endpoint errors.
func TestVerifyEdgeEmptySourceTargetAllowed(t *testing.T) {
	result := run(t, mkGraph([]dify.NodeDecl{
		vnode("a_1", "start", &dify.StartDataNode{}),
		vnode("b_1", "end", &dify.OutputNode{}),
	}, []dify.EdgeDecl{
		vedge("e1", "", "b_1", ""),
		vedge("e2", "a_1", "", ""),
	}), nil, nil, nil)
	if !result.OK() {
		t.Fatalf("empty source/target edges must verify clean: %v", messages(result))
	}
}

func TestVerifyDuplicateNodeIDs(t *testing.T) {
	result := run(t, mkGraph([]dify.NodeDecl{
		vnode("dup", "start", &dify.StartDataNode{}),
		vnode("dup", "end", &dify.OutputNode{}),
	}, nil), nil, nil, nil)
	if !hasMessage(result, "Graph node at index 1 has duplicate id 'dup'") {
		t.Fatalf("missing duplicate-id diagnostic: %v", messages(result))
	}
	// Duplicate ids are flagged regardless of edge validity.
	result = run(t, mkGraph([]dify.NodeDecl{
		vnode("first", "start", &dify.StartDataNode{}),
		vnode("first", "llm", &dify.LLMDataNode{}),
		vnode("other", "end", &dify.OutputNode{}),
	}, []dify.EdgeDecl{
		vedge("e1", "first", "other", ""),
	}), nil, nil, nil)
	if !hasMessage(result, "Graph node at index 1 has duplicate id 'first'") {
		t.Fatalf("duplicate ids must be flagged regardless of edge validity: %v", messages(result))
	}
}

func TestVerifyEmptyIDNodeWithEdges(t *testing.T) {
	result := run(t, mkGraph([]dify.NodeDecl{
		vnode("", "start", &dify.StartDataNode{}),
		vnode("B", "end", &dify.OutputNode{}),
	}, []dify.EdgeDecl{
		vedge("e1", "B", "", ""),
	}), nil, nil, nil)
	if !hasMessage(result, "Graph node at index 0 has an empty id") {
		t.Fatalf("empty-id node with incoming edge must be flagged: %v", messages(result))
	}
}

func TestVerifyIterationFalseFlag(t *testing.T) {
	// Child of an iteration container that claims is_in_iteration=false
	// (test_graph_verifier_gaps.py:260).
	result := run(t, mkGraph([]dify.NodeDecl{
		vnode("iter_1", "iteration", &dify.IterationDataNode{}),
		vnodeParent("child_2", "code", &dify.CodeDataNode{DataNode: dify.DataNode{IsInIteration: false}, CodeLanguage: "python3"}, "iter_1"),
	}, nil), nil, nil, nil)
	if !hasMessage(result, "Node 'child_2' has inconsistent iteration flags for nearest container 'iter_1' (active=False, iteration_id='')") {
		t.Fatalf("missing iteration false-flag diagnostic: %v", messages(result))
	}
}

func codeWithResult(id string) dify.NodeDecl {
	return vnode(id, "code", &dify.CodeDataNode{
		CodeLanguage: "python3",
		Outputs:      map[string]dify.OutputDefNode{"result": {}},
	})
}

// TestVerifySelectorDisconnectedProducer ports
// test_semantic_graph_verifier.py:61: a producer that never reaches the
// consumer yields "does not dominate" (single E1099).
func TestVerifySelectorDisconnectedProducer(t *testing.T) {
	result := run(t, mkGraph([]dify.NodeDecl{
		codeWithResult("prod_2"),
		vnode("cons_3", "code", &dify.CodeDataNode{CodeLanguage: "python3", Variables: []dify.VariableSelectorNode{
			dify.NewVariableSelectorNode("prod_2", "result"),
		}}),
	}, nil), nil, nil, nil)
	if !hasMessage(result, "Selector producer 'prod_2' does not dominate consumer 'cons_3' for path ['prod_2', 'result']; add an explicit legal merge") {
		t.Fatalf("missing dominance diagnostic: %v", messages(result))
	}
}

// TestVerifySelectorNoLegalExecutionCoverage ports
// test_semantic_graph_verifier.py:73: a rootless cycle bypasses entry
// reachability, so the consumer has no dominator entry at all.
func TestVerifySelectorNoLegalExecutionCoverage(t *testing.T) {
	result := run(t, mkGraph([]dify.NodeDecl{
		vnode("start_1", "start", &dify.StartDataNode{}),
		codeWithResult("prod_2"),
		vnode("cons_3", "code", &dify.CodeDataNode{CodeLanguage: "python3", Variables: []dify.VariableSelectorNode{
			dify.NewVariableSelectorNode("prod_2", "result"),
		}}),
	}, []dify.EdgeDecl{
		vedge("e1", "prod_2", "cons_3", ""),
		vedge("e2", "cons_3", "prod_2", ""),
	}), nil, nil, nil)
	if !hasMessage(result, "Selector consumer 'cons_3' has no legal execution coverage for producer 'prod_2' and path ['prod_2', 'result']") {
		t.Fatalf("missing execution-coverage diagnostic: %v", messages(result))
	}
}
