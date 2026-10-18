package passes

// Prove exactly-one-yield paths for value-producing For expressions, ported
// 1:1 from src/nclang/lang/passes/yield_contract_pass.py (128 lines):
//
//	YieldContractPass.run         :36-39
//	visit_for_expr                :41-52
//	visit_yield_stmt              :54-59
//	visit_parallel_expr           :61-72
//	_block_outcomes               :74-80
//	_statement_outcomes           :82-108
//	_emit                         :110-112
//
// D2 (位置判据；**有意偏离参考实现**，2026-09-15 用户重申原始设计)：参考实现的
// `_contains_owned_yield` (:115-128) 只对**顶层尾部**设限，并在 `_statement_outcomes`
// 里主动为 `if` / `switch` 臂内的 yield 计路径数 —— 即显式把「臂内 yield」当成
// 合法的路径发布。那与设计相反：yield 是 for 体的**独立语句**，条件发布走值位置
// 表达式。故本文件不移植 `_contains_owned_yield`，改在 visitForExpr 里做**一条
// 位置判据**（通用遍历器 + 不下降进内层 value-producing for），并让
// `_statement_outcomes` 只保留 exited 轴。规则表 = docs/five-step/rules/A11-yield-position.yaml。
//
// Every E1043 YIELD_CONTRACT message is byte-for-byte the Python f-string
// result (see the emitters below)，位置判据的那条消息除外（它是新增判据）。
//
// D1 (visitor mechanics): Python ProcASTVisitor uses try/finally depth
// semantics around the child walk (super().visit_*). Go's ast.Walk visitor
// contract has no after-children hook, so each handled node re-enters
// ast.Walk under a per-node guard (the "entered" set): the handler runs the
// check at the current depth, bumps the depth, walks the subtree itself,
// then restores the depth. Nested handled nodes inside the subtree dispatch
// through the same guard, preserving arbitrary nesting.

import (
	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/diag"
)

// YieldContractPass requires one yield on every normal path of a
// value-producing for and forbids external exits, mirroring
// yield_contract_pass.py:36-39.
//
// coze loop 语义实证（roundtrip 语料）：coze-studio loop 支持 Break
// （canvas type 19 "终止循环"）与 Continue（type 29 "继续循环"）节点，
// loop 输出收集发生在"每轮迭代结束事件"（break/continue 轮同样收集）。
// 因此 ncoda 约定：
//
//	for (item in coll) { stmts*; yield+ }
//
//	- yield 是循环体的**独立语句**：只能出现在 for 体的**直接语句序列**里，
//	  不得嵌在 if / switch / parallel / while / 内层语句内部（2026-09-15
//	  用户重申的原始设计：用语法限制换语义分析的简单性，并让
//	  `let x = for …` 的返回值一眼可读）。条件发布用**值位置表达式**表达
//	  （`yield (c) ? a : b;`），不引入第三条「按路径发布」的语义。
//	- yield 序列恒在 for 体末尾（yield 是 loop 的输出注解，每轮迭代
//	  结束固定执行，类似 C 的 update-op）
//	- yield 之后不允许再出现其它语句（除 yield）
//	- break / continue 在 value-producing for 内合法：提前退出路径
//	  （exited=true）豁免 "每条正常路径至少一个 yield" 约束；其后语句
//	  不可达（C 语义，反编译负责任地挂在条件分支内）
//	- return 仍非法（函数级退出，破坏聚合语义）
type YieldContractPass struct {
	depth int
	ctx   *Context
}

// Run mirrors run (yield_contract_pass.py:36-39): reset the depth and
// traverse the whole program.
func (p *YieldContractPass) Run(program *ast.Program, ctx *Context) {
	p.depth = 0
	p.ctx = ctx
	w := &yieldWalker{pass: p, ctx: ctx, entered: map[ast.Node]bool{}}
	ast.Walk(w, program)
}

// yieldWalker is the pass's AST visitor; the three handled node types
// re-enter ast.Walk under a guard so the Python try/finally depth semantics
// hold (see the file header D1).
type yieldWalker struct {
	pass    *YieldContractPass
	ctx     *Context
	entered map[ast.Node]bool
}

// Visit mirrors the visit_* handlers (yield_contract_pass.py:41-72) plus
// the base-visitor delegation for parallel-fors (I2, R1): Python
// visit_parallel_for_expr (ast.py:686-688) is `return self.visit_for_expr(
// node, ctx)`, so a parallel-for is a value-producing for with the exact
// same contract.
func (w *yieldWalker) Visit(n ast.Node) ast.Visitor {
	switch node := n.(type) {
	case *ast.ForExpr:
		return w.visitForExpr(node, node.Body())
	case *ast.ParallelForExpr:
		return w.visitForExpr(node, node.Body())
	case *ast.YieldStmt:
		// visit_yield_stmt (:54-59).
		if w.pass.depth == 0 {
			w.pass.emit(node, "yield is legal only inside a value-producing for")
		}
	}
	return w
}

// visitForExpr mirrors visit_for_expr (yield_contract_pass.py:41-52),
// shared by ForExpr and ParallelForExpr (see the Visit dispatch note).
//
// 多口扩展：命名 yield（yield port = expr）允许一条路径发布多个口，契约
// 从"恰一个 yield"放宽为"每条正常路径至少一个 yield"（0 ∈ outcomes 即拒）。
// 计数按 2 截断（多口 = 2），路径间端口集不一致是边缘用例（反编译产物
// 恒为无条件全口发布），由 IR 层 union 处理，不在本 pass 逐路径校验。
func (w *yieldWalker) visitForExpr(node ast.Node, body *ast.Block) ast.Visitor {
	if w.entered[node] {
		return w // children of the guarded walk below
	}
	w.entered[node] = true
	defer func() { w.entered[node] = false }()
	// visit_for_expr (:41-52): analyze the body first; a new error inside
	// the block analysis suppresses the aggregate message.
	before := len(w.ctx.Diagnostics)
	// 位置判据（设计契约）：yield 必须是**本循环体**的直接语句。判据对递归
	// 封闭 —— 直接语句序列里的每条非 yield 语句，其子树（**不下降进内层
	// value-producing for**：内层 yield 是内层体的直接语句，由内层自己的
	// visitForExpr 判）不得含 yield。用通用遍历器而不是逐容器枚举：语句宿主
	// 家族是开放的（if/switch/parallel/while/attempt/ask/…），以后新增宿主
	// 自动被这条判据覆盖，不会留下静默通道。
	for _, st := range body.Stmts() {
		if _, isYield := st.(*ast.YieldStmt); isYield {
			continue
		}
		ast.Walk(&nestedYieldFinder{pass: w.pass}, st)
	}
	outcomes := w.pass.blockOutcomes(body, w.ctx)
	// 正常路径（未提前退出）必须至少一个 yield；break/continue 提前退出
	// 路径（exited=true）豁免。
	if !hasNewErrorSince(w.ctx.Diagnostics, before) && outcomes[yieldOutcome{count: 0}] {
		w.pass.emit(node,
			"value-producing for requires at least one yield on every normal path")
	}
	// yield 序列恒在 for 体末尾：首个 yield 之后只允许 yield。
	// （yield 是 loop 的输出注解，类似 C 的 update-op，位置固定收尾。）
	seenYield := false
	for _, st := range body.Stmts() {
		if _, isYield := st.(*ast.YieldStmt); isYield {
			seenYield = true
			continue
		}
		if seenYield {
			w.pass.emit(st, "yield must be the last statement(s) of a value-producing for")
			break
		}
	}
	w.pass.depth++
	ast.Walk(w, node)
	w.pass.depth--
	return nil
}

// yieldOutcome 一条控制流路径的 (yield 计数, 是否提前退出)。
// exited=true 表示路径以 break/continue/return 结束：循环提前终止，
// 其后的语句不可达，也不再要求 yield。
type yieldOutcome struct {
	count  int
	exited bool
}

// blockOutcomes computes the reachable yield-count set for a block,
// mirroring _block_outcomes (yield_contract_pass.py:74-80): the Cartesian
// sum of statement outcomes, capped at 2.
//
// 提前退出路径（exited=true）不再与后续语句合并（break 之后的语句是
// 死代码，不产生假路径）；未退出路径与语句正常合并。
func (p *YieldContractPass) blockOutcomes(block *ast.Block, ctx *Context) map[yieldOutcome]bool {
	outcomes := map[yieldOutcome]bool{{count: 0}: true}
	for _, statement := range block.Stmts() {
		statementOutcomes := p.statementOutcomes(statement, ctx)
		next := map[yieldOutcome]bool{}
		for left := range outcomes {
			if left.exited {
				next[left] = true // 提前退出路径：后续语句不可达，保留原样
				continue
			}
			for right := range statementOutcomes {
				sum := left.count + right.count
				if sum > 2 {
					sum = 2
				}
				next[yieldOutcome{count: sum, exited: right.exited}] = true
			}
		}
		outcomes = next
	}
	return outcomes
}

// statementOutcomes computes the outcome set for one statement, mirroring
// _statement_outcomes (yield_contract_pass.py:82-108).
func (p *YieldContractPass) statementOutcomes(statement ast.Stmt, ctx *Context) map[yieldOutcome]bool {
	switch s := statement.(type) {
	case *ast.YieldStmt:
		return map[yieldOutcome]bool{{count: 1}: true}
	case *ast.BreakStmt:
		// coze loop Break（type 19）：提前终止循环，该轮跳过末尾 yield。
		// 合法（不要求前置 if），exited 路径豁免 "至少一个 yield"。
		return map[yieldOutcome]bool{{count: 0, exited: true}: true}
	case *ast.ContinueStmt:
		// coze loop Continue（type 29）：直接进入下一轮迭代，跳过末尾 yield。
		return map[yieldOutcome]bool{{count: 0, exited: true}: true}
	case *ast.ReturnStmt:
		p.emit(statement, "return cannot exit a value-producing for")
		return map[yieldOutcome]bool{{count: 0, exited: true}: true}
	case *ast.IfStmt:
		outcomes := map[yieldOutcome]bool{}
		merge := func(block *ast.Block) {
			for value := range p.blockOutcomes(block, ctx) {
				// count 恒 0：臂内 yield 已由位置判据拒绝（visitForExpr），
				// 不构成路径发布。保留 exited 轴（break/continue 仍合法，
				// 提前退出路径豁免「至少一个 yield」）。
				outcomes[yieldOutcome{exited: value.exited}] = true
			}
		}
		merge(s.ThenBody())
		for _, branch := range s.ElseIfs() {
			merge(branch.Body())
		}
		if s.ElseBody() != nil {
			merge(s.ElseBody())
		} else {
			outcomes[yieldOutcome{count: 0}] = true
		}
		return outcomes
	case *ast.SwitchStmt:
		outcomes := map[yieldOutcome]bool{}
		for _, br := range s.Branches() {
			for value := range p.blockOutcomes(br.Body(), ctx) {
				outcomes[yieldOutcome{exited: value.exited}] = true
			}
		}
		if s.DefaultBody() != nil {
			for value := range p.blockOutcomes(s.DefaultBody(), ctx) {
				outcomes[yieldOutcome{exited: value.exited}] = true
			}
		} else {
			outcomes[yieldOutcome{count: 0}] = true
		}
		return outcomes
	}
	return map[yieldOutcome]bool{{count: 0}: true}
}

// emit mirrors _emit (yield_contract_pass.py:110-112): E1043 on the node's
// span.
func (p *YieldContractPass) emit(node ast.Node, message string) {
	p.ctx.Emit(diag.YIELD_CONTRACT, diag.Error, message, node.Span())
}

// nestedYieldFinder 报告「嵌在别的语句里」的 yield（位置判据的执行体）。
//
// 下降规则与设计一致：内层 value-producing for（ForExpr / ParallelForExpr）的
// yield 属于**内层**体，由内层自己的 visitForExpr 判 —— 故不再下降。其它一切
// 语句（if / switch / parallel / while / attempt / ask / 语句形态 for / …）
// 都下降：它们内部的 yield 都不可能是本循环体的直接语句。
type nestedYieldFinder struct {
	pass *YieldContractPass
}

func (f *nestedYieldFinder) Visit(n ast.Node) ast.Visitor {
	switch x := n.(type) {
	case *ast.ForExpr, *ast.ParallelForExpr:
		return nil // 内层循环自有其 yield 契约
	case *ast.YieldStmt:
		f.pass.emit(x, "yield must be a direct statement of the value-producing for body "+
			"(it cannot be nested inside another statement; publish a conditional value with "+
			"`yield (cond) ? a : b;` instead)")
		return nil
	}
	return f
}

// hasNewErrorSince reports whether any error diagnostic was added after the
// before index (mirrors the reference's
// `any(d.severity == ERROR for d in ctx.diagnostics[before:])`).
func hasNewErrorSince(diagnostics []diag.Diagnostic, before int) bool {
	for _, d := range diagnostics[before:] {
		if d.Severity == diag.Error {
			return true
		}
	}
	return false
}
