package compile

// code_e2e_test.go — M4 compile 级 e2e:synthetic code shim 节点
// （start→code→end）编译并运行,验证求值器输出端口与 UNWRAP 语义。

import (
	"context"
	"os/exec"
	"reflect"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// M4.1:start→code(wrap-list)→end,shim 输出 dict 的 key 直接作为命名端口。
func TestM41CodeShimE2E(t *testing.T) {
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001", Params: []spec.ParamSpec{{Name: "item", Type: &spec.TypeSpec{Kind: "string"}}}},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("c1", spec.KindCode, &spec.CodeConfig{
				Code:      "def main(item) -> dict:\n    return {\"result\": [item]}",
				Language:  "python",
				Synthetic: true,
				OutOrder:  []string{"result"},
				Inputs:    []spec.Source{{Path: []string{"item"}, BlockID: "100001", Name: "item"}},
			}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "c1", Output: "result"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "c1"},
			{Source: "c1", Target: "e1"},
		},
	}
	r := mustCompile(t, New(testRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"item": "x"}))
	// End.Inputs 装配到规范 "output" 结果键（D3）;值 = shim 的 result 端口。
	if !reflect.DeepEqual(out["output"], []any{"x"}) {
		t.Fatalf("output = %#v, want [x]", out["output"])
	}
}

// M4.2:多输出 shim（record dict）→ 每个 key 都是命名输出端口,UNWRAP 全部可见。
func TestM42CodeMultiOutputE2E(t *testing.T) {
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001", Params: []spec.ParamSpec{{Name: "v", Type: &spec.TypeSpec{Kind: "string"}}}},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("c1", spec.KindCode, &spec.CodeConfig{
				Code:      "def main(v) -> dict:\n    return {\"a\": (v + \":a\"), \"b\": (v + \":b\")}",
				Language:  "python",
				Synthetic: true,
				OutOrder:  []string{"a", "b"},
				Inputs:    []spec.Source{{Path: []string{"v"}, BlockID: "100001", Name: "v"}},
			}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Outputs: []spec.EndOutput{
				{Name: "a", Ref: spec.Ref{Producer: "c1", Output: "a"}},
				{Name: "b", Ref: spec.Ref{Producer: "c1", Output: "b"}},
			}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "c1"},
			{Source: "c1", Target: "e1"},
		},
	}
	r := mustCompile(t, New(testRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"v": "x"}))
	if out["a"] != "x:a" || out["b"] != "x:b" {
		t.Fatalf("multi-output = %#v, want a=x:a b=x:b", out)
	}
}

// M4.3:非法 shim（求值器子集外）→ Compile 期 fail-fast（RExpressionUnsupported）。
func TestM43CodeBadShimCompileRejected(t *testing.T) {
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001", Params: []spec.ParamSpec{{Name: "v", Type: &spec.TypeSpec{Kind: "string"}}}},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("c1", spec.KindCode, &spec.CodeConfig{
				// dict comprehension 仍不在求值器子集（generator 只发射
				// enumerate 列表推导）→ Build 期解析拒绝（fail-fast）。
				Code:      "def main(v) -> dict:\n    return {\"a\": {k: k for k in v}}",
				Language:  "python",
				Synthetic: true,
				OutOrder:  []string{"a"},
				Inputs:    []spec.Source{{Path: []string{"v"}, BlockID: "100001", Name: "v"}},
			}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "c1", Output: "a"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "c1"},
			{Source: "c1", Target: "e1"},
		},
	}
	_, err := New(testRegistry()).Compile(context.Background(), s)
	if err == nil {
		t.Fatal("bad shim must fail at Compile (fail-fast)")
	}
	if got := diag.CodeOf(err); got != diag.RExpressionUnsupported {
		t.Fatalf("err code = %v, want RExpressionUnsupported", got)
	}
}

// M4.4（M17 更新）:用户 python（非 synthetic）编译通过,Invoke 走
// subprocess python3 沙箱执行器（pyrun.go）,输出契约与 shim 一致。
func TestM44CodeUserPythonE2E(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 not available: %v", err)
	}
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001", Params: []spec.ParamSpec{{Name: "v", Type: &spec.TypeSpec{Kind: "string"}}}},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("c1", spec.KindCode, &spec.CodeConfig{
				Code:     "def main(v) -> dict:\n    return {\"a\": v + \":py\"}",
				Language: "python",
				OutOrder: []string{"a"},
				Inputs:   []spec.Source{{Path: []string{"v"}, BlockID: "100001", Name: "v"}},
			}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "c1", Output: "a"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "c1"},
			{Source: "c1", Target: "e1"},
		},
	}
	r := mustCompile(t, New(testRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"v": "x"}))
	if out["output"] != "x:py" {
		t.Fatalf("output = %#v, want x:py", out["output"])
	}
}

// M4.5（M17）:用户 javascript code 仍在 Compile 期 REJECT（语言面收窄
// 到 python,D4 裁剪面收窄到语言本身）。
// M45:用户 javascript code 节点经子进程执行器运行（jrun.go,node 可用时）；
// node 缺失时 Build 期 fail-fast RCodeLanguageUnsupported。
func TestM45CodeUserJavascriptRuns(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not available: %v", err)
	}
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001", Params: []spec.ParamSpec{{Name: "v", Type: &spec.TypeSpec{Kind: "string"}}}},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("c1", spec.KindCode, &spec.CodeConfig{
				Code:     `async function main({ params }: Args): Promise<Output> { return {a: params.v + ":js"}; }`,
				Language: "javascript",
				OutOrder: []string{"a"},
				Inputs:   []spec.Source{{Path: []string{"v"}, BlockID: "100001", Name: "v"}},
			}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "c1", Output: "a"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "c1"},
			{Source: "c1", Target: "e1"},
		},
	}
	r, err := New(testRegistry()).Compile(context.Background(), s)
	if err != nil {
		t.Fatalf("javascript compile: %v", err)
	}
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"v": "x"}))
	if out["output"] != "x:js" {
		t.Fatalf("output = %#v, want x:js", out["output"])
	}
}

// 第三种语言（ruby）仍被拒绝（语言面收窄到 python + javascript）。
func TestM45CodeUserRubyStillRejected(t *testing.T) {
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("c1", spec.KindCode, &spec.CodeConfig{
				Code: `def main(v)
 v
end`,
				Language: "ruby",
			}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "c1"},
			{Source: "c1", Target: "e1"},
		},
	}
	_, err := New(testRegistry()).Compile(context.Background(), s)
	if err == nil {
		t.Fatal("ruby must be rejected at Compile")
	}
	if got := diag.CodeOf(err); got != diag.RCodeLanguageUnsupported {
		t.Fatalf("err code = %v, want RCodeLanguageUnsupported", got)
	}
}
