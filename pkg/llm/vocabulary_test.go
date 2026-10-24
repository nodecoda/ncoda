package llm

// vocabulary_test.go —— 词表不变量（一处定义的可证伪面）。

import "testing"

func TestVocabularyNamesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, spec := range Vocabulary() {
		if spec.Name == "" {
			t.Error("empty key name in vocabulary")
		}
		if seen[spec.Name] {
			t.Errorf("duplicate key %q in vocabulary", spec.Name)
		}
		seen[spec.Name] = true
	}
}

func TestAliasesPointAtCanonicalEntries(t *testing.T) {
	for _, spec := range Vocabulary() {
		if spec.Canonical == "" {
			continue
		}
		if spec.Canonical == spec.Name {
			t.Errorf("%q is its own alias", spec.Name)
		}
		canonical, ok := Lookup(spec.Canonical)
		if !ok {
			t.Errorf("alias %q points at unknown canonical %q", spec.Name, spec.Canonical)
			continue
		}
		if canonical.Canonical != "" {
			t.Errorf("canonical %q is itself an alias of %q", spec.Canonical, canonical.Canonical)
		}
		if canonical.Tier != spec.Tier {
			t.Errorf("alias %q tier %s != canonical %q tier %s", spec.Name, spec.Tier, spec.Canonical, canonical.Tier)
		}
	}
}

func TestNamespacedKeysAreExtensions(t *testing.T) {
	for _, key := range []string{"coze:anything", "openai:seed", "openai:reasoning_format", "x-custom"} {
		spec, ok := Lookup(key)
		if !ok {
			t.Errorf("Lookup(%q) not found", key)
			continue
		}
		if spec.Tier != TierExtension {
			t.Errorf("Lookup(%q).Tier = %s, want E", key, spec.Tier)
		}
		if spec.Type != nil {
			t.Errorf("namespaced key %q must stay open (nil type)", key)
		}
	}
	if IsNamespaced("temperature") {
		t.Error("temperature must not be namespaced")
	}
}

func TestKnownKeyTypesCoverAliasesAndCanonical(t *testing.T) {
	types := KnownKeyTypes()
	for _, key := range []string{
		"temperature", "seed", "response_format",
		"coze:generation_diversity", "generation_diversity",
		"coze:model_name", "model_name",
	} {
		if types[key] == nil {
			t.Errorf("KnownKeyTypes missing %q", key)
		}
	}
	if types["stop"] != nil {
		t.Error("stop is intentionally open (no union type); it must not be typed")
	}
	if types["coze:anything"] != nil {
		t.Error("unknown namespaced keys must stay open")
	}
}
