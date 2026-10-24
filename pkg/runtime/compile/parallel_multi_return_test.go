package compile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func TestParallelMultiReturnFlowEnd(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "probes", "zz_parallel_multi_return_probe.ncoda"))
	if err != nil {
		t.Fatal(err)
	}
	res := passes.DefaultPipeline().Run(string(b), "zz_parallel_multi_return_probe.ncoda", nil)
	if res.HasErrors() {
		t.Fatalf("frontend: %v", res.Errors())
	}
	g, err := ir.Build(res.Program)
	if err != nil {
		t.Fatal(err)
	}
	p, err := flow.Project(g)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range p.Nodes {
		t.Logf("NODE %s %s", n.NodeID(), n.Kind())
	}
	for _, e := range p.Edges {
		t.Logf("EDGE %s[%s] -> %s[%s]", e.SourceNodeID, e.SourcePortID, e.TargetNodeID, e.TargetPortID)
	}
	s, err := spec.Build(g, p)
	if err != nil {
		t.Fatal(err)
	}
	c := New(nodes.StdRegistry(), WithDeps(nodes.Deps{Models: gateEchoModel{}, HTTP: gateStubHTTP{}, Knowledge: gateStubKnowledge{}}))
	r, cerr := c.Compile(context.Background(), s)
	if cerr != nil {
		t.Fatalf("compile: %v", cerr)
	}
	out, ierr := r.Invoke(context.Background(), map[string]any{})
	if ierr != nil {
		t.Fatalf("invoke: %v", ierr)
	}
	output := UnwrapOutput(out)
	bb, _ := json.Marshal(output)
	t.Logf("OUT=%s", bb)
	// first-to-end wins: left:x or right:x, never "after"
	if string(bb) != `{"output":"left:x"}` && string(bb) != `{"output":"right:x"}` {
		t.Errorf("want left:x or right:x (first-to-end wins), got %s", bb)
	}
}
