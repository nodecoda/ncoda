package nodes

// agent_test.go — agent 节点单测（对齐参考目标 FunctionCall 循环）。
//
// T3 零外部依赖:ModelFactory / ToolRegistry 全用 fake,断言:
//   - 无工具 agent 单模型调用（persona + query 消息形状）
//   - 工具 wire 绑定缺失 → RBindingMissing（Build 期）
//   - deps 缺失 → RNodeError（Build 期）
//   - 工具调用循环:模型 tool_call → 工具执行 → 观察回填 → 下一轮
//   - 工具缺失 → 错误文本回填（不崩溃）
//   - 达 max_iteration 仍有 tool_calls → 错误
//   - 最后一轮清空工具（逼最终答案）
//   - AgentStep 钩子收到 thought / 工具事件
//   - MaxRetry 重试

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// fakeAgentToolReg 是最小 ToolRegistry 桩。
type fakeAgentToolReg struct {
	names []string
	out   any
	err   error
	got   map[string]any
	callN string
}

func (f *fakeAgentToolReg) Names() []string { return f.names }
func (f *fakeAgentToolReg) Call(ctx context.Context, name string, args map[string]any) (any, error) {
	f.callN = name
	f.got = args
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}

// Describe 让 fakeAgentToolReg 充当 ToolDescriber（react 策略需要）。
func (f *fakeAgentToolReg) Describe(ctx context.Context, name string) (ToolSpec, bool) {
	lg := name
	if lg == "" {
		lg = f.callN
	}
	if lg != "" {
		for _, n := range f.names {
			if n == lg || strings.HasSuffix(n, "/"+lg) {
				return ToolSpec{Name: lg, Description: "fake tool", Parameters: map[string]any{"type": "object"}}, true
			}
		}
	}
	return ToolSpec{}, false
}

// fakeToolModel 是可编程 ToolCallingModel:按轮次返回预设 ChatResult。
type fakeToolModel struct {
	fakeModel
	results []ChatResult // 每轮一个;耗尽后返回最后一个（无工具）
	round   int
	got     []struct {
		msgs  []Message
		tools []ToolSpec
	}
}

func (f *fakeToolModel) ChatWithTools(ctx context.Context, model string, msgs []Message, tools []ToolSpec) (ChatResult, error) {
	f.calls++
	f.got = append(f.got, struct {
		msgs  []Message
		tools []ToolSpec
	}{msgs: msgs, tools: tools})
	if len(f.results) == 0 {
		return ChatResult{Content: "no plan"}, nil
	}
	if f.round >= len(f.results) {
		return f.results[len(f.results)-1], nil
	}
	r := f.results[f.round]
	f.round++
	return r, nil
}

func TestAgentNoTools(t *testing.T) {
	fake := &fakeModel{out: "agent reply"}
	n := buildDeps(t, spec.KindAgent, &spec.AgentConfig{
		Model: "m", Instruction: "be terse", Query: &spec.Source{Path: []string{"Query"}, Val: "hi"},
	}, ioDeps(nil, fake, nil))
	out, err := n.Invoke(t.Context(), map[string]any{"Query": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if out["output"] != "agent reply" {
		t.Fatalf("agent out = %v", out)
	}
	if fake.calls < 1 {
		t.Fatalf("model not called")
	}
	// persona 注入:系统消息 + user 消息
	msgs := fake.got
	if len(msgs) != 2 || msgs[0].Role != "system" || msgs[0].Content != "be terse" ||
		msgs[1].Role != "user" || msgs[1].Content != "hi" {
		t.Fatalf("messages = %v", msgs)
	}
}

func TestAgentNoFactory(t *testing.T) {
	b, _ := json.Marshal(&spec.AgentConfig{Model: "m"})
	_, err := StdRegistry().Build(context.Background(), &spec.NodeSpec{ID: "n", Kind: spec.KindAgent, Config: b}, Deps{})
	if err == nil || diag.CodeOf(err) != diag.RNodeError {
		t.Fatalf("err = %v, want RNodeError", err)
	}
}

func TestAgentToolCallLoop(t *testing.T) {
	// 模型:第 1 轮调用 sum(1,2) → 第 2 轮给最终答案。
	tr := &fakeAgentToolReg{names: []string{"math/sum"}, out: 3}
	fake := &fakeToolModel{results: []ChatResult{
		{Content: "let me compute", ToolCalls: []ToolCall{{ID: "c1", Name: "sum", Arguments: `{"a":1,"b":2}`}}},
		{Content: "answer is 3"},
	}}
	n := buildDeps(t, spec.KindAgent, &spec.AgentConfig{
		Model: "m",
		Tools: []spec.AgentToolWire{{Alias: "sum", ProviderName: "math", ToolName: "sum"}},
	}, ioDeps(nil, fake, tr))
	out, err := n.Invoke(t.Context(), map[string]any{"Query": "1+2?"})
	if err != nil {
		t.Fatal(err)
	}
	// 参考目标语义:final answer 拼接所有轮次响应。
	if out["output"] != "let me computeanswer is 3" {
		t.Fatalf("output = %q", out["output"])
	}
	// 工具调用回注册表逻辑名 math/sum,参数正确。
	if tr.callN != "math/sum" || tr.got["a"] != float64(1) || tr.got["b"] != float64(2) {
		t.Fatalf("tool call = %s %v", tr.callN, tr.got)
	}
	// 消息历史（无 instruction → 无 system）:user + assistant{tool_calls} + tool{sum=3}
	last := fake.got[len(fake.got)-1].msgs
	if len(last) != 3 {
		t.Fatalf("round-2 messages = %d (want 3): %v", len(last), last)
	}
	if last[1].Role != "assistant" || len(last[1].ToolCalls) != 1 || last[1].ToolCalls[0].Name != "sum" {
		t.Fatalf("assistant msg = %+v", last[1])
	}
	if last[2].Role != "tool" || last[2].ToolCallID != "c1" || last[2].Content != "3" {
		t.Fatalf("tool msg = %+v", last[2])
	}
}

func TestAgentToolMissingInRegistry(t *testing.T) {
	// 模型调用注册表里不存在的工具 → 错误文本回填,不崩溃。
	tr := &fakeAgentToolReg{names: []string{"math/sum"}, out: 3}
	fake := &fakeToolModel{results: []ChatResult{
		{Content: "calling ghost", ToolCalls: []ToolCall{{ID: "c1", Name: "ghost", Arguments: `{}`}}},
		{Content: "done"},
	}}
	n := buildDeps(t, spec.KindAgent, &spec.AgentConfig{
		Model: "m",
		Tools: []spec.AgentToolWire{{Alias: "sum", ProviderName: "math", ToolName: "sum"}},
	}, ioDeps(nil, fake, tr))
	out, err := n.Invoke(t.Context(), map[string]any{"Query": "q"})
	if err != nil {
		t.Fatal(err)
	}
	if out["output"] != "calling ghostdone" {
		t.Fatalf("output = %q", out["output"])
	}
	last := fake.got[len(fake.got)-1].msgs
	toolMsg := last[len(last)-1]
	if !strings.Contains(toolMsg.Content, "there is not a tool named ghost") {
		t.Fatalf("tool msg = %q", toolMsg.Content)
	}
}

func TestAgentMaxIteration(t *testing.T) {
	// 模型每轮都发工具调用 → 达上限报错。
	tr := &fakeAgentToolReg{names: []string{"math/sum"}, out: 3}
	fake := &fakeToolModel{results: []ChatResult{
		{Content: "loop", ToolCalls: []ToolCall{{ID: "c", Name: "sum", Arguments: `{}`}}},
	}}
	n := buildDeps(t, spec.KindAgent, &spec.AgentConfig{
		Model: "m",
		Tools: []spec.AgentToolWire{{Alias: "sum", ProviderName: "math", ToolName: "sum"}},
	}, ioDeps(nil, fake, tr))
	_, err := n.Invoke(t.Context(), map[string]any{"Query": "q"})
	if err == nil || !strings.Contains(err.Error(), "max iteration") {
		t.Fatalf("want max iteration error, got %v", err)
	}
	// 对齐参考目标：max_iteration_steps = max+1，收尾轮仍调用模型逼最终答案
	want := maxAgentIteration + 1
	if fake.calls != want {
		t.Fatalf("rounds = %d, want %d", fake.calls, want)
	}
}

func TestAgentFinalRoundNoTools(t *testing.T) {
	// 最后一轮必须清空工具（对齐参考目标:逼最终答案）。
	tr := &fakeAgentToolReg{names: []string{"math/sum"}, out: 3}
	fake := &fakeToolModel{results: []ChatResult{
		{Content: "still", ToolCalls: []ToolCall{{ID: "c", Name: "sum", Arguments: `{}`}}},
	}}
	n := buildDeps(t, spec.KindAgent, &spec.AgentConfig{
		Model: "m",
		Tools: []spec.AgentToolWire{{Alias: "sum", ProviderName: "math", ToolName: "sum"}},
	}, ioDeps(nil, fake, tr))
	_, _ = n.Invoke(t.Context(), map[string]any{"Query": "q"})
	last := fake.got[len(fake.got)-1]
	if last.tools != nil {
		t.Fatalf("final round tools = %v, want nil", last.tools)
	}
}

func TestAgentStepEvents(t *testing.T) {
	tr := &fakeAgentToolReg{names: []string{"math/sum"}, out: 3}
	fake := &fakeToolModel{results: []ChatResult{
		{Content: "thinking", ToolCalls: []ToolCall{{ID: "c1", Name: "sum", Arguments: `{"a":1,"b":2}`}}},
		{Content: "final"},
	}}
	var steps []AgentStep
	deps := ioDeps(nil, fake, tr)
	deps.AgentStep = func(st AgentStep) { steps = append(steps, st) }
	n := buildDeps(t, spec.KindAgent, &spec.AgentConfig{
		Model: "m",
		Tools: []spec.AgentToolWire{{Alias: "sum", ProviderName: "math", ToolName: "sum"}},
	}, deps)
	if _, err := n.Invoke(t.Context(), map[string]any{"Query": "q"}); err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 {
		t.Fatalf("steps = %d (want 3): %+v", len(steps), steps)
	}
	if steps[0].Thought != "thinking" {
		t.Fatalf("step0 = %+v", steps[0])
	}
	if steps[1].ToolName != "sum" || steps[1].Observation != "3" {
		t.Fatalf("step1 = %+v", steps[1])
	}
	if steps[2].Thought != "final" {
		t.Fatalf("step2 = %+v", steps[2])
	}
}

func TestAgentToolsWired(t *testing.T) {
	fake := &fakeModel{out: "with tools"}
	tr := &fakeAgentToolReg{names: []string{"biz/search"}, out: `{"hits":1}`}
	n := buildDeps(t, spec.KindAgent, &spec.AgentConfig{
		Model: "m",
		Tools: []spec.AgentToolWire{{Alias: "web", ProviderName: "biz", ToolName: "search"}},
	}, ioDeps(nil, fake, tr))
	out, err := n.Invoke(t.Context(), map[string]any{"Query": "q"})
	if err != nil {
		t.Fatal(err)
	}
	if out["output"] != "with tools" {
		t.Fatalf("agent out = %v", out)
	}
}

func TestAgentToolMissingBinding(t *testing.T) {
	tr := &fakeAgentToolReg{names: []string{"other"}}
	b, _ := json.Marshal(&spec.AgentConfig{
		Model: "m",
		Tools: []spec.AgentToolWire{{Alias: "web", ProviderName: "biz", ToolName: "search"}},
	})
	_, err := StdRegistry().Build(context.Background(), &spec.NodeSpec{ID: "n", Kind: spec.KindAgent, Config: b}, ioDeps(nil, &fakeModel{}, tr))
	if err == nil || diag.CodeOf(err) != diag.RBindingMissing {
		t.Fatalf("err = %v, want RBindingMissing", err)
	}
}

func TestAgentToolsWithoutRegistry(t *testing.T) {
	b, _ := json.Marshal(&spec.AgentConfig{
		Model: "m",
		Tools: []spec.AgentToolWire{{Alias: "web", ToolName: "search"}},
	})
	_, err := StdRegistry().Build(context.Background(), &spec.NodeSpec{ID: "n", Kind: spec.KindAgent, Config: b}, ioDeps(nil, &fakeModel{}, nil))
	if err == nil || diag.CodeOf(err) != diag.RNodeError {
		t.Fatalf("err = %v, want RNodeError (missing ToolRegistry)", err)
	}
}

func TestAgentRetry(t *testing.T) {
	fake := &fakeModel{err: errors.New("flaky"), out: "ok"}
	n := buildDeps(t, spec.KindAgent, &spec.AgentConfig{
		Model:     "m",
		Exception: &spec.ExceptionConfig{MaxRetry: 2},
		Query:     &spec.Source{Path: []string{"Query"}, Val: "q"},
	}, ioDeps(nil, fake, nil))
	if _, err := n.Invoke(t.Context(), map[string]any{"Query": "q"}); err == nil {
		t.Fatal("expected error after retries")
	}
	if fake.calls != 3 {
		t.Fatalf("calls = %d, want 3", fake.calls)
	}
}

func TestAgentOutputPortContract(t *testing.T) {
	// 输出端口与 llm 一致:只有 "output"
	fake := &fakeModel{out: "x"}
	n := buildDeps(t, spec.KindAgent, &spec.AgentConfig{Model: "m"}, ioDeps(nil, fake, nil))
	out, err := n.Invoke(t.Context(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out["output"] != "x" {
		t.Fatalf("out = %v", out)
	}
}

func TestAgentStrategyValidation(t *testing.T) {
	tr := &fakeAgentToolReg{names: []string{"math/sum"}}
	fake := &fakeToolModel{results: []ChatResult{{Content: "ok"}}}
	deps := ioDeps(nil, fake, tr)

	cases := []struct {
		name     string
		strategy string
		wantErr  string
	}{
		{"empty-is-function-calling", "", ""},
		{"function-calling-ok", "function-calling", ""},
		{"react-ok", "react", ""},
		{"unknown-rejected", "bogus", "unknown agent strategy"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, _ := json.Marshal(&spec.AgentConfig{
				Model:    "m",
				Strategy: c.strategy,
				Tools:    []spec.AgentToolWire{{Alias: "sum", ProviderName: "math", ToolName: "sum"}},
			})
			n, err := (Agent{}).Build(context.Background(), &spec.NodeSpec{ID: "n", Kind: spec.KindAgent, Config: b}, deps)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("want ok, got %v", err)
				}
				if n == nil {
					t.Fatal("node is nil")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, c.wantErr)
			}
		})
	}
}

// nodeSpecFor marshals an agent config into a NodeSpec for direct Build.
func nodeSpecFor(cfg *spec.AgentConfig) *spec.NodeSpec {
	b, _ := json.Marshal(cfg)
	return &spec.NodeSpec{ID: "n", Kind: spec.KindAgent, Config: b}
}

// ── strategy "react"（eino flow/agent/react 适配层） ─────────────────────

func TestAgentReactStrategyNoTools(t *testing.T) {
	// react + 无工具: 模型一轮出最终答案。
	fake := &fakeToolModel{results: []ChatResult{{Content: "react answer"}}}
	n, err := (Agent{}).Build(context.Background(), nodeSpecFor(&spec.AgentConfig{
		Model:    "m",
		Strategy: "react",
	}), ioDeps(nil, fake, nil))
	if err != nil {
		t.Fatalf("build react: %v", err)
	}
	out, err := n.Invoke(t.Context(), map[string]any{"Query": "hello"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out["output"] != "react answer" {
		t.Fatalf("output = %q, want react answer", out["output"])
	}
}

func TestAgentReactStrategyToolLoop(t *testing.T) {
	// react + 工具: 模型先调工具（sum 2+3）,观察回填后再给最终答案。
	tr := &fakeAgentToolReg{names: []string{"math/sum"}, out: 5}
	fake := &fakeToolModel{results: []ChatResult{
		{Content: "thinking", ToolCalls: []ToolCall{{ID: "c1", Name: "sum", Arguments: `{"a":2,"b":3}`}}},
		{Content: "the sum is 5"},
	}}
	n, err := (Agent{}).Build(context.Background(), nodeSpecFor(&spec.AgentConfig{
		Model:    "m",
		Strategy: "react",
		Tools:    []spec.AgentToolWire{{Alias: "sum", ProviderName: "math", ToolName: "sum"}},
	}), ioDeps(nil, fake, tr))
	if err != nil {
		t.Fatalf("build react: %v", err)
	}
	out, err := n.Invoke(t.Context(), map[string]any{"Query": "2+3?"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !strings.Contains(out["output"].(string), "the sum is 5") {
		t.Fatalf("output = %q, want final answer", out["output"])
	}
	// 工具被实际执行（fakeAgentToolReg.out=5 → 观察 "5"）。
	// 验证:第 2 轮模型收到的消息含 tool 观察。
	if len(fake.got) < 2 {
		t.Fatalf("model calls = %d, want >= 2 (tool round + final)", len(fake.got))
	}
	found := false
	for _, m := range fake.got[1].msgs {
		if m.Role == "tool" && strings.Contains(m.Content, "5") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no tool observation in round-2 messages: %+v", fake.got[1].msgs)
	}
}

// ── M15 回归: react 策略 LLMTrace 的 nodeID 必须传节点 ID 而非 model 名 ──

func TestAgentReactTraceNodeID(t *testing.T) {
	// 回归: reactModelAdapter.Generate 曾把 model 名当 nodeID 传给
	// LLMTrace（a.trace(a.model, a.model, ours)）,且 adapter 未保存
	// nodeID —— react 策略下 --debug 的 llm[<nodeID>] 显示错误。
	// 契约: trace 第一参数 = 节点 ID（与 function-calling 路径一致）。
	fake := &fakeToolModel{results: []ChatResult{{Content: "react answer"}}}
	var traceID, traceModel string
	trace := func(nodeID, model string, _ []Message) {
		traceID, traceModel = nodeID, model
	}
	deps := ioDeps(nil, fake, nil)
	deps.LLMTrace = trace
	n, err := (Agent{}).Build(context.Background(), nodeSpecFor(&spec.AgentConfig{
		Model: "m", Strategy: "react",
	}), deps)
	if err != nil {
		t.Fatalf("build react: %v", err)
	}
	if _, err := n.Invoke(t.Context(), map[string]any{"Query": "hello"}); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if traceID != "n" {
		t.Fatalf("trace nodeID = %q, want %q (node ID, not model name)", traceID, "n")
	}
	if traceModel != "m" {
		t.Fatalf("trace model = %q, want m", traceModel)
	}
}

func TestAgentReactTraceNodeIDWithTools(t *testing.T) {
	// 回归: react 工具循环中 eino 会经 WithTools 创建新 adapter,
	// nodeID 必须在 WithTools 透传（否则第二轮 trace 又丢 nodeID）。
	tr := &fakeAgentToolReg{names: []string{"math/sum"}, out: 5}
	fake := &fakeToolModel{results: []ChatResult{
		{Content: "thinking", ToolCalls: []ToolCall{{ID: "c1", Name: "sum", Arguments: `{"a":2,"b":3}`}}},
		{Content: "the sum is 5"},
	}}
	var firstID, lastID string
	trace := func(nodeID, _ string, _ []Message) {
		if firstID == "" {
			firstID = nodeID
		}
		lastID = nodeID
	}
	deps := ioDeps(nil, fake, tr)
	deps.LLMTrace = trace
	n, err := (Agent{}).Build(context.Background(), nodeSpecFor(&spec.AgentConfig{
		Model: "m", Strategy: "react",
		Tools: []spec.AgentToolWire{{Alias: "sum", ProviderName: "math", ToolName: "sum"}},
	}), deps)
	if err != nil {
		t.Fatalf("build react: %v", err)
	}
	if _, err := n.Invoke(t.Context(), map[string]any{"Query": "2+3?"}); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if firstID != "n" || lastID != "n" {
		t.Fatalf("trace nodeID first/last = %q/%q, want n/n (WithTools must carry nodeID)", firstID, lastID)
	}
}
