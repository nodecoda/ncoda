package nodes

// code_norm_test.go — 输入形状/填充归一化单测（P1a-2: 唯一实现在 boundary）。
//
// 语义：声明输入按类型补齐；容器 nil → null（不是 []/{}），标量 nil → 零值，
// array<T> 的 nil 元素按 T 补零（T 为容器时保持 null）。未声明键透传。

import (
	"reflect"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/boundary"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func ts(kind string) *spec.TypeSpec { return &spec.TypeSpec{Kind: kind} }

// fillOne runs one declared value through the production boundary defaults.
func fillOne(t *testing.T, kind string, v any) any {
	t.Helper()
	b := boundary.New(boundary.Options{Policy: boundary.DefaultPolicy()})
	out, err := b.FillInputs(map[string]any{"v": v}, map[string]*spec.TypeSpec{"v": ts(kind)}, 1)
	if err != nil {
		t.Fatalf("fill %s: %v", kind, err)
	}
	return out["v"]
}

func TestShapeValueList(t *testing.T) {
	// string → 包单元素数组（.map 可迭代）
	if got := fillOne(t, "list", "hello").([]any); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("string→list: %v", got)
	}
	// JSON 数组字符串 → parse（真实契约:工具返回 JSON 数组文本）
	got := fillOne(t, "list", `[{"a":1},{"a":2}]`)
	if arr := got.([]any); len(arr) != 2 {
		t.Fatalf("json array string→list: %v", got)
	}
	// P1a-2 flip: nil 容器发布 null，不再兜成空数组（PROJECT-CONTRACT.md:423）。
	if got := fillOne(t, "list", nil); got != nil {
		t.Fatalf("nil→list = %#v, want null", got)
	}
	// 已是数组 → 零副作用
	orig := []any{"x"}
	if got := fillOne(t, "list", orig); !reflect.DeepEqual(got, orig) {
		t.Fatalf("array passthrough: %v", got)
	}
	// empty 仍发布 []（zero 与 empty 是两件事）。
	empty := fillOne(t, "list", []any{})
	if arr, ok := empty.([]any); !ok || len(arr) != 0 {
		t.Fatalf("empty list = %#v, want []", empty)
	}
}

func TestShapeValueObject(t *testing.T) {
	// string JSON 对象 → parse
	got := fillOne(t, "object", `{"code": 0, "data": {"title": "t"}}`)
	m := got.(map[string]any)
	if m["code"] != float64(0) || m["data"].(map[string]any)["title"] != "t" {
		t.Fatalf("json obj string→object: %v", got)
	}
	// 非 JSON string → {"value": s}（py data["field"] 不再 string-indices）
	got = fillOne(t, "object", "stub:data")
	if m := got.(map[string]any); m["value"] != "stub:data" {
		t.Fatalf("plain string→object: %v", got)
	}
	// P1a-2 flip: nil 容器发布 null，不再兜成空对象。
	if got := fillOne(t, "object", nil); got != nil {
		t.Fatalf("nil→object = %#v, want null", got)
	}
	empty := fillOne(t, "object", map[string]any{})
	if m, ok := empty.(map[string]any); !ok || len(m) != 0 {
		t.Fatalf("empty object = %#v, want {}", empty)
	}
}

func TestShapeValueString(t *testing.T) {
	if got := fillOne(t, "string", nil); got != "" {
		t.Fatalf("nil→string: %v", got)
	}
	if got := fillOne(t, "string", int64(42)); got != "42" {
		t.Fatalf("int→string: %v", got)
	}
	if got := fillOne(t, "string", "keep"); got != "keep" {
		t.Fatalf("string passthrough: %v", got)
	}
}

// normalize 只动声明键,未声明键透传。
func TestNormalizeInputsUndeclaredPassthrough(t *testing.T) {
	in := map[string]any{"scenes": "x", "title": "keep"}
	types := map[string]*spec.TypeSpec{"scenes": ts("list")}
	b := boundary.New(boundary.Options{Policy: boundary.DefaultPolicy()})
	out, err := b.FillInputs(in, types, 1)
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	if arr := out["scenes"].([]any); len(arr) != 1 {
		t.Fatalf("scenes normalized: %v", out["scenes"])
	}
	if out["title"] != "keep" {
		t.Fatalf("undeclared mutated: %v", out)
	}
}

// 有类型切片/映射不得被「包成单元素容器」：text split 的输出是 []string，
// 旧实现只认 []any ⇒ 下游 JS 收到 `[[]]`（元素是数组，.replace/.trim/
// idx.field 全崩）。判据 = 语料 48 份 split 下游 code 节点的共同形态。
func TestShapeValueTypedSlice(t *testing.T) {
	got := fillOne(t, "list", []string{"a", "b"})
	arr, ok := got.([]any)
	if !ok || len(arr) != 2 || arr[0] != "a" {
		t.Fatalf("[]string → list = %#v, want [a b] flattened", got)
	}
	empty := fillOne(t, "list", []string{})
	if arr, ok := empty.([]any); !ok || len(arr) != 0 {
		t.Fatalf("empty []string → list = %#v, want [] (not [[]])", empty)
	}
	recs := fillOne(t, "list", []map[string]any{{"a": 1}})
	if arr, ok := recs.([]any); !ok || len(arr) != 1 {
		t.Fatalf("[]map → list = %#v, want one element", recs)
	}
}

func TestShapeValueTypedMap(t *testing.T) {
	got := fillOne(t, "object", map[string]string{"k": "v"})
	m, ok := got.(map[string]any)
	if !ok || m["k"] != "v" || len(m) != 1 {
		t.Fatalf("map[string]string → object = %#v, want {k:v}", got)
	}
}
