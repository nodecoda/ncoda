// Task 5 tests: the canonical expression production parser (parseExpr family),
// ported from tests/lang/unit/test_parser.py (TestExpressions +
// TestTemplateStringParsing) plus Task 5-owned span-parity, deep-copy, and
// template-fidelity assertions that lock the semantics fixed in task-5-report.
//@feature lambda — lambda 表达式语法（filter/take/sort 谓词形态，parser expr_test.go lambda 段）

package parser

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
)

// initExpr returns the initializer expression of the first statement (a var
// declaration) in the entry function body, failing the test otherwise.
func initExpr(t *testing.T, src string) ast.Expr {
	t.Helper()
	stmts := entryBody(t, src)
	vd, ok := stmts[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("stmts[0] = %T, want *ast.VarDeclStmt", stmts[0])
	}
	return vd.Init()
}

// hasSpan reports whether a node carries a non-zero source span (the Go
// equivalent of Python's span is not None).
func hasSpan(n ast.Node) bool {
	s := n.Span()
	return s.Start.Line != 0 || s.Start.Column != 0 || s.End.Line != 0 || s.End.Column != 0
}

// ── literals / identifiers ──

func TestExprIntLiteral(t *testing.T) {
	// Port of test_int_literal.
	init := initExpr(t, "function main(string query) { var x = 42; }")
	lit, ok := init.(*ast.IntLit)
	if !ok {
		t.Fatalf("init = %T, want *ast.IntLit", init)
	}
	if lit.Value() != 42 {
		t.Errorf("Value() = %d, want 42", lit.Value())
	}
	if !hasSpan(lit) {
		t.Error("literal carries no span (Python sets one)")
	}
}

func TestExprFloatLiteral(t *testing.T) {
	init := initExpr(t, "function main(string query) { var x = 3.14; }")
	lit, ok := init.(*ast.FloatLit)
	if !ok {
		t.Fatalf("init = %T, want *ast.FloatLit", init)
	}
	if lit.Value() != 3.14 {
		t.Errorf("Value() = %v, want 3.14", lit.Value())
	}
}

func TestExprStringLiteral(t *testing.T) {
	init := initExpr(t, `function main(string query) { var x = "hello"; }`)
	lit, ok := init.(*ast.StringLit)
	if !ok {
		t.Fatalf("init = %T, want *ast.StringLit", init)
	}
	if lit.Value() != "hello" {
		t.Errorf("Value() = %q, want hello", lit.Value())
	}
}

func TestExprBoolLiteral(t *testing.T) {
	stmts := entryBody(t, "function main(string query) { var x = true; var y = false; }")
	b1, ok := stmts[0].(*ast.VarDeclStmt).Init().(*ast.BoolLit)
	if !ok || b1.Value() != true {
		t.Errorf("x init = %T %v, want BoolLit true", stmts[0].(*ast.VarDeclStmt).Init(), stmts[0].(*ast.VarDeclStmt).Init())
	}
	b2, ok := stmts[1].(*ast.VarDeclStmt).Init().(*ast.BoolLit)
	if !ok || b2.Value() != false {
		t.Errorf("y init = %T %v, want BoolLit false", stmts[1].(*ast.VarDeclStmt).Init(), stmts[1].(*ast.VarDeclStmt).Init())
	}
}

func TestExprNullLiteral(t *testing.T) {
	init := initExpr(t, "function main(string query) { var x = null; }")
	if _, ok := init.(*ast.NullLit); !ok {
		t.Fatalf("init = %T, want *ast.NullLit", init)
	}
}

func TestExprIdentifier(t *testing.T) {
	init := initExpr(t, "function main(string query) { var x = y; }")
	ident, ok := init.(*ast.IdentExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.IdentExpr", init)
	}
	if ident.Name() != "y" {
		t.Errorf("Name() = %q, want y", ident.Name())
	}
}

// ── array / map literals ──

func TestExprArrayLiteral(t *testing.T) {
	init := initExpr(t, "function main(string query) { var x = [1, 2, 3]; }")
	arr, ok := init.(*ast.ArrayLit)
	if !ok {
		t.Fatalf("init = %T, want *ast.ArrayLit", init)
	}
	if len(arr.Elements()) != 3 {
		t.Errorf("len(Elements()) = %d, want 3", len(arr.Elements()))
	}
}

func TestExprMapLiteral(t *testing.T) {
	init := initExpr(t, `function main(string query) { var x = { "key": 42 }; }`)
	m, ok := init.(*ast.MapLit)
	if !ok {
		t.Fatalf("init = %T, want *ast.MapLit", init)
	}
	if len(m.Entries()) != 1 {
		t.Errorf("len(Entries()) = %d, want 1", len(m.Entries()))
	}
	if _, ok := m.Entries()[0].Key.(*ast.StringLit); !ok {
		t.Errorf("key = %T, want *ast.StringLit", m.Entries()[0].Key)
	}
	if _, ok := m.Entries()[0].Value.(*ast.IntLit); !ok {
		t.Errorf("value = %T, want *ast.IntLit", m.Entries()[0].Value)
	}
}

// ── access / calls ──

func TestExprFieldAccess(t *testing.T) {
	init := initExpr(t, "function main(string query) { var x = obj.field; }")
	fa, ok := init.(*ast.FieldAccessExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.FieldAccessExpr", init)
	}
	if fa.FieldName() != "field" {
		t.Errorf("FieldName() = %q, want field", fa.FieldName())
	}
}

func TestExprIndexAccess(t *testing.T) {
	init := initExpr(t, "function main(string query) { var x = arr[0]; }")
	if _, ok := init.(*ast.IndexExpr); !ok {
		t.Fatalf("init = %T, want *ast.IndexExpr", init)
	}
}

func TestExprFunctionCall(t *testing.T) {
	init := initExpr(t, "function main(string query) { var x = foo(1, 2); }")
	call, ok := init.(*ast.CallExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.CallExpr", init)
	}
	if len(call.Args()) != 2 {
		t.Errorf("len(Args()) = %d, want 2", len(call.Args()))
	}
}

func TestExprMethodCall(t *testing.T) {
	init := initExpr(t, "function main(string query) { var x = obj.method(1); }")
	call, ok := init.(*ast.CallExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.CallExpr", init)
	}
	if _, ok := call.Func().(*ast.FieldAccessExpr); !ok {
		t.Errorf("Func() = %T, want *ast.FieldAccessExpr", call.Func())
	}
}

func TestExprTypeArgumentCallParsesWithoutBreakingLessThan(t *testing.T) {
	// Port of test_type_argument_call_parses_without_breaking_less_than:
	// extract<Travel>(...) parses type args; result.value.city < text is
	// still a comparison (the speculative <T> parse must not steal '<').
	prog := parseOK(t, `
		type Travel = { string city; }
		function main(string text) {
			let result = extract<Travel>("provider/model", text, {});
			let compared = result.value.city < text;
		}`)
	stmts := prog.Entry().Body().Stmts()
	call, ok := stmts[0].(*ast.VarDeclStmt).Init().(*ast.CallExpr)
	if !ok {
		t.Fatalf("init[0] = %T, want *ast.CallExpr", stmts[0].(*ast.VarDeclStmt).Init())
	}
	if len(call.TypeArgs()) != 1 {
		t.Fatalf("len(TypeArgs()) = %d, want 1", len(call.TypeArgs()))
	}
	if named, ok := call.TypeArgs()[0].(*ast.NamedTypeRef); !ok || named.Name() != "Travel" {
		t.Errorf("TypeArgs()[0] = %T %v, want NamedTypeRef Travel", call.TypeArgs()[0], call.TypeArgs()[0])
	}
	compared, ok := stmts[1].(*ast.VarDeclStmt).Init().(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("init[1] = %T, want *ast.BinaryExpr", stmts[1].(*ast.VarDeclStmt).Init())
	}
	if compared.Op() != "<" {
		t.Errorf("Op() = %q, want <", compared.Op())
	}
}

// ── binary / unary / ternary ──

func TestExprBinaryPlus(t *testing.T) {
	init := initExpr(t, "function main(string query) { var x = 1 + 2; }")
	b, ok := init.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.BinaryExpr", init)
	}
	if b.Op() != "+" {
		t.Errorf("Op() = %q, want +", b.Op())
	}
}

func TestExprBinaryPrecedence(t *testing.T) {
	// Port of test_binary_precedence: 1 + 2 * 3 -> +(1, *(2, 3)).
	init := initExpr(t, "function main(string query) { var x = 1 + 2 * 3; }")
	b, ok := init.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.BinaryExpr", init)
	}
	if b.Op() != "+" {
		t.Fatalf("Op() = %q, want +", b.Op())
	}
	r, ok := b.Right().(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("Right() = %T, want *ast.BinaryExpr", b.Right())
	}
	if r.Op() != "*" {
		t.Errorf("Right().Op() = %q, want *", r.Op())
	}
}

func TestExprBinaryEquality(t *testing.T) {
	init := initExpr(t, "function main(string query) { var x = a == b; }")
	b, ok := init.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.BinaryExpr", init)
	}
	if b.Op() != "==" {
		t.Errorf("Op() = %q, want ==", b.Op())
	}
}

func TestExprLogicalOperators(t *testing.T) {
	// Port of test_logical_operators: true && false || true -> || at root.
	init := initExpr(t, "function main(string query) { var x = true && false || true; }")
	b, ok := init.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.BinaryExpr", init)
	}
	if b.Op() != "||" {
		t.Errorf("Op() = %q, want || (lowest precedence)", b.Op())
	}
}

func TestExprUnaryNot(t *testing.T) {
	init := initExpr(t, "function main(string query) { var x = !true; }")
	u, ok := init.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.UnaryExpr", init)
	}
	if u.Op() != "!" {
		t.Errorf("Op() = %q, want !", u.Op())
	}
}

func TestExprUnaryMinus(t *testing.T) {
	init := initExpr(t, "function main(string query) { var x = -42; }")
	u, ok := init.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.UnaryExpr", init)
	}
	if u.Op() != "-" {
		t.Errorf("Op() = %q, want -", u.Op())
	}
}

func TestExprTernary(t *testing.T) {
	init := initExpr(t, "function main(string query) { var x = true ? 1 : 2; }")
	if _, ok := init.(*ast.TernaryExpr); !ok {
		t.Fatalf("init = %T, want *ast.TernaryExpr", init)
	}
}

func TestExprGroupedExpression(t *testing.T) {
	// Port of test_grouped_expression: (1 + 2) * 3 -> *(+(1,2), 3).
	init := initExpr(t, "function main(string query) { var x = (1 + 2) * 3; }")
	b, ok := init.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.BinaryExpr", init)
	}
	if b.Op() != "*" {
		t.Fatalf("Op() = %q, want *", b.Op())
	}
	l, ok := b.Left().(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("Left() = %T, want *ast.BinaryExpr", b.Left())
	}
	if l.Op() != "+" {
		t.Errorf("Left().Op() = %q, want +", l.Op())
	}
}

func TestExprChainedFieldAccess(t *testing.T) {
	init := initExpr(t, "function main(string query) { var x = a.b.c; }")
	fa, ok := init.(*ast.FieldAccessExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.FieldAccessExpr", init)
	}
	if _, ok := fa.Obj().(*ast.FieldAccessExpr); !ok {
		t.Errorf("Obj() = %T, want *ast.FieldAccessExpr", fa.Obj())
	}
}

// ── operation policies ──

func TestExprOperationPolicy(t *testing.T) {
	// retry + timeout policies attached via `with` (parser.py:_parse_postfix).
	init := initExpr(t, "function main() { let r = call() with retry(max: 2, interval: 100 ms), timeout(5 s); }")
	pe, ok := init.(*ast.OperationPolicyExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.OperationPolicyExpr", init)
	}
	if _, ok := pe.Operation().(*ast.CallExpr); !ok {
		t.Errorf("Operation() = %T, want *ast.CallExpr", pe.Operation())
	}
	if len(pe.Policies()) != 2 {
		t.Fatalf("len(Policies()) = %d, want 2", len(pe.Policies()))
	}
	retry, ok := pe.Policies()[0].(*ast.RetryPolicy)
	if !ok {
		t.Fatalf("Policies()[0] = %T, want *ast.RetryPolicy", pe.Policies()[0])
	}
	if retry.MaxRetries() != 2 || retry.IntervalMs() != 100 {
		t.Errorf("RetryPolicy = %d/%d, want 2/100", retry.MaxRetries(), retry.IntervalMs())
	}
	timeout, ok := pe.Policies()[1].(*ast.TimeoutPolicy)
	if !ok {
		t.Fatalf("Policies()[1] = %T, want *ast.TimeoutPolicy", pe.Policies()[1])
	}
	if timeout.TimeoutMs() != 5000 {
		t.Errorf("TimeoutMs() = %d, want 5000 (5 s)", timeout.TimeoutMs())
	}
}

func TestExprOperationPolicyDefault(t *testing.T) {
	init := initExpr(t, "function main() { let r = lookup(key) with default(null); }")
	pe, ok := init.(*ast.OperationPolicyExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.OperationPolicyExpr", init)
	}
	d, ok := pe.Policies()[0].(*ast.DefaultPolicy)
	if !ok {
		t.Fatalf("Policies()[0] = %T, want *ast.DefaultPolicy", pe.Policies()[0])
	}
	if _, ok := d.Value().(*ast.NullLit); !ok {
		t.Errorf("Value() = %T, want *ast.NullLit", d.Value())
	}
}

// ── lambda ──

func TestExprLambda(t *testing.T) {
	init := initExpr(t, "function main() { let f = (a, b) -> a + b; }")
	lam, ok := init.(*ast.LambdaExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.LambdaExpr", init)
	}
	if len(lam.Params()) != 2 || lam.Params()[0] != "a" || lam.Params()[1] != "b" {
		t.Errorf("Params() = %v, want [a b]", lam.Params())
	}
	if _, ok := lam.Body().(*ast.BinaryExpr); !ok {
		t.Errorf("Body() = %T, want *ast.BinaryExpr", lam.Body())
	}
}

func TestExprLambdaInCallArgument(t *testing.T) {
	init := initExpr(t, "function main(string[][] groups) { let values = flatMap(groups, (group) -> group); }")
	call, ok := init.(*ast.CallExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.CallExpr", init)
	}
	if _, ok := call.Args()[1].(*ast.LambdaExpr); !ok {
		t.Errorf("Args()[1] = %T, want *ast.LambdaExpr", call.Args()[1])
	}
}

// ── template strings ──

func TestTemplateSimpleInterpolation(t *testing.T) {
	// Port of test_simple_interpolation: `Hello ${name}` ->
	// parts ["Hello ", IdentExpr(name), ""].
	init := initExpr(t, "function main(string query) { var x = `Hello ${name}`; }")
	ts, ok := init.(*ast.TemplateString)
	if !ok {
		t.Fatalf("init = %T, want *ast.TemplateString", init)
	}
	parts := ts.Parts()
	if len(parts) != 3 {
		t.Fatalf("len(Parts()) = %d, want 3", len(parts))
	}
	if parts[0].Text != "Hello " || parts[0].Expr != nil {
		t.Errorf("Parts()[0] = %+v, want Text %q", parts[0], "Hello ")
	}
	if ident, ok := parts[1].Expr.(*ast.IdentExpr); !ok || ident.Name() != "name" {
		t.Errorf("Parts()[1].Expr = %T, want *ast.IdentExpr name", parts[1].Expr)
	}
	if parts[2].Text != "" || parts[2].Expr != nil {
		t.Errorf("Parts()[2] = %+v, want trailing empty Text", parts[2])
	}
}

func TestTemplateMultipleInterpolations(t *testing.T) {
	// Port of test_multiple_interpolations: `${a} + ${b}` ->
	// 5 parts ["", a, " + ", b, ""].
	init := initExpr(t, "function main(string query) { var x = `${a} + ${b}`; }")
	ts, ok := init.(*ast.TemplateString)
	if !ok {
		t.Fatalf("init = %T, want *ast.TemplateString", init)
	}
	parts := ts.Parts()
	if len(parts) != 5 {
		t.Fatalf("len(Parts()) = %d, want 5", len(parts))
	}
	if parts[0].Text != "" || parts[1].Expr == nil || parts[2].Text != " + " || parts[3].Expr == nil || parts[4].Text != "" {
		t.Errorf("parts = %+v, want ['', expr, \" + \", expr, '']", parts)
	}
}

func TestTemplateExpressionInInterpolation(t *testing.T) {
	// Port of test_expression_in_interpolation: parts[1] is the BinaryExpr.
	init := initExpr(t, "function main(string query) { var x = `result: ${a + b}`; }")
	ts, ok := init.(*ast.TemplateString)
	if !ok {
		t.Fatalf("init = %T, want *ast.TemplateString", init)
	}
	b, ok := ts.Parts()[1].Expr.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("Parts()[1].Expr = %T, want *ast.BinaryExpr", ts.Parts()[1].Expr)
	}
	if b.Op() != "+" {
		t.Errorf("Op() = %q, want +", b.Op())
	}
}

func TestTemplatePlainBacktickIsStringLit(t *testing.T) {
	// Port of test_plain_backtick_is_string_lit: `hello` (no ${) is a plain
	// StringLit (the lexer emits STRING_LITERAL without interpolation).
	init := initExpr(t, "function main(string query) { var x = `hello`; }")
	lit, ok := init.(*ast.StringLit)
	if !ok {
		t.Fatalf("init = %T, want *ast.StringLit", init)
	}
	if lit.Value() != "hello" {
		t.Errorf("Value() = %q, want hello", lit.Value())
	}
}

func TestTemplateIncompleteExpressionErrors(t *testing.T) {
	// `${1+}`: the sub-parser hits EOF mid-expression, records the error and
	// falls back to an empty StringLit part (parser.py:_parse_template_expr).
	prog, errs := New(lex("function main(string query) { var x = `${1+}`; }")).Parse()
	if len(errs) != 1 {
		t.Fatalf("len(errs) = %d, want 1", len(errs))
	}
	if !strings.Contains(errs[0].Message, "Expected expression, got EOF") {
		t.Errorf("Message = %q, want the sub-parser EOF expression error", errs[0].Message)
	}
	ts, ok := prog.Entry().Body().Stmts()[0].(*ast.VarDeclStmt).Init().(*ast.TemplateString)
	if !ok {
		t.Fatalf("init = %T, want *ast.TemplateString", prog.Entry().Body().Stmts()[0].(*ast.VarDeclStmt).Init())
	}
	if lit, ok := ts.Parts()[1].Expr.(*ast.StringLit); !ok || lit.Value() != "" {
		t.Errorf("Parts()[1].Expr = %T, want empty *ast.StringLit fallback", ts.Parts()[1].Expr)
	}
}

func TestTemplateTrailingTokensMatchPython(t *testing.T) {
	// Oracle parity lock (verified against parser.py): `${1 2}` — the
	// sub-parser parses the prefix `1` and Python does NOT re-check the
	// cursor, so the trailing `2` is silently dropped and no error is
	// recorded. parts = ["", IntLit(1), ""].
	prog, errs := New(lex("function main(string query) { var x = `${1 2}`; }")).Parse()
	if len(errs) != 0 {
		t.Fatalf("len(errs) = %d, want 0 (Python silently truncates)", len(errs))
	}
	ts, ok := prog.Entry().Body().Stmts()[0].(*ast.VarDeclStmt).Init().(*ast.TemplateString)
	if !ok {
		t.Fatalf("init = %T, want *ast.TemplateString", prog.Entry().Body().Stmts()[0].(*ast.VarDeclStmt).Init())
	}
	parts := ts.Parts()
	if len(parts) != 3 {
		t.Fatalf("len(Parts()) = %d, want 3", len(parts))
	}
	if parts[0].Text != "" || parts[2].Text != "" {
		t.Errorf("parts[0]/[2] = %+v/%+v, want empty strings", parts[0], parts[2])
	}
	if lit, ok := parts[1].Expr.(*ast.IntLit); !ok || lit.Value() != 1 {
		t.Errorf("Parts()[1].Expr = %T, want IntLit 1", parts[1].Expr)
	}
}

// ── Task 5 span parity (Python: span only on ||/&& binary + ternary) ──

func TestSpanArithmeticBinaryNone(t *testing.T) {
	// `1 + 2 * 3`: arithmetic BinaryExpr nodes carry NO span (Python leaves
	// them None); literals keep their spans.
	init := initExpr(t, "function main(string query) { var x = 1 + 2 * 3; }")
	b, ok := init.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.BinaryExpr", init)
	}
	if hasSpan(b) {
		t.Errorf("arithmetic BinaryExpr(+) has a span; Python leaves it None")
	}
	r, ok := b.Right().(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("Right() = %T, want *ast.BinaryExpr", b.Right())
	}
	if hasSpan(r) {
		t.Errorf("arithmetic BinaryExpr(*) has a span; Python leaves it None")
	}
	if !hasSpan(b.Left()) || !hasSpan(r.Left()) || !hasSpan(r.Right()) {
		t.Error("literal operands must keep their spans")
	}
}

func TestSpanComparisonEqualityNone(t *testing.T) {
	// `1 < 2 == true`: comparison and equality BinaryExpr nodes have NO span.
	init := initExpr(t, "function main(string query) { var x = 1 < 2 == true; }")
	b, ok := init.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.BinaryExpr", init)
	}
	if b.Op() != "==" || hasSpan(b) {
		t.Errorf("equality node op=%q span=%v, want == with no span", b.Op(), hasSpan(b))
	}
	l, ok := b.Left().(*ast.BinaryExpr)
	if !ok || l.Op() != "<" || hasSpan(l) {
		t.Errorf("left = %T op=%q span=%v, want < comparison with no span", b.Left(), opOf(l), hasSpan(l))
	}
}

func TestSpanLogicalBinarySet(t *testing.T) {
	// `a || b && c`: || and && nodes DO carry spans (Python sets them).
	init := initExpr(t, "function main(string query) { var x = a || b && c; }")
	b, ok := init.(*ast.BinaryExpr)
	if !ok || b.Op() != "||" {
		t.Fatalf("init = %T op=%q, want *ast.BinaryExpr ||", init, opOf(init))
	}
	if !hasSpan(b) {
		t.Error("|| node has no span; Python sets one")
	}
	r, ok := b.Right().(*ast.BinaryExpr)
	if !ok || r.Op() != "&&" {
		t.Fatalf("Right() = %T op=%q, want *ast.BinaryExpr &&", b.Right(), opOf(b.Right()))
	}
	if !hasSpan(r) {
		t.Error("&& node has no span; Python sets one")
	}
}

func TestSpanTernarySet(t *testing.T) {
	init := initExpr(t, "function main(string query) { var x = true ? 1 : 2; }")
	tn, ok := init.(*ast.TernaryExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.TernaryExpr", init)
	}
	if !hasSpan(tn) {
		t.Error("ternary node has no span; Python sets one")
	}
}

func TestSpanUnaryCopiesOperand(t *testing.T) {
	// `-42`: unary span copies the operand's span (set). `-(1+2)`: operand
	// is a grouped arithmetic BinaryExpr with no span, so the unary span is
	// also None (Python: u.span = operand.span).
	u := initExpr(t, "function main(string query) { var x = -42; }").(*ast.UnaryExpr)
	if u.Op() != "-" || !hasSpan(u) {
		t.Errorf("unary -42 op=%q span=%v, want - with span", u.Op(), hasSpan(u))
	}
	u2, ok := initExpr(t, "function main(string query) { var x = -(1 + 2); }").(*ast.UnaryExpr)
	if !ok {
		t.Fatal("-(1 + 2) did not parse as UnaryExpr")
	}
	if u2.Op() != "-" || hasSpan(u2) {
		t.Errorf("unary -(1+2) op=%q span=%v, want - with NO span (operand has none)", u2.Op(), hasSpan(u2))
	}
}

func TestSpanLambdaFallsBackToStart(t *testing.T) {
	// `(a) -> a + 1`: the body is an arithmetic BinaryExpr with no span, so
	// Python sets the lambda span to the '(' location on both ends
	// (body.span.end if body.span else start.location).
	init := initExpr(t, "function main() { let f = (a) -> a + 1; }")
	lam, ok := init.(*ast.LambdaExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.LambdaExpr", init)
	}
	s := lam.Span()
	if s.Start.Line != 1 || s.Start.Column != 27 {
		t.Errorf("lambda span start = %d:%d, want 1:27 (the '(')", s.Start.Line, s.Start.Column)
	}
	if s.Start != s.End {
		t.Errorf("lambda span end = %+v, want == start %+v (body has no span)", s.End, s.Start)
	}
}

func TestSpanCompoundDesugaredValueNone(t *testing.T) {
	// `x += 2` desugars to AssignStmt(value=BinaryExpr("+", deepcopy(x), 2));
	// the desugared BinaryExpr carries NO span (Python), while the deep-copied
	// target retains the original span (copy.deepcopy preserves attributes).
	stmts := entryBody(t, "function main() { var x = 1; x += 2; }")
	s := stmts[1].(*ast.AssignStmt)
	v, ok := s.Value().(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("Value() = %T, want *ast.BinaryExpr", s.Value())
	}
	if hasSpan(v) {
		t.Error("desugared BinaryExpr has a span; Python leaves it None")
	}
	left, ok := v.Left().(*ast.IdentExpr)
	if !ok {
		t.Fatalf("Value().Left() = %T, want *ast.IdentExpr", v.Left())
	}
	if left.Name() != "x" || !hasSpan(left) {
		t.Errorf("deep-copied target = %q span=%v, want x with span preserved", left.Name(), hasSpan(left))
	}
	if left.Span() != s.Target().Span() {
		t.Errorf("deep-copied target span %+v != target span %+v", left.Span(), s.Target().Span())
	}
}

// ── Task 5 deep copy: no shared NodeID in compound desugaring ──

func TestCompoundAssignDeepCopyNoSharedNode(t *testing.T) {
	// `f(x).y += 1`: Python copy.deepcopy builds a full independent copy of
	// the target for the desugared value; Go must not reuse the target node
	// (which would create a DAG and break NodeID-keyed walkers).
	stmts := entryBody(t, "function main() { f(x).y += 1; }")
	s := stmts[0].(*ast.AssignStmt)
	tgt, ok := s.Target().(*ast.FieldAccessExpr)
	if !ok {
		t.Fatalf("Target() = %T, want *ast.FieldAccessExpr", s.Target())
	}
	v, ok := s.Value().(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("Value() = %T, want *ast.BinaryExpr", s.Value())
	}
	copy_, ok := v.Left().(*ast.FieldAccessExpr)
	if !ok {
		t.Fatalf("Value().Left() = %T, want *ast.FieldAccessExpr", v.Left())
	}
	if copy_ == tgt {
		t.Fatal("desugared left reuses the target node (shared NodeID / DAG)")
	}
	if copy_.NodeID() == tgt.NodeID() {
		t.Fatal("desugared left shares the target NodeID")
	}
	// The nested CallExpr below the field access must also be a fresh copy.
	tc, ok := tgt.Obj().(*ast.CallExpr)
	if !ok {
		t.Fatalf("Target().Obj() = %T, want *ast.CallExpr", tgt.Obj())
	}
	cc, ok := copy_.Obj().(*ast.CallExpr)
	if !ok {
		t.Fatalf("copy.Obj() = %T, want *ast.CallExpr", copy_.Obj())
	}
	if cc.NodeID() == tc.NodeID() {
		t.Error("nested CallExpr shares the target NodeID (incomplete deep copy)")
	}
	if cc.Args()[0].NodeID() == tc.Args()[0].NodeID() {
		t.Error("call argument node shared across the copy (incomplete deep copy)")
	}
	if !hasSpan(copy_) || copy_.Span() != tgt.Span() {
		t.Errorf("deep copy span %+v, want %+v (preserved like copy.deepcopy)", copy_.Span(), tgt.Span())
	}
}

// opOf returns the operator string of a BinaryExpr or "" for other nodes.
func opOf(e ast.Expr) string {
	if b, ok := e.(*ast.BinaryExpr); ok {
		return b.Op()
	}
	return ""
}

func TestSpanConstValueLiteralNone(t *testing.T) {
	// Python's _parse_const_value builds plain literals without a span (only
	// const template strings keep theirs); Go must mirror that so const
	// literal spans stay zero (decl.go parseConstValue).
	prog := parseOK(t, `const X = 42;
const NAME = "hi";
const FLAG = true;
const PI = 3.14;
const NOTHING = null;
const GREETING = `+"`"+`Hi ${NAME}`+"`"+`;
function main() { return 0; }`)
	decls := prog.ConstDecls()
	want := []struct {
		val  ast.Expr
		name string
	}{{decls[0].Value(), "X"}, {decls[1].Value(), "NAME"}, {decls[2].Value(), "FLAG"}, {decls[3].Value(), "PI"}, {decls[4].Value(), "NOTHING"}}
	for _, w := range want {
		if hasSpan(w.val) {
			t.Errorf("const %s value %T carries a span; Python leaves it None", w.name, w.val)
		}
	}
	// The const template string keeps its span (Python: _parse_template_string).
	ts, ok := decls[5].Value().(*ast.TemplateString)
	if !ok {
		t.Fatalf("GREETING value = %T, want *ast.TemplateString", decls[5].Value())
	}
	if !hasSpan(ts) {
		t.Error("const template string lost its span; Python sets it")
	}
}
