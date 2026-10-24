package compile

// agent_e2e_test.go — agent 节点端到端（参考目标 FunctionCall 循环语义）。
//
// 链路: spec.v1 agent 节点 → nodes.Agent 手写循环 → fake ToolCallingModel
// （带工具调用）→ plugin.Manager 工具执行 → 观察回填 → 最终答案。
// 验证: 工具调用循环、schema 描述下发、消息历史（assistant tool_calls +
// tool 观察）、输出端口。

import (
	"context"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// agentLoopModel 是可编程 ToolCallingModel:按轮次返回预设结果。
type agentLoopModel struct {
	results []nodes.ChatResult
	round   int
	seen    [][]nodes.ToolSpec // 每轮收到的工具描述
}

func (m *agentLoopModel) Chat(ctx context.Context, model string, msgs []nodes.Message) (string, error) {
	res, err := m.ChatWithTools(ctx, model, msgs, nil)
	return res.Content, err
}

func (m *agentLoopModel) ChatWithTools(ctx context.Context, model string, msgs []nodes.Message, tools []nodes.ToolSpec) (nodes.ChatResult, error) {
	m.seen = append(m.seen, tools)
	if m.round >= len(m.results) {
		return nodes.ChatResult{Content: "done"}, nil
	}
	r := m.results[m.round]
	m.round++
	return r, nil
}

func TestAgentE2EToolCallLoop(t *testing.T) {
	pm := plugin.New()
	sum := &plugin.Plugin{
		ID: "math", Type: plugin.PluginCallable,
		Tools: []plugin.Tool{{
			Name: "sum", Desc: "add two integers",
			Call: func(ctx context.Context, args map[string]any) (any, error) {
				a := int(args["a"].(float64))
				b := int(args["b"].(float64))
				return a + b, nil
			},
			Params: []plugin.ParamSchema{
				{Name: "a", Type: "int", Required: true},
				{Name: "b", Type: "int", Required: true},
			},
		}},
	}
	if err := pm.Register(sum); err != nil {
		t.Fatal(err)
	}

	fake := &agentLoopModel{results: []nodes.ChatResult{
		{Content: "computing", ToolCalls: []nodes.ToolCall{{ID: "c1", Name: "sum", Arguments: `{"a":2,"b":3}`}}},
		{Content: "the sum is 5"},
	}}

	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001", Params: []spec.ParamSpec{{Name: "query", Type: &spec.TypeSpec{Kind: "string"}}}},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("ag", spec.KindAgent, &spec.AgentConfig{
				Model:       "m",
				Instruction: "you are a calculator",
				Query:       &spec.Source{Path: []string{"query"}, BlockID: "100001", Name: "query"},
				Tools: []spec.AgentToolWire{
					{Alias: "sum", ProviderName: "math", ToolName: "sum"},
				},
			}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "ag", Output: "output"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "ag"},
			{Source: "ag", Target: "e1"},
		},
	}

	deps := nodes.Deps{Models: fake, Tools: pm}
	var steps []nodes.AgentStep
	deps.AgentStep = func(st nodes.AgentStep) { steps = append(steps, st) }

	c := New(nodes.StdRegistry(), WithDeps(deps))
	r, err := c.Compile(context.Background(), s)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out := mustInvoke(t, r, map[string]any{"query": "2+3?"})
	res := UnwrapOutput(out)
	if got := mustJSON(t, res["output"]); !strings.Contains(got, "the sum is 5") {
		t.Fatalf("output = %s", got)
	}

	// 第 1 轮工具描述带 schema（别名 sum + JSON Schema 参数）。
	if len(fake.seen) == 0 || len(fake.seen[0]) != 1 {
		t.Fatalf("round-1 tools = %+v", fake.seen)
	}
	spec1 := fake.seen[0][0]
	if spec1.Name != "sum" || spec1.Description != "add two integers" {
		t.Fatalf("tool spec = %+v", spec1)
	}
	props, _ := spec1.Parameters["properties"].(map[string]any)
	if props["a"].(map[string]any)["type"] != "integer" {
		t.Fatalf("props = %v", props)
	}

	// 步骤事件:thought + 工具观察 + 最终 thought。
	if len(steps) != 3 {
		t.Fatalf("steps = %d (want 3): %+v", len(steps), steps)
	}
	if steps[1].ToolName != "sum" || steps[1].Observation != "5" {
		t.Fatalf("step1 = %+v", steps[1])
	}
}

func TestAgentE2EFinalRoundNoTools(t *testing.T) {
	pm := plugin.New()
	_ = pm.Register(&plugin.Plugin{
		ID: "math", Type: plugin.PluginCallable,
		Tools: []plugin.Tool{{Name: "sum", Call: func(ctx context.Context, args map[string]any) (any, error) { return 0, nil }}},
	})
	fake := &alwaysToolModel{tools: []nodes.ToolSpec{{Name: "sum"}}}
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("ag", spec.KindAgent, &spec.AgentConfig{
				Model: "m",
				Tools: []spec.AgentToolWire{{Alias: "sum", ProviderName: "math", ToolName: "sum"}},
			}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "ag", Output: "output"}}}),
		},
		Edges: []spec.EdgeSpec{{Source: "100001", Target: "ag"}, {Source: "ag", Target: "e1"}},
	}
	c := New(nodes.StdRegistry(), WithDeps(nodes.Deps{Models: fake, Tools: pm}))
	r, err := c.Compile(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Invoke(context.Background(), map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "max iteration") {
		t.Fatalf("want max iteration error, got %v", err)
	}
	// 最后一轮工具清空。
	if fake.seen[len(fake.seen)-1] != nil {
		t.Fatalf("final round tools = %v", fake.seen[len(fake.seen)-1])
	}
}

// alwaysToolModel 每轮都发起工具调用（max-iteration 压力场景）。
type alwaysToolModel struct {
	tools []nodes.ToolSpec
	seen  [][]nodes.ToolSpec
}

func (m *alwaysToolModel) Chat(ctx context.Context, model string, msgs []nodes.Message) (string, error) {
	res, err := m.ChatWithTools(ctx, model, msgs, nil)
	return res.Content, err
}

func (m *alwaysToolModel) ChatWithTools(ctx context.Context, model string, msgs []nodes.Message, tools []nodes.ToolSpec) (nodes.ChatResult, error) {
	m.seen = append(m.seen, tools)
	return nodes.ChatResult{
		Content:   "looping",
		ToolCalls: []nodes.ToolCall{{ID: "c", Name: "sum", Arguments: `{"a":1,"b":1}`}},
	}, nil
}
