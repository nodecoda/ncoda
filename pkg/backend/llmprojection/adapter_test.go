package llmprojection

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/llm"
)

func TestLowerSubstitutesWholeEnvelopeSuccessPath(t *testing.T) {
	call := &ir.LLMCall{ID: "llm", Result: &ir.LLMResultContract{PayloadSchema: llm.String(), WholeEnvelopeConsumed: true}}
	projection := &flow.Projection{
		Nodes: []ir.Node{&ir.Start{ID: "start"}, call, &ir.End{ID: "end", Inputs: []ir.Ref{{Producer: "llm", Output: ir.LLMEnvelopeOutput}}}},
		Edges: []flow.Edge{{SourceNodeID: "start", TargetNodeID: "llm"}, {SourceNodeID: "llm", TargetNodeID: "end"}},
	}
	plan := Lower(projection, MetadataPolicy{})
	adapterID := plan.Adapters["llm"]
	if adapterID == "" || len(plan.Projection.Nodes) != 4 {
		t.Fatalf("adapter plan = %#v, nodes=%d", plan.Adapters, len(plan.Projection.Nodes))
	}
	want := []flow.Edge{
		{SourceNodeID: "start", TargetNodeID: "llm"},
		{SourceNodeID: "llm", TargetNodeID: adapterID},
		{SourceNodeID: adapterID, TargetNodeID: "end"},
	}
	if len(plan.Projection.Edges) != len(want) {
		t.Fatalf("edges = %#v, want %#v", plan.Projection.Edges, want)
	}
	for i := range want {
		if plan.Projection.Edges[i] != want[i] {
			t.Fatalf("edge[%d] = %#v, want %#v", i, plan.Projection.Edges[i], want[i])
		}
	}
	adapter := plan.Projection.Nodes[2].(*ir.CodeRun)
	if !strings.Contains(adapter.Code, `"reasoning_content": None`) || !strings.Contains(adapter.Code, `"usage": None`) || !strings.Contains(adapter.Code, `"finish_reason": None`) {
		t.Fatalf("unsupported metadata is not literal null:\n%s", adapter.Code)
	}
	if len(adapter.Inputs) != 1 || adapter.Inputs[0].BlockID != "llm" || adapter.Inputs[0].Name != "text" {
		t.Fatalf("adapter data refs = %#v", adapter.Inputs)
	}
}

func TestLowerOrdersTerminalAdapterEdgesByProducer(t *testing.T) {
	projection := &flow.Projection{Nodes: []ir.Node{
		&ir.LLMCall{ID: "z", Result: &ir.LLMResultContract{PayloadSchema: llm.String(), WholeEnvelopeConsumed: true}},
		&ir.LLMCall{ID: "a", Result: &ir.LLMResultContract{PayloadSchema: llm.String(), WholeEnvelopeConsumed: true}},
	}}
	want := []flow.Edge{
		{SourceNodeID: "a", TargetNodeID: AdapterID("a")},
		{SourceNodeID: "z", TargetNodeID: AdapterID("z")},
	}
	for i := 0; i < 20; i++ {
		if got := Lower(projection, MetadataPolicy{}).Projection.Edges; !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d edges = %#v, want %#v", i, got, want)
		}
	}
}

func TestLowerOmitsAdapterForMemberOnlyAccess(t *testing.T) {
	call := &ir.LLMCall{ID: "llm", Result: &ir.LLMResultContract{PayloadSchema: llm.String()}}
	projection := &flow.Projection{
		Nodes: []ir.Node{call, &ir.End{ID: "end", Inputs: []ir.Ref{{Producer: "llm", Output: "text"}}}},
		Edges: []flow.Edge{{SourceNodeID: "llm", TargetNodeID: "end"}},
	}
	plan := Lower(projection, MetadataPolicy{})
	if len(plan.Adapters) != 0 || len(plan.Projection.Nodes) != 2 || len(plan.Projection.Edges) != 1 {
		t.Fatalf("member-only lowering changed graph: adapters=%#v projection=%#v", plan.Adapters, plan.Projection)
	}
}
