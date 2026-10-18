package expr

import (
	"encoding/json"
	"testing"
)

// ── Compare (coze SelectorOperators 1–16) ────────────────────────────────

func TestCompare(t *testing.T) {
	cases := []struct {
		name string
		op   int
		lv   any
		rv   any
		want bool
	}{
		// 1=`=`（Sprint 字符串强转相等）
		{"eq-str", 1, "a", "a", true},
		{"eq-diff", 1, "a", "b", false},
		{"eq-coerce", 1, 1, "1", true},
		// 2=`!=`
		{"ne-diff", 2, "a", "b", true},
		{"ne-same", 2, "a", "a", false},
		// 7=contain
		{"contain-hit", 7, "hello world", "world", true},
		{"contain-miss", 7, "hello world", "xyz", false},
		{"contain-empty-needle", 7, "abc", "", true},
		// 8=not_contain
		{"notcontain-hit", 8, "hello world", "xyz", true},
		{"notcontain-miss", 8, "hello world", "world", false},
		// 9=empty（Sprint 结果为空；nil 的 Sprint 是 "<nil>"，不算空 —— 现有契约）
		{"empty-empty", 9, "", "", true},
		{"empty-nonempty", 9, "x", "", false},
		{"empty-nil", 9, nil, "", false},
		// 10=not_empty
		{"notempty-nonempty", 10, "x", "", true},
		{"notempty-empty", 10, "", "", false},
		// 11=true
		{"istrue-bool", 11, true, "", true},
		{"istrue-str", 11, "x", "", true},
		{"istrue-empty", 11, "", "", false},
		{"istrue-nil", 11, nil, "", false},
		// 12=false —— 已知缺口：当前恒 false（coze 语义应为 !Truthy(lv)，
		// M2.1 与上游对齐；此处锁定 M1 行为）。
		{"isfalse-any", 12, "x", "", false},
		{"isfalse-false", 12, false, "", false},
		{"isfalse-nil", 12, nil, "", false},
		// 13=`>` 14=`>=` 15=`<` 16=`<=`
		{"gt-hit", 13, 5, 3, true},
		{"gt-miss", 13, 3, 5, false},
		{"gte-eq", 14, 5, 5, true},
		{"gte-miss", 14, 3, 5, false},
		{"lt-hit", 15, 3, 5, true},
		{"lt-miss", 15, 5, 3, false},
		{"lte-eq", 16, 5, 5, true},
		{"lte-miss", 16, 5, 3, false},
		// 非数值 → 比较不可用 → false
		{"gt-nonnumeric", 13, "a", 5, false},
		// 未知 op → false（条件不满足 = 不选中该分支）
		{"unknown-0", 0, "a", "a", false},
		{"unknown-99", 99, "a", "a", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Compare(c.op, c.lv, c.rv); got != c.want {
				t.Fatalf("Compare(%d, %v, %v) = %v, want %v", c.op, c.lv, c.rv, got, c.want)
			}
		})
	}
}

// ── Truthy ───────────────────────────────────────────────────────────────

func TestTruthy(t *testing.T) {
	cases := []struct {
		v    any
		want bool
	}{
		{true, true},
		{false, false},
		{nil, false},
		{"", false},
		{"x", true},
		{0, true}, // 非 bool/nil：按 Sprint 非空判定，0 视为真（现有契约）
	}
	for _, c := range cases {
		if got := Truthy(c.v); got != c.want {
			t.Fatalf("Truthy(%v) = %v, want %v", c.v, got, c.want)
		}
	}
}

// ── ToFloat / NumericPair ────────────────────────────────────────────────

func TestToFloat(t *testing.T) {
	cases := []struct {
		name string
		v    any
		want float64
		ok   bool
	}{
		{"int", 42, 42, true},
		{"int64", int64(-7), -7, true},
		{"float64", 3.14, 3.14, true},
		{"float32", float32(1.5), 1.5, true},
		{"json.Number", json.Number("3.14"), 3.14, true},
		{"numeric-string", "42", 42, true},
		{"numeric-string-float", "3.5", 3.5, true},
		{"numeric-string-space", " 42 ", 0, false}, // ParseFloat 不接受首尾空白（现有契约）
		{"non-numeric-string", "abc", 0, false},
		{"nil", nil, 0, false},
		{"bool", true, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ToFloat(c.v)
			if ok != c.ok || (ok && got != c.want) {
				t.Fatalf("ToFloat(%v) = %v, %v; want %v, %v", c.v, got, ok, c.want, c.ok)
			}
		})
	}
}

func TestNumericPair(t *testing.T) {
	if l, r, ok := NumericPair("5", 5.0); !ok || l != 5 || r != 5 {
		t.Fatalf("NumericPair(5, 5.0) = %v, %v, %v; want 5, 5, true", l, r, ok)
	}
	if _, _, ok := NumericPair("x", 5); ok {
		t.Fatalf("NumericPair(x, 5) ok = true, want false")
	}
}

// ── RenderTemplate ───────────────────────────────────────────────────────

func TestRenderTemplate(t *testing.T) {
	cases := []struct {
		name string
		tpl  string
		vars map[string]any
		want string
	}{
		{"plain", "hello", nil, "hello"},
		{"single", "hi {{name}}", map[string]any{"name": "dev"}, "hi dev"},
		{"trim-key", "{{ name }}", map[string]any{"name": "dev"}, "dev"},
		{"multi", "{{a}}/{{b}}", map[string]any{"a": "1", "b": "2"}, "1/2"},
		{"missing", "{{nope}}", map[string]any{"a": "1"}, "<missing>"},
		{"nil-value", "{{a}}", map[string]any{"a": nil}, "<missing>"},
		// 未闭合 {{：标记被消费，剩余原文保留（现有契约）
		{"unclosed", "a {{x", map[string]any{}, "a x"},
		{"no-open", "a }} b", nil, "a }} b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RenderTemplate(c.tpl, c.vars); got != c.want {
				t.Fatalf("RenderTemplate(%q, %v) = %q, want %q", c.tpl, c.vars, got, c.want)
			}
		})
	}
}

// 确定性 + 不修改入参（并发安全基础）。
func TestRenderTemplateDeterministicAndPure(t *testing.T) {
	vars := map[string]any{"a": "1", "b": "2"}
	want := "1/2/3"
	tpl := "{{a}}/{{b}}/3"
	if got := RenderTemplate(tpl, vars); got != want {
		t.Fatalf("first pass = %q, want %q", got, want)
	}
	if got := RenderTemplate(tpl, vars); got != want {
		t.Fatalf("second pass = %q, want %q (nondeterministic)", got, want)
	}
	if len(vars) != 2 {
		t.Fatalf("vars mutated: len = %d, want 2", len(vars))
	}
}

// ── M16: FormatTemplateValue 复合值渲染契约 ─────────────────────────────

func TestFormatTemplateValue(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"string", "dev", "dev"},
		{"int", 42, "42"},
		{"float", 3.5, "3.5"},
		{"bool", true, "true"},
		{"nil", nil, "<nil>"},
		// 复合值 → 紧凑 JSON（不再 fmt.Sprint 的 [map[content:...]]）
		{"map", map[string]any{"content": "doc-a", "score": 0.9}, `{"content":"doc-a","score":0.9}`},
		{"slice", []any{"a", "b"}, `["a","b"]`},
		{"knowledge-chunks", []any{
			map[string]any{"content": "go is compiled", "title": "Go", "score": 0.36},
			map[string]any{"content": "rust is systems", "title": "Rust", "score": 0.1},
		}, `[{"content":"go is compiled","score":0.36,"title":"Go"},{"content":"rust is systems","score":0.1,"title":"Rust"}]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FormatTemplateValue(c.in); got != c.want {
				t.Fatalf("FormatTemplateValue(%#v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// RenderTemplate 对复合值走 JSON 渲染（RAG 结果注入 prompt 可读）。
func TestRenderTemplateCompositeValue(t *testing.T) {
	got := RenderTemplate("ref: {{ctx}}", map[string]any{
		"ctx": []any{map[string]any{"content": "doc-a", "score": 0.9}},
	})
	want := `ref: [{"content":"doc-a","score":0.9}]`
	if got != want {
		t.Fatalf("RenderTemplate = %q, want %q", got, want)
	}
}
