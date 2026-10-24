package astnodes

import (
	"github.com/nodecoda/ncoda/pkg/ncoda/typesystem"
)

// ---------- 基础节点 ----------

type EntryNode struct {
	NodeBase
	DefaultValues map[string]any
}

func (n *EntryNode) Kind() string { return "entry" }
func (n *EntryNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type ExitNode struct {
	NodeBase
	TerminatePlan   string
	Template        string
	StreamingOutput bool
}

func (n *ExitNode) Kind() string { return "exit" }
func (n *ExitNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type CodeRunnerNode struct {
	NodeBase
	Code        string
	Language    string
	Imports     []string
	SyntaxError string
}

func (n *CodeRunnerNode) Kind() string { return "code_runner" }
func (n *CodeRunnerNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type LLMNode struct {
	NodeBase
	ModelID             string
	ModelName           string
	SystemPrompt        string
	UserPrompt          string
	ModelResponseFormat string
	OutputFormat        string
	Temperature         *float64
	MaxTokens           int
	TopP                *float64
	// GenerationDiversity 是平台 llmParam 的生成多样性旋钮（default_val 之外的
	// balance/creative/precise）。本语言**已有**载体（`generation_diversity` config 键，
	// 见 pkg/ir/build_node.go / internal/backend/cozebiz/（商业仓）render_node.go）⇒ 必须读
	// 出来才能往返保真（R-73：修前不读 ⇒ 543/776 个非默认值静默消失）。空串 = 该导出
	// 没有这个键。
	GenerationDiversity  string
	EnableChatHistory    bool
	ChatHistoryRound     int
	BackupModelID        string
	BackupTemperature    *float64
	BackupMaxTokens      int
	BackupTopP           *float64
	BackupResponseFormat string
	Tools                []LLMToolConfig
	KnowledgeRecall      *LLMKnowledgeRecallConfig
	// LLMParamRaw 是 wire 的**逐字快照**（llmParam 原表的 name → value），
	// **不参与语义**（语义字段在上面）。用途只有一个：反编译侧判定「平台写了哪些
	// 键、本语言装不装得下」时，键**在不在**必须是平台事实，不能由默认值推理代答
	// （R-73 (b)/(c)：平台 784 个节点 / 29 个键，本语言只承载其中 9 个）。
	// nil = 该导出没有 llmParam。
	LLMParamRaw map[string]any
	// LLMParamTypes 与 LLMParamRaw **同表平行**：wire 每个 llmParam 条目的**声明类型**
	// （`input.type`：`string` / `boolean` / `integer` / `float`）。为什么必须留它（2026-09-30 一手）：
	// 平台的数值取值是**字符串内容 + 声明类型**（语料一手：`{type: float, value: '0.8'}` ·
	// `{type: integer, value: '4096'}`）⇒ 只看 `LLMParamRaw` 的内容分不出「字符串 "2.5"」与
	// 「浮点 2.5」。反编译侧要把无槽位键**原样回填** ncoda config 时，缺了这一格就只能
	// 猜类型 ⇒ 回填会把 float 写成 string（键面不闭环）。nil = 该导出没有 llmParam。
	LLMParamTypes map[string]string
	// FCParamRaw 是**节点级** function-calling 配置（节点参数里的 `fcParam` /
	// `fcParamVar` 两个键，是 `llmParam` 的兄弟）的 wire **逐字快照**，**不参与语义**
	// （语义字段 = 上面的 Tools / KnowledgeRecall）。用途与 LLMParamRaw 同一条纪律：
	// 判「平台写了什么、本语言装得下哪些」时，键在不在必须是平台事实，不能由默认值
	// 推理代答。nil = 该导出没有这两个键。
	FCParamRaw map[string]any
}

func (n *LLMNode) Kind() string { return "llm" }
func (n *LLMNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type IntentDetectorNode struct {
	NodeBase
	Intents             []string
	FastMode            bool
	ModelID             string
	ModelName           string
	SystemPrompt        string
	ModelResponseFormat string
	Temperature         *float64
	MaxTokens           int
	TopP                *float64
	EnableChatHistory   bool
	ChatHistoryRound    int
	// LLMParamRaw / ModeRaw 是 wire 的**逐字快照**（llmParam 原表 + 节点级 mode），
	// **不参与语义**（语义字段在上面）。用途只有一个：反编译侧判定「平台写了
	// 哪些键、本语言装不装得下」时，键**在不在**必须是平台事实，不能由我们
	// 的默认值推理代答（R-70：intent 配置整块没承载 ⇒ 换模型 / 关快模式静默
	// 丢失）。nil / "" = 该导出没有这个键（不是"等于默认"）。
	LLMParamRaw map[string]any
	ModeRaw     string
}

func (n *IntentDetectorNode) Kind() string { return "intent_detector" }
func (n *IntentDetectorNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type PluginNode struct {
	NodeBase
	PluginID      string
	ToolID        string
	PluginVersion string
	PluginFrom    *int
	// APIName/PluginName 是 canvas apiParam 里的原始显示名（apiName/pluginName）。
	// 反编译生成 <slug>.<op> 需要操作名；部分导出不带 apiName 时回退 apiID。
	APIName    string
	PluginName string
	// InputDefs 是 canvas inputs.inputDefs 的参数 schema（name → 平台类型）。
	// coze 插件 file 语义参数在平台侧标 image/audio/video（本质 URL 字符串,
	// 与 file 同义）; 反编译器据此在 string→file 边界自动 file(...) 标注。
	// 缺省时为空（旧导出无 inputDefs → 不注入, 保持现状）。
	InputDefs []PluginInputDef
}

// PluginInputDef 一条插件输入定义（canvas inputDefs 项）。
type PluginInputDef struct {
	Name string
	Type string
}

func (n *PluginNode) Kind() string { return "plugin" }
func (n *PluginNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type SelectorNode struct {
	NodeBase
	Clauses      []SelectorClause
	OutputKey    string
	DefaultIndex int
}

func (n *SelectorNode) Kind() string { return "selector" }
func (n *SelectorNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type TextProcessorNode struct {
	NodeBase
	Method     string
	Template   string
	ConcatChar string
	Separators []string
	OutputKey  string
}

func (n *TextProcessorNode) Kind() string { return "text_processor" }
func (n *TextProcessorNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type JsonSerializationNode struct {
	NodeBase
	InputKey  string
	OutputKey string
}

func (n *JsonSerializationNode) Kind() string { return "json_serialization" }
func (n *JsonSerializationNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type JsonDeserializationNode struct {
	NodeBase
	OutputType *typesystem.TypeInfo
	InputKey   string
	OutputKey  string
}

func (n *JsonDeserializationNode) Kind() string { return "json_deserialization" }
func (n *JsonDeserializationNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type VideoAudioExtractorNode struct {
	NodeBase
	Config map[string]any
}

func (n *VideoAudioExtractorNode) Kind() string { return "video_audio_extractor" }
func (n *VideoAudioExtractorNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type VariableAggregatorNode struct {
	NodeBase
	GroupLengths  map[string]int
	GroupOrder    []string
	MergeStrategy string
}

func (n *VariableAggregatorNode) Kind() string { return "variable_aggregator" }
func (n *VariableAggregatorNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type VariableAssignerNode struct {
	NodeBase
	Assignments []VariableAssignment
}

func (n *VariableAssignerNode) Kind() string { return "variable_assigner" }
func (n *VariableAssignerNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type VariableAssignerWithinLoopNode struct {
	NodeBase
	Assignments []VariableAssignment
}

func (n *VariableAssignerWithinLoopNode) Kind() string { return "variable_assigner_within_loop" }
func (n *VariableAssignerWithinLoopNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type LoopNode struct {
	NodeBase
	LoopType          string
	InputArrays       []string
	IntermediateTypes map[string]*typesystem.TypeInfo
	// IntermediateTypesDefaulted 记「该状态名的类型**不是平台声明的**，而是
	// 缺省占位（BlockInputFromDict 的 string 兜底）」——raising 的 RS-LOOP-002
	// 必须能区分「声明的 string」与「缺省出来的 string」，否则判据恒不成立
	// （L25 盲面：缺类型被静默读成 string）。**构造时定性、不撤销**。
	IntermediateTypesDefaulted map[string]bool
	// IntermediateTypesResolved 记「该缺省占位已被权威源（上游 ref 回填）补齐」
	// ——回填的落点是 InputTypes（IntermediateTypes 只留构造时的占位指针），
	// 故 raising 侧据此改读 InputTypes。声明类型**不**置此位（声明即权威，
	// 回填压过也不改变 IntermediateTypes 的权威地位）。
	IntermediateTypesResolved map[string]bool
}

// IsIntermediateDefaulted 该状态名的类型是否只是缺省占位（非权威）。
func (n *LoopNode) IsIntermediateDefaulted(name string) bool {
	return n.IntermediateTypesDefaulted[name]
}

// IsIntermediateResolved 该缺省占位是否已被权威源补齐（回填落点见字段注）。
func (n *LoopNode) IsIntermediateResolved(name string) bool {
	return n.IntermediateTypesResolved[name]
}

// MarkIntermediateResolved 上游回填补齐权威类型后置位（只对缺省占位有意义）。
func (n *LoopNode) MarkIntermediateResolved(name string) {
	if n.IntermediateTypesResolved == nil {
		n.IntermediateTypesResolved = map[string]bool{}
	}
	n.IntermediateTypesResolved[name] = true
}

func (n *LoopNode) Kind() string { return "loop" }
func (n *LoopNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

// GetIntermediateTypes 供 ChildVisibleTypeAt 探测中间变量。
func (n *LoopNode) GetIntermediateTypes() map[string]*typesystem.TypeInfo {
	return n.IntermediateTypes
}

type BatchNode struct {
	NodeBase
	InputArrays       []string
	BatchSizeKey      string
	ConcurrentSizeKey string
}

func (n *BatchNode) Kind() string { return "batch" }
func (n *BatchNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type BreakNode struct{ NodeBase }

func (n *BreakNode) Kind() string                 { return "break" }
func (n *BreakNode) ToConfigDict() map[string]any { return n.NodeBase.ToConfigDict(n) }

type ContinueNode struct{ NodeBase }

func (n *ContinueNode) Kind() string                 { return "continue" }
func (n *ContinueNode) ToConfigDict() map[string]any { return n.NodeBase.ToConfigDict(n) }

type SubWorkflowNode struct {
	NodeBase
	WorkflowID      string
	WorkflowVersion string
	TerminatePlan   string
	// SpaceID 是子工作流所在空间（coze-biz yaml 参数 spaceId）。
	SpaceID string
	// InputDefs 是 subflow 输入契约（yaml inputDefs: name → 平台类型）。
	// 反编译据此重建 subflow 声明的 input: {...} 契约（子工作流是声明式
	// 节点，输入 schema 静态完整，无 LLM 开放 object 问题）。
	InputDefs []SubflowInputDef
}

// SubflowInputDef 一条 subflow 输入契约定义（platform inputDefs 项）。
//
// Type / AssistType = 平台 `Variable` DTO 的两个字段（coze-studio
// `backend/domain/workflow/entity/vo/canvas.go::Variable`）：`Type` 是字段类型，
// `AssistType` 是**字符串字段的副类型**（不同 file 族 / image / time）。两条通道
// 都用它：yaml 通道把族写进 `type:` 拼写，canvas 通道写 `{type: string,
// assistType: N}`（`platform_type_table.go` 是拼写 ↔ 族号的唯一映射处）。
type SubflowInputDef struct {
	Name string
	Type string
	// AssistType 是平台副类型（0 = 平台没写这个键）。用途 = canvas 通道的 file
	// 族载体；由 raising 侧投影成 ncoda `file<…>`，不参与 lowering 决策。
	AssistType int
}

func (n *SubWorkflowNode) Kind() string { return "sub_workflow" }
func (n *SubWorkflowNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type InputReceiverNode struct {
	NodeBase
	OutputSchema string
}

func (n *InputReceiverNode) Kind() string { return "input_receiver" }
func (n *InputReceiverNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type QuestionAnswerNode struct {
	NodeBase
	QuestionTemplate  string
	AnswerType        string
	ChoiceType        string
	FixedChoices      []string
	ExtractFromAnswer bool
	MaxAnswerCount    int
	// LLMParam = 商业 yaml 里 `question.llmParam` 的**逐字快照**（当它是
	// `SimpleLLMParam` 的**扁平标量表**时；nil = 该导出没有这个键）。
	//
	// 为什么必须逐字（而不是只解析成类型化字段）：① raising 要按**键存在性**
	// 发 ncoda config（平台写 `systemPrompt: ""` 这种「写了、值空」的键，值类型
	// 分不出「缺席」与「空值」）；② 平台 llmParam 的未知键要能被点名而不是静默丢。
	// 注意形状：llm 节点的 `llmParam` 是 `[{name, input}]` **行表**，
	// question 的不同形 —— 本字段只在后者成立时非 nil。
	LLMParam map[string]any
	// DynamicOptionRaw = `dynamic_option` 的逐字快照（仅当它是 canvas 引用信封
	// map）。`option_type: dynamic` 时代码用它还原 `dynamic_options` 的值表达式。
	DynamicOptionRaw any
}

func (n *QuestionAnswerNode) Kind() string { return "question_answer" }
func (n *QuestionAnswerNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type OutputEmitterNode struct {
	NodeBase
	Template string
	// StreamingOutput / CallTransferVoice / ChatHistoryWriting 是平台 `output`
	// 节点的**投递开关**（不在 config、也不在模板里）。本语言没有对应拼写
	// （`output(<expr>)` 只发布消息文本）⇒ lowering 无条件写回固定值，非默认值
	// 由 raising 报账（`RS-OUT-001`，见 cozebiz/decompile/output_params.go）。
	// CallTransferVoice 用指针区分「平台没写这个键」与「写了 false」——
	// 前者不是失配（无可对照），后者才是。
	StreamingOutput    bool
	CallTransferVoice  *bool
	ChatHistoryWriting string
}

func (n *OutputEmitterNode) Kind() string { return "output_emitter" }
func (n *OutputEmitterNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

// HTTPRequesterNode 的韧性问题（重试/超时）**不在本结构体里** —— 它归一进
// NodeBase.ExceptionConfig（平台 http 节点自带的 `setting` 与通用 settingOnError
// 是同一语义的两个载体，见 parser.NormalizeHTTPLegacyPolicy）。
type HTTPRequesterNode struct {
	NodeBase
	Method       string
	URLTemplate  string
	Body         HTTPBodyConfig
	Auth         *HTTPAuthConfig
	HeaderFields map[string]string
	QueryFields  map[string]string
	URLFields    map[string]string
	BodyFields   map[string]string
}

func (n *HTTPRequesterNode) Kind() string { return "http_requester" }
func (n *HTTPRequesterNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

// ---------- 数据库节点 ----------
//
// 四型都带 `ParamRaw`：yaml 通道该节点 `parameters` 的**逐字快照**
// （databaseInfoList / insertParam / selectParam / updateParam / deleteParam /
// node_outputs）。
//
// 为什么必须逐字（而不是只用下面解析出的语义字段）：lowering 侧对数据库族的
// 参数子树是**逐字承载**（internal/backend/cozebiz/（商业仓）database_wire_shape.go），
// 而 canvas 归一**有损** —— yaml 方言 `input.value: <裸值>` 被包成 canvas 方言
// `{type: literal, content: …}`，node_outputs 被折成 canvas outputs 数组（类型
// 名映射 / required 归一）。只用语义字段会造出**平台不认的方言**。
// 与 LLMNode.LLMParamRaw 同一纪律：raw 只记「平台写了什么」，不参与语义。
// json/canvas 通道没有该快照（nil）⇒ raising 退回 canvas 参数面
// （`Node.Data.Inputs.Raw`），两个通道各自自洽（见 database_family.go 头注）。

type DatabaseQueryNode struct {
	NodeBase
	DatabaseInfoIDs []string
	QueryFields     []string
	OrderClauses    []DatabaseOrderClause
	ClauseGroup     DatabaseClauseGroup
	Limit           int
	ParamRaw        map[string]any
}

func (n *DatabaseQueryNode) Kind() string { return "database_query" }
func (n *DatabaseQueryNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type DatabaseInsertNode struct {
	NodeBase
	DatabaseInfoIDs []string
	ParamRaw        map[string]any
}

func (n *DatabaseInsertNode) Kind() string { return "database_insert" }
func (n *DatabaseInsertNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type DatabaseUpdateNode struct {
	NodeBase
	DatabaseInfoIDs []string
	ClauseGroup     DatabaseClauseGroup
	ParamRaw        map[string]any
}

func (n *DatabaseUpdateNode) Kind() string { return "database_update" }
func (n *DatabaseUpdateNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type DatabaseDeleteNode struct {
	NodeBase
	DatabaseInfoIDs []string
	ClauseGroup     DatabaseClauseGroup
	ParamRaw        map[string]any
}

func (n *DatabaseDeleteNode) Kind() string { return "database_delete" }
func (n *DatabaseDeleteNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type DatabaseCustomSQLNode struct {
	NodeBase
	DatabaseInfoIDs []string
	SQLTemplate     string
}

func (n *DatabaseCustomSQLNode) Kind() string { return "database_custom_sql" }
func (n *DatabaseCustomSQLNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

// ---------- 知识库节点 ----------

type KnowledgeIndexerNode struct {
	NodeBase
	KnowledgeIDs     []string
	ParsingStrategy  ParsingStrategyConfig
	ChunkingStrategy ChunkingStrategyConfig
}

func (n *KnowledgeIndexerNode) Kind() string { return "knowledge_indexer" }
func (n *KnowledgeIndexerNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type KnowledgeRetrieverNode struct {
	NodeBase
	KnowledgeIDs       []string
	RetrievalStrategy  RetrievalStrategyConfig
	ChatHistorySetting *ChatHistorySettingConfig
}

func (n *KnowledgeRetrieverNode) Kind() string { return "knowledge_retriever" }
func (n *KnowledgeRetrieverNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type KnowledgeDeleterNode struct {
	NodeBase
	KnowledgeIDs []string
}

func (n *KnowledgeDeleterNode) Kind() string { return "knowledge_deleter" }
func (n *KnowledgeDeleterNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

// ---------- 会话节点 ----------

type ConversationNode struct {
	NodeBase
	Operation string
}

func (n *ConversationNode) Kind() string { return "conversation" }
func (n *ConversationNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

// ---------- 延迟节点 ----------

// ImageGenerateNode 图像生成（canvas type 16，coze 内置节点）。
// prompt 走 InputSources（ref 绑定）；modelSetting / prompt 模板是静态配置，
// 从 Inputs.Raw 提取保留（正编译 P1 通道还原）。
type ImageGenerateNode struct {
	NodeBase
	ModelSetting   map[string]any // model/ratio/custom_ratio/ddim_steps/images_reference
	PromptTemplate map[string]any // prompt + negative_prompt（含 {{prompt}} 占位符）
	References     []any
}

func (n *ImageGenerateNode) Kind() string { return "image_generate" }
func (n *ImageGenerateNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

// DrawingBoardNode 画板（canvas type 23，coze 内置节点）。
// canvasSchema 是静态 JSON 字符串（含 customVariableRefs.variableName）；
// variables 走 InputSources（variableName → ref 绑定）。
type DrawingBoardNode struct {
	NodeBase
	CanvasSchema string
}

func (n *DrawingBoardNode) Kind() string { return "drawing_board" }
func (n *DrawingBoardNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

// VideoGenerationNode 视频生成（coze-biz 商业独有节点 doubao-seedance，
// canvas 无数字码；yaml 适配器映射为枚举名 "VideoGeneration"）。
// 动态输入（node_inputs）走 InputSources（ref 绑定）；静态配置
// （model/duration/prompt/ratio/resolution/firstFrame/...）整体保留在
// Config map（wire 键直通，含 firstFrame 的 canvas ref 形态），
// 反编译 → 正编译全链路 opaque 透传，roundtrip 保真。
type VideoGenerationNode struct {
	NodeBase
	Config map[string]any // 静态配置（wire 键），已剔除 inputParameters
}

func (n *VideoGenerationNode) Kind() string { return "video_generation" }
func (n *VideoGenerationNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

// VideoFrameExtractorNode 视频抽帧（coze-biz 商业独有节点，canvas type 16
// 数字码与 image_generate 同码；yaml 适配器映射为枚举名 "VideoFrameExtractor"）。
// 动态输入（video）走 InputSources（ref 绑定）；静态配置
// （frameExtractionCount/IntervalMs/Method）整体保留在 Config map（wire 键
// 直通），反编译 → 正编译全链路 opaque 透传，roundtrip 保真。
type VideoFrameExtractorNode struct {
	NodeBase
	Config map[string]any // 静态配置（wire 键）
}

func (n *VideoFrameExtractorNode) Kind() string { return "video_frame_extractor" }
func (n *VideoFrameExtractorNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

type DeferredNode struct {
	NodeBase
	DeferredType string
}

func (n *DeferredNode) Kind() string { return "deferred" }
func (n *DeferredNode) ToConfigDict() map[string]any {
	return n.NodeBase.ToConfigDict(n)
}

// NodeClassByKind KIND -> 构造工厂（运行时 IR config 分派用）。
var NodeClassByKind = map[string]func() NodeDecl{
	"entry":                         func() NodeDecl { return &EntryNode{} },
	"exit":                          func() NodeDecl { return &ExitNode{} },
	"code_runner":                   func() NodeDecl { return &CodeRunnerNode{} },
	"llm":                           func() NodeDecl { return &LLMNode{} },
	"intent_detector":               func() NodeDecl { return &IntentDetectorNode{} },
	"plugin":                        func() NodeDecl { return &PluginNode{} },
	"selector":                      func() NodeDecl { return &SelectorNode{} },
	"text_processor":                func() NodeDecl { return &TextProcessorNode{} },
	"json_serialization":            func() NodeDecl { return &JsonSerializationNode{} },
	"json_deserialization":          func() NodeDecl { return &JsonDeserializationNode{} },
	"variable_aggregator":           func() NodeDecl { return &VariableAggregatorNode{} },
	"variable_assigner":             func() NodeDecl { return &VariableAssignerNode{} },
	"variable_assigner_within_loop": func() NodeDecl { return &VariableAssignerWithinLoopNode{} },
	"loop":                          func() NodeDecl { return &LoopNode{} },
	"batch":                         func() NodeDecl { return &BatchNode{} },
	"break":                         func() NodeDecl { return &BreakNode{} },
	"continue":                      func() NodeDecl { return &ContinueNode{} },
	"sub_workflow":                  func() NodeDecl { return &SubWorkflowNode{} },
	"input_receiver":                func() NodeDecl { return &InputReceiverNode{} },
	"question_answer":               func() NodeDecl { return &QuestionAnswerNode{} },
	"output_emitter":                func() NodeDecl { return &OutputEmitterNode{} },
	"http_requester":                func() NodeDecl { return &HTTPRequesterNode{} },
	"database_query":                func() NodeDecl { return &DatabaseQueryNode{} },
	"database_insert":               func() NodeDecl { return &DatabaseInsertNode{} },
	"database_update":               func() NodeDecl { return &DatabaseUpdateNode{} },
	"database_delete":               func() NodeDecl { return &DatabaseDeleteNode{} },
	"database_custom_sql":           func() NodeDecl { return &DatabaseCustomSQLNode{} },
	"knowledge_indexer":             func() NodeDecl { return &KnowledgeIndexerNode{} },
	"knowledge_retriever":           func() NodeDecl { return &KnowledgeRetrieverNode{} },
	"knowledge_deleter":             func() NodeDecl { return &KnowledgeDeleterNode{} },
	"conversation":                  func() NodeDecl { return &ConversationNode{} },
	"deferred":                      func() NodeDecl { return &DeferredNode{} },
}
