package parser

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
)

type zipVisitor struct {
	onForExpr func(*ast.ForExpr)
	onForStmt func(*ast.ForStmt)
}

func (v *zipVisitor) Visit(n ast.Node) ast.Visitor {
	switch f := n.(type) {
	case *ast.ForExpr:
		if v.onForExpr != nil {
			v.onForExpr(f)
		}
	case *ast.ForStmt:
		if v.onForStmt != nil {
			v.onForStmt(f)
		}
	}
	return v
}

func (v *zipVisitor) VisitLeave(n ast.Node) {}

func TestZipForParseSmoke(t *testing.T) {
	src := `
@language nodecoda/1
@mode workflow

function main() {
    let agg = for (input, outputList in zip(v1.list, v2.outputList)) {
        yield text = input.title + outputList.name;
    };

    parallel for (a, b in zip(xs, ys), concurrency: 4, on_error: keep_null) {
        run(fn(a, b));
    }
    for (string k, int v in zip(keys, vals)) {
        run(fn2(k, v));
    }
    output(agg);
}
`
	prog, errs := ParseSource(src, "smoke.ncoda")
	if len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	gotExpr, gotStmt := 0, 0
	ast.Walk(&zipVisitor{
		onForExpr: func(f *ast.ForExpr) {
			if !f.IsZip() {
				t.Fatalf("ForExpr expected zip")
			}
			if len(f.LoopVars()) != 2 || len(f.LoopSources()) != 2 {
				t.Fatalf("zip vars/sources: %v / %v", f.LoopVars(), f.LoopSources())
			}
			gotExpr++
		},
		onForStmt: func(f *ast.ForStmt) {
			gotStmt++
			if f.IsParallel() {
				if !f.IsZip() {
					t.Fatalf("parallel for expected zip")
				}
				if f.Concurrency() != 4 || f.OnError() != "keep_null" {
					t.Fatalf("parallel config: %d %s", f.Concurrency(), f.OnError())
				}
			} else if !f.IsZip() || len(f.LoopVars()) != 2 {
				t.Fatalf("stmt for zip: %v", f.LoopVars())
			}
		},
	}, prog)
	if gotExpr != 1 || gotStmt != 2 {
		t.Fatalf("expected 1 ForExpr + 2 ForStmt, got %d/%d", gotExpr, gotStmt)
	}
}

// TestZipSingleVarCompat: 单变量时 zip(...) 仍是普通函数调用语义（向后兼容）。
func TestZipSingleVarCompat(t *testing.T) {
	prog, errs := ParseSource(`@language nodecoda/1
@mode workflow

function main() {
    for (x in zip(a, b)) { run(f(x)); }
    output("done");
}
`, "compat.ncoda")
	if len(errs) != 0 {
		t.Fatalf("single-var zip call should parse as function call: %v", errs)
	}
	ast.Walk(&zipVisitor{onForStmt: func(f *ast.ForStmt) {
		if f.IsZip() {
			t.Fatalf("single-var loop must not be zip")
		}
		call, ok := f.Iterable().(*ast.CallExpr)
		if !ok {
			t.Fatalf("expected zip(...) function call iterable, got %T", f.Iterable())
		}
		ident, ok := call.Func().(*ast.IdentExpr)
		if !ok || ident.Name() != "zip" {
			t.Fatalf("expected callee zip, got %T", call.Func())
		}
	}}, prog)
}
