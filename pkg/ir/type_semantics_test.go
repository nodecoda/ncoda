package ir

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	dsltypes "github.com/nodecoda/ncoda/pkg/types"
)

func TestIRPreservesOptionalTypeMarker(t *testing.T) {
	b := &Builder{}
	got := b.typeOf(ast.NewOptionalTypeRef(ast.NewPrimTypeRef("string")))
	if got == nil || got.Kind != "string" || !got.Optional {
		t.Fatalf("typeOf(string?) = %#v, want string with Optional=true", got)
	}

	got = b.typeOf(ast.NewArrayTypeRef(ast.NewOptionalTypeRef(ast.NewPrimTypeRef("int"))))
	if got == nil || got.Kind != "list" || got.Elem == nil || got.Elem.Kind != "integer" || !got.Elem.Optional {
		t.Fatalf("typeOf((int?)[]) = %#v, want list<int?>", got)
	}
}

func TestIRSemanticDescriptorPreservesOptionalRecordFields(t *testing.T) {
	b := &Builder{}
	got := b.semanticDescriptorType(dsltypes.RecordType("R", []dsltypes.RecordField{
		{Name: "value", Type: dsltypes.OptionalType(dsltypes.StringType())},
	}))
	if got == nil || got.Kind != "object" || got.Props["value"] == nil || !got.Props["value"].Optional {
		t.Fatalf("semantic record optional field lost: %#v", got)
	}
}
