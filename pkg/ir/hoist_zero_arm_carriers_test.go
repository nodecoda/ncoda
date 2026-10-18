package ir_test

// hoist_zero_arm_carriers_test.go — direct unit lock for P28
// (`ir.HoistZeroArmCarriers`), restored after the O2 split.
//
// Why this exists in the open core: the only pre-split test for the rule lived
// in the commercial compiler (`internal/compiler/...`). The rule itself is open
// (`pkg/ir`), so the open repository must be able to prove it independently.
//
//@feature hoist-zero-default — 臂内可证恒零载体上提到宿主之前（「臂写了零值」与「臂什么都没写」在 wire 上同一形状）

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

func zeroShim(id string) *ir.CodeRun {
	cr := &ir.CodeRun{
		ID: id, Code: "# ncoda:shim 1\ndef main() -> dict:\n    return {\"result\": \"\"}",
		Language: "python", Synthetic: true,
		OutTypes: map[string]*ir.Type{"result": ir.T("string")}, OutOrder: []string{"result"},
	}
	cr.ZeroOf = ir.T("string")
	return cr
}

func hoistHost(zero *ir.CodeRun, then *ir.Block) *ir.IfStmt {
	return &ir.IfStmt{
		ID: "100004", Cond: &ir.Selector{ID: "100004"}, Then: then, Else: &ir.Block{},
		Merge: &ir.Aggregate{ID: "100010", Groups: []ir.AggregateGroup{{
			Name: "a", Refs: []ir.Ref{{Producer: zero.ID, Output: "result"}}, Type: ir.T("string"),
		}}},
	}
}

func firstNodeID(stmts []ir.Stmt) string {
	if len(stmts) == 0 {
		return ""
	}
	if ns, ok := stmts[0].(*ir.NodeStmt); ok && ns.Node != nil {
		return ns.Node.NodeID()
	}
	return ""
}

func TestHoistZeroArmCarriersMovesArmCarrierBeforeHost(t *testing.T) {
	zero := zeroShim("100002")
	host := hoistHost(zero, &ir.Block{Stmts: []ir.Stmt{&ir.NodeStmt{Node: zero}}})
	g := &ir.Graph{Root: &ir.Block{Stmts: []ir.Stmt{host}}}

	if !ir.HoistZeroArmCarriers(g) {
		t.Fatal("rule did not fire on an arm-local zero carrier")
	}
	if got := firstNodeID(g.Root.Stmts); got != "100002" {
		t.Fatalf("carrier not hoisted before host: first=%q stmts=%d", got, len(g.Root.Stmts))
	}
	if len(host.Then.Stmts) != 0 {
		t.Fatalf("arm still holds statements after hoist: %#v", host.Then.Stmts)
	}
	if got := host.Merge.Groups[0].Refs[0].Producer; got != "100002" {
		t.Fatalf("candidate ref changed (first_non_zero order lost): %q", got)
	}
}

func TestHoistZeroArmCarriersCrossesNestedHosts(t *testing.T) {
	zero := zeroShim("100002")
	inner := hoistHost(zero, &ir.Block{Stmts: []ir.Stmt{&ir.NodeStmt{Node: zero}}})
	outer := hoistHost(zero, &ir.Block{Stmts: []ir.Stmt{inner}})
	g := &ir.Graph{Root: &ir.Block{Stmts: []ir.Stmt{outer}}}

	if !ir.HoistZeroArmCarriers(g) {
		t.Fatal("rule did not fire through nested arm blocks")
	}
	if got := firstNodeID(g.Root.Stmts); got != "100002" {
		t.Fatalf("nested carrier must reach the outermost host entry, first=%q", got)
	}
	if len(inner.Then.Stmts) != 0 {
		t.Fatalf("nested arm still holds the carrier: %#v", inner.Then.Stmts)
	}
}

func TestHoistZeroArmCarriersRejectsCarrierWithInputs(t *testing.T) {
	zero := zeroShim("100002")
	zero.Inputs = []ir.Source{{Path: []string{"arg1"}, BlockID: "100099", Name: "result"}}
	host := hoistHost(zero, &ir.Block{Stmts: []ir.Stmt{&ir.NodeStmt{Node: zero}}})
	g := &ir.Graph{Root: &ir.Block{Stmts: []ir.Stmt{host}}}

	if ir.HoistZeroArmCarriers(g) {
		t.Fatal("a shim with inputs carries a real arm value and must not be hoisted")
	}
	if len(host.Then.Stmts) != 1 {
		t.Fatalf("arm mutated on a rejected carrier: %#v", host.Then.Stmts)
	}
}

func TestHoistZeroArmCarriersDoesNotCrossLoopBodies(t *testing.T) {
	zero := zeroShim("100002")
	loop := &ir.LoopStmt{Body: &ir.Block{Stmts: []ir.Stmt{&ir.NodeStmt{Node: zero}}}}
	host := hoistHost(zero, &ir.Block{Stmts: []ir.Stmt{loop}})
	g := &ir.Graph{Root: &ir.Block{Stmts: []ir.Stmt{host}}}

	if ir.HoistZeroArmCarriers(g) {
		t.Fatal("loop bodies are not arm gates; the carrier must stay inside")
	}
	if len(loop.Body.Stmts) != 1 {
		t.Fatalf("loop body mutated: %#v", loop.Body.Stmts)
	}
}

func TestHoistZeroArmCarriersNilGraph(t *testing.T) {
	if ir.HoistZeroArmCarriers(nil) {
		t.Fatal("nil graph must be a no-op")
	}
}
