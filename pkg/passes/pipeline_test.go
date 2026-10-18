package passes

// Ports of lang/tests/lang/spec_lock/test_dc_diagnostic_codes.py (existence +
// name/value parity against pkg/diag — the 58-code enum lives in
// pkg/diag since Task 2, per the task-12 brief correction) plus
// self-built coverage for the frontend pipeline: empty-program assembly,
// lexer/parser error short-circuit, Bind diagnostic passthrough, pass-order
// determinism, registry mechanics (registered runs / unregistered skips /
// error short-circuit / StageScope reservation).

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/diag"
)

// dcCorrectnessCodes mirrors the first spec_lock test: the 15 DC correctness
// allocations must keep stable values and E-labels.
func TestDCCorrectnessDiagnosticAllocationsAreStable(t *testing.T) {
	got := map[diag.Code]string{}
	for _, code := range []diag.Code{
		diag.RECURSIVE_CALL, diag.TARGET_NOT_LOWERABLE,
		diag.PARALLEL_EXPORT_CONFLICT, diag.PARALLEL_ACCESS_HAZARD,
		diag.PARALLEL_EXTERNAL_EXIT, diag.DUPLICATE_TYPE,
		diag.OPTIONAL_REQUIRED, diag.ANY_NOT_NARROWED,
		diag.EMPTY_AGGREGATE_TYPE, diag.EFFECT_CONTEXT,
		diag.INVALID_STATE, diag.YIELD_CONTRACT,
		diag.RESPONSE_CONTEXT, diag.OPERATION_POLICY,
		diag.LANGUAGE_IDENTITY, diag.CHATFLOW_ENTRY,
	} {
		got[code] = codeLabel(code)
	}
	want := map[diag.Code]string{
		diag.RECURSIVE_CALL: "E1033", diag.TARGET_NOT_LOWERABLE: "E1034",
		diag.PARALLEL_EXPORT_CONFLICT: "E1035", diag.PARALLEL_ACCESS_HAZARD: "E1036",
		diag.PARALLEL_EXTERNAL_EXIT: "E1037", diag.DUPLICATE_TYPE: "E1038",
		diag.OPTIONAL_REQUIRED: "E1039", diag.ANY_NOT_NARROWED: "E1040",
		diag.EMPTY_AGGREGATE_TYPE: "E1049", diag.EFFECT_CONTEXT: "E1041",
		diag.INVALID_STATE: "E1042", diag.YIELD_CONTRACT: "E1043",
		diag.RESPONSE_CONTEXT: "E1044", diag.OPERATION_POLICY: "E1045",
		diag.LANGUAGE_IDENTITY: "E1050", diag.CHATFLOW_ENTRY: "E1054",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DC correctness allocations drifted:\n got %v\nwant %v", got, want)
	}
}

// allDiagnosticCodes 是**去重后存活**的码表（原样镜像已退役的 base.py
// DSLDiagnosticCode，2026-09-17 去掉死常量 OUTPUT_KEY_TYPE/E1017：该码没有任何
// 发射点，且外部语言标准已随 @answer 族一并删除 E1017 —— 标准 ↔ 实现同步）。
var allDiagnosticCodes = []diag.Code{
	diag.SYNTAX_ERROR, diag.UNDEFINED_VAR, diag.DUPLICATE_DEF, diag.TYPE_MISMATCH,
	diag.ARG_COUNT, diag.RETURN_TYPE, diag.NOT_CALLABLE, diag.NOT_ITERABLE,
	diag.CONDITION_NOT_BOOL, diag.NO_ENTRY, diag.MISSING_RETURN, diag.UNREACHABLE_CODE,
	diag.BINARY_TYPE, diag.INDEX_TYPE, diag.FIELD_NOT_FOUND, diag.UNKNOWN_TYPE,
	diag.NOT_IN_LOOP, diag.ASSIGN_IMMUTABLE, diag.INVALID_MODE,
	diag.ENTRY_NO_RETURN, diag.CODE_INPUT_TYPE, diag.CODE_INPUT_SELECTOR,
	diag.CODE_DUPLICATE_INPUT, diag.CODE_SOURCE_REQUIRED, diag.CODE_OUTPUT_INVALID,
	diag.CODE_DUPLICATE_OUTPUT, diag.CODE_LANGUAGE_UNSUPPORTED, diag.CODE_SOURCE_INVALID,
	diag.CODE_PORT_INVALID, diag.STDLIB_STATIC_ARGUMENT, diag.STDLIB_CALL_CONTEXT,
	diag.RECURSIVE_CALL, diag.TARGET_NOT_LOWERABLE, diag.PARALLEL_EXPORT_CONFLICT,
	diag.PARALLEL_ACCESS_HAZARD, diag.PARALLEL_EXTERNAL_EXIT, diag.DUPLICATE_TYPE,
	diag.OPTIONAL_REQUIRED, diag.ANY_NOT_NARROWED, diag.EFFECT_CONTEXT, diag.INVALID_STATE,
	diag.YIELD_CONTRACT, diag.RESPONSE_CONTEXT, diag.OPERATION_POLICY,
	diag.TARGET_PROFILE_UNKNOWN, diag.SELECTOR_NO_PRODUCER, diag.UNINITIALIZED_USE,
	diag.EMPTY_AGGREGATE_TYPE, diag.LANGUAGE_IDENTITY, diag.LOWERING_INVARIANT,
	diag.DEAD_CODE, diag.UNUSED_VAR, diag.EMPTY_ENTRY, diag.EMPTY_FOR_BODY,
	diag.CODE_SOURCE_CONTRACT_UNVERIFIED, diag.CODE_SOURCE_CONTRACT_MISMATCH,
	diag.ANY_FIELD_ACCESS,
}

// warningDiagnosticCodes mirrors base.py _WARNING_DIAGNOSTIC_CODES.
var warningDiagnosticCodes = map[diag.Code]bool{
	diag.DEAD_CODE: true, diag.UNUSED_VAR: true, diag.EMPTY_ENTRY: true,
	diag.EMPTY_FOR_BODY: true, diag.CODE_SOURCE_CONTRACT_UNVERIFIED: true,
	diag.CODE_SOURCE_CONTRACT_MISMATCH: true, diag.ANY_FIELD_ACCESS: true,
}

// TestDiagnosticCodesAreUniqueAndSeveritySplit ports the spec_lock uniqueness
// + metadata-severity tests: no duplicate values, and the warning set is exactly
// the seven 2xxx codes.
func TestDiagnosticCodesAreUniqueAndSeveritySplit(t *testing.T) {
	if len(allDiagnosticCodes) != 57 {
		t.Fatalf("allDiagnosticCodes has %d entries, want 57（58 = base.py 列表；2026-09-17 减去死常量 OUTPUT_KEY_TYPE）", len(allDiagnosticCodes))
	}
	seen := map[diag.Code]bool{}
	for _, code := range allDiagnosticCodes {
		if seen[code] {
			t.Errorf("duplicate diagnostic code %d", int(code))
		}
		seen[code] = true
		if wantErr := !warningDiagnosticCodes[code]; code.IsError() != wantErr {
			t.Errorf("code %d severity mismatch: IsError()=%v want %v", int(code), code.IsError(), wantErr)
		}
	}
}

func codeLabel(code diag.Code) string {
	if code.IsError() {
		return fmt.Sprintf("E%04d", int(code))
	}
	return fmt.Sprintf("W%04d", int(code))
}

// ── frontend pipeline ──────────────────────────────────────────────

func TestRunEmptyProgramAssemblesTypedBoundProgram(t *testing.T) {
	res := Run("", "empty.dc")
	if len(res.Diagnostics) != 0 {
		t.Fatalf("empty program produced diagnostics: %v", res.Diagnostics)
	}
	if res.HasErrors() {
		t.Fatal("empty program reported errors")
	}
	if res.Program == nil {
		t.Fatal("Run returned a nil TypedBoundProgram")
	}
	if res.Program.Snapshot == nil {
		t.Fatal("TypedBoundProgram.Snapshot is nil")
	}
	if res.Program.EntryFunction != 0 {
		t.Fatalf("EntryFunction = %d, want 0 (none)", int(res.Program.EntryFunction))
	}
}

func TestRunLexerErrorShortCircuits(t *testing.T) {
	p := New()
	rec := &recordingPass{}
	if err := p.Register(StageDesugar, rec); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res := p.Run(`"unterminated`, "bad.dc", nil)
	if res.Program != nil {
		t.Fatal("lexer failure must not assemble a TypedBoundProgram")
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Diagnostics[0].Code != diag.SYNTAX_ERROR {
		t.Fatalf("code = %v, want SYNTAX_ERROR", res.Diagnostics[0].Code)
	}
	if rec.called {
		t.Fatal("registered pass ran despite lexer failure (no short-circuit)")
	}
}

func TestRunParserErrorShortCircuits(t *testing.T) {
	p := New()
	rec := &recordingPass{}
	if err := p.Register(StageDesugar, rec); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res := p.Run("function main() { var x = 42;", "bad.dc", nil)
	if res.Program != nil {
		t.Fatal("parser failure must not assemble a TypedBoundProgram")
	}
	if len(res.Diagnostics) == 0 || res.Diagnostics[0].Code != diag.SYNTAX_ERROR {
		t.Fatalf("want a SYNTAX_ERROR diagnostic, got %v", res.Diagnostics)
	}
	if rec.called {
		t.Fatal("registered pass ran despite parser failure (no short-circuit)")
	}
}

func TestRunBindDiagnosticsPassthrough(t *testing.T) {
	res := Run("function main() { var x = y; }", "t.dc")
	if !res.HasErrors() {
		t.Fatal("expected errors for undefined variable")
	}
	if res.Program == nil || res.Program.Snapshot == nil {
		t.Fatal("bind errors must still publish a snapshot and typed program")
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(res.Diagnostics), res.Diagnostics)
	}
	d := res.Diagnostics[0]
	if d.Code != diag.UNDEFINED_VAR {
		t.Fatalf("code = %v, want UNDEFINED_VAR", d.Code)
	}
	if d.Line < 1 || d.Column < 1 {
		t.Fatalf("diagnostic lacks a source span: %+v", d)
	}
	if d.Filename != "t.dc" {
		t.Fatalf("Filename = %q, want t.dc", d.Filename)
	}
}

func TestRunDiagnosticsOrderDeterministic(t *testing.T) {
	src := "function main() { var a = q; var b = r; var c = s; }"
	r1 := Run(src, "t.dc")
	r2 := Run(src, "t.dc")
	if len(r1.Diagnostics) != 3 {
		t.Fatalf("got %d diagnostics, want 3: %v", len(r1.Diagnostics), r1.Diagnostics)
	}
	if !reflect.DeepEqual(r1.Diagnostics, r2.Diagnostics) {
		t.Fatalf("two runs produced different diagnostics:\n1: %v\n2: %v", r1.Diagnostics, r2.Diagnostics)
	}
}

// recordingPass records invocations; emit, when non-zero, is emitted on run.
type recordingPass struct {
	called bool
	emit   diag.Code
	order  *[]Stage
	stage  Stage
}

func (r *recordingPass) Run(prog *ast.Program, ctx *Context) {
	r.called = true
	if r.order != nil {
		*r.order = append(*r.order, r.stage)
	}
	if r.emit != 0 {
		ctx.Emit(r.emit, diag.Error, "boom", ast.Span{})
	}
}

func TestPipelineShortCircuitsOnError(t *testing.T) {
	p := New()
	boom := &recordingPass{emit: diag.TYPE_MISMATCH}
	after := &recordingPass{}
	if err := p.Register(StageDesugar, boom); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := p.Register(StageNormalize, after); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res := p.Run("function main() { var x = 1; }", "t.dc", nil)
	if !boom.called {
		t.Fatal("registered pass was not run")
	}
	if after.called {
		t.Fatal("later registered stage ran after an error (short-circuit broken)")
	}
	if !res.HasErrors() {
		t.Fatal("expected the pass diagnostic to surface")
	}
	if res.Diagnostics[0].Code != diag.TYPE_MISMATCH {
		t.Fatalf("first diagnostic = %v, want TYPE_MISMATCH", res.Diagnostics[0].Code)
	}
}

func TestPipelineRegisteredStagesRunInStageOrder(t *testing.T) {
	p := New()
	var order []Stage
	rec := func(stage Stage) *recordingPass { return &recordingPass{order: &order, stage: stage} }
	if err := p.Register(StageDefiniteInit, rec(StageDefiniteInit)); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := p.Register(StageEffect, rec(StageEffect)); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := p.Register(StageSyntax, rec(StageSyntax)); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res := p.Run("function main() { var x = 1; }", "t.dc", nil)
	if res.HasErrors() {
		t.Fatalf("unexpected errors: %v", res.Diagnostics)
	}
	want := []Stage{StageDefiniteInit, StageEffect, StageSyntax}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("stage execution order = %v, want %v", order, want)
	}
}

func TestUnregisteredStagesAreSkipped(t *testing.T) {
	p := New()
	var order []Stage
	r := &recordingPass{order: &order, stage: StageSemantic}
	if err := p.Register(StageSemantic, r); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res := p.Run("function main() { var x = 1; }", "t.dc", nil)
	if res.HasErrors() {
		t.Fatalf("unexpected errors: %v", res.Diagnostics)
	}
	if !reflect.DeepEqual(order, []Stage{StageSemantic}) {
		t.Fatalf("only the registered stage should run, got %v", order)
	}
}

func TestRegisterScopeStageIsAllowed(t *testing.T) {
	// Task 14: StageScope is no longer reserved; a caller may register a
	// custom scope pass (D1). The unregistered path falls back to the
	// default ScopePass instead.
	p := New()
	if err := p.Register(StageScope, &recordingPass{}); err != nil {
		t.Fatalf("Register(StageScope, ...) must succeed after Task 14, got %v", err)
	}
	if err := p.Register(StageDesugar, nil); err == nil {
		t.Fatal("Register(stage, nil) must fail")
	}
}

func TestScopeStageFallbackRunsDefaultScopePass(t *testing.T) {
	// Unregistered StageScope falls back to ScopePass{}: the scope phase
	// always runs (frontend contract), Bindings is published, and Bind runs
	// exactly once (a second bind would duplicate the UNDEFINED_VAR).
	res := Run("function main() { var x = y; }", "t.dc")
	if res.Program == nil || res.Program.Snapshot == nil {
		t.Fatal("fallback scope pass must publish a snapshot and typed program")
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want exactly 1 (Bind must run once): %v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Diagnostics[0].Code != diag.UNDEFINED_VAR {
		t.Fatalf("code = %v, want UNDEFINED_VAR from the fallback ScopePass", res.Diagnostics[0].Code)
	}
}

func TestScopeStageRegisteredReplacesFallback(t *testing.T) {
	// A registered StageScope pass replaces the default ScopePass; the
	// pipeline must run it once and still publish bindings on demand.
	p := New()
	rec := &recordingPass{stage: StageScope}
	if err := p.Register(StageScope, rec); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// The registered pass does not bind; the pipeline must publish bindings
	// on demand exactly once. An undefined var produces exactly one
	// UNDEFINED_VAR when Bind runs once — a second bind would duplicate it.
	res := p.Run("function main() { var x = y; }", "t.dc", nil)
	if !rec.called {
		t.Fatal("registered StageScope pass was not run")
	}
	if res.Program == nil || res.Program.Snapshot == nil {
		t.Fatal("bindings must still be published after a registered scope pass")
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != diag.UNDEFINED_VAR {
		t.Fatalf("got %d diagnostics, want exactly 1 UNDEFINED_VAR from the on-demand bind: %v",
			len(res.Diagnostics), res.Diagnostics)
	}
}

// ── review round 1 additions ───────────────────────────────────────

// TestPipelineConsumedCodeValuesPinned locks the numeric values of the codes
// the pipeline itself consumes (parse-error normalization, bind passthrough,
// short-circuit test pass) plus the seven warning codes, so a future enum
// renumbering cannot silently change externally-visible diagnostics.
func TestPipelineConsumedCodeValuesPinned(t *testing.T) {
	checks := map[diag.Code]int{
		diag.SYNTAX_ERROR:                    1000,
		diag.UNDEFINED_VAR:                   1001,
		diag.TYPE_MISMATCH:                   1003,
		diag.DEAD_CODE:                       2001,
		diag.UNUSED_VAR:                      2002,
		diag.EMPTY_ENTRY:                     2003,
		diag.EMPTY_FOR_BODY:                  2004,
		diag.CODE_SOURCE_CONTRACT_UNVERIFIED: 2005,
		diag.CODE_SOURCE_CONTRACT_MISMATCH:   2006,
		diag.ANY_FIELD_ACCESS:                2010,
	}
	for code, want := range checks {
		if int(code) != want {
			t.Errorf("%s = %d, want %d", code, int(code), want)
		}
	}
}

// TestFallbackBindAfterPreScopePassError covers the on-demand bind fallback:
// a registered pre-scope pass (desugar) errors and breaks the loop before
// the scope phase runs; the fallback bind must still publish a snapshot and
// append its diagnostics after the pass diagnostic, preserving stage order
// (desugar precedes scope).
func TestFallbackBindAfterPreScopePassError(t *testing.T) {
	p := New()
	boom := &recordingPass{emit: diag.TYPE_MISMATCH}
	if err := p.Register(StageDesugar, boom); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res := p.Run("function main() { var x = y; }", "t.dc", nil)
	if len(res.Diagnostics) != 2 {
		t.Fatalf("got %d diagnostics, want 2: %v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Diagnostics[0].Code != diag.TYPE_MISMATCH {
		t.Fatalf("first diagnostic = %v, want TYPE_MISMATCH (pass)", res.Diagnostics[0].Code)
	}
	if res.Diagnostics[1].Code != diag.UNDEFINED_VAR {
		t.Fatalf("second diagnostic = %v, want UNDEFINED_VAR (fallback bind)", res.Diagnostics[1].Code)
	}
	if res.Program == nil || res.Program.Snapshot == nil {
		t.Fatal("fallback bind must still publish a snapshot and typed program")
	}
}

// I2 invariant: an import colliding with a builtin namespace (std, json)
// fails the frontend with DUPLICATE_DEF, so lowering never sees a
// std/json entry in the IR import-namespace set (the divergence the type
// pass / IR discriminants used to disagree on is unreachable).
func TestPipelineRejectsImportCollidingWithBuiltinNamespace(t *testing.T) {
	for _, provider := range []string{"dify.std", "dify.json"} {
		src := "@language nodecoda/1\n@mode workflow\nimport \"" + provider + "\";\nfunction main() { return \"ok\"; }"
		res := DefaultPipeline().Run(src, "i2.ncoda", nil)
		if !res.HasErrors() {
			t.Fatalf("import %q must fail the pipeline", provider)
		}
		found := false
		for _, d := range res.Diagnostics {
			if d.Code == diag.DUPLICATE_DEF {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected DUPLICATE_DEF for import %q, got %v", provider, res.Diagnostics)
		}
	}
}
