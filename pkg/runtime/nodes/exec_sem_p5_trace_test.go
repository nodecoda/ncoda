package nodes

// exec_sem_p5_trace_test.go — P5 full-trace gate (ES-060b).
//
// One real code execution triggers coerce (present input), fill (declared
// missing input), postfill (declared missing output port) and retry (the first
// python attempt fails, the second succeeds). Every emitted record must pass
// the minimal contract; timeout/retry records must additionally pass
// ValidateFull (fill_rule/attempt/axis/input/output). Dropping any of those
// five fields is the falsification control.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/trace"
)

func TestExecSemP5ES060bFullCodeTrace(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "attempts")
	code := fmt.Sprintf(`import os
p = %q
n = int(open(p).read()) if os.path.exists(p) else 0
open(p, "w").write(str(n + 1))
if n == 0:
    raise RuntimeError("first attempt fails")
def main(x, y):
    result = {"result": str(x)}
    return result
`, counter)
	cfg := &spec.CodeConfig{
		Code:      code,
		Language:  "python",
		Synthetic: false,
		Types: map[string]*spec.TypeSpec{
			"x": {Kind: "list", Elem: &spec.TypeSpec{Kind: "integer"}},
			"y": {Kind: "string"},
		},
		OutTypes: map[string]*spec.TypeSpec{
			"result": {Kind: "string"},
			"extra":  {Kind: "boolean"},
		},
		Exception: &spec.ExceptionConfig{MaxRetry: 1, TimeoutMS: 60000},
	}
	emit, recs := collectTrace(t)
	n := buildP1a2Code(t, "code_060b", cfg, emit)
	out, err := n.Invoke(context.Background(), map[string]any{"x": []any{nil, int64(1)}})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out["extra"] != nil || out["output"] != "[0, 1]" {
		t.Fatalf("unexpected output: %#v", out)
	}

	var sawCoerce, sawFill, sawPostfill, sawFull bool
	var retryAttempts []int
	for _, r := range *recs {
		if r.RuleID == trace.RuleFill && r.Decision == "coerced" {
			sawCoerce = true
		}
		if r.RuleID == trace.RuleFill && r.FillRule == "absent_scalar_zero" {
			sawFill = true
		}
		if r.RuleID == trace.RulePostFill && r.FillRule == "postfill_nil" {
			sawPostfill = true
		}
		if r.RuleID == trace.RuleRetry {
			retryAttempts = append(retryAttempts, r.Attempt)
			if r.Axis != "total" {
				t.Fatalf("retry record axis = %q, want total: %+v", r.Axis, r)
			}
		}
		if r.RuleID == trace.RuleTimeout || r.RuleID == trace.RuleRetry {
			if err := r.ValidateFull(); err != nil {
				t.Fatalf("full record: %v (%+v)", err, r)
			}
			sawFull = true
		}
	}
	if !sawCoerce || !sawFill || !sawPostfill || !sawFull {
		t.Fatalf("trace incomplete coerce=%v fill=%v postfill=%v full=%v: %+v",
			sawCoerce, sawFill, sawPostfill, sawFull, *recs)
	}
	if !reflect.DeepEqual(retryAttempts, []int{1, 2}) {
		t.Fatalf("retry attempts = %v, want [1 2]", retryAttempts)
	}

	// Trace on/off must not change the published value. Reset the counter so
	// both runs take the same retry path.
	_ = os.Remove(counter)
	n2 := buildP1a2Code(t, "code_060b", cfg, nil)
	out2, err := n2.Invoke(context.Background(), map[string]any{"x": []any{nil, int64(1)}})
	if err != nil {
		t.Fatalf("invoke without trace: %v", err)
	}
	if !reflect.DeepEqual(out, out2) {
		t.Fatalf("trace changed output: %#v vs %#v", out, out2)
	}
}
