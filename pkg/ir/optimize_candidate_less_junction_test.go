package ir_test

//@feature control-flow — 零候选 junction 与活汇聚（R7-a，2026-09-11）

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

// ─────────────────────────────────────────────────────────────
// R7-a 契约：**零候选 junction**（既无 phi 候选又无数据消费者的
// `Aggregate`）不得落入 lowering。
//
// 为什么它是死脚手架而不是结构标记：
//   - 真实平台 wire 从不含零候选 merge —— 350 篇语料中 142 个
//     variable_merge 全部至少有一个候选；仅有的 5 例零候选出现在我们
//     自己生成的产物里（可辨认的合成坐标 x = row*340, y = 0）；
//   - raising 对该节点零依赖（同一份 wire 摘掉后反编译输出逐字节相同）；
//   - 两个渲染器都已有 merge-less 投影路径（臂尾与**显式分支端口**改接
//     续点 / 容器回环端口），所以删除后控制流仍完整（P4）。
//
// 物化它使 compile∘raise 在 **wire 层不幂等**：第一轮渲染出它、raising
// 忽略它、第二轮 compile 再生成它（s08：w1 4 节点 → w2 5 节点）。
//
// 规则通用无例外：顶层 / 循环体 / inline 函数体同一判据；**候选仍在的
// 汇聚一律保留**（递归自洽：删除判据按真实消费点，不按形态）。
// ─────────────────────────────────────────────────────────────

const candidateLessTopLevelSrc = `@language nodecoda/1
@mode workflow
function main(int v = 1) -> string {
    if (v == 1) { return "a"; } else if (v > 4) { return "b"; }
    return "c";
}
`

// TestOptimizeDropsCandidateLessJunctionAtTopLevel：R7-a 判据按**候选**而不是
// 形态 —— 任何零候选 junction 都不得落入 lowering；带真实候选的汇聚一律保留。
//
// 2026-09-12 更新（带值多出口汇聚规则）：两臂返回**值**时该 if 现在有活汇聚
// （臂值 = 汇聚候选，2026-09-12 用户拍板「带值出口必须经 merge 到 end」），
// 所以「外层 Merge == nil」不再是判据；取代它的是下面这条更严格的、与形态
// 无关的不变量：**图里不允许存在零候选 Aggregate**。
func TestOptimizeDropsCandidateLessJunctionAtTopLevel(t *testing.T) {
	g := buildIR(t, candidateLessTopLevelSrc)
	outer := firstIf(t, g.Root)
	if outer.Merge != nil && len(outer.Merge.Refs) == 0 && len(outer.Merge.Groups) == 0 {
		t.Fatalf("candidate-less junction reached lowering: %#v", outer.Merge)
	}
	// 无条件判据：整张图不得留零候选聚合（顶层 / 循环体 / inline 同一规则，
	// 由本文件其余用例覆盖）。
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			switch st := s.(type) {
			case *ir.IfStmt:
				if st.Merge != nil && len(st.Merge.Refs) == 0 && len(st.Merge.Groups) == 0 {
					t.Errorf("top-level if kept a candidate-less junction: %#v", st.Merge)
				}
				walk(st.Then)
				walk(st.Else)
			case *ir.SwitchStmt:
				if st.Merge != nil && len(st.Merge.Refs) == 0 && len(st.Merge.Groups) == 0 {
					t.Errorf("switch kept a candidate-less junction: %#v", st.Merge)
				}
				for _, br := range st.Branches {
					walk(br.Body)
				}
			case *ir.AttemptStmt:
				if st.Exit != nil && len(st.Exit.Refs) == 0 && len(st.Exit.Groups) == 0 {
					t.Errorf("attempt kept a candidate-less junction: %#v", st.Exit)
				}
				walk(st.Success)
				walk(st.Failure)
			case *ir.QaStmt:
				if st.Merge != nil && len(st.Merge.Refs) == 0 && len(st.Merge.Groups) == 0 {
					t.Errorf("QA kept a candidate-less junction: %#v", st.Merge)
				}
				for _, br := range st.Branches {
					walk(br.Body)
				}
			}
		}
	}
	walk(g.Root)
	// 存活侧判据（2026-09-15 判据更正，A12-H12 同片）：else-if 链的三个出口
	// （"a" / "b" / "c"）必须**一个不少地**收口于一个汇聚单元。
	//
	// 旧写法断言 `inner.Merge != nil`（内层 if 自带的树内 junction）—— 那正是
	// 本族要删的脚手架：两条臂都终止本流程的 if 没有续接点、没有值可 phi，
	// 规范形里**不存在**该单元（与显式 `else { return …; }` 同形）。判据改按
	// **规则**写（删过头 = 出口丢失），不按形态写 —— 后者会把脚手架固化成契约。
	end := flowExitEnd(t, g)
	if end.FlowExitMerge == nil {
		t.Fatal("live convergence (candidate-carrying exits) was pruned — deletion criterion is over-broad")
	}
	if got := len(end.FlowExitMerge.AllRefs()); got != 3 {
		t.Fatalf("flow-exit convergence candidates = %d, want 3 (one per exit: a/b/c): %#v\n%#v",
			got, end.FlowExitMerge, end.Inputs)
	}
}

const candidateLessLoopBodySrc = `@language nodecoda/1
@mode workflow
function main() -> string {
    let values = [1, 2, 3];
    for (v in values) {
        if (v == 1) { continue; } else if (v > 2) { break; }
    }
    return "done";
}
`

// TestOptimizeDropsCandidateLessJunctionInsideLoopBody：同一条规则在容器体内
// （更深一层）必须同样成立——loop 体末的 if/else-if 两臂都是控制叶子
// （continue / break），无候选无消费者。这是 s08 漂移链上的实际形状。
func TestOptimizeDropsCandidateLessJunctionInsideLoopBody(t *testing.T) {
	g := buildIR(t, candidateLessLoopBodySrc)
	loop := firstLoop(t, g.Root)
	outer := firstIf(t, loop.Body)
	if outer.Merge != nil {
		t.Fatalf("loop-body candidate-less junction reached lowering: %#v", outer.Merge)
	}
	inner := firstIf(t, outer.Else)
	if inner.Merge != nil {
		t.Fatalf("nested loop-body candidate-less junction reached lowering: %#v", inner.Merge)
	}
	// 语句本身必须仍然存在：删除的是 junction，不是控制流。
	if len(loop.Body.Stmts) == 0 {
		t.Fatal("loop body was emptied")
	}
}

const candidateLessInlineSrc = `@language nodecoda/1
@mode workflow
function guard(string x) {
    if (x == "a") { let q = 1; } else if (x == "b") { let r = 2; }
}
function main(string v) -> string {
    guard(v);
    return "done";
}
`

// TestOptimizeDropsCandidateLessJunctionInsideInlineBody：inline 函数体是
// **同一个图**里的隔离返回作用域，不是独立程序——规则必须穿透它
// （同一判据，任意嵌套深度；这也让 inline 体内汇聚的候选引用参与消费计数）。
func TestOptimizeDropsCandidateLessJunctionInsideInlineBody(t *testing.T) {
	g := buildIR(t, candidateLessInlineSrc)
	inline := firstInline(t, g.Root)
	outer := firstIf(t, inline.Body)
	if outer.Merge != nil {
		t.Fatalf("inline-body candidate-less junction reached lowering: %#v", outer.Merge)
	}
	inner := firstIf(t, outer.Else)
	if inner.Merge != nil {
		t.Fatalf("nested inline-body candidate-less junction reached lowering: %#v", inner.Merge)
	}
}

func firstInline(t *testing.T, b *ir.Block) *ir.InlineStmt {
	t.Helper()
	var found *ir.InlineStmt
	var walk func(x *ir.Block)
	walk = func(x *ir.Block) {
		if x == nil || found != nil {
			return
		}
		for _, s := range x.Stmts {
			switch v := s.(type) {
			case *ir.InlineStmt:
				found = v
				return
			case *ir.IfStmt:
				walk(v.Then)
				walk(v.Else)
			case *ir.LoopStmt:
				walk(v.Body)
			}
		}
	}
	walk(b)
	if found == nil {
		t.Fatal("no inline statement found")
	}
	return found
}
