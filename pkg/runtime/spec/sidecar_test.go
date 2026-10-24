package spec

// sidecar_test.go — OQ5 naming + loader decision table, exercised through the
// production loader `DecodeJSON` (never through a test-only parser). ES-103a
// requires the consumer to be the production path: removing the loadSidecar
// call from DecodeJSON must turn these tests red.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const p1a1BaseSpec = `{"version":"v2","nodes":[{"id":"n1","kind":"code","config":{"code":"def main() -> dict:\n    return {\"result\": 1}","language":"python","synthetic":true}}],"edges":[]}`

func writeSidecar(t *testing.T, artifact string, body string) string {
	t.Helper()
	p, err := SidecarPath(artifact)
	if err != nil {
		t.Fatalf("SidecarPath: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	return p
}

func decodeWithWarning(t *testing.T, raw []byte, artifact string) (*Spec, []string) {
	t.Helper()
	var warnings []string
	s, err := DecodeJSON(raw, WithSidecar(artifact), WithWarningSink(func(msg string) {
		warnings = append(warnings, msg)
	}))
	if err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	return s, warnings
}

func TestSidecarPathNaming(t *testing.T) {
	cases := []struct{ artifact, want string }{
		{"/out/workflow.spec.json", "/out/workflow.spec.semantics.json"},
		{"/out/foo.json", "/out/foo.semantics.json"},
		{"/out/foo", "/out/foo.semantics.json"},
		{"/out/.foo", "/out/.foo.semantics.json"},
	}
	for _, tc := range cases {
		got, err := SidecarPath(tc.artifact)
		if err != nil {
			t.Fatalf("SidecarPath(%q): %v", tc.artifact, err)
		}
		if got != tc.want {
			t.Fatalf("SidecarPath(%q) = %q, want %q", tc.artifact, got, tc.want)
		}
	}
	if _, err := SidecarPath("/out/foo.semantics.json"); err == nil || !strings.Contains(err.Error(), ErrCodeSidecarNameInvalid) {
		t.Fatalf("already-sidecar name must be rejected with %s, got %v", ErrCodeSidecarNameInvalid, err)
	}
}

// TestP1a1ES103aDecodeJSONLoadsSidecar is the ES-103a production-path
// assertion: DecodeJSON (not the test) reads the sidecar, carries the version
// and projects provenance.
func TestP1a1ES103aDecodeJSONLoadsSidecar(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "workflow.spec.json")
	sidecar := writeSidecar(t, artifact, `{
  "semantics_version": "v1",
  "profile": "coze-biz",
  "artifact": "workflow.spec.json",
  "nodes": {
    "n1": {"wire":"code","prefill":"zero","postfill":"nil","host_fields":"carry","timeout_axis":"total","required_default":false,"origin":"policy","evidence":"platform_model.yaml#node_kinds.code"}
  },
  "unsupported": ["n2"]
}`)

	s, warnings := decodeWithWarning(t, []byte(p1a1BaseSpec), artifact)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warning for present sidecar: %v", warnings)
	}
	if s.SemanticsVersion != "v1" {
		t.Fatalf("SemanticsVersion = %q, want v1 (sidecar %s)", s.SemanticsVersion, sidecar)
	}
	var versionRow, nodeRow *ProvenanceRow
	for i := range s.Provenance {
		switch s.Provenance[i].Field {
		case "semantics_version":
			versionRow = &s.Provenance[i]
		case "nodes.n1.semantics":
			nodeRow = &s.Provenance[i]
		}
	}
	if versionRow == nil {
		t.Fatalf("semantics_version provenance row missing: %+v", s.Provenance)
	}
	if versionRow.Origin != OriginWirePresent || !strings.HasPrefix(versionRow.Evidence, "sidecar:") {
		t.Fatalf("semantics_version row = %+v, want origin=%s evidence=sidecar:…", versionRow, OriginWirePresent)
	}
	if nodeRow == nil || nodeRow.Origin != OriginPolicy {
		t.Fatalf("node policy provenance row = %+v, want origin=policy", nodeRow)
	}

	// Roundtrip must keep the loader-populated version/provenance.
	norm, err := s.Normalize()
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if norm.SemanticsVersion != "v1" || len(norm.Provenance) != len(s.Provenance) {
		t.Fatalf("Normalize dropped loader facts: %+v", norm)
	}
}

func TestP1a1SidecarMissingUndeclaredWarnsAndAssumesV1(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "legacy.spec.json")
	s, warnings := decodeWithWarning(t, []byte(p1a1BaseSpec), artifact)

	if s.SemanticsVersion != "v1" {
		t.Fatalf("SemanticsVersion = %q, want v1 degradation", s.SemanticsVersion)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], ErrCodeSemanticsVersionUnknown) {
		t.Fatalf("warnings = %v, want one %s warning", warnings, ErrCodeSemanticsVersionUnknown)
	}
	row := p1a1ProvenanceRow(t, s, "semantics_version")
	if row == nil || row.Origin != OriginUnverified {
		t.Fatalf("missing-sidecar provenance = %+v, want origin=%s", row, OriginUnverified)
	}
	if !strings.Contains(row.Evidence, "#missing-sidecar") {
		t.Fatalf("missing-sidecar evidence = %q", row.Evidence)
	}
}

func TestP1a1SidecarMissingDeclaredErrors(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "declared.spec.json")
	raw := []byte(`{"version":"v2","semantics_version":"v1","nodes":[],"edges":[]}`)
	_, err := DecodeJSON(raw, WithSidecar(artifact), WithWarningSink(func(string) {}))
	if err == nil || !strings.Contains(err.Error(), ErrCodeSemanticsVersionInconsistent) {
		t.Fatalf("missing+declared error = %v, want %s", err, ErrCodeSemanticsVersionInconsistent)
	}
}

func TestP1a1SidecarMissingVersionErrors(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "nv.spec.json")
	writeSidecar(t, artifact, `{"profile":"coze-biz"}`)
	_, err := DecodeJSON([]byte(p1a1BaseSpec), WithSidecar(artifact), WithWarningSink(func(string) {}))
	if err == nil || !strings.Contains(err.Error(), ErrCodeSemanticsVersionMissing) {
		t.Fatalf("missing version error = %v, want %s", err, ErrCodeSemanticsVersionMissing)
	}
}

// TestP1a1SidecarUnsupportedMajor is the negative control for major=2: an
// implementation that removed the major check would accept this sidecar and
// turn the test red.
func TestP1a1SidecarUnsupportedMajor(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "v2.spec.json")
	writeSidecar(t, artifact, `{"semantics_version":"v2"}`)
	_, err := DecodeJSON([]byte(p1a1BaseSpec), WithSidecar(artifact), WithWarningSink(func(string) {}))
	if err == nil || !strings.Contains(err.Error(), ErrCodeSemanticsVersionUnsupported) {
		t.Fatalf("major=2 error = %v, want %s", err, ErrCodeSemanticsVersionUnsupported)
	}
}

func TestP1a1SidecarMalformedVersionErrors(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "bad.spec.json")
	writeSidecar(t, artifact, `{"semantics_version":"latest"}`)
	_, err := DecodeJSON([]byte(p1a1BaseSpec), WithSidecar(artifact), WithWarningSink(func(string) {}))
	if err == nil || !strings.Contains(err.Error(), ErrCodeSemanticsVersionUnsupported) {
		t.Fatalf("malformed version error = %v, want %s", err, ErrCodeSemanticsVersionUnsupported)
	}
}

func TestP1a1SidecarSpecVersionMismatchErrors(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "mismatch.spec.json")
	writeSidecar(t, artifact, `{"semantics_version":"v1"}`)
	raw := []byte(`{"version":"v2","semantics_version":"v2","nodes":[],"edges":[]}`)
	_, err := DecodeJSON(raw, WithSidecar(artifact), WithWarningSink(func(string) {}))
	if err == nil || !strings.Contains(err.Error(), ErrCodeSemanticsVersionInconsistent) {
		t.Fatalf("mismatch error = %v, want %s", err, ErrCodeSemanticsVersionInconsistent)
	}
}

func TestP1a1SidecarInvalidNodeStanzaErrors(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "badnode.spec.json")
	writeSidecar(t, artifact, `{"semantics_version":"v1","nodes":{"n1":{"prefill":"bogus"}}}`)
	_, err := DecodeJSON([]byte(p1a1BaseSpec), WithSidecar(artifact), WithWarningSink(func(string) {}))
	if err == nil || !strings.Contains(err.Error(), ErrCodeSemanticsSidecarInvalid) {
		t.Fatalf("invalid stanza error = %v, want %s", err, ErrCodeSemanticsSidecarInvalid)
	}
}
