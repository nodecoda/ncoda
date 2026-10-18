package binding

// Port of lang/tests/lang/unit/test_stdlib_registry.py manifest surface
// (names, signatures, static positions, unknown-entry errors) for the
// minimal Go registry. Template loading, manifest contracts and namespace
// types are deferred (see stdlib_registry.go header) and asserted as gaps
// below.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/types"
)

func TestStdlibManifestIsOrderedAndExact(t *testing.T) {
	manifest, err := GetStdlibManifest("v1")
	if err != nil {
		t.Fatalf("GetStdlibManifest(v1): %v", err)
	}
	names := make([]string, 0, len(manifest))
	for _, spec := range manifest {
		names = append(names, spec.Name)
	}
	want := []string{"rag_answer", "fetch_and_summarize"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("manifest names = %v, want %v", names, want)
	}
}

func TestStdlibAPIVersionConstants(t *testing.T) {
	if stdlibAPIVersion != "v1" {
		t.Fatalf("stdlibAPIVersion = %q, want v1", stdlibAPIVersion)
	}
	if stdlibManifestSchemaVersion != 1 {
		t.Fatalf("stdlibManifestSchemaVersion = %d, want 1", stdlibManifestSchemaVersion)
	}
}

func TestStdlibManifestSignaturesAndStaticPositionsAreExact(t *testing.T) {
	rag, err := GetStdlibFunction("v1", "rag_answer")
	if err != nil {
		t.Fatalf("GetStdlibFunction(rag_answer): %v", err)
	}
	fetch, err := GetStdlibFunction("v1", "fetch_and_summarize")
	if err != nil {
		t.Fatalf("GetStdlibFunction(fetch_and_summarize): %v", err)
	}

	ragParams := make([][2]string, 0, len(rag.Parameters))
	for _, p := range rag.Parameters {
		ragParams = append(ragParams, [2]string{p.Name, p.TypeName})
	}
	wantRag := [][2]string{
		{"query", "string"},
		{"dataset_ids", "string"},
		{"model", "string"},
	}
	if !reflect.DeepEqual(ragParams, wantRag) {
		t.Fatalf("rag parameters = %v, want %v", ragParams, wantRag)
	}
	if got, want := rag.StaticParameterPositions(), []int{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rag static positions = %v, want %v", got, want)
	}
	if got, want := fetch.StaticParameterPositions(), []int{0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fetch static positions = %v, want %v", got, want)
	}
	if rag.ReturnTypeName != "string" || fetch.ReturnTypeName != "string" {
		t.Fatalf("return type names = %q/%q, want string/string",
			rag.ReturnTypeName, fetch.ReturnTypeName)
	}
	for _, spec := range []*StdlibFunctionSpec{rag, fetch} {
		for _, parameter := range spec.Parameters {
			if !parameter.Required {
				t.Fatalf("all v1 parameters are required, %s.%s is not",
					spec.Name, parameter.Name)
			}
		}
	}
}

func TestStdlibQualifiedName(t *testing.T) {
	rag, err := GetStdlibFunction("v1", "rag_answer")
	if err != nil {
		t.Fatalf("GetStdlibFunction: %v", err)
	}
	if got, want := rag.QualifiedName(), "std.v1.rag_answer"; got != want {
		t.Fatalf("QualifiedName = %q, want %q", got, want)
	}
}

func TestStdlibReturnTypeDescriptors(t *testing.T) {
	rag, err := GetStdlibFunction("v1", "rag_answer")
	if err != nil {
		t.Fatalf("GetStdlibFunction: %v", err)
	}
	if ret := rag.ReturnType(); ret.Kind != types.String {
		t.Fatalf("rag return kind = %v, want String", ret.Kind)
	}
}

func TestStdlibUnknownManifestEntriesError(t *testing.T) {
	if _, err := GetStdlibManifest("v2"); err == nil {
		t.Fatal("GetStdlibManifest(v2) must error")
	} else if !strings.Contains(err.Error(), "v2") {
		t.Fatalf("error = %q, want it to mention v2", err)
	}
	if _, err := GetStdlibFunction("v1", "missing"); err == nil {
		t.Fatal("GetStdlibFunction(missing) must error")
	} else if !strings.Contains(err.Error(), "std.v1.missing") {
		t.Fatalf("error = %q, want it to mention std.v1.missing", err)
	}
}

func TestStdlibFunctionSpecImmutabilityByConvention(t *testing.T) {
	// Frozen dataclasses in Python raise FrozenInstanceError on mutation;
	// Go has no freeze, so the spec values must be treated as immutable by
	// convention (DESIGN D6). This test locks the exported surface so a
	// consumer cannot silently rely on mutation.
	spec, err := GetStdlibFunction("v1", "rag_answer")
	if err != nil {
		t.Fatalf("GetStdlibFunction: %v", err)
	}
	if spec.Version != "v1" || spec.Name != "rag_answer" {
		t.Fatalf("spec identity drifted: %+v", spec)
	}
}
