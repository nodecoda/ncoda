package compile

// plugin_e2e_test.go — tool 节点 ↔ plugin.Manager 端到端链路。
//
// 链路: spec.v1 tool 节点 → nodes.Tool.Invoke → plugin.Manager.Call
// （nodes.ToolRegistry 接口）→ 分派（callable / http）→ 结果经输出端口
// 汇入 end 节点。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func toolSpec(toolName, provider string, params []spec.ToolParam) *spec.Spec {
	return &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001", Params: []spec.ParamSpec{{Name: "x", Type: &spec.TypeSpec{Kind: "string"}}}},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("tk", spec.KindTool, &spec.ToolConfig{
				ToolName:   toolName,
				ProviderID: provider,
				Params:     params,
			}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "tk", Output: "output"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "tk"},
			{Source: "tk", Target: "e1"},
		},
	}
}

func compileWithPlugins(t *testing.T, s *spec.Spec, pm *plugin.Manager) Runnable {
	t.Helper()
	deps := nodes.Deps{Tools: pm}
	c := New(nodes.StdRegistry(), WithDeps(deps))
	r, err := c.Compile(context.Background(), s)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return r
}

func TestPluginE2ECallableToolNode(t *testing.T) {
	pm := plugin.New()
	greet := &plugin.Plugin{
		ID: "builtin", Type: plugin.PluginCallable,
		Tools: []plugin.Tool{{
			Name: "greet",
			Call: func(ctx context.Context, args map[string]any) (any, error) {
				return fmt.Sprintf("hello %v", args["x"]), nil
			},
		}},
	}
	if err := pm.Register(greet); err != nil {
		t.Fatal(err)
	}

	s := toolSpec("greet", "builtin", []spec.ToolParam{
		{Key: "x", Ref: &spec.Source{Path: []string{"x"}, BlockID: "100001", Name: "x"}},
	})
	r := compileWithPlugins(t, s, pm)
	res := UnwrapOutput(mustInvoke(t, r, map[string]any{"x": "world"}))
	if got := mustJSON(t, res["output"]); got != `"hello world"` {
		t.Fatalf("output = %s", got)
	}
}

func TestPluginE2EHTTPToolNode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"sum":7}`)
	}))
	defer srv.Close()

	pm := plugin.New()
	calc := &plugin.Plugin{
		ID: "math", Type: plugin.PluginHTTP, ServerURL: srv.URL,
		Tools: []plugin.Tool{{
			Name: "add", Method: "GET", Path: "/add",
			Params: []plugin.ParamSchema{{Name: "a", Type: "int", In: plugin.InQuery}},
		}},
	}
	if err := pm.Register(calc); err != nil {
		t.Fatal(err)
	}

	s := toolSpec("add", "math", []spec.ToolParam{
		{Key: "a", Value: 3},
	})
	r := compileWithPlugins(t, s, pm)
	res := UnwrapOutput(mustInvoke(t, r, map[string]any{}))
	if got := mustJSON(t, res["output"]); !strings.Contains(got, `"sum":7`) {
		t.Fatalf("output = %s", got)
	}
}

func TestPluginE2EToolBindingMissing(t *testing.T) {
	pm := plugin.New() // 空注册表
	s := toolSpec("nope", "ghost", nil)
	r := compileWithPlugins(t, s, pm) // Build 期允许（注册检查在调用期）
	_, err := r.Invoke(context.Background(), map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "no binding") {
		t.Fatalf("want binding missing error, got %v", err)
	}
}

func TestPluginE2EToolRetryThenFailBranch(t *testing.T) {
	pm := plugin.New()
	attempts := 0
	flakey := &plugin.Plugin{
		ID: "flaky", Type: plugin.PluginCallable,
		Tools: []plugin.Tool{{
			Name: "boom",
			Call: func(ctx context.Context, args map[string]any) (any, error) {
				attempts++
				if attempts < 3 {
					return nil, fmt.Errorf("transient %d", attempts)
				}
				return "recovered", nil
			},
		}},
	}
	if err := pm.Register(flakey); err != nil {
		t.Fatal(err)
	}
	s := toolSpec("boom", "flaky", nil)
	b, err := json.Marshal(&spec.ToolConfig{
		ToolName: "boom", ProviderID: "flaky",
		Exception: &spec.ExceptionConfig{MaxRetry: 2, FailBranch: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Nodes[1].Config = b
	r := compileWithPlugins(t, s, pm)
	res := UnwrapOutput(mustInvoke(t, r, map[string]any{}))
	if attempts != 3 {
		t.Fatalf("attempts = %d (want 3)", attempts)
	}
	if got := mustJSON(t, res["output"]); got != `"recovered"` {
		t.Fatalf("output = %s", got)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
