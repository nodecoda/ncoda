// Package ast_test — 静态 map 键规则（`ast.StaticMapKey`，D-2，2026-09-14）。
//
// 规则的**唯一实现**就在这里；前端两个 pass（`passes.staticMapKey` /
// `TypePass.staticMapKey`）与 IR 建图（`ir.staticMapKeyName`）都委托它。
// 之所以单独立测：本族缺陷的形态就是「同一条规则被抄成三份、其中两份只认
// 字符串字面量」——抄本会各自漂移，规则函数的行为才是契约。
package ast_test

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
)

func TestStaticMapKey(t *testing.T) {
	// 键是**标签**不是引用（`binder` 从不把键当变量解析）；空串键是**静态**键
	// `""`——这正是旧实现用 `name != ""` 当动态判据而静默丢键的地方。
	cases := []struct {
		name    string
		key     ast.Expr
		want    string
		wantOK  bool
		comment string
	}{
		{name: "identifier_is_a_label", key: ast.NewIdentExpr("lyric"), want: "lyric", wantOK: true},
		{name: "string_literal_value", key: ast.NewStringLit("lyric"), want: "lyric", wantOK: true},
		{name: "empty_string_is_static", key: ast.NewStringLit(""), want: "", wantOK: true},
		{name: "binary_expr_is_dynamic", key: ast.NewBinaryExpr("+", ast.NewStringLit("a"), ast.NewIdentExpr("q")), wantOK: false},
		{name: "template_string_is_dynamic", key: ast.NewTemplateString(nil), wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ast.StaticMapKey(tc.key)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("StaticMapKey(%T) = (%q, %v), want (%q, %v)", tc.key, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
