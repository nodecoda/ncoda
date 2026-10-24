package nodes

// code_timeout_budget_contract_test.go —— 预算**选择**与诊断**自报**的判据本体
// （`codeTimeoutMessage` / `declaredCodeTimeout` 的一处定义）。
//
// 为什么这两条必须有判据（而不是只靠上面的 e2e）：
//   · `declaredCodeTimeout` 决定「作者没声明时**不许发明**超时」——发明一个默认值
//     与静默丢声明同罪（本仓「失配 ≠ 静默丢弃」/「不发明无先例拼写」同源）；
//   · `codeTimeoutMessage` 的 stub 分支决定**分诊能不能把账算对人**：平台声明的
//     超时（语料值域 60s–600s）与「我们自加的安全网」是两件事，标记丢了就会把
//     桩的产物记成平台的账（假信号比不判更坏）。
//
// 真实 30s 安全网那一支不在这里跑（会拖垮秒级面）：它由 `stubBudget` 的推导保证
// ——「ctx 无截止 ⇒ 预算是我们自加的」是**一行的等价式**（见 pyrun.go / jrun.go），
// 上面 e2e 的格 ③（有声明的路径）与这里的纯函数锁合起来覆盖两端。

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// 格 ④a：stub 分支必须带**一处定义**的标记；非 stub 分支不得带。
func TestCodeTimeoutMessageDistinguishesStubBudget(t *testing.T) {
	stub := codeTimeoutMessage("python", 30*time.Second, true)
	if !strings.Contains(stub, StubBudgetTimeoutMarker) {
		t.Fatalf("stub 分支必须自报（标记 = StubBudgetTimeoutMarker）: %q", stub)
	}
	if !strings.Contains(stub, "no timeout_ms") {
		t.Fatalf("stub 分支要说清原因（节点没声明 timeout_ms）: %q", stub)
	}
	for _, lang := range []string{"python", "javascript"} {
		declared := codeTimeoutMessage(lang, 300*time.Second, false)
		if strings.Contains(declared, StubBudgetTimeoutMarker) {
			t.Fatalf("%s 的**声明**预算分支不得带桩标记（会把平台的账记成桩的）: %q", lang, declared)
		}
	}
}

// 格 ④b：诊断里的时长必须与人脸一致（声明 `timeout(1s)` 就得印 `1s`，
// 不是 `999.987046ms` —— 纳秒残差是 `time.Until` 的产物，不是语义差异）。
func TestCodeTimeoutMessageRoundsToHumanUnit(t *testing.T) {
	got := codeTimeoutMessage("python", time.Second-12954*time.Nanosecond, false)
	if !strings.Contains(got, "1s") {
		t.Fatalf("时长未归一到人类单位: %q", got)
	}
}

// 格 ④c：不发明预算 —— nil / 缺省 / 显式 0 都必须读成「没有声明」。
func TestDeclaredCodeTimeoutDoesNotInvent(t *testing.T) {
	if d := declaredCodeTimeout(nil); d != 0 {
		t.Fatalf("nil config ⇒ %v, want 0（不得发明）", d)
	}
	if d := declaredCodeTimeout(&spec.CodeConfig{}); d != 0 {
		t.Fatalf("Exception 缺省 ⇒ %v, want 0（不得发明）", d)
	}
	if d := declaredCodeTimeout(&spec.CodeConfig{Exception: &spec.ExceptionConfig{TimeoutMS: 0}}); d != 0 {
		t.Fatalf("显式 0 ⇒ %v, want 0（平台语义 = 不超时）", d)
	}
	if d := declaredCodeTimeout(&spec.CodeConfig{Exception: &spec.ExceptionConfig{TimeoutMS: 180000}}); d != 180*time.Second {
		t.Fatalf("180000ms ⇒ %v, want 180s", d)
	}
}

// 格 ④d：诊断报**声明预算**，不报执行器到达时的残差（2026-10-12 修 flaky）。
//
// 撤实现（删 `codeNode.Invoke` 里的 `context.WithValue(ctx, codeBudgetKey{}, …)`
// 记录，或让 `codeTimeoutBudget` 只认 `ctx.Deadline`）⇒ RED：声明 1s 的节点在负载下
// 会印成 `998ms`，`TestCodeNodeDeclaredTimeoutIsEnforced` 时红时绿（一手 2026-10-12：
// pre-push 门禁 `go test ./...` 两度抓到 `timed out after 998ms`）。
func TestCodeTimeoutBudgetPrefersDeclaredOverDeadlineResidual(t *testing.T) {
	// 声明 1s，但 ctx 截止只剩 ~997ms（残差 3ms）⇒ 诊断必须报**声明的** 1s。
	ctx := context.WithValue(context.Background(), codeBudgetKey{}, time.Second)
	ctx, cancel := context.WithDeadline(ctx, time.Now().Add(time.Second-3*time.Millisecond))
	defer cancel()
	got, ok := codeTimeoutBudget(ctx, defaultPyRunTimeout)
	if !ok {
		t.Fatalf("有声明预算却报 ok=false")
	}
	if got != time.Second {
		t.Fatalf("诊断预算应 = 声明的 1s（不是残差），实测 %v", got)
	}
	// 无声明、仅截止 ⇒ 退到截止剩余（ok=true）；都没有 ⇒ fallback（ok=false）。
	bare, bcancel := context.WithDeadline(context.Background(), time.Now().Add(time.Second))
	defer bcancel()
	if d, ok := codeTimeoutBudget(bare, defaultPyRunTimeout); !ok || d <= 0 || d > time.Second {
		t.Fatalf("仅截止时应退到剩余（ok=true, 0<d<=1s），实测 d=%v ok=%v", d, ok)
	}
	if d, ok := codeTimeoutBudget(context.Background(), defaultPyRunTimeout); ok || d != defaultPyRunTimeout {
		t.Fatalf("无预算无截止时应退 fallback（ok=false），实测 d=%v ok=%v", d, ok)
	}
}
