// Package llmaudit classifies LLM semantic differences in compiler corpus
// audits. It is intentionally separate from package llm: the latter owns the
// language-level schema contract, while this package knows about diagnostics,
// migration normalization, roundtrip status, and named historical gaps.
package llmaudit

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/migrate"
	"github.com/nodecoda/ncoda/pkg/parser"
)

const (
	CategorySemantic            = "semantic"
	CategoryProjectionOnly      = "projection-only"
	CategoryMigrationNormalized = "migration-normalized"
	CategoryUnknown             = "UNKNOWN"
	CategoryPreExisting         = "pre-existing"
	CategoryAdapterRejected     = "adapter-rejected"
	CategoryFailure             = "failure"
	CategoryNotApplicable       = "not-applicable"
)

type Classification struct {
	Category string `json:"category"`
	Reason   string `json:"reason"`
	Detail   string `json:"detail,omitempty"`
}

type Input struct {
	Source       string
	Round2Source string
	Status       string
	DiagCode     string
	DiagMessage  string
	DecompileErr string
	Round2       string
	// RawLLMKnown is true only when the original workflow artifact was parsed
	// structurally. RawWorkflowLLM then records whether it contained an LLM node.
	RawLLMKnown    bool
	RawWorkflowLLM bool
}

type Summary struct {
	Counts       map[string]int `json:"counts"`
	Unclassified int            `json:"unclassified"`
}

type semantics struct {
	hasLLM                 bool
	codeStringInputFromLLM bool
	calls                  []string
	paths                  []string
	parseErr               string
}

func Classify(in Input) Classification {
	if in.DecompileErr != "" {
		if in.RawLLMKnown && !in.RawWorkflowLLM {
			return Classification{Category: CategoryNotApplicable, Reason: "non-llm-decompile-failure"}
		}
		return Classification{Category: CategoryFailure, Reason: "unclassified-llm-decompile-failure", Detail: normalizeDiagnosticMessage(in.DecompileErr)}
	}

	migrated := migrate.Rewrite(in.Source, "llm-corpus-audit.ncoda")
	canonicalSource := in.Source
	if len(migrated.Diagnostics) == 0 {
		canonicalSource = migrated.Source
	}
	first := inspect(canonicalSource)

	if in.Status != "SUCCEEDED" {
		switch {
		case in.DiagCode == "ADAPTER_ERROR" && strings.Contains(in.DiagMessage, "no integrated structured generation/validation/retry proof for non-string llm<T>"):
			return Classification{Category: CategoryAdapterRejected, Reason: "structured-llm-profile-not-proven"}
		case in.DiagCode == "TYPE_MISMATCH" && strings.Contains(in.DiagMessage, "llm payload type any is not admitted"):
			return Classification{Category: CategoryUnknown, Reason: "llm-schema-unknown"}
		case in.DiagCode == "UNDEFINED_VAR" && strings.Contains(in.DiagMessage, "SubWorkflow"):
			return Classification{Category: CategoryPreExisting, Reason: "subworkflow-language-coverage"}
		case in.DiagCode == "CODE_INPUT_TYPE" && first.codeStringInputFromLLM && strings.Contains(in.DiagMessage, "expected Type(string)") && strings.Contains(in.DiagMessage, "got Type(record"):
			return Classification{Category: CategoryPreExisting, Reason: "structured-llm-payload-to-string-code-input"}
		case first.hasLLM:
			return Classification{Category: CategoryFailure, Reason: "unclassified-llm-compile-failure", Detail: normalizedDiagnostic(in.DiagCode, in.DiagMessage)}
		default:
			return Classification{Category: CategoryNotApplicable, Reason: "non-llm-compile-failure"}
		}
	}

	if !first.hasLLM {
		return Classification{Category: CategoryNotApplicable, Reason: "no-llm-semantics"}
	}
	if first.parseErr != "" {
		return Classification{Category: CategoryFailure, Reason: "unclassified-llm-source", Detail: first.parseErr}
	}
	if in.Round2 == "" {
		return Classification{Category: CategoryNotApplicable, Reason: "round2-not-run"}
	}
	if in.Round2 == "skip" || in.Round2Source == "" {
		return Classification{Category: CategoryFailure, Reason: "unclassified-llm-round2-skip"}
	}

	second := inspect(in.Round2Source)
	if second.parseErr != "" {
		return Classification{Category: CategoryFailure, Reason: "unclassified-llm-round2-source", Detail: second.parseErr}
	}
	removed, added := sortedSetDiff(first.paths, second.paths)
	schemasStable := strings.Join(first.calls, "\x00") == strings.Join(second.calls, "\x00")
	if schemasStable && ((len(removed) > 0 && len(added) == 0) || (len(removed) == 0 && len(added) > 0)) {
		return Classification{
			Category: CategoryPreExisting,
			Reason:   "downstream-non-llm-reference-projection-drift",
			Detail:   semanticDiff(first, second),
		}
	}
	if !equalSemantics(first, second) {
		return Classification{Category: CategoryFailure, Reason: "unclassified-llm-path-difference", Detail: semanticDiff(first, second)}
	}
	if migrated.Changed {
		return Classification{Category: CategoryMigrationNormalized, Reason: "legacy-llm-access-normalized"}
	}
	if in.Round2 == "diff" {
		return Classification{Category: CategoryProjectionOnly, Reason: "llm-semantics-stable"}
	}
	return Classification{Category: CategorySemantic, Reason: "llm-fixed-point"}
}

func Summarize(classifications []Classification) Summary {
	summary := Summary{Counts: map[string]int{}}
	for _, classification := range classifications {
		if classification.Category == "" {
			continue
		}
		summary.Counts[classification.Category]++
		if strings.HasPrefix(classification.Reason, "unclassified-") {
			summary.Unclassified++
		}
	}
	return summary
}

func semanticDiff(before, after semantics) string {
	parts := make([]string, 0, 3)
	if strings.Join(before.calls, "\x00") != strings.Join(after.calls, "\x00") {
		parts = append(parts, "schemas changed: "+strings.Join(before.calls, " | ")+" -> "+strings.Join(after.calls, " | "))
	}
	removed, added := sortedSetDiff(before.paths, after.paths)
	if len(removed) > 0 {
		parts = append(parts, "paths removed: "+strings.Join(removed, ", "))
	}
	if len(added) > 0 {
		parts = append(parts, "paths added: "+strings.Join(added, ", "))
	}
	return strings.Join(parts, "; ")
}

func sortedSetDiff(before, after []string) (removed, added []string) {
	beforeSet := make(map[string]struct{}, len(before))
	afterSet := make(map[string]struct{}, len(after))
	for _, value := range before {
		beforeSet[value] = struct{}{}
	}
	for _, value := range after {
		afterSet[value] = struct{}{}
	}
	for value := range beforeSet {
		if _, ok := afterSet[value]; !ok {
			removed = append(removed, value)
		}
	}
	for value := range afterSet {
		if _, ok := beforeSet[value]; !ok {
			added = append(added, value)
		}
	}
	sort.Strings(removed)
	sort.Strings(added)
	return removed, added
}

var (
	diagnosticVarRe   = regexp.MustCompile(`\bv\d+\b`)
	diagnosticItemRe  = regexp.MustCompile(`\bitem_\d+\b`)
	diagnosticSpanRe  = regexp.MustCompile(`\d+:\d+`)
	diagnosticNumRe   = regexp.MustCompile(`\b\d+\b`)
	diagnosticQuoteRe = regexp.MustCompile(`"[^"]*"`)
	llmSyntaxRe       = regexp.MustCompile(`\bllm\s*(?:<|\()`)
)

func normalizedDiagnostic(code, message string) string {
	message = normalizeDiagnosticMessage(message)
	if code == "" {
		return message
	}
	return code + " :: " + message
}

func normalizeDiagnosticMessage(message string) string {
	message = diagnosticVarRe.ReplaceAllString(message, "<v>")
	message = diagnosticItemRe.ReplaceAllString(message, "<item>")
	message = diagnosticSpanRe.ReplaceAllString(message, "<loc>")
	message = diagnosticQuoteRe.ReplaceAllString(message, `"<lit>"`)
	return diagnosticNumRe.ReplaceAllString(message, "<n>")
}

func equalSemantics(a, b semantics) bool {
	return a.hasLLM == b.hasLLM &&
		strings.Join(a.calls, "\x00") == strings.Join(b.calls, "\x00") &&
		strings.Join(a.paths, "\x00") == strings.Join(b.paths, "\x00")
}

func inspect(source string) semantics {
	program, errs := parser.ParseSource(source, "llm-corpus-audit.ncoda")
	if len(errs) > 0 {
		return semantics{hasLLM: llmSyntaxRe.MatchString(source), parseErr: normalizeDiagnosticMessage(errs[0].Message)}
	}
	collector := &semanticCollector{
		vars:      map[string]bool{},
		typeDecls: map[string]*ast.TypeDecl{},
		enumDecls: map[string]*ast.EnumDecl{},
		pathSet:   map[string]struct{}{},
	}
	for _, decl := range program.TypeDecls() {
		collector.typeDecls[decl.Name()] = decl
	}
	for _, decl := range program.EnumDecls() {
		collector.enumDecls[decl.Name()] = decl
	}
	ast.Walk(collector, program)
	sort.Strings(collector.calls)
	paths := make([]string, 0, len(collector.pathSet))
	for path := range collector.pathSet {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return semantics{
		hasLLM:                 collector.hasLLM,
		codeStringInputFromLLM: collector.codeStringInputFromLLM,
		calls:                  collector.calls,
		paths:                  paths,
	}
}

type semanticCollector struct {
	vars                   map[string]bool
	typeDecls              map[string]*ast.TypeDecl
	enumDecls              map[string]*ast.EnumDecl
	pathSet                map[string]struct{}
	calls                  []string
	hasLLM                 bool
	codeStringInputFromLLM bool
}

func (c *semanticCollector) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case *ast.VarDeclStmt:
		c.vars[n.Name()] = c.exprFromLLM(n.Init())
	case *ast.CallExpr:
		if isLLMCall(n) {
			c.hasLLM = true
			if len(n.TypeArgs()) == 0 {
				c.calls = append(c.calls, "string")
			} else {
				c.calls = append(c.calls, c.typeFingerprint(n.TypeArgs()[0], map[string]bool{}))
			}
		}
	case *ast.FieldAccessExpr:
		if path, ok := c.llmFieldPath(n); ok {
			c.pathSet[path] = struct{}{}
		}
	case *ast.CodeInputBinding:
		if isStringType(n.TypeRef()) && c.exprFromLLM(n.Value()) {
			c.codeStringInputFromLLM = true
		}
	}
	return c
}

func (c *semanticCollector) exprFromLLM(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.IdentExpr:
		return c.vars[n.Name()]
	case *ast.FieldAccessExpr:
		return c.exprFromLLM(n.Obj())
	case *ast.IndexExpr:
		return c.exprFromLLM(n.Obj())
	case *ast.CallExpr:
		return isLLMCall(n)
	case *ast.OperationPolicyExpr:
		return c.exprFromLLM(n.Operation())
	default:
		return false
	}
}

func (c *semanticCollector) llmFieldPath(expr *ast.FieldAccessExpr) (string, bool) {
	fields := []string{expr.FieldName()}
	var root ast.Expr = expr.Obj()
	for {
		field, ok := root.(*ast.FieldAccessExpr)
		if !ok {
			break
		}
		fields = append(fields, field.FieldName())
		root = field.Obj()
	}
	if !c.exprFromLLM(root) {
		return "", false
	}
	for left, right := 0, len(fields)-1; left < right; left, right = left+1, right-1 {
		fields[left], fields[right] = fields[right], fields[left]
	}
	return strings.Join(fields, "."), true
}

func (c *semanticCollector) typeFingerprint(ref ast.TypeRef, seen map[string]bool) string {
	switch n := ref.(type) {
	case *ast.PrimTypeRef:
		return n.Name()
	case *ast.FileTypeRef:
		return "file"
	case *ast.ArrayTypeRef:
		return "array<" + c.typeFingerprint(n.Element(), seen) + ">"
	case *ast.MapTypeRef:
		return "map<" + c.typeFingerprint(n.Key(), seen) + "," + c.typeFingerprint(n.Value(), seen) + ">"
	case *ast.OptionalTypeRef:
		return "optional<" + c.typeFingerprint(n.Value(), seen) + ">"
	case *ast.NamedTypeRef:
		if seen[n.Name()] {
			return "recursive"
		}
		if decl := c.typeDecls[n.Name()]; decl != nil {
			next := cloneSeen(seen)
			next[n.Name()] = true
			fields := make([]string, 0, len(decl.Fields()))
			for _, field := range decl.Fields() {
				fields = append(fields, field.Name()+":"+c.typeFingerprint(field.TypeRef(), next))
			}
			return "record{" + strings.Join(fields, ",") + "}"
		}
		if decl := c.enumDecls[n.Name()]; decl != nil {
			members := make([]string, 0, len(decl.Members()))
			for _, member := range decl.Members() {
				members = append(members, member.Name())
			}
			return "enum{" + strings.Join(members, ",") + "}"
		}
		return "named"
	default:
		return fmt.Sprintf("unknown-type:%T", ref)
	}
}

func cloneSeen(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in)+1)
	for key, value := range in {
		out[key] = value
	}
	return out
}

func isLLMCall(call *ast.CallExpr) bool {
	id, ok := call.Func().(*ast.IdentExpr)
	return ok && id.Name() == "llm"
}

func isStringType(ref ast.TypeRef) bool {
	primitive, ok := ref.(*ast.PrimTypeRef)
	return ok && primitive.Name() == "string"
}
