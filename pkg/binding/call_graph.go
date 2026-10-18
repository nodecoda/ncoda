package binding

// Stable-ID user call graph construction and recursion rejection, ported
// from src/nclang/lang/call_graph.py (RecursiveCycle /
// propagate_function_effects / find_recursive_cycle) plus the graph builder
// binder.py build_user_call_graph / BoundProgramBuilder._build_call_graph
// (binder.py:119-157,453-461). The graph is safe to publish immediately
// after the scope phase: it consumes only function bindings and call
// targets, both produced by Bind.

import (
	"sort"

	"github.com/nodecoda/ncoda/pkg/ast"
)

// Edge identifies one call edge (caller, callee) in the user call graph,
// mirroring the Python call_sites dict key tuple[FunctionId, FunctionId].
type Edge = [2]FunctionID

// RecursiveCycle mirrors the frozen RecursiveCycle dataclass in
// call_graph.py:7-10: the lexically canonical cycle path plus the minimal
// AstNodeID of the first call site closing the cycle.
type RecursiveCycle struct {
	Functions []FunctionID
	CallSite  AstNodeID
}

// BuildUserCallGraph publishes only the user call edges, safe immediately
// after ScopePass, mirroring binder.py build_user_call_graph (binder.py:453)
// and _build_call_graph (binder.py:119-157):
//
//   - edges contains an (initially empty) adjacency list for every function
//     binding, so FindRecursiveCycle's graph scan sees every function;
//   - every function body (code functions, functions, entry) is walked in
//     traversal order; each CallExpr bound to a FunctionCallTarget becomes
//     one edge (deduplicated) and one call-site entry (NOT deduplicated:
//     the reference appends every call_id to the edge's site list);
//   - builtin / stdlib calls never produce edges.
func BuildUserCallGraph(p *ast.Program, snap *Snapshot) (map[FunctionID][]FunctionID, map[Edge][]AstNodeID) {
	edges := map[FunctionID][]FunctionID{}
	snap.Functions().Range(func(_ AstNodeID, fid FunctionID) bool {
		edges[fid] = []FunctionID{}
		return true
	})
	sites := map[Edge][]AstNodeID{}

	add := func(caller FunctionID, callee FunctionID, site AstNodeID) {
		if !containsID(edges[caller], callee) {
			edges[caller] = append(edges[caller], callee)
		}
		key := Edge{caller, callee}
		sites[key] = append(sites[key], site)
	}

	for _, fn := range programFunctions(p) {
		caller, found := snap.NodeIDOf(fn)
		if !found {
			continue // Python: function_bindings.get(node_id) is None
		}
		fid, found := snap.Functions().Get(caller)
		if !found {
			continue
		}
		body := fn.Body()
		if body == nil {
			continue
		}
		ast.Walk(&callCollector{snap: snap, add: add, caller: fid}, body)
	}
	return edges, sites
}

// callCollector walks one function body and publishes every CallExpr bound
// to a user function, mirroring binder.py _build_call_graph's
// `for node in _iter_frozen(body)` loop (binder.py:134-141).
type callCollector struct {
	snap   *Snapshot
	add    func(caller, callee FunctionID, site AstNodeID)
	caller FunctionID
}

func (c *callCollector) Visit(n ast.Node) ast.Visitor {
	if call, ok := n.(*ast.CallExpr); ok {
		callID, found := c.snap.NodeIDOf(call)
		if !found {
			return c
		}
		target, found := c.snap.CallTargets().Get(callID)
		if !found || target.Kind != CallFunction {
			return c
		}
		c.add(c.caller, target.Function, callID)
	}
	return c
}

func containsID(ids []FunctionID, want FunctionID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// PropagateFunctionEffects propagates workflow-operation effects through an
// acyclic user call graph, mirroring call_graph.py:20-39. directEffects is
// the per-function immediate classification (missing entries default to
// CALCULATION); a function is WORKFLOW_OPERATION when it is directly one or
// when any callee (transitively) is. The visiting guard raises on cycles —
// the pipeline's E1033 check runs before EffectPass, but the defensive
// check is preserved for direct callers (call_graph.py:29-30 raise).
func PropagateFunctionEffects(graph map[FunctionID][]FunctionID, directEffects map[FunctionID]EffectKind) map[FunctionID]EffectKind {
	resolved := map[FunctionID]EffectKind{}
	visiting := map[FunctionID]bool{}

	var resolve func(functionID FunctionID) EffectKind
	resolve = func(functionID FunctionID) EffectKind {
		if existing, ok := resolved[functionID]; ok {
			return existing
		}
		if visiting[functionID] {
			panic("binding: effect propagation requires an acyclic call graph")
		}
		visiting[functionID] = true
		effect, found := directEffects[functionID]
		if !found {
			effect = EffectCalculation // Python: direct_effects.get(fid, CALCULATION)
		}
		if effect == EffectCalculation && hasOperationCallee(graph[functionID], resolve) {
			effect = EffectWorkflowOperation
		}
		delete(visiting, functionID)
		resolved[functionID] = effect
		return effect
	}

	for functionID := range graph {
		resolve(functionID)
	}
	return resolved
}

func hasOperationCallee(callees []FunctionID, resolve func(FunctionID) EffectKind) bool {
	for _, callee := range callees {
		if resolve(callee) == EffectWorkflowOperation {
			return true
		}
	}
	return false
}

// FindRecursiveCycle returns the lexically canonical cyclic SCC, excluding
// acyclic callers, mirroring call_graph.py:41-95. Tarjan's algorithm finds
// the SCCs; the smallest (by ascending FunctionID tuple) cyclic component is
// chosen; a directed cycle within it starts at the smallest member and the
// minimal call site (smallest AstNodeID) on the closing edge is returned.
// Returns nil when the graph is acyclic.
func FindRecursiveCycle(graph map[FunctionID][]FunctionID, callSites map[Edge][]AstNodeID) *RecursiveCycle {
	index := 0
	indices := map[FunctionID]int{}
	lowlinks := map[FunctionID]int{}
	var stack []FunctionID
	onStack := map[FunctionID]bool{}
	var components [][]FunctionID

	var strongConnect func(node FunctionID)
	strongConnect = func(node FunctionID) {
		indices[node] = index
		lowlinks[node] = index
		index++
		stack = append(stack, node)
		onStack[node] = true
		for _, target := range sortedFunctionIDs(graph[node]) {
			if _, seen := indices[target]; !seen {
				strongConnect(target)
				if lowlinks[target] < lowlinks[node] {
					lowlinks[node] = lowlinks[target]
				}
			} else if onStack[target] {
				if indices[target] < lowlinks[node] {
					lowlinks[node] = indices[target]
				}
			}
		}
		if lowlinks[node] != indices[node] {
			return
		}
		var component []FunctionID
		for {
			member := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			delete(onStack, member)
			component = append(component, member)
			if member == node {
				break
			}
		}
		sort.Slice(component, func(i, j int) bool { return component[i] < component[j] })
		components = append(components, component)
	}

	for _, functionID := range sortedFunctionIDs(keys(graph)) {
		if _, seen := indices[functionID]; !seen {
			strongConnect(functionID)
		}
	}

	var cyclic [][]FunctionID
	for _, component := range components {
		if len(component) > 1 || containsID(graph[component[0]], component[0]) {
			cyclic = append(cyclic, component)
		}
	}
	if len(cyclic) == 0 {
		return nil
	}
	members := cyclic[0]
	for _, component := range cyclic[1:] {
		if componentLess(component, members) {
			members = component
		}
	}
	memberSet := map[FunctionID]bool{}
	for _, m := range members {
		memberSet[m] = true
	}
	start := members[0]

	var directedCycle func(node FunctionID, path []FunctionID, visited map[FunctionID]bool) []FunctionID
	directedCycle = func(node FunctionID, path []FunctionID, visited map[FunctionID]bool) []FunctionID {
		for _, target := range sortedFunctionIDs(graph[node]) {
			if !memberSet[target] {
				continue
			}
			if target == start {
				return path
			}
			if visited[target] {
				continue
			}
			nextVisited := map[FunctionID]bool{}
			for v := range visited {
				nextVisited[v] = true
			}
			nextVisited[target] = true
			if found := directedCycle(target, append(append([]FunctionID(nil), path...), target), nextVisited); found != nil {
				return found
			}
		}
		return nil
	}

	path := directedCycle(start, []FunctionID{start}, map[FunctionID]bool{start: true})
	if path == nil {
		panic("binding: cyclic SCC has no directed cycle")
	}
	firstTarget := path[0]
	if len(path) > 1 {
		firstTarget = path[1]
	}
	edgeSites := callSites[Edge{path[0], firstTarget}]
	return &RecursiveCycle{Functions: path, CallSite: minAstNodeID(edgeSites)}
}

func sortedFunctionIDs(ids []FunctionID) []FunctionID {
	out := append([]FunctionID(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func keys(m map[FunctionID][]FunctionID) []FunctionID {
	out := make([]FunctionID, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// componentLess compares two cyclic components by their ascending ID tuple
// (Python: key=lambda component: tuple(item.value for item in component)).
func componentLess(a, b []FunctionID) bool {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func minAstNodeID(ids []AstNodeID) AstNodeID {
	if len(ids) == 0 {
		panic("binding: recursive-cycle edge has no call sites")
	}
	best := ids[0]
	for _, id := range ids[1:] {
		if id < best {
			best = id
		}
	}
	return best
}
