package passes

// Ports of regression/test_iteration_for_correctness.py yield-contract cases
// (E1043) plus self-built unit cases: exit keywords, top-level yield, nested
// loop cardinality, parallel branches, and the exactly-one-yield rule. The
// harness runs the full pipeline (DefaultPipeline), mirroring
// compile_nodecoda_result's pass loop.

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/diag"
)

// runFullPipeline compiles src through every implemented stage in pipeline
// order, mirroring compile_nodecoda_result's pass loop.
func runFullPipeline(t *testing.T, src string) *FrontendResult {
	t.Helper()
	return DefaultPipeline().Run(src, "t.dc", nil)
}

func yieldContractErrors(res *FrontendResult) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range res.Diagnostics {
		if d.Severity == diag.Error && d.Code == diag.YIELD_CONTRACT {
			out = append(out, d)
		}
	}
	return out
}

// assertYieldContract mirrors _assert_yield_contract
// (test_iteration_for_correctness.py:118-125): the only errors are E1043,
// the first one sits on the given line and the message contains the keyword.
func assertYieldContract(t *testing.T, res *FrontendResult, line int, keyword string) {
	t.Helper()
	if errs := res.Errors(); len(errs) != 1 || errs[0].Code != diag.YIELD_CONTRACT {
		t.Fatalf("want exactly one E1043 error, got %v", res.Diagnostics)
	}
	d := res.Errors()[0]
	if d.Line != line {
		t.Fatalf("E1043 line = %d, want %d: %v", d.Line, line, d)
	}
	if !strings.Contains(strings.ToLower(d.Message), keyword) {
		t.Fatalf("E1043 message %q must contain %q", d.Message, keyword)
	}
}

const forExprReturnSource = `function main(int item) {
    let items = [item];
    var results = for (item in items) {
        return item;
    };
}
`

const forExprBreakSource = `function main(int item) {
    let items = [item];
    var results = for (item in items) {
        break;
    };
}
`

const forExprContinueSource = `function main(int item) {
    let items = [item];
    var results = for (item in items) {
        continue;
    };
}
`

func TestForExprReturnExitIsClosed(t *testing.T) {
	assertYieldContract(t, runFullPipeline(t, forExprReturnSource), 4, "return")
}

// break/continue 在 value-producing for 内合法（coze loop Break/Continue
// 语义实证）：语法合法、语义死代码（break 后语句不可达），编译器放行。
// 提前退出路径豁免 "每条正常路径至少一个 yield" —— 全提前退出的 for 体
// 不再报 E1043（聚合恒空是作者语义，不强制 if 前置 break）。
func TestForExprBreakExitIsLegal(t *testing.T) {
	res := runFullPipeline(t, forExprBreakSource)
	if errs := yieldContractErrors(res); len(errs) != 0 {
		t.Fatalf("break in value-producing for must be legal, got E1043: %v", res.Diagnostics)
	}
}

func TestForExprContinueExitIsLegal(t *testing.T) {
	res := runFullPipeline(t, forExprContinueSource)
	if errs := yieldContractErrors(res); len(errs) != 0 {
		t.Fatalf("continue in value-producing for must be legal, got E1043: %v", res.Diagnostics)
	}
}

// yield 序列恒在 for 体末尾：yield 之后不允许其它语句（yield 是 loop 的
// 输出注解，类似 C 的 update-op，位置固定收尾）。
func TestForExprStatementAfterYieldIsRejected(t *testing.T) {
	const src = `function main() {
    let items = ["one"];
    let results = for (item in items) {
        yield output = item;
        let late = 1;
    };
}
`
	res := runFullPipeline(t, src)
	errs := yieldContractErrors(res)
	if len(errs) != 1 {
		t.Fatalf("want one E1043 for statement after yield, got %v", res.Diagnostics)
	}
	want := "yield must be the last statement(s) of a value-producing for"
	if errs[0].Message != want {
		t.Fatalf("E1043 message = %q, want %q", errs[0].Message, want)
	}
}

// 条件分支内的 break 合法：if (cond) { break; } 提前退出路径豁免 yield，
// 正常路径（else/fall-through 到末尾 yield）仍满足 "至少一个 yield"。
func TestForExprConditionalBreakIsLegal(t *testing.T) {
	const src = `function main() {
    let items = ["one"];
    let results = for (item in items) {
        if (item == "one") {
            break;
        }
        yield output = item;
    };
}
`
	res := runFullPipeline(t, src)
	if errs := yieldContractErrors(res); len(errs) != 0 {
		t.Fatalf("conditional break must be legal, got E1043: %v", res.Diagnostics)
	}
}

func TestForExprWithoutYieldIsRejected(t *testing.T) {
	const src = `function main() {
    let items = ["one"];
    let results = for (item in items) {
        let response = 1;
    };
}
`
	res := runFullPipeline(t, src)
	errs := yieldContractErrors(res)
	if len(errs) != 1 {
		t.Fatalf("want one E1043, got %v", res.Diagnostics)
	}
	want := "value-producing for requires at least one yield on every normal path"
	if errs[0].Message != want {
		t.Fatalf("message = %q, want %q", errs[0].Message, want)
	}
	if errs[0].Line != 3 {
		t.Fatalf("E1043 line = %d, want 3 (the for-expr)", errs[0].Line)
	}
}

func TestMissingYieldOnOneNormalPathIsRejected(t *testing.T) {
	const src = `
function main(string[] items) {
    let values = for (item in items) {
        if (item == "keep") { yield item; }
    };
}
`
	res := runFullPipeline(t, src)
	if errs := yieldContractErrors(res); len(errs) != 1 {
		t.Fatalf("want one E1043, got %v", res.Diagnostics)
	}
}

// 位置判据（2026-09-15 用户重申的设计）：yield 必须是循环体的**直接语句**。
// 负向：臂内 yield 一律拒绝（每条臂各报一次，位置精确到 yield）。
func TestYieldInsideIfArmIsRejected(t *testing.T) {
	const src = `
function main(string item) {
    let items = [item];
    let values = for (item in items) {
        if (item == "") { yield "empty"; } else { yield item; }
    };
}
`
	res := runFullPipeline(t, src)
	errs := yieldContractErrors(res)
	if len(errs) != 2 {
		t.Fatalf("want two E1043 (one per arm yield), got %v", res.Diagnostics)
	}
	for _, d := range errs {
		if d.Message != "yield must be a direct statement of the value-producing for body (it cannot be nested inside another statement; publish a conditional value with `yield (cond) ? a : b;` instead)" {
			t.Fatalf("E1043 message = %q", d.Message)
		}
	}
}

// 正向锚点：条件发布用**值位置表达式**表达则合法（位置判据不是"一律拒绝 yield"）。
// 只锁负向会被「一律报错」的错规则满足，只锁正向会被「什么都放行」满足 ⇒ 两侧同锁。
func TestConditionalYieldViaExpressionIsLegal(t *testing.T) {
	const src = `
function main(string item) {
    let items = [item];
    let values = for (item in items) {
        yield (item == "") ? "empty" : item;
    };
}
`
	res := runFullPipeline(t, src)
	if errs := yieldContractErrors(res); len(errs) != 0 {
		t.Fatalf("conditional-yield expression must satisfy the contract: %v", res.Diagnostics)
	}
	if len(res.Errors()) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors())
	}
}

func TestValidForExprPassesContract(t *testing.T) {
	const src = `function main(string[] items) {
    var r = for (item in items) { yield item; };
}
`
	res := runFullPipeline(t, src)
	if errs := yieldContractErrors(res); len(errs) != 0 {
		t.Fatalf("valid for-expr must not violate the contract: %v", errs)
	}
}

func TestYieldOutsideValueProducingForIsRejected(t *testing.T) {
	const src = `function main() {
    yield 1;
}
`
	res := runFullPipeline(t, src)
	errs := yieldContractErrors(res)
	if len(errs) != 1 {
		t.Fatalf("want one E1043, got %v", res.Diagnostics)
	}
	want := "yield is legal only inside a value-producing for"
	if errs[0].Message != want {
		t.Fatalf("message = %q, want %q", errs[0].Message, want)
	}
}

func TestYieldInsideStatementForIsRejected(t *testing.T) {
	const src = `function main(string[] items) {
    var r = for (item in items) {
        for (b in [item]) {
            yield b;
        }
    };
}
`
	res := runFullPipeline(t, src)
	errs := yieldContractErrors(res)
	if len(errs) != 1 {
		t.Fatalf("want one E1043, got %v", res.Diagnostics)
	}
	if errs[0].Message != "yield must be a direct statement of the value-producing for body (it cannot be nested inside another statement; publish a conditional value with `yield (cond) ? a : b;` instead)" {
		t.Fatalf("message = %q", errs[0].Message)
	}
}

func TestParallelStmtYieldIsRejected(t *testing.T) {
	// The parallel expression form (and its per-branch yield collection)
	// was removed: parallel is a pure control-flow barrier, so `yield`
	// inside a branch has no collection target — it is legal only inside a
	// value-producing for / parallel-for.
	const src = `function main(string[] items) {
    parallel {
        { yield "a"; }
        { let b = 1; }
    }
}
`
	res := runFullPipeline(t, src)
	for _, d := range res.Diagnostics {
		if d.Severity == diag.Error && d.Code != diag.YIELD_CONTRACT {
			t.Fatalf("unexpected error %v", d)
		}
	}
	errs := yieldContractErrors(res)
	if len(errs) < 1 || !strings.Contains(errs[0].Message, "yield is legal only inside a value-producing for") {
		t.Fatalf("want the yield-outside-for E1043, got %v", res.Diagnostics)
	}
}

func TestParallelStmtBreakIsRejected(t *testing.T) {
	// break/continue are loop constructs; inside a parallel branch (no
	// enclosing loop) they are illegal — a parallel statement is not a
	// loop container.
	const src = `function main(string[] items) {
    parallel {
        { break; }
        { let b = 1; }
    }
}
`
	res := runFullPipeline(t, src)
	if !res.HasErrors() {
		t.Fatal("break inside a parallel branch must not compile")
	}
}

func TestYieldInsideNestedForExprBodyIsRejected(t *testing.T) {
	// Reference parity (verified against the Python oracle): a yield inside a
	// nested for-expr cannot be counted by the outer for's block-outcome
	// analysis, so the reference emits exactly one E1043
	// "yield inside a nested loop or parallel region has unprovable
	// cardinality" and the outer "requires exactly one yield" aggregate is
	// suppressed by the has-error guard.
	const src = `function main(string[][] matrix) {
    var flat = for (row in matrix) {
        for (cell in row) {
            yield cell;
        }
    };
}
`
	res := runFullPipeline(t, src)
	errs := yieldContractErrors(res)
	if len(errs) != 1 {
		t.Fatalf("want exactly one E1043, got %v", res.Diagnostics)
	}
	if errs[0].Message != "yield must be a direct statement of the value-producing for body (it cannot be nested inside another statement; publish a conditional value with `yield (cond) ? a : b;` instead)" {
		t.Fatalf("message = %q", errs[0].Message)
	}
}

// 位置判据的递归封闭性：语句宿主家族是开放的，判据用通用遍历器（不枚举
// 容器类型）⇒ 内层 while / parallel 里的 yield 同样被拒。这两例是"以后新增
// 宿主也自动覆盖"的锚点。
func TestYieldInsideWhileInLoopBodyIsRejected(t *testing.T) {
	const src = `
function main(string item) {
    let items = [item];
    let values = for (item in items) {
        while (item == "") limit 3 {
            yield item;
        }
    };
}
`
	res := runFullPipeline(t, src)
	errs := yieldContractErrors(res)
	if len(errs) == 0 || errs[0].Message != "yield must be a direct statement of the value-producing for body (it cannot be nested inside another statement; publish a conditional value with `yield (cond) ? a : b;` instead)" {
		t.Fatalf("yield nested in a while inside the loop body must be rejected with the placement message: %v", res.Diagnostics)
	}
}

func TestYieldInsideParallelInLoopBodyIsRejected(t *testing.T) {
	const src = `
function main(string item) {
    let items = [item];
    let values = for (item in items) {
        parallel {
            { yield item; }
            { let b = 1; }
        }
    };
}
`
	res := runFullPipeline(t, src)
	errs := yieldContractErrors(res)
	if len(errs) == 0 || errs[0].Message != "yield must be a direct statement of the value-producing for body (it cannot be nested inside another statement; publish a conditional value with `yield (cond) ? a : b;` instead)" {
		t.Fatalf("yield nested in a parallel branch inside the loop body must be rejected with the placement message: %v", res.Diagnostics)
	}
}

// ── parallel-for (I2, R1) ──────────────────────────────────────────────
//
// Python visit_parallel_for_expr (ast.py:686-688) delegates to
// visit_for_expr, so parallel-fors are value-producing fors with the exact
// same yield contract. Before R1 the Go walker left *ast.ParallelForExpr
// undirected, so `yield` inside a parallel-for was misreported as E1043
// "yield is legal only inside a value-producing for". Oracle evidence
// (compile_nodecoda_result): with-yield → zero diagnostics; no-yield →
// E1043 line 4 col 19 "value-producing for requires exactly one yield on
// every normal path"; break/return inside → E1043 "break/return cannot exit
// a value-producing for" line 5.

func TestParallelForWithYieldIsLegal(t *testing.T) {
	const src = `
function main() -> int {
    let items = [1, 2];
    var results = parallel for (x in items, concurrency: 2, on_error: terminate) {
        yield x;
    };
    return 1;
}
`
	res := runFullPipeline(t, src)
	if errs := res.Errors(); len(errs) != 0 {
		t.Fatalf("parallel-for with yield must be legal, got %v", res.Diagnostics)
	}
}

func TestParallelForWithoutYieldIsRejected(t *testing.T) {
	const src = `
function main() -> int {
    let items = [1, 2];
    var results = parallel for (x in items, concurrency: 2, on_error: terminate) {
        let y = x;
    };
    return 1;
}
`
	res := runFullPipeline(t, src)
	if errs := res.Errors(); len(errs) != 1 || errs[0].Code != diag.YIELD_CONTRACT {
		t.Fatalf("want exactly one E1043, got %v", res.Diagnostics)
	}
	d := res.Errors()[0]
	if d.Line != 4 {
		t.Fatalf("E1043 line = %d, want 4: %v", d.Line, d)
	}
	if d.Message != "value-producing for requires at least one yield on every normal path" {
		t.Fatalf("E1043 message = %q", d.Message)
	}
}

// parallel-for → coze batch：**语言级改动（D-L3(a)，2026-09-14 用户拍板）**——
// 并行迭代体内的 `break`（终止整个循环 = 已执行/未执行的迭代集合随调度变化）
// 与 `return`（终止流程 = 哪个迭代的值胜出随调度变化）都是**语言错**：
// `PARALLEL_EXTERNAL_EXIT`（E1037），由 parallel 阶段发出。
//
// 本测试原期望语义阶段的 NOT_IN_LOOP（「batch 无循环控制语义」）/ yield 契约的
// E1043（「return 不能退出 value-producing for」）——两者是同一形状的**后端
// 兜底**，现在被更早的语言规则短路（end-to-end 先报 E1037）。顺序 `for` 表达式
// 上的 E1043 覆盖仍在（TestForExprReturnExitIsClosed / TestForExprBreakExitIsLegal）；
// 语义阶段的 NOT_IN_LOOP 兜底覆盖见 TestSemanticParallelForBreakBackstop。
func TestParallelForBreakCannotExitValueProducingFor(t *testing.T) {
	const src = `
function main() -> int {
    let items = [1, 2];
    var results = parallel for (x in items, concurrency: 2, on_error: terminate) {
        break;
    };
    return 1;
}
`
	res := runFullPipeline(t, src)
	errs := res.Errors()
	if len(errs) != 1 || errs[0].Code != diag.PARALLEL_EXTERNAL_EXIT {
		t.Fatalf("want exactly one E1037, got %v", res.Diagnostics)
	}
	if errs[0].Line != 5 {
		t.Fatalf("E1037 line = %d, want 5: %v", errs[0].Line, res.Diagnostics)
	}
}

func TestParallelForReturnCannotExitValueProducingFor(t *testing.T) {
	const src = `
function main() -> int {
    let items = [1, 2];
    var results = parallel for (x in items, concurrency: 2, on_error: terminate) {
        return 1;
    };
    return 1;
}
`
	res := runFullPipeline(t, src)
	if errs := res.Errors(); len(errs) != 1 || errs[0].Code != diag.PARALLEL_EXTERNAL_EXIT {
		t.Fatalf("want exactly one E1037, got %v", res.Diagnostics)
	}
	d := res.Errors()[0]
	if d.Line != 5 {
		t.Fatalf("E1037 line = %d, want 5: %v", d.Line, d)
	}
	if !strings.Contains(d.Message, "yield") {
		t.Fatalf("E1037 message must offer the yield workaround, got %q", d.Message)
	}
}

// ── I3 (R2): parallel-for in the owned-yield prune set ─────────────────
//
// Python's _contains_owned_yield prune test
// `isinstance(value, (ForExpr, ParallelExpr))` covers ParallelForExpr
// because it subclasses ForExpr (ast.py:208). The Go ownedYieldCollector
// switch is an exact-type dispatch, so *ast.ParallelForExpr must be listed
// explicitly (type_infer.go:1116 idiom). Before R2 the collector descended
// into a parallel-for nested in a value-producing for body and misreported
// its yield as E1043 "yield inside a nested loop or parallel region has
// unprovable cardinality". Oracle evidence (compile_nodecoda_result): both
// cases below produce zero diagnostics in Python; pre-fix Go emitted E1043
// on the `var r = parallel for` line.

func TestNestedParallelForYieldInValueProducingFor(t *testing.T) {
	const src = `function main() -> int {
    let items = [1, 2];
    var results = for (x in items) {
        var r = parallel for (y in items, concurrency: 2, on_error: terminate) {
            yield y;
        };
        yield x;
    };
    return 1;
}
`
	res := runFullPipeline(t, src)
	if errs := res.Errors(); len(errs) != 0 {
		t.Fatalf("nested parallel-for yield must be pruned, got %v", res.Diagnostics)
	}
}

func TestNestedParallelForYieldInIfBranchIsPruned(t *testing.T) {
	const src = `function main() -> int {
    let items = [1, 2];
    var results = for (x in items) {
        if (x == 1) {
            var r = parallel for (y in items, concurrency: 2, on_error: terminate) {
                yield y;
            };
        }
        yield x;
    };
    return 1;
}
`
	res := runFullPipeline(t, src)
	if errs := res.Errors(); len(errs) != 0 {
		t.Fatalf("nested parallel-for yield in if branch must be pruned, got %v", res.Diagnostics)
	}
}
