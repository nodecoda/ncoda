package nodes

// streaming.go — 模型流式接口（M12）。
//
// v1 契约:图执行仍走 Invoke（输出 map 语义不变）;流式输出 = agent 节点
// 执行时把"最终答案的增量 token"经 Deps.TokenSink 边算边发（CLI 打字机
// 效果）。模型层可选实现流式接口:
//
//	StreamingModel            — 纯文本流（llm 节点可用）
//	ToolCallingStreamModel    — 工具调用流（agent 节点可用;工具调用以
//	                            Index 标识的 delta 增量返回,调用方合并）
//
// StreamChunk.ToolCalls 是增量:同一次调用的 arguments 按 Index 拼接,
// id/type/name 只出现在首个 delta（对齐 OpenAI SSE / eino concatToolCalls）。

import (
	"context"
	"errors"
	"io"
)

// StreamChunk 是一次模型流式输出的一段增量:Content 是文本增量;
// ToolCalls 是工具调用增量（按 Index 合并）。
type StreamChunk struct {
	Content   string
	ToolCalls []ToolCall
}

// StreamReader 是模型输出流:Recv 返回 io.EOF 表示流结束。
type StreamReader interface {
	Recv() (StreamChunk, error)
	Close()
}

// StreamingModel 是支持流式文本补全的工厂。
type StreamingModel interface {
	ModelFactory
	Stream(ctx context.Context, model string, messages []Message) (StreamReader, error)
}

// ToolCallingStreamModel 是支持流式工具调用的工厂（agent 节点优先使用）。
type ToolCallingStreamModel interface {
	ToolCallingModel
	StreamWithTools(ctx context.Context, model string, messages []Message, tools []ToolSpec) (StreamReader, error)
}

// ErrStreamNotSupported 是流式接口的降级信号:节点发现模型不支持流式时
// 回退到一次性 Chat（非错误,仅无增量输出）。
var ErrStreamNotSupported = errors.New("model does not support streaming")

// chunkList 是内存版 StreamReader（测试/echo 用）:按序吐 chunk。
type chunkList struct {
	chunks []StreamChunk
	closed bool
}

// NewChunkReader builds an in-memory StreamReader over chunks.
func NewChunkReader(chunks []StreamChunk) StreamReader {
	return &chunkList{chunks: chunks}
}

func (c *chunkList) Recv() (StreamChunk, error) {
	if c.closed || len(c.chunks) == 0 {
		return StreamChunk{}, io.EOF
	}
	ch := c.chunks[0]
	c.chunks = c.chunks[1:]
	return ch, nil
}

func (c *chunkList) Close() { c.closed = true }

// drainStream 读完一个流并返回累积结果（content 拼接 + 工具调用按
// Index 合并）。用于非流式消费者/暂存判定。
func drainStream(r StreamReader) (string, []ToolCall, error) {
	defer r.Close()
	var content string
	var toolCalls []ToolCall
	for {
		ch, err := r.Recv()
		if err == io.EOF {
			return content, toolCalls, nil
		}
		if err != nil {
			return content, toolCalls, err
		}
		content += ch.Content
		toolCalls = mergeToolCallDeltas(toolCalls, ch.ToolCalls)
	}
}

// mergeToolCallDeltas 按 Index 合并工具调用增量（OpenAI delta 语义）:
// 同 Index 的 arguments 拼接,id/type/name 取首个非空。
func mergeToolCallDeltas(acc, deltas []ToolCall) []ToolCall {
	for _, d := range deltas {
		if d.Index < 0 {
			acc = append(acc, d)
			continue
		}
		found := false
		for i := range acc {
			if acc[i].Index == d.Index {
				acc[i].Arguments += d.Arguments
				if d.ID != "" {
					acc[i].ID = d.ID
				}
				if d.Name != "" {
					acc[i].Name = d.Name
				}
				found = true
				break
			}
		}
		if !found {
			acc = append(acc, d)
		}
	}
	return acc
}
