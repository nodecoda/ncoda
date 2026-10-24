package trace_test

// trace_test.go — P1a-1 minimum trace (ES-060a) + schema-shape checks.
//
// The code node emission is driven through the real registry so the test
// proves the production path, not a helper.

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/trace"
)

func TestRecordMinimalJSONShape(t *testing.T) {
	rec := trace.Record{
		Stage:      "timeout",
		RuleID:     trace.RuleTimeout,
		Origin:     trace.OriginWirePresent,
		Evidence:   "observed:provenance:nodes.n1.config.exception.timeout_ms",
		DeadlineMS: 60000,
	}
	if err := rec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"deadline_ms", "evidence", "origin", "rule_id", "stage"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("minimal record keys = %v, want %v", keys, want)
	}
	if m["deadline_ms"].(float64) != 60000 {
		t.Fatalf("deadline_ms = %v, want 60000", m["deadline_ms"])
	}
}

func TestRecordRejectsInvalidOriginAndMissingEvidence(t *testing.T) {
	base := trace.Record{
		Stage: "timeout", RuleID: trace.RuleTimeout, Origin: trace.OriginWirePresent,
		Evidence: "observed:x", DeadlineMS: 1,
	}
	badOrigin := base
	badOrigin.Origin = "authored"
	if err := badOrigin.Validate(); err == nil {
		t.Fatal("invalid origin must be rejected (closed four-value set)")
	}
	noEvidence := base
	noEvidence.Evidence = ""
	if err := noEvidence.Validate(); err == nil {
		t.Fatal("missing evidence must be rejected")
	}
	negDeadline := base
	negDeadline.DeadlineMS = -1
	if err := negDeadline.Validate(); err == nil {
		t.Fatal("negative deadline_ms must be rejected")
	}
}

func buildCodeNode(t *testing.T, id string, cfg *spec.CodeConfig, semTrace func(trace.Record)) nodes.NodeImpl {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	impl, err := nodes.StdRegistry().Build(context.Background(),
		&spec.NodeSpec{ID: id, Kind: spec.KindCode, Config: raw},
		nodes.Deps{SemTrace: semTrace})
	if err != nil {
		t.Fatalf("build code node: %v", err)
	}
	return impl
}

// TestP1a1ES060aCodeNodeEmitsTimeoutTrace is ES-060a: one code execution with
// settingOnError.timeoutMs=60000 must emit stage/rule_id/origin/evidence/
// deadline_ms with deadline_ms=60000 and origin=wire-present.
func TestP1a1ES060aCodeNodeEmitsTimeoutTrace(t *testing.T) {
	var got []trace.Record
	n := buildCodeNode(t, "code_100001", &spec.CodeConfig{
		Code:      "def main(x) -> dict:\n    return {\"result\": [x]}",
		Language:  "python",
		Synthetic: true,
		Exception: &spec.ExceptionConfig{TimeoutMS: 60000},
	}, func(r trace.Record) { got = append(got, r) })

	out, err := n.Invoke(context.Background(), map[string]any{"x": "v"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("emitted %d trace records, want 1: %+v", len(got), got)
	}
	rec := got[0]
	if err := rec.Validate(); err != nil {
		t.Fatalf("emitted record invalid: %v (%+v)", err, rec)
	}
	if rec.Stage != "timeout" {
		t.Fatalf("stage = %q, want timeout", rec.Stage)
	}
	if rec.RuleID != "SEM-TIMEOUT-001" {
		t.Fatalf("rule_id = %q, want SEM-TIMEOUT-001", rec.RuleID)
	}
	if rec.Origin != trace.OriginWirePresent {
		t.Fatalf("origin = %q, want wire-present", rec.Origin)
	}
	if rec.DeadlineMS != 60000 {
		t.Fatalf("deadline_ms = %d, want 60000", rec.DeadlineMS)
	}
	if rec.Evidence == "" {
		t.Fatal("evidence is empty")
	}
	if rec.Node != "code_100001" {
		t.Fatalf("node = %q, want code_100001", rec.Node)
	}
	if _, ok := out["result"]; !ok {
		t.Fatalf("execution output changed: %+v", out)
	}
}

func TestP1a1CodeNodeWithoutTimeoutTraceIsWireAbsent(t *testing.T) {
	var got []trace.Record
	n := buildCodeNode(t, "code_2", &spec.CodeConfig{
		Code:      "def main(x) -> dict:\n    return {\"result\": [x]}",
		Language:  "python",
		Synthetic: true,
	}, func(r trace.Record) { got = append(got, r) })

	if _, err := n.Invoke(context.Background(), map[string]any{"x": "v"}); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("emitted %d records, want 1", len(got))
	}
	if got[0].Origin != trace.OriginWireAbsent {
		t.Fatalf("origin = %q, want wire-absent", got[0].Origin)
	}
	if got[0].DeadlineMS != 0 {
		t.Fatalf("deadline_ms = %d, want 0", got[0].DeadlineMS)
	}
}

// TestP1a1TraceClosedIsZeroChange proves nil tracing leaves execution output
// and errors identical.
func TestP1a1TraceClosedIsZeroChange(t *testing.T) {
	cfg := &spec.CodeConfig{
		Code:      "def main(x) -> dict:\n    return {\"result\": [x]}",
		Language:  "python",
		Synthetic: true,
		Exception: &spec.ExceptionConfig{TimeoutMS: 60000},
	}
	withTrace := buildCodeNode(t, "code_3", cfg, func(trace.Record) {})
	closed := buildCodeNode(t, "code_3", cfg, nil)

	in := map[string]any{"x": "v"}
	a, errA := withTrace.Invoke(context.Background(), in)
	b, errB := closed.Invoke(context.Background(), in)
	if (errA == nil) != (errB == nil) {
		t.Fatalf("trace changed error behavior: %v vs %v", errA, errB)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("trace changed output: %+v vs %+v", a, b)
	}
}

// ES-060b: a collect record must satisfy the same common full-record fields as
// any other full record; only the rule-name field differs (collect_rule).
func TestValidateFullCollectBranch(t *testing.T) {
	base := trace.Record{Stage: "fill", RuleID: trace.RuleCollect, Origin: trace.OriginPolicy,
		Evidence: "hand:docs/AGGREGATION-HOISTING-ALGORITHM.md:63", CollectRule: "absent_skip",
		Attempt: 1, Axis: "per_attempt", Input: "output", Output: "<redacted>", Decision: "passed"}
	if err := base.ValidateFull(); err != nil {
		t.Fatalf("valid collect record: %v", err)
	}
	cases := map[string]func(*trace.Record){
		"collect_rule": func(r *trace.Record) { r.CollectRule = "" },
		"attempt":      func(r *trace.Record) { r.Attempt = 0 },
		"axis":         func(r *trace.Record) { r.Axis = "" },
		"input":        func(r *trace.Record) { r.Input = "" },
		"output":       func(r *trace.Record) { r.Output = "" },
	}
	for name, mutate := range cases {
		r := base
		mutate(&r)
		if err := r.ValidateFull(); err == nil {
			t.Fatalf("collect record without %s passed ValidateFull", name)
		}
	}
	// A collect record must not be satisfied by fill_rule instead.
	r := base
	r.CollectRule = ""
	r.FillRule = "absent_scalar_zero"
	if err := r.ValidateFull(); err == nil {
		t.Fatal("collect record with fill_rule but no collect_rule passed")
	}
}

// ES-116: foreign-code verdict records carry check_rule instead of fill_rule,
// and the full-record contract enforces the branch (SEM-CODE-001/002/003).
func TestValidateFullCodeCheckBranch(t *testing.T) {
	base := trace.Record{
		Stage: "load", RuleID: trace.RuleCodeSyntax, Origin: trace.OriginWirePresent,
		Evidence: "observed:artifact:node:n1", Attempt: 1, Axis: "per_attempt",
		Input: "source", Output: "ok", Decision: "passed",
	}
	if err := base.ValidateFull(); err == nil {
		t.Fatal("code-check record without check_rule must be rejected")
	}
	base.CheckRule = "syntax"
	if err := base.ValidateFull(); err != nil {
		t.Fatalf("check_rule record rejected: %v", err)
	}

	// Import verdicts are policy-origin records; same shape requirement.
	imp := base
	imp.RuleID, imp.Origin, imp.CheckRule = trace.RuleCodeImport, trace.OriginPolicy, "import"
	if err := imp.ValidateFull(); err != nil {
		t.Fatalf("import record rejected: %v", err)
	}

	// A fill record must still carry fill_rule — the branches are exclusive.
	fill := base
	fill.RuleID, fill.CheckRule = trace.RuleFill, ""
	if err := fill.ValidateFull(); err == nil {
		t.Fatal("fill record without fill_rule must stay rejected")
	}
}

func TestCheckRuleClosedSet(t *testing.T) {
	rec := trace.Record{
		Stage: "load", RuleID: trace.RuleCodeSyntax, Origin: trace.OriginWirePresent,
		Evidence: "observed:artifact:node:n1", CheckRule: "guess",
	}
	if err := rec.Validate(); err == nil {
		t.Fatal("unknown check_rule accepted")
	}
}
