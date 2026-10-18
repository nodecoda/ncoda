package binding

// Minimal standard-library registry ported from
// src/nclang/lang/stdlib/registry.py (251 lines). Only the surface the
// semantic checks (and later stdlib passes) need is ported: the immutable
// StdlibFunctionSpec manifest with qualified_name / parameters /
// static_parameter_positions / return_type. The registry file also owns
// stdlib_call_path parity — StdlibCallPath lives in binder.go (line 1043)
// and is exported for the passes.
//
// Deferred (recorded, not ported in Task 17A): StdlibManifestContract,
// load_stdlib_program / get_stdlib_template (bundled .ncoda template
// loading) and the manifest/template drift validation — those belong to the
// stdlib lowering task. stdlib_namespace_type (StdlibNamespaceType,
// function_type) was ported in Task 24b so the `std` namespace symbol and
// its calls type through the manifest. The Python get_stdlib_function
// raises KeyError for unknown entries; the Go analogue returns an error so
// the passes can skip gracefully instead of crashing a whole compilation
// (deviation, recorded).

import (
	"fmt"
	"sort"

	"github.com/nodecoda/ncoda/pkg/types"
)

// StdlibParameterSpec mirrors StdlibParameterSpec (registry.py:34-41):
// name, type_name, required (default True), static (default False).
type StdlibParameterSpec struct {
	Name     string
	TypeName string
	Required bool
	Static   bool
}

// StdlibFunctionSpec mirrors StdlibFunctionSpec (registry.py:43-66):
// version, name, parameters, return_type_name.
type StdlibFunctionSpec struct {
	Version        string
	Name           string
	Parameters     []StdlibParameterSpec
	ReturnTypeName string
}

// QualifiedName mirrors the qualified_name property (registry.py:51-53):
// "std.<version>.<name>".
func (s *StdlibFunctionSpec) QualifiedName() string {
	return "std." + s.Version + "." + s.Name
}

// StaticParameterPositions mirrors the static_parameter_positions property
// (registry.py:55-57): the indices of parameters whose config values must
// be compile-time string literals.
func (s *StdlibFunctionSpec) StaticParameterPositions() []int {
	positions := []int{}
	for index, parameter := range s.Parameters {
		if parameter.Static {
			positions = append(positions, index)
		}
	}
	return positions
}

// ReturnType mirrors the return_type() method (registry.py:62-64), resolving
// the return type name through the _descriptor_for_name constructor table
// (registry.py:207-216).
func (s *StdlibFunctionSpec) ReturnType() *types.Descriptor {
	return stdlibDescriptorForName(s.ReturnTypeName)
}

// FunctionType mirrors the function_type() method (registry.py:56-60): the
// function descriptor the namespace record carries for this spec (one
// parameter descriptor per manifest parameter plus the return descriptor).
func (s *StdlibFunctionSpec) FunctionType() *types.Descriptor {
	params := make([]types.Descriptor, 0, len(s.Parameters))
	for _, parameter := range s.Parameters {
		params = append(params, *stdlibDescriptorForName(parameter.TypeName))
	}
	return types.FunctionType(params, stdlibDescriptorForName(s.ReturnTypeName))
}

// stdlibAPIVersion mirrors STDLIB_API_VERSION (registry.py:23).
const stdlibAPIVersion = "v1"

// stdlibManifestSchemaVersion mirrors STDLIB_MANIFEST_SCHEMA_VERSION
// (registry.py:24).
const stdlibManifestSchemaVersion = 1

// v1Manifest mirrors _V1_MANIFEST (registry.py:100-133): the exact ordered
// public v1 function table. All parameters are required (default True); the
// static positions are dataset_ids/model for rag_answer and url/model for
// fetch_and_summarize.
var v1Manifest = []*StdlibFunctionSpec{
	{
		Version: "v1",
		Name:    "rag_answer",
		Parameters: []StdlibParameterSpec{
			{Name: "query", TypeName: "string", Required: true},
			{Name: "dataset_ids", TypeName: "string", Required: true, Static: true},
			{Name: "model", TypeName: "string", Required: true, Static: true},
		},
		ReturnTypeName: "string",
	},
	{
		Version: "v1",
		Name:    "fetch_and_summarize",
		Parameters: []StdlibParameterSpec{
			{Name: "url", TypeName: "string", Required: true, Static: true},
			{Name: "model", TypeName: "string", Required: true, Static: true},
		},
		ReturnTypeName: "string",
	},
}

// stdlibManifests mirrors _MANIFESTS (registry.py:135): the versioned
// manifest table.
var stdlibManifests = map[string][]*StdlibFunctionSpec{
	"v1": v1Manifest,
}

// StdlibNamespaceType mirrors stdlib_namespace_type (registry.py:150-159):
// the nested std.<version>.<function> record descriptor the binder attaches
// to the `std` namespace symbol, so the type pass narrows
// std.v1.rag_answer field accesses to the manifest function descriptors
// (Task 24b ungate). Version keys are sorted so the field order is
// deterministic (Python dict insertion order; Go map iteration is
// unspecified).
func StdlibNamespaceType() *types.Descriptor {
	versions := make([]string, 0, len(stdlibManifests))
	for version := range stdlibManifests {
		versions = append(versions, version)
	}
	sort.Strings(versions)
	fields := make([]types.RecordField, 0, len(versions))
	for _, version := range versions {
		manifest := stdlibManifests[version]
		functionFields := make([]types.RecordField, 0, len(manifest))
		for _, spec := range manifest {
			functionFields = append(functionFields, types.RecordField{Name: spec.Name, Type: spec.FunctionType()})
		}
		fields = append(fields, types.RecordField{Name: version, Type: types.RecordType("std."+version, functionFields)})
	}
	return types.RecordType("std", fields)
}

// GetStdlibManifest mirrors get_stdlib_manifest (registry.py:138-144):
// the ordered immutable manifest for a public API version, or an error for
// an unknown version (Python raises KeyError).
func GetStdlibManifest(version string) ([]*StdlibFunctionSpec, error) {
	manifest, ok := stdlibManifests[version]
	if !ok {
		return nil, fmt.Errorf("Unknown standard-library version: %s", version)
	}
	return manifest, nil
}

// GetStdlibFunction mirrors get_stdlib_function (registry.py:149-154):
// look up one public function specification, or an error when the function
// is not in the manifest (Python raises KeyError).
func GetStdlibFunction(version, name string) (*StdlibFunctionSpec, error) {
	manifest, err := GetStdlibManifest(version)
	if err != nil {
		return nil, err
	}
	for _, spec := range manifest {
		if spec.Name == name {
			return spec, nil
		}
	}
	return nil, fmt.Errorf("Unknown standard-library function: std.%s.%s", version, name)
}

// stdlibDescriptorForName mirrors _descriptor_for_name (registry.py:207-216):
// the closed constructor table for stdlib type names.
func stdlibDescriptorForName(typeName string) *types.Descriptor {
	switch typeName {
	case "string":
		return types.StringType()
	case "int":
		return types.IntType()
	case "float":
		return types.FloatType()
	case "bool":
		return types.BoolType()
	case "file":
		return types.FileType()
	case "any":
		return types.AnyType()
	default:
		panic(fmt.Sprintf("Unsupported standard-library type: %s", typeName))
	}
}
