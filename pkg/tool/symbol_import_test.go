package tool

// P3.5 symbol import: LoadCapabilityPackages resolves import declarations
// against search paths (the ncoda-internal equivalent of #include with -I).
// Only the imported packages enter the registry; a missing package reports
// the searched paths so the user can fix --tool-path.

import (
	"strings"
	"testing"
)

// writeCapability writes one capability header (provider == file stem).
func writeCapability(t *testing.T, dir, provider, body string) {
	t.Helper()
	writeHeader(t, dir, provider+CapabilityHeaderExt, body)
}

const simpleHeader = `@language nodecoda/1
type TtsResult = { string url; };
function speech_synthesis(string text, string language = "zh") -> TtsResult {}
`

func TestLoadCapabilityPackagesResolvesImports(t *testing.T) {
	dir := t.TempDir()
	writeCapability(t, dir, "tts", simpleHeader)
	registry, err := LoadCapabilityPackages([]string{dir}, []string{"tts"})
	if err != nil {
		t.Fatalf("LoadCapabilityPackages: %v", err)
	}
	def, found := registry.Lookup("tts", "speech_synthesis")
	if !found {
		t.Fatal("tts/speech_synthesis not loaded")
	}
	if def.Provider != "tts" || len(def.Parameters) != 2 {
		t.Fatalf("definition = %#v, want tts + 2 params", def)
	}
}

func TestLoadCapabilityPackagesRespectsSearchPathOrder(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	writeCapability(t, first, "dup", simpleHeader)
	writeCapability(t, second, "dup", strings.Replace(simpleHeader, "speech_synthesis", "other_op", 1))
	registry, err := LoadCapabilityPackages([]string{first, second}, []string{"dup"})
	if err != nil {
		t.Fatalf("LoadCapabilityPackages: %v", err)
	}
	if _, found := registry.Lookup("dup", "speech_synthesis"); !found {
		t.Fatal("first search path should win (dup/speech_synthesis)")
	}
	if _, found := registry.Lookup("dup", "other_op"); found {
		t.Fatal("second search path must not shadow the first")
	}
}

func TestLoadCapabilityPackagesUnresolvedReportsSearchedPaths(t *testing.T) {
	dir := t.TempDir()
	writeCapability(t, dir, "tts", simpleHeader)
	_, err := LoadCapabilityPackages([]string{dir, "/nonexistent"}, []string{"tts", "missing"})
	if err == nil {
		t.Fatal("expected unresolved import error")
	}
	if !strings.Contains(err.Error(), `unresolved import(s) "missing"`) {
		t.Fatalf("error = %q, want unresolved \"missing\"", err)
	}
	if !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "/nonexistent") {
		t.Fatalf("error = %q, want searched paths listed", err)
	}
}

func TestLoadCapabilityPackagesEmptyImports(t *testing.T) {
	registry, err := LoadCapabilityPackages([]string{t.TempDir()}, nil)
	if err != nil {
		t.Fatalf("LoadCapabilityPackages(nil imports): %v", err)
	}
	if len(registry.Definitions()) != 0 {
		t.Fatalf("definitions = %d, want 0", len(registry.Definitions()))
	}
}

func TestLoadCapabilityPackagesDuplicateImportsDeduplicated(t *testing.T) {
	dir := t.TempDir()
	writeCapability(t, dir, "tts", simpleHeader)
	registry, err := LoadCapabilityPackages([]string{dir}, []string{"tts", "tts"})
	if err != nil {
		t.Fatalf("LoadCapabilityPackages(dup imports): %v", err)
	}
	if len(registry.Definitions()) != 1 {
		t.Fatalf("definitions = %d, want 1 (deduplicated)", len(registry.Definitions()))
	}
}

func TestLoadCapabilityPackagesMissingFileSkipsPath(t *testing.T) {
	empty := t.TempDir()
	real := t.TempDir()
	writeCapability(t, real, "tts", simpleHeader)
	// empty dir has no tts.ncoda; the search must fall through to real.
	registry, err := LoadCapabilityPackages([]string{empty, real}, []string{"tts"})
	if err != nil {
		t.Fatalf("LoadCapabilityPackages: %v", err)
	}
	if _, found := registry.Lookup("tts", "speech_synthesis"); !found {
		t.Fatal("fall-through across empty search path failed")
	}
}
