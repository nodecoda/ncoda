package nodes

// text_split_contract_test.go — text split 的 wire 输入键与多分隔符契约
// （2026-10-21，语料 48 份 NodeRunError 的共性根因）。
//
// 判据：① 输入按 `TextConfig.Inputs[].Path`（wire 的 node_inputs.name）取，
// ② `Separators`（splitParams.delimiters）全部生效。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func splitNode(t *testing.T, cfg *spec.TextConfig) NodeImpl {
	t.Helper()
	return build(t, spec.KindText, cfg)
}

func TestTextSplitReadsDeclaredInputPath(t *testing.T) {
	n := splitNode(t, &spec.TextConfig{
		Method:     "split",
		ConcatChar: ",",
		Inputs:     []spec.Source{{Path: []string{"input"}}},
	})
	out, err := n.Invoke(ctx(), map[string]any{"input": "a,b,c"})
	if err != nil {
		t.Fatal(err)
	}
	got := out["output"].([]string)
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("split by declared input = %v, want [a b c]", got)
	}
}

func TestTextSplitEmptyInputKeepsSingleEmptySegment(t *testing.T) {
	n := splitNode(t, &spec.TextConfig{
		Method:     "split",
		ConcatChar: ",",
		Inputs:     []spec.Source{{Path: []string{"input"}}},
	})
	out, _ := n.Invoke(ctx(), map[string]any{"input": ""})
	got := out["output"].([]string)
	if len(got) != 1 || got[0] != "" {
		t.Fatalf("split(\"\") = %v, want [\"\"]", got)
	}
}

func TestTextSplitSeparators(t *testing.T) {
	n := splitNode(t, &spec.TextConfig{
		Method:     "split",
		Separators: []string{"\n", "\t", "。", "；"},
		Inputs:     []spec.Source{{Path: []string{"input"}}},
	})
	out, _ := n.Invoke(ctx(), map[string]any{"input": "甲。乙\n丙；丁"})
	got := out["output"].([]string)
	want := []string{"甲", "乙", "丙", "丁"}
	if len(got) != len(want) {
		t.Fatalf("split by separators = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("split by separators = %v, want %v", got, want)
		}
	}
}

func TestTextSplitSeparatorsKeepEmptySegments(t *testing.T) {
	n := splitNode(t, &spec.TextConfig{
		Method:     "split",
		Separators: []string{","},
		Inputs:     []spec.Source{{Path: []string{"input"}}},
	})
	out, _ := n.Invoke(ctx(), map[string]any{"input": "a,,b"})
	got := out["output"].([]string)
	if len(got) != 3 || got[1] != "" {
		t.Fatalf("split keeps empty segments: %v", got)
	}
}

func TestTextSplitNoSeparatorsReturnsWhole(t *testing.T) {
	n := splitNode(t, &spec.TextConfig{
		Method: "split",
		Inputs: []spec.Source{{Path: []string{"input"}}},
	})
	out, _ := n.Invoke(ctx(), map[string]any{"input": "abc"})
	got := out["output"].([]string)
	if len(got) != 1 || got[0] != "abc" {
		t.Fatalf("no-separator split = %v, want [abc]", got)
	}
}
