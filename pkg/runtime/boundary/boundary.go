package boundary

// boundary.go implements the single ValueBoundary consumed by the code node.
// Shaping/zero-fill/postfill lives here only: internal/runtime/nodes must not
// carry a second shaping implementation (ES-017a/ES-077).

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/trace"
)

// Options configures one node's boundary instance.
type Options struct {
	Policy Policy
	NodeID string
	Kind   string
	Emit   func(trace.Record)
}

// ValueBoundary applies resolve/coerce/fill/postfill to node values.
type ValueBoundary struct {
	policy Policy
	nodeID string
	kind   string
	emit   func(trace.Record)
}

// New builds a boundary from options. Empty policy fields fall back to the
// coze-biz defaults; a nil Emit means tracing is off and changes nothing.
func New(o Options) *ValueBoundary {
	return &ValueBoundary{
		policy: o.Policy.normalized(),
		nodeID: o.NodeID,
		kind:   o.Kind,
		emit:   o.Emit,
	}
}

// Policy returns the effective policy.
func (b *ValueBoundary) Policy() Policy { return b.policy }

// RequiredError is returned when a required declaration is absent and the
// policy enables required enforcement. It is wrapped with RInputMissing by the
// node; the required check happens inside fill, before any zero is applied.
type RequiredError struct {
	Port string
	Kind string
}

func (e *RequiredError) Error() string {
	return fmt.Sprintf("required input %q (%s) is missing", e.Port, e.Kind)
}

// FillInputs resolves and fills declared inputs. Undeclared keys pass through
// untouched. Absent/null/value stay distinguishable in trace, while published
// values are tri-state only (value, scalar zero, or null).
func (b *ValueBoundary) FillInputs(in map[string]any, decl map[string]*spec.TypeSpec, attempt int) (map[string]any, error) {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	if len(decl) == 0 {
		return out, nil
	}
	for _, k := range sortedTypeKeys(decl) {
		ts := decl[k]
		if ts == nil {
			continue
		}
		v, present := in[k]
		if present && !isNilValue(v) {
			coerced, rule, changed := b.coerceValue(ts, v)
			if coerced == nil {
				// A typed-nil container ([]any(nil) / map[string]any(nil) or
				// JSON "null") is container zero: publish null, do not invent
				// an empty container.
				b.fillMissing(out, k, ts, true, attempt)
				continue
			}
			out[k] = coerced
			origin := trace.OriginWirePresent
			decision := "passed"
			if changed {
				decision = "coerced"
			}
			if rule == "" {
				rule = "value_passthrough"
			} else if rule == ruleArrayElemZero || rule == ruleArrayElemTypedNil || rule == ruleArrayElemUnknown {
				origin = trace.OriginPolicy
			}
			b.emitFill(k, rule, origin, decision, attempt)
			continue
		}
		if ts.Required && b.policy.EnforceRequired {
			b.emitFill(k, "required_missing", trace.OriginPolicy, "error", attempt)
			return nil, &RequiredError{Port: k, Kind: ts.Kind}
		}
		b.fillMissing(out, k, ts, present, attempt)
	}
	return out, nil
}

func (b *ValueBoundary) fillMissing(out map[string]any, key string, ts *spec.TypeSpec, present bool, attempt int) {
	// Negative-control knob: merging the two states must make ES-001 red.
	absent := !present && !b.policy.MergeAbsentNull
	switch b.policy.PreFill {
	case PreFillNone:
		// Leave the key exactly as it arrived (absent stays absent).
		if present {
			out[key] = nil
		}
		b.emitFill(key, "prefill_none", trace.OriginPolicy, "skipped", attempt)
		return
	case PreFillNil:
		out[key] = nil
		rule := "null_prefill_nil"
		if absent {
			rule = "absent_prefill_nil"
		}
		b.emitFill(key, rule, trace.OriginPolicy, "filled", attempt)
		return
	}

	if z, ok := zeroValue(ts.Kind); ok {
		out[key] = z
		rule := "null_scalar_zero"
		if absent {
			rule = "absent_scalar_zero"
		}
		b.emitFill(key, rule, trace.OriginPolicy, "filled", attempt)
		return
	}
	if isContainer(ts.Kind) {
		// Container default-zero is published as null, not []/{} (ES-004/ES-014).
		out[key] = nil
		b.emitFill(key, "container_zero_published_null", trace.OriginPolicy, "filled", attempt)
		return
	}
	// Unknown kinds are not invented; keep null and record the gap honestly.
	out[key] = nil
	b.emitFill(key, "unknown_kind_nil", trace.OriginPolicy, "gap", attempt)
}

// PostFillOutputs materialises declared output ports after invoke. The key set
// is driven by OutTypes (ES-032b/ES-107a); when OutTypes is absent the legacy
// OutOrder set is used and a gap record is emitted.
func (b *ValueBoundary) PostFillOutputs(out map[string]any, outTypes map[string]*spec.TypeSpec, outOrder []string, attempt int) map[string]any {
	if out == nil {
		out = map[string]any{}
	}
	keys, gap := declaredOutputKeys(outTypes, outOrder)
	if gap {
		b.emitTrace(trace.Record{
			Stage:    "postfill",
			RuleID:   trace.RulePostFill,
			Origin:   trace.OriginPolicy,
			Evidence: "observed:p1a2:" + b.nodeLabel() + ".out_types_missing",
			Node:     b.nodeID,
			Kind:     b.kind,
			FillRule: "postfill_out_types_gap",
			Attempt:  attempt,
			Input:    "<out_types>",
			Output:   "gap",
			Decision: "gap",
		})
	}
	if len(keys) == 0 {
		return out
	}
	if b.policy.PostFill == PostFillNone {
		b.emitFillPostFill("<declared_ports>", "postfill_none", "skipped", attempt)
		return out
	}
	for _, k := range keys {
		if v, ok := out[k]; ok && !isNilValue(v) {
			continue
		}
		out[k] = nil
		b.emitFillPostFill(k, "postfill_nil", "filled", attempt)
	}
	return out
}

func (b *ValueBoundary) emitFillPostFill(key, rule, decision string, attempt int) {
	b.emitTrace(trace.Record{
		Stage:    "postfill",
		RuleID:   trace.RulePostFill,
		Origin:   trace.OriginPolicy,
		Evidence: PostFillEvidence,
		Node:     b.nodeID,
		Kind:     b.kind,
		FillRule: rule,
		Attempt:  attempt,
		Axis:     "per_attempt",
		Input:    key,
		Output:   decision,
		Decision: decision,
	})
}

const (
	ruleArrayElemZero     = "array_elem_zero"
	ruleArrayElemTypedNil = "array_elem_typed_nil"
	ruleArrayElemUnknown  = "array_elem_unknown"
)

// coerceValue aligns a present value to its declared shape. It never invents a
// container for nil: nil is handled by fill. Empty containers stay empty.
func (b *ValueBoundary) coerceValue(ts *spec.TypeSpec, v any) (any, string, bool) {
	switch ts.Kind {
	case "list":
		arr, changed, rule := b.coerceList(ts.Elem, v)
		if arr == nil {
			return nil, "container_zero_published_null", true
		}
		return arr, rule, changed
	case "object":
		obj, changed := coerceObject(v)
		if obj == nil {
			return nil, "container_zero_published_null", true
		}
		// ES-005: declared object members are materialised with isInsideObject
		// semantics. A missing member key exists with its zero (container zero
		// publishes null, never an invented []/{}); undeclared members pass
		// through untouched.
		if b.fillDeclaredMembers(ts, obj) {
			changed = true
		}
		return obj, "coerce_object", changed
	case "string", "time", "file":
		if s, ok := v.(string); ok {
			return s, "", false
		}
		return stringify(v), "coerce_string", true
	case "integer", "number", "boolean":
		return v, "", false
	}
	return v, "", false
}

func (b *ValueBoundary) coerceList(elem *spec.TypeSpec, v any) ([]any, bool, string) {
	var arr []any
	matched := false
	changed := false
	rule := ""
	switch x := v.(type) {
	case []any:
		arr = x
		matched = true
	case string:
		if parsed, err := parseJSONArray(x); err == nil {
			arr = parsed
			matched = true
			changed = true
			rule = "coerce_list_from_json"
		}
	default:
		if flat, ok := anySlice(v); ok {
			arr = flat
			matched = true
			changed = true
			rule = "coerce_list_from_slice"
		}
	}
	if !matched {
		// Not a list shape: wrap so .map/.forEach can at least iterate.
		return []any{v}, true, "coerce_list_wrap"
	}
	if arr == nil {
		// Typed nil (or JSON "null") is container zero, not an empty list.
		return nil, false, ""
	}
	if b.policy.PreFill != PreFillZero {
		// Dropping the prefill-zero policy also drops element zero fill:
		// array<integer>[nil,1] stays [null,1] (ES-073 red control).
		return arr, changed, rule
	}
	filled, elemChanged, elemRule := fillListElems(elem, arr)
	if elemChanged {
		changed = true
	}
	if elemRule != "" {
		rule = elemRule
	}
	return filled, changed, rule
}

func fillListElems(elem *spec.TypeSpec, arr []any) ([]any, bool, string) {
	out := make([]any, len(arr))
	copy(out, arr)
	changed := false
	rule := ""
	for i, el := range out {
		if el != nil {
			continue
		}
		if elem == nil {
			rule = ruleArrayElemUnknown
			continue
		}
		if isContainer(elem.Kind) {
			// Typed nil: array<object{...}> keeps null (ES-004/ES-011).
			rule = ruleArrayElemTypedNil
			continue
		}
		z, ok := zeroValue(elem.Kind)
		if !ok {
			rule = ruleArrayElemUnknown
			continue
		}
		out[i] = z
		changed = true
		rule = ruleArrayElemZero
	}
	return out, changed, rule
}

// fillDeclaredMembers materialises declared object props (ES-005). It mirrors
// the OSS isInsideObject=true branch: every declared member key exists, and a
// missing container member is null (container zero), not []/{}. Only declared
// props are touched; recursion covers object-valued members and object
// elements of arrays.
func (b *ValueBoundary) fillDeclaredMembers(ts *spec.TypeSpec, obj map[string]any) bool {
	if ts == nil || len(ts.Props) == 0 || obj == nil {
		return false
	}
	changed := false
	for _, k := range sortedTypeKeys(ts.Props) {
		pt := ts.Props[k]
		if pt == nil {
			continue
		}
		v, present := obj[k]
		if present && !isNilValue(v) {
			if nv, nested := b.fillNestedMembers(pt, v); nested {
				obj[k] = nv
				changed = true
			}
			continue
		}
		if b.policy.PreFill == PreFillNone {
			// Absent stays absent; an explicit null stays null.
			continue
		}
		if b.policy.PreFill == PreFillNil {
			obj[k] = nil
			changed = true
			continue
		}
		if z, ok := zeroValue(pt.Kind); ok {
			obj[k] = z
		} else {
			// Container/unknown zero publishes null, never []/{} (ES-004).
			obj[k] = nil
		}
		changed = true
	}
	return changed
}

// fillNestedMembers recurses into a present member value. Null containers and
// null elements are zero and stay null (ES-004/ES-011); they are never
// invented into empty containers.
func (b *ValueBoundary) fillNestedMembers(ts *spec.TypeSpec, v any) (any, bool) {
	if ts == nil {
		return v, false
	}
	switch ts.Kind {
	case "object":
		m, ok := anyStringMap(v)
		if !ok {
			return v, false
		}
		if b.fillDeclaredMembers(ts, m) {
			return m, true
		}
		return v, false
	case "list":
		arr, ok := anySlice(v)
		if !ok || ts.Elem == nil {
			return v, false
		}
		changed := false
		for i, el := range arr {
			if isNilValue(el) {
				continue
			}
			if nv, nested := b.fillNestedMembers(ts.Elem, el); nested {
				arr[i] = nv
				changed = true
			}
		}
		if changed {
			return arr, true
		}
		return v, false
	}
	return v, false
}

func coerceObject(v any) (map[string]any, bool) {
	switch x := v.(type) {
	case map[string]any:
		return x, false
	case string:
		if m, err := parseJSONObject(x); err == nil {
			return m, true
		}
	default:
		if m, ok := anyStringMap(v); ok {
			return m, true
		}
	}
	return map[string]any{"value": v}, true
}

func (b *ValueBoundary) emitFill(key, rule string, origin trace.Origin, decision string, attempt int) {
	evidence := b.policy.Evidence
	if origin == trace.OriginWirePresent {
		evidence = "observed:p1a2:" + b.nodeLabel() + ".inputs." + key
	}
	b.emitTrace(trace.Record{
		Stage:    "fill",
		RuleID:   trace.RuleFill,
		Origin:   origin,
		Evidence: evidence,
		Node:     b.nodeID,
		Kind:     b.kind,
		FillRule: rule,
		Attempt:  attempt,
		Axis:     "per_attempt",
		Input:    key,
		Output:   decision,
		Decision: decision,
	})
}

func (b *ValueBoundary) emitTrace(r trace.Record) {
	if b.emit == nil {
		return
	}
	b.emit(r)
}

func (b *ValueBoundary) nodeLabel() string {
	if b.nodeID == "" {
		return "?"
	}
	return b.nodeID
}

// declaredOutputKeys returns the authoritative output port set. OutTypes wins;
// OutOrder is the legacy fallback and sets gap=true so trace records the
// missing declaration.
func declaredOutputKeys(outTypes map[string]*spec.TypeSpec, outOrder []string) ([]string, bool) {
	if len(outTypes) > 0 {
		return sortedTypeKeys(outTypes), false
	}
	if len(outOrder) == 0 {
		return nil, false
	}
	seen := map[string]bool{}
	var keys []string
	for _, k := range outOrder {
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		keys = append(keys, k)
	}
	return keys, true
}

// AbsorbsNil reports whether a nil value is converted to a non-nil value by the
// fill rules. Scalar zeros are absorbed; container zero is published as null,
// so containers do NOT absorb nil (P1a-2 flip).
func AbsorbsNil(kind string) bool {
	switch kind {
	case "string", "integer", "number", "boolean", "time", "file":
		return true
	}
	return false
}

// Zero returns the language zero for a declared port type. It is the single
// home for "declared but absent ⇒ zero" (ES-017b): nodes must not carry their
// own zero tables. Unsupported/nil types report ok=false so the key stays
// absent (nil semantics).
func Zero(ts *spec.TypeSpec) (any, bool) {
	if ts == nil {
		return nil, false
	}
	return zeroValue(ts.Kind)
}

// zeroValue is the kind-keyed table behind Zero. "int"/"float"/"double" are
// accepted as aliases because spec producers (spec.TypeSpec) use both the
// language spellings and the JSON-schema ones.
func zeroValue(kind string) (any, bool) {
	switch kind {
	case "string", "time", "file":
		return "", true
	case "integer", "int":
		return int64(0), true
	case "number", "float", "double":
		return float64(0), true
	case "boolean":
		return false, true
	}
	return nil, false
}

// isNilValue treats typed nil containers/pointers as null for output
// materialisation (a port carrying []any(nil) is still a missing value).
func isNilValue(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map, reflect.Ptr, reflect.Interface, reflect.Func, reflect.Chan:
		return rv.IsNil()
	}
	return false
}

func isContainer(kind string) bool {
	return kind == "list" || kind == "object"
}

func sortedTypeKeys(m map[string]*spec.TypeSpec) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func anySlice(v any) ([]any, bool) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil, false
	}
	k := rv.Kind()
	if k != reflect.Slice && k != reflect.Array {
		return nil, false
	}
	out := make([]any, rv.Len())
	for i := range out {
		out[i] = rv.Index(i).Interface()
	}
	return out, true
}

func anyStringMap(v any) (map[string]any, bool) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil, false
	}
	out := make(map[string]any, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		out[iter.Key().String()] = iter.Value().Interface()
	}
	return out, true
}

func parseJSONArray(s string) ([]any, error) {
	var a []any
	err := json.Unmarshal([]byte(s), &a)
	return a, err
}

func parseJSONObject(s string) (map[string]any, error) {
	var m map[string]any
	err := json.Unmarshal([]byte(s), &m)
	return m, err
}

func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case int64:
		return strconv.FormatInt(x, 10)
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(b)
	}
}
