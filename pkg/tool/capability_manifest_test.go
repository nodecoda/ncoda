package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The first capability manifest is an identity/alias index only. It must link
// canonical T2 names to the existing registry provider/operation pair without
// copying the registry schema into a second source of truth.
func TestLoadCapabilityManifestYAML(t *testing.T) {
	data := []byte(`version: capability-manifest/v1
capabilities:
- canonical: media.extract_audio
  version: 1
  kind: effect
  providers:
  - provider: video_audio_extractor
    operation: extract
- canonical: image.generate
  version: 1
  kind: effect
  providers:
  - provider: image_generate
    operation: generate
aliases:
  coze-biz:
  - alias: coze-biz.video_audio_extractor
    canonical: media.extract_audio
    provider: video_audio_extractor
    operation: extract
`)
	manifest, err := LoadCapabilityManifestYAML(data)
	if err != nil {
		t.Fatalf("LoadCapabilityManifestYAML: %v", err)
	}
	capability, ok := manifest.LookupCanonical("media.extract_audio")
	if !ok {
		t.Fatal("LookupCanonical(media.extract_audio) not found")
	}
	if capability.Version != 1 || capability.Kind != CapabilityEffect {
		t.Fatalf("capability = %+v, want version 1 / effect", capability)
	}
	if len(capability.Providers) != 1 ||
		capability.Providers[0].Provider != "video_audio_extractor" ||
		capability.Providers[0].Operation != "extract" {
		t.Fatalf("providers = %+v, want video_audio_extractor/extract", capability.Providers)
	}
	if _, ok := manifest.LookupProvider("video_audio_extractor", "extract"); !ok {
		t.Error("LookupProvider(video_audio_extractor, extract) not found")
	}
	if _, ok := manifest.LookupProvider("video_audio_extractor", "other"); ok {
		t.Error("LookupProvider(video_audio_extractor, other) unexpectedly found")
	}
	byAlias, canonical, ok := manifest.LookupAlias("coze-biz", "coze-biz.video_audio_extractor")
	if !ok || canonical != "media.extract_audio" || byAlias == nil {
		t.Fatalf("LookupAlias = %+v/%q/%v, want capability/media.extract_audio/true", byAlias, canonical, ok)
	}
}

// Canonical identity and alias identity are closed sets. A misspelling or a
// second owner for the same identity must fail at load time, not at call time.
func TestLoadCapabilityManifestYAMLRejectsBadShape(t *testing.T) {
	valid := `version: capability-manifest/v1
capabilities:
- canonical: media.extract_audio
  version: 1
  kind: effect
  providers:
  - provider: video_audio_extractor
    operation: extract
aliases:
  coze-biz:
  - alias: coze-biz.video_audio_extractor
    canonical: media.extract_audio
    provider: video_audio_extractor
    operation: extract
`
	cases := []struct {
		name string
		data string
		want string
	}{
		{
			name: "wrong manifest version",
			data: strings.Replace(valid, "capability-manifest/v1", "capability-manifest/v2", 1),
			want: `version must be "capability-manifest/v1"`,
		},
		{
			name: "missing provider operation",
			data: strings.Replace(valid, "operation: extract", "operation: \"\"", 1),
			want: "provider and operation must be non-empty",
		},
		{
			name: "duplicate canonical",
			data: `version: capability-manifest/v1
capabilities:
- canonical: media.extract_audio
  version: 1
  kind: effect
  providers:
  - provider: video_audio_extractor
    operation: extract
- canonical: media.extract_audio
  version: 1
  kind: effect
  providers:
  - provider: other
    operation: extract
`,
			want: `duplicate capability "media.extract_audio"`,
		},
		{
			name: "duplicate provider link",
			data: `version: capability-manifest/v1
capabilities:
- canonical: media.extract_audio
  version: 1
  kind: effect
  providers:
  - provider: video_audio_extractor
    operation: extract
  - provider: video_audio_extractor
    operation: extract
`,
			want: `duplicate provider link "video_audio_extractor/extract"`,
		},
		{
			name: "alias maps to unknown canonical",
			data: strings.Replace(valid, "canonical: media.extract_audio\n    provider:", "canonical: media.missing\n    provider:", 1),
			want: `alias "coze-biz.video_audio_extractor" maps to unknown capability`,
		},
		{
			name: "duplicate alias in platform",
			data: `version: capability-manifest/v1
capabilities:
- canonical: media.extract_audio
  version: 1
  kind: effect
  providers:
  - provider: video_audio_extractor
    operation: extract
aliases:
  coze-biz:
  - alias: coze-biz.video_audio_extractor
    canonical: media.extract_audio
    provider: video_audio_extractor
    operation: extract
  - alias: coze-biz.video_audio_extractor
    canonical: media.extract_audio
    provider: video_audio_extractor
    operation: extract
`,
			want: `duplicate alias+operation "coze-biz.video_audio_extractor"/"extract"`,
		},
		{
			name: "unknown platform alias",
			data: strings.Replace(valid, "  coze-biz:\n  - alias: coze-biz.video_audio_extractor", "  hologram:\n  - alias: hologram.video_audio_extractor", 1),
			want: `unknown alias platform "hologram"`,
		},
		{
			name: "unknown kind",
			data: strings.Replace(valid, "kind: effect", "kind: magic", 1),
			want: `unknown capability kind "magic"`,
		},
		{
			name: "unknown yaml field",
			data: strings.Replace(valid, "kind: effect", "kind: effect\n  schema: {}", 1),
			want: "field schema not found",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := LoadCapabilityManifestYAML([]byte(c.data)); err == nil {
				t.Fatalf("LoadCapabilityManifestYAML succeeded, want error %q", c.want)
			} else if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want contains %q", err, c.want)
			}
		})
	}
}

// A manifest is only an index: the provider/operation link must resolve in the
// capability registry. Schema still comes from registry headers; the manifest
// must not invent a second schema.
func TestCapabilityManifestValidateAgainstRegistry(t *testing.T) {
	registry, err := NewRegistry(Definition{
		Provider:  "video_audio_extractor",
		Operation: "extract",
		Outputs:   []OutputField{{Name: "audio", Type: "file"}},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	manifest, err := LoadCapabilityManifestYAML([]byte(`version: capability-manifest/v1
capabilities:
- canonical: media.extract_audio
  version: 1
  kind: effect
  providers:
  - provider: video_audio_extractor
    operation: extract
  - provider: missing_capability
    operation: extract
aliases:
  coze-biz:
  - alias: coze-biz.missing_capability
    canonical: media.extract_audio
    provider: missing_capability
    operation: extract
`))
	if err != nil {
		t.Fatalf("LoadCapabilityManifestYAML: %v", err)
	}
	err = manifest.ValidateAgainst(registry)
	if err == nil {
		t.Fatal("ValidateAgainst succeeded, want missing registry link error")
	}
	if !strings.Contains(err.Error(), `registry missing capability "missing_capability/extract"`) {
		t.Fatalf("err = %v, want missing missing_capability/extract", err)
	}
}

// The first real T2 capability is the commercial media builtin. The manifest
// must be wired to the same registry headers used by the compiler, not to a
// separate hand-written schema.
func TestCapabilityManifestRealCorpus(t *testing.T) {
	root := filepath.Join("..", "..", "examples", "coze-reference")
	if _, err := os.Stat(root); err != nil {
		t.Skipf("corpus not present: %s", root)
	}
	data, err := os.ReadFile(filepath.Join(root, "capability-manifest.yaml"))
	if err != nil {
		t.Fatalf("read capability manifest: %v", err)
	}
	registry, err := LoadCapabilityDir(filepath.Join(root, "capabilities-ncoda"))
	if err != nil {
		t.Fatalf("LoadCapabilityDir: %v", err)
	}
	manifest, err := LoadCapabilityManifestYAML(data)
	if err != nil {
		t.Fatalf("LoadCapabilityManifestYAML: %v", err)
	}
	if err := manifest.ValidateAgainst(registry); err != nil {
		t.Fatalf("ValidateAgainst: %v", err)
	}
	// Every coze-biz P1 commercial builtin must have one canonical T2
	// identity and one long-term platform alias. This is an identity/alias
	// contract only; physical wire mapping remains owned by bindings.
	for _, tc := range []struct {
		alias     string
		canonical string
		provider  string
		operation string
	}{
		{"coze-biz.drawing_board", "design.render_canvas", "drawing_board", "render"},
		{"coze-biz.image_generate", "image.generate", "image_generate", "generate"},
		{"coze-biz.video_audio_extractor", "media.extract_audio", "video_audio_extractor", "extract"},
		{"coze-biz.video_frame_extractor", "video.extract_frames", "video_frame_extractor", "extract"},
		{"coze-biz.video_generation", "video.generate", "video_generation", "generate"},
	} {
		aliasCapability, canonical, ok := manifest.LookupAlias("coze-biz", tc.alias)
		if !ok || canonical != tc.canonical || aliasCapability == nil {
			t.Fatalf("LookupAlias(coze-biz, %s) = %+v/%q/%v, want %s", tc.alias, aliasCapability, canonical, ok, tc.canonical)
		}
		if _, ok := manifest.LookupProvider(tc.provider, tc.operation); !ok {
			t.Errorf("LookupProvider(%s, %s) not found", tc.provider, tc.operation)
		}
	}

	// Ordinary plugins can share one provider-level import across multiple
	// operations. Alias+operation resolution is the closed-set gate.
	for _, tc := range []struct {
		alias     string
		operation string
		canonical string
	}{
		{"coze-biz.dy_user_profile", "dy_get_user_profile", "social.user_profile"},
		{"coze-biz.link_extract", "extract_all_links", "text.extract_links"},
		{"coze-biz.link_extract", "extract_first_link", "text.extract_first_link"},
	} {
		link, canonical, ok := manifest.LookupAliasOperation("coze-biz", tc.alias, tc.operation)
		if !ok || canonical != tc.canonical || link.Operation != tc.operation {
			t.Fatalf("LookupAliasOperation(%s, %s) = %+v/%q/%v, want %s", tc.alias, tc.operation, link, canonical, ok, tc.canonical)
		}
	}
}

// Manifest discovery follows the same tool-root convention as binding
// discovery: callers pass tool roots or capability-header directories, and the
// optional capability-manifest.yaml sits at the root.
func TestDiscoverCapabilityManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "capability-manifest.yaml"), []byte(`version: capability-manifest/v1
capabilities:
- canonical: media.extract_audio
  version: 1
  kind: effect
  providers:
  - provider: video_audio_extractor
    operation: extract
aliases:
  coze-biz:
  - alias: coze-biz.video_audio_extractor
    canonical: media.extract_audio
    provider: video_audio_extractor
    operation: extract
`), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, found, err := DiscoverCapabilityManifest([]string{root})
	if err != nil || !found || manifest == nil {
		t.Fatalf("DiscoverCapabilityManifest = %+v/%v/%v, want manifest/true/nil", manifest, found, err)
	}
	if _, _, ok := manifest.LookupAlias("coze-biz", "coze-biz.video_audio_extractor"); !ok {
		t.Fatal("discovered manifest did not resolve coze-biz alias")
	}

	if _, found, err := DiscoverCapabilityManifest(nil); err != nil || found {
		t.Fatalf("empty search = found=%v err=%v, want false/nil", found, err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if _, found, err := DiscoverCapabilityManifest([]string{missing}); err != nil || found {
		t.Fatalf("missing search = found=%v err=%v, want false/nil", found, err)
	}
}

// A plugin import is provider-level, but one provider commonly exposes several
// operations. The identity layer must therefore index alias+operation, not
// force each operation to invent a different platform import.
func TestCapabilityManifestSupportsProviderWithMultipleOperations(t *testing.T) {
	data := []byte(`version: capability-manifest/v1
capabilities:
- canonical: text.extract_links
  version: 1
  kind: effect
  providers:
  - provider: link_extract
    operation: extract_all_links
- canonical: text.extract_first_link
  version: 1
  kind: effect
  providers:
  - provider: link_extract
    operation: extract_first_link
aliases:
  coze-biz:
  - alias: coze-biz.link_extract
    canonical: text.extract_links
    provider: link_extract
    operation: extract_all_links
  - alias: coze-biz.link_extract
    canonical: text.extract_first_link
    provider: link_extract
    operation: extract_first_link
`)
	manifest, err := LoadCapabilityManifestYAML(data)
	if err != nil {
		t.Fatalf("LoadCapabilityManifestYAML: %v", err)
	}
	for _, tc := range []struct {
		operation string
		canonical string
	}{
		{"extract_all_links", "text.extract_links"},
		{"extract_first_link", "text.extract_first_link"},
	} {
		link, canonical, ok := manifest.LookupAliasOperation("coze-biz", "coze-biz.link_extract", tc.operation)
		if !ok || canonical != tc.canonical || link.Provider != "link_extract" || link.Operation != tc.operation {
			t.Fatalf("LookupAliasOperation(%s) = %+v/%q/%v, want %s", tc.operation, link, canonical, ok, tc.canonical)
		}
		if _, ok := manifest.LookupProvider("link_extract", tc.operation); !ok {
			t.Fatalf("LookupProvider(link_extract, %s) not found", tc.operation)
		}
	}

	// Exact duplicate alias+operation remains a closed-set error.
	_, err = NewCapabilityManifest(
		"capability-manifest/v1",
		[]Capability{{
			Canonical: "text.extract_links",
			Version:   1,
			Kind:      CapabilityEffect,
			Providers: []CapabilityProviderLink{{Provider: "link_extract", Operation: "extract_all_links"}},
		}},
		map[string][]CapabilityAlias{"coze-biz": {
			{Alias: "coze-biz.link_extract", Canonical: "text.extract_links", Provider: "link_extract", Operation: "extract_all_links"},
			{Alias: "coze-biz.link_extract", Canonical: "text.extract_links", Provider: "link_extract", Operation: "extract_all_links"},
		}},
	)
	if err == nil || !strings.Contains(err.Error(), `duplicate alias+operation`) {
		t.Fatalf("err = %v, want duplicate alias+operation", err)
	}
}
