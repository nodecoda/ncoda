package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/llm"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

type richCaptureFactory struct {
	calls int
	resp  ModelResponse
}

type schemaCaptureFactory struct {
	calls  int
	schema *llm.LLMSchema
}

func (f *schemaCaptureFactory) Chat(context.Context, string, []Message) (string, error) {
	return "legacy path must not be used", nil
}

func (f *schemaCaptureFactory) ChatWithSchema(_ context.Context, _ string, _ []Message, schema *llm.LLMSchema) (ModelResponse, error) {
	f.calls++
	f.schema = schema
	return ModelResponse{Text: `{"ok":true}`}, nil
}

func TestLLMV2PassesPayloadSchemaToCapableModel(t *testing.T) {
	schema := llm.Record("R", []llm.LLMField{{Name: "ok", Required: true, Schema: llm.Bool()}})
	f := &schemaCaptureFactory{}
	n := &llmNode{cfg: &spec.LLMConfig{Model: "m", Result: &spec.LLMResultConfig{PayloadSchema: schema}}, mf: f, id: "n"}
	out, err := n.Invoke(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 || f.schema != schema {
		t.Fatalf("schema-aware calls=%d schema=%p, want 1 and %p", f.calls, f.schema, schema)
	}
	if got := out["text"].(map[string]any)["ok"]; got != true {
		t.Fatalf("decoded text.ok = %#v", got)
	}
}

func (f *richCaptureFactory) Chat(context.Context, string, []Message) (string, error) {
	return f.resp.Text, nil
}
func (f *richCaptureFactory) ChatWithMetadata(context.Context, string, []Message) (ModelResponse, error) {
	f.calls++
	return f.resp, nil
}

func TestLLMV2StructuredDecodeAndMetadata(t *testing.T) {
	reason := "chain of thought"
	prompt, completion, total := int64(2), int64(3), int64(5)
	f := &richCaptureFactory{resp: ModelResponse{
		Text: `{"answer":"ok","score":3}`, ReasoningContent: &reason,
		Usage: &LLMUsage{PromptTokens: &prompt, CompletionTokens: &completion, TotalTokens: &total},
	}}
	cfg := &spec.LLMConfig{Model: "m", Result: &spec.LLMResultConfig{
		PayloadSchema: llm.Record("Answer", []llm.LLMField{
			{Name: "answer", Required: true, Schema: llm.String()},
			{Name: "score", Required: true, Schema: llm.Int64()},
		}),
		Metadata: spec.LLMMetadataConfig{ReasoningContent: true, Usage: spec.LLMUsageConfig{PromptTokens: true, TotalTokens: true}, FinishReason: true},
	}}
	out, err := (&llmNode{cfg: cfg, mf: f, id: "node-1"}).Invoke(context.Background(), map[string]any{"input": []any{"hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 {
		t.Fatalf("rich calls = %d, want 1", f.calls)
	}
	text, ok := out["text"].(map[string]any)
	if !ok || text["answer"] != "ok" || text["score"] != int64(3) {
		t.Fatalf("decoded text = %#v", out["text"])
	}
	if out["output"] == nil || out["reasoning_content"] != reason || out["finish_reason"] != nil {
		t.Fatalf("normalized output = %#v", out)
	}
	u, ok := out["usage"].(*LLMUsage)
	if !ok || u.PromptTokens == nil || u.CompletionTokens != nil || u.TotalTokens == nil {
		t.Fatalf("normalized usage = %#v", out["usage"])
	}
	envelope, ok := out["response"].(map[string]any)
	if !ok || !reflect.DeepEqual(envelope["text"], out["text"]) || envelope["reasoning_content"] != reason || envelope["finish_reason"] != nil || !reflect.DeepEqual(envelope["usage"], out["usage"]) {
		t.Fatalf("normalized response envelope = %#v", out["response"])
	}
}

func TestLLMV2RichContentCompatibilityAndTextPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name string
		resp ModelResponse
		want string
	}{
		{name: "content-only", resp: ModelResponse{Content: "legacy"}, want: "legacy"},
		{name: "text-precedes-content", resp: ModelResponse{Text: "canonical", Content: "legacy"}, want: "canonical"},
		{name: "explicit-empty-text-precedes-content", resp: ModelResponse{TextPresent: true, Content: "legacy"}, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &richCaptureFactory{resp: tc.resp}
			n := &llmNode{cfg: &spec.LLMConfig{Model: "m", Result: stringLLMResult()}, mf: f, id: "n"}
			out, err := n.Invoke(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := out["text"]; got != tc.want {
				t.Fatalf("text = %#v, want %q", got, tc.want)
			}
		})
	}
}

func TestLLMV2RejectsMissingPayloadSchemaBeforeModelCall(t *testing.T) {
	f := &sequenceFactory{outs: []string{"unchecked"}}
	n := &llmNode{cfg: &spec.LLMConfig{Model: "m", Result: &spec.LLMResultConfig{}}, mf: f, id: "n"}
	if out, err := n.Invoke(context.Background(), nil); err == nil || out != nil || f.calls != 0 {
		t.Fatalf("out=%#v err=%v calls=%d, want schema error before invocation", out, err, f.calls)
	}
}

type sequenceFactory struct {
	outs  []string
	calls int
}

func (f *sequenceFactory) Chat(context.Context, string, []Message) (string, error) {
	i := f.calls
	f.calls++
	if i >= len(f.outs) {
		return "{}", nil
	}
	return f.outs[i], nil
}

func TestLLMV2DecodeFailureUsesOperationRetry(t *testing.T) {
	f := &sequenceFactory{outs: []string{"not-json", `{"ok":true}`}}
	n := &llmNode{cfg: &spec.LLMConfig{Model: "m", Exception: &spec.ExceptionConfig{MaxRetry: 1}, Result: &spec.LLMResultConfig{PayloadSchema: llm.Record("R", []llm.LLMField{{Name: "ok", Required: true, Schema: llm.Bool()}})}}, mf: f, id: "n"}
	out, err := n.Invoke(context.Background(), nil)
	if err != nil || out["text"].(map[string]any)["ok"] != true {
		t.Fatalf("retry result = %#v, err=%v", out, err)
	}
	if f.calls != 2 {
		t.Fatalf("calls = %d, want 2 (one per attempt)", f.calls)
	}
}

func TestLLMV2DecodeFailureDiagnosticAndNoRawPayload(t *testing.T) {
	f := &sequenceFactory{outs: []string{"```json\n{}\n```"}}
	n := &llmNode{cfg: &spec.LLMConfig{Model: "secret-model", Result: &spec.LLMResultConfig{PayloadSchema: llm.Int64()}}, mf: f, id: "node-x"}
	out, err := n.Invoke(context.Background(), nil)
	if out != nil || err == nil || !strings.Contains(err.Error(), "node-x") || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("err = %v, out = %#v", err, out)
	}
	if strings.Contains(err.Error(), "```json") {
		t.Fatalf("raw model payload leaked: %v", err)
	}
}

func TestLLMV1NormalizeAndCancellation(t *testing.T) {
	s := &spec.Spec{Version: spec.VersionV1, Nodes: []spec.NodeSpec{{ID: "n", Kind: spec.KindLLM, Config: json.RawMessage(`{"model":"m"}`)}}}
	norm, err := s.Normalize()
	if err != nil || norm.Version != spec.VersionV2 {
		t.Fatalf("v1 normalize = %#v, err=%v", norm, err)
	}
	var cfg spec.LLMConfig
	if err := json.Unmarshal(norm.Nodes[0].Config, &cfg); err != nil || cfg.Result == nil || cfg.Result.PayloadSchema.Kind != llm.KindString {
		t.Fatalf("normalized v1 llm = %s, err=%v", norm.Nodes[0].Config, err)
	}
	f := &sequenceFactory{outs: []string{"x"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = (&llmNode{cfg: &cfg, mf: f, id: "n"}).Invoke(ctx, nil)
	if !errors.Is(err, context.Canceled) || f.calls != 0 {
		t.Fatalf("cancel err=%v calls=%d", err, f.calls)
	}
}
