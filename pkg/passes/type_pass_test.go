package passes

// Ports of lang/tests/lang/unit/test_type_pass.py (26 reference cases) plus
// self-built coverage for the MapLit panic-release regression and the brief's
// explicit extra cases (ternary join, conversation default, assignment
// compatibility). The harness mirrors run_type_pass: ScopePass first, TypePass
// only when binding was clean (base.py short-circuit).

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/types"
)

// runTypePass mirrors the reference run_type_pass helper.
func runTypePass(t *testing.T, src string) *Context {
	t.Helper()
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	if !ctx.HasErrors() {
		(&TypePass{}).Run(prog, ctx)
	}
	return ctx
}

// runTypePipeline compiles through the full pipeline with the type stage
// registered (mirrors compile_nodecoda_result's pass-loop segment).
func runTypePipeline(t *testing.T, src string) *FrontendResult {
	t.Helper()
	p := New()
	if err := p.Register(StageType, &TypePass{}); err != nil {
		t.Fatalf("Register(StageType): %v", err)
	}
	return p.Run(src, "t.dc", nil)
}

// errorCodes returns the diagnostic codes of the error diagnostics.
func errorCodes(ctx *Context) []diag.Code {
	out := []diag.Code{}
	for _, d := range ctx.Diagnostics {
		if d.Severity == diag.Error {
			out = append(out, d.Code)
		}
	}
	return out
}

func codesEqual(got []diag.Code, want ...diag.Code) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// ── TestTypeInference (test_type_pass.py:27-117) ──────────────────────

func TestIntLiteral(t *testing.T) {
	ctx := runTypePass(t, "function main() { var x = 42; }")
	if ctx.HasErrors() {
		t.Fatalf("unexpected errors: %v", ctx.Diagnostics)
	}
}

func TestStringLiteral(t *testing.T) {
	ctx := runTypePass(t, `function main() { var x = "hello"; }`)
	if ctx.HasErrors() {
		t.Fatalf("unexpected errors: %v", ctx.Diagnostics)
	}
}

func TestArrayLiteral(t *testing.T) {
	ctx := runTypePass(t, "function main() { var x = [1, 2, 3]; }")
	if ctx.HasErrors() {
		t.Fatalf("unexpected errors: %v", ctx.Diagnostics)
	}
}

func TestBinaryInt(t *testing.T) {
	ctx := runTypePass(t, "function main() { var x = 1 + 2; }")
	if ctx.HasErrors() {
		t.Fatalf("unexpected errors: %v", ctx.Diagnostics)
	}
}

func TestBinaryIntFloat(t *testing.T) {
	ctx := runTypePass(t, "function main() { var x = 1 + 2.0; }")
	if ctx.HasErrors() {
		t.Fatalf("unexpected errors: %v", ctx.Diagnostics)
	}
}

func TestBuiltinCall(t *testing.T) {
	ctx := runTypePass(t, "function main() { var x = len([1, 2, 3]); }")
	if ctx.HasErrors() {
		t.Fatalf("unexpected errors: %v", ctx.Diagnostics)
	}
}

func TestFieldAccess(t *testing.T) {
	src := `
        type R = { string content; float score; }
        function main() {
            var x = { "content": "hi", "label": "result" };
        }
        `
	ctx := runTypePass(t, src)
	// Map literals don't produce struct types yet
	if ctx.HasErrors() {
		t.Fatalf("unexpected errors: %v", ctx.Diagnostics)
	}
}

func TestNumericAggregateMembersUseExplicitWidening(t *testing.T) {
	for _, expression := range []string{
		"[1, 2.0, 3]",
		`{"first": 1, "second": 2.0, "third": 3}`,
		"[{\"value\": 1}, {\"value\": 2.0}]",
	} {
		ctx := runTypePass(t, "function main() { var value = "+expression+"; }")
		if ctx.HasErrors() {
			t.Fatalf("expression %s produced errors: %v", expression, ctx.Diagnostics)
		}
	}
}

func TestExplicitAnyMapAcceptsHeterogeneousValues(t *testing.T) {
	ctx := runTypePass(t,
		`function main() { map<string,any> value = {"name": "ok", "count": 1}; }`)
	if ctx.HasErrors() {
		t.Fatalf("unexpected errors: %v", ctx.Diagnostics)
	}
}

func TestInferredAggregateAnyMemberPropagatesAny(t *testing.T) {
	ctx := runTypePass(t, "function main(any item) { var values = [item, 1]; }")
	if ctx.HasErrors() {
		t.Fatalf("unexpected errors: %v", ctx.Diagnostics)
	}
}

func TestInferredAndContextualNullAggregatePolicyMatch(t *testing.T) {
	for _, statement := range []string{
		"var values = [null, 1];",
		"var values = [1, null];",
		`var values = {"missing": null, "count": 1};`,
		`var values = {"count": 1, "missing": null};`,
	} {
		ctx := runTypePass(t, "function main() { "+statement+" }")
		if ctx.HasErrors() {
			t.Fatalf("statement %s produced errors: %v", statement, ctx.Diagnostics)
		}
	}
}

func TestContextualNonOptionalAggregateRejectsNull(t *testing.T) {
	for _, statement := range []string{
		"int[] values = [null, 1];",
		`map<string,int> values = {"missing": null, "count": 1};`,
	} {
		ctx := runTypePass(t, "function main() { "+statement+" }")
		if got := errorCodes(ctx); !codesEqual(got, diag.TYPE_MISMATCH) {
			t.Fatalf("statement %s: codes = %v, want [TYPE_MISMATCH]", statement, got)
		}
	}
}

// ── TestTypeErrors (test_type_pass.py:119-250) ────────────────────────

func TestConditionNotBool(t *testing.T) {
	src := `
        function main() {
            if (42) { }
        }
        `
	result := runTypePipeline(t, src)
	// Deviation (recorded): the Go pipeline publishes a TypedBoundProgram even
	// when the type stage reports errors (Python compile_nodecoda_result returns
	// ast=None on validation failure). The observable contract asserted here is
	// the diagnostic set, not program absence.
	errs := result.Errors()
	if got := codesOf(result.Diagnostics); !codesEqual(got, diag.CONDITION_NOT_BOOL) {
		t.Fatalf("codes = %v, want [CONDITION_NOT_BOOL]", got)
	}
	if errs[0].Line == 0 || errs[0].Column == 0 {
		t.Fatalf("expected a source location, got line=%d column=%d", errs[0].Line, errs[0].Column)
	}
}

func TestArgCountMismatch(t *testing.T) {
	src := `
        function f(int x) { }
        function main() { f(1, 2); }
        `
	ctx := runTypePass(t, src)
	found := false
	for _, d := range ctx.Diagnostics {
		if d.Severity == diag.Error && strings.Contains(strings.ToLower(d.Message), "argument") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no error mentions arguments: %v", ctx.Diagnostics)
	}
}

func TestReturnTypeMismatch(t *testing.T) {
	src := `
        function f() -> int { return "hello"; }
        function main() { }
        `
	ctx := runTypePass(t, src)
	found := false
	for _, d := range ctx.Diagnostics {
		if d.Severity == diag.Error && strings.Contains(strings.ToLower(d.Message), "return") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no error mentions return: %v", ctx.Diagnostics)
	}
}

func TestDeclaredReturnTypeUsesTheSameRulesForEveryFunction(t *testing.T) {
	for _, functionName := range []string{"main", "helper"} {
		for _, pair := range [][2]string{
			{"-> string", "return 1;"},
			{"-> string", "return;"},
			{"-> void", `return "value";`},
		} {
			helper := ""
			if functionName != "main" {
				helper = "function main() {}"
			}
			src := "function " + functionName + "() " + pair[0] + " { " + pair[1] + " } " + helper
			ctx := runTypePass(t, src)
			found := false
			for _, d := range ctx.Diagnostics {
				if d.Severity == diag.Error && d.Code == diag.RETURN_TYPE {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("function %s %s %s: expected RETURN_TYPE, got %v",
					functionName, pair[0], pair[1], ctx.Diagnostics)
			}
		}
	}
}

func TestOmittedReturnTypeIsUnconstrainedForEveryFunction(t *testing.T) {
	for _, src := range []string{
		`function main(string value) { return {"result": value}; }`,
		"function helper(string value) { return value; } function main() {}",
	} {
		ctx := runTypePass(t, src)
		if ctx.HasErrors() {
			t.Fatalf("unexpected errors for %q: %v", src, ctx.Diagnostics)
		}
	}
}

func TestTypeMismatchAssignment(t *testing.T) {
	src := `
        function f() -> int { return "hello"; }
        function main() { }
        `
	ctx := runTypePass(t, src)
	if got := ctx.Errors(); len(got) < 1 {
		t.Fatalf("expected at least one error, got none")
	}
}

func TestDcType002HeterogeneousArrayMembersReportE1003(t *testing.T) {
	src := `function main() { var value = [1, "bad"]; }`
	ctx := runTypePass(t, src)

	errs := ctx.Errors()
	if len(errs) != 1 || errs[0].Code != diag.TYPE_MISMATCH {
		t.Fatalf("codes = %v, want exactly [TYPE_MISMATCH]", errorCodes(ctx))
	}
	error := errs[0]
	if error.Line == 0 || error.Column == 0 {
		t.Fatalf("expected a source location, got line=%d column=%d", error.Line, error.Column)
	}
	// Parent oracle: locate the incompatible element (`"bad"`), not the array head.
	badColumn := strings.Index(src, `"bad"`) + 1
	if error.Line != 1 || error.Column != badColumn {
		t.Fatalf("location = %d:%d, want 1:%d", error.Line, error.Column, badColumn)
	}
	lower := strings.ToLower(error.Message)
	if !strings.Contains(lower, "string") || !strings.Contains(lower, "int") {
		t.Fatalf("message %q must mention string and int", error.Message)
	}
}

// Heterogeneous dict literals without an expected type are legal: the
// literal self-declares map<string,any> (X3 roundtrip: {"code": float,
// "error_message": string}). TYPE_MISMATCH fires only when an expected map
// type constrains the values.
func TestDcType003HeterogeneousMapValuesWidenToAny(t *testing.T) {
	ctx := runTypePass(t, `function main() { var value = {"first": 1, "second": "bad"}; }`)
	if len(ctx.Errors()) != 0 {
		t.Fatalf("codes = %v, want none (heterogeneous dict widens to map<string,any>)", errorCodes(ctx))
	}
}

// Dot access on a map value resolves the string-key member to the value type
// (decompiler renders dict reads as .field; obj["field"] equivalent).
func TestMapDotAccessResolvesValueType(t *testing.T) {
	ctx := runTypePass(t, `function main() {
    var d = {"error_message": "boom"};
    var m = d.error_message;
    return m;
}`)
	if len(ctx.Errors()) != 0 {
		t.Fatalf("codes = %v, want none (map dot access resolves value type)", errorCodes(ctx))
	}
}

func TestNestedHeterogeneousAggregateReportsE1003(t *testing.T) {
	ctx := runTypePass(t, `function main() { var value = [{"value": 1}, {"value": "bad"}]; }`)

	errs := ctx.Errors()
	if len(errs) != 1 || errs[0].Code != diag.TYPE_MISMATCH {
		t.Fatalf("codes = %v, want exactly [TYPE_MISMATCH]", errorCodes(ctx))
	}
}

// ── TestComparisonMatrix (test_type_pass.py:252-282) ──────────────────

func TestDcType008AcceptsCompatibleEqualityAndOrderingPairs(t *testing.T) {
	for _, expression := range []string{
		"1 == 1.0",
		"1.0 != 1",
		`"a" == "b"`,
		"true != false",
		"[1] == [1.0]",
		`{"value": 1} == {"value": 1.0}`,
		"1 < 2.0",
		"1.0 >= 2",
		`"a" < "b"`,
	} {
		ctx := runTypePass(t, "function main() { var value = "+expression+"; }")
		if ctx.HasErrors() {
			t.Fatalf("expression %s produced errors: %v", expression, ctx.Diagnostics)
		}
	}
}

func TestDcType008RejectsIncompatibleEqualityAndOrderingPairs(t *testing.T) {
	// 相等比较跨类型：语料实证（X211 bool==string、H36 record!=string，
	// 平台可运行的商业工作流）→ CROSS_TYPE_EQUALITY 警告而非错误，
	// roundtrip 原样渲染平台语义一致（L0 平台接受优先于编译器洁癖）。
	// 顺序比较保持严格 BINARY_TYPE。
	equalityPairs := []string{
		`1 == "1"`,
		`null == "value"`,
		`[1] == ["1"]`,
		`{"value": 1} == {"value": "1"}`,
		`1 != "1"`,
	}
	orderingPairs := []string{
		"true < false",
		"null < 1",
		"[1] < [2]",
	}
	for _, expression := range equalityPairs {
		ctx := runTypePass(t, "function main() { var value = "+expression+"; }")
		if got := errorCodes(ctx); len(got) != 0 {
			t.Fatalf("equality %s: error codes = %v, want none (CROSS_TYPE_EQUALITY warning)", expression, got)
		}
		found := false
		for _, d := range ctx.Diagnostics {
			if d.Code == diag.CROSS_TYPE_EQUALITY && d.Severity == diag.Warning {
				found = true
			}
		}
		if !found {
			t.Fatalf("equality %s: want CROSS_TYPE_EQUALITY warning, got %v", expression, ctx.Diagnostics)
		}
	}
	for _, expression := range orderingPairs {
		ctx := runTypePass(t, "function main() { var value = "+expression+"; }")
		if got := errorCodes(ctx); !codesEqual(got, diag.BINARY_TYPE) {
			t.Fatalf("ordering %s: codes = %v, want [BINARY_TYPE]", expression, got)
		}
	}
}

// ── TestTemplateStringTyping (test_type_pass.py:284-289) ──────────────

func TestTemplateStringHasStringType(t *testing.T) {
	ctx := runTypePass(t, "function main(string name) { var x = `Hello ${name}`; }")
	if ctx.HasErrors() {
		t.Fatalf("unexpected errors: %v", ctx.Diagnostics)
	}
}

func TestTemplateStringWithUndefinedVarErrors(t *testing.T) {
	ctx := runTypePass(t, "function main() { var x = `Hello ${undefined_var}`; }")
	if !ctx.HasErrors() {
		t.Fatalf("expected an undefined-variable error, got none")
	}
}

// ── self-built coverage (task brief) ───────────────────────────────────

// TestMapLitWithExpectedTypeDoesNotPanic is the MapLit panic-release
// regression: `var x = {};` with a declared map type must compile through
// the full pipeline (scope → type → TypeBind with requireAggregates)
// without the old requireAggregates panic — the type pass now types the
// literal as map<string,int> so the aggregate fact publishes.
func TestMapLitWithExpectedTypeDoesNotPanic(t *testing.T) {
	src := "function main() { map<string,int> x = {}; }"
	result := runTypePipeline(t, src)
	if result.HasErrors() {
		t.Fatalf("expected a clean compile, got %v", result.Errors())
	}
	if result.Program == nil {
		t.Fatal("expected a typed bound program")
	}
	if got := result.Program.AggregateFacts; len(got) != 1 {
		t.Fatalf("len(AggregateFacts) = %d, want 1", len(got))
	}
}

// TestMapLitWithoutExpectedTypeReportsEmptyAggregate: `var x = {}` with no
// expected type reports EMPTY_AGGREGATE_TYPE (Python parity) and the error
// short-circuit means TypeBind's requireAggregates guard is skipped — the
// old panic would fire exactly here (empty MapLit defaulting to Any).
func TestMapLitWithoutExpectedTypeReportsEmptyAggregate(t *testing.T) {
	src := "function main() { var x = {}; }"
	result := runTypePipeline(t, src)
	if got := codesOf(result.Diagnostics); !codesEqual(got, diag.EMPTY_AGGREGATE_TYPE) {
		t.Fatalf("codes = %v, want [EMPTY_AGGREGATE_TYPE]", got)
	}
}

// TestEmptyArrayLiteralRequiresExplicitType mirrors the array side of the
// EMPTY_AGGREGATE_TYPE rule.
func TestEmptyArrayLiteralRequiresExplicitType(t *testing.T) {
	ctx := runTypePass(t, "function main() { var x = []; }")
	if got := errorCodes(ctx); !codesEqual(got, diag.EMPTY_AGGREGATE_TYPE) {
		t.Fatalf("codes = %v, want [EMPTY_AGGREGATE_TYPE]", got)
	}
}

// TestTernaryBranchesJoinTypes covers visit_ternary_expr (type_pass.py:1743-1758):
// compatible branches join; incompatible branches report E1003.
func TestTernaryBranchesJoinTypes(t *testing.T) {
	ctx := runTypePass(t, "function main(bool c) { var x = c ? 1 : 2.0; }")
	if ctx.HasErrors() {
		t.Fatalf("numeric ternary join produced errors: %v", ctx.Diagnostics)
	}
	ctx = runTypePass(t, "function main(bool c) { var x = c ? 1 : \"bad\"; }")
	if got := errorCodes(ctx); !codesEqual(got, diag.TYPE_MISMATCH) {
		t.Fatalf("incompatible ternary codes = %v, want [TYPE_MISMATCH]", got)
	}
}

// TestConversationDefaultTypeMismatch covers the run() conversation-default
// check (type_pass.py:133-141).
func TestConversationDefaultTypeMismatch(t *testing.T) {
	ctx := runTypePass(t, "@mode advanced-chat\n@conversation string status = 42;\nfunction main() {}")
	if got := errorCodes(ctx); !codesEqual(got, diag.TYPE_MISMATCH) {
		t.Fatalf("codes = %v, want [TYPE_MISMATCH]", got)
	}
	ctx = runTypePass(t, "@mode advanced-chat\n@conversation string status = \"new\";\nfunction main() {}")
	if ctx.HasErrors() {
		t.Fatalf("valid conversation default produced errors: %v", ctx.Diagnostics)
	}
}

// TestAssignmentCompatibility covers visit_assign_stmt (type_pass.py:304-325).
func TestAssignmentCompatibility(t *testing.T) {
	ctx := runTypePass(t, "function main() { var x = 1; x = \"s\"; }")
	if got := errorCodes(ctx); !codesEqual(got, diag.TYPE_MISMATCH) {
		t.Fatalf("codes = %v, want [TYPE_MISMATCH]", got)
	}
	// The append operator checks the element type (type_pass.py:308-314).
	ctx = runTypePass(t, "function main() { int[] xs = [1]; xs << \"s\"; }")
	if got := errorCodes(ctx); !codesEqual(got, diag.TYPE_MISMATCH) {
		t.Fatalf("append codes = %v, want [TYPE_MISMATCH]", got)
	}
	ctx = runTypePass(t, "function main() { int[] xs = [1]; xs << 2; }")
	if ctx.HasErrors() {
		t.Fatalf("valid append produced errors: %v", ctx.Diagnostics)
	}
}

// ── typed-program publication checks (T1/T2/T3) ────────────────────────

// TestTypeBindOverlaysVarTypesAndEnumMembers exercises TypeBindFull: the
// type pass's VarTypes mutation surface and enum-member facts reach the
// typed bound program (T2/T3).
func TestTypeBindOverlaysVarTypesAndEnumMembers(t *testing.T) {
	src := `
        enum Color { red, green, };
        function main() {
            var x = 42;
            Color c = Color.red;
        }
        `
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("scope errors: %v", ctx.Diagnostics)
	}
	(&TypePass{}).Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("type errors: %v", ctx.Diagnostics)
	}
	_ = prog

	// x's VarTypes entry: inferred int (Python sym.type_info mutation).
	var varSymID binding.SymbolID
	foundVar := false
	for _, stmt := range prog.Entry().Body().Stmts() {
		if vds, ok := stmt.(*ast.VarDeclStmt); ok && vds.Name() == "x" {
			id, _ := ctx.Bindings.NodeIDOf(vds)
			varSymID, foundVar = ctx.Bindings.DeclarationSymbols().Get(id)
			break
		}
	}
	if !foundVar {
		t.Fatal("var x declaration symbol not found")
	}
	if got := ctx.VarTypes[varSymID]; got == nil || got.Kind != types.Int {
		t.Fatalf("VarTypes[x] = %v, want int", got)
	}

	// Color.red field access published an EnumMemberFact.
	if len(ctx.EnumMembers) != 1 {
		t.Fatalf("len(EnumMembers) = %d, want 1", len(ctx.EnumMembers))
	}

	typed := binding.TypeBindFull(prog, ctx.Bindings, ctx.ExprTypes, ctx.VarTypes, ctx.EnumMembers, true, nil)
	if got := typed.EnumMembers; len(got) != 1 {
		t.Fatalf("TypedBoundProgram.EnumMembers = %d entries, want 1", len(got))
	}
}

// codesOf extracts the error codes of a FrontendResult.
func codesOf(diags []diag.Diagnostic) []diag.Code {
	out := []diag.Code{}
	for _, d := range diags {
		if d.Severity == diag.Error {
			out = append(out, d.Code)
		}
	}
	return out
}

// ── Task 15 review regressions (I1 / M1 / C1) ─────────────────────────

// TestForExprLoopVarElementType is the I1 regression: the for-expr loop
// variable (untyped) must receive the iterable's element type through the
// VarTypes mutation surface, so field access and arithmetic on the loop
// variable type-check inside the body. Pre-fix the binder registered the
// DeclarationSymbols entry against nil (visitLoopBlock(nil)) and the
// parallel-for delegated with a ForExpr pointer copy, so the loop variable
// stayed Any and the body diagnostics fired.
func TestForExprLoopVarElementType(t *testing.T) {
	src := `
        type R = { string text; int score; }
        function main(R[] items) {
            var results = for (x in items) { yield x.text; };
        }
        `
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("scope errors: %v", ctx.Diagnostics)
	}
	(&TypePass{}).Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("for-expr loop-var field access produced errors: %v", ctx.Diagnostics)
	}

	// Assert VarTypes[loopVar] == element type (R).
	var loopSymID binding.SymbolID
	found := false
	for _, stmt := range prog.Entry().Body().Stmts() {
		vd, ok := stmt.(*ast.VarDeclStmt)
		if !ok {
			continue
		}
		fe, ok := vd.Init().(*ast.ForExpr)
		if !ok {
			continue
		}
		if id, ok2 := ctx.Bindings.NodeIDOf(fe); ok2 {
			loopSymID, found = ctx.Bindings.DeclarationSymbols().Get(id)
		}
		break
	}
	if !found {
		t.Fatal("for-expr DeclarationSymbols entry not found")
	}
	got := ctx.VarTypes[loopSymID]
	if got == nil || got.Kind != types.Record {
		t.Fatalf("VarTypes[loop var] = %v, want Record (R)", got)
	}
}

// TestParallelForExprLoopVarElementType is the parallel-for half of I1: the
// ParallelForExpr node itself carries the loop-variable declaration, and the
// type pass must write the element type for arithmetic/field access in the
// body.
func TestParallelForExprLoopVarElementType(t *testing.T) {
	src := `
        function main(int[] xs) {
            var results = parallel for (var x in xs, concurrency: 2, on_error: terminate) { yield x + 1; };
        }
        `
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("scope errors: %v", ctx.Diagnostics)
	}
	(&TypePass{}).Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("parallel-for loop-var arithmetic produced errors: %v", ctx.Diagnostics)
	}

	var loopSymID binding.SymbolID
	found := false
	for _, stmt := range prog.Entry().Body().Stmts() {
		vd, ok := stmt.(*ast.VarDeclStmt)
		if !ok {
			continue
		}
		pfe, ok := vd.Init().(*ast.ParallelForExpr)
		if !ok {
			continue
		}
		if id, ok2 := ctx.Bindings.NodeIDOf(pfe); ok2 {
			loopSymID, found = ctx.Bindings.DeclarationSymbols().Get(id)
		}
		break
	}
	if !found {
		t.Fatal("parallel-for DeclarationSymbols entry not found")
	}
	if got := ctx.VarTypes[loopSymID]; got == nil || got.Kind != types.Int {
		t.Fatalf("VarTypes[loop var] = %v, want int", got)
	}
}

// TestForExprYieldNilValueRegression is the M1 regression: a bare `yield;`
// (nil value) between typed yields must not shift the incompatible-yield
// diagnostic onto the wrong statement. The diagnostic must reference the
// offending yield's span (the "bad" string yield on line 6), not the
// skipped bare yield (line 5). Pre-fix the Go port reused the filtered
// yields slice with an i+1 index, so the nil-value yield was skipped in
// typedYields but re-appeared as the diagnostic location.
func TestForExprYieldNilValueRegression(t *testing.T) {
	src := `
        function main(int[] xs) {
            var results = for (x in xs) {
                yield 1;
                yield;
                yield "bad";
            };
        }
        `
	ctx := runTypePass(t, src)
	if got := errorCodes(ctx); !codesEqual(got, diag.TYPE_MISMATCH) {
		t.Fatalf("codes = %v, want [TYPE_MISMATCH]", got)
	}
	found := false
	for _, d := range ctx.Diagnostics {
		if d.Severity == diag.Error && d.Code == diag.TYPE_MISMATCH {
			found = true
			if d.Line != 6 {
				t.Fatalf("diagnostic line = %d, want 6 (the 'bad' yield, not the bare yield on line 5)", d.Line)
			}
			break
		}
	}
	if !found {
		t.Fatal("no TYPE_MISMATCH diagnostic found")
	}
}

// TestHttpOperationArgumentChecks is the C1 regression for the operation
// path: argument count (min required .. max total), required args and
// argument types against the http spec (method/url required, config
// optional).
func TestHttpOperationArgumentChecks(t *testing.T) {
	// Valid: method + url required, config optional (2-3 args).
	ctx := runTypePass(t, `function main() { var r = http("GET", "https://example.com"); }`)
	if ctx.HasErrors() {
		t.Fatalf("valid http call produced errors: %v", ctx.Diagnostics)
	}
	ctx = runTypePass(t, `function main() { var r = http("GET", "https://example.com", {}); }`)
	if ctx.HasErrors() {
		t.Fatalf("valid http call with config produced errors: %v", ctx.Diagnostics)
	}
	// Too few: only 1 of 2 required args.
	ctx = runTypePass(t, `function main() { var r = http("GET"); }`)
	if got := errorCodes(ctx); !codesEqual(got, diag.ARG_COUNT) {
		t.Fatalf("too-few codes = %v, want [ARG_COUNT]", got)
	}
	// Too many: 4 args, spec allows at most 3.
	ctx = runTypePass(t, `function main() { var r = http("GET", "https://example.com", {}, 1); }`)
	if got := errorCodes(ctx); !codesEqual(got, diag.ARG_COUNT) {
		t.Fatalf("too-many codes = %v, want [ARG_COUNT]", got)
	}
	// Argument type: url must be string, got int.
	ctx = runTypePass(t, `function main() { var r = http("GET", 42); }`)
	if got := errorCodes(ctx); !codesEqual(got, diag.TYPE_MISMATCH) {
		t.Fatalf("bad-arg-type codes = %v, want [TYPE_MISMATCH]", got)
	}
}

// TestAttemptFailureBranchPolicy is the C1 regression for attempt: an
// operation without failure-branch support (knowledge) reports
// OPERATION_POLICY, while a supported one (http) type-checks cleanly and
// binds the result/error symbols.
func TestAttemptFailureBranchPolicy(t *testing.T) {
	ctx := runTypePass(t, `
        function main(string q) {
            attempt knowledge("ds", q) as result {
                success { }
                failure (err) { }
            }
        }
        `)
	if got := errorCodes(ctx); !codesEqual(got, diag.OPERATION_POLICY) {
		t.Fatalf("codes = %v, want [OPERATION_POLICY]", got)
	}
	ctx = runTypePass(t, `
        function main() {
            attempt http("GET", "https://example.com") as result {
                success { }
                failure (err) { }
            }
        }
        `)
	if ctx.HasErrors() {
		t.Fatalf("attempt on http (failure-branch supported) produced errors: %v", ctx.Diagnostics)
	}
}

// TestOrValueMerge → route-1a R1/R4（2026-09-05 裁决）：`||` 收紧为纯布尔
// 逻辑或（两侧须 bool）；值兜底语义迁往 `??`（first-not-null）。旧 truthy
// 值合并（Python `or` 语义）退役——string 操作数进 `||` 报 BINARY_TYPE +
// hint，Optional 进 `||` 报 OPTIONAL_REQUIRED；`??` 承接 null 兜底并收敛类型。
func TestOrValueMerge(t *testing.T) {
	// string || string → 编译错误（R1：值兜底请用 ??），不得静默值合并
	ctx := runTypePass(t, `function main(string x) -> string { var r = x || "d"; return r; }`)
	if got := errorCodes(ctx); !codesEqual(got, diag.BINARY_TYPE, diag.BINARY_TYPE, diag.RETURN_TYPE) {
		t.Fatalf("string || string 应报 BINARY_TYPE×2（两操作数）+ RETURN_TYPE，codes = %v", got)
	}
	// bool || bool 保持布尔逻辑（R1 纯布尔）
	ctx = runTypePass(t, `function main(bool a, bool b) -> bool { var r = a || b; return r; }`)
	if ctx.HasErrors() {
		t.Fatalf("bool || bool 布尔逻辑应无错误: %v", ctx.Diagnostics)
	}
	// Optional || bool → OPTIONAL_REQUIRED（R1：|| 不接受 Optional）
	ctx = runTypePass(t, `function main(bool? a, bool b) -> bool { var r = a || b; return r; }`)
	if got := errorCodes(ctx); !codesEqual(got, diag.OPTIONAL_REQUIRED) {
		t.Fatalf("Optional || bool codes = %v, want [OPTIONAL_REQUIRED]", got)
	}
	// ??：Optional<T> ?? T → T（null 消除收敛，R4 主用例）
	ctx = runTypePass(t, `function main(string? x) -> string { var r = x ?? "d"; return r; }`)
	if ctx.HasErrors() {
		t.Fatalf("optional ?? fallback 应无错误: %v", ctx.Diagnostics)
	}
	// ??：T（非 Optional）?? T → 恒非 null 冗余警告 REDUNDANT_COALESCE，无错误
	ctx = runTypePass(t, `function main(string x) -> string { var r = x ?? "d"; return r; }`)
	if ctx.HasErrors() {
		t.Fatalf("非 Optional ?? fallback 不得报错: %v", ctx.Diagnostics)
	}
	var redundant bool
	for _, d := range ctx.Diagnostics {
		if d.Severity == diag.Warning && d.Code == diag.REDUNDANT_COALESCE {
			redundant = true
		}
	}
	if !redundant {
		t.Fatalf("非 Optional ?? fallback 应报 REDUNDANT_COALESCE 警告, got %v", ctx.Diagnostics)
	}
	// 左侧已知非空时，右侧不可达，不因 Optional 或物理类型不同再报错。
	ctx = runTypePass(t, `function main(string x, int? y) -> string { var r = x ?? y; return r; }`)
	if ctx.HasErrors() {
		t.Fatalf("非 Optional ?? Optional fallback 应仅警告，不得报错: %v", ctx.Diagnostics)
	}
	// ??：Optional 左侧的 fallback 类型不匹配 → TYPE_MISMATCH
	ctx = runTypePass(t, `function main(string? x) { var r = x ?? 3; }`)
	if got := errorCodes(ctx); !codesEqual(got, diag.TYPE_MISMATCH) {
		t.Fatalf("?? fallback 类型不匹配 codes = %v, want [TYPE_MISMATCH]", got)
	}
}

// TestOptionalFieldAccessNullPropagation — M2（2026-09-05 用户拍板）：Optional
// 记录成员访问 = 空传播 —— `r: R?` 时 `r.text` 静态类型 = string?（接收者
// null → 整条读值为 null），取代旧 OPTIONAL_REQUIRED 硬错误。读值用于非
// Optional 上下文必须经 ?? / 判空收敛（R4）。
func TestOptionalFieldAccessNullPropagation(t *testing.T) {
	// M2 前：r.text on R? = OPTIONAL_REQUIRED 硬错误；M2 后空传播 string?，
	// return 到 string 须报 RETURN_TYPE（未收敛）
	ctx := runTypePass(t, `
        type R = { string text; }
        function main(R? r) -> string {
            var s = r.text;
            return s;
        }
        `)
	if got := errorCodes(ctx); !codesEqual(got, diag.RETURN_TYPE) {
		t.Fatalf("r.text on R? 应空传播 string?（return 未收敛报 RETURN_TYPE），codes = %v", got)
	}
	// ?? 收敛：string? ?? string → string，无错误
	ctx = runTypePass(t, `
        type R = { string text; }
        function main(R? r) -> string {
            var s = r.text ?? "d";
            return s;
        }
        `)
	if ctx.HasErrors() {
		t.Fatalf("r.text ?? \"d\" 应收敛无错误: %v", ctx.Diagnostics)
	}
	// 深层成员链逐层空传播：r: A? → r.a.b.c : string?
	ctx = runTypePass(t, `
        type C = { string c; }
        type B = { C b; }
        type A = { B a; }
        function main(A? r) -> string {
            var s = r.a.b.c;
            return s;
        }
        `)
	if got := errorCodes(ctx); !codesEqual(got, diag.RETURN_TYPE) {
		t.Fatalf("深层成员链 r.a.b.c on A? 应逐层传播为 string?，codes = %v", got)
	}
	// ?? 收敛整链
	ctx = runTypePass(t, `
        type C = { string c; }
        type B = { C b; }
        type A = { B a; }
        function main(A? r) -> string {
            var s = r.a.b.c ?? "";
            return s;
        }
        `)
	if ctx.HasErrors() {
		t.Fatalf("深层 ?? 收敛应无错误: %v", ctx.Diagnostics)
	}
}

// TestCollectionLambdaElementType is the C1 regression for collection calls:
// flatMap/filter over an array with a lambda infers the element type through
// setLambdaParameterType, so field access / arithmetic in the lambda body
// type-checks. (map is a reserved keyword in both the Python and Go
// lexers, so the reference's "map" collection call is unreachable in the
// DSL; filter/flatMap exercise the same lambda-typing path.)
func TestCollectionLambdaElementType(t *testing.T) {
	ctx := runTypePass(t, `
        type R = { string text; }
        function main(R[] items) {
            var names = flatMap(items, (x) -> [x.text]);
        }
        `)
	if ctx.HasErrors() {
		t.Fatalf("flatMap with lambda field access produced errors: %v", ctx.Diagnostics)
	}
	ctx = runTypePass(t, `
        function main(int[] xs) {
            var doubled = flatMap(xs, (x) -> [x * 2]);
        }
        `)
	if ctx.HasErrors() {
		t.Fatalf("flatMap with lambda arithmetic produced errors: %v", ctx.Diagnostics)
	}
	ctx = runTypePass(t, `
        function main(int[] xs) {
            var evens = filter(xs, (x) -> x > 0);
        }
        `)
	if ctx.HasErrors() {
		t.Fatalf("filter with lambda comparison produced errors: %v", ctx.Diagnostics)
	}
}

// TestMultiReturnExitValueTypesConsistent locks the C-style multi-return-exit
// contract for functions WITHOUT a declared `->` return type: the first
// value-return exit anchors the implicit return type, and every later
// value-return exit must be mutually assignable. Bare `return;` exits carry
// no value and neither anchor nor constrain. (Declared-return functions are
// already anchored by their `-> T`; see TestDeclaredReturnTypeUsesTheSameRulesForEveryFunction.)
func TestMultiReturnExitValueTypesConsistent(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name:    "consistent strings across exits",
			body:    `function main(string q) { if (q) { return q; } return "fallback"; }`,
			wantErr: false,
		},
		{
			name:    "single exit any type",
			body:    `function main() { return 42; }`,
			wantErr: false,
		},
		{
			name:    "value exit then bare return is fine",
			body:    `function main(string q) { if (q) { return q; } return; }`,
			wantErr: false,
		},
		{
			name:    "bare return then value exit is fine",
			body:    `function main(string q) { if (q) { return; } return q; }`,
			wantErr: false,
		},
		{
			name:    "string then int mismatch",
			body:    `function main(string q) { if (q) { return q; } return 42; }`,
			wantErr: true,
		},
		{
			name:    "int then string mismatch",
			body:    `function main() { if (true) { return 1; } return "no"; }`,
			wantErr: true,
		},
		{
			name:    "int then float does not widen back",
			body:    `function main() { if (true) { return 1; } return 1.5; }`,
			wantErr: true,
		},
		{
			// while/for statement body returns are subscope exits (the
			// value flows to the loop's __return_value), NOT function
			// exits: they must not anchor the function's implicit return
			// type. Only the trailing `return "done"` is a function exit.
			name:    "subscope loop return does not anchor the function type",
			body:    `function main() { var i = 0; while (i < 2) limit 3 { i += 1; return 42; } return "done"; }`,
			wantErr: false,
		},
		{
			// request_input action/timeout returns are subscope exits (the
			// value resumes the enclosing loop), not function exits: they
			// must not anchor the function's implicit return type. The
			// real multi-line shape is locked by
			// internal/runtime/（商业仓）compile/qa_loop_byarray_test.go; this is the
			// same mechanism through a for-statement body.
			name:    "subscope loop return does not anchor the function type",
			body:    `function main() { let items = ["a"]; for (x in items) { while (x != "") limit 2 { return 42; } } return "done"; }`,
			wantErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := runTypePass(t, tc.body)
			got := false
			for _, d := range ctx.Diagnostics {
				if d.Severity == diag.Error && d.Code == diag.RETURN_TYPE {
					got = true
					break
				}
			}
			if got != tc.wantErr {
				t.Fatalf("wantErr=%v, got RETURN_TYPE=%v, diagnostics=%v", tc.wantErr, got, ctx.Diagnostics)
			}
		})
	}
}

func TestRegistryTypeDescriptorMapParam(t *testing.T) {
	// P1 通道回归: drawing_board.render 的 variables 参数声明为
	// map<string,any>,调用端 map 值可为 string 变量（toolValueTree ref 叶子）
	// 或 any/map 值。此前 registryTypeDescriptor 不认 "map<string,any>"
	// fallback 到 Any → 无 expected 聚合检查把 map 值变量误报 TYPE_MISMATCH。
	cases := []struct {
		name string
		typ  string
	}{
		{"no-space", "map<string,any>"},
		{"space", "map<string, any>"},
		{"string-val", "map<string,string>"},
	}
	for _, tc := range cases {
		d := registryTypeDescriptor(tc.typ)
		if d == nil || d.Kind != types.Map {
			t.Fatalf("%s: got %v, want Map", tc.name, d)
		}
		if d.Key == nil || d.Key.Kind != types.String {
			t.Fatalf("%s: key kind = %v, want String", tc.name, d.Key)
		}
		if d.Val == nil || d.Val.Kind != types.Any {
			t.Fatalf("%s: val kind = %v, want Any (accepts string/any values)", tc.name, d.Val)
		}
	}
}

func TestRegistryTypeDescriptorArrayAny(t *testing.T) {
	// 输出侧值类型: array<any> 元素按 map<string,any> 解析，元素可字段访问
	// （X2_W05: aweme_list 元素 aweme.awemeLink）。
	d := registryTypeDescriptor("array<any>")
	if d == nil || d.Kind != types.Array {
		t.Fatalf("array<any> output: got %v, want Array", d)
	}
	if d.Elem == nil || d.Elem.Kind != types.Map {
		t.Fatalf("array<any> output: elem = %v, want Map (元素可字段访问)", d.Elem)
	}
	// 参数侧期望: array<any> 声明"任意数组"，宽松接受 array<string> 实参
	// （Z34: str_to_list 输出 infos(array<string>) 接 na2 image_urls）。
	pd := registryParamTypeDescriptor("array<any>")
	if pd == nil || pd.Kind != types.Array || pd.Elem == nil || pd.Elem.Kind != types.Any {
		t.Fatalf("array<any> param: got %v, want Array<Any>", pd)
	}
	if !types.IsAssignable(types.ArrayType(types.StringType()), pd) {
		t.Errorf("array<string> 应 assignable 到参数期望 array<any>")
	}
	if types.IsAssignable(types.ArrayType(types.StringType()), d) {
		t.Errorf("array<string> 不应 assignable 到输出侧 array<map>")
	}
}

func TestLLMMessageContentTypeEnforced(t *testing.T) {
	// llm() 的 messages content 必须为 string（替代旧聚合误报）。
	// M30 突变锚点: content 传 int → TYPE_MISMATCH;string → 通过。
	cases := []struct {
		name    string
		content string
		wantErr bool
	}{
		{"int-content", "q", true},
		{"string-content", `"hello"`, false},
	}
	for _, tc := range cases {
		src := `@language nodecoda/1
@mode workflow
function main(int q) -> string {
    let r = llm("m", { "messages": [{ "role": "user", "content": ` + tc.content + ` }] });
    return r.text;
}`
		res := DefaultPipeline().Run(src, "llm-msg-test", nil)
		errs := [](diag.Diagnostic){}
		for _, d := range res.Diagnostics {
			if d.Severity == diag.Error && d.Code == diag.TYPE_MISMATCH {
				errs = append(errs, d)
			}
		}
		got := len(errs) > 0
		if got != tc.wantErr {
			t.Errorf("%s: content %s: got err=%v want err=%v (diags=%v)",
				tc.name, tc.content, got, tc.wantErr, res.Diagnostics)
		}
	}
}

func TestFileCast(t *testing.T) {
	// file(x) — string→file 显式标注 cast: 只接受 string 参数（int/float/bool
	// 走通用函数校验报 TYPE_MISMATCH）；返回类型 file；file 输入幂等透传。
	cases := []struct {
		name    string
		header  string
		body    string
		wantErr bool // 期望出现 TYPE_MISMATCH
	}{
		{
			"string-return-file", "function main(string url) -> file {",
			`return file(url);`, false,
		},
		{
			// let f = file(url); return f; 要求 f 推断为 file —— 若 f 是
			// string, string→file 不可赋值必然报 TYPE_MISMATCH, 所以此例
			// 强断言绑定类型为 file。
			"let-binds-file", "function main(string url) -> file {",
			`let f = file(url); return f;`, false,
		},
		{
			// file→string 隐式兼容: file(url) 作 string 返回。
			"file-to-string-implicit", "function main(string url) -> string {",
			`return file(url);`, false,
		},
		{
			"int-rejected", "function main(int n) -> file {",
			`return file(n);`, true,
		},
		{
			"float-rejected", "function main(float n) -> file {",
			`return file(n);`, true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := `@language nodecoda/1
@mode workflow
` + tc.header + `
    ` + tc.body + `
}`
			res := DefaultPipeline().Run(src, "file-cast", nil)
			got := false
			for _, d := range res.Diagnostics {
				if d.Severity == diag.Error && d.Code == diag.TYPE_MISMATCH {
					got = true
				}
			}
			if got != tc.wantErr {
				t.Errorf("%s: TYPE_MISMATCH got=%v want=%v (diags=%v)",
					tc.name, got, tc.wantErr, res.Diagnostics)
			}
		})
	}
}

func TestNullTypingAndOptionalCoalesceBoundaries(t *testing.T) {
	// null is a value of its own and may initialize an Optional, but it cannot
	// be silently assigned to a non-Optional value.
	ctx := runTypePass(t, `function main() -> string? {
        string? value;
        value = null;
        return value;
    }`)
	if ctx.HasErrors() {
		t.Fatalf("explicit null should initialize Optional: %v", ctx.Diagnostics)
	}

	ctx = runTypePass(t, `function main() {
        string value;
        value = null;
    }`)
	if got := errorCodes(ctx); !codesEqual(got, diag.TYPE_MISMATCH) {
		t.Fatalf("null assigned to non-Optional should be TYPE_MISMATCH, got %v", got)
	}

	// Optional on both sides preserves optionality; a concrete fallback
	// discharges it. This also locks the rule recursively for a ?? chain.
	ctx = runTypePass(t, `function main(string? a, string? b, string c) -> string? {
        var x = a ?? b ?? c;
        return x;
    }`)
	if ctx.HasErrors() {
		t.Fatalf("Optional ?? Optional ?? concrete should type-check: %v", ctx.Diagnostics)
	}
}

func TestOptionalNullComparisonNarrowsInBranches(t *testing.T) {
	ctx := runTypePass(t, `function main(string? value) -> string {
        if (value != null) {
            return value;
        }
        return "default";
    }`)
	if ctx.HasErrors() {
		t.Fatalf("value != null should narrow Optional in the true branch: %v", ctx.Diagnostics)
	}
}

// TestEmptyStringMapKeyStaysStaticLabel is the D-P30-1 root fix: an
// empty-string key is a **static label** `""`, not the sentinel for "dynamic".
// The retired `name == ""` test put `{ "": 1 }` in the same family as a
// genuinely dynamic key `{ "a" + "b": 1 }` — but the rule (pattern catalog P30
// V1 note / rule_forward ④) says the empty key is a label named "", i.e. the
// same family as `{ "a": 1 }` (record). `ast.StaticMapKey` and
// passes/target_validation.go's staticMapKey have always returned the
// (name, ok) pair; TypePass was the only lossy one.
func TestEmptyStringMapKeyStaysStaticLabel(t *testing.T) {
	inferMapLit := func(t *testing.T, src string) *types.Descriptor {
		t.Helper()
		prog := parseSourceOrFail(t, src)
		ctx := NewContext(nil)
		ScopePass{}.Run(prog, ctx)
		if ctx.HasErrors() {
			t.Fatalf("scope errors: %v", ctx.Diagnostics)
		}
		(&TypePass{}).Run(prog, ctx)
		if ctx.HasErrors() {
			t.Fatalf("type errors: %v", ctx.Diagnostics)
		}
		maps := collectMapLits(prog.Entry().Body())
		if len(maps) != 1 {
			t.Fatalf("map literals = %d, want 1", len(maps))
		}
		id, ok := ctx.Bindings.NodeIDOf(maps[0])
		if !ok {
			t.Fatal("map literal has no AST node id")
		}
		got := ctx.ExprTypes[id]
		if got == nil {
			t.Fatal("map literal has no inferred type")
		}
		return got
	}

	// Static label "" ⇒ same family as { "a": 1 } ⇒ record with field "".
	got := inferMapLit(t, `function main() { var x = { "": 1 }; }`)
	if got.Kind != types.Record {
		t.Fatalf("`{ \"\": 1 }` inferred %v (%v), want record — empty-string key is a static label", got.Kind, got)
	}
	if len(got.Fields) != 1 || got.Fields[0].Name != "" || got.Fields[0].Type.Kind != types.Int {
		t.Fatalf("record fields = %+v, want a single field named %q of int", got.Fields, "")
	}

	// Non-empty label unchanged (control cell).
	got = inferMapLit(t, `function main() { var x = { "a": 1 }; }`)
	if got.Kind != types.Record || len(got.Fields) != 1 || got.Fields[0].Name != "a" {
		t.Fatalf("`{ \"a\": 1 }` = %v / %+v, want record with field \"a\"", got.Kind, got.Fields)
	}

	// Genuinely dynamic key stays in the map family (must NOT become a record).
	got = inferMapLit(t, `function main() { var x = { "a" + "b": 1 }; }`)
	if got.Kind != types.Map {
		t.Fatalf("`{ \"a\" + \"b\": 1 }` inferred %v, want map (dynamic key)", got.Kind)
	}
}
