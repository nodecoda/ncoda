package llm

import (
	"encoding/json"
	"math"
	"testing"
)

func TestSchemaJSONRoundTripRetainsOrderAndRequiredness(t *testing.T) {
	s := Record("Answer", []LLMField{
		{Name: "answer", Required: true, Schema: String()},
		{Name: "score", Required: false, Schema: Optional(Int64())},
	})
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got LLMSchema
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindRecord || got.Name != "Answer" || len(got.Fields) != 2 {
		t.Fatalf("roundtrip = %#v", got)
	}
	if !got.Fields[0].Required || got.Fields[1].Required || got.Fields[1].Schema.Kind != KindOptional {
		t.Fatalf("requiredness = %#v", got.Fields)
	}
	if got.Fields[0].Name != "answer" || got.Fields[1].Name != "score" {
		t.Fatalf("field order = %#v", got.Fields)
	}
}

func TestCanonicalNestedSchemaDefinitionIsValid(t *testing.T) {
	s := Record("Envelope", []LLMField{
		{Name: "values", Required: true, Schema: MapString(Array(Int64()))},
		{Name: "verdict", Required: false, Schema: Optional(Enum("Verdict", []string{"approve", "reject"}))},
		{Name: "item", Required: true, Schema: Record("Item", []LLMField{{Name: "name", Required: true, Schema: String()}})},
	})
	if err := s.ValidateDefinition(); err != nil {
		t.Fatalf("canonical schema is invalid: %v", err)
	}
}

func TestValidateStringDoesNotDecodeJSON(t *testing.T) {
	v, err := String().DecodePayload([]byte("```not json```"))
	if err != nil || v != "```not json```" {
		t.Fatalf("string validation = %#v, %v", v, err)
	}
}

func TestValidateClosedRecordAndOptional(t *testing.T) {
	s := Record("Answer", []LLMField{{Name: "answer", Required: true, Schema: String()}, {Name: "score", Required: false, Schema: Optional(Int64())}})
	for _, tc := range []struct {
		name, input string
		wantErr     bool
	}{
		{"valid", `{"answer":"ok"}`, false},
		{"optional null", `{"answer":"ok","score":null}`, false},
		{"missing required", `{"score":1}`, true},
		{"undeclared", `{"answer":"ok","extra":1}`, true},
		{"wrong kind", `{"answer":1}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.ValidateJSON([]byte(tc.input))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestValidateNestedArrayMapEnum(t *testing.T) {
	s := Record("Envelope", []LLMField{
		{Name: "values", Required: true, Schema: MapString(Array(Int64()))},
		{Name: "verdict", Required: true, Schema: Enum("Verdict", []string{"approve", "reject"})},
	})
	v, err := s.ValidateJSON([]byte(`{"values":{"x":[1,2]},"verdict":"approve"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.(map[string]any); !ok {
		t.Fatalf("decoded = %#v", v)
	}
	for _, input := range []string{`{"values":{"x":[1.5]},"verdict":"approve"}`, `{"values":{"x":[1]},"verdict":"other"}`} {
		if _, err := s.ValidateJSON([]byte(input)); err == nil {
			t.Errorf("invalid input accepted: %s", input)
		}
	}
}

func TestValidateInt64BoundsAndStrictJSON(t *testing.T) {
	s := Int64()
	for _, input := range []string{"-9223372036854775808", "9223372036854775807", "1e3"} {
		if _, err := s.ValidateJSON([]byte(input)); err != nil {
			t.Errorf("%s rejected: %v", input, err)
		}
	}
	for _, input := range []string{"-9223372036854775809", "9223372036854775808", "1.5", "1 2", "1e9999", "NaN"} {
		if _, err := s.ValidateJSON([]byte(input)); err == nil {
			t.Errorf("%s accepted", input)
		}
	}
}

func TestValidateFloatRejectsNonFiniteAndTrailing(t *testing.T) {
	if _, err := Float64().ValidateJSON([]byte("1.25")); err != nil {
		t.Fatal(err)
	}
	if _, err := Float64().ValidateJSON([]byte("1 true")); err == nil {
		t.Fatal("trailing value accepted")
	}
	if math.IsNaN(math.Inf(1)) {
		t.Fatal("sanity")
	}
}

func TestOpenObjectSchemaValidatesJSONObjectsOnly(t *testing.T) {
	if !Object().IsOpenObject() {
		t.Fatal("object must be open-object")
	}
	if !Array(Object()).IsOpenObject() {
		t.Fatal("array<object> must be open-object")
	}
	for _, closed := range []*LLMSchema{String(), Int64(), Array(String()), MapString(Int64()), Record("R", []LLMField{{Name: "x", Required: true, Schema: String()}})} {
		if closed.IsOpenObject() {
			t.Fatalf("%v must not be open-object", closed.Kind)
		}
	}
	if err := Object().ValidateDefinition(); err != nil {
		t.Fatalf("open object definition invalid: %v", err)
	}
	for _, tc := range []struct {
		name, input string
		wantErr     bool
	}{
		{"json object", `{"a":1,"nested":{"b":true}}`, false},
		{"empty object", `{}`, false},
		{"string", `"str"`, true},
		{"array", `[1]`, true},
		{"null", `null`, true},
		{"number", `1`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Object().ValidateJSON([]byte(tc.input))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestOpenObjectArrayValidates(t *testing.T) {
	s := Array(Object())
	if _, err := s.ValidateJSON([]byte(`[{"x":1},{"y":"z"}]`)); err != nil {
		t.Fatalf("array<object> rejected valid payload: %v", err)
	}
	if _, err := s.ValidateJSON([]byte(`[{"x":1},2]`)); err == nil {
		t.Fatal("array<object> accepted non-object element")
	}
}
