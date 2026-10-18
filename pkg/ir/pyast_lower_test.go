package ir

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/pyast"
	"github.com/nodecoda/ncoda/pkg/pygen"
)

// TestWrapShimByteEquivalent: `wrapShim` output must equal the template
// fmt.Sprintf("def main(%s) -> dict:\n    return {\"result\": %s}", ...).
func TestWrapShimByteEquivalent(t *testing.T) {
	cases := []struct {
		name   string
		params []pyast.Param
		expr   string
		want   string
	}{
		{
			name:   "no-params",
			params: nil,
			expr:   `"hi"`,
			want: `# ncoda:shim 1
def main() -> dict:
    return {"result": "hi"}`,
		},
		{
			name:   "single-param",
			params: []pyast.Param{{Name: "arg1"}},
			expr:   "arg1",
			want: `# ncoda:shim 1
def main(arg1) -> dict:
    return {"result": arg1}`,
		},
		{
			name:   "multi-param",
			params: []pyast.Param{{Name: "a"}, {Name: "b"}},
			expr:   "len(a)",
			want: `# ncoda:shim 1
def main(a, b) -> dict:
    return {"result": len(a)}`,
		},
		{
			name:   "multi-output",
			params: []pyast.Param{{Name: "v"}},
			expr:   "v",
			want: `# ncoda:shim 1
def main(v) -> dict:
    return {"result": v}`,
		},
		{
			name:   "typed-param",
			params: []pyast.Param{{Name: "items", Type: "list"}},
			expr:   "item",
			want: `# ncoda:shim 1
def main(items: list) -> dict:
    return {"result": item}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := parseExprOrString(t, c.expr)
			got := wrapShim(c.params, body)
			got = strings.TrimSuffix(got, "\n")
			if got != c.want {
				t.Errorf("wrapShim mismatch:\ngot:\n%s\n\nwant:\n%s", got, c.want)
			}
		})
	}
}

func parseExprOrString(t *testing.T, s string) pyast.Expr {
	t.Helper()
	switch s {
	case `"hi"`:
		return &pyast.StringExpr{Value: "hi"}
	case "arg1", "v", "a", "b", "item":
		return &pyast.NameExpr{Name: s}
	case "len(a)":
		return &pyast.CallExpr{Func: "len", Args: []pyast.Expr{&pyast.NameExpr{Name: "a"}}}
	}
	t.Fatalf("unhandled expr text %q", s)
	return nil
}

// TestLowerStmtThreeState: lowerStmt follows the three-state protocol from
// TRANSLATION-PATTERN.md §5 — expressible shapes lower to pyast, inexpressible
// shapes return ok=false so the caller falls back to the legacy string shim.
func TestLowerStmtThreeState(t *testing.T) {
	b := &Builder{}
	cases := []struct {
		name string
		stmt ast.Stmt
		want pyast.Stmt // non-nil => expect ok=true and this exact node type/value
	}{
		{
			name: "return-with-value",
			stmt: ast.NewReturnStmt(ast.NewStringLit("hi")),
			want: &pyast.ReturnStmt{Value: &pyast.StringExpr{Value: "hi"}},
		},
		{
			name: "return-bare-fallback", // bare return; — nil value not expressible → ok=false
			stmt: ast.NewReturnStmt(nil),
			want: nil,
		},
		{
			name: "var-decl-with-init",
			stmt: ast.NewVarDeclStmt("x", nil, ast.NewStringLit("hi"), true),
			want: &pyast.AssignStmt{Name: "x", Value: &pyast.StringExpr{Value: "hi"}},
		},
		{
			name: "var-decl-no-init-fallback",
			stmt: ast.NewVarDeclStmt("x", nil, nil, true),
			want: nil,
		},
		{
			name: "expr-stmt-fallback", // pyast has no ExprStmt yet — ok=false
			stmt: ast.NewExprStmt(ast.NewStringLit("hi")),
			want: nil,
		},
		{
			name: "unknown-stmt-fallback",
			stmt: ast.NewBreakStmt(),
			want: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok, err := lowerStmt(c.stmt, b, nil)
			if err != nil {
				t.Fatalf("lowerStmt(%T) error: %v", c.stmt, err)
			}
			if c.want == nil {
				if ok {
					t.Fatalf("lowerStmt(%T) = ok, want fallback (ok=false)", c.stmt)
				}
				if got != nil {
					t.Fatalf("lowerStmt(%T) returned %T with ok=false, want nil", c.stmt, got)
				}
				return
			}
			if !ok {
				t.Fatalf("lowerStmt(%T) = ok=false, want true", c.stmt)
			}
			if !stmtEqual(got, c.want) {
				t.Fatalf("lowerStmt(%T) mismatch:\ngot:  %#v\nwant: %#v", c.stmt, got, c.want)
			}
		})
	}
}

// stmtEqual compares pyast statements structurally (pyast nodes are plain
// structs without an Eq method).
func stmtEqual(a, b pyast.Stmt) bool {
	ra, aok := a.(*pyast.ReturnStmt)
	rb, bok := b.(*pyast.ReturnStmt)
	if aok && bok {
		return ra.Value != nil && rb.Value != nil && ra.Value.(*pyast.StringExpr).Value == rb.Value.(*pyast.StringExpr).Value
	}
	aa, aok := a.(*pyast.AssignStmt)
	ab, bok := b.(*pyast.AssignStmt)
	if aok && bok {
		return aa.Name == ab.Name &&
			aa.Value != nil && ab.Value != nil &&
			aa.Value.(*pyast.StringExpr).Value == ab.Value.(*pyast.StringExpr).Value
	}
	return false
}

// TestShimCodeParity is the P5.5 migration gate: for every shape the pyast
// shim path accepts, shimCodeFor must be byte-identical to the legacy
// renderConditionPython template (PYTHON-SUBSET-GENERATOR §6: 字节等价 =
// 迁移门禁). Shapes that must NOT take the pyast path (comparisons,
// map/array literals — renderConditionPython rejects them, P4 boundary)
// assert ok=false so the caller falls back to today's exact bytes.
func TestShimCodeParity(t *testing.T) {
	orBin := func(a, b ast.Expr) *ast.BinaryExpr {
		return ast.NewBinaryExpr("||", a, b)
	}
	bound := &Builder{varToOutput: map[string]varRef{
		"x":     {nodeID: "n1", output: "out", kind: "code"},
		"a":     {nodeID: "n1", output: "out", kind: "code"},
		"b":     {nodeID: "n1", output: "out", kind: "code"},
		"c":     {nodeID: "n1", output: "out", kind: "code"},
		"d":     {nodeID: "n1", output: "out", kind: "code"},
		"items": {nodeID: "n1", output: "out", kind: "code"},
		"y":     {nodeID: "n1", output: "out", kind: "code"},
		"z":     {nodeID: "n1", output: "out", kind: "code"},
	}}
	cases := []struct {
		name string
		g    *Builder
		e    ast.Expr
		want bool // true = pyast path taken; false = must fall back
	}{
		{name: "string", e: ast.NewStringLit("hi"), want: true},
		{name: "empty-string", e: ast.NewStringLit(""), want: true},
		{name: "escaped-string", e: ast.NewStringLit("a\nb"), want: true},
		{name: "non-ascii-string", e: ast.NewStringLit("中文"), want: true},
		{name: "int", e: ast.NewIntLit(7), want: true},
		{name: "float", e: ast.NewFloatLit(1.5), want: true},
		{name: "bool-true", e: ast.NewBoolLit(true), want: true},
		{name: "bool-false", e: ast.NewBoolLit(false), want: true},
		{name: "null", e: ast.NewNullLit(), want: true},
		{name: "or-value-merge-literals", e: orBin(ast.NewStringLit("hi"), ast.NewStringLit("")), want: true},
		{name: "or-value-merge-bound", g: bound, e: orBin(ast.NewIdentExpr("x"), ast.NewStringLit("")), want: true},
		{name: "ident-bound", g: bound, e: ast.NewIdentExpr("x"), want: true},
		{name: "comparison", e: ast.NewBinaryExpr("<", ast.NewIntLit(1), ast.NewIntLit(2)), want: true},
		{name: "comparison-bound", g: bound, e: ast.NewBinaryExpr("<", ast.NewIdentExpr("a"), ast.NewIdentExpr("b")), want: true},
		{name: "arith-nested-same-prec", g: bound, e: ast.NewBinaryExpr("+", ast.NewBinaryExpr("+", ast.NewIdentExpr("a"), ast.NewIdentExpr("b")), ast.NewIdentExpr("c")), want: true},
		{name: "and-chain", g: bound, e: ast.NewBinaryExpr("&&", ast.NewBinaryExpr("<", ast.NewIdentExpr("a"), ast.NewIdentExpr("b")), ast.NewBinaryExpr(">", ast.NewIdentExpr("c"), ast.NewIdentExpr("d"))), want: true},
		{name: "unary-not", g: bound, e: ast.NewUnaryExpr("!", ast.NewIdentExpr("a")), want: true},
		{name: "ternary", g: bound, e: ast.NewTernaryExpr(ast.NewBinaryExpr(">", ast.NewIdentExpr("a"), ast.NewIdentExpr("b")), ast.NewIdentExpr("x"), ast.NewIdentExpr("y")), want: true},
		{name: "index-bound", g: bound, e: ast.NewIndexExpr(ast.NewIdentExpr("items"), ast.NewIntLit(0)), want: true},
		{name: "calc-len", g: bound, e: ast.NewCallExpr(ast.NewIdentExpr("len"), nil, []ast.Expr{ast.NewIdentExpr("a")}), want: true},
		{name: "template", g: bound, e: ast.NewTemplateString([]ast.TemplatePart{{Text: "a"}, {Expr: ast.NewIdentExpr("x")}, {Text: "b"}}), want: true},
		{name: "template-binary-part", g: bound, e: ast.NewTemplateString([]ast.TemplatePart{{Text: "v="}, {Expr: ast.NewBinaryExpr("+", ast.NewIdentExpr("a"), ast.NewIdentExpr("b"))}}), want: true},
		{name: "template-plus-ternary", g: bound, e: ast.NewBinaryExpr("+", ast.NewTemplateString([]ast.TemplatePart{{Text: "问题："}, {Expr: ast.NewIdentExpr("x")}, {Text: ""}}), ast.NewTernaryExpr(ast.NewBinaryExpr("!=", ast.NewIdentExpr("a"), ast.NewStringLit("")), ast.NewTemplateString([]ast.TemplatePart{{Text: "\n上下文："}, {Expr: ast.NewIdentExpr("b")}, {Text: ""}}), ast.NewStringLit(""))), want: true},
		{name: "unary-not-in-and", g: bound, e: ast.NewBinaryExpr("&&", ast.NewUnaryExpr("!", ast.NewIdentExpr("a")), ast.NewIdentExpr("b")), want: true},
		{name: "ternary-in-add", g: bound, e: ast.NewBinaryExpr("+", ast.NewIdentExpr("x"), ast.NewTernaryExpr(ast.NewBinaryExpr(">", ast.NewIdentExpr("a"), ast.NewIdentExpr("b")), ast.NewIdentExpr("y"), ast.NewIdentExpr("z"))), want: true},
		{name: "template-in-add", g: bound, e: ast.NewBinaryExpr("+", ast.NewTemplateString([]ast.TemplatePart{{Text: "a"}, {Expr: ast.NewIdentExpr("x")}, {Text: ""}}), ast.NewIdentExpr("y")), want: true},
		{name: "unary-minus", g: bound, e: ast.NewUnaryExpr("-", ast.NewIdentExpr("a")), want: true},
		{name: "unary-minus-over-binary", g: bound, e: ast.NewUnaryExpr("-", ast.NewBinaryExpr("+", ast.NewIdentExpr("a"), ast.NewIdentExpr("b"))), want: true},
		{name: "unary-minus-in-binary", g: bound, e: ast.NewBinaryExpr("+", ast.NewIdentExpr("a"), ast.NewUnaryExpr("-", ast.NewIdentExpr("b"))), want: true},
		{name: "map-fallback", e: ast.NewMapLit([]ast.MapEntry{{Key: ast.NewStringLit("k"), Value: ast.NewStringLit("v")}}), want: false},
		{name: "array-fallback", e: ast.NewArrayLit([]ast.Expr{ast.NewStringLit("a")}), want: false},
		{name: "or-map-arm-fallback", e: orBin(ast.NewStringLit("x"), ast.NewMapLit([]ast.MapEntry{{Key: ast.NewStringLit("k"), Value: ast.NewStringLit("v")}})), want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.g == nil {
				c.g = &Builder{}
			}
			var pb, lb []textBinding
			code, ok, err := shimCodeFor(c.e, c.g, &pb)
			if err != nil {
				t.Fatalf("shimCodeFor error: %v", err)
			}
			if !c.want {
				// Boundary stability: shapes outside the renderConditionPython
				// accept set must NOT take the pyast path (P4 explicit-error
				// boundary preserved — legacy rejects map/array literals).
				if ok {
					t.Fatalf("shimCodeFor = ok, want fallback (ok=false); pyast:\n%s", code)
				}
				if code != "" {
					t.Fatalf("shimCodeFor returned code %q with ok=false, want empty", code)
				}
				return
			}
			legacyExpr, lerr := renderConditionPython(c.e, c.g, &lb)
			if lerr != nil {
				t.Fatalf("legacy render error: %v", lerr)
			}
			legacy := pyast.ShimMarker + fmt.Sprintf("def main(%s) -> dict:\n    return {\"result\": %s}",
				strings.Join(shimParamNames(lb), ", "), legacyExpr)
			if !ok {
				t.Fatalf("shimCodeFor = ok=false, want pyast path")
			}
			if code != legacy {
				t.Errorf("byte drift:\npyast:\n%s\n\nlegacy:\n%s", code, legacy)
			}
		})
	}
}

// TestEmitMapOpParity is the P5.8 migration gate for emitMapOp: the pyast
// path (typed `items: list` param + ListCompExpr elt) must be byte-identical
// to the legacy exprToPython comprehension template for every lambda body
// it accepts, and byte-identical for fallback shapes too (unary minus).
func TestEmitMapOpParity(t *testing.T) {
	method := func(obj ast.Expr, name string, args ...ast.Expr) *ast.CallExpr {
		return ast.NewCallExpr(ast.NewFieldAccessExpr(obj, name), nil, args)
	}
	call := func(body ast.Expr) *ast.CallExpr {
		return ast.NewCallExpr(ast.NewIdentExpr("map"), nil,
			[]ast.Expr{ast.NewIdentExpr("items"), ast.NewLambdaExpr([]string{"x"}, body)})
	}
	bodies := map[string]ast.Expr{
		"bare-ident":   ast.NewIdentExpr("x"),
		"eq-literal":   ast.NewBinaryExpr("==", ast.NewIdentExpr("x"), ast.NewStringLit("y")),
		"arith":        ast.NewBinaryExpr("+", ast.NewIdentExpr("x"), ast.NewIntLit(1)),
		"and-chain":    ast.NewBinaryExpr("&&", ast.NewBinaryExpr(">", ast.NewIdentExpr("x"), ast.NewIntLit(0)), ast.NewBinaryExpr("<", ast.NewIdentExpr("x"), ast.NewIntLit(10))),
		"contains":     method(ast.NewIdentExpr("x"), "contains", ast.NewStringLit("a")),
		"not-contains": method(ast.NewIdentExpr("x"), "not_contains", ast.NewStringLit("a")),
		"starts-with":  method(ast.NewIdentExpr("x"), "startsWith", ast.NewStringLit("a")),
		"ends-with":    method(ast.NewIdentExpr("x"), "endsWith", ast.NewStringLit("b")),
		"not-unary":    ast.NewUnaryExpr("!", ast.NewIdentExpr("x")),
		"unary-minus":  ast.NewUnaryExpr("-", ast.NewIdentExpr("x")),
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			g := &Builder{}
			c := call(body)
			ref, err := g.emitMapOp(c, varRef{}, nil, "")
			if err != nil {
				t.Fatalf("emitMapOp error: %v", err)
			}
			pred, perr := lambdaPredicate(c, "item")
			if perr != nil {
				t.Fatalf("legacy predicate error: %v", perr)
			}
			legacy := pyast.ShimMarker + "def main(items: list) -> dict:\n    return {\"result\": [" + pred + " for item in items]}"
			if ref.kind == "" {
				t.Fatalf("emitMapOp returned empty varRef")
			}
			n := g.find(ref.nodeID)
			cr, ok := n.(*CodeRun)
			if !ok {
				t.Fatalf("node type = %T, want *CodeRun", n)
			}
			if cr.Code != legacy {
				t.Errorf("byte drift:\npyast:\n%s\n\nlegacy:\n%s", cr.Code, legacy)
			}
		})
	}

	// Shapes both paths reject: emitMapOp must surface the same error the
	// legacy lambdaPredicate renderer produces (free vars, unsupported
	// method calls) — the pyast path never silently accepts them.
	errBodies := map[string]ast.Expr{
		"free-var":       ast.NewIdentExpr("other"),
		"unknown-method": method(ast.NewIdentExpr("x"), "strip"),
		"no-lambda":      ast.NewIdentExpr("items"),
	}
	for name, body := range errBodies {
		t.Run(name, func(t *testing.T) {
			g := &Builder{}
			c := call(body)
			_, err := g.emitMapOp(c, varRef{}, nil, "")
			if err == nil {
				t.Fatalf("emitMapOp succeeded, want error (legacy also errors)")
			}
			if _, perr := lambdaPredicate(c, "item"); perr == nil {
				t.Fatalf("legacy predicate succeeded, want error")
			}
		})
	}
}

// TestBuildArrayParity is the P5.9 migration gate for buildArray: the pyast
// path must be byte-identical to the legacy array-shim template for every
// element set it accepts (bound idents, literals, single-key map elements),
// and byte-identical for fallback shapes too (quote-unsafe keys — rendered
// through the legacy json.Marshal template). Multi-key dict elements are
// covered by TestBuildArraySemanticParity (key-order exemption).
func TestBuildArrayParity(t *testing.T) {
	// Fixtures must carry the producer's output contract: refType resolves
	// authoritative types only (TypePass / varRef.typ / port registry) and
	// errors instead of forging string — a hand-built IR without a typed
	// program has to declare its own contract.
	// 参数名 = **值身份**的函数（`arrayElemParamName` 头注 / R-104）：名字只认
	// 被解析引用的端口名，**不认源拼写**。夹具因此必须给出真实身份：
	//   · x / y —— 两个**不同产出方**、**同一个端口名** `out` ⇒ 第二个落位置名
	//     （同名两参数会让 shim 的按名投影失去身份）。
	//   · px / py —— 端口名互不相同 ⇒ 各用端口名（语义名档位）。
	// 旧夹具把 x 与 y 都指向 `(n1, out)`（**同一个身份**），在新规范下两者必然
	// 同名 ⇒ 它锁的是「按标识符拼写取名」这条已被删除的档位。
	bound := &Builder{varToOutput: map[string]varRef{
		"x":       {nodeID: "n1", output: "out", kind: "code", typ: T("string")},
		"y":       {nodeID: "n2", output: "out", kind: "code", typ: T("string")},
		"px":      {nodeID: "n3", output: "alpha", kind: "code", typ: T("string")},
		"py":      {nodeID: "n4", output: "beta", kind: "code", typ: T("string")},
		"payload": {nodeID: "n1", output: "out", kind: "code", typ: T("string")},
	}}
	legacy := func(params, values []string) string {
		return pyast.ShimMarker + fmt.Sprintf("def main(%s) -> dict:\n    return {\"result\": [%s]}",
			strings.Join(params, ", "), strings.Join(values, ", "))
	}
	cases := []struct {
		name string
		g    *Builder
		arr  *ast.ArrayLit
		want string
	}{
		{
			// 标识符拼写不进名字：x/y 是**另一个**产出方的同端口名 ⇒ [out, arg2]。
			name: "bound-idents-spelling-ignored",
			g:    bound,
			arr:  ast.NewArrayLit([]ast.Expr{ast.NewIdentExpr("x"), ast.NewIdentExpr("y")}),
			want: legacy([]string{"out", "arg2"}, []string{"out", "arg2"}),
		},
		{
			// 端口名互不相同 ⇒ 两个语义名（不再退化成 arg1/arg2）。
			name: "bound-idents-distinct-ports",
			g:    bound,
			arr:  ast.NewArrayLit([]ast.Expr{ast.NewIdentExpr("px"), ast.NewIdentExpr("py")}),
			want: legacy([]string{"alpha", "beta"}, []string{"alpha", "beta"}),
		},
		{
			name: "string-literals",
			g:    &Builder{},
			arr:  ast.NewArrayLit([]ast.Expr{ast.NewStringLit("a"), ast.NewStringLit("b")}),
			want: legacy(nil, []string{`"a"`, `"b"`}),
		},
		{
			name: "single-key-map-element",
			g:    bound,
			arr:  ast.NewArrayLit([]ast.Expr{ast.NewMapLit([]ast.MapEntry{{Key: ast.NewStringLit("fields"), Value: ast.NewIdentExpr("payload")}})}),
			want: legacy([]string{"out"}, []string{`{"fields": out}`}),
		},
		{
			name: "quote-unsafe-key-fallback",
			g:    &Builder{},
			arr:  ast.NewArrayLit([]ast.Expr{ast.NewMapLit([]ast.MapEntry{{Key: ast.NewStringLit(">"), Value: ast.NewIntLit(1)}})}),
			want: legacy(nil, []string{`{"\u003e": 1}`}),
		},
		{
			name: "mixed-ref-and-literal",
			g:    bound,
			arr:  ast.NewArrayLit([]ast.Expr{ast.NewIdentExpr("x"), ast.NewStringLit("a"), ast.NewIdentExpr("y")}),
			want: legacy([]string{"out", "arg3"}, []string{"out", `"a"`, "arg3"}),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := c.g
			id := g.nextID()
			node, err := g.buildArray(id, c.arr)
			if err != nil {
				t.Fatalf("buildArray error: %v", err)
			}
			cr, ok := node.(*CodeRun)
			if !ok {
				t.Fatalf("node type = %T, want *CodeRun", node)
			}
			if cr.Code != c.want {
				t.Errorf("byte drift:\npyast:\n%s\n\nlegacy:\n%s", cr.Code, c.want)
			}
		})
	}
}

// TestLowerLiteralPyastMapGuard: lowerLiteralPyast accepts dicts with
// quote-safe keys regardless of entry count (multi-key dicts lower through
// the sorted-key pyast path — key ORDER is the semantic-equivalence
// exemption; the key SET is preserved). Quote-unsafe and dynamic keys still
// fall back so the legacy json.Marshal / insertion-order bytes are kept.
func TestLowerLiteralPyastMapGuard(t *testing.T) {
	g := &Builder{}
	cases := []struct {
		name    string
		entries []ast.MapEntry
		wantOK  bool
	}{
		{name: "single-safe-key", entries: []ast.MapEntry{{Key: ast.NewStringLit("fields"), Value: ast.NewStringLit("v")}}, wantOK: true},
		{name: "multi-key-accepted", entries: []ast.MapEntry{{Key: ast.NewStringLit("b"), Value: ast.NewStringLit("2")}, {Key: ast.NewStringLit("a"), Value: ast.NewStringLit("1")}}, wantOK: true},
		{name: "gt-key-rejected", entries: []ast.MapEntry{{Key: ast.NewStringLit(">"), Value: ast.NewStringLit("v")}}, wantOK: false},
		{name: "non-ascii-key-rejected", entries: []ast.MapEntry{{Key: ast.NewStringLit("中文"), Value: ast.NewStringLit("v")}}, wantOK: false},
		// 标识符键是**静态键**（名字即键名；键是标签，不是引用）——与字符串键同走
		// pyast 路径。旧夹具把它当「动态键」= 编码了被 D-2（2026-09-14）修掉的那条
		// 错误规则（IR 侧曾用只认字面量的 stringValue 读键 ⇒ `{k: v}` 被静默丢）。
		{name: "identifier-key-accepted", entries: []ast.MapEntry{{Key: ast.NewIdentExpr("k"), Value: ast.NewStringLit("v")}}, wantOK: true},
		// 真正的动态键（键位不是静态名字）必须回退，不得被当成静态键发射。
		{name: "dynamic-key-rejected", entries: []ast.MapEntry{{Key: ast.NewBinaryExpr("+", ast.NewStringLit("a"), ast.NewIdentExpr("q")), Value: ast.NewStringLit("v")}}, wantOK: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pa, ok, err := lowerLiteralPyast(ast.NewMapLit(c.entries), g, &[]textBinding{})
			if err != nil {
				t.Fatalf("lowerLiteralPyast error: %v", err)
			}
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (pa=%v)", ok, c.wantOK, pa)
			}
		})
	}
}

// dictKeySet extracts the string keys of every `"k":` dict item in a Python
// source fragment and returns them sorted. Used by the semantic-equivalence
// assertions: dict key ORDER is exempt from byte parity, but the key SET
// must match exactly. Keys here are quote-safe (printable ASCII minus
// < > &), so a plain `"([^"]+)":` match is unambiguous.
var dictKeyRe = regexp.MustCompile(`"([^"]+)":`)

func dictKeySet(src string) []string {
	seen := map[string]bool{}
	for _, m := range dictKeyRe.FindAllStringSubmatch(src, -1) {
		seen[m[1]] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestBuildArraySemanticParity is the P5.10a gate for the dict key-order
// semantic-equivalence exemption: a multi-key dict array element lowers
// through the pyast path (keys sorted, `{"a": 1, "b": 2, "c": 3}`) instead
// of the legacy insertion-order template (`{"b": 2, "a": 1, "c": 3}`). The
// output bytes differ — the KEY SET must not. (Python dicts are orderless;
// downstream platform consumers read by key, so workflow semantics hold.)
func TestBuildArraySemanticParity(t *testing.T) {
	g := &Builder{}
	arr := ast.NewArrayLit([]ast.Expr{ast.NewMapLit([]ast.MapEntry{
		{Key: ast.NewStringLit("b"), Value: ast.NewIntLit(2)},
		{Key: ast.NewStringLit("a"), Value: ast.NewIntLit(1)},
		{Key: ast.NewStringLit("c"), Value: ast.NewIntLit(3)},
	})})
	id := g.nextID()
	node, err := g.buildArray(id, arr)
	if err != nil {
		t.Fatalf("buildArray error: %v", err)
	}
	cr, ok := node.(*CodeRun)
	if !ok {
		t.Fatalf("node type = %T, want *CodeRun", node)
	}
	// pyast path is taken (sorted keys), NOT the legacy insertion order.
	wantPyast := pyast.ShimMarker + "def main() -> dict:\n    return {\"result\": [{\"a\": 1, \"b\": 2, \"c\": 3}]}"
	if cr.Code != wantPyast {
		t.Errorf("byte drift:\npyast:\n%s\n\nwant:\n%s", cr.Code, wantPyast)
	}
	legacy := pyast.ShimMarker + "def main() -> dict:\n    return {\"result\": [{\"b\": 2, \"a\": 1, \"c\": 3}]}"
	pyastKeys := dictKeySet(cr.Code)
	legacyKeys := dictKeySet(legacy)
	if strings.Join(pyastKeys, ",") != strings.Join(legacyKeys, ",") {
		t.Errorf("key SET drifted:\npyast keys: %v\nlegacy keys: %v", pyastKeys, legacyKeys)
	}
}

// TestInlineCodeCallParity is the P5.10 migration gate for inlineCodeCall:
// the pyast path (param substitution included) must be byte-identical to
// the legacy renderInlinePython template for every return-expression shape
// it accepts; both-error shapes (params inside field access — neither path
// substitutes there) are asserted separately.
//
// 2026-09-17（PE-H1）：比较面**不含** shim 标记 —— `inlineCodeCall` 是**作者写的
// `code fn` 调用**，不是「编译器为 ncoda 表达式物化的 shim」；标记只发给编译器自建
// 的物化（存活侧锚点 = cozebiz_code_fn_call_contract_test.go 的
// TestCozeBizSyntheticShimKeepsMarker）。两条路径的字节一致性主张不变（剥掉标记后
// 必须逐字节相同），故 legacy() 也不带标记。
func TestInlineCodeCallParity(t *testing.T) {
	bound := &Builder{varToOutput: map[string]varRef{
		"x": {nodeID: "n1", output: "out", kind: "code", typ: T("string")},
		"y": {nodeID: "n1", output: "out", kind: "code", typ: T("string")},
	}}
	fn := func(body ast.Expr, paramNames ...string) *ast.FunctionDef {
		params := make([]*ast.ParamDecl, 0, len(paramNames))
		for _, n := range paramNames {
			params = append(params, ast.NewParamDecl(n, nil, nil))
		}
		return ast.NewFunctionDef("f", params, nil, ast.NewBlock([]ast.Stmt{ast.NewReturnStmt(body)}))
	}
	legacy := func(names []string, expr string) string {
		return fmt.Sprintf("def main(%s) -> dict:\n    return {\"result\": %s}",
			strings.Join(names, ", "), expr)
	}
	cases := []struct {
		name string
		g    *Builder
		fn   *ast.FunctionDef
		args []ast.Expr
		want string
	}{
		{
			name: "add-bound",
			g:    bound,
			fn:   fn(ast.NewBinaryExpr("+", ast.NewIdentExpr("a"), ast.NewIdentExpr("b")), "a", "b"),
			args: []ast.Expr{ast.NewIdentExpr("x"), ast.NewIdentExpr("y")},
			want: legacy([]string{"out", "out_1"}, "(out + out_1)"),
		},
		{
			name: "literal-arg",
			g:    &Builder{},
			fn:   fn(ast.NewIdentExpr("a"), "a"),
			args: []ast.Expr{ast.NewStringLit("hi")},
			want: legacy(nil, `"hi"`),
		},
		{
			name: "not-unary",
			g:    bound,
			fn:   fn(ast.NewUnaryExpr("!", ast.NewIdentExpr("a")), "a"),
			args: []ast.Expr{ast.NewIdentExpr("x")},
			want: legacy([]string{"out"}, "(not out)"),
		},
		{
			name: "ternary",
			g:    bound,
			fn:   fn(ast.NewTernaryExpr(ast.NewBinaryExpr(">", ast.NewIdentExpr("a"), ast.NewIdentExpr("b")), ast.NewIdentExpr("a"), ast.NewIdentExpr("b")), "a", "b"),
			args: []ast.Expr{ast.NewIdentExpr("x"), ast.NewIdentExpr("y")},
			want: legacy([]string{"out", "out_1"}, "(out if (out > out_1) else out_1)"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, ref, err := c.g.inlineCodeCall(c.fn, c.args)
			if err != nil {
				t.Fatalf("inlineCodeCall error: %v", err)
			}
			n := c.g.find(ref.nodeID)
			cr, ok := n.(*CodeRun)
			if !ok {
				t.Fatalf("node type = %T, want *CodeRun", n)
			}
			if cr.Code != c.want {
				t.Errorf("byte drift:\npyast:\n%s\n\nlegacy:\n%s", cr.Code, c.want)
			}
		})
	}

	// Param inside a field access: neither path substitutes there — both
	// fail on the unresolvable param name. Assert identical error behavior.
	t.Run("param-field-both-error", func(t *testing.T) {
		g := &Builder{}
		f := fn(ast.NewFieldAccessExpr(ast.NewIdentExpr("a"), "field"), "a")
		_, _, _, err := g.inlineCodeCall(f, []ast.Expr{ast.NewIdentExpr("x")})
		if err == nil {
			t.Fatalf("inlineCodeCall succeeded, want error (param in field access)")
		}
	})
}

// TestJsonParseParity is the P5.11 migration gate for buildJSONParse: both
// shim shapes (top-level array schema / per-field record extraction) lower
// through the pyast multi-statement path. Two semantic-equivalence
// exemptions are asserted explicitly:
//   - array branch: pyast emits `(data if isinstance(data, list) else [])`
//     (ternary always parenthesized) vs legacy's parenthesless form in
//     dict-value position — identical evaluation;
//   - record branch: pyast sorts dict keys vs legacy's field declaration
//     order — the key SET must match (dictKeySet, P5.10a).
func TestJsonParseParity(t *testing.T) {
	bound := &Builder{
		varToOutput: map[string]varRef{
			"raw": {nodeID: "n1", output: "out", kind: "code", typ: T("string")},
		},
	}
	call := func(typeArgs []ast.TypeRef) *ast.CallExpr {
		return ast.NewCallExpr(ast.NewIdentExpr("json.parse"), typeArgs, []ast.Expr{ast.NewIdentExpr("raw")})
	}
	run := func(g *Builder, c *ast.CallExpr) *CodeRun {
		t.Helper()
		node, err := g.buildJSONParse(g.nextID(), c, nil, "")
		if err != nil {
			t.Fatalf("buildJSONParse error: %v", err)
		}
		cr, ok := node.(*CodeRun)
		if !ok {
			t.Fatalf("node type = %T, want *CodeRun", node)
		}
		return cr
	}

	t.Run("array-branch", func(t *testing.T) {
		cr := run(bound, call([]ast.TypeRef{ast.NewArrayTypeRef(ast.NewPrimTypeRef("string"))}))
		want := "import json\ndef main(arg1) -> dict:\n    data = json.loads(arg1)\n    return {\"output\": (data if isinstance(data, list) else [])}"
		if cr.Code != want {
			t.Errorf("byte drift:\npyast:\n%s\n\nwant:\n%s", cr.Code, want)
		}
		// Semantic equivalence with the legacy parenthesless ternary. The
		// import position differs by design (pyast: module top; legacy:
		// function body) — both are valid, self-sufficient Python — so the
		// comparison normalizes the import line before asserting the parens.
		legacy := "def main(arg1) -> dict:\n    import json\n    data = json.loads(arg1)\n    return {\"output\": data if isinstance(data, list) else []}"
		stripImports := func(c string) string {
			return strings.ReplaceAll(strings.ReplaceAll(c, "    import json\n", ""), "import json\n", "")
		}
		parenStripped := strings.ReplaceAll(cr.Code, "(data if isinstance(data, list) else [])", "data if isinstance(data, list) else []")
		if stripImports(parenStripped) != stripImports(legacy) {
			t.Errorf("ternary parens drifted:\npyast (paren-stripped):\n%s\n\nlegacy:\n%s", parenStripped, legacy)
		}
	})

	t.Run("record-branch-key-sort", func(t *testing.T) {
		g := &Builder{
			types: map[string]*ast.TypeDecl{
				"Doc": ast.NewTypeDecl("Doc", []*ast.FieldDecl{
					ast.NewFieldDecl("b", ast.NewPrimTypeRef("string")),
					ast.NewFieldDecl("a", ast.NewPrimTypeRef("int")),
				}),
			},
			varToOutput: bound.varToOutput,
		}
		cr := run(g, call([]ast.TypeRef{ast.NewNamedTypeRef("Doc")}))
		// pyast path taken (sorted keys) — NOT the legacy field order.
		want := "import json\ndef main(arg1) -> dict:\n    data = json.loads(arg1)\n    return {\"output\": {\"a\": data.get(\"a\", 0), \"b\": data.get(\"b\", \"\")}}"
		if cr.Code != want {
			t.Errorf("byte drift:\npyast:\n%s\n\nwant:\n%s", cr.Code, want)
		}
		legacy := "def main(arg1) -> dict:\n    import json\n    data = json.loads(arg1)\n    return {\"output\": {\"b\": data.get(\"b\", \"\"), \"a\": data.get(\"a\", 0)}}"
		pyastKeys := dictKeySet(cr.Code)
		legacyKeys := dictKeySet(legacy)
		if strings.Join(pyastKeys, ",") != strings.Join(legacyKeys, ",") {
			t.Errorf("key SET drifted:\npyast keys: %v\nlegacy keys: %v", pyastKeys, legacyKeys)
		}
	})

	t.Run("record-defaults", func(t *testing.T) {
		g := &Builder{
			types: map[string]*ast.TypeDecl{
				"Full": ast.NewTypeDecl("Full", []*ast.FieldDecl{
					ast.NewFieldDecl("title", ast.NewPrimTypeRef("string")),
					ast.NewFieldDecl("count", ast.NewPrimTypeRef("int")),
					ast.NewFieldDecl("ok", ast.NewPrimTypeRef("boolean")),
					ast.NewFieldDecl("tags", ast.NewPrimTypeRef("array")),
					ast.NewFieldDecl("misc", ast.NewPrimTypeRef("file")),
				}),
			},
			varToOutput: bound.varToOutput,
		}
		cr := run(g, call([]ast.TypeRef{ast.NewNamedTypeRef("Full")}))
		for _, want := range []string{
			`data.get("title", "")`,
			`data.get("count", 0)`,
			`data.get("ok", False)`,
			`data.get("tags", [])`,
			`data.get("misc", None)`,
		} {
			if !strings.Contains(cr.Code, want) {
				t.Errorf("missing default shape %q in:\n%s", want, cr.Code)
			}
		}
	})
}

// TestJSONParseArrayGuardParity is the P5.12 migration gate for the
// json.parse<array<T>> element-access guard (G12-2). Two shapes lower through
// the pyast path; the guard's synthetic empty `{}` is exempted from the
// hasMapOrList boundary (the legacy renderer itself emits it):
//   - plain index `items[i]`: pyast serializes the ternary WITH parens
//     `(items[i] if len(items) > i else {})` vs legacy's paren-free
//     dict-value form — identical evaluation, L2 exemption (same family as
//     the P5.11 array-branch ternary); asserted paren-stripped.
//   - element field `items[i].field`: byte-identical to legacy
//     (`str((...) .get("f") or "")` — TernaryExpr self-parenthesizes).
//
// Non-top-level nesting (e.g. `items[0].content + x`) keeps falling back to
// the legacy shim (conservative boundary, P4 explicit-error preserved).
func TestJSONParseArrayGuardParity(t *testing.T) {
	guard := &Builder{varToOutput: map[string]varRef{
		"items": {nodeID: "n1", output: "out", kind: "code", jsonParseArray: true},
	}}
	shim := func(e ast.Expr) (string, bool, error) {
		var bindings []textBinding
		return shimCodeFor(e, guard, &bindings)
	}
	legacyShim := func(e ast.Expr) (string, error) {
		var bindings []textBinding
		expr, err := renderConditionPython(e, guard, &bindings)
		if err != nil {
			return "", err
		}
		return pyast.ShimMarker + fmt.Sprintf("def main(%s) -> dict:\n    return {\"result\": %s}",
			strings.Join(shimParamNames(bindings), ", "), expr), nil
	}

	t.Run("plain-index-paren-exempt", func(t *testing.T) {
		e := ast.NewIndexExpr(ast.NewIdentExpr("items"), ast.NewIntLit(0))
		code, ok, err := shim(e)
		if err != nil {
			t.Fatalf("shimCodeFor error: %v", err)
		}
		if !ok {
			t.Fatalf("plain index took fallback (ok=false), want pyast path")
		}
		// pyast ternary is always parenthesized — the L2 exemption.
		wantPyast := pyast.ShimMarker + "def main(out) -> dict:\n    return {\"result\": (out[0] if len(out) > 0 else {})}"
		if code != wantPyast {
			t.Errorf("byte drift:\npyast:\n%s\n\nwant:\n%s", code, wantPyast)
		}
		legacy, lerr := legacyShim(e)
		if lerr != nil {
			t.Fatalf("legacy render error: %v", lerr)
		}
		parenStripped := strings.ReplaceAll(code, "(out[0] if len(out) > 0 else {})", "out[0] if len(out) > 0 else {}")
		if parenStripped != legacy {
			t.Errorf("paren exemption drifted:\npyast (paren-stripped):\n%s\n\nlegacy:\n%s", parenStripped, legacy)
		}
	})

	t.Run("element-field-byte-identical", func(t *testing.T) {
		// i=1：常量 0 + 成员字段已有 dotted 端口形（E5-b），故用 i=1 逼出 shim。
		e := ast.NewFieldAccessExpr(ast.NewIndexExpr(ast.NewIdentExpr("items"), ast.NewIntLit(1)), "content")
		code, ok, err := shim(e)
		if err != nil {
			t.Fatalf("shimCodeFor error: %v", err)
		}
		if !ok {
			t.Fatalf("element field took fallback (ok=false), want pyast path")
		}
		want := pyast.ShimMarker + "def main(out) -> dict:\n    return {\"result\": str((out[1] if len(out) > 1 else {}).get(\"content\") or \"\")}"
		if code != want {
			t.Errorf("byte drift:\npyast:\n%s\n\nwant:\n%s", code, want)
		}
		legacy, lerr := legacyShim(e)
		if lerr != nil {
			t.Fatalf("legacy render error: %v", lerr)
		}
		if code != legacy {
			t.Errorf("not byte-identical to legacy:\npyast:\n%s\n\nlegacy:\n%s", code, legacy)
		}
	})

	t.Run("nested-keeps-fallback", func(t *testing.T) {
		// `items[1].content + "x"`: guard nested under a binary — the strict
		// hasMapOrList boundary still rejects it (conservative). i=1 同上。
		e := ast.NewBinaryExpr("+",
			ast.NewFieldAccessExpr(ast.NewIndexExpr(ast.NewIdentExpr("items"), ast.NewIntLit(1)), "content"),
			ast.NewStringLit("x"))
		code, ok, err := shim(e)
		if err != nil {
			t.Fatalf("shimCodeFor error: %v", err)
		}
		if ok {
			t.Fatalf("nested guard took pyast path, want fallback; pyast:\n%s", code)
		}
	})
}

// TestBuildMapParity is the P5.13 migration gate for buildMap (G8 record
// shim): object literals take the pyast path when every key is quote-safe
// and every value lowers byte-identically. Ref values bind by argN (legacy
// uses argN for every ref — no ident-name special case). Multi-key dicts
// serialize with SORTED keys vs legacy insertion order — the P5.10a key-SET
// exemption; quote-unsafe keys fall back to the legacy json.Marshal bytes.
func TestBuildMapParity(t *testing.T) {
	// Fixtures must carry the producer's output contract: refType resolves
	// authoritative types only (TypePass / varRef.typ / port registry) and
	// errors instead of forging string — a hand-built IR without a typed
	// program has to declare its own contract.
	bound := &Builder{varToOutput: map[string]varRef{
		"x":       {nodeID: "n1", output: "out", kind: "code", typ: T("string")},
		"y":       {nodeID: "n1", output: "out", kind: "code", typ: T("string")},
		"payload": {nodeID: "n1", output: "out", kind: "code", typ: T("string")},
	}}
	legacy := func(params, kv []string) string {
		return ShimMarker + fmt.Sprintf("def main(%s) -> dict:\n    return {%s}",
			strings.Join(params, ", "), strings.Join(kv, ", "))
	}
	mk := func(kv ...ast.MapEntry) *ast.MapLit { return ast.NewMapLit(kv) }
	cases := []struct {
		name     string
		g        *Builder
		m        *ast.MapLit
		want     string
		wantKeys []string // key set assertion for the exemption cases
	}{
		{
			name: "single-key-ref",
			g:    bound,
			m:    mk(ast.MapEntry{Key: ast.NewStringLit("g1"), Value: ast.NewIdentExpr("x")}),
			want: legacy([]string{"arg1"}, []string{`"g1": arg1`}),
		},
		{
			name: "single-key-literal",
			g:    &Builder{},
			m:    mk(ast.MapEntry{Key: ast.NewStringLit("k"), Value: ast.NewStringLit("v")}),
			want: legacy(nil, []string{`"k": "v"`}),
		},
		{
			name: "mixed-ref-literal-sorted-equal",
			g:    bound,
			m: mk(ast.MapEntry{Key: ast.NewStringLit("a"), Value: ast.NewIdentExpr("x")},
				ast.MapEntry{Key: ast.NewStringLit("b"), Value: ast.NewStringLit("v")}),
			want: legacy([]string{"arg1"}, []string{`"a": arg1`, `"b": "v"`}),
		},
		{
			// 键序 = 表示（2026-09-18 族 2 收口）：位置身份（`argN`）按**规范序**
			// （自然序）分配 ⇒ 两个渲染路径在**这一形状上不再漂移**（旧注释的
			// P5.10a 豁免在此消失）。撤实现 ⇒ 本格 RED（两路径按源文本序分配 ⇒
			// pyast 体序 ≠ 绑定序）。
			name: "multi-key-canonical-order",
			g:    bound,
			m: mk(ast.MapEntry{Key: ast.NewStringLit("b"), Value: ast.NewIdentExpr("x")},
				ast.MapEntry{Key: ast.NewStringLit("a"), Value: ast.NewIdentExpr("y")}),
			// 规范序 = (a, b)：值随键走（"a"→y=arg1、"b"→x=arg2）。
			want:     legacy([]string{"arg1", "arg2"}, []string{`"a": arg1`, `"b": arg2`}),
			wantKeys: []string{"a", "b"},
		},
		{
			// 豁免仍在（P5.10a）：dict 体由序列化器按**字典序**渲染，而位置身份
			// 按**自然序**分配 ⇒ 含数字的键集上两路径的**字节**仍不同（键集与配对
			// 不变；两侧都是规范序 = 源文本序的函数已消失，逐轮稳定）。
			name: "multi-key-natural-vs-lexicographic",
			g:    bound,
			m: mk(ast.MapEntry{Key: ast.NewStringLit("a10"), Value: ast.NewIdentExpr("x")},
				ast.MapEntry{Key: ast.NewStringLit("a2"), Value: ast.NewIdentExpr("y")}),
			// 位置身份按自然序 = (a2, a10) ⇒ a2←y=arg1、a10←x=arg2；而 pyast 的
			// **体序**是字典序（a10 在前）—— 这一处字节差异就是仍在的豁免本身。
			want:     legacy([]string{"arg1", "arg2"}, []string{`"a10": arg2`, `"a2": arg1`}),
			wantKeys: []string{"a10", "a2"},
		},
		{
			name: "quote-unsafe-key-fallback",
			g:    bound,
			m:    mk(ast.MapEntry{Key: ast.NewStringLit(">"), Value: ast.NewIdentExpr("x")}),
			want: legacy([]string{"arg1"}, []string{`"\u003e": arg1`}),
		},
		{
			name: "nested-map-value",
			g:    bound,
			m:    mk(ast.MapEntry{Key: ast.NewStringLit("fields"), Value: ast.NewMapLit([]ast.MapEntry{{Key: ast.NewStringLit("a"), Value: ast.NewIdentExpr("payload")}})}),
			// Nested-map leaf binds by its ident name (payload), not argN —
			// the legacy non-ref branch does the same (bindConditionPython).
			want: legacy([]string{"out"}, []string{`"fields": {"a": out}`}),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := c.g
			id := g.nextID()
			node, err := g.buildMap(id, c.m)
			if err != nil {
				t.Fatalf("buildMap error: %v", err)
			}
			cr, ok := node.(*CodeRun)
			if !ok {
				t.Fatalf("node type = %T, want *CodeRun", node)
			}
			if cr.Code != c.want {
				t.Errorf("byte drift:\npyast:\n%s\n\nlegacy:\n%s", cr.Code, c.want)
			}
			if c.wantKeys != nil {
				got := dictKeySet(cr.Code)
				if strings.Join(got, ",") != strings.Join(c.wantKeys, ",") {
					t.Errorf("key SET drifted:\npyast keys: %v\nwant: %v", got, c.wantKeys)
				}
			}
		})
	}
}

// TestCalcBuiltinComplexArgParity locks the P5.2 remaining shapes: when a
// calc-builtin argument is a complex scalar (binary/unary/ternary/template/
// subscript) or a nested calc-builtin call, the pyast path must stay
// byte-identical to the legacy string path (renderLiteralPython arg + the
// shared pygen.Template). These used to fall back to string templates; the
// lowerLiteralPyast delegation to the condition mirror made them pyast-first.
func TestCalcBuiltinComplexArgParity(t *testing.T) {
	bound := &Builder{varToOutput: map[string]varRef{
		"x":     {nodeID: "n1", output: "out", kind: "code"},
		"y":     {nodeID: "n1", output: "out", kind: "code"},
		"text":  {nodeID: "n1", output: "out", kind: "code"},
		"items": {nodeID: "n1", output: "out", kind: "code"},
		"count": {nodeID: "n1", output: "out", kind: "code"},
	}}
	ident := func(n string) ast.Expr { return ast.NewIdentExpr(n) }
	call := func(builtin string, args ...ast.Expr) *ast.CallExpr {
		return ast.NewCallExpr(ast.NewIdentExpr(builtin), nil, args)
	}
	cases := []struct {
		name    string
		builtin string
		args    []ast.Expr
	}{
		{"binary-arg", "to_string", []ast.Expr{ast.NewBinaryExpr("+", ident("count"), ast.NewIntLit(1))}},
		{"ternary-arg", "to_string", []ast.Expr{ast.NewTernaryExpr(
			ast.NewBinaryExpr(">", ident("count"), ast.NewIntLit(0)), ident("count"), ast.NewIntLit(0))}},
		{"unary-minus-arg", "to_string", []ast.Expr{ast.NewUnaryExpr("-", ident("count"))}},
		{"template-arg", "contains", []ast.Expr{ast.NewTemplateString([]ast.TemplatePart{
			{Text: "前缀"}, {Expr: ident("text")}, {Text: "后缀"}}), ast.NewStringLit("abc")}},
		{"index-arg", "to_string", []ast.Expr{ast.NewIndexExpr(ident("items"), ast.NewIntLit(0))}},
		{"nested-calc", "len", []ast.Expr{call("split", ident("text"), ast.NewStringLit(","))}},
		{"nested-join-split", "join", []ast.Expr{call("split", ident("text"), ast.NewStringLit(",")), ast.NewStringLit("-")}},
		{"or-merge-arg", "len", []ast.Expr{ast.NewBinaryExpr("||", ident("count"), ast.NewIntLit(0))}},
		{"field-arg", "len", []ast.Expr{ast.NewFieldAccessExpr(ident("text"), "length")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			full := call(c.builtin, c.args...)
			var pb, lb []textBinding

			pa, ok, err := lowerCalcBuiltin(full, bound, &pb)
			if err != nil {
				t.Fatalf("lowerCalcBuiltin error: %v", err)
			}
			if !ok {
				t.Fatal("lowerCalcBuiltin = ok=false, want pyast path")
			}
			pyastCode := pyast.SerializeExpr(pa)

			// legacy string path: renderLiteralPython per arg + shared template.
			strArgs := make([]string, 0, len(c.args))
			for _, a := range c.args {
				rendered, rerr := renderLiteralPython(a, bound, &lb)
				if rerr != nil {
					t.Fatalf("renderLiteralPython error: %v", rerr)
				}
				strArgs = append(strArgs, rendered)
			}
			legacy, tok := pygen.Template(c.builtin, strArgs)
			if !tok {
				t.Fatalf("pygen.Template(%q) not lowerable", c.builtin)
			}
			if pyastCode != legacy {
				t.Errorf("byte drift:\npyast:  %s\nlegacy: %s", pyastCode, legacy)
			}
		})
	}
}
