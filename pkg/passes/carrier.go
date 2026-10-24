package passes

// carrier.go —— 目标**承载面**（representability facts）的轴词汇 + 语言缺省。
//
// 边界（ASSET-BOUNDARY D8）：内核拥有「有哪些轴」以及一个语言缺省；每个平台
// 适配器声明「这些轴取什么值」。平台事实（节点形状、字段承载面）属于适配器，
// 不进语言。
//
// 过渡（P2d 发版批次）：商业仓 v0.6.0 的 profile 还不带 Carrier，故
// carrierOf 在未声明时按 profile 名回退到当前事实（carrierFromName）。内核
// 打 tag、商业仓 bump 之后，适配器改为显式声明，carrierFromName 整段删除 ——
// 从那时起内核不认识任何平台名。
//
// 判据（scripts/audit_split.py S6）：本文件是内核里唯一知道历史目标名的代码
// 位置，且只以字符串字面量出现（不是标识符）；台账
// docs/PLATFORM-IDENTITY-RESIDUALS.yaml 的 transitional_name_gates 记录它。

import "strings"

// NativeTargetName is the language's own target identity: the default profile
// a standalone ncoda program is compiled and run against. It is deliberately
// not a platform — an adapter declares its own name.
const NativeTargetName = "ncoda-native-1.0"

// CarrierProfile declares one target's representability facts. The zero value
// means "nothing declared": the conservative reading is that the target
// carries no optional axis (never invent a capability).
type CarrierProfile struct {
	// Declared separates "this profile declares its facts" from the zero
	// value of a hand-built profile that has not. Adapters set it true.
	Declared bool

	// NodeGraphShapes makes TargetValidationPass enforce node-graph shape
	// constraints (Start input shapes, explicit nulls, single-file field
	// access …). false = language-level judgements only.
	NodeGraphShapes bool

	FileContract FileContractCarrier
	// TimeoutAxis is the axis a non-http node's timeout carrier expresses.
	TimeoutAxis TimeoutCarrierAxis
	// RetryInterval says whether the target consumes the retry-interval axis.
	RetryInterval RetryIntervalCarrier
	// EnumValueSet: the target's product carries an enum's value set.
	EnumValueSet bool
	// ExplicitNull: the target carries an explicit null initial value.
	ExplicitNull bool
	// ParallelOnError: the target carries a batch's on_error axis.
	ParallelOnError bool
	// QAActionValueSet: the target's question widget carries the action
	// enum's value set (an options table exists).
	QAActionValueSet bool

	Ask AskCarrierAxes
}

// FileContractCarrier is one target's physical carrying surface for the three
// file-contract axes. Zero = carries none (conservative).
type FileContractCarrier struct {
	Kinds        bool // a file-kind carrier exists
	KindsAreSets bool // that carrier holds a set (multi-value), not one value
	Extensions   bool // an extension carrier exists
	Uploads      bool // an upload-method carrier exists
}

// TimeoutCarrierAxis is what a target's non-http timeout carrier measures.
type TimeoutCarrierAxis int

const (
	// TimeoutAxisUnknown: no evidence for this target ⇒ state nothing.
	TimeoutAxisUnknown TimeoutCarrierAxis = iota
	// TimeoutAxisPerAttempt: the carrier is per attempt (matches the language
	// ruling for `with timeout(D)`).
	TimeoutAxisPerAttempt
	// TimeoutAxisSharedBudget: the carrier is one budget shared by all
	// retries ⇒ the language axis is degraded and must be reported.
	TimeoutAxisSharedBudget
)

// RetryIntervalCarrier says whether a target consumes a retry interval.
type RetryIntervalCarrier int

const (
	// RetryIntervalUnknown: no evidence ⇒ state nothing.
	RetryIntervalUnknown RetryIntervalCarrier = iota
	// RetryIntervalCarried: the target really sleeps between attempts.
	RetryIntervalCarried
	// RetryIntervalAbsent: the target retries immediately ⇒ report.
	RetryIntervalAbsent
)

// AskCarrierAxes is one target's carrying surface for `ask` (human input).
// Known=false means the target declared nothing ⇒ make no claim and reject
// nothing on its behalf.
type AskCarrierAxes struct {
	Known bool
	// The three fidelity axes: the widget holds title / style / timeout.
	Title   bool
	Style   bool
	Timeout bool
	// RequiresActionID: the widget's action entries are identifiers, not
	// free text (members must be canonical identifiers).
	RequiresActionID bool
	// QuestionParams: the question widget carries the parameter face
	// (limit / extra_output / llm / dynamic_options).
	QuestionParams bool
	// Actionless: the target's carrier for the action-less form
	// (`ask<Form>({…}) as r;`) is wired.
	Actionless bool
	// NoTemplateSlotCarrier: the widget has no slot carrier, so a content
	// template with bindings is rejected instead of silently flattened.
	NoTemplateSlotCarrier bool
	// ModelProtocolPrefix: the node's model carrier is an integer, so the
	// language string must be "<prefix><digits>". Empty = no protocol
	// requirement.
	ModelProtocolPrefix string
}

// carrierOf resolves a profile's carrier: an adapter-declared value wins;
// otherwise the transitional name bridge supplies today's facts.
func carrierOf(profile *TargetProfile) CarrierProfile {
	if profile == nil {
		return referenceCarrier()
	}
	if profile.Carrier.Declared {
		return profile.Carrier
	}
	return carrierFromName(profile.Name)
}

// referenceCarrier is the reference node-graph carrier: the facts the
// language's reference oracle declared. DefaultTargetProfile publishes it
// until the adapters inject their own (P2d); it is not a platform identity.
func referenceCarrier() CarrierProfile {
	return CarrierProfile{
		Declared:         true,
		NodeGraphShapes:  true,
		FileContract:     FileContractCarrier{Kinds: true, KindsAreSets: true, Extensions: true, Uploads: true},
		TimeoutAxis:      TimeoutAxisPerAttempt,
		RetryInterval:    RetryIntervalCarried,
		EnumValueSet:     true,
		ExplicitNull:     true,
		ParallelOnError:  true,
		QAActionValueSet: true,
		Ask: AskCarrierAxes{
			Known: true, Title: true, Style: true, Timeout: true, RequiresActionID: true,
			NoTemplateSlotCarrier: true,
			ModelProtocolPrefix:   "coze:",
		},
	}
}

// bizCarrier is the node-graph carrier with a single-value file kind, a
// shared timeout budget, no retry interval, and the question parameter face.
func bizCarrier() CarrierProfile {
	return CarrierProfile{
		Declared:         true,
		FileContract:     FileContractCarrier{Kinds: true},
		TimeoutAxis:      TimeoutAxisSharedBudget,
		RetryInterval:    RetryIntervalAbsent,
		QAActionValueSet: true,
		Ask: AskCarrierAxes{
			Known: true, QuestionParams: true, Actionless: true,
			ModelProtocolPrefix: "coze:",
		},
	}
}

// studioCarrier is the node-graph carrier with no file-contract surface, a
// shared timeout budget, and no retry interval.
func studioCarrier() CarrierProfile {
	return CarrierProfile{
		Declared:         true,
		TimeoutAxis:      TimeoutAxisSharedBudget,
		RetryInterval:    RetryIntervalAbsent,
		QAActionValueSet: true,
		Ask:              AskCarrierAxes{Known: true, ModelProtocolPrefix: "coze:"},
	}
}

// runtimeCarrier is the execution carrier: it consumes the retry interval
// and the batch on_error axis directly.
func runtimeCarrier() CarrierProfile {
	return CarrierProfile{
		Declared:        true,
		RetryInterval:   RetryIntervalCarried,
		ParallelOnError: true,
	}
}

// carrierFromName is the transitional bridge for profiles built before the
// adapters injected a Carrier (kernel tag + commercial bump = P2d release
// batch). It is the only place in the open kernel that maps a historical
// target name to facts; delete it once every adapter sets Carrier.
//
// The language's own name resolves to the reference carrier: profiles are
// routinely built by copying DefaultTargetProfile() and renaming, so the
// carrier must follow the *name*, not ride along in the struct.
func carrierFromName(name string) CarrierProfile {
	switch {
	case name == NativeTargetName, strings.HasPrefix(name, "dify"):
		return referenceCarrier()
	case strings.HasPrefix(name, "coze-biz"):
		return bizCarrier()
	case strings.HasPrefix(name, "coze-studio"), strings.HasPrefix(name, "coze-1."):
		return studioCarrier()
	case strings.Contains(name, "runtime"):
		return runtimeCarrier()
	}
	return CarrierProfile{}
}
