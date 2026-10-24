package diag

import "testing"

// diag_3030_test.go — ES-094 registration gate for PRD §4.7.
//
// The code must exist in BOTH the const table and `names`: removing the names
// entry makes String() fall back to RUNTIME_CODE_3030 and turns this test red
// (negative control for the closed-set registration).
func TestSemanticsVersionUnsupportedRegistered(t *testing.T) {
	if RSemanticsVersionUnsupported != 3030 {
		t.Fatalf("RSemanticsVersionUnsupported = %d, want 3030", int(RSemanticsVersionUnsupported))
	}
	if got := RSemanticsVersionUnsupported.String(); got != "RSEMANTICS_VERSION_UNSUPPORTED" {
		t.Fatalf("String() = %q, want RSEMANTICS_VERSION_UNSUPPORTED", got)
	}
	if !RSemanticsVersionUnsupported.IsError() {
		t.Fatal("3030 must be an error-severity runtime code")
	}
	if _, ok := names[RSemanticsVersionUnsupported]; !ok {
		t.Fatal("3030 missing from names map (closed-set registration incomplete)")
	}
}
