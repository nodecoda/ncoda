package analyzer

// Performance regression guard for buildSymbolTables (Task 45 P2/P3).
//
// Before the P2 fix the reference copies every ancestor's FULL symbol
// table into each node's table — O(n^3) on long chains (an 800-node chain
// took 5.97s). P2 precomputed each node's own outputs and built each
// table as the union of ancestor own-output sets (identical content,
// O(sum |ancestors|) total, still O(n^2) table memory). P3 removed the
// materialized union entirely: each table keeps only its own outputs
// plus an ancestor bitset (O(n^2/64) memory) and resolves any reference
// in O(1) — chain 5000 nodes: 26.3s/3GB → 0.87s/113MB; star 10000 (a
// chain whose leaves all read the root, the worst case for a lazy
// walk): 12.1s → 2.0s; chain 20000: 1.3s/193MB. This benchmark pins
// the 800-node-chain cost so a future quadratic regression is visible:
//
//	go test -bench=BenchmarkBuildSymbolTables -benchtime=1x ./internal/backend/dify/analyzer/
//
// Expected on the dev box (post-P3): ~1-3ms for 800 nodes.

import (
	"fmt"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
)

func chainAST(n int) *dify.WorkflowAST {
	g := newGB()
	g.node("start_1", "start", &dify.StartDataNode{})
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("code_%d", i+2)
		g.node(id, "code", &dify.CodeDataNode{
			Outputs: map[string]dify.OutputDefNode{
				"result": {Type: "string"},
			},
		})
		if i == 0 {
			g.edge(fmt.Sprintf("edge_%d", i+1), "start_1", id)
		} else {
			g.edge(fmt.Sprintf("edge_%d", i+1), fmt.Sprintf("code_%d", i+1), id)
		}
	}
	return g.ast
}

func BenchmarkBuildSymbolTables(b *testing.B) {
	ast := chainAST(800)
	nodeIDs := make([]string, 0, len(ast.Graph.Nodes))
	for i := range ast.Graph.Nodes {
		nodeIDs = append(nodeIDs, ast.Graph.Nodes[i].ID)
	}
	edges := make([][2]string, 0, len(ast.Graph.Edges))
	for _, e := range ast.Graph.Edges {
		edges = append(edges, [2]string{e.Source, e.Target})
	}
	fg := buildFlowGraph(nodeIDs, edges, "start_1", map[string]struct{}{})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tables := buildSymbolTables(ast, fg)
		if len(tables) != 801 {
			b.Fatalf("tables = %d, want 801", len(tables))
		}
	}
}
