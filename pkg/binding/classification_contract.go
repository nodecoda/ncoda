package binding

// Immutable frontend contracts for enum-valued classification, ported 1:1
// from src/nclang/lang/classification_contract.py (69 lines). The Python
// frozen dataclasses with __post_init__ invariants become Go structs built
// through validated constructors (the human_input_contract.go and
// agent_contract.go patterns); the same invariant texts are reused for the
// error messages.
//
// Mapping notes (recorded in task-25e-report.md):
//   - Python classes is a frozen tuple; the Go contract keeps an ordered
//     slice, and OrderedClasses() reconstructs the reference's fallback-
//     first tuple.
//   - Python __post_init__ freezes the enum descriptor (freeze_type deep-
//     copies); Go follows the immutable-descriptor convention (shared
//     pointer, never mutated) — the same deviation recorded for Snapshot
//     publish and the agent contracts.
//   - resolve_classification_enum returns the frozen descriptor; Go returns
//     the resolved shared pointer.

import (
	"fmt"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/types"
)

// ClassificationClass mirrors ClassificationClass (classification_contract.py
// :15-18): one enum member paired with its human-readable label.
type ClassificationClass struct {
	Member string
	Label  string
}

// ValidatedClassificationContract mirrors ValidatedClassificationContract
// (classification_contract.py:20-51): one statically closed classification
// invocation resolved against a nominal enum type. Query is the AstNodeID
// of the call's query argument (Python AstNodeId).
type ValidatedClassificationContract struct {
	EnumType       *types.Descriptor
	Model          string
	Query          AstNodeID
	Instruction    string
	Classes        []ClassificationClass
	FallbackMember string
}

// NewValidatedClassificationContract validates and builds one contract,
// mirroring ValidatedClassificationContract.__post_init__
// (classification_contract.py:29-48). It returns an error carrying the
// Python ValueError message text when the shape is invalid; Classes is
// copied so the contract stays immutable.
func NewValidatedClassificationContract(enumType *types.Descriptor, model string, query AstNodeID, instruction string, classes []ClassificationClass, fallbackMember string) (*ValidatedClassificationContract, error) {
	if !types.IsWellFormedEnum(enumType) {
		return nil, errClassification("Classification requires a well-formed nominal enum type")
	}
	members := make([]string, len(classes))
	for i, item := range classes {
		members[i] = item.Member
	}
	if !equalStrings(members, enumType.EnumMembers) {
		return nil, errClassification("Classification classes must cover the enum exactly in declaration order")
	}
	descriptions := make([]string, len(classes))
	for i, item := range classes {
		descriptions[i] = item.Label
	}
	if !allDistinct(descriptions) {
		return nil, errClassification("Classification class descriptions must be unique")
	}
	if !containsString(members, fallbackMember) {
		return nil, errClassification("Classification fallback must belong to the classified enum")
	}
	return &ValidatedClassificationContract{
		EnumType:       enumType,
		Model:          model,
		Query:          query,
		Instruction:    instruction,
		Classes:        append([]ClassificationClass(nil), classes...),
		FallbackMember: fallbackMember,
	}, nil
}

// OrderedClasses places the explicit fallback first while preserving all
// other enum order, mirroring ValidatedClassificationContract.ordered_classes
// (classification_contract.py:53-57).
func (c *ValidatedClassificationContract) OrderedClasses() []ClassificationClass {
	fallback := -1
	for i, item := range c.Classes {
		if item.Member == c.FallbackMember {
			fallback = i
			break
		}
	}
	if fallback < 0 {
		return append([]ClassificationClass(nil), c.Classes...)
	}
	out := make([]ClassificationClass, 0, len(c.Classes))
	out = append(out, c.Classes[fallback])
	for i, item := range c.Classes {
		if i != fallback {
			out = append(out, item)
		}
	}
	return out
}

// ResolveClassificationEnum mirrors resolve_classification_enum
// (classification_contract.py:59-69): resolve the one permitted nominal
// enum type argument, returning the Python ValueError message text on each
// rejection. The Go form returns the resolved descriptor (immutable
// convention) instead of a frozen copy.
func ResolveClassificationEnum(ref ast.TypeRef, namedTypes map[string]*types.Descriptor) (*types.Descriptor, error) {
	named, ok := ref.(*ast.NamedTypeRef)
	if !ok {
		return nil, errClassification("classification type argument must be a nominal enum type")
	}
	enumType := namedTypes[named.Name()]
	if enumType == nil {
		return nil, errClassification(fmt.Sprintf("unknown classification enum '%s'", named.Name()))
	}
	if enumType.Kind != types.Enum || !types.IsWellFormedEnum(enumType) {
		return nil, errClassification(fmt.Sprintf("classification type argument '%s' must be an enum", named.Name()))
	}
	return enumType, nil
}

// errClassification wraps one contract invariant as an error (the Go
// analogue of the Python ValueError raised in __post_init__ /
// resolve_classification_enum).
func errClassification(message string) error {
	return &classificationContractError{message: message}
}

type classificationContractError struct{ message string }

func (e *classificationContractError) Error() string { return e.message }

// allDistinct reports whether every element of values is unique
// (Python len(set(...)) != len(...)).
func allDistinct(values []string) bool {
	seen := make(map[string]bool, len(values))
	for _, v := range values {
		if seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}

// containsString reports whether values holds target.
func containsString(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

// equalStrings reports whether a and b hold the same elements in order
// (the Go analogue of Python tuple equality over enum members).
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
