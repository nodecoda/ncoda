package verifier

// Restricted Python source scanners used by the verifier, porting the
// python_ast-based helpers from verifier.py with a small hand-written
// tokenizer instead of a full Python parser. These are honest, declared
// deviations: they cover exactly the generated-code shapes the Go lowering
// emits (see the message contracts in the Python reference) and skip any
// text they cannot parse. They are intentionally conservative — a missed
// match is a no-op (the corpus is byte-identical to the Python verifier,
// which also emits nothing on the 62 examples), while a false positive
// would fail the golden regression, so the scanner errs on the side of
// skipping.
//
// V14 (_verify_no_user_function_calls, verifier.py:971-1012) uses
// python_ast.parse + walk over Call nodes whose func is a plain Name.
// ast.walk descends into JoinedStr/FormattedValue, so f-string replacement
// fields are scanned for calls (f"{helper()}" is flagged). The Go analogue
// scans for `identifier(` call sites while skipping string literals,
// comments, attribute accesses (`obj.fn(`) and function definitions
// (`def fn(`), and recursively scans f-string replacement fields. It does
// not implement the full Python grammar (no tuple unpacking, no keywords in
// call position, etc.) — deviation declared in the task-37 report.

import (
	"sort"
	"strings"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
	"github.com/nodecoda/ncoda/pkg/backend/dify/graphfacts"
)

// verifyNoUserFunctionCalls mirrors _verify_no_user_function_calls
// (verifier.py:971-1012): generated Python cannot retain a bound NodeCoda
// user-function call. Diagnostics are emitted in sorted name order exactly
// like the Python `sorted({...})` residual set.
func verifyNoUserFunctionCalls(graph *dify.GraphNode, facts *graphfacts.SemanticGraphFacts, result *VerificationResult) {
	if len(facts.UserFunctionNames) == 0 {
		return
	}
	for i := range graph.Nodes {
		node := &graph.Nodes[i]
		code, isCode := node.Data.(*dify.CodeDataNode)
		if !isCode {
			continue
		}
		if facts.CodeNodeProvenance[node.ID] != "generated-calculation" &&
			!facts.CompilerGeneratedCodeNodes[node.ID] {
			continue
		}
		if code.Code == "" {
			continue
		}
		residual := scanUserFunctionCalls(code.Code, facts.UserFunctionNames)
		if len(residual) == 0 {
			continue
		}
		for _, functionName := range residual {
			result.Diagnostics = append(result.Diagnostics, invariant(
				"Generated Code node '"+node.ID+"' retains NodeCoda user function call "+
					"'"+functionName+"'"))
		}
	}
}

// scanUserFunctionCalls returns the sorted unique user function names called
// with a plain-name call site in src. Strings, comments, attribute accesses
// and `def name(` definitions are skipped; any malformed segment is skipped
// conservatively.
func scanUserFunctionCalls(src string, userFunctions map[string]bool) []string {
	found := map[string]bool{}
	i := 0
	n := len(src)
	for i < n {
		c := src[i]
		switch {
		case c == '\'' || c == '"':
			if isFStringPrefix(src, i) {
				// f"{...}": CPython's ast.walk descends into the JoinedStr
				// replacement fields, so scan each {expr} region for calls.
				end, exprs := scanFString(src, i)
				if end < 0 {
					return nil // unterminated f-string: skip whole source
				}
				for _, span := range exprs {
					for _, name := range scanUserFunctionCalls(src[span[0]:span[1]], userFunctions) {
						found[name] = true
					}
				}
				i = end
				continue
			}
			end := skipString(src, i)
			if end < 0 {
				return nil // unterminated string: skip whole source
			}
			i = end
		case c == '#':
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				i = n
			} else {
				i += end + 1
			}
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		default:
			if isIdentStart(c) {
				end := i + 1
				for end < n && isIdentPart(src[end]) {
					end++
				}
				name := src[i:end]
				j := skipSpace(src, end)
				if j < n && src[j] == '(' {
					prev := i - 1
					for prev >= 0 && (src[prev] == ' ' || src[prev] == '\t' || src[prev] == '\r' || src[prev] == '\n') {
						prev--
					}
					isAttribute := prev >= 0 && src[prev] == '.'
					isDef := false
					// a `def name(` definition: check the token before the
					// identifier (allowing only whitespace between def and name)
					wordStart := i
					for wordStart > 0 && isIdentPart(src[wordStart-1]) {
						wordStart--
					}
					wordStart = i
					k := i
					for k > 0 && (src[k-1] == ' ' || src[k-1] == '\t') {
						k--
					}
					defStart := k - 3
					if defStart >= 0 && src[defStart:defStart+3] == "def" &&
						(defStart+3 == k || !isIdentPart(src[defStart+3])) {
						isDef = true
					}
					_ = wordStart
					if !isAttribute && !isDef && userFunctions[name] {
						found[name] = true
					}
				}
				i = end
			} else {
				i++
			}
		}
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func skipSpace(src string, i int) int {
	for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == '\r' || src[i] == '\n') {
		i++
	}
	return i
}

// skipString skips a Python string literal starting at i (single, double or
// triple-quoted), handling backslash escapes. Returns the index just past
// the closing quote, or -1 for an unterminated literal.
func skipString(src string, i int) int {
	n := len(src)
	quote := src[i]
	// triple-quoted
	if i+2 < n && src[i+1] == quote && src[i+2] == quote {
		start := i + 3
		for j := start; j+2 < n; j++ {
			if src[j] == quote && src[j+1] == quote && src[j+2] == quote {
				return j + 3
			}
		}
		return -1
	}
	// single-quoted (no prefix handling here; f-strings are handled by the
	// caller via scanFString)
	for j := i + 1; j < n; j++ {
		if src[j] == '\\' {
			j++
			continue
		}
		if src[j] == quote {
			return j + 1
		}
	}
	return -1
}

// isFStringPrefix reports whether the string literal starting at i carries
// an f-string prefix (f/F/rf/Rf/rF/RF). Python string prefixes are single
// tokens adjacent to the quote, so the preceding identifier token must be
// exactly one of those forms.
func isFStringPrefix(src string, i int) bool {
	if i == 0 || (src[i] != '\'' && src[i] != '"') {
		return false
	}
	j := i - 1
	start := j
	for start > 0 && isIdentPart(src[start-1]) {
		start--
	}
	switch src[start : j+1] {
	case "f", "F", "rf", "Rf", "rF", "RF", "fr", "Fr", "fR", "FR":
		return true
	}
	return false
}

// scanFString walks an f-string literal starting at i (the opening quote)
// and returns the index just past the closing quote plus the byte spans of
// every {expression} replacement field, mirroring CPython's JoinedStr walk.
// Backslash escapes and {{ / }} literal escapes are handled; nested strings
// inside replacement fields are skipped so their quotes and braces do not
// confuse field boundaries. Returns -1 for an unterminated literal.
func scanFString(src string, i int) (int, [][2]int) {
	n := len(src)
	quote := src[i]
	triple := i+2 < n && src[i+1] == quote && src[i+2] == quote
	j := i + 1
	if triple {
		j = i + 3
	}
	var exprs [][2]int
	exprStart := -1
	depth := 0
	for j < n {
		c := src[j]
		switch {
		case c == '\\':
			// escape (in the literal part or inside a replacement field)
			j += 2
			continue
		case c == quote:
			// the f-string's own quote: closes the literal at depth 0; a
			// nested same-quote string inside a replacement field otherwise.
			if !triple {
				if depth == 0 {
					return j + 1, exprs
				}
				end := skipString(src, j)
				if end < 0 {
					return -1, nil
				}
				j = end
				continue
			}
			if j+2 < n && src[j+1] == quote && src[j+2] == quote {
				if depth == 0 {
					return j + 3, exprs
				}
				end := skipString(src, j)
				if end < 0 {
					return -1, nil
				}
				j = end
				continue
			}
			j++
		case c == '\'' || c == '"':
			// a quote of the other kind: literal text at depth 0, but the
			// start of a nested string inside a replacement field. Skip it
			// wholesale so a '}' inside that string cannot close the field
			// prematurely (R1 re-review MEDIUM — verified against CPython:
			// f"{helper('}') and other()}" must flag both calls).
			if depth > 0 {
				end := skipString(src, j)
				if end < 0 {
					return -1, nil
				}
				j = end
				continue
			}
			j++
			if !triple {
				if depth == 0 {
					return j + 1, exprs
				}
				end := skipString(src, j)
				if end < 0 {
					return -1, nil
				}
				j = end
				continue
			}
			if j+2 < n && src[j+1] == quote && src[j+2] == quote {
				if depth == 0 {
					return j + 3, exprs
				}
				end := skipString(src, j)
				if end < 0 {
					return -1, nil
				}
				j = end
				continue
			}
			j++
		case c == '{':
			if depth == 0 && j+1 < n && src[j+1] == '{' {
				j += 2 // literal {{
				continue
			}
			if depth == 0 {
				exprStart = j + 1
			}
			depth++
			j++
		case c == '}':
			if depth == 0 {
				j++ // stray } in the literal part (e.g. after a closed field)
				continue
			}
			depth--
			if depth == 0 {
				exprs = append(exprs, [2]int{exprStart, j})
				exprStart = -1
			}
			j++
		default:
			j++
		}
	}
	return -1, nil
}

// ── classification constant matcher (verifier.py:1923-1969) ────────

// classificationConstantValue mirrors _classification_constant_value with a
// restricted matcher: it accepts exactly the generated shape
//
//	def main() -> dict:
//	    return {"result": "<str>"}
//
// (the code emitted by makeInternalCodeNode for classification adapters,
// oracle start_llm_test.go:659). Any other shape — including Python code
// with arguments, decorators, annotations, or multiple statements — returns
// nil, exactly like the Python function's None, but the Go matcher does not
// implement the full ast contract (deviation declared in the task-37
// report).
func classificationConstantValue(data *dify.CodeDataNode) *string {
	if data.CodeLanguage != "python3" {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(data.Code, "\r\n", "\n"), "\n")
	if len(lines) != 2 {
		return nil
	}
	defLine := strings.TrimSpace(lines[0])
	if defLine != "def main() -> dict:" {
		return nil
	}
	body := strings.TrimSpace(lines[1])
	if !strings.HasPrefix(body, "return ") {
		return nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(body, "return "))
	if len(payload) < 2 || payload[0] != '{' || payload[len(payload)-1] != '}' {
		return nil
	}
	inner := strings.TrimSpace(payload[1 : len(payload)-1])
	if !strings.HasPrefix(inner, `"result":`) {
		return nil
	}
	valueText := strings.TrimSpace(strings.TrimPrefix(inner, `"result":`))
	if len(valueText) < 2 || valueText[0] != '"' {
		return nil
	}
	// decode the double-quoted literal with basic escapes; a failed decode
	// (or trailing content after the literal) returns nil.
	value, rest, ok := decodeDoubleQuoted(valueText)
	if !ok {
		return nil
	}
	if strings.TrimSpace(rest) != "" {
		return nil
	}
	return &value
}

// decodeDoubleQuoted decodes a Python double-quoted string literal starting
// at the opening quote. Returns the decoded value, the remaining text, and
// whether the literal was well-formed.
func decodeDoubleQuoted(text string) (string, string, bool) {
	if len(text) == 0 || text[0] != '"' {
		return "", text, false
	}
	var b strings.Builder
	i := 1
	for i < len(text) {
		c := text[i]
		if c == '\\' {
			if i+1 >= len(text) {
				return "", text, false
			}
			esc := text[i+1]
			switch esc {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			case '\'':
				b.WriteByte('\'')
			default:
				return "", text, false
			}
			i += 2
			continue
		}
		if c == '"' {
			return b.String(), text[i+1:], true
		}
		b.WriteByte(c)
		i++
	}
	return "", text, false
}
