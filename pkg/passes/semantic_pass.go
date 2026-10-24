package passes

// Semantic pass — higher-level semantic rules after scope + type are
// established, ported 1:1 from src/nclang/lang/passes/semantic_pass.py
// (247 lines):
//
//	SemanticPass.run                :33-52
//	_BreakContinueCheck             :57-88
//	_OutputKeyCheck                 :94-108
//	_AssignmentTargetCheck          :114-131
//	_UnusedVarCheck                 :137-156
//	_StdlibStaticArgumentCheck      :162-181
//	_StdlibCallContextCheck         :187-216
//
// Every E1009/E1016/E1018/E1019/E1031/E1032/W2002/W2003 message is
// byte-for-byte the Python f-string result (see the emitters below).
// E1017（output key）已于 2026-09-17 退场：它没有任何发射点，外部语言标准也随
// @answer 族把 E1017 删除 ⇒ 常量与探针一并删除（标准 ↔ 实现同步）。
//
// D1 (run order): Python runs the six visitor checks in this exact order —
// break/continue, output key, assign immutable, unused var, stdlib call
// context, stdlib static argument (semantic_pass.py:48-51). The Task 17A
// brief listed "static arg" before "call context"; the Python source is
// authoritative and the port follows it.
//
// D2 (symbol lookup): Python reads ctx.expr_symbols / ctx.stmt_symbols
// (node → Symbol) and mutability on the Symbol object. Go reads the
// published snapshot — ReferenceSymbols / DeclarationSymbols → SymbolDecl
// — which carries the same mutability bit (bindings.py
// SymbolDeclaration.mutability).
//
// D3 (stdlib registry errors): Python get_stdlib_function raises KeyError
// for an unknown stdlib function (a hard crash). Go returns an error and
// the stdlib checks skip that call (deviation, recorded).

import (
	"fmt"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
)

// SemanticPass applies higher-level semantic rules after scope + type are
// established, mirroring semantic_pass.py:33-52.
type SemanticPass struct{}

// Run mirrors run (semantic_pass.py:33-52): mode validity, entry presence
// (E1009 short-circuits the pass), empty-entry warning, then the six
// visitor checks in the reference order.
func (SemanticPass) Run(program *ast.Program, ctx *Context) {
	validModes := map[string]bool{"workflow": true, "advanced-chat": true, "agent": true}
	if !validModes[program.Mode()] {
		ctx.Emit(diag.INVALID_MODE, diag.Error,
			fmt.Sprintf("Unknown @mode '%s'. Expected: advanced-chat, workflow, agent", program.Mode()),
			ast.Span{})
	}

	if program.Entry() == nil {
		ctx.Emit(diag.NO_ENTRY, diag.Error,
			"No main function defined (expected 'function main(...) { ... }')", ast.Span{})
		return
	}

	if len(program.Entry().Body().Stmts()) == 0 {
		ctx.Emit(diag.EMPTY_ENTRY, diag.Warning, "Main function is empty", program.Entry().Span())
	}

	// M13: @mode advanced-chat（对齐参考目标 chatflow）入口应声明 string query
	// —— 会话输入。无参 main 放行：runtime 可直接从 input map 读取 query
	//（兼容 target_validation 降级路径）;仅当显式声明了非 query 首参时报错。
	if program.Mode() == "advanced-chat" {
		params := program.Entry().Params()
		if len(params) > 0 && params[0].Name() != "query" {
			ctx.Emit(diag.CHATFLOW_ENTRY, diag.Error,
				"@mode advanced-chat requires entry parameter 'query' (function main(string query) -> ...)",
				program.Entry().Span())
		}
	}

	(&breakContinueCheck{ctx: ctx, entered: map[ast.Node]bool{}}).run(program)
	(&assignmentTargetCheck{ctx: ctx, snap: ctx.Bindings}).run(program)
	(&unusedVarCheck{ctx: ctx, snap: ctx.Bindings}).run(program)
	(&stdlibCallContextCheck{ctx: ctx}).run(program)
	(&stdlibStaticArgumentCheck{ctx: ctx}).run(program)
}

func (c *breakContinueCheck) run(program *ast.Program)    { ast.Walk(c, program) }
func (c *assignmentTargetCheck) run(program *ast.Program) { ast.Walk(c, program) }
func (c *unusedVarCheck) run(program *ast.Program)        { ast.Walk(c, program) }
func (c *stdlibCallContextCheck) run(program *ast.Program) {
	ast.Walk(c, program)
}
func (c *stdlibStaticArgumentCheck) run(program *ast.Program) {
	ast.Walk(c, program)
}

// ── Check 1: break/continue must be inside a loop ─────────────────────

// breakContinueCheck ensures break/continue appear only inside loops,
// mirroring _BreakContinueCheck (semantic_pass.py:57-88). The loop depth
// flag is set around the loop subtree via the per-node re-entry guard
// (D1 of the yield_contract.go header).
//
// 循环上下文用栈而非布尔标志：ParallelForExpr 压入 parallel 屏障（coze
// batch 子图没有循环控制语义——batch 是对数组逐项并行执行，无 break/
// continue 概念）。break/continue 撞上屏障 → 报错（即使在嵌套的外层 for
// 内，最近的可退出循环是 parallel-for，它不支持退出）。ForStmt/ForExpr/
// WhileStmt 压入合法循环上下文。
type loopCtxKind int

const (
	loopCtxLoop     loopCtxKind = iota // 合法 break/continue 目标（ForStmt/ForExpr/WhileStmt）
	loopCtxParallel                    // parallel-for 屏障（batch：无循环控制）
)

type breakContinueCheck struct {
	ctx     *Context
	loopCtx []loopCtxKind
	entered map[ast.Node]bool
}

// Visit mirrors the six visit_* handlers (semantic_pass.py:60-88).
func (c *breakContinueCheck) Visit(n ast.Node) ast.Visitor {
	switch node := n.(type) {
	case *ast.ForStmt, *ast.ForExpr, *ast.WhileStmt:
		if c.entered[node] {
			return c
		}
		c.entered[node] = true
		defer func() { c.entered[node] = false }()
		saved := c.loopCtx
		c.loopCtx = append(c.loopCtx, loopCtxLoop)
		ast.Walk(c, node)
		c.loopCtx = saved
		return nil
	case *ast.ParallelForExpr:
		if c.entered[node] {
			return c
		}
		c.entered[node] = true
		defer func() { c.entered[node] = false }()
		saved := c.loopCtx
		c.loopCtx = append(c.loopCtx, loopCtxParallel)
		ast.Walk(c, node)
		c.loopCtx = saved
		return nil
	case *ast.BreakStmt:
		c.checkExit(node, "'break'", true)
		return c
	case *ast.ContinueStmt:
		c.checkExit(node, "'continue'", false)
		return c
	}
	return c
}

// checkExit 最近的循环上下文决定 break/continue 合法性。
//
// 「并发迭代体内 break 非法」现在是**语言规则**（D-L3(a)，2026-09-14 用户拍板：
// break = 终止整个循环 = 并发下不确定），判据与循环形态/嵌套深度无关，由
// parallel 阶段发布 `PARALLEL_EXTERNAL_EXIT`（E1037，先于本阶段）；
// 这里保留 NOT_IN_LOOP 只是**同义兜底**（本阶段单独运行时的白盒表面）。
//
// `continue` 从本兜底中**移除**：S4 定义 continue = 结束本迭代剩余部分 =
// **迭代局部**（平台 `Continue.Invoke` 是 no-op 控制叶子，语义正确）⇒ 合法，
// 且**语句形态与表达式形态同一把尺子**（语句形态 `parallel for (...) { }`
// 此前从不经 loopCtxParallel，只有表达式形态被拒 = 形态分叉，已统一）。
func (c *breakContinueCheck) checkExit(node ast.Node, keyword string, isBreak bool) {
	if len(c.loopCtx) == 0 {
		c.ctx.Emit(diag.NOT_IN_LOOP, diag.Error, keyword+" outside of loop", node.Span())
		return
	}
	top := c.loopCtx[len(c.loopCtx)-1]
	if top == loopCtxParallel && isBreak {
		c.ctx.Emit(diag.NOT_IN_LOOP, diag.Error,
			keyword+" cannot exit a parallel for (batch has no loop control)", node.Span())
	}
}

// ── Check 3: assignment to immutable variables ────────────────────────

// assignmentTargetCheck ensures assignment targets are mutable variables,
// mirroring _AssignmentTargetCheck (semantic_pass.py:114-131).
type assignmentTargetCheck struct {
	ctx  *Context
	snap *binding.Snapshot
}

// Visit mirrors visit_assign_stmt (semantic_pass.py:117-131).
func (c *assignmentTargetCheck) Visit(n ast.Node) ast.Visitor {
	node, ok := n.(*ast.AssignStmt)
	if !ok {
		return c
	}
	if target, ok := node.Target().(*ast.IdentExpr); ok {
		if symbolID, found := c.referenceSymbol(target); found {
			decl, found := c.snap.Declarations().Get(symbolID)
			if found && !decl.Mutable {
				c.ctx.Emit(diag.ASSIGN_IMMUTABLE, diag.Error,
					fmt.Sprintf("Cannot assign to immutable variable '%s'", target.Name()),
					node.Span())
			}
		}
	}
	ast.Walk(c, node.Target())
	ast.Walk(c, node.Value())
	return nil
}

// referenceSymbol resolves the snapshot symbol ID bound to an expression
// node (Python ctx.expr_symbols / ctx.symbol_id).
func (c *assignmentTargetCheck) referenceSymbol(n ast.Node) (binding.SymbolID, bool) {
	id, ok := c.snap.NodeIDOf(n)
	if !ok {
		return 0, false
	}
	return c.snap.ReferenceSymbols().Get(id)
}

// ── Check 4: unused variables (resolved symbol bindings) ──────────────

// unusedVarCheck warns about declarations whose resolved symbol is never
// referenced, mirroring _UnusedVarCheck (semantic_pass.py:137-156). The
// referenced set is computed once at the program root, before the walk
// (Python visit_program_node).
type unusedVarCheck struct {
	ctx        *Context
	snap       *binding.Snapshot
	referenced map[binding.SymbolID]bool
}

// Visit mirrors visit_program_node + visit_var_decl_stmt
// (semantic_pass.py:143-155).
func (c *unusedVarCheck) Visit(n ast.Node) ast.Visitor {
	switch node := n.(type) {
	case *ast.Program:
		c.referenced = map[binding.SymbolID]bool{}
		c.snap.ReferenceSymbols().Range(func(_ binding.AstNodeID, symbolID binding.SymbolID) bool {
			c.referenced[symbolID] = true
			return true
		})
		return c
	case *ast.VarDeclStmt:
		if symbolID, ok := c.declarationSymbol(node); ok && !c.referenced[symbolID] {
			c.ctx.Emit(diag.UNUSED_VAR, diag.Warning,
				fmt.Sprintf("Variable '%s' is declared but never used", node.Name()),
				node.Span())
		}
		return c
	}
	return c
}

// declarationSymbol resolves the snapshot symbol declared by a node
// (Python ctx.stmt_symbols).
func (c *unusedVarCheck) declarationSymbol(n ast.Node) (binding.SymbolID, bool) {
	id, ok := c.snap.NodeIDOf(n)
	if !ok {
		return 0, false
	}
	return c.snap.DeclarationSymbols().Get(id)
}

// ── Check 5: stdlib calls in unsupported expression positions ─────────

// stdlibCallContextCheck rejects stdlib calls in expression positions the
// translator cannot expand, mirroring _StdlibCallContextCheck
// (semantic_pass.py:187-216).
type stdlibCallContextCheck struct {
	ctx *Context
}

// Visit mirrors visit_var_decl_stmt / visit_return_stmt / visit_expr_stmt /
// visit_call_expr (semantic_pass.py:190-207).
func (c *stdlibCallContextCheck) Visit(n ast.Node) ast.Visitor {
	switch node := n.(type) {
	case *ast.VarDeclStmt:
		c.visitSupportedRoot(node.Init())
		return nil
	case *ast.ReturnStmt:
		c.visitSupportedRoot(node.Value())
		return nil
	case *ast.ExprStmt:
		c.visitSupportedRoot(node.Expr())
		return nil
	case *ast.CallExpr:
		path := binding.StdlibCallPath(node)
		if path == "" {
			return c
		}
		c.ctx.Emit(diag.STDLIB_CALL_CONTEXT, diag.Error,
			path+" must be a variable initializer, expression statement, or direct return",
			node.Span())
		ast.Walk(c, node.Func())
		for _, argument := range node.Args() {
			ast.Walk(c, argument)
		}
		return nil
	}
	return c
}

// visitSupportedRoot mirrors _visit_supported_root (semantic_pass.py:209-
// 216): a stdlib call at a supported root is allowed and only its arguments
// are walked (nested stdlib calls inside arguments are rejected by
// visit_call_expr); any other expression is walked normally.
func (c *stdlibCallContextCheck) visitSupportedRoot(expr ast.Expr) {
	if call, ok := expr.(*ast.CallExpr); ok && binding.StdlibCallPath(call) != "" {
		for _, argument := range call.Args() {
			ast.Walk(c, argument)
		}
		return
	}
	if expr != nil {
		ast.Walk(c, expr)
	}
}

// ── Check 6: compile-time string literals for stdlib config positions ──

// stdlibStaticArgumentCheck requires compile-time string literals for
// stdlib configuration positions, mirroring _StdlibStaticArgumentCheck
// (semantic_pass.py:162-181).
type stdlibStaticArgumentCheck struct {
	ctx *Context
}

// Visit mirrors visit_call_expr (semantic_pass.py:165-181).
func (c *stdlibStaticArgumentCheck) Visit(n ast.Node) ast.Visitor {
	node, ok := n.(*ast.CallExpr)
	if !ok {
		return c
	}
	path := binding.StdlibCallPath(node)
	if path == "" {
		return c
	}
	parts := strings.Split(path, ".")
	spec, err := binding.GetStdlibFunction(parts[1], parts[2])
	if err == nil {
		for _, position := range spec.StaticParameterPositions() {
			if position >= len(node.Args()) {
				continue
			}
			if _, ok := node.Args()[position].(*ast.StringLit); ok {
				continue
			}
			parameter := spec.Parameters[position]
			c.ctx.Emit(diag.STDLIB_STATIC_ARGUMENT, diag.Error,
				fmt.Sprintf("%s parameter '%s' must be a string literal",
					spec.QualifiedName(), parameter.Name),
				node.Args()[position].Span())
		}
	}
	ast.Walk(c, node.Func())
	for _, argument := range node.Args() {
		ast.Walk(c, argument)
	}
	return nil
}
