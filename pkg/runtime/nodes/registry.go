// Package nodes — 节点注册表（PRD M2 接口面；v1 只含注册机制与测试桩，
// 19 Kind 实现随 M2 落地）。
//
// compile 通过 Registry 把 spec 节点实例化为可执行 NodeImpl；NodeImpl 的
// 契约是"输入 map → 输出 map + error"，输入 map 的 key 为节点端口路径
// （Source.Path），输出 map 的 key 为输出端口名。命名空间化（按节点 ID 包装）
// 与输入装配由 compile 层负责，节点实现不感知图结构。
package nodes

import (
	"context"
	"fmt"
	"sync"

	"github.com/nodecoda/ncoda/pkg/llm"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/trace"
)

// Deps carries the execution-side dependencies a node builder may need.
// M2.0: 最小接口面（I/O 节点 M2.2 落地的契约；签名随实现演进）。
type Deps struct {
	// HTTP 抽象出站调用（http 节点）。
	HTTP HTTPClient
	// Models 创建 LLM 补全（llm 节点；工厂而非单客户端，便于按配置注入凭证）。
	Models ModelFactory
	// Tools 提供工具注册与调用（tool 节点）。
	Tools ToolRegistry
	// AgentStep 是 agent 节点内部步骤的观察钩子（nil 安全;thought 事件
	// 广播,对齐参考目标 AgentLogEvent —— 解 agent 黑盒）。
	AgentStep func(AgentStep)
	// State 是跨节点可共享的执行期暂存（loop 迭代状态、变量绑定）。
	State State
	// Memory 是 agent 跨轮会话历史存储（M11;nil = 记忆关闭,与
	// AgentConfig.Memory=false 等价）。agent 节点读取并追加
	// user+assistant 文本交换,按会话 ID 隔离。
	Memory Memory
	// TokenSink 接收模型输出增量 token（M12 流式;nil = 非流式）。
	// 语义:只在"最终答案"轮触发（thought/工具轮不流入）;文本为增量,
	// 调用方按序打印即得打字机效果。
	TokenSink func(text string)
	// Knowledge 提供知识库检索（knowledge 节点,M14）。nil = 未注入 →
	// knowledge 节点 Build 期 RKindRejected（D4 诚实 fail-fast）。
	Knowledge KnowledgeRetriever
	// LLMTrace 观察 llm/agent 节点每次实际发出的消息列表（M15 调试）。
	// nil = 关闭;回调同步调用、只读（不得修改 msgs;语义与 TokenSink
	// 一样是纯观察钩子）。
	LLMTrace func(nodeID, model string, msgs []Message)
	// SubWorkflows 解析并执行外部子工作流调用（subflow 声明节点）。
	// nil → subworkflow 节点 Build 期 RNodeError（fail-closed,与
	// tool/knowledge 一致;不静默跳过子工作流）。
	SubWorkflows SubWorkflowRunner
	// NodeModules 是用户 JavaScript 节点的模块根（含 node_modules 的目录，
	// 或 node_modules 目录本身）。空 ⇒ 只有 node 内置与相对/绝对路径可用
	// （Build 期 RCODE_MODULE_UNRESOLVED，见 jsimports.go）。
	NodeModules string
	// SemTrace observes execution-semantics decisions (P1a-1 minimum trace).
	// nil = trace disabled; the node path is then byte-for-byte unchanged.
	SemTrace func(trace.Record)
}

// KnowledgeChunk 是单条检索命中的最小语义载荷（对齐参考目标
// knowledge-retrieval 的文档块形状）。
type KnowledgeChunk struct {
	DocumentID int64
	Content    string
	Title      string
	Score      float64
}

// KnowledgeRetriever 抽象知识库检索后端:query + 数据集 → 命中片段。
// 适配器示例:向量库（milvus/pgvector）embedding 检索、参考目标 dataset API、
// 离线 stub（测试确定性）。
type KnowledgeRetriever interface {
	Retrieve(ctx context.Context, query string, datasetIDs []string) ([]KnowledgeChunk, error)
}

// SubWorkflowRequest 是一次子工作流调用的外部载荷：平台身份 + 输入绑定。
// Inputs 按 subflow 声明 input 契约字段名组织（装配侧 keyed by 字段名）。
type SubWorkflowRequest struct {
	WorkflowID      string
	WorkflowVersion string
	SpaceID         string
	Inputs          map[string]any
}

// SubWorkflowRunner 抽象外部子工作流执行后端：workflowId/version → 结果。
// 适配器示例：coze API 调用、本地子图展开 stub、测试确定性 runner。
// 返回结果按输出端口 key 组织（与 OutTypes 声明一致）。
type SubWorkflowRunner interface {
	Run(ctx context.Context, req SubWorkflowRequest) (map[string]any, error)
}

// HTTPClient abstracts outbound HTTP calls.
type HTTPClient interface {
	Do(ctx context.Context, method, url string, headers map[string]string, body any) (status int, response []byte, err error)
}

// ToolCall 是一次模型发起的工具调用（对齐参考目标 AssistantPromptMessage.ToolCall）。
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // JSON 参数字符串
	Index     int    // 流式多工具调用合并标识（OpenAI delta 语义;-1 = 非流式单条）
}

// ToolSpec 是暴露给模型的工具描述（JSON Schema 参数）。
type ToolSpec struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON Schema object
}

// PartType 是多模态内容片段类型（对齐 gateway / OpenAI wire 类型）。
type PartType string

const (
	PartText       PartType = "text"
	PartImageURL   PartType = "image_url"
	PartInputAudio PartType = "input_audio"
	PartVideoURL   PartType = "video_url"
	PartFile       PartType = "file"
)

// Part 是消息中的一个内容片段。文本消息可继续只用 Content；出现多模态
// 内容时 Parts 承载完整有序片段，Content 保留文本拼接用于 trace/兼容。
type Part struct {
	Type     PartType
	Text     string
	URL      string
	MIME     string
	Filename string
	Data     []byte
}

// Message is one chat turn. Role ∈ {system, user, assistant, tool}。
//   - assistant 消息可携带 ToolCalls（模型发起的工具调用）；
//   - tool 消息回填一次工具调用结果（ToolCallID/ToolName/Content）；
//   - user 消息可用 Parts 承载 text/image_url/... 多模态内容。
type Message struct {
	Role       string
	Content    string
	Parts      []Part
	ToolCallID string     // role=tool
	ToolName   string     // role=tool
	ToolCalls  []ToolCall // role=assistant
}

// ModelFactory creates chat completions.
type ModelFactory interface {
	Chat(ctx context.Context, model string, messages []Message) (string, error)
}

// LLMUsage is normalized token accounting. A nil member means the provider
// did not expose that observation; zero is a real reported value.
type LLMUsage struct {
	PromptTokens     *int64 `json:"prompt_tokens,omitempty"`
	CompletionTokens *int64 `json:"completion_tokens,omitempty"`
	TotalTokens      *int64 `json:"total_tokens,omitempty"`
}

// ModelResponse is the optional rich result returned by v2-capable models.
// Content is retained as a compatibility spelling for adapters that model
// chat APIs directly. TextPresent distinguishes an explicitly empty canonical
// response from an adapter that only populated Content.
type ModelResponse struct {
	Text             string
	TextPresent      bool
	Content          string
	ReasoningContent *string
	Usage            *LLMUsage
	FinishReason     *string
}

// LLMResponse is the descriptive name used by integrations; it aliases the
// runtime DTO so adapters can use either spelling without wrappers.
type LLMResponse = ModelResponse

// RichModelFactory is optional. Legacy ModelFactory implementations remain
// valid and are adapted to a response with absent metadata.
type RichModelFactory interface {
	ModelFactory
	ChatWithMetadata(ctx context.Context, model string, messages []Message) (ModelResponse, error)
}

// RichModel is a short alias for adapters that do not embed ModelFactory in
// their declaration. It is intentionally accepted by the runtime as well.
type RichModel interface {
	ChatWithMetadata(ctx context.Context, model string, messages []Message) (ModelResponse, error)
}

// RichChatModelFactory accepts the alternate ChatRich spelling used by a few
// provider adapters. It is kept separate so the legacy ToolCallingModel
// contract remains unchanged.
type RichChatModelFactory interface {
	ModelFactory
	ChatRich(ctx context.Context, model string, messages []Message) (ModelResponse, error)
}

// SchemaAwareModelFactory is optional. Structured-output adapters can use the
// declared payload schema when requesting a completion instead of relying on
// prompt text alone. Legacy and rich string-only adapters remain valid.
type SchemaAwareModelFactory interface {
	ModelFactory
	ChatWithSchema(ctx context.Context, model string, messages []Message, schema *llm.LLMSchema) (ModelResponse, error)
}

// CallOptions carries the per-call knobs that are not part of the message
// list: provider-facing sampling parameters (from the llm() config) and the
// declared structured-output schema. It is assembled once per call, so retries
// reuse the same options.
type CallOptions struct {
	Params map[string]any
	Schema *llm.LLMSchema
}

// OptionsModelFactory is optional. It receives the full per-call options in
// one place, so sampling parameters and structured output do not multiply
// interfaces (SchemaAware / Rich / RichChat remain valid and are used when a
// factory does not implement this one). Implementations must also satisfy
// ModelFactory for backward compatibility.
type OptionsModelFactory interface {
	ModelFactory
	ChatWithOptions(ctx context.Context, model string, messages []Message, opts CallOptions) (ModelResponse, error)
}

// ChatResult 是一次模型调用的产物：文本回复 + 可选工具调用。
type ChatResult struct {
	Content   string
	ToolCalls []ToolCall
}

// ToolCallingModel 是支持工具调用的模型工厂（agent 节点断言使用；
// 对齐参考目标 LLMModelRuntime.invoke_llm 的 tools 参数面）。
type ToolCallingModel interface {
	ModelFactory
	ChatWithTools(ctx context.Context, model string, messages []Message, tools []ToolSpec) (ChatResult, error)
}

// ToolRegistry exposes callable tools by name.
type ToolRegistry interface {
	Names() []string
	Call(ctx context.Context, name string, args map[string]any) (any, error)
}

// ToolDescriber 提供工具的 JSON Schema 描述（agent 节点从注册表取
// 工具参数形状，构造模型可见的工具描述；plugin.Manager 实现之）。
type ToolDescriber interface {
	ToolRegistry
	Describe(ctx context.Context, name string) (ToolSpec, bool)
}

// AgentStep 是 agent 节点内部一步（模型推理 / 工具调用）的可观测事件。
// 对齐参考目标 AgentLogEvent 的 thought/tool_name/tool_input/observation 载荷。
type AgentStep struct {
	NodeID      string
	Iteration   int
	Thought     string
	ToolName    string
	ToolInput   string
	Observation string
	Error       string
}

// State is the execution-side scratchpad shared across nodes.
type State interface {
	Get(key string) any
	Set(key string, value any)
}

// NodeImpl is one executable node: assembled inputs → port-keyed outputs.
type NodeImpl interface {
	Invoke(ctx context.Context, in map[string]any) (map[string]any, error)
}

// NodeBuilder builds one kind of node from its spec config.
type NodeBuilder interface {
	Kind() string
	Build(ctx context.Context, n *spec.NodeSpec, deps Deps) (NodeImpl, error)
}

// Registry maps kind → NodeBuilder. Register is idempotent-safe (last
// writer wins is rejected to keep the closed kind contract honest).
type Registry struct {
	mu       sync.RWMutex
	builders map[string]NodeBuilder
}

func NewRegistry() *Registry {
	return &Registry{builders: map[string]NodeBuilder{}}
}

// Register adds a builder. Duplicate kind → error (a kind must have exactly
// one implementation).
func (r *Registry) Register(b NodeBuilder) error {
	if b == nil || b.Kind() == "" {
		return fmt.Errorf("nodes: register: nil builder or empty kind")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.builders[b.Kind()]; ok {
		return fmt.Errorf("nodes: register: kind %q already registered", b.Kind())
	}
	r.builders[b.Kind()] = b
	return nil
}

// Has reports whether a kind is registered.
func (r *Registry) Has(kind string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.builders[kind]
	return ok
}

// Build instantiates a node. Unknown kind → ErrKindNotRegistered.
func (r *Registry) Build(ctx context.Context, n *spec.NodeSpec, deps Deps) (NodeImpl, error) {
	r.mu.RLock()
	b, ok := r.builders[n.Kind]
	r.mu.RUnlock()
	if !ok {
		return nil, &ErrKindNotRegistered{Kind: n.Kind}
	}
	impl, err := b.Build(ctx, n, deps)
	if err != nil {
		return nil, fmt.Errorf("nodes: build %s (%s): %w", n.ID, n.Kind, err)
	}
	return impl, nil
}

// ErrKindNotRegistered is a sentinel-wrapped error for missing builders.
type ErrKindNotRegistered struct{ Kind string }

func (e *ErrKindNotRegistered) Error() string {
	return fmt.Sprintf("node kind %q has no registered builder", e.Kind)
}
