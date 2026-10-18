package passes

// Syntax pass — syntax-level validation (dead code, missing return),
// ported 1:1 from src/nclang/lang/passes/syntax_pass.py (73 lines):
//
//	SyntaxPass.run         :25-51
//	SyntaxPass._check_dead_code :53-73
//
// E1010 MISSING_RETURN and W2001 DEAD_CODE messages are byte-for-byte the
// Python f-string results (syntax_pass.py:36-39 and 55-58).
//
// D1 (return-type resolution): Python resolves the function return type
// with type_ref_to_descriptor(fn.return_type) and NO type_env
// (syntax_pass.py:33), so user-defined named types fall back to ANY there
// and only the base VOID distinction matters. The Go port mirrors this
// exactly by passing a nil lookup to types.TypeRefToDescriptor.

import (
	"fmt"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/types"
)

// SyntaxPass performs syntax-level validation after the scope and type
// passes: value-returning functions must not have a reachable fall-through
// path (E1010), and statements after a return are dead (W2001).
type SyntaxPass struct{}

// Run mirrors SyntaxPass.run (syntax_pass.py:25-51): the MISSING_RETURN
// check runs over entry + functions in that order, then dead-code
// recursion over entry and function bodies.
func (SyntaxPass) Run(program *ast.Program, ctx *Context) {
	functions := []*ast.FunctionDef{}
	if program.Entry() != nil {
		functions = append(functions, program.Entry())
	}
	functions = append(functions, program.Functions()...)
	for _, fn := range functions {
		var retType *types.Descriptor
		if fn.ReturnType() != nil {
			retType = types.TypeRefToDescriptor(fn.ReturnType(), nil)
		}
		if retType != nil && retType.Kind != types.Void && AnalyzeBlock(fn.Body()).CanFallThrough {
			ctx.Emit(diag.MISSING_RETURN, diag.Error,
				fmt.Sprintf("Function '%s' may not return a value on all paths", fn.Name()),
				fn.Span())
		}
	}

	if program.Entry() != nil {
		checkDeadCode(program.Entry().Body(), ctx)
	}
	for _, fn := range program.Functions() {
		checkDeadCode(fn.Body(), ctx)
	}
}

// checkDeadCode warns about statements after a return, recursing into the
// nested statement lists Python's _check_dead_code visits (syntax_pass.py:
// 53-73). Note the reference does NOT recurse into else-if branches — only
// then/else bodies, loop bodies, attempt bodies and request-input branches
// (the port matches that surface exactly).
func checkDeadCode(block *ast.Block, ctx *Context) {
	stmts := block.Stmts()
	for i, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ReturnStmt:
			if i < len(stmts)-1 {
				ctx.Emit(diag.DEAD_CODE, diag.Warning,
					"Unreachable code after return", stmts[i+1].Span())
			}
		case *ast.IfStmt:
			checkDeadCode(s.ThenBody(), ctx)
			if s.ElseBody() != nil {
				checkDeadCode(s.ElseBody(), ctx)
			}
		case *ast.ForStmt:
			checkDeadCode(s.Body(), ctx)
		case *ast.WhileStmt:
			checkDeadCode(s.Body(), ctx)
		case *ast.AttemptStmt:
			checkDeadCode(s.SuccessBody(), ctx)
			checkDeadCode(s.FailureBody(), ctx)
		case *ast.RequestInputStmt:
			for _, branch := range s.ActionBranches() {
				checkDeadCode(branch.Body(), ctx)
			}
			if tb := s.TimeoutBody(); tb != nil {
				checkDeadCode(tb, ctx)
			}
		case *ast.SwitchStmt:
			for _, branch := range s.Branches() {
				checkDeadCode(branch.Body(), ctx)
			}
			if s.DefaultBody() != nil {
				checkDeadCode(s.DefaultBody(), ctx)
			}
		}
	}
}
