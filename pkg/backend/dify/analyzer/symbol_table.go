// symbol_table.go ports src/nclang/symbol_table.py (193 lines): scope-by-
// graph-reachability symbol tables and per-node output inference.
package analyzer

import (
	"github.com/nodecoda/ncoda/pkg/backend/dify"
)

// SymbolTable mirrors symbol_table.py SymbolTable (:26-33): the symbols
// available at one node, keyed "node_id.key".
//
// Memory (Task 45 P3, long-chain scaling): the reference materializes every
// node's table as the full union of ancestor outputs, which is O(n^2)
// memory on chains (5000 nodes ≈ 3GB). The Go table keeps only the node's
// own outputs in Symbols and answers ancestor lookups in O(1) through an
// ancestor bitset (ancBits): a bit set at index j means "ordered[j] is an
// ancestor of this node". Bitsets cost O(n^2/64) memory total (≈1.2KB per
// node at 10k nodes) and resolve works for arbitrary reference distance
// (a chain walk would degrade to O(distance) per lookup and O(n^2) total
// when nodes reference far ancestors, e.g. a star whose leaves all read
// the root). Symbols maps are shared with the builder context (never
// mutated after build), so retaining a table retains the shared own-output
// pool, not per-node copies.
type SymbolTable struct {
	NodeID  string
	Symbols map[string]TypedOutput // own outputs of this node (shared, read-only)
	ancBits []uint64               // ancestors: flow-graph closure ∪ parent chain
	ctx     *symbolTableCtx        // shared own-output pool + node index
}

// symbolTableCtx carries the shared state for one analyze pass: the
// own-output pool and the ordered node-ID -> index map used by ancBits.
type symbolTableCtx struct {
	ownOutputs map[string]map[string]TypedOutput
	nodeIndex  map[string]int
}

// resolve ports SymbolTable.resolve (:35-37).
func (t *SymbolTable) resolve(ref *dify.VariableSelectorNode) *TypedOutput {
	return t.lookupRef(ref.NodeID, ref.Key)
}

// resolveRaw ports SymbolTable.resolve_raw (:39-41).
func (t *SymbolTable) resolveRaw(nodeID, key string) *TypedOutput {
	return t.lookupRef(nodeID, key)
}

// lookupRef finds the "nodeID.key" output visible at this node: ancestors
// first, then the node's own outputs. The reference builds the table as
// ancestor outputs then own outputs only-if-missing (ancestors win); keys
// embed the producing node_id, so the same key can never come from two
// different nodes and the hit is unique. The ancestor test is one bit
// probe against ancBits (O(1)), then a direct hit into the shared
// own-output pool.
func (t *SymbolTable) lookupRef(nodeID, key string) *TypedOutput {
	k := nodeID + "." + key
	if t.ctx != nil && t.ancBits != nil {
		if idx, ok := t.ctx.nodeIndex[nodeID]; ok && t.ancBits[idx/64]&(1<<(uint(idx)%64)) != 0 {
			if out, ok := t.ctx.ownOutputs[nodeID][k]; ok {
				return &out
			}
		}
	}
	if out, ok := t.Symbols[k]; ok {
		return &out
	}
	return nil
}

// inferNodeOutputs ports _infer_node_outputs (symbol_table.py:49-103):
// the typed outputs a node produces, dispatched on the concrete data node.
func inferNodeOutputs(node *dify.NodeDecl) []TypedOutput {
	ntype := node.NodeType
	var result []TypedOutput

	switch nd := node.Data.(type) {
	case *dify.StartDataNode:
		return inferStartOutputs(nd.Variables)
	case *dify.CodeDataNode:
		return inferCodeOutputs(nd.Outputs)
	case *dify.ParameterExtractorDataNode:
		return inferParameterOutputs(nd.Parameters)
	case *dify.LoopDataNode:
		typeMap := map[string]DifyType{
			"string":  TypeString,
			"number":  TypeNumber,
			"integer": TypeInteger,
			"boolean": TypeBoolean,
			"object":  TypeObject,
			"array":   TypeArray,
			"file":    TypeFile,
		}
		for _, loopVar := range nd.LoopVariables {
			if loopVar.Variable == "" {
				continue
			}
			result = append(result, TypedOutput{
				Name:           loopVar.Variable,
				DType:          getWithDefault(typeMap, loopVar.VarType, TypeAny),
				SourceNode:     node.ID,
				SourceNodeType: ntype,
			})
		}
		return result
	case *dify.VariableAggregatorDataNode:
		// Dify's variable-aggregator has one native output port. The
		// platform-neutral IR may call the same value Group1/Group2, but
		// those are merge labels and never become Dify ports.
		dtype := TypeAny
		if nd.OutputType != nil {
			dtype = difyTypeFromName(*nd.OutputType)
		}
		return []TypedOutput{{
			Name: "output", DType: dtype, SourceNode: node.ID,
			SourceNodeType: ntype,
		}}
	case *dify.LLMDataNode:
		for _, pair := range nodeOutputTypes[ntype] {
			result = append(result, TypedOutput{
				Name: pair.name, DType: pair.dtype, SourceNode: node.ID, SourceNodeType: ntype,
			})
		}
		if nd.StructuredOutput != nil && nd.StructuredOutput.Enabled {
			result = append(result, TypedOutput{
				Name: "structured_output", DType: TypeObject, SourceNode: node.ID, SourceNodeType: ntype,
			})
		}
		return result
	}

	// Generic: look up from NODE_OUTPUT_TYPES.
	for _, pair := range nodeOutputTypes[ntype] {
		result = append(result, TypedOutput{
			Name:           pair.name,
			DType:          pair.dtype,
			SourceNode:     node.ID,
			SourceNodeType: ntype,
		})
	}

	// Tool nodes: outputs from output_schema (fallback to ANY). A tool
	// without a registry-driven output_schema still exposes its result
	// port as ANY (TOOL_REGISTRY_HINT reports "validation skipped"), so
	// references collected from LLM prompt bindings resolve instead of
	// failing UNRESOLVED_REFERENCE (infer_tool_outputs fallback ANY).
	if nd, ok := node.Data.(*dify.ToolDataNode); ok {
		if nd.OutputSchema != nil {
			props, _ := nd.OutputSchema["properties"].(map[string]any)
			for key := range props {
				result = append(result, TypedOutput{
					Name:           key,
					DType:          TypeAny,
					SourceNode:     node.ID,
					SourceNodeType: ntype,
				})
			}
		}
		if len(result) == 0 || !toolResultPortListed(result) {
			result = append(result, TypedOutput{
				Name:           "result",
				DType:          TypeAny,
				SourceNode:     node.ID,
				SourceNodeType: ntype,
			})
		}
	}

	return result
}

// toolResultPortListed reports whether the inferred outputs already carry
// the "result" port (schema properties may or may not name it; when the
// schema names other keys, LLM refs to the funnel port still need it).
func toolResultPortListed(outputs []TypedOutput) bool {
	for _, o := range outputs {
		if o.Name == "result" {
			return true
		}
	}
	return false
}

// buildSymbolTables ports build_symbol_tables (symbol_table.py:107-160):
// BFS-from-entry ordering (FIFO queue), orphan nodes appended afterwards,
// ancestor symbols (flow_graph.ancestors + parent_id chain) plus each
// ancestor's own outputs, then the node's own outputs (self-referencing in
// loops). Orphan ordering is a Python set-difference — any deterministic
// Go order is equivalent (orphan symbol tables only ever feed diagnostics
// with explicit node references, never ordering).
func buildSymbolTables(ast *dify.WorkflowAST, flowGraph *FlowGraph) map[string]*SymbolTable {
	nodeMap := make(map[string]*dify.NodeDecl, len(ast.Graph.Nodes))
	for i := range ast.Graph.Nodes {
		n := &ast.Graph.Nodes[i]
		nodeMap[n.ID] = n
	}
	tables := make(map[string]*SymbolTable)

	// BFS from entry for ordering (queue.pop(0) FIFO).
	visited := make(map[string]struct{})
	queue := []string{}
	if flowGraph.Entry != "" {
		queue = append(queue, flowGraph.Entry)
	}
	ordered := []string{}
	for len(queue) > 0 {
		nid := queue[0]
		queue = queue[1:]
		if _, ok := visited[nid]; ok {
			continue
		}
		visited[nid] = struct{}{}
		ordered = append(ordered, nid)
		for _, succ := range flowGraph.successors(nid) {
			if _, ok := visited[succ]; !ok {
				queue = append(queue, succ)
			}
		}
	}

	// Append orphans (nodes not visited by the BFS).
	for nid := range nodeMap {
		if _, ok := visited[nid]; !ok {
			ordered = append(ordered, nid)
		}
	}

	// Incremental transitive closure (perf): ancestors is O(n) BFS per call
	// in the reference; for the long chains real workflows produce this
	// would be O(n^2) overall. Since `ordered` is BFS order, the closure of
	// a node can be derived from its predecessors' memoized closures in
	// O(closure) total; cyclic predecessors (loop back-edges) fall back to
	// the full BFS of the reference, and the result set is identical (a
	// transitive closure is order-independent).
	ancMemo := make(map[string]map[string]struct{}, len(ordered))
	ancOf := func(nodeID string) map[string]struct{} {
		if set, ok := ancMemo[nodeID]; ok {
			return set
		}
		preds := flowGraph.predecessors(nodeID)
		complete := true
		for _, p := range preds {
			if _, ok := ancMemo[p]; !ok {
				complete = false
				break
			}
		}
		if complete {
			set := make(map[string]struct{})
			for _, p := range preds {
				set[p] = struct{}{}
				for a := range ancMemo[p] {
					set[a] = struct{}{}
				}
			}
			ancMemo[nodeID] = set
			return set
		}
		return flowGraph.ancestors(nodeID)
	}

	// Build tables.
	// Perf (Task 45 P2/P3, long chains): the reference copies every
	// ancestor's FULL symbol table into each node's table (O(n^3) time,
	// O(n^2) memory on chains — 5000 nodes ≈ 3GB). The Go build keeps each
	// node's own outputs once (ownOutputs) and gives every table an
	// ancestor bitset (flow-graph closure ∪ parent chain). Single-
	// predecessor nodes clone the predecessor's bitset and set the
	// predecessor bit (O(words) per node = O(n^2/64) total bit ops);
	// multi-predecessor/parent nodes OR their ancestors' sets. resolve
	// probes the bitset and hits the shared pool directly in O(1),
	// regardless of reference distance (a lazy chain walk would degrade to
	// O(distance) per lookup — O(n^2) total when many nodes reference a
	// far ancestor, e.g. a star whose leaves all read the root).
	ownOutputs := make(map[string]map[string]TypedOutput, len(ordered))
	for _, nodeID := range ordered {
		node, ok := nodeMap[nodeID]
		if !ok {
			continue
		}
		set := make(map[string]TypedOutput)
		for _, output := range inferNodeOutputs(node) {
			set[nodeID+"."+output.Name] = output
		}
		ownOutputs[nodeID] = set
	}
	n := len(ordered)
	words := (n + 63) / 64
	nodeIndex := make(map[string]int, n)
	for i, id := range ordered {
		nodeIndex[id] = i
	}
	ctx := &symbolTableCtx{ownOutputs: ownOutputs, nodeIndex: nodeIndex}

	// ancestorBits[i] is the bitset of ancestors of ordered[i]. Built in
	// BFS order so a single predecessor's bitset is already available.
	ancestorBits := make([][]uint64, n)
	for i, nodeID := range ordered {
		node, ok := nodeMap[nodeID]
		if !ok {
			continue
		}
		parentID := node.ParentID
		var bits []uint64
		buildGeneral := true
		if parentID == nil {
			preds := flowGraph.predecessors(nodeID)
			if len(preds) == 1 {
				p := preds[0]
				if pi, ok := nodeIndex[p]; ok && ancestorBits[pi] != nil {
					bits = make([]uint64, words)
					copy(bits, ancestorBits[pi])
					bits[pi/64] |= 1 << (uint(pi) % 64)
					buildGeneral = false
				}
			}
		}
		if buildGeneral {
			// Ancestor set: flow-graph closure (memoized) plus the parent
			// chain. The memoized set is shared between sibling tables, so
			// copy before mutating when the parent chain adds entries.
			ancestors := ancOf(nodeID)
			if parentID != nil {
				copied := make(map[string]struct{}, len(ancestors))
				for a := range ancestors {
					copied[a] = struct{}{}
				}
				ancestors = copied
			}
			for parentID != nil {
				p := *parentID
				parentNode, inMap := nodeMap[p]
				if !inMap {
					break
				}
				ancestors[p] = struct{}{}
				for a := range ancOf(p) {
					ancestors[a] = struct{}{}
				}
				parentID = parentNode.ParentID
			}
			bits = make([]uint64, words)
			for ancID := range ancestors {
				if ai, ok := nodeIndex[ancID]; ok {
					bits[ai/64] |= 1 << (uint(ai) % 64)
				}
			}
		}
		ancestorBits[i] = bits

		tables[nodeID] = &SymbolTable{
			NodeID:  nodeID,
			Symbols: ownOutputs[nodeID], // shared, read-only
			ancBits: bits,
			ctx:     ctx,
		}
	}

	return tables
}
