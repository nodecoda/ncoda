package compile

// state_e2e_test.go — M2.3 状态面 e2e:RuntimeState 取代 scanPort。
//
// 验证:
//   - loop_assign 经 ProcessState 写 Loops[loopID].Vars,驱动器跨迭代读取
//     （状态输出 finalState + 每迭代绑定）,取代 body 输出端口扫描;
//   - break / continue 经状态标记控制循环,取代 $break/$continue 端口扫描;
//   - loop_assign 在无 RuntimeState 上下文（非图调用）→ RNodeError。

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// 场景:by_array 5 项,loop_assign 把每项写入状态变量 acc;
// 状态输出 acc = 最后一项;每迭代输出 = 该项。
func TestLoopStateAccumulate(t *testing.T) {
	assign := nodeSpec("b_assign", spec.KindLoopAssign, &spec.AssignConfig{
		VarName:   "acc",
		LoopState: true,
		LoopID:    "lp",
		Value:     spec.Source{Path: []string{"value"}, BlockID: "lp", Name: "item"},
	})
	loopCfg := &spec.LoopConfig{
		LoopType:    "by_array",
		VarName:     "item",
		InputArrays: []string{"items"},
		Inputs:      []spec.Source{{Path: []string{"items"}, BlockID: "100001", Name: "items"}},
		StateValues: map[string]spec.Source{"acc": {Val: "init"}},
		OutputSources: []spec.Source{
			{Path: []string{"output"}, BlockID: "b_assign", Name: "acc"}, // 每迭代输出
			{Path: []string{"acc"}, BlockID: "lp", Name: "acc"},          // 状态输出
		},
		Children: []spec.NodeSpec{assign},
		InnerEdges: []spec.EdgeSpec{
			{Source: "lp", Target: "b_assign", SourcePort: wLoopFunctionInlineOutput},
			{Source: "b_assign", Target: "lp", TargetPort: wLoopFunctionInlineInput},
		},
	}
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("lp", spec.KindLoop, loopCfg),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{
				Inputs: []spec.Ref{{Producer: "lp", Output: "output"}},
				Outputs: []spec.EndOutput{
					{Name: "acc", Ref: spec.Ref{Producer: "lp", Output: "acc"}},
				},
			}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "lp"},
			{Source: "lp", Target: "e1"},
		},
	}
	r := mustCompile(t, New(testRegistry()), s)
	out := mustInvoke(t, r, map[string]any{"items": []any{"a", "b", "c", "d", "e"}})
	got := UnwrapOutput(out)
	if got["acc"] != "e" {
		t.Fatalf("state acc = %v, want e", got["acc"])
	}
	list, ok := got["output"].([]any)
	if !ok || len(list) != 5 || list[4] != "e" {
		t.Fatalf("iter output = %v, want [a b c d e]", got["output"])
	}
}

// break:体含无条件 break sink → 首迭代后停止;状态变量 acc 停在首项 "a"
// （marker 在输出收集前生效,当前迭代输出被丢弃 —— M1 原语义保留）。
func TestLoopStateBreak(t *testing.T) {
	assign := nodeSpec("b_assign", spec.KindLoopAssign, &spec.AssignConfig{
		VarName: "acc", LoopState: true, LoopID: "lp",
		Value: spec.Source{Path: []string{"value"}, BlockID: "lp", Name: "item"},
	})
	loopCfg := &spec.LoopConfig{
		LoopType:    "by_array",
		VarName:     "item",
		InputArrays: []string{"items"},
		Inputs:      []spec.Source{{Path: []string{"items"}, BlockID: "100001", Name: "items"}},
		StateValues: map[string]spec.Source{"acc": {Val: "init"}},
		Children: []spec.NodeSpec{
			assign,
			nodeSpec("b_br", spec.KindBreak, &spec.BreakConfig{}),
		},
		InnerEdges: []spec.EdgeSpec{
			{Source: "lp", Target: "b_assign", SourcePort: wLoopFunctionInlineOutput},
			{Source: "b_assign", Target: "b_br"},
			{Source: "b_br", Target: "lp", TargetPort: wLoopFunctionInlineInput},
		},
		OutputSources: []spec.Source{
			{Path: []string{"acc"}, BlockID: "lp", Name: "acc"},
		},
	}
	s := loopStateSpec(loopCfg)
	r := mustCompile(t, New(testRegistry()), s)
	out := mustInvoke(t, r, map[string]any{"items": []any{"a", "b", "c"}})
	got := UnwrapOutput(out)
	if got["acc"] != "a" {
		t.Fatalf("break: acc = %v, want a (first item only)", got["acc"])
	}
}

// continue:体含无条件 continue sink → 每迭代跳过输出收集但状态继续累积;
// acc = 最后一项 "c"（continue 不影响状态写入,只跳过 outputs 收集）。
func TestLoopStateContinue(t *testing.T) {
	assign := nodeSpec("b_assign", spec.KindLoopAssign, &spec.AssignConfig{
		VarName: "acc", LoopState: true, LoopID: "lp",
		Value: spec.Source{Path: []string{"value"}, BlockID: "lp", Name: "item"},
	})
	loopCfg := &spec.LoopConfig{
		LoopType:    "by_array",
		VarName:     "item",
		InputArrays: []string{"items"},
		Inputs:      []spec.Source{{Path: []string{"items"}, BlockID: "100001", Name: "items"}},
		StateValues: map[string]spec.Source{"acc": {Val: "init"}},
		Children: []spec.NodeSpec{
			assign,
			nodeSpec("b_c", spec.KindContinue, &spec.ContinueConfig{}),
		},
		InnerEdges: []spec.EdgeSpec{
			{Source: "lp", Target: "b_assign", SourcePort: wLoopFunctionInlineOutput},
			{Source: "b_assign", Target: "b_c"},
			{Source: "b_c", Target: "lp", TargetPort: wLoopFunctionInlineInput},
		},
		OutputSources: []spec.Source{
			{Path: []string{"acc"}, BlockID: "lp", Name: "acc"},
		},
	}
	s := loopStateSpec(loopCfg)
	r := mustCompile(t, New(testRegistry()), s)
	out := mustInvoke(t, r, map[string]any{"items": []any{"a", "b", "c"}})
	got := UnwrapOutput(out)
	if got["acc"] != "c" {
		t.Fatalf("continue: acc = %v, want c (state accumulates, outputs skipped)", got["acc"])
	}
}

func loopStateSpec(loopCfg *spec.LoopConfig) *spec.Spec {
	return &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("lp", spec.KindLoop, loopCfg),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{
				Inputs: []spec.Ref{{Producer: "lp", Output: "output"}},
				Outputs: []spec.EndOutput{
					{Name: "acc", Ref: spec.Ref{Producer: "lp", Output: "acc"}},
				},
			}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "lp"},
			{Source: "lp", Target: "e1"},
		},
	}
}

// loop_assign 非图上下文（无 RuntimeState）→ RNodeError（fail-fast）。
func TestLoopAssignMissingState(t *testing.T) {
	b, _ := json.Marshal(&spec.AssignConfig{
		VarName: "acc", LoopState: true, LoopID: "lp",
		Value: spec.Source{Val: 1},
	})
	impl, err := nodes.StdRegistry().Build(context.Background(),
		&spec.NodeSpec{ID: "n", Kind: spec.KindLoopAssign, Config: b}, nodes.Deps{})
	if err != nil {
		t.Fatalf("build should succeed (state checked at invoke): %v", err)
	}
	_, err = impl.Invoke(context.Background(), map[string]any{"value": 1})
	if err == nil || diag.CodeOf(err) != diag.RNodeError {
		t.Fatalf("err = %v, want RNodeError", err)
	}
}
