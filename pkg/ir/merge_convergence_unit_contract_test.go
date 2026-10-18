package ir_test

// merge 汇聚单元契约（2026-09-08，TDD RED 锁）：
// 完整翻译算法设计（docs/ideas-todo/2026-09-08-merge-convergence-complete-algorithm.md）
// 的单元不变量 —— 同一 if 交汇点上，record 形与标量形逃逸 hoisted 变量各得一个
// 独立汇聚单元（组名 = 字段/变量名）；任何一类的规划不得抑制另一类的通道。
//
// family C（S130）根因：emitIf 的 `len(agg.Groups)==0` 互斥 gate 在 record
// 字段组独占 Aggregate 后掐死 scalarPhiGroups —— 标量逃逸变量 agg 无汇聚组，
// varToOutput 滞留 if 前零值，真分支 producer 变孤儿。V1/V2 = 同交汇混合的
// 两个镜像朝向；V6 = 嵌套一层递归自洽。修复前全红。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

// collectAggregates 收集结构树中的全部 *ir.Aggregate（IfStmt.Merge 与链式
// NodeStmt 里的独立汇聚节点）。
func collectAggregates(t *testing.T, root *ir.Block) []*ir.Aggregate {
	t.Helper()
	var out []*ir.Aggregate
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, st := range b.Stmts {
			switch v := st.(type) {
			case *ir.IfStmt:
				if v.Merge != nil {
					out = append(out, v.Merge)
				}
				walk(v.Then)
				walk(v.Else)
			case *ir.NodeStmt:
				if a, ok := v.Node.(*ir.Aggregate); ok {
					out = append(out, a)
				}
			case *ir.LoopStmt:
				walk(v.Body)
			case *ir.ParallelStmt:
				for _, blk := range v.Branches {
					walk(blk)
				}
			case *ir.AttemptStmt:
				walk(v.Success)
				walk(v.Failure)
			}
		}
	}
	walk(root)
	return out
}

func groupsSummary(as []*ir.Aggregate) []string {
	out := []string{}
	for _, a := range as {
		names := []string{}
		for _, g := range a.Groups {
			names = append(names, g.Name)
		}
		if len(names) == 0 {
			names = append(names, "(Refs legacy)")
		}
		out = append(out, "Aggregate{"+joinComma(names)+"}")
	}
	return out
}

func joinComma(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ","
		}
		out += x
	}
	return out
}

func groupNames(a *ir.Aggregate) map[string]bool {
	out := map[string]bool{}
	for _, g := range a.Groups {
		out[g.Name] = true
	}
	return out
}

func anyAggregateHasGroup(as []*ir.Aggregate, name string) bool {
	for _, a := range as {
		if groupNames(a)[name] {
			return true
		}
	}
	return false
}

// 迁移记录（2026-09-24，记录整值折叠 R-43 §42.3 A 轴）：本文件三个夹具的 RT1
// 都是**单叶字段**记录且整值语义不可观察（只成员读 + 只字面量写）⇒ 折叠把绑定
// 折成**叶单元**，组名按 C 轴档位取**源变量名**（`rec`）而不是字段名（`data`）。
// 三个断言的**对象**（record 单元与标量单元必须并存、候选按臂齐全）逐字不变。
// V1：THEN 尾 = hoisted 标量写（新 producer 成员路径），ELSE 尾 = hoisted
// record 写；标量双臂都写、record 只 ELSE 写 —— family C/S130 正向。
// 断言：record 字段组 data 与标量逐变量组 agg 必须同时存在（各自单元），
// 标量组候选 ≥ 2（双臂真 producer），record 组候选含 ELSE 臂 producer。
func TestBuildMixedScalarThenTailRecordElseTailConverges(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
type RT1 = {
  string data;
};
type RT2 = {
  string a;
  string b;
};
function main(bool cond) -> RT2 {
    string agg;
    agg = "";
    RT1 rec;
    rec = { "data": "" };
    if (cond) {
        RT1 v2;
        v2 = { "data": "then-val" };
        agg = v2.data;
    } else {
        agg = "";
        rec = { "data": "else-val" };
    }
    return { "a": agg, "b": rec.data };
}
`
	g := buildIR(t, src)
	as := collectAggregates(t, g.Root)
	if !anyAggregateHasGroup(as, "rec") {
		t.Fatalf("record convergence unit missing: no aggregate group %q among %v", "rec", as)
	}
	if !anyAggregateHasGroup(as, "agg") {
		t.Fatalf("scalar convergence unit missing: hoisted scalar agg lost its per-variable phi group (emitIf mutual-exclusion gate); aggregates = %v", groupsSummary(as))
	}
	// 标量组双臂候选齐全 + 组端口可被下游解析（语义保真断言）。
	for _, a := range as {
		for _, g := range a.Groups {
			if g.Name == "agg" {
				if len(g.Refs) < 2 {
					t.Fatalf("scalar unit group agg must carry both branch producers, got %d refs: %#v", len(g.Refs), g)
				}
			}
			if g.Name == "rec" {
				if len(g.Refs) < 1 {
					t.Fatalf("record unit group rec must carry the else-arm producer: %#v", g)
				}
			}
		}
	}
}

// V2：镜像 —— THEN 尾 = record 写，ELSE 尾 = hoisted 标量写（record 双臂都写、
// 标量只 ELSE 尾）。同一条单元不变量必须双向成立。
func TestBuildMixedRecordThenTailScalarElseTailConverges(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
type RT1 = {
  string data;
};
type RT2 = {
  string a;
  string b;
};
function main(bool cond) -> RT2 {
    string agg;
    agg = "";
    RT1 rec;
    rec = { "data": "" };
    if (cond) {
        rec = { "data": "then-val" };
        agg = "then-scalar";
    } else {
        agg = "else-scalar";
        rec = { "data": "else-val" };
    }
    return { "a": agg, "b": rec.data };
}
`
	g := buildIR(t, src)
	as := collectAggregates(t, g.Root)
	if !anyAggregateHasGroup(as, "rec") {
		t.Fatalf("record convergence unit missing: no aggregate group %q", "rec")
	}
	if !anyAggregateHasGroup(as, "agg") {
		t.Fatalf("scalar convergence unit missing in mirror orientation (THEN=record tail / ELSE=scalar tail); aggregates = %v", groupsSummary(as))
	}
}

// V6：递归自洽 —— V1 混合形态嵌进外层 if 的一臂（一层嵌套），内层交汇点的
// record + 标量单元必须同样并存（规则无深度守卫）。
func TestBuildNestedMixedConvergenceUnitsRecursive(t *testing.T) {
	// V6：递归自洽 —— V1 混合形态整体嵌进外层 if 的 then 臂（rec/agg 为分支内
	// 局部、内层 if 处提升），内层交汇点的 record + 标量单元必须同样并存。
	// 规则不得有深度守卫：单层成立的形态在嵌套/容器内同一规则仍成立。
	src := `@language nodecoda/1
@mode workflow
type RT1 = {
  string data;
};
type RT2 = {
  string a;
  string b;
};
function main(bool c1, bool c2) -> RT2 {
    if (c1) {
        string agg;
        agg = "";
        RT1 rec;
        rec = { "data": "" };
        if (c2) {
            RT1 v2;
            v2 = { "data": "inner-then" };
            agg = v2.data;
        } else {
            agg = "";
            rec = { "data": "inner-else" };
        }
        return { "a": agg, "b": rec.data };
    }
    return { "a": "", "b": "" };
}
`
	g := buildIR(t, src)
	as := collectAggregates(t, g.Root)
	if !anyAggregateHasGroup(as, "rec") {
		t.Fatalf("nested record convergence unit missing (V1 shape inside outer branch, group name = source variable `rec`): %v", groupsSummary(as))
	}
	if !anyAggregateHasGroup(as, "agg") {
		t.Fatalf("nested scalar convergence unit missing (V1 shape inside outer branch): %v", groupsSummary(as))
	}
}
