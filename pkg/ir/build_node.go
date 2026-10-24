// IR builder — 按语句/节点族拆分的文件之一（Tier-2 review #9:拆文件不拆包,
// diff 局部化）。同一包内符号共享;本文件族的归属见各函数注释。
package ir

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/pyast"
	dsltypes "github.com/nodecoda/ncoda/pkg/types"
)

// ── semantic node builders (IR payloads, resolved at build time) ─────────

// buildKnowledge maps knowledge(ids, query, opts) to a KnowledgeCall.
//
// Two slots, two rulers — they are not the same kind of slot:
//
//	· DATASET ids = **constant config slot** (see constantSlotString): the
//	  platform carries the selection as configuration, so only a literal /
//	  `const` has a carrier and anything else is a compile error.
//	· QUERY = **value slot** (see querySource): the platform carries a value
//	  (`input["Query"].(string)`), so the slot is total over string
//	  expressions — literal, reference, or materialized expression.
func (g *Builder) buildKnowledge(id string, call *ast.CallExpr, prevIDs []string, firstPort string) (Node, error) {
	kc := &KnowledgeCall{ID: id}
	if len(call.Args()) > 0 {
		// DATASET slot = constant config slot (same ruler as http() method):
		// the platform carries the selected knowledge bases as configuration
		// (datasetParam.datasetList = id list), so a runtime expression has no
		// carrier. Pre-2026-09-24 it was silently dropped, and the product
		// shipped without datasetParam at all (retrieval over nothing) while
		// the compile reported SUCCEEDED.
		raw, ok := stringValue(call.Args()[0])
		if !ok {
			if _, err := g.constantSlotString("knowledge() dataset ids", call.Args()[0]); err != nil {
				return nil, err
			}
		}
		for _, part := range strings.Split(raw, ",") {
			if p := strings.TrimSpace(part); p != "" {
				kc.DatasetNames = append(kc.DatasetNames, p)
				kc.DatasetIDs = append(kc.DatasetIDs, datasetID(p))
			}
		}
	}
	if len(call.Args()) > 1 {
		src, err := g.querySource(call.Args()[1], prevIDs, firstPort)
		if err != nil {
			return nil, err
		}
		kc.Inputs = append(kc.Inputs, src)
	}
	return kc, nil
}

// querySource renders the knowledge() QUERY slot.
//
// The platform input is a **value** input — coze-studio's runtime reads
// `input["Query"].(string)` (nodes/knowledge/knowledge_retrieve.go:198) and
// the canvas builds it from `node_inputs[]` (name = "Query"), which carries
// literals and refs alike. So the slot is total over string expressions and
// admits exactly three renderings, no fourth:
//
//	· a resolvable reference (however spelled: `q`, `${q}`, a field path)
//	  → {path: Query, ref_node};
//	· a string literal → verbatim in the input `value` (the platform's own
//	  literal carrier — `{type: literal, content: …}`);
//	· any other expression → materialized as a producer, then referenced.
//
// Pre-2026-09-24 only the first case was written and the `else` did nothing:
// `knowledge("kb1", "literal query")`, `knowledge("kb1", "a" + q)` and
// `knowledge("kb1", pick(a, b))` all shipped **without** any node_inputs —
// retrieval over nothing — while the compile reported SUCCEEDED with zero
// diagnostics (R-40). This is the same ruler as the P12 template slot
// (`httpConfigRef`): enumerate nothing, carry everything the target can hold,
// and never drop a value silently.
func (g *Builder) querySource(expr ast.Expr, prevIDs []string, firstPort string) (Source, error) {
	if ref, ok := g.resolveValueRef(expr); ok {
		return Source{Path: []string{"Query"}, BlockID: ref.nodeID, Name: ref.output}, nil
	}
	if lit, ok := expr.(*ast.StringLit); ok {
		return Source{Path: []string{"Query"}, Val: lit.Value()}, nil
	}
	ref, err := g.emitValueExpr(prevIDs, firstPort, false, expr)
	if err != nil {
		// A string expression the target cannot carry is a build error, not a
		// silently empty query (the value would otherwise disappear from the
		// product while the compile stayed green).
		return Source{}, &UnsupportedError{Construct: fmt.Sprintf(
			"knowledge() query %s is not a carryable string value (assign it to a variable first)", exprLabel(expr))}
	}
	return Source{Path: []string{"Query"}, BlockID: ref.nodeID, Name: ref.output}, nil
}

// buildSplit maps split(text, sep) / split(text, [sep₁, sep₂…]) to a
// TextProcess split. Callers only use this builder after proving the separator
// slot is static; dynamic separators must stay on the generic builtin-shim path
// instead of silently becoming "," — and a static **list** must carry every
// delimiter rather than collapsing to the first one (platform order is
// semantic: sequential split + flatten).
func (g *Builder) buildSplit(id string, call *ast.CallExpr) (Node, error) {
	if g.caps != nil && !g.caps.TextSplit {
		return nil, &UnsupportedError{Construct: "split has no native target node"}
	}
	if len(call.Args()) < 2 {
		return nil, &UnsupportedError{Construct: "split expects text and a static separator"}
	}
	seps, ok := g.staticSeparators(call.Args()[1])
	if !ok {
		return nil, &UnsupportedError{Construct: "split with dynamic separator requires a Python shim"}
	}
	tn := &TextProcess{ID: id, Method: "split", Separators: seps}
	if len(call.Args()) > 0 {
		if ref, ok := g.resolveValueRef(call.Args()[0]); ok {
			tn.Inputs = append(tn.Inputs, Source{Path: []string{"input"}, BlockID: ref.nodeID, Name: ref.output})
		} else if value, ok := stringValue(call.Args()[0]); ok {
			// TextProcessor accepts a literal input. Keep it in the IR instead
			// of emitting an input-less node whose runtime input is undefined.
			tn.Inputs = append(tn.Inputs, Source{Path: []string{"input"}, Val: value})
		} else {
			return nil, &UnsupportedError{Construct: "split input is not a resolvable string"}
		}
	}
	return tn, nil
}

// buildJoinNative builds the Coze TextProcessor concat shape for the strict
// native subset of ncoda join. TextProcessor's array-placeholder behavior is
// only equivalent for array<string>; unknown/record arrays must remain shims.
// The first operand must already be a value reference: materializing an
// arbitrary expression here would make control-flow placement ambiguous, and
// the generic shim already covers that case exactly.
func (g *Builder) buildJoinNative(id string, call *ast.CallExpr) (Node, bool) {
	if g.caps == nil || !g.caps.TextConcat || len(call.Args()) < 2 {
		return nil, false
	}
	sep, ok := g.staticString(call.Args()[1])
	if !ok {
		return nil, false
	}
	if ref, ok := g.resolveValueRef(call.Args()[0]); ok {
		t := g.portTypeOf(ref.nodeID, ref.output)
		if t == nil || t.Kind != "list" || t.Elem == nil || t.Elem.Kind != "string" {
			return nil, false
		}
		return &TextProcess{
			ID:         id,
			Method:     "concat",
			Template:   "{{String1}}",
			ConcatChar: sep,
			Inputs:     []Source{{Path: []string{"String1"}, BlockID: ref.nodeID, Name: ref.output}},
		}, true
	}
	// Literal list input: the platform's text-concat over a compile-time
	// string list carries the list VALUE directly in node_inputs, with no
	// producer node (real exports, e.g. S118 图片转数组 rewrite node: String1
	// = ["https://…", …]). Routing it through a synthetic producer would
	// emit a forbidden python shim (SHIM contract: a native text node hosts
	// this construct) ⇒ carry the literal inline instead.
	if vals, ok := g.staticStringArray(call.Args()[0]); ok {
		return &TextProcess{
			ID:         id,
			Method:     "concat",
			Template:   "{{String1}}",
			ConcatChar: sep,
			Inputs:     []Source{{Path: []string{"String1"}, Val: vals}},
		}, true
	}
	return nil, false
}

// buildTool lowers a qualified capability call p.op({params})
// (TOOL-DIRECT-CALL-FORM) to a ToolCall: ProviderID/Operation/ToolName
// come from the callee syntax, the params map is the single argument.
// The type pass enforces exactly-one-map-literal arity (ARG_COUNT) before
// IR runs; the scan below stays defensive so bad arity can never crash
// here. The legacy tool("p","op",{params}) builtin was deleted (Task 5),
// so this is the only capability-call surface. Every dispatch site gates
// on isQualifiedToolCall before calling buildTool, so the callee is
// guaranteed to be a field access on an imported namespace; the assertion
// below is that invariant (a zero-value ToolCall must never be added).
func (g *Builder) buildTool(id string, call *ast.CallExpr) (Node, error) {
	tc := &ToolCall{ID: id}
	fa := call.Func().(*ast.FieldAccessExpr)
	obj := fa.Obj().(*ast.IdentExpr)
	tc.ToolName = obj.Name()
	tc.ProviderID = obj.Name()
	tc.Operation = fa.FieldName()
	// 声明端口表：类型层已按注册表 output 表把该调用标成结果记录
	// （visitQualifiedCapabilityCall → recordFromRegistryOutputs），字段名即
	// 物理口名。这里只搬运结论，不重解析。
	tc.ResultType = g.semanticExprType(call)
	// Ordered parameters from the params map literal p.op({params}); refs
	// stay live as dataflow refs, scalars keep their literal values
	// (extractMapParams semantics).
	for _, a := range call.Args() {
		if ml, ok := a.(*ast.MapLit); ok {
			if err := g.collectToolParams(tc, ml); err != nil {
				return nil, err
			}
		}
	}
	return tc, nil
}

// buildAgent lowers a validated agent(...) call to an AgentCall node. The
// semantic contract (profile alias / model / instruction / tools) comes
// from the TargetValidationPass-published AgentCallContract; the wire
// identity (strategy / plugin / per-tool pinning) is resolved from the
// pipeline-published AgentProfiles face (the legacy lowerer resolved the
// same profile through context.py:396-404). The query argument resolves as
// a dataflow ref exactly like knowledge's query.
func (g *Builder) buildAgent(id string, call *ast.CallExpr) (Node, error) {
	ac := &AgentCall{ID: id}
	var contract *binding.AgentCallContract
	if g.prog.Snapshot != nil {
		if astID, ok := g.prog.Snapshot.NodeIDOf(call); ok {
			contract = g.prog.ValidatedAgents[astID]
		}
	}
	if contract == nil && len(g.prog.ValidatedAgents) == 1 {
		for _, only := range g.prog.ValidatedAgents {
			contract = only
		}
	}
	if contract == nil {
		return nil, &UnsupportedError{Construct: "agent call without a validated contract"}
	}
	ac.Model = contract.Model
	ac.Instruction = contract.Instruction
	if profile := g.prog.AgentProfiles[contract.ProfileAlias]; profile != nil {
		ac.StrategyName = profile.StrategyName
		ac.StrategyProviderName = profile.ProviderName
		ac.StrategyLabel = profile.StrategyLabel
		ac.PluginUniqueID = profile.PluginUniqueIdentifier
		for _, alias := range contract.Tools {
			target := profile.Tools[alias]
			if target == nil {
				continue
			}
			ac.Tools = append(ac.Tools, AgentToolWire{
				Alias:          alias,
				PluginUniqueID: target.PluginUniqueIdentifier,
				ProviderName:   target.ProviderName,
				ToolName:       target.ToolName,
				ProviderType:   target.ProviderType,
			})
		}
	}
	if len(call.Args()) > 1 {
		if ref, ok := g.resolveValueRef(call.Args()[1]); ok {
			ac.Query = &Source{Path: []string{"Query"}, BlockID: ref.nodeID, Name: ref.output}
		}
	}
	return ac, nil
}

// buildAgentRun lowers the @mode agent entry call run(query) to an
// AgentCall. The declaration comes from the top-level @agent block; the
// query argument resolves as a dataflow ref exactly like buildAgent.
func (g *Builder) buildAgentRun(id string, call *ast.CallExpr) (Node, error) {
	decl := g.prog.Program.AgentDecl()
	if decl == nil {
		return nil, &UnsupportedError{Construct: "run call without @agent declaration"}
	}
	ac := &AgentCall{ID: id, Model: decl.Model(), Instruction: decl.Instruction(), Strategy: decl.Strategy(), MaxIteration: decl.MaxIteration(), Memory: decl.Memory(), MemoryWindow: decl.MemoryWindow(), KnowledgeDatasetIDs: decl.KnowledgeDatasetIDs(), KnowledgeTopK: decl.KnowledgeTopK()}
	for _, t := range decl.Tools() {
		ac.Tools = append(ac.Tools, AgentToolWire{Alias: t, ToolName: t})
	}
	if len(call.Args()) > 0 {
		if ref, ok := g.resolveValueRef(call.Args()[0]); ok {
			ac.Query = &Source{Path: []string{"Query"}, BlockID: ref.nodeID, Name: ref.output}
		}
	}
	return ac, nil
}

// isQualifiedToolCall reports whether call is a qualified capability call
// p.op({...}) where p is an imported namespace. Method calls (x.contains)
// and record-field calls never match — x is not a namespace — and std.*
// calls never match either: `std` is never in the import-namespace set,
// and the std.v1 base is a FieldAccessExpr, not an IdentExpr.
//
// I2 invariant: the binder rejects an import whose provider name collides
// with a builtin namespace (std, json) with DUPLICATE_DEF, so for every
// program that reaches lowering the import set here is disjoint from the
// builtin namespace names by construction — the binder, type pass (explicit
// NamespaceKind discriminant) and this import-set gate agree.
func (g *Builder) isQualifiedToolCall(call *ast.CallExpr) bool {
	fa, ok := call.Func().(*ast.FieldAccessExpr)
	if !ok {
		return false
	}
	obj, ok := fa.Obj().(*ast.IdentExpr)
	if !ok {
		return false
	}
	return g.namespaces[obj.Name()]
}

// collectToolParams appends the entries of a params map literal to tc in
// source order: ident/field-access values become live dataflow refs,
// scalars keep their literal values, and structured literals (array/map)
// become value trees (e.g. effect_infos timelines arrays).
func (g *Builder) collectToolParams(tc *ToolCall, ml *ast.MapLit) error {
	for _, e := range ml.Entries() {
		key, ok := staticMapKeyName(e.Key)
		if !ok {
			return dynamicMapKeyError("tool parameter", e.Key)
		}
		switch e.Value.(type) {
		case *ast.IdentExpr, *ast.FieldAccessExpr, *ast.CallExpr:
			// CallExpr 仅 file(x) 可解析（resolveValueRef 解包）—— 工具
			// 的 file 参数（image/audio/video）传 string 值需显式标注。
			if ref, ok := g.resolveValueRef(e.Value); ok {
				tc.Params = append(tc.Params, ToolParam{Key: key, Ref: &Source{BlockID: ref.nodeID, Name: ref.output}})
				continue
			}
			// 字面量局部的引用（`let m = 0; op({p: m})`）：litVars 里就是这个
			// **常量**（`let` 不建 producer 节点），而槽位直接收字面量 ⇒ 就地内联。
			// 缺这支 = 同一意思两种命运：内联写 `{p: 0}` 编得过、经 `let` 绑一次
			// 就 `ADAPTER_ERROR: value must be a variable reference or a literal`
			// （既有缺陷：`let s = "x"; op({p: s})` 同样中招）。
			if id, ok := e.Value.(*ast.IdentExpr); ok {
				if lv, isLit := g.litVars[id.Name()]; isLit {
					tc.Params = append(tc.Params, ToolParam{Key: key, Value: litVarNative(lv)})
					continue
				}
			}
		}
		if v := literalValue(e.Value); v != nil {
			tc.Params = append(tc.Params, ToolParam{Key: key, Value: v})
			continue
		}
		if v := literalValueTree(e.Value); v != nil {
			// Structured literal (array/map) — e.g. effect_infos
			// timelines=[{"start":0,"end":2000000}].
			tc.Params = append(tc.Params, ToolParam{Key: key, Value: derefNullTree(v)})
			continue
		}
		if v := g.toolValueTree(e.Value); v != nil {
			// Structured literal with VARIABLE leaves (P1 内置节点通道):
			// map/array 值树的叶子可以是数据流 ref —— 编码为
			// {ref_node, path}（与 coze wire 的 value 形态一致）,渲染器
			// 据此生成 node_inputs 绑定。平台 builtin 的 map 值绑定与参考图等
			// 富参数均走此路径。
			tc.Params = append(tc.Params, ToolParam{Key: key, Value: derefNullTree(v)})
			continue
		}
		// Neither a resolvable ref nor a literal (structured literal with
		// variable elements, binary expressions, calls): an explicit error
		// instead of silently dropping the parameter — a dropped
		// error_message would erase error-raising semantics.
		return &UnsupportedError{Construct: fmt.Sprintf(
			"tool parameter %q: value must be a variable reference or a literal (assign complex values to a variable first)", key)}
	}
	return nil
}

// valueTreeNull 是值树里 **JSON null 字面量**的哨兵。Go 的 nil 在本函数族里
// 已经表示「不是字面量」（调用方以 nil 判定失败），所以 null 需要一个独立可
// 辨识的值；值树离开 IR（写进 ToolParam / Source.Val / DefaultField）之前由
// derefNullTree 统一归一化成 Go nil（wire 上的 null）。
//
// 语料实证：平台内置节点的参考图参数里 `{"variables": null}` 是合法成员
// （X135/X237/V028，642 份 json）——旧实现让整个值树返回 nil ⇒
// `ADAPTER_ERROR: value must be a variable reference or a literal`，把可编译的
// 语料挡在门外。（平台能力标识不写进语言内核，见 archgate 门禁；证据留在
// platforms/ 与 docs/。）
type valueTreeNull struct{}

// derefNullTree 把值树里的 null 哨兵还原为 Go nil（递归 map/array，原地改写）。
func derefNullTree(v any) any {
	switch t := v.(type) {
	case valueTreeNull:
		return nil
	case map[string]any:
		for k, item := range t {
			if _, isNull := item.(valueTreeNull); isNull {
				t[k] = nil
				continue
			}
			t[k] = derefNullTree(item)
		}
		return t
	case []any:
		for i := range t {
			t[i] = derefNullTree(t[i])
		}
		return t
	}
	return v
}

// literalValueTree converts an AST literal (string/int/float/bool, or
// nested array/map literals) into a Go value tree so tool parameters can
// carry structured literals (e.g. effect_infos timelines arrays). Returns
// nil for anything that is not a pure literal.
func literalValueTree(expr ast.Expr) any {
	switch lit := expr.(type) {
	case *ast.StringLit:
		return lit.Value()
	case *ast.IntLit:
		return lit.Value()
	case *ast.FloatLit:
		return lit.Value()
	case *ast.BoolLit:
		return lit.Value()
	case *ast.NullLit:
		// 值树里的 null：哨兵（Go nil 在本函数族里表示「不是字面量」）。
		return valueTreeNull{}
	case *ast.UnaryExpr:
		// -N / -1.5: signed numeric literals in structured value trees
		// （与 literalValue 同款；P1 内置节点 config 透传的 seed:-1 实证）。
		if lit.Op() != "-" {
			return nil
		}
		switch o := lit.Operand().(type) {
		case *ast.IntLit:
			return -o.Value()
		case *ast.FloatLit:
			return -o.Value()
		}
		return nil
	case *ast.CallExpr:
		// file(<静态字面量>)：file 是零节点类型标注 cast（string→file，运行时
		// 就是 URL 字符串，见 build.go resolveValueRef 的透传契约）——静态
		// 字面量经 cast 仍是静态字面量（`with default({ "data": file("") })`
		// 的 PT2 载荷承载）。非 file 调用不是字面量。
		if name := calleeName(lit.Func()); name == "file" && len(lit.Args()) == 1 {
			return literalValueTree(lit.Args()[0])
		}
	case *ast.ArrayLit:
		out := make([]any, 0, len(lit.Elements()))
		for _, el := range lit.Elements() {
			v := literalValueTree(el)
			if v == nil {
				return nil
			}
			out = append(out, v)
		}
		return out
	case *ast.MapLit:
		out := map[string]any{}
		for _, e := range lit.Entries() {
			key, ok := staticMapKeyName(e.Key)
			if !ok {
				return nil
			}
			v := literalValueTree(e.Value)
			if v == nil {
				return nil
			}
			out[key] = v
		}
		return out
	}
	return nil
}

// toolValueTree 是 literalValueTree 的变量叶子扩展:结构化字面量
// （map/array）的叶子可以是 IdentExpr/FieldAccessExpr —— 可解析为数据流
// ref 时编码为 wire 形态 map[string]any{"path":…, "ref_node":…}（与
// coze-biz 参数 value 形态一致）;解析失败（未定义/复合表达式）返回 nil。
// 纯字面量树仍走 literalValueTree（结果不含 ref 标记,保持现有语义）。
func (g *Builder) toolValueTree(expr ast.Expr) any {
	switch lit := expr.(type) {
	case *ast.StringLit:
		return lit.Value()
	case *ast.IntLit:
		return lit.Value()
	case *ast.FloatLit:
		return lit.Value()
	case *ast.BoolLit:
		return lit.Value()
	case *ast.NullLit:
		return valueTreeNull{}
	case *ast.UnaryExpr:
		if lit.Op() != "-" {
			return nil
		}
		switch o := lit.Operand().(type) {
		case *ast.IntLit:
			return -o.Value()
		case *ast.FloatLit:
			return -o.Value()
		}
		return nil
	case *ast.IdentExpr, *ast.FieldAccessExpr:
		if ref, ok := g.resolveValueRef(lit); ok {
			return map[string]any{"path": ref.output, "ref_node": ref.nodeID}
		}
		return nil
	case *ast.ArrayLit:
		// 数组元素**本身是裸引用**（数组作为身份的直接父节点）不是平台形态
		// ——语料 12613/12613 引用身份的直接父键皆为 `content`（json 12109 +
		// yaml 504）⇒ 保持旧报错路径（"assign complex values to a variable
		// first"），不发明形态；lowering 侧 platformParamValue 的位置判据与
		// 此同一条规则。
		//
		// 但数组元素可以是**结构化值**（map），其内部成员位置仍可带 ref 叶子：
		// 语料实证 X157 `references[0].url.value.content` = 真实引用身份
		// （参考图列表的规范形态）。旧实现让整个数组走 literalValueTree ⇒
		// 含 ref 叶子的 map 元素返回 nil ⇒ ADAPTER_ERROR，把可编译的语料挡在
		// 门外。逐元素：先 literalValueTree（纯字面量 + file(...) 静态 cast），
		// 失败再交给 toolValueTree（成员感知，支持 ref 叶子）；仍失败 ⇒ nil。
		out := make([]any, 0, len(lit.Elements()))
		for _, el := range lit.Elements() {
			v := literalValueTree(el)
			if v == nil {
				switch el.(type) {
				case *ast.IdentExpr, *ast.FieldAccessExpr:
					return nil // 裸引用元素 = 无平台形态
				}
				v = g.toolValueTree(el)
			}
			if v == nil {
				return nil
			}
			out = append(out, v)
		}
		return out
	case *ast.MapLit:
		out := map[string]any{}
		for _, e := range lit.Entries() {
			key, ok := staticMapKeyName(e.Key)
			if !ok {
				return nil
			}
			v := g.toolValueTree(e.Value)
			if v == nil {
				return nil
			}
			out[key] = v
		}
		return out
	}
	return nil
}

// llmConfig extracts the llm(model, {opts}) configuration map: the
// "messages" array is carried as a ConfigPair (D1/D2 — prompt shape is a
// config surface interpreted by the backend mapping table). Message content
// is resolved through the output-template machinery so dataflow refs stay
// live as backend template strings.
func (g *Builder) llmConfig(call *ast.CallExpr, prevIDs []string, firstPort string) ([]ConfigPair, []Source, error) {
	if len(call.Args()) < 2 {
		return nil, nil, nil
	}
	m, ok := call.Args()[1].(*ast.MapLit)
	if !ok {
		return nil, nil, nil
	}
	var cfg []ConfigPair
	var inputs []Source
	seen := map[string]bool{}
	for _, e := range m.Entries() {
		key, ok := staticMapKeyName(e.Key)
		if !ok {
			return nil, nil, dynamicMapKeyError("llm config", e.Key)
		}
		switch key {
		case "temperature":
			if v := literalValue(e.Value); v != nil {
				cfg = append(cfg, ConfigPair{Key: "temperature", Value: v})
			}
			continue
		case "max_tokens":
			if v := literalValue(e.Value); v != nil {
				cfg = append(cfg, ConfigPair{Key: "max_tokens", Value: v})
			}
			continue
		case "top_p":
			if v := literalValue(e.Value); v != nil {
				cfg = append(cfg, ConfigPair{Key: "top_p", Value: v})
			}
			continue
		case "response_format":
			// 值形有三类：抽象枚举字符串 / 旧数值码 / provider 原生结构化对象
			// （{"type": "json_object"}，标准 LANGUAGE-REFERENCE 与示例 62）。
			// 用值树承载，否则对象形在 IR 层静默消失（编译通过 = 语义丢了）。
			if v := literalValueTree(e.Value); v != nil {
				cfg = append(cfg, ConfigPair{Key: "response_format", Value: v})
			}
			continue
		case "generation_diversity":
			if v := literalValue(e.Value); v != nil {
				cfg = append(cfg, ConfigPair{Key: "generation_diversity", Value: v})
			}
			continue
		case "model_name":
			// 模型显示名（① 用户拍板 2026-09-14，R1-1）：平台把 (id, 显示名) 成对存着，
			// 而显示名**不是 id 的函数**（同一 id 可有多名）⇒ 反编译读到的名字必须原样
			// 携带、正编译原样还原。它不作为平台参数下传，由各后端在自己的模型槽位消费。
			if v, ok := stringValue(e.Value); ok {
				cfg = append(cfg, ConfigPair{Key: "model_name", Value: v})
			}
			continue
		case "messages":
		default:
			// 未知键 ⇒ **原样承载**（标准：`llm(model, config)` 的 config 是开放
			// 字典，未知键保持开放透传 —— spec/docs/user/LANGUAGE-REFERENCE.md
			// 的 llm config 节；D-P30-2）。修前这里 `continue` ⇒ 未知键在 IR 层
			// 就消失（产物零痕迹、零诊断 = 静默丢作者写的参数）。
			// 只承载**静态标量字面量**（平台参数槽是静态名/值条目）：值不是字面量
			// （变量 / 表达式 / 容器）⇒ 今天不承载（登记 = residuals.yaml#R-122）。
			// 键名与取值原样进 IR，由各目标在自己的参数槽里写线
			// （coze-biz = renderLLMParam 的透传段，类型拼写由值类型一处决定）。
			if v := literalValue(e.Value); v != nil {
				cfg = append(cfg, ConfigPair{Key: key, Value: v})
			}
			continue
		}
		arr, ok := e.Value.(*ast.ArrayLit)
		if !ok {
			continue
		}
		var msgs []Message
		for _, el := range arr.Elements() {
			mm, ok := el.(*ast.MapLit)
			if !ok {
				continue
			}
			var msg Message
			for _, me := range mm.Entries() {
				k, ok := staticMapKeyName(me.Key)
				if !ok {
					return nil, nil, dynamicMapKeyError("llm message", me.Key)
				}
				switch k {
				case "role":
					if r, ok2 := stringValue(me.Value); ok2 {
						msg.Role = r
					}
				case "content":
					// 作者显式名额表（`content: template "<逐字文本>" using {…}`，
					// R-68 甲）：文本**逐字承载** + 名额表由作者写死 ⇒ 不走
					// templateExpr（那是内插路线：名额 ≡ 文本推导，装不下推不出的行）。
					if tr, ok2 := me.Value.(*ast.TemplateValue); ok2 {
						bs, berr := g.resolveTemplateBindings(tr, "llm message content")
						if berr != nil {
							return nil, nil, berr
						}
						msg.Content = tr.Text()
						msg.Bindings = bs
						break
					}
					content, terr := g.templateExpr(me.Value, prevIDs, firstPort)
					if terr != nil {
						return nil, nil, terr
					}
					msg.Content = content
					// Dataflow refs embedded in the prompt text are
					// registered as LLM inputs (node_inputs) so backends
					// wire producer → LLM edges (deduped by producer+port).
					g.collectBlockRefs(content, &inputs, seen)
				}
			}
			if msg.Role != "" {
				msgs = append(msgs, msg)
			}
		}
		// 名额名是平台解析模板的键，而 node_inputs 是**节点级**的一张表（system /
		// user 两个模板槽共用）⇒ 同名必须同引用：同名异引用是平台表达不了的形状
		// （一条 name 只能绑一个 ref），fail loud 而不是让 lowering 悄悄丢一条。
		names := map[string]Ref{}
		for _, msg := range msgs {
			for _, b := range msg.Bindings {
				if prev, dup := names[b.Name]; dup && prev != b.Ref {
					return nil, nil, &LanguageError{
						Code: diag.TEMPLATE_BINDING_INVALID,
						Message: fmt.Sprintf(
							"llm message content: 名额 %q 在两条 message 里指向不同的值（%s vs %s）—— 平台按名字解析模板，整节点的 node_inputs 是一张表，同名只能绑一个值",
							b.Name, refString(prev), refString(b.Ref)),
					}
				}
				names[b.Name] = b.Ref
			}
		}
		cfg = append(cfg, ConfigPair{Key: "messages", Value: msgs})
	}
	return cfg, inputs, nil
}

// blockOutputRefRe matches dataflow placeholders inside rendered templates:
// {{block_output_<id>.<port>}} where <port> may be a dotted record path
// (result.summary).
var blockOutputRefRe = regexp.MustCompile(`\{\{block_output_([0-9]+)\.([^}]+)\}\}`)

// collectBlockRefs extracts {{block_output_<id>.<port>}} references from a
// rendered template into inputs (deduped by producer+port). A dotted port
// yields both the full Name ("result.summary") and its Path segments
// ([result summary]) so backends can walk nested record fields.
func (g *Builder) collectBlockRefs(tmpl string, inputs *[]Source, seen map[string]bool) {
	for _, m := range blockOutputRefRe.FindAllStringSubmatch(tmpl, -1) {
		blockID, port := m[1], m[2]
		key := blockID + "\x00" + port
		if seen[key] {
			continue
		}
		seen[key] = true
		// Path always carries the port segments (single-element for a
		// scalar port, multi-element for a dotted record path): backends
		// key named-variable emission off len(Path) > 0.
		src := Source{BlockID: blockID, Name: port, Path: strings.Split(port, ".")}
		*inputs = append(*inputs, src)
	}
}

// buildExtract maps `extract<Type>(model, text, opts)` to a JSON-mode LLM
// call. The extraction payload is exposed as **one record port** `value`
// (the language field is `Extraction[T].value`, one property per record
// field). coze-biz's llm node has exactly one payload port (`output`, 414
// corpus examples) and no soft-failure ports, so an ok/reason/usage
// envelope has no carrier — declaring it would fabricate ports the platform
// never produces (2026-09-26, R-108).
func (g *Builder) buildExtract(id string, call *ast.CallExpr) (Node, error) {
	model := ""
	if len(call.Args()) > 0 {
		// MODEL slot = constant config slot; the target-validation pass pins
		// this one pass earlier too ("Extraction model must be a static
		// string"), so the two agree on the same ruler.
		m, merr := g.constantSlotString("extract() model", call.Args()[0])
		if merr != nil {
			return nil, merr
		}
		model = m
	}
	outTypes := map[string]*Type{
		"ok":     T("boolean"),
		"reason": T("string"),
		"usage":  TObject(map[string]*Type{}),
		"value":  TObject(map[string]*Type{}),
	}
	if len(call.TypeArgs()) > 0 {
		if nt, ok := call.TypeArgs()[0].(*ast.NamedTypeRef); ok {
			if td, ok := g.types[nt.Name()]; ok {
				props := map[string]*Type{}
				for _, f := range td.Fields() {
					props[f.Name()] = g.typeOf(f.TypeRef())
				}
				outTypes["value"] = TObject(props)
			}
		}
	}
	ln := &LLMCall{
		ID:             id,
		Model:          model,
		OutTypes:       outTypes,
		OutputFormat:   "json",
		ResponseFormat: 2,
		Result:         stringLLMResultContract(),
	}
	var contract *binding.ValidatedExtractionContract
	if g.prog.Snapshot != nil {
		if astID, ok := g.prog.Snapshot.NodeIDOf(call); ok {
			contract = g.prog.ValidatedExtractions[astID]
		}
	}
	if contract == nil && len(g.prog.ValidatedExtractions) == 1 {
		for _, only := range g.prog.ValidatedExtractions {
			contract = only
		}
	}
	if contract != nil {
		spec := &ExtractionSpec{
			Instruction: contract.Instruction,
			Strategy:    contract.Strategy,
			MemoryTurns: contract.MemoryTurns,
		}
		valueProps := outTypes["value"].Props
		for _, field := range contract.Fields {
			fieldType := valueProps[field.Name]
			if fieldType == nil {
				fieldType = extractionType(field.GraphonType)
			}
			spec.Fields = append(spec.Fields, ExtractionField{
				Name:        field.Name,
				Type:        fieldType,
				GraphonType: field.GraphonType,
				Description: field.Description,
				Options:     append([]string(nil), field.Options...),
			})
		}
		ln.Model = contract.Model
		ln.Extraction = spec
	}
	// 提示词承载（R-108）：`instruction` 与**抽取源**必须进产物。
	//
	// 修前两者一并不落：c0 的 llmParam 连 prompt 都没有（instruction 静默消失），
	// node_inputs 里那条 `input` 也没有任何模板槽引用它 ⇒ 模型既看不到指令、也
	// 看不到被抽取的文本，反编译只能还出一个 llm<T>（题面 + 抽取源整块丢）。
	//
	// 形状用 coze-biz llm 节点的**两个原生文本槽**（平台一手：语料 776 个 llm
	// 节点就是 prompt / systemPrompt 两槽，模板里的引用必须出现在槽文本里才会被
	// 求值，否则输入悬空）：
	//   · instruction  → systemPrompt（指令位；它同时是 raising 识别 extract 的
	//     位点之一，见 platforms/cozebiz/decompile/convert.go）
	//   · 抽取源       → prompt 里的数据引用（引用位，名字由 rewriteTemplateRefs
	//     按 node_inputs 物化）；字面量则逐字进 prompt
	var msgs []Message
	if ln.Extraction != nil && ln.Extraction.Instruction != "" {
		msgs = append(msgs, Message{Role: "system", Content: ln.Extraction.Instruction})
	}
	if len(call.Args()) > 1 {
		src := call.Args()[1]
		switch ref, isRef := g.resolveValueRef(src); {
		case isRef:
			ln.Inputs = append(ln.Inputs, Source{Path: []string{"input"}, BlockID: ref.nodeID, Name: ref.output})
			msgs = append(msgs, Message{
				Role:    "user",
				Content: fmt.Sprintf("{{block_output_%s.%s}}", ref.nodeID, ref.output),
			})
		default:
			if lit, ok := g.staticString(src); ok {
				msgs = append(msgs, Message{Role: "user", Content: lit})
				break
			}
			// 非引用、非静态字面量（如拼接表达式）：本目标的 LLM 节点只能把
			// 抽取源承载成 prompt 里的一条引用或一段字面文本 ⇒ 静默丢 = 模型
			// 看不到文本（照平台物理形状发射只会得到一次无输入的抽取）。fail
			// loud，并给作者可执行的替代写法。
			return nil, &UnsupportedError{Construct: fmt.Sprintf(
				"extract() text argument (%s) is neither a value reference nor a literal string: "+
					"this target carries the extraction source as one prompt slot (a reference or literal text), "+
					"so compute it into a local first (`let t = <expr>;` then `extract<T>(model, t, {...})`)",
				exprLabel(src))}
		}
	}
	if len(msgs) > 0 {
		ln.Config = append(ln.Config, ConfigPair{Key: "messages", Value: msgs})
	}
	return ln, nil
}

func extractionType(graphonType string) *Type {
	switch graphonType {
	case "number":
		return T("number")
	case "boolean":
		return T("boolean")
	case "object":
		return TObject(map[string]*Type{})
	case "array[string]":
		return TList(T("string"))
	case "array[number]":
		return TList(T("number"))
	case "array[boolean]":
		return TList(T("boolean"))
	default:
		return T("string")
	}
}

// buildCode maps `foreign code <lang>(...) -> T { source ... }` to a CodeRun.
func (g *Builder) buildCode(id string, fc *ast.ForeignCodeExpr) (Node, error) {
	g.recordPlacement(id, placementConstruct("foreign-code", ""), PlacementPassthrough, placementReason(PlacementCausePurity, "user-owned code, never rewritten (透传契约)"))
	language := "python"
	switch fc.Language() {
	case "javascript", "js", "nodejs":
		language = "javascript"
	}
	code, interp := templateText(fc.Source())
	if interp {
		return nil, &UnsupportedError{Construct: "foreign code source with interpolation"}
	}
	cn := &CodeRun{ID: id, Code: code, Language: language, Types: map[string]*Type{}, OutTypes: map[string]*Type{}}
	for _, b := range fc.Inputs() {
		if ref, ok := g.resolveValueRef(b.Value()); ok {
			cn.Inputs = append(cn.Inputs, Source{Path: []string{b.Name()}, BlockID: ref.nodeID, Name: ref.output})
			cn.Types[b.Name()] = g.typeOf(b.TypeRef())
			continue
		}
		// 字面量输入（roundtrip：coze code 节点参数可绑定字面量）→
		// literal Source，渲染端 fieldSourceToValue 写 literal 参数。
		// literalValueTree：标量 + 数组/map 字面量树（X212/S2：
		// array<any> caption_list = [] 空数组常量参数）。
		if v := literalValueTree(b.Value()); v != nil {
			cn.Inputs = append(cn.Inputs, Source{Path: []string{b.Name()}, Val: derefNullTree(v)})
			cn.Types[b.Name()] = g.typeOf(b.TypeRef())
			continue
		}
		// 其余表达式无法表达为参数 → 显式失败（P4：不静默丢参，用户需
		// 先赋值给变量再绑定）。
		return nil, &UnsupportedError{Construct: fmt.Sprintf(
			"foreign code input %q: assign the value to a variable first (cannot lower %T to a workflow parameter)",
			b.Name(), b.Value())}
	}
	// declared output contract: direct outputs expose one "result" port;
	// structural outputs expose one port per field.
	switch out := fc.Output().(type) {
	case *ast.CodeDirectOutput:
		cn.OutTypes["result"] = g.typeOf(out.TypeRef())
		cn.OutOrder = []string{"result"}
	case *ast.CodeStructuralOutput:
		for _, f := range out.Fields() {
			cn.OutTypes[f.Name()] = g.typeOf(f.TypeRef())
			cn.OutOrder = append(cn.OutOrder, f.Name())
		}
	}
	return cn, nil
}

// semanticExprType returns the type conclusion for an expression in the
// typed-bound program, translated to the neutral IR type used by platform
// ports.  Record literals are particularly important here: treating every
// literal property as string loses the nested output contract and makes a
// later branch phi appear to have a different type.  The typed conclusion is
// authoritative; the syntax fallback only exists for small hand-built IR
// tests that do not carry a TypedBoundProgram.
func (g *Builder) semanticExprType(expr ast.Expr) *Type {
	if expr == nil {
		return nil
	}
	if g.prog != nil && g.prog.Snapshot != nil && g.prog.NodeTypes != nil {
		if id, ok := g.prog.Snapshot.NodeIDOf(expr); ok {
			if d := g.prog.NodeTypes[id]; d != nil {
				if t := g.semanticDescriptorType(d); t != nil {
					return t
				}
			}
		}
	}
	// Keep this fallback deliberately narrow. It improves diagnostics and
	// preserves type information in tests without guessing the type of an
	// arbitrary expression or foreign-code result.
	switch e := expr.(type) {
	case *ast.StringLit:
		return T("string")
	case *ast.IntLit:
		return T("integer")
	case *ast.FloatLit:
		return T("number")
	case *ast.BoolLit:
		return T("boolean")
	case *ast.NullLit:
		return nil
	case *ast.ArrayLit:
		var elem *Type
		for _, child := range e.Elements() {
			ct := g.semanticExprType(child)
			if ct == nil {
				continue
			}
			if elem == nil {
				elem = ct
			} else if !sameIRType(elem, ct) {
				return TList(T("object"))
			}
		}
		if elem == nil {
			elem = T("object")
		}
		return TList(elem)
	case *ast.MapLit:
		props := map[string]*Type{}
		for _, entry := range e.Entries() {
			key, ok := staticMapKeyName(entry.Key)
			if !ok || !IsPlatformIdent(key) {
				continue
			}
			if ct := g.semanticExprType(entry.Value); ct != nil {
				props[key] = ct
			}
		}
		return TObject(props)
	case *ast.IdentExpr, *ast.FieldAccessExpr, *ast.IndexExpr:
		if ref, ok := g.resolveValueRef(expr); ok {
			return g.portTypeOf(ref.nodeID, ref.output)
		}
	}
	return nil
}

// refType resolves the authoritative type conclusion for a bound reference
// value when materializing a shim's input/output contract.  Resolution order:
// (1) the typed-bound semantic conclusion for the expression (TypePass
// NodeTypes — authoritative), (2) the varRef's stamped capability shape,
// (3) the narrow port registry.  If every source is silent the reference type
// is UNKNOWN — that is a hard build error, never a forged string.  A forged
// string is trusted by the next decompile and breaks the roundtrip on L4
// (X4 root cause); fail loud instead of hiding the missing contract.
// dottedRefType resolves a dotted output path (producer "it.like_count")
// against a producer's stamped element shape: strip the item/port prefix
// after the first dot and descend the record Props chain when the base type
// is an object/record.  Returns nil when the path is not a field descent
// (whole-port or unknown field) so the caller falls back to the base type.
// portFieldType resolves a **port name** (possibly dotted: `data.table_id`)
// against a declared result record, descending Props per dot-segment.
// Unlike dottedRefType it does NOT treat an undotted name as "the whole
// port": the name IS the field name, so a miss returns nil (unknown port)
// rather than the base record.
func portFieldType(base *Type, port string) *Type {
	if base == nil || port == "" {
		return nil
	}
	cur := base
	for _, field := range strings.Split(port, ".") {
		if cur == nil || (cur.Kind != "object" && cur.Kind != "record") {
			return nil
		}
		ft, ok := cur.Props[field]
		if !ok || ft == nil {
			return nil
		}
		cur = ft
	}
	return cur
}

func dottedRefType(base *Type, output string) *Type {
	if base == nil || output == "" {
		return nil
	}
	i := strings.Index(output, ".")
	if i < 0 {
		// whole port: the base shape is authoritative (element type already
		// stamped by resolveValueRef / loop registration).
		return base
	}
	cur := base
	for _, field := range strings.Split(output[i+1:], ".") {
		if cur == nil || (cur.Kind != "object" && cur.Kind != "record") {
			return nil
		}
		ft, ok := cur.Props[field]
		if !ok {
			return nil
		}
		cur = ft
	}
	return cur
}

func (g *Builder) refType(ref varRef, expr ast.Expr) (*Type, error) {
	if expr != nil {
		if t := g.semanticExprType(expr); t != nil {
			return t, nil
		}
	}
	if ref.typ != nil {
		// tool 绑定：ref.typ 是**整条结果记录**（callBindRef 为成员路径还原
		// 携带的字段表），ref.output 是该记录下的端口/成员路径（`duration`、
		// `data.url`）。此时整段都是字段路径，必须按记录下钻取**字段类型**；
		// dottedRefType 把无点的名字当「整个端口」（返回 base），会把
		// `int(r.duration)` 的 shim 参数声明成 `object{...}` 而不是 number
		// （一手：语料 3 份 S122/S132/mryy 的 `int(v.duration)` 因此恒失败——
		// 运行期 object 声明把数字包成 {value:n}，`int()` 收不到标量）。
		if ref.kind == "tool" && ref.output != "" {
			if t := portFieldType(ref.typ, ref.output); t != nil {
				return t, nil
			}
		}
		if t := dottedRefType(ref.typ, ref.output); t != nil {
			return t, nil
		}
		return ref.typ, nil
	}
	if t := g.portTypeOf(ref.nodeID, ref.output); t != nil {
		return t, nil
	}
	return nil, fmt.Errorf("cannot resolve type of reference %s.%s (producer kind %q has no typed conclusion and no port contract)",
		ref.nodeID, ref.output, ref.kind)
}

func (g *Builder) semanticDescriptorType(d *dsltypes.Descriptor) *Type {
	if d == nil {
		return nil
	}
	switch d.Kind {
	case dsltypes.String:
		return T("string")
	case dsltypes.Int:
		return T("integer")
	case dsltypes.Float:
		return T("number")
	case dsltypes.Bool:
		return T("boolean")
	case dsltypes.File:
		return &Type{Kind: "file", AllowedFileTypes: append([]string(nil), d.File.Types...), AllowedFileExtensions: append([]string(nil), d.File.Extensions...), AllowedFileUploadMethods: append([]string(nil), d.File.UploadMethods...)}
	case dsltypes.Optional:
		inner := g.semanticDescriptorType(d.Elem)
		if inner == nil {
			return nil
		}
		inner.Optional = true
		return inner
	case dsltypes.Array:
		return TList(g.semanticDescriptorType(d.Elem))
	case dsltypes.Map:
		return TObject(map[string]*Type{})
	case dsltypes.Record:
		props := make(map[string]*Type, len(d.Fields))
		for _, f := range d.Fields {
			if ft := g.semanticDescriptorType(f.Type); ft != nil {
				props[f.Name] = ft
			}
		}
		return TObject(props)
	case dsltypes.Enum:
		// 枚举的载体 = 成员串（与 `typeOf` 的枚举分支同一条规则；R-96）。
		return T("string")
	case dsltypes.Any:
		return nil
	case dsltypes.Null, dsltypes.Void, dsltypes.Function:
		return nil
	default:
		return nil
	}
}

func sameIRType(a, b *Type) bool {
	if a == nil || b == nil || a.Kind != b.Kind {
		return a == nil && b == nil
	}
	if a.Kind == "list" {
		return sameIRType(a.Elem, b.Elem)
	}
	if a.Kind != "object" {
		return true
	}
	if len(a.Props) != len(b.Props) {
		return false
	}
	for name, at := range a.Props {
		if !sameIRType(at, b.Props[name]) {
			return false
		}
	}
	return true
}

// mapFieldType preserves the contextual record slot, not just the value's
// narrower type (null itself has no physical base type; a string reference can
// still initialize a string? slot). The TypePass aggregate conclusion is the
// authority for every recursive field. Input bindings retain producer types.
func (g *Builder) mapFieldType(m *ast.MapLit, key string, valueType *Type) *Type {
	if record := g.semanticExprType(m); record != nil && record.Props[key] != nil {
		return record.Props[key]
	}
	return valueType
}

// buildMap materializes an object literal as a CodeRunner that returns the
// bound values under their literal keys ({"g1": g1, "g2": g2}).
// Every value renders through the declarative renderer (renderLiteralPython
// + coalesce): resolvable references bind as parameters, scalars and nested
// literals inline. A value the renderer cannot express is an explicit error
// (P4: never silently degrade to None — the user must assign the value to a
// variable first or write foreign code).
func (g *Builder) buildMap(id string, m *ast.MapLit) (Node, error) {
	// P5.13: pyast path when every key is quote-safe and every value lowers
	// byte-identically (ref values bind by argN; literal values via
	// lowerLiteralPyast). Multi-key dicts take the sorted-key pyast path —
	// key ORDER is the P5.10a semantic-equivalence exemption (key SET
	// identical). Quote-unsafe keys fall back so the legacy json.Marshal
	// bytes are kept.
	if node, ok, err := g.lowerMapPyast(id, m); err != nil {
		return nil, &UnsupportedError{Construct: fmt.Sprintf(
			"map value: %s (assign the value to a variable first, or write foreign code)",
			constructOf(err))}
	} else if ok {
		return node, nil
	}
	names := []string{}
	sources := []Source{}
	types := map[string]*Type{}
	kv := []string{}
	props := map[string]*Type{}
	for i, e := range canonicalMapEntries(m.Entries()) {
		key, ok := staticMapKeyName(e.Key)
		if !ok {
			return nil, dynamicMapKeyError("map literal", e.Key)
		}
		keyLit, _ := json.Marshal(key)
		// 1) plain resolvable reference → bind by parameter name
		if ref, ok2 := g.resolveValueRef(e.Value); ok2 {
			arg := fmt.Sprintf("arg%d", i+1)
			sources = append(sources, Source{Path: []string{arg}, BlockID: ref.nodeID, Name: ref.output})
			rt, rerr := g.refType(ref, e.Value)
			if rerr != nil {
				return nil, &UnsupportedError{Construct: fmt.Sprintf("map value: %s", rerr.Error())}
			}
			types[arg] = rt
			names = append(names, arg)
			kv = append(kv, fmt.Sprintf("%s: %s", keyLit, arg))
			// Dict keys that are not platform identifiers are DATA (they
			// stay in the generated code as literal keys); only legal
			// identifiers become referenceable output-schema properties —
			// a Chinese property name is rejected at import (只能包含字母、
			// 数字或下划线...).
			if IsPlatformIdent(key) {
				props[key] = g.mapFieldType(m, key, rt)
			}
			continue
		}
		// 2) non-ref value → declarative render (coalesce projection, scalar /
		// nested literal, or expression): bind resolvable leaves as parameters.
		// A value outside the declarative set is an explicit error, never a
		// silent None (P4).
		var bindings []textBinding
		rendered, rerr := renderLiteralPython(e.Value, g, &bindings)
		if rerr != nil {
			return nil, &UnsupportedError{Construct: fmt.Sprintf(
				"map value: %s (assign the value to a variable first, or write foreign code)",
				constructOf(rerr))}
		}
		kv = append(kv, fmt.Sprintf("%s: %s", keyLit, rendered))
		if IsPlatformIdent(key) {
			// The generated Python returns the complete literal, not a string
			// serialization.  Preserve the typed AST conclusion for nested
			// records, arrays, files, and numeric/bool leaves; using string for
			// every literal makes the next decompile advertise the wrong port
			// contract and breaks branch aggregation on L4.
			props[key] = g.mapFieldType(m, key, g.semanticExprType(e.Value))
			if props[key] == nil {
				props[key] = T("string")
			}
		}
		for _, b := range bindings {
			if containsParam(names, b.name) {
				continue
			}
			names = append(names, b.name)
			sources = append(sources, Source{Path: []string{b.name}, BlockID: b.ref.nodeID, Name: b.ref.output})
			bt, berr := g.refType(b.ref, nil)
			if berr != nil {
				return nil, &UnsupportedError{Construct: fmt.Sprintf("map value: %s", berr.Error())}
			}
			types[b.name] = bt
		}
	}
	code := ShimMarker + fmt.Sprintf("def main(%s) -> dict:\n    return {%s}", strings.Join(names, ", "), strings.Join(kv, ", "))
	// D3 (G8): the record shim declares its object output so downstream
	// nodes can bind it: the reference target maps the logical "output" port through
	// singleTypeKey(OutTypes) → "result", coze keeps its canonical
	// "output" port and exposes the extra result port.
	node := &CodeRun{
		ID:        id,
		Code:      code,
		Language:  "python",
		Inputs:    sources,
		Types:     types,
		OutTypes:  map[string]*Type{"result": TObject(props)},
		OutOrder:  []string{"result"},
		Synthetic: true,
	}
	// 零值标注：同 lowerMapPyast（quote-unsafe key 的 legacy 回退路径）。
	if shape := node.OutTypes["result"]; shape != nil {
		if isZeroRecordLiteral(m, shape) {
			node.ZeroOf = shape
		}
		node.ZeroFields = zeroRecordLiteralFields(m, shape)
	}
	return node, nil
}

// lowerMapPyast lowers an object literal (G8 record shim) through the pyast
// path, mirroring the legacy buildMap loop: resolvable reference values bind
// by argN parameter name (no ident-name special case — legacy uses argN for
// every ref value); every other value lowers via lowerLiteralPyast
// (byte-identical for the same shapes: pythonJSONString/quoteString share
// the double-quote \uXXXX escape family). ok=false means at least one key is
// quote-unsafe (json.Marshal vs quoteString escape difference) or a value is
// not provably byte-identical — the caller falls back to the legacy string
// template. Dict key ORDER is the P5.10a semantic-equivalence exemption
// (pyast sorts, legacy preserves insertion order — the key SET is
// identical).
func (g *Builder) lowerMapPyast(id string, m *ast.MapLit) (Node, bool, error) {
	names := []string{}
	sources := []Source{}
	types := map[string]*Type{}
	props := map[string]*Type{}
	entries := canonicalMapEntries(m.Entries())
	items := make([]pyast.DictItem, 0, len(entries))
	for i, e := range entries {
		key, ok := staticMapKeyName(e.Key)
		if !ok || !keyQuoteSafe(key) {
			return nil, false, nil // dynamic / quote-unsafe key — fallback
		}
		// 1) plain resolvable reference → bind by parameter name (argN).
		if ref, ok2 := g.resolveValueRef(e.Value); ok2 {
			arg := fmt.Sprintf("arg%d", i+1)
			names = append(names, arg)
			sources = append(sources, Source{Path: []string{arg}, BlockID: ref.nodeID, Name: ref.output})
			rt, rerr := g.refType(ref, e.Value)
			if rerr != nil {
				return nil, false, rerr
			}
			types[arg] = rt
			items = append(items, pyast.DictItem{
				Key:   &pyast.StringExpr{Value: key},
				Value: &pyast.NameExpr{Name: arg},
			})
			if IsPlatformIdent(key) {
				props[key] = g.mapFieldType(m, key, rt)
			}
			continue
		}
		// 2) non-ref value → declarative lower (coalesce projection, scalar /
		// nested literal, or expression): bind resolvable leaves as params.
		var bindings []textBinding
		pa, ok2, err := lowerLiteralPyast(e.Value, g, &bindings)
		if err != nil {
			return nil, false, err
		}
		if !ok2 {
			return nil, false, nil
		}
		items = append(items, pyast.DictItem{
			Key:   &pyast.StringExpr{Value: key},
			Value: pa,
		})
		if IsPlatformIdent(key) {
			props[key] = g.mapFieldType(m, key, g.semanticExprType(e.Value))
			if props[key] == nil {
				props[key] = T("string")
			}
		}
		for _, b := range bindings {
			if containsParam(names, b.name) {
				continue
			}
			names = append(names, b.name)
			sources = append(sources, Source{Path: []string{b.name}, BlockID: b.ref.nodeID, Name: b.ref.output})
			bt, berr := g.refType(b.ref, nil)
			if berr != nil {
				return nil, false, berr
			}
			types[b.name] = bt
		}
	}
	// The record shim returns the object literal DIRECTLY (no "result"
	// wrapper — the dict IS the multi-output record), mirroring the legacy
	// buildMap template; wrapShim would add {"result": ...}.
	mod := &pyast.Module{
		Params: namedParams(names),
		Body:   []pyast.Stmt{&pyast.ReturnStmt{Value: &pyast.DictExpr{Items: items}}},
	}
	code := ShimMarker + strings.TrimSuffix(pyast.Serialize(mod), "\n")
	node := &CodeRun{
		ID:        id,
		Code:      code,
		Language:  "python",
		Inputs:    sources,
		Types:     types,
		OutTypes:  map[string]*Type{"result": TObject(props)},
		OutOrder:  []string{"result"},
		Synthetic: true,
	}
	// 零值标注（IR 优化阶段的输入，见 fold_zero_identity_candidates.go）：
	// 只有**整个字面量就是该 record 的类型零值**（字段集一致且每字段递归为零）
	// 时才标；有一个字段非零就是真实值，标注即撒谎。
	if shape := node.OutTypes["result"]; shape != nil {
		if isZeroRecordLiteral(m, shape) {
			node.ZeroOf = shape
		}
		node.ZeroFields = zeroRecordLiteralFields(m, shape)
	}
	return node, true, nil
}

// buildJSONParse lowers json.parse<T>(input) (G11) to a Synthetic
// multi-output CodeRun: `json.loads` + per-field extraction with typed
// defaults (string → "", integer/number → 0, boolean → false). The schema
// comes from the type argument T (a named record declared in the program);
// coze recognizes the shape and lowers it to the native type-59
// JsonDeserialization node, the reference target keeps the code node (same runtime
// semantics as today's hand-written json.loads foreign code). Field access
// on the bound variable resolves onto the per-field output ports.
func (g *Builder) buildJSONParse(id string, call *ast.CallExpr, prevIDs []string, firstPort string) (Node, error) {
	if len(call.Args()) != 1 {
		return nil, &UnsupportedError{Construct: fmt.Sprintf(
			"json.parse expects exactly 1 argument, got %d", len(call.Args()))}
	}
	// Schema resolution (G12): either a named record (per-field extraction)
	// or a top-level array type argument json.parse<array<T>> whose whole
	// parsed value IS the array (single "result" output).
	var fields []*ast.FieldDecl
	var arrayElem *Type
	var openObject bool
	if len(call.TypeArgs()) == 1 {
		switch targ := call.TypeArgs()[0].(type) {
		case *ast.NamedTypeRef:
			if td, ok := g.types[targ.Name()]; ok {
				fields = td.Fields()
			}
		case *ast.ArrayTypeRef:
			arrayElem = g.typeOf(targ.Element())
		case *ast.PrimTypeRef:
			if targ.Name() != "object" {
				return nil, &UnsupportedError{Construct: "json.parse requires a named record, array<...>, or object schema type argument"}
			}
			openObject = true
		}
	}
	if fields == nil && arrayElem == nil && !openObject {
		return nil, &UnsupportedError{Construct: "json.parse requires a named record, array<...>, or object schema type argument"}
	}
	a2, herr := g.hoistUserCalls(call.Args()[0], prevIDs, firstPort)
	if herr != nil {
		return nil, herr
	}
	rr, ok := g.resolveValueRef(a2)
	if !ok {
		var verr error
		rr, verr = g.emitExprCode(prevIDs, firstPort, false, a2)
		if verr != nil {
			return nil, &UnsupportedError{Construct: fmt.Sprintf(
				"json.parse argument %T is not a resolvable value (assign the value to a variable first, or wrap it in a `foreign code` block)", a2)}
		}
	}
	if openObject {
		// Open-object schema: the parsed value is a JSON object without
		// statically committed keys. Preserve it as an explicit object
		// contract; do not invent record fields or widen it to a scalar.
		typ, terr := g.refType(rr, a2)
		if terr != nil {
			return nil, &UnsupportedError{Construct: fmt.Sprintf("json.parse: %s", terr.Error())}
		}
		objType := TObject(map[string]*Type{})
		data := &pyast.NameExpr{Name: "data"}
		assign := &pyast.AssignStmt{
			Name:  "data",
			Value: &pyast.CallExpr{Func: "json.loads", Args: []pyast.Expr{&pyast.NameExpr{Name: "arg1"}}},
		}
		ret := &pyast.ReturnStmt{Value: &pyast.DictExpr{Items: []pyast.DictItem{
			{Key: &pyast.StringExpr{Value: "output"}, Value: data},
		}}}
		// Compiler-generated shims must use the canonical def main wrapper.
		// A hand-formatted body would be indistinguishable from foreign code
		// on the reverse pass and break the shim round-trip contract.
		code := ShimMarker + wrapShimStmts([]pyast.Param{{Name: "arg1"}}, []string{"json"}, []pyast.Stmt{assign, ret})
		return &CodeRun{
			ID:         id,
			Code:       code,
			Language:   "python",
			Inputs:     []Source{{Path: []string{"arg1"}, BlockID: rr.nodeID, Name: rr.output}},
			Types:      map[string]*Type{"arg1": typ},
			OutTypes:   map[string]*Type{"output": objType},
			OutOrder:   []string{"output"},
			Synthetic:  true,
			JSONSchema: objType,
		}, nil
	}
	if arrayElem != nil {
		// Top-level array schema: the parsed value is the whole array.
		// The coze type-59 node exposes it on the single "output" port as
		// a list; the reference target keeps the code node whose "result" port carries the
		// array (empty list when the payload is not a JSON array, mirroring
		// the hand-written isinstance guard).
		// P5.11: pyast path (multi-statement shim; ternary parens are the
		// L2 semantic-equivalence exemption); legacy template otherwise.
		code, _ := lowerJsonParseShim(g, "arg1", nil, arrayElem)
		typ, terr := g.refType(rr, a2)
		if terr != nil {
			return nil, &UnsupportedError{Construct: fmt.Sprintf("json.parse: %s", terr.Error())}
		}
		return &CodeRun{
			ID:         id,
			Code:       code,
			Language:   "python",
			Inputs:     []Source{{Path: []string{"arg1"}, BlockID: rr.nodeID, Name: rr.output}},
			Types:      map[string]*Type{"arg1": typ},
			OutTypes:   map[string]*Type{"output": TList(arrayElem)},
			OutOrder:   []string{"output"},
			Synthetic:  true,
			JSONSchema: TList(arrayElem),
		}, nil
	}
	schemaProps := map[string]*Type{}
	for _, f := range fields {
		schemaProps[f.Name()] = g.typeOf(f.TypeRef())
	}
	// 记录形态 = **一个真实口 `output` 承载整个解析结果**（成员经 `output.<field>`
	// 的 dotted 路径读）。这既是平台的形状（见 lowerJsonParseShim 的语料依据），
	// 也是唯一能让「整值读 `json.parse<T>(…)`」与「成员读 `.field`」共用同一承载
	// 口的形态：逐字段散口会让整值读没有承载者（@whole 标记漏进 wire）。
	recType := TObject(schemaProps)
	outTypes := map[string]*Type{"output": recType}
	outOrder := []string{"output"}
	typ, terr := g.refType(rr, a2)
	if terr != nil {
		return nil, &UnsupportedError{Construct: fmt.Sprintf("json.parse: %s", terr.Error())}
	}
	// P5.11: pyast path (multi-statement shim, per-field data.get, multi-key
	// dict key order exempted — P5.10a); legacy template otherwise (e.g. a
	// quote-unsafe field name in the string-literal-exemption family).
	code, ok := lowerJsonParseShim(g, "arg1", fields, nil)
	if !ok {
		lines := make([]string, 0, len(fields))
		for _, f := range fields {
			ft := g.typeOf(f.TypeRef())
			lines = append(lines, fmt.Sprintf("%s: data.get(%s, %s)",
				pythonJSONString(f.Name()), pythonJSONString(f.Name()), jsonDefaultLit(ft)))
		}
		code = fmt.Sprintf("def main(arg1) -> dict:\n    import json\n    data = json.loads(arg1)\n    return {\"output\": {%s}}",
			strings.Join(lines, ", "))
	}
	return &CodeRun{
		ID:         id,
		Code:       code,
		Language:   "python",
		Inputs:     []Source{{Path: []string{"arg1"}, BlockID: rr.nodeID, Name: rr.output}},
		Types:      map[string]*Type{"arg1": typ},
		OutTypes:   outTypes,
		OutOrder:   outOrder,
		Synthetic:  true,
		JSONSchema: TObject(schemaProps),
	}, nil
}

// IsJSONParseShim reports whether this CodeRun is the synthetic json.parse<T>
// shim (G11): compiler-generated, exactly one bound input whose body calls
// `json.loads(arg)`. Both targets use the shape: coze lowers it to the
// native JsonDeserialization node (type 59), the reference target keeps the code node and
// must map the shim's dotted "output.field" refs onto its per-field ports.
func (n *CodeRun) IsJSONParseShim() bool {
	if !n.Synthetic || len(n.Inputs) != 1 || len(n.Inputs[0].Path) == 0 {
		return false
	}
	return strings.Contains(n.Code, "json.loads("+n.Inputs[0].Path[0]+")")
}

// IsJSONStringifyShim reports whether this CodeRun is the synthetic
// json.stringify(v) shim (G8): compiler-generated, exactly one bound input
// whose body calls `json.dumps(arg, ensure_ascii=False)`. Both targets use the
// shape: coze lowers it to the native JsonSerialization node (type 58, wire
// `to_json`); 参考目标 keeps the code node. 对称面 = IsJSONParseShim（同一族，
// 一读一写；反编译端 `nodeTypeAliases` 认 `to_json` 而正编译曾经只发 code
// ⇒「只读不写」的不对称，R-117 收口）。
func (n *CodeRun) IsJSONStringifyShim() bool {
	if !n.Synthetic || len(n.Inputs) != 1 || len(n.Inputs[0].Path) == 0 {
		return false
	}
	return strings.Contains(n.Code, "json.dumps("+n.Inputs[0].Path[0])
}

// ── json 内建：**原生承载条件**（与「平台有没有这个节点」分离）─────────────
//
// 用户口径（2026-09-27）：「json 是 ncoda 内置的功能，coze-biz 恰巧有这个节点；
// ncoda 有独立语义和语法，不被平台绑架」。
//
// 所以「这个内建落原生还是落 shim」由**两条独立事实**共同决定，缺一不可：
//
//	① 平台**有没有**原生 json 节点 —— 能力轴 `capability.Table.JSONSerial/JSONDeserial`
//	   （本包不持平台知识，只消费目标名与那两个布尔）；
//	② 这个**载荷**能不能逐字放进那个节点 —— 下面两个谓词（IR 侧可判；唯一用到的
//	   平台词汇是已有的 `IsPlatformIdent`）。
//
// ② 的两个谓词是**逐字搬自渲染侧原有守卫**（cozebiz `renderJSONSerialization` /
// `renderJSONDeserialization`）。搬出来是因为：决策日志在构造期写、真实落点在渲染期定，
// 守卫只长在渲染侧 ⇒ 构造期只能猜（历史现场：日志恒写「no IR-native node」，
// 而产物里是原生节点 —— 日志与产物互相矛盾，一手可复算）。
//
// **本地不判「平台有没有」** —— 那是能力轴的事，一处定义。

// JSONSerializeNativeHostable reports whether the target's native JSON
// serialization carrier can hold this shim's payload verbatim: a single
// ref-bound input (字面量直喂无先例) and a single string output port.
func (n *CodeRun) JSONSerializeNativeHostable() bool {
	if !n.IsJSONStringifyShim() {
		return false
	}
	if n.Language != "" && n.Language != "python" {
		return false
	}
	if n.Exception != nil {
		return false
	}
	if len(n.Inputs) != 1 || n.Inputs[0].BlockID == "" {
		return false
	}
	if len(n.OutTypes) != 1 {
		return false
	}
	for _, rt := range n.OutTypes {
		if rt == nil || rt.Kind != "string" {
			return false
		}
	}
	return true
}

// JSONParseNativeHostable reports whether the target's native JSON
// deserialization carrier can hold this shim's payload verbatim: a single
// ref-bound input, one `output` port, and a schema whose member names are all
// spellable on the platform (a non-identifier member cannot be declared ⇒ the
// carrier would silently drop it).
func (n *CodeRun) JSONParseNativeHostable() bool {
	if !n.IsJSONParseShim() {
		return false
	}
	if n.Language != "" && n.Language != "python" {
		return false
	}
	if n.Exception != nil {
		return false
	}
	if len(n.Inputs) != 1 || n.Inputs[0].BlockID == "" {
		return false
	}
	if len(n.OutTypes) != 1 {
		return false
	}
	rt, ok := n.OutTypes["output"]
	if !ok || rt == nil || (rt.Kind != "object" && rt.Kind != "list") {
		return false
	}
	return jsonDeserPropsDeclarable(rt)
}

// jsonDeserPropsDeclarable 递归判定成员表里的**名字**能否逐字写出：非平台标识符
// ⇒ **不可声明**（渲染侧的 `typeDecl` 会**静默丢**该成员 —— 撞「失配 ≠ 静默丢弃」
// 红线 ⇒ 这种情况必须留 shim，而不是发一个丢字段的原生节点）。
func jsonDeserPropsDeclarable(t *Type) bool {
	if t == nil {
		return true
	}
	switch t.Kind {
	case "object":
		for k, v := range t.Props {
			if !IsPlatformIdent(k) || !jsonDeserPropsDeclarable(v) {
				return false
			}
		}
		return true
	case "list":
		return jsonDeserPropsDeclarable(t.Elem)
	default:
		return true
	}
}

// jsonDefaultLit returns the Python default literal for a JSON extraction
// field of the given type (string → "", integer/number → 0, boolean →
// false, array → []; anything else → None). The array default keeps the
// reference code-node path equivalent to the hand-written `or []` guard: a
// missing field binds [] instead of None, so downstream array inputs stay
// well-typed.
func jsonDefaultLit(t *Type) string {
	switch t.Kind {
	case "integer", "number":
		return "0"
	case "boolean":
		return "False"
	case "string":
		return "\"\""
	case "list", "array":
		return "[]"
	}
	return "None"
}

// buildStringify lowers the json.stringify(value) projection primitive (G8)
// to a compiler-generated JSON serialization shim — the exact single-input
// `json.dumps(arg)` shape the coze target recognizer (codeRunJSON) lowers to
// the native type-58 JsonSerialization node. All produced shims carry
// Synthetic=true (compiler-generated), so the G7 passthrough contract holds:
// user foreign code is never rewritten. Inline object/array literals are
// materialized first (reusing buildMap/buildArray) and their canonical output
// port is serialized, so coze renders code(record)+type-58 and the reference target renders
// code(record)+code(dumps) — identical runtime semantics to today's
// hand-written foreign code.
func (g *Builder) buildStringify(id string, call *ast.CallExpr, prevIDs []string, firstPort string) (Node, error) {
	if len(call.Args()) != 1 {
		return nil, &UnsupportedError{Construct: fmt.Sprintf(
			"json.stringify expects exactly 1 argument, got %d", len(call.Args()))}
	}
	arg := call.Args()[0]
	var ref varRef
	switch a := arg.(type) {
	case *ast.MapLit:
		a2, herr := g.hoistUserCalls(a, prevIDs, firstPort)
		if herr != nil {
			return nil, herr
		}
		rid := g.nextID()
		rec, merr := g.buildMap(rid, a2.(*ast.MapLit))
		if merr != nil {
			return nil, merr
		}
		g.add(rec)
		ref = varRef{nodeID: rid, output: bindPortOf(rec, "code"), kind: "code"}
	case *ast.ArrayLit:
		a2, herr := g.hoistUserCalls(a, prevIDs, firstPort)
		if herr != nil {
			return nil, herr
		}
		rid := g.nextID()
		anode, aerr := g.buildArray(rid, a2.(*ast.ArrayLit))
		if aerr != nil {
			return nil, aerr
		}
		g.add(anode)
		ref = varRef{nodeID: rid, output: bindPortOf(anode, "code"), kind: "code"}
	default:
		// hoist first so an operation call inside the argument
		// (json.stringify(split(...)[0])) is materialized into a bound
		// variable before resolution / shim rendering (§5.3).
		a2, herr := g.hoistUserCalls(arg, prevIDs, firstPort)
		if herr != nil {
			return nil, herr
		}
		rr, ok := g.resolveValueRef(a2)
		if !ok {
			// scalar array index (split(...)[0]) has no dotted-port form —
			// materialize the value as a Synthetic code shim, then
			// serialize its output (same P0 fallback as flattenConcat).
			var verr error
			rr, verr = g.emitExprCode(prevIDs, firstPort, false, a2)
			if verr != nil {
				return nil, &UnsupportedError{Construct: fmt.Sprintf(
					"json.stringify argument %T is not a resolvable value (assign the value to a variable first, or wrap it in a `foreign code` block)", a2)}
			}
		}
		ref = rr
	}
	// B9a: json.stringify 输入端口按实际 ref 类型声明（json.dumps 接受任意
	// JSON 值，但平台端口没有 any——证明不了类型就报错，禁止伪造）。
	typ, terr := g.refType(ref, arg)
	if terr != nil {
		return nil, &UnsupportedError{Construct: fmt.Sprintf("json.stringify: %s", terr.Error())}
	}
	return &CodeRun{
		ID: id,
		// 形态 = shim 契约的**规范拼写**（`docs/PYTHON-SHIM-TRANSLATION-CONTRACT.md`
		// §1「import 自足：stdlib 依赖在 `def main` 之前，绝不放在 def 之后」+
		// `# ncoda:shim 1` 单源标记，见同文件其余 shim 构造点与
		// `pkg/expr/shim_test.go::TestShimEvalJSONDumps` 的同一拼写）。
		// 两点都不可省（一手实证 X109/S96/S12/S19/S44/V021/V024/7598）：
		//   ① 漏 marker ⇒ raising 认不出自建 shim（它只认标记）⇒ 读回被当成
		//      **用户手写 foreign code**（编译器产物伪装成用户代码）
		//      ⇒ 原语句 `json.stringify(...)` 丢失；
		//   ② import 放函数体内 ⇒ 即使带标记也**不可逆**（`tryReverseShim` 要求
		//      import 在模块级、body 恰一条 return）⇒ 同上，且 `# ncoda:shim`
		//      会随透传漏进 ncoda 文本（违反「ncoda 层不存在 shim」不变量）。
		Code: ShimMarker + "import json\ndef main(arg1) -> dict:\n" +
			"    return {\"result\": json.dumps(arg1, ensure_ascii=False)}",
		Language:  "python",
		Inputs:    []Source{{Path: []string{"arg1"}, BlockID: ref.nodeID, Name: ref.output}},
		Types:     map[string]*Type{"arg1": typ},
		OutTypes:  map[string]*Type{"result": T("string")},
		OutOrder:  []string{"result"},
		Synthetic: true,
	}, nil
}
