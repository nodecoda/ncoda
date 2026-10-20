package passes

// Expression type inference, ported from the expression visitors of
// src/nclang/lang/passes/type_pass.py (literals :635-662, aggregates
// :663-821, identifiers/field access/index :822-1088, foreign code
// :974-1041, calls/collections/coalesce :1089-1590, unary/binary/ternary
// :1591-1758, lambda :1759-1800) plus the module-level helpers. See
// type_pass.go for the pass skeleton and the recording contract.

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/llm"
	"github.com/nodecoda/ncoda/pkg/tool"
	"github.com/nodecoda/ncoda/pkg/types"
)

// ── literals / templates (type_pass.py:635-662) ────────────────────────

func (p *TypePass) visitTemplateString(n *ast.TemplateString) *types.Descriptor {
	for _, part := range n.Parts() {
		if part.Expr == nil {
			continue
		}
		// 模板插值平台运行期容错：字段访问路径不存在渲染空（不强类型报错）。
		saved := p._lenientTemplate
		p._lenientTemplate = true
		partType := p.visit(part.Expr)
		p._lenientTemplate = saved
		if types.IsWellFormedEnum(types.UnwrapOptional(partType)) {
			p.emit(diag.TYPE_MISMATCH,
				"Enum template interpolation requires an explicit '.value' projection", part.Expr.Span())
		}
	}
	return types.StringType()
}

// ── aggregates (type_pass.py:663-821) ──────────────────────────────────

// visitArrayLit mirrors visit_array_lit (type_pass.py:663-704): expected
// array/any shapes constrain members; otherwise members join with the
// aggregate rules (E1003 on incompatible joins).
func (p *TypePass) visitArrayLit(n *ast.ArrayLit) *types.Descriptor {
	expected := p.topExpected()
	if len(n.Elements()) == 0 {
		if expected != nil {
			if expected.Kind == types.Array {
				return expected
			}
			if expected.Kind == types.Any {
				return types.ArrayType(types.AnyType())
			}
		}
		p.emit(diag.EMPTY_AGGREGATE_TYPE,
			"Empty array literal requires an explicit array type", n.Span())
		return types.ArrayType(types.AnyType())
	}
	if expected != nil && expected.Kind == types.Array {
		for _, element := range n.Elements() {
			candidate := p.visitWithExpected(element, expected.Elem)
			if !types.IsAssignable(candidate, expected.Elem) {
				p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
					"Array element has incompatible type %s; expected %s", candidate, expected.Elem), element.Span())
			}
		}
		return expected
	}
	elementType := p.visit(n.Elements()[0])
	for _, element := range n.Elements()[1:] {
		candidate := p.visit(element)
		joined := types.JoinAggregateTypes(elementType, candidate)
		if joined == nil {
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
				"Array element has incompatible type %s; expected %s", candidate, elementType), element.Span())
		} else {
			elementType = joined
		}
	}
	return types.ArrayType(elementType)
}

// visitMapLit mirrors visit_map_lit (type_pass.py:705-805): expected
// map/record/any shapes constrain entries (record field checks, map
// key/value checks); without an expected type the entries join with the
// aggregate rules (E1003 on incompatible joins). This is also the MapLit
// panic-release point: every MapLit in a clean program receives a map or
// record type, so binding.TypeBind's requireAggregates guard never fires.
// llmConfigKnownKeyTypes 从 pkg/llm.Vocabulary **派生**（一处定义，2026-10-20）。
//
// 可选性用 OptionalType 表达（键缺失合法）；int→float 拓宽由 IsAssignable
// 提供（temperature/top_p 语料实证：0.8/1 都合法）；未知键保持开放
// （map<string,any>, L2 边界）。
//
// 以前这张表是手写的，与 coze-biz lowering、gateway adapter 白名单三处独立
// 演化（新增一个键要改三处，且可以静默分叉）。词表成为唯一权威后，这里只做
// 「词表 → 类型检查表」的投影；别名（generation_diversity / model_name）也
// 从词表带类型，旧源码继续受类型检查保护。
var llmConfigKnownKeyTypes = llm.KnownKeyTypes()

// checkLLMConfig 显式检查 llm() config 的已知键类型（ANY-SHRINK L1-a
// 推广 checkLLMMessageContents 为通用 config 校验）。聚合路径在 Any
// 期望下不再检查 map 值（drawing_board variables 允许 string/map
// 异构值），因此 config 已知键的类型强制改在这里完成。messages 键
// 额外走消息体 role/content 字符串检查（替代旧聚合误报）。
func (p *TypePass) checkLLMConfig(config *ast.MapLit) {
	for _, entry := range config.Entries() {
		key, ok := toolStringValue(entry.Key)
		if !ok {
			continue
		}
		if key == "messages" {
			p.checkLLMMessageContents(config)
			continue
		}
		spec, known := llm.Lookup(key)
		if !known {
			continue // 未知名：L2 边界保持开放
		}
		if spec.Deprecated != "" {
			p.emitWarning(diag.LLM_CONFIG_DEPRECATED,
				fmt.Sprintf("llm config key '%s' is deprecated: %s", key, spec.Deprecated),
				entry.Key.Span())
		}
		if len(spec.AllowedValues) > 0 {
			// 枚举值检查是**值**层面的事（类型系统只有 string，没有 union /
			// 字面量枚举）。旧数值码按 AllowLegacyInt 降级为 warning。
			// 字面量已由本函数完整判定 ⇒ 跳过下面的通用类型检查（避免
			// response_format: 9 同时报「枚举非法」和「int 不是 string」两条）。
			if p.checkLLMConfigEnum(key, spec, entry.Value) {
				continue
			}
		}
		expected := spec.Type
		if expected == nil {
			continue
		}
		t := p.visit(entry.Value)
		if t == nil || t.Kind == types.Null {
			continue
		}
		if !types.IsAssignable(t, expected) {
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
				"llm config key '%s' expected %s, got %s", key, expected, t), entry.Value.Span())
		}
	}
}

// checkLLMConfigEnum 校验字符串枚举键（当前只有 response_format）。
// 返回 true = 字面量已完整判定（调用方跳过通用类型检查）。
//
// 非字面量（变量 / 表达式）返回 false：类型检查仍然要求 string；运行期取值由
// 目标绑定层校验（coze-biz：compile-side ledger；gateway：param_policy）。
func (p *TypePass) checkLLMConfigEnum(key string, spec llm.KeySpec, expr ast.Expr) bool {
	switch v := expr.(type) {
	case *ast.StringLit:
		for _, allowed := range spec.AllowedValues {
			if v.Value() == allowed {
				return true
			}
		}
		p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
			"llm config key '%s' expected one of %v, got %q", key, spec.AllowedValues, v.Value()), expr.Span())
		return true
	case *ast.IntLit:
		if spec.AllowLegacyInt && v.Value() >= 0 && v.Value() <= 2 {
			p.emitWarning(diag.LLM_CONFIG_DEPRECATED, fmt.Sprintf(
				"llm config key '%s': numeric code %d is deprecated; use \"text\" / \"json\" / \"markdown\"",
				key, v.Value()), expr.Span())
			return true
		}
		p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
			"llm config key '%s' expected one of %v, got %d", key, spec.AllowedValues, v.Value()), expr.Span())
		return true
	case *ast.MapLit:
		// provider 原生的结构化对象形（标准 LANGUAGE-REFERENCE 与示例
		// 62_llm_open_object 都写 {"type": "json_object"}）。标准把
		// response_format 当开放性扩展，并未列入已知键类型表 ⇒ 对象形必须
		// 继续可编译；具体承载（coze-biz → JSON 数值码 / runtime 原生对象）
		// 由目标绑定层判定，而不是在语言层拒收。
		return true
	}
	return false
}

// checkLLMMessageContents 显式检查 llm() config 的 messages 消息体:
// role/content 必须是字符串。聚合路径在 Any 期望下不再检查 map 值
// （drawing_board variables 允许 string/map 异构值）,因此 message
// content 的类型强制改在这里完成（替代旧聚合误报）。
func (p *TypePass) checkLLMMessageContents(config *ast.MapLit) {
	for _, entry := range config.Entries() {
		key, ok := toolStringValue(entry.Key)
		if !ok || key != "messages" {
			continue
		}
		arr, ok := entry.Value.(*ast.ArrayLit)
		if !ok {
			continue
		}
		for _, el := range arr.Elements() {
			mm, ok := el.(*ast.MapLit)
			if !ok {
				continue
			}
			for _, me := range mm.Entries() {
				k, ok := toolStringValue(me.Key)
				if !ok || (k != "content" && k != "role") {
					continue
				}
				t := p.visit(me.Value)
				if t == nil || t.Kind == types.Null {
					continue
				}
				if !types.IsAssignable(t, types.StringType()) {
					p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
						"llm message '%s' expected string, got %s", k, t), me.Value.Span())
				}
			}
		}
	}
}

func (p *TypePass) visitMapLit(n *ast.MapLit) *types.Descriptor {
	// subflowInput is this MapLit's subflow name when it is a subflow
	// call-site argument map (D-P31-1), else "". Consumed here so nested
	// map literals inside the argument do not inherit it.
	subflowInput := p.subflowInputPending
	p.subflowInputPending = ""
	expected := p.topExpected()
	if len(n.Entries()) == 0 && expected != nil && expected.Kind == types.Map {
		return expected
	}
	if expected != nil && expected.Kind == types.Any {
		// Any 是"不检查"语义: 嵌套 map（如 drawing_board.render 的
		// variables）在 Any 期望下不应触发聚合 join 误报 —— 值可以是
		// string 变量（toolValueTree ref 叶子）或 map/any 值。
		// 记录 key 类型后返回 map<string,any>,与空 map 分支一致；嵌套
		// MapLit 仍须访问（publish exprTypes），否则干净程序在
		// requireCompleteAggregateFacts 处 panic（命名 record 类型修复
		// 暴露的潜在缺陷：any 参数下的嵌套 dict 从未被推断）。
		for _, entry := range n.Entries() {
			p.visit(entry.Value)
		}
		return types.MapType(types.StringType(), types.AnyType())
	}
	if len(n.Entries()) == 0 && !(expected != nil && expected.Kind == types.Record) {
		p.emit(diag.EMPTY_AGGREGATE_TYPE,
			"Empty map literal requires an explicit map or Record type", n.Span())
		return types.MapType(types.StringType(), types.AnyType())
	}
	if expected != nil && expected.Kind == types.Record {
		actualFields := map[string]bool{}
		for _, entry := range n.Entries() {
			keyName, keyOK := p.staticMapKey(entry.Key)
			if subflowInput != "" && !keyOK {
				// The subflow input contract is a fixed-name record: a key
				// the compiler cannot name statically cannot be bound to a
				// declared input port.
				p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
					"Subflow '%s' input field name must be a static name", subflowInput), entry.Key.Span())
				p.visit(entry.Value)
				continue
			}
			if !keyOK || !recordHasField(expected, keyName) {
				if subflowInput != "" {
					p.emit(diag.FIELD_NOT_FOUND, fmt.Sprintf(
						"Subflow '%s' input field '%s' is not declared", subflowInput, keyName), entry.Key.Span())
				} else {
					p.emit(diag.TYPE_MISMATCH,
						"Record literal has an unknown or non-string field", entry.Key.Span())
				}
				p.visit(entry.Value)
				continue
			}
			actualFields[keyName] = true
			if subflowInput != "" {
				// The target input boundary accepts the bound value as-is
				// (the platform does its own coercion at the port), so the
				// call-site contract check is key-scoped: visiting with Any
				// is exactly the old open-map expectation for the value.
				p.visitWithExpected(entry.Value, types.AnyType())
				continue
			}
			fieldType := recordFieldType(expected, keyName)
			candidate := p.visitWithExpected(entry.Value, fieldType)
			if !types.IsAssignable(candidate, fieldType) {
				p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
					"Record field '%s' expected %s, got %s", keyName, fieldType, candidate), entry.Value.Span())
			}
		}
		missing := missingRecordFields(expected, actualFields)
		if len(missing) > 0 {
			if subflowInput != "" {
				// 未绑定的声明入参 = 目标承载面的软降级（平台按自己的默认值
				// 补），不是语言错：语言允许少传（AGENTS.md：软降级必须报账、
				// 不许升 error）。正本 = docs/five-step/spec/C-construct-algorithms.md
				// 「subflow 调用点入参契约」。
				p.emitWarning(diag.TARGET_CONTRACT_DEGRADED, fmt.Sprintf(
					"Subflow '%s' call does not bind declared input field(s): %s (the target feeds its default value)",
					subflowInput, strings.Join(missing, ", ")), n.Span())
			} else {
				p.emit(diag.TYPE_MISMATCH,
					"Record literal is missing fields: "+strings.Join(missing, ", "), n.Span())
			}
		}
		return expected
	}
	if expected != nil && expected.Kind == types.Map {
		for _, entry := range n.Entries() {
			candidateKey := p.mapKeyType(entry.Key, expected.Key)
			if candidateKey.Kind != types.String {
				p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
					"Map key has incompatible type %s; expected %s", candidateKey, expected.Key), entry.Key.Span())
			}
			candidateValue := p.visitWithExpected(entry.Value, expected.Val)
			if !types.IsAssignable(candidateValue, expected.Val) {
				p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
					"Map value has incompatible type %s; expected %s", candidateValue, expected.Val), entry.Value.Span())
			}
		}
		return expected
	}
	entries := n.Entries()
	keyType := p.mapKeyType(entries[0].Key, nil)
	if keyType.Kind != types.String {
		p.emit(diag.TYPE_MISMATCH,
			fmt.Sprintf("map key must be string, got %s", keyType), entries[0].Key.Span())
	}
	// 无期望类型 + 全静态字符串键 → record 推断（字段独立类型）。异构字段
	// 不再整体降级 map<string,any>（字段访问全丢类型，X212 mergeGroups
	// roundtrip 实证：Group1 string / Group3 array<string> 同 dict）。
	// 空 dict 已在上方显式报错；动态键保持 map 聚合推断。
	allStatic := true
	for _, entry := range entries {
		if _, ok := p.staticMapKey(entry.Key); !ok {
			allStatic = false
			break
		}
	}
	if allStatic {
		fields := make([]types.RecordField, 0, len(entries))
		for _, entry := range entries {
			ftype := p.visit(entry.Value)
			keyName, _ := p.staticMapKey(entry.Key)
			fields = append(fields, types.RecordField{Name: keyName, Type: ftype})
		}
		return types.RecordType("", fields)
	}
	valueType := p.visit(entries[0].Value)
	for _, entry := range entries[1:] {
		candidateKey := p.mapKeyType(entry.Key, nil)
		if candidateKey.Kind != types.String {
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
				"Map key has incompatible type %s; expected %s", candidateKey, keyType), entry.Key.Span())
		}
		candidateValue := p.visit(entry.Value)
		joinedValue := types.JoinAggregateTypes(valueType, candidateValue)
		if joinedValue == nil {
			// 无期望类型时 dict 字面量自证类型：异构值（如
			// {"code": float, "error_message": string}）合法，放宽为
			// map<string,any> 而非报错 —— 报错仅在有期望 map 类型时
			// 触发（上面 expected.Map 分支）。widen 后仍继续 visit
			// 后续值，保留 ExprTypes 记录副作用。
			valueType = types.AnyType()
			continue
		}
		valueType = joinedValue
	}
	return types.MapType(types.StringType(), valueType)
}

// staticMapKey reports the static key name of a map entry and whether the key is
// static at all. Rule authority = ast.StaticMapKey (single implementation,
// 2026-09-14); this method only adds the side effect (registering the key node's
// type as string) and keeps the (name, ok) shape so an **empty-string key** stays
// distinguishable from a dynamic key — the retired `name == ""` test conflated
// the two, which demoted a fully static literal like `{ "": 1 }` to
// map<string,any> (debt R2 / D-P30-1; raising has always read the (name, ok)
// shape, so both sides now agree: the empty key is a *label* named "").
func (p *TypePass) staticMapKey(key ast.Expr) (string, bool) {
	name, ok := ast.StaticMapKey(key)
	if !ok {
		return "", false
	}
	if id, ok := p.nodeID(key); ok {
		p.ctx.ExprTypes[id] = types.StringType()
	}
	return name, true
}

// mapKeyType mirrors _map_key_type (type_pass.py:812-816).
func (p *TypePass) mapKeyType(key ast.Expr, expected *types.Descriptor) *types.Descriptor {
	if _, ok := p.staticMapKey(key); ok {
		return types.StringType()
	}
	return p.visitWithExpected(key, expected)
}

// topExpected returns the innermost expected type (type_pass.py
// self._expected_types[-1]), or nil when the stack is empty.
func (p *TypePass) topExpected() *types.Descriptor {
	if len(p._expectedTypes) == 0 {
		return nil
	}
	return p._expectedTypes[len(p._expectedTypes)-1]
}

// ── identifiers / field access / indexing (type_pass.py:822-1088) ──────

func (p *TypePass) visitIdentExpr(n *ast.IdentExpr) *types.Descriptor {
	symID, found := p.referenceSymbol(n)
	if !found {
		p.emit(diag.UNDEFINED_VAR, fmt.Sprintf("Undefined variable: '%s'", n.Name()), n.Span())
		return types.AnyType()
	}
	decl, ok := p.snap.Declarations().Get(symID)
	if !ok {
		return types.AnyType()
	}
	if decl.Kind == binding.SymbolType {
		p.emit(diag.TYPE_MISMATCH,
			fmt.Sprintf("Type '%s' cannot be used as a value; select a qualified member", n.Name()), n.Span())
		return types.AnyType()
	}
	if decl.Kind == binding.SymbolNamespace {
		// Builtin namespaces (std, json) expose real members through field
		// access (std.v1.<function>, json.stringify) — using the namespace
		// itself as a value yields its record type. Only an import
		// namespace (a capability provider) used as a value is an error:
		// its members resolve exclusively through the
		// qualified-capability-call interception. The explicit NS
		// discriminant (I2) replaces the former Type.Kind == Any guess.
		if decl.NS == binding.NamespaceBuiltin {
			return decl.Type
		}
		p.emit(diag.TYPE_MISMATCH,
			fmt.Sprintf("Package namespace '%s' cannot be used as a value; select a qualified operation (e.g. %s.op(...))", n.Name(), n.Name()), n.Span())
		return types.AnyType()
	}
	for i := len(p._narrowedTypes) - 1; i >= 0; i-- {
		if narrowed, ok := p._narrowedTypes[i][symID]; ok {
			return narrowed
		}
	}
	return p.symbolType(symID)
}

// visitFieldAccessExpr mirrors visit_field_access_expr (type_pass.py:846-973):
// the enum-member path publishes EnumMemberFacts (T3); records, enums, file
// fields, string methods, any and direct-code fallbacks follow the
// reference's order.
func (p *TypePass) visitFieldAccessExpr(n *ast.FieldAccessExpr) *types.Descriptor {
	if obj, ok := n.Obj().(*ast.IdentExpr); ok {
		if symID, found := p.referenceSymbol(obj); found {
			decl, _ := p.snap.Declarations().Get(symID)
			if decl.Kind == binding.SymbolType && decl.Type != nil && decl.Type.Kind == types.Enum {
				enumType := decl.Type
				if !types.IsWellFormedEnum(enumType) {
					p.emit(diag.UNKNOWN_TYPE,
						fmt.Sprintf("Malformed enum type: '%s'", decl.Name), obj.Span())
					return types.AnyType()
				}
				if id, ok := p.nodeID(obj); ok {
					p.ctx.ExprTypes[id] = enumType
				}
				if !containsString(enumType.EnumMembers, n.FieldName()) {
					p.emit(diag.FIELD_NOT_FOUND,
						fmt.Sprintf("Enum member '%s.%s' is not declared", decl.Name, n.FieldName()), n.Span())
					return types.AnyType()
				}
				if id, ok := p.nodeID(n); ok {
					p.ctx.EnumMembers[id] = binding.EnumMemberFact{
						EnumType:  enumType,
						Member:    n.FieldName(),
						WireValue: n.FieldName(),
					}
				}
				return enumType
			}
		}
	}
	objType := p.visit(n.Obj())
	nullable := false
	if objType.Kind == types.Optional {
		if objType.Elem == nil {
			return types.AnyType()
		}
		// M2（2026-09-05 用户拍板）：Optional 记录成员访问 = 空传播 ——
		// 解开到 Elem 解析成员，结果统一包回 Optional（成员类型 T? 的
		// 运行时值：接收者为 null → 整条读值为 null）。取代旧 OPTIONAL_REQUIRED
		// 硬错误。全称无例外：一切成员（含方法/枚举值/投影）读 Optional 都
		// 得 Optional，下游必须经 ?? / 判空收敛。
		nullable = true
		objType = objType.Elem
	}
	res := func() *types.Descriptor {
		switch objType.Kind {
		case types.Record:
			// direct code 值（foreign code -> T）的 .result 段 = 平台单端口引用，
			// 值即端口内容 → 类型 = 值本身（X262 v<id>.result.<field> roundtrip；
			// direct 拍平 record 类型不含 result 字段，普通 Record 分支误报
			// FIELD_NOT_FOUND 'result'）。标量 direct code（string/int）不动：
			// string 走宽松 any（X70 既有行为），int 走 default 分支报错
			// （TestScalarDirectCodeAliasFieldAccessRegression 锁定）。
			if p.isDirectCodeValue(n.Obj()) && n.FieldName() == "result" {
				if id, ok := p.nodeID(n); ok {
					p.ctx.ExprTypes[id] = objType
				}
				return objType
			}
			if f, ok := recordField(objType, n.FieldName()); ok {
				return f
			}
			// B3：record.contains / not_contains = 键包含（平台 object 的 op 7/8
			// 语义；record 是封闭 object）。字段名优先（record 真字段叫
			// "contains" 时走字段），方法其次。
			if ft := containerContainsMethod(n.FieldName(), types.StringType()); ft != nil {
				return ft
			}
			if isLLMResponseType(objType) {
				p.emit(diag.TYPE_MISMATCH,
					fmt.Sprintf("Legacy llm record field access '.%s' is invalid; use '.text.%s'", n.FieldName(), n.FieldName()), n.Span())
				return types.AnyType()
			}
			if p._lenientTemplate {
				return types.AnyType() // 模板插值容错（平台渲染空）
			}
			p.emit(diag.FIELD_NOT_FOUND,
				fmt.Sprintf("Field '%s' not found in struct", n.FieldName()), n.Span())
		case types.Enum:
			// Python requires obj_type.params to be an EnumType; the Go
			// descriptor cannot distinguish a bare enum from a nominal one, so
			// an empty name plays the malformed role (never constructed by the
			// type layer — defensive).
			if objType.EnumName == "" {
				p.emit(diag.UNKNOWN_TYPE, "Malformed enum value type", n.Span())
				return types.AnyType()
			}
			if n.FieldName() == "value" {
				return types.StringType()
			}
			p.emit(diag.FIELD_NOT_FOUND, fmt.Sprintf(
				"Field '%s' not found on enum '%s'; only '.value' is available",
				n.FieldName(), objType.EnumName), n.Span())
			return types.AnyType()
		case types.File:
			if f, ok := fileFieldType(n.FieldName()); ok {
				return f
			}
			if p._lenientTemplate {
				return types.AnyType() // 模板插值容错（平台渲染空）
			}
			p.emit(diag.FIELD_NOT_FOUND, fmt.Sprintf(
				"Field '%s' not found on file type. Available fields: %s",
				n.FieldName(), strings.Join(fileFieldNames(), ", ")), n.Span())
			return types.AnyType()
		case types.Array:
			// 循环聚合数组的附加端口访问 agg_<loop>.<port>（多口 roundtrip：
			// loop/batch outputs 的命名口）。端口收集全部迭代 → 类型为
			// array<elem>（LoopPortTypes 存元素类型，与正编译 loop.OutTypes
			// TList(itemType) 一致）。
			if id, ok := n.Obj().(*ast.IdentExpr); ok {
				if symID, found := p.referenceSymbol(id); found {
					if ports, ok := p.ctx.LoopPortTypes[symID]; ok {
						if pt, ok := ports[n.FieldName()]; ok {
							return types.ArrayType(pt)
						}
					}
				}
			}
			// 数组投影：array<record>.field → array<字段类型>。coze 聚合数组成员
			// 投影语义（原始 ref "outputList.data.link" = 端口元素逐元素取
			// data.link；反编译单口循环省略端口名 → agg.data.link）。
			// 元素 record/map 时按字段解析投影（收窄 any，消除隐式 any 源头）。
			//
			// 落到「元素不是 record/map」（如 `array<string>.field`）或
			// 「元素是 record 但**没有**这个字段」两档时，走向下方的 fail loud
			// （R-167，2026-09-29）。修前这里一律 `return any` **零诊断** ——
			// 「失配 ≠ 静默丢弃」的正面命中：lowering 会把这一段拼进 dotted
			// 路径（`{path: outputs.outputs}` = 指向产出方**没声明过**的口），
			// 而产物门禁不校验后续段 ⇒ 悬空引用可出厂。同族先例都在：基值
			// any ⇒ ANY_NOT_NARROWED、基值 void ⇒ FIELD_NOT_FOUND（R-29）、
			// 枚举/file 缺成员 ⇒ FIELD_NOT_FOUND —— 只有 array 这条漏了。
			if objType.Elem != nil {
				switch objType.Elem.Kind {
				case types.Record:
					if f, ok := recordField(objType.Elem, n.FieldName()); ok {
						return types.ArrayType(f)
					}
				case types.Map:
					return types.ArrayType(objType.Elem.Val)
				}
			}
			// B3 (2026-09-01, coze-condition-operator-matrix.md)：array.contains /
			// not_contains = 元素包含（平台 op 7/8 按接收者类型分派：array 元素、
			// string 子串、object 键）。签名参数 = 元素类型（未知 → any 宽松）。
			if ft := containerContainsMethod(n.FieldName(), func() *types.Descriptor {
				if objType.Elem != nil {
					return objType.Elem
				}
				return types.AnyType()
			}()); ft != nil {
				return ft
			}
			if p._lenientTemplate {
				return types.AnyType() // 模板插值容错（平台渲染空），与同层 record/string/file/void 同规则
			}
			if objType.Elem != nil && objType.Elem.Kind == types.Record {
				names := make([]string, 0, len(objType.Elem.Fields))
				for _, f := range objType.Elem.Fields {
					names = append(names, f.Name)
				}
				if len(names) == 0 {
					p.emit(diag.FIELD_NOT_FOUND, fmt.Sprintf(
						"Field '%s' not found: the array element record has no fields",
						n.FieldName()), n.Span())
				} else {
					p.emit(diag.FIELD_NOT_FOUND, fmt.Sprintf(
						"Field '%s' not found on the array element record. Available fields: %s",
						n.FieldName(), strings.Join(names, ", ")), n.Span())
				}
				return types.AnyType()
			}
			p.emit(diag.FIELD_NOT_FOUND, fmt.Sprintf(
				"Field '%s' not found: an array has no members (projection applies to record elements only)",
				n.FieldName()), n.Span())
			return types.AnyType()
		case types.Map:
			// 点号访问 map = 字符串键成员查找，与 visitIndexExpr 的 Map 分支
			// 一致（反编译器对 dict 渲染 .field 形式；obj["field"] 等价）。
			// B3：map.contains / not_contains = 键包含（string 键）。
			if ft := containerContainsMethod(n.FieldName(), types.StringType()); ft != nil {
				return ft
			}
			return objType.Val
		case types.String:
			if n.FieldName() == "contains" {
				return types.FunctionType([]types.Descriptor{*types.StringType()}, types.BoolType())
			}
			if n.FieldName() == "not_contains" {
				// coze string 方法（IR build_expr/pyast_lower 均支持，
				// 反编译条件 not_contains 保留原方法名）：SC193 实证。
				return types.FunctionType([]types.Descriptor{*types.StringType()}, types.BoolType())
			}
			if n.FieldName() == "startsWith" {
				return types.FunctionType([]types.Descriptor{*types.StringType()}, types.BoolType())
			}
			if n.FieldName() == "endsWith" {
				return types.FunctionType([]types.Descriptor{*types.StringType()}, types.BoolType())
			}
			if n.FieldName() == "split" {
				return types.FunctionType([]types.Descriptor{*types.StringType()}, types.ArrayType(types.StringType()))
			}
			// R-167 的另一半（2026-09-29）：string 上取成员 = 语言错（与 Array 同一条
			// 规则、同一句判据）。修前这里静默落 `any` ⇒ 产物带**悬空的 dotted 路径**
			// （一手：`r.url.nosuch`（`url` 声明 string）编译 SUCCEEDED 零诊断，产物
			// `path: r.url.nosuch`），而产物门禁不校验后续段。
			// 两条例外都是**既有事实**，不是为放行而放行：
			//   ① 模板插值容错（平台渲染空），与同层 record/file/void 同规则；
			//   ② **direct code 的标量口**：平台把 code 节点的唯一号口当记录引用
			//      （`v.<成员>`），成员名在声明的 string 口上没有静态证据 —— 属平台
			//      物理形状（X70 家族既有宽松；`TestScalarDirectCodeAliasFieldAccessRegression`
			//      只锁 int 侧报错）。
			if p._lenientTemplate || p.isDirectCodeValue(n.Obj()) {
				return types.AnyType()
			}
			p.emit(diag.FIELD_NOT_FOUND, fmt.Sprintf(
				"Field '%s' not found: a string has no members", n.FieldName()), n.Span())
			return types.AnyType()
		case types.Any:
			p.emit(diag.ANY_NOT_NARROWED,
				"Field access requires a proven record shape, not any", n.Span())
			return types.AnyType()
		case types.Void:
			// void = **没有值** ⇒ 没有成员（R-29，2026-09-24）。走到这里的是
			// 「把没有结果值的东西当记录用」：显式声明空输出表（`-> void`）的
			// 能力调用、或 `-> void` 函数的返回值。既然 void 是**声明的事实**，
			// 这里就必须 fail loud —— 否则这条路径会静默降级成 any（默认分支），
			// 把语言错推到产物门禁上（表现为难懂的物理口名缺失）。
			if p._lenientTemplate {
				return types.AnyType() // 模板插值容错（与同层 record/string/file 同规则）
			}
			p.emit(diag.FIELD_NOT_FOUND, fmt.Sprintf(
				"Field '%s' not found: the value has no members (void)", n.FieldName()), n.Span())
			return types.AnyType()
		default:
			if p.isDirectCodeValue(n.Obj()) {
				p.emit(diag.FIELD_NOT_FOUND, fmt.Sprintf(
					"Field '%s' not found on %s", n.FieldName(), objType), n.Span())
			}
			return types.AnyType()
		}
		return types.AnyType()
	}()
	if nullable && res != nil && res.Kind != types.Any {
		return types.OptionalType(res)
	}
	return res
}

// containerContainsMethod 返回容器 contains/not_contains 方法签名
// （B3，2026-09-01 coze-condition-operator-matrix.md）。参数类型 = 元素/
// 键类型：array 元素包含（param = 元素类型）、record/map/object 键包含
// （param = string）。非容器方法名返回 nil。
func containerContainsMethod(field string, param *types.Descriptor) *types.Descriptor {
	switch field {
	case "contains", "not_contains":
		return types.FunctionType([]types.Descriptor{*param}, types.BoolType())
	}
	return nil
}

func isLLMResponseType(d *types.Descriptor) bool {
	if d == nil || d.Kind != types.Record {
		return false
	}
	seen := map[string]bool{}
	for _, f := range d.Fields {
		seen[f.Name] = true
	}
	if !(seen["text"] && seen["reasoning_content"] && seen["usage"] && seen["finish_reason"]) {
		return false
	}
	// Only record payloads had the legacy top-level field expansion. Default
	// string and scalar payloads use the ordinary FIELD_NOT_FOUND diagnostic.
	for _, f := range d.Fields {
		if f.Name == "text" {
			return f.Type != nil && f.Type.Kind == types.Record
		}
	}
	return false
}

// fileFieldType / fileFieldNames mirror the File field table of
// visit_field_access_expr (type_pass.py:921-946), including the legacy
// source-level aliases; the names keep the reference's dict insertion order
// for the availability message.
func fileFieldType(name string) (*types.Descriptor, bool) {
	switch name {
	case "name", "filename", "extension", "mime_type", "type", "transfer_method", "url", "remote_url", "related_id":
		return types.StringType(), true
	case "size":
		return types.IntType(), true
	}
	return nil, false
}

func fileFieldNames() []string {
	return []string{"name", "filename", "extension", "mime_type", "size", "type",
		"transfer_method", "url", "remote_url", "related_id"}
}

// visitIndexExpr mirrors visit_index_expr (type_pass.py:1042-1088).
func (p *TypePass) visitIndexExpr(n *ast.IndexExpr) *types.Descriptor {
	indexType := p.visit(n.Index())
	objType := p.visit(n.Obj())
	if objType.Kind == types.Optional {
		p.emit(diag.OPTIONAL_REQUIRED,
			"Indexing requires a non-optional value; check it against null first", n.Span())
		if objType.Elem != nil {
			objType = objType.Elem
		} else {
			return types.AnyType()
		}
	}
	switch objType.Kind {
	case types.Array:
		if !isValidIndexType(indexType, types.IntType()) {
			p.emit(diag.INDEX_TYPE,
				fmt.Sprintf("Array index must be int, got %s", indexType), spanOf(n.Index()))
		}
		// E5-b / G12-2: record-typed arrays require a constant index. Registry
		// tool outputs expose only item-0 member access (no native ref form
		// for other indices — the target backends render item-0 dotted
		// ports). json.parse<array<Record>> arrays (G12) additionally
		// support any constant index: G12-2 lowers every element access to
		// an extraction shim, so 0..N are all representable. Foreign code
		// arrays are unaffected (any-typed elements carry no record shape).
		if objType.Elem != nil && objType.Elem.Kind == types.Record {
			lit, ok := n.Index().(*ast.IntLit)
			if !ok {
				p.emit(diag.INDEX_TYPE,
					"Element access on a record array requires a constant index", spanOf(n.Index()))
			} else if lit.Value() != 0 && !objType.JSONParseArray {
				p.emit(diag.INDEX_TYPE,
					"Element access on a record array supports only index 0", spanOf(n.Index()))
			}
		}
		return objType.Elem
	case types.Map:
		if !isValidIndexType(indexType, objType.Key) {
			p.emit(diag.INDEX_TYPE,
				fmt.Sprintf("Map key must be %s, got %s", objType.Key, indexType), spanOf(n.Index()))
		}
		return objType.Val
	case types.Record:
		// 字符串字面量下标 = 成员字段访问（反编译器对关键字字段 code/msg/output
		// 渲染 obj["field"]，与 .field 等价；resolveValueRef 同语义解析）。
		if sl, ok := n.Index().(*ast.StringLit); ok {
			for _, f := range objType.Fields {
				if f.Name == sl.Value() {
					return f.Type
				}
			}
			p.emit(diag.INDEX_TYPE,
				fmt.Sprintf("Record has no field %q", sl.Value()), spanOf(n.Index()))
			return types.AnyType()
		}
		p.emit(diag.INDEX_TYPE,
			fmt.Sprintf("Cannot index %s with %s", objType, indexType), n.Span())
		return types.AnyType()
	case types.Any:
		p.emit(diag.ANY_NOT_NARROWED,
			"Indexing requires a proven array or map shape, not any", n.Span())
	default:
		p.emit(diag.INDEX_TYPE, fmt.Sprintf("Cannot index %s", objType), n.Span())
	}
	return types.AnyType()
}

// ── calls (type_pass.py:1089-1590) ─────────────────────────────────────

// visitCallExpr mirrors visit_call_expr (type_pass.py:1089-1251).
func (p *TypePass) visitCallExpr(n *ast.CallExpr) *types.Descriptor {
	// Qualified capability call (TOOL-DIRECT-CALL-FORM): p.op({...}) where p
	// resolves to a package namespace symbol. Intercepted before normal field
	// access so `tts` is never visited as a value.
	if fa, ok := n.Func().(*ast.FieldAccessExpr); ok {
		if obj, ok := fa.Obj().(*ast.IdentExpr); ok {
			if symID, found := p.referenceSymbol(obj); found {
				if decl, ok := p.snap.Declarations().Get(symID); ok && decl.Kind == binding.SymbolNamespace {
					// Builtin namespaces (json, std) are not capabilities —
					// the normal field-access/function path types their
					// calls. Only import namespaces (capability providers)
					// route to the qualified capability call. Explicit NS
					// discriminant (I2), not the Type.Kind == Any guess.
					if decl.NS == binding.NamespaceBuiltin {
						// fall through to generic function application
					} else if rt, handled := p.visitQualifiedCapabilityCall(n, obj.Name()); handled {
						return rt
					}
				}
			}
		}
	}
	funcType := p.visit(n.Func())
	operation := operationSpec(n, p.ctx)
	if operation != nil {
		if operation.ResultKind == "static" && len(n.TypeArgs()) > 0 {
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
				"Static operation '%s' does not accept type arguments", operation.Name), n.Span())
		}
		if (operation.ResultKind == "schema-extraction" || operation.ResultKind == "classification") &&
			len(n.TypeArgs()) != 1 {
			for _, argument := range n.Args() {
				p.visit(argument)
			}
			label := "enum"
			if operation.ResultKind == "schema-extraction" {
				label = "schema"
			}
			p.emit(diag.ARG_COUNT, fmt.Sprintf(
				"Operation '%s' expects exactly one %s type argument, got %d",
				operation.Name, label, len(n.TypeArgs())), n.Span())
			return types.AnyType()
		}
		minimumCount := 0
		for _, parameter := range operation.Parameters {
			if parameter.Required {
				minimumCount++
			}
		}
		argumentsValid := minimumCount <= len(n.Args()) && len(n.Args()) <= len(operation.Parameters)
		if !argumentsValid {
			p.emit(diag.ARG_COUNT, fmt.Sprintf(
				"Operation '%s' expects %d to %d arguments, got %d",
				operation.Name, minimumCount, len(operation.Parameters), len(n.Args())), n.Span())
		}
		for i, argument := range n.Args() {
			if i >= len(operation.Parameters) {
				break
			}
			parameter := operation.Parameters[i]
			if (operation.ResultKind == "schema-extraction" || operation.ResultKind == "classification") &&
				parameter.Name == "config" {
				// _visit_extraction_metadata (:1252-1267) is dead in Go:
				// extract/classify are unregistered (Task 10 deviation), so
				// this branch is unreachable until Task 25. The Go analogue
				// keeps the map/expression visit split for parity.
				if ml, ok := argument.(*ast.MapLit); ok {
					p.visitExtractionMetadata(ml)
				} else {
					p.visit(argument)
				}
				continue
			}
			actual := p.visitWithExpected(argument, parameter.Type)
			if !types.IsAssignable(actual, parameter.Type) {
				argumentsValid = false
				p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
					"Operation parameter '%s' expected %s, got %s",
					parameter.Name, parameter.Type, actual), argument.Span())
			}
			if operation.Name == "llm" && parameter.Name == "config" {
				if ml, ok := argument.(*ast.MapLit); ok {
					p.checkLLMConfig(ml)
				}
			}
		}
		// Classification branch (type_pass.py:1169-1174): a classify call
		// with valid arguments resolves its nominal enum type argument;
		// invalid arguments (diagnosed above) or an unresolvable enum falls
		// back to Any. The resolve errors surface as TYPE_MISMATCH at the
		// type argument's span, byte-exact with the reference.
		if operation.ResultKind == "classification" {
			if !argumentsValid {
				return types.AnyType()
			}
			enumType, err := binding.ResolveClassificationEnum(n.TypeArgs()[0], p.ctx.NamedTypes)
			if err != nil {
				p.emit(diag.TYPE_MISMATCH, err.Error(), n.TypeArgs()[0].Span())
				return types.AnyType()
			}
			return enumType
		}
		// The schema-extraction branch mirrors type_pass.py:1154-1162: an
		// arguments-invalid call falls back to Any (the argument errors
		// were already emitted above); a resolution failure surfaces as
		// UNKNOWN_TYPE with the Python ValueError text at the type
		// argument's span and falls back to Any; success publishes the
		// ExtractionContract and returns the Extraction[Name] result type.
		if operation.ResultKind == "schema-extraction" {
			if !argumentsValid {
				return types.AnyType()
			}
			contract, err := binding.BuildExtractionContract(n.TypeArgs()[0], p.ctx.NamedTypes)
			if err != nil {
				p.emit(diag.UNKNOWN_TYPE, err.Error(), n.TypeArgs()[0].Span())
				return types.AnyType()
			}
			if nodeID, ok := p.nodeID(n); ok {
				p.ctx.ExtractionContracts[nodeID] = contract
			}
			return contract.ResultType
		}
		// Unified envelope: T always denotes the semantic text payload. Record
		// fields are not expanded into top-level result fields; physical port
		// projection is owned by backend lowering.
		if operation.ResultKind == "llm" {
			if len(n.TypeArgs()) > 1 {
				p.emit(diag.ARG_COUNT, fmt.Sprintf(
					"Operation 'llm' expects at most one text type argument, got %d",
					len(n.TypeArgs())), n.Span())
				return types.AnyType()
			}
			if len(n.TypeArgs()) == 1 {
				textType := p.typeRefToDescriptor(n.TypeArgs()[0])
				if textType == nil {
					p.emit(diag.UNKNOWN_TYPE, "llm type argument is unresolved", n.TypeArgs()[0].Span())
					return types.AnyType()
				}
				if err := validateLLMPayloadType(textType); err != nil {
					p.emit(diag.TYPE_MISMATCH, err.Error(), n.TypeArgs()[0].Span())
					return types.AnyType()
				}
				return llmResponseType(textType)
			}
		}
		return operation.ResultType
	}
	if p.isJSONParseCall(n) {
		return p.visitJSONParseCall(n)
	}
	if len(n.TypeArgs()) > 0 {
		for _, argument := range n.Args() {
			p.visit(argument)
		}
		p.emit(diag.TYPE_MISMATCH,
			"Type arguments require a direct call to a registered schema operation", n.Span())
		return types.AnyType()
	}

	if p.isEnumerateCall(n) {
		return p.visitEnumerateCall(n)
	}
	if collectionName := p.calculationCollectionName(n); collectionName != "" {
		return p.visitCollectionCall(n, collectionName)
	}
	if funcType.Kind == types.Function {
		expectedCount := len(funcType.Params)
		actualCount := len(n.Args())
		minimumCount := expectedCount
		// Python lowers the minimum via the stdlib registry
		// (get_stdlib_function) for optional stdlib params; without a ported
		// registry the lookup always raises and minimum stays expected_count
		// (the except-branch) — deviation recorded (stdlib subsystem is a
		// later task).
		if !(minimumCount <= actualCount && actualCount <= expectedCount) {
			expectedLabel := fmt.Sprintf("%d", expectedCount)
			if minimumCount != expectedCount {
				expectedLabel = fmt.Sprintf("%d to %d", minimumCount, expectedCount)
			}
			p.emit(diag.ARG_COUNT,
				fmt.Sprintf("Expected %s arguments, got %d", expectedLabel, actualCount), n.Span())
		} else {
			// A subflow call's argument map is the DECLARED input contract
			// as a record (D-P31-1). The contract is enforced at the literal
			// (visitMapLit: undeclared key = language error; declared field
			// the caller omitted = target-contract degradation warning); a
			// non-literal argument is the platform input boundary's business,
			// so no record-assignability rejection is added for this call.
			subflowArg := p.subflowCalleeName(n.Func())
			builtin := p.resolvedBuiltinName(n)
			for i, arg := range n.Args() {
				if i >= expectedCount {
					break
				}
				expected := &funcType.Params[i]
				if subflowArg != "" && i == 0 {
					if _, ok := arg.(*ast.MapLit); ok {
						p.subflowInputPending = subflowArg
					}
					p.visitWithExpected(arg, expected)
					p.subflowInputPending = ""
					continue
				}
				// Static config slots wider than the declared parameter type
				// (builtinSlotWidening) are typed **against the widened shape**
				// so element inference and the acceptance test agree, and a
				// widened-but-still-wrong argument keeps the precise
				// TYPE_MISMATCH instead of slipping through to lowering.
				widened := builtinSlotWidening(builtin, i, expected)
				inferAgainst := expected
				if widened != nil {
					inferAgainst = widened
				}
				argType := p.visitWithExpected(arg, inferAgainst)
				if !types.IsAssignable(argType, expected) &&
					(widened == nil || !types.IsAssignable(argType, widened)) {
					p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
						"Argument %d: expected %s, got %s", i+1, expected, argType), arg.Span())
				}
			}
		}
		return funcType.Result
	}
	if funcType.Kind == types.Any {
		p.emit(diag.ANY_NOT_NARROWED,
			"Dynamic calls require a proven function type, not any", spanOf(n.Func()))
	} else {
		p.emit(diag.NOT_CALLABLE, fmt.Sprintf("Not callable: %s", funcType), n.Span())
	}
	return types.AnyType()
}

// visitQualifiedCapabilityCall types a qualified capability call p.op({...})
// (TOOL-DIRECT-CALL-FORM decision 1/2): the provider is the namespace
// identifier (a compile-time constant by syntax) and the operation is the
// field name. The registry definition is the symbol signature: the argument
// map is strong-checked against the declared parameters and the result is
// typed from the declared outputs, or from the open-world default record
// when the definition declares none.
func (p *TypePass) visitQualifiedCapabilityCall(n *ast.CallExpr, provider string) (*types.Descriptor, bool) {
	if p.ctx.ToolRegistry == nil {
		// Closed-world enforcement (design §8.3): the program imports a
		// provider but no registry is configured, so the namespace symbol
		// exists yet no capability can be resolved. Returning (nil, false)
		// falls back to field-access typing, where visitIdentExpr hits the
		// namespace-as-value error — the direct form forces a closed world.
		// This guard IS reachable (import "tts" + Options{} with no registry
		// or tool-paths).
		return nil, false
	}
	fa := n.Func().(*ast.FieldAccessExpr)
	operation := fa.FieldName()
	definition, found := p.ctx.ToolRegistry.Lookup(provider, operation)
	if !found {
		// Deliberately NOT gated on Strict(): the direct call form is always
		// closed world when a registry exists.
		if !p.ctx.ToolRegistry.HasProvider(provider) {
			p.emit(diag.UNKNOWN_TYPE, fmt.Sprintf(
				"undeclared capability provider %q (register it via a capability header or --tool-path)", provider), n.Span())
		} else {
			p.emit(diag.UNKNOWN_TYPE, fmt.Sprintf(
				"unknown capability operation %q in provider %q", operation, provider), n.Span())
		}
		return types.AnyType(), true
	}
	// Arity enforcement (TOOL-DIRECT-CALL-FORM decision 1/2, Task 2 review
	// gate): the direct call form accepts EXACTLY ONE argument — the params
	// map literal (p.op({ "name": value, ... })). Enforced here at the type
	// stage (ARG_COUNT, with a source span) so bad arity never reaches the
	// IR builder, which assumes a single params map: zero arguments must
	// not crash lowering and extra arguments must not be silently dropped.
	if len(n.Args()) != 1 {
		p.emit(diag.ARG_COUNT, fmt.Sprintf(
			"capability call %s.%s expects exactly one argument (the params map literal), got %d",
			provider, operation, len(n.Args())), n.Span())
		return types.AnyType(), true
	}
	if _, ok := n.Args()[0].(*ast.MapLit); !ok {
		p.emit(diag.ARG_COUNT, fmt.Sprintf(
			"capability call %s.%s expects a map literal as its argument, got %T",
			provider, operation, n.Args()[0]), n.Span())
		return types.AnyType(), true
	}
	p.checkRegistryArguments(n, definition)
	// Record the map literal argument's own types (ExprTypes): visiting with
	// the expected map type records the map node and its keys without
	// re-emitting aggregate-join errors on mixed value types.
	// checkRegistryArguments already strong-checks each entry value against
	// the declared parameter type; both happen on the success path.
	if len(n.Args()) > 0 {
		p.visitWithExpected(n.Args()[0], types.MapType(types.StringType(), types.AnyType()))
	}
	outputs := registryToolOutputs(definition)
	if definition.OutputsUndeclared {
		// R-29（2026-09-24 用户裁定「收紧」）：输出表是**头文件的义务**——
		// `function f(...) {}` 没写 `-> T` 就是声明方的错误（类比 C 函数声明
		// 缺返回类型），不是使用方的降级场景 ⇒ 拒收并点名 provider/op/头文件。
		// 回 any 作毒值，避免下游把一次声明错误放大成一串级联诊断。
		p.emit(diag.CAPABILITY_OUTPUTS_UNDECLARED, fmt.Sprintf(
			"capability %s.%s declares no output table: the capability header must state the result "+
				"(`-> T`, or `-> void` when the operation has no result value)",
			provider, operation), n.Span())
		return types.AnyType(), true
	}
	if len(outputs) == 0 {
		// `-> void`：显式声明的空输出表 = 本次调用没有结果值。void 是**声明的
		// 事实**，不是降级；凭空造 `{result: any}` 字段才是撒谎。
		return types.VoidType(), true
	}
	return recordFromRegistryOutputs(outputs), true
}

// registryToolOutputs returns the definition's declared outputs for result
// typing. errorBody.* members ARE part of the result record: Coze exposes
// the operation error record as a read-only errorBody port (X3 实证
// dy_get_user_profile.errorBody.errorMessage 直接访问；平台独立只读端口，
// 不在 output 对象 schema 内但 ref 名 errorBody.<member> 合法）。早期 P3.5
// 决策禁止直接访问（要求走 attempt 失败绑定）与平台真实不符，已修正。
// 渲染层（toolOutputTypeInfo）仍把 errorBody 排除出 output 对象、单独
// 生成 errorBody 端口 —— 类型层与渲染层职责分离。
func registryToolOutputs(definition tool.Definition) []tool.OutputField {
	return definition.Outputs
}

// checkRegistryArguments strong-checks the capability call's params map
// against the registry definition's parameter signature. Errors are
// emitted at the offending key/value span with the operation identity in
// the message, so a
// single call can report several independent problems (unknown key, missing
// required, wrong type) in one pass.
func (p *TypePass) checkRegistryArguments(n *ast.CallExpr, definition tool.Definition) {
	args := n.Args()
	// The params map is the qualified call's sole argument p.op({params})
	// (args[0]); the scan stays defensive so a malformed callee can never
	// crash the checker.
	var ml *ast.MapLit
	for i := len(args) - 1; i >= 0; i-- {
		if m, ok := args[i].(*ast.MapLit); ok {
			ml = m
			break
		}
	}
	if ml == nil {
		return
	}
	identity := tool.SymbolKey(definition.Provider, definition.Operation)
	declared := make(map[string]tool.Parameter, len(definition.Parameters))
	for _, parameter := range definition.Parameters {
		declared[parameter.Name] = parameter
	}
	provided := make(map[string]bool, len(ml.Entries()))
	for _, entry := range ml.Entries() {
		key, ok := toolStringValue(entry.Key)
		if !ok {
			continue
		}
		provided[key] = true
		parameter, known := declared[key]
		if !known {
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
				"%s: unknown argument %q (declared: %s)",
				identity, key, declaredNames(definition.Parameters)), entry.Key.Span())
			continue
		}
		expected := registryParamTypeDescriptor(parameter.Type)
		actual := p.visitWithExpected(entry.Value, expected)
		if !types.IsAssignable(actual, expected) {
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
				"%s: argument %q: expected %s, got %s",
				identity, key, parameter.Type, actual), entry.Value.Span())
		}
	}
	for _, parameter := range definition.Parameters {
		if parameter.Required && !provided[parameter.Name] {
			p.emit(diag.ARG_COUNT, fmt.Sprintf(
				"%s: missing required argument %q", identity, parameter.Name), n.Span())
		}
	}
}

// numericStringLiteralOK 字符串数字字面量 → int/float 放行。**唯一消费者 = code
// 节点的输入声明边界**（`weakCodeInputScalar`）——那是 foreign code 的物理边界，
// 平台 JS/Python 弱类型事实由 foreign code 自己承担（本次不动，残差登记）。
//
// **2026-09-29（A 刀）：能力头文件参数边界不再放行**。原来这里还兼作
// `checkRegistryArguments` 的放行口（「反编译产物 `"1"` 传 int 参数必须接受」）——
// 那是在 raising 没有归一能力时**用放行口代替转换**（一个静默的类型洞：`"0.5"` 传
// int 也一并放行，而平台自己的 int 声明路径 `CanvasBlockInputToFieldInfo` 对
// `"0.5"` 走 `strconv.ParseInt` 是**硬失败**）。raising 现在有了常量归一
// （`NumericStringLiteralValue`）⇒ 反编译产物不再出现 `"0"` 传 int ⇒ 放行口
// 失去存在理由，删除；`"0"`/`"0.5"` 传 int 一律 `TYPE_MISMATCH`（强类型）。
func numericStringLiteralOK(v ast.Expr, expected *types.Descriptor) bool {
	if expected == nil || (expected.Kind != types.Int && expected.Kind != types.Float) {
		return false
	}
	lit, ok := v.(*ast.StringLit)
	if !ok {
		return false
	}
	return NumericStringLiteralAccepted(lit.Value())
}

// NumericStringLiteralAccepted 是「数字字符串字面量 → int/float」这条**弱类型
// 放行**判据（输入 = 字面量文本；是不是数字串）。**语义极性 = 越宽越放行**（`"0.5"`
// 也是数字串 ⇒ 也放行），故它只适用于「平台的弱类型事实由 foreign code 承担」的
// code 输入边界；能力头文件参数边界要求的是**无损可转**（`NumericStringLiteralValue`
// 是同族的另一极：越宽越**拒**），两者不可互换 —— 混用会在参数边界放行 `"0.5"`。
//
// **历史（2026-09-28 第三刀 → 2026-09-29 A 刀）**：反编译侧曾用本判据决定「要不要
// 补显式转换」；第三刀换成常量归一（`NumericStringLiteralValue`）后，本判据在
// 参数边界已无消费者（只剩 `weakCodeInputScalar`）。
func NumericStringLiteralAccepted(literal string) bool {
	if literal == "" {
		return false
	}
	_, err := strconv.ParseFloat(literal, 64)
	return err == nil
}

// NumericStringLiteralValue 是「数字串字面量 → 目标数值类型的**无损**规范值」这条
// 规则的**唯一实现**（`kind ∈ {"int","float"}`，与反编译侧 `declaredParamKind` /
// 正编译侧 `registryParamTypeDescriptor` 同一词汇）。
//
// 无损才转：
//   - int 位：文本的数值必须**恰好是整数**且在 int64 范围内
//     （`"0"`→0、`"0.0"`→0、`"-3"`→-3、`"007"`→7）；
//   - float 位：文本必须可解析且有限（`"0.5"`→0.5、`"1e3"`→1000）。
//
// 有损（`"0.5"` 进 int 位）/ 非数字（`"abc"`）/ 不可表示（`"1e400"`、超 int64）
// ⇒ false，调用方**原样承载**（不发明值；由正编译按声明类型点名）。
//
// 两个消费者（两侧同解，各写一份必然漂移 —— 与 NumericStringLiteralAccepted 同理）：
//   - 反编译侧 `normalizeParamArg`：平台画布的**弱类型值位** `"0"` 落在声明 int 的
//     参数上 ⇒ 发字面量 `0`。依据 = 平台 runtime 自己在节点输入边界就是这么归一的
//     （coze-studio `internal/nodes/convert.go` `convertToInt64`：string 走
//     `strconv.ParseInt`，故 `"0"` 与 JSON 数字 `0` 在运行期**完全等价**）；
//   - 正编译侧常量折叠：内建 `int("0")` / `float("0")` 编译期算完 ⇒ 不物化 shim 载体。
func NumericStringLiteralValue(literal, kind string) (any, bool) {
	text := strings.TrimSpace(literal)
	if text == "" {
		return nil, false
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return nil, false
	}
	if kind == "int" {
		// 2^63 在 float64 上可精确表示 ⇒ 上下界比较无浮点误差。
		const twoTo63 = 9223372036854775808.0
		if math.Trunc(f) != f || f < -twoTo63 || f >= twoTo63 {
			return nil, false
		}
		return int64(f), true
	}
	return f, true
}

// registryParamTypeDescriptor maps a semantic registry parameter type onto
// a type descriptor for argument checking. Unlike registryTypeDescriptor
// (which drives output schemas and maps "any"/"dict" onto a map so backends
// get a JSON shape), a parameter declared `any` accepts every value, so the
// strong-check path maps it onto the true any descriptor. All other names
// delegate to the shared table (string/int/float/bool/array<...>).
func registryParamTypeDescriptor(name string) *types.Descriptor {
	if name == "any" || name == "" {
		return types.AnyType()
	}
	// 参数侧期望: array<any> / array<dict> 声明的是"任意数组"，必须宽松
	// 接受 array<string> 等具体实参（Z34: str_to_list 输出接 na2
	// image_urls）。输出侧 registryTypeDescriptor 把同一名字解析为
	// array<map<string,any>>（元素可字段访问 — X2_W05 aweme.awemeLink），
	// 两处语义分开。
	if name == "array<any>" || name == "array<dict>" {
		return types.ArrayType(types.AnyType())
	}
	return registryTypeDescriptor(name)
}

func declaredNames(parameters []tool.Parameter) string {
	names := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		names = append(names, parameter.Name)
	}
	return strings.Join(names, ", ")
}

func toolStringValue(expr ast.Expr) (string, bool) {
	// 工具调用/配置的**键位**取值：静态键 = 标识符名 或 字符串值（唯一判据
	// ast.StaticMapKey，2026-09-14）。只认字符串字面量会让 `{ html: q }` 这类
	// 合法写法整条跳过校验，进而误报 ARG_COUNT: missing required argument。
	return ast.StaticMapKey(expr)
}

// visitExtractionMetadata mirrors _visit_extraction_metadata
// (type_pass.py:1252-1267). Dead in Go until Task 25 registers
// extract/classify (see type_ops.go); kept as the faithful branch so the
// call path needs no re-port.
func (p *TypePass) visitExtractionMetadata(n *ast.MapLit) {
	metadataType := types.MapType(types.StringType(), types.AnyType())
	if id, ok := p.nodeID(n); ok {
		p.ctx.ExprTypes[id] = metadataType
	}
	for _, entry := range n.Entries() {
		keyType := types.AnyType()
		switch entry.Key.(type) {
		case *ast.IdentExpr, *ast.StringLit:
			keyType = types.StringType()
		}
		if id, ok := p.nodeID(entry.Key); ok {
			p.ctx.ExprTypes[id] = keyType
		}
		if ml, ok := entry.Value.(*ast.MapLit); ok {
			p.visitExtractionMetadata(ml)
		} else if entry.Value != nil {
			p.visit(entry.Value)
		}
	}
}

// visitOperationPolicyExpr mirrors visit_operation_policy_expr
// (type_pass.py:1268-1344).
func (p *TypePass) visitOperationPolicyExpr(n *ast.OperationPolicyExpr) *types.Descriptor {
	resultType := p.visit(n.Operation())
	spec := operationSpec(n.Operation(), p.ctx)
	if spec == nil {
		p.emit(diag.OPERATION_POLICY,
			"Policies may be attached only to a registered workflow operation", n.Span())
		return resultType
	}
	seen := map[string]bool{}
	for _, policy := range n.Policies() {
		key := policyKind(policy)
		if seen[key] {
			p.emit(diag.OPERATION_POLICY, fmt.Sprintf("Duplicate %s policy", key), policy.Span())
			continue
		}
		seen[key] = true
		switch pol := policy.(type) {
		case *ast.RetryPolicy:
			if !spec.Policies.Retry {
				p.unsupportedPolicy(spec.Name, "retry", policy)
			} else if pol.MaxRetries() <= 0 || pol.IntervalMs() <= 0 {
				p.emit(diag.OPERATION_POLICY, "Retry max and interval must be positive", policy.Span())
			}
		case *ast.TimeoutPolicy:
			if !spec.Policies.Timeout {
				p.unsupportedPolicy(spec.Name, "timeout", policy)
			} else if pol.TimeoutMs() <= 0 || pol.TimeoutMs()%1000 != 0 {
				p.emit(diag.OPERATION_POLICY, "Timeout must be a positive whole number of seconds", policy.Span())
			}
		case *ast.DefaultPolicy:
			if !spec.Policies.DefaultValue {
				p.unsupportedPolicy(spec.Name, "default", policy)
			}
			ml, ok := pol.Value().(*ast.MapLit)
			if !ok {
				p.emit(diag.OPERATION_POLICY, "Default fallback must be a static record literal", policy.Span())
				continue
			}
			before := len(p.ctx.Diagnostics)
			fallbackType := p.visitWithExpected(ml, resultType)
			policyErrors := false
			for _, d := range p.ctx.Diagnostics[before:] {
				if d.Severity == diag.Error {
					policyErrors = true
					break
				}
			}
			if policyErrors {
				// 内层已经给出了**可操作**的定位（缺哪些字段：「Record literal is
				// missing fields: …」/ 哪个字段类型不符：「Record field 'x' expected
				// …, got …」）—— 保留它们，**不要**回滚。
				//
				// 旧行为（F1-H1）：这里把内层诊断全部回滚，再补一条泛化消息
				// 「Default fallback must be <T>, got <T>」。但 `visitWithExpected`
				// 对 **record-期望的字面量**返回的是**期望类型本身**（见本函数
				// record 分支的 `return expected`）⇒ `fallbackType == resultType`
				// ⇒ 泛化消息两侧恒相同（实测：都是完整记录类型），作者只看到
				// 「两边一样」，无法据此定位缺哪个字段。
				continue
			}
			if !types.IsAssignable(fallbackType, resultType) {
				p.emit(diag.OPERATION_POLICY, fmt.Sprintf(
					"Default fallback must be %s, got %s", resultType, fallbackType), policy.Span())
			}
		}
	}
	return resultType
}

// policyKind mirrors `policy_type.__name__.removesuffix('Policy').lower()`
// (type_pass.py:1295): retry / timeout / default.
func policyKind(policy ast.OperationPolicy) string {
	switch policy.(type) {
	case *ast.RetryPolicy:
		return "retry"
	case *ast.TimeoutPolicy:
		return "timeout"
	case *ast.DefaultPolicy:
		return "default"
	}
	return "policy"
}

// unsupportedPolicy mirrors _unsupported_policy (type_pass.py:1345-1358).
func (p *TypePass) unsupportedPolicy(operation, policy string, node ast.Node) {
	p.emit(diag.OPERATION_POLICY,
		fmt.Sprintf("Operation '%s' does not support %s", operation, policy), node.Span())
}

// calculationCollectionName mirrors _calculation_collection_name
// (type_pass.py:1359-1367).
func (p *TypePass) calculationCollectionName(n *ast.CallExpr) string {
	ident, ok := n.Func().(*ast.IdentExpr)
	if !ok {
		return ""
	}
	symID, found := p.referenceSymbol(ident)
	if !found {
		return ""
	}
	decl, _ := p.snap.Declarations().Get(symID)
	if decl.Kind != binding.SymbolBuiltinFunction {
		return ""
	}
	if _, ok := collectionBuiltinNames[ident.Name()]; !ok {
		return ""
	}
	return ident.Name()
}

var collectionBuiltinNames = map[string]bool{
	"map": true, "filter": true, "flatMap": true, "sort": true, "sort_by": true, "take": true,
}

// visitCollectionCall mirrors _visit_collection_call (type_pass.py:1369-1451).
func (p *TypePass) visitCollectionCall(n *ast.CallExpr, name string) *types.Descriptor {
	if name == "sort" || name == "sort_by" || name == "take" {
		return p.visitListOperatorCall(n, name)
	}
	if len(n.Args()) != 2 {
		p.emit(diag.ARG_COUNT,
			fmt.Sprintf("%s expects 2 arguments, got %d", name, len(n.Args())), n.Span())
		for _, argument := range n.Args() {
			p.visit(argument)
		}
		return types.AnyType()
	}
	collectionType := p.visit(n.Args()[0])
	if collectionType.Kind != types.Array {
		if collectionType.Kind == types.Any {
			p.emit(diag.ANY_NOT_NARROWED,
				fmt.Sprintf("%s requires a proven array, not any", name), n.Args()[0].Span())
		} else {
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
				"%s expects an array as its first argument, got %s", name, collectionType), n.Args()[0].Span())
		}
		p.visit(n.Args()[1])
		return types.AnyType()
	}
	lambdaExpr, ok := n.Args()[1].(*ast.LambdaExpr)
	if !ok || len(lambdaExpr.Params()) != 1 {
		p.emit(diag.TYPE_MISMATCH,
			fmt.Sprintf("%s expects a single-parameter calculation lambda", name), n.Args()[1].Span())
		p.visit(n.Args()[1])
		return types.AnyType()
	}
	p.setLambdaParameterType(lambdaExpr, collectionType.Elem)
	lambdaType := p.visit(lambdaExpr)
	if lambdaType.Kind != types.Function {
		return types.AnyType()
	}
	bodyType := lambdaType.Result
	switch name {
	case "filter":
		if bodyType.Kind != types.Bool {
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
				"filter predicate must return bool, got %s", bodyType), spanOf(lambdaExpr.Body()))
		}
		return collectionType
	case "flatMap":
		if bodyType.Kind != types.Array {
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
				"flatMap lambda must return an array, got %s", bodyType), spanOf(lambdaExpr.Body()))
			return types.AnyType()
		}
		return types.ArrayType(bodyType.Elem)
	}
	return types.ArrayType(bodyType)
}

// visitListOperatorCall mirrors _visit_list_operator_call (type_pass.py:1452-1538).
func (p *TypePass) visitListOperatorCall(n *ast.CallExpr, name string) *types.Descriptor {
	expectedCount := 2
	if name == "sort_by" {
		expectedCount = 3
	}
	if len(n.Args()) != expectedCount {
		p.emit(diag.ARG_COUNT,
			fmt.Sprintf("%s expects %d arguments, got %d", name, expectedCount, len(n.Args())), n.Span())
		for _, argument := range n.Args() {
			p.visit(argument)
		}
		return types.AnyType()
	}
	collectionType := p.visit(n.Args()[0])
	if collectionType.Kind != types.Array {
		if collectionType.Kind == types.Any {
			p.emit(diag.ANY_NOT_NARROWED,
				fmt.Sprintf("%s requires a proven array, not any", name), n.Args()[0].Span())
		} else {
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
				"%s expects an array as its first argument, got %s", name, collectionType), n.Args()[0].Span())
		}
		for _, argument := range n.Args()[1:] {
			p.visit(argument)
		}
		return types.AnyType()
	}
	elementType := collectionType.Elem
	if name == "sort" && !isScalarKind(elementType.Kind) {
		p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
			"sort expects an array of scalar values, got %s", collectionType), n.Args()[0].Span())
	}
	if name == "sort_by" && elementType.Kind != types.File {
		p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
			"sort_by expects an array of files, got %s", collectionType), n.Args()[0].Span())
	}
	for _, argument := range n.Args()[1:] {
		argumentType := p.visit(argument)
		expected := types.StringType()
		expectedLabel := "string"
		if name == "take" {
			expected = types.IntType()
			expectedLabel = "int"
		}
		if !types.IsAssignable(argumentType, expected) {
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
				"%s argument must be %s, got %s", name, expectedLabel, argumentType), argument.Span())
		}
	}
	if name == "sort" || name == "sort_by" {
		order := n.Args()[len(n.Args())-1]
		if lit, ok := order.(*ast.StringLit); ok && lit.Value() != "asc" && lit.Value() != "desc" {
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
				"%s order must be 'asc' or 'desc', got %s", name, pythonStringRepr(lit.Value())), order.Span())
		}
	}
	return collectionType
}

// setLambdaParameterType mirrors _set_lambda_parameter_type
// (type_pass.py:1539-1549): PARAMETER-kind identifiers inside the lambda
// body are re-typed to the collection element type (T2 mutation surface).
func (p *TypePass) setLambdaParameterType(node *ast.LambdaExpr, parameterType *types.Descriptor) {
	for _, ident := range p.iterIdentifiers(node.Body()) {
		symID, found := p.referenceSymbol(ident)
		if !found {
			continue
		}
		decl, _ := p.snap.Declarations().Get(symID)
		if decl.Kind == binding.SymbolParameter {
			p.ctx.VarTypes[symID] = parameterType
		}
	}
}

// isEnumerateCall reports whether n is the enumerate builtin (G16): the
// generic pairing builtin whose return type depends on the argument's
// element type (array<T> → array<Enumerated<T>>), so it is special-cased
// like coalesce/collection calls instead of using the static fallback
// signature.
func (p *TypePass) isEnumerateCall(n *ast.CallExpr) bool {
	return p.resolvedBuiltinName(n) == "enumerate"
}

// resolvedBuiltinName returns the callee name when — and only when — the call
// is a direct call to a symbol declared as a **builtin function**. A name that
// merely looks like a builtin but resolves to a user function, a method call or
// a capability is deliberately not matched: the builtin-specific rules below
// must never apply to a user-defined `split`.
func (p *TypePass) resolvedBuiltinName(n *ast.CallExpr) string {
	ident, ok := n.Func().(*ast.IdentExpr)
	if !ok {
		return ""
	}
	symID, found := p.referenceSymbol(ident)
	if !found {
		return ""
	}
	decl, ok := p.snap.Declarations().Get(symID)
	if !ok || decl.Kind != binding.SymbolBuiltinFunction {
		return ""
	}
	return ident.Name()
}

// builtinSlotWidening records the few **static config slots** that accept more
// than their single declared parameter type. There is exactly one today:
// split's separator slot. The platform field (`splitParams.delimiters`) is
// itself a list, and a single delimiter is simply a one-element list on the
// wire — so the language accepts both `split(s, ",")` and
// `split(s, [",", "；"])`. The list spelling is what carries **order** when
// there is more than one delimiter (the runtime splits sequentially), which is
// why it cannot be folded into a single string. Returns nil = no widening.
func builtinSlotWidening(builtin string, index int, declared *types.Descriptor) *types.Descriptor {
	if builtin != "split" || index != 1 {
		return nil
	}
	if declared == nil || declared.Kind != types.String {
		return nil
	}
	return types.ArrayType(types.StringType())
}

// visitEnumerateCall types enumerate(arr) as array<Enumerated<T>> where
// Enumerated<T> = { index: integer; value: T; } and T is the argument's
// element type. The paired view gives loop bodies numbered access
// (pair.index) plus the original element (pair.value) — design A.
func (p *TypePass) visitEnumerateCall(n *ast.CallExpr) *types.Descriptor {
	if len(n.Args()) != 1 {
		p.emit(diag.ARG_COUNT, fmt.Sprintf(
			"enumerate expects 1 argument, got %d", len(n.Args())), n.Span())
		for _, a := range n.Args() {
			p.visit(a)
		}
		return types.AnyType()
	}
	arrType := p.visit(n.Args()[0])
	if arrType.Kind != types.Array {
		if arrType.Kind == types.Any {
			p.emit(diag.ANY_NOT_NARROWED,
				"enumerate requires a proven array, not any", n.Args()[0].Span())
		} else {
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
				"enumerate expects an array as its argument, got %s", arrType), n.Args()[0].Span())
		}
		return types.AnyType()
	}
	// Enumerated<T> = { index: integer; value: T; }
	enumerated := types.RecordType("Enumerated", []types.RecordField{
		{Name: "index", Type: types.IntType()},
		{Name: "value", Type: arrType.Elem},
	})
	return types.ArrayType(enumerated)
}

// isJSONParseCall reports whether n is a json.parse call (G11 schema-driven
// JSON deserialization). The call shape is json.parse<T>(input).
func (p *TypePass) isJSONParseCall(n *ast.CallExpr) bool {
	fa, ok := n.Func().(*ast.FieldAccessExpr)
	if !ok || fa.FieldName() != "parse" {
		return false
	}
	obj, ok := fa.Obj().(*ast.IdentExpr)
	return ok && obj.Name() == "json"
}

// visitJSONParseCall types json.parse<T>(input): the schema type argument T
// (a named record) is the call's result type, so field access on the parsed
// value resolves against a proven record shape (the ANY_NOT_NARROWED gate
// stays intact). Mirrors BuildExtractionContract's schema resolution; the
// input argument must be a string.
func (p *TypePass) visitJSONParseCall(n *ast.CallExpr) *types.Descriptor {
	for _, a := range n.Args() {
		p.visit(a)
	}
	if len(n.TypeArgs()) != 1 {
		p.emit(diag.ARG_COUNT, fmt.Sprintf(
			"json.parse expects exactly one schema type argument, got %d", len(n.TypeArgs())), n.Span())
		return types.AnyType()
	}
	// The schema type argument is either a named record
	// (json.parse<DocResult>) or a top-level array of scalar/record
	// elements (json.parse<array<string>>, G12): the whole parsed JSON
	// value is the array.
	var result *types.Descriptor
	switch targ := n.TypeArgs()[0].(type) {
	case *ast.PrimTypeRef:
		if targ.Name() != "object" {
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
				"json.parse schema '%s' must be a named record, array<...>, or object", targ.Name()), targ.Span())
			return types.AnyType()
		}
		// object is ncoda's existing open-object type: a proven JSON object
		// whose keys and value shapes are not statically committed.  It is an
		// explicit schema, not an unknown scalar fallback.
		result = types.ObjectType()
	case *ast.NamedTypeRef:
		resolved := p.ctx.NamedTypes[targ.Name()]
		if resolved == nil {
			p.emit(diag.UNKNOWN_TYPE, fmt.Sprintf(
				"unknown json.parse schema '%s'", targ.Name()), targ.Span())
			return types.AnyType()
		}
		if resolved.Kind != types.Record {
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
				"json.parse schema '%s' must be a record", targ.Name()), targ.Span())
			return types.AnyType()
		}
		result = resolved
	case *ast.ArrayTypeRef:
		elem := p.typeRefToDescriptor(targ.Element())
		switch elem.Kind {
		case types.String, types.Int, types.Float, types.Bool, types.Record:
			result = types.ArrayType(elem)
			result.JSONParseArray = true
		default:
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
				"json.parse array element must be a scalar or record, got %s", elem), targ.Element().Span())
			return types.AnyType()
		}
	default:
		p.emit(diag.TYPE_MISMATCH,
			"json.parse schema must be a named record type or array<...>", n.TypeArgs()[0].Span())
		return types.AnyType()
	}
	if len(n.Args()) != 1 {
		p.emit(diag.ARG_COUNT, fmt.Sprintf(
			"json.parse expects 1 argument, got %d", len(n.Args())), n.Span())
		return types.AnyType()
	}
	argType := p.visit(n.Args()[0])
	if !types.IsAssignable(argType, types.StringType()) {
		p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
			"json.parse input must be string, got %s", argType), n.Args()[0].Span())
	}
	return result
}

// ── unary / binary / ternary / lambda (type_pass.py:1591-1800) ─────────

func (p *TypePass) visitUnaryExpr(n *ast.UnaryExpr) *types.Descriptor {
	operandType := p.visit(n.Operand())
	if n.Op() == "!" {
		switch operandType.Kind {
		case types.Any:
			p.emit(diag.ANY_NOT_NARROWED,
				"Logical '!' requires a proven bool, not any", spanOf(n.Operand()))
		case types.Optional:
			p.emit(diag.OPTIONAL_REQUIRED, "Logical '!' requires a non-optional bool", spanOf(n.Operand()))
		default:
			if operandType.Kind != types.Bool {
				p.emit(diag.BINARY_TYPE, fmt.Sprintf(
					"Logical '!' requires bool, got %s", operandType), spanOf(n.Operand()))
			}
		}
		return types.BoolType()
	}
	if n.Op() == "-" {
		if operandType.Kind == types.Int || operandType.Kind == types.Float {
			return operandType
		}
		if operandType.Kind == types.Any {
			p.emit(diag.ANY_NOT_NARROWED,
				"Numeric negation requires a proven numeric type, not any", spanOf(n.Operand()))
			return types.AnyType()
		}
		p.emit(diag.BINARY_TYPE, fmt.Sprintf("Cannot negate %s", operandType), n.Span())
	}
	return types.AnyType()
}

func (p *TypePass) visitBinaryExpr(n *ast.BinaryExpr) *types.Descriptor {
	leftType := p.visit(n.Left())
	rightType := p.visit(n.Right())
	if leftType.Kind == types.Any || rightType.Kind == types.Any {
		if n.Op() == "??" {
			// route 1a R4：first-not-null。any 侧放行（平台字段契约具体
			// 类型但运行时可能缺失 → 自动视为可空，?? fallback = 兜底；
			// 旧 || 的 any→fallback 适配随 || 收紧迁至 ??，记录在案）。
			if leftType.Kind == types.Any {
				return rightType
			}
			return leftType
		}
		p.emit(diag.ANY_NOT_NARROWED, fmt.Sprintf(
			"Operator '%s' requires proven operand types, not any", n.Op()), n.Span())
		if n.Op() == "&&" || n.Op() == "==" || n.Op() == "!=" ||
			n.Op() == "<" || n.Op() == "<=" || n.Op() == ">" || n.Op() == ">=" || n.Op() == "in" {
			return types.BoolType()
		}
		return types.AnyType()
	}
	switch n.Op() {
	case "&&":
		// 逻辑与：严格 bool 操作数（&& 无语义：值合并不适用）。
		for _, operand := range []ast.Expr{n.Left(), n.Right()} {
			operandType := leftType
			if operand == n.Right() {
				operandType = rightType
			}
			switch operandType.Kind {
			case types.Any:
				p.emit(diag.ANY_NOT_NARROWED, fmt.Sprintf(
					"Logical '%s' requires proven bool operands, not any", n.Op()), spanOf(operand))
			case types.Optional:
				p.emit(diag.OPTIONAL_REQUIRED, fmt.Sprintf(
					"Logical '%s' requires non-optional bool operands", n.Op()), spanOf(operand))
			default:
				if operandType.Kind != types.Bool {
					p.emit(diag.BINARY_TYPE, fmt.Sprintf(
						"Logical '%s' requires bool operands, got %s", n.Op(), operandType), spanOf(operand))
				}
			}
		}
		return types.BoolType()
	case "??":
		// route 1a R4：first-not-null 值选择（平台 merge 精确孪生）。
		//  左 Null → 右；Any 由顶部放行（平台字段运行时缺失适配）；
		//  左 Optional<T>：右 T → 收敛 T（null 消除）；右 Optional → 仍 Optional；
		//  左非 Optional → 恒取左（REDUNDANT_COALESCE 警告，裁决 3）。
		// 类型检查右可赋值左（或左 Elem），对齐旧 coalesce first_not_null 推断。
		switch leftType.Kind {
		case types.Null:
			return rightType
		case types.Optional:
			elem := leftType.Elem
			if elem == nil {
				return rightType
			}
			if rightType.Kind == types.Optional {
				if !types.IsAssignable(rightType.Elem, elem) {
					p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
						"'??' fallback must be %s, got %s", elem, rightType), n.Span())
				}
				return leftType
			}
			if !types.IsAssignable(rightType, elem) {
				p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
					"'??' fallback must be %s, got %s", elem, rightType), n.Span())
			}
			return elem
		}
		// 左非 Optional（不含 Null/Any）→ ?? 冗余：右侧永不取。
		// 右操作数因此不参与结果类型约束；`x ?? y` 的既定规则是
		// REDUNDANT_COALESCE 警告而非错误，即使 y 是 Optional 或形状不同。
		p.emitWarning(diag.REDUNDANT_COALESCE, fmt.Sprintf(
			"'??' left side is never null (right operand never taken): %s ?? %s",
			leftType, rightType), n.Span())
		return leftType
	case "||":
		// route 1a R1：纯布尔逻辑或（与 && 对称，全称规则无类型分叉）。
		// 值兜底改用 ??（first-not-null）；truthy 值合并已退役（09-05
		// 裁决，推翻 09-01 Python-or 语义）。操作数须 bool。
		for _, operand := range []ast.Expr{n.Left(), n.Right()} {
			operandType := leftType
			if operand == n.Right() {
				operandType = rightType
			}
			switch operandType.Kind {
			case types.Any:
				p.emit(diag.ANY_NOT_NARROWED, fmt.Sprintf(
					"Logical '%s' requires proven bool operands, not any", n.Op()), spanOf(operand))
			case types.Optional:
				p.emit(diag.OPTIONAL_REQUIRED, fmt.Sprintf(
					"Logical '%s' requires non-optional bool operands", n.Op()), spanOf(operand))
			default:
				if operandType.Kind != types.Bool {
					p.emit(diag.BINARY_TYPE, fmt.Sprintf(
						"Logical '%s' requires bool operands, got %s (value fallback: use '??')",
						n.Op(), operandType), spanOf(operand))
				}
			}
		}
		return types.BoolType()
	case "==", "!=":
		if !types.IsComparable(leftType, rightType) {
			// 跨类型相等比较降级为警告（平台接受真实语料：X211
			// bool==string、H36 record!=string 均为可运行的商业工作流，
			// 原样渲染平台语义一致，roundtrip L0 平台接受要求放行；
			// 顺序比较 <,<=,>,>= 保持严格 BINARY_TYPE）。ana2 参考
			// 严格拒绝，此处为语料实证驱动的有意放宽（工作契约：
			// 平台接受 > 编译器洁癖）。
			p.emitWarning(diag.CROSS_TYPE_EQUALITY, fmt.Sprintf(
				"Cannot compare %s and %s with '%s'", leftType, rightType, n.Op()), n.Span())
		}
		return types.BoolType()
	case "in":
		if rightType.Kind != types.Array {
			p.emit(diag.BINARY_TYPE, fmt.Sprintf(
				"Right operand of 'in' must be an array, got %s", rightType), spanOf(n.Right()))
		} else if containsEnumType(leftType) || containsEnumType(rightType.Elem) {
			p.emit(diag.BINARY_TYPE,
				"Enum values support only same-enum '==' and '!=' comparisons", n.Span())
		} else if !types.IsComparable(leftType, rightType.Elem) {
			p.emit(diag.BINARY_TYPE, fmt.Sprintf(
				"Cannot test %s membership in %s", leftType, rightType), n.Span())
		}
		return types.BoolType()
	case "<", "<=", ">", ">=":
		if !types.IsOrderable(leftType, rightType) {
			p.emit(diag.BINARY_TYPE, fmt.Sprintf(
				"Cannot order %s and %s with '%s'", leftType, rightType, n.Op()), n.Span())
		}
		return types.BoolType()
	case "+", "-", "*", "/", "%":
		var optionalOperand ast.Expr
		for _, operand := range []struct {
			node ast.Expr
			typ  *types.Descriptor
		}{{n.Left(), leftType}, {n.Right(), rightType}} {
			if operand.typ.Kind == types.Optional {
				optionalOperand = operand.node
				break
			}
		}
		if optionalOperand != nil {
			p.emit(diag.OPTIONAL_REQUIRED, fmt.Sprintf(
				"Operator '%s' requires non-optional operands", n.Op()), optionalOperand.Span())
			return types.AnyType()
		}
		ct := types.CommonType(leftType, rightType, n.Op())
		if ct == nil {
			p.emit(diag.BINARY_TYPE, fmt.Sprintf(
				"Cannot apply '%s' to %s and %s", n.Op(), leftType, rightType), n.Span())
			return types.AnyType()
		}
		return ct
	}
	return types.AnyType()
}

func (p *TypePass) visitTernaryExpr(n *ast.TernaryExpr) *types.Descriptor {
	conditionType := p.visit(n.Condition())
	p.validateConditionType(n.Condition(), conditionType)
	thenType := p.visit(n.ThenVal())
	elseType := p.visit(n.ElseVal())
	joined := types.JoinAggregateTypes(thenType, elseType)
	if joined == nil {
		p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
			"Ternary branches have incompatible types %s and %s", thenType, elseType), n.Span())
		return types.AnyType()
	}
	return joined
}

// visitLambdaExpr mirrors visit_lambda_expr (type_pass.py:1759-1800):
// parameter types come from the first body identifier's current symbol
// type; the body is inferred afterwards.
func (p *TypePass) visitLambdaExpr(n *ast.LambdaExpr) *types.Descriptor {
	parameterTypes := []types.Descriptor{}
	for _, name := range n.Params() {
		parameterType := types.AnyType()
		for _, ident := range p.iterIdentifiers(n.Body()) {
			if ident.Name() != name {
				continue
			}
			if symID, found := p.referenceSymbol(ident); found {
				parameterType = p.symbolType(symID)
				break
			}
		}
		parameterTypes = append(parameterTypes, *parameterType)
	}
	bodyType := types.AnyType()
	if n.Body() != nil {
		bodyType = p.visit(n.Body())
	}
	return types.FunctionType(parameterTypes, bodyType)
}

// ── foreign code (type_pass.py:974-1041) ───────────────────────────────

// visitForeignCodeExpr mirrors visit_foreign_code_expr (type_pass.py:974-1041):
// type a Code expression solely from its declared boundary contract.
func (p *TypePass) visitForeignCodeExpr(n *ast.ForeignCodeExpr) *types.Descriptor {
	for _, input := range n.Inputs() {
		expected := p.typeRefToDescriptor(input.TypeRef())
		if !isCodeInputPortType(expected) {
			p.emit(diag.CODE_OUTPUT_INVALID, fmt.Sprintf(
				"Code input '%s' uses unsupported type %s", input.Name(), expected), input.Span())
		}
		actual := p.visitWithExpected(input.Value(), expected)
		if !isCodeInputAssignable(actual, expected) &&
			!weakCodeInputScalar(input.Value(), actual, expected) &&
			!weakForeignCodeSelector(input.Value(), actual, expected) {
			p.emit(diag.CODE_INPUT_TYPE, fmt.Sprintf(
				"Code input '%s' expected %s, got %s", input.Name(), expected, actual), input.Span())
		}
	}
	p.visit(n.Source())
	switch out := n.Output().(type) {
	case *ast.CodeDirectOutput:
		result := p.typeRefToDescriptor(out.TypeRef())
		p.validateCodeOutputType("result", result, out.Span())
		return result
	case *ast.CodeStructuralOutput:
		fields := []types.RecordField{}
		for _, outputField := range out.Fields() {
			fieldType := p.typeRefToDescriptor(outputField.TypeRef())
			p.validateCodeOutputType(outputField.Name(), fieldType, outputField.Span())
			fields = append(fields, types.RecordField{Name: outputField.Name(), Type: fieldType})
		}
		return types.RecordType(codeResultIdentity(n), fields)
	}
	return types.AnyType()
}

// isCodeInputPortType allows the language's existing optional wrapper at a
// foreign-code input boundary. Target code nodes still receive the underlying
// physical port type; nullability is a semantic property of the bound value.
// Outputs remain governed by isCodePortType because the target code-port
// schemas do not have a portable optional-output declaration.
func isCodeInputPortType(t *types.Descriptor) bool {
	if t != nil && t.Kind == types.Optional {
		return isCodePortType(t.Elem)
	}
	return isCodePortType(t)
}

// isCodeInputAssignable 判定 foreign code 输入赋值兼容：标准 IsAssignable
// 之外，放宽 JS 弱类型（coze code 节点运行时弱类型 —— capcut duration
// float 传入 code int 参数，S5 实证；X16 反编译 map 字面量传入 record
// 声明，X203 等 file 数组传入 string 数组）。int→float 加宽已有，这里补
// float→int 收窄、number 互认、map→record 结构与 file→string；数组元素
// 递归（覆盖数字对/file/map-record 组合）。
func isCodeInputAssignable(src, dst *types.Descriptor) bool {
	if types.IsAssignable(src, dst) {
		return true
	}
	src = types.UnwrapOptional(src)
	dst = types.UnwrapOptional(dst)
	if src == nil || dst == nil {
		return false
	}
	if src.Kind == types.Any {
		// foreign code JS 弱类型：any 输入直接放行（运行时动态检查；
		// H35 input string-vs-any、X70 detail array<string>-vs-any）。
		return true
	}
	if src.Kind == types.Array && dst.Kind == types.Array {
		if src.Elem == nil || dst.Elem == nil {
			return false
		}
		if isCodeInputAssignable(src.Elem, dst.Elem) {
			return true
		}
		// 元素级 JS 弱类型：string↔number 互认（聚合端口元素运行时确定、
		// coze 平台接受；X151 statusList = for 聚合 array<int> vs 声明
		// array<string>）。顶层字面量仍由 weakStringNumber 把关（探针
		// `int n = "not-int"` 报错）。
		return numericStringScalarPair(src.Elem, dst.Elem)
	}
	if src.Kind == types.Array && dst.Kind != types.Any {
		// 聚合端口 → 标量声明：coze 平台对 code 输入不做类型检查，循环聚合
		// 数组（agg.<port>）直接传 code（X212/S2：int pre_time = agg.pre_time，
		// 原始 schema 声明 int 但运行时是数组）。canvas 端口类型与原始一致
		// （int 声明 + 数组连线），放行前提 = 数组元素可赋给声明类型
		// （string x = array<int> 仍报错）。
		if src.Elem != nil && isCodeInputAssignable(src.Elem, dst) {
			return true
		}
	}
	if src.Kind == types.Map && dst.Kind == types.Record {
		// 反编译产物：coze 节点对象输出以带引号键 dict（map<string,any>）
		// 渲染，而 code 输入声明为 record（X16 RT1）。JS 弱类型下两者同形
		// —— record 每个字段都能在 map 值类型下可分配即放行。
		if src.Val == nil {
			return false
		}
		for _, f := range dst.Fields {
			if !isCodeInputAssignable(src.Val, f.Type) {
				return false
			}
		}
		return true
	}
	if src.Kind == types.Record && dst.Kind == types.Record {
		// 字段子集兼容：dst 每个字段在 src 中存在且可分配（coze 节点输出
		// 字段集与 code 输入声明有差异，X19 coverOCR 缺 lang——JS 弱类型
		// 下 extra 字段运行时可忽略）。src 多出的字段不追究。
		srcField := map[string]*types.Descriptor{}
		for i := range src.Fields {
			srcField[src.Fields[i].Name] = src.Fields[i].Type
		}
		for _, f := range dst.Fields {
			sf, ok := srcField[f.Name]
			if !ok {
				// 字段缺失放行：coze code 输入 JS 弱类型，缺字段 = undefined，
				// 平台接受（X19 coverOCR 声明含 lang，实际 OCR 输出无 lang
				// —— 原工作流即如此配置，roundtrip 保持原样，不判错）。
				continue
			}
			// 递归 isCodeInputAssignable：嵌套 record/array 差异（X19
			// words 内层 record 缺 lang）也需要字段子集兼容。
			if !isCodeInputAssignable(sf, f.Type) {
				return false
			}
		}
		return true
	}
	return isNumericPair(src, dst) || isStringFilePair(src, dst)
}

// numericStringScalarPair 标量 string↔number 互认（JS 弱类型，coze
// 平台接受数字字符串参数）。仅标量（不含 array/record/map 嵌套）。
func numericStringScalarPair(src, dst *types.Descriptor) bool {
	if src == nil || dst == nil {
		return false
	}
	isNum := func(k types.DSLType) bool { return k == types.Int || k == types.Float }
	sn := (src.Kind == types.String && isNum(dst.Kind)) || (dst.Kind == types.String && isNum(src.Kind))
	return sn
}

// weakStringNumber JS 弱类型 string→number 放行（code 输入）：字符串
// 字面量必须可解析为数字（探针 `int n = "not-int"` 仍报错）；非字面量
// （变量/字段/调用结果，如 X15 `int input = count`）运行时才确定，coze
// 平台 JS 自动转换、接受 → 放行。
func weakStringNumber(v ast.Expr, actual, expected *types.Descriptor) bool {
	actual = types.UnwrapOptional(actual)
	if actual == nil || actual.Kind != types.String {
		return false
	}
	if expected == nil || (expected.Kind != types.Int && expected.Kind != types.Float) {
		return false
	}
	if _, isLit := v.(*ast.StringLit); isLit {
		return numericStringLiteralOK(v, expected)
	}
	return true
}

// weakCodeInputScalar mirrors the coercion boundary of Coze code nodes for
// both directions of a string/number mismatch.  The source is foreign code:
// its declared parameter type remains the contract, while the platform's JS
// and Python shims commonly receive a numeric value through a string-typed
// parameter (or the reverse).  Do not weaken the language for an explicit
// non-numeric string literal; that remains a real authoring error.
func weakCodeInputScalar(v ast.Expr, actual, expected *types.Descriptor) bool {
	actual = types.UnwrapOptional(actual)
	expected = types.UnwrapOptional(expected)
	if !numericStringScalarPair(actual, expected) {
		return false
	}
	if _, isStringLiteral := v.(*ast.StringLit); isStringLiteral {
		return weakStringNumber(v, actual, expected)
	}
	return true
}

// weakForeignCodeSelector permits a non-literal workflow value to cross the
// foreign-code ABI even when its declared port type differs from the static
// ncoda type.  This is intentionally narrower than "accept anything":
// literals retain useful authoring checks, and function/void values remain
// invalid selectors.  The foreign implementation is a black box; runtime
// and the user own the responsibility for interpreting the received value.
func weakForeignCodeSelector(v ast.Expr, actual, expected *types.Descriptor) bool {
	if actual == nil || expected == nil {
		return false
	}
	switch actual.Kind {
	case types.Function, types.Void:
		return false
	}
	switch v.(type) {
	case *ast.IdentExpr, *ast.FieldAccessExpr:
		return true
	default:
		return false
	}
}

// isNumericPair 数字标量对是否 JS 弱类型互认（int/float/number 间任意）。
func isNumericPair(src, dst *types.Descriptor) bool {
	if src == nil || dst == nil {
		return false
	}
	sn := src.Kind == types.Int || src.Kind == types.Float
	dn := dst.Kind == types.Int || dst.Kind == types.Float
	return sn && dn
}

// isStringFilePair file↔string 互认：coze 平台 file 本质是 URL 字符串
// （isCodePortType 同语义），code 输入声明 array<file> 传实际 string 数组
// 合法（X203 等 18 例 img_list/imageList/word_imgs 全谱）。
func isStringFilePair(src, dst *types.Descriptor) bool {
	if src == nil || dst == nil {
		return false
	}
	return (src.Kind == types.String && dst.Kind == types.File) ||
		(src.Kind == types.File && dst.Kind == types.String)
}

// validateCodeOutputType mirrors _validate_code_output_type (type_pass.py:1027-1041).
func (p *TypePass) validateCodeOutputType(name string, outputType *types.Descriptor, location ast.Span) {
	if !isCodePortType(outputType) {
		p.emit(diag.CODE_OUTPUT_INVALID,
			fmt.Sprintf("Code output '%s' uses unsupported type %s", name, outputType), location)
	}
}

// ── module-level helpers (type_pass.py:1802-1882) ──────────────────────

// arrayElementType mirrors _array_element_type (:1809-1816).
func arrayElementType(t *types.Descriptor) *types.Descriptor {
	if t != nil && t.Kind == types.Array && t.Elem != nil {
		return t.Elem
	}
	if t == nil {
		return types.AnyType()
	}
	return t
}

// containsEnumType mirrors _contains_enum_type (:117-133): whether a value
// comparison would reach a nominal enum.
func containsEnumType(t *types.Descriptor) bool {
	if t == nil {
		return false
	}
	if t.Kind == types.Enum {
		return true
	}
	switch t.Kind {
	case types.Array, types.Optional:
		return containsEnumType(t.Elem)
	case types.Map:
		return containsEnumType(t.Key) || containsEnumType(t.Val)
	case types.Record:
		for _, f := range t.Fields {
			if containsEnumType(f.Type) {
				return true
			}
		}
		return false
	case types.Function:
		for i := range t.Params {
			if containsEnumType(&t.Params[i]) {
				return true
			}
		}
		return containsEnumType(t.Result)
	}
	return false
}

// isValidIndexType mirrors _is_valid_index_type (:1817-1822).
func isValidIndexType(actual, expected *types.Descriptor) bool {
	if actual.Kind == types.Any {
		return true
	}
	if actual.Kind == types.Null {
		return false
	}
	return types.IsAssignable(actual, expected)
}

// codeResultIdentity mirrors _code_result_identity (:1824-1831).
func codeResultIdentity(n *ast.ForeignCodeExpr) string {
	span := n.Span()
	if span == (ast.Span{}) {
		return "<anonymous-code-result>"
	}
	filename := ""
	if span.Start.Filename != "" {
		filename = span.Start.Filename + ":"
	}
	return fmt.Sprintf("<code-result:%s%d:%d>", filename, span.Start.Line, span.Start.Column)
}

// isCodePortType mirrors _is_code_port_type (:1868-1882): whether a declared
// type has an exact Dify Code port representation.
func isCodePortType(t *types.Descriptor) bool {
	if t == nil {
		return false
	}
	// Optional is a semantic nullability wrapper, not a different physical
	// CodeRunner ABI. Unwrap it before recursively checking arrays, maps, and
	// record fields; otherwise valid T? contracts are rejected as
	// CODE_OUTPUT_INVALID at the language/platform boundary.
	if t.Kind == types.Optional {
		return isCodePortType(t.Elem)
	}
	switch t.Kind {
	case types.String, types.Int, types.Float, types.Bool:
		return true
	case types.File:
		// coze-biz code 节点接受 image/audio/video 输入（python 侧为 URL
		// 字符串）: S176 原始 code 158269 即 15 个 file 输入。file 作为
		// string 端口类型处理。
		return true
	case types.Any:
		// E5: `any` is a JSON value (the Dify codec maps it onto the string
		// code port). Real plugins return untyped records (array<any>,
		// map<string,any>); rejecting them forces every consumer through a
		// decode shim for no safety gain — code nodes already receive
		// arbitrary JSON and must handle it defensively.
		return true
	case types.Array:
		return isCodePortType(t.Elem)
	case types.Map:
		return t.Key != nil && t.Key.Kind == types.String && isCodePortType(t.Val)
	case types.Record:
		for _, f := range t.Fields {
			if !isCodePortType(f.Type) {
				return false
			}
		}
		return true
	}
	return false
}

// ── node-walking collectors (type_pass.py _owned_yields / _iter_identifiers) ─

// ownedYields mirrors _owned_yields (:1833-1851): every YieldStmt owned by
// a block, pruned at ForExpr/ParallelForExpr boundaries (Python's ForExpr
// exclusion covers the ParallelForExpr subclass).
func (p *TypePass) ownedYields(n ast.Node) []*ast.YieldStmt {
	c := &yieldCollector{}
	ast.Walk(c, n)
	return c.out
}

type yieldCollector struct{ out []*ast.YieldStmt }

func (c *yieldCollector) Visit(n ast.Node) ast.Visitor {
	switch n.(type) {
	case *ast.YieldStmt:
		c.out = append(c.out, n.(*ast.YieldStmt))
		return nil
	case *ast.ForExpr, *ast.ParallelForExpr:
		return nil
	}
	return c
}

// iterIdentifiers mirrors _iter_identifiers (:1853-1865): every IdentExpr
// in an expression subtree, in traversal order. Python's _iter_identifiers
// recurses only dataclass Expr fields (and lists/tuples), so it descends
// into a ForExpr's iterable but never into its Block body, and never into
// ParallelExpr branches (NamedParallelBranch is a ProcNode, not an Expr —
// the expression form was removed; parallel is statement-only now). The Go
// collector mirrors that pruning explicitly (ForExpr/ParallelForExpr walk
// only the iterable).
func (p *TypePass) iterIdentifiers(n ast.Node) []*ast.IdentExpr {
	c := &identCollector{}
	ast.Walk(c, n)
	return c.out
}

type identCollector struct{ out []*ast.IdentExpr }

func (c *identCollector) Visit(n ast.Node) ast.Visitor {
	switch n := n.(type) {
	case *ast.ForExpr:
		for _, src := range n.LoopSources() {
			if src != nil {
				ast.Walk(c, src)
			}
		}
		return nil
	case *ast.ParallelForExpr:
		for _, src := range n.LoopSources() {
			if src != nil {
				ast.Walk(c, src)
			}
		}
		return nil
	}
	if id, ok := n.(*ast.IdentExpr); ok {
		c.out = append(c.out, id)
	}
	return c
}

// ── small helpers ──────────────────────────────────────────────────────

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func recordHasField(d *types.Descriptor, name string) bool {
	for _, f := range d.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

func recordField(d *types.Descriptor, name string) (*types.Descriptor, bool) {
	for _, f := range d.Fields {
		if f.Name == name {
			return f.Type, true
		}
	}
	return nil, false
}

func recordFieldType(d *types.Descriptor, name string) *types.Descriptor {
	if t, ok := recordField(d, name); ok {
		return t
	}
	return types.AnyType()
}

// missingRecordFields mirrors `set(expected.params.fields) - actual_fields`,
// sorted for the diagnostic message.
func missingRecordFields(d *types.Descriptor, actual map[string]bool) []string {
	out := []string{}
	for _, f := range d.Fields {
		if !actual[f.Name] {
			out = append(out, f.Name)
		}
	}
	sort.Strings(out)
	return out
}

// isScalarKind mirrors the sort scalar check (type_pass.py:1478-1481).
func isScalarKind(k types.DSLType) bool {
	return k == types.String || k == types.Int || k == types.Float || k == types.Bool
}

// pythonStringRepr mirrors Python's repr(str) with single quotes (the
// '{order.value!r}' message interpolation).
func pythonStringRepr(s string) string {
	return "'" + s + "'"
}
