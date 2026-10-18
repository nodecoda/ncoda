package codec

// Ports of the Python reference codec suite
// (lang/tests/lang/unit/test_dify_type_codec.py, 313 lines), restricted to
// codec-contract cases. Translator-layer routing tests (gate boundary /
// regression routing) are deferred to the translator task and noted in the
// task-8 report. Group names mirror the Python tests so parity is auditable.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/types"
)

func mustEqual(t *testing.T, got, want *types.Descriptor, label string) {
	t.Helper()
	if !got.Equal(want) {
		t.Errorf("%s: got %+v, want %+v", label, got, want)
	}
}

func record(fields ...types.RecordField) *types.Descriptor {
	return types.RecordType("R", fields)
}

// requireSupported is the Go form of the Python tests' require_supported
// call: it fails the test when the shape is unsupported.
func requireSupported(t *testing.T, shape *Shape, context string) *Shape {
	t.Helper()
	s, err := Default().RequireSupported(shape, context)
	if err != nil {
		t.Fatalf("%s: unsupported shape: %v", context, err)
	}
	return s
}

// assertUnsupported is the Go form of the Python tests' unsupported-shape
// assertions (loss is UNSUPPORTED, value_type is None, require_supported
// raises).
func assertUnsupported(t *testing.T, shape *Shape, context string) {
	t.Helper()
	if shape.Loss != LossUnsupported {
		t.Errorf("%s: loss = %q, want %q", context, shape.Loss, LossUnsupported)
	}
	if shape.ValueType != "" {
		t.Errorf("%s: value_type = %q, want none", context, shape.ValueType)
	}
	if _, err := Default().RequireSupported(shape, context); err == nil {
		t.Errorf("%s: require_supported did not fail", context)
	}
}

// test_locked_profile_owns_the_locked_codec: the profile registry is not
// ported (YAGNI), so ownership is expressed by Default() locking TargetName.
func TestLockedProfileOwnsTheLockedCodec(t *testing.T) {
	if TargetName != "dify-1.16-graphon-0.6" {
		t.Errorf("TargetName = %q, want %q", TargetName, "dify-1.16-graphon-0.6")
	}
	if got := Default().ProfileName; got != TargetName {
		t.Errorf("Default().ProfileName = %q, want %q", got, TargetName)
	}
}

func TestScalarEnumMapsToSelectAndStringPorts(t *testing.T) {
	c := Default()
	kind := types.EnumType("TravelMode", []string{"air", "rail"})
	if got := requireSupported(t, c.ToStartInput(kind), "Start input").ValueType; got != PTSelect {
		t.Errorf("to_start_input(enum) = %q, want %q", got, PTSelect)
	}
	if got := requireSupported(t, c.ToCodePort(kind), "Code port").ValueType; got != PTString {
		t.Errorf("to_code_port(enum) = %q, want %q", got, PTString)
	}
	if got := requireSupported(t, c.ToOperationPort(kind, "equals"), "test port").ValueType; got != PTString {
		t.Errorf("to_operation_port(enum) = %q, want %q", got, PTString)
	}
}

func TestEnumCompositesRemainUnsupported(t *testing.T) {
	c := Default()
	composite := types.ArrayType(types.EnumType("Mode", []string{"a"}))
	assertUnsupported(t, c.ToStartInput(composite), "start")
	assertUnsupported(t, c.ToCodePort(composite), "code")
}

func TestRecordChildrenAreRecursivelyImmutable(t *testing.T) {
	c := Default()
	user := record(
		types.RecordField{Name: "name", Type: types.StringType()},
		types.RecordField{Name: "meta", Type: record(types.RecordField{Name: "score", Type: types.FloatType()})},
	)
	shape := c.ToCodePort(user)

	if shape.ValueType != PTObject {
		t.Errorf("value_type = %q, want %q", shape.ValueType, PTObject)
	}
	if shape.SourceKind != SourceRecord {
		t.Errorf("source_kind = %q, want %q", shape.SourceKind, SourceRecord)
	}
	if shape.Loss != LossExact {
		t.Errorf("loss = %q, want %q", shape.Loss, LossExact)
	}
	if shape.Children == nil {
		t.Fatal("children must not be nil for a record")
	}
	if got := shape.Children["name"].ValueType; got != PTString {
		t.Errorf("children[name].value_type = %q, want %q", got, PTString)
	}
	if got := shape.Children["meta"].Children["score"].ValueType; got != PTNumber {
		t.Errorf("children[meta].children[score].value_type = %q, want %q", got, PTNumber)
	}

	// Go cannot raise on mutation (Python TypeError / FrozenInstanceError);
	// the equivalent contract is that mutating a returned shape never leaks
	// into later mappings, because every call builds fresh shapes.
	shape.Children["extra"] = shape
	shape.Children["meta"].Children["extra"] = shape
	again := c.ToCodePort(user)
	if _, ok := again.Children["extra"]; ok {
		t.Error("mutating a returned shape leaked into a later mapping")
	}
	if _, ok := again.Children["meta"].Children["extra"]; ok {
		t.Error("mutating a nested returned shape leaked into a later mapping")
	}
}

func TestScalarFileOptionalAndFileArrayForwardMatrix(t *testing.T) {
	c := Default()
	cases := []struct {
		label      string
		descriptor *types.Descriptor
		start      PhysicalType
		value      PhysicalType
	}{
		{"string", types.StringType(), PTTextInput, PTString},
		{"int", types.IntType(), PTNumber, PTNumber},
		{"float", types.FloatType(), PTNumber, PTNumber},
		{"bool", types.BoolType(), PTCheckbox, PTBoolean},
		{"any", types.AnyType(), PTTextInput, PTString},
		{"file", types.FileType(), PTFile, PTFile},
		{"array[file]", types.ArrayType(types.FileType()), PTFileList, "array[file]"},
		{"optional[string]", types.OptionalType(types.StringType()), PTTextInput, PTString},
	}
	for _, tc := range cases {
		if got := requireSupported(t, c.ToStartInput(tc.descriptor), "Start input "+tc.label).ValueType; got != tc.start {
			t.Errorf("%s: to_start_input = %q, want %q", tc.label, got, tc.start)
		}
		if got := requireSupported(t, c.ToCodePort(tc.descriptor), "Code port "+tc.label).ValueType; got != tc.value {
			t.Errorf("%s: to_code_port = %q, want %q", tc.label, got, tc.value)
		}
		if got := requireSupported(t, c.ToOperationPort(tc.descriptor, "test"), "test port "+tc.label).ValueType; got != tc.value {
			t.Errorf("%s: to_operation_port = %q, want %q", tc.label, got, tc.value)
		}
	}
}

func TestMapRecordAndArrayChildrenForwardMatrix(t *testing.T) {
	c := Default()
	mapping := types.MapType(types.StringType(), types.IntType())
	user := record(types.RecordField{Name: "name", Type: types.StringType()})

	mapShape := c.ToCodePort(mapping)
	if mapShape.ValueType != PTObject {
		t.Errorf("map code port = %q, want %q", mapShape.ValueType, PTObject)
	}
	if mapShape.Children != nil {
		t.Errorf("map code port children = %v, want nil", mapShape.Children)
	}
	if got := c.ToCodePort(types.ArrayType(mapping)).Children; got != nil {
		t.Errorf("array[map] code port children = %v, want nil", got)
	}

	recordShape := c.ToCodePort(user)
	if recordShape.Children == nil {
		t.Fatal("record code port children must not be nil")
	}
	if got := recordShape.Children["name"].ValueType; got != PTString {
		t.Errorf("record children[name] = %q, want %q", got, PTString)
	}
	arrayShape := c.ToCodePort(types.ArrayType(user))
	if arrayShape.ValueType != "array[object]" {
		t.Errorf("array[record] = %q, want %q", arrayShape.ValueType, "array[object]")
	}
	if !reflect.DeepEqual(arrayShape.Children, recordShape.Children) {
		t.Errorf("array children %v != record children %v", arrayShape.Children, recordShape.Children)
	}
}

func TestUnsupportedValueShapesNeverSerializeAsString(t *testing.T) {
	c := Default()
	cases := []struct {
		label      string
		descriptor *types.Descriptor
	}{
		{"map[int,string]", types.MapType(types.IntType(), types.StringType())},
		{"void", types.VoidType()},
		{"null", types.NullType()},
		{"function", types.FunctionType(nil, types.StringType())},
		{"bare array", &types.Descriptor{Kind: types.Array}},
		{"bare map", &types.Descriptor{Kind: types.Map}},
		{"bare optional", &types.Descriptor{Kind: types.Optional}},
	}
	for _, tc := range cases {
		shape := c.ToCodePort(tc.descriptor)
		if shape.ValueType != "" {
			t.Errorf("%s: value_type = %q, want none", tc.label, shape.ValueType)
		}
		if shape.Loss != LossUnsupported {
			t.Errorf("%s: loss = %q, want %q", tc.label, shape.Loss, LossUnsupported)
		}
		if _, err := c.RequireSupported(shape, "Code port"); err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Errorf("%s: require_supported err = %v, want error mentioning unsupported", tc.label, err)
		}
	}
}

func TestStartRejectsNonFileAggregates(t *testing.T) {
	c := Default()
	cases := []struct {
		label      string
		descriptor *types.Descriptor
	}{
		{"map[string,any]", types.MapType(types.StringType(), types.AnyType())},
		{"record", record(types.RecordField{Name: "name", Type: types.StringType()})},
		{"array[string]", types.ArrayType(types.StringType())},
	}
	for _, tc := range cases {
		assertUnsupported(t, c.ToStartInput(tc.descriptor), "start "+tc.label)
	}
}

func TestMalformedParameterizedDescriptorsFailClosed(t *testing.T) {
	c := Default()
	// Go-expressible subset of the Python malformed cases. The two remaining
	// cases (file constraints with a non-string tuple value; a list passed as
	// descriptor params) have no Go representation.
	cases := []struct {
		label      string
		descriptor *types.Descriptor
	}{
		{"string with array params", &types.Descriptor{Kind: types.String, Elem: types.IntType()}},
		{"file with array params", &types.Descriptor{Kind: types.File, Elem: types.StringType()}},
		{"array of file with optional params", types.ArrayType(&types.Descriptor{Kind: types.File, Elem: types.OptionalType(types.StringType())})},
		{"bare map", &types.Descriptor{Kind: types.Map}},
		{"string with file constraints", &types.Descriptor{Kind: types.String, File: types.FileConstraints{Types: []string{"image"}}}},
		{"record with duplicate field names", record(
			types.RecordField{Name: "a", Type: types.StringType()},
			types.RecordField{Name: "a", Type: types.FloatType()},
		)},
	}
	for _, tc := range cases {
		assertUnsupported(t, c.ToStartInput(tc.descriptor), "start "+tc.label)
		assertUnsupported(t, c.ToCodePort(tc.descriptor), "code "+tc.label)
		assertUnsupported(t, c.ToParameterExtractorField(tc.descriptor), "extractor "+tc.label)
	}
}

func TestParameterExtractorExposesOnlyCertifiedShapes(t *testing.T) {
	c := Default()
	cases := []struct {
		descriptor *types.Descriptor
		want       PhysicalType
	}{
		{types.StringType(), PTString},
		{types.FloatType(), PTNumber},
		{types.BoolType(), PTBoolean},
		{types.ArrayType(types.StringType()), "array[string]"},
		{types.ArrayType(types.FloatType()), "array[number]"},
		{types.ArrayType(types.BoolType()), "array[boolean]"},
		{types.ArrayType(types.MapType(types.StringType(), types.AnyType())), "array[object]"},
	}
	for i, tc := range cases {
		shape := c.ToParameterExtractorField(tc.descriptor)
		if shape.ValueType != tc.want {
			t.Errorf("case %d: value_type = %q, want %q", i, shape.ValueType, tc.want)
		}
		if shape.Loss != LossExact {
			t.Errorf("case %d: loss = %q, want %q", i, shape.Loss, LossExact)
		}
	}
}

func TestParameterExtractorDoesNotInheritWiderCodeSupport(t *testing.T) {
	c := Default()
	cases := []struct {
		label      string
		descriptor *types.Descriptor
	}{
		{"int", types.IntType()},
		{"any", types.AnyType()},
		{"file", types.FileType()},
		{"map[string,any]", types.MapType(types.StringType(), types.AnyType())},
		{"record", record(types.RecordField{Name: "name", Type: types.StringType()})},
		{"array[record]", types.ArrayType(record(types.RecordField{Name: "name", Type: types.StringType()}))},
	}
	for _, tc := range cases {
		if got := c.ToParameterExtractorField(tc.descriptor).Loss; got != LossUnsupported {
			t.Errorf("%s: loss = %q, want %q", tc.label, got, LossUnsupported)
		}
	}
}

func TestBareObjectAndArrayObjectDecodeAsOpaqueMaps(t *testing.T) {
	c := Default()
	opaque := types.MapType(types.StringType(), types.AnyType())

	decoded, err := c.FromPort("object", nil, "")
	if err != nil {
		t.Fatalf("from_dify_port(object, None): %v", err)
	}
	mustEqual(t, decoded.Descriptor, opaque, "object,None")
	if decoded.Loss != LossExternalOpaque {
		t.Errorf("object,None loss = %q, want %q", decoded.Loss, LossExternalOpaque)
	}

	decodedEmpty, err := c.FromPort("object", map[string]any{}, "")
	if err != nil {
		t.Fatalf("from_dify_port(object, {}): %v", err)
	}
	mustEqual(t, decodedEmpty.Descriptor, opaque, "object,{}")
	if decodedEmpty.Loss != LossExternalOpaque {
		t.Errorf("object,{} loss = %q, want %q", decodedEmpty.Loss, LossExternalOpaque)
	}

	array, err := c.FromPort("array[object]", map[string]any{}, "")
	if err != nil {
		t.Fatalf("from_dify_port(array[object], {}): %v", err)
	}
	mustEqual(t, array.Descriptor, types.ArrayType(opaque), "array[object],{}")
	if array.Loss != LossExternalOpaque {
		t.Errorf("array[object],{} loss = %q, want %q", array.Loss, LossExternalOpaque)
	}
}

func TestEmptyChildrenRejectedForNonObjectPorts(t *testing.T) {
	c := Default()
	for _, valueType := range []string{"string", "array[string]"} {
		if _, err := c.FromPort(valueType, map[string]any{}, "Port.result"); err == nil ||
			!strings.Contains(err.Error(), "cannot carry children") {
			t.Errorf("from_dify_port(%q, {{}}): err = %v, want 'cannot carry children'", valueType, err)
		}
	}
}

func TestObjectChildrenDecodeNestedRecordsFromOwnerIdentity(t *testing.T) {
	c := Default()
	decoded, err := c.FromPort(
		"object",
		map[string]any{
			"name": map[string]any{"type": "string"},
			"meta": map[string]any{
				"type": "object",
				"children": map[string]any{
					"score": map[string]any{"type": "number"},
				},
			},
		},
		"OperationResult[search].result",
	)
	if err != nil {
		t.Fatalf("from_dify_port: %v", err)
	}
	if decoded.Loss != LossExact {
		t.Errorf("loss = %q, want %q", decoded.Loss, LossExact)
	}
	// Field order is deterministic (sorted names); record names are structural
	// in this layer (Task 7 deviation).
	want := types.RecordType("OperationResult[search].result", []types.RecordField{
		{Name: "meta", Type: types.RecordType("OperationResult[search].result.meta", []types.RecordField{
			{Name: "score", Type: types.FloatType()},
		})},
		{Name: "name", Type: types.StringType()},
	})
	mustEqual(t, decoded.Descriptor, want, "decoded record")
}

func TestReverseMappingRejectsUnknownAmbiguousOrMalformedShapes(t *testing.T) {
	c := Default()
	cases := []struct {
		label     string
		valueType string
		children  map[string]any
		identity  string
	}{
		{"unknown type", "tuple", nil, ""},
		{"unbalanced array", "array[object", nil, ""},
		{"children on scalar", "string", map[string]any{"field": map[string]any{"type": "string"}}, "Port.result"},
		{"object children without identity", "object", map[string]any{"field": map[string]any{"type": "string"}}, ""},
		{"unsupported child type", "object", map[string]any{"field": map[string]any{"type": "tuple"}}, "Port.result"},
	}
	for _, tc := range cases {
		if _, err := c.FromPort(tc.valueType, tc.children, tc.identity); err == nil {
			t.Errorf("%s: from_dify_port(%q) did not fail", tc.label, tc.valueType)
		}
	}
}

// Brief contract: Map forward-maps to the code-port physical type and fails
// closed on unsupported shapes.
func TestMapFailsClosedOnUnsupported(t *testing.T) {
	c := Default()
	got, err := c.Map(types.StringType())
	if err != nil || got != PTString {
		t.Errorf("Map(string) = (%q, %v), want (%q, nil)", got, err, PTString)
	}
	got, err = c.Map(types.ArrayType(types.FileType()))
	if err != nil || got != "array[file]" {
		t.Errorf("Map(array[file]) = (%q, %v), want (array[file], nil)", got, err)
	}
	if _, err := c.Map(types.VoidType()); err == nil {
		t.Error("Map(void) did not fail closed")
	}
}

// NewShape mirrors the DifyPortShape __post_init__ invariant: unsupported
// shapes are the only shapes without a value type.
func TestNewShapeEnforcesUnsupportedInvariant(t *testing.T) {
	if _, err := NewShape("", nil, SourceScalar, LossExact); err == nil {
		t.Error("NewShape(exact, no type) did not fail")
	}
	if _, err := NewShape(PTString, nil, SourceScalar, LossUnsupported); err == nil {
		t.Error("NewShape(unsupported, with type) did not fail")
	}
}

// NewShape must not retain the caller's children map: Python's
// MappingProxyType(dict(children)) copy means later mutation of the input
// cannot leak into the shape (shallow copy — nested shapes stay shared).
func TestNewShapeSnapshotsChildren(t *testing.T) {
	child := &Shape{ValueType: PTString, SourceKind: SourceScalar, Loss: LossExact}
	input := map[string]*Shape{"a": child}
	shape, err := NewShape(PTObject, input, SourceRecord, LossExact)
	if err != nil {
		t.Fatalf("NewShape: %v", err)
	}
	input["b"] = &Shape{ValueType: PTNumber, SourceKind: SourceScalar, Loss: LossExact}
	delete(input, "a")
	if _, ok := shape.Children["b"]; ok {
		t.Error("mutating the input map after NewShape leaked a new child into the shape")
	}
	if _, ok := shape.Children["a"]; !ok {
		t.Error("deleting from the input map after NewShape removed a child from the shape")
	}
}
