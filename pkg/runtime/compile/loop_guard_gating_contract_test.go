package compile

// loop_guard_gating_contract_test.go — N-LOOP-2 的语言级判据：
// `while (c)` 的守卫门控**整个迭代体**——c 为假时，体内的任何节点都不得执行，
// 包括那些直接读循环状态变量、在图上表现为「无控制前驱」的节点。
//
// 为什么需要单独一条（一手，2026-10-24）：`while` 在 IR 里投影为
// `if (cond) { body } else { break }`（语言仓 pkg/ir/build_stmt.go::emitWhileStmt）。
// body 子图装配时，**所有**引用外部（循环容器）数据的节点都被接到 START 上
// （internal/runtime/compile/compile.go 的 external 分支）——于是守卫为假的
// 那一轮，只读循环状态的兄弟节点（如 if 的比较节点）照样执行。实测：
//
//	i=0; out=0; while (i < 3) { i = i + 1; if (i == 3) { out = out + 1; } } return out;
//	修复前 = 2（真循环 3 轮各 1 次 + 守卫为假那轮又 1 次），正确 = 1。
//
// 判据的纠缠（诚实声明）：这条同时依赖 N-LOOP-1（同迭代写后读实时）——
// 陈旧实现即使在真循环里也会因为读到 i==3 而绕开。两者都成立才得到 1：
//   - 陈旧读 + 不门控：假轮读到 3 ⇒ 1（碰巧对）
//   - 实时读 + 不门控：真轮 1 次 + 假轮 1 次 ⇒ 2（红）
//   - 实时读 + 门控：1（绿）
// 所以它是「门控」的判别位，但只在 N-LOOP-1 已成立之后；单独锁门控的纯判据
// 需要可观察的副作用（当前语言无 I/O 原语），此边界如实记录。

import "testing"

const whileGuardGatesBody = `@language nodecoda/1
@mode workflow

function main() -> int {
    int i;
    int out;
    i = 0;
    out = 0;
    while (i < 3) limit 10 {
        i = i + 1;
        if (i == 3) {
            out = out + 1;
        }
    }
    return out;
}
`

func TestWhileGuardGatesWholeBody(t *testing.T) {
	runConstructDiff(t, "while_guard_gates_body", whileGuardGatesBody, []diffCase{
		{"恰好三轮、每轮一次：守卫为假那轮不得再执行 if 臂", map[string]any{}, int64(1)},
	})
}
