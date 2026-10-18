package expr

import (
	"reflect"
	"testing"

	"github.com/nodecoda/ncoda/pkg/pyast"
)

// shim_test.go — M4 synthetic-shim 求值器测试。
//
// 覆盖:
//  1. parse/serialize round-trip 稳定性（Parse(Serialize(Parse(x))) == Parse(x)）
//  2. 语料 11 种 shim 形状的 eval 断言（期望值从 Python 子集语义推导）
//  3. 边界:json.loads/dumps kwargs、int+int、str(None)、方法调用、三元、下标

// ── round-trip 稳定性 ────────────────────────────────────────────────────

func TestShimRoundTripStable(t *testing.T) {
	cases := []string{
		`def main(item) -> dict:
    return {"result": [item]}`,
		`def main(query, context) -> dict:
    return {"result": ("问题：" + str(query) + "" + ("\n上下文：" + str(context) + "" if (context != "") else ""))}`,
		`def main() -> dict:
    return {"result": ""}`,
		`def main() -> dict:
    return {"result": "0"}`,
		`def main(value) -> dict:
    return {"result": (value + ":second")}`,
		`def main(value) -> dict:
    return {"result": ((value + 1) > 2)}`,
		`def main(greeting, q) -> dict:
    return {"result": (greeting + q)}`,
		`def main(response_body) -> dict:
    return {"result": ("unexpected-success:" + response_body)}`,
		`def main(error_error_type) -> dict:
    return {"result": ("attempt-recovered:" + error_error_type)}`,
		`def main() -> dict:
    return {"result": ["a", "bad", "c"]}`,
		`import json
def main(arg1) -> dict:
    data = json.loads(arg1)
    return {"result": data.get("content", "")}`,
	}
	for i, src := range cases {
		m, err := ParseShim(src)
		if err != nil {
			t.Fatalf("case %d parse: %v", i, err)
		}
		once := pyast.Serialize(m)
		m2, err := ParseShim(once)
		if err != nil {
			t.Fatalf("case %d reparse: %v\nserialized:\n%s", i, err, once)
		}
		twice := pyast.Serialize(m2)
		if once != twice {
			t.Fatalf("case %d not stable:\nfirst:\n%s\nsecond:\n%s", i, once, twice)
		}
	}
}

// ── 语料 shim eval 断言 ─────────────────────────────────────────────────

func TestShimEvalCorpus(t *testing.T) {
	cases := []struct {
		name   string
		code   string
		inputs map[string]any
		want   map[string]any
	}{
		{
			name:   "wrap-list",
			code:   "def main(item) -> dict:\n    return {\"result\": [item]}",
			inputs: map[string]any{"item": "x"},
			want:   map[string]any{"result": []any{"x"}},
		},
		{
			name: "prompt-ctx",
			code: "def main(query, context) -> dict:\n    return {\"result\": (\"\u95ee\u9898\uff1a\" + str(query) + \"\" + (\"\\n\u4e0a\u4e0b\u6587\uff1a\" + str(context) + \"\" if (context != \"\") else \"\"))}",
			inputs: map[string]any{
				"query":   "hello",
				"context": "ctx1",
			},
			want: map[string]any{"result": "问题：hello\n上下文：ctx1"},
		},
		{
			name:   "empty-context-skipped",
			code:   "def main(query, context) -> dict:\n    return {\"result\": (\"\u95ee\u9898\uff1a\" + str(query) + \"\" + (\"\\n\u4e0a\u4e0b\u6587\uff1a\" + str(context) + \"\" if (context != \"\") else \"\"))}",
			inputs: map[string]any{"query": "hi", "context": ""},
			want:   map[string]any{"result": "问题：hi"},
		},
		{
			name: "empty-result",
			code: "def main() -> dict:\n    return {\"result\": \"\"}",
			want: map[string]any{"result": ""},
		},
		{
			name: "zero-result",
			code: "def main() -> dict:\n    return {\"result\": \"0\"}",
			want: map[string]any{"result": "0"},
		},
		{
			name:   "second-suffix",
			code:   "def main(value) -> dict:\n    return {\"result\": (value + \":second\")}",
			inputs: map[string]any{"value": "v1"},
			want:   map[string]any{"result": "v1:second"},
		},
		{
			name:   "final-suffix",
			code:   "def main(value) -> dict:\n    return {\"result\": (value + \":final\")}",
			inputs: map[string]any{"value": "v2"},
			want:   map[string]any{"result": "v2:final"},
		},
		{
			name:   "gt-bool",
			code:   "def main(value) -> dict:\n    return {\"result\": ((value + 1) > 2)}",
			inputs: map[string]any{"value": int64(1)},
			want:   map[string]any{"result": false},
		},
		{
			name:   "gt-bool-true",
			code:   "def main(value) -> dict:\n    return {\"result\": ((value + 1) > 2)}",
			inputs: map[string]any{"value": int64(2)},
			want:   map[string]any{"result": true},
		},
		{
			name:   "concat",
			code:   "def main(greeting, q) -> dict:\n    return {\"result\": (greeting + q)}",
			inputs: map[string]any{"greeting": "你好，", "q": "世界"},
			want:   map[string]any{"result": "你好，世界"},
		},
		{
			name:   "unexpected-success",
			code:   "def main(response_body) -> dict:\n    return {\"result\": (\"unexpected-success:\" + response_body)}",
			inputs: map[string]any{"response_body": "ok"},
			want:   map[string]any{"result": "unexpected-success:ok"},
		},
		{
			name:   "attempt-recovered",
			code:   "def main(error_error_type) -> dict:\n    return {\"result\": (\"attempt-recovered:\" + error_error_type)}",
			inputs: map[string]any{"error_error_type": "Timeout"},
			want:   map[string]any{"result": "attempt-recovered:Timeout"},
		},
		{
			name: "list-literal",
			code: "def main() -> dict:\n    return {\"result\": [\"a\", \"bad\", \"c\"]}",
			want: map[string]any{"result": []any{"a", "bad", "c"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := EvalShim(c.code, c.inputs)
			if err != nil {
				t.Fatalf("EvalShim: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("EvalShim = %#v, want %#v", got, c.want)
			}
		})
	}
}

// ── 边界:json / 算术 / 方法 / 下标 ──────────────────────────────────────

func TestShimEvalJSON(t *testing.T) {
	code := `import json
def main(arg1) -> dict:
    data = json.loads(arg1)
    return {"result": data.get("content", "")}`
	got, err := EvalShim(code, map[string]any{"arg1": `{"content": "hello", "n": 3}`})
	if err != nil {
		t.Fatal(err)
	}
	if got["result"] != "hello" {
		t.Fatalf("json path result = %#v, want hello", got["result"])
	}
	// dict 键缺失 → 默认值
	got, err = EvalShim(code, map[string]any{"arg1": `{"other": 1}`})
	if err != nil {
		t.Fatal(err)
	}
	if got["result"] != "" {
		t.Fatalf("default result = %#v, want \"\"", got["result"])
	}
	// 非法 JSON → 错误
	if _, err = EvalShim(code, map[string]any{"arg1": "not-json"}); err == nil {
		t.Fatal("want error on invalid json")
	}
}

func TestShimEvalJSONDumps(t *testing.T) {
	code := `import json
def main(arg1) -> dict:
    return {"result": json.dumps(arg1, ensure_ascii=False)}`
	got, err := EvalShim(code, map[string]any{"arg1": map[string]any{"a": "中文<>&"}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":"中文<>&"}`
	if got["result"] != want {
		t.Fatalf("dumps = %#v, want %#v", got["result"], want)
	}
}

func TestShimEvalArith(t *testing.T) {
	cases := []struct {
		code  string
		input any
		want  any
	}{
		{`def main(x) -> dict:
    return {"result": (x + 1)}`, int64(41), int64(42)}, // int+int → int
		{`def main(x) -> dict:
    return {"result": (x + 1)}`, 2.5, 3.5}, // float 参与 → float
		{`def main(x) -> dict:
    return {"result": (x * 2)}`, int64(21), int64(42)},
	}
	for _, c := range cases {
		got, err := EvalShim(c.code, map[string]any{"x": c.input})
		if err != nil {
			t.Fatalf("%s: %v", c.code, err)
		}
		if got["result"] != c.want {
			t.Fatalf("result = %#v (%T), want %#v", got["result"], got["result"], c.want)
		}
	}
}

func TestShimEvalStr(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, "None"},
		{true, "True"},
		{false, "False"},
		{int64(42), "42"},
		{2.0, "2.0"}, // Python str(2.0) 保留 .0
	}
	for _, c := range cases {
		code := `def main(x) -> dict:
    return {"result": str(x)}`
		got, err := EvalShim(code, map[string]any{"x": c.in})
		if err != nil {
			t.Fatalf("str(%v): %v", c.in, err)
		}
		if got["result"] != c.want {
			t.Fatalf("str(%v) = %#v, want %#v", c.in, got["result"], c.want)
		}
	}
}

func TestShimEvalMethodsAndSubscript(t *testing.T) {
	// .get with default / .startswith / 下标
	code := `def main(data) -> dict:
    return {"result": (data.get("k", "d") + ":" + (data.get("s") or "") + ":" + str(data["s"].startswith("he")))}`
	got, err := EvalShim(code, map[string]any{"data": map[string]any{"s": "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if got["result"] != "d:hello:True" {
		t.Fatalf("result = %#v, want d:hello:True", got["result"])
	}
	// 列表下标
	code2 := `def main(items) -> dict:
    return {"result": items[1]}`
	got2, err := EvalShim(code2, map[string]any{"items": []any{"a", "b", "c"}})
	if err != nil {
		t.Fatal(err)
	}
	if got2["result"] != "b" {
		t.Fatalf("subscript = %#v, want b", got2["result"])
	}
}

func TestShimEvalRange(t *testing.T) {
	calls := []struct {
		name string
		src  string
		want []any
	}{
		{"stop", "def main() -> dict:\n    return {\"result\": list(range(3))}", []any{int64(0), int64(1), int64(2)}},
		{"start-stop", "def main() -> dict:\n    return {\"result\": list(range(1, 4))}", []any{int64(1), int64(2), int64(3)}},
		{"step", "def main() -> dict:\n    return {\"result\": list(range(0, 10, 3))}", []any{int64(0), int64(3), int64(6), int64(9)}},
		{"neg-step", "def main() -> dict:\n    return {\"result\": list(range(5, 0, -2))}", []any{int64(5), int64(3), int64(1)}},
		{"in-listcomp", "def main(v) -> dict:\n    return {\"result\": len(list(range(1, v)))}", nil}, // 只验证不报错
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			scope := map[string]any{"v": int64(5)}
			got, err := EvalShim(tc.src, scope)
			if err != nil {
				t.Fatalf("EvalShim(%q) err: %v", tc.src, err)
			}
			if tc.want == nil {
				return
			}
			res, ok := got["result"]
			if !ok {
				t.Fatalf("EvalShim(%q) result missing", tc.src)
			}
			gotL, ok := res.([]any)
			if !ok {
				t.Fatalf("EvalShim(%q) result = %T, want []any", tc.src, res)
			}
			if len(gotL) != len(tc.want) {
				t.Fatalf("len = %d, want %d (%v)", len(gotL), len(tc.want), gotL)
			}
			for i := range tc.want {
				if gotL[i] != tc.want[i] {
					t.Fatalf("elem %d = %v, want %v", i, gotL[i], tc.want[i])
				}
			}
		})
	}
}

func TestShimEvalErrors(t *testing.T) {
	cases := []struct {
		name string
		code string
		in   map[string]any
	}{
		{"undefined-name", "def main(x) -> dict:\n    return {\"result\": (y + 1)}", map[string]any{"x": int64(1)}},
		{"no-return", "def main(x) -> dict:\n    import json", map[string]any{"x": int64(1)}},
		{"mixed-concat", "def main(x) -> dict:\n    return {\"result\": (x + \"a\")}", map[string]any{"x": int64(1)}},
		{"list-oob", "def main(x) -> dict:\n    return {\"result\": x[9]}", map[string]any{"x": []any{"a"}}},
		{"unknown-func", "def main(x) -> dict:\n    return {\"result\": nope(x)}", map[string]any{"x": int64(1)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := EvalShim(c.code, c.in); err == nil {
				t.Fatalf("want error for %s", c.name)
			}
		})
	}
}

func TestShimEvalTruthyShortCircuit(t *testing.T) {
	// Python 语义:左侧已定值时右侧不求值（未定义名 nope 会报错 → 求值=报错）。
	// and 左侧假 → 短路,不报错;左侧真 → 求值右侧 → 报错。
	// or 左侧真 → 短路,不报错;左侧假 → 求值右侧 → 报错。
	andFalse := `def main(x) -> dict:
    return {"result": ((x != "") and nope(x))}`
	if _, err := EvalShim(andFalse, map[string]any{"x": ""}); err != nil {
		t.Fatalf("and 左侧假应短路: %v", err)
	}
	andTrue := `def main(x) -> dict:
    return {"result": ((x == "") and nope(x))}`
	if _, err := EvalShim(andTrue, map[string]any{"x": ""}); err == nil {
		t.Fatal("and 左侧真应求值右侧并报错")
	}
	orTrue := `def main(x) -> dict:
    return {"result": ((x == "") or nope(x))}`
	if _, err := EvalShim(orTrue, map[string]any{"x": ""}); err != nil {
		t.Fatalf("or 左侧真应短路: %v", err)
	}
	orFalse := `def main(x) -> dict:
    return {"result": ((x != "") or nope(x))}`
	if _, err := EvalShim(orFalse, map[string]any{"x": ""}); err == nil {
		t.Fatal("or 左侧假应求值右侧并报错")
	}
}

func TestShimEvalJSONParseRecord(t *testing.T) {
	// json.parse<record> 形状（IR pyast 路径,import 在模块顶部）:
	// 多语句 body + 每字段 .get 带类型默认值。
	code := `import json
def main(arg1) -> dict:
    data = json.loads(arg1)
    return {"a": data.get("a", 0), "b": data.get("b", "")}`
	got, err := EvalShim(code, map[string]any{"arg1": `{"a": 7, "b": "x"}`})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"a": float64(7), "b": "x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("record = %#v, want %#v", got, want)
	}
	// 缺字段 → 默认值（int 0,string ""）
	got, err = EvalShim(code, map[string]any{"arg1": `{}`})
	if err != nil {
		t.Fatal(err)
	}
	// 缺字段默认值:.get 返回字面默认（int64 0）,JSON 命中值是 float64
	if n, ok := toNum(got["a"]); !ok || n != 0 {
		t.Fatalf("default a = %#v, want numeric 0", got["a"])
	}
	if got["b"] != "" {
		t.Fatalf("default b = %#v, want empty string", got["b"])
	}
}

func TestShimEvalJSONParseArrayGuard(t *testing.T) {
	// json.parse<array<T>> 形状:isinstance(data, list) guard。
	code := `import json
def main(arg1) -> dict:
    data = json.loads(arg1)
    return {"result": (data if isinstance(data, list) else [])}`
	got, err := EvalShim(code, map[string]any{"arg1": `[1, 2, 3]`})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got["result"], []any{float64(1), float64(2), float64(3)}) {
		t.Fatalf("list pass-through = %#v", got["result"])
	}
	// 非数组 → 空列表
	got, err = EvalShim(code, map[string]any{"arg1": `{"k": 1}`})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got["result"], []any{}) {
		t.Fatalf("guard fallback = %#v, want []", got["result"])
	}
}

func TestShimEvalCoalesce(t *testing.T) {
	// coalesce → (x or fall):x 真值保留,空/None 落到 fall。
	code := `def main(x) -> dict:
    return {"result": (x or "fall")}`
	for _, c := range []struct {
		in   any
		want any
	}{
		{"v", "v"},
		{"", "fall"},
		{nil, "fall"},
	} {
		got, err := EvalShim(code, map[string]any{"x": c.in})
		if err != nil {
			t.Fatalf("coalesce(%v): %v", c.in, err)
		}
		if got["result"] != c.want {
			t.Fatalf("coalesce(%v) = %#v, want %#v", c.in, got["result"], c.want)
		}
	}
}

// M14 回归:loop state 的 JSON 字符串数值（"1"）在 shim 算术中按数值处理
// （IR 把 integer literal 存为字符串,lv.value=FormatInt）。str+str 仍走
// 拼接（toNum 只是宽容,不改变既有字符串语义）。
func TestShimEvalNumericString(t *testing.T) {
	cases := []struct {
		name  string
		code  string
		input any
		want  any
	}{
		{"str-int-plus-int", `def main(x) -> dict:
    return {"result": (x + 1)}`, "1", int64(2)}, // "1"+1 → int（数值字符串）
		{"str-float-plus-int", `def main(x) -> dict:
    return {"result": (x + 1)}`, "2.5", 3.5}, // "2.5"+1 → float
		{"float-int-valued", `def main(x) -> dict:
    return {"result": (x + 1)}`, 2.0, int64(3)}, // float 整值参与 → int
		{"str-plus-str-concat", `def main(x) -> dict:
    return {"result": (x + "b")}`, "a", "ab"}, // str+str → 拼接（不误解为数值）
	}
	for _, c := range cases {
		got, err := EvalShim(c.code, map[string]any{"x": c.input})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got["result"] != c.want {
			t.Fatalf("%s: result = %#v (%T), want %#v", c.name, got["result"], got["result"], c.want)
		}
	}
}

// M14 回归:非数值字符串在算术中仍报错（toNum 只对数值字符串成功）。
func TestShimEvalNonNumericStringArith(t *testing.T) {
	_, err := EvalShim(`def main(x) -> dict:
    return {"result": (x + 1)}`, map[string]any{"x": "abc"})
	if err == nil {
		t.Fatal("expected error for non-numeric string in arithmetic")
	}
}

// isIntVal / toNum 直接契约（loop state 数值字符串 + float 整值）。
func TestShimNumCoercionHelpers(t *testing.T) {
	if !isIntVal("1") || isIntVal("1.5") || isIntVal("abc") {
		t.Fatal("isIntVal: numeric strings only")
	}
	if !isIntVal(2.0) || isIntVal(2.5) {
		t.Fatal("isIntVal: integral floats only")
	}
	for _, s := range []string{"0", "-3", "42.0"} {
		f, ok := toNum(s)
		if !ok {
			t.Fatalf("toNum(%q) failed", s)
		}
		_ = f
	}
	if _, ok := toNum("abc"); ok {
		t.Fatal("toNum(non-numeric) must fail")
	}
}

// ── 列表推导（enumerate 计算内建,pygen 发射形态）──────────────────────

func TestShimEvalEnumerateListComp(t *testing.T) {
	code := `def main(texts) -> dict:
    return {"result": [{"index": i, "value": v} for i, v in enumerate(texts)]}`
	got, err := EvalShim(code, map[string]any{"texts": []any{"a", "b", "c"}})
	if err != nil {
		t.Fatalf("EvalShim: %v", err)
	}
	want := map[string]any{"result": []any{
		map[string]any{"index": int64(0), "value": "a"},
		map[string]any{"index": int64(1), "value": "b"},
		map[string]any{"index": int64(2), "value": "c"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EvalShim = %#v, want %#v", got, want)
	}
}

func TestShimEvalSimpleListComp(t *testing.T) {
	code := `def main(xs) -> dict:
    return {"result": [x for x in xs]}`
	got, err := EvalShim(code, map[string]any{"xs": []any{"a", "b"}})
	if err != nil {
		t.Fatalf("EvalShim: %v", err)
	}
	if !reflect.DeepEqual(got, map[string]any{"result": []any{"a", "b"}}) {
		t.Fatalf("EvalShim = %#v", got)
	}
}

// 非列表可迭代 → 报错（不 panic）。
func TestShimEvalListCompNonList(t *testing.T) {
	code := `def main(xs) -> dict:
    return {"result": [x for x in xs]}`
	if _, err := EvalShim(code, map[string]any{"xs": "not-a-list"}); err == nil {
		t.Fatalf("want error for non-list iterable")
	}
}

// ── R-128 第二刀补的两个 builtin（int/float）+ 求值器新增的 7 个形态 ────────
// 两端同解判据在 internal/pygen（名册 × 求值器，覆盖 + 语义），这里只锁
// **求值器一侧不可让**的性质：**语义对齐 Python，不宽容** —— 宽容 ⇒
// rt-l3 绿而平台 CodeRunner（真 Python）红 = 假绿。

func TestShimEvalIntFloatFaithful(t *testing.T) {
	type intFloatCase struct {
		name string
		code string
		in   map[string]any
		want any
	}
	ok := []intFloatCase{
		{"int(string)", `def main(x) -> dict:
    return {"result": int(x)}`, map[string]any{"x": "764"}, int64(764)},
		{"int(float) 向零截断", `def main(x) -> dict:
    return {"result": int(x)}`, map[string]any{"x": -1.9}, int64(-1)},
		{"float(string)", `def main(x) -> dict:
    return {"result": float(x)}`, map[string]any{"x": "10"}, float64(10)},
		{"int 已 int 原样", `def main(x) -> dict:
    return {"result": int(x)}`, map[string]any{"x": int64(7)}, int64(7)},
	}
	for _, c := range ok {
		got, err := EvalShim(c.code, c.in)
		if err != nil {
			t.Fatalf("%s: EvalShim: %v", c.name, err)
		}
		if !reflect.DeepEqual(got, map[string]any{"result": c.want}) {
			t.Fatalf("%s: EvalShim = %#v, want %#v", c.name, got, c.want)
		}
	}
	// 非数字串必须**报错**（Python: ValueError）—— 不是静默 0。
	for _, bad := range []string{"", "abc", "1.5"} {
		if _, err := EvalShim("def main(x) -> dict:\n    return {\"result\": int(x)}", map[string]any{"x": bad}); err == nil {
			t.Fatalf("int(%q) 必须报错（Python ValueError），不得宽容", bad)
		}
	}
	if _, err := EvalShim("def main(x) -> dict:\n    return {\"result\": float(x)}", map[string]any{"x": "abc"}); err == nil {
		t.Fatalf("float(\"abc\") 必须报错")
	}
}

// repeat 内建（`[v] * n`）与 flatten（多子句推导）与 first_non_empty（括号生成器）。
func TestShimEvalSequenceForms(t *testing.T) {
	got, err := EvalShim("def main(v, n) -> dict:\n    return {\"result\": [v] * n}", map[string]any{"v": "a", "n": int64(2)})
	if err != nil {
		t.Fatalf("repeat: %v", err)
	}
	if !reflect.DeepEqual(got, map[string]any{"result": []any{"a", "a"}}) {
		t.Fatalf("repeat = %#v", got)
	}
	got, err = EvalShim("def main(xs) -> dict:\n    return {\"result\": [str(x) for g in (xs or []) if isinstance(g, list) for x in g]}",
		map[string]any{"xs": []any{[]any{"a"}, []any{"b"}}})
	if err != nil {
		t.Fatalf("flatten: %v", err)
	}
	if !reflect.DeepEqual(got, map[string]any{"result": []any{"a", "b"}}) {
		t.Fatalf("flatten = %#v", got)
	}
	got, err = EvalShim("def main(xs) -> dict:\n    return {\"result\": next((str(u) for u in (xs or []) if u and str(u).strip()), \"\")}",
		map[string]any{"xs": []any{"", "  ", "c"}})
	if err != nil {
		t.Fatalf("first_non_empty: %v", err)
	}
	if !reflect.DeepEqual(got, map[string]any{"result": "c"}) {
		t.Fatalf("first_non_empty = %#v", got)
	}
}
