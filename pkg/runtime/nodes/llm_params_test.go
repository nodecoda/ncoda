package nodes

// llm_params_test.go — llm config 的 provider 参数转发契约（PR1）：
// OptionsModelFactory 收到采样参数与 schema；非 provider 键不得混入；
// 旧工厂（Chat / Rich）路径保持兼容。

import (
	"context"
	"testing"

	"github.com/nodecoda/ncoda/pkg/llm"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// 本包测试用两个合成命名空间验证"声明给别的目标 ⇒ 不转发"，
// 不拼写任何真实平台名（内核构建不带平台命名空间）。
func init() { SetTargetParamNamespaces("targeta", "targetb") }

type optionsCaptureFactory struct {
	optionsCalls int
	richCalls    int
	opts         CallOptions
	msgs         []Message
}

func (f *optionsCaptureFactory) Chat(context.Context, string, []Message) (string, error) {
	return "legacy", nil
}

func (f *optionsCaptureFactory) ChatWithMetadata(context.Context, string, []Message) (ModelResponse, error) {
	f.richCalls++
	return ModelResponse{Text: "rich"}, nil
}

func (f *optionsCaptureFactory) ChatWithOptions(_ context.Context, _ string, msgs []Message, opts CallOptions) (ModelResponse, error) {
	f.optionsCalls++
	f.opts = opts
	f.msgs = msgs
	return ModelResponse{Text: "ok"}, nil
}

func TestLLMCallParamsForwardedToOptionsFactory(t *testing.T) {
	cfg := &spec.LLMConfig{
		Model: "m",
		Config: []spec.ConfigPair{
			{Key: "messages", Value: []any{map[string]any{"role": "user", "content": "hi"}}},
			{Key: "temperature", Value: 0.9},
			{Key: "top_p", Value: 0.8},
			{Key: "max_tokens", Value: 256},
			// 未知名：今天类型表不认识，但作者写了就必须送达 gateway，
			// 由 gateway 报「目标没消费」，而不是在 runtime 静默丢弃。
			{Key: "seed", Value: 42},
			// 以下全部是「不是 provider 参数」的键，必须被排除。
			{Key: "model_name", Value: "display"},
			{Key: "timeout_ms", Value: 30000},
			{Key: "stream", Value: true},
			{Key: "response_format", Value: 2},
			{Key: "reasoning_format", Value: "separated"},
			{Key: "generation_diversity", Value: "balance"},
			{Key: "systemPrompt", Value: "legacy-system"},
			{Key: "userPrompt", Value: "legacy-user"},
			{Key: "model", Value: "alias"},
		},
		Result: &spec.LLMResultConfig{PayloadSchema: llm.String()},
	}

	f := &optionsCaptureFactory{}
	if _, err := (&llmNode{cfg: cfg, mf: f, id: "n"}).Invoke(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if f.optionsCalls != 1 || f.richCalls != 0 {
		t.Fatalf("options=%d rich=%d, want options path once and rich never", f.optionsCalls, f.richCalls)
	}

	want := map[string]any{
		"temperature": 0.9,
		"top_p":       0.8,
		"max_tokens":  256,
		"seed":        42,
	}
	if len(f.opts.Params) != len(want) {
		t.Fatalf("params = %#v, want exactly %#v", f.opts.Params, want)
	}
	for k, v := range want {
		if got := f.opts.Params[k]; got != v {
			t.Fatalf("params[%q] = %#v, want %#v (all=%#v)", k, got, v, f.opts.Params)
		}
	}
	for _, k := range []string{
		"messages", "model_name", "timeout_ms", "stream", "response_format",
		"reasoning_format", "generation_diversity", "systemPrompt", "userPrompt", "model",
	} {
		if _, ok := f.opts.Params[k]; ok {
			t.Fatalf("non-provider key %q leaked into Params: %#v", k, f.opts.Params)
		}
	}
	if f.opts.Schema == nil {
		t.Fatal("schema must be carried in CallOptions")
	}
	if len(f.msgs) != 1 || f.msgs[0].Content != "hi" {
		t.Fatalf("messages = %#v, want the config message", f.msgs)
	}
}

func TestLLMCallParamsNilWhenNoProviderParams(t *testing.T) {
	cfg := &spec.LLMConfig{
		Model: "m",
		Config: []spec.ConfigPair{
			{Key: "messages", Value: []any{map[string]any{"role": "user", "content": "hi"}}},
			{Key: "timeout_ms", Value: 1000},
		},
		Result: &spec.LLMResultConfig{PayloadSchema: llm.String()},
	}
	f := &optionsCaptureFactory{}
	if _, err := (&llmNode{cfg: cfg, mf: f, id: "n"}).Invoke(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if f.opts.Params != nil {
		t.Fatalf("Params = %#v, want nil when only non-provider keys are present", f.opts.Params)
	}
}

// 旧工厂不实现 OptionsModelFactory：带采样参数的 config 不得改变既有行为。
func TestLLMCallParamsLegacyFactoryUnaffected(t *testing.T) {
	cfg := &spec.LLMConfig{
		Model: "m",
		Config: []spec.ConfigPair{
			{Key: "messages", Value: []any{map[string]any{"role": "user", "content": "hi"}}},
			{Key: "temperature", Value: 0.9},
		},
		Result: &spec.LLMResultConfig{PayloadSchema: llm.String()},
	}
	capt := &captureFactory{}
	out, err := (&llmNode{cfg: cfg, mf: capt, id: "n"}).Invoke(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["output"] != "ok" {
		t.Fatalf("output = %#v", out["output"])
	}
}

// ── 命名空间路由（2026-10-20）────────────────────────────────────────

func TestLLMCallParamsNamespaceRouting(t *testing.T) {
	cfg := &spec.LLMConfig{
		Model: "m",
		Config: []spec.ConfigPair{
			{Key: "messages", Value: []any{map[string]any{"role": "user", "content": "hi"}}},
			// openai:* 是给 provider 的 ⇒ 剥前缀后转发。
			{Key: "openai:seed", Value: 7},
			// <target>:* 声明给别的目标 ⇒ 不发给 provider（命名空间由适配器登记）。
			{Key: "targeta:generation_diversity", Value: "balance"},
			{Key: "targetb:reasoning_format", Value: "separated"},
			// x-* 供应商自定义参数 ⇒ 原样转发。
			{Key: "x-custom", Value: "v"},
		},
		Result: &spec.LLMResultConfig{PayloadSchema: llm.String()},
	}
	f := &optionsCaptureFactory{}
	if _, err := (&llmNode{cfg: cfg, mf: f, id: "n"}).Invoke(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"seed": 7, "x-custom": "v"}
	if len(f.opts.Params) != len(want) {
		t.Fatalf("params = %#v, want exactly %#v", f.opts.Params, want)
	}
	for k, v := range want {
		if got := f.opts.Params[k]; got != v {
			t.Fatalf("params[%q] = %#v, want %#v", k, got, v)
		}
	}
	for _, k := range []string{"openai:seed", "targeta:generation_diversity", "targetb:reasoning_format"} {
		if _, ok := f.opts.Params[k]; ok {
			t.Fatalf("namespaced key %q leaked into Params: %#v", k, f.opts.Params)
		}
	}
}

// ── 废弃提示词表面：messages 缺席时作为单条消息 ─────────────────────

func TestLLMPromptAliasesBecomeMessages(t *testing.T) {
	cfg := &spec.LLMConfig{
		Model: "m",
		Config: []spec.ConfigPair{
			{Key: "systemPrompt", Value: "sys"},
			{Key: "userPrompt", Value: "usr"},
		},
		Result: &spec.LLMResultConfig{PayloadSchema: llm.String()},
	}
	f := &optionsCaptureFactory{}
	if _, err := (&llmNode{cfg: cfg, mf: f, id: "n"}).Invoke(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(f.msgs) != 2 ||
		f.msgs[0].Role != "system" || f.msgs[0].Content != "sys" ||
		f.msgs[1].Role != "user" || f.msgs[1].Content != "usr" {
		t.Fatalf("messages = %#v, want system+user from prompt aliases", f.msgs)
	}
}

func TestLLMMessagesWinOverPromptAliases(t *testing.T) {
	cfg := &spec.LLMConfig{
		Model: "m",
		Config: []spec.ConfigPair{
			{Key: "messages", Value: []any{map[string]any{"role": "user", "content": "from-messages"}}},
			{Key: "userPrompt", Value: "from-alias"},
		},
		Result: &spec.LLMResultConfig{PayloadSchema: llm.String()},
	}
	f := &optionsCaptureFactory{}
	if _, err := (&llmNode{cfg: cfg, mf: f, id: "n"}).Invoke(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(f.msgs) != 1 || f.msgs[0].Content != "from-messages" {
		t.Fatalf("messages = %#v, want the explicit messages array to win", f.msgs)
	}
}
