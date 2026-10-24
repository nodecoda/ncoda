package compile

// loop_return_e2e_test.go — M17:loop 内 return 语义 E2E（46_loop_escape
// 的 spec 级等价）。覆盖:
//   - loop 内 return → loop 输出 returned/return_value 端口;
//   - spec 层短路（selector(loop.returned) → aggregate(return_value |
//     default)）→ 流程短路到 return_value,不执行 default 路径;
//   - 正常路径（无 return）→ default 值。
//
// 46 的全链断言在 corpus gate（extraPass "output":4）;此处是隔离的
// spec 级证据,不依赖 python。

import (
	"context"
	"fmt"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// loopReturnSpec 手工构造 46 的 spec 形状:
// start → code(values=[1,2,4,6]) → LOOP(by_array,var=value,带 loop_return)
//
//	→ 短路(SEL+AGGR) → end。
//
// loop body:selector(value==4) → true: loop_return(value) / false: continue
//
//	→ 汇聚回容器（loop-back）。return_value=4 时短路输出 4;否则 aggregate
//	取 default(0)。
func loopReturnSpec() *spec.Spec {
	return &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("src", spec.KindCode, &spec.CodeConfig{
				Code:      "def main() -> dict:\n    return {\"result\": [1, 2, 4, 6]}",
				Language:  "python",
				Synthetic: true,
				OutOrder:  []string{"result"},
			}),
			nodeSpec("loop", spec.KindLoop, &spec.LoopConfig{
				VarName:    "value",
				LoopType:   "by_array",
				Inputs:     []spec.Source{{Path: []string{"values"}, BlockID: "src", Name: "result"}},
				Types:      map[string]*spec.TypeSpec{"values": {Kind: "list"}},
				StateTypes: map[string]*spec.TypeSpec{"value": {Kind: "integer"}},
				OutputSources: []spec.Source{
					{Path: []string{"returned"}, BlockID: "loop", Name: "returned"},
					{Path: []string{"return_value"}, BlockID: "loop", Name: "return_value"},
				},
				Children: []spec.NodeSpec{
					nodeSpec("sel", spec.KindSelector, &spec.SelectorConfig{Conditions: []spec.Condition{{
						Left:     spec.Operand{Ref: &spec.Ref{Producer: "loop", Output: "value"}},
						Operator: 1, // equals
						Right:    spec.Operand{Val: 4},
					}}}),
					nodeSpec("lr", spec.KindLoopReturn, &spec.LoopReturnConfig{
						Value: spec.Source{Path: []string{"value"}, BlockID: "loop", Name: "value"},
					}),
					nodeSpec("cont", spec.KindContinue, &spec.ContinueConfig{}),
					nodeSpec("agg", spec.KindAggregate, &spec.AggregateConfig{Refs: []spec.Ref{}}),
				},
				InnerEdges: []spec.EdgeSpec{
					{Source: "loop", Target: "sel", SourcePort: wLoopFunctionInlineOutput},
					{Source: "sel", Target: "lr", SourcePort: "true"},
					{Source: "lr", Target: "agg"},
					{Source: "sel", Target: "cont", SourcePort: "false"},
					{Source: "cont", Target: "agg"},
					{Source: "agg", Target: "loop", TargetPort: wLoopFunctionInlineInput},
				},
			}),
			nodeSpec("def", spec.KindCode, &spec.CodeConfig{
				Code:      "def main() -> dict:\n    return {\"result\": 0}",
				Language:  "python",
				Synthetic: true,
				OutOrder:  []string{"result"},
			}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "lr_agg_loop", Output: wGroup1}}}),
			// 短路结构（spec.Build 对带 ReturnValue 的 loop 自动生成）:
			nodeSpec("lr_sel_loop", spec.KindSelector, &spec.SelectorConfig{Conditions: []spec.Condition{{
				Left:     spec.Operand{Ref: &spec.Ref{Producer: "loop", Output: "returned"}},
				Operator: 1,
				Right:    spec.Operand{Val: true},
			}}}),
			nodeSpec("lr_agg_loop", spec.KindAggregate, &spec.AggregateConfig{Refs: []spec.Ref{
				{Producer: "loop", Output: "return_value"},
				{Producer: "def", Output: "output"},
			}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "src"},
			{Source: "src", Target: "loop"},
			{Source: "loop", Target: "lr_sel_loop"},
			{Source: "lr_sel_loop", Target: "lr_agg_loop", SourcePort: "true"},
			{Source: "lr_sel_loop", Target: "def", SourcePort: "false"},
			{Source: "def", Target: "lr_agg_loop"},
			{Source: "lr_agg_loop", Target: "e1"},
		},
	}
}

// TestM17LoopReturnShortCircuit:values 含 4 → 迭代 2 return 4 → 短路输出 4
// （default 0 不执行）。
func TestM17LoopReturnShortCircuit(t *testing.T) {
	r := mustCompile(t, New(testRegistry()), loopReturnSpec())
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{}))
	if fmt.Sprint(out["output"]) != "4" {
		t.Fatalf("output = %#v, want 4 (loop return short-circuit)", out["output"])
	}
}

// TestM17LoopReturnDefaultPath:values 不含 4 → loop 正常完成 → 短路 false
// → default 0。
func TestM17LoopReturnDefaultPath(t *testing.T) {
	s := loopReturnSpec()
	// 改数组不含 4
	for i := range s.Nodes {
		if s.Nodes[i].ID == "src" {
			s.Nodes[i] = nodeSpec("src", spec.KindCode, &spec.CodeConfig{
				Code:      "def main() -> dict:\n    return {\"result\": [1, 2, 6]}",
				Language:  "python",
				Synthetic: true,
				OutOrder:  []string{"result"},
			})
		}
	}
	r := mustCompile(t, New(testRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{}))
	if fmt.Sprint(out["output"]) != "0" {
		t.Fatalf("output = %#v, want 0 (default path)", out["output"])
	}
}

// TestM17LoopReturnNodeState:loop_return 节点直接写 LoopState 契约
// （Returned/ReturnValue/Break）—— 独立于图编译验证节点语义。
func TestM17LoopReturnNodeState(t *testing.T) {
	reg := testRegistry()
	n := nodeSpec("lr", spec.KindLoopReturn, &spec.LoopReturnConfig{
		Value: spec.Source{Path: []string{"value"}, BlockID: "loop", Name: "value"},
	})
	impl, err := reg.Build(context.Background(), &n, nodes.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := impl.Invoke(context.Background(), map[string]any{"value": 9})
	if err != nil {
		t.Fatal(err)
	}
	if out["$return"] != 9 {
		t.Fatalf("loop_return out = %v, want $return=9", out)
	}
}
