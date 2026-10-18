package binding

// Immutable frontend contracts for Human Input suspension statements, ported
// 1:1 from src/nclang/lang/human_input_contract.py (108 lines). The Python
// frozen dataclasses with __post_init__ invariants become Go structs built
// through validated constructors; the same invariant text is reused for the
// error messages.
//
// Deviations (recorded in task-17c2a-report.md):
//   - Python freezes descriptors with freeze_type; the Go types.Descriptor is
//     immutable by construction convention, so no copy is needed.
//   - Python requires the form record to carry a name
//     (ValidatedHumanInputContract.__post_init__: "Human Input form record
//     must have a name"); the Go types.Descriptor drops record names (Task 7
//     deviation: RecordType(name, fields) reserves but does not store the
//     name), so that single check is not portable and is skipped.

import (
	"regexp"
	"unicode/utf8"

	"github.com/nodecoda/ncoda/pkg/types"
)

// HumanInputFieldKind mirrors human_input_contract.py HumanInputFieldKind.
type HumanInputFieldKind string

const (
	HumanInputFieldKindParagraph HumanInputFieldKind = "paragraph"
	HumanInputFieldKindSelect    HumanInputFieldKind = "select"
)

// HumanInputActionStyle mirrors HumanInputActionStyle.
type HumanInputActionStyle string

const (
	HumanInputActionStylePrimary HumanInputActionStyle = "primary"
	HumanInputActionStyleDefault HumanInputActionStyle = "default"
	HumanInputActionStyleAccent  HumanInputActionStyle = "accent"
	HumanInputActionStyleGhost   HumanInputActionStyle = "ghost"
)

// HumanInputTimeoutUnit mirrors HumanInputTimeoutUnit.
type HumanInputTimeoutUnit string

const (
	HumanInputTimeoutUnitHour HumanInputTimeoutUnit = "hour"
	HumanInputTimeoutUnitDay  HumanInputTimeoutUnit = "day"
)

// validActionStyles is the closed style set (HumanInputActionStyle literal).
var validActionStyles = map[HumanInputActionStyle]bool{
	HumanInputActionStylePrimary: true,
	HumanInputActionStyleDefault: true,
	HumanInputActionStyleAccent:  true,
	HumanInputActionStyleGhost:   true,
}

var actionIDPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidActionStyle reports whether style is one of the canonical Dify
// human-input action button styles (primary/default/accent/ghost).
func ValidActionStyle(style HumanInputActionStyle) bool {
	return validActionStyles[style]
}

// IsCanonicalHumanInputActionID mirrors is_canonical_human_input_action_id
// (human_input_contract.py:25-27): value must match Dify's exact action ID
// wire contract — a canonical identifier of at most 20 characters.
func IsCanonicalHumanInputActionID(value string) bool {
	return len(value) <= 20 && actionIDPattern.MatchString(value)
}

// HumanInputFieldContract mirrors HumanInputFieldContract
// (human_input_contract.py:29-47). Options preserves the enum declaration
// order for select fields (empty for paragraph fields).
type HumanInputFieldContract struct {
	Name     string
	Kind     HumanInputFieldKind
	TypeInfo *types.Descriptor
	Options  []string
}

// NewHumanInputFieldContract validates and builds one field contract,
// mirroring __post_init__ (human_input_contract.py:35-46). It returns an
// error carrying the Python ValueError message text when the shape is
// invalid.
func NewHumanInputFieldContract(name string, kind HumanInputFieldKind, typeInfo *types.Descriptor, options []string) (*HumanInputFieldContract, error) {
	if name == "" {
		return nil, errHumanInput("Human Input field name must not be empty")
	}
	switch kind {
	case HumanInputFieldKindParagraph:
		if typeInfo == nil || typeInfo.Kind != types.String || len(options) > 0 {
			return nil, errHumanInput("Human Input paragraph fields require string without options")
		}
	case HumanInputFieldKindSelect:
		if !isWellFormedEnumType(typeInfo) {
			return nil, errHumanInput("Human Input select fields require a nominal enum")
		}
		if !StringSlicesEqual(options, typeInfo.EnumMembers) {
			return nil, errHumanInput("Human Input select options must preserve enum declaration order")
		}
	default:
		return nil, errHumanInput("Unsupported Human Input field kind: " + string(kind))
	}
	return &HumanInputFieldContract{Name: name, Kind: kind, TypeInfo: typeInfo, Options: append([]string(nil), options...)}, nil
}

// HumanInputActionContract mirrors HumanInputActionContract
// (human_input_contract.py:49-61).
type HumanInputActionContract struct {
	Member string
	Title  string
	Style  HumanInputActionStyle
}

// NewHumanInputActionContract validates and builds one action contract,
// mirroring __post_init__ (human_input_contract.py:55-60).
func NewHumanInputActionContract(member, title string, style HumanInputActionStyle) (*HumanInputActionContract, error) {
	if member == "" || !IsCanonicalHumanInputActionID(member) {
		return nil, errHumanInput("Human Input action member must be a canonical identifier of at most 20 characters")
	}
	// Python len(title) counts code points, so runes, not UTF-8 bytes
	// (R1 M1; the pass-level check mirrors this limit).
	if title == "" || utf8.RuneCountInString(title) > 100 {
		return nil, errHumanInput("Human Input action title must contain at most 100 characters")
	}
	if !validActionStyles[style] {
		return nil, errHumanInput("Unsupported Human Input action style: " + string(style))
	}
	return &HumanInputActionContract{Member: member, Title: title, Style: style}, nil
}

// HumanInputLLM 是 ask 宿主节点**自带**的模型配置（平台 coze-biz `question`
// 的 `llmParam`，平台类型 = `SimpleLLMParam` —— 它是一个**扁平标量表**，
// 与 llm 节点的 `llmParam`（`[{name, input}]` 行表）**不同形**，一手 =
// 语料 `7574994076144779304_calendar_helper` 节点 145373/156803）。
//
// 它是**节点级**配置，不是一次 llm() 调用的采样参数 —— 平台的读点：
//
//   - `answer_type: text` + `extra_output: true`（结构化抽取）：模型 + `systemPrompt`
//   - `limit` 共同定义「从用户作答里抽 ≤limit 个字段」（coze-studio
//     `nodes/qa/question_answer.go::Adapt` 的 `AnswerDirectly` 分支在 `ExtractOutput`
//     为真时**要求** `LLMParams`，否则报错）；
//   - `answer_type: option`：同一份 `llmParam` 也读（注释原文：intent recognize）。
//
// 键词汇 = `llm()` 的旋钮词汇（**一处定义**：UNIFIED-LLM-SEMANTICS §1 / R-70）：
// `model` 是 `"coze:<modelType>"` 协议串、`model_name` 是显示名载体，其余 1:1。
//
// **每个字段都是指针**：`nil` = 平台导出**没写这个键**；`&""` / `&0` = 写了、
// 值恰是空串/零。平台导出会写 `systemPrompt: ""`（一手，同源 2/2 例）⇒
// 「缺席」与「写了空值」必须可分辨，否则往返闭不上（键存在性判据按**有无**
// 分类，见 scripts/five-step/platform_delta.py::_presence）。**不发明**平台没写
// 的键 —— 这正是 2026-09-27 修掉的「无 config 也写回 temperature=0.5」那条纪律。
type HumanInputLLM struct {
	Model               *string  // `model` = "coze:<modelType>" → wire `modelType`（int）
	ModelName           *string  // `model_name` → wire `modelName`
	Temperature         *float64 // `temperature`
	MaxTokens           *int     // `max_tokens`
	TopP                *float64 // `top_p`
	ResponseFormat      *int     // `response_format`
	GenerationDiversity *string  // `generation_diversity`
	SystemPrompt        *string  // `system_prompt`
}

// HasAny 报「这份配置至少携带一个键」：全缺席（nil）⇒ 平台侧无键可发 ⇒
// 与「没有 llm 配置」不可观察地等价（同 `IntervalExplicit` 的口径）。
func (l *HumanInputLLM) HasAny() bool {
	if l == nil {
		return false
	}
	return l.Model != nil || l.ModelName != nil || l.Temperature != nil ||
		l.MaxTokens != nil || l.TopP != nil || l.ResponseFormat != nil ||
		l.GenerationDiversity != nil || l.SystemPrompt != nil
}

// ValidatedHumanInputContract mirrors ValidatedHumanInputContract
// (human_input_contract.py:63-95): the published, closed contract one
// validated request_input statement lowers against.
type ValidatedHumanInputContract struct {
	FormType *types.Descriptor
	// ActionType == nil = **无动作形态**（S1）：平台 `answer_type: text`
	// 的问答节点（无选项、无分支口、单条无标签推进口）。
	ActionType   *types.Descriptor
	Content      string
	Fields       []HumanInputFieldContract
	Actions      []HumanInputActionContract
	TimeoutValue int
	TimeoutUnit  HumanInputTimeoutUnit
	// ── coze-biz `question` 参数面的 ②③ 两块（2026-10-10 用户批准「丙·扩语言」）──
	// 平台这三个键**原生存在**（`Adapt` 逐字读它们），语言面无它们就等于往返丢
	// 一整个「从作答抽结构化输出」特征 ⇒ 加槽承载。
	//
	// MaxAnswerCount = 平台 `limit`（coze-studio `Adapt`: `c.MaxAnswerCount = qaConf.Limit`；
	// 消费点只在 `needExtractFromAnswer` 下）。0 = 缺席（与平台 `omitempty` 同义）。
	MaxAnswerCount int
	// ExtractFromAnswer = 平台 `extra_output`（= `ExtractFromAnswer`）。
	// false = 缺席。为真时平台**要求**模型（`llm != nil`）—— 见构造器不变量。
	ExtractFromAnswer bool
	// LLM = 平台 `llmParam`（节点自带模型配置）。nil = 该节点没有这个键。
	LLM *HumanInputLLM
}

// NewValidatedHumanInputContract validates and builds the closed contract,
// mirroring __post_init__ (human_input_contract.py:71-93).
func NewValidatedHumanInputContract(
	formType, actionType *types.Descriptor,
	content string,
	fields []HumanInputFieldContract,
	actions []HumanInputActionContract,
	timeoutValue int,
	timeoutUnit HumanInputTimeoutUnit,
	maxAnswerCount int,
	extractFromAnswer bool,
	llm *HumanInputLLM,
) (*ValidatedHumanInputContract, error) {
	if formType == nil || formType.Kind != types.Record {
		return nil, errHumanInput("Human Input form must be a nominal record")
	}
	// Python additionally requires the form record name to be non-empty
	// (human_input_contract.py:76-77); the Go descriptor drops record names
	// (Task 7 deviation) so the check is not portable. Recorded in the task
	// report.
	if actionType == nil {
		// **无动作形态**（S1，`ask<Form>({content, fields}) as r;`）：没有 action
		// 类型 ⇒ 契约里没有动作臂。行动臂必须为空 —— 否则「无类型的臂」既没有
		// 成员可校验、也没有载体可落（用户 2026-10-01 批准的形态）。
		if len(actions) != 0 {
			return nil, errHumanInput("Human Input without an action type has no action contracts")
		}
	} else if !isWellFormedEnumType(actionType) {
		return nil, errHumanInput("Human Input action type must be a nominal enum")
	}
	fieldNames := make([]string, 0, len(fields))
	for _, field := range fields {
		fieldNames = append(fieldNames, field.Name)
	}
	recordFieldNames := make([]string, 0, len(formType.Fields))
	for _, field := range formType.Fields {
		recordFieldNames = append(recordFieldNames, field.Name)
	}
	if !StringSlicesEqual(fieldNames, recordFieldNames) {
		return nil, errHumanInput("Human Input fields must cover the form record in declaration order")
	}
	if actionType != nil {
		actionMembers := make([]string, 0, len(actions))
		for _, action := range actions {
			actionMembers = append(actionMembers, action.Member)
		}
		if !StringSlicesEqual(actionMembers, actionType.EnumMembers) {
			return nil, errHumanInput("Human Input actions must cover the action enum in declaration order")
		}
	}
	if content == "" {
		return nil, errHumanInput("Human Input content must not be empty")
	}
	// timeout 可选：0/"no timeout" 合法，否则必须为正整数+合法单位。
	if timeoutValue == 0 && timeoutUnit == "" {
		// 无超时，合法
	} else if timeoutValue <= 0 {
		return nil, errHumanInput("Human Input timeout value must be a positive integer")
	} else if timeoutUnit != HumanInputTimeoutUnitHour && timeoutUnit != HumanInputTimeoutUnitDay {
		return nil, errHumanInput("Human Input timeout unit must be hour or day")
	}
	// coze-biz `question` 的 ②③ 两块（2026-10-10）：平台的读点自己给出的
	// 不变量 —— `Adapt` 在 `ExtractOutput && llmParams == nil` 时**报错**
	// （"qa node needs to extract from answer, but LLMParams not provided"）
	// ⇒ 语言侧同款 fail loud，不静默发一个平台必拒/必废的产物。
	if maxAnswerCount < 0 {
		return nil, errHumanInput("Human Input max answer count must not be negative")
	}
	if extractFromAnswer && !llm.HasAny() {
		return nil, errHumanInput("Human Input with extra_output requires an llm config (the platform needs the node's model to extract from the answer)")
	}
	return &ValidatedHumanInputContract{
		FormType:          formType,
		ActionType:        actionType,
		Content:           content,
		Fields:            append([]HumanInputFieldContract(nil), fields...),
		Actions:           append([]HumanInputActionContract(nil), actions...),
		TimeoutValue:      timeoutValue,
		TimeoutUnit:       timeoutUnit,
		MaxAnswerCount:    maxAnswerCount,
		ExtractFromAnswer: extractFromAnswer,
		LLM:               llm,
	}, nil
}

// errHumanInput wraps one contract invariant as an error (the Go analogue of
// the Python ValueError raised in __post_init__).
func errHumanInput(message string) error {
	return &humanInputContractError{message: message}
}

type humanInputContractError struct{ message string }

func (e *humanInputContractError) Error() string { return e.message }

// isWellFormedEnumType mirrors types.py is_well_formed_enum_type (line 264):
// a nominal enum descriptor with a non-empty name, non-empty ordered members
// that are all non-empty strings and duplicate-free.
func isWellFormedEnumType(d *types.Descriptor) bool {
	if d == nil || d.Kind != types.Enum {
		return false
	}
	if d.EnumName == "" || len(d.EnumMembers) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, member := range d.EnumMembers {
		if member == "" || seen[member] {
			return false
		}
		seen[member] = true
	}
	return true
}

// stringSlicesEqual reports slice equality in order (the Go form of the
// Python tuple equality used by the contract invariants).
func StringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
