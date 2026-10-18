// Package token defines token types, tokens, and source locations for the
// NodeCoda DSL lexer. Ported from src/nclang/lang/tokens.py.
package token

import "fmt"

// Type enumerates all token types recognized by the DSL lexer.
// Order matches TokenType in lang/tokens.py (auto() = 1-based sequence) minus the
// removed dead reserved tokens KW_ENTRY / KW_STREAM / PIPE (A1 cleanup, see docs/PARITY.md §2).
type Type int

const (
	// Literals
	STRING_LITERAL  Type = iota + 1
	TEMPLATE_STRING      // backtick string with ${expr} interpolation
	INT_LITERAL
	FLOAT_LITERAL
	BOOL_LITERAL
	NULL_LITERAL

	// Identifier
	IDENTIFIER

	// Keywords
	KW_FUNCTION
	KW_IF
	KW_ELSE
	KW_FOR
	KW_WHILE
	KW_IN
	KW_RETURN
	KW_VAR
	KW_LET
	KW_CONST
	KW_OUTPUT
	KW_TYPE
	KW_ENUM
	KW_PARALLEL
	KW_BREAK
	KW_CONTINUE
	KW_CODE
	KW_FOREIGN
	KW_SOURCE
	KW_YIELD
	KW_LIMIT
	KW_ATTEMPT
	KW_SUCCESS
	KW_FAILURE
	KW_WITH
	KW_RETRY
	KW_TIMEOUT
	KW_DEFAULT
	KW_AS
	KW_REQUEST_INPUT
	KW_INTENT
	KW_SWITCH
	KW_ACTION
	KW_IMPORT
	KW_SUBFLOW
	KW_TRUE
	KW_FALSE
	KW_NULL

	// Type keywords
	KW_STRING
	KW_INT
	KW_FLOAT
	KW_BOOL
	KW_FILE
	KW_VOID
	KW_MAP
	KW_ARRAY
	KW_OBJECT
	KW_ANY

	// Operators
	PLUS
	MINUS
	STAR
	SLASH
	PERCENT
	ASSIGN
	EQUAL
	NOT_EQUAL
	LESS
	LESS_EQUAL
	GREATER
	GREATER_EQUAL
	AND
	OR
	NOT
	DOT
	ARROW        // ->
	PLUS_ASSIGN  // +=
	MINUS_ASSIGN // -=
	STAR_ASSIGN  // *=
	SLASH_ASSIGN // /=
	LESS_LESS    // << (append)

	// Delimiters
	LPAREN
	RPAREN
	LBRACE
	RBRACE
	LBRACKET
	RBRACKET
	COMMA
	COLON
	SEMICOLON
	QUESTION
	NULL_COALESCE // ?? (route 1a: first-not-null 值选择)

	// At-declarations
	AT_LANGUAGE     // @language declaration
	AT_MODE         // @mode declaration
	AT_CONVERSATION // @conversation declaration
	AT_AGENT        // @agent declaration

	// Special
	EOF
	ERROR
)

// names mirrors the Python enum member names exactly (used for diagnostics
// parity and differential serialization).
var names = map[Type]string{
	STRING_LITERAL: "STRING_LITERAL", TEMPLATE_STRING: "TEMPLATE_STRING",
	INT_LITERAL: "INT_LITERAL", FLOAT_LITERAL: "FLOAT_LITERAL",
	BOOL_LITERAL: "BOOL_LITERAL", NULL_LITERAL: "NULL_LITERAL",
	IDENTIFIER:  "IDENTIFIER",
	KW_FUNCTION: "KW_FUNCTION", KW_IF: "KW_IF",
	KW_ELSE: "KW_ELSE", KW_FOR: "KW_FOR", KW_WHILE: "KW_WHILE",
	KW_IN: "KW_IN", KW_RETURN: "KW_RETURN", KW_VAR: "KW_VAR",
	KW_LET: "KW_LET", KW_CONST: "KW_CONST", KW_OUTPUT: "KW_OUTPUT",
	KW_TYPE: "KW_TYPE", KW_ENUM: "KW_ENUM", KW_PARALLEL: "KW_PARALLEL",
	KW_BREAK: "KW_BREAK", KW_CONTINUE: "KW_CONTINUE",
	KW_CODE: "KW_CODE", KW_FOREIGN: "KW_FOREIGN", KW_SOURCE: "KW_SOURCE",
	KW_YIELD: "KW_YIELD", KW_LIMIT: "KW_LIMIT",
	KW_ATTEMPT: "KW_ATTEMPT", KW_SUCCESS: "KW_SUCCESS", KW_FAILURE: "KW_FAILURE",
	KW_WITH: "KW_WITH", KW_RETRY: "KW_RETRY", KW_TIMEOUT: "KW_TIMEOUT",
	KW_DEFAULT: "KW_DEFAULT", KW_AS: "KW_AS", KW_REQUEST_INPUT: "KW_REQUEST_INPUT",
	KW_ACTION: "KW_ACTION", KW_IMPORT: "KW_IMPORT", KW_SUBFLOW: "KW_SUBFLOW", KW_INTENT: "KW_INTENT", KW_SWITCH: "KW_SWITCH",
	KW_TRUE: "KW_TRUE", KW_FALSE: "KW_FALSE",
	KW_NULL:   "KW_NULL",
	KW_STRING: "KW_STRING", KW_INT: "KW_INT", KW_FLOAT: "KW_FLOAT",
	KW_BOOL: "KW_BOOL", KW_FILE: "KW_FILE", KW_VOID: "KW_VOID",
	KW_MAP: "KW_MAP", KW_ARRAY: "KW_ARRAY", KW_OBJECT: "KW_OBJECT", KW_ANY: "KW_ANY",
	PLUS: "PLUS", MINUS: "MINUS", STAR: "STAR", SLASH: "SLASH",
	PERCENT: "PERCENT", ASSIGN: "ASSIGN", EQUAL: "EQUAL", NOT_EQUAL: "NOT_EQUAL",
	LESS: "LESS", LESS_EQUAL: "LESS_EQUAL", GREATER: "GREATER",
	GREATER_EQUAL: "GREATER_EQUAL", AND: "AND", OR: "OR", NOT: "NOT",
	DOT: "DOT", ARROW: "ARROW", PLUS_ASSIGN: "PLUS_ASSIGN",
	MINUS_ASSIGN: "MINUS_ASSIGN", STAR_ASSIGN: "STAR_ASSIGN",
	SLASH_ASSIGN: "SLASH_ASSIGN", LESS_LESS: "LESS_LESS",
	LPAREN: "LPAREN", RPAREN: "RPAREN", LBRACE: "LBRACE", RBRACE: "RBRACE",
	LBRACKET: "LBRACKET", RBRACKET: "RBRACKET", COMMA: "COMMA",
	COLON: "COLON", SEMICOLON: "SEMICOLON", QUESTION: "QUESTION", NULL_COALESCE: "NULL_COALESCE",
	AT_LANGUAGE: "AT_LANGUAGE", AT_MODE: "AT_MODE", AT_CONVERSATION: "AT_CONVERSATION",
	AT_AGENT: "AT_AGENT",
	EOF:      "EOF", ERROR: "ERROR",
}

// String returns the Python-equivalent enum member name.
func (t Type) String() string {
	if s, ok := names[t]; ok {
		return s
	}
	return fmt.Sprintf("TokenType(%d)", int(t))
}

// keywords maps reserved keyword spellings to their token types,
// mirroring _KEYWORDS in lang/tokens.py.
var keywords = map[string]Type{
	"function": KW_FUNCTION, "if": KW_IF,
	"else": KW_ELSE, "for": KW_FOR, "while": KW_WHILE,
	"in": KW_IN, "return": KW_RETURN, "var": KW_VAR,
	"let": KW_LET, "const": KW_CONST, "output": KW_OUTPUT,
	"type": KW_TYPE, "enum": KW_ENUM, "parallel": KW_PARALLEL,
	"break": KW_BREAK, "continue": KW_CONTINUE,
	"code": KW_CODE, "foreign": KW_FOREIGN, "source": KW_SOURCE,
	"yield": KW_YIELD, "limit": KW_LIMIT,
	"attempt": KW_ATTEMPT, "success": KW_SUCCESS, "failure": KW_FAILURE,
	"with": KW_WITH, "retry": KW_RETRY, "timeout": KW_TIMEOUT,
	"default": KW_DEFAULT, "as": KW_AS, "ask": KW_REQUEST_INPUT,
	"request_input": KW_REQUEST_INPUT, // 兼容别名（旧关键字保留）
	"intent":        KW_INTENT,
	"switch":        KW_SWITCH,
	"action":        KW_ACTION, "import": KW_IMPORT, "subflow": KW_SUBFLOW, "true": KW_TRUE, "false": KW_FALSE,
	"null":   KW_NULL,
	"string": KW_STRING, "int": KW_INT, "float": KW_FLOAT,
	"bool": KW_BOOL, "file": KW_FILE, "void": KW_VOID,
	"map": KW_MAP, "array": KW_ARRAY, "object": KW_OBJECT, "any": KW_ANY,
}

// LookupKeyword looks up a keyword by its text. Returns 0 if not a keyword.
func LookupKeyword(text string) Type { return keywords[text] }

// IsKeyword reports whether text is a reserved keyword.
func IsKeyword(text string) bool { _, ok := keywords[text]; return ok }

// Location is a precise position in source text.
// Line and Column are 1-based; Offset is a 0-based index into source
// (character index, matching Python str indexing semantics).
type Location struct {
	Line     int
	Column   int
	Offset   int
	Filename string
}

func (l Location) String() string {
	if l.Filename != "" {
		return fmt.Sprintf("%s:%d:%d", l.Filename, l.Line, l.Column)
	}
	return fmt.Sprintf("%d:%d", l.Line, l.Column)
}

// Span is a span from start to end in source text.
type Span struct {
	Start Location
	End   Location
}

func (s Span) String() string {
	return fmt.Sprintf("%s-%s", s.Start, s.End)
}

// Literal is the typed literal carried by a Token (string, int64, float64, or bool).
type Literal struct {
	Kind  LiteralKind
	S     string
	I     int64
	F     float64
	B     bool
	Valid bool
}

// LiteralKind distinguishes literal payload kinds.
type LiteralKind int

const (
	LitNone LiteralKind = iota
	LitString
	LitInt
	LitFloat
	LitBool
)

// Token is a single lexical token with source location.
type Token struct {
	Type     Type
	Lexeme   string
	Literal  Literal
	Location Location
}

// IsError reports whether the token is an ERROR token.
func (t *Token) IsError() bool { return t.Type == ERROR }

// IsEOF reports whether the token is the EOF token.
func (t *Token) IsEOF() bool { return t.Type == EOF }

func (t *Token) String() string {
	return fmt.Sprintf("Token(%s, %q, %s)", t.Type, t.Lexeme, t.Location)
}
