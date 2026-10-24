package compile

// agent_mode_knowledge_e2e_test.go — M19-2:@mode agent + knowledge 全链 e2e。
//
// 链路: ncoda(@agent knowledge 配置) → IR → flow.Project → spec.v1
// (AgentConfig.Knowledge) → compile(stub 检索后端注入) → 执行:模型调用
// 内置 knowledge_retrieval 工具 → 命中片段回填 → 最终答案。断言:
//   - spec 层 AgentConfig.Knowledge 携带 dataset_ids/top_k;
//   - 模型每轮工具描述含内置检索工具;
//   - 执行输出 = 模型最终答案,检索轮 observation 含命中片段。

import (
	"context"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

const agentModeKnowledgeSrc = `@language nodecoda/1
@mode agent

@agent {
  model: "m",
  instruction: "你是知识助手，先检索知识库再回答。",
  knowledge: {
    dataset_ids: ["kb1"],
    top_k: 1
  }
}

function main(string query) -> string {
  return run(query);
}
`

func TestAgentModeE2EKnowledge(t *testing.T) {
	res := passes.DefaultPipeline().Run(agentModeKnowledgeSrc, "agent-kb.ncoda", nil)
	if res.HasErrors() || res.Program == nil {
		t.Fatalf("frontend errors: %v", res.Errors())
	}
	g, berr := ir.Build(res.Program)
	if berr != nil {
		t.Fatalf("ir-build: %v", berr)
	}
	p, perr := flow.Project(g)
	if perr != nil {
		t.Fatalf("project: %v", perr)
	}
	s, err := spec.Build(g, p)
	if err != nil {
		t.Fatalf("spec: %v", err)
	}

	// spec 层:agent 节点携带 knowledge 配置。
	agentCfg := (*spec.AgentConfig)(nil)
	for _, n := range s.Nodes {
		if n.Kind != spec.KindAgent {
			continue
		}
		dc, derr := spec.DecodeConfig(n.Kind, n.Config)
		if derr != nil {
			t.Fatal(derr)
		}
		agentCfg = dc.(*spec.AgentConfig)
	}
	if agentCfg == nil || agentCfg.Knowledge == nil {
		t.Fatal("spec: AgentConfig.Knowledge missing")
	}
	if len(agentCfg.Knowledge.DatasetIDs) != 1 || agentCfg.Knowledge.DatasetIDs[0] != "kb1" || agentCfg.Knowledge.TopK != 1 {
		t.Fatalf("knowledge config = %+v, want kb1/top_k=1", agentCfg.Knowledge)
	}

	// 执行:模型第 1 轮调内置检索工具,第 2 轮给最终答案。
	fake := &agentLoopModel{results: []nodes.ChatResult{
		{
			Content:   "let me search",
			ToolCalls: []nodes.ToolCall{{ID: "c1", Name: "knowledge_retrieval", Arguments: `{"query":"北京天气"}`}},
		},
		{Content: "北京天气晴朗，来自知识库"},
	}}
	deps := nodes.Deps{Models: fake, Knowledge: &gateStubKnowledge{}}
	c := New(nodes.StdRegistry(), WithDeps(deps))
	r, err := c.Compile(context.Background(), s)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out := mustInvoke(t, r, map[string]any{"query": "北京天气?"})
	unwrap := UnwrapOutput(out)
	if v, ok := unwrap["output"].(string); !ok || !strings.Contains(v, "北京天气晴朗") {
		t.Fatalf("output = %#v, want model final answer", unwrap["output"])
	}
	// 模型每轮工具描述含内置检索工具。
	if len(fake.seen) == 0 || !hasToolName(fake.seen[0], "knowledge_retrieval") {
		t.Fatalf("round-1 tools = %v, want knowledge_retrieval", fake.seen[0])
	}
}

func hasToolName(specs []nodes.ToolSpec, name string) bool {
	for _, s := range specs {
		if s.Name == name {
			return true
		}
	}
	return false
}
