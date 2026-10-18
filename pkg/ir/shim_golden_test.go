package ir

// P5.4 golden snapshot (2026-08-20): every compiler-generated Python shim
// shape is frozen as a byte snapshot under testdata/golden/*.py. Unlike the
// CPython execution gate (exec_shim_test.go) and the inline byte-equivalence
// assertions (pygen_test.go), this is a pure byte-diff against a checked-in
// file: any serializer/lowering drift is visible as a git diff immediately,
// without requiring python3 on the runner. Regenerate with
//
//	go test ./internal/ir/ -run TestShimGoldenSnapshots -update
//
// The golden files are the contract: a change that intentionally alters
// generated shim bytes must update them in the same commit (reviewable).

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/pyast"
	"github.com/nodecoda/ncoda/pkg/pygen"
)

var update = flag.Bool("update", false, "rewrite golden shim snapshot files")

// shimGoldenCase builds one full shim module (def main + body) through the
// production lowering paths.
type shimGoldenCase struct {
	name string
	code func() string
}

// goldenName builds a NameExpr from a bare identifier (pygen_test.go has a
// package-local helper of the same shape; this test lives in package ir).
func goldenName(s string) pyast.Expr {
	return &pyast.NameExpr{Name: s}
}

func shimGoldenCases() []shimGoldenCase {
	// Fixture carries the producer's output contract (refType refuses to
	// forge types — see pyast_lower_test.go TestBuildMapParity note).
	bound := &Builder{varToOutput: map[string]varRef{
		"raw": {nodeID: "n1", output: "out", kind: "code", typ: T("string")},
	}}
	guard := &Builder{varToOutput: map[string]varRef{
		"items": {nodeID: "n1", output: "out", kind: "code", jsonParseArray: true},
	}}

	// 16 calc builtins through LowerBuiltin + wrapShim (single-result body).
	builtinArgs := map[string][]pyast.Expr{
		"len":             {goldenName("x")},
		"trim":            {goldenName("s")},
		"join":            {goldenName("items"), goldenName("sep")},
		"to_string":       {goldenName("x")},
		"int":             {goldenName("x")},
		"float":           {goldenName("x")},
		"replace":         {goldenName("s"), goldenName("old"), goldenName("new")},
		"split":           {goldenName("s"), goldenName("delim")},
		"repeat":          {goldenName("v"), goldenName("n")},
		"div":             {goldenName("a"), goldenName("b")},
		"concat":          {goldenName("a"), goldenName("b")},
		"contains":        {goldenName("xs"), goldenName("x")},
		"range":           {goldenName("a"), goldenName("b")},
		"enumerate":       {goldenName("xs")},
		"flatten":         {goldenName("xs")},
		"first_non_empty": {goldenName("xs")},
	}
	var cases []shimGoldenCase
	for builtin, args := range builtinArgs {
		builtin := builtin
		args := args
		cases = append(cases, shimGoldenCase{
			name: "builtin-" + builtin,
			code: func() string {
				pa, ok := pygen.LowerBuiltin(builtin, args)
				if !ok {
					return "// LowerBuiltin returned false\n"
				}
				params := make([]pyast.Param, 0, len(args))
				for _, a := range args {
					if n, ok := a.(*pyast.NameExpr); ok {
						params = append(params, pyast.Param{Name: n.Name})
					}
				}
				return wrapShim(params, pa) + "\n"
			},
		})
	}

	// json.parse record (key-sorted fields) — full module with import.
	cases = append(cases, shimGoldenCase{
		name: "json-parse-record",
		code: func() string {
			g := &Builder{
				types: map[string]*ast.TypeDecl{
					"Doc": ast.NewTypeDecl("Doc", []*ast.FieldDecl{
						ast.NewFieldDecl("b", ast.NewPrimTypeRef("string")),
						ast.NewFieldDecl("a", ast.NewPrimTypeRef("int")),
					}),
				},
				varToOutput: bound.varToOutput,
			}
			call := ast.NewCallExpr(ast.NewIdentExpr("json.parse"), []ast.TypeRef{ast.NewNamedTypeRef("Doc")}, []ast.Expr{ast.NewIdentExpr("raw")})
			node, err := g.buildJSONParse(g.nextID(), call, nil, "")
			if err != nil {
				return "// buildJSONParse error: " + err.Error() + "\n"
			}
			return node.(*CodeRun).Code + "\n"
		},
	})

	// json.parse array (guard ternary) — full module with import.
	cases = append(cases, shimGoldenCase{
		name: "json-parse-array",
		code: func() string {
			call := ast.NewCallExpr(ast.NewIdentExpr("json.parse"), []ast.TypeRef{ast.NewArrayTypeRef(ast.NewPrimTypeRef("string"))}, []ast.Expr{ast.NewIdentExpr("raw")})
			node, err := bound.buildJSONParse(bound.nextID(), call, nil, "")
			if err != nil {
				return "// buildJSONParse error: " + err.Error() + "\n"
			}
			return node.(*CodeRun).Code + "\n"
		},
	})

	// buildMap record shim.
	cases = append(cases, shimGoldenCase{
		name: "build-map-record",
		code: func() string {
			m := ast.NewMapLit([]ast.MapEntry{
				{Key: ast.NewStringLit("title"), Value: ast.NewIdentExpr("raw")},
				{Key: ast.NewStringLit("ok"), Value: ast.NewStringLit("true")},
			})
			node, err := bound.buildMap(bound.nextID(), m)
			if err != nil {
				return "// buildMap error: " + err.Error() + "\n"
			}
			return node.(*CodeRun).Code + "\n"
		},
	})

	// json.stringify.
	cases = append(cases, shimGoldenCase{
		name: "json-stringify",
		code: func() string {
			call := ast.NewCallExpr(ast.NewIdentExpr("json.stringify"), nil, []ast.Expr{ast.NewIdentExpr("raw")})
			node, err := bound.buildStringify(bound.nextID(), call, nil, "")
			if err != nil {
				return "// buildStringify error: " + err.Error() + "\n"
			}
			return node.(*CodeRun).Code + "\n"
		},
	})

	// condition shim (binary ≥ with parens).
	cases = append(cases, shimGoldenCase{
		name: "condition-binary",
		code: func() string {
			var bindings []textBinding
			e := ast.NewBinaryExpr(">=", ast.NewIdentExpr("score"), ast.NewIntLit(60))
			code, ok, err := shimCodeFor(e, &Builder{varToOutput: map[string]varRef{"score": {nodeID: "n1", output: "out", kind: "code"}}}, &bindings)
			if err != nil || !ok {
				return "// shimCodeFor not lowered\n"
			}
			return code + "\n"
		},
	})

	// condition shim (ternary).
	cases = append(cases, shimGoldenCase{
		name: "condition-ternary",
		code: func() string {
			var bindings []textBinding
			e := ast.NewTernaryExpr(
				ast.NewBinaryExpr(">=", ast.NewIdentExpr("score"), ast.NewIntLit(90)),
				ast.NewStringLit("A"),
				ast.NewStringLit("B"),
			)
			code, ok, err := shimCodeFor(e, &Builder{varToOutput: map[string]varRef{"score": {nodeID: "n1", output: "out", kind: "code"}}}, &bindings)
			if err != nil || !ok {
				return "// shimCodeFor not lowered\n"
			}
			return code + "\n"
		},
	})

	// json-parse-array element field (guarded extraction) —— i=1：常量 0 + 成员
	// 字段已走 dotted 端口形（E5-b），旧夹具会变成空锚（不产 shim）⇒ 用 i=1。
	cases = append(cases, shimGoldenCase{
		name: "json-parse-array-index",
		code: func() string {
			var bindings []textBinding
			e := ast.NewFieldAccessExpr(ast.NewIndexExpr(ast.NewIdentExpr("items"), ast.NewIntLit(1)), "content")
			code, ok, err := shimCodeFor(e, guard, &bindings)
			if err != nil || !ok {
				return "// shimCodeFor not lowered\n"
			}
			return code + "\n"
		},
	})

	return cases
}

func TestShimGoldenSnapshots(t *testing.T) {
	dir := filepath.Join("testdata", "golden")
	if *update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	var checked int
	for _, c := range shimGoldenCases() {
		t.Run(c.name, func(t *testing.T) {
			got := c.code()
			path := filepath.Join(dir, c.name+".py")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatalf("write %s: %v", path, err)
				}
				t.Logf("updated %s", path)
				return
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s (run with -update to create): %v", path, err)
			}
			if string(data) != got {
				t.Errorf("shim byte drift for %s:\n--- golden ---\n%s\n--- current ---\n%s", c.name, data, got)
			}
			checked++
		})
	}
	if !*update && checked == 0 {
		t.Fatal("no golden snapshots checked")
	}
}
