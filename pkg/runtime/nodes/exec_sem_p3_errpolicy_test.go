package nodes

// exec_sem_p3_errpolicy_test.go — P3 gate evidence for ES-046
// (`settingOnError.processType`).
//
// The platform numbers three outcomes: throw (PT1, abort), return (PT2,
// publish the carried fallback payload) and error branch (PT3, move control to
// the failure branch). The contract is that they are **pairwise
// distinguishable** on the production path: merging PT2 into PT1 would abort a
// flow that declares `with default(...)`, merging PT3 into PT2 would drop the
// control transfer. The falsification is positive: delete any branch in
// errpolicy.go and one of these assertions goes red.
//
// Every case runs the real node implementations through StdRegistry().Build,
// not the helper directly.

import (
	"context"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func p3Int(v int) *int { return &v }

// p3FailingCodeConfig is a code node that always fails at evaluation time
// (undefined shim name), so retry/error-policy tests never depend on the
// sandbox or on wall-clock time.
func p3FailingCodeConfig(exc *spec.ExceptionConfig) *spec.CodeConfig {
	return &spec.CodeConfig{
		Code:      "def main(x) -> dict:\n    return {\"result\": missing_name}",
		Language:  "python",
		Synthetic: true,
		OutOrder:  []string{"result"},
		Exception: exc,
	}
}

// TestExecSemP3ES046CodePolicyThreeBranches: PT1 / PT2 / PT3 must produce
// three different outcomes for the same failing code node.
func TestExecSemP3ES046CodePolicyThreeBranches(t *testing.T) {
	invoke := func(t *testing.T, exc *spec.ExceptionConfig) (map[string]any, error) {
		t.Helper()
		n := buildP1a2Code(t, "code_046", p3FailingCodeConfig(exc), nil)
		return n.Invoke(context.Background(), map[string]any{"x": "v"})
	}

	t.Run("PT1-throw-aborts", func(t *testing.T) {
		out, err := invoke(t, &spec.ExceptionConfig{ProcessType: p3Int(1)})
		if err == nil {
			t.Fatalf("PT1 must abort, got payload %#v", out)
		}
		if out != nil {
			t.Fatalf("PT1 must not publish a payload, got %#v", out)
		}
	})

	t.Run("PT2-return-publishes-carried-payload", func(t *testing.T) {
		out, err := invoke(t, &spec.ExceptionConfig{
			ProcessType: p3Int(2),
			DataOnErr:   []spec.DefaultField{{Name: "result", Value: "fallback"}},
		})
		if err != nil {
			t.Fatalf("PT2 must absorb the failure, got %v", err)
		}
		if out["result"] != "fallback" {
			t.Fatalf("result = %v, want carried fallback", out["result"])
		}
		// code's PT2 payload exposes the failure to downstream code.
		if out["isSuccess"] != false {
			t.Fatalf("isSuccess = %v, want false", out["isSuccess"])
		}
		body, _ := out["errorBody"].(map[string]any)
		if body == nil || body["errorMessage"] == "" || body["errorMessage"] == nil {
			t.Fatalf("errorBody = %#v, want a non-empty errorMessage", out["errorBody"])
		}
		if out[MarkerBranch] != nil {
			t.Fatalf("PT2 must not transfer control, got %v=%v", MarkerBranch, out[MarkerBranch])
		}
	})

	t.Run("PT3-error-branch-transfers-control", func(t *testing.T) {
		out, err := invoke(t, &spec.ExceptionConfig{ProcessType: p3Int(3)})
		if err != nil {
			t.Fatalf("PT3 must absorb the failure, got %v", err)
		}
		if out[MarkerBranch] != "fail-branch" {
			t.Fatalf("%s = %v, want fail-branch", MarkerBranch, out[MarkerBranch])
		}
		if out["error_type"] != "code_error" {
			t.Fatalf("error_type = %v, want code_error", out["error_type"])
		}
		if out["error_message"] == nil || out["error_message"] == "" {
			t.Fatalf("error_message empty: %#v", out)
		}
	})

	t.Run("PT2-without-payload-fails-loud", func(t *testing.T) {
		_, err := invoke(t, &spec.ExceptionConfig{ProcessType: p3Int(2)})
		if err == nil || !strings.Contains(err.Error(), "refusing to fabricate") {
			t.Fatalf("PT2 without data_on_err must fail loud, got %v", err)
		}
	})

	t.Run("unknown-process-type-fails-loud", func(t *testing.T) {
		_, err := invoke(t, &spec.ExceptionConfig{ProcessType: p3Int(9)})
		if err == nil || !strings.Contains(err.Error(), "unknown failure processType 9") {
			t.Fatalf("processType=9 must fail loud, got %v", err)
		}
	})
}

// TestExecSemP3ES046FailBranchFlagOverridesProcessType: lowering an attempt
// sets FailBranch, and that must win over a stale/inherited processType byte
// (the platform's switch axis is derived from PT3, not read back).
func TestExecSemP3ES046FailBranchFlagOverridesProcessType(t *testing.T) {
	n := buildP1a2Code(t, "code_flag", p3FailingCodeConfig(&spec.ExceptionConfig{
		ProcessType: p3Int(1), FailBranch: true,
	}), nil)
	out, err := n.Invoke(context.Background(), map[string]any{"x": "v"})
	if err != nil {
		t.Fatalf("fail-branch policy must absorb the failure, got %v", err)
	}
	if out[MarkerBranch] != "fail-branch" {
		t.Fatalf("%s = %v, want fail-branch", MarkerBranch, out[MarkerBranch])
	}
}

// TestExecSemP3ES046LLMToolPolicy: llm and tool previously dropped the policy
// entirely (tool only handled FailBranch); both now share the same home.
func TestExecSemP3ES046LLMToolPolicy(t *testing.T) {
	t.Run("llm-PT2-return", func(t *testing.T) {
		m := &p3Model{failUntil: 1}
		cfg := &spec.LLMConfig{
			Model: "m", Result: stringLLMResult(),
			Exception: &spec.ExceptionConfig{
				ProcessType: p3Int(2),
				DataOnErr:   []spec.DefaultField{{Name: "output", Value: "llm_fallback"}},
			},
		}
		n := p3Build(t, "llm_046", spec.KindLLM, cfg, Deps{Models: m})
		out, err := n.Invoke(context.Background(), map[string]any{"input": "q"})
		if err != nil {
			t.Fatalf("PT2 must absorb the failure, got %v", err)
		}
		if out["output"] != "llm_fallback" {
			t.Fatalf("output = %v, want llm_fallback", out["output"])
		}
		if m.calls != 1 {
			t.Fatalf("calls = %d, want 1 (no retry declared)", m.calls)
		}
	})

	t.Run("llm-PT1-throw", func(t *testing.T) {
		m := &p3Model{failUntil: 1}
		cfg := &spec.LLMConfig{
			Model: "m", Result: stringLLMResult(),
			Exception: &spec.ExceptionConfig{ProcessType: p3Int(1)},
		}
		n := p3Build(t, "llm_046_throw", spec.KindLLM, cfg, Deps{Models: m})
		if _, err := n.Invoke(context.Background(), map[string]any{"input": "q"}); err == nil {
			t.Fatal("PT1 must abort")
		}
	})

	t.Run("tool-PT2-return", func(t *testing.T) {
		reg := &p3ToolReg{names: []string{"p/t"}, failUntil: 1}
		cfg := &spec.ToolConfig{
			ToolName: "t", ProviderID: "p",
			Exception: &spec.ExceptionConfig{
				ProcessType: p3Int(2),
				DataOnErr:   []spec.DefaultField{{Name: "output", Value: "tool_fallback"}},
			},
		}
		n := p3Build(t, "tool_046", spec.KindTool, cfg, Deps{Tools: reg})
		out, err := n.Invoke(context.Background(), nil)
		if err != nil {
			t.Fatalf("PT2 must absorb the failure, got %v", err)
		}
		if out["output"] != "tool_fallback" {
			t.Fatalf("output = %v, want tool_fallback", out["output"])
		}
	})

	t.Run("tool-PT3-error-branch", func(t *testing.T) {
		reg := &p3ToolReg{names: []string{"p/t"}, failUntil: 1}
		cfg := &spec.ToolConfig{
			ToolName: "t", ProviderID: "p",
			Exception: &spec.ExceptionConfig{ProcessType: p3Int(3)},
		}
		n := p3Build(t, "tool_046_branch", spec.KindTool, cfg, Deps{Tools: reg})
		out, err := n.Invoke(context.Background(), nil)
		if err != nil {
			t.Fatalf("PT3 must absorb the failure, got %v", err)
		}
		if out[MarkerBranch] != "fail-branch" || out["error_type"] != "tool_error" {
			t.Fatalf("branch/type = %v/%v, want fail-branch/tool_error", out[MarkerBranch], out["error_type"])
		}
	})
}

// p3StatusClient returns a fixed status without a transport error, which is
// how the http node distinguishes http_error from network_error.
type p3StatusClient struct {
	status int
	calls  int
}

func (c *p3StatusClient) Do(context.Context, string, string, map[string]string, any) (int, []byte, error) {
	c.calls++
	return c.status, []byte("nope"), nil
}

// TestExecSemP3ES046HTTPPolicy keeps http's two platform-observed error_type
// spellings while routing the policy through the shared home.
func TestExecSemP3ES046HTTPPolicy(t *testing.T) {
	t.Run("network-error", func(t *testing.T) {
		cfg := &spec.HTTPConfig{
			Method: "GET", URL: "https://example.com",
			Exception: &spec.ExceptionConfig{ProcessType: p3Int(3)},
		}
		n := p3Build(t, "http_046_net", spec.KindHTTP, cfg, Deps{HTTP: &p3HTTPClient{fail: true}})
		out, err := n.Invoke(context.Background(), nil)
		if err != nil {
			t.Fatalf("PT3 must absorb the failure, got %v", err)
		}
		if out[MarkerBranch] != "fail-branch" || out["error_type"] != "network_error" {
			t.Fatalf("branch/type = %v/%v, want fail-branch/network_error", out[MarkerBranch], out["error_type"])
		}
	})

	t.Run("http-error-keeps-status", func(t *testing.T) {
		client := &p3StatusClient{status: 500}
		cfg := &spec.HTTPConfig{
			Method: "GET", URL: "https://example.com",
			Exception: &spec.ExceptionConfig{ProcessType: p3Int(3)},
		}
		n := p3Build(t, "http_046_500", spec.KindHTTP, cfg, Deps{HTTP: client})
		out, err := n.Invoke(context.Background(), nil)
		if err != nil {
			t.Fatalf("PT3 must absorb the failure, got %v", err)
		}
		if out["error_type"] != "http_error" || out["status"] != 500 {
			t.Fatalf("type/status = %v/%v, want http_error/500", out["error_type"], out["status"])
		}
		if client.calls != 1 {
			t.Fatalf("calls = %d, want 1 (no retry on a successful HTTP call)", client.calls)
		}
	})

	t.Run("PT2-return", func(t *testing.T) {
		cfg := &spec.HTTPConfig{
			Method: "GET", URL: "https://example.com",
			Exception: &spec.ExceptionConfig{
				ProcessType: p3Int(2),
				DataOnErr:   []spec.DefaultField{{Name: "output", Value: "http_fallback"}},
			},
		}
		n := p3Build(t, "http_046_ret", spec.KindHTTP, cfg, Deps{HTTP: &p3HTTPClient{fail: true}})
		out, err := n.Invoke(context.Background(), nil)
		if err != nil {
			t.Fatalf("PT2 must absorb the failure, got %v", err)
		}
		if out["output"] != "http_fallback" {
			t.Fatalf("output = %v, want http_fallback", out["output"])
		}
	})
}
