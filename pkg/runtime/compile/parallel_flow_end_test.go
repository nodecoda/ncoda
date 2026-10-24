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

func TestParallelReturnFlowEnd(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "probes", "zz_parallel_return_probe.ncoda"))
	if err != nil {
		t.Fatal(err)
	}
	res := passes.DefaultPipeline().Run(string(b), "zz_parallel_return_probe.ncoda", nil)
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
	s, err := spec.Build(g, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range s.Nodes {
		t.Logf("NODE %s %s", n.ID, n.Kind)
	}
	for _, e := range s.Edges {
		t.Logf("EDGE %s[%s] -> %s[%s]", e.Source, e.SourcePort, e.Target, e.TargetPort)
	}
	// 关键断言：left return 分支（100002 code）直连 900001 end；100004(after)
	// 不再接收 left 分支的边。
	for _, e := range s.Edges {
		if e.Source == "100002" && e.Target == "100004" {
			t.Errorf("return branch 100002 still flows to after-parallel 100004: %v", e)
		}
		if e.Source == "100002" && e.Target == "900001" {
			t.Logf("OK: return branch 100002 -> end 900001")
		}
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
	if string(bb) != `{"output":"left:x"}` {
		t.Errorf("want flow-end output left:x, got %s", bb)
	}
}
