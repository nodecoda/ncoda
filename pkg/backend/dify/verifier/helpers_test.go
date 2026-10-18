package verifier

import (
	"reflect"
	"testing"

	"github.com/nodecoda/ncoda/pkg/types"
)

// Helper-function coverage (2026-08-20 test governance): pure helpers the
// rule tests only reach indirectly.

func TestContainsString(t *testing.T) {
	if !containsString([]string{"a", "b"}, "a") {
		t.Error("containsString must find a")
	}
	if containsString([]string{"a", "b"}, "z") {
		t.Error("containsString must miss z")
	}
	if containsString(nil, "z") {
		t.Error("containsString(nil) must miss")
	}
}

func TestDescriptorsEqual(t *testing.T) {
	if !descriptorsEqual(nil, nil) {
		t.Error("nil == nil must be true")
	}
	if descriptorsEqual(nil, types.StringType()) {
		t.Error("nil != string must be true")
	}
	if descriptorsEqual(types.StringType(), nil) {
		t.Error("string != nil must be true")
	}
	if !descriptorsEqual(types.StringType(), types.StringType()) {
		t.Error("string == string must be true")
	}
	if descriptorsEqual(types.StringType(), types.IntType()) {
		t.Error("string != int must be true")
	}
	recA := types.RecordType("", []types.RecordField{{Name: "a", Type: types.StringType()}, {Name: "b", Type: types.IntType()}})
	recB := types.RecordType("", []types.RecordField{{Name: "a", Type: types.StringType()}, {Name: "b", Type: types.IntType()}})
	recC := types.RecordType("", []types.RecordField{{Name: "b", Type: types.IntType()}, {Name: "a", Type: types.StringType()}})
	if !descriptorsEqual(recA, recB) {
		t.Error("structurally equal records must be equal")
	}
	if descriptorsEqual(recA, recC) {
		t.Error("field-ordered records differ (ordered Fields list mirrors Python)")
	}
	arr := types.ArrayType(types.StringType())
	if !descriptorsEqual(arr, types.ArrayType(types.StringType())) {
		t.Error("equal arrays must be equal")
	}
	if descriptorsEqual(arr, types.ArrayType(types.IntType())) {
		t.Error("different element arrays differ")
	}
}

func TestSortedKeysHelpers(t *testing.T) {
	got := sortedKeys(map[string]bool{"b": true, "a": true, "c": true})
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("sortedKeys = %v, want [a b c]", got)
	}
	gotM := sortedKeysMap(map[string]int{"z": 1, "y": 2})
	if !reflect.DeepEqual(gotM, []string{"y", "z"}) {
		t.Errorf("sortedKeysMap = %v, want [y z]", gotM)
	}
	gotD := differenceSortedKeys(map[string]bool{"a": true, "b": true}, map[string]bool{"b": true})
	if !reflect.DeepEqual(gotD, []string{"a"}) {
		t.Errorf("differenceSortedKeys = %v, want [a]", gotD)
	}
}
