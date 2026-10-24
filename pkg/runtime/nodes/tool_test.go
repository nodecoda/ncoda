package nodes

// tool_test.go —— tool 节点的分支端口契约，与 `http_test.go` 的 M14 同一条。
//
// 规则一句话（可判真假）：**带分支边的节点，成功也要选边** —— attempt 的成功臂
// 读 `$branch == "source"`。只发 fail-branch 的节点在成功路径上直接炸
// （runtime 建图段报 `node did not emit a $branch key`）。
//
// 一手现场（2026-09-28，672 源判决面的 `branch invoke run error` 一族 10 源）：
// tool 节点此前**只在 FailBranch 时**发 `$branch="fail-branch"`，
// 于是 `attempt { <工具调用> } success { … }` 的成功臂在运行期拿不到选边键
// ⇒ `RSPEC_INVALID`。http 节点早已按同一条口径发成功键（`http.go` 的 M14 修复，
// 本文件是它欠下的另一半）—— 两个宿主同一条语义，不得只有一边发键。
//
// 撤实现必红（2026-09-28 实测）：删掉 `toolOutput` 末尾的
// `out[MarkerBranch] = "source"` ⇒ `TestToolSuccessEmitsBranchSource` 红。

import (
	"errors"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// 成功：必须发 `$branch="source"`，且结果对象的顶层字段按端口展开。
func TestToolSuccessEmitsBranchSource(t *testing.T) {
	fake := &fakeToolReg{names: []string{"biz/http"}, out: map[string]any{"url": "https://x"}}
	n := buildDeps(t, spec.KindTool, &spec.ToolConfig{
		ProviderID: "biz", ToolName: "http", Operation: "GET",
		Params:    []spec.ToolParam{{Key: "url", Value: "https://x"}},
		Exception: &spec.ExceptionConfig{FailBranch: true},
	}, ioDeps(nil, nil, fake))
	out, err := n.Invoke(t.Context(), map[string]any{})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out[MarkerBranch] != "source" {
		t.Errorf("$branch = %v, want source（成功臂拿不到选边键 ⇒ runtime 报 node did not emit a $branch key）",
			out[MarkerBranch])
	}
	if out["url"] != "https://x" {
		t.Errorf("url = %v, want 结果对象顶层字段按端口展开", out["url"])
	}
}

// 失败 + FailBranch：发 fail-branch（既有行为，本文件只钉住它不被上面那条改动带偏）。
func TestToolFailBranchStillEmitsFailBranch(t *testing.T) {
	fake := &fakeToolReg{names: []string{"biz/http"}, err: errors.New("boom")}
	n := buildDeps(t, spec.KindTool, &spec.ToolConfig{
		ProviderID: "biz", ToolName: "http", Operation: "GET",
		Exception: &spec.ExceptionConfig{FailBranch: true},
	}, ioDeps(nil, nil, fake))
	out, err := n.Invoke(t.Context(), map[string]any{})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out[MarkerBranch] != "fail-branch" {
		t.Errorf("$branch = %v, want fail-branch", out[MarkerBranch])
	}
}

// 失败且**没有** FailBranch：节点错误（RNodeError）—— 不得因为「成功要发键」
// 就把失败也吞成选边。
func TestToolErrorWithoutFailBranchFails(t *testing.T) {
	fake := &fakeToolReg{names: []string{"biz/http"}, err: errors.New("boom")}
	n := buildDeps(t, spec.KindTool, &spec.ToolConfig{
		ProviderID: "biz", ToolName: "http", Operation: "GET",
	}, ioDeps(nil, nil, fake))
	if _, err := n.Invoke(t.Context(), map[string]any{}); err == nil {
		t.Fatal("expected RNodeError")
	}
}
