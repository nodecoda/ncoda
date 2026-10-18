package passes

// Task 24a const-substitution tests, ported from
// stmt_translator.py:_substitute_constants (stmt_translator.py:230-363).
// The reference mutates ProcAST dataclasses in place; the Go port REBUILDS
// every touched node (immutable SourceAST) and shares the const value node
// across substitution sites, so two references to the same const land on
// the SAME node (identical NodeID). Parity caveat: Python _copy_literal
// (stmt_translator.py:207-226) mints a fresh node per site for scalar
// literals and only deepcopy (node_id-preserving) falls back for composite
// values such as TemplateString — the shared-pointer assertions below are
// therefore Go behavior, exact node_id parity for composite const values
// only (see const_substitution.go header).
//
// RED: before the pass existed, a const reference died in the scope phase
// with E1001 "Undefined variable" (probe evidence, 2026-08-13). The
// integration case below locks the fixed behavior; the unit cases lock the
// substitution mechanics (coverage, sharing, spans, no-op).

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/parser"
)

func parseProgramOrFail(t *testing.T, src string) *ast.Program {
	t.Helper()
	prog, perrs := parser.ParseSource(src, "t.dc")
	if len(perrs) > 0 {
		t.Fatalf("parse errors: %v", perrs)
	}
	return prog
}

// identCount counts IdentExpr nodes named want reachable from root via
// ast.Walk (the exhaustive traverser).
func identCount(root ast.Node, want string) int {
	v := &identCounter{name: want}
	ast.Walk(v, root)
	return v.count
}

type identCounter struct {
	name  string
	count int
}

func (v *identCounter) Visit(n ast.Node) ast.Visitor {
	if ident, ok := n.(*ast.IdentExpr); ok && ident.Name() == v.name {
		v.count++
	}
	return v
}

// ── 1. RED integration: const source compiles end-to-end ───────────

func TestConstSubstitutionIntegrationFixesUndefinedVariable(t *testing.T) {
	src := `
@mode workflow
const MODEL = "openai_api_compatible/gpt-5.4";
function main() {
    let response = llm(MODEL, {"messages": [{"role": "user", "content": "Hello"}]});
    return response.text;
}
`
	res := DefaultPipeline().Run(src, "t.dc", nil)
	if res.HasErrors() {
		t.Fatalf("const-using source must compile after substitution, got: %v", res.Diagnostics)
	}
	if res.Program == nil {
		t.Fatal("frontend produced no TypedBoundProgram")
	}
}

// ── 2. No consts: zero-op (same pointers, nothing rebuilt) ─────────

func TestSubstituteConstantsNoConstsNoop(t *testing.T) {
	prog := parseProgramOrFail(t, `
function main() {
    let x = llm("gpt4", {"messages": [{"role": "user", "content": "Hi"}]});
    return x.text;
}
`)
	entryBefore := prog.Entry()
	funcsBefore := prog.Functions()
	codesBefore := prog.CodeFunctions()
	entryBodyBefore := prog.Entry().Body()

	SubstituteConstants(prog)

	if prog.Entry() != entryBefore {
		t.Fatal("entry rebuilt with no const declarations")
	}
	if prog.Entry().Body() != entryBodyBefore {
		t.Fatal("entry body rebuilt with no const declarations")
	}
	if len(prog.Functions()) != len(funcsBefore) {
		t.Fatalf("functions = %d, want %d", len(prog.Functions()), len(funcsBefore))
	}
	for i := range funcsBefore {
		if prog.Functions()[i] != funcsBefore[i] {
			t.Fatalf("function %d rebuilt with no const declarations", i)
		}
	}
	if len(prog.CodeFunctions()) != len(codesBefore) {
		t.Fatalf("code functions = %d, want %d", len(prog.CodeFunctions()), len(codesBefore))
	}
	for i := range codesBefore {
		if prog.CodeFunctions()[i] != codesBefore[i] {
			t.Fatalf("code function %d rebuilt with no const declarations", i)
		}
	}
}

// ── 3. Coverage: entry + functions + code functions (Python covers all
// three: `for fn in [*program.functions, *program.code_functions]` plus
// `program.entry`, stmt_translator.py:385-390). ─────────────────────

func TestSubstituteConstantsCoversEntryFunctionsCodeFunctions(t *testing.T) {
	prog := parseProgramOrFail(t, `
@mode workflow
const MODEL = "openai_api_compatible/gpt-5.4";
code helper() -> string {
    return MODEL;
}
function getModel() -> string {
    return MODEL;
}
function main() {
    let m = MODEL;
    return m;
}
`)
	SubstituteConstants(prog)

	if identCount(prog.Entry().Body(), "MODEL") != 0 {
		t.Error("entry body still references MODEL")
	}
	if identCount(prog.Functions()[0].Body(), "MODEL") != 0 {
		t.Error("function body still references MODEL")
	}
	if identCount(prog.CodeFunctions()[0].Body(), "MODEL") != 0 {
		t.Error("code function body still references MODEL")
	}
	// The substituted value is present at each site.
	for _, body := range []*ast.Block{prog.Entry().Body(), prog.Functions()[0].Body(), prog.CodeFunctions()[0].Body()} {
		found := false
		ast.Walk(&stringLitProbe{want: "openai_api_compatible/gpt-5.4", found: &found}, body)
		if !found {
			t.Error("substituted MODEL literal missing from a body")
		}
	}
}

type stringLitProbe struct {
	want  string
	found *bool
}

func (p *stringLitProbe) Visit(n ast.Node) ast.Visitor {
	if lit, ok := n.(*ast.StringLit); ok && lit.Value() == p.want {
		*p.found = true
	}
	return p
}

// ── 4. Shared node: two references to one const land on the SAME node
// (Go shares the const value pointer; Python _copy_literal
// stmt_translator.py:207-226 creates a fresh node per site for scalars and
// only its deepcopy fallback preserves node_id for composite values);
// different consts stay distinct. ────────────────────────────────────

func TestSubstituteConstantsSharedNodePerConst(t *testing.T) {
	prog := parseProgramOrFail(t, `
@mode workflow
const A = "alpha";
const B = "beta";
function main() {
    string x = A;
    string y = A;
    string z = B;
    return x;
}
`)
	SubstituteConstants(prog)

	stmts := prog.Entry().Body().Stmts()
	xInit := stmts[0].(*ast.VarDeclStmt).Init()
	yInit := stmts[1].(*ast.VarDeclStmt).Init()
	zInit := stmts[2].(*ast.VarDeclStmt).Init()

	xLit, ok := xInit.(*ast.StringLit)
	if !ok || xLit.Value() != "alpha" {
		t.Fatalf("x init = %T %v, want StringLit(alpha)", xInit, xInit)
	}
	yLit, ok := yInit.(*ast.StringLit)
	if !ok || yLit.Value() != "alpha" {
		t.Fatalf("y init = %T %v, want StringLit(alpha)", yInit, yInit)
	}
	if xLit.NodeID() != yLit.NodeID() {
		t.Errorf("two references to const A must share one node (NodeID %d != %d)", xLit.NodeID(), yLit.NodeID())
	}
	zLit, ok := zInit.(*ast.StringLit)
	if !ok || zLit.Value() != "beta" {
		t.Fatalf("z init = %T %v, want StringLit(beta)", zInit, zInit)
	}
	if zLit.NodeID() == xLit.NodeID() {
		t.Error("const B must not share the node of const A")
	}
}

// ── 5. Spans: rebuilt containers keep their original spans; the
// substituted literal keeps the const declaration value's span. ──────

func TestSubstituteConstantsPreservesSpans(t *testing.T) {
	prog := parseProgramOrFail(t, `
@mode workflow
const MODEL = "m";
function main() {
    if (MODEL) { string x = MODEL; }
}
`)
	origIf := prog.Entry().Body().Stmts()[0].(*ast.IfStmt)
	origSpan := origIf.Span()
	constValueSpan := prog.ConstDecls()[0].Value().Span()

	SubstituteConstants(prog)

	newIf := prog.Entry().Body().Stmts()[0].(*ast.IfStmt)
	if newIf == origIf {
		t.Fatal("if statement must be rebuilt")
	}
	if newIf.Span() != origSpan {
		t.Errorf("rebuilt if span = %+v, want %+v", newIf.Span(), origSpan)
	}
	inner := newIf.ThenBody().Stmts()[0].(*ast.VarDeclStmt)
	lit := inner.Init().(*ast.StringLit)
	if lit.Span() != constValueSpan {
		t.Errorf("substituted literal span = %+v, want const value span %+v", lit.Span(), constValueSpan)
	}
}

// TestSubstituteConstantsTemplateConstKeepsValueSpan: a TemplateString const
// value (the reference's deepcopy fallback — node_id-preserving, so Go's
// shared pointer IS the node_id equivalent here) keeps its parsed non-zero
// span when substituted into a body; the shared node is the const value
// node itself.
func TestSubstituteConstantsTemplateConstKeepsValueSpan(t *testing.T) {
	prog := parseProgramOrFail(t, `
@mode workflow
const T = `+"`pre-${x}-post`"+`;
function main() {
    string s = T;
    return s;
}
`)
	constValue := prog.ConstDecls()[0].Value().(*ast.TemplateString)
	if constValue.Span() == (ast.Span{}) {
		t.Fatal("parsed const value must carry a non-zero span")
	}
	constValueNodeID := constValue.NodeID()

	SubstituteConstants(prog)

	init := prog.Entry().Body().Stmts()[0].(*ast.VarDeclStmt).Init()
	subbed, ok := init.(*ast.TemplateString)
	if !ok {
		t.Fatalf("substituted init = %T, want *ast.TemplateString", init)
	}
	if subbed != constValue {
		t.Fatal("substituted site must share the const value node (composite deepcopy parity)")
	}
	if subbed.NodeID() != constValueNodeID {
		t.Fatal("shared node must keep its original NodeID")
	}
	if subbed.Span() != constValue.Span() {
		t.Errorf("substituted template span = %+v, want const value span %+v", subbed.Span(), constValue.Span())
	}
}

// ── 6. Exhaustive shape coverage: every _sub_expr/_sub_stmt branch
// (stmt_translator.py:253-353) substitutes const references in a
// programmatically-built tree; const names must not survive anywhere. ──

func TestSubstituteConstantsNestedShapes(t *testing.T) {
	constA := ast.NewStringLit("a")
	constN := ast.NewIntLit(7)
	prog := ast.NewProgram(
		ast.WithConstDecls([]*ast.ConstDecl{
			ast.NewConstDecl("A", constA),
			ast.NewConstDecl("N", constN),
		}),
		ast.WithEntry(ast.NewFunctionDef("main", nil, nil, buildNestedBlock())),
	)

	SubstituteConstants(prog)

	// 键位是**名字位**（标签）：代入只改值、不改键 —— 完整判据 =
	// TestSubstituteConstantsDoesNotRewriteMapKeyLabel（本文件），这里只做
	// 覆盖账：buildNestedBlock 的 MapLit 是 `{ A: N }`，所以唯一合法存活的名字
	// 是键上的那一个 `A`。
	if got := identCount(prog.Entry().Body(), "A"); got != 1 {
		t.Errorf("only the map-key label may keep the const name: got %d occurrence(s)", got)
	}
	if got := identCount(prog.Entry().Body(), "N"); got != 0 {
		t.Errorf("const N references survive: %d", got)
	}
	maps := collectMapLits(prog.Entry().Body())
	if len(maps) != 1 || len(maps[0].Entries()) != 1 {
		t.Fatalf("want exactly one single-entry map literal, got %d map(s)", len(maps))
	}
	if k, ok := maps[0].Entries()[0].Key.(*ast.IdentExpr); !ok || k.Name() != "A" {
		t.Errorf("map key must stay the label ident A, got %T", maps[0].Entries()[0].Key)
	}
	if _, ok := maps[0].Entries()[0].Value.(*ast.IntLit); !ok {
		t.Errorf("map value must be substituted to the const literal (N), got %T",
			maps[0].Entries()[0].Value)
	}
}

// buildNestedBlock builds one body exercising every expression and
// statement branch of the reference substitution walker.
func buildNestedBlock() *ast.Block {
	ident := func(name string) ast.Expr { return ast.NewIdentExpr(name) }
	block := func(stmts ...ast.Stmt) *ast.Block { return ast.NewBlock(stmts) }
	// Statement-shape locals (python-style function-local closures).
	stmts := []ast.Stmt{
		// VarDeclStmt.init
		ast.NewVarDeclStmt("s1", nil, ident("A"), true),
		// AssignStmt target + value
		ast.NewAssignStmt(ident("s1"), ident("A"), "set"),
		// ExprStmt.expr (binary with both const refs)
		ast.NewExprStmt(ast.NewBinaryExpr("+", ident("A"), ident("N"))),
		// ReturnStmt.value
		ast.NewReturnStmt(ident("A")),
		// IfStmt condition + then + elif + else
		ast.NewIfStmt(ast.NewUnaryExpr("!", ident("A")),
			block(ast.NewVarDeclStmt("s2", nil, ident("N"), true)),
			[]*ast.ElseIfBranch{ast.NewElseIfBranch(ident("N"), block(ast.NewVarDeclStmt("s3", nil, ident("A"), true)))},
			block(ast.NewVarDeclStmt("s4", nil, ident("A"), true))),
		// ForStmt iterable + body
		ast.NewForStmt("i", nil, ast.NewArrayLit([]ast.Expr{ident("N")}),
			block(ast.NewVarDeclStmt("s5", nil, ident("A"), true))),
		// WhileStmt condition + body
		ast.NewWhileStmt(ident("A"), 0, block(ast.NewVarDeclStmt("s6", nil, ident("N"), true))),
		// OutputStmt.value
		ast.NewOutputStmt(ident("A")),
		// YieldStmt.value
		ast.NewYieldStmt(ident("A")),
		// ParallelStmt branches
		ast.NewParallelStmt([]*ast.Block{
			block(ast.NewVarDeclStmt("p1", nil, ident("A"), true)),
			block(ast.NewVarDeclStmt("p2", nil, ident("N"), true)),
		}),
		// AttemptStmt operation + success + failure
		ast.NewAttemptStmt(ident("N"), nil, block(ast.NewVarDeclStmt("a1", nil, ident("A"), true)), nil,
			block(ast.NewVarDeclStmt("a2", nil, ident("A"), true))),
		// TernaryExpr all three arms
		ast.NewVarDeclStmt("s7", nil, ast.NewTernaryExpr(ident("A"), ident("N"), ident("A")), true),
		// CallExpr func + args
		ast.NewVarDeclStmt("s8", nil, ast.NewCallExpr(ident("A"), nil, []ast.Expr{ident("N"), ident("A")}), true),
		// ArrayLit elements
		ast.NewVarDeclStmt("s9", nil, ast.NewArrayLit([]ast.Expr{ident("N"), ident("N")}), true),
		// MapLit key + value
		ast.NewVarDeclStmt("s10", nil, ast.NewMapLit([]ast.MapEntry{{Key: ident("A"), Value: ident("N")}}), true),
		// TemplateString expr parts
		ast.NewVarDeclStmt("s11", nil, ast.NewTemplateString([]ast.TemplatePart{
			{Text: "pre-", Expr: ident("A")}, {Text: "-", Expr: ident("N")}, {Text: "-post"},
		}), true),
		// LambdaExpr body
		ast.NewVarDeclStmt("s12", nil, ast.NewLambdaExpr([]string{"v"}, ident("A")), true),
		// ForExpr iterable + body block
		ast.NewVarDeclStmt("s13", nil, ast.NewForExpr("i", nil, ident("N"),
			block(ast.NewVarDeclStmt("s14", nil, ident("A"), true))), true),
		// ForeignCodeExpr inputs.value + source template parts
		ast.NewVarDeclStmt("s17", nil, ast.NewForeignCodeExpr("python3",
			[]*ast.CodeInputBinding{ast.NewCodeInputBinding("v", nil, ident("A"))},
			ast.NewCodeDirectOutput(nil),
			ast.NewTemplateString([]ast.TemplatePart{{Text: "src-", Expr: ident("N")}, {Text: "-end"}})), true),
		// OperationPolicyExpr operation + DefaultPolicy.value
		ast.NewVarDeclStmt("s18", nil, ast.NewOperationPolicyExpr(ident("N"),
			[]ast.OperationPolicy{ast.NewDefaultPolicy(ident("A"))}), true),
	}
	return block(stmts...)
}

// collectMapLits returns every *ast.MapLit reachable from root (ast.Walk is
// the exhaustive traverser).
func collectMapLits(root ast.Node) []*ast.MapLit {
	c := &mapLitCollector{}
	ast.Walk(c, root)
	return c.out
}

type mapLitCollector struct{ out []*ast.MapLit }

func (c *mapLitCollector) Visit(n ast.Node) ast.Visitor {
	if m, ok := n.(*ast.MapLit); ok {
		c.out = append(c.out, m)
	}
	return c
}

// TestSubstituteConstantsDoesNotRewriteMapKeyLabel — 键位 = **名字位**，
// 常量代入只改值、不改键（SG-4 根因面；方向 = 已裁「甲：键是标签，不做常量折叠」）。
//
// RED（修前一手，ncc 实测 2026-09-27）：`const K = "a"; let m = { K: 1 };`
// 的键被代入成 `"a"` ⇒ `m.K` 报 FIELD_NOT_FOUND、`m.a` 反而可读 —— 同一拼写
// `{ K: 1 }` 的键随「作用域里有没有同名 const」变化（静默改写作者写的键名）。
//
// GREEN：键保持标签 `K`，值侧照常代入。权威 = ast.StaticMapKey 的单点规则
// （"a *label*, not a reference"）+ LANGUAGE-REFERENCE §1.6（名字位是词法
// 规则、全位置一致）+ 同族先例 = template `using {…}` 只代入值（TemplateValue
// 分支）。
func TestSubstituteConstantsDoesNotRewriteMapKeyLabel(t *testing.T) {
	src := `
@mode workflow
const K = "a";
const V = 7;
function main() -> int {
    let m = { K: V };
    return m.K;
}
`
	prog := parseProgramOrFail(t, src)
	SubstituteConstants(prog)

	maps := collectMapLits(prog.Entry().Body())
	if len(maps) != 1 || len(maps[0].Entries()) != 1 {
		t.Fatalf("want exactly one single-entry map literal, got %d map(s)", len(maps))
	}
	entry := maps[0].Entries()[0]
	key, ok := entry.Key.(*ast.IdentExpr)
	if !ok || key.Name() != "K" {
		t.Errorf("map key must stay the label K, got %T — a const in scope must not "+
			"rewrite the key (same spelling must mean the same key either way)", entry.Key)
	}
	if _, ok := entry.Value.(*ast.IntLit); !ok {
		t.Errorf("map value must be substituted to the const literal (V=7), got %T", entry.Value)
	}
	// 键上的 K 是唯一合法存活者：值位/字段访问位一律代入。
	if got := identCount(prog.Entry().Body(), "K"); got != 1 {
		t.Errorf("const K may survive only as the key label: got %d occurrence(s)", got)
	}
	if got := identCount(prog.Entry().Body(), "V"); got != 0 {
		t.Errorf("const V references survive in the value position: %d", got)
	}
}

// ── 7. I1: scope-aware substitution — local declarations shadow consts ─

// The reference substituted const references by NAME only, so a local
// variable shadowing a const was replaced by the const value
// (`const x = "a"; function main() { let x = "b"; return x; }` returned
// "a"). The parity oracle is retired; substitution now mirrors the
// binder's scope model: an identifier that resolves to a local binding is
// never substituted.

// A let/var declaration shadows a const of the same name from its
// declaration point onward in the same block (binder define order): the
// return below must stay an IdentExpr, not the const literal.
func TestSubstituteConstantsLocalShadowing(t *testing.T) {
	prog := parseProgramOrFail(t, `
@mode workflow
const MODEL = "const-value";
function main() {
    let MODEL = "local-value";
    return MODEL;
}
`)
	SubstituteConstants(prog)
	stmts := prog.Entry().Body().Stmts()
	ret := stmts[1].(*ast.ReturnStmt)
	if _, ok := ret.Value().(*ast.IdentExpr); !ok {
		t.Fatalf("shadowed return value = %T %v, want IdentExpr(MODEL)", ret.Value(), ret.Value())
	}
	if identCount(prog.Entry().Body(), "MODEL") != 1 {
		t.Errorf("MODEL idents = %d, want exactly the local reference (1)", identCount(prog.Entry().Body(), "MODEL"))
	}
}

// A reference BEFORE the declaration in the same block still resolves to
// the const (the binder defines let/var names at the statement walk
// point): the first init is substituted, the return is not.
func TestSubstituteConstantsShadowingIsStatementOrdered(t *testing.T) {
	prog := parseProgramOrFail(t, `
@mode workflow
const MODEL = "const-value";
function main() {
    let before = MODEL;
    let MODEL = "local-value";
    return MODEL;
}
`)
	SubstituteConstants(prog)
	stmts := prog.Entry().Body().Stmts()
	first := stmts[0].(*ast.VarDeclStmt).Init()
	if lit, ok := first.(*ast.StringLit); !ok || lit.Value() != "const-value" {
		t.Fatalf("pre-declaration reference = %T %v, want StringLit(const-value)", first, first)
	}
	ret := stmts[2].(*ast.ReturnStmt)
	if _, ok := ret.Value().(*ast.IdentExpr); !ok {
		t.Fatalf("shadowed return value = %T %v, want IdentExpr(MODEL)", ret.Value(), ret.Value())
	}
}

// Function parameters shadow consts across the whole body.
func TestSubstituteConstantsParameterShadowing(t *testing.T) {
	prog := parseProgramOrFail(t, `
@mode workflow
const MODEL = "const-value";
function main(string MODEL) {
    return MODEL;
}
`)
	SubstituteConstants(prog)
	ret := prog.Entry().Body().Stmts()[0].(*ast.ReturnStmt)
	if _, ok := ret.Value().(*ast.IdentExpr); !ok {
		t.Fatalf("parameter-shadowed return = %T %v, want IdentExpr(MODEL)", ret.Value(), ret.Value())
	}
}

// A block-local declaration (if branch, loop body, lambda body) shadows
// only inside its block: the enclosing reference is still substituted.
func TestSubstituteConstantsBlockLocalShadowingDoesNotLeak(t *testing.T) {
	prog := parseProgramOrFail(t, `
@mode workflow
const MODEL = "const-value";
function main() {
    if (true) { let MODEL = "local-value"; }
    return MODEL;
}
`)
	SubstituteConstants(prog)
	// The branch-local declaration keeps its literal init (block scope is
	// entered with a shadow copy; the enclosing reference is not affected)...
	ifBody := prog.Entry().Body().Stmts()[0].(*ast.IfStmt).ThenBody()
	local := ifBody.Stmts()[0].(*ast.VarDeclStmt).Init()
	if lit, ok := local.(*ast.StringLit); !ok || lit.Value() != "local-value" {
		t.Fatalf("branch-local init = %T %v, want StringLit(local-value)", local, local)
	}
	// ...and the post-block return is still substituted (no leak).
	ret := prog.Entry().Body().Stmts()[1].(*ast.ReturnStmt)
	if lit, ok := ret.Value().(*ast.StringLit); !ok || lit.Value() != "const-value" {
		t.Fatalf("post-block return = %T %v, want StringLit(const-value)", ret.Value(), ret.Value())
	}
}

// Loop variables shadow consts inside the loop body; the iterable is
// evaluated in the enclosing scope and is still substituted.
func TestSubstituteConstantsLoopVariableShadowing(t *testing.T) {
	prog := parseProgramOrFail(t, `
@mode workflow
const MODEL = "const-value";
function main() {
    for (var MODEL in [MODEL]) {
        return MODEL;
    }
    return "ok";
}
`)
	SubstituteConstants(prog)
	forStmt := prog.Entry().Body().Stmts()[0].(*ast.ForStmt)
	// Iterable element: substituted (outer scope).
	if lit, ok := forStmt.Iterable().(*ast.ArrayLit); ok {
		elem := lit.Elements()[0]
		if s, ok := elem.(*ast.StringLit); !ok || s.Value() != "const-value" {
			t.Fatalf("iterable element = %T %v, want StringLit(const-value)", elem, elem)
		}
	} else {
		t.Fatalf("iterable = %T, want *ast.ArrayLit", forStmt.Iterable())
	}
	// Body return: loop variable shadows — stays an identifier.
	ret := forStmt.Body().Stmts()[0].(*ast.ReturnStmt)
	if _, ok := ret.Value().(*ast.IdentExpr); !ok {
		t.Fatalf("loop-body return = %T %v, want IdentExpr(MODEL)", ret.Value(), ret.Value())
	}
}

// Lambda parameters shadow consts inside the lambda body only.
func TestSubstituteConstantsLambdaParameterShadowing(t *testing.T) {
	constA := ast.NewStringLit("a")
	prog := ast.NewProgram(
		ast.WithConstDecls([]*ast.ConstDecl{ast.NewConstDecl("A", constA)}),
		ast.WithEntry(ast.NewFunctionDef("main", nil, nil, ast.NewBlock([]ast.Stmt{
			ast.NewVarDeclStmt("f", nil, ast.NewLambdaExpr([]string{"A"}, ast.NewIdentExpr("A")), true),
			ast.NewReturnStmt(ast.NewCallExpr(ast.NewIdentExpr("f"), nil, nil)),
		}))),
	)
	SubstituteConstants(prog)
	init := prog.Entry().Body().Stmts()[0].(*ast.VarDeclStmt).Init().(*ast.LambdaExpr)
	body, ok := init.Body().(*ast.IdentExpr)
	if !ok {
		t.Fatalf("lambda body = %T %v, want IdentExpr(A)", init.Body(), init.Body())
	}
	if body.Name() != "A" {
		t.Fatalf("lambda body name = %q, want A", body.Name())
	}
}

// Parallel-branch declarations are exported to the enclosing scope at the
// join (binder parallel export), so a post-parallel reference to an
// exported name is not substituted.
func TestSubstituteConstantsParallelExportShadowing(t *testing.T) {
	constA := ast.NewStringLit("a")
	prog := ast.NewProgram(
		ast.WithConstDecls([]*ast.ConstDecl{ast.NewConstDecl("A", constA)}),
		ast.WithEntry(ast.NewFunctionDef("main", nil, nil, ast.NewBlock([]ast.Stmt{
			ast.NewParallelStmt([]*ast.Block{
				ast.NewBlock([]ast.Stmt{ast.NewVarDeclStmt("A", nil, ast.NewIntLit(1), true)}),
				ast.NewBlock([]ast.Stmt{}),
			}),
			ast.NewReturnStmt(ast.NewIdentExpr("A")),
		}))),
	)
	SubstituteConstants(prog)
	ret := prog.Entry().Body().Stmts()[1].(*ast.ReturnStmt)
	if _, ok := ret.Value().(*ast.IdentExpr); !ok {
		t.Fatalf("post-parallel return = %T %v, want IdentExpr(A) (exported, not const)", ret.Value(), ret.Value())
	}
}

// ── 8. I1 integration: shadowing source compiles end-to-end and the
// frontend program keeps the local reference (not the const literal). ──

func TestConstSubstitutionShadowingEndToEnd(t *testing.T) {
	src := `
@mode workflow
const MODEL = "const-value";
function main() {
    let MODEL = "local-value";
    return MODEL;
}
`
	res := DefaultPipeline().Run(src, "t.dc", nil)
	if res.HasErrors() {
		t.Fatalf("shadowing source must compile, got: %v", res.Diagnostics)
	}
	if res.Program == nil {
		t.Fatal("frontend produced no TypedBoundProgram")
	}
	entry := res.Program.Program.Entry()
	stmts := entry.Body().Stmts()
	ret := stmts[len(stmts)-1].(*ast.ReturnStmt)
	if _, ok := ret.Value().(*ast.IdentExpr); !ok {
		t.Fatalf("frontend return value = %T %v, want the local variable reference", ret.Value(), ret.Value())
	}
}
