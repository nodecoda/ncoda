package pyast

// reverse_test.go — P3 反向 lexer/parser 回环验证（2026-09-02）。
// 互逆不变量：Serialize(Parse(S)) == S（字节级）。golden 文件
// （pkg/ir/testdata/golden/*.py）是契约字节：带 `# ncoda:shim 1`
// 标记的单表达式模块必须精确回环；无标记 / 子集外形态解析报错（透传）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenModule 读取 golden 文件并剥离 marker（parser 不处理 marker）。
func goldenModule(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "ir", "testdata", "golden", name+".py"))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	s := string(b)
	s = strings.TrimPrefix(s, ShimMarker)
	return s
}

// reversibleGoldens 是带 marker 的单表达式 shim（可逆子集契约字节）。
var reversibleGoldens = []string{
	"condition-binary",
	"condition-ternary",
	"builtin-concat",
	"builtin-contains",
	"builtin-div",
	"builtin-enumerate",
	"builtin-first_non_empty",
	"builtin-flatten",
	"builtin-int",
	"builtin-join",
	"builtin-len",
	"builtin-range",
	"builtin-repeat",
	"builtin-replace",
	"builtin-split",
	"builtin-to_string",
	"builtin-trim",
	"json-parse-array-index",
}

func TestParseModuleRoundtrip(t *testing.T) {
	for _, name := range reversibleGoldens {
		name := name
		t.Run(name, func(t *testing.T) {
			src := goldenModule(t, name)
			m, err := ParseModule(src)
			if err != nil {
				t.Fatalf("ParseModule: %v", err)
			}
			got := Serialize(m)
			if got != src {
				t.Fatalf("module byte roundtrip failed\nwant: %q\n got: %q", src, got)
			}
		})
	}
}

func TestParseExprRoundtrip(t *testing.T) {
	for _, name := range reversibleGoldens {
		name := name
		t.Run(name, func(t *testing.T) {
			src := goldenModule(t, name)
			m, err := ParseModule(src)
			if err != nil {
				t.Fatalf("ParseModule: %v", err)
			}
			ret, ok := m.Body[len(m.Body)-1].(*ReturnStmt)
			if !ok {
				t.Fatalf("body tail is not return")
			}
			d, ok := ret.Value.(*DictExpr)
			if !ok || len(d.Items) != 1 {
				t.Fatalf("return value is not single-key dict")
			}
			exprBytes := SerializeExpr(d.Items[0].Value)
			if !strings.Contains(src, exprBytes) {
				t.Fatalf("serialized expr %q not contained in module %q", exprBytes, src)
			}
			// expression-level parse roundtrip
			e2, err := ParseExpr(exprBytes)
			if err != nil {
				t.Fatalf("ParseExpr(%q): %v", exprBytes, err)
			}
			if got := SerializeExpr(e2); got != exprBytes {
				t.Fatalf("expr byte roundtrip failed\nwant: %q\n got: %q", exprBytes, got)
			}
		})
	}
}

// 解析稳定性：同一字节两次 parse → 结构等价（Serialize 字节一致）。
func TestParseStable(t *testing.T) {
	for _, name := range reversibleGoldens {
		name := name
		t.Run(name, func(t *testing.T) {
			src := goldenModule(t, name)
			m1, err := ParseModule(src)
			if err != nil {
				t.Fatalf("ParseModule #1: %v", err)
			}
			m2, err := ParseModule(Serialize(m1))
			if err != nil {
				t.Fatalf("ParseModule #2: %v", err)
			}
			if Serialize(m2) != Serialize(m1) {
				t.Fatal("parse not idempotent")
			}
		})
	}
}

func TestParseStringEscapes(t *testing.T) {
	// quoteString 的转义族与 lexString 解码严格互逆。
	cases := []string{
		`""`,
		`"hello"`,
		`"a\"b"`,
		`"a\\b"`,
		`"a\nb"`,
		`"a\tb"`,
		`"a\rb"`,
		`"a\bb"`,
		`"a\fb"`,
		`"\u4f60\u597d"`, // 你好（BMP）
		`"\u7ea2"`,       // 红
		`"\ud83d\ude00"`, // 😀 surrogate pair
		`"mixed: \u95ee\u9898 \n \""`,
	}
	for _, lit := range cases {
		e, err := ParseExpr(lit)
		if err != nil {
			t.Errorf("ParseExpr(%q): %v", lit, err)
			continue
		}
		if got := SerializeExpr(e); got != lit {
			t.Errorf("string roundtrip %q -> %q", lit, got)
		}
	}
}

func TestParseNumber(t *testing.T) {
	cases := []string{"0", "1", "60", "123456", "1.5", "3.25"}
	for _, lit := range cases {
		e, err := ParseExpr(lit)
		if err != nil {
			t.Errorf("ParseExpr(%q): %v", lit, err)
			continue
		}
		if got := SerializeExpr(e); got != lit {
			t.Errorf("number roundtrip %q -> %q", lit, got)
		}
	}
}

// 子集外形态必须报错（反向判定失败 → P4 透传，绝不半反向）。
func TestParseRejectsOutsideSubset(t *testing.T) {
	bad := []string{
		"f\"fmt {x}\"",       // f-string（map-lambda 形态，v2）
		"x := 1",             // walrus（非子集）
		"lambda x: x",        // lambda（非子集）
		"`not a thing`",      // 反引号
		"\"bad \\q escape\"", // 未知转义
		"import json",        // 裸 import 表达式
		"1 +",                // 悬空操作符
	}
	for _, src := range bad {
		if _, err := ParseExpr(src); err == nil {
			t.Errorf("ParseExpr(%q) should fail (outside subset)", src)
		}
	}
	// 函数体内 import（json-stringify 形态）在模块级解析时报错
	src := "def main(arg1) -> dict:\n    import json\n    return {\"result\": json.dumps(arg1, ensure_ascii=False)}\n"
	if _, err := ParseModule(src); err == nil {
		t.Error("function-body import should fail (outside reversible subset)")
	}
	// 多语句（json.parse 形态）可解析（parser 能力），但由 P4 形态判定不透传
	src2 := "import json\ndef main(arg1) -> dict:\n    data = json.loads(arg1)\n    return {\"result\": (data if isinstance(data, list) else [])}\n"
	m, err := ParseModule(src2)
	if err != nil {
		t.Fatalf("multi-statement module should parse: %v", err)
	}
	if len(m.Body) != 2 || len(m.Imports) != 1 {
		t.Fatalf("unexpected module shape: imports=%d body=%d", len(m.Imports), len(m.Body))
	}
}
