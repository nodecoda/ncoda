package nodes

// exec_sem_p4_merge_test.go — P4 gate evidence for the merge value model.
//
// ES-102: merge = first_non_zero. The old runtime implementation was
// first_non_nil, so an executed arm that produced "" (a legal zero) short-
// circuited and the later candidate was never considered. The three cases are
// the ones the test-spec names: [zero, "ok"], [zero, ""], ["", "x"].
//
// ES-111: a declared-but-absent candidate is materialised as its type zero by
// the raising side; the runtime must keep it **in the candidate sequence**
// (skipping it by value, never by position) and record `fill_rule=merge_zero`
// with the candidate order.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/trace"
)

func p4Merge(t *testing.T, cfg *spec.AggregateConfig, semTrace func(trace.Record)) NodeImpl {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal aggregate config: %v", err)
	}
	impl, err := StdRegistry().Build(context.Background(),
		&spec.NodeSpec{ID: "merge_1", Kind: spec.KindAggregate, Config: raw},
		Deps{SemTrace: semTrace})
	if err != nil {
		t.Fatalf("build aggregate: %v", err)
	}
	return impl
}

// TestExecSemP4ES102FirstNonZero locks the three named cases.
func TestExecSemP4ES102FirstNonZero(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want any
	}{
		// (a) unexecuted arm contributes zero "" and the executed arm wins.
		{"unexecuted-zero-then-value", map[string]any{"r0": "", "r1": "ok"}, "ok"},
		// (b) every arm is zero ⇒ zero (empty string stays empty string).
		{"all-zero-keeps-zero", map[string]any{"r0": "", "r1": ""}, ""},
		// (c) empty-string candidate is skipped, not treated as a value.
		{"empty-string-candidate-skipped", map[string]any{"r0": "", "r1": "x"}, "x"},
		// numeric/bool zero follow the same rule.
		{"numeric-zero-skipped", map[string]any{"r0": 0, "r1": 7}, 7},
		{"bool-false-skipped", map[string]any{"r0": false, "r1": true}, true},
		// absent key (nil) and an empty container are zero as well.
		{"absent-and-empty-container", map[string]any{"r0": nil, "r1": []any{}, "r2": "tail"}, "tail"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := p4Merge(t, &spec.AggregateConfig{Refs: []spec.Ref{
				{Producer: "a"}, {Producer: "b"}, {Producer: "c"},
			}}, nil)
			out, err := n.Invoke(context.Background(), tc.in)
			if err != nil {
				t.Fatalf("invoke: %v", err)
			}
			if out[wGroup1] != tc.want {
				t.Fatalf("Group1 = %#v, want %#v", out[wGroup1], tc.want)
			}
		})
	}
}

// TestExecSemP4ES111DeclaredCandidateZeroedAndOrdered: the declared candidate
// arrives as a typed zero; the runtime skips it by value (not by position) and
// records the arm order in the merge trace.
func TestExecSemP4ES111DeclaredCandidateZeroedAndOrdered(t *testing.T) {
	emit, recs := collectTrace(t)
	n := p4Merge(t, &spec.AggregateConfig{Groups: []spec.AggregateGroup{{
		Name: "message",
		Refs: []spec.Ref{
			{Producer: "a", Output: "message"},
			{Producer: "b", Output: "message"},
			{Producer: "c", Output: "message"},
		},
	}}}, emit)

	// Arm a did not produce anything; raising materialised its declared zero.
	// Arm b produced the legal value. Arm c did not run either.
	out, err := n.Invoke(context.Background(), map[string]any{
		"message": map[string]any{"r0": "", "r1": "from-b", "r2": nil},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out["message"] != "from-b" {
		t.Fatalf("message = %#v, want from-b (zero candidate skipped, arm order kept)", out["message"])
	}

	var got []trace.Record
	for _, r := range *recs {
		if r.FillRule == "merge_zero" {
			got = append(got, r)
		}
	}
	if len(got) != 1 {
		t.Fatalf("merge_zero records = %d, want 1: %+v", len(got), got)
	}
	rec := got[0]
	if rec.Stage != "fill" || rec.RuleID != trace.RuleMerge {
		t.Fatalf("record = %s/%s, want fill/%s", rec.Stage, rec.RuleID, trace.RuleMerge)
	}
	if rec.Node != "merge_1" || rec.Kind != spec.KindAggregate {
		t.Fatalf("node/kind = %s/%s, want merge_1/%s", rec.Node, rec.Kind, spec.KindAggregate)
	}
	if rec.Decision != "pick_r1" {
		t.Fatalf("decision = %q, want pick_r1", rec.Decision)
	}
	// Candidate order is the arm order, 0-based r0/r1/r2 — not the survivors.
	for _, want := range []string{"message:a.message", "b.message", "c.message"} {
		if !strings.Contains(rec.Input, want) {
			t.Fatalf("trace input %q does not carry candidate %q", rec.Input, want)
		}
	}
	if strings.Contains(rec.Input, "from-b") || strings.Contains(rec.Output, "from-b") {
		t.Fatalf("trace leaked a candidate value: %+v", rec)
	}

	// All-zero group: still a value, and the trace says so.
	out, err = n.Invoke(context.Background(), map[string]any{
		"message": map[string]any{"r0": "", "r1": "", "r2": ""},
	})
	if err != nil {
		t.Fatalf("invoke(all-zero): %v", err)
	}
	if out["message"] != "" {
		t.Fatalf("all-zero message = %#v, want \"\"", out["message"])
	}
	got = got[:0]
	for _, r := range *recs {
		if r.FillRule == "merge_zero" {
			got = append(got, r)
		}
	}
	if len(got) != 2 || got[1].Decision != "all_zero" {
		t.Fatalf("all-zero merge trace = %+v, want decision all_zero", got)
	}
}

// TestExecSemP4ES111AbsentCandidateNotRenumbered: a missing key must not shift
// the remaining arms (r2 is still candidate #2). If the runtime compacted the
// candidate list, the trace order would change and this goes red.
func TestExecSemP4ES111AbsentCandidateNotRenumbered(t *testing.T) {
	emit, recs := collectTrace(t)
	n := p4Merge(t, &spec.AggregateConfig{Refs: []spec.Ref{
		{Producer: "a"}, {Producer: "b"}, {Producer: "c"},
	}}, emit)
	// r0 and r1 absent; only r2 carries a value.
	out, err := n.Invoke(context.Background(), map[string]any{"r2": "tail"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out[wGroup1] != "tail" {
		t.Fatalf("Group1 = %#v, want tail", out[wGroup1])
	}
	var last trace.Record
	for _, r := range *recs {
		if r.FillRule == "merge_zero" {
			last = r
		}
	}
	if last.Decision != "pick_r2" {
		t.Fatalf("decision = %q, want pick_r2 (absent candidates must not renumber)", last.Decision)
	}
	if !strings.Contains(last.Input, "a,b,c") {
		t.Fatalf("trace input %q lost the full candidate order", last.Input)
	}
}
