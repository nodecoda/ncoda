package ast

import "reflect"

// This file ports the traversal surface of ast.py ProcASTVisitor (visit +
// generic_visit) to Go. The Go design follows the go/ast convention: a
// Visitor returns the visitor to use for children (nil prunes the subtree).
//
// Unlike the Python reference visitor — which omits several child lists
// (const_decls, outputs, params, return types, type refs, call type_args) —
// Walk visits every child node, so a full pass can enumerate the whole tree
// (see TestWalkEnumeratesEveryConstructedNode).

// Visitor is implemented by traversers. Visit is called for each node;
// returning nil prunes the node's subtree, returning a visitor (usually
// itself) continues the walk with that visitor for the children.
type Visitor interface {
	Visit(n Node) Visitor
}

// Walk traverses the AST rooted at n in depth-first pre-order. It is the Go
// equivalent of ProcASTVisitor.visit/generic_visit with exhaustive child
// coverage. Walking a nil node is a no-op.
func Walk(v Visitor, n Node) {
	if n == nil {
		return
	}
	// A typed nil child (e.g. IfStmt.elseBody *Block(nil)) is a non-nil
	// interface; the comparison above cannot see it. Guard against it so a
	// nil typed pointer never reaches the type switch (where range over a
	// nil slice field would panic).
	if rv := reflect.ValueOf(n); rv.Kind() == reflect.Ptr && rv.IsNil() {
		return
	}
	v = v.Visit(n)
	if v == nil {
		return
	}
	switch n := n.(type) {
	// ── program / declarations ─────────────────────────────────────
	case *Program:
		for _, i := range n.imports {
			Walk(v, i)
		}
		for _, c := range n.constDecls {
			Walk(v, c)
		}
		for _, o := range n.outputs {
			Walk(v, o)
		}
		for _, e := range n.enumDecls {
			Walk(v, e)
		}
		for _, t := range n.typeDecls {
			Walk(v, t)
		}
		for _, c := range n.conversationDecls {
			Walk(v, c)
		}
		for _, f := range n.codeFunctions {
			Walk(v, f)
		}
		for _, f := range n.functions {
			Walk(v, f)
		}
		if n.entry != nil {
			Walk(v, n.entry)
		}
	case *ImportDecl:
		// leaf: no children
	case *ConstDecl:
		Walk(v, n.value)
	case *OutputDecl:
		Walk(v, n.typeRef)
	case *EnumDecl:
		for _, m := range n.members {
			Walk(v, m)
		}
	case *EnumMemberDecl:
		// leaf
	case *TypeDecl:
		for _, f := range n.fields {
			Walk(v, f)
		}
	case *FieldDecl:
		Walk(v, n.typeRef)
	case *ConversationVarDecl:
		Walk(v, n.typeRef)
		Walk(v, n.default_)
	case *ParamDecl:
		Walk(v, n.typeRef)
		Walk(v, n.default_)
	case *FunctionDef:
		for _, p := range n.params {
			Walk(v, p)
		}
		Walk(v, n.returnType)
		Walk(v, n.body)
	case *CodeFunctionDef:
		for _, p := range n.params {
			Walk(v, p)
		}
		Walk(v, n.returnType)
		Walk(v, n.body)

	// ── statements ─────────────────────────────────────────────────
	case *Block:
		for _, s := range n.stmts {
			Walk(v, s)
		}
	case *VarDeclStmt:
		Walk(v, n.typeRef)
		Walk(v, n.init)
	case *AssignStmt:
		Walk(v, n.target)
		Walk(v, n.value)
	case *ExprStmt:
		Walk(v, n.expr)
	case *IfStmt:
		Walk(v, n.condition)
		Walk(v, n.thenBody)
		for _, ei := range n.elseIfs {
			Walk(v, ei)
		}
		Walk(v, n.elseBody)
	case *ElseIfBranch:
		Walk(v, n.condition)
		Walk(v, n.body)
	case *ForStmt:
		for i, src := range n.LoopSources() {
			Walk(v, src)
			if i < len(n.LoopTypes()) {
				Walk(v, n.LoopTypes()[i])
			}
		}
		Walk(v, n.body)
	case *WhileStmt:
		Walk(v, n.condition)
		Walk(v, n.body)
	case *ReturnStmt:
		Walk(v, n.value)
	case *TemplateValue:
		for _, b := range n.bindings {
			Walk(v, b.Value)
		}
	case *YieldStmt:
		Walk(v, n.value)
	case *OutputStmt:
		Walk(v, n.value)
	case *ParallelStmt:
		for _, b := range n.branches {
			Walk(v, b)
		}
	case *AttemptStmt:
		Walk(v, n.operation)
		Walk(v, n.resultBinding)
		Walk(v, n.successBody)
		Walk(v, n.errorBinding)
		Walk(v, n.failureBody)
	case *HumanActionBranch:
		Walk(v, n.actionExpr)
		Walk(v, n.body)
	case *RequestInputStmt:
		Walk(v, n.formType)
		Walk(v, n.actionType)
		Walk(v, n.config)
		Walk(v, n.resultBinding)
		for _, br := range n.actionBranches {
			Walk(v, br)
		}
		Walk(v, n.timeoutBody)
	case *SwitchBranch:
		Walk(v, n.body)
	case *SwitchStmt:
		Walk(v, n.query)
		Walk(v, n.config)
		for _, br := range n.branches {
			Walk(v, br)
		}
		if n.defBody != nil {
			Walk(v, n.defBody)
		}
		Walk(v, n.result)
	case *BreakStmt, *ContinueStmt:
		// leaf

	// ── expressions ────────────────────────────────────────────────
	case *TemplateString:
		for _, part := range n.parts {
			Walk(v, part.Expr)
		}
	case *ArrayLit:
		for _, e := range n.elements {
			Walk(v, e)
		}
	case *MapLit:
		for _, en := range n.entries {
			Walk(v, en.Key)
			Walk(v, en.Value)
		}
	case *FieldAccessExpr:
		Walk(v, n.obj)
	case *IndexExpr:
		Walk(v, n.obj)
		Walk(v, n.index)
	case *CallExpr:
		Walk(v, n.func_)
		for _, t := range n.typeArgs {
			Walk(v, t)
		}
		for _, a := range n.args {
			Walk(v, a)
		}
	case *OperationPolicyExpr:
		Walk(v, n.operation)
		for _, p := range n.policies {
			Walk(v, p)
		}
	case *RetryPolicy, *TimeoutPolicy:
		// leaf
	case *DefaultPolicy:
		Walk(v, n.value)
	case *UnaryExpr:
		Walk(v, n.operand)
	case *BinaryExpr:
		Walk(v, n.left)
		Walk(v, n.right)
	case *TernaryExpr:
		Walk(v, n.condition)
		Walk(v, n.thenVal)
		Walk(v, n.elseVal)
	case *LambdaExpr:
		Walk(v, n.body)
	case *ForExpr:
		for i, src := range n.LoopSources() {
			Walk(v, src)
			if i < len(n.LoopTypes()) {
				Walk(v, n.LoopTypes()[i])
			}
		}
		Walk(v, n.body)
	case *ParallelForExpr:
		// Shares ForExpr children (sources/types/body); concurrency and
		// on_error are plain values.
		for i, src := range n.LoopSources() {
			Walk(v, src)
			if i < len(n.LoopTypes()) {
				Walk(v, n.LoopTypes()[i])
			}
		}
		Walk(v, n.body)
	case *CodeInputBinding:
		Walk(v, n.typeRef)
		Walk(v, n.value)
	case *CodeDirectOutput:
		Walk(v, n.typeRef)
	case *CodeOutputField:
		Walk(v, n.typeRef)
	case *CodeStructuralOutput:
		for _, f := range n.fields {
			Walk(v, f)
		}
	case *ForeignCodeExpr:
		for _, in := range n.inputs {
			Walk(v, in)
		}
		Walk(v, n.output)
		Walk(v, n.source)

	// ── type references ────────────────────────────────────────────
	case *ArrayTypeRef:
		Walk(v, n.element)
	case *MapTypeRef:
		Walk(v, n.key)
		Walk(v, n.value)
	case *OptionalTypeRef:
		Walk(v, n.value)
	case *PrimTypeRef, *FileTypeRef, *NamedTypeRef:
		// leaf

	// ── literals / identifiers (leaf) ──────────────────────────────
	case *IntLit, *FloatLit, *StringLit, *BoolLit, *NullLit, *IdentExpr:
		// leaf
	}
}
