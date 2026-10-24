package compile

// return_stratification_contract_test.go — N-RET-1 的**语言级**判据（C3 双语义）。
//
// 规范 §5.7：`return` 的作用域 = **当前函数 / 当前流程**；循环不构成边界；
// 唯一例外是 QA `action` / attempt 分支体内的 `return`（§5.8 subscope return）。
//
// 已判（绿）：
//  1. 主流程 return = 终止（后续语句不得改变返回值）；
//  2. 子函数 return = 值回流（调用点拿到值后流程继续）。
//
// 已判（绿）：
//  3. 循环内 return 穿透循环 —— 返回值 = return 处看到的实时值（不是迭代开始
//     快照）。修复前恒为 n+1；根因是 body 对循环状态槽的引用读迭代快照，
//     而 loop_return 读实时 Vars —— 同一条语句链上两种新鲜度。修复 =
//     LoopState.SetVar 写穿本迭代读视图（见 loop_state_read_freshness 判据）。
//
// 未判（**known gap，见下**，带实测值，删 skip 即红）：
//  4. attempt 臂内 return = 分支结果值 —— 本地 runtime **装配不出这个形状**
//     （`RUNREACHABLE_NODE: node … (text) has no incoming edges`）。
//
// 为什么不用平台测试：SEM-N-RET-1 此前由 TestCozeBizReturnVariablesEndStaysReturn
// （断言 wire 的 terminatePlan）背书 —— 那是**平台形状**，不是语言语义。
// 本文件把 1/2 换成「源文本在本地 runtime 上的值」+「往返后仍是同一张表」。
// 3/4 未覆盖之前，该条目**不得**摘掉 evidence: platform —— 摘了就是拿一条判据
// 给三条断言背书。

import "testing"

// ── 1. 主流程 return = 终止 ────────────────────────────────────────────────
const retTerminate = `@language nodecoda/1
@mode workflow

function main(string q) -> string {
    string out;
    out = "before";
    if (q == "stop") {
        return "early";
    }
    out = "after";
    return out;
}
`

// ── 2. 子函数 return = 值回流 ──────────────────────────────────────────────
const retSubFunction = `@language nodecoda/1
@mode workflow

function add(int a, int b) -> int {
    return a + b;
}

function main(int n) -> int {
    int x;
    x = add(n, 10);
    return x;
}
`

// ── 3. attempt 臂内 return = 分支结果值（§5.8 subscope return）─────────────
const retAttemptSubscope = `@language nodecoda/1
@mode workflow

function main() -> string {
    string out;
    out = "start";
    attempt http("GET", "http://127.0.0.1:1/", {}) as resp {
        success { return "ok"; }
        failure(err) { return "err"; }
    }
    out = "after";
    return out;
}
`

// ── 4. 循环内 return 穿透循环（规范：循环不构成边界）───────────────────────
const retThroughLoop = `@language nodecoda/1
@mode workflow

function main(int n) -> int {
    int i;
    i = 0;
    while (i < 10) limit 10 {
        i = i + 1;
        if (i == n) {
            return i;
        }
    }
    return -1;
}
`

func TestReturnTerminatesMainFlow(t *testing.T) {
	runConstructDiff(t, "ret_terminate", retTerminate, []diffCase{
		{"早退分支", map[string]any{"q": "stop"}, "early"},
		{"走到末尾", map[string]any{"q": "go"}, "after"},
	})
}

func TestSubFunctionReturnFlowsBack(t *testing.T) {
	runConstructDiff(t, "ret_subfunction", retSubFunction, []diffCase{
		{"回流并继续", map[string]any{"n": int64(1)}, int64(11)},
		{"另一个入参", map[string]any{"n": int64(-5)}, int64(5)},
	})
}

// TestAttemptArmReturnIsSubscope —— 臂内 return = 分支结果值，流程继续。
//
// 修复前（2026-10-24）：编译期失败 `RUNREACHABLE_NODE: node 100009 (text) has
// no incoming edges` —— runtime 把所有 `end` 都当叶子并丢弃出边，而 attempt
// 臂端 End（SuccessEnd/FailureEnd）是**控制流载体**：`臂端 → 后继` 是活的
// 控制边。修复 = 只丢**规范出口** 900001 的出边（return 后的死代码），
// 非规范 End 的出边照常物化；臂端不再进 END（否则臂值会与真出口争 "output"）。
// 残余（同构造、失败臂相位）：见 attempt_subscope_value_contract_test.go
// 的 TestAttemptArmReturnAsFlowTailFailure（known gap）。
func TestAttemptArmReturnIsSubscope(t *testing.T) {
	runConstructDiff(t, "ret_attempt_subscope", retAttemptSubscope, []diffCase{
		{"臂内 return 不终止流程", map[string]any{}, "after"},
	})
}

// TestReturnPiercesLoop —— 规范 §5.7「循环不构成边界」：n ⇒ n。
//
// 修复前实测（源文本与往返后**同错**）：n=2⇒3、n=3⇒4、n=5⇒6，恒为 n+1；
// 根因不是 return 取值本身（它读实时 Vars），而是体内条件/自增读到迭代开始
// 快照（陈旧 k）：条件 k == n 命中时，自增已把 Vars 写成 n+1。
func TestReturnPiercesLoop(t *testing.T) {
	runConstructDiff(t, "ret_through_loop", retThroughLoop, []diffCase{
		{"n=2", map[string]any{"n": int64(2)}, int64(2)},
		{"n=3", map[string]any{"n": int64(3)}, int64(3)},
		{"n=5", map[string]any{"n": int64(5)}, int64(5)},
		{"循环内不命中", map[string]any{"n": int64(99)}, int64(-1)},
	})
}

// ── 5. return 之后的不可达语句（known gap）──────────────────────────────────
const retDeadCode = `@language nodecoda/1
@mode workflow

function main() -> string {
    return "ok";
    int x;
    x = 1;
    return "never";
}
`

// TestReturnDeadCodeDoesNotRun —— return 已终止流程，其后语句不得执行。
//
// 修复前（2026-10-24）：编译期失败 `RUNREACHABLE_NODE: node 100003 (code) has
// no incoming edges`。机制：投影把死代码挂在规范出口之后
// （`900001 → 100003 → 100004 → 900001`）；runtime 丢弃 900001 的出边（正确），
// 但把死代码链判成「无入边孤儿」并 fail loud。
// 修复：丢弃规范出口出边时，把**只经由这些边可达**的子图整体标成死代码
// （从入口可达的节点仍活），既不执行也不报孤儿。
func TestReturnDeadCodeDoesNotRun(t *testing.T) {
	runConstructDiff(t, "ret_dead_code", retDeadCode, []diffCase{
		{"死代码不执行", map[string]any{}, "ok"},
	})
}
