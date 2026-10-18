// Package llm contains the platform-neutral semantic contract for LLM
// payloads. It deliberately does not contain target port names or graph
// concepts; those belong to backend lowering.
package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
)

// Kind is the tagged JSON representation used by runtime spec.v2.
type Kind string

const (
	KindString   Kind = "string"
	KindBool     Kind = "bool"
	KindInt64    Kind = "int64"
	KindFloat64  Kind = "float64"
	KindArray    Kind = "array"
	KindMap      Kind = "map_string"
	KindObject   Kind = "object"
	KindOptional Kind = "optional"
	KindEnum     Kind = "enum"
	KindRecord   Kind = "record"
)

// LLMSchema is a lossless, target-independent payload schema. Only fields
// relevant to Kind are populated. Fields and enum Values retain source order.
type LLMSchema struct {
	Kind   Kind       `json:"kind"`
	Name   string     `json:"name,omitempty"`
	Elem   *LLMSchema `json:"elem,omitempty"`
	Value  *LLMSchema `json:"value,omitempty"`
	Values []string   `json:"values,omitempty"`
	Fields []LLMField `json:"fields,omitempty"`
}

type LLMField struct {
	Name     string     `json:"name"`
	Required bool       `json:"required"`
	Schema   *LLMSchema `json:"schema"`
}

func String() *LLMSchema                    { return &LLMSchema{Kind: KindString} }
func Bool() *LLMSchema                      { return &LLMSchema{Kind: KindBool} }
func Int64() *LLMSchema                     { return &LLMSchema{Kind: KindInt64} }
func Float64() *LLMSchema                   { return &LLMSchema{Kind: KindFloat64} }
func Array(elem *LLMSchema) *LLMSchema      { return &LLMSchema{Kind: KindArray, Elem: elem} }
func MapString(value *LLMSchema) *LLMSchema { return &LLMSchema{Kind: KindMap, Value: value} }
func Object() *LLMSchema                    { return &LLMSchema{Kind: KindObject} }

// IsOpenObject reports whether the schema is an open-object payload (object or
// array<object>): the runtime verifies each value is a JSON object but does not
// commit to keys or value types. Open-object payloads require no closed
// field-level structured output proof, so external profiles may compile them
// without an integrated structured-generation capability.
func (s *LLMSchema) IsOpenObject() bool {
	if s == nil {
		return false
	}
	switch s.Kind {
	case KindObject:
		return true
	case KindArray:
		return s.Elem != nil && s.Elem.Kind == KindObject
	default:
		return false
	}
}
func Optional(value *LLMSchema) *LLMSchema { return &LLMSchema{Kind: KindOptional, Value: value} }
func Enum(name string, values []string) *LLMSchema {
	return &LLMSchema{Kind: KindEnum, Name: name, Values: append([]string(nil), values...)}
}
func Record(name string, fields []LLMField) *LLMSchema {
	return &LLMSchema{Kind: KindRecord, Name: name, Fields: append([]LLMField(nil), fields...)}
}

// ValidateDefinition checks that the tagged schema is structurally complete
// before a runtime or backend treats it as executable type authority.
func (s *LLMSchema) ValidateDefinition() error {
	return s.validateDefinition("payload schema")
}

func (s *LLMSchema) validateDefinition(path string) error {
	if s == nil {
		return fmt.Errorf("%s is missing", path)
	}
	switch s.Kind {
	case KindString, KindBool, KindInt64, KindFloat64:
		return nil
	case KindArray:
		if s.Elem == nil {
			return fmt.Errorf("%s array element schema is missing", path)
		}
		return s.Elem.validateDefinition(path + " element")
	case KindMap:
		if s.Value == nil {
			return fmt.Errorf("%s map value schema is missing", path)
		}
		return s.Value.validateDefinition(path + " value")
	case KindObject:
		// open object: self-contained, no sub-schema.
		return nil
	case KindOptional:
		if s.Value == nil {
			return fmt.Errorf("%s optional value schema is missing", path)
		}
		return s.Value.validateDefinition(path + " value")
	case KindEnum:
		if s.Name == "" || len(s.Values) == 0 {
			return fmt.Errorf("%s enum requires a name and wire values", path)
		}
		seen := map[string]bool{}
		for _, value := range s.Values {
			if value == "" || seen[value] {
				return fmt.Errorf("%s enum contains an empty or duplicate wire value", path)
			}
			seen[value] = true
		}
		return nil
	case KindRecord:
		if s.Name == "" || len(s.Fields) == 0 {
			return fmt.Errorf("%s record requires a name and fields", path)
		}
		seen := map[string]bool{}
		for _, field := range s.Fields {
			if field.Name == "" || seen[field.Name] {
				return fmt.Errorf("%s record contains an empty or duplicate field name", path)
			}
			seen[field.Name] = true
			if field.Schema == nil {
				return fmt.Errorf("%s field %q schema is missing", path, field.Name)
			}
			if field.Required == (field.Schema.Kind == KindOptional) {
				return fmt.Errorf("%s field %q requiredness disagrees with optional schema", path, field.Name)
			}
			if err := field.Schema.validateDefinition(path + " field " + field.Name); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("%s has unknown kind %q", path, s.Kind)
	}
}

// Validate decodes one JSON value and validates it against the schema. The
// returned value uses Go scalar/container values and retains map/field order
// only where the Go representation permits it. Object duplicates follow the
// encoding/json last-value-wins behavior.
func (s *LLMSchema) ValidateJSON(data []byte) (any, error) {
	if s == nil {
		return nil, fmt.Errorf("nil llm schema")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing JSON value")
		}
		return nil, fmt.Errorf("trailing data after JSON value")
	}
	return s.validate(raw, "$")
}

// DecodePayload applies the LLM contract: string payloads are primary model
// text and therefore remain unchanged, while every other payload is strict
// JSON validated by ValidateJSON.
func (s *LLMSchema) DecodePayload(data []byte) (any, error) {
	if s != nil && s.Kind == KindString {
		return string(data), nil
	}
	return s.ValidateJSON(data)
}

func (s *LLMSchema) validate(v any, path string) (any, error) {
	if s == nil {
		return nil, fmt.Errorf("%s: nil schema", path)
	}
	if s.Kind == KindOptional {
		if v == nil {
			return nil, nil
		}
		return s.Value.validate(v, path)
	}
	switch s.Kind {
	case KindString:
		if x, ok := v.(string); ok {
			return x, nil
		}
	case KindBool:
		if x, ok := v.(bool); ok {
			return x, nil
		}
	case KindInt64:
		if n, ok := v.(json.Number); ok {
			i, err := exactInt64(string(n))
			if err == nil {
				return i, nil
			}
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	case KindFloat64:
		if n, ok := v.(json.Number); ok {
			x, err := strconv.ParseFloat(string(n), 64)
			if err == nil && !math.IsNaN(x) && !math.IsInf(x, 0) {
				return x, nil
			}
		}
	case KindEnum:
		if x, ok := v.(string); ok {
			for _, value := range s.Values {
				if x == value {
					return x, nil
				}
			}
			return nil, fmt.Errorf("%s: enum value %q is not declared", path, x)
		}
	case KindArray:
		if arr, ok := v.([]any); ok {
			out := make([]any, len(arr))
			for i, item := range arr {
				converted, err := s.Elem.validate(item, fmt.Sprintf("%s[%d]", path, i))
				if err != nil {
					return nil, err
				}
				out[i] = converted
			}
			return out, nil
		}
	case KindObject:
		// open object: verify the value is a JSON object; keys and
		// value types are not statically committed.
		if obj, ok := v.(map[string]any); ok {
			return obj, nil
		}
	case KindMap:
		if obj, ok := v.(map[string]any); ok {
			out := make(map[string]any, len(obj))
			for key, item := range obj {
				converted, err := s.Value.validate(item, path+"."+key)
				if err != nil {
					return nil, err
				}
				out[key] = converted
			}
			return out, nil
		}
	case KindRecord:
		if obj, ok := v.(map[string]any); ok {
			known := make(map[string]LLMField, len(s.Fields))
			for _, f := range s.Fields {
				known[f.Name] = f
			}
			for key := range obj {
				if _, ok := known[key]; !ok {
					return nil, fmt.Errorf("%s: undeclared field %q", path, key)
				}
			}
			out := make(map[string]any, len(s.Fields))
			for _, f := range s.Fields {
				item, present := obj[f.Name]
				if !present {
					if f.Required {
						return nil, fmt.Errorf("%s: missing required field %q", path, f.Name)
					}
					continue
				}
				converted, err := f.Schema.validate(item, path+"."+f.Name)
				if err != nil {
					return nil, err
				}
				out[f.Name] = converted
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("%s: expected %s, got %T", path, s.Kind, v)
}

func exactInt64(raw string) (int64, error) {
	r := new(big.Rat)
	if _, ok := r.SetString(raw); !ok || !r.IsInt() {
		return 0, fmt.Errorf("expected exact int64")
	}
	if !r.Num().IsInt64() {
		return 0, fmt.Errorf("int64 out of range")
	}
	return r.Num().Int64(), nil
}
