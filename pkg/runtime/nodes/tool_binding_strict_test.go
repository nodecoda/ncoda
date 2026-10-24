package nodes

// tool_binding_strict_test.go — 库名必须参与解析（RUNTIME-LIBRARY-SPEC §3.2）。
//
// 旧行为：ProviderID 不匹配时按 operation 末段兜底 ⇒ A 库调用会静默落到
// B 库的同名函数上（库名形同虚设）。本文件钉住收紧后的规则：
//   - 库已知 ⇒ 只接受 provider/operation 精确（或绑定别名）；
//   - 无库（旧 spec）⇒ 精确工具名，或**唯一**末段匹配；歧义不猜。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func TestToolDoesNotBindAcrossLibrariesByOperationName(t *testing.T) {
	fake := &fakeToolReg{names: []string{"library_b/image_generator"}, out: "b"}
	n := buildDeps(t, spec.KindTool, &spec.ToolConfig{
		ProviderID: "library_a", Operation: "image_generator",
	}, ioDeps(nil, nil, fake))

	out, err := n.Invoke(t.Context(), map[string]any{})
	if err == nil {
		t.Fatalf("library_a 不得静默绑到 library_b/image_generator；got out=%v", out)
	}
	if !strings.Contains(err.Error(), "RBINDING_MISSING") {
		t.Fatalf("want RBINDING_MISSING, got %v", err)
	}
}

func TestToolBindsExactSymbolAcrossRegistry(t *testing.T) {
	fake := &fakeToolReg{names: []string{"library_a/image_generator"}, out: "a"}
	n := buildDeps(t, spec.KindTool, &spec.ToolConfig{
		ProviderID: "library_a", Operation: "image_generator",
	}, ioDeps(nil, nil, fake))
	out, err := n.Invoke(t.Context(), map[string]any{})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out["output"] != "a" {
		t.Fatalf("output = %v", out["output"])
	}
}

func TestToolAmbiguousBareOperationFailsClosed(t *testing.T) {
	fake := &fakeToolReg{names: []string{"library_a/op", "library_b/op"}, out: "x"}
	n := buildDeps(t, spec.KindTool, &spec.ToolConfig{ToolName: "op"}, ioDeps(nil, nil, fake))
	if _, err := n.Invoke(t.Context(), map[string]any{}); err == nil {
		t.Fatal("ambiguous bare operation must fail closed")
	}
}

func TestPlatformNamedProviderExplainsTheRule(t *testing.T) {
	fake := &fakeToolReg{names: []string{"local_image_generate/image_generator"}, out: "x"}
	n := buildDeps(t, spec.KindTool, &spec.ToolConfig{
		ProviderID: "plugin_7547770968681447458", Operation: "image_generator",
	}, ioDeps(nil, nil, fake))
	_, err := n.Invoke(t.Context(), map[string]any{})
	if err == nil {
		t.Fatal("platform-named provider without binding must fail")
	}
	if !strings.Contains(err.Error(), "platform-named library is a legacy form") {
		t.Fatalf("诊断缺绑定指引: %v", err)
	}
}
