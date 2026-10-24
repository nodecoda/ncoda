package passes

// Operation-spec table, ported from src/nclang/lang/operation_registry.py
// (Task 15 T6). The reference registry carries the full OperationSpec
// (parameters / outputs / policies / provider / result_kind); the Go port
// keeps the minimal face the type pass consumes: parameters (name/type/
// required), policy support, result_kind and the computed result_type
// (OperationSpec.result_type). The name-only Task 14 Operations set is
// superseded — EffectPass.Has keeps its lookup semantics.
//
// Unreachable-in-Go note (Task 10 deviation, resolved as of Task 25f): the
// reference registers the operation builtins agent/extract_text/extract/
// classify with operation function types BEFORE the std namespace, which
// shifts every later SymbolID by 4; the Go binder re-registered all four
// during Task 25 (25c extract_text, 25d agent, 25e classify, 25f extract),
// so the SymbolID offset deviation is zero and every operation call
// resolves at the scope stage. The type-pass code keeps the faithful
// branches (with real contract builders) for parity.

import (
	"fmt"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/tool"
	"github.com/nodecoda/ncoda/pkg/types"
)

// OperationParameter mirrors operation_registry.py OperationParameter:
// name, type_info (Go: Type), required.
type OperationParameter struct {
	Name     string
	Type     *types.Descriptor
	Required bool
}

// OperationPolicySupport mirrors operation_registry.py
// OperationPolicySupport: which policies an operation accepts.
type OperationPolicySupport struct {
	Retry         bool
	Timeout       bool
	DefaultValue  bool
	FailureBranch bool
}

// OperationSpec mirrors operation_registry.py OperationSpec (the face the
// type pass reads): name, parameters, policies, result_kind and the
// computed result_type (OperationSpec.result_type — a record over the
// operation's outputs; extract/classify publish an empty record).
type OperationSpec struct {
	Name       string
	Parameters []OperationParameter
	Policies   OperationPolicySupport
	ResultKind string // "static" | "llm" | "schema-extraction" | "classification"
	ResultType *types.Descriptor
}

// anyMap is map<string, any> (operation_registry.py _map_of(any)).
func opAnyMap() *types.Descriptor { return types.MapType(types.StringType(), types.AnyType()) }

// llmUsage mirrors _LLM_USAGE (operation_registry.py:100-108).
func llmUsage() *types.Descriptor {
	return types.RecordType("LLMUsage", []types.RecordField{
		{Name: "prompt_tokens", Type: types.OptionalType(types.IntType())},
		{Name: "completion_tokens", Type: types.OptionalType(types.IntType())},
		{Name: "total_tokens", Type: types.OptionalType(types.IntType())},
	})
}

func llmResponseType(payload *types.Descriptor) *types.Descriptor {
	if payload == nil {
		payload = types.StringType()
	}
	return types.RecordType("LLMResponse", []types.RecordField{
		{Name: "text", Type: payload},
		{Name: "reasoning_content", Type: types.OptionalType(types.StringType())},
		{Name: "usage", Type: types.OptionalType(llmUsage())},
		{Name: "finish_reason", Type: types.OptionalType(types.StringType())},
	})
}

func validateLLMPayloadType(d *types.Descriptor) error {
	if d == nil {
		return fmt.Errorf("llm type argument is unresolved")
	}
	switch d.Kind {
	case types.String, types.Bool, types.Int, types.Float:
		return nil
	case types.Array, types.Optional:
		return validateLLMPayloadType(d.Elem)
	case types.Map:
		if d.Key == nil || d.Key.Kind != types.String {
			return fmt.Errorf("llm payload maps must use string keys")
		}
		// 合并后 `object` == map<string,any>（2026-09-15 裁定）：开放对象的值位
		// 不承诺类型 ⇒ 值位 any 必须放行，否则 `llm<object>` 会因合并而回归被拒。
		// 判据由此统一：`llm<object>` 与 `llm<map<string, any>>` 是同一个类型、
		// 同一个判定（合并前分裂为「允许 / 拒绝」）。
		if d.Val != nil && d.Val.Kind == types.Any {
			return nil
		}
		return validateLLMPayloadType(d.Val)
	case types.Enum:
		if d.EnumName == "" || len(d.EnumMembers) == 0 {
			return fmt.Errorf("llm enum payload must declare a named non-empty enum")
		}
		return nil
	case types.Record:
		if len(d.Fields) == 0 {
			return fmt.Errorf("llm record payload must declare fields")
		}
		for _, f := range d.Fields {
			if f.Name == "" {
				return fmt.Errorf("llm record payload field name is empty")
			}
			if err := validateLLMPayloadType(f.Type); err != nil {
				return fmt.Errorf("field %q: %w", f.Name, err)
			}
		}
		return nil
	default:
		return fmt.Errorf("llm payload type %s is not admitted", d.Kind.String())
	}
}

// opSearchResult 是 knowledge 结果元素类型的类型层镜像。K-H2（2026-10-17
// 用户裁定）：字段集 = coze 平台事实 `{documentId: integer, output: string}`；
// 旧 content/score/source 退场。与 binding/symbol.go 的 searchResult 同形
// （两处是 scope 注册 vs 类型推断的两条必经路，必须一致）。
func opSearchResult() *types.Descriptor {
	return types.RecordType("SearchResult", []types.RecordField{
		{Name: "documentId", Type: types.IntType()},
		{Name: "output", Type: types.StringType()},
	})
}

// operationResultType mirrors OperationSpec.result_type: a record named
// "OperationResult[<name>]" over the operation's output ports. The Go
// Descriptor drops record names (Task 7 deviation), so the name is
// documentation only.
func operationResultType(name string, fields []types.RecordField) *types.Descriptor {
	return types.RecordType("OperationResult["+name+"]", fields)
}

// defaultOperationSpecs mirrors _builtin_specs() (operation_registry.py
// :130-241), field-for-field: parameter order, required flags, policy
// support and result_kind literals.
func defaultOperationSpecs() map[string]*OperationSpec {
	retryDefaultAndFailure := OperationPolicySupport{Retry: true, DefaultValue: true, FailureBranch: true}
	httpPolicies := OperationPolicySupport{Retry: true, Timeout: true, DefaultValue: true, FailureBranch: true}
	agentPolicies := OperationPolicySupport{Retry: true, FailureBranch: true}
	extractPolicies := OperationPolicySupport{FailureBranch: true}

	specs := map[string]*OperationSpec{}
	put := func(spec *OperationSpec) { specs[spec.Name] = spec }

	put(&OperationSpec{
		Name: "llm",
		Parameters: []OperationParameter{
			{Name: "model", Type: types.StringType(), Required: true},
			{Name: "config", Type: opAnyMap(), Required: true},
		},
		Policies:   retryDefaultAndFailure,
		ResultKind: "llm",
		ResultType: llmResponseType(types.StringType()),
	})
	put(&OperationSpec{
		Name: "http",
		Parameters: []OperationParameter{
			{Name: "method", Type: types.StringType(), Required: true},
			{Name: "url", Type: types.StringType(), Required: true},
			{Name: "config", Type: opAnyMap(), Required: false},
		},
		Policies:   httpPolicies,
		ResultKind: "static",
		ResultType: operationResultType("http", []types.RecordField{
			{Name: "body", Type: types.StringType()},
			{Name: "status_code", Type: types.IntType()},
			{Name: "headers", Type: opAnyMap()},
			// 与 binding/symbol.go httpResponse 同步（scope 注册 vs 类型推断 operation
			// spec 两处必须一致）。`files` 是参考目标平台专属端口，不入共享原型
			// （2026-09-14 用户拍板 R2）。
		}),
	})
	put(&OperationSpec{
		Name: "agent",
		Parameters: []OperationParameter{
			{Name: "model", Type: types.StringType(), Required: true},
			{Name: "query", Type: types.StringType(), Required: true},
			{Name: "config", Type: opAnyMap(), Required: true},
		},
		Policies:   agentPolicies,
		ResultKind: "static",
		ResultType: operationResultType("agent", []types.RecordField{
			{Name: "text", Type: types.StringType()},
		}),
	})
	put(&OperationSpec{
		Name: "knowledge",
		Parameters: []OperationParameter{
			{Name: "datasets", Type: types.StringType(), Required: true},
			{Name: "query", Type: types.StringType(), Required: false},
			{Name: "config", Type: opAnyMap(), Required: false},
		},
		Policies:   OperationPolicySupport{},
		ResultKind: "static",
		ResultType: operationResultType("knowledge", []types.RecordField{
			{Name: "result", Type: types.ArrayType(opSearchResult())},
		}),
	})
	put(&OperationSpec{
		Name: "extract_text",
		Parameters: []OperationParameter{
			{Name: "file", Type: types.AnyType(), Required: true},
		},
		Policies:   OperationPolicySupport{},
		ResultKind: "static",
		ResultType: operationResultType("extract_text", []types.RecordField{
			{Name: "text", Type: types.StringType()},
		}),
	})
	put(&OperationSpec{
		Name: "extract",
		Parameters: []OperationParameter{
			{Name: "model", Type: types.StringType(), Required: true},
			{Name: "text", Type: types.StringType(), Required: true},
			{Name: "config", Type: opAnyMap(), Required: true},
		},
		Policies:   extractPolicies,
		ResultKind: "schema-extraction",
		ResultType: operationResultType("extract", nil),
	})
	put(&OperationSpec{
		Name: "classify",
		Parameters: []OperationParameter{
			{Name: "model", Type: types.StringType(), Required: true},
			{Name: "query", Type: types.StringType(), Required: true},
			{Name: "config", Type: opAnyMap(), Required: true},
		},
		Policies:   OperationPolicySupport{},
		ResultKind: "classification",
		ResultType: operationResultType("classify", nil),
	})
	return specs
}

// operationSpec mirrors type_pass.py _operation_spec (:1800-1830): unwrap
// an OperationPolicyExpr, require a direct builtin call, and return the
// registered operation spec (nil when the callee is not a registered
// operation builtin).
//
// TOOL-DIRECT-CALL-FORM (Task 4): a qualified capability call p.op({...})
// in a policy/attempt context is the direct-call analog of the legacy
// tool() spec — retry/failure-branch support comes from the capability
// registry definition (SupportsRetry / SupportsFailureBranch) instead of
// the static tool builtin's unconditional {Retry, FailureBranch}. The
// registry is required (closed world); without it the call is a type error
// anyway, so no spec is returned.
func operationSpec(node ast.Expr, ctx *Context) *OperationSpec {
	expr := node
	if p, ok := node.(*ast.OperationPolicyExpr); ok {
		expr = p.Operation()
	}
	// `foreign code`（CodeRunner）**不是 CallExpr**，但平台侧它与 qualified
	// 能力调用同族：节点自带 settingOnError{processType,retryTimes,timeoutMs,
	// dataOnErr}（coze-studio OSS vo/canvas.go:590-620 + compose/node_runner.go
	// onError 分支），是可失败的副作用节点 ⇒ 策略合法性由**结构**决定
	// （2026-09-10 决策 A / S2），不由语料抽样决定：开 Retry / Timeout /
	// DefaultValue。FailureBranch（PT3 双端口菱形）不在此列：raising 侧的
	// attempt-on-code 尚未实现（`attempt foreign code` 仍硬红），半开状态
	// 比不开更糟（见 2026-09-11 S2-gate §3）。
	if _, ok := expr.(*ast.ForeignCodeExpr); ok {
		return &OperationSpec{
			Name:       "foreign code",
			Policies:   OperationPolicySupport{Retry: true, Timeout: true, DefaultValue: true},
			ResultKind: "static",
		}
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil
	}
	if fa, ok := call.Func().(*ast.FieldAccessExpr); ok {
		if obj, ok := fa.Obj().(*ast.IdentExpr); ok {
			if id, ok := ctx.Bindings.NodeIDOf(obj); ok {
				if symID, ok := ctx.Bindings.ReferenceSymbols().Get(id); ok {
					if decl, ok := ctx.Bindings.Declarations().Get(symID); ok && decl.Kind == binding.SymbolNamespace {
						// I2: only import namespaces (capability providers)
						// can carry an operation spec. Builtin namespaces
						// (json.stringify, std.v1.x) are not capabilities;
						// the registry lookup would miss anyway, but the
						// explicit NS discriminant makes that exclusion
						// structural instead of accidental.
						if decl.NS != binding.NamespaceImport {
							return nil
						}
						return qualifiedOperationSpec(obj.Name(), fa.FieldName(), ctx)
					}
				}
			}
		}
		return nil
	}
	ident, ok := call.Func().(*ast.IdentExpr)
	if !ok {
		return nil
	}
	id, ok := ctx.Bindings.NodeIDOf(ident)
	if !ok {
		return nil
	}
	symID, ok := ctx.Bindings.ReferenceSymbols().Get(id)
	if !ok {
		return nil
	}
	decl, ok := ctx.Bindings.Declarations().Get(symID)
	if !ok || decl.Kind != binding.SymbolBuiltinFunction {
		return nil
	}
	return ctx.Profile.Operations.Lookup(ident.Name())
}

// qualifiedOperationSpec builds the policy/attempt operation spec for a
// qualified capability call p.op({...}) from the registry definition
// (TOOL-DIRECT-CALL-FORM Task 4). It returns nil when no registry is
// configured or the provider/operation is undeclared — the call then fails
// on its own type-stage diagnostics and is not a policy operation.
func qualifiedOperationSpec(provider, operation string, ctx *Context) *OperationSpec {
	if ctx.ToolRegistry == nil {
		return nil
	}
	definition, found := ctx.ToolRegistry.Lookup(provider, operation)
	if !found {
		return nil
	}
	spec := &OperationSpec{
		Name: provider + "." + operation,
		Policies: OperationPolicySupport{
			Retry:         definition.SupportsRetry,
			FailureBranch: definition.SupportsFailureBranch,
			// `with default(<record>)` 的合法性按**结构**判定：qualified 调用
			// 就是一次插件调用 = 平台上带 settingOnError 的可失败副作用节点，
			// 失败缺省（processType=2 + dataOnErr）是节点级的能力，不随语料
			// 抽样摇摆（A 级裁定「让合法性由结构决定，而不是由语料抽样决定」，
			// 见 docs/ideas-todo/2026-09-10-attempt-capability-modeling-
			// callsite-vs-optype.md；同一把尺子也用于 N2 的 raising 侧）。
			// Timeout 同一把尺子（2026-09-29，R-151）：内建 code / http 已开
			// Timeout 位而 qualified op 未开 = 最少例外违反；平台一侧
			// plugin 节点确实携带 settingOnError.timeoutMs（语料 4665 + 4146
			// 个节点，按插件 API 定值）⇒ 不开位就在 raising 侧静默丢数据。
			// retry / failure_branch 的轴错（call-site vs op-type）仍按原样
			// 保留，属该文档的独立待办。
			DefaultValue: true,
			Timeout:      true,
		},
		ResultKind: "static",
	}
	// 结果形状只由**声明的输出表**决定（R-29，2026-09-24 收紧）：
	//  - 有声明 ⇒ 契约记录；
	//  - `-> void`（显式空表）⇒ void（没有结果值，`default(...)` 无载荷可匹配）；
	//  - **未声明输出表** ⇒ 不在这里发明形状（调用点 visitQualifiedCapabilityCall
	//    已就地 emit CAPABILITY_OUTPUTS_UNDECLARED）⇒ 同 void，且不造假字段。
	if outputs := registryToolOutputs(definition); len(outputs) > 0 {
		spec.ResultType = recordFromRegistryOutputs(outputs)
	} else {
		spec.ResultType = types.VoidType()
	}
	return spec
}

// operationErrorType mirrors type_pass.py _operation_error_type (:1809-1815):
// the error binding type of an attempt statement.
func operationErrorType() *types.Descriptor {
	return types.RecordType("OperationError", []types.RecordField{
		{Name: "error_message", Type: types.StringType()},
		{Name: "error_type", Type: types.StringType()},
	})
}

// registryTypeDescriptor maps a semantic registry type name onto a
// type descriptor. Unsupported names fall back to Any so an evolving
// registry never hard-fails the type stage.
func registryTypeDescriptor(name string) *types.Descriptor {
	// array<record:X>: element record fields arrive as sibling dotted
	// paths; recordFromRegistryOutputs substitutes the record shape when
	// children exist. The bare fallback keeps the type stage open.
	if tool.CanHaveElementChildren(name) {
		return types.ArrayType(opAnyMap())
	}
	switch name {
	case "string":
		return types.StringType()
	case "int":
		return types.IntType()
	case "float":
		return types.FloatType()
	case "bool":
		return types.BoolType()
	case "file":
		// 媒体输出（image/video/audio → file）: 字段可做 file→string
		// 透传、非空条件与插件参数传递（实证: S11 即梦 url）。
		return types.FileType()
	case "any":
		// 开放 object（对齐反编译端 registryLeafTypeInfo any → DataTypeObject）：
		// 头文件 `any <field>` 在两端同构为开放 object，避免分支赋值报
		// object vs map<string,any> 不匹配（X14 实证）。dict 保持显式
		// map<string,any>（参数侧语义，registryParamTypeDescriptor 特判）。
		return types.ObjectType()
	case "dict":
		return opAnyMap()
	case "map<string,any>", "map<string, any>", "map<string,string>", "map<string, string>":
		return types.MapType(types.StringType(), types.AnyType())
	case "array<string>":
		return types.ArrayType(types.StringType())
	case "array<int>":
		return types.ArrayType(types.IntType())
	case "array<float>":
		return types.ArrayType(types.FloatType())
	case "array<bool>":
		return types.ArrayType(types.BoolType())
	case "array<dict>", "array<any>":
		// 输出侧值类型: 元素按 map<string,any> 解析（元素可字段访问 —
		// X2_W05 aweme.awemeLink）。参数侧期望经 registryParamTypeDescriptor
		// 特判为 array<Any>（宽松接受任意数组 — Z34 image_urls）。
		return types.ArrayType(opAnyMap())
	}
	if kinds := tool.FileKindTokens(name); kinds != nil {
		// 注册表串的约束形态 `file<image>`（能力头文件的 file<…> 输出字段，见
		// pkg/tool.fileRegistryTypeString）：族是**契约**不是身份 ⇒ 描述符
		// 带约束，兼容判据仍族盲（B7，file<x>/file<y> 互可赋值）。
		return types.FileTypeWithConstraints(types.FileConstraints{Types: kinds})
	}
	return types.AnyType()
}

// recordFromRegistryOutputs builds a nested record descriptor from declared
// tool outputs. Dotted output names (e.g. "data.table_id") declare nested
// record paths, mirroring backend variable paths. An array/map-of-record
// leaf whose descendant paths are element member fields becomes an array
// (map) of the record built over those children (E5-b).
func recordFromRegistryOutputs(fields []tool.OutputField) *types.Descriptor {
	type node struct {
		leafType string
		leaf     *types.Descriptor
		children map[string]*node
	}
	root := &node{children: map[string]*node{}}
	for _, field := range fields {
		parts := splitDotted(field.Name)
		cur := root
		for _, part := range parts {
			next, ok := cur.children[part]
			if !ok {
				next = &node{children: map[string]*node{}}
				cur.children[part] = next
			}
			cur = next
		}
		cur.leafType = field.Type
		cur.leaf = registryTypeDescriptor(field.Type)
	}
	var build func(n *node) *types.Descriptor
	build = func(n *node) *types.Descriptor {
		// A pure leaf node yields its declared type; interior nodes are records
		// over their children (the registry guarantees leaf/parent disjointness
		// except for array/map-of-record leaves, which become arrays over the
		// element record built from their children).
		if n.leaf != nil && len(n.children) == 0 {
			return n.leaf
		}
		fields := make([]types.RecordField, 0, len(n.children))
		for name, child := range n.children {
			fields = append(fields, types.RecordField{Name: name, Type: build(child)})
		}
		record := types.RecordType("ToolResult", fields)
		if n.leaf != nil && tool.CanHaveElementChildren(n.leafType) {
			if strings.HasPrefix(n.leafType, "map<string,record:") {
				return types.MapType(types.StringType(), record)
			}
			return types.ArrayType(record)
		}
		return record
	}
	return build(root)
}

func splitDotted(name string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			parts = append(parts, name[start:i])
			start = i + 1
		}
	}
	return append(parts, name[start:])
}
