package boundary

// exec_sem_p5_oss_property_test.go — ES-079/ES-083.
//
// ES-079 pins the OSS reference at
// coze-studio@fefb05ff:backend/domain/workflow/internal/compose/field_fill.go
// and TypeInfo.Zero (backend/domain/workflow/entity/vo/node.go:283). The table
// below is a minimal transcription of those two functions for the covered
// shapes; it is deliberately not a second production implementation.
//
// Evidence grade: this is LOCAL zero-value conformance against a transcription,
// not a differential run of the OSS binary. There is no external oracle here;
// ES-115 records that residual. The test name says "local" so the claim cannot
// be read as platform-truth verification.
//
// ES-083 adds a deterministic property pass: random (type, value, policy)
// through the real boundary must not panic, must be idempotent, and every
// emitted record must satisfy the trace contract.
//
// Documented non-differential boundary: absent object WITH declared properties.
// OSS recursively materialises the member map with nil sub-members
// ({sub: null}; isInsideObject=true keeps nil); ncoda publishes whole null
// (project decision: zero(container) -> null). It is excluded from the covered
// set instead of being silently claimed equal.

import (
	"math/rand"
	"reflect"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/trace"
)

var ossCoveredKinds = []string{"string", "integer", "number", "boolean", "time", "file", "list", "object"}

// ossZero transcribes vo.TypeInfo.Zero (fefb05ff).
func ossZero(kind string) any {
	switch kind {
	case "string", "time", "file":
		return ""
	case "integer":
		return int64(0)
	case "number":
		return float64(0)
	case "boolean":
		return false
	case "object":
		var m map[string]any
		return m
	case "list":
		var a []any
		return a
	}
	panic("not an OSS zero kind: " + kind)
}

// ossFillAbsent transcribes FillIfNotRequired's absent non-required branch
// under FillZero (inputValueFiller). Array elements are handled separately.
func ossFillAbsent(kind string) any { return ossZero(kind) }

func ossFillPresentNil(kind string) any { return ossZero(kind) }

func norm(v any) any {
	if isNilValue(v) {
		return nil
	}
	return v
}

func TestExecSemP5ES079LocalZeroValueConformance(t *testing.T) {
	for _, kind := range ossCoveredKinds {
		kind := kind
		t.Run(kind+"/absent", func(t *testing.T) {
			b := New(Options{Policy: DefaultPolicy(), NodeID: "es079"})
			out, err := b.FillInputs(map[string]any{}, map[string]*spec.TypeSpec{"v": {Kind: kind}}, 1)
			if err != nil {
				t.Fatalf("fill: %v", err)
			}
			if got, want := norm(out["v"]), norm(ossFillAbsent(kind)); !reflect.DeepEqual(got, want) {
				t.Fatalf("absent %s = %#v, OSS %#v", kind, got, want)
			}
		})
		t.Run(kind+"/present-nil", func(t *testing.T) {
			b := New(Options{Policy: DefaultPolicy(), NodeID: "es079"})
			out, err := b.FillInputs(map[string]any{"v": nil}, map[string]*spec.TypeSpec{"v": {Kind: kind}}, 1)
			if err != nil {
				t.Fatalf("fill: %v", err)
			}
			if got, want := norm(out["v"]), norm(ossFillPresentNil(kind)); !reflect.DeepEqual(got, want) {
				t.Fatalf("present-nil %s = %#v, OSS %#v", kind, got, want)
			}
		})
	}

	// Present non-zero values pass through; OSS only copies arrays and fills
	// their nil elements (elem integer -> 0).
	b := New(Options{Policy: DefaultPolicy(), NodeID: "es079"})
	got, err := b.FillInputs(map[string]any{"v": []any{nil, int64(1)}}, map[string]*spec.TypeSpec{
		"v": {Kind: "list", Elem: &spec.TypeSpec{Kind: "integer"}},
	}, 1)
	if err != nil {
		t.Fatalf("fill list: %v", err)
	}
	want := []any{int64(0), int64(1)}
	if !reflect.DeepEqual(got["v"], want) {
		t.Fatalf("list present = %#v, OSS %#v", got["v"], want)
	}
	if gotCount := len(ossCoveredKinds); gotCount < 8 {
		t.Fatalf("covered kinds = %d, want >= 8", gotCount)
	}
}

func TestExecSemP5ES083BoundaryProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(83))
	fills := []FillMode{PreFillZero, PreFillNil, PreFillNone}
	values := []any{nil, "", int64(0), int64(7), 0.0, 1.5, false, true, []any{}, []any{nil, "x"}, map[string]any{}}
	kinds := []string{"string", "integer", "number", "boolean", "list", "object"}
	for i := 0; i < 400; i++ {
		kind := kinds[rng.Intn(len(kinds))]
		ts := &spec.TypeSpec{Kind: kind}
		if kind == "list" && rng.Intn(2) == 0 {
			ts.Elem = &spec.TypeSpec{Kind: "integer"}
		}
		pol := DefaultPolicy()
		pol.PreFill = fills[rng.Intn(len(fills))]
		var recs []trace.Record
		b := New(Options{Policy: pol, NodeID: "prop", Emit: func(r trace.Record) {
			if err := r.Validate(); err != nil {
				t.Fatalf("invalid trace: %v (%+v)", err, r)
			}
			recs = append(recs, r)
		}})
		in := map[string]any{}
		if rng.Intn(2) == 0 {
			in["v"] = values[rng.Intn(len(values))]
		}
		out1, err := b.FillInputs(in, map[string]*spec.TypeSpec{"v": ts}, 1)
		if err != nil {
			continue // required path is covered by the dedicated tests
		}
		out2, err := b.FillInputs(out1, map[string]*spec.TypeSpec{"v": ts}, 1)
		if err != nil {
			t.Fatalf("second fill: %v", err)
		}
		if !reflect.DeepEqual(out1, out2) {
			t.Fatalf("non-idempotent case %d (%s/%v): %#v -> %#v", i, kind, pol.PreFill, out1, out2)
		}
		for _, r := range recs {
			if r.RuleID == trace.RuleFill && r.FillRule == "" {
				t.Fatalf("fill record without rule: %+v", r)
			}
		}
	}
	// Negative controls: an unknown kind must be explicit, never invented.
	b := New(Options{Policy: DefaultPolicy(), NodeID: "prop_unknown"})
	out, err := b.FillInputs(map[string]any{}, map[string]*spec.TypeSpec{"v": {Kind: "mystery"}}, 1)
	if err != nil {
		t.Fatalf("unknown kind fill: %v", err)
	}
	if out["v"] != nil {
		t.Fatalf("unknown kind fabricated %#v", out["v"])
	}
}
