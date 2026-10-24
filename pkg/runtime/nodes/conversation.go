package nodes

// conversation.go — @mode advanced-chat 会话历史读取节点（M13,对齐
// 参考目标 chatflow 的 chat-history 语义）。
//
// 节点从 Deps.Memory 读取当前会话（in["ConversationID"],缺省 "default"）
// 的历史并渲染为文本（输出端口 "history"）。渲染格式:
//
//	user: <内容>
//	assistant: <内容>
//
// 空历史 / 无 Memory store → ""（advanced-chat 图不依赖历史时无需 store;
// 与 agent 模式的 memory fail-fast 不同,这里是宽容降级 —— 文档写明）。
// 图级记忆追加（user query + 最终输出）由 compile 层 chatflow 包装负责,
// 见 compile 包。

import (
	"context"
	"fmt"
	"strings"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// Conversation is the advanced-chat history reader node.
type Conversation struct{}

func (Conversation) Kind() string { return spec.KindConversation }

func (Conversation) Build(ctx context.Context, n *spec.NodeSpec, deps Deps) (NodeImpl, error) {
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	_ = cfg.(*spec.ConversationConfig)
	return &conversationNode{mem: deps.Memory}, nil
}

type conversationNode struct {
	mem Memory // nil = 无记忆（渲染空历史）
}

func (c *conversationNode) Invoke(ctx context.Context, in map[string]any) (map[string]any, error) {
	convID := conversationID(in)
	var text string
	if c.mem != nil {
		h, err := c.mem.History(ctx, convID)
		if err != nil {
			return nil, diag.New(diag.RNodeError, "conversation history: %v", err)
		}
		text = renderHistory(h)
	}
	return map[string]any{"history": text}, nil
}

// renderHistory 把消息列表渲染为可注入 prompt 的文本。窗口裁剪已在
// 记忆侧/图级完成;这里仅逐条渲染（tool 角色跳过 —— 记忆只存文本交换）。
func renderHistory(h []Message) string {
	var b strings.Builder
	for _, m := range h {
		switch m.Role {
		case "user", "assistant":
			fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Content)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
