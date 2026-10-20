//go:build poison

package flow_test

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/obligation"
)

// TC-28 C2: under -tags poison every legacy emitter helper panics. The new
// solver must still produce its full semantics; the legacy projection must
// fail loudly, proving the poison is actually active.
func TestPoisonSemanticsIndependent(t *testing.T) {
	g := graph(
		&ir.IfStmt{
			ID:    "cond",
			Cond:  &ir.Selector{ID: "cond"},
			Then:  &ir.Block{Stmts: []ir.Stmt{stmt("then_a")}},
			Else:  &ir.Block{},
			Merge: &ir.Aggregate{ID: "cond_merge"},
		},
		stmt("after"),
	)

	sem, err := flow.Semantics(g)
	if err != nil {
		t.Fatalf("Semantics under poison: %v", err)
	}
	if len(sem.Constructs) != 1 || sem.Constructs[0].ID != "cond" || sem.Constructs[0].Kind != "if" {
		t.Fatalf("constructs = %+v, want one if cond", sem.Constructs)
	}
	if len(sem.Constructs[0].Arms) != 2 || !sem.Constructs[0].Arms[1].Empty {
		t.Fatalf("arms = %+v, want then + empty else", sem.Constructs[0].Arms)
	}
	ports := map[string]bool{}
	for _, o := range sem.Obligations {
		if o.Selector != "cond" {
			continue
		}
		if o.Status != obligation.ObligationClosed || o.Target != "cond_merge" || o.TargetKind != "merge" {
			t.Fatalf("obligation = %+v, want closed on cond_merge", o)
		}
		ports[o.Port] = true
	}
	if !ports["true"] || !ports["false"] {
		t.Fatalf("obligation ports = %v, want true+false", ports)
	}
	if len(sem.Nodes) == 0 {
		t.Fatal("semantics recorded no nodes")
	}

	expectPanic(t, func() {
		_, _ = flow.Project(g)
	})
}

func expectPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic from the poisoned legacy emitter")
		}
	}()
	fn()
}
