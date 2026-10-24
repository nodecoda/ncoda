// Package ir implements the platform-neutral workflow IR
// (docs/WORKFLOW-IR.md, decisions D1–D6).
//
// The IR expresses flow / dataflow / control-flow only. Configuration
// surfaces (model shapes, provider/model splits, retry policies) travel as
// uninterpreted payloads and are owned by backend mapping tables — the IR
// itself does not know about platform node shapes.
//
// Expression payloads (conditions, templates) are resolved by the builder
// into semantic values; complex expression trees remain an AST-referencing
// payload until the expression-IR layer lands (documented migration point).
package ir

import (
	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/llm"
)

// Graph is the platform-neutral workflow graph (docs/WORKFLOW-IR.md §11).
// Entry and Root are the structured semantic source. Backend graph shape is
// projected on demand and is never stored on Graph.
type Graph struct {
	// Mode is the program's top-level form (@mode workflow|advanced-chat|
	// agent). Carried so spec.v1 can express the runtime semantics (e.g.
	// advanced-chat = workflow graph + conversation memory, M13).
	Mode string
	// Entry is the structured workflow entry. It is graph-level because it
	// precedes the first statement in Root, but it is part of the semantic IR
	// rather than the transitional flat projection.
	Entry *Start
	// Root is the structured program tree (D9). nil for graphs built
	// before the structured lowering landed.
	Root *Block
	// ConversationVars are the program's @conversation declarations
	// (semantic payload; the backend renders its variable table).
	ConversationVars []ConvVar
	// Placements record every native-vs-materialize lowering decision
	// (direction 2 — docs/ideas-todo/PLACEMENT-PASS.md): the builder logs
	// (node, construct, decision, reason) at each placement point so the
	// lowering is inspectable, snapshot-testable and audit-proof. The
	// backend never consumes this list; it is a compile-time record.
	Placements []Placement
	// AllocatedNodeIDs 是 IR 构造期分配过的全部 node id（按分配顺序）——
	// id 空间的身份。产物里不存在的那些 = 我们自己的删除 / 未落地足迹
	// （死节点清理、聚合删除、折叠，以及内联用户函数吃掉的一次性分配……）。
	// 后端在产物出口按这条集合压缩 id 空间，使「同一个语义 ⇒ 同一套 id」
	// 对**全部**删除路径成立，而不是逐个 pass 各自闭合自己的足迹。
	AllocatedNodeIDs []string
}

// Placement records one native-vs-materialize lowering decision. The four
// outcomes mirror PLACEMENT-PASS §1 and describe **what lands in the
// artifact** (not the IR's internal representation): NATIVE (the target's own
// node / native expression carries it), SHIM (it lands as a compiler-generated
// python code node), INLINE (compile-time value folded into a host shim),
// REJECT (explicit compile error — never silent), PASSTHROUGH (user foreign
// code, never rewritten).
//
// 口径（一条，别处不再定义）：决策必须**与产物一致** —— 记录由「目标能力轴 × 载荷」
// 在同一处算出（例：`Builder.recordJSONBuiltinPlacement`），渲染期用**同一份**谓词定
// 落点 ⇒ 两边不可能各说各话（历史现场：`json.*` 的记录恒写「平台没有承载面」，
// 而产物里是平台原生节点）。
type Placement struct {
	NodeID    string // owning node id ("" for decisions not bound to a node)
	Construct string // construct family, e.g. "condition:ref-ref", "calc:len"
	Decision  string // Placement* constant below
	Reason    string // why: contract / capability / purity / no-native-node
}

// Placement decision constants.
const (
	PlacementNative      = "NATIVE"
	PlacementShim        = "SHIM"
	PlacementInline      = "INLINE"
	PlacementReject      = "REJECT"
	PlacementPassthrough = "PASSTHROUGH"
)

// ConvVar is one @conversation declaration: name, type and literal default.
type ConvVar struct {
	Name    string
	Type    *Type
	Default any
}

// Kind identifies a semantic node family.
type Kind string

const (
	KindStart        Kind = "start"
	KindEnd          Kind = "end"
	KindLLM          Kind = "llm"
	KindHTTP         Kind = "http"
	KindAttempt      Kind = "attempt"
	KindTool         Kind = "tool"
	KindAgent        Kind = "agent"
	KindConversation Kind = "conversation" // @mode advanced-chat 会话历史读取（M13）
	KindSubWorkflow  Kind = "sub_workflow"
	KindKnowledge    Kind = "knowledge"
	KindText         Kind = "text"
	KindListOp       Kind = "list_op"
	KindCode         Kind = "code"
	KindSelector     Kind = "selector"
	KindAggregate    Kind = "aggregate"
	KindAssign       Kind = "assign"
	KindQA           Kind = "question_answer"
	KindSwitch       Kind = "switch"
	KindOutput       Kind = "output"
	KindLoop         Kind = "loop"
	KindBreak        Kind = "break"
	KindContinue     Kind = "continue"
	KindLoopReturn   Kind = "loop_return"
)

// Node is one workflow node.
type Node interface {
	Kind() Kind
	NodeID() string
}

// ── dataflow ─────────────────────────────────────────────────────────────

// Ref is a dataflow reference. Output is the producer's output-port name
// (dotted for nested field paths, e.g. "value.city") — the field-path
// semantics of E3 stay in the IR and are never flattened at the frontend;
// backends split the dotted name for their own path systems.
type Ref struct {
	Producer string // upstream node id
	Output   string // producer output port (may be dotted)
}

// Source is one input-port data source: a block-output ref or a literal.
type Source struct {
	Path    []string // input-port path
	BlockID string   // non-empty ⇒ block-output ref
	Name    string   // producer output port
	Val     any      // literal value (when BlockID == "")
}

// Type is the platform-neutral port-type description; backends map it onto
// their own type system (Coze TypeInfo, reference descriptors).
type Type struct {
	Kind     string // "null"|"string"|"integer"|"number"|"boolean"|"list"|"object"|"file"
	Elem     *Type  // list element, or Optional element when Optional is true
	Props    map[string]*Type
	Required bool
	// Optional preserves the language-level T? marker. It is deliberately
	// separate from Required: Required describes a platform port/schema,
	// while Optional describes the semantic value carried by the IR.
	// Lowerings may map the two differently, but the IR must not erase T?.
	Optional bool
	// File constraints are preserved for entry parameters so the reference target can
	// reconstruct its upload configuration without coupling the IR to the reference target.
	AllowedFileTypes         []string
	AllowedFileExtensions    []string
	AllowedFileUploadMethods []string
}

func T(kind string) *Type                  { return &Type{Kind: kind} }
func TList(elem *Type) *Type               { return &Type{Kind: "list", Elem: elem} }
func TObject(props map[string]*Type) *Type { return &Type{Kind: "object", Props: props} }

// Operand is one comparison operand: a ref or a literal.
type Operand struct {
	Ref *Ref
	Val any
}

// Condition is one selector comparison (left op right).
type Condition struct {
	Left     Operand
	Operator int // backend SELECTOR_OPERATORS id (1=eq, 2=neq, 12=false, ...)
	Right    Operand
}

// ConfigPair is an uninterpreted configuration payload (D1/D2). Interpretation
// is owned by the backend mapping table.
type ConfigPair struct {
	Key   string
	Value any
}

// Message is one chat message in a model-call configuration. Content is a
// template string with dataflow refs already resolved to backend template
// syntax ({{block_output_<id>.<port>}}) — references stay live, the IR does
// not own the backend's prompt shape (D2). The json tags make spec.v1
// documents carry lowercase role/content keys, which the runtime llm node
// consumes directly (backend renderers use the Go fields, unaffected).
type Message struct {
	Role    string `json:"role"`    // "system" | "user" | ...
	Content string `json:"content"` // template string with live dataflow refs
	// Bindings 是作者**显式**给出的名额表（content 写成
	// `template "<逐字文本>" using {…}`，R-68 甲）。nil = 内插形态：Content 里
	// 带 `{{block_output_<id>.<port>}}` 保留占位符，由 lowering 合成 node_inputs。
	Bindings []TemplateBinding
}

// ExceptionConfig mirrors retry/timeout/default policy payloads (D1: config
// surface — carried here as a semantic payload, shaped by the backend).
type ExceptionConfig struct {
	MaxRetry        int
	RetryIntervalMS int // retry policy interval (the reference target retry_interval); coze
	// renders timeouts from TimeoutMS, so the two stay distinct
	TimeoutMS   int
	ProcessType *int
	// FailBranch marks an operation lowered from an attempt statement: the
	// backend renders its error strategy as a failure branch (reference target
	// "fail-branch") and exposes error_type/error_message semantic output
	// ports. Platform-neutral: it is the operation's failure branch, not a
	// platform shape.
	FailBranch bool
	// DefaultFields is the failure fallback payload of `with default(<record>)`
	// (meaningful only when ProcessType == 2): the value the operation's result
	// takes when the operation fails. Names are **ncoda semantic result field
	// names** (platform-neutral — the backend maps them onto its physical
	// output ports); values are static literals.
	//
	// ProcessType == 2 with DefaultFields == nil means the policy exists but
	// cannot be carried statically (a non-literal value), or the IR was built
	// before this field landed: backends must fail loud rather than emit the
	// node without its failure policy (silently turning an absorbed failure
	// into a flow abort) — see AGENTS.md「根因优先原则」and
	// docs/ideas-todo/2026-09-10-ncoda-error-handling-semantics-and-
	// bidirectional-design.md §10/§11.
	DefaultFields []DefaultField
}

// DefaultField is one entry of a `with default(<record>)` failure payload:
// FieldName is the ncoda semantic result field, Value a static literal
// (string/int64/float64/bool, or nested map[string]any / []any).
type DefaultField struct {
	Name  string
	Value any
}

// ── node types ───────────────────────────────────────────────────────────

// Param is an entry input parameter.
type Param struct {
	Name     string
	Type     *Type
	Optional bool
	Default  any // literal default value when Optional (config surface, D1)
}

// Start is the workflow entry.
type Start struct {
	ID     string
	Params []Param
}

// FlowExitDefaultKey 是「无名流程出口」的语言默认键 —— **别名**，唯一声明点在
// 语言包 `pkg/ast.FlowExitDefaultKey`（理由与语料数字见那里）。
//
// 这里保留别名而不是重新声明：IR 的出口归一化（本包 ports.go 的
// `soleDefaultKeyedExit`）与类型检查（internal/passes）、raising 读回
// （platforms/cozebiz/decompile）必须读同一个名字，否则「糖 ≡ 显式」
// （`return { $output: x };` ≡ `return x;`）会在某一侧分叉（A12-H20）。
const FlowExitDefaultKey = ast.FlowExitDefaultKey

// EndAnswer marks the terminal End as an answer template (coze-biz
// useAnswerContent terminate plan): the workflow's final response is a
// rendered template whose {{name}} placeholders bind to producer refs. A
// template without bindings is a pure literal answer (the end carries no
// node_inputs).
type EndAnswer struct {
	Template string
	Bindings []EndAnswerBinding
	// Streaming = 答复端点的**投递开关**（`return template "…" streaming;`，用户
	// 2026-09-27 裁决 甲）：平台侧 End 的 `streamingOutput`。false = 平台默认值。
	Streaming bool
}

// TemplateBinding binds one {{name}} template placeholder to a producer ref
// (W2 名额表：模板文本里的名字 → 生产者引用)。R-67/R-68 甲 起由三个位点共用 ——
// 答复端点（EndAnswer）/ `output template` / llm message content。
type TemplateBinding struct {
	Name string
	Ref  Ref
	// Type = **被引用值的语言类型**（nil = 取不到）。CB-10：答复端点名额行的
	// `input.type` **被平台运行期读**（coze-studio `SetInputsForNodeSchema` →
	// `NodeSchema.InputTypes` → `preTypeConverter`：缺失/空值按声明类型填零、
	// 字符串按声明类型归一为数组）⇒ 它是**语义**，不是保真注解。
	//
	// 权威 = 语言侧「生产者口类型」（`Builder.portTypeOf`，全仓唯一）。由
	// `stampEndPortTypes` 在建图收尾**一处**填；渲染器**只读**：取不到就**不
	// 发射** `type`，**绝不虚构 `string`**（无权威的发射 = 语义篡改，CB-10）。
	//
	// 不参与身份比较（`sameEndAnswer` 只比 Name/Ref）——Type 是 Ref 的函数。
	Type *Type
}

// EndAnswerBinding 是 TemplateBinding 的历史别名（语义一字未改）。
type EndAnswerBinding = TemplateBinding

// EndOutput is one explicit output("name", value) mapping on a continuing
// End node (legacy translateOutput: the end carries the named variable).
type EndOutput struct {
	Name string
	Ref  Ref
}

// End terminates the workflow with the given value refs. Outputs carries
// explicit output("name", value) mappings; Inputs feeds the canonical
// terminal end.
type End struct {
	ID      string
	Inputs  []Ref
	Outputs []EndOutput
	// NamedReturn is a terminal record/map return. It uses named outputs like
	// output(...) nodes but retains terminal End identity and mint order.
	NamedReturn bool
	// Implicit marks the terminal end synthesized from a tail value of a
	// function that never returns (no return type, no explicit branch
	// return). The reference backend drops it (the legacy emits no end for a
	// bare void body); the coze backend keeps the 900001 exit its checker
	// requires.
	Implicit bool
	// Answer marks the terminal end as an answer template (coze-biz
	// useAnswerContent); nil keeps the plain returnVariables shape.
	Answer *EndAnswer
	// FlowExitMerge is the flow-exit phi: when the main flow has TWO or more
	// value-carrying exits (`return v` at top level or inside an if/intent/
	// parallel arm), the platform `end` node — which carries exactly ONE
	// value binding — cannot hold them all. The exits converge here and only
	// this merge feeds the end (real coze-biz exports: merge(G1:[A,B]) → end;
	// end.node_inputs = [{path: Group1, ref_node: merge}]). Backends route
	// every wire that used to enter this End into the merge instead, and wire
	// the merge into the End. Void exits (bare `return;`, side-effect-only
	// tails) contribute no candidate and keep entering the End directly
	// (user ruling 2026-09-12: "仅副作用、没有值就可以直连 end").
	// nil ⇒ the End is bound directly (zero or one value exit).
	FlowExitMerge *Aggregate
	// PortTypes = 端点两通道（Inputs / Outputs）每条绑定**被引用值的语言类型**，
	// 键 = 该条绑定的 Ref（CB-10；答复名额表那一侧是 TemplateBinding.Type）。
	// 语义同 TemplateBinding.Type：平台的 `input.type` 被运行期读 ⇒ 按语言类型
	// 投影发射；缺项 / 值为 nil ⇒ **不发射** `type`（不虚构）。
	//
	// 由 `stampEndPortTypes` 在建图收尾**一处**填（End 的绑定点有 7 处构造 /
	// 改写：ensureEnd / resolvePendingElse / materializeFlowExitPhi /
	// canonicalizeFlowExitGroupName …，在各构造点各写一遍 = 同一规则 N 处实现）。
	PortTypes map[Ref]*Type
}

// LLMCall invokes a model with uninterpreted configuration. Model is the
// provider/name string; backends split it (Coze modelType/modelName) or map
// it (reference model config).
type LLMCall struct {
	ID             string
	Model          string
	Config         []ConfigPair // uninterpreted config payloads (D1/D2)
	Inputs         []Source
	OutTypes       map[string]*Type
	OutputFormat   string
	ResponseFormat int
	Exception      *ExceptionConfig
	Extraction     *ExtractionSpec
	// Result is the language-level LLMResponse<T> contract. OutTypes remains
	// a transitional wire declaration used only by target lowering; it is not
	// the authority for semantic payload typing.
	Result *LLMResultContract
}

type LLMResultContract struct {
	PayloadSchema *llm.LLMSchema
	Metadata      LLMMetadataContract
	Validation    LLMValidationContract
	// WholeEnvelopeConsumed is derived from semantic response refs after IR
	// construction. Backends use it to decide whether their target-local
	// envelope assembly adapter is required.
	WholeEnvelopeConsumed bool
}

// LLMEnvelopeOutput is the semantic ref for the complete LLMResponse<T>.
// It is deliberately not a target port name.
const LLMEnvelopeOutput = "response"

// LLMPayloadOutput is the semantic ref prefix for the payload (`text: T`)
// **and everything below it** (`text.<record field>…`).  It is the one place
// that names the payload port for the member-chain/port-projection rule
// "首段是 payload 口 ⇒ 成员活在口下方，路径继续下钻"（三处同族判据：
// pkg/ir/build_return.go 的 aggregateFieldPort、internal/backend/cozebiz
// 的 rewriteLLMPhysicalValue 与 render_loop 的端口映射）。
const LLMPayloadOutput = "text"

// ExtractValuePort 是 `extract<T>` 记录载荷口的**规范名** —— 它同时是：
//
//	· 语言侧结果字段名（`Extraction[T].value`，ExtractionResultType 的字段之一）；
//	· coze-biz 产物里承载该载荷的物理口名（node_outputs.value，见
//	  internal/backend/cozebiz::ExtractValuePort，那边是同一值的别名）。
//
// 一名的两面是**语言事实**（口名 = 字段名），不是平台巧合，故定义在这一层。
const ExtractValuePort = "value"

// extractPayloadKind 是「`extract<T>` 的**载荷记录 T**」绑定的 varRef kind，
// 与调用值 kind `"extract"`（整个 `Extraction[T]`）刻意分开 —— **编码必须单射**
// （2026-09-26 一手 R-108 家族）：`e.value`（载荷记录）与 `e`（整个 Extraction[T]）
// 在旧编码下同形（都是 `{kind:"extract", output:"value"}`）⇒ 下游无法区分
// 「在载荷口下方下钻」（`p.city` 应为 `value.city`）与「取信封字段」
// （`e.value` 应为 `value`）。一手反例（本仓最小形，修前实测）：
//
//	out = e.value;            // if 臂内：载荷记录绑到外层变量
//	                          // ⇒ 逐字段 phi 候选退化成裸字段口
//	ref 100004.city does not resolve: node 100004 declares ports [value]
//
// 成员路径分派见 build.go（成员链）与 aggregateFieldPort（出口汇聚），两处同尺。
const extractPayloadKind = "extract-payload"

type LLMMetadataContract struct {
	ReasoningContent bool
	Usage            bool
	FinishReason     bool
}

type LLMValidationContract struct {
	Mode         string
	ClosedRecord bool
	AttemptScope string
}

// ExtractionSpec is the target-neutral schema extraction contract attached
// to an LLMCall. Backends without a native extractor can keep rendering the
// model call; The reference target expands it into its parameter-extractor plus result adapter.
type ExtractionSpec struct {
	Instruction string
	Strategy    string
	Fields      []ExtractionField
	MemoryTurns *int
	Vision      *Source
}

type ExtractionField struct {
	Name        string
	Type        *Type
	GraphonType string
	Description string
	Options     []string
}

// HTTPHeader is one request header entry: a literal value or a
// {{block_output_<id>.<port>}} template (ref-backed values render as the
// platform's name-keyed template + node_inputs binding, same as Body).
type HTTPHeader struct {
	Name  string
	Value string
}

// HTTPParam is one query-parameter entry. It is the same template slot as
// HTTPHeader (`{name: value}`, value may hold a {{block_output_…}} placeholder)
// because the platform reads both channels through the same `vo.Param` shape
// (`{name, input:{type,value}}`): headers from `inputs.Headers`, query from
// `inputs.Params` (coze-studio httprequester/http_requester.go:428). Order is
// the author's order, never a map order.
type HTTPParam struct {
	Name  string
	Value string
}

// HTTPCall performs an HTTP request.
type HTTPCall struct {
	ID     string
	Method string
	URL    string
	Inputs []Source
	// Body carries the RAW_TEXT body template ({{block_output_<id>.<port>}}
	// placeholders); empty means no body. Literal fragments live in the
	// template text; every placeholder has a matching Source in Inputs.
	Body      string
	Headers   []HTTPHeader
	Params    []HTTPParam
	Exception *ExceptionConfig
}

// ToolParam is one ordered tool parameter: either a scalar literal (Value)
// or a dataflow ref (Ref) resolved at render time.
type ToolParam struct {
	Key   string
	Value any
	Ref   *Source
}

// ToolCall invokes a logical Tool operation. Target backends resolve physical
// provider/plugin identifiers from their binding manifests.
type ToolCall struct {
	ID         string
	ToolName   string      // emitted tool name (semantic)
	ProviderID string      // provider/registered name (semantic)
	Operation  string      // logical operation name (semantic)
	Params     []ToolParam // ordered parameters (semantic)
	Exception  *ExceptionConfig
	// ResultType 是**声明端口表**：能力注册表（collector.db 导出的 output 表）
	// 经 TypePass 结论带入 IR 的结果记录，字段名 = **物理口名**（`output` /
	// `data.table_id` / `code` / …，与 wire 的 node_outputs 逐字同源）。
	//
	// 为什么必须挂在节点上（M2 残余①）：ToolCall 的端口类型此前在 IR 侧没有
	// 权威，`portTypeOf` 只能给「结构化但无 schema 的记录」这一 open-world
	// 占位（TObject(∅)）。占位一旦被**声明出去**（例：臂门控物化的恒等载体把
	// 它当成自己的 node_outputs）就把「我们不知道」写成了产物事实，与平台
	// 对同一产出口的真实声明冲突 ⇒ 类型在 wire 上被改写、往返不再是不动点。
	// nil = 能力未声明输出（open-world），调用方按既有保守形态处理。
	//
	// 消费点（乙，2026-09-24）：`declaredPortType`（`junction_phi.go`）把它当
	// φ 槽类型权威链的 **③ 级**读 —— 与 `*CodeRun` 同解（先查直口、再按 Props
	// 解 dotted 成员）。此前这一支不读它 ⇒ 插件端口在 ③ 级无权威 ⇒ 形状先被甲
	// 拒收（R-27）、后由乙接通编对（R-28）。
	ResultType *Type
}

// SubWorkflowCall invokes an external subworkflow by its platform identity
// (workflowId/version). The call is lowered from a subflow declaration
// reference (SUBWORKFLOW-DECLARATION). Inputs carry the binding map literal
// entries as dataflow refs; OutTypes are the declared output record fields.
type SubWorkflowCall struct {
	ID              string
	WorkflowID      string
	WorkflowVersion string
	SpaceID         string
	Inputs          []Source
	// InFields is the **declared** input contract, in declaration order:
	// the subworkflow's own ports. `Inputs` only carries the call-site
	// bindings (values); re-deriving the contract from them loses the
	// declared types and any field the caller did not pass (D-2, 2026-09-14).
	InFields  []SubflowField
	OutTypes  map[string]*Type
	Exception *ExceptionConfig
}

// SubflowField is one declared subflow contract field (input side).
type SubflowField struct {
	Name string
	Type *Type
}

func (n *SubWorkflowCall) Kind() Kind     { return KindSubWorkflow }
func (n *SubWorkflowCall) NodeID() string { return n.ID }

// KnowledgeCall retrieves from datasets by symbolic name (backend maps ids).
type KnowledgeCall struct {
	ID           string
	DatasetIDs   []string // platform placeholder ids (coze mapping)
	DatasetNames []string // symbolic dataset names (semantic payload)
	Inputs       []Source
	// QueryName is the declared query variable name (bound-stdlib steps
	// declare the query parameter, e.g. rag_answer's "query"); empty means
	// the query is a bare value reference (direct knowledge() calls).
	QueryName string
}

// AgentCall invokes a validated agent strategy (agent(...) with a pinned
// target profile). The IR carries both the semantic contract payload and
// the resolved target-profile wire identity (StrategyName/ProviderName/
// StrategyLabel/PluginUniqueIdentifier + per-tool pinning); the backend
// renders them directly (reference target: AgentDataNode v2). The profile is resolved
// at IR build time from the pipeline-published AgentProfiles face.
type AgentCall struct {
	ID           string
	Model        string // provider/name model string (profile-pinned)
	Instruction  string // pinned instruction text
	Query        *Source
	Strategy     string // agent reasoning strategy ("" = function-calling)
	MaxIteration int    // tool-round cap (@mode agent @agent block; 0 = runtime default)
	Memory       bool   // cross-turn conversation memory (@mode agent @agent block)
	MemoryWindow int    // memory message-pair window (0 = unlimited)
	Exception    *ExceptionConfig
	// KnowledgeDatasetIDs / KnowledgeTopK:@mode agent 数据集检索配置
	// （对齐参考目标 agent-chat DatasetRetrieverTool 注入）。空 = 不启用。
	KnowledgeDatasetIDs []string
	KnowledgeTopK       int

	// Resolved target-profile wire identity (AgentTargetProfile fields).
	StrategyName         string
	StrategyProviderName string
	StrategyLabel        string
	PluginUniqueID       string
	// Tools maps each validated tool alias to its pinned wire identity
	// (AgentToolTargetProfile fields).
	Tools []AgentToolWire
}

// HistoryCall reads the current conversation's rendered history
// (@mode advanced-chat, M13 chatflow semantics). Output port: "history"
// (string; empty when no memory store or empty conversation).
type HistoryCall struct {
	ID string
}

// Kind implements Node.
func (h *HistoryCall) Kind() Kind { return KindConversation }

// NodeID implements Node.
func (h *HistoryCall) NodeID() string { return h.ID }

// AgentToolWire is the wire identity of one pinned agent tool: the plugin
// identity and provider/tool/type triple from AgentToolTargetProfile.
type AgentToolWire struct {
	Alias          string
	PluginUniqueID string
	ProviderName   string
	ToolName       string
	ProviderType   string
}

// TextProcess is a text operation: concat (template + ref bindings), split,
// or a constant string.
type TextProcess struct {
	ID         string
	Method     string // "concat" | "split" | "const"
	Template   string
	ConcatChar string
	Separators []string
	Inputs     []Source
	Answer     bool // direct return of a template literal (reference target: answer node)
}

// CodeRun executes embedded code with bound inputs. Types maps the input
// binding names onto their declared types; OutTypes maps the code node's
// declared output ports (a single "result" for direct outputs, one entry
// per field for structural outputs). Condition marks a condition
// materializer (a non-direct if-condition lowered to a boolean code
// result); backends shape its id/title like the legacy makeInternalCodeNode
// "condition" prefix.
type CodeRun struct {
	ID        string
	Code      string
	Language  string
	Inputs    []Source
	Types     map[string]*Type
	OutTypes  map[string]*Type
	OutOrder  []string // output-port declaration order (structural outputs)
	Condition bool
	// Synthetic marks a compiler-generated shim CodeRun (object/array/
	// const/increment/map lowering) as opposed to a user-written foreign
	// code block. Target-specific optimizations (e.g. coze JsonSerialization
	// lowering) may rewrite Synthetic nodes; user-written code is always
	// passed through untouched.
	Synthetic bool
	// Exception carries the caller-site error policy (`with retry/timeout/
	// default`) attached to a *user-written* foreign code expression
	// (CodeRunner). The platform node family physically carries the same
	// settingOnError stanza as plugin/http nodes (coze-studio OSS
	// vo/canvas.go:590-620), so the policy is representable; synthetic shims
	// (Synthetic=true) never carry one (the compiler builds them).
	Exception *ExceptionConfig
	// JSONSchema carries the resolved schema type for json.parse<T> shims
	// (G11/G12): the full record type for json.parse<Record>, or the list
	// element type for json.parse<array<T>>. Element access (items[i] /
	// resp.field[i], G12-2) resolves the element record shape from it so
	// the extraction shim can declare per-field outputs with typed
	// defaults. Non-json shims leave it nil.
	JSONSchema *Type
	// ZeroOf 是**溯源标注**：本节点的每个输出端口都承载 ZeroOf 的类型零值
	// （record 逐字段递归到零）。由唯一构造点设置（literal-zero shim 构造器），
	// 不是形态守卫，也**不由优化器解析 python 文本来推断**。消费点 =
	// pkg/ir/fold_zero_identity_candidates.go（汇聚候选的零值重定向）。
	ZeroOf *Type
	// ZeroFields 是**溯源标注**：本节点的 record 口（`OutTypes["result"]`）的
	// 这些**成员口**承载**作者写的类型零字面量**（`{ "a": 0, "b": "y" }` 里的
	// `a`）。与 ZeroOf 同族、同在字面量构造点设置，**不由优化器反推**：
	// ZeroOf 回答「整节点是不是类型零」，本标注回答「哪些成员口是类型零」——
	// 平台 merge 逐字段取候选，故「本组有没有作者写的零值」这个门控判据必须能
	// 看见**字段级**的零（一手：`RC r = {"a":k,"b":"x"}; if (q=="a") { if
	// (k>3) { r = {"a":0,"b":"y"}; } else { } } else { } return r.a;` —— 记录
	// 字面量整值非零、字段 `a` 是零；看不见它 ⇒ 臂外非零基座在零写路径上胜出
	// ⇒ 作者的零被静默丢掉）。消费点 = `refValueProvablyZero`
	// （fold_zero_identity_candidates.go）⇒ 臂门控与基座门控条件。
	ZeroFields []string
	// Identity 是**溯源标注**：本节点的 `result` 端口**逐字复制**它的唯一输入
	// （一个形参加一个 return）。只在恒等转发 relay 的两个构造点设置
	// （scalarBranchDefault / materializeBranchDefault）。
	//
	// 使用限制：只允许用于「零值性传播」——非零源的 relay 在臂控制路径上的
	// 位置是**语义承载**的（scalarBranchDefault 注释：merge 不得在错误路径上
	// 看到 junction 前的绑定），禁止把 Identity 用于一般折叠。
	Identity bool
}

// ListOp applies one reference list-operator operation to a collection. Each
// operation lowers to one list-operator node (filter/sort/sort_by/take);
// chained operations connect source → op → next op via dataflow edges.
// map is NOT a list-operator in the legacy backend and stays a CodeRun.
type ListOp struct {
	ID        string
	Operation string // "filter" | "sort" | "sort_by" | "take"
	Inputs    []Source
	FilterKey string // filter_by condition key ("" for bare elements)
	FilterOp  string // comparison operator ("contains", "is", "=", ...)
	FilterVal any    // compared literal
	OrderKey  string // order_by key ("" for bare sort)
	OrderVal  string // "asc" | "desc"
	Limit     int    // take size
	ItemType  *Type  // collection element type (item_var_type)
	// ResultType is the contract of the node's whole-value port (`output`):
	// take/filter/sort are collection-type preserving, so it is the source
	// collection's type. It is stamped by the builder from the authoritative
	// type conclusion (TypePass) or the resolved source port — never forged.
	// nil = contract unknown (consumers then fail loud instead of guessing).
	ResultType *Type
}

func (n *ListOp) Kind() Kind { return KindListOp }

// Selector branches on conditions (backend shapes the branch ports).
type Selector struct {
	ID         string
	Conditions []Condition
	Logic      int // 1=or, 2=and
}

// AggregateGroup is one output port (phi) of an Aggregate — the platform
// variable_merge renders one group per output port; the group name is the
// port / record field name (real coze-biz exports name groups semantically,
// e.g. (title, author, contentList) — X212). Refs are the per-path
// candidate refs (platform first-not-null over candidates; the
// non-executed path's candidate is null at runtime). Contract:
// docs/ideas-todo/COPY-REFERENCE-SEMANTICS.md R1/R3 — candidates must
// name REAL producer ports; convergence unit = the consumed port/field,
// never a whole-record pseudo-port.
type AggregateGroup struct {
	Name string
	Refs []Ref
	Type *Type
	// Owner is the hoisted variable whose phi this group converges.  The
	// convergence unit of one junction is the ncoda variable (family3/X4,
	// 2026-09-09): scalar variables own one group each (group name == the
	// variable's port name), a record variable owns one group per declared
	// field (group names == field names).  Dead-scaffolding removal in
	// optimizeGraph prunes at owner granularity — a record field group is
	// kept when any other group of the same record variable is consumed, so
	// a partially-consumed record keeps its full declared field set on the
	// platform merge (the reference target variable-aggregator lock), while an entirely
	// unconsumed carrier variable (mirror group chain, X4/X12/X20) is
	// dropped.  Empty Owner means legacy/unannotated: pruning falls back to
	// per-port granularity.
	Owner string
}

// Aggregate merges branch values. Refs is the legacy single-group form
// (one "Group1" output, first-not-null over Refs). Groups is the
// multi-port phi form (one output port per group, name = port / field).
// Renderers and runtime prefer Groups when non-empty.
type Aggregate struct {
	ID string
	// Refs is the legacy single-group form.  RefName is the **wire port name**
	// that group declares.
	//
	// 组名 = 汇聚单元的身份，而汇聚单元的候选个数（1 个还是 N 个）不是身份的
	// 一部分：单候选标量 phi 单元的身份就是它承载的那个提升变量，所以它的组名
	// 与多组通道的逐组名来源**同源**（组名 = 变量名）。空值 = 该汇聚没有变量
	// 身份（流程出口汇聚 / 循环回值物化），此时用平台对「无名组」的拼写
	// `Group<k>`（平台节点编辑器新增组的默认命名族），`k` = 本流程**最小可用
	// 序号**、且**同一流程内不得重名**（名字是单元身份的唯一载体 A13-H2；重名 ⇒
	// 反编译必逃逸一个 ⇒ 一轮不动点破裂 `I1b`，R-85）。细则：作者写过的
	// `Group<数字>` 标识符先占位（作者名优先，不夺名）；号按**上线（IR 优化后
	// 幸存）**的单元在创建序上分配 —— 先物化、随后被剪掉的死脚手架占过的号
	// 释放给出口汇聚单元（见 `unnamed_group_name.go`）。
	//
	// 为什么必须分开：平台 wire 里 `mergeGroups[].name` 是**读回侧的采纳依据**
	// （raising A13-H2）；legacy 通道硬编码 `Group1` 时，作者名在单候选形状上
	// 无法往返（c0 名 ≠ c1 名 ⇒ I1b 违反，实测族 1）。见
	// docs/five-step/debt-classes.md §3。
	RefName string
	Refs    []Ref            // legacy single group (port name = RefName / "Group1")
	Groups  []AggregateGroup // multi-port phi (per record field)
	// ValueSources records the whole-value refs the junction's arms produced
	// and this unit absorbed (value channel: 值位置三元 / 无 else 尾值 /
	// switch·QA·attempt 臂值 / 主流程出口 phi).  When the value is a record it
	// is carried **per field**, so the recorded whole-value refs are no longer
	// candidates and `AllRefs()` cannot see them — yet they ARE consumed by
	// this unit, and the flow-exit coverage rule must know it (otherwise the
	// same value is registered as an exit twice and the End reads a raw
	// producer, silently dropping the other arm: 2026-09-14 实测).
	// Legacy single-group units leave it empty: `Refs` covers them.
	ValueSources []Ref
}

// LegacySingleGroupUnit reports whether this unit uses the **legacy single-group
// encoding** (`Refs`, one port) rather than the per-name group encoding
// (`Groups`, one port per name). 它是「谁该拿平台无名组拼写 `Group<k>`」（`RefName
// == ""` 的 legacy 单元）这半句里的**编码那一半**（R-85）。
//
// 判据 = **编码**，不是口名 / 宿主 / 节点类型；它与 `SingleGroupName()` /
// `AllRefs()` 的回落条件**是同一件事**，所以定义在这里。**一处定义**：分配器
// （`Builder.nameUnnamedConvergenceUnit`）、上线后重排
// （`canonicalizeFlowExitGroupName`）、完备性判据（`assertUnnamedUnitsAreNamed`）
// 全部调它 —— 谁都不许再手写一遍这个判别（R-85 收口前是三处副本）。
func (a *Aggregate) LegacySingleGroupUnit() bool {
	//@guard-exempt: 这是**编码判别**（legacy 单组通道 vs 具名组通道），不是
	// 「仅当某形态/宿主/口名才…」的补丁守卫 —— 它替换掉的正是三处手写副本。
	// 事由 + 日期 = R-85 收口（2026-09-25，`docs/five-step/residuals.yaml#R-85`）。
	// 退出条件 = `Refs` / `Groups` 两种编码合并成一种（届时本方法随之消失）。
	return a != nil && len(a.Groups) == 0 && len(a.Refs) > 0
}

// SingleGroupName reports the wire port name of the legacy single-group
// channel: the aggregate's own identity when it has one (a phi unit carries
// the hoisted variable's name), else the platform's unnamed-group spelling.
func (a *Aggregate) SingleGroupName() string {
	if a != nil && a.RefName != "" {
		return a.RefName
	}
	// 平台无名组拼写的**第一号**：与分配器同源（`unnamedGroupPrefix`）——
	// 分配器把最小可用序号发出去，未指定时即 1。两个拼写不得各写一份。
	return unnamedGroupPrefix + "1"
}

// SingleGroupRef is the data reference to the legacy single-group channel's
// declared port ({aggregate, SingleGroupName()}).
func (a *Aggregate) SingleGroupRef() Ref {
	return Ref{Producer: a.ID, Output: a.SingleGroupName()}
}

// TailValueRef reports the data reference this junction hands to its enclosing
// consumer — the synthesized tail End, a parent junction's candidate, a loop
// output slot — or nil when the junction has **no value**.
//
// 规则只有一条（无形态守卫）：「零候选 junction（`Groups` 与 `Refs` 皆空）
// 没有值」——「没有值」不是「零值的值」；具名组形态（`Groups > 0`）也不带
// 尾值（平台 merge 只声明各组成员口，不声明合成 Group1）。
//
// 判据**一处定义**：`junctionTailRef`（本包内部形态；builder 与优化器消费）。
// 本方法是它对**包外**（渲染器 / 后端）的适配，供消费者直接取绑定值。
// 包外消费点**不得**重新判定（例如手写 `len(Exit.Groups) == 0`）——那正是
// 同一规则两份实现、其中一份缺 `Refs > 0` 的复发形态（2026-09-23 N1 收口前：
// `internal/backend/cozebiz/（商业仓）render_stmt.go` 的 attempt flow-tail）。
func (a *Aggregate) TailValueRef() *Ref {
	vr := junctionTailRef(a)
	if vr == nil {
		return nil
	}
	return &Ref{Producer: vr.nodeID, Output: vr.output}
}

// AllRefs flattens every candidate ref (single-group Refs or all groups).
func (a *Aggregate) AllRefs() []Ref {
	if len(a.Groups) == 0 {
		return a.Refs
	}
	var out []Ref
	for _, g := range a.Groups {
		out = append(out, g.Refs...)
	}
	return out
}

// Assign writes a variable (conversation or loop state). Operation is the
// raw assignment operator ("set", "+=", "<<", ...); the backend maps it
// onto its own assigner operations.
type Assign struct {
	ID        string
	VarName   string
	Value     Source
	Operation string
	LoopState bool   // false=VariableAssigner, true=VariableAssignerWithinLoop
	LoopID    string // owning loop container (loop-state assigns)
	// ExtraPairs carries the additional (variable, value) writes performed by
	// the SAME physical assigner node: the platform's loop-state assigner
	// (`set_variable`) holds N (left,right) pairs in one node (语料 13 个
	// set_variable：9 个 1 对 / 1 个 2 对 / 3 个 3 对），and the loop-return
	// rewrite (`loop_return_materialize.go`) writes the escape flag and its
	// value **atomically** in one node so raising folds them back as ONE
	// site. VarName/Value stay the FIRST pair (every existing writer keeps
	// working unchanged); readers must go through AllPairs.
	//
	// Writer is target-gated: only the coze-biz loop-return materialization
	// sets this (see materializeLoopReturns). Any future writer must also
	// teach its target's renderer to emit the pairs — a renderer that only
	// reads VarName/Value would silently drop the rest.
	ExtraPairs []AssignPair
}

// AssignPair is one (variable, value) write of an assigner node.
type AssignPair struct {
	VarName string
	Value   Source
}

// AllPairs returns every write of the assigner node in wire order — the
// single source of truth for renderers (VarName/Value first, then the
// extra pairs).
func (a *Assign) AllPairs() []AssignPair {
	out := make([]AssignPair, 0, 1+len(a.ExtraPairs))
	out = append(out, AssignPair{VarName: a.VarName, Value: a.Value})
	out = append(out, a.ExtraPairs...)
	return out
}

// QA is a human-input question with fixed choices and structured outputs.
// QaField is one request_input form field: the widget kind plus, for
// select widgets, the declared option strings (semantic payload — the
// backend shapes it into its platform field widget).
type QaField struct {
	Name    string
	Kind    string // "paragraph" | "select"
	Options []string
}

// QaAction is one request_input action button (semantic payload).
type QaAction struct {
	Member string
	Title  string
	Style  string
}

// QA is a human-input question with fixed choices and structured outputs.
// Fields/Actions/Timeout are the semantic (platform-neutral) payload of the
// request_input contract; the backend maps them onto its own widgets.
type QA struct {
	ID       string
	Choices  []string
	OutTypes map[string]*Type
	Inputs   []Source
	Template string // question content template
	// Bindings = 作者名额表（`ask<F>({content: template "…" using {k: v}})`）。
	// 平台侧 `question` 文本里的 `{{name}}` 按 `node_inputs[].name` 解析 ⇒ 这张表
	// 是模板变量的唯一载体，**必须**回到产物的 node_inputs（否则占位符悬空）。
	Bindings []TemplateBinding
	// TextForm = **无动作形态**（S1，`ask<Form>({content, fields}) as r;`）：
	// 没有选项、没有分支口 ⇒ 平台 `answer_type: text` + **单条无标签推进口**。
	// 判据来自 AST（`RequestInputStmt.ActionType() == nil`），不是「选项表为空」
	// （空选项表是畸形，不是这一格）。
	TextForm    bool
	Fields      []QaField
	Actions     []QaAction
	Timeout     int
	TimeoutUnit string
	// ── coze-biz `question` 参数面 ②③（2026-10-10 用户批准「丙·扩语言」）──
	// 平台这三个键**原生存在**（`Adapt`/`SimpleLLMParam` 逐字读它们），语言面无
	// 它们就等于往返丢一整个「从作答抽结构化输出」特征 ⇒ 加槽承载。
	//
	// MaxAnswerCount = 平台 `limit`（`Adapt`: `c.MaxAnswerCount = qaConf.Limit`；
	// 消费点只在 `needExtractFromAnswer` 下）。0 = 缺席（与平台 `omitempty` 同义）。
	MaxAnswerCount int
	// ExtractFromAnswer = 平台 `extra_output`。false = 缺席。为真时平台**要求**
	// 模型（`LLM != nil` —— 见 binding 构造器不变量）。
	ExtractFromAnswer bool
	// LLM = 平台 `llmParam`（节点自带模型配置，扁平标量表）。nil = 该节点没有这个键。
	LLM *QaLlm
	// DynamicOptions = 平台 `option_type: dynamic` 的选项来源（`dynamic_option`
	// 引用身份）。nil = 静态选项（`options`）或文本型。非 nil ⇒ `renderQa` 发
	// `option_type: dynamic` + `dynamic_option: <ref>`，端口 = `branch_0 [+ default]`。
	DynamicOptions *Source
}

// QaLlm 是 QA 宿主节点自带的模型配置（平台 coze-biz `question` 的 `llmParam`，
// 平台类型 `SimpleLLMParam` = **扁平标量表**）。IR 不依赖 frontend 包，故按
// `QaField` / `QaAction` 先例在这里重复声明一份（与 `binding.HumanInputLLM` 字段
// 一一对应）。`Model` 保持**协议串原文**（`"coze:<n>"`）—— 把它翻成平台的整数
// `modelType` 是**目标层**的事（`internal/backend/cozebiz/（商业仓）cozemodel`），语言层不认
// 平台协议（判据 = internal/archgate 的语言内核轴）。
//
// **指针 = presence**：nil = 平台导出没写这个键（语言面也不写）。
type QaLlm struct {
	Model               *string  // wire `modelType` 的协议串载体（`"coze:<n>"`；后端剥前缀）
	ModelName           *string  // wire `modelName`
	Temperature         *float64 // wire `temperature`
	MaxTokens           *int     // wire `maxTokens`
	TopP                *float64 // wire `topP`
	ResponseFormat      *int     // wire `responseFormat`
	GenerationDiversity *string  // wire `generationDiversity`
	SystemPrompt        *string  // wire `systemPrompt`
}

// Output emits a value with a template.
type Output struct {
	ID       string
	Template string
	// Bindings 是作者**显式**给出的名额表（`output template "<逐字文本>" using {…}`，
	// R-67 甲）。nil = 内插形态：Template 里带 `{{block_output_<id>.<port>}}` 保留
	// 占位符，由 lowering 的 rewriteTemplateRefs 合成 node_inputs。
	Bindings []TemplateBinding
	// Streaming = 作者的**投递开关**（`output(<expr>) streaming;`，用户
	// 2026-09-27 裁决 甲）：平台侧 `streamingOutput`。false = 采用平台默认值
	// （软降级，不报账 —— 缺省就是平台默认）。
	Streaming bool
}

// Loop is the semantic loop configuration. Its body lives on LoopStmt;
// backend-only nested graph shape is created by Project.
type Loop struct {
	ID            string
	InlineName    string // owning inlined function, empty at top level
	VarName       string
	LoopType      string // "by_array" | "by_iteration" | "infinite"
	LoopCount     int
	InputArrays   []string
	Inputs        []Source
	Types         map[string]*Type
	StateTypes    map[string]*Type
	StateValues   map[string]Source
	OutTypes      map[string]*Type
	OutputSources []Source
	FlattenOutput bool
	// FlattenPorts = 本 loop 里「展平语义真正生效」的输出口（`var x = for(...)`
	// 且该口收集到的每迭代值本身是 list ⇒ 语义类型 = 元素类型而非 list<元素>）。
	// 决策点唯一（build_stmt.go 算出 outputType 的那两处），因为**平台 loop 不展平**
	//
	// 与 FlattenOutput 的关系（**两个事实、不是两份权威**）：`FlattenOutput` 是
	// **绑定级**的源（拼写 `var` ⇒ true，见 build_call.go），`FlattenPorts` 是**口级**
	// 的物化义务 —— 后者推不出来：展平口与「恰好也收集 list」的普通口在 `OutTypes`
	// 上**同形**（展平 `list<int>` vs 收集 `list<int>`），渲染端要判它只能再走一遍
	// 循环体（P08：平行 walker 必然漏项）⇒ 在决策点顺手记下，别无第二处可推。
	// （收集 = 每迭代一个值 ⇒ 物理 list<list<T>>），lowering 必须为这些口物化一层
	// 展平（`pkg/pyast.FlattenAdapterModule`）。空 = 无展平。
	FlattenPorts    map[string]bool
	IsParallel      bool
	ParallelNums    int
	ErrorHandleMode string
	// ReturnValue 是 loop 体内的提前返回（return）值引用（M17）。nil =
	// loop 无 return;非 nil 时 loop 输出端口额外暴露 "returned"（bool,
	// loop 是否因 return 终止）与 "return_value"（终止时的返回值）,由
	// 驱动器填充 —— 对齐参考目标渲染的 __returning / __return_value 机制。
	ReturnValue *varRef
}

// BreakNode exits the innermost loop.
type BreakNode struct{ ID string }

// ContinueNode skips to the next iteration.
type ContinueNode struct{ ID string }

// LoopReturnNode terminates the innermost loop carrying a return value
// (M17, 46_loop_escape): its Invoke records the value into LoopState and
// raises the break marker. Value is the returned value source (may be a
// loop-port ref — iteration variable — which the body input map carries).
type LoopReturnNode struct {
	ID    string
	Value Source
}

func (n *LoopReturnNode) Kind() Kind     { return KindLoopReturn }
func (n *LoopReturnNode) NodeID() string { return n.ID }

// ── constructors (concise) ────────────────────────────────────────────────

func (s *Start) Kind() Kind             { return KindStart }
func (s *Start) NodeID() string         { return s.ID }
func (e *End) Kind() Kind               { return KindEnd }
func (e *End) NodeID() string           { return e.ID }
func (n *LLMCall) Kind() Kind           { return KindLLM }
func (n *LLMCall) NodeID() string       { return n.ID }
func (n *HTTPCall) Kind() Kind          { return KindHTTP }
func (n *HTTPCall) NodeID() string      { return n.ID }
func (n *ToolCall) Kind() Kind          { return KindTool }
func (n *ToolCall) NodeID() string      { return n.ID }
func (n *KnowledgeCall) Kind() Kind     { return KindKnowledge }
func (n *KnowledgeCall) NodeID() string { return n.ID }
func (n *AgentCall) Kind() Kind         { return KindAgent }
func (n *AgentCall) NodeID() string     { return n.ID }
func (n *TextProcess) Kind() Kind       { return KindText }
func (n *TextProcess) NodeID() string   { return n.ID }
func (n *CodeRun) Kind() Kind           { return KindCode }
func (n *CodeRun) NodeID() string       { return n.ID }
func (n *ListOp) NodeID() string        { return n.ID }
func (n *Selector) Kind() Kind          { return KindSelector }
func (n *Selector) NodeID() string      { return n.ID }
func (n *Aggregate) Kind() Kind         { return KindAggregate }
func (n *Aggregate) NodeID() string     { return n.ID }
func (n *Assign) Kind() Kind            { return KindAssign }
func (n *Assign) NodeID() string        { return n.ID }
func (n *QA) Kind() Kind                { return KindQA }
func (n *QA) NodeID() string            { return n.ID }

// Switch is the classification node (coze IntentDetector, type 22): the
// query is classified against the declared class labels; the runtime routes
// control flow along branch_<i> (classificationId == i) or the default
// edge. classificationId (int) / reason (string) are the data outputs.
type Switch struct {
	ID      string
	Query   Source
	Intents []string
	// llmParam 语义载荷（D1 配置面；零值 = 平台默认）
	Temperature  *float64
	MaxTokens    int
	TopP         *float64
	Mode         string // "all" | "top_speed"（"" = 平台默认 all）
	SystemPrompt string
	// Model / ModelName = 分类器模型的载体（R-70 甲，用户 2026-09-25 批准）。
	// Model 走与 llm() **同一协议**（`coze:<modelType>`，见 internal/cozemodel）；
	// ModelName 是平台**显示名**的载体（名字不是 id 的函数 ⇒ 原样携带，不作为
	// 平台参数下传；消费点 = internal/backend/cozebiz/（商业仓）switch_model.go）。
	// "" = 未携带 ⇒ lowering 写回平台默认对（1 / 豆包·工具调用）。
	Model     string
	ModelName string
}

func (n *Switch) Kind() Kind           { return KindSwitch }
func (n *Switch) NodeID() string       { return n.ID }
func (n *Output) Kind() Kind           { return KindOutput }
func (n *Output) NodeID() string       { return n.ID }
func (n *Loop) Kind() Kind             { return KindLoop }
func (n *Loop) NodeID() string         { return n.ID }
func (n *BreakNode) Kind() Kind        { return KindBreak }
func (n *BreakNode) NodeID() string    { return n.ID }
func (n *ContinueNode) Kind() Kind     { return KindContinue }
func (n *ContinueNode) NodeID() string { return n.ID }

// ── structured tree (D9: graph shape is a renderer projection) ────────────
// The IR is a structured program tree (docs/WORKFLOW-IR.md §11). Connect
// edges are NOT part of the stored IR; backend projections are derived by
// walking Entry + Root.

// Block is an ordered statement sequence.
type Block struct {
	Stmts []Stmt
}

// Stmt is one statement in a Block.
type Stmt interface {
	stmtNode() Node // entry node of the statement (nil = pure control)
	stmtID() string // entry node id ("" for pure-control statements)
}

// NodeStmt wraps a leaf node. Value carries the tail value the statement
// produces (build-time tailRef snapshot; nil when the node is a pure side
// effect).
type NodeStmt struct {
	Node  Node
	Value *varRef
}

func (s *NodeStmt) stmtNode() Node { return s.Node }
func (s *NodeStmt) stmtID() string { return s.Node.NodeID() }

// IfStmt lowers `if (cond) { then } else { else }`: a Selector entry, two
// branch blocks and a VariableAggregator merge (nil Merge = else-less if
// whose unmatched path falls through the selector's false port into the
// following statement).
type IfStmt struct {
	ID    string
	Cond  *Selector
	Then  *Block
	Else  *Block
	Merge *Aggregate
	// Relay is the TextProcessor that materializes the then-value of a
	// no-node else-less branch (legacy: a concrete edge for the selector's
	// true port). nil otherwise.
	Relay Node
	// ChainArms = `else if` 链融进**同一个条件节点**后的**中臂**（按源码序）。
	// nil = 普通两臂 if（既有形状 ⇒ 全部既有消费者一字不改）。
	//
	// 唯一生产者 = coze-biz 专属规范化 pass `FuseBranchChains`（渲染前最后一站，
	// 见 fuse_branch_chains.go 的头注：平台自己的多分支条件节点就是这个形状，
	// 一手 X212 节点 110750）。语义：
	//   · 臂序 = 求值序（平台逐条求值、第一个为真即走该臂）；
	//   · 端口名：臂 0（Then）= `true`，ChainArms[i] = `true_<i+1>`，兜底 Else = `false`；
	//   · Merge 仍是**一个** Aggregate（候选按臂序收齐）—— `FlattenChainAggregates`
	//     早就把内层 join 折掉了，本字段只是让**条件头**也合一。
	//
	// 阅读约定：语句树里还有 ChainArms 时，任何「按 Then/Else 枚举臂」的 walker
	// 都必须连 ChainArms 一起走（`walkStmts` 已并入）；漏走 = 静默丢节点。
	ChainArms []*ChainArm
}

// ChainArm 是中臂：一条 (守卫, 体) —— 与 Then 同构，只是带自己的条件表。
type ChainArm struct {
	Cond  []Condition
	Logic int
	Block *Block
}

func (s *IfStmt) stmtNode() Node { return s.Cond }
func (s *IfStmt) stmtID() string { return s.ID }

// AttemptStmt lowers `attempt { op } ok { ... } err { ... }`: the operation
// node feeds two branch blocks (source / fail-branch ports). Each branch
// terminates in its own End (nil when the branch produces no value).
type AttemptStmt struct {
	ID         string
	Op         Node
	Success    *Block
	Failure    *Block
	SuccessEnd *End
	FailureEnd *End
	// FlowExit reports that the attempt is the enclosing flow's tail
	// statement (top of the flow root, not inside a branch / loop / inline
	// body, and last in its block). Only then does the attempt's exit merge
	// carry the flow's return value into the canonical End (900001) and cut
	// the path after the statement. Otherwise the statement yields a value
	// and execution continues after it — materializing an End there used to
	// hang the continuation off that End (`end 900001 has outgoing edge(s)`,
	// L1 violation) for every non-terminal attempt.
	//
	// Why the renderer cannot infer this from the wire: a returning arm and a
	// value-yielding arm (including an empty arm, whose tail value is the
	// attempt result) both produce a branch carry ref, so the distinction is
	// structural, known only here.
	FlowExit bool
	// Exit is the attempt's value aggregate: Refs = the arm values that
	// escape each branch (the same refs its SuccessEnd/FailureEnd carry),
	// Groups = the per-variable phi plan when the arms write hoisted
	// variables instead of returning a tail value. Mirrors IfStmt.Merge /
	// SwitchStmt.Merge: the statement's value IS this aggregate's output,
	// so a consumer (an enclosing branch value, a following statement)
	// references the aggregate — NOT the operation node and NOT the error
	// binding. Before this field existed the builder left `tailRef` holding
	// whatever the last arm set (the result / error binding), so an attempt
	// inside an if arm made the arm's value the operation's error payload
	// (`{op,"error"}` → physical `errorBody`, an object) — a leaked binding
	// that no renderer could resolve into the attempt's real value.
	Exit *Aggregate
}

func (s *AttemptStmt) stmtNode() Node { return s.Op }
func (s *AttemptStmt) stmtID() string { return s.ID }

// AttemptStmt also implements Node: the failure branch's error binding
// resolves through the attempt container itself (build_stmt.go:360
// varToOutput[errName] = varRef{nodeID: id, output: "error",
// kind: "attempt-error"}), so portTypeOfNode must be able to look up the
// container's error port contract.
func (s *AttemptStmt) Kind() Kind     { return KindAttempt }
func (s *AttemptStmt) NodeID() string { return s.ID }

// LoopStmt is a loop container with a structured body. Container is the
// flattened Loop node the builder created (children/inner edges are derived
// from Body by Project and stored on the ephemeral backend projection).
type LoopStmt struct {
	ID            string
	InlineName    string
	VarName       string
	LoopType      string
	LoopCount     int
	InputArrays   []string
	Inputs        []Source
	Types         map[string]*Type
	StateTypes    map[string]*Type
	StateValues   map[string]Source
	OutTypes      map[string]*Type
	OutputSources []Source
	FlattenOutput bool
	// FlattenPorts 透传 Loop.FlattenPorts（= 语义上被展平、必须由 lowering
	// 物化展平垫片的输出口集合）。
	FlattenPorts    map[string]bool
	IsParallel      bool
	ParallelNums    int
	ErrorHandleMode string
	Body            *Block
	Container       *Loop
	Value           *varRef
	// ReturnValue 透传 Loop.ReturnValue（M17 loop 内 return）;flow.Project
	// 据它生成 post-loop 检查分支。
	ReturnValue *varRef
}

func (s *LoopStmt) stmtNode() Node { return s.Container }
func (s *LoopStmt) stmtID() string { return s.ID }

// ParallelStmt fans out its branches from the previous junction; the
// junction is the collection of all branch ends (no merge node).
type ParallelStmt struct {
	ID       string
	Branches []*Block
}

func (s *ParallelStmt) stmtNode() Node { return nil }
func (s *ParallelStmt) stmtID() string { return "" }

// QaBranch is one request_input action branch or the timeout branch.
type QaBranch struct {
	Port string
	Body *Block
}

// QaStmt lowers request_input: a QA node, fan-out branches (branch_N /
// default ports) and a VariableAggregator merge.
type QaStmt struct {
	ID       string
	QA       *QA
	Branches []QaBranch
	Merge    *Aggregate
}

func (s *QaStmt) stmtNode() Node { return s.QA }
func (s *QaStmt) stmtID() string { return s.ID }

// SwitchBranch is one switch case body.
type SwitchBranch struct {
	Port string
	Body *Block
}

// SwitchStmt lowers switch: a classification node, fan-out branches
// (branch_N / default ports) and a VariableAggregator merge (结构化 switch
// 后隐含汇聚点, user rule).
type SwitchStmt struct {
	ID       string
	Switch   *Switch
	Branches []SwitchBranch
	Merge    *Aggregate
}

func (s *SwitchStmt) stmtNode() Node { return s.Switch }
func (s *SwitchStmt) stmtID() string { return s.ID }

// ReturnStmt exits the enclosing function scope. End is the shared End
// node created by the top-level return (nil inside a branch / inline /
// loop-body scope, where the value flows upward instead). SharedEnd marks
// the canonical merged end (ensureEnd): Project creates it once and reuses
// it for every subsequent top-level return. A non-shared End (SharedEnd
// false, e.g. output("name", v) or the synthesized trailing return) is an
// independent terminal that does not set the end-created flag.
type ReturnStmt struct {
	ID        string
	Value     *varRef
	End       *End
	SharedEnd bool
	// Implicit marks the synthesized trailing return that carries the
	// top-level terminal End built from the junction after the last
	// statement (build()'s "!endCreated" branch). Its edges come from the
	// full junction (un-merged), not from the value producer.
	Implicit bool
	// FlowEnd marks a return inside a parallel branch (P5 #4): it
	// terminates the WHOLE flow, not just the branch. Backends must wire
	// its tail straight to the canonical end (900001) and exclude the
	// branch from the parallel junction (the barrier does not wait for it).
	// Set only when the return is the branch tail (dead code after it is
	// rejected by the frontend). Void returns (nil Value) are FlowEnd too.
	FlowEnd bool
}

// MaterializedProducer reports the concrete value node a backend projection
// must use when it differs from the current control-flow tail.
func (s *ReturnStmt) MaterializedProducer() (string, bool) {
	if s.Value == nil || !materializedKind(s.Value.kind) {
		return "", false
	}
	return s.Value.nodeID, true
}

// ResultRef exposes the semantic return value without leaking the builder's
// private varRef representation to backend renderers.
func (s *ReturnStmt) ResultRef() (Ref, bool) {
	if s.Value == nil {
		return Ref{}, false
	}
	return Ref{Producer: s.Value.nodeID, Output: s.Value.output}, true
}

func (s *ReturnStmt) stmtNode() Node { return nil }
func (s *ReturnStmt) stmtID() string { return "" }

// BreakStmt / ContinueStmt are loop control nodes inside the loop body.
type BreakStmt struct{ Node *BreakNode }

func (s *BreakStmt) stmtNode() Node { return s.Node }
func (s *BreakStmt) stmtID() string { return s.Node.NodeID() }

type ContinueStmt struct{ Node *ContinueNode }

func (s *ContinueStmt) stmtNode() Node { return s.Node }
func (s *ContinueStmt) stmtID() string { return s.Node.NodeID() }

// InlineStmt is an inlined function body: an isolated return scope whose
// trailing value becomes the call result.
type InlineStmt struct {
	Name       string
	Params     []InlineParam
	ReturnType *Type
	Body       *Block
	Value      *varRef
	// Exit 是**内联帧**（子函数体）多个值出口的汇聚单元 —— 子函数在平台上没有
	// 表示，它的值只能在调用点由一条汇聚表达：「函数的值 = 各 return 的第一个
	// 非零候选」。Value 读它的口；控制流上它是**帧的出口入口**（每条以 return
	// 结束的臂/路径终止于它，未匹配路径经选择器端口续接到它）。
	//
	// 为什么值出口必须汇聚而不是单槽 tailRef：`return` 是**值出口**（每一条
	// 各携带一个值），单槽只留最后一个 ⇒ 前面的出口值消失（一手：内联三路
	// return 的产物 merge 只剩 2 个候选，首臂值静默丢失，X14 / examples
	// 一等实测）。
	//
	// nil = 单出口帧（一个出口不需要汇聚单元：单候选 phi 是同一判据的空形）
	// 或零出口帧（void 子函数）。与 End.FlowExitMerge 同一条规则，只是宿主从
	// 「流程出口」换成「内联帧出口」。
	Exit *Aggregate
}

type InlineParam struct {
	Name   string
	Type   *Type
	Source Source
}

func (s *InlineStmt) stmtNode() Node { return nil }
func (s *InlineStmt) stmtID() string { return "" }
