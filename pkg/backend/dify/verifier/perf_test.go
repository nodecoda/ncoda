package verifier

import (
	"fmt"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
)

// Performance regression guards for computeDominators (Task 43 P3).
//
// computeDominators returns a bitset dominatorSet (O(n^2/64) memory) and
// converges in a single reverse-postorder sweep on chains/DAGs. These
// benchmarks pin the 10k-node chain and star (a star's root fans out to
// every leaf) so a regression to the map-materializing or O(n^3)
// intersection form shows up as seconds / GBs.
//
//	go test -bench=BenchmarkComputeDominators -benchtime=1x ./internal/backend/dify/verifier/
//
// Expected on the dev box (post-P3): chain-10k ~5-15ms, star-10k ~5-15ms.

func dominatorBenchGraph(shape string, n int) (*dify.GraphNode, map[string]*dify.NodeDecl) {
	nodes := make([]dify.NodeDecl, 0, n)
	nodesByID := make(map[string]*dify.NodeDecl, n)
	edges := make([]dify.EdgeDecl, 0, n-1)
	add := func(id, ntype string) {
		nd := &dify.NodeDecl{ID: id, NodeType: ntype}
		nodesByID[id] = nd
		nodes = append(nodes, *nd)
	}
	add("n0", "start")
	for i := 1; i < n; i++ {
		add(fmt.Sprintf("n%d", i), "code")
		if shape == "chain" {
			edges = append(edges, dify.EdgeDecl{Source: fmt.Sprintf("n%d", i-1), Target: fmt.Sprintf("n%d", i)})
		} else { // star: every leaf reads the root
			edges = append(edges, dify.EdgeDecl{Source: "n0", Target: fmt.Sprintf("n%d", i)})
		}
	}
	return &dify.GraphNode{Nodes: nodes, Edges: edges}, nodesByID
}

func BenchmarkComputeDominatorsChain(b *testing.B) {
	graph, nodesByID := dominatorBenchGraph("chain", 10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dom := computeDominators(graph, nodesByID)
		if !dom.has("n9999") {
			b.Fatal("missing coverage")
		}
	}
}

func BenchmarkComputeDominatorsStar(b *testing.B) {
	graph, nodesByID := dominatorBenchGraph("star", 10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dom := computeDominators(graph, nodesByID)
		if !dom.has("n9999") {
			b.Fatal("missing coverage")
		}
	}
}
