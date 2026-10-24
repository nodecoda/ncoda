package nodes

// agent_invalidargs_test.go — B 测试治理:agent 工具调用参数校验回填。
//
// 契约（对齐参考目标 fc_agent_runner）:模型返回非 JSON 工具参数时,工具执行
// 路径必须把错误回填为 observation（"invalid arguments: ..."）,绝不崩溃;
// 循环继续,模型下一轮可给出最终答案。本文件锁定该回填路径 —— 此前
// execTool 的无效参数分支无直接测试。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// TestAgentInvalidToolArgs回填:模型返回非 JSON Arguments → observation
// 携带 invalid arguments → 下一轮给最终答案。
func TestAgentInvalidToolArgs(t *testing.T) {
	tr := &fakeAgentToolReg{names: []string{"math/sum"}, out: 3}
	fake := &fakeToolModel{results: []ChatResult{
		{
			Content:   "let me compute",
			ToolCalls: []ToolCall{{ID: "c1", Name: "sum", Arguments: "not-json{{{"}},
		},
		{Content: "answer is 3"},
	}}
	n := buildDeps(t, spec.KindAgent, &spec.AgentConfig{
		Model: "m",
		Tools: []spec.AgentToolWire{{Alias: "sum", ProviderName: "math", ToolName: "sum"}},
	}, ioDeps(nil, fake, tr))

	var steps []AgentStep
	an := n.(*agentNode)
	an.step = func(st AgentStep) { steps = append(steps, st) }

	out, err := n.Invoke(t.Context(), map[string]any{"Query": "3+3?"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	// 最终答案 = 各轮 content 拼接（对齐参考目标 final_answer 累加）:thought 轮
	// 文本 + 最终答案轮文本。
	if !strings.HasSuffix(out["output"].(string), "answer is 3") {
		t.Fatalf("output = %v, want suffix 'answer is 3'", out["output"])
	}
	// 工具调用轮的 observation 必须包含 invalid arguments 回填。
	found := false
	for _, st := range steps {
		if strings.Contains(st.Observation, "invalid arguments") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no invalid-arguments observation in steps: %+v", steps)
	}
}
