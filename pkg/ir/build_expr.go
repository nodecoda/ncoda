// IR builder — 按语句/节点族拆分的文件之一（Tier-2 review #9:拆文件不拆包,
// diff 局部化）。同一包内符号共享;本文件族的归属见各函数注释。
package ir

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/pyast"
	"github.com/nodecoda/ncoda/pkg/pygen"
	"github.com/nodecoda/ncoda/pkg/types"
)

// buildArray materializes an array literal as a CodeRunner that returns the
// bound element values ([a, b] → code([a, b])).
// renderLiteralPython renders a nested object/array literal as a Python
// literal expression, binding resolvable identifier/field-access leaves as
// input parameters (G8: `[{"fields": payload}]` projection composition).
// Scalars render inline; unsupported constructs fall through to the caller's
// error path (the array materializer degrades such elements to None, keeping
// the existing fallback behavior for non-projection shapes).
// hoistUserCalls rewrites a value expression so every user-function call
// subtree is materialized through the producer pipeline (emitValueExpr →
// inlineCall) and the call site becomes a bound variable reference. §5.1:
// the combinator renderer translates composition, never invocation — hoist
// is the unlock for invocation inside literals/conditions, performed at the
// entry points (emitIf / emitValueExpr literals / buildStringify), NOT in
// the renderers. Returns expr unchanged (same pointer) when nothing was
// hoisted, so pass-0 native/materialize marks stay valid. Declarative
// primitives (coalesce / json.stringify / std.* / list ops) and unknown
// calls are left for their renderers.
func (g *Builder) hoistUserCalls(expr ast.Expr, prevIDs []string, firstPort string) (ast.Expr, error) {
	switch x := expr.(type) {
	case *ast.OperationPolicyExpr:
		// A policy-wrapped operation (`op(...) with retry/timeout/default`) is
		// an operation like any other: wherever this materialization entry
		// meets one — json.stringify argument, template interpolation, map /
		// array element, condition operand — it must become a preceding
		// producer node and the call site a bound variable, exactly like the
		// bare operation case below. The node is built by the SAME producer as
		// the let / statement carriers (materializePolicyOperation): the policy
		// suffix is part of the call, never a third shape enumeration.
		id := g.nextID()
		kind, output, perr := g.materializePolicyOperation(id, x, prevIDs, firstPort)
		if perr != nil {
			return nil, perr
		}
		vname := g.freshHoistName()
		g.varToOutput[vname] = varRef{nodeID: id, output: output, kind: kind}
		return ast.NewIdentExpr(vname), nil
	case *ast.CallExpr:
		name := calleeName(x.Func())
		if g.funcs[name] != nil {
			// Materialize the call through the producer pipeline. We call
			// inlineCall directly (not emitValueExpr) so a hoist at the top
			// of emitValueExpr cannot recurse into another hoist of the
			// same call (3AC: the call becomes a preceding producer; the
			// call site becomes a bound variable).
			_, _, ref, err := g.inlineCall(g.funcs[name], x.Args(), prevIDs, firstPort)
			if err != nil {
				return nil, err
			}
			if ref.nodeID == "" {
				return nil, &UnsupportedError{Construct: fmt.Sprintf("function %q produces no value", name)}
			}
			vname := g.freshHoistName()
			g.varToOutput[vname] = ref
			return ast.NewIdentExpr(vname), nil
		}
		// §5.3 stage C: bare operation calls (llm/http/knowledge/split/
		// extract/qualified tool) in expressions are materialized through
		// the shared producer pipeline (materializeCall) — the same node
		// the top-level let path produces. The call site becomes a bound
		// variable; field access on it resolves via dotted output ports.
		if g.isMaterializableOperation(x) {
			_, ref, merr := g.materializeCall(x, prevIDs, firstPort)
			if merr != nil {
				return nil, merr
			}
			vname := g.freshHoistName()
			g.varToOutput[vname] = ref
			return ast.NewIdentExpr(vname), nil
		}
		// Declarative primitive / unknown call (coalesce, json.stringify,
		// std.*, list ops, ...): the call itself stays for its renderer, but
		// its ARGUMENTS are recursed so a user-fn call inside (e.g.
		// `coalesce(helper(o), "")`) still gets hoisted — the parameter is a
		// producer boundary even when the outer shape is declarative (§5.1).
		args, changed, aerr := g.hoistCallArgs(x.Args(), prevIDs, firstPort)
		if aerr != nil {
			return nil, aerr
		}
		if !changed {
			return expr, nil
		}
		return ast.NewCallExpr(x.Func(), x.TypeArgs(), args), nil
	case *ast.BinaryExpr:
		left, err := g.hoistUserCalls(x.Left(), prevIDs, firstPort)
		if err != nil {
			return nil, err
		}
		right, err := g.hoistUserCalls(x.Right(), prevIDs, firstPort)
		if err != nil {
			return nil, err
		}
		if left == x.Left() && right == x.Right() {
			return expr, nil
		}
		return ast.NewBinaryExpr(x.Op(), left, right), nil
	case *ast.UnaryExpr:
		op, err := g.hoistUserCalls(x.Operand(), prevIDs, firstPort)
		if err != nil {
			return nil, err
		}
		if op == x.Operand() {
			return expr, nil
		}
		return ast.NewUnaryExpr(x.Op(), op), nil
	case *ast.TernaryExpr:
		c, err := g.hoistUserCalls(x.Condition(), prevIDs, firstPort)
		if err != nil {
			return nil, err
		}
		t, err := g.hoistUserCalls(x.ThenVal(), prevIDs, firstPort)
		if err != nil {
			return nil, err
		}
		el, err := g.hoistUserCalls(x.ElseVal(), prevIDs, firstPort)
		if err != nil {
			return nil, err
		}
		if c == x.Condition() && t == x.ThenVal() && el == x.ElseVal() {
			return expr, nil
		}
		return ast.NewTernaryExpr(c, t, el), nil
	case *ast.FieldAccessExpr:
		obj, err := g.hoistUserCalls(x.Obj(), prevIDs, firstPort)
		if err != nil {
			return nil, err
		}
		if obj == x.Obj() {
			return expr, nil
		}
		return ast.NewFieldAccessExpr(obj, x.FieldName()), nil
	case *ast.IndexExpr:
		// Constant-0 array element access (rec.result[0].output): recurse
		// the indexed object so an operation call inside the chain
		// (knowledge(...).result[0].output) is materialized; the index
		// itself is a scalar and left as-is. §5.3 + E5-b.
		obj, err := g.hoistUserCalls(x.Obj(), prevIDs, firstPort)
		if err != nil {
			return nil, err
		}
		idx, err := g.hoistUserCalls(x.Index(), prevIDs, firstPort)
		if err != nil {
			return nil, err
		}
		if obj == x.Obj() && idx == x.Index() {
			return expr, nil
		}
		return ast.NewIndexExpr(obj, idx), nil
	case *ast.TemplateString:
		parts := x.Parts()
		changed := false
		rebuilt := make([]ast.TemplatePart, len(parts))
		for i, part := range parts {
			if part.Expr == nil {
				rebuilt[i] = part
				continue
			}
			h, err := g.hoistUserCalls(part.Expr, prevIDs, firstPort)
			if err != nil {
				return nil, err
			}
			rebuilt[i] = ast.TemplatePart{Text: part.Text, Expr: h}
			if h != part.Expr {
				changed = true
			}
		}
		if !changed {
			return expr, nil
		}
		return ast.NewTemplateString(rebuilt), nil
	case *ast.MapLit:
		entries := x.Entries()
		changed := false
		rebuilt := make([]ast.MapEntry, len(entries))
		for i, entry := range entries {
			v, err := g.hoistUserCalls(entry.Value, prevIDs, firstPort)
			if err != nil {
				return nil, err
			}
			rebuilt[i] = ast.MapEntry{Key: entry.Key, Value: v}
			if v != entry.Value {
				changed = true
			}
		}
		if !changed {
			return expr, nil
		}
		return ast.NewMapLit(rebuilt), nil
	case *ast.ArrayLit:
		elements := x.Elements()
		changed := false
		rebuilt := make([]ast.Expr, len(elements))
		for i, el := range elements {
			h, err := g.hoistUserCalls(el, prevIDs, firstPort)
			if err != nil {
				return nil, err
			}
			rebuilt[i] = h
			if h != el {
				changed = true
			}
		}
		if !changed {
			return expr, nil
		}
		return ast.NewArrayLit(rebuilt), nil
	default:
		return expr, nil
	}
}

// hoistCallArgs recurses hoistUserCalls over a call's argument list,
// returning the rebuilt args plus a changed flag (same-pointer when
// untouched, so pass-0 marks and node-id allocation stay stable).
func (g *Builder) hoistCallArgs(args []ast.Expr, prevIDs []string, firstPort string) ([]ast.Expr, bool, error) {
	changed := false
	rebuilt := make([]ast.Expr, len(args))
	for i, a := range args {
		h, err := g.hoistUserCalls(a, prevIDs, firstPort)
		if err != nil {
			return nil, false, err
		}
		rebuilt[i] = h
		if h != a {
			changed = true
		}
	}
	if !changed {
		return args, false, nil
	}
	return rebuilt, true, nil
}

// isMaterializableOperation reports whether a call is a platform operation
// the shared producer pipeline can materialize (builtin operations plus
// qualified tool calls) — §5.3: these are hoisted into nodes when they
// appear inside expressions.
func (g *Builder) isMaterializableOperation(call *ast.CallExpr) bool {
	name := calleeName(call.Func())
	switch name {
	case "llm", "http", "knowledge", "split", "extract":
		return true
		// json.parse is deliberately NOT listed: an unused `json.parse<T>(x);`
		// statement is pure (no effects) and must stay node-free. Its value
		// positions are handled by emitValueExpr's explicit branch (R-216).
	}
	return g.isQualifiedToolCall(call)
}

// freshHoistName allocates a compiler-generated variable name for a hoisted
// call result, guaranteed not to collide with user bindings in varToOutput.
func (g *Builder) freshHoistName() string {
	i := 1
	for {
		name := fmt.Sprintf("_h%d", i)
		if _, ok := g.varToOutput[name]; !ok {
			return name
		}
		i++
	}
}

func renderLiteralPython(e ast.Expr, g *Builder, bindings *[]textBinding) (string, error) {
	switch x := e.(type) {
	case *ast.MapLit:
		entries := canonicalMapEntries(x.Entries())
		parts := make([]string, 0, len(entries))
		for _, entry := range entries {
			key, ok := staticMapKeyName(entry.Key)
			if !ok {
				return "", dynamicMapKeyError("projection map literal", entry.Key)
			}
			val, err := renderLiteralPython(entry.Value, g, bindings)
			if err != nil {
				return "", err
			}
			keyLit, _ := json.Marshal(key)
			parts = append(parts, fmt.Sprintf("%s: %s", keyLit, val))
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	case *ast.ArrayLit:
		parts := make([]string, 0, len(x.Elements()))
		for _, el := range x.Elements() {
			val, err := renderLiteralPython(el, g, bindings)
			if err != nil {
				return "", err
			}
			parts = append(parts, val)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case *ast.IdentExpr:
		return bindConditionPython(x.Name(), x, g, bindings)
	case *ast.FieldAccessExpr:
		return renderFieldAccessPython(x, g, bindings)
	case *ast.StringLit:
		return pythonJSONString(x.Value()), nil
	case *ast.IntLit:
		return strconv.FormatInt(x.Value(), 10), nil
	case *ast.FloatLit:
		return pyast.FloatLiteralText(x.Value()), nil
	case *ast.BoolLit:
		if x.Value() {
			return "True", nil
		}
		return "False", nil
	case *ast.NullLit:
		return "None", nil
	}
	// scalar-expression fallback (binary/ternary/template reuse the
	// condition renderer); unsupported shapes (e.g. workflow-op or
	// user-function calls) error up — callers must surface the error, never
	// degrade to None (P4).
	return renderConditionPython(e, g, bindings)
}

// arrayElemParamName 定数组字面量元素的 shim 参数名。
//
// **一处定义**：`buildArray`（legacy 文本路径）与 `lowerArrayPyast`（pyast 路径）
// 是同一件事的两次实现，参数名规则必须同源 —— 两处各写一份 = 同一事实两份权威。
//
// 规则（= C 轴命名档位在 shim 参数名上的投影，见
// `docs/five-step/spec/J-join-canonical-form.md` §42.3；不另立档位表）：
// 参数名是**值身份**的函数，不是源拼写的函数。
//
//	① 可解析的引用 ⇒ **被解析引用的端口名**（= 反编译侧回吐的名字，同源）
//	② 端口名不可用 ⇒ 位置名 `argN`（自造名，兜底；唯一允许的末档）
//
// 「元素是裸标识符 ⇒ 用源名」**不是**一档（2026-09-26 删除，R-104）：源名是
// 拼写，不是身份。同一份产物读出成 ncoda 后，数组元素从 `[x1.text, x2.text]`
// （成员访问）变成 `[v1, v2]`（提升后的裸标识符）——两个 ref 仍是
// `(100002, text)` / `(100003, text)`（**同一个值身份**），旧头档却把元素 0 铸成
// `v1`、元素 1 铸成 `v2` ⇒ `ARTIFACT_DRIFT/PARAM@r2`（examples 面一手 =
// `examples/coze-reference/x001_llm_chain/source.ncoda`，节点数已稳定 5→5）。
// 身份不变 ⇒ 名字必须不变；名字能随拼写变，就说明它挂在拼写上。
//
// 为什么 ① 不能再落回位置名：位置名是**拼写相关**的名字。`[r1.slot]`（成员路径）
// 与 `[fp1]` 折成同一个值（记录整值折叠，§42.3 A 轴），却一个铸 `arg1`、一个铸
// `fp1` ⇒「同一个意思两种拼写两种产物」⇒ `C∘R` 无不动点（R-43 §42.4 E3 的 `s3`
// 残差，实测 = `ARTIFACT_DRIFT/PARAM@r2`，节点数已稳定 5→5）。
//
// 「不可用」= 空 / 非标识符（含整值标记 `@whole`）/ Python 关键字 / 已在参数表里
// （同名两参数会让 shim 的按名投影失去身份）；② 保证回落方向永远是「更弱的
// 名字」，不是「更错的名字」。
func arrayElemParamName(i int, ref varRef, isRef bool, used []string) string {
	fallback := fmt.Sprintf("arg%d", i+1)
	if !isRef {
		return fallback
	}
	n := ref.output
	if !isShimParamName(n) || containsParam(used, n) {
		return fallback
	}
	return n
}

// isShimParamName 报告 n 能否作为 shim 的形参名（合法标识符 ∧ 非整值标记 ∧
// 非 Python 关键字）。保守白名单：任一不满足 ⇒ 调用方回落位置名。
func isShimParamName(n string) bool {
	if n == "" || n == wholeValueMarker {
		return false
	}
	for i, r := range n {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return !shimReservedWords[n]
}

// shimReservedWords = Python 关键字（形参名逐字进 code 文本，形参名是关键字的
// shim 不是合法 python）。与 `pkg/parser/expr.go` 的 pythonKeywords 同集合
// —— 此处只守形参名这一个入口，不做跨包依赖。
var shimReservedWords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true,
	"assert": true, "async": true, "await": true, "break": true, "class": true,
	"continue": true, "def": true, "del": true, "elif": true, "else": true,
	"except": true, "finally": true, "for": true, "from": true, "global": true,
	"if": true, "import": true, "in": true, "is": true, "lambda": true,
	"nonlocal": true, "not": true, "or": true, "pass": true, "raise": true,
	"return": true, "try": true, "while": true, "with": true, "yield": true,
}

// containsParam reports whether params already declares name (dedupe for
// literal-bound leaves that collide across array elements).
func containsParam(params []string, name string) bool {
	for _, p := range params {
		if p == name {
			return true
		}
	}
	return false
}

func (g *Builder) buildArray(id string, arr *ast.ArrayLit) (Node, error) {
	// P5.9: pyast path when every element lowers byte-identically (ref
	// elements bind by name; literal elements via lowerLiteralPyast, which
	// rejects multi-key dicts and quote-unsafe keys); the legacy template
	// keeps today's exact bytes otherwise.
	if node, ok, err := g.lowerArrayPyast(id, arr); err != nil {
		return nil, &UnsupportedError{Construct: fmt.Sprintf(
			"array element: %s (assign the value to a variable first, or write foreign code)",
			constructOf(err))}
	} else if ok {
		return node, nil
	}
	params := []string{}
	values := []string{}
	sources := []Source{}
	types := map[string]*Type{}
	for i, el := range arr.Elements() {
		elRef, hasRef := g.resolveValueRef(el)
		name := arrayElemParamName(i, elRef, hasRef, params)
		if ref, ok := elRef, hasRef; ok {
			params = append(params, name)
			values = append(values, name)
			sources = append(sources, Source{Path: []string{name}, BlockID: ref.nodeID, Name: ref.output})
			et, eerr := g.refType(ref, el)
			if eerr != nil {
				return nil, &UnsupportedError{Construct: fmt.Sprintf("array element: %s", eerr.Error())}
			}
			types[name] = et
			continue
		}
		// nested object/array literal elements: render the Python literal and
		// bind its resolvable leaves as input parameters (G8 projection
		// composition, e.g. [{"fields": payload}]). A non-renderable element
		// is an explicit error, never a silent None (P4).
		var bindings []textBinding
		rendered, rerr := renderLiteralPython(el, g, &bindings)
		if rerr != nil {
			return nil, &UnsupportedError{Construct: fmt.Sprintf(
				"array element: %s (assign the value to a variable first, or write foreign code)",
				constructOf(rerr))}
		}
		values = append(values, rendered)
		for _, b := range bindings {
			if containsParam(params, b.name) {
				continue
			}
			params = append(params, b.name)
			sources = append(sources, Source{Path: []string{b.name}, BlockID: b.ref.nodeID, Name: b.ref.output})
			bt, berr := g.refType(b.ref, nil)
			if berr != nil {
				return nil, &UnsupportedError{Construct: fmt.Sprintf("array element: %s", berr.Error())}
			}
			types[b.name] = bt
		}
	}
	code := ShimMarker + fmt.Sprintf("def main(%s) -> dict:\n    return {\"result\": [%s]}",
		strings.Join(params, ", "), strings.Join(values, ", "))
	return &CodeRun{
		ID:        id,
		Code:      code,
		Language:  "python",
		Inputs:    sources,
		Types:     types,
		OutTypes:  map[string]*Type{"result": g.arrayLiteralShimType(arr)},
		OutOrder:  []string{"result"},
		Synthetic: true,
	}, nil
}

// lowerArrayPyast lowers an array literal to the typed-array shim
// (`def main(...) -> dict: return {"result": [...]}`) through the pyast
// path, mirroring the legacy element loop (argN/ident naming for ref
// elements, literal lowering + binding dedupe for the rest). ok=false means
// at least one element is not provably byte-identical — the caller falls
// back to the legacy string template.
func (g *Builder) lowerArrayPyast(id string, arr *ast.ArrayLit) (Node, bool, error) {
	params := []string{}
	elts := make([]pyast.Expr, 0, len(arr.Elements()))
	sources := []Source{}
	types := map[string]*Type{}
	bindings := []textBinding{}
	for i, el := range arr.Elements() {
		elRef, hasRef := g.resolveValueRef(el)
		name := arrayElemParamName(i, elRef, hasRef, params)
		if ref, ok := elRef, hasRef; ok {
			params = append(params, name)
			elts = append(elts, &pyast.NameExpr{Name: name})
			sources = append(sources, Source{Path: []string{name}, BlockID: ref.nodeID, Name: ref.output})
			et, eerr := g.refType(ref, el)
			if eerr != nil {
				return nil, false, eerr
			}
			types[name] = et
			continue
		}
		pa, ok, err := lowerLiteralPyast(el, g, &bindings)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		elts = append(elts, pa)
		for _, b := range bindings {
			if containsParam(params, b.name) {
				continue
			}
			params = append(params, b.name)
			sources = append(sources, Source{Path: []string{b.name}, BlockID: b.ref.nodeID, Name: b.ref.output})
			bt, berr := g.refType(b.ref, nil)
			if berr != nil {
				return nil, false, berr
			}
			types[b.name] = bt
		}
	}
	code := wrapShim(namedParams(params), &pyast.ListExpr{Elts: elts})
	node := &CodeRun{
		ID:        id,
		Code:      code,
		Language:  "python",
		Inputs:    sources,
		Types:     types,
		OutTypes:  map[string]*Type{"result": g.arrayLiteralShimType(arr)},
		OutOrder:  []string{"result"},
		Synthetic: true,
	}
	// 零值标注：空数组是该 list 类型的零值（非空数组是真实值）。
	if len(arr.Elements()) == 0 {
		node.ZeroOf = node.OutTypes["result"]
	}
	return node, true, nil
}

// buildConst materializes a scalar constant as a CodeRunner returning it.
func (g *Builder) buildConst(id, value string) Node {
	lit, _ := json.Marshal(value)
	return &CodeRun{
		ID:        id,
		Code:      ShimMarker + fmt.Sprintf("def main() -> dict:\n    return {\"result\": %s}", lit),
		Language:  "python",
		Synthetic: true,
	}
}

// buildText builds a TextProcess concat node: template plus ref bindings.
func (g *Builder) buildText(id, template string, refs []textBinding) Node {
	sources := make([]Source, 0, len(refs))
	for _, b := range refs {
		sources = append(sources, Source{Path: []string{b.name}, BlockID: b.ref.nodeID, Name: b.ref.output})
	}
	return &TextProcess{
		ID:       id,
		Method:   "concat",
		Template: template,
		Inputs:   sources,
	}
}

// ── value expressions ────────────────────────────────────────────────────

// emitValueExpr resolves a value-producing expression to a ref,
// materializing a TextProcess node when the value is a literal or a string
// concatenation (no bare-value nodes).
func (g *Builder) emitValueExpr(prevIDs []string, firstPort string, portOnLast bool, expr ast.Expr) (varRef, error) {
	// hoist user-fn calls at the value-expression entry (covers return /
	// template interpolation / ternary / unary / binary operands that reach
	// this function), then resolve the rewritten expression (a hoisted call
	// is now a bound ident). §5.1/5.2. Top-level calls (`return helper(s)`)
	// are NOT hoisted — they keep the dedicated CallExpr branch below.
	if _, isCall := expr.(*ast.CallExpr); !isCall {
		hoisted, herr := g.hoistUserCalls(expr, prevIDs, firstPort)
		if herr != nil {
			return varRef{}, herr
		}
		expr = hoisted
	}
	if ref, ok := g.resolveValueRef(expr); ok {
		return ref, nil
	}
	switch e := expr.(type) {
	case *ast.IdentExpr:
		// 字面量绑定变量（`let v = 1` / `let ok = true`）在**值位置**物化常量
		// producer：litVars 只存值、没有 producer 地址（`let` 常量不建节点），
		// 而值位置需要可寻址的 ref。与条件操作数（`operandOf` /
		// `bindConditionPython`）和提升变量的字面量写（F1 `literalWriteRef`）
		// 同一条规则：**字面量绑定在需要地址的位置就地物化**。
		// 此前该形态直接落到函数末的 `value expression *ast.IdentExpr` 硬错误——
		// `let v1 = 1; return v1;` 首编 ADAPTER_ERROR，而 `return v1 + 0` 能过，
		// 不对称即出生于此（R8 全链的最后一腿：反编译产 `return <折叠后字面量名>`）。
		if lv, ok := g.litVars[e.Name()]; ok {
			return g.emitLitConstShim(lv), nil
		}
		return varRef{}, &UnsupportedError{Construct: fmt.Sprintf("value expression %T", expr)}
	case *ast.StringLit:
		return g.emitTextConst(prevIDs, firstPort, portOnLast, e.Value())
	case *ast.IntLit, *ast.FloatLit, *ast.BoolLit:
		// 标量字面量在**值位置**物化 = 与可写位置（F1 `literalWriteRef` /
		// 提升变量的字面量写）**同一条尺子**：类型化常量 shim
		// （`emitLitConstShim`：IntExpr/FloatExpr/BoolExpr + OutTypes{result:T}）。
		//
		// 旧实现走 `buildConst`——它把字面量 json.Marshal 成**字符串**
		// （`return {"result": "7"}`）且不声明任何 OutTypes ⇒ 产物自相矛盾：
		// 代码返回的字典键是 `result`、节点却只声明平台默认口 `output`
		// （该口在平台上永远是 null），类型也从 integer 退化成 string。抬回
		// 侧读不出 `output` 的权威 schema（RS-NORM-004）⇒ 合成记录访问
		// `v1.$output`，且这个脚手架第二轮才被清掉 = 一轮不动点破裂
		// （最小复现 `function main() -> int { return 7; }`，无循环、与 M17
		// 无关，故这是既有的字面量物化族缺陷，本族只是把它照亮）。
		lv := litVar{typ: "string"}
		switch t := e.(type) {
		case *ast.IntLit:
			lv.value, lv.typ = strconv.FormatInt(t.Value(), 10), "integer"
		case *ast.FloatLit:
			lv.value, lv.typ = pyast.FloatLiteralText(t.Value()), "number"
		case *ast.BoolLit:
			lv.value, lv.typ = "false", "boolean"
			if t.Value() {
				lv.value = "true"
			}
		}
		return g.emitLitConstShim(lv), nil
	case *ast.FieldAccessExpr:
		if value, ok := g.enumMemberValue(e); ok {
			// 枚举成员 = 它的**线值字符串字面量**（R-62）⇒ 与书写 `"<线值>"` 走
			// **同一条**物化路径（StringLit 分支的 `emitTextConst`）。自造常量
			// code shim 会让同一常量有两种产物 ⇒ 一次往返后拓扑变化（I1b）。
			return g.emitTextConst(prevIDs, firstPort, portOnLast, value)
		}
		// G12-2: a json.parse<array<T>> element chain (items[0].field) has
		// no dotted-port form — materialize it as a guarded extraction
		// shim (let/map/template paths already do; a bare value position
		// such as `return items[0].title` reaches here).
		return g.emitExprCode(prevIDs, firstPort, portOnLast, e)
	case *ast.IndexExpr:
		// G12-2: element access on a json.parse<array<T>> variable (scalar
		// or record element) materializes as a guarded extraction shim.
		return g.emitExprCode(prevIDs, firstPort, portOnLast, e)
	case *ast.TemplateString:
		return g.emitTextTemplate(prevIDs, firstPort, portOnLast, e)
	case *ast.BinaryExpr:
		return g.emitBinaryValue(prevIDs, firstPort, portOnLast, e)
	case *ast.TernaryExpr:
		// 裸值位置（return / 实参 / 模板插值）：没有命名目标 ⇒ 槽名留空（U6 只
		// 给「有命名目标」的位置命名；无目标名不是缺陷，是合法的无名汇聚）。
		ids, err := g.emitTernaryJunction(prevIDs, firstPort, e.Condition(), e.ThenVal(), e.ElseVal(), "")
		if err != nil {
			return varRef{}, err
		}
		// 值位置三元 = junction 的整值（组形状由值类型决定，见 junctionValueRef）。
		if len(ids) == 0 {
			return varRef{}, &UnsupportedError{Construct: "ternary value position: junction produced no value"}
		}
		return g.junctionValueRefOf(ids[0])
	case *ast.UnaryExpr:
		return g.emitExprCode(prevIDs, firstPort, portOnLast, e)
	case *ast.MapLit:
		e2, herr := g.hoistUserCalls(e, prevIDs, firstPort)
		if herr != nil {
			return varRef{}, herr
		}
		id := g.nextID()
		node, merr := g.buildMap(id, e2.(*ast.MapLit))
		if merr != nil {
			return varRef{}, merr
		}
		g.add(node)
		return varRef{nodeID: id, output: "result", kind: "code"}, nil
	case *ast.ArrayLit:
		e2, herr := g.hoistUserCalls(e, prevIDs, firstPort)
		if herr != nil {
			return varRef{}, herr
		}
		id := g.nextID()
		node, aerr := g.buildArray(id, e2.(*ast.ArrayLit))
		if aerr != nil {
			return varRef{}, aerr
		}
		g.add(node)
		return varRef{nodeID: id, output: "result", kind: "code"}, nil
	case *ast.CallExpr:
		if calleeName(e.Func()) == "file" {
			// file(x) 值位置透传：可解析参数在 emitValueExpr 顶部已被
			// resolveValueRef 解包; 到这里只剩不可解析参数（file(a+b) 等）
			// —— 物化参数表达式后原样透传其 ref（零节点）。
			if len(e.Args()) != 1 {
				return varRef{}, &UnsupportedError{Construct: "call \"file\" expects 1 argument"}
			}
			if ref, ok := g.resolveValueRef(e.Args()[0]); ok {
				return ref, nil
			}
			return g.emitValueExpr(prevIDs, firstPort, portOnLast, e.Args()[0])
		}
		if isListOpName(calleeName(e.Func())) {
			ref, lerr := g.emitListOp(e, prevIDs, firstPort)
			if lerr != nil {
				return varRef{}, lerr
			}
			g.recordPlacement(ref.nodeID, placementConstruct("list-op", calleeName(e.Func())), PlacementShim, placementReason(PlacementCauseNoNativeNode, "lambda op materializes as python (no native collection node)"))
			return ref, nil
		}
		if isCalcBuiltin(calleeName(e.Func())) {
			if name := calleeName(e.Func()); name == "join" {
				if _, ok := g.buildJoinNative("", e); ok {
					id := g.nextID()
					node, _ := g.buildJoinNative(id, e)
					g.add(node)
					g.recordPlacement(id, placementConstruct("builtin", "join"), PlacementNative, placementReason(PlacementCauseCapability, "TextProcessor concat array<string> + static separator"))
					return varRef{nodeID: id, output: "output", kind: "text"}, nil
				}
			}
			if name := calleeName(e.Func()); name == "split" {
				if _, err := g.buildSplit("", e); err == nil {
					id := g.nextID()
					node, _ := g.buildSplit(id, e)
					g.add(node)
					g.recordPlacement(id, placementConstruct("builtin", "split"), PlacementNative, placementReason(PlacementCauseCapability, "workflow operation: native text node"))
					return varRef{nodeID: id, output: "output", kind: "text"}, nil
				}
			}
			// G10/G13/G14: pure calculation builtin (len/join/trim/to_string/
			// int/float/contains/split) in a bare value position (e.g.
			// `return join(out, "\n")`) materializes as a Synthetic code
			// shim with the builtin's real output type.
			ref, cerr := g.emitExprCodeOut(prevIDs, firstPort, portOnLast, e, g.calcBuiltinOutTypeFor(calleeName(e.Func()), e))
			if cerr != nil {
				return varRef{}, cerr
			}
			g.recordPlacement(ref.nodeID, placementConstruct("calc", calleeName(e.Func())), PlacementShim, placementReason(PlacementCauseNoNativeNode, "no native node for generic form"))
			return ref, nil
		}
		if calleeName(e.Func()) == "json.parse" {
			// R-216: the same primitive must lower in every value position,
			// not just when bound to a let. materializeCall is the single
			// producer path the let / statement lanes already use, so the
			// shim shape (record vs array, JSONSchema, placement record)
			// cannot drift between positions.
			_, ref, merr := g.materializeCall(e, prevIDs, firstPort)
			if merr != nil {
				return varRef{}, merr
			}
			return ref, nil
		}
		if calleeName(e.Func()) == "json.stringify" {
			id := g.nextID()
			n, jerr := g.buildStringify(id, e, prevIDs, firstPort)
			if jerr != nil {
				return varRef{}, jerr
			}
			g.add(n)
			g.recordJSONBuiltinPlacement(id, "stringify", n)
			return varRef{nodeID: id, output: "result", kind: "json"}, nil
		}
		if calleeName(e.Func()) == "run" {
			// @mode agent entry call in value position (`return run(q)`).
			if g.prog.Program.Mode() != "agent" {
				return varRef{}, &UnsupportedError{Construct: fmt.Sprintf("value call %q outside @mode agent", "run")}
			}
			id := g.nextID()
			ac, aerr := g.buildAgentRun(id, e)
			if aerr != nil {
				return varRef{}, aerr
			}
			g.add(ac)
			g.recordPlacement(id, placementConstruct("agent-mode", "run"), PlacementNative, placementReason(PlacementCauseCapability, "agent-mode entry: @agent declaration → native agent node"))
			return varRef{nodeID: id, output: "output", kind: "agent"}, nil
		}
		if calleeName(e.Func()) == "conversation_history" {
			// @mode advanced-chat entry call in value position
			// (`let h = conversation_history()`).
			if g.prog.Program.Mode() != "advanced-chat" {
				return varRef{}, &UnsupportedError{Construct: fmt.Sprintf("value call %q outside @mode advanced-chat", "conversation_history")}
			}
			id := g.nextID()
			g.add(&HistoryCall{ID: id})
			g.recordPlacement(id, placementConstruct("advanced-chat", "history"), PlacementNative, placementReason(PlacementCauseCapability, "advanced-chat entry: conversation history → native conversation node"))
			return varRef{nodeID: id, output: "history", kind: "conversation"}, nil
		}
		if strings.HasPrefix(calleeName(e.Func()), "std.") {
			_, ref, serr := g.emitStdlibCall(e, prevIDs, firstPort)
			if serr != nil {
				return varRef{}, serr
			}
			return ref, nil
		}
		if fn, ok := g.funcs[calleeName(e.Func())]; ok {
			_, _, ref, ierr := g.inlineCall(fn, e.Args(), prevIDs, firstPort)
			if ierr != nil {
				return varRef{}, ierr
			}
			if ref.nodeID == "" {
				return varRef{}, &UnsupportedError{Construct: fmt.Sprintf("function %q produces no value", calleeName(e.Func()))}
			}
			return ref, nil
		}
		return varRef{}, &UnsupportedError{Construct: fmt.Sprintf("value call %q", calleeName(e.Func()))}
	}
	return varRef{}, &UnsupportedError{Construct: fmt.Sprintf("value expression %T", expr)}
}

// emitTextConst materializes a TextProcess concat node emitting a literal.
func (g *Builder) emitTextConst(prevIDs []string, firstPort string, portOnLast bool, value string) (varRef, error) {
	// Concat requires a non-empty template; the empty string lowers to a
	// CodeRunner constant instead of a TextProcessor.
	if value == "" {
		// 空串常量按 (block, code) 池化：同一直线区 N 个 `v = ""` 提升初始化
		// 只物化一个纯常量 code 节点（无输入、同值，复用不出块 ⇒ 生产者恒在
		// 消费者上游，P2 不变）。
		if ref, ok := g.zeroStringShim(g.curBlock()); ok {
			return ref, nil
		}
		id := g.nextID()
		g.add(&CodeRun{
			ID:        id,
			Code:      wrapShim(nil, &pyast.StringExpr{}),
			Language:  "python",
			OutTypes:  map[string]*Type{"result": T("string")},
			OutOrder:  []string{"result"},
			Synthetic: true,
			ZeroOf:    T("string"),
		})
		return varRef{nodeID: id, output: "result", kind: "text"}, nil
	}
	// **native-first（P0'）**：concat 节点是目标的**能力**，不是语言的必然 ——
	// 有原生 TextProcess 的目标（`caps.TextConcat`，如 coze / coze-biz）才对非空
	// 常量物化 text 节点；没有该承载面的目标（参考目标：文本也落 code 节点）走常量
	// shim —— 与该文件 `emitBinaryValue` 的字符串 `+` 同一条尺子（同处唯一 gate）。
	//
	// 缺这道 gate 的后果（2026-09-25 一手）：`let v1 = "abc"; return { "k": v1 };`
	// 在参考目标上产出「无 `# ncoda:shim 1` 标记」的 code 节点 —— 自家载体丢标记
	// ⇒ 反向读回时被当成用户 foreign code（而内联拼写走通用表达式 shim，带标记）
	// ⇒ 同一意思两种产物、且其中一个不可逆。
	if !g.nativeTextConcat() {
		return g.emitLitConstShim(litVar{typ: "string", value: value}), nil
	}
	id := g.nextID()
	g.add(g.buildText(id, value, nil))
	return varRef{nodeID: id, output: "output", kind: "text"}, nil
}

// nativeTextConcat 报告目标是否有原生文本拼接节点（emitTextConst /
// emitTextConcat 的**唯一** gate）。口径与 emitBinaryValue 一致：能力表缺席
// （nil，仅见于直接构造 IR 的单元测试）按**有**处理 —— 表是对**已知目标**的
// 声明，不是「未提供即没有」。
func (g *Builder) nativeTextConcat() bool {
	return g.caps == nil || g.caps.TextConcat
}

// emitBinaryValue lowers a binary value expression native-first (P0'):
// string `+` on a target with a native text-concat node (TextProcess)
// maps natively in pass 1; everything else (arithmetic, logic, targets
// without the node) materializes as a Synthetic code shim in pass 2.
func (g *Builder) emitBinaryValue(prevIDs []string, firstPort string, portOnLast bool, e *ast.BinaryExpr) (varRef, error) {
	if e.Op() == "+" && g.caps != nil && g.caps.TextConcat && g.isStringConcat(e) {
		ref, err := g.emitTextConcat(prevIDs, firstPort, portOnLast, e)
		// 「拼接只剩一段」被折成该段本身时**没有**物化任何节点（落点跟着变成
		// 上游生产者）⇒ 不得给上游节点记一条 native concat 的落点（记了就与
		// 事实不符：那条链上没造过 text 节点）。
		if err == nil && materializedKind(ref.kind) {
			g.recordPlacement(ref.nodeID, placementConstruct("binary", "+"), PlacementNative, placementReason(PlacementCauseCapability, "native text-concat node"))
		}
		return ref, err
	}
	ref, err := g.emitExprCode(prevIDs, firstPort, portOnLast, e)
	if err != nil {
		return ref, err
	}
	g.recordPlacement(ref.nodeID, placementConstruct("binary", e.Op()), PlacementShim, placementReason(PlacementCauseNoNativeNode, "no native arithmetic node"))
	return ref, nil
}

// isStringConcat reports whether a `+` expression is string concatenation
// (at least one string-typed operand per the typed program's node types),
// i.e. hostable by a native text-concat node. Numeric addition stays a code
// shim (no native arithmetic node); unknown/optional kinds stay conservative.
func (g *Builder) isStringConcat(e *ast.BinaryExpr) bool {
	if e.Op() != "+" {
		return false
	}
	return g.exprKind(e.Left()) == types.String || g.exprKind(e.Right()) == types.String
}

// exprKind returns the typed program's descriptor kind for an expression
// node (any when unavailable). NodeTypes is keyed in the binder's
// AstNodeID space (binder.go enumerate: 1-based pre-order ordinals), NOT the
// parser's construction ids — resolve through the snapshot's node→id map.
func (g *Builder) exprKind(expr ast.Expr) types.DSLType {
	if g.prog == nil || g.prog.NodeTypes == nil {
		return types.Any
	}
	id, ok := g.prog.Snapshot.NodeIDOf(expr)
	if !ok {
		return types.Any
	}
	if d, ok := g.prog.NodeTypes[id]; ok && d != nil {
		return d.Kind
	}
	return types.Any
}

// emitExprCode materializes a pure expression as a Synthetic CodeRunner
// (`def main(value) -> dict: return {"result": (value - 1)}`). Used where
// the platform has no native value node for the operation (arithmetic,
// comparison-as-value, logical negation, coalesce): native-first (P0), code
// is the fallback. `+` string concat keeps the legacy code-node shape (see
// EXPRESSION-MATERIALIZATION.md §8 audit item). The type layer already
// gated the expression's semantics (P3); this render is mechanical.
// isCalcBuiltin reports whether name is a pure calculation builtin that
// materializes as a Synthetic code shim (G10). The set mirrors
// mainBuiltins' pure string/number functions; side-effecting builtins
// (print/push/code/output) stay outside.
func isCalcBuiltin(name string) bool {
	return pygen.IsLowerable(name)
}

// calcBuiltinOutType returns the shim output type for a calculation
// builtin so downstream conditions keep correctly-typed operands (len →
// integer, contains → boolean, split → list<string>; remaining scalar
// builtins are strings).
func calcBuiltinOutType(name string) *Type {
	switch name {
	case "len":
		return T("integer")
	case "int":
		return T("integer")
	case "float":
		return T("number")
	case "contains":
		return T("boolean")
	case "empty":
		return T("boolean")
	case "range":
		return TList(T("integer"))
	case "flatten":
		return TList(T("string"))
	case "repeat":
		return TList(T("integer"))
	case "concat":
		return TList(T("integer"))
	case "split":
		return TList(T("string"))
	case "enumerate":
		return TList(TObject(map[string]*Type{"index": T("integer"), "value": T("string")}))
	}
	return T("string")
}

// calcBuiltinOutTypeFor resolves a calc builtin's shim output type from the
// call expression when the type depends on an argument (enumerate: the
// paired-view value field carries the input element type), falling back to
// the static table otherwise. enumerate(items) where items: array<Item>
// yields list<{index: integer, value: Item}> so loop iteration variables
// resolve pair.value.<field> through the object schema.
func (g *Builder) calcBuiltinOutTypeFor(name string, e ast.Expr) *Type {
	if name == "enumerate" {
		if call, ok := e.(*ast.CallExpr); ok && len(call.Args()) == 1 {
			if ref, ok := g.resolveValueRef(call.Args()[0]); ok {
				if et := g.portTypeOf(ref.nodeID, ref.output); et != nil && et.Kind == "list" && et.Elem != nil {
					return TList(TObject(map[string]*Type{
						"index": T("integer"),
						"value": et.Elem,
					}))
				}
			}
		}
		// Unknown element: keep the value slot string-typed so downstream
		// field access degrades predictably instead of any.
		return TList(TObject(map[string]*Type{"index": T("integer"), "value": T("string")}))
	}
	return calcBuiltinOutType(name)
}

// emitExprCode 物化一个表达式为 shim，输出口类型取**类型结论**（权威，
// 见 semanticExprType：TypedBoundProgram 的 NodeTypes → 中性 IR 类型），
// 只有在类型结论缺席时才保持历史缺省 string（窄兜底，不猜）。
//
// 此前恒传 T("string")：`let b = 1 < 5;` 的 shim 因此声明 `result: string`
// （**伪造类型**，AGENTS.md 红线）——下游条件发射看到"非布尔口"就回落到
// `== "true"` 字符串比较，反编译再读回 `b == "true"`，语义漂移（R8）。
func (g *Builder) emitExprCode(prevIDs []string, firstPort string, portOnLast bool, e ast.Expr) (varRef, error) {
	out := g.semanticExprType(e)
	if out == nil {
		out = T("string")
	}
	return g.emitExprCodeOut(prevIDs, firstPort, portOnLast, e, out)
}

// arrayLiteralShimType 是数组字面量 shim 的输出类型：list<元素类型>。
// 元素类型取类型结论（semanticExprType 的 ArrayLit 分支：逐元素结论一致则
// 用该结论，混合则 object）；无结论时保持历史缺省 list<string>（窄兜底）。
// 此前恒写死 list<string>：`for (x in [1,2,3])` 的迭代口元素类型因此塌成
// string（R8 的循环面）。
func (g *Builder) arrayLiteralShimType(arr *ast.ArrayLit) *Type {
	if t := g.semanticExprType(arr); t != nil && t.Kind == "list" && t.Elem != nil {
		return t
	}
	return TList(T("string"))
}

// emitExprCodeOut is emitExprCode with an explicit Synthetic-shim output
// type (G10: len/contains shims declare integer/boolean so downstream
// selector operands resolve to the real type).
func (g *Builder) emitExprCodeOut(prevIDs []string, firstPort string, portOnLast bool, e ast.Expr, outType *Type) (varRef, error) {
	var bindings []textBinding
	// P5.5: pyast path when the expression is provably byte-equivalent to
	// renderConditionPython's output; otherwise the legacy template keeps
	// today's exact bytes (bindConditionPython dedupes, so a partial pyast
	// binding set is harmless on fallback).
	code, ok, err := shimCodeFor(e, g, &bindings)
	if err != nil {
		// §5.1 守则 4: any combinator-entry error carries the guided
		// sentence (position + construct + next step), never a bare
		// internal construct dump.
		return varRef{}, &UnsupportedError{Construct: fmt.Sprintf(
			"expression: %s (assign the value to a variable first, or write foreign code)",
			constructOf(err))}
	}
	if !ok {
		expr, rerr := renderConditionPython(e, g, &bindings)
		if rerr != nil {
			return varRef{}, &UnsupportedError{Construct: fmt.Sprintf(
				"expression: %s (assign the value to a variable first, or write foreign code)",
				constructOf(rerr))}
		}
		code = ShimMarker + fmt.Sprintf("def main(%s) -> dict:\n    return {\"result\": %s}",
			strings.Join(shimParamNames(bindings), ", "), expr)
	}
	sources := make([]Source, 0, len(bindings))
	types := map[string]*Type{}
	for _, b := range bindings {
		sources = append(sources, Source{Path: []string{b.name}, BlockID: b.ref.nodeID, Name: b.ref.output})
		bt, berr := g.refType(b.ref, nil)
		if berr != nil {
			return varRef{}, &UnsupportedError{Construct: fmt.Sprintf(
				"expression: %s (assign the value to a variable first, or write foreign code)",
				berr.Error())}
		}
		types[b.name] = bt
	}
	id := g.nextID()
	g.add(&CodeRun{
		ID:        id,
		Code:      code,
		Language:  "python",
		Inputs:    sources,
		Types:     types,
		OutTypes:  map[string]*Type{"result": outType},
		OutOrder:  []string{"result"},
		Synthetic: true,
	})
	// 根因修复（2026-09-02，S19 聚类 ANY_NOT_NARROWED round2 丢失链）：
	// shim 的数据在 result 端口（OutTypes/OutOrder 只声明 result），此前返回
	// "output"（code 渲染默认补的 string 空端口）——loop 迭代源 / 条件
	// operand / 模板插值等消费方全部引用到空 output，产物运行期无值且
	// round2 反编译把迭代源类型解析为 string → countLoop 退化/字段访问 any。
	// 改回 result：ref 指向真实数据端口（已声明，L2 校验通过）。
	return varRef{nodeID: id, output: "result", kind: "code"}, nil
}

// singleOperandConcatRef —— 「拼接只剩一段」的规范形（2026-09-28 用户裁 甲，
// 取代 r12 的乙）：**字面文本为空 ∧ 恰有一个操作数 ∧ 该操作数已是 string** ⇒ 与
// **该段本身**同产物（不生成 TextProcess 节点）。
//
// 为什么必须有这条：三处语法糖本来就该同产物 —— 多段模板 ≡ 拼接是既有规范形
// （探针 `templatestring_vs_concat`）、`"" + raw` ≡ 单占位模板串也是（探针
// `concat_empty_vs_single_interp`），**孤点只有裸值 `raw` 那一侧**（探针
// `single_interp_vs_bare`，一手：`text` 节点 sha `9728b6ac343c` vs 裸 ref
// `483419f752a3`，且两侧各自都是不动点 ⇒ 同语义两种产物）。缺的就是
// 「拼接只剩一段 ⇒ 应与该段同产物」这一条（`X + ""` 同族）。
//
// 判据落地：`template` 是**拼好的模板**（字面文本 + `{{名字}}` 占位符）⇒
// 「字面文本为空」等价于「整条模板恰好等于那**一个**占位符」。这个写法顺带把作者
// 手写的 `{{x}}` 也挡在外面（那种模板更长 ⇒ 不折，与 C2-H4 的保留名额同一结论）。
//
// 前置（**不是**可省的近似）：值类型必须已经是 `string`。`${n}`（`n` 为
// `int`/`number`）是 int→string 转换，语义与裸 `n` **不同** ⇒ 不得退化；类型取
// 不到（nil / any）⇒ **保守不折**（fail-safe：宁可留一个节点，不发错产物）。
func (g *Builder) singleOperandConcatRef(template string, refs []textBinding) (varRef, bool) {
	if len(refs) != 1 || template != "{{"+refs[0].name+"}}" {
		return varRef{}, false
	}
	ref := refs[0].ref
	if t := g.portTypeOf(ref.nodeID, ref.output); t == nil || t.Kind != "string" {
		return varRef{}, false
	}
	return ref, true
}

// emitTextConcat materializes a TextProcess concat node from a `+` chain.
func (g *Builder) emitTextConcat(prevIDs []string, firstPort string, portOnLast bool, e *ast.BinaryExpr) (varRef, error) {
	var sb strings.Builder
	var refs []textBinding
	cur := append([]string{}, prevIDs...)
	fp := firstPort
	// 整条 `+` 链的字面文本先扫一遍（含 `"..."` 字面量里手写的 `{{x}}`）：
	// 链内任一处的字面占位符名都不得被我们的绑定名抢占（C2-H4）。
	reserved := map[string]bool{}
	concatLiteralClaims(e, reserved)
	if err := g.flattenConcat(e, &sb, &refs, &cur, &fp, reserved); err != nil {
		return varRef{}, err
	}
	if sb.Len() == 0 && len(refs) == 0 {
		return varRef{}, &UnsupportedError{Construct: "empty concat result"}
	}
	if ref, ok := g.singleOperandConcatRef(sb.String(), refs); ok {
		return ref, nil
	}
	id := g.nextID()
	g.add(g.buildText(id, sb.String(), refs))
	return varRef{nodeID: id, output: "output", kind: "text"}, nil
}

// templateNavToken —— 模板插值位上的「变量 + 导航」表达式 → 平台**模板令牌**形态
// （`{{槽名[2].f}}`），让平台原生 `text` 节点承载，而不是物化 python shim。
//
// 为什么这是**载体**而非优化（SHIM 契约 §0.2 的 `¬∃c` 那一项）：平台文本节点的模板
// 令牌本就把「槽名 + 行值上的导航」写在同一段里（一手语料
// `7583741450392109119_S135_sr2_xiuxian`：5 个原生 `text` 节点，模板
// `{{String1[0]}}`…`{{String1[4]}}`、`String1` 绑 `list`）⇒ `∃c: host(c,v)` 成立
// ⇒ **禁止 shim**。读侧同一条（`convert.go::navigateTemplateSubPath`）把这些 token
// 折回值导航 ⇒ ③④ 互为逆。
//
// 四条前置（不是可省的近似；任一条不成立 ⇒ 返回 false，调用方保持 shim）：
//  1. **段只认 `.name` / `[n]`（n ≥ 0 常量）** —— 这是读侧 `navigateTemplateSubPath`
//     的**全部**文法；别的形态（`[i]` 变量下标）读侧解析不了 ⇒ 发 token 会造出读不回的
//     产物（③④ 不再互为逆）。
//  2. **根可寻址**（`varToOutput` 里的变量）：令牌的槽名要绑到一个生产者 ref；根是任意
//     子表达式（`f(x)[0]`）⇒ 令牌写不出 ⇒ 保持 shim。
//  3. **基 ref 取「最长可解析前缀」**（R-166，2026-09-29）：token 里的槽名（= 绑定名）
//     由基 ref 的**端口路径**给出，故前缀那几段**不得**再写进 token —— 否则同一段写
//     两遍。旧实现拿链根的 ref 当基：单口 code 节点的根 ref **就是**那个口本身
//     （`codeWholeValuePort` = `firstOutputPort`）⇒ `v.outputs[2]` 发成
//     `{{outputs.outputs[2]}}`（名额行名已是口名 `outputs`，token 里又写一遍），读回
//     `v.outputs.outputs[2]` ⇒ 再编译 `ANY_NOT_NARROWED`（一手：672 源收口跑批 3 源
//     `COMPILE_FAIL@r2`；期望编码 = `{{outputs[2]}}`）。取**最长**前缀而不是链根：根
//     ref 覆盖的端口路径可能只是链的一段（多口 code 节点的扁平字段口），把覆盖到的段
//     留给前缀正是 ④ raising `{{行名 + 尾段}}` 的逆（读侧把行名解析成基值、尾段解析成
//     导航）。
//  4. **读侧折得回来**：尾段出现非 0 常量下标时，基值必须是「元素不是记录」的列表 ——
//     读侧对记录数组的非 0 下标保守不折（R-111 乙：换不回本语言的可编译读法），
//     发 token 会一轮漂移。类型取不到 ⇒ 保守 false（宁可留 shim，不发错产物）。
func (g *Builder) templateNavToken(expr ast.Expr) (varRef, string, bool) {
	// 拆链：段只认 `.name` / `[n]`（n ≥ 0 常量）。
	root := expr
	segs := []string{}
	for {
		switch e := root.(type) {
		case *ast.FieldAccessExpr:
			segs = append([]string{"." + e.FieldName()}, segs...)
			root = e.Obj()
		case *ast.IndexExpr:
			lit, ok := e.Index().(*ast.IntLit)
			if !ok || lit.Value() < 0 {
				return varRef{}, "", false
			}
			segs = append([]string{"[" + strconv.FormatInt(lit.Value(), 10) + "]"}, segs...)
			root = e.Obj()
		default:
			goto done
		}
	}
done:
	// 根必须是**可寻址变量**（前置 2）：任意子表达式写不出槽名。
	if _, ok := root.(*ast.IdentExpr); !ok {
		return varRef{}, "", false
	}
	// 逐段长出前缀表达式，再取**最长**（= 段数最多）的一个能解析成 ref 的前缀：
	// 它覆盖的段由槽名承担，其余段才是「槽值上的导航」（前置 3）。
	prefixes := make([]ast.Expr, 0, len(segs)+1)
	prefixes = append(prefixes, root)
	for i, s := range segs {
		prev := prefixes[i]
		if strings.HasPrefix(s, "[") {
			n, err := strconv.Atoi(s[1 : len(s)-1])
			if err != nil {
				return varRef{}, "", false
			}
			prefixes = append(prefixes, ast.NewIndexExpr(prev, ast.NewIntLit(int64(n))))
			continue
		}
		prefixes = append(prefixes, ast.NewFieldAccessExpr(prev, s[1:]))
	}
	best := -1
	var ref varRef
	for k := len(prefixes) - 1; k >= 0; k-- {
		if r, ok := g.resolveValueRef(prefixes[k]); ok {
			best, ref = k, r
			break
		}
	}
	// 整条链都可解析 ⇒ 不是本函数的形态（`resolveValueRef` 那条 dotted 端口路已在
	// 调用方先行）。也兜住「谁都不解析」的情形。
	if best < 0 || best == len(segs) {
		return varRef{}, "", false
	}
	sub := segs[best:]
	hasIndex, anyNonZero := false, false
	for _, s := range sub {
		if !strings.HasPrefix(s, "[") {
			continue
		}
		hasIndex = true
		if s != "[0]" {
			anyNonZero = true
		}
	}
	// 尾段必须含下标：纯成员链是 dotted 端口路径那条路（没有下标的 token 与「裸槽名」
	// 同形 ⇒ 会把作者的字面 `{{name}}` 接管）。
	if !hasIndex {
		return varRef{}, "", false
	}
	if anyNonZero && !g.raisingFoldsNonZeroIndexOnList(ref) {
		return varRef{}, "", false
	}
	return ref, strings.Join(sub, ""), true
}

// raisingFoldsNonZeroIndexOnList —— 读侧 `convert.go::textNavExpressible` 的同一把尺：
// 非 0 常量下标只在「根是列表 ∧ 元素不是记录」上折得回值导航。类型取不到 / 不是列表 /
// 元素是记录 ⇒ false（调用方保持 shim）。
func (g *Builder) raisingFoldsNonZeroIndexOnList(ref varRef) bool {
	t := ref.typ
	if t == nil {
		t = g.portTypeOf(ref.nodeID, ref.output)
	}
	if t == nil || t.Kind != "list" || t.Elem == nil {
		return false
	}
	return t.Elem.Kind != "object"
}

// emitNamedTextTemplate materializes a **value-position named template**
// (`let s = template "…" using {…}`) as a native TextProcess concat node:
// `concatResult` = the verbatim text, `node_inputs` = the author's slot table in
// wire order (name → value path).
//
// 与 `emitTextTemplate`（内插形态）的分工 —— 两条路落**同一条 wire 形状**，差别只在
// 谁定名字：内插的名字由编译器从值的落点派生（`takeBindingLabelReserved`），具名形态的
// 名字是作者写死的源信息，**一字不改**。
//
// 两处**不做**：① 不折叠（`singleOperandConcatRef`）—— 折叠 = 丢掉那个名字，而名字正是
// 这条形态要保住的东西；② 不重命名（`takeBindingLabelReserved` / reserved 集合）——
// 文本逐字、名额名逐字，是它回编译出同一条 wire 的前提。
func (g *Builder) emitNamedTextTemplate(prevIDs []string, firstPort string, portOnLast bool, tr *ast.TemplateValue) (varRef, error) {
	bindings, err := g.resolveTemplateBindings(tr, "文本节点的模板值")
	if err != nil {
		return varRef{}, err
	}
	refs := make([]textBinding, 0, len(bindings))
	for _, b := range bindings {
		refs = append(refs, textBinding{name: b.Name, ref: varRef{nodeID: b.Ref.Producer, output: b.Ref.Output}})
	}
	if len(refs) == 0 && tr.Text() == "" {
		return varRef{}, &UnsupportedError{Construct: "empty named text template"}
	}
	id := g.nextID()
	g.add(g.buildText(id, tr.Text(), refs))
	return varRef{nodeID: id, output: "output", kind: "text"}, nil
}

// emitTextTemplate materializes a TextProcess concat node from a template
// string: literal text stays verbatim, interpolations become {{varN}}
// placeholders bound to upstream refs.
func (g *Builder) emitTextTemplate(prevIDs []string, firstPort string, portOnLast bool, ts *ast.TemplateString) (varRef, error) {
	var sb strings.Builder
	var refs []textBinding
	// 作者写在这个模板里的字面占位符名：我们的绑定名必须避开（见
	// takeBindingLabelReserved / C2-H4）。
	reserved := templateLiteralClaims(ts)
	for _, part := range ts.Parts() {
		sb.WriteString(part.Text)
		if part.Expr == nil {
			continue
		}
		if ref, ok := g.resolveValueRef(part.Expr); ok {
			name := takeBindingLabelReserved(&refs, ref.output, reserved)
			sb.WriteString("{{" + name + "}}")
			refs = append(refs, textBinding{name: name, ref: ref})
			continue
		}
		if lit, ok := stringValue(part.Expr); ok {
			sb.WriteString(lit)
			continue
		}
		// 平台**原生** `text` 节点承载「槽名 + 行值上的导航」（`{{slot[2].f}}`）——
		// 不物化 python shim。载体判定与两条前置见 templateNavToken 头注。
		if ref, sub, ok := g.templateNavToken(part.Expr); ok {
			name := takeBindingLabelReserved(&refs, ref.output, reserved)
			sb.WriteString("{{" + name + sub + "}}")
			refs = append(refs, textBinding{name: name, ref: ref})
			continue
		}
		// §5.3 P0 fallback（R-80，2026-09-25）：物化 = **值位置唯一入口** `emitValueExpr`
		// （它自挑载体：原生节点 / Synthetic shim）。此前直调 `emitExprCode`（只认封闭
		// shim 子集）⇒ 结构字面量在此红、而经绑定的同一语义绿；`split` / 三元也在
		// 裸写法与内插之间分叉（一手：模板里的三元第二轮 `UNDEFINED_VAR`）。
		ref, verr := g.emitValueExpr(prevIDs, firstPort, false, part.Expr)
		if verr != nil {
			return varRef{}, &UnsupportedError{Construct: fmt.Sprintf("template interpolation %T", part.Expr)}
		}
		name := takeBindingLabelReserved(&refs, ref.output, reserved)
		sb.WriteString("{{" + name + "}}")
		refs = append(refs, textBinding{name: name, ref: ref})
	}
	if sb.Len() == 0 && len(refs) == 0 {
		return varRef{}, &UnsupportedError{Construct: "empty template literal"}
	}
	if ref, ok := g.singleOperandConcatRef(sb.String(), refs); ok {
		return ref, nil
	}
	id := g.nextID()
	g.add(g.buildText(id, sb.String(), refs))
	return varRef{nodeID: id, output: "output", kind: "text"}, nil
}

// templateLiteralClaims 收集模板串里**不是我们的**占位符名（= 作者手写的字面
// `{{x}}`）：平台按 name 解析 `{{name}}`，同名的绑定会把作者的字面文本接管
// （C2-H4）。故这些名字必须保留给作者，我们的绑定名退让（takeBindingLabelReserved）。
func templateLiteralClaims(ts *ast.TemplateString) map[string]bool {
	out := map[string]bool{}
	if ts == nil {
		return out
	}
	for _, part := range ts.Parts() {
		if part.Expr == nil {
			addLiteralClaims(part.Text, out)
			continue
		}
		if lit, ok := stringValue(part.Expr); ok {
			addLiteralClaims(lit, out)
		}
	}
	return out
}

// concatLiteralClaims 同一条规则，作用于一条 `+` 链：递归收集**字面文本**里的
// 占位符名（`"..."` 字面量、模板串的字面段、常量插值），跳过我们的插值。
func concatLiteralClaims(e ast.Expr, out map[string]bool) {
	switch x := e.(type) {
	case *ast.StringLit:
		addLiteralClaims(x.Value(), out)
	case *ast.TemplateString:
		for _, part := range x.Parts() {
			if part.Expr == nil {
				addLiteralClaims(part.Text, out)
				continue
			}
			if lit, ok := stringValue(part.Expr); ok {
				addLiteralClaims(lit, out)
			}
		}
	case *ast.BinaryExpr:
		if x.Op() != "+" {
			return
		}
		concatLiteralClaims(x.Left(), out)
		concatLiteralClaims(x.Right(), out)
	default:
		if lit, ok := stringValue(e); ok {
			addLiteralClaims(lit, out)
		}
	}
}

// addLiteralClaims 扫描一段字面文本里的占位符名。`{{block_output_...}}` 不会出现
// 在字面文本里（那是我们在 IR 层写的引用拼法，字面文本来自作者），故不豁免。
func addLiteralClaims(text string, out map[string]bool) {
	for _, m := range literalPlaceholderRe.FindAllStringSubmatch(text, -1) {
		out[m[1]] = true
	}
}

var literalPlaceholderRe = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

// flattenConcat expands a `+` chain into a template string plus ref bindings.
func (g *Builder) flattenConcat(e ast.Expr, sb *strings.Builder, refs *[]textBinding, prevIDs *[]string, firstPort *string, reserved map[string]bool) error {
	switch x := e.(type) {
	case *ast.StringLit:
		sb.WriteString(x.Value())
		return nil
	case *ast.TemplateString:
		for _, part := range x.Parts() {
			sb.WriteString(part.Text)
			if part.Expr == nil {
				continue
			}
			if ref, ok := g.resolveValueRef(part.Expr); ok {
				name := takeBindingLabelReserved(refs, ref.output, reserved)
				sb.WriteString("{{" + name + "}}")
				*refs = append(*refs, textBinding{name: name, ref: ref})
				continue
			}
			if lit, ok := stringValue(part.Expr); ok {
				sb.WriteString(lit)
				continue
			}
			// §5.3 P0 fallback（R-80，2026-09-25）：物化 = **值位置唯一入口** `emitValueExpr`
			// （它自挑载体：原生节点 / Synthetic shim）。此前直调 `emitExprCode`（只认封闭
			// shim 子集）⇒ 结构字面量在此红、而经绑定的同一语义绿；`split` / 三元也在
			// 裸写法与内插之间分叉（一手：模板里的三元第二轮 `UNDEFINED_VAR`）。
			ref, verr := g.emitValueExpr(*prevIDs, *firstPort, false, part.Expr)
			if verr != nil {
				return &UnsupportedError{Construct: fmt.Sprintf("concat template interpolation %T", part.Expr)}
			}
			name := takeBindingLabelReserved(refs, ref.output, reserved)
			sb.WriteString("{{" + name + "}}")
			*refs = append(*refs, textBinding{name: name, ref: ref})
		}
		return nil
	case *ast.TernaryExpr:
		// 模板插值里的三元走**第 5 宿主的唯一入口**（`emitTernaryJunction`）。
		// 这里曾经自己合成一遍 `if`（第二份合成序列）⇒ **少了 `syntheticTernary`
		// 保护**：合成 `return` 会把 `branchReturns` 置真并泄漏到它所在的语句臂，
		// 宿主 if 因此把「臂里有个三元表达式」误读成「这条臂产出了尾值」——
		// 该臂**后续的语句与调用整段丢失**（一手实测：臂内 `prompt` 拼接与其后的
		// `llm(…)` 在产物里 0 节点，见 `TestCozeBizExpressionTernaryMustNotSwallowArm`）。
		// 同一个洞 54e3a82 在 `emitValueExpr` 那条入口修过一次（loop 体内三元 ⇒
		// 误判为 loop 返回），修的是**入口**而不是**规则** ⇒ 留在这条入口上复发。
		// 插值位置**没有命名目标** ⇒ 槽名传空（与裸值位置同一条规则，U6）。
		ids, err := g.emitTernaryJunction(*prevIDs, *firstPort, x.Condition(), x.ThenVal(), x.ElseVal(), "")
		if err != nil {
			return err
		}
		*prevIDs = ids
		*firstPort = ""
		if len(ids) == 0 {
			return &UnsupportedError{Construct: "ternary value position: junction produced no value"}
		}
		ref, verr := g.junctionValueRefOf(ids[0])
		if verr != nil {
			return verr
		}
		name := takeBindingLabelReserved(refs, ref.output, reserved)
		sb.WriteString("{{" + name + "}}")
		*refs = append(*refs, textBinding{name: name, ref: ref})
		return nil
	case *ast.BinaryExpr:
		if x.Op() != "+" {
			return &UnsupportedError{Construct: fmt.Sprintf("binary op %q in concat", x.Op())}
		}
		if err := g.flattenConcat(x.Left(), sb, refs, prevIDs, firstPort, reserved); err != nil {
			return err
		}
		return g.flattenConcat(x.Right(), sb, refs, prevIDs, firstPort, reserved)
	default:
		if ref, ok := g.resolveValueRef(x); ok {
			name := takeBindingLabelReserved(refs, ref.output, reserved)
			sb.WriteString("{{" + name + "}}")
			*refs = append(*refs, textBinding{name: name, ref: ref})
			return nil
		}
		if lit, ok := stringValue(x); ok {
			sb.WriteString(lit)
			return nil
		}
		// §5.3 P0 fallback（R-80，2026-09-25）：物化 = **值位置唯一入口** `emitValueExpr`
		// （它自挑载体：原生节点 / Synthetic shim）。此前直调 `emitExprCode`（只认封闭
		// shim 子集）⇒ 结构字面量在此红、而经绑定的同一语义绿；`split` / 三元也在
		// 裸写法与内插之间分叉（一手：模板里的三元第二轮 `UNDEFINED_VAR`）。
		ref, verr := g.emitValueExpr(*prevIDs, *firstPort, false, x)
		if verr != nil {
			return &UnsupportedError{Construct: fmt.Sprintf("concat operand %T", x)}
		}
		name := takeBindingLabelReserved(refs, ref.output, reserved)
		sb.WriteString("{{" + name + "}}")
		*refs = append(*refs, textBinding{name: name, ref: ref})
		return nil
	}
}

// inlineCall expands a user-defined function at its call site: caller
// arguments bind to the function parameters (materializing non-ref args as
// nodes), then the function body is emitted inline.
func (g *Builder) inlineCall(fn *ast.FunctionDef, args []ast.Expr, prevIDs []string, firstPort string) ([]string, string, varRef, error) {
	if g.codeFuncs[fn.Name()] {
		return g.inlineCodeCall(fn, args)
	}
	snapshot := g.varToOutput
	g.varToOutput = cloneRefs(snapshot)
	tail := append([]string{}, prevIDs...)
	fp := firstPort
	inlineParams := []InlineParam{}
	for i, p := range fn.Params() {
		if i >= len(args) {
			continue
		}
		if ref, ok := g.resolveValueRef(args[i]); ok {
			g.varToOutput[p.Name()] = ref
			inlineParams = append(inlineParams, InlineParam{
				Name: p.Name(), Type: g.typeOf(p.TypeRef()),
				Source: Source{Path: []string{p.Name()}, BlockID: ref.nodeID, Name: ref.output},
			})
			continue
		}
		ref, err := g.emitValueExpr(tail, fp, false, args[i])
		if err != nil {
			g.varToOutput = snapshot
			return nil, "", varRef{}, err
		}
		g.varToOutput[p.Name()] = ref
		inlineParams = append(inlineParams, InlineParam{
			Name: p.Name(), Type: g.typeOf(p.TypeRef()),
			Source: Source{Path: []string{p.Name()}, BlockID: ref.nodeID, Name: ref.output},
		})
		tail = []string{ref.nodeID}
		fp = ""
	}
	g.flushTreeNodes() // argument materialization → leaf stmts before the call
	// 容器所有权上下文屏障（P13，2026-09-11）：函数定义体**不属于调用点的
	// 任何容器** —— 这是语言已生效的词法所有权规则（前端已强制函数体不得
	// 读写调用方变量）。builder 的容器上下文却是全局字段，inline 边界因此
	// 必须把「宿主容器所有权」整体快照-清空-恢复；否则函数体内的 return /
	// 赋值会被调用方容器夺权或被抑制（S2-gate 实测 5 类，多数**无诊断**）：
	//   - loopStack 非空 ⇒ 函数内 return 被夺权为调用方循环的 M17 loop
	//     return（不设 tailRef 也不置 branchReturns ⇒ 空 ref ⇒
	//     `function "x" produces no value`）；
	//   - parallelDepth > 0 ⇒ 函数内 return 被误判为「终止整个流程」
	//     （静默生成 flow-end 直连 unique end ⇒ 运行期从助手函数里把整个
	//     工作流提前结束）；
	//   - loopStates 栈顶含同名 ⇒ 函数内赋值被静默写成调用方循环的状态赋值
	//     （Assign{LoopState:true, LoopID:调用方循环}）；
	//   - loopReturns/loopYields ⇒ 调用方循环静默获得 return_value/输出口；
	//   - branchValueDepth/switchBranchDepth ⇒ 抑制函数自带循环的 M17；
	//   - branchFlowReturns 等分支事实标志 ⇒ 调用方分支被误判为 flow-return
	//     （静默多出一个 phi 汇聚）。
	// 一条规则覆盖全部容器类型与两个方向（向内夺权 / 向外错判），不逐字段
	// 加守卫；恢复点与 tailRef/branchReturns/varToOutput 同处（错误路径同样
	// 经过）。branchDepth 与 inlineName 不在屏障内：前者是「已进入 body/
	// 分支」标记（子函数 return 分支的选中前提），后者是本边界自身置位的
	// 体内标记。
	savedLoopStack := g.loopStack
	savedLoopStates := g.loopStates
	savedLoopTopDepth := g.loopBodyTopDepth
	savedLoopReturns := g.loopReturns
	savedLoopYields := g.loopYields
	savedParallelDepth := g.parallelDepth
	savedBranchValueDepth := g.branchValueDepth
	savedSwitchBranchDepth := g.switchBranchDepth
	savedSyntheticTernary := g.syntheticTernary
	savedBranchFlowReturns := g.branchFlowReturns
	savedBranchVoidReturns := g.branchVoidReturns
	savedLastBranchFlowReturned := g.lastBranchFlowReturned
	savedLastBranchVoidReturned := g.lastBranchVoidReturned
	g.loopStack = nil
	g.loopStates = nil
	g.loopBodyTopDepth = nil
	g.loopReturns = nil
	g.loopYields = nil
	g.parallelDepth = 0
	g.branchValueDepth = 0
	g.switchBranchDepth = 0
	g.syntheticTernary = 0
	g.branchFlowReturns = false
	g.branchVoidReturns = false
	g.lastBranchFlowReturned = false
	g.lastBranchVoidReturned = false
	savedTail := g.tailRef
	g.tailRef = nil
	// Track whether the body actually EXECUTED a return: a void helper (no
	// return anywhere, or only a bare `return;`) must not inherit the
	// body's incidental tail (an else-less-if fall-through keeps the
	// pre-if value) as its own result — that stale ref ({plugin, output})
	// would land in the caller's if/else aggregate and name a port the
	// plugin does not declare (引用变量不存在).
	savedBranchReturns := g.branchReturns
	// 帧出口表：**词法**范围 —— 进入函数体时清空、离开时恢复（嵌套内联各得
	// 其帧）；与 loopStack / inlineName 同处屏障，错误路径同样经过（见下方
	// 恢复点）。
	savedInlineExits := g.inlineExits
	g.inlineExits = nil
	g.branchDepth++
	savedInlineName := g.inlineName
	g.inlineName = fn.Name()
	bodyBlock := &Block{}
	g.blockStack = append(g.blockStack, bodyBlock)
	junction, _, err := g.emitStmts(fn.Body().Stmts(), tail, fp)
	retPort := ""
	if g.pendingElse != nil {
		// A trailing else-less if inside the inline body falls through to
		// the statement after the call. Resolve it at the inline boundary
		// (merged then-tails + selector on the false port) — leaking it
		// into the caller would scramble the caller's next statement or,
		// worse, the following else-branch entry (regression: statement
		// helper calls in loop/if bodies producing branch_port_not_connected).
		p := g.pendingElse
		g.pendingElse = nil
		seen := map[string]bool{p.selID: true}
		merged := []string{}
		for _, id := range p.thenIDs {
			if id == p.selID || seen[id] || g.isBreakish(id) {
				continue
			}
			seen[id] = true
			merged = append(merged, id)
		}
		junction = append(merged, p.selID)
		retPort = "false"
	}
	// 帧出口汇聚单元：子函数的多个值出口在这里收成一个值（单出口/零出口 ⇒
	// nil，保持既有形状）。表的读取必须**先于**恢复（恢复后属于调用方帧）。
	frameExit := g.inlineFrameValuePhi(g.inlineExits, g.tailRef)
	g.inlineExits = savedInlineExits
	out := g.tailRef
	if frameExit != nil {
		v := junctionValueRef(frameExit)
		out = &v
	}
	returned := g.branchReturns != savedBranchReturns
	g.branchReturns = savedBranchReturns // never pollute the caller's end decision
	g.inlineName = savedInlineName
	g.tailRef = savedTail
	// 容器上下文屏障出口：恢复调用方容器的所有权上下文（函数体内建立的
	// 容器状态已在其自身作用域内消费完毕，不外泄）。
	g.loopStack = savedLoopStack
	g.loopStates = savedLoopStates
	g.loopBodyTopDepth = savedLoopTopDepth
	g.loopReturns = savedLoopReturns
	g.loopYields = savedLoopYields
	g.parallelDepth = savedParallelDepth
	g.branchValueDepth = savedBranchValueDepth
	g.switchBranchDepth = savedSwitchBranchDepth
	g.syntheticTernary = savedSyntheticTernary
	g.branchFlowReturns = savedBranchFlowReturns
	g.branchVoidReturns = savedBranchVoidReturns
	g.lastBranchFlowReturned = savedLastBranchFlowReturned
	g.lastBranchVoidReturned = savedLastBranchVoidReturned
	g.branchDepth--
	g.blockStack = g.blockStack[:len(g.blockStack)-1]
	g.flushTreeNodes()
	g.varToOutput = snapshot
	if err != nil {
		return nil, "", varRef{}, err
	}
	// A void helper (no return executed) inlined as a statement yields no
	// value — the zero ref is legal here; VALUE-position callers reject it.
	if out == nil || !returned {
		g.appendStmt(&InlineStmt{
			Name: fn.Name(), Params: inlineParams, ReturnType: g.typeOf(fn.ReturnType()),
			Body: bodyBlock, Value: nil, Exit: frameExit,
		})
		return junction, retPort, varRef{}, nil
	}
	g.appendStmt(&InlineStmt{
		Name: fn.Name(), Params: inlineParams, ReturnType: g.typeOf(fn.ReturnType()),
		Body: bodyBlock, Value: out, Exit: frameExit,
	})
	return junction, retPort, *out, nil
}

func (g *Builder) inlineCodeCall(fn *ast.FunctionDef, args []ast.Expr) ([]string, string, varRef, error) {
	if fn.Body() == nil || len(fn.Body().Stmts()) != 1 {
		return nil, "", varRef{}, &UnsupportedError{Construct: fmt.Sprintf("code function %q body", fn.Name())}
	}
	ret, ok := fn.Body().Stmts()[0].(*ast.ReturnStmt)
	if !ok {
		return nil, "", varRef{}, &UnsupportedError{Construct: fmt.Sprintf("code function %q return", fn.Name())}
	}
	params := map[string]ast.Expr{}
	for i, param := range fn.Params() {
		if i < len(args) {
			params[param.Name()] = args[i]
		}
	}
	bindings := []textBinding{}
	// P5.10: pyast path when the return expression lowers byte-identically
	// (param substitution included); the legacy renderInlinePython template
	// keeps today's exact bytes otherwise (bindConditionPython dedupes, so
	// a partial pyast binding set is harmless on fallback).
	code, ok, err := lowerInlineShimCode(ret.Value(), params, g, &bindings)
	if err != nil {
		return nil, "", varRef{}, err
	}
	if !ok {
		expr, rerr := renderInlinePython(ret.Value(), params, g, &bindings)
		if rerr != nil {
			return nil, "", varRef{}, rerr
		}
		code = ShimMarker + fmt.Sprintf("def main(%s) -> dict:\n    return {\"result\": %s}",
			strings.Join(shimParamNames(bindings), ", "), expr)
	}
	// 作者写的 `code fn` 调用**不是**「编译器为 ncoda 表达式物化的 shim」——
	// 它是作者写的一次**调用**，python 体只是后端翻译（名字/声明形式本就不可
	// 从 wire 恢复）。因此**剥掉** shim 标记（`# ncoda:shim 1`）：
	//
	//   · 标记会授权反编译把它反译成**裸表达式** ⇒ 调用形式消失，且读侧实测振荡
	//     （c0 4 节点 → c1 6 节点，已登记 code_call 族棘轮债务）；
	//   · 标记还会让反编译的死值清扫把它当**纯值**删掉（`pureDeadSafeInit` 认
	//     表达式），而 lowering 侧判据相反（非 Synthetic 的 code 节点**不删**）
	//     ⇒ 同一节点两侧死活判断不一致 ⇒ 一轮拓扑漂移（PE-H1）。
	//
	// 剥掉标记后，两侧判据一致：反编译只能按**原样 python 盒**
	// （`foreign code … source`）保真 —— 节点在、python 逐字在、既不内联也不删。
	// 编译器自建的表达式物化（buildMap/buildArray/emitExprCode…）仍带标记，
	// 可逆机制本体不动（存活侧锚点见 cozebiz_code_fn_call_contract_test.go）。
	code = strings.TrimPrefix(code, ShimMarker)
	inputs := make([]Source, 0, len(bindings))
	types := map[string]*Type{}
	names := make([]string, 0, len(bindings))
	for _, b := range bindings {
		names = append(names, b.name)
		inputs = append(inputs, Source{Path: []string{b.name}, BlockID: b.ref.nodeID, Name: b.ref.output})
		bt, berr := g.refType(b.ref, nil)
		if berr != nil {
			return nil, "", varRef{}, &UnsupportedError{Construct: fmt.Sprintf(
				"code function %q: %s (assign the value to a variable first, or write foreign code)",
				fn.Name(), berr.Error())}
		}
		types[b.name] = bt
	}
	id := g.nextID()
	outType := g.typeOf(fn.ReturnType())
	g.add(&CodeRun{
		ID:       id,
		Code:     code,
		Language: "python",
		Inputs:   inputs,
		Types:    types,
		OutTypes: map[string]*Type{"result": outType},
		OutOrder: []string{"result"},
	})
	ref := varRef{nodeID: id, output: "result", kind: "code"}
	return []string{id}, "", ref, nil
}

func renderInlinePython(e ast.Expr, params map[string]ast.Expr, g *Builder, bindings *[]textBinding) (string, error) {
	if id, ok := e.(*ast.IdentExpr); ok {
		if arg, exists := params[id.Name()]; exists {
			return renderConditionPython(arg, g, bindings)
		}
	}
	switch x := e.(type) {
	case *ast.BinaryExpr:
		left, err := renderInlinePython(x.Left(), params, g, bindings)
		if err != nil {
			return "", err
		}
		right, err := renderInlinePython(x.Right(), params, g, bindings)
		if err != nil {
			return "", err
		}
		op := x.Op()
		if op == "&&" {
			op = "and"
		} else if op == "||" {
			op = "or"
		} else if op == "??" {
			return "(" + left + " if " + left + " is not None else " + right + ")", nil
		}
		return "(" + left + " " + op + " " + right + ")", nil
	case *ast.UnaryExpr:
		operand, err := renderInlinePython(x.Operand(), params, g, bindings)
		if err != nil {
			return "", err
		}
		op := x.Op()
		if op == "!" {
			op = "not "
		}
		return "(" + op + operand + ")", nil
	case *ast.TernaryExpr:
		condition, err := renderInlinePython(x.Condition(), params, g, bindings)
		if err != nil {
			return "", err
		}
		thenValue, err := renderInlinePython(x.ThenVal(), params, g, bindings)
		if err != nil {
			return "", err
		}
		elseValue, err := renderInlinePython(x.ElseVal(), params, g, bindings)
		if err != nil {
			return "", err
		}
		return "(" + thenValue + " if " + condition + " else " + elseValue + ")", nil
	default:
		return renderConditionPython(e, g, bindings)
	}
}

// emitStdlibCall expands a std.v1.* library call into its node sequence
// (knowledge → llm for rag_answer; http → llm for fetch_and_summarize).
func (g *Builder) emitStdlibCall(call *ast.CallExpr, prevIDs []string, firstPort string) ([]string, varRef, error) {
	switch calleeName(call.Func()) {
	case "std.v1.rag_answer":
		return g.emitRagAnswer(call, prevIDs, firstPort)
	case "std.v1.fetch_and_summarize":
		return g.emitFetchAndSummarize(call, prevIDs, firstPort)
	}
	return nil, varRef{}, &UnsupportedError{Construct: fmt.Sprintf("stdlib %q", calleeName(call.Func()))}
}

// emitRagAnswer: knowledge(datasets, query) → llm(model).
func (g *Builder) emitRagAnswer(call *ast.CallExpr, prevIDs []string, firstPort string) ([]string, varRef, error) {
	args := call.Args()
	if len(args) != 3 {
		return nil, varRef{}, &UnsupportedError{Construct: "std.v1.rag_answer arity (query, dataset_ids, model)"}
	}
	// Both the dataset list and the model are constant config slots (same
	// ruler as http() method): the platform carries them as configuration, so
	// a runtime expression has no carrier and must fail loud rather than
	// silently degrade to "no datasets" / the platform default model.
	datasets, derr := g.constantSlotString("std.v1.rag_answer dataset ids", args[1])
	if derr != nil {
		return nil, varRef{}, derr
	}
	model, merr := g.constantSlotString("std.v1.rag_answer model", args[2])
	if merr != nil {
		return nil, varRef{}, merr
	}
	kid := g.nextID()
	knode, kerr := g.buildKnowledge(kid, ast.NewCallExpr(ast.NewIdentExpr("knowledge"), nil, []ast.Expr{ast.NewStringLit(datasets), args[0]}), prevIDs, firstPort)
	if kerr != nil {
		return nil, varRef{}, kerr
	}
	if kc, ok := knode.(*KnowledgeCall); ok {
		kc.QueryName = "query" // bound parameter: rag_answer(query, ...)
	}
	g.add(knode)
	lid := g.nextID()
	// The rag prompt is the function's semantic payload: a system
	// instruction plus a user question that inlines the retrieval result
	// and the query as dataflow refs (backend shapes the prompt/variables).
	content := "Context:\n" + fmt.Sprintf("{{block_output_%s.outputList}}", kid) + "\n\nQuestion:\n"
	// 绑定名 = **最终消费字段**（= 生产者端口），不是构造期的位置计数器：
	// 名字要进 wire（node_inputs.name / 模板占位符的键），而位置计数器不在任何
	// 平台通道上 ⇒ 一次往返必然改名（实测 stdlib 语料 r0≍c0 违反：产物
	// `value_1` → 再编译成 `outputList`）。同一条规则在通用通道上早已成立
	// （collectBlockRefs：Path = 端口段），此处是唯一的例外。
	inputs := []Source{{Path: []string{"outputList"}, BlockID: kid, Name: "outputList"}}
	if queryRef, qok := g.resolveValueRef(args[0]); qok {
		content += fmt.Sprintf("{{block_output_%s.%s}}", queryRef.nodeID, queryRef.output)
		inputs = append(inputs, Source{Path: strings.Split(queryRef.output, "."), BlockID: queryRef.nodeID, Name: queryRef.output})
	}
	g.add(&LLMCall{
		ID:       lid,
		Model:    model,
		OutTypes: llmTextOutTypes(T("string")),
		Result:   stringLLMResultContract(),
		Config: []ConfigPair{{Key: "messages", Value: []Message{
			{Role: "system", Content: "Answer using the retrieved context."},
			{Role: "user", Content: content},
		}}},
		Inputs: inputs,
	})
	// 结果端口 = **语义锚**（LLMResponse 的 text 字段，wire 映射 output），
	// 不是 legacy 的裸 "output" 别名：stdlib 展开的返回值就是响应文本，
	// 出口绑定的具名（outputPortName）与下游字段访问都按这个锚定名走
	// （实测：裸 "output" 让 `return std.v1.fetch_and_summarize(...)` 的 end
	// 输入具名为 output，而再编译同一语义给出 text ⇒ 一次往返改名）。
	return []string{lid}, varRef{nodeID: lid, output: "text", kind: "code"}, nil
}

// emitFetchAndSummarize: http(GET, url) → llm(model).
func (g *Builder) emitFetchAndSummarize(call *ast.CallExpr, prevIDs []string, firstPort string) ([]string, varRef, error) {
	args := call.Args()
	if len(args) != 2 {
		return nil, varRef{}, &UnsupportedError{Construct: "std.v1.fetch_and_summarize arity (url, model)"}
	}
	model, merr := g.constantSlotString("std.v1.fetch_and_summarize model", args[1])
	if merr != nil {
		return nil, varRef{}, merr
	}
	url, uerr := g.resolveHTTPTemplate(args[0], prevIDs, firstPort, "url")
	if uerr != nil {
		return nil, varRef{}, uerr
	}
	hid := g.nextID()
	g.add(&HTTPCall{ID: hid, Method: "GET", URL: url})
	lid := g.nextID()
	// http 结果 body 口：口名取自内建节点结果契约（builtin_result_ports.go），
	// 本处不手抄物理口名。
	bodyPort := httpResultPortName("body")
	// The summarize prompt is the function's semantic payload: a user
	// instruction inlining the fetched body as a dataflow ref.
	g.add(&LLMCall{
		ID:       lid,
		Model:    model,
		OutTypes: llmTextOutTypes(T("string")),
		Result:   stringLLMResultContract(),
		Config: []ConfigPair{{Key: "messages", Value: []Message{
			{Role: "user", Content: "Summarize this content:\n" + fmt.Sprintf("{{block_output_%s.%s}}", hid, bodyPort)},
		}}},
		Inputs: []Source{{Path: []string{bodyPort}, BlockID: hid, Name: bodyPort}},
	})
	// 结果端口 = **语义锚**（LLMResponse 的 text 字段，wire 映射 output），
	// 不是 legacy 的裸 "output" 别名：stdlib 展开的返回值就是响应文本，
	// 出口绑定的具名（outputPortName）与下游字段访问都按这个锚定名走
	// （实测：裸 "output" 让 `return std.v1.fetch_and_summarize(...)` 的 end
	// 输入具名为 output，而再编译同一语义给出 text ⇒ 一次往返改名）。
	return []string{lid}, varRef{nodeID: lid, output: "text", kind: "code"}, nil
}

// emitListOp lowers filter/take/sort/sort_by into reference list-operator
// nodes (legacy list_operator_lowerer.go): each operation becomes one
// ordered NodeStmt and the last node exposes its "result" output. map is not a the reference target
// list-operator (the legacy backend falls through to the generic code
// materializer), so it stays a CodeRunner.
func (g *Builder) emitListOp(call *ast.CallExpr, prevIDs []string, firstPort string) (varRef, error) {
	name := calleeName(call.Func())
	// recursively resolve a list-arg that is itself a list op
	src := varRef{}
	if len(call.Args()) > 0 {
		if inner, ok := call.Args()[0].(*ast.CallExpr); ok && isListOpName(calleeName(inner.Func())) {
			ref, err := g.emitListOp(inner, prevIDs, firstPort)
			if err != nil {
				return varRef{}, err
			}
			src = ref
		} else if ref, ok := g.resolveValueRef(call.Args()[0]); ok {
			src = ref
		}
	}
	if name == "map" {
		return g.emitMapOp(call, src, prevIDs, firstPort)
	}
	id := g.nextID()
	op := &ListOp{ID: id, Operation: name}
	if src.nodeID != "" {
		op.Inputs = append(op.Inputs, Source{Path: []string{"input"}, BlockID: src.nodeID, Name: src.output})
	}
	switch name {
	case "filter":
		fi, ferr := listFilterItem(call)
		if ferr != nil {
			return varRef{}, ferr
		}
		op.FilterKey, op.FilterOp, op.FilterVal = fi.key, fi.op, fi.val
	case "take":
		// 限额位必须是整数字面量：平台载体（`renderListOpNode` 生成的 python）
		// 把切片上界**内联成字面量**，动态值没有承载面。非字面量 ⇒ fail loud ——
		// **静默留 op.Limit = 0** 会产出 `items[:0]`（作者要的前 k 项变成空数组
		// = 静默丢值，撞「失配 ≠ 静默丢弃」）。
		// （`const K = 3;` 在**前端**已替换成字面量 ⇒ 到这里的非字面量是真动态值。）
		if len(call.Args()) > 1 {
			il, ok := call.Args()[1].(*ast.IntLit)
			if !ok {
				return varRef{}, &UnsupportedError{Construct: "take limit"}
			}
			op.Limit = int(il.Value())
		}
	case "sort":
		// 同 take：方向位（`asc`/`desc`）被内联成字面量 ⇒ 动态值无承载面，
		// 必须拒收（旧行为 = 静默按升序产出，作者写的方向消失）。
		//
		// **今天不可达**（`listOpNames` 不含 `sort`/`sort_by` ⇒ 调用在更外层先被判
		// `call "sort" in let` 拒收，见 build_call.go）；本支保留为**对称判据** ——
		// 一旦接线 sort，这里的「非字面量必须拒收」即刻生效，不会退回静默默认值。
		if len(call.Args()) > 1 {
			sl, ok := call.Args()[1].(*ast.StringLit)
			if !ok {
				return varRef{}, &UnsupportedError{Construct: "sort order"}
			}
			op.OrderVal = sl.Value()
		}
	case "sort_by":
		if len(call.Args()) > 1 {
			sl, ok := call.Args()[1].(*ast.StringLit)
			if !ok {
				return varRef{}, &UnsupportedError{Construct: "sort_by key"}
			}
			op.OrderKey = sl.Value()
		}
		if len(call.Args()) > 2 {
			sl, ok := call.Args()[2].(*ast.StringLit)
			if !ok {
				return varRef{}, &UnsupportedError{Construct: "sort_by order"}
			}
			op.OrderVal = sl.Value()
		}
	default:
		return varRef{}, &UnsupportedError{Construct: fmt.Sprintf("list operator %q", name)}
	}
	op.ItemType = T("string") // collection element type (array[string] corpus default)
	// Whole-value port contract: take/filter/sort preserve the collection
	// type, so the node's `output` port carries the source collection's type.
	// Authoritative source first (TypePass conclusion for this call), then the
	// resolved source port; both nil ⇒ the contract stays unknown and a
	// consumer that needs the type fails loud (never a forged string).
	//@guard-exempt: 本行是**类型谓词**（Type 轴），不是「仅当 X 形态才…」的形态枚举守卫
	// ——判据对 filter / take / sort / sort_by / 链式 / 任意分支体一视同仁；与既有
	// isObjectType（build_return.go:1280）同惯用法。日期 2026-09-14。
	// 退出条件：仓库出现通用 isListType/Elem 谓词后改调它（本标注一并删除）。
	if rt := g.semanticExprType(call); rt != nil && rt.Kind == "list" {
		op.ResultType = rt
	} else if src.nodeID != "" {
		//@guard-exempt: 同上（源端口契约的类型谓词；形态无关）。日期 2026-09-14。
		if st := g.portTypeOf(src.nodeID, src.output); st != nil && st.Kind == "list" {
			op.ResultType = st
		}
	}
	g.add(op)
	return varRef{nodeID: id, output: "output", typ: op.ResultType, kind: "listop"}, nil
}

// listFilterItem mirrors the legacy list-operator filter lowering
// (lowering/list_operator_lowerer.go filterCondition): a lambda predicate
// body of `item.contains(v)` becomes a contains condition; a comparison
// expression becomes the matching comparison operator.
type listFilter struct {
	key string
	op  string
	val any
}

func listFilterItem(call *ast.CallExpr) (listFilter, error) {
	for _, a := range call.Args() {
		lam, ok := a.(*ast.LambdaExpr)
		if !ok {
			continue
		}
		params := lam.Params()
		if len(params) != 1 {
			return listFilter{}, &UnsupportedError{Construct: "filter lambda with multiple params"}
		}
		param := params[0]
		body := lam.Body()
		if bc, ok := body.(*ast.CallExpr); ok {
			fa, ok := bc.Func().(*ast.FieldAccessExpr)
			if !ok || len(bc.Args()) != 1 {
				return listFilter{}, &UnsupportedError{Construct: "filter lambda call"}
			}
			// 方法谓词闭集：`contains` 与其否定 `not_contains` **同族同源** ——
			// 平台没有「不包含」算子，但谓词位是 python 推导式（`renderListOpNode`），
			// 一条 `not in` 就能模拟（同 R-176 条件位 `empty(contains(...))` 的立场：
			// 语法不同 ≠ 语义装不下）。其余方法（startsWith / …）不在此列。
			var op string
			switch fa.FieldName() {
			case "contains":
				op = "contains"
			case "not_contains":
				op = "not_contains"
			default:
				return listFilter{}, &UnsupportedError{Construct: "filter lambda call"}
			}
			key, kerr := listFilterKey(fa.Obj(), param)
			if kerr != nil {
				return listFilter{}, kerr
			}
			val, verr := listLiteralValue(bc.Args()[0])
			if verr != nil {
				return listFilter{}, verr
			}
			return listFilter{key: key, op: op, val: val}, nil
		}
		if be, ok := body.(*ast.BinaryExpr); ok {
			key, kerr := listFilterKey(be.Left(), param)
			if kerr != nil {
				return listFilter{}, kerr
			}
			val, verr := listLiteralValue(be.Right())
			if verr != nil {
				return listFilter{}, verr
			}
			op := be.Op()
			// `in` 不是本闭集的成员（2026-10-17 用户裁定「乙 · 删死臂」，C5b-H1）：
			// 右值要求 array，而 `listLiteralValue` 只收标量 ⇒ 语言面根本造不出
			// `it in <array>` 谓词；保留该臂只会让映射表声称「支持 in」而实际不可达。
			// 条件位的 `in` 是另一条路径（build_condition 的 membership 折叠），不受影响。
			// **算子闭集**（2026-10-17 修，C5b-H2 · 构造网格撞出）：只有下面 6 个算子在
			// `renderListOpNode` 里有**显式**译法。此前 `encoded = op` 把任意算子透传，
			// 而渲染器的 `default` 把未知算子**静默**当 `==` ⇒ 在 `array<bool>` 上
			// `filter(xs, (it) -> it || false)` 被译成 `item == false`（**语义反转**：
			// `x||false ≡ x`，`x==false ≡ ¬x`）；`it && false` / `it ?? true` /
			// `it + true` / `it % true` 同样落 `==` 且编译 SUCCEEDED。
			// ⇒ 闭集外一律 **loud 拒收**（与 startsWith 同口径：语法不同 ≠ 语义装不下，
			//   但**没有**正确译法的算子不得静默落成别的算子）。
			comparison := map[string]string{
				"==": "is", "!=": "is not",
				">": ">", ">=": ">=", "<": "<", "<=": "<=",
			}
			encoded, ok := comparison[op]
			if !ok {
				return listFilter{}, &UnsupportedError{Construct: "filter predicate operator " + op}
			}
			return listFilter{key: key, op: encoded, val: val}, nil
		}
		return listFilter{}, &UnsupportedError{Construct: "filter lambda body"}
	}
	return listFilter{}, &UnsupportedError{Construct: "filter without lambda"}
}

// listFilterKey mirrors the legacy filterKey: the bare parameter keys "",
// a parameter field access keys the field name.
func listFilterKey(expr ast.Expr, param string) (string, error) {
	if ie, ok := expr.(*ast.IdentExpr); ok && ie.Name() == param {
		return "", nil
	}
	if fa, ok := expr.(*ast.FieldAccessExpr); ok {
		if obj, ok := fa.Obj().(*ast.IdentExpr); ok && obj.Name() == param {
			return fa.FieldName(), nil
		}
	}
	return "", &UnsupportedError{Construct: "filter target"}
}

// listLiteralValue mirrors the legacy literalValue: scalar literals pass
// through.
func listLiteralValue(expr ast.Expr) (any, error) {
	switch e := expr.(type) {
	case *ast.StringLit:
		return e.Value(), nil
	case *ast.IntLit:
		return e.Value(), nil
	case *ast.FloatLit:
		return e.Value(), nil
	case *ast.BoolLit:
		return e.Value(), nil
	}
	return nil, &UnsupportedError{Construct: "filter value"}
}

// emitMapOp keeps map as a CodeRunner over the input list (the legacy
// backend has no map list-operator; the generic materializer path renders
// the comprehension).
func (g *Builder) emitMapOp(call *ast.CallExpr, src varRef, prevIDs []string, firstPort string) (varRef, error) {
	id := g.nextID()
	node := &CodeRun{ID: id, Language: "python", Types: map[string]*Type{}, Synthetic: true}
	// P5.8 pyast path: lower the lambda body through the python-subset AST
	// when every shape is byte-equivalent to exprToPython's output; the
	// legacy string template keeps today's exact bytes otherwise.
	if lam, ok := lambdaIn(call); ok {
		params := lam.Params()
		if len(params) == 1 {
			if elt, lok, err := lowerLambdaPyast(lam.Body(), params[0], "item"); err != nil {
				return varRef{}, err
			} else if lok {
				if _, isBin := elt.(*pyast.BinaryExpr); isBin {
					elt = &pyast.ParenExpr{X: elt}
				}
				node.Code = wrapShim([]pyast.Param{{Name: "items", Type: "list"}}, &pyast.ListCompExpr{
					Elt: elt,
					Clauses: []pyast.CompClause{
						{Vars: []string{"item"}, Iter: &pyast.NameExpr{Name: "items"}},
					},
				})
			}
		}
	}
	if node.Code == "" {
		pred, perr := lambdaPredicate(call, "item")
		if perr != nil {
			return varRef{}, perr
		}
		node.Code = "def main(items: list) -> dict:\n    return {\"result\": [" + pred + " for item in items]}"
	}
	if src.nodeID != "" {
		node.Inputs = append(node.Inputs, Source{Path: []string{"items"}, BlockID: src.nodeID, Name: src.output})
		node.Types["items"] = TList(T("string"))
	}
	g.add(node)
	return varRef{nodeID: id, output: codeWholeValuePort(node), kind: "code"}, nil
}

// lambdaIn returns the single lambda argument of a list-operator call
// (map/filter/take), if present.
func lambdaIn(call *ast.CallExpr) (*ast.LambdaExpr, bool) {
	for _, a := range call.Args() {
		if lam, ok := a.(*ast.LambdaExpr); ok {
			return lam, true
		}
	}
	return nil, false
}

// lambdaPredicate renders a (param) -> bool lambda body to a Python
// expression using the given variable name.
func lambdaPredicate(call *ast.CallExpr, varName string) (string, error) {
	lam, ok := lambdaIn(call)
	if !ok {
		return "", &UnsupportedError{Construct: "list operator without lambda"}
	}
	params := lam.Params()
	if len(params) != 1 {
		return "", &UnsupportedError{Construct: "lambda with multiple params"}
	}
	return exprToPython(lam.Body(), params[0], varName)
}

// exprToPython renders a small ncoda expression subset to Python.
func exprToPython(e ast.Expr, ncodaParam, pyParam string) (string, error) {
	switch x := e.(type) {
	case *ast.IdentExpr:
		if x.Name() == ncodaParam {
			return pyParam, nil
		}
		return "", &UnsupportedError{Construct: fmt.Sprintf("lambda free var %q", x.Name())}
	case *ast.StringLit:
		b, _ := json.Marshal(x.Value())
		return string(b), nil
	case *ast.IntLit:
		return strconv.FormatInt(x.Value(), 10), nil
	case *ast.FloatLit:
		return pyast.FloatLiteralText(x.Value()), nil
	case *ast.BoolLit:
		if x.Value() {
			return "True", nil
		}
		return "False", nil
	case *ast.BinaryExpr:
		left, err := exprToPython(x.Left(), ncodaParam, pyParam)
		if err != nil {
			return "", err
		}
		right, err := exprToPython(x.Right(), ncodaParam, pyParam)
		if err != nil {
			return "", err
		}
		switch x.Op() {
		case "+":
			return "(" + left + " + " + right + ")", nil
		case "&&":
			return "(" + left + " and " + right + ")", nil
		case "||":
			return "(" + left + " or " + right + ")", nil
		case "??":
			// first-not-null（route-1a R4）：lambda 表达式内 `a ?? b` → python
			// `(a) if (a) is not None else (b)`（a 恒等式双引用，纯值安全）。
			return "(" + left + ") if (" + left + ") is not None else (" + right + ")", nil
		case "==":
			return "(" + left + " == " + right + ")", nil
		case "!=":
			return "(" + left + " != " + right + ")", nil
		case ">":
			return "(" + left + " > " + right + ")", nil
		case ">=":
			return "(" + left + " >= " + right + ")", nil
		case "<":
			return "(" + left + " < " + right + ")", nil
		case "<=":
			return "(" + left + " <= " + right + ")", nil
		}
		return "", &UnsupportedError{Construct: fmt.Sprintf("lambda operator %q", x.Op())}
	case *ast.UnaryExpr:
		operand, err := exprToPython(x.Operand(), ncodaParam, pyParam)
		if err != nil {
			return "", err
		}
		switch x.Op() {
		case "!":
			return "(not " + operand + ")", nil
		case "-":
			return "(-" + operand + ")", nil
		}
		return "", &UnsupportedError{Construct: fmt.Sprintf("lambda unary %q", x.Op())}
	case *ast.CallExpr:
		if fa, ok := x.Func().(*ast.FieldAccessExpr); ok {
			obj, err := exprToPython(fa.Obj(), ncodaParam, pyParam)
			if err != nil {
				return "", err
			}
			switch fa.FieldName() {
			case "contains":
				if len(x.Args()) != 1 {
					return "", &UnsupportedError{Construct: "contains arity"}
				}
				arg, err := exprToPython(x.Args()[0], ncodaParam, pyParam)
				if err != nil {
					return "", err
				}
				return "(" + arg + " in " + obj + ")", nil
			case "not_contains":
				if len(x.Args()) != 1 {
					return "", &UnsupportedError{Construct: "not_contains arity"}
				}
				arg, err := exprToPython(x.Args()[0], ncodaParam, pyParam)
				if err != nil {
					return "", err
				}
				return "(" + arg + " not in " + obj + ")", nil
			case "startsWith":
				if len(x.Args()) != 1 {
					return "", &UnsupportedError{Construct: "startsWith arity"}
				}
				arg, err := exprToPython(x.Args()[0], ncodaParam, pyParam)
				if err != nil {
					return "", err
				}
				return obj + ".startswith(" + arg + ")", nil
			case "endsWith":
				if len(x.Args()) != 1 {
					return "", &UnsupportedError{Construct: "endsWith arity"}
				}
				arg, err := exprToPython(x.Args()[0], ncodaParam, pyParam)
				if err != nil {
					return "", err
				}
				return obj + ".endswith(" + arg + ")", nil
			}
		}
		return "", &UnsupportedError{Construct: "lambda call"}
	}
	return "", &UnsupportedError{Construct: fmt.Sprintf("lambda expression %T", e)}
}

var listOpNames = map[string]bool{"filter": true, "take": true, "map": true}

func isListOpName(name string) bool { return listOpNames[name] }

// emitTernaryJunction lowers a **value-position** ternary
// `cond ? thenVal : elseVal` into its junction.
//
// 合成 return 的作用域（A12-H15 收口，2026-09-17）：三元被降低成「两条臂各带
// 一个**合成** return 的 if」；该合成 return 通过 `emitReturnStmt` 把
// `g.branchReturns` 置真 —— 那是**表达式内部**的机制（宿主据此判「这条臂产出了
// 尾值」= emitBranch 的 `produced`）。但它**不是所在语句臂的返回事实**：一旦
// 泄漏出去，宿主 if 会把「臂里有个三元表达式」误判成「这条臂产出了尾值」⇒
// 值通道收集到**单臂**候选 ⇒ 变量通道（逐变量 phi，`emitIf` 的
// `if len(agg.Refs) == 0` 门）被跳过 ⇒ 兄弟臂的写值在产物里**不存在**
// （A12-H15：静默丢值，且可逆性判据①满射破裂）。
//
// 纪律与 `inline` 边界同一条（「never pollute the caller's end decision」）：
// **表达式不得污染调用方的返回事实**。此处复原 `branchReturns`，使三元对宿主
// 呈现为「一个表达式」，而不是「一条 return」。
// slotName = **宿主提供的槽名**（J §4 的第四项数据）：值位置三元被绑到命名目标
// （`T v = …` / `v = …`，两者在本语言等价 ⇒ 同一条路径）时，那个目标名就是该
// junction 的整值槽名；无命名目标（`return (c) ? a : b;` / 调用实参 / 模板插值）
// 传空 ⇒ 无名（J1 允许，走平台对无名组的拼写）。
func (g *Builder) emitTernaryJunction(prevIDs []string, firstPort string, cond, thenVal, elseVal ast.Expr, slotName string) ([]string, error) {
	savedBranchReturns := g.branchReturns
	synth := ternarySynthIf(cond, thenVal, elseVal)
	g.syntheticTernary++
	ids, _, err := g.emitIf(synth, prevIDs, firstPort, slotName)
	g.syntheticTernary--
	g.branchReturns = savedBranchReturns
	return ids, err
}

// ternarySynthIf builds the synthesized `if` for a value-position ternary.
// 嵌套 else 三元（`a ? x : (b ? y : z)`）合成为**嵌套 if 语句**（不是
// `return <内层三元>`）—— 使 else 臂块恰好一条语句（内层 IfStmt）⇒ 与
// `else if` / 嵌套 `else { if }` 两种拼写同形 ⇒ 由 FuseBranchChains 归一到
// **一个 N 臂 condition 节点**（否则内层三元另起一个 condition 节点 ⇒ 首编非规范、
// 一轮往返拓扑漂移，examples/nodecoda/07_template_transform 一手）。
func ternarySynthIf(cond, thenVal, elseVal ast.Expr) *ast.IfStmt {
	elseBody := []ast.Stmt{ast.NewReturnStmt(elseVal)}
	if inner, ok := elseVal.(*ast.TernaryExpr); ok {
		elseBody = []ast.Stmt{ternarySynthIf(inner.Condition(), inner.ThenVal(), inner.ElseVal())}
	}
	return ast.NewIfStmt(cond,
		ast.NewBlock([]ast.Stmt{ast.NewReturnStmt(thenVal)}),
		nil,
		ast.NewBlock(elseBody))
}
