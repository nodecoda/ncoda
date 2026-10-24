package nodes

// exec_sem_p1a2_test.go — P1a-2 code-node integration gates.
//
// These tests drive the real registry builder, the production policy loader,
// and the real trace callback. Boundary-only L1/L2 gates live in
// internal/runtime/boundary/boundary_test.go.

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nodecoda/ncoda/pkg/runtime/boundary"
	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/trace"
)

func buildP1a2Code(t *testing.T, id string, cfg *spec.CodeConfig, semTrace func(trace.Record)) NodeImpl {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	impl, err := StdRegistry().Build(context.Background(),
		&spec.NodeSpec{ID: id, Kind: spec.KindCode, Config: raw},
		Deps{SemTrace: semTrace})
	if err != nil {
		t.Fatalf("build code node: %v", err)
	}
	return impl
}

func buildP1a2CodeErr(cfg *spec.CodeConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = StdRegistry().Build(context.Background(),
		&spec.NodeSpec{ID: "code_err", Kind: spec.KindCode, Config: raw}, Deps{})
	return err
}

func collectTrace(t *testing.T) (func(trace.Record), *[]trace.Record) {
	t.Helper()
	var recs []trace.Record
	return func(r trace.Record) {
		if err := r.Validate(); err != nil {
			t.Fatalf("invalid trace record: %v (%+v)", err, r)
		}
		recs = append(recs, r)
	}, &recs
}

func hasTraceRule(recs []trace.Record, rule string) bool {
	for _, r := range recs {
		if r.FillRule == rule {
			return true
		}
	}
	return false
}

func traceCount(recs []trace.Record, rule string) int {
	n := 0
	for _, r := range recs {
		if r.FillRule == rule {
			n++
		}
	}
	return n
}

func policyPath(name string) string {
	return filepath.Join("..", "boundary", "testdata", "policy", name)
}

// ES-010: a declared array<integer> input [nil,1] reaches user code as [0,1]
// and the trace names array_elem_zero.
func TestP1a2ES010CodeArrayIntNilElements(t *testing.T) {
	emit, recs := collectTrace(t)
	cfg := &spec.CodeConfig{
		Code:      "def main(x) -> dict:\n    return {\"result\": x}",
		Language:  "python",
		Synthetic: true,
		Types: map[string]*spec.TypeSpec{
			"x": {Kind: "list", Elem: &spec.TypeSpec{Kind: "integer"}},
		},
		OutOrder: []string{"result"},
	}
	n := buildP1a2Code(t, "code_010", cfg, emit)
	out, err := n.Invoke(context.Background(), map[string]any{"x": []any{nil, int64(1)}})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	want := []any{int64(0), int64(1)}
	if !reflect.DeepEqual(out["result"], want) {
		t.Fatalf("user code received %#v, want %#v", out["result"], want)
	}
	if !hasTraceRule(*recs, "array_elem_zero") {
		t.Fatalf("trace missing array_elem_zero: %+v", *recs)
	}
}

// ES-012: a declared output port the code did not return exists with value null.
func TestP1a2ES012PostFillNilMissingOutput(t *testing.T) {
	emit, recs := collectTrace(t)
	cfg := &spec.CodeConfig{
		Code:      "def main(x) -> dict:\n    return {\"image_url\": x}",
		Language:  "python",
		Synthetic: true,
		OutTypes: map[string]*spec.TypeSpec{
			"duration":  {Kind: "integer"},
			"image_url": {Kind: "string"},
		},
	}
	n := buildP1a2Code(t, "code_012", cfg, emit)
	out, err := n.Invoke(context.Background(), map[string]any{"x": "u"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	v, ok := out["duration"]
	if !ok {
		t.Fatalf("declared output duration is absent: %#v", out)
	}
	if v != nil {
		t.Fatalf("duration = %#v, want null", v)
	}
	if out["image_url"] != "u" {
		t.Fatalf("image_url = %#v", out["image_url"])
	}
	if !hasTraceRule(*recs, "postfill_nil") {
		t.Fatalf("trace missing postfill_nil: %+v", *recs)
	}
}

// ES-014: declared-but-absent container input is published as null, not [].
func TestP1a2ES014CodeAbsentContainerInput(t *testing.T) {
	emit, recs := collectTrace(t)
	cfg := &spec.CodeConfig{
		Code:      "def main(textList) -> dict:\n    return {\"result\": textList}",
		Language:  "python",
		Synthetic: true,
		Types: map[string]*spec.TypeSpec{
			"textList": {Kind: "list", Elem: &spec.TypeSpec{Kind: "string"}},
		},
		OutOrder: []string{"result"},
	}
	n := buildP1a2Code(t, "code_014", cfg, emit)
	out, err := n.Invoke(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if v, ok := out["result"]; !ok || v != nil {
		t.Fatalf("user code result = %#v (ok=%v), want explicit null", v, ok)
	}
	found := false
	for _, r := range *recs {
		if r.FillRule == "container_zero_published_null" {
			found = true
			if r.Origin != trace.OriginPolicy {
				t.Fatalf("origin = %q, want policy", r.Origin)
			}
		}
	}
	if !found {
		t.Fatalf("missing container_zero_published_null record: %+v", *recs)
	}
}

// ES-016: required is enforced inside fill (policy fixture), before zero fill.
func TestP1a2ES016CodeRequiredPolicy(t *testing.T) {
	t.Setenv(boundary.EnvPolicyPath, policyPath("required.yaml"))
	cfg := &spec.CodeConfig{
		Code:      "def main(a) -> dict:\n    return {\"result\": a}",
		Language:  "python",
		Synthetic: true,
		Types:     map[string]*spec.TypeSpec{"a": {Kind: "string", Required: true}},
		OutOrder:  []string{"result"},
	}
	n := buildP1a2Code(t, "code_016", cfg, nil)
	_, err := n.Invoke(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("required missing input must fail")
	}
	if got := diag.CodeOf(err); got != diag.RInputMissing {
		t.Fatalf("err code = %v, want RInputMissing (%v)", got, err)
	}
}

// ES-023a: retry attempts share the total timeout budget; trace attempt
// increments with axis=total. The existing code contract is MaxRetry extra
// attempts (retryTimes=3 ⇒ 4 attempts); this test locks the shared budget, not
// a redefinition of retryTimes.
func TestP1a2ES023aCodeRetrySharedTotalBudget(t *testing.T) {
	emit, recs := collectTrace(t)
	cfg := &spec.CodeConfig{
		Code:      "def main(x) -> dict:\n    return {\"result\": missing_name}",
		Language:  "python",
		Synthetic: true,
		Exception: &spec.ExceptionConfig{MaxRetry: 3, TimeoutMS: 60000},
		OutOrder:  []string{"result"},
	}
	n := buildP1a2Code(t, "code_023a", cfg, emit)
	if _, err := n.Invoke(context.Background(), map[string]any{"x": "v"}); err == nil {
		t.Fatal("undefined shim name must fail")
	}
	var attempts []int
	for _, r := range *recs {
		if r.Stage != "retry" {
			continue
		}
		attempts = append(attempts, r.Attempt)
		if r.Axis != "total" {
			t.Fatalf("retry axis = %q, want total", r.Axis)
		}
		if r.DeadlineMS != 60000 {
			t.Fatalf("retry deadline = %d, want shared 60000", r.DeadlineMS)
		}
	}
	want := []int{1, 2, 3, 4} // retryTimes=3 ⇒ 1 + 3 attempts (existing contract)
	if !reflect.DeepEqual(attempts, want) {
		t.Fatalf("attempt trace = %v, want %v", attempts, want)
	}
}

// ES-024: the diagnostic reports the declared budget and never calls it a
// stub-added safety net.
func TestP1a2ES024DiagnosticReportsDeclaredBudget(t *testing.T) {
	msg := codeTimeoutMessage("python", 60*time.Second, false)
	// The diagnostic renders the declared duration in Go's human form; the
	// contract is "reports the declared budget", not a specific unit.
	if !strings.Contains(msg, (60 * time.Second).String()) {
		t.Fatalf("diagnostic must report the declared budget: %q", msg)
	}
	if strings.Contains(msg, StubBudgetTimeoutMarker) {
		t.Fatalf("declared budget must not carry the stub marker: %q", msg)
	}
	if strings.Contains(msg, "declares no timeout_ms") {
		t.Fatalf("declared budget must not claim no timeout_ms: %q", msg)
	}
	if !strings.Contains(msg, "budget exhausted") {
		t.Fatalf("declared budget exhaustion must be explicit: %q", msg)
	}
}

// ES-042: a declared 60000 budget is reported by the timeout trace.
func TestP1a2ES042DeclaredTimeoutBudget(t *testing.T) {
	emit, recs := collectTrace(t)
	cfg := &spec.CodeConfig{
		Code:      "def main(x) -> dict:\n    return {\"result\": x}",
		Language:  "python",
		Synthetic: true,
		Exception: &spec.ExceptionConfig{TimeoutMS: 60000},
		OutOrder:  []string{"result"},
	}
	n := buildP1a2Code(t, "code_042", cfg, emit)
	if _, err := n.Invoke(context.Background(), map[string]any{"x": "v"}); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	found := false
	for _, r := range *recs {
		if r.Stage == "timeout" {
			found = true
			if r.DeadlineMS != 60000 {
				t.Fatalf("deadline = %d, want 60000", r.DeadlineMS)
			}
		}
	}
	if !found {
		t.Fatalf("missing timeout trace: %+v", *recs)
	}
}

// ES-032b: OutTypes drives the PostFillNil key set.
func TestP1a2ES032bOutTypesDrivesPostFillNil(t *testing.T) {
	emit, recs := collectTrace(t)
	cfg := &spec.CodeConfig{
		Code:      "def main(x) -> dict:\n    return {\"a\": x}",
		Language:  "python",
		Synthetic: true,
		OutTypes: map[string]*spec.TypeSpec{
			"a": {Kind: "string"},
			"b": {Kind: "integer"},
			"c": {Kind: "list", Elem: &spec.TypeSpec{Kind: "string"}},
		},
	}
	n := buildP1a2Code(t, "code_032b", cfg, emit)
	out, err := n.Invoke(context.Background(), map[string]any{"x": "v"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	for _, k := range []string{"a", "b", "c"} {
		if _, ok := out[k]; !ok {
			t.Fatalf("OutTypes port %q missing from output: %#v", k, out)
		}
	}
	if out["b"] != nil || out["c"] != nil {
		t.Fatalf("missing declared ports must be null: %#v", out)
	}
	if n := traceCount(*recs, "postfill_nil"); n != 2 {
		t.Fatalf("postfill_nil records = %d, want 2 (one per missing declared port): %+v", n, *recs)
	}
	for _, r := range *recs {
		if r.FillRule == "postfill_nil" && r.RuleID != trace.RulePostFill {
			t.Fatalf("postfill rule_id = %q, want %q", r.RuleID, trace.RulePostFill)
		}
	}
}

// ES-043: all declared ports are present after invoke (no downstream KeyError).
func TestP1a2ES043AllDeclaredPortsPresent(t *testing.T) {
	cfg := &spec.CodeConfig{
		Code:      "def main(x) -> dict:\n    return {\"a\": x}",
		Language:  "python",
		Synthetic: true,
		OutTypes: map[string]*spec.TypeSpec{
			"a": {Kind: "string"},
			"b": {Kind: "integer"},
			"c": {Kind: "string"},
		},
	}
	n := buildP1a2Code(t, "code_043", cfg, nil)
	out, err := n.Invoke(context.Background(), map[string]any{"x": "v"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	got := make([]string, 0, len(out))
	for k := range out {
		got = append(got, k)
	}
	sort.Strings(got)
	for _, k := range []string{"a", "b", "c"} {
		if _, ok := out[k]; !ok {
			t.Fatalf("declared port %q absent; output keys = %v", k, got)
		}
	}
}

// ES-040: the corpus failure chain. A missing duration becomes explicit null;
// the downstream array<integer> input then fills null elements to 0.
func TestP1a2ES040MissingDurationBecomesZeroElements(t *testing.T) {
	producer := &spec.CodeConfig{
		Code:      "def main(x) -> dict:\n    return {\"image_url\": x}",
		Language:  "python",
		Synthetic: true,
		OutTypes: map[string]*spec.TypeSpec{
			"duration":  {Kind: "integer"},
			"image_url": {Kind: "string"},
		},
	}
	pn := buildP1a2Code(t, "code_040_producer", producer, nil)
	pout, err := pn.Invoke(context.Background(), map[string]any{"x": "u"})
	if err != nil {
		t.Fatalf("producer invoke: %v", err)
	}
	if v, ok := pout["duration"]; !ok || v != nil {
		t.Fatalf("duration = %#v (ok=%v), want explicit null", v, ok)
	}

	durationList := []any{pout["duration"], pout["duration"], pout["duration"]}
	consumer := &spec.CodeConfig{
		Code:      "def main(duration_list) -> dict:\n    return {\"result\": duration_list}",
		Language:  "python",
		Synthetic: true,
		Types: map[string]*spec.TypeSpec{
			"duration_list": {Kind: "list", Elem: &spec.TypeSpec{Kind: "integer"}},
		},
		OutOrder: []string{"result"},
	}
	cn := buildP1a2Code(t, "code_040_consumer", consumer, nil)
	cout, err := cn.Invoke(context.Background(), map[string]any{"duration_list": durationList})
	if err != nil {
		t.Fatalf("consumer invoke: %v", err)
	}
	want := []any{int64(0), int64(0), int64(0)}
	if !reflect.DeepEqual(cout["result"], want) {
		t.Fatalf("downstream received %#v, want %#v", cout["result"], want)
	}
}

// ES-071: the production policy consumer fails loud when the policy file is
// missing; it does not silently fall back.
func TestP1a2ES071MissingPolicyProductionPath(t *testing.T) {
	t.Setenv(boundary.EnvPolicyPath, filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	cfg := &spec.CodeConfig{Code: "def main() -> dict:\n    return {\"result\": 1}", Language: "python", Synthetic: true}
	err := buildP1a2CodeErr(cfg)
	if err == nil {
		t.Fatal("missing policy must fail at the production builder")
	}
	if !strings.Contains(err.Error(), "execution policy") {
		t.Fatalf("error does not identify the policy: %v", err)
	}
}

// ES-073: dropping PreFillZero on the production path changes array element
// fill (negative control turns ES-010 red).
func TestP1a2ES073DropPreFillZeroProductionPath(t *testing.T) {
	t.Setenv(boundary.EnvPolicyPath, policyPath("drop_prefill.yaml"))
	cfg := &spec.CodeConfig{
		Code:      "def main(x) -> dict:\n    return {\"result\": x}",
		Language:  "python",
		Synthetic: true,
		Types: map[string]*spec.TypeSpec{
			"x": {Kind: "list", Elem: &spec.TypeSpec{Kind: "integer"}},
		},
		OutOrder: []string{"result"},
	}
	n := buildP1a2Code(t, "code_073", cfg, nil)
	out, err := n.Invoke(context.Background(), map[string]any{"x": []any{nil, int64(1)}})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	want := []any{nil, int64(1)}
	if !reflect.DeepEqual(out["result"], want) {
		t.Fatalf("drop_prefill output = %#v, want %#v (ES-010 must be red)", out["result"], want)
	}
}

// ES-074/ES-090: dropping PostFillNil on the production path leaves the
// declared port absent; the node_kinds view is accepted as a valid policy.
func TestP1a2ES074DropPostFillNilProductionPath(t *testing.T) {
	cfg := &spec.CodeConfig{
		Code:      "def main(x) -> dict:\n    return {\"image_url\": x}",
		Language:  "python",
		Synthetic: true,
		OutTypes: map[string]*spec.TypeSpec{
			"duration":  {Kind: "integer"},
			"image_url": {Kind: "string"},
		},
	}

	t.Setenv(boundary.EnvPolicyPath, policyPath("drop_postfill.yaml"))
	n := buildP1a2Code(t, "code_074", cfg, nil)
	out, err := n.Invoke(context.Background(), map[string]any{"x": "u"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if _, ok := out["duration"]; ok {
		t.Fatalf("drop_postfill must leave duration absent: %#v", out)
	}

	t.Setenv(boundary.EnvPolicyPath, policyPath("node_kinds_view.yaml"))
	n = buildP1a2Code(t, "code_090", cfg, nil)
	out, err = n.Invoke(context.Background(), map[string]any{"x": "u"})
	if err != nil {
		t.Fatalf("invoke with node_kinds view: %v", err)
	}
	if v, ok := out["duration"]; !ok || v != nil {
		t.Fatalf("node_kinds view must materialise duration=null: %#v", out)
	}
}

// ES-077/ES-017a: nodes imports the single boundary and keeps no second shaping
// implementation.
func TestP1a2ES017aImportBoundaryBoundary(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	banned := map[string]bool{
		"shapeValue": true, "normalizeInputs": true, "anySlice": true,
		"anyStringMap": true, "stringify": true, "parseJSONArray": true,
		"parseJSONObject": true,
	}
	importsBoundary := false
	fset := token.NewFileSet()
	for _, de := range files {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			if imp.Path.Value == `"github.com/nodecoda/ncoda/pkg/runtime/boundary"` {
				importsBoundary = true
			}
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			if banned[fn.Name.Name] {
				t.Fatalf("%s defines %s: a second shaping implementation is forbidden", name, fn.Name.Name)
			}
		}
	}
	if !importsBoundary {
		t.Fatal("nodes package does not import internal/runtime/boundary")
	}

	// The triage alias must be the flipped semantics.
	if ShapeAbsorbsNil("list") || ShapeAbsorbsNil("object") {
		t.Fatal("ShapeAbsorbsNil(list/object) must be false after P1a-2")
	}
	if !ShapeAbsorbsNil("string") || !ShapeAbsorbsNil("integer") {
		t.Fatal("scalar kinds must still absorb nil")
	}
}

// ES-098: required is unverified by default; an explicit policy fixture
// enforces it.
func TestP1a2ES098RequiredUnverifiedDefault(t *testing.T) {
	cfg := &spec.CodeConfig{
		Code:      "def main(a) -> dict:\n    return {\"result\": a}",
		Language:  "python",
		Synthetic: true,
		Types:     map[string]*spec.TypeSpec{"a": {Kind: "string", Required: true}},
		OutOrder:  []string{"result"},
	}
	n := buildP1a2Code(t, "code_098a", cfg, nil)
	out, err := n.Invoke(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("default policy must not enforce required: %v", err)
	}
	if out["result"] != "" {
		t.Fatalf("default required fill = %#v, want zero", out["result"])
	}

	t.Setenv(boundary.EnvPolicyPath, policyPath("required.yaml"))
	n = buildP1a2Code(t, "code_098b", cfg, nil)
	if _, err := n.Invoke(context.Background(), map[string]any{}); err == nil {
		t.Fatal("required fixture must enforce required")
	} else if diag.CodeOf(err) != diag.RInputMissing {
		t.Fatalf("err code = %v, want RInputMissing", diag.CodeOf(err))
	}
}

// ES-107a: the consumed PostFillNil key set equals OutTypes' key set and the
// trace carries the corresponding rule id.
func TestP1a2ES107aOutTypesKeySetConsumed(t *testing.T) {
	emit, recs := collectTrace(t)
	outTypes := map[string]*spec.TypeSpec{
		"a": {Kind: "string"},
		"b": {Kind: "integer"},
		"c": {Kind: "string"},
	}
	cfg := &spec.CodeConfig{
		Code:      "def main(x) -> dict:\n    return {\"a\": x}",
		Language:  "python",
		Synthetic: true,
		OutTypes:  outTypes,
	}
	n := buildP1a2Code(t, "code_107a", cfg, emit)
	out, err := n.Invoke(context.Background(), map[string]any{"x": "v"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	for k := range outTypes {
		if _, ok := out[k]; !ok {
			t.Fatalf("OutTypes key %q not consumed by PostFillNil: %#v", k, out)
		}
	}
	seenRule := false
	for _, r := range *recs {
		if r.RuleID == trace.RulePostFill && r.FillRule == "postfill_nil" {
			seenRule = true
		}
	}
	if !seenRule {
		t.Fatalf("no postfill rule id in trace: %+v", *recs)
	}
}

// Trace off must change nothing: output and error are byte-identical to a
// traced run (the boundary only observes records).
func TestP1a2TraceClosedZeroChange(t *testing.T) {
	cfg := &spec.CodeConfig{
		Code:      "def main(x) -> dict:\n    return {\"a\": x}",
		Language:  "python",
		Synthetic: true,
		Types: map[string]*spec.TypeSpec{
			"x": {Kind: "list", Elem: &spec.TypeSpec{Kind: "integer"}},
		},
		OutTypes: map[string]*spec.TypeSpec{
			"a": {Kind: "list", Elem: &spec.TypeSpec{Kind: "integer"}},
			"b": {Kind: "integer"},
		},
	}
	traced := buildP1a2Code(t, "code_trace_on", cfg, func(trace.Record) {})
	closed := buildP1a2Code(t, "code_trace_off", cfg, nil)
	in := map[string]any{"x": []any{nil, int64(1)}}

	a, errA := traced.Invoke(context.Background(), in)
	b, errB := closed.Invoke(context.Background(), in)
	if (errA == nil) != (errB == nil) {
		t.Fatalf("trace changed error behavior: %v vs %v", errA, errB)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("trace changed output:\n on=%#v\n off=%#v", a, b)
	}
}

// When OutTypes is absent the legacy OutOrder set is used, and the gap is
// recorded in trace instead of being silently invented.
func TestP1a2OutOrderFallbackRecordsGap(t *testing.T) {
	emit, recs := collectTrace(t)
	cfg := &spec.CodeConfig{
		Code:      "def main(x) -> dict:\n    return {\"a\": x}",
		Language:  "python",
		Synthetic: true,
		OutOrder:  []string{"a", "b"},
	}
	n := buildP1a2Code(t, "code_fallback", cfg, emit)
	out, err := n.Invoke(context.Background(), map[string]any{"x": "v"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if v, ok := out["b"]; !ok || v != nil {
		t.Fatalf("OutOrder fallback must materialise b=null: %#v", out)
	}
	if !hasTraceRule(*recs, "postfill_out_types_gap") {
		t.Fatalf("missing postfill_out_types_gap trace: %+v", *recs)
	}
}
