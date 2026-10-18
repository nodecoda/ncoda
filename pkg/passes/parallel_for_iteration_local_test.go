//@feature parallel-fence — P5/P27 parallel for 迭代体作用域（迭代局部写/逃逸读/控制转移）

package passes

// parallel_for_iteration_local_test.go — 「parallel for 迭代体是迭代局部作用域」
// 契约（先 RED，2026-09-14 用户拍板 D-L1(a)/D-L2(a)/D-L3(a)）。
//
// 语言模型（docs/ideas-todo/2026-09-14-parallel-for-language-semantics.md §1）：
//   S1 迭代独立（并发 = 数据竞争 = 编译错误，同 parallel{} 的 R2）；
//   S2 循环外名字的读 = 循环入口**快照**（跨迭代依赖结构上不可能）；
//   S3 循环外变量的写 = **迭代局部、不逃逸**（死写无害，不得报错）；
//   S4 控制转移：yield/continue 迭代局部（合法）；break（终止整个循环）与
//      return（终止流程）是**全局不确定效果** ⇒ 语言错。
//
// 实现判据：写集合 = 体内子树闭包；逃逸 = 「同一符号在循环体之外的读」。
// 现状（RED 原因）：语句形态 `parallel for (...) { }` 是 *ast.ForStmt +
// IsParallel，而 ParallelAnalysisPass 只覆盖表达式形态 *ast.ParallelForExpr
// ⇒ 语句形态整条漏检（且旧判据「体内写共享变量 = 错」过严，用户已裁定）。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/diag"
)

func runParallelForFrontend(t *testing.T, src string) *FrontendResult {
	t.Helper()
	return runParallelPipeline(t, src)
}

func diagCodes(res *FrontendResult) []string {
	out := []string{}
	for _, d := range res.Diagnostics {
		out = append(out, d.Code.String())
	}
	return out
}

// TestParallelForStatementEscapeReadIsRejected：语句形态 + 循环外读被迭代写过的
// 变量 ⇒ E1036（迭代写不逃逸；读到的只能是循环前的值）。
func TestParallelForStatementEscapeReadIsRejected(t *testing.T) {
	src := `
function main() -> string {
    string v1;
    v1 = "";
    parallel for (item in ["x", "y"], concurrency: 1, on_error: terminate) {
        v1 = item;
    }
    return v1;
}
`
	res := runParallelForFrontend(t, src)
	errs := res.Errors()
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want exactly one E1036", diagCodes(res))
	}
	if errs[0].Code != diag.PARALLEL_ACCESS_HAZARD {
		t.Fatalf("code = %d, want E1036 (PARALLEL_ACCESS_HAZARD)", int(errs[0].Code))
	}
	for _, want := range []string{"v1", "iteration-local", "yield"} {
		if !strings.Contains(errs[0].Message, want) {
			t.Errorf("message %q must mention %q", errs[0].Message, want)
		}
	}
}

// TestParallelForExpressionEscapeReadIsRejected：表达式形态同一把尺子（一条规则
// 两个形态，禁形态分叉）。
func TestParallelForExpressionEscapeReadIsRejected(t *testing.T) {
	src := `
function main() -> array<string> {
    string v1;
    v1 = "";
    let out = parallel for (item in ["x", "y"], concurrency: 1, on_error: terminate) {
        v1 = item;
        yield item;
    };
    return [v1];
}
`
	res := runParallelForFrontend(t, src)
	errs := res.Errors()
	if len(errs) != 1 || errs[0].Code != diag.PARALLEL_ACCESS_HAZARD {
		t.Fatalf("errors = %v, want exactly one E1036", diagCodes(res))
	}
}

// TestParallelForDeadWriteIsLegal（用户裁定 D-L1(a)）：迭代体写循环外变量但
// 无人读 = 死写，无害 ⇒ 不得报错。
func TestParallelForDeadWriteIsLegal(t *testing.T) {
	src := `
function main() -> string {
    string v1;
    v1 = "";
    parallel for (item in ["x", "y"], concurrency: 1, on_error: terminate) {
        v1 = item;
    }
    return "ok";
}
`
	res := runParallelForFrontend(t, src)
	if len(res.Errors()) != 0 {
		t.Fatalf("dead write must be legal, got %v", diagCodes(res))
	}
}

// TestParallelForInBodyReadAfterWriteIsLegal（S3）：体内写后读 = 迭代局部，
// 合法（读到的是本迭代的值）。
func TestParallelForInBodyReadAfterWriteIsLegal(t *testing.T) {
	src := `
function main() -> string {
    string v1;
    v1 = "";
    parallel for (item in ["x", "y"], concurrency: 1, on_error: terminate) {
        v1 = item;
        let seen = v1;
    }
    return "ok";
}
`
	res := runParallelForFrontend(t, src)
	if len(res.Errors()) != 0 {
		t.Fatalf("iteration-local write+read must be legal, got %v", diagCodes(res))
	}
}

// TestSequentialForOuterWriteReadIsLegal（负向控制）：顺序 for 的写逃逸（末次
// 胜出）⇒ 循环外读合法，同一条逃逸判据不得误伤。
func TestSequentialForOuterWriteReadIsLegal(t *testing.T) {
	src := `
function main() -> string {
    string v1;
    v1 = "";
    for (item in ["x", "y"]) {
        v1 = item;
    }
    return v1;
}
`
	res := runParallelForFrontend(t, src)
	if len(res.Errors()) != 0 {
		t.Fatalf("sequential for escape must stay legal, got %v", diagCodes(res))
	}
}

// TestParallelForContinueIsLegal（S4）：continue = 结束本迭代，迭代局部 ⇒ 合法。
func TestParallelForContinueIsLegal(t *testing.T) {
	src := `
function main() -> string {
    parallel for (item in ["x", "y"], concurrency: 1, on_error: terminate) {
        if (item == "x") { continue; }
    }
    return "ok";
}
`
	res := runParallelForFrontend(t, src)
	if len(res.Errors()) != 0 {
		t.Fatalf("continue inside an iteration must be legal, got %v", diagCodes(res))
	}
}

// TestParallelForBreakIsRejected（S4）：break = 终止整个循环 = 并发下的全局
// 不确定效果 ⇒ 语言错（E1037）。
func TestParallelForBreakIsRejected(t *testing.T) {
	src := `
function main() -> string {
    parallel for (item in ["x", "y"], concurrency: 1, on_error: terminate) {
        if (item == "x") { break; }
    }
    return "ok";
}
`
	res := runParallelForFrontend(t, src)
	errs := res.Errors()
	if len(errs) != 1 || errs[0].Code != diag.PARALLEL_EXTERNAL_EXIT {
		t.Fatalf("errors = %v, want exactly one E1037", diagCodes(res))
	}
	if !strings.Contains(errs[0].Message, "break") || !strings.Contains(errs[0].Message, "continue") {
		t.Errorf("message %q must name break and offer continue/for workarounds", errs[0].Message)
	}
}

// TestParallelForReturnIsRejected（S4）：return = 终止流程 = 并发下的全局不确定
// 效果（哪个迭代的值胜出不确定）⇒ 语言错（E1037），与循环种类无关。
func TestParallelForReturnIsRejected(t *testing.T) {
	src := `
function main() -> string {
    parallel for (item in ["x", "y"], concurrency: 1, on_error: terminate) {
        if (item == "x") { return "early"; }
    }
    return "ok";
}
`
	res := runParallelForFrontend(t, src)
	errs := res.Errors()
	if len(errs) != 1 || errs[0].Code != diag.PARALLEL_EXTERNAL_EXIT {
		t.Fatalf("errors = %v, want exactly one E1037", diagCodes(res))
	}
}

// TestParallelForTemplateReturnIsNotAnExternalExit（CB-12 收口，2026-09-27）：迭代体内的
// `return template "…"`（W2 答复端点）**不是**「带值逃逸循环」—— 它的值不是迭代产物，而是
// **流程答复端点**的值侧 ⇒ 位置归 `TEMPLATE_RETURN_PLACEMENT`（E1057，语言错），
// 不归本判据。本格只要求**这一侧不报 E1037**；E1057 那一侧的判据在编译器面
// （`internal/compiler` 的模板返回位置契约测试第四例 = `parallel for` 体）。两侧合起来
// 才是完整规则：「模板返回只允许出现在流程出口位置」——与循环/臂种类无关。
func TestParallelForTemplateReturnIsNotAnExternalExit(t *testing.T) {
	src := `
function main(string x) {
    parallel for (item in ["x", "y"], concurrency: 1, on_error: terminate) {
        return template "并发体内";
    }
}
`
	res := runParallelForFrontend(t, src)
	for _, d := range res.Diagnostics {
		if d.Code == diag.PARALLEL_EXTERNAL_EXIT {
			t.Fatalf("迭代体的模板返回被归成 E1037（`PARALLEL_EXTERNAL_EXIT`）；它应由位置判据"+
				"（E1057）处置，诊断 = %v", diagCodes(res))
		}
	}
}

// TestParallelForVoidReturnIsLegal（S4 收窄：证据驱动，2026-09-14）：迭代体内的
// **void** `return;`（终止流程、**不选值**）合法 —— 不选值 ⇒ 可观察结果确定（流程
// 结束），且平台**有通道**（容器内节点直连根 `end` 的边）。一手证据：语料 **5 份**
// 真实导出（`X19_Tdouyin3` / `X12_Tdouyin4` / `X20_Tred2` / `X22_T_piliang2` /
// `X23_Tpiliang_table_2`）的 batch 体内 `throw_error → 900001 end`，且 HEAD 上这
// 5 份编译成功、往返 `r0 ≍ r1` 逐字节相同。
//
// 反例在另一侧（同族另一半，必须同时锁）：**带值** `return <v>` 仍是语言错
// （E1037）—— 并发下「哪个迭代的值胜出」不确定，见 TestParallelForReturnIsRejected。
func TestParallelForVoidReturnIsLegal(t *testing.T) {
	src := `
function main(string s) {
    if (s == "stop") {
        return;
    }
    parallel for (item in ["x", "y"], concurrency: 1, on_error: terminate) {
        if (item == "x") {
            return;
        }
    }
    return;
}
`
	res := runParallelForFrontend(t, src)
	if errs := res.Errors(); len(errs) != 0 {
		t.Fatalf("void return 在并发迭代体内合法，不得报错；got %v", diagCodes(res))
	}
}

// TestParallelForBreakInNestedSequentialLoopIsLegal（递归边界）：体内再嵌顺序
// 循环，其 break 目标是**内层循环**（迭代局部）⇒ 合法；return 仍是流程终止 ⇒ 错。
func TestParallelForBreakInNestedSequentialLoopIsLegal(t *testing.T) {
	src := `
function main() -> string {
    parallel for (item in ["x", "y"], concurrency: 1, on_error: terminate) {
        for (inner in ["a", "b"]) {
            if (inner == "a") { break; }
        }
    }
    return "ok";
}
`
	res := runParallelForFrontend(t, src)
	if len(res.Errors()) != 0 {
		t.Fatalf("break targeting a nested loop must be legal, got %v", diagCodes(res))
	}
}
