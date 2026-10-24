package compile

// agent_mode_e2e_test.go — @mode agent 顶层形态端到端。
//
// 链路: ncoda 源码(@mode agent + @agent 块 + run()) → frontend → IR →
// flow.Project → spec.v1 → compile(最小图 start→agent→end) → eino 执行。
// 验证: 最小图结构、entry 输入绑定 query、agent 输出 = 最终答案、
// thought 可观测。

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

const agentModeE2ESrc = `@language nodecoda/1
@mode agent

@agent {
  model: "m",
  instruction: "你是天气助手",
  max_iteration: 10
}

function main(string query) -> string {
  return run(query);
}
`

func TestAgentModeE2EMinimalGraph(t *testing.T) {
	res := passes.DefaultPipeline().Run(agentModeE2ESrc, "agent-mode.ncoda", nil)
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
		t.Fatalf("spec build: %v", err)
	}

	// 最小图: start + agent + end（3 节点）。
	if len(s.Nodes) != 3 {
		t.Fatalf("spec nodes = %d, want 3 (start/agent/end): %+v", len(s.Nodes), s.Nodes)
	}
	var agentFound bool
	for _, n := range s.Nodes {
		if n.Kind != spec.KindAgent {
			continue
		}
		agentFound = true
		cfg, err := spec.DecodeConfig(n.Kind, n.Config)
		if err != nil {
			t.Fatal(err)
		}
		ac := cfg.(*spec.AgentConfig)
		if ac.Model != "m" || ac.Instruction != "你是天气助手" || ac.MaxIteration != 10 {
			t.Fatalf("agent config = %+v", ac)
		}
		if ac.Query == nil || ac.Query.Name != "query" {
			t.Fatalf("agent query = %+v, want start.query", ac.Query)
		}
	}
	if !agentFound {
		t.Fatal("no agent node in spec")
	}

	// 执行: 无工具调用模型,一轮出最终答案。
	fake := &agentLoopModel{results: nil} // 默认返回 "done"
	deps := nodes.Deps{Models: fake}
	c := New(nodes.StdRegistry(), WithDeps(deps))
	r, err := c.Compile(context.Background(), s)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out := mustInvoke(t, r, map[string]any{"query": "北京天气?"})
	unwrap := UnwrapOutput(out)
	if v, ok := unwrap["output"].(string); !ok || !strings.Contains(v, "done") {
		t.Fatalf("output = %#v, want final answer from model", unwrap["output"])
	}
	// agent 输入收到 query（第 1 轮消息里带用户 query）。
	if len(fake.seen) != 1 {
		t.Fatalf("agent rounds = %d, want 1", len(fake.seen))
	}
}

// memoryModel 是记忆 e2e 用的可观察模型:记录每轮收到的消息并返回
// 固定答案（答案含轮次,便于断言历史注入）。
type memoryModel struct {
	round int
	seen  [][]nodes.Message
}

func (m *memoryModel) Chat(ctx context.Context, model string, msgs []nodes.Message) (string, error) {
	m.seen = append(m.seen, append([]nodes.Message{}, msgs...))
	m.round++
	return "answer", nil
}

func TestAgentModeE2EMemory(t *testing.T) {
	res := passes.DefaultPipeline().Run(agentModeE2ESrcMemory, "agent-memory.ncoda", nil)
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
		t.Fatalf("spec build: %v", err)
	}

	var agentFound bool
	for _, n := range s.Nodes {
		if n.Kind != spec.KindAgent {
			continue
		}
		agentFound = true
		cfg, err := spec.DecodeConfig(n.Kind, n.Config)
		if err != nil {
			t.Fatal(err)
		}
		ac := cfg.(*spec.AgentConfig)
		if !ac.Memory || ac.MemoryWindow != 6 {
			t.Fatalf("agent memory config = %+v, want memory=true window=6", ac)
		}
	}
	if !agentFound {
		t.Fatal("no agent node")
	}

	fake := &memoryModel{}
	mem := nodes.NewInMemoryMemory()
	c := New(nodes.StdRegistry(), WithDeps(nodes.Deps{Models: fake, Memory: mem}))
	r, err := c.Compile(context.Background(), s)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	// 第一轮 conv-a:无历史 → [system, user]。
	if _, err := r.Invoke(context.Background(), map[string]any{"query": "q1", "conversation_id": "conv-a"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.seen) != 1 || len(fake.seen[0]) != 2 {
		t.Fatalf("round1 msgs = %v, want [system user]", fake.seen)
	}
	// 第二轮 conv-a:注入第一轮交换 → [system, user q1, assistant answer, user q2]。
	if _, err := r.Invoke(context.Background(), map[string]any{"query": "q2", "conversation_id": "conv-a"}); err != nil {
		t.Fatal(err)
	}
	got := fake.seen[1]
	if len(got) != 4 {
		t.Fatalf("round2 msgs = %v, want 4 (history injected)", got)
	}
	if got[1].Content != "q1" || got[2].Content != "answer" || got[3].Content != "q2" {
		t.Fatalf("round2 = %v, want [q1, answer, q2] tail", got)
	}
	// 另一会话首轮无历史（隔离）。
	if _, err := r.Invoke(context.Background(), map[string]any{"query": "x", "conversation_id": "conv-b"}); err != nil {
		t.Fatal(err)
	}
	gotB := fake.seen[2]
	if len(gotB) != 2 {
		t.Fatalf("conv-b round1 msgs = %v, want [system user]", gotB)
	}
}

const agentModeE2ESrcMemory = `@language nodecoda/1
@mode agent

@agent {
  model: "m",
  instruction: "你是记忆助手",
  memory: true,
  memory_window: 6
}

function main(string query) -> string {
  return run(query);
}
`
