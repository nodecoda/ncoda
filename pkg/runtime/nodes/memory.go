package nodes

// memory.go — 会话记忆抽象（M11,对齐 参考目标的会话记忆）。
//
// 语义:agent 的跨轮记忆按会话 ID（conversation id）存取"文本交换对"
// （user + assistant 最终回复）。工具调用链属于单轮内部状态,不进入记忆
// （对齐参考目标 ConversationMemory:只存 conversation 消息）。
//
// 记忆是 Deps 注入的运行时资源（可替换为文件/DB 持久化）;节点实现只
// 依赖 Memory 接口,进程内默认实现是 InMemoryMemory。v1 用条数窗口
// （memory_window = 消息对数,0 = 不限,默认 20）而非 参考目标的 token 缓冲
// 裁剪（无 tokenizer 依赖,确定性可测）。

import (
	"context"
	"sync"
)

// windowed 把 history 裁剪为最近 window 对（user+assistant = 2 条/对）。
// window <= 0 表示不裁剪（保留全部历史）。history 奇数长度时按 2*window
// 条截尾（窗口是"最近 N 对"的近似）。
func windowed(history []Message, window int) []Message {
	if window <= 0 || len(history) <= 2*window {
		return history
	}
	keep := 2 * window
	return history[len(history)-keep:]
}

// Memory 是 agent 跨轮会话历史存储（每轮 user+assistant 文本交换）。
type Memory interface {
	// History 返回 convID 的已存历史（最旧在前）。不存在 → 空切片,nil error。
	History(ctx context.Context, convID string) ([]Message, error)
	// Append 为 convID 持久化一轮新交换（通常 [user, assistant]）。
	Append(ctx context.Context, convID string, exchange []Message) error
}

// InMemoryMemory 是进程内默认记忆存储:map[convID][]Message,并发安全。
// 单进程多次 run()（或多次图 Invoke）共享;进程退出即失（CLI 用
// 文件持久化包装,见 cmd/ncoda-run/memory.go）。
type InMemoryMemory struct {
	mu sync.Mutex
	m  map[string][]Message
}

// NewInMemoryMemory builds an empty in-process memory store.
func NewInMemoryMemory() *InMemoryMemory {
	return &InMemoryMemory{m: map[string][]Message{}}
}

// History implements Memory.
func (s *InMemoryMemory) History(_ context.Context, convID string) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, len(s.m[convID]))
	copy(out, s.m[convID])
	return out, nil
}

// Append implements Memory.
func (s *InMemoryMemory) Append(_ context.Context, convID string, exchange []Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[convID] = append(s.m[convID], exchange...)
	return nil
}
