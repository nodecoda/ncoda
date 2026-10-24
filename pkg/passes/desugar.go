package passes

// Desugar pass, mirroring src/nclang/lang/passes/desugar.py (read-only
// oracle). Runs BEFORE scope/type/syntax/semantic passes.
//
// Transformations (semantics-preserving):
//  1. elif → nested if/else (ElseIfBranch chains collapse into nested
//     IfStmt/Block wrappers in the outer if's else body);
//  2. for normalization (recurse into the body). For→while conversion is
//     deliberately NOT done: the reference target's IterationNode is a first-class
//     construct, the translator maps ForStmt → IterationNode, and
//     converting would lose the iteration semantics (desugar.py:118-129).
//
// Rewrite strategy (rebuild, not mutate): the SourceAST is immutable by
// convention — node fields are unexported, values are assigned through
// constructors and read back through getters; SetSpan is the only setter
// (node.go). The reference mutates ProcAST dataclasses in place; the Go
// port REBUILDS every touched node and hands the rebuilt FunctionDefs back
// to the original *ast.Program through the exported ProgramOption
// functions (WithEntry/WithFunctions applied post-construction). That keeps
// the same *ast.Program pointer the pipeline binds at StageScope, so the
// binder enumerates the REWRITTEN tree. AstNodeIDs are fresh 1-based
// traversal ordinals (Python AstNodeId semantics), so node identity is
// irrelevant — tree structure is what must match, and rebuild preserves the
// reference's post-desugar structure exactly.
//
// Span preservation: every rebuilt node copies the original node's Span via
// SetSpan (Python preserves spans by in-place mutation; Go copies them
// explicitly). Without this, binder NodeInfo spans and the binder's
// function-sort spanKey would degrade to the zero span.

// S107-family deviation (2026-09-08, deliberate, documented): the Python
// oracle does NOT recurse into expression-embedded loop bodies (a
// ForExpr/ParallelForExpr inside a VarDecl init / assign RHS) nor into
// Attempt/Switch/RequestInput branch bodies, so elif chains there are never
// flattened and the IR builder's emitIf (which consumes ElseBody only)
// silently drops every middle arm. The Go port intentionally EXTENDS the
// traversal to close the invariant "canonical AST has zero ElseIfs before
// IR build" (see desugarStmt / desugarExpr below). Keep this note when
// re-syncing with desugar.py: this is a fix, not a porting error.

import "github.com/nodecoda/ncoda/pkg/ast"

// DesugarPass expands syntactic sugar before semantic analysis, mirroring
// desugar.py DesugarPass.run. Only the entry function and the regular
// function list are rewritten (desugar.py:33-39); code functions are
// intentionally untouched, matching the reference.
type DesugarPass struct{}

// Run desugars the entry body and every function body in place (through
// the program's exported option setters). Emits no diagnostics.
func (p *DesugarPass) Run(program *ast.Program, ctx *Context) {
	rewriteProgramBodies(program, desugarBlock)
}

// rewriteProgramBodies rewrites the entry body and each function body with
// the given block rewrite, rebuilding the affected FunctionDefs and
// replacing them on the original Program via its exported option functions.
// It mirrors the shared shape of desugar.py run() and normalize.py run()
// (both touch exactly entry + functions).
func rewriteProgramBodies(program *ast.Program, rewrite func(*ast.Block) *ast.Block) {
	if e := program.Entry(); e != nil {
		ast.WithEntry(rebuildFunction(e, rewrite))(program)
	}
	funcs := program.Functions()
	if len(funcs) == 0 {
		return
	}
	rebuilt := make([]*ast.FunctionDef, len(funcs))
	for i, fn := range funcs {
		rebuilt[i] = rebuildFunction(fn, rewrite)
	}
	ast.WithFunctions(rebuilt)(program)
}

// rebuildFunction returns a new FunctionDef with the rewritten body and the
// original span. The parser only ever puts plain FunctionDefs into
// Program.Functions/Entry (code functions live in a separate
// []*CodeFunctionDef list), so no CodeFunctionDef case is needed here —
// matching the reference, which only iterates program.functions/entry.
func rebuildFunction(fn *ast.FunctionDef, rewrite func(*ast.Block) *ast.Block) *ast.FunctionDef {
	nb := ast.NewFunctionDef(fn.Name(), fn.Params(), fn.ReturnType(), rewrite(fn.Body()))
	nb.SetSpan(fn.Span())
	return nb
}

// desugarBlock recursively desugars every statement in a block and returns
// a rebuilt Block with the original span (desugar.py _desugar_block).
func desugarBlock(block *ast.Block) *ast.Block {
	stmts := block.Stmts()
	desugared := make([]ast.Stmt, 0, len(stmts))
	for _, stmt := range stmts {
		desugared = append(desugared, desugarStmt(stmt))
	}
	nb := ast.NewBlock(desugared)
	nb.SetSpan(block.Span())
	return nb
}

// desugarStmt desugars a single statement, returning one statement
// (desugar.py _desugar_stmt returns Stmt | list[Stmt]; the list branch is
// unreachable today — every transformation yields exactly one statement, so
// the Go port returns a single Stmt).
func desugarStmt(stmt ast.Stmt) ast.Stmt {
	switch s := stmt.(type) {
	case *ast.IfStmt:
		return desugarIf(s)
	case *ast.ForStmt:
		return desugarFor(s)
	case *ast.WhileStmt:
		nb := ast.NewWhileStmt(s.Condition(), s.Limit(), desugarBlock(s.Body()))
		nb.SetSpan(s.Span())
		return nb
	case *ast.Block:
		return desugarBlock(s)
	case *ast.ParallelStmt:
		branches := make([]*ast.Block, len(s.Branches()))
		for i, br := range s.Branches() {
			branches[i] = desugarBlock(br)
		}
		nb := ast.NewParallelStmt(branches, s.BranchNames()...)
		nb.SetSpan(s.Span())
		return nb
	// ── S107 族容器闭包（2026-09-08）：表达式值位 + 分支体容器 ──
	// 以下容器此前落入 default 原样返回，其内嵌 if 的 elif 链从未被展平，
	// IR emitIf 只读 ElseBody → 中臂静默丢失。containsLoopExpr/表达式重建
	// 的快速路径保证无 loop-expr 的子树保持指针同一性（零行为变化）。
	case *ast.VarDeclStmt:
		return desugarVarDeclStmt(s)
	case *ast.AssignStmt:
		if !containsLoopExpr(s.Value()) && !containsLoopExpr(s.Target()) {
			return stmt
		}
		nb := ast.NewAssignStmt(desugarExpr(s.Target()), desugarExpr(s.Value()), s.Operation())
		nb.SetSpan(s.Span())
		return nb
	case *ast.ExprStmt:
		if !containsLoopExpr(s.Expr()) {
			return stmt
		}
		nb := ast.NewExprStmt(desugarExpr(s.Expr()))
		nb.SetSpan(s.Span())
		return nb
	case *ast.ReturnStmt:
		if s.Value() == nil || !containsLoopExpr(s.Value()) {
			return stmt
		}
		nb := ast.NewReturnStmt(desugarExpr(s.Value()), s.Streaming())
		nb.SetSpan(s.Span())
		return nb
	case *ast.YieldStmt:
		if s.Value() == nil || !containsLoopExpr(s.Value()) {
			return stmt
		}
		var nb *ast.YieldStmt
		if s.Port() == "" {
			nb = ast.NewYieldStmt(desugarExpr(s.Value()))
		} else {
			nb = ast.NewNamedYieldStmt(s.Port(), desugarExpr(s.Value()))
		}
		nb.SetSpan(s.Span())
		return nb
	case *ast.OutputStmt:
		if s.Value() == nil || !containsLoopExpr(s.Value()) {
			return stmt
		}
		nb := ast.NewOutputStmt(desugarExpr(s.Value()), s.Streaming())
		nb.SetSpan(s.Span())
		return nb
	case *ast.AttemptStmt:
		nb := ast.NewAttemptStmt(
			desugarExpr(s.Operation()),
			desugarVarDeclStmt(s.ResultBinding()),
			desugarBlock(s.SuccessBody()),
			desugarVarDeclStmt(s.ErrorBinding()),
			desugarBlock(s.FailureBody()))
		nb.SetSpan(s.Span())
		return nb
	case *ast.SwitchStmt:
		branches := make([]*ast.SwitchBranch, len(s.Branches()))
		for i, br := range s.Branches() {
			nb := ast.NewSwitchBranch(br.Label(), desugarBlock(br.Body()))
			nb.SetSpan(br.Span())
			branches[i] = nb
		}
		var defBody *ast.Block
		if s.DefaultBody() != nil {
			defBody = desugarBlock(s.DefaultBody())
		}
		cfg := desugarExpr(s.Config())
		mcfg, _ := cfg.(*ast.MapLit)
		nb := ast.NewSwitchStmt(desugarExpr(s.Query()), mcfg, branches, defBody, desugarVarDeclStmt(s.Result()))
		nb.SetSpan(s.Span())
		return nb
	case *ast.RequestInputStmt:
		actionBranches := make([]*ast.HumanActionBranch, len(s.ActionBranches()))
		for i, br := range s.ActionBranches() {
			nb := ast.NewHumanActionBranch(desugarExpr(br.ActionExpr()), desugarBlock(br.Body()))
			nb.SetSpan(br.Span())
			actionBranches[i] = nb
		}
		cfg := desugarExpr(s.Config())
		mcfg, _ := cfg.(*ast.MapLit)
		nb := ast.NewRequestInputStmt(s.FormType(), s.ActionType(), mcfg, desugarVarDeclStmt(s.ResultBinding()), actionBranches, desugarBlock(s.TimeoutBody()))
		nb.SetSpan(s.Span())
		return nb
	default:
		// BreakStmt / ContinueStmt — 无子语句，无需 desugar。
		return stmt
	}
}

// desugarVarDeclStmt rebuilds a variable declaration whose initializer holds
// a loop expression; otherwise returns the original node unchanged (used for
// statement-level declarations and attempt/switch/request-input bindings).
func desugarVarDeclStmt(s *ast.VarDeclStmt) *ast.VarDeclStmt {
	if s == nil || s.Init() == nil || !containsLoopExpr(s.Init()) {
		return s
	}
	nb := ast.NewVarDeclStmt(s.Name(), s.TypeRef(), desugarExpr(s.Init()), s.IsMutable())
	nb.SetSpan(s.Span())
	return nb
}

// ── 表达式级递归（S107 族容器闭包核心） ──────────────────────────────
// loop-expr（ForExpr/ParallelForExpr）是 parser primary，可嵌在任何表达式
// 值位；其 body 里的 elif 链必须在 desugar 阶段展平，否则 IR build 前
// elseIfs 残留、中臂静默丢失。

type loopExprProbe struct{ hit bool }

// Visit implements ast.Visitor.
func (p *loopExprProbe) Visit(n ast.Node) ast.Visitor {
	switch n.(type) {
	case *ast.ForExpr, *ast.ParallelForExpr:
		p.hit = true
		return nil
	}
	return p
}

// containsLoopExpr 报告子树内是否存在 loop-expr。desugar 只在确需改变时
// 重建表达式容器——无 loop-expr 的树保持指针同一性（既有语料零行为变化）。
func containsLoopExpr(n ast.Node) bool {
	if n == nil {
		return false
	}
	p := &loopExprProbe{}
	ast.Walk(p, n)
	return p.hit
}

// desugarExpr 递归表达式树：遇 loop-expr 重建其 body（desugarBlock +
// source 表达式递归），复合表达式重建子节点；不含 loop-expr 的子树原样返回。
func desugarExpr(e ast.Expr) ast.Expr {
	if e == nil || !containsLoopExpr(e) {
		return e
	}
	switch x := e.(type) {
	case *ast.ForExpr:
		return desugarForExpr(x)
	case *ast.ParallelForExpr:
		return desugarParallelForExpr(x)
	case *ast.BinaryExpr:
		nb := ast.NewBinaryExpr(x.Op(), desugarExpr(x.Left()), desugarExpr(x.Right()))
		nb.SetSpan(x.Span())
		return nb
	case *ast.UnaryExpr:
		nb := ast.NewUnaryExpr(x.Op(), desugarExpr(x.Operand()))
		nb.SetSpan(x.Span())
		return nb
	case *ast.TernaryExpr:
		nb := ast.NewTernaryExpr(desugarExpr(x.Condition()), desugarExpr(x.ThenVal()), desugarExpr(x.ElseVal()))
		nb.SetSpan(x.Span())
		return nb
	case *ast.CallExpr:
		args := make([]ast.Expr, len(x.Args()))
		for i, a := range x.Args() {
			args[i] = desugarExpr(a)
		}
		nb := ast.NewCallExpr(desugarExpr(x.Func()), x.TypeArgs(), args)
		nb.SetSpan(x.Span())
		return nb
	case *ast.ArrayLit:
		els := make([]ast.Expr, len(x.Elements()))
		for i, el := range x.Elements() {
			els[i] = desugarExpr(el)
		}
		nb := ast.NewArrayLit(els)
		nb.SetSpan(x.Span())
		return nb
	case *ast.MapLit:
		entries := x.Entries()
		reb := make([]ast.MapEntry, len(entries))
		for i, en := range entries {
			reb[i] = ast.MapEntry{Key: desugarExpr(en.Key), Value: desugarExpr(en.Value)}
		}
		nb := ast.NewMapLit(reb)
		nb.SetSpan(x.Span())
		return nb
	case *ast.IndexExpr:
		nb := ast.NewIndexExpr(desugarExpr(x.Obj()), desugarExpr(x.Index()))
		nb.SetSpan(x.Span())
		return nb
	case *ast.FieldAccessExpr:
		nb := ast.NewFieldAccessExpr(desugarExpr(x.Obj()), x.FieldName())
		nb.SetSpan(x.Span())
		return nb
	case *ast.TemplateString:
		parts := x.Parts()
		reb := make([]ast.TemplatePart, len(parts))
		for i, pt := range parts {
			reb[i] = ast.TemplatePart{Text: pt.Text, Expr: desugarExpr(pt.Expr)}
		}
		nb := ast.NewTemplateString(reb)
		nb.SetSpan(x.Span())
		return nb
	case *ast.LambdaExpr:
		nb := ast.NewLambdaExpr(x.Params(), desugarExpr(x.Body()))
		nb.SetSpan(x.Span())
		return nb
	default:
		// 叶子/无子表达式节点（字面量/标识符/NullLit/类型类表达式等）。
		return e
	}
}

// desugarForExpr rebuilds a for-expression: body desugared, sources recurse.
func desugarForExpr(s *ast.ForExpr) *ast.ForExpr {
	body := desugarBlock(s.Body())
	if s.IsZip() {
		nb := ast.NewZipForExpr(s.LoopVars(), s.LoopTypes(), desugarExprs(s.LoopSources()), body)
		nb.SetSpan(s.Span())
		return nb
	}
	nb := ast.NewForExpr(s.VarName(), s.VarType(), desugarExpr(s.Iterable()), body)
	nb.SetSpan(s.Span())
	return nb
}

// desugarParallelForExpr rebuilds a parallel-for-expression.
func desugarParallelForExpr(s *ast.ParallelForExpr) *ast.ParallelForExpr {
	body := desugarBlock(s.Body())
	if s.IsZip() {
		nb := ast.NewZipParallelForExpr(s.LoopVars(), s.LoopTypes(), desugarExprs(s.LoopSources()), body, s.Concurrency(), s.OnError())
		nb.SetSpan(s.Span())
		return nb
	}
	nb := ast.NewParallelForExpr(s.VarName(), s.VarType(), desugarExpr(s.Iterable()), body, s.Concurrency(), s.OnError())
	nb.SetSpan(s.Span())
	return nb
}

// desugarExprs maps desugarExpr over a source list (zip loop sources).
func desugarExprs(srcs []ast.Expr) []ast.Expr {
	out := make([]ast.Expr, len(srcs))
	for i, e := range srcs {
		out[i] = desugarExpr(e)
	}
	return out
}

// desugarIf flattens elif chains into nested if/else, mirroring desugar.py
// _desugar_if:
//
//	Before: if (a) { A } elif (b) { B } elif (c) { C } else { D }
//	After:  if (a) { A } else { if (b) { B } else { if (c) { C } else { D } } }
//
// Each rebuilt IfStmt and each wrapper Block copies the span of the elif
// branch it came from, preserving diagnostic fidelity.
func desugarIf(stmt *ast.IfStmt) *ast.IfStmt {
	thenBody := desugarBlock(stmt.ThenBody())
	var elseBody *ast.Block
	if stmt.ElseBody() != nil {
		elseBody = desugarBlock(stmt.ElseBody())
	}

	// No elif branches — nothing to flatten.
	if len(stmt.ElseIfs()) == 0 {
		nb := ast.NewIfStmt(stmt.Condition(), thenBody, nil, elseBody)
		nb.SetSpan(stmt.Span())
		return nb
	}

	// Build the nested structure from the inside out: start with the
	// original else body, then wrap each elif (in reverse) as
	// if (elif_cond) { elif_body } else { current_else }.
	currentElse := elseBody
	for i := len(stmt.ElseIfs()) - 1; i >= 0; i-- {
		branch := stmt.ElseIfs()[i]
		branchBody := desugarBlock(branch.Body())

		inner := ast.NewIfStmt(branch.Condition(), branchBody, nil, currentElse)
		inner.SetSpan(branch.Span())

		currentElse = ast.NewBlock([]ast.Stmt{inner})
		currentElse.SetSpan(branch.Span())
	}

	// The original if keeps its condition/then body; its else_ifs are
	// replaced by the nested structure (desugar.py:108-113).
	nb := ast.NewIfStmt(stmt.Condition(), thenBody, nil, currentElse)
	nb.SetSpan(stmt.Span())
	return nb
}

// desugarFor normalizes a for-stmt: recurse into the body. It does NOT
// convert for→while (desugar.py _desugar_for:117-129). The zip form
// (for (a, b in zip(x, y))) must be rebuilt with ALL loop vars/sources —
// the single-var accessors (VarName/Iterable) only see the first entry, so
// collapsing through them would silently drop the remaining variables and
// sources (binder then reports UNDEFINED_VAR for the lost vars). The
// rebuild mirrors normalizeStmt's zip handling exactly.
func desugarFor(stmt *ast.ForStmt) *ast.ForStmt {
	body := desugarBlock(stmt.Body())
	srcs := desugarExprs(stmt.LoopSources())
	if stmt.IsZip() {
		if stmt.IsParallel() {
			nb := ast.NewZipParallelForStmt(stmt.LoopVars(), stmt.LoopTypes(), srcs, body, stmt.Concurrency(), stmt.OnError())
			nb.SetSpan(stmt.Span())
			return nb
		}
		nb := ast.NewZipForStmt(stmt.LoopVars(), stmt.LoopTypes(), srcs, body)
		nb.SetSpan(stmt.Span())
		return nb
	}
	if stmt.IsParallel() {
		nb := ast.NewParallelForStmt(stmt.VarName(), stmt.VarType(), desugarExpr(stmt.Iterable()), body, stmt.Concurrency(), stmt.OnError())
		nb.SetSpan(stmt.Span())
		return nb
	}
	nb := ast.NewForStmt(stmt.VarName(), stmt.VarType(), desugarExpr(stmt.Iterable()), body)
	nb.SetSpan(stmt.Span())
	return nb
}
