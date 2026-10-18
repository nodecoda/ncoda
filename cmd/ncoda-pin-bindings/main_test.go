package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/tool"
)

// writeHeaders writes a minimal capability header root (规范根: capabilities-ncoda/)
// with two providers so the registry has real hashes to pin.
func writeHeaders(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "capabilities-ncoda")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	alpha := `// alpha.ncoda
@language nodecoda/1

type AlphaSearchResult = {
  string result;
};
@capability failure_branch=true, timeout_ms=1000
function search(string query) -> AlphaSearchResult {}
`
	beta := `// beta.ncoda
@language nodecoda/1

type BetaDoResult = {
  string out;
};
function do(string key = "") -> BetaDoResult {}
`
	for name, content := range map[string]string{"alpha.ncoda": alpha, "beta.ncoda": beta} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func writeBindings(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func loadRegistry(t *testing.T, root string) *tool.Registry {
	t.Helper()
	reg, err := tool.LoadCapabilitySearchPaths([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

const bindingYAML = `version: v1
bindings:
- provider: alpha
  operation: search
  plugin_id: '1'
  api_id: '2'
- provider: beta
  operation: do
  plugin_id: '3'
  api_id: '4'
  interface_hash: stale-will-be-corrected
- provider: gamma
  operation: missing
  plugin_id: '9'
  api_id: '10'
`

func TestPinBindings(t *testing.T) {
	root := t.TempDir()
	writeHeaders(t, root)
	bindPath := filepath.Join(root, "bindings.yaml")
	writeBindings(t, bindPath, bindingYAML)
	reg := loadRegistry(t, root)

	// dry-run: report only, no write.
	pinned, missing, changed, err := pinFile(bindPath, reg, true)
	if err != nil {
		t.Fatalf("pinFile dry-run: %v", err)
	}
	if pinned != 2 || changed != true {
		t.Errorf("dry-run: pinned=%d changed=%v, want 2/true (alpha + beta; gamma unpinned)", pinned, changed)
	}
	if len(missing) != 1 || missing[0] != "gamma/missing" {
		t.Errorf("dry-run missing = %v, want [gamma/missing]", missing)
	}
	if data, _ := os.ReadFile(bindPath); !strings.Contains(string(data), "stale-will-be-corrected") {
		t.Error("dry-run must not write")
	}

	// real run: pins alpha, corrects stale beta pin, leaves gamma unpinned.
	pinned, missing, changed, err = pinFile(bindPath, reg, false)
	if err != nil {
		t.Fatalf("pinFile: %v", err)
	}
	if pinned != 2 || !changed {
		t.Errorf("pinned=%d changed=%v, want 2/true (alpha + beta; gamma unpinned)", pinned, changed)
	}
	if len(missing) != 1 {
		t.Errorf("missing = %v, want [gamma/missing]", missing)
	}
	data, err := os.ReadFile(bindPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "stale-will-be-corrected") {
		t.Error("stale pin not corrected")
	}
	if !strings.Contains(text, "interface_hash: sha256:") {
		t.Error("no sha256 interface_hash written")
	}
	if strings.Count(text, "interface_hash:") != 2 {
		t.Errorf("interface_hash count = %d, want 2 (beta corrected, alpha added)", strings.Count(text, "interface_hash:"))
	}

	// idempotency: second run changes nothing.
	_, _, changed2, err := pinFile(bindPath, reg, false)
	if err != nil {
		t.Fatalf("pinFile rerun: %v", err)
	}
	if changed2 {
		t.Error("rerun changed the file; pin must be idempotent")
	}
}
