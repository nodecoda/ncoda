package ir

// P5 pyast lowering (direction 3 — Python-subset generator).
//
// lowerLiteralPyast mirrors renderLiteralPython but produces pyast nodes
// instead of strings. A builtin call only takes the pyast path when its
// callee is registered in pygen AND every argument lowers cleanly; any
// not-yet-expressible shape returns ok=false so the caller falls back to
// the string-template shim. This keeps P5 a byte-equivalent, builtin-by-
// builtin migration — never a silent byte drift.

import (
	"encoding/json"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/pyast"
	"github.com/nodecoda/ncoda/pkg/pygen"
)

// lowerCalcBuiltin lowers a calc-builtin call to a pyast expression.
// Returns the pyast expr and true when the full path is expressible;
// otherwise ok=false (caller falls back to the legacy string template).
func lowerCalcBuiltin(x *ast.CallExpr, g *Builder, bindings *[]textBinding) (pyast.Expr, bool, error) {
	name := calleeName(x.Func())
	if !pygen.IsLowerable(name) {
		return nil, false, nil
	}
	args := make([]pyast.Expr, 0, len(x.Args()))
	for _, a := range x.Args() {
		pa, ok, err := lowerLiteralPyast(a, g, bindings)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		args = append(args, pa)
	}
	expr, ok := pygen.LowerBuiltin(name, args)
	return expr, ok, nil
}

// lowerLiteralPyast mirrors renderLiteralPython: it lowers the literal /
// identifier / field-access subset to pyast nodes. ok=false signals a shape
// the current pyast node set cannot express byte-identically.
func lowerLiteralPyast(e ast.Expr, g *Builder, bindings *[]textBinding) (pyast.Expr, bool, error) {
	switch x := e.(type) {
	case *ast.MapLit:
		// Semantic-equivalence guards (P5.9 + P5.10a): pyast's DictExpr
		// serializes keys with quoteString and SORTS items, while the legacy
		// renderer (renderLiteralPython) uses json.Marshal keys and preserves
		// source order. Dict key ORDER is a semantic-equivalence exemption
		// (a Python dict is orderless; downstream consumers read by key), so
		// multi-key dicts are accepted — the sorted pyast bytes differ from
		// the legacy insertion order, but the key SET is identical.
		// Quote-unsafe keys still fall back, conservatively: the json.Marshal
		// vs quoteString escape difference is the string-LITERAL-exemption
		// family (it applies to every string position, not just dict keys),
		// which is not yet on the exemption whitelist — revisit separately.
		entries := canonicalMapEntries(x.Entries())
		items := make([]pyast.DictItem, 0, len(entries))
		for _, entry := range entries {
			key, ok := staticMapKeyName(entry.Key)
			if !ok || !keyQuoteSafe(key) {
				return nil, false, nil // dynamic projection key — fallback
			}
			val, ok2, err := lowerLiteralPyast(entry.Value, g, bindings)
			if err != nil {
				return nil, false, err
			}
			if !ok2 {
				return nil, false, nil
			}
			items = append(items, pyast.DictItem{
				Key:   &pyast.StringExpr{Value: key},
				Value: val,
			})
		}
		return &pyast.DictExpr{Items: items}, true, nil
	case *ast.ArrayLit:
		elts := make([]pyast.Expr, 0, len(x.Elements()))
		for _, el := range x.Elements() {
			pe, ok, err := lowerLiteralPyast(el, g, bindings)
			if err != nil {
				return nil, false, err
			}
			if !ok {
				return nil, false, nil
			}
			elts = append(elts, pe)
		}
		return &pyast.ListExpr{Elts: elts}, true, nil
	case *ast.IdentExpr:
		rendered, err := bindConditionPython(x.Name(), x, g, bindings)
		if err != nil {
			return nil, false, err
		}
		return &pyast.NameExpr{Name: rendered}, true, nil
	case *ast.FieldAccessExpr:
		return lowerFieldAccessPyast(x, g, bindings)
	case *ast.StringLit:
		return &pyast.StringExpr{Value: x.Value()}, true, nil
	case *ast.IntLit:
		return &pyast.IntExpr{Value: x.Value()}, true, nil
	case *ast.FloatLit:
		return &pyast.FloatExpr{Value: x.Value()}, true, nil
	case *ast.BoolLit:
		return &pyast.BoolExpr{Value: x.Value()}, true, nil
	case *ast.NullLit:
		return &pyast.NoneExpr{}, true, nil
	case *ast.CallExpr:
		// calc-builtin arguments (len(split(x, ","))) and other
		// condition-renderable calls lower through the condition mirror
		// (P5.2 remaining shapes).
		return lowerConditionPyast(x, g, bindings)
	case *ast.BinaryExpr:
		// Top-level binary as a calc-builtin argument: legacy
		// renderLiteralPython falls back to renderConditionPython, which
		// always parenthesizes a top-level binary — mirror lowerShimExpr's
		// ParenExpr wrap (P5.2).
		pa, ok, err := lowerConditionPyast(e, g, bindings)
		if err != nil || !ok {
			return nil, ok, err
		}
		return &pyast.ParenExpr{X: pa}, true, nil
	case *ast.UnaryExpr, *ast.TernaryExpr, *ast.TemplateString, *ast.IndexExpr:
		// Complex scalar shapes as calc-builtin arguments (unary/ternary/
		// template/subscript): serializer already reproduces the legacy
		// always-paren / no-paren bytes — delegate to the condition mirror
		// (P5.2).
		return lowerConditionPyast(e, g, bindings)
	}
	return nil, false, nil
}

// lowerFieldAccessPyast mirrors renderFieldAccessPython. The jsonParseArray
// special case builds a nested ternary chain that pyast cannot express yet —
// it returns ok=false so the caller falls back to the legacy shim.
func lowerFieldAccessPyast(x *ast.FieldAccessExpr, g *Builder, bindings *[]textBinding) (pyast.Expr, bool, error) {
	// 枚举成员（R-62）：**不在 pyast 面实现第二遍** —— 返回 ok=false 让调用者回落到
	// 唯一实现（`renderConditionPython` 的枚举成员分支，产出 `pythonJSONString`）。
	// 一处定义：非 ASCII 成员名的转义口径（`\uXXXX`）只由那一处决定，两条路不会漂。
	if _, ok := g.enumMemberValue(x); ok {
		return nil, false, nil
	}
	root, path := splitFieldAccess(x)
	if root != "" {
		name := root
		if len(path) > 0 {
			name = root + "_" + strings.Join(path, "_")
		}
		rendered, err := bindConditionPython(name, x, g, bindings)
		if err != nil {
			return nil, false, err
		}
		return &pyast.NameExpr{Name: rendered}, true, nil
	}
	// E5-b record-array element member chains (v.data[0].bgm_url): mirror
	// renderFieldAccessPython — bind the whole chain onto its dotted output
	// port instead of splitting at the index (which lost [0] and turned the
	// member into an array string subscript — round2 INDEX_TYPE).
	if rendered, ok, err := g.e5bFieldChainBinding(x, bindings); err != nil {
		return nil, false, err
	} else if ok {
		return &pyast.NameExpr{Name: rendered}, true, nil
	}
	if ix, ok := x.Obj().(*ast.IndexExpr); ok {
		if id, ok2 := ix.Obj().(*ast.IdentExpr); ok2 {
			if ref, ok3 := g.varToOutput[id.Name()]; ok3 && ref.jsonParseArray {
				// G12-2 element field: `items[0].content` →
				// `str((items[0] if len(items) > 0 else {}).get("content") or "")`.
				// Byte-identical to the legacy renderFieldAccessPython form:
				// the TernaryExpr self-parenthesizes, the CallExpr renders
				// obj.get(...), and the `or ""` BinaryExpr stays paren-free
				// as str's argument (P5.12).
				obj, ok, err := lowerCondChild(x.Obj(), g, bindings)
				if err != nil || !ok {
					return nil, false, err
				}
				key, _ := json.Marshal(x.FieldName())
				keyLit := string(key[1 : len(key)-1])
				return &pyast.CallExpr{
					Func: "str",
					Args: []pyast.Expr{&pyast.BinaryExpr{
						Op: "or",
						Left: &pyast.CallExpr{
							Obj:  obj,
							Func: "get",
							Args: []pyast.Expr{&pyast.StringExpr{Value: keyLit}},
						},
						Right: &pyast.StringExpr{Value: ""},
					}},
				}, true, nil
			}
		}
	}
	obj, ok, err := lowerLiteralPyast(x.Obj(), g, bindings)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	key, _ := json.Marshal(x.FieldName())
	return &pyast.SubscriptExpr{
		Obj:   obj,
		Index: &pyast.StringExpr{Value: string(key[1 : len(key)-1])},
	}, true, nil
}

// lowerStmt lowers a ncoda statement to a pyast statement.
// Returns ok=false when the statement shape is not expressible.
func lowerStmt(s ast.Stmt, g *Builder, bindings *[]textBinding) (pyast.Stmt, bool, error) {
	switch x := s.(type) {
	case *ast.ReturnStmt:
		val, ok, err := lowerLiteralPyast(x.Value(), g, bindings)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		return &pyast.ReturnStmt{Value: val}, true, nil
	case *ast.VarDeclStmt:
		if x.Init() == nil {
			return nil, false, nil // no-init decl — not expressible in Python subset
		}
		val, ok, err := lowerLiteralPyast(x.Init(), g, bindings)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		return &pyast.AssignStmt{Name: x.Name(), Value: val}, true, nil
	case *ast.ExprStmt:
		// expr as statement: lower the expression, return it as a pass-through.
		// pyast has no ExprStmt — the expression is embedded directly.
		// For now, ok=false to fallback; future: emit as expression statement.
		return nil, false, nil
	}
	return nil, false, nil
}

// wrapShim wraps a pyast expression into a full shim module:
//
//	def main(p1, p2) -> dict:
//	    return {"result": <expr>}
//
// The output is byte-equivalent to the template
//
//	fmt.Sprintf("def main(%s) -> dict:\n    return {\"result\": %s}", params, expr)
//
// (trailing newline stripped to match existing template behavior).
// ShimMarker 别名（单源在 pkg/pyast.ShimMarker，编译/反编译共用）。
const ShimMarker = pyast.ShimMarker

func wrapShim(params []pyast.Param, body pyast.Expr) string {
	code := wrapShimStmts(params, nil, []pyast.Stmt{&pyast.ReturnStmt{
		Value: &pyast.DictExpr{Items: []pyast.DictItem{
			{Key: &pyast.StringExpr{Value: "result"}, Value: body},
		}},
	}})
	return ShimMarker + code
}

// wrapShimStmts renders a full shim module from a statement list through the
// pyast path (`def main(...) -> dict:` + body). Multi-statement bodies
// (json.parse: `data = json.loads(arg1)` + `return {...}`) go through here;
// the trailing newline is stripped like the legacy templates.
func wrapShimStmts(params []pyast.Param, imports []string, stmts []pyast.Stmt) string {
	m := &pyast.Module{Params: params, Imports: imports, Body: stmts}
	code := pyast.Serialize(m)
	return strings.TrimSuffix(code, "\n")
}

// lowerShimExpr lowers an ncoda expression for a single-"result" shim body
// (P5.5/P5.6: materializeCondition / emitExprCodeOut). Byte-equivalence
// contract: the pyast path is only taken when the legacy renderer at these
// sites — renderConditionPython — accepts the same shape AND produces
// identical bytes. The lowering mirrors renderConditionPython's accept set
// (scalars, identifiers, field access, index, binary/unary/ternary, coalesce,
// calc builtins) with legacy always-paren emulation; map/array literals are
// rejected via the recursive hasMapOrList guard. A top-level BinaryExpr is
// parenthesized because the legacy shim body renders
// `{"result": (a < b)}` — the pyast serializer only parens when needed.
//
// 守卫的理由（2026-09-25 R-82 更正）：原写「renderConditionPython 对它们报
// UnsupportedError」。R-82 把结构字面量纳入该渲染器之后这条不再成立 —— 守卫现在
// 守的是**字节对等未证**：`lowerLiteralPyast` 的 DictExpr 按键 `compareExpr` 排序，
// legacy `renderLiteralPython` 按 `naturalLess` 排序（多键字面量两侧可能不同序）⇒
// 保守回退 legacy，**拼写唯一**。数组面（两侧同为 `[1, 2, 3]`）已字节等价，若要放行
// pyast 需先按节点形态证明，属独立改动 —— 不在此顺手扩大。
func lowerShimExpr(e ast.Expr, g *Builder, bindings *[]textBinding) (pyast.Expr, bool, error) {
	pa, ok, err := lowerConditionPyast(e, g, bindings)
	if err != nil || !ok {
		return nil, ok, err
	}
	// jsonParseArray guards carry a synthetic empty `{}` (ternary else /
	// record-fallback arm) that the legacy renderer itself emits — exempt
	// them from the map/array-literal boundary (P5.12). Non-guard shapes
	// with map/array literals keep falling back (P4 explicit-error).
	if !isJSONParseArrayAccess(e, g) && hasMapOrList(pa) {
		return nil, false, nil
	}
	if _, isBin := pa.(*pyast.BinaryExpr); isBin {
		pa = &pyast.ParenExpr{X: pa}
	}
	return pa, true, nil
}

// hasMapOrList reports whether the pyast tree contains a DictExpr or
// ListExpr anywhere. Keeps single-"result" shim sites on the legacy renderer
// for these shapes because the two renderers' bytes are not proven equal
// (dict key order — see lowerShimExpr; R-82 更正前的理由是「legacy 报错」，已不成立).
func hasMapOrList(e pyast.Expr) bool {
	switch x := e.(type) {
	case *pyast.DictExpr, *pyast.ListExpr:
		return true
	case *pyast.CallExpr:
		if x.Obj != nil && hasMapOrList(x.Obj) {
			return true
		}
		for _, a := range x.Args {
			if hasMapOrList(a) {
				return true
			}
		}
	case *pyast.BinaryExpr:
		return hasMapOrList(x.Left) || hasMapOrList(x.Right)
	case *pyast.UnaryExpr:
		return hasMapOrList(x.Operand)
	case *pyast.ParenExpr:
		return hasMapOrList(x.X)
	case *pyast.SubscriptExpr:
		return hasMapOrList(x.Obj) || hasMapOrList(x.Index)
	case *pyast.AttributeExpr:
		return hasMapOrList(x.Obj)
	case *pyast.TernaryExpr:
		return hasMapOrList(x.Cond) || hasMapOrList(x.Then) || hasMapOrList(x.Else)
	case *pyast.ListCompExpr:
		return hasMapOrList(x.Elt)
	case *pyast.DictCompExpr:
		return hasMapOrList(x.Key) || hasMapOrList(x.Value)
	case *pyast.GeneratorExpr:
		return hasMapOrList(x.Elt)
	case *pyast.ConcatExpr:
		for _, part := range x.Parts {
			if hasMapOrList(part) {
				return true
			}
		}
	}
	return false
}

// isJSONParseArrayAccess reports whether e is a top-level json.parse<array<T>>
// element access — a plain index (`items[i]`) or an element field chain
// (`items[i].field`) rooted at a jsonParseArray-marked variable. These are
// the two shapes whose lowered pyast tree legitimately contains the guard's
// synthetic empty `{}` (P5.12); everything else keeps the strict
// map/array-literal boundary.
func isJSONParseArrayAccess(e ast.Expr, g *Builder) bool {
	if g == nil {
		return false
	}
	obj := e
	if fa, ok := obj.(*ast.FieldAccessExpr); ok {
		obj = fa.Obj()
	}
	ix, ok := obj.(*ast.IndexExpr)
	if !ok {
		return false
	}
	id, ok := ix.Obj().(*ast.IdentExpr)
	if !ok {
		return false
	}
	ref, ok := g.varToOutput[id.Name()]
	return ok && ref.jsonParseArray
}

// shimParamNames returns the binding names in order — the parameter list of
// a single-"result" shim. Identical in the pyast path (wrapShim) and the
// legacy template path (strings.Join(params, ", ")).
func shimParamNames(bindings []textBinding) []string {
	names := make([]string, 0, len(bindings))
	for _, b := range bindings {
		names = append(names, b.name)
	}
	return names
}

// shimCodeFor renders the full single-"result" shim source
// (`def main(...) -> dict: return {"result": ...}`) through the pyast path.
// ok=false means the expression is not provably byte-equivalent at a
// renderConditionPython site; the caller must fall back to the legacy
// string template (which yields today's exact bytes). Errors are shared
// with the legacy renderer (same lowering origins).
func shimCodeFor(e ast.Expr, g *Builder, bindings *[]textBinding) (string, bool, error) {
	pa, ok, err := lowerShimExpr(e, g, bindings)
	if err != nil || !ok {
		return "", ok, err
	}
	return wrapShim(shimParamNodes(*bindings), pa), true, nil
}

// shimParamNodes converts bindings to untyped pyast Param nodes — the
// parameter list of a single-"result" shim (typed variants like emitMapOp's
// `items: list` are constructed at their call site).
// coalescePyast lowers ncoda `a ?? b` (first-not-null value selection,
// route-1a R4) to the canonical python form `(a if a is not None else b)`.
// The condition arm and then-arm share the same operand node; the pyast
// serializer writes it twice. Operands are pure bound refs / literals in the
// closed shim subset, so double reference is side-effect free. The decompile
// reverse folds this shape back to `??` (shim_reverse.go).
func coalescePyast(left, right pyast.Expr) pyast.Expr {
	return &pyast.TernaryExpr{
		Cond: &pyast.BinaryExpr{Op: "is not", Left: left, Right: &pyast.NoneExpr{}},
		Then: left,
		Else: right,
	}
}

func shimParamNodes(bindings []textBinding) []pyast.Param {
	nodes := make([]pyast.Param, 0, len(bindings))
	for _, b := range bindings {
		nodes = append(nodes, pyast.Param{Name: b.name})
	}
	return nodes
}

// lowerConditionPyast lowers an ncoda expression the way
// renderConditionPython renders it — the accept set of the single-"result"
// condition/value shim sites. Byte-equivalence rules (P5.6):
//
//   - renderConditionPython parenthesizes EVERY binary node; the pyast
//     serializer only parens when precedence requires. Children that the
//     legacy renderer feeds through renderConditionPython (binary operands,
//     subscript obj/index, ternary arms/condition, unary operand, coalesce
//     arms) therefore wrap any lowered BinaryExpr in ParenExpr — this
//     reproduces the legacy bytes for same-precedence chains
//     ((a + b) + c, not a + b + c).
//   - unary minus is rejected: legacy renders `(-x)`, pyast serializes
//     `(- x)` — no byte-equivalent node form.
//   - the jsonParseArray index guard (`x[i] if len(x) > i else {}`) lowers
//     as a pyast ternary with parens — L2 semantic-equivalence exemption
//     (P5.12), asserted by TestJSONParseArrayGuardParity.
//   - template strings are rejected: the flat `"a" + str(x) + "b"` join has
//     no n-ary pyast node.
//   - map/array literals are rejected (renderConditionPython errors on them,
//     P4 explicit-error boundary).
func lowerConditionPyast(e ast.Expr, g *Builder, bindings *[]textBinding) (pyast.Expr, bool, error) {
	switch x := e.(type) {
	case *ast.StringLit:
		return &pyast.StringExpr{Value: x.Value()}, true, nil
	case *ast.IntLit:
		return &pyast.IntExpr{Value: x.Value()}, true, nil
	case *ast.FloatLit:
		return &pyast.FloatExpr{Value: x.Value()}, true, nil
	case *ast.BoolLit:
		return &pyast.BoolExpr{Value: x.Value()}, true, nil
	case *ast.NullLit:
		return &pyast.NoneExpr{}, true, nil
	case *ast.IdentExpr:
		rendered, err := bindConditionPython(x.Name(), x, g, bindings)
		if err != nil {
			return nil, false, err
		}
		return &pyast.NameExpr{Name: rendered}, true, nil
	case *ast.FieldAccessExpr:
		return lowerFieldAccessPyast(x, g, bindings)
	case *ast.IndexExpr:
		// E5-b record-array member chains (rec.result[0].output) resolve
		// onto dotted output ports and bind like a field access.
		if _, ok := g.resolveValueRef(x); ok {
			name := indexExprBindName(x)
			rendered, err := bindConditionPython(name, x, g, bindings)
			if err != nil {
				return nil, false, err
			}
			return &pyast.NameExpr{Name: rendered}, true, nil
		}
		// jsonParseArray guard form (`x[i] if len(x) > i else {}`, G12-2):
		// lower as a pyast ternary. The serializer always parenthesizes the
		// ternary `(x[i] if len(x) > i else {})` vs the legacy paren-free
		// dict-value form — L2 semantic-equivalence exemption (P5.12, same
		// family as the P5.11 array-branch ternary).
		if id, ok := x.Obj().(*ast.IdentExpr); ok {
			if ref, ok2 := g.varToOutput[id.Name()]; ok2 && ref.jsonParseArray {
				obj, ok, err := lowerCondChild(x.Obj(), g, bindings)
				if err != nil || !ok {
					return nil, false, err
				}
				idx, ok, err := lowerCondChild(x.Index(), g, bindings)
				if err != nil || !ok {
					return nil, false, err
				}
				cond := &pyast.BinaryExpr{
					Op:    ">",
					Left:  &pyast.CallExpr{Func: "len", Args: []pyast.Expr{obj}},
					Right: idx,
				}
				return &pyast.TernaryExpr{
					Cond: cond,
					Then: &pyast.SubscriptExpr{Obj: obj, Index: idx},
					Else: &pyast.DictExpr{},
				}, true, nil
			}
		}
		obj, ok, err := lowerCondChild(x.Obj(), g, bindings)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		idx, ok, err := lowerCondChild(x.Index(), g, bindings)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		return &pyast.SubscriptExpr{Obj: obj, Index: idx}, true, nil
	case *ast.BinaryExpr:
		// Children lower raw (not via lowerCondChild): the serializer's
		// parenNeeded now parenthesizes every binary child of a binary,
		// reproducing the legacy always-paren bytes for same-precedence
		// chains without double parens.
		left, ok1, err := lowerConditionPyast(x.Left(), g, bindings)
		if err != nil {
			return nil, false, err
		}
		if !ok1 {
			return nil, false, nil
		}
		right, ok2, err := lowerConditionPyast(x.Right(), g, bindings)
		if err != nil {
			return nil, false, err
		}
		if !ok2 {
			return nil, false, nil
		}
		op := x.Op()
		switch op {
		case "&&":
			op = "and"
		case "||":
			op = "or"
		case "??":
			return coalescePyast(left, right), true, nil
		}
		return &pyast.BinaryExpr{Op: op, Left: left, Right: right}, true, nil
	case *ast.UnaryExpr:
		op := x.Op()
		if op == "!" {
			op = "not"
		}
		operand, ok, err := lowerCondChild(x.Operand(), g, bindings)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		return &pyast.UnaryExpr{Op: op, Operand: operand}, true, nil
	case *ast.TernaryExpr:
		c, ok1, err := lowerCondChild(x.Condition(), g, bindings)
		if err != nil {
			return nil, false, err
		}
		if !ok1 {
			return nil, false, nil
		}
		t, ok2, err := lowerCondChild(x.ThenVal(), g, bindings)
		if err != nil {
			return nil, false, err
		}
		if !ok2 {
			return nil, false, nil
		}
		el, ok3, err := lowerCondChild(x.ElseVal(), g, bindings)
		if err != nil {
			return nil, false, err
		}
		if !ok3 {
			return nil, false, nil
		}
		return &pyast.TernaryExpr{Cond: c, Then: t, Else: el}, true, nil
	case *ast.TemplateString:
		// `a${x}b` → flat `"a" + str(x) + "b"` join (legacy
		// renderConditionPython TemplateString branch). Parts always start
		// and end with a literal (ast.py contract); expr parts render as
		// str(<part>) with the part lowered through the condition renderer
		// (always-paren emulation included).
		var parts []pyast.Expr
		for _, part := range x.Parts() {
			if part.Expr == nil {
				parts = append(parts, &pyast.StringExpr{Value: part.Text})
				continue
			}
			inner, ok, err := lowerCondChild(part.Expr, g, bindings)
			if err != nil {
				return nil, false, err
			}
			if !ok {
				return nil, false, nil
			}
			parts = append(parts, &pyast.CallExpr{Func: "str", Args: []pyast.Expr{inner}})
		}
		if len(parts) == 0 {
			return &pyast.StringExpr{Value: ""}, true, nil
		}
		if len(parts) == 1 {
			return parts[0], true, nil
		}
		return &pyast.ConcatExpr{Parts: parts}, true, nil
	case *ast.CallExpr:
		// 条件位专属的方法形态谓词 → 函数形态：与字符串模板路径
		// `renderConditionPython` **同解**（同一处规则 `conditionPredicateValueForm`），
		// 两条路径因此拿到同一份 calc-builtin 字节（P5.6）。
		if fn, ok := conditionPredicateValueForm(x); ok {
			return lowerConditionPyast(fn, g, bindings)
		}
		if name := calleeName(x.Func()); name == "file" {
			// file(x) 透明透传 (P5 parity): 渲染内层表达式, 与
			// renderConditionPython 的 file 分支字节等价（lowerCondChild
			// 对 binary 包 ParenExpr, 与 legacy 恒加括号一致）。
			if len(x.Args()) != 1 {
				return nil, false, &UnsupportedError{Construct: "call \"file\" expects 1 argument"}
			}
			return lowerCondChild(x.Args()[0], g, bindings)
		}
		// G10 calc builtins (P5.1 path): the call lowers via pygen when
		// registered and every argument lowers cleanly.
		if pa, ok, err := lowerCalcBuiltin(x, g, bindings); err != nil {
			return nil, false, err
		} else if ok {
			return pa, true, nil
		}
	}
	return nil, false, nil
}

// lowerCondChild lowers an expression that the legacy renderer would feed
// through renderConditionPython, wrapping any lowered BinaryExpr in
// ParenExpr to reproduce the legacy always-parenthesized binary bytes.
func lowerCondChild(e ast.Expr, g *Builder, bindings *[]textBinding) (pyast.Expr, bool, error) {
	pa, ok, err := lowerConditionPyast(e, g, bindings)
	if err != nil || !ok {
		return nil, ok, err
	}
	if _, isBin := pa.(*pyast.BinaryExpr); isBin {
		return &pyast.ParenExpr{X: pa}, true, nil
	}
	return pa, true, nil
}

// lowerLambdaPyast mirrors exprToPython (the lambda-subset renderer used by
// emitMapOp): the lambda parameter renders as the given Python variable
// name, literals as themselves, binaries always parenthesized, `!` as
// `not`, and field-access method calls (contains/not_contains/startsWith/
// endsWith) as their Python forms. ok=false keeps the legacy exprToPython
// string path for any shape with no byte-equivalent pyast form (free vars,
// unary minus, other method calls).
func lowerLambdaPyast(e ast.Expr, ncodaParam, pyParam string) (pyast.Expr, bool, error) {
	switch x := e.(type) {
	case *ast.IdentExpr:
		if x.Name() == ncodaParam {
			return &pyast.NameExpr{Name: pyParam}, true, nil
		}
		return nil, false, nil // free var — legacy exprToPython errors
	case *ast.StringLit:
		return &pyast.StringExpr{Value: x.Value()}, true, nil
	case *ast.IntLit:
		return &pyast.IntExpr{Value: x.Value()}, true, nil
	case *ast.FloatLit:
		return &pyast.FloatExpr{Value: x.Value()}, true, nil
	case *ast.BoolLit:
		return &pyast.BoolExpr{Value: x.Value()}, true, nil
	case *ast.BinaryExpr:
		left, ok1, err := lowerLambdaPyast(x.Left(), ncodaParam, pyParam)
		if err != nil {
			return nil, false, err
		}
		if !ok1 {
			return nil, false, nil
		}
		right, ok2, err := lowerLambdaPyast(x.Right(), ncodaParam, pyParam)
		if err != nil {
			return nil, false, err
		}
		if !ok2 {
			return nil, false, nil
		}
		op := x.Op()
		switch op {
		case "&&":
			op = "and"
		case "||":
			op = "or"
		case "??":
			return coalescePyast(left, right), true, nil
		}
		// Children lower raw: the serializer parens binary children of a
		// binary (P5.7 parenNeeded), reproducing exprToPython's always-paren
		// output; the top-level binary gets parens at the comprehension-elt
		// call site.
		return &pyast.BinaryExpr{Op: op, Left: left, Right: right}, true, nil
	case *ast.UnaryExpr:
		op := x.Op()
		if op == "!" {
			op = "not"
		}
		operand, ok, err := lowerLambdaChild(x.Operand(), ncodaParam, pyParam)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		return &pyast.UnaryExpr{Op: op, Operand: operand}, true, nil
	case *ast.CallExpr:
		fa, ok := x.Func().(*ast.FieldAccessExpr)
		if !ok || len(x.Args()) != 1 {
			return nil, false, nil
		}
		obj, ok, err := lowerLambdaChild(fa.Obj(), ncodaParam, pyParam)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		arg, ok, err := lowerLambdaChild(x.Args()[0], ncodaParam, pyParam)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		switch fa.FieldName() {
		case "contains":
			// (arg in obj)
			return &pyast.BinaryExpr{Op: "in", Left: arg, Right: obj}, true, nil
		case "not_contains":
			return &pyast.BinaryExpr{Op: "not in", Left: arg, Right: obj}, true, nil
		case "startsWith":
			return &pyast.CallExpr{Obj: obj, Func: "startswith", Args: []pyast.Expr{arg}}, true, nil
		case "endsWith":
			return &pyast.CallExpr{Obj: obj, Func: "endswith", Args: []pyast.Expr{arg}}, true, nil
		}
		return nil, false, nil
	}
	return nil, false, nil
}

// lowerLambdaChild lowers an expression that exprToPython would render
// inside a lambda body, wrapping any BinaryExpr result in ParenExpr (the
// legacy renderer parenthesizes binaries in method-call/contains positions:
// `(a + b).startswith(x)`).
func lowerLambdaChild(e ast.Expr, ncodaParam, pyParam string) (pyast.Expr, bool, error) {
	pa, ok, err := lowerLambdaPyast(e, ncodaParam, pyParam)
	if err != nil || !ok {
		return nil, ok, err
	}
	if _, isBin := pa.(*pyast.BinaryExpr); isBin {
		return &pyast.ParenExpr{X: pa}, true, nil
	}
	return pa, true, nil
}

// keyQuoteSafe reports whether a string is rendered identically by
// quoteString (pyast serializer) and json.Marshal (legacy map-key renderer):
// printable ASCII except '<', '>', '&' (json escapes those as
// \u003c/\u003e/\u0026) and except DEL (json emits it raw, quoteString
// escapes \u007f). Non-ASCII is unsafe too (json emits raw UTF-8,
// quoteString escapes \uXXXX).
func keyQuoteSafe(s string) bool {
	for _, r := range s {
		if r < 0x20 || r > 0x7e || r == '<' || r == '>' || r == '&' {
			return false
		}
	}
	return true
}

// namedParams converts parameter names to untyped pyast Param nodes.
func namedParams(names []string) []pyast.Param {
	nodes := make([]pyast.Param, 0, len(names))
	for _, n := range names {
		nodes = append(nodes, pyast.Param{Name: n})
	}
	return nodes
}

// lowerInlinePyast mirrors renderInlinePython (the code-function body
// renderer): a parameter identifier substitutes its argument (lowered
// through the condition renderer, always-paren emulation included);
// binary/unary/ternary recurse param-aware; every other shape delegates to
// lowerConditionPyast (which, like the legacy default branch, does NOT
// substitute parameters inside field access / index / call shapes — those
// error identically on both paths). ok=false falls back to the legacy
// renderInlinePython string path.
func lowerInlinePyast(e ast.Expr, params map[string]ast.Expr, g *Builder, bindings *[]textBinding) (pyast.Expr, bool, error) {
	if id, ok := e.(*ast.IdentExpr); ok {
		if arg, exists := params[id.Name()]; exists {
			return lowerCondChild(arg, g, bindings)
		}
	}
	switch x := e.(type) {
	case *ast.BinaryExpr:
		left, ok1, err := lowerInlinePyast(x.Left(), params, g, bindings)
		if err != nil {
			return nil, false, err
		}
		if !ok1 {
			return nil, false, nil
		}
		right, ok2, err := lowerInlinePyast(x.Right(), params, g, bindings)
		if err != nil {
			return nil, false, err
		}
		if !ok2 {
			return nil, false, nil
		}
		op := x.Op()
		switch op {
		case "&&":
			op = "and"
		case "||":
			op = "or"
		case "??":
			return coalescePyast(left, right), true, nil
		}
		return &pyast.BinaryExpr{Op: op, Left: left, Right: right}, true, nil
	case *ast.UnaryExpr:
		op := x.Op()
		if op == "!" {
			op = "not"
		}
		operand, ok, err := lowerInlineChild(x.Operand(), params, g, bindings)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		return &pyast.UnaryExpr{Op: op, Operand: operand}, true, nil
	case *ast.TernaryExpr:
		c, ok1, err := lowerInlineChild(x.Condition(), params, g, bindings)
		if err != nil {
			return nil, false, err
		}
		if !ok1 {
			return nil, false, nil
		}
		t, ok2, err := lowerInlineChild(x.ThenVal(), params, g, bindings)
		if err != nil {
			return nil, false, err
		}
		if !ok2 {
			return nil, false, nil
		}
		el, ok3, err := lowerInlineChild(x.ElseVal(), params, g, bindings)
		if err != nil {
			return nil, false, err
		}
		if !ok3 {
			return nil, false, nil
		}
		return &pyast.TernaryExpr{Cond: c, Then: t, Else: el}, true, nil
	default:
		return lowerConditionPyast(e, g, bindings)
	}
}

// lowerInlineChild lowers an expression rendered inside a code-function body
// in a non-binary context (ternary arm/condition, unary operand), wrapping
// any BinaryExpr result in ParenExpr (legacy renderInlinePython
// parenthesizes binaries in those positions too).
func lowerInlineChild(e ast.Expr, params map[string]ast.Expr, g *Builder, bindings *[]textBinding) (pyast.Expr, bool, error) {
	pa, ok, err := lowerInlinePyast(e, params, g, bindings)
	if err != nil || !ok {
		return nil, ok, err
	}
	if _, isBin := pa.(*pyast.BinaryExpr); isBin {
		return &pyast.ParenExpr{X: pa}, true, nil
	}
	return pa, true, nil
}

// lowerInlineShimExpr lowers a code-function return expression for a
// single-"result" shim with param substitution; the top-level binary is
// parenthesized like the legacy shim body (`{"result": (a + b)}`).
func lowerInlineShimExpr(e ast.Expr, params map[string]ast.Expr, g *Builder, bindings *[]textBinding) (pyast.Expr, bool, error) {
	pa, ok, err := lowerInlinePyast(e, params, g, bindings)
	if err != nil || !ok {
		return nil, ok, err
	}
	if hasMapOrList(pa) {
		return nil, false, nil
	}
	if _, isBin := pa.(*pyast.BinaryExpr); isBin {
		pa = &pyast.ParenExpr{X: pa}
	}
	return pa, true, nil
}

// lowerInlineShimCode renders the full single-"result" shim source for a
// code-function return through the pyast path; ok=false falls back to the
// legacy renderInlinePython template (byte-identical fallback).
func lowerInlineShimCode(e ast.Expr, params map[string]ast.Expr, g *Builder, bindings *[]textBinding) (string, bool, error) {
	pa, ok, err := lowerInlineShimExpr(e, params, g, bindings)
	if err != nil || !ok {
		return "", ok, err
	}
	return wrapShim(shimParamNodes(*bindings), pa), true, nil
}

// jsonDefaultPyast mirrors jsonDefaultLit: the Python default literal for a
// JSON extraction field of the given type (string → "", integer/number → 0,
// boolean → false, array → [], anything else → None), as a pyast node.
func jsonDefaultPyast(t *Type) pyast.Expr {
	switch t.Kind {
	case "integer", "number":
		return &pyast.IntExpr{Value: 0}
	case "boolean":
		return &pyast.BoolExpr{Value: false}
	case "string":
		return &pyast.StringExpr{Value: ""}
	case "list", "array":
		return &pyast.ListExpr{}
	default:
		return &pyast.NoneExpr{}
	}
}

// lowerJsonParseShim mirrors buildJSONParse's two code templates (G11) via
// the pyast path: a multi-statement body — `data = json.loads(<input>)` +
// `return {...}`. The array branch guards with `isinstance(data, list)`; the
// record branch emits one `data.get("<f>", <default>)` per field.
//
// Semantic-equivalence exemptions (L2 whitelist, P5.10a/P5.11):
//   - array branch ternary: pyast serializes `(data if isinstance(data, list)
//     else [])` with parens, legacy omits them in dict-value position — same
//     evaluation, asserted by TestJsonParseParity;
//   - record branch multi-key dict: pyast sorts keys, legacy keeps field
//     declaration order — key SET identical (P5.10a).
//
// Field names must be quote-safe (pythonJSONString vs quoteString escape
// family): a quote-unsafe field name falls back to the legacy template.
// ok=false is the fallback signal; the caller keeps today's exact bytes.
func lowerJsonParseShim(g *Builder, inputName string, fields []*ast.FieldDecl, arrayElem *Type) (string, bool) {
	data := &pyast.NameExpr{Name: "data"}
	assign := &pyast.AssignStmt{
		Name:  "data",
		Value: &pyast.CallExpr{Func: "json.loads", Args: []pyast.Expr{&pyast.NameExpr{Name: inputName}}},
	}
	params := []pyast.Param{{Name: inputName}}
	if arrayElem != nil {
		cond := &pyast.CallExpr{Func: "isinstance", Args: []pyast.Expr{data, &pyast.NameExpr{Name: "list"}}}
		ternary := &pyast.TernaryExpr{Cond: cond, Then: data, Else: &pyast.ListExpr{}}
		ret := &pyast.ReturnStmt{Value: &pyast.DictExpr{Items: []pyast.DictItem{
			{Key: &pyast.StringExpr{Value: "output"}, Value: ternary},
		}}}
		return wrapShimStmts(params, []string{"json"}, []pyast.Stmt{assign, ret}), true
	}
	items := make([]pyast.DictItem, 0, len(fields))
	for _, f := range fields {
		items = append(items, pyast.DictItem{
			Key: &pyast.StringExpr{Value: f.Name()},
			Value: &pyast.CallExpr{
				Obj:  data,
				Func: "get",
				Args: []pyast.Expr{&pyast.StringExpr{Value: f.Name()}, jsonDefaultPyast(g.typeOf(f.TypeRef()))},
			},
		})
	}
	// 整个解析结果挂在**唯一真实口 `output`** 之下（记录形态）——与语料同形：
	// coze-biz 里「一个口承载整个记录、成员经 dotted 路径读」是平台自身的形状
	// （672 份真实导出：`output` 是 code 节点最常见的单口名 227/621；指向 code
	// 节点的 65 处 dotted 引用里，56 处首段就是该节点声明的且为 `output`；
	// 15 个 code 节点的 object 口带 `schema` 成员表）。口是 python **真写出的键**
	// ⇒ 声明集 = 产出集，成员读 `output.<field>` 才有承载者（详见 codeMemberPort
	// 头注与 docs/five-step/rules/C3-code-ports.yaml）。
	ret := &pyast.ReturnStmt{Value: &pyast.DictExpr{Items: []pyast.DictItem{
		{Key: &pyast.StringExpr{Value: "output"}, Value: &pyast.DictExpr{Items: items}},
	}}}
	return wrapShimStmts(params, []string{"json"}, []pyast.Stmt{assign, ret}), true
}
