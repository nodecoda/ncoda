package llm

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

type fixtureManifest struct {
	Version  int    `yaml:"version"`
	Name     string `yaml:"name"`
	Runner   string `yaml:"runner"`
	Fixtures []struct {
		ID       string   `yaml:"id"`
		Source   string   `yaml:"source"`
		Schema   string   `yaml:"schema"`
		Expected string   `yaml:"expected"`
		Profiles []string `yaml:"profiles"`
	} `yaml:"fixtures"`
}

func TestUnifiedLLMFixtureManifest(t *testing.T) {
	path := filepath.Join("..", "..", "tests", "fixtures", "llm", "manifest.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m fixtureManifest
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatalf("manifest YAML: %v", err)
	}
	if m.Version != 1 || m.Name != "unified-llm-semantics" || m.Runner != "credential-free" {
		t.Fatalf("manifest header = %#v", m)
	}
	want := []string{"string-text", "scalar-int", "record-member", "record-whole", "nested-array-record", "map-optional-enum", "metadata-envelope", "retry-decode-failure", "coze-biz-batch-inner", "open-object", "open-object-array"}
	if len(m.Fixtures) != len(want) {
		t.Fatalf("fixture count = %d, want %d", len(m.Fixtures), len(want))
	}
	seen := map[string]bool{}
	for i, f := range m.Fixtures {
		if f.ID != want[i] {
			t.Errorf("fixture %d id = %q, want %q", i, f.ID, want[i])
		}
		if seen[f.ID] {
			t.Errorf("duplicate fixture id %q", f.ID)
		}
		seen[f.ID] = true
		if f.Source == "" || f.Schema == "" || f.Expected == "" || len(f.Profiles) == 0 {
			t.Errorf("fixture %q is incomplete: %#v", f.ID, f)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(path), f.Source)); err != nil {
			t.Errorf("fixture %q source: %v", f.ID, err)
		}
	}
}
