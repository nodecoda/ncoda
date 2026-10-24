package compile

// loop_whole_value_contract_test.go —— 循环产出源的**整值绑定**（空名）必须取到
// 整节点输出（R-139 第一刀的运行时一半；契约侧判据见
// internal/compiler/cozebiz_whole_value_runtime_carrier_contract_test.go）。
//
// 契约（一处定义）：`spec.Source.Name == ""` = 「产出方的整个值」——
// `lookupDotted(producer, "")` 返回该节点的整个输出 map。IR 侧「值由一组字段口
// 承载」的整值标记在 spec 构建时降为空名（`spec.fromSource`），驱动器据此按整节点
// 取值。**回归现场**（672 源跑批，修前）：循环产出源带着 IR 内部标记 @"whole" 穿透，
// 驱动器按字段名查不到 ⇒ 每次迭代收集到 nil ⇒ 下游用户 code 读 `item.code` 崩。

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// loopWholeValueSpec: start → loop(by_array over start.items) → end。
// body = 一个声明式扁平字段口的工具节点；循环产出源 = 该节点的**整值**（空名）。
func loopWholeValueSpec() *spec.Spec {
	children := []spec.NodeSpec{
		nodeSpec("100003", spec.KindTool, &spec.ToolConfig{ToolName: "record", ProviderID: "p"}),
	}
	inner := []spec.EdgeSpec{
		{Source: "100002", Target: "100003", SourcePort: wLoopFunctionInlineOutput},
		{Source: "100003", Target: "100002", TargetPort: wLoopFunctionInlineInput},
	}
	loopCfg := &spec.LoopConfig{
		LoopType:      "by_array",
		Inputs:        []spec.Source{{Path: []string{"items"}, BlockID: "100001", Name: "items"}},
		OutTypes:      map[string]*spec.TypeSpec{"output": {Kind: "list", Elem: &spec.TypeSpec{Kind: "object"}}},
		OutputSources: []spec.Source{{Path: []string{"output"}, BlockID: "100003", Name: ""}},
		Children:      children,
		InnerEdges:    inner,
	}
	return &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001", Params: []spec.ParamSpec{{Name: "items", Type: &spec.TypeSpec{Kind: "list"}}}},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("100002", spec.KindLoop, loopCfg),
			nodeSpec("900001", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "100002", Output: "output"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "100002"},
			{Source: "100002", Target: "900001"},
		},
	}
}

func TestLoopWholeValueBindsNodeOutputNotNil(t *testing.T) {
	pm := plugin.New()
	if err := pm.Register(&plugin.Plugin{
		ID: "p", Type: plugin.PluginCallable,
		Tools: []plugin.Tool{{
			Name: "record",
			Call: func(ctx context.Context, args map[string]any) (any, error) {
				return map[string]any{"code": 0, "data": map[string]any{"title": "t"}}, nil
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	c := New(nodes.StdRegistry(), WithDeps(nodes.Deps{Tools: pm}))
	r := mustCompile(t, c, loopWholeValueSpec())
	res := UnwrapOutput(mustInvoke(t, r, map[string]any{"items": []any{"a", "b"}}))

	raw, err := json.Marshal(res["output"])
	if err != nil {
		t.Fatalf("marshal loop output: %v", err)
	}
	var list []any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("loop output is not a list: %s", raw)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 collected iterations, got %d: %s", len(list), raw)
	}
	for i, elem := range list {
		m, ok := elem.(map[string]any)
		if !ok {
			t.Fatalf("iteration %d: 整值绑定取到的不是产出方的整节点输出（got %T: %v）—— "+"空名必须解析为整个节点输出，落空即 nil ⇒ 下游 `Cannot read properties of null`", i, elem, elem)
		}
		if got := m["code"]; got != float64(0) {
			t.Fatalf("iteration %d: 整节点输出缺声明字段 code（got %v）：%s", i, got, raw)
		}
		data, ok := m["data"].(map[string]any)
		if !ok || data["title"] != "t" {
			t.Fatalf("iteration %d: 整节点输出缺嵌套字段 data.title：%s", i, raw)
		}
	}
}
