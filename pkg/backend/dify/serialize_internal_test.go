package dify

// Internal-package tests for the scalar coercion / literal-parsing helpers
// that back SerializeToDict. These lock parity with serializer.py
// _coerce_variable_value (:124-162) and pythonLiteral (:107-112) — R1 fixes
// for valueOrEmpty(unsigned), boolean/number coercion, and token rewriting.

import (
	"strings"

	"github.com/nodecoda/ncoda/pkg/ir"
	"reflect"
	"regexp"
	"testing"
)

// TestCoerceVariableValueParity locks the R1/Minor 5 _coerce_variable_value
// parity gaps: (a) value_type "number" with a bool — Python float(True) ==
// 1.0 → int 1 (serializer.py:98-104); (b) value_type "boolean" with 1.0 —
// Python str(1.0) == "1.0", not in ("true","1"), so the value passes through
// as 1.0 instead of becoming true (serializer.py:117-122).
func TestCoerceVariableValueParity(t *testing.T) {
	cases := []struct {
		name      string
		value     any
		valueType string
		want      any
	}{
		{"number+true", true, "number", 1},
		{"number+false", false, "number", 0},
		{"integer+true", true, "integer", 1},
		{"boolean+1.0 passthrough", 1.0, "boolean", 1.0},
		{"boolean+int1", 1, "boolean", true},
		{"boolean+int0", 0, "boolean", false},
		{"boolean+str true", "true", "boolean", true},
		{"boolean+str 0", "0", "boolean", false},
		{"number+string", "42", "number", 42},
		{"number+string float", "4.5", "number", 4.5},
		{"string+empty", "", "string", ""},
		{"string+nonempty", "x", "string", "x"},
		{"string+nil", nil, "string", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := coerceVariableValue(tc.value, tc.valueType)
			if !equalAny(got, tc.want) {
				t.Errorf("coerceVariableValue(%#v, %q) = %#v, want %#v", tc.value, tc.valueType, got, tc.want)
			}
		})
	}
}

// TestValueOrEmptyUnsigned locks the R1/Important 1 regression: isIntKind
// admits unsigned kinds, so valueOrEmpty must route them through reflect
// Uint() — the old fallback called Int() on unsigned values and panicked.
func TestValueOrEmptyUnsigned(t *testing.T) {
	if got := valueOrEmpty(uint(0)); got != "" {
		t.Errorf("valueOrEmpty(uint(0)) = %#v, want \"\"", got)
	}
	if got := valueOrEmpty(uint(5)); !equalAny(got, uint(5)) {
		t.Errorf("valueOrEmpty(uint(5)) = %#v, want uint(5)", got)
	}
	if got := valueOrEmpty(uint64(0)); got != "" {
		t.Errorf("valueOrEmpty(uint64(0)) = %#v, want \"\"", got)
	}
	if got := valueOrEmpty(uint64(9)); !equalAny(got, uint64(9)) {
		t.Errorf("valueOrEmpty(uint64(9)) = %#v, want uint64(9)", got)
	}
	// Through the public coercion path (value_type "string" falls back to
	// valueOrEmpty).
	if got := coerceVariableValue(uint(0), "string"); got != "" {
		t.Errorf("coerceVariableValue(uint(0), \"string\") = %#v, want \"\"", got)
	}
}

// TestPythonLiteralTokensInsideStrings locks the R1/LOW (a) narrowing:
// True/False/None are rewritten only as standalone tokens OUTSIDE string
// literals, so a value like 'True story' or a key like 'TrueData' is never
// corrupted (serializer.py:107-112 fallback path).
func TestPythonLiteralTokensInsideStrings(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  any
	}{
		{"string value with token word", "{'a': 'True story'}", map[string]any{"a": "True story"}},
		{"list of strings", "['True', 'False', 'None']", []any{"True", "False", "None"}},
		{"bare tokens", "[True, False, None]", []any{true, false, nil}},
		{"dict with tokens", "{'a': True, 'b': None}", map[string]any{"a": true, "b": nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pythonLiteral(tc.input)
			if !ok {
				t.Fatalf("pythonLiteral(%q) failed, want ok", tc.input)
			}
			if !equalAny(got, tc.want) {
				t.Errorf("pythonLiteral(%q) = %#v, want %#v", tc.input, got, tc.want)
			}
		})
	}
}

// TestPythonStrParity locks pythonStr against Python str(): integral floats
// keep their ".0" suffix (str(1.0) == "1.0", Go %v prints "1").
func TestPythonStrParity(t *testing.T) {
	if got := pythonStr(1.0); got != "1.0" {
		t.Errorf("pythonStr(1.0) = %q, want \"1.0\"", got)
	}
	if got := pythonStr(1.5); got != "1.5" {
		t.Errorf("pythonStr(1.5) = %q, want \"1.5\"", got)
	}
	if got := pythonStr(true); got != "true" {
		t.Errorf("pythonStr(true) = %q, want \"true\"", got)
	}
	if got := pythonStr(42); got != "42" {
		t.Errorf("pythonStr(42) = %q, want \"42\"", got)
	}
}

// equalAny compares any values via reflect.DeepEqual.
func equalAny(a, b any) bool { return reflect.DeepEqual(a, b) }

// ── I5: dify tool node artifact contract omits `variables` ────────────

// The IR path passes an empty ToolDataNode.Variables and serializeTool
// never emits the field (legacy collectVarRefs filled it for structural
// parity only). The artifact contract carries tool-node dataflow
// exclusively through the tool_parameters {{#...#}} interpolations: the
// frozen oracle 06_tool_use.yml has no variables key on the tool node, and
// dify-compile accepts the omission. Locked here so a future serializer
// change cannot silently reintroduce the field.
func TestSerializeToolOmitsVariablesContract(t *testing.T) {
	call := &ir.ToolCall{
		ID: "tool", ToolName: "google_search", ProviderID: "google_search", Operation: "search",
		Params: []ir.ToolParam{
			{Key: "query", Ref: &ir.Source{BlockID: "start", Name: "query"}},
			{Key: "max_results", Value: int64(5)},
		},
	}
	wf, err := RenderFromIR(toolGraph(call), "workflow")
	if err != nil {
		t.Fatalf("RenderFromIR: %v", err)
	}
	om, err := SerializeToDict(wf)
	if err != nil {
		t.Fatalf("SerializeToDict: %v", err)
	}
	yamlOut, err := SerializeYAML(om)
	if err != nil {
		t.Fatalf("SerializeYAML: %v", err)
	}
	// Locate the tool node block and assert the contract: no variables key,
	// interpolation carries the dataflow ref.
	toolIdx := strings.Index(yamlOut, "tool_name: google_search")
	if toolIdx < 0 {
		t.Fatalf("tool node missing from serialized yaml:\n%s", yamlOut)
	}
	block := yamlOut[toolIdx : toolIdx+600]
	if strings.Contains(block, "variables:") {
		t.Errorf("tool node must not serialize a variables field (I5 contract):\n%s", block)
	}
	refPattern := regexp.MustCompile(`\{\{#start_\d+\.query#\}\}`)
	if !refPattern.MatchString(block) {
		t.Errorf("tool node must carry the dataflow ref in tool_parameters:\n%s", block)
	}
	// The ToolDataNode struct still carries the (unserialized) field empty
	// from the IR renderer — assert the renderer state too.
	data := onlyToolData(t, wf)
	if len(data.Variables) != 0 {
		t.Errorf("IR renderer must pass empty Variables, got %d", len(data.Variables))
	}
}
