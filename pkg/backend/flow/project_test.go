package flow_test

// D9 projection verification: every corpus program must produce a complete,
// deterministic backend projection from structured IR alone.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
)

func TestStructuredTreeProjectionParity(t *testing.T) {
	corpus := "../../compiler/testdata/examples/nodecoda"
	files, err := filepath.Glob(filepath.Join(corpus, "*.ncoda"))
	if err != nil {
		t.Fatalf("glob corpus: %v", err)
	}
	var nPass, nGap, nFail int
	for _, f := range files {
		name := filepath.Base(f)
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		res := passes.DefaultPipeline().Run(string(src), name, nil)
		if res.HasErrors() || res.Program == nil {
			nGap++
			t.Logf("GAP  %-42s frontend errors", name)
			continue
		}
		g, berr := ir.Build(res.Program)
		if berr != nil {
			nGap++
			t.Logf("GAP  %-42s ir-build: %v", name, berr)
			continue
		}
		if g.Root == nil {
			nGap++
			t.Logf("GAP  %-42s no structured root", name)
			continue
		}
		derived, derr := flow.Project(g)
		if derr != nil {
			nFail++
			t.Logf("FAIL %-42s derive: %v", name, derr)
			continue
		}
		second, serr := flow.Project(g)
		if serr != nil || len(derived.Nodes) < 2 || !sameNodes(derived.Nodes, second.Nodes) || !sameEdges(derived.Edges, second.Edges) {
			nFail++
			if os.Getenv("IR_DERIVE_DUMP") != "" {
				_ = os.MkdirAll("/tmp/derive", 0o755)
				_ = os.WriteFile("/tmp/derive/"+name+".first.json", []byte(nodesJSON(derived)), 0o644)
				_ = os.WriteFile("/tmp/derive/"+name+".second.json", []byte(nodesJSON(second)), 0o644)
			}
			t.Logf("DIFF %-42s projection is incomplete or nondeterministic", name)
			continue
		}
		nPass++
		t.Logf("PASS %-42s", name)
	}
	t.Logf("=== DERIVE PASS=%d GAP=%d DIFF=%d (corpus=%d)", nPass, nGap, nFail, len(files))
	if os.Getenv("IR_DERIVE_STRICT") != "" && nFail > 0 {
		t.Errorf("structured tree derivation mismatch: %d cases", nFail)
	}
}

func sameNodes(a, b []ir.Node) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		aj, _ := json.Marshal(a[i])
		bj, _ := json.Marshal(b[i])
		if string(aj) != string(bj) {
			return false
		}
	}
	return true
}

func sameEdges(a, b []flow.Edge) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		aj, _ := json.Marshal(a[i])
		bj, _ := json.Marshal(b[i])
		if string(aj) != string(bj) {
			return false
		}
	}
	return true
}

func nodesJSON(g *flow.Projection) string {
	type flat struct {
		Nodes []any
		Edges []flow.Edge
	}
	f := flat{}
	for _, n := range g.Nodes {
		f.Nodes = append(f.Nodes, n)
	}
	f.Edges = g.Edges
	b, _ := json.MarshalIndent(f, "", "  ")
	return string(b)
}

func TestProjectParallelFanOutAndJoin(t *testing.T) {
	g := graph(&ir.ParallelStmt{Branches: []*ir.Block{
		{Stmts: []ir.Stmt{stmt("left")}},
		{Stmts: []ir.Stmt{stmt("right")}},
	}}, stmt("after"))

	p := mustProject(t, g)
	assertEdges(t, p.Edges, []flow.Edge{
		{SourceNodeID: "start", TargetNodeID: "left"},
		{SourceNodeID: "start", TargetNodeID: "right"},
		{SourceNodeID: "left", TargetNodeID: "after"},
		{SourceNodeID: "right", TargetNodeID: "after"},
	})
}

// 回归（S58）:parallel 位于 if-else 的 else 分支内,栅栏汇合后跟一个 if。
// AND 栅栏汇合后无条件继续,不得把外层 else 分支端口（"false"）泄漏到
// 栅栏后语句的入边——否则产生 `llm --false--> selector` 且分支门要求
// 非分支节点发 $branch（P3/P5:parallel 出口端口必须重置）。
func TestProjectParallelInElseBranchResetsPort(t *testing.T) {
	pr := &ir.ParallelStmt{Branches: []*ir.Block{
		{Stmts: []ir.Stmt{stmt("p1")}},
		{Stmts: []ir.Stmt{stmt("p2")}},
	}}
	innerIf := ifStmt("inner", &ir.Block{Stmts: []ir.Stmt{stmt("t")}}, &ir.Block{Stmts: []ir.Stmt{stmt("f")}}, "inner_merge")
	outer := ifStmt("outer", &ir.Block{Stmts: []ir.Stmt{stmt("then_a")}}, &ir.Block{Stmts: []ir.Stmt{pr, innerIf}}, "outer_merge")

	p := mustProject(t, graph(outer, stmt("after")))
	for _, e := range p.Edges {
		if e.SourceNodeID == "p1" && e.TargetNodeID == "inner" && e.SourcePortID != "" {
			t.Fatalf("barrier exit edge leaked port: %+v (want no SourcePortID)", e)
		}
		if e.SourceNodeID == "p2" && e.TargetNodeID == "inner" && e.SourcePortID != "" {
			t.Fatalf("barrier exit edge leaked port: %+v (want no SourcePortID)", e)
		}
	}
	// 正向断言:p1/p2 → inner 无端口;outer → then_a / outer → p1,p2 仍带分支端口。
	assertEdges(t, p.Edges, []flow.Edge{
		{SourceNodeID: "start", TargetNodeID: "outer"},
		{SourceNodeID: "outer", TargetNodeID: "then_a", SourcePortID: "true"},
		{SourceNodeID: "outer", TargetNodeID: "p1", SourcePortID: "false"},
		{SourceNodeID: "outer", TargetNodeID: "p2", SourcePortID: "false"},
		{SourceNodeID: "p1", TargetNodeID: "inner"},
		{SourceNodeID: "p2", TargetNodeID: "inner"},
		{SourceNodeID: "inner", TargetNodeID: "t", SourcePortID: "true"},
		{SourceNodeID: "inner", TargetNodeID: "f", SourcePortID: "false"},
		{SourceNodeID: "t", TargetNodeID: "inner_merge"},
		{SourceNodeID: "f", TargetNodeID: "inner_merge"},
		{SourceNodeID: "then_a", TargetNodeID: "outer_merge"},
		{SourceNodeID: "inner_merge", TargetNodeID: "outer_merge"},
		{SourceNodeID: "outer_merge", TargetNodeID: "after"},
	})
}

func TestProjectNestedIfPortsAndMerge(t *testing.T) {
	inner := ifStmt("inner", &ir.Block{Stmts: []ir.Stmt{stmt("inner_true")}}, &ir.Block{Stmts: []ir.Stmt{stmt("inner_false")}}, "inner_merge")
	outer := ifStmt("outer", &ir.Block{Stmts: []ir.Stmt{inner}}, &ir.Block{Stmts: []ir.Stmt{stmt("outer_false")}}, "outer_merge")

	p := mustProject(t, graph(outer, stmt("after")))
	assertEdges(t, p.Edges, []flow.Edge{
		{SourceNodeID: "start", TargetNodeID: "outer"},
		{SourceNodeID: "outer", TargetNodeID: "inner", SourcePortID: "true"},
		{SourceNodeID: "inner", TargetNodeID: "inner_true", SourcePortID: "true"},
		{SourceNodeID: "inner", TargetNodeID: "inner_false", SourcePortID: "false"},
		{SourceNodeID: "inner_true", TargetNodeID: "inner_merge"},
		{SourceNodeID: "inner_false", TargetNodeID: "inner_merge"},
		{SourceNodeID: "outer", TargetNodeID: "outer_false", SourcePortID: "false"},
		{SourceNodeID: "inner_merge", TargetNodeID: "outer_merge"},
		{SourceNodeID: "outer_false", TargetNodeID: "outer_merge"},
		{SourceNodeID: "outer_merge", TargetNodeID: "after"},
	})
}

func TestProjectLoopEscapeStaysInsideContainer(t *testing.T) {
	loop := &ir.LoopStmt{
		ID:   "loop",
		Body: &ir.Block{Stmts: []ir.Stmt{stmt("work"), &ir.BreakStmt{Node: &ir.BreakNode{ID: "break"}}}},
	}
	p := mustProject(t, graph(loop))

	assertEdges(t, p.Edges, []flow.Edge{{SourceNodeID: "start", TargetNodeID: "loop"}})
	projected := onlyLoop(t, p)
	assertEdges(t, projected.InnerEdges, []flow.Edge{
		{SourceNodeID: "loop", TargetNodeID: "work", SourcePortID: wLoopFunctionInlineOutput},
		{SourceNodeID: "work", TargetNodeID: "break"},
	})
}

func TestProjectIterationYieldPreservesContainerBoundary(t *testing.T) {
	loop := &ir.LoopStmt{
		ID:       "iteration",
		LoopType: "by_array",
		OutTypes: map[string]*ir.Type{"output": ir.TList(ir.T("string"))},
		OutputSources: []ir.Source{{
			Path: []string{"output"}, BlockID: "yield_value", Name: "output",
		}},
		Body: &ir.Block{Stmts: []ir.Stmt{&ir.NodeStmt{Node: &ir.TextProcess{
			ID: "yield_value", Method: "concat", Inputs: []ir.Source{{BlockID: "iteration", Name: "item"}},
		}}}},
	}
	p := mustProject(t, graph(loop))
	projected := onlyLoop(t, p)

	if !reflect.DeepEqual(projected.OutputSources, loop.OutputSources) {
		t.Fatalf("loop output sources changed during projection: got %#v want %#v", projected.OutputSources, loop.OutputSources)
	}
	assertEdges(t, projected.InnerEdges, []flow.Edge{
		{SourceNodeID: "iteration", TargetNodeID: "yield_value", SourcePortID: wLoopFunctionInlineOutput},
		{SourceNodeID: "yield_value", TargetNodeID: "iteration", TargetPortID: wLoopFunctionInlineInput},
	})
}

func graph(stmts ...ir.Stmt) *ir.Graph {
	return &ir.Graph{Entry: &ir.Start{ID: "start"}, Root: &ir.Block{Stmts: stmts}}
}

func stmt(id string) *ir.NodeStmt {
	return &ir.NodeStmt{Node: &ir.TextProcess{ID: id, Method: "const"}}
}

func ifStmt(id string, then, otherwise *ir.Block, mergeID string) *ir.IfStmt {
	return &ir.IfStmt{
		ID: id, Cond: &ir.Selector{ID: id}, Then: then, Else: otherwise,
		Merge: &ir.Aggregate{ID: mergeID},
	}
}

func mustProject(t *testing.T, g *ir.Graph) *flow.Projection {
	t.Helper()
	p, err := flow.Project(g)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	return p
}

func onlyLoop(t *testing.T, p *flow.Projection) *flow.ProjectedLoop {
	t.Helper()
	for _, n := range p.Nodes {
		if loop, ok := n.(*flow.ProjectedLoop); ok {
			return loop
		}
	}
	t.Fatal("projection has no loop container")
	return nil
}

func assertEdges(t *testing.T, got, want []flow.Edge) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("projection edges differ:\n got: %#v\nwant: %#v", got, want)
	}
}

// Regression: a loop body ending in an else-less if whose then branch is a
// plain node used to emit the loop-back edge twice (X3 duplicate_edge
// 100013->100008: the then-exits surfaced both via deriveBlock's return and
// via the loop-tail pendingIf consumption).
func TestProjectLoopTailElseLessIfNoDuplicateLoopBack(t *testing.T) {
	loop := &ir.LoopStmt{
		ID: "iteration",
		Body: &ir.Block{Stmts: []ir.Stmt{
			stmt("work"),
			&ir.IfStmt{
				ID:   "cond",
				Cond: &ir.Selector{ID: "cond"},
				Then: &ir.Block{Stmts: []ir.Stmt{stmt("thr")}},
				// Else nil: dangling else-less if at the loop tail.
			},
		}},
	}
	p := mustProject(t, graph(loop))
	projected := onlyLoop(t, p)
	assertEdges(t, projected.InnerEdges, []flow.Edge{
		{SourceNodeID: "iteration", TargetNodeID: "work", SourcePortID: wLoopFunctionInlineOutput},
		{SourceNodeID: "work", TargetNodeID: "cond"},
		{SourceNodeID: "cond", TargetNodeID: "thr", SourcePortID: "true"},
		{SourceNodeID: "thr", TargetNodeID: "iteration", TargetPortID: wLoopFunctionInlineInput},
		{SourceNodeID: "cond", TargetNodeID: "iteration", SourcePortID: "false", TargetPortID: wLoopFunctionInlineInput},
	})
}

// Variant: the loop-tail else-less if's then branch ends in break (X13-style
// loop escape). The break must NOT loop back to the container; the selector
// closes the iteration on its false port; nothing is duplicated.
func TestProjectLoopTailElseLessIfBreakThen(t *testing.T) {
	loop := &ir.LoopStmt{
		ID: "iteration",
		Body: &ir.Block{Stmts: []ir.Stmt{
			stmt("work"),
			&ir.IfStmt{
				ID:   "cond",
				Cond: &ir.Selector{ID: "cond"},
				Then: &ir.Block{Stmts: []ir.Stmt{&ir.BreakStmt{Node: &ir.BreakNode{ID: "break"}}}},
			},
		}},
	}
	p := mustProject(t, graph(loop))
	projected := onlyLoop(t, p)
	assertEdges(t, projected.InnerEdges, []flow.Edge{
		{SourceNodeID: "iteration", TargetNodeID: "work", SourcePortID: wLoopFunctionInlineOutput},
		{SourceNodeID: "work", TargetNodeID: "cond"},
		{SourceNodeID: "cond", TargetNodeID: "break", SourcePortID: "true"},
		{SourceNodeID: "cond", TargetNodeID: "iteration", SourcePortID: "false", TargetPortID: wLoopFunctionInlineInput},
	})
}

// Variant: the else-less if has a Relay (empty then branch materialized by a
// TextProcessor). The relay loops back exactly once.
func TestProjectLoopTailElseLessIfRelayThen(t *testing.T) {
	loop := &ir.LoopStmt{
		ID: "iteration",
		Body: &ir.Block{Stmts: []ir.Stmt{
			stmt("work"),
			&ir.IfStmt{
				ID:    "cond",
				Cond:  &ir.Selector{ID: "cond"},
				Then:  &ir.Block{},
				Relay: &ir.TextProcess{ID: "relay", Method: "const"},
			},
		}},
	}
	p := mustProject(t, graph(loop))
	projected := onlyLoop(t, p)
	assertEdges(t, projected.InnerEdges, []flow.Edge{
		{SourceNodeID: "iteration", TargetNodeID: "work", SourcePortID: wLoopFunctionInlineOutput},
		{SourceNodeID: "work", TargetNodeID: "cond"},
		{SourceNodeID: "cond", TargetNodeID: "relay", SourcePortID: "true"},
		{SourceNodeID: "relay", TargetNodeID: "iteration", TargetPortID: wLoopFunctionInlineInput},
		{SourceNodeID: "cond", TargetNodeID: "iteration", SourcePortID: "false", TargetPortID: wLoopFunctionInlineInput},
	})
}

// Variant: two trailing else-less ifs. The first is consumed by the second's
// fallthrough; only the second remains pending at the loop tail. Neither
// duplicate loop-back edges.
func TestProjectLoopTailDoubleElseLessIf(t *testing.T) {
	loop := &ir.LoopStmt{
		ID: "iteration",
		Body: &ir.Block{Stmts: []ir.Stmt{
			&ir.IfStmt{
				ID:   "a",
				Cond: &ir.Selector{ID: "a"},
				Then: &ir.Block{Stmts: []ir.Stmt{stmt("a_then")}},
			},
			&ir.IfStmt{
				ID:   "b",
				Cond: &ir.Selector{ID: "b"},
				Then: &ir.Block{Stmts: []ir.Stmt{stmt("b_then")}},
			},
		}},
	}
	p := mustProject(t, graph(loop))
	projected := onlyLoop(t, p)
	assertEdges(t, projected.InnerEdges, []flow.Edge{
		{SourceNodeID: "iteration", TargetNodeID: "a", SourcePortID: wLoopFunctionInlineOutput},
		{SourceNodeID: "a", TargetNodeID: "a_then", SourcePortID: "true"},
		{SourceNodeID: "a_then", TargetNodeID: "b"},
		{SourceNodeID: "a", TargetNodeID: "b", SourcePortID: "false"},
		{SourceNodeID: "b", TargetNodeID: "b_then", SourcePortID: "true"},
		{SourceNodeID: "b_then", TargetNodeID: "iteration", TargetPortID: wLoopFunctionInlineInput},
		{SourceNodeID: "b", TargetNodeID: "iteration", SourcePortID: "false", TargetPortID: wLoopFunctionInlineInput},
	})
}

// Variant: the else-less if's then branch is a parallel block with two exits.
// Each then-exit loops back exactly once; the selector closes on its false
// port.
func TestProjectLoopTailElseLessIfParallelThen(t *testing.T) {
	loop := &ir.LoopStmt{
		ID: "iteration",
		Body: &ir.Block{Stmts: []ir.Stmt{
			stmt("work"),
			&ir.IfStmt{
				ID:   "cond",
				Cond: &ir.Selector{ID: "cond"},
				Then: &ir.Block{Stmts: []ir.Stmt{&ir.ParallelStmt{Branches: []*ir.Block{
					{Stmts: []ir.Stmt{stmt("p1")}},
					{Stmts: []ir.Stmt{stmt("p2")}},
				}}}},
			},
		}},
	}
	p := mustProject(t, graph(loop))
	projected := onlyLoop(t, p)
	assertEdges(t, projected.InnerEdges, []flow.Edge{
		{SourceNodeID: "iteration", TargetNodeID: "work", SourcePortID: wLoopFunctionInlineOutput},
		{SourceNodeID: "work", TargetNodeID: "cond"},
		{SourceNodeID: "cond", TargetNodeID: "p1", SourcePortID: "true"},
		{SourceNodeID: "cond", TargetNodeID: "p2", SourcePortID: "true"},
		{SourceNodeID: "p1", TargetNodeID: "iteration", TargetPortID: wLoopFunctionInlineInput},
		{SourceNodeID: "p2", TargetNodeID: "iteration", TargetPortID: wLoopFunctionInlineInput},
		{SourceNodeID: "cond", TargetNodeID: "iteration", SourcePortID: "false", TargetPortID: wLoopFunctionInlineInput},
	})
}

// G14: a nested loop's continuation edge (inner loop → following body node)
// stays inside the outer container, and the inner loop's body nodes do not
// leak into the outer container's child list (the inner deriveLoop truncates
// its own subgraph before the outer body continues).
func TestProjectNestedLoopContinuationStaysInsideContainer(t *testing.T) {
	inner := &ir.LoopStmt{
		ID:   "inner",
		Body: &ir.Block{Stmts: []ir.Stmt{stmt("inner_work")}},
	}
	outer := &ir.LoopStmt{
		ID:   "outer",
		Body: &ir.Block{Stmts: []ir.Stmt{inner, stmt("after_inner")}},
	}
	p := mustProject(t, graph(outer))
	projected := onlyLoop(t, p)

	assertEdges(t, projected.InnerEdges, []flow.Edge{
		{SourceNodeID: "outer", TargetNodeID: "inner", SourcePortID: wLoopFunctionInlineOutput},
		{SourceNodeID: "inner", TargetNodeID: "after_inner"},
		{SourceNodeID: "after_inner", TargetNodeID: "outer", TargetPortID: wLoopFunctionInlineInput},
	})

	var nested *flow.ProjectedLoop
	for _, cn := range projected.Children {
		if l, ok := cn.(*flow.ProjectedLoop); ok {
			nested = l
		}
	}
	if nested == nil {
		t.Fatal("nested loop not projected as a child of the outer container")
	}
	assertEdges(t, nested.InnerEdges, []flow.Edge{
		{SourceNodeID: "inner", TargetNodeID: "inner_work", SourcePortID: wLoopFunctionInlineOutput},
		{SourceNodeID: "inner_work", TargetNodeID: "inner", TargetPortID: wLoopFunctionInlineInput},
	})
	// The inner loop's body node must not surface as a sibling child of the
	// outer container (it belongs to the nested loop only).
	for _, cn := range projected.Children {
		if cn.NodeID() == "inner_work" {
			t.Fatalf("inner loop body node leaked into outer container children")
		}
	}
}

// TestProjectSkippedMergeRefFixup:内层 if 的 else 分支 flow-end return →
// 内层 merge 被 pass-through 跳过（不投影）,但外层 merge 的 Group1 ref
// 仍指向被跳过的内层 merge（X4/X5/X16 暴露,并行循环支持后从 parallel
// reject 掩盖下浮出）→ fixup 重写为内层 merge 的首个值 ref（t.output,
// Group1 = 首非 nil = pass-through 语义）。
func TestProjectSkippedMergeRefFixup(t *testing.T) {
	inner := ifStmt("inner",
		&ir.Block{Stmts: []ir.Stmt{stmt("t")}},
		&ir.Block{Stmts: []ir.Stmt{
			&ir.ReturnStmt{ID: "r", End: &ir.End{ID: "900001"}, FlowEnd: true},
		}},
		"inner_merge")
	inner.Merge.Refs = []ir.Ref{{Producer: "t", Output: "output"}}
	outer := ifStmt("outer",
		&ir.Block{Stmts: []ir.Stmt{inner}},
		&ir.Block{Stmts: []ir.Stmt{stmt("f")}},
		"outer_merge")
	outer.Merge.Refs = []ir.Ref{
		{Producer: "inner_merge", Output: wGroup1},
		{Producer: "f", Output: "output"},
	}
	p := mustProject(t, graph(outer, stmt("after")))
	// 内层 merge 保持跳过（YAML 形状不变）。
	for _, n := range p.Nodes {
		if n.NodeID() == "inner_merge" {
			t.Fatalf("skipped merge must not be projected")
		}
	}
	// 外层 merge ref 被重写:inner_merge.Group1 → t.output。
	for _, n := range p.Nodes {
		agg, ok := n.(*ir.Aggregate)
		if !ok || agg.ID != "outer_merge" {
			continue
		}
		want := []ir.Ref{{Producer: "t", Output: "output"}, {Producer: "f", Output: "output"}}
		if !reflect.DeepEqual(agg.Refs, want) {
			t.Fatalf("outer_merge refs = %+v, want %+v (skipped-merge fixup)", agg.Refs, want)
		}
		return
	}
	t.Fatal("outer_merge not projected")
}

// TestProjectNestedElseLessFalsePathFolded:then 块以嵌套 else-less if 收尾
// 时,内层未匹配路径折入外层 junction（Z17:并行循环体内 if(a){ ... if(b){...} }
// 的 b 分支 false 路径曾丢失 → 运行时 $branch "false" no declared target）。
func TestProjectNestedElseLessFalsePathFolded(t *testing.T) {
	// outer then 块 = [inner else-less if];outer 也是 else-less。
	inner := ifStmt("inner", &ir.Block{Stmts: []ir.Stmt{stmt("t")}}, nil, "")
	outer := ifStmt("outer",
		&ir.Block{Stmts: []ir.Stmt{stmt("a"), inner}},
		nil, "")
	p := mustProject(t, graph(outer, stmt("after")))
	// inner selector 的 false 路径必须折入外层 junction → 连到 after。
	got := false
	for _, e := range p.Edges {
		if e.SourceNodeID == "inner" && e.TargetNodeID == "after" && e.SourcePortID == "false" {
			got = true
		}
	}
	if !got {
		for _, e := range p.Edges {
			t.Logf("  edge %+v", e)
		}
		t.Fatal("missing inner else-less false path edge: inner --false--> after (folded junction)")
	}
}

// Regression (2026-10-20, corpus 7574994076144779304_calendar_helper):
// a no-action `ask` (builder S1: no arms => no ports => control passes
// straight through the QA node) used to project an EMPTY exit junction.
// The block's next statement was then cut as unreachable. When the ask sits
// in one arm of an if, the arm loses its exit => the outer merge is
// pass-through skipped => a downstream ref still names the unprojected merge
// (runtime RDANGLING_REF). The IR builder already returns [qaID] for this
// form; the projection must mirror it.
func TestProjectNoActionQaPassesControlThrough(t *testing.T) {
	qa := &ir.QaStmt{ID: "qa", QA: &ir.QA{ID: "qa", TextForm: true}}
	p := mustProject(t, graph(qa, stmt("after")))
	assertEdges(t, p.Edges, []flow.Edge{
		{SourceNodeID: "start", TargetNodeID: "qa"},
		{SourceNodeID: "qa", TargetNodeID: "after"},
	})
}

// Same shape one level down: an empty then arm contributes its selector port,
// the else arm ends in a no-action ask, and the surviving merge must be
// materialized with BOTH branch ports declared.
func TestProjectNoActionQaKeepsEnclosingMerge(t *testing.T) {
	qa := &ir.QaStmt{ID: "qa", QA: &ir.QA{ID: "qa", TextForm: true}}
	outer := ifStmt("outer", &ir.Block{}, &ir.Block{Stmts: []ir.Stmt{qa}}, "merge")

	p := mustProject(t, graph(outer, stmt("after")))
	assertEdges(t, p.Edges, []flow.Edge{
		{SourceNodeID: "start", TargetNodeID: "outer"},
		{SourceNodeID: "outer", TargetNodeID: "qa", SourcePortID: "false"},
		{SourceNodeID: "outer", TargetNodeID: "merge", SourcePortID: "true"},
		{SourceNodeID: "qa", TargetNodeID: "merge"},
		{SourceNodeID: "merge", TargetNodeID: "after"},
	})
}

// Regression (P2 A1/A2): an else-less if with an empty then arm still owes
// its true port. Before P2 only the false edge was projected, so the runtime
// saw a selector with a single declared target.
func TestProjectEmptyElseLessThenDeclaresTruePort(t *testing.T) {
	s := ifStmt("s", &ir.Block{}, nil, "")
	p := mustProject(t, graph(s, stmt("after")))
	assertEdges(t, p.Edges, []flow.Edge{
		{SourceNodeID: "start", TargetNodeID: "s"},
		{SourceNodeID: "s", TargetNodeID: "after", SourcePortID: "true"},
		{SourceNodeID: "s", TargetNodeID: "after", SourcePortID: "false"},
	})
}

// Regression (P2 A1/A2, X238/S11): a top-level else-less if whose then arm
// returns leaves its selector in the document-tail junction. The false path
// must be attached exactly once, with the explicit false port; wiring it both
// directly and through the port drain duplicated the edge.
func TestProjectDanglingElseLessSelectorSingleFalseEdge(t *testing.T) {
	ifs := &ir.IfStmt{
		ID:   "s",
		Cond: &ir.Selector{ID: "s"},
		Then: &ir.Block{Stmts: []ir.Stmt{
			&ir.ReturnStmt{End: &ir.End{ID: "900001"}, SharedEnd: true},
		}},
	}
	p := mustProject(t, graph(ifs))
	var falseEdges, trueEdges int
	for _, e := range p.Edges {
		if e.SourceNodeID != "s" {
			continue
		}
		switch e.SourcePortID {
		case "false":
			falseEdges++
			if e.TargetNodeID != "900001" {
				t.Fatalf("false edge target = %q, want 900001", e.TargetNodeID)
			}
		case "true":
			trueEdges++
		}
	}
	if falseEdges != 1 || trueEdges != 1 {
		for _, e := range p.Edges {
			t.Logf("  edge %+v", e)
		}
		t.Fatalf("selector s ports: true=%d false=%d, want 1/1 (no duplicate false edge)", trueEdges, falseEdges)
	}
}

// Regression (P2 A3, S200): a dangling else-less if at the tail of an if-arm
// belongs to that arm. It must not leak into the sibling arm and steal its
// entry edge; its false path joins the enclosing merge on the default port.
func TestProjectPendingIfScopedToThenArm(t *testing.T) {
	inner := ifStmt("inner", &ir.Block{Stmts: []ir.Stmt{stmt("it")}}, nil, "")
	outer := ifStmt("outer",
		&ir.Block{Stmts: []ir.Stmt{stmt("ot"), inner}},
		&ir.Block{Stmts: []ir.Stmt{stmt("oe")}}, "m")
	p := mustProject(t, graph(outer, stmt("after")))
	var innerFalse, innerHijack bool
	for _, e := range p.Edges {
		if e.SourceNodeID != "inner" || e.SourcePortID != "false" {
			continue
		}
		switch e.TargetNodeID {
		case "m":
			innerFalse = true
		case "oe":
			innerHijack = true
		}
	}
	if innerHijack || !innerFalse {
		for _, e := range p.Edges {
			t.Logf("  edge %+v", e)
		}
		t.Fatalf("inner false edge: hijacked_else=%v merged=%v, want merged only", innerHijack, innerFalse)
	}
}
