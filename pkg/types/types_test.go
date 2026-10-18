package types

// Ports of the Python reference type-system suite
// (lang/tests/lang/unit/test_dsl_types.py, 252 lines) plus the one
// any-strictness rule that belongs to this layer (any is an explicit target
// only, never an implicit source conversion). The ANY_NOT_NARROWED diagnostic
// cases from test_any_strictness.py belong to the passes layer, not here.
// Group and case names mirror the Python classes so parity is auditable.

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
)

func mustEqual(t *testing.T, got, want *Descriptor, label string) {
	t.Helper()
	if !got.Equal(want) {
		t.Errorf("%s: got %+v, want %+v", label, got, want)
	}
}

func mustNil(t *testing.T, got *Descriptor, label string) {
	t.Helper()
	if got != nil {
		t.Errorf("%s: got %+v, want nil", label, got)
	}
}

func record(fields ...RecordField) *Descriptor {
	return RecordType("R", fields)
}

// ── TestTypeDescriptor ──

func TestDSLTypeNames(t *testing.T) {
	cases := map[DSLType]string{
		String: "string", Int: "int", Float: "float", Bool: "bool",
		File: "file", Void: "void", Any: "any", Null: "null",
		Optional: "optional", Array: "array", Map: "map",
		Record: "record", Enum: "enum", Function: "function",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("DSLType(%d).String() = %q, want %q", int(k), got, want)
		}
	}
}

func TestPrimitiveDescriptors(t *testing.T) {
	for _, c := range []struct {
		label string
		got   *Descriptor
		want  DSLType
	}{
		{"string", StringType(), String},
		{"int", IntType(), Int},
		{"float", FloatType(), Float},
		{"bool", BoolType(), Bool},
		{"file", FileType(), File},
		{"void", VoidType(), Void},
		{"any", AnyType(), Any},
		{"null", NullType(), Null},
	} {
		if c.got.Kind != c.want {
			t.Errorf("%s: Kind = %v, want %v", c.label, c.got.Kind, c.want)
		}
	}
}

func TestArrayDescriptor(t *testing.T) {
	arr := ArrayType(StringType())
	if arr.Kind != Array {
		t.Fatalf("Kind = %v, want array", arr.Kind)
	}
	mustEqual(t, arr.Elem, StringType(), "array element")
}

func TestMapDescriptor(t *testing.T) {
	m := MapType(StringType(), IntType())
	if m.Kind != Map {
		t.Fatalf("Kind = %v, want map", m.Kind)
	}
	mustEqual(t, m.Key, StringType(), "map key")
	mustEqual(t, m.Val, IntType(), "map value")
}

func TestRecordDescriptor(t *testing.T) {
	s := RecordType("Point", []RecordField{{Name: "x", Type: FloatType()}, {Name: "y", Type: FloatType()}})
	if s.Kind != Record {
		t.Fatalf("Kind = %v, want record", s.Kind)
	}
	if len(s.Fields) != 2 {
		t.Fatalf("Fields len = %d, want 2", len(s.Fields))
	}
	if s.Fields[0].Name != "x" {
		t.Errorf("Fields[0].Name = %q, want %q", s.Fields[0].Name, "x")
	}
	mustEqual(t, s.Fields[1].Type, FloatType(), "field y type")
}

func TestFunctionDescriptor(t *testing.T) {
	fn := FunctionType([]Descriptor{*StringType(), *IntType()}, StringType())
	if fn.Kind != Function {
		t.Fatalf("Kind = %v, want function", fn.Kind)
	}
	if len(fn.Params) != 2 {
		t.Fatalf("Params len = %d, want 2", len(fn.Params))
	}
	mustEqual(t, &fn.Params[0], StringType(), "param 0")
	mustEqual(t, fn.Result, StringType(), "result")
}

func TestEnumDescriptor(t *testing.T) {
	enum := EnumType("TravelMode", []string{"air", "rail"})
	if enum.Kind != Enum {
		t.Fatalf("Kind = %v, want enum", enum.Kind)
	}
	if enum.EnumName != "TravelMode" {
		t.Errorf("EnumName = %q, want %q", enum.EnumName, "TravelMode")
	}
	if len(enum.EnumMembers) != 2 || enum.EnumMembers[0] != "air" || enum.EnumMembers[1] != "rail" {
		t.Errorf("EnumMembers = %v, want [air rail]", enum.EnumMembers)
	}
}

func TestDescriptorEqual(t *testing.T) {
	// Primitives with the same kind are equal.
	mustEqual(t, StringType(), StringType(), "string == string")
	if StringType().Equal(IntType()) {
		t.Error("string == int must be false")
	}
	// Records compare field names and types in order (order-sensitive).
	a := record(RecordField{Name: "x", Type: IntType()}, RecordField{Name: "y", Type: StringType()})
	b := record(RecordField{Name: "x", Type: IntType()}, RecordField{Name: "y", Type: StringType()})
	mustEqual(t, a, b, "record same fields/order")
	reordered := record(RecordField{Name: "y", Type: StringType()}, RecordField{Name: "x", Type: IntType()})
	if a.Equal(reordered) {
		t.Error("records with reordered fields must not be equal")
	}
	dropped := record(RecordField{Name: "x", Type: IntType()})
	if a.Equal(dropped) {
		t.Error("records with different field sets must not be equal")
	}
	// Enums are nominal and member-order-sensitive.
	travel := EnumType("TravelMode", []string{"air", "rail"})
	shipping := EnumType("ShippingMode", []string{"air", "rail"})
	reorderedMembers := EnumType("TravelMode", []string{"rail", "air"})
	mustEqual(t, travel, EnumType("TravelMode", []string{"air", "rail"}), "enum same name/members")
	if travel.Equal(shipping) {
		t.Error("enums with different names must not be equal")
	}
	if travel.Equal(reorderedMembers) {
		t.Error("enums with reordered members must not be equal")
	}
	// Function params are order-sensitive.
	fnA := FunctionType([]Descriptor{*StringType(), *IntType()}, StringType())
	fnB := FunctionType([]Descriptor{*IntType(), *StringType()}, StringType())
	if fnA.Equal(fnB) {
		t.Error("functions with reordered params must not be equal")
	}
	// File constraints compare structurally.
	plain := FileType()
	cImage := FileTypeWithConstraints(FileConstraints{Types: []string{"image"}})
	cImage2 := FileTypeWithConstraints(FileConstraints{Types: []string{"image"}})
	cPdf := FileTypeWithConstraints(FileConstraints{Types: []string{"pdf"}})
	mustEqual(t, cImage, cImage2, "file same constraints")
	if plain.Equal(cImage) {
		t.Error("file with constraints must not equal bare file")
	}
	if cImage.Equal(cPdf) {
		t.Error("file with different constraints must not be equal")
	}
}

// ── Enum members defensive copy (types.py TypeDescriptor.enum snapshots via tuple(members)) ──

func TestEnumDescriptorDefensiveCopy(t *testing.T) {
	members := []string{"air", "rail"}
	enum := EnumType("TravelMode", members)
	// Mutating the caller's slice after construction must not affect the
	// descriptor (Python tuple(members) snapshots; test_enum verifies this).
	members[0] = "road"
	members = append(members, "sea")
	if len(enum.EnumMembers) != 2 || enum.EnumMembers[0] != "air" || enum.EnumMembers[1] != "rail" {
		t.Errorf("EnumMembers changed after input slice mutation: %v", enum.EnumMembers)
	}
}

// ── TestUnwrapOptional ──

func TestUnwrapOptional(t *testing.T) {
	mustEqual(t, UnwrapOptional(OptionalType(StringType())), StringType(), "unwrap one layer")
	mustEqual(t, UnwrapOptional(OptionalType(OptionalType(IntType()))), IntType(), "unwrap nested")
	mustEqual(t, UnwrapOptional(StringType()), StringType(), "unwrap non-optional")
	malformed := &Descriptor{Kind: Optional} // no element: cannot unwrap
	if got := UnwrapOptional(malformed); got != malformed {
		t.Errorf("malformed optional unwrapped to %+v, want itself", got)
	}
}

// ── TestIsAssignable ──

func TestIsAssignable(t *testing.T) {
	cases := []struct {
		name string
		src  *Descriptor
		dst  *Descriptor
		want bool
	}{
		// any strictness: explicit target only, never implicit source
		{"any-is-explicit-target", StringType(), AnyType(), true},
		{"any-is-not-implicit-source", AnyType(), StringType(), false},
		{"any-to-any", AnyType(), AnyType(), true},
		// null requires optional target
		{"null-to-string", NullType(), StringType(), false},
		{"null-to-optional", NullType(), OptionalType(StringType()), true},
		{"string-to-optional", StringType(), OptionalType(StringType()), true},
		{"optional-not-to-string", OptionalType(StringType()), StringType(), false},
		{"optional-to-optional", OptionalType(IntType()), OptionalType(FloatType()), true},
		// widening
		{"int-to-float", IntType(), FloatType(), true},
		{"float-not-to-int", FloatType(), IntType(), false},
		{"string-not-to-int", StringType(), IntType(), false},
		// coze-biz: file is a URL string on the wire (image/audio/video)
		{"file-to-string", FileType(), StringType(), true},
		{"string-not-to-file", StringType(), FileType(), false},
		{"file-not-to-int", FileType(), IntType(), false},
		// arrays recurse
		{"array-same", ArrayType(StringType()), ArrayType(StringType()), true},
		{"array-int-to-array-any", ArrayType(IntType()), ArrayType(AnyType()), true},
		{"array-int-not-to-array-string", ArrayType(IntType()), ArrayType(StringType()), false},
		// maps recurse on key and value
		{"map-same", MapType(StringType(), IntType()), MapType(StringType(), IntType()), true},
		{"map-val-to-any", MapType(StringType(), IntType()), MapType(StringType(), AnyType()), true},
		{"map-key-mismatch", MapType(StringType(), IntType()), MapType(IntType(), IntType()), false},
		// records: source must be a superset of target fields
		{"record-superset", record(RecordField{Name: "x", Type: IntType()}, RecordField{Name: "y", Type: StringType()}), record(RecordField{Name: "x", Type: IntType()}), true},
		{"record-missing-field", record(RecordField{Name: "x", Type: IntType()}), record(RecordField{Name: "x", Type: IntType()}, RecordField{Name: "y", Type: StringType()}), false},
		{"record-field-type-mismatch", record(RecordField{Name: "x", Type: IntType()}), record(RecordField{Name: "x", Type: StringType()}), false},
		// open object: record (fixed-key JSON object) is a subtype of
		// object (open object, LLM-OPEN-OBJECT.md); object is narrower
		// than any but does not promise specific keys.
		{"record-to-object", record(RecordField{Name: "x", Type: IntType()}), ObjectType(), true},
		{"empty-record-to-object", record(), ObjectType(), true},
		{"array-record-to-array-object", ArrayType(record(RecordField{Name: "x", Type: IntType()})), ArrayType(ObjectType()), true},
		{"object-not-to-record", ObjectType(), record(RecordField{Name: "x", Type: IntType()}), false},
		{"object-to-any", ObjectType(), AnyType(), true},
		{"any-not-to-object", AnyType(), ObjectType(), false},
		// enums are nominal and must be well formed
		{"enum-self", EnumType("TravelMode", []string{"air", "rail"}), EnumType("TravelMode", []string{"air", "rail"}), true},
		{"enum-nominal", EnumType("TravelMode", []string{"air", "rail"}), EnumType("ShippingMode", []string{"air", "rail"}), false},
		{"enum-reordered-members", EnumType("TravelMode", []string{"air", "rail"}), EnumType("TravelMode", []string{"rail", "air"}), false},
		{"string-not-to-enum", StringType(), EnumType("TravelMode", []string{"air", "rail"}), false},
		// malformed enum descriptors are never assignable (fail closed)
		{"malformed-enum-bare", &Descriptor{Kind: Enum}, &Descriptor{Kind: Enum}, false},
		{"malformed-enum-empty-name", EnumType("", []string{"air"}), EnumType("", []string{"air"}), false},
		{"malformed-enum-empty-members", EnumType("TravelMode", nil), EnumType("TravelMode", nil), false},
		{"malformed-enum-duplicate-members", EnumType("TravelMode", []string{"air", "air"}), EnumType("TravelMode", []string{"air", "air"}), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAssignable(tc.src, tc.dst); got != tc.want {
				t.Errorf("IsAssignable(%+v, %+v) = %v, want %v", tc.src, tc.dst, got, tc.want)
			}
		})
	}
}

// ── TestIsComparable ──

func TestIsComparable(t *testing.T) {
	cases := []struct {
		name string
		a, b *Descriptor
		want bool
	}{
		{"same-type", IntType(), IntType(), true},
		{"numeric", IntType(), FloatType(), true},
		{"string-not-with-int", StringType(), IntType(), false},
		{"any-comparable", AnyType(), StringType(), true},
		{"null-not-with-int", NullType(), IntType(), false},
		{"null-with-optional", NullType(), OptionalType(IntType()), true},
		{"optional-with-optional", OptionalType(IntType()), OptionalType(FloatType()), true},
		{"optional-mismatch", OptionalType(IntType()), OptionalType(StringType()), false},
		{"optional-not-with-array", OptionalType(IntType()), ArrayType(IntType()), false},
		{"array-not-with-optional", ArrayType(IntType()), OptionalType(IntType()), false},
		{"array-recursion", ArrayType(IntType()), ArrayType(FloatType()), true},
		{"array-mismatch", ArrayType(IntType()), ArrayType(StringType()), false},
		{"map-recursion", MapType(StringType(), IntType()), MapType(StringType(), FloatType()), true},
		{"map-key-mismatch", MapType(StringType(), IntType()), MapType(IntType(), IntType()), false},
		{"enum-self", EnumType("TravelMode", []string{"air", "rail"}), EnumType("TravelMode", []string{"air", "rail"}), true},
		{"enum-nominal", EnumType("TravelMode", []string{"air", "rail"}), EnumType("ShippingMode", []string{"air", "rail"}), false},
		{"malformed-enum", &Descriptor{Kind: Enum}, &Descriptor{Kind: Enum}, false},
		{"null-with-null", NullType(), NullType(), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsComparable(tc.a, tc.b); got != tc.want {
				t.Errorf("IsComparable(%+v, %+v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// ── TestIsOrderable ──

func TestIsOrderable(t *testing.T) {
	cases := []struct {
		name string
		a, b *Descriptor
		want bool
	}{
		{"numeric", IntType(), FloatType(), true},
		{"string-pair", StringType(), StringType(), true},
		{"string-not-with-int", StringType(), IntType(), false},
		{"any-orderable", AnyType(), StringType(), true},
		{"enum-not-orderable", EnumType("TravelMode", []string{"air", "rail"}), EnumType("TravelMode", []string{"air", "rail"}), false},
		{"bool-not-orderable", BoolType(), BoolType(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsOrderable(tc.a, tc.b); got != tc.want {
				t.Errorf("IsOrderable(%+v, %+v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// ── TestJoinAggregateTypes ──

func TestJoinAggregateTypes(t *testing.T) {
	cases := []struct {
		name string
		a, b *Descriptor
		want *Descriptor
	}{
		{"same-primitive", StringType(), StringType(), StringType()},
		{"int-float", IntType(), FloatType(), FloatType()},
		{"any-wins", AnyType(), IntType(), AnyType()},
		{"null-left", NullType(), IntType(), OptionalType(IntType())},
		{"null-right", IntType(), NullType(), OptionalType(IntType())},
		{"optional-with-value", OptionalType(IntType()), IntType(), OptionalType(IntType())},
		{"value-with-optional", IntType(), OptionalType(IntType()), OptionalType(IntType())},
		{"optional-equal", OptionalType(IntType()), OptionalType(IntType()), OptionalType(IntType())},
		{"optional-mismatch", OptionalType(IntType()), OptionalType(StringType()), nil},
		{"array-join", ArrayType(IntType()), ArrayType(FloatType()), ArrayType(FloatType())},
		{"array-mismatch", ArrayType(IntType()), ArrayType(StringType()), nil},
		{"map-join", MapType(StringType(), IntType()), MapType(StringType(), FloatType()), MapType(StringType(), FloatType())},
		{"map-key-mismatch", MapType(StringType(), IntType()), MapType(IntType(), IntType()), nil},
		{"string-int", StringType(), IntType(), nil},
		{"enum-identity", EnumType("TravelMode", []string{"air", "rail"}), EnumType("TravelMode", []string{"air", "rail"}), EnumType("TravelMode", []string{"air", "rail"})},
		{"enum-reordered", EnumType("TravelMode", []string{"air", "rail"}), EnumType("TravelMode", []string{"rail", "air"}), nil},
		{"enum-nominal", EnumType("TravelMode", []string{"air", "rail"}), EnumType("ShippingMode", []string{"air", "rail"}), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := JoinAggregateTypes(tc.a, tc.b)
			if tc.want == nil {
				mustNil(t, got, tc.name)
				return
			}
			mustEqual(t, got, tc.want, tc.name)
		})
	}
}

func TestJoinAggregateTypesMalformedEnumFailsClosed(t *testing.T) {
	// Python's malformed core is EnumType("TravelMode", ["air"]) where members
	// is a list, not a tuple; Go has no tuple/list distinction, so duplicate
	// members are the malformed core here (same intent: fail closed).
	malformed := EnumType("TravelMode", []string{"air", "air"})
	if got := JoinAggregateTypes(malformed, malformed); got != nil {
		t.Errorf("malformed enum self-join = %+v, want nil", got)
	}
	for _, aggregate := range []*Descriptor{
		OptionalType(malformed),
		ArrayType(malformed),
		MapType(StringType(), malformed),
	} {
		if got := JoinAggregateTypes(aggregate, aggregate); got != nil {
			t.Errorf("nested malformed enum aggregate self-join (%v) = %+v, want nil", aggregate.Kind, got)
		}
	}
}

// ── TestCommonType ──

func TestCommonType(t *testing.T) {
	cases := []struct {
		name string
		a, b *Descriptor
		op   string
		want *Descriptor
	}{
		{"same-type", IntType(), IntType(), "+", IntType()},
		{"int-float", IntType(), FloatType(), "+", FloatType()},
		{"string-concat-plus", StringType(), IntType(), "+", StringType()},
		{"string-concat-no-minus", StringType(), IntType(), "-", nil},
		{"string-concat-no-star", StringType(), IntType(), "*", nil},
		{"numeric-mult", IntType(), FloatType(), "*", FloatType()},
		{"any-wins", AnyType(), IntType(), "+", AnyType()},
		{"null-promotes", NullType(), IntType(), "+", OptionalType(IntType())},
		{"incompatible", BoolType(), FileType(), "+", nil},
		{"enum-incompatible", EnumType("TravelMode", []string{"air"}), IntType(), "+", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CommonType(tc.a, tc.b, tc.op)
			if tc.want == nil {
				mustNil(t, got, tc.name)
				return
			}
			mustEqual(t, got, tc.want, tc.name)
		})
	}
}

// ── TypeRefToDescriptor ──

func TestTypeRefToDescriptor(t *testing.T) {
	cases := []struct {
		name string
		ref  ast.TypeRef
		want *Descriptor
	}{
		{"prim-string", ast.NewPrimTypeRef("string"), StringType()},
		{"prim-int", ast.NewPrimTypeRef("int"), IntType()},
		{"prim-float", ast.NewPrimTypeRef("float"), FloatType()},
		{"prim-bool", ast.NewPrimTypeRef("bool"), BoolType()},
		{"prim-file", ast.NewPrimTypeRef("file"), FileType()},
		{"prim-void", ast.NewPrimTypeRef("void"), VoidType()},
		{"prim-any", ast.NewPrimTypeRef("any"), AnyType()},
		{"prim-unknown-recover-any", ast.NewPrimTypeRef("blob"), AnyType()},
		{"array", ast.NewArrayTypeRef(ast.NewPrimTypeRef("int")), ArrayType(IntType())},
		{"map", ast.NewMapTypeRef(ast.NewPrimTypeRef("string"), ast.NewPrimTypeRef("int")), MapType(StringType(), IntType())},
		{"optional", ast.NewOptionalTypeRef(ast.NewPrimTypeRef("string")), OptionalType(StringType())},
		{"named-unknown-recover-any", ast.NewNamedTypeRef("Missing"), AnyType()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mustEqual(t, TypeRefToDescriptor(tc.ref, nil), tc.want, tc.name)
		})
	}
}

func TestTypeRefToDescriptorNamedLookup(t *testing.T) {
	resolved := record(RecordField{Name: "x", Type: IntType()})
	got := TypeRefToDescriptor(ast.NewNamedTypeRef("Point"), func(name string) *Descriptor {
		if name == "Point" {
			return resolved
		}
		return nil
	})
	mustEqual(t, got, resolved, "named type resolves via lookup")
}

func TestTypeRefToDescriptorFileConstraints(t *testing.T) {
	ref := ast.NewFileTypeRef(ast.FileTypeConstraints{
		Types:         []string{"image"},
		Extensions:    []string{"png"},
		UploadMethods: []string{"remote_url"},
	})
	want := FileTypeWithConstraints(FileConstraints{
		Types:         []string{"image"},
		Extensions:    []string{"png"},
		UploadMethods: []string{"remote_url"},
	})
	mustEqual(t, TypeRefToDescriptor(ref, nil), want, "file constraints carried through")
}
