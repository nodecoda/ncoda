package ir_test

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

// A named loop-output port has the same collection contract as the default
// output port: each iteration's empty-array yield is an array element.
func TestNamedEmptyArrayYieldPreservesLoopOutputListShape(t *testing.T) {
	src := workflowHeader + `
function main() {
    let agg = for (item in ["a", "b"]) {
        yield all_fenjing_list = [];
    };
}
`
	g := buildIR(t, src)
	loop := firstOf(t, g, ir.KindLoop).(*ir.Loop)
	got := loop.OutTypes["all_fenjing_list"]
	if got == nil || got.Kind != "list" {
		t.Fatalf("loop output = %v, want list<list<string>>", got)
	}
	if got.Elem == nil || got.Elem.Kind != "list" {
		t.Fatalf("loop output element = %v, want list<string>", got.Elem)
	}
}
