package localtools

// Options.Only（--local-plugins=<lib,…>）判据：
//   - 子集注册只出现被选中的库，别名仍然可用；
//   - 未知库名是错误（不能静默变成「什么都没注册」）；
//   - 空 Only 仍等于全开（回归）。

import (
	"context"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
)

func registerOnly(t *testing.T, only []string) *plugin.Manager {
	t.Helper()
	m := plugin.New()
	if _, err := Register(m, Options{ArtifactDir: t.TempDir(), Only: only}); err != nil {
		t.Fatalf("Register(only=%v): %v", only, err)
	}
	return m
}

func hasName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func TestRegisterOnlySubset(t *testing.T) {
	m := registerOnly(t, []string{"array_count"})
	names := m.Names()
	if !hasName(names, "array_count/array_count") {
		t.Fatalf("selected library missing: %v", names)
	}
	for _, absent := range []string{"database/insert", "tts/speech_synthesis", "matting/cutout"} {
		if hasName(names, absent) {
			t.Fatalf("%q must not be registered when Only=[array_count]; names=%v", absent, names)
		}
	}
	// 子集注册后别名仍然指向可用实现。
	got, err := m.Call(context.Background(), "array_count/array_count",
		map[string]any{"array": []any{"a", "b"}})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if n, _ := got.(map[string]any)["count"].(int); n != 2 {
		t.Fatalf("count = %v, want 2", got)
	}
}

func TestRegisterOnlyUnknownLibrary(t *testing.T) {
	m := plugin.New()
	_, err := Register(m, Options{ArtifactDir: t.TempDir(), Only: []string{"database", "nope"}})
	if err == nil {
		t.Fatal("unknown library must be rejected")
	}
	if !strings.Contains(err.Error(), "unknown library") {
		t.Fatalf("error = %v", err)
	}
	// 用法错误要在注册前发现：不能留下「注册了一半」的 manager。
	if names := m.Names(); len(names) != 0 {
		t.Fatalf("manager mutated before validation: %v", names)
	}
}

func TestRegisterWithoutOnlyRegistersEverything(t *testing.T) {
	m := registerOnly(t, nil)
	names := m.Names()
	for _, want := range []string{"database/insert", "tts/speech_synthesis", "datetime/datetime"} {
		if !hasName(names, want) {
			t.Fatalf("%q missing when Only is empty; names=%v", want, names)
		}
	}
}
