package compile

// qa_terminal_contract_test.go — a question_answer node with no outgoing
// edge is a terminal node. Coze exports end the workflow at a QA node
// (structured output answers), so the compiled spec carries no End node.
// The runtime must compile it and interrupt at the QA (HITL), not reject
// the spec with "no terminal end node".

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/compose"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func TestQATerminalCompilesAndInterrupts(t *testing.T) {
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("ask", spec.KindQA, &spec.QaConfig{
				Template: "{{input}}",
				Fields:   []spec.QaField{{Name: "name", Kind: "paragraph"}},
			}),
		},
		Edges: []spec.EdgeSpec{{Source: "100001", Target: "ask"}},
	}
	store := &qaMemStore{}
	r := mustCompile(t, New(testRegistry()), s, compose.WithCheckPointStore(store))

	_, err := r.Invoke(context.Background(), map[string]any{"input": "q"}, compose.WithCheckPointID("qa-terminal"))
	if _, ok := compose.ExtractInterruptInfo(err); !ok {
		t.Fatalf("expected interrupt at terminal QA, got %v", err)
	}
}
