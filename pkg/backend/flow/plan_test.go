package flow_test

import (
	"encoding/json"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/obligation"
)

// P1 behavior neutrality: Plan + ProjectFromPlan must reproduce Project
// exactly (same nodes, same edges, same order) for every graph shape.
func TestPlanProjectFromPlanMatchesProject(t *testing.T) {
	cases := []struct {
		name string
		g    *ir.Graph
	}{
		{
			name: "sequential",
			g:    graph(stmt("a"), stmt("b")),
		},
		{
			name: "if_else_merge",
			g: graph(
				ifStmt("outer",
					&ir.Block{Stmts: []ir.Stmt{stmt("then_a")}},
					&ir.Block{Stmts: []ir.Stmt{stmt("else_a")}},
					"outer_merge"),
				stmt("after"),
			),
		},
		{
			name: "nested_if_parallel",
			g: graph(
				&ir.ParallelStmt{Branches: []*ir.Block{
					{Stmts: []ir.Stmt{stmt("left")}},
					{Stmts: []ir.Stmt{stmt("right")}},
				}},
				ifStmt("cond",
					&ir.Block{Stmts: []ir.Stmt{stmt("yes")}},
					&ir.Block{Stmts: []ir.Stmt{stmt("no")}},
					"cond_merge"),
				stmt("after"),
			),
		},
		{
			name: "loop_container",
			g: graph(
				&ir.LoopStmt{
					ID:   "loop",
					Body: &ir.Block{Stmts: []ir.Stmt{stmt("work"), stmt("yield")}},
				},
				stmt("after"),
			),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := flow.Project(tc.g)
			if err != nil {
				t.Fatalf("Project: %v", err)
			}
			bundle, err := flow.Plan(tc.g)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			got, err := flow.ProjectFromPlan(bundle)
			if err != nil {
				t.Fatalf("ProjectFromPlan: %v", err)
			}
			if len(got.Nodes) != len(want.Nodes) || !sameNodes(got.Nodes, want.Nodes) {
				t.Fatalf("nodes differ:\n got %s\nwant %s", nodesJSON(got), nodesJSON(want))
			}
			if !sameEdges(got.Edges, want.Edges) {
				t.Fatalf("edges differ:\n got %#v\nwant %#v", got.Edges, want.Edges)
			}
			if bundle.Semantics == nil {
				t.Fatal("Plan returned nil Semantics")
			}
		})
	}
}

func TestPlanSemanticsIsSerializable(t *testing.T) {
	bundle, err := flow.Plan(graph(
		ifStmt("outer",
			&ir.Block{Stmts: []ir.Stmt{stmt("then_a")}},
			nil,
			"outer_merge"),
	))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if _, err := json.Marshal(bundle.Semantics); err != nil {
		t.Fatalf("marshal semantics: %v", err)
	}
	var doc obligation.Plan
	doc.Schema = obligation.PlanSchemaV2
	doc.Semantics = bundle.Semantics
	full, err := json.Marshal(doc)
	if err != nil || len(full) == 0 {
		t.Fatalf("marshal plan doc: %v", err)
	}
	var round obligation.Plan
	if err := json.Unmarshal(full, &round); err != nil {
		t.Fatalf("unmarshal plan doc: %v", err)
	}
	if round.Schema != obligation.PlanSchemaV2 || round.Semantics == nil {
		t.Fatalf("roundtrip lost plan contract: %+v", round)
	}
	if len(round.Semantics.Constructs) != 1 || round.Semantics.Constructs[0].Kind != "if" {
		t.Fatalf("constructs = %+v, want one if", round.Semantics.Constructs)
	}
	// A merge + an empty else arm produce the schema's obligation fact.
	if len(round.Semantics.Obligations) == 0 {
		t.Fatal("else-less if should record an arm obligation")
	}
}

func TestPlanIsDeterministic(t *testing.T) {
	g := graph(
		ifStmt("cond",
			&ir.Block{Stmts: []ir.Stmt{stmt("yes")}},
			&ir.Block{Stmts: []ir.Stmt{stmt("no")}},
			"cond_merge"),
		stmt("after"),
	)
	first, err := flow.Plan(g)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	second, err := flow.Plan(g)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	a, _ := json.Marshal(first.Semantics)
	b, _ := json.Marshal(second.Semantics)
	if string(a) != string(b) {
		t.Fatalf("Plan semantics are not deterministic:\nfirst  %s\nsecond %s", a, b)
	}
}

func TestPlanRejectsNilGraph(t *testing.T) {
	if _, err := flow.Plan(nil); err == nil {
		t.Fatal("Plan(nil) should error")
	}
	if _, err := flow.ProjectFromPlan(nil); err == nil {
		t.Fatal("ProjectFromPlan(nil) should error")
	}
}

func TestSemanticsRejectsNilGraph(t *testing.T) {
	if _, err := flow.Semantics(nil); err == nil {
		t.Fatal("Semantics(nil) should error")
	}
	if _, err := flow.Semantics(&ir.Graph{}); err == nil {
		t.Fatal("Semantics with nil root should error")
	}
}

func TestProjectionGraphView(t *testing.T) {
	p, err := flow.Project(graph(stmt("a"), stmt("b")))
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	view := p.GraphView()
	if got := view.NodeIDs(); len(got) != len(p.Nodes) {
		t.Fatalf("GraphView NodeIDs = %v, want %d ids", got, len(p.Nodes))
	}
	if got := view.Edges(); len(got) != len(p.Edges) {
		t.Fatalf("GraphView Edges = %d, want %d", len(got), len(p.Edges))
	}
}
