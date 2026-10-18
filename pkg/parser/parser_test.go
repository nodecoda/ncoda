// Tests for the parser skeleton: cursor utilities, error collection, and
// recovery. Error-path cases are ported from the Python reference suite
// (tests/lang/unit/test_parser_errors.py) down to the utility level: the
// full grammar productions land in later tasks, so the "missing token",
// "illegal sequence", "span" and "recovery" behaviors are exercised through
// the helpers the productions will use (consume/error/codedError/synchronize).
package parser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/lexer"
	"github.com/nodecoda/ncoda/pkg/token"
)

// mkTok builds a token at a given 1-based line/column.
func mkTok(typ token.Type, lexeme string, line, col int) token.Token {
	return token.Token{
		Type:     typ,
		Lexeme:   lexeme,
		Location: token.Location{Line: line, Column: col, Offset: col - 1},
	}
}

// lex tokenizes a source string with the real lexer (ends with EOF).
func lex(src string) []token.Token { return lexer.New(src, "").Tokenize() }

// ── Empty input / Parse() skeleton ──

func TestParseEmptyInputReturnsEmptyProgram(t *testing.T) {
	cases := []struct {
		name   string
		tokens []token.Token
	}{
		{"empty-source", lex("")},
		{"no-tokens", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, errs := New(tc.tokens).Parse()
			if prog == nil {
				t.Fatal("Parse() returned a nil program")
			}
			if len(errs) != 0 {
				t.Fatalf("expected no errors for empty input, got %v", errs)
			}
			// Python ProgramNode() zero-value fields: mode "workflow", all else empty.
			if prog.Mode() != "workflow" {
				t.Errorf("Mode() = %q, want %q", prog.Mode(), "workflow")
			}
			if prog.LanguageIdentity() != "" {
				t.Errorf("LanguageIdentity() = %q, want empty", prog.LanguageIdentity())
			}
			if len(prog.ConstDecls()) != 0 || len(prog.Outputs()) != 0 ||
				len(prog.EnumDecls()) != 0 || len(prog.TypeDecls()) != 0 ||
				len(prog.ConversationDecls()) != 0 || len(prog.CodeFunctions()) != 0 ||
				len(prog.Functions()) != 0 {
				t.Errorf("expected empty program, got populated decls")
			}
			if prog.Entry() != nil {
				t.Errorf("Entry() = %v, want nil", prog.Entry())
			}
		})
	}
}

func TestParseReturnsProgramAndErrorsSlice(t *testing.T) {
	prog, errs := New(lex("function main() { }")).Parse()
	if prog == nil {
		t.Fatal("Parse() returned a nil program")
	}
	// Go convention: a nil slice is an empty slice; just assert emptiness.
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

// ── Cursor utilities (advance/check/match/peek/previous) ──

func TestCursorUtilities(t *testing.T) {
	toks := []token.Token{
		mkTok(token.IDENTIFIER, "a", 1, 1),
		mkTok(token.SEMICOLON, ";", 1, 3),
		mkTok(token.EOF, "", 1, 4),
	}
	p := New(toks)

	if p.peek().Lexeme != "a" {
		t.Errorf("peek() = %q, want %q", p.peek().Lexeme, "a")
	}
	if p.isAtEnd() {
		t.Error("isAtEnd() = true before any token is consumed")
	}
	// previous() before any advance mirrors Python's tokens[-1] (the EOF token).
	if got := p.previous().Type; got != token.EOF {
		t.Errorf("previous() at cursor 0 = %s, want EOF", got)
	}

	if adv := p.advance(); adv.Lexeme != "a" {
		t.Errorf("advance() = %q, want %q", adv.Lexeme, "a")
	}
	if p.previous().Lexeme != "a" {
		t.Errorf("previous() = %q, want %q", p.previous().Lexeme, "a")
	}
	if p.peek().Type != token.SEMICOLON {
		t.Errorf("peek() after one advance = %s, want SEMICOLON", p.peek().Type)
	}

	if !p.check(token.SEMICOLON) {
		t.Error("check(SEMICOLON) = false, want true")
	}
	if p.check(token.EOF) {
		t.Error("check(EOF) = true, want false")
	}
	if !p.check(token.SEMICOLON, token.COMMA) {
		t.Error("check(SEMICOLON, COMMA) = false, want true (variadic)")
	}

	got, ok := p.match(token.COMMA)
	if ok {
		t.Error("match(COMMA) matched a non-matching token")
	}
	if got.Type != 0 {
		t.Errorf("match() on miss = %v, want zero token", got)
	}
	if p.peek().Type != token.SEMICOLON {
		t.Error("match() on miss must not advance the cursor")
	}

	got, ok = p.match(token.SEMICOLON, token.COMMA)
	if !ok {
		t.Fatal("match(SEMICOLON, COMMA) did not match")
	}
	if got.Lexeme != ";" {
		t.Errorf("match() = %q, want %q", got.Lexeme, ";")
	}
	if !p.isAtEnd() {
		t.Error("isAtEnd() = false after consuming the last real token")
	}

	// advance() at EOF must not move the cursor; it returns the last real token.
	if last := p.advance(); last.Lexeme != ";" {
		t.Errorf("advance() at EOF = %q, want %q (previous token)", last.Lexeme, ";")
	}
	if p.current != 2 {
		t.Errorf("cursor after advance at EOF = %d, want 2", p.current)
	}
}

func TestCheckNext(t *testing.T) {
	toks := []token.Token{
		mkTok(token.KW_FUNCTION, "function", 1, 1),
		mkTok(token.IDENTIFIER, "main", 1, 10),
		mkTok(token.EOF, "", 1, 14),
	}
	p := New(toks)
	if !p.checkNext(token.IDENTIFIER) {
		t.Error("checkNext(IDENTIFIER) = false, want true")
	}
	if p.checkNext(token.EOF) {
		t.Error("checkNext(EOF) = true before the last token, want false")
	}
	p.advance()
	p.advance()
	if !p.checkNext(token.EOF) {
		t.Error("checkNext(EOF) at the last token = false, want true (clamped)")
	}
}

// ── consume: matching token, missing token, span ──

func TestConsumeMatchesAndAdvances(t *testing.T) {
	toks := []token.Token{
		mkTok(token.IDENTIFIER, "a", 1, 1),
		mkTok(token.EOF, "", 1, 3),
	}
	p := New(toks)
	got, err := p.consume(token.IDENTIFIER, "Expected identifier")
	if err != nil {
		t.Fatalf("consume() on a match returned an error: %v", err)
	}
	if got.Lexeme != "a" {
		t.Errorf("consume() = %q, want %q", got.Lexeme, "a")
	}
	if len(p.Errors()) != 0 {
		t.Errorf("consume() on a match recorded %d errors, want 0", len(p.Errors()))
	}
	if p.peek().Type != token.EOF {
		t.Errorf("consume() on a match did not advance (peek = %s)", p.peek().Type)
	}
}

func TestConsumeMissingTokenRecordsErrorWithSpan(t *testing.T) {
	// Port of test_parser_errors.py:TestMissingTokens (missing semicolon /
	// bracket): a required token that is absent records a ParseError carrying
	// the offending token's span and does not advance.
	toks := []token.Token{
		mkTok(token.INT_LITERAL, "42", 1, 14),
		mkTok(token.EOF, "", 1, 16),
	}
	p := New(toks)
	got, err := p.consume(token.SEMICOLON, "Expected ';' after statement")
	if err == nil {
		t.Fatal("consume() on a mismatch must return a *ParseError")
	}
	if got.Type != 0 {
		t.Errorf("consume() on a mismatch = %v, want zero token", got)
	}
	wantMsg := "Expected ';' after statement, got INT_LITERAL ('42')"
	if err.Message != wantMsg {
		t.Errorf("Message = %q, want %q", err.Message, wantMsg)
	}
	if len(p.Errors()) != 1 {
		t.Fatalf("len(Errors()) = %d, want 1", len(p.Errors()))
	}
	if p.Errors()[0] != err {
		t.Error("the returned error was not recorded in the errors slice")
	}
	want := token.Span{Start: toks[0].Location, End: toks[0].Location}
	if err.Span != want {
		t.Errorf("Span = %v, want %v (current token span)", err.Span, want)
	}
	if p.peek().Lexeme != "42" {
		t.Error("consume() on a mismatch must not advance the cursor")
	}
}

func TestConsumeAtEOFRecordsEOFSpan(t *testing.T) {
	// Port of test_missing_brace_close: an unterminated block runs into EOF;
	// the required closing token is reported at the EOF token's location.
	toks := lex("function main() { var x = 42;")
	p := New(toks)
	for !p.isAtEnd() {
		p.advance() // run the cursor into EOF, as a real parse would
	}
	_, err := p.consume(token.RBRACE, "Expected '}' after block")
	if err == nil {
		t.Fatal("expected a *ParseError at EOF")
	}
	last := toks[len(toks)-1] // EOF token
	if err.Span.Start != last.Location || err.Span.End != last.Location {
		t.Errorf("Span = %v, want point span at EOF location %v", err.Span, last.Location)
	}
	if !strings.Contains(err.Message, "EOF") {
		t.Errorf("Message = %q, want it to name the EOF token", err.Message)
	}
}

func TestErrorLocationAccurateAcrossLines(t *testing.T) {
	// Port of test_error_location_accurate: the recorded span points at the
	// offending token even on a later line.
	toks := []token.Token{
		mkTok(token.INT_LITERAL, "42", 2, 5),
		mkTok(token.EOF, "", 2, 7),
	}
	p := New(toks)
	_, err := p.consume(token.SEMICOLON, "Expected ';'")
	if err == nil {
		t.Fatal("expected a *ParseError")
	}
	if err.Span.Start.Line != 2 || err.Span.Start.Column != 5 {
		t.Errorf("Span.Start = %v, want line 2 col 5", err.Span.Start)
	}
	if err.Span.Start.Offset != 4 {
		t.Errorf("Span.Start.Offset = %d, want 4", err.Span.Start.Offset)
	}
}

// ── error(): plain syntax errors, accumulation, illegal sequences ──

func TestErrorCollectsIntoSlice(t *testing.T) {
	// Port of test_collects_multiple_errors: errors accumulate without
	// aborting; each carries the current token's span.
	p := New(lex(""))
	if len(p.Errors()) != 0 {
		t.Fatalf("fresh parser has %d errors, want 0", len(p.Errors()))
	}
	first := p.error("first problem")
	second := p.error("second problem")
	if len(p.Errors()) != 2 {
		t.Fatalf("len(Errors()) = %d, want 2", len(p.Errors()))
	}
	if p.Errors()[0] != first || p.Errors()[1] != second {
		t.Error("errors not recorded in order")
	}
}

func TestIllegalTokenSequenceRecordsError(t *testing.T) {
	// Port of test_expression_at_top_level ("42 + 1;"): an illegal token
	// sequence is reported via error() naming the offending token and
	// carrying its span.
	p := New(lex("42 + 1;"))
	err := p.error("Expected declaration at top level")
	if err == nil {
		t.Fatal("error() returned nil")
	}
	if !strings.Contains(err.Message, "INT_LITERAL") {
		t.Errorf("Message = %q, want it to name the INT_LITERAL token", err.Message)
	}
	if !strings.Contains(err.Message, "'42'") {
		t.Errorf("Message = %q, want it to quote the lexeme '42'", err.Message)
	}
	if err.Span.Start.Line != 1 || err.Span.Start.Column != 1 {
		t.Errorf("Span.Start = %v, want line 1 col 1", err.Span.Start)
	}
}

func TestCodedErrorSetsCodeAndSpan(t *testing.T) {
	p := New(lex(""))
	err := p.codedError(diag.SYNTAX_ERROR, "coded problem")
	if err == nil {
		t.Fatal("codedError() returned nil")
	}
	if err.Code != diag.SYNTAX_ERROR {
		t.Errorf("Code = %d, want %d (SYNTAX_ERROR)", err.Code, diag.SYNTAX_ERROR)
	}
	if !err.NumericWire {
		t.Error("NumericWire = false, want true (Python parser passes bare int codes through codedError)")
	}
	if len(p.Errors()) != 1 {
		t.Errorf("len(Errors()) = %d, want 1", len(p.Errors()))
	}

	// Explicit-token variant attaches the given token's span instead of the
	// current token's.
	explicit := mkTok(token.IDENTIFIER, "x", 4, 9)
	err2 := p.codedError(diag.SYNTAX_ERROR, "at explicit token", &explicit)
	if err2.Span.Start != explicit.Location || err2.Span.End != explicit.Location {
		t.Errorf("Span = %v, want point span at %v", err2.Span, explicit.Location)
	}
}

// ── ParseError model ──

func TestParseErrorString(t *testing.T) {
	var _ error = (*ParseError)(nil) // must satisfy the error interface

	loc := token.Location{Line: 2, Column: 5, Offset: 7}
	base := token.Span{Start: loc, End: loc}

	plain := &ParseError{Message: "syntax problem", Span: base}
	if got, want := plain.String(), "syntax problem at 2:5-2:5"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	coded := &ParseError{Code: diag.SYNTAX_ERROR, Message: "syntax problem", Span: base}
	if got, want := coded.String(), "E1000: syntax problem at 2:5-2:5"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	if plain.Error() != plain.String() {
		t.Errorf("Error() = %q, want identical to String() %q", plain.Error(), plain.String())
	}
}

func TestParseErrorZeroCodeIsPlain(t *testing.T) {
	// A zero/unset Code renders without the "E####:" prefix, mirroring
	// parser.py ParseError with code=None.
	err := &ParseError{
		Message: "plain syntax error",
		Span:    token.Span{Start: token.Location{Line: 1, Column: 1}, End: token.Location{Line: 1, Column: 1}},
	}
	if err.Code != 0 {
		t.Fatalf("Code = %d, want 0 (unset)", err.Code)
	}
	if strings.Contains(err.String(), "E0000") {
		t.Errorf("String() = %q, zero code must not render a code prefix", err.String())
	}
}

// ── synchronize(): statement-boundary recovery ──

func TestSynchronizeSkipsToStatementBoundary(t *testing.T) {
	// Port of test_recovers_after_error: after an error mid-statement,
	// synchronize() discards tokens until a statement boundary (semicolon,
	// declaration keyword, or RBRACE) so parsing can resume.
	toks := []token.Token{
		mkTok(token.IDENTIFIER, "x", 1, 1),
		mkTok(token.ASSIGN, "=", 1, 3),
		mkTok(token.SEMICOLON, ";", 1, 5),
		mkTok(token.KW_FUNCTION, "function", 2, 1),
		mkTok(token.IDENTIFIER, "main", 2, 10),
		mkTok(token.EOF, "", 2, 14),
	}
	p := New(toks)
	p.synchronize()
	if p.peek().Type != token.KW_FUNCTION {
		t.Errorf("peek() after synchronize = %s, want KW_FUNCTION", p.peek().Type)
	}
}

func TestSynchronizeStopsAtRBRACEAndEOF(t *testing.T) {
	// RBRACE is a boundary: recovery stops just before the closing brace.
	toks := []token.Token{
		mkTok(token.IDENTIFIER, "x", 1, 1),
		mkTok(token.RBRACE, "}", 1, 3),
		mkTok(token.EOF, "", 1, 4),
	}
	p := New(toks)
	p.synchronize()
	if p.peek().Type != token.RBRACE {
		t.Errorf("peek() after synchronize = %s, want RBRACE", p.peek().Type)
	}

	// An unterminated statement runs synchronize to EOF and stops safely.
	toks2 := []token.Token{
		mkTok(token.IDENTIFIER, "x", 1, 1),
		mkTok(token.PLUS, "+", 1, 3),
		mkTok(token.EOF, "", 1, 4),
	}
	p2 := New(toks2)
	p2.synchronize()
	if !p2.isAtEnd() {
		t.Errorf("isAtEnd() after synchronize = false, want true (peek %s)", p2.peek().Type)
	}

	// Empty token list: synchronize is a no-op.
	p3 := New(nil)
	p3.synchronize()
	if !p3.isAtEnd() {
		t.Error("isAtEnd() after synchronize on empty input = false")
	}
}

func TestSynchronizeStopsAtDeclarationKeyword(t *testing.T) {
	// Recovery stops before a top-level declaration keyword (KW_RETURN,
	// KW_VAR, KW_IF, ... are statement boundaries in parser.py:_synchronize).
	toks := []token.Token{
		mkTok(token.INT_LITERAL, "42", 1, 1),
		mkTok(token.PLUS, "+", 1, 4),
		mkTok(token.KW_VAR, "var", 2, 1),
		mkTok(token.IDENTIFIER, "x", 2, 5),
		mkTok(token.EOF, "", 2, 7),
	}
	p := New(toks)
	p.synchronize()
	if p.peek().Type != token.KW_VAR {
		t.Errorf("peek() after synchronize = %s, want KW_VAR", p.peek().Type)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Task 3: top-level declarations — ports of test_parser.py declaration and
// type-ref cases, test_const_decl.py parser cases, and test_grammar_conformance.py
// declaration cases.
// ─────────────────────────────────────────────────────────────────────────────

// parseOK lexes and parses source, failing the test on any parse error
// (mirrors the Python parse() helper, which asserts parser.errors == []).
func parseOK(t *testing.T, src string) *ast.Program {
	t.Helper()
	prog, errs := New(lex(src)).Parse()
	if len(errs) != 0 {
		t.Fatalf("parse %q: unexpected errors: %v", src, errs)
	}
	return prog
}

// ── @language / @mode ──

func TestLanguageIdentityIsOptional(t *testing.T) {
	// Port of test_language_identity_is_optional_in_the_internal_parser.
	if got := parseOK(t, "function main() {}").LanguageIdentity(); got != "" {
		t.Errorf("LanguageIdentity() = %q, want empty", got)
	}
	prog := parseOK(t, "@language nodecoda/1\nfunction main() {}")
	if got := prog.LanguageIdentity(); got != "nodecoda/1" {
		t.Errorf("LanguageIdentity() = %q, want %q", got, "nodecoda/1")
	}
}

func TestParseProgramWithLanguageMode(t *testing.T) {
	// Port of test_parse_program_with_language_mode: @language and @mode
	// land in the Program identity/mode fields (parser.py parse_language_decl
	// / parse_mode_decl sections of _parse_program).
	prog := parseOK(t, "@language nodecoda/1\n@mode workflow\nfunction main() {}")
	if got := prog.LanguageIdentity(); got != "nodecoda/1" {
		t.Errorf("LanguageIdentity() = %q, want %q", got, "nodecoda/1")
	}
	if got := prog.Mode(); got != "workflow" {
		t.Errorf("Mode() = %q, want %q", got, "workflow")
	}
	if prog.Entry() == nil || prog.Entry().Name() != "main" {
		t.Errorf("Entry() = %v, want function main", prog.Entry())
	}

	prog2 := parseOK(t, "@mode advanced-chat\nfunction main() {}")
	if got := prog2.Mode(); got != "advanced-chat" {
		t.Errorf("Mode() = %q, want %q", got, "advanced-chat")
	}
}

func TestUnknownModeRecordsError(t *testing.T) {
	// parser.py: an unknown mode raises "Unknown mode: ..." which escapes
	// parse(); the error is collected in the errors slice.
	_, errs := New(lex("@mode foobar\nfunction main() {}")).Parse()
	if len(errs) == 0 {
		t.Fatal("expected an error for an unknown mode")
	}
	if !strings.Contains(errs[0].Message, "Unknown mode: foobar") {
		t.Errorf("Message = %q, want it to name the unknown mode", errs[0].Message)
	}
}

// ── const declarations ──

func TestConstDecl(t *testing.T) {
	// Port of TestConstParser cases from test_const_decl.py.

	t.Run("string", func(t *testing.T) {
		prog := parseOK(t, `const MODEL = "gpt-4o";
function main() { return "ok"; }`)
		decls := prog.ConstDecls()
		if len(decls) != 1 {
			t.Fatalf("len(ConstDecls()) = %d, want 1", len(decls))
		}
		if decls[0].Name() != "MODEL" {
			t.Errorf("Name() = %q, want %q", decls[0].Name(), "MODEL")
		}
		v, ok := decls[0].Value().(*ast.StringLit)
		if !ok {
			t.Fatalf("Value() = %T, want *ast.StringLit", decls[0].Value())
		}
		if v.Value() != "gpt-4o" {
			t.Errorf("StringLit.Value() = %q, want %q", v.Value(), "gpt-4o")
		}
	})

	t.Run("int", func(t *testing.T) {
		prog := parseOK(t, "const MAX = 42;\nfunction main() { return \"ok\"; }")
		decls := prog.ConstDecls()
		if len(decls) != 1 {
			t.Fatalf("len(ConstDecls()) = %d, want 1", len(decls))
		}
		v, ok := decls[0].Value().(*ast.IntLit)
		if !ok {
			t.Fatalf("Value() = %T, want *ast.IntLit", decls[0].Value())
		}
		if v.Value() != 42 {
			t.Errorf("IntLit.Value() = %d, want 42", v.Value())
		}
	})

	t.Run("multiple", func(t *testing.T) {
		prog := parseOK(t, "const A = \"hello\";\nconst B = 10;\nfunction main() { return \"ok\"; }")
		decls := prog.ConstDecls()
		if len(decls) != 2 {
			t.Fatalf("len(ConstDecls()) = %d, want 2", len(decls))
		}
		if decls[0].Name() != "A" || decls[1].Name() != "B" {
			t.Errorf("names = [%q %q], want [A B]", decls[0].Name(), decls[1].Name())
		}
	})

	t.Run("before-type", func(t *testing.T) {
		prog := parseOK(t, "const X = \"test\";\ntype Foo = { string bar; };\nfunction main() { return \"ok\"; }")
		if len(prog.ConstDecls()) != 1 {
			t.Errorf("len(ConstDecls()) = %d, want 1", len(prog.ConstDecls()))
		}
		if len(prog.TypeDecls()) != 1 {
			t.Errorf("len(TypeDecls()) = %d, want 1", len(prog.TypeDecls()))
		}
	})

	t.Run("bool", func(t *testing.T) {
		prog := parseOK(t, "const FLAG = true;\nfunction main() { return \"ok\"; }")
		decls := prog.ConstDecls()
		if len(decls) != 1 {
			t.Fatalf("len(ConstDecls()) = %d, want 1", len(decls))
		}
		v, ok := decls[0].Value().(*ast.BoolLit)
		if !ok {
			t.Fatalf("Value() = %T, want *ast.BoolLit", prog.ConstDecls()[0].Value())
		}
		if !v.Value() {
			t.Error("BoolLit.Value() = false, want true")
		}
	})

	t.Run("float", func(t *testing.T) {
		prog := parseOK(t, "const PI = 3.14;\nfunction main() { return \"ok\"; }")
		decls := prog.ConstDecls()
		if len(decls) != 1 {
			t.Fatalf("len(ConstDecls()) = %d, want 1", len(decls))
		}
		v, ok := decls[0].Value().(*ast.FloatLit)
		if !ok {
			t.Fatalf("Value() = %T, want *ast.FloatLit", prog.ConstDecls()[0].Value())
		}
		if v.Value() != 3.14 {
			t.Errorf("FloatLit.Value() = %v, want 3.14", v.Value())
		}
	})

	t.Run("non-literal-rejected", func(t *testing.T) {
		// Port of test_const_nonliteral_error: "const X = 1 + 2;" fails at
		// the missing ';' (parse_const_value only reads one literal); error
		// recovery skips the const decl entirely.
		prog, errs := New(lex("const X = 1 + 2;\nfunction main() { return \"ok\"; }")).Parse()
		if len(errs) != 1 {
			t.Fatalf("len(errs) = %d, want exactly 1 (the missing ';')", len(errs))
		}
		if len(prog.ConstDecls()) != 0 {
			t.Errorf("len(ConstDecls()) = %d, want 0 (recovered past the invalid const)", len(prog.ConstDecls()))
		}
	})

	t.Run("identifier-rejected", func(t *testing.T) {
		// Port of test_const_referencing_const_rejected: "const B = A;" is
		// rejected (identifiers are not literals) and recovery skips B.
		prog, errs := New(lex("const A = \"hello\";\nconst B = A;\nfunction main() { return \"ok\"; }")).Parse()
		if len(errs) != 1 {
			t.Fatalf("len(errs) = %d, want exactly 1 (const value must be a literal)", len(errs))
		}
		if !strings.Contains(errs[0].Message, "Const value must be a literal") {
			t.Errorf("Message = %q, want the const-literal message", errs[0].Message)
		}
		decls := prog.ConstDecls()
		if len(decls) != 1 {
			t.Fatalf("len(ConstDecls()) = %d, want 1 (only A)", len(decls))
		}
		if decls[0].Name() != "A" {
			t.Errorf("ConstDecls()[0].Name() = %q, want A", prog.ConstDecls()[0].Name())
		}
	})

	t.Run("missing-assignment", func(t *testing.T) {
		// Port of TestConstDecl.test_reject_missing_assignment.
		_, errs := New(lex(`const STATUS "new"; function main() {}`)).Parse()
		if len(errs) == 0 {
			t.Fatal("expected an error for a missing '=' after the const name")
		}
		if !strings.Contains(errs[0].Message, "Expected '=' after constant name") {
			t.Errorf("Message = %q, want it to name the missing '='", errs[0].Message)
		}
	})

	t.Run("template-string", func(t *testing.T) {
		// Const values allow template strings; ${expr} parts split into
		// alternating literal/expression parts (parser.py:_parse_const_value
		// + _parse_template_string).
		prog := parseOK(t, "const GREETING = `Hello ${NAME}!`;\nfunction main() { return GREETING; }")
		decls := prog.ConstDecls()
		if len(decls) != 1 {
			t.Fatalf("len(ConstDecls()) = %d, want 1", len(decls))
		}
		ts, ok := decls[0].Value().(*ast.TemplateString)
		if !ok {
			t.Fatalf("Value() = %T, want *ast.TemplateString", decls[0].Value())
		}
		parts := ts.Parts()
		if len(parts) != 3 {
			t.Fatalf("len(Parts()) = %d, want 3", len(parts))
		}
		if parts[0].Text != "Hello " || parts[0].Expr != nil {
			t.Errorf("Parts()[0] = %+v, want Text %q", parts[0], "Hello ")
		}
		if ident, ok := parts[1].Expr.(*ast.IdentExpr); !ok || ident.Name() != "NAME" {
			t.Errorf("Parts()[1].Expr = %T, want *ast.IdentExpr NAME", parts[1].Expr)
		}
		if parts[2].Text != "!" || parts[2].Expr != nil {
			t.Errorf("Parts()[2] = %+v, want Text %q", parts[2], "!")
		}
	})
}

// ── type declarations ──

func TestTypeDecl(t *testing.T) {
	// Port of TestTypeDeclarations.test_type_decl.
	prog := parseOK(t, "type SearchResult = { string content; float score; }")
	decls := prog.TypeDecls()
	if len(decls) != 1 {
		t.Fatalf("len(TypeDecls()) = %d, want 1", len(decls))
	}
	if decls[0].Name() != "SearchResult" {
		t.Errorf("Name() = %q, want %q", decls[0].Name(), "SearchResult")
	}
	fields := decls[0].Fields()
	if len(fields) != 2 {
		t.Fatalf("len(Fields()) = %d, want 2", len(fields))
	}
	if fields[0].Name() != "content" || fields[1].Name() != "score" {
		t.Errorf("field names = [%q %q], want [content score]", fields[0].Name(), fields[1].Name())
	}
	if prim, ok := fields[0].TypeRef().(*ast.PrimTypeRef); !ok || prim.Name() != "string" {
		t.Errorf("Fields()[0].TypeRef() = %T, want *ast.PrimTypeRef string", fields[0].TypeRef())
	}
}

// ── enum declarations ──

func TestEnumDecl(t *testing.T) {
	// Port of test_enum_declaration_roundtrips_canonically: trailing comma
	// is allowed and not duplicated.
	prog := parseOK(t, "enum TravelMode { air, rail, };")
	decls := prog.EnumDecls()
	if len(decls) != 1 {
		t.Fatalf("len(EnumDecls()) = %d, want 1", len(decls))
	}
	if decls[0].Name() != "TravelMode" {
		t.Errorf("Name() = %q, want %q", decls[0].Name(), "TravelMode")
	}
	members := decls[0].Members()
	if len(members) != 2 {
		t.Fatalf("len(Members()) = %d, want 2", len(members))
	}
	if members[0].Name() != "air" || members[1].Name() != "rail" {
		t.Errorf("member names = [%q %q], want [air rail]", members[0].Name(), members[1].Name())
	}
}

func TestEnumDeclRequiresMember(t *testing.T) {
	// parser.py: an empty enum body "{}" is rejected.
	_, errs := New(lex("enum Empty { };")).Parse()
	if len(errs) == 0 {
		t.Fatal("expected an error for an empty enum")
	}
	if !strings.Contains(errs[0].Message, "Enum declaration requires at least one member") {
		t.Errorf("Message = %q, want the empty-enum message", errs[0].Message)
	}
}

// ── code functions ──

func TestCodeFunction(t *testing.T) {
	// Port of test_code_function_and_foreign_code_are_distinct (code part):
	// a `code` declaration lands in CodeFunctions with name/params/return
	// type; a plain `function main` is the entry, not a code function.
	prog := parseOK(t, "code normalize(string value) -> string { return value; }\nfunction main() {}")
	cfs := prog.CodeFunctions()
	if len(cfs) != 1 {
		t.Fatalf("len(CodeFunctions()) = %d, want 1", len(cfs))
	}
	cf := cfs[0]
	// CodeFunctions() is typed []*ast.CodeFunctionDef; assert the node is
	// not a plain *FunctionDef so the "distinct" property is explicit.
	if _, plain := interface{}(cf).(*ast.FunctionDef); plain {
		t.Error("code function reported as a plain *FunctionDef")
	}
	if cf.Name() != "normalize" {
		t.Errorf("Name() = %q, want %q", cf.Name(), "normalize")
	}
	if len(cf.Params()) != 1 {
		t.Fatalf("len(Params()) = %d, want 1", len(cf.Params()))
	}
	p0 := cf.Params()[0]
	if p0.Name() != "value" {
		t.Errorf("Params()[0].Name() = %q, want value", p0.Name())
	}
	if prim, ok := p0.TypeRef().(*ast.PrimTypeRef); !ok || prim.Name() != "string" {
		t.Errorf("Params()[0].TypeRef() = %T, want *ast.PrimTypeRef string", p0.TypeRef())
	}
	if rt, ok := cf.ReturnType().(*ast.PrimTypeRef); !ok || rt.Name() != "string" {
		t.Errorf("ReturnType() = %T, want *ast.PrimTypeRef string", cf.ReturnType())
	}
	if prog.Entry() == nil || prog.Entry().Name() != "main" {
		t.Errorf("Entry() = %v, want function main", prog.Entry())
	}
	if len(prog.Functions()) != 0 {
		t.Errorf("len(Functions()) = %d, want 0", len(prog.Functions()))
	}
}

// ── type references ──

func TestTypeRefPrimitives(t *testing.T) {
	// Port of test_type_ref_primitives: keyword primitives become
	// PrimTypeRef; file becomes FileTypeRef (Name() stays "file").
	cases := []struct {
		name   string
		isFile bool
	}{
		{"string", false}, {"int", false}, {"float", false}, {"bool", false},
		{"file", true}, {"void", false}, {"any", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog := parseOK(t, fmt.Sprintf("function main(%s x) {}", tc.name))
			ref := prog.Entry().Params()[0].TypeRef()
			if tc.isFile {
				if _, ok := ref.(*ast.FileTypeRef); !ok {
					t.Fatalf("type ref = %T, want *ast.FileTypeRef", ref)
				}
			} else if prim, ok := ref.(*ast.PrimTypeRef); !ok || prim.Name() != tc.name {
				t.Fatalf("type ref = %T (name %v), want *ast.PrimTypeRef %q", ref, ref, tc.name)
			}
		})
	}
}

func TestTypeRefArray(t *testing.T) {
	// Port of test_type_ref_array: string[] -> ArrayTypeRef(Prim string).
	prog := parseOK(t, "function main(string[] tags) {}")
	ref := prog.Entry().Params()[0].TypeRef()
	arr, ok := ref.(*ast.ArrayTypeRef)
	if !ok {
		t.Fatalf("type ref = %T, want *ast.ArrayTypeRef", ref)
	}
	if prim, ok := arr.Element().(*ast.PrimTypeRef); !ok || prim.Name() != "string" {
		t.Errorf("Element() = %T, want *ast.PrimTypeRef string", arr.Element())
	}
}

func TestTypeRefArrayGeneric(t *testing.T) {
	// Port of test_type_ref_array_generic: array<int>.
	prog := parseOK(t, "function main(array<int> nums) {}")
	arr, ok := prog.Entry().Params()[0].TypeRef().(*ast.ArrayTypeRef)
	if !ok {
		t.Fatalf("type ref = %T, want *ast.ArrayTypeRef", prog.Entry().Params()[0].TypeRef())
	}
	if prim, ok := arr.Element().(*ast.PrimTypeRef); !ok || prim.Name() != "int" {
		t.Errorf("Element() = %T, want *ast.PrimTypeRef int", arr.Element())
	}
}

func TestTypeRefMap(t *testing.T) {
	// Port of test_type_ref_map: map<string, any>.
	prog := parseOK(t, "function main(map<string, any> config) {}")
	m, ok := prog.Entry().Params()[0].TypeRef().(*ast.MapTypeRef)
	if !ok {
		t.Fatalf("type ref = %T, want *ast.MapTypeRef", prog.Entry().Params()[0].TypeRef())
	}
	if prim, ok := m.Key().(*ast.PrimTypeRef); !ok || prim.Name() != "string" {
		t.Errorf("Key() = %T, want *ast.PrimTypeRef string", m.Key())
	}
	if prim, ok := m.Value().(*ast.PrimTypeRef); !ok || prim.Name() != "any" {
		t.Errorf("Value() = %T, want *ast.PrimTypeRef any", m.Value())
	}
}

func TestTypeRefNamed(t *testing.T) {
	// Port of test_type_ref_named.
	prog := parseOK(t, "function main(SearchResult result) {}")
	ref, ok := prog.Entry().Params()[0].TypeRef().(*ast.NamedTypeRef)
	if !ok {
		t.Fatalf("type ref = %T, want *ast.NamedTypeRef", prog.Entry().Params()[0].TypeRef())
	}
	if ref.Name() != "SearchResult" {
		t.Errorf("Name() = %q, want %q", ref.Name(), "SearchResult")
	}
}

func TestTypeRefNamedArray(t *testing.T) {
	// Port of test_type_ref_named_array: SearchResult[].
	prog := parseOK(t, "function main(SearchResult[] results) {}")
	arr, ok := prog.Entry().Params()[0].TypeRef().(*ast.ArrayTypeRef)
	if !ok {
		t.Fatalf("type ref = %T, want *ast.ArrayTypeRef", prog.Entry().Params()[0].TypeRef())
	}
	named, ok := arr.Element().(*ast.NamedTypeRef)
	if !ok {
		t.Fatalf("Element() = %T, want *ast.NamedTypeRef", arr.Element())
	}
	if named.Name() != "SearchResult" {
		t.Errorf("Element().Name() = %q, want %q", named.Name(), "SearchResult")
	}
}

func TestTypeRefOptional(t *testing.T) {
	// string? -> OptionalTypeRef(Prim string).
	prog := parseOK(t, "function main(string? maybe) {}")
	opt, ok := prog.Entry().Params()[0].TypeRef().(*ast.OptionalTypeRef)
	if !ok {
		t.Fatalf("type ref = %T, want *ast.OptionalTypeRef", prog.Entry().Params()[0].TypeRef())
	}
	if prim, ok := opt.Value().(*ast.PrimTypeRef); !ok || prim.Name() != "string" {
		t.Errorf("Value() = %T, want *ast.PrimTypeRef string", opt.Value())
	}
}

func TestTypeRefNamedOptional(t *testing.T) {
	// User? -> OptionalTypeRef(NamedTypeRef User) (parser.py suffix loop).
	prog := parseOK(t, "function main(User? selected) {}")
	opt, ok := prog.Entry().Params()[0].TypeRef().(*ast.OptionalTypeRef)
	if !ok {
		t.Fatalf("type ref = %T, want *ast.OptionalTypeRef", prog.Entry().Params()[0].TypeRef())
	}
	if named, ok := opt.Value().(*ast.NamedTypeRef); !ok || named.Name() != "User" {
		t.Errorf("Value() = %T, want *ast.NamedTypeRef User", opt.Value())
	}
}

func TestTypeRefFileConstraints(t *testing.T) {
	// Port of test_file_constraints_are_part_of_the_type_ref.
	prog := parseOK(t, "function main(file<document; .pdf; local_file> doc) { return doc; }")
	ref, ok := prog.Entry().Params()[0].TypeRef().(*ast.FileTypeRef)
	if !ok {
		t.Fatalf("type ref = %T, want *ast.FileTypeRef", prog.Entry().Params()[0].TypeRef())
	}
	c := ref.Constraints()
	if len(c.Types) != 1 || c.Types[0] != "document" {
		t.Errorf("Types = %v, want [document]", c.Types)
	}
	if len(c.Extensions) != 1 || c.Extensions[0] != ".pdf" {
		t.Errorf("Extensions = %v, want [.pdf]", c.Extensions)
	}
	if len(c.UploadMethods) != 1 || c.UploadMethods[0] != "local_file" {
		t.Errorf("UploadMethods = %v, want [local_file]", c.UploadMethods)
	}
}

func TestTypeRefFileArrayKeepsConstraints(t *testing.T) {
	// Port of test_file_array_keeps_file_constraints: the array suffix wraps
	// the constrained file type, keeping its constraints.
	prog := parseOK(t, "function main(file<document; .pdf>[] docs) { return docs; }")
	arr, ok := prog.Entry().Params()[0].TypeRef().(*ast.ArrayTypeRef)
	if !ok {
		t.Fatalf("type ref = %T, want *ast.ArrayTypeRef", prog.Entry().Params()[0].TypeRef())
	}
	file, ok := arr.Element().(*ast.FileTypeRef)
	if !ok {
		t.Fatalf("Element() = %T, want *ast.FileTypeRef", arr.Element())
	}
	if exts := file.Constraints().Extensions; len(exts) != 1 || exts[0] != ".pdf" {
		t.Errorf("Extensions = %v, want [.pdf]", exts)
	}
}

// ── parameters ──

func TestParamDecl(t *testing.T) {
	// Port of TestInputOutput cases.

	t.Run("single-string", func(t *testing.T) {
		prog := parseOK(t, "function main(string query) {}")
		params := prog.Entry().Params()
		if len(params) != 1 {
			t.Fatalf("len(Params()) = %d, want 1", len(params))
		}
		if params[0].Name() != "query" {
			t.Errorf("Name() = %q, want query", params[0].Name())
		}
		if prim, ok := params[0].TypeRef().(*ast.PrimTypeRef); !ok || prim.Name() != "string" {
			t.Errorf("TypeRef() = %T, want *ast.PrimTypeRef string", params[0].TypeRef())
		}
	})

	t.Run("with-default", func(t *testing.T) {
		prog := parseOK(t, `function main(string prompt = "hello") {}`)
		p := prog.Entry().Params()[0]
		if p.Default() == nil {
			t.Fatal("Default() = nil, want a StringLit")
		}
		if v, ok := p.Default().(*ast.StringLit); !ok || v.Value() != "hello" {
			t.Errorf("Default() = %T (%v), want *ast.StringLit \"hello\"", p.Default(), p.Default())
		}
	})

	t.Run("multiple", func(t *testing.T) {
		prog := parseOK(t, "function main(string a, int b, bool c) {}")
		if got := len(prog.Entry().Params()); got != 3 {
			t.Fatalf("len(Params()) = %d, want 3", got)
		}
	})

	t.Run("default-is-not-required", func(t *testing.T) {
		// Port of test_function_with_default_param.
		prog := parseOK(t, `function f(string x = "default") { }`)
		if prog.Functions()[0].Params()[0].Default() == nil {
			t.Error("Default() = nil, want non-nil for a defaulted parameter")
		}
	})
}

// ── function definitions and entry ──

func TestFunctionDef(t *testing.T) {
	t.Run("no-params", func(t *testing.T) {
		// Port of test_function_no_params.
		prog := parseOK(t, `function greet() { return "hello"; }`)
		fns := prog.Functions()
		if len(fns) != 1 {
			t.Fatalf("len(Functions()) = %d, want 1", len(fns))
		}
		if fns[0].Name() != "greet" {
			t.Errorf("Name() = %q, want greet", fns[0].Name())
		}
		if len(fns[0].Params()) != 0 {
			t.Errorf("len(Params()) = %d, want 0", len(fns[0].Params()))
		}
		if fns[0].ReturnType() != nil {
			t.Errorf("ReturnType() = %v, want nil", fns[0].ReturnType())
		}
	})

	t.Run("with-params-and-return", func(t *testing.T) {
		// Port of test_function_with_params.
		prog := parseOK(t, "function add(int a, int b) -> int { return a + b; }")
		fn := prog.Functions()[0]
		if fn.Name() != "add" {
			t.Errorf("Name() = %q, want add", fn.Name())
		}
		if len(fn.Params()) != 2 {
			t.Fatalf("len(Params()) = %d, want 2", len(fn.Params()))
		}
		if rt, ok := fn.ReturnType().(*ast.PrimTypeRef); !ok || rt.Name() != "int" {
			t.Errorf("ReturnType() = %T, want *ast.PrimTypeRef int", fn.ReturnType())
		}
	})
}

func TestEntryBlock(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		// Port of test_entry_basic.
		prog := parseOK(t, "function main(string query) { }")
		if prog.Entry() == nil {
			t.Fatal("Entry() = nil, want function main")
		}
		if prog.Entry().Name() != "main" {
			t.Errorf("Entry().Name() = %q, want main", prog.Entry().Name())
		}
		if len(prog.Entry().Params()) != 1 {
			t.Errorf("len(Entry().Params()) = %d, want 1", len(prog.Entry().Params()))
		}
	})

	t.Run("main-is-entry-not-function", func(t *testing.T) {
		// Port of test_function_main_is_entry.
		prog := parseOK(t, "function main(string x) { }")
		if prog.Entry() == nil || prog.Entry().Name() != "main" {
			t.Fatalf("Entry() = %v, want function main", prog.Entry())
		}
		if len(prog.Functions()) != 0 {
			t.Errorf("len(Functions()) = %d, want 0 (main is entry)", len(prog.Functions()))
		}
	})
}

func TestMultipleEntryBlocksError(t *testing.T) {
	// Port of test_double_entry: a second `function main` raises "Multiple
	// entry blocks defined".
	_, errs := New(lex("function main() { } function main() { }")).Parse()
	if len(errs) == 0 {
		t.Fatal("expected an error for a duplicate entry block")
	}
	found := false
	for _, e := range errs {
		msg := strings.ToLower(e.Message)
		if strings.Contains(msg, "entry") || strings.Contains(msg, "multiple") {
			found = true
		}
	}
	if !found {
		t.Errorf("errors = %v, want one naming the multiple-entry problem", errs)
	}
}

// ── @conversation ──

func TestConversationDecl(t *testing.T) {
	// Port of TestConversationDecl.test_valid_typed_literal_default.
	prog := parseOK(t, "@mode advanced-chat\n@conversation string status = \"new\";\nfunction main() {}")
	decls := prog.ConversationDecls()
	if len(decls) != 1 {
		t.Fatalf("len(ConversationDecls()) = %d, want 1", len(decls))
	}
	d := decls[0]
	if d.Name() != "status" {
		t.Errorf("Name() = %q, want status", d.Name())
	}
	if prim, ok := d.TypeRef().(*ast.PrimTypeRef); !ok || prim.Name() != "string" {
		t.Errorf("TypeRef() = %T, want *ast.PrimTypeRef string", d.TypeRef())
	}
	if v, ok := d.Default().(*ast.StringLit); !ok || v.Value() != "new" {
		t.Errorf("Default() = %T (%v), want *ast.StringLit \"new\"", d.Default(), d.Default())
	}
}

func TestConversationDeclOmittedDefault(t *testing.T) {
	// Port of TestConversationDecl.test_valid_omitted_default.
	prog := parseOK(t, "@mode advanced-chat\n@conversation string status;\nfunction main() {}")
	decls := prog.ConversationDecls()
	if len(decls) != 1 {
		t.Fatalf("len(ConversationDecls()) = %d, want 1", len(decls))
	}
	if decls[0].Name() != "status" {
		t.Errorf("Name() = %q, want status", decls[0].Name())
	}
	if decls[0].Default() != nil {
		t.Errorf("Default() = %v, want nil", decls[0].Default())
	}
}

func TestConversationDeclMissingSemicolon(t *testing.T) {
	// Port of TestConversationDecl.test_reject_missing_semicolon.
	_, errs := New(lex("@mode advanced-chat\n@conversation string status = \"new\"\nfunction main() {}")).Parse()
	if len(errs) == 0 {
		t.Fatal("expected an error for a missing ';' after a conversation declaration")
	}
	if !strings.Contains(errs[0].Message, "Expected ';' after conversation variable declaration") {
		t.Errorf("Message = %q, want the missing-semicolon message", errs[0].Message)
	}
}

// ── top-level recovery ──

func TestExpressionAtTopLevelError(t *testing.T) {
	// Port of test_expression_at_top_level: "42 + 1;" is not a declaration.
	_, errs := New(lex("42 + 1;")).Parse()
	if len(errs) == 0 {
		t.Fatal("expected an error for an expression at top level")
	}
	if !strings.Contains(errs[0].Message, "Expected declaration") {
		t.Errorf("Message = %q, want it to name the missing declaration", errs[0].Message)
	}
}

func TestRecoversAfterTopLevelError(t *testing.T) {
	// Port of test_recovers_after_error: an invalid top-level line is
	// synchronized away and later declarations still parse.
	src := "input ;\nfunction greet() { return \"hello\"; }\nfunction main() { }"
	prog, errs := New(lex(src)).Parse()
	if len(errs) == 0 {
		t.Fatal("expected at least one error for the invalid top-level line")
	}
	if len(prog.Functions()) != 1 {
		t.Errorf("len(Functions()) = %d, want 1 (greet)", len(prog.Functions()))
	}
	if prog.Functions()[0].Name() != "greet" {
		t.Errorf("Functions()[0].Name() = %q, want greet", prog.Functions()[0].Name())
	}
	if prog.Entry() == nil || prog.Entry().Name() != "main" {
		t.Errorf("Entry() = %v, want function main", prog.Entry())
	}
}

func TestMissingTypeNameError(t *testing.T) {
	// A parameter that does not start with a type records the type-ref
	// error; recovery skips the rest of the declaration.
	_, errs := New(lex("function main(123 x) {}")).Parse()
	if len(errs) == 0 {
		t.Fatal("expected an error for a missing type name in a parameter")
	}
	if !strings.Contains(errs[0].Message, "Expected type name") {
		t.Errorf("Message = %q, want the missing-type-name message", errs[0].Message)
	}
}

func TestMissingParamNameError(t *testing.T) {
	// Port of test_missing_param_name: "function f(string) { }" — the type
	// parses as a NamedTypeRef, then the expected parameter name is missing.
	_, errs := New(lex("function f(string) { }")).Parse()
	if len(errs) == 0 {
		t.Fatal("expected an error for a missing parameter name")
	}
	if !strings.Contains(errs[0].Message, "Expected parameter name") {
		t.Errorf("Message = %q, want the missing-parameter-name message", errs[0].Message)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ── statements (Task 4) ──
// ─────────────────────────────────────────────────────────────────────────────

// entryBody returns the statement list of the program's entry function body.
func entryBody(t *testing.T, src string) []ast.Stmt {
	t.Helper()
	prog := parseOK(t, src)
	if prog.Entry() == nil {
		t.Fatalf("parse %q: no entry function", src)
	}
	return prog.Entry().Body().Stmts()
}

func TestStatementIfSimple(t *testing.T) {
	// Port of test_if_simple.
	stmts := entryBody(t, "function main(string query) { if (true) { } }")
	if len(stmts) != 1 {
		t.Fatalf("len(stmts) = %d, want 1", len(stmts))
	}
	s, ok := stmts[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("stmt = %T, want *ast.IfStmt", stmts[0])
	}
	if _, ok := s.Condition().(*ast.BoolLit); !ok {
		t.Errorf("Condition() = %T, want *ast.BoolLit", s.Condition())
	}
	if s.ElseBody() != nil {
		t.Errorf("ElseBody() = %v, want nil", s.ElseBody())
	}
}

func TestStatementIfElse(t *testing.T) {
	// Port of test_if_else.
	stmts := entryBody(t, "function main(string query) { if (true) { } else { } }")
	s := stmts[0].(*ast.IfStmt)
	if s.ElseBody() == nil {
		t.Fatal("ElseBody() = nil, want non-nil")
	}
}

func TestStatementIfElseIf(t *testing.T) {
	// Port of test_if_else_if: else-if chain expands into ElseIfBranch list.
	stmts := entryBody(t, "function main(string query) { if (true) { } else if (false) { } else { } }")
	s := stmts[0].(*ast.IfStmt)
	if len(s.ElseIfs()) != 1 {
		t.Fatalf("len(ElseIfs()) = %d, want 1", len(s.ElseIfs()))
	}
	if _, ok := s.ElseIfs()[0].Condition().(*ast.BoolLit); !ok {
		t.Errorf("ElseIfs()[0].Condition() = %T, want *ast.BoolLit", s.ElseIfs()[0].Condition())
	}
	if s.ElseBody() == nil {
		t.Error("ElseBody() = nil, want non-nil")
	}
}

func TestStatementIfElseIfChain(t *testing.T) {
	// Else-if chain with two branches; the else-if bodies nest left to right.
	stmts := entryBody(t, "function main() { if (true) { } else if (a) { } else if (b) { } }")
	s := stmts[0].(*ast.IfStmt)
	if len(s.ElseIfs()) != 2 {
		t.Fatalf("len(ElseIfs()) = %d, want 2", len(s.ElseIfs()))
	}
	if _, ok := s.ElseIfs()[1].Condition().(*ast.IdentExpr); !ok {
		t.Errorf("ElseIfs()[1].Condition() = %T, want *ast.IdentExpr", s.ElseIfs()[1].Condition())
	}
	if s.ElseBody() != nil {
		t.Error("ElseBody() != nil, want nil")
	}
}

func TestStatementForLoop(t *testing.T) {
	// Port of test_for_loop: for (var item in items) { }.
	stmts := entryBody(t, "function main(string query) { for (var item in items) { } }")
	s := stmts[0].(*ast.ForStmt)
	if s.VarName() != "item" {
		t.Errorf("VarName() = %q, want item", s.VarName())
	}
	if s.VarType() != nil {
		t.Errorf("VarType() = %v, want nil (var)", s.VarType())
	}
	if _, ok := s.Iterable().(*ast.IdentExpr); !ok {
		t.Errorf("Iterable() = %T, want *ast.IdentExpr", s.Iterable())
	}
}

func TestStatementForWithType(t *testing.T) {
	// Port of test_for_with_type: for (string s in list) { }.
	stmts := entryBody(t, "function main(string query) { for (string s in list) { } }")
	s := stmts[0].(*ast.ForStmt)
	if s.VarName() != "s" {
		t.Errorf("VarName() = %q, want s", s.VarName())
	}
	rt, ok := s.VarType().(*ast.PrimTypeRef)
	if !ok || rt.Name() != "string" {
		t.Errorf("VarType() = %T %v, want PrimTypeRef string", s.VarType(), s.VarType())
	}
}

func TestStatementWhileLoop(t *testing.T) {
	// Port of test_while_loop: while (true) limit 3 { }.
	stmts := entryBody(t, "function main(string query) { while (true) limit 3 { } }")
	s := stmts[0].(*ast.WhileStmt)
	if s.Limit() != 3 {
		t.Errorf("Limit() = %d, want 3", s.Limit())
	}
	if _, ok := s.Condition().(*ast.BoolLit); !ok {
		t.Errorf("Condition() = %T, want *ast.BoolLit", s.Condition())
	}
}

func TestStatementWhileMissingLimitError(t *testing.T) {
	// while requires the static limit clause: while (cond) limit <int>.
	_, errs := New(lex("function f() { while (true) { } }")).Parse()
	if len(errs) == 0 {
		t.Fatal("expected an error for a missing while limit")
	}
	if !strings.Contains(errs[0].Message, "Expected static 'limit'") {
		t.Errorf("Message = %q, want the missing-limit message", errs[0].Message)
	}
}

func TestStatementReturnValue(t *testing.T) {
	// Port of test_return_value: return 42; inside -> int function.
	prog := parseOK(t, "function f() -> int { return 42; }")
	stmts := prog.Functions()[0].Body().Stmts()
	s := stmts[0].(*ast.ReturnStmt)
	if _, ok := s.Value().(*ast.IntLit); !ok {
		t.Errorf("Value() = %T, want *ast.IntLit", s.Value())
	}
}

func TestStatementReturnVoid(t *testing.T) {
	// Port of test_return_void: return; has no value.
	prog := parseOK(t, "function f() { return; }")
	stmts := prog.Functions()[0].Body().Stmts()
	s := stmts[0].(*ast.ReturnStmt)
	if s.Value() != nil {
		t.Errorf("Value() = %v, want nil", s.Value())
	}
}

func TestStatementOutput(t *testing.T) {
	// Port of test_output_stmt: output(x) — intermediate message.
	stmts := entryBody(t, `function main(string query) { output(query); }`)
	s := stmts[0].(*ast.OutputStmt)
	if _, ok := s.Value().(*ast.IdentExpr); !ok {
		t.Errorf("Value() = %T, want *ast.IdentExpr", s.Value())
	}
}

func TestStatementExprStmt(t *testing.T) {
	// Port of test_expr_stmt: print("hello"); is an expression statement.
	stmts := entryBody(t, `function main(string query) { print("hello"); }`)
	s := stmts[0].(*ast.ExprStmt)
	call, ok := s.Expr().(*ast.CallExpr)
	if !ok {
		t.Fatalf("Expr() = %T, want *ast.CallExpr", s.Expr())
	}
	if len(call.Args()) != 1 {
		t.Errorf("len(Args()) = %d, want 1", len(call.Args()))
	}
}

func TestStatementAssignment(t *testing.T) {
	// Port of test_assignment: var x = 0; x = 42;
	stmts := entryBody(t, "function main(string query) { var x = 0; x = 42; }")
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2", len(stmts))
	}
	s := stmts[1].(*ast.AssignStmt)
	if s.Operation() != "set" {
		t.Errorf("Operation() = %q, want set", s.Operation())
	}
	if _, ok := s.Target().(*ast.IdentExpr); !ok {
		t.Errorf("Target() = %T, want *ast.IdentExpr", s.Target())
	}
	if _, ok := s.Value().(*ast.IntLit); !ok {
		t.Errorf("Value() = %T, want *ast.IntLit", s.Value())
	}
}

func TestStatementCompoundAssignment(t *testing.T) {
	// Port of test_compound_assignment: x += 1 desugars value to x + 1.
	stmts := entryBody(t, "function main(string query) { var x = 0; x += 1; }")
	s := stmts[1].(*ast.AssignStmt)
	if s.Operation() != "+=" {
		t.Errorf("Operation() = %q, want +=", s.Operation())
	}
	b, ok := s.Value().(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("Value() = %T, want *ast.BinaryExpr", s.Value())
	}
	if b.Op() != "+" {
		t.Errorf("BinaryExpr.Op() = %q, want +", b.Op())
	}
}

func TestStatementBreakContinue(t *testing.T) {
	// Port of test_break_continue: break; continue; inside a while body.
	stmts := entryBody(t, "function main(string query) { while (true) limit 3 { break; continue; } }")
	w := stmts[0].(*ast.WhileStmt)
	if _, ok := w.Body().Stmts()[0].(*ast.BreakStmt); !ok {
		t.Errorf("body[0] = %T, want *ast.BreakStmt", w.Body().Stmts()[0])
	}
	if _, ok := w.Body().Stmts()[1].(*ast.ContinueStmt); !ok {
		t.Errorf("body[1] = %T, want *ast.ContinueStmt", w.Body().Stmts()[1])
	}
}

func TestStatementParallel(t *testing.T) {
	// Port of test_parallel: parallel { { a(); } { b(); } } has two branches.
	stmts := entryBody(t, "function main(string query) { parallel { { a(); } { b(); } } }")
	s := stmts[0].(*ast.ParallelStmt)
	if len(s.Branches()) != 2 {
		t.Fatalf("len(Branches()) = %d, want 2", len(s.Branches()))
	}
	first := s.Branches()[0].Stmts()[0]
	if _, ok := first.(*ast.ExprStmt); !ok {
		t.Errorf("branch[0][0] = %T, want *ast.ExprStmt", first)
	}
}

func TestVarDeclTyped(t *testing.T) {
	// Port of test_function_var_decl: string x = "hello";.
	prog := parseOK(t, `function f() { string x = "hello"; }`)
	s := prog.Functions()[0].Body().Stmts()[0].(*ast.VarDeclStmt)
	if s.Name() != "x" {
		t.Errorf("Name() = %q, want x", s.Name())
	}
	rt, ok := s.TypeRef().(*ast.PrimTypeRef)
	if !ok || rt.Name() != "string" {
		t.Errorf("TypeRef() = %T %v, want PrimTypeRef string", s.TypeRef(), s.TypeRef())
	}
	if !s.IsMutable() {
		t.Error("IsMutable() = false, want true (typed decls default to mutable)")
	}
}

func TestVarDeclInferred(t *testing.T) {
	// Port of test_function_var_inferred: var x = 42; has no type ref.
	prog := parseOK(t, "function f() { var x = 42; }")
	s := prog.Functions()[0].Body().Stmts()[0].(*ast.VarDeclStmt)
	if s.TypeRef() != nil {
		t.Errorf("TypeRef() = %v, want nil", s.TypeRef())
	}
	if _, ok := s.Init().(*ast.IntLit); !ok {
		t.Errorf("Init() = %T, want *ast.IntLit", s.Init())
	}
	if !s.IsMutable() {
		t.Error("IsMutable() = false, want true for var")
	}
}

func TestVarDeclLetImmutable(t *testing.T) {
	prog := parseOK(t, "function f() { let x = 42; }")
	s := prog.Functions()[0].Body().Stmts()[0].(*ast.VarDeclStmt)
	if s.IsMutable() {
		t.Error("IsMutable() = true, want false for let")
	}
	if s.Init() == nil {
		t.Error("Init() = nil, want non-nil")
	}
}

func TestVarDeclNamedType(t *testing.T) {
	// Identifier-led typed declaration: User u = ...; (named type prefix).
	prog := parseOK(t, `function f() { User u = "x"; }`)
	s := prog.Functions()[0].Body().Stmts()[0].(*ast.VarDeclStmt)
	if s.Name() != "u" {
		t.Errorf("Name() = %q, want u", s.Name())
	}
	if _, ok := s.TypeRef().(*ast.NamedTypeRef); !ok {
		t.Errorf("TypeRef() = %T, want *ast.NamedTypeRef", s.TypeRef())
	}
}

func TestEmptyBlock(t *testing.T) {
	// Empty block yields an empty statements list (Python: stmts=[]).
	prog := parseOK(t, "function f() { }")
	if n := len(prog.Functions()[0].Body().Stmts()); n != 0 {
		t.Errorf("len(stmts) = %d, want 0", n)
	}
}

func TestInvalidBodyProducesError(t *testing.T) {
	// The Task 3 placeholder silently accepted invalid bodies; Task 4 must
	// report them like Python.
	_, errs := New(lex("function f() { 42 43 }")).Parse()
	if len(errs) == 0 {
		t.Fatal("expected an error for an invalid statement body")
	}
	if !strings.Contains(errs[0].Message, "Expected ';' after expression") {
		t.Errorf("Message = %q, want the missing-semicolon message", errs[0].Message)
	}
}

func TestStatementRecoveryInsideBlock(t *testing.T) {
	// A bad statement is synchronized away; later statements still parse.
	prog, errs := New(lex("function main() { !!! ; return 1; }")).Parse()
	if len(errs) == 0 {
		t.Fatal("expected an error for the invalid statement")
	}
	if prog.Entry() == nil {
		t.Fatal("Entry() = nil")
	}
	stmts := prog.Entry().Body().Stmts()
	if len(stmts) != 1 {
		t.Fatalf("len(stmts) = %d, want 1 (recovered return)", len(stmts))
	}
	if _, ok := stmts[0].(*ast.ReturnStmt); !ok {
		t.Errorf("stmts[0] = %T, want *ast.ReturnStmt", stmts[0])
	}
}

func TestForeignCodeInInitializer(t *testing.T) {
	// Port of test_code_function_and_foreign_code_are_distinct: the let init
	// is a ForeignCodeExpr with typed inputs, direct output and a source.
	prog := parseOK(t, "code normalize(string value) -> string { return value; } "+
		"function main(string value) -> string { "+
		"let raw = foreign code python3(string input = value) -> string { "+
		"source `def main(input: str):\n    return {\"result\": input}`; "+
		"}; "+
		"return normalize(raw); }")
	stmts := prog.Entry().Body().Stmts()
	vd := stmts[0].(*ast.VarDeclStmt)
	fc, ok := vd.Init().(*ast.ForeignCodeExpr)
	if !ok {
		t.Fatalf("Init() = %T, want *ast.ForeignCodeExpr", vd.Init())
	}
	if fc.Language() != "python3" {
		t.Errorf("Language() = %q, want python3", fc.Language())
	}
	if len(fc.Inputs()) != 1 {
		t.Fatalf("len(Inputs()) = %d, want 1", len(fc.Inputs()))
	}
	if fc.Inputs()[0].Name() != "input" {
		t.Errorf("Inputs()[0].Name() = %q, want input", fc.Inputs()[0].Name())
	}
	if _, ok := fc.Output().(*ast.CodeDirectOutput); !ok {
		t.Errorf("Output() = %T, want *ast.CodeDirectOutput", fc.Output())
	}
	if fc.Source() == nil {
		t.Error("Source() = nil, want template source")
	}
	if len(prog.CodeFunctions()) != 1 {
		t.Errorf("len(CodeFunctions()) = %d, want 1", len(prog.CodeFunctions()))
	}
}

func TestForeignCodeJavascriptLanguagePassthrough(t *testing.T) {
	// 反编译器对 JS CodeRunner 生成 `foreign code javascript(...)`；
	// 语言必须透传进 AST（此前硬编码 python3，导致 JS 源码被误按 Python 扫描）。
	prog := parseOK(t, "function main() -> string { "+
		"let js = foreign code javascript(array<any> input = value) -> { content: string; } { "+
		"source `async function main({ params }: Args): Promise<Output> {\n"+
		"  const t = String(params.input);\n"+
		"  return { content: t };\n"+
		"}`; "+
		"}; "+
		"return js.content; }")
	stmts := prog.Entry().Body().Stmts()
	vd := stmts[0].(*ast.VarDeclStmt)
	fc, ok := vd.Init().(*ast.ForeignCodeExpr)
	if !ok {
		t.Fatalf("Init() = %T, want *ast.ForeignCodeExpr", vd.Init())
	}
	if fc.Language() != "javascript" {
		t.Errorf("Language() = %q, want javascript (语言透传)", fc.Language())
	}
}

func TestForExprWithYieldAndWhileOutput(t *testing.T) {
	// Port of test_yield_answer_optional_and_while_limit_parse:
	//   let values = for (item in items) { yield item; };
	//   while (prefix != null) limit 2 { output(prefix); break; }
	//   return values[0];
	prog := parseOK(t, "@mode advanced-chat\n"+
		"function main(string[] items, string? prefix) { "+
		"let values = for (item in items) { yield item; }; "+
		"while (prefix != null) limit 2 { output(prefix); break; } "+
		"return values[0]; }")
	stmts := prog.Entry().Body().Stmts()

	vd := stmts[0].(*ast.VarDeclStmt)
	fe, ok := vd.Init().(*ast.ForExpr)
	if !ok {
		t.Fatalf("Init() = %T, want *ast.ForExpr", vd.Init())
	}
	if fe.VarName() != "item" {
		t.Errorf("ForExpr.VarName() = %q, want item", fe.VarName())
	}
	if _, ok := fe.Body().Stmts()[0].(*ast.YieldStmt); !ok {
		t.Errorf("ForExpr body[0] = %T, want *ast.YieldStmt", fe.Body().Stmts()[0])
	}

	w := stmts[1].(*ast.WhileStmt)
	if w.Limit() != 2 {
		t.Errorf("WhileStmt.Limit() = %d, want 2", w.Limit())
	}
	if _, ok := w.Body().Stmts()[0].(*ast.OutputStmt); !ok {
		t.Errorf("WhileStmt body[0] = %T, want *ast.OutputStmt", w.Body().Stmts()[0])
	}
	if _, ok := w.Body().Stmts()[1].(*ast.BreakStmt); !ok {
		t.Errorf("WhileStmt body[1] = %T, want *ast.BreakStmt", w.Body().Stmts()[1])
	}

	ret := stmts[2].(*ast.ReturnStmt)
	if _, ok := ret.Value().(*ast.IndexExpr); !ok {
		t.Errorf("ReturnStmt.Value() = %T, want *ast.IndexExpr", ret.Value())
	}
}

func TestOperationPolicyOnCall(t *testing.T) {
	// with retry(max: 2, interval: 1s) postfix on a call expression.
	prog := parseOK(t, "function main(string q) -> string { "+
		"let r = llm(MODEL, { \"messages\": [ { \"role\": \"user\", \"content\": q } ] }) with retry(max: 2, interval: 1s); "+
		"return r.text; }")
	stmts := prog.Entry().Body().Stmts()
	vd := stmts[0].(*ast.VarDeclStmt)
	op, ok := vd.Init().(*ast.OperationPolicyExpr)
	if !ok {
		t.Fatalf("Init() = %T, want *ast.OperationPolicyExpr", vd.Init())
	}
	if len(op.Policies()) != 1 {
		t.Fatalf("len(Policies()) = %d, want 1", len(op.Policies()))
	}
	rp, ok := op.Policies()[0].(*ast.RetryPolicy)
	if !ok {
		t.Fatalf("Policies()[0] = %T, want *ast.RetryPolicy", op.Policies()[0])
	}
	if rp.MaxRetries() != 2 {
		t.Errorf("MaxRetries() = %d, want 2", rp.MaxRetries())
	}
	if rp.IntervalMs() != 1000 {
		t.Errorf("IntervalMs() = %d, want 1000 (1s)", rp.IntervalMs())
	}
	if _, ok := stmts[1].(*ast.ReturnStmt); !ok {
		t.Errorf("stmts[1] = %T, want *ast.ReturnStmt", stmts[1])
	}
}

func TestAttemptStmt(t *testing.T) {
	// attempt <expr> as <result> { success { ... } failure ( <err> ) { ... } }
	stmts := entryBody(t, "function main() { "+
		"attempt llm(MODEL, query) as result { "+
		"success { return result; } "+
		"failure (err) { return err; } "+
		"} }")
	s := stmts[0].(*ast.AttemptStmt)
	if _, ok := s.Operation().(*ast.CallExpr); !ok {
		t.Errorf("Operation() = %T, want *ast.CallExpr", s.Operation())
	}
	if s.ResultBinding().Name() != "result" {
		t.Errorf("ResultBinding().Name() = %q, want result", s.ResultBinding().Name())
	}
	if s.ResultBinding().IsMutable() {
		t.Error("ResultBinding().IsMutable() = true, want false")
	}
	if len(s.SuccessBody().Stmts()) != 1 {
		t.Errorf("len(SuccessBody().Stmts()) = %d, want 1", len(s.SuccessBody().Stmts()))
	}
	if s.ErrorBinding().Name() != "err" {
		t.Errorf("ErrorBinding().Name() = %q, want err", s.ErrorBinding().Name())
	}
	if len(s.FailureBody().Stmts()) != 1 {
		t.Errorf("len(FailureBody().Stmts()) = %d, want 1", len(s.FailureBody().Stmts()))
	}
}

func TestRequestInputStmt(t *testing.T) {
	// Port of test_request_input_parses_actions_and_timeout (shape).
	prog := parseOK(t, "enum ReviewAction { approve, reject } "+
		"type ReviewForm = { string comment; } "+
		"function main() { "+
		"request_input<ReviewForm, ReviewAction>({ "+
		"content: \"Review this request\", "+
		"timeout: { value: 3, unit: \"day\" } "+
		"}) as review { "+
		"action ReviewAction.approve { return review.comment; } "+
		"action ReviewAction.reject { return \"rejected\"; } "+
		"timeout { return \"timed out\"; } "+
		"} }")
	stmts := prog.Entry().Body().Stmts()
	ri := stmts[0].(*ast.RequestInputStmt)
	ft, ok := ri.FormType().(*ast.NamedTypeRef)
	if !ok || ft.Name() != "ReviewForm" {
		t.Errorf("FormType() = %T %v, want NamedTypeRef ReviewForm", ri.FormType(), ri.FormType())
	}
	at, ok := ri.ActionType().(*ast.NamedTypeRef)
	if !ok || at.Name() != "ReviewAction" {
		t.Errorf("ActionType() = %T %v, want NamedTypeRef ReviewAction", ri.ActionType(), ri.ActionType())
	}
	if ri.Config() == nil {
		t.Fatal("Config() = nil, want map literal")
	}
	if ri.ResultBinding().Name() != "review" {
		t.Errorf("ResultBinding().Name() = %q, want review", ri.ResultBinding().Name())
	}
	if len(ri.ActionBranches()) != 2 {
		t.Fatalf("len(ActionBranches()) = %d, want 2", len(ri.ActionBranches()))
	}
	ab, ok := ri.ActionBranches()[0].ActionExpr().(*ast.FieldAccessExpr)
	if !ok || ab.FieldName() != "approve" {
		t.Errorf("ActionBranches()[0].ActionExpr() = %T, want FieldAccessExpr .approve", ri.ActionBranches()[0].ActionExpr())
	}
	if _, ok := ri.ActionBranches()[0].Body().Stmts()[0].(*ast.ReturnStmt); !ok {
		t.Errorf("action branch[0] body[0] = %T, want *ast.ReturnStmt", ri.ActionBranches()[0].Body().Stmts()[0])
	}
	if len(ri.TimeoutBody().Stmts()) != 1 {
		t.Errorf("len(TimeoutBody().Stmts()) = %d, want 1", len(ri.TimeoutBody().Stmts()))
	}
}

func TestRequestInputMissingActionBranchError(t *testing.T) {
	// Human Input requires at least one action branch.
	_, errs := New(lex("function main() { request_input<Form, Action>({}) as r { timeout { } } }")).Parse()
	if len(errs) == 0 {
		t.Fatal("expected an error for missing action branches")
	}
	if !strings.Contains(errs[0].Message, "requires at least one action branch") {
		t.Errorf("Message = %q, want the action-branch message", errs[0].Message)
	}
}

func TestParallelStmtNamedBranches(t *testing.T) {
	// Statement-level named parallel branches: names are source labels
	// only; values are reached downstream through the transparent scope.
	prog := parseOK(t, "function main(string seed) -> string { "+
		"parallel { "+
		"left: { let r = f(seed); } "+
		"right: { let r2 = f(seed); } "+
		"} "+
		"return r; }")
	stmts := prog.Entry().Body().Stmts()
	ps, ok := stmts[0].(*ast.ParallelStmt)
	if !ok {
		t.Fatalf("stmts[0] = %T, want *ast.ParallelStmt", stmts[0])
	}
	if len(ps.Branches()) != 2 {
		t.Fatalf("len(Branches()) = %d, want 2", len(ps.Branches()))
	}
	names := ps.BranchNames()
	if names[0] != "left" || names[1] != "right" {
		t.Errorf("branch names = %q,%q want left,right", names[0], names[1])
	}
}

func TestParallelStmtUnnamedBranches(t *testing.T) {
	// Anonymous branches keep working (unnamed labels are "").
	prog := parseOK(t, "function main(string seed) -> string { "+
		"parallel { { let r = f(seed); } { let r2 = f(seed); } } "+
		"return r; }")
	ps := prog.Entry().Body().Stmts()[0].(*ast.ParallelStmt)
	if names := ps.BranchNames(); len(names) != 2 || names[0] != "" || names[1] != "" {
		t.Errorf("unnamed branch labels = %v, want [\"\" \"\"]", names)
	}
}

func TestParallelExprFormIsRejected(t *testing.T) {
	// The `let x = parallel {...}` value form was removed: parallel is a
	// statement (transparent barrier), not an expression.
	_, errs := New(lex("function main() { let x = parallel { { } }; }")).Parse()
	if len(errs) == 0 {
		t.Fatal("expected an error for the removed parallel expression form")
	}
}

func TestParallelStmtDuplicateBranchNameError(t *testing.T) {
	_, errs := New(lex("function main() { parallel { a: { } a: { } }; }")).Parse()
	if len(errs) == 0 {
		t.Fatal("expected an error for a duplicate parallel branch name")
	}
	if !strings.Contains(errs[0].Message, "Duplicate parallel branch name 'a'") {
		t.Errorf("Message = %q, want the duplicate-name message", errs[0].Message)
	}
}

func TestParallelStmtRequiresBranch(t *testing.T) {
	_, errs := New(lex("function main() { parallel { }; }")).Parse()
	if len(errs) == 0 {
		t.Fatal("expected an error for an empty parallel statement")
	}
	if !strings.Contains(errs[0].Message, "requires at least one branch") {
		t.Errorf("Message = %q, want the at-least-one-branch message", errs[0].Message)
	}
}

func TestLambdaInCallArgument(t *testing.T) {
	// Port of test_calculation_lambda_parses_as_expression_only:
	// flatMap(groups, (group) -> group) has a LambdaExpr second argument.
	prog := parseOK(t, "function main(string[][] groups) { let values = flatMap(groups, (group) -> group); }")
	stmts := prog.Entry().Body().Stmts()
	vd := stmts[0].(*ast.VarDeclStmt)
	call, ok := vd.Init().(*ast.CallExpr)
	if !ok {
		t.Fatalf("Init() = %T, want *ast.CallExpr", vd.Init())
	}
	lambda, ok := call.Args()[1].(*ast.LambdaExpr)
	if !ok {
		t.Fatalf("Args()[1] = %T, want *ast.LambdaExpr", call.Args()[1])
	}
	if len(lambda.Params()) != 1 || lambda.Params()[0] != "group" {
		t.Errorf("Params() = %v, want [group]", lambda.Params())
	}
}

func TestLambdaMultiParam(t *testing.T) {
	prog := parseOK(t, "function main() { let f = (a, b) -> a + b; }")
	vd := prog.Entry().Body().Stmts()[0].(*ast.VarDeclStmt)
	lambda, ok := vd.Init().(*ast.LambdaExpr)
	if !ok {
		t.Fatalf("Init() = %T, want *ast.LambdaExpr", vd.Init())
	}
	if len(lambda.Params()) != 2 || lambda.Params()[0] != "a" || lambda.Params()[1] != "b" {
		t.Errorf("Params() = %v, want [a b]", lambda.Params())
	}
	if _, ok := lambda.Body().(*ast.BinaryExpr); !ok {
		t.Errorf("Body() = %T, want *ast.BinaryExpr", lambda.Body())
	}
}
