package nodes

// io_test.go — I/O 节点单测（M2.2, U2.2/U2.3/U2.4/U2.7）。
//
// T3 零外部依赖:HTTP 用注入 fake、LLM 用 fake ModelFactory、tool 用 stub
// 注册表、qa 用 compose interrupt/resume roundtrip。确定性断言。

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// ── fakes ────────────────────────────────────────────────────────────────

type fakeHTTPClient struct {
	status int
	body   string
	err    error
	gotURL string
}

func (f *fakeHTTPClient) Do(ctx context.Context, method, url string, headers map[string]string, body any) (int, []byte, error) {
	f.gotURL = url
	if f.err != nil {
		return 0, nil, f.err
	}
	return f.status, []byte(f.body), nil
}

type fakeModel struct {
	out   string
	err   error
	got   []Message
	calls int
}

func (f *fakeModel) Chat(ctx context.Context, model string, messages []Message) (string, error) {
	f.calls++
	f.got = messages
	if f.err != nil {
		return "", f.err
	}
	return f.out, nil
}

type fakeToolReg struct {
	names []string
	out   any
	err   error
	got   map[string]any
}

func (f *fakeToolReg) Names() []string { return f.names }
func (f *fakeToolReg) Call(ctx context.Context, name string, args map[string]any) (any, error) {
	f.got = args
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}

func ioDeps(http HTTPClient, mf ModelFactory, tr ToolRegistry) Deps {
	return Deps{HTTP: http, Models: mf, Tools: tr}
}

// ── U2.3 http ────────────────────────────────────────────────────────────

func TestHTTPOK(t *testing.T) {
	fake := &fakeHTTPClient{status: 200, body: `{"ok":true}`}
	n := buildDeps(t, spec.KindHTTP, &spec.HTTPConfig{Method: "GET", URL: "https://x/{{id}}"}, ioDeps(fake, nil, nil))
	out, err := n.Invoke(t.Context(), map[string]any{"id": "42"})
	if err != nil {
		t.Fatal(err)
	}
	if out["status"] != 200 || out["output"] != `{"ok":true}` {
		t.Fatalf("http out = %v", out)
	}
	if fake.gotURL != "https://x/42" {
		t.Fatalf("url = %q", fake.gotURL)
	}
}

func TestHTTPFailBranch(t *testing.T) {
	fake := &fakeHTTPClient{status: 500, body: "boom"}
	n := buildDeps(t, spec.KindHTTP, &spec.HTTPConfig{
		Method: "GET", URL: "https://x",
		Exception: &spec.ExceptionConfig{FailBranch: true},
	}, ioDeps(fake, nil, nil))
	out, err := n.Invoke(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out[MarkerBranch] != "fail-branch" {
		t.Fatalf("fail-branch out = %v", out)
	}
}

func TestHTTPRetry(t *testing.T) {
	fake := &fakeHTTPClient{err: errors.New("flaky")}
	n := buildDeps(t, spec.KindHTTP, &spec.HTTPConfig{
		Method: "GET", URL: "https://x",
		Exception: &spec.ExceptionConfig{MaxRetry: 2},
	}, ioDeps(fake, nil, nil))
	if _, err := n.Invoke(t.Context(), nil); err == nil {
		t.Fatal("expected error after retries")
	}
	// fake can't count calls — verify via a counting wrapper instead
}

// ── U2.2 llm ─────────────────────────────────────────────────────────────

func TestLLMMessages(t *testing.T) {
	fake := &fakeModel{out: "hi"}
	n := buildDeps(t, spec.KindLLM, &spec.LLMConfig{Model: "m", Result: stringLLMResult()}, ioDeps(nil, fake, nil))
	out, err := n.Invoke(t.Context(), map[string]any{"input": []any{map[string]any{"role": "user", "content": "q"}}})
	if err != nil {
		t.Fatal(err)
	}
	if out["output"] != "hi" {
		t.Fatalf("llm out = %v", out)
	}
	if len(fake.got) != 1 || fake.got[0].Content != "q" {
		t.Fatalf("messages = %v", fake.got)
	}
}

func TestLLMNoFactory(t *testing.T) {
	// deps 缺失在 Build 期拒绝（fail-fast,比运行期错误更早暴露）
	b, _ := json.Marshal(&spec.LLMConfig{Model: "m"})
	_, err := StdRegistry().Build(context.Background(), &spec.NodeSpec{ID: "n", Kind: spec.KindLLM, Config: b}, Deps{})
	if err == nil {
		t.Fatal("expected dependency-missing error at build")
	}
}

// ── U2.4 tool ────────────────────────────────────────────────────────────

func TestToolCall(t *testing.T) {
	fake := &fakeToolReg{names: []string{"biz/http"}, out: "ok"}
	n := buildDeps(t, spec.KindTool, &spec.ToolConfig{
		ProviderID: "biz", ToolName: "http", Operation: "GET",
		Params: []spec.ToolParam{{Key: "url", Value: "https://x"}},
	}, ioDeps(nil, nil, fake))
	out, err := n.Invoke(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["output"] != "ok" {
		t.Fatalf("tool out = %v", out)
	}
	if fake.got["url"] != "https://x" {
		t.Fatalf("args = %v", fake.got)
	}
}

func TestToolMissingBinding(t *testing.T) {
	fake := &fakeToolReg{names: []string{"other"}}
	n := buildDeps(t, spec.KindTool, &spec.ToolConfig{ToolName: "nope"}, ioDeps(nil, nil, fake))
	if _, err := n.Invoke(t.Context(), nil); err == nil {
		t.Fatal("expected RBindingMissing")
	}
}

// ── helpers ──────────────────────────────────────────────────────────────

func buildDeps(t *testing.T, kind string, cfg any, deps Deps) NodeImpl {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	impl, err := StdRegistry().Build(context.Background(), &spec.NodeSpec{ID: "n", Kind: kind, Config: b}, deps)
	if err != nil {
		t.Fatalf("build %s: %v", kind, err)
	}
	return impl
}

// countingHTTP wraps a fake and counts Do calls (retry assertion).
type countingHTTP struct {
	inner *fakeHTTPClient
	calls int
}

func (c *countingHTTP) Do(ctx context.Context, method, url string, headers map[string]string, body any) (int, []byte, error) {
	c.calls++
	return c.inner.Do(ctx, method, url, headers, body)
}

func TestHTTPRetryCount(t *testing.T) {
	fake := &fakeHTTPClient{err: errors.New("flaky")}
	counting := &countingHTTP{inner: fake}
	n := buildDeps(t, spec.KindHTTP, &spec.HTTPConfig{
		Method: "GET", URL: "https://x",
		Exception: &spec.ExceptionConfig{MaxRetry: 2},
	}, ioDeps(counting, nil, nil))
	if _, err := n.Invoke(t.Context(), nil); err == nil {
		t.Fatal("expected error after retries")
	}
	if counting.calls != 3 { // 1 initial + 2 retries
		t.Fatalf("Do calls = %d, want 3", counting.calls)
	}
}

func TestHTTPNoDepsDefaultsToNetHTTP(t *testing.T) {
	// deps 无 HTTP → 回退默认 net/http client（真实请求失败即节点错误,
	// 不会 panic;本地无网络也是确定性错误）
	n := buildDeps(t, spec.KindHTTP, &spec.HTTPConfig{Method: "GET", URL: "http://127.0.0.1:1/x"}, Deps{})
	_, err := n.Invoke(t.Context(), nil)
	if err == nil {
		t.Fatal("expected connection error from default client")
	}
}

// ── U2.4b tool 输出端口展开（IR fieldPort 语义）─────────────────────────

func TestToolOutputFieldPorts(t *testing.T) {
	fake := &fakeToolReg{names: []string{"biz/http"}, out: map[string]any{"result": "top5", "meta": "m"}}
	n := buildDeps(t, spec.KindTool, &spec.ToolConfig{
		ProviderID: "biz", ToolName: "http", Operation: "GET",
	}, ioDeps(nil, nil, fake))
	out, err := n.Invoke(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// 字段展开端口 + output 整体端口
	if out["result"] != "top5" {
		t.Fatalf("field port result = %v", out["result"])
	}
	if out["meta"] != "m" {
		t.Fatalf("field port meta = %v", out["meta"])
	}
	whole, ok := out["output"].(map[string]any)
	if !ok || whole["result"] != "top5" {
		t.Fatalf("output port = %#v", out["output"])
	}
}

func TestToolOutputScalarOnlyOutputPort(t *testing.T) {
	fake := &fakeToolReg{names: []string{"biz/http"}, out: "plain"}
	n := buildDeps(t, spec.KindTool, &spec.ToolConfig{
		ProviderID: "biz", ToolName: "http",
	}, ioDeps(nil, nil, fake))
	out, err := n.Invoke(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["output"] != "plain" {
		t.Fatalf("output = %v", out["output"])
	}
	if _, hasField := out["result"]; hasField {
		t.Fatal("scalar result must not expand field ports")
	}
}
