// Package migrate implements the binding-aware LLM result migration.
package migrate

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/lexer"
	"github.com/nodecoda/ncoda/pkg/parser"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/token"
	"github.com/nodecoda/ncoda/pkg/types"
)

type Code string

const (
	CodeParse             Code = "LLM_MIGRATE_PARSE"
	CodeBinding           Code = "LLM_MIGRATE_BINDING"
	CodeAmbiguousProducer Code = "LLM_MIGRATE_AMBIGUOUS_PRODUCER"
	CodeDynamicIndex      Code = "LLM_MIGRATE_DYNAMIC_INDEX"
	CodeSpan              Code = "LLM_MIGRATE_SPAN"
)

type Severity string

const SeverityError Severity = "error"

type Edit struct {
	Start, End  int
	Replacement string
}

type Diagnostic struct {
	Line, Column int
	Code         Code
	Severity     Severity
	Message      string
	Start, End   int
}

type Result struct {
	Source      string
	Changed     bool
	Diagnostics []Diagnostic
}

// OriginKind identifies migration-only provenance supplied by an artifact
// decompiler. It is not inferred from source variable names.
type OriginKind string

const OriginBatchInnerLLM OriginKind = "batch-inner-llm"

// BatchInnerSourceMarker is emitted by artifact decompilers immediately before
// an LLM call whose physical output came from a batch container. Comments are
// semantically inert ncoda, but preserve the origin needed by the migration
// command after generated variables are renamed.
const BatchInnerSourceMarker = "/* ncoda-migrate:llm-origin=batch-inner */"

// OriginTag identifies one LLM call by its source rune offset. Callers that
// only have ncoda source omit tags; ambiguous physical fields then remain
// untouched instead of being guessed from generated variable names.
type OriginTag struct {
	CallOffset int
	Kind       OriginKind
}

type provenanceKind uint8

const (
	notLLM provenanceKind = iota
	singleLLM
	ambiguous
)

type provenance struct {
	kind       provenanceKind
	callID     binding.AstNodeID
	shape      legacyShape
	batchInner bool
}

type legacyShape uint8

const (
	legacyNone legacyShape = iota
	legacyTextResult
	legacyRecordExpanded
)

type environment map[binding.SymbolID]provenance

// Rewrite parses and binds source, computes reaching LLM definitions by
// SymbolID, then applies non-overlapping source-span edits right to left.
func Rewrite(source, filename string, origins ...OriginTag) Result {
	program, parseErrors := parser.ParseSource(source, filename)
	if len(parseErrors) > 0 {
		diagnostics := make([]Diagnostic, 0, len(parseErrors))
		for _, parseErr := range parseErrors {
			diagnostics = append(diagnostics, newDiagnostic(
				CodeParse, parseErr.Message, parseErr.Span.Start, parseErr.Span.End,
			))
		}
		return Result{Source: source, Diagnostics: diagnostics}
	}

	// The normal compiler pipeline substitutes top-level const references before
	// binding. Generated decompiler sources use const model aliases, so the
	// migration analysis must bind the same preprocessed AST while still applying
	// edits to the untouched source text.
	passes.SubstituteConstants(program)
	snapshot, bindDiagnostics := binding.Bind(program)
	if len(bindDiagnostics) > 0 {
		diagnostics := make([]Diagnostic, 0, len(bindDiagnostics))
		for _, item := range bindDiagnostics {
			diagnostics = append(diagnostics, Diagnostic{
				Line: item.Line, Column: item.Column, Code: CodeBinding,
				Severity: SeverityError, Message: item.Message,
			})
		}
		sortDiagnostics(diagnostics)
		return Result{Source: source, Diagnostics: diagnostics}
	}

	tokens := lexer.New(source, filename).Tokenize()
	analyzer := &analyzer{
		source: source, filename: filename, snapshot: snapshot,
		batchInnerCalls: batchInnerCallOffsets(source, tokens, origins),
		spanContexts: []spanContext{{
			source: source,
			tokens: tokens,
		}},
	}
	for _, function := range program.Functions() {
		analyzer.block(function.Body(), environment{})
	}
	if program.Entry() != nil {
		analyzer.block(program.Entry().Body(), environment{})
	}
	for _, function := range program.CodeFunctions() {
		analyzer.block(function.Body(), environment{})
	}
	sortDiagnostics(analyzer.diagnostics)
	if len(analyzer.diagnostics) > 0 {
		return Result{Source: source, Diagnostics: analyzer.diagnostics}
	}
	if len(analyzer.edits) == 0 {
		return Result{Source: source}
	}

	sort.Slice(analyzer.edits, func(i, j int) bool {
		if analyzer.edits[i].Start != analyzer.edits[j].Start {
			return analyzer.edits[i].Start > analyzer.edits[j].Start
		}
		return analyzer.edits[i].End > analyzer.edits[j].End
	})
	out := source
	lastStart := utf8.RuneCountInString(source) + 1
	for _, edit := range analyzer.edits {
		if edit.Start < 0 || edit.End < edit.Start || edit.End > lastStart {
			loc := locationAt(source, max(edit.Start, 0), filename)
			return Result{Source: source, Diagnostics: []Diagnostic{
				newDiagnostic(CodeSpan, "overlapping or invalid LLM migration edits", loc, loc),
			}}
		}
		startByte, endByte := runeToByte(source, edit.Start), runeToByte(source, edit.End)
		out = out[:startByte] + edit.Replacement + out[endByte:]
		lastStart = edit.Start
	}
	if _, errors := parser.ParseSource(out, filename); len(errors) > 0 {
		err := errors[0]
		return Result{Source: source, Diagnostics: []Diagnostic{
			newDiagnostic(CodeSpan, fmt.Sprintf("rewritten source failed to reparse: %s", err.Message), err.Span.Start, err.Span.End),
		}}
	}
	return Result{Source: out, Changed: out != source}
}

type analyzer struct {
	source          string
	filename        string
	snapshot        *binding.Snapshot
	spanContexts    []spanContext
	edits           []Edit
	diagnostics     []Diagnostic
	batchInnerCalls map[int]struct{}
}

type spanContext struct {
	source string
	tokens []token.Token
	base   int
}

func (a *analyzer) block(block *ast.Block, env environment) environment {
	if block == nil {
		return env
	}
	for _, statement := range block.Stmts() {
		env = a.statement(statement, env)
	}
	return env
}

func (a *analyzer) statement(statement ast.Stmt, env environment) environment {
	switch node := statement.(type) {
	case *ast.VarDeclStmt:
		value := a.expression(node.Init(), env)
		if symbol, ok := a.declarationSymbol(node); ok {
			env[symbol] = value
		}
	case *ast.AssignStmt:
		value := a.expression(node.Value(), env)
		a.expression(node.Target(), env)
		if ident, ok := node.Target().(*ast.IdentExpr); ok {
			if symbol, found := a.referenceSymbol(ident); found {
				env[symbol] = value
			}
		}
	case *ast.ExprStmt:
		a.expression(node.Expr(), env)
	case *ast.ReturnStmt:
		a.expression(node.Value(), env)
	case *ast.YieldStmt:
		a.expression(node.Value(), env)
	case *ast.OutputStmt:
		a.expression(node.Value(), env)
	case *ast.IfStmt:
		a.expression(node.Condition(), env)
		branches := []environment{a.block(node.ThenBody(), cloneEnvironment(env))}
		for _, branch := range node.ElseIfs() {
			a.expression(branch.Condition(), env)
			branches = append(branches, a.block(branch.Body(), cloneEnvironment(env)))
		}
		if node.ElseBody() != nil {
			branches = append(branches, a.block(node.ElseBody(), cloneEnvironment(env)))
		} else {
			branches = append(branches, cloneEnvironment(env))
		}
		env = mergeEnvironments(env, branches...)
	case *ast.WhileStmt:
		a.expression(node.Condition(), env)
		body := a.block(node.Body(), cloneEnvironment(env))
		env = mergeEnvironments(env, cloneEnvironment(env), body)
	case *ast.ForStmt:
		for _, source := range node.LoopSources() {
			a.expression(source, env)
		}
		body := a.block(node.Body(), cloneEnvironment(env))
		env = mergeEnvironments(env, cloneEnvironment(env), body)
	case *ast.ParallelStmt:
		branches := make([]environment, 0, len(node.Branches()))
		for _, branch := range node.Branches() {
			branches = append(branches, a.block(branch, cloneEnvironment(env)))
		}
		env = mergeEnvironments(env, branches...)
	case *ast.AttemptStmt:
		result := a.expression(node.Operation(), env)
		successEnv := cloneEnvironment(env)
		if symbol, ok := a.declarationSymbol(node.ResultBinding()); ok {
			successEnv[symbol] = result
		}
		successEnv = a.block(node.SuccessBody(), successEnv)
		failureEnv := a.block(node.FailureBody(), cloneEnvironment(env))
		env = mergeEnvironments(env, successEnv, failureEnv)
	case *ast.SwitchStmt:
		a.expression(node.Query(), env)
		a.expression(node.Config(), env)
		branches := make([]environment, 0, len(node.Branches())+1)
		for _, branch := range node.Branches() {
			branches = append(branches, a.block(branch.Body(), cloneEnvironment(env)))
		}
		if node.DefaultBody() != nil {
			branches = append(branches, a.block(node.DefaultBody(), cloneEnvironment(env)))
		} else {
			branches = append(branches, cloneEnvironment(env))
		}
		env = mergeEnvironments(env, branches...)
	case *ast.RequestInputStmt:
		a.expression(node.Config(), env)
		branches := make([]environment, 0, len(node.ActionBranches())+1)
		for _, branch := range node.ActionBranches() {
			a.expression(branch.ActionExpr(), env)
			branches = append(branches, a.block(branch.Body(), cloneEnvironment(env)))
		}
		branches = append(branches, a.block(node.TimeoutBody(), cloneEnvironment(env)))
		env = mergeEnvironments(env, branches...)
	}
	return env
}

func (a *analyzer) expression(expression ast.Expr, env environment) provenance {
	if expression == nil {
		return provenance{}
	}
	switch node := expression.(type) {
	case *ast.IdentExpr:
		if symbol, ok := a.referenceSymbol(node); ok {
			return env[symbol]
		}
	case *ast.CallExpr:
		a.expression(node.Func(), env)
		for _, argument := range node.Args() {
			a.expression(argument, env)
		}
		if id, ok := a.snapshot.NodeIDOf(node); ok {
			if target, found := a.snapshot.CallTargets().Get(id); found &&
				target.Kind == binding.CallBuiltin && target.Name == "llm" {
				_, batchInner := a.batchInnerCalls[node.Span().Start.Offset]
				return provenance{kind: singleLLM, callID: id, shape: a.legacyLLMShape(node), batchInner: batchInner}
			}
		}
	case *ast.OperationPolicyExpr:
		value := a.expression(node.Operation(), env)
		for _, policy := range node.Policies() {
			switch policy := policy.(type) {
			case *ast.DefaultPolicy:
				a.expression(policy.Value(), env)
			}
		}
		return value
	case *ast.FieldAccessExpr:
		object := a.expression(node.Obj(), env)
		a.rewriteField(node, object)
	case *ast.IndexExpr:
		object := a.expression(node.Obj(), env)
		a.expression(node.Index(), env)
		a.rewriteIndex(node, object)
	case *ast.TemplateString:
		a.templateExpressions(node, env)
	case *ast.ArrayLit:
		for _, item := range node.Elements() {
			a.expression(item, env)
		}
	case *ast.MapLit:
		for _, entry := range node.Entries() {
			a.expression(entry.Key, env)
			a.expression(entry.Value, env)
		}
	case *ast.UnaryExpr:
		a.expression(node.Operand(), env)
	case *ast.BinaryExpr:
		a.expression(node.Left(), env)
		a.expression(node.Right(), env)
	case *ast.TernaryExpr:
		a.expression(node.Condition(), env)
		return mergeProvenance(a.expression(node.ThenVal(), env), a.expression(node.ElseVal(), env))
	case *ast.LambdaExpr:
		a.expression(node.Body(), env)
	case *ast.ForExpr:
		for _, source := range node.LoopSources() {
			a.expression(source, env)
		}
		a.block(node.Body(), cloneEnvironment(env))
	case *ast.ParallelForExpr:
		for _, source := range node.LoopSources() {
			a.expression(source, env)
		}
		a.block(node.Body(), cloneEnvironment(env))
	case *ast.ForeignCodeExpr:
		for _, input := range node.Inputs() {
			a.expression(input.Value(), env)
		}
	}
	return provenance{}
}

func batchInnerCallOffsets(source string, tokens []token.Token, origins []OriginTag) map[int]struct{} {
	offsets := make(map[int]struct{}, len(origins))
	for _, origin := range origins {
		if origin.Kind == OriginBatchInnerLLM && origin.CallOffset >= 0 {
			offsets[origin.CallOffset] = struct{}{}
		}
	}

	// Comments are discarded by the lexer, so marker discovery must scan source
	// state explicitly. In particular, a marker-looking string or line comment
	// is not provenance, and a marker separated from an LLM call by code must
	// not retarget a later call.
	byOffset := make(map[int]int, len(tokens))
	for index, item := range tokens {
		byOffset[item.Location.Offset] = index
	}
	for position := 0; position < len(source); {
		switch source[position] {
		case '/':
			if position+1 >= len(source) {
				position++
				continue
			}
			switch source[position+1] {
			case '/':
				position += 2
				for position < len(source) && source[position] != '\n' {
					position++
				}
				continue
			case '*':
				end, closed := scanBlockComment(source, position)
				if closed && source[position:end] == BatchInnerSourceMarker {
					next := skipUnicodeSpace(source, end)
					nextOffset := utf8.RuneCountInString(source[:next])
					if tokenIndex, ok := byOffset[nextOffset]; ok && tokenIndex+1 < len(tokens) {
						item := tokens[tokenIndex]
						nextToken := tokens[tokenIndex+1]
						if item.Lexeme == "llm" &&
							(nextToken.Type == token.LPAREN || nextToken.Type == token.LESS) {
							offsets[item.Location.Offset] = struct{}{}
						}
					}
				}
				position = end
				continue
			}
		case '\'', '"', '`':
			position = skipQuotedSource(source, position, source[position])
			continue
		}
		_, width := utf8.DecodeRuneInString(source[position:])
		if width == 0 {
			width = 1
		}
		position += width
	}
	return offsets
}

func scanBlockComment(source string, start int) (end int, closed bool) {
	depth := 1
	position := start + 2
	for position < len(source) {
		if position+1 < len(source) && source[position] == '/' && source[position+1] == '*' {
			depth++
			position += 2
			continue
		}
		if position+1 < len(source) && source[position] == '*' && source[position+1] == '/' {
			depth--
			position += 2
			if depth == 0 {
				return position, true
			}
			continue
		}
		_, width := utf8.DecodeRuneInString(source[position:])
		if width == 0 {
			width = 1
		}
		position += width
	}
	return len(source), false
}

func skipUnicodeSpace(source string, start int) int {
	position := start
	for position < len(source) {
		r, width := utf8.DecodeRuneInString(source[position:])
		if width == 0 || !unicode.IsSpace(r) {
			break
		}
		position += width
	}
	return position
}

func skipQuotedSource(source string, start int, quote byte) int {
	position := start + 1
	for position < len(source) {
		switch source[position] {
		case '\\':
			position++
			if position < len(source) {
				_, width := utf8.DecodeRuneInString(source[position:])
				if width == 0 {
					width = 1
				}
				position += width
			}
		case quote:
			return position + 1
		default:
			_, width := utf8.DecodeRuneInString(source[position:])
			if width == 0 {
				width = 1
			}
			position += width
		}
	}
	return len(source)
}

func (a *analyzer) rewriteField(field *ast.FieldAccessExpr, object provenance) {
	if object.kind == ambiguous {
		a.addDiagnostic(CodeAmbiguousProducer, "ambiguous LLM producer; migrate manually", field.Span())
		return
	}
	if object.kind != singleLLM {
		return
	}
	start, end, escaped, ok := a.fieldRange(field)
	if !ok {
		a.addDiagnostic(CodeSpan, "cannot locate LLM field span", field.Span())
		return
	}
	switch field.FieldName() {
	case "text", "reasoning_content", "usage", "finish_reason":
		return
	case "content":
		if object.shape == legacyTextResult {
			a.edits = append(a.edits, Edit{Start: start, End: end, Replacement: ".text"})
		} else if object.shape == legacyRecordExpanded {
			a.edits = append(a.edits, Edit{Start: start, End: start, Replacement: ".text"})
		}
	case "output":
		if escaped && object.batchInner {
			a.edits = append(a.edits, Edit{Start: start, End: end, Replacement: ".text"})
		} else if !escaped && object.shape == legacyRecordExpanded {
			a.edits = append(a.edits, Edit{Start: start, End: start, Replacement: ".text"})
		}
	default:
		if object.shape == legacyRecordExpanded {
			a.edits = append(a.edits, Edit{Start: start, End: start, Replacement: ".text"})
		}
	}
}

func (a *analyzer) rewriteIndex(index *ast.IndexExpr, object provenance) {
	if object.kind == ambiguous {
		a.addDiagnostic(CodeAmbiguousProducer, "ambiguous LLM producer; migrate manually", index.Span())
		return
	}
	if object.kind != singleLLM {
		return
	}
	if object.shape != legacyRecordExpanded {
		return
	}
	if _, ok := index.Index().(*ast.StringLit); !ok {
		a.addDiagnostic(CodeDynamicIndex, "dynamic index on legacy LLM result; migrate manually", index.Span())
		return
	}
	position, ok := a.indexBracket(index)
	if !ok {
		a.addDiagnostic(CodeSpan, "cannot locate LLM index span", index.Span())
		return
	}
	a.edits = append(a.edits, Edit{Start: position, End: position, Replacement: ".text"})
}

func (a *analyzer) legacyLLMShape(call *ast.CallExpr) legacyShape {
	if len(call.TypeArgs()) == 0 {
		return legacyTextResult
	}
	d := types.TypeRefToDescriptor(call.TypeArgs()[0], func(name string) *types.Descriptor {
		value, _ := a.snapshot.NamedTypes().Get(name)
		return value
	})
	if d == nil {
		return legacyNone
	}
	switch d.Kind {
	case types.String:
		return legacyTextResult
	case types.Record:
		return legacyRecordExpanded
	default:
		return legacyNone
	}
}

func (a *analyzer) fieldRange(field *ast.FieldAccessExpr) (start, end int, escaped, ok bool) {
	context := a.spanContext()
	span := field.Span()
	for i := len(context.tokens) - 2; i >= 0; i-- {
		dot, name := context.tokens[i], context.tokens[i+1]
		if dot.Type != token.DOT || name.Lexeme != field.FieldName() {
			continue
		}
		if dot.Location.Offset < span.Start.Offset || name.Location.Offset > span.End.Offset+1 {
			continue
		}
		end = name.Location.Offset + utf8.RuneCountInString(name.Lexeme)
		escaped = name.Location.Offset > dot.Location.Offset+1 &&
			runeSlice(context.source, name.Location.Offset-1, name.Location.Offset) == "$"
		return context.base + dot.Location.Offset, context.base + end, escaped, true
	}
	return 0, 0, false, false
}

func (a *analyzer) indexBracket(index *ast.IndexExpr) (int, bool) {
	context := a.spanContext()
	span := index.Span()
	for i := len(context.tokens) - 1; i >= 0; i-- {
		item := context.tokens[i]
		if item.Type == token.LBRACKET && item.Location.Offset >= span.Start.Offset &&
			item.Location.Offset <= span.End.Offset {
			return context.base + item.Location.Offset, true
		}
	}
	return 0, false
}

func (a *analyzer) templateExpressions(template *ast.TemplateString, env environment) {
	fragments := a.templateFragments(template)
	partIndex := 0
	for _, part := range template.Parts() {
		if part.Expr == nil {
			continue
		}
		if partIndex >= len(fragments) {
			a.addDiagnostic(CodeSpan, "cannot locate template expression span", template.Span())
			return
		}
		fragment := fragments[partIndex]
		partIndex++
		a.spanContexts = append(a.spanContexts, spanContext{
			source: fragment.source,
			tokens: lexer.New(fragment.source, a.filename).Tokenize(),
			base:   fragment.base,
		})
		a.expression(part.Expr, env)
		a.spanContexts = a.spanContexts[:len(a.spanContexts)-1]
	}
	if partIndex != len(fragments) {
		a.addDiagnostic(CodeSpan, "template expression span count mismatch", template.Span())
	}
}

type templateFragment struct {
	source string
	base   int
}

func (a *analyzer) templateFragments(template *ast.TemplateString) []templateFragment {
	context := a.spanContext()
	span := template.Span()
	for _, item := range context.tokens {
		if item.Type != token.TEMPLATE_STRING || item.Location.Offset != span.Start.Offset {
			continue
		}
		return splitTemplateFragments(item.Lexeme, context.base+item.Location.Offset)
	}
	return nil
}

// splitTemplateFragments mirrors the lexer's brace-depth rule. base is the
// absolute rune offset of the opening backtick; each returned base points to
// the first rune inside its ${...} expression.
func splitTemplateFragments(lexeme string, base int) []templateFragment {
	runes := []rune(lexeme)
	fragments := []templateFragment{}
	for position := 1; position+1 < len(runes); {
		if runes[position] == '\\' && position+1 < len(runes) &&
			(runes[position+1] == '`' || runes[position+1] == '\\' || runes[position+1] == '$') {
			position += 2
			continue
		}
		if runes[position] != '$' || runes[position+1] != '{' {
			position++
			continue
		}
		start := position + 2
		depth := 1
		position = start
		for position < len(runes) && depth > 0 {
			switch runes[position] {
			case '{':
				depth++
			case '}':
				depth--
			}
			position++
		}
		if depth != 0 {
			return fragments
		}
		end := position - 1
		fragments = append(fragments, templateFragment{
			source: string(runes[start:end]),
			base:   base + start,
		})
	}
	return fragments
}

func (a *analyzer) spanContext() spanContext {
	return a.spanContexts[len(a.spanContexts)-1]
}

func (a *analyzer) declarationSymbol(node ast.Node) (binding.SymbolID, bool) {
	if node == nil {
		return 0, false
	}
	id, ok := a.snapshot.NodeIDOf(node)
	if !ok {
		return 0, false
	}
	return a.snapshot.DeclarationSymbols().Get(id)
}

func (a *analyzer) referenceSymbol(node ast.Node) (binding.SymbolID, bool) {
	id, ok := a.snapshot.NodeIDOf(node)
	if !ok {
		return 0, false
	}
	return a.snapshot.ReferenceSymbols().Get(id)
}

func (a *analyzer) addDiagnostic(code Code, message string, span ast.Span) {
	context := a.spanContext()
	start := locationAt(a.source, context.base+span.Start.Offset, a.filename)
	end := locationAt(a.source, context.base+span.End.Offset, a.filename)
	a.diagnostics = append(a.diagnostics, newDiagnostic(code, message, start, end))
}

func mergeProvenance(left, right provenance) provenance {
	if left.kind == ambiguous || right.kind == ambiguous {
		return provenance{kind: ambiguous}
	}
	if left.kind == notLLM && right.kind == notLLM {
		return provenance{}
	}
	if left.kind == singleLLM && right.kind == singleLLM && left.callID == right.callID &&
		left.shape == right.shape && left.batchInner == right.batchInner {
		return left
	}
	return provenance{kind: ambiguous}
}

func mergeEnvironments(base environment, environments ...environment) environment {
	result := cloneEnvironment(base)
	keys := map[binding.SymbolID]bool{}
	for key := range base {
		keys[key] = true
	}
	for _, env := range environments {
		for key := range env {
			keys[key] = true
		}
	}
	for key := range keys {
		var merged provenance
		if len(environments) > 0 {
			merged = environments[0][key]
			for _, env := range environments[1:] {
				merged = mergeProvenance(merged, env[key])
			}
		}
		result[key] = merged
	}
	return result
}

func cloneEnvironment(source environment) environment {
	out := make(environment, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func newDiagnostic(code Code, message string, start, end token.Location) Diagnostic {
	return Diagnostic{
		Line: start.Line, Column: start.Column, Code: code, Severity: SeverityError,
		Message: strings.TrimSpace(message), Start: start.Offset, End: end.Offset,
	}
}

func sortDiagnostics(diagnostics []Diagnostic) {
	sort.SliceStable(diagnostics, func(i, j int) bool {
		if diagnostics[i].Start != diagnostics[j].Start {
			return diagnostics[i].Start < diagnostics[j].Start
		}
		if diagnostics[i].End != diagnostics[j].End {
			return diagnostics[i].End < diagnostics[j].End
		}
		return diagnostics[i].Code < diagnostics[j].Code
	})
}

func locationAt(source string, offset int, filename string) token.Location {
	line, column := 1, 1
	for index, value := range []rune(source) {
		if index >= offset {
			break
		}
		if value == '\n' {
			line, column = line+1, 1
		} else {
			column++
		}
	}
	return token.Location{Line: line, Column: column, Offset: offset, Filename: filename}
}

func runeSlice(source string, start, end int) string {
	runes := []rune(source)
	if start < 0 || end < start || end > len(runes) {
		return ""
	}
	return string(runes[start:end])
}

func runeToByte(source string, runeOffset int) int {
	if runeOffset <= 0 {
		return 0
	}
	count := 0
	for position := range source {
		if count == runeOffset {
			return position
		}
		count++
	}
	return len(source)
}

func (d Diagnostic) String() string { return strings.TrimSpace(d.Message) }
