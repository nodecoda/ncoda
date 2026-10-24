package spec

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestLiftNestedToolRefs(t *testing.T) {
	original := map[string]any{
		"inputs": map[string]any{
			"desc_prompt": map[string]any{"path": "prompt_text", "ref_node": "100001"},
		},
		"references": []any{
			map[string]any{
				"url":    map[string]any{"path": "image", "ref_node": "100003"},
				"weight": 0.4,
			},
		},
	}
	seq := 0
	value, refs := liftNestedToolRefs("100002", "params", original, &seq)
	if len(refs) != 2 {
		t.Fatalf("refs=%+v", refs)
	}
	for _, src := range refs {
		if len(src.Path) != 2 || src.Path[0] != NestedRefMapKey || src.Path[1] == "" {
			t.Fatalf("source path=%v", src.Path)
		}
		if src.BlockID == "" || src.Name == "" {
			t.Fatalf("source=%+v", src)
		}
	}
	// 原 IR 值树不被就地改写。
	if _, err := json.Marshal(original); err != nil {
		t.Fatal(err)
	}
	if m := original["inputs"].(map[string]any)["desc_prompt"].(map[string]any); m[NestedRefMarker] != nil {
		t.Fatalf("original mutated: %#v", m)
	}
	// 克隆值树带唯一 marker。
	clone := value.(map[string]any)
	in := clone["inputs"].(map[string]any)["desc_prompt"].(map[string]any)
	if in[NestedRefMarker] == "" || in["ref_node"] != "100001" || in["path"] != "prompt_text" {
		t.Fatalf("nested leaf=%#v", in)
	}
	arr := clone["references"].([]any)
	leaf := arr[0].(map[string]any)["url"].(map[string]any)
	if leaf[NestedRefMarker] == "" || leaf[NestedRefMarker] == in[NestedRefMarker] {
		t.Fatalf("markers not unique: %#v %#v", leaf, in)
	}
}

func TestToolConfigNestedRefsAreSources(t *testing.T) {
	cfg := &ToolConfig{
		ToolName: "image_generate",
		Params: []ToolParam{{
			Key: "inputs",
			Value: map[string]any{
				"desc_prompt": map[string]any{
					"path": "prompt_text", "ref_node": "100001", NestedRefMarker: "t1",
				},
			},
		}},
		NestedRefs: []Source{{Path: []string{NestedRefMapKey, "t1"}, BlockID: "100001", Name: "prompt_text"}},
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srcs, err := Sources(&NodeSpec{Kind: KindTool, Config: raw})
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 1 || srcs[0].BlockID != "100001" || srcs[0].Name != "prompt_text" {
		t.Fatalf("sources=%+v", srcs)
	}
	if len(srcs[0].Path) != 2 || srcs[0].Path[0] != NestedRefMapKey {
		t.Fatalf("path=%v", srcs[0].Path)
	}
}

// Determinism: sibling nested refs must be lifted in sorted key order. Go
// map iteration is randomized, and the marker token embeds the visit
// sequence, so an unsorted walk changes spec bytes run to run.
func TestLiftNestedToolRefsDeterministicMapOrder(t *testing.T) {
	var first []Source
	for i := 0; i < 50; i++ {
		value := map[string]any{
			"inputs": map[string]any{
				"z_title": map[string]any{"path": "title", "ref_node": "100001"},
				"a_icon":  map[string]any{"path": "icon", "ref_node": "100001"},
				"m_desc":  map[string]any{"path": "desc", "ref_node": "100001"},
			},
		}
		seq := 0
		_, refs := liftNestedToolRefs("100047", "variables", value, &seq)
		if len(refs) != 3 {
			t.Fatalf("refs=%+v", refs)
		}
		if i == 0 {
			first = refs
			continue
		}
		if !reflect.DeepEqual(refs, first) {
			t.Fatalf("run %d refs=%+v, first=%+v", i, refs, first)
		}
	}
	want := []string{"icon", "desc", "title"} // key order: a_icon, m_desc, z_title
	for i, name := range want {
		if first[i].Name != name {
			t.Fatalf("refs order=%v, want %v", first, want)
		}
		if wantToken := "100047:variables:" + string(rune('1'+i)); first[i].Path[1] != wantToken {
			t.Fatalf("token=%q, want %q", first[i].Path[1], wantToken)
		}
	}
}
