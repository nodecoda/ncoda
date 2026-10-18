package binding

// chatflow_builtin_test.go — M13 advanced-chat 入口内置函数
// conversation_history() -> string:仅 @mode advanced-chat 注册,
// workflow/agent 下调用报未定义。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/diag"
)

func TestConversationHistoryBuiltinAdvancedChat(t *testing.T) {
	errs, _ := errorDiagnostics(t, `@mode advanced-chat
function main(string query) {
    let h = conversation_history();
    output(h);
}
`)
	if len(errs) != 0 {
		t.Fatalf("advanced-chat conversation_history() should bind cleanly, got %v", errs)
	}
}

func TestConversationHistoryBuiltinRejectedInWorkflow(t *testing.T) {
	errs, _ := errorDiagnostics(t, `@mode workflow
function main(string query) {
    let h = conversation_history();
    output(h);
}
`)
	assertUndefinedConversationHistory(t, errs)
}

func TestConversationHistoryBuiltinRejectedInAgent(t *testing.T) {
	errs, _ := errorDiagnostics(t, `@mode agent
function main(string query) {
    let h = conversation_history();
    output(h);
}
`)
	assertUndefinedConversationHistory(t, errs)
}

func assertUndefinedConversationHistory(t *testing.T, errs []diag.Diagnostic) {
	t.Helper()
	for _, d := range errs {
		if d.Code == diag.UNDEFINED_VAR && strings.Contains(d.Message, "conversation_history") {
			return
		}
	}
	t.Fatalf("want UNDEFINED_VAR for conversation_history, got %v", errs)
}
