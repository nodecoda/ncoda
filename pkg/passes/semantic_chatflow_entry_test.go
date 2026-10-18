package passes

// Pins LANGUAGE-REFERENCE.md §10「chatflow 入口」：@mode advanced-chat 入口首参必须
// 命名为 query（E1054 / diag.CHATFLOW_ENTRY）。R-SEM SEM-S10-14 的机器判据。
//
// 为什么专门补：规则实现在 semantic_pass.go M13，但此前**无行为测试**（全仓仅
// pipeline_test.go 把 CHATFLOW_ENTRY 用在「诊断码分配稳定性」表里 —— 只钉码值，
// 不触发规则）⇒ R-SEM 记为显式缺口，本测试把它闭合。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/diag"
)

func TestChatflowEntryRequiresQueryParam(t *testing.T) {
	src := `@mode advanced-chat
function main(string q) {
    output(q);
}
`
	ctx := runSemanticHarness(t, src)
	errs := semanticByCode(ctx, diag.CHATFLOW_ENTRY)
	if len(errs) != 1 {
		t.Fatalf("want one CHATFLOW_ENTRY (E1054), got %v", ctx.Diagnostics)
	}
	if !strings.Contains(strings.ToLower(errs[0].Message), "query") {
		t.Fatalf("message = %q, want it to mention query", errs[0].Message)
	}
}

func TestChatflowEntryQueryParamIsAccepted(t *testing.T) {
	src := `@mode advanced-chat
function main(string query) {
    output(query);
}
`
	ctx := runSemanticHarness(t, src)
	if errs := semanticByCode(ctx, diag.CHATFLOW_ENTRY); len(errs) != 0 {
		t.Fatalf("want no CHATFLOW_ENTRY for 'query' first param, got %v", errs)
	}
}

func TestChatflowEntryNoParamIsAccepted(t *testing.T) {
	// 无参 main 放行（runtime 可直接从 input map 读 query）—— M13 注释里的既定口径。
	src := `@mode advanced-chat
function main() {
    output("hi");
}
`
	ctx := runSemanticHarness(t, src)
	if errs := semanticByCode(ctx, diag.CHATFLOW_ENTRY); len(errs) != 0 {
		t.Fatalf("want no CHATFLOW_ENTRY for no-param main, got %v", errs)
	}
}
