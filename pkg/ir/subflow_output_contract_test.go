package ir_test

// subflow_output_contract_test.go — subflow 声明调用的输出端口契约（N-LANG-3）。
//
// 64_subflow_decl 的 `v.text` / `v.timelines[0].start` 读的是 subflow 声明里
// 的 output 字段。IR 建图层必须把声明的 output 表登记成端口契约，否则
// refType 只能报「producer kind "sub_workflow" has no typed conclusion and no
// port contract」——语言合法的成员读被 IR 当成未知类型。
//
// 撤掉 build_condition.go::portTypeOfNode 的 *SubWorkflowCall 分支，本测试即红。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
)

const subflowDeclSource = `@language nodecoda/1
@mode workflow

type Timeline = { int start; int end; };

subflow lyrics_timeline = ("7567215314397528118", "v0.0.1") {
  input:  { string lyric; int time; },
  output: { string text; array<Timeline> timelines; }
};

function main(string l, int t) -> string {
    let v = lyrics_timeline({ lyric: l, time: t });
    let first = v.timelines[0].start;
    return v.text + "/" + first;
}
`

func TestSubflowDeclaredOutputPortsResolve(t *testing.T) {
	profile := passes.DefaultTargetProfile()
	profile.Carrier = passes.LanguageCarrier()
	res := passes.DefaultPipeline().Run(subflowDeclSource, "64_subflow_decl.ncoda", profile)
	if res.HasErrors() {
		for _, d := range res.Diagnostics {
			t.Logf("frontend %d %s: %s", d.Code, d.Severity, d.Message)
		}
		t.Fatalf("frontend rejected the language-legal subflow declaration")
	}
	if res.Program == nil {
		t.Fatal("frontend produced no typed program")
	}
	g, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("ir.Build rejected a declared subflow output read: %v", err)
	}
	found := false
	for _, p := range g.Placements {
		if strings.Contains(p.Construct, "sub_workflow") || strings.Contains(p.Construct, "lyrics_timeline") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no subflow placement recorded: %+v", g.Placements)
	}
}
