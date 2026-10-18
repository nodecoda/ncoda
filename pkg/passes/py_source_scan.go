package passes

// py_source_scan.go — restricted Python source scanner (Task 35).
//
// Ports the parameter/return-key contract face of
// src/nclang/lang/passes/code_analysis.py's source-validation segment
// (:84-157 and helpers :282-309) without a Python parser: the Go compiler
// has zero external dependencies, so the embedded Python source of a Code
// template cannot be handed to the standard-library ast module. This file
// implements a restricted scanner over the joined literal source text that
// reproduces the reference's observable behavior for the contract checks:
//
//   - top-level `main` detection (`def main(` / `async def main(` at column 0)
//   - visible parameter names (posonlyargs + args + kwonlyargs, source
//     order) with annotations and defaults stripped, plus the *args /
//     **kwargs flags (vararg/kwarg → visible inputs become None, exactly
//     like _parameter_names + code_analysis.py:121-122)
//   - every `return` in the main body — descending into if/for/while/try
//     blocks and inline suites, skipping nested def/async def/class bodies
//     (mirrors _function_returns :287-299) — each classified as a
//     constant-string-key dict literal (multi-line dicts, parenthesized
//     dicts, adjacent-string key concatenation, empty dict → provable) or
//     unprovable (mirrors _literal_dict_keys :301-309)
//   - scanner-recognized structural Python errors with CPython 3.12's
//     verbatim SyntaxError.msg: unclosed brackets ("'(' was never closed",
//     "'[' was never closed", "'{' was never closed"), `def main(:` and
//     other malformed parameter lists ("invalid syntax"), a missing header
//     colon ("expected ':'"), a bare `*` with no kwonly argument ("named
//     arguments must follow bare *"), a missing indented body ("expected an
//     indented block after function definition on line 1"), and
//     unterminated string literals.
//
// E1028 deviation (declared in the task-35 report): this is NOT a full
// Python parser. Only the structural errors above are recognized; any other
// syntax error (malformed expressions, bad operators, wrong string
// prefixes, indentation pathologies, ...) passes through to the contract
// checks where Python would have rejected the source with E1028. The
// "no main" structural check and the parameter/return-key contract checks
// are faithful to the reference on the corpus and the boundary tests.
//
// Determinism: pure text scanning — same input, same output.

import (
	"fmt"
	"strings"
)

// pyReturn records one return statement in the scanned main body. keys is
// nil when the return value is not a statically-provable dict literal with
// all-constant string keys (mirrors _literal_dict_keys); an empty non-nil
// slice means `return {}`.
type pyReturn struct {
	keys []string
}

// pySourceScan is the result of scanning one joined literal Code source.
type pySourceScan struct {
	syntaxError string // scanner-recognized structural error (CPython msg); "" = none
	mainFound   bool
	params      []string // visible parameter names in source order (excludes *args/**kwargs)
	vararg      bool     // *args present → visible inputs are None
	kwarg       bool     // **kwargs present → visible inputs are None
	returns     []pyReturn
	// paramKeys 是 coze 代码节点 args 惯例的输入取用键集合：source 里
	// `params['x']` / `params.get('x')` / `args.params['x']` 等形态取用
	// 的键名（去重、保持首次出现顺序）。声明输入能被这些键覆盖时，
	// main(args) 单参 + params 取用即平台惯例（非契约错误），消除
	// CODE_SOURCE_CONTRACT_MISMATCH 的误报。
	paramKeys []string
}

// pyCodeLine is one physical line of the scanned source: the raw text, the
// code view (strings and comments blanked with spaces — same length, so
// columns line up with the raw text), the leading-whitespace indent, and
// the bracket depth at line start/end (continuation lines have open > 0).
type pyCodeLine struct {
	raw    string
	code   string
	indent int
	open   int
	depth  int
}

// pyTok is one token of the raw source (or of a statement substring).
type pyTok struct {
	kind      byte   // 'i' ident, 'n' number, 's' string, 'o' op
	text      string // ident/number text, decoded string value, or op text
	strPrefix string // string prefix for 's' tokens (f/b/r/u combos), "" plain
	op        string // operator text for 'o' tokens (single char, "->", "**")
	line      int    // 0-based physical line (full-source lexing only)
	col       int    // 0-based column in the line
}

func (t pyTok) isIdent(s string) bool { return t.kind == 'i' && t.text == s }

func isIdentChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isOpenOp(op string) bool  { return op == "(" || op == "[" || op == "{" }
func isCloseOp(op string) bool { return op == ")" || op == "]" || op == "}" }

// scanPythonSource scans one joined literal Code source template (the
// reference's "".join(literal parts), code_analysis.py:85) and returns the
// contract-relevant facts plus any scanner-recognized structural error.
func scanPythonSource(src string) pySourceScan {
	lines, toks, lexErr, _, unclosedTop := lexPyLines(src)
	if lexErr != "" {
		return pySourceScan{syntaxError: lexErr}
	}
	// unclosedBracket maps the innermost still-open opener to CPython's
	// "never closed" message; consulted only after the main signature is
	// known to parse, mirroring the parser's error priority (a malformed
	// `def main(` reports its own "'(' was never closed" first).
	unclosedBracket := func() string {
		switch unclosedTop {
		case '(':
			return "'(' was never closed"
		case '[':
			return "'[' was never closed"
		case '{':
			return "'{' was never closed"
		}
		return ""
	}
	// First token index per line (for statement dispatch).
	firstTok := make([]int, len(lines))
	for i := range firstTok {
		firstTok[i] = -1
	}
	for i, t := range toks {
		if t.line >= 0 && t.line < len(firstTok) && firstTok[t.line] == -1 {
			firstTok[t.line] = i
		}
	}

	// Find the top-level main definition: a statement at column 0 whose
	// first token is `def main(` or `async def main(`.
	parenIdx := -1
	defLine := 0 // 1-based line of the `def main(` header — CPython reports it in the no-body error
	for i, ln := range lines {
		if ln.open != 0 || ln.indent != 0 {
			continue
		}
		k := firstTok[i]
		if k < 0 {
			continue
		}
		if p, ok := matchMainDef(toks, k); ok {
			parenIdx = p
			defLine = i + 1
			break
		}
	}
	if parenIdx == -1 {
		if msg := unclosedBracket(); msg != "" {
			return pySourceScan{syntaxError: msg}
		}
		return pySourceScan{mainFound: false}
	}

	params, vararg, kwarg, sigErr, colonLine, colonCol := parseMainSignature(toks, parenIdx)
	if sigErr != "" {
		return pySourceScan{syntaxError: sigErr}
	}
	if msg := unclosedBracket(); msg != "" {
		return pySourceScan{syntaxError: msg}
	}

	// Inline suite on the header line (e.g. `def main(): return {...}`).
	inlineRaw := ""
	if colonLine >= 0 && colonLine < len(lines) {
		line := lines[colonLine]
		if colonCol+1 <= len(line.raw) {
			inlineRaw = line.raw[colonCol+1:]
		}
	}

	var returns []pyReturn
	if strings.TrimSpace(inlineRaw) != "" {
		returns = append(returns, scanInlineReturns(inlineRaw)...)
	}

	// Locate the first body statement: the next statement line after the
	// header must be indented, else the function has no body (CPython:
	// "expected an indented block after function definition on line 1").
	bodyStart := -1
	if strings.TrimSpace(inlineRaw) == "" {
		j := colonLine + 1
		for j < len(lines) && (lines[j].open != 0 || strings.TrimSpace(lines[j].code) == "") {
			j++
		}
		if j >= len(lines) || lines[j].indent <= 0 {
			return pySourceScan{syntaxError: fmt.Sprintf("expected an indented block after function definition on line %d", defLine)}
		}
		bodyStart = j
	}
	returns = append(returns, collectMainReturns(lines, bodyStart)...)

	return pySourceScan{mainFound: true, params: params, vararg: vararg, kwarg: kwarg, returns: returns,
		paramKeys: collectParamsKeys(toks, bodyStart)}
}

// collectParamsKeys scans the token stream for the coze code-node args
// convention: `params['key']`, `params.get('key')`, or `args.params['key']`
// input access (the platform injects every workflow input into a single
// `args` object; code pulls values by name). Returns the accessed key names
// in first-seen order, deduplicated. Restricts to the main body when
// bodyStart >= 0 (inline-suite main has no separate body); without a body
// (syntax-error path) it conservatively scans the whole stream.
func collectParamsKeys(toks []pyTok, bodyStart int) []string {
	seen := map[string]bool{}
	var out []string
	add := func(k string) {
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	// Determine the token range: main body starts at the first statement
	// token of the body line; tokens before it belong to the header/other
	// top-level statements (excluded when a body is known).
	start := 0
	if bodyStart >= 0 {
		for i, t := range toks {
			if t.line >= bodyStart {
				start = i
				break
			}
		}
	}
	for i := start; i < len(toks); i++ {
		// `params` ident (or `args.params` member chain) followed by a
		// subscript `['key']` or `.get('key')`.
		if !toks[i].isIdent("params") {
			continue
		}
		j := i + 1
		// allow `args.params` (member access before the subscript) — the
		// `params` token itself is what we matched; the subscript follows.
		if j >= len(toks) {
			continue
		}
		if toks[j].kind == 'o' && toks[j].op == "[" {
			// params['key']
			k := j + 1
			if k < len(toks) && toks[k].kind == 's' {
				add(toks[k].text)
			}
			continue
		}
		if toks[j].kind == 'o' && toks[j].op == "." {
			// params.get('key', ...)
			if j+2 < len(toks) && toks[j+1].isIdent("get") &&
				toks[j+2].kind == 'o' && toks[j+2].op == "(" {
				k := j + 3
				if k < len(toks) && toks[k].kind == 's' {
					add(toks[k].text)
				}
			}
		}
	}
	return out
}

// matchMainDef reports whether the statement starting at token k is
// `def main(` or `async def main(`, and returns the index of the opening
// paren token.
func matchMainDef(toks []pyTok, k int) (parenIdx int, ok bool) {
	j := k
	if j < len(toks) && toks[j].isIdent("async") {
		j++
	}
	if j >= len(toks) || !toks[j].isIdent("def") {
		return 0, false
	}
	j++
	if j >= len(toks) || !toks[j].isIdent("main") {
		return 0, false
	}
	j++
	if j >= len(toks) || !(toks[j].kind == 'o' && toks[j].op == "(") {
		return 0, false
	}
	return j, true
}

// parseMainSignature parses the parameter list starting at the opening
// paren token parenIdx. Returns the visible parameter names (posonly+args+
// kwonly, source order), the vararg/kwarg flags, a scanner-recognized
// structural error ("" when none), and the position of the header colon
// (the last token of the header).
func parseMainSignature(toks []pyTok, parenIdx int) (params []string, vararg, kwarg bool, errMsg string, colonLine, colonCol int) {
	depth := 1
	i := parenIdx + 1
	atParamStart := true
	prevSlash := false
	bareStar := false
	afterBareStar := false
	kwargSeen := false
	colonLine, colonCol = -1, -1

	// skipUntilParamEnd consumes the annotation/default of a parameter
	// (bracket-aware) up to the next top-level `,` or `)`; the terminating
	// token is left for the caller.
	skipUntilParamEnd := func() {
		for i < len(toks) {
			t := toks[i]
			if t.kind == 'o' {
				switch t.op {
				case "(", "[", "{":
					depth++
				case ",", ")", "]", "}":
					if depth == 1 {
						return // the parameter-list separator/close — leave for the caller
					}
					if isCloseOp(t.op) {
						depth--
						if depth < 0 {
							return
						}
					}
				}
			}
			i++
		}
	}

	for i < len(toks) {
		t := toks[i]
		if t.kind == 'o' {
			switch t.op {
			case "(", "[", "{":
				depth++
				i++
				continue
			case ")", "]", "}":
				depth--
				if depth == 0 {
					if bareStar {
						return nil, false, false, "named arguments must follow bare *", -1, -1
					}
					i++
					goto afterParen
				}
				if depth < 0 {
					return nil, false, false, "invalid syntax", -1, -1
				}
				i++
				continue
			}
		}
		if depth > 1 {
			i++
			continue
		}
		// depth == 1: parameter-list level
		switch {
		case t.kind == 'o' && t.op == ",":
			if atParamStart && !prevSlash && !afterBareStar {
				return nil, false, false, "invalid syntax", -1, -1
			}
			atParamStart = true
			prevSlash = false
			afterBareStar = false
			i++
		case t.kind == 'o' && t.op == ":":
			if atParamStart {
				return nil, false, false, "invalid syntax", -1, -1
			}
			// annotation after a name/vararg/kwarg: skip to the next
			// top-level `,` or `)`
			i++
			skipUntilParamEnd()
		case t.kind == 'o' && t.op == "*":
			if !atParamStart {
				return nil, false, false, "invalid syntax", -1, -1
			}
			if i+1 >= len(toks) {
				return nil, false, false, "invalid syntax", -1, -1
			}
			nxt := toks[i+1]
			if nxt.kind == 'o' && nxt.op == "**" {
				return nil, false, false, "invalid syntax", -1, -1
			}
			if nxt.kind == 'i' {
				vararg = true
				atParamStart = false
				i += 2
				continue
			}
			if nxt.kind == 'o' && (nxt.op == "," || nxt.op == ")") {
				// bare `*` (kwonly separator): legal only when a kwonly
				// argument follows — validated when the list closes
				bareStar = true
				afterBareStar = true
				atParamStart = true
				i++
				continue
			}
			return nil, false, false, "invalid syntax", -1, -1
		case t.kind == 'o' && t.op == "**":
			if !atParamStart {
				return nil, false, false, "invalid syntax", -1, -1
			}
			if i+1 >= len(toks) || toks[i+1].kind != 'i' {
				return nil, false, false, "invalid syntax", -1, -1
			}
			kwarg = true
			kwargSeen = true
			atParamStart = false
			i += 2
			continue
		case t.kind == 'o' && t.op == "/":
			if !atParamStart {
				return nil, false, false, "invalid syntax", -1, -1
			}
			if i+1 >= len(toks) {
				return nil, false, false, "invalid syntax", -1, -1
			}
			nxt := toks[i+1]
			if nxt.kind == 'o' && nxt.op == "*" {
				// CPython: "expected comma between / and *"
				return nil, false, false, "expected comma between / and *", -1, -1
			}
			if !(nxt.kind == 'o' && (nxt.op == "," || nxt.op == ")")) {
				return nil, false, false, "invalid syntax", -1, -1
			}
			prevSlash = true
			i++
		case t.kind == 'i':
			if !atParamStart {
				return nil, false, false, "invalid syntax", -1, -1
			}
			if kwargSeen {
				return nil, false, false, "arguments cannot follow var-keyword argument", -1, -1
			}
			if bareStar {
				bareStar = false // a kwonly argument followed the bare *
			}
			params = append(params, t.text)
			atParamStart = false
			i++
			skipUntilParamEnd()
		default:
			return nil, false, false, "invalid syntax", -1, -1
		}
	}
	return nil, false, false, "'(' was never closed", -1, -1

afterParen:
	// After the closing paren: an optional `->` return annotation, then the
	// mandatory colon. Anything else (e.g. `def main() int:`) is the same
	// "expected ':'" error CPython reports.
	if i < len(toks) && toks[i].kind == 'o' && toks[i].op == "->" {
		i++
		for i < len(toks) {
			t := toks[i]
			if t.kind == 'o' {
				switch t.op {
				case "(", "[", "{":
					depth++
				case ")", "]", "}":
					depth--
					if depth < 0 {
						return nil, false, false, "invalid syntax", -1, -1
					}
				case ":":
					if depth == 0 {
						goto colonFound
					}
				}
			}
			i++
		}
		return nil, false, false, "expected ':'", -1, -1
	}
	if i < len(toks) && toks[i].kind == 'o' && toks[i].op == ":" {
		goto colonFound
	}
	return nil, false, false, "expected ':'", -1, -1

colonFound:
	colonLine, colonCol = toks[i].line, toks[i].col
	return params, vararg, kwarg, "", colonLine, colonCol
}

// collectMainReturns scans the main body (statement lines after the header
// with indent > 0, up to the next module-level statement) and returns every
// `return` — descending into compound statement blocks, skipping nested
// def/class bodies and match case blocks, mirroring _function_returns
// (code_analysis.py:287-299). bodyStart < 0 means the body is the inline
// suite only.
func collectMainReturns(lines []pyCodeLine, bodyStart int) []pyReturn {
	var out []pyReturn
	if bodyStart < 0 {
		return out
	}
	i := bodyStart
	for i < len(lines) {
		ln := lines[i]
		code := strings.TrimSpace(ln.code)
		if code == "" {
			i++
			continue
		}
		if isMatchStart(ln) {
			// Python ast: match_case nodes are not stmt children, so
			// _function_returns never descends into case bodies — returns
			// there are not collected. Skip the whole match block (a
			// multiline header like `match (` ... `):` keeps its closing
			// terminator at the opener's indent, so consume it too).
			j := skipIndentedBlock(lines, i, ln.indent)
			if j < len(lines) && isBracketCloseTerminator(lines[j]) {
				j++
				j = skipIndentedBlock(lines, j, ln.indent)
			}
			i = j
			continue
		}
		if ln.open != 0 {
			i++
			continue
		}
		if ln.indent <= 0 {
			break // module-level statement: main body ended
		}
		if isNestedDefOrClass(code) {
			i = skipIndentedBlock(lines, i, ln.indent)
			continue
		}
		raw, consumed := statementRaw(lines, i)
		if colon := topLevelColonPos(ln.code); colon >= 0 {
			// Compound-statement header (`if x: return ...`): scan the
			// trailing inline suite after the first top-level colon — it
			// covers `;`-separated returns too. A valid Python statement
			// cannot place a return before the header's colon (compound
			// statements cannot follow `;`), so the pre-colon part is
			// scanned only when there is no top-level colon.
			if rest := strings.TrimSpace(ln.raw[colon+1:]); rest != "" {
				out = append(out, scanInlineReturns(rest)...)
			}
		} else {
			// Plain statement: scan for `;`-separated return statements —
			// covers the return-start form and non-return starts
			// (`x = 1; return {...}`, `return {...}; return {...}`).
			// Comments and string contents are already excluded by the
			// lexer.
			out = append(out, scanInlineReturns(raw)...)
		}
		i += consumed
	}
	return out
}

// skipIndentedBlock returns the index just past the block opened at line i
// (every following line with a greater indent than the opener, plus any
// continuation line that starts inside open brackets).
func skipIndentedBlock(lines []pyCodeLine, i, openerIndent int) int {
	j := i + 1
	for j < len(lines) && (lines[j].indent > openerIndent || lines[j].open != 0) {
		j++
	}
	return j
}

// isBracketCloseTerminator reports whether the line merely closes the
// brackets of a multiline header (only closing brackets plus an optional
// trailing colon), e.g. the `):` line of `match (` ... `):`.
func isBracketCloseTerminator(ln pyCodeLine) bool {
	if ln.open != 0 {
		return false
	}
	code := strings.TrimSpace(ln.code)
	if strings.HasSuffix(code, ":") {
		code = strings.TrimSpace(code[:len(code)-1])
	}
	if code == "" {
		return true
	}
	for i := 0; i < len(code); i++ {
		if code[i] != ')' && code[i] != ']' && code[i] != '}' {
			return false
		}
	}
	return true
}

// isMatchStart reports whether the statement is a `match` compound
// statement opener (Python 3.10+ structural pattern matching). `match` is
// a soft keyword: statement position `match <subject>:` with whitespace
// after the keyword. The header may span continuation lines (`match (`
// ... `):`), so the opener is also accepted while a bracket stays open.
func isMatchStart(ln pyCodeLine) bool {
	code := strings.TrimSpace(ln.code)
	if !strings.HasPrefix(code, "match ") && !strings.HasPrefix(code, "match\t") {
		return false
	}
	return topLevelColonPos(code) >= 0 || endsWithOpenBracket(code)
}

// endsWithOpenBracket reports whether the line's code view ends with an
// unbalanced open bracket (a multiline match header keeps it open).
func endsWithOpenBracket(code string) bool {
	depth := 0
	for i := 0; i < len(code); i++ {
		switch code[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		}
	}
	return depth > 0
}

func isNestedDefOrClass(code string) bool {
	rest := ""
	switch {
	case strings.HasPrefix(code, "def"):
		rest = code[len("def"):]
	case strings.HasPrefix(code, "class"):
		rest = code[len("class"):]
	case strings.HasPrefix(code, "async"):
		rest = strings.TrimLeft(code[len("async"):], " \t")
		if !strings.HasPrefix(rest, "def") {
			return false
		}
		rest = rest[len("def"):]
	default:
		return false
	}
	return rest == "" || rest[0] == ' ' || rest[0] == '\t'
}

// statementRaw returns the raw text of the statement whose first physical
// line is lines[i], spanning continuation lines while brackets stay open,
// and the number of physical lines consumed.
func statementRaw(lines []pyCodeLine, i int) (string, int) {
	var b strings.Builder
	b.WriteString(lines[i].raw)
	depth := lines[i].depth
	n := 1
	for depth > 0 && i+n < len(lines) {
		b.WriteByte('\n')
		b.WriteString(lines[i+n].raw)
		depth = lines[i+n].depth
		n++
	}
	return b.String(), n
}

// topLevelColonPos returns the column of the first `:` at bracket depth 0
// in a line's code view, or -1 when there is none. (The code view blanks
// strings and comments, so only real structure remains.)
func topLevelColonPos(code string) int {
	depth := 0
	for i := 0; i < len(code); i++ {
		switch code[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ':':
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// scanInlineReturns scans a single-line inline suite (the trailing raw text
// after a header colon) for `return` statements, mirroring how Python's
// ast places inline-suite statements in the parent block.
func scanInlineReturns(raw string) []pyReturn {
	toks := lexPyTokens(raw)
	var out []pyReturn
	i := 0
	stmtStart := true
	for i < len(toks) {
		t := toks[i]
		if t.kind == 'o' && t.op == ";" {
			stmtStart = true
			i++
			continue
		}
		if stmtStart && t.isIdent("return") {
			depth := 0
			j := i + 1
			for j < len(toks) {
				tj := toks[j]
				if tj.kind == 'o' {
					if isOpenOp(tj.op) {
						depth++
					} else if isCloseOp(tj.op) {
						depth--
						if depth < 0 {
							break
						}
					} else if tj.op == ";" && depth == 0 {
						break
					}
				}
				j++
			}
			out = append(out, pyReturn{keys: dictKeys(toks[i+1 : j])})
			i = j
			stmtStart = true
			continue
		}
		stmtStart = false
		i++
	}
	return out
}

// dictKeys returns the all-constant-string keys of a dict literal whose
// token stream starts at index 0, or nil when the expression is not such a
// dict (mirrors _literal_dict_keys, code_analysis.py:301-309). Balanced
// outer parentheses are unwrapped (a depth-1 comma inside them means a
// tuple → nil); trailing `;` statement separators are ignored.
func dictKeys(toks []pyTok) []string {
	// unwrap balanced outer parens (a depth-1 comma inside them means a
	// tuple → unprovable); the closing paren is excluded from the segment
	for len(toks) > 0 && toks[0].kind == 'o' && toks[0].op == "(" {
		depth := 0
		closeIdx := -1
		comma := false
		for j := 0; j < len(toks); j++ {
			tj := toks[j]
			if tj.kind != 'o' {
				continue
			}
			if isOpenOp(tj.op) {
				depth++
			} else if isCloseOp(tj.op) {
				depth--
				if depth == 0 {
					closeIdx = j
					break
				}
			} else if tj.op == "," && depth == 1 {
				comma = true
			}
		}
		if closeIdx < 0 || comma {
			return nil // unclosed or a tuple
		}
		if !semicolonTail(toks, closeIdx+1) {
			return nil
		}
		toks = toks[1:closeIdx]
	}
	if len(toks) == 0 || !(toks[0].kind == 'o' && toks[0].op == "{") {
		return nil
	}
	// the dict must be the whole remaining expression (up to a `;`)
	depth := 0
	closeIdx := -1
	for j := 0; j < len(toks); j++ {
		tj := toks[j]
		if tj.kind != 'o' {
			continue
		}
		if isOpenOp(tj.op) {
			depth++
		} else if isCloseOp(tj.op) {
			depth--
			if depth == 0 {
				closeIdx = j
				break
			}
		}
	}
	if closeIdx < 0 || !semicolonTail(toks, closeIdx+1) {
		return nil
	}
	return dictKeysAt(toks[:closeIdx+1], 0)
}

// semicolonTail reports whether the tokens from `from` are empty or start
// with a `;` statement separator (Python: `return {...}; x = 2` still has
// the dict as the return value).
func semicolonTail(toks []pyTok, from int) bool {
	if from >= len(toks) {
		return true
	}
	return toks[from].kind == 'o' && toks[from].op == ";"
}

// dictKeysAt extracts the keys of the dict literal whose opening `{` is
// toks[start]. Returns nil when any key is not a plain (non-f/non-b)
// constant string or the literal is not a well-formed dict.
func dictKeysAt(toks []pyTok, start int) []string {
	depth := 1 // the opening brace at toks[start] is already consumed
	entryStart := start + 1
	var keys []string
	for j := start + 1; j < len(toks); j++ {
		t := toks[j]
		if t.kind != 'o' {
			continue
		}
		switch t.op {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
			if depth == 0 {
				// closing brace of the dict literal
				if !semicolonTail(toks, j+1) {
					return nil // trailing junk: not a bare dict value
				}
				if j > entryStart {
					key, ok := dictEntryKey(toks, entryStart, j)
					if !ok {
						return nil
					}
					keys = append(keys, key)
				}
				if keys == nil {
					return []string{} // `return {}` — provable, zero keys (Python [])
				}
				return keys
			}
			if depth < 0 {
				return nil
			}
		case ",":
			if depth == 1 {
				if j == entryStart {
					return nil // empty entry
				}
				key, ok := dictEntryKey(toks, entryStart, j)
				if !ok {
					return nil
				}
				keys = append(keys, key)
				entryStart = j + 1
			}
		}
	}
	return nil // unclosed dict
}

// dictEntryKey returns the constant-string key of one dict entry
// (toks[from:to], the tokens before the depth-0 comma or closing brace).
// Adjacent plain string literals concatenate (Python implicit string
// concatenation); f-strings and bytes keys are not str Constants → not ok.
func dictEntryKey(toks []pyTok, from, to int) (string, bool) {
	if from >= to {
		return "", false
	}
	if toks[from].kind == 'o' && toks[from].op == "**" {
		return "", false // {**other} unpacking
	}
	var b strings.Builder
	i := from
	for i < to && toks[i].kind == 's' && isPlainStrPrefix(toks[i].strPrefix) {
		b.WriteString(toks[i].text)
		i++
	}
	if i == from {
		return "", false // no string literal at the entry start
	}
	if i >= to || !(toks[i].kind == 'o' && toks[i].op == ":") {
		return "", false // set literal / computed key / missing colon
	}
	return b.String(), true
}

// isPlainStrPrefix reports whether a string prefix yields a str Constant
// ("" / r / u — and case variants). f → JoinedStr, b → bytes: not str.
func isPlainStrPrefix(p string) bool {
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case 'r', 'u', 'R', 'U':
		default:
			return false
		}
	}
	return true
}

// lexPyLines scans the full source into per-line code views and a token
// stream, and recognizes unterminated string literals (CPython 3.12
// messages, verbatim).
func lexPyLines(src string) ([]pyCodeLine, []pyTok, string, int, byte) {
	var lines []pyCodeLine
	var toks []pyTok
	var codeBuf, rawBuf strings.Builder
	indent := 0
	indentDone := false
	depth := 0
	openDepth := 0
	lineIdx := 0
	col := 0

	state := byte(0) // 0 normal, 's' single, 'd' double, 'S' triple-single, 'D' triple-double
	strPrefix := ""
	strStartLine := 0
	var strBuf strings.Builder
	var bracketStack []byte

	flushLine := func() {
		lines = append(lines, pyCodeLine{
			raw:    rawBuf.String(),
			code:   codeBuf.String(),
			indent: indent,
			open:   openDepth,
			depth:  depth,
		})
		codeBuf.Reset()
		rawBuf.Reset()
		indent = 0
		indentDone = false
		openDepth = depth
		lineIdx++
		col = 0
	}

	emitTok := func(kind byte, text, prefix, op string) {
		toks = append(toks, pyTok{kind: kind, text: text, strPrefix: prefix, op: op, line: lineIdx, col: col})
	}

	finishString := func(value string) {
		emitTok('s', value, strPrefix, "")
		strPrefix = ""
		state = 0
	}

	i := 0
	n := len(src)
	for i < n {
		ch := src[i]
		switch state {
		case 's', 'd':
			// single/double-quoted string
			rawBuf.WriteByte(ch)
			codeBuf.WriteByte(' ')
			col++
			if ch == '\\' && i+1 < n {
				// escape: consume the escaped char (may be a newline)
				rawBuf.WriteByte(src[i+1])
				codeBuf.WriteByte(' ')
				if src[i+1] == '\n' {
					// line continuation inside a string: flush the line,
					// the string state persists
					flushLine()
				}
				col++
				i += 2
				continue
			}
			if ch == '\n' {
				return lines, toks, "unterminated string literal (detected at line " + itoa(strStartLine+1) + ")", strStartLine, 0
			}
			if (state == 's' && ch == '\'') || (state == 'd' && ch == '"') {
				finishString(strBuf.String())
				strBuf.Reset()
				i++
				continue
			}
			strBuf.WriteByte(ch)
			i++
			continue
		case 'S', 'D':
			// triple-quoted string
			rawBuf.WriteByte(ch)
			codeBuf.WriteByte(' ')
			col++
			if ch == '\\' && i+1 < n {
				rawBuf.WriteByte(src[i+1])
				codeBuf.WriteByte(' ')
				if src[i+1] == '\n' {
					strBuf.WriteByte('\n')
					flushLine()
				} else {
					strBuf.WriteByte('\\')
					strBuf.WriteByte(src[i+1])
				}
				col++
				i += 2
				continue
			}
			if ch == '\n' {
				strBuf.WriteByte('\n')
				rawBuf.WriteByte(ch) // already written above
				flushLine()
				i++
				continue
			}
			// closing triple quote?
			if i+2 < n && src[i] == src[i+1] && src[i+1] == src[i+2] &&
				((state == 'S' && ch == '\'') || (state == 'D' && ch == '"')) {
				// consume the three quote chars (already blanked one)
				rawBuf.WriteByte(src[i+1])
				rawBuf.WriteByte(src[i+2])
				codeBuf.WriteByte(' ')
				codeBuf.WriteByte(' ')
				col += 2
				finishString(strBuf.String())
				strBuf.Reset()
				i += 3
				continue
			}
			strBuf.WriteByte(ch)
			i++
			continue
		}
		// normal state
		if !indentDone {
			if ch == ' ' || ch == '\t' {
				indent++
				rawBuf.WriteByte(ch)
				codeBuf.WriteByte(ch)
				i++
				continue
			}
			indentDone = true
		}
		if ch == '\n' {
			flushLine()
			i++
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\r' {
			// mid-line whitespace: kept in both views, no token
			rawBuf.WriteByte(ch)
			codeBuf.WriteByte(ch)
			col++
			i++
			continue
		}
		if ch == '#' {
			// comment to end of line: blank it in the code view
			for i < n && src[i] != '\n' {
				rawBuf.WriteByte(src[i])
				codeBuf.WriteByte(' ')
				i++
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			// triple-quoted?
			if i+2 < n && src[i+1] == ch && src[i+2] == ch {
				state = 'S'
				if ch == '"' {
					state = 'D'
				}
				strStartLine = lineIdx
				rawBuf.WriteByte(ch)
				rawBuf.WriteByte(ch)
				rawBuf.WriteByte(ch)
				codeBuf.WriteByte(' ')
				codeBuf.WriteByte(' ')
				codeBuf.WriteByte(' ')
				col += 3
				i += 3
				continue
			}
			state = 's'
			if ch == '"' {
				state = 'd'
			}
			strStartLine = lineIdx
			rawBuf.WriteByte(ch)
			codeBuf.WriteByte(' ')
			col++
			i++
			continue
		}
		// identifiers (and string prefixes)
		if isIdentStart(ch) {
			start := i
			for i < n && isIdentChar(src[i]) {
				i++
			}
			word := src[start:i]
			rawBuf.WriteString(word)
			for k := 0; k < len(word); k++ {
				codeBuf.WriteByte(word[k])
			}
			// string prefix? contiguous alnum run directly before a quote
			if i < n && (src[i] == '\'' || src[i] == '"') {
				q := src[i]
				triple := i+2 < n && src[i+1] == q && src[i+2] == q
				rawBuf.WriteString(word)
				for k := 0; k < len(word); k++ {
					codeBuf.WriteByte(' ')
				}
				col += len(word)
				strPrefix = word
				quotes := 1
				if triple {
					quotes = 3
					state = 'S'
					if q == '"' {
						state = 'D'
					}
				} else {
					state = 's'
					if q == '"' {
						state = 'd'
					}
				}
				strStartLine = lineIdx
				for k := 0; k < quotes; k++ {
					rawBuf.WriteByte(q)
					codeBuf.WriteByte(' ')
				}
				col += quotes
				i += quotes
				continue
			}
			emitTok('i', word, "", "")
			col += len(word)
			continue
		}
		if ch >= '0' && ch <= '9' {
			start := i
			for i < n && (isIdentChar(src[i]) || src[i] == '.') {
				i++
			}
			word := src[start:i]
			emitTok('n', word, "", "")
			rawBuf.WriteString(word)
			codeBuf.WriteString(word)
			col += len(word)
			continue
		}
		// A backslash outside a string is only a legal line continuation
		// (CPython tokenizer: "unexpected character after line continuation
		// character" when followed by anything but a newline). Keep the raw
		// backslash, blank it in the code view, and let the '\n' branch flush
		// the joined logical line (bracket depth carries over unchanged).
		if ch == '\\' {
			if i+1 >= n || src[i+1] != '\n' {
				return lines, toks,
					"unexpected character after line continuation character", lineIdx, 0
			}
			rawBuf.WriteByte('\\')
			codeBuf.WriteByte(' ')
			col++
			i++
			continue
		}
		// operators
		if ch == '-' && i+1 < n && src[i+1] == '>' {
			emitTok('o', "->", "", "->")
			rawBuf.WriteByte(ch)
			rawBuf.WriteByte('>')
			codeBuf.WriteByte(ch)
			codeBuf.WriteByte('>')
			i += 2
			col += 2
			continue
		}
		if ch == '*' && i+1 < n && src[i+1] == '*' {
			emitTok('o', "**", "", "**")
			rawBuf.WriteByte(ch)
			rawBuf.WriteByte('*')
			codeBuf.WriteByte(ch)
			codeBuf.WriteByte('*')
			i += 2
			col += 2
			continue
		}
		switch ch {
		case '(', '[', '{':
			depth++
			bracketStack = append(bracketStack, ch)
			emitTok('o', string(ch), "", string(ch))
		case ')', ']', '}':
			depth--
			if depth < 0 {
				// unmatched close: Python "unmatched ')'" — recover by
				// treating the close as a normal token
				emitTok('o', string(ch), "", string(ch))
				depth = 0
			} else {
				if len(bracketStack) > 0 {
					bracketStack = bracketStack[:len(bracketStack)-1]
				}
				emitTok('o', string(ch), "", string(ch))
			}
		default:
			emitTok('o', string(ch), "", string(ch))
		}
		rawBuf.WriteByte(ch)
		codeBuf.WriteByte(ch)
		col++
		i++
	}
	flushLine()
	if state != 0 {
		if state == 'S' || state == 'D' {
			return lines, toks, "unterminated triple-quoted string literal (detected at line " + itoa(strStartLine+1) + ")", strStartLine, 0
		}
		return lines, toks, "unterminated string literal (detected at line " + itoa(strStartLine+1) + ")", strStartLine, 0
	}
	top := byte(0)
	if depth != 0 && len(bracketStack) > 0 {
		top = bracketStack[len(bracketStack)-1]
	}
	return lines, toks, "", 0, top
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for v > 0 {
		pos--
		buf[pos] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[pos:])
}

// lexPyTokens scans a statement substring into tokens (no line tracking,
// no structural errors; used for return values and inline suites). String
// literals are decoded with CPython-compatible escape handling.
func lexPyTokens(text string) []pyTok {
	var toks []pyTok
	i := 0
	n := len(text)
	for i < n {
		ch := text[i]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n':
			i++
		case ch == '#':
			for i < n && text[i] != '\n' {
				i++
			}
		case ch == '\'' || ch == '"':
			prefix := ""
			value, next, closed := lexString(text, i, prefix)
			if !closed {
				return toks
			}
			toks = append(toks, pyTok{kind: 's', text: value, strPrefix: prefix})
			i = next
		case isIdentStart(ch):
			start := i
			for i < n && isIdentChar(text[i]) {
				i++
			}
			word := text[start:i]
			if i < n && (text[i] == '\'' || text[i] == '"') {
				value, next, closed := lexString(text, i, word)
				if !closed {
					return toks
				}
				toks = append(toks, pyTok{kind: 's', text: value, strPrefix: word})
				i = next
				continue
			}
			toks = append(toks, pyTok{kind: 'i', text: word})
		case ch >= '0' && ch <= '9':
			start := i
			for i < n && (isIdentChar(text[i]) || text[i] == '.') {
				i++
			}
			toks = append(toks, pyTok{kind: 'n', text: text[start:i]})
		case ch == '-' && i+1 < n && text[i+1] == '>':
			toks = append(toks, pyTok{kind: 'o', op: "->"})
			i += 2
		case ch == '*' && i+1 < n && text[i+1] == '*':
			toks = append(toks, pyTok{kind: 'o', op: "**"})
			i += 2
		default:
			toks = append(toks, pyTok{kind: 'o', op: string(ch)})
			i++
		}
	}
	return toks
}

// lexString decodes a string literal whose opening quote is at text[i].
// Returns the decoded value, the index past the closing quote, and whether
// the literal closed.
func lexString(text string, i int, prefix string) (value string, next int, closed bool) {
	quote := text[i]
	triple := i+2 < len(text) && text[i+1] == quote && text[i+2] == quote
	start := i
	if triple {
		i += 3
	} else {
		i++
	}
	var b strings.Builder
	for i < len(text) {
		ch := text[i]
		if ch == '\\' && i+1 < len(text) {
			esc := text[i+1]
			switch esc {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case 'a':
				b.WriteByte('\a')
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'v':
				b.WriteByte('\v')
			case '\\', '\'', '"':
				b.WriteByte(esc)
			case 'x':
				if i+3 < len(text) {
					if hi, lo, ok := hexPair(text[i+2], text[i+3]); ok {
						b.WriteByte(hi<<4 | lo)
						i += 4
						continue
					}
				}
				b.WriteByte('\\')
				b.WriteByte('x')
				i += 2
				continue
			case 'u', 'U':
				digits := 4
				if esc == 'U' {
					digits = 8
				}
				if i+1+digits < len(text) {
					ok := true
					var r rune
					for k := 0; k < digits; k++ {
						d, good := hexDigit(text[i+2+k])
						if !good {
							ok = false
							break
						}
						r = r<<4 | rune(d)
					}
					if ok {
						b.WriteRune(r)
						i += 2 + digits
						continue
					}
				}
				b.WriteByte('\\')
				b.WriteByte(esc)
				i += 2
				continue
			default:
				b.WriteByte('\\')
				b.WriteByte(esc)
			}
			i += 2
			continue
		}
		if triple {
			if i+2 < len(text) && text[i] == quote && text[i+1] == quote && text[i+2] == quote {
				_ = start
				return b.String(), i + 3, true
			}
		} else if ch == quote {
			return b.String(), i + 1, true
		}
		b.WriteByte(ch)
		i++
	}
	return b.String(), i, false
}

func hexDigit(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

func hexPair(a, b byte) (byte, byte, bool) {
	ha, oka := hexDigit(a)
	hb, okb := hexDigit(b)
	if !oka || !okb {
		return 0, 0, false
	}
	return byte(ha), byte(hb), true
}
