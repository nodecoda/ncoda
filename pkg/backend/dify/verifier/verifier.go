// Package verifier ports the post-lowering graph verifier from the
// authoritative Python reference src/nclang/lang/backends/dify/verifier.py
// (1987 lines, read-only oracle) plus the selector-inspection helpers from
// graph_selector_inspection.py. It validates structural invariants on the
// finished WorkflowAST that the lowering pass promises but that are cheaper
// to check once on the graph than to maintain incrementally.
//
// All violations are E1099 (LOWERING_INVARIANT) diagnostics with severity
// error; messages are verbatim string ports of the Python source. The rule
// order inside Verify mirrors verify() (:136-188) exactly, so diagnostics
// appear in Python order. Everything is deterministic: rules iterate slices
// only, set fields are used solely for membership checks, and sorted
// materialization is used whenever Python calls sorted() on a set.
//
// V14 (_verify_no_user_function_calls) uses a restricted Python token
// scanner instead of the full ast module — deviation declared in scan.go.
package verifier

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
	"github.com/nodecoda/ncoda/pkg/backend/dify/graphfacts"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/target/codec"
	"github.com/nodecoda/ncoda/pkg/types"
)

// VerificationResult mirrors verifier.py VerificationResult (:75-86).
type VerificationResult struct {
	Diagnostics []diag.Diagnostic
}

// OK mirrors VerificationResult.ok: no error-severity diagnostic.
func (r *VerificationResult) OK() bool {
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error {
			return false
		}
	}
	return true
}

// Errors mirrors VerificationResult.errors.
func (r *VerificationResult) Errors() []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error {
			out = append(out, d)
		}
	}
	return out
}

func invariant(message string) diag.Diagnostic {
	return diag.Diagnostic{Code: diag.LOWERING_INVARIANT, Severity: diag.Error, Message: message}
}

// ── helpers (verifier.py module-level + static) ─────────────────────

// codeSelectorOutputType mirrors _code_selector_output_type (:89-108).
func codeSelectorOutputType(data *dify.CodeDataNode, path []string) *string {
	if len(path) < 2 {
		return nil
	}
	output, ok := data.Outputs[path[1]]
	if !ok {
		return nil
	}
	result := output.Type
	children := output.Children
	for _, segment := range path[2:] {
		child, ok := children[segment]
		if !ok {
			return nil
		}
		result = child.Type
		children = child.Children
	}
	return &result
}

// portShapeToComparison mirrors _port_shape_to_comparison (:110-118):
// (value_type, children or None) with children {name: {"type": ..., "children": ...}}.
// The children slot is an untyped nil for an empty record (Python None):
// a typed-nil map would break reflect.DeepEqual against the emitted
// OutputDefNode.Children nil.
func portShapeToComparison(shape *codec.Shape) (string, any) {
	children := map[string]any{}
	for name, child := range shape.Children {
		childType, childChildren := portShapeToComparison(child)
		children[name] = map[string]any{"type": childType, "children": childChildren}
	}
	if len(children) == 0 {
		return string(shape.ValueType), nil
	}
	return string(shape.ValueType), children
}

// outputDefChildrenToComparisonMap converts an OutputDefNode children tree
// to the comparison map shape {name: {"type": ..., "children": ...}} so the
// verifier's reflect.DeepEqual contract is unchanged by the ordered-children
// representation (ChildOrder is serialization metadata, not shape).
func outputDefChildrenToComparisonMap(children map[string]*dify.OutputDefNode) map[string]any {
	m := map[string]any{}
	for name, child := range children {
		var nested any
		if len(child.Children) > 0 {
			nested = outputDefChildrenToComparisonMap(child.Children)
		}
		m[name] = map[string]any{"type": child.Type, "children": nested}
	}
	return m
}

// portShapeToChildComparison mirrors _port_shape_to_child_comparison
// (:120-123): {"type": value_type, "children": children}.
func portShapeToChildComparison(shape *codec.Shape) map[string]any {
	valueType, children := portShapeToComparison(shape)
	return map[string]any{"type": valueType, "children": children}
}

// nearestContainer mirrors _nearest_container (:724-737): walk the parent
// chain and return the nearest ancestor whose data is an Iteration (kind
// "iteration") or a Loop (kind "loop").
func nearestContainer(nodeID string, nodesByID map[string]*dify.NodeDecl, kind string) *string {
	node, ok := nodesByID[nodeID]
	if !ok {
		return nil
	}
	current := node.ParentID
	visited := map[string]bool{}
	for current != nil && *current != "" {
		if visited[*current] {
			return nil
		}
		visited[*current] = true
		ancestor, ok := nodesByID[*current]
		if !ok {
			return nil
		}
		switch kind {
		case "iteration":
			if _, isIter := ancestor.Data.(*dify.IterationDataNode); isIter {
				id := *current
				return &id
			}
		case "loop":
			if _, isLoop := ancestor.Data.(*dify.LoopDataNode); isLoop {
				id := *current
				return &id
			}
		}
		current = ancestor.ParentID
	}
	return nil
}

// extractionFieldAlias mirrors node_factory.py extraction_field_alias
// (:135-137): "field_{index}".
func extractionFieldAlias(index int) string { return "field_" + strconv.Itoa(index) }

// ── rules ───────────────────────────────────────────────────────────

// verifyNodeIDs mirrors _verify_node_ids (:562-590): V0.
func verifyNodeIDs(graph *dify.GraphNode, result *VerificationResult) {
	seen := map[string]bool{}
	for index, node := range graph.Nodes {
		if node.ID == "" {
			result.Diagnostics = append(result.Diagnostics,
				invariant("Graph node at index "+strconv.Itoa(index)+" has an empty id"))
		} else if seen[node.ID] {
			result.Diagnostics = append(result.Diagnostics,
				invariant("Graph node at index "+strconv.Itoa(index)+" has duplicate id '"+node.ID+"'"))
		} else {
			seen[node.ID] = true
		}
	}
}

// verifyEdgeEndpoints mirrors _verify_edge_endpoints (:593-617): V1/V2.
func verifyEdgeEndpoints(graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl, result *VerificationResult) {
	for _, edge := range graph.Edges {
		if edge.Source != "" {
			if _, ok := nodesByID[edge.Source]; !ok {
				result.Diagnostics = append(result.Diagnostics,
					invariant("Edge source node '"+edge.Source+"' does not exist in graph"))
			}
		}
		if edge.Target != "" {
			if _, ok := nodesByID[edge.Target]; !ok {
				result.Diagnostics = append(result.Diagnostics,
					invariant("Edge target node '"+edge.Target+"' does not exist in graph"))
			}
		}
	}
}

// verifyNoSelfLoops mirrors _verify_no_self_loops (:620-637): V1b.
func verifyNoSelfLoops(graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl, result *VerificationResult) {
	for _, edge := range graph.Edges {
		if edge.Source != "" && edge.Source == edge.Target {
			if _, ok := nodesByID[edge.Source]; ok {
				result.Diagnostics = append(result.Diagnostics,
					invariant("Edge on node '"+edge.Source+"' forms an invalid self-loop"))
			}
		}
	}
}

// verifyParentCycles mirrors _verify_parent_cycles (:691-722): returns the
// set of node ids participating in a parent chain cycle (membership only).
func verifyParentCycles(graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl, result *VerificationResult) map[string]bool {
	cyclicNodes := map[string]bool{}
	reported := map[string]bool{}
	for _, node := range graph.Nodes {
		var path []string
		offsets := map[string]int{}
		current := node.ID
		for current != "" {
			if _, ok := nodesByID[current]; !ok {
				break
			}
			if offset, seen := offsets[current]; seen {
				cycle := path[offset:]
				for _, id := range cycle {
					cyclicNodes[id] = true
				}
				if len(cycle) > 0 {
					key := strings.Join(sortedCopy(cycle), "\x00")
					if !reported[key] {
						reported[key] = true
						result.Diagnostics = append(result.Diagnostics,
							invariant("Container parent cycle detected among nodes "+formatSorted(cycle)))
					}
				}
				break
			}
			offsets[current] = len(path)
			path = append(path, current)
			parent := nodesByID[current].ParentID
			if parent == nil {
				current = ""
			} else {
				current = *parent
			}
		}
	}
	return cyclicNodes
}

// verifyNodeContainerClaim mirrors _verify_node_container_claim (:739-768).
func verifyNodeContainerClaim(node *dify.NodeDecl, kind string, expectedID *string, active bool, claimedID string, result *VerificationResult) {
	if expectedID == nil {
		if !active && claimedID == "" {
			return
		}
		result.Diagnostics = append(result.Diagnostics, invariant(
			"Node '"+node.ID+"' claims "+kind+" owner '"+claimedID+"' "+
				"but has no enclosing "+kind+" container"))
		return
	}
	if active && claimedID == *expectedID {
		return
	}
	result.Diagnostics = append(result.Diagnostics, invariant(
		"Node '"+node.ID+"' has inconsistent "+kind+" flags for nearest "+
			"container '"+*expectedID+"' (active="+pyBool(active)+", "+kind+"_id='"+claimedID+"')"))
}

// verifyContainerFlags mirrors _verify_container_flags (:640-689): V3/V11.
func verifyContainerFlags(graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl, result *VerificationResult) {
	cyclicNodes := verifyParentCycles(graph, nodesByID, result)
	for _, node := range graph.Nodes {
		if node.ID == "" {
			continue
		}
		parentID := node.ParentID
		if parentID != nil && *parentID != "" {
			if _, ok := nodesByID[*parentID]; !ok {
				result.Diagnostics = append(result.Diagnostics, invariant(
					"Node '"+node.ID+"' parent_id '"+*parentID+"' "+
						"does not reference an existing node"))
				continue // can't check flags if parent doesn't exist
			}
		}
		if cyclicNodes[node.ID] || (parentID != nil && *parentID != "" && !containsNode(nodesByID, *parentID)) {
			continue
		}
		expectedIteration := nearestContainer(node.ID, nodesByID, "iteration")
		verifyNodeContainerClaim(&node, "iteration", expectedIteration, nodeDataIsInIteration(&node), nodeDataIterationID(&node), result)
		expectedLoop := nearestContainer(node.ID, nodesByID, "loop")
		verifyNodeContainerClaim(&node, "loop", expectedLoop, nodeDataIsInLoop(&node), nodeDataLoopID(&node), result)
	}
	verifyEdgeContainerOwners(graph, nodesByID, cyclicNodes, result)
}

// containsNode reports whether id is present in nodesByID.
func containsNode(nodesByID map[string]*dify.NodeDecl, id string) bool {
	_, ok := nodesByID[id]
	return ok
}

// verifyEdgeContainerOwners mirrors _verify_edge_container_owners (:770-805).
func verifyEdgeContainerOwners(graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl, cyclicNodes map[string]bool, result *VerificationResult) {
	for _, edge := range graph.Edges {
		source, ok := nodesByID[edge.Source]
		if !ok || cyclicNodes[source.ID] {
			continue
		}
		expectedLoop := nearestContainer(source.ID, nodesByID, "loop")
		verifyEdgeContainerClaim(edge.Source, edge.Target, "loop", expectedLoop, edge.IsInLoop != nil && *edge.IsInLoop, edge.LoopID, result)
		expectedIteration := nearestContainer(source.ID, nodesByID, "iteration")
		verifyEdgeContainerClaim(edge.Source, edge.Target, "iteration", expectedIteration, edge.IsInIteration != nil && *edge.IsInIteration, edge.IterationID, result)
	}
}

// verifyEdgeContainerClaim mirrors _verify_edge_container_claim (:807-830).
func verifyEdgeContainerClaim(sourceID, targetID, kind string, expectedID *string, active bool, claimedID string, result *VerificationResult) {
	if expectedID == nil {
		if !active && claimedID == "" {
			return
		}
	} else if active && claimedID == *expectedID {
		return
	}
	claimed := claimedID
	if claimed == "" {
		claimed = "(none)"
	}
	expected := ""
	if expectedID != nil {
		expected = *expectedID
	} else {
		expected = "(none)"
	}
	result.Diagnostics = append(result.Diagnostics, invariant(
		"Edge '"+sourceID+"' -> '"+targetID+"' has nested container owner "+
			"'"+claimed+"' for "+kind+"; expected "+
			"'"+expected+"' from its source region"))
}

// verifyIterationOutputOwnership mirrors _verify_iteration_output_ownership
// (:832-871): V12.
func verifyIterationOutputOwnership(graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl, result *VerificationResult) {
	for _, iteration := range graph.Nodes {
		iterData, isIter := iteration.Data.(*dify.IterationDataNode)
		if !isIter {
			continue
		}
		if len(iterData.OutputSelector) == 0 {
			continue
		}
		producerID := iterData.OutputSelector[0]
		_, ok := nodesByID[producerID]
		if !ok {
			result.Diagnostics = append(result.Diagnostics, invariant(
				"Iteration '"+iteration.ID+"' output selector references missing "+
					"producer '"+producerID+"'"))
			continue
		}
		if producerID == iteration.ID {
			continue
		}
		ownerID := nearestContainer(producerID, nodesByID, "iteration")
		if ownerID == nil || *ownerID != iteration.ID {
			result.Diagnostics = append(result.Diagnostics, invariant(
				"Iteration '"+iteration.ID+"' output selector references producer "+
					"'"+producerID+"' outside its iteration owner"))
		}
	}
}

// verifyConditionShapes mirrors _verify_condition_shapes (:874-900): V4.
func verifyConditionShapes(graph *dify.GraphNode, result *VerificationResult) {
	for _, node := range graph.Nodes {
		for index, condition := range dify.CollectNodeConditions(&node) {
			if condition.VariableSelector.NodeID == "" || condition.VariableSelector.Key == "" {
				result.Diagnostics = append(result.Diagnostics, invariant(
					"Node '"+node.ID+"' condition "+strconv.Itoa(index)+" has an empty selector"))
			}
			if condition.ComparisonOperator == "" {
				result.Diagnostics = append(result.Diagnostics, invariant(
					"Node '"+node.ID+"' condition "+strconv.Itoa(index)+" has an empty operator"))
			}
		}
	}
}

// verifySelectorProducers mirrors _verify_selector_producers (:902-926): V4.
func verifySelectorProducers(graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl, result *VerificationResult) {
	for _, node := range graph.Nodes {
		for _, sel := range dify.CollectNodeSelectors(&node) {
			if sel.NodeID != "" {
				if _, ok := nodesByID[sel.NodeID]; !ok {
					if sel.NodeID == "conversation" {
						continue
					}
					result.Diagnostics = append(result.Diagnostics, invariant(
						"Node '"+node.ID+"' references selector "+
							"path "+fmtList(sel.Path)+" but producer node "+
							"'"+sel.NodeID+"' does not exist in graph"))
				}
			}
		}
	}
}

// verifySelectorPorts mirrors _verify_selector_ports (:928-968): V4b.
func verifySelectorPorts(graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl, result *VerificationResult) {
	for _, node := range graph.Nodes {
		for _, sel := range dify.CollectNodeSelectors(&node) {
			if sel.NodeID == "" {
				continue
			}
			producer, ok := nodesByID[sel.NodeID]
			if !ok {
				continue // already caught by V4
			}
			if codeData, isCode := producer.Data.(*dify.CodeDataNode); isCode && sel.Key != "" {
				if _, exists := codeData.Outputs[sel.Key]; !exists {
					result.Diagnostics = append(result.Diagnostics, invariant(
						"Node '"+node.ID+"' references Code node "+
							"'"+sel.NodeID+"' port '"+sel.Key+"' "+
							"but that port does not exist "+
							"(available: "+formatSortedMapKeys(codeData.Outputs)+")"))
					continue
				}
				actualType := codeSelectorOutputType(codeData, sel.Path)
				if sel.ValueType != "" && actualType != nil && sel.ValueType != *actualType {
					result.Diagnostics = append(result.Diagnostics, invariant(
						"Node '"+node.ID+"' selector "+fmtList(sel.Path)+" declares type "+
							"'"+sel.ValueType+"' but Code port type is '"+*actualType+"'"))
				}
			}
		}
	}
}

// verifyCanonicalAssignmentContract mirrors
// _verify_canonical_assignment_contract (:439-487): V16.
func verifyCanonicalAssignmentContract(graph *dify.GraphNode, result *VerificationResult) {
	for _, node := range graph.Nodes {
		if node.NodeType == "variable-assigner" {
			result.Diagnostics = append(result.Diagnostics, invariant(
				"Graph node '"+node.ID+"' uses legacy node type 'variable-assigner'"))
		}
		assignerData, isAssigner := node.Data.(*dify.AssignerDataNode)
		if node.NodeType == "assigner" && !isAssigner {
			dataName := "NoneType"
			if node.Data != nil {
				dataName = dataTypeName(node.Data)
			}
			result.Diagnostics = append(result.Diagnostics, invariant(
				"Assigner '"+node.ID+"' has noncanonical data '"+dataName+"'"))
		}
		if isAssigner && node.NodeType != "assigner" {
			result.Diagnostics = append(result.Diagnostics, invariant(
				"Graph node '"+node.ID+"' has assigner data with node type "+
					"'"+node.NodeType+"'"))
		}
		if isAssigner && len(assignerData.Items) == 0 {
			result.Diagnostics = append(result.Diagnostics, invariant(
				"Assigner '"+node.ID+"' has no operation items"))
		}
	}
	for _, edge := range graph.Edges {
		if edge.Type == "variable-assigner" {
			result.Diagnostics = append(result.Diagnostics, invariant(
				"Graph edge '"+edge.ID+"' uses legacy edge type 'variable-assigner'"))
		}
	}
}

// verifyParallelIterations mirrors _verify_parallel_iterations (:490-503).
func verifyParallelIterations(graph *dify.GraphNode, result *VerificationResult) {
	validModes := map[string]bool{"terminated": true, "continue-on-error": true, "remove-abnormal-output": true}
	for _, node := range graph.Nodes {
		iterData, isIter := node.Data.(*dify.IterationDataNode)
		if !isIter || !iterData.IsParallel {
			continue
		}
		if iterData.ParallelNums <= 0 || !validModes[iterData.ErrorHandleMode] {
			result.Diagnostics = append(result.Diagnostics, invariant(
				"Parallel Iteration '"+node.ID+"' has invalid concurrency or error mode"))
		}
	}
}

// verifyLoopContracts mirrors _verify_loop_contracts (:506-537): V15.
func verifyLoopContracts(graph *dify.GraphNode, result *VerificationResult) {
	for _, node := range graph.Nodes {
		loopData, isLoop := node.Data.(*dify.LoopDataNode)
		if !isLoop {
			continue
		}
		if loopData.LoopCount <= 0 {
			result.Diagnostics = append(result.Diagnostics, invariant(
				"Loop '"+node.ID+"' has non-positive loop_count "+strconv.Itoa(loopData.LoopCount)))
		}
		var labels []string
		for _, variable := range loopData.LoopVariables {
			labels = append(labels, variable.Variable)
		}
		if !uniqueStrings(labels) {
			result.Diagnostics = append(result.Diagnostics, invariant(
				"Loop '"+node.ID+"' has duplicate loop state labels"))
		}
		labelsSet := map[string]bool{}
		for _, label := range labels {
			labelsSet[label] = true
		}
		hasReturning := labelsSet["__returning"]
		hasReturnValue := labelsSet["__return_value"]
		if hasReturnValue && !hasReturning {
			result.Diagnostics = append(result.Diagnostics, invariant(
				"Loop '"+node.ID+"' has an incomplete function-return escape state"))
		}
	}
}

// verifyClosedCapabilityNodes mirrors _verify_closed_capability_nodes
// (:539-559): V13.
func verifyClosedCapabilityNodes(graph *dify.GraphNode, result *VerificationResult) {
	forbidden := map[string]bool{"parallel": true, "parallel-barrier": true, "parallel-product": true}
	for _, node := range graph.Nodes {
		if !forbidden[node.NodeType] {
			continue
		}
		result.Diagnostics = append(result.Diagnostics, invariant(
			"Graph node '"+node.ID+"' has unsupported synthetic parallel type "+
				"'"+node.NodeType+"'"))
	}
}

// verifyAgentNodes mirrors _verify_agent_nodes (:192-251): V17.
func verifyAgentNodes(graph *dify.GraphNode, agentProfiles map[string]*binding.AgentTargetProfile, result *VerificationResult) {
	expectedParameters := []string{"model", "query", "instruction", "tools"}
	for _, node := range graph.Nodes {
		if node.NodeType != "agent" {
			continue
		}
		agentData, isAgent := node.Data.(*dify.AgentDataNode)
		if !isAgent {
			result.Diagnostics = append(result.Diagnostics, invariant(
				"Agent node '"+node.ID+"' has invalid data payload"))
			continue
		}
		// @mode agent 声明式形态（run() ← @agent 块）不携带 target-profile
		// 身份（四个身份字段全空）:完整配置在 AgentData 本身,等价于 pinned
		// profile,跳过匹配校验。workflow 形态的 agent(...) 仍要求精确匹配。
		declarative := agentData.AgentStrategyProviderName == "" &&
			agentData.AgentStrategyName == "" &&
			agentData.AgentStrategyLabel == "" &&
			agentData.PluginUniqueIdentifier == ""
		if !declarative {
			matches := 0
			for _, candidate := range agentProfiles {
				if candidate.ProviderName == agentData.AgentStrategyProviderName &&
					candidate.StrategyName == agentData.AgentStrategyName &&
					candidate.StrategyLabel == agentData.AgentStrategyLabel &&
					candidate.PluginUniqueIdentifier == agentData.PluginUniqueIdentifier {
					matches++
				}
			}
			if matches != 1 {
				result.Diagnostics = append(result.Diagnostics, invariant(
					"Agent node '"+node.ID+"' does not match one pinned target profile"))
				continue
			}
		}
		if agentData.ToolNodeVersion == nil || *agentData.ToolNodeVersion != "2" {
			result.Diagnostics = append(result.Diagnostics, invariant(
				"Agent node '"+node.ID+"' must use tool_node_version '2'"))
		}
		paramKeys := make([]string, 0, len(agentData.AgentParameters))
		for key := range agentData.AgentParameters {
			paramKeys = append(paramKeys, key)
		}
		if len(paramKeys) != len(expectedParameters) {
			result.Diagnostics = append(result.Diagnostics, invariant(
				"Agent node '"+node.ID+"' must declare exactly "+pyList(sortedCopy(expectedParameters))))
		} else {
			seen := map[string]bool{}
			allMatch := true
			for _, key := range paramKeys {
				if !containsString(expectedParameters, key) {
					allMatch = false
				}
				seen[key] = true
			}
			if allMatch && len(seen) != len(expectedParameters) {
				allMatch = false
			}
			if !allMatch {
				result.Diagnostics = append(result.Diagnostics, invariant(
					"Agent node '"+node.ID+"' must declare exactly "+pyList(sortedCopy(expectedParameters))))
			}
		}
		if agentData.Memory != nil || agentData.OutputSchema != nil {
			result.Diagnostics = append(result.Diagnostics, invariant(
				"Agent node '"+node.ID+"' emitted unsupported memory or output schema"))
		}
	}
}

// verifyExtractionAdapters mirrors _verify_extraction_adapters (:254-437).
func verifyExtractionAdapters(graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl, facts *graphfacts.SemanticGraphFacts, codec *codec.Codec, result *VerificationResult) {
	reject := func(message string) {
		result.Diagnostics = append(result.Diagnostics, invariant(message))
	}
	provenanceAdapterIDs := map[string]bool{}
	for nodeID, provenance := range facts.CodeNodeProvenance {
		if provenance == "extraction-result" {
			provenanceAdapterIDs[nodeID] = true
		}
	}
	adapterIDs := map[string]bool{}
	for adapterID := range facts.ExtractionContracts {
		adapterIDs[adapterID] = true
	}
	provenanceOnly := differenceSortedKeys(provenanceAdapterIDs, adapterIDs)
	for _, adapterID := range provenanceOnly {
		reject("Extraction adapter '" + adapterID + "' has no immutable contract fingerprint")
	}
	producerToAdapters := map[string][]string{}
	sortedAdapterIDs := sortedKeys(adapterIDs)
	for _, adapterID := range sortedAdapterIDs {
		fingerprint := facts.ExtractionContracts[adapterID]
		producerToAdapters[fingerprint.ProducerNodeID] = append(producerToAdapters[fingerprint.ProducerNodeID], adapterID)
		adapter, ok := nodesByID[adapterID]
		if !ok {
			reject("Extraction adapter '" + adapterID + "' is not a Code node")
			continue
		}
		adapterCode, isCode := adapter.Data.(*dify.CodeDataNode)
		if !isCode {
			reject("Extraction adapter '" + adapterID + "' is not a Code node")
			continue
		}
		if !facts.CompilerGeneratedCodeNodes[adapterID] || facts.CodeNodeProvenance[adapterID] != "extraction-result" {
			reject("Extraction adapter '" + adapterID + "' has user-authored provenance")
		}

		var incoming []dify.EdgeDecl
		for _, edge := range graph.Edges {
			if edge.Target == adapterID {
				incoming = append(incoming, edge)
			}
		}
		var normal []dify.EdgeDecl
		for _, edge := range incoming {
			if (edge.SourceHandle == "" || edge.SourceHandle == "source") && edge.Type == "custom" {
				normal = append(normal, edge)
			}
		}
		if len(incoming) != 1 || len(normal) != 1 {
			reject("Extraction adapter '" + adapterID + "' requires exactly one incoming normal edge")
			continue
		}
		producer, ok := nodesByID[normal[0].Source]
		if !ok {
			reject("Extraction adapter '" + adapterID + "' is not fed by one Parameter Extractor")
			continue
		}
		if _, isExtractor := producer.Data.(*dify.ParameterExtractorDataNode); !isExtractor {
			reject("Extraction adapter '" + adapterID + "' is not fed by one Parameter Extractor")
			continue
		}
		if producer.ID != fingerprint.ProducerNodeID {
			reject("Extraction adapter '" + adapterID + "' producer does not match its immutable " +
				"contract fingerprint")
		}

		contract := adapterCode.DeclaredOutputContract
		if contract == nil {
			reject("Extraction adapter '" + adapterID + "' has no exact declared frontend contract")
			continue
		}
		// Python checks list(contract) != ["value","ok","reason","usage"] — exact
		// insertion order. Go's DeclaredOutputContract is a map, so order is
		// lost; we check the sorted key set instead. Declared deviation: a
		// hand-built graph with non-canonical insertion order is accepted here
		// but rejected by Python. Unreachable via the Go lowering (canonical
		// order is always emitted), so we document rather than add an ordered
		// map to the contract type.
		contractKeys := sortedKeysMap(contract)
		if len(contractKeys) != 4 || contractKeys[0] != "ok" || contractKeys[1] != "reason" || contractKeys[2] != "usage" || contractKeys[3] != "value" {
			reject("Extraction adapter '" + adapterID + "' has no exact declared frontend contract")
			continue
		}
		valueType := contract["value"]
		if valueType == nil || valueType.Kind != types.Record {
			reject("Extraction adapter '" + adapterID + "' value contract is not structural")
			continue
		}
		declaredFields := valueType.Fields
		var fingerprintFields []struct {
			name         string
			nodecodaType *types.Descriptor
		}
		for _, field := range fingerprint.Fields {
			fingerprintFields = append(fingerprintFields, struct {
				name         string
				nodecodaType *types.Descriptor
			}{field.Name, field.NodecodaType})
		}
		if len(declaredFields) != len(fingerprintFields) {
			reject("Extraction adapter '" + adapterID + "' declared value contract does not match " +
				"its immutable fingerprint")
		} else {
			mismatch := false
			for i := range declaredFields {
				if declaredFields[i].Name != fingerprintFields[i].name {
					mismatch = true
					break
				}
				// descriptor equality via Kind + Fields (structural)
				if !descriptorsEqual(declaredFields[i].Type, fingerprintFields[i].nodecodaType) {
					mismatch = true
					break
				}
			}
			if mismatch {
				reject("Extraction adapter '" + adapterID + "' declared value contract does not match " +
					"its immutable fingerprint")
			}
		}
		publicContract := map[string]*types.Descriptor{
			"ok":     types.BoolType(),
			"reason": types.StringType(),
			"usage":  types.MapType(types.StringType(), types.AnyType()),
		}
		publicMismatch := false
		for name, typeInfo := range publicContract {
			if !descriptorsEqual(contract[name], typeInfo) {
				publicMismatch = true
				break
			}
		}
		if publicMismatch {
			reject("Extraction adapter '" + adapterID + "' public result contract has invalid types")
		}

		extractorData := producer.Data.(*dify.ParameterExtractorDataNode)
		var actualParameters [][4]any
		for _, parameter := range extractorData.Parameters {
			options := parameter.Options
			var optTuple []string
			if options != nil {
				optTuple = *options
			}
			actualParameters = append(actualParameters, [4]any{parameter.Name, parameter.Type, parameter.Required, optTuple})
		}
		var expectedParameters [][4]any
		for _, field := range fingerprint.Fields {
			expectedParameters = append(expectedParameters, [4]any{field.Name, field.GraphonType, true, field.Options})
		}
		if !reflect.DeepEqual(actualParameters, expectedParameters) {
			reject("Parameter Extractor '" + producer.ID + "' does not preserve validated parameter " +
				"order, names, types, and ordered options")
		}

		var expectedInputs [][3]any
		expectedInputs = append(expectedInputs, [3]any{"__is_success", []string{fingerprint.ProducerNodeID, "__is_success"}, "number"})
		expectedInputs = append(expectedInputs, [3]any{"__reason", []string{fingerprint.ProducerNodeID, "__reason"}, "string"})
		expectedInputs = append(expectedInputs, [3]any{"__usage", []string{fingerprint.ProducerNodeID, "__usage"}, "object"})
		for index, field := range fingerprint.Fields {
			expectedInputs = append(expectedInputs, [3]any{
				extractionFieldAlias(index),
				[]string{fingerprint.ProducerNodeID, field.Name},
				field.GraphonType,
			})
		}
		var actualInputs [][3]any
		for _, variable := range adapterCode.Variables {
			actualInputs = append(actualInputs, [3]any{variable.Variable, append([]string(nil), variable.Path...), variable.ValueType})
		}
		if !reflect.DeepEqual(actualInputs, expectedInputs) {
			reject("Extraction adapter '" + adapterID + "' does not consume the producer's " +
				"reserved and schema ports in validated order")
		}

		expectedValueChildren := map[string]any{}
		for _, field := range fingerprint.Fields {
			expectedValueChildren[field.Name] = portShapeToChildComparison(codec.ToCodePort(field.NodecodaType))
		}
		expectedOutputs := map[string][2]any{
			"value":  {"object", expectedValueChildren},
			"ok":     {"boolean", nil},
			"reason": {"string", nil},
			"usage":  {"object", nil},
		}
		// Same declared deviation as the contract-key check above: Python
		// requires insertion order ["value","ok","reason","usage"]; Go checks
		// the sorted key set (map loses order).
		outputKeys := sortedKeysMap(adapterCode.Outputs)
		if len(outputKeys) != 4 || outputKeys[0] != "ok" || outputKeys[1] != "reason" || outputKeys[2] != "usage" || outputKeys[3] != "value" {
			reject("Extraction adapter '" + adapterID + "' does not expose exactly " +
				"value, ok, reason, usage")
			continue
		}
		actualOutputs := map[string][2]any{}
		for name, output := range adapterCode.Outputs {
			var children any
			if len(output.Children) > 0 {
				children = outputDefChildrenToComparisonMap(output.Children)
			}
			actualOutputs[name] = [2]any{output.Type, children}
		}
		if !reflect.DeepEqual(actualOutputs, expectedOutputs) {
			reject("Extraction adapter '" + adapterID + "' output types do not match the " +
				"validated frontend contract")
		}
	}

	for _, node := range graph.Nodes {
		_, isExtractor := node.Data.(*dify.ParameterExtractorDataNode)
		if !isExtractor {
			continue
		}
		adapters := producerToAdapters[node.ID]
		if len(adapters) != 1 {
			reject("Parameter Extractor '" + node.ID + "' requires exactly one normalized result adapter")
			continue
		}
		normalOutgoing := 0
		var normalTarget string
		for _, edge := range graph.Edges {
			if edge.Source == node.ID && (edge.SourceHandle == "" || edge.SourceHandle == "source") {
				normalOutgoing++
				normalTarget = edge.Target
			}
		}
		if normalOutgoing != 1 || normalTarget != adapters[0] {
			reject("Parameter Extractor '" + node.ID + "' requires exactly one normal successor, " +
				"its normalized adapter '" + adapters[0] + "'")
		}
	}
}
