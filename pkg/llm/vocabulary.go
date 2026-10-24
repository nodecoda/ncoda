package llm

// 平台证据注记（2026-10-25）：本文引用的平台源码/产物路径是**审计追溯**（当年判据的
// 一手证据）；平台实现与证据正本已随适配器移入商业仓（internal/backend/*、platforms/*）。
// 判据读的是 TargetProfile.Carrier / capability.Table 的轴，代码不认识平台名。
//

// vocabulary.go —— `llm(model, config)` 参数的**唯一词表**（2026-10-20）。
//
// 背景：同一批键此前散落在三处（语言类型表 / coze-biz lowering / gateway
// adapter 白名单），新增一个键要改三处，且三处可以静默分叉。本文件是语言侧的
// **一处定义**：每个键的档位、类型、规范名、别名都只在这里写一次。
//
// 档位判据（唯一一条）：
//
//	忽略这个键，会不会改变程序的可观察契约？
//	  会 → S（结构性）；只改变分布/成本 → T（调优）；
//	  只有某平台知道 → E（扩展）；作者写了、语言不认识 → U（未知名，开放字典）。
//
// 语言承诺：
//
//	S：目标必须执行；做不到要报错（当前实现见 docs/LLM-PARAM-SEMANTICS.md）。
//	T：目标尽力执行；未执行必须报账（warning，strict 升级）。
//	C：调用控制，由 runtime 拥有，不发给模型。
//	E：命名空间键（coze:* / openai:* / x-*），只在声明目标生效；
//	   其他目标静默忽略是约定，不是丢值。
//	U：不在词表里 = 原样透传；目标不消费时由目标侧报账（不在这里拒收）。
//
// 命名空间规则：
//
//	coze:*   → coze 绑定层剥掉前缀后写线；其他目标静默忽略。
//	openai:* → OpenAI 兼容 provider 绑定层剥前缀；其他目标静默忽略。
//	x-*      → 供应商自定义参数，按目标约定处理。
//
// 适配器自己的命名空间（例如某平台私有键）由适配器在绑定层处理：语言只声明
// 词表里已有的规范名与前缀，不替任何平台预留命名空间。
//
// **平台拼写不进语言**：语言写 max_tokens / top_p；coze-biz 写 maxTokens /
// topP，翻译在绑定层。本文件只写语言名与规范名（规范名可以带 coze: 前缀，
// 因为前缀本身就是语言层的「这是 coze 概念」声明）。

import "github.com/nodecoda/ncoda/pkg/types"

// Tier 是键的语义档位（见文件头判据）。
type Tier string

const (
	// TierStructural：忽略会改变程序的可观察契约（messages / response_format）。
	TierStructural Tier = "S"
	// TierTuning：只改变结果分布或成本（temperature / seed / max_tokens …）。
	TierTuning Tier = "T"
	// TierControl：调用控制，由 runtime 拥有，不发给模型（timeout_ms / stream）。
	TierControl Tier = "C"
	// TierExtension：平台 / provider 扩展，只在声明目标生效（coze:* / openai:* …）。
	TierExtension Tier = "E"
)

// KeySpec 描述一个 llm config 键的语言语义。
type KeySpec struct {
	// Name = 作者写的键名（规范名或别名）。
	Name string
	// Tier = 语义档位。
	Tier Tier
	// Type = 类型检查期望；nil = 开放（不在这里检查类型）。
	// 注意：OptionalType 表达「键缺失合法」，与现有类型表同口径。
	Type *types.Descriptor
	// AllowedValues 非空 = 字符串枚举白名单（如 response_format）。
	AllowedValues []string
	// AllowLegacyInt = 接受旧的数值码（response_format: 0/1/2）。
	// 旧码在类型检查时降级为 warning（可编译），新代码应写字符串枚举。
	AllowLegacyInt bool
	// Canonical = 规范名（别名才有值；"" = Name 本身就是规范名）。
	Canonical string
	// Deprecated 非空 = 废弃说明；类型检查阶段发 warning。
	Deprecated string
}

// vocabulary 是词表本体。顺序只影响文档展示；查找走 map。
var vocabulary = []KeySpec{
	// ── S 结构性 ───────────────────────────────────────────────────────
	// messages 的类型检查由 checkLLMMessageContents 单独完成（role/content
	// 必须是 string），所以这里的 Type 留空。
	{Name: "messages", Tier: TierStructural},
	// 语言写抽象枚举 text/json/markdown；provider 原生的结构化对象形
	// （{"type": "json_object"}，标准 LANGUAGE-REFERENCE 与示例 62 采用）
	// 与旧数值码 0/1/2 在迁移窗口内继续接受（type_infer 对 MapLit 放行）。
	{
		Name:           "response_format",
		Tier:           TierStructural,
		Type:           types.OptionalType(types.StringType()),
		AllowedValues:  []string{"text", "json", "markdown"},
		AllowLegacyInt: true,
	},

	// ── T 调优 ─────────────────────────────────────────────────────────
	{Name: "temperature", Tier: TierTuning, Type: types.OptionalType(types.FloatType())},
	{Name: "top_p", Tier: TierTuning, Type: types.OptionalType(types.FloatType())},
	{Name: "max_tokens", Tier: TierTuning, Type: types.OptionalType(types.IntType())},
	// seed：OpenAI 语义是整数；此前不在类型表 ⇒ `seed: "42"` 能编译、到上游才炸。
	{Name: "seed", Tier: TierTuning, Type: types.OptionalType(types.IntType())},
	// stop：OpenAI 接受 string 或 array<string>，当前类型系统没有 union ⇒
	// 先保持开放（nil），由目标侧校验取值形状。
	{Name: "stop", Tier: TierTuning},
	{Name: "presence_penalty", Tier: TierTuning, Type: types.OptionalType(types.FloatType())},
	{Name: "frequency_penalty", Tier: TierTuning, Type: types.OptionalType(types.FloatType())},
	{Name: "reasoning_effort", Tier: TierTuning, Type: types.OptionalType(types.StringType())},
	// reasoning_format：推理内容呈现方式（如 separated）；平台有载体，
	// coze-biz / gateway 目前无载体 ⇒ 由目标侧报账。
	{Name: "reasoning_format", Tier: TierTuning, Type: types.OptionalType(types.StringType())},

	// ── C 调用控制 ─────────────────────────────────────────────────────
	{Name: "timeout_ms", Tier: TierControl, Type: types.OptionalType(types.IntType())},
	{Name: "stream", Tier: TierControl, Type: types.OptionalType(types.BoolType())},
	// model：模型身份只认 llm() 第一参数。config 里的 model 今天在两个目标上
	// 都是惰性的（runtime 排除、coze-biz 平台不消费）⇒ 废弃，不再作为第二条
	// 解析路径。
	{Name: "model", Tier: TierControl, Type: types.OptionalType(types.StringType()),
		Deprecated: "model identity is the first llm() argument; a config \"model\" key is ignored by targets"},

	// ── 废弃的提示词表面（统一改用 messages）──────────────────────────
	// systemPrompt / userPrompt 在类型表里，但 runtime 只从 messages 重建
	// chat turns；coze-biz 也只拆 messages（userPrompt 平台不消费）。保留
	// 类型检查 + 废弃 warning，迁移窗口内由绑定层把它们当 messages 的别名
	// 处理（见 docs/LLM-PARAM-SEMANTICS.md）。
	{Name: "systemPrompt", Tier: TierStructural, Type: types.OptionalType(types.StringType()),
		Deprecated: "use messages with role \"system\""},
	{Name: "userPrompt", Tier: TierStructural, Type: types.OptionalType(types.StringType()),
		Deprecated: "use messages with role \"user\""},

	// ── E 扩展（规范名）───────────────────────────────────────────────
	// coze 概念：生成多样性 / 平台显示名。迁移后新产物写 coze:*，旧名保留别名。
	{Name: "coze:generation_diversity", Tier: TierExtension, Type: types.OptionalType(types.StringType())},
	{Name: "coze:model_name", Tier: TierExtension, Type: types.OptionalType(types.StringType())},

	// ── E 扩展（旧名别名，迁移窗口）──────────────────────────────────
	{Name: "generation_diversity", Tier: TierExtension, Type: types.OptionalType(types.StringType()),
		Canonical:  "coze:generation_diversity",
		Deprecated: "use coze:generation_diversity (the old name is a compatibility alias)"},
	{Name: "model_name", Tier: TierExtension, Type: types.OptionalType(types.StringType()),
		Canonical:  "coze:model_name",
		Deprecated: "use coze:model_name (the old name is a compatibility alias)"},
}

// namespacePrefixes = 扩展键前缀。顺序无关；只要命中就按 E 档处理。
var namespacePrefixes = []string{"coze:", "openai:", "x-"}

// Vocabulary 返回词表副本（调用方可以排序/展示，但不能改内部状态）。
func Vocabulary() []KeySpec {
	out := make([]KeySpec, len(vocabulary))
	copy(out, vocabulary)
	return out
}

// Lookup 查一个作者写的键。
//
// 精确命中词表 ⇒ 返回该 spec（别名带 Canonical / Deprecated）。
// 否则命中命名空间前缀 ⇒ 返回一个 E 档、开放类型的合成 spec。
// 其余 ⇒ ok=false（U 档：开放字典，类型检查跳过）。
func Lookup(name string) (KeySpec, bool) {
	for _, spec := range vocabulary {
		if spec.Name == name {
			return spec, true
		}
	}
	if IsNamespaced(name) {
		return KeySpec{Name: name, Tier: TierExtension}, true
	}
	return KeySpec{}, false
}

// IsNamespaced 报一个键是否使用扩展命名空间。
func IsNamespaced(name string) bool {
	for _, prefix := range namespacePrefixes {
		if len(name) > len(prefix) && name[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// CanonicalName 返回规范名：别名 → Canonical；其他 → 原名。
func CanonicalName(name string) string {
	if spec, ok := Lookup(name); ok && spec.Canonical != "" {
		return spec.Canonical
	}
	return name
}

// KnownKeyTypes 派生类型检查表（供 pkg/passes/type_infer.go 使用）。
//
// 只包含有 Type 的精确词表项（含别名与规范名）；命名空间合成键与开放键
// 不在表里 = 类型检查跳过，保持开放字典。
func KnownKeyTypes() map[string]*types.Descriptor {
	out := make(map[string]*types.Descriptor, len(vocabulary))
	for _, spec := range vocabulary {
		if spec.Type != nil {
			out[spec.Name] = spec.Type
		}
	}
	return out
}
