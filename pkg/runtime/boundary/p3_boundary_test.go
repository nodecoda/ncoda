package boundary

// p3_boundary_test.go — P3 ES-005 L2 gate.
//
// The gate drives the production constructor (DefaultPolicy/New), so deleting
// the object-member fill in boundary.go is observable here.

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// ES-005: object{textList:array<string>} with value {} materialises the
// declared member key with null (container zero), never [] and never absent.
// Undeclared members pass through untouched.
func TestP3ES005ObjectMemberMissingKey(t *testing.T) {
	decl := map[string]*spec.TypeSpec{
		"payload": {
			Kind: "object",
			Props: map[string]*spec.TypeSpec{
				"textList": {Kind: "list", Elem: &spec.TypeSpec{Kind: "string"}},
			},
		},
	}
	in := map[string]any{"payload": map[string]any{"undeclared": "keep"}}
	out, _, err := fillWith(t, DefaultPolicy(), decl, in)
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	obj, ok := out["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload = %#v, want object", out["payload"])
	}
	v, present := obj["textList"]
	if !present {
		t.Fatalf("declared member key is absent: %#v", obj)
	}
	if v != nil {
		t.Fatalf("declared member = %#v, want null (not [] and not absent)", v)
	}
	if got := obj["undeclared"]; got != "keep" {
		t.Fatalf("undeclared member mutated: %#v", got)
	}
}

// ES-005 recursion: nested declared object members are materialised too; only
// declared props are touched (undeclared nested keys are preserved).
func TestP3ES005NestedObjectMemberRecursion(t *testing.T) {
	decl := map[string]*spec.TypeSpec{
		"payload": {
			Kind: "object",
			Props: map[string]*spec.TypeSpec{
				"meta": {
					Kind: "object",
					Props: map[string]*spec.TypeSpec{
						"textList": {Kind: "list", Elem: &spec.TypeSpec{Kind: "string"}},
					},
				},
			},
		},
	}
	in := map[string]any{"payload": map[string]any{"meta": map[string]any{"keep": "v"}}}
	out, _, err := fillWith(t, DefaultPolicy(), decl, in)
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	payload := out["payload"].(map[string]any)
	meta, ok := payload["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta = %#v, want object", payload["meta"])
	}
	if v, present := meta["textList"]; !present || v != nil {
		t.Fatalf("nested declared member = %#v (present=%v), want null", v, present)
	}
	if meta["keep"] != "v" {
		t.Fatalf("undeclared nested member mutated: %#v", meta)
	}
}
