// Package analyzer ports nclang.analyzer (src/nclang/analyzer.py 534 lines)
// plus its three dependency modules — type_system.py, flow_graph.py and
// symbol_table.py — as a zero-dependency Go analysis stage (Task 36). The
// analyzer runs on the lowered dify.WorkflowAST (the graph shape that
// compile_cli.py hands to analyze(), compile_cli.py:100-128) and produces
// the same diagnostics with the same messages, severities, node_id/path
// values and pass ordering.
package analyzer

// type_system.go ports src/nclang/type_system.py (300 lines): the DifyType
// coarse analysis types, compatibility rule, and per-node output inference.

import (
	"strings"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
)

// DifyType is one of the coarse Dify analysis types (type_system.py:15-26).
// The Go value IS the wire string (Python's enum .value), so diagnostics
// interpolate it directly.
type DifyType string

const (
	TypeString  DifyType = "string"
	TypeNumber  DifyType = "number"
	TypeInteger DifyType = "integer"
	TypeBoolean DifyType = "boolean"
	TypeObject  DifyType = "object"
	TypeArray   DifyType = "array"
	TypeFile    DifyType = "file"
	TypeSecret  DifyType = "secret"
	TypeAny     DifyType = "any"
)

// TypedOutput mirrors type_system.py TypedOutput (:30-35).
type TypedOutput struct {
	Name           string
	DType          DifyType
	SourceNode     string
	SourceNodeType string
}

var difyTypeNames = map[string]DifyType{
	"string":  TypeString,
	"number":  TypeNumber,
	"integer": TypeInteger,
	"boolean": TypeBoolean,
	"object":  TypeObject,
	"array":   TypeArray,
	"file":    TypeFile,
	"secret":  TypeSecret,
	"any":     TypeAny,
}

// difyTypeFromName ports dify_type_from_name (type_system.py:37-41):
// strip/lower, "array[...]" → ARRAY, unknown → ANY.
func difyTypeFromName(typeName string) DifyType {
	normalized := strings.TrimSpace(strings.ToLower(typeName))
	if strings.HasPrefix(normalized, "array[") {
		return TypeArray
	}
	if t, ok := difyTypeNames[normalized]; ok {
		return t
	}
	return TypeAny
}

// areTypesCompatible ports are_types_compatible (type_system.py:43-48):
// ANY at either end → true; equal → true; INTEGER → NUMBER → true.
func areTypesCompatible(actual, expected DifyType) bool {
	if actual == TypeAny || expected == TypeAny {
		return true
	}
	if actual == expected {
		return true
	}
	return actual == TypeInteger && expected == TypeNumber
}

type typedOutputPair struct {
	name  string
	dtype DifyType
}

// nodeOutputTypes ports NODE_OUTPUT_TYPES (type_system.py:57-98): the
// generic per-node-type output table, consulted when a node has no
// type-carrying definition (start/code/tool/parameter-extractor etc. are
// handled by their own inference functions first). Empty entries in the
// reference are omitted — lookup misses behave identically.
var nodeOutputTypes = map[string][]typedOutputPair{
	"llm": {
		{"text", TypeString},
		{"usage", TypeObject},
		{"reasoning_content", TypeString},
		{"finish_reason", TypeString},
	},
	"http-request": {
		{"response", TypeObject},
		{"headers", TypeObject},
		{"status_code", TypeInteger},
		{"body", TypeString},
		{"error_message", TypeString},
		{"error_type", TypeString},
	},
	"template-transform": {
		{"output", TypeString},
	},
	"knowledge-retrieval": {
		{"result", TypeArray},
		{"records", TypeArray},
	},
	"question-classifier": {
		{"topic_name", TypeString},
		{"topic_id", TypeString},
	},
	"iteration": {
		{"item", TypeAny},
		{"output", TypeArray},
	},
	"document-extractor": {
		{"text", TypeString},
		{"files", TypeArray},
	},
	"list-operator": {
		{"result", TypeArray},
		{"first_record", TypeObject},
	},
	"agent": {
		{"text", TypeString},
		{"usage", TypeObject},
		{"files", TypeArray},
	},
	"human-input": {
		{"inputs", TypeObject},
	},
}

// inferCodeOutputs ports infer_code_outputs (type_system.py:102-125). The
// Go side always passes the map[string]dify.OutputDefNode form.
func inferCodeOutputs(outputDefs map[string]dify.OutputDefNode) []TypedOutput {
	typeMap := map[string]DifyType{
		"string":  TypeString,
		"number":  TypeNumber,
		"integer": TypeInteger,
		"boolean": TypeBoolean,
		"object":  TypeObject,
		"array":   TypeArray,
		"file":    TypeFile,
	}
	var result []TypedOutput
	for name, defn := range outputDefs {
		result = append(result, TypedOutput{
			Name:           name,
			DType:          getWithDefault(typeMap, defn.Type, TypeAny),
			SourceNode:     "",
			SourceNodeType: "code",
		})
	}
	return result
}

// inferStartOutputs ports infer_start_outputs (type_system.py:127-147).
func inferStartOutputs(variables []dify.VariableDef) []TypedOutput {
	typeMap := map[string]DifyType{
		"string":        TypeString,
		"number":        TypeNumber,
		"integer":       TypeInteger,
		"boolean":       TypeBoolean,
		"object":        TypeObject,
		"array[string]": TypeArray,
		"array[number]": TypeArray,
		"array[file]":   TypeArray,
		"file":          TypeFile,
	}
	var result []TypedOutput
	for _, v := range variables {
		result = append(result, TypedOutput{
			Name:           v.Variable,
			DType:          getWithDefault(typeMap, v.Type, TypeAny),
			SourceNode:     "",
			SourceNodeType: "start",
		})
	}
	return result
}

var parameterExtractorTypes = map[string]DifyType{
	"string":         TypeString,
	"select":         TypeString,
	"number":         TypeNumber,
	"boolean":        TypeBoolean,
	"bool":           TypeBoolean,
	"array[string]":  TypeArray,
	"array[number]":  TypeArray,
	"array[boolean]": TypeArray,
	"array[object]":  TypeArray,
}

// getWithDefault is dict.get(key, default) for a string-keyed map — Go's
// zero-value lookup would silently yield "" (an invalid DifyType) where
// Python defaults to DifyType.ANY (type_system.py:117, :144).
func getWithDefault[T any](m map[string]T, key string, def T) T {
	if v, ok := m[key]; ok {
		return v
	}
	return def
}

// parameterExtractorType ports parameter_extractor_type
// (type_system.py:221-224).
func parameterExtractorType(parameterType string) DifyType {
	if t, ok := parameterExtractorTypes[parameterType]; ok {
		return t
	}
	return TypeAny
}

// inferParameterOutputs ports infer_parameter_outputs (type_system.py:226-
// 250): the parameter outputs followed by the three fixed __-prefixed
// convenience outputs.
func inferParameterOutputs(parameters []dify.ParameterDef) []TypedOutput {
	var result []TypedOutput
	for _, p := range parameters {
		result = append(result, TypedOutput{
			Name:           p.Name,
			DType:          parameterExtractorType(p.Type),
			SourceNode:     "",
			SourceNodeType: "parameter-extractor",
		})
	}
	result = append(result,
		TypedOutput{Name: "__is_success", DType: TypeNumber, SourceNode: "", SourceNodeType: "parameter-extractor"},
		TypedOutput{Name: "__reason", DType: TypeString, SourceNode: "", SourceNodeType: "parameter-extractor"},
		TypedOutput{Name: "__usage", DType: TypeObject, SourceNode: "", SourceNodeType: "parameter-extractor"},
	)
	return result
}
