package ir

//@feature field-index — E5-b 记录数组成员链进 code shim 的 dotted 端口绑定
//（2026-09-03 INDEX_TYPE×4 回归：data[0].bgm_url 不得降成 listVar["bgm_url"]）

// E5-b item-0 元素字段访问（v.data[0].bgm_url）在 code shim 值位置必须整链
// 绑定平台 dotted 端口（data.bgm_url → python 参数直引用），字节与纯成员链
// 一致（v114206.data.text → v114206_data_text）。旧实现把元素片段（data[0]）
// 解析到整数组端口后再把尾随字段做成数组字符串下标（v_data_0["bgm_url"]），
// [0] 丢失 → round2 反成 list["str"]，L4 再编译 INDEX_TYPE。两条 shim 路径
// （legacy renderFieldAccessPython + pyast lowerFieldAccessPyast 镜像）必须
// 同时修正且字节一致。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/pyast"
)

// e5bChainBuilder 构造 v188660 为 code 输出（record-array 成员链字段 data）。
func e5bChainBuilder() *Builder {
	return &Builder{varToOutput: map[string]varRef{
		"v188660": {nodeID: "188660", output: "output", kind: "code", typ: T("record")},
	}}
}

// e5bChain 构造 v188660.data[0].bgm_url（E5-b 常量 0 元素字段访问）。
func e5bChain() *ast.FieldAccessExpr {
	return ast.NewFieldAccessExpr(
		ast.NewIndexExpr(
			ast.NewFieldAccessExpr(ast.NewIdentExpr("v188660"), "data"),
			ast.NewIntLit(0),
		),
		"bgm_url",
	)
}

// TestE5bChainShimBindsDottedPort 锁定前向降级：整链 dotted 端口绑定，
// 参数名 data_bgm_url，ref 端口 data.bgm_url——绝不绑定整数组
// （v188660_data_0 + 字符串下标）。
func TestE5bChainShimBindsDottedPort(t *testing.T) {
	g := e5bChainBuilder()
	var bindings []textBinding
	rendered, err := renderFieldAccessPython(e5bChain(), g, &bindings)
	if err != nil {
		t.Fatalf("renderFieldAccessPython: %v", err)
	}
	if rendered != "data_bgm_url" {
		t.Errorf("legacy rendered = %q, want 整链 dotted 绑定 data_bgm_url（无 [0] 丢失/无字符串下标）", rendered)
	}
	if len(bindings) != 1 {
		t.Fatalf("bindings = %d, want 1", len(bindings))
	}
	b := bindings[0]
	if b.name != "data_bgm_url" {
		t.Errorf("binding name = %q, want data_bgm_url", b.name)
	}
	if b.ref.nodeID != "188660" || b.ref.output != "data.bgm_url" {
		t.Errorf("binding ref = %s:%s, want 188660:data.bgm_url（dotted item-0 端口，非整数组 data）",
			b.ref.nodeID, b.ref.output)
	}

	// pyast 镜像路径字节一致。
	pa, ok, err := lowerFieldAccessPyast(e5bChain(), e5bChainBuilder(), &bindings)
	if err != nil || !ok {
		t.Fatalf("lowerFieldAccessPyast ok=%v err=%v", ok, err)
	}
	if name, isName := pa.(*pyast.NameExpr); !isName || name.Name != "data_bgm_url" {
		t.Errorf("pyast = %#v, want NameExpr(data_bgm_url)", pa)
	}
	if got := pyast.SerializeExpr(pa); got != rendered {
		t.Errorf("pyast bytes = %q, want 与 legacy 一致 %q", got, rendered)
	}

	// 全 shim（merge 边界 record 形态）不允许出现数组字符串下标。
	var sb []textBinding
	code, ok2, err2 := shimCodeFor(e5bChain(), e5bChainBuilder(), &sb)
	if err2 != nil || !ok2 {
		t.Fatalf("shimCodeFor ok=%v err=%v", ok2, err2)
	}
	if strings.Contains(code, `["bgm_url"]`) || strings.Contains(code, "data_0") {
		t.Errorf("shim 不得出现数组字符串下标 / 元素片段绑定：\n%s", code)
	}
	if !strings.Contains(code, "def main(data_bgm_url)") {
		t.Errorf("shim 应以整链 dotted 参数直引用：\n%s", code)
	}
}
