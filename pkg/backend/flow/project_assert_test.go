package flow_test

// Structural assertion matrix for flow.Project (WORKFLOW-IR §11.6).
// Each scenario is built from minimal structured IR, independent of any
// backend renderer, and asserts the full projection contract: ordered node
// set, edge set with ports, edge order, and container membership.

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
)

// assertNodeIDs asserts the exact ordered top-level node set.
func assertNodeIDs(t *testing.T, p *flow.Projection, want ...string) {
	t.Helper()
	if len(p.Nodes) != len(want) {
		t.Fatalf("top-level nodes = %v, want %v", nodeIDs(p), want)
	}
	for i, w := range want {
		if p.Nodes[i].NodeID() != w {
			t.Fatalf("node[%d] = %s, want %s (full: %v)", i, p.Nodes[i].NodeID(), w, nodeIDs(p))
		}
	}
}

func nodeIDs(p *flow.Projection) []string {
	ids := make([]string, 0, len(p.Nodes))
	for _, n := range p.Nodes {
		ids = append(ids, n.NodeID())
	}
	return ids
}

func loopByID(t *testing.T, p *flow.Projection, id string) *flow.ProjectedLoop {
	t.Helper()
	for _, n := range p.Nodes {
		if loop, ok := n.(*flow.ProjectedLoop); ok && loop.NodeID() == id {
			return loop
		}
	}
	t.Fatalf("projection has no loop container %q", id)
	return nil
}

func childIDs(loop *flow.ProjectedLoop) []string {
	ids := make([]string, 0, len(loop.Children))
	for _, c := range loop.Children {
		ids = append(ids, c.NodeID())
	}
	return ids
}

// §11.6.a — parallel fan-out/fan-in: entry fans into both branches, both
// branches fan into the following junction consumer, node order is stable.
func TestProjectStructuralParallelFanOutFanIn(t *testing.T) {
	p := mustProject(t, graph(&ir.ParallelStmt{Branches: []*ir.Block{
		{Stmts: []ir.Stmt{stmt("left")}},
		{Stmts: []ir.Stmt{stmt("right")}},
	}}, stmt("after")))

	assertNodeIDs(t, p, "start", "left", "right", "after")
	assertEdges(t, p.Edges, []flow.Edge{
		{SourceNodeID: "start", TargetNodeID: "left"},
		{SourceNodeID: "start", TargetNodeID: "right"},
		{SourceNodeID: "left", TargetNodeID: "after"},
		{SourceNodeID: "right", TargetNodeID: "after"},
	})
}

// §11.6.b — nested if/else: true/false ports on every selector, both
// branches merge into their own aggregates, then into the outer merge.
func TestProjectStructuralNestedIfElseMerge(t *testing.T) {
	inner := ifStmt("inner", &ir.Block{Stmts: []ir.Stmt{stmt("inner_true")}}, &ir.Block{Stmts: []ir.Stmt{stmt("inner_false")}}, "inner_merge")
	outer := ifStmt("outer", &ir.Block{Stmts: []ir.Stmt{inner}}, &ir.Block{Stmts: []ir.Stmt{stmt("outer_false")}}, "outer_merge")
	p := mustProject(t, graph(outer, stmt("after")))

	assertNodeIDs(t, p, "start", "outer", "inner", "inner_true", "inner_false", "inner_merge", "outer_false", "outer_merge", "after")
	assertEdges(t, p.Edges, []flow.Edge{
		{SourceNodeID: "start", TargetNodeID: "outer"},
		{SourceNodeID: "outer", TargetNodeID: "inner", SourcePortID: "true"},
		{SourceNodeID: "inner", TargetNodeID: "inner_true", SourcePortID: "true"},
		{SourceNodeID: "inner", TargetNodeID: "inner_false", SourcePortID: "false"},
		{SourceNodeID: "inner_true", TargetNodeID: "inner_merge"},
		{SourceNodeID: "inner_false", TargetNodeID: "inner_merge"},
		{SourceNodeID: "outer", TargetNodeID: "outer_false", SourcePortID: "false"},
		{SourceNodeID: "inner_merge", TargetNodeID: "outer_merge"},
		{SourceNodeID: "outer_false", TargetNodeID: "outer_merge"},
		{SourceNodeID: "outer_merge", TargetNodeID: "after"},
	})
}

// §11.6.c — loop container boundary: body nodes live inside the container,
// the break escape stays inside, the container connects to its successor.
func TestProjectStructuralLoopContainerBoundary(t *testing.T) {
	loop := &ir.LoopStmt{
		ID: "loop",
		Body: &ir.Block{Stmts: []ir.Stmt{
			stmt("work"),
			&ir.IfStmt{
				ID:   "cond",
				Cond: &ir.Selector{ID: "cond"},
				Then: &ir.Block{Stmts: []ir.Stmt{&ir.BreakStmt{Node: &ir.BreakNode{ID: "break"}}}},
			},
		}},
	}
	p := mustProject(t, graph(loop, stmt("after")))

	// Top level: container replaces its body; work/cond/break are not visible.
	assertNodeIDs(t, p, "start", "loop", "after")
	assertEdges(t, p.Edges, []flow.Edge{
		{SourceNodeID: "start", TargetNodeID: "loop"},
		{SourceNodeID: "loop", TargetNodeID: "after"},
	})

	container := loopByID(t, p, "loop")
	assertChildIDs := func(want ...string) {
		t.Helper()
		got := childIDs(container)
		if len(got) != len(want) {
			t.Fatalf("loop children = %v, want %v", got, want)
		}
		for i, w := range want {
			if got[i] != w {
				t.Fatalf("loop child[%d] = %s, want %s (full: %v)", i, got[i], w, got)
			}
		}
	}
	assertChildIDs("work", "cond", "break")
	assertEdges(t, container.InnerEdges, []flow.Edge{
		{SourceNodeID: "loop", TargetNodeID: "work", SourcePortID: wLoopFunctionInlineOutput},
		{SourceNodeID: "work", TargetNodeID: "cond"},
		{SourceNodeID: "cond", TargetNodeID: "break", SourcePortID: "true"},
		{SourceNodeID: "cond", TargetNodeID: "loop", SourcePortID: "false", TargetPortID: wLoopFunctionInlineInput},
	})
}

// §11.6.d — iteration yield preserves the container boundary: the loop
// output source points at a body node, and the downstream consumer is wired
// from the container.
func TestProjectStructuralIterationYieldBoundary(t *testing.T) {
	loop := &ir.LoopStmt{
		ID:       "iteration",
		LoopType: "by_array",
		OutTypes: map[string]*ir.Type{"output": ir.TList(ir.T("string"))},
		OutputSources: []ir.Source{{
			Path: []string{"output"}, BlockID: "yield_value", Name: "output",
		}},
		Body: &ir.Block{Stmts: []ir.Stmt{&ir.NodeStmt{Node: &ir.TextProcess{
			ID: "yield_value", Method: "concat", Inputs: []ir.Source{{BlockID: "iteration", Name: "item"}},
		}}}},
	}
	p := mustProject(t, graph(loop, stmt("after")))

	assertNodeIDs(t, p, "start", "iteration", "after")
	container := loopByID(t, p, "iteration")
	assertChildIDs := func(want ...string) {
		t.Helper()
		got := childIDs(container)
		if len(got) != len(want) {
			t.Fatalf("loop children = %v, want %v", got, want)
		}
		for i, w := range want {
			if got[i] != w {
				t.Fatalf("loop child[%d] = %s, want %s (full: %v)", i, got[i], w, got)
			}
		}
	}
	assertChildIDs("yield_value")
	assertEdges(t, container.InnerEdges, []flow.Edge{
		{SourceNodeID: "iteration", TargetNodeID: "yield_value", SourcePortID: wLoopFunctionInlineOutput},
		{SourceNodeID: "yield_value", TargetNodeID: "iteration", TargetPortID: wLoopFunctionInlineInput},
	})
}

// §11.6.e — nested loop boundaries: the inner container is a child of the
// outer container only; inner body nodes never leak into the outer body or
// the top level.
func TestProjectStructuralNestedLoopBoundaries(t *testing.T) {
	inner := &ir.LoopStmt{ID: "inner", Body: &ir.Block{Stmts: []ir.Stmt{stmt("deep")}}}
	outer := &ir.LoopStmt{ID: "outer", Body: &ir.Block{Stmts: []ir.Stmt{inner, stmt("outer_tail")}}}
	p := mustProject(t, graph(outer, stmt("after")))

	assertNodeIDs(t, p, "start", "outer", "after")
	outerLoop := loopByID(t, p, "outer")

	// inner is a child of outer, not a top-level node; find it inside.
	var innerFound *flow.ProjectedLoop
	for _, c := range outerLoop.Children {
		if loop, ok := c.(*flow.ProjectedLoop); ok && loop.NodeID() == "inner" {
			innerFound = loop
		}
	}
	if innerFound == nil {
		t.Fatalf("outer children = %v, want inner container + outer_tail", childIDs(outerLoop))
	}
	if got := childIDs(outerLoop); len(got) != 2 || got[0] != "inner" || got[1] != "outer_tail" {
		t.Fatalf("outer children = %v, want [inner outer_tail]", got)
	}
	if got := childIDs(innerFound); len(got) != 1 || got[0] != "deep" {
		t.Fatalf("inner children = %v, want [deep]", got)
	}
	// Inner body edge chain lives in the inner container only.
	assertEdges(t, innerFound.InnerEdges, []flow.Edge{
		{SourceNodeID: "inner", TargetNodeID: "deep", SourcePortID: wLoopFunctionInlineOutput},
		{SourceNodeID: "deep", TargetNodeID: "inner", TargetPortID: wLoopFunctionInlineInput},
	})
	// Outer body: fan-out into inner, then outer_tail loops back.
	assertEdges(t, outerLoop.InnerEdges, []flow.Edge{
		{SourceNodeID: "outer", TargetNodeID: "inner", SourcePortID: wLoopFunctionInlineOutput},
		{SourceNodeID: "inner", TargetNodeID: "outer_tail"},
		{SourceNodeID: "outer_tail", TargetNodeID: "outer", TargetPortID: wLoopFunctionInlineInput},
	})
}
