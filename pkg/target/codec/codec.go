// Package difycodec ports the target-owned DC-to-Dify physical type mappings
// from src/nclang/lang/dify_type_codec.py (DifyTypeCodec and friends). It
// maps types.Descriptor values to Dify port contracts and back, failing
// closed on unsupported, malformed, or unknown shapes. Only the single locked
// target dify-1.16-graphon-0.6 is supported (see target.go); the
// translator-layer routing (custom codec classes, target profiles) is a later
// task and deliberately not ported here.
package codec

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nodecoda/ncoda/pkg/types"
)

// PhysicalType is a Dify port value type ("string", "number", ...), mirroring
// the Python value_type strings.
type PhysicalType string

const (
	PTTextInput PhysicalType = "text-input"
	PTNumber    PhysicalType = "number"
	PTCheckbox  PhysicalType = "checkbox"
	PTSelect    PhysicalType = "select"
	PTString    PhysicalType = "string"
	PTBoolean   PhysicalType = "boolean"
	PTFile      PhysicalType = "file"
	PTFileList  PhysicalType = "file-list"
	PTObject    PhysicalType = "object"
	PTAny       PhysicalType = "any"
)

// SourceKind mirrors dify_type_codec.py DifySourceKind.
type SourceKind string

const (
	SourceScalar SourceKind = "scalar"
	SourceArray  SourceKind = "array"
	SourceMap    SourceKind = "map"
	SourceRecord SourceKind = "record"
	SourceFile   SourceKind = "file"
)

// Loss mirrors dify_type_codec.py DifyMappingLoss.
type Loss string

const (
	LossExact          Loss = "exact"
	LossExternalOpaque Loss = "external_opaque"
	LossUnsupported    Loss = "unsupported"
)

// Shape mirrors dify_type_codec.py DifyPortShape: a Dify port contract with
// its physical type, optional children (object ports from records), the
// source kind, and the mapping loss. Shapes returned by a Codec are built
// fresh per call and must be treated as immutable. Go cannot enforce the
// Python frozen dataclass / MappingProxy behavior at runtime, so the contract
// is guaranteed by construction: no codec method mutates or caches a returned
// shape, and later calls never observe earlier mutations (see
// TestRecordChildrenAreRecursivelyImmutable).
type Shape struct {
	ValueType PhysicalType // "" iff Loss == LossUnsupported
	Children  map[string]*Shape
	// ChildOrder preserves the record field order (Python dict insertion
	// order for shape.children). Empty falls back to sorted keys in
	// consumers for determinism.
	ChildOrder []string
	SourceKind SourceKind
	Loss       Loss
}

// NewShape builds a validated Shape, enforcing the Python DifyPortShape
// __post_init__ invariant: unsupported shapes are the only shapes without a
// value type. The children mapping is shallow-copied, mirroring Python's
// MappingProxyType(dict(children)): later mutation of the caller's map must
// not leak into the shape (nested shapes are shared, matching the shallow
// copy).
func NewShape(valueType PhysicalType, children map[string]*Shape, kind SourceKind, loss Loss) (*Shape, error) {
	if (loss == LossUnsupported) != (valueType == "") {
		return nil, fmt.Errorf("unsupported Dify shapes must be the only shapes without a type")
	}
	return &Shape{ValueType: valueType, Children: cloneChildren(children), SourceKind: kind, Loss: loss}, nil
}

// cloneChildren shallow-copies a children mapping, mirroring Python's
// MappingProxyType(dict(children)) in DifyPortShape.__post_init__. A nil
// mapping stays nil (Python None children).
func cloneChildren(children map[string]*Shape) map[string]*Shape {
	if children == nil {
		return nil
	}
	out := make(map[string]*Shape, len(children))
	for name, child := range children {
		out[name] = child
	}
	return out
}

// Decoded mirrors dify_type_codec.py DecodedDifyType.
type Decoded struct {
	Descriptor *types.Descriptor
	Loss       Loss
}

// MappingError mirrors dify_type_codec.py DifyTypeMappingError (a ValueError
// subclass in Python). It is returned when a DC type cannot be represented by
// a Dify port contract, or a Dify port shape cannot be decoded.
type MappingError struct{ msg string }

func (e *MappingError) Error() string { return e.msg }

// Codec mirrors dify_type_codec.py DifyTypeCodec, locked to the single
// supported target profile name (see target.go).
type Codec struct {
	ProfileName string
}

// RequireSupported mirrors require_supported (line 71): fail closed when the
// shape is unsupported.
func (c *Codec) RequireSupported(shape *Shape, context string) (*Shape, error) {
	if shape.Loss == LossUnsupported {
		return nil, &MappingError{fmt.Sprintf("%s has an unsupported Dify type mapping", context)}
	}
	return shape, nil
}

// Map mirrors the brief contract: forward-map d to its Dify code-port
// physical type, failing closed on unsupported shapes (equivalent to
// require_supported(to_code_port(d), context="Code port")).
func (c *Codec) Map(d *types.Descriptor) (PhysicalType, error) {
	shape, err := c.RequireSupported(c.ToCodePort(d), "Code port")
	if err != nil {
		return "", err
	}
	return shape.ValueType, nil
}

// ToCodePort mirrors to_code_port (line 93): the Dify value-port contract.
func (c *Codec) ToCodePort(d *types.Descriptor) *Shape { return c.toValuePort(d) }

// ToOperationPort mirrors to_operation_port (line 96); operation is ignored.
func (c *Codec) ToOperationPort(d *types.Descriptor, operation string) *Shape {
	_ = operation
	return c.toValuePort(d)
}

// ToStartInput mirrors to_start_input (line 78): the Dify start-node input
// contract, which only accepts scalars, files, and file arrays.
func (c *Codec) ToStartInput(d *types.Descriptor) *Shape {
	if !isWellFormed(d) {
		return unsupportedShape(sourceKind(d))
	}
	value := types.UnwrapOptional(d)
	switch value.Kind {
	case types.String:
		return exactShape(PTTextInput, SourceScalar, nil)
	case types.Int, types.Float:
		return exactShape(PTNumber, SourceScalar, nil)
	case types.Bool:
		return exactShape(PTCheckbox, SourceScalar, nil)
	case types.Enum:
		if !enumWellFormed(value) {
			return unsupportedShape(SourceScalar)
		}
		return exactShape(PTSelect, SourceScalar, nil)
	case types.Any:
		return exactShape(PTTextInput, SourceScalar, nil)
	case types.File:
		return exactShape(PTFile, SourceFile, nil)
	case types.Array:
		if value.Elem != nil && value.Elem.Kind == types.File {
			return exactShape(PTFileList, SourceArray, nil)
		}
		return unsupportedShape(SourceArray)
	}
	return unsupportedShape(sourceKind(value))
}

// ToParameterExtractorField mirrors to_parameter_extractor_field (line 100):
// only the certified scalar/array shapes are exact; everything else that the
// code port supports (int, any, file, maps, records, record arrays) is
// deliberately unsupported here.
func (c *Codec) ToParameterExtractorField(d *types.Descriptor) *Shape {
	if !isWellFormed(d) {
		return unsupportedShape(sourceKind(d))
	}
	if d.Kind == types.Enum && enumWellFormed(d) {
		return exactShape(PTString, SourceScalar, nil)
	}
	switch {
	case d.Equal(types.StringType()):
		return exactShape(PTString, sourceKind(d), nil)
	case d.Equal(types.FloatType()):
		return exactShape(PTNumber, sourceKind(d), nil)
	case d.Equal(types.BoolType()):
		return exactShape(PTBoolean, sourceKind(d), nil)
	case d.Equal(types.ArrayType(types.StringType())):
		return exactShape("array[string]", sourceKind(d), nil)
	case d.Equal(types.ArrayType(types.FloatType())):
		return exactShape("array[number]", sourceKind(d), nil)
	case d.Equal(types.ArrayType(types.BoolType())):
		return exactShape("array[boolean]", sourceKind(d), nil)
	case d.Equal(types.ArrayType(types.MapType(types.StringType(), types.AnyType()))):
		return exactShape("array[object]", sourceKind(d), nil)
	}
	return unsupportedShape(sourceKind(d))
}

// FromPort mirrors from_dify_port (line 124): decode a Dify port contract
// back into a Descriptor. identity is the stable record identity for object
// ports with children; "" plays the role of Python None and is rejected for
// non-empty children.
func (c *Codec) FromPort(valueType string, children map[string]any, identity string) (*Decoded, error) {
	if valueType == "" {
		return nil, &MappingError{"Dify port type must be a non-empty string"}
	}
	if strings.HasPrefix(valueType, "array[") {
		if !strings.HasSuffix(valueType, "]") || strings.Count(valueType, "[") != strings.Count(valueType, "]") {
			return nil, &MappingError{fmt.Sprintf("malformed Dify array type: %q", valueType)}
		}
		elementType := valueType[len("array[") : len(valueType)-1]
		elementIdentity := ""
		if identity != "" {
			elementIdentity = identity + "[]"
		}
		element, err := c.FromPort(elementType, children, elementIdentity)
		if err != nil {
			return nil, err
		}
		return &Decoded{Descriptor: types.ArrayType(element.Descriptor), Loss: element.Loss}, nil
	}
	if children != nil && valueType != "object" {
		return nil, &MappingError{fmt.Sprintf("Dify type %q cannot carry children", valueType)}
	}
	switch valueType {
	case "string":
		return &Decoded{Descriptor: types.StringType(), Loss: LossExact}, nil
	case "number":
		return &Decoded{Descriptor: types.FloatType(), Loss: LossExact}, nil
	case "boolean":
		return &Decoded{Descriptor: types.BoolType(), Loss: LossExact}, nil
	case "file":
		return &Decoded{Descriptor: types.FileType(), Loss: LossExact}, nil
	case "any":
		return &Decoded{Descriptor: types.AnyType(), Loss: LossExact}, nil
	}
	if valueType != "object" {
		return nil, &MappingError{fmt.Sprintf("unsupported Dify type: %q", valueType)}
	}
	if len(children) == 0 {
		return &Decoded{
			Descriptor: types.MapType(types.StringType(), types.AnyType()),
			Loss:       LossExternalOpaque,
		}, nil
	}
	if identity == "" {
		return nil, &MappingError{"non-empty object children require a stable identity"}
	}
	// Deterministic field order: Go maps are unordered whereas Python dicts
	// preserve insertion order. Sorted names give a stable record field order
	// (record equality in this layer is order-sensitive; see task-7 report).
	names := make([]string, 0, len(children))
	for name := range children {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make([]types.RecordField, 0, len(children))
	for _, name := range names {
		decoded, err := c.decodeChild(name, children[name], identity)
		if err != nil {
			return nil, err
		}
		fields = append(fields, types.RecordField{Name: name, Type: decoded.Descriptor})
	}
	return &Decoded{Descriptor: types.RecordType(identity, fields), Loss: LossExact}, nil
}

// toValuePort mirrors _to_value_port (line 170): the shared code/operation
// port mapping.
func (c *Codec) toValuePort(d *types.Descriptor) *Shape {
	if !isWellFormed(d) {
		return unsupportedShape(sourceKind(d))
	}
	value := types.UnwrapOptional(d)
	switch value.Kind {
	case types.String:
		return exactShape(PTString, SourceScalar, nil)
	case types.Int, types.Float:
		return exactShape(PTNumber, SourceScalar, nil)
	case types.Bool:
		return exactShape(PTBoolean, SourceScalar, nil)
	case types.Any:
		return exactShape(PTString, SourceScalar, nil)
	case types.File:
		return exactShape(PTFile, SourceFile, nil)
	case types.Enum:
		if !enumWellFormed(value) {
			return unsupportedShape(SourceScalar)
		}
		return exactShape(PTString, SourceScalar, nil)
	case types.Map:
		if value.Key == nil || value.Key.Kind != types.String {
			return unsupportedShape(SourceMap)
		}
		return exactShape(PTObject, SourceMap, nil)
	case types.Record:
		children := make(map[string]*Shape, len(value.Fields))
		order := make([]string, 0, len(value.Fields))
		for _, f := range value.Fields {
			children[f.Name] = c.toValuePort(f.Type)
			order = append(order, f.Name)
		}
		for _, child := range children {
			if child.Loss == LossUnsupported {
				return unsupportedShape(SourceRecord)
			}
		}
		shape := exactShape(PTObject, SourceRecord, children)
		shape.ChildOrder = order
		return shape
	case types.Array:
		if value.Elem == nil || value.Elem.Kind == types.Enum {
			return unsupportedShape(SourceArray)
		}
		element := c.toValuePort(value.Elem)
		if element.Loss == LossUnsupported || element.ValueType == "" {
			return unsupportedShape(SourceArray)
		}
		return exactShape(
			PhysicalType("array["+string(element.ValueType)+"]"),
			SourceArray,
			element.Children,
		)
	}
	return unsupportedShape(sourceKind(value))
}

// decodeChild mirrors _decode_child (line 209): validate one raw Dify object
// field payload and decode it under the field identity.
func (c *Codec) decodeChild(fieldName string, raw any, parentIdentity string) (*Decoded, error) {
	if fieldName == "" {
		return nil, &MappingError{"Dify object field name must be a non-empty string"}
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, &MappingError{fmt.Sprintf("Dify object field %q must be a mapping", fieldName)}
	}
	var unknown []string
	for key := range m {
		if key != "type" && key != "children" {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, &MappingError{
			fmt.Sprintf("Dify object field %q has unknown keys: %s", fieldName, strings.Join(unknown, ", ")),
		}
	}
	childType, ok := m["type"].(string)
	if !ok {
		return nil, &MappingError{fmt.Sprintf("Dify object field %q type must be a string", fieldName)}
	}
	if childChildren := m["children"]; childChildren != nil {
		if _, ok := childChildren.(map[string]any); !ok {
			return nil, &MappingError{fmt.Sprintf("Dify object field %q children must be a mapping or null", fieldName)}
		}
	}
	childChildren, _ := m["children"].(map[string]any)
	return c.FromPort(childType, childChildren, parentIdentity+"."+fieldName)
}

// hasParams reports whether the descriptor carries a parameterized payload
// ("params is not None"), duplicating pkg/types/rules.go hasParams (line
// 36) — the same helper, kept out of pkg/types so this commit's scope
// stays inside pkg/backend/dify/codec. This copy is authoritative for difycodec;
// if a second consumer ever needs it, export it from pkg/types and
// remove both copies. File constraints are excluded in both (Python params
// never carries them).
func hasParams(d *types.Descriptor) bool {
	return d.Elem != nil || d.Key != nil || d.Val != nil ||
		len(d.Fields) > 0 || d.EnumName != "" || len(d.EnumMembers) > 0 ||
		len(d.Params) > 0 || d.Result != nil
}

// hasFileConstraints reports whether any file constraint tuple is present
// (Python "file_constraints is not None").
func hasFileConstraints(d *types.Descriptor) bool {
	return d.File.Types != nil || d.File.Extensions != nil || d.File.UploadMethods != nil
}

// enumWellFormed mirrors types.py is_well_formed_enum_type (line 264): a
// valid enum has a non-empty name and a non-empty, duplicate-free member
// list. Go []string cannot express non-string members, so those malformed
// shapes have no Go representation (same deviation as pkg/types).
func enumWellFormed(d *types.Descriptor) bool {
	if d == nil || d.Kind != types.Enum {
		return false
	}
	if d.EnumName == "" || len(d.EnumMembers) == 0 {
		return false
	}
	seen := make(map[string]bool, len(d.EnumMembers))
	for _, m := range d.EnumMembers {
		if m == "" || seen[m] {
			return false
		}
		seen[m] = true
	}
	return true
}

// payloadOnly reports whether the descriptor payload is confined exactly to
// the named field groups. Python TypeDescriptor has a single params slot, so
// a Go descriptor carrying payload in a slot its kind cannot own has no
// Python representation and fails closed.
func payloadOnly(d *types.Descriptor, keepElem, keepKV, keepFields, keepEnum, keepFn bool) bool {
	if (d.Elem != nil) != keepElem {
		return false
	}
	if (d.Key != nil || d.Val != nil) != keepKV {
		return false
	}
	if (d.Fields != nil) != keepFields {
		return false
	}
	if (d.EnumName != "" || len(d.EnumMembers) > 0) != keepEnum {
		return false
	}
	if (len(d.Params) > 0 || d.Result != nil) != keepFn {
		return false
	}
	return true
}

// isWellFormed mirrors dify_type_codec.py _is_well_formed (line 257): reject
// forged descriptors before interpreting their base kind. Record names are
// structural in this layer (types.RecordType discards them), so the Python
// non-empty record-name check is vacuous here.
func isWellFormed(d *types.Descriptor) bool {
	if d == nil {
		return false
	}
	if d.Kind != types.File && hasFileConstraints(d) {
		return false
	}
	switch d.Kind {
	case types.String, types.Int, types.Float, types.Bool,
		types.Void, types.Any, types.Null:
		return !hasParams(d)
	case types.File:
		// params must be None. Every expressible FileConstraints shape is
		// all-string and therefore well formed (non-string constraint tuples
		// have no Go representation).
		return !hasParams(d)
	case types.Enum:
		if !payloadOnly(d, false, false, false, true, false) {
			return false
		}
		return enumWellFormed(d)
	case types.Optional, types.Array:
		if !payloadOnly(d, true, false, false, false, false) {
			return false
		}
		return d.Elem != nil && isWellFormed(d.Elem)
	case types.Map:
		if !payloadOnly(d, false, true, false, false, false) {
			return false
		}
		return d.Key != nil && d.Val != nil && isWellFormed(d.Key) && isWellFormed(d.Val)
	case types.Record:
		if !payloadOnly(d, false, false, true, false, false) {
			return false
		}
		seen := make(map[string]bool, len(d.Fields))
		for _, f := range d.Fields {
			if f.Name == "" || !isWellFormed(f.Type) {
				return false
			}
			if seen[f.Name] {
				// Python record fields are a dict, so duplicate names are
				// unexpressible there; a Go descriptor with duplicates would
				// silently collapse fields in toValuePort, so fail closed.
				return false
			}
			seen[f.Name] = true
		}
		return true
	case types.Function:
		if !payloadOnly(d, false, false, false, false, true) {
			return false
		}
		for i := range d.Params {
			if !isWellFormed(&d.Params[i]) {
				return false
			}
		}
		return d.Result != nil && isWellFormed(d.Result)
	}
	return false
}

// sourceKind mirrors dify_type_codec.py _source_kind (line 246).
func sourceKind(d *types.Descriptor) SourceKind {
	if d == nil {
		return SourceScalar
	}
	switch d.Kind {
	case types.Array:
		return SourceArray
	case types.Map:
		return SourceMap
	case types.Record:
		return SourceRecord
	case types.File:
		return SourceFile
	}
	return SourceScalar
}

// exactShape mirrors _exact (line 324); unsupportedShape mirrors _unsupported
// (line 332). Both construct directly because their invariants hold by
// construction. exactShape snapshots children exactly like NewShape (Python
// __post_init__ copies on every DifyPortShape construction).
func exactShape(valueType PhysicalType, kind SourceKind, children map[string]*Shape) *Shape {
	return &Shape{ValueType: valueType, Children: cloneChildren(children), SourceKind: kind, Loss: LossExact}
}

func unsupportedShape(kind SourceKind) *Shape {
	return &Shape{ValueType: "", Children: nil, SourceKind: kind, Loss: LossUnsupported}
}
