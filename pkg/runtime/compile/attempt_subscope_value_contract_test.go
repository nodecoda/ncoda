package compile

// attempt_subscope_value_contract_test.go — N-RET-1 第三条（§5.8）的语言级判据：
// attempt 臂内的 `return` = **该分支的结果值**，不是流程终止。
//
// 三个相位，分开记（全绿）：
//  1. 臂返回后仍有语句：`success { return "ok"; }` 之后 `out = "after"` 必须执行
//     （臂 return 只交出分支值，流程继续）——
//     return_stratification_contract_test.go::TestAttemptArmReturnIsSubscope。
//  2. attempt 是最后一条语句 / 成功臂：臂 return 的值 = 函数结果——
//     本文件 TestAttemptArmReturnIsFlowValue。
//  3. 同形状 / 失败臂：活臂值不得被未走臂的 nil 覆盖——
//     本文件 TestAttemptArmReturnAsFlowTailFailure。
//
// 失败臂用不可达地址（127.0.0.1:1）确定性触发；成功臂用本地 httptest。

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

const attemptValueShape = `@language nodecoda/1
@mode workflow

function main() -> string {
    attempt http("GET", %q, {}) as resp {
        success { return "ok"; }
        failure(err) { return "err"; }
    }
}
`

func TestAttemptArmReturnIsFlowValue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	runConstructDiff(t, "attempt_arm_value_success", fmt.Sprintf(attemptValueShape, srv.URL), []diffCase{
		{"成功臂 return 的值 = 函数结果", map[string]any{}, "ok"},
	})
}

// TestAttemptArmReturnAsFlowTailFailure —— 失败臂相位。
//
// 修复前（2026-10-24）：实测 nil（期望 "err"）。机制：该形状没有规范出口
// 900001，两个臂端 End 都是终止 End；Eino 把**未走臂**的 End 也作为 END 数据
// 前驱投递，其 output 为 nil。UnwrapOutput 的「后写覆盖」会用它吃掉活臂的
// "err"（顺序相关）。
// 修复：UnwrapOutput 的 null 规则（N-NULL-1）—— nil 不覆盖已存在的值。
// 显式 null 且全为 nil 时结果仍是 nil。
func TestAttemptArmReturnAsFlowTailFailure(t *testing.T) {
	runConstructDiff(t, "attempt_arm_value_failure", fmt.Sprintf(attemptValueShape, "http://127.0.0.1:1/"), []diffCase{
		{"失败臂 return 的值 = 函数结果", map[string]any{}, "err"},
	})
}
