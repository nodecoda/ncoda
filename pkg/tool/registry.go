// Package tool defines platform-neutral external Tool capability contracts.
package tool

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/token"
)

// SymbolKey returns the canonical identity of one logical Tool operation.
func SymbolKey(provider, operation string) string {
	return provider + "/" + operation
}

// Parameter describes one named operation parameter.
// Type is the semantic type name (e.g. string, int, array<string>, dict);
// the type pass resolves it when typing tool() results from a registry.
//
// Desc/Default/Enum/Min/Max 无损耗承载 runtime plugin 的 ParamSchema
// （Tier-2 review #11）:插件声明 → semanticRegistry 投影不再丢字段,
// computeInterfaceHash 也覆盖它们,声明变化会驱动 ABI 指纹漂移。
type Parameter struct {
	Name     string   `yaml:"name"`
	Type     string   `yaml:"type"`
	Desc     string   `yaml:"desc,omitempty"`
	Required bool     `yaml:"required"`
	Default  any      `yaml:"default,omitempty"`
	Enum     []any    `yaml:"enum,omitempty"`
	Min      *float64 `yaml:"min,omitempty"`
	Max      *float64 `yaml:"max,omitempty"`
}

// OutputField declares one tool result output. A dotted Name (e.g.
// "data.table_id") declares a nested record path, mirroring the backend
// variable path used by target renderers.
type OutputField struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
}

// Definition declares one platform-neutral Tool operation.
type Definition struct {
	Provider    string
	Operation   string
	Description string // human description (e.g. the source plugin display name)
	Parameters  []Parameter
	Outputs     []OutputField
	// OutputsUndeclared 标记**声明源根本没有输出表这一栏**（R-29，2026-09-24
	// 收紧）：能力头文件 `function f(...) {}` 没写 `-> T` 时置位。输出表是
	// 头文件的义务（类比 C 函数声明缺返回类型 = 声明方的错误）⇒ 调用这种 op
	// 是编译错误（`diag.CAPABILITY_OUTPUTS_UNDECLARED`），**不**再静默回落
	// `{result: any}` 这种凭空捏造的字段。
	//
	// 零值 = 已声明（输出表由构造方显式给出，空表也算声明）—— 极性刻意如此：
	// 只有「能判断『没有这一栏』的那一个读点」（能力头文件加载器）才置位，
	// 其余构造方（内建节点绑定、运行期插件桥、反编译注册表）默认合法。
	//
	// **不进 ABI 指纹**（computeInterfaceHash）：未声明是**非法状态**（调用即
	// 拒收），不是接口的一个取值 ⇒ 加它只会在合法头文件之间造出差分。声明
	// `-> void`（显式空输出表）与旧的无输出表在指纹上早已同值（都不写 outputs），
	// 故本字段引入不驱动 `cmd/ncoda-pin-bindings` 重钉。
	OutputsUndeclared     bool
	SupportsRetry         bool
	SupportsFailureBranch bool
	// TimeoutMS is the platform-default timeout ceiling declared by the
	// capability (0 = unconstrained). A call asking for an explicit timeout
	// above the ceiling is rejected (P3.3 §16.3).
	TimeoutMS int
	// Idempotent marks an operation that can be safely replayed (P3.3
	// §16.3). Diagnostic only — no gate.
	Idempotent bool
	// InterfaceHash is the stable ABI fingerprint computed by NewRegistry
	// (P3.3 §16.2). It covers the canonical identity, the ordered parameter
	// table (name/type/required), the ordered output path table and the
	// effect bits. Bindings may pin it explicitly to reject stale links.
	InterfaceHash string
}

// Registry is an immutable-by-convention set of Tool capability definitions.
// Strict marks a registry built from the program's import declarations
// (P3.5 closed world): a tool() call whose provider is not in the registry
// is a compile error instead of falling back to the open-world {result:any}
// default. LoadCapabilityPackages returns a strict registry; LoadCapabilityDir
// and NewRegistry return an open one.
type Registry struct {
	definitions map[string]Definition
	strict      bool
}

// Strict reports whether the registry was loaded from source imports
// (closed world: undeclared providers are errors).
func (r *Registry) Strict() bool { return r.strict }

// NewRegistry validates and indexes definitions by canonical symbol identity.
func NewRegistry(definitions ...Definition) (*Registry, error) {
	r := &Registry{definitions: make(map[string]Definition, len(definitions))}
	for _, definition := range definitions {
		key := SymbolKey(definition.Provider, definition.Operation)
		if definition.Provider == "" || definition.Operation == "" {
			return nil, fmt.Errorf("tool registry: provider and operation must be non-empty: %q", key)
		}
		if _, exists := r.definitions[key]; exists {
			return nil, fmt.Errorf("tool registry: duplicate definition %q", key)
		}
		parameters := make([]Parameter, len(definition.Parameters))
		copy(parameters, definition.Parameters)
		seen := make(map[string]bool, len(parameters))
		for _, parameter := range parameters {
			if parameter.Name == "" {
				return nil, fmt.Errorf("tool registry: %q has an empty parameter name", key)
			}
			if seen[parameter.Name] {
				return nil, fmt.Errorf("tool registry: %q has duplicate parameter %q", key, parameter.Name)
			}
			seen[parameter.Name] = true
		}
		definition.Parameters = parameters
		definition.InterfaceHash = computeInterfaceHash(definition)
		outputs := make([]OutputField, len(definition.Outputs))
		seenPath := make(map[string]string, len(definition.Outputs))
		for i, field := range definition.Outputs {
			if field.Name == "" || field.Type == "" {
				return nil, fmt.Errorf("tool registry: %q has an empty output name/type", key)
			}
			if _, dup := seenPath[field.Name]; dup {
				return nil, fmt.Errorf("tool registry: %q has duplicate output %q", key, field.Name)
			}
			// A dotted path may not collide with an ancestor or a descendant:
			// each tree node is either a scalar leaf, a record, or an
			// array-of-record whose descendants are element member paths
			// (array<record:X> / map<string,record:X>).
			parts := strings.Split(field.Name, ".")
			for j := 1; j < len(parts); j++ {
				prefix := strings.Join(parts[:j], ".")
				if parentType, exists := seenPath[prefix]; exists && !CanHaveElementChildren(parentType) {
					return nil, fmt.Errorf("tool registry: %q output %q conflicts with parent %q", key, field.Name, prefix)
				}
			}
			for seen := range seenPath {
				if strings.HasPrefix(seen, field.Name+".") && !CanHaveElementChildren(field.Type) {
					return nil, fmt.Errorf("tool registry: %q output %q conflicts with descendant %q", key, field.Name, seen)
				}
			}
			seenPath[field.Name] = field.Type
			outputs[i] = field
		}
		definition.Outputs = outputs
		r.definitions[key] = definition
	}
	return r, nil
}

// FileKindTokens returns the constraint tokens of a registry file type:
// "file<image>" → ["image"], bare "file" → nil. The registry leaf-type
// vocabulary is defined here (next to CanHaveElementChildren) so every
// consumer parses the same spelling.
//
// 回读的是**名字**：注册表串里可能带 `$` 转义（`file<$code>` —— code 是关键字，
// 见 `token.EscapedSpelling`），这里统一剥掉，调用方只看名字。
//
// 为什么需要它（2026-09-21）：能力头文件的 `file<image> data;`（语料
// outputs[].assistType 实证）在此前的 typeRefToTypeString 里被压成裸 "file"
// ⇒ 族在**注册表**这一层就丢了，头文件写得再准也不进产物（S33 一手实证：
// 头文件改成 file<image> 后产物逐字节不变）。注册表串是 carrier，不是标签。
func FileKindTokens(registryType string) []string {
	if !strings.HasPrefix(registryType, "file<") || !strings.HasSuffix(registryType, ">") {
		return nil
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(registryType, "file<"), ">")
	var out []string
	for _, part := range strings.FieldsFunc(inner, func(r rune) bool { return r == ';' || r == ',' }) {
		if tok := token.NameFromSpelling(strings.TrimSpace(part)); tok != "" {
			out = append(out, tok)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// IsFileType reports whether a registry leaf type is a file type — bare
// "file" or a constrained "file<…>". Consumers that predate the constraint
// axis (dify / coze-studio 渲染器) use it to keep their existing "file"
// behaviour instead of falling through to the string default.
func IsFileType(registryType string) bool {
	return registryType == "file" || FileKindTokens(registryType) != nil
}

// CanHaveElementChildren reports whether a registry leaf type may carry
// descendant dotted paths — i.e. the element member fields of an array/map
// of a named record ("array<record:X>" / "map<string,record:X>"). Scalar and
// plain (array<string>, array<any>) leaves terminate the tree.
func CanHaveElementChildren(t string) bool {
	return strings.HasPrefix(t, "array<record:") || strings.HasPrefix(t, "map<string,record:")
}

// Definitions returns the validated definitions in canonical order
// (sorted by (provider, operation)).
func (r *Registry) Definitions() []Definition {
	if r == nil {
		return nil
	}
	keys := make([]string, 0, len(r.definitions))
	for key := range r.definitions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	definitions := make([]Definition, 0, len(keys))
	for _, key := range keys {
		definitions = append(definitions, r.definitions[key])
	}
	return definitions
}

// Lookup returns the definition for one logical Tool operation.
func (r *Registry) Lookup(provider, operation string) (Definition, bool) {
	if r == nil {
		return Definition{}, false
	}
	definition, ok := r.definitions[SymbolKey(provider, operation)]
	return definition, ok
}

// HasProvider reports whether any operation of the provider is registered.
func (r *Registry) HasProvider(provider string) bool {
	if r == nil {
		return false
	}
	prefix := provider + "/"
	for key := range r.definitions {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// ValidateGraph checks all Tool calls in the backend projection, including
// calls nested inside projected loop containers. A nil registry preserves the
// compiler's open-world behavior.
func (r *Registry) ValidateGraph(graph *ir.Graph) error {
	if r == nil {
		return nil
	}
	projection, err := flow.Project(graph)
	if err != nil {
		return err
	}
	return r.validateNodes(projection.Nodes)
}

func (r *Registry) validateNodes(nodes []ir.Node) error {
	for _, node := range nodes {
		if loop, ok := node.(*flow.ProjectedLoop); ok {
			if err := r.validateNodes(loop.Children); err != nil {
				return err
			}
			continue
		}
		call, ok := node.(*ir.ToolCall)
		if !ok {
			continue
		}
		if err := r.validateCall(call); err != nil {
			return err
		}
	}
	return nil
}

func (r *Registry) validateCall(call *ir.ToolCall) error {
	key := SymbolKey(call.ProviderID, call.Operation)
	definition, ok := r.definitions[key]
	if !ok {
		return fmt.Errorf("tool registry: node %s uses unknown capability %q", call.ID, key)
	}

	declared := make(map[string]Parameter, len(definition.Parameters))
	for _, parameter := range definition.Parameters {
		declared[parameter.Name] = parameter
	}
	provided := make(map[string]bool, len(call.Params))
	for _, parameter := range call.Params {
		if _, ok := declared[parameter.Key]; !ok {
			return fmt.Errorf("tool registry: %q does not declare argument %q", key, parameter.Key)
		}
		provided[parameter.Key] = true
	}
	for _, parameter := range definition.Parameters {
		if parameter.Required && !provided[parameter.Name] {
			return fmt.Errorf("tool registry: %q requires argument %q", key, parameter.Name)
		}
	}

	if call.Exception != nil {
		if (call.Exception.MaxRetry > 0 || call.Exception.RetryIntervalMS > 0) && !definition.SupportsRetry {
			return fmt.Errorf("tool registry: %q does not support retry", key)
		}
		if call.Exception.FailBranch && !definition.SupportsFailureBranch {
			return fmt.Errorf("tool registry: %q does not support a failure branch", key)
		}
		// 调用点 timeout **不设上限**（2026-09-29，R-151）。
		//
		// 旧实现把 `@capability timeout_ms=` 当成上限，超了就是硬错误。语料
		// 一手证伪了这个前提：coze-biz 322 份 yaml 里 **15 个 op** 的节点把
		// timeoutMs 写成 600000 而头文件标称 180000（feishu_bitable/add_records
		// 9 例、capcut_data/audio_timelines 5 例、file_read/read、url_get/get_url…），
		// 平台接受并运行 —— 它们就是平台的真实产物。把标称当上限的代价是
		// **raising 产出的 ncoda 过不了 compile**（实测 34/322 源编译失败，
		// 10.5% 的语料在结构上无法往返）。
		//
		// `timeout_ms` 的真实语义 = **该 op 的平台标称/默认超时（数据）**：
		// 它是 raising「等于默认就不写」的判据来源，不是拒绝理由。同一形状的
		// 裁断在本仓已有先例（文件族契约：「软降级必须报账，**不是** error ——
		// 升级成 error 会拒收合法源码」，AGENTS.md 平台形状契约节）。
		_ = definition.TimeoutMS
	}
	return nil
}

// computeInterfaceHash returns the stable ABI fingerprint of one Tool
// definition (P3.3 §16.2). SHA-256 over the normalized interface:
//
//	canonical identity (provider "/" operation)
//	+ ordered parameter table: name, semantic type, required bit,
//	  default, enum members, min/max bounds (Tier-2 review #11:约束
//	  字段进入 ABI,声明约束变化不再静默)
//	+ ordered output path table: dotted path, leaf type
//	+ effect bits: SupportsRetry, SupportsFailureBranch
//
// Every component is written with a length prefix so adjacent fields cannot
// ambiguously concatenate ("ab"+"c" vs "a"+"bc"). Parameter order and output
// order are ABI-relevant (platform ports are positional/ordered), so the
// hash is order-sensitive. Extending the effect set later (timeout_ms,
// idempotent — P3.3 §16.3) adds new bits here, which changes the hash by
// design; pinned bindings then fail loudly instead of silently mislinking.
// Desc 是文档字段,不参与 ABI（改描述不改签名）。
func computeInterfaceHash(d Definition) string {
	h := sha256.New()
	writeField := func(v string) {
		_, _ = h.Write([]byte(fmt.Sprintf("%d:%s", len(v), v)))
	}

	writeField(SymbolKey(d.Provider, d.Operation))
	for _, p := range d.Parameters {
		writeField(p.Name)
		writeField(p.Type)
		if p.Required {
			writeField("required")
		} else {
			writeField("optional")
		}
		// Tier-2 review #11:约束字段进入 ABI。Default/Enum 用 %T:%v 序列化
		// ——类型敏感且确定:纯 %v 会把 int(1) 与 float64(1) 都渲染成 "1",
		// Go 的 json.Marshal 同样输出 "1"（最短表示,无类型区分）,而
		// JSON Schema 语义里 1 与 1.0 是不同默认值,ABI 指纹必须能区分。
		if p.Default != nil {
			writeField(fmt.Sprintf("default=%T:%v", p.Default, p.Default))
		}
		for _, e := range p.Enum {
			writeField(fmt.Sprintf("enum=%T:%v", e, e))
		}
		if p.Min != nil {
			writeField("min=" + fmt.Sprintf("%v", *p.Min))
		}
		if p.Max != nil {
			writeField("max=" + fmt.Sprintf("%v", *p.Max))
		}
	}
	for _, o := range d.Outputs {
		writeField(o.Name)
		writeField(o.Type)
	}
	writeField(effectBits(d))

	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// effectBits renders the effect flags as a stable ordered string so the hash
// distinguishes capability changes without depending on bool formatting.
func effectBits(d Definition) string {
	bits := []string{}
	if d.SupportsRetry {
		bits = append(bits, "retry")
	}
	if d.SupportsFailureBranch {
		bits = append(bits, "failure_branch")
	}
	if d.TimeoutMS > 0 {
		bits = append(bits, fmt.Sprintf("timeout_ms=%d", d.TimeoutMS))
	}
	if d.Idempotent {
		bits = append(bits, "idempotent")
	}
	return strings.Join(bits, ",")
}

// BindingLink is the platform-binding projection the registry validates
// against (P3.3 §16.2): the semantic identity plus the semantic-name key
// sets a binding maps, and an optional explicit ABI pin.
type BindingLink struct {
	Provider      string
	Operation     string
	Arguments     map[string]string // semantic parameter name -> platform name
	Outputs       map[string]string // semantic output path -> platform name
	InterfaceHash string            // explicit ABI pin ("sha256:..."); empty = unpinned
}

// ValidateBindings performs the two-layer binding↔registry check (P3.3
// §16.2): collection consistency is always enforced (every semantic
// argument/output name must exist in the registry definition — the layer
// that catches typos, renames and deleted fields with a readable diff), and
// an explicit InterfaceHash pin is enforced when present (the ABI sentinel
// that catches stale bindings after a non-breaking registry upgrade). The
// returned slice names the unpinned bindings (informational). A nil
// registry preserves the open-world behavior (no check, no report).
func (r *Registry) ValidateBindings(links []BindingLink) (unpinned []string, err error) {
	if r == nil {
		return nil, nil
	}
	unpinned = make([]string, 0)
	for _, link := range links {
		key := SymbolKey(link.Provider, link.Operation)
		definition, ok := r.definitions[key]
		if !ok {
			// P3.5: the registry may be a subset of the binding manifest
			// (imports load only the declared packages). A binding for a
			// capability the registry does not contain is simply not
			// exercised by this program; skip it instead of failing the
			// whole compile. Unknown-key checks for the capabilities that
			// ARE in scope still run below.
			continue
		}
		declaredParams := make(map[string]bool, len(definition.Parameters))
		for _, p := range definition.Parameters {
			declaredParams[p.Name] = true
		}
		for arg := range link.Arguments {
			if !declaredParams[arg] {
				return nil, fmt.Errorf("tool registry: binding %q declares argument %q not in registry", key, arg)
			}
		}
		declaredOutputs := make(map[string]bool, len(definition.Outputs))
		for _, o := range definition.Outputs {
			declaredOutputs[o.Name] = true
		}
		for out := range link.Outputs {
			if !declaredOutputs[out] {
				return nil, fmt.Errorf("tool registry: binding %q declares output %q not in registry", key, out)
			}
		}
		if link.InterfaceHash != "" && link.InterfaceHash != definition.InterfaceHash {
			return nil, fmt.Errorf("tool registry: binding %q interface mismatch: pinned %s, registry %s",
				key, link.InterfaceHash, definition.InterfaceHash)
		}
		if link.InterfaceHash == "" {
			unpinned = append(unpinned, key)
		}
	}
	return unpinned, nil
}

// ValidateCredentialRef rejects raw credential values in binding files:
// only logical references (env://VAR, secret://NAME) are accepted (P3.3
// §16.3). Empty is allowed (no credential). The real secret is supplied by
// the deployment environment / platform secret store, never by the binding.
func ValidateCredentialRef(ref string) error {
	if ref == "" {
		return nil
	}
	if strings.HasPrefix(ref, "env://") || strings.HasPrefix(ref, "secret://") {
		return nil
	}
	return fmt.Errorf("credential_ref must be a reference (env://VAR or secret://NAME), got %q", ref)
}
