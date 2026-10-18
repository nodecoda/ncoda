package dify

// Selector inspection — port of
// src/nclang/lang/backends/dify/graph_selector_inspection.py (19 lines).
// collect_node_selectors / collect_node_conditions are shared by the
// human-input lowerer (branch submitted-selector capture) and the
// post-lowering graph verifier (re-derivation), so the Go port lives here
// in the node-model package — one copy, no drift (task-38 review MINOR 4).

// CollectNodeConditions returns every target condition owned directly by one
// emitted graph node, mirroring collect_node_conditions
// (graph_selector_inspection.py:8-15).
func CollectNodeConditions(node *NodeDecl) []ConditionNode {
	switch data := node.Data.(type) {
	case *IfElseDataNode:
		var out []ConditionNode
		for _, c := range data.Cases {
			out = append(out, c.Conditions...)
		}
		return out
	case *LoopDataNode:
		return data.BreakConditions
	}
	return nil
}

// CollectNodeSelectors returns every variable selector owned directly by one
// emitted graph node, mirroring collect_node_selectors
// (graph_selector_inspection.py). Python reads getattr(data, "variables",
// ()) and filters isinstance(selector, VariableSelectorNode) — the Go
// analogue is a type switch over the node types whose variables field
// carries VariableSelectorNode values (StartDataNode.variables is
// list[VariableDef], which the isinstance filter drops, so it is absent).
func CollectNodeSelectors(node *NodeDecl) []VariableSelectorNode {
	if node.Data == nil {
		return nil
	}
	var selectors []VariableSelectorNode
	switch data := node.Data.(type) {
	case *AnswerNode:
		selectors = append(selectors, data.Variables...)
	case *LLMDataNode:
		selectors = append(selectors, data.Variables...)
	case *CodeDataNode:
		selectors = append(selectors, data.Variables...)
	case *HTTPRequestDataNode:
		selectors = append(selectors, data.Variables...)
	case *ToolDataNode:
		selectors = append(selectors, data.Variables...)
	case *TemplateTransformDataNode:
		selectors = append(selectors, data.Variables...)
	case *KnowledgeRetrievalDataNode:
		selectors = append(selectors, data.Variables...)
	case *ParameterExtractorDataNode:
		selectors = append(selectors, data.Variables...)
	case *VariableAggregatorDataNode:
		selectors = append(selectors, data.Variables...)
	case *OutputNode:
		for _, output := range data.Outputs {
			if len(output.ValueSelector) > 0 {
				// __post_init__ (ast_nodes.py:60-64) derives node_id/key from
				// path[:2]; without it V4/V4b/V8 skip end-node output mappings
				// (R1 finding 2 — Python emits E1099 for missing producers/ports).
				// Derive runs on a copy, never mutating the graph node.
				selector := VariableSelectorNode{Path: output.ValueSelector, ValueType: output.ValueType}
				selector.Derive()
				selectors = append(selectors, selector)
			}
		}
	case *QuestionClassifierDataNode:
		if data.QueryVariableSelector != nil {
			selectors = append(selectors, *data.QueryVariableSelector)
		}
	}
	for _, condition := range CollectNodeConditions(node) {
		selectors = append(selectors, condition.VariableSelector)
	}
	return selectors
}
