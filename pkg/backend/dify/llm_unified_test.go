package dify

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/llm"
)

//@feature llm-unified-envelope
//@feature open-object

func TestUnifiedLLMStructuredOutputUsesClosedSemanticSchema(t *testing.T) {
	n := &ir.LLMCall{ID: "llm", Model: "openai/gpt", Result: &ir.LLMResultContract{
		PayloadSchema: llm.Record("Answer", []llm.LLMField{
			{Name: "answer", Required: true, Schema: llm.String()},
			{Name: "score", Required: false, Schema: llm.Optional(llm.Int64())},
		}),
	}}
	r := &irRenderer{premint: map[string]string{}}
	decl := r.emitLLM(n)
	data := decl.Data.(*LLMDataNode)
	if data.StructuredOutput == nil || !data.StructuredOutput.Enabled {
		t.Fatal("structured output was not enabled")
	}
	schema := data.StructuredOutput.SchemaDef
	if schema["additionalProperties"] != false {
		t.Fatalf("schema is not closed: %#v", schema)
	}
	required, _ := schema["required"].([]string)
	if len(required) != 1 || required[0] != "value" {
		t.Fatalf("required = %#v", schema["required"])
	}
	props := schema["properties"].(map[string]any)
	payload := props["value"].(map[string]any)
	if payload["additionalProperties"] != false {
		t.Fatalf("payload schema is not closed: %#v", payload)
	}
	r.index = map[string]ir.Node{"llm": n}
	if got := r.portFor("llm", "text.answer"); got != "structured_output.value.answer" {
		t.Fatalf("semantic path mapped to %q", got)
	}
}

func TestLLMSchemaDefPreservesOptionalEnumAndMapValue(t *testing.T) {
	schema := llm.Record("Envelope", []llm.LLMField{
		{Name: "values", Required: true, Schema: llm.MapString(llm.Int64())},
		{Name: "verdict", Required: false, Schema: llm.Optional(llm.Enum("Verdict", []string{"approve", "reject"}))},
	})
	got := llmSchemaDef(schema)
	props := got["properties"].(map[string]any)
	wantMap := map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "integer"}}
	if !reflect.DeepEqual(props["values"], wantMap) {
		t.Fatalf("map schema = %#v, want %#v", props["values"], wantMap)
	}
	verdict := props["verdict"].(map[string]any)
	if _, ok := verdict["anyOf"]; !ok {
		t.Fatalf("optional enum schema = %#v, want nullable anyOf", verdict)
	}
}

func TestUnifiedLLMWholeEnvelopeAddsOneAssemblyAdapter(t *testing.T) {
	call := &ir.LLMCall{ID: "llm", Model: "openai/gpt", OutTypes: map[string]*ir.Type{"text": ir.T("string")}, Result: &ir.LLMResultContract{PayloadSchema: llm.String()}}
	graph := &ir.Graph{
		Entry: &ir.Start{ID: "start"},
		Root: &ir.Block{Stmts: []ir.Stmt{
			&ir.NodeStmt{Node: call},
			&ir.ReturnStmt{End: &ir.End{ID: "end", Inputs: []ir.Ref{{Producer: "llm", Output: "response"}}}},
		}},
	}
	got, err := RenderFromIR(graph, "workflow")
	if err != nil {
		t.Fatal(err)
	}
	adapters := 0
	var llmID, adapterID, endID string
	for _, node := range got.Graph.Nodes {
		switch data := node.Data.(type) {
		case *LLMDataNode:
			llmID = node.ID
		case *CodeDataNode:
			if !strings.Contains(data.Code, "__ncoda_llm_envelope__") {
				continue
			}
			adapters++
			adapterID = node.ID
			if strings.Contains(data.Code, `"reasoning_content": None`) || strings.Contains(data.Code, `"usage": None`) || strings.Contains(data.Code, `"finish_reason": None`) {
				t.Fatalf("Dify-native metadata was lowered to null:\n%s", data.Code)
			}
		case *OutputNode:
			endID = node.ID
			if len(data.Outputs) != 1 || len(data.Outputs[0].ValueSelector) != 2 || data.Outputs[0].ValueSelector[0] != adapterID || data.Outputs[0].ValueSelector[1] != ir.LLMEnvelopeOutput {
				t.Fatalf("end output selector = %#v, want adapter response", data.Outputs)
			}
		}
	}
	if adapters != 1 {
		t.Fatalf("whole-envelope adapters = %d, want 1", adapters)
	}
	wantEdges := map[[2]string]bool{{llmID, adapterID}: false, {adapterID, endID}: false}
	for _, edge := range got.Graph.Edges {
		key := [2]string{edge.Source, edge.Target}
		if _, ok := wantEdges[key]; ok {
			wantEdges[key] = true
		}
	}
	for edge, found := range wantEdges {
		if !found {
			t.Fatalf("missing control edge %s -> %s; edges=%#v", edge[0], edge[1], got.Graph.Edges)
		}
	}
}

func TestUnifiedLLMTextAccessDoesNotAddAssemblyAdapter(t *testing.T) {
	call := &ir.LLMCall{ID: "llm", Model: "openai/gpt", OutTypes: map[string]*ir.Type{"text": ir.T("string")}, Result: &ir.LLMResultContract{PayloadSchema: llm.String()}}
	graph := &ir.Graph{
		Entry: &ir.Start{ID: "start"},
		Root: &ir.Block{Stmts: []ir.Stmt{
			&ir.NodeStmt{Node: call},
			&ir.ReturnStmt{End: &ir.End{ID: "end", Inputs: []ir.Ref{{Producer: "llm", Output: "text"}}}},
		}},
	}
	got, err := RenderFromIR(graph, "workflow")
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range got.Graph.Nodes {
		if code, ok := node.Data.(*CodeDataNode); ok && strings.Contains(code.Code, "__ncoda_llm_envelope__") {
			t.Fatal("member-only access generated an envelope adapter")
		}
	}
}
