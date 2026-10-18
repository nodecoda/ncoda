// Task 6: grammar conformance + example smoke. Ports the core cases of the
// Python gate suites (tests/lang/unit/test_grammar_conformance.py and the
// syntax-level assertions of tests/lang/gate/test_grammar_differential.py)
// onto the Go pipeline entry point ParseSource, and smokes the five
// canonical example programs.
//
// Grouping follows the reference: one Go test per production rule group,
// with test_<production>_valid_<variant> / reject_<reason> semantics.
package parser

import (
	"os"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
)

// conformOK lexes+parses source through ParseSource, failing the test on any
// error and returning the program (Python conformance helper `parse`).
func conformOK(t *testing.T, src string) *ast.Program {
	t.Helper()
	prog, errs := ParseSource(src, "<conformance>")
	if len(errs) != 0 {
		t.Fatalf("ParseSource(%q): unexpected errors: %v", src, errs)
	}
	if prog == nil {
		t.Fatalf("ParseSource(%q): nil program", src)
	}
	return prog
}

// conformErr lexes+parses source through ParseSource, expecting >= min
// errors and an unusable (nil) program (Python `parse_expect_errors`).
func conformErr(t *testing.T, src string, min int) {
	t.Helper()
	prog, errs := ParseSource(src, "<conformance>")
	if len(errs) < min {
		t.Fatalf("ParseSource(%q): expected >= %d errors, got %d: %v", src, min, len(errs), errs)
	}
	if prog != nil {
		t.Errorf("ParseSource(%q): returned a program despite %d errors", src, len(errs))
	}
}

// firstStmt returns stmts[0] of the entry body, failing if absent.
func firstStmt(t *testing.T, src string) ast.Stmt {
	t.Helper()
	stmts := entryBody(t, src)
	if len(stmts) == 0 {
		t.Fatalf("parse %q: empty entry body", src)
	}
	return stmts[0]
}

func initOfFirstStmt(t *testing.T, src string) ast.Expr {
	t.Helper()
	vd, ok := firstStmt(t, src).(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("stmts[0] = %T, want *ast.VarDeclStmt", firstStmt(t, src))
	}
	if vd.Init() == nil {
		t.Fatal("VarDeclStmt has no initializer")
	}
	return vd.Init()
}

// ── ParseSource wiring (lexer→parser, mirroring pipeline.py compile_dc_result) ──

func TestParseSourceValidSource(t *testing.T) {
	prog, errs := ParseSource("function main() { return 42; }", "test.ncoda")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if prog == nil || prog.Entry() == nil || prog.Entry().Name() != "main" {
		t.Fatalf("prog = %v, want entry function main", prog)
	}
}

func TestParseSourceEmptyInput(t *testing.T) {
	// Empty input -> empty Program, zero errors (Python ProgramNode()).
	prog, errs := ParseSource("", "<empty>")
	if len(errs) != 0 {
		t.Fatalf("expected no errors for empty input, got %v", errs)
	}
	if prog == nil {
		t.Fatal("ParseSource(\"\") returned nil program")
	}
	if prog.Mode() != "workflow" || prog.Entry() != nil {
		t.Errorf("empty program: Mode()=%q Entry()=%v, want workflow/nil", prog.Mode(), prog.Entry())
	}
}

func TestParseSourceLexerErrorsShortCircuit(t *testing.T) {
	// pipeline.py: lexer errors short-circuit before the parser runs; the
	// program is unusable. Sources: illegal char, stray &, unterminated string.
	for _, src := range []string{"&", "|", "@", `"unterminated`, `function main() { var x = "hi; }`} {
		t.Run(src, func(t *testing.T) {
			prog, errs := ParseSource(src, "<lexerr>")
			if len(errs) == 0 {
				t.Fatal("expected lexer errors, got none")
			}
			if prog != nil {
				t.Errorf("prog = %v, want nil (lexer failed)", prog)
			}
		})
	}
}

func TestParseSourceParserErrors(t *testing.T) {
	// parser.py: parse errors also leave the program unusable.
	for _, src := range []string{
		"function main( { }", // missing param name
		"function foo {}",    // missing parens
		"const X 1; function main() {}",
		"@mode bogus\nfunction main() {}",
		"function main() { if true { } }",
	} {
		t.Run(src, func(t *testing.T) {
			prog, errs := ParseSource(src, "<parseerr>")
			if len(errs) == 0 {
				t.Fatal("expected parse errors, got none")
			}
			if prog != nil {
				t.Errorf("prog = %v, want nil (parser failed)", prog)
			}
		})
	}
}

// ── program / headers (test_language_declaration_is_optional_and_must_be_first) ──

func TestGrammarProgramHeaders(t *testing.T) {
	t.Run("identity-optional", func(t *testing.T) {
		if got := conformOK(t, "function main() {}").LanguageIdentity(); got != "" {
			t.Errorf("LanguageIdentity() = %q, want empty", got)
		}
	})
	t.Run("identity-first", func(t *testing.T) {
		prog := conformOK(t, "@language nodecoda/1\nfunction main() {}")
		if got := prog.LanguageIdentity(); got != "nodecoda/1" {
			t.Errorf("LanguageIdentity() = %q, want nodecoda/1", got)
		}
	})
	t.Run("mode-workflow", func(t *testing.T) {
		if got := conformOK(t, "@mode workflow\nfunction main() {}").Mode(); got != "workflow" {
			t.Errorf("Mode() = %q, want workflow", got)
		}
	})
	t.Run("mode-advanced-chat", func(t *testing.T) {
		if got := conformOK(t, "@mode advanced-chat\nfunction main() {}").Mode(); got != "advanced-chat" {
			t.Errorf("Mode() = %q, want advanced-chat", got)
		}
	})
	t.Run("reject-language-after-mode", func(t *testing.T) {
		// @language must precede @mode (Python: "Expected declaration, got @language").
		conformErr(t, "@mode workflow\n@language nodecoda/1\nfunction main() {}", 1)
	})
	t.Run("reject-unknown-mode", func(t *testing.T) {
		_, errs := ParseSource("@mode foobar\nfunction main() {}", "<conformance>")
		if len(errs) == 0 || !strings.Contains(errs[0].Message, "Unknown mode: foobar") {
			t.Fatalf("errs = %v, want Unknown mode message", errs)
		}
	})
}

// ── @conversation (TestConversationDecl) ──

func TestGrammarConversationDecl(t *testing.T) {
	t.Run("valid-typed-literal-default", func(t *testing.T) {
		prog := conformOK(t, `@mode advanced-chat
@conversation string status = "new";
function main() {}`)
		convs := prog.ConversationDecls()
		if len(convs) != 1 {
			t.Fatalf("len(ConversationDecls()) = %d, want 1", len(convs))
		}
		if convs[0].Name() != "status" {
			t.Errorf("Name() = %q, want status", convs[0].Name())
		}
		if convs[0].Default() == nil {
			t.Error("Default() = nil, want StringLit")
		}
	})
	t.Run("valid-omitted-default", func(t *testing.T) {
		prog := conformOK(t, "@mode advanced-chat\n@conversation string status;\nfunction main() {}")
		if got := prog.ConversationDecls()[0].Default(); got != nil {
			t.Errorf("Default() = %v, want nil", got)
		}
	})
	t.Run("reject-missing-semicolon", func(t *testing.T) {
		conformErr(t, "@mode advanced-chat\n@conversation string status = \"new\"\nfunction main() {}", 1)
	})
}

// ── const (TestConstDecl) ──

func TestGrammarConstDecl(t *testing.T) {
	t.Run("valid-literal", func(t *testing.T) {
		prog := conformOK(t, `const STATUS = "new"; function main() {}`)
		decls := prog.ConstDecls()
		if len(decls) != 1 || decls[0].Name() != "STATUS" {
			t.Fatalf("ConstDecls() = %v, want [STATUS]", decls)
		}
	})
	t.Run("reject-missing-assignment", func(t *testing.T) {
		conformErr(t, `const STATUS "new"; function main() {}`, 1)
	})
}

// ── enum (TestEnumDecl) ──

func TestGrammarEnumDecl(t *testing.T) {
	valid := []string{
		"enum TravelMode { air, rail }",
		"enum TravelMode { air, rail };",
		"enum TravelMode { air, rail, }",
		"enum TravelMode { air, rail, };",
	}
	for _, src := range valid {
		t.Run("valid-"+src, func(t *testing.T) {
			prog := conformOK(t, src)
			enums := prog.EnumDecls()
			if len(enums) != 1 || enums[0].Name() != "TravelMode" {
				t.Fatalf("EnumDecls() = %v, want [TravelMode]", enums)
			}
			members := enums[0].Members()
			if len(members) != 2 || members[0].Name() != "air" || members[1].Name() != "rail" {
				t.Errorf("Members() = %v, want [air rail]", members)
			}
		})
	}
	rejects := []string{
		"enum Empty {}",
		"enum TravelMode { air,, rail }",
		"enum TravelMode { air, rail",
		`enum TravelMode { air = "plane" }`,
	}
	for _, src := range rejects {
		t.Run("reject-"+src, func(t *testing.T) {
			conformErr(t, src, 1)
		})
	}
}

// ── type (TestTypeDecl) ──

func TestGrammarTypeDecl(t *testing.T) {
	t.Run("valid-simple", func(t *testing.T) {
		prog := conformOK(t, "type Foo = { string name; int age; }")
		types := prog.TypeDecls()
		if len(types) != 1 || len(types[0].Fields()) != 2 {
			t.Fatalf("TypeDecls() = %v, want 1 decl with 2 fields", types)
		}
	})
	t.Run("reject-missing-brace", func(t *testing.T) {
		conformErr(t, "type Foo string name;", 1)
	})
	t.Run("reject-missing-field-semicolon", func(t *testing.T) {
		conformErr(t, "type Foo { string name }", 1)
	})
}

// ── function (TestFunctionDef) ──

func TestGrammarFunctionDef(t *testing.T) {
	t.Run("valid-no-return", func(t *testing.T) {
		prog := conformOK(t, "function foo() {}")
		fns := prog.Functions()
		if len(fns) != 1 || fns[0].ReturnType() != nil {
			t.Fatalf("Functions() = %v, want foo with nil return type", fns)
		}
	})
	t.Run("valid-with-return", func(t *testing.T) {
		prog := conformOK(t, `function foo() -> string { return "x"; }`)
		if prog.Functions()[0].ReturnType() == nil {
			t.Error("ReturnType() = nil, want string")
		}
	})
	t.Run("valid-with-params", func(t *testing.T) {
		prog := conformOK(t, "function foo(string a, int b) {}")
		if len(prog.Functions()[0].Params()) != 2 {
			t.Errorf("len(Params()) = %d, want 2", len(prog.Functions()[0].Params()))
		}
	})
	t.Run("reject-missing-parens", func(t *testing.T) {
		conformErr(t, "function foo {}", 1)
	})
}

// ── entry (TestEntryPoint) ──

func TestGrammarEntryPoint(t *testing.T) {
	t.Run("valid-entry", func(t *testing.T) {
		prog := conformOK(t, "function main(string q) {}")
		if prog.Entry() == nil || prog.Entry().Name() != "main" {
			t.Fatalf("Entry() = %v, want function main", prog.Entry())
		}
	})
	t.Run("reject-multiple-entries", func(t *testing.T) {
		conformErr(t, "function main() {} function main() {}", 1)
	})
	t.Run("reject-stray-entry-keyword", func(t *testing.T) {
		// Port of the Python suite's `entry a() {} entry b() {}`: `entry` is
		// not a valid top-level declaration keyword in the hand-written parser.
		conformErr(t, "entry a() {} entry b() {}", 1)
	})
}

// ── type_ref (TestTypeRef) ──

func TestGrammarTypeRef(t *testing.T) {
	for _, prim := range []string{"string", "int", "float", "bool", "file", "void", "any"} {
		t.Run("valid-primitive-"+prim, func(t *testing.T) {
			prog := conformOK(t, "function main("+prim+" x) {}")
			ref := prog.Entry().Params()[0].TypeRef()
			if ref == nil {
				t.Fatal("TypeRef() = nil")
			}
		})
	}
	t.Run("valid-array-suffix", func(t *testing.T) {
		prog := conformOK(t, "function main(string[] items) {}")
		if _, ok := prog.Entry().Params()[0].TypeRef().(*ast.ArrayTypeRef); !ok {
			t.Errorf("TypeRef() = %T, want *ast.ArrayTypeRef", prog.Entry().Params()[0].TypeRef())
		}
	})
	t.Run("valid-map-generic", func(t *testing.T) {
		prog := conformOK(t, "function main(map<string, int> counts) {}")
		if _, ok := prog.Entry().Params()[0].TypeRef().(*ast.MapTypeRef); !ok {
			t.Errorf("TypeRef() = %T, want *ast.MapTypeRef", prog.Entry().Params()[0].TypeRef())
		}
	})
	t.Run("valid-array-generic", func(t *testing.T) {
		prog := conformOK(t, "function main(array<string> items) {}")
		if _, ok := prog.Entry().Params()[0].TypeRef().(*ast.ArrayTypeRef); !ok {
			t.Errorf("TypeRef() = %T, want *ast.ArrayTypeRef", prog.Entry().Params()[0].TypeRef())
		}
	})
	t.Run("valid-named-type", func(t *testing.T) {
		prog := conformOK(t, "function f(Foo x) {}")
		ref, ok := prog.Functions()[0].Params()[0].TypeRef().(*ast.NamedTypeRef)
		if !ok {
			t.Fatalf("TypeRef() = %T, want *ast.NamedTypeRef", prog.Functions()[0].Params()[0].TypeRef())
		}
		if got := ref.Name(); got != "Foo" {
			t.Errorf("TypeRef().Name() = %q, want Foo", got)
		}
	})
	t.Run("reject-stray-identifier", func(t *testing.T) {
		conformErr(t, "input ;", 1)
	})
	t.Run("reject-map-nonstring-key", func(t *testing.T) {
		// Port of test_map_type_requires_string_key: locks decl.go's
		// "map keys must be string" constraint.
		conformErr(t, "function main(map<int, string> v) {}", 1)
	})
}

// ── file constraints (test_file_constraints_are_part_of_the_type_ref) ──

func TestGrammarFileTypeConstraints(t *testing.T) {
	// Locks decl.go's file-constraint parsing: file<types; extensions; upload_methods>.
	prog := conformOK(t, "function main(file<document; .pdf; local_file> doc) { return doc; }")
	ref, ok := prog.Entry().Params()[0].TypeRef().(*ast.FileTypeRef)
	if !ok {
		t.Fatalf("TypeRef() = %T, want *ast.FileTypeRef", prog.Entry().Params()[0].TypeRef())
	}
	c := ref.Constraints()
	if len(c.Types) != 1 || c.Types[0] != "document" {
		t.Errorf("Constraints().Types = %v, want [document]", c.Types)
	}
	if len(c.Extensions) != 1 || c.Extensions[0] != ".pdf" {
		t.Errorf("Constraints().Extensions = %v, want [.pdf]", c.Extensions)
	}
	if len(c.UploadMethods) != 1 || c.UploadMethods[0] != "local_file" {
		t.Errorf("Constraints().UploadMethods = %v, want [local_file]", c.UploadMethods)
	}
}

// ── typed_var_decl (TestTypedVarDecl) ──

func TestGrammarTypedVarDecl(t *testing.T) {
	t.Run("valid-uninitialized", func(t *testing.T) {
		vd, ok := firstStmt(t, "function main(string q) { string x; }").(*ast.VarDeclStmt)
		if !ok {
			t.Fatal("stmts[0] not *ast.VarDeclStmt")
		}
		if vd.Init() != nil {
			t.Error("Init() = non-nil, want nil")
		}
	})
	t.Run("valid-initialized", func(t *testing.T) {
		vd, ok := firstStmt(t, `function main(string q) { string x = "hi"; }`).(*ast.VarDeclStmt)
		if !ok || vd.Init() == nil {
			t.Fatalf("stmts[0] = %T (Init %v), want initialized VarDeclStmt", firstStmt(t, `function main(string q) { string x = "hi"; }`), vd.Init())
		}
	})
	t.Run("reject-missing-name", func(t *testing.T) {
		conformErr(t, "function main(string q) { string = 1; }", 1)
	})
}

// ── var_decl (TestVarDecl) ──

func TestGrammarVarDecl(t *testing.T) {
	t.Run("valid-var-mutable", func(t *testing.T) {
		vd := firstStmt(t, `function main(string q) { var x = "hi"; }`).(*ast.VarDeclStmt)
		if !vd.IsMutable() {
			t.Error("IsMutable() = false, want true")
		}
	})
	t.Run("valid-let-immutable", func(t *testing.T) {
		vd := firstStmt(t, `function main(string q) { let x = "hi"; }`).(*ast.VarDeclStmt)
		if vd.IsMutable() {
			t.Error("IsMutable() = true, want false")
		}
	})
	t.Run("valid-no-init", func(t *testing.T) {
		vd := firstStmt(t, "function main(string q) { var x; }").(*ast.VarDeclStmt)
		if vd.Init() != nil {
			t.Error("Init() = non-nil, want nil")
		}
	})
}

// ── output_stmt (TestOutputStmt) ──

func TestGrammarOutputStmt(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		if _, ok := firstStmt(t, `function main(string q) { output(q); }`).(*ast.OutputStmt); !ok {
			t.Fatal("stmts[0] not *ast.OutputStmt")
		}
	})
	t.Run("valid-literal", func(t *testing.T) {
		if _, ok := firstStmt(t, `function main(string q) { output("progress"); }`).(*ast.OutputStmt); !ok {
			t.Fatal("stmts[0] not *ast.OutputStmt")
		}
	})
	t.Run("reject-two-args", func(t *testing.T) {
		conformErr(t, `function main(string q) { output("key", q); }`, 1)
	})
	t.Run("reject-missing-semicolon", func(t *testing.T) {
		conformErr(t, `function main(string q) { output(q) }`, 1)
	})
}

// ── request_input (TestRequestInputStmt) ──

func TestGrammarRequestInputStmt(t *testing.T) {
	t.Run("valid-actions-then-timeout", func(t *testing.T) {
		stmts := entryBody(t, `function main() {
			request_input<Form, Action>({timeout: {value: 1}}) as result {
				action Action.submit { return result; }
				timeout { return; }
			}
		}`)
		ri, ok := stmts[0].(*ast.RequestInputStmt)
		if !ok {
			t.Fatalf("stmts[0] = %T, want *ast.RequestInputStmt", stmts[0])
		}
		if len(ri.ActionBranches()) != 1 {
			t.Errorf("len(ActionBranches()) = %d, want 1", len(ri.ActionBranches()))
		}
		if ri.TimeoutBody() == nil {
			t.Error("TimeoutBody() = nil, want block")
		}
	})
	t.Run("reject-timeout-before-actions", func(t *testing.T) {
		conformErr(t, `function main() {
			request_input<Form, Action>({}) as result {
				timeout {} action Action.submit {}
			}
		}`, 1)
	})
}

// ── if (TestIfStmt) ──

func TestGrammarIfStmt(t *testing.T) {
	t.Run("valid-if-only", func(t *testing.T) {
		if _, ok := firstStmt(t, "function main(string q) { if (true) {} }").(*ast.IfStmt); !ok {
			t.Fatal("stmts[0] not *ast.IfStmt")
		}
	})
	t.Run("valid-if-else", func(t *testing.T) {
		s := firstStmt(t, "function main(string q) { if (true) {} else {} }").(*ast.IfStmt)
		if s.ElseBody() == nil {
			t.Error("ElseBody() = nil, want block")
		}
	})
	t.Run("valid-if-elif-else", func(t *testing.T) {
		s := firstStmt(t, "function main(string q) { if (true) {} else if (false) {} else {} }").(*ast.IfStmt)
		if len(s.ElseIfs()) != 1 {
			t.Errorf("len(ElseIfs()) = %d, want 1", len(s.ElseIfs()))
		}
	})
	t.Run("reject-missing-parens", func(t *testing.T) {
		conformErr(t, "function main(string q) { if true {} }", 1)
	})
}

// ── for (TestForStmt) ──

func TestGrammarForStmt(t *testing.T) {
	t.Run("valid-simple", func(t *testing.T) {
		if _, ok := firstStmt(t, "function main(string q) { for (x in items) {} }").(*ast.ForStmt); !ok {
			t.Fatal("stmts[0] not *ast.ForStmt")
		}
	})
	t.Run("valid-typed-var", func(t *testing.T) {
		if _, ok := firstStmt(t, "function main(string q) { for (var x in items) {} }").(*ast.ForStmt); !ok {
			t.Fatal("stmts[0] not *ast.ForStmt")
		}
	})
	t.Run("reject-missing-in", func(t *testing.T) {
		conformErr(t, "function main(string q) { for (x items) {} }", 1)
	})
}

// ── while (TestWhileStmt) ──

func TestGrammarWhileStmt(t *testing.T) {
	t.Run("valid-with-limit", func(t *testing.T) {
		s := firstStmt(t, "function main(string q) { while (true) limit 3 {} }").(*ast.WhileStmt)
		if s.Limit() != 3 {
			t.Errorf("Limit() = %d, want 3", s.Limit())
		}
	})
	t.Run("reject-missing-parens", func(t *testing.T) {
		conformErr(t, "function main(string q) { while true {} }", 1)
	})
	t.Run("reject-missing-limit", func(t *testing.T) {
		// Port of test_while_requires_static_limit: locks stmt.go's
		// "Expected static 'limit' after while condition" constraint.
		conformErr(t, "function main() { while (true) { break; } }", 1)
	})
}

// ── return (TestReturnStmt) ──

func TestGrammarReturnStmt(t *testing.T) {
	// The Python suite reads functions[0].body.stmts[0]; `function f` is a
	// plain function, not the entry, so use Functions() not Entry().
	firstFnBody := func(t *testing.T, src string) []ast.Stmt {
		t.Helper()
		fns := parseOK(t, src).Functions()
		if len(fns) != 1 {
			t.Fatalf("len(Functions()) = %d, want 1", len(fns))
		}
		return fns[0].Body().Stmts()
	}
	t.Run("valid-with-value", func(t *testing.T) {
		stmts := firstFnBody(t, `function f() -> string { return "x"; }`)
		ret, ok := stmts[0].(*ast.ReturnStmt)
		if !ok || ret.Value() == nil {
			t.Fatalf("stmts[0] = %T (Value %v), want ReturnStmt with value", stmts[0], ret.Value())
		}
	})
	t.Run("valid-void", func(t *testing.T) {
		stmts := firstFnBody(t, "function f() { return; }")
		ret := stmts[0].(*ast.ReturnStmt)
		if ret.Value() != nil {
			t.Error("Value() = non-nil, want nil")
		}
	})
	t.Run("reject-missing-semicolon", func(t *testing.T) {
		conformErr(t, "function f() { return 1 }", 1)
	})
}

// ── parallel (TestParallelStmt) ──

func TestGrammarParallelStmt(t *testing.T) {
	t.Run("valid-two-branches", func(t *testing.T) {
		s := firstStmt(t, "function main(string q) { parallel { {} {} } }").(*ast.ParallelStmt)
		if len(s.Branches()) != 2 {
			t.Errorf("len(Branches()) = %d, want 2", len(s.Branches()))
		}
	})
	t.Run("reject-missing-braces", func(t *testing.T) {
		conformErr(t, "function main(string q) { parallel }", 1)
	})
}

// ── break / continue (TestBreakContinue) ──

func TestGrammarBreakContinue(t *testing.T) {
	t.Run("valid-break", func(t *testing.T) {
		stmts := entryBody(t, "function main(string q) { for (x in y) { break; } }")
		body := stmts[0].(*ast.ForStmt).Body().Stmts()
		if _, ok := body[0].(*ast.BreakStmt); !ok {
			t.Errorf("body[0] = %T, want *ast.BreakStmt", body[0])
		}
	})
	t.Run("valid-continue", func(t *testing.T) {
		stmts := entryBody(t, "function main(string q) { for (x in y) { continue; } }")
		body := stmts[0].(*ast.ForStmt).Body().Stmts()
		if _, ok := body[0].(*ast.ContinueStmt); !ok {
			t.Errorf("body[0] = %T, want *ast.ContinueStmt", body[0])
		}
	})
	t.Run("reject-break-missing-semicolon", func(t *testing.T) {
		conformErr(t, "function main(string q) { for (x in y) { break } }", 1)
	})
}

// ── yield / answer (extra rule groups the brief names) ──

func TestGrammarYieldAnswer(t *testing.T) {
	t.Run("valid-yield", func(t *testing.T) {
		stmts := entryBody(t, "function main() { for (x in items) { yield x; } }")
		body := stmts[0].(*ast.ForStmt).Body().Stmts()
		if _, ok := body[0].(*ast.YieldStmt); !ok {
			t.Errorf("body[0] = %T, want *ast.YieldStmt", body[0])
		}
	})
	t.Run("valid-output", func(t *testing.T) {
		if _, ok := firstStmt(t, "function main() { output(\"hi\"); }").(*ast.OutputStmt); !ok {
			t.Fatal("stmts[0] not *ast.OutputStmt")
		}
	})
	t.Run("reject-yield-missing-semicolon", func(t *testing.T) {
		conformErr(t, "function main() { for (x in items) { yield x } }", 1)
	})
}

// ── attempt (attempt_stmt production) ──

func TestGrammarAttemptStmt(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		s := firstStmt(t, "function main() { attempt llm(MODEL, query) as result { success { return result; } failure (err) { return err; } } }").(*ast.AttemptStmt)
		if _, ok := s.Operation().(*ast.CallExpr); !ok {
			t.Errorf("Operation() = %T, want *ast.CallExpr", s.Operation())
		}
		if s.ResultBinding().Name() != "result" || s.ErrorBinding().Name() != "err" {
			t.Errorf("bindings = %s/%s, want result/err", s.ResultBinding().Name(), s.ErrorBinding().Name())
		}
	})
	t.Run("reject-missing-branches", func(t *testing.T) {
		conformErr(t, "function main() { attempt llm(MODEL, query) as result { } }", 1)
	})
}

// ── expr_or_assign_stmt (TestExprOrAssign) ──

func TestGrammarExprOrAssign(t *testing.T) {
	t.Run("valid-expr-stmt", func(t *testing.T) {
		if _, ok := firstStmt(t, "function main(string q) { foo(); }").(*ast.ExprStmt); !ok {
			t.Fatal("stmts[0] not *ast.ExprStmt")
		}
	})
	t.Run("valid-assign", func(t *testing.T) {
		if _, ok := firstStmt(t, "function main(string q) { x = 1; }").(*ast.AssignStmt); !ok {
			t.Fatal("stmts[0] not *ast.AssignStmt")
		}
	})
	for _, tc := range []struct{ name, src, operation, binary string }{
		{"plus", "x += 1;", "+=", "+"},
		{"minus", "x -= 1;", "-=", "-"},
		{"multiply", "x *= 2;", "*=", "*"},
		{"divide", "x /= 2;", "/=", "/"},
		{"array-append", "items << q;", "<<", ""},
	} {
		t.Run("valid-"+tc.name, func(t *testing.T) {
			s := firstStmt(t, "function main(string q) { "+tc.src+" }").(*ast.AssignStmt)
			if s.Operation() != tc.operation {
				t.Errorf("Operation() = %q, want %q", s.Operation(), tc.operation)
			}
			if tc.binary != "" {
				if b, ok := s.Value().(*ast.BinaryExpr); !ok || b.Op() != tc.binary {
					t.Errorf("Value() = %T, want BinaryExpr op %q", s.Value(), tc.binary)
				}
			}
		})
	}
	for _, op := range []string{"*=", "/=", "<<"} {
		t.Run("reject-"+op+"-missing-semicolon", func(t *testing.T) {
			conformErr(t, "function main(string q) { x "+op+" 1 }", 1)
		})
	}
	t.Run("reject-missing-semicolon", func(t *testing.T) {
		conformErr(t, "function main(string q) { x = 1 }", 1)
	})
}

// ── expression precedence (TestExprPrecedence) ──

func TestGrammarExprPrecedence(t *testing.T) {
	binary := []struct{ src, op string }{
		{"true || false", "||"},
		{"true && false", "&&"},
		{"1 == 2", "=="},
		{"1 != 2", "!="},
		{"1 < 2", "<"},
		{"1 <= 2", "<="},
		{"1 > 2", ">"},
		{"1 >= 2", ">="},
		{`"a" in ["a"]`, "in"},
		{"1 + 2", "+"},
		{"1 - 2", "-"},
		{"2 * 3", "*"},
		{"6 / 3", "/"},
		{"7 % 3", "%"},
	}
	for _, tc := range binary {
		t.Run("valid-"+tc.op, func(t *testing.T) {
			init := initOfFirstStmt(t, "function main(string q) { var x = "+tc.src+"; }")
			b, ok := init.(*ast.BinaryExpr)
			if !ok {
				t.Fatalf("init = %T, want *ast.BinaryExpr", init)
			}
			if b.Op() != tc.op {
				t.Errorf("Op() = %q, want %q", b.Op(), tc.op)
			}
		})
	}
	t.Run("valid-ternary", func(t *testing.T) {
		if _, ok := initOfFirstStmt(t, "function main(string q) { var x = true ? 1 : 2; }").(*ast.TernaryExpr); !ok {
			t.Fatal("init not *ast.TernaryExpr")
		}
	})
	t.Run("valid-not", func(t *testing.T) {
		u := initOfFirstStmt(t, "function main(string q) { var x = !true; }").(*ast.UnaryExpr)
		if u.Op() != "!" {
			t.Errorf("Op() = %q, want !", u.Op())
		}
	})
	t.Run("valid-negate", func(t *testing.T) {
		u := initOfFirstStmt(t, "function main(string q) { var x = -1; }").(*ast.UnaryExpr)
		if u.Op() != "-" {
			t.Errorf("Op() = %q, want -", u.Op())
		}
	})
	t.Run("precedence-mul-over-add", func(t *testing.T) {
		add := initOfFirstStmt(t, "function main(string q) { var x = 1 + 2 * 3; }").(*ast.BinaryExpr)
		if add.Op() != "+" {
			t.Fatalf("Op() = %q, want +", add.Op())
		}
		if r, ok := add.Right().(*ast.BinaryExpr); !ok || r.Op() != "*" {
			t.Errorf("Right() = %T, want BinaryExpr *", add.Right())
		}
	})
	t.Run("precedence-and-over-or", func(t *testing.T) {
		or := initOfFirstStmt(t, "function main(string q) { var x = a || b && c; }").(*ast.BinaryExpr)
		if or.Op() != "||" {
			t.Fatalf("Op() = %q, want ||", or.Op())
		}
		if r, ok := or.Right().(*ast.BinaryExpr); !ok || r.Op() != "&&" {
			t.Errorf("Right() = %T, want BinaryExpr &&", or.Right())
		}
	})
	t.Run("reject-in-missing-rhs", func(t *testing.T) {
		_, errs := ParseSource("function main(string q) { var x = 1 in; }", "<conformance>")
		if len(errs) == 0 {
			t.Fatal("expected errors")
		}
		if !strings.Contains(errs[0].Message, "Expected expression") {
			t.Errorf("errs[0].Message = %q, want it to mention 'Expected expression'", errs[0].Message)
		}
	})
}

// ── postfix (TestPostfix) ──

func TestGrammarPostfix(t *testing.T) {
	t.Run("valid-field-access", func(t *testing.T) {
		fa := initOfFirstStmt(t, "function main(string q) { var x = a.b; }").(*ast.FieldAccessExpr)
		if fa.FieldName() != "b" {
			t.Errorf("FieldName() = %q, want b", fa.FieldName())
		}
	})
	t.Run("valid-index", func(t *testing.T) {
		if _, ok := initOfFirstStmt(t, "function main(string q) { var x = a[0]; }").(*ast.IndexExpr); !ok {
			t.Fatal("init not *ast.IndexExpr")
		}
	})
	t.Run("valid-call", func(t *testing.T) {
		c := initOfFirstStmt(t, "function main(string q) { var x = foo(1, 2); }").(*ast.CallExpr)
		if len(c.Args()) != 2 {
			t.Errorf("len(Args()) = %d, want 2", len(c.Args()))
		}
	})
	t.Run("valid-chained", func(t *testing.T) {
		fa := initOfFirstStmt(t, "function main(string q) { var x = a.b.c; }").(*ast.FieldAccessExpr)
		if _, ok := fa.Obj().(*ast.FieldAccessExpr); !ok {
			t.Errorf("Obj() = %T, want *ast.FieldAccessExpr", fa.Obj())
		}
	})
	t.Run("valid-call-then-field", func(t *testing.T) {
		fa := initOfFirstStmt(t, "function main(string q) { var x = foo().bar; }").(*ast.FieldAccessExpr)
		if _, ok := fa.Obj().(*ast.CallExpr); !ok {
			t.Errorf("Obj() = %T, want *ast.CallExpr", fa.Obj())
		}
	})
}

// ── primary literals (TestPrimary) ──

func TestGrammarPrimary(t *testing.T) {
	t.Run("valid-int", func(t *testing.T) {
		if _, ok := initOfFirstStmt(t, "function main(string q) { var x = 42; }").(*ast.IntLit); !ok {
			t.Fatal("init not *ast.IntLit")
		}
	})
	t.Run("valid-float", func(t *testing.T) {
		if _, ok := initOfFirstStmt(t, "function main(string q) { var x = 3.14; }").(*ast.FloatLit); !ok {
			t.Fatal("init not *ast.FloatLit")
		}
	})
	t.Run("valid-string", func(t *testing.T) {
		if _, ok := initOfFirstStmt(t, `function main(string q) { var x = "hi"; }`).(*ast.StringLit); !ok {
			t.Fatal("init not *ast.StringLit")
		}
	})
	t.Run("valid-bool-true", func(t *testing.T) {
		b := initOfFirstStmt(t, "function main(string q) { var x = true; }").(*ast.BoolLit)
		if !b.Value() {
			t.Error("Value() = false, want true")
		}
	})
	t.Run("valid-bool-false", func(t *testing.T) {
		b := initOfFirstStmt(t, "function main(string q) { var x = false; }").(*ast.BoolLit)
		if b.Value() {
			t.Error("Value() = true, want false")
		}
	})
	t.Run("valid-null", func(t *testing.T) {
		if _, ok := initOfFirstStmt(t, "function main(string q) { var x = null; }").(*ast.NullLit); !ok {
			t.Fatal("init not *ast.NullLit")
		}
	})
	t.Run("valid-identifier", func(t *testing.T) {
		if _, ok := initOfFirstStmt(t, "function main(string q) { var x = foo; }").(*ast.IdentExpr); !ok {
			t.Fatal("init not *ast.IdentExpr")
		}
	})
	t.Run("valid-grouped", func(t *testing.T) {
		if _, ok := initOfFirstStmt(t, "function main(string q) { var x = (1 + 2); }").(*ast.BinaryExpr); !ok {
			t.Fatal("init not *ast.BinaryExpr")
		}
	})
	t.Run("valid-empty-array", func(t *testing.T) {
		a := initOfFirstStmt(t, "function main(string q) { var x = []; }").(*ast.ArrayLit)
		if len(a.Elements()) != 0 {
			t.Errorf("len(Elements()) = %d, want 0", len(a.Elements()))
		}
	})
	t.Run("valid-array", func(t *testing.T) {
		a := initOfFirstStmt(t, "function main(string q) { var x = [1, 2, 3]; }").(*ast.ArrayLit)
		if len(a.Elements()) != 3 {
			t.Errorf("len(Elements()) = %d, want 3", len(a.Elements()))
		}
	})
	t.Run("valid-empty-map", func(t *testing.T) {
		m := initOfFirstStmt(t, "function main(string q) { var x = {}; }").(*ast.MapLit)
		if len(m.Entries()) != 0 {
			t.Errorf("len(Entries()) = %d, want 0", len(m.Entries()))
		}
	})
	t.Run("valid-map", func(t *testing.T) {
		m := initOfFirstStmt(t, `function main(string q) { var x = {"a": 1, "b": 2}; }`).(*ast.MapLit)
		if len(m.Entries()) != 2 {
			t.Errorf("len(Entries()) = %d, want 2", len(m.Entries()))
		}
	})
	t.Run("reject-unterminated-string", func(t *testing.T) {
		// Lexer-level failure surfaces through ParseSource as an error.
		prog, errs := ParseSource(`function main(string q) { var x = "unterminated; }`, "<conformance>")
		if len(errs) == 0 {
			t.Fatal("expected errors for unterminated string")
		}
		if prog != nil {
			t.Error("prog = non-nil, want nil")
		}
	})
}

// ── template strings / lambda / operation policy / for-expr (brief extras) ──

func TestGrammarTemplateLambdaPolicy(t *testing.T) {
	t.Run("valid-template-string", func(t *testing.T) {
		init := initOfFirstStmt(t, "function main(string q) { var x = `Hello ${q}!`; }")
		ts, ok := init.(*ast.TemplateString)
		if !ok {
			t.Fatalf("init = %T, want *ast.TemplateString", init)
		}
		if len(ts.Parts()) < 2 {
			t.Errorf("len(Parts()) = %d, want >= 2", len(ts.Parts()))
		}
	})
	t.Run("valid-lambda", func(t *testing.T) {
		// Port of TestLambdaInCallArgument: flatMap(groups, (group) -> group).
		prog := conformOK(t, "function main() { var r = flatMap(groups, (group) -> group); }")
		vd := prog.Entry().Body().Stmts()[0].(*ast.VarDeclStmt)
		call := vd.Init().(*ast.CallExpr)
		if _, ok := call.Args()[1].(*ast.LambdaExpr); !ok {
			t.Errorf("Args()[1] = %T, want *ast.LambdaExpr", call.Args()[1])
		}
	})
	t.Run("valid-operation-policy", func(t *testing.T) {
		prog := conformOK(t, "function main(string q) -> string { let r = llm(MODEL, q) with retry(max: 2, interval: 1s); return r; }")
		vd := prog.Entry().Body().Stmts()[0].(*ast.VarDeclStmt)
		op, ok := vd.Init().(*ast.OperationPolicyExpr)
		if !ok {
			t.Fatalf("Init() = %T, want *ast.OperationPolicyExpr", vd.Init())
		}
		rp, ok := op.Policies()[0].(*ast.RetryPolicy)
		if !ok || rp.MaxRetries() != 2 || rp.IntervalMs() != 1000 {
			t.Errorf("Policies()[0] = %T (retries %d, ms %d), want retry(2, 1000)",
				op.Policies()[0], op.Policies()[0].(*ast.RetryPolicy).MaxRetries(), op.Policies()[0].(*ast.RetryPolicy).IntervalMs())
		}
	})
	t.Run("valid-for-expr", func(t *testing.T) {
		// test_reference_parser_accepts_declared_constructs: value-producing for.
		init := initOfFirstStmt(t, "function main() { let r = for (x in xs) { yield x; }; }")
		fe, ok := init.(*ast.ForExpr)
		if !ok {
			t.Fatalf("init = %T, want *ast.ForExpr", init)
		}
		if fe.VarName() != "x" {
			t.Errorf("VarName() = %q, want x", fe.VarName())
		}
	})
}

// ── syntax-level differential gate (test_grammar_differential.py) ──

func TestGrammarDifferentialSyntaxAccepts(t *testing.T) {
	cases := []string{
		"function main() { return 42; }",
		"@mode workflow\nfunction main(int v) { if (v > 1) { return v; } return 0; }",
		"@mode workflow\nfunction main() { var x = 1; while (x < 5) limit 3 { x += 1; } return x; }",
		"@mode workflow\nfunction main() { let r = for (x in xs) { yield x; }; return r; }",
		"@mode workflow\nconst M = \"gpt\";\nfunction main() { return M; }",
	}
	for _, src := range cases {
		t.Run(src[:28], func(t *testing.T) {
			conformOK(t, src)
		})
	}
}

// ── full conformance programs (TestFullConformance) ──

func TestGrammarFullConformance(t *testing.T) {
	t.Run("minimal-workflow", func(t *testing.T) {
		conformOK(t, `
			function main(string q) {
				output(q);
			}
		`)
	})
	t.Run("function-with-return", func(t *testing.T) {
		conformOK(t, `
			function add(int a, int b) -> int {
				return a + b;
			}
			function main(string q) {
				var r = add(1, 2);
				output(r);
			}
		`)
	})
	t.Run("if-else-workflow", func(t *testing.T) {
		conformOK(t, `
			function main(string q) {
				if (q == "hi") {
					output("hello");
				} else {
					output("unknown");
				}
			}
		`)
	})
}

// ── example smoke: the five canonical .ncoda files must parse cleanly ──

func TestGrammarExampleSmoke(t *testing.T) {
	samples := []string{
		"../../examples/nodecoda/00_minimal_start_llm_end.ncoda",
		"../../examples/nodecoda/03_conditional_branch.ncoda",
		"../../examples/nodecoda/05_http_request.ncoda",
		"../../examples/nodecoda/09_iteration.ncoda",
		"../../examples/nodecoda/12_loop_with_break.ncoda",
	}
	for _, path := range samples {
		t.Run(path, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			prog, errs := ParseSource(string(src), path)
			if len(errs) != 0 {
				t.Errorf("%s: %d parse errors (first: %v)", path, len(errs), errs[0])
			}
			if prog == nil {
				t.Errorf("%s: nil program", path)
			}
			if prog.Entry() == nil {
				t.Errorf("%s: no entry function", path)
			}
		})
	}
}

// TestDeadKeywordsUsableAsIdentifiers is the A1 regression lock: 'entry' and
// 'stream' were reserved keywords with no grammar production (dead words);
// after removal they must parse as ordinary identifiers.
func TestDeadKeywordsUsableAsIdentifiers(t *testing.T) {
	src := `@mode workflow

function main() {
    let entry = 5;
    let stream = 1;
    return entry + stream;
}
`
	prog := conformOK(t, src)
	_ = prog
}
