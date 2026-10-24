package spec

import (
	"sort"
	"testing"
)

// TestExecSem096KindCoverage is ES-096's total-function assertion: every one
// of the 23 Kind* constants declared in kind.go must have a decodeConfig
// switch branch in build.go. Both sides are read with go/ast; the check does
// not restate the 48-wire table or a literal kind list.
//
// A new Kind* constant without a decodeConfig case makes this test red
// instead of silently falling through to the `unknown kind` error path.
func TestExecSem096KindCoverage(t *testing.T) {
	defs := loadSpecKindDefs(t)
	if len(defs) != 23 {
		t.Fatalf("kind.go declares %d Kind* constants, want 23", len(defs))
	}
	cases := loadDecodeConfigCaseNames(t)

	want := map[string]string{} // const name → value
	for _, d := range defs {
		want[d.name] = d.value
	}

	var missing, extra []string
	for _, d := range defs {
		if !cases[d.name] {
			missing = append(missing, d.name+" ("+d.value+")")
		}
	}
	for name := range cases {
		if _, ok := want[name]; !ok {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	if len(missing) > 0 {
		t.Fatalf("decodeConfig is missing branches for %d/%d spec kinds: %v", len(missing), len(defs), missing)
	}
	if len(extra) > 0 {
		t.Fatalf("decodeConfig has branches for unknown Kind identifiers (not in kind.go): %v", extra)
	}
	if len(cases) != len(defs) {
		t.Fatalf("decodeConfig covers %d kind identifiers, want exactly %d", len(cases), len(defs))
	}

	// The production decoder must actually accept each kind (not just name a
	// case expression). nil raw config is the branch-only probe.
	for _, d := range defs {
		cfg, err := DecodeConfig(d.value, nil)
		if err != nil || cfg == nil {
			t.Errorf("DecodeConfig(%q, nil) = (%T, %v), want a config and no error", d.value, cfg, err)
		}
	}
}
