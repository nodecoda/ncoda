package nodes

// http_test.go — M14 回归:http 节点分支/端口契约。
//
// 对齐 coze fail-branch 语义:
//   - 成功:输出 body/statusCode/output/status 双端口 + $branch="source"
//     （此前成功分支缺 $branch,带分支边的图报 "node did not emit a
//     $branch key";IR fieldPort 按 body/statusCode/headers 引用,此前
//     只输出 output/status 导致 body 引用 <missing>）。
//   - 失败 + FailBranch:输出 $branch="fail-branch" + error_type/
//     error_message（attempt 失败分支的 OperationError 语义端口）。
//   - 失败无 FailBranch:节点错误（RNodeError）。

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func httpNodeSpec(cfg *spec.HTTPConfig) *spec.NodeSpec {
	b, _ := json.Marshal(cfg)
	return &spec.NodeSpec{ID: "h1", Kind: spec.KindHTTP, Config: b}
}

// M14:成功分支输出 $branch="source" + body/statusCode 双端口。
func TestHTTPSuccessEmitsBranchSource(t *testing.T) {
	fake := &fakeHTTPClient{status: 200, body: `{"ok":true}`}
	impl, err := (HTTP{}).Build(context.Background(), httpNodeSpec(&spec.HTTPConfig{
		Method: "GET", URL: "https://x.test/api",
		Exception: &spec.ExceptionConfig{FailBranch: true},
	}), Deps{HTTP: fake})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	out, err := impl.Invoke(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out[MarkerBranch] != "source" {
		t.Errorf("$branch = %v, want source", out[MarkerBranch])
	}
	if out["body"] != `{"ok":true}` {
		t.Errorf("body = %v, want response body", out["body"])
	}
	if out["output"] != `{"ok":true}` {
		t.Errorf("output = %v (legacy alias), want body", out["output"])
	}
	// status_code 是语言字段名；平台口名（如 statusCode）是在适配器声明契约后
	// 由 httpResultPayload 一并发出的别名，内核构建不拼写它。
	if out["status_code"] != 200 || out["status"] != 200 {
		t.Errorf("status_code/status = %v/%v, want 200/200", out["status_code"], out["status"])
	}
	if _, ok := out["headers"].(map[string]string); !ok {
		t.Errorf("headers = %#v, want map[string]string", out["headers"])
	}
}

// M14:失败 + FailBranch 输出 fail-branch 标记 + error_type/error_message。
func TestHTTPFailBranchEmitsErrorPorts(t *testing.T) {
	fake := &fakeHTTPClient{status: 503, body: "upstream down"}
	impl, err := (HTTP{}).Build(context.Background(), httpNodeSpec(&spec.HTTPConfig{
		Method: "GET", URL: "https://x.test/api",
		Exception: &spec.ExceptionConfig{FailBranch: true},
	}), Deps{HTTP: fake})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	out, err := impl.Invoke(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out[MarkerBranch] != "fail-branch" {
		t.Errorf("$branch = %v, want fail-branch", out[MarkerBranch])
	}
	if out["error_type"] != "http_error" {
		t.Errorf("error_type = %v, want http_error", out["error_type"])
	}
	if out["error_message"] == nil || out["error_message"] == "" {
		t.Errorf("error_message = %v, want non-empty", out["error_message"])
	}
}

// M14:网络错误 + FailBranch → error_type=network_error。
func TestHTTPNetworkErrorBranch(t *testing.T) {
	fake := &fakeHTTPClient{err: errHTTPBoom}
	impl, err := (HTTP{}).Build(context.Background(), httpNodeSpec(&spec.HTTPConfig{
		Method: "GET", URL: "https://x.test/api",
		Exception: &spec.ExceptionConfig{FailBranch: true},
	}), Deps{HTTP: fake})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	out, err := impl.Invoke(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out[MarkerBranch] != "fail-branch" || out["error_type"] != "network_error" {
		t.Errorf("branch/type = %v/%v, want fail-branch/network_error", out[MarkerBranch], out["error_type"])
	}
}

// 失败但无 FailBranch → RNodeError（保持既有错误语义）。
func TestHTTPNoFailBranchErrors(t *testing.T) {
	fake := &fakeHTTPClient{status: 500, body: "boom"}
	impl, err := (HTTP{}).Build(context.Background(), httpNodeSpec(&spec.HTTPConfig{
		Method: "GET", URL: "https://x.test/api",
	}), Deps{HTTP: fake})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	_, err = impl.Invoke(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error without FailBranch on non-2xx")
	}
	if diag.CodeOf(err) != diag.RNodeError {
		t.Fatalf("code = %v, want RNodeError", diag.CodeOf(err))
	}
}

var errHTTPBoom = errors.New("http network boom")

// Tier-1 review #4 回归:未注入 HTTP client 时,默认 client 必须带 120s
// 兜底超时（上游挂死不无限阻塞 run）。断言 defaultHTTPClient 的内部
// net/http client 超时值;未来若改用不同实现,此测试锁定"默认必带超时"
// 这一契约。
func TestHTTPDefaultClientTimeout(t *testing.T) {
	impl, err := (HTTP{}).Build(context.Background(), httpNodeSpec(&spec.HTTPConfig{
		Method: "GET", URL: "https://x.test/api",
	}), Deps{}) // Deps.HTTP nil → 默认 client
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	hn, ok := impl.(*httpNode)
	if !ok {
		t.Fatalf("impl type = %T, want *httpNode", impl)
	}
	dhc, ok := hn.hc.(*defaultHTTPClient)
	if !ok {
		t.Fatalf("hc type = %T, want *defaultHTTPClient", hn.hc)
	}
	if got := dhc.client.Timeout; got != 120*time.Second {
		t.Errorf("default client Timeout = %v, want 120s", got)
	}
}
