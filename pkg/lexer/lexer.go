// Package lexer implements the hand-written character-by-character lexer for
// the NodeCoda C-style DSL. Ported from src/nclang/lang/lexer.py.
package lexer

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/nodecoda/ncoda/pkg/token"
)

// Error is a lexical error carrying a precise source location.
// Mirrors LexerError in lang/lexer.py.
type Error struct {
	Message  string
	Location token.Location
}

func (e *Error) Error() string {
	return e.Message
}

// Lexer scans source text into a token stream ending with EOF.
type Lexer struct {
	source []rune // rune-indexed source, matching Python str indexing
	file   string

	start, current int
	line, col      int
	startLine      int
	startCol       int

	tokens []token.Token
	errors []*Error
}

// New creates a lexer for the given source text and filename.
func New(source, filename string) *Lexer {
	return &Lexer{
		source: []rune(source),
		file:   filename,
		line:   1,
		col:    1,
	}
}

// Tokenize scans the entire source and returns the token list ending with EOF.
func (l *Lexer) Tokenize() []token.Token {
	for !l.isAtEnd() {
		l.start = l.current
		l.startLine = l.line
		l.startCol = l.col
		l.scanToken()
	}
	l.tokens = append(l.tokens, token.Token{
		Type:     token.EOF,
		Location: token.Location{Line: l.line, Column: l.col, Offset: l.current, Filename: l.file},
	})
	return l.tokens
}

// Errors returns all lexical errors collected during scanning.
func (l *Lexer) Errors() []*Error { return l.errors }

// ── Character reading ──

func (l *Lexer) advance() rune {
	ch := l.source[l.current]
	l.current++
	if ch == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return ch
}

func (l *Lexer) peek() rune {
	if l.isAtEnd() {
		return 0
	}
	return l.source[l.current]
}

func (l *Lexer) peekNext() rune {
	pos := l.current + 1
	if pos >= len(l.source) {
		return 0
	}
	return l.source[pos]
}

func (l *Lexer) match(expected rune) bool {
	if l.isAtEnd() || l.source[l.current] != expected {
		return false
	}
	l.advance()
	return true
}

func (l *Lexer) isAtEnd() bool { return l.current >= len(l.source) }

// ── Token scanning ──

func (l *Lexer) scanToken() {
	ch := l.advance()

	switch {
	case ch == '(':
		l.add(token.LPAREN, token.Literal{}, "")
	case ch == ')':
		l.add(token.RPAREN, token.Literal{}, "")
	case ch == '{':
		l.add(token.LBRACE, token.Literal{}, "")
	case ch == '}':
		l.add(token.RBRACE, token.Literal{}, "")
	case ch == '[':
		l.add(token.LBRACKET, token.Literal{}, "")
	case ch == ']':
		l.add(token.RBRACKET, token.Literal{}, "")
	case ch == ',':
		l.add(token.COMMA, token.Literal{}, "")
	case ch == ':':
		l.add(token.COLON, token.Literal{}, "")
	case ch == ';':
		l.add(token.SEMICOLON, token.Literal{}, "")
	case ch == '?':
		if l.match('?') {
			// ?? = NULL_COALESCE（route 1a first-not-null）。连续两个 '?'
			// 合并为单 token；三目 `a ? b : c` 的 '?' 后不跟 '?' 不受影响，
			// Optional 类型标记 `T?` 的 '?' 后是标识符/空白也不受影响。
			l.add(token.NULL_COALESCE, token.Literal{}, "")
		} else {
			l.add(token.QUESTION, token.Literal{}, "")
		}
	case ch == '@':
		l.scanAtDeclaration()
	case ch == '%':
		l.add(token.PERCENT, token.Literal{}, "")
	case ch == '+':
		if l.match('=') {
			l.add(token.PLUS_ASSIGN, token.Literal{}, "")
		} else {
			l.add(token.PLUS, token.Literal{}, "")
		}
	case ch == '-':
		if l.match('>') {
			l.add(token.ARROW, token.Literal{}, "")
		} else if l.match('=') {
			l.add(token.MINUS_ASSIGN, token.Literal{}, "")
		} else {
			l.add(token.MINUS, token.Literal{}, "")
		}
	case ch == '*':
		if l.match('=') {
			l.add(token.STAR_ASSIGN, token.Literal{}, "")
		} else {
			l.add(token.STAR, token.Literal{}, "")
		}
	case ch == '/':
		switch {
		case l.match('='):
			l.add(token.SLASH_ASSIGN, token.Literal{}, "")
		case l.match('/'):
			l.skipLineComment()
		case l.match('*'):
			l.skipBlockComment()
		default:
			l.add(token.SLASH, token.Literal{}, "")
		}
	case ch == '=':
		if l.match('=') {
			l.add(token.EQUAL, token.Literal{}, "")
		} else {
			l.add(token.ASSIGN, token.Literal{}, "")
		}
	case ch == '!':
		if l.match('=') {
			l.add(token.NOT_EQUAL, token.Literal{}, "")
		} else {
			l.add(token.NOT, token.Literal{}, "")
		}
	case ch == '<':
		switch {
		case l.match('='):
			l.add(token.LESS_EQUAL, token.Literal{}, "")
		case l.match('<'):
			l.add(token.LESS_LESS, token.Literal{}, "")
		default:
			l.add(token.LESS, token.Literal{}, "")
		}
	case ch == '>':
		if l.match('=') {
			l.add(token.GREATER_EQUAL, token.Literal{}, "")
		} else {
			l.add(token.GREATER, token.Literal{}, "")
		}
	case ch == '&':
		if l.match('&') {
			l.add(token.AND, token.Literal{}, "")
		} else {
			l.error("Unexpected character '&' (did you mean '&&'?)")
		}
	case ch == '|':
		switch {
		case l.match('|'):
			l.add(token.OR, token.Literal{}, "")
		case l.match('>'):
			l.error("Pipe operator '|>' is not supported")
		default:
			l.error("Unexpected character '|' (did you mean '||'?)")
		}
	case ch == '.':
		l.add(token.DOT, token.Literal{}, "")
	case ch == '"' || ch == '\'':
		l.scanString(ch)
	case ch == '`':
		l.scanBacktickString()
	case ch == '$':
		l.scanDollarEscape()
	case ch == ' ' || ch == '\t' || ch == '\r':
		// insignificant whitespace
	case ch == '\n':
		// handled in advance
	case unicode.IsDigit(ch):
		l.scanNumber()
	case isIdentStart(ch):
		l.scanIdentifier()
	default:
		l.error("Unexpected character: " + quoteChar(ch))
	}
}

func isIdentStart(ch rune) bool { return ch == '_' || unicode.IsLetter(ch) }

func isIdentPart(ch rune) bool { return ch == '_' || unicode.IsLetter(ch) || unicode.IsDigit(ch) }

func quoteChar(ch rune) string {
	if ch == '\'' {
		return `"'"`
	}
	if ch == '"' {
		return `'"'`
	}
	return "'" + string(ch) + "'"
}

// scanAtDeclaration scans @language/@mode/@conversation/@agent.
// @language and @mode values are scanned as a single identifier token until
// end-of-line or EOF, allowing versioned or hyphenated values. @conversation
// and @agent emit their AT_* marker only; the rest of the line is tokenized
// normally.
//
// `@answer` was retired with W1-D4/W2: 流程答复值由 `return template … using {…}`
// 承载（答复端点是**流程出口**，不是文件头声明）。
func (l *Lexer) scanAtDeclaration() {
	start := l.current
	for !l.isAtEnd() && isIdentPart(l.peek()) {
		l.advance()
	}
	keyword := string(l.source[start:l.current])

	switch keyword {
	case "language":
		l.add(token.AT_LANGUAGE, token.Literal{}, "")
		l.scanAtValue(len("@language"))
	case "mode":
		l.add(token.AT_MODE, token.Literal{}, "")
		l.scanAtValue(len("@mode"))
	case "conversation":
		l.add(token.AT_CONVERSATION, token.Literal{}, "")
	case "agent":
		l.add(token.AT_AGENT, token.Literal{}, "")
	default:
		if keyword == "" {
			l.add(token.AT, token.Literal{}, "@")
			return
		}
		l.error("Unknown @-declaration: @" + keyword)
	}
}

func (l *Lexer) scanAtValue(declarationLength int) {
	for !l.isAtEnd() && (l.peek() == ' ' || l.peek() == '\t') {
		l.advance()
	}
	valueStart := l.current
	for !l.isAtEnd() && l.peek() != '\n' && l.peek() != '\r' {
		l.advance()
	}
	value := strings.TrimSpace(string(l.source[valueStart:l.current]))
	if value != "" {
		l.tokens = append(l.tokens, token.Token{
			Type:   token.IDENTIFIER,
			Lexeme: value,
			Location: token.Location{
				Line:     l.startLine,
				Column:   l.startCol + declarationLength + 1,
				Offset:   valueStart,
				Filename: l.file,
			},
		})
	}
}

// scanString scans a single- or double-quoted string literal with escapes.
func (l *Lexer) scanString(quote rune) {
	var parts []rune
	for !l.isAtEnd() && l.peek() != quote {
		// Python lexer.py:262 checks the raw newline before the backslash
		// branch: an unescaped newline inside a quoted string is an error,
		// reported at the token start ("Unterminated string literal (newline
		// in string)"). A backslash-newline escape consumes the newline and
		// stays legal, exactly like Python.
		if l.peek() == '\n' {
			l.error("Unterminated string literal (newline in string)")
			return
		}
		if l.peek() == '\\' {
			l.advance() // consume backslash
			if l.isAtEnd() {
				l.error("Unterminated string literal (ends with backslash)")
				return
			}
			parts = append(parts, l.escapeChar(l.advance()))
		} else {
			parts = append(parts, l.advance())
		}
	}
	if l.isAtEnd() {
		l.error("Unterminated string literal")
		return
	}
	l.advance() // closing quote
	l.add(token.STRING_LITERAL, token.Literal{Kind: token.LitString, S: string(parts), Valid: true}, "")
}

// scanBacktickString scans a backtick-delimited string, supporting ${expr}
// interpolation. A backtick string without interpolation is a plain
// STRING_LITERAL; with interpolation it is a TEMPLATE_STRING.
func (l *Lexer) scanBacktickString() {
	var parts []rune
	hasTemplate := false
	for !l.isAtEnd() && l.peek() != '`' {
		// Backslash escapes inside backtick strings: \`` → literal `, `\\` →
		// literal backslash, `\$` → literal $ (disables ${ interpolation).
		// This lets a foreign-code source carry JavaScript template literals
		// (backticks + ${...}) byte-exactly; the decompiler escapes them and
		// the parser restores the literal text (透传语义不变).
		if l.peek() == '\\' && l.peekNext() != 0 {
			nxt := l.peekNext()
			if nxt == '`' || nxt == '\\' || nxt == '$' {
				l.advance() // consume backslash
				parts = append(parts, l.advance())
				continue
			}
		}
		if l.peek() == '$' && l.peekNext() == '{' {
			hasTemplate = true
			l.advance() // $
			l.advance() // {
			parts = append(parts, '$', '{')
			depth := 1
			for !l.isAtEnd() && depth > 0 {
				ch := l.peek()
				if ch == '{' {
					depth++
				} else if ch == '}' {
					depth--
					if depth == 0 {
						break
					}
				}
				parts = append(parts, l.advance())
			}
			if l.isAtEnd() {
				l.error("Unterminated template expression (missing })")
				return
			}
			l.advance() // closing }
			parts = append(parts, '}')
		} else {
			parts = append(parts, l.advance())
		}
	}
	if l.isAtEnd() {
		l.error("Unterminated backtick string")
		return
	}
	l.advance() // closing backtick
	raw := string(parts)
	tokType := token.STRING_LITERAL
	if hasTemplate {
		tokType = token.TEMPLATE_STRING
	}
	l.add(tokType, token.Literal{Kind: token.LitString, S: raw, Valid: true}, "")
}

func (l *Lexer) escapeChar(ch rune) rune {
	switch ch {
	case 'n':
		return '\n'
	case 't':
		return '\t'
	case 'r':
		return '\r'
	case '\\':
		return '\\'
	case '\'':
		return '\''
	case '"':
		return '"'
	default:
		l.error("Unknown escape sequence: \\" + string(ch))
		return ch
	}
}

// scanNumber scans an integer or float literal. A '.' only continues the
// number when followed by a digit.
func (l *Lexer) scanNumber() {
	for !l.isAtEnd() && unicode.IsDigit(l.peek()) {
		l.advance()
	}
	if !l.isAtEnd() && l.peek() == '.' && unicode.IsDigit(l.peekNext()) {
		l.advance() // '.'
		for !l.isAtEnd() && unicode.IsDigit(l.peek()) {
			l.advance()
		}
		text := string(l.source[l.start:l.current])
		// strconv parses the same float syntax Python accepts here.
		f := parseFloat(text)
		l.add(token.FLOAT_LITERAL, token.Literal{Kind: token.LitFloat, F: f, Valid: true}, "")
	} else {
		text := string(l.source[l.start:l.current])
		i := parseInt(text)
		l.add(token.INT_LITERAL, token.Literal{Kind: token.LitInt, I: i, Valid: true}, "")
	}
}

// scanDollarEscape scans a '$'-escaped identifier (NCODA-DOLLAR-ESCAPE-SPEC):
// '$' is a pure lexical escape marker — it is skipped and the following
// identifier (even a keyword like type/output) is emitted as an ordinary
// IDENTIFIER token. External contract field names that collide with ncoda
// keywords are written $type/$output in ncoda source; parser/IR/platform see
// "type"/"output", so roundtrip needs zero render-layer rewriting.
//
//	$type → IDENTIFIER("type")   $foo → IDENTIFIER("foo")
//	$1 / $$ / '$ ' / '$:' / trailing '$' → lexical error (never silently
//	dropped; the escaped identifier must start with a letter or '_').
func (l *Lexer) scanDollarEscape() {
	// '$' was already consumed by scanToken; l.current points past it.
	if l.isAtEnd() || !(l.peek() == '_' || unicode.IsLetter(l.peek())) {
		l.error("Unexpected character '$' (a '$'-escaped identifier must be '$' followed by a letter or '_')")
		return
	}
	for !l.isAtEnd() && isIdentPart(l.peek()) {
		l.advance()
	}
	// Drop the '$': the identifier token starts at the character after it
	// (Lexeme excludes '$'; Location points at the identifier body).
	l.start++
	l.add(token.IDENTIFIER, token.Literal{}, "")
}

// scanIdentifier scans an identifier or keyword. true/false/null become
// BOOL_LITERAL/NULL_LITERAL with their typed values.
func (l *Lexer) scanIdentifier() {
	for !l.isAtEnd() && isIdentPart(l.peek()) {
		l.advance()
	}
	text := string(l.source[l.start:l.current])
	kw := token.LookupKeyword(text)
	switch kw {
	case token.KW_TRUE:
		l.add(token.BOOL_LITERAL, token.Literal{Kind: token.LitBool, B: true, Valid: true}, text)
	case token.KW_FALSE:
		l.add(token.BOOL_LITERAL, token.Literal{Kind: token.LitBool, B: false, Valid: true}, text)
	case token.KW_NULL:
		l.add(token.NULL_LITERAL, token.Literal{}, text)
	case 0:
		l.add(token.IDENTIFIER, token.Literal{}, "")
	default:
		l.add(kw, token.Literal{}, "")
	}
}

// ── Comment handling ──

func (l *Lexer) skipLineComment() {
	for !l.isAtEnd() && l.peek() != '\n' {
		l.advance()
	}
}

func (l *Lexer) skipBlockComment() {
	depth := 1
	for !l.isAtEnd() && depth > 0 {
		if l.peek() == '/' && l.peekNext() == '*' {
			l.advance()
			l.advance()
			depth++
		} else if l.peek() == '*' && l.peekNext() == '/' {
			l.advance()
			l.advance()
			depth--
		} else {
			l.advance()
		}
	}
	if depth > 0 {
		l.error("Unterminated block comment")
	}
}

// ── Token creation ──

func (l *Lexer) makeLocation() token.Location {
	return token.Location{
		Line:     l.startLine,
		Column:   l.startCol,
		Offset:   l.start,
		Filename: l.file,
	}
}

func (l *Lexer) add(tokType token.Type, lit token.Literal, lexemeOverride string) {
	lexeme := lexemeOverride
	if lexeme == "" {
		lexeme = string(l.source[l.start:l.current])
	}
	l.tokens = append(l.tokens, token.Token{
		Type:     tokType,
		Lexeme:   lexeme,
		Literal:  lit,
		Location: l.makeLocation(),
	})
}

func (l *Lexer) error(message string) {
	loc := l.makeLocation()
	l.errors = append(l.errors, &Error{Message: message, Location: loc})
	l.tokens = append(l.tokens, token.Token{Type: token.ERROR, Location: loc})
}

// ── Numeric parsing helpers ──

func parseInt(text string) int64 {
	// Python int(text) on pure-digit strings; the lexer guarantees digits only.
	var n int64
	for _, r := range text {
		n = n*10 + int64(r-'0')
	}
	return n
}

func parseFloat(text string) float64 {
	// The lexer guarantees a shape like "123.456" (digits '.' digits),
	// so strconv.ParseFloat with bitSize=64 matches Python float() for this
	// subset. Parse failures are impossible for the guaranteed shape.
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0
	}
	return f
}
