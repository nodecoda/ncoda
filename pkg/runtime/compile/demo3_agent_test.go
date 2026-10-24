package compile

// demo3_agent_test.go — M2.3 实录:start → agent → end 的执行过程。
// 展示 agent 节点进主图后:输入装配 → Generate(内部 ReAct 图)→ 命名空间
// 化输出 → END 汇聚。与 demo2(llm)同构——agent 对主图而言就是一个
// 普通 lambda 节点。

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

type logAgentModel struct{}

func (logAgentModel) Chat(ctx context.Context, model string, msgs []nodes.Message) (string, error) {
	fmt.Printf("    └─ ModelFactory.Chat(model=%q) 收到 %d 条消息:\n", model, len(msgs))
	for _, m := range msgs {
		fmt.Printf("       [%s] %s\n", m.Role, m.Content)
	}
	return "agent 回答:你问的是《" + msgs[len(msgs)-1].Content + "》对吧?", nil
}

func TestDemoStartAgentEnd(t *testing.T) {
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("100002", spec.KindAgent, &spec.AgentConfig{
				Model:       "gpt-4o",
				Instruction: "你是简明的助手",
				Query:       &spec.Source{Path: []string{"Query"}, BlockID: "100001", Name: "q"},
			}),
			nodeSpec("900001", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "100002", Output: "output"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "100002"},
			{Source: "100002", Target: "900001"},
		},
	}
	norm, _ := s.Normalize()
	jb, _ := json.MarshalIndent(norm, "", "  ")
	fmt.Println("═══════ 1) spec.v1 指令文本(agent 节点) ═══════")
	fmt.Println(string(jb))

	fmt.Println("═══════ 2) 编译:agent → eino react agent(内部图) ═══════")
	fmt.Println("  · react.NewAgent(persona=instruction, model=ModelFactory 适配器)")
	fmt.Println("  · agent 内部图:[messages]→ ChatModel →(无工具调用)→ END")
	fmt.Println("  · 主图只挂一个 lambda 节点:AssembleInput → Generate → {100002:{output}}")

	fmt.Println("═══════ 3) 执行 trace ═══════")
	r := mustCompile(t, New(testRegistry(), WithDeps(nodes.Deps{Models: logAgentModel{}})), s)
	out := mustInvoke(t, r, map[string]any{"q": "eino 是什么"})
	fmt.Println("═══════ 4) 图原始输出 ═══════")
	fmt.Printf("  Eino 返回 %#v\n", out)
	fmt.Printf("  UnwrapOutput → %#v\n", UnwrapOutput(out))

	if got := UnwrapOutput(out)["output"]; got != "agent 回答:你问的是《eino 是什么》对吧?" {
		t.Fatalf("output = %v", got)
	}
}
