package compile

// loop_body_collect_surface_contract_test.go —— loop 的收集输出引用**体内非尾
// 节点**时必须取到值（R-142 第二刀）。
//
// 契约（平台一手 = coze-studio `backend/domain/workflow/internal/nodes/loop/loop.go`）：
// loop 的输出按**节点完成事件**收集（`setIthOutput` 在节点完成时写 `output[k]`），
// 所以 `outputs[]` 允许引用体内**任意**节点 —— 不限于接到循环体出口（END）的那一个。
// 我们的体图返回值却只有「END 数据前驱的命名空间」（eino `dagChannel.reportValues`
// 只收 `DataPredecessors`）⇒ 引用体内中间节点时 `resolveRef` 取不到 ⇒ 声明了的
// 输出静默变 nil（撞 AGENTS.md「失配 ≠ 静默丢弃」）。
//
// 形状（语料 `7571759719838089216_V038_ifbook_canspeak` 的 `100022` 同构：
// 两个 tts → aggregate(Group1) → get_audio_duration；同时收集 Group1 与 duration）：
//
//	100003 tool(record) → 100004 aggregate(Group1) → 100005 tool(duration)
//
// 其中 `audio_list` 引用的是**体内非尾**节点 100004，`duration_list` 引用尾节点
// 100005 —— 修前前者每迭代都是 nil，后者正常。
//
// 撤实现必红：去掉体图出口面补齐（`bodyExtraExit` → END）⇒ `audio_list` 变 [nil nil]。

import (
	"context"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func loopBodyCollectSpec() *spec.Spec {
	children := []spec.NodeSpec{
		nodeSpec("100003", spec.KindTool, &spec.ToolConfig{ToolName: "record", ProviderID: "p"}),
		nodeSpec("100004", spec.KindAggregate, &spec.AggregateConfig{
			Refs:            []spec.Ref{{Producer: "100003"}}, // 空 Output = 整值
			LegacyGroupName: wGroup1,
		}),
		nodeSpec("100005", spec.KindTool, &spec.ToolConfig{
			ToolName: "duration", ProviderID: "p",
			Params: []spec.ToolParam{{
				Key: "mp3_url",
				Ref: &spec.Source{BlockID: "100004", Name: wGroup1},
			}},
		}),
	}
	inner := []spec.EdgeSpec{
		{Source: "100002", Target: "100003", SourcePort: wLoopFunctionInlineOutput},
		{Source: "100003", Target: "100004"},
		{Source: "100004", Target: "100005"},
		{Source: "100005", Target: "100002", TargetPort: wLoopFunctionInlineInput},
	}
	loopCfg := &spec.LoopConfig{
		LoopType: "by_array",
		Inputs:   []spec.Source{{Path: []string{"items"}, BlockID: "100001", Name: "items"}},
		OutTypes: map[string]*spec.TypeSpec{
			"audio_list":    {Kind: "list", Elem: &spec.TypeSpec{Kind: "object"}},
			"duration_list": {Kind: "list", Elem: &spec.TypeSpec{Kind: "integer"}},
		},
		OutputSources: []spec.Source{
			{Path: []string{"audio_list"}, BlockID: "100004", Name: wGroup1},       // 体内非尾节点
			{Path: []string{"duration_list"}, BlockID: "100005", Name: "duration"}, // 尾节点
		},
		Children:   children,
		InnerEdges: inner,
	}
	return &spec.Spec{
		Version: spec.Version,
		Entry: &spec.EntrySpec{ID: "100001",
			Params: []spec.ParamSpec{{Name: "items", Type: &spec.TypeSpec{Kind: "list"}}}},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("100002", spec.KindLoop, loopCfg),
			// end 的 Inputs 全部落在规范 "output" 槽（后写覆盖），命名口走 Outputs。
			nodeSpec("900001", spec.KindEnd, &spec.EndConfig{Outputs: []spec.EndOutput{
				{Name: "audio_list", Ref: spec.Ref{Producer: "100002", Output: "audio_list"}},
				{Name: "duration_list", Ref: spec.Ref{Producer: "100002", Output: "duration_list"}},
			}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "100002"},
			{Source: "100002", Target: "900001"},
		},
	}
}

func loopBodyCollectCompiler(t *testing.T) *Compiler {
	t.Helper()
	pm := plugin.New()
	if err := pm.Register(&plugin.Plugin{
		ID: "p", Type: plugin.PluginCallable,
		Tools: []plugin.Tool{
			{Name: "record", Call: func(ctx context.Context, args map[string]any) (any, error) {
				return map[string]any{"code": 0, "tag": "rec"}, nil
			}},
			{Name: "duration", Call: func(ctx context.Context, args map[string]any) (any, error) {
				return map[string]any{"duration": 1}, nil
			}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return New(nodes.StdRegistry(), WithDeps(nodes.Deps{Tools: pm}))
}

func TestLoopCollectsBodyNonTailNodeOutput(t *testing.T) {
	r := mustCompile(t, loopBodyCollectCompiler(t), loopBodyCollectSpec())
	res := UnwrapOutput(mustInvoke(t, r, map[string]any{"items": []any{"a", "b"}}))

	lst, ok := res["audio_list"].([]any)
	if !ok {
		t.Fatalf("audio_list = %#v（类型 %T），期望列表", res["audio_list"], res["audio_list"])
	}
	if len(lst) != 2 {
		t.Fatalf("audio_list = %#v，期望 2 个迭代值", lst)
	}
	for i, v := range lst {
		m, isMap := v.(map[string]any)
		if !isMap {
			t.Fatalf("audio_list[%d] = %#v —— 收集输出引用体内**非尾**节点时静默丢值"+
				"（声明了就必须有值：平台按节点完成事件收集，可引用体内任意节点）", i, v)
		}
		if m["code"] != 0 || m["tag"] != "rec" {
			t.Fatalf("audio_list[%d] = %#v，期望 aggregate 收集到的 record 输出", i, m)
		}
	}

	// 尾节点的收集面照旧（回归护栏）。
	dl, ok := res["duration_list"].([]any)
	if !ok || len(dl) != 2 || dl[0] != 1 || dl[1] != 1 {
		t.Fatalf("duration_list = %#v，期望 [1 1]", res["duration_list"])
	}
}

// 零迭代 + 体内非尾节点引用：声明了的输出不得消失（R-142 第一半 + 第二半交叉）。
func TestLoopCollectsBodyNonTailNodeOutputZeroIter(t *testing.T) {
	r := mustCompile(t, loopBodyCollectCompiler(t), loopBodyCollectSpec())
	res := UnwrapOutput(mustInvoke(t, r, map[string]any{"items": []any{}}))

	for _, k := range []string{"audio_list", "duration_list"} {
		v, ok := res[k]
		if !ok {
			t.Fatalf("零迭代后 `%s` 没有键 —— 声明了的输出静默消失（平台语义 = 空列表）", k)
		}
		l, isList := v.([]any)
		if !isList || len(l) != 0 {
			t.Fatalf("零迭代后 `%s` = %#v，期望空列表", k, v)
		}
	}
}
