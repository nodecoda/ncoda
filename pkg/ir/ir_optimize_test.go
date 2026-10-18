package ir_test

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
)

// L4.5 contract: a phi is a representation of a consumed mutually-exclusive
// value, not a side effect of seeing an assignment.  A record phi whose merge
// output has no consumer must not reach lowering; its pure synthetic wrapper
// candidates are dead with it.
func TestBuildDropsUnconsumedRecordPhi(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
type RT1 = {
  string a;
};
function main(string x) -> string {
    if (x == "a") {
        RT1 v1;
        v1 = { "a": "" };
        if (x == "b") {
            v1 = { "a": "then" };
        } else {
            v1 = { "a": "else" };
        }
    }
    return "ok";
}
`
	g := buildIR(t, src)
	outer := firstIf(t, g.Root)
	inner := firstIf(t, outer.Then)
	if inner.Merge != nil {
		t.Fatalf("unconsumed record phi reached lowering: %#v", inner.Merge)
	}
}

// The same rule must not delete a phi that has a real consumption point.  This
// is the recursive-safety guard: liveness, not presence of assignments, decides.
func TestBuildKeepsConsumedRecordPhi(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
type RT1 = {
  string a;
};
function main(string x) -> string {
    RT1 v1;
    v1 = { "a": "" };
    if (x == "b") {
        v1 = { "a": "then" };
    } else {
        v1 = { "a": "else" };
    }
    return v1.a;
}
`
	g := buildIR(t, src)
	node := firstIf(t, g.Root)
	if node.Merge == nil {
		t.Fatal("consumed record phi was dropped")
	}
	// 迁移记录（2026-09-24，记录整值折叠 R-43 §42.3 A 轴）：`RT1{string a}` 是
	// 单叶字段记录且整值语义不可观察 ⇒ 折成**叶单元**，单元名按 C 轴取源变量名
	// （`v1`），该单元的候选落在 `Refs`（单组形态，组名 = 变量名见 RefName）。
	// 断言对象不变：**被消费的 φ 必须保住两条臂的候选**。
	found := len(node.Merge.Groups) == 0 && node.Merge.RefName == "v1" && len(node.Merge.Refs) == 2
	for _, group := range node.Merge.Groups {
		if group.Name == "v1" && len(group.Refs) == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("consumed record phi lost its candidates: %#v", node.Merge)
	}
}

// Output templates carry P1 data references.  A merge consumed only by an
// output placeholder is live and must survive optimization.
func TestBuildKeepsAggregateConsumedByOutputTemplate(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(string outer, string inner) {
    string agg;
    agg = "";
    if (outer == "true") {
        if (inner == "true") {
            return "success";
        } else {
            agg = "inner failed";
        }
    } else {
        agg = "outer failed";
    }
    output(agg);
}
`
	g := buildIR(t, src)
	if len(nodesOf(g)[ir.KindAggregate]) == 0 {
		t.Fatal("aggregate consumed by output template was dropped")
	}
}

// A dead phi is dead scaffolding, not permission to delete work.  A plugin,
// LLM, HTTP or user-code producer in an unconsumed branch must survive.
func TestBuildKeepsSideEffectProducerFromDeadPhi(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(string x) -> string {
    if (x == "a") {
        string v1;
        v1 = "";
        if (x == "b") {
            v1 = llm("openai_api_compatible/gpt-5.4", { "messages": [{ "role": "user", "content": x }] }).text;
        } else {
            v1 = "else";
        }
    }
    return "ok";
}
`
	g := buildIR(t, src)
	if len(nodesOf(g)[ir.KindLLM]) != 1 {
		t.Fatal("side-effect producer was deleted with an unconsumed phi")
	}
}

func firstIf(t *testing.T, b *ir.Block) *ir.IfStmt {
	t.Helper()
	if b == nil {
		t.Fatal("nil block")
	}
	for _, s := range b.Stmts {
		if node, ok := s.(*ir.IfStmt); ok {
			return node
		}
	}
	t.Fatalf("no if statement in block with %d stmts", len(b.Stmts))
	return nil
}

// Container output sources are real consumers of a phi: `yield selected`
// publishes the loop port from that aggregate even though the source text
// names a synthetic hoisted variable.  The optimizer must scan structured
// container outputs before deciding liveness.
func TestBuildKeepsPhiConsumedByLoopOutputSource(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
type RT1 = {
  string a;
};
function main(bool choose) {
    let agg = for (item in [1, 2]) {
        RT1 v1;
        v1 = { "a": "" };
        if (choose == true) {
            v1 = { "a": "then" };
        } else {
            v1 = { "a": "else" };
        }
        yield v1;
    };
    return agg;
}
`
	g := buildIR(t, src)
	loop := firstLoop(t, g.Root)
	var phi *ir.Aggregate
	for _, s := range loop.Body.Stmts {
		if node, ok := s.(*ir.IfStmt); ok {
			phi = node.Merge
			break
		}
	}
	if phi == nil {
		t.Fatal("phi consumed by loop output source was dropped")
	}
}

func firstLoop(t *testing.T, b *ir.Block) *ir.LoopStmt {
	t.Helper()
	for _, s := range b.Stmts {
		if node, ok := s.(*ir.LoopStmt); ok {
			return node
		}
	}
	t.Fatalf("no loop statement in block with %d stmts", len(b.Stmts))
	return nil
}

// Container traversal must be recursive for every structured container.  A
// loop output inside another loop is still a real consumption point; missing
// it would only make nested roundtrips fail at a deeper recursion level.
func TestBuildKeepsPhiConsumedByNestedLoopOutputSource(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
type RT1 = {
  string a;
};
function main(bool choose) {
    let agg = for (outer in [1, 2]) {
        let inner = for (item in [1, 2]) {
            RT1 v1;
            v1 = { "a": "" };
            if (choose == true) {
                v1 = { "a": "then" };
            } else {
                v1 = { "a": "else" };
            }
            yield v1;
        };
        yield inner;
    };
    return agg;
}
`
	g := buildIR(t, src)
	outerLoop := firstLoop(t, g.Root)
	innerLoop := firstLoop(t, outerLoop.Body)
	var phi *ir.Aggregate
	for _, s := range innerLoop.Body.Stmts {
		if node, ok := s.(*ir.IfStmt); ok {
			phi = node.Merge
			break
		}
	}
	if phi == nil {
		t.Fatal("phi consumed by nested loop output source was dropped")
	}
}

// A switch classification query is a real data consumer.  The optimizer's
// consumer scan must cover every node kind carrying a Source, not only the
// legacy call kinds.
func TestBuildKeepsPhiConsumedBySwitchQuery(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(bool choose) -> string {
    string query;
    if (choose == true) {
        query = "yes";
    } else {
        query = "no";
    }
    switch (query) {
        "yes": { return "yes"; }
        default: { return "?"; }
    }
}
`
	g := buildIR(t, src)
	if firstIf(t, g.Root).Merge == nil {
		t.Fatal("phi consumed by switch query was dropped")
	}
}

// Agent queries are Source consumers like every other call parameter.  This
// uses the registry-backed agent contract so the test covers the real IR
// builder rather than hand-assembling an incomplete graph.
func TestBuildKeepsPhiConsumedByAgentQuery(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(bool choose) -> string {
    string query;
    if (choose == true) {
        query = "hello";
    } else {
        query = "fallback";
    }
    let result = agent("openai/gpt-4o", query, {
        profile: "assistant",
        instruction: "You are a helpful assistant",
        tools: []
    });
    return result.text;
}
`
	profile := passes.DefaultTargetProfile()
	toolProfile, err := binding.NewAgentToolTargetProfile(
		"weather", "wx", "WeatherProvider", "builtin", "plugin-weather", "get_weather", nil)
	if err != nil {
		t.Fatalf("NewAgentToolTargetProfile: %v", err)
	}
	agentProfile, err := binding.NewAgentTargetProfile(
		"assistant", "provider-name", "ReActAgentStrategy", "ReAct Agent",
		"plugin-strategy", "2",
		[]binding.AgentParameterTarget{
			{Name: "model", ParameterType: "string", Required: true},
			{Name: "query", ParameterType: "string", Required: true},
			{Name: "instruction", ParameterType: "string", Required: false},
			{Name: "tools", ParameterType: "array", Required: true},
		},
		map[string]*binding.AgentToolTargetProfile{"weather": toolProfile})
	if err != nil {
		t.Fatalf("NewAgentTargetProfile: %v", err)
	}
	profile.AgentProfiles["assistant"] = agentProfile
	res := passes.DefaultPipeline().Run(src, "t.dc", profile)
	if res.HasErrors() {
		t.Fatalf("frontend pipeline reported errors: %v", res.Diagnostics)
	}
	g, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("ir.Build: %v", err)
	}
	if firstIf(t, g.Root).Merge == nil {
		t.Fatal("phi consumed by agent query was dropped")
	}
}

// Loop inputs are data consumers.  A phi feeding a for-in source must not be
// deleted just because the Loop is represented by a container statement.
func TestBuildKeepsPhiConsumedByLoopInput(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(bool choose) -> array<string> {
    array<string> values;
    if (choose == true) {
        values = ["yes"];
    } else {
        values = ["no"];
    }
    let agg = for (item in values) {
        yield item;
    };
    return agg;
}
`
	g := buildIR(t, src)
	if firstIf(t, g.Root).Merge == nil {
		t.Fatal("phi consumed by loop input was dropped")
	}
}
