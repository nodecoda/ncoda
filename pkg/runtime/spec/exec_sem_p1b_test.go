package spec

// exec_sem_p1b_test.go — ES-094: a sidecar whose semantics major is unknown
// (or whose version string is malformed) must fail loud with the runtime diag
// spelling, and that spelling must be the same string registered as 3030.

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
)

func TestExecSem094UnsupportedVersionMatchesDiag3030(t *testing.T) {
	if got := diag.RSemanticsVersionUnsupported.String(); got != "RSEMANTICS_VERSION_UNSUPPORTED" {
		t.Fatalf("diag 3030 name = %q", got)
	}
	if ErrCodeSemanticsVersionUnsupported != diag.RSemanticsVersionUnsupported.String() {
		t.Fatalf("sidecar spelling %q != diag 3030 name %q",
			ErrCodeSemanticsVersionUnsupported, diag.RSemanticsVersionUnsupported.String())
	}

	for _, tc := range []struct{ name, version string }{
		{"major", "v2"},
		{"malformed", "latest"},
		{"zero-major", "v0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			artifact := dir + "/v.spec.json"
			writeSidecar(t, artifact, `{"semantics_version":"`+tc.version+`"}`)
			_, err := DecodeJSON([]byte(p1a1BaseSpec), WithSidecar(artifact), WithWarningSink(func(string) {}))
			if err == nil {
				t.Fatalf("semantics_version %q must fail, got nil", tc.version)
			}
			if !strings.Contains(err.Error(), diag.RSemanticsVersionUnsupported.String()) {
				t.Fatalf("err = %v, want %s", err, diag.RSemanticsVersionUnsupported)
			}
		})
	}
}
