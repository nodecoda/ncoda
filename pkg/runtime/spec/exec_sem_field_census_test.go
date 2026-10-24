package spec

// exec_sem_field_census_test.go —— ES-095 field-level schema diff (P0).
//
// ES-095 asks for a **field** census, not the wire census that
// platforms/cozebiz/decompile/exec_sem_census_test.go already emits: every
// exported field of the kernel IR package and of this spec package must be
// listed with three columns —
//
//	fact_class ∈ {F1..F6}   (docs/five-step/EXEC-SEMANTICS.md §1)
//	mapping                 (the projection that consumes the field), or
//	not-projected + gap     (why the field never reaches the execution face)
//	evidence                (a resolvable pointer, §3.2 shape)
//
// Enumeration is source-level reflection: Go's runtime reflect cannot list
// package-scope type names, so the package structs are read with go/parser
// (the same technique ES-096/ES-105 use) and every exported field of every
// exported struct type is emitted exactly once. Each exported struct type is
// enumerated under its own name — including types reachable only through
// nesting (e.g. LLMMetadataConfig.Usage) — so a field is never duplicated
// per reachability path; `path` is `Type.Field` for spec and `ir.Type.Field`
// for the kernel, matching the ES-095 examples.
//
// Classification is ruled (first match wins, `censusRules`), and every rule
// is reviewable in one table. The rules follow EXEC-SEMANTICS §1 + §1.1:
//
//	R0 (fallback)  F1 — the value is observable in the source artifact/IR
//	               (§1.1 rule 1: "值在源 artifact/IR 里可见 ⇒ F1"). This is
//	               the home for declared payloads of both packages.
//	R1             F6, not-projected — compiler-internal surfaces (Builder,
//	               error types, Placement/BindingUsage records) are harness
//	               state and never project into spec/execution.
//	R2             F6 — version facts (§1: "engine version ⇒ F6"; §2.2 pins
//	               spec.version as the schema version).
//	R3             F4 — host/client protocol (HTTP method/url/body/headers);
//	               §1.1 rule 4 + §4.1: platform physical stays in the adapter.
//	R4             F3 — platform node-level policy defaults (retry/timeout,
//	               agent iteration/memory, QA wait, extraction memory).
//	R5             F5 — control/graph semantics: edges, ports, branches,
//	               loops, merge/phi, flow exit (§1.1 rule 5).
//	R6             F2 — fields whose *absence* is resolved by the language
//	               zero/nil contract (defaults, optional refs/tables, nil
//	               policy pointers); §1 F2 owns "平台缺失/nil 静默吸收".
//
// The F1 fallback is deliberate: any new field is classified F1 and the
// golden `fields` list then differs, so the test is red until the new field
// is reviewed. An unclassified field can never be green.
//
// Ratchet: the checked-in JSON pins `ratchet.{total,not_projected,ratio}`.
// The test (and scripts/five-step/exec_sem_field_diff.py) fails when the
// current enumeration shrinks below the ratchet, when the not-projected
// ratio rises, or when every field is not-projected (ratio < 1.0 and each of
// F1..F6 ≥ class_floor). Regenerate only with an explicit rebase:
//
//	EXEC_SEM_FIELD_CENSUS_REBASE=1 go test ./internal/runtime/spec -run ExecSem095
//
// The file is never rewritten implicitly: a new field must be reviewed.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const execSem095CensusPath = "testdata/exec-semantics/field-census.json"

const (
	execSem095IRModule   = "github.com/nodecoda/ncoda/pkg/ir"
	execSem095SpecModule = "github.com/nodecoda/ncoda/pkg/runtime/spec"

	// evidenceF* are the owner pointers from EXEC-SEMANTICS §1's owner matrix;
	// every mapped row must carry one of these so a reviewer can walk from a
	// field row to the class definition in one hop.
	evF1 = "hand:docs/coze-biz-node-encoding.md:91"
	evF2 = "hand:docs/PROJECT-CONTRACT.md:419"
	evF3 = "platform_model.yaml#node_kinds"
	evF4 = "hand:internal/runtime/nodes/http.go:159"
	evF5 = "hand:internal/obligations/model.go:1"
	evF6 = "hand:internal/cli/run.go:748"

	mappingSpec = "spec.DecodeJSON/DecodeConfig → runtime consumer"
	mappingIR   = "pkg/ir → cozebiz lowering → spec projection"
)

// execSem095FactClasses is the closed set of ES-095 fact classes.
var execSem095FactClasses = []string{"F1", "F2", "F3", "F4", "F5", "F6"}

// fieldCensusRow is one field of the ES-095 census.
type fieldCensusRow struct {
	Pkg          string `json:"pkg"`
	Path         string `json:"path"`
	FactClass    string `json:"fact_class"`
	Mapping      string `json:"mapping,omitempty"`
	NotProjected bool   `json:"not_projected,omitempty"`
	Gap          string `json:"gap,omitempty"`
	Evidence     string `json:"evidence"`
	Rule         string `json:"rule"`
}

// fieldCensusTotals is both the current count block and the ratchet record.
type fieldCensusTotals struct {
	Total        int            `json:"total"`
	NotProjected int            `json:"not_projected"`
	Ratio        float64        `json:"ratio"`
	ByFactClass  map[string]int `json:"by_fact_class"`
}

type fieldCensusDoc struct {
	SchemaVersion int `json:"schema_version"`
	GeneratedFrom struct {
		IRModule    string `json:"ir_module"`
		SpecPackage string `json:"spec_package"`
	} `json:"generated_from"`
	Ratchet    fieldCensusTotals `json:"ratchet"`
	ClassFloor map[string]int    `json:"class_floor"`
	Totals     fieldCensusTotals `json:"totals"`
	Fields     []fieldCensusRow  `json:"fields"`
}

// censusRule is one classification rule. `owner`/`field` are comma-separated
// exact names or "*"/"" for any. A rule without mapping classifies as
// not-projected and must carry a gap.
type censusRule struct {
	id       string
	pkg      string // "spec" | "ir" | "" (any)
	owner    string
	field    string
	class    string
	mapping  string
	gap      string
	evidence string // optional override; defaults to the class pointer
}

func censusMatches(pattern, value string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	for _, p := range strings.Split(pattern, ",") {
		if strings.TrimSpace(p) == value {
			return true
		}
	}
	return false
}

// censusRules is ordered; the first matching rule wins.
var censusRules = []censusRule{
	// R1 — compiler-internal harness surfaces: never projected.
	{id: "R1-harness-structs", pkg: "ir", owner: "Builder,UnsupportedError,LanguageError,MissingDeclarationError,Placement,BindingUsage", field: "*", class: "F6",
		gap: "compile-time harness/diagnostic surface; not a runtime fact and never projected into spec or the artifact"},
	{id: "R1-graph-records", pkg: "ir", owner: "Graph", field: "Placements,AllocatedNodeIDs", class: "F6",
		gap: "compile-time audit record (placement decisions / id-space bookkeeping); backend never consumes it"},
	{id: "R1-unconsumed-file-constraints", pkg: "ir", owner: "Type", field: "AllowedFileExtensions,AllowedFileUploadMethods", class: "F1",
		gap: "populated by the IR builder but has no consumer in spec or the cozebiz lowering; EXEC-SEMANTICS §4.1 / spec.TypeSpec exclude file-upload constraints from the execution subset (AllowedFileTypes stays mapped: render_llm.go consumes it)"},

	// R2 — version facts.
	{id: "R2-spec-schema-version", pkg: "spec", owner: "Spec", field: "Version", class: "F6",
		mapping: "spec.DecodeJSON version-first dispatch (schema version)", evidence: "hand:internal/runtime/spec/build.go:810"},

	// R3 — host/client protocol (F4).
	{id: "R3-http-spec", pkg: "spec", owner: "HTTPConfig", field: "Method,URL,Body", class: "F4",
		mapping: "runtime http node transport (host adapter)", evidence: evF4},
	{id: "R3-http-ir", pkg: "ir", owner: "HTTPCall", field: "Method,URL,Body,Headers", class: "F4",
		mapping: "cozebiz http lowering (host adapter)", evidence: evF4},
	{id: "R3-http-header-ir", pkg: "ir", owner: "HTTPHeader", field: "*", class: "F4",
		mapping: "cozebiz http lowering (host adapter)", evidence: evF4},

	// R4 — platform node-level policy defaults (F3).
	// DataOnErr is the carried `with default(<record>)` payload, i.e. a source
	// fact resolved like ir.ExceptionConfig.DefaultFields — not a node-kind
	// policy default. Must precede the ExceptionConfig catch-all.
	{id: "R6-default-fields-spec", pkg: "spec", owner: "ExceptionConfig", field: "DataOnErr", class: "F2",
		mapping: "resolved error-policy defaults", evidence: evF2},
	{id: "R4-exception-spec", pkg: "spec", owner: "ExceptionConfig", field: "*", class: "F3",
		mapping: "runtime retry/timeout/error-policy resolution", evidence: evF3},
	{id: "R4-exception-ir", pkg: "ir", owner: "ExceptionConfig", field: "MaxRetry,RetryIntervalMS,TimeoutMS,ProcessType,FailBranch", class: "F3",
		mapping: "runtime retry/timeout/error-policy resolution", evidence: evF3},
	{id: "R4-agent-spec", pkg: "spec", owner: "AgentConfig", field: "MaxIteration,Memory,MemoryWindow,Strategy", class: "F3",
		mapping: "runtime agent policy defaults", evidence: evF3},
	{id: "R4-agent-ir", pkg: "ir", owner: "AgentCall", field: "MaxIteration,Memory,MemoryWindow,Strategy", class: "F3",
		mapping: "runtime agent policy defaults", evidence: evF3},
	{id: "R4-qa-spec", pkg: "spec", owner: "QaConfig", field: "Timeout", class: "F3",
		mapping: "runtime question-answer wait policy", evidence: evF3},
	{id: "R4-qa-ir", pkg: "ir", owner: "QA", field: "Timeout,TimeoutUnit,MaxAnswerCount", class: "F3",
		mapping: "runtime question-answer wait policy", evidence: evF3},
	{id: "R4-extraction-memory", pkg: "ir", owner: "ExtractionSpec", field: "MemoryTurns", class: "F3",
		mapping: "runtime extraction memory policy", evidence: evF3},

	// R5 — control/graph semantics (F5).
	{id: "R5-spec-edges", pkg: "spec", owner: "Spec", field: "Edges", class: "F5",
		mapping: "graph build (edges)", evidence: evF5},
	{id: "R5-edge-spec", pkg: "spec", owner: "EdgeSpec", field: "*", class: "F5",
		mapping: "graph build (arcs/ports)", evidence: evF5},
	{id: "R5-loop-exit-spec", pkg: "spec", owner: "BreakConfig,ContinueConfig,LoopReturnConfig", field: "*", class: "F5",
		mapping: "loop control lowering", evidence: evF5},
	{id: "R5-selector-spec", pkg: "spec", owner: "SelectorConfig", field: "*", class: "F5",
		mapping: "branch selection lowering", evidence: evF5},
	{id: "R5-condition-operator", pkg: "", owner: "Condition", field: "Operator", class: "F5",
		mapping: "branch selection lowering", evidence: evF5},
	{id: "R5-aggregate-spec", pkg: "spec", owner: "AggregateConfig,AggregateGroup", field: "*", class: "F5",
		mapping: "merge/phi lowering", evidence: evF5},
	{id: "R5-aggregate-ir", pkg: "ir", owner: "Aggregate,AggregateGroup", field: "*", class: "F5",
		mapping: "merge/phi lowering", evidence: evF5},
	{id: "R5-loop-spec", pkg: "spec", owner: "LoopConfig", field: "LoopType,LoopCount,InputArrays,FlattenOutput,IsParallel,ParallelNums,ErrorHandleMode,Children,InnerEdges,ReturnValue", class: "F5",
		mapping: "loop control lowering", evidence: evF5},
	{id: "R5-loop-ir", pkg: "ir", owner: "Loop", field: "LoopType,LoopCount,InputArrays,FlattenOutput,FlattenPorts,IsParallel,ParallelNums,ErrorHandleMode,ReturnValue", class: "F5",
		mapping: "loop control lowering", evidence: evF5},
	{id: "R5-assign-loop", pkg: "", owner: "AssignConfig,Assign", field: "LoopState,LoopID", class: "F5",
		mapping: "loop state lowering", evidence: evF5},
	{id: "R5-switch-spec", pkg: "spec", owner: "SwitchConfig", field: "Intents,Mode", class: "F5",
		mapping: "switch/intent routing lowering", evidence: evF5},
	{id: "R5-switch-ir", pkg: "ir", owner: "Switch", field: "Intents,Mode", class: "F5",
		mapping: "switch/intent routing lowering", evidence: evF5},
	{id: "R5-end-ir", pkg: "ir", owner: "End", field: "Implicit,FlowExitMerge", class: "F5",
		mapping: "flow-exit lowering", evidence: evF5},
	{id: "R5-graph-root", pkg: "ir", owner: "Graph", field: "Root", class: "F5",
		mapping: "structured program lowering", evidence: evF5},
	{id: "R5-statements", pkg: "ir", owner: "Block,NodeStmt,IfStmt,ChainArm,AttemptStmt,LoopStmt,ParallelStmt,QaBranch,QaStmt,SwitchBranch,SwitchStmt,ReturnStmt,BreakStmt,ContinueStmt,InlineStmt,Selector,BreakNode,ContinueNode,LoopReturnNode", field: "*", class: "F5",
		mapping: "control/graph lowering", evidence: evF5},

	// R6 — absence resolved by the language zero/nil contract (F2).
	{id: "R6-default-spec", pkg: "spec", owner: "ParamSpec,ConvVarSpec", field: "Default", class: "F2",
		mapping: "zero-value default", evidence: evF2},
	{id: "R6-default-ir", pkg: "ir", owner: "Param,ConvVar", field: "Default", class: "F2",
		mapping: "zero-value default", evidence: evF2},
	{id: "R6-default-fields", pkg: "ir", owner: "ExceptionConfig", field: "DefaultFields", class: "F2",
		mapping: "resolved error-policy defaults", evidence: evF2},
	{id: "R6-source-val", pkg: "", owner: "Source", field: "Val", class: "F2",
		mapping: "nil literal absorbed by the consumer", evidence: evF2},
	{id: "R6-operand-val", pkg: "", owner: "Operand", field: "Val", class: "F2",
		mapping: "nil literal absorbed by the consumer", evidence: evF2},
	{id: "R6-type-optional", pkg: "", owner: "TypeSpec,Type", field: "Elem,Props", class: "F2",
		mapping: "nil element/props ⇒ scalar/empty (zero contract)", evidence: evF2},
	{id: "R6-exception-nil", pkg: "spec", owner: "LLMConfig,HTTPConfig,ToolConfig,AgentConfig,CodeConfig", field: "Exception", class: "F2",
		mapping: "nil ⇒ no retry/timeout policy", evidence: evF2},
	{id: "R6-exception-nil-ir", pkg: "ir", owner: "LLMCall,HTTPCall,ToolCall,SubWorkflowCall", field: "Exception", class: "F2",
		mapping: "nil ⇒ no retry/timeout policy", evidence: evF2},
	{id: "R6-output-format", pkg: "", owner: "LLMConfig,LLMCall", field: "OutputFormat,ResponseFormat", class: "F2",
		mapping: "empty/0 ⇒ default response shape", evidence: evF2},
	{id: "R6-query-name", pkg: "", owner: "KnowledgeConfig,KnowledgeCall", field: "QueryName", class: "F2",
		mapping: "empty ⇒ default query", evidence: evF2},
	{id: "R6-nested-refs", pkg: "spec", owner: "ToolConfig", field: "NestedRefs", class: "F2",
		mapping: "empty ⇒ no nested refs", evidence: evF2},
	{id: "R6-qa-template", pkg: "spec", owner: "QaConfig", field: "Template", class: "F2",
		mapping: "empty ⇒ no template", evidence: evF2},
	{id: "R6-space-id", pkg: "", owner: "SubWorkflowConfig,SubWorkflowCall", field: "SpaceID", class: "F2",
		mapping: "empty ⇒ declaration default", evidence: evF2},
	{id: "R6-switch-params", pkg: "", owner: "SwitchConfig,Switch", field: "Temperature,MaxTokens,TopP,SystemPrompt", class: "F2",
		mapping: "zero/nil ⇒ model default", evidence: evF2},
	{id: "R6-loop-tables", pkg: "", owner: "LoopConfig,Loop", field: "VarName,Types,StateTypes,StateValues,OutTypes", class: "F2",
		mapping: "absent ⇒ empty table", evidence: evF2},
	{id: "R6-extraction-vision", pkg: "ir", owner: "ExtractionSpec", field: "Vision", class: "F2",
		mapping: "nil ⇒ text-only extraction", evidence: evF2},
	{id: "R6-payload-schema", pkg: "ir", owner: "LLMResultContract", field: "PayloadSchema", class: "F2",
		mapping: "nil ⇒ untyped payload", evidence: evF2},
	{id: "R6-end-answer", pkg: "ir", owner: "End", field: "Answer", class: "F2",
		mapping: "nil ⇒ plain end node", evidence: evF2},
}

// srcField is one exported field read from source.
type srcField struct {
	owner string
	name  string
	file  string
	line  int
}

// loadSourceFields reads every exported field of every exported struct type
// in dir. Test files are ignored: the census covers the shipped surface.
func loadSourceFields(t *testing.T, dir string) []srcField {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	var out []srcField
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				collectSrcFields(ts.Name.Name, "", st, name, fset, &out)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].owner != out[j].owner {
			return out[i].owner < out[j].owner
		}
		return out[i].name < out[j].name
	})
	return out
}

func collectSrcFields(owner, prefix string, st *ast.StructType, file string, fset *token.FileSet, out *[]srcField) {
	for _, fl := range st.Fields.List {
		var names []string
		var embedded *ast.StructType
		if len(fl.Names) == 0 {
			// Embedded field: record the exported type name as the field.
			if id := embeddedFieldName(fl.Type); id != "" {
				names = append(names, id)
			}
		}
		for _, id := range fl.Names {
			if id.IsExported() {
				names = append(names, id.Name)
			}
		}
		for _, n := range names {
			path := n
			if prefix != "" {
				path = prefix + "." + n
			}
			*out = append(*out, srcField{owner: owner, name: path, file: file, line: fset.Position(fl.Pos()).Line})
			// Anonymous struct fields keep their sub-fields in the same row space.
			if sub, ok := fl.Type.(*ast.StructType); ok {
				embedded = sub
			}
			if embedded != nil {
				collectSrcFields(owner, path, embedded, file, fset, out)
			}
		}
	}
}

func embeddedFieldName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		if e.IsExported() {
			return e.Name
		}
	case *ast.StarExpr:
		return embeddedFieldName(e.X)
	case *ast.SelectorExpr:
		if e.Sel.IsExported() {
			return e.Sel.Name
		}
	}
	return ""
}

// classifyCensus returns the rule-matched class row for one field.
func classifyCensus(pkg string, f srcField) fieldCensusRow {
	path := f.owner + "." + f.name
	if pkg == "ir" {
		path = "ir." + path
	}
	row := fieldCensusRow{Pkg: pkg, Path: path}
	for i := range censusRules {
		r := &censusRules[i]
		if r.pkg != "" && r.pkg != pkg {
			continue
		}
		if !censusMatches(r.owner, f.owner) || !censusMatches(r.field, f.name) {
			continue
		}
		row.Rule = r.id
		row.FactClass = r.class
		if r.mapping != "" {
			row.Mapping = r.mapping
		} else {
			row.NotProjected = true
			row.Gap = r.gap
		}
		row.Evidence = r.evidence
		if row.Evidence == "" {
			row.Evidence = classEvidence(r.class)
		}
		if row.NotProjected {
			if pkg == "ir" {
				row.Evidence = fmt.Sprintf("oss:v0.3.0:pkg/ir/%s:%d", f.file, f.line)
			} else {
				row.Evidence = fmt.Sprintf("hand:internal/runtime/spec/%s:%d", f.file, f.line)
			}
		}
		return row
	}
	// R0 fallback: a visible declared value (EXEC-SEMANTICS §1.1 rule 1).
	row.Rule = "R0-declared"
	row.FactClass = "F1"
	if pkg == "ir" {
		row.Mapping = mappingIR
	} else {
		row.Mapping = mappingSpec
	}
	row.Evidence = evF1
	return row
}

func classEvidence(class string) string {
	switch class {
	case "F1":
		return evF1
	case "F2":
		return evF2
	case "F3":
		return evF3
	case "F4":
		return evF4
	case "F5":
		return evF5
	case "F6":
		return evF6
	}
	return ""
}

func buildFieldCensus(t *testing.T) []fieldCensusRow {
	t.Helper()
	irDir, err := goListDir(execSem095IRModule)
	if err != nil {
		t.Fatalf("locate pkg/ir: %v", err)
	}
	specDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolve spec dir: %v", err)
	}

	var rows []fieldCensusRow
	for _, f := range loadSourceFields(t, specDir) {
		rows = append(rows, classifyCensus("spec", f))
	}
	for _, f := range loadSourceFields(t, irDir) {
		rows = append(rows, classifyCensus("ir", f))
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Pkg != rows[j].Pkg {
			return rows[i].Pkg < rows[j].Pkg
		}
		return rows[i].Path < rows[j].Path
	})
	return rows
}

func censusTotals(rows []fieldCensusRow) fieldCensusTotals {
	tot := fieldCensusTotals{Total: len(rows), ByFactClass: map[string]int{}}
	for _, c := range execSem095FactClasses {
		tot.ByFactClass[c] = 0
	}
	for _, r := range rows {
		tot.ByFactClass[r.FactClass]++
		if r.NotProjected {
			tot.NotProjected++
		}
	}
	if tot.Total > 0 {
		tot.Ratio = math.Round(float64(tot.NotProjected)/float64(tot.Total)*1e6) / 1e6
	}
	return tot
}

// TestExecSem095FieldCensus is the ES-095 generator and its golden gate.
func TestExecSem095FieldCensus(t *testing.T) {
	rows := buildFieldCensus(t)
	totals := censusTotals(rows)

	doc := fieldCensusDoc{
		SchemaVersion: 1,
		ClassFloor: map[string]int{
			"F1": 1, "F2": 1, "F3": 1, "F4": 1, "F5": 1, "F6": 1,
		},
		Totals: totals,
		Fields: rows,
	}
	doc.GeneratedFrom.IRModule = execSem095IRModule
	doc.GeneratedFrom.SpecPackage = execSem095SpecModule
	doc.Ratchet = totals // ratchet defaults to the first enumeration

	rebase := os.Getenv("EXEC_SEM_FIELD_CENSUS_REBASE") == "1"
	old, readErr := os.ReadFile(execSem095CensusPath)
	if readErr == nil {
		var prev fieldCensusDoc
		if err := json.Unmarshal(old, &prev); err != nil {
			t.Fatalf("decode %s: %v", execSem095CensusPath, err)
		}
		if !rebase {
			doc.Ratchet = prev.Ratchet
			if len(prev.ClassFloor) > 0 {
				doc.ClassFloor = prev.ClassFloor
			}
		}
	}

	validateFieldCensus(t, doc)

	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal census: %v", err)
	}
	raw = append(raw, '\n')

	if rebase {
		if err := os.MkdirAll(filepath.Dir(execSem095CensusPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(execSem095CensusPath), err)
		}
		if err := os.WriteFile(execSem095CensusPath, raw, 0o644); err != nil {
			t.Fatalf("write %s: %v", execSem095CensusPath, err)
		}
		t.Logf("ES-095 field census rebased: %d fields (%s)", totals.Total, execSem095CensusPath)
		return
	}
	if readErr != nil {
		t.Fatalf("ES-095 census golden missing (%s); generate it with EXEC_SEM_FIELD_CENSUS_REBASE=1", execSem095CensusPath)
	}

	var prev fieldCensusDoc
	if err := json.Unmarshal(old, &prev); err != nil {
		t.Fatalf("decode %s: %v", execSem095CensusPath, err)
	}
	if len(prev.Fields) == 0 {
		t.Fatalf("ES-095 census golden has no fields; regenerate with EXEC_SEM_FIELD_CENSUS_REBASE=1")
	}
	if !bytes.Equal(mustJSON(t, prev.Fields), mustJSON(t, doc.Fields)) {
		t.Fatalf("ES-095 field census drift: source enumerates %d fields, golden has %d. "+
			"Review the new/changed field rows, then regenerate with EXEC_SEM_FIELD_CENSUS_REBASE=1",
			len(doc.Fields), len(prev.Fields))
	}
	t.Logf("ES-095 field census ok: total=%d not_projected=%d ratio=%.4f F1=%d F2=%d F3=%d F4=%d F5=%d F6=%d",
		totals.Total, totals.NotProjected, totals.Ratio,
		totals.ByFactClass["F1"], totals.ByFactClass["F2"], totals.ByFactClass["F3"],
		totals.ByFactClass["F4"], totals.ByFactClass["F5"], totals.ByFactClass["F6"])
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// validateFieldCensus enforces ES-095's three columns and the count ratchet.
func validateFieldCensus(t *testing.T, doc fieldCensusDoc) {
	t.Helper()
	if len(doc.Fields) == 0 {
		t.Fatalf("ES-095: field enumeration is empty")
	}
	if doc.Totals.Total != len(doc.Fields) {
		t.Fatalf("ES-095: totals.total=%d but %d fields enumerated", doc.Totals.Total, len(doc.Fields))
	}
	legal := map[string]bool{}
	for _, c := range execSem095FactClasses {
		legal[c] = true
	}
	seen := map[string]bool{}
	np := 0
	counts := map[string]int{}
	for _, row := range doc.Fields {
		if row.Pkg != "spec" && row.Pkg != "ir" {
			t.Errorf("ES-095: %s: unknown pkg %q", row.Path, row.Pkg)
		}
		if row.Path == "" {
			t.Errorf("ES-095: empty path in row %+v", row)
		}
		if seen[row.Pkg+"/"+row.Path] {
			t.Errorf("ES-095: duplicate field row %s/%s", row.Pkg, row.Path)
		}
		seen[row.Pkg+"/"+row.Path] = true
		if !legal[row.FactClass] {
			t.Errorf("ES-095: %s: missing/illegal fact_class %q", row.Path, row.FactClass)
			continue
		}
		counts[row.FactClass]++
		switch {
		case row.NotProjected:
			np++
			if row.Mapping != "" {
				t.Errorf("ES-095: %s: not-projected row must not carry mapping", row.Path)
			}
			if strings.TrimSpace(row.Gap) == "" {
				t.Errorf("ES-095: %s: not-projected row missing gap", row.Path)
			}
		default:
			if strings.TrimSpace(row.Mapping) == "" {
				t.Errorf("ES-095: %s: mapped row missing mapping", row.Path)
			}
			if row.Gap != "" {
				t.Errorf("ES-095: %s: mapped row must not carry gap", row.Path)
			}
		}
		if strings.TrimSpace(row.Evidence) == "" {
			t.Errorf("ES-095: %s: missing evidence", row.Path)
		}
	}

	if doc.Totals.NotProjected != np {
		t.Errorf("ES-095: totals.not_projected=%d but %d rows are not-projected", doc.Totals.NotProjected, np)
	}
	for c, n := range counts {
		if doc.Totals.ByFactClass[c] != n {
			t.Errorf("ES-095: by_fact_class[%s]=%d but %d rows", c, doc.Totals.ByFactClass[c], n)
		}
	}
	wantRatio := math.Round(float64(np)/float64(len(doc.Fields))*1e6) / 1e6
	if math.Abs(doc.Totals.Ratio-wantRatio) > 1e-9 {
		t.Errorf("ES-095: totals.ratio=%.6f want %.6f", doc.Totals.Ratio, wantRatio)
	}
	// All-not-projected (or a ratio that leaves the execution face empty) is
	// self-certification, not a census.
	if doc.Totals.Ratio >= 1.0 {
		t.Errorf("ES-095: not_projected ratio %.4f >= 1.0 (all fields unprojected)", doc.Totals.Ratio)
	}
	for _, c := range execSem095FactClasses {
		floor, ok := doc.ClassFloor[c]
		if !ok {
			floor = 1
		}
		if counts[c] < floor {
			t.Errorf("ES-095: fact class %s has %d fields, below class_floor %d (no written exemption)", c, counts[c], floor)
		}
	}
	// Ratchet: totals may only grow; not-projected count/ratio may not.
	if doc.Totals.Total < doc.Ratchet.Total {
		t.Errorf("ES-095: total %d fell below ratchet %d", doc.Totals.Total, doc.Ratchet.Total)
	}
	if doc.Totals.NotProjected > doc.Ratchet.NotProjected {
		t.Errorf("ES-095: not_projected %d rose above ratchet %d", doc.Totals.NotProjected, doc.Ratchet.NotProjected)
	}
	if doc.Totals.Ratio > doc.Ratchet.Ratio+1e-9 {
		t.Errorf("ES-095: not_projected ratio %.6f rose above ratchet %.6f", doc.Totals.Ratio, doc.Ratchet.Ratio)
	}
	if doc.Totals.Total == 0 {
		t.Fatalf("ES-095: zero fields enumerated")
	}
	// The emitted row schema is part of the ES-095 contract: pin the JSON
	// keys with reflect so a renamed tag cannot silently break the Python
	// judge (which reads these keys by name).
	wantTags := []string{"pkg", "path", "fact_class", "mapping", "not_projected", "gap", "evidence"}
	haveTags := map[string]bool{}
	rowType := reflect.TypeOf(fieldCensusRow{})
	for i := 0; i < rowType.NumField(); i++ {
		haveTags[strings.Split(rowType.Field(i).Tag.Get("json"), ",")[0]] = true
	}
	for _, tag := range wantTags {
		if !haveTags[tag] {
			t.Errorf("ES-095: fieldCensusRow is missing the %q JSON key", tag)
		}
	}
}
