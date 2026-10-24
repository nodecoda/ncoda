package spec

import (
	"encoding/json"
	"strings"
	"testing"
)

// ── Agent config 解析契约 ────────────────────────────────────────────────

// decodeAgent 是 DecodeConfig 的便捷封装,断言返回 AgentConfig。
func decodeAgent(t *testing.T, raw string) *AgentConfig {
	t.Helper()
	cfg, err := DecodeConfig(string(KindAgent), json.RawMessage(raw))
	if err != nil {
		t.Fatalf("decode agent config %q: %v", raw, err)
	}
	ac, ok := cfg.(*AgentConfig)
	if !ok {
		t.Fatalf("decoded %T, want *AgentConfig", cfg)
	}
	return ac
}

func TestAgentConfigDecodeMaxIteration(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int // 0 = 未设置(spec 层透传,clamp 是 nodes 层职责)
	}{
		{"omitted defaults to zero", `{"model":"m","tools":[]}`, 0},
		{"explicit value passes through", `{"model":"m","tools":[],"max_iteration":5}`, 5},
		{"negative passes through", `{"model":"m","tools":[],"max_iteration":-1}`, -1},
		{"over-99 passes through", `{"model":"m","tools":[],"max_iteration":100}`, 100},
		{"cap 99 passes through", `{"model":"m","tools":[],"max_iteration":99}`, 99},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ac := decodeAgent(t, c.raw)
			if ac.MaxIteration != c.want {
				t.Fatalf("MaxIteration = %d, want %d", ac.MaxIteration, c.want)
			}
		})
	}
}

func TestAgentConfigDecodeRest(t *testing.T) {
	ac := decodeAgent(t, `{
		"model": "m",
		"instruction": "be terse",
		"query": {"block_id": "s1"},
		"max_iteration": 3,
		"tools": [{"alias": "w", "provider_name": "math", "tool_name": "sum"}]
	}`)
	if ac.Model != "m" || ac.Instruction != "be terse" {
		t.Fatalf("model/instruction mismatch: %+v", ac)
	}
	if ac.Query == nil || ac.Query.BlockID != "s1" {
		t.Fatalf("query mismatch: %+v", ac.Query)
	}
	if len(ac.Tools) != 1 || ac.Tools[0].Alias != "w" {
		t.Fatalf("tools mismatch: %+v", ac.Tools)
	}
}

func TestAgentConfigDecodeUnknownKindRejected(t *testing.T) {
	_, err := DecodeConfig("bogus-kind", json.RawMessage(`{"model":"m"}`))
	if err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("want unknown-kind error, got %v", err)
	}
}

// ── Selector 条件 source 投影(operandPath 路径约定) ─────────────────────

func TestSelectorSourcesOperandPath(t *testing.T) {
	// 单条件:path = ["0","left"/"right"];多条件:path = ["0",i,"left"/"right"]。
	n := &NodeSpec{
		ID:   "sel",
		Kind: KindSelector,
		Config: rawConfig(t, &SelectorConfig{Conditions: []Condition{
			{Left: Operand{Ref: &Ref{Producer: "a", Output: "x"}}, Right: Operand{Val: 1}},
			{Left: Operand{Ref: &Ref{Producer: "b", Output: "y"}}, Right: Operand{Ref: &Ref{Producer: "c", Output: "z"}}},
		}}),
	}
	srcs, err := Sources(n)
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 4 {
		t.Fatalf("sources = %d, want 4", len(srcs))
	}
	want := []struct {
		path    []string
		blockID string
		name    string
	}{
		{[]string{"0", "left"}, "a", "x"},       // 条件0 左(ref)
		{[]string{"0", "right"}, "", ""},        // 条件0 右(字面量 1)
		{[]string{"0", "1", "left"}, "b", "y"},  // 条件1 左(ref)
		{[]string{"0", "1", "right"}, "c", "z"}, // 条件1 右(ref)
	}
	for i, w := range want {
		s := srcs[i]
		if !equalStrings(s.Path, w.path) || s.BlockID != w.blockID || s.Name != w.name {
			t.Fatalf("src[%d] = %+v, want path=%v block=%q name=%q", i, s, w.path, w.blockID, w.name)
		}
	}
	if v, ok := srcs[1].Val.(float64); !ok || v != 1 {
		t.Fatalf("src[1].Val = %#v, want float64(1) (literal operand, JSON-decoded)", srcs[1].Val)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// rawConfig marshals a config DTO into a RawMessage for NodeSpec.Config.
func rawConfig(t *testing.T, cfg any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return b
}
