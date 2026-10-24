package compile

// loop_regress_test.go — M14 回归:loop 容器接线。
//
// 修复:
//   - 主图 dangling 检查曾把 loop body 内部引用（child 间数据流,如
//     assign→code）误报 RDanglingRef —— isLoopChild 跳过。
//   - increment shim 曾引用 "output" 端口而实际输出 "result",循环累加值
//     恒 nil —— buildIncrement 声明 OutOrder + firstOutputPort 引用。
//
// 断言形态直接取自 12_loop_with_break（while num<5 { num+=1 } → 5）,
// 聚焦回归,不依赖 corpus gate。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// loopIncrementSpec 构造 start → loop(by_iteration,state num="1") → end;
// body: selector(num<5) → code(increment shim) → assign(loop_assign num)
// → aggregate;selector false → break。inner_edges 即 12 的 spec 形态。
func loopIncrementSpec() *spec.Spec {
	children := []spec.NodeSpec{
		nodeSpec("100003", spec.KindSelector, &spec.SelectorConfig{Conditions: []spec.Condition{
			{Left: spec.Operand{Ref: &spec.Ref{Producer: "100002", Output: "num"}}, Operator: 15, Right: spec.Operand{Val: 5}},
		}, Logic: 0}),
		nodeSpec("100004", spec.KindCode, &spec.CodeConfig{
			Code: "def main(num) -> dict:\n    return {\"result\": num + 1}", Language: "python",
			Inputs:    []spec.Source{{Path: []string{"num"}, BlockID: "100002", Name: "num"}},
			Synthetic: true,
			OutOrder:  []string{"result"},
		}),
		nodeSpec("100005", spec.KindLoopAssign, &spec.AssignConfig{
			VarName: "num", Value: spec.Source{Path: []string{"num"}, BlockID: "100004", Name: "result"},
			LoopState: true, LoopID: "100002",
		}),
		nodeSpec("100006", spec.KindBreak, &spec.BreakConfig{}),
		nodeSpec("100007", spec.KindAggregate, &spec.AggregateConfig{Refs: []spec.Ref{}}),
	}
	inner := []spec.EdgeSpec{
		{Source: "100002", Target: "100003", SourcePort: wLoopFunctionInlineOutput},
		{Source: "100003", Target: "100004", SourcePort: "true"},
		{Source: "100004", Target: "100005"},
		{Source: "100003", Target: "100006", SourcePort: "false"},
		{Source: "100005", Target: "100007"},
		{Source: "100006", Target: "100007"},
		{Source: "100007", Target: "100002", TargetPort: wLoopFunctionInlineInput},
		{Source: "100002", Target: "100004", SourcePort: wLoopFunctionInlineOutput},
	}
	loopCfg := &spec.LoopConfig{
		LoopType:      "by_iteration",
		LoopCount:     10,
		StateTypes:    map[string]*spec.TypeSpec{"num": {Kind: "integer"}},
		StateValues:   map[string]spec.Source{"num": {Val: "1"}},
		OutTypes:      map[string]*spec.TypeSpec{"num": {Kind: "integer"}},
		OutputSources: []spec.Source{{Path: []string{"num"}, BlockID: "100002", Name: "num"}},
		Children:      children,
		InnerEdges:    inner,
	}
	return &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("100002", spec.KindLoop, loopCfg),
			nodeSpec("900001", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "100002", Output: "num"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "100002"},
			{Source: "100002", Target: "900001"},
		},
	}
}

// M14:loop body 内部引用不误报 RDanglingRef,且 increment 端口正确 →
// 循环累加 num 1→5。
func TestLoopIncrementRegression(t *testing.T) {
	r := mustCompile(t, New(testRegistry()), loopIncrementSpec())
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{}))
	// shim 对整数值保持 int64（"1"+1 → int64 2,逐轮累加至 5）。
	got, ok := out["output"].(int64)
	if !ok {
		gotf, okf := out["output"].(float64)
		if !okf || gotf != 5 {
			t.Fatalf("output = %#v (%T), want 5", out["output"], out["output"])
		}
		return
	}
	if got != 5 {
		t.Fatalf("output = %v, want 5", got)
	}
}

// M14:body 内部引用（assign→code）在无 isLoopChild 时曾误报 RDanglingRef;
// 这里显式断言编译成功（spec 中 100005 引用 100004 —— 都是 loop children,
// 不在主图 nodesByID）。
func TestLoopBodyInternalRefCompiles(t *testing.T) {
	if _, err := New(testRegistry()).Compile(t.Context(), loopIncrementSpec()); err != nil {
		t.Fatalf("Compile: %v (loop body internal ref must not be RDanglingRef)", err)
	}
}
