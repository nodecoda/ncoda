package binding

// Unit tests for the stable-ID user call graph (binder.py
// _build_call_graph) and the recursion-rejection helpers (call_graph.py):
// Tarjan SCC, lexically canonical cycle selection, minimal call-site choice
// and effect propagation with cycle defense.

import (
	"reflect"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/diag"
)

// buildGraph binds source and returns the program, snapshot and the
// published user call graph plus call sites.
func buildGraph(t *testing.T, source string) (*ast.Program, *Snapshot, map[FunctionID][]FunctionID, map[[2]FunctionID][]AstNodeID) {
	t.Helper()
	prog := parseSource(t, source)
	snap, diags := Bind(prog)
	for _, d := range diags {
		if d.Severity == diag.Error {
			t.Fatalf("unexpected bind error: %v", d)
		}
	}
	graph, sites := BuildUserCallGraph(prog, snap)
	return prog, snap, graph, sites
}

func sortedAdj(adj []FunctionID) []FunctionID {
	out := append([]FunctionID(nil), adj...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func TestBuildUserCallGraphDirectCall(t *testing.T) {
	_, snap, graph, sites := buildGraph(t, `
        function a() -> string { return b(); }
        function b() -> string { return "x"; }
        function main() -> string { let v = a(); return v; }
    `)
	if got := sortedAdj(graph[1]); !reflect.DeepEqual(got, []FunctionID{2}) {
		t.Fatalf("edges[a] = %v, want [b]", got)
	}
	if got := sortedAdj(graph[2]); len(got) != 0 {
		t.Fatalf("edges[b] = %v, want []", got)
	}
	if got := sortedAdj(graph[3]); !reflect.DeepEqual(got, []FunctionID{1}) {
		t.Fatalf("edges[main] = %v, want [a]", got)
	}
	edgeSites, ok := sites[[2]FunctionID{1, 2}]
	if !ok || len(edgeSites) != 1 {
		t.Fatalf("call sites for (a,b) = %v (ok=%v), want one site", edgeSites, ok)
	}
	if info, found := snap.AstNodes().Get(edgeSites[0]); !found || info.Kind != "CallExpr" {
		t.Fatalf("call site %d must name a CallExpr node (info=%+v, found=%v)", edgeSites[0], info, found)
	}
}

func TestBuildUserCallGraphForwardAndSelfCalls(t *testing.T) {
	_, _, graph, _ := buildGraph(t, `
        function a() -> string { return a(); }
        function b() -> string { return a(); }
        function main() -> string { return b(); }
    `)
	if got := sortedAdj(graph[1]); !reflect.DeepEqual(got, []FunctionID{1}) {
		t.Fatalf("edges[a] = %v, want self-loop [a]", got)
	}
	if got := sortedAdj(graph[2]); !reflect.DeepEqual(got, []FunctionID{1}) {
		t.Fatalf("edges[b] = %v, want [a]", got)
	}
	if got := sortedAdj(graph[3]); !reflect.DeepEqual(got, []FunctionID{2}) {
		t.Fatalf("edges[main] = %v, want [b]", got)
	}
}

func TestBuildUserCallGraphBuiltinCallIsNotAnEdge(t *testing.T) {
	_, _, graph, sites := buildGraph(t, `
        function main() -> string { let r = llm("g", {}); return r.text; }
    `)
	if got := sortedAdj(graph[1]); len(got) != 0 {
		t.Fatalf("edges[main] = %v, want [] (builtin calls are not user edges)", got)
	}
	if len(sites) != 0 {
		t.Fatalf("builtin call must not publish a call site, got %v", sites)
	}
}

func TestFindRecursiveCycleNone(t *testing.T) {
	graph := map[FunctionID][]FunctionID{1: {2}, 2: {3}, 3: {}}
	sites := map[[2]FunctionID][]AstNodeID{
		{1, 2}: {10}, {2, 3}: {11},
	}
	if cycle := FindRecursiveCycle(graph, sites); cycle != nil {
		t.Fatalf("acyclic graph must not report a cycle, got %+v", cycle)
	}
}

func TestFindRecursiveCycleDirect(t *testing.T) {
	graph := map[FunctionID][]FunctionID{1: {1}, 2: {}}
	sites := map[[2]FunctionID][]AstNodeID{
		{1, 1}: {7, 3},
	}
	cycle := FindRecursiveCycle(graph, sites)
	if cycle == nil {
		t.Fatal("self-loop must be reported as a cycle")
	}
	if !reflect.DeepEqual(cycle.Functions, []FunctionID{1}) {
		t.Fatalf("cycle functions = %v, want [1]", cycle.Functions)
	}
	if cycle.CallSite != 3 {
		t.Fatalf("cycle call site = %d, want 3 (smallest)", cycle.CallSite)
	}
}

func TestFindRecursiveCycleIndirect(t *testing.T) {
	graph := map[FunctionID][]FunctionID{
		1: {2}, 2: {1}, 3: {1},
	}
	sites := map[[2]FunctionID][]AstNodeID{
		{1, 2}: {4}, {2, 1}: {5}, {3, 1}: {6},
	}
	cycle := FindRecursiveCycle(graph, sites)
	if cycle == nil {
		t.Fatal("indirect cycle must be reported")
	}
	if !reflect.DeepEqual(cycle.Functions, []FunctionID{1, 2}) {
		t.Fatalf("cycle functions = %v, want [1 2]", cycle.Functions)
	}
	if cycle.CallSite != 4 {
		t.Fatalf("cycle call site = %d, want 4", cycle.CallSite)
	}
}

func TestFindRecursiveCycleLexicallySmallestComponent(t *testing.T) {
	graph := map[FunctionID][]FunctionID{
		30: {40}, 40: {30},
		10: {20}, 20: {10},
		99: {},
	}
	sites := map[[2]FunctionID][]AstNodeID{
		{30, 40}: {1}, {40, 30}: {2},
		{10, 20}: {3}, {20, 10}: {4},
	}
	cycle := FindRecursiveCycle(graph, sites)
	if cycle == nil {
		t.Fatal("a cyclic SCC exists; must be reported")
	}
	if !reflect.DeepEqual(cycle.Functions, []FunctionID{10, 20}) {
		t.Fatalf("cycle functions = %v, want lexically smallest [10 20]", cycle.Functions)
	}
	if cycle.CallSite != 3 {
		t.Fatalf("cycle call site = %d, want 3", cycle.CallSite)
	}
}

func TestFindRecursiveCycleExcludesAcyclicCallers(t *testing.T) {
	graph := map[FunctionID][]FunctionID{
		10: {10},
		99: {10},
	}
	sites := map[[2]FunctionID][]AstNodeID{
		{10, 10}: {2},
		{99, 10}: {1},
	}
	cycle := FindRecursiveCycle(graph, sites)
	if cycle == nil {
		t.Fatal("self-loop on 10 must be reported")
	}
	if !reflect.DeepEqual(cycle.Functions, []FunctionID{10}) {
		t.Fatalf("cycle functions = %v, want [10] (acyclic callers excluded)", cycle.Functions)
	}
	if cycle.CallSite != 2 {
		t.Fatalf("cycle call site = %d, want 2", cycle.CallSite)
	}
}

func TestPropagateFunctionEffectsTransitive(t *testing.T) {
	graph := map[FunctionID][]FunctionID{
		1: {2}, 2: {3}, 3: {},
	}
	direct := map[FunctionID]EffectKind{
		1: EffectCalculation,
		2: EffectCalculation,
		3: EffectWorkflowOperation,
	}
	got := PropagateFunctionEffects(graph, direct)
	want := map[FunctionID]EffectKind{
		1: EffectWorkflowOperation,
		2: EffectWorkflowOperation,
		3: EffectWorkflowOperation,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("propagated effects = %v, want %v", got, want)
	}
}

func TestPropagateFunctionEffectsAllCalculation(t *testing.T) {
	graph := map[FunctionID][]FunctionID{
		1: {2}, 2: {},
	}
	direct := map[FunctionID]EffectKind{1: EffectCalculation, 2: EffectCalculation}
	got := PropagateFunctionEffects(graph, direct)
	if got[1] != EffectCalculation || got[2] != EffectCalculation {
		t.Fatalf("all-calculation graph must stay calculation, got %v", got)
	}
}

func TestPropagateFunctionEffectsMissingDirectDefaultsToCalculation(t *testing.T) {
	graph := map[FunctionID][]FunctionID{1: {2}, 2: {}}
	got := PropagateFunctionEffects(graph, map[FunctionID]EffectKind{})
	if got[1] != EffectCalculation || got[2] != EffectCalculation {
		t.Fatalf("missing direct effects must default to calculation, got %v", got)
	}
}

func TestPropagateFunctionEffectsCyclePanics(t *testing.T) {
	graph := map[FunctionID][]FunctionID{1: {2}, 2: {1}}
	defer func() {
		if recover() == nil {
			t.Fatal("effect propagation over a cycle must panic (visiting guard)")
		}
	}()
	PropagateFunctionEffects(graph, map[FunctionID]EffectKind{})
}
