package flow_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

var legacyEmitterHelpers = map[string]bool{
	"owePort":            true,
	"drainPorts":         true,
	"recordSkippedMerge": true,
}

// TC-28 C2: the new solver files must never reference the legacy emitter
// helpers, even transitively through a method value. The check parses
// plan*.go and inspects identifiers (comments are ignored).
func TestPlanASTHasNoLegacyHelpers(t *testing.T) {
	files, err := filepath.Glob("plan*.go")
	if err != nil {
		t.Fatalf("glob plan*.go: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no plan*.go files found")
	}
	for _, path := range files {
		if hits := legacyHelperHits(t, path); len(hits) > 0 {
			t.Fatalf("%s references legacy emitter helpers %v; the solver must stay independent", path, hits)
		}
	}
}

// The negative control is a poisoned fixture that MUST be reported.
func TestPlanASTCheckCatchesNegativeControl(t *testing.T) {
	path := filepath.Join("testdata", "poison", "negative_control.go.txt")
	hits := legacyHelperHits(t, path)
	if len(hits) == 0 {
		t.Fatalf("AST check missed the negative control %s; the guard is dead", path)
	}
	found := false
	for _, h := range hits {
		if h == "owePort" {
			found = true
		}
	}
	if !found {
		t.Fatalf("negative control hits = %v, want owePort", hits)
	}
}

func legacyHelperHits(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var hits []string
	ast.Inspect(file, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		if legacyEmitterHelpers[id.Name] {
			hits = appendUniqueString(hits, id.Name)
		}
		return true
	})
	return hits
}

func appendUniqueString(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}
