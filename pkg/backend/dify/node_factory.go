package dify

// Node factory — port of src/nclang/lang/backends/dify/node_factory.py
// (moved from pkg/backend/dify/legacy: shared AST constructors used by both the
// legacy lowering path and the IR render; exported so both packages can use them).
// (Task 20 subset). make_node_decl (:75-83), make_start_node (:85-88),
// make_output_node (:90-93), make_answer_node (:95-98), make_llm_node
// (:100-125) and split_model_name (:127-131). create_node (:449-461) is a
// test-compat wrapper over dataclass introspection; the Go NodeData
// constructors already exist in pkg/backend/dify, so it is not ported.
//
// Position: every factory node uses PositionNode{x:0, y:0} (node_factory.py
// :81-82); width/height stay nil and the NodeDecl defaults (source_position
// "right", target_position "left", selected false, z_index nil) match the
// Python dataclass defaults (ast_nodes.py:621-637) — the oracle dump locks
// them (start_1 position {0,0}, width None, height None).

import (
	"strings"
)

// MakeNodeDecl creates a NodeDecl wrapping a DataNode instance, mirroring
// node_factory.py:75-83.
func MakeNodeDecl(nodeID, nodeType string, data NodeData) NodeDecl {
	return NodeDecl{
		ID:             nodeID,
		NodeType:       nodeType,
		Data:           data,
		Position:       PositionNode{X: 0, Y: 0},
		SourcePosition: "right",
		TargetPosition: "left",
	}
}

// MakeStartNode mirrors node_factory.py:85-88.
func MakeStartNode(nodeID string, variables []VariableDef) NodeDecl {
	if variables == nil {
		variables = []VariableDef{}
	}
	data := &StartDataNode{Variables: variables}
	return MakeNodeDecl(nodeID, "start", data)
}

// MakeOutputNode mirrors node_factory.py:90-93.
func MakeOutputNode(nodeID string, outputs []OutputMapping) NodeDecl {
	if outputs == nil {
		outputs = []OutputMapping{}
	}
	data := &OutputNode{Outputs: outputs}
	return MakeNodeDecl(nodeID, "end", data)
}

// MakeAnswerNode mirrors node_factory.py:95-98. Variables is a non-nil
// empty slice like the Python default_factory (ast_nodes.py:431) — the 05
// oracle locks answer variables to [].
func MakeAnswerNode(nodeID, answerRef string) NodeDecl {
	data := &AnswerNode{Answer: answerRef, Variables: []VariableSelectorNode{}}
	return MakeNodeDecl(nodeID, "answer", data)
}

// MakeLLMNode mirrors node_factory.py:100-125. The factory form does not
// carry completion params (Python ModelConfigNode defaults apply:
// temperature 1.0 / top_p 1.0 — NewModelConfigNode); the call translator
// (_translate_llm_call) builds the model config with explicit params.
func MakeLLMNode(nodeID, modelProvider, modelName, promptText string, variables []VariableSelectorNode) NodeDecl {
	provider := modelProvider
	if provider == "" {
		if modelName != "" {
			provider = "custom"
		}
	}
	var model *ModelConfigNode
	if provider != "" || modelName != "" {
		m := NewModelConfigNode()
		m.Provider = provider
		m.Name = modelName
		m.Mode = "chat"
		// Python ModelConfigNode() carries completion_params with
		// default_factory stop=[]/extra={} (ast_nodes.py:88-90); the Go
		// build-phase node mirrors that at the factory boundary.
		m.CompletionParams.Stop = []string{}
		m.CompletionParams.Extra = map[string]any{}
		model = &m
	}
	var prompt []PromptMessageNode
	if promptText != "" {
		prompt = []PromptMessageNode{{Text: promptText}}
	}
	// R1 M2: Python prompt_nodes starts as [] (node_factory.py:100-125);
	// a factory call without prompt text yields an empty list, not nil.
	if prompt == nil {
		prompt = []PromptMessageNode{}
	}
	if variables == nil {
		variables = []VariableSelectorNode{}
	}
	data := &LLMDataNode{
		Model:          model,
		PromptTemplate: prompt,
		Variables:      variables,
		Context:        &ContextConfig{Enabled: false, VariableSelector: []string{}},
	}
	return MakeNodeDecl(nodeID, "llm", data)
}

// splitModelName mirrors node_factory.py:127-131: one validated model
// reference splits on the first "/"; without one the provider is "" and the
// caller's custom-provider fallback applies.
func SplitModelName(model string) (string, string) {
	if idx := strings.IndexByte(model, '/'); idx >= 0 {
		return model[:idx], model[idx+1:]
	}
	return "", model
}

// MakeListOperatorNode mirrors node_factory.py:365-385: one native
// list-operator node. The five configs carry their Python dataclass
// defaults (ListFilterConfig conditions [], ListExtractConfig serial "1",
// ListOrderConfig value "asc", ListLimitConfig size -1) — the caller
// (lowerNativeCall) starts from those defaults and enables the active
// operation. MakeNodeDecl supplies the node-type title via AddNode.
func MakeListOperatorNode(nodeID string, variable []string, filterBy ListFilterConfig, extractBy ListExtractConfig, orderBy ListOrderConfig, limit ListLimitConfig, varType, itemVarType string) NodeDecl {
	data := &ListOperatorDataNode{
		Variable:    variable,
		FilterBy:    filterBy,
		ExtractBy:   extractBy,
		OrderBy:     orderBy,
		Limit:       limit,
		VarType:     varType,
		ItemVarType: itemVarType,
	}
	return MakeNodeDecl(nodeID, "list-operator", data)
}

// MakeHTTPRequestNode mirrors node_factory.py:388-401. The HTTP node carries
// the Python HTTPRequestDataNode defaults: params {} (ast_nodes.py:467
// default_factory) and authorization AuthConfig(type="no-auth")
// (node_factory.py:397).
func MakeHTTPRequestNode(nodeID, method, url string, variables []VariableSelectorNode) NodeDecl {
	if variables == nil {
		variables = []VariableSelectorNode{}
	}
	data := &HTTPRequestDataNode{
		Method:        method,
		URL:           url,
		Params:        map[string]any{},
		Authorization: &AuthConfig{Type: "no-auth"},
		Variables:     variables,
	}
	return MakeNodeDecl(nodeID, "http-request", data)
}

// MakeToolNode mirrors node_factory.py:403-417. The empty Python
// default_factory collections (tool_configuration, tool_configurations,
// output_schema, param_schemas, params) are filled so serialization matches
// the reference dataclass; the None-able fields stay nil.
// MakeToolNode builds a tool node. variables is accepted for legacy-lowerer
// parity only — serializeTool does not serialize it (I5), so callers may
// pass an empty list; the tool_parameters interpolations carry the dataflow.
func MakeToolNode(nodeID, toolName, providerID string, toolParameters map[string]any, parameterOrder []string, variables []VariableSelectorNode) NodeDecl {
	if toolParameters == nil {
		toolParameters = map[string]any{}
	}
	if variables == nil {
		variables = []VariableSelectorNode{}
	}
	data := &ToolDataNode{
		ToolName:           toolName,
		ProviderID:         providerID,
		ToolParameters:     toolParameters,
		ToolParameterOrder: parameterOrder,
		ToolConfiguration:  map[string]any{},
		ToolConfigurations: map[string]any{},
		OutputSchema:       map[string]any{},
		ParamSchemas:       []map[string]any{},
		Params:             map[string]any{},
		Variables:          variables,
	}
	return MakeNodeDecl(nodeID, "tool", data)
}

// MakeKnowledgeRetrievalNode mirrors node_factory.py:419-431. The empty
// Python default_factory collections (multiple/single_retrieval_config)
// are filled for serialization parity; query_variable_selector stays nil
// when absent.
func MakeKnowledgeRetrievalNode(nodeID string, datasetIDs []string, queryVariableSelector *VariableSelectorNode, variables []VariableSelectorNode) NodeDecl {
	if datasetIDs == nil {
		datasetIDs = []string{}
	}
	if variables == nil {
		variables = []VariableSelectorNode{}
	}
	data := &KnowledgeRetrievalDataNode{
		DatasetIDs:              datasetIDs,
		QueryVariableSelector:   queryVariableSelector,
		MultipleRetrievalConfig: map[string]any{},
		SingleRetrievalConfig:   map[string]any{},
		Variables:               variables,
	}
	return MakeNodeDecl(nodeID, "knowledge-retrieval", data)
}

// MakeTemplateTransformNode mirrors node_factory.py:433-446.
func MakeTemplateTransformNode(nodeID, template string, variables []VariableSelectorNode) NodeDecl {
	if variables == nil {
		variables = []VariableSelectorNode{}
	}
	data := &TemplateTransformDataNode{Template: template, Variables: variables}
	return MakeNodeDecl(nodeID, "template-transform", data)
}

// MakeIterationNode mirrors node_factory.py:318-326: iterator_selector
// defaults to [] and start_node_id to "" exactly like the Python factory.
func MakeIterationNode(nodeID string, iteratorSelector []string, startNodeID string) NodeDecl {
	if iteratorSelector == nil {
		iteratorSelector = []string{}
	}
	data := NewIterationDataNode()
	data.IteratorSelector = iteratorSelector
	data.StartNodeID = startNodeID
	return MakeNodeDecl(nodeID, "iteration", data)
}

// MakeIterationStartNode mirrors node_factory.py:330-332.
func MakeIterationStartNode(nodeID string) NodeDecl {
	data := NewIterationStartDataNode()
	return MakeNodeDecl(nodeID, "iteration-start", data)
}

// MakeVariableAggregatorNode mirrors node_factory.py:357-362: variables
// default to [] like the Python default_factory (ast_nodes.py:556).
func MakeVariableAggregatorNode(nodeID string, variables []VariableSelectorNode) NodeDecl {
	if variables == nil {
		variables = []VariableSelectorNode{}
	}
	data := NewVariableAggregatorDataNode()
	data.Variables = variables
	return MakeNodeDecl(nodeID, "variable-aggregator", data)
}

// MakeLoopNode mirrors node_factory.py:335-337.
func MakeLoopNode(nodeID string) NodeDecl {
	data := NewLoopDataNode()
	return MakeNodeDecl(nodeID, "loop", data)
}

// MakeLoopStartNode mirrors node_factory.py:340-342.
func MakeLoopStartNode(nodeID string) NodeDecl {
	data := NewLoopStartDataNode()
	return MakeNodeDecl(nodeID, "loop-start", data)
}

// MakeLoopEndNode mirrors node_factory.py:345-347.
func MakeLoopEndNode(nodeID string) NodeDecl {
	data := NewLoopEndDataNode()
	return MakeNodeDecl(nodeID, "loop-end", data)
}

// MakeAssignerV2Node mirrors node_factory.py:350-354: a canonical non-empty
// assignment node. The empty-items ValueError becomes a panic (the Go
// analogue of the reference's hard error, like NewAggregateFact).
func MakeAssignerV2Node(nodeID string, items []AssignmentItem) NodeDecl {
	if len(items) == 0 {
		panic("dify: assigner v2 requires at least one operation item")
	}
	data := &AssignerDataNode{Items: items}
	return MakeNodeDecl(nodeID, "assigner", data)
}
