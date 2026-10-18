package dify

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

func TestHTTPURLTemplatePreservesVariableBinding(t *testing.T) {
	call := &ir.HTTPCall{
		ID:     "2",
		Method: "GET",
		URL:    "{{block_output_1.url}}",
		Inputs: []ir.Source{{BlockID: "1", Name: "url"}},
	}
	graph := &ir.Graph{
		Entry: &ir.Start{ID: "1", Params: []ir.Param{{Name: "url", Type: ir.T("string")}}},
		Root:  &ir.Block{Stmts: []ir.Stmt{&ir.NodeStmt{Node: call}, &ir.ReturnStmt{End: &ir.End{ID: "end"}}}},
	}
	got, err := RenderFromIR(graph, "workflow")
	if err != nil {
		t.Fatal(err)
	}
	var url string
	for _, node := range got.Graph.Nodes {
		if data, ok := node.Data.(*HTTPRequestDataNode); ok {
			url = data.URL
		}
	}
	const want = "{{#start_1.url#}}"
	if url != want {
		t.Fatalf("http URL = %q, want %q", url, want)
	}
	if url != "{{#start_1.url#}}" {
		t.Fatalf("http URL = %q, want {{#start_1.url#}}", url)
	}
}
