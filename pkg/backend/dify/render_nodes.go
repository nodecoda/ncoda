package dify

// Per-kind IR→Dify translates. Shapes mirror the legacy lowering values
// (node_factory + per-lowerer defaults); the serializer handles omission
// rules, so constructing equal values yields byte-identical YAML.

import (
	"fmt"
	"strings"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/llm"
	"github.com/nodecoda/ncoda/pkg/tool"
)

// emitLLM mirrors call_translator's LLM lowering: model split, prompt
// template from messages, completion params, context, inputs.
func (r *irRenderer) emitLLM(n *ir.LLMCall) NodeDecl {
	provider, name := SplitModelName(n.Model)
	if provider == "" && name != "" {
		provider = "custom"
	}
	var model *ModelConfigNode
	if provider != "" || name != "" {
		cp := NewCompletionParams()
		cp.Stop = []string{}
		cp.Extra = map[string]any{}
		if v, ok := r.configValue(n.Config, "temperature"); ok {
			if f, ok2 := asFloat(v); ok2 {
				cp.Temperature = f
			}
		}
		if v, ok := r.configValue(n.Config, "max_tokens"); ok {
			if i, ok2 := asInt(v); ok2 {
				cp.MaxTokens = &i
			}
		}
		m := NewModelConfigNode()
		m.Provider = provider
		m.Name = name
		m.Mode = "chat"
		m.CompletionParams = cp
		model = &m
	}

	var prompt []PromptMessageNode
	if msgs := r.messages(n.Config); len(msgs) > 0 {
		for _, m := range msgs {
			prompt = append(prompt, PromptMessageNode{Role: m.Role, Text: r.rewriteTemplate(m.Content)})
		}
	} else {
		prompt = []PromptMessageNode{}
	}

	vars := make([]VariableSelectorNode, 0, len(n.Inputs))
	for _, in := range n.Inputs {
		// The producer port may be nested (inline code: "result.summary").
		// portFor returns the dotted Dify port; emit the full selector path
		// [node, result, summary] so the verifier's code-port contract
		// resolves the leaf field, and resolve the value_type against the
		// dotted port (nestedPortType walks the field chain).
		key := r.portFor(in.BlockID, in.Name)
		var sel VariableSelectorNode
		if strings.Contains(key, ".") {
			path := append([]string{r.difyIDs[in.BlockID]}, strings.Split(key, ".")...)
			sel = VariableSelectorFromList(path)
			sel.Derive()
		} else {
			sel = r.inputSelector(in)
		}
		// bound-stdlib LLM steps declare one named variable per message ref
		// (legacy makeBoundLLMNode: variable value_N + value type). The
		// value_type resolves the IR producer port (in.Name); nested refs
		// resolve through the dotted Dify key so nestedPortType can walk
		// the field chain.
		if len(in.Path) > 0 {
			sel.Variable = fmt.Sprintf("value_%d", len(vars)+1)
			typeKey := in.Name
			if strings.Contains(key, ".") {
				typeKey = key
			}
			sel.ValueType = r.portType(in.BlockID, typeKey)
		}
		vars = append(vars, sel)
	}

	data := &LLMDataNode{
		Model:          model,
		PromptTemplate: prompt,
		Variables:      vars,
		Context:        &ContextConfig{Enabled: false, VariableSelector: []string{}},
	}
	if n.Result != nil && n.Result.PayloadSchema != nil && n.Result.PayloadSchema.Kind != llm.KindString {
		data.StructuredOutput = &StructuredOutputConfig{Enabled: true, SchemaDef: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"value": llmSchemaDef(n.Result.PayloadSchema)},
			"required":   []string{"value"},
		}}
	}
	if n.Exception != nil {
		data.RetryConfig = retryConfigFrom(n.Exception)
		if n.Exception.FailBranch {
			strategy := "fail-branch"
			data.ErrorStrategy = &strategy
		}
	}
	return MakeNodeDecl(r.idFor(n), "llm", data)
}

func llmSchemaDef(s *llm.LLMSchema) map[string]any {
	if s == nil {
		return map[string]any{}
	}
	switch s.Kind {
	case llm.KindString:
		return map[string]any{"type": "string"}
	case llm.KindBool:
		return map[string]any{"type": "boolean"}
	case llm.KindInt64:
		return map[string]any{"type": "integer"}
	case llm.KindFloat64:
		return map[string]any{"type": "number"}
	case llm.KindArray:
		return map[string]any{"type": "array", "items": llmSchemaDef(s.Elem)}
	case llm.KindMap:
		return map[string]any{"type": "object", "additionalProperties": llmSchemaDef(s.Value)}
	case llm.KindObject:
		// open object: no closed properties (ideas-todo/LLM-OPEN-OBJECT.md).
		return map[string]any{"type": "object"}
	case llm.KindOptional:
		return map[string]any{"anyOf": []any{llmSchemaDef(s.Value), map[string]any{"type": "null"}}}
	case llm.KindEnum:
		return map[string]any{"type": "string", "enum": append([]string(nil), s.Values...)}
	case llm.KindRecord:
		props := map[string]any{}
		required := []string{}
		for _, f := range s.Fields {
			props[f.Name] = llmSchemaDef(f.Schema)
			if f.Required {
				required = append(required, f.Name)
			}
		}
		out := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 {
			out["required"] = required
		}
		return out
	default:
		return map[string]any{}
	}
}

// emitExtraction expands a target-neutral extraction model call into Dify's
// native parameter-extractor and its public-result adapter.
func (r *irRenderer) emitExtraction(n *ir.LLMCall) error {
	spec := n.Extraction
	extractorID := r.nextNodeID("extract")
	adapterID := r.nextNodeID("extract_result")

	provider, modelName := SplitModelName(n.Model)
	model := NewModelConfigNode()
	model.Provider = provider
	model.Name = modelName
	model.Mode = "chat"

	params := make([]ParameterDef, 0, len(spec.Fields))
	for _, field := range spec.Fields {
		var options *[]string
		if len(field.Options) > 0 {
			copyOptions := append([]string(nil), field.Options...)
			options = &copyOptions
		}
		params = append(params, ParameterDef{
			Name:        field.Name,
			Type:        field.GraphonType,
			Description: field.Description,
			Required:    true,
			Options:     options,
		})
	}

	query := []string{}
	if len(n.Inputs) > 0 {
		query = []string{r.difyIDs[n.Inputs[0].BlockID], r.portFor(n.Inputs[0].BlockID, n.Inputs[0].Name)}
	}
	var memory *MemoryConfig
	if spec.MemoryTurns != nil {
		m := NewMemoryConfig()
		size := *spec.MemoryTurns + 1
		m.Window = &MemoryWindowConfig{Enabled: true, Size: &size}
		memory = &m
	}
	reasoning := spec.Strategy
	extractor := MakeNodeDecl(extractorID, "parameter-extractor", &ParameterExtractorDataNode{
		Model:         &model,
		Parameters:    params,
		Query:         query,
		Instruction:   spec.Instruction,
		Memory:        memory,
		ReasoningMode: &reasoning,
		Vision:        &VisionConfig{Enabled: false, Configs: NewOrderedMap()},
		Variables:     []VariableSelectorNode{},
	})
	setDataTitle(extractor.Data, r.titleFor("parameter-extractor"))

	signature := []string{"__is_success", "__reason", "__usage"}
	valueLines := make([]string, 0, len(spec.Fields))
	variables := []VariableSelectorNode{
		{Variable: "__is_success", ValueType: "number", Path: []string{extractorID, "__is_success"}},
		{Variable: "__reason", ValueType: "string", Path: []string{extractorID, "__reason"}},
		{Variable: "__usage", ValueType: "object", Path: []string{extractorID, "__usage"}},
	}
	valueChildren := map[string]*OutputDefNode{}
	childOrder := make([]string, 0, len(spec.Fields))
	for i, field := range spec.Fields {
		alias := fmt.Sprintf("field_%d", i)
		signature = append(signature, alias)
		valueLines = append(valueLines, fmt.Sprintf("            %q: %s,", field.Name, alias))
		variables = append(variables, VariableSelectorNode{
			Variable:  alias,
			ValueType: field.GraphonType,
			Path:      []string{extractorID, field.Name},
		})
		child := codeOutputDefFor(field.Type)
		valueChildren[field.Name] = &child
		childOrder = append(childOrder, field.Name)
	}
	code := fmt.Sprintf("def main(%s):\n    return {\n        \"ok\": bool(__is_success),\n        \"reason\": __reason or \"\",\n        \"usage\": __usage or {},\n        \"value\": {\n%s\n        },\n    }", strings.Join(signature, ", "), strings.Join(valueLines, "\n"))
	outputs := map[string]OutputDefNode{
		"value":  {Type: "object", Children: valueChildren, ChildOrder: childOrder},
		"ok":     {Type: "boolean", Children: map[string]*OutputDefNode{}},
		"reason": {Type: "string", Children: map[string]*OutputDefNode{}},
		"usage":  {Type: "object", Children: map[string]*OutputDefNode{}},
	}
	adapter := MakeCodeNode(adapterID, code, outputs, variables)
	if data, ok := adapter.Data.(*CodeDataNode); ok {
		data.OutputOrder = []string{"value", "ok", "reason", "usage"}
	}
	setDataTitle(adapter.Data, r.titleFor("code"))

	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, extractor, adapter)
	r.difyIDs[n.NodeID()] = adapterID
	r.inputNodeIDs[n.NodeID()] = extractorID
	r.extractionEdges[n.NodeID()] = EdgeDecl{
		Source:       extractorID,
		Target:       adapterID,
		Type:         "custom",
		SourceHandle: "source",
	}
	return nil
}

// retryConfigFrom maps the IR exception payload onto the Dify retry
// config (legacy applyPolicies: MaxRetries / RetryInterval / enabled). A
// zero retry payload renders no retry_config at all (legacy only sets it
// when a RetryPolicy is present — a FailBranch attempt without a retry
// policy must not emit an empty retry block).
func retryConfigFrom(ec *ir.ExceptionConfig) *RetryConfig {
	if ec == nil || ec.MaxRetry <= 0 {
		return nil
	}
	return &RetryConfig{
		MaxRetries:    ec.MaxRetry,
		RetryInterval: ec.RetryIntervalMS,
		RetryEnabled:  true,
	}
}

// emitHTTP mirrors call_translator's http lowering shape.
func (r *irRenderer) emitHTTP(n *ir.HTTPCall) NodeDecl {
	url := r.rewriteTemplate(n.URL)
	variables := make([]VariableSelectorNode, 0, len(n.Inputs))
	for _, input := range n.Inputs {
		variables = append(variables, r.inputSelector(input))
	}
	data := &HTTPRequestDataNode{
		Method:        n.Method,
		URL:           url,
		Headers:       "",
		Params:        "",
		Body:          &HTTPBody{Type: "none", Data: ""},
		Authorization: &AuthConfig{Type: "no-auth"},
		Variables:     variables,
	}
	if n.Exception != nil {
		data.RetryConfig = retryConfigFrom(n.Exception)
		if n.Exception.TimeoutMS > 0 {
			seconds := n.Exception.TimeoutMS / 1000
			if seconds < 1 {
				seconds = 1
			}
			data.Timeout = &TimeoutConfig{MaxConnectTimeout: seconds, MaxReadTimeout: seconds}
		}
		if n.Exception.FailBranch {
			// attempt: the operation's failure branch renders as the dify
			// fail-branch error strategy (legacy lowerAttempt).
			fb := "fail-branch"
			data.ErrorStrategy = &fb
		}
	}
	return MakeNodeDecl(r.idFor(n), "http-request", data)
}

// emitCode mirrors code materialization: python3, variables, outputs.
func (r *irRenderer) emitCode(n *ir.CodeRun) NodeDecl {
	language := n.Language
	if language == "" {
		language = "python3"
	}
	vars := make([]VariableSelectorNode, 0, len(n.Inputs))
	for _, in := range n.Inputs {
		sel := r.inputSelector(in)
		// The code variable is the parameter name the generated python
		// declares (Path[0]); the selector key is the producer's mapped
		// port (inputSelector → portFor). Types are keyed by param name.
		param := in.Name
		if len(in.Path) > 0 {
			param = in.Path[0]
		}
		sel.Variable = param
		if t, ok := n.Types[param]; ok {
			sel.ValueType = codeValueTypeFor(t)
		}
		vars = append(vars, sel)
	}
	outputs := map[string]OutputDefNode{}
	types := n.OutTypes
	if len(types) == 0 {
		types = n.Types
	}
	order := n.OutOrder
	if len(order) == 0 {
		order = make([]string, 0, len(types))
		for name := range types {
			order = append(order, name)
		}
	}
	for _, name := range order {
		if t, ok := types[name]; ok {
			outputs[name] = codeOutputDefFor(t)
		}
	}
	node := MakeCodeNode(r.idFor(n), n.Code, outputs, vars)
	if cd, ok := node.Data.(*CodeDataNode); ok {
		cd.OutputOrder = order
	}
	_ = language
	return node
}

func (r *irRenderer) emitInlineCall(n *ir.CodeRun, plan *ir.InlineStmt) error {
	paramID := r.nextNodeID(plan.Name + "_param")
	bodyID := r.nextNodeID(plan.Name + "_code")

	paramNames := make([]string, 0, len(plan.Params))
	paramItems := make([]string, 0, len(plan.Params))
	paramVars := make([]VariableSelectorNode, 0, len(plan.Params))
	paramOutputs := map[string]OutputDefNode{}
	paramOrder := make([]string, 0, len(plan.Params))
	for _, param := range plan.Params {
		paramNames = append(paramNames, param.Name)
		paramItems = append(paramItems, fmt.Sprintf("'%s': %s", param.Name, param.Name))
		sel := r.inputSelector(param.Source)
		sel.Variable = param.Name
		sel.ValueType = codeValueTypeFor(param.Type)
		paramVars = append(paramVars, sel)
		paramOutputs[param.Name] = codeOutputDefFor(param.Type)
		paramOrder = append(paramOrder, param.Name)
	}
	paramCode := fmt.Sprintf("def main(%s) -> dict:\n    return {%s}", strings.Join(paramNames, ", "), strings.Join(paramItems, ", "))
	paramNode := MakeCodeNode(paramID, paramCode, paramOutputs, paramVars)
	if data, ok := paramNode.Data.(*CodeDataNode); ok {
		data.OutputOrder = paramOrder
	}
	setDataTitle(paramNode.Data, r.titleFor("code"))

	bodyVars := make([]VariableSelectorNode, 0, len(n.Inputs))
	for _, input := range n.Inputs {
		name := input.Name
		if len(input.Path) > 0 {
			name = input.Path[0]
		}
		bodyVars = append(bodyVars, VariableSelectorNode{
			Path: []string{paramID, name}, Variable: name,
			ValueType: codeValueTypeFor(n.Types[name]),
		})
	}
	bodyOutputs := map[string]OutputDefNode{}
	bodyOrder := append([]string(nil), n.OutOrder...)
	for _, name := range bodyOrder {
		bodyOutputs[name] = codeOutputDefFor(n.OutTypes[name])
	}
	bodyNode := MakeCodeNode(bodyID, n.Code, bodyOutputs, bodyVars)
	if data, ok := bodyNode.Data.(*CodeDataNode); ok {
		data.OutputOrder = bodyOrder
	}
	setDataTitle(bodyNode.Data, r.titleFor("code"))

	lastID := bodyID
	edges := []EdgeDecl{{Source: paramID, Target: bodyID, Type: "custom"}}
	if len(bodyOrder) > 1 {
		structID := r.nextNodeID(plan.Name + "_code_struct")
		structVars := make([]VariableSelectorNode, 0, len(bodyOrder))
		items := make([]string, 0, len(bodyOrder))
		children := map[string]*OutputDefNode{}
		for _, name := range bodyOrder {
			structVars = append(structVars, VariableSelectorNode{
				Path: []string{bodyID, name}, Variable: name,
				ValueType: codeValueTypeFor(n.OutTypes[name]),
			})
			items = append(items, fmt.Sprintf("'%s': %s", name, name))
			child := codeOutputDefFor(n.OutTypes[name])
			children[name] = &child
		}
		structCode := fmt.Sprintf("def main(%s) -> dict:\n    return {\"result\": {%s}}", strings.Join(bodyOrder, ", "), strings.Join(items, ", "))
		structNode := MakeCodeNode(structID, structCode, map[string]OutputDefNode{
			"result": {Type: "object", Children: children, ChildOrder: bodyOrder},
		}, structVars)
		if data, ok := structNode.Data.(*CodeDataNode); ok {
			data.OutputOrder = []string{"result"}
		}
		setDataTitle(structNode.Data, r.titleFor("code"))
		r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, paramNode, bodyNode, structNode)
		edges = append(edges, EdgeDecl{Source: bodyID, Target: structID, Type: "custom"})
		lastID = structID
	} else {
		r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, paramNode, bodyNode)
	}

	returnID := r.nextNodeID(plan.Name + "_return")
	returnType := codeOutputDefFor(plan.ReturnType)
	if returnType.Type == "object" && len(bodyOrder) > 0 {
		returnType.ChildOrder = append([]string(nil), bodyOrder...)
	}
	returnNode := MakeCodeNode(returnID,
		"def main(result) -> dict:\n    return {\"result\": result}",
		map[string]OutputDefNode{"result": returnType},
		[]VariableSelectorNode{{Path: []string{bodyID}, Variable: "result", ValueType: codeValueTypeFor(plan.ReturnType)}},
	)
	if data, ok := returnNode.Data.(*CodeDataNode); ok {
		data.OutputOrder = []string{"result"}
	}
	setDataTitle(returnNode.Data, r.titleFor("code"))
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, returnNode)
	edges = append(edges, EdgeDecl{Source: lastID, Target: returnID, Type: "custom"})

	r.difyIDs[n.NodeID()] = returnID
	r.inputNodeIDs[n.NodeID()] = paramID
	r.inlineResults[n.NodeID()] = true
	r.inlineEdges[n.NodeID()] = edges
	return nil
}

// codeValueTypeFor maps an IR port kind onto the Dify code-port value_type
// convention (the difycodec ToCodePort mapping: int/float → "number").
func codeValueTypeFor(t *ir.Type) string {
	if t == nil {
		return "string"
	}
	switch t.Kind {
	case "integer", "number":
		return "number"
	case "boolean":
		return "boolean"
	case "file":
		return "file"
	case "object":
		return "object"
	case "list":
		if t.Elem != nil {
			return "array[" + codeValueTypeFor(t.Elem) + "]"
		}
		return "array[string]"
	default:
		return "string"
	}
}

// emitTool renders a logical Tool call through the optional Dify binding.
func (r *irRenderer) emitTool(n *ir.ToolCall) (NodeDecl, error) {
	binding, err := r.toolBindings.resolve(n)
	if err != nil {
		return NodeDecl{}, err
	}
	params := map[string]any{}
	order := make([]string, 0, len(n.Params))
	for _, p := range n.Params {
		name := p.Key
		if binding != nil {
			if mapped, ok := binding.Arguments[name]; ok {
				name = mapped
			}
		}
		if p.Ref != nil {
			params[name] = "{{#" + r.difyIDs[p.Ref.BlockID] + "." + r.portFor(p.Ref.BlockID, p.Ref.Name) + "#}}"
		} else {
			params[name] = p.Value
		}
		order = append(order, name)
	}
	toolName := n.ToolName
	providerID := n.ProviderID
	// Variables intentionally empty (I5): serializeTool omits the field, so
	// the dify tool node artifact carries dataflow exclusively through the
	// params' {{#...#}} interpolations above.
	node := MakeToolNode(r.idFor(n), toolName, providerID, params, order, []VariableSelectorNode{})
	data := node.Data.(*ToolDataNode)
	// P3.3 §16.4: registry-driven output_schema. When a capability registry
	// is supplied the tool node declares its structured outputs (dotted
	// member paths become nested properties) so the platform analyzer
	// resolves LLM/template references to tool ports (parity with py
	// infer_tool_outputs: registry output_schema > node output_schema >
	// fallback ANY). Independent of the binding manifest — the schema comes
	// from the semantic capability, renames come from the binding.
	if r.registry != nil {
		if def, ok := r.registry.Lookup(n.ProviderID, n.Operation); ok && len(def.Outputs) > 0 {
			data.OutputSchema = toolOutputSchema(def.Outputs)
		}
	}
	if binding != nil {
		data.ToolName = binding.ToolName
		data.ProviderID = binding.ProviderID
		data.ProviderType = binding.ProviderType
		data.ToolLabel = binding.ToolLabel
		data.ProviderName = binding.ProviderName
		data.ToolDescription = binding.ToolDescription
		data.ToolNodeVersion = binding.ToolNodeVersion
		data.OutputSchema = renameOutputSchema(binding, data.OutputSchema)
	}
	if n.Exception != nil {
		data := node.Data.(*ToolDataNode)
		data.RetryConfig = retryConfigFrom(n.Exception)
		if n.Exception.FailBranch {
			strategy := "fail-branch"
			data.ErrorStrategy = &strategy
		}
	}
	return node, nil
}

// emitKnowledge mirrors knowledge-retrieval legacy.
func (r *irRenderer) emitKnowledge(n *ir.KnowledgeCall) NodeDecl {
	var query *VariableSelectorNode
	if len(n.Inputs) > 0 {
		sel := r.inputSelector(n.Inputs[0])
		if n.QueryName != "" {
			// bound-stdlib steps declare the query parameter with a name
			// and value type (legacy boundVariables full form).
			sel.Variable = n.QueryName
			sel.ValueType = r.portType(n.Inputs[0].BlockID, n.Inputs[0].Name)
		}
		query = &sel
	}
	names := n.DatasetIDs
	if len(n.DatasetNames) > 0 {
		names = n.DatasetNames
	}
	return MakeKnowledgeRetrievalNode(r.idFor(n), names, query, []VariableSelectorNode{})
}

// emitAgent shapes a validated agent(...) call into Dify's v2 Agent node,
// mirroring node_factory.py:179-227 (migrated from the legacy agent
// lowerer). The IR carries the resolved target-profile wire identity
// (strategy/provider/label/plugin + per-tool pinning) resolved at IR build
// time; the renderer only shapes the payload.
func (r *irRenderer) emitAgent(n *ir.AgentCall) NodeDecl {
	modelProvider, modelName := SplitModelName(n.Model)
	selectedTools := make([]map[string]any, 0, len(n.Tools))
	for _, tool := range n.Tools {
		selectedTools = append(selectedTools, map[string]any{
			"enabled":                  true,
			"extra":                    map[string]any{},
			"parameters":               map[string]any{},
			"plugin_unique_identifier": tool.PluginUniqueID,
			"provider_name":            tool.ProviderName,
			"tool_name":                tool.ToolName,
			"type":                     tool.ProviderType,
		})
	}
	var querySel string
	if n.Query != nil && n.Query.BlockID != "" {
		querySel = "{{#" + r.difyIDs[n.Query.BlockID] + "." + r.portFor(n.Query.BlockID, n.Query.Name) + "#}}"
	} else if n.Query != nil {
		querySel = "{{#" + n.Query.Name + "#}}"
	}
	version := "2"
	data := &AgentDataNode{
		AgentStrategyName:         n.StrategyName,
		AgentStrategyProviderName: n.StrategyProviderName,
		AgentStrategyLabel:        n.StrategyLabel,
		AgentParameters: map[string]AgentInputNode{
			"model": *NewAgentInputNode(map[string]any{
				"completion_params": map[string]any{},
				"mode":              "chat",
				"model":             modelName,
				"model_type":        "llm",
				"provider":          modelProvider,
				"type":              "model-selector",
			}, "constant"),
			"query":       *NewAgentInputNode(querySel, "constant"),
			"instruction": *NewAgentInputNode(n.Instruction, "constant"),
			"tools":       *NewAgentInputNode(selectedTools, "constant"),
		},
		PluginUniqueIdentifier: n.PluginUniqueID,
		ToolNodeVersion:        &version,
	}
	return MakeNodeDecl(r.idFor(n), "agent", data)
}

// emitSelector mirrors if-else lowering (flow_if.go + condition.go):
// `||` decomposes into one case per condition, `&&` into a single case
// with all conditions; case ids are "{ifID}_case_{i}".
func (r *irRenderer) emitSelector(n *ir.Selector) NodeDecl {
	ifID := r.idFor(n)
	var cases []CaseNode
	switch n.Logic {
	case 1: // or: one case per condition
		for i, c := range n.Conditions {
			cases = append(cases, CaseNode{
				CaseID:          fmt.Sprintf("%s_case_%d", ifID, i),
				LogicalOperator: "and",
				Conditions:      []ConditionNode{r.conditionNode(c)},
			})
		}
	default: // and: single case with all conditions
		conds := make([]ConditionNode, 0, len(n.Conditions))
		for _, c := range n.Conditions {
			conds = append(conds, r.conditionNode(c))
		}
		if len(conds) == 0 {
			conds = []ConditionNode{}
		}
		cases = append(cases, CaseNode{
			CaseID:          ifID + "_case_0",
			LogicalOperator: "and",
			Conditions:      conds,
		})
	}
	if len(cases) == 0 {
		cases = []CaseNode{}
	}
	node := MakeNodeDecl(ifID, "if-else", &IfElseDataNode{Cases: cases})
	r.selCases[ifID] = len(cases)
	return node
}

// conditionNode maps one IR selector condition onto a Dify condition,
// mirroring the legacy selector encoding (lowering/condition.go
// conditionFromExpr): string/enum selectors use is/is not, numeric and
// boolean selectors use equals/not_equals (serialized as =/≠), an empty
// string literal selects empty/not empty, and a negated selector (unary
// "!") becomes equals false.
func (r *irRenderer) conditionNode(c ir.Condition) ConditionNode {
	sel := NewVariableSelectorNode("", "")
	if c.Left.Ref != nil {
		sel = NewVariableSelectorNode(r.difyIDs[c.Left.Ref.Producer], r.portFor(c.Left.Ref.Producer, c.Left.Ref.Output))
	}
	return ConditionNode{
		VariableSelector:   sel,
		ComparisonOperator: r.selectorOperator(c),
		Value:              r.conditionValue(c),
	}
}

// conditionValue resolves the compared literal for one IR condition.
func (r *irRenderer) conditionValue(c ir.Condition) string {
	// Unary checks: isEmpty/isNotEmpty carry no right operand (empty
	// value); isTrue/isFalse carry the boolean literal.
	switch c.Operator {
	case 11:
		return "true"
	case 12:
		return "false"
	}
	return operandValue(c.Right)
}

// emitAggregate mirrors variable-aggregator.
func (r *irRenderer) emitAggregate(n *ir.Aggregate) NodeDecl {
	if len(n.Groups) > 0 {
		// Dify's grouped aggregator stores one first-not-null candidate list
		// per named output under advanced_settings.groups. Keep the legacy
		// variables field populated from the first group for compatibility
		// with the platform's older reader.
		data := NewVariableAggregatorDataNode()
		data.Variables = aggregateSelectors(r, n.Groups[0].Refs)
		if n.Groups[0].Type != nil {
			valueType := valueTypeFor(n.Groups[0].Type)
			data.OutputType = &valueType
		}
		groups := make([]map[string]any, 0, len(n.Groups))
		for i, group := range n.Groups {
			variables := make([]any, 0, len(group.Refs))
			for _, ref := range group.Refs {
				variables = append(variables, []string{r.difyIDs[ref.Producer], r.portFor(ref.Producer, ref.Output)})
			}
			groupData := map[string]any{
				"groupId":    uuid5Name(fmt.Sprintf("nclang://aggregate-group/%s/%d/%s", n.ID, i, group.Name)),
				"group_name": group.Name,
				"variables":  variables,
			}
			if group.Type != nil {
				groupData["output_type"] = valueTypeFor(group.Type)
			}
			groups = append(groups, groupData)
		}
		data.AdvancedSettings = map[string]any{"group_enabled": true, "groups": groups}
		return MakeNodeDecl(r.idFor(n), "variable-aggregator", data)
	}
	vars := make([]VariableSelectorNode, 0, len(n.Refs))
	for _, ref := range n.Refs {
		vars = append(vars, NewVariableSelectorNode(r.difyIDs[ref.Producer], r.portFor(ref.Producer, ref.Output)))
	}
	return MakeVariableAggregatorNode(r.idFor(n), vars)
}

func aggregateSelectors(r *irRenderer, refs []ir.Ref) []VariableSelectorNode {
	vars := make([]VariableSelectorNode, 0, len(refs))
	for _, ref := range refs {
		vars = append(vars, NewVariableSelectorNode(r.difyIDs[ref.Producer], r.portFor(ref.Producer, ref.Output)))
	}
	return vars
}

// emitOutput mirrors answer lowering: answer = template text.
func (r *irRenderer) emitOutput(n *ir.Output) NodeDecl {
	return MakeAnswerNode(r.idFor(n), r.rewriteTemplate(n.Template))
}

// emitWorkflowOutput renders output(expr) as a Dify output node (type: end)
// in workflow mode — the platform's "output" concept; final-result
// collection is the platform's concern (2026-08-23). A single
// {{block_output_<irID>.<port>}} ref becomes the value selector directly;
// literals/mixed templates materialize into a code node the output node
// references (extract-adapter pattern: predecessor → mat → output →
// successor).
func (r *irRenderer) emitWorkflowOutput(n *ir.Output) NodeDecl {
	tpl := strings.TrimSpace(n.Template)
	if sub := blockOutputRe.FindStringSubmatch(tpl); sub != nil && sub[0] == tpl {
		producer := sub[1]
		port := strings.TrimPrefix(sub[2], ".")
		if port == "" {
			port = "output"
		}
		return MakeOutputNode(r.nextNodeID("output"), []OutputMapping{{
			Variable:      r.allocOutput("output"),
			ValueType:     "string",
			ValueSelector: []string{r.difyIDs[producer], r.portFor(producer, port)},
		}})
	}
	code, vars := r.materializeOutputTemplate(n.Template)
	matID := r.nextNodeID("code")
	mat := MakeCodeNode(matID, code, map[string]OutputDefNode{"result": {Type: "string"}}, vars)
	if data, ok := mat.Data.(*CodeDataNode); ok {
		data.OutputOrder = []string{"result"}
	}
	setDataTitle(mat.Data, r.titleFor("code"))
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, mat)

	outID := r.nextNodeID("output") // workflow output 节点 id 前缀 = "output"(非 answer)
	out := MakeOutputNode(outID, []OutputMapping{{
		Variable:      r.allocOutput("output"),
		ValueType:     "string",
		ValueSelector: []string{matID, "result"},
	}})
	// 主节点链: 前驱 → mat(inputNodeIDs) → output(extractionEdges) → 后继
	r.difyIDs[n.NodeID()] = outID
	r.inputNodeIDs[n.NodeID()] = matID
	r.extractionEdges[n.NodeID()] = EdgeDecl{Source: matID, Target: outID, Type: "custom"}
	return out
}

// materializeOutputTemplate converts an output message template into Python
// source + input variables: {{block_output_<irID>.<port>}} refs become
// var_N parameters joined by f-string, literal text stays verbatim.
func (r *irRenderer) materializeOutputTemplate(tpl string) (string, []VariableSelectorNode) {
	var vars []VariableSelectorNode
	var parts []string
	idx := 0
	rest := tpl
	for rest != "" {
		loc := blockOutputRe.FindStringSubmatchIndex(rest)
		if loc == nil {
			parts = append(parts, JSONDumpsString(rest))
			break
		}
		if head := rest[:loc[0]]; head != "" {
			parts = append(parts, JSONDumpsString(head))
		}
		sub := blockOutputRe.FindStringSubmatch(rest)
		irID := sub[1]
		port := strings.TrimPrefix(sub[2], ".")
		if port == "" {
			port = "output"
		}
		vname := fmt.Sprintf("var_%d", idx)
		idx++
		vars = append(vars, VariableSelectorNode{
			Variable:  vname,
			ValueType: "string",
			Path:      []string{r.difyIDs[irID], r.portFor(irID, port)},
		})
		parts = append(parts, "{"+vname+"}")
		rest = rest[loc[1]:]
	}
	if len(parts) == 0 {
		parts = append(parts, `""`)
	}
	params := ""
	if len(vars) > 0 {
		names := make([]string, 0, len(vars))
		for _, v := range vars {
			names = append(names, v.Variable)
		}
		params = "(" + strings.Join(names, ", ") + ")"
	}
	expr := strings.Join(parts, " + ")
	return fmt.Sprintf("def main%s -> dict:\n    return {\"result\": %s}", params, expr), vars
}

// emitAssign mirrors variable-assigner.
func (r *irRenderer) emitAssign(n *ir.Assign) NodeDecl {
	sel := NewVariableSelectorNode("conversation", n.VarName)
	item := AssignmentItem{
		VariableSelector: sel,
		Operation:        assignerOperation(n.Operation),
		InputType:        "variable",
	}
	if v, ok := r.inputLiteral(n.Value); ok {
		item.Value = v
		item.InputType = "constant"
	} else {
		item.Value = []string{r.difyIDs[n.Value.BlockID], n.Value.Name}
	}
	return MakeAssignerV2Node(r.idFor(n), []AssignmentItem{item})
}

// assignerOperation maps the raw IR assignment operator onto the Dify
// assigner-v2 operation names (translator: "set" for plain, "append" for
// "<<", compound operators pass through).
func assignerOperation(op string) string {
	switch op {
	case "<<":
		return "append"
	case "":
		return "set"
	default:
		return op
	}
}

// emitText dispatches text ops onto template-transform / list-operator / code.
func (r *irRenderer) emitText(n *ir.TextProcess) (string, NodeDecl) {
	switch n.Method {
	case "split":
		// The legacy backend lowers split() to a code node
		// (`csv.split(",")`), not a list-operator (list_operator_lowerer
		// only handles filter/sort/sort_by/take; split falls through to the
		// generic code materializer).
		name := ""
		vars := make([]VariableSelectorNode, 0, len(n.Inputs))
		for _, in := range n.Inputs {
			sel := r.inputSelector(in)
			sel.Variable = in.Name
			sel.ValueType = "string"
			vars = append(vars, sel)
			if name == "" {
				name = in.Name
			}
		}
		if name == "" {
			name = "value"
		}
		sep := ","
		if len(n.Separators) > 0 {
			sep = n.Separators[0]
		}
		code := fmt.Sprintf("def main(%s) -> dict:\n    return {\"result\": %s.split(%s)}", name, name, JSONDumpsString(sep))
		node := MakeCodeNode(r.idFor(n), code, map[string]OutputDefNode{"result": {Type: "array[string]"}}, vars)
		if cd, ok := node.Data.(*CodeDataNode); ok {
			cd.OutputOrder = []string{"result"}
		}
		return "code", node
	default: // concat / const → template-transform, or a code node for pure literals
		if len(n.Inputs) == 0 && !strings.Contains(n.Template, "{{") {
			// Dify workflow End outputs need a concrete node selector: the
			// legacy lowering materializes literal returns into a code node
			// (translate.go makeInternalCodeNode path). The IR carries the
			// literal; synthesize the same Python source.
			code := "def main() -> dict:\n    return {\"result\": " + JSONDumpsString(n.Template) + "}"
			node := MakeCodeNode(r.idFor(n), code, map[string]OutputDefNode{"result": {Type: "string"}}, []VariableSelectorNode{})
			return "code", node
		}
		// The legacy lowering inlines every interpolation into the template
		// text as a {{#node.port#}} ref and keeps the variable list empty
		// (translate.go translateTemplateVar passes nil); ternary values
		// inline as Jinja2 chains (render.go planTernaryFolds).
		return "template-transform", MakeTemplateTransformNode(r.idFor(n), r.templateText(n), nil)
	}
}

// emitListOpNode mirrors the legacy list-operator lowering
// (list_operator_lowerer.go lowerNativeCall): one list-operator node with
// filter_by / extract_by / order_by / limit config and the collection
// source selector. The id prefix is "list_operator" (legacy
// GenerateNodeID("list_operator")), distinct from the node type.
func (r *irRenderer) emitListOpNode(n *ir.ListOp) NodeDecl {
	filterBy := ListFilterConfig{Enabled: false, Conditions: []ListFilterItem{}}
	extractBy := ListExtractConfig{Enabled: false, Serial: "1"}
	orderBy := ListOrderConfig{Enabled: false, Key: "", Value: "asc"}
	limit := ListLimitConfig{Enabled: false, Size: -1}
	switch n.Operation {
	case "filter":
		filterBy = ListFilterConfig{Enabled: true, Conditions: []ListFilterItem{{
			Key:                n.FilterKey,
			ComparisonOperator: n.FilterOp,
			Value:              n.FilterVal,
		}}}
	case "sort":
		orderBy = ListOrderConfig{Enabled: true, Key: "", Value: n.OrderVal}
	case "sort_by":
		orderBy = ListOrderConfig{Enabled: true, Key: n.OrderKey, Value: n.OrderVal}
	case "take":
		limit = ListLimitConfig{Enabled: true, Size: n.Limit}
	}
	variable := []string{}
	if len(n.Inputs) > 0 {
		in := n.Inputs[0]
		variable = []string{r.difyIDs[in.BlockID], r.portFor(in.BlockID, in.Name)}
	}
	item := n.ItemType
	if item == nil {
		item = ir.T("string")
	}
	return MakeListOperatorNode(r.idFor(n), variable, filterBy, extractBy, orderBy, limit, "array["+codeValueTypeFor(item)+"]", codeValueTypeFor(item))
}

// emitQA shapes a request_input QA node into the Dify human-input widget,
// mirroring lowering/human_input_lowerer.go (form fields, action buttons,
// timeout and the deterministic webapp delivery method).
func (r *irRenderer) emitQA(n *ir.QA) NodeDecl {
	nodeID := r.idFor(n)
	webappDeliveryMethod := map[string]any{
		"id":      HumanInputDeliveryMethodID(nodeID),
		"type":    "webapp",
		"enabled": true,
	}
	inputs := make([]HumanInputField, 0, len(n.Fields))
	for _, f := range n.Fields {
		switch f.Kind {
		case "paragraph":
			inputs = append(inputs, &ParagraphHumanInput{
				OutputVariableName: f.Name,
				Default: &StringSourceNode{
					Type:     "constant",
					Selector: []string{},
					Value:    "",
				},
				Type: "paragraph",
			})
		case "select":
			inputs = append(inputs, &SelectHumanInput{
				OutputVariableName: f.Name,
				OptionSource: StringListSourceNode{
					Type:     "constant",
					Selector: []string{},
					Value:    append([]string(nil), f.Options...),
				},
				Type: "select",
			})
		}
	}
	fieldNames := make([]string, 0, len(n.Fields))
	for _, f := range n.Fields {
		fieldNames = append(fieldNames, f.Name)
	}
	formContent := RenderFormContent(n.Template, fieldNames)
	userActions := make([]UserAction, 0, len(n.Actions))
	for _, a := range n.Actions {
		userActions = append(userActions, UserAction{
			ID:          a.Member,
			Title:       a.Title,
			ButtonStyle: a.Style,
		})
	}
	return MakeNodeDecl(nodeID, "human-input", &HumanInputDataNode{
		FormContent:     formContent,
		Inputs:          inputs,
		UserActions:     userActions,
		Timeout:         n.Timeout,
		TimeoutUnit:     n.TimeoutUnit,
		DeliveryMethods: []any{webappDeliveryMethod},
	})
}

// emitSwitch shapes an IR switch detector into the Dify question-classifier
// widget: a query_variable_selector + model + the declared classes. Each
// class is a multi-exit port (branch_i → class name); the structured
// planStructured owner emits the per-class branch edges and the aggregator
// confluence.
func (r *irRenderer) emitSwitch(n *ir.Switch) NodeDecl {
	nodeID := r.idFor(n)
	query := r.inputSelector(n.Query)
	m := NewModelConfigNode()
	classes := make([]ClassDef, 0, len(n.Intents))
	for _, name := range n.Intents {
		classes = append(classes, ClassDef{ID: name, Name: name, Label: ""})
	}
	return MakeNodeDecl(nodeID, "question-classifier", &QuestionClassifierDataNode{
		QueryVariableSelector: &query,
		Model:                 &m,
		Classes:               classes,
		Instruction:           nil,
		Memory:                nil,
		Vision:                &VisionConfig{Enabled: false, Configs: NewOrderedMap()},
	})
}

func (r *irRenderer) emitLoop(n *ir.Loop) NodeDecl {
	panic("unreachable")
}

const iterationRegionZIndex = 1002

func (r *irRenderer) emitProjectedLoop(n *flow.ProjectedLoop) error {
	switch n.LoopType {
	case "by_array":
		// A multi-node body is still a normal Dify Iteration.  The old
		// InlineName/child-count heuristic confused "inlined function owns
		// this loop" with "the loop has an early return" and routed ordinary
		// iteration bodies into the rigid exitful-for lowering.  ReturnValue is
		// the semantic discriminator established by IR construction.
		if n.ReturnValue != nil {
			return r.emitProjectedExitfulFor(n)
		}
		return r.emitProjectedIteration(n)
	case "by_iteration":
		return r.emitProjectedWhile(n)
	default:
		return &RenderError{Kind: "loop", Reason: "unsupported semantic loop mode " + n.LoopType}
	}
}

// emitProjectedIteration maps a by-array semantic loop onto Dify's Iteration
// container. The body may contain any ordinary sequence of projected nodes;
// only a semantic loop return requires the separate exitful-for lowering.
func (r *irRenderer) emitProjectedIteration(n *flow.ProjectedLoop) error {
	if n.LoopType != "by_array" || len(n.Inputs) != 1 {
		return &RenderError{Kind: "loop", Reason: "requires Dify Loop lowering"}
	}

	iterationID := r.nextNodeID("iteration")
	startID := r.nextNodeID("iteration_start")
	r.difyIDs[n.NodeID()] = iterationID
	r.iterationStarts[n.NodeID()] = startID

	start := MakeIterationStartNode(startID)
	markIterationChild(&start, iterationID)
	if getDataTitle(start.Data) == "" {
		setDataTitle(start.Data, r.titleFor("iteration-start"))
	}
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, start)

	input := n.Inputs[0]
	selector := r.inputSelector(input)
	iteration := MakeIterationNode(iterationID, selector.Path, startID)
	data := iteration.Data.(*IterationDataNode)
	data.IteratorInputType = r.portType(input.BlockID, input.Name)
	data.IsParallel = n.IsParallel
	data.ParallelNums = n.ParallelNums
	if data.ParallelNums <= 0 {
		// 作者省了 `concurrency:` ⇒ 物化平台缺省并发度（dify 的 parallel_nums 缺省 = 10；
		// serialize.go 写侧把 10 当缺省省略该键）。取值唯一处 =
		// pkg/ir/parallel_for_defaults.go。
		data.ParallelNums = ir.ParallelForDefaultConcurrency
	}
	data.ErrorHandleMode = n.ErrorHandleMode
	if getDataTitle(iteration.Data) == "" {
		setDataTitle(iteration.Data, r.titleFor("iteration"))
	}
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, iteration)

	for _, child := range n.Children {
		if isDirectIterationItem(child, n.NodeID()) {
			r.iterationFolded[n.NodeID()] = true
			continue
		}
		before := len(r.ast.Graph.Nodes)
		if err := r.emitNode(child); err != nil {
			return err
		}
		for i := before; i < len(r.ast.Graph.Nodes); i++ {
			markIterationChild(&r.ast.Graph.Nodes[i], iterationID)
		}
	}

	if r.loopOutputUsed[n.NodeID()] && len(n.OutputSources) > 0 {
		output := n.OutputSources[0]
		if r.iterationFolded[n.NodeID()] {
			data.OutputSelector = []string{iterationID, "item"}
		} else {
			data.OutputSelector = []string{r.difyIDs[output.BlockID], r.portFor(output.BlockID, output.Name)}
		}
		data.OutputType = r.iterationOutputType(n)
		flatten := false
		data.FlattenOutput = &flatten
	}
	return nil
}

func isDirectIterationItem(node ir.Node, loopID string) bool {
	text, ok := node.(*ir.TextProcess)
	if !ok || text.Method != "concat" || len(text.Inputs) != 1 {
		return false
	}
	input := text.Inputs[0]
	return input.BlockID == loopID && len(input.Path) > 0 && text.Template == "{{"+input.Path[0]+"}}"
}

func markIterationChild(node *NodeDecl, iterationID string) {
	parentID := iterationID
	zIndex := iterationRegionZIndex
	node.ParentID = &parentID
	node.ZIndex = &zIndex
	if provider, ok := node.Data.(interface{ baseData() *DataNode }); ok {
		base := provider.baseData()
		base.IterationID = iterationID
		base.IsInIteration = true
	}
}

func (r *irRenderer) emitProjectedWhile(n *flow.ProjectedLoop) error {
	// Preserve invalid loop_count values long enough for the post-render
	// verifier to report the canonical LOWERING_INVARIANT diagnostic. The
	// invalid graph is never serialized, so its body does not need the
	// legacy while-pattern expansion.
	if n.LoopCount <= 0 {
		loopID := r.nextNodeID("loop")
		startID := r.nextNodeID("loop_start")
		r.difyIDs[n.NodeID()] = loopID

		start := MakeLoopStartNode(startID)
		startData := start.Data.(*LoopStartDataNode)
		startData.Metadata.LoopID = loopID
		startData.Metadata.IsInLoop = true
		markLoopChild(&start, loopID)
		setDataTitle(start.Data, r.titleFor("loop-start"))
		r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, start)

		loop := MakeLoopNode(loopID)
		loopData := loop.Data.(*LoopDataNode)
		loopData.Metadata.StartNodeID = startID
		loopData.LoopCount = n.LoopCount
		setDataTitle(loop.Data, r.titleFor("loop"))
		r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, loop)
		r.pendingLoopEdges[n.NodeID()] = []EdgeDecl{}
		return nil
	}
	guard, increment, incrementAssign, breakNode, accumulator, err := classifyWhileBody(n)
	if err != nil {
		return err
	}
	var loopReturn *ir.LoopReturnNode
	for _, child := range n.Children {
		if candidate, ok := child.(*ir.LoopReturnNode); ok {
			loopReturn = candidate
			break
		}
	}

	loopID := r.nextNodeID("loop")
	startID := r.nextNodeID("loop_start")
	r.difyIDs[n.NodeID()] = loopID

	start := MakeLoopStartNode(startID)
	startData := start.Data.(*LoopStartDataNode)
	startData.Metadata.LoopID = loopID
	startData.Metadata.IsInLoop = true
	markLoopChild(&start, loopID)
	setDataTitle(start.Data, r.titleFor("loop-start"))
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, start)

	loop := MakeLoopNode(loopID)
	loopData := loop.Data.(*LoopDataNode)
	loopData.Metadata.StartNodeID = startID
	loopData.LoopCount = n.LoopCount
	loopData.ErrorHandleMode = n.ErrorHandleMode
	loopData.LogicalOperator = "and"
	for _, name := range sortedKeys(n.StateValues) {
		typeInfo := n.StateTypes[name]
		loopData.LoopVariables = append(loopData.LoopVariables, LoopVariable{
			Variable:  name,
			ValueType: "constant",
			Value:     loopLiteralValue(n.StateValues[name].Val, typeInfo),
			VarType:   codeValueTypeFor(typeInfo),
		})
	}
	for _, c := range guard.Conditions {
		condition := r.conditionNode(c)
		condition.ComparisonOperator = inverseLoopOperator(condition.ComparisonOperator)
		loopData.BreakConditions = append(loopData.BreakConditions, condition)
	}
	if guard.Logic == 1 && len(loopData.BreakConditions) > 1 {
		// Negating an OR guard produces an AND break condition; negating an
		// AND guard produces OR. Dify's loop logical_operator describes how
		// multiple break conditions combine.
		loopData.LogicalOperator = "and"
	} else if guard.Logic == 2 && len(loopData.BreakConditions) > 1 {
		loopData.LogicalOperator = "or"
	}
	if loopReturn != nil {
		returnType := r.portType(loopReturn.Value.BlockID, loopReturn.Value.Name)
		loopData.LoopVariables = append(loopData.LoopVariables,
			LoopVariable{Variable: "__returning", ValueType: "constant", Value: false, VarType: "boolean"},
			LoopVariable{Variable: "__return_value", ValueType: "constant", Value: defaultLoopValue(returnType), VarType: returnType},
		)
	}
	setDataTitle(loop.Data, r.titleFor("loop"))
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, loop)

	retained := map[string]bool{}
	for _, child := range n.Children {
		if child.NodeID() == guard.NodeID() || child.NodeID() == increment.NodeID() ||
			child.NodeID() == breakNode.NodeID() || child.NodeID() == accumulator.NodeID() {
			continue
		}
		if child.NodeID() == loopReturnNodeID(loopReturn) {
			returnNode := r.makeLoopReturnAssigner(loopID, loopReturn)
			r.appendLoopChild(child.NodeID(), loopID, returnNode)
			retained[child.NodeID()] = true
			continue
		}
		if child.NodeID() == incrementAssign.NodeID() {
			// The increment CodeRun + its loop-state assignment is a semantic
			// counter update. Dify represents it natively as += 1, so retain
			// the assignment slot and replace its source with the native op.
			assigner := r.makeLoopAssigner(loopID, incrementAssign, true)
			r.appendLoopChild(incrementAssign.NodeID(), loopID, assigner)
			retained[incrementAssign.NodeID()] = true
			continue
		}
		if err := r.emitLoopChild(child, loopID); err != nil {
			return err
		}
		retained[child.NodeID()] = true
	}

	r.difyIDs[increment.NodeID()] = r.difyIDs[incrementAssign.NodeID()]
	r.difyIDs[guard.NodeID()] = loopID
	r.difyIDs[breakNode.NodeID()] = loopID
	r.difyIDs[accumulator.NodeID()] = loopID
	pending := r.projectWhileBodyEdges(n, loopID, startID, retained)
	if loopReturn != nil {
		returnID := r.difyIDs[loopReturn.NodeID()]
		returnEndID := r.nextNodeID("loop_return_end")
		returnEnd := MakeLoopEndNode(returnEndID)
		setDataTitle(returnEnd.Data, r.titleFor("loop-end"))
		markLoopChild(&returnEnd, loopID)
		r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, returnEnd)
		pending = append(pending, loopRegionEdge(returnID, returnEndID, loopID, "source"))

		escapeID := r.nextNodeID("loop_return_check")
		escape := MakeNodeDecl(escapeID, "if-else", &IfElseDataNode{Cases: []CaseNode{{
			CaseID: escapeID + "_return", LogicalOperator: "and", Conditions: []ConditionNode{{
				VariableSelector: NewVariableSelectorNode(loopID, "__returning"), ComparisonOperator: "equals", Value: "true",
			}},
		}}})
		setDataTitle(escape.Data, r.titleFor("if-else"))
		r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, escape)
		r.loopEscapes[n.NodeID()] = escapeID

		answerID := r.nextNodeID("answer")
		answer := MakeAnswerNode(answerID, "{{#"+loopID+".__return_value#}}")
		setDataTitle(answer.Data, r.titleFor("answer"))
		r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, answer)
		pending = append(pending,
			EdgeDecl{Source: loopID, Target: escapeID, Type: "custom", SourceHandle: ""},
			EdgeDecl{Source: escapeID, Target: answerID, Type: "custom", SourceHandle: escapeID + "_return"},
		)
	}
	r.pendingLoopEdges[n.NodeID()] = pending
	return nil
}

func loopReturnNodeID(n *ir.LoopReturnNode) string {
	if n == nil {
		return ""
	}
	return n.NodeID()
}

// classifyWhileBody identifies the structural control nodes emitted by the
// IR builder for a while statement. The remaining children are ordinary loop
// work and are rendered unchanged. This keeps the Dify adapter independent of
// the number or kinds of work nodes in the body.
func classifyWhileBody(n *flow.ProjectedLoop) (*ir.Selector, *ir.CodeRun, *ir.Assign, *ir.BreakNode, *ir.Aggregate, error) {
	var guard *ir.Selector
	var increment *ir.CodeRun
	var incrementAssign *ir.Assign
	var breakNode *ir.BreakNode
	var accumulator *ir.Aggregate

	byID := make(map[string]ir.Node, len(n.Children))
	for _, child := range n.Children {
		byID[child.NodeID()] = child
		switch x := child.(type) {
		case *ir.BreakNode:
			if breakNode == nil {
				breakNode = x
			}
		case *ir.Aggregate:
			// The loop-tail aggregate is the one whose control-flow edge
			// returns to the loop. Other aggregates may be real body values.
			for _, edge := range n.InnerEdges {
				if edge.SourceNodeID == x.NodeID() && edge.TargetNodeID == n.NodeID() {
					accumulator = x
				}
			}
		}
	}
	if breakNode == nil {
		return nil, nil, nil, nil, nil, &RenderError{Kind: "loop", Reason: "while projection has no break control"}
	}
	if accumulator == nil {
		return nil, nil, nil, nil, nil, &RenderError{Kind: "loop", Reason: "while projection has no loop-tail aggregate"}
	}
	for _, edge := range n.InnerEdges {
		if edge.TargetNodeID == breakNode.NodeID() && edge.SourcePortID == "false" {
			if candidate, ok := byID[edge.SourceNodeID].(*ir.Selector); ok {
				guard = candidate
				break
			}
		}
	}
	if guard == nil || len(guard.Conditions) == 0 {
		return nil, nil, nil, nil, nil, &RenderError{Kind: "loop", Reason: "while projection has no guard selector"}
	}
	for _, candidate := range n.Children {
		code, ok := candidate.(*ir.CodeRun)
		if !ok {
			continue
		}
		for _, edge := range n.InnerEdges {
			if edge.SourceNodeID != code.NodeID() {
				continue
			}
			if assign, ok := byID[edge.TargetNodeID].(*ir.Assign); ok && isLoopIncrement(code, assign.VarName) {
				increment, incrementAssign = code, assign
				break
			}
		}
		if increment != nil {
			break
		}
	}
	if increment == nil || incrementAssign == nil {
		return nil, nil, nil, nil, nil, &RenderError{Kind: "loop", Reason: "while projection has no recognized counter update"}
	}
	return guard, increment, incrementAssign, breakNode, accumulator, nil
}

// emitLoopChild renders a body node without consuming the outer projection's
// incoming edges. Body edges are emitted in one structural pass after every
// child has an assigned Dify id, which is necessary when synthetic control
// nodes are removed from the platform shape.
func (r *irRenderer) emitLoopChild(child ir.Node, loopID string) error {
	saved := r.inEdges
	r.inEdges = map[string][]flow.Edge{}
	before := len(r.ast.Graph.Nodes)
	err := r.emitNode(child)
	r.inEdges = saved
	if err != nil {
		return err
	}
	for i := before; i < len(r.ast.Graph.Nodes); i++ {
		markLoopChild(&r.ast.Graph.Nodes[i], loopID)
	}
	return nil
}

func (r *irRenderer) makeLoopAssigner(loopID string, n *ir.Assign, increment bool) NodeDecl {
	item := AssignmentItem{VariableSelector: NewVariableSelectorNode(loopID, n.VarName)}
	if increment {
		item.Operation = "+="
		item.InputType = "constant"
		item.Value = int64(1)
	} else {
		item.Operation = assignerOperation(n.Operation)
		item.InputType = "variable"
		if value, ok := r.inputLiteral(n.Value); ok {
			item.InputType = "constant"
			item.Value = value
		} else if n.Value.BlockID != "" {
			item.Value = r.outputSelector(n.Value.BlockID, n.Value.Name)
		} else if len(n.Value.Path) > 0 {
			item.Value = []string{loopID, n.Value.Path[0]}
		} else {
			item.Value = []string{loopID, n.VarName}
		}
	}
	return MakeAssignerV2Node(r.nextNodeID("assigner"), []AssignmentItem{item})
}

func (r *irRenderer) makeLoopReturnAssigner(loopID string, n *ir.LoopReturnNode) NodeDecl {
	item := AssignmentItem{
		VariableSelector: NewVariableSelectorNode(loopID, "__returning"),
		Operation:        "set",
		InputType:        "constant",
		Value:            true,
	}
	value := AssignmentItem{
		VariableSelector: NewVariableSelectorNode(loopID, "__return_value"),
		Operation:        "over-write",
		InputType:        "variable",
	}
	if literal, ok := r.inputLiteral(n.Value); ok {
		value.InputType = "constant"
		value.Value = literal
	} else if n.Value.BlockID != "" {
		value.Value = r.outputSelector(n.Value.BlockID, n.Value.Name)
	} else if len(n.Value.Path) > 0 {
		value.Value = []string{loopID, n.Value.Path[0]}
	}
	return MakeAssignerV2Node(r.nextNodeID("loop_return"), []AssignmentItem{item, value})
}

func (r *irRenderer) appendLoopChild(irID, loopID string, node NodeDecl) {
	setDataTitle(node.Data, r.titleFor("assigner"))
	markLoopChild(&node, loopID)
	r.difyIDs[irID] = node.ID
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, node)
}

// projectWhileBodyEdges removes the builder-only guard/break/aggregate and
// increment-code nodes, then reconnects their retained neighbors. This is a
// structural projection: every emitted edge is derived from InnerEdges, and
// duplicate fan-in caused by the guard junction is collapsed defensively.
func (r *irRenderer) projectWhileBodyEdges(n *flow.ProjectedLoop, loopID, startID string, retained map[string]bool) []EdgeDecl {
	outgoing := map[string][]flow.Edge{}
	for _, edge := range n.InnerEdges {
		outgoing[edge.SourceNodeID] = append(outgoing[edge.SourceNodeID], edge)
	}
	var walk func(string, map[string]bool) []string
	walk = func(id string, seen map[string]bool) []string {
		if retained[id] {
			return []string{id}
		}
		if id == n.NodeID() || seen[id] {
			return nil
		}
		seen[id] = true
		var targets []string
		for _, edge := range outgoing[id] {
			targets = append(targets, walk(edge.TargetNodeID, seen)...)
		}
		return targets
	}
	type edgeKey struct{ source, target, handle string }
	seenEdges := map[edgeKey]bool{}
	var projected []EdgeDecl
	for _, edge := range n.InnerEdges {
		if edge.SourceNodeID != n.NodeID() && !retained[edge.SourceNodeID] {
			continue
		}
		source := startID
		if edge.SourceNodeID != n.NodeID() {
			source = r.difyIDs[edge.SourceNodeID]
		}
		if source == "" || edge.TargetNodeID == n.NodeID() {
			continue
		}
		targets := []string{edge.TargetNodeID}
		collapsed := !retained[edge.TargetNodeID]
		if collapsed {
			targets = walk(edge.TargetNodeID, map[string]bool{})
		}
		for _, targetIR := range targets {
			target := r.difyIDs[targetIR]
			if target == "" || target == source {
				continue
			}
			handles := []string{""}
			if source == startID {
				// Dify's loop-start has a real `source` handle. The old
				// lowering emitted it explicitly and existing imports depend
				// on that platform shape.
				handles = []string{"source"}
			}
			if !collapsed && edge.SourceNodeID != n.NodeID() {
				handles = r.edgeHandles(edge)
			}
			for _, handle := range handles {
				key := edgeKey{source, target, handle}
				if seenEdges[key] {
					continue
				}
				seenEdges[key] = true
				projected = append(projected, loopRegionEdge(source, target, loopID, handle))
			}
		}
	}
	return projected
}

func (r *irRenderer) emitProjectedExitfulFor(n *flow.ProjectedLoop) error {
	if len(n.Inputs) != 1 || len(n.Children) != 6 {
		return &RenderError{Kind: "loop", Reason: "unrecognized exitful-for projection"}
	}
	first, ok1 := n.Children[0].(*ir.Selector)
	_, ok2 := n.Children[1].(*ir.ContinueNode)
	second, ok3 := n.Children[2].(*ir.Selector)
	// M17 (46_loop_escape):loop 内 return 从 TextProcess 值物化改为
	// LoopReturnNode。两者都代表"return 值"槽位 —— 渲染统一按当前迭代
	// item（return 迭代变量的情况,46）投影 __return_value;其它表达式
	// return 的物化由编译器保证在 slot 内,值源以 slot 类型为准。
	_, ok4 := n.Children[3].(*ir.TextProcess)
	if !ok4 {
		_, ok4 = n.Children[3].(*ir.LoopReturnNode)
	}
	third, ok5 := n.Children[4].(*ir.Selector)
	_, ok6 := n.Children[5].(*ir.BreakNode)
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 {
		return &RenderError{Kind: "loop", Reason: "unrecognized exitful-for body"}
	}

	prefix := n.InlineName
	input := n.Inputs[0]
	inputName := "items"
	if len(n.InputArrays) > 0 {
		inputName = n.InputArrays[0]
	}
	inputType := r.portType(input.BlockID, input.Name)
	itemType := "string"
	if strings.HasPrefix(inputType, "array[") && strings.HasSuffix(inputType, "]") {
		itemType = strings.TrimSuffix(strings.TrimPrefix(inputType, "array["), "]")
	}

	paramID := r.nextNodeID(prefix + "_param")
	paramVar := NewVariableSelectorNode(r.difyIDs[input.BlockID], r.portFor(input.BlockID, input.Name))
	paramVar.Variable = inputName
	paramVar.ValueType = inputType
	param := MakeCodeNode(paramID,
		"def main("+inputName+") -> dict:\n    return {'"+inputName+"': "+inputName+"}",
		map[string]OutputDefNode{inputName: {Type: inputType}},
		[]VariableSelectorNode{paramVar},
	)
	setDataTitle(param.Data, r.titleFor("code"))
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, param)

	lengthID := r.nextNodeID(prefix + "_for_length")
	lengthVar := NewVariableSelectorNode(paramID, inputName)
	lengthVar.Variable = "items"
	lengthVar.ValueType = inputType
	length := MakeCodeNode(lengthID,
		"def main(items) -> dict:\n    return {\"result\": len(items)}",
		map[string]OutputDefNode{"result": {Type: "number"}},
		[]VariableSelectorNode{lengthVar},
	)
	setDataTitle(length.Data, r.titleFor("code"))
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, length)

	loopID := r.nextNodeID(prefix + "_for_loop")
	startID := r.nextNodeID(prefix + "_for_loop_start")
	r.difyIDs[n.NodeID()] = loopID
	start := MakeLoopStartNode(startID)
	startData := start.Data.(*LoopStartDataNode)
	startData.Metadata.LoopID = loopID
	startData.Metadata.IsInLoop = true
	markLoopChild(&start, loopID)
	setDataTitle(start.Data, r.titleFor("loop-start"))
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, start)

	loop := MakeLoopNode(loopID)
	loopData := loop.Data.(*LoopDataNode)
	loopData.Metadata.StartNodeID = startID
	loopData.LoopVariables = []LoopVariable{
		{Variable: "__items", ValueType: "variable", Value: []string{paramID, inputName}, VarType: inputType},
		{Variable: "__index", ValueType: "constant", Value: int64(0), VarType: "number"},
		{Variable: "__length", ValueType: "variable", Value: []string{lengthID, "result"}, VarType: "number"},
		{Variable: "__returning", ValueType: "constant", Value: false, VarType: "boolean"},
		{Variable: "__return_value", ValueType: "constant", Value: defaultLoopValue(itemType), VarType: itemType},
	}
	loopData.BreakConditions = []ConditionNode{{
		VariableSelector:   NewVariableSelectorNode(loopID, "__index"),
		ComparisonOperator: ">=", Value: "{{#" + loopID + ".__length#}}",
	}}
	setDataTitle(loop.Data, r.titleFor("loop"))
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, loop)

	itemID := r.nextNodeID(prefix + "_for_item")
	itemsVar := NewVariableSelectorNode(loopID, "__items")
	itemsVar.Variable, itemsVar.ValueType = "items", inputType
	indexVar := NewVariableSelectorNode(loopID, "__index")
	indexVar.Variable, indexVar.ValueType = "index", "number"
	item := MakeCodeNode(itemID,
		"def main(items, index) -> dict:\n    return {\"result\": items[index]}",
		map[string]OutputDefNode{"result": {Type: itemType}},
		[]VariableSelectorNode{itemsVar, indexVar},
	)
	setDataTitle(item.Data, r.titleFor("code"))
	markLoopChild(&item, loopID)
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, item)

	selectors := []*ir.Selector{first, second, third}
	selectorIDs := make([]string, 0, len(selectors))
	for _, selector := range selectors {
		selectorID := r.nextNodeID(prefix + "_if")
		selectorIDs = append(selectorIDs, selectorID)
		node := r.exitfulForSelector(selector, selectorID, itemID)
		markLoopChild(&node, loopID)
		r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, node)
		r.difyIDs[selector.NodeID()] = selectorID
	}

	incrementID := r.nextNodeID(prefix + "_for_increment")
	increment := MakeAssignerV2Node(incrementID, []AssignmentItem{{
		VariableSelector: NewVariableSelectorNode(loopID, "__index"),
		Operation:        "+=", InputType: "constant", Value: int64(1),
	}})
	setDataTitle(increment.Data, r.titleFor("assigner"))
	markLoopChild(&increment, loopID)
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, increment)

	breakEndID := r.nextNodeID(prefix + "_for_loop_end")
	breakEnd := MakeLoopEndNode(breakEndID)
	setDataTitle(breakEnd.Data, r.titleFor("loop-end"))
	markLoopChild(&breakEnd, loopID)
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, breakEnd)

	returnID := r.nextNodeID(prefix + "_for_return")
	returnNode := MakeAssignerV2Node(returnID, []AssignmentItem{
		{VariableSelector: NewVariableSelectorNode(loopID, "__returning"), Operation: "set", InputType: "constant", Value: true},
		{VariableSelector: NewVariableSelectorNode(loopID, "__return_value"), Operation: "over-write", InputType: "variable", Value: []string{itemID, "result"}},
	})
	setDataTitle(returnNode.Data, r.titleFor("assigner"))
	markLoopChild(&returnNode, loopID)
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, returnNode)

	returnEndID := r.nextNodeID(prefix + "_for_return_end")
	returnEnd := MakeLoopEndNode(returnEndID)
	setDataTitle(returnEnd.Data, r.titleFor("loop-end"))
	markLoopChild(&returnEnd, loopID)
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, returnEnd)

	escapeID := r.nextNodeID(prefix + "_for_return_check")
	escape := MakeNodeDecl(escapeID, "if-else", &IfElseDataNode{Cases: []CaseNode{{
		CaseID: escapeID + "_return", LogicalOperator: "and", Conditions: []ConditionNode{{
			VariableSelector: NewVariableSelectorNode(loopID, "__returning"), ComparisonOperator: "equals", Value: "true",
		}},
	}}})
	setDataTitle(escape.Data, r.titleFor("if-else"))
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, escape)
	r.loopEscapes[n.NodeID()] = escapeID

	returnValueID := r.nextNodeID(prefix + "_return")
	returnVarName := "for_return_" + loopID
	returnVar := NewVariableSelectorNode(loopID, "__return_value")
	returnVar.Variable, returnVar.ValueType = returnVarName, itemType
	returnValue := MakeCodeNode(returnValueID,
		"def main("+returnVarName+") -> dict:\n    return {\"result\": "+returnVarName+"}",
		map[string]OutputDefNode{"result": {Type: itemType}},
		[]VariableSelectorNode{returnVar},
	)
	setDataTitle(returnValue.Data, r.titleFor("code"))
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, returnValue)

	defaultID := r.nextNodeID(prefix + "_return")
	defaultValue := MakeCodeNode(defaultID,
		"def main() -> dict:\n    return {\"result\": 0}",
		map[string]OutputDefNode{"result": {Type: itemType}}, nil,
	)
	setDataTitle(defaultValue.Data, r.titleFor("code"))
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, defaultValue)

	mergeID := r.nextNodeID(prefix + "_return_merge")
	merge := MakeVariableAggregatorNode(mergeID, []VariableSelectorNode{
		NewVariableSelectorNode(returnValueID, "result"),
		NewVariableSelectorNode(defaultID, "result"),
	})
	mergeType := itemType
	merge.Data.(*VariableAggregatorDataNode).OutputType = &mergeType
	setDataTitle(merge.Data, r.titleFor("variable-aggregator"))
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, merge)

	defaultIR := ""
	for _, edge := range r.irEdges {
		if edge.SourceNodeID == n.NodeID() {
			if _, ok := r.index[edge.TargetNodeID].(*ir.CodeRun); ok {
				defaultIR = edge.TargetNodeID
				break
			}
		}
	}
	if defaultIR == "" {
		return &RenderError{Kind: "loop", Reason: "missing inlined default return"}
	}
	r.skipNodes[defaultIR] = true
	r.difyIDs[defaultIR] = mergeID
	r.portRedirect[defaultIR] = "output"
	r.typeRedirect[defaultIR] = itemType

	r.pendingLoopEntries[n.NodeID()] = []EdgeDecl{
		{Source: r.difyIDs[input.BlockID], Target: paramID, Type: "custom", SourceHandle: ""},
		{Source: paramID, Target: lengthID, Type: "custom", SourceHandle: ""},
		{Source: lengthID, Target: loopID, Type: "custom", SourceHandle: ""},
	}
	case0, case1, case2 := selectorIDs[0]+"_case_0", selectorIDs[1]+"_case_0", selectorIDs[2]+"_case_0"
	r.pendingLoopEdges[n.NodeID()] = []EdgeDecl{
		loopRegionEdge(startID, itemID, loopID, "source"),
		loopRegionEdge(itemID, selectorIDs[0], loopID, "source"),
		loopRegionEdge(selectorIDs[0], selectorIDs[1], loopID, "false"),
		loopRegionEdge(selectorIDs[1], selectorIDs[2], loopID, "false"),
		loopRegionEdge(selectorIDs[2], breakEndID, loopID, case2),
		loopRegionEdge(selectorIDs[2], incrementID, loopID, "false"),
		loopRegionEdge(selectorIDs[0], incrementID, loopID, case0),
		loopRegionEdge(selectorIDs[1], returnID, loopID, case1),
		loopRegionEdge(returnID, returnEndID, loopID, "source"),
		{Source: loopID, Target: escapeID, Type: "custom", SourceHandle: ""},
		{Source: escapeID, Target: returnValueID, Type: "custom", SourceHandle: escapeID + "_return"},
		{Source: escapeID, Target: defaultID, Type: "custom", SourceHandle: "false"},
		{Source: returnValueID, Target: mergeID, Type: "custom", SourceHandle: ""},
		{Source: defaultID, Target: mergeID, Type: "custom", SourceHandle: ""},
	}
	return nil
}

func (r *irRenderer) exitfulForSelector(selector *ir.Selector, selectorID, itemID string) NodeDecl {
	condition := ConditionNode{VariableSelector: NewVariableSelectorNode(itemID, "result")}
	if len(selector.Conditions) > 0 {
		condition.Value = operandValue(selector.Conditions[0].Right)
		switch selector.Conditions[0].Operator {
		case 1:
			condition.ComparisonOperator = "equals"
		case 2:
			condition.ComparisonOperator = "not_equals"
		case 13:
			condition.ComparisonOperator = ">"
		case 14:
			condition.ComparisonOperator = ">="
		case 15:
			condition.ComparisonOperator = "<"
		case 16:
			condition.ComparisonOperator = "<="
		}
	}
	node := MakeNodeDecl(selectorID, "if-else", &IfElseDataNode{Cases: []CaseNode{{
		CaseID: selectorID + "_case_0", LogicalOperator: "and", Conditions: []ConditionNode{condition},
	}}})
	setDataTitle(node.Data, r.titleFor("if-else"))
	r.selCases[selectorID] = 1
	return node
}

func isLoopIncrement(node *ir.CodeRun, name string) bool {
	want := "return {\"result\": " + name + " + 1}"
	return len(node.Inputs) == 1 && node.Inputs[0].Name == name && strings.Contains(node.Code, want)
}

func markLoopChild(node *NodeDecl, loopID string) {
	parentID := loopID
	zIndex := iterationRegionZIndex
	node.ParentID = &parentID
	node.ZIndex = &zIndex
	if provider, ok := node.Data.(interface{ baseData() *DataNode }); ok {
		base := provider.baseData()
		base.LoopID = loopID
		base.IsInLoop = true
	}
}

func loopRegionEdge(source, target, loopID, sourceHandle string) EdgeDecl {
	return EdgeDecl{
		Source: source, Target: target, Type: "custom", SourceHandle: sourceHandle,
		IsInLoop: boolPtr(true), LoopID: loopID, ZIndex: iterationRegionZIndex,
	}
}

func inverseLoopOperator(operator string) string {
	switch operator {
	case "equals":
		return "not_equals"
	case "not_equals":
		return "equals"
	case ">":
		return "<="
	case ">=":
		return "<"
	case "<":
		return ">="
	case "<=":
		return ">"
	default:
		return operator
	}
}

func loopLiteralValue(value any, typeInfo *ir.Type) any {
	text, ok := value.(string)
	if !ok || typeInfo == nil {
		return value
	}
	switch typeInfo.Kind {
	case "integer":
		var parsed int64
		if _, err := fmt.Sscan(text, &parsed); err == nil {
			return parsed
		}
	case "number":
		var parsed float64
		if _, err := fmt.Sscan(text, &parsed); err == nil {
			return parsed
		}
	}
	return value
}

func defaultLoopValue(valueType string) any {
	switch {
	case valueType == "number":
		return int64(0)
	case valueType == "boolean":
		return false
	case strings.HasPrefix(valueType, "array["):
		return []any{}
	default:
		return ""
	}
}

// ── helpers ────────────────────────────────────────────────────────

func (r *irRenderer) inputSelector(in ir.Source) VariableSelectorNode {
	if in.BlockID != "" {
		path := []string{r.difyIDs[in.BlockID]}
		path = append(path, strings.Split(r.portFor(in.BlockID, in.Name), ".")...)
		return VariableSelectorFromList(path)
	}
	return NewVariableSelectorNode("", "")
}

func (r *irRenderer) inputLiteral(in ir.Source) (any, bool) {
	if in.BlockID == "" {
		return in.Val, true
	}
	return nil, false
}

func (r *irRenderer) operandSelector(op ir.Operand) VariableSelectorNode {
	if op.Ref != nil {
		return NewVariableSelectorNode(r.difyIDs[op.Ref.Producer], op.Ref.Output)
	}
	return NewVariableSelectorNode("", "")
}

func operandValue(op ir.Operand) string {
	if op.Val == nil {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(fmt.Sprintf("%v", op.Val), "\n", " "))
}

// selectorOperator maps one IR selector condition onto the Dify comparison
// operator, mirroring the legacy type- and literal-aware encoding
// (lowering/condition.go conditionFromExpr): an empty string literal
// selects empty/not empty; string/enum selectors use is/is not; numeric
// and boolean selectors use equals/not_equals; comparison operators pass
// through; contains/not_contains map onto the legacy spellings.
func (r *irRenderer) selectorOperator(c ir.Condition) string {
	// Empty string literal (legacy: right StringLit with rawValue == "").
	if s, ok := c.Right.Val.(string); ok && s == "" {
		switch c.Operator {
		case 1:
			return "empty"
		case 2:
			return "not empty"
		}
	}
	isString := false
	if c.Left.Ref != nil {
		t := r.portType(c.Left.Ref.Producer, c.Left.Ref.Output)
		isString = t == "string" || t == "object"
	}
	switch c.Operator {
	case 1:
		if isString {
			return "is"
		}
		return "equals"
	case 2:
		if isString {
			return "is not"
		}
		return "not_equals"
	case 9:
		// Unary isEmpty — the IR folds `x == ""` here (no right operand).
		return "empty"
	case 10:
		// Unary isNotEmpty — the IR folds `x != ""` here (no right operand).
		return "not empty"
	case 11:
		// Unary isTrue — `x == true` / `x != false` / plain boolean `if (x)`.
		return "is"
	case 12:
		return "equals"
	case 13:
		return ">"
	case 14:
		return ">="
	case 15:
		return "<"
	case 16:
		return "<="
	case 7:
		return "contains"
	case 8:
		return "not contains"
	default:
		return "contains"
	}
}

// codeOutputDefFor builds a code node's output definition using the
// code-port value_type convention (int → "number").
func codeOutputDefFor(t *ir.Type) OutputDefNode {
	if t == nil {
		return OutputDefNode{Type: "string", Children: map[string]*OutputDefNode{}}
	}
	children := map[string]*OutputDefNode{}
	order := []string{}
	if t.Props != nil {
		for name, pt := range t.Props {
			child := codeOutputDefFor(pt)
			children[name] = &child
			order = append(order, name)
		}
	}
	return OutputDefNode{Type: codeValueTypeFor(t), Children: children, ChildOrder: order}
}

func outputDefFor(t *ir.Type) OutputDefNode {
	if t == nil {
		return OutputDefNode{Type: "string", Children: map[string]*OutputDefNode{}}
	}
	children := map[string]*OutputDefNode{}
	order := []string{}
	if t.Props != nil {
		for name, pt := range t.Props {
			child := outputDefFor(pt)
			children[name] = &child
			order = append(order, name)
		}
	}
	return OutputDefNode{Type: valueTypeFor(t), Children: children, ChildOrder: order}
}

func asFloat(v any) (float64, bool) {
	switch f := v.(type) {
	case float64:
		return f, true
	case float32:
		return float64(f), true
	case int:
		return float64(f), true
	case int64:
		return float64(f), true
	}
	return 0, false
}

func asInt(v any) (int, bool) {
	switch i := v.(type) {
	case int:
		return i, true
	case int64:
		return int(i), true
	case float64:
		return int(i), true
	}
	return 0, false
}

// toolOutputSchema builds a Dify tool-node output_schema from registry
// output fields. A dotted field name ("result.summary") becomes a nested
// properties path; the semantic type name maps onto Dify's type vocabulary
// (mirror of py type_system.infer_tool_outputs type_map).
func toolOutputSchema(outputs []tool.OutputField) map[string]any {
	props := map[string]any{}
	for _, out := range outputs {
		segments := strings.Split(out.Name, ".")
		node := props
		for i, seg := range segments {
			if i == len(segments)-1 {
				node[seg] = map[string]any{"type": difySchemaTypeName(out.Type)}
				continue
			}
			child, ok := node[seg].(map[string]any)
			if !ok {
				child = map[string]any{}
				node[seg] = child
			}
			sub, ok := child["properties"].(map[string]any)
			if !ok {
				sub = map[string]any{}
				child["properties"] = sub
			}
			node = sub
		}
	}
	return map[string]any{"properties": props}
}

// difySchemaTypeName maps a capability semantic type name onto the Dify
// output_schema type vocabulary (py type_map: string/number/integer/
// boolean/object/array/file; unknown → string).
func difySchemaTypeName(semantic string) string {
	switch {
	case semantic == "string":
		return "string"
	case semantic == "integer" || semantic == "int":
		return "integer"
	case semantic == "number" || semantic == "float":
		return "number"
	case semantic == "boolean" || semantic == "bool":
		return "boolean"
	case strings.HasPrefix(semantic, "array"):
		return "array"
	case semantic == "dict" || semantic == "object" || semantic == "any" || semantic == "file":
		return semantic
	}
	if tool.IsFileType(semantic) {
		// 带约束轴的注册表串（`file<image>`，见 internal/tool）：dify 的输出
		// schema 词汇表只有 file，按 file 投影（与改动前一致），不降级 string。
		return "file"
	}
	return "string"
}

// renameOutputSchema applies a ToolBinding's output renames to a Dify
// tool-node output_schema keyed by semantic result names (P3.3 §16.4).
// Unmapped keys stay unchanged; the caller owns the original map.
func renameOutputSchema(binding *ToolBinding, schema map[string]any) map[string]any {
	if binding == nil || len(binding.Outputs) == 0 || len(schema) == 0 {
		return schema
	}
	renamed := make(map[string]any, len(schema))
	for semanticName, def := range schema {
		if target, ok := binding.Outputs[semanticName]; ok {
			renamed[target] = def
		} else {
			renamed[semanticName] = def
		}
	}
	return renamed
}
