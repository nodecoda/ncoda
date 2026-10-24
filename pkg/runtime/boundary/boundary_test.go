package boundary

// boundary_test.go — P1a-2 L1/L2 gates for the single ValueBoundary.
//
// Every test drives the production constructor (DefaultPolicy/New), not a
// parallel test-only shaping copy.

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/trace"
)

func tspec(kind string, elem *spec.TypeSpec) *spec.TypeSpec {
	return &spec.TypeSpec{Kind: kind, Elem: elem}
}

func fillWith(t *testing.T, pol Policy, decl map[string]*spec.TypeSpec, in map[string]any) (map[string]any, []trace.Record, error) {
	t.Helper()
	var recs []trace.Record
	b := New(Options{Policy: pol, NodeID: "n1", Kind: spec.KindCode, Emit: func(r trace.Record) {
		if err := r.Validate(); err != nil {
			t.Fatalf("invalid trace record: %v (%+v)", err, r)
		}
		recs = append(recs, r)
	}})
	out, err := b.FillInputs(in, decl, 1)
	return out, recs, err
}

func fillRules(recs []trace.Record) []string {
	var rules []string
	for _, r := range recs {
		if r.Stage == "fill" {
			rules = append(rules, r.FillRule)
		}
	}
	return rules
}

func hasRule(recs []trace.Record, rule string) bool {
	for _, r := range recs {
		if r.FillRule == rule {
			return true
		}
	}
	return false
}

func countRule(recs []trace.Record, rule string) int {
	n := 0
	for _, r := range recs {
		if r.FillRule == rule {
			n++
		}
	}
	return n
}

// ES-001: absent / null / value stay distinguishable in trace; published
// values are tri-state only.
func TestP1a2ES001TriStateDistinct(t *testing.T) {
	decl := map[string]*spec.TypeSpec{
		"absent_s": tspec("string", nil),
		"null_s":   tspec("string", nil),
		"value_s":  tspec("string", nil),
	}
	in := map[string]any{"null_s": nil, "value_s": "v"}
	out, recs, err := fillWith(t, DefaultPolicy(), decl, in)
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	if out["absent_s"] != "" || out["null_s"] != "" || out["value_s"] != "v" {
		t.Fatalf("published tri-state = %#v", out)
	}
	if !hasRule(recs, "absent_scalar_zero") || !hasRule(recs, "null_scalar_zero") || !hasRule(recs, "value_passthrough") {
		t.Fatalf("trace does not distinguish absent/null/value: %v", fillRules(recs))
	}
	// Origins must also differ: fills are policy, a present value is wire-present.
	origins := map[string]trace.Origin{}
	for _, r := range recs {
		origins[r.FillRule] = r.Origin
	}
	if origins["absent_scalar_zero"] != trace.OriginPolicy ||
		origins["null_scalar_zero"] != trace.OriginPolicy ||
		origins["value_passthrough"] != trace.OriginWirePresent {
		t.Fatalf("origins = %v", origins)
	}
}

// ES-002: scalar zero table for nil and absent.
func TestP1a2ES002ScalarZeroTable(t *testing.T) {
	cases := []struct {
		kind string
		want any
	}{
		{"string", ""},
		{"integer", int64(0)},
		{"number", float64(0)},
		{"boolean", false},
		{"time", ""},
		{"file", ""},
	}
	for _, tc := range cases {
		t.Run(tc.kind+"/null", func(t *testing.T) {
			decl := map[string]*spec.TypeSpec{"v": tspec(tc.kind, nil)}
			out, recs, err := fillWith(t, DefaultPolicy(), decl, map[string]any{"v": nil})
			if err != nil {
				t.Fatalf("fill: %v", err)
			}
			if !reflect.DeepEqual(out["v"], tc.want) {
				t.Fatalf("%s null → %#v, want %#v", tc.kind, out["v"], tc.want)
			}
			if !hasRule(recs, "null_scalar_zero") {
				t.Fatalf("missing null_scalar_zero trace: %v", fillRules(recs))
			}
		})
		t.Run(tc.kind+"/absent", func(t *testing.T) {
			decl := map[string]*spec.TypeSpec{"v": tspec(tc.kind, nil)}
			out, recs, err := fillWith(t, DefaultPolicy(), decl, map[string]any{})
			if err != nil {
				t.Fatalf("fill: %v", err)
			}
			if !reflect.DeepEqual(out["v"], tc.want) {
				t.Fatalf("%s absent → %#v, want %#v", tc.kind, out["v"], tc.want)
			}
			if !hasRule(recs, "absent_scalar_zero") {
				t.Fatalf("missing absent_scalar_zero trace: %v", fillRules(recs))
			}
		})
	}
}

// ES-003/ES-010: array element zero.
func TestP1a2ES003ArrayElemZero(t *testing.T) {
	decl := map[string]*spec.TypeSpec{"xs": tspec("list", tspec("integer", nil))}
	out, recs, err := fillWith(t, DefaultPolicy(), decl, map[string]any{"xs": []any{nil, 1, nil}})
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	want := []any{int64(0), 1, int64(0)}
	if !reflect.DeepEqual(out["xs"], want) {
		t.Fatalf("array<integer>[nil,1,nil] = %#v, want %#v", out["xs"], want)
	}
	if !hasRule(recs, "array_elem_zero") {
		t.Fatalf("trace fill_rule must be array_elem_zero: %v", fillRules(recs))
	}
}

// ES-004: container zero is published as null; empty stays []/{}. A typed nil
// element of array<object> stays null.
func TestP1a2ES004ContainerZeroPublishesNull(t *testing.T) {
	decl := map[string]*spec.TypeSpec{
		"nil_list": tspec("list", tspec("string", nil)),
		"nil_obj":  tspec("object", nil),
		"nil_recs": tspec("list", tspec("object", nil)),
	}
	out, recs, err := fillWith(t, DefaultPolicy(), decl, map[string]any{
		"nil_list": nil,
		"nil_obj":  nil,
		"nil_recs": []any{nil},
	})
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	if out["nil_list"] != nil || out["nil_obj"] != nil {
		t.Fatalf("container zero must publish null, got %#v", out)
	}
	if recs := countRule(recs, "container_zero_published_null"); recs != 2 {
		t.Fatalf("container_zero_published_null records = %d, want 2", recs)
	}
	if arr, ok := out["nil_recs"].([]any); !ok || len(arr) != 1 || arr[0] != nil {
		t.Fatalf("array<object>[nil] = %#v, want [null]", out["nil_recs"])
	}
	if !hasRule(recs, "array_elem_typed_nil") {
		t.Fatalf("typed nil must record array_elem_typed_nil: %v", fillRules(recs))
	}

	// Typed-nil containers are still container zero, not empty containers.
	typedNilDecl := map[string]*spec.TypeSpec{
		"typed_nil_list": tspec("list", tspec("string", nil)),
		"typed_nil_obj":  tspec("object", nil),
	}
	typedNilOut, _, err := fillWith(t, DefaultPolicy(), typedNilDecl, map[string]any{
		"typed_nil_list": []any(nil),
		"typed_nil_obj":  map[string]any(nil),
	})
	if err != nil {
		t.Fatalf("fill typed nil: %v", err)
	}
	if typedNilOut["typed_nil_list"] != nil || typedNilOut["typed_nil_obj"] != nil {
		t.Fatalf("typed-nil containers must publish null, got %#v", typedNilOut)
	}

	emptyDecl := map[string]*spec.TypeSpec{
		"empty_list": tspec("list", tspec("string", nil)),
		"empty_obj":  tspec("object", nil),
	}
	emptyOut, _, err := fillWith(t, DefaultPolicy(), emptyDecl, map[string]any{
		"empty_list": []any{},
		"empty_obj":  map[string]any{},
	})
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	if arr, ok := emptyOut["empty_list"].([]any); !ok || len(arr) != 0 {
		t.Fatalf("empty list = %#v, want []", emptyOut["empty_list"])
	}
	if m, ok := emptyOut["empty_obj"].(map[string]any); !ok || len(m) != 0 {
		t.Fatalf("empty object = %#v, want {}", emptyOut["empty_obj"])
	}
}

// ES-006: a second boundary pass is a no-op.
func TestP1a2ES006Idempotent(t *testing.T) {
	decl := map[string]*spec.TypeSpec{
		"s":  tspec("string", nil),
		"xs": tspec("list", tspec("integer", nil)),
		"o":  tspec("object", nil),
	}
	in := map[string]any{"s": nil, "xs": []any{nil, 1}, "o": nil}
	pol := DefaultPolicy()
	once, _, err := fillWith(t, pol, decl, in)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	twice, _, err := fillWith(t, pol, decl, once)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !reflect.DeepEqual(once, twice) {
		t.Fatalf("not idempotent:\n first=%#v\nsecond=%#v", once, twice)
	}
}

// ES-014: a declared but absent container input is published as null (not
// absent, not []), with origin=policy and the container-zero rule.
func TestP1a2ES014AbsentContainerInputNull(t *testing.T) {
	decl := map[string]*spec.TypeSpec{"textList": tspec("list", tspec("string", nil))}
	out, recs, err := fillWith(t, DefaultPolicy(), decl, map[string]any{})
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	v, ok := out["textList"]
	if !ok {
		t.Fatalf("declared absent key must exist: %#v", out)
	}
	if v != nil {
		t.Fatalf("absent container = %#v, want null", v)
	}
	if _, isSlice := v.([]any); isSlice {
		t.Fatalf("absent container must not become []: %#v", v)
	}
	found := false
	for _, r := range recs {
		if r.FillRule == "container_zero_published_null" {
			found = true
			if r.Origin != trace.OriginPolicy {
				t.Fatalf("origin = %q, want policy", r.Origin)
			}
		}
	}
	if !found {
		t.Fatalf("missing container_zero_published_null record: %v", fillRules(recs))
	}
}

// ES-016: required is checked inside fill, before any zero is applied.
func TestP1a2ES016RequiredCheckedInsideFill(t *testing.T) {
	pol := DefaultPolicy()
	pol.EnforceRequired = true
	decl := map[string]*spec.TypeSpec{"a": {Kind: "string", Required: true}}
	_, recs, err := fillWith(t, pol, decl, map[string]any{})
	var req *RequiredError
	if !errors.As(err, &req) {
		t.Fatalf("missing required input must error, got %v", err)
	}
	if req.Port != "a" {
		t.Fatalf("RequiredError.Port = %q, want a", req.Port)
	}
	if !hasRule(recs, "required_missing") {
		t.Fatalf("trace must record required_missing: %v", fillRules(recs))
	}
	if hasRule(recs, "absent_scalar_zero") {
		t.Fatalf("zero was applied before the required check: %v", fillRules(recs))
	}
}

// ES-073 negative control: dropping prefill zero changes array element fill.
func TestP1a2ES073DropPreFillZero(t *testing.T) {
	pol, err := LoadPolicy(filepath.Join("testdata", "policy", "drop_prefill.yaml"))
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	decl := map[string]*spec.TypeSpec{"xs": tspec("list", tspec("integer", nil))}
	out, recs, err := fillWith(t, pol, decl, map[string]any{"xs": []any{nil, 1}})
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	want := []any{nil, 1}
	if !reflect.DeepEqual(out["xs"], want) {
		t.Fatalf("drop_prefill must keep nil element: %#v", out["xs"])
	}
	if hasRule(recs, "array_elem_zero") {
		t.Fatalf("drop_prefill must not emit array_elem_zero: %v", fillRules(recs))
	}
}

// ES-074 negative control: dropping postfill leaves the declared port absent.
func TestP1a2ES074DropPostFillNil(t *testing.T) {
	outTypes := map[string]*spec.TypeSpec{"duration": tspec("integer", nil), "image_url": tspec("string", nil)}
	out := map[string]any{"image_url": "u"}

	on := New(Options{Policy: DefaultPolicy()})
	got := on.PostFillOutputs(out, outTypes, nil, 1)
	if v, ok := got["duration"]; !ok || v != nil {
		t.Fatalf("postfill on must materialise duration=null: %#v", got)
	}

	pol, err := LoadPolicy(filepath.Join("testdata", "policy", "drop_postfill.yaml"))
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	off := New(Options{Policy: pol})
	got = off.PostFillOutputs(map[string]any{"image_url": "u"}, outTypes, nil, 1)
	if _, ok := got["duration"]; ok {
		t.Fatalf("drop_postfill must leave duration absent: %#v", got)
	}

	// A typed-nil output is still missing: materialise an untyped null.
	typedNil := on.PostFillOutputs(map[string]any{"duration": []any(nil), "image_url": "u"}, outTypes, nil, 1)
	if v, ok := typedNil["duration"]; !ok || v != nil {
		t.Fatalf("typed-nil duration must become null: %#v", typedNil)
	}
}

// ES-075 negative control: merging absent/null removes the distinction ES-001
// depends on.
func TestP1a2ES075MergeAbsentNull(t *testing.T) {
	decl := map[string]*spec.TypeSpec{
		"absent_s": tspec("string", nil),
		"null_s":   tspec("string", nil),
	}
	in := map[string]any{"null_s": nil}

	_, defaultRecs, err := fillWith(t, DefaultPolicy(), decl, in)
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	if !hasRule(defaultRecs, "absent_scalar_zero") || !hasRule(defaultRecs, "null_scalar_zero") {
		t.Fatalf("default policy must distinguish absent/null: %v", fillRules(defaultRecs))
	}

	pol, err := LoadPolicy(filepath.Join("testdata", "policy", "merge_absent_null.yaml"))
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	_, mergedRecs, err := fillWith(t, pol, decl, in)
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	if countRule(mergedRecs, "absent_scalar_zero") != 0 {
		t.Fatalf("merged policy must lose absent rule: %v", fillRules(mergedRecs))
	}
	if countRule(mergedRecs, "null_scalar_zero") != 2 {
		t.Fatalf("merged policy must collapse both to null rule: %v", fillRules(mergedRecs))
	}
}

// ES-098: required is unverified by default; only an explicit policy fixture
// enforces it.
func TestP1a2ES098RequiredUnverifiedByDefault(t *testing.T) {
	decl := map[string]*spec.TypeSpec{"a": {Kind: "string", Required: true}}
	out, _, err := fillWith(t, DefaultPolicy(), decl, map[string]any{})
	if err != nil {
		t.Fatalf("default policy must not enforce required (unverified): %v", err)
	}
	if out["a"] != "" {
		t.Fatalf("default required fill = %#v, want zero", out["a"])
	}

	pol, err := LoadPolicy(filepath.Join("testdata", "policy", "required.yaml"))
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	_, _, err = fillWith(t, pol, decl, map[string]any{})
	var req *RequiredError
	if !errors.As(err, &req) {
		t.Fatalf("required fixture must enforce required, got %v", err)
	}
}

// ES-071/ES-090: the production policy loader fails loud for a missing or
// incomplete policy, and accepts the generated node_kinds view.
func TestP1a2PolicyLoaderFailsLoud(t *testing.T) {
	if _, err := LoadPolicy(filepath.Join("testdata", "policy", "does-not-exist.yaml")); err == nil {
		t.Fatal("missing policy file must error")
	}
	if _, err := LoadPolicy(filepath.Join("testdata", "policy", "missing_policy.yaml")); err == nil {
		t.Fatal("incomplete policy must error")
	}
	pol, err := LoadPolicy(filepath.Join("testdata", "policy", "node_kinds_view.yaml"))
	if err != nil {
		t.Fatalf("node_kinds view: %v", err)
	}
	if pol.PreFill != PreFillZero || pol.PostFill != PostFillNil {
		t.Fatalf("node_kinds view policy = %+v", pol)
	}
}

// The P1a-2 flip: containers no longer absorb nil; scalars do.
func TestP1a2AbsorbsNilFlip(t *testing.T) {
	for _, k := range []string{"list", "object"} {
		if AbsorbsNil(k) {
			t.Fatalf("%s must NOT absorb nil (zero publishes null)", k)
		}
	}
	for _, k := range []string{"string", "integer", "number", "boolean", "time", "file"} {
		if !AbsorbsNil(k) {
			t.Fatalf("%s must absorb nil into its scalar zero", k)
		}
	}
}
