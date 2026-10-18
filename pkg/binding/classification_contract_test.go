package binding

// Whitebox lock for the classification contract invariants ported from
// src/nclang/lang/classification_contract.py: the four __post_init__
// ValueError texts (:29-48) and the three resolve_classification_enum
// texts (:59-69), asserted byte-exact, plus the positive paths (resolved
// descriptor identity and fallback-first ordered_classes). Constructs the
// contract types directly — no parse needed (same convention as the
// snapshot/stdlib whitebox tests).

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/types"
)

func TestNewValidatedClassificationContractInvariants(t *testing.T) {
	enumType := types.EnumType("Intent", []string{"unknown", "support", "billing"})
	classes := []ClassificationClass{
		{Member: "unknown", Label: "Cannot determine intent"},
		{Member: "support", Label: "Technical support request"},
		{Member: "billing", Label: "Billing request"},
	}

	t.Run("non-well-formed-enum", func(t *testing.T) {
		bad := map[string]*types.Descriptor{
			"nil":           nil,
			"non-enum-kind": types.AnyType(),
			"empty-members": types.EnumType("E", nil),
		}
		for name, enum := range bad {
			t.Run(name, func(t *testing.T) {
				_, err := NewValidatedClassificationContract(enum, "provider/model", AstNodeID(1), "Classify the request", classes, "support")
				wantErr(t, err, "Classification requires a well-formed nominal enum type")
			})
		}
	})

	t.Run("classes-not-covering-enum", func(t *testing.T) {
		// Missing 'billing': members must cover the enum exactly in
		// declaration order (Python tuple equality).
		_, err := NewValidatedClassificationContract(enumType, "provider/model", AstNodeID(1), "Classify the request", classes[:2], "support")
		wantErr(t, err, "Classification classes must cover the enum exactly in declaration order")
	})

	t.Run("duplicate-descriptions", func(t *testing.T) {
		dup := append([]ClassificationClass(nil), classes...)
		dup[2].Label = dup[1].Label // billing reuses the support label
		_, err := NewValidatedClassificationContract(enumType, "provider/model", AstNodeID(1), "Classify the request", dup, "support")
		wantErr(t, err, "Classification class descriptions must be unique")
	})

	t.Run("fallback-not-in-enum", func(t *testing.T) {
		_, err := NewValidatedClassificationContract(enumType, "provider/model", AstNodeID(1), "Classify the request", classes, "other")
		wantErr(t, err, "Classification fallback must belong to the classified enum")
	})

	t.Run("positive-and-ordered-classes", func(t *testing.T) {
		c, err := NewValidatedClassificationContract(enumType, "provider/model", AstNodeID(1), "Classify the request", classes, "support")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.EnumType != enumType || c.Model != "provider/model" || c.Query != AstNodeID(1) ||
			c.Instruction != "Classify the request" || c.FallbackMember != "support" {
			t.Fatalf("unexpected contract: %+v", c)
		}
		// The caller's slice must not leak into the frozen contract.
		classes[0].Label = "mutated"
		if c.Classes[0].Label == "mutated" {
			t.Fatal("Classes slice was not copied on construction")
		}
		got := make([]string, len(c.Classes))
		for i, item := range c.OrderedClasses() {
			got[i] = item.Member
		}
		want := []string{"support", "unknown", "billing"}
		if len(got) != len(want) {
			t.Fatalf("ordered_classes = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("ordered_classes = %v, want %v", got, want)
			}
		}
	})
}

func TestResolveClassificationEnum(t *testing.T) {
	enumType := types.EnumType("Intent", []string{"unknown", "support", "billing"})
	// A well-formed non-enum named type: Kind != Enum triggers the
	// "must be an enum" rejection even though the name resolves.
	recType := types.RecordType("Rec", []types.RecordField{{Name: "a", Type: types.StringType()}})
	namedTypes := map[string]*types.Descriptor{"Intent": enumType, "Rec": recType}

	t.Run("non-nominal-ref", func(t *testing.T) {
		_, err := ResolveClassificationEnum(ast.NewPrimTypeRef("string"), namedTypes)
		wantErr(t, err, "classification type argument must be a nominal enum type")
	})

	t.Run("unknown-enum", func(t *testing.T) {
		_, err := ResolveClassificationEnum(ast.NewNamedTypeRef("Missing"), namedTypes)
		wantErr(t, err, "unknown classification enum 'Missing'")
	})

	t.Run("well-formed-non-enum", func(t *testing.T) {
		_, err := ResolveClassificationEnum(ast.NewNamedTypeRef("Rec"), namedTypes)
		wantErr(t, err, "classification type argument 'Rec' must be an enum")
	})

	t.Run("positive", func(t *testing.T) {
		got, err := ResolveClassificationEnum(ast.NewNamedTypeRef("Intent"), namedTypes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != enumType {
			t.Fatalf("resolved descriptor = %v, want the registered Intent enum", got)
		}
	})
}

// wantErr asserts err carries exactly the Python ValueError message text.
func wantErr(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %q, got nil", want)
	}
	if got := err.Error(); got != want {
		t.Fatalf("error message = %q, want %q", got, want)
	}
}
