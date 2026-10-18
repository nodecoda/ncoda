package parser

// `return template "…" [using {…}]`（W2）的语法面：模板槽 = 字符串字面量
// （§1.1 裁定的被否形态在这里逐一锁死）、`using` 可省、`template` 是**上下文
// 关键字**（不是保留字 —— `return template;` 仍返回名为 template 的变量）。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
)

func TestParseReturnTemplateTextOnly(t *testing.T) {
	src := `@mode workflow
function main(string x) {
    return template "结果输出到了填入的多维表格中";
}
`
	prog, errs := ParseSource(src, "rt_text.ncoda")
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	rt := firstTemplateValue(t, prog)
	if rt.Text() != "结果输出到了填入的多维表格中" {
		t.Errorf("text = %q", rt.Text())
	}
	if len(rt.Bindings()) != 0 {
		t.Errorf("bindings = %d, want 0", len(rt.Bindings()))
	}
}

func TestParseReturnTemplateBindings(t *testing.T) {
	src := `@mode workflow
function main(string x) {
    return template "![]({{image[0]}})" using { output: v1.body, image: v2.images };
}
`
	prog, errs := ParseSource(src, "rt_bind.ncoda")
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	rt := firstTemplateValue(t, prog)
	if rt.Text() != "![]({{image[0]}})" {
		t.Errorf("text = %q", rt.Text())
	}
	bs := rt.Bindings()
	if len(bs) != 2 {
		t.Fatalf("bindings = %d, want 2", len(bs))
	}
	if bs[0].Name != "output" || bs[1].Name != "image" {
		t.Errorf("names = %q,%q（源序 = wire 序，保序是保真的必要条件）", bs[0].Name, bs[1].Name)
	}
}

// 模板文本**逐字**承载：`{{…}}` / `${…}` / 换行 / 空白都不解析、不改写。
func TestParseReturnTemplateVerbatimText(t *testing.T) {
	for _, text := range []string{
		"根 ≠ 名额名：{{draft_url}}",
		"带下标 {{image[0]}} 与子路径 {{a.b.c}}",
		"未被名额引用的 {{ghost}}",
		"${not_ncoda}",
		"多行\n第二行\t制表",
	} {
		src := "@mode workflow\nfunction main(string x) {\n    return template " + quoteForTest(text) + ";\n}\n"
		prog, errs := ParseSource(src, "rt_verbatim.ncoda")
		if len(errs) > 0 {
			t.Fatalf("%q: parse errors: %v", text, errs)
		}
		if got := firstTemplateValue(t, prog).Text(); got != text {
			t.Errorf("逐字保真失败：got %q, want %q", got, text)
		}
	}
}

// `template` 是上下文关键字（不是保留字）：`return template;` 返回变量 template。
func TestParseReturnTemplateMarkerIsContextual(t *testing.T) {
	src := `@mode workflow
function main(string x) -> string {
    let template = "v";
    return template;
}
`
	prog, errs := ParseSource(src, "rt_ctx.ncoda")
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	value := entryReturnValue(t, prog)
	if _, isTemplate := value.(*ast.TemplateValue); isTemplate {
		t.Error("`return template;` 被解析成了模板返回（template 不应是保留字）")
	}
	if id, ok := value.(*ast.IdentExpr); !ok || id.Name() != "template" {
		t.Errorf("`return template;` 的值 = %T，期望标识符 template", value)
	}
}

// 名额重名 = 语法错（语言侧硬约束：名字是平台名额名，重复无名可承载）。
func TestParseReturnTemplateDuplicateSlotRejected(t *testing.T) {
	src := `@mode workflow
function main(string x) {
    return template "t" using { a: x, a: x };
}
`
	if _, errs := ParseSource(src, "rt_dup.ncoda"); len(errs) == 0 {
		t.Fatal("expected duplicate slot error")
	}
}

// 模板**标记**仍要求 `template` 紧跟字符串**字面量**（§1.1）：变量 / 调用 /
// 反引号插值串都不是字面量 ⇒ 语法错（`template` 这时退回普通标识符）。
//
// ⚠ 2026-09-25 修正：原用例里还有一条 `"a" + "b"`，其断言（「拼接 ⇒ 语法错」）
// **已被语言变更取代** —— `template "…" [using {…}]` 现在是**表达式**
// （`parsePrimary` 的模板分支，R-68 甲，用户批准）⇒ `template "a" + "b"` 的
// 解析 = `(template "a") + "b"`，是**合法表达式**（类型 string），不再是语法错。
// 「它到底能不能承载」是**承载位**问题（值位只有 `output(…)` 实参 / llm message
// content / `return` 值三处成立），那句话由 lowering 面负责，登记 = 残余 #R-78。
// 该用例的表达式身份改由 `TestParseTemplateValueIsExpressionOperand` 正锁。
func TestParseReturnTemplateSlotMarkerRejected(t *testing.T) {
	for _, value := range []string{
		`v`,
		`f()`,
		"`x${v}`",
	} {
		src := "@mode workflow\nfunction main(string v) {\n    return template " + value + ";\n}\n"
		if _, errs := ParseSource(src, "rt_nonlit.ncoda"); len(errs) == 0 {
			t.Errorf("模板标记 %s 应被拒绝（标记只认紧跟的字符串字面量）", value)
		}
	}
}

// `template "…"` 是**表达式** ⇒ 可以参与更大的表达式（锁语法身份，不锁承载面）：
// `template "a" + "b"` 的 AST = 二元 `+`，左操作数是 `*ast.TemplateValue`。
// 载体判定（该位点有没有承载面 ⇒ 该不该报错）在 lowering 面，见残余 #R-78。
func TestParseTemplateValueIsExpressionOperand(t *testing.T) {
	src := "@mode workflow\nfunction main(string v) -> string {\n    return template \"a\" + \"b\";\n}\n"
	prog, errs := ParseSource(src, "rt_operand.ncoda")
	if len(errs) > 0 {
		t.Fatalf("`template \"a\" + \"b\"` 应可解析（表达式位）：%v", errs)
	}
	be, ok := entryReturnValue(t, prog).(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("返回值位应为二元表达式，实得 %T", entryReturnValue(t, prog))
	}
	if _, isTpl := be.Left().(*ast.TemplateValue); !isTpl {
		t.Errorf("二元 `+` 的左操作数应为 *ast.TemplateValue，实得 %T", be.Left())
	}
}

// 缺 `;` / 缺 `}` 一律语法错（不静默吞下）。
func TestParseReturnTemplateSyntaxErrors(t *testing.T) {
	for _, src := range []string{
		"@mode workflow\nfunction main(string x) {\n    return template \"t\" using { a: x ;\n}\n",
		"@mode workflow\nfunction main(string x) {\n    return template \"t\";\n",
	} {
		if _, errs := ParseSource(src, "rt_syntax.ncoda"); len(errs) == 0 {
			t.Errorf("期望语法错：%q", src)
		}
	}
}

// ── 夹具助手 ─────────────────────────────────────────────────────────

// firstTemplateValue 取入口函数返回值位的 `*ast.TemplateValue`（失败即停）。
func firstTemplateValue(t *testing.T, prog *ast.Program) *ast.TemplateValue {
	t.Helper()
	rt, ok := entryReturnValue(t, prog).(*ast.TemplateValue)
	if !ok {
		t.Fatalf("入口函数的返回值位不是 TemplateValue，实得 %T", entryReturnValue(t, prog))
	}
	return rt
}

// entryReturnValue 取入口函数体里第一条 return 语句的值（nil = 无值返回）。
func entryReturnValue(t *testing.T, prog *ast.Program) ast.Expr {
	t.Helper()
	fn := prog.Entry()
	if fn == nil {
		t.Fatal("no entry function")
	}
	for _, s := range fn.Body().Stmts() {
		if r, ok := s.(*ast.ReturnStmt); ok {
			return r.Value()
		}
	}
	t.Fatal("入口函数体里没有 return 语句")
	return nil
}

// quoteForTest 把一个任意文本渲染成 ncoda 字符串字面量（与实现同一转义集合）。
func quoteForTest(s string) string {
	out := []rune{'"'}
	for _, r := range s {
		switch r {
		case '\\':
			out = append(out, '\\', '\\')
		case '"':
			out = append(out, '\\', '"')
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			out = append(out, r)
		}
	}
	out = append(out, '"')
	return string(out)
}
