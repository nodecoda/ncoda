package spec

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

// extract(..., vision: img) 的 vision 操作数必须作为独立输入口进入 spec，
// 而不是被折叠进 prompt 文本或静默丢失。
func TestBuildLLMCarriesExtractionVision(t *testing.T) {
	ns, err := fromNode(&ir.LLMCall{
		ID:    "100001",
		Model: "m",
		Extraction: &ir.ExtractionSpec{
			Instruction: "read the image",
			Vision:      &ir.Source{BlockID: "100000", Name: "image"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := DecodeConfig(ns.Kind, ns.Config)
	if err != nil {
		t.Fatal(err)
	}
	lc := cfg.(*LLMConfig)
	if lc.Vision == nil {
		t.Fatal("vision not carried into spec")
	}
	if lc.Vision.BlockID != "100000" || lc.Vision.Name != "image" {
		t.Fatalf("vision=%+v", *lc.Vision)
	}
	found := false
	for _, in := range lc.Inputs {
		if len(in.Path) == 1 && in.Path[0] == "vision" {
			found = true
			if in.BlockID != "100000" || in.Name != "image" {
				t.Fatalf("vision input=%+v", in)
			}
		}
	}
	if !found {
		t.Fatalf("vision input port missing: %+v", lc.Inputs)
	}
}

func TestBuildLLMWithoutExtractionHasNoVision(t *testing.T) {
	ns, err := fromNode(&ir.LLMCall{ID: "100001", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := DecodeConfig(ns.Kind, ns.Config)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.(*LLMConfig).Vision != nil {
		t.Fatal("unexpected vision on a plain llm call")
	}
}
