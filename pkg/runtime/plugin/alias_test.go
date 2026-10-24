package plugin

import (
	"context"
	"testing"
)

func registerImageGen(t *testing.T) *Manager {
	t.Helper()
	m := New()
	p := &Plugin{
		ID: "local_image_generate", Version: "1", Type: PluginCallable,
		Tools: []Tool{{
			Name:         "image_generator",
			Capabilities: []string{"image_generate.image_generator"},
			Call: func(context.Context, map[string]any) (any, error) {
				return map[string]any{"image_url": "file:///tmp/x.png"}, nil
			},
		}},
	}
	if err := m.Register(p); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return m
}

// 绑定层把平台名接到符号上：查找/调用/Names 都必须认这个别名。
func TestAliasBindsPlatformNameToSymbol(t *testing.T) {
	m := registerImageGen(t)
	const alias = "plugin_7547770968681447458/image_generator"
	if err := m.Alias(alias, "image_generate.image_generator"); err != nil {
		t.Fatalf("Alias: %v", err)
	}
	if _, ok := m.Lookup(alias); !ok {
		t.Fatalf("Lookup(%q) failed", alias)
	}
	if !contains(m.Names(), alias) {
		t.Fatalf("Names() 不含别名：%v", m.Names())
	}
	got, err := m.Call(context.Background(), alias, nil)
	if err != nil {
		t.Fatalf("Call(%q): %v", alias, err)
	}
	if got.(map[string]any)["image_url"] != "file:///tmp/x.png" {
		t.Fatalf("Call = %v", got)
	}
	if got := m.Aliases()[alias]; got != "local_image_generate/image_generator" {
		t.Fatalf("Aliases[%q] = %q", alias, got)
	}
}

func TestAliasRequiresRegisteredTarget(t *testing.T) {
	m := registerImageGen(t)
	if err := m.Alias("plugin_1/x", "no_such_library.no_such_fn"); err == nil {
		t.Fatal("alias to unregistered target must fail")
	}
}

func TestAliasCannotShadowRealTool(t *testing.T) {
	m := registerImageGen(t)
	if err := m.Alias("local_image_generate/image_generator", "image_generate.image_generator"); err == nil {
		t.Fatal("alias shadowing a real logical name must fail")
	}
}

func TestAliasConflictRequiresUnregister(t *testing.T) {
	m := registerImageGen(t)
	p := &Plugin{ID: "other", Version: "1", Type: PluginCallable, Tools: []Tool{{
		Name: "image_generator",
		Call: func(context.Context, map[string]any) (any, error) { return nil, nil },
	}}}
	if err := m.Register(p); err != nil {
		t.Fatal(err)
	}
	if err := m.Alias("plugin_1/x", "image_generate.image_generator"); err != nil {
		t.Fatal(err)
	}
	if err := m.Alias("plugin_1/x", "other/image_generator"); err == nil {
		t.Fatal("rebinding an alias to a different target must fail")
	}
}

func TestUnregisterRemovesAliases(t *testing.T) {
	m := registerImageGen(t)
	const alias = "plugin_1/x"
	if err := m.Alias(alias, "image_generate.image_generator"); err != nil {
		t.Fatal(err)
	}
	if err := m.Unregister("local_image_generate"); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Lookup(alias); ok {
		t.Fatal("alias survived Unregister")
	}
	if len(m.Aliases()) != 0 {
		t.Fatalf("aliases = %v", m.Aliases())
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
