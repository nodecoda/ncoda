package flow_test

import (
	"reflect"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
)

func throwStmt(id string) *ir.NodeStmt {
	return &ir.NodeStmt{Node: &ir.ToolCall{ID: id, ProviderID: "throw_error", Operation: "throw_error"}}
}

// TestProjectThrowErrorAtFunctionEnd locks the ordinary-plugin projection
// contract: when the last statement in an else-less branch is a plugin call
// and the function immediately ends, the call's explicit tail goes to the
// function's End. This is control-flow projection, not name-based semantics.
func TestProjectThrowErrorAtFunctionEnd(t *testing.T) {
	g := &ir.Graph{
		Entry: &ir.Start{ID: "100001", Params: []ir.Param{{Name: "input", Type: ir.T("string")}}},
		Root: &ir.Block{Stmts: []ir.Stmt{
			&ir.IfStmt{
				ID:   "100002",
				Cond: &ir.Selector{ID: "100002"},
				Then: &ir.Block{Stmts: []ir.Stmt{throwStmt("100003")}},
				// Else nil: else-less if — the throw arm terminates at end.
			},
			&ir.ReturnStmt{End: &ir.End{ID: "900001"}},
		}},
	}
	p := mustProject(t, g)
	want := []flow.Edge{
		flow.Edge{SourceNodeID: "100001", TargetNodeID: "100002"},
		flow.Edge{SourceNodeID: "100002", TargetNodeID: "100003", SourcePortID: "true"},
		{SourceNodeID: "100003", TargetNodeID: "900001"},
		flow.Edge{SourceNodeID: "100002", TargetNodeID: "900001", SourcePortID: "false"},
	}
	if !reflect.DeepEqual(p.Edges, want) {
		t.Fatalf("projection edges:\n got: %#v\nwant: %#v", p.Edges, want)
	}
}

// TestProjectPluginCallIntoExplicitBranchMerge: an if-else with a merge whose
// then branch is a plugin call — the call follows the branch's explicit
// convergence into the merge, and the merge then reaches the function End.
// No operation-name shortcut is allowed.
func TestProjectPluginCallIntoExplicitBranchMerge(t *testing.T) {
	g := &ir.Graph{
		Entry: &ir.Start{ID: "100001", Params: []ir.Param{{Name: "input", Type: ir.T("string")}}},
		Root: &ir.Block{Stmts: []ir.Stmt{
			&ir.IfStmt{
				ID:   "100002",
				Cond: &ir.Selector{ID: "100002"},
				Then: &ir.Block{Stmts: []ir.Stmt{throwStmt("100003")}},
				Else: &ir.Block{Stmts: []ir.Stmt{stmt("100004")}},
				Merge: &ir.Aggregate{ID: "100005", Refs: []ir.Ref{
					{Producer: "100004", Output: "output"},
				}},
			},
			&ir.ReturnStmt{End: &ir.End{ID: "900001"}},
		}},
	}
	p := mustProject(t, g)
	want := []flow.Edge{
		flow.Edge{SourceNodeID: "100001", TargetNodeID: "100002"},
		flow.Edge{SourceNodeID: "100002", TargetNodeID: "100003", SourcePortID: "true"},
		flow.Edge{SourceNodeID: "100002", TargetNodeID: "100004", SourcePortID: "false"},
		{SourceNodeID: "100003", TargetNodeID: "100005"},
		{SourceNodeID: "100004", TargetNodeID: "100005"},
		{SourceNodeID: "100005", TargetNodeID: "900001"},
	}
	if !reflect.DeepEqual(p.Edges, want) {
		t.Fatalf("projection edges:\n got: %#v\nwant: %#v", p.Edges, want)
	}
}
