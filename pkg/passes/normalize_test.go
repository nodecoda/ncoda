package passes

// Ports of lang/tests/lang/unit/test_normalize.py (4 cases) plus a
// self-built idempotence check. Block flattening only inlines BARE Block
// statements; if/for/while bodies keep their Block (scope significance).

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
)

func normalizeProgram(t *testing.T, prog *ast.Program) {
	t.Helper()
	ctx := NewContext(nil)
	(&NormalizePass{}).Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("unexpected errors: %v", ctx.Diagnostics)
	}
}

func TestNormalizeBareBlockInlined(t *testing.T) {
	inner := ast.NewBlock([]ast.Stmt{
		ast.NewVarDeclStmt("a", nil, nil, false),
		ast.NewVarDeclStmt("b", nil, nil, false),
	})
	outer := ast.NewBlock([]ast.Stmt{inner, ast.NewVarDeclStmt("c", nil, nil, false)})
	prog := ast.NewProgram(ast.WithEntry(ast.NewFunctionDef("main", nil, nil, outer)))

	normalizeProgram(t, prog)

	stmts := prog.Entry().Body().Stmts()
	if len(stmts) != 3 {
		t.Fatalf("after flatten, body has %d stmts, want 3", len(stmts))
	}
	for _, s := range stmts {
		if _, ok := s.(*ast.VarDeclStmt); !ok {
			t.Fatalf("stmt is %T, want *ast.VarDeclStmt (bare block must be inlined)", s)
		}
	}
}

func TestNormalizeIfBodyNotFlattened(t *testing.T) {
	src := `
    function main() {
        if (true) {
            var a = 1;
        }
    }
    `
	prog := parseSourceOrFail(t, src)
	normalizeProgram(t, prog)

	ifStmt, ok := prog.Entry().Body().Stmts()[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("stmt is %T, want *ast.IfStmt", prog.Entry().Body().Stmts()[0])
	}
	if ifStmt.ThenBody() == nil {
		t.Fatal("then_body must stay a *ast.Block (scope-significant bodies are not flattened)")
	}
}

func TestNormalizeEmptyProgram(t *testing.T) {
	normalizeProgram(t, ast.NewProgram())
}

func TestNormalizeDesugarThenNormalize(t *testing.T) {
	src := `
    function main(string x) {
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
	ctx := NewContext(nil)
	(&DesugarPass{}).Run(prog, ctx)
	(&NormalizePass{}).Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("unexpected errors: %v", ctx.Diagnostics)
	}

	body := prog.Entry().Body().Stmts()
	if len(body) != 1 {
		t.Fatalf("after desugar+normalize, body has %d stmts, want 1", len(body))
	}
	outer, ok := body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("stmt is %T, want *ast.IfStmt", body[0])
	}
	if len(outer.ElseIfs()) != 0 {
		t.Fatalf("else_ifs = %d entries, want 0", len(outer.ElseIfs()))
	}
}

// TestNormalizeIsIdempotent: a second normalize run must not change the
// flattened structure (Python normalize.py is structurally idempotent).
func TestNormalizeIsIdempotent(t *testing.T) {
	inner := ast.NewBlock([]ast.Stmt{
		ast.NewVarDeclStmt("a", nil, nil, false),
		ast.NewBlock([]ast.Stmt{ast.NewVarDeclStmt("b", nil, nil, false)}),
	})
	prog := ast.NewProgram(ast.WithEntry(ast.NewFunctionDef("main", nil, nil,
		ast.NewBlock([]ast.Stmt{inner}))))

	normalizeProgram(t, prog)
	normalizeProgram(t, prog)

	stmts := prog.Entry().Body().Stmts()
	if len(stmts) != 2 {
		t.Fatalf("after double normalize, body has %d stmts, want 2", len(stmts))
	}
	if _, ok := stmts[0].(*ast.VarDeclStmt); !ok {
		t.Fatalf("stmts[0] is %T, want *ast.VarDeclStmt", stmts[0])
	}
	if _, ok := stmts[1].(*ast.VarDeclStmt); !ok {
		t.Fatalf("stmts[1] is %T, want *ast.VarDeclStmt", stmts[1])
	}
}
