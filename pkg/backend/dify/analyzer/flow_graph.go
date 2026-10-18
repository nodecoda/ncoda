// flow_graph.go ports src/nclang/flow_graph.py (134 lines): the adjacency
// lists, reachability closures and build helper used by the analyzer
// passes. Only the members analyze() actually uses are ported;
// topological_sort and detect_cycles are not part of the analysis stage
// (no caller in analyzer.py) and are omitted.
package analyzer

// FlowGraph mirrors flow_graph.py FlowGraph (:13-32): directed graph built
// from workflow nodes and edges.
type FlowGraph struct {
	Adj        map[string][]string
	ReverseAdj map[string][]string
	NodeIDs    map[string]struct{}
	Entry      string
	Exits      map[string]struct{}
}

func newFlowGraph() *FlowGraph {
	return &FlowGraph{
		Adj:        make(map[string][]string),
		ReverseAdj: make(map[string][]string),
		NodeIDs:    make(map[string]struct{}),
		Exits:      make(map[string]struct{}),
	}
}

// predecessors ports FlowGraph.predecessors (:22-24).
func (g *FlowGraph) predecessors(nodeID string) []string {
	return g.ReverseAdj[nodeID]
}

// successors ports FlowGraph.successors (:25-27).
func (g *FlowGraph) successors(nodeID string) []string {
	return g.Adj[nodeID]
}

// ancestors ports FlowGraph.ancestors (:28-40): the transitive closure of
// predecessors (nodes reachable by walking backwards). BFS order is
// irrelevant — the result is a set.
func (g *FlowGraph) ancestors(nodeID string) map[string]struct{} {
	visited := make(map[string]struct{})
	queue := append([]string(nil), g.predecessors(nodeID)...)
	for head := 0; head < len(queue); head++ {
		n := queue[head]
		if _, ok := visited[n]; ok {
			continue
		}
		visited[n] = struct{}{}
		queue = append(queue, g.predecessors(n)...)
	}
	return visited
}

// descendants ports FlowGraph.descendants (:42-54): the transitive closure
// of successors.
func (g *FlowGraph) descendants(nodeID string) map[string]struct{} {
	visited := make(map[string]struct{})
	queue := append([]string(nil), g.successors(nodeID)...)
	for head := 0; head < len(queue); head++ {
		n := queue[head]
		if _, ok := visited[n]; ok {
			continue
		}
		visited[n] = struct{}{}
		queue = append(queue, g.successors(n)...)
	}
	return visited
}

// reachableFromEntry ports FlowGraph.reachable_from_entry (:56-66): all
// nodes reachable from the entry node (entry itself included).
func (g *FlowGraph) reachableFromEntry() map[string]struct{} {
	if g.Entry == "" {
		return map[string]struct{}{}
	}
	visited := map[string]struct{}{g.Entry: {}}
	queue := append([]string(nil), g.successors(g.Entry)...)
	for head := 0; head < len(queue); head++ {
		n := queue[head]
		if _, ok := visited[n]; ok {
			continue
		}
		visited[n] = struct{}{}
		queue = append(queue, g.successors(n)...)
	}
	return visited
}

// buildFlowGraph ports build_flow_graph (flow_graph.py:122-134).
func buildFlowGraph(nodeIDs []string, edges [][2]string, entry string, exits map[string]struct{}) *FlowGraph {
	g := newFlowGraph()
	for _, id := range nodeIDs {
		g.NodeIDs[id] = struct{}{}
	}
	g.Entry = entry
	g.Exits = exits
	for _, e := range edges {
		g.Adj[e[0]] = append(g.Adj[e[0]], e[1])
		g.ReverseAdj[e[1]] = append(g.ReverseAdj[e[1]], e[0])
	}
	return g
}
