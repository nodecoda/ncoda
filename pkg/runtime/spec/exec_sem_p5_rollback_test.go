package spec

// exec_sem_p5_rollback_test.go — ES-084/ES-103b P5 rollback matrix.
//
// Cases: v1 sidecar under profile A and B, and a legacy artifact without any
// sidecar. Behavior and executable text must be identical in all three; the
// two sidecar cases must additionally carry identical provenance (a profile
// name is not a semantic input). The legacy artifact must emit the assertable
// RSEMANTICS_VERSION_UNKNOWN warning instead of silently picking a profile.
// The fail-loud half (v2/malformed) is covered by exec_sem_p1b_test.go.
//
// The sidecars below carry a real `nodes` stanza, so the loader really parses
// and validates the projection instead of accepting an empty document.

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const rollbackNodeStanza = `"nodes":{"n1":{"wire":"code","prefill":"zero","postfill":"nil",` +
	`"timeout_axis":"total","origin":"policy","evidence":"platform_model.yaml#node_kinds.code"}}`

func rollbackSidecar(profile string) string {
	return `{"semantics_version":"v1","profile":"` + profile + `",` + rollbackNodeStanza + `}`
}

// behaviorHash ignores the derived provenance/version carrier: rollback must
// reproduce behavior and executable text, while provenance is allowed to say
// "unverified" for a legacy artifact.
func behaviorHash(t *testing.T, s *Spec) [sha256.Size]byte {
	t.Helper()
	cp := *s
	cp.SemanticsVersion = ""
	cp.Provenance = nil
	b, err := cp.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return sha256.Sum256(b)
}

// fullHash covers provenance too — used between the two sidecar profiles,
// where nothing at all is allowed to move.
func fullHash(t *testing.T, s *Spec) [sha256.Size]byte {
	t.Helper()
	b, err := s.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return sha256.Sum256(b)
}

func provenanceRow(t *testing.T, s *Spec, field string) (ProvenanceRow, bool) {
	t.Helper()
	for _, r := range s.Provenance {
		if r.Field == field {
			return r, true
		}
	}
	return ProvenanceRow{}, false
}

func TestExecSemP5ES084ES103bRollbackMatrix(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "old.spec.json")

	writeSidecar(t, artifact, rollbackSidecar("coze-biz"))
	cur, warnings := decodeWithWarning(t, []byte(p1a1BaseSpec), artifact)
	if len(warnings) != 0 {
		t.Fatalf("v1/coze-biz warnings = %v", warnings)
	}
	if cur.SemanticsVersion != "v1" {
		t.Fatalf("semantics version = %q, want v1", cur.SemanticsVersion)
	}
	nodeRow, ok := provenanceRow(t, cur, "nodes.n1.semantics")
	if !ok {
		t.Fatalf("sidecar node provenance row missing: %+v", cur.Provenance)
	}
	if nodeRow.Origin != OriginPolicy || nodeRow.Evidence != "platform_model.yaml#node_kinds.code" {
		t.Fatalf("node provenance row = %+v, want the canonical projection", nodeRow)
	}
	hCur := behaviorHash(t, cur)
	hCurFull := fullHash(t, cur)

	// Roll back the profile name only: no semantic change, identical text and
	// identical provenance.
	writeSidecar(t, artifact, rollbackSidecar("legacy-2025"))
	old, warnings := decodeWithWarning(t, []byte(p1a1BaseSpec), artifact)
	if len(warnings) != 0 {
		t.Fatalf("v1/legacy warnings = %v", warnings)
	}
	if old.SemanticsVersion != "v1" {
		t.Fatalf("rollback semantics version = %q, want v1", old.SemanticsVersion)
	}
	if hOldFull := fullHash(t, old); hOldFull != hCurFull {
		t.Fatalf("profile rollback changed the decoded spec (provenance included): %s != %s", hOldFull, hCurFull)
	}
	if hOld := behaviorHash(t, old); hOld != hCur {
		t.Fatalf("profile rollback changed semantics: %s != %s", hOld, hCur)
	}

	// Legacy artifact with no sidecar: loud warning, same behavior.
	s, err := SidecarPath(artifact)
	if err != nil {
		t.Fatalf("SidecarPath: %v", err)
	}
	if err := os.Remove(s); err != nil {
		t.Fatalf("remove sidecar: %v", err)
	}
	legacy, warnings := decodeWithWarning(t, []byte(p1a1BaseSpec), artifact)
	if len(warnings) != 1 || !strings.Contains(warnings[0], ErrCodeSemanticsVersionUnknown) {
		t.Fatalf("legacy warnings = %v, want one %s", warnings, ErrCodeSemanticsVersionUnknown)
	}
	if legacy.SemanticsVersion != "v1" {
		t.Fatalf("legacy semantics version = %q, want the degraded v1", legacy.SemanticsVersion)
	}
	if len(legacy.Provenance) != 1 || legacy.Provenance[0].Origin != OriginUnverified ||
		!strings.Contains(legacy.Provenance[0].Evidence, "#missing-sidecar") {
		t.Fatalf("legacy provenance = %+v, want one unverified row", legacy.Provenance)
	}
	if _, ok := provenanceRow(t, legacy, "nodes.n1.semantics"); ok {
		t.Fatalf("legacy artifact invented a node projection: %+v", legacy.Provenance)
	}
	if hLegacy := behaviorHash(t, legacy); hLegacy != hCur {
		t.Fatalf("legacy artifact changed semantics: %s != %s", hLegacy, hCur)
	}
	// Sanity: the two profiles really were distinct inputs.
	var sc Sidecar
	if err := json.Unmarshal([]byte(rollbackSidecar("legacy-2025")), &sc); err != nil || sc.Profile != "legacy-2025" {
		t.Fatalf("fixture profile = %+v (err %v)", sc, err)
	}
}
