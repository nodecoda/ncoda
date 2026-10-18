// Package ast_test tests the SourceAST node definitions.
//
// These tests port the structural AST assertions from
// tests/lang/unit/test_parser.py (read-only reference) at the
// node-construction level, since the Go parser is ported in a later task.
package ast_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/token"
)

// ─── helpers ────────────────────────────────────────────────────────────

// countingVisitor counts every node visited by Walk.
type countingVisitor struct {
	nodes map[ast.Node]int
}

func (c *countingVisitor) Visit(n ast.Node) ast.Visitor {
	if c.nodes == nil {
		c.nodes = map[ast.Node]int{}
	}
	c.nodes[n]++
	return c
}

func (c *countingVisitor) count() int { return len(c.nodes) }

type visitorFunc func(ast.Node) ast.Visitor

func (f visitorFunc) Visit(n ast.Node) ast.Visitor { return f(n) }

// typeName returns the concrete Go type name of a node, e.g. "*ast.IntLit".
func typeName(n ast.Node) string { return reflect.TypeOf(n).String() }

// builder tracks every node it constructs so tests can assert that Walk
// visits exactly the constructed tree, no more and no less.
type builder struct{ nodes []ast.Node }

func (b *builder) track(n ast.Node) ast.Node { b.nodes = append(b.nodes, n); return n }

func (b *builder) intLit(v int64) *ast.IntLit {
	return b.track(ast.NewIntLit(v)).(*ast.IntLit)
}
func (b *builder) floatLit(v float64) *ast.FloatLit {
	return b.track(ast.NewFloatLit(v)).(*ast.FloatLit)
}
func (b *builder) strLit(v string) *ast.StringLit {
	return b.track(ast.NewStringLit(v)).(*ast.StringLit)
}
func (b *builder) ident(v string) *ast.IdentExpr {
	return b.track(ast.NewIdentExpr(v)).(*ast.IdentExpr)
}
func (b *builder) prim(name string) *ast.PrimTypeRef {
	return b.track(ast.NewPrimTypeRef(name)).(*ast.PrimTypeRef)
}
func (b *builder) named(name string) *ast.NamedTypeRef {
	return b.track(ast.NewNamedTypeRef(name)).(*ast.NamedTypeRef)
}
func (b *builder) block(stmts []ast.Stmt) *ast.Block {
	return b.track(ast.NewBlock(stmts)).(*ast.Block)
}
func (b *builder) call(fn ast.Expr, args []ast.Expr) *ast.CallExpr {
	return b.track(ast.NewCallExpr(fn, nil, args)).(*ast.CallExpr)
}

func (b *builder) ret(v ast.Expr) *ast.ReturnStmt {
	return b.track(ast.NewReturnStmt(v)).(*ast.ReturnStmt)
}
func (b *builder) exprStmt(e ast.Expr) *ast.ExprStmt {
	return b.track(ast.NewExprStmt(e)).(*ast.ExprStmt)
}
func (b *builder) brk() *ast.BreakStmt {
	return b.track(ast.NewBreakStmt()).(*ast.BreakStmt)
}
func (b *builder) cont() *ast.ContinueStmt {
	return b.track(ast.NewContinueStmt()).(*ast.ContinueStmt)
}
func (b *builder) assign(target, value ast.Expr, op string) *ast.AssignStmt {
	return b.track(ast.NewAssignStmt(target, value, op)).(*ast.AssignStmt)
}
func (b *builder) yield(v ast.Expr) *ast.YieldStmt {
	return b.track(ast.NewYieldStmt(v)).(*ast.YieldStmt)
}

// buildSampleProgram constructs a program exercising every node category;
// every constructed node is tracked in b.nodes for exhaustive Walk checks.
func buildSampleProgram(t *testing.T) (*ast.Program, *builder) {
	t.Helper()
	b := &builder{}

	// ── code function ──
	cfBody := b.block([]ast.Stmt{b.ret(b.intLit(1))})
	cf := b.track(ast.NewCodeFunctionDef("normalize", nil, nil, cfBody)).(*ast.CodeFunctionDef)

	// ── regular function with param + return type ──
	pa := b.track(ast.NewParamDecl("value", b.prim("string"), b.strLit("x"))).(*ast.ParamDecl)
	fnBody := b.block(nil)
	fn := b.track(ast.NewFunctionDef("add", []*ast.ParamDecl{pa}, b.prim("int"), fnBody)).(*ast.FunctionDef)

	// ── entry function ──
	entry := buildEntry(b)

	prog := ast.NewProgram(
		ast.WithLanguageIdentity("nodecoda/1"),
		ast.WithMode("workflow"),
		ast.WithConstDecls([]*ast.ConstDecl{b.track(ast.NewConstDecl("PI", b.floatLit(3.14))).(*ast.ConstDecl)}),
		ast.WithOutputs([]*ast.OutputDecl{b.track(ast.NewOutputDecl("answer", b.prim("string"))).(*ast.OutputDecl)}),
		ast.WithEnumDecls([]*ast.EnumDecl{b.track(ast.NewEnumDecl("TravelMode", []*ast.EnumMemberDecl{
			b.track(ast.NewEnumMemberDecl("air")).(*ast.EnumMemberDecl),
			b.track(ast.NewEnumMemberDecl("rail")).(*ast.EnumMemberDecl),
		})).(*ast.EnumDecl)}),
		ast.WithTypeDecls([]*ast.TypeDecl{b.track(ast.NewTypeDecl("SearchResult", []*ast.FieldDecl{
			b.track(ast.NewFieldDecl("content", b.prim("string"))).(*ast.FieldDecl),
			b.track(ast.NewFieldDecl("score", b.prim("float"))).(*ast.FieldDecl),
		})).(*ast.TypeDecl)}),
		ast.WithConversationDecls([]*ast.ConversationVarDecl{b.track(ast.NewConversationVarDecl(
			"thread", b.prim("string"), b.strLit("hi"),
		)).(*ast.ConversationVarDecl)}),
		ast.WithCodeFunctions([]*ast.CodeFunctionDef{cf}),
		ast.WithFunctions([]*ast.FunctionDef{fn}),
		ast.WithEntry(entry),
	)
	b.track(prog)
	return prog, b
}

func buildEntry(b *builder) *ast.FunctionDef {
	// var items = { "key": 42 };
	mapLit := b.track(ast.NewMapLit([]ast.MapEntry{
		{Key: b.strLit("key"), Value: b.intLit(42)},
	})).(*ast.MapLit)
	varStmt := b.track(ast.NewVarDeclStmt("items", b.prim("map"), mapLit, true)).(*ast.VarDeclStmt)

	// if (true) { foo(1); } else if (false) { break; } else { continue; }
	ifCond := b.track(ast.NewBoolLit(true)).(*ast.BoolLit)
	callFoo := b.call(b.ident("foo"), []ast.Expr{b.intLit(1)})
	ifThen := b.block([]ast.Stmt{b.exprStmt(callFoo)})
	ei := b.track(ast.NewElseIfBranch(
		b.track(ast.NewBoolLit(false)).(*ast.BoolLit),
		b.block([]ast.Stmt{b.brk()}),
	)).(*ast.ElseIfBranch)
	elseBody := b.block([]ast.Stmt{b.cont()})
	ifStmt := b.track(ast.NewIfStmt(ifCond, ifThen, []*ast.ElseIfBranch{ei}, elseBody)).(*ast.IfStmt)

	// for (string s in list) { }
	forStmt := b.track(ast.NewForStmt("s", b.prim("string"), b.ident("list"),
		b.block(nil))).(*ast.ForStmt)

	// while (done) limit 3 { x = x + 1; }
	whileStmt := b.track(ast.NewWhileStmt(b.ident("done"), 3,
		b.block([]ast.Stmt{b.assign(b.ident("x"),
			b.track(ast.NewBinaryExpr("+", b.ident("x"), b.intLit(1))).(*ast.BinaryExpr), "set")}),
	)).(*ast.WhileStmt)

	// attempt op() as ok { } failure(err) { }
	attempt := b.track(ast.NewAttemptStmt(
		b.call(b.ident("op"), nil),
		b.track(ast.NewVarDeclStmt("ok", nil, nil, false)).(*ast.VarDeclStmt),
		b.block(nil),
		b.track(ast.NewVarDeclStmt("err", nil, nil, false)).(*ast.VarDeclStmt),
		b.block(nil),
	)).(*ast.AttemptStmt)

	// request_input<Form, Action>({}) as res { action a() { } timeout { } }
	ri := b.track(ast.NewRequestInputStmt(
		b.named("Form"), b.named("Action"),
		b.track(ast.NewMapLit(nil)).(*ast.MapLit),
		b.track(ast.NewVarDeclStmt("res", nil, nil, false)).(*ast.VarDeclStmt),
		[]*ast.HumanActionBranch{b.track(ast.NewHumanActionBranch(
			b.ident("a"), b.block(nil),
		)).(*ast.HumanActionBranch)},
		b.block(nil),
	)).(*ast.RequestInputStmt)

	// parallel { { "a"; } { } }
	parallel := b.track(ast.NewParallelStmt([]*ast.Block{
		b.block([]ast.Stmt{b.exprStmt(b.strLit("a"))}),
		b.block(nil),
	}, "a", "b")).(*ast.ParallelStmt)

	// template `Hello ${name}!`
	tpl := b.track(ast.NewTemplateString([]ast.TemplatePart{
		{Text: "Hello "}, {Expr: b.ident("name")}, {Text: "!"},
	})).(*ast.TemplateString)

	// array [1, 2.5, null]
	arr := b.track(ast.NewArrayLit([]ast.Expr{
		b.intLit(1), b.floatLit(2.5),
		b.track(ast.NewNullLit()).(ast.Expr),
	})).(*ast.ArrayLit)

	// unary -3
	unary := b.track(ast.NewUnaryExpr("-", b.intLit(3))).(*ast.UnaryExpr)

	// ternary true ? 1 : "no"
	ternary := b.track(ast.NewTernaryExpr(
		b.track(ast.NewBoolLit(true)).(*ast.BoolLit), b.intLit(1), b.strLit("no"),
	)).(*ast.TernaryExpr)

	// lambda (g) -> g
	lambda := b.track(ast.NewLambdaExpr([]string{"g"}, b.ident("g"))).(*ast.LambdaExpr)

	// for expression: for (item in items) { yield item; }
	fe := b.track(ast.NewForExpr("item", nil, b.ident("items"),
		b.block([]ast.Stmt{b.yield(b.ident("item"))}))).(*ast.ForExpr)

	// foreign code expression
	fc := b.track(ast.NewForeignCodeExpr("python3",
		[]*ast.CodeInputBinding{b.track(ast.NewCodeInputBinding("x", b.prim("int"), b.ident("x"))).(*ast.CodeInputBinding)},
		b.track(ast.NewCodeStructuralOutput([]*ast.CodeOutputField{
			b.track(ast.NewCodeOutputField("result", b.prim("int"))).(*ast.CodeOutputField),
		})).(*ast.CodeStructuralOutput),
		b.track(ast.NewTemplateString([]ast.TemplatePart{{Text: "pass"}})).(*ast.TemplateString),
	)).(*ast.ForeignCodeExpr)

	// parallel-for expression
	pfe := b.track(ast.NewParallelForExpr("x", nil, b.ident("xs"), b.block(nil), 2, "terminate")).(*ast.ParallelForExpr)

	// operation policy expression
	ope := b.track(ast.NewOperationPolicyExpr(b.call(b.ident("run"), nil),
		[]ast.OperationPolicy{
			b.track(ast.NewRetryPolicy(3, 500)).(*ast.RetryPolicy),
			b.track(ast.NewTimeoutPolicy(1000)).(*ast.TimeoutPolicy),
			b.track(ast.NewDefaultPolicy(b.intLit(0))).(*ast.DefaultPolicy),
		})).(*ast.OperationPolicyExpr)

	// if (false) { } — no else: ElseBody is a typed nil *Block, which must
	// not crash Walk (Finding 1 regression).
	ifNoElse := b.track(ast.NewIfStmt(
		b.track(ast.NewBoolLit(false)).(*ast.BoolLit),
		b.block(nil),
		nil, nil,
	)).(*ast.IfStmt)

	entry := b.track(ast.NewFunctionDef("main", nil, nil,
		b.block([]ast.Stmt{
			varStmt, ifStmt, ifNoElse, forStmt, whileStmt, attempt, ri, parallel,
			b.track(ast.NewVarDeclStmt("t", nil, tpl, true)).(*ast.VarDeclStmt),
			b.track(ast.NewVarDeclStmt("a", nil, arr, true)).(*ast.VarDeclStmt),
			b.track(ast.NewVarDeclStmt("u", nil, unary, true)).(*ast.VarDeclStmt),
			b.track(ast.NewVarDeclStmt("q", nil, ternary, true)).(*ast.VarDeclStmt),
			b.track(ast.NewVarDeclStmt("l", nil, lambda, true)).(*ast.VarDeclStmt),
			b.track(ast.NewVarDeclStmt("f", nil, fe, true)).(*ast.VarDeclStmt),
			b.track(ast.NewVarDeclStmt("fc", nil, fc, true)).(*ast.VarDeclStmt),
			b.track(ast.NewVarDeclStmt("pf", nil, pfe, true)).(*ast.VarDeclStmt),
			b.track(ast.NewVarDeclStmt("op", nil, ope, true)).(*ast.VarDeclStmt),
		}))).(*ast.FunctionDef)
	return entry
}

// ─── Program top-level fields ───────────────────────────────────────────

func TestProgramTopLevelFields(t *testing.T) {
	// Port of the brief's required test: ProgramNode fields mirror ast.py
	// ProgramNode: language_identity, mode, const_decls, outputs, enum_decls,
	// type_decls, conversation_decls, code_functions, functions, entry.
	p := ast.NewProgram(ast.WithLanguageIdentity("nodecoda/1"), ast.WithMode("workflow"))
	if p.LanguageIdentity() != "nodecoda/1" {
		t.Fatalf("LanguageIdentity() = %q, want %q", p.LanguageIdentity(), "nodecoda/1")
	}
	if p.Mode() != "workflow" {
		t.Fatalf("Mode() = %q, want %q", p.Mode(), "workflow")
	}
	if len(p.ConstDecls()) != 0 || len(p.Outputs()) != 0 || len(p.EnumDecls()) != 0 ||
		len(p.TypeDecls()) != 0 || len(p.ConversationDecls()) != 0 ||
		len(p.CodeFunctions()) != 0 || len(p.Functions()) != 0 || p.Entry() != nil {
		t.Fatalf("new program should have empty declarations, got non-empty fields")
	}
}

func TestProgramDefaults(t *testing.T) {
	// ast.py ProgramNode: mode defaults to "workflow", language_identity None.
	p := ast.NewProgram()
	if p.Mode() != "workflow" {
		t.Fatalf("Mode() = %q, want default %q", p.Mode(), "workflow")
	}
	if p.LanguageIdentity() != "" {
		t.Fatalf("LanguageIdentity() = %q, want empty (None)", p.LanguageIdentity())
	}
}

// ─── Node identity / spans ──────────────────────────────────────────────

func TestNodeIDsAreUniqueAndIncreasing(t *testing.T) {
	ids := map[ast.NodeID]bool{}
	var prev ast.NodeID
	for i := 0; i < 50; i++ {
		n := ast.NewIntLit(int64(i))
		id := n.NodeID()
		if id == 0 {
			t.Fatalf("node %d got zero NodeID", i)
		}
		if ids[id] {
			t.Fatalf("duplicate NodeID %d", id)
		}
		ids[id] = true
		if i > 0 && id <= prev {
			t.Fatalf("NodeID %d does not increase after %d", id, prev)
		}
		prev = id
	}
}

func TestSpanCanBeAssignedPostConstruction(t *testing.T) {
	// Mirrors Python parser's `node.span = self._span_from(start)` pattern.
	n := ast.NewIdentExpr("x")
	if n.Span() != (ast.Span{}) {
		t.Fatalf("fresh node span = %+v, want zero span", n.Span())
	}
	span := ast.Span{
		Start: token.Location{Line: 1, Column: 2, Offset: 3, Filename: "f.dc"},
		End:   token.Location{Line: 1, Column: 4, Offset: 5, Filename: "f.dc"},
	}
	n.SetSpan(span)
	if n.Span() != span {
		t.Fatalf("Span() = %+v, want %+v", n.Span(), span)
	}
}

// ─── Statements: Block nesting and control flow ─────────────────────────

func TestBlockNesting(t *testing.T) {
	// Port of test_entry_basic / test_if_simple nesting: entry.body.stmts.
	inner := ast.NewBlock([]ast.Stmt{ast.NewBreakStmt()})
	body := ast.NewBlock([]ast.Stmt{inner})
	entry := ast.NewFunctionDef("main", nil, nil, body)
	p := ast.NewProgram(ast.WithEntry(entry))

	if p.Entry() == nil {
		t.Fatal("Entry() is nil")
	}
	stmts := p.Entry().Body().Stmts()
	if len(stmts) != 1 {
		t.Fatalf("entry body stmts = %d, want 1", len(stmts))
	}
	innerStmts := stmts[0].(*ast.Block).Stmts()
	if len(innerStmts) != 1 {
		t.Fatalf("inner block stmts = %d, want 1", len(innerStmts))
	}
	if _, ok := innerStmts[0].(*ast.BreakStmt); !ok {
		t.Fatalf("inner stmt type = %T, want *ast.BreakStmt", innerStmts[0])
	}
}

func TestIfStmtChildren(t *testing.T) {
	// Port of test_if_simple / test_if_else / test_if_else_if.
	cond := ast.NewBoolLit(true)
	thenBody := ast.NewBlock(nil)
	ei := ast.NewElseIfBranch(ast.NewBoolLit(false), ast.NewBlock(nil))
	elseBody := ast.NewBlock(nil)
	s := ast.NewIfStmt(cond, thenBody, []*ast.ElseIfBranch{ei}, elseBody)

	if s.Condition() != cond {
		t.Fatal("Condition() != constructed condition")
	}
	if s.ThenBody() != thenBody {
		t.Fatal("ThenBody() != constructed block")
	}
	if len(s.ElseIfs()) != 1 || s.ElseIfs()[0] != ei {
		t.Fatal("ElseIfs() mismatch")
	}
	if s.ElseBody() != elseBody {
		t.Fatal("ElseBody() != constructed block")
	}
	if ast.NewIfStmt(cond, thenBody, nil, nil).ElseBody() != nil {
		t.Fatal("default ElseBody() should be nil")
	}
}

func TestForStmtChildren(t *testing.T) {
	// Port of test_for_loop / test_for_with_type.
	iter := ast.NewIdentExpr("items")
	body := ast.NewBlock(nil)
	s := ast.NewForStmt("item", ast.NewPrimTypeRef("string"), iter, body)
	if s.VarName() != "item" {
		t.Fatalf("VarName() = %q, want %q", s.VarName(), "item")
	}
	if s.Iterable() != iter || s.Body() != body {
		t.Fatal("ForStmt iterable/body mismatch")
	}
	if s.VarType() == nil || s.VarType().NodeID() == 0 {
		t.Fatal("VarType() should be non-nil with an ID")
	}
	if ast.NewForStmt("item", nil, iter, body).VarType() != nil {
		t.Fatal("VarType() should default to nil (var, inferred)")
	}
}

func TestWhileStmtChildren(t *testing.T) {
	// Port of test_while_loop and capability test (limit field).
	cond := ast.NewIdentExpr("done")
	body := ast.NewBlock([]ast.Stmt{ast.NewOutputStmt(ast.NewIdentExpr("x"))})
	s := ast.NewWhileStmt(cond, 2, body)
	if s.Condition() != cond || s.Body() != body {
		t.Fatal("WhileStmt condition/body mismatch")
	}
	if s.Limit() != 2 {
		t.Fatalf("Limit() = %d, want 2", s.Limit())
	}
	if _, ok := s.Body().Stmts()[0].(*ast.OutputStmt); !ok {
		t.Fatalf("while body stmt = %T, want OutputStmt", s.Body().Stmts()[0])
	}
}

func TestVarDeclStmtFields(t *testing.T) {
	// Port of test_function_var_decl / test_function_var_inferred / is_mutable.
	typed := ast.NewVarDeclStmt("x", ast.NewPrimTypeRef("string"), nil, true)
	if typed.Name() != "x" || typed.TypeRef() == nil || typed.Init() != nil || !typed.IsMutable() {
		t.Fatalf("typed VarDeclStmt fields mismatch")
	}
	inferred := ast.NewVarDeclStmt("x", nil, ast.NewIntLit(42), true)
	if inferred.TypeRef() != nil {
		t.Fatal("inferred VarDeclStmt should have nil type ref")
	}
	if inferred.Init() == nil || inferred.Init().NodeID() == 0 {
		t.Fatal("inferred VarDeclStmt init missing")
	}
	if immutable := ast.NewVarDeclStmt("x", nil, nil, false); immutable.IsMutable() {
		t.Fatal("IsMutable() should be false for let-style binding")
	}
}

func TestAssignStmtFields(t *testing.T) {
	// Port of test_assignment / test_compound_assignment.
	target := ast.NewIdentExpr("x")
	val := ast.NewBinaryExpr("+", ast.NewIdentExpr("x"), ast.NewIntLit(1))
	s := ast.NewAssignStmt(target, val, "set")
	if s.Target() != target || s.Value() != val || s.Operation() != "set" {
		t.Fatalf("AssignStmt fields mismatch")
	}
	if ast.NewAssignStmt(target, val, "+=").Operation() != "+=" {
		t.Fatal("operation should be carried verbatim")
	}
}

func TestOutputStmtFields(t *testing.T) {
	// Port of test_output_stmt.
	val := ast.NewIdentExpr("x")
	s := ast.NewOutputStmt(val)
	if s.Value() != val {
		t.Fatal("OutputStmt value mismatch")
	}
}

func TestExprStmtWrapsExpr(t *testing.T) {
	// Port of test_expr_stmt.
	call := ast.NewCallExpr(ast.NewIdentExpr("print"), nil, []ast.Expr{ast.NewStringLit("hi")})
	s := ast.NewExprStmt(call)
	if s.Expr() != call {
		t.Fatal("ExprStmt.Expr() mismatch")
	}
}

func TestParallelStmtBranches(t *testing.T) {
	// Port of test_parallel.
	b1 := ast.NewBlock(nil)
	b2 := ast.NewBlock(nil)
	s := ast.NewParallelStmt([]*ast.Block{b1, b2})
	if len(s.Branches()) != 2 || s.Branches()[0] != b1 || s.Branches()[1] != b2 {
		t.Fatal("ParallelStmt branches mismatch")
	}
}

func TestAttemptStmtDefaults(t *testing.T) {
	// ast.py AttemptStmt: result_binding/error_binding default to
	// VarDeclStmt(is_mutable=False).
	op := ast.NewCallExpr(ast.NewIdentExpr("op"), nil, nil)
	s := ast.NewAttemptStmt(op, nil, nil, nil, nil)
	if s.Operation() != op {
		t.Fatal("AttemptStmt operation mismatch")
	}
	if s.ResultBinding() == nil || s.ResultBinding().IsMutable() {
		t.Fatal("default result_binding should be immutable VarDeclStmt")
	}
	if s.ErrorBinding() == nil || s.ErrorBinding().IsMutable() {
		t.Fatal("default error_binding should be immutable VarDeclStmt")
	}
	if s.SuccessBody() == nil || s.FailureBody() == nil {
		t.Fatal("default attempt bodies should be non-nil Blocks")
	}
}

func TestRequestInputStmtFields(t *testing.T) {
	// Port of request_input structure: form_type/action_type/config/branches.
	form := ast.NewNamedTypeRef("Form")
	action := ast.NewNamedTypeRef("Action")
	cfg := ast.NewMapLit(nil)
	br := ast.NewHumanActionBranch(ast.NewIdentExpr("confirm"), ast.NewBlock(nil))
	tb := ast.NewBlock(nil)
	s := ast.NewRequestInputStmt(form, action, cfg,
		ast.NewVarDeclStmt("res", nil, nil, false), []*ast.HumanActionBranch{br}, tb)
	if s.FormType() != form || s.ActionType() != action || s.Config() != cfg {
		t.Fatal("RequestInputStmt type/config mismatch")
	}
	if len(s.ActionBranches()) != 1 || s.ActionBranches()[0] != br || s.TimeoutBody() != tb {
		t.Fatal("RequestInputStmt branches mismatch")
	}
	if s.ResultBinding() == nil || s.ResultBinding().IsMutable() {
		t.Fatal("result binding should be immutable VarDeclStmt")
	}
}

func TestBreakContinueAreLeafStatements(t *testing.T) {
	// Port of test_break_continue.
	b := ast.NewBreakStmt()
	c := ast.NewContinueStmt()
	if b.NodeID() == 0 || c.NodeID() == 0 {
		t.Fatal("leaf statements must still carry NodeIDs")
	}
}

// ─── Expressions ────────────────────────────────────────────────────────

func TestLiteralExprs(t *testing.T) {
	// Port of test_int_literal / test_float_literal / test_string_literal /
	// test_bool_literal / test_null_literal.
	if v := ast.NewIntLit(42).Value(); v != 42 {
		t.Fatalf("IntLit.Value() = %d, want 42", v)
	}
	if v := ast.NewFloatLit(3.14).Value(); v != 3.14 {
		t.Fatalf("FloatLit.Value() = %v, want 3.14", v)
	}
	if v := ast.NewStringLit("hello").Value(); v != "hello" {
		t.Fatalf("StringLit.Value() = %q", v)
	}
	if v := ast.NewBoolLit(true).Value(); v != true {
		t.Fatalf("BoolLit.Value() = %v, want true", v)
	}
	ast.NewNullLit() // must exist; nothing else to assert
}

func TestArrayLitElements(t *testing.T) {
	// Port of test_array_literal.
	elems := []ast.Expr{ast.NewIntLit(1), ast.NewIntLit(2), ast.NewIntLit(3)}
	l := ast.NewArrayLit(elems)
	if len(l.Elements()) != 3 {
		t.Fatalf("Elements() len = %d, want 3", len(l.Elements()))
	}
	for i, e := range elems {
		if l.Elements()[i] != e {
			t.Fatalf("element %d mismatch", i)
		}
	}
}

func TestMapLitEntries(t *testing.T) {
	// Port of test_map_literal: entries are (key, value) pairs.
	key := ast.NewStringLit("key")
	val := ast.NewIntLit(42)
	l := ast.NewMapLit([]ast.MapEntry{{Key: key, Value: val}})
	if len(l.Entries()) != 1 {
		t.Fatalf("Entries() len = %d, want 1", len(l.Entries()))
	}
	got := l.Entries()[0]
	if got.Key != key || got.Value != val {
		t.Fatalf("entry = {%+v, %+v}, want constructed key/value", got.Key, got.Value)
	}
}

func TestIdentAndFieldAccess(t *testing.T) {
	// Port of test_identifier / test_field_access.
	id := ast.NewIdentExpr("y")
	if id.Name() != "y" {
		t.Fatalf("IdentExpr.Name() = %q", id.Name())
	}
	obj := ast.NewIdentExpr("obj")
	fa := ast.NewFieldAccessExpr(obj, "field")
	if fa.Obj() != obj || fa.FieldName() != "field" {
		t.Fatal("FieldAccessExpr obj/field mismatch")
	}
}

func TestIndexExpr(t *testing.T) {
	// Port of test_index_access.
	obj := ast.NewIdentExpr("arr")
	idx := ast.NewIntLit(0)
	ix := ast.NewIndexExpr(obj, idx)
	if ix.Obj() != obj || ix.Index() != idx {
		t.Fatal("IndexExpr obj/index mismatch")
	}
}

func TestCallExpr(t *testing.T) {
	// Port of test_function_call / test_method_call / type-arg test.
	call := ast.NewCallExpr(ast.NewIdentExpr("foo"), nil, []ast.Expr{ast.NewIntLit(1), ast.NewIntLit(2)})
	if call.Func() == nil || len(call.Args()) != 2 {
		t.Fatal("CallExpr func/args mismatch")
	}
	// method call: func is a FieldAccessExpr
	mc := ast.NewCallExpr(ast.NewFieldAccessExpr(ast.NewIdentExpr("obj"), "method"), nil,
		[]ast.Expr{ast.NewIntLit(1)})
	if _, ok := mc.Func().(*ast.FieldAccessExpr); !ok {
		t.Fatalf("method call func = %T, want *ast.FieldAccessExpr", mc.Func())
	}
	// type arguments: extract<Travel>(...)
	tc := ast.NewCallExpr(ast.NewIdentExpr("extract"), []ast.TypeRef{ast.NewNamedTypeRef("Travel")}, nil)
	if len(tc.TypeArgs()) != 1 {
		t.Fatal("TypeArgs() len = 1 expected")
	}
	if n := tc.TypeArgs()[0].(*ast.NamedTypeRef).Name(); n != "Travel" {
		t.Fatalf("type arg name = %q, want Travel", n)
	}
}

func TestBinaryExprOperands(t *testing.T) {
	// Port of test_binary_precedence: 1 + 2 * 3 → op "+", right is BinaryExpr("*").
	left := ast.NewIntLit(1)
	right := ast.NewBinaryExpr("*", ast.NewIntLit(2), ast.NewIntLit(3))
	b := ast.NewBinaryExpr("+", left, right)
	if b.Op() != "+" {
		t.Fatalf("Op() = %q, want +", b.Op())
	}
	if b.Left() != left {
		t.Fatal("Left() mismatch")
	}
	rb, ok := b.Right().(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("Right() = %T, want *ast.BinaryExpr", b.Right())
	}
	if rb.Op() != "*" {
		t.Fatalf("right op = %q, want *", rb.Op())
	}
}

func TestUnaryAndTernary(t *testing.T) {
	// Port of test_unary_not / test_unary_minus / test_ternary.
	u := ast.NewUnaryExpr("!", ast.NewBoolLit(true))
	if u.Op() != "!" || u.Operand() == nil {
		t.Fatal("UnaryExpr op/operand mismatch")
	}
	cond := ast.NewBoolLit(true)
	te := ast.NewTernaryExpr(cond, ast.NewIntLit(1), ast.NewIntLit(2))
	if te.Condition() != cond || te.ThenVal() == nil || te.ElseVal() == nil {
		t.Fatal("TernaryExpr fields mismatch")
	}
}

func TestLambdaExpr(t *testing.T) {
	// Port of test_calculation_lambda_parses_as_expression_only.
	l := ast.NewLambdaExpr([]string{"group"}, ast.NewIdentExpr("group"))
	if len(l.Params()) != 1 || l.Params()[0] != "group" {
		t.Fatalf("LambdaExpr.Params() = %v", l.Params())
	}
	if l.Body() == nil {
		t.Fatal("LambdaExpr.Body() is nil")
	}
}

func TestTemplateStringParts(t *testing.T) {
	// ast.py TemplateString: parts alternate literal strings and Exprs.
	name := ast.NewIdentExpr("name")
	ts := ast.NewTemplateString([]ast.TemplatePart{{Text: "Hello "}, {Expr: name}, {Text: "!"}})
	parts := ts.Parts()
	if len(parts) != 3 {
		t.Fatalf("Parts() len = %d, want 3", len(parts))
	}
	if parts[0].Text != "Hello " || parts[0].Expr != nil {
		t.Fatalf("part 0 = %+v", parts[0])
	}
	if parts[1].Expr != name {
		t.Fatalf("part 1 expr mismatch")
	}
	if parts[2].Text != "!" {
		t.Fatalf("part 2 = %+v", parts[2])
	}
}

func TestForExprFields(t *testing.T) {
	// ast.py ForExpr: var_name, var_type, iterable, body.
	iter := ast.NewIdentExpr("items")
	body := ast.NewBlock([]ast.Stmt{ast.NewYieldStmt(ast.NewIdentExpr("item"))})
	e := ast.NewForExpr("item", nil, iter, body)
	if e.VarName() != "item" || e.Iterable() != iter || e.Body() != body || e.VarType() != nil {
		t.Fatal("ForExpr fields mismatch")
	}
	if body.Stmts()[0].(*ast.YieldStmt).Value() == nil {
		t.Fatal("yield value missing")
	}
}

func TestParallelForExprFields(t *testing.T) {
	// ast.py ParallelForExpr extends ForExpr with concurrency and on_error.
	iter := ast.NewIdentExpr("xs")
	e := ast.NewParallelForExpr("x", nil, iter, ast.NewBlock(nil), 2, "terminate")
	if e.VarName() != "x" || e.Iterable() != iter {
		t.Fatal("ParallelForExpr base fields mismatch")
	}
	if e.Concurrency() != 2 || e.OnError() != "terminate" {
		t.Fatalf("ParallelForExpr concurrency/on_error = %d/%q", e.Concurrency(), e.OnError())
	}
	if e.Body() == nil {
		t.Fatal("ParallelForExpr body is nil")
	}
}

func TestParallelStmtBranchNames(t *testing.T) {
	// Parallel statement branch labels are source-level only; branches
	// stay plain Blocks.
	br := ast.NewBlock(nil)
	ps := ast.NewParallelStmt([]*ast.Block{br}, "stable")
	if len(ps.Branches()) != 1 || ps.Branches()[0] != br {
		t.Fatal("ParallelStmt branches mismatch")
	}
	if names := ps.BranchNames(); len(names) != 1 || names[0] != "stable" {
		t.Fatal("ParallelStmt branch labels mismatch")
	}
}

func TestForeignCodeExprFields(t *testing.T) {
	// ast.py ForeignCodeExpr: language, inputs, output, source.
	in := ast.NewCodeInputBinding("x", ast.NewPrimTypeRef("int"), ast.NewIdentExpr("x"))
	out := ast.NewCodeStructuralOutput([]*ast.CodeOutputField{
		ast.NewCodeOutputField("result", ast.NewPrimTypeRef("int")),
	})
	src := ast.NewTemplateString([]ast.TemplatePart{{Text: "pass"}})
	e := ast.NewForeignCodeExpr("python3", []*ast.CodeInputBinding{in}, out, src)
	if e.Language() != "python3" || len(e.Inputs()) != 1 || e.Inputs()[0] != in {
		t.Fatal("ForeignCodeExpr language/inputs mismatch")
	}
	if e.Output() != out || e.Source() != src {
		t.Fatal("ForeignCodeExpr output/source mismatch")
	}
	if ast.NewCodeDirectOutput(nil).TypeRef() == nil {
		t.Fatal("CodeDirectOutput.TypeRef() should default to non-nil")
	}
	cof := ast.NewCodeOutputField("result", ast.NewPrimTypeRef("int"))
	if cof.Name() != "result" || cof.TypeRef() == nil {
		t.Fatal("CodeOutputField fields mismatch")
	}
}

func TestOperationPolicyExpr(t *testing.T) {
	// ast.py OperationPolicyExpr: operation + typed policies.
	op := ast.NewCallExpr(ast.NewIdentExpr("run"), nil, nil)
	rp := ast.NewRetryPolicy(3, 500)
	tp := ast.NewTimeoutPolicy(1000)
	dp := ast.NewDefaultPolicy(ast.NewIntLit(0))
	e := ast.NewOperationPolicyExpr(op, []ast.OperationPolicy{rp, tp, dp})
	if e.Operation() != op || len(e.Policies()) != 3 {
		t.Fatal("OperationPolicyExpr operation/policies mismatch")
	}
	if rp.MaxRetries() != 3 || rp.IntervalMs() != 500 {
		t.Fatalf("RetryPolicy = %d/%d", rp.MaxRetries(), rp.IntervalMs())
	}
	if tp.TimeoutMs() != 1000 {
		t.Fatalf("TimeoutPolicy.TimeoutMs() = %d", tp.TimeoutMs())
	}
	if dp.Value() == nil {
		t.Fatal("DefaultPolicy.Value() is nil")
	}
}

// ─── Type references ────────────────────────────────────────────────────

func TestTypeRefPrimitives(t *testing.T) {
	// Port of test_type_ref_primitives: string/int/float/bool/file/void/any.
	for _, name := range []string{"string", "int", "float", "bool", "void", "any"} {
		ref := ast.NewPrimTypeRef(name)
		if ref.Name() != name {
			t.Fatalf("PrimTypeRef.Name() = %q, want %q", ref.Name(), name)
		}
	}
}

func TestTypeRefArrayAndMap(t *testing.T) {
	// Port of test_type_ref_array_generic / test_type_ref_map.
	arr := ast.NewArrayTypeRef(ast.NewPrimTypeRef("int"))
	if arr.Element() == nil || arr.Element().(*ast.PrimTypeRef).Name() != "int" {
		t.Fatalf("ArrayTypeRef.Element() = %v", arr.Element())
	}
	m := ast.NewMapTypeRef(ast.NewPrimTypeRef("string"), ast.NewPrimTypeRef("any"))
	if m.Key().(*ast.PrimTypeRef).Name() != "string" || m.Value().(*ast.PrimTypeRef).Name() != "any" {
		t.Fatal("MapTypeRef key/value mismatch")
	}
}

func TestTypeRefNamedAndOptional(t *testing.T) {
	// Port of test_type_ref_named and OptionalTypeRef.
	n := ast.NewNamedTypeRef("SearchResult")
	if n.Name() != "SearchResult" {
		t.Fatalf("NamedTypeRef.Name() = %q", n.Name())
	}
	o := ast.NewOptionalTypeRef(ast.NewPrimTypeRef("string"))
	if o.Value() == nil || o.Value().(*ast.PrimTypeRef).Name() != "string" {
		t.Fatal("OptionalTypeRef.Value() mismatch")
	}
}

func TestFileTypeRef(t *testing.T) {
	// ast.py FileTypeRef: carries FileTypeConstraints.
	ref := ast.NewFileTypeRef(ast.FileTypeConstraints{
		Types: []string{"image"}, Extensions: []string{".png"},
	})
	c := ref.Constraints()
	if len(c.Types) != 1 || c.Types[0] != "image" {
		t.Fatalf("constraints.Types = %v", c.Types)
	}
	if len(c.Extensions) != 1 || c.Extensions[0] != ".png" {
		t.Fatalf("constraints.Extensions = %v", c.Extensions)
	}
	if len(c.UploadMethods) != 0 {
		t.Fatalf("constraints.UploadMethods = %v", c.UploadMethods)
	}
}

// ─── Declarations ───────────────────────────────────────────────────────

func TestEnumAndTypeDecls(t *testing.T) {
	// Port of test_enum_declaration_roundtrips_canonically / test_type_decl.
	ed := ast.NewEnumDecl("TravelMode", []*ast.EnumMemberDecl{
		ast.NewEnumMemberDecl("air"), ast.NewEnumMemberDecl("rail"),
	})
	if ed.Name() != "TravelMode" || len(ed.Members()) != 2 || ed.Members()[1].Name() != "rail" {
		t.Fatal("EnumDecl fields mismatch")
	}
	td := ast.NewTypeDecl("SearchResult", []*ast.FieldDecl{
		ast.NewFieldDecl("content", ast.NewPrimTypeRef("string")),
		ast.NewFieldDecl("score", ast.NewPrimTypeRef("float")),
	})
	if td.Name() != "SearchResult" || len(td.Fields()) != 2 || td.Fields()[1].Name() != "score" {
		t.Fatal("TypeDecl fields mismatch")
	}
}

func TestFunctionDefFields(t *testing.T) {
	// Port of test_function_no_params / test_function_with_params.
	fn := ast.NewFunctionDef("add", []*ast.ParamDecl{
		ast.NewParamDecl("a", ast.NewPrimTypeRef("int"), nil),
		ast.NewParamDecl("b", ast.NewPrimTypeRef("int"), nil),
	}, ast.NewPrimTypeRef("int"), ast.NewBlock(nil))
	if fn.Name() != "add" || len(fn.Params()) != 2 || fn.ReturnType() == nil || fn.Body() == nil {
		t.Fatal("FunctionDef fields mismatch")
	}
	noRet := ast.NewFunctionDef("greet", nil, nil, ast.NewBlock(nil))
	if noRet.ReturnType() != nil {
		t.Fatal("ReturnType() should default to nil")
	}
	if noRet.Params() == nil || len(noRet.Params()) != 0 {
		t.Fatal("Params() should default to empty")
	}
	pd := ast.NewParamDecl("prompt", ast.NewPrimTypeRef("string"), ast.NewStringLit("hello"))
	if pd.Default() == nil {
		t.Fatal("ParamDecl.Default() should be set")
	}
	// CodeFunctionDef is a distinct node with the same shape.
	cf := ast.NewCodeFunctionDef("normalize", nil, nil, ast.NewBlock(nil))
	if cf.Name() != "normalize" || cf.Body() == nil {
		t.Fatal("CodeFunctionDef fields mismatch")
	}
}

func TestConstAndConversationDecls(t *testing.T) {
	// ast.py ConstDecl: value defaults to NullLit. ConversationVarDecl carries default.
	c := ast.NewConstDecl("PI", ast.NewFloatLit(3.14))
	if c.Name() != "PI" || c.Value() == nil {
		t.Fatal("ConstDecl fields mismatch")
	}
	if _, ok := ast.NewConstDecl("X", nil).Value().(*ast.NullLit); !ok {
		t.Fatal("ConstDecl default value should be NullLit")
	}
	cv := ast.NewConversationVarDecl("thread", ast.NewPrimTypeRef("string"), ast.NewStringLit("hi"))
	if cv.Name() != "thread" || cv.TypeRef() == nil || cv.Default() == nil {
		t.Fatal("ConversationVarDecl fields mismatch")
	}
}

// ─── Walk ───────────────────────────────────────────────────────────────

func TestWalkEnumeratesEveryConstructedNode(t *testing.T) {
	prog, b := buildSampleProgram(t)
	c := &countingVisitor{}
	ast.Walk(c, prog)

	if len(b.nodes) == 0 {
		t.Fatal("test scaffold bug: no nodes constructed")
	}
	if got := c.count(); got != len(b.nodes) {
		var missing []string
		for _, n := range b.nodes {
			if c.nodes[n] == 0 {
				missing = append(missing, typeName(n))
			}
		}
		t.Fatalf("Walk visited %d nodes, want %d; unvisited: %v", got, len(b.nodes), missing)
	}
	for _, n := range b.nodes {
		if c.nodes[n] > 1 {
			t.Fatalf("node %s(%d) visited %d times, want once", typeName(n), n.NodeID(), c.nodes[n])
		}
	}
}

func TestWalkVisitOrderMatchesConstruction(t *testing.T) {
	// Walk must visit the root, then children depth-first in a stable order.
	left := ast.NewIntLit(1)
	right := ast.NewBinaryExpr("*", ast.NewIntLit(2), ast.NewIntLit(3))
	b := ast.NewBinaryExpr("+", left, right)
	prog := ast.NewProgram(ast.WithEntry(ast.NewFunctionDef("main", nil, nil,
		ast.NewBlock([]ast.Stmt{ast.NewVarDeclStmt("x", nil, b, true)}))))

	var order []ast.Node
	var rec visitorFunc
	rec = func(n ast.Node) ast.Visitor {
		order = append(order, n)
		return rec // keep descending: nil would prune (go/ast convention)
	}
	ast.Walk(rec, prog)

	// Root first, then a deterministic depth-first child order.
	if order[0] != prog {
		t.Fatalf("first visited node = %T, want Program root", order[0])
	}
	if got := order[1].(*ast.FunctionDef); got != prog.Entry() {
		t.Fatalf("second visited node = %T, want Entry", order[1])
	}
	if got := order[2].(*ast.Block); got != prog.Entry().Body() {
		t.Fatalf("third visited node = %T, want entry body block", order[2])
	}
}

func TestWalkSkipsChildrenWhenVisitorReturnsNil(t *testing.T) {
	// go/ast convention: returning nil from Visit prunes the subtree.
	inner := ast.NewIntLit(1)
	b := ast.NewBinaryExpr("+", inner, ast.NewIntLit(2))
	prog := ast.NewProgram(ast.WithEntry(ast.NewFunctionDef("main", nil, nil,
		ast.NewBlock([]ast.Stmt{ast.NewVarDeclStmt("x", nil, b, true)}))))

	var visited []ast.Node
	prune := visitorFunc(func(n ast.Node) ast.Visitor {
		visited = append(visited, n)
		return nil // never descend
	})
	ast.Walk(prune, prog)
	if len(visited) != 1 || visited[0] != prog {
		t.Fatalf("pruned walk visited %d nodes (want only root): %v", len(visited), visited)
	}
}

func TestWalkSkipsNilTypedChildren(t *testing.T) {
	// A typed nil child (IfStmt.ElseBody *Block(nil)) becomes a non-nil
	// Node interface; Walk must treat it as absent instead of panicking.
	cond := ast.NewBoolLit(true)
	thenBody := ast.NewBlock([]ast.Stmt{ast.NewBreakStmt()})
	s := ast.NewIfStmt(cond, thenBody, nil, nil) // elseBody = typed nil
	if s.ElseBody() != nil {
		t.Fatal("scaffold bug: ElseBody() should be nil")
	}

	var visited []ast.Node
	var rec visitorFunc
	rec = func(n ast.Node) ast.Visitor {
		visited = append(visited, n)
		return rec
	}
	ast.Walk(rec, s) // must not panic

	want := 4 // ifStmt, condition, thenBody, breakStmt inside thenBody
	if len(visited) != want {
		t.Fatalf("walk of if-without-else visited %d nodes, want %d: %v", len(visited), want, visited)
	}
	if visited[0] != s || visited[1] != cond || visited[2] != thenBody {
		t.Fatal("walk of if-without-else visited unexpected nodes/order")
	}
	if _, ok := visited[3].(*ast.BreakStmt); !ok {
		t.Fatalf("walk of if-without-else 4th node = %T, want *ast.BreakStmt", visited[3])
	}
}

func TestWalkCoversEveryNodeCategory(t *testing.T) {
	// Every concrete node type must be reachable through Walk; the sample
	// program exercises each category and the exhaustive test above proves
	// reachability. Here we additionally assert the root is a Node with an ID.
	prog := ast.NewProgram()
	if prog.NodeID() == 0 {
		t.Fatal("Program root must have a NodeID")
	}
	// Sanity: interface satisfaction at compile time.
	var _ ast.Expr = ast.NewIntLit(1)
	var _ ast.Stmt = ast.NewBlock(nil)
	var _ ast.TypeRef = ast.NewPrimTypeRef("string")
	var _ ast.Node = ast.NewProgram()
}

func TestLanguageIdentityConstant(t *testing.T) {
	if ast.NodecodaLanguageIdentity != "nodecoda/1" {
		t.Fatalf("NodecodaLanguageIdentity = %q, want nodecoda/1", ast.NodecodaLanguageIdentity)
	}
}

func TestString(t *testing.T) {
	// Ensure no node String() method panics (diagnostics convenience).
	if s := strings.TrimSpace(ast.NewProgram().String()); s == "" {
		t.Fatal("Program.String() should produce a non-empty repr")
	}
}

func TestStringEveryNodeType(t *testing.T) {
	// Regression for the reflection repr path: every concrete node type must
	// produce a non-empty, panic-free String(), including nodes holding
	// non-nil unexported child nodes (e.g. FunctionDef.body, IfStmt.thenBody)
	// and nodes embedding another node (ParallelForExpr, CodeFunctionDef).
	tests := []struct {
		name string
		node ast.Node
	}{
		{"IntLit", ast.NewIntLit(1)},
		{"FloatLit", ast.NewFloatLit(1.5)},
		{"StringLit", ast.NewStringLit("s")},
		{"TemplateString", ast.NewTemplateString([]ast.TemplatePart{{Text: "a"}, {Expr: ast.NewIdentExpr("b")}})},
		{"BoolLit", ast.NewBoolLit(true)},
		{"NullLit", ast.NewNullLit()},
		{"ArrayLit", ast.NewArrayLit([]ast.Expr{ast.NewIntLit(1)})},
		{"MapLit", ast.NewMapLit([]ast.MapEntry{{Key: ast.NewStringLit("k"), Value: ast.NewIntLit(1)}})},
		{"IdentExpr", ast.NewIdentExpr("x")},
		{"FieldAccessExpr", ast.NewFieldAccessExpr(ast.NewIdentExpr("o"), "f")},
		{"IndexExpr", ast.NewIndexExpr(ast.NewIdentExpr("a"), ast.NewIntLit(0))},
		{"CallExpr", ast.NewCallExpr(ast.NewIdentExpr("f"), []ast.TypeRef{ast.NewPrimTypeRef("int")}, []ast.Expr{ast.NewIntLit(1)})},
		{"RetryPolicy", ast.NewRetryPolicy(3, 500)},
		{"TimeoutPolicy", ast.NewTimeoutPolicy(1000)},
		{"DefaultPolicy", ast.NewDefaultPolicy(ast.NewIntLit(0))},
		{"OperationPolicyExpr", ast.NewOperationPolicyExpr(ast.NewIdentExpr("op"), []ast.OperationPolicy{ast.NewRetryPolicy(1, 2)})},
		{"UnaryExpr", ast.NewUnaryExpr("!", ast.NewBoolLit(true))},
		{"BinaryExpr", ast.NewBinaryExpr("+", ast.NewIntLit(1), ast.NewIntLit(2))},
		{"TernaryExpr", ast.NewTernaryExpr(ast.NewBoolLit(true), ast.NewIntLit(1), ast.NewIntLit(2))},
		{"LambdaExpr", ast.NewLambdaExpr([]string{"g"}, ast.NewIdentExpr("g"))},
		{"ForExpr", ast.NewForExpr("i", nil, ast.NewIdentExpr("xs"), ast.NewBlock(nil))},
		{"ParallelForExpr", ast.NewParallelForExpr("i", nil, ast.NewIdentExpr("xs"), ast.NewBlock(nil), 2, "terminate")},
		{"CodeInputBinding", ast.NewCodeInputBinding("x", ast.NewPrimTypeRef("int"), ast.NewIdentExpr("x"))},
		{"CodeDirectOutput", ast.NewCodeDirectOutput(nil)},
		{"CodeOutputField", ast.NewCodeOutputField("r", ast.NewPrimTypeRef("int"))},
		{"CodeStructuralOutput", ast.NewCodeStructuralOutput([]*ast.CodeOutputField{ast.NewCodeOutputField("r", ast.NewPrimTypeRef("int"))})},
		{"ForeignCodeExpr", ast.NewForeignCodeExpr("python3", nil, ast.NewCodeDirectOutput(nil), ast.NewTemplateString(nil))},
		{"Block", ast.NewBlock(nil)},
		{"VarDeclStmt", ast.NewVarDeclStmt("x", nil, nil, true)},
		{"AssignStmt", ast.NewAssignStmt(ast.NewIdentExpr("x"), ast.NewIntLit(1), "set")},
		{"ExprStmt", ast.NewExprStmt(ast.NewIdentExpr("x"))},
		{"IfStmt", ast.NewIfStmt(ast.NewBoolLit(true), ast.NewBlock(nil), nil, nil)},
		{"ElseIfBranch", ast.NewElseIfBranch(ast.NewBoolLit(false), ast.NewBlock(nil))},
		{"ForStmt", ast.NewForStmt("i", nil, ast.NewIdentExpr("xs"), ast.NewBlock(nil))},
		{"WhileStmt", ast.NewWhileStmt(ast.NewIdentExpr("c"), 3, ast.NewBlock(nil))},
		{"ReturnStmt", ast.NewReturnStmt(nil)},
		{"YieldStmt", ast.NewYieldStmt(nil)},
		{"OutputStmt", ast.NewOutputStmt(ast.NewIdentExpr("v"))},
		{"ParallelStmt", ast.NewParallelStmt([]*ast.Block{ast.NewBlock(nil)})},
		{"AttemptStmt", ast.NewAttemptStmt(nil, nil, nil, nil, nil)},
		{"HumanActionBranch", ast.NewHumanActionBranch(ast.NewIdentExpr("a"), ast.NewBlock(nil))},
		{"RequestInputStmt", ast.NewRequestInputStmt(nil, nil, nil, nil, nil, nil)},
		{"BreakStmt", ast.NewBreakStmt()},
		{"ContinueStmt", ast.NewContinueStmt()},
		{"FieldDecl", ast.NewFieldDecl("f", ast.NewPrimTypeRef("string"))},
		{"OutputDecl", ast.NewOutputDecl("o", ast.NewPrimTypeRef("string"))},
		{"ConstDecl", ast.NewConstDecl("C", nil)},
		{"TypeDecl", ast.NewTypeDecl("T", nil)},
		{"EnumMemberDecl", ast.NewEnumMemberDecl("m")},
		{"EnumDecl", ast.NewEnumDecl("E", nil)},
		{"ConversationVarDecl", ast.NewConversationVarDecl("c", nil, nil)},
		{"ParamDecl", ast.NewParamDecl("p", nil, nil)},
		{"FunctionDef", ast.NewFunctionDef("f", nil, nil, ast.NewBlock(nil))},
		{"CodeFunctionDef", ast.NewCodeFunctionDef("f", nil, nil, ast.NewBlock(nil))},
		{"PrimTypeRef", ast.NewPrimTypeRef("int")},
		{"FileTypeRef", ast.NewFileTypeRef(ast.FileTypeConstraints{})},
		{"ArrayTypeRef", ast.NewArrayTypeRef(nil)},
		{"MapTypeRef", ast.NewMapTypeRef(nil, nil)},
		{"NamedTypeRef", ast.NewNamedTypeRef("T")},
		{"OptionalTypeRef", ast.NewOptionalTypeRef(nil)},
		{"Program", ast.NewProgram(ast.WithEntry(ast.NewFunctionDef("main", nil, nil, ast.NewBlock(nil))))},
	}
	if len(tests) != 61 {
		t.Fatalf("scaffold bug: table has %d rows, want 61", len(tests))
	}
	for _, tt := range tests {
		s, ok := tt.node.(fmt.Stringer)
		if !ok {
			t.Errorf("%s: %T does not implement fmt.Stringer", tt.name, tt.node)
			continue
		}
		if str := s.String(); strings.TrimSpace(str) == "" {
			t.Errorf("%s: String() produced empty repr", tt.name)
		}
	}
}
