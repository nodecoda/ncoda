package compile

// code_node_timeout_contract_test.go —— **节点级 `settingOnError.timeoutMs`
// 在运行期必须被消费**（2026-09-29 修掉的缺陷）。
//
// 为什么单列一条判据：编译侧**一直是对的**（一手：作者写 `with timeout(300s)`
// ⇒ 产物 `settingOnError.timeoutMs: 300000`），丢的只在 **IR→spec→执行** 这一跳
// —— `CodeConfig` 不带 `Exception`、`codeNode` 不读它，于是子进程执行器用固定
// 30s 常量替掉平台声明的值。平台值域一手（语料 672 源）：`timeoutMs` **6622 处**
// （180000 / 60000 / 600000 / 300000 / 360000 / 120000 / 10000）。⇒ 平台允许跑
// 3 分钟的节点，我们 30 秒就掐。
//
// 同一事实的其他四处（llm / http / tool / agent）各自在体内读 `Exception.TimeoutMS`
// ⇒ 本判据锁的是「code 与它们同解」。判据本体（预算选择 + 诊断拼写）=
// `internal/runtime/nodes/code.go::declaredCodeTimeout / codeTimeoutMessage`。

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

const timeoutProbeHead = "@language nodecoda/1\n@mode workflow\n"

// codeSrcWithTimeout 造一份含**用户** foreign code 节点的最小源。
// policy 为空串 = 作者没写调用点策略（格 ② 的对照）。
func codeSrcWithTimeout(policy string) string {
	return timeoutProbeHead + `function main(string x) -> string {
    let c = foreign code python3(string v = x) -> { out: string; } { source ` + "`" + `def main(args):
    return {"out": args.params["v"]}
` + "`" + `; }` + policy + `;
    return c.out;
}
`
}

// specFromSource 走**真实链路**：源码 → 前端+IR 管线 → ir.Build → flow.Project →
// spec.Build（与 `nclang-compile` / `rt-l3` 同源，不手搓 spec）。
func specFromSource(t *testing.T, src string) *spec.Spec {
	t.Helper()
	res := passes.DefaultPipeline().Run(src, "code_timeout_contract.ncoda", nil)
	if res.HasErrors() || res.Program == nil {
		t.Fatalf("frontend: %v", res.Errors())
	}
	g, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("ir-build: %v", err)
	}
	p, err := flow.Project(g)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	s, err := spec.Build(g, p)
	if err != nil {
		t.Fatalf("spec-build: %v", err)
	}
	return s
}

// codeCfg 取 spec 里唯一的 code 节点配置（找不到 = 夹具坏了，必须红）。
func codeCfg(t *testing.T, s *spec.Spec) *spec.CodeConfig {
	t.Helper()
	for i := range s.Nodes {
		if s.Nodes[i].Kind != spec.KindCode {
			continue
		}
		var cc spec.CodeConfig
		if err := json.Unmarshal(s.Nodes[i].Config, &cc); err != nil {
			t.Fatalf("decode CodeConfig: %v", err)
		}
		return &cc
	}
	t.Fatal("夹具里没有 code 节点")
	return nil
}

// 格 ① 传输面：作者写的 `with timeout(300s)` 必须出现在 spec 的 CodeConfig 里。
// 撤实现必红 = 去掉 `internal/runtime/spec/build.go` CodeRun 分支的 `Exception:`。
func TestCodeNodeDeclaredTimeoutReachesRuntimeSpec(t *testing.T) {
	cc := codeCfg(t, specFromSource(t, codeSrcWithTimeout(" with timeout(300s)")))
	if cc.Exception == nil {
		t.Fatalf("CodeConfig.Exception = nil：IR→spec 这一跳把节点声明丢了（平台 6622 处 timeoutMs 都会这样丢）")
	}
	if cc.Exception.TimeoutMS != 300000 {
		t.Fatalf("CodeConfig.Exception.TimeoutMS = %d, want 300000", cc.Exception.TimeoutMS)
	}
}

// 格 ② 不发明：作者**没写**策略 ⇒ 不得被我们补一个默认超时（平台语义
// 「缺省/0 = 不超时」；发明一个值同样是「无痕发明」）。
func TestCodeNodeWithoutPolicyDoesNotInventTimeout(t *testing.T) {
	cc := codeCfg(t, specFromSource(t, codeSrcWithTimeout("")))
	if cc.Exception != nil && cc.Exception.TimeoutMS != 0 {
		t.Fatalf("作者没写策略，CodeConfig.Exception.TimeoutMS = %d, want 0/缺省",
			cc.Exception.TimeoutMS)
	}
}

// 格 ③ 消费面（端到端）：声明的 1s 必须**真的生效**——
// 用户代码睡 3s ⇒ 1s 就 RCodeTimeout，且诊断报的是**声明的那个预算**
// （不是我们的 30s 安全网，也不得带桩自加标记）。
// 撤实现必红 = 去掉 `code.go::Invoke` 里的 `WithTimeout` 下发（睡满 2s 反而成功），
// 或去掉 `spec/build.go` 的 `Exception:`（退回 30s 安全网）。
func TestCodeNodeDeclaredTimeoutIsEnforced(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 not available: %v", err)
	}
	src := timeoutProbeHead + `function main(string x) -> string {
    let c = foreign code python3(string v = x) -> { out: string; } { source ` + "`" + `import time
def main(args):
    time.sleep(3)
    return {"out": args.params["v"]}
` + "`" + `; } with timeout(1s);
    return c.out;
}
`
	r := mustCompile(t, New(testRegistry()), specFromSource(t, src))
	_, err := r.Invoke(context.Background(), map[string]any{"x": "v"})
	if err == nil {
		t.Fatal("声明 1s 未生效：睡了 3s 却成功返回（预算被安全网顶掉 = 本次修的缺陷）")
	}
	msg := err.Error()
	if !strings.Contains(msg, "1s") {
		t.Fatalf("诊断未报**声明的**预算：%s", msg)
	}
	if strings.Contains(msg, nodes.StubBudgetTimeoutMarker) {
		t.Fatalf("声明了预算却报成「桩自加的安全网」⇒ 分诊必错位：%s", msg)
	}
}
