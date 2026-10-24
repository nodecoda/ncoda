package nodes

// qa_test.go — question_answer 节点语义单测（U2.7,loop 外）。
//
// 注意:StatefulInterrupt 的裸 err 与 ResumeWithData 只在 Eino graph runner
// 内被解释（context 注入机制）。节点级只能断言"首轮中断抛出";
// 完整 interrupt→checkpoint→resume roundtrip 是 compile 层集成测试
// （compile/qa_roundtrip_test.go）。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func TestQAFirstRunInterrupts(t *testing.T) {
	n := build(t, spec.KindQA, &spec.QaConfig{Template: "确认?"})
	_, err := n.Invoke(t.Context(), map[string]any{})
	if err == nil {
		t.Fatal("expected interrupt on first run")
	}
	if err.Error() == "" {
		t.Fatal("interrupt error must carry context")
	}
}
