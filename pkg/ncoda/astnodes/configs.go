package astnodes

import (
	"fmt"

	"github.com/nodecoda/ncoda/pkg/ncoda/typesystem"
)

// ExceptionConfig 节点异常处理配置（从 canvas settingOnError 解析）。
// ProcessType: 1=抛异常, 2=返回默认值, 3=返回空。None 时按 1 处理。
type ExceptionConfig struct {
	TimeoutMS   int
	MaxRetry    int
	DataOnErr   string
	ProcessType *int
}

// EffectiveProcessType 实际使用的 process_type（None -> 1）。
func (e *ExceptionConfig) EffectiveProcessType() int {
	if e.ProcessType != nil {
		return *e.ProcessType
	}
	return 1
}

// LLMToolConfig LLM 工具配置。
type LLMToolConfig struct {
	ResourceType string
	ResourceID   string
	ToolID       string
	Version      string
	Name         string
	IsDraft      bool
	PluginFrom   *int
	Settings     map[string]any
}

// LLMKnowledgeRecallConfig LLM 知识召回配置。
type LLMKnowledgeRecallConfig struct {
	KnowledgeIDs []string
	Settings     map[string]any
}

// ConversationOperation 会话操作字面量集合。
var ConversationOperations = map[string]struct{}{
	"CreateConversation":       {},
	"ConversationList":         {},
	"ConversationUpdate":       {},
	"ConversationDelete":       {},
	"ClearConversationHistory": {},
	"ConversationHistory":      {},
	"MessageList":              {},
	"CreateMessage":            {},
	"EditMessage":              {},
	"DeleteMessage":            {},
}

// SelectorClause Selector 子句。
type SelectorClause struct {
	Operator  string
	Operators []string
	Relation  string
}

// VariableAssignment 变量赋值。
type VariableAssignment struct {
	VariableType string
	Path         []string
	TypeInfo     *typesystem.TypeInfo
}

// HTTPAuthConfig HTTP 认证配置。
type HTTPAuthConfig struct {
	AuthType string
	Location string
}

// HTTPBodyConfig HTTP 请求体配置。
type HTTPBodyConfig struct {
	BodyType   string
	Template   string
	FileFields []string
}

// DatabaseOperation 数据库操作字面量。
type DatabaseOperation string

const (
	DatabaseOpEqual       DatabaseOperation = "EQUAL"
	DatabaseOpNotEqual    DatabaseOperation = "NOT_EQUAL"
	DatabaseOpGreaterThan DatabaseOperation = "GREATER_THAN"
	DatabaseOpLessThan    DatabaseOperation = "LESS_THAN"
	DatabaseOpGreaterEq   DatabaseOperation = "GREATER_EQUAL"
	DatabaseOpLessEq      DatabaseOperation = "LESS_EQUAL"
	DatabaseOpIn          DatabaseOperation = "IN"
	DatabaseOpNotIn       DatabaseOperation = "NOT_IN"
	DatabaseOpIsNull      DatabaseOperation = "IS_NULL"
	DatabaseOpIsNotNull   DatabaseOperation = "IS_NOT_NULL"
	DatabaseOpLike        DatabaseOperation = "LIKE"
	DatabaseOpNotLike     DatabaseOperation = "NOT_LIKE"
)

var validDatabaseOperations = map[DatabaseOperation]struct{}{
	DatabaseOpEqual: {}, DatabaseOpNotEqual: {}, DatabaseOpGreaterThan: {},
	DatabaseOpLessThan: {}, DatabaseOpGreaterEq: {}, DatabaseOpLessEq: {},
	DatabaseOpIn: {}, DatabaseOpNotIn: {}, DatabaseOpIsNull: {},
	DatabaseOpIsNotNull: {}, DatabaseOpLike: {}, DatabaseOpNotLike: {},
}

// ValidateDatabaseOperation 校验数据库操作。
func ValidateDatabaseOperation(op string) (DatabaseOperation, error) {
	o := DatabaseOperation(op)
	if _, ok := validDatabaseOperations[o]; !ok {
		return "", fmt.Errorf("not a valid Operation string: %s", op)
	}
	return o, nil
}

// DatabaseClause 数据库子句。
type DatabaseClause struct {
	Left     string
	Operator DatabaseOperation
}

// DatabaseClauseGroup 数据库子句组。
type DatabaseClauseGroup struct {
	Single        *DatabaseClause
	MultiClauses  []DatabaseClause
	MultiRelation string // "AND" | "OR"
}

// IsEmpty 判断是否空组。
func (g *DatabaseClauseGroup) IsEmpty() bool {
	return g.Single == nil && len(g.MultiClauses) == 0
}

// DatabaseOrderClause 数据库排序子句。
type DatabaseOrderClause struct {
	FieldID string
	IsAsc   bool
}

// KnowledgeParseMode / KnowledgeChunkType / KnowledgeSearchType 字面量。
type KnowledgeParseMode string

const (
	KnowledgeParseFast     KnowledgeParseMode = "fast"
	KnowledgeParseAccurate KnowledgeParseMode = "accurate"
)

type KnowledgeChunkType string

const (
	KnowledgeChunkCustom  KnowledgeChunkType = "custom"
	KnowledgeChunkDefault KnowledgeChunkType = "default"
)

type KnowledgeSearchType string

const (
	KnowledgeSearchSemantic KnowledgeSearchType = "semantic"
	KnowledgeSearchHybrid   KnowledgeSearchType = "hybrid"
	KnowledgeSearchFullText KnowledgeSearchType = "full_text"
)

// ValidateParseMode 校验解析模式。
func ValidateParseMode(p string) (KnowledgeParseMode, error) {
	switch KnowledgeParseMode(p) {
	case KnowledgeParseFast, KnowledgeParseAccurate:
		return KnowledgeParseMode(p), nil
	}
	return "", fmt.Errorf("invalid parsingType: %s", p)
}

// ValidateChunkType 校验分块类型。
func ValidateChunkType(p string) (KnowledgeChunkType, error) {
	switch KnowledgeChunkType(p) {
	case KnowledgeChunkCustom, KnowledgeChunkDefault:
		return KnowledgeChunkType(p), nil
	}
	return "", fmt.Errorf("invalid ChunkType: %s", p)
}

var searchTypeMap = map[int]KnowledgeSearchType{
	0:  KnowledgeSearchSemantic,
	1:  KnowledgeSearchHybrid,
	20: KnowledgeSearchFullText,
}

// ConvertSearchType 转换检索类型。
func ConvertSearchType(s int) (KnowledgeSearchType, error) {
	if st, ok := searchTypeMap[s]; ok {
		return st, nil
	}
	return "", fmt.Errorf("invalid RetrievalSearchType %d", s)
}

// ParsingStrategyConfig 解析策略配置。
type ParsingStrategyConfig struct {
	ParseMode    KnowledgeParseMode
	ImageOCR     bool
	ExtractImage bool
	ExtractTable bool
}

// ChunkingStrategyConfig 分块策略配置。
type ChunkingStrategyConfig struct {
	ChunkType KnowledgeChunkType
	Separator string
	ChunkSize int
	Overlap   int
}

// RetrievalStrategyConfig 检索策略配置。
type RetrievalStrategyConfig struct {
	TopK               *int
	EnableRerank       bool
	EnableQueryRewrite bool
	IsPersonalOnly     bool
	EnableNL2SQL       bool
	MinScore           *float64
	SearchType         *KnowledgeSearchType
}

// ChatHistorySettingConfig 聊天历史设置配置。
type ChatHistorySettingConfig struct {
	EnableChatHistory bool
	ChatHistoryRound  int
}
