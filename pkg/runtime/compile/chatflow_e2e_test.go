package compile

// chatflow_e2e_test.go — M13 @mode advanced-chat 会话记忆闭环:
// conversation 节点读历史 + chatflowRunnable 写历史（跨轮追加、会话隔离、
// 缺 query 不追加、无 Memory store 降级普通 workflow）。

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

const chatflowE2ESrc = `@language nodecoda/1
@mode advanced-chat
function main(string query) {
    let h = conversation_history();
    output(h + "|" + query);
}
`

// buildChatflow lowers the advanced-chat source through the full chain and
// compiles it with the given deps.
func buildChatflow(t *testing.T, deps nodes.Deps) (Runnable, *nodes.InMemoryMemory) {
	t.Helper()
	res := passes.DefaultPipeline().Run(chatflowE2ESrc, "chatflow.ncoda", nil)
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
	if s.Mode != "advanced-chat" {
		t.Fatalf("spec.Mode = %q, want advanced-chat", s.Mode)
	}
	// conversation 节点必须在 spec 里。
	var convFound bool
	for _, n := range s.Nodes {
		if n.Kind == spec.KindConversation {
			convFound = true
		}
	}
	if !convFound {
		t.Fatal("spec has no KindConversation node")
	}
	mem := nodes.NewInMemoryMemory()
	deps.Memory = mem
	c := New(nodes.StdRegistry(), WithDeps(deps))
	r, err := c.Compile(context.Background(), s)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return r, mem
}

func TestChatflowMemoryRoundtrip(t *testing.T) {
	r, mem := buildChatflow(t, nodes.Deps{})

	// 轮 1:空历史 → "|hello";包装器追加 [user hello, assistant |hello]。
	out1 := mustInvoke(t, r, map[string]any{"query": "hello", "ConversationID": "c1"})
	if got := UnwrapOutput(out1)["output"]; got != "|hello" {
		t.Fatalf("round1 output = %#v, want |hello", got)
	}
	h, _ := mem.History(context.Background(), "c1")
	if len(h) != 2 || h[0].Content != "hello" || h[1].Content != "|hello" {
		t.Fatalf("round1 history = %+v, want [user hello, assistant |hello]", h)
	}

	// 轮 2:历史注入 → 答案含上轮完整交换。
	out2 := mustInvoke(t, r, map[string]any{"query": "world", "ConversationID": "c1"})
	want2 := "user: hello\nassistant: |hello|world"
	if got := UnwrapOutput(out2)["output"]; got != want2 {
		t.Fatalf("round2 output = %#v, want %q", UnwrapOutput(out2)["output"], want2)
	}
	h, _ = mem.History(context.Background(), "c1")
	if len(h) != 4 {
		t.Fatalf("round2 history len = %d, want 4", len(h))
	}
	if h[2].Content != "world" || h[3].Content != want2 {
		t.Fatalf("round2 appended history = %+v", h)
	}
}

func TestChatflowSessionIsolation(t *testing.T) {
	r, _ := buildChatflow(t, nodes.Deps{})
	mustInvoke(t, r, map[string]any{"query": "a", "ConversationID": "s1"})
	out := mustInvoke(t, r, map[string]any{"query": "b", "ConversationID": "s2"})
	if got := UnwrapOutput(out)["output"]; got != "|b" {
		t.Fatalf("s2 output = %#v, want |b (no cross-session leak)", got)
	}
}

func TestChatflowNoQueryNoAppend(t *testing.T) {
	r, mem := buildChatflow(t, nodes.Deps{})
	// 非对话轮:缺 query 键 → 图照常执行,但不追加记忆。
	out := mustInvoke(t, r, map[string]any{"ConversationID": "c1", "extra": 1})
	if got := UnwrapOutput(out)["output"]; got != "|" {
		t.Fatalf("no-query output = %#v, want | (empty query + empty history)", got)
	}
	h, _ := mem.History(context.Background(), "c1")
	if len(h) != 0 {
		t.Fatalf("no-query must not append, got %+v", h)
	}
}

func TestChatflowNilMemoryDegrades(t *testing.T) {
	// 无 Memory store → 不包装:仍可执行,但无记忆（第一轮后历史不再注入）。
	res := passes.DefaultPipeline().Run(chatflowE2ESrc, "chatflow.ncoda", nil)
	g, _ := ir.Build(res.Program)
	p, _ := flow.Project(g)
	s, _ := spec.Build(g, p)
	c := New(nodes.StdRegistry(), WithDeps(nodes.Deps{})) // Memory == nil
	r, err := c.Compile(context.Background(), s)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out1 := mustInvoke(t, r, map[string]any{"query": "hello"})
	out2 := mustInvoke(t, r, map[string]any{"query": "world"})
	if got := UnwrapOutput(out1)["output"]; got != "|hello" {
		t.Fatalf("out1 = %#v, want |hello", got)
	}
	if got := UnwrapOutput(out2)["output"]; got != "|world" {
		t.Fatalf("out2 = %#v, want |world (nil-memory degrade: history stays empty)", got)
	}
}

func TestChatflowNonStringQueryNoAppend(t *testing.T) {
	r, mem := buildChatflow(t, nodes.Deps{})
	out := mustInvoke(t, r, map[string]any{"query": 42, "ConversationID": "c1"})
	// 非 string query 会被模板渲染成 "42"（图正常执行）,但记忆追加必须跳过。
	if got := UnwrapOutput(out)["output"]; got != "|42" {
		t.Fatalf("int-query output = %#v, want |42", got)
	}
	h, _ := mem.History(context.Background(), "c1")
	if len(h) != 0 {
		t.Fatalf("non-string query must not append, got %+v", h)
	}
	_ = strings.TrimSpace
}
