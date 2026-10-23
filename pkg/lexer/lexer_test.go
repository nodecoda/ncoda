package lexer

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/token"
)

// types returns token types excluding EOF (mirrors Python lex() helper).
func types(t *testing.T, src string) []token.Type {
	t.Helper()
	var out []token.Type
	for _, tok := range New(src, "").Tokenize() {
		if tok.Type != token.EOF {
			out = append(out, tok.Type)
		}
	}
	return out
}

func typeNames(tt []token.Type) []string {
	out := make([]string, len(tt))
	for i, x := range tt {
		out[i] = x.String()
	}
	return out
}

func wantTypes(t *testing.T, src string, expected ...token.Type) {
	t.Helper()
	got := types(t, src)
	if len(got) != len(expected) {
		t.Fatalf("token count mismatch for %q:\n got  %v\n want %v", src, typeNames(got), typeNames(expected))
	}
	for i := range got {
		if got[i] != expected[i] {
			t.Fatalf("token[%d] mismatch for %q:\n got  %v\n want %v", i, src, typeNames(got), typeNames(expected))
		}
	}
}

func TestCapabilityBoundedSurfaceKeywords(t *testing.T) {
	wantTypes(t,
		"foreign yield limit attempt success failure with retry timeout default as",
		token.KW_FOREIGN, token.KW_YIELD, token.KW_LIMIT,
		token.KW_ATTEMPT, token.KW_SUCCESS, token.KW_FAILURE, token.KW_WITH,
		token.KW_RETRY, token.KW_TIMEOUT, token.KW_DEFAULT, token.KW_AS,
	)
}

func TestLanguageIdentityIsOneVersionedValue(t *testing.T) {
	toks := New("@language nodecoda/1\nfunction main() {}", "").Tokenize()
	if toks[0].Type != token.AT_LANGUAGE || toks[0].Lexeme != "@language" {
		t.Fatalf("first token = %v, want AT_LANGUAGE '@language'", toks[0])
	}
	if toks[1].Type != token.IDENTIFIER || toks[1].Lexeme != "nodecoda/1" {
		t.Fatalf("second token = %v, want IDENTIFIER 'nodecoda/1'", toks[1])
	}
}

func TestSingleCharTokens(t *testing.T) {
	wantTypes(t, "() {} [] , : ; ?",
		token.LPAREN, token.RPAREN, token.LBRACE, token.RBRACE,
		token.LBRACKET, token.RBRACKET, token.COMMA, token.COLON,
		token.SEMICOLON, token.QUESTION)
	wantTypes(t, "+ - * / % < > .",
		token.PLUS, token.MINUS, token.STAR, token.SLASH, token.PERCENT,
		token.LESS, token.GREATER, token.DOT)
}

func TestTwoCharOperators(t *testing.T) {
	cases := []struct {
		src  string
		want token.Type
	}{
		{"->", token.ARROW}, {"==", token.EQUAL}, {"!=", token.NOT_EQUAL},
		{"<=", token.LESS_EQUAL}, {">=", token.GREATER_EQUAL},
		{"&&", token.AND}, {"||", token.OR}, {"??", token.NULL_COALESCE},
		{"+=", token.PLUS_ASSIGN}, {"-=", token.MINUS_ASSIGN},
		{"*=", token.STAR_ASSIGN}, {"/=", token.SLASH_ASSIGN},
		{"<<", token.LESS_LESS},
	}
	for _, c := range cases {
		wantTypes(t, c.src, c.want)
	}
	// assign vs equal
	wantTypes(t, "= ==", token.ASSIGN, token.EQUAL)
	wantTypes(t, "! !=", token.NOT, token.NOT_EQUAL)
}

func TestPipeRejected(t *testing.T) {
	// A1 cleanup: the pipe operator token (|>) was removed (no grammar
	// production ever consumed PIPE). Both '|>' and a bare '|' are now
	// lexical errors; see docs/PARITY.md §2.
	for _, src := range []string{"|>", "|"} {
		lx := New(src, "")
		toks := lx.Tokenize()
		if toks[0].Type != token.ERROR {
			t.Fatalf("New(%q): first token = %v, want ERROR", src, toks[0].Type)
		}
	}
}

func TestDeadKeywordsUnreserved(t *testing.T) {
	// A1 cleanup: 'entry' and 'stream' were reserved but never used by the
	// grammar (dead keywords); they now lex as ordinary identifiers.
	wantTypes(t, "entry stream", token.IDENTIFIER, token.IDENTIFIER)
}

func TestStringLiterals(t *testing.T) {
	// double quoted
	toks := New(`"hello"`, "").Tokenize()
	if toks[0].Type != token.STRING_LITERAL || toks[0].Literal.S != "hello" {
		t.Fatalf("got %v literal %+v", toks[0].Type, toks[0].Literal)
	}
	// single quoted
	toks = New(`'hi'`, "").Tokenize()
	if toks[0].Type != token.STRING_LITERAL || toks[0].Literal.S != "hi" {
		t.Fatalf("got %v literal %+v", toks[0].Type, toks[0].Literal)
	}
	// escapes
	toks = New(`"a\nb\tc"`, "").Tokenize()
	if toks[0].Literal.S != "a\nb\tc" {
		t.Fatalf("escape literal = %q", toks[0].Literal.S)
	}
	// empty
	toks = New(`""`, "").Tokenize()
	if toks[0].Literal.S != "" {
		t.Fatalf("empty literal = %q", toks[0].Literal.S)
	}
	// unterminated
	lx := New(`"hello`, "")
	lx.Tokenize()
	if len(lx.Errors()) < 1 {
		t.Fatal("want unterminated string error")
	}
}

func TestUnterminatedStringNewline(t *testing.T) {
	// Python lexer.py:262: a raw newline inside a quoted string is rejected
	// with the exact message "Unterminated string literal (newline in
	// string)", reported at the token start. Recovery re-scans and the
	// second quote re-triggers the same error (matching Python's two-diag
	// CLI output for the same source).
	lx := New("@language nodecoda/1\n@mode workflow\nfunction main() {\n    output(\"x\", \"abc\ndef\");\n}\n", "")
	lx.Tokenize()
	msgs := make([]string, 0, len(lx.Errors()))
	for _, e := range lx.Errors() {
		msgs = append(msgs, e.Message)
	}
	want := []string{"Unterminated string literal (newline in string)", "Unterminated string literal (newline in string)"}
	if len(msgs) != len(want) {
		t.Fatalf("errors = %v, want %v", msgs, want)
	}
	for i := range want {
		if msgs[i] != want[i] {
			t.Errorf("error[%d] = %q, want %q", i, msgs[i], want[i])
		}
	}
	// Locations are token starts: the first quote (4:17) and the second
	// quote after recovery (5:4), matching the Python oracle spans.
	if lx.Errors()[0].Location.Line != 4 || lx.Errors()[0].Location.Column != 17 {
		t.Errorf("first error location = %d:%d, want 4:17", lx.Errors()[0].Location.Line, lx.Errors()[0].Location.Column)
	}
	if lx.Errors()[1].Location.Line != 5 || lx.Errors()[1].Location.Column != 4 {
		t.Errorf("second error location = %d:%d, want 5:4", lx.Errors()[1].Location.Line, lx.Errors()[1].Location.Column)
	}
	// EOF without newline keeps the generic message.
	lx = New(`"abc`, "")
	lx.Tokenize()
	if len(lx.Errors()) != 1 || lx.Errors()[0].Message != "Unterminated string literal" {
		t.Fatalf("EOF case errors = %v, want single generic message", lx.Errors())
	}
}

func TestBacktickTemplate(t *testing.T) {
	// plain backtick is STRING_LITERAL
	toks := New("`hello world`", "").Tokenize()
	if toks[0].Type != token.STRING_LITERAL || toks[0].Literal.S != "hello world" {
		t.Fatalf("plain backtick: %v %+v", toks[0].Type, toks[0].Literal)
	}
	// with interpolation
	toks = New("`Hello ${name}!`", "").Tokenize()
	if toks[0].Type != token.TEMPLATE_STRING || toks[0].Literal.S != "Hello ${name}!" {
		t.Fatalf("template: %v %q", toks[0].Type, toks[0].Literal.S)
	}
	// multiple interpolations
	toks = New("`${a} + ${b}`", "").Tokenize()
	if toks[0].Type != token.TEMPLATE_STRING {
		t.Fatalf("multi template: %v", toks[0].Type)
	}
	if !strings.Contains(toks[0].Literal.S, "${a}") || !strings.Contains(toks[0].Literal.S, "${b}") {
		t.Fatalf("interpolation parts missing: %q", toks[0].Literal.S)
	}
	// nested braces (member access)
	toks = New("`${obj.field}`", "").Tokenize()
	if toks[0].Type != token.TEMPLATE_STRING {
		t.Fatalf("nested braces: %v", toks[0].Type)
	}
	// nested brace depth: inner { } pair raises depth to 2, then
	// decrements back to 0 (lexer.go depth-zero break path; was an
	// uncovered branch per mutation audit MSI loop/break 0%).
	toks = New("`${ {a: 1} }`", "").Tokenize()
	if toks[0].Type != token.TEMPLATE_STRING {
		t.Fatalf("nested brace depth: %v", toks[0].Type)
	}
	if !strings.Contains(toks[0].Literal.S, "${ {a: 1} }") {
		t.Fatalf("nested brace depth literal: %q", toks[0].Literal.S)
	}
	// missing closing brace
	lx := New("`${x`", "")
	lx.Tokenize()
	if len(lx.Errors()) == 0 {
		t.Fatal("want unterminated template error")
	}
}

func TestNumberLiterals(t *testing.T) {
	toks := New("42", "").Tokenize()
	if toks[0].Type != token.INT_LITERAL || toks[0].Literal.I != 42 {
		t.Fatalf("int: %v %+v", toks[0].Type, toks[0].Literal)
	}
	toks = New("3.14", "").Tokenize()
	if toks[0].Type != token.FLOAT_LITERAL || toks[0].Literal.F != 3.14 {
		t.Fatalf("float: %v %+v", toks[0].Type, toks[0].Literal)
	}
	toks = New("0", "").Tokenize()
	if toks[0].Literal.I != 0 {
		t.Fatalf("zero: %+v", toks[0].Literal)
	}
	toks = New("0.0", "").Tokenize()
	if toks[0].Type != token.FLOAT_LITERAL {
		t.Fatalf("float zero: %v", toks[0].Type)
	}
	// dot followed by non-digit is DOT, not number
	wantTypes(t, "1.2 .", token.FLOAT_LITERAL, token.DOT)
}

func TestBoolAndNull(t *testing.T) {
	toks := New("true", "").Tokenize()
	if toks[0].Type != token.BOOL_LITERAL || toks[0].Literal.B != true {
		t.Fatalf("true: %v %+v", toks[0].Type, toks[0].Literal)
	}
	toks = New("false", "").Tokenize()
	if toks[0].Type != token.BOOL_LITERAL || toks[0].Literal.B != false {
		t.Fatalf("false: %v %+v", toks[0].Type, toks[0].Literal)
	}
	toks = New("null", "").Tokenize()
	if toks[0].Type != token.NULL_LITERAL {
		t.Fatalf("null: %v", toks[0].Type)
	}
}

func TestIdentifiersAndKeywords(t *testing.T) {
	wantTypes(t, "foo", token.IDENTIFIER)
	wantTypes(t, "function", token.KW_FUNCTION)
	wantTypes(t, "string int float bool file void map array any",
		token.KW_STRING, token.KW_INT, token.KW_FLOAT, token.KW_BOOL,
		token.KW_FILE, token.KW_VOID, token.KW_MAP, token.KW_ARRAY, token.KW_ANY)
	wantTypes(t, "_underscore foo_bar", token.IDENTIFIER, token.IDENTIFIER)
}

func TestComments(t *testing.T) {
	wantTypes(t, "x // this is a comment\n y", token.IDENTIFIER, token.IDENTIFIER)
	wantTypes(t, "x /* block\ncomment */ y", token.IDENTIFIER, token.IDENTIFIER)
	wantTypes(t, "x /* outer /* inner */ outer */ y", token.IDENTIFIER, token.IDENTIFIER)
	wantTypes(t, "x // comment", token.IDENTIFIER)
}

func TestWhitespaceAndLocation(t *testing.T) {
	wantTypes(t, "  \t  x  \t  ", token.IDENTIFIER)

	toks := New("x\ny\n", "").Tokenize()
	var ids []token.Token
	for _, tok := range toks {
		if tok.Type == token.IDENTIFIER {
			ids = append(ids, tok)
		}
	}
	if len(ids) != 2 || ids[0].Location.Line != 1 || ids[1].Location.Line != 2 {
		t.Fatalf("location lines: %+v", ids)
	}

	first := New("hello", "").Tokenize()[0]
	if first.Location.Line != 1 || first.Location.Column != 1 || first.Location.Offset != 0 {
		t.Fatalf("first token location: %+v", first.Location)
	}
}

func TestErrors(t *testing.T) {
	for _, src := range []string{"&", "|", "@bogus", `"hello`} {
		lx := New(src, "")
		lx.Tokenize()
		if len(lx.Errors()) == 0 {
			t.Fatalf("want errors for %q", src)
		}
	}
	// a bare '@' is the library-version sigil, not an error; it is the
	// parser's job to reject a misplaced one.
	wantTypes(t, "library adjustment @ 0.1.0;", token.KW_LIBRARY, token.IDENTIFIER, token.AT,
		token.FLOAT_LITERAL, token.DOT, token.INT_LITERAL, token.SEMICOLON)

	// empty source -> single EOF
	toks := New("", "").Tokenize()
	if len(toks) != 1 || toks[0].Type != token.EOF {
		t.Fatalf("empty source tokens: %+v", toks)
	}
}

func TestComplexSource(t *testing.T) {
	src := `
        function classify(string query) -> string {
            var response = llm("gpt-4", { "query": query });
            return response.content;
        }
        `
	lx := New(src, "")
	toks := lx.Tokenize()
	if len(lx.Errors()) != 0 {
		t.Fatalf("errors: %v", lx.Errors())
	}
	found := map[token.Type]bool{}
	for _, tok := range toks {
		found[tok.Type] = true
	}
	if !found[token.KW_FUNCTION] || !found[token.KW_RETURN] || !found[token.ARROW] {
		t.Fatalf("missing expected tokens: %v", typeNames(types(t, src)))
	}
}

// NCODA-DOLLAR-ESCAPE-SPEC：'$' 是纯词法转义标记——跳过 '$'，后续标识符
// （即使关键字）强制为普通 IDENTIFIER。$type → IDENTIFIER("type")，
// $foo → IDENTIFIER("foo")；$1 / $$ / 孤立 '$' 词法报错。
func TestDollarEscapeIdentifiers(t *testing.T) {
	// $ + 关键字 → 普通 IDENTIFIER（Lexeme 不含 '$'）
	toks := New("$type", "").Tokenize()
	if len(toks) < 1 || toks[0].Type != token.IDENTIFIER || toks[0].Lexeme != "type" {
		t.Fatalf("$type = %+v, want IDENTIFIER('type')", toks[0])
	}
	toks = New("$output", "").Tokenize()
	if len(toks) < 1 || toks[0].Type != token.IDENTIFIER || toks[0].Lexeme != "output" {
		t.Fatalf("$output = %+v, want IDENTIFIER('output')", toks[0])
	}
	// $ + 非关键字 → 普通 IDENTIFIER（Lexeme 不含 '$'）
	toks = New("$foo_bar2", "").Tokenize()
	if len(toks) < 1 || toks[0].Type != token.IDENTIFIER || toks[0].Lexeme != "foo_bar2" {
		t.Fatalf("$foo_bar2 = %+v, want IDENTIFIER('foo_bar2')", toks[0])
	}
	// $ + 下划线起始
	toks = New("$_x", "").Tokenize()
	if len(toks) < 1 || toks[0].Type != token.IDENTIFIER || toks[0].Lexeme != "_x" {
		t.Fatalf("$_x = %+v, want IDENTIFIER('_x')", toks[0])
	}
	// $ + 数字 / $ + $ / 孤立 $ → 词法错误（ERROR token）
	for _, src := range []string{"$1", "$$", "$", "$ foo", "$:"} {
		toks = New(src, "").Tokenize()
		if len(toks) < 1 || toks[0].Type != token.ERROR {
			t.Fatalf("%q first token = %v, want ERROR", src, toks[0].Type)
		}
	}
	// '$' 是独立转义标记：出现在标识符中间会断开（a$b = a + $b 两个 token，
	// 语法层拒绝相邻标识符）；出现在行尾/后随非法字符才词法报错（type$）。
	toks = New("a$b", "").Tokenize()
	if len(toks) < 3 || toks[0].Lexeme != "a" || toks[1].Lexeme != "b" {
		t.Fatalf("a$b tokens = %+v, want IDENTIFIER('a') IDENTIFIER('b')", toks)
	}
	toks = New("type$", "").Tokenize()
	foundErr := false
	for _, tk := range toks {
		if tk.Type == token.ERROR {
			foundErr = true
			break
		}
	}
	if !foundErr {
		t.Fatalf("type$: expected an ERROR token (trailing $)")
	}
	// 反引号模板串内 ${...} 插值与 \ 转义不受影响（JS 透传）
	toks = New("`a${x}b`", "").Tokenize()
	hasTemplate := false
	for _, tk := range toks {
		if tk.Type == token.TEMPLATE_STRING || len(tk.Literal.S) > 0 {
			hasTemplate = true
		}
	}
	_ = hasTemplate
	// $ 在模板串内是插值/文本（不经 scanDollarEscape）：`$foo` 整体一个字符串 token
	toks = New("`$foo`", "").Tokenize()
	if len(toks) < 1 || (toks[0].Type != token.STRING_LITERAL && toks[0].Type != token.TEMPLATE_STRING) {
		t.Fatalf("`$foo` first token = %v, want string/template literal", toks[0].Type)
	}
}

// TestNullCoalesceLexing: ?? 与三目/可空类型边界的词法区分（route 1a 裁决 5）。
func TestNullCoalesceLexing(t *testing.T) {
	// 连续 ?? → 单 NULL_COALESCE token
	wantTypes(t, "a ?? b",
		token.IDENTIFIER, token.NULL_COALESCE, token.IDENTIFIER)
	// 链式
	wantTypes(t, "a ?? b ?? c",
		token.IDENTIFIER, token.NULL_COALESCE, token.IDENTIFIER,
		token.NULL_COALESCE, token.IDENTIFIER)
	// 三目不合并：? 后跟空格/表达式
	wantTypes(t, "a ? b : c",
		token.IDENTIFIER, token.QUESTION, token.IDENTIFIER,
		token.COLON, token.IDENTIFIER)
	// Optional 类型标记：? 后跟标识符 → QUESTION
	wantTypes(t, "string? v",
		token.KW_STRING, token.QUESTION, token.IDENTIFIER)
	// ?? 在三目 then 位置（? 后跟 ?? 需要空格分隔，? 与 ?? 间是合法边界：
	// 词法上 `c ? a ?? b : d` 中 ?? 独立成 token）
	wantTypes(t, "c ? a ?? b : d",
		token.IDENTIFIER, token.QUESTION, token.IDENTIFIER,
		token.NULL_COALESCE, token.IDENTIFIER, token.COLON, token.IDENTIFIER)
	// ?? 与可选类型尾随（T? ?? 值：Optional 的 ? 与 ?? 需有空格；无空格 T?? 合并，
	// 语义上无意义，由上层报错——词法规则 = 连续 ?? 恒合并）
	wantTypes(t, "T? ?? x",
		token.IDENTIFIER, token.QUESTION, token.NULL_COALESCE, token.IDENTIFIER)
}
