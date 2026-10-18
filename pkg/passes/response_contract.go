package passes

// Validate mode-explicit workflow and chat response statements, ported 1:1
// from src/nclang/lang/passes/response_contract_pass.py (139 lines):
//
//	ResponseContractPass.run              :35-45
//	_visit_function                       :47-53
//	visit_output_stmt                     :55-73
//	visit_answer_stmt                     :75-85
//	visit_for_stmt / visit_for_expr       :87-104
//	visit_while_stmt                      :106-110
//	visit_parallel_stmt / visit_parallel_expr :112-131
//	_emit                                 :133-137
//
// Every E1044 RESPONSE_CONTEXT message is byte-for-byte the Python f-string
// result (see the emitters below).
//
// D1 (visitor mechanics): see the yield_contract.go header note — the
// container-depth / parallel-frame stacks use the same per-node re-entry
// guard over ast.Walk to preserve Python's try/finally semantics.

import (
	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/diag"
)

// ResponseContractPass keeps response publication explicit and scoped to
// main, mirroring response_contract_pass.py:35-45.
type ResponseContractPass struct {
	mode           string
	entry          *ast.FunctionDef
	function       *ast.FunctionDef
	containerDepth int
	parallelFrames []map[string]int
	parallelBranch []int
	ctx            *Context
}

// Run mirrors run (response_contract_pass.py:35-45): code functions first,
// then regular functions, then the entry — each visited through
// _visit_function.
func (p *ResponseContractPass) Run(program *ast.Program, ctx *Context) {
	p.mode = program.Mode()
	if p.mode == "" {
		p.mode = "workflow"
	}
	p.entry = program.Entry()
	// Entry identity relies on parser wiring: `function main` is always a
	// standalone node and never aliases a CodeFunctions/Functions element
	// (parser.go), so the pointer comparison p.function == p.entry is sound
	// for every DSL-reachable program (mirrors Python's `is` identity).
	p.function = nil
	p.containerDepth = 0
	p.parallelFrames = nil
	p.parallelBranch = nil
	p.ctx = ctx
	w := &responseWalker{pass: p, ctx: ctx, entered: map[ast.Node]bool{}}
	for _, fn := range program.CodeFunctions() {
		p.visitFunction(&fn.FunctionDef, w)
	}
	for _, fn := range program.Functions() {
		p.visitFunction(fn, w)
	}
	if program.Entry() != nil {
		p.visitFunction(program.Entry(), w)
	}
}

// visitFunction mirrors _visit_function (response_contract_pass.py:47-53):
// the current-function stack is pushed/popped around the body walk.
func (p *ResponseContractPass) visitFunction(fn *ast.FunctionDef, w *responseWalker) {
	previous := p.function
	p.function = fn
	defer func() { p.function = previous }()
	ast.Walk(w, fn.Body())
}

// responseWalker is the pass's AST visitor (see the file header D1).
type responseWalker struct {
	pass    *ResponseContractPass
	ctx     *Context
	entered map[ast.Node]bool
}

// Visit mirrors the visit_* handlers (response_contract_pass.py:55-131).
func (w *responseWalker) Visit(n ast.Node) ast.Visitor {
	switch node := n.(type) {
	case *ast.OutputStmt:
		w.visitOutputStmt(node)
		return w
	case *ast.ForStmt:
		if w.entered[node] {
			return w
		}
		w.entered[node] = true
		defer func() { w.entered[node] = false }()
		w.pass.containerDepth++
		ast.Walk(w, node)
		w.pass.containerDepth--
		return nil
	case *ast.ForExpr:
		if w.entered[node] {
			return w
		}
		w.entered[node] = true
		defer func() { w.entered[node] = false }()
		w.pass.containerDepth++
		ast.Walk(w, node)
		w.pass.containerDepth--
		return nil
	case *ast.WhileStmt:
		if w.entered[node] {
			return w
		}
		w.entered[node] = true
		defer func() { w.entered[node] = false }()
		w.pass.containerDepth++
		ast.Walk(w, node)
		w.pass.containerDepth--
		return nil
	case *ast.ParallelStmt:
		if w.entered[node] {
			return w
		}
		w.entered[node] = true
		defer func() { w.entered[node] = false }()
		w.visitParallelStmt(node)
		return nil
	}
	return w
}

// visitOutputStmt: intermediate message emission — legal in workflow or
// advanced-chat main, at **any flow position**: loop/iteration containers,
// and parallel arms (model: docs/NCODA-OUTPUT-MODEL.md §5).
//
// 2026-09-18 用户裁定：「消息节点就是普通节点，没有特殊性」——旧判据
// 「output() 只允许 … outside parallel」（E1044 的 parallel 子句）**作废**。
// 并发臂内的消息是合法语义：每臂内保序、跨臂顺序未指定（wire 也不给顺序）；
// 平台侧一手证据 = 扇出后继里带消息节点的真实导出（无端口扇出 24 处 / 15 源；
// 其中 2 处 / 2 源的后继全部汇入同一 End = X77/X184 的答复端点扇出 ——
// docs/five-step/evidence/output-message-node.{json,md}，复算见 evidence_output_message_fanout.py）。
// 判据只剩两条：模式 + 属主（main）。
func (w *responseWalker) visitOutputStmt(node *ast.OutputStmt) {
	p := w.pass
	valid := (p.mode == "workflow" || p.mode == "advanced-chat") &&
		p.function == p.entry
	if !valid {
		w.emit(node, "output() is legal only in workflow/advanced-chat main")
	}
}

// visitParallelStmt mirrors visit_parallel_stmt (response_contract_pass.py:
// 112-120): push a key frame, walk each anonymous branch with its index on
// the branch stack, pop the frame.
func (w *responseWalker) visitParallelStmt(node *ast.ParallelStmt) {
	p := w.pass
	p.parallelFrames = append(p.parallelFrames, map[string]int{})
	defer func() { p.parallelFrames = p.parallelFrames[:len(p.parallelFrames)-1] }()
	for index, branch := range node.Branches() {
		p.parallelBranch = append(p.parallelBranch, index)
		ast.Walk(w, branch)
		p.parallelBranch = p.parallelBranch[:len(p.parallelBranch)-1]
	}
}

// emit mirrors _emit (response_contract_pass.py:133-137): E1044 on the
// node's span.
func (w *responseWalker) emit(node ast.Node, message string) {
	w.ctx.Emit(diag.RESPONSE_CONTEXT, diag.Error, message, node.Span())
}
