package parser

import (
	"fmt"

	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/token"
)

// ParseError is a syntax error with an optional diagnostic code and a source
// span. It mirrors parser.py:ParseError:
//
//   - Code may be 0/unset for plain syntax errors (Python: code=None).
//   - Span covers the offending token; Start == End for point errors.
//     Python exposes the same location via ParseError.location.
type ParseError struct {
	Code    diag.Code
	Message string
	Span    token.Span
	// AdapterError marks a synthetic depth-limit failure (P4-4) that must
	// serialize as ADAPTER_ERROR / "maximum recursion depth exceeded"
	// without a location — exactly like Python's RecursionError catch in
	// compile_cli.compile_to_json (compile_cli.py:84-90).
	AdapterError bool
	// NumericWire mirrors the Python parser's bare-int diagnostic codes:
	// _coded_error/_expect_coded pass literal 1023-1030 ints (never the
	// DSLDiagnosticCode enum), so the wire serializes str(code). Set only
	// by codedError/expectCoded; plain syntax errors (Code 0, normalized
	// to SYNTAX_ERROR downstream) and lexer errors (already enum-wrapped
	// by ParseSource) leave it false.
	NumericWire bool
}

// recursionLimitError builds the synthetic depth-limit ParseError whose
// diagnostics serialize byte-identically to Python's RecursionError catch
// (ADAPTER_ERROR, no location).
func (p *Parser) recursionLimitError() *ParseError {
	return &ParseError{Message: "maximum recursion depth exceeded", AdapterError: true}
}

// String renders the error in the Python-equivalent form:
//
//	with code:    "E1000: message at 2:5-2:5"
//	without code: "message at 2:5-2:5"
func (e *ParseError) String() string {
	prefix := ""
	if e.Code != 0 {
		prefix = fmt.Sprintf("E%04d: ", e.Code)
	}
	return fmt.Sprintf("%s%s at %s", prefix, e.Message, e.Span)
}

// Error implements the error interface.
func (e *ParseError) Error() string { return e.String() }
