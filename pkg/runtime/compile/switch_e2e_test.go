package compile

//@feature switch — M1 端到端:switch 源码 → frontend → IR → Project → spec

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func TestSwitchEndToEndMinimalChain(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(string input) -> string {
    switch (input) -> r {
        "support": {
            return "supported:" + r.reason;
        }
        "billing": {
            return "billing:" + r.classificationId;
        }
        default: {
            return "default:" + r.reason;
        }
    }
}
`
	res := passes.DefaultPipeline().Run(src, "switch.ncoda", nil)
	if res.HasErrors() || res.Program == nil {
		t.Fatalf("frontend errors: %v", res.Errors())
	}
	g, berr := ir.Build(res.Program)
	if berr != nil {
		t.Fatalf("ir-build: %v", berr)
	}
	// switch：断言 IR 含 SwitchStmt 节点
	found := false
	for _, st := range g.Root.Stmts {
		if _, ok := st.(*ir.SwitchStmt); ok {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected SwitchStmt in IR Root for switch source; stmts=%v", g.Root.Stmts)
	}
	p, perr := flow.Project(g)
	if perr != nil {
		t.Fatalf("project: %v", perr)
	}
	if _, err := spec.Build(g, p); err != nil {
		t.Fatalf("spec build: %v", err)
	}
}
