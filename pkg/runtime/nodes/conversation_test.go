package nodes

// conversation_test.go — M13 advanced-chat 会话历史读取节点:
// 渲染格式、空历史/无 store 宽容降级、会话隔离。

import (
	"context"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func TestRenderHistory(t *testing.T) {
	cases := []struct {
		name string
		in   []Message
		want string
	}{
		{"empty", nil, ""},
		{"single-user", []Message{{Role: "user", Content: "hi"}}, "user: hi"},
		{"pair", []Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "yo"}},
			"user: hi\nassistant: yo"},
		{"tool-skipped", []Message{{Role: "user", Content: "q"}, {Role: "tool", Content: "r1"}, {Role: "assistant", Content: "a"}},
			"user: q\nassistant: a"},
		{"trailing-newline-trimmed", []Message{{Role: "user", Content: "x"}, {Role: "assistant", Content: "y"}},
			"user: x\nassistant: y"},
		{"system-skipped", []Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "u"}},
			"user: u"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := renderHistory(c.in); got != c.want {
				t.Fatalf("renderHistory = %q, want %q", got, c.want)
			}
		})
	}
}

func TestConversationNodeInvoke(t *testing.T) {
	ctx := context.Background()
	build := func(mem Memory) (NodeImpl, error) {
		return (Conversation{}).Build(ctx, &spec.NodeSpec{Kind: spec.KindConversation, Config: nil}, Deps{Memory: mem})
	}

	t.Run("renders-session-history", func(t *testing.T) {
		mem := NewInMemoryMemory()
		if err := mem.Append(ctx, "s1", []Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "yo"},
		}); err != nil {
			t.Fatal(err)
		}
		n, err := build(mem)
		if err != nil {
			t.Fatal(err)
		}
		out, err := n.Invoke(ctx, map[string]any{"ConversationID": "s1"})
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := out["history"].(string); !ok || got != "user: hi\nassistant: yo" {
			t.Fatalf("history = %#v, want rendered pair", out["history"])
		}
	})

	t.Run("default-session", func(t *testing.T) {
		mem := NewInMemoryMemory()
		if err := mem.Append(ctx, "default", []Message{{Role: "user", Content: "d"}}); err != nil {
			t.Fatal(err)
		}
		n, _ := build(mem)
		out, err := n.Invoke(ctx, map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		if got := out["history"].(string); got != "user: d" {
			t.Fatalf("history = %q, want default session", got)
		}
	})

	t.Run("session-isolation", func(t *testing.T) {
		mem := NewInMemoryMemory()
		if err := mem.Append(ctx, "s1", []Message{{Role: "user", Content: "one"}}); err != nil {
			t.Fatal(err)
		}
		if err := mem.Append(ctx, "s2", []Message{{Role: "user", Content: "two"}}); err != nil {
			t.Fatal(err)
		}
		n, _ := build(mem)
		out1, err := n.Invoke(ctx, map[string]any{"ConversationID": "s1"})
		if err != nil {
			t.Fatal(err)
		}
		out2, err := n.Invoke(ctx, map[string]any{"ConversationID": "s2"})
		if err != nil {
			t.Fatal(err)
		}
		if out1["history"].(string) != "user: one" || out2["history"].(string) != "user: two" {
			t.Fatalf("isolation broken: %q vs %q", out1["history"], out2["history"])
		}
	})

	t.Run("empty-history-is-empty-string", func(t *testing.T) {
		n, _ := build(NewInMemoryMemory())
		out, err := n.Invoke(ctx, map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		if got := out["history"].(string); got != "" {
			t.Fatalf("history = %q, want empty", got)
		}
	})

	t.Run("nil-memory-graceful-degrade", func(t *testing.T) {
		n, err := build(nil)
		if err != nil {
			t.Fatal(err)
		}
		out, err := n.Invoke(ctx, map[string]any{"ConversationID": "s1"})
		if err != nil {
			t.Fatal(err)
		}
		if got := out["history"].(string); got != "" {
			t.Fatalf("history = %q, want empty (nil store degrade)", got)
		}
	})
}

func TestConversationNodeBuildConfig(t *testing.T) {
	// ConversationConfig 是空 DTO:任何 config 都能 decode;nil 也宽容。
	n, err := (Conversation{}).Build(context.Background(),
		&spec.NodeSpec{Kind: spec.KindConversation, Config: []byte(`{}`)}, Deps{})
	if err != nil {
		t.Fatalf("Build with empty config: %v", err)
	}
	if n == nil {
		t.Fatal("Build returned nil impl")
	}
	if (Conversation{}).Kind() != spec.KindConversation {
		t.Fatal("Kind mismatch")
	}
}

func TestConversationIDKeys(t *testing.T) {
	if got := conversationID(map[string]any{}); got != "default" {
		t.Fatalf("empty input convID = %q, want default", got)
	}
	if got := conversationID(map[string]any{"ConversationID": "abc"}); got != "abc" {
		t.Fatalf("ConversationID convID = %q, want abc", got)
	}
	if got := conversationID(map[string]any{"conversation_id": "x-y"}); got != "x-y" {
		t.Fatalf("conversation_id convID = %q, want x-y", got)
	}
	if got := conversationID(map[string]any{"ConversationID": "", "conversation_id": "real"}); got != "real" {
		t.Fatalf("fallback convID = %q, want real", got)
	}
}
