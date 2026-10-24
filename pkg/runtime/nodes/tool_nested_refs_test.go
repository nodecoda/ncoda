package nodes

import (
	"context"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func TestToolResolvesNestedStructuredRefs(t *testing.T) {
	reg := &fakeToolReg{names: []string{"image_generate/generate"}, out: map[string]any{"msg": ""}}
	n := &toolNode{
		cfg: &spec.ToolConfig{
			ToolName:   "image_generate",
			ProviderID: "image_generate",
			Operation:  "generate",
			Params: []spec.ToolParam{{
				Key: "inputs",
				Value: map[string]any{
					"desc_prompt": map[string]any{
						"path":               "prompt_text",
						"ref_node":           "100001",
						spec.NestedRefMarker: "t1",
					},
				},
			}},
		},
		tr: reg,
	}
	in := map[string]any{
		spec.NestedRefMapKey: map[string]any{"t1": "cat"},
	}
	if _, err := n.Invoke(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	inputs, ok := reg.got["inputs"].(map[string]any)
	if !ok {
		t.Fatalf("args=%#v", reg.got)
	}
	if inputs["desc_prompt"] != "cat" {
		t.Fatalf("nested ref not substituted: %#v", inputs)
	}
}

func TestResolveNestedToolRefsInArrays(t *testing.T) {
	in := map[string]any{
		spec.NestedRefMapKey: map[string]any{"t1": "https://example.invalid/ref.png"},
	}
	value := []any{
		map[string]any{
			"url": map[string]any{
				"path":               "image",
				"ref_node":           "100003",
				spec.NestedRefMarker: "t1",
			},
			"weight": 0.4,
		},
	}
	got := resolveNestedToolRefs(value, in)
	list := got.([]any)
	item := list[0].(map[string]any)
	if item["url"] != "https://example.invalid/ref.png" || item["weight"] != 0.4 {
		t.Fatalf("ref leaf not replaced: %#v", item)
	}
	// resolveNestedToolRefs 返回叶子值本身。
	in2 := map[string]any{spec.NestedRefMapKey: map[string]any{"t1": "resolved"}}
	leaf := resolveNestedToolRefs(map[string]any{
		"path": "image", "ref_node": "100003", spec.NestedRefMarker: "t1",
	}, in2)
	if leaf != "resolved" {
		t.Fatalf("leaf=%#v", leaf)
	}
}
