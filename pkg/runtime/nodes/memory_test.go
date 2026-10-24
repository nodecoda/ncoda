package nodes

// memory_test.go — M11 会话记忆:Memory 存储、窗口裁剪、fc/react agent
// 跨轮注入与追加。

import (
	"context"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// ── windowed / InMemoryMemory ────────────────────────────────────────────

func TestWindowed(t *testing.T) {
	mk := func(n int) []Message {
		out := make([]Message, n)
		for i := range out {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			out[i] = Message{Role: role, Content: string(rune('a' + i))}
		}
		return out
	}
	cases := []struct {
		name   string
		n, win int
		want   int
	}{
		{"unlimited", 10, 0, 10},
		{"negative-unlimited", 10, -1, 10},
		{"under-window", 5, 10, 5},
		{"exact", 10, 5, 10},
		{"trim", 20, 5, 10},
		{"single-pair", 2, 1, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := mk(c.n)
			got := windowed(h, c.win)
			if len(got) != c.want {
				t.Fatalf("windowed(%d, %d) = %d msgs, want %d", c.n, c.win, len(got), c.want)
			}
			// 保留最近 N 条
			if c.want < c.n {
				for i := range got {
					if got[i].Content != h[c.n-c.want+i].Content {
						t.Fatalf("windowed did not keep most recent tail")
					}
				}
			}
		})
	}
}

func TestInMemoryMemory(t *testing.T) {
	m := NewInMemoryMemory()
	ctx := context.Background()
	h, err := m.History(ctx, "s1")
	if err != nil || len(h) != 0 {
		t.Fatalf("empty history = %v, %v", h, err)
	}
	if err := m.Append(ctx, "s1", []Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(ctx, "s1", []Message{{Role: "assistant", Content: "yo"}}); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(ctx, "s2", []Message{{Role: "user", Content: "other"}}); err != nil {
		t.Fatal(err)
	}
	h, err = m.History(ctx, "s1")
	if err != nil || len(h) != 2 || h[0].Content != "hi" || h[1].Content != "yo" {
		t.Fatalf("s1 history = %v, %v", h, err)
	}
	h, _ = m.History(ctx, "s2")
	if len(h) != 1 || h[0].Content != "other" {
		t.Fatalf("s2 history = %v", h)
	}
}

// ── fc agent 跨轮记忆 ────────────────────────────────────────────────────

func TestAgentMemoryCrossTurn(t *testing.T) {
	fake := &fakeToolModel{results: []ChatResult{{Content: "first-answer"}, {Content: "second-answer"}}}
	mem := NewInMemoryMemory()
	n := buildDeps(t, spec.KindAgent, &spec.AgentConfig{
		Model: "m", Instruction: "persona", Memory: true,
		Query: &spec.Source{Path: []string{"Query"}, Name: "q"},
	}, Deps{Models: fake, Memory: mem})

	// 第一轮:无历史。
	if _, err := n.Invoke(t.Context(), map[string]any{"Query": "q1", "ConversationID": "c1"}); err != nil {
		t.Fatal(err)
	}
	// 第二轮:历史注入到 persona 之后、当前 query 之前。
	if _, err := n.Invoke(t.Context(), map[string]any{"Query": "q2", "ConversationID": "c1"}); err != nil {
		t.Fatal(err)
	}
	got := fake.got[1].msgs
	if len(got) != 4 {
		t.Fatalf("round2 msgs = %d, want 4 (system+history[2]+user): %v", len(got), got)
	}
	if got[0].Role != "system" || got[0].Content != "persona" {
		t.Fatalf("round2[0] = %+v, want persona", got[0])
	}
	if got[1].Role != "user" || got[1].Content != "q1" {
		t.Fatalf("round2[1] = %+v, want q1 history", got[1])
	}
	if got[2].Role != "assistant" || got[2].Content != "first-answer" {
		t.Fatalf("round2[2] = %+v, want first-answer history", got[2])
	}
	if got[3].Role != "user" || got[3].Content != "q2" {
		t.Fatalf("round2[3] = %+v, want current query", got[3])
	}
	// 存储里有两轮交换。
	h, err := mem.History(t.Context(), "c1")
	if err != nil || len(h) != 4 {
		t.Fatalf("stored history = %v, %v (want 4)", h, err)
	}
}

func TestAgentMemoryIsolationByConv(t *testing.T) {
	fake := &fakeToolModel{results: []ChatResult{{Content: "a"}, {Content: "b"}, {Content: "c"}}}
	mem := NewInMemoryMemory()
	n := buildDeps(t, spec.KindAgent, &spec.AgentConfig{
		Model: "m", Memory: true, Query: &spec.Source{Path: []string{"Query"}, Name: "q"},
	}, Deps{Models: fake, Memory: mem})
	_, _ = n.Invoke(t.Context(), map[string]any{"Query": "q1", "ConversationID": "c1"})
	_, _ = n.Invoke(t.Context(), map[string]any{"Query": "q1", "ConversationID": "c2"})
	// c2 首轮无历史 → 仅 system?（无 instruction）→ 1 条 user。
	got := fake.got[1].msgs
	if len(got) != 1 || got[0].Role != "user" || got[0].Content != "q1" {
		t.Fatalf("c2 first round msgs = %v, want fresh [user q1]", got)
	}
}

func TestAgentMemoryWindowTrim(t *testing.T) {
	fake := &fakeToolModel{results: []ChatResult{{Content: "a"}, {Content: "b"}, {Content: "c"}}}
	mem := NewInMemoryMemory()
	// 窗口 1 对:第三轮只见第二轮,不见第一轮。
	n := buildDeps(t, spec.KindAgent, &spec.AgentConfig{
		Model: "m", Memory: true, MemoryWindow: 1,
		Query: &spec.Source{Path: []string{"Query"}, Name: "q"},
	}, Deps{Models: fake, Memory: mem})
	_, _ = n.Invoke(t.Context(), map[string]any{"Query": "q1", "ConversationID": "c1"})
	_, _ = n.Invoke(t.Context(), map[string]any{"Query": "q2", "ConversationID": "c1"})
	_, _ = n.Invoke(t.Context(), map[string]any{"Query": "q3", "ConversationID": "c1"})
	got := fake.got[2].msgs
	if len(got) != 3 { // q2, a(second), q3
		t.Fatalf("round3 msgs = %v, want 3 (window=1 pair)", got)
	}
	if got[0].Content != "q2" || got[2].Content != "q3" {
		t.Fatalf("round3 = %v, want [q2, second-answer, q3]", got)
	}
}

func TestAgentMemoryRequiresStore(t *testing.T) {
	// memory:true 但 Deps.Memory == nil → Build 期 fail-fast。
	_, err := (Agent{}).Build(context.Background(), nodeSpecFor(&spec.AgentConfig{
		Model: "m", Memory: true,
	}), ioDeps(nil, &fakeModel{out: "x"}, nil))
	if err == nil || !strings.Contains(err.Error(), "no Memory store") {
		t.Fatalf("err = %v, want Memory store error", err)
	}
	// memory:false + nil store → 合法（v1 向后兼容）。
	if _, err := (Agent{}).Build(context.Background(), nodeSpecFor(&spec.AgentConfig{
		Model: "m",
	}), ioDeps(nil, &fakeModel{out: "x"}, nil)); err != nil {
		t.Fatalf("memory-off build: %v", err)
	}
}

// ── react 跨轮记忆 ───────────────────────────────────────────────────────

func TestReactMemoryCrossTurn(t *testing.T) {
	fake := &fakeToolModel{results: []ChatResult{{Content: "r1"}, {Content: "r2"}}}
	mem := NewInMemoryMemory()
	n, err := (Agent{}).Build(context.Background(), nodeSpecFor(&spec.AgentConfig{
		Model: "m", Strategy: "react", Instruction: "persona", Memory: true,
	}), Deps{Models: fake, Memory: mem})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.Invoke(t.Context(), map[string]any{"Query": "q1", "ConversationID": "c1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Invoke(t.Context(), map[string]any{"Query": "q2", "ConversationID": "c1"}); err != nil {
		t.Fatal(err)
	}
	// react 适配层把历史经 MessageModifier 重注入:第二轮模型应看到
	// [system persona, user q1, assistant r1, user q2]。
	got := fake.got[1].msgs
	if len(got) != 4 {
		t.Fatalf("react round2 msgs = %d, want 4: %v", len(got), got)
	}
	if got[0].Role != "system" || got[0].Content != "persona" {
		t.Fatalf("round2[0] = %+v, want persona", got[0])
	}
	if got[1].Role != "user" || got[1].Content != "q1" {
		t.Fatalf("round2[1] = %+v, want q1", got[1])
	}
	if got[2].Role != "assistant" || got[2].Content != "r1" {
		t.Fatalf("round2[2] = %+v, want r1", got[2])
	}
	if got[3].Role != "user" || got[3].Content != "q2" {
		t.Fatalf("round2[3] = %+v, want q2", got[3])
	}
	h, err := mem.History(t.Context(), "c1")
	if err != nil || len(h) != 4 {
		t.Fatalf("stored = %v, %v", h, err)
	}
}
