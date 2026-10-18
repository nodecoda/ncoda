package decompile

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ncoda/render"
)

// Dify knowledge-retrieval carries its query as query_variable_selector in
// the current graph shape. The legacy top-level query template remains a
// registered compatibility shape, but must not override a live selector.
func TestKnowledgeQuerySelectorRestored(t *testing.T) {
	n := &nodeInfo{ID: "retrieval", Type: "knowledge-retrieval", Data: map[string]any{
		"dataset_ids":             []any{"ds-001", "ds-002"},
		"query_variable_selector": []any{"sys", "query"},
		"query":                   "",
	}}
	c := &converter{
		g: &wfGraph{
			nodes: map[string]*nodeInfo{"retrieval": n},
			edges: []edgeInfo{},
		},
		startID:     "start",
		extraParams: []string{},
	}
	got := render.RenderNCodaStmt(c.convertKnowledge(n)[0], 0)
	if !strings.Contains(got, `knowledge("ds-001,ds-002", query)`) {
		t.Fatalf("knowledge query selector not restored, got:\n%s", got)
	}
	if strings.Contains(got, `knowledge("ds-001,ds-002", "")`) {
		t.Fatalf("live selector must not be replaced by legacy empty query, got:\n%s", got)
	}
}

// The legacy top-level query remains valid for older Dify exports.
func TestKnowledgeLegacyQueryTemplateRestored(t *testing.T) {
	n := &nodeInfo{ID: "retrieval", Type: "knowledge-retrieval", Data: map[string]any{
		"dataset_ids": []any{"ds-001"},
		"query":       "{{#sys.query#}}",
	}}
	c := &converter{
		g: &wfGraph{
			nodes: map[string]*nodeInfo{"retrieval": n},
			edges: []edgeInfo{},
		},
		startID:     "start",
		extraParams: []string{},
	}
	got := render.RenderNCodaStmt(c.convertKnowledge(n)[0], 0)
	if !strings.Contains(got, `knowledge("ds-001", query)`) {
		t.Fatalf("legacy knowledge query not restored, got:\n%s", got)
	}
}
