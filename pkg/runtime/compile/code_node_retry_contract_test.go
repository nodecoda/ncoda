package compile

// code_node_retry_contract_test.go —— **节点自带 `settingOnError.retryTimes`
// 在运行期必须被消费**（2026-09-29 收 `residuals.yaml#R-149`）。
//
// 为什么单列一条判据：与超时轴**同一 stanza、同一根因**（见
// code_node_timeout_contract_test.go）—— 编译侧**一直是对的**（一手：
// `internal/backend/cozebiz/render_node.go::errorPolicySetting` 是
// `processType` / `retryTimes` 的唯一发射点，作者写 `with retry(max: N)` ⇒
// 产物 `settingOnError.retryTimes: N`），丢的只在**运行期**：llm / http / tool /
// agent 四处都在体内读 `Exception.MaxRetry`，code **唯一不读** ⇒ 产物里写着
// `retryTimes: 3`，执行时只跑一次 = 「一轴两处两种行为」。
//
// 量级一手（只读语料 322 份 yaml）：`retryTimes` 非零 **105** 处 / 6573
// （`1`×65 · `3`×35 · `2`×5）。
//
// 判据本体（读哪个字段、怎么夹负数、尝试总数怎么算）=
// `internal/runtime/nodes/code.go::declaredCodeRetries`。夹具走**真实链路**
// （源码 → 前端/IR → flow.Project → spec.Build，复用同包的 `specFromSource`），
// 不手搓 spec。
//
// **撤实现必红（2026-09-29 一手实测，五处各撤一次；每格都有它能分辨的变异）**：
//
// | 撤哪 | 红哪 |
// | --- | --- |
// | `code.go` 重试循环失效（`retries := 0`） | 格 ②（节点失败 + 计数 1） |
// | `code.go::declaredCodeRetries` 无策略也发明 3 次 | 格 ③（计数 2 且节点成功） |
// | `code.go` 截止挪成「每次尝试各起一份」（从同一基 ctx 重起） | 格 ④（计数 4） |
// | `spec/build.go` CodeRun 分支 `Exception: fromException(…)` → nil | 格 ①（+②④） |
// | `errorPolicySetting` 的 `retryTimes ← ec.MaxRetry` | **不在这里红** —— 产物面由既有判据 `internal/compiler/cozebiz_timeout_axis_contract_test.go::TestCozeBizSharedBudgetTimeoutAxisIsReported` 锁（实测撤它 ⇒ 该判据红）。**不为同一事实再造第二份判据**。 |

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// retryProbeBody 生成一段 python 体：每次执行先把「已尝试次数」落到 cfile，
// 前 failFirst 次抛错、其后返回 `{"out": v}`；sleepSec 非空则每次先睡那么久
// （格 ④ 用）。**体内不得出现反引号** —— ncoda 的 `source \`…\“ 原文块以反引号
// 定界（这也是为什么用占位符替换而不是 Sprintf：python 的 `%` 格式化不能过 Go
// 的格式化函数）。
func retryProbeBody(cfile string, failFirst int, sleepSec string) string {
	body := "import os\n"
	if sleepSec != "" {
		body += "import time\n"
	}
	body += `def main(args):
    p = "__COUNTER__"
    n = 0
    if os.path.exists(p):
        n = int(open(p).read() or "0")
    open(p, "w").write(str(n + 1))
`
	if failFirst >= 0 {
		body += "    if n < __FAILFIRST__:\n        raise ValueError(\"probe attempt %d fails\" % (n + 1))\n"
	}
	if sleepSec != "" {
		body += "    time.sleep(__SLEEP__)\n"
	}
	body += "    return {\"out\": args.params[\"v\"]}\n"
	body = strings.ReplaceAll(body, "__COUNTER__", cfile)
	body = strings.ReplaceAll(body, "__FAILFIRST__", strconv.Itoa(failFirst))
	body = strings.ReplaceAll(body, "__SLEEP__", sleepSec)
	return body
}

// retryProbeSrc 把 python 体包进最小 ncoda 源；policy 为空串 = 作者没写策略
// （格 ③ 的对照），否则是挂在调用点上的 ` with retry(...)` / ` with timeout(...)`。
func retryProbeSrc(body, policy string) string {
	return timeoutProbeHead + `function main(string x) -> string {
    let c = foreign code python3(string v = x) -> { out: string; } { source ` + "`" + body + "`" + `; }` + policy + `;
    return c.out;
}
`
}

// attempts 读探针写的尝试次数（文件不存在 = 一次都没跑起来，按 0 计）。
func attempts(t *testing.T, cfile string) int {
	t.Helper()
	b, err := os.ReadFile(cfile)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("探针计数文件内容不是数字：%q (%v)", string(b), err)
	}
	return n
}

func requirePython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 not available: %v", err)
	}
}

// 格 ① IR→spec 传输面：作者写的 `with retry(max: 3)` 必须出现在 spec 的
// CodeConfig 里。撤实现必红（实测）= `internal/runtime/spec/build.go` 的 CodeRun
// 分支把 `Exception: fromException(v.Exception)` 改成 nil。
//
// **产物面那半不在这里锁**（避免同一事实两处判据）：`settingOnError.retryTimes`
// 真的写进 yaml 这件事，既有判据
// `internal/compiler/cozebiz_timeout_axis_contract_test.go::TestCozeBizSharedBudgetTimeoutAxisIsReported`
// 已经锁住（撤发射点 ⇒ 它红，一手实测）。
func TestCodeNodeDeclaredRetryReachesRuntimeSpec(t *testing.T) {
	cfile := filepath.Join(t.TempDir(), "n")
	s := specFromSource(t, retryProbeSrc(retryProbeBody(cfile, 0, ""), " with retry(max: 3)"))
	cc := codeCfg(t, s)
	if cc.Exception == nil {
		t.Fatalf("CodeConfig.Exception = nil：IR→spec 这一跳把节点声明丢了")
	}
	if cc.Exception.MaxRetry != 3 {
		t.Fatalf("CodeConfig.Exception.MaxRetry = %d, want 3（作者写了 with retry(max: 3)）",
			cc.Exception.MaxRetry)
	}
}

// 格 ② 消费面（端到端）：声明的重试必须**真的生效** —— 前 1 次失败、第 2 次成功
// ⇒ `with retry(max: 2)` 下节点成功，且 python 体确实被跑了 **2** 次。
// 撤实现必红 = 去掉 `code.go::Invoke` 的重试循环（把 `retries` 恒当 0）⇒
// 节点失败且计数 = 1。
func TestCodeNodeRetryIsConsumedEndToEnd(t *testing.T) {
	requirePython(t)
	cfile := filepath.Join(t.TempDir(), "n")
	s := specFromSource(t, retryProbeSrc(retryProbeBody(cfile, 1, ""), " with retry(max: 2)"))
	r := mustCompile(t, New(testRegistry()), s)
	res, err := r.Invoke(context.Background(), map[string]any{"x": "v"})
	if err != nil {
		t.Fatalf("声明了 retry(max: 2)、第 2 次本会成功，却失败：%v", err)
	}
	if len(res) == 0 {
		t.Fatal("节点成功但工作流没有出口值：成功没有被传播出去")
	}
	// 「第 2 次成功」的**可判定**形式 = 计数 2 + 整体无错：若第 2 次的值没被
	// 采纳、或错误被吞，这两条至少红一条。**不**去挖出口值的物理键名 ——
	// 本仓不变量「产物 id 不是契约」，测试里出现物理节点号会被 gov 的
	// `id_echo` 判据点名（值面的通路由既有 code 节点判据覆盖）。
	if n := attempts(t, cfile); n != 2 {
		t.Fatalf("尝试次数 = %d, want 2（第 1 次失败 + 第 2 次成功）", n)
	}
}

// 格 ③ 不发明：作者**没写** retry ⇒ 只跑一次（不得被我们补一个默认重试；
// 平台语义「缺省 = 不重试」）。同时锁住**不吞错**：唯一那次失败必须报出来。
func TestCodeNodeWithoutRetryPolicyRunsExactlyOnce(t *testing.T) {
	requirePython(t)
	cfile := filepath.Join(t.TempDir(), "n")
	s := specFromSource(t, retryProbeSrc(retryProbeBody(cfile, 1, ""), ""))
	r := mustCompile(t, New(testRegistry()), s)
	if _, err := r.Invoke(context.Background(), map[string]any{"x": "v"}); err == nil {
		t.Fatal("没写重试却把「前 1 次必失败」的节点跑成功了：错误被吞了")
	}
	if n := attempts(t, cfile); n != 1 {
		t.Fatalf("尝试次数 = %d, want 1（无策略 ⇒ 不重试）", n)
	}
}

// 格 ④ 总预算**跨尝试共享**（一手 coze-studio OSS `vo/canvas.go` 的
// SettingOnError.TimeoutMs 注释：「If any retry happens, ALL retry attempts
// accumulates to the same timeout threshold」）：预算耗尽后**不再起新尝试**
// —— 每次尝试各起一份新预算 = 把 1s 放大成 (1+retries)×1s（作者没要的东西）。
// 撤实现必红 = 把截止下发挪进重试循环内（每次尝试各起一份）⇒ 计数 = 4 而不是 1。
// 预算粒度受语言约束（`timeout` 只收**正整数秒**，`OPERATION_POLICY`）⇒ 预算
// 1s、体内睡 2s（1s 就被掐，故正确路径耗时 ≈1s）。
func TestCodeNodeRetrySharesOneTimeoutBudget(t *testing.T) {
	requirePython(t)
	cfile := filepath.Join(t.TempDir(), "n")
	body := retryProbeBody(cfile, -1, "2")
	s := specFromSource(t, retryProbeSrc(body, " with timeout(1s), retry(max: 3)"))
	r := mustCompile(t, New(testRegistry()), s)
	if _, err := r.Invoke(context.Background(), map[string]any{"x": "v"}); err == nil {
		t.Fatal("1s 预算下的 2s sleep 却成功返回：预算没生效")
	}
	if n := attempts(t, cfile); n != 1 {
		t.Fatalf("尝试次数 = %d, want 1（总预算耗尽 ⇒ 不再重试）", n)
	}
}
