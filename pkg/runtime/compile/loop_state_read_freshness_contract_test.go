package compile

// loop_state_read_freshness_contract_test.go — N-LOOP-1 的语言级判据。
//
// 规范（语句序）：同一基本块内语句按文本顺序执行，后一条语句读到的循环状态
// 变量必须是前一条语句**刚写入**的值。这不是平台形状问题 —— 它只谈源文本
// 在本地 runtime 上的值。
//
// 为什么单独一条（不依赖 return）：return 穿透循环的 n→n+1 只是这个缺陷的
// 一个出口。若判据只挂在 return 上，修完 return 取值可能把缺陷赶到别的出口
// （条件、循环输出、嵌套 if）。这里直接问「写完再读，读到什么」。
//
// 判据的证伪面（关键：必须挑一个**不被"条件为假那轮仍跑兄弟节点"掩盖**的相位）：
//   - n=0：`i = i + 1` 之后 i 至少为 1，`if (i == 0)` **永不命中** ⇒ "none"。
//     陈旧读（读到自增前的 k）会在第一轮命中 k==0 ⇒ "hit"。这是判别位。
//   - n=1 / n=4 是相位控制：正确语义给 "hit" / "none"，防止"永远 hit"或
//     "永远 none"的假绿。
//   - 为什么不用 `if (i == 3)` + `while (i < 3)`：条件为假的那一轮仍会执行
//     读循环状态的兄弟节点（独立缺陷 N-LOOP-2，已由
//     loop_guard_gating_contract_test.go 锁住）；在 N-LOOP-2 修复前，陈旧读与
//     实时读在该轮都读到 3 ⇒ 不是判别位（2026-10-24 一手实测）。

import "testing"

const loopStateReadFreshness = `@language nodecoda/1
@mode workflow

function main(int n) -> string {
    int i;
    string out;
    i = 0;
    out = "none";
    while (i < 3) limit 10 {
        i = i + 1;
        if (i == n) {
            out = "hit";
        }
    }
    return out;
}
`

func TestLoopStateReadSeesSameIterationWrite(t *testing.T) {
	runConstructDiff(t, "loop_state_read_freshness", loopStateReadFreshness, []diffCase{
		{"判别位：自增后不得再读到 0", map[string]any{"n": int64(0)}, "none"},
		{"相位控制：第一轮写入后命中", map[string]any{"n": int64(1)}, "hit"},
		{"相位控制：永不命中", map[string]any{"n": int64(4)}, "none"},
	})
}

// loopStateSum 是 N-LOOP-1 的第二条判据：不带 if、不带 return，只问
// 「自增后再把 i 累加进 total」读到什么。陈旧读 ⇒ 0+1+2 = 3；实时读 ⇒ 1+2+3 = 6。
// 它不依赖守卫门控（累加链本身受 while 条件门控），是干净的单点判别位。
const loopStateSum = `@language nodecoda/1
@mode workflow

function main() -> int {
    int i;
    int total;
    i = 0;
    total = 0;
    while (i < 3) limit 10 {
        i = i + 1;
        total = total + i;
    }
    return total;
}
`

func TestLoopStateReadSum(t *testing.T) {
	runConstructDiff(t, "loop_state_read_sum", loopStateSum, []diffCase{
		{"每轮读到自增后的 i：1+2+3", map[string]any{}, int64(6)},
	})
}
