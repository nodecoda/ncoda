package nodes

// switch_test.go — switch 确定性分类桩单测。

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func TestClassifyIntent(t *testing.T) {
	intents := []string{"查天气", "订机票", "视频生成"}
	cases := []struct {
		query string
		wantI int
		wantR string
	}{
		{"帮我查天气,北京的天气如何", 0, "查天气"},
		{"帮我订机票去上海", 1, "订机票"},
		{"帮我视频生成一段", 2, "视频生成"},
		{"今天吃什么", -1, ""},
		{"", -1, ""},
	}
	for _, tc := range cases {
		gotI, gotR := classifySwitch(tc.query, intents)
		if gotI != tc.wantI || gotR != tc.wantR {
			t.Fatalf("classifySwitch(%q) = (%d,%q), want (%d,%q)", tc.query, gotI, gotR, tc.wantI, tc.wantR)
		}
	}
}

// 最长命中优先:两个意图都匹配时取长名（"视频生成" 压过 "生成"）。
func TestClassifyIntentLongestWins(t *testing.T) {
	gotI, gotR := classifySwitch("帮我视频生成一下", []string{"生成", "视频生成"})
	if gotI != 1 || gotR != "视频生成" {
		t.Fatalf("longest-wins: got (%d,%q), want (1,\"视频生成\")", gotI, gotR)
	}
}

func intentSpecNode(cfg *spec.SwitchConfig) *spec.NodeSpec {
	b, err := json.Marshal(cfg)
	if err != nil {
		panic(err)
	}
	return &spec.NodeSpec{ID: "i1", Kind: spec.KindSwitch, Config: b}
}

func TestIntentInvokeBranch(t *testing.T) {
	n, err := Switch{}.Build(context.Background(), intentSpecNode(&spec.SwitchConfig{Intents: []string{"a", "b"}}), Deps{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	out, err := n.Invoke(context.Background(), map[string]any{"query": "xx b yy"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out[MarkerBranch] != "branch_1" {
		t.Fatalf("$branch = %v, want branch_1", out[MarkerBranch])
	}
	if out["classificationId"] != int64(1) || out["reason"] != "b" {
		t.Fatalf("out = %v, want classificationId=1 reason=b", out)
	}
	// 无命中 → default + classificationId=-1
	out, err = n.Invoke(context.Background(), map[string]any{"query": "zzz"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out[MarkerBranch] != "default" || out["classificationId"] != int64(-1) {
		t.Fatalf("default: $branch=%v classificationId=%v, want default/-1", out[MarkerBranch], out["classificationId"])
	}
}
