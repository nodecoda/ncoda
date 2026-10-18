package binding

// Target-neutral frontend contracts for schema-driven extraction, ported
// 1:1 from src/nclang/lang/extraction_contract.py (82 lines). The Python
// frozen dataclasses become Go structs; build_extraction_contract's
// ValueError texts are reused for the error messages (the classification
// contract pattern). No validated constructor exists in the reference —
// ValidatedExtractionContract is a plain frozen dataclass with defaults
// (memory_turns=None, vision=False), so the Go struct is built directly by
// the target-validation pass.
//
// Mapping notes (recorded in task-25f-report.md):
//   - Python stores fields as tuples and options as a tuple; the Go
//     contracts keep ordered slices.
//   - Python freezes the value_type/result_type descriptors (freeze_type
//     deep-copies); Go follows the immutable-descriptor convention (shared
//     pointer, never mutated) — the same deviation recorded for Snapshot
//     publish and the agent/classification contracts.
//   - memory_turns is int | None; Go uses *int (nil = Python None).

import (
	"fmt"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/types"
)

// ExtractionField mirrors ExtractionField (extraction_contract.py:9-12):
// one schema field with its target-neutral type.
type ExtractionField struct {
	Name     string
	TypeInfo *types.Descriptor
}

// ExtractionContract mirrors ExtractionContract (extraction_contract.py
// :14-19): one resolved record schema plus its closed Extraction[Name]
// result shape.
type ExtractionContract struct {
	SchemaName string
	ValueType  *types.Descriptor
	ResultType *types.Descriptor
	Fields     []ExtractionField
}

// ValidatedExtractionField mirrors ValidatedExtractionField
// (extraction_contract.py:21-27): one target-certified field binding with
// its Graphon parameter shape, description and optional enum options.
type ValidatedExtractionField struct {
	Name         string
	NodecodaType *types.Descriptor
	GraphonType  string
	Description  string
	Options      []string
}

// ValidatedExtractionContract mirrors ValidatedExtractionContract
// (extraction_contract.py:29-37): one statically closed extraction
// invocation resolved against a target profile. MemoryTurns is nil when the
// call carries no memory config (Python None); Vision records whether the
// call passed a validated vision selector.
type ValidatedExtractionContract struct {
	Schema      *ExtractionContract
	Model       string
	Instruction string
	Strategy    string
	Fields      []ValidatedExtractionField
	MemoryTurns *int
	Vision      bool
}

// ExtractionResultType mirrors extraction_result_type
// (extraction_contract.py:39-47): the record "Extraction[<schema_name>]"
// with value/ok/reason/usage fields. The record name is dropped by the
// Task 7 record rule (types.go RecordType) — the structural shape is what
// this layer's compatibility checks consume.
func ExtractionResultType(schemaName string, valueType *types.Descriptor) *types.Descriptor {
	return types.RecordType(fmt.Sprintf("Extraction[%s]", schemaName), []types.RecordField{
		{Name: "value", Type: valueType},
		{Name: "ok", Type: types.BoolType()},
		{Name: "reason", Type: types.StringType()},
		{Name: "usage", Type: types.MapType(types.StringType(), types.AnyType())},
	})
}

// BuildExtractionContract mirrors build_extraction_contract
// (extraction_contract.py:52-67): resolve one named record into its
// target-neutral extraction result, returning the Python ValueError message
// text on each rejection.
func BuildExtractionContract(typeRef ast.TypeRef, namedTypes map[string]*types.Descriptor) (*ExtractionContract, error) {
	named, ok := typeRef.(*ast.NamedTypeRef)
	if !ok {
		return nil, errExtractionContract("extraction schema must be a named record type")
	}
	resolved := namedTypes[named.Name()]
	if resolved == nil {
		return nil, errExtractionContract(fmt.Sprintf("unknown extraction schema '%s'", named.Name()))
	}
	if resolved.Kind != types.Record {
		return nil, errExtractionContract(fmt.Sprintf("extraction schema '%s' must be a record", named.Name()))
	}
	if len(resolved.Fields) == 0 {
		return nil, errExtractionContract(fmt.Sprintf("extraction schema '%s' must contain at least one field", named.Name()))
	}
	fields := make([]ExtractionField, 0, len(resolved.Fields))
	for _, field := range resolved.Fields {
		fields = append(fields, ExtractionField{Name: field.Name, TypeInfo: field.Type})
	}
	return &ExtractionContract{
		SchemaName: named.Name(),
		ValueType:  resolved,
		ResultType: ExtractionResultType(named.Name(), resolved),
		Fields:     fields,
	}, nil
}

// errExtractionContract wraps one extraction-contract invariant as an error
// (the Go analogue of the ValueError raised by build_extraction_contract).
func errExtractionContract(message string) error {
	return &extractionContractError{message: message}
}

type extractionContractError struct{ message string }

func (e *extractionContractError) Error() string { return e.message }
