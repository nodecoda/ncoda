package nodes

// agent.go — agent 节点（对齐参考目标 FunctionCall Agent 循环）。
//
// 语义（参考 api/core/agent/fc_agent_runner.py）:
//   - 执行通道:Deps.Models（ModelFactory）+ Deps.Tools（ToolRegistry）。
//     Models nil → RNodeError（Build 期 fail-fast）;配置了 tools 而注册表
//     nil → RNodeError;工具绑定缺失（注册表不认识的 wire）→ RBindingMissing。
//   - 循环:模型带工具调用 → 逐个执行 → 观察回填 → 下一轮;直至模型不再
//     发起工具调用（最终答案）或达 max_iteration（默认 10,对齐参考目标）。
//     最后一轮清空工具（逼出最终答案,对齐参考目标 同语义）。
//   - 工具缺失 → 错误文本回填（"there is not a tool named X",不崩溃）。
//   - 可观测:每轮 thought / 工具调用经 Deps.AgentStep 钩子广播
//     （对齐参考目标 AgentLogEvent 的 thought/tool_name/tool_input/observation）。
//   - 输入:in["Query"]（IR 投影路径）为 user 消息;instruction → system
//     消息;无 Query 时折叠整个输入 map（与 llm.messagesFrom 同语义）。
//   - 输出端口:"output" = 最终回复文本（与 llm 端口契约一致）。
//   - ExceptionConfig.MaxRetry 重试整个 agent 循环。
//
// v1 契约边界:模型层工具调用需要 ModelFactory 实现 ToolCallingModel
// （openai 工厂已支持）。纯文本工厂（echo）降级为无工具对话。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/nodecoda/ncoda/pkg/expr"
	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// maxAgentIteration 是 agent 工具调用轮次上限的默认值（对齐参考目标
// max_iteration 默认 10）。参考目标语义:max_iteration 轮内模型可带工具执行,
// 之后强制 1 轮无工具收尾 —— 总循环次数 = maxIteration+1,收尾轮仍发起
// 工具调用即 AgentMaxIterationError。
const maxAgentIteration = 10

// clampMaxIteration 应用参考目标的 max_iteration 边界（默认 10,上限 99）。
func clampMaxIteration(v int) int {
	if v <= 0 {
		return maxAgentIteration
	}
	if v > 99 {
		return 99
	}
	return v
}

// Agent invokes a Function-Calling agent loop over ModelFactory + ToolRegistry.
type Agent struct{}

func (Agent) Kind() string { return spec.KindAgent }

func (Agent) Build(ctx context.Context, n *spec.NodeSpec, deps Deps) (NodeImpl, error) {
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	ac := cfg.(*spec.AgentConfig)
	switch ac.Strategy {
	case "", "function-calling":
		// 手写 fc 循环（对齐参考目标 fc_agent_runner）。
	case "react":
		// eino 官方 flow/agent/react（对齐 coze singleagent）。
		return buildReactNode(ctx, n, ac, deps)
	default:
		return nil, diag.New(diag.RNodeError,
			"node %s: unknown agent strategy %q (want function-calling or react)", n.ID, ac.Strategy)
	}
	if deps.Models == nil {
		return nil, diag.New(diag.RNodeError, "node %s: agent node requires a ModelFactory (WithDeps)", n.ID)
	}
	if ac.Memory && deps.Memory == nil {
		return nil, diag.New(diag.RNodeError,
			"node %s: agent node has memory enabled but no Memory store (WithDeps.Memory)", n.ID)
	}
	if ac.Knowledge != nil && deps.Knowledge == nil {
		return nil, diag.New(diag.RNodeError,
			"node %s: agent node has knowledge configured but no retrieval backend (WithDeps.Knowledge)", n.ID)
	}
	wires := ac.Tools
	if len(wires) > 0 {
		if registryNil(deps.Tools) {
			return nil, diag.New(diag.RNodeError, "node %s: agent node wires %d tools but no ToolRegistry (WithDeps)", n.ID, len(wires))
		}
		// 绑定校验:每个 wire 在注册表中有落点（toolRegistered 后缀匹配）。
		for _, w := range wires {
			lg := agentLogicalName(w)
			if !toolRegistered(deps.Tools, lg) {
				return nil, diag.New(diag.RBindingMissing, "agent %s: tool wire %q has no binding in registry (have %v)", n.ID, lg, deps.Tools.Names())
			}
		}
	}
	return &agentNode{
		cfg: ac, model: deps.Models, reg: deps.Tools, wires: wires,
		step: deps.AgentStep, nodeID: n.ID,
		maxIter: clampMaxIteration(ac.MaxIteration),
		mem:     deps.Memory, memory: ac.Memory, window: ac.MemoryWindow,
		sink: deps.TokenSink, trace: deps.LLMTrace,
		knowledge: ac.Knowledge, kr: deps.Knowledge,
	}, nil
}

type agentNode struct {
	cfg       *spec.AgentConfig
	model     ModelFactory
	reg       ToolRegistry
	wires     []spec.AgentToolWire
	step      func(AgentStep)
	nodeID    string
	maxIter   int                                        // 工具调用轮次上限（对齐参考目标 max_iteration）
	mem       Memory                                     // M11 会话记忆存储（memory=true 时非 nil）
	memory    bool                                       // M11 记忆开关（AgentConfig.Memory）
	window    int                                        // M11 记忆窗口（<=0 不限）
	sink      func(string)                               // M12 最终答案增量 token 输出（nil = 非流式）
	trace     func(nodeID, model string, msgs []Message) // M15 调试观察
	knowledge *spec.AgentKnowledgeConfig                 // M19-2 数据集检索配置（nil = 关闭）
	kr        KnowledgeRetriever                         // M19-2 检索后端（Deps.Knowledge）
}

func (a *agentNode) Invoke(ctx context.Context, in map[string]any) (map[string]any, error) {
	retries := 0
	if ec := a.cfg.Exception; ec != nil {
		retries = ec.MaxRetry
		if retries < 0 {
			retries = 0
		}
	}
	var out string
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		out, lastErr = a.run(ctx, in)
		if lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		return nil, diag.New(diag.RNodeError, "agent: %v", lastErr)
	}
	return map[string]any{"output": out}, nil
}

// run 执行一次完整的 agent 循环（对齐参考目标 fc_agent_runner while 循环）。
func (a *agentNode) run(ctx context.Context, in map[string]any) (string, error) {
	convID := conversationID(in)
	var history []Message
	if a.memory && a.mem != nil {
		h, err := a.mem.History(ctx, convID)
		if err != nil {
			return "", fmt.Errorf("agent memory: %w", err)
		}
		history = windowed(h, a.window)
	}
	msgs := make([]Message, 0, 4+len(history))
	if a.cfg.Instruction != "" {
		msgs = append(msgs, Message{Role: "system", Content: a.cfg.Instruction})
	}
	msgs = append(msgs, history...)
	userMsg := agentUserMessage(in)
	msgs = append(msgs, userMsg)

	tcm, toolCalling := a.model.(ToolCallingModel)
	var final strings.Builder
	// 对齐参考目标 while 循环:max_iteration 轮工具执行 + 1 轮无工具收尾。
	for iter := 1; iter <= a.maxIter+1; iter++ {
		// 收尾轮清空工具（对齐参考目标 iteration_step == max_iteration_steps）。
		tools := a.toolSpecs(ctx)
		if iter == a.maxIter+1 {
			tools = nil
		}

		var res ChatResult
		var err error
		// M12 流式:优先走 ToolCallingStreamModel —— 本轮 content 增量暂存,
		// 流结束判定:无工具调用（最终答案轮）→ 回放给 TokenSink;有工具
		// 调用（thought 轮）→ 丢弃,继续工具循环。
		var streamed []string // 本轮 content 增量（回放用;nil = 非流式路径）
		var streamOK bool     // 本轮是否走了流式（无 content 的工具轮 streamed 也可能为 nil）
		if tsm, ok := tcm.(ToolCallingStreamModel); ok {
			var sr StreamReader
			sr, err = tsm.StreamWithTools(ctx, a.cfg.Model, msgs, tools)
			switch {
			case err == nil:
				streamOK = true
				streamed, res, err = consumeAgentStream(sr)
			case errors.Is(err, ErrStreamNotSupported):
				err = nil // 降级到非流式
			}
		}
		if !streamOK && err == nil {
			if a.trace != nil {
				a.trace(a.nodeID, a.cfg.Model, msgs)
			}
			if toolCalling {
				res, err = tcm.ChatWithTools(ctx, a.cfg.Model, msgs, tools)
			} else {
				var content string
				content, err = a.model.Chat(ctx, a.cfg.Model, msgs)
				res = ChatResult{Content: content}
			}
		}
		if err != nil {
			return "", fmt.Errorf("agent step %d: %w", iter, err)
		}

		a.emit(AgentStep{NodeID: a.nodeID, Iteration: iter, Thought: res.Content})
		final.WriteString(res.Content)
		if len(res.ToolCalls) == 0 {
			// 最终答案轮:把流式暂存按 chunk 边界回放（打字机效果）。
			if a.sink != nil && streamed != nil {
				for _, part := range streamed {
					a.sink(part)
				}
			}
			break
		}
		if iter == a.maxIter+1 {
			return "", fmt.Errorf("agent: max iteration (%d) reached with pending tool calls", a.maxIter)
		}

		// 模型发起了工具调用 → 执行并回填观察。
		msgs = append(msgs, Message{Role: "assistant", Content: res.Content, ToolCalls: res.ToolCalls})
		for _, tc := range res.ToolCalls {
			obs := a.execTool(ctx, tc)
			msgs = append(msgs, Message{Role: "tool", ToolCallID: tc.ID, ToolName: tc.Name, Content: obs})
			a.emit(AgentStep{NodeID: a.nodeID, Iteration: iter, ToolName: tc.Name, ToolInput: tc.Arguments, Observation: obs})
		}
	}
	answer := strings.TrimSpace(final.String())
	if a.memory && a.mem != nil {
		if err := a.mem.Append(ctx, convID, []Message{userMsg, {Role: "assistant", Content: answer}}); err != nil {
			return "", fmt.Errorf("agent memory append: %w", err)
		}
	}
	return answer, nil
}

// consumeAgentStream 读完一轮流式输出:返回 content 增量列表（回放用）、
// 累积结果（content + 按 Index 合并的工具调用）。
func consumeAgentStream(sr StreamReader) (parts []string, res ChatResult, err error) {
	defer sr.Close()
	var toolCalls []ToolCall
	for {
		ch, rerr := sr.Recv()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, ChatResult{}, rerr
		}
		if ch.Content != "" {
			parts = append(parts, ch.Content)
			res.Content += ch.Content
		}
		toolCalls = mergeToolCallDeltas(toolCalls, ch.ToolCalls)
	}
	res.ToolCalls = toolCalls
	return parts, res, nil
}

// conversationID 从节点输入提取会话 ID:ConversationID / conversation_id
// 键（编译层注入的合成 source）,缺省 "default"。空串视为缺省。
func conversationID(in map[string]any) string {
	for _, key := range []string{"ConversationID", "conversation_id"} {
		if v, ok := in[key]; ok && v != nil && fmt.Sprint(v) != "" {
			return fmt.Sprint(v)
		}
	}
	return "default"
}

// toolSpecs 把 wire 列表投影为模型可见的工具描述。注册表实现
// ToolDescriber 时取 JSON Schema;否则用空 schema（参数自由）。
func (a *agentNode) toolSpecs(ctx context.Context) []ToolSpec {
	td, ok := a.reg.(ToolDescriber)
	if !ok {
		specs := make([]ToolSpec, 0, len(a.wires)+1)
		for _, w := range a.wires {
			name := w.Alias
			if name == "" {
				name = agentLogicalName(w)
			}
			specs = append(specs, ToolSpec{Name: name, Parameters: map[string]any{"type": "object"}})
		}
		if a.knowledge != nil && a.kr != nil {
			specs = append(specs, knowledgeToolSpec())
		}
		return specs
	}
	specs := make([]ToolSpec, 0, len(a.wires)+1)
	for _, w := range a.wires {
		name := w.Alias
		if name == "" {
			name = agentLogicalName(w)
		}
		s, ok := td.Describe(ctx, agentLogicalName(w))
		if !ok {
			continue
		}
		s.Name = name // 模型看到的别名
		specs = append(specs, s)
	}
	// M19-2:knowledge 配置 → 内置检索工具注入（对齐参考目标把数据集封装为
	// DatasetRetrieverTool 注入 prompt_messages_tools,模型自主调用）。
	if a.knowledge != nil && a.kr != nil {
		specs = append(specs, knowledgeToolSpec())
	}
	return specs
}

// knowledgeToolName 是内置数据集检索工具的逻辑名（模型可见）。
const knowledgeToolName = "knowledge_retrieval"

// knowledgeToolSpec 是内置检索工具的描述（模型可见 JSON Schema）。
func knowledgeToolSpec() ToolSpec {
	return ToolSpec{
		Name:        knowledgeToolName,
		Description: "检索知识库数据集，获取与问题相关的文档片段，用于回答用户问题。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "检索查询（用户问题或子问题）"},
			},
			"required": []string{"query"},
		},
	}
}

// execTool 执行一次工具调用;失败/缺失一律回填文本（对齐参考目标,不崩溃）。
func (a *agentNode) execTool(ctx context.Context, tc ToolCall) string {
	// M19-2:内置检索工具 —— 走 Deps.Knowledge,不经注册表。
	if a.knowledge != nil && a.kr != nil && tc.Name == knowledgeToolName {
		return a.execKnowledge(ctx, tc)
	}
	if a.reg == nil {
		return fmt.Sprintf("there is not a tool named %s", tc.Name)
	}
	lg := a.resolveWire(tc.Name)
	if lg == "" {
		return fmt.Sprintf("there is not a tool named %s", tc.Name)
	}
	args := map[string]any{}
	if strings.TrimSpace(tc.Arguments) != "" {
		if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
			return fmt.Sprintf("tool %s: invalid arguments: %v", tc.Name, err)
		}
	}
	res, err := a.reg.Call(ctx, lg, args)
	if err != nil {
		return err.Error()
	}
	b, merr := json.Marshal(res)
	if merr != nil {
		return fmt.Sprintf("%v", res)
	}
	return string(b)
}

// execKnowledge 执行内置知识库检索:query 参数 → kr.Retrieve(dataset_ids)
// → 命中片段文本回填（title/score + content,对齐参考目标 检索工具返回）。
func (a *agentNode) execKnowledge(ctx context.Context, tc ToolCall) string {
	args := map[string]any{}
	if strings.TrimSpace(tc.Arguments) != "" {
		if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
			return fmt.Sprintf("knowledge_retrieval: invalid arguments: %v", err)
		}
	}
	query, _ := args["query"].(string)
	if strings.TrimSpace(query) == "" {
		return "knowledge_retrieval: query is required"
	}
	chunks, err := a.kr.Retrieve(ctx, query, a.knowledge.DatasetIDs)
	if err != nil {
		return fmt.Sprintf("knowledge_retrieval: %v", err)
	}
	if a.knowledge.TopK > 0 && len(chunks) > a.knowledge.TopK {
		chunks = chunks[:a.knowledge.TopK]
	}
	if len(chunks) == 0 {
		return "knowledge_retrieval: no matching documents"
	}
	var b strings.Builder
	for i, c := range chunks {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "[%s] (score: %.3f)\n%s", c.Title, c.Score, c.Content)
	}
	return b.String()
}

// resolveWire 把模型调用的名字（别名或逻辑名）映射到注册表逻辑名。
func (a *agentNode) resolveWire(modelName string) string {
	for _, w := range a.wires {
		if w.Alias != "" && w.Alias == modelName {
			return agentLogicalName(w)
		}
	}
	for _, w := range a.wires {
		lg := agentLogicalName(w)
		if lg == modelName || strings.HasSuffix(lg, "/"+modelName) {
			return lg
		}
	}
	return ""
}

func (a *agentNode) emit(st AgentStep) {
	if a.step != nil {
		a.step(st)
	}
}

// agentUserMessage 提取用户消息：Query/query/input/messages 之一 → user；
// 无 → 折叠整个输入 map（确定性 JSON,与 llm.messagesFrom 同语义）。
func agentUserMessage(in map[string]any) Message {
	for _, key := range []string{"Query", "query", "input", "messages"} {
		if v, ok := in[key]; ok && v != nil {
			return Message{Role: "user", Content: expr.FormatTemplateValue(v)}
		}
	}
	b, _ := json.Marshal(in)
	return Message{Role: "user", Content: string(b)}
}

// agentLogicalName is the registry key for an agent tool wire (provider/tool
// when provider present, plain tool name otherwise — same as tool node).
func agentLogicalName(w spec.AgentToolWire) string {
	if w.ProviderName != "" {
		return w.ProviderName + "/" + w.ToolName
	}
	return w.ToolName
}

// toolRegistered reports whether the registry binds lg. lg may be the full
// provider/tool name or the bare tool name: "weather/get_weather" matches
// exactly, "get_weather" matches the trailing segment (same contract as
// toolNode's runtime lookup).
func toolRegistered(tr ToolRegistry, lg string) bool {
	for _, n := range tr.Names() {
		if n == lg || strings.HasSuffix(n, "/"+lg) {
			return true
		}
	}
	return false
}

// registryNil reports whether a ToolRegistry is nil or a typed-nil pointer
// in an interface (e.g. a *plugin.Manager(nil) from CLI wiring). Direct
// interface comparison misses typed nils and panics on method calls.
func registryNil(tr ToolRegistry) bool {
	if tr == nil {
		return true
	}
	v := reflect.ValueOf(tr)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func:
		return v.IsNil()
	}
	return false
}
