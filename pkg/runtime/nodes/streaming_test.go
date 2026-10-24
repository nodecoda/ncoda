package nodes

// streaming_test.go — M12 流式输出:delta 合并、fc/react 流式路径
// （thought 不转发、最终答案回放、工具循环、降级）。

import (
	"context"
	"io"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// ── delta 合并 ───────────────────────────────────────────────────────────

func TestMergeToolCallDeltas(t *testing.T) {
	got := mergeToolCallDeltas(nil, []ToolCall{
		{Index: 0, ID: "c1", Name: "weather", Arguments: `{"city":"`},
		{Index: 1, ID: "c2", Name: "clock", Arguments: ``},
		{Index: 0, Arguments: `北京"}`},
	})
	if len(got) != 2 {
		t.Fatalf("merged = %+v, want 2", got)
	}
	if got[0].ID != "c1" || got[0].Name != "weather" || got[0].Arguments != `{"city":"北京"}` {
		t.Fatalf("tc0 = %+v", got[0])
	}
	// 再次合并（模拟跨轮累积不存在;单轮内多次 delta 追加）
	got = mergeToolCallDeltas(got, []ToolCall{{Index: 1, Arguments: `{"tz":"cst"}`}})
	if got[1].Arguments != `{"tz":"cst"}` {
		t.Fatalf("tc1 = %+v", got[1])
	}
	// Index < 0 → 直接追加（非流式单条）
	got = mergeToolCallDeltas(got, []ToolCall{{Index: -1, ID: "x", Name: "y", Arguments: "{}"}})
	if len(got) != 3 {
		t.Fatalf("merged = %+v, want 3", got)
	}
}

func TestChunkReaderAndDrain(t *testing.T) {
	r := NewChunkReader([]StreamChunk{
		{Content: "a"},
		{Content: "b", ToolCalls: []ToolCall{{Index: 0, Arguments: "{"}}},
		{ToolCalls: []ToolCall{{Index: 0, Arguments: "}"}}},
	})
	content, tcs, err := drainStream(r)
	if err != nil {
		t.Fatal(err)
	}
	if content != "ab" {
		t.Fatalf("content = %q, want ab", content)
	}
	if len(tcs) != 1 || tcs[0].Arguments != "{}" {
		t.Fatalf("toolcalls = %+v, want merged {}", tcs)
	}
	// EOF 后再次 Recv
	ch, err := r.Recv()
	if err != io.EOF {
		t.Fatalf("second Recv = %+v, %v, want EOF", ch, err)
	}
}

// ── fc agent 流式 ────────────────────────────────────────────────────────

// fakeStreamModel 是可编程流式模型:按轮返回预设 chunk;同时保留
// ChatWithTools 降级路径（返回空结果）。
type fakeStreamModel struct {
	fakeToolModel
	streams [][]StreamChunk
	round   int
}

func (f *fakeStreamModel) StreamWithTools(ctx context.Context, model string, msgs []Message, tools []ToolSpec) (StreamReader, error) {
	if f.round >= len(f.streams) {
		return nil, ErrStreamNotSupported
	}
	chunks := f.streams[f.round]
	f.round++
	return NewChunkReader(chunks), nil
}

func streamAgentNode(t *testing.T, fake ModelFactory, cfg *spec.AgentConfig, reg ToolRegistry) NodeImpl {
	t.Helper()
	cfg.Model = "m"
	cfg.Query = &spec.Source{Path: []string{"Query"}, Name: "q"}
	if cfg.Strategy == "" {
		cfg.Strategy = "function-calling"
	}
	n, err := (Agent{}).Build(context.Background(), nodeSpecFor(cfg), Deps{
		Models: fake, Tools: reg, TokenSink: func(text string) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAgentStreamFinalAnswerReplay(t *testing.T) {
	var sink []string
	fake := &fakeStreamModel{streams: [][]StreamChunk{
		{{Content: "你"}, {Content: "好"}, {Content: "！"}},
	}}
	cfg := &spec.AgentConfig{Instruction: "persona"}
	n := streamAgentNode(t, fake, cfg, nil)
	n.(*agentNode).sink = func(text string) { sink = append(sink, text) }

	if _, err := n.Invoke(t.Context(), map[string]any{"Query": "hi"}); err != nil {
		t.Fatal(err)
	}
	if len(sink) != 3 || sink[0] != "你" || sink[1] != "好" || sink[2] != "！" {
		t.Fatalf("sink = %v, want [你 好 ！]", sink)
	}
	// 输出契约不变:最终答案完整。
}

func TestAgentStreamThoughtNotForwarded(t *testing.T) {
	// 语义（M9 契约）:final answer 拼接所有轮次（thought 也拼进输出）。
	// M12 流式 sink 只转发"最终答案轮"增量 —— thought 不流到终端。
	var sink []string
	fake := &fakeStreamModel{streams: [][]StreamChunk{
		// 轮1:thought + 工具调用 delta
		{{Content: "让我想想"}, {ToolCalls: []ToolCall{{Index: 0, ID: "c1", Name: "sum", Arguments: `{"a":1,"b":2}`}}}},
		// 轮2:最终答案
		{{Content: "答案是 3"}},
	}}
	reg := &fakeAgentToolReg{names: []string{"math/sum"}, out: 3}
	cfg := &spec.AgentConfig{Tools: []spec.AgentToolWire{{Alias: "sum", ProviderName: "math", ToolName: "sum"}}}
	n := streamAgentNode(t, fake, cfg, reg)
	n.(*agentNode).sink = func(text string) { sink = append(sink, text) }
	n.(*agentNode).reg = reg

	out, err := n.Invoke(t.Context(), map[string]any{"Query": "1+2?"})
	if err != nil {
		t.Fatal(err)
	}
	if out["output"] != "让我想想答案是 3" {
		t.Fatalf("output = %q (拼接语义不变)", out["output"])
	}
	if len(sink) != 1 || sink[0] != "答案是 3" {
		t.Fatalf("sink = %v, want [答案是 3] (thought not streamed)", sink)
	}
}

func TestAgentStreamMultiToolDeltaMerge(t *testing.T) {
	var sink []string
	fake := &fakeStreamModel{streams: [][]StreamChunk{
		// 工具调用按 delta 拆分（Index 合并）: name 在首 chunk,args 分两段
		{{ToolCalls: []ToolCall{{Index: 0, ID: "c1", Name: "sum"}}},
			{ToolCalls: []ToolCall{{Index: 0, Arguments: `{"a":2,"b":3}`}}}},
		{{Content: "结果 5"}},
	}}
	reg := &fakeAgentToolReg{names: []string{"math/sum"}, out: 5}
	cfg := &spec.AgentConfig{Tools: []spec.AgentToolWire{{Alias: "sum", ProviderName: "math", ToolName: "sum"}}}
	n := streamAgentNode(t, fake, cfg, reg)
	n.(*agentNode).sink = func(text string) { sink = append(sink, text) }
	n.(*agentNode).reg = reg

	out, err := n.Invoke(t.Context(), map[string]any{"Query": "2+3?"})
	if err != nil {
		t.Fatal(err)
	}
	if out["output"] != "结果 5" {
		t.Fatalf("output = %v", out["output"])
	}
	if reg.callN != "math/sum" || reg.got["a"] != float64(2) || reg.got["b"] != float64(3) {
		t.Fatalf("tool call = %s %v", reg.callN, reg.got)
	}
	if len(sink) != 1 || sink[0] != "结果 5" {
		t.Fatalf("sink = %v", sink)
	}
}

func TestAgentStreamFallbackWhenNotSupported(t *testing.T) {
	// 模型不支持流式 → 降级 ChatWithTools,输出完整、sink 不触发。
	var sink []string
	fake := &fakeToolModel{results: []ChatResult{{Content: "plain answer"}}}
	cfg := &spec.AgentConfig{}
	n := streamAgentNode(t, fake, cfg, nil)
	n.(*agentNode).sink = func(text string) { sink = append(sink, text) }

	out, err := n.Invoke(t.Context(), map[string]any{"Query": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if out["output"] != "plain answer" {
		t.Fatalf("output = %v", out["output"])
	}
	if len(sink) != 0 {
		t.Fatalf("sink = %v, want none", sink)
	}
}

// ── react 流式 ───────────────────────────────────────────────────────────

func TestReactStreamForward(t *testing.T) {
	var sink []string
	fake := &fakeStreamModel{streams: [][]StreamChunk{
		{{Content: "react "}, {Content: "answer"}},
	}}
	cfg := &spec.AgentConfig{Strategy: "react", Instruction: "persona"}
	n := streamAgentNode(t, fake, cfg, nil)
	n.(*reactNode).sink = func(text string) { sink = append(sink, text) }

	out, err := n.Invoke(t.Context(), map[string]any{"Query": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if out["output"] != "react answer" {
		t.Fatalf("output = %q", out["output"])
	}
	if len(sink) != 2 || sink[0] != "react " || sink[1] != "answer" {
		t.Fatalf("sink = %v, want [react  answer]", sink)
	}
}

func TestReactStreamFallback(t *testing.T) {
	// react + 非流式模型（仅 ChatWithTools）→ 降级 Generate。
	var sink []string
	fake := &fakeToolModel{results: []ChatResult{{Content: "gen answer"}}}
	cfg := &spec.AgentConfig{Strategy: "react"}
	n := streamAgentNode(t, fake, cfg, nil)
	n.(*reactNode).sink = func(text string) { sink = append(sink, text) }

	out, err := n.Invoke(t.Context(), map[string]any{"Query": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if out["output"] != "gen answer" {
		t.Fatalf("output = %q", out["output"])
	}
	if len(sink) != 0 {
		t.Fatalf("sink = %v, want none", sink)
	}
}
