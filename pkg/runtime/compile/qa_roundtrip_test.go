package compile

// qa_roundtrip_test.go — question_answer 节点完整中断 roundtrip（U2.7,
// loop 外）。链路:graph(checkpoint store) → Invoke 首轮中断 → Extract
// → ResumeWithData 注入 → 恢复运行 → USER_RESPONSE 端口。
//
// D2 契约:qa 节点 v1 仅 loop 外（loop 内 resume 由 v2 覆盖,见 spike
// TestSpikeInterruptInsideLoop*）。

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/compose"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

type qaMemStore struct{ m map[string][]byte }

func (s *qaMemStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	b, ok := s.m[id]
	return b, ok, nil
}
func (s *qaMemStore) Set(_ context.Context, id string, b []byte) error {
	if s.m == nil {
		s.m = map[string][]byte{}
	}
	s.m[id] = b
	return nil
}

func TestQAInterruptResumeRoundtrip(t *testing.T) {
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("ask", spec.KindQA, &spec.QaConfig{Template: "确认操作?", Choices: []string{"yes", "no"}}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "ask", Output: "USER_RESPONSE"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "ask"},
			{Source: "ask", Target: "e1"},
		},
	}
	store := &qaMemStore{}
	r := mustCompile(t, New(testRegistry()), s, compose.WithCheckPointStore(store))

	// 首轮:中断
	_, err := r.Invoke(context.Background(), map[string]any{}, compose.WithCheckPointID("qa-run"))
	info, ok := compose.ExtractInterruptInfo(err)
	if !ok {
		t.Fatalf("expected interrupt, got %v", err)
	}
	if len(info.InterruptContexts) != 1 {
		t.Fatalf("interrupt contexts = %d, want 1", len(info.InterruptContexts))
	}
	id := info.InterruptContexts[0].ID

	// resume:注入选择
	rctx := compose.ResumeWithData(context.Background(), id, map[string]any{"choice": "yes"})
	out, err := r.Invoke(rctx, map[string]any{}, compose.WithCheckPointID("qa-run"))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	got := UnwrapOutput(out)
	resp, _ := got["output"].(map[string]any)
	if resp["choice"] != "yes" {
		t.Fatalf("resume output = %v, want choice=yes", got)
	}
	t.Logf("PASS qa roundtrip: interrupt -> resume -> USER_RESPONSE=%v", resp)
}
