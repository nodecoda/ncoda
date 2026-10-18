package passes

// Effect classification and explicit workflow-operation ordering, ported
// line-for-line from src/nclang/lang/passes/effect_pass.py (EffectPass
// :38-274, E1041 EFFECT_CONTEXT). It publishes the immutable EffectFacts
// (node + function effects) and rejects operations evaluated in any
// position other than a complete `let` initializer or a dedicated
// expression statement.
//
// Direct effect rule (effect_pass.py:38-56): a function is directly a
// WORKFLOW_OPERATION when its body contains a ForeignCodeExpr or a call to
// an operation builtin (CallTargets is a BuiltinCallTarget whose name is in
// ctx.Profile.Operations). Function effects are then propagated through the
// user call graph; code functions are forced to CALCULATION
// (effect_pass.py:70-74) so illegal operations inside a code body are
// diagnosed locally rather than cascading into every caller.
//
// Emission determinism (brief): statements are validated in statement order
// and each statement's expressions are walked in ast.Walk pre-order, the
// Go analogue of the reference _iter_expressions dataclass-field order.

import (
	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
)

// EffectPass implements the effect stage (effect_pass.py:38).
type EffectPass struct{}

// Run publishes the effect facts and validates every function body
// (effect_pass.py:42-89).
func (EffectPass) Run(p *ast.Program, ctx *Context) {
	if ctx.Bindings == nil {
		panic("passes: EffectPass requires a published binding snapshot (run ScopePass first)")
	}
	snap := ctx.Bindings
	graph, _ := binding.BuildUserCallGraph(p, snap)

	// function_nodes = {function_id: node_id} (effect_pass.py:43-45).
	functionNodes := map[binding.FunctionID]binding.AstNodeID{}
	snap.Functions().Range(func(nodeID binding.AstNodeID, fid binding.FunctionID) bool {
		functionNodes[fid] = nodeID
		return true
	})

	// functions_by_node = {ast_id(function): function} for code functions,
	// functions and the entry (effect_pass.py:46-50).
	functionsByNode := map[binding.AstNodeID]ast.Node{}
	addFunction := func(fn ast.Node) {
		if id, ok := snap.NodeIDOf(fn); ok {
			functionsByNode[id] = fn
		}
	}
	for _, cf := range p.CodeFunctions() {
		addFunction(cf)
	}
	for _, f := range p.Functions() {
		addFunction(f)
	}
	if e := p.Entry(); e != nil {
		addFunction(e)
	}

	// direct_effects (effect_pass.py:51-60).
	directEffects := map[binding.FunctionID]binding.EffectKind{}
	for functionID, nodeID := range functionNodes {
		fn, ok := functionsByNode[nodeID]
		if !ok {
			// Python raises KeyError here; unreachable in practice because
			// both sides cover code functions + functions + entry.
			panic("passes: function binding has no AST function node")
		}
		effect := binding.EffectCalculation
		if anyDirectOperation(iterExprs(functionBody(fn)), snap, ctx.Profile) {
			effect = binding.EffectWorkflowOperation
		}
		directEffects[functionID] = effect
	}

	// function_effects = propagate + force code functions to CALCULATION
	// (effect_pass.py:62-74).
	functionEffects := binding.PropagateFunctionEffects(graph, directEffects)
	codeFunctionIDs := map[binding.FunctionID]bool{}
	for _, cf := range p.CodeFunctions() {
		if id, ok := snap.NodeIDOf(cf); ok {
			if fid, ok := snap.Functions().Get(id); ok {
				codeFunctionIDs[fid] = true
			}
		}
	}
	for functionID := range codeFunctionIDs {
		functionEffects[functionID] = binding.EffectCalculation
	}

	// node_effects: every expression in the program, walked in pre-order
	// (effect_pass.py:76-82).
	nodeEffects := map[binding.AstNodeID]binding.EffectKind{}
	for _, expr := range iterExprs(p) {
		if id, ok := snap.NodeIDOf(expr); ok {
			nodeEffects[id] = expressionEffect(expr, snap, functionEffects, ctx.Profile)
		}
	}

	ctx.EffectFacts = binding.NewEffectFacts(nodeEffects, functionEffects)
	facts := ctx.EffectFacts

	// Validation (effect_pass.py:84-89): functions, then entry, then
	// code functions which reject every operation outright.
	for _, fn := range p.Functions() {
		validateBlock(fn.Body(), facts, ctx)
	}
	if e := p.Entry(); e != nil {
		validateBlock(e.Body(), facts, ctx)
	}
	for _, cf := range p.CodeFunctions() {
		rejectAllOperations(cf.Body(), facts, ctx)
	}
}

// anyDirectOperation mirrors the any(...) in effect_pass.py:54.
func anyDirectOperation(exprs []ast.Expr, snap *binding.Snapshot, profile *TargetProfile) bool {
	for _, expr := range exprs {
		if isDirectOperation(expr, snap, profile) {
			return true
		}
	}
	return false
}

// isDirectOperation mirrors EffectPass._is_direct_operation
// (effect_pass.py:91-101): ForeignCodeExpr is always an operation; a
// CallExpr is an operation when its call target is a BuiltinCallTarget
// whose name is in the target profile's operation registry.
func isDirectOperation(expr ast.Expr, snap *binding.Snapshot, profile *TargetProfile) bool {
	if _, ok := expr.(*ast.ForeignCodeExpr); ok {
		return true
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := snap.NodeIDOf(call)
	if !ok {
		return false
	}
	target, found := snap.CallTargets().Get(id)
	if !found {
		return false
	}
	return target.Kind == binding.CallBuiltin && profile.Operations.Has(target.Name)
}

// expressionEffect mirrors EffectPass._expression_effect
// (effect_pass.py:103-117): ForeignCodeExpr → WORKFLOW_OPERATION; a
// user-function call takes the callee's propagated effect; an operation
// builtin call is WORKFLOW_OPERATION; everything else is CALCULATION.
func expressionEffect(expr ast.Expr, snap *binding.Snapshot, functionEffects map[binding.FunctionID]binding.EffectKind, profile *TargetProfile) binding.EffectKind {
	if _, ok := expr.(*ast.ForeignCodeExpr); ok {
		return binding.EffectWorkflowOperation
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return binding.EffectCalculation
	}
	id, ok := snap.NodeIDOf(call)
	if !ok {
		return binding.EffectCalculation
	}
	target, found := snap.CallTargets().Get(id)
	if !found {
		return binding.EffectCalculation
	}
	switch target.Kind {
	case binding.CallFunction:
		if eff, ok := functionEffects[target.Function]; ok {
			return eff
		}
		return binding.EffectCalculation
	case binding.CallBuiltin:
		if profile.Operations.Has(target.Name) {
			return binding.EffectWorkflowOperation
		}
	}
	return binding.EffectCalculation
}

// validateBlock mirrors EffectPass._validate_block (effect_pass.py:119-181).
func validateBlock(block *ast.Block, facts *binding.EffectFacts, ctx *Context) {
	if block == nil {
		return
	}
	for _, stmt := range block.Stmts() {
		switch s := stmt.(type) {
		case *ast.VarDeclStmt:
			if s.Init() == nil {
				continue
			}
			if forExpr, ok := s.Init().(*ast.ForExpr); ok {
				for _, src := range forExpr.LoopSources() {
					rejectExpressionOperations(src, facts, ctx)
				}
				validateBlock(forExpr.Body(), facts, ctx)
				continue
			}
			if pf, ok := s.Init().(*ast.ParallelForExpr); ok {
				// ast.py ParallelForExpr(ForExpr): the Python
				// isinstance(init, ForExpr) branch (effect_pass.py:144-146)
				// also matches parallel-for, so the Go analogue must handle
				// it explicitly (Go has no type inheritance).
				for _, src := range pf.LoopSources() {
					rejectExpressionOperations(src, facts, ctx)
				}
				validateBlock(pf.Body(), facts, ctx)
				continue
			}
			rootOperation := isOperation(s.Init(), facts, ctx)
			if rootOperation && !s.IsMutable() {
				rejectNestedOperations(s.Init(), facts, ctx)
			} else {
				rejectExpressionOperations(s.Init(), facts, ctx)
			}
		case *ast.ExprStmt:
			if s.Expr() == nil {
				continue
			}
			if isOperation(s.Expr(), facts, ctx) {
				rejectNestedOperations(s.Expr(), facts, ctx)
			} else {
				rejectExpressionOperations(s.Expr(), facts, ctx)
			}
		case *ast.AssignStmt:
			rejectExpressionOperations(s.Target(), facts, ctx)
			rejectExpressionOperations(s.Value(), facts, ctx)
		case *ast.IfStmt:
			rejectExpressionOperations(s.Condition(), facts, ctx)
			validateBlock(s.ThenBody(), facts, ctx)
			for _, elseIf := range s.ElseIfs() {
				validateElseIf(elseIf, facts, ctx)
			}
			validateBlock(s.ElseBody(), facts, ctx)
		case *ast.ForStmt:
			for _, src := range s.LoopSources() {
				rejectExpressionOperations(src, facts, ctx)
			}
			validateBlock(s.Body(), facts, ctx)
		case *ast.WhileStmt:
			rejectExpressionOperations(s.Condition(), facts, ctx)
			validateBlock(s.Body(), facts, ctx)
		case *ast.ParallelStmt:
			for _, branch := range s.Branches() {
				validateBlock(branch, facts, ctx)
			}
		case *ast.SwitchStmt:
			rejectExpressionOperations(s.Query(), facts, ctx)
			for _, br := range s.Branches() {
				validateBlock(br.Body(), facts, ctx)
			}
			if s.DefaultBody() != nil {
				validateBlock(s.DefaultBody(), facts, ctx)
			}
		case *ast.AttemptStmt:
			if s.Operation() != nil {
				operation := s.Operation()
				// Mirror Python's unconditional unwrap: an
				// OperationPolicyExpr with a nil inner operation yields
				// None and skips the scan entirely (effect_pass.py:159-165).
				if opExpr, ok := operation.(*ast.OperationPolicyExpr); ok {
					operation = opExpr.Operation()
				}
				if operation != nil {
					rejectNestedOperations(operation, facts, ctx)
				}
			}
			validateBlock(s.SuccessBody(), facts, ctx)
			validateBlock(s.FailureBody(), facts, ctx)
		case *ast.ReturnStmt, *ast.YieldStmt:
			rejectExpressionOperations(stmtValue(stmt), facts, ctx)
		case *ast.OutputStmt:
			rejectExpressionOperations(s.Value(), facts, ctx)
		}
	}
}

// validateElseIf mirrors EffectPass._validate_else_if (effect_pass.py:183-186).
func validateElseIf(branch *ast.ElseIfBranch, facts *binding.EffectFacts, ctx *Context) {
	rejectExpressionOperations(branch.Condition(), facts, ctx)
	validateBlock(branch.Body(), facts, ctx)
}

// rejectAllOperations mirrors EffectPass._reject_all_operations
// (effect_pass.py:188-192): every operation inside a code body is rejected.
func rejectAllOperations(body *ast.Block, facts *binding.EffectFacts, ctx *Context) {
	for _, expr := range iterExprs(body) {
		if isOperation(expr, facts, ctx) {
			emitEffectContext(expr, ctx)
		}
	}
}

// rejectExpressionOperations is kept as the §5.3 widening point: bare
// operation calls (llm/http/knowledge/...) inside expressions are now
// ALLOWED —
// the lowering hoist materializes them into producer nodes
// (hoistUserCalls → materializeCall, §5.3/§5.2). The gate remains ONLY for
// strategy contexts (attempt/parallel) and code-function bodies
// (rejectNestedOperations / rejectAllOperations). This is a no-op on
// purpose; removing it entirely would break the reference-parity call
// graph, keeping it documents the widening.
func rejectExpressionOperations(expr ast.Expr, facts *binding.EffectFacts, ctx *Context) {
	// §5.3: bare operation calls in expressions are materialized by the
	// lowering hoist; no type-level rejection here.
	_ = expr
	_ = facts
	_ = ctx
}

// rejectNestedOperations mirrors EffectPass._reject_nested_operations
// (effect_pass.py:201-207): only the CHILDREN of the expression are
// scanned (the root was already accepted as an explicit operation position).
func rejectNestedOperations(expr ast.Expr, facts *binding.EffectFacts, ctx *Context) {
	if opExpr, ok := expr.(*ast.OperationPolicyExpr); ok && opExpr.Operation() != nil {
		expr = opExpr.Operation()
	}
	for _, child := range iterExprChildren(expr) {
		if isOperation(child, facts, ctx) {
			emitEffectContext(child, ctx)
		}
	}
}

// isOperation mirrors EffectPass._is_operation (effect_pass.py:209-214):
// an OperationPolicyExpr delegates to its inner operation; otherwise the
// node's published effect decides.
func isOperation(expr ast.Expr, facts *binding.EffectFacts, ctx *Context) bool {
	if opExpr, ok := expr.(*ast.OperationPolicyExpr); ok && opExpr.Operation() != nil {
		return isOperation(opExpr.Operation(), facts, ctx)
	}
	id, ok := ctx.Bindings.NodeIDOf(expr)
	if !ok {
		return false
	}
	eff, found := facts.NodeEffects().Get(id)
	return found && eff == binding.EffectWorkflowOperation
}

// emitEffectContext mirrors EffectPass._emit (effect_pass.py:216-220).
func emitEffectContext(expr ast.Expr, ctx *Context) {
	ctx.Emit(diag.EFFECT_CONTEXT, diag.Error,
		"Workflow operation must be a complete let initializer or dedicated statement",
		expr.Span())
}

// stmtValue returns the single value expression of return/yield/answer
// statements (they share the same shape).
func stmtValue(stmt ast.Stmt) ast.Expr {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		return s.Value()
	case *ast.YieldStmt:
		return s.Value()
	}
	return nil
}

// ── expression iteration (effect_pass.py _iter_expressions) ───────────

// exprCollector collects every Expr node under a subtree in ast.Walk
// pre-order (root first), mirroring Python _iter_expressions which yields
// an Expr before recursing into its dataclass fields.
type exprCollector struct {
	out []ast.Expr
}

func (c *exprCollector) Visit(n ast.Node) ast.Visitor {
	if expr, ok := n.(ast.Expr); ok {
		c.out = append(c.out, expr)
	}
	return c
}

// iterExprs returns every expression under n in pre-order, mirroring
// _iter_expressions (effect_pass.py:268-274). Walking a Block (or the
// whole program) yields statements' expressions in statement order; walking
// an expression yields the expression itself first.
func iterExprs(n ast.Node) []ast.Expr {
	c := &exprCollector{}
	ast.Walk(c, n)
	return c.out
}

// exprChildCollector is iterExprChildren's visitor: the root node is
// visited first and skipped, so only the descendants are collected
// (mirroring _iter_expression_children, effect_pass.py:263-266).
type exprChildCollector struct {
	first bool
	out   []ast.Expr
}

func (c *exprChildCollector) Visit(n ast.Node) ast.Visitor {
	if c.first {
		c.first = false
		return c
	}
	if expr, ok := n.(ast.Expr); ok {
		c.out = append(c.out, expr)
	}
	return c
}

// iterExprChildren returns every expression strictly below n in pre-order,
// mirroring _iter_expression_children (effect_pass.py:263-266).
func iterExprChildren(n ast.Node) []ast.Expr {
	c := &exprChildCollector{first: true}
	ast.Walk(c, n)
	return c.out
}

// functionBody returns the body Block of a FunctionDef or CodeFunctionDef
// node.
func functionBody(fn ast.Node) *ast.Block {
	switch f := fn.(type) {
	case *ast.FunctionDef:
		return f.Body()
	case *ast.CodeFunctionDef:
		return f.Body()
	}
	return nil
}
