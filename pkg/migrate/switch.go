// Package migrate — switch.go: intent→switch / classify→switch migration codemod.
//
// Rewrites old `intent(...)` statements to `switch(...)` (lexer-precise
// keyword rewrite) and detects `classify<T>(...)` value calls, which have no
// mechanical switch form (branch bodies are unknowable): per PRD §C2, pure
// value extraction migrates to llm<enum>, branch-form classify (reference target
// question-classifier) migrates to switch statements at the decompiler. The
// codemod emits a manual-migration diagnostic instead of inventing branch
// bodies.
package migrate

import (
	"fmt"
	"sort"

	"github.com/nodecoda/ncoda/pkg/lexer"
	"github.com/nodecoda/ncoda/pkg/token"
)

// SWITCH migration diagnostic codes.
const (
	CodeIntentClassifyParse Code = "SWITCH_MIGRATE_PARSE"
	CodeClassifyToSwitch    Code = "CLASSIFY_TO_SWITCH"
	CodeIntentRewrite       Code = "INTENT_TO_SWITCH"
)

// RewriteIntentClassify rewrites intent→switch (lexer-precise keyword
// rewrite, string/comment aware) and detects classify<T> value calls (manual
// migration required; the rewrite leaves the call untouched and emits
// CodeClassifyToSwitch diagnostics). It is idempotent: a second pass on a
// fully migrated source reports no changes.
func RewriteIntentClassify(source, filename string) Result {
	changed := false
	diags := []Diagnostic{}

	// Phase 1: replace every KW_INTENT token span with `switch`.
	// Lexer-precise: only the keyword token is rewritten, never identifiers
	// or string/comment content.
	tokens, lexErrors := tokenize(source, filename)
	if len(lexErrors) > 0 {
		return Result{Source: source, Diagnostics: lexErrors}
	}
	intentEdits := []Edit{}
	for _, tok := range tokens {
		if tok.Type == token.KW_INTENT {
			intentEdits = append(intentEdits, Edit{
				Start: tok.Location.Offset,
				End:   tok.Location.Offset + len("intent"),
			})
		}
	}
	if len(intentEdits) > 0 {
		changed = true
		diags = append(diags, Diagnostic{
			Code:     CodeIntentRewrite,
			Severity: "info",
			Message:  fmt.Sprintf("rewrote %d intent( → switch( (1:1 keyword migration)", len(intentEdits)),
		})
		source = applyEdits(source, intentEdits, "switch")
	}

	// Phase 2: classify<T>(...) value-call detection. No mechanical switch
	// form exists (branch bodies are unknowable); per PRD §C2 value
	// extraction migrates to llm<enum> and branch-form classify migrates at
	// the decompiler. Emit a manual-migration diagnostic.
	classifyCount := 0
	for _, tok := range tokens {
		// classify is an ordinary identifier call (classify<Enum>); detect
		// the ident token whose lexeme is classify followed by '<'.
		if tok.Type == token.IDENTIFIER && tok.Lexeme == "classify" {
			classifyCount++
		}
	}
	if classifyCount > 0 {
		diags = append(diags, Diagnostic{
			Code:     CodeClassifyToSwitch,
			Severity: SeverityError,
			Message: fmt.Sprintf(
				"found %d classify<T>(...) call(s); no mechanical switch form — migrate value extraction to llm<enum> or the branch form to switch at the decompiler",
				classifyCount),
		})
	}

	return Result{Source: source, Changed: changed, Diagnostics: diags}
}

// tokenize lexes source and converts lexer errors to migrate diagnostics.
func tokenize(source, filename string) ([]token.Token, []Diagnostic) {
	lx := lexer.New(source, filename)
	tokens := lx.Tokenize()
	if len(lx.Errors()) == 0 {
		return tokens, nil
	}
	diags := make([]Diagnostic, 0, len(lx.Errors()))
	for _, le := range lx.Errors() {
		diags = append(diags, Diagnostic{
			Line:     le.Location.Line,
			Column:   le.Location.Column,
			Code:     CodeIntentClassifyParse,
			Severity: SeverityError,
			Message:  le.Message,
			Start:    le.Location.Offset,
			End:      le.Location.Offset,
		})
	}
	return tokens, diags
}

// applyEdits applies replacement edits to source in descending span order
// (right-to-left, so earlier offsets stay valid).
func applyEdits(source string, edits []Edit, replacement string) string {
	sort.Slice(edits, func(i, j int) bool { return edits[i].Start > edits[j].Start })
	out := source
	for _, e := range edits {
		out = out[:e.Start] + replacement + out[e.End:]
	}
	return out
}
