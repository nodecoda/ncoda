package diag

import "testing"

// TestRuntimeCodeParity — D4 gate: every runtime code is 3xxx, every const
// is registered exactly once, and no value collides with the compiler-side
// pkg/diag table (1xxx errors, 2xxx warnings, 1099 invariant).
func TestRuntimeCodeParity(t *testing.T) {
	seen := map[Code]string{}
	for c, name := range names {
		if c < 3000 || c >= 4000 {
			t.Errorf("code %d (%s) outside runtime 3xxx range", int(c), name)
		}
		if prev, ok := seen[c]; ok {
			t.Errorf("duplicate code %d (%s vs %s)", int(c), prev, name)
		}
		seen[c] = name
	}
	// compiler-side range must never be touched: 1000..1099, 2001..2010.
	for c := range names {
		if (c >= 1000 && c <= 1099) || (c >= 2000 && c <= 2010) {
			t.Errorf("code %d collides with compiler diag range", int(c))
		}
	}
	if len(seen) != len(names) {
		t.Errorf("names map has %d entries but table has %d distinct codes", len(names), len(seen))
	}
}

func TestCodeOf(t *testing.T) {
	err := New(RUnknownKind, "kind %q unknown", "x")
	if CodeOf(err) != RUnknownKind {
		t.Fatalf("CodeOf = %v, want RUnknownKind", CodeOf(err))
	}
	if CodeOf(nil) != 0 {
		t.Fatalf("CodeOf(nil) != 0")
	}
	if CodeOf(&Error{Code: RInputMissing}) != RInputMissing {
		t.Fatalf("CodeOf direct")
	}
}
