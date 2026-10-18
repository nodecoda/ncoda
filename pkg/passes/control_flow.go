package passes

// Pure control-flow facts for the NodeCoda AST, ported 1:1 from
// src/nclang/lang/control_flow.py (83 lines):
//
//	ExitFacts        :22-27   ExitFacts
//	analyze_block    :30-40   AnalyzeBlock
//	analyze_statement:43-80   AnalyzeStatement
//
// Both functions are pure (no diagnostics, no context); the syntax pass
// consumes them for the MISSING_RETURN E1010 check (syntax_pass.py:32-42).

import "github.com/nodecoda/ncoda/pkg/ast"

// ExitFacts reports whether a statement can continue past it (fall through)
// and whether it contains a reachable return, mirroring the frozen
// ExitFacts dataclass (control_flow.py:22-27).
type ExitFacts struct {
	CanFallThrough bool
	HasReturn      bool
}

// AnalyzeBlock analyzes sequential reachability without target-platform
// assumptions (control_flow.py:30-40): each statement's facts update the
// running state, and analysis stops at the first statement that cannot
// fall through.
func AnalyzeBlock(block *ast.Block) ExitFacts {
	canFallThrough := true
	hasReturn := false
	for _, statement := range block.Stmts() {
		if !canFallThrough {
			break
		}
		facts := AnalyzeStatement(statement)
		canFallThrough = facts.CanFallThrough
		hasReturn = hasReturn || facts.HasReturn
	}
	return ExitFacts{CanFallThrough: canFallThrough, HasReturn: hasReturn}
}

// AnalyzeStatement returns conservative exit facts for one statement
// (control_flow.py:43-80):
//
//	Return             -> no fall-through, has return
//	Block              -> recursive AnalyzeBlock
//	If                 -> any branch falls through / has return (missing
//	                      else contributes a fall-through branch)
//	For / While        -> always falls through; return inside the body
//	                      counts
//	Parallel           -> always falls through; any branch return counts
//	Attempt             -> success/failure OR of both facts
//	RequestInput        -> any branch facts (action branches + timeout)
//	anything else      -> falls through, no return
func AnalyzeStatement(statement ast.Stmt) ExitFacts {
	switch s := statement.(type) {
	case *ast.ReturnStmt:
		return ExitFacts{CanFallThrough: false, HasReturn: true}
	case *ast.Block:
		return AnalyzeBlock(s)
	case *ast.IfStmt:
		branchFacts := []ExitFacts{AnalyzeBlock(s.ThenBody())}
		for _, branch := range s.ElseIfs() {
			branchFacts = append(branchFacts, AnalyzeBlock(branch.Body()))
		}
		if s.ElseBody() == nil {
			branchFacts = append(branchFacts, ExitFacts{CanFallThrough: true, HasReturn: false})
		} else {
			branchFacts = append(branchFacts, AnalyzeBlock(s.ElseBody()))
		}
		return ExitFacts{
			CanFallThrough: anyExitFallThrough(branchFacts),
			HasReturn:      anyExitReturn(branchFacts),
		}
	case *ast.ForStmt:
		return ExitFacts{CanFallThrough: true, HasReturn: AnalyzeBlock(s.Body()).HasReturn}
	case *ast.WhileStmt:
		return ExitFacts{CanFallThrough: true, HasReturn: AnalyzeBlock(s.Body()).HasReturn}
	case *ast.ParallelStmt:
		branchFacts := make([]ExitFacts, 0, len(s.Branches()))
		for _, branch := range s.Branches() {
			branchFacts = append(branchFacts, AnalyzeBlock(branch))
		}
		return ExitFacts{CanFallThrough: true, HasReturn: anyExitReturn(branchFacts)}
	case *ast.AttemptStmt:
		success := AnalyzeBlock(s.SuccessBody())
		failure := AnalyzeBlock(s.FailureBody())
		return ExitFacts{
			CanFallThrough: success.CanFallThrough || failure.CanFallThrough,
			HasReturn:      success.HasReturn || failure.HasReturn,
		}
	case *ast.RequestInputStmt:
		branchFacts := make([]ExitFacts, 0, len(s.ActionBranches())+1)
		for _, branch := range s.ActionBranches() {
			branchFacts = append(branchFacts, AnalyzeBlock(branch.Body()))
		}
		if tb := s.TimeoutBody(); tb != nil {
			branchFacts = append(branchFacts, AnalyzeBlock(tb))
		}
		return ExitFacts{
			CanFallThrough: anyExitFallThrough(branchFacts),
			HasReturn:      anyExitReturn(branchFacts),
		}
	case *ast.SwitchStmt:
		branchFacts := make([]ExitFacts, 0, len(s.Branches()))
		for _, branch := range s.Branches() {
			branchFacts = append(branchFacts, AnalyzeBlock(branch.Body()))
		}
		if s.DefaultBody() != nil {
			branchFacts = append(branchFacts, AnalyzeBlock(s.DefaultBody()))
		}
		return ExitFacts{
			CanFallThrough: anyExitFallThrough(branchFacts),
			HasReturn:      anyExitReturn(branchFacts),
		}
	}
	return ExitFacts{CanFallThrough: true, HasReturn: false}
}

func anyExitFallThrough(facts []ExitFacts) bool {
	for _, f := range facts {
		if f.CanFallThrough {
			return true
		}
	}
	return false
}

func anyExitReturn(facts []ExitFacts) bool {
	for _, f := range facts {
		if f.HasReturn {
			return true
		}
	}
	return false
}
