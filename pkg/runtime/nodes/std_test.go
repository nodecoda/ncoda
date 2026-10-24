package nodes

// std_test.go — 正式节点实现的单测（M2.1，U2.x 节点级黄金）。
//
// 直接构造 NodeSpec → Build → Invoke(已装配输入),断言输出端口值。
// 与 compile 层 e2e(compile_test.go)互补:这里锁节点自身的确定性语义。

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func ctx() context.Context { return context.Background() }

// build builds one node from a config DTO via the standard registry.
func build(t *testing.T, kind string, cfg any) NodeImpl {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	impl, err := StdRegistry().Build(ctx(), &spec.NodeSpec{ID: "n", Kind: kind, Config: b}, Deps{})
	if err != nil {
		t.Fatalf("build %s: %v", kind, err)
	}
	return impl
}

func TestStartDefaults(t *testing.T) {
	n := build(t, spec.KindStart, &spec.StartConfig{Params: []spec.ParamSpec{
		{Name: "a", Default: "dflt"},
		{Name: "b"},
	}})
	out, err := n.Invoke(ctx(), map[string]any{"a": "given"})
	if err != nil {
		t.Fatal(err)
	}
	if out["a"] != "given" || out["b"] != nil {
		t.Fatalf("start out = %v, want a=given b=nil", out)
	}
}

func TestEndAggregate(t *testing.T) {
	n := build(t, spec.KindEnd, &spec.EndConfig{
		Inputs:  []spec.Ref{{Producer: "x", Output: "output"}},
		Outputs: []spec.EndOutput{{Name: "greet", Ref: spec.Ref{Producer: "x", Output: "greet"}}},
	})
	out, err := n.Invoke(ctx(), map[string]any{"output": "main", "greet": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if out["output"] != "main" || out["greet"] != "hi" {
		t.Fatalf("end out = %v", out)
	}
}

func TestTextConcat(t *testing.T) {
	n := build(t, spec.KindText, &spec.TextConfig{Method: "concat", Template: "hi {{name}}"})
	out, err := n.Invoke(ctx(), map[string]any{"name": "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if out["output"] != "hi dev" {
		t.Fatalf("concat out = %v", out)
	}
}

func TestTextConstJSONArray(t *testing.T) {
	n := build(t, spec.KindText, &spec.TextConfig{Method: "const", Template: `["a","b"]`})
	out, err := n.Invoke(ctx(), nil)
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := out["output"].([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("const-array out = %#v, want []any{...}", out["output"])
	}
}

func TestTextSplit(t *testing.T) {
	n := build(t, spec.KindText, &spec.TextConfig{Method: "split", ConcatChar: ","})
	out, err := n.Invoke(ctx(), map[string]any{"content": "a,b,c"})
	if err != nil {
		t.Fatal(err)
	}
	if got := out["output"].([]string); len(got) != 3 || got[0] != "a" {
		t.Fatalf("split out = %v", out["output"])
	}
}

func TestAggregateFirstNonZero(t *testing.T) {
	n := build(t, spec.KindAggregate, &spec.AggregateConfig{Refs: []spec.Ref{{Producer: "a"}, {Producer: "b"}}})
	// D3/P4: first_non_zero, not first_not_null. An executed arm that produced
	// a zero falls through to the next candidate; all-zero stays zero.
	out, err := n.Invoke(ctx(), map[string]any{"r0": nil, "r1": ""})
	if err != nil {
		t.Fatal(err)
	}
	if out[wGroup1] != "" {
		t.Fatalf("Group1 = %v, want empty string (all candidates zero => zero)", out[wGroup1])
	}
	out, _ = n.Invoke(ctx(), map[string]any{"r0": "", "r1": "x"})
	if out[wGroup1] != "x" {
		t.Fatalf("Group1 = %v, want x (empty string is zero and is skipped)", out[wGroup1])
	}
	out, _ = n.Invoke(ctx(), map[string]any{"r0": "", "r1": ""})
	if out[wGroup1] != "" {
		t.Fatalf("Group1 = %v, want empty string (all zero => zero)", out[wGroup1])
	}
	out, _ = n.Invoke(ctx(), map[string]any{"r0": nil, "r1": nil})
	if out[wGroup1] != nil {
		t.Fatalf("Group1 = %v, want nil", out[wGroup1])
	}
}

func TestAggregateNamedGroupsFirstNonZero(t *testing.T) {
	n := build(t, spec.KindAggregate, &spec.AggregateConfig{
		Groups: []spec.AggregateGroup{
			{Name: "message", Refs: []spec.Ref{{Producer: "a", Output: "message"}, {Producer: "b", Output: "message"}}},
			{Name: "status_code", Refs: []spec.Ref{{Producer: "a", Output: "status_code"}, {Producer: "b", Output: "status_code"}}},
		},
	})
	// Named groups are independent field-level phis with the same zero rule:
	// a zero in the first arm (empty string / status 0) falls through.
	out, err := n.Invoke(ctx(), map[string]any{
		"message":     map[string]any{"r0": "", "r1": "ok"},
		"status_code": map[string]any{"r0": 0, "r1": 200},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["message"] != "ok" {
		t.Fatalf("message = %#v, want ok (empty string is zero)", out["message"])
	}
	if out["status_code"] != 200 {
		t.Fatalf("status_code = %#v, want 200 (zero falls through)", out["status_code"])
	}
	// All-zero group => the zero value itself, never nil.
	out, _ = n.Invoke(ctx(), map[string]any{
		"message":     map[string]any{"r0": "", "r1": ""},
		"status_code": map[string]any{"r0": 0, "r1": 0},
	})
	if out["message"] != "" || out["status_code"] != 0 {
		t.Fatalf("all-zero named groups = %#v / %#v, want \"\" / 0", out["message"], out["status_code"])
	}
}

func TestSelectorBranchKey(t *testing.T) {
	n := build(t, spec.KindSelector, &spec.SelectorConfig{
		Conditions: []spec.Condition{{Operator: 1, Left: spec.Operand{Ref: &spec.Ref{Producer: "start", Output: "flag"}}, Right: spec.Operand{Val: true}}},
		Logic:      1, // or
	})
	out, err := n.Invoke(ctx(), map[string]any{"0": map[string]any{"left": true, "right": true}})
	if err != nil {
		t.Fatal(err)
	}
	if out[MarkerBranch] != "true" {
		t.Fatalf("branch = %v, want true", out[MarkerBranch])
	}
	out, _ = n.Invoke(ctx(), map[string]any{"0": map[string]any{"left": false, "right": true}})
	if out[MarkerBranch] != "false" {
		t.Fatalf("branch = %v, want false", out[MarkerBranch])
	}
}

func TestAssignVar(t *testing.T) {
	for _, kind := range []string{spec.KindAssign, spec.KindLoopAssign} {
		n := build(t, kind, &spec.AssignConfig{VarName: "v"})
		out, err := n.Invoke(ctx(), map[string]any{"value": 42})
		if err != nil {
			t.Fatal(err)
		}
		if out["v"] != 42 {
			t.Fatalf("%s out = %v, want v=42", kind, out)
		}
	}
}

func TestBreakContinueMarkers(t *testing.T) {
	for _, c := range []struct {
		kind, marker string
	}{
		{spec.KindBreak, MarkerBreak},
		{spec.KindContinue, MarkerContinue},
	} {
		n := build(t, c.kind, &spec.BreakConfig{})
		out, err := n.Invoke(ctx(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if out[c.marker] != true {
			t.Fatalf("%s out = %v, want %s=true", c.kind, out, c.marker)
		}
	}
}

// REJECT 契约（D4/U2.8, M17 收窄）:code 非 python 语言在 Build 期拒绝;
// 用户 python 自 M17 起经 pyrun 子进程执行器解锁（Build 期仅要求
// python3 解释器可用）。
func TestCodeUserPythonAccepted(t *testing.T) {
	if !pythonAvailable() {
		t.Skip("python3 not on PATH")
	}
	b, _ := json.Marshal(&spec.CodeConfig{Code: "def main() -> dict:\n    return {\"result\": 1}", Language: "python"})
	_, err := StdRegistry().Build(ctx(), &spec.NodeSpec{ID: "n", Kind: spec.KindCode, Config: b}, Deps{})
	if err != nil {
		t.Fatalf("user python must be accepted (M17): %v", err)
	}
}

func TestCodeNonPythonRejected(t *testing.T) {
	b, _ := json.Marshal(&spec.CodeConfig{Code: "def main()\n  nil\nend", Language: "ruby"})
	_, err := StdRegistry().Build(ctx(), &spec.NodeSpec{ID: "n", Kind: spec.KindCode, Config: b}, Deps{})
	if err == nil {
		t.Fatal("non-python/javascript code language must be rejected")
	}
}

func TestCodeSyntheticEval(t *testing.T) {
	b, _ := json.Marshal(&spec.CodeConfig{
		Code:      "def main(item) -> dict:\n    return {\"result\": [item]}",
		Language:  "python",
		Synthetic: true,
	})
	impl, err := StdRegistry().Build(ctx(), &spec.NodeSpec{ID: "n", Kind: spec.KindCode, Config: b}, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := impl.Invoke(ctx(), map[string]any{"item": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if got := out["result"]; !reflect.DeepEqual(got, []any{"x"}) {
		t.Fatalf("synthetic shim result = %#v, want [x]", got)
	}
}

func TestCodeSyntheticBadShimRejected(t *testing.T) {
	// M4 fail-fast:Build 期解析 shim,不支持/非法构造编译期报错。
	b, _ := json.Marshal(&spec.CodeConfig{
		// dict comprehension 不在求值器子集（仅 enumerate 列表推导受支持,
		// 生成器发射形态）→ Build 期解析拒绝（fail-fast）
		Code:      "def main(x) -> dict:\n    return {\"result\": {k: k for k in x}}",
		Language:  "python",
		Synthetic: true,
	})
	_, err := StdRegistry().Build(ctx(), &spec.NodeSpec{ID: "n", Kind: spec.KindCode, Config: b}, Deps{})
	if err == nil {
		t.Fatal("unsupported shim construct must be rejected at Build (fail-fast)")
	}
}

func TestCodeSyntheticEvalTypeError(t *testing.T) {
	// 语法合法但运行时类型错（int + str）→ Invoke 报 RExpressionType。
	b, _ := json.Marshal(&spec.CodeConfig{
		Code:      "def main(x) -> dict:\n    return {\"result\": (x + \"a\")}",
		Language:  "python",
		Synthetic: true,
	})
	impl, err := StdRegistry().Build(ctx(), &spec.NodeSpec{ID: "n", Kind: spec.KindCode, Config: b}, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := impl.Invoke(ctx(), map[string]any{"x": int64(1)}); err == nil {
		t.Fatal("int+str must fail at Invoke")
	}
}

func TestKnowledgeRejected(t *testing.T) {
	b, _ := json.Marshal(&spec.KnowledgeConfig{DatasetIDs: []string{"d1"}})
	_, err := StdRegistry().Build(ctx(), &spec.NodeSpec{ID: "n", Kind: spec.KindKnowledge, Config: b}, Deps{})
	if err == nil {
		t.Fatal("knowledge must be rejected in v1 (D4)")
	}
}
