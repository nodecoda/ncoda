package ir_test

// N-LANG-5 登记判据（docs/LANGUAGE-GAPS.md）。
//
// 现状是**规范已裁定**（A12-H4）：多端口 record 没有单一承载口，非出口位置必须
// 写明字段。本文件把这个裁定锁成机器判据：
//   - TestNLang5RecordInContainerIsRejected 断言现状（一旦支持 records-as-values 就会变红，
//     提醒把条目反过来改写，而不是悄悄放行）；
//   - TestNLang5FieldAccessIsTheRemedy 是正控：文档给出的替代写法必须真的能编译。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/tool"
)

const nl5Header = `@language nodecoda/1
@mode workflow
import "acme.gapkit";
`

// nl5Pipeline 声明一个**两输出字段**的工具：record 有两个端口 ⇒ 没有单一承载口。
func nl5Pipeline(t *testing.T) *passes.Pipeline {
	t.Helper()
	reg, err := tool.NewRegistry(tool.Definition{
		Provider:  "gapkit",
		Operation: "multi",
		Parameters: []tool.Parameter{
			{Name: "q", Type: "string", Required: true},
		},
		Outputs: []tool.OutputField{
			{Name: "a", Type: "string"},
			{Name: "b", Type: "string"},
		},
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	p := passes.DefaultPipeline()
	if reg != nil {
		p = p.WithToolRegistry(reg)
	}
	return p
}

// buildIRFromProgram 只跑 IR 层，便于断言「前端放行、IR 拒绝」这一分层事实。
func buildIRFromProgram(t *testing.T, res *passes.FrontendResult) (*ir.Graph, error) {
	t.Helper()
	if res.Program == nil {
		t.Fatal("frontend produced no TypedBoundProgram")
	}
	return ir.Build(res.Program)
}

func TestNLang5RecordInContainerIsRejected(t *testing.T) {
	src := nl5Header + `
function main() -> any {
    let v = gapkit.multi({ "q": "x" });
    let xs = [v, v];
    return xs;
}
`
	res := nl5Pipeline(t).Run(src, "t.dc", nil)
	if res.HasErrors() {
		t.Fatalf("frontend rejected the source before IR: %v", res.Errors())
	}
	_, err := buildIRFromProgram(t, res)
	if err == nil {
		t.Fatal("records-as-values now compile — N-LANG-5 must be re-decided " +
			"(docs/LANGUAGE-GAPS.md) and this judgement flipped to …IsAccepted")
	}
	if !strings.Contains(err.Error(), "没有单一承载口") {
		t.Fatalf("error = %v, want RECORD_FIELD_REQUIRED (没有单一承载口)", err)
	}
}

func TestNLang5FieldAccessIsTheRemedy(t *testing.T) {
	src := nl5Header + `
function main() -> any {
    let v = gapkit.multi({ "q": "x" });
    let xs = [v.a, v.b];
    return xs;
}
`
	res := nl5Pipeline(t).Run(src, "t.dc", nil)
	if res.HasErrors() {
		t.Fatalf("frontend: %v", res.Errors())
	}
	if _, err := buildIRFromProgram(t, res); err != nil {
		t.Fatalf("documented remedy (per-field access) must compile, got: %v", err)
	}
}
