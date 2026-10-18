package parser

// P3.5 import declaration syntax: import "provider"; at top level.

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
)

func TestParseImportDeclarations(t *testing.T) {
	source := `@language nodecoda/1
@mode workflow
import "tts";
import "feiyin_digital_human";
function main() { return "ok"; }
`
	prog, errs := ParseSource(source, "import_test.ncoda")
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	imports := prog.Imports()
	if len(imports) != 2 {
		t.Fatalf("imports = %d, want 2", len(imports))
	}
	if imports[0].Provider() != "tts" || imports[1].Provider() != "feiyin_digital_human" {
		t.Fatalf("providers = %q, %q", imports[0].Provider(), imports[1].Provider())
	}
}

func TestParseImportDuplicateRejected(t *testing.T) {
	source := `@language nodecoda/1
import "tts";
import "tts";
function main() { return "ok"; }
`
	_, errs := ParseSource(source, "dup.ncoda")
	if len(errs) == 0 {
		t.Fatal("expected duplicate import error")
	}
	if !strings.Contains(errs[0].Message, `Duplicate import "tts"`) {
		t.Fatalf("error = %q, want duplicate import", errs[0].Message)
	}
}

func TestParseImportEmptyRejected(t *testing.T) {
	source := `@language nodecoda/1
import "";
function main() { return "ok"; }
`
	_, errs := ParseSource(source, "empty.ncoda")
	if len(errs) == 0 {
		t.Fatal("expected empty import error")
	}
}

func TestParseImportNonStringRejected(t *testing.T) {
	source := `@language nodecoda/1
import tts;
function main() { return "ok"; }
`
	_, errs := ParseSource(source, "ident.ncoda")
	if len(errs) == 0 {
		t.Fatal("expected non-string import error")
	}
	if !strings.Contains(errs[0].Message, "Expected provider string after 'import'") {
		t.Fatalf("error = %q, want provider string message", errs[0].Message)
	}
}

// ImportDecl must be visitor-safe and program-root reachable.
func TestImportDeclVisitorReachesImports(t *testing.T) {
	source := `@language nodecoda/1
import "tts";
function main() { return "ok"; }
`
	prog, errs := ParseSource(source, "visitor.ncoda")
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	seen := 0
	ast.Walk(&astVisitor{fn: func(n ast.Node) {
		if _, ok := n.(*ast.ImportDecl); ok {
			seen++
		}
	}}, prog)
	if seen != 1 {
		t.Fatalf("ImportDecl visited %d times, want 1", seen)
	}
}

type astVisitor struct{ fn func(ast.Node) }

func (v *astVisitor) Visit(n ast.Node) ast.Visitor {
	v.fn(n)
	return v
}
