package plugin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SYMBOL-SPEC V3: a capability names what a tool implements, so an
// implementation package can serve several symbols — and its id no longer has
// to double as the library name.
func TestCapabilityAliasesResolveToOneImplementation(t *testing.T) {
	ctx := context.Background()
	m := New()
	p := &Plugin{
		ID: "impl_cut", Version: "1.0.0", Type: PluginCallable,
		Tools: []Tool{{
			Name:         "create_draft",
			Capabilities: []string{"capcut.create_draft", "capcut_video_compose.create_draft"},
			Call: func(ctx context.Context, args map[string]any) (any, error) {
				return "draft", nil
			},
		}},
	}
	if err := m.Register(p); err != nil {
		t.Fatalf("Register: %v", err)
	}
	for _, name := range []string{
		"capcut.create_draft",
		"capcut_video_compose.create_draft",
		"impl_cut/create_draft",
	} {
		if _, ok := m.Lookup(name); !ok {
			t.Fatalf("Lookup(%q) failed", name)
		}
		got, err := m.Call(ctx, name, nil)
		if err != nil {
			t.Fatalf("Call(%q): %v", name, err)
		}
		if got != "draft" {
			t.Fatalf("Call(%q) = %v, want draft", name, got)
		}
	}
	if err := m.Unregister(p.ID); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if _, ok := m.Lookup("capcut.create_draft"); ok {
		t.Fatal("capability survived Unregister")
	}
}

func TestCapabilityGrammarRejected(t *testing.T) {
	m := New()
	p := &Plugin{
		ID: "impl_bad", Version: "1", Type: PluginCallable,
		Tools: []Tool{{
			Name:         "op",
			Capabilities: []string{"no-dot-here"},
			Call:         func(context.Context, map[string]any) (any, error) { return nil, nil },
		}},
	}
	err := m.Register(p)
	if err == nil || !strings.Contains(err.Error(), "invalid capability") {
		t.Fatalf("err = %v, want invalid capability", err)
	}
}

func TestDuplicateCapabilityAcrossToolsRejected(t *testing.T) {
	m := New()
	p := &Plugin{
		ID: "impl_dup", Version: "1", Type: PluginCallable,
		Tools: []Tool{
			{Name: "a", Capabilities: []string{"capcut.create_draft"}, Call: func(context.Context, map[string]any) (any, error) { return nil, nil }},
			{Name: "b", Capabilities: []string{"capcut.create_draft"}, Call: func(context.Context, map[string]any) (any, error) { return nil, nil }},
		},
	}
	err := m.Register(p)
	if err == nil || !strings.Contains(err.Error(), "declared by both") {
		t.Fatalf("err = %v, want duplicate capability", err)
	}
}

func TestCapabilityConflictWithOtherPlugin(t *testing.T) {
	m := New()
	first := &Plugin{ID: "impl_one", Version: "1", Type: PluginCallable, Tools: []Tool{{
		Name: "op", Capabilities: []string{"capcut.create_draft"},
		Call: func(context.Context, map[string]any) (any, error) { return nil, nil },
	}}}
	if err := m.Register(first); err != nil {
		t.Fatalf("register first: %v", err)
	}
	second := &Plugin{ID: "impl_two", Version: "1", Type: PluginCallable, Tools: []Tool{{
		Name: "op", Capabilities: []string{"capcut.create_draft"},
		Call: func(context.Context, map[string]any) (any, error) { return nil, nil },
	}}}
	if err := m.Register(second); err == nil {
		t.Fatal("expected capability conflict error")
	}
}

func TestCapabilitiesLoadFromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "impl.yaml")
	manifest := `id: impl_cut
version: 1.0.0
type: http
server_url: http://127.0.0.1:1
tools:
- name: create_draft
  method: POST
  path: /draft
  capabilities: [capcut.create_draft, capcut_video_compose.create_draft]
`
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	p, err := ManifestFromFile(path)
	if err != nil {
		t.Fatalf("ManifestFromFile: %v", err)
	}
	if got := len(p.Tools[0].Capabilities); got != 2 {
		t.Fatalf("capabilities = %v, want 2 entries", p.Tools[0].Capabilities)
	}
	m := New()
	if err := m.Register(p); err != nil {
		t.Fatalf("Register: %v", err)
	}
	for _, name := range []string{"capcut.create_draft", "capcut_video_compose.create_draft"} {
		if _, ok := m.Lookup(name); !ok {
			t.Fatalf("Lookup(%q) failed", name)
		}
	}
}
