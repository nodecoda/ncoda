//@feature parallel-fence — P5 parallel 栅栏：透明屏障/同名跨分支编译错误/join 引用（pkg/passes/parallel_test.go 全文件）

package passes

// Ports of lang/tests/lang/unit/test_parallel_access_check.py (6 cases) and
// lang/tests/lang/unit/test_loop_semantics.py (10 cases), plus self-built
// coverage for surfaces the reference reaches only through later stages:
// the parallel-pass E1035 export-conflict path (the binder's E1035 fires on
// DECLARED types; the parallel pass fires on INFERRED types with branch
// locations), the ParallelForExpr E1036 write check, conversation-variable
// loop-state exclusion, and facts immutability (Go has no
// FrozenInstanceError; unexported fields + accessor copies enforce the
// frozen contract at compile time).
//
// Recorded deviations:
//   - test_parallel_code_exports_keep_value_ids_in_join_consumer
//     (test_parallel_access_check.py:133) asserts on code-signature
//     ValueDefId wiring (code_signatures) — the Go compiler does not publish
//     that surface yet (deferred to the code-analysis task), so the case is
//     unreachable and is not ported.
//   - test_exitful_for_uses_loop_not_iteration /
//     test_loop_return_escapes_before_function_response assert on the reference target
//     backend graph (loop vs iteration nodes, __returning labels); the Go
//     frontend stops at LoopStatePass, so those cases are ported at pass
//     level (facts/no-errors) and the graph assertions are documented as
//     not portable.

import (
	"reflect"
	"sort"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/types"
)

// ── harness ────────────────────────────────────────────────────────────

// parallelFacts mirrors the reference _facts helper: bind only, then run
// analyze_parallel_stmt against the published snapshot. Facts computation
// (analyze_parallel_blocks) consults only the snapshot, so ScopePass alone
// matches test_parallel_access_check.py's ScopePass-only setup.
// findParallel parses src and returns the program plus its entry-body
// ParallelStmt (shared so tests can resolve snapshot IDs on the same
// program instance that produced the facts).
func findParallel(t *testing.T, src string) (*ast.Program, *ast.ParallelStmt) {
	t.Helper()
	prog := parseSourceOrFail(t, src)
	var parallel *ast.ParallelStmt
	for _, stmt := range prog.Entry().Body().Stmts() {
		if s, ok := stmt.(*ast.ParallelStmt); ok {
			parallel = s
			break
		}
	}
	if parallel == nil {
		t.Fatal("no ParallelStmt in entry body")
	}
	return prog, parallel
}

func parallelFacts(t *testing.T, src string) (ParallelFacts, *binding.Snapshot, *ast.Program) {
	t.Helper()
	prog, parallel := findParallel(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("binding errors: %v", ctx.Diagnostics)
	}
	snap := ctx.Bindings
	nodeID := func(n ast.Node) binding.AstNodeID {
		id, ok := snap.NodeIDOf(n)
		if !ok {
			t.Fatalf("node not enumerated: %T", n)
		}
		return id
	}
	return analyzeParallelStmt(parallel, snap, nodeID), snap, prog
}

// runParallelPipeline compiles through every available stage in pipeline
// order (desugar → normalize → scope → definite_init → effect → type →
// parallel → loop_state), mirroring compile_nodecoda_result's pass loop.
// The harness reuses a single Pipeline so stage registration mirrors the
// reference pipeline wiring.
func runParallelPipeline(t *testing.T, src string) *FrontendResult {
	t.Helper()
	p := New()
	if err := p.Register(StageDesugar, &DesugarPass{}); err != nil {
		t.Fatalf("Register(StageDesugar): %v", err)
	}
	if err := p.Register(StageNormalize, &NormalizePass{}); err != nil {
		t.Fatalf("Register(StageNormalize): %v", err)
	}
	if err := p.Register(StageDefiniteInit, &DefiniteInitPass{}); err != nil {
		t.Fatalf("Register(StageDefiniteInit): %v", err)
	}
	if err := p.Register(StageEffect, &EffectPass{}); err != nil {
		t.Fatalf("Register(StageEffect): %v", err)
	}
	if err := p.Register(StageType, &TypePass{}); err != nil {
		t.Fatalf("Register(StageType): %v", err)
	}
	if err := p.Register(StageParallel, &ParallelAnalysisPass{}); err != nil {
		t.Fatalf("Register(StageParallel): %v", err)
	}
	if err := p.Register(StageLoopState, &LoopStatePass{}); err != nil {
		t.Fatalf("Register(StageLoopState): %v", err)
	}
	return p.Run(src, "t.dc", nil)
}

// factNames maps a sorted symbol-id slice to its declaration names.
func symbolNames(snap *binding.Snapshot, ids []binding.SymbolID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		decl, ok := snap.Declarations().Get(id)
		if !ok {
			out = append(out, "<?>")
			continue
		}
		out = append(out, decl.Name)
	}
	sort.Strings(out)
	return out
}

// errCodes returns the error diagnostic codes in emission order.
func errCodes(result *FrontendResult) []diag.Code {
	out := []diag.Code{}
	for _, d := range result.Diagnostics {
		if d.Severity == diag.Error {
			out = append(out, d.Code)
		}
	}
	return out
}

// ── test_parallel_access_check.py ─────────────────────────────────────

const parallelFactsSource = `
function main(bool running, string seed) -> string {
    var shared = seed;
    parallel {
        {
            var local = "initial";
            var seed = "shadow";
            local = "updated";
            shared = "left";
            while (running) limit 3 { break; }
        }
        { var observed = shared; }
    }
    return shared;
}
`

func TestParallelBranchFactsAreDeterministicAndImmutable(t *testing.T) {
	first, _, _ := parallelFacts(t, parallelFactsSource)
	second, _, _ := parallelFacts(t, parallelFactsSource)

	if !reflect.DeepEqual(first, second) {
		t.Fatal("facts are not deterministic across binds")
	}
	// Go has no FrozenInstanceError (recorded deviation): the frozen
	// contract is enforced by unexported fields; accessors return fresh
	// copies so mutation cannot reach the facts.
	reads := first.Branches()[0].Reads()
	reads[0] = ParallelSymbolAccess{} // must not reach the facts
	if got := first.Branches()[0].Reads(); reflect.DeepEqual(reads, got) {
		t.Fatal("Reads() returned a mutable alias of the facts")
	}
	exports := first.Branches()[0].Exports()
	exports[0] = 0 // must not reach the facts
	if got := first.Branches()[0].Exports(); reflect.DeepEqual(exports, got) {
		t.Fatal("Exports() returned a mutable alias of the facts")
	}
}

func TestParallelBranchFactsUseSharedSymbolIDsOnly(t *testing.T) {
	facts, snap, _ := parallelFacts(t, parallelFactsSource)
	if len(facts.Branches()) != 2 {
		t.Fatalf("branches = %d, want 2", len(facts.Branches()))
	}
	left, right := facts.Branches()[0], facts.Branches()[1]

	if got, want := symbolNames(snap, left.ReadSymbols()), []string{"running"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("left reads = %v, want %v", got, want)
	}
	if got, want := symbolNames(snap, left.WriteSymbols()), []string{"shared"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("left writes = %v, want %v", got, want)
	}
	if got, want := symbolNames(snap, left.Exports()), []string{"local"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("left exports = %v, want %v", got, want)
	}
	if got, want := symbolNames(snap, right.ReadSymbols()), []string{"shared"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("right reads = %v, want %v", got, want)
	}
	if writes := right.WriteSymbols(); len(writes) != 0 {
		t.Fatalf("right writes = %v, want none", symbolNames(snap, writes))
	}
	if got, want := symbolNames(snap, right.Exports()), []string{"observed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("right exports = %v, want %v", got, want)
	}
}

func TestAssignmentTargetIsWriteOnlyAndLocalAssignmentIsExcluded(t *testing.T) {
	facts, snap, _ := parallelFacts(t, parallelFactsSource)
	left := facts.Branches()[0]

	for _, name := range symbolNames(snap, left.ReadSymbols()) {
		if name == "shared" {
			t.Fatal("assignment target 'shared' must not appear in reads")
		}
	}
	for _, name := range append(symbolNames(snap, left.ReadSymbols()), symbolNames(snap, left.WriteSymbols())...) {
		if name == "local" {
			t.Fatal("branch-local 'local' must not appear in reads or writes")
		}
	}
}

func TestBranchOwnedBreakIsRecordedInExitSet(t *testing.T) {
	facts, snap, prog := parallelFacts(t, parallelFactsSource)
	left, right := facts.Branches()[0], facts.Branches()[1]

	targets := left.ExitTargets()
	if len(targets) != 1 {
		t.Fatalf("left exits = %v, want exactly one BREAK target", targets)
	}
	if targets[0].Kind != binding.ExitBreak {
		t.Fatalf("left exit kind = %v, want BREAK", targets[0].Kind)
	}
	// The ExitTarget keeps the bound loop scope: the branch while's body
	// scope (Python ExitTarget{kind, scope_id}).
	var parallel *ast.ParallelStmt
	for _, stmt := range prog.Entry().Body().Stmts() {
		if s, ok := stmt.(*ast.ParallelStmt); ok {
			parallel = s
			break
		}
	}
	branchStmts := parallel.Branches()[0].Stmts()
	while := branchStmts[len(branchStmts)-1].(*ast.WhileStmt)
	bodyID, _ := snap.NodeIDOf(while.Body())
	wantScope, _ := snap.ScopeBindings().Get(bodyID)
	if targets[0].ScopeID != wantScope {
		t.Fatalf("exit target scope = %d, want while body scope %d", targets[0].ScopeID, wantScope)
	}
	if exits := right.ExitTargets(); len(exits) != 0 {
		t.Fatalf("right exits = %v, want none", exits)
	}
}

// TestExitTargetsKeepDistinctLoopScopes locks the I1 review finding: Python
// exit_targets is a frozenset of full ExitTarget values (kind + scope_id),
// so two breaks bound to different while scopes stay distinct; a kind-only
// view would collapse them into one.
func TestExitTargetsKeepDistinctLoopScopes(t *testing.T) {
	src := `
function main(bool running) -> string {
    var shared = "s";
    parallel {
        {
            while (running) limit 3 { break; }
            while (running) limit 2 { break; }
        }
        { var observed = shared; }
    }
    return shared;
}
`
	facts, _, _ := parallelFacts(t, src)
	left := facts.Branches()[0]
	targets := left.ExitTargets()
	if len(targets) != 2 {
		t.Fatalf("exits = %v, want two distinct BREAK targets (one per while scope)", targets)
	}
	for _, target := range targets {
		if target.Kind != binding.ExitBreak {
			t.Fatalf("exit kind = %v, want BREAK", target.Kind)
		}
	}
	if targets[0].ScopeID == targets[1].ScopeID {
		t.Fatalf("both breaks bound to the same scope %d; want distinct while scopes", targets[0].ScopeID)
	}
}

func TestParallelJoinReferenceUsesBranchDeclarationSymbolID(t *testing.T) {
	src := `
    function main(string seed) -> string {
        parallel {
            { var left = seed; }
            { var right = seed; }
        }
        return left;
    }
    `
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("binding errors: %v", ctx.Diagnostics)
	}
	snap := ctx.Bindings
	body := prog.Entry().Body().Stmts()
	parallel, ok := body[0].(*ast.ParallelStmt)
	if !ok {
		t.Fatalf("stmts[0] is %T, want *ast.ParallelStmt", body[0])
	}
	declaration := parallel.Branches()[0].Stmts()[0]
	continuation, ok := body[1].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("stmts[1] is %T, want *ast.ReturnStmt", body[1])
	}
	value, ok := continuation.Value().(*ast.IdentExpr)
	if !ok {
		t.Fatalf("return value is %T, want *ast.IdentExpr", continuation.Value())
	}
	declarationID, ok := snap.NodeIDOf(declaration)
	if !ok {
		t.Fatal("declaration not enumerated")
	}
	referenceID, ok := snap.NodeIDOf(value)
	if !ok {
		t.Fatal("return value not enumerated")
	}
	declarationSymbol, _ := snap.DeclarationSymbols().Get(declarationID)
	referenceSymbol, found := snap.ReferenceSymbols().Get(referenceID)
	if !found {
		t.Fatal("return value has no reference symbol")
	}
	if referenceSymbol != declarationSymbol {
		t.Fatalf("reference symbol = %d, want branch declaration symbol %d", referenceSymbol, declarationSymbol)
	}
}

// test_parallel_code_exports_keep_value_ids_in_join_consumer
// (test_parallel_access_check.py:133) is NOT ported: it asserts on
// code-signature ValueDefId wiring, which the Go compiler does not publish
// yet (recorded deviation).

// ── self-built: parallel-pass E1035 / E1036 diagnostics ───────────────

func TestParallelExportConflictUsesInferredTypesWithLocations(t *testing.T) {
	src := `
function main() -> string {
    parallel {
        { var x = "left"; }
        { var x = 42; }
    }
    return "done";
}
`
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("binding errors: %v", ctx.Diagnostics)
	}
	(&TypePass{}).Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("type errors: %v", ctx.Diagnostics)
	}
	(&ParallelAnalysisPass{}).Run(prog, ctx)

	var conflict *diag.Diagnostic
	for i := range ctx.Diagnostics {
		if ctx.Diagnostics[i].Code == diag.PARALLEL_EXPORT_CONFLICT {
			conflict = &ctx.Diagnostics[i]
			break
		}
	}
	if conflict == nil {
		t.Fatalf("no E1035 emitted; got %v", errorCodesFromCtx(ctx))
	}
	parallel := prog.Entry().Body().Stmts()[0].(*ast.ParallelStmt)
	leftStmt := parallel.Branches()[0].Stmts()[0]
	rightStmt := parallel.Branches()[1].Stmts()[0]
	want := "Variable 'x' is exported from multiple parallel branches; " +
		"rename the branch locals (ambiguous downstream reference): " +
		"Type(int), Type(string); branch 1 at " + leftStmt.Span().Start.String() +
		"; branch 2 at " + rightStmt.Span().Start.String()
	if conflict.Message != want {
		t.Fatalf("message = %q, want %q", conflict.Message, want)
	}
	if conflict.Line == 0 {
		t.Fatal("E1035 must carry the parallel statement location")
	}
}

// TestParallelForIterationWriteIsNotAnError —— **语言级改动（D-L1(a)，2026-09-14
// 用户拍板）**：本测试原为 Python 参考实现的 1:1 移植（`Parallel-for cannot
// write shared variable 'shared'`，见 parallel_analysis.py:136-149）。
//
// 该规则被用户判定**太严**并废止：迭代体对循环外变量的写是**迭代局部、不逃逸**
// （S3）——死写无害，按迭代 `yield` 收集是最自然的写法，语言不需要禁止。取而代之
// 的判据只有一条：**循环之后读到「被迭代写过」的名字**才是错（E1036，读到的必然
// 是循环前的值，与作者的写意图不符）。
//
// 本测试的正例形状同时钉住判据的另一半：**循环之前**的读（`let items = [shared];`
// 里的 shared）不是逃逸读 —— 它读的就是循环前的值，确定且无歧义，不得报错。
// 反例（循环之后的读 ⇒ E1036）见
// TestParallelForExpressionEscapeReadIsRejected（parallel_for_iteration_local_test.go）。
func TestParallelForIterationWriteIsNotAnError(t *testing.T) {
	src := `
function main() -> array<string> {
    var shared = "s";
    let items = [shared];
    let out = parallel for (item in items, concurrency: 2, on_error: terminate) {
        shared = "x";
        yield item;
    };
    return out;
}
`
	result := runParallelPipeline(t, src)
	if len(result.Errors()) != 0 {
		t.Fatalf("iteration-local write must be legal, got %v", errCodes(result))
	}
}

// ── test_loop_semantics.py ────────────────────────────────────────────

func TestWhileLoopCompiles(t *testing.T) {
	src := `@mode workflow

function main() {
    var i = 0;
    while (i < 10) limit 10 {
        var x = i + 1;
    }
}
`
	result := runParallelPipeline(t, src)
	if len(result.Errors()) != 0 {
		t.Fatalf("expected ok but got errors: %v", result.Diagnostics)
	}
}

func TestWhileWithBreak(t *testing.T) {
	src := `@mode workflow

function main() {
    var i = 0;
    while (i < 10) limit 10 {
        if (i == 5) {
            break;
        }
        var x = i + 1;
    }
}
`
	result := runParallelPipeline(t, src)
	if len(result.Errors()) != 0 {
		t.Fatalf("expected ok but got errors: %v", result.Diagnostics)
	}
}

func TestWhileWithContinue(t *testing.T) {
	src := `@mode workflow

function main() {
    var i = 0;
    while (i < 10) limit 10 {
        var x = i + 1;
        if (x == 5) {
            continue;
        }
        var y = x * 2;
    }
}
`
	result := runParallelPipeline(t, src)
	if len(result.Errors()) != 0 {
		t.Fatalf("expected ok but got errors: %v", result.Diagnostics)
	}
}

func TestReturnInLoop(t *testing.T) {
	src := `@mode workflow

function main() {
    var i = 0;
    while (i < 10) limit 10 {
        if (i == 5) {
            return;
        }
        var x = i + 1;
    }
}
`
	result := runParallelPipeline(t, src)
	if len(result.Errors()) != 0 {
		t.Fatalf("expected ok but got errors: %v", result.Diagnostics)
	}
}

func TestLoopVariableScope(t *testing.T) {
	src := `@mode workflow

function main() {
    var i = 0;
    while (i < 5) limit 5 {
        var inner = i * 2;
        var x = inner + 1;
    }
}
`
	result := runParallelPipeline(t, src)
	if len(result.Errors()) != 0 {
		t.Fatalf("expected ok but got errors: %v", result.Diagnostics)
	}
}

// loopStateContext compiles and returns the Context (post loop_state).
func loopStateContext(t *testing.T, src string) (*Context, *ast.Program) {
	t.Helper()
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("binding errors: %v", ctx.Diagnostics)
	}
	(&TypePass{}).Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("type errors: %v", ctx.Diagnostics)
	}
	(&LoopStatePass{}).Run(prog, ctx)
	return ctx, prog
}

func TestLoopStateIsDeclaredAndReexposed(t *testing.T) {
	src := `
        function main() -> int {
            var index = 0;
            while (index < 3) limit 3 { index += 1; }
            return index;
        }
        `
	ctx, prog := loopStateContext(t, src)
	body := prog.Entry().Body().Stmts()
	while := body[1].(*ast.WhileStmt)
	ownerID, _ := ctx.Bindings.NodeIDOf(while)

	facts, found := ctx.LoopStates[ownerID]
	if !found {
		t.Fatalf("no loop-state facts for the while node; got %v", loopStateOwnerNames(ctx))
	}
	if len(facts) != 1 {
		t.Fatalf("facts = %d, want 1", len(facts))
	}
	fact := facts[0]
	decl, _ := ctx.Bindings.Declarations().Get(fact.SymbolID)
	if decl.Name != "index" {
		t.Fatalf("fact symbol = %q, want index", decl.Name)
	}
	if fact.InitialValue != int64(0) {
		t.Fatalf("initial value = %v, want 0", fact.InitialValue)
	}
	if fact.TypeInfo == nil || fact.TypeInfo.Kind != types.Int {
		t.Fatalf("type = %v, want int", fact.TypeInfo)
	}
	declID, _ := ctx.Bindings.NodeIDOf(body[0])
	if fact.InitializerID != declID {
		t.Fatalf("initializer id = %d, want declaration id %d", fact.InitializerID, declID)
	}
	bodyID, _ := ctx.Bindings.NodeIDOf(while.Body())
	wantScope, _ := ctx.Bindings.ScopeBindings().Get(bodyID)
	if fact.OwnerScopeID != wantScope {
		t.Fatalf("owner scope = %d, want %d", fact.OwnerScopeID, wantScope)
	}
	assign := while.Body().Stmts()[0]
	assignID, _ := ctx.Bindings.NodeIDOf(assign)
	if len(fact.WriteSites) != 1 || fact.WriteSites[0] != assignID {
		t.Fatalf("write sites = %v, want [%d]", fact.WriteSites, assignID)
	}
}

// test_exitful_for_uses_loop_not_iteration asserts on the reference backend graph
// (loop vs iteration nodes); the Go frontend stops at LoopStatePass. Ported
// at pass level: the for loop compiles cleanly and produces no loop-state
// facts (for loops never own mutable state).
func TestExitfulForUsesLoopNotIteration(t *testing.T) {
	src := `
        function main(string item) {
            let items = [item];
            for (item in items) { if (item == "stop") { break; } }
        }
        `
	result := runParallelPipeline(t, src)
	if len(result.Errors()) != 0 {
		t.Fatalf("expected ok but got errors: %v", result.Diagnostics)
	}
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	(&TypePass{}).Run(prog, ctx)
	(&LoopStatePass{}).Run(prog, ctx)
	if len(ctx.LoopStates) != 0 {
		t.Fatalf("for loop must not own state; got %v", loopStateOwnerNames(ctx))
	}
}

// TestMutationOutsideAnOwningLoopIsLegal 改判（A 级，2026-09-19 用户拍板）：
// 本用例原名 TestMutationOutsideAnOwningLoopIsRejected，钉的是 E1058 的**域外**
// 行为 —— 选择谓词「可变 ∧ 声明处有字面量初值」宽于规则域（loop state），于是
// 没有任何 while 的程序也报 E1058，而文案断言 "before its owning while loop"
// （指向一个不存在的 while）。收窄后：无 owning while ⇒ 本规则不表态，普通可变
// 本地由 IR 的通用规则处理（`T v = init;` ≡ `T v; v = init;`，用户同日拍板）。
// 存在侧（确有 owning while 且体外还有写 ⇒ 仍 E1058）见
// TestLoopStateContractStillFiresWithinItsDomain。
func TestMutationOutsideAnOwningLoopIsLegal(t *testing.T) {
	src := `
        function main() -> int {
            var value = 0;
            value += 1;
            return value;
        }
        `
	result := runParallelPipeline(t, src)
	if got := errCodes(result); len(got) != 0 {
		t.Fatalf("error codes = %v, want none (no owning while ⇒ not workflow state)", got)
	}
}

// test_loop_return_escapes_before_function_response asserts on the reference target
// backend graph (__returning labels); ported at pass level: the loop
// compiles cleanly and the state fact is still published.
func TestLoopReturnEscapesBeforeFunctionResponse(t *testing.T) {
	src := `
        function main() -> int {
            var index = 0;
            while (index < 3) limit 3 {
                if (index == 1) { return index; }
                index += 1;
            }
            return index;
        }
        `
	ctx, prog := loopStateContext(t, src)
	if len(ctx.Diagnostics) != 0 {
		t.Fatalf("expected ok but got errors: %v", ctx.Diagnostics)
	}
	body := prog.Entry().Body().Stmts()
	ownerID, _ := ctx.Bindings.NodeIDOf(body[1])
	if facts := ctx.LoopStates[ownerID]; len(facts) != 1 {
		t.Fatalf("index fact missing; got %v", loopStateOwnerNames(ctx))
	}
}

func TestLoopReexposesAllContiguousProvenState(t *testing.T) {
	src := `
        function main() -> int {
            var index = 0;
            var total = 0;
            while (index < 3) limit 3 {
                index += 1;
                total += index;
            }
            return total;
        }
        `
	ctx, prog := loopStateContext(t, src)
	body := prog.Entry().Body().Stmts()
	ownerID, _ := ctx.Bindings.NodeIDOf(body[2])
	facts, found := ctx.LoopStates[ownerID]
	if !found {
		t.Fatalf("no loop-state facts; got %v", loopStateOwnerNames(ctx))
	}
	if len(facts) != 2 {
		t.Fatalf("facts = %d, want 2", len(facts))
	}
	names := []string{}
	for _, f := range facts {
		decl, _ := ctx.Bindings.Declarations().Get(f.SymbolID)
		names = append(names, decl.Name)
	}
	if want := []string{"index", "total"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("fact names = %v, want %v", names, want)
	}
}

// ── self-built: conversation vars are not loop state ──────────────────

func TestConversationVarWriteIsNotLoopState(t *testing.T) {
	src := `@mode advanced-chat
@conversation array<string> messages = [];
function main() {
    while (true) limit 3 {
        messages << "hi";
    }
}
`
	result := runParallelPipeline(t, src)
	if len(result.Errors()) != 0 {
		t.Fatalf("expected ok but got errors: %v", result.Diagnostics)
	}
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	if len(ctx.ConversationVarIDs) == 0 {
		t.Fatal("ctx.ConversationVarIDs must contain the conversation variable")
	}
	found := false
	for symID := range ctx.ConversationVarIDs {
		if decl, ok := ctx.Bindings.Declarations().Get(symID); ok && decl.Name == "messages" {
			found = true
		}
	}
	if !found {
		t.Fatal("ctx.ConversationVarIDs does not include 'messages'")
	}
	(&TypePass{}).Run(prog, ctx)
	(&LoopStatePass{}).Run(prog, ctx)
	if len(ctx.LoopStates) != 0 {
		t.Fatalf("conversation var write must not produce loop state; got %v", loopStateOwnerNames(ctx))
	}
}

// loopStateOwnerNames renders the owners of the published loop states for
// failure messages.
func loopStateOwnerNames(ctx *Context) []binding.AstNodeID {
	out := []binding.AstNodeID{}
	for owner := range ctx.LoopStates {
		out = append(out, owner)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func errorCodesFromCtx(ctx *Context) []diag.Code {
	out := []diag.Code{}
	for _, d := range ctx.Diagnostics {
		if d.Severity == diag.Error {
			out = append(out, d.Code)
		}
	}
	return out
}

// ── self-built: loop-state ownership edge cases (reference-verified) ───

func TestNestedWhileOwners(t *testing.T) {
	src := `
function main() -> int {
    var i = 0;
    while (i < 3) limit 3 {
        var j = 0;
        while (j < 2) limit 2 { j += 1; }
        i += 1;
    }
    return i;
}
`
	ctx, prog := loopStateContext(t, src)
	body := prog.Entry().Body().Stmts()
	outer := body[1].(*ast.WhileStmt)
	inner := outer.Body().Stmts()[1].(*ast.WhileStmt)
	outerID, _ := ctx.Bindings.NodeIDOf(outer)
	innerID, _ := ctx.Bindings.NodeIDOf(inner)

	ownerNames := func(ownerID binding.AstNodeID) []string {
		facts, found := ctx.LoopStates[ownerID]
		if !found {
			return nil
		}
		out := []string{}
		for _, f := range facts {
			decl, _ := ctx.Bindings.Declarations().Get(f.SymbolID)
			out = append(out, decl.Name)
		}
		return out
	}
	if got, want := ownerNames(outerID), []string{"i"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("outer while owns %v, want [i]", got)
	}
	if got, want := ownerNames(innerID), []string{"j"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("inner while owns %v, want [j]", got)
	}
}

// 改判（A 级，2026-09-19 用户拍板）：本用例原名 TestParallelWriteIsRejected，钉的是
// 「任何 parallel 内写都拒」——该规则被推翻：`parallel { A } { B }` 与 `A; B` 的差异
// 只有并发交错，写体外绑定 / 写分支局部都是**普通写**（栅栏后可读）。语料规范形正是
// 这个形状（672 源中 92 份含带赋值的 parallel 块，见 parallel_scope_contract_test.go）。
// 唯一仍拒 = 同一绑定被两个分支触碰（写/写、读/写），见
// TestParallelCrossBranchConflictIsAConflict。
func TestParallelSingleBranchWriteIsLegal(t *testing.T) {
	src := `
function main() -> int {
    var x = 0;
    parallel {
        { x = 1; }
        { var y = 2; }
    }
    return x;
}
`
	result := runParallelPipeline(t, src)
	if got := errCodes(result); len(got) != 0 {
		t.Fatalf("error codes = %v, want none (a single writer is deterministic)", got)
	}
}
