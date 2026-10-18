package tool

import (
	"os"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

func TestNewRegistryRejectsDuplicateDefinitions(t *testing.T) {
	definition := Definition{Provider: "web_search", Operation: "search"}
	if _, err := NewRegistry(definition, definition); err == nil || !strings.Contains(err.Error(), "duplicate definition") {
		t.Fatalf("NewRegistry duplicate error = %v, want duplicate definition", err)
	}
}

func writeRegistryFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := dir + "/" + name
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func TestRegistryValidatesToolCalls(t *testing.T) {
	registry, err := NewRegistry(Definition{
		Provider:  "web_search",
		Operation: "search",
		Parameters: []Parameter{
			{Name: "query", Type: "string", Required: true},
			{Name: "limit", Type: "integer"},
		},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	tests := []struct {
		name string
		call *ir.ToolCall
		want string
	}{
		{
			name: "unknown capability",
			call: &ir.ToolCall{ID: "tool-1", ProviderID: "time", Operation: "now"},
			want: "unknown capability \"time/now\"",
		},
		{
			name: "unknown argument",
			call: &ir.ToolCall{ID: "tool-1", ProviderID: "web_search", Operation: "search", Params: []ir.ToolParam{
				{Key: "query", Value: "ncoda"},
				{Key: "extra", Value: true},
			}},
			want: "does not declare argument \"extra\"",
		},
		{
			name: "missing required argument",
			call: &ir.ToolCall{ID: "tool-1", ProviderID: "web_search", Operation: "search"},
			want: "requires argument \"query\"",
		},
		{
			name: "unsupported retry",
			call: &ir.ToolCall{
				ID: "tool-1", ProviderID: "web_search", Operation: "search",
				Params:    []ir.ToolParam{{Key: "query", Value: "ncoda"}},
				Exception: &ir.ExceptionConfig{MaxRetry: 2},
			},
			want: "does not support retry",
		},
		{
			name: "unsupported failure branch",
			call: &ir.ToolCall{
				ID: "tool-1", ProviderID: "web_search", Operation: "search",
				Params:    []ir.ToolParam{{Key: "query", Value: "ncoda"}},
				Exception: &ir.ExceptionConfig{FailBranch: true},
			},
			want: "does not support a failure branch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := registry.ValidateGraph(graphWithNode(tt.call))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateGraph error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestRegistryAcceptsDeclaredPoliciesAndArguments(t *testing.T) {
	registry, err := NewRegistry(Definition{
		Provider:              "web_search",
		Operation:             "search",
		Parameters:            []Parameter{{Name: "query", Required: true}},
		SupportsRetry:         true,
		SupportsFailureBranch: true,
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	call := &ir.ToolCall{
		ID: "tool-1", ProviderID: "web_search", Operation: "search",
		Params:    []ir.ToolParam{{Key: "query", Value: "ncoda"}},
		Exception: &ir.ExceptionConfig{MaxRetry: 2, FailBranch: true},
	}
	if err := registry.ValidateGraph(graphWithNode(call)); err != nil {
		t.Fatalf("ValidateGraph: %v", err)
	}
}

func TestRegistryValidatesToolsInsideProjectedLoops(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	call := &ir.ToolCall{ID: "tool-1", ProviderID: "web_search", Operation: "search"}
	loop := &ir.Loop{ID: "loop-1", LoopType: "infinite"}
	graph := &ir.Graph{
		Entry: &ir.Start{ID: "start"},
		Root: &ir.Block{Stmts: []ir.Stmt{&ir.LoopStmt{
			ID:        loop.ID,
			Container: loop,
			Body:      &ir.Block{Stmts: []ir.Stmt{&ir.NodeStmt{Node: call}}},
		}}},
	}
	if err := registry.ValidateGraph(graph); err == nil || !strings.Contains(err.Error(), "web_search/search") {
		t.Fatalf("ValidateGraph nested Tool error = %v, want unknown nested capability", err)
	}
}

func graphWithNode(node ir.Node) *ir.Graph {
	return &ir.Graph{
		Entry: &ir.Start{ID: "start"},
		Root:  &ir.Block{Stmts: []ir.Stmt{&ir.NodeStmt{Node: node}}},
	}
}

func TestRegistryAllowsArrayOfRecordElementChildren(t *testing.T) {
	// E5-b: an array<record:X> leaf may carry descendant element member
	// paths; a scalar leaf still may not.
	registry, err := NewRegistry(Definition{
		Provider:  "search",
		Operation: "query",
		Outputs: []OutputField{
			{Name: "doc_results", Type: "array<record:SearchDoc>"},
			{Name: "doc_results.url", Type: "string"},
			{Name: "doc_results.title", Type: "string"},
			{Name: "status_code", Type: "int"},
		},
	})
	if err != nil {
		t.Fatalf("NewRegistry with element children: %v", err)
	}
	registry, err = NewRegistry(Definition{
		Provider:  "search",
		Operation: "query",
		Outputs: []OutputField{
			// element paths first, then the array leaf (order-independent).
			{Name: "doc_results.url", Type: "string"},
			{Name: "doc_results", Type: "array<record:SearchDoc>"},
		},
	})
	if err != nil {
		t.Fatalf("NewRegistry with leaf after children: %v", err)
	}
	_ = registry
}

func TestRegistryRejectsScalarLeafWithChildren(t *testing.T) {
	_, err := NewRegistry(Definition{
		Provider:  "search",
		Operation: "query",
		Outputs: []OutputField{
			{Name: "doc_results", Type: "array<string>"},
			{Name: "doc_results.url", Type: "string"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts with parent") {
		t.Fatalf("NewRegistry array<string> parent error = %v, want conflicts with parent", err)
	}
	_, err = NewRegistry(Definition{
		Provider:  "search",
		Operation: "query",
		Outputs: []OutputField{
			{Name: "doc_results.url", Type: "string"},
			{Name: "doc_results", Type: "array<string>"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts with descendant") {
		t.Fatalf("NewRegistry array<string> descendant error = %v, want conflicts with descendant", err)
	}
}
