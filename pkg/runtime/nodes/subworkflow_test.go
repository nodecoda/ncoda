package nodes

// subworkflow_test.go —— SubWorkflow 节点单测（SUBWORKFLOW-DECLARATION）。
// 覆盖：runner 注入后输入透传/输出端口；runner 缺失 → RNodeError（fail-closed）。

//@feature subflow

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

type fakeSubWorkflowRunner struct {
	got     SubWorkflowRequest
	result  map[string]any
	callCnt int
}

func (f *fakeSubWorkflowRunner) Run(_ context.Context, req SubWorkflowRequest) (map[string]any, error) {
	f.callCnt++
	f.got = req
	return f.result, nil
}

func subWorkflowSpecNode(cfg *spec.SubWorkflowConfig) *spec.NodeSpec {
	b, err := json.Marshal(cfg)
	if err != nil {
		panic(err)
	}
	return &spec.NodeSpec{ID: "s1", Kind: spec.KindSubWorkflow, Config: b}
}

func TestSubWorkflowInvokeWithRunner(t *testing.T) {
	runner := &fakeSubWorkflowRunner{result: map[string]any{"text": "ok"}}
	n, err := StdRegistry().Build(context.Background(), subWorkflowSpecNode(&spec.SubWorkflowConfig{
		WorkflowID:      "7567215314397528118",
		WorkflowVersion: "v0.0.1",
		Inputs: []spec.Source{
			{Path: []string{"歌词"}},
			{Path: []string{"时间"}},
		},
	}), Deps{SubWorkflows: runner})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	out, err := n.Invoke(t.Context(), map[string]any{"歌词": "hello", "时间": 10})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if runner.callCnt != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.callCnt)
	}
	if runner.got.WorkflowID != "7567215314397528118" || runner.got.WorkflowVersion != "v0.0.1" {
		t.Fatalf("runner req = %+v", runner.got)
	}
	if runner.got.Inputs["歌词"] != "hello" || runner.got.Inputs["时间"] != 10 {
		t.Fatalf("runner inputs = %v", runner.got.Inputs)
	}
	if out["text"] != "ok" {
		t.Fatalf("out = %v", out)
	}
}

func TestSubWorkflowNoRunnerFailsClosed(t *testing.T) {
	_, err := StdRegistry().Build(context.Background(), subWorkflowSpecNode(&spec.SubWorkflowConfig{
		WorkflowID: "x", WorkflowVersion: "v1",
	}), Deps{})
	if err == nil || diag.CodeOf(err) != diag.RNodeError {
		t.Fatalf("err = %v, want RNodeError", err)
	}
}
