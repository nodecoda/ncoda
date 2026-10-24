package nodes

// listop_test.go — list_op 内存实现黄金表（PRD U2.5）。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func TestListOpFilter(t *testing.T) {
	cases := []struct {
		name string
		cfg  *spec.ListOpConfig
		in   []any
		want []any
	}{
		{
			"contains", &spec.ListOpConfig{Operation: "filter", FilterOp: "contains", FilterVal: "a"},
			[]any{"apple", "banana", "cherry"}, []any{"apple", "banana"},
		},
		{
			"is", &spec.ListOpConfig{Operation: "filter", FilterOp: "is", FilterVal: "x"},
			[]any{"x", "y", "x"}, []any{"x", "x"},
		},
		{
			"gt", &spec.ListOpConfig{Operation: "filter", FilterOp: ">", FilterVal: float64(2)},
			[]any{float64(1), float64(3), float64(2)}, []any{float64(3)},
		},
		{
			"field-key", &spec.ListOpConfig{Operation: "filter", FilterKey: "v", FilterOp: "is", FilterVal: "1"},
			[]any{map[string]any{"v": "1"}, map[string]any{"v": "2"}}, []any{map[string]any{"v": "1"}},
		},
		{
			"empty-op-passthrough", &spec.ListOpConfig{Operation: "filter"},
			[]any{"a", "b"}, []any{"a", "b"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := build(t, spec.KindListOp, c.cfg)
			out, err := n.Invoke(t.Context(), map[string]any{"input": c.in})
			if err != nil {
				t.Fatal(err)
			}
			if len(out["output"].([]any)) != len(c.want) {
				t.Fatalf("out = %v, want %v", out["output"], c.want)
			}
		})
	}
}

func TestListOpTake(t *testing.T) {
	n := build(t, spec.KindListOp, &spec.ListOpConfig{Operation: "take", Limit: 2})
	out, err := n.Invoke(t.Context(), map[string]any{"input": []any{"a", "b", "c"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := out["output"].([]any); len(got) != 2 || got[0] != "a" {
		t.Fatalf("take out = %v", out["output"])
	}
}

func TestListOpSort(t *testing.T) {
	n := build(t, spec.KindListOp, &spec.ListOpConfig{Operation: "sort", OrderVal: "asc"})
	out, _ := n.Invoke(t.Context(), map[string]any{"input": []any{float64(3), float64(1), float64(2)}})
	if got := out["output"].([]any); len(got) != 3 || got[0] != float64(1) || got[2] != float64(3) {
		t.Fatalf("sort asc out = %v", out["output"])
	}
	n = build(t, spec.KindListOp, &spec.ListOpConfig{Operation: "sort_by", OrderKey: "k", OrderVal: "desc"})
	out, _ = n.Invoke(t.Context(), map[string]any{"input": []any{
		map[string]any{"k": "1"}, map[string]any{"k": "3"}, map[string]any{"k": "2"},
	}})
	got := out["output"].([]any)
	if got[0].(map[string]any)["k"] != "3" {
		t.Fatalf("sort_by desc out = %v", out["output"])
	}
}

func TestStdRegistryCoverage(t *testing.T) {
	r := StdRegistry()
	want := []string{
		spec.KindStart, spec.KindEnd, spec.KindText, spec.KindAggregate,
		spec.KindSelector, spec.KindAssign, spec.KindLoopAssign,
		spec.KindListOp, spec.KindOutput, spec.KindCode, spec.KindKnowledge,
		spec.KindBreak, spec.KindContinue,
	}
	for _, k := range want {
		if !r.Has(k) {
			t.Errorf("StdRegistry missing kind %q", k)
		}
	}
	if r.Has(spec.KindLoop) {
		t.Error("loop must not be a registry kind (compile-built driver)")
	}
}
