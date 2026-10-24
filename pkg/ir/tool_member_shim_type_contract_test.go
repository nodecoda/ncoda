// 语言特性矩阵锚点声明（docs/LANGUAGE-FEATURE-MATRIX.md）——calc-builtin shim 输入类型：
//@feature field-index — tool 结果成员（`r.duration` / `r.data.url`）进入 `int()` / `len()` shim 时
// 必须按字段类型声明，而不是整条结果记录（TestToolMemberShimInputTypeIsFieldType）。

package ir_test

// tool_member_shim_type_contract_test.go — tool 结果成员进入 calc-builtin shim 的
// **输入类型**契约（2026-10-21）。
//
// 一手：语料 3 份（S122jryl_gd / S132dongtailishi / mryy_douyin）的
// `int(v.duration)` 在平台 wire 里声明 `type: integer`（源工作流 node_inputs
// 实测），但重编译后 shim 参数被声明成 `object{duration, outputList}`
// （= 整条工具结果记录）⇒ 运行期 object 形状归一化把数字包成 `{value: n}`，
// `int()` shim 收不到标量、恒失败。根因 = `refType` 对 tool 绑定的成员路径走了
// `dottedRefType` 的「无点 = 整个端口」分支。
//
// 判据（一条）：tool 绑定的 ref.typ 是整条结果记录，ref.output 是记录下的
// 成员路径 ⇒ 类型 = 该字段的类型；字段缺失才回落到整条记录。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/tool"
)

func toolMemberShimGraph(t *testing.T) *ir.Graph {
	t.Helper()
	reg, err := tool.NewRegistry(tool.Definition{
		Provider:   "p",
		Operation:  "op",
		Parameters: []tool.Parameter{{Name: "q", Type: "string"}},
		Outputs: []tool.OutputField{
			{Name: "duration", Type: "float"},
			{Name: "data.url", Type: "string"},
			{Name: "outputList", Type: "array<record>"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	src := `@language nodecoda/1
@mode workflow

import "acme.p";

function main(string q) -> int {
    let r = p.op({ q: q });
    let n = int(r.duration);
    let u = len(r.data.url);
    return n + u;
}
`
	res := passes.DefaultPipeline().WithToolRegistry(reg).Run(src, "t.dc", nil)
	if res.HasErrors() {
		for _, d := range res.Diagnostics {
			t.Logf("%d %s: %s", d.Code, d.Severity, d.Message)
		}
		t.Fatal("pipeline errors")
	}
	if res.Program == nil {
		t.Fatal("no typed program")
	}
	g, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("ir.Build: %v", err)
	}
	return g
}

func TestToolMemberShimInputTypeIsFieldType(t *testing.T) {
	g := toolMemberShimGraph(t)
	want := map[string]string{"duration": "number", "data_url": "string"}
	got := map[string]string{}
	for _, nodes := range nodesOf(g) {
		for _, n := range nodes {
			cr, ok := n.(*ir.CodeRun)
			if !ok {
				continue
			}
			for name, typ := range cr.Types {
				if typ == nil {
					continue
				}
				if _, tracked := want[name]; tracked {
					got[name] = typ.Kind
					if typ.Kind == "object" {
						t.Errorf("shim input %q declared object (whole tool record): %+v", name, typ)
					}
				}
			}
		}
	}
	for name, k := range want {
		if got[name] != k {
			t.Errorf("shim input %q type = %q, want %q (all: %v)", name, got[name], k, got)
		}
	}
}

// code 产出方的同一根因（`v.otherInfos.font_size` → calc-builtin shim）：
// 首段是已声明口、类型是记录时，必须下钻到成员字段类型。
func TestCodeMemberShimInputTypeIsFieldType(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow

type OtherInfos = { int font_size; };

function main(string s) -> int {
    let v = foreign code javascript(string text = s) -> { otherInfos: OtherInfos; } { source ` + "`" + `async function main({ params }) {
  return { otherInfos: { font_size: 12 } };
}` + "`" + `; };
    return int(v.otherInfos.font_size);
}
`
	res := passes.DefaultPipeline().Run(src, "t.dc", nil)
	if res.HasErrors() {
		for _, d := range res.Diagnostics {
			t.Logf("%d %s: %s", d.Code, d.Severity, d.Message)
		}
		t.Fatal("pipeline errors")
	}
	g, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("ir.Build: %v", err)
	}
	found := false
	for _, nodes := range nodesOf(g) {
		for _, n := range nodes {
			cr, ok := n.(*ir.CodeRun)
			if !ok || cr.Synthetic == false {
				continue
			}
			for name, typ := range cr.Types {
				if name != "otherInfos_font_size" || typ == nil {
					continue
				}
				found = true
				if typ.Kind != "integer" {
					t.Fatalf("shim input otherInfos_font_size type = %q (%+v), want integer", typ.Kind, typ)
				}
			}
		}
	}
	if !found {
		t.Fatal("no shim input named otherInfos_font_size (int(v.otherInfos.font_size) not materialized?)")
	}
}
