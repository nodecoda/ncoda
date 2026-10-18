package passes

// Ports of regression/test_response_language_contract.py (E1044
// RESPONSE_CONTEXT): wrong mode / wrong owner. output(expr) is an
// intermediate-message emission legal in workflow/advanced-chat main at **any**
// flow position — containers and parallel arms alike (docs/NCODA-OUTPUT-MODEL.md
// §5，2026-09-18 用户裁定). The harness
// is the full pipeline, mirroring compile_nodecoda_result.

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/diag"
)

func responseErrors(res *FrontendResult) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range res.Diagnostics {
		if d.Severity == diag.Error && d.Code == diag.RESPONSE_CONTEXT {
			out = append(out, d)
		}
	}
	return out
}

// assertFirstResponseContract mirrors the reference parametrized test: the
// first error is RESPONSE_CONTEXT.
func assertFirstResponseContract(t *testing.T, src string) diag.Diagnostic {
	t.Helper()
	res := runFullPipeline(t, src)
	errs := res.Errors()
	if len(errs) == 0 {
		t.Fatalf("expected RESPONSE_CONTEXT errors: %v", res.Diagnostics)
	}
	if errs[0].Code != diag.RESPONSE_CONTEXT {
		t.Fatalf("first error code = %v, want RESPONSE_CONTEXT: %v", errs[0].Code, errs[0])
	}
	return errs[0]
}

func TestWorkflowOutputInMainIsLegal(t *testing.T) {
	res := runFullPipeline(t,
		`function main(string value) { output("ready"); return value; }`)
	if len(res.Errors()) != 0 {
		t.Fatalf("workflow main output must compile clean: %v", res.Errors())
	}
}

func TestAdvancedChatOutputInMainIsLegal(t *testing.T) {
	// output(expr) publishes an intermediate message in both workflow and
	// advanced-chat main (model: docs/NCODA-OUTPUT-MODEL.md §6).
	res := runFullPipeline(t,
		"@mode advanced-chat\nfunction main() { output(\"progress\"); }")
	if errs := responseErrors(res); len(errs) != 0 {
		t.Fatalf("advanced-chat main output must be legal: %v", errs)
	}
}

func TestOutputInAgentModeIsRejected(t *testing.T) {
	d := assertFirstResponseContract(t,
		"@mode agent\nfunction main() { output(\"x\"); }")
	if !strings.Contains(d.Message, "output() is legal only in workflow/advanced-chat main") {
		t.Fatalf("message = %q", d.Message)
	}
}

func TestOutputInNestedFunctionIsRejected(t *testing.T) {
	const src = `function helper() {
    output(1);
}
function main() { }
`
	d := assertFirstResponseContract(t, src)
	if !strings.Contains(d.Message, "output() is legal only in workflow/advanced-chat main") {
		t.Fatalf("message = %q", d.Message)
	}
}

// TestOutputInsideParallelIsLegal：并发臂内的中间消息 **合法**（2026-09-18 用户
// 裁定「消息节点就是普通节点，没有特殊性」）。旧判据「output() 只允许 … outside
// parallel」作废：并发臂内消息的语义 = 每臂内保序、跨臂顺序未指定（wire 也不给
// 顺序）；平台一手证据 = 672 源里 43 处扇出后继含消息节点的真实导出（X77/X184
// 的答复端点扇出就在其中）。
func TestOutputInsideParallelIsLegal(t *testing.T) {
	const src = `@mode advanced-chat
function main() {
    parallel {
        { output("a"); }
        { output("b"); }
    }
}
`
	if errs := responseErrors(runFullPipeline(t, src)); len(errs) != 0 {
		t.Fatalf("parallel 臂内的 output 必须合法: %v", errs)
	}
}

func TestOutputInsideForExprContainerIsLegal(t *testing.T) {
	// output() is an intermediate-message emission legal at any flow
	// position, including loop/iteration containers — coze OutputEmitter
	// is a faithful projection inside loops (corpus S110/S82/jianggushi1/
	// S111: output nodes nested in loop children). Regression-locked:
	// decompile(loop{output}) → compile must not raise RESPONSE_CONTEXT.
	const src = `function main(string[] items) {
    var r = for (item in items) {
        output(item);
        yield item;
    };
}
`
	if errs := responseErrors(runFullPipeline(t, src)); len(errs) != 0 {
		t.Fatalf("loop-contained output must be legal: %v", errs)
	}
}
