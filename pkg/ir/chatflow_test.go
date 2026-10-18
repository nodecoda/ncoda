package ir_test

// chatflow_test.go — M13 advanced-chat:conversation_history() 降级为
// KindConversation 节点（输出端口 history）;workflow/agent 下报
// UnsupportedError。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
)

func TestBuildHistoryCallAdvancedChat(t *testing.T) {
	g := buildIR(t, `@mode advanced-chat
function main(string query) {
    let h = conversation_history();
    output(h);
}
`)
	byKind := nodesOf(g)
	conv, ok := byKind[ir.KindConversation]
	if !ok || len(conv) == 0 {
		t.Fatalf("no KindConversation node, kinds=%v", byKind)
	}
	hc, ok := conv[0].(*ir.HistoryCall)
	if !ok {
		t.Fatalf("conversation node is %T, want *ir.HistoryCall", conv[0])
	}
	if hc.NodeID() == "" || hc.Kind() != ir.KindConversation {
		t.Fatalf("HistoryCall identity wrong: %+v", hc)
	}
	// 输出端口 history 的下游引用（answer 消费）由 compile e2e 覆盖
	// （spec 边线从 conversation.history 连到 text/answer）。
}

func TestBuildHistoryCallValuePosition(t *testing.T) {
	g := buildIR(t, `@mode advanced-chat
function main(string query) {
    let h = conversation_history();
    output("len:" + h);
}
`)
	if _, ok := nodesOf(g)[ir.KindConversation]; !ok {
		t.Fatal("value-position conversation_history() must materialize a conversation node")
	}
}

func TestBuildHistoryCallRejectedOutsideAdvancedChat(t *testing.T) {
	for _, mode := range []string{"workflow", "agent"} {
		src := "@mode " + mode + "\nfunction main(string query) {\n    let h = conversation_history();\n    output(h);\n}\n"
		res := passes.DefaultPipeline().Run(src, "t.dc", nil)
		g, err := ir.Build(res.Program)
		if err == nil {
			t.Fatalf("%s: conversation_history() must be rejected, built graph %v", mode, g)
		}
		if !strings.Contains(err.Error(), "outside @mode advanced-chat") {
			t.Fatalf("%s: error = %v, want UnsupportedError mentioning advanced-chat", mode, err)
		}
	}
}
