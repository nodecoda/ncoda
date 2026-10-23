// Package parser implements the recursive-descent parser for the NodeCoda
// DSL, ported from src/nclang/lang/parser.py.
//
// This file holds the parser skeleton: the Parser struct, the token cursor
// utilities (advance/check/match/peek/previous/consume), and error
// collection with statement-boundary synchronization. Errors accumulate in
// a slice instead of aborting on the first one, mirroring the Python
// parser's raise-and-suppress recovery with an explicit error list.
// Declaration/statement/expression productions land in later tasks.
package parser

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/lexer"
	"github.com/nodecoda/ncoda/pkg/token"
)

// libraryVersionRe validates the MAJOR.MINOR.PATCH version of a library
// declaration. The lexer splits "0.1.0" into FLOAT("0.1") DOT INT("0"), so
// parseLibraryDecl re-joins the lexemes before matching here.
var libraryVersionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// Parser is a recursive-descent parser over a token stream. It collects
// multiple parse errors without aborting on the first one, mirroring
// parser.py:Parser. The zero value is not usable; construct with New.
type Parser struct {
	tokens  []token.Token
	current int
	errors  []*ParseError
	// Nesting-depth guards (P4-4): the Go parser has no interpreter
	// recursion limit, so pathologically nested input (thousands of parens
	// or nested blocks) would parse and build unbounded AST/stack — a DoS
	// vector that the Python reference stops via RecursionError
	// (→ ADAPTER_ERROR "maximum recursion depth exceeded"). Counters are
	// bumped at the single recursive entry points and trip a synthetic
	// ADAPTER_ERROR diagnostic at the same depths the reference rejects
	// (paren/expression ~88, statement blocks ~128; the reference's
	// threshold is implementation-dependent frame depth, recorded as an
	// honest boundary deviation in PARITY.md).
	exprDepth int
	stmtDepth int
	// grouped 记录直接来自括号分组 `( expr )` 的表达式子树（route 1a：
	// JS 式 ?? 与 ||/&& 混用限制需要区分 `a || b ?? c`（裸混用，语法错）
	// 与 `(a || b) ?? c`（括号分组，合法）——AST 折叠括号不保留节点，故
	// parser 侧标记。
	grouped map[ast.Expr]bool
}

// markGrouped 把括号分组返回的表达式子树标记为"来自显式括号"。
func (p *Parser) markGrouped(e ast.Expr) {
	if e == nil {
		return
	}
	if p.grouped == nil {
		p.grouped = map[ast.Expr]bool{}
	}
	p.grouped[e] = true
}

// fromGrouped 报告表达式是否直接来自括号分组。
func (p *Parser) fromGrouped(e ast.Expr) bool {
	if p.grouped == nil {
		return false
	}
	return p.grouped[e]
}

// New constructs a Parser over the given tokens. The token list should end
// with an EOF token (as produced by the lexer); an empty list is treated as
// a lone EOF so empty input parses to an empty Program instead of failing.
func New(tokens []token.Token) *Parser {
	return &Parser{tokens: tokens}
}

// Parse parses the token stream into a Program and returns all collected
// parse errors (nil when none). Full parsing lands in later tasks; for now
// it returns an empty Program (zero-value fields, mode defaulting to
// "workflow" exactly like Python's ProgramNode()) with no errors.
func (p *Parser) Parse() (*ast.Program, []*ParseError) {
	return p.parseProgram()
}

// parseProgram is the program production (parser.py:_parse_program):
// optional @language/@mode headers, @conversation declarations, then the
// top-level declaration loop (const/type/enum/code/function) with
// per-declaration error recovery and semicolon skipping. Declarations are
// collected into locals and assembled into the Program at the end: the ast
// Program fields are assigned through With* construction options, mirroring
// the Python attribute assignments inside _parse_program.
//
// Errors in the @language/@mode/@conversation sections escape parsing
// without recovery (Python raises them out of parse()); errors inside the
// declaration loop are recovered via synchronize, mirroring Python's
// try/except ParseError + _synchronize.
func (p *Parser) parseProgram() (*ast.Program, []*ParseError) {
	identity := ""
	mode := "workflow"
	var imports []*ast.ImportDecl
	var consts []*ast.ConstDecl
	var types []*ast.TypeDecl
	var enums []*ast.EnumDecl
	var codeFns []*ast.CodeFunctionDef
	var subflows []*ast.SubflowDecl
	var fns []*ast.FunctionDef
	var convs []*ast.ConversationVarDecl
	var entry *ast.FunctionDef

	p.skipSemicolons()

	// Optional @language declaration (parser.py: parse_language_decl
	// section): @language <identity> ;.
	if p.check(token.AT_LANGUAGE) {
		p.advance()
		identityTok, err := p.consume(token.IDENTIFIER, "Expected language identity after @language")
		if err != nil {
			return ast.NewProgram(), p.errors
		}
		identity = identityTok.Lexeme
		p.skipSemicolons()
	}

	// Optional @mode declaration (parser.py: parse_mode_decl section):
	// @mode workflow|advanced-chat ;.
	if p.check(token.AT_MODE) {
		p.advance()
		modeTok, err := p.consume(token.IDENTIFIER, "Expected mode name after @mode")
		if err != nil {
			return ast.NewProgram(ast.WithLanguageIdentity(identity)), p.errors
		}
		if modeTok.Lexeme != "workflow" && modeTok.Lexeme != "advanced-chat" && modeTok.Lexeme != "agent" {
			p.error(fmt.Sprintf("Unknown mode: %s. Expected 'workflow', 'advanced-chat' or 'agent'", modeTok.Lexeme))
			return ast.NewProgram(ast.WithLanguageIdentity(identity)), p.errors
		}
		mode = modeTok.Lexeme
		p.skipSemicolons()
	}

	// @conversation declarations (parser.py:_parse_conversation_decl).
	for p.check(token.AT_CONVERSATION) {
		decl, err := p.parseConversationDecl()
		if err != nil {
			return ast.NewProgram(ast.WithLanguageIdentity(identity), ast.WithMode(mode)), p.errors
		}
		convs = append(convs, decl)
		p.skipSemicolons()
	}

	// @agent declaration (@mode agent only): @agent { model: "...", ... }.
	// Block body is a map literal; semantic field extraction happens here
	// (parser-level validation for friendlier errors).
	var agentDecl *ast.AgentDecl
	var library *ast.LibraryDecl
	for p.check(token.AT_AGENT) {
		if agentDecl != nil {
			p.error("Duplicate @agent declaration: only one agent application per program")
			p.synchronize()
			continue
		}
		decl, err := p.parseAgentDecl()
		if err != nil {
			return ast.NewProgram(ast.WithLanguageIdentity(identity), ast.WithMode(mode)), p.errors
		}
		if mode != "agent" {
			p.error("@agent declaration requires @mode agent")
		}
		agentDecl = decl
		p.skipSemicolons()
	}

	// Top-level declaration loop with per-declaration recovery (Python's
	// try/except ParseError around each declaration).
	for !p.isAtEnd() {
		var declErr *ParseError
		switch p.peek().Type {
		case token.KW_LIBRARY:
			if library != nil {
				declErr = p.error("Duplicate library declaration")
				break
			}
			var d *ast.LibraryDecl
			if d, declErr = p.parseLibraryDecl(); declErr == nil {
				library = d
			}
		case token.KW_IMPORT:
			var d *ast.ImportDecl
			if d, declErr = p.parseImportDecl(); declErr == nil {
				for _, existing := range imports {
					if existing.Provider() == d.Provider() {
						declErr = p.error(fmt.Sprintf("Duplicate import %q", d.Provider()))
						break
					}
				}
				if declErr == nil {
					imports = append(imports, d)
				}
			}
		case token.KW_CONST:
			var d *ast.ConstDecl
			if d, declErr = p.parseConstDecl(); declErr == nil {
				consts = append(consts, d)
			}
		case token.KW_TYPE:
			var d *ast.TypeDecl
			if d, declErr = p.parseTypeDecl(); declErr == nil {
				types = append(types, d)
			}
		case token.KW_ENUM:
			var d *ast.EnumDecl
			if d, declErr = p.parseEnumDecl(); declErr == nil {
				enums = append(enums, d)
			}
		case token.KW_CODE:
			var d *ast.CodeFunctionDef
			if d, declErr = p.parseCodeFunctionDef(); declErr == nil {
				codeFns = append(codeFns, d)
			}
		case token.KW_SUBFLOW:
			var d *ast.SubflowDecl
			if d, declErr = p.parseSubflowDecl(); declErr == nil {
				for _, existing := range subflows {
					if existing.Name() == d.Name() {
						declErr = p.error(fmt.Sprintf("Duplicate subflow %q", d.Name()))
						break
					}
				}
				if declErr == nil {
					subflows = append(subflows, d)
				}
			}
		case token.KW_FUNCTION:
			var fn *ast.FunctionDef
			if fn, declErr = p.parseFunctionDef(); declErr == nil {
				if fn.Name() == "main" {
					if entry != nil {
						declErr = p.error("Multiple entry blocks defined")
					} else {
						entry = fn
					}
				} else {
					fns = append(fns, fn)
				}
			}
		default:
			declErr = p.error(fmt.Sprintf("Expected declaration, got %s", p.peek().Type))
		}
		if declErr != nil {
			if declErr.AdapterError {
				return ast.NewProgram(
					ast.WithLanguageIdentity(identity),
					ast.WithMode(mode),
					ast.WithImports(imports),
					ast.WithConstDecls(consts),
					ast.WithEnumDecls(enums),
					ast.WithTypeDecls(types),
				), []*ParseError{declErr}
			}
			p.synchronize()
		}
		p.skipSemicolons()
	}

	return ast.NewProgram(
		ast.WithLanguageIdentity(identity),
		ast.WithMode(mode),
		ast.WithImports(imports),
		ast.WithConstDecls(consts),
		ast.WithEnumDecls(enums),
		ast.WithTypeDecls(types),
		ast.WithConversationDecls(convs),
		ast.WithAgentDecl(agentDecl),
		ast.WithLibraryDecl(library),
		ast.WithCodeFunctions(codeFns),
		ast.WithSubflowDecls(subflows),
		ast.WithFunctions(fns),
		ast.WithEntry(entry),
	), p.errors
}

// Errors returns all parse errors collected so far (Python: parser.errors).
// The returned slice aliases the parser's internal storage; treat it as
// read-only.
func (p *Parser) Errors() []*ParseError { return p.errors }

// ── Cursor utilities (parser.py:_peek/_check_next/_previous/_is_at_end/
// _advance/_check/_match) ──

// peek returns the current token without consuming it.
func (p *Parser) peek() token.Token {
	if p.current >= len(p.tokens) {
		return token.Token{Type: token.EOF}
	}
	return p.tokens[p.current]
}

// checkNext reports whether the token after the current one has the given
// type, clamping at the end of the token list (Python: min(current+1, len-1)).
func (p *Parser) checkNext(t token.Type) bool {
	if len(p.tokens) == 0 {
		return false
	}
	n := p.current + 1
	if n >= len(p.tokens) {
		n = len(p.tokens) - 1
	}
	return p.tokens[n].Type == t
}

// previous returns the most recently consumed token. Before any token is
// consumed, Python's negative index returns the last token (the EOF token in
// practice); mirror that without a negative index.
func (p *Parser) previous() token.Token {
	if p.current == 0 {
		if len(p.tokens) > 0 {
			return p.tokens[len(p.tokens)-1]
		}
		return token.Token{Type: token.EOF}
	}
	return p.tokens[p.current-1]
}

// isAtEnd reports whether the parser has reached the end of input (EOF
// token or exhausted token list).
func (p *Parser) isAtEnd() bool {
	return p.current >= len(p.tokens) || p.tokens[p.current].Type == token.EOF
}

// advance consumes and returns the next token. It never moves past EOF and
// returns the previous token when already at the end (Python semantics).
func (p *Parser) advance() token.Token {
	if !p.isAtEnd() {
		p.current++
	}
	return p.previous()
}

// check reports whether the current token has any of the given types
// without consuming it.
func (p *Parser) check(types ...token.Type) bool {
	t := p.peek().Type
	for _, want := range types {
		if t == want {
			return true
		}
	}
	return false
}

// match consumes and returns the current token if its type matches any of
// the given types; otherwise it returns a zero token and false without
// advancing (Python: _match returns the token or None).
func (p *Parser) match(types ...token.Type) (token.Token, bool) {
	if p.check(types...) {
		return p.advance(), true
	}
	return token.Token{}, false
}

// consume advances past the expected token type, recording a parse error
// when the current token does not match (Python: _expect). On a mismatch it
// returns a zero token and the recorded *ParseError without advancing.
func (p *Parser) consume(t token.Type, message string) (token.Token, *ParseError) {
	if p.check(t) {
		return p.advance(), nil
	}
	return token.Token{}, p.error(message)
}

// expectCoded is consume with an explicit diagnostic code (Python:
// _expect_coded).
func (p *Parser) expectCoded(t token.Type, code diag.Code, message string) (token.Token, *ParseError) {
	if p.check(t) {
		return p.advance(), nil
	}
	return token.Token{}, p.codedError(code, message)
}

// skipSemicolons consumes any run of semicolons at the current position
// (Python: _skip_semicolons).
func (p *Parser) skipSemicolons() {
	for p.check(token.SEMICOLON) {
		p.advance()
	}
}

// ── Error collection (parser.py:_error/_coded_error) ──

// error records a plain syntax error at the current token and returns it.
// The message names the offending token, mirroring the Python format
// "{message}, got {type.name} ('{lexeme}')".
func (p *Parser) error(message string) *ParseError {
	tok := p.peek()
	err := &ParseError{
		Message: fmt.Sprintf("%s, got %s ('%s')", message, tok.Type, tok.Lexeme),
		Span:    token.Span{Start: tok.Location, End: tok.Location},
	}
	p.errors = append(p.errors, err)
	return err
}

// codedError records a coded parse error at the given token (defaults to the
// current token) and returns it (Python: _coded_error). The Python reference
// passes bare int codes through this path (never the DSLDiagnosticCode enum),
// so the error is marked NumericWire for wire parity (str(code)).
func (p *Parser) codedError(code diag.Code, message string, tok ...*token.Token) *ParseError {
	t := p.peek()
	if len(tok) > 0 && tok[0] != nil {
		t = *tok[0]
	}
	err := &ParseError{
		Code:        code,
		Message:     message,
		Span:        token.Span{Start: t.Location, End: t.Location},
		NumericWire: true,
	}
	p.errors = append(p.errors, err)
	return err
}

// synchronize discards tokens until a statement boundary is found so that
// parsing can resume after an error (Python: _synchronize). Boundaries are
// a consumed semicolon, a statement/declaration keyword, or RBRACE.
func (p *Parser) synchronize() {
	p.advance()
	for !p.isAtEnd() {
		if p.previous().Type == token.SEMICOLON {
			return
		}
		switch p.peek().Type {
		case token.KW_FUNCTION, token.KW_CODE, token.KW_IF, token.KW_FOR,
			token.KW_WHILE, token.KW_RETURN, token.KW_OUTPUT, token.KW_TYPE,
			token.KW_ENUM, token.KW_VAR, token.KW_PARALLEL, token.KW_BREAK,
			token.KW_CONTINUE, token.RBRACE:
			return
		}
		p.advance()
	}
}

// spanFrom builds the AST span from the given start token to the most
// recently consumed token (Python: _span_from).
func (p *Parser) spanFrom(start token.Token) ast.Span {
	return ast.Span{Start: start.Location, End: p.previous().Location}
}

// ParseSource lexes and parses source text, mirroring the front segment of
// pipeline.py:compile_dc_result (lexer -> parser). Lexer errors short-circuit
// before the parser runs and are surfaced as SYNTAX_ERROR ParseErrors with an
// unusable (nil) program, matching the pipeline's failed_stage="lexer" exit;
// parse errors likewise leave the program unusable (the pipeline's
// failed_stage="parser" exit). Empty input lexes to a lone EOF and parses to
// an empty Program with zero errors, exactly like Python's ProgramNode()
// zero-value defaults (mode "workflow").
//
// Error-code asymmetry: lexer failures already carry Code=SYNTAX_ERROR
// (pipeline.py wraps lexer errors in DSLDiagnosticCode.SYNTAX_ERROR directly),
// while parser errors surface with Code=0 (plain syntax errors). Normalizing
// parser errors to SYNTAX_ERROR belongs to the later diagnostic layer,
// mirroring pipeline.py:compile_dc_result's `_parser_diagnostic` (lines
// 211-213), not to this entry point.
//
// Diagnostic wrapping and failed_stage plumbing land in a later task; this
// entry point only covers the lexer->parser segment.
func ParseSource(source, filename string) (*ast.Program, []*ParseError) {
	lx := lexer.New(source, filename)
	tokens := lx.Tokenize()
	if len(lx.Errors()) > 0 {
		errs := make([]*ParseError, 0, len(lx.Errors()))
		for _, le := range lx.Errors() {
			errs = append(errs, &ParseError{
				Code:    diag.SYNTAX_ERROR,
				Message: le.Message,
				Span:    token.Span{Start: le.Location, End: le.Location},
			})
		}
		return nil, errs
	}
	prog, errs := New(tokens).Parse()
	if len(errs) > 0 {
		return nil, errs
	}
	return prog, nil
}

// isWordToken reports whether tok is a name-like token (identifier or
// keyword): used where an arbitrary word may appear as a name, e.g. an
// reply-slot name ("output" lexes as KW_OUTPUT).
func isWordToken(tok token.Token) bool {
	if tok.Type == token.IDENTIFIER {
		return true
	}
	return tok.Type >= token.KW_FUNCTION && tok.Type <= token.KW_ANY
}

// parseLibraryDecl parses `library <name> @ <major>.<minor>.<patch> ;`.
// The version is collected token-by-token because the lexer splits "0.1.0"
// into a float, a dot and an int. The declaration is identity only: it never
// reaches the IR and never participates in the ABI hash.
func (p *Parser) parseLibraryDecl() (*ast.LibraryDecl, *ParseError) {
	if _, err := p.consume(token.KW_LIBRARY, "Expected 'library'"); err != nil {
		return nil, err
	}
	nameTok, err := p.consume(token.IDENTIFIER, "Expected library name after 'library'")
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.AT, "Expected '@' before library version"); err != nil {
		return nil, err
	}
	var parts []string
	for !p.isAtEnd() && !p.check(token.SEMICOLON) {
		tok := p.advance()
		switch tok.Type {
		case token.INT_LITERAL, token.FLOAT_LITERAL, token.IDENTIFIER, token.DOT:
			parts = append(parts, tok.Lexeme)
		default:
			return nil, p.error(fmt.Sprintf("Unexpected %s in library version", tok.Type))
		}
	}
	version := strings.Join(parts, "")
	if !libraryVersionRe.MatchString(version) {
		return nil, p.error(fmt.Sprintf("Library version %q must be MAJOR.MINOR.PATCH", version))
	}
	if _, err := p.consume(token.SEMICOLON, "Expected ';' after library declaration"); err != nil {
		return nil, err
	}
	return ast.NewLibraryDecl(nameTok.Lexeme, version), nil
}
