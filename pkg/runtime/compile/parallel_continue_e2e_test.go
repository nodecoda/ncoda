package compile

// parallel_continue_e2e_test.go — continue inside a parallel loop.
//
// A continue is iteration-local: it skips the rest of that iteration only.
// The marker is carried in a per-iteration context box (never the shared
// LoopState), and the skipped iteration leaves no output slot — the same
// compacted shape the serial driver produces.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func parallelContinueSpec() *spec.Spec {
	return &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("src", spec.KindCode, &spec.CodeConfig{
				Code:      "def main() -> dict:\n    return {\"values\": [1, 2, 3]}",
				Language:  "python",
				Synthetic: true,
				OutOrder:  []string{"values"},
			}),
			nodeSpec("loop", spec.KindLoop, &spec.LoopConfig{
				VarName:      "value",
				LoopType:     "by_array",
				Inputs:       []spec.Source{{Path: []string{"values"}, BlockID: "src", Name: "values"}},
				Types:        map[string]*spec.TypeSpec{"values": {Kind: "list"}},
				IsParallel:   true,
				ParallelNums: 3,
				OutputSources: []spec.Source{
					{Path: []string{"output"}, BlockID: "body", Name: "result"},
				},
				Children: []spec.NodeSpec{
					nodeSpec("sel", spec.KindSelector, &spec.SelectorConfig{Conditions: []spec.Condition{{
						Left:     spec.Operand{Ref: &spec.Ref{Producer: "loop", Output: "value"}},
						Operator: 1, // equals
						Right:    spec.Operand{Val: 2},
					}}}),
					nodeSpec("cont", spec.KindContinue, &spec.ContinueConfig{}),
					nodeSpec("body", spec.KindCode, &spec.CodeConfig{
						Code:     "def main(value) -> dict:\n    return {\"result\": value}",
						Language: "python",
						OutOrder: []string{"result"},
						Inputs:   []spec.Source{{Path: []string{"value"}, BlockID: "loop", Name: "value"}},
					}),
				},
				InnerEdges: []spec.EdgeSpec{
					{Source: "loop", Target: "sel", SourcePort: wLoopFunctionInlineOutput},
					{Source: "sel", Target: "cont", SourcePort: "true"},
					{Source: "sel", Target: "body", SourcePort: "false"},
					{Source: "body", Target: "loop", TargetPort: wLoopFunctionInlineInput},
				},
			}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "loop", Output: "output"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "src"},
			{Source: "src", Target: "loop"},
			{Source: "loop", Target: "e1"},
		},
	}
}

func TestParallelLoopContinueSkipsIteration(t *testing.T) {
	r := mustCompile(t, New(testRegistry()), parallelContinueSpec())
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{}))
	got, _ := out["output"].([]any)
	if len(got) != 2 {
		t.Fatalf("output = %#v, want two slotted iterations", out["output"])
	}
	if got[0] != float64(1) && got[0] != 1 {
		t.Fatalf("output[0] = %#v, want 1", got[0])
	}
	if got[1] != float64(3) && got[1] != 3 {
		t.Fatalf("output[1] = %#v, want 3 (value 2 skipped)", got[1])
	}
}

// TestParallelLoopRejectsNestedLoopContainer locks the fail-closed contract:
// a nested loop keeps its progress in LoopState keyed by node id, which every
// worker of the outer parallel loop would share (races / wrong results).
func TestParallelLoopRejectsNestedLoopContainer(t *testing.T) {
	s := parallelContinueSpec()
	var lc spec.LoopConfig
	if err := json.Unmarshal(s.Nodes[2].Config, &lc); err != nil {
		t.Fatal(err)
	}
	lc.Children = append(lc.Children, nodeSpec("nested", spec.KindLoop, &spec.LoopConfig{LoopType: "by_array"}))
	b, err := json.Marshal(&lc)
	if err != nil {
		t.Fatal(err)
	}
	s.Nodes[2].Config = b

	_, err = New(testRegistry()).Compile(context.Background(), s)
	if err == nil || diag.CodeOf(err) != diag.RKindRejected {
		t.Fatalf("err = %v, want RKindRejected for a nested loop in a parallel body", err)
	}
}
