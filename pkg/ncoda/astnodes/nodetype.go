package astnodes

import "fmt"

// NodeType 节点类型（对应 ncoda_coze.ast_nodes.node_type.NodeType）。
type NodeType string

const (
	NodeTypeEntry                      NodeType = "Entry"
	NodeTypeExit                       NodeType = "Exit"
	NodeTypeLLM                        NodeType = "LLM"
	NodeTypeCodeRunner                 NodeType = "CodeRunner"
	NodeTypePlugin                     NodeType = "Plugin"
	NodeTypeHTTPRequester              NodeType = "HTTPRequester"
	NodeTypeLambda                     NodeType = "Lambda"
	NodeTypeImageGenerate              NodeType = "ImageGenerate"       // canvas id 16（coze 内置图像生成）
	NodeTypeDrawingBoard               NodeType = "DrawingBoard"        // canvas id 23（coze 内置画板）
	NodeTypeVideoGeneration            NodeType = "VideoGeneration"     // coze-biz 商业独有视频生成（doubao-seedance，JSON 导出数字码 65）
	NodeTypeVideoFrameExtractor        NodeType = "VideoFrameExtractor" // coze-biz 商业独有视频抽帧（canvas 面未实证数字码 ⇒ 走具名枚举；16 是 image_generate 的码，不共用）
	NodeTypeVideoAudioExtractor        NodeType = "VideoAudioExtractor" // coze-biz 商业独有视频提取音频（yaml 业务名 video_audio_extractor）
	NodeTypeComment                    NodeType = "Comment"
	NodeTypeLoop                       NodeType = "Loop"
	NodeTypeBreak                      NodeType = "Break"
	NodeTypeContinue                   NodeType = "Continue"
	NodeTypeBatch                      NodeType = "Batch"
	NodeTypeSubWorkflow                NodeType = "SubWorkflow"
	NodeTypeSelector                   NodeType = "Selector"
	NodeTypeIntentDetector             NodeType = "IntentDetector"
	NodeTypeQuestionAnswer             NodeType = "QuestionAnswer"
	NodeTypeInputReceiver              NodeType = "InputReceiver"
	NodeTypeOutputEmitter              NodeType = "OutputEmitter"
	NodeTypeVariableAggregator         NodeType = "VariableAggregator"
	NodeTypeVariableAssigner           NodeType = "VariableAssigner"
	NodeTypeVariableAssignerWithinLoop NodeType = "VariableAssignerWithinLoop"
	NodeTypeTextProcessor              NodeType = "TextProcessor"
	NodeTypeJsonSerialization          NodeType = "JsonSerialization"
	NodeTypeJsonDeserialization        NodeType = "JsonDeserialization"
	NodeTypeDatabaseCustomSQL          NodeType = "DatabaseCustomSQL"
	NodeTypeDatabaseQuery              NodeType = "DatabaseQuery"
	NodeTypeDatabaseInsert             NodeType = "DatabaseInsert"
	NodeTypeDatabaseDelete             NodeType = "DatabaseDelete"
	NodeTypeDatabaseUpdate             NodeType = "DatabaseUpdate"
	NodeTypeKnowledgeIndexer           NodeType = "KnowledgeIndexer"
	NodeTypeKnowledgeRetriever         NodeType = "KnowledgeRetriever"
	NodeTypeKnowledgeDeleter           NodeType = "KnowledgeDeleter"
	NodeTypeCreateConversation         NodeType = "CreateConversation"
	NodeTypeConversationList           NodeType = "ConversationList"
	NodeTypeConversationUpdate         NodeType = "ConversationUpdate"
	NodeTypeConversationDelete         NodeType = "ConversationDelete"
	NodeTypeClearConversationHistory   NodeType = "ClearConversationHistory"
	NodeTypeConversationHistory        NodeType = "ConversationHistory"
	NodeTypeMessageList                NodeType = "MessageList"
	NodeTypeCreateMessage              NodeType = "CreateMessage"
	NodeTypeEditMessage                NodeType = "EditMessage"
	NodeTypeDeleteMessage              NodeType = "DeleteMessage"
)

// NodeTypeFromString 从字符串构造 NodeType，未知值报错。
func NodeTypeFromString(s string) (NodeType, error) {
	t := NodeType(s)
	switch t {
	case NodeTypeEntry, NodeTypeExit, NodeTypeLLM, NodeTypeCodeRunner, NodeTypePlugin,
		NodeTypeHTTPRequester, NodeTypeLambda, NodeTypeComment, NodeTypeLoop, NodeTypeBreak,
		NodeTypeContinue, NodeTypeBatch, NodeTypeSubWorkflow, NodeTypeSelector,
		NodeTypeIntentDetector, NodeTypeQuestionAnswer, NodeTypeInputReceiver,
		NodeTypeOutputEmitter, NodeTypeVariableAggregator, NodeTypeVariableAssigner,
		NodeTypeVariableAssignerWithinLoop, NodeTypeTextProcessor, NodeTypeVideoGeneration,
		NodeTypeVideoFrameExtractor, NodeTypeVideoAudioExtractor,
		NodeTypeImageGenerate, NodeTypeDrawingBoard,
		NodeTypeJsonSerialization,
		NodeTypeJsonDeserialization, NodeTypeDatabaseCustomSQL, NodeTypeDatabaseQuery,
		NodeTypeDatabaseInsert, NodeTypeDatabaseDelete, NodeTypeDatabaseUpdate,
		NodeTypeKnowledgeIndexer, NodeTypeKnowledgeRetriever, NodeTypeKnowledgeDeleter,
		NodeTypeCreateConversation, NodeTypeConversationList, NodeTypeConversationUpdate,
		NodeTypeConversationDelete, NodeTypeClearConversationHistory,
		NodeTypeConversationHistory, NodeTypeMessageList, NodeTypeCreateMessage,
		NodeTypeEditMessage, NodeTypeDeleteMessage:
		return t, nil
	}
	return "", fmt.Errorf("unknown NodeType: %q", s)
}

// IsDeclaredNodeType 判断字符串是否为本仓**声明的**节点类型（NodeTypeFromString 的谓词形态）。
//
// 用途 = 通道词汇校验：canvas(json) 通道的 `nodes[].type` 只允许「数字码 ∪ 具名 canvas 枚举」，
// 而「具名 canvas 枚举」的闭集就是这个谓词 —— 权威仍是本文件的 const 块，不另立清单。
// 一手实证（2026-09-27）：canvas 导出里混进 yaml 词汇（`"type": "start"`）时，
// ParseNodeType 的兜底分支 `return NodeType(value)` 会把它当成一个「没声明过的类型」放行，
// 节点静默降级成 deferred、产物多出 `let v1 = start();`、`main` 丢返回类型，rc=0 零诊断。
func IsDeclaredNodeType(t NodeType) bool {
	_, err := NodeTypeFromString(string(t))
	return err == nil
}

// IsComposite 判断是否复合节点（含内部 Blocks: loop/batch）。
func (t NodeType) IsComposite() bool {
	return t == NodeTypeLoop || t == NodeTypeBatch
}

// IsDatabase 判断是否数据库节点。
func (t NodeType) IsDatabase() bool {
	switch t {
	case NodeTypeDatabaseCustomSQL, NodeTypeDatabaseQuery, NodeTypeDatabaseInsert,
		NodeTypeDatabaseDelete, NodeTypeDatabaseUpdate:
		return true
	}
	return false
}

// IsKnowledge 判断是否知识库节点。
func (t NodeType) IsKnowledge() bool {
	switch t {
	case NodeTypeKnowledgeIndexer, NodeTypeKnowledgeRetriever, NodeTypeKnowledgeDeleter:
		return true
	}
	return false
}

// IsConversation 判断是否会话节点。
func (t NodeType) IsConversation() bool {
	switch t {
	case NodeTypeCreateConversation, NodeTypeConversationList, NodeTypeConversationUpdate,
		NodeTypeConversationDelete, NodeTypeClearConversationHistory,
		NodeTypeConversationHistory, NodeTypeMessageList, NodeTypeCreateMessage,
		NodeTypeEditMessage, NodeTypeDeleteMessage:
		return true
	}
	return false
}

// EntryNodeKey / ExitNodeKey 是固定节点 key。
const (
	EntryNodeKey = "100001"
	ExitNodeKey  = "900001"
)

// TerminatePlan 终止计划。
type TerminatePlan string

const (
	TerminatePlanReturnVariables  TerminatePlan = "returnVariables"
	TerminatePlanUseAnswerContent TerminatePlan = "useAnswerContent"
)

// TerminatePlanFromString 从字符串构造 TerminatePlan；空/未知回退 RETURN_VARIABLES。
func TerminatePlanFromString(s string) TerminatePlan {
	switch TerminatePlan(s) {
	case TerminatePlanReturnVariables, TerminatePlanUseAnswerContent:
		return TerminatePlan(s)
	default:
		return TerminatePlanReturnVariables
	}
}

// NodeTypeByID 是 Canvas Node.type 数字 ID -> NodeType 映射。
var NodeTypeByID = map[string]NodeType{
	"1":  NodeTypeEntry,
	"2":  NodeTypeExit,
	"3":  NodeTypeLLM,
	"4":  NodeTypePlugin,
	"5":  NodeTypeCodeRunner,
	"6":  NodeTypeKnowledgeRetriever,
	"8":  NodeTypeSelector,
	"9":  NodeTypeSubWorkflow,
	"12": NodeTypeDatabaseCustomSQL,
	"13": NodeTypeOutputEmitter,
	"15": NodeTypeTextProcessor,
	"16": NodeTypeImageGenerate,
	"18": NodeTypeQuestionAnswer,
	"19": NodeTypeBreak,
	"20": NodeTypeVariableAssignerWithinLoop,
	"21": NodeTypeLoop,
	"22": NodeTypeIntentDetector,
	"23": NodeTypeDrawingBoard,
	"27": NodeTypeKnowledgeIndexer,
	"28": NodeTypeBatch,
	"29": NodeTypeContinue,
	"30": NodeTypeInputReceiver,
	"31": NodeTypeComment,
	"32": NodeTypeVariableAggregator,
	"37": NodeTypeMessageList,
	"38": NodeTypeClearConversationHistory,
	"39": NodeTypeCreateConversation,
	"40": NodeTypeVariableAssigner,
	"42": NodeTypeDatabaseUpdate,
	"43": NodeTypeDatabaseQuery,
	"44": NodeTypeDatabaseDelete,
	"45": NodeTypeHTTPRequester,
	"46": NodeTypeDatabaseInsert,
	"51": NodeTypeConversationUpdate,
	"52": NodeTypeConversationDelete,
	"53": NodeTypeConversationList,
	"54": NodeTypeConversationHistory,
	"55": NodeTypeCreateMessage,
	"56": NodeTypeEditMessage,
	"57": NodeTypeDeleteMessage,
	"58": NodeTypeJsonSerialization,
	"59": NodeTypeJsonDeserialization,
	"60": NodeTypeKnowledgeDeleter,
	// 16 / 23 / 65 都是 coze-biz canvas 面的**真实码**（2026-09-30 一手复算：
	// 350 份 workflow.json 里 16×165 图像生成 · 23×112 画板 · 65×10 视频生成，
	// 65 的 nodeMeta.title 全为「视频生成」）。**65 是 coze-biz 的扩展码**
	// （开源名册 internal/backend/coze/（商业仓）node_type.go 无此码）。
	// yaml 侧适配器（platforms/cozebiz/decompile/adapter.go::YamlTypeToCanvasID）
	// 走具名枚举，与数字码**同解**；码面权威 = internal/backend/cozebiz/（商业仓）
	// node_types.go::NodeTypeTable；两侧同解由判据
	// platforms/cozebiz/decompile/canvas_numeric_code_contract_test.go 锁住。
	"65":   NodeTypeVideoGeneration,
	"1000": NodeTypeLambda,
}

// NodeIDByType 是 NodeType -> Canvas 数字 ID 的反向映射。
var NodeIDByType = func() map[NodeType]string {
	m := make(map[NodeType]string, len(NodeTypeByID))
	for id, t := range NodeTypeByID {
		m[t] = id
	}
	return m
}()

// nodeTypeAliases maps semantic type strings used by platform artifacts to
// their canonical AST node types.  They are artifact-facing aliases, not new
// node kinds: canonical AST, IR and lowering must see the one semantic type.
var nodeTypeAliases = map[string]NodeType{
	"from_json": NodeTypeJsonDeserialization,
	"to_json":   NodeTypeJsonSerialization,
}

// ParseNodeType 解析 Canvas 数字 ID 或语义化 NodeType 名称。
func ParseNodeType(value string) NodeType {
	if t, ok := NodeTypeByID[value]; ok {
		return t
	}
	if t, ok := nodeTypeAliases[value]; ok {
		return t
	}
	t, err := NodeTypeFromString(value)
	if err != nil {
		return NodeType(value)
	}
	return t
}
