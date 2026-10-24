package nodes

// react.go — strategy "react"（eino 官方 flow/agent/react,对齐 coze
// singleagent 的 ReAct agent）。适配层把本项目的 ModelFactory /
// ToolRegistry 抽象桥接到 eino 的 model.ToolCallingChatModel 与
// tool.InvokableTool 接口,react 图在 eino 内执行 ReAct 循环
// （thought/action/observation 文本协议）。
//
// 契约:v1 阶段仅支持非流式（Generate 路径）;纯文本工厂（非
// ToolCallingModel）降级为无工具对话。MaxStep = max_iteration + 4
// （图节点数≈3,预留余量）,对齐 fc 循环的 max_iteration+1 轮语义。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// ── 消息转换（nodes.Message ↔ eino schema.Message） ─────────────────────

func toEinoMessages(msgs []Message) []*schema.Message {
	out := make([]*schema.Message, 0, len(msgs))
	for _, m := range msgs {
		em := &schema.Message{Role: schema.RoleType(m.Role), Content: m.Content}
		switch m.Role {
		case "assistant":
			for _, tc := range m.ToolCalls {
				em.ToolCalls = append(em.ToolCalls, schema.ToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: schema.FunctionCall{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				})
			}
		case "tool":
			em.ToolCallID = m.ToolCallID
			em.ToolName = m.ToolName
		}
		out = append(out, em)
	}
	return out
}

func fromEinoMessage(m *schema.Message) ChatResult {
	res := ChatResult{Content: m.Content}
	for _, tc := range m.ToolCalls {
		res.ToolCalls = append(res.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return res
}

// toolsToSpecs 把 eino ToolInfo 参数 schema 投影回本项目的 ToolSpec
// （adapter 的 WithTools 侧已持有原始 ToolSpec,直接透传更简单）。
func toolsToSpecs(ts []*schema.ToolInfo) []ToolSpec {
	specs := make([]ToolSpec, 0, len(ts))
	for _, t := range ts {
		params := map[string]any{}
		if t.ParamsOneOf != nil {
			b, _ := json.Marshal(t)
			var tmp struct {
				JSONSchema map[string]any `json:"json_schema"`
			}
			if json.Unmarshal(b, &tmp) == nil {
				params = tmp.JSONSchema
			}
		}
		specs = append(specs, ToolSpec{Name: t.Name, Description: t.Desc, Parameters: params})
	}
	return specs
}

// ── 模型适配器: ModelFactory → eino model.ToolCallingChatModel ─────────

type reactModelAdapter struct {
	mf     ModelFactory
	model  string
	nodeID string // M15 回归: LLMTrace 需要节点 ID 而非 model 名
	tools  []*schema.ToolInfo
	trace  func(nodeID, model string, msgs []Message) // M15 调试观察
}

var _ model.ToolCallingChatModel = (*reactModelAdapter)(nil)

func (a *reactModelAdapter) Generate(ctx context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	ours := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		om := Message{Role: string(m.Role), Content: m.Content}
		switch m.Role {
		case schema.Assistant:
			for _, tc := range m.ToolCalls {
				om.ToolCalls = append(om.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
			}
		case schema.Tool:
			om.ToolCallID = m.ToolCallID
			om.ToolName = m.ToolName
		}
		ours = append(ours, om)
	}
	var res ChatResult
	var err error
	if a.trace != nil {
		a.trace(a.nodeID, a.model, ours)
	}
	if tcm, ok := a.mf.(ToolCallingModel); ok {
		res, err = tcm.ChatWithTools(ctx, a.model, ours, toolsToSpecs(a.tools))
	} else {
		var content string
		content, err = a.mf.Chat(ctx, a.model, ours)
		res = ChatResult{Content: content}
	}
	if err != nil {
		return nil, err
	}
	em := &schema.Message{Role: schema.Assistant, Content: res.Content}
	for _, tc := range res.ToolCalls {
		em.ToolCalls = append(em.ToolCalls, schema.ToolCall{
			ID: tc.ID, Type: "function",
			Function: schema.FunctionCall{Name: tc.Name, Arguments: tc.Arguments},
		})
	}
	return em, nil
}

// Stream 把底层 ModelFactory 的流式能力桥接到 eino 流（M12）。
// 工具调用轮:增量经 schema.Message.ToolCalls（Index 标识）透出,
// eino 的 concatToolCalls 负责累积（与 OpenAI SSE delta 语义一致）。
// 底层不支持流式 → ErrStreamNotSupported（reactNode 降级 Generate）。
func (a *reactModelAdapter) Stream(ctx context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	ours := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		om := Message{Role: string(m.Role), Content: m.Content}
		switch m.Role {
		case schema.Assistant:
			for _, tc := range m.ToolCalls {
				om.ToolCalls = append(om.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
			}
		case schema.Tool:
			om.ToolCallID = m.ToolCallID
			om.ToolName = m.ToolName
		}
		ours = append(ours, om)
	}
	tsm, ok := a.mf.(ToolCallingStreamModel)
	if !ok {
		return nil, ErrStreamNotSupported
	}
	sr, err := tsm.StreamWithTools(ctx, a.model, ours, toolsToSpecs(a.tools))
	if err != nil {
		return nil, err
	}
	r, w := schema.Pipe[*schema.Message](8)
	go func() {
		defer w.Close()
		for {
			ch, rerr := sr.Recv()
			if rerr == io.EOF {
				return
			}
			if rerr != nil {
				_ = w.Send(nil, rerr)
				return
			}
			// 流式 chunk 属于 assistant 消息:eino 累积后传给 tools 节点
			// 时校验 role（工具调用只能来自 assistant）。
			em := &schema.Message{Role: schema.Assistant, Content: ch.Content}
			for _, tc := range ch.ToolCalls {
				idx := tc.Index
				em.ToolCalls = append(em.ToolCalls, schema.ToolCall{
					Index: &idx, ID: tc.ID, Type: "function",
					Function: schema.FunctionCall{Name: tc.Name, Arguments: tc.Arguments},
				})
			}
			if closed := w.Send(em, nil); closed {
				return
			}
		}
	}()
	return r, nil
}

func (a *reactModelAdapter) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return &reactModelAdapter{mf: a.mf, model: a.model, nodeID: a.nodeID, tools: tools, trace: a.trace}, nil
}

// ── 工具适配器: ToolRegistry → eino tool.InvokableTool ─────────────────

type reactToolAdapter struct {
	name string
	spec ToolSpec
	tr   ToolRegistry
}

var _ tool.InvokableTool = (*reactToolAdapter)(nil)

func (t *reactToolAdapter) Info(context.Context) (*schema.ToolInfo, error) {
	params := t.spec.Parameters
	var js *jsonschema.Schema
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &js); err != nil {
			return nil, err
		}
	}
	return &schema.ToolInfo{Name: t.name, Desc: t.spec.Description, ParamsOneOf: schema.NewParamsOneOfByJSONSchema(js)}, nil
}

func (t *reactToolAdapter) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	args := map[string]any{}
	if argumentsInJSON != "" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			return "", fmt.Errorf("react tool %s: bad arguments %q: %w", t.name, argumentsInJSON, err)
		}
	}
	out, err := t.tr.Call(ctx, t.name, args)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ── react 节点（strategy="react" 的执行实现） ───────────────────────────

type reactNode struct {
	agent  *react.Agent
	cfg    *spec.AgentConfig
	step   func(AgentStep)
	nodeID string
	query  *spec.Source
	mem    Memory // M11 会话记忆存储（memory=true 时非 nil）
	memory bool   // M11 记忆开关
	window int    // M11 记忆窗口（<=0 不限）
	// history 是本轮注入的历史（Invoke 时从 mem 加载;MessageModifier
	// 每步重注入 —— eino react state.Messages 不保留 modifier 输出）。
	history []Message
	sink    func(string) // M12 最终答案增量 token 输出（nil = 非流式）
}

// buildReactNode 装配 eino ReAct agent（对齐 coze agentflow 的 react_agent）。
func buildReactNode(ctx context.Context, n *spec.NodeSpec, ac *spec.AgentConfig, deps Deps) (NodeImpl, error) {
	if tcm, ok := deps.Models.(ToolCallingModel); ok {
		_ = tcm
	}
	var einoTools []tool.BaseTool
	if len(ac.Tools) > 0 {
		td, ok := deps.Tools.(ToolDescriber)
		if !ok {
			return nil, diag.New(diag.RNodeError,
				"node %s: react strategy needs a ToolDescriber registry (plugin.Manager)", n.ID)
		}
		for _, w := range ac.Tools {
			lg := agentLogicalName(w)
			desc, ok := td.Describe(ctx, lg)
			if !ok {
				continue
			}
			desc.Name = w.Alias // 模型看到的别名
			if desc.Name == "" {
				desc.Name = lg
			}
			einoTools = append(einoTools, &reactToolAdapter{name: desc.Name, spec: desc, tr: deps.Tools})
		}
	}

	agentCfg := &react.AgentConfig{
		ToolCallingModel: &reactModelAdapter{mf: deps.Models, model: ac.Model, nodeID: n.ID, trace: deps.LLMTrace},
		ToolsConfig:      compose.ToolsNodeConfig{Tools: einoTools},
		MaxStep:          clampMaxIteration(ac.MaxIteration) + 4,
		GraphName:        "ncoda-react-agent",
	}
	if ac.Memory && deps.Memory == nil {
		return nil, diag.New(diag.RNodeError,
			"node %s: react agent has memory enabled but no Memory store (WithDeps.Memory)", n.ID)
	}
	rn := &reactNode{
		cfg: ac, step: deps.AgentStep, nodeID: n.ID, query: ac.Query,
		mem: deps.Memory, memory: ac.Memory, window: ac.MemoryWindow,
		sink: deps.TokenSink,
	}
	persona := ac.Instruction
	// MessageModifier 每步把 persona + 记忆历史重注入消息头（eino react
	// 的 state.Messages 只累积图内消息,不保留 modifier 输出 —— 历史必须
	// 每步重加,否则第 2 轮工具循环后模型就看不到历史了）。
	agentCfg.MessageModifier = func(_ context.Context, msgs []*schema.Message) []*schema.Message {
		out := make([]*schema.Message, 0, len(msgs)+1+len(rn.history))
		if persona != "" {
			out = append(out, &schema.Message{Role: schema.System, Content: persona})
		}
		out = append(out, toEinoMessages(rn.history)...)
		out = append(out, msgs...)
		return out
	}
	agent, err := react.NewAgent(ctx, agentCfg)
	if err != nil {
		return nil, diag.New(diag.RNodeError, "node %s: build react agent: %v", n.ID, err)
	}
	rn.agent = agent
	return rn, nil
}

func (r *reactNode) Invoke(ctx context.Context, in map[string]any) (map[string]any, error) {
	var query string
	if r.query != nil {
		if v, ok := in[r.query.Name]; ok {
			query = fmt.Sprintf("%v", v)
		} else if v, ok := in["Query"]; ok {
			query = fmt.Sprintf("%v", v)
		}
	} else if v, ok := in["Query"]; ok {
		query = fmt.Sprintf("%v", v)
	}
	// M11 会话记忆:加载历史（窗口裁剪）供 MessageModifier 注入。
	convID := conversationID(in)
	if r.memory && r.mem != nil {
		h, err := r.mem.History(ctx, convID)
		if err != nil {
			return nil, diag.New(diag.RNodeError, "node %s: react agent memory: %v", r.nodeID, err)
		}
		r.history = windowed(h, r.window)
		defer func() { r.history = nil }()
	}
	msgs := []*schema.Message{{Role: schema.User, Content: query}}
	// M12 流式优先:Agent.Stream 产出最终答案的增量（中间工具轮不进
	// 最终流）,边读边转发 TokenSink。底层不支持流式 → 降级 Generate。
	var out *schema.Message
	sr, serr := r.agent.Stream(ctx, msgs)
	if serr != nil {
		if !errors.Is(serr, ErrStreamNotSupported) {
			return nil, diag.New(diag.RNodeError, "node %s: react agent stream: %v", r.nodeID, serr)
		}
		out, serr = r.agent.Generate(ctx, msgs)
		if serr != nil {
			return nil, diag.New(diag.RNodeError, "node %s: react agent: %v", r.nodeID, serr)
		}
	} else {
		out = &schema.Message{}
		for {
			chunkMsg, rerr := sr.Recv()
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				return nil, diag.New(diag.RNodeError, "node %s: react agent stream: %v", r.nodeID, rerr)
			}
			if chunkMsg.Content != "" {
				out.Content += chunkMsg.Content
				if r.sink != nil {
					r.sink(chunkMsg.Content)
				}
			}
		}
	}
	res := fromEinoMessage(out)
	if r.step != nil {
		r.step(AgentStep{NodeID: r.nodeID, Iteration: 1, Thought: res.Content})
	}
	if r.memory && r.mem != nil {
		if err := r.mem.Append(ctx, convID, []Message{
			{Role: "user", Content: query},
			{Role: "assistant", Content: res.Content},
		}); err != nil {
			return nil, diag.New(diag.RNodeError, "node %s: react agent memory append: %v", r.nodeID, err)
		}
	}
	return map[string]any{"output": res.Content}, nil
}
