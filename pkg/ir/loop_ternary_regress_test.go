package ir_test

// 回归（commit 54e3a82 fix(ir): loop body 内三元 varDecl 漏 syntheticTernary
// 保护 → loop_return）:loop body 内出现的三元表达式（cond ? a : b）经 if/else
// 机制降级,两个分支的合成 return 只是"分支值上浮",不是用户函数返回 —— 不得
// 进入 emitLoopReturn（否则生成 LoopReturnNode,coze-biz 渲染器不支持,
// "node kind loop_return"）。此前 emitVarDecl 的 TernaryExpr 路径漏了
// syntheticTernary 保护（emitValueExpr 路径有）;修复后与 emitValueExpr 一致。
//
// 本文件锁定 IR 层:以下每种形态编译后图中都不得出现 LoopReturnNode,且三元
// 的变量归并（Aggregate/Group1）必须真实物化。真正的 loop 提前返回（用户
// return）仍走 emitLoopReturn —— 正向对照见 TestBuildLoopReturn。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

// loopTernaryForms: loop 内三元的五种形态。varDecl 直写 / 复合条件（X4
// content_check 形态）/ if 分支内嵌 / 裸表达式语句 / yield 值位置 —— 覆盖
// emitVarDecl 与 emitValueExpr 两条降级路径。
var loopTernaryForms = map[string]string{
	"varDecl-simple": workflowHeader + `
function main(string item, string fallback) -> string {
    let items = [item];
    for (item in items) {
        let chosen = item != "" ? item : fallback;
        let _d = chosen;
    }
    return "done";
}
`,
	"varDecl-composite": workflowHeader + `
function main(string item, string fallback) -> string {
    let items = [item];
    for (item in items) {
        let checked = item;
        let content = checked != "" ? checked : fallback;
        let _d = content;
    }
    return "done";
}
`,
	"if-branch": workflowHeader + `
function main(string item, string fallback) -> string {
    let items = [item];
    for (item in items) {
        if (item != "") {
            let x = item != "" ? item : fallback;
            let _d = x;
        }
    }
    return "done";
}
`,
	"expr-stmt": workflowHeader + `
function main(string item, string fallback) -> string {
    let items = [item];
    for (item in items) {
        item != "" ? item : fallback;
    }
    return "done";
}
`,
	"value-position": workflowHeader + `
function main(string item, string fallback) -> array<string> {
    let items = [item];
    let out = for (item in items) {
        yield item != "" ? item : fallback;
    };
    return out;
}
`,
}

// TestBuildLoopTernaryNoLoopReturn locks the core regression: no ternary
// form inside a loop body may emit a LoopReturnNode (synthesized branch
// returns are value-floating, not user loop escapes).
func TestBuildLoopTernaryNoLoopReturn(t *testing.T) {
	for name, src := range loopTernaryForms {
		t.Run(name, func(t *testing.T) {
			g := buildIR(t, src)
			if lrs := nodesOf(g)[ir.KindLoopReturn]; len(lrs) != 0 {
				t.Fatalf("%s: loop return nodes = %d, want 0 (synthesized ternary branch returns must not escape the loop)", name, len(lrs))
			}
		})
	}
}

// loopTernaryConsumedForms: 三元的**值真被下游读**的形态 —— 归并必须真实
// 物化（值上浮到归并节点，渲染器才能把 loop 输出 / 后续语句绑到 Group1）。
// 与 loopTernaryForms 的 unread 形态成对：同一条规则（值可观察 ⇒ 物化）
// 的正负两侧，缺一侧就会把「删死脚手架」读成「删活值」。
var loopTernaryConsumedForms = map[string]string{
	// yield 位置：loop 输出源读 Group1（= loopTernaryForms["value-position"]）。
	"value-position": loopTernaryForms["value-position"],
	// 提升变量 keep 在循环外被 return 读：三元的归并是 keep 的值来源。
	"varDecl-consumed": workflowHeader + `
function main(string item, string fallback) -> string {
    string keep;
    keep = "";
    let items = [item];
    for (item in items) {
        let chosen = item != "" ? item : fallback;
        keep = chosen;
    }
    return keep;
}
`,
}

// TestBuildLoopTernaryMaterializesMerge: 值可观察的三元必须真实物化为变量
// 归并（Aggregate/Group1）—— 只断言"能编译"不够,值必须上浮到归并节点。
//
// 反面（2026-09-13 第三族「写而无人读的汇聚 = 死脚手架」）：`let _d = chosen;`
// 这种**绑定本身无人读**的形态，其归并没有任何数据消费者 ⇒ IR 优化阶段照
// 删（判据见 pkg/ir/optimize.go 的汇聚活性规则）。旧版本这里对 unread
// 形态也断言"恰好 1 个归并"，那是把「值被观察」与「值被写」混为一谈——
// 删掉死归并后产物逐字节更小且语义不变（`let _d` 是纯别名，不产生节点，
// 没有引用站点会因此悬空）。
func TestBuildLoopTernaryMaterializesMerge(t *testing.T) {
	for name, src := range loopTernaryConsumedForms {
		t.Run("consumed/"+name, func(t *testing.T) {
			g := buildIR(t, src)
			if aggs := nodesOf(g)[ir.KindAggregate]; len(aggs) != 1 {
				t.Fatalf("%s: aggregate nodes = %d, want 1 (ternary variable-merge)", name, len(aggs))
			}
		})
	}
	for name, src := range loopTernaryForms {
		if name == "expr-stmt" || name == "value-position" {
			// 裸表达式三元被 emitExprStmt 丢弃（纯表达式无副作用），无归并；
			// value-position 的值被 loop 输出源读，归并存活（见上一段）。
			continue
		}
		t.Run("unread/"+name, func(t *testing.T) {
			g := buildIR(t, src)
			if aggs := nodesOf(g)[ir.KindAggregate]; len(aggs) != 0 {
				t.Fatalf("%s: aggregate nodes = %d, want 0 (值无人读 ⇒ 死归并必须删掉；"+
					"留着就是 X23/X88 那类死载体)", name, len(aggs))
			}
		})
	}
}

// TestBuildLoopTernaryYieldBindsLoopOutput: value-position 三元（yield）的
// 归并值必须绑定为 loop 输出,且 loop.ReturnValue 必须保持 nil —— 它是值
// 表达式,不是 loop 提前返回（对照 TestBuildLoopReturn:真实 return 会登记
// ReturnValue）。
func TestBuildLoopTernaryYieldBindsLoopOutput(t *testing.T) {
	g := buildIR(t, loopTernaryForms["value-position"])
	loop := firstOf(t, g, ir.KindLoop).(*ir.Loop)
	if loop.ReturnValue != nil {
		t.Fatalf("loop.ReturnValue = %+v, want nil (ternary is a value, not a loop escape)", loop.ReturnValue)
	}
	// loop 输出端口应绑定到归并 Group1（body 内唯一 aggregate）。
	agg := firstOf(t, g, ir.KindAggregate).(*ir.Aggregate)
	found := false
	for _, os := range loop.OutputSources {
		if os.BlockID == agg.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("loop output sources = %+v, want one bound to aggregate %s (Group1)", loop.OutputSources, agg.ID)
	}
}
