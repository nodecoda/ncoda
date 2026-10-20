package dify

import (
	"reflect"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

// TestRenderFromIRWithAuditMatchesRender locks the audit entry point: the
// rendered bytes must be identical to RenderFromIR and the returned id map
// must be a detached clone keyed by IR node id.
func TestRenderFromIRWithAuditMatchesRender(t *testing.T) {
	call := &ir.CodeRun{ID: "2"}
	graph := &ir.Graph{
		Entry: &ir.Start{ID: "1", Params: []ir.Param{{Name: "url", Type: ir.T("string")}}},
		Root:  &ir.Block{Stmts: []ir.Stmt{&ir.NodeStmt{Node: call}, &ir.ReturnStmt{End: &ir.End{ID: "900001"}}}},
	}
	plain, err := RenderFromIR(graph, "workflow")
	if err != nil {
		t.Fatal(err)
	}
	audited, ids, err := RenderFromIRWithAudit(graph, "workflow")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plain, audited) {
		t.Fatalf("audit render diverged from RenderFromIR:\nplain=%#v\naudited=%#v", plain, audited)
	}
	if ids["1"] != "start_1" {
		t.Fatalf("start id = %q, want start_1 (map %v)", ids["1"], ids)
	}
	if ids["2"] == "" {
		t.Fatalf("code node missing from audit map: %v", ids)
	}
	ids["1"] = "tampered"
	_, again, err := RenderFromIRWithAudit(graph, "workflow")
	if err != nil {
		t.Fatal(err)
	}
	if again["1"] != "start_1" {
		t.Fatalf("audit map is not detached: %q", again["1"])
	}
}
