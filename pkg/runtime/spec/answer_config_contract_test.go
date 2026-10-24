package spec

// answer_config_contract_test.go — the runtime execution subset must carry
// the answer endpoint through the production loader, not just through the
// in-memory Build path. If `answer` is dropped by Marshal/Normalize/
// DecodeJSON, a `.json` spec run silently loses the workflow's final reply.

import (
	"encoding/json"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

func TestEndAnswerConfigSurvivesLoader(t *testing.T) {
	cfg, err := json.Marshal(&EndConfig{Answer: &EndAnswerConfig{
		Template: "hi {{n}}",
		Bindings: []EndOutput{{Name: "n", Ref: Ref{Producer: "start", Output: "x"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Spec{
		Version: Version,
		Entry:   &EntrySpec{ID: "start", Params: []ParamSpec{{Name: "x", Type: &TypeSpec{Kind: "string"}}}},
		Nodes: []NodeSpec{
			{ID: "start", Kind: string(ir.KindStart), Config: json.RawMessage(`{}`)},
			{ID: "e1", Kind: string(ir.KindEnd), Config: cfg},
		},
	}
	raw, err := s.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	loaded, err := DecodeJSON(raw)
	if err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	var endRaw json.RawMessage
	for _, n := range loaded.Nodes {
		if n.Kind == string(ir.KindEnd) {
			endRaw = n.Config
		}
	}
	if endRaw == nil {
		t.Fatal("loaded spec has no end node")
	}
	decoded, err := DecodeConfig(string(ir.KindEnd), endRaw)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	got := decoded.(*EndConfig).Answer
	if got == nil {
		t.Fatal("answer endpoint dropped by the loader")
	}
	if got.Template != "hi {{n}}" {
		t.Errorf("template = %q, want %q", got.Template, "hi {{n}}")
	}
	if len(got.Bindings) != 1 || got.Bindings[0].Name != "n" || got.Bindings[0].Ref.Producer != "start" || got.Bindings[0].Ref.Output != "x" {
		t.Errorf("bindings = %+v, want [{n start x}]", got.Bindings)
	}
}
