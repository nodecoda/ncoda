package nodes

// exec_sem_p3_import_test.go — P3 ES-017b single-ValueBoundary gate.
//
// Both assertions parse the package AST (go/parser), not grep text: imports are
// read from the import decls and declarations are walked with go/ast.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

const p3BoundaryImport = "github.com/nodecoda/ncoda/pkg/runtime/boundary"

// ES-017b: code/http/llm/tool all consume the one boundary package.
//
// Cross-lane honesty: http/llm/tool are wired by lane A. Until that lands this
// test is expected to be red for those files; do not add imports on their
// behalf.
func TestP3ES017bFourFamiliesImportBoundary(t *testing.T) {
	fset := token.NewFileSet()
	for _, name := range []string{"code.go", "http.go", "llm.go", "tool.go"} {
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		found := false
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) == p3BoundaryImport {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s does not import %s (ES-017b: code/http/llm/tool must all consume the single ValueBoundary)", name, p3BoundaryImport)
		}
	}
}

// ES-017b: no second value-fill implementation may live in package nodes.
func TestP3ES017bNoSecondShapingDeclarations(t *testing.T) {
	banned := map[string]bool{
		"shapeValue":      true,
		"fillZero":        true,
		"zeroValue":       true,
		"normalizeInputs": true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	for _, de := range entries {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok {
				return true
			}
			if banned[fn.Name.Name] {
				pos := fset.Position(fn.Pos())
				t.Errorf("%s:%d: declares %s; value filling must live in internal/runtime/boundary (ES-017b)", name, pos.Line, fn.Name.Name)
			}
			return true
		})
	}
}
