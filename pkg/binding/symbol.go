package binding

// Symbol table and builtin registry, ported from
// src/nclang/lang/symbol_table.py. Symbol mirrors the Python dataclass
// (name/kind/type_info/is_mutable/is_builtin; definition_site is deferred —
// the Go AST nodes carry their own spans, see the binder report).
// RegisterBuiltins mirrors DSLBuiltinRegistry.register_all, including the
// merged calculation-builtin signatures (calculation_builtins.py).

import (
	"fmt"

	"github.com/nodecoda/ncoda/pkg/types"
)

// Symbol is one declared symbol, mirroring symbol_table.Symbol. Values are
// treated as immutable after construction (DESIGN D6 convention).
type Symbol struct {
	Name      string
	Kind      SymbolKind
	TypeInfo  *types.Descriptor
	IsMutable bool
	IsBuiltin bool
	// NS is the namespace discriminant (I2): meaningful only when Kind ==
	// SymbolNamespace. Explicit kind instead of the Type.Kind == Any
	// heuristic, so binder/type-pass/IR share one discriminator.
	NS NamespaceKind
}

// NewSymbol constructs a Symbol (Python Symbol(...) dataclass constructor).
func NewSymbol(name string, kind SymbolKind, typeInfo *types.Descriptor, isMutable, isBuiltin bool) *Symbol {
	return &Symbol{Name: name, Kind: kind, TypeInfo: typeInfo, IsMutable: isMutable, IsBuiltin: isBuiltin}
}

// NewNamespaceSymbol constructs a SymbolNamespace symbol with an explicit
// builtin/import discriminant (I2). Builtin namespaces carry their record
// type (StdlibNamespaceType / JSONNamespaceType); import namespaces carry
// Any — the type pass and IR read NS, not the descriptor kind.
func NewNamespaceSymbol(name string, ns NamespaceKind, typeInfo *types.Descriptor) *Symbol {
	return &Symbol{Name: name, Kind: SymbolNamespace, TypeInfo: typeInfo, IsMutable: false, IsBuiltin: true, NS: ns}
}

// String renders the Python-style repr: "Symbol(x, VARIABLE, <type>)".
func (s *Symbol) String() string {
	typ := "any"
	if s.TypeInfo != nil {
		typ = s.TypeInfo.Kind.String()
	}
	return fmt.Sprintf("Symbol(%s, %s, %s)", s.Name, s.Kind, typ)
}

// builtinSpec is one entry of the merged builtin function table
// (calculation_builtins.py + symbol_table.py DSLBuiltinRegistry.BUILTINS).
// The params slice is stored by value in the resulting function descriptor,
// mirroring Python's FunctionType(params, return_type) conversion.
type builtinSpec struct {
	name   string
	params []types.Descriptor
	ret    *types.Descriptor
}

// anyMap is the shared map<string, any> descriptor (types.py any_map).
func anyMap() *types.Descriptor { return types.MapType(types.StringType(), types.AnyType()) }

// calculationBuiltins ports calculation_builtins.py _BUILTINS (merged first
// in register_all, so these names are registered before the main table).
func calculationBuiltins() []builtinSpec {
	anyArray := types.ArrayType(types.AnyType())
	anyFunc := types.FunctionType([]types.Descriptor{*types.AnyType()}, types.AnyType())
	predFunc := types.FunctionType([]types.Descriptor{*types.AnyType()}, types.BoolType())
	arrayFunc := types.FunctionType([]types.Descriptor{*types.AnyType()}, anyArray)
	return []builtinSpec{
		{name: "map", params: []types.Descriptor{*anyArray, *anyFunc}, ret: anyArray},
		{name: "filter", params: []types.Descriptor{*anyArray, *predFunc}, ret: anyArray},
		{name: "sort", params: []types.Descriptor{*anyArray, *types.StringType()}, ret: anyArray},
		{name: "sort_by", params: []types.Descriptor{*anyArray, *types.StringType(), *types.StringType()}, ret: anyArray},
		{name: "take", params: []types.Descriptor{*anyArray, *types.IntType()}, ret: anyArray},
		{name: "flatMap", params: []types.Descriptor{*anyArray, *arrayFunc}, ret: anyArray},
	}
}

// mainBuiltins ports symbol_table.py DSLBuiltinRegistry.BUILTINS.
func mainBuiltins() []builtinSpec {
	llmUsage := types.RecordType("LLMUsage", []types.RecordField{
		{Name: "prompt_tokens", Type: types.OptionalType(types.IntType())},
		{Name: "completion_tokens", Type: types.OptionalType(types.IntType())},
		{Name: "total_tokens", Type: types.OptionalType(types.IntType())},
	})
	llmResponse := types.RecordType("LLMResponse", []types.RecordField{
		{Name: "text", Type: types.StringType()},
		{Name: "reasoning_content", Type: types.OptionalType(types.StringType())},
		{Name: "usage", Type: types.OptionalType(llmUsage)},
		{Name: "finish_reason", Type: types.OptionalType(types.StringType())},
	})
	// K-H2（2026-10-17 用户裁定）：元素字段集 = coze 平台事实
	// `{documentId: integer, output: string}`；旧 content/score/source 退场。
	searchResult := types.RecordType("SearchResult", []types.RecordField{
		{Name: "documentId", Type: types.IntType()},
		{Name: "output", Type: types.StringType()},
	})
	httpResponse := types.RecordType("HTTPResponse", []types.RecordField{
		{Name: "body", Type: types.StringType()},
		{Name: "status_code", Type: types.IntType()},
		{Name: "headers", Type: anyMap()},
		// 共享原型 = 语言级：只放**平台无关**的响应字段。
		// `files`（响应附件数组）是参考目标 http-request 的固定端口，coze-biz `type: http`
		// 节点只有 body/headers/statusCode（② 语料 3/3）——平台专属字段不进语言，
		// 参考目标侧按其自身端口契约另作适配（2026-09-14 用户拍板 R2）。
	})
	m := anyMap()
	return []builtinSpec{
		{name: "llm", params: []types.Descriptor{*types.StringType(), *m}, ret: llmResponse},
		{name: "embed", params: []types.Descriptor{*types.StringType(), *types.StringType()}, ret: types.ArrayType(types.FloatType())},
		{name: "knowledge", params: []types.Descriptor{*types.StringType(), *types.StringType(), *m}, ret: types.ArrayType(searchResult)},
		{name: "http", params: []types.Descriptor{*types.StringType(), *types.StringType(), *m}, ret: httpResponse},
		// A12-H17（2026-09-17 用户裁定 (a)）：`print` 已**逐出语言**（零兼容）。
		// 它原本是调试函数，语义与工作流的输出不是一回事；保留它只能得到
		// 「编译 SUCCEEDED、产物里什么都没有」的静默丢弃（IR 语句位分派表不含它
		// ⇒ discardExprEffects 收敛为零节点）。作者要输出请写 `output(...)`。
		// 反编译侧从不产出 `print(...)`（raising 产出 `output(...)`），故删它不动往返。
		{name: "len", params: []types.Descriptor{*types.AnyType()}, ret: types.IntType()}, // any（array|string|map，shimLen 按容器求长度；反编译 len(role) 字符串长度条件）
		// G-empty (2026-09-01, EMPTY-CHECK-DESIGN): empty(x) 返回 boolean——
		// string 空串 / array 零元素 / record|object 空对象 / optional null
		// 视为空（与平台运行期一致）。条件位原生映射 coze isEmpty/not_empty
		// （op 9/10），表达式位物化为 `not x` shim。
		{name: "empty", params: []types.Descriptor{*types.AnyType()}, ret: types.BoolType()},
		{name: "trim", params: []types.Descriptor{*types.StringType()}, ret: types.StringType()},
		{name: "join", params: []types.Descriptor{*types.ArrayType(types.StringType()), *types.StringType()}, ret: types.StringType()},
		{name: "split", params: []types.Descriptor{*types.StringType(), *types.StringType()}, ret: types.ArrayType(types.StringType())},
		{name: "contains", params: []types.Descriptor{*types.StringType(), *types.StringType()}, ret: types.BoolType()},
		{name: "code", params: []types.Descriptor{*types.StringType(), *m}, ret: m},
		{name: "output", params: []types.Descriptor{*types.StringType(), *types.AnyType()}, ret: types.VoidType()},
		{name: "push", params: []types.Descriptor{*types.ArrayType(types.AnyType()), *types.AnyType()}, ret: types.VoidType()},
		{name: "to_string", params: []types.Descriptor{*types.AnyType()}, ret: types.StringType()},
		// G13: int(x) numeric cast (string/float/int → int), mirror of
		// to_string's Any-typed input contract.
		{name: "int", params: []types.Descriptor{*types.AnyType()}, ret: types.IntType()},
		// G14: float(x) numeric cast (string/int/float → float)，与 int(x) 同形
		// （Any 入参契约）：平台数值参数的真实入参常是数值字符串，参数边界
		// 归一需要它（R-128）。
		{name: "float", params: []types.Descriptor{*types.AnyType()}, ret: types.FloatType()},
		// file(x) — string→file 显式类型标注 cast（只接受 string：int/float/
		// bool 走通用函数校验报 TYPE_MISMATCH; file 输入幂等透传）。coze
		// 平台 file 输入本质是 URL 字符串, 所以编译端零节点透传（无 shim
		// 无节点, resolveValueRef/渲染器把 file(x) 解包为 x 的 ref）; 反
		// 编译器在 string→file 参数/端口边界自动注入 file(...)。
		{name: "file", params: []types.Descriptor{*types.StringType()}, ret: types.FileType()},
		// G15: residual-set closing builtins (replace / first_non_empty /
		// range / flatten) — string substitution, first truthy element of a
		// string array, inclusive-exclusive int range, and one-level array
		// flattening. All four render as Synthetic python shims in both
		// backends (calcBuiltinPython / legacy renderCall parity).
		{name: "replace", params: []types.Descriptor{*types.StringType(), *types.StringType(), *types.StringType()}, ret: types.StringType()},
		{name: "first_non_empty", params: []types.Descriptor{*types.ArrayType(types.StringType())}, ret: types.StringType()},
		{name: "range", params: []types.Descriptor{*types.IntType(), *types.IntType()}, ret: types.ArrayType(types.IntType())},
		{name: "flatten", params: []types.Descriptor{*types.ArrayType(types.ArrayType(types.StringType()))}, ret: types.ArrayType(types.StringType())},
		// G16: arithmetic/array composition builtins (div / repeat / concat) —
		// integer floor division (// is comment syntax, so a builtin), array
		// replication ([v]*n), and array concatenation (a+b). X15 chunks
		// `[N]*num + [n%N]` migrates declaratively.
		{name: "div", params: []types.Descriptor{*types.IntType(), *types.IntType()}, ret: types.IntType()},
		{name: "repeat", params: []types.Descriptor{*types.IntType(), *types.IntType()}, ret: types.ArrayType(types.IntType())},
		{name: "concat", params: []types.Descriptor{*types.ArrayType(types.IntType()), *types.ArrayType(types.IntType())}, ret: types.ArrayType(types.IntType())},
		// G16: enumerate(arr) — numbered pairing view array<T> →
		// array<Enumerated<T>> (Enumerated = { index: integer; value: T; }).
		// The static fallback signature is Any-typed; the type pass
		// special-cases the call (visitEnumerateCall) to derive the real
		// element type so pair.value.field resolves type-safely.
		{name: "enumerate", params: []types.Descriptor{*types.ArrayType(types.AnyType())}, ret: types.ArrayType(types.AnyType())},
	}
}

// operationBuiltins ports the target-profile operation builtins
// (scope_pass.py:61-73): the reference registers every operation from
// ctx.target_profile.operations.entries AFTER DSLBuiltinRegistry.register_all
// and BEFORE the std namespace, so operation calls resolve as builtins. Task
// 10 kept all four operations unregistered (SymbolID offset deviation); Task
// 25c re-registers extract_text (the operation function type: file:any →
// record{text:string}, operation_registry.py:213-218); Task 25d registers
// agent (model/query/config: string/string/map<string,any> →
// record{text:string}, operation_registry.py:179-188 — parameter types
// mirror type_ops.go:142-153); Task 25e registers classify (model/query/
// config: string/string/map<string,any> → empty
// record{OperationResult[classify]}, operation_registry.py:230-238 — the
// type pass resolves the real enum result type, type_pass.py:1169-1174);
// Task 25f registers extract (model/text/config: string/string/
// map<string,any> → empty record{OperationResult[extract]},
// operation_registry.py:219-229 — the type pass resolves the real
// Extraction[Name] result type, type_pass.py:1154-1162). With all five
// operation builtins registered the Task 10 SymbolID offset deviation is
// zero (25f completes the set).
func operationBuiltins() []builtinSpec {
	return []builtinSpec{
		{
			name:   "extract_text",
			params: []types.Descriptor{*types.AnyType()},
			ret:    types.RecordType("OperationResult[extract_text]", []types.RecordField{{Name: "text", Type: types.StringType()}}),
		},
		{
			name:   "agent",
			params: []types.Descriptor{*types.StringType(), *types.StringType(), *anyMap()},
			ret:    types.RecordType("OperationResult[agent]", []types.RecordField{{Name: "text", Type: types.StringType()}}),
		},
		{
			name:   "classify",
			params: []types.Descriptor{*types.StringType(), *types.StringType(), *anyMap()},
			ret:    types.RecordType("OperationResult[classify]", nil),
		},
		{
			name:   "extract",
			params: []types.Descriptor{*types.StringType(), *types.StringType(), *anyMap()},
			ret:    types.RecordType("OperationResult[extract]", nil),
		},
	}
}

// DefineRunBuiltin registers the agent-mode entry builtin
// run(query) -> string (@mode agent). It is only registered for agent
// programs, so workflow/advanced-chat reject the call as an undefined
// variable. Idempotent like RegisterBuiltins.
func DefineRunBuiltin(chain *Chain) {
	global := chain.Scope(chain.Global())
	if global.ResolveSymbolLocal("run") != nil {
		return
	}
	sym := NewSymbol("run", SymbolBuiltinFunction,
		types.FunctionType([]types.Descriptor{*types.StringType()}, types.StringType()), false, true)
	id := chain.NewSymbolID()
	global.DefineSymbol("run", sym, id)
}

// DefineConversationHistoryBuiltin registers the advanced-chat entry builtin
// conversation_history() -> string (@mode advanced-chat): reads the current
// conversation's rendered history (M13 chatflow semantics). Only registered
// for advanced-chat programs, so workflow/agent reject the call as undefined.
// Idempotent like RegisterBuiltins.
func DefineConversationHistoryBuiltin(chain *Chain) {
	global := chain.Scope(chain.Global())
	if global.ResolveSymbolLocal("conversation_history") != nil {
		return
	}
	sym := NewSymbol("conversation_history", SymbolBuiltinFunction,
		types.FunctionType(nil, types.StringType()), false, true)
	id := chain.NewSymbolID()
	global.DefineSymbol("conversation_history", sym, id)
}

// RegisterOperationBuiltins registers the operation builtins in the chain's
// global scope, mirroring scope_pass.py:61-73 (the operations loop after
// register_all; Python mutates an existing builtin's type_info to the
// operation signature, the Go form keeps the existing definition untouched —
// llm/http/knowledge already carry matching signatures and
// extract_text is not previously bound). Idempotent like RegisterBuiltins.
func RegisterOperationBuiltins(chain *Chain) {
	global := chain.Scope(chain.Global())
	for _, spec := range operationBuiltins() {
		if global.ResolveSymbolLocal(spec.name) != nil {
			continue
		}
		sym := NewSymbol(spec.name, SymbolBuiltinFunction,
			types.FunctionType(spec.params, spec.ret), false, true)
		id := chain.NewSymbolID()
		global.DefineSymbol(spec.name, sym, id)
	}
}

// RegisterBuiltins registers every built-in function in the chain's global
// scope, mirroring DSLBuiltinRegistry.register_all(chain). It is idempotent:
// a name already bound in the global scope is left untouched (the Python
// chain.define returns the existing symbol and never overwrites), so
// registering twice neither fails nor replaces the first symbol.
//
// Python registers into the current scope; the Go port targets the global
// scope explicitly because every caller (scope pass run and the standalone
// registry tests) invokes it at global depth — deviation recorded in the
// binder report.
func RegisterBuiltins(chain *Chain) {
	global := chain.Scope(chain.Global())
	register := func(spec builtinSpec) {
		if global.ResolveSymbolLocal(spec.name) != nil {
			return // already bound (Python define returns existing, no overwrite)
		}
		sym := NewSymbol(spec.name, SymbolBuiltinFunction,
			types.FunctionType(spec.params, spec.ret), false, true)
		id := chain.NewSymbolID()
		global.DefineSymbol(spec.name, sym, id)
	}
	for _, spec := range calculationBuiltins() {
		register(spec)
	}
	for _, spec := range mainBuiltins() {
		register(spec)
	}
}

// JSONNamespaceType mirrors the std namespace pattern (stdlib_registry.go):
// the json namespace exposes the declarative projection primitive
// `json.stringify(value) -> string` as a nested record field, so the type
// pass narrows json.stringify field accesses to the function descriptor and
// the generic function-application path types the call (zero type-pass
// changes; G8 — see TARGET-OPTIMIZATION-PROJECTION-STRINGIFY.md).
func JSONNamespaceType() *types.Descriptor {
	return types.RecordType("json", []types.RecordField{
		{
			Name: "stringify",
			Type: types.FunctionType([]types.Descriptor{*types.AnyType()}, types.StringType()),
		},
		{
			// json.parse<T>(input) — schema-driven JSON deserialization
			// (G11). The declared return is Any; visitCallExpr special-cases
			// the call and narrows it to the type argument T, so the
			// generic function-application path never sees parse without a
			// type argument.
			Name: "parse",
			Type: types.FunctionType([]types.Descriptor{*types.StringType()}, types.AnyType()),
		},
	})
}
