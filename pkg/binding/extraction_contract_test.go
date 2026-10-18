package binding

// Whitebox lock for the extraction contract resolution ported from
// src/nclang/lang/extraction_contract.py: the four build_extraction_contract
// ValueError texts (:52-67) asserted byte-exact, plus the positive path
// (result-type record shape, ordered fields, immutable-descriptor sharing).
// Constructs the contract types directly — no parse needed.

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/types"
)

// travelRecord mirrors the fixture's TravelRequest record (declaration order
// city/days/flexible/tags).
func travelRecord() *types.Descriptor {
	return types.RecordType("TravelRequest", []types.RecordField{
		{Name: "city", Type: types.StringType()},
		{Name: "days", Type: types.FloatType()},
		{Name: "flexible", Type: types.BoolType()},
		{Name: "tags", Type: types.ArrayType(types.StringType())},
	})
}

func TestBuildExtractionContract(t *testing.T) {
	record := travelRecord()
	enumType := types.EnumType("TravelMode", []string{"air", "rail"})
	namedTypes := map[string]*types.Descriptor{"TravelRequest": record, "TravelMode": enumType}

	t.Run("non-named-ref", func(t *testing.T) {
		_, err := BuildExtractionContract(ast.NewPrimTypeRef("string"), namedTypes)
		wantErr(t, err, "extraction schema must be a named record type")
	})

	t.Run("unknown-schema", func(t *testing.T) {
		_, err := BuildExtractionContract(ast.NewNamedTypeRef("Missing"), namedTypes)
		wantErr(t, err, "unknown extraction schema 'Missing'")
	})

	t.Run("non-record", func(t *testing.T) {
		_, err := BuildExtractionContract(ast.NewNamedTypeRef("TravelMode"), namedTypes)
		wantErr(t, err, "extraction schema 'TravelMode' must be a record")
	})

	t.Run("empty-record", func(t *testing.T) {
		empty := types.RecordType("Empty", nil)
		_, err := BuildExtractionContract(ast.NewNamedTypeRef("Empty"), map[string]*types.Descriptor{"Empty": empty})
		wantErr(t, err, "extraction schema 'Empty' must contain at least one field")
	})

	t.Run("positive", func(t *testing.T) {
		c, err := BuildExtractionContract(ast.NewNamedTypeRef("TravelRequest"), namedTypes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.SchemaName != "TravelRequest" {
			t.Fatalf("schema_name = %q, want TravelRequest", c.SchemaName)
		}
		if c.ValueType != record {
			t.Fatalf("value_type identity lost: %v", c.ValueType)
		}
		if len(c.Fields) != 4 {
			t.Fatalf("fields = %d, want 4", len(c.Fields))
		}
		wantNames := []string{"city", "days", "flexible", "tags"}
		for i, want := range wantNames {
			if c.Fields[i].Name != want {
				t.Errorf("fields[%d].name = %q, want %q", i, c.Fields[i].Name, want)
			}
		}
		// result_type = record Extraction[TravelRequest] {value, ok, reason,
		// usage} in declaration order.
		if c.ResultType.Kind != types.Record {
			t.Fatalf("result_type = %v, want record", c.ResultType)
		}
		wantFields := []struct {
			name string
			typ  *types.Descriptor
		}{
			{"value", record},
			{"ok", types.BoolType()},
			{"reason", types.StringType()},
			{"usage", types.MapType(types.StringType(), types.AnyType())},
		}
		for i, want := range wantFields {
			if c.ResultType.Fields[i].Name != want.name || !c.ResultType.Fields[i].Type.Equal(want.typ) {
				t.Errorf("result_type.fields[%d] = %s/%v, want %s/%v",
					i, c.ResultType.Fields[i].Name, c.ResultType.Fields[i].Type, want.name, want.typ)
			}
		}
	})
}
