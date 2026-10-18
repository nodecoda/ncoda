package passes

// Ports of the Phase-0 capability-gate contract from the reference suite:
// unit/test_capability_manifest.py (manifest contract),
// regression/test_operation_failure_contract.py
// ::test_attempt_is_rejected_without_target_runtime_evidence, and
// integration/test_frontend_handoff.py
// ::test_dc_cap_004_parallel_external_return_is_rejected_with_e1037, plus
// self-built gate cases (conditions, list operators, loop exits) with
// per-message/per-line oracle evidence captured from the Python reference
// (capability_gate.py emit sites + compile_nodecoda_result).
//
// Harness: the full pipeline (DefaultPipeline().Run) for reachable cases;
// scope+type+gate-only whitebox (runCapabilityGateWhiteBox) for the two
// branches the reference's earlier passes pre-empt — CONDITION_EFFECTFUL
// (E1041 effect pass rejects workflow operations in conditions first) and
// the list-operator E1034 for non-FILE sort_by (E1003 type pass rejects
// sort_by on non-file arrays first). The whitebox mirrors
// runCodeAnalysisWhiteBox's isolation pattern and is documented per case.

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
)

// ── harness ────────────────────────────────────────────────────────────

// runCapabilityGateWhiteBox runs scope + type (populating Bindings and
// ExprTypes), discards their diagnostics, then runs ONLY the capability
// gate — for gate branches that earlier passes pre-empt in the full
// pipeline (see the header note).
func runCapabilityGateWhiteBox(t *testing.T, src string, profile *TargetProfile) (*Context, *ast.Program) {
	t.Helper()
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(profile)
	ScopePass{}.Run(prog, ctx)
	(&TypePass{}).Run(prog, ctx)
	ctx.Diagnostics = nil
	(&CapabilityGatePass{}).Run(prog, ctx)
	return ctx, prog
}

// callFinder collects the first call to a named builtin.
type callFinder struct {
	name string
	out  *ast.CallExpr
}

func (f *callFinder) Visit(n ast.Node) ast.Visitor {
	if call, ok := n.(*ast.CallExpr); ok {
		if id, ok := call.Func().(*ast.IdentExpr); ok && id.Name() == f.name {
			f.out = call
		}
	}
	return f
}

// assertSingleGateError asserts exactly one error with the given code on the
// given line/column whose message equals wantMessage (verbatim reference
// text). Column is pinned (M5, R2): the Go parser's columns match the
// Python oracle start columns for every ported case (measured and locked).
func assertSingleGateError(t *testing.T, res *FrontendResult, code diag.Code, line, col int, wantMessage string) {
	t.Helper()
	if errs := res.Errors(); len(errs) != 1 {
		t.Fatalf("want exactly one error, got %d: %v", len(errs), res.Diagnostics)
	}
	d := res.Errors()[0]
	if d.Code != code {
		t.Fatalf("error code = %d, want %d: %v", d.Code, code, d)
	}
	if d.Line != line || d.Column != col {
		t.Fatalf("error at %d:%d, want %d:%d: %v", d.Line, d.Column, line, col, d)
	}
	if d.Message != wantMessage {
		t.Fatalf("error message = %q, want %q", d.Message, wantMessage)
	}
}

func assertNoGateErrors(t *testing.T, res *FrontendResult) {
	t.Helper()
	for _, d := range res.Diagnostics {
		if d.Severity == diag.Error && (d.Code == diag.TARGET_NOT_LOWERABLE || d.Code == diag.PARALLEL_EXTERNAL_EXIT) {
			t.Fatalf("unexpected gate error %v: %v", d.Code, res.Diagnostics)
		}
	}
}

// rejectedProfile builds a target profile whose manifest is the default with
// one shape flipped to REJECTED, mirroring the reference's dataclasses.replace
// recipe (test_operation_failure_contract.py:101-116).
func rejectedProfile(shape CapabilityShape) *TargetProfile {
	p := DefaultTargetProfile()
	p.Capabilities = NewCapabilityManifestWithState(map[CapabilityShape]CapabilityState{
		shape: CapabilityStateRejected,
	})
	return p
}

// ── manifest contract (test_capability_manifest.py) ───────────────────

func TestCapabilityManifestUnknownShapeIsRejected(t *testing.T) {
	m := DefaultCapabilityManifest()
	entry := m.Lookup(CapabilityShape("future.unknown.shape"))
	if entry.State != CapabilityStateRejected {
		t.Fatalf("unknown shape state = %s, want REJECTED", entry.State)
	}
	if entry.Diagnostic() != diag.TARGET_NOT_LOWERABLE {
		t.Fatalf("unknown shape diagnostic = %d, want E1034", entry.Diagnostic())
	}
}

func TestDefaultManifestPublishesStates(t *testing.T) {
	m := DefaultCapabilityManifest()
	cases := []struct {
		shape CapabilityShape
		want  CapabilityState
	}{
		{CapabilityShapeConditionDirect, CapabilityStateEnabled},
		{CapabilityShapeConditionCalculation, CapabilityStateEnabled},
		{CapabilityShapeConditionEffectful, CapabilityStateRejected},
		{CapabilityShapeForExpressionWithYield, CapabilityStateEnabled},
		{CapabilityShapeForStatementPlain, CapabilityStateEnabled},
		{CapabilityShapeForStatementWithExit, CapabilityStateEnabled},
		{CapabilityShapeLoopFunctionReturn, CapabilityStateEnabled},
		{CapabilityShapeParallelIsolated, CapabilityStateEnabled},
		{CapabilityShapeControlAttempt, CapabilityStateEnabled},
		{CapabilityShapeStateAssignment, CapabilityStateEnabled},
		{CapabilityShapeClassificationEnum, CapabilityStateEnabled},
		{CapabilityShapeGraphonNodeQuestionClassifierEnum, CapabilityStateEnabled},
		{CapabilityShapeStructuredExtractionFlat, CapabilityStateEnabled},
		{CapabilityShapeStructuredExtractionFunctionCall, CapabilityStateEnabled},
		{CapabilityShapeStructuredExtractionEnum, CapabilityStateEnabled},
		{CapabilityShapeStructuredExtractionMemory, CapabilityStateEnabled},
		{CapabilityShapeStructuredExtractionVision, CapabilityStateEnabled},
	}
	for _, tc := range cases {
		if got := m.Lookup(tc.shape).State; got != tc.want {
			t.Errorf("shape %s state = %s, want %s", tc.shape, got, tc.want)
		}
	}
	if m.Len() != 17 {
		t.Fatalf("manifest has %d entries, want all 17 phase-0 shapes", m.Len())
	}
}

func TestCapabilityManifestConstructionCopiesEntries(t *testing.T) {
	source := map[CapabilityShape]CapabilityEntry{
		CapabilityShapeConditionDirect: {State: CapabilityStateEnabled},
	}
	m := NewCapabilityManifest("v2", source)
	source[CapabilityShapeConditionDirect] = CapabilityEntry{State: CapabilityStateRejected}
	if got := m.Lookup(CapabilityShapeConditionDirect).State; got != CapabilityStateEnabled {
		t.Fatalf("mutating the source map leaked into the manifest: state = %s", got)
	}
}

func TestCapabilityManifestEntriesAreReadOnly(t *testing.T) {
	m := DefaultCapabilityManifest()
	defer func() {
		if recover() == nil {
			t.Fatal("Entries().Set must panic (RoMap read-only contract)")
		}
	}()
	m.Entries().Set(CapabilityShapeConditionDirect, CapabilityEntry{State: CapabilityStateRejected})
}

func TestNewCapabilityManifestWithStateOverrides(t *testing.T) {
	m := NewCapabilityManifestWithState(map[CapabilityShape]CapabilityState{
		CapabilityShapeControlAttempt: CapabilityStateRejected,
	})
	if got := m.Lookup(CapabilityShapeControlAttempt).State; got != CapabilityStateRejected {
		t.Fatalf("overridden shape state = %s, want REJECTED", got)
	}
	if got := m.Lookup(CapabilityShapeConditionDirect).State; got != CapabilityStateEnabled {
		t.Fatalf("unoverridden shape state = %s, want ENABLED", got)
	}
	if got := m.Lookup(CapabilityShapeConditionEffectful).State; got != CapabilityStateRejected {
		t.Fatalf("default REJECTED shape changed to %s", got)
	}
}

func TestCozeBizManifestRejectsExtractionVision(t *testing.T) {
	m := CapabilityManifestForTarget("coze-biz-1.0")
	entry := m.Lookup(CapabilityShapeStructuredExtractionVision)
	if entry.State != CapabilityStateRejected {
		t.Fatalf("coze-biz extraction vision state = %s, want REJECTED", entry.State)
	}
	if entry.Diagnostic() != diag.TARGET_NOT_LOWERABLE {
		t.Fatalf("coze-biz extraction vision diagnostic = %d, want TARGET_NOT_LOWERABLE", entry.Diagnostic())
	}
}

// ── pass behavior: parallel external exits (E1037) ─────────────────────

func TestParallelReturnEndsTheFlow(t *testing.T) {
	// A return inside a parallel branch terminates the WHOLE flow (flow-end
	// semantic): the branch tail wires straight to end, the barrier does
	// not wait, and code after the parallel is naturally unreachable.
	// E1037 no longer fires for returns — only loop exits (break/continue)
	// crossing the parallel region stay rejected.
	const src = `
function main(string seed) -> string {
    parallel { { return "left"; } { var right = seed; } }
    return seed;
}
`
	res := DefaultPipeline().Run(src, "t.dc", nil)
	for _, d := range res.Diagnostics {
		if d.Code == diag.PARALLEL_EXTERNAL_EXIT || d.Severity == diag.Error {
			t.Fatalf("return inside parallel branch must compile (flow end), got: %v", d)
		}
	}
}

func TestParallelBreakTargetingOuterLoopIsRejectedWithE1037(t *testing.T) {
	// Oracle: capability_gate.py:729-731 — break inside a parallel branch
	// targeting a loop outside the parallel region.
	const src = `
function main() -> int {
    let items = [1, 2];
    for (item in items) {
        parallel { { break; } { let y = 1; } }
    }
    return 1;
}
`
	res := DefaultPipeline().Run(src, "t.dc", nil)
	assertSingleGateError(t, res, diag.PARALLEL_EXTERNAL_EXIT, 5, 22,
		"break targeting a loop outside the parallel branch is not supported")
}

// ── pass behavior: conditions (E1034) ─────────────────────────────────

func TestWhileNonDirectConditionIsRejectedWithE1034(t *testing.T) {
	// capability_gate.py:596-606 — while forbids logical conditions
	// (allow_logical=False), so a non-direct condition is rejected even
	// though CONDITION_CALCULATION is ENABLED by default.
	const src = `
function main(int x) -> int {
    while (x + 1 > 0) limit 10 {
        let y = x;
    }
    return x;
}
`
	res := DefaultPipeline().Run(src, "t.dc", nil)
	assertSingleGateError(t, res, diag.TARGET_NOT_LOWERABLE, 3, 5,
		"condition expression is not lowerable in this container for the selected target")
}

func TestEffectfulConditionIsRejectedWithE1034(t *testing.T) {
	// CONDITION_EFFECTFUL is the manifest's only default REJECTED shape
	// (capability_gate.py:596-613). Unreachable through the full pipeline:
	// the effect pass rejects workflow operations in conditions first with
	// E1041 (same as the reference), so the gate runs whitebox with
	// EffectFacts injected for the condition node.
	const src = `
function main() -> string {
    if (http("GET", "https://example.com")) {
        return "x";
    }
    return "y";
}
`
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	(&TypePass{}).Run(prog, ctx)
	var finder callFinder
	finder.name = "http"
	ast.Walk(&finder, prog)
	if finder.out == nil {
		t.Fatal("http call not found in source")
	}
	condID, ok := ctx.Bindings.NodeIDOf(finder.out)
	if !ok {
		t.Fatal("http call has no AstNodeID")
	}
	ctx.EffectFacts = binding.NewEffectFacts(
		map[binding.AstNodeID]binding.EffectKind{condID: binding.EffectWorkflowOperation}, nil)
	ctx.Diagnostics = nil
	(&CapabilityGatePass{}).Run(prog, ctx)
	if errs := ctx.Errors(); len(errs) != 1 {
		t.Fatalf("want exactly one error, got %d: %v", len(errs), ctx.Diagnostics)
	}
	d := ctx.Errors()[0]
	if d.Code != diag.TARGET_NOT_LOWERABLE || d.Line != 3 {
		t.Fatalf("got %v on line %d, want E1034 on line 3", d.Code, d.Line)
	}
	if d.Message != "condition expression is not lowerable in this container for the selected target" {
		t.Fatalf("message = %q", d.Message)
	}
}

func TestLegalConditionsHaveNoGateErrors(t *testing.T) {
	// capability_gate.py:615-647 — direct selectors and comparisons with
	// static literals are direct conditions.
	const directIf = `
function main(string query) -> string {
    if (query == "x") {
        return "a";
    }
    return "b";
}
`
	const directWhile = `
function main(bool flag) -> int {
    var acc = 0;
    while (flag) limit 10 {
        acc = acc + 1;
    }
    return acc;
}
`
	assertNoGateErrors(t, DefaultPipeline().Run(directIf, "t.dc", nil))
	assertNoGateErrors(t, DefaultPipeline().Run(directWhile, "t.dc", nil))
}

// ── pass behavior: list operators (E1034) ──────────────────────────────

func TestFilterListOperatorIsLowerable(t *testing.T) {
	// capability_gate.py:456-489 — a filter over a runtime array source with
	// a native selector predicate lowers; no E1034.
	const src = `
function main() -> int {
    let items = [1, 2];
    let result = filter(items, (x) -> x > 0);
    return 1;
}
`
	res := DefaultPipeline().Run(src, "t.dc", nil)
	assertNoGateErrors(t, res)
}

func TestSortByNonFileElementIsNotLowerable(t *testing.T) {
	// capability_gate.py:483-489 — sort_by over a string array cannot lower
	// (can_lower requires FILE elements; can_materialize rejects sort_by).
	// Unreachable through the full pipeline: the type pass rejects sort_by
	// on non-file arrays first with E1003 (same as the reference), so the
	// gate runs whitebox.
	const src = `
function main() -> int {
    let strings = ["a", "b"];
    let result = sort_by(strings, "name", "asc");
    return 1;
}
`
	ctx, _ := runCapabilityGateWhiteBox(t, src, nil)
	if errs := ctx.Errors(); len(errs) != 1 {
		t.Fatalf("want exactly one error, got %d: %v", len(errs), ctx.Diagnostics)
	}
	d := ctx.Errors()[0]
	if d.Code != diag.TARGET_NOT_LOWERABLE || d.Line != 4 {
		t.Fatalf("got %v on line %d, want E1034 on line 4", d.Code, d.Line)
	}
	if !strings.Contains(d.Message, "sort_by result type Type(array, ArrayType(element=Type(string)))") {
		t.Fatalf("message %q must contain the sort_by result-type prefix", d.Message)
	}
	if !strings.Contains(d.Message, "is not safely lowerable") {
		t.Fatalf("message %q must contain the lowerability tail", d.Message)
	}
}

// ── pass behavior: default manifest legal programs (regression) ────────

func TestDefaultManifestLegalProgramsHaveNoGateDiagnostics(t *testing.T) {
	// for statement, multi-branch parallel, attempt, loop return and while
	// continue are all ENABLED by default — no gate diagnostics.
	const src = `
function main(string seed) -> string {
    let values = [seed];
    for (value in values) {
        let observed = value;
    }
    parallel { { let a = seed; } { let b = 2; } }
    attempt http("GET", "https://example.com") as response {
        success { return response.body; }
        failure(error) { return error.error_type; }
    }
    return seed;
}
`
	assertNoGateErrors(t, DefaultPipeline().Run(src, "t.dc", nil))

	const loopReturn = `
function main(int seed) -> int {
    let values = [seed];
    for (value in values) {
        return value;
    }
    return 0;
}
`
	assertNoGateErrors(t, DefaultPipeline().Run(loopReturn, "t.dc", nil))

	const whileContinue = `
function main(bool flag) -> int {
    var acc = 0;
    while (flag) limit 10 {
        acc = acc + 1;
        continue;
    }
    return acc;
}
`
	assertNoGateErrors(t, DefaultPipeline().Run(whileContinue, "t.dc", nil))
}

// ── pass behavior: injected REJECTED manifests ─────────────────────────

func TestAttemptIsRejectedWithoutTargetRuntimeEvidence(t *testing.T) {
	// regression/test_operation_failure_contract.py
	// ::test_attempt_is_rejected_without_target_runtime_evidence.
	const src = `
function main() -> string {
    attempt http("GET", "https://example.com") as response {
        success { return response.body; }
        failure(error) { return error.error_type; }
    }
}
`
	res := DefaultPipeline().Run(src, "t.dc", rejectedProfile(CapabilityShapeControlAttempt))
	assertSingleGateError(t, res, diag.TARGET_NOT_LOWERABLE, 3, 5,
		"attempt/recovery control is not supported without verified target runtime semantics")
}

func TestForStatementIsRejectedWithoutRuntimeEvidence(t *testing.T) {
	const src = `
function main(string value) -> string {
    let values = [value];
    for (value in values) {
        let observed = value;
    }
    return value;
}
`
	res := DefaultPipeline().Run(src, "t.dc", rejectedProfile(CapabilityShapeForStatementPlain))
	assertSingleGateError(t, res, diag.TARGET_NOT_LOWERABLE, 4, 5,
		"for statement is not supported without DIFY-FOR-01 runtime evidence")
}

func TestForExpressionIsRejectedWithoutRuntimeEvidence(t *testing.T) {
	const src = `
function main() -> int {
    let items = [1, 2];
    var results = for (item in items) {
        yield item;
    };
    return 1;
}
`
	res := DefaultPipeline().Run(src, "t.dc", rejectedProfile(CapabilityShapeForExpressionWithYield))
	assertSingleGateError(t, res, diag.TARGET_NOT_LOWERABLE, 4, 19,
		"for expression is not supported without DIFY-FOREXPR-01 runtime evidence")
}

func TestParallelMultiBranchIsRejectedWithoutRuntimeSemantics(t *testing.T) {
	const src = `
function main(int seed) -> int {
    parallel { { let a = seed; } { let b = 2; } }
    return seed;
}
`
	res := DefaultPipeline().Run(src, "t.dc", rejectedProfile(CapabilityShapeParallelIsolated))
	assertSingleGateError(t, res, diag.TARGET_NOT_LOWERABLE, 3, 5,
		"multi-branch parallel is not supported without verified runtime semantics")
}

func TestReturnInsideLoopIsRejectedWithoutFunctionExitProtocol(t *testing.T) {
	const src = `
function main(int item) -> int {
    let items = [item];
    for (item in items) {
        return item;
    }
    return 0;
}
`
	res := DefaultPipeline().Run(src, "t.dc", rejectedProfile(CapabilityShapeLoopFunctionReturn))
	// 2026-09-13 D1/D3 用户拍板：这条**不是平台限制**——平台有通道（循环状态
	// 变量 + break + 循环后检查），是 lowering 未实现（internal + pending）。
	// 消息必须说「编译器缺口」，否则用户会去永久改掉本来没问题的代码。
	assertSingleGateError(t, res, diag.TARGET_NOT_LOWERABLE, 5, 9,
		"return inside a loop body is not lowerable to this target yet (compiler gap, not a "+
			"platform limit): the platform's only in-loop early exit is `break` plus loop state "+
			"variables, and the status/break rewriting is not implemented; until it lands, guard "+
			"the rest of the body with a condition instead of returning early")
	errs := res.Errors()
	if len(errs) != 1 {
		t.Fatalf("want exactly one error, got %d: %v", len(errs), res.Diagnostics)
	}
	if errs[0].Class != diag.ClassInternal || !errs[0].Pending {
		t.Fatalf("class = %q pending = %v, want internal/true（可约未实现，不得报成平台限制）",
			errs[0].Class, errs[0].Pending)
	}
	if errs[0].Capability != string(CapabilityShapeLoopFunctionReturn) {
		t.Fatalf("capability = %q, want %q", errs[0].Capability, CapabilityShapeLoopFunctionReturn)
	}
}

func TestForStatementWithExitIsRejected(t *testing.T) {
	// capability_gate.py:732-738 — break → "early exit", continue →
	// "flow control".
	const breakSrc = `
function main() -> int {
    let items = [1];
    for (item in items) {
        break;
    }
    return 0;
}
`
	res := DefaultPipeline().Run(breakSrc, "t.dc", rejectedProfile(CapabilityShapeForStatementWithExit))
	assertSingleGateError(t, res, diag.TARGET_NOT_LOWERABLE, 5, 9,
		"break inside iteration is not supported; use a while-loop for early exit")

	const continueSrc = `
function main() -> int {
    let items = [1];
    for (item in items) {
        continue;
    }
    return 0;
}
`
	res = DefaultPipeline().Run(continueSrc, "t.dc", rejectedProfile(CapabilityShapeForStatementWithExit))
	assertSingleGateError(t, res, diag.TARGET_NOT_LOWERABLE, 5, 9,
		"continue inside iteration is not supported; use a while-loop for flow control")
}

func TestConditionCalculationIsRejected(t *testing.T) {
	// capability_gate.py:596-613 — a non-direct, non-effectful condition
	// (x + 1 > 0 is a calculation) is rejected when CONDITION_CALCULATION
	// is REJECTED.
	const src = `
function main(int x) -> int {
    if (x + 1 > 0) {
        return 1;
    }
    return 0;
}
`
	res := DefaultPipeline().Run(src, "t.dc", rejectedProfile(CapabilityShapeConditionCalculation))
	assertSingleGateError(t, res, diag.TARGET_NOT_LOWERABLE, 3, 5,
		"condition expression is not lowerable in this container for the selected target")
}

// ── parallel-for (I1, R1) ──────────────────────────────────────────────

func TestParallelForIsDispatchedToForExpressionGate(t *testing.T) {
	// I1 (R1): Python visit_parallel_for_expr (ast.py:686-688) delegates to
	// visit_for_expr — a parallel-for runs the full for-expression gate
	// (loop_targets "for_expr" entry + FOR_EXPRESSION_WITH_YIELD). Oracle:
	// REJECTED manifest + with-yield parallel-for → E1034 line 4 col 19
	// "for expression is not supported without DIFY-FOREXPR-01 runtime
	// evidence" (with-yield so the yield contract passes and the gate runs).
	const src = `
function main() -> int {
    let items = [1, 2];
    var results = parallel for (x in items, concurrency: 2, on_error: terminate) {
        yield x;
    };
    return 1;
}
`
	res := DefaultPipeline().Run(src, "t.dc", rejectedProfile(CapabilityShapeForExpressionWithYield))
	assertSingleGateError(t, res, diag.TARGET_NOT_LOWERABLE, 4, 19,
		"for expression is not supported without DIFY-FOREXPR-01 runtime evidence")
}

func TestBreakInsideParallelForTargetsItsOwnLoop(t *testing.T) {
	// I1 (R1)：parallel-for 内的 break 目标是 parallel-for **自身**，所以能力门禁
	// 不得发 E1037「break targeting a loop outside the parallel branch」（那是
	// `parallel {}` 分支跨越的语义）；门禁白盒断言（loop_targets 归属）保持不变。
	//
	// 端到端期望随 **D-L3(a)（2026-09-14 用户拍板）**更新：并行迭代体内的
	// `break` 现在是**语言错** —— `PARALLEL_EXTERNAL_EXIT`（E1037），由
	// parallel 阶段（StageParallel，先于 semantic/capability_gate）发出并短路，
	// 因此语义阶段此前的 NOT_IN_LOOP 后端兜底不再被到达。判据与「循环种类」
	// 无关、与容器/嵌套深度无关：目标是内层**顺序**循环时仍合法（见
	// TestParallelForBreakInNestedSequentialLoopIsLegal）。
	const src = `
function main() -> int {
    let items = [1, 2];
    for (x in items) {
        parallel {
            { var r = parallel for (y in items, concurrency: 2, on_error: terminate) { break; }; }
            { let z = 1; }
        }
    }
    return 1;
}
`
	res := DefaultPipeline().Run(src, "t.dc", nil)
	errs := res.Errors()
	if len(errs) != 1 || errs[0].Code != diag.PARALLEL_EXTERNAL_EXIT {
		t.Fatalf("want exactly one E1037 (PARALLEL_EXTERNAL_EXIT), got %v", res.Diagnostics)
	}
	d := errs[0]
	if d.Line != 6 {
		t.Fatalf("E1037 = line %d %q, want line 6", d.Line, d.Message)
	}
	if d.Class != "" && d.Class != diag.ClassLanguage {
		t.Fatalf("class = %q, want language (缺省)", d.Class)
	}

	ctx, _ := runCapabilityGateWhiteBox(t, src, nil)
	for _, d := range ctx.Diagnostics {
		if d.Severity == diag.Error &&
			(d.Code == diag.PARALLEL_EXTERNAL_EXIT || d.Code == diag.TARGET_NOT_LOWERABLE) {
			t.Fatalf("unexpected gate diagnostic %v: %v", d.Code, ctx.Diagnostics)
		}
	}
}
