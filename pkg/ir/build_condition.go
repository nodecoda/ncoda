// IR builder — 按语句/节点族拆分的文件之一（Tier-2 review #9:拆文件不拆包,
// diff 局部化）。同一包内符号共享;本文件族的归属见各函数注释。
package ir

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/pyast"
	"github.com/nodecoda/ncoda/pkg/pygen"
)

// symmetricConditionOp reports whether a comparison is order-insensitive:
// `a == b` ⟺ `b == a`（`!=` 同理）。**非对称算子**（`<` `>` `<=` `>=`）换边
// **换意思** ⇒ 那不是拼写自由度，是语义，**任何规范方向都不得重排它们**。
func symmetricConditionOp(op string) bool { return op == "==" || op == "!=" }

// canonicalConditionExpr returns the condition in canonical operand order:
// 对称算子（`==` / `!=`）**值路径在左、字面量在右**；非对称算子**原样返回**。
//
// 规范方向的一手依据（平台语料）：1340 份解码产物里 condition 条目的左操作数
// **100% 是 ref**（字面量在左 **0 例**），右操作数字面量 123 例 ⇒ 平台范式就是
// 「值路径在左」，「字面量在左」是**没有先例的拼写**（本仓纪律：不发明无先例拼写）。
//
// 为什么必须有它：`conditionNativeDecision` 是**不对称**判据（left 必须是值路径、
// right 必须是字面量），于是 `if ("x" == raw)` 整条被判「超出原生条件面」⇒ 造一个
// python-code-shim（死载体：平台**能**内联承载，A 侧 `if (raw == "x")` 即证）
// + 条件槽退化成「读 shim 结果 == true」，反编译再读回 `v1 == true`
// ⇒ **往返漂移**（R-64：同一意思两种产物 + 一侧往返坏）。
// 规范化后两种拼写走**同一条**路径 ⇒ 逐字节同产物、往返保真。
//
// 落点纪律：判据（本文件的 conditionNativeDecision）与发射（build_return.go 的
// conditionToSelector）**共用本函数** —— 同一事实两处实现 = 两处真相。
func canonicalConditionExpr(e *ast.BinaryExpr) *ast.BinaryExpr {
	if e == nil || !symmetricConditionOp(e.Op()) {
		return e
	}
	l, r := e.Left(), e.Right()
	if isLiteralConditionOperand(l) && !isLiteralConditionOperand(r) {
		return ast.NewBinaryExpr(e.Op(), r, l)
	}
	return e
}

// conditionNativeDecision is the single source of truth for
// native-vs-materialize on a comparison condition. 判据吃**规范序**（与发射同解）。
func (g *Builder) conditionNativeDecision(e *ast.BinaryExpr) bool {
	e = canonicalConditionExpr(e)
	// 右操作数原生 = ①目标承载 ref × 字面量（`caps.Condition.RefLiteral`）且右边是字面量，
	// 或 ②目标承载 ref × ref（`caps.Condition.RefRef`）且右边是可求值引用。
	// **两条都读能力表**（2026-09-27）：此前 ① 写死为 true ⇒ `RefLiteral` 是死轴，
	// 落点不由能力表决定 —— 与 json 内建同一病根（`recordJSONBuiltinPlacement` 修法）。
	rightNative := false
	if isLiteralConditionOperand(e.Right()) {
		rightNative = g.caps == nil || g.caps.Condition.RefLiteral
	}
	if !rightNative && g.caps != nil && g.caps.Condition.RefRef && isPlainConditionOperand(e.Right()) {
		rightNative = true
	}
	return isPlainConditionOperand(e.Left()) && rightNative
}

// conditionNative reports the pass-0 mark for a comparison condition,
// falling back to the same decision for emission-synthesized conditions.
func (g *Builder) conditionNative(e *ast.BinaryExpr) bool {
	if g.condMarks != nil {
		if native, ok := g.condMarks[e]; ok {
			return native
		}
	}
	return g.conditionNativeDecision(e)
}

// indexExprBindName builds a stable binding name for an array-element
// access expression (r[0] → "r_0", rec.result[0] → "rec_result_0") used
// by the condition-materialization binder when the element access resolved
// onto a dotted output port (record-array member chains, E5-b).
func indexExprBindName(e *ast.IndexExpr) string {
	base := e.Obj()
	if fa, ok := base.(*ast.FieldAccessExpr); ok {
		root, path := splitFieldAccess(fa)
		name := root
		if len(path) > 0 {
			name = root + "_" + strings.Join(path, "_")
		}
		return name + "_0"
	}
	if id, ok := base.(*ast.IdentExpr); ok {
		return id.Name() + "_0"
	}
	return "arr_0"
}

// splitFieldAccess decomposes a field-access chain into its root ident
// and field path (obj.a.b → "obj", ["a","b"]).
func splitFieldAccess(expr *ast.FieldAccessExpr) (string, []string) {
	var fields []string
	current := ast.Expr(expr)
	for {
		fa, ok := current.(*ast.FieldAccessExpr)
		if !ok {
			break
		}
		fields = append(fields, fa.FieldName())
		current = fa.Obj()
	}
	if _, ok := current.(*ast.IdentExpr); !ok {
		return "", nil
	}
	for i, j := 0, len(fields)-1; i < j; i, j = i+1, j-1 {
		fields[i], fields[j] = fields[j], fields[i]
	}
	return current.(*ast.IdentExpr).Name(), fields
}

// fieldChainHasConst0Index reports whether a member-field chain contains a
// constant-0 array element marker (E5-b: v.data[0].bgm_url). Plain member
// chains and string-subscript member access (obj["code"]) do not count.
func fieldChainHasConst0Index(x *ast.FieldAccessExpr) bool {
	cur := ast.Expr(x)
	for {
		switch b := cur.(type) {
		case *ast.FieldAccessExpr:
			cur = b.Obj()
		case *ast.IndexExpr:
			if il, ok := b.Index().(*ast.IntLit); ok && il.Value() == 0 {
				return true
			}
			cur = b.Obj()
		default:
			return false
		}
	}
}

// fieldChainBindingName builds the code-parameter name of a member-field
// chain, mirroring the plain-chain "<root>_<segments>" convention with
// constant-0 element markers dropped (v.data[0].bgm_url → v_data_bgm_url)
// and string subscripts folded in as member names (obj["code"].value →
// v_code_value). ok=false when the chain is not rooted at an ident or
// carries a non-constant index.
func fieldChainBindingName(x *ast.FieldAccessExpr) (string, bool) {
	segs := []string{}
	cur := ast.Expr(x)
	for {
		switch b := cur.(type) {
		case *ast.FieldAccessExpr:
			segs = append([]string{b.FieldName()}, segs...)
			cur = b.Obj()
		case *ast.IndexExpr:
			switch idx := b.Index().(type) {
			case *ast.IntLit:
				if idx.Value() != 0 {
					return "", false
				}
				cur = b.Obj() // E5-b item-0 marker — dropped from the name
			case *ast.StringLit:
				segs = append([]string{idx.Value()}, segs...)
				cur = b.Obj()
			default:
				return "", false
			}
		case *ast.IdentExpr:
			segs = append([]string{b.Name()}, segs...)
			if len(segs) == 0 {
				return "", false
			}
			name := segs[0]
			for _, s := range segs[1:] {
				name += "_" + s
			}
			return name, true
		default:
			return "", false
		}
	}
}

// e5bFieldChainBinding binds a whole member-field chain that crosses a
// constant-0 record-array element onto its E5-b dotted output port (see
// renderFieldAccessPython). ok=false keeps the legacy split-at-index path:
// no constant-0 marker, no dotted-port resolution (json.parse<array<T>>
// element guards stay on the guarded-extraction path), or unresolvable
// root.
func (g *Builder) e5bFieldChainBinding(x *ast.FieldAccessExpr, bindings *[]textBinding) (string, bool, error) {
	if !fieldChainHasConst0Index(x) {
		return "", false, nil
	}
	if _, ok := g.resolveValueRef(x); !ok {
		return "", false, nil
	}
	name, ok := fieldChainBindingName(x)
	if !ok {
		return "", false, nil
	}
	rendered, err := bindConditionPython(name, x, g, bindings)
	if err != nil {
		return "", false, err
	}
	return rendered, true, nil
}

// materializeCondition lowers a non-direct condition expression to a
// CodeRunner computing the boolean result (legacy makeInternalCodeNode
// path), binding every referenced producer as an input parameter.
func (g *Builder) materializeCondition(cond ast.Expr, flow *[]string) (varRef, error) {
	var bindings []textBinding
	// P5.5: pyast path when the condition shape is provably byte-equivalent
	// to renderConditionPython's output; otherwise the legacy template keeps
	// today's exact bytes (bindConditionPython dedupes, so a partial pyast
	// binding set is harmless on fallback).
	code, ok, err := shimCodeFor(cond, g, &bindings)
	if err != nil {
		return varRef{}, &UnsupportedError{Construct: fmt.Sprintf(
			"if condition: %s (assign the value to a variable first, or write foreign code)",
			constructOf(err))}
	}
	if !ok {
		expr, rerr := renderConditionPython(cond, g, &bindings)
		if rerr != nil {
			return varRef{}, &UnsupportedError{Construct: fmt.Sprintf(
				"if condition: %s (assign the value to a variable first, or write foreign code)",
				constructOf(rerr))}
		}
		code = ShimMarker + fmt.Sprintf("def main(%s) -> dict:\n    return {\"result\": %s}",
			strings.Join(shimParamNames(bindings), ", "), expr)
	}
	sources := make([]Source, 0, len(bindings))
	types := map[string]*Type{}
	for _, b := range bindings {
		sources = append(sources, Source{Path: []string{b.name}, BlockID: b.ref.nodeID, Name: b.ref.output})
		// Legacy bindSelector stamps the bound variable with the source
		// selector's type (ToCodePort); propagate the producer port type so
		// the Code node input carries the same value_type (number/int → number).
		// refType resolves the authoritative conclusion and errors instead of
		// forging string when the producer type is unknown.
		bt, berr := g.refType(b.ref, nil)
		if berr != nil {
			return varRef{}, &UnsupportedError{Construct: fmt.Sprintf(
				"if condition: %s (assign the value to a variable first, or write foreign code)",
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
		OutTypes:  map[string]*Type{"result": T("boolean")},
		Condition: true,
		Synthetic: true,
	})
	*flow = []string{id}
	// 引用**真实数据口** result（OutTypes/OutOrder 只声明 result）；返回 "output"
	// 会指向 code 渲染默认补的 string 空端口 ⇒ 条件操作数读成 string ⇒ 类型判定
	// 落空 ⇒ 发射 `== "true"` **字符串比较**（反编译再读回 `v == "true"`，R8 的
	// 最后一腿）。与 emitExprCodeOut 的同类修复（2026-09-02）同一把尺子。
	return varRef{nodeID: id, output: "result", kind: "code"}, nil
}

// renderFieldAccessPython renders a field-access expression for the Python
// materializers. A plain ident chain (user.name) binds as "<root>_<path>";
// a chain rooted in an index over a json.parse<array<T>> variable
// (items[0].content) renders as a guarded .get extraction (G12-2),
// preserving the hand-written corpus semantics (`str(it.get("content") or
// "")` over an OOB-guarded element).
func renderFieldAccessPython(x *ast.FieldAccessExpr, g *Builder, bindings *[]textBinding) (string, error) {
	root, path := splitFieldAccess(x)
	if root != "" {
		name := root
		if len(path) > 0 {
			name = root + "_" + strings.Join(path, "_")
		}
		return bindConditionPython(name, x, g, bindings)
	}
	// E5-b record-array element member chains (v.data[0].bgm_url,
	// content_chunks[0].text) resolve onto the platform's dotted output
	// port (data.bgm_url — item-0 drill) exactly like plain member chains.
	// The legacy split-at-index fallback below binds only the array field
	// (whole port value) and turns the trailing member into a string
	// subscript of the array (listVar["bgm_url"]) — losing the [0] element
	// marker and making round2 decompile to list["str"] (L4 INDEX_TYPE).
	if rendered, ok, err := g.e5bFieldChainBinding(x, bindings); err != nil {
		return "", err
	} else if ok {
		return rendered, nil
	}
	// json.parse<array<T>> element field: items[0].content →
	// str((items[0] if len(items) > 0 else {}).get("content") or "").
	if ix, ok := x.Obj().(*ast.IndexExpr); ok {
		if id, ok2 := ix.Obj().(*ast.IdentExpr); ok2 {
			if ref, ok3 := g.varToOutput[id.Name()]; ok3 && ref.jsonParseArray {
				obj, err := renderConditionPython(x.Obj(), g, bindings)
				if err != nil {
					return "", err
				}
				key, _ := json.Marshal(x.FieldName())
				return "str((" + obj + ").get(" + string(key) + ") or \"\")", nil
			}
		}
	}
	// generic fallback: render the base expression, then index the field
	// off the rendered object.
	obj, err := renderConditionPython(x.Obj(), g, bindings)
	if err != nil {
		return "", err
	}
	key, _ := json.Marshal(x.FieldName())
	return obj + "[" + string(key) + "]", nil
}

// conditionPredicateValueForm maps a **条件位专属** surface predicate onto its
// **值形态**函数调用，供**物化**渲染器（值表达式 / python shim）使用。
//
// 为什么需要它：ncoda 把平台的 contain / not-contain 条件算子拼成**方法形态**
// （`obj.contains(arg)`），`conditionToSelector` 的 `CallExpr` 分支把它映射到平台
// **原生条件算子**（op 7）—— 于是它平时根本不进值渲染器。但还有一种位置：
// **混合逻辑守卫**（`a || (b && c)`）平台扁平条件表装不下 ⇒ 整条守卫被
// `materializeMixedLogicCondition` 当**值表达式**物化 ⇒ 方法形态在值渲染器里
// 没有对应物 ⇒ `UnsupportedError: call "s.contains" in expression`（整条编译不过）。
//
// 一手（2026-09-30）：真实语料 `7651538127668903951_SC193_rensheng_fuben` 的
// `if (failed139483 || empty(res139483) && res139483.contains("/placeholder"))`
// 编译 `COMPILE_ERROR`。（修前该条**能编**，但产物是**静默错译**的
// `failed || empty || contains` 扁平表 —— R-172 的混合逻辑根因修复把静默错译
// 换成了 loud 拒收；本修法把它换回「能编且语义正确」。）
//
// 返回的是**函数形态**（`contains(obj, arg)`）—— 那正是既有的 calc builtin
// 拼写，所以两个值渲染器（字符串模板 `renderConditionPython` / pyast
// `lowerConditionPyast`）都经**既有的 calc-builtin 通道**落到同一份字节
// （一处定义）；反向由 `shim_reverse.go::mapShimIn` 的 `contains` 支读回。
//
// `obj.not_contains(arg)` 与之同族，值形态 = `empty(contains(obj, arg))`：
// 平台没有「不包含」的值算子，但**能模拟**（shim 里就是 `not (arg in obj)`）；
// 两个组成件都是既有 calc builtin ⇒ 不新增语言面。选择它而不是新增内建
// `not_contains` 的理由 = **反向已经闭合**：`shim_reverse.go` 的
// `mapShimUnary("not")` ∘ `mapShimIn("in")` 把 `(not (arg in obj))` 读回
// `empty(contains(obj, arg))` —— 与我们写出去的是**同一拼写**（规范型），
// 而 `not_contains(obj, arg)` 这种函数形态源码里写不出来（`binding/symbol.go`
// 未声明）⇒ 读回即不可编译。一手（2026-10-01）：`empty(contains(n, "a"))` 与
// `n.not_contains("a")` 在混合守卫里产出**同一份 shim 字节**
// `((not ("a" in n)) or …)`。
func conditionPredicateValueForm(x *ast.CallExpr) (*ast.CallExpr, bool) {
	fa, ok := x.Func().(*ast.FieldAccessExpr)
	if !ok || len(x.Args()) != 1 || len(x.TypeArgs()) != 0 {
		return nil, false
	}
	// 只在**可逆**（组成件都是可 lowering 的 calc builtin）时才改写 ——
	// 判据与 `lowerCalcBuiltin` 同源（`pygen.IsLowerable`），不是第二份清单。
	switch fa.FieldName() {
	case "contains":
		if !pygen.IsLowerable("contains") {
			return nil, false
		}
		return ast.NewCallExpr(ast.NewIdentExpr("contains"), nil, []ast.Expr{fa.Obj(), x.Args()[0]}), true
	case "not_contains":
		if !pygen.IsLowerable("contains") || !pygen.IsLowerable("empty") {
			return nil, false
		}
		inner := ast.NewCallExpr(ast.NewIdentExpr("contains"), nil, []ast.Expr{fa.Obj(), x.Args()[0]})
		return ast.NewCallExpr(ast.NewIdentExpr("empty"), nil, []ast.Expr{inner}), true
	}
	return nil, false
}

// renderConditionPython renders an ncoda expression to Python, binding
// referenced producers into bindings (first reference wins, legacy
// bindSelector semantics). Field accesses bind as "<root>_<path>" like the
// legacy materializer.
func renderConditionPython(e ast.Expr, g *Builder, bindings *[]textBinding) (string, error) {
	switch x := e.(type) {
	case nil:
		return "None", nil
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
	case *ast.ArrayLit, *ast.MapLit:
		// 结构字面量（数组 / 映射）在**条件位与值位**：与 `renderLiteralPython`
		// 同一实现（一处定义）—— 这是本渲染器对「结构字面量」的唯一决定处。
		//
		// 规范形（R-82）：平台条件槽只装得下**标量与 ref**（语料 865 个操作数：
		// 字面量全为标量，object/list 声明的一律是 ref），所以「比较的操作数是
		// 结构字面量」不能坐进槽 ⇒ **整个表达式物化成 shim 值**，shim 文本里按
		// python 字面量原样写（`[1, 2, 3]` / `{"k": 1}`），与同族（`a == "x"+"y"`、
		// `len(a) == 3`）的既有走法同形 —— 反编译读回 `let vN = <比较>; if (vN == true)`。
		//
		// 复用它而不是在此另写一份：`renderLiteralPython` 已实现数组/映射的
		// 规范渲染（键序 / 动态键报错 / 递归元素），两处各写一份 = 同一事实两份权威。
		return renderLiteralPython(e, g, bindings)
	case *ast.IdentExpr:
		return bindConditionPython(x.Name(), x, g, bindings)
	case *ast.FieldAccessExpr:
		// 枚举成员是**字面量**（R-62）：中性 IR 把 `enum → string`（`typeOf` 的
		// NamedTypeRef 分支），值路径早已把 `E.a` / `E.a.value` 落成它的**线值**
		// （`pkg/ir/build_expr.go` 的 `enumMemberValue` 分支）。表达式位置
		// 必须同解 —— 否则 `E t = E.a;` 会被当普通字段链拼出不存在的名字 `E_a`
		// ⇒ `resolveValueRef` 失败 ⇒ `UnsupportedError`（实测：四个目标全红，
		// 而 `return E.a.value;` 全绿 ⇒ 同一个表达式「一个位置能编、另一个编不了」）。
		if value, ok := g.enumMemberValue(x); ok {
			return pythonJSONString(value), nil
		}
		return renderFieldAccessPython(x, g, bindings)
	case *ast.IndexExpr:
		// Array element access: record-array member chains (E5-b,
		// rec.result[0].output) resolve onto dotted output ports and bind
		// like a field access; scalar arrays (split(...)[0]) and
		// json.parse<array<T>> elements (G12-2) have no dotted-port form,
		// so they render as plain Python indexing on the bound array ref
		// (code shim, P0 fallback — no native form).
		if _, ok := g.resolveValueRef(x); ok {
			name := indexExprBindName(x)
			return bindConditionPython(name, x, g, bindings)
		}
		obj, err := renderConditionPython(x.Obj(), g, bindings)
		if err != nil {
			return "", err
		}
		idx, err := renderConditionPython(x.Index(), g, bindings)
		if err != nil {
			return "", err
		}
		// G12-2: element access on a json.parse<array<T>> variable keeps the
		// out-of-bounds guard the hand-written corpus used
		// (`items[i] if i < len(items) else {}`).
		if id, ok := x.Obj().(*ast.IdentExpr); ok {
			if ref, ok2 := g.varToOutput[id.Name()]; ok2 && ref.jsonParseArray {
				return obj + "[" + idx + "] if len(" + obj + ") > " + idx + " else {}", nil
			}
		}
		return obj + "[" + idx + "]", nil
	case *ast.BinaryExpr:
		left, err := renderConditionPython(x.Left(), g, bindings)
		if err != nil {
			return "", err
		}
		right, err := renderConditionPython(x.Right(), g, bindings)
		if err != nil {
			return "", err
		}
		op := x.Op()
		switch op {
		case "&&":
			op = "and"
		case "||":
			op = "or"
		case "??":
			// first-not-null (route-1a R4): `a ?? b` → `(a if a is not None else b)`.
			return "(" + left + " if " + left + " is not None else " + right + ")", nil
		}
		return "(" + left + " " + op + " " + right + ")", nil
	case *ast.UnaryExpr:
		operand, err := renderConditionPython(x.Operand(), g, bindings)
		if err != nil {
			return "", err
		}
		op := x.Op()
		if op == "!" {
			op = "not "
		}
		return "(" + op + operand + ")", nil
	case *ast.TernaryExpr:
		c, err := renderConditionPython(x.Condition(), g, bindings)
		if err != nil {
			return "", err
		}
		t, err := renderConditionPython(x.ThenVal(), g, bindings)
		if err != nil {
			return "", err
		}
		el, err := renderConditionPython(x.ElseVal(), g, bindings)
		if err != nil {
			return "", err
		}
		return "(" + t + " if " + c + " else " + el + ")", nil
	case *ast.TemplateString:
		var parts []string
		for _, part := range x.Parts() {
			if part.Expr == nil {
				parts = append(parts, pythonJSONString(part.Text))
				continue
			}
			rendered, err := renderConditionPython(part.Expr, g, bindings)
			if err != nil {
				return "", err
			}
			parts = append(parts, "str("+rendered+")")
		}
		if len(parts) == 0 {
			return `""`, nil
		}
		return strings.Join(parts, " + "), nil
	case *ast.CallExpr:
		// 条件位专属的方法形态谓词（`obj.contains(arg)`）→ 函数形态（值渲染器
		// 没有方法调用形态）。必须与 pyast 路径 `lowerConditionPyast` **同解**
		// （P5.6 字节等价）；两处都只调本函数，规则只有一份。
		if fn, ok := conditionPredicateValueForm(x); ok {
			return renderConditionPython(fn, g, bindings)
		}
		if calleeName(x.Func()) == "file" {
			// file(x) 透明渲染: 运行时就是 x（URL 字符串）—— 渲染内层
			// 表达式, 不产生 Python "file(...)" 调用（该名字运行期不存在,
			// 静默 NameError 违反铁律 3）。可解析参数在到达渲染器前已被
			// resolveValueRef 解包, 这里只剩不可解析参数。
			if len(x.Args()) == 1 {
				return renderConditionPython(x.Args()[0], g, bindings)
			}
			return "", &UnsupportedError{Construct: "call \"file\" expects 1 argument"}
		}
		// G10 calc builtins: pure calculation functions (len/join/trim/
		// to_string/int/contains/split) render as Python calls with bound
		// arguments (arrays/maps via renderLiteralPython), so they
		// materialize in any expression position instead of failing
		// "call X in expression" (§5.1 composition renderer stays
		// invocation-free; these are compiler-owned primitives).
		if isCalcBuiltin(calleeName(x.Func())) {
			// P5 pyast path: when the builtin has a byte-equivalent pyast
			// lowering AND every argument lowers cleanly, route through the
			// Python-subset AST instead of the string template. Any shape
			// the current pyast node set cannot express falls back to the
			// shared string template (pygen.Template — P5.3, one definition
			// for the IR fallback and the dify legacy renderer); byte
			// equivalence is the gate (§ PYTHON-SUBSET-GENERATOR P5.1).
			if pa, ok2, err := lowerCalcBuiltin(x, g, bindings); err != nil {
				return "", err
			} else if ok2 {
				// 顶层二元 → **恒加括号**（既有字节约定）：本分支与 pyast 路径
				// （`lowerShimExpr` / `lowerCondChild`）必须同一把尺子。
				//
				// 旧实现直传 `pa`：calc-builtin 降到二元时（`repeat(a,b)` → `[a] * b`、
				// `div(a,b)` → `(a // b)` 除外）**不带**括号，而 pyast 序列化器给嵌套
				// 二元加括号 ⇒ 同一语义两条拼写两种字节、一轮不动点破裂。
				// 一手（2026-09-26，C 组 X15，`I1b ARTIFACT_DRIFT/PARAM@r2`，首个差异
				// 正是这一行）：
				//
				//	concat(repeat(20, div(n,20)), [n % 20]) → (([20] * (n // 20)) + [(n % 20)])
				//	repeat(20, div(n,20)) + [n % 20]         → ([20] * (n // 20) + [(n % 20)])
				//
				// 判据 = internal/compiler/cozebiz_shim_print_single_ruler_contract_test.go
				// （「shim 打印只有一处」那条尺子的 calc-builtin 面）。
				if _, isBin := pa.(*pyast.BinaryExpr); isBin {
					pa = &pyast.ParenExpr{X: pa}
				}
				return pyast.SerializeExpr(pa), nil
			}
			args := make([]string, 0, len(x.Args()))
			for _, a := range x.Args() {
				rendered, err := renderLiteralPython(a, g, bindings)
				if err != nil {
					return "", err
				}
				args = append(args, rendered)
			}
			if py, ok := pygen.Template(calleeName(x.Func()), args); ok {
				return py, nil
			}
			return "", &UnsupportedError{Construct: fmt.Sprintf("call %q in expression", calleeName(x.Func()))}
		}
		// Non-declarative call (user function / unknown): the combinator
		// renderer translates "composition", never "invocation" (§5.1).
		// Name the callee so entry-point guidance stays navigable.
		if name := calleeName(x.Func()); name != "" {
			return "", &UnsupportedError{Construct: fmt.Sprintf("call %q in expression", name)}
		}
	}
	return "", &UnsupportedError{Construct: fmt.Sprintf("condition materialization %T", e)}
}

func pythonJSONString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			switch {
			case r >= 0x20 && r <= 0x7e:
				b.WriteRune(r)
			case r <= 0xffff:
				fmt.Fprintf(&b, `\u%04x`, r)
			default:
				r -= 0x10000
				fmt.Fprintf(&b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// bindConditionPython binds one referenced producer to a code parameter,
// reusing an existing binding if the same variable was already bound.
func bindConditionPython(name string, e ast.Expr, g *Builder, bindings *[]textBinding) (string, error) {
	// G10: a literal-bound variable (let n = 400) inlines its Python
	// literal — literal lets live in litVars (no output node), so there is
	// no ref to bind and the constant value renders directly. Unblocks
	// arithmetic/template/index expressions over literal lets.
	if lv, ok := g.litVars[name]; ok {
		switch lv.typ {
		case "integer", "number":
			return lv.value, nil
		case "boolean":
			if lv.value == "true" {
				return "True", nil
			}
			return "False", nil
		}
	}
	ref, ok := g.resolveValueRef(e)
	if !ok {
		return "", &UnsupportedError{Construct: fmt.Sprintf("condition materialization operand %q not resolvable", name)}
	}
	for _, b := range *bindings {
		if b.key == name {
			return b.name, nil
		}
	}
	label := takeBindingLabel(bindings, ref.output)
	*bindings = append(*bindings, textBinding{key: name, name: label, ref: ref})
	return label, nil
}

// portTypeOf resolves the neutral type of a producer's output port
// (used by condition materialization to stamp Code input types like the
// legacy bindSelector ToCodePort path). Dotted ports resolve by their
// first segment when no exact match exists; unknown ports return nil.
func (g *Builder) portTypeOf(nodeID, output string) *Type {

	// Aggregates are structural outputs, not ordinary nodes with an implicit
	// Group1 port. Resolve them first so a multi-group phi exposes exactly the
	// ports emitted by the renderer.
	if n, ok := g.findAny(nodeID).(*Aggregate); ok {
		if len(n.Groups) > 0 {
			if output == "" {
				props := make(map[string]*Type, len(n.Groups))
				for _, group := range n.Groups {
					props[group.Name] = group.Type
				}
				return TObject(props)
			}
			// Dotted group path (text.wenan): resolve the group's merged
			// record field through the group Type's Props. This is the phi
			// value the renderer exposes on that port — never forge string.
			if i := strings.Index(output, "."); i >= 0 {
				name := output[:i]
				for _, group := range n.Groups {
					if group.Name == name {
						if ft := dottedAggGroupField(group.Type, strings.TrimPrefix(output, name+".")); ft != nil {
							return ft
						}
					}
				}
			}
			for _, group := range n.Groups {
				if group.Name == output {
					return group.Type
				}
			}
		} else if output == n.SingleGroupName() || output == "" {
			// Legacy one-group aggregates retain the historical Group1 port.
			// 登记（第七次 S2-gate C8）：零候选（`len(n.Refs)==0`）时这里
			// **静默返回 `T("string")`**——「没有值」在类型查询通道被当成
			// string 值源，正是 AGENTS.md 明令禁止的「推断型静默 fallback」。
			// 今日未构造出改变产物的形状（产物里的 `Group1: string` 另有
			// legacy 兜底来源），故按 fail-loud 缺口登记，收口需自己的 S0-S6。
			// 见 docs/ideas-todo/2026-09-11-branch-port-obligation-single-list.md §16.1-C8。
			if len(n.Refs) > 0 {
				return g.portTypeOf(n.Refs[0].Producer, n.Refs[0].Output)
			}
			return T("string")
		}
	}
	if t := portTypeOfNode(g.findAny(nodeID), output); t != nil {
		return t
	}
	// The entry Start node lives on the graph (graph.Entry), not in the
	// statement node registry — resolve its parameter ports directly so a
	// plain boolean operand (`if (flag)`) can lower to the platform's
	// unary isTrue check (op 11).
	if g.graph != nil && g.graph.Entry != nil && g.graph.Entry.ID == nodeID {
		for _, p := range g.graph.Entry.Params {
			if p.Name == output {
				return p.Type
			}
		}
	}
	return nil
}

// dottedAggGroupField resolves a dotted member path against an aggregate
// group's merged value shape (record Props chain). Returns nil on non-record
// shapes / unknown fields so the caller keeps the authoritative diagnostic.
//
// A group whose merged value is a record ARRAY (data: array<RT8>, E5-b) is
// the [0]-drill form: the platform ref name drops the constant-0 element
// marker (data.bgm_url = data[0].bgm_url — see resolveValueRef), so the
// dotted remainder applies to the ELEMENT record, never to the array port
// itself. Array-typed ports followed by a member segment therefore descend
// through the element type first.
func dottedAggGroupField(base *Type, path string) *Type {
	cur := base
	for _, field := range strings.Split(path, ".") {
		// E5-b [0]-drill: a list-typed port/field consumed by an element
		// member segment was accessed as <list>[0] in the source; the index
		// marker was dropped from the output path. Descend to the element
		// type before the member lookup.
		if cur != nil && (cur.Kind == "list" || cur.Kind == "array") && cur.Elem != nil {
			cur = cur.Elem
		}
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

func portTypeOfNode(n Node, output string) *Type {
	switch nd := n.(type) {
	case *Start:
		for _, p := range nd.Params {
			if p.Name == output {
				return p.Type
			}
		}
	case *CodeRun:
		// 整值标记口（wholeValueMarker）：多口 code 节点的值由一组字段口承载 ⇒
		// 绑定写标记口（codeWholeValuePort）。它是**端口名**，不是形态守卫 ——
		// 本函数按口名分派，产出方必须对整值口作答（见 wholeValueRecordType）。
		if output == wholeValueMarker {
			if t := wholeValueRecordType(nd); t != nil {
				return t
			}
		}
		if t, ok := nd.OutTypes[output]; ok {
			return t
		}
		if t, ok := nd.Types[output]; ok {
			return t
		}
		// Dotted structural output: json.parse<Record>/tool structural outputs
		// expose one OutTypes entry per field, and the canvas references them
		// as result.field (port "output" is the whole record).  Resolve
		// output.<field> → OutTypes[field]; also accept a single structural
		// port whose name is the dotted first segment, then its field.
		if i := strings.Index(output, "."); i >= 0 {
			if t, ok := nd.OutTypes[output[i+1:]]; ok {
				return t
			}
			if t, ok := nd.OutTypes[output[:i]]; ok {
				return t
			}
		}
		// 记录 shim 的**成员口**：shim 声明的唯一容器口承载**整个记录 schema**
		// （容器端口的声明 = schema 载体），而成员引用用**扁平成员名**寻址
		// （`url`），容器口名（`result`）不出现在引用里 ⇒ 该端口的类型是
		// **成员类型**，不是容器自身。回落到 `singleOutputKey` 会把容器类型
		// 当成成员类型（一手实测 R-13：两臂尾值 `v<arm>.url` / `v<hoist>.url`
		// 被查成 record ⇒ junction 被误分类成「record 收敛」⇒ 逐字段组
		// （Groups）⇒ 流程尾 attempt 的渲染器按「Groups ⇒ 臂只写了提升变量
		// ⇒ 无返回值」丢弃 End 的值绑定 ⇒ **出口值静默消失**、零诊断，
		// 产物 6 → 3 节点）。
		if k, ok := singleOutputKey(nd.OutTypes); ok {
			if cont := nd.OutTypes[k]; cont != nil && cont.Props != nil {
				if member, ok := cont.Props[output]; ok && member != nil {
					return member
				}
			}
			return nd.OutTypes[k]
		}
	case *LLMCall:
		if t, ok := nd.OutTypes[output]; ok {
			return t
		}
		if t := nestedOutputType(nd.OutTypes, output); t != nil {
			return t
		}
		switch {
		case output == "reasoning_content", output == "finish_reason":
			return T("string")
		case output == "usage":
			return TObject(map[string]*Type{})
		case strings.HasPrefix(output, "usage."):
			return T("integer")
		}
		// LLMEnvelopeOutput ("response") is the complete LLMResponse<T> record:
		// {text: T, reasoning_content, usage, finish_reason}. The payload type
		// lives on OutTypes["text"]; every other metadata field is string if
		// not structurally declared. Resolve the envelope and its dotted
		// fields (response.text) here so the port registry is honest about a
		// bare llm(...) result, never a forged string.
		if output == LLMEnvelopeOutput || strings.HasPrefix(output, LLMEnvelopeOutput+".") {
			props := map[string]*Type{"text": nd.OutTypes["text"], "reasoning_content": T("string"), "finish_reason": T("string"), "usage": TObject(map[string]*Type{})}
			if output == LLMEnvelopeOutput {
				return TObject(props)
			}
			field := strings.TrimPrefix(output, LLMEnvelopeOutput+".")
			if ft, ok := props[field]; ok {
				return ft
			}
		}

		// Legacy builders represented the primary LLM payload as a sole
		// output/result port. Preserve that compatibility without treating
		// canonical metadata names as aliases of the payload.
		if (output == "output" || output == "result") && len(nd.OutTypes) == 1 {
			k, ok := singleOutputKey(nd.OutTypes)
			if !ok {
				return nil
			}
			return nd.OutTypes[k]
		}
		// attempt 上下文（FailBranch exception）：op 失败暴露 OperationError
		// record（error_type/error_message 语义端口）。error ref 的 producer
		// 是 op 节点（与 AttemptStmt 容器共享 id，find 返回 op），端口契约
		// 挂在 op 上 —— 不开启 FailBranch 的普通 op 无 error 端口。
		if nd.Exception != nil && nd.Exception.FailBranch {
			if t := attemptErrorType(output); t != nil {
				return t
			}
		}
	case *QA:
		// QA 的输出口 = 表单字段（含 record 字段的下钻口 `priority.value`）。
		// 逐段下钻与其它声明口宿主同一条规则（portFieldType）：只做精确键匹配
		// 会让 `priority.value` 这类口查不到而回落到更粗的类型 ⇒ 消费者（例如
		// 臂门控物化的恒等载体）把粗类型声明进 wire，与源口自相矛盾（实测 61：
		// 复制 priority.value 的载体声明成 object{value} 而非 string）。
		if t, ok := nd.OutTypes[output]; ok {
			return t
		}
		if t := portFieldType(TObject(nd.OutTypes), output); t != nil {
			return t
		}
	case *ListOp:
		// list-op 的整值口（`output`，IR 里物化节点的整值口名）承载它保型
		// 处理后的集合（take/filter/sort 都保元素类型）。缺这条端口契约时，
		// 消费方 `refType` 只能报「producer kind "listop" has no typed
		// conclusion and no port contract」而硬失败（D-1 症状 1）。契约来自
		// builder 用权威类型结论盖的戳；没盖到 ⇒ 返回 nil（fail loud，不伪造）。
		if output == "output" || output == "result" || output == "" {
			return nd.ResultType
		}
		return nil
	case *TextProcess:
		if nd.Method == "split" {
			// split materializes an array<string> (coze TextProcessor
			// split / dify code node): the port type must match, or a
			// downstream Python index shim (r[0]) declares a mismatched
			// input type and dify's type-invariant check fails.
			return TList(T("string"))
		}
		return T("string")
	case *Switch:
		// IntentDetector/switch: the classification result exposes semantic
		// ports classificationId (matched intent) and reason (plain text),
		// both strings. Path access r.reason / r.classificationId resolves
		// through the dotted fallback (portTypeOfNode(n, "reason")).
		if output == "classificationId" || output == "reason" {
			return T("string")
		}
		return nil
	case *KnowledgeCall:
		// knowledge retrieval exposes the canonical "outputList" port
		// (fieldPort maps knowledge().result → outputList; matches the
		// open-source coze renderer) and a bare "output" port for the
		// single retrieval answer.  outputList element is a retrieval
		// record whose "content" member is the chunk text — the honest
		// structural shape so a bare knowledge(...) result in a map value
		// never forges string.
		switch {
		case output == "outputList":
			return KnowledgeOutputListType()
		case output == "output":
			return T("string")
		case strings.HasPrefix(output, "outputList."):
			// element field of the retrieval record（字段集与类型见
			// KnowledgeOutputListType 的唯一声明点）—— 不得一律返回 string：
			// `documentId` 是 integer，写死 string 会让类型检查放过错误的比较/赋值。
			field := strings.TrimPrefix(output, "outputList.")
			if elem := KnowledgeOutputListType().Elem; elem != nil {
				if ft, ok := elem.Props[field]; ok {
					return ft
				}
			}
		}
		return nil
	case *ToolCall:
		// Tool op exposes capability-declared output ports (toolOutputDecls).
		// attempt 上下文（FailBranch exception）同样暴露 OperationError
		// record —— error ref 的 producer 是 op 节点（与 AttemptStmt 容器
		// 共享 id，find 返回 op）。
		if nd.Exception != nil && nd.Exception.FailBranch {
			if t := attemptErrorType(output); t != nil {
				return t
			}
		}
		// 类型权威 = **声明端口表**（ToolCall.ResultType = 注册表 output 表的
		// TypePass 结论，字段名即物理口名，与 wire 的 node_outputs 同源）。
		// 取不到（能力未声明输出 / 该口不在声明集）才回落到 open-world 占位
		// 「结构化但无 schema 的记录」——占位是**未知**的表达，不是可发射的
		// 声明（见 ToolCall.ResultType：占位一旦被声明出去就不再是未知）。
		// 旧实现在此恒返占位：字段级精度只能靠调用点的类型结论旁路，凡经
		// portTypeOf 的消费者（臂门控物化的恒等载体、junction phi 的组类型）
		// 都会把一个「不知道」写成 wire 事实。
		// 整值标记口：`@whole` = 「整个值」（wholeValueBindingPort：tool 的绑定写
		// 它），它的类型 = 声明端口表的整体（wholeValueRecordType ①）—— 拿单口
		// 读取去查它必然查不到，回落成空记录占位就把**已知**字段表报成**未知**
		// （R-130：聚合口元素丢字段表 ⇒ 下标丢失 ⇒ 再编译 TYPE_MISMATCH）。
		if output == wholeValueMarker {
			if t := wholeValueRecordType(nd); t != nil {
				return t
			}
		}
		if t := portFieldType(nd.ResultType, output); t != nil {
			return t
		}
		return TObject(map[string]*Type{})
	case *HTTPCall:
		// 整值标记口：http 的值同样由一组声明口承载（body/statusCode/headers）⇒
		// 绑定写标记口；整值 = 这张声明表的整体（wholeValueRecordType ③）。
		if output == wholeValueMarker {
			if t := wholeValueRecordType(nd); t != nil {
				return t
			}
		}
		// HTTP op 的**语言层结果类型**：单一声明点 = httpResultPortType
		// （builtin_result_ports.go），本处不手抄副本 —— 该文件同时记着
		// 「不得把平台物理类型回流进 IR」的一手实测依据。
		if t, ok := httpResultPortType(output); ok {
			return t
		}
		if nd.Exception != nil && nd.Exception.FailBranch {
			if t := attemptErrorType(output); t != nil {
				return t
			}
		}
		return nil
	case *Loop:
		// The loop container's iteration variable (VarName) is its item port;
		// the element type lives on StateTypes[VarName] (set when the iterable
		// type is known, build_stmt.go).  Field access resolves through the
		// element record's Props; an unknown field is silent (nil) so the
		// richer dotted fallback / caller diagnostics can decide.
		if t := loopItemType(nd, output); t != nil {
			return t
		}
		// Loop STATE ports (variableParameters): the container's own state
		// read inside the body resolves to the container port (lowering) /
		// the hoisted name (raising). During body emission the loop node is
		// not yet in any registry, so its type surface must be published
		// before the body is built (emitLoopNode) — and the lookup here must
		// answer for state ports, exactly like the item port above.
		if t, ok := nd.StateTypes[output]; ok {
			return t
		}

		// VALUE loops (for-in expressions) expose an output port whose element
		// type lives on OutTypes (single-output "output" port).  Statement
		// loops carry no value port — return nil so callers diagnose.
		if t, ok := nd.OutTypes[output]; ok {
			return t
		}
		// DOTTED yield-port path: `port.field` on a named multi-yield loop
		// (`yield otherInfos_list = v.otherInfos;`).  The platform reference
		// drops the constant-0 element marker (`agg_1.otherInfos_list.line_spacing`
		// = `agg_1.otherInfos_list[0].line_spacing`) — the same E5-b rule
		// dottedAggGroupField already applies to aggregate groups, so descend
		// through the port's element type instead of forging a type.
		// Regression: corpus 7490978684577693748 (xlx_douyin) r1 compile.
		if i := strings.Index(output, "."); i > 0 {
			if base, ok := nd.OutTypes[output[:i]]; ok {
				if t := dottedAggGroupField(base, output[i+1:]); t != nil {
					return t
				}
			}
		}
		return nil
	case *AttemptStmt:

		// attempt failure binding exposes the OperationError record
		// (error_type / error_message semantic ports, fieldPort
		// attempt-error → the same names; renderers map onto
		// default/branch_error). The error ref's producer is the
		// AttemptStmt container node itself (build_stmt.go:360).
		switch output {
		case "error":
			return TObject(map[string]*Type{
				"error_type":    T("string"),
				"error_message": T("string"),
			})
		case "error.error_type", "error_type":
			return T("string")
		case "error.error_message", "error_message":
			return T("string")
		}
		return nil
	}
	// dotted output ("a.b")：**先按记录逐段下钻**，下钻不成再回落到首段类型。
	//
	// 为什么下钻是权威（2026-09-27，CB-5）：点号引用读的是**成员**，不是容器 ——
	// 平台一手口径（672 目录两种序列化全扫）dotted 终点绑定 684 处里 **456 处**
	// 的 `input.type` 是成员类型（`data.link`→string 457 · `all_timelines.start`
	// →integer 123 · `text_positions.x`→float 37 · …），只有 **1 处**是容器自身，
	// 而那 1 处的值就是整体读（`text_positions=input.text_positions`，无残余段）。
	// 回落首段会把容器类型当成成员类型：`function main(Pair p) -> string { return
	// p.left; }` 的出口声明成 `object`（值却是 string）——填零/归一语义按 object 走
	// （同 CB-10 引的 `preTypeConverter`），是**发了一个错的类型**，比"不发"更坏。
	//
	// 下钻用 `dottedAggGroupField`（同一把尺子：record/object 逐段 Props，遇到
	// list/array 先取元素 = E5-b 的 `[0]` 下钻），不另写一份。
	if i := strings.Index(output, "."); i >= 0 {
		base := portTypeOfNode(n, output[:i])
		if mt := dottedAggGroupField(base, output[i+1:]); mt != nil {
			return mt
		}
		return base
	}
	return nil
}

// loopItemType resolves the loop iteration variable's element type and its
// dotted field paths (r.name → StateTypes["r"].Props["name"]).  The item
// port name is the loop's VarName; the element type is recorded when the
// iterable type is known (build_stmt.go StateTypes).  Non-item ports and
// unknown fields return nil so callers keep the authoritative diagnostic.
func loopItemType(loop *Loop, output string) *Type {
	if loop == nil || loop.VarName == "" {
		return nil
	}
	elem := loop.StateTypes[loop.VarName]
	// Single-var loops store the element under VarName; multi-var / zip
	// loops store per-var entries but VarName is the first binding — fall
	// back to the first element-typed entry if VarName itself is absent.
	if elem == nil && len(loop.StateTypes) > 0 {
		for _, t := range loop.StateTypes {
			elem = t
			break
		}
	}
	if elem == nil {
		return nil
	}
	if output == loop.VarName {
		return elem
	}
	prefix := loop.VarName + "."
	if strings.HasPrefix(output, prefix) {
		field := strings.TrimPrefix(output, prefix)
		if elem.Kind == "object" || elem.Kind == "record" {
			if ft, ok := elem.Props[field]; ok {
				return ft
			}
		}
		if elem.Kind == "list" {
			// zip 多源 / nested loop element: resolve against the innermost
			// element type when the item itself is a list.
			if el := elem.Elem; el != nil && (el.Kind == "object" || el.Kind == "record") {
				if ft, ok := el.Props[field]; ok {
					return ft
				}
			}
		}
	}
	return nil
}

// attemptErrorType resolves the OperationError record contract exposed by
// an attempt operation's failure branch (error_type / error_message semantic
// ports; renderers map onto platform errorBody.errorCode/.errorMessage or
// branch_error). Shared by every op family with Exception support so the
// attempt error contract is defined once, not per node kind.
func attemptErrorType(output string) *Type {
	switch output {
	case "error":
		return TObject(map[string]*Type{
			"error_type":    T("string"),
			"error_message": T("string"),
		})
	case "error.error_type", "error_type":
		return T("string")
	case "error.error_message", "error_message":
		return T("string")
	}
	return nil
}

func nestedOutputType(outputs map[string]*Type, output string) *Type {
	parts := strings.Split(output, ".")
	if len(parts) < 2 {
		return nil
	}
	t := outputs[parts[0]]
	for _, part := range parts[1:] {
		if t == nil || t.Props == nil {
			return nil
		}
		t = t.Props[part]
	}
	return t
}

// singleOutputKey returns the lone key of a one-entry type map (the
// single-output convention for code/llm families).
func singleOutputKey(m map[string]*Type) (string, bool) {
	if len(m) != 1 {
		return "", false
	}
	for k := range m {
		return k, true
	}
	return "", false
}

// emitSwitch lowers `switch (query [, config]) [-> r] { "a": {...}, ... }`:
// an IntentDetector node (type 22), fan-out branches (branch_N / default
// ports, N = 分类列表长度 = case 顺序), and a VariableAggregator merge —
// the structured switch's implicit confluence point (user rule: 空分支 =
// 悬挂 = 错误，case 体必须非空).
func (g *Builder) emitSwitch(s *ast.SwitchStmt, prevIDs []string, firstPort string) ([]string, string, error) {
	intentID := g.nextID()

	intents := []string{}
	for _, br := range s.Branches() {
		intents = append(intents, br.Label())
	}
	querySrc := Source{Path: []string{"query"}}
	if ref, ok := g.resolveValueRef(s.Query()); ok {
		querySrc.BlockID = ref.nodeID
		querySrc.Name = ref.output
	} else if lit := literalValue(s.Query()); lit != nil {
		querySrc.Val = lit
	} else {
		return nil, firstPort, &UnsupportedError{Construct: "intent query must resolve to a variable or literal"}
	}
	it := &Switch{ID: intentID, Query: querySrc, Intents: intents}
	applySwitchConfig(it, s.Config())
	g.add(it)
	g.consumeTreeNodes(intentID)
	g.flushTreeNodes()

	// 结果绑定：r.classificationId / r.reason 解析到 Switch 节点的
	// 数据输出端口（fieldPort kind "intent" 原样返回字段名）。
	if name := s.Result().Name(); name != "" {
		g.varToOutput[name] = varRef{nodeID: intentID, output: "classificationId", kind: "switch"}
	}

	bodies := []*ast.Block{}
	ports := []string{}
	for i, br := range s.Branches() {
		bodies = append(bodies, br.Body())
		ports = append(ports, fmt.Sprintf("branch_%d", i))
	}
	// 源码**未写 `default:`** 的 switch：平台端口仍在（`renderSwitch` 的
	// unownedSwitchPorts 把「源码缺 default 臂」与「空臂」并成一列 = 同一债）
	// ⇒ **值侧必须同构**：合成空 default 臂，让核心按语言矩阵那条规则
	// 「候选 = 各臂真实写值直引 + **未写臂直引 junction 前绑定**」贡献候选。
	// 与 `emitIf` 无 else 时的 `synthElse` 是同一条规则、同一个理由，不设宿主守卫。
	//
	// 不合成时的失效（一手实测，2026-09-21）：`string v; v = "x"; switch (k) {
	// "a": { v = "y"; } } return v;` —— merge 组只有臂内 1 个候选 ⇒ k 未命中任何
	// 分类时汇聚取零值 ⇒ **switch 前的绑定静默丢失**（一轮往返后 r1 里 `v = "x"`
	// 变 `v = ""`）；而 `if` 无 else、`switch { … default: { } }`（显式空 default）
	// 两种同义拼写都正确保留。
	//
	// **不是**宿主特例：`request-input` 之所以不合成，是因为它缺 timeout 时
	// **根本不投影 default 端口**（平台无超时边，见下方 emitRequestInput）⇒
	// 不存在「未写臂」这条控制路径，也就没有要兜的值 —— 差异在**数据**
	// （端口有无），不在规则。
	defaultBody := s.DefaultBody()
	if defaultBody == nil {
		defaultBody = ast.NewBlock(nil)
	}
	bodies = append(bodies, defaultBody)
	ports = append(ports, "default")

	// 互斥臂汇聚（P01 正向，臂数无知的一条规则，2026-09-10）：switch 的 N 条臂
	// 与 if 双臂同族 —— 臂内赋值提升变量 = 一个 phi（候选 = 臂内真实写值直引），
	// junction 之后的下游引用重定向到汇聚单元。只认「显式 return 的臂尾值」的
	// 旧实现会让臂内赋值静默丢失（候选空 merge + 下游读零值：Z78 首编失败、
	// H41/Z21/S188 等语料数据丢失实证）。
	outerVars := cloneRefs(g.varToOutput)
	outerDeclared := cloneNames(g.hoistedVars)
	// armBase = 本 junction 的臂入口作用域快照（宿主结果绑定已就位，尚无臂抬出）；
	// junctionScope = 宿主包围作用域对象（本 junction 全部臂的抬出 delta 提交目标，
	// 跨臂 first-write-wins）。
	junctionScope := g.varToOutput
	armBase := cloneRefs(junctionScope)
	var armTails []armTailValue
	treeBranches := []SwitchBranch{}
	arms := make([]junctionArm, 0, len(bodies))
	for i, body := range bodies {
		branchBlock := &Block{}
		g.branchValueDepth++
		g.switchBranchDepth++
		tailBound := map[string]bool{}
		// 臂入口视图 = **junction 入口作用域快照**（armBase，含条件物化与宿主绑定）：
		// 臂的抬出 delta 只提交进 junctionScope（= 宿主包围作用域对象，跨臂
		// first-write-wins），**不得**进入本臂入口视图 —— 否则臂内**嵌套 junction**
		// 会把互斥兄弟臂的 producer 当成「junction 前绑定」（复制语义兜底候选）
		// ⇒ 跨互斥臂引用（P2 违规，L2 实测 2026-09-12）。一条规则、无宿主/臂数/
		// 形态守卫，四宿主同构。
		g.varToOutput = cloneRefs(armBase)
		_, val, _, valProduced, _, err := g.emitBranch(branchBlock, body, intentID, ports[i], tailBound, junctionScope)
		g.switchBranchDepth--
		g.branchValueDepth--
		if err != nil {
			return nil, firstPort, err
		}
		treeBranches = append(treeBranches, SwitchBranch{Port: ports[i], Body: branchBlock})
		arms = append(arms, junctionArm{
			Port:      ports[i],
			Block:     branchBlock,
			Vars:      cloneRefs(g.lastBranchVars),
			TailBound: tailBound,
		})
		if val != nil && valProduced {
			// 臂门控（arm_gate_candidate.go）：臂尾值产在臂外（上游/入口参数）
			// ⇒ 该候选在每条路径上都存在 ⇒ merge 按位置恒取它 ⇒ 条件失效。
			// 候选按臂序收齐后统一门控（默认位 = 最后一条不门控）。
			armTails = append(armTails, armTailValue{arm: branchBlock, v: *val})
		}
	}
	// φ 核心（唯一实现）：组装序列收在 assembleArmJunction（junction_phi.go），
	// 本宿主只送数据 —— 臂集合 / 臂尾值通道 / junction 前视图；尾值与重定向口径
	// 见该函数头注（「零候选 ⇒ 没有值」）。
	// 槽名留空：本宿主的槽名由提升变量规划给出（`plan.RefName` = 变量名），
	// 没有「junction 之后才绑上去的目标名」这种形态（那是值位置三元的 U6 面）。
	res, err := g.assembleArmJunction(arms, outerDeclared, outerVars, armTails, "")
	if err != nil {
		return nil, "", err
	}
	g.consumeTreeNodes(res.Primary.ID)
	g.flushTreeNodes()
	g.appendStmt(&SwitchStmt{ID: intentID, Switch: g.find(intentID).(*Switch), Branches: treeBranches, Merge: res.Primary})
	g.appendChainedArmJunctions(res)
	return res.FlowIDs(), "", nil
}

// applyIntentConfig reads the intent config map into the Intent node's
// semantic payload (D1 config surface): temperature / max_tokens / top_p /
// mode / system_prompt. 缺省保持零值 = 平台默认。
func applySwitchConfig(it *Switch, m *ast.MapLit) {
	if m == nil {
		return
	}
	for _, e := range m.Entries() {
		// 静态键 = 标识符名 或 字符串值（唯一判据）：只认 IdentExpr 会让
		// **字符串键**（= 我们自家反编译产物的形态）静默丢配置（D-2 反向破口）。
		key, _ := staticMapKeyName(e.Key)
		switch key {
		case "temperature":
			if f, ok := float64FromExpr(e.Value); ok {
				it.Temperature = &f
			}
		case "max_tokens":
			if n, ok := intFromExpr(e.Value); ok {
				it.MaxTokens = n
			}
		case "top_p":
			if f, ok := float64FromExpr(e.Value); ok {
				it.TopP = &f
			}
		case "mode":
			if str, ok := stringValue(e.Value); ok {
				it.Mode = str
			}
		case "system_prompt":
			if str, ok := stringValue(e.Value); ok {
				it.SystemPrompt = str
			}
		case "model":
			// R-70 甲：分类器模型（`coze:<modelType>`，与 llm() 同一协议）。
			if str, ok := stringValue(e.Value); ok {
				it.Model = str
			}
		case "model_name":
			// R-70 甲：显示名载体（同 llm 的 model_name 句式；不作为平台参数下传，
			// 由 switch_model.go 组装进 llmParam 的 modelName）。
			if str, ok := stringValue(e.Value); ok {
				it.ModelName = str
			}
		}
	}
}

func float64FromExpr(e ast.Expr) (float64, bool) {
	switch v := e.(type) {
	case *ast.IntLit:
		return float64(v.Value()), true
	case *ast.FloatLit:
		return v.Value(), true
	}
	return 0, false
}

func intFromExpr(e ast.Expr) (int, bool) {
	if v, ok := e.(*ast.IntLit); ok {
		return int(v.Value()), true
	}
	return 0, false
}

// emitRequestInput lowers `request_input<Form, Action>({config}) as review { ... }`
// into a QuestionAnswer semantics: the form fields become structured
// outputs, each action branch is a fixed choice, and the branches resume
// from the QA's branch_i / default ports, converging through an aggregator.
func (g *Builder) emitRequestInput(s *ast.RequestInputStmt, prevIDs []string, firstPort string) ([]string, string, error) {
	qaID := g.nextID()

	choices := []string{}
	for _, b := range s.ActionBranches() {
		if fa, ok := b.ActionExpr().(*ast.FieldAccessExpr); ok {
			choices = append(choices, fa.FieldName())
		}
	}

	outTypes := map[string]*Type{"USER_RESPONSE": T("string")}
	if nt, ok := s.FormType().(*ast.NamedTypeRef); ok {
		if td, ok := g.types[nt.Name()]; ok {
			for _, f := range td.Fields() {
				outTypes[f.Name()] = g.typeOf(f.TypeRef())
			}
		}
	}
	qa := &QA{
		ID:       qaID,
		Choices:  choices,
		OutTypes: outTypes,
		Template: mapLitString(s.Config(), "content"),
		TextForm: s.ActionType() == nil,
	}
	// `content: template "…" using {k: v}`（作者名额表）：文本逐字 + 名额表，
	// 与答复端点 / 中间消息同一载体（resolveTemplateBindings 一处定义）。
	if tv, ok := mapLitTemplate(s.Config(), "content"); ok {
		bindings, err := g.resolveTemplateBindings(tv, "ask content")
		if err != nil {
			return nil, "", err
		}
		qa.Template = tv.Text()
		qa.Bindings = bindings
	}
	// Semantic payload from the validated request_input contract (the
	// platform-neutral config surface; backends map it onto their own
	// widgets). Resolve through the binding snapshot like the lowering
	// context does (lowering/context.go astID/ValidatedHumanInput).
	if g.prog != nil && g.prog.Snapshot != nil {
		if id, ok := g.prog.Snapshot.NodeIDOf(s); ok {
			if c := g.prog.ValidatedHumanInputs[id]; c != nil {
				for _, f := range c.Fields {
					qa.Fields = append(qa.Fields, QaField{Name: f.Name, Kind: string(f.Kind), Options: append([]string(nil), f.Options...)})
				}
				for _, a := range c.Actions {
					qa.Actions = append(qa.Actions, QaAction{Member: a.Member, Title: a.Title, Style: string(a.Style)})
				}
				qa.Timeout = c.TimeoutValue
				qa.TimeoutUnit = string(c.TimeoutUnit)
				// question 参数面 ②③（2026-10-10 用户批准）——平台原生键，语言面
				// 缺席就等于往返丢特征（见 ir.go::QA 注释）。
				qa.MaxAnswerCount = c.MaxAnswerCount
				qa.ExtractFromAnswer = c.ExtractFromAnswer
				qa.LLM = qaLlmFromContract(c.LLM)
			}
		}
	}
	// `dynamic_options: <值表达式>`（平台 `option_type: dynamic` 的选项来源）：
	// 值位置 ⇒ 解析成**生产者引用**（`dynamic_option` 的引用身份 = 产出方 +
	// 口名，同 `node_inputs` 的二元组）。解析不出 ⇒ 硬错（不发明形态）。
	if dv, ok := mapLitValueExpr(s.Config(), "dynamic_options"); ok {
		ref, resolved := g.resolveValueRef(dv)
		if !resolved {
			return nil, "", &UnsupportedError{Construct: "ask dynamic_options must resolve to a node output reference (a value path)"}
		}
		qa.DynamicOptions = &Source{BlockID: ref.nodeID, Name: ref.output}
	}
	g.add(qa)
	g.consumeTreeNodes(qaID)
	g.flushTreeNodes()

	if name := s.ResultBinding().Name(); name != "" {
		g.varToOutput[name] = varRef{nodeID: qaID, output: "USER_RESPONSE", kind: "qa"}
	}

	if s.ActionType() == nil {
		// **无动作形态**（S1）：没有臂 ⇒ 没有 junction、没有汇聚、没有端口。
		// 控制**直通**（平台 = 单条无标签推进口）；宿主结果绑定已落在**语句
		// 所在作用域**（binder：与 switch 结果绑定同一条，R-71）⇒ 语句之后的
		// `r.<字段>` 直接解析到 QA 节点输出（build.go 的 `"qa"` kind）。
		g.appendStmt(&QaStmt{ID: qaID, QA: qa})
		return []string{qaID}, "", nil
	}

	bodies := []*ast.Block{}
	ports := []string{}
	for i, b := range s.ActionBranches() {
		bodies = append(bodies, b.Body())
		ports = append(ports, fmt.Sprintf("branch_%d", i))
	}
	// timeout 分支可选：缺省时不投影 default 端口（平台无超时边）。
	if tb := s.TimeoutBody(); tb != nil {
		bodies = append(bodies, tb)
		ports = append(ports, "default")
	}

	// 与 switch 同族：action / timeout 臂内赋值提升变量 = 一个 phi（臂数无知的
	// 同一条规则；旧实现只认显式 return 的臂尾值 → 臂内赋值静默丢失）。
	outerVars := cloneRefs(g.varToOutput)
	outerDeclared := cloneNames(g.hoistedVars)
	// armBase = 本 junction 的臂入口作用域快照（宿主结果绑定已就位，尚无臂抬出）；
	// junctionScope = 宿主包围作用域对象（本 junction 全部臂的抬出 delta 提交目标，
	// 跨臂 first-write-wins）。
	junctionScope := g.varToOutput
	armBase := cloneRefs(junctionScope)
	var armTails []armTailValue
	treeBranches := []QaBranch{}
	arms := make([]junctionArm, 0, len(bodies))
	for i, body := range bodies {
		branchBlock := &Block{}
		g.branchValueDepth++
		tailBound := map[string]bool{}
		// 臂入口视图 = **junction 入口作用域快照**（armBase，含条件物化与宿主绑定）：
		// 臂的抬出 delta 只提交进 junctionScope（= 宿主包围作用域对象，跨臂
		// first-write-wins），**不得**进入本臂入口视图 —— 否则臂内**嵌套 junction**
		// 会把互斥兄弟臂的 producer 当成「junction 前绑定」（复制语义兜底候选）
		// ⇒ 跨互斥臂引用（P2 违规，L2 实测 2026-09-12）。一条规则、无宿主/臂数/
		// 形态守卫，四宿主同构。
		g.varToOutput = cloneRefs(armBase)
		_, val, _, valProduced, _, err := g.emitBranch(branchBlock, body, qaID, ports[i], tailBound, junctionScope)
		g.branchValueDepth--
		if err != nil {
			return nil, firstPort, err
		}
		treeBranches = append(treeBranches, QaBranch{Port: ports[i], Body: branchBlock})
		arms = append(arms, junctionArm{
			Port:      ports[i],
			Block:     branchBlock,
			Vars:      cloneRefs(g.lastBranchVars),
			TailBound: tailBound,
		})
		if val != nil && valProduced {
			// 臂门控（arm_gate_candidate.go）：臂尾值产在臂外（上游/入口参数）
			// ⇒ 该候选在每条路径上都存在 ⇒ merge 按位置恒取它 ⇒ 条件失效。
			// 候选按臂序收齐后统一门控（默认位 = 最后一条不门控）。
			armTails = append(armTails, armTailValue{arm: branchBlock, v: *val})
		}
	}
	// φ 核心（唯一实现）：组装序列收在 assembleArmJunction（junction_phi.go），
	// 本宿主只送数据 —— 臂集合 / 臂尾值通道 / junction 前视图；尾值与重定向口径
	// 见该函数头注（「零候选 ⇒ 没有值」）。
	// 槽名留空：本宿主的槽名由提升变量规划给出（`plan.RefName` = 变量名），
	// 没有「junction 之后才绑上去的目标名」这种形态（那是值位置三元的 U6 面）。
	res, err := g.assembleArmJunction(arms, outerDeclared, outerVars, armTails, "")
	if err != nil {
		return nil, "", err
	}
	g.consumeTreeNodes(res.Primary.ID)
	g.flushTreeNodes()
	g.appendStmt(&QaStmt{ID: qaID, QA: g.find(qaID).(*QA), Branches: treeBranches, Merge: res.Primary})
	g.appendChainedArmJunctions(res)
	return res.FlowIDs(), "", nil
}

// mapLitString reads a string-typed entry from a config map literal.
// mapLitTemplate 取 map 字面量里某键的具名模板值（`template "…" using {…}`）。
// 取不到（键缺席 / 值不是模板）⇒ false —— 调用方退回纯字符串读法（零变化）。
func mapLitTemplate(m *ast.MapLit, key string) (*ast.TemplateValue, bool) {
	if m == nil {
		return nil, false
	}
	for _, e := range m.Entries() {
		if name, ok := staticMapKeyName(e.Key); ok && name == key {
			if tv, ok := e.Value.(*ast.TemplateValue); ok {
				return tv, true
			}
		}
	}
	return nil, false
}

func mapLitString(m *ast.MapLit, key string) string {
	if m == nil {
		return ""
	}
	for _, e := range m.Entries() {
		if name, ok := staticMapKeyName(e.Key); ok && name == key {
			if s, ok := stringValue(e.Value); ok {
				return s
			}
		}
	}
	return ""
}

// mapLitValueExpr 取 map 字面量里某键的**原始值表达式**（不解释、不校验类型）：
// 值位置（`dynamic_options`）要把表达式交给生产者解析器，不能预先降成字面量。
func mapLitValueExpr(m *ast.MapLit, key string) (ast.Expr, bool) {
	if m == nil {
		return nil, false
	}
	for _, e := range m.Entries() {
		if name, ok := staticMapKeyName(e.Key); ok && name == key {
			return e.Value, true
		}
	}
	return nil, false
}

// qaLlmFromContract 把语言契约的 ask `llm` 子表搬进 IR（指针搬指针 = presence
// 逐键保持）。**只搬不翻译**：`model` 保持协议串原文，翻成平台整数 `modelType`
// 是目标层（`internal/backend/cozebiz`）的事 —— 语言层不认平台协议
// （判据 = internal/archgate 的语言内核轴）。
func qaLlmFromContract(c *binding.HumanInputLLM) *QaLlm {
	if c == nil || !c.HasAny() {
		return nil
	}
	return &QaLlm{
		Model:               c.Model,
		ModelName:           c.ModelName,
		Temperature:         c.Temperature,
		MaxTokens:           c.MaxTokens,
		TopP:                c.TopP,
		ResponseFormat:      c.ResponseFormat,
		GenerationDiversity: c.GenerationDiversity,
		SystemPrompt:        c.SystemPrompt,
	}
}
