package ir_test

// flatten 语义锁（2026-09-09）：FlattenChainAggregates 是 coze-biz 编译路径
// 的专属规范化（internal/backend/cozebiz generate() 入口调用），不在共享
// ir.Build 内运行 —— dify/coze-studio 链式聚合是真实 canvas 形状。本测试直接
// 调用导出的 FlattenChainAggregates 锁定其变换：
//   1. 嵌套 if 链式 merge → 最外逃逸点单 merge、组候选 = 叶直引（first_
//      non_zero 保序展开）、内层 if merge-less（只赋值不汇聚）；
//   2. 紧邻 owner-if 的 subscope ReturnStmt 尾值载体被置空（透传 junction，
//      不残留指向已删聚合的 ghost MaterializedProducer —— 07_template 幽灵边
//      实证族）；
//   3. 折叠目标被编译器 shim CodeRun 消费（纯读级联 = collapse/D2 域）→
//      拒绝折叠（链形态保留）。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

func TestFlattenChainAggregatesNestedHoistedScalar(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(string x) -> string {
    string agg;
    agg = "";
    if (x == "a") {
        string msg;
        msg = "";
        agg = msg;
        if (x == "b") {
        } else {
            msg = "err";
            agg = msg;
        }
    } else {
        agg = "outer";
    }
    return agg;
}
`
	g := buildIR(t, src)
	var outer *ir.IfStmt
	for _, stmt := range g.Root.Stmts {
		if candidate, ok := stmt.(*ir.IfStmt); ok {
			outer = candidate
			break
		}
	}
	if outer == nil || outer.Merge == nil {
		t.Fatalf("outer if must have a phi merge pre-flatten: %#v", outer)
	}
	var inner *ir.IfStmt
	for _, stmt := range outer.Then.Stmts {
		if candidate, ok := stmt.(*ir.IfStmt); ok {
			inner = candidate
			break
		}
	}
	if inner == nil || inner.Merge == nil {
		t.Fatalf("pre-flatten inner if must own a chain merge: %#v", outer.Then)
	}
	innerMergeID := inner.Merge.ID
	ir.FlattenChainAggregates(g)
	if inner.Merge != nil {
		t.Fatalf("post-flatten inner if must be merge-less (只赋值不汇聚): %#v", inner)
	}
	group := outer.Merge.Groups[0]
	if len(group.Refs) != 3 {
		t.Fatalf("post-flatten outer group refs = %d, want 3 leaf direct refs (链折平): %#v", len(group.Refs), group)
	}
	for _, r := range group.Refs {
		if r.Producer == innerMergeID || r.Producer == outer.Merge.ID {
			t.Fatalf("post-flatten candidate must be a leaf direct ref, got aggregate %s", r.Producer)
		}
	}
}

// TestFlattenDeclinesShimConsumedMerge locks the interpretation boundary: when
// the fold target's output is re-read by a compiler shim CodeRun (pure-read
// cascade — collapse/D2 domain), flatten declines so the YAML-level collapse
// pass still sees the chain it folds into the consumer boundary.
func TestFlattenDeclinesShimConsumedMerge(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(string a) -> string {
    string e_voice;
    e_voice = "";
    string e_parse;
    e_parse = "";
    if (a == "ok") {
        e_parse = "err-parse";
    } else {
        e_voice = "err-voice";
    }
    let msg = e_parse ?? e_voice;
    return msg;
}
`
	g := buildIR(t, src)
	var outer *ir.IfStmt
	for _, stmt := range g.Root.Stmts {
		if candidate, ok := stmt.(*ir.IfStmt); ok {
			outer = candidate
			break
		}
	}
	if outer == nil || outer.Merge == nil {
		t.Fatalf("outer if must have a phi merge: %#v", outer)
	}
	// the ?? chain must have materialized a synthetic shim consuming the merge
	shimReadsMerge := false
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			if ns, ok := s.(*ir.NodeStmt); ok {
				if cr, ok := ns.Node.(*ir.CodeRun); ok {
					for _, in := range cr.Inputs {
						if in.BlockID == outer.Merge.ID {
							shimReadsMerge = true
						}
					}
				}
			}
			if v, ok := s.(*ir.IfStmt); ok {
				walk(v.Then)
				walk(v.Else)
			}
		}
	}
	walk(g.Root)
	if !shimReadsMerge {
		t.Fatalf("precondition: ?? chain must read the merge output via a shim CodeRun")
	}
	ir.FlattenChainAggregates(g)
	if outer.Merge == nil {
		t.Fatalf("flatten must decline when the fold target is shim-consumed (chain preserved)")
	}
}
