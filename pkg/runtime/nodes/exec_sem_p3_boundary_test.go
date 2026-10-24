package nodes

// exec_sem_p3_boundary_test.go — P3 ES-005 / ES-044 code-side gates.
//
// ES-044's shared helper (assertBoundaryPassthrough) is family-agnostic: llm
// and tool reuse it once lane A binds their declared types to the same
// internal/runtime/boundary. Today those nodes declare no types, so their fill
// is honestly a no-op — the first block of the helper asserts exactly that
// state; it does not pretend a fill happened.

import (
	"context"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/boundary"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// p3PolicyCase is one prefill profile driven through the production policy
// loader. file == "" means the built-in coze-biz default (prefill zero).
type p3PolicyCase struct {
	name string
	file string
	mode boundary.FillMode
}

func p3Policy(t *testing.T, file string) boundary.Policy {
	t.Helper()
	if file == "" {
		return boundary.DefaultPolicy()
	}
	pol, err := boundary.LoadPolicy(policyPath(file))
	if err != nil {
		t.Fatalf("load policy %s: %v", file, err)
	}
	return pol
}

func p3UsePolicy(t *testing.T, file string) {
	t.Helper()
	if file == "" {
		t.Setenv(boundary.EnvPolicyPath, "")
		return
	}
	t.Setenv(boundary.EnvPolicyPath, policyPath(file))
}

// assertBoundaryPassthrough is the ES-044 shared helper. It asserts the two
// contract halves for whatever ValueBoundary a node family uses:
//
//  1. no declared inputs → pass-through (absent stays absent, null stays null,
//     present values untouched). This is the current llm/tool state.
//  2. declared inputs → absent/null follow the boundary's PreFill profile
//     (zero: scalar zero / container null; nil: both null; none: absent stays
//     absent, explicit null stays null).
//
// llm/tool lane A calls it with the boundary they build; code calls it below
// with the same production policy loader the node uses.
func assertBoundaryPassthrough(t *testing.T, vb *boundary.ValueBoundary) {
	t.Helper()

	// 1) No declared inputs: the boundary must not reshape anything.
	in := map[string]any{"present": "v", "null": nil}
	out, err := vb.FillInputs(in, nil, 1)
	if err != nil {
		t.Fatalf("fill (no declarations): %v", err)
	}
	if got, ok := out["present"]; !ok || got != "v" {
		t.Fatalf("no declarations: present = %#v (present=%v), want v", got, ok)
	}
	if got, ok := out["null"]; !ok || got != nil {
		t.Fatalf("no declarations: null = %#v (present=%v), want explicit null", got, ok)
	}
	if _, ok := out["absent"]; ok {
		t.Fatalf("no declarations: absent key was invented: %#v", out)
	}

	// 2) Declared inputs: the profile decides absent/null.
	decl := map[string]*spec.TypeSpec{
		"absent_s":    {Kind: "string"},
		"null_s":      {Kind: "string"},
		"absent_list": {Kind: "list", Elem: &spec.TypeSpec{Kind: "string"}},
		"null_list":   {Kind: "list", Elem: &spec.TypeSpec{Kind: "string"}},
	}
	out, err = vb.FillInputs(map[string]any{"null_s": nil, "null_list": nil}, decl, 1)
	if err != nil {
		t.Fatalf("fill (declared): %v", err)
	}
	switch vb.Policy().PreFill {
	case boundary.PreFillZero:
		if out["absent_s"] != "" || out["null_s"] != "" {
			t.Fatalf("zero: scalar = %#v/%#v, want empty strings", out["absent_s"], out["null_s"])
		}
		for _, k := range []string{"absent_list", "null_list"} {
			if v, ok := out[k]; !ok || v != nil {
				t.Fatalf("zero: %s = %#v (present=%v), want explicit null", k, v, ok)
			}
		}
	case boundary.PreFillNil:
		for _, k := range []string{"absent_s", "null_s", "absent_list", "null_list"} {
			if v, ok := out[k]; !ok || v != nil {
				t.Fatalf("nil: %s = %#v (present=%v), want explicit null", k, v, ok)
			}
		}
	case boundary.PreFillNone:
		for _, k := range []string{"absent_s", "absent_list"} {
			if v, ok := out[k]; ok {
				t.Fatalf("none: absent %s was materialised: %#v", k, v)
			}
		}
		for _, k := range []string{"null_s", "null_list"} {
			if v, ok := out[k]; !ok || v != nil {
				t.Fatalf("none: %s = %#v (present=%v), want explicit null", k, v, ok)
			}
		}
	default:
		t.Fatalf("unexpected profile %q", vb.Policy().PreFill)
	}
}

// ES-044: the code node consumes the single boundary under all three prefill
// profiles, with and without declared inputs.
func TestP3ES044CodeBoundaryProfiles(t *testing.T) {
	cases := []p3PolicyCase{
		{name: "zero", file: "", mode: boundary.PreFillZero},
		{name: "nil", file: "prefill_nil.yaml", mode: boundary.PreFillNil},
		{name: "none", file: "drop_prefill.yaml", mode: boundary.PreFillNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pol := p3Policy(t, tc.file)
			if pol.PreFill != tc.mode {
				t.Fatalf("policy %s prefill = %q, want %q", tc.name, pol.PreFill, tc.mode)
			}
			vb := boundary.New(boundary.Options{Policy: pol, NodeID: "code_p3", Kind: spec.KindCode})
			assertBoundaryPassthrough(t, vb)

			p3UsePolicy(t, tc.file)

			// No declared inputs: the node sees the raw values (no shaping).
			plain := &spec.CodeConfig{
				Code:      "def main(extra) -> dict:\n    return {\"result\": extra}",
				Language:  "python",
				Synthetic: true,
				OutOrder:  []string{"result"},
			}
			n := buildP1a2Code(t, "code_p3_"+tc.name+"_plain", plain, nil)
			out, err := n.Invoke(context.Background(), map[string]any{"extra": "keep"})
			if err != nil {
				t.Fatalf("invoke (no declarations): %v", err)
			}
			if out["result"] != "keep" {
				t.Fatalf("no declarations: code received %#v, want keep", out["result"])
			}

			// Declared scalar: null input follows the profile.
			declared := &spec.CodeConfig{
				Code:      "def main(title) -> dict:\n    return {\"result\": title}",
				Language:  "python",
				Synthetic: true,
				Types:     map[string]*spec.TypeSpec{"title": {Kind: "string"}},
				OutOrder:  []string{"result"},
			}
			n = buildP1a2Code(t, "code_p3_"+tc.name+"_decl", declared, nil)
			out, err = n.Invoke(context.Background(), map[string]any{"title": nil})
			if err != nil {
				t.Fatalf("invoke (null declared): %v", err)
			}
			wantNull := tc.mode == boundary.PreFillZero
			if wantNull {
				if out["result"] != "" {
					t.Fatalf("%s: null declared input reached code as %#v, want empty string", tc.name, out["result"])
				}
			} else if v, ok := out["result"]; !ok || v != nil {
				t.Fatalf("%s: null declared input reached code as %#v (present=%v), want null", tc.name, v, ok)
			}

			// Declared scalar: absent input follows the profile (none leaves it
			// absent, so the shim reports the undefined name honestly).
			out, err = n.Invoke(context.Background(), map[string]any{})
			if tc.mode == boundary.PreFillNone {
				if err == nil {
					t.Fatal("prefill none: absent declared input must stay absent (shim must report undefined name)")
				}
				return
			}
			if err != nil {
				t.Fatalf("invoke (absent declared): %v", err)
			}
			if tc.mode == boundary.PreFillZero {
				if out["result"] != "" {
					t.Fatalf("zero: absent declared input reached code as %#v, want empty string", out["result"])
				}
			} else if v, ok := out["result"]; !ok || v != nil {
				t.Fatalf("nil: absent declared input reached code as %#v (present=%v), want null", v, ok)
			}
		})
	}
}

// ES-005 at the code-node boundary: declared object{textList:array<string>}
// with value {} reaches user code with the member key present and null.
func TestP3ES005CodeObjectMemberMissingKey(t *testing.T) {
	p3UsePolicy(t, "")
	cfg := &spec.CodeConfig{
		Code:      "def main(payload) -> dict:\n    return {\"result\": payload}",
		Language:  "python",
		Synthetic: true,
		Types: map[string]*spec.TypeSpec{
			"payload": {
				Kind: "object",
				Props: map[string]*spec.TypeSpec{
					"textList": {Kind: "list", Elem: &spec.TypeSpec{Kind: "string"}},
				},
			},
		},
		OutOrder: []string{"result"},
	}
	n := buildP1a2Code(t, "code_p3_005", cfg, nil)
	out, err := n.Invoke(context.Background(), map[string]any{
		"payload": map[string]any{"undeclared": "keep"},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	payload, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("code result = %#v, want object", out["result"])
	}
	v, present := payload["textList"]
	if !present {
		t.Fatalf("declared member key is absent after code fill: %#v", payload)
	}
	if v != nil {
		t.Fatalf("declared member = %#v, want null (not [] and not absent)", v)
	}
	if payload["undeclared"] != "keep" {
		t.Fatalf("undeclared member mutated: %#v", payload)
	}
}
