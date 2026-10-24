// Package target defines the platform-neutral physical-type face the language
// frontend consults when it must certify that a language value can be carried
// by a target's physical port.
//
// The open repo deliberately ships **no platform codec**: a platform adapter
// (for example the Dify codec in the commercial repo) implements Codec and is
// injected through passes.TargetProfile.Codec. DefaultCodec is a canonical
// language mapping — it names no platform and exists so that the language lane
// can still build IR without a target.
package target

import "github.com/nodecoda/ncoda/pkg/types"

// PhysicalType is a target port value type. It is a plain string so that a
// platform adapter can supply its own vocabulary without the kernel naming it.
type PhysicalType string

// SourceKind classifies the ncoda descriptor a shape was derived from.
type SourceKind string

const (
	SourceScalar SourceKind = "scalar"
	SourceArray  SourceKind = "array"
	SourceMap    SourceKind = "map"
	SourceRecord SourceKind = "record"
	SourceFile   SourceKind = "file"
)

// Loss reports how much of the source type survives the physical mapping.
type Loss string

const (
	LossExact          Loss = "exact"
	LossExternalOpaque Loss = "external_opaque"
	LossUnsupported    Loss = "unsupported"
)

// Shape is a target port contract: its physical type, optional children
// (object ports), the source kind, and the mapping loss. Shapes returned by a
// Codec are built fresh per call and must be treated as immutable.
type Shape struct {
	ValueType  PhysicalType
	Children   map[string]*Shape
	ChildOrder []string
	SourceKind SourceKind
	Loss       Loss
}

// Codec is the physical-type mapping face TargetProfile carries. The language
// frontend reads it through the profile; it never imports a backend package.
type Codec interface {
	ToParameterExtractorField(d *types.Descriptor) *Shape
}

// canonicalCodec is the language-level mapping: it answers "what portable
// scalar/array shape does this descriptor have?" without naming a platform.
// The vocabulary matches the IR extraction encoding (pkg/ir.extractionType).
type canonicalCodec struct{}

var _ Codec = canonicalCodec{}

// DefaultCodec returns the canonical language codec. A target adapter replaces
// it through TargetProfile.Codec when a platform needs its own vocabulary.
func DefaultCodec() Codec { return canonicalCodec{} }

func (canonicalCodec) ToParameterExtractorField(d *types.Descriptor) *Shape {
	if d == nil {
		return unsupported(sourceKind(nil))
	}
	if d.Kind == types.Enum && types.IsWellFormedEnum(d) {
		return exact("string", SourceScalar)
	}
	switch {
	case d.Equal(types.StringType()):
		return exact("string", sourceKind(d))
	case d.Equal(types.FloatType()):
		return exact("number", sourceKind(d))
	case d.Equal(types.BoolType()):
		return exact("boolean", sourceKind(d))
	case d.Equal(types.ArrayType(types.StringType())):
		return exact("array[string]", sourceKind(d))
	case d.Equal(types.ArrayType(types.FloatType())):
		return exact("array[number]", sourceKind(d))
	case d.Equal(types.ArrayType(types.BoolType())):
		return exact("array[boolean]", sourceKind(d))
	case d.Equal(types.ArrayType(types.MapType(types.StringType(), types.AnyType()))):
		return exact("array[object]", sourceKind(d))
	}
	return unsupported(sourceKind(d))
}

func exact(valueType PhysicalType, kind SourceKind) *Shape {
	return &Shape{ValueType: valueType, SourceKind: kind, Loss: LossExact}
}

func unsupported(kind SourceKind) *Shape {
	return &Shape{SourceKind: kind, Loss: LossUnsupported}
}

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
