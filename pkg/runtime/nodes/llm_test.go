package nodes

// llm_test.go — llm 节点:Config 字面量消息重建 + {{block_output_<id>.<port>}}
// 模板替换（M5 CLI 面 llm 语料依赖的语义）。

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nodecoda/ncoda/pkg/llm"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func stringLLMResult() *spec.LLMResultConfig {
	return &spec.LLMResultConfig{PayloadSchema: llm.String()}
}

// captureFactory records the message list it receives.
type captureFactory struct {
	got []Message
}

func (c *captureFactory) Chat(_ context.Context, _ string, msgs []Message) (string, error) {
	c.got = msgs
	return "ok", nil
}

func TestLLMConfigMessagesTemplate(t *testing.T) {
	// 模拟 spec 反序列化后的形状:ConfigPair{Key:"messages"} 的值是
	// []any{map[string]any{role, content}},content 带 block 引用。
	cfg := &spec.LLMConfig{
		Model: "m", Result: stringLLMResult(),
		Config: []spec.ConfigPair{{
			Key: "messages",
			Value: []any{
				map[string]any{"role": "system", "content": "用 {{block_output_100001.language}} 回答"},
				map[string]any{"role": "user", "content": "{{block_output_100001.query}}"},
			},
		}},
	}
	impl := &llmNode{cfg: cfg, mf: &captureFactory{}}
	in := map[string]any{"language": "English", "query": "hi"}
	out, err := impl.Invoke(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out["output"] != "ok" {
		t.Fatalf("output = %v", out["output"])
	}
	capt := impl.mf.(*captureFactory)
	if len(capt.got) != 2 {
		t.Fatalf("got %d messages, want 2: %#v", len(capt.got), capt.got)
	}
	if capt.got[0].Role != "system" || capt.got[0].Content != "用 English 回答" {
		t.Fatalf("system msg = %#v, want template resolved", capt.got[0])
	}
	if capt.got[1].Role != "user" || capt.got[1].Content != "hi" {
		t.Fatalf("user msg = %#v, want query passthrough", capt.got[1])
	}
}

func TestLLMConfigMessagesMissingRef(t *testing.T) {
	// 引用缺失 → "<missing>"（RenderTemplate 契约）;消息结构保留。
	cfg := &spec.LLMConfig{
		Model: "m", Result: stringLLMResult(),
		Config: []spec.ConfigPair{{
			Key: "messages",
			Value: []any{
				map[string]any{"role": "user", "content": "hi {{block_output_999.nope}}"},
			},
		}},
	}
	impl := &llmNode{cfg: cfg, mf: &captureFactory{}}
	if _, err := impl.Invoke(context.Background(), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	capt := impl.mf.(*captureFactory)
	if len(capt.got) != 1 || capt.got[0].Content != "hi <missing>" {
		t.Fatalf("msg = %#v, want hi <missing>", capt.got)
	}
}

// 直通优先:装配输入里显式 messages 数组优先于 Config 字面量。
func TestLLMInputMessagesPriority(t *testing.T) {
	cfg := &spec.LLMConfig{Model: "m", Result: stringLLMResult()}
	impl := &llmNode{cfg: cfg, mf: &captureFactory{}}
	in := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "direct"}}}
	if _, err := impl.Invoke(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	capt := impl.mf.(*captureFactory)
	if len(capt.got) != 1 || capt.got[0].Content != "direct" {
		t.Fatalf("msg = %#v, want direct passthrough", capt.got)
	}
}

// spec 反序列化形状验证:ir.Message 的 json tag 必须是小写 role/content
// （runtime 消费契约;后端渲染用 Go 字段,不受影响）。
func TestLLMMessageJSONShape(t *testing.T) {
	b, err := json.Marshal(struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{Role: "user", Content: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"role":"user","content":"x"}` {
		t.Fatalf("message json shape = %s", b)
	}
}

// M14:conversation 变量引用 {{global_variable_app.<name>}} 在 llm prompt
// 中解析（compile 层以 "global_variable_app.<name>" 键注入 inputs）。
func TestLLMConfigGlobalVariableRef(t *testing.T) {
	cfg := &spec.LLMConfig{
		Model: "m", Result: stringLLMResult(),
		Config: []spec.ConfigPair{{
			Key: "messages",
			Value: []any{
				map[string]any{"role": "user", "content": "user={{global_variable_app.username}}"},
			},
		}},
	}
	impl := &llmNode{cfg: cfg, mf: &captureFactory{}}
	out, err := impl.Invoke(context.Background(), map[string]any{"global_variable_app.username": "updated"})
	if err != nil {
		t.Fatal(err)
	}
	if out["output"] != "ok" {
		t.Fatalf("output = %v", out["output"])
	}
	capt := impl.mf.(*captureFactory)
	if len(capt.got) != 1 || capt.got[0].Content != "user=updated" {
		t.Fatalf("msg = %#v, want user=updated", capt.got)
	}
}

// M14:global_variable_app 引用缺失 → <missing>（确定性契约）。
func TestLLMConfigGlobalVariableMissing(t *testing.T) {
	cfg := &spec.LLMConfig{
		Model: "m", Result: stringLLMResult(),
		Config: []spec.ConfigPair{{
			Key: "messages",
			Value: []any{
				map[string]any{"role": "user", "content": "user={{global_variable_app.nope}}"},
			},
		}},
	}
	impl := &llmNode{cfg: cfg, mf: &captureFactory{}}
	if _, err := impl.Invoke(context.Background(), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	capt := impl.mf.(*captureFactory)
	if len(capt.got) != 1 || capt.got[0].Content != "user=<missing>" {
		t.Fatalf("msg = %#v, want user=<missing>", capt.got)
	}
}

// M15:LLMTrace 回调观察实际发往模型的 prompt（块引用已替换）。
func TestLLMTraceHook(t *testing.T) {
	cfg := &spec.LLMConfig{
		Model: "m", Result: stringLLMResult(),
		Config: []spec.ConfigPair{{
			Key: "messages",
			Value: []any{
				map[string]any{"role": "user", "content": "hi {{block_output_100001.query}}"},
			},
		}},
	}
	var gotID, gotModel string
	var gotMsgs []Message
	trace := func(nodeID, model string, msgs []Message) {
		gotID, gotModel = nodeID, model
		gotMsgs = msgs
	}
	b, _ := json.Marshal(cfg)
	impl, err := (LLM{}).Build(context.Background(), &spec.NodeSpec{ID: "100007", Kind: spec.KindLLM, Config: b},
		Deps{Models: &captureFactory{}, LLMTrace: trace})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := impl.Invoke(context.Background(), map[string]any{"query": "world"}); err != nil {
		t.Fatal(err)
	}
	if gotID != "100007" || gotModel != "m" {
		t.Fatalf("trace id/model = %q/%q, want 100007/m", gotID, gotModel)
	}
	if len(gotMsgs) != 1 || gotMsgs[0].Content != "hi world" {
		t.Fatalf("trace msgs = %#v, want [hi world]", gotMsgs)
	}
}

// ── M16: resolveBlockTemplate 复合值渲染（RAG 结果注入 prompt） ────────

func TestResolveBlockTemplateCompositeValue(t *testing.T) {
	// in 是装配后的扁平 input map:端口路径可直接查（node id 仅正则匹配,
	// 取值走端口路径）。
	in := map[string]any{
		"outputList": []any{
			map[string]any{"content": "go is compiled", "title": "Go", "score": 0.36},
		},
	}
	got := resolveBlockTemplate("ref: {{block_output_100002.outputList}}", in)
	want := `ref: [{"content":"go is compiled","score":0.36,"title":"Go"}]`
	if got != want {
		t.Fatalf("resolveBlockTemplate = %q, want %q", got, want)
	}
}

// 标量路径不受影响（回归:字符串/数字原契约）。
func TestResolveBlockTemplateScalarStillWorks(t *testing.T) {
	in := map[string]any{"output": "hello world"}
	got := resolveBlockTemplate("{{block_output_100002.output}}", in)
	if got != "hello world" {
		t.Fatalf("scalar = %q, want hello world", got)
	}
}

// ── E5-b item-0: 记录数组成员路径的模板引用（第二个 walker）──────────────
// 参考目标 把 doc_results[0].url 渲染成 item-0 点路径(data.doc_results.url),
// resolveBlockTemplate 的 lookupDottedPath 在段位置遇到 record 数组时同样
// 按 item-0 下钻（空数组 → <missing>,与 RenderTemplate 契约一致）。

func TestResolveBlockTemplateRecordArrayMember(t *testing.T) {
	in := map[string]any{
		"data": map[string]any{
			"doc_results": []any{
				map[string]any{"url": "https://example.com/a", "title": "A"},
				map[string]any{"url": "https://example.com/b", "title": "B"},
			},
		},
	}
	got := resolveBlockTemplate("cite: {{block_output_100002.data.doc_results.url}}", in)
	want := "cite: https://example.com/a"
	if got != want {
		t.Fatalf("item-0 member = %q, want %q", got, want)
	}
}

func TestResolveBlockTemplateRecordArrayEmptyMissing(t *testing.T) {
	in := map[string]any{"outputList": []any{}}
	got := resolveBlockTemplate("x={{block_output_100002.outputList.content}}", in)
	if got != "x=<missing>" {
		t.Fatalf("empty-array member = %q, want <missing>", got)
	}
}

// ── Phase 2: multimodal content parts + extract vision ──────────────────

func TestLLMMessageContentParts(t *testing.T) {
	cfg := &spec.LLMConfig{Model: "m", Result: stringLLMResult()}
	impl := &llmNode{cfg: cfg, mf: &captureFactory{}}
	in := map[string]any{"messages": []any{map[string]any{
		"role": "user",
		"content": []any{
			map[string]any{"type": "text", "text": "描述这张图"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://x/y.png"}},
		},
	}}}
	if _, err := impl.Invoke(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	got := impl.mf.(*captureFactory).got
	if len(got) != 1 || len(got[0].Parts) != 2 {
		t.Fatalf("messages=%#v, want one message with two parts", got)
	}
	if got[0].Content != "描述这张图" {
		t.Fatalf("content=%q, want text part projected", got[0].Content)
	}
	if got[0].Parts[1].Type != PartImageURL || got[0].Parts[1].URL != "https://x/y.png" {
		t.Fatalf("image part=%#v", got[0].Parts[1])
	}
}

func TestLLMMessageContentPartsDecodeAudioAndFile(t *testing.T) {
	msg, ok := messageFromMap(map[string]any{
		"role": "user",
		"content": []any{
			map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": "aGk=", "format": "wav"}},
			map[string]any{"type": "file", "file": map[string]any{
				"filename": "a.txt", "file_data": "data:text/plain;base64,aGk=",
			}},
		},
	}, nil)
	if !ok {
		t.Fatal("message not recognized")
	}
	if len(msg.Parts) != 2 {
		t.Fatalf("parts=%#v", msg.Parts)
	}
	if msg.Parts[0].Type != PartInputAudio || string(msg.Parts[0].Data) != "hi" || msg.Parts[0].MIME != "audio/wav" {
		t.Fatalf("audio=%#v", msg.Parts[0])
	}
	if msg.Parts[1].Type != PartFile || msg.Parts[1].Filename != "a.txt" || msg.Parts[1].MIME != "text/plain" || string(msg.Parts[1].Data) != "hi" {
		t.Fatalf("file=%#v", msg.Parts[1])
	}
}

func TestLLMConfigPartsTemplate(t *testing.T) {
	cfg := &spec.LLMConfig{
		Model: "m", Result: stringLLMResult(),
		Config: []spec.ConfigPair{{
			Key: "messages",
			Value: []any{map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "看 {{block_output_100001.query}}"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://x/{{block_output_100001.query}}.png"}},
				},
			}},
		}},
	}
	impl := &llmNode{cfg: cfg, mf: &captureFactory{}}
	in := map[string]any{"query": "cat"}
	if _, err := impl.Invoke(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	got := impl.mf.(*captureFactory).got
	if len(got) != 1 || len(got[0].Parts) != 2 {
		t.Fatalf("messages=%#v", got)
	}
	if got[0].Parts[0].Text != "看 cat" || got[0].Parts[1].URL != "https://x/cat.png" {
		t.Fatalf("parts=%#v", got[0].Parts)
	}
}

func TestLLMVisionAttachesImageToUserTurn(t *testing.T) {
	cfg := &spec.LLMConfig{
		Model: "m", Result: stringLLMResult(),
		Vision: &spec.Source{Path: []string{"vision"}},
		Config: []spec.ConfigPair{{
			Key: "messages",
			Value: []any{
				map[string]any{"role": "system", "content": "extract"},
				map[string]any{"role": "user", "content": "what is this?"},
			},
		}},
	}
	impl := &llmNode{cfg: cfg, mf: &captureFactory{}}
	in := map[string]any{"vision": "https://x/cat.png"}
	if _, err := impl.Invoke(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	got := impl.mf.(*captureFactory).got
	if len(got) != 2 {
		t.Fatalf("messages=%#v", got)
	}
	if len(got[1].Parts) != 1 || got[1].Parts[0].Type != PartImageURL || got[1].Parts[0].URL != "https://x/cat.png" {
		t.Fatalf("vision part=%#v", got[1].Parts)
	}
	if len(got[0].Parts) != 0 {
		t.Fatalf("system message must stay text-only: %#v", got[0])
	}
}

func TestLLMVisionAcceptsPathAndDataURL(t *testing.T) {
	for _, vision := range []any{
		map[string]any{"path": "/tmp/cat.png", "mime_type": "image/png"},
		map[string]any{"data_url": "data:image/png;base64,Y2F0"},
	} {
		cfg := &spec.LLMConfig{
			Model: "m", Result: stringLLMResult(),
			Vision: &spec.Source{Path: []string{"vision"}},
		}
		impl := &llmNode{cfg: cfg, mf: &captureFactory{}}
		if _, err := impl.Invoke(context.Background(), map[string]any{"vision": vision}); err != nil {
			t.Fatal(err)
		}
		got := impl.mf.(*captureFactory).got
		if len(got) != 1 || len(got[0].Parts) != 1 {
			t.Fatalf("vision=%v messages=%#v", vision, got)
		}
		if got[0].Parts[0].URL == "" {
			t.Fatalf("vision=%v part=%#v", vision, got[0].Parts[0])
		}
	}
}

func TestLLMVisionCreatesUserTurnWhenMissing(t *testing.T) {
	cfg := &spec.LLMConfig{
		Model: "m", Result: stringLLMResult(),
		Vision: &spec.Source{Path: []string{"vision"}, Val: map[string]any{"url": "https://x/dog.png", "mime_type": "image/png"}},
	}
	impl := &llmNode{cfg: cfg, mf: &captureFactory{}}
	if _, err := impl.Invoke(context.Background(), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	got := impl.mf.(*captureFactory).got
	if len(got) != 1 || got[0].Role != "user" || len(got[0].Parts) != 1 {
		t.Fatalf("messages=%#v", got)
	}
	if got[0].Parts[0].URL != "https://x/dog.png" || got[0].Parts[0].MIME != "image/png" {
		t.Fatalf("vision part=%#v", got[0].Parts[0])
	}
}
