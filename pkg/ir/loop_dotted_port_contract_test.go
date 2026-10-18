package ir

import "testing"

// 命名多口 loop 的 yield 口是**列表**（每次迭代一个元素），而平台引用把
// 常量 0 的元素下标省掉：`agg.otherInfos_list.line_spacing` 实际是
// `agg.otherInfos_list[0].line_spacing`（E5-b 同款规则）。整口名之外的
// 点路径必须穿过元素类型求字段类型 —— 求不出就返回 nil（不伪造）。
//
// 回归：语料 7490978684577693748（xlx_douyin）r1 编译曾因
// `100042.otherInfos_list.line_spacing` 解析不出类型而失败。
func TestLoopNamedPortDottedFieldResolvesThroughElement(t *testing.T) {
	rec := TObject(map[string]*Type{
		"line_spacing": T("number"),
		"font_size":    T("integer"),
	})
	loop := &Loop{ID: "100042", VarName: "texts"}
	loop.OutTypes = map[string]*Type{
		"otherInfos_list": TList(rec),
		"infos_list":      TList(T("string")),
	}

	if got := portTypeOfNode(loop, "otherInfos_list"); got == nil || got.Kind != "list" {
		t.Fatalf("whole port = %v, want list", got)
	}
	if got := portTypeOfNode(loop, "otherInfos_list.line_spacing"); got == nil || got.Kind != "number" {
		t.Fatalf("dotted port = %v, want number (element field)", got)
	}
	if got := portTypeOfNode(loop, "otherInfos_list.nope"); got != nil {
		t.Fatalf("unknown field = %v, want nil (no forged type)", got)
	}
	// 非列表口上的点路径不按元素规则处理（保持 fail loud）。
	if got := portTypeOfNode(loop, "returned.x"); got != nil {
		t.Fatalf("non-list port dotted = %v, want nil", got)
	}
}
