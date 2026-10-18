package decompile

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ncoda/render"
)

// Inside an iteration, the lowering shape encodes the current item as a node
// reference. It must restore as the already-declared loop variable, not as
// literal template text.
func TestIterationTemplateNodeReferenceUsesLoopItem(t *testing.T) {
	iteration := &nodeInfo{ID: "iteration_3", Type: "iteration"}
	tpl := &nodeInfo{ID: "template_5", Type: "template-transform", Data: map[string]any{
		"template":     "output: {{#iteration_3.item#}}",
		"variables":    []any{},
		"iteration_id": "iteration_3",
	}}
	c := &converter{
		g: &wfGraph{nodes: map[string]*nodeInfo{
			"iteration_3": iteration,
			"template_5":  tpl,
		}},
		startID: "start",
		used:    map[string]bool{},
	}
	c.registerLoopVar("iteration_3", "item")
	c.inIterationBody = true

	got := render.RenderNCodaStmt(c.convertTemplateTransform(tpl)[0], 0)
	if !strings.Contains(got, "`output: ${item}`") {
		t.Fatalf("iteration item reference not restored, got:\n%s", got)
	}
	if strings.Contains(got, "{{#iteration_3.item#}}") {
		t.Fatalf("lowering node reference must not remain literal text, got:\n%s", got)
	}
}

// The legacy template-transform local variable shape remains supported.
func TestTemplateTransformVariableShapeStillSupported(t *testing.T) {
	tpl := &nodeInfo{ID: "template_5", Type: "template-transform", Data: map[string]any{
		"template": "output: {{ arg1 }}",
		"variables": []any{map[string]any{
			"variable":       "arg1",
			"value_selector": []any{"iteration_3", "item"},
		}},
	}}
	c := &converter{
		g: &wfGraph{nodes: map[string]*nodeInfo{
			"iteration_3": &nodeInfo{ID: "iteration_3", Type: "iteration"},
			"template_5":  tpl,
		}},
		startID: "start",
		used:    map[string]bool{},
	}
	c.registerLoopVar("iteration_3", "item")
	c.inIterationBody = true

	got := render.RenderNCodaStmt(c.convertTemplateTransform(tpl)[0], 0)
	if !strings.Contains(got, "`output: ${item}`") {
		t.Fatalf("template variable not restored, got:\n%s", got)
	}
}
