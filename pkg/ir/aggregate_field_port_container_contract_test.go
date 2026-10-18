package ir

import "testing"

// 逐字段 phi 候选的**基段判据**（2026-09-10，S119×2 首编失败 + 反例 E）。
//
// 规则：基段 `ref.output + "." + field` 只在「生产者把 ref.output 声明为
// **record 型（object）输出端口**」时保留——即这个 record 的字段活在该容器
// 之下。判据**只读声明类型**，绝不按「某个端口恰好叫这个叶子名」匹配。
//
// 两个反方向的实证：
//   - S119×2：多口 foreign code（一字段一端口，`image: array<string>`）的
//     ref.output 是 firstOutputPort 启发式的首声明端口 ⇒ 按名字匹配会合成
//     `image.image`（错，反编译还原成 v.image[0].image）⇒ 必须裸字段。
//   - 反例 E：生产者同时声明 `text: RT2` 与叶子 `wenan: string`，绑定在
//     `text` 上的 record 的字段仍是 `text.wenan`；按名字匹配会误选同名兄弟
//     叶子口 `wenan`（读到另一个值）⇒ 必须看声明类型。
func TestAggregateFieldPortBaseKeptOnlyForRecordTypedPort(t *testing.T) {
	rt2 := TObject(map[string]*Type{"wenan": T("string")})
	cases := []struct {
		name  string
		ports map[string]*Type
		base  string
		field string
		want  string
	}{
		{
			name:  "flat multiport leaf is its own port (S119)",
			ports: map[string]*Type{"image": TList(T("string"))},
			base:  "image", field: "image", want: "image",
		},
		{
			name:  "record-typed member port keeps the base (shim/llm payload)",
			ports: map[string]*Type{"text": rt2},
			base:  "text", field: "wenan", want: "text.wenan",
		},
		{
			name:  "record member wins over a same-named sibling leaf (probe E)",
			ports: map[string]*Type{"text": rt2, "wenan": T("string")},
			base:  "text", field: "wenan", want: "text.wenan",
		},
		{
			name:  "undeclared base is not a container",
			ports: map[string]*Type{"other": T("string")},
			base:  "image", field: "image", want: "image",
		},
		{
			name:  "whole-value binding stays flat",
			ports: map[string]*Type{"image": TList(T("string"))},
			base:  "", field: "image", want: "image",
		},
	}
	for _, tc := range cases {
		g := &Builder{nodes: []Node{&CodeRun{ID: "100004", OutTypes: tc.ports}}}
		got := g.aggregateFieldPort(varRef{nodeID: "100004", output: tc.base, kind: "code"}, tc.field)
		if got != tc.want {
			t.Errorf("%s: aggregateFieldPort(output=%q, field=%q) = %q, want %q",
				tc.name, tc.base, tc.field, got, tc.want)
		}
	}
}

// 「整体值绑定」的两种生产者必须分治（2026-09-13，产物门禁接线暴露）：
//
//   - **物化 record 字面量的 shim**（Synthetic 单 `result` object 口）：渲染器
//     把 record 的成员**展开成扁平顶层端口**（node_outputs 同时有 f0/f1 与
//     result），共享 field-port 契约因此把 `r.f0` 读成 from_path `f0`；反编译
//     侧同样只逆译扁平叶子。⇒ 候选必须是**裸叶子**，`result.f0` 会被抬回
//     `v1.result.f0`（RT1 没有 result 字段 → FIELD_NOT_FOUND，收敛测试
//     roundtrip_property seed=1/3/… 实测）。
//   - **foreign code 的 record 型整体口**（非 Synthetic，OutTypes = {result: Resp}）：
//     成员**不在**扁平端口里（node 只声明 [output result]），扁平叶子是**未声明
//     端口**（门禁 L2：`ref 100006.data does not resolve`）⇒ 必须保留基段。
//
// 判据 = 生产者**声明的端口模型**（是否把成员展开成扁平端口），不是文件名、
// 也不是绑定形状。
func TestAggregateFieldPortWholeBindingRecordShimVsForeignRecord(t *testing.T) {
	rt2 := TObject(map[string]*Type{"wenan": T("string")})
	// 物化 record 字面量：Synthetic 单 result object 口 —— 成员展开为扁平端口。
	shim := &CodeRun{ID: "100004", Synthetic: true, OutTypes: map[string]*Type{"result": rt2}}
	// foreign code 的 record 型整体口：成员不展开。
	foreign := &CodeRun{ID: "100006", OutTypes: map[string]*Type{"result": rt2}}
	cases := []struct {
		name string
		node *CodeRun
		base string
		fld  string
		want string
	}{
		{"record shim whole binding is the bare leaf", shim, "result", "wenan", "wenan"},
		{"record shim whole binding empty base is the bare leaf", shim, "", "wenan", "wenan"},
		{"foreign record whole port keeps the base", foreign, "result", "wenan", "result.wenan"},
	}
	for _, tc := range cases {
		g := &Builder{nodes: []Node{tc.node}}
		got := g.aggregateFieldPort(varRef{nodeID: tc.node.ID, output: tc.base, kind: "code"}, tc.fld)
		if got != tc.want {
			t.Errorf("%s: aggregateFieldPort(output=%q, field=%q) = %q, want %q",
				tc.name, tc.base, tc.fld, got, tc.want)
		}
	}
}
