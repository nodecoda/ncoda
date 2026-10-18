package passes

// Self-built unit coverage for the control_flow.py port (ExitFacts /
// AnalyzeBlock / AnalyzeStatement). Nodes are built programmatically —
// control-flow analysis is pure and span-independent.

import (
	"reflect"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
)

// cfBlock builds a Block from statements.
func cfBlock(stmts ...ast.Stmt) *ast.Block { return ast.NewBlock(stmts) }

func cfReturn() ast.Stmt { return ast.NewReturnStmt(ast.NewIntLit(1)) }

func cfVar(name string) ast.Stmt {
	return ast.NewVarDeclStmt(name, nil, ast.NewIntLit(42), true)
}

func TestAnalyzeBlockEmptyBlockFallsThrough(t *testing.T) {
	facts := AnalyzeBlock(cfBlock())
	if want := (ExitFacts{CanFallThrough: true, HasReturn: false}); facts != want {
		t.Fatalf("AnalyzeBlock(empty) = %+v, want %+v", facts, want)
	}
}

func TestAnalyzeBlockReturnStopsSequence(t *testing.T) {
	facts := AnalyzeBlock(cfBlock(cfVar("a"), cfReturn(), cfVar("b")))
	if want := (ExitFacts{CanFallThrough: false, HasReturn: true}); facts != want {
		t.Fatalf("AnalyzeBlock(return in sequence) = %+v, want %+v", facts, want)
	}
}

func TestAnalyzeStatementDefaultFallsThrough(t *testing.T) {
	facts := AnalyzeStatement(cfVar("x"))
	if want := (ExitFacts{CanFallThrough: true, HasReturn: false}); facts != want {
		t.Fatalf("AnalyzeStatement(var) = %+v, want %+v", facts, want)
	}
}

func TestAnalyzeStatementReturn(t *testing.T) {
	facts := AnalyzeStatement(cfReturn())
	if want := (ExitFacts{CanFallThrough: false, HasReturn: true}); facts != want {
		t.Fatalf("AnalyzeStatement(return) = %+v, want %+v", facts, want)
	}
}

func TestAnalyzeStatementBlock(t *testing.T) {
	facts := AnalyzeStatement(cfBlock(cfReturn()))
	if want := (ExitFacts{CanFallThrough: false, HasReturn: true}); facts != want {
		t.Fatalf("AnalyzeStatement(block) = %+v, want %+v", facts, want)
	}
}

func TestAnalyzeStatementIfBothBranchesReturnNoFallThrough(t *testing.T) {
	ifStmt := ast.NewIfStmt(ast.NewBoolLit(true),
		cfBlock(cfReturn()), nil, cfBlock(cfReturn()))
	facts := AnalyzeStatement(ifStmt)
	if want := (ExitFacts{CanFallThrough: false, HasReturn: true}); facts != want {
		t.Fatalf("AnalyzeStatement(if both return) = %+v, want %+v", facts, want)
	}
}

func TestAnalyzeStatementIfMissingElseFallsThrough(t *testing.T) {
	ifStmt := ast.NewIfStmt(ast.NewBoolLit(true), cfBlock(cfReturn()), nil, nil)
	facts := AnalyzeStatement(ifStmt)
	if !facts.CanFallThrough {
		t.Fatalf("AnalyzeStatement(if without else) must fall through: %+v", facts)
	}
	if !facts.HasReturn {
		t.Fatalf("AnalyzeStatement(if without else) must carry the branch return: %+v", facts)
	}
}

func TestAnalyzeStatementIfElseIfChain(t *testing.T) {
	elseIf := ast.NewElseIfBranch(ast.NewBoolLit(false), cfBlock(cfReturn()))
	ifStmt := ast.NewIfStmt(ast.NewBoolLit(true), cfBlock(cfVar("a")), []*ast.ElseIfBranch{elseIf}, nil)
	facts := AnalyzeStatement(ifStmt)
	if !facts.CanFallThrough {
		t.Fatalf("then falls through so the if must fall through: %+v", facts)
	}
	if !facts.HasReturn {
		t.Fatalf("else-if return must be visible: %+v", facts)
	}
}

func TestAnalyzeStatementForAndWhileAlwaysFallThrough(t *testing.T) {
	forStmt := ast.NewForStmt("item", nil, nil, cfBlock(cfReturn()))
	forFacts := AnalyzeStatement(forStmt)
	if want := (ExitFacts{CanFallThrough: true, HasReturn: true}); forFacts != want {
		t.Fatalf("AnalyzeStatement(for with return) = %+v, want %+v", forFacts, want)
	}
	whileStmt := ast.NewWhileStmt(ast.NewBoolLit(true), 1, cfBlock(cfReturn()))
	whileFacts := AnalyzeStatement(whileStmt)
	if want := (ExitFacts{CanFallThrough: true, HasReturn: true}); whileFacts != want {
		t.Fatalf("AnalyzeStatement(while with return) = %+v, want %+v", whileFacts, want)
	}
}

func TestAnalyzeStatementParallelFallsThroughWithBranchReturn(t *testing.T) {
	parallel := ast.NewParallelStmt([]*ast.Block{cfBlock(cfReturn()), cfBlock(cfVar("x"))})
	facts := AnalyzeStatement(parallel)
	if want := (ExitFacts{CanFallThrough: true, HasReturn: true}); facts != want {
		t.Fatalf("AnalyzeStatement(parallel) = %+v, want %+v", facts, want)
	}
}

func TestAnalyzeStatementAttempt(t *testing.T) {
	attempt := ast.NewAttemptStmt(nil, nil, cfBlock(cfReturn()), nil, cfBlock(cfVar("x")))
	facts := AnalyzeStatement(attempt)
	// success returns, failure falls through -> the attempt falls through
	// and carries the success return.
	if want := (ExitFacts{CanFallThrough: true, HasReturn: true}); facts != want {
		t.Fatalf("AnalyzeStatement(attempt) = %+v, want %+v", facts, want)
	}
}

func TestAnalyzeStatementRequestInput(t *testing.T) {
	branch := ast.NewHumanActionBranch(nil, cfBlock(cfReturn()))
	request := ast.NewRequestInputStmt(nil, nil, nil, nil, []*ast.HumanActionBranch{branch}, cfBlock(cfReturn()))
	facts := AnalyzeStatement(request)
	if want := (ExitFacts{CanFallThrough: false, HasReturn: true}); facts != want {
		t.Fatalf("AnalyzeStatement(request_input) = %+v, want %+v", facts, want)
	}
}

func TestAnalyzeBlockForLoopBodyReturnNotFallThroughEnd(t *testing.T) {
	// A for loop inside a sequence always falls through even when its body
	// returns; only the loop's own facts matter for the enclosing block.
	facts := AnalyzeBlock(cfBlock(
		ast.NewForStmt("item", nil, nil, cfBlock(cfReturn())),
		cfReturn(),
	))
	if want := (ExitFacts{CanFallThrough: false, HasReturn: true}); facts != want {
		t.Fatalf("AnalyzeBlock(loop then return) = %+v, want %+v", facts, want)
	}
}

func TestExitFactsValueSemantics(t *testing.T) {
	a := ExitFacts{CanFallThrough: false, HasReturn: true}
	b := ExitFacts{CanFallThrough: false, HasReturn: true}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("ExitFacts must be comparable values: %+v vs %+v", a, b)
	}
}
