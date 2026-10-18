// Command rewrite-calls migrates legacy tool("p","op",{...}) call sites to
// the qualified namespace-call form p.op({...}) (TOOL-DIRECT-CALL-FORM,
// Task 4). It is a span-surgery codemod over the original source bytes:
// only the callee and the dropped argument region change, so formatting,
// comments and whitespace everywhere else survive byte-for-byte.
//
// The parser/linter spans are RUNE offsets (Python str-indexing semantics,
// matching lexer.go's []rune source), so every edit position is converted
// to byte offsets before slicing — sources whose comments contain
// multi-byte characters (CJK; the X13/06 corpus shape) would corrupt
// otherwise.
//
// Transform rules (decision 3, brief Step 3):
//   - tool("p","op",{params}) -> p.op({params})           (string provider)
//   - tool(CONST,"op",{...})  -> <constvalue>.op({...})    (const folded via
//     the const table; the const declaration itself is deleted by the
//     file-level migrateSource transform only when the const is used
//     exclusively as a tool provider — decision 3 forbids const providers)
//   - tool(p, {params}) two-arg form -> error (no direct-call analog;
//     0 occurrences in the corpus; dies with tool() in Task 5)
//
// Usage:
//
//	rewrite-calls file.ncoda...      rewrite in place
//	rewrite-calls --check file...    exit 1 if any tool( call remains
//	rewrite-calls --stdin            read one source from stdin, write the
//	                                 rewritten source to stdout
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/lexer"
	"github.com/nodecoda/ncoda/pkg/parser"
	"github.com/nodecoda/ncoda/pkg/token"
)

// edit is one byte-range replacement on the original source.
type edit struct {
	start, end int // byte offsets (end exclusive)
	repl       string
}

// toolSite captures one legacy tool(...) call's surgery positions (rune
// offsets from the lexer/parser) and its resolved identity.
type toolSite struct {
	callStart   int // rune offset of the callee identifier
	parenEnd    int // rune offset just past the '('
	arg0Start   int // rune offset of args[0] start
	arg2Start   int // rune offset of the first token after args[1]'s comma
	provider    string
	operation   string
	arg0IsConst bool
	span        ast.Span
}

// constTable builds the compile-time const table from a parsed program:
// name -> string-literal value. Non-string consts are omitted (a tool
// provider must fold to a string).
func constTable(src string) map[string]string {
	prog, perrs := parser.ParseSource(src, "t.dc")
	if len(perrs) > 0 {
		return nil
	}
	table := make(map[string]string)
	for _, c := range prog.ConstDecls() {
		if lit, ok := c.Value().(*ast.StringLit); ok {
			table[c.Name()] = lit.Value()
		}
	}
	return table
}

// rewrite rewrites every legacy tool(...) call to p.op({...}). consts may
// be nil (built from the source's ConstDecls). It returns the rewritten
// source; any unsupported shape (two-arg form, non-literal provider or
// operation) is an error so the codemod never silently drops a call.
func rewrite(src string, consts map[string]string) (string, error) {
	if consts == nil {
		consts = constTable(src)
	}
	toks := lexer.New(src, "t.dc").Tokenize()
	prog, perrs := parser.ParseSource(src, "t.dc")
	if len(perrs) > 0 {
		return "", fmt.Errorf("parse error: %s", perrs[0].Message)
	}
	var sites []toolSite
	visitor := &toolCallVisitor{src: src, toks: toks, consts: consts, sites: &sites}
	ast.Walk(visitor, prog)
	if visitor.err != nil {
		return "", visitor.err
	}
	if len(sites) == 0 {
		return src, nil
	}
	edits := make([]edit, 0, len(sites)*2)
	for _, s := range sites {
		// Site offsets are rune offsets (parser/lexer semantics); the
		// surgery slices bytes, so convert before building the edits.
		edits = append(edits,
			edit{start: runeToByte(src, s.callStart), end: runeToByte(src, s.parenEnd), repl: s.provider + "." + s.operation + "("},
			edit{start: runeToByte(src, s.arg0Start), end: runeToByte(src, s.arg2Start), repl: ""},
		)
	}
	out, err := applyEdits(src, edits)
	if err != nil {
		return "", err
	}
	// Verify the result reparses cleanly before returning (brief Step 3).
	if _, perrs := parser.ParseSource(out, "t.dc"); len(perrs) > 0 {
		return "", fmt.Errorf("rewritten source failed to reparse: %s", perrs[0].Message)
	}
	return out, nil
}

// toolCallVisitor collects legacy tool(...) call sites. It is an
// ast.Visitor so nested calls (tool() inside a map argument) are also
// found; overlapping edits are rejected by applyEdits.
type toolCallVisitor struct {
	src    string
	toks   []token.Token
	consts map[string]string
	sites  *[]toolSite
	err    error
}

func (v *toolCallVisitor) Visit(n ast.Node) ast.Visitor {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return v
	}
	ident, ok := call.Func().(*ast.IdentExpr)
	if !ok || ident.Name() != "tool" {
		return v
	}
	s, err := v.siteFor(call)
	if err != nil {
		v.err = err
		return nil
	}
	*v.sites = append(*v.sites, s)
	return v
}

// siteFor resolves the surgery positions and identity of one tool() call.
func (v toolCallVisitor) siteFor(call *ast.CallExpr) (toolSite, error) {
	span := call.Span()
	callStart := span.Start.Offset
	callEnd := span.End.Offset
	// Locate the '(' and the argument boundaries from the token stream
	// (leaf AST nodes carry zero spans; tokens always carry real offsets).
	argStarts, parenEnd, err := toolArgBoundaries(v.toks, callStart, callEnd)
	if err != nil {
		return toolSite{}, err
	}
	site := toolSite{callStart: callStart, parenEnd: parenEnd, span: span}
	args := call.Args()
	if len(args) == 2 {
		return toolSite{}, fmt.Errorf("two-arg tool() has no direct-call form: %s", v.render(call))
	}
	if len(args) != 3 {
		return toolSite{}, fmt.Errorf("tool() call has %d arguments, want provider, operation and params map: %s", len(args), v.render(call))
	}
	// Provider: string literal or const reference.
	switch p := args[0].(type) {
	case *ast.StringLit:
		site.provider = p.Value()
	case *ast.IdentExpr:
		value, ok := v.consts[p.Name()]
		if !ok {
			return toolSite{}, fmt.Errorf("tool provider must be a string literal or const: %s", v.render(call))
		}
		site.provider = value
		site.arg0IsConst = true
	default:
		return toolSite{}, fmt.Errorf("tool provider must be a string literal or const: %s", v.render(call))
	}
	// Operation: string literal (const operations are not folded — the
	// corpus has none and the qualified form's operation is the field name).
	op, ok := args[1].(*ast.StringLit)
	if !ok {
		return toolSite{}, fmt.Errorf("tool operation must be a string literal: %s", v.render(call))
	}
	site.operation = op.Value()
	site.arg0Start = argStarts[0]
	site.arg2Start = argStarts[2]
	return site, nil
}

func (v toolCallVisitor) render(call *ast.CallExpr) string {
	start, end := byteSpan(v.src, call.Span())
	if end > len(v.src) {
		end = len(v.src)
	}
	if start > end {
		start = end
	}
	text := strings.TrimSpace(v.src[start:end])
	if len(text) > 60 {
		text = text[:57] + "..."
	}
	return text
}

// toolArgBoundaries finds the rune offsets of the '(' (end), the start of
// each top-level argument, and validates the call's paren structure by
// scanning tokens between callStart and callEnd. argStarts has one entry
// per top-level argument.
func toolArgBoundaries(toks []token.Token, callStart, callEnd int) (argStarts []int, parenEnd int, err error) {
	i := 0
	for i < len(toks) && toks[i].Location.Offset < callStart {
		i++
	}
	// toks[i] is the callee identifier; the '(' follows.
	if i >= len(toks) || toks[i].Type != token.IDENTIFIER {
		return nil, 0, fmt.Errorf("tool callee token not found at %d", callStart)
	}
	i++
	if i >= len(toks) || toks[i].Type != token.LPAREN {
		return nil, 0, fmt.Errorf("tool( call at %d has no '('", callStart)
	}
	parenEnd = toks[i].Location.Offset + 1
	depth := 0
	i++
	first := true
	for ; i < len(toks); i++ {
		t := toks[i]
		if t.Location.Offset > callEnd && depth == 0 {
			break
		}
		switch t.Type {
		case token.LPAREN, token.LBRACKET, token.LBRACE:
			if depth == 0 && first {
				argStarts = append(argStarts, t.Location.Offset)
				first = false
			}
			depth++
		case token.RPAREN, token.RBRACKET, token.RBRACE:
			if depth == 0 && t.Type == token.RPAREN {
				return argStarts, parenEnd, nil
			}
			depth--
		case token.COMMA:
			if depth == 0 {
				first = true
			}
		default:
			if depth == 0 && first {
				argStarts = append(argStarts, t.Location.Offset)
				first = false
			}
		}
	}
	return nil, 0, fmt.Errorf("unbalanced tool( call region [%d,%d)", callStart, callEnd)
}

// byteSpan converts a rune-offset span into byte offsets for slicing.
func byteSpan(src string, s ast.Span) (int, int) {
	return runeToByte(src, s.Start.Offset), runeToByte(src, s.End.Offset)
}

// runeToByte converts a rune offset into a byte offset in src (Go range
// over a string yields byte offsets, so the rune counter is decremented
// explicitly).
func runeToByte(src string, runeOff int) int {
	if runeOff <= 0 {
		return 0
	}
	b := 0
	for _, r := range src {
		if runeOff == 0 {
			return b
		}
		runeOff--
		b += utf8.RuneLen(r)
	}
	return len(src)
}

// applyEdits applies non-overlapping byte edits (ascending by start
// offset) to src: the result is src with each [start,end) region replaced
// by repl, in order, so earlier regions keep their original offsets.
func applyEdits(src string, edits []edit) (string, error) {
	sorted := append([]edit(nil), edits...)
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].start < sorted[b].start })
	var sb strings.Builder
	pos := 0
	for _, e := range sorted {
		if e.start < pos || e.start > e.end || e.end > len(src) {
			return "", fmt.Errorf("overlapping or invalid edit [%d,%d)", e.start, e.end)
		}
		sb.WriteString(src[pos:e.start])
		sb.WriteString(e.repl)
		pos = e.end
	}
	sb.WriteString(src[pos:])
	return sb.String(), nil
}

// findRemainingToolCalls returns the byte spans of every tool( call still
// present in src (AST-backed; comments are not calls). Used by --check.
func findRemainingToolCalls(src string) []ast.Span {
	prog, _ := parser.ParseSource(src, "t.dc")
	var spans []ast.Span
	ast.Walk(toolCheckVisitor{spans: &spans}, prog)
	return spans
}

type toolCheckVisitor struct{ spans *[]ast.Span }

func (v toolCheckVisitor) Visit(n ast.Node) ast.Visitor {
	if call, ok := n.(*ast.CallExpr); ok {
		if ident, ok := call.Func().(*ast.IdentExpr); ok && ident.Name() == "tool" {
			*v.spans = append(*v.spans, call.Span())
		}
	}
	return v
}

// migrateSource is the file-level migration: rewrite() plus deletion of
// const declarations that were used exclusively as tool providers
// (decision 3 — const providers are not supported; the namespace must be
// a literal identifier).
func migrateSource(src string) (string, error) {
	consts := constTable(src)
	used := providerConsts(src)
	rewritten, err := rewrite(src, consts)
	if err != nil {
		return "", err
	}
	out, err := deleteConstLines(rewritten, used)
	if err != nil {
		return "", err
	}
	// Verify the const-deleted output reparses before returning (review
	// R1). rewrite() already reparse-verifies its own output, but the
	// whole-line const deletion that follows is a second source of
	// corruption: deleteConstLines rejects shared-line consts, and this
	// re-parse is the final backstop so a bug there can never silently
	// drop sibling statements.
	if _, perrs := parser.ParseSource(out, "t.dc"); len(perrs) > 0 {
		return "", fmt.Errorf("rewritten source failed to reparse after const deletion: %s", perrs[0].Message)
	}
	return out, nil
}

// providerConsts returns the names of consts referenced as tool() provider
// arguments (arg0 IdentExpr) anywhere in src.
func providerConsts(src string) map[string]bool {
	prog, perrs := parser.ParseSource(src, "t.dc")
	if len(perrs) > 0 {
		return nil
	}
	used := map[string]bool{}
	ast.Walk(providerConstVisitor{used: used}, prog)
	return used
}

type providerConstVisitor struct{ used map[string]bool }

func (v providerConstVisitor) Visit(n ast.Node) ast.Visitor {
	if call, ok := n.(*ast.CallExpr); ok {
		if ident, ok := call.Func().(*ast.IdentExpr); ok && ident.Name() == "tool" {
			if len(call.Args()) > 0 {
				if arg0, ok := call.Args()[0].(*ast.IdentExpr); ok {
					v.used[arg0.Name()] = true
				}
			}
		}
	}
	return v
}

// deleteConstLines removes the declaration line of every const in names
// that is NOT referenced anywhere else in the (already rewritten) source
// — the safe rule: a const folded into a namespace is deletable only when
// it has no remaining identifier references.
func deleteConstLines(src string, names map[string]bool) (string, error) {
	if len(names) == 0 {
		return src, nil
	}
	prog, perrs := parser.ParseSource(src, "t.dc")
	if len(perrs) > 0 {
		return "", fmt.Errorf("parse error: %s", perrs[0].Message)
	}
	var lines []edit
	for _, c := range prog.ConstDecls() {
		if !names[c.Name()] {
			continue
		}
		if constRefsElsewhere(src, c.Name()) {
			continue // still used (e.g. as a param value): keep it
		}
		if !constLineIsAlone(src, c.Span()) {
			// Whole-line deletion is safe only when the const is the only
			// code on its line. A sibling statement (before or after the
			// const) would otherwise be silently removed (review R1) —
			// error loudly and let the user split the line by hand.
			return "", fmt.Errorf(
				"const %s: provider const shares its line with other code (or spans multiple lines); move it to its own line before migrating",
				c.Name())
		}
		start, _ := byteSpan(src, c.Span())
		lineStart := start
		for lineStart > 0 && src[lineStart-1] != '\n' {
			lineStart--
		}
		lineEnd := start
		for lineEnd < len(src) && src[lineEnd] != '\n' {
			lineEnd++
		}
		if lineEnd < len(src) {
			lineEnd++ // include the newline
		}
		lines = append(lines, edit{start: lineStart, end: lineEnd, repl: ""})
	}
	if len(lines) == 0 {
		return src, nil
	}
	return applyEdits(src, lines)
}

// constLineIsAlone reports whether the const declaration is the only code
// on its line (review R1). The declaration must be single-line, and every
// non-EOF token on that line must belong to the const itself. Comments are
// lexer-skipped trivia, so `const X = "y"; // note` passes;
// `import "x"; const X = "y";` and `const X = "y"; import "x";` fail.
// Note the span's End is the START of the terminating SEMICOLON (the
// parser sets it via p.previous().Location after consuming the token), so
// the comparison is end-INCLUSIVE: the const's own `;` sits exactly at
// `end` and must not count as a sibling.
func constLineIsAlone(src string, span ast.Span) bool {
	line := span.Start.Line
	if span.End.Line != line {
		return false // multi-line declaration: whole-line deletion not safe
	}
	start, end := byteSpan(src, span)
	toks := lexer.New(src, "t.dc").Tokenize()
	for _, t := range toks {
		if t.Type == token.EOF || t.Location.Line != line {
			continue
		}
		if off := runeToByte(src, t.Location.Offset); off >= start && off <= end {
			continue // part of the const declaration (incl. its `;`)
		}
		return false // sibling token on the const's line
	}
	return true
}

// constRefsElsewhere reports whether name is referenced as an identifier
// anywhere in src (after rewriting, tool() calls are gone, so any hit is a
// real remaining use).
func constRefsElsewhere(src, name string) bool {
	prog, perrs := parser.ParseSource(src, "t.dc")
	if len(perrs) > 0 {
		return true // conservative: parse problems mean keep the const
	}
	found := false
	ast.Walk(identRefVisitor{name: name, found: &found}, prog)
	return found
}

type identRefVisitor struct {
	name  string
	found *bool
}

func (v identRefVisitor) Visit(n ast.Node) ast.Visitor {
	if *v.found {
		return nil
	}
	if id, ok := n.(*ast.IdentExpr); ok && id.Name() == v.name {
		*v.found = true
		return nil
	}
	return v
}

// ── CLI ───────────────────────────────────────────────────────────────

func main() {
	check := flag.Bool("check", false, "exit 1 if any tool( call remains (no rewrite)")
	stdin := flag.Bool("stdin", false, "read one source from stdin, write rewritten source to stdout")
	flag.Parse()
	if *stdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rewrite-calls: reading stdin:", err)
			os.Exit(1)
		}
		out, err := migrateSource(string(data))
		if err != nil {
			fmt.Fprintln(os.Stderr, "rewrite-calls:", err)
			os.Exit(1)
		}
		os.Stdout.WriteString(out)
		return
	}
	files := flag.Args()
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "usage: rewrite-calls [--check] file.ncoda...")
		os.Exit(2)
	}
	exit := 0
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rewrite-calls: %s: %v\n", path, err)
			exit = 1
			continue
		}
		if *check {
			for _, s := range findRemainingToolCalls(string(data)) {
				fmt.Fprintf(os.Stderr, "%s: remaining tool( call at %d:%d\n", path, s.Start.Line, s.Start.Column)
				exit = 1
			}
			continue
		}
		out, err := migrateSource(string(data))
		if err != nil {
			fmt.Fprintf(os.Stderr, "rewrite-calls: %s: %v\n", path, err)
			exit = 1
			continue
		}
		if out != string(data) {
			if werr := os.WriteFile(path, []byte(out), 0o644); werr != nil {
				fmt.Fprintf(os.Stderr, "rewrite-calls: %s: %v\n", path, werr)
				exit = 1
				continue
			}
			fmt.Printf("rewrote %s\n", path)
		} else {
			fmt.Printf("unchanged %s\n", path)
		}
	}
	os.Exit(exit)
}
