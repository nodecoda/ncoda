package capability

import "testing"

// TestConservativeSurface locks the kernel default: ref × literal only.
// The table must not depend on the target string — if it did, the kernel
// would be carrying platform knowledge again (D8).
func TestConservativeSurface(t *testing.T) {
	for _, name := range []string{"", "future-platform-9", "adapter-owned-target"} {
		tb := Conservative(name)
		if tb == nil {
			t.Fatalf("Conservative(%q) = nil", name)
		}
		if tb.Target != name {
			t.Fatalf("Conservative(%q).Target = %q", name, tb.Target)
		}
		if !tb.Condition.RefLiteral || tb.Condition.RefRef ||
			tb.TextConcat || tb.TextSplit || tb.JSONSerial || tb.JSONDeserial {
			t.Fatalf("Conservative(%q) must be the conservative surface, got %+v", name, tb)
		}
	}
}

// TestForMatchesConservative keeps the pre-rename alias honest for the one
// release the commercial adapters still link it.
func TestForMatchesConservative(t *testing.T) {
	for _, name := range []string{"", "adapter-target-a", "adapter-target-b"} {
		got, want := For(name), Conservative(name)
		if *got != *want {
			t.Fatalf("For(%q) = %+v, want %+v", name, got, want)
		}
	}
}
