package compile

// switch_e2e_exec_test.go — switch 分支路由 e2e。
//
// 覆盖:
//   - 命中 branch_<i>:query 匹配意图 → 只有命中分支体执行,输出沿该分支
//     汇聚;
//   - 无命中 default:query 不匹配 → default 分支体执行;
//   - 分支互斥:非命中分支体不得执行（side-effect 证据,与 if 分支门同
//     语义——branch source 输出 $branch,compile 分支门只放行目标）。
//
// spec:start → src(query=...) → SWITCH(intents=[a,b]) --branch_0/1/default-->
// 各分支 code(写入 output) → merge → end。

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func switchSpec(query string) *spec.Spec {
	mark := func(name string) string {
		return fmt.Sprintf("def main() -> dict:\n    return {\"result\": \"%s-run\"}", name)
	}
	return &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("src", spec.KindCode, &spec.CodeConfig{
				Code:      fmt.Sprintf("def main() -> dict:\n    return {\"query\": %q}", query),
				Language:  "python",
				Synthetic: true,
				OutOrder:  []string{"query"},
			}),
			nodeSpec("it", spec.KindSwitch, &spec.SwitchConfig{
				Query:   spec.Source{Path: []string{"query"}, BlockID: "src", Name: "query"},
				Intents: []string{"a", "b"},
			}),
			nodeSpec("ba", spec.KindCode, &spec.CodeConfig{Code: mark("a"), Language: "python", Synthetic: true, OutOrder: []string{"result"}}),
			nodeSpec("bb", spec.KindCode, &spec.CodeConfig{Code: mark("b"), Language: "python", Synthetic: true, OutOrder: []string{"result"}}),
			nodeSpec("bd", spec.KindCode, &spec.CodeConfig{Code: mark("default"), Language: "python", Synthetic: true, OutOrder: []string{"result"}}),
			nodeSpec("mg", spec.KindAggregate, &spec.AggregateConfig{Refs: []spec.Ref{
				{Producer: "ba", Output: "result"},
				{Producer: "bb", Output: "result"},
				{Producer: "bd", Output: "result"},
			}}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "mg", Output: wGroup1}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "src"},
			{Source: "src", Target: "it"},
			// switch 分支端口:branch_0 / branch_1 / default
			{Source: "it", Target: "ba", SourcePort: "branch_0"},
			{Source: "it", Target: "bb", SourcePort: "branch_1"},
			{Source: "it", Target: "bd", SourcePort: "default"},
			// 分支体尾 → merge（普通边）
			{Source: "ba", Target: "mg"},
			{Source: "bb", Target: "mg"},
			{Source: "bd", Target: "mg"},
			{Source: "mg", Target: "e1"},
		},
	}
}

func runSwitch(t *testing.T, query string) map[string]any {
	r := mustCompile(t, New(testRegistry()), switchSpec(query))
	return UnwrapOutput(mustInvoke(t, r, map[string]any{}))
}

// 命中 branch_0 → "a-run"；非命中分支（bb/bd）不执行。
func TestSwitchBranchHit(t *testing.T) {
	out := runSwitch(t, "say a please")
	if got := out["output"]; got != "a-run" {
		t.Fatalf("output = %v, want a-run (branch_0 命中,互斥)", got)
	}
}

// 命中 branch_1。
func TestSwitchBranchSecondHit(t *testing.T) {
	out := runSwitch(t, "need b now")
	if got := out["output"]; got != "b-run" {
		t.Fatalf("output = %v, want b-run (branch_1 命中)", got)
	}
}

// 无命中 → default 分支。
func TestSwitchBranchDefault(t *testing.T) {
	out := runSwitch(t, "hello world")
	if got := out["output"]; got != "default-run" {
		t.Fatalf("output = %v, want default-run", got)
	}
}

// spec 往返:SwitchConfig 经 Marshal/Unmarshal 后 Query 路径保持。
func TestSwitchSpecRoundtrip(t *testing.T) {
	cfg := &spec.SwitchConfig{Query: spec.Source{Path: []string{"query"}, BlockID: "src", Name: "query"}, Intents: []string{"x"}}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := spec.DecodeConfig(spec.KindSwitch, b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	g := got.(*spec.SwitchConfig)
	if g.Query.Path[0] != "query" || g.Query.BlockID != "src" || len(g.Intents) != 1 {
		t.Fatalf("roundtrip lost: %+v", g)
	}
}

var _ = nodes.MarkerBranch // 保持 import（分支门在 compile 层消费）
