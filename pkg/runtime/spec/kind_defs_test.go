package spec

// kind_defs_test.go — go/ast readers shared by the kind-coverage judge.
//
// They read the real kind.go / build.go sources instead of a hand-kept list,
// so a new Kind cannot silently escape decodeConfig coverage.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// kindDef is one `KindX = "value"` declaration from kind.go.
type kindDef struct {
	name  string
	value string
}

// loadSpecKindDefs parses kind.go with go/ast and returns its Kind* string
// constants in declaration order. This is the 23-kind registry (ES-096).
func loadSpecKindDefs(t *testing.T) []kindDef {
	t.Helper()
	path := "kind.go"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var out []kindDef
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if len(name.Name) < 4 || name.Name[:4] != "Kind" {
					continue
				}
				if i >= len(vs.Values) {
					t.Fatalf("kind.go: %s has no literal value", name.Name)
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("kind.go: %s value is not a string literal", name.Name)
				}
				val, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("kind.go: %s value %q is not a string literal: %v", name.Name, lit.Value, err)
				}
				out = append(out, kindDef{name: name.Name, value: val})
			}
		}
	}
	return out
}

// loadDecodeConfigCaseNames parses build.go and returns the identifier names
// used in decodeConfig's switch cases (string(ir.KindX) → KindX,
// KindLoopAssign → KindLoopAssign). It also fails on any case expression it
// cannot resolve, so a new spelling cannot silently escape the coverage
// assertion.
func loadDecodeConfigCaseNames(t *testing.T) map[string]bool {
	t.Helper()
	path := "build.go"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	fn := findFunc(f, "decodeConfig")
	if fn == nil {
		t.Fatalf("%s: decodeConfig not found", path)
	}
	var sw *ast.SwitchStmt
	ast.Inspect(fn, func(n ast.Node) bool {
		if s, ok := n.(*ast.SwitchStmt); ok && sw == nil {
			sw = s
			return false
		}
		return true
	})
	if sw == nil {
		t.Fatalf("%s: decodeConfig has no switch statement", path)
	}
	out := map[string]bool{}
	for _, stmt := range sw.Body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok || cc.List == nil { // nil = default
			continue
		}
		for _, expr := range cc.List {
			name, ok := caseKindName(expr)
			if !ok {
				t.Fatalf("%s: decodeConfig case %s is not string(ir.KindX) or KindX", path, exprString(expr))
			}
			out[name] = true
		}
	}
	return out
}

func findFunc(f *ast.File, name string) *ast.FuncDecl {
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

// caseKindName resolves a decodeConfig case expression to the Kind identifier.
func caseKindName(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		if len(e.Name) >= 4 && e.Name[:4] == "Kind" {
			return e.Name, true
		}
	case *ast.CallExpr:
		fun, ok := e.Fun.(*ast.Ident)
		if !ok || fun.Name != "string" || len(e.Args) != 1 {
			return "", false
		}
		sel, ok := e.Args[0].(*ast.SelectorExpr)
		if !ok {
			return "", false
		}
		if len(sel.Sel.Name) >= 4 && sel.Sel.Name[:4] == "Kind" {
			return sel.Sel.Name, true
		}
	}
	return "", false
}

func exprString(e ast.Expr) string {
	return fmt.Sprintf("%T", e)
}
