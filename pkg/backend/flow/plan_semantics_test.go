package flow_test

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/obligation"
)

// findConstruct returns the construct with the given id, or nil.
func findConstruct(sem *obligation.Semantics, id string) *obligation.Construct {
	for i := range sem.Constructs {
		if sem.Constructs[i].ID == id {
			return &sem.Constructs[i]
		}
	}
	return nil
}

// findObligation returns the obligation keyed by (selector, port, group).
func findObligation(sem *obligation.Semantics, selector, port, group string) *obligation.Obligation {
	for i := range sem.Obligations {
		o := &sem.Obligations[i]
		if o.Selector == selector && o.Port == port && o.Group == group {
			return o
		}
	}
	return nil
}

func findNode(sem *obligation.Semantics, id string) *obligation.NodeFact {
	for i := range sem.Nodes {
		if sem.Nodes[i].ID == id {
			return &sem.Nodes[i]
		}
	}
	return nil
}

func mustSemantics(t *testing.T, g *ir.Graph) *obligation.Semantics {
	t.Helper()
	sem, err := flow.Semantics(g)
	if err != nil {
		t.Fatalf("Semantics: %v", err)
	}
	return sem
}

// L1: a declared but empty arm must stay declared (Empty=true, no entry) and
// still owe its port obligation, closed on the merge.
func TestSemanticsEmptyArmClosesOnMerge(t *testing.T) {
	sem := mustSemantics(t, graph(
		&ir.IfStmt{
			ID:    "cond",
			Cond:  &ir.Selector{ID: "cond"},
			Then:  &ir.Block{Stmts: []ir.Stmt{stmt("then_a")}},
			Else:  &ir.Block{},
			Merge: &ir.Aggregate{ID: "cond_merge"},
		},
	))

	c := findConstruct(sem, "cond")
	if c == nil {
		t.Fatal("missing if construct")
	}
	if len(c.Arms) != 2 {
		t.Fatalf("arms = %d, want 2", len(c.Arms))
	}
	if c.Arms[0].Empty {
		t.Fatal("then arm must not be empty")
	}
	if !c.Arms[1].Empty {
		t.Fatal("else arm with empty block must be Empty=true")
	}
	if c.Arms[1].Entry != nil {
		t.Fatalf("empty arm entry = %v, want nil", *c.Arms[1].Entry)
	}
	if c.Arms[1].Terminal != "none" {
		t.Fatalf("empty arm terminal = %q, want none", c.Arms[1].Terminal)
	}
	if c.Merge == nil || c.Merge.ID != "cond_merge" {
		t.Fatalf("merge = %+v, want cond_merge", c.Merge)
	}

	for _, port := range []string{"true", "false"} {
		o := findObligation(sem, "cond", port, "Group1")
		if o == nil {
			t.Fatalf("missing obligation (%q,%q)", "cond", port)
		}
		if o.Status != obligation.ObligationClosed {
			t.Fatalf("obligation %q status = %q, want closed", port, o.Status)
		}
		if o.Target != "cond_merge" || o.TargetKind != "merge" {
			t.Fatalf("obligation %q target = (%q,%q), want (cond_merge,merge)", port, o.Target, o.TargetKind)
		}
	}
}

// L1: an else-less if without a merge falls through to the next statement;
// both selector ports land there structurally, so both close on the flow
// target.
func TestSemanticsElseLessIfFallsThrough(t *testing.T) {
	sem := mustSemantics(t, graph(
		&ir.IfStmt{
			ID:   "cond",
			Cond: &ir.Selector{ID: "cond"},
			Then: &ir.Block{Stmts: []ir.Stmt{stmt("then_a")}},
		},
		stmt("after"),
	))

	c := findConstruct(sem, "cond")
	if c == nil {
		t.Fatal("missing if construct")
	}
	if len(c.Arms) != 2 || !c.Arms[1].Empty {
		t.Fatalf("else-less if arms = %+v, want 2 arms with empty else", c.Arms)
	}
	if c.Merge != nil {
		t.Fatalf("merge = %+v, want nil", c.Merge)
	}

	for _, port := range []string{"true", "false"} {
		o := findObligation(sem, "cond", port, "Group1")
		if o == nil {
			t.Fatalf("missing obligation (%q,%q)", "cond", port)
		}
		if o.Status != obligation.ObligationClosed {
			t.Fatalf("obligation %q status = %q, want closed", port, o.Status)
		}
		if o.Target != "after" || o.TargetKind != "flow" {
			t.Fatalf("obligation %q target = (%q,%q), want (after,flow)", port, o.Target, o.TargetKind)
		}
	}
}

// Regression (RDANGLING_REF): a zero-branch ask is a straight-through
// control node. It must expose its own id as the arm exit and must not owe
// any port obligation.
func TestSemanticsZeroArmAskPassesThrough(t *testing.T) {
	sem := mustSemantics(t, graph(
		&ir.IfStmt{
			ID:    "cond",
			Cond:  &ir.Selector{ID: "cond"},
			Then:  &ir.Block{Stmts: []ir.Stmt{&ir.QaStmt{ID: "ask1", QA: &ir.QA{ID: "qa_node"}}}},
			Else:  &ir.Block{Stmts: []ir.Stmt{stmt("else_a")}},
			Merge: &ir.Aggregate{ID: "cond_merge"},
		},
	))

	c := findConstruct(sem, "cond")
	if c == nil {
		t.Fatal("missing if construct")
	}
	if len(c.Arms) != 2 {
		t.Fatalf("arms = %d, want 2", len(c.Arms))
	}
	if len(c.Arms[0].Exits) != 1 || c.Arms[0].Exits[0] != "ask1" {
		t.Fatalf("then arm exits = %v, want [ask1]", c.Arms[0].Exits)
	}
	if c.Arms[0].Terminal != "flow" {
		t.Fatalf("then arm terminal = %q, want flow", c.Arms[0].Terminal)
	}
	for _, o := range sem.Obligations {
		if o.Selector == "ask1" {
			t.Fatalf("zero-arm ask owed an obligation: %+v", o)
		}
	}
	ask := findConstruct(sem, "ask1")
	if ask == nil || ask.Kind != "ask" {
		t.Fatalf("ask construct = %+v, want kind ask", ask)
	}
	if len(ask.Arms) != 0 {
		t.Fatalf("zero-arm ask arms = %d, want 0", len(ask.Arms))
	}
}

// L2: the loop plan records the body entry, its continue back edges, the
// exit target and the body statement ids in source order.
func TestSemanticsLoopPlan(t *testing.T) {
	sem := mustSemantics(t, graph(
		&ir.LoopStmt{
			ID: "loop",
			Body: &ir.Block{Stmts: []ir.Stmt{
				stmt("work"),
				&ir.ContinueStmt{Node: &ir.ContinueNode{ID: "cont"}},
			}},
		},
		stmt("after"),
	))

	c := findConstruct(sem, "loop")
	if c == nil || c.Kind != "loop" {
		t.Fatalf("loop construct = %+v", c)
	}
	if c.Loop == nil {
		t.Fatal("loop construct has no LoopPlan")
	}
	if c.Loop.Entry != "work" {
		t.Fatalf("loop entry = %q, want work", c.Loop.Entry)
	}
	if len(c.Loop.Back) != 1 || c.Loop.Back[0] != "cont" {
		t.Fatalf("loop back = %v, want [cont]", c.Loop.Back)
	}
	if c.Loop.Exit != "after" {
		t.Fatalf("loop exit = %q, want after", c.Loop.Exit)
	}
	if len(c.Loop.Body) != 2 || c.Loop.Body[0] != "work" || c.Loop.Body[1] != "cont" {
		t.Fatalf("loop body = %v, want [work cont]", c.Loop.Body)
	}
}

// A5: statements after a return are unreachable: they keep a NodeFact marked
// Reachable=false and never produce a construct or an obligation.
func TestSemanticsUnreachableAfterReturn(t *testing.T) {
	sem := mustSemantics(t, graph(
		&ir.ReturnStmt{End: &ir.End{ID: "end"}},
		&ir.IfStmt{
			ID:    "dead_if",
			Cond:  &ir.Selector{ID: "dead_if"},
			Then:  &ir.Block{Stmts: []ir.Stmt{stmt("dead_then")}},
			Merge: &ir.Aggregate{ID: "dead_merge"},
		},
		stmt("dead_after"),
	))

	if findConstruct(sem, "dead_if") != nil {
		t.Fatal("unreachable if produced a construct")
	}
	for _, id := range []string{"dead_if", "dead_after", "dead_then"} {
		n := findNode(sem, id)
		if n == nil {
			t.Fatalf("unreachable node %q missing from NodeFacts", id)
		}
		if n.Reachable {
			t.Fatalf("node %q marked reachable", id)
		}
	}
	if n := findNode(sem, "end"); n == nil || !n.Reachable {
		t.Fatalf("return end node = %+v, want reachable", n)
	}
	for _, o := range sem.Obligations {
		if o.Selector == "dead_if" {
			t.Fatalf("unreachable if owed %+v", o)
		}
	}
}

// Merge value closure: two ordered candidates are a phi, not an alias.
func TestSemanticsMergePhiKeepsOrder(t *testing.T) {
	sem := mustSemantics(t, graph(
		&ir.IfStmt{
			ID:   "cond",
			Cond: &ir.Selector{ID: "cond"},
			Then: &ir.Block{Stmts: []ir.Stmt{stmt("then_a")}},
			Else: &ir.Block{Stmts: []ir.Stmt{stmt("else_a")}},
			Merge: &ir.Aggregate{
				ID:      "cond_merge",
				RefName: "answer",
				Refs: []ir.Ref{
					{Producer: "then_a", Output: "output"},
					{Producer: "else_a", Output: "output"},
				},
			},
		},
	))

	c := findConstruct(sem, "cond")
	if c == nil || c.Merge == nil {
		t.Fatal("missing if construct/merge")
	}
	vc, ok := c.Merge.ValueClosure["answer"]
	if !ok {
		t.Fatalf("value closure keys = %v, want answer", c.Merge.ValueClosure)
	}
	if vc.Kind != obligation.ClosurePhi {
		t.Fatalf("closure kind = %q, want phi", vc.Kind)
	}
	if len(vc.Candidates) != 2 ||
		vc.Candidates[0].Producer != "then_a" ||
		vc.Candidates[1].Producer != "else_a" {
		t.Fatalf("candidates = %+v, want [then_a else_a] in order", vc.Candidates)
	}
}

// TC-33 seed: every closed obligation lands on a real structure — a declared
// merge id for target_kind=merge, a known node id for target_kind=flow.
func TestSemanticsObligationTargetsAreInternallyConsistent(t *testing.T) {
	cases := []struct {
		name string
		g    *ir.Graph
	}{
		{
			name: "if_merge",
			g: graph(
				&ir.IfStmt{
					ID:    "cond",
					Cond:  &ir.Selector{ID: "cond"},
					Then:  &ir.Block{Stmts: []ir.Stmt{stmt("then_a")}},
					Else:  &ir.Block{Stmts: []ir.Stmt{stmt("else_a")}},
					Merge: &ir.Aggregate{ID: "cond_merge"},
				},
				stmt("after"),
			),
		},
		{
			name: "attempt_merge",
			g: graph(
				&ir.AttemptStmt{
					ID:      "try",
					Op:      &ir.TextProcess{ID: "op", Method: "const"},
					Success: &ir.Block{Stmts: []ir.Stmt{stmt("ok_a")}},
					Failure: &ir.Block{Stmts: []ir.Stmt{stmt("err_a")}},
					Exit:    &ir.Aggregate{ID: "try_merge"},
				},
				stmt("after"),
			),
		},
		{
			name: "qa_branches",
			g: graph(
				&ir.QaStmt{
					ID: "ask1",
					QA: &ir.QA{ID: "qa_node"},
					Branches: []ir.QaBranch{
						{Port: "branch_0", Body: &ir.Block{Stmts: []ir.Stmt{stmt("b0")}}},
						{Port: "default", Body: &ir.Block{Stmts: []ir.Stmt{stmt("bd")}}},
					},
					Merge: &ir.Aggregate{ID: "ask_merge"},
				},
				stmt("after"),
			),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sem := mustSemantics(t, tc.g)
			nodes := make(map[string]bool, len(sem.Nodes))
			for _, n := range sem.Nodes {
				nodes[n.ID] = true
			}
			merges := make(map[string]bool, len(sem.Constructs))
			for _, c := range sem.Constructs {
				if c.Merge != nil {
					merges[c.Merge.ID] = true
				}
			}
			if len(sem.Obligations) == 0 {
				t.Fatal("expected at least one obligation")
			}
			for _, o := range sem.Obligations {
				if o.Status != obligation.ObligationClosed {
					t.Fatalf("obligation %+v is not closed", o)
				}
				switch o.TargetKind {
				case "merge":
					if !merges[o.Target] {
						t.Fatalf("merge target %q is not a declared merge", o.Target)
					}
				case "flow":
					if !nodes[o.Target] {
						t.Fatalf("flow target %q is not a known node", o.Target)
					}
				default:
					t.Fatalf("unknown target_kind %q in %+v", o.TargetKind, o)
				}
			}
		})
	}
}
