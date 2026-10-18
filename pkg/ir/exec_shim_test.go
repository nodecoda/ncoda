// 语言特性矩阵锚点声明（docs/LANGUAGE-FEATURE-MATRIX.md）——IR 层特性锁定位：
//@feature field-index — 字段访问/索引 ref 值解析（TestResolveValueRefStringIndexMember + parser TestExprFieldAccess）

package ir

// Runtime-execution gate for compiler-generated Python shims (test
// governance, 2026-08-20). The P5.12 import self-sufficiency fix was
// triggered by a blind spot no static gate could catch: Dify sandboxes make
// stdlib modules importable but do NOT auto-bind names, so a shim that calls
// json.loads without `import json` is a runtime NameError — while every
// acceptance gate (coze-probe / flow-probe / dify-verify) and even
// py_compile (compile(), no exec()) passes. This test locks the runtime
// semantics of every generated shim shape by actually executing it under
// CPython, asserting the output (including out-of-bounds / missing-field
// fallbacks). Environment-guarded like TestAllLoweringsCompile.

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
)

// execShim runs a generated shim module's main(args) under python3 and
// asserts the result equals want (JSON round-trip, order-insensitive).
func execShim(t *testing.T, name, code, args string, want interface{}) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	wantJSON, _ := json.Marshal(want)
	// wantJSON is JSON (true/false); embed it as a string and parse it, so
	// the Python source never contains JSON-only literals.
	script := fmt.Sprintf(`import json as _json
%s
_result = main(%s)
_want = _json.loads(%q)
assert _result == _want, (_result, _want)
`, code, args, wantJSON)
	cmd := exec.Command("python3", "-c", script)
	cmd.Env = append(cmd.Environ(), "PYTHONIOENCODING=utf-8")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: python exec failed:\n%s\nshim:\n%s", name, out, code)
	}
}

// TestGeneratedShimsExecute executes every generated shim shape under real
// CPython. Sources come from the actual lowering paths (buildJSONParse /
// buildMap / shimCodeFor), so a serializer change that breaks runtime
// semantics fails here in lockstep with the byte-parity tests.
func TestGeneratedShimsExecute(t *testing.T) {
	bound := &Builder{varToOutput: map[string]varRef{
		"raw": {nodeID: "n1", output: "out", kind: "code", typ: T("string")},
	}}
	guard := &Builder{varToOutput: map[string]varRef{
		"items": {nodeID: "n1", output: "out", kind: "code", jsonParseArray: true, typ: T("string")},
	}}

	t.Run("json-parse-record", func(t *testing.T) {
		g := &Builder{
			types: map[string]*ast.TypeDecl{
				"Doc": ast.NewTypeDecl("Doc", []*ast.FieldDecl{
					ast.NewFieldDecl("title", ast.NewPrimTypeRef("string")),
					ast.NewFieldDecl("count", ast.NewPrimTypeRef("int")),
					ast.NewFieldDecl("ok", ast.NewPrimTypeRef("boolean")),
				}),
			},
			varToOutput: bound.varToOutput,
		}
		call := ast.NewCallExpr(ast.NewIdentExpr("json.parse"), []ast.TypeRef{ast.NewNamedTypeRef("Doc")}, []ast.Expr{ast.NewIdentExpr("raw")})
		node, err := g.buildJSONParse(g.nextID(), call, nil, "")
		if err != nil {
			t.Fatalf("buildJSONParse: %v", err)
		}
		code := node.(*CodeRun).Code
		// 记录形态的 shim 把整个解析结果挂在唯一真实口 `output` 之下
		// （coze-biz 原生 json_deserialization 节点的形状；见 build_node.go
		// 与 lowerJsonParseShim 的语料依据）⇒ 运行期返回 dict 的键是 output。
		execShim(t, "record full", code, `'{"title":"hi","count":3,"ok":true}'`,
			map[string]interface{}{"output": map[string]interface{}{"count": 3, "ok": true, "title": "hi"}})
		execShim(t, "record defaults", code, `'{"title":"hi"}'`,
			map[string]interface{}{"output": map[string]interface{}{"count": 0, "ok": false, "title": "hi"}})
	})

	t.Run("json-parse-array", func(t *testing.T) {
		call := ast.NewCallExpr(ast.NewIdentExpr("json.parse"), []ast.TypeRef{ast.NewArrayTypeRef(ast.NewPrimTypeRef("string"))}, []ast.Expr{ast.NewIdentExpr("raw")})
		node, err := bound.buildJSONParse(bound.nextID(), call, nil, "")
		if err != nil {
			t.Fatalf("buildJSONParse: %v", err)
		}
		code := node.(*CodeRun).Code
		execShim(t, "array full", code, `'["a","b"]'`, map[string]interface{}{"output": []string{"a", "b"}})
		execShim(t, "array non-list guard", code, `'"x"'`, map[string]interface{}{"output": []string{}})
	})

	t.Run("json-parse-array-index-guard", func(t *testing.T) {
		var bindings []textBinding
		code, ok, err := shimCodeFor(ast.NewIndexExpr(ast.NewIdentExpr("items"), ast.NewIntLit(0)), guard, &bindings)
		if err != nil || !ok {
			t.Fatalf("plain index did not lower (ok=%v err=%v)", ok, err)
		}
		execShim(t, "index oob", code, `[]`, map[string]interface{}{"result": map[string]interface{}{}})
		execShim(t, "index hit", code, `[{"a":1}]`, map[string]interface{}{"result": map[string]interface{}{"a": 1}})
	})

	// 索引 1（非常量 0）：only 非常量 0 / 根索引才需要守卫 shim —— 常量 0 + 成员
	// 字段有 dotted 端口形（E5-b，2026-09-26 收口 json.parse 基段），故本夹具用
	// i=1 才能逼出 shim 生成器（判据对象 = 守卫语义本身，不是索引值）。
	t.Run("json-parse-array-element-field", func(t *testing.T) {
		var bindings []textBinding
		e := ast.NewFieldAccessExpr(ast.NewIndexExpr(ast.NewIdentExpr("items"), ast.NewIntLit(1)), "content")
		code, ok, err := shimCodeFor(e, guard, &bindings)
		if err != nil || !ok {
			t.Fatalf("element field did not lower (ok=%v err=%v)", ok, err)
		}
		execShim(t, "field oob", code, `[]`, map[string]interface{}{"result": ""})
		execShim(t, "field hit", code, `[{"content":"z"},{"content":"a"}]`, map[string]interface{}{"result": "a"})
		execShim(t, "field missing", code, `[{"content":"z"},{"title":"x"}]`, map[string]interface{}{"result": ""})
	})

	t.Run("build-map-record", func(t *testing.T) {
		m := ast.NewMapLit([]ast.MapEntry{
			{Key: ast.NewStringLit("title"), Value: ast.NewIdentExpr("raw")},
			{Key: ast.NewStringLit("ok"), Value: ast.NewStringLit("true")},
		})
		node, err := bound.buildMap(bound.nextID(), m)
		if err != nil {
			t.Fatalf("buildMap: %v", err)
		}
		execShim(t, "record", node.(*CodeRun).Code, `"T1"`, map[string]interface{}{"title": "T1", "ok": "true"})
	})

	t.Run("json-stringify", func(t *testing.T) {
		call := ast.NewCallExpr(ast.NewIdentExpr("json.stringify"), nil, []ast.Expr{ast.NewIdentExpr("raw")})
		node, err := bound.buildStringify(bound.nextID(), call, nil, "")
		if err != nil {
			t.Fatalf("buildStringify: %v", err)
		}
		execShim(t, "stringify", node.(*CodeRun).Code, `"hi"`, map[string]interface{}{"result": `"hi"`})
	})

	t.Run("condition-shim", func(t *testing.T) {
		var bindings []textBinding
		e := ast.NewBinaryExpr(">=", ast.NewIdentExpr("score"), ast.NewIntLit(60))
		code, ok, err := shimCodeFor(e, &Builder{varToOutput: map[string]varRef{"score": {nodeID: "n1", output: "out", kind: "code", typ: T("string")}}}, &bindings)
		if err != nil || !ok {
			t.Fatalf("condition did not lower (ok=%v err=%v)", ok, err)
		}
		execShim(t, "cond true", code, `70`, map[string]interface{}{"result": true})
		execShim(t, "cond false", code, `50`, map[string]interface{}{"result": false})
	})
}

func TestResolveValueRefStringIndexMember(t *testing.T) {
	// 反编译器对关键字字段（code/msg/output 等）渲染 obj["field"]；
	// resolveValueRef 必须把字符串下标等价为成员字段段（与 .field 相同），
	// 否则关键字字段访问退化成 Python shim，condition 失去原生 Selector 形态。
	// resolveValueRef 是 Builder 方法（成员链首段要查产出方声明的端口模型）；
	// 本用例只用绑定表，节点不在图里 ⇒ 查图得到「无证据」⇒ 走既有字段名即口名。
	g := &Builder{varToOutput: map[string]varRef{
		"v160311": {nodeID: "160311", output: "root", kind: "code"},
	}}
	expr := ast.NewIndexExpr(ast.NewIdentExpr("v160311"), ast.NewStringLit("code"))
	ref, ok := g.resolveValueRef(expr)
	if !ok {
		t.Fatal("v[\"code\"] 字符串下标应解析为成员字段访问")
	}
	if ref.nodeID != "160311" || ref.output != "code" {
		t.Errorf("ref = %s:%s, want 160311:code (kind code 字段名即端口)", ref.nodeID, ref.output)
	}
	// 混合链：v["code"].value → 同字段访问路径（code 为字段，value 为后续段）。
	chain := ast.NewFieldAccessExpr(ast.NewIndexExpr(ast.NewIdentExpr("v160311"),
		ast.NewStringLit("code")), "value")
	ref2, ok := g.resolveValueRef(chain)
	if !ok {
		t.Fatal("v[\"code\"].value 链应解析")
	}
	if ref2.output != "code.value" {
		t.Errorf("ref2 = %s:%s, want 160311:code.value", ref2.nodeID, ref2.output)
	}
}
