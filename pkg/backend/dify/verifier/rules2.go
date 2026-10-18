package verifier

// Remaining verifier rules, ported from the authoritative Python reference
// src/nclang/lang/backends/dify/verifier.py (read-only oracle). Each rule
// mirrors the Python message strings verbatim; rule order inside Verify
// mirrors verify() (:133-190) exactly. Determinism: rules iterate slices or
// sorted materialization only; set fields are membership checks only.

import (
	"math/bits"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
	"github.com/nodecoda/ncoda/pkg/backend/dify/graphfacts"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/target/codec"
	"github.com/nodecoda/ncoda/pkg/types"
)

// edgeTriple is the Go analogue of a Python (source, target, source_handle)
// tuple used by the human-input fingerprint topology comparisons.
type edgeTriple struct{ Source, Target, Handle string }

// triple is the Go analogue of a Python (source, target, source_handle)
// tuple used by the classification fingerprint edge comparisons.
type triple [3]string

// Verify runs all base verification rules on the workflow AST, mirroring
// verifier.py verify (:133-190). Rule order follows the Python call order
// exactly so diagnostics appear in reference order. A nil graph (Python
// `ast.graph is None`) short-circuits to an empty result.
func Verify(ast *dify.WorkflowAST, facts *graphfacts.SemanticGraphFacts,
	agentProfiles map[string]*binding.AgentTargetProfile, typeCodec *codec.Codec) *VerificationResult {
	return verify(ast, facts, agentProfiles, typeCodec, true)
}

// VerifyStructure validates invariants that are independently observable on
// the rendered Dify graph. It is used by renderers that do not publish the
// legacy lowering's SemanticGraphFacts proof bundle; fact-dependent checks
// remain exclusive to Verify so missing proofs are never treated as empty
// proofs.
func VerifyStructure(ast *dify.WorkflowAST,
	agentProfiles map[string]*binding.AgentTargetProfile, typeCodec *codec.Codec) *VerificationResult {
	return verify(ast, nil, agentProfiles, typeCodec, false)
}

func verify(ast *dify.WorkflowAST, facts *graphfacts.SemanticGraphFacts,
	agentProfiles map[string]*binding.AgentTargetProfile, typeCodec *codec.Codec, verifyFacts bool) *VerificationResult {
	result := &VerificationResult{}
	if ast == nil {
		return result
	}
	graph := &ast.Graph

	if typeCodec == nil {
		typeCodec = codec.Default()
	}

	verifyNodeIDs(graph, result)
	nodesByID := make(map[string]*dify.NodeDecl, len(graph.Nodes))
	for i := range graph.Nodes {
		node := &graph.Nodes[i]
		if node.ID != "" {
			nodesByID[node.ID] = node
		}
	}

	verifyEdgeEndpoints(graph, nodesByID, result)
	verifyNoSelfLoops(graph, nodesByID, result)
	verifyCanonicalAssignmentContract(graph, result)
	verifyContainerFlags(graph, nodesByID, result)
	verifyClosedCapabilityNodes(graph, result)
	verifyAgentNodes(graph, agentProfiles, result)
	verifyIterationOutputOwnership(graph, nodesByID, result)
	verifyParallelIterations(graph, result)
	verifyLoopContracts(graph, result)
	verifyConditionShapes(graph, result)
	verifySelectorProducers(graph, nodesByID, result)
	verifySelectorPorts(graph, nodesByID, result)
	if verifyFacts {
		if facts == nil {
			facts = graphfacts.NewSemanticGraphFacts()
		}
		verifyExtractionAdapters(graph, nodesByID, facts, typeCodec, result)
		verifyParallelJoins(graph, facts, result)
		verifySelectorDominance(graph, nodesByID, facts, result)
	}
	verifyAggregatorCompleteness(graph, nodesByID, result)
	if verifyFacts {
		verifyHumanInputContracts(graph, nodesByID, facts, result)
		verifyBranchHandles(graph, nodesByID, facts, result)
		verifyConsumedExits(graph, nodesByID, facts, result)
		verifyNoUserFunctionCalls(graph, facts, result)
	}
	verifyCodeOutputContracts(graph, typeCodec, result)
	verifyCodePassthrough(graph, nodesByID, result)
	return result
}

// ── V-: parallel joins (verifier.py:1169-1194) ─────────────────────

// verifyParallelJoins mirrors _verify_parallel_joins (:1169-1194): every
// published parallel producer group must reach one common successor.
func verifyParallelJoins(graph *dify.GraphNode, facts *graphfacts.SemanticGraphFacts, result *VerificationResult) {
	successors := map[string]map[string]bool{}
	for _, edge := range graph.Edges {
		if successors[edge.Source] == nil {
			successors[edge.Source] = map[string]bool{}
		}
		successors[edge.Source][edge.Target] = true
	}
	// Python iterates a set[frozenset] (hash-random there too); sorted
	// iteration keeps Go deterministic without changing content.
	for _, groupKey := range sortedKeysMap(facts.ParallelJoinGroups) {
		group := strings.Split(groupKey, "\x00")
		allEmpty := true
		for _, producer := range group {
			if len(successors[producer]) > 0 {
				allEmpty = false
				break
			}
		}
		if allEmpty {
			continue
		}
		var common []string
		for target := range successors[group[0]] {
			present := true
			for _, producer := range group[1:] {
				if !successors[producer][target] {
					present = false
					break
				}
			}
			if present {
				common = append(common, target)
			}
		}
		if len(common) > 0 {
			continue
		}
		result.Diagnostics = append(result.Diagnostics, invariant(
			"Parallel product producers do not all reach the same continuation: "+
				pyList(group)))
	}
}

// ── V8: selector dominance (verifier.py:1055-1168) ─────────────────

// computeDominators mirrors _compute_dominators (:1195-1240).
func computeDominators(graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl) *dominatorSet {
	// Mirrors _compute_dominators (:1195-1240) with a bitset + reverse-DFS
	// worklist that converges to the same unique lattice fixed point (the
	// dominator solution) — near-linear on the long chains real workflows
	// produce, vs the literal O(n^3) map-intersection loop of the Python
	// reference (semantics identical; only the representation and the
	// propagation order differ, and the fixed point is order-independent).
	n := len(nodesByID)
	if n == 0 {
		return nil
	}
	indexOf := make(map[string]int, n)
	rev := make([]string, 0, n)
	for id := range nodesByID {
		indexOf[id] = len(rev)
		rev = append(rev, id)
	}
	words := (n + 63) / 64

	// Per-node predecessor/successor bitsets plus sparse predecessor
	// lists (deduped) for the fixed-point sweep: scanning the bitset is
	// O(n) per node even for single-predecessor graphs (chain/star); the
	// list makes single-predicate nodes O(words).
	pred := make([][]uint64, n)
	predList := make([][]int, n)
	succ := make([][]uint64, n)
	for _, edge := range graph.Edges {
		si, okS := indexOf[edge.Source]
		ti, okT := indexOf[edge.Target]
		if !okS || !okT {
			continue
		}
		if pred[ti] == nil {
			pred[ti] = make([]uint64, words)
		}
		pred[ti][si/64] |= 1 << (uint(si) % 64)
		if predList[ti] == nil || predList[ti][len(predList[ti])-1] != si {
			predList[ti] = append(predList[ti], si)
		}
		if succ[si] == nil {
			succ[si] = make([]uint64, words)
		}
		succ[si][ti/64] |= 1 << (uint(ti) % 64)
	}

	// Roots: nodes with no predecessors (fallback: the start node).
	rootBits := make([]uint64, words)
	hasRoot := false
	for i := 0; i < n; i++ {
		if pred[i] == nil {
			rootBits[i/64] |= 1 << (uint(i) % 64)
			hasRoot = true
			continue
		}
		empty := true
		for _, w := range pred[i] {
			if w != 0 {
				empty = false
				break
			}
		}
		if empty {
			rootBits[i/64] |= 1 << (uint(i) % 64)
			hasRoot = true
		}
	}
	if !hasRoot {
		for i := range graph.Nodes {
			node := &graph.Nodes[i]
			if node.NodeType == "start" && node.ID != "" {
				if idx, ok := indexOf[node.ID]; ok {
					rootBits[idx/64] |= 1 << (uint(idx) % 64)
				}
			}
		}
	}

	// DFS from the roots, recording reverse postorder (children before
	// parents) so the fixed-point pass below propagates parents first and
	// converges in a single sweep on chains/DAGs.
	var order []int
	seen := make([]bool, n)
	var visit func(bs []uint64)
	visit = func(bs []uint64) {
		for wi, w := range bs {
			for w != 0 {
				b := bits.TrailingZeros64(w)
				w &= w - 1
				idx := wi*64 + b
				if idx < n && !seen[idx] {
					seen[idx] = true
					visit(succ[idx])
					order = append(order, idx)
				}
			}
		}
	}
	visit(rootBits)
	if len(order) == 0 {
		return nil
	}

	reachable := make([]bool, n)
	reachableBits := make([]uint64, words)
	for _, i := range order {
		reachable[i] = true
		reachableBits[i/64] |= 1 << (uint(i) % 64)
	}
	isRoot := func(i int) bool { return rootBits[i/64]&(1<<(uint(i)%64)) != 0 }

	dom := make([][]uint64, n)
	for _, i := range order {
		if isRoot(i) {
			bs := make([]uint64, words)
			bs[i/64] |= 1 << (uint(i) % 64)
			dom[i] = bs
		} else {
			dom[i] = append([]uint64(nil), reachableBits...)
		}
	}

	changed := true
	for changed {
		changed = false
		for oi := len(order) - 1; oi >= 0; oi-- {
			i := order[oi]
			if isRoot(i) {
				continue
			}
			// Fast path: a single reachable predecessor dominates by
			// cloning its set plus self — O(words), no intersection
			// sweep. Chain/star (every realistic long shape) hit this.
			single := -1
			for _, pi := range predList[i] {
				if reachable[pi] {
					if single != -1 {
						single = -2 // more than one
						break
					}
					single = pi
				}
			}
			if single >= 0 {
				shared := make([]uint64, words)
				copy(shared, dom[single])
				shared[i/64] |= 1 << (uint(i) % 64)
				if !bitsEqual(shared, dom[i]) {
					dom[i] = shared
					changed = true
				}
				continue
			}
			shared := make([]uint64, words)
			first := true
			for _, pi := range predList[i] {
				if !reachable[pi] {
					continue
				}
				if first {
					copy(shared, dom[pi])
					first = false
				} else {
					for wi := range shared {
						shared[wi] &= dom[pi][wi]
					}
				}
			}
			if first {
				// No reachable predecessor: dominators = {self}.
				for wi := range shared {
					shared[wi] = 0
				}
			}
			shared[i/64] |= 1 << (uint(i) % 64)
			if !bitsEqual(shared, dom[i]) {
				dom[i] = shared
				changed = true
			}
		}
	}

	// Return the bitset directly (O(n^2/64) memory, e.g. ~0.8MB for a
	// 2500-node graph) instead of materializing map[string]map[string]bool
	// (O(n^2) map entries, ~300MB at 2500 nodes). Consumers only need
	// single dominance lookups (dominates/has), which the bitset answers in
	// O(1).
	return &dominatorSet{indexOf: indexOf, dom: dom, reachable: reachable}
}

// bitsEqual reports whether two same-length bitsets are identical.
func bitsEqual(a, b []uint64) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// dominatorSet is the bitset form of the dominator solution: dom[i] is
// the bitset of nodes dominating node i (bit j set ⇔ rev[j] dominates i),
// reachable[i] marks nodes in the DFS tree (order), and indexOf maps
// node IDs to indices. Memory is O(n^2/64) vs O(n^2) for the map form.
type dominatorSet struct {
	indexOf   map[string]int
	dom       [][]uint64
	reachable []bool
}

// has reports whether nodeID has a legal execution coverage (was reached
// by the DFS from the roots and therefore has a non-empty dominator set).
func (d *dominatorSet) has(nodeID string) bool {
	i, ok := d.indexOf[nodeID]
	if !ok {
		return false
	}
	return d.reachable[i] && d.dom[i] != nil
}

// dominates reports whether candidate dominates nodeID (nodeID is
// dominated by candidate).
func (d *dominatorSet) dominates(nodeID, candidate string) bool {
	i, ok := d.indexOf[nodeID]
	if !ok {
		return false
	}
	c, ok := d.indexOf[candidate]
	if !ok {
		return false
	}
	return d.dom[i][c/64]&(1<<(uint(c)%64)) != 0
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for key := range a {
		if !b[key] {
			return false
		}
	}
	return true
}

// verifySelectorDominance mirrors _verify_selector_dominance (:1055-1168).
func verifySelectorDominance(graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl, facts *graphfacts.SemanticGraphFacts, result *VerificationResult) {
	dominators := computeDominators(graph, nodesByID)
	if dominators == nil {
		return
	}
	directEdges := map[[2]string]bool{}
	for _, edge := range graph.Edges {
		directEdges[[2]string{edge.Source, edge.Target}] = true
	}
	successors := map[string]map[string]bool{}
	incoming := map[string]map[string]bool{}
	for _, edge := range graph.Edges {
		if successors[edge.Source] == nil {
			successors[edge.Source] = map[string]bool{}
		}
		successors[edge.Source][edge.Target] = true
		if incoming[edge.Target] == nil {
			incoming[edge.Target] = map[string]bool{}
		}
		incoming[edge.Target][edge.Source] = true
	}
	parallelJoinSuccessors := map[string]map[string]bool{}
	for _, groupKey := range sortedKeysMap(facts.ParallelJoinGroups) {
		group := strings.Split(groupKey, "\x00")
		var common map[string]bool
		for _, producer := range group {
			if common == nil {
				common = map[string]bool{}
				for target := range successors[producer] {
					common[target] = true
				}
				continue
			}
			for target := range common {
				if !successors[producer][target] {
					delete(common, target)
				}
			}
		}
		for _, producer := range group {
			if parallelJoinSuccessors[producer] == nil {
				parallelJoinSuccessors[producer] = map[string]bool{}
			}
			for target := range common {
				parallelJoinSuccessors[producer][target] = true
			}
		}
	}

	checked := map[string]bool{}
	for i := range graph.Nodes {
		consumer := &graph.Nodes[i]
		selectors := dify.CollectNodeSelectors(consumer)
		incomingSources := incoming[consumer.ID] // nil map reads are safe
		var productGroup []string
		for _, groupKey := range sortedKeysMap(facts.ParallelJoinGroups) {
			group := strings.Split(groupKey, "\x00")
			subset := true
			for _, member := range group {
				if !incomingSources[member] {
					subset = false
					break
				}
			}
			if subset {
				productGroup = group
				break
			}
		}
		for _, selector := range selectors {
			checkKey := consumer.ID + "\x00" + strings.Join(selector.Path, "\x00")
			if checked[checkKey] {
				continue
			}
			checked[checkKey] = true
			producerID := selector.NodeID
			if producerID == "" {
				continue
			}
			if _, ok := nodesByID[producerID]; !ok {
				continue
			}
			if producerID == consumer.ID {
				continue
			}
			if !dominators.has(consumer.ID) {
				result.Diagnostics = append(result.Diagnostics, invariant(
					"Selector consumer '"+consumer.ID+"' has no legal execution coverage "+
						"for producer '"+producerID+"' and path "+fmtList(selector.Path)))
				continue
			}
			_, isAggregator := consumer.Data.(*dify.VariableAggregatorDataNode)
			if isAggregator && directEdges[[2]string{producerID, consumer.ID}] {
				continue
			}
			if productGroup != nil && containsString(productGroup, producerID) &&
				directEdges[[2]string{producerID, consumer.ID}] {
				continue
			}
			producerNode, ok := nodesByID[producerID]
			if ok {
				_, producerIsAggregator := producerNode.Data.(*dify.VariableAggregatorDataNode)
				if producerIsAggregator && directEdges[[2]string{producerID, consumer.ID}] {
					continue
				}
			}
			joinPass := false
			for joinID := range parallelJoinSuccessors[producerID] {
				if joinID == consumer.ID || dominators.dominates(consumer.ID, joinID) {
					joinPass = true
					break
				}
			}
			if joinPass {
				continue
			}
			if ok {
				_, producerIsIteration := producerNode.Data.(*dify.IterationDataNode)
				if producerIsIteration {
					container := nearestContainer(consumer.ID, nodesByID, "iteration")
					if container != nil && *container == producerID {
						continue
					}
				}
				_, producerIsLoop := producerNode.Data.(*dify.LoopDataNode)
				if producerIsLoop {
					container := nearestContainer(consumer.ID, nodesByID, "loop")
					if container != nil && *container == producerID {
						continue
					}
				}
			}
			containerID := consumer.ParentID
			for containerID != nil && *containerID != "" {
				if _, exists := nodesByID[*containerID]; !exists {
					break
				}
				if dominators.dominates(*containerID, producerID) {
					break
				}
				containerID = nodesByID[*containerID].ParentID
			}
			if containerID != nil && *containerID != "" {
				continue
			}
			if !dominators.dominates(consumer.ID, producerID) {
				result.Diagnostics = append(result.Diagnostics, invariant(
					"Selector producer '"+producerID+"' does not dominate consumer "+
						"'"+consumer.ID+"' for path "+fmtList(selector.Path)+
						"; add an explicit legal merge"))
			}
		}
	}
}

// ── V9/V10: consumed exits (verifier.py:1241-1408) ─────────────────

// verifyConsumedExits mirrors _verify_consumed_exits (:1241-1273).
func verifyConsumedExits(graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl, facts *graphfacts.SemanticGraphFacts, result *VerificationResult) {
	for _, proof := range facts.ConsumedExits {
		if proof.Kind == binding.ExitReturn {
			if !facts.FunctionScopes[proof.TargetScopeID] {
				exitError(proof, "RETURN targets undeclared function scope "+scopeValue(proof.TargetScopeID), result)
			}
			if proof.OwnerContainerID != nil {
				exitError(proof, "RETURN cannot be consumed by a graph container", result)
			}
		} else {
			verifyLoopExitOwner(proof, graph, nodesByID, facts, result)
		}
		if proof.ConsumerNodeID != nil {
			verifyExitConsumerEdge(proof, graph, nodesByID, result)
		}
		if proof.Terminal {
			verifyTerminalReturn(proof, graph, result)
		}
	}
}

func scopeValue(scope binding.ScopeID) string {
	return strconv.Itoa(int(scope))
}

// verifyLoopExitOwner mirrors _verify_loop_exit_owner (:1274-1316).
func verifyLoopExitOwner(proof graphfacts.ConsumedExitProof, graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl, facts *graphfacts.SemanticGraphFacts, result *VerificationResult) {
	ownerID := proof.OwnerContainerID
	var ownerScope binding.ScopeID
	var hasOwnerScope bool
	if ownerID != nil {
		ownerScope, hasOwnerScope = facts.ContainerScopes[*ownerID]
	}
	if !hasOwnerScope || ownerScope != proof.TargetScopeID {
		ownerScopeText := "(none)"
		if hasOwnerScope {
			ownerScopeText = scopeValue(ownerScope)
		}
		exitError(proof, proof.Kind.String()+" targets scope "+scopeValue(proof.TargetScopeID)+
			" but container '"+derefStr(ownerID)+"' owns "+ownerScopeText, result)
		return
	}
	owner, ok := nodesByID[derefStr(ownerID)]
	if !ok {
		exitError(proof, "exit owner '"+derefStr(ownerID)+"' is not a Loop container", result)
		return
	}
	if _, isLoop := owner.Data.(*dify.LoopDataNode); !isLoop {
		exitError(proof, "exit owner '"+derefStr(ownerID)+"' is not a Loop container", result)
		return
	}
	var consumer *dify.NodeDecl
	if proof.ConsumerNodeID != nil {
		consumer = nodesByID[*proof.ConsumerNodeID]
	}
	expectedType := "loop-end"
	if proof.Kind != binding.ExitBreak {
		expectedType = "loop-continue"
	}
	validConsumer := consumer != nil && consumer.NodeType == expectedType &&
		parentIDEquals(consumer.ParentID, derefStr(ownerID))
	if proof.Kind == binding.ExitContinue && consumer != nil {
		validConsumer = isLoopContinueConsumer(consumer, derefStr(ownerID), graph) ||
			isForIncrementConsumer(consumer, derefStr(ownerID), graph)
		expectedType = "verified loop continue sink or for increment"
	}
	if !validConsumer {
		exitError(proof, proof.Kind.String()+" consumer '"+derefStr(proof.ConsumerNodeID)+
			"' is not a child "+expectedType+" of loop '"+derefStr(ownerID)+"'", result)
	}
}

func parentIDEquals(parent *string, id string) bool {
	return parent != nil && *parent == id
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// isLoopContinueConsumer mirrors _is_loop_continue_consumer (:1318-1329).
func isLoopContinueConsumer(consumer *dify.NodeDecl, ownerID string, graph *dify.GraphNode) bool {
	_, isCode := consumer.Data.(*dify.CodeDataNode)
	if !isCode || !parentIDEquals(consumer.ParentID, ownerID) {
		return false
	}
	for _, edge := range graph.Edges {
		if edge.Source == consumer.ID {
			return false
		}
	}
	return true
}

// isForIncrementConsumer mirrors _is_for_increment_consumer (:1330-1348).
func isForIncrementConsumer(consumer *dify.NodeDecl, ownerID string, graph *dify.GraphNode) bool {
	assigner, isAssigner := consumer.Data.(*dify.AssignerDataNode)
	if !isAssigner || !parentIDEquals(consumer.ParentID, ownerID) {
		return false
	}
	incrementsOwnerIndex := false
	for _, item := range assigner.Items {
		path := item.VariableSelector.Path
		if len(path) == 2 && path[0] == ownerID && path[1] == "__index" &&
			item.Operation == "+=" && intValueEquals(item.Value, 1) && item.InputType == "constant" {
			incrementsOwnerIndex = true
			break
		}
	}
	returnsToOwner := false
	for _, edge := range graph.Edges {
		if edge.Source == consumer.ID && edge.Target == ownerID {
			returnsToOwner = true
			break
		}
	}
	return incrementsOwnerIndex && !returnsToOwner
}

// verifyExitConsumerEdge mirrors _verify_exit_consumer_edge (:1350-1372).
func verifyExitConsumerEdge(proof graphfacts.ConsumedExitProof, graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl, result *VerificationResult) {
	if _, ok := nodesByID[derefStr(proof.TailNodeID)]; !ok {
		exitError(proof, "consumed exit references a missing graph witness", result)
		return
	}
	if _, ok := nodesByID[derefStr(proof.ConsumerNodeID)]; !ok {
		exitError(proof, "consumed exit references a missing graph witness", result)
		return
	}
	matching := 0
	for _, edge := range graph.Edges {
		if edge.Source == derefStr(proof.TailNodeID) && edge.Target == derefStr(proof.ConsumerNodeID) &&
			(proof.TailSourceHandle == "" || edge.SourceHandle == proof.TailSourceHandle) {
			matching++
		}
	}
	if matching != 1 {
		exitError(proof, "consumed exit must have exactly one edge from its tail to its consumer", result)
	}
}

// verifyTerminalReturn mirrors _verify_terminal_return (:1374-1391).
func verifyTerminalReturn(proof graphfacts.ConsumedExitProof, graph *dify.GraphNode, result *VerificationResult) {
	outgoing := 0
	for _, edge := range graph.Edges {
		if edge.Source != derefStr(proof.TailNodeID) {
			continue
		}
		if proof.TailSourceHandle != "" && edge.SourceHandle != proof.TailSourceHandle {
			continue
		}
		outgoing++
	}
	if outgoing > 0 {
		exitError(proof, "terminal return tail '"+derefStr(proof.TailNodeID)+"' has a successor", result)
	}
}

// exitError mirrors _exit_error (:1393-1408): the "Consumed {kind} exit:"
// prefix is applied to every consumed-exit diagnostic.
func exitError(proof graphfacts.ConsumedExitProof, message string, result *VerificationResult) {
	result.Diagnostics = append(result.Diagnostics, invariant(
		"Consumed "+proof.Kind.String()+" exit: "+message))
}

// ── V5: aggregator completeness (verifier.py:1409-1431) ────────────

// verifyAggregatorCompleteness mirrors _verify_aggregator_completeness.
func verifyAggregatorCompleteness(graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl, result *VerificationResult) {
	for i := range graph.Nodes {
		node := &graph.Nodes[i]
		if _, isAggregator := node.Data.(*dify.VariableAggregatorDataNode); !isAggregator {
			continue
		}
		incoming := 0
		for _, edge := range graph.Edges {
			if edge.Target == node.ID {
				incoming++
			}
		}
		if incoming == 0 {
			result.Diagnostics = append(result.Diagnostics, invariant(
				"Variable-aggregator '"+node.ID+"' has no incoming edges; "+
					"every aggregator must be reached by at least one branch leaf"))
		}
	}
}

// ── V6: branch handles (verifier.py:1432-1518) ─────────────────────

// verifyBranchHandles mirrors _verify_branch_handles.
func verifyBranchHandles(graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl, facts *graphfacts.SemanticGraphFacts, result *VerificationResult) {
	compilerOwned := facts.CompilerGeneratedQuestionClassifiers
	fingerprintIDs := map[string]bool{}
	for id := range facts.ClassificationContracts {
		fingerprintIDs[id] = true
	}
	for _, classifierID := range differenceSortedKeys(compilerOwned, fingerprintIDs) {
		branchError(result, "Compiler-owned question classifier '"+classifierID+"' has no immutable fingerprint")
	}
	for _, classifierID := range differenceSortedKeys(fingerprintIDs, compilerOwned) {
		branchError(result, "Question classifier fingerprint '"+classifierID+"' has no compiler-owned identity")
	}
	for _, classifierID := range sortedKeys(compilerOwned) {
		classifier := nodesByID[classifierID]
		if classifier == nil {
			branchError(result, "Question classifier immutable fingerprint '"+classifierID+"' has no emitted compiler-owned node")
			continue
		}
		if _, isClassifier := classifier.Data.(*dify.QuestionClassifierDataNode); !isClassifier {
			branchError(result, "Question classifier immutable fingerprint '"+classifierID+"' has no emitted compiler-owned node")
		}
	}

	for _, edge := range graph.Edges {
		if edge.Source == "" {
			continue
		}
		sourceNode := nodesByID[edge.Source]
		if sourceNode == nil {
			continue // already caught by V1
		}
		ifElse, isIfElse := sourceNode.Data.(*dify.IfElseDataNode)
		if !isIfElse {
			continue
		}
		validHandles := map[string]bool{"false": true}
		for _, c := range ifElse.Cases {
			validHandles[c.CaseID] = true
		}
		if !validHandles[edge.SourceHandle] {
			result.Diagnostics = append(result.Diagnostics, invariant(
				"Edge from '"+edge.Source+"' uses source_handle '"+edge.SourceHandle+
					"' which is not a valid case handle (valid: "+pyList(sortedKeys(validHandles))+")"))
		}
	}

	for i := range graph.Nodes {
		sourceNode := &graph.Nodes[i]
		classifier, isClassifier := sourceNode.Data.(*dify.QuestionClassifierDataNode)
		if !isClassifier {
			continue
		}
		configuredIDs := make([]string, 0, len(classifier.Classes))
		for _, classDef := range classifier.Classes {
			configuredIDs = append(configuredIDs, classDef.ID)
		}
		var outgoing []dify.EdgeDecl
		for _, edge := range graph.Edges {
			if edge.Source == sourceNode.ID {
				outgoing = append(outgoing, edge)
			}
		}
		actualHandles := make([]string, 0, len(outgoing))
		for _, edge := range outgoing {
			actualHandles = append(actualHandles, edge.SourceHandle)
		}
		if len(uniqueSet(configuredIDs)) != len(configuredIDs) {
			branchError(result, "Question classifier '"+sourceNode.ID+"' has duplicate configured class IDs")
		}
		if len(actualHandles) != len(configuredIDs) ||
			len(uniqueSet(actualHandles)) != len(actualHandles) ||
			!sameStringSet(actualHandles, configuredIDs) ||
			containsString(actualHandles, "false") {
			branchError(result, "Question classifier '"+sourceNode.ID+"' edges "+
				pyList(sortedCopy(actualHandles))+" do not exactly match classes "+
				pyList(sortedCopy(configuredIDs)))
		}
	}

	// Python iterates insertion-ordered dicts; Go maps are unordered. Sorted
	// iteration keeps diagnostics deterministic (Python's exact insertion
	// order is unreproducible without an ordered map; content is identical).
	for _, classifierID := range sortedKeysMap(facts.ClassificationContracts) {
		sourceNode := nodesByID[classifierID]
		if sourceNode == nil {
			continue
		}
		if _, isClassifier := sourceNode.Data.(*dify.QuestionClassifierDataNode); !isClassifier {
			continue
		}
		verifyClassificationFingerprint(graph, nodesByID, sourceNode, facts.ClassificationContracts[classifierID], facts, result)
	}
}

func uniqueSet(items []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

func sameStringSet(a, b []string) bool {
	sa := map[string]bool{}
	for _, item := range a {
		sa[item] = true
	}
	if len(sa) != len(b) {
		return false
	}
	for _, item := range b {
		if !sa[item] {
			return false
		}
	}
	return true
}

// branchError mirrors _branch_error (:1912-1921).
func branchError(result *VerificationResult, message string) {
	result.Diagnostics = append(result.Diagnostics, invariant(message))
}

// ── Human Input contracts (verifier.py:1519-1590) ──────────────────

// verifyHumanInputContracts mirrors _verify_human_input_contracts.
func verifyHumanInputContracts(graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl, facts *graphfacts.SemanticGraphFacts, result *VerificationResult) {
	owned := facts.CompilerGeneratedHumanInputs
	fingerprints := facts.HumanInputContracts
	emitted := map[string]bool{}
	for i := range graph.Nodes {
		node := &graph.Nodes[i]
		if _, isHuman := node.Data.(*dify.HumanInputDataNode); isHuman {
			emitted[node.ID] = true
		}
	}
	ownedOrFingerprinted := map[string]bool{}
	for id := range owned {
		ownedOrFingerprinted[id] = true
	}
	for id := range fingerprints {
		ownedOrFingerprinted[id] = true
	}
	for _, nodeID := range differenceSortedKeys(emitted, ownedOrFingerprinted) {
		branchError(result, "Emitted Human Input '"+nodeID+"' has no compiler-owned identity or fingerprint")
	}
	for _, nodeID := range differenceSortedKeys(owned, mapKeys(fingerprints)) {
		branchError(result, "Compiler-owned Human Input '"+nodeID+"' has no immutable fingerprint")
	}
	for _, nodeID := range differenceSortedKeys(mapKeys(fingerprints), owned) {
		branchError(result, "Human Input fingerprint '"+nodeID+"' has no compiler-owned identity")
	}

	for i := range graph.Nodes {
		node := &graph.Nodes[i]
		human, isHuman := node.Data.(*dify.HumanInputDataNode)
		if !isHuman {
			continue
		}
		inputNames := make([]string, 0, len(human.Inputs))
		for _, item := range human.Inputs {
			inputNames = append(inputNames, inputOutputName(item))
		}
		actionIDs := make([]string, 0, len(human.UserActions))
		for _, action := range human.UserActions {
			actionIDs = append(actionIDs, action.ID)
		}
		if len(uniqueSet(inputNames)) != len(inputNames) {
			branchError(result, "Human Input '"+node.ID+"' has duplicate input output names")
		}
		if len(uniqueSet(actionIDs)) != len(actionIDs) {
			branchError(result, "Human Input '"+node.ID+"' has duplicate action IDs")
		}
		var actualHandles []string
		for _, edge := range graph.Edges {
			if edge.Source == node.ID {
				actualHandles = append(actualHandles, edge.SourceHandle)
			}
		}
		expectedHandles := append(append([]string(nil), actionIDs...), "__timeout")
		if len(actualHandles) != len(expectedHandles) ||
			len(uniqueSet(actualHandles)) != len(actualHandles) ||
			!sameStringSet(actualHandles, expectedHandles) {
			branchError(result, "Human Input '"+node.ID+"' outgoing handles "+
				pyList(sortedCopy(actualHandles))+" do not exactly match NodeCoda branch handles "+
				pyList(sortedCopy(expectedHandles)))
		}
	}

	// Same sorted-iteration rationale as the classification contracts loop:
	// Python iterates dict insertion order; Go maps are unordered.
	for _, nodeID := range sortedKeysMap(fingerprints) {
		fingerprint := fingerprints[nodeID]
		emittedNode := nodesByID[nodeID]
		if emittedNode == nil {
			branchError(result, "Human Input immutable fingerprint '"+nodeID+"' has no emitted node")
			continue
		}
		if _, isHuman := emittedNode.Data.(*dify.HumanInputDataNode); !isHuman {
			branchError(result, "Human Input immutable fingerprint '"+nodeID+"' has no emitted node")
			continue
		}
		verifyHumanInputFingerprint(graph, nodesByID, emittedNode, fingerprint, facts, result)
	}
}

func inputOutputName(field dify.HumanInputField) string {
	switch f := field.(type) {
	case *dify.ParagraphHumanInput:
		return f.OutputVariableName
	case *dify.SelectHumanInput:
		return f.OutputVariableName
	case *dify.FileHumanInput:
		return f.OutputVariableName
	case *dify.FileListHumanInput:
		return f.OutputVariableName
	}
	return ""
}

func mapKeys[V any](m map[string]V) map[string]bool {
	out := make(map[string]bool, len(m))
	for key := range m {
		out[key] = true
	}
	return out
}

// verifyHumanInputFingerprint mirrors _verify_human_input_fingerprint
// (:1591-1810).
func verifyHumanInputFingerprint(graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl, node *dify.NodeDecl, fingerprint graphfacts.HumanInputContractFingerprint, facts *graphfacts.SemanticGraphFacts, result *VerificationResult) {
	data := node.Data.(*dify.HumanInputDataNode)
	var actualFields [][4]any
	for _, inputField := range data.Inputs {
		switch f := inputField.(type) {
		case *dify.ParagraphHumanInput:
			var sourceType any
			var selector []string
			var sourceValue any
			if f.Default != nil {
				sourceType = f.Default.Type
				selector = f.Default.Selector
				sourceValue = f.Default.Value
			}
			actualFields = append(actualFields, [4]any{f.OutputVariableName, f.Type, []string{}, [3]any{sourceType, selector, sourceValue}})
		case *dify.SelectHumanInput:
			actualFields = append(actualFields, [4]any{f.OutputVariableName, f.Type, f.OptionSource.Value, [2]any{f.OptionSource.Type, f.OptionSource.Selector}})
		case *dify.FileHumanInput:
			actualFields = append(actualFields, [4]any{f.OutputVariableName, f.Type, []string{}, nil})
		case *dify.FileListHumanInput:
			actualFields = append(actualFields, [4]any{f.OutputVariableName, f.Type, []string{}, nil})
		}
	}
	var expectedFields [][4]any
	for _, field := range fingerprint.Fields {
		if field.Kind == "paragraph" {
			expectedFields = append(expectedFields, [4]any{field.Name, field.Kind, field.Options, [3]any{field.SourceType, field.SourceSelector, field.SourceValue}})
		} else {
			expectedFields = append(expectedFields, [4]any{field.Name, field.Kind, field.Options, [2]any{field.SourceType, field.SourceSelector}})
		}
	}
	var actualActions [][3]string
	for _, action := range data.UserActions {
		actualActions = append(actualActions, [3]string{action.ID, action.Title, action.ButtonStyle})
	}
	var expectedActions [][3]string
	for _, action := range fingerprint.Actions {
		expectedActions = append(expectedActions, [3]string{action.Member, action.Title, action.Style})
	}
	if data.FormContent != fingerprint.Content ||
		!reflect.DeepEqual(actualFields, expectedFields) ||
		!reflect.DeepEqual(actualActions, expectedActions) ||
		data.Timeout != fingerprint.TimeoutValue ||
		data.TimeoutUnit != fingerprint.TimeoutUnit ||
		!deliveryMethodsEqual(data.DeliveryMethods, fingerprint.DeliveryMethods) {
		branchError(result, "Human Input '"+node.ID+"' payload disagrees with its immutable fingerprint")
	}

	expectedHandles := make([]string, 0, len(fingerprint.Actions)+1)
	for _, action := range fingerprint.Actions {
		expectedHandles = append(expectedHandles, action.Member)
	}
	expectedHandles = append(expectedHandles, "__timeout")
	var branchHandles []string
	for _, branch := range fingerprint.Branches {
		branchHandles = append(branchHandles, branch.Handle)
	}
	if !reflect.DeepEqual(branchHandles, expectedHandles) {
		branchError(result, "Human Input '"+node.ID+"' fingerprint has missing or extra NodeCoda branches")
	}

	var continuationID *string
	if fingerprint.ContinuationNodeID != nil {
		continuationID = fingerprint.ContinuationNodeID
	}
	var continuation *dify.NodeDecl
	if continuationID != nil {
		continuation = nodesByID[*continuationID]
	}
	expectedContinuationEdges := map[edgeTriple]int{}
	if continuationID != nil {
		for _, branch := range fingerprint.Branches {
			for _, tail := range branch.FallthroughTails {
				tailID, ok := tail[0].(string)
				if !ok {
					continue
				}
				tailHandle, _ := tail[1].(string)
				expectedContinuationEdges[edgeTriple{tailID, *continuationID, tailHandle}]++
			}
		}
	}
	if continuationID != nil && (continuation == nil ||
		!humanInputContinuationPayloadEqual(graphfacts.HumanInputContinuationPayload(continuation), fingerprint.ContinuationPayload) ||
		!facts.CompilerGeneratedCodeNodes[*continuationID] ||
		facts.CodeNodeProvenance[*continuationID] != "generated-calculation") {
		branchError(result, "Human Input '"+node.ID+"' continuation disagrees with its immutable fingerprint")
	}
	var actualOwnership *graphfacts.NodeOwnershipFingerprint
	if continuation != nil {
		actualOwnership = nodeOwnership(continuation)
	}
	if !nodeOwnershipEqual(actualOwnership, fingerprint.ContinuationOwnership) {
		branchError(result, "Human Input '"+node.ID+"' continuation ownership disagrees with its immutable fingerprint")
	}

	allBranchNodes := map[string]bool{}
	fieldNames := map[string]bool{}
	for _, field := range fingerprint.Fields {
		fieldNames[field.Name] = true
	}
	var outgoing []dify.EdgeDecl
	for _, edge := range graph.Edges {
		if edge.Source == node.ID {
			outgoing = append(outgoing, edge)
		}
	}
	for _, branch := range fingerprint.Branches {
		branchIDs := map[string]bool{}
		for _, id := range branch.NodeIDs {
			branchIDs[id] = true
		}
		shared := false
		for id := range branchIDs {
			if allBranchNodes[id] {
				shared = true
				break
			}
		}
		if shared {
			branchError(result, "Human Input '"+node.ID+"' branch bodies share emitted graph nodes")
		}
		for id := range branchIDs {
			allBranchNodes[id] = true
		}
		missing := false
		for _, id := range branch.NodeIDs {
			if nodesByID[id] == nil {
				missing = true
				break
			}
		}
		if missing {
			branchError(result, "Human Input '"+node.ID+"' branch '"+branch.Handle+"' references missing nodes")
			continue
		}
		liveTails := map[edgeTriple]bool{}
		for _, tail := range branch.FallthroughTails {
			tailID, ok := tail[0].(string)
			if !ok {
				continue
			}
			tailHandle, _ := tail[1].(string)
			liveTails[edgeTriple{tailID, "", tailHandle}] = true
		}
		var actualBranchEdges []edgeTriple
		for _, edge := range graph.Edges {
			included := (edge.Source == node.ID && edge.SourceHandle == branch.Handle) ||
				branchIDs[edge.Source] || branchIDs[edge.Target]
			if included && liveTails[edgeTriple{edge.Source, "", edge.SourceHandle}] && !branchIDs[edge.Target] {
				continue
			}
			if included {
				actualBranchEdges = append(actualBranchEdges, edgeTriple{edge.Source, edge.Target, edge.SourceHandle})
			}
		}
		expectedBranchEdges := make([]edgeTriple, 0, len(branch.Edges))
		for _, triple := range branch.Edges {
			expectedBranchEdges = append(expectedBranchEdges, edgeTriple{triple[0], triple[1], triple[2]})
		}
		if !multisetEqual(actualBranchEdges, expectedBranchEdges) {
			branchError(result, "Human Input '"+node.ID+"' branch '"+branch.Handle+"' topology disagrees with its immutable fingerprint")
		}
		for _, tail := range branch.FallthroughTails {
			tailID, ok := tail[0].(string)
			if !ok {
				continue
			}
			tailHandle, _ := tail[1].(string)
			tailEdges := 0
			tailTargets := map[string]bool{}
			for _, edge := range graph.Edges {
				if edge.Source == tailID && edge.SourceHandle == tailHandle && !branchIDs[edge.Target] {
					tailEdges++
					tailTargets[edge.Target] = true
				}
			}
			if tailEdges > 1 {
				branchError(result, "Human Input '"+node.ID+"' branch '"+branch.Handle+"' continuation tails disagree with its immutable fingerprint")
			}
			expectedTargets := map[string]bool{}
			if continuationID != nil {
				expectedTargets[*continuationID] = true
			}
			if !sameSet(tailTargets, expectedTargets) {
				branchError(result, "Human Input '"+node.ID+"' branch continuation targets disagree with its immutable fingerprint")
			}
		}
		var sourceEdges []dify.EdgeDecl
		for _, edge := range outgoing {
			if edge.SourceHandle == branch.Handle {
				sourceEdges = append(sourceEdges, edge)
			}
		}
		if len(sourceEdges) == 1 && len(branch.NodeIDs) > 0 {
			witnessedTargets := map[string]bool{}
			for _, triple := range branch.Edges {
				if triple[0] == node.ID && triple[2] == branch.Handle {
					witnessedTargets[triple[1]] = true
				}
			}
			if !sameSet(map[string]bool{sourceEdges[0].Target: true}, witnessedTargets) {
				branchError(result, "Human Input '"+node.ID+"' branch '"+branch.Handle+"' enters the wrong body")
			}
		}
		var actualSelectors [][]string
		for _, branchNodeID := range branch.NodeIDs {
			for _, selector := range dify.CollectNodeSelectors(nodesByID[branchNodeID]) {
				if len(selector.Path) >= 1 && selector.Path[0] == node.ID {
					actualSelectors = append(actualSelectors, append([]string(nil), selector.Path...))
				}
			}
		}
		if !reflect.DeepEqual(actualSelectors, branch.SubmittedSelectors) {
			branchError(result, "Human Input '"+node.ID+"' branch '"+branch.Handle+"' submitted selectors disagree with its immutable fingerprint")
		}
		invalidSelectors := false
		for _, selector := range actualSelectors {
			if len(selector) != 2 || !fieldNames[selector[1]] {
				invalidSelectors = true
				break
			}
		}
		if invalidSelectors {
			branchError(result, "Human Input '"+node.ID+"' branch '"+branch.Handle+"' has unknown submitted selectors")
		}
		if branch.Handle == "__timeout" && len(actualSelectors) > 0 {
			branchError(result, "Human Input '"+node.ID+"' timeout branch carries a submitted-value selector")
		}
	}

	actualContinuationEdges := map[edgeTriple]int{}
	if continuationID != nil {
		for _, edge := range graph.Edges {
			if edge.Target == *continuationID {
				actualContinuationEdges[edgeTriple{edge.Source, edge.Target, edge.SourceHandle}]++
			}
		}
	}
	if !multisetEqualKeys(actualContinuationEdges, expectedContinuationEdges) {
		branchError(result, "Human Input '"+node.ID+"' continuation edges disagree with its immutable fingerprint")
	}
}

// nodeOwnership builds a NodeOwnershipFingerprint from a node's container
// flags (graph_facts.py seal_human_input_continuation_ownership :208-225).
func nodeOwnership(node *dify.NodeDecl) *graphfacts.NodeOwnershipFingerprint {
	cf, ok := node.Data.(interface {
		ContainerFlags() (bool, string, bool, string)
	})
	var isInIteration, isInLoop bool
	var iterationID, loopID string
	if ok {
		isInIteration, iterationID, isInLoop, loopID = cf.ContainerFlags()
	}
	return &graphfacts.NodeOwnershipFingerprint{
		ParentID:      node.ParentID,
		ZIndex:        node.ZIndex,
		IsInIteration: isInIteration,
		IterationID:   iterationID,
		IsInLoop:      isInLoop,
		LoopID:        loopID,
	}
}

func nodeOwnershipEqual(a, b *graphfacts.NodeOwnershipFingerprint) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return reflect.DeepEqual(a, b)
}

func humanInputContinuationPayloadEqual(a, b *graphfacts.HumanInputContinuationPayloadFingerprint) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return reflect.DeepEqual(a, b)
}

func multisetEqual(a, b []edgeTriple) bool {
	if len(a) != len(b) {
		return false
	}
	counts := map[edgeTriple]int{}
	for _, item := range a {
		counts[item]++
	}
	for _, item := range b {
		counts[item]--
		if counts[item] < 0 {
			return false
		}
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

func multisetEqualKeys(a, b map[edgeTriple]int) bool {
	if len(a) != len(b) {
		return false
	}
	for key, count := range a {
		if b[key] != count {
			return false
		}
	}
	return true
}

// ── classification fingerprint (verifier.py:1811-1910) ─────────────

// verifyClassificationFingerprint mirrors _verify_classification_fingerprint.
func verifyClassificationFingerprint(graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl, sourceNode *dify.NodeDecl, fingerprint graphfacts.ClassificationContractFingerprint, facts *graphfacts.SemanticGraphFacts, result *VerificationResult) {
	data := sourceNode.Data.(*dify.QuestionClassifierDataNode)
	var outgoing []dify.EdgeDecl
	for _, edge := range graph.Edges {
		if edge.Source == sourceNode.ID {
			outgoing = append(outgoing, edge)
		}
	}
	configuredIDs := make([]string, 0, len(data.Classes))
	for _, classDef := range data.Classes {
		configuredIDs = append(configuredIDs, classDef.ID)
	}
	expectedIDs := make([]string, 0, len(fingerprint.Classes))
	for _, item := range fingerprint.Classes {
		expectedIDs = append(expectedIDs, item.ClassID)
	}
	type classTriple struct{ ID, Name, Label string }
	emittedClasses := make([]classTriple, 0, len(data.Classes))
	for _, item := range data.Classes {
		emittedClasses = append(emittedClasses, classTriple{item.ID, item.Name, item.Label})
	}
	expectedClasses := make([]classTriple, 0, len(fingerprint.Classes))
	for _, item := range fingerprint.Classes {
		expectedClasses = append(expectedClasses, classTriple{item.ClassID, item.Name, item.Label})
	}
	if !reflect.DeepEqual(configuredIDs, expectedIDs) || !reflect.DeepEqual(emittedClasses, expectedClasses) {
		branchError(result, "Question classifier '"+sourceNode.ID+"' classes disagree with its immutable fingerprint")
	}
	expectedTargets := map[string]string{}
	for _, item := range fingerprint.Classes {
		expectedTargets[item.ClassID] = item.AdapterNodeID
	}
	actualTargets := map[string]string{}
	for _, edge := range outgoing {
		actualTargets[edge.SourceHandle] = edge.Target
	}
	if !reflect.DeepEqual(actualTargets, expectedTargets) {
		branchError(result, "Question classifier '"+sourceNode.ID+"' branch targets disagree with its immutable fingerprint")
	}

	for _, item := range fingerprint.Classes {
		adapter := nodesByID[item.AdapterNodeID]
		var actualIncoming []triple
		for _, edge := range graph.Edges {
			if edge.Target == item.AdapterNodeID {
				actualIncoming = append(actualIncoming, triple{edge.Source, edge.Target, edge.SourceHandle})
			}
		}
		sortTriples(actualIncoming)
		var actualOutgoing []triple
		for _, edge := range graph.Edges {
			if edge.Source == item.AdapterNodeID {
				actualOutgoing = append(actualOutgoing, triple{edge.Source, edge.Target, edge.SourceHandle})
			}
		}
		sortTriples(actualOutgoing)
		expectedIncoming := []triple{{sourceNode.ID, item.AdapterNodeID, item.ClassID}}
		expectedOutgoing := []triple{{item.AdapterNodeID, fingerprint.AggregatorID, ""}}
		adapterCode, isCode := adapter.Data.(*dify.CodeDataNode)
		constantValue := classificationConstantValue(adapterCode)
		violation := adapter == nil || !isCode ||
			facts.CodeNodeProvenance[item.AdapterNodeID] != "classification-constant" ||
			!facts.CompilerGeneratedCodeNodes[item.AdapterNodeID] ||
			len(adapterCode.Variables) > 0 ||
			!singleResultContract(adapterCode.DeclaredOutputContract, fingerprint.EnumType) ||
			(derefConstValue(constantValue) != item.WireValue) ||
			!reflect.DeepEqual(actualIncoming, expectedIncoming) ||
			!reflect.DeepEqual(actualOutgoing, expectedOutgoing)
		if violation {
			branchError(result, "Question classifier '"+sourceNode.ID+"' adapter '"+item.AdapterNodeID+"' disagrees with its immutable fingerprint")
		}
	}

	aggregator := nodesByID[fingerprint.AggregatorID]
	if aggregator == nil {
		branchError(result, "Question classifier '"+sourceNode.ID+"' aggregator '"+fingerprint.AggregatorID+"' disagrees with its immutable fingerprint")
		return
	}
	aggregatorData, isAggregator := aggregator.Data.(*dify.VariableAggregatorDataNode)
	if !isAggregator {
		branchError(result, "Question classifier '"+sourceNode.ID+"' aggregator '"+fingerprint.AggregatorID+"' disagrees with its immutable fingerprint")
		return
	}
	var actualSelectors [][]string
	for _, selector := range aggregatorData.Variables {
		actualSelectors = append(actualSelectors, append([]string(nil), selector.Path...))
	}
	selectorsArePlain := true
	for _, selector := range aggregatorData.Variables {
		if selector.Variable != "" || selector.ValueType != "" {
			selectorsArePlain = false
			break
		}
	}
	outputType := ""
	if aggregatorData.OutputType != nil {
		outputType = *aggregatorData.OutputType
	}
	if !reflect.DeepEqual(actualSelectors, fingerprint.AggregatorSelectors) ||
		!selectorsArePlain ||
		outputType != fingerprint.OutputType {
		branchError(result, "Question classifier '"+sourceNode.ID+"' aggregator payload disagrees with its immutable fingerprint")
	}

	adapterIDs := map[string]bool{}
	for _, item := range fingerprint.Classes {
		adapterIDs[item.AdapterNodeID] = true
	}
	var actualAdapterEdges []triple
	for _, edge := range graph.Edges {
		if adapterIDs[edge.Source] || edge.Target == fingerprint.AggregatorID {
			actualAdapterEdges = append(actualAdapterEdges, triple{edge.Source, edge.Target, edge.SourceHandle})
		}
	}
	sortTriples(actualAdapterEdges)
	var expectedAdapterEdges []triple
	for adapterID := range adapterIDs {
		expectedAdapterEdges = append(expectedAdapterEdges, triple{adapterID, fingerprint.AggregatorID, ""})
	}
	sortTriples(expectedAdapterEdges)
	if !reflect.DeepEqual(actualAdapterEdges, expectedAdapterEdges) {
		branchError(result, "Question classifier '"+sourceNode.ID+"' adapter-to-aggregator edges disagree with its immutable fingerprint")
	}
}

func sortTriples(items []triple) {
	sort.Slice(items, func(i, j int) bool {
		for k := 0; k < 3; k++ {
			if items[i][k] != items[j][k] {
				return items[i][k] < items[j][k]
			}
		}
		return false
	})
}

// singleResultContract reports whether contract is exactly {"result": typ}
// (Python `adapter.data.declared_output_contract != {"result": enum_type}`).
func singleResultContract(contract map[string]*types.Descriptor, typ *types.Descriptor) bool {
	if len(contract) != 1 {
		return false
	}
	return descriptorsEqual(contract["result"], typ)
}

// ── V14b: code output contracts (verifier.py:1014-1053) ────────────

// verifyCodeOutputContracts mirrors _verify_code_output_contracts.
func verifyCodeOutputContracts(graph *dify.GraphNode, typeCodec *codec.Codec, result *VerificationResult) {
	if typeCodec == nil {
		typeCodec = codec.Default()
	}
	for i := range graph.Nodes {
		node := &graph.Nodes[i]
		code, isCode := node.Data.(*dify.CodeDataNode)
		if !isCode {
			continue
		}
		contract := code.DeclaredOutputContract
		if contract == nil {
			continue
		}
		outputNames := sortedKeysMap(code.Outputs)
		contractNames := sortedKeysMap(contract)
		if !sameStringSet(outputNames, contractNames) {
			result.Diagnostics = append(result.Diagnostics, invariant(
				"Code node '"+node.ID+"' ports "+pyList(outputNames)+" do not match "+
					"published contract "+pyList(contractNames)))
			continue
		}
		for portName, typeInfo := range contract {
			expectedType, expectedChildren := portShapeToComparison(typeCodec.ToCodePort(typeInfo))
			actual := code.Outputs[portName]
			var actualChildren any
			if len(actual.Children) > 0 {
				actualChildren = outputDefChildrenToComparisonMap(actual.Children)
			}
			if actual.Type != expectedType || !reflect.DeepEqual(actualChildren, expectedChildren) {
				result.Diagnostics = append(result.Diagnostics, invariant(
					"Code node '"+node.ID+"' port '"+portName+"' shape does not match "+
						"published type "+typeInfo.String()))
			}
		}
	}
}

// ── V7: code passthrough (verifier.py:1971-1987) ───────────────────

// verifyCodePassthrough mirrors _verify_code_passthrough: a no-op
// documentation anchor (NODECODA-VER-006). The verifier intentionally never
// rejects helper-like Code nodes without compiler call metadata.
func verifyCodePassthrough(graph *dify.GraphNode, nodesByID map[string]*dify.NodeDecl, result *VerificationResult) {
}

func derefConstValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// intValueEquals compares an any-typed numeric value with an integer target,
// matching Python's numeric == semantics (Python int == int64/float values
// compare by value). The Go lowering stores assignment values as int64
// (flow_iter.go for_increment), but direct constructions may use int; the
// dynamic type must not leak into the comparison (see task-37 R1: any == 1
// is false when the dynamic type is int64).
func intValueEquals(v any, target int) bool {
	switch n := v.(type) {
	case int:
		return n == target
	case int64:
		return n == int64(target)
	case int32:
		return int64(n) == int64(target)
	case uint64:
		return n == uint64(target)
	case uint32:
		return uint64(n) == uint64(target)
	case float64:
		return n == float64(target)
	case float32:
		return float64(n) == float64(target)
	}
	return false
}
