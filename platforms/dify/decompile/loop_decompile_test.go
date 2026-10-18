package decompile

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ncoda/render"
)

// A loop variable's declared var_type is the authority for break-condition
// operands. Current exports may omit the echoed varType on each break
// condition, so raising must resolve the selector label through
// loop_variables instead of rendering the wire string.
func TestWhileConditionUsesLoopVariableNumberType(t *testing.T) {
	loop := &nodeInfo{ID: "loop_2", Type: "loop", Data: map[string]any{
		"loop_variables": []any{map[string]any{
			"label": "num", "value_type": "constant",
			"var_type": "number", "value": 1,
		}},
		"break_conditions": []any{map[string]any{
			"variable_selector":   []any{"loop_2", "num"},
			"comparison_operator": "≥",
			"value":               "5",
		}},
	}}
	c := &converter{g: &wfGraph{nodes: map[string]*nodeInfo{"loop_2": loop}}}
	c.registerLoopVar("loop_2", "num")
	got := render.RenderNCodaExpr(c.whileCond(loop.Data))
	if !strings.Contains(got, "num < 5") {
		t.Fatalf("numeric loop break condition not restored, got: %s", got)
	}
	if strings.Contains(got, `num < "5"`) {
		t.Fatalf("loop variable must not be compared to a string, got: %s", got)
	}
}
