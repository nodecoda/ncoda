// Package diag defines the shared diagnostic vocabulary for the compiler.
// DSLDiagnosticCode mirrors the 58-code enum in the Python reference
// (src/nclang/lang/passes/base.py:DSLDiagnosticCode); values are extracted
// verbatim so parity is mechanical and drift is detectable.
package diag

import (
	"fmt"

	"github.com/nodecoda/ncoda/pkg/token"
)

// Code is a DSL diagnostic code. Error codes are 1xxx, warnings 2xxx,
// mirroring the Python enum.
type Code int

const (
	// Errors (1xxx)
	SYNTAX_ERROR              Code = 1000
	UNDEFINED_VAR             Code = 1001
	DUPLICATE_DEF             Code = 1002
	TYPE_MISMATCH             Code = 1003
	ARG_COUNT                 Code = 1004
	RETURN_TYPE               Code = 1005
	NOT_CALLABLE              Code = 1006
	NOT_ITERABLE              Code = 1007
	CONDITION_NOT_BOOL        Code = 1008
	NO_ENTRY                  Code = 1009
	MISSING_RETURN            Code = 1010
	UNREACHABLE_CODE          Code = 1011
	BINARY_TYPE               Code = 1012
	INDEX_TYPE                Code = 1013
	FIELD_NOT_FOUND           Code = 1014
	UNKNOWN_TYPE              Code = 1015
	NOT_IN_LOOP               Code = 1016
	ASSIGN_IMMUTABLE          Code = 1018
	INVALID_MODE              Code = 1019
	ENTRY_NO_RETURN           Code = 1020
	CODE_INPUT_TYPE           Code = 1021
	CODE_INPUT_SELECTOR       Code = 1022
	CODE_DUPLICATE_INPUT      Code = 1023
	CODE_SOURCE_REQUIRED      Code = 1024
	CODE_OUTPUT_INVALID       Code = 1025
	CODE_DUPLICATE_OUTPUT     Code = 1026
	CODE_LANGUAGE_UNSUPPORTED Code = 1027
	CODE_SOURCE_INVALID       Code = 1028
	CODE_PORT_INVALID         Code = 1030
	STDLIB_STATIC_ARGUMENT    Code = 1031
	STDLIB_CALL_CONTEXT       Code = 1032
	RECURSIVE_CALL            Code = 1033
	TARGET_NOT_LOWERABLE      Code = 1034
	PARALLEL_EXPORT_CONFLICT  Code = 1035
	PARALLEL_ACCESS_HAZARD    Code = 1036
	PARALLEL_EXTERNAL_EXIT    Code = 1037
	DUPLICATE_TYPE            Code = 1038
	OPTIONAL_REQUIRED         Code = 1039
	ANY_NOT_NARROWED          Code = 1040
	EFFECT_CONTEXT            Code = 1041
	INVALID_STATE             Code = 1042
	YIELD_CONTRACT            Code = 1043
	RESPONSE_CONTEXT          Code = 1044
	OPERATION_POLICY          Code = 1045
	TARGET_PROFILE_UNKNOWN    Code = 1046
	SELECTOR_NO_PRODUCER      Code = 1047
	UNINITIALIZED_USE         Code = 1048
	EMPTY_AGGREGATE_TYPE      Code = 1049
	LANGUAGE_IDENTITY         Code = 1050
	SHADOWED_NAMESPACE        Code = 1051
	IMPORT_PLATFORM_REQUIRED  Code = 1052
	CHATFLOW_ENTRY            Code = 1054
	// RECORD_FIELD_REQUIRED (1056, Go-native)：多端口 record 值没有单一承载口
	// （平台画布上它的成员就是端口，没有「整个 record」那个口）⇒ 非出口位置
	// 必须写明字段，或把整值放到 `return` 里由出口逐字段具名输出（A12-H4，
	// 2026-09-17 用户裁定 (b)）。1055 已被标准占用（已撤提案），号段不回收。
	RECORD_FIELD_REQUIRED Code = 1056
	// 答复模板（W2，2026-09-17）：`return template … using {…}` 是**流程出口的
	// 值侧**（平台 `terminatePlan: useAnswerContent`），不是普通返回值 ——
	// 下面四条是它的位置 / 共存 / 身份 / 名额值判据。
	TEMPLATE_RETURN_PLACEMENT Code = 1057
	TEMPLATE_RETURN_MIXED     Code = 1058
	TEMPLATE_RETURN_IDENTITY  Code = 1059
	TEMPLATE_BINDING_INVALID  Code = 1060
	// CAPABILITY_OUTPUTS_UNDECLARED (1061, Go-native，2026-09-24 用户裁定
	// 「收紧」)：被调用的能力 op **没有输出表这一栏**（头文件 `function f(…) {}`
	// 没写 `-> T`）。输出表是头文件的义务（类比 C 函数声明缺返回类型 = 声明方
	// 的错误），不是使用方的降级场景 ⇒ 拒收并点名 provider/op/头文件，不再静默
	// 回落 `{result: any}`（`pkg/passes/type_infer.go`）。显式空输出表写
	// `-> void`（合法、非本码）。
	CAPABILITY_OUTPUTS_UNDECLARED Code = 1061
	// TEMPLATE_VALUE_PLACEMENT (1062, Go-native，2026-09-25 R-78)：`template "…"
	// [using {…}]` 是**节点级**载体（逐字文本 + 作者具名名额表）⇒ 只有三个承载位
	// 装得下它（`return template …` / `output(template …)` / llm message
	// `content: template …`）。落在**值通道**（let 初始化 / 数组元素 / 条件 /
	// 拼接操作数 / 调用实参……）是**位置错**（语言规则，不是平台不支持、不是编译器
	// 缺陷）：那里只物化「一个值」，没有放置名额表的位置。修前那四处各写一遍通用
	// 兜底 ⇒ 四种说法都看不出病根，其中两条还给出「先赋给变量」这个**正是失败那条
	// 路**的死建议。正本 = docs/five-step/spec/TEMPLATE-STRING-CANONICAL-FORM.md §4。
	TEMPLATE_VALUE_PLACEMENT Code = 1062
	LOWERING_INVARIANT       Code = 1099

	// Warnings (2xxx)
	DEAD_CODE                       Code = 2001
	UNUSED_VAR                      Code = 2002
	EMPTY_ENTRY                     Code = 2003
	EMPTY_FOR_BODY                  Code = 2004
	CODE_SOURCE_CONTRACT_UNVERIFIED Code = 2005
	CODE_SOURCE_CONTRACT_MISMATCH   Code = 2006
	ANY_FIELD_ACCESS                Code = 2010
	CROSS_TYPE_EQUALITY             Code = 2011
	REDUNDANT_COALESCE              Code = 2012 // route 1a: ?? 左操作数恒非 null
	// TARGET_CONTRACT_DEGRADED (2013, Go-native)：目标承载不了某个约束轴 ⇒ 软降级
	// （放宽 / 丢弃）。这是「平台无法表达 ⇒ 采用平台默认值」的**报账**：降级本身
	// 合法（不许变成 error —— 那会拒收合法的 ncoda 源码），但不许静默。
	// 方向纪律：只许放宽，不许收紧（多值不许取首）。见
	// pkg/passes/target_validation.go 的 fileContractCapacity。
	TARGET_CONTRACT_DEGRADED Code = 2013
	// LLM_CONFIG_DEPRECATED (2014, Go-native)：llm() config 的旧键 / 旧值形态
	// 仍在迁移窗口内。可编译（warning），但作者应改用规范名：
	//   generation_diversity → coze:generation_diversity
	//   model_name           → coze:model_name
	//   model / userPrompt / systemPrompt → 第一参数 / messages
	//   response_format 数值码 → "text" / "json" / "markdown"
	// 正本 = pkg/llm/vocabulary.go。
	LLM_CONFIG_DEPRECATED Code = 2014
)

// names mirrors the Python enum member names for stable serialization.
var names = map[Code]string{
	SYNTAX_ERROR: "SYNTAX_ERROR", UNDEFINED_VAR: "UNDEFINED_VAR",
	DUPLICATE_DEF: "DUPLICATE_DEF", TYPE_MISMATCH: "TYPE_MISMATCH",
	ARG_COUNT: "ARG_COUNT", RETURN_TYPE: "RETURN_TYPE",
	NOT_CALLABLE: "NOT_CALLABLE", NOT_ITERABLE: "NOT_ITERABLE",
	CONDITION_NOT_BOOL: "CONDITION_NOT_BOOL", NO_ENTRY: "NO_ENTRY",
	MISSING_RETURN: "MISSING_RETURN", UNREACHABLE_CODE: "UNREACHABLE_CODE",
	BINARY_TYPE: "BINARY_TYPE", INDEX_TYPE: "INDEX_TYPE",
	FIELD_NOT_FOUND: "FIELD_NOT_FOUND", UNKNOWN_TYPE: "UNKNOWN_TYPE",
	NOT_IN_LOOP:      "NOT_IN_LOOP",
	ASSIGN_IMMUTABLE: "ASSIGN_IMMUTABLE", INVALID_MODE: "INVALID_MODE",
	ENTRY_NO_RETURN: "ENTRY_NO_RETURN", CODE_INPUT_TYPE: "CODE_INPUT_TYPE",
	CODE_INPUT_SELECTOR: "CODE_INPUT_SELECTOR", CODE_DUPLICATE_INPUT: "CODE_DUPLICATE_INPUT",
	CODE_SOURCE_REQUIRED: "CODE_SOURCE_REQUIRED", CODE_OUTPUT_INVALID: "CODE_OUTPUT_INVALID",
	CODE_DUPLICATE_OUTPUT: "CODE_DUPLICATE_OUTPUT", CODE_LANGUAGE_UNSUPPORTED: "CODE_LANGUAGE_UNSUPPORTED",
	CODE_SOURCE_INVALID: "CODE_SOURCE_INVALID", CODE_PORT_INVALID: "CODE_PORT_INVALID",
	STDLIB_STATIC_ARGUMENT: "STDLIB_STATIC_ARGUMENT", STDLIB_CALL_CONTEXT: "STDLIB_CALL_CONTEXT",
	RECURSIVE_CALL: "RECURSIVE_CALL", TARGET_NOT_LOWERABLE: "TARGET_NOT_LOWERABLE",
	PARALLEL_EXPORT_CONFLICT: "PARALLEL_EXPORT_CONFLICT", PARALLEL_ACCESS_HAZARD: "PARALLEL_ACCESS_HAZARD",
	PARALLEL_EXTERNAL_EXIT: "PARALLEL_EXTERNAL_EXIT", DUPLICATE_TYPE: "DUPLICATE_TYPE",
	OPTIONAL_REQUIRED: "OPTIONAL_REQUIRED", ANY_NOT_NARROWED: "ANY_NOT_NARROWED",
	CHATFLOW_ENTRY: "CHATFLOW_ENTRY", RECORD_FIELD_REQUIRED: "RECORD_FIELD_REQUIRED",
	EFFECT_CONTEXT: "EFFECT_CONTEXT", INVALID_STATE: "INVALID_STATE",
	YIELD_CONTRACT: "YIELD_CONTRACT", RESPONSE_CONTEXT: "RESPONSE_CONTEXT",
	OPERATION_POLICY: "OPERATION_POLICY", TARGET_PROFILE_UNKNOWN: "TARGET_PROFILE_UNKNOWN",
	SELECTOR_NO_PRODUCER: "SELECTOR_NO_PRODUCER", UNINITIALIZED_USE: "UNINITIALIZED_USE",
	EMPTY_AGGREGATE_TYPE: "EMPTY_AGGREGATE_TYPE", LANGUAGE_IDENTITY: "LANGUAGE_IDENTITY",
	SHADOWED_NAMESPACE: "SHADOWED_NAMESPACE", IMPORT_PLATFORM_REQUIRED: "IMPORT_PLATFORM_REQUIRED",
	LOWERING_INVARIANT:        "LOWERING_INVARIANT",
	TEMPLATE_RETURN_PLACEMENT: "TEMPLATE_RETURN_PLACEMENT", TEMPLATE_RETURN_MIXED: "TEMPLATE_RETURN_MIXED",
	TEMPLATE_RETURN_IDENTITY: "TEMPLATE_RETURN_IDENTITY", TEMPLATE_BINDING_INVALID: "TEMPLATE_BINDING_INVALID",
	CAPABILITY_OUTPUTS_UNDECLARED: "CAPABILITY_OUTPUTS_UNDECLARED",
	TEMPLATE_VALUE_PLACEMENT:      "TEMPLATE_VALUE_PLACEMENT",
	DEAD_CODE:                     "DEAD_CODE", UNUSED_VAR: "UNUSED_VAR",
	EMPTY_ENTRY: "EMPTY_ENTRY", EMPTY_FOR_BODY: "EMPTY_FOR_BODY",
	CODE_SOURCE_CONTRACT_UNVERIFIED: "CODE_SOURCE_CONTRACT_UNVERIFIED",
	CODE_SOURCE_CONTRACT_MISMATCH:   "CODE_SOURCE_CONTRACT_MISMATCH",
	ANY_FIELD_ACCESS:                "ANY_FIELD_ACCESS",
	CROSS_TYPE_EQUALITY:             "CROSS_TYPE_EQUALITY",
	REDUNDANT_COALESCE:              "REDUNDANT_COALESCE",
	TARGET_CONTRACT_DEGRADED:        "TARGET_CONTRACT_DEGRADED",
	LLM_CONFIG_DEPRECATED:           "LLM_CONFIG_DEPRECATED",
}

// String returns the Python-equivalent enum member name.
func (c Code) String() string {
	if s, ok := names[c]; ok {
		return s
	}
	return fmt.Sprintf("DSLDiagnosticCode(%d)", int(c))
}

// IsError reports whether the code belongs to the 1xxx error range.
func (c Code) IsError() bool { return c >= 1000 && c < 2000 }

// Severity mirrors Severity in the Python reference.
type Severity int

const (
	Error Severity = iota
	Warning
)

func (s Severity) String() string {
	if s == Warning {
		return "warning"
	}
	return "error"
}

// Diagnostic is a structured compiler diagnostic with a source span.
// The End* fields mirror the end of the Python Diagnostic.location
// SourceSpan (tokens.py:214-220); they are zero for programmatic
// diagnostics without a location.
type Diagnostic struct {
	Code      Code
	Severity  Severity
	Message   string
	Filename  string
	Line      int // 1-based start line
	Column    int // 1-based start column
	Offset    int // 0-based start offset
	EndLine   int // 1-based end line (span end; 0 = no end captured)
	EndColumn int // 1-based end column
	EndOffset int // 0-based end offset
	// NumericWire marks diagnostics the Python reference emits as bare ints
	// (parser _coded_error/_expect_coded pass literal 1023-1030 codes), so
	// the JSON wire serializes str(code) instead of the enum member name.
	// Every other surface (lexer, pass, lowering) uses the DSLDiagnosticCode
	// enum and must keep the symbol name; only the parser path sets this.
	NumericWire bool
	// AdapterError marks the synthetic depth-limit diagnostic (P4-4): it
	// serializes as ADAPTER_ERROR / "maximum recursion depth exceeded"
	// with no location, byte-identical to Python's RecursionError catch.
	AdapterError bool
	// Class is the error class (see ClassLanguage/ClassPlatform/ClassInternal).
	// Empty = language (the default; keeps existing language diagnostics
	// byte-identical on the wire).
	Class string
	// Capability is the stable capability id for platform/internal
	// diagnostics ("loop.function_return", "container.nested", ...).
	Capability CapabilityID
	// Pending marks a **reducible-but-unimplemented** lowering
	// (class=internal): the platform has a physical channel, the compiler
	// has not implemented the translation yet — as opposed to an
	// irreducible platform limit (class=platform).
	Pending bool
}

// 错误类别（2026-09-13 用户拍板 D1/D3，AGENTS.md 契约）：
//
//	ClassLanguage —— ncoda 源码违反语言规则（语法/绑定/类型/语义）。
//	ClassPlatform —— **不可约**的平台物理限制：平台连通道都没有，用户必须改写法。
//	ClassInternal —— 编译器的锅：lowering 可约未实现，或产物缺陷（我们自己的 bug）。
//
// 缺省（空串）即 ClassLanguage；只有 platform / internal 才携带类别，因此现有
// 语言错诊断的 JSON 输出逐字节不变（加法式扩展，不动冻结的 58 码表）。
const (
	ClassLanguage = "language"
	ClassPlatform = "platform"
	ClassInternal = "internal"
)

// CapabilityID 是平台/内部类诊断的**稳定能力标识**（如
// "loop.function_return"、"container.nested"）。消费者按 (target, capability)
// 分流；能力清单的唯一声明点见 docs/PLATFORM-CAPABILITY.md。
type CapabilityID = string

// SetSpan stores the full source span (start + end) on the diagnostic,
// mirroring Python's Diagnostic.location SourceSpan. A zero span leaves the
// location fields empty (programmatic diagnostics, matching Python's
// location=None default).
func (d *Diagnostic) SetSpan(span token.Span) {
	if span == (token.Span{}) {
		return
	}
	d.Filename = span.Start.Filename
	d.Line = span.Start.Line
	d.Column = span.Start.Column
	d.Offset = span.Start.Offset
	d.EndLine = span.End.Line
	d.EndColumn = span.End.Column
	d.EndOffset = span.End.Offset
}

func (d Diagnostic) String() string {
	loc := d.Filename
	if loc == "" {
		loc = "<input>"
	}
	return fmt.Sprintf("%s:%d:%d: %s: %s (%s)", loc, d.Line, d.Column, d.Severity, d.Message, d.Code)
}
