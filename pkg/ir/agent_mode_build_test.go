//@feature return-hierarchy — return 语义分层：主流程终止直连 end / 子函数值回流（build_return.go + agent_mode_build_test.go）

package ir_test

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
)

// graphHasEnd walks the root tree for a ReturnStmt carrying an End node.
func graphHasEnd(g *ir.Graph) bool {
	if g == nil || g.Root == nil {
		return false
	}
	for _, s := range g.Root.Stmts {
		if rs, ok := s.(*ir.ReturnStmt); ok && rs.End != nil {
			return true
		}
	}
	return false
}

// buildIRErrs runs the frontend pipeline and returns every diagnostic
// message (tolerating errors, unlike buildIR).
func buildIRErrs(t *testing.T, src string) []string {
	t.Helper()
	res := passes.DefaultPipeline().Run(src, "t.dc", nil)
	var msgs []string
	for _, d := range res.Diagnostics {
		msgs = append(msgs, d.Message)
	}
	if res.Program != nil {
		if _, err := ir.Build(res.Program); err != nil {
			msgs = append(msgs, err.Error())
		}
	}
	return msgs
}

// ── @mode agent: 顶层 agent 应用（workflow 语法糖） ──────────────────────

const agentModeIRSrc = `@language nodecoda/1
@mode agent

@agent {
  model: "gpt-5.6-sol",
  instruction: "你是天气助手",
  max_iteration: 10,
  tools: ["weather"]
}

function main(string query) -> string {
  return run(query);
}
`

func TestBuildAgentModeMinimalGraph(t *testing.T) {
	g := buildIR(t, agentModeIRSrc)
	agents := nodesOf(g)[ir.KindAgent]
	if len(agents) != 1 {
		t.Fatalf("agent nodes = %d, want 1", len(agents))
	}
	ac, ok := agents[0].(*ir.AgentCall)
	if !ok {
		t.Fatalf("agent node = %T, want *ir.AgentCall", agents[0])
	}
	if ac.Model != "gpt-5.6-sol" {
		t.Errorf("Model = %q", ac.Model)
	}
	if ac.Instruction != "你是天气助手" {
		t.Errorf("Instruction = %q", ac.Instruction)
	}
	if ac.MaxIteration != 10 {
		t.Errorf("MaxIteration = %d, want 10", ac.MaxIteration)
	}
	if len(ac.Tools) != 1 || ac.Tools[0].Alias != "weather" {
		t.Errorf("Tools = %+v, want [weather]", ac.Tools)
	}
	// query 绑定到 start 的 query 端口（entry 输入）。
	if ac.Query == nil || ac.Query.Name != "query" {
		t.Errorf("Query = %+v, want start.query ref", ac.Query)
	}
	// 最小图: start(graph 级) + agent + end(ReturnStmt 尾端)。
	if g.Entry == nil {
		t.Error("missing start node (graph.Entry)")
	}
	if !graphHasEnd(g) {
		t.Error("missing end node")
	}
	if len(nodesOf(g)[ir.KindAgent]) != 1 {
		t.Errorf("agent nodes = %d, want exactly 1 (minimal graph)", len(nodesOf(g)[ir.KindAgent]))
	}
}

func TestBuildAgentModeMinimalDecl(t *testing.T) {
	// 只有 model:缺省 max_iteration/tools。
	g := buildIR(t, `@language nodecoda/1
@mode agent
@agent { model: "m" }
function main(string q) -> string { return run(q); }
`)
	agents := nodesOf(g)[ir.KindAgent]
	if len(agents) != 1 {
		t.Fatalf("agent nodes = %d, want 1", len(agents))
	}
	ac := agents[0].(*ir.AgentCall)
	if ac.MaxIteration != 0 || len(ac.Tools) != 0 {
		t.Errorf("defaults: MaxIteration=%d Tools=%v, want 0/nil", ac.MaxIteration, ac.Tools)
	}
}

func TestBuildRunOutsideAgentMode(t *testing.T) {
	// run() 在 workflow 模式必须被拒绝（binder 层 UNDEFINED_VAR 先行）。
	errs := buildIRErrs(t, `@mode workflow
function main() -> string { return run("x"); }
`)
	if len(errs) == 0 {
		t.Fatal("want error for run() in workflow mode")
	}
	joined := strings.Join(errs, "; ")
	if !strings.Contains(joined, "Undefined variable") && !strings.Contains(joined, "run") {
		t.Fatalf("errs = %s, want run-related error", joined)
	}
}

func TestBuildAgentModeMemory(t *testing.T) {
	g := buildIR(t, `@language nodecoda/1
@mode agent
@agent { model: "m", instruction: "i", memory: true, memory_window: 6 }
function main(string q) -> string { return run(q); }
`)
	agents := nodesOf(g)[ir.KindAgent]
	if len(agents) != 1 {
		t.Fatalf("agent nodes = %d, want 1", len(agents))
	}
	ac := agents[0].(*ir.AgentCall)
	if !ac.Memory {
		t.Errorf("Memory = false, want true")
	}
	if ac.MemoryWindow != 6 {
		t.Errorf("MemoryWindow = %d, want 6", ac.MemoryWindow)
	}
}

func TestBuildAgentModeMemoryDefault(t *testing.T) {
	// memory 缺省 false → AgentCall.Memory=false（运行时无记忆行为）。
	g := buildIR(t, `@language nodecoda/1
@mode agent
@agent { model: "m" }
function main(string q) -> string { return run(q); }
`)
	ac := nodesOf(g)[ir.KindAgent][0].(*ir.AgentCall)
	if ac.Memory || ac.MemoryWindow != 0 {
		t.Errorf("defaults: Memory=%v MemoryWindow=%d, want false/0", ac.Memory, ac.MemoryWindow)
	}
}
