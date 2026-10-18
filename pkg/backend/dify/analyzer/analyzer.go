// analyzer.go ports src/nclang/analyzer.py (534 lines): the 8 validation
// passes and the variable-reference extraction dispatch. Pass order and the
// early return on graph-integrity errors mirror analyze() (:456-498)
// exactly; all messages are verbatim CPython-side strings (oracle-captured,
// see the task-36 report).
package analyzer

import (
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
)

// Diagnostic mirrors analyzer.py Diagnostic (:33-39).
type Diagnostic struct {
	Severity string // "error" or "warning"
	Code     string
	Message  string
	NodeID   string
	Path     string
}

// AnalysisResult mirrors analyzer.py AnalysisResult (:43-52).
type AnalysisResult struct {
	Diagnostics  []Diagnostic
	FlowGraph    *FlowGraph
	SymbolTables map[string]*SymbolTable
}

// errors ports AnalysisResult.errors (:54-56).
func (r *AnalysisResult) errors() []Diagnostic {
	var out []Diagnostic
	for _, d := range r.Diagnostics {
		if d.Severity == "error" {
			out = append(out, d)
		}
	}
	return out
}

// Ok ports AnalysisResult.ok (:60-62): no error diagnostics.
func (r *AnalysisResult) Ok() bool { return len(r.errors()) == 0 }

// ---------------------------------------------------------------------------
// variable-reference extraction (_VAR_REF_EXTRACTORS :153-175)
// ---------------------------------------------------------------------------

// varRef is one extracted variable reference: (context, ref, path).
type varRef struct {
	context string
	ref     *dify.VariableSelectorNode
	path    string
}

// extractVarList ports _extract_var_list (:145-148).
func extractVarList(nd *[]dify.VariableSelectorNode, ctxPrefix string) []varRef {
	var out []varRef
	for i := range *nd {
		v := &(*nd)[i]
		out = append(out, varRef{ctxPrefix + "_variable", v, "variables[" + itoa(i) + "]"})
	}
	return out
}

// extractLLMRefs ports _extract_llm_refs (:150-157).
func extractLLMRefs(nd *dify.LLMDataNode) []varRef {
	refs := extractVarList(&nd.Variables, "llm")
	if nd.Context != nil {
		vs := nd.Context.VariableSelector
		if len(vs) >= 2 {
			sel := dify.VariableSelectorFromList(vs)
			refs = append(refs, varRef{"llm_context", &sel, "context.variable_selector"})
		}
	}
	return refs
}

// extractIfElseRefs ports _extract_if_else_refs (:159-166).
func extractIfElseRefs(nd *dify.IfElseDataNode) []varRef {
	var refs []varRef
	for ci := range nd.Cases {
		for cj := range nd.Cases[ci].Conditions {
			vs := &nd.Cases[ci].Conditions[cj].VariableSelector
			if vs.NodeID != "" {
				refs = append(refs, varRef{"if_condition", vs, "cases[" + itoa(ci) + "].conditions[" + itoa(cj) + "].variable_selector"})
			}
		}
	}
	return refs
}

// extractLoopRefs ports _extract_loop_refs (:168-173).
func extractLoopRefs(nd *dify.LoopDataNode) []varRef {
	var refs []varRef
	for ci := range nd.BreakConditions {
		vs := &nd.BreakConditions[ci].VariableSelector
		if vs.NodeID != "" {
			refs = append(refs, varRef{"loop_break", vs, "break_conditions[" + itoa(ci) + "].variable_selector"})
		}
	}
	return refs
}

// extractAssignerRefs ports _extract_assigner_refs (:175-186).
func extractAssignerRefs(nd *dify.AssignerDataNode) []varRef {
	var refs []varRef
	for i := range nd.Items {
		item := &nd.Items[i]
		if item.VariableSelector.NodeID != "" {
			refs = append(refs, varRef{"assigner_target", &item.VariableSelector, "items[" + itoa(i) + "].variable_selector"})
		}
		if item.Operation != "clear" && item.InputType == "variable" {
			if vs, ok := item.Value.(dify.VariableSelectorNode); ok && vs.NodeID != "" {
				refs = append(refs, varRef{"assigner_source", &vs, "items[" + itoa(i) + "].value"})
			}
		}
	}
	return refs
}

// extractQuerySelector ports _extract_query_selector (:188-191).
func extractQuerySelector(nd *dify.VariableSelectorNode, ctxPrefix string) []varRef {
	if nd != nil {
		return []varRef{{ctxPrefix + "_query", nd, "query_variable_selector"}}
	}
	return nil
}

// extractDocExtractorRefs ports _extract_doc_extractor_refs (:193-197).
func extractDocExtractorRefs(nd *dify.DocumentExtractorDataNode) []varRef {
	if nd.VariableSelector != nil {
		return []varRef{{"de_variable", nd.VariableSelector, "variable_selector"}}
	}
	return nil
}

// extractSelectorRef ports _extract_selector_ref (:199-202).
func extractSelectorRef(selector []string, context, path string) []varRef {
	if len(selector) < 2 {
		return nil
	}
	sel := dify.VariableSelectorFromList(selector)
	return []varRef{{context, &sel, path}}
}

// collectVariableRefs ports _collect_variable_refs (:204-208): dispatch on
// the concrete data node type.
func collectVariableRefs(node *dify.NodeDecl) []varRef {
	switch nd := node.Data.(type) {
	case *dify.LLMDataNode:
		return extractLLMRefs(nd)
	case *dify.CodeDataNode:
		return extractVarList(&nd.Variables, "code")
	case *dify.HTTPRequestDataNode:
		return extractVarList(&nd.Variables, "http")
	case *dify.TemplateTransformDataNode:
		return extractVarList(&nd.Variables, "tt")
	case *dify.AnswerNode:
		return extractVarList(&nd.Variables, "answer")
	case *dify.ParameterExtractorDataNode:
		return extractVarList(&nd.Variables, "pe")
	case *dify.VariableAggregatorDataNode:
		return extractVarList(&nd.Variables, "va")
	case *dify.KnowledgeRetrievalDataNode:
		return extractQuerySelector(nd.QueryVariableSelector, "kr")
	case *dify.QuestionClassifierDataNode:
		return extractQuerySelector(nd.QueryVariableSelector, "qc")
	case *dify.DocumentExtractorDataNode:
		return extractDocExtractorRefs(nd)
	case *dify.IterationDataNode:
		return extractSelectorRef(nd.IteratorSelector, "iteration_input", "iterator_selector")
	case *dify.ListOperatorDataNode:
		return extractSelectorRef(nd.Variable, "list_input", "variable")
	case *dify.IfElseDataNode:
		return extractIfElseRefs(nd)
	case *dify.LoopDataNode:
		return extractLoopRefs(nd)
	case *dify.AssignerDataNode:
		return extractAssignerRefs(nd)
	}
	return nil
}

// ---------------------------------------------------------------------------
// passes
// ---------------------------------------------------------------------------

// passGraphIntegrity ports _pass_graph_integrity (analyzer.py:213-238):
// validate all edges reference existing nodes. Returns False when broken —
// the caller must stop (later passes would misbehave on a broken graph).
func passGraphIntegrity(ast *dify.WorkflowAST, nodeMap map[string]*dify.NodeDecl, result *AnalysisResult) bool {
	for i := range ast.Graph.Edges {
		edge := &ast.Graph.Edges[i]
		if _, ok := nodeMap[edge.Source]; !ok {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Severity: "error",
				Code:     "EDGE_INVALID_SOURCE",
				Message:  "Edge '" + edge.ID + "' references unknown source node '" + edge.Source + "'",
				NodeID:   edge.ID,
				Path:     "edge.source",
			})
		}
		if _, ok := nodeMap[edge.Target]; !ok {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Severity: "error",
				Code:     "EDGE_INVALID_TARGET",
				Message:  "Edge '" + edge.ID + "' references unknown target node '" + edge.Target + "'",
				NodeID:   edge.ID,
				Path:     "edge.target",
			})
		}
	}
	return len(result.errors()) == 0
}

// passEntryExit ports _pass_entry_exit (analyzer.py:240-263).
func passEntryExit(ast *dify.WorkflowAST, result *AnalysisResult) (startNodes, endNodes []*dify.NodeDecl) {
	for i := range ast.Graph.Nodes {
		n := &ast.Graph.Nodes[i]
		switch n.NodeType {
		case "start":
			startNodes = append(startNodes, n)
		case "end", "answer":
			endNodes = append(endNodes, n)
		}
	}
	if len(startNodes) == 0 {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{
			Severity: "error",
			Code:     "NO_START_NODE",
			Message:  "Workflow has no start node",
		})
	} else if len(startNodes) > 1 {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{
			Severity: "error",
			Code:     "MULTIPLE_START_NODES",
			Message:  "Workflow has " + itoa(len(startNodes)) + " start nodes (expected 1)",
		})
	}
	if len(endNodes) == 0 {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{
			Severity: "warning",
			Code:     "NO_END_NODE",
			Message:  "Workflow has no end or answer node",
		})
	}
	return startNodes, endNodes
}

// passConnectivity ports _pass_connectivity (analyzer.py:265-280).
func passConnectivity(ast *dify.WorkflowAST, fg *FlowGraph, entry string, result *AnalysisResult) {
	if entry == "" {
		return
	}
	reachable := fg.reachableFromEntry()
	changed := true
	for changed {
		changed = false
		for i := range ast.Graph.Nodes {
			node := &ast.Graph.Nodes[i]
			if node.ParentID != nil {
				if _, ok := reachable[*node.ParentID]; ok {
					if _, in := reachable[node.ID]; !in {
						reachable[node.ID] = struct{}{}
						changed = true
					}
				}
			}
		}
	}
	for i := range ast.Graph.Nodes {
		node := &ast.Graph.Nodes[i]
		if _, ok := reachable[node.ID]; !ok {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Severity: "warning",
				Code:     "UNREACHABLE_NODE",
				Message:  "Node '" + node.ID + "' (" + node.NodeType + ") is not reachable from start",
				NodeID:   node.ID,
			})
		}
	}
}

// passVariableResolution ports _pass_variable_resolution (analyzer.py:282-
// 293).
func passVariableResolution(ast *dify.WorkflowAST, tables map[string]*SymbolTable, result *AnalysisResult) {
	for i := range ast.Graph.Nodes {
		node := &ast.Graph.Nodes[i]
		refs := collectVariableRefs(node)
		table, ok := tables[node.ID]
		if !ok {
			continue
		}
		for _, ref := range refs {
			if ref.ref.NodeID == "" || ref.ref.Key == "" {
				continue
			}
			// Conversation variables are virtual, not graph nodes.
			if ref.ref.NodeID == "conversation" {
				continue
			}
			if table.resolve(ref.ref) == nil {
				result.Diagnostics = append(result.Diagnostics, Diagnostic{
					Severity: "error",
					Code:     "UNRESOLVED_REFERENCE",
					Message:  "Cannot resolve variable reference [" + ref.ref.NodeID + ", " + ref.ref.Key + "]",
					NodeID:   node.ID,
					Path:     ref.path,
				})
			}
		}
	}
}

// referenceTypeExpectations ports _REFERENCE_TYPE_EXPECTATIONS
// (analyzer.py:298-302).
var referenceTypeExpectations = map[string][]DifyType{
	"kr_query":        {TypeString},
	"qc_query":        {TypeString},
	"iteration_input": {TypeArray},
	"list_input":      {TypeArray},
}

// expectedReferenceTypes ports _expected_reference_types (analyzer.py:304-
// 315).
func expectedReferenceTypes(node *dify.NodeDecl, context string, ref *dify.VariableSelectorNode) []DifyType {
	if ref.ValueType != "" {
		declared := difyTypeFromName(ref.ValueType)
		if declared != TypeAny {
			return []DifyType{declared}
		}
	}

	if context == "de_variable" {
		if nd, ok := node.Data.(*dify.DocumentExtractorDataNode); ok {
			expected := TypeFile
			if nd.IsArrayFile {
				expected = TypeArray
			}
			return []DifyType{expected}
		}
	}

	if context == "va_variable" {
		if nd, ok := node.Data.(*dify.VariableAggregatorDataNode); ok {
			declared := TypeAny
			if nd.OutputType != nil {
				declared = difyTypeFromName(*nd.OutputType)
			}
			if declared != TypeAny {
				return []DifyType{declared}
			}
		}
	}

	return referenceTypeExpectations[context]
}

// formatExpectedTypes ports _format_expected_types (analyzer.py:317-318).
func formatExpectedTypes(expected []DifyType) string {
	parts := make([]string, len(expected))
	for i, t := range expected {
		parts[i] = string(t)
	}
	return strings.Join(parts, " or ")
}

// emitTypeMismatch ports _emit_type_mismatch (analyzer.py:320-332).
func emitTypeMismatch(result *AnalysisResult, node *dify.NodeDecl, ref *dify.VariableSelectorNode, path string, actual DifyType, expected []DifyType) {
	expectedText := formatExpectedTypes(expected)
	result.Diagnostics = append(result.Diagnostics, Diagnostic{
		Severity: "error",
		Code:     "E5001",
		Message:  "Variable reference [" + ref.NodeID + ", " + ref.Key + "] expected " + expectedText + ", got " + string(actual) + ". Hint: connect an output of type " + expectedText + " or convert the value before this node.",
		NodeID:   node.ID,
		Path:     path,
	})
}

// passTypeCompatibility ports _pass_type_compatibility (analyzer.py:334-
// 358).
func passTypeCompatibility(ast *dify.WorkflowAST, tables map[string]*SymbolTable, result *AnalysisResult) {
	for i := range ast.Graph.Nodes {
		node := &ast.Graph.Nodes[i]
		table, ok := tables[node.ID]
		if !ok {
			continue
		}

		for _, ref := range collectVariableRefs(node) {
			expected := expectedReferenceTypes(node, ref.context, ref.ref)
			if len(expected) == 0 {
				continue
			}
			symbol := table.resolve(ref.ref)
			if symbol == nil {
				continue // Pass 4 owns unresolved-reference diagnostics.
			}
			if len(ref.ref.Path) > 2 {
				continue // The coarse YAML type table does not model nested field types.
			}
			compatible := false
			for _, dtype := range expected {
				if areTypesCompatible(symbol.DType, dtype) {
					compatible = true
					break
				}
			}
			if !compatible {
				emitTypeMismatch(result, node, ref.ref, ref.path, symbol.DType, expected)
			}
		}

		if nd, ok := node.Data.(*dify.AssignerDataNode); ok {
			for index := range nd.Items {
				item := &nd.Items[index]
				if item.Operation == "clear" || item.InputType != "variable" {
					continue
				}
				vs, isVar := item.Value.(dify.VariableSelectorNode)
				if !isVar {
					continue
				}
				targetRef := &item.VariableSelector
				sourceRef := &vs
				if len(targetRef.Path) > 2 || len(sourceRef.Path) > 2 {
					continue
				}
				target := table.resolve(targetRef)
				source := table.resolve(sourceRef)
				if target == nil || source == nil {
					continue
				}
				if !areTypesCompatible(source.DType, target.DType) {
					emitTypeMismatch(result, node, sourceRef, "items["+itoa(index)+"].value", source.DType, []DifyType{target.DType})
				}
			}
		}
	}
}

// passLoopValidation ports _pass_loop_validation (analyzer.py:363-370).
func passLoopValidation(ast *dify.WorkflowAST, result *AnalysisResult) {
	for i := range ast.Graph.Nodes {
		node := &ast.Graph.Nodes[i]
		if nd, ok := node.Data.(*dify.LoopDataNode); ok && len(nd.BreakConditions) == 0 {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Severity: "warning",
				Code:     "LOOP_NO_BREAK",
				Message:  "Loop node '" + node.ID + "' has no break conditions (may loop forever)",
				NodeID:   node.ID,
			})
		}
	}
}

// passBranchCompleteness ports _pass_branch_completeness (analyzer.py:372-
// 387).
func passBranchCompleteness(ast *dify.WorkflowAST, result *AnalysisResult) {
	for i := range ast.Graph.Nodes {
		node := &ast.Graph.Nodes[i]
		nd, ok := node.Data.(*dify.IfElseDataNode)
		if !ok {
			continue
		}
		cases := nd.Cases
		if len(cases) == 0 {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Severity: "warning",
				Code:     "IFELSE_NO_CASES",
				Message:  "If-else node '" + node.ID + "' has no cases",
				NodeID:   node.ID,
			})
		}
		handles := map[string]struct{}{}
		for j := range ast.Graph.Edges {
			edge := &ast.Graph.Edges[j]
			if edge.Source == node.ID {
				handles[edge.SourceHandle] = struct{}{}
			}
		}
		_, hasTrue := handles["true"]
		_, hasFalse := handles["false"]
		if !hasTrue && !hasFalse {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Severity: "warning",
				Code:     "IFELSE_NO_BRANCHES",
				Message:  "If-else node '" + node.ID + "' has no true/false branch edges",
				NodeID:   node.ID,
			})
		}
	}
}

// passToolValidation ports _pass_tool_validation (analyzer.py:497-534).
// The dify analyzer never receives a registry: registry-based validation
// (P3.3 §16.2) runs earlier in the compile pipeline when the source imports
// capability packages via --tool-path (P3.5). The hint message text stays
// byte-identical to the frozen Python oracle (scripts/oracle/snapshots) so
// differential parity remains stable.
func passToolValidation(ast *dify.WorkflowAST, result *AnalysisResult) {
	var toolNodes []*dify.NodeDecl
	for i := range ast.Graph.Nodes {
		node := &ast.Graph.Nodes[i]
		if nd, ok := node.Data.(*dify.ToolDataNode); ok && nd.ToolName != "" {
			toolNodes = append(toolNodes, node)
		}
	}
	if len(toolNodes) > 0 {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{
			Severity: "warning",
			Code:     "TOOL_REGISTRY_HINT",
			Message:  itoa(len(toolNodes)) + " tool node(s) found but no --tools registry provided; validation skipped",
		})
	}
}

// Analyze ports analyze() (analyzer.py:456-498). Pass 1 returns early on
// graph-integrity errors; every later pass runs in the reference order.
// toolValidator is always nil (the CLI surface never supplies a registry).
func Analyze(ast *dify.WorkflowAST) *AnalysisResult {
	result := &AnalysisResult{}
	nodeMap := make(map[string]*dify.NodeDecl, len(ast.Graph.Nodes))
	for i := range ast.Graph.Nodes {
		n := &ast.Graph.Nodes[i]
		nodeMap[n.ID] = n
	}
	edgesRaw := make([][2]string, 0, len(ast.Graph.Edges))
	for i := range ast.Graph.Edges {
		edgesRaw = append(edgesRaw, [2]string{ast.Graph.Edges[i].Source, ast.Graph.Edges[i].Target})
	}

	// Pass 1: Graph integrity (early exit on broken graph).
	if !passGraphIntegrity(ast, nodeMap, result) {
		return result
	}

	// Build flow graph.
	startNodes, endNodes := passEntryExit(ast, result)
	entry := ""
	if len(startNodes) > 0 {
		entry = startNodes[0].ID
	}
	exits := make(map[string]struct{}, len(endNodes))
	for _, n := range endNodes {
		exits[n.ID] = struct{}{}
	}
	nodeIDs := make([]string, 0, len(ast.Graph.Nodes))
	for i := range ast.Graph.Nodes {
		nodeIDs = append(nodeIDs, ast.Graph.Nodes[i].ID)
	}
	fg := buildFlowGraph(nodeIDs, edgesRaw, entry, exits)
	result.FlowGraph = fg

	// Pass 3: Connectivity.
	passConnectivity(ast, fg, entry, result)

	// Build symbol tables.
	tables := buildSymbolTables(ast, fg)
	result.SymbolTables = tables

	// Pass 4: Variable resolution.
	passVariableResolution(ast, tables, result)

	// Pass 5: Type compatibility.
	passTypeCompatibility(ast, tables, result)

	// Pass 6: Loop validation.
	passLoopValidation(ast, result)

	// Pass 7: Branch completeness.
	passBranchCompleteness(ast, result)

	// Pass 8: Tool validation (opt-in).
	passToolValidation(ast, result)

	return result
}

// itoa is the strconv.Itoa alias used by the message builders.
func itoa(n int) string { return strconv.Itoa(n) }
