package passes

// Ports of lang/tests/lang/unit/test_desugar.py (13 cases) plus self-built
// coverage: desugar idempotence and the desugar→bind interaction (the
// binder must enumerate the REWRITTEN tree — no ElseIfBranch nodes survive
// into the snapshot, and rebuilt nodes keep the original elif spans).

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/parser"
)

func parseSourceOrFail(t *testing.T, src string) *ast.Program {
	t.Helper()
	prog, perrs := parser.ParseSource(src, "t.dc")
	if len(perrs) > 0 {
		t.Fatalf("parse errors: %v", perrs)
	}
	return prog
}

func desugarProgram(t *testing.T, prog *ast.Program) {
	t.Helper()
	ctx := NewContext(nil)
	(&DesugarPass{}).Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("unexpected errors: %v", ctx.Diagnostics)
	}
}

// ── Rule 1: elif → nested if/else ─────────────────────────────────

func TestElifSimpleIfNoElif(t *testing.T) {
	src := `
    function main() {
        if (true) {
            var x = 1;
        }
    }
    `
	prog := parseSourceOrFail(t, src)
	desugarProgram(t, prog)

	body := prog.Entry().Body().Stmts()
	if len(body) != 1 {
		t.Fatalf("body has %d stmts, want 1", len(body))
	}
	stmt, ok := body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("stmt is %T, want *ast.IfStmt", body[0])
	}
	if len(stmt.ElseIfs()) != 0 {
		t.Fatalf("else_ifs = %d entries, want 0", len(stmt.ElseIfs()))
	}
	if stmt.ElseBody() != nil {
		t.Fatal("else_body must be nil for a bare if")
	}
}

func TestElifIfElifBecomesNested(t *testing.T) {
	src := `
    function main() {
        if (true) {
            var a = 1;
        } else if (false) {
            var b = 2;
        }
    }
    `
	prog := parseSourceOrFail(t, src)
	desugarProgram(t, prog)

	outer, ok := prog.Entry().Body().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("stmt is %T, want *ast.IfStmt", prog.Entry().Body().Stmts()[0])
	}
	if len(outer.ElseIfs()) != 0 {
		t.Fatalf("else_ifs = %d entries, want 0", len(outer.ElseIfs()))
	}
	if outer.ElseBody() == nil {
		t.Fatal("else_body must hold the nested if")
	}
	inner, ok := outer.ElseBody().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("else body stmt is %T, want *ast.IfStmt", outer.ElseBody().Stmts()[0])
	}
	if inner.Condition() == nil {
		t.Fatal("nested if lost its condition")
	}
	if inner.ElseBody() != nil {
		t.Fatal("last elif must have no else body")
	}
}

func TestElifIfElifElseBecomesNested(t *testing.T) {
	src := `
    function main() {
        if (true) {
            var a = 1;
        } else if (false) {
            var b = 2;
        } else {
            var c = 3;
        }
    }
    `
	prog := parseSourceOrFail(t, src)
	desugarProgram(t, prog)

	outer, ok := prog.Entry().Body().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("stmt is %T, want *ast.IfStmt", prog.Entry().Body().Stmts()[0])
	}
	if len(outer.ElseIfs()) != 0 {
		t.Fatalf("else_ifs = %d entries, want 0", len(outer.ElseIfs()))
	}
	if outer.ElseBody() == nil {
		t.Fatal("outer else_body is nil")
	}
	inner, ok := outer.ElseBody().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("else body stmt is %T, want *ast.IfStmt", outer.ElseBody().Stmts()[0])
	}
	if inner.ElseBody() == nil {
		t.Fatal("inner if must carry the final else body")
	}
	stmts := inner.ElseBody().Stmts()
	if len(stmts) != 1 {
		t.Fatalf("final else body has %d stmts, want 1", len(stmts))
	}
	if _, ok := stmts[0].(*ast.VarDeclStmt); !ok {
		t.Fatalf("final else stmt is %T, want *ast.VarDeclStmt", stmts[0])
	}
}

func TestElifMultipleElif(t *testing.T) {
	src := `
    function main() {
        if (true) {
            var a = 1;
        } else if (false) {
            var b = 2;
        } else if (true) {
            var c = 3;
        } else {
            var d = 4;
        }
    }
    `
	prog := parseSourceOrFail(t, src)
	desugarProgram(t, prog)

	body := prog.Entry().Body().Stmts()
	if len(body) != 1 {
		t.Fatalf("top level has %d stmts, want 1", len(body))
	}
	outer, ok := body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("stmt is %T, want *ast.IfStmt", body[0])
	}
	if len(outer.ElseIfs()) != 0 {
		t.Fatalf("else_ifs = %d entries, want 0", len(outer.ElseIfs()))
	}

	level1, ok := outer.ElseBody().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("else body stmt is %T, want *ast.IfStmt", outer.ElseBody().Stmts()[0])
	}
	if len(level1.ElseIfs()) != 0 {
		t.Fatalf("level1 else_ifs = %d entries, want 0", len(level1.ElseIfs()))
	}
	level2, ok := level1.ElseBody().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("else body stmt is %T, want *ast.IfStmt", level1.ElseBody().Stmts()[0])
	}
	if len(level2.ElseIfs()) != 0 {
		t.Fatalf("level2 else_ifs = %d entries, want 0", len(level2.ElseIfs()))
	}
	if level2.ElseBody() == nil {
		t.Fatal("level2 must carry the final else body")
	}
	if len(level2.ElseBody().Stmts()) != 1 {
		t.Fatalf("final else body has %d stmts, want 1", len(level2.ElseBody().Stmts()))
	}
}

func TestElifNoElse(t *testing.T) {
	src := `
    function main() {
        if (true) {
            var a = 1;
        } else if (false) {
            var b = 2;
        } else if (true) {
            var c = 3;
        }
    }
    `
	prog := parseSourceOrFail(t, src)
	desugarProgram(t, prog)

	outer, ok := prog.Entry().Body().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("stmt is %T, want *ast.IfStmt", prog.Entry().Body().Stmts()[0])
	}
	if len(outer.ElseIfs()) != 0 {
		t.Fatalf("else_ifs = %d entries, want 0", len(outer.ElseIfs()))
	}
	level1, ok := outer.ElseBody().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("else body stmt is %T, want *ast.IfStmt", outer.ElseBody().Stmts()[0])
	}
	if len(level1.ElseIfs()) != 0 {
		t.Fatalf("level1 else_ifs = %d entries, want 0", len(level1.ElseIfs()))
	}
	level2, ok := level1.ElseBody().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("else body stmt is %T, want *ast.IfStmt", level1.ElseBody().Stmts()[0])
	}
	if level2.ElseBody() != nil {
		t.Fatal("last elif must have no else body")
	}
}

func TestElifInFunction(t *testing.T) {
	src := `
    function classify(int x) -> string {
        if (x > 0) {
            return "positive";
        } else if (x < 0) {
            return "negative";
        } else {
            return "zero";
        }
    }
    function main() {}
    `
	prog := parseSourceOrFail(t, src)
	desugarProgram(t, prog)

	funcs := prog.Functions()
	if len(funcs) != 1 {
		t.Fatalf("functions = %d, want 1", len(funcs))
	}
	outer, ok := funcs[0].Body().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("stmt is %T, want *ast.IfStmt", funcs[0].Body().Stmts()[0])
	}
	if len(outer.ElseIfs()) != 0 {
		t.Fatalf("else_ifs = %d entries, want 0", len(outer.ElseIfs()))
	}
	if outer.ElseBody() == nil {
		t.Fatal("else_body must hold the nested if")
	}
}

func TestElifNestedInBody(t *testing.T) {
	src := `
    function main() {
        if (true) {
            if (false) {
                var a = 1;
            } else if (true) {
                var b = 2;
            }
        }
    }
    `
	prog := parseSourceOrFail(t, src)
	desugarProgram(t, prog)

	outerIf, ok := prog.Entry().Body().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("stmt is %T, want *ast.IfStmt", prog.Entry().Body().Stmts()[0])
	}
	innerIf, ok := outerIf.ThenBody().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("then body stmt is %T, want *ast.IfStmt", outerIf.ThenBody().Stmts()[0])
	}
	if len(innerIf.ElseIfs()) != 0 {
		t.Fatalf("else_ifs = %d entries, want 0", len(innerIf.ElseIfs()))
	}
	if innerIf.ElseBody() == nil {
		t.Fatal("nested if must carry the desugared elif")
	}
}

func TestElifSourceSpanPreserved(t *testing.T) {
	src := `
    function main() {
        if (true) {
            var a = 1;
        } else if (false) {
            var b = 2;
        }
    }
    `
	prog := parseSourceOrFail(t, src)
	outer0, ok := prog.Entry().Body().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("stmt is %T, want *ast.IfStmt", prog.Entry().Body().Stmts()[0])
	}
	originalElifSpan := outer0.ElseIfs()[0].Span()
	desugarProgram(t, prog)

	outer, ok := prog.Entry().Body().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("stmt is %T, want *ast.IfStmt", prog.Entry().Body().Stmts()[0])
	}
	inner, ok := outer.ElseBody().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("else body stmt is %T, want *ast.IfStmt", outer.ElseBody().Stmts()[0])
	}
	if inner.Span() != originalElifSpan {
		t.Fatalf("inner if span = %+v, want the elif branch span %+v", inner.Span(), originalElifSpan)
	}
}

// ── Rule 2: for normalization ─────────────────────────────────────

func TestForBodyDesugared(t *testing.T) {
	src := `
    function main() {
        for (x in items) {
            if (true) {
                var a = 1;
            } else if (false) {
                var b = 2;
            }
        }
    }
    `
	prog := parseSourceOrFail(t, src)
	desugarProgram(t, prog)

	forStmt, ok := prog.Entry().Body().Stmts()[0].(*ast.ForStmt)
	if !ok {
		t.Fatalf("stmt is %T, want *ast.ForStmt", prog.Entry().Body().Stmts()[0])
	}
	innerIf, ok := forStmt.Body().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("for body stmt is %T, want *ast.IfStmt", forStmt.Body().Stmts()[0])
	}
	if len(innerIf.ElseIfs()) != 0 {
		t.Fatalf("else_ifs = %d entries, want 0", len(innerIf.ElseIfs()))
	}
}

func TestForPreservedAsFor(t *testing.T) {
	src := `
    function main() {
        for (x in items) {
            var a = 1;
        }
    }
    `
	prog := parseSourceOrFail(t, src)
	desugarProgram(t, prog)

	if _, ok := prog.Entry().Body().Stmts()[0].(*ast.ForStmt); !ok {
		t.Fatalf("for must stay a *ast.ForStmt, got %T", prog.Entry().Body().Stmts()[0])
	}
}

// ── integration: pipeline + bind ──────────────────────────────────

func TestElifFullPipelineWithElif(t *testing.T) {
	src := `
    function main(string input_text) {
        var x = 1;
        if (x > 0) {
            var a = 1;
        } else if (x < 0) {
            var b = 2;
        } else {
            var c = 3;
        }
    }
    `
	p := New()
	if err := p.Register(StageDesugar, &DesugarPass{}); err != nil {
		t.Fatalf("Register(StageDesugar): %v", err)
	}
	if err := p.Register(StageNormalize, &NormalizePass{}); err != nil {
		t.Fatalf("Register(StageNormalize): %v", err)
	}
	res := p.Run(src, "t.dc", nil)
	if res.HasErrors() {
		t.Fatalf("pipeline errors: %v", res.Diagnostics)
	}
	for _, d := range res.Diagnostics {
		if strings.Contains(strings.ToLower(d.Message), "elif") || strings.Contains(strings.ToLower(d.Message), "else if") {
			t.Fatalf("unexpected desugar-related diagnostic: %v", d)
		}
	}
	// Locate the if chain by node type rather than a fixed index: earlier
	// statements (e.g. `var x = 1;`) may precede it, and that is not part
	// of the desugar contract under test.
	var outer *ast.IfStmt
	for _, s := range res.Program.Program.Entry().Body().Stmts() {
		if cand, ok := s.(*ast.IfStmt); ok {
			outer = cand
			break
		}
	}
	if outer == nil {
		t.Fatal("no if statement found in the desugared entry body")
	}
	if len(outer.ElseIfs()) != 0 {
		t.Fatalf("else_ifs = %d entries after pipeline, want 0", len(outer.ElseIfs()))
	}
}

func TestDesugarEmptyProgram(t *testing.T) {
	prog := ast.NewProgram()
	desugarProgram(t, prog)
}

func TestDesugarPreservesEntryParams(t *testing.T) {
	src := `
    function main(string x, int y) {
        if (true) {
            var a = 1;
        } else if (false) {
            var b = 2;
        }
    }
    `
	prog := parseSourceOrFail(t, src)
	desugarProgram(t, prog)

	params := prog.Entry().Params()
	if len(params) != 2 {
		t.Fatalf("params = %d, want 2", len(params))
	}
	if params[0].Name() != "x" || params[1].Name() != "y" {
		t.Fatalf("params = [%s %s], want [x y]", params[0].Name(), params[1].Name())
	}
}

// ── self-built: idempotence + bind interaction ────────────────────

// TestDesugarIsIdempotent runs desugar twice; the flattened structure must
// be stable (Python reference is structurally idempotent — verified against
// desugar.py on the same corpus).
func TestDesugarIsIdempotent(t *testing.T) {
	src := `
    function main() {
        if (true) {
            var a = 1;
        } else if (false) {
            var b = 2;
        } else if (true) {
            var c = 3;
        } else {
            var d = 4;
        }
    }
    `
	prog := parseSourceOrFail(t, src)
	desugarProgram(t, prog)
	desugarProgram(t, prog)

	outer, ok := prog.Entry().Body().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("stmt is %T, want *ast.IfStmt", prog.Entry().Body().Stmts()[0])
	}
	if len(outer.ElseIfs()) != 0 {
		t.Fatalf("else_ifs = %d entries after second desugar, want 0", len(outer.ElseIfs()))
	}
	level1, ok := outer.ElseBody().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("else body stmt is %T, want *ast.IfStmt", outer.ElseBody().Stmts()[0])
	}
	if len(level1.ElseIfs()) != 0 {
		t.Fatalf("level1 else_ifs = %d entries after second desugar, want 0", len(level1.ElseIfs()))
	}
	level2, ok := level1.ElseBody().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("else body stmt is %T, want *ast.IfStmt", level1.ElseBody().Stmts()[0])
	}
	if level2.ElseBody() == nil {
		t.Fatal("level2 else body must survive the second desugar")
	}
}

// TestPipelineBindSeesDesugaredTree locks the critical correctness
// property: binding runs AFTER desugar/normalize at the StageScope
// position, so the binder must enumerate the REWRITTEN tree. The snapshot
// must contain no ElseIfBranch nodes, and the rebuilt nested-if nodes must
// carry the original elif spans (diagnostic fidelity).
func TestPipelineBindSeesDesugaredTree(t *testing.T) {
	src := `
    function main() {
        if (true) {
            var a = 1;
        } else if (false) {
            var b = 2;
        } else {
            var c = 3;
        }
    }
    `
	outer0, ok := parseSourceOrFail(t, src).Entry().Body().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("stmt is %T, want *ast.IfStmt", parseSourceOrFail(t, src).Entry().Body().Stmts()[0])
	}
	originalElifSpan := outer0.ElseIfs()[0].Span()

	p := New()
	if err := p.Register(StageDesugar, &DesugarPass{}); err != nil {
		t.Fatalf("Register(StageDesugar): %v", err)
	}
	if err := p.Register(StageNormalize, &NormalizePass{}); err != nil {
		t.Fatalf("Register(StageNormalize): %v", err)
	}
	res := p.Run(src, "t.dc", nil)
	if res.HasErrors() {
		t.Fatalf("pipeline errors: %v", res.Diagnostics)
	}

	// No ElseIfBranch may survive into the binder enumeration.
	count := 0
	res.Program.Snapshot.AstNodes().Range(func(id binding.AstNodeID, info binding.NodeInfo) bool {
		count++
		if info.Kind == "ElseIfBranch" {
			t.Errorf("ElseIfBranch node %v survived into the binder snapshot", id)
		}
		return true
	})
	if count == 0 {
		t.Fatal("snapshot is empty; binder did not enumerate the program")
	}

	// The rebuilt nested if resolves in the snapshot with the elif span.
	outer, ok := res.Program.Program.Entry().Body().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("stmt is %T, want *ast.IfStmt", res.Program.Program.Entry().Body().Stmts()[0])
	}
	inner, ok := outer.ElseBody().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("else body stmt is %T, want *ast.IfStmt", outer.ElseBody().Stmts()[0])
	}
	id, found := res.Program.Snapshot.NodeIDOf(inner)
	if !found {
		t.Fatal("nested if was not enumerated by the binder")
	}
	info, found := res.Program.Snapshot.AstNodes().Get(id)
	if !found {
		t.Fatalf("AstNodes has no entry for nested if id %v", id)
	}
	if info.Span != originalElifSpan {
		t.Fatalf("nested if NodeInfo span = %+v, want original elif span %+v", info.Span, originalElifSpan)
	}
}

// ── Rule 3: zip multi-variable for preserved ───────────────────────────
// Regression: desugarFor previously rebuilt every for through the
// single-var accessors (VarName/Iterable), silently dropping the second
// variable/source of a zip loop (for (a, b in zip(x, y))). The binder then
// saw a body referencing an undeclared 'b' → UNDEFINED_VAR (blind-test
// 7535404609906491419_X38_V19_shudan_video).
func TestZipForPreservedAfterDesugar(t *testing.T) {
	prog := parseSourceOrFail(t, `
function main() {
    let xs = [1, 2, 3];
    let ys = ["a", "b", "c"];
    for (a, b in zip(xs, ys)) {
        let c = a + 1;
        let d = b + "x";
    }
    return "ok";
}
`)
	desugarProgram(t, prog)
	forStmt, ok := prog.Entry().Body().Stmts()[2].(*ast.ForStmt)
	if !ok {
		t.Fatalf("stmt is %T, want *ast.ForStmt", prog.Entry().Body().Stmts()[2])
	}
	if !forStmt.IsZip() {
		t.Fatalf("zip loop lost its zip form after desugar")
	}
	if got := forStmt.LoopVars(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("zip vars = %v, want [a b]", got)
	}
	if got := forStmt.LoopSources(); len(got) != 2 {
		t.Fatalf("zip sources = %d, want 2", len(got))
	}
}

func TestZipParallelForPreservedAfterDesugar(t *testing.T) {
	prog := parseSourceOrFail(t, `
function main() {
    let xs = [1, 2, 3];
    let ys = ["a", "b", "c"];
    parallel for (a, b in zip(xs, ys), concurrency: 2, on_error: keep_null) {
        let c = a + 1;
        let d = b + "x";
    }
    return "ok";
}
`)
	desugarProgram(t, prog)
	forStmt, ok := prog.Entry().Body().Stmts()[2].(*ast.ForStmt)
	if !ok {
		t.Fatalf("stmt is %T, want *ast.ForStmt", prog.Entry().Body().Stmts()[2])
	}
	if !forStmt.IsZip() || !forStmt.IsParallel() {
		t.Fatalf("parallel zip loop lost its form after desugar: zip=%v parallel=%v", forStmt.IsZip(), forStmt.IsParallel())
	}
	if got := forStmt.LoopVars(); len(got) != 2 || got[1] != "b" {
		t.Fatalf("parallel zip vars = %v, want [a b]", got)
	}
}
