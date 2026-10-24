package spec

// elseLessJunctionMerge 单元测试:else-less if 中段汇合物化为显式 merge。
//
// 形状（S147/X245 语料实证）:
//
//	S --true--> A --> ... --> X          S --false--> T
//	X --> T（普通边,then 分支尾进入续节点）
//
// T 因此成为 S 的直接分支目标 —— runtime 分支门数据隔离使 T 的 refs 拿不到
// then 分支产物（S147:code 节点全 nil）且 X→T 被 drop（X245:true 路径
// dead-end）。重写后: X→M、S--false-->M、M→T,T 变普通节点。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
)

func TestElseLessJunctionMergeMaterializesMerge(t *testing.T) {
	nodes := []ir.Node{
		&ir.Selector{ID: "S", Conditions: []ir.Condition{{Operator: 2}}},
		&ir.LLMCall{ID: "A"},
		&ir.ToolCall{ID: "X"},
		&ir.CodeRun{ID: "T"},
	}
	edges := []flow.Edge{
		{SourceNodeID: "start", TargetNodeID: "S"},
		{SourceNodeID: "S", TargetNodeID: "A", SourcePortID: "true"},
		{SourceNodeID: "A", TargetNodeID: "X"},
		{SourceNodeID: "S", TargetNodeID: "T", SourcePortID: "false"},
		{SourceNodeID: "X", TargetNodeID: "T"},
	}
	gotNodes, out, err := elseLessJunctionMerge(nodes, edges)
	if err != nil {
		t.Fatalf("elseLessJunctionMerge: %v", err)
	}
	// M 出现且入边正确
	want := map[string]bool{
		"start\x00S":            true,
		"S\x00A":                true,
		"A\x00X":                true,
		"S\x00elseless_merge_S": true,
		"X\x00elseless_merge_S": true,
		"elseless_merge_S\x00T": true,
	}
	got := map[string]bool{}
	for _, e := range out {
		got[e.SourceNodeID+"\x00"+e.TargetNodeID] = true
	}
	if len(got) != len(want) {
		t.Fatalf("edge count = %d, want %d\ngot=%v\nwant=%v", len(got), len(want), got, want)
	}
	for k := range want {
		if !got[k] {
			t.Fatalf("missing edge %q\ngot=%v", k, got)
		}
	}
	// 端口:false 端口边指向 M;X→M 与 M→T 无端口。
	for _, e := range out {
		switch {
		case e.SourceNodeID == "S" && e.TargetNodeID == "elseless_merge_S":
			if e.SourcePortID != "false" {
				t.Fatalf("S→M port = %q, want false", e.SourcePortID)
			}
		case e.SourceNodeID == "X" && e.TargetNodeID == "elseless_merge_S":
			if e.SourcePortID != "" {
				t.Fatalf("X→M must be plain, got port %q", e.SourcePortID)
			}
		case e.SourceNodeID == "elseless_merge_S":
			if e.SourcePortID != "" {
				t.Fatalf("M→T must be plain, got port %q", e.SourcePortID)
			}
		}
	}
	// M 已追加到节点列表
	var aggFound bool
	for _, n := range gotNodes {
		if n.NodeID() == "elseless_merge_S" {
			if _, ok := n.(*ir.Aggregate); !ok {
				t.Fatalf("elseless_merge_S is %T, want *ir.Aggregate", n)
			}
			aggFound = true
		}
	}
	if !aggFound {
		t.Fatal("merge node not appended to nodes")
	}
}

// with-else 的 merge 形状（S--true-->A / S--false-->B / A→M / B→M）不触发
// 重写:false 端口目标已是 merge,且 T 无来自分支区域的普通入边。
func TestElseLessJunctionMergeSkipsWithElse(t *testing.T) {
	nodes := []ir.Node{
		&ir.Selector{ID: "S", Conditions: []ir.Condition{{Operator: 2}}},
		&ir.LLMCall{ID: "A"},
		&ir.LLMCall{ID: "B"},
		&ir.Aggregate{ID: "M"},
		&ir.CodeRun{ID: "T"},
	}
	edges := []flow.Edge{
		{SourceNodeID: "start", TargetNodeID: "S"},
		{SourceNodeID: "S", TargetNodeID: "A", SourcePortID: "true"},
		{SourceNodeID: "S", TargetNodeID: "B", SourcePortID: "false"},
		{SourceNodeID: "A", TargetNodeID: "M"},
		{SourceNodeID: "B", TargetNodeID: "M"},
		{SourceNodeID: "M", TargetNodeID: "T"},
	}
	_, out, err := elseLessJunctionMerge(nodes, edges)
	if err != nil {
		t.Fatalf("elseLessJunctionMerge: %v", err)
	}
	if len(out) != len(edges) {
		t.Fatalf("with-else shape must be unchanged: got %d edges, want %d", len(out), len(edges))
	}
}
