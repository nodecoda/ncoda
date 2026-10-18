package ir

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
)

// 成员链首段的**两条通路必须同解**（2026-09-24，S74 一手）。
//
// 同一个语义（「读产出记录的某个成员」）在编译端有两条独立通路：
//
//	① 出口汇聚通路 `aggregateFieldPort`（build_return.go）—— 把绑定投影到出口；
//	② 引用解析通路 `resolveValueRef`（build.go）—— 把表达式解析成 ref path。
//
// 两条通路各写一份「首段是口名还是口下方的子路径」的规则 = **必然漂移**：S74
// (`7593231813066801190_S74_xuexi_fangfa`) 的 PLACEHOLDER 一手 —— ① 给 payload
// 字段读产出 `text.content`（后端再投成 `value.content`），② 给裸 `content`
// （后端投影加不上前缀）⇒ 产物门禁 ARTIFACT_GATE：
//
//	ref 100004.content does not resolve: node 100004 declares ports [value]
//
// 判据 = 逐 kind 的**同解表**：resolveValueRef 的结论必须等于 aggregateFieldPort
// 的结论。撤 build.go 的 `case "llm"` 分支（退回裸 `fieldPort`）⇒ 本测试红。
func TestResolveMemberChainFirstSegmentMatchesAggregatePath(t *testing.T) {
	rtText := TObject(map[string]*Type{"content": T("string")})
	cases := []struct {
		name   string
		ref    varRef
		field  string
		want   string
		ports  map[string]*Type
		absent bool
	}{
		{
			// S74 形：`v = r.text;` 后读 `v.content` ⇒ payload 字段活在口**下方**。
			name:  "llm payload base keeps the dotted path",
			ref:   varRef{nodeID: "100004", output: "text", kind: "llm", typ: rtText},
			field: "content",
			want:  "text.content",
		},
		{
			// S161 形（R-58）：基段本身已是 payload **成员路径**（`v = r.text.date_info;`
			// 后读 `v.date`）⇒ 成员仍在 payload 口**下方**，必须继续下钻。
			// 旧实现只认整段 `text`（`ref.output == "text"`）⇒ 退化成裸字段口
			// `date` ⇒ 后端投影加不上物理前缀 ⇒ 产物引用悬空：`ref 100002.date
			// does not resolve: node 100002 declares ports [value]`（一手 S161）。
			name:  "llm dotted payload base keeps drilling",
			ref:   varRef{nodeID: "100002", output: "text.date_info", kind: "llm", typ: rtText},
			field: "date",
			want:  "text.date_info.date",
		},
		{
			// 整只信封（`v = r`）：metadata/payload 字段**就是**信封的口名。
			name:  "llm envelope base keeps bare field ports",
			ref:   varRef{nodeID: "100004", output: LLMEnvelopeOutput, kind: "llm"},
			field: "text",
			want:  "text",
		},
		{
			// code（声明了 record 型整体口）= 成员在口下方（判据 = 声明端口模型）。
			name:  "code record-typed port keeps the base",
			ref:   varRef{nodeID: "100004", output: "result", kind: "code"},
			field: "f0",
			want:  "result.f0",
			ports: map[string]*Type{"result": TObject(map[string]*Type{"f0": T("string")})},
		},
		{
			// code（扁平多口）= 字段名即端口，不得合成 `image.image`。
			name:  "code flat multiport is its own port",
			ref:   varRef{nodeID: "100004", output: "image", kind: "code"},
			field: "image",
			want:  "image",
			ports: map[string]*Type{"image": TList(T("string"))},
		},
		{
			// tool / http 一类：注册表声明端口模型 ⇒ 沿用 fieldPort。
			name:  "http result field is its own port",
			ref:   varRef{nodeID: "100004", output: "body", kind: "http"},
			field: "body",
			want:  "body",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := &Builder{varToOutput: map[string]varRef{"v1": tc.ref}}
			if tc.ports != nil {
				g.nodes = []Node{&CodeRun{ID: tc.ref.nodeID, OutTypes: tc.ports}}
			}
			chain := ast.NewFieldAccessExpr(ast.NewIdentExpr("v1"), tc.field)
			got, ok := g.resolveValueRef(chain)
			if tc.absent {
				if ok {
					t.Fatalf("应不可解析，实际 %s:%s", got.nodeID, got.output)
				}
				return
			}
			if !ok {
				t.Fatalf("成员链应可解析（kind=%s base=%q field=%q）", tc.ref.kind, tc.ref.output, tc.field)
			}
			if got.output != tc.want {
				t.Fatalf("resolveValueRef 首段 = %q，want %q（kind=%s base=%q）",
					got.output, tc.want, tc.ref.kind, tc.ref.output)
			}
			// 同解判据：与出口汇聚通路**同一把尺子**（两处实现必然漂移）。
			if gv := g.aggregateFieldPort(tc.ref, tc.field); gv != got.output {
				t.Fatalf("两条通路不同解：aggregateFieldPort = %q，resolveValueRef = %q（kind=%s base=%q field=%q）",
					gv, got.output, tc.ref.kind, tc.ref.output, tc.field)
			}
		})
	}
}
