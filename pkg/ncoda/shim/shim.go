package shim

// shim_reverse.go — python-code shim 反向引擎（2026-09-02，P4）。
//
// 把编译器自建的单表达式 shim（`# ncoda:shim 1` 标记，pkg/pyast 单源）
// 还原为 ncoda 第一性表达式。判定 = 标记 ∧ 模块形态 §2.1 ∧ 表达式落在
// §2.2 全集 ∧ 歧义点类型可判定（§3 歧义表）。任一不满足 → false →
// convertCodeRunner 按用户 foreign code 透传（不打开盒子，封闭契约 §1.1；
// 与 LLM「定义 T 即信任 T」同一哲学——shim 由 ncoda 维护，修改责任在用户）。
//
// 互逆不变量：对子集表达式 E，compile(E)=S（带标记），reverse(S)=E（ncoda），
// compile(reverse(S))==S 字节级（golden 锁定），reverse(compile(E))==E（ncoda 级）。

import (
	"strings"

	"github.com/nodecoda/ncoda/pkg/ncoda/astnodes"
	"github.com/nodecoda/ncoda/pkg/ncoda/typesystem"
	"github.com/nodecoda/ncoda/pkg/pyast"
)

// tryReverseShim 尝试反向一个 CodeRunner 的 python 源码。
// inputTypes 是 canvas inputParameters 权威类型（歧义消解用）。
// bindings 是参数名 → 上游引用表达式（反编译环境中已声明的变量/表达式）。
// 成功返回 ncoda 表达式；任一判定不满足返回 (nil, false)。
// ReverseShim is the narrow cross-platform entry point to the compiler-owned
// shim reverse engine. Callers outside this package must not inspect or
// reinterpret Python directly; a false result means the code remains foreign.
func (g *Reverser) ReverseShim(code string, inputTypes map[string]*typesystem.TypeInfo, bindings map[string]Expr) (Expr, bool) {
	return g.tryReverseShim(code, inputTypes, bindings)
}

func (g *Reverser) tryReverseShim(code string, inputTypes map[string]*typesystem.TypeInfo, bindings map[string]Expr) (Expr, bool) {
	if !strings.HasPrefix(code, pyast.ShimMarker) {
		return nil, false
	}
	m, err := pyast.ParseModule(strings.TrimPrefix(code, pyast.ShimMarker))
	if err != nil {
		return nil, false
	}
	// 模块形态 §2.1：单 return {"result": E}。多语句（json.parse 等）、多端口
	// （buildMap）一律不透传反向。
	//
	// **模块级 import 白名单**（2026-09-23 校正）：shim 契约 §1 要求 stdlib
	// import 写在 `def main` **之前**（`docs/PYTHON-SHIM-TRANSLATION-CONTRACT.md`
	// §1「import 自足：…绝不放在 def 之后」）⇒ 可逆 shim 本来就允许携带模块级
	// import。原实现把「有 import」一律判不可逆，与契约自相矛盾：`json.stringify`
	// shim（`import json` + 一条 `return {"result": json.dumps(arg1, ensure_ascii=False)}`）
	// 因此永远读不回来，原 ncoda 语句被降级成**用户 foreign code**
	// （一手实证 X109/S96/S12/S19/S44/V021/V024）。
	if len(m.Body) != 1 || !shimImportsAllowed(m.Imports) {
		return nil, false
	}
	ret, ok := m.Body[0].(*pyast.ReturnStmt)
	if !ok {
		return nil, false
	}
	dict, ok := ret.Value.(*pyast.DictExpr)
	if !ok || len(dict.Items) == 0 {
		return nil, false
	}
	// 单键 result → 表达式 shim（{"result": E} 包裹）。
	if len(dict.Items) == 1 {
		if key, ok := dict.Items[0].Key.(*pyast.StringExpr); ok && key.Value == "result" {
			return g.mapShimExpr(dict.Items[0].Value, inputTypes, bindings)
		}
	}
	// 多键 / 非 result 键 → record shim（G8 buildMap：dict IS the record）。
	// mapShimExpr 的 DictExpr 分支整体逆译为 record 字面量，字段访问
	// （vN.G1）在 nodeRefExpr 走 member 链，不剥端口段。
	return g.mapShimExpr(dict, inputTypes, bindings)
}

// shimImportsAllowed 判定标记 shim 携带的模块级 import 是否落在**我们自己**的
// 封闭子集依赖白名单里。当前白名单 = {json}（唯一会带 import 的自建 shim 族 =
// json.stringify / json.parse）。空集 = 允许（绝大多数 shim 无 import）。
// 白名单外的模块 ⇒ 不是我们的 shim ⇒ 调用方透传（封闭契约 §1.1：不打开盒子）。
func shimImportsAllowed(imports []string) bool {
	for _, im := range imports {
		if im != "json" {
			return false
		}
	}
	return true
}

// shimDictParams 返回可逆 shim 的「返回 dict 键 → 参数名」恒等表：只收录值
// **恰好是**该参数本身（裸标识符）的项 —— 该端口的值就是那个输入。值被计算过
// （调用 / 二元 / 下标 / 字面量…）的项不收录：那些端口的载体是 shim 自己。
// 解析失败 / 形态不在封闭子集 → nil（与 tryReverseShim 同源判定）。
func parseShimDictParams(code string) map[string]string {
	if !strings.HasPrefix(code, pyast.ShimMarker) {
		return nil
	}
	m, err := pyast.ParseModule(strings.TrimPrefix(code, pyast.ShimMarker))
	if err != nil || len(m.Body) != 1 || !shimImportsAllowed(m.Imports) {
		return nil
	}
	ret, ok := m.Body[0].(*pyast.ReturnStmt)
	if !ok {
		return nil
	}
	dict, ok := ret.Value.(*pyast.DictExpr)
	if !ok || len(dict.Items) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, item := range dict.Items {
		key, ok := item.Key.(*pyast.StringExpr)
		if !ok {
			continue
		}
		name, ok := item.Value.(*pyast.NameExpr)
		if !ok {
			continue
		}
		out[key.Value] = name.Name
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// shimDictParams 按节点缓存 parseShimDictParams 的结果（同一 shim 的多个候选
// 各查一次，解析只做一次；nil = 该节点不是恒等可穿透的 shim）。
func (g *Reverser) shimDictParams(nodeID string) map[string]string {
	if p, ok := g.params[nodeID]; ok {
		return p
	}
	var out map[string]string
	if node := g.node(nodeID); node != nil {
		if cr, ok := node.(*astnodes.CodeRunnerNode); ok && cr != nil {
			out = parseShimDictParams(cr.Code)
		}
	}
	if g.params == nil {
		g.params = map[string]map[string]string{}
	}
	g.params[nodeID] = out
	return out
}

// shimRecordFileCast record shim（DictExpr）反向后的 file 语义字段归一：
// 以 code 节点输出契约（recTI = result object schema）为权威，把 file 字段
// 的字符串字面量还原为 file(...) cast（前向 file("x") → "x" 的精确逆，
// 含零值 "" → file("") 与 URL 字面量 → file("http://…")）。非字面量值
// （上游 file 变量 ref / || 链等）类型已由源携带，不动；recTI 非 object 或
// 字段缺失 → 不猜（fail-loud 由 L4 类型检查兜底，禁静默造类型）。
func (g *Reverser) shimRecordFileCast(d *DictExpr, recTI *typesystem.TypeInfo) {
	if d == nil || recTI == nil || recTI.Type != typesystem.DataTypeObject {
		return
	}
	for i, k := range d.Keys {
		if i >= len(d.Values) {
			break
		}
		fti := recTI.Properties[k]
		if fti == nil || fti.Type != typesystem.DataTypeFile {
			continue
		}
		// The compiler's file lowering is string-shaped at the platform boundary.
		// A record shim can therefore carry a mixed `string ?? file` expression,
		// not only a string literal.  Cast the whole field when its inferred
		// semantic type is not already file; this keeps the record contract
		// closed on the next compile round without wrapping known file values.
		if call, ok := d.Values[i].(*CallExpr); ok && call.Callee == "file" {
			continue
		}
		if ti := g.ExprTypeInfo(d.Values[i]); ti != nil && ti.Type == typesystem.DataTypeFile {
			continue
		}
		d.Values[i] = &CallExpr{Callee: "file", Args: []Expr{d.Values[i]}}
	}
}

// recordShimNullableLifts 判定 record shim（G8 多键 dict）中哪些输出字段的值
// 来自可空输入源（nullableInputs 命中）。反编译把可空源绑定到 record 字段时，
// 字段声明类型必须提升为 T?（见 registerRecordTypeNullable / convertCodeRunner）：
// 否则两段式「类型裸声明 + 赋值」产出字段 T + 值 T? 的自我矛盾代码。
// 判定按 pyast dict 值子树收集输入参数名（与 mapShimExpr 的 NameExpr→binding
// 替换同源）；字段与输入无交集或模块形态不符 → nil（不提升，维持旧行为）。
func (g *Reverser) recordShimNullableLifts(code string, nullableInputs map[string]bool) map[string]bool {
	if len(nullableInputs) == 0 {
		return nil
	}
	if !strings.HasPrefix(code, pyast.ShimMarker) {
		return nil
	}
	m, err := pyast.ParseModule(strings.TrimPrefix(code, pyast.ShimMarker))
	if err != nil {
		return nil
	}
	if len(m.Imports) != 0 || len(m.Body) != 1 {
		return nil
	}
	ret, ok := m.Body[0].(*pyast.ReturnStmt)
	if !ok {
		return nil
	}
	dict, ok := ret.Value.(*pyast.DictExpr)
	if !ok || len(dict.Items) == 0 {
		return nil
	}
	lift := map[string]bool{}
	for _, item := range dict.Items {
		key, ok := item.Key.(*pyast.StringExpr)
		if !ok {
			continue
		}
		names := map[string]bool{}
		pyastNames(item.Value, names)
		for n := range names {
			if nullableInputs[n] {
				lift[key.Value] = true
				break
			}
		}
	}
	if len(lift) == 0 {
		return nil
	}
	return lift
}

// pyastNames 收集 pyast 表达式子树内的全部 NameExpr 标识符（shim 参数引用）。
func pyastNames(e pyast.Expr, out map[string]bool) {
	switch x := e.(type) {
	case nil:
		return
	case *pyast.NameExpr:
		out[x.Name] = true
	case *pyast.BinaryExpr:
		pyastNames(x.Left, out)
		pyastNames(x.Right, out)
	case *pyast.UnaryExpr:
		pyastNames(x.Operand, out)
	case *pyast.SubscriptExpr:
		pyastNames(x.Obj, out)
		pyastNames(x.Index, out)
	case *pyast.ParenExpr:
		pyastNames(x.X, out)
	case *pyast.AttributeExpr:
		pyastNames(x.Obj, out)
	case *pyast.TernaryExpr:
		pyastNames(x.Cond, out)
		pyastNames(x.Then, out)
		pyastNames(x.Else, out)
	case *pyast.CallExpr:
		if x.Obj != nil {
			pyastNames(x.Obj, out)
		}
		for _, a := range x.Args {
			pyastNames(a, out)
		}
		for _, kw := range x.Kwargs {
			pyastNames(kw.Value, out)
		}
	case *pyast.DictExpr:
		for _, it := range x.Items {
			pyastNames(it.Key, out)
			pyastNames(it.Value, out)
		}
	case *pyast.ListExpr:
		for _, el := range x.Elts {
			pyastNames(el, out)
		}
	case *pyast.ConcatExpr:
		for _, p := range x.Parts {
			pyastNames(p, out)
		}
	case *pyast.ListCompExpr:
		pyastNames(x.Elt, out)
		for _, c := range x.Clauses {
			pyastNames(c.Iter, out)
			for _, f := range c.Ifs {
				pyastNames(f, out)
			}
		}
	case *pyast.DictCompExpr:
		pyastNames(x.Key, out)
		pyastNames(x.Value, out)
		for _, c := range x.Clauses {
			pyastNames(c.Iter, out)
			for _, f := range c.Ifs {
				pyastNames(f, out)
			}
		}
	case *pyast.GeneratorExpr:
		pyastNames(x.Elt, out)
		for _, c := range x.Clauses {
			pyastNames(c.Iter, out)
			for _, f := range c.Ifs {
				pyastNames(f, out)
			}
		}
	}
}

// mapShimExpr 递归映射 pyast 表达式 → 反编译 ast4 表达式。
func (g *Reverser) mapShimExpr(e pyast.Expr, inputTypes map[string]*typesystem.TypeInfo, bindings map[string]Expr) (Expr, bool) {
	switch x := e.(type) {
	case *pyast.IntExpr:
		return &LiteralExpr{Value: x.Value}, true
	case *pyast.FloatExpr:
		// 反向词法按拼写分 int/float（pyast/lexer.go 的 IsFloat）⇒ 拼写即类型载体。
		return &LiteralExpr{Value: x.Value, SpellType: true}, true
	case *pyast.StringExpr:
		return &LiteralExpr{Value: x.Value}, true
	case *pyast.BoolExpr:
		return &LiteralExpr{Value: x.Value}, true
	case *pyast.NoneExpr:
		return &LiteralExpr{Value: nil}, true
	case *pyast.NameExpr:
		// 参数名 → 上游绑定表达式（反编译环境中的实际变量/表达式）；
		// 未绑定（编译器产物外的自由名）保守透传。
		if b, ok := bindings[x.Name]; ok {
			return b, true
		}
		return &IdentExpr{Name: x.Name}, true
	case *pyast.ParenExpr:
		return g.mapShimExpr(x.X, inputTypes, bindings)
	case *pyast.BinaryExpr:
		return g.mapShimBinary(x, inputTypes, bindings)
	case *pyast.UnaryExpr:
		return g.mapShimUnary(x, inputTypes, bindings)
	case *pyast.CallExpr:
		return g.mapShimCall(x, inputTypes, bindings)
	case *pyast.SubscriptExpr:
		obj, ok := g.mapShimExpr(x.Obj, inputTypes, bindings)
		if !ok {
			return nil, false
		}
		idx, ok := g.mapShimExpr(x.Index, inputTypes, bindings)
		if !ok {
			return nil, false
		}
		return &IndexExpr{Obj: obj, Index: idx}, true
	case *pyast.AttributeExpr:
		obj, ok := g.mapShimExpr(x.Obj, inputTypes, bindings)
		if !ok {
			return nil, false
		}
		return &MemberExpr{Obj: obj, Field: x.Attr}, true
	case *pyast.TernaryExpr:
		// route-1a R4 逆：编译器 `??` shim 的规范形态
		// `(a if a is not None else b)` 折叠回 ncoda `a ?? b`（P1e 前向互逆）。
		// Cond 恒等判定容忍 legacy/pyast 两路径的括号差（StripParens 归一）。
		if l, ok := coalesceCondArm(x); ok {
			le, ok := g.mapShimExpr(l, inputTypes, bindings)
			if !ok {
				return nil, false
			}
			el, ok := g.mapShimExpr(x.Else, inputTypes, bindings)
			if !ok {
				return nil, false
			}
			return &BinaryExpr{Op: "??", Left: le, Right: el}, true
		}
		c, ok := g.mapShimExpr(x.Cond, inputTypes, bindings)
		if !ok {
			return nil, false
		}
		th, ok := g.mapShimExpr(x.Then, inputTypes, bindings)
		if !ok {
			return nil, false
		}
		el, ok := g.mapShimExpr(x.Else, inputTypes, bindings)
		if !ok {
			return nil, false
		}
		return &TernaryExpr{Cond: c, Then: th, Else: el}, true
	case *pyast.ListExpr:
		items := make([]Expr, 0, len(x.Elts))
		for _, el := range x.Elts {
			m, ok := g.mapShimExpr(el, inputTypes, bindings)
			if !ok {
				return nil, false
			}
			items = append(items, m)
		}
		return &ListExpr{Items: items}, true
	case *pyast.DictExpr:
		d := newDict()
		for _, item := range x.Items {
			k, ok := item.Key.(*pyast.StringExpr)
			if !ok {
				return nil, false // dict 键必须是字符串字面量（子集契约）
			}
			v, ok := g.mapShimExpr(item.Value, inputTypes, bindings)
			if !ok {
				return nil, false
			}
			d.Put(k.Value, v)
		}
		return d, true
	case *pyast.ConcatExpr:
		return g.mapShimConcat(x, inputTypes, bindings)
	case *pyast.ListCompExpr:
		return g.mapShimListComp(x, inputTypes, bindings)
	case *pyast.GeneratorExpr:
		return nil, false // generator 只出现在 first_non_empty 模式内（CallExpr 处理）
	}
	return nil, false
}

// mapShimBinary 二元运算（含 `in` 的类型歧义消解）。
func (g *Reverser) mapShimBinary(x *pyast.BinaryExpr, inputTypes map[string]*typesystem.TypeInfo, bindings map[string]Expr) (Expr, bool) {
	op := x.Op
	// `and` / `or` 短路逻辑 → ncoda && / ||
	if op == "and" || op == "or" {
		l, ok := g.mapShimExpr(x.Left, inputTypes, bindings)
		if !ok {
			return nil, false
		}
		r, ok := g.mapShimExpr(x.Right, inputTypes, bindings)
		if !ok {
			return nil, false
		}
		ncodaOp := "&&"
		if op == "or" {
			ncodaOp = "||"
		}
		return &BinaryExpr{Op: ncodaOp, Left: l, Right: r}, true
	}
	if op == "in" {
		return g.mapShimIn(x, inputTypes, bindings)
	}
	if op == "not in" {
		// ncoda 主语法无 `not in` 二元操作符（仅 lambda 方法族）→ 保守透传。
		return nil, false
	}
	// 其余二元：左/右操作数各自映射，操作符原样（// 除外）。
	l, ok := g.mapShimExpr(x.Left, inputTypes, bindings)
	if !ok {
		return nil, false
	}
	r, ok := g.mapShimExpr(x.Right, inputTypes, bindings)
	if !ok {
		return nil, false
	}
	switch op {
	case "//":
		return &CallExpr{Callee: "div", Args: []Expr{l, r}}, true
	case "*":
		// 前向 `repeat(a, b)` ⇒ `[a] * b`（pkg/pygen/pygen.go）。ncoda 的 `*`
		// **不接受数组左操作数**（一手：BINARY_TYPE Cannot apply '*' to
		// Type(array, ArrayType(element=Type(int))) and Type(int)）⇒ `[X] * N`
		// **不是**合法 ncoda，只可能来自我们自己的前向 ⇒ 判别只看「左操作数是
		// **单元素**列表字面量」，不做类型推演（该形状 ncoda 源写不出来）。
		// 多于一个元素（`[a, b] * n`）不是前向产物 ⇒ 保守走通用二元（会编译红，不静默发明）。
		if lst, ok := l.(*ListExpr); ok && len(lst.Items) == 1 {
			return &CallExpr{Callee: "repeat", Args: []Expr{lst.Items[0], r}}, true
		}
		return &BinaryExpr{Op: op, Left: l, Right: r}, true
	case "==", "!=", "<", "<=", ">", ">=", "+", "-", "/", "%":
		return &BinaryExpr{Op: op, Left: l, Right: r}, true
	}
	return nil, false
}

// mapShimIn 处理 `in` 的类型歧义（§3 表）：
//   - 右操作数 array → 二元 `a in b`（ncoda 成员判断）
//   - 右操作数 string → `contains(b, a)`（内建子串；ncoda string 无二元 in）
//   - 类型不可知 → 透传（保守正确，不误伤）
func (g *Reverser) mapShimIn(x *pyast.BinaryExpr, inputTypes map[string]*typesystem.TypeInfo, bindings map[string]Expr) (Expr, bool) {
	l, ok := g.mapShimExpr(x.Left, inputTypes, bindings)
	if !ok {
		return nil, false
	}
	r, ok := g.mapShimExpr(x.Right, inputTypes, bindings)
	if !ok {
		return nil, false
	}
	switch rv := x.Right.(type) {
	case *pyast.ListExpr:
		// 静态数组字面量（如 `(xs or [])` 的 `[]`）→ 二元 in
		return &BinaryExpr{Op: "in", Left: l, Right: r}, true
	case *pyast.StringExpr:
		return &CallExpr{Callee: "contains", Args: []Expr{r, l}}, true
	case *pyast.NameExpr:
		ti, known := inputTypes[rv.Name]
		if !known || ti == nil {
			return nil, false // 类型不可知 → 透传
		}
		if ti.Type == typesystem.DataTypeArray {
			return &BinaryExpr{Op: "in", Left: l, Right: r}, true
		}
		if ti.Type == typesystem.DataTypeString {
			return &CallExpr{Callee: "contains", Args: []Expr{r, l}}, true
		}
		return nil, false
	}
	return nil, false
}

// mapShimUnary 一元运算，两档**都要反向**：
//
//	`not x` → `empty(x)`（真值否定；ncoda `!` 要求 bool，empty 接受 any，语义等价）
//	`-x`    → `-x`（算术取反；ncoda 一元负号）
//
// 旧注释写「`-x` 前向不支持（子集外）→ 透传」——**与一手事实矛盾**（2026-09-26）：
// 前向**会**产出带一元负号的 shim（`let x = -1;` ⇒ `{"result": (-1)}`；
// `let m = -n;` ⇒ `{"result": (-result)}`；条件操作数 `n > -2` ⇒
// `{"result": (result > (-2))}`）。缺这一档的后果不是「少个映射」：整个 shim
// 反向失败 ⇒ **整体透传** ⇒ `# ncoda:shim` 漏进 ncoda 文本，违反
// AGENTS.md「shim-python-subset 定位标准」的目标不变量（前向/反向封闭子集）。
// 判据 = `platforms/cozebiz/decompile/unary_minus_shim_roundtrip_contract_test.go`。
func (g *Reverser) mapShimUnary(x *pyast.UnaryExpr, inputTypes map[string]*typesystem.TypeInfo, bindings map[string]Expr) (Expr, bool) {
	switch x.Op {
	case "not":
		operand, ok := g.mapShimExpr(x.Operand, inputTypes, bindings)
		if !ok {
			return nil, false
		}
		return &CallExpr{Callee: "empty", Args: []Expr{operand}}, true
	case "-":
		operand, ok := g.mapShimExpr(x.Operand, inputTypes, bindings)
		if !ok {
			return nil, false
		}
		return &UnaryExpr{Op: "-", Operand: operand}, true
	}
	return nil, false
}

// mapShimCall 调用映射：确定性内建逆映射表 + 复杂内建模式（first_non_empty
// / range / json 守卫）。前向表镜像见 pygen.LowerBuiltin。
func (g *Reverser) mapShimCall(x *pyast.CallExpr, inputTypes map[string]*typesystem.TypeInfo, bindings map[string]Expr) (Expr, bool) {
	// json 序列化守卫（`json.dumps(X, ensure_ascii=False)` → `json.stringify(X)`）：
	// 前向唯一生产者 = `pkg/ir/build_node.go::buildStringify` 的自建 shim
	// （coze-biz 无 json 序列化原生节点 ⇒ 走 python 通道）。
	// 必须在 `mapShimExpr(x.Obj)` **之前**判定：`json` 是模块名不是绑定名，
	// 先走通用对象映射必失败 ⇒ 整条 shim 被判不可逆。
	// kwarg 只在**恰好**是 `ensure_ascii=False` 时接受（前向只发这一种写法；
	// 别的写法不是我们的 shim ⇒ 透传）。
	if nm, ok := x.Obj.(*pyast.NameExpr); ok && nm.Name == "json" && x.Func == "dumps" {
		if len(x.Args) != 1 || len(x.Kwargs) != 1 || x.Kwargs[0].Name != "ensure_ascii" {
			return nil, false
		}
		if b, ok := x.Kwargs[0].Value.(*pyast.BoolExpr); !ok || b.Value {
			return nil, false
		}
		a, ok := g.mapShimExpr(x.Args[0], inputTypes, bindings)
		if !ok {
			return nil, false
		}
		return &CallExpr{Callee: "json.stringify", Args: []Expr{a}}, true
	}
	// 方法调用（obj.func(args)）
	if x.Obj != nil {
		obj, ok := g.mapShimExpr(x.Obj, inputTypes, bindings)
		if !ok {
			return nil, false
		}
		switch x.Func {
		case "strip":
			if len(x.Args) != 0 {
				return nil, false
			}
			return &CallExpr{Callee: "trim", Args: []Expr{obj}}, true
		case "join":
			if len(x.Args) != 1 {
				return nil, false
			}
			items, ok := g.mapShimExpr(x.Args[0], inputTypes, bindings)
			if !ok {
				return nil, false
			}
			return &CallExpr{Callee: "join", Args: []Expr{items, obj}}, true
		case "split":
			if len(x.Args) != 1 {
				return nil, false
			}
			d, ok := g.mapShimExpr(x.Args[0], inputTypes, bindings)
			if !ok {
				return nil, false
			}
			return &CallExpr{Callee: "split", Args: []Expr{obj, d}}, true
		case "replace":
			if len(x.Args) != 2 {
				return nil, false
			}
			a, ok := g.mapShimExpr(x.Args[0], inputTypes, bindings)
			if !ok {
				return nil, false
			}
			b, ok := g.mapShimExpr(x.Args[1], inputTypes, bindings)
			if !ok {
				return nil, false
			}
			return &CallExpr{Callee: "replace", Args: []Expr{obj, a, b}}, true
		case "get":
			return g.mapShimJSONGuard(x, inputTypes, bindings)
		}
		return nil, false
	}
	// 普通调用
	switch x.Func {
	case "str":
		if len(x.Args) != 1 {
			return nil, false
		}
		// json 守卫形态：str((ARR[0] if len(ARR) > 0 else {}).get(KEY) or "")
		// → ARR[0].KEY（先于普通 to_string 判定）。
		if getCall, ok := shimUnwrapOrEmpty(x.Args[0]); ok {
			return g.mapShimJSONGuard(getCall, inputTypes, bindings)
		}
		a, ok := g.mapShimExpr(x.Args[0], inputTypes, bindings)
		if !ok {
			return nil, false
		}
		return &CallExpr{Callee: "to_string", Args: []Expr{a}}, true
	case "int":
		if len(x.Args) != 1 {
			return nil, false
		}
		a, ok := g.mapShimExpr(x.Args[0], inputTypes, bindings)
		if !ok {
			return nil, false
		}
		return &CallExpr{Callee: "int", Args: []Expr{a}}, true
	case "float":
		if len(x.Args) != 1 {
			return nil, false
		}
		a, ok := g.mapShimExpr(x.Args[0], inputTypes, bindings)
		if !ok {
			return nil, false
		}
		return &CallExpr{Callee: "float", Args: []Expr{a}}, true
	case "len":
		if len(x.Args) != 1 {
			return nil, false
		}
		a, ok := g.mapShimExpr(x.Args[0], inputTypes, bindings)
		if !ok {
			return nil, false
		}
		return &CallExpr{Callee: "len", Args: []Expr{a}}, true
	case "list":
		// list(range(a, b)) → range(a, b)
		if len(x.Args) != 1 {
			return nil, false
		}
		inner, ok := x.Args[0].(*pyast.CallExpr)
		if !ok || inner.Obj != nil || inner.Func != "range" || len(inner.Args) != 2 {
			return nil, false
		}
		a, ok := g.mapShimExpr(inner.Args[0], inputTypes, bindings)
		if !ok {
			return nil, false
		}
		b, ok := g.mapShimExpr(inner.Args[1], inputTypes, bindings)
		if !ok {
			return nil, false
		}
		return &CallExpr{Callee: "range", Args: []Expr{a, b}}, true
	case "next":
		return g.mapShimFirstNonEmpty(x, inputTypes, bindings)
	case "isinstance":
		return nil, false // 只在 flatten 模式内
	case "enumerate":
		return nil, false // 只在 enumerate 模式内
	}
	return nil, false
}

// mapShimFirstNonEmpty 匹配 first_non_empty 生成形态：
// next((str(u) for u in (ARR or []) if u and str(u).strip()), "")
// → first_non_empty(ARR)
func (g *Reverser) mapShimFirstNonEmpty(x *pyast.CallExpr, inputTypes map[string]*typesystem.TypeInfo, bindings map[string]Expr) (Expr, bool) {
	if len(x.Args) != 2 {
		return nil, false
	}
	def, ok := x.Args[1].(*pyast.StringExpr)
	if !ok || def.Value != "" {
		return nil, false
	}
	gen, ok := x.Args[0].(*pyast.GeneratorExpr)
	if !ok || len(gen.Clauses) != 1 {
		return nil, false
	}
	c := gen.Clauses[0]
	if len(c.Vars) != 1 || c.Vars[0] != "u" || len(c.Ifs) != 1 {
		return nil, false
	}
	// elt = str(u)
	elt, ok := gen.Elt.(*pyast.CallExpr)
	if !ok || elt.Obj != nil || elt.Func != "str" || len(elt.Args) != 1 {
		return nil, false
	}
	if _, ok := elt.Args[0].(*pyast.NameExpr); !ok || elt.Args[0].(*pyast.NameExpr).Name != "u" {
		return nil, false
	}
	// iter = (ARR or [])
	arr, ok := shimOrNilList(c.Iter)
	if !ok {
		return nil, false
	}
	// ifs[0] = u and str(u).strip()
	cond, ok := c.Ifs[0].(*pyast.BinaryExpr)
	if !ok || cond.Op != "and" {
		return nil, false
	}
	lu, ok := cond.Left.(*pyast.NameExpr)
	if !ok || lu.Name != "u" {
		return nil, false
	}
	strip, ok := cond.Right.(*pyast.CallExpr)
	if !ok || strip.Obj == nil || strip.Func != "strip" || len(strip.Args) != 0 {
		return nil, false
	}
	sru, ok := strip.Obj.(*pyast.CallExpr)
	if !ok || sru.Obj != nil || sru.Func != "str" || len(sru.Args) != 1 {
		return nil, false
	}
	if u2, ok := sru.Args[0].(*pyast.NameExpr); !ok || u2.Name != "u" {
		return nil, false
	}
	mapped, ok := g.mapShimExpr(arr, inputTypes, bindings)
	if !ok {
		return nil, false
	}
	return &CallExpr{Callee: "first_non_empty", Args: []Expr{mapped}}, true
}

// mapShimListComp 匹配 flatten / enumerate 两个 listcomp 生成形态。
func (g *Reverser) mapShimListComp(x *pyast.ListCompExpr, inputTypes map[string]*typesystem.TypeInfo, bindings map[string]Expr) (Expr, bool) {
	// enumerate: [{"index": i, "value": v} for i, v in enumerate(ARR)]
	if len(x.Clauses) == 1 {
		c := x.Clauses[0]
		if len(c.Vars) == 2 && len(c.Ifs) == 0 {
			if de, ok := x.Elt.(*pyast.DictExpr); ok && len(de.Items) == 2 {
				if enum, ok := c.Iter.(*pyast.CallExpr); ok && enum.Obj == nil && enum.Func == "enumerate" && len(enum.Args) == 1 {
					if ok := shimEnumDict(de, c.Vars[0], c.Vars[1]); ok {
						mapped, ok := g.mapShimExpr(enum.Args[0], inputTypes, bindings)
						if !ok {
							return nil, false
						}
						return &CallExpr{Callee: "enumerate", Args: []Expr{mapped}}, true
					}
				}
			}
		}
	}
	// flatten: [str(x) for g in (ARR or []) if isinstance(g, list) for x in g]
	if len(x.Clauses) == 2 {
		c1, c2 := x.Clauses[0], x.Clauses[1]
		if len(c1.Vars) == 1 && c1.Vars[0] == "g" && len(c1.Ifs) == 1 &&
			len(c2.Vars) == 1 && c2.Vars[0] == "x" && len(c2.Ifs) == 0 {
			// elt = str(x)
			elt, ok := x.Elt.(*pyast.CallExpr)
			if !ok || elt.Obj != nil || elt.Func != "str" || len(elt.Args) != 1 {
				return nil, false
			}
			if n, ok := elt.Args[0].(*pyast.NameExpr); !ok || n.Name != "x" {
				return nil, false
			}
			// c2.Iter = g
			if gi, ok := c2.Iter.(*pyast.NameExpr); !ok || gi.Name != "g" {
				return nil, false
			}
			// c1.Iter = (ARR or [])
			arr, ok := shimOrNilList(c1.Iter)
			if !ok {
				return nil, false
			}
			// c1.Ifs[0] = isinstance(g, list)
			ifs, ok := c1.Ifs[0].(*pyast.CallExpr)
			if !ok || ifs.Obj != nil || ifs.Func != "isinstance" || len(ifs.Args) != 2 {
				return nil, false
			}
			if gn, ok := ifs.Args[0].(*pyast.NameExpr); !ok || gn.Name != "g" {
				return nil, false
			}
			if ln, ok := ifs.Args[1].(*pyast.NameExpr); !ok || ln.Name != "list" {
				return nil, false
			}
			mapped, ok := g.mapShimExpr(arr, inputTypes, bindings)
			if !ok {
				return nil, false
			}
			return &CallExpr{Callee: "flatten", Args: []Expr{mapped}}, true
		}
	}
	return nil, false
}

// mapShimJSONGuard 匹配 json 数组元素/字段提取形态：
// str((ARR[0] if len(ARR) > 0 else {}).get(KEY) or "") → ARR[0].KEY
//
// **只认常量下标 0**（i≠0 = 已登记缺口 R-111，不是能力）：本引擎支持的这一档 =
// 平台导出里真实存在的手写形态（`items[0] if i < len(items) else {}`，U3「承诺读
// 任意平台导出」）。发射侧同形 shim 的**下标是源码常量 i**（`items[i] if len(items)
// > i else {}`），i≠0 时反向会产出 `X[i≠0].field`，而合法性判据今天**按基段身份**
// 分档（`pkg/passes/type_infer.go` 的 `!objType.JSONParseArray`）⇒ 该 ncoda
// 编译不过（X23 一手：泛化下标后 `INDEX_TYPE Element access on a record array
// supports only index 0`）。⇒ 泛化下标**必须先统一这条合法性判据**（C6：落点由
// 平台承载面决定，不由基段身份决定）；在那之前 i≠0 一律透传（不半反向）。
func (g *Reverser) mapShimJSONGuard(x *pyast.CallExpr, inputTypes map[string]*typesystem.TypeInfo, bindings map[string]Expr) (Expr, bool) {
	if x.Obj == nil || x.Func != "get" || len(x.Args) != 1 {
		return nil, false
	}
	key, ok := x.Args[0].(*pyast.StringExpr)
	if !ok {
		return nil, false
	}
	// x.Obj 是 (ARR[0] if len(ARR) > 0 else {})：pyast 三元序列化自带括号、
	// parse 时不包 ParenExpr（parser.go 字节回环契约），故直接是
	// TernaryExpr；外部手写括号（ParenExpr 包裹）也一并接受。
	var tern *pyast.TernaryExpr
	switch obj := x.Obj.(type) {
	case *pyast.ParenExpr:
		tern, _ = obj.X.(*pyast.TernaryExpr)
	case *pyast.TernaryExpr:
		tern = obj
	}
	if tern == nil {
		return nil, false
	}
	// then = ARR[0]（只认常量 0，见头注 R-111）
	sub, ok := tern.Then.(*pyast.SubscriptExpr)
	if !ok {
		return nil, false
	}
	zero, ok := sub.Index.(*pyast.IntExpr)
	if !ok || zero.Value != 0 {
		return nil, false
	}
	// cond = len(ARR) > 0 —— `len` 必须作用在**与下标同一个** ARR 上（发射侧
	// build_condition.go 用同一个 obj 渲染两处）。少了这条身份校验会把
	// `x[0] if len(y) > 0 else {}` 误读成 `x[0]`（读任意平台导出时是误读，不是能力）。
	cond, ok := tern.Cond.(*pyast.BinaryExpr)
	if !ok || cond.Op != ">" {
		return nil, false
	}
	ln, ok := cond.Left.(*pyast.CallExpr)
	if !ok || ln.Obj != nil || ln.Func != "len" || len(ln.Args) != 1 {
		return nil, false
	}
	if pyast.SerializeExpr(ln.Args[0]) != pyast.SerializeExpr(sub.Obj) {
		return nil, false
	}
	zero2, ok := cond.Right.(*pyast.IntExpr)
	if !ok || zero2.Value != 0 {
		return nil, false
	}
	// else = {}
	emptyDict, ok := tern.Else.(*pyast.DictExpr)
	if !ok || len(emptyDict.Items) != 0 {
		return nil, false
	}
	arr, ok := g.mapShimExpr(sub.Obj, inputTypes, bindings)
	if !ok {
		return nil, false
	}
	idx := &IndexExpr{Obj: arr, Index: &LiteralExpr{Value: int64(0)}}
	return &MemberExpr{Obj: idx, Field: key.Value}, true
}

// mapShimConcat flat `+` 链 → 模板串（首尾字面量契约）。part 为
// str(inner) 时剥掉 → ${inner}；其余表达式 → ${part}。
func (g *Reverser) mapShimConcat(x *pyast.ConcatExpr, inputTypes map[string]*typesystem.TypeInfo, bindings map[string]Expr) (Expr, bool) {
	parts := x.Parts
	if len(parts) < 2 {
		return nil, false
	}
	// 首尾必须是字面量（ast.py TemplateString 契约）
	if _, ok := parts[0].(*pyast.StringExpr); !ok {
		return nil, false
	}
	if _, ok := parts[len(parts)-1].(*pyast.StringExpr); !ok {
		return nil, false
	}
	tpl := &TemplateString{}
	for _, part := range parts {
		switch p := part.(type) {
		case *pyast.StringExpr:
			tpl.Parts = append(tpl.Parts, TemplatePart{Text: p.Value})
		case *pyast.CallExpr:
			// str(inner) → ${inner}
			if p.Obj == nil && p.Func == "str" && len(p.Args) == 1 {
				inner, ok := g.mapShimExpr(p.Args[0], inputTypes, bindings)
				if !ok {
					return nil, false
				}
				tpl.Parts = append(tpl.Parts, TemplatePart{Expr: inner})
				continue
			}
			m, ok := g.mapShimExpr(part, inputTypes, bindings)
			if !ok {
				return nil, false
			}
			tpl.Parts = append(tpl.Parts, TemplatePart{Expr: m})
		default:
			m, ok := g.mapShimExpr(part, inputTypes, bindings)
			if !ok {
				return nil, false
			}
			tpl.Parts = append(tpl.Parts, TemplatePart{Expr: m})
		}
	}
	return tpl, true
}

// shimUnwrapOrEmpty 匹配 `(X or "")`（json 守卫的兜底空串），返回 X。
func shimUnwrapOrEmpty(e pyast.Expr) (*pyast.CallExpr, bool) {
	bin, ok := e.(*pyast.BinaryExpr)
	if !ok || bin.Op != "or" {
		return nil, false
	}
	empty, ok := bin.Right.(*pyast.StringExpr)
	if !ok || empty.Value != "" {
		return nil, false
	}
	call, ok := bin.Left.(*pyast.CallExpr)
	if !ok {
		return nil, false
	}
	return call, true
}

// shimOrNilList 匹配 `(EXPR or [])`（编译器 or-[] 防空形态），返回 EXPR。
func shimOrNilList(e pyast.Expr) (pyast.Expr, bool) {
	paren, ok := e.(*pyast.ParenExpr)
	if !ok {
		return nil, false
	}
	bin, ok := paren.X.(*pyast.BinaryExpr)
	if !ok || bin.Op != "or" {
		return nil, false
	}
	lst, ok := bin.Right.(*pyast.ListExpr)
	if !ok || len(lst.Elts) != 0 {
		return nil, false
	}
	return bin.Left, true
}

// shimEnumDict 匹配 enumerate 的 elt 字典：{"index": I, "value": V}。
func shimEnumDict(d *pyast.DictExpr, wantI, wantV string) bool {
	if len(d.Items) != 2 {
		return false
	}
	seen := map[string]bool{}
	for _, item := range d.Items {
		k, ok := item.Key.(*pyast.StringExpr)
		if !ok {
			return false
		}
		n, ok := item.Value.(*pyast.NameExpr)
		if !ok {
			return false
		}
		switch k.Value {
		case "index":
			if n.Name != wantI {
				return false
			}
			seen["index"] = true
		case "value":
			if n.Name != wantV {
				return false
			}
			seen["value"] = true
		default:
			return false
		}
	}
	return seen["index"] && seen["value"]
}

// coalesceCondArm 识别编译器 `??` shim 的规范手臂形态：
// Cond = `L is (not) None` 且 Then 与 Cond.Left 恒等（StripParens 归一）。
// 命中 → 返回 L（left 操作数），供 mapShimExpr 折叠为 ncoda `L ?? Else`。
// 非编译器产物（用户 foreign code）经 ShimMarker 门禁不会进入 mapShimExpr；
// 恒等 + None 判定双条件保证不误伤用户手写 python 三元。
func coalesceCondArm(t *pyast.TernaryExpr) (pyast.Expr, bool) {
	bin, ok := t.Cond.(*pyast.BinaryExpr)
	if !ok || (bin.Op != "is" && bin.Op != "is not") {
		return nil, false
	}
	if _, ok := bin.Right.(*pyast.NoneExpr); !ok {
		return nil, false
	}
	if !pyast.SameExpr(bin.Left, t.Then) {
		return nil, false
	}
	return bin.Left, true
}

// retypeReversedShim —— shim 反向的**类型保真**（2026-10-17 · S67 一手）。
//
// 编译器用 shim 的**输出口声明**承载值位转换：`file(x)` 在平台上物化成恒等
// shim（输入 `string`，输出 `image`）—— 转换的全部语义只在「输入类型 ≠ 输出
// 类型」这一对声明里。把它反向成纯值 `x` 后，若不再写回同一转换，产出的
// 声明就是「声明 T、赋 S」⇒ 下一轮正编译必红（一手：S67 的
// `file<image> v = <plugin>.image_url` ⇒ TYPE_MISMATCH）。
//
// 判定（复用**参数边界的同一闭集**，不另立一套）：
//   - 输出类型不可判（any/未知）或与反向值同族 ⇒ 原样（不发明）；
//   - 有语言级显式转换（ParamCastScalarTable + `file(...)` 构造）⇒ 补在值边界；
//   - 已知且无可表达的转换 ⇒ keep=false：**不反向**，透传 foreign code
//     （它本来就是平台事实；反过来又声明不符的类型才是撒谎）。
func (g *Reverser) RetypeReversed(rev Expr, outTI *typesystem.TypeInfo) (Expr, bool) {
	if rev == nil || outTI == nil {
		return rev, true
	}
	dst := ParamSrcKind(outTI)
	src := ParamSrcKind(g.ExprTypeInfo(rev))
	if dst == "any" || src == "any" || dst == src {
		return rev, true
	}
	if dst == "file" && src == "string" {
		return &CallExpr{Callee: "file", Args: []Expr{rev}}, true
	}
	if dst == "string" && (src == "array" || src == "map" || src == "record") {
		if callee := ParamCastScalarTable["__container_to_string"][src]; callee != "" {
			return &CallExpr{Callee: callee, Args: []Expr{rev}}, true
		}
	} else if tbl, ok := ParamCastScalarTable[dst]; ok {
		if callee := tbl[src]; callee != "" {
			return &CallExpr{Callee: callee, Args: []Expr{rev}}, true
		}
	}
	return rev, false
}
