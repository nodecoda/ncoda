package passes

// carrier.go —— 目标**承载面**（representability facts）的轴词汇 + 语言缺省。
//
// 边界（ASSET-BOUNDARY D8）：内核拥有「有哪些轴」以及语言自己的两个缺省
// （参考目标 = 语言定义的严格承载面；语言中立 = 什么都不声称承载）。每个平台
// 适配器在自己的仓里声明「这些轴取什么值」，通过 TargetProfile.Carrier 注入。
//
// 内核**不认识任何平台名**：没有按名查表的回退。未声明 Carrier 的 profile
// 承载面为零 —— 「不发明能力」是唯一安全的缺省。
//
// 判据（scripts/audit_split.py S6/S7）：本文件与内核其余部分一样，不含平台名。

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

// carrierOf resolves a profile's carrier: an adapter-declared value wins; a
// profile that declares nothing carries nothing (never invent a capability).
func carrierOf(profile *TargetProfile) CarrierProfile {
	if profile == nil {
		return ReferenceCarrier()
	}
	if profile.Carrier.Declared {
		return profile.Carrier
	}
	return CarrierProfile{}
}

// ReferenceCarrier is the language reference target's carrier: the strictest
// representability surface the language itself defines — a node-graph product
// that carries every optional axis. It is a language declaration, not a
// platform identity: an adapter that carries a different surface declares its
// own (TargetProfile.Carrier).
func ReferenceCarrier() CarrierProfile {
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
		},
	}
}

// LanguageCarrier is the language-neutral carrier: a standalone ncoda program
// is judged by the language alone, so no optional axis is claimed carried.
// The target-free language lane (`--target __none__`) runs under this carrier.
func LanguageCarrier() CarrierProfile { return CarrierProfile{Declared: true} }
