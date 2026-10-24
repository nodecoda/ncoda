package spec

import "github.com/nodecoda/ncoda/pkg/llm"

// configs.go — per-kind config DTOs (execution subset). Every field here
// is a field the runtime may legitimately read (D1 consumed-fields
// registry); anything else in the IR is compile-time only and must NOT be
// serialized into the spec.

// ── shared payloads ──────────────────────────────────────────────────────

// Ref is a dataflow reference to a producer's output port (dotted names
// carry field paths, e.g. "value.city").
type Ref struct {
	Producer string `json:"producer"`
	Output   string `json:"output"`
}

// Source is one input-port data source: a block-output ref or a literal.
// Val carries the literal when BlockID == ""; it is nullable JSON so
// ""/0/false literals survive the roundtrip.
type Source struct {
	Path    []string `json:"path,omitempty"`
	BlockID string   `json:"block_id,omitempty"`
	Name    string   `json:"name,omitempty"`
	Val     any      `json:"val"`
}

// Operand is one comparison operand: a ref or a literal.
type Operand struct {
	Ref *Ref `json:"ref,omitempty"`
	Val any  `json:"val"`
}

// Condition is one selector comparison (left op right).
type Condition struct {
	Left     Operand `json:"left"`
	Operator int     `json:"operator"`
	Right    Operand `json:"right"`
}

// ConfigPair is an uninterpreted configuration payload.
type ConfigPair struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// ExceptionConfig mirrors retry/timeout/default policy payloads.
type ExceptionConfig struct {
	MaxRetry        int  `json:"max_retry"`
	RetryIntervalMS int  `json:"retry_interval_ms"`
	TimeoutMS       int  `json:"timeout_ms"`
	ProcessType     *int `json:"process_type,omitempty"`
	FailBranch      bool `json:"fail_branch"`
	// TimeoutAxis names the semantics of TimeoutMS for this node (ES-022a,
	// P3): "total" (one budget shared by every attempt), "per_attempt"
	// (each attempt gets a fresh budget) or "none"/"" (no node-level
	// timeout). It is projected from BuildOptions.TimeoutAxes; an axis is
	// never invented from the IR alone because the same TimeoutMS bytes
	// carry different axes per family and per wire carrier.
	TimeoutAxis string `json:"timeout_axis,omitempty"`
	// DataOnErr is the `with default(<record>)` fallback payload
	// (ProcessType == 2). Empty with ProcessType == 2 is a hard error at the
	// failure site: the runtime never invents a fallback value.
	DataOnErr []DefaultField `json:"data_on_err,omitempty"`
}

// DefaultField is one entry of the failure fallback payload. Names are ncoda
// semantic result-port names; values are static literals.
type DefaultField struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

// EndOutput is one explicit output("name", value) mapping on a continuing
// End node.
type EndOutput struct {
	Name string `json:"name"`
	Ref  Ref    `json:"ref"`
}

// ── per-kind configs ─────────────────────────────────────────────────────

// StartConfig is the start node config. The compiler synthesizes it from
// Spec.Entry at graph-build time (single source of truth for params).
type StartConfig struct {
	Params []ParamSpec `json:"params"`
}

// EndAnswerConfig is the value side of a workflow answer endpoint
// (`return template "..." using { slot: value }`). The template is rendered
// with the resolved bindings and becomes the workflow's final "output"
// string; Answer != nil marks that endpoint (an empty template is legal and
// therefore distinct from nil).
type EndAnswerConfig struct {
	Template string      `json:"template"`
	Bindings []EndOutput `json:"bindings,omitempty"`
}

// EndConfig terminates the workflow: Inputs feed the canonical "output"
// result key; Outputs feed named result keys (NamedReturn). Answer carries
// the template answer endpoint (see EndAnswerConfig); it is exclusive with
// the value channels by the language rule.
type EndConfig struct {
	Inputs      []Ref            `json:"inputs"`
	Outputs     []EndOutput      `json:"outputs"`
	NamedReturn bool             `json:"named_return"`
	Answer      *EndAnswerConfig `json:"answer,omitempty"`
}

// LLMConfig invokes a model.
type LLMConfig struct {
	Model          string           `json:"model"`
	Config         []ConfigPair     `json:"config"`
	Inputs         []Source         `json:"inputs"`
	OutputFormat   string           `json:"output_format,omitempty"`
	ResponseFormat int              `json:"response_format,omitempty"`
	Exception      *ExceptionConfig `json:"exception,omitempty"`
	// Result is the semantic LLMResponse<T> contract. It is present in
	// spec.v2; v1 inputs are normalized to a string payload with no metadata.
	Result *LLMResultConfig `json:"result,omitempty"`
	// Vision is the extract(..., vision: img) operand. The runtime resolves it
	// from the dedicated "vision" input port and attaches it as an image part.
	Vision *Source `json:"vision,omitempty"`
}

type LLMResultConfig struct {
	PayloadSchema *llm.LLMSchema      `json:"payload_schema"`
	Metadata      LLMMetadataConfig   `json:"metadata"`
	Validation    LLMValidationConfig `json:"validation"`
}

type LLMMetadataConfig struct {
	ReasoningContent bool           `json:"reasoning_content"`
	Usage            LLMUsageConfig `json:"usage"`
	FinishReason     bool           `json:"finish_reason"`
}

type LLMUsageConfig struct {
	PromptTokens     bool `json:"prompt_tokens"`
	CompletionTokens bool `json:"completion_tokens"`
	TotalTokens      bool `json:"total_tokens"`
}

type LLMValidationConfig struct {
	Mode          string `json:"mode"`
	ClosedRecords bool   `json:"closed_records"`
	DuplicateKeys string `json:"duplicate_keys"`
	NonFiniteNums string `json:"non_finite_numbers"`
	AttemptScope  string `json:"attempt_scope"`
}

// HTTPConfig performs an HTTP request.
type HTTPConfig struct {
	Method    string           `json:"method"`
	URL       string           `json:"url"`
	Inputs    []Source         `json:"inputs"`
	Body      string           `json:"body,omitempty"`
	Headers   []HTTPKV         `json:"headers,omitempty"` // ordered, author order preserved
	Params    []HTTPKV         `json:"params,omitempty"`  // ordered query parameters
	Exception *ExceptionConfig `json:"exception,omitempty"`
	// SettingTimeoutSec is the http node's own per-attempt carrier
	// (`setting.timeout`, seconds) projected only when Exception.TimeoutAxis
	// is "per_attempt"; it is 0 for every other axis (the total carrier is
	// Exception.TimeoutMS + timeout_axis=total). Kept separate from TimeoutMS
	// so the two axes cannot be stacked by accident (ES-022a/ES-100).
	SettingTimeoutSec int `json:"setting_timeout_sec,omitempty"`
}

// HTTPKV is one ordered HTTP header or query entry. Name is a static field
// name; Value may be a template string rendered by the host node at invoke
// time (the projection carries it verbatim, never pre-rendered).
type HTTPKV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ToolConfig invokes a logical Tool operation.
type ToolConfig struct {
	ToolName   string      `json:"tool_name"`
	ProviderID string      `json:"provider_id"`
	Operation  string      `json:"operation"`
	Params     []ToolParam `json:"params"`
	// NestedRefs carries refs embedded in structured Param values (the
	// frontend encodes value-tree leaves as {ref_node, path}). They are
	// assembled under NestedRefMapKey/<token> and substituted by the tool
	// node at invocation time.
	NestedRefs []Source         `json:"nested_refs,omitempty"`
	Exception  *ExceptionConfig `json:"exception,omitempty"`
}

// Structured tool-argument refs: the frontend emits {path, ref_node} map
// leaves. Spec build tags each leaf with NestedRefMarker=<token> and wires
// its producer through ToolConfig.NestedRefs; AssembleInput stores resolved
// values under NestedRefMapKey, and the tool node substitutes them.
const (
	NestedRefMarker = "__ncoda_ref__"
	NestedRefMapKey = "__ncoda_refs__"
)

// ToolParam is one ordered tool parameter: a scalar literal or a ref.
type ToolParam struct {
	Key   string  `json:"key"`
	Value any     `json:"value"`
	Ref   *Source `json:"ref,omitempty"`
}

// KnowledgeConfig retrieves from datasets (v1 runtime REJECT, D4).
type KnowledgeConfig struct {
	DatasetIDs   []string `json:"dataset_ids"`
	DatasetNames []string `json:"dataset_names"`
	Inputs       []Source `json:"inputs"`
	QueryName    string   `json:"query_name,omitempty"`
}

// AgentConfig invokes a validated agent strategy.
type AgentConfig struct {
	Model       string  `json:"model"`
	Instruction string  `json:"instruction,omitempty"`
	Query       *Source `json:"query,omitempty"`
	// MaxIteration 是工具调用轮次上限（对齐参考目标 max_iteration:该轮次内
	// 模型可带工具执行,之后强制 1 轮无工具收尾;0 → 默认 10;>99 → 99）。
	MaxIteration int `json:"max_iteration,omitempty"`
	// Strategy 是 agent 推理策略（对齐参考目标 AgentEntity.strategy）:
	// "function-calling"（默认,手写循环）或 "react"（eino flow/agent/react,
	// 对齐 coze singleagent）。
	Strategy string `json:"strategy,omitempty"`
	// Memory 开启跨轮会话记忆（对齐参考目标的会话记忆）:
	// 每轮 run() 的历史（user+assistant 文本对）按会话 ID 持久化,下轮
	// 注入指令之后、当前 query 之前。会话 ID 由运行时提供（input map 的
	// conversation_id / CLI -conv）,缺省 "default"。
	Memory bool `json:"memory,omitempty"`
	// MemoryWindow 是记忆窗口（user+assistant 消息对数;<=0 = 不限,
	// 对齐参考目标 memory window 语义 —— v1 用条数窗口而非 token 缓冲）。
	MemoryWindow int              `json:"memory_window,omitempty"`
	Exception    *ExceptionConfig `json:"exception,omitempty"`
	Tools        []AgentToolWire  `json:"tools"`
	// Knowledge 是 agent 数据集检索配置（对齐参考目标 agent-chat 把数据集封装
	// 为检索工具注入 agent）:非 nil 时 agent 额外暴露内置 knowledge_retrieval
	// 工具,模型自主调用检索。nil = 不启用。
	Knowledge *AgentKnowledgeConfig `json:"knowledge,omitempty"`
}

// AgentKnowledgeConfig 配置 agent 内置数据集检索（对齐参考目标
// DatasetRetrieverTool:query + dataset_ids → 命中片段回填）。
type AgentKnowledgeConfig struct {
	DatasetIDs []string `json:"dataset_ids,omitempty"`
	TopK       int      `json:"top_k,omitempty"` // 检索条数上限（0 = 后端默认）
}

// ConversationConfig 是 @mode advanced-chat 的会话历史读取节点配置
// （M13 chatflow）:无字段 —— 从 Deps.Memory 按会话 ID 读历史并渲染为
// 文本（输出端口 "history"）。会话 ID 由运行时提供（input map 的
// conversation_id / CLI -conv）,缺省 "default"。
type ConversationConfig struct{}

// AgentToolWire is the wire identity of one pinned agent tool.
type AgentToolWire struct {
	Alias          string `json:"alias"`
	PluginUniqueID string `json:"plugin_unique_id"`
	ProviderName   string `json:"provider_name"`
	ToolName       string `json:"tool_name"`
	ProviderType   string `json:"provider_type"`
}

// TextConfig is a text operation: concat / split / const.
type TextConfig struct {
	Method     string   `json:"method"`
	Template   string   `json:"template,omitempty"`
	ConcatChar string   `json:"concat_char,omitempty"`
	Separators []string `json:"separators"`
	Inputs     []Source `json:"inputs"`
	Answer     bool     `json:"answer,omitempty"`
}

// ListOpConfig applies one list-operator operation.
type ListOpConfig struct {
	Operation string   `json:"operation"`
	Inputs    []Source `json:"inputs"`
	FilterKey string   `json:"filter_key,omitempty"`
	FilterOp  string   `json:"filter_op,omitempty"`
	FilterVal any      `json:"filter_val"`
	OrderKey  string   `json:"order_key,omitempty"`
	OrderVal  string   `json:"order_val,omitempty"`
	Limit     int      `json:"limit,omitempty"`
}

// CodeConfig executes embedded code (v1: Synthetic shims only; user
// python → RKindRejected/RCodeLanguageUnsupported, D4).
type CodeConfig struct {
	Code     string   `json:"code"`
	Language string   `json:"language"`
	Inputs   []Source `json:"inputs"`
	// Types maps input-port name → declared type（IR CodeRun.Types;runtime
	// 用它做输入形状归一化——桩假数据按声明补齐,见 code.go normalizeInputs）。
	Types    map[string]*TypeSpec `json:"types,omitempty"`
	OutOrder []string             `json:"out_order,omitempty"`
	// OutTypes maps output-port name → declared type, carried verbatim from
	// `ir.CodeRun.OutTypes` (PRD C11 carrier). This stage only transports the
	// ports; consumer-side shaping is P1a-2. Every declared port must survive
	// IR→spec→Normalize or a downstream ref can silently lose its type.
	OutTypes  map[string]*TypeSpec `json:"out_types,omitempty"`
	Condition bool                 `json:"condition,omitempty"`
	Synthetic bool                 `json:"synthetic,omitempty"`
	// KeepWhole: json.parse<Record> 合成 shim 标记（IR JSONSchema 为 object
	// schema）。运行时 normalizeCodeOutput 对单 key dict 的折叠（output=值）
	// 会丢掉 record 层级,破坏 output.<field> dotted ref;keep_whole=true 时
	// output 恒为整体 dict 拷贝。用户 python 与 json.parse<array<T>> shim
	// 不设此标记,保持旧折叠语义（output=单值）。
	KeepWhole bool `json:"keep_whole,omitempty"`
	// Exception 是**节点自带**的错误策略 stanza（`settingOnError`：
	// processType / retryTimes / timeoutMs / …），与 llm / http / tool /
	// agent **同族同载体**（一手：平台导出 6622 处 `timeoutMs`）。
	//
	// 2026-09-29：此前 code 是**唯一**不携带本字段的节点族 ⇒ 声明被静默丢在
	// IR→spec 这一跳，运行期于是用固定 30s 常量替掉平台声明的 60s–600s
	// ——「同一事实两处两种行为」。只携带不代表消费：**两轴都已接** ——
	// 超时轴 = `nodes/code.go::declaredCodeTimeout`（2026-09-29 R-148）、
	// 重试轴 = `declaredCodeRetries`（同日 R-149），判据 =
	// `internal/runtime/compile/code_node_{timeout,retry}_contract_test.go`。
	Exception *ExceptionConfig `json:"exception,omitempty"`
}

// SelectorConfig branches on conditions.
type SelectorConfig struct {
	Conditions []Condition `json:"conditions"`
	Logic      int         `json:"logic"` // 1=or, 2=and
}

// AggregateGroup is one output field of an aggregate. Refs are evaluated in
// order with first-not-null semantics.
type AggregateGroup struct {
	Name string    `json:"name"`
	Refs []Ref     `json:"refs"`
	Type *TypeSpec `json:"type,omitempty"`
}

// AggregateConfig merges branch values. Refs is the legacy single-group form;
// Groups is the field-level form used for hoisted records. LegacyGroupName is
// the port name the legacy group is published under — the aggregate's own
// identity when it has one (a junction phi unit publishes the hoisted
// variable's name), else the platform spelling "Group1". Empty means "Group1".
type AggregateConfig struct {
	Refs            []Ref            `json:"refs"`
	LegacyGroupName string           `json:"legacy_group_name,omitempty"`
	Groups          []AggregateGroup `json:"groups,omitempty"`
}

// AssignConfig writes a variable (conversation or loop state).
type AssignConfig struct {
	VarName   string `json:"var_name"`
	Value     Source `json:"value"`
	Operation string `json:"operation"`
	LoopState bool   `json:"loop_state,omitempty"`
	LoopID    string `json:"loop_id,omitempty"`
}

// QaConfig is a human-input question (v1: loop-external only, D2).
type QaConfig struct {
	Choices  []string  `json:"choices"`
	Inputs   []Source  `json:"inputs"`
	Template string    `json:"template,omitempty"`
	Fields   []QaField `json:"fields"`
	Timeout  int       `json:"timeout,omitempty"`
}

// QaField is one request_input form field.
type QaField struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Options []string `json:"options"`
}

// IntentConfig is the intent-detection node (coze IntentDetector, type 22):
// query classified against declared intents; runtime routes control flow
// along branch_<i> (classificationId == i) or default; classificationId
// (int) / reason (string) are the data outputs.
type SwitchConfig struct {
	Query        Source   `json:"query"`
	Intents      []string `json:"intents"`
	Temperature  *float64 `json:"temperature,omitempty"`
	MaxTokens    int      `json:"max_tokens,omitempty"`
	TopP         *float64 `json:"top_p,omitempty"`
	Mode         string   `json:"mode,omitempty"`
	SystemPrompt string   `json:"system_prompt,omitempty"`
}

// SubWorkflowConfig invokes a declared sub-workflow (SUBWORKFLOW-DECLARATION).
// Identity (workflowId/version/spaceId) comes from the declaration; Inputs
// carry the call-site bindings; OutTypes mirror the declared output contract.
// The runtime resolves sub-workflow execution through Deps.SubWorkflows.
type SubWorkflowConfig struct {
	WorkflowID      string               `json:"workflow_id"`
	WorkflowVersion string               `json:"workflow_version"`
	SpaceID         string               `json:"space_id,omitempty"`
	Inputs          []Source             `json:"inputs"`
	OutTypes        map[string]*TypeSpec `json:"out_types,omitempty"`
}

// OutputConfig emits a value with a template.
type OutputConfig struct {
	Template string `json:"template"`
}

// LoopConfig is the loop container: driver config + nested body subgraph
// (children + inner edges). loop-back arcs (TargetPort==PortLoopInlineInput)
// are carried here so the driver can absorb them; they never become Eino
// edges.
type LoopConfig struct {
	VarName         string               `json:"var_name,omitempty"`
	LoopType        string               `json:"loop_type"`
	LoopCount       int                  `json:"loop_count,omitempty"`
	InputArrays     []string             `json:"input_arrays"`
	Inputs          []Source             `json:"inputs"`
	Types           map[string]*TypeSpec `json:"types,omitempty"`
	StateTypes      map[string]*TypeSpec `json:"state_types,omitempty"`
	StateValues     map[string]Source    `json:"state_values,omitempty"`
	OutTypes        map[string]*TypeSpec `json:"out_types,omitempty"`
	OutputSources   []Source             `json:"output_sources"`
	FlattenOutput   bool                 `json:"flatten_output,omitempty"`
	IsParallel      bool                 `json:"is_parallel,omitempty"`
	ParallelNums    int                  `json:"parallel_nums,omitempty"`
	ErrorHandleMode string               `json:"error_handle_mode,omitempty"`
	Children        []NodeSpec           `json:"children"`
	InnerEdges      []EdgeSpec           `json:"inner_edges"`
	// ReturnValue 是 loop body 内 return 的值源（M17）:非 nil 时 loop 输出
	// 额外携带 returned/return_value 端口,spec.Build 用它在 loop 之后插入
	// 短路分支（returned ? return_value : 正常后续）。
	ReturnValue *Source `json:"return_value,omitempty"`
}

// BreakConfig / ContinueConfig are sinks inside a loop body.
type BreakConfig struct{}
type ContinueConfig struct{}

// LoopReturnConfig terminates the innermost loop carrying a return value
// (M17, 46_loop_escape). Value is the returned value source (iteration
// variable direct ref or materialized expression).
type LoopReturnConfig struct {
	Value Source `json:"value"`
}
