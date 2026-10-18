package passes

// Normalize pass, mirroring src/nclang/lang/passes/normalize.py (read-only
// oracle). Runs AFTER desugar, BEFORE scope/type/syntax/semantic.
//
// Transformation: flatten single-statement nested blocks (bare Block
// statements inline into the parent). If/for/while bodies are NOT flattened
// — they carry scope significance (normalize.py:26-36). Like the desugar
// pass, the Go port rebuilds the touched nodes and replaces the entry and
// function bodies through the program's exported option setters; every
// rebuilt node copies the original span (see desugar.go header).
//
// Deliberate deviation from the Python oracle (2026-09-30, R-172), same
// precedent as the desugar.go S107 note: this pass ALSO chooses the canonical
// form when one meaning has two spellings — "a guard whose then-arm is
// exactly one else-less guard" collapses into a single conjunction guard.
// See canonicalElessGuardFusion below for the rule, its three structural
// preconditions and the evidence.

import "github.com/nodecoda/ncoda/pkg/ast"

// NormalizePass performs structural normalization after desugar, mirroring
// normalize.py NormalizePass.run. Only entry + functions are rewritten
// (normalize.py:25-30); code functions are untouched, matching the
// reference.
type NormalizePass struct{}

// Run normalizes the entry body and every function body in place (through
// the program's exported option setters). Emits no diagnostics.
func (p *NormalizePass) Run(program *ast.Program, ctx *Context) {
	rewriteProgramBodies(program, normalizeBlock)
}

// normalizeBlock normalizes every statement, then inlines bare Block
// statements into the parent (normalize.py _normalize_block:32-51).
func normalizeBlock(block *ast.Block) *ast.Block {
	stmts := block.Stmts()
	normalized := make([]ast.Stmt, 0, len(stmts))
	for _, stmt := range stmts {
		normalized = append(normalized, normalizeStmt(stmt))
	}

	flattened := make([]ast.Stmt, 0, len(normalized))
	for _, stmt := range normalized {
		if bare, ok := stmt.(*ast.Block); ok {
			flattened = append(flattened, bare.Stmts()...)
		} else {
			flattened = append(flattened, stmt)
		}
	}

	// `if` 链的**规范形（第一半）**：相邻的 else-less 守卫，只要「前一条臂不可能
	// 落穿」，就折成 else 链。位置 = **合取融合之后**（上面的 normalizeStmt 已把
	// `if (a) { if (b) {X} }` 折成 `if (a && b) {X}`）—— 判据、理由与边界见
	// absorbIntoChain 头注。
	chained := make([]ast.Stmt, 0, len(flattened))
	for _, stmt := range flattened {
		if n := len(chained); n > 0 && isIfStmt(stmt) {
			if merged, ok := absorbIntoChain(chained[n-1], stmt); ok {
				chained[n-1] = merged
				continue
			}
		}
		chained = append(chained, stmt)
	}

	nb := ast.NewBlock(chained)
	nb.SetSpan(block.Span())
	return nb
}

// armReachesNextStmt 报告「这条臂的活路径能不能落到紧跟其后的语句」。
//
// 与 `AnalyzeBlock(...).CanFallThrough` 的关系：那是**同一个问题在函数级**的
// 回答（MISSING_RETURN E1010 用它问「函数会不会掉出末尾」），只是它对
// `break` / `continue` 记的是「落穿」—— 因为循环跳转不影响函数是否掉出末尾。
// 但**块级**的答案相反：`break` / `continue` 之后，本块的续行不可达。两者不是
// 同一个问题，所以这里补上后半个条件，而不是去改 `AnalyzeStatement` 的口径
// （改它 = 换掉 E1010 的判据）。
//
// 一手（2026-10-01，`cozebiz_loop_break_eless_if_zero_candidate_contract_test.go`
// 的 `two_terminated_else_less_ifs_then_valueless_return`）：不补这一条 ⇒ 该链
// 折不起来 ⇒ c0 两条条件节点、c1 一条 ⇒ 往返不落定点。
func armReachesNextStmt(b *ast.Block) bool {
	if !AnalyzeBlock(b).CanFallThrough {
		return false
	}
	stmts := b.Stmts()
	if len(stmts) == 0 {
		return true
	}
	switch stmts[len(stmts)-1].(type) {
	case *ast.BreakStmt, *ast.ContinueStmt:
		return false
	}
	return true
}

// isIfStmt 报告 next 是不是 `if`（链成员唯一可能的形状）。
func isIfStmt(s ast.Stmt) bool {
	_, ok := s.(*ast.IfStmt)
	return ok
}

// absorbIntoChain 把紧随其后的语句 next 吸收成前一条 if 链的**链尾 else 臂**，
// 成功则返回重建后的链（否则原样返回、ok=false）。
//
// ── 为什么这是**规范化**而不是新语义（2026-10-01）────────────────────
// `if (a) { …return x… }` 与 `if (a) { …return x… } else { … }` 对**后继语句**
// 是同一个意思：前者只有当 a 为假时才走到后面，而这正是 else。⇒ 当
// `AnalyzeBlock(then).CanFallThrough == false` 时，把后继并进 else 臂**不改变
// 任何可观察行为**（⟦·⟧ 逐点相同），只是把「两条并列守卫」写成「一条链」。
//
// ── 为什么必须在**合取融合之后**（normalize）折，而不是在 desugar ──────
// 判据「前一条臂不可能落穿」在**合取融合（R-172，`canonicalElessGuardFusion`）
// 之前**对 `if (a) { if (b) { return } } else…` 恒为假（内层 else-less ⇒ 落穿），
// 于是同一语义的两种源拼写仍会分叉。实测（2026-10-01，生成器 `exit_phi_shared`）：
// desugar 折 ⇒ 6 例 `ARTIFACT_DRIFT/TOPOLOGY@r2`；挪到 normalize（融合之后）⇒ 0 例。
//
// ── 为什么必须在 AST 就折（而不是留到 lowering）────────────────────
// 同一语义在 IR 里有**两种形状**（并列 if / 嵌套 else-if），而两向的产物必须
// 唯一：平台自己的多分支条件节点是**一个**节点（一手 X212 节点 110750），
// coze-biz 的条件头合一（`pkg/ir/fuse_branch_chains.go`）只认**嵌套**形状。
// 不折 ⇒ 同一语义两种 IR 形状 ⇒ 两种产物 ⇒ 一轮往返不落定点（实测：生成器
// `exit_phi_*` / `loop_inner_return_*` 24 例 `ARTIFACT_DRIFT/TOPOLOGY@r2`）。
//
// ── 复用而非重写「能不能落穿」──────────────────────────────────────
// 该事实的**唯一定义** = `AnalyzeStatement` / `AnalyzeBlock`（本包，
// control_flow.go，1:1 移植自参考实现，MISSING_RETURN E1010 的同一份事实）。
// 本函数不另立终止性分析 —— 那正是本仓反复吃亏的「同一事实第二份实现」。
//
// ── 边界（保守，登记不掩盖）──────────────────────────────────────────
//
//	· `AnalyzeStatement` 对 break/continue 报 CanFallThrough=true ⇒ 以 break
//	  结尾的臂**不折**（该形状今天仍会编出两条并列条件节点）。保守方向安全：
//	  不折只是少归一，不会把「落穿」误判成「不落穿」。
//	· 链尾已有 else（或 else 臂不止一条语句）⇒ 不折：后面的语句确实在 if 之后。
func absorbIntoChain(prev, next ast.Stmt) (ast.Stmt, bool) {
	a, ok := prev.(*ast.IfStmt)
	if !ok || len(a.ElseIfs()) != 0 {
		return prev, false
	}
	if a.ElseBody() == nil {
		if armReachesNextStmt(a.ThenBody()) {
			return prev, false
		}
		eb := ast.NewBlock([]ast.Stmt{next})
		eb.SetSpan(a.Span())
		nb := ast.NewIfStmt(a.Condition(), a.ThenBody(), nil, eb)
		nb.SetSpan(a.Span())
		return nb, true
	}
	// 沿 else 臂下钻链尾：else 块恰好一条语句且它是 if。
	elseStmts := a.ElseBody().Stmts()
	if len(elseStmts) != 1 {
		return prev, false
	}
	inner, ok := absorbIntoChain(elseStmts[0], next)
	if !ok {
		return prev, false
	}
	eb := ast.NewBlock([]ast.Stmt{inner})
	eb.SetSpan(a.ElseBody().Span())
	nb := ast.NewIfStmt(a.Condition(), a.ThenBody(), nil, eb)
	nb.SetSpan(a.Span())
	return nb, true
}

// normalizeStmt normalizes a single statement, recursing into children.
// Faithful to normalize.py _normalize_stmt: a bare Block statement is
// returned UNCHANGED (its children are not normalized — the parent's
// flatten step inlines it); else-if branches are likewise not recursed into
// (they are empty by the time normalize runs in the pipeline, and the
// reference does not visit them).
func normalizeStmt(stmt ast.Stmt) ast.Stmt {
	switch s := stmt.(type) {
	case *ast.IfStmt:
		thenBody := normalizeBlock(s.ThenBody())
		var elseBody *ast.Block
		if s.ElseBody() != nil {
			elseBody = normalizeBlock(s.ElseBody())
		}
		nb := ast.NewIfStmt(s.Condition(), thenBody, s.ElseIfs(), elseBody)
		nb.SetSpan(s.Span())
		return canonicalElessGuardFusion(nb)
	case *ast.ForStmt:
		body := normalizeBlock(s.Body())
		var nb *ast.ForStmt
		if s.IsZip() {
			if s.IsParallel() {
				nb = ast.NewZipParallelForStmt(s.LoopVars(), s.LoopTypes(), s.LoopSources(), body, s.Concurrency(), s.OnError())
			} else {
				nb = ast.NewZipForStmt(s.LoopVars(), s.LoopTypes(), s.LoopSources(), body)
			}
		} else if s.IsParallel() {
			nb = ast.NewParallelForStmt(s.VarName(), s.VarType(), s.Iterable(), body, s.Concurrency(), s.OnError())
		} else {
			nb = ast.NewForStmt(s.VarName(), s.VarType(), s.Iterable(), body)
		}
		nb.SetSpan(s.Span())
		return nb
	case *ast.WhileStmt:
		nb := ast.NewWhileStmt(s.Condition(), s.Limit(), normalizeBlock(s.Body()))
		nb.SetSpan(s.Span())
		return nb
	case *ast.ParallelStmt:
		branches := make([]*ast.Block, len(s.Branches()))
		for i, br := range s.Branches() {
			branches[i] = normalizeBlock(br)
		}
		nb := ast.NewParallelStmt(branches, s.BranchNames()...)
		nb.SetSpan(s.Span())
		return nb
	case *ast.SwitchStmt:
		branches := make([]*ast.SwitchBranch, len(s.Branches()))
		for i, br := range s.Branches() {
			nb := ast.NewSwitchBranch(br.Label(), normalizeBlock(br.Body()))
			nb.SetSpan(br.Span())
			branches[i] = nb
		}
		var defBody *ast.Block
		if s.DefaultBody() != nil {
			defBody = normalizeBlock(s.DefaultBody())
		}
		nb := ast.NewSwitchStmt(s.Query(), s.Config(), branches, defBody, s.Result())
		nb.SetSpan(s.Span())
		return nb
	default:
		return stmt
	}
}

// ── 规范形：嵌套 else-less 守卫的**合取融合**（R-172 · 2026-09-30）───────
//
// 为什么（一手）：`if (a) { if (b) { X } }` 与 `if (a && b) { X }` 是**同一个
// 意思的两种拼写**，但产物**不同构** —— 前者物化两道门，而帧/流程出口的公共落点
// 会被这两条门的未匹配端口**同时命中**（同一生产者两条控制入边）；反编译器判不出
// 汇聚候选互斥 ⇒ `RS-MRG-001` / `DECOMPILE_FAIL`（产物诚实拒读，不是错读）。
// 本仓原则「同一个意思能写出两种产物 = 一个必须钉死的坑」⇒ 规范形取**单门形态**：
// 规范化阶段就把前者折成后者（产物 8 → 7 节点），下游（提升 / φ / lowering）只见一种
// 形状。定点证据 = 生成器 `sub_fn` 形状 depth≥2 六格（形状与证据见
// `docs/five-step/rulings/2026-09-30-ruling-sheet-r32.md` §九 / 残余 `R-172`）。
//
// 折叠前置（三条，全部**结构**判据 —— 不做取值域推理，不猜可达性）：
//
//  1. **外层 else-less**（无 else-if 链、else 体为空或 nil）。否则 else 的归属会变：
//     `if (a) { if (b) {X} } else {E}` 折成 `if (a && b) {X} else {E}` 之后，
//     `a ∧ ¬b` 路径会把 E 跑出来 —— 语义不等价（`if` 的未匹配臂就是它的 else，
//     共用落点这件事只对 else-less 外层成立）。
//  2. then 臂**恰好一条语句**，且该语句是 **else-less** 的 `if`。
//  3. 内层守卫**不含调用**（叶级纯表达式）。平台 condition 以**入参引用**求值 ⇒
//     把内层守卫上提到外层条件 = 把它的产出方挪到门**之前无条件执行**：对纯表达式
//     无观察差异，对有副作用的调用是语义变化（一手反例：内层守卫 `fmt(n) != ""`，
//     `fmt` 是 code 函数）。⇒ 该支**不折**，保持原形状（如实登记，不假装整族归零）。
//  4. **两侧守卫都能不加括号地做 `&&` 的操作数**（顶层的 `||` / 三元 ⇒ 不折）。
//     规范形是**一种拼写**，两向都必须能写出它：`a || b && c` 与 `(a || b) && c`
//     不是一个意思（`&&` 结合更紧）⇒ 折出来的守卫若需括号才成义，反编译端（只发
//     文本、不造括号）就发不出同一个拼写 ⇒ 两侧的**准入集合必须相等**，否则
//     往返破裂。⇒ `||`/三元守卫不折（该形状保持原样；平台条件表是**扁平**的
//     —— 语料 322 yaml 实测：最多 3 条、logic ∈ {1,2}、**零嵌套** —— 混合逻辑
//     由 shim 承载，见 pkg/ir/build_return.go::conditionToSelector）。
//
// 语义等价性（`&&` 短路）：`a && b` 的求值面 = 「a 为真时才求 b」，与
// `if (a) { if (b) {...} }` 的「a 为真才进内层门」逐点相同；臂体 X 的执行条件
// 两侧都是 `a ∧ b`。链长 ≥3 时递归折叠（`a && (b && c)`），assoc 由遍历序固定。
func canonicalElessGuardFusion(s *ast.IfStmt) ast.Stmt {
	if len(s.ElseIfs()) != 0 || !emptyOrNilBlock(s.ElseBody()) {
		return s
	}
	body := s.ThenBody()
	if body == nil || len(body.Stmts()) != 1 {
		return s
	}
	inner, ok := body.Stmts()[0].(*ast.IfStmt)
	if !ok || len(inner.ElseIfs()) != 0 || !emptyOrNilBlock(inner.ElseBody()) {
		return s
	}
	if exprRequiresNode(inner.Condition()) {
		return s
	}
	if !conjunctionComposable(s.Condition()) || !conjunctionComposable(inner.Condition()) {
		return s
	}
	cond := ast.NewBinaryExpr("&&", s.Condition(), inner.Condition())
	cond.SetSpan(ast.Span{Start: s.Condition().Span().Start, End: inner.Condition().Span().End})
	fused := ast.NewIfStmt(cond, inner.ThenBody(), nil, nil)
	fused.SetSpan(s.Span())
	// 链长 ≥3：折出的形态若仍匹配（then 臂恰好是单个 else-less 守卫），继续折。
	return canonicalElessGuardFusion(fused)
}

// emptyOrNilBlock reports whether b carries no statement (nil and an empty
// block are the same thing to this rule: an absent else).
func emptyOrNilBlock(b *ast.Block) bool { return b == nil || len(b.Stmts()) == 0 }

// conjunctionComposable reports whether e can appear as an operand of `&&`
// **without parentheses**. `&&` binds tighter than `||` and the ternary, so
// falling back on `a || b && c` / `a ? x : y && c` would change the meaning
// (see precondition 4 in the rule header): the canonical spelling must be
// writable on BOTH sides — the raising side emits text and adds no brackets.
// The mirror implementation lives in
// platforms/cozebiz/decompile/raising_canonical_eless_guard.go, and the two
// admission sets are locked by
// TestCozeBizNestedElessFrameExit.../spellings-share-one-shape.
func conjunctionComposable(e ast.Expr) bool {
	if b, ok := e.(*ast.BinaryExpr); ok && b.Op() == "||" {
		return false
	}
	if _, ok := e.(*ast.TernaryExpr); ok {
		return false
	}
	return true
}

// exprRequiresNode reports whether evaluating e can require **producing a
// node** — any call (tool / plugin / llm / foreign code / user function),
// an inline code block, a loop expression or an operation-policy wrapper.
// The check is deliberately syntactic and conservative: the normalize stage
// runs before scope/type, so no binding information is available, and an
// unknown-but-pure call (e.g. a builtin) is treated as node-bearing rather
// than risk moving a node's execution out of the arm it was written in.
func exprRequiresNode(e ast.Expr) bool {
	f := &nodeBearingExprFinder{}
	ast.Walk(f, e)
	return f.found
}

type nodeBearingExprFinder struct{ found bool }

func (f *nodeBearingExprFinder) Visit(n ast.Node) ast.Visitor {
	if f.found {
		return nil
	}
	switch n.(type) {
	case *ast.CallExpr, *ast.ForeignCodeExpr, *ast.ForExpr, *ast.ParallelForExpr,
		*ast.OperationPolicyExpr:
		f.found = true
		return nil
	}
	return f
}
