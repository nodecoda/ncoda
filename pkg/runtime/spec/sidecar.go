package spec

// sidecar.go — artifact sidecar naming, decoding and reconciliation
// (OQ5 decision, 2026-10-22).
//
// The sidecar is the per-node projection of platform policy emitted by the
// decompiler. It is NOT a second definition: every stanza must equal its
// `platform_model.yaml#node_kinds` home unless it carries an explicit
// origin/evidence override.
//
// Naming (OQ5 §1): sidecar(P) = dirname(P) / (basename(P) minus one final
// extension) + ".semantics.json". The loader only reads; the decompiler is
// the sole writer.
//
// Degradation table (OQ5 §4):
//   - sidecar missing + spec undeclared → v1 + loud warning + origin=unverified
//   - sidecar missing + spec declared  → error (SEMANTICS_VERSION_INCONSISTENT)
//   - sidecar present, version missing → error (SEMANTICS_VERSION_MISSING)
//   - sidecar present, major > 1 or malformed → error (RSEMANTICS_VERSION_UNSUPPORTED)
//   - sidecar present, v1 consistent → accept
//
// DecodeJSON is the only production entry and calls the loader through
// `WithSidecar`; tests must not become the sole consumer.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
)

const (
	// ErrCodeSemanticsVersionMissing: sidecar exists but has no version.
	ErrCodeSemanticsVersionMissing = "SEMANTICS_VERSION_MISSING"
	// ErrCodeSemanticsVersionInconsistent: sidecar/spec disagree (or a sidecar
	// was expected but is absent while the spec declared a version).
	ErrCodeSemanticsVersionInconsistent = "SEMANTICS_VERSION_INCONSISTENT"
	// ErrCodeSemanticsVersionUnknown: loud warning for legacy artifacts.
	ErrCodeSemanticsVersionUnknown = "RSEMANTICS_VERSION_UNKNOWN"
	// ErrCodeSidecarNameInvalid: sidecar naming rule violation.
	ErrCodeSidecarNameInvalid = "SIDECAR_NAME_INVALID"
	// ErrCodeSemanticsSidecarInvalid: unreadable/malformed sidecar.
	ErrCodeSemanticsSidecarInvalid = "SEMANTICS_SIDECAR_INVALID"
)

// ErrCodeSemanticsVersionUnsupported is the PRD §4.7 diagnostic spelling for
// a sidecar whose semantics major is unknown or malformed. The runtime diag
// table owns the single definition (code 3030); this alias keeps the caller-
// facing spelling derived from it so text and code cannot drift.
var ErrCodeSemanticsVersionUnsupported = diag.RSemanticsVersionUnsupported.String()

var semanticsVersionPattern = regexp.MustCompile(`^v[1-9][0-9]*$`)

// Sidecar is the on-disk artifact sidecar shape (OQ5 §6).
type Sidecar struct {
	SemanticsVersion string                 `json:"semantics_version"`
	Profile          string                 `json:"profile,omitempty"`
	Artifact         string                 `json:"artifact,omitempty"`
	Nodes            map[string]SidecarNode `json:"nodes,omitempty"`
}

// SidecarNode is one per-node policy projection.
type SidecarNode struct {
	Wire            string `json:"wire,omitempty"`
	Prefill         string `json:"prefill,omitempty"`
	Postfill        string `json:"postfill,omitempty"`
	HostFields      string `json:"host_fields,omitempty"`
	TimeoutAxis     string `json:"timeout_axis,omitempty"`
	RequiredDefault *bool  `json:"required_default,omitempty"`
	Origin          string `json:"origin,omitempty"`
	Evidence        string `json:"evidence,omitempty"`
}

// DecodeOption customizes DecodeJSON without breaking existing callers.
type DecodeOption func(*decodeOptions) error

type decodeOptions struct {
	artifactPath string
	sidecarPath  string
	warn         func(string)
}

// WithSidecar makes DecodeJSON load the sidecar derived from artifactPath
// (OQ5 naming, same directory). artifactPath is the artifact, not the
// sidecar; passing an already-sidecar name is rejected.
func WithSidecar(artifactPath string) DecodeOption {
	return func(o *decodeOptions) error {
		p, err := SidecarPath(artifactPath)
		if err != nil {
			return err
		}
		o.artifactPath = artifactPath
		o.sidecarPath = p
		return nil
	}
}

// WithWarningSink overrides the warning destination. The default writes to
// stderr so legacy artifacts are loud in production (OQ5 requires an
// assertable `RSEMANTICS_VERSION_UNKNOWN` warning at rc=0).
func WithWarningSink(fn func(string)) DecodeOption {
	return func(o *decodeOptions) error {
		o.warn = fn
		return nil
	}
}

func (o *decodeOptions) warnf(format string, args ...any) {
	if o == nil || o.warn == nil {
		return
	}
	o.warn(fmt.Sprintf(format, args...))
}

// SidecarPath derives the sidecar path from an artifact path (OQ5 §1).
func SidecarPath(artifactPath string) (string, error) {
	if strings.TrimSpace(artifactPath) == "" {
		return "", fmt.Errorf("%s: empty artifact path", ErrCodeSidecarNameInvalid)
	}
	base := filepath.Base(artifactPath)
	if strings.HasSuffix(base, ".semantics.json") {
		return "", fmt.Errorf("%s: %q is already a sidecar name; derive from the artifact", ErrCodeSidecarNameInvalid, artifactPath)
	}
	if ext := filepath.Ext(base); ext != "" && !(strings.HasPrefix(base, ".") && ext == base) {
		base = strings.TrimSuffix(base, ext)
	}
	return filepath.Join(filepath.Dir(artifactPath), base+".semantics.json"), nil
}

func validateSemanticsVersion(v string) error {
	if !semanticsVersionPattern.MatchString(v) {
		return fmt.Errorf("%s: invalid semantics_version %q (want ^v[1-9][0-9]*$)", diag.RSemanticsVersionUnsupported, v)
	}
	major, err := strconv.Atoi(strings.TrimPrefix(v, "v"))
	if err != nil || major > 1 {
		return fmt.Errorf("%s: semantics_version %q has unsupported major (supported: v1)", diag.RSemanticsVersionUnsupported, v)
	}
	return nil
}

func sidecarEnum(field, value string, allowed ...string) error {
	if value == "" {
		return nil
	}
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return fmt.Errorf("%s: node field %s=%q (want one of %s)", ErrCodeSemanticsSidecarInvalid, field, value, strings.Join(allowed, "|"))
}

func validateSidecarNodes(sc *Sidecar) error {
	ids := make([]string, 0, len(sc.Nodes))
	for id := range sc.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		st := sc.Nodes[id]
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("%s: empty node id", ErrCodeSemanticsSidecarInvalid)
		}
		if err := sidecarEnum("prefill", st.Prefill, "zero", "nil", "none"); err != nil {
			return fmt.Errorf("node %s: %w", id, err)
		}
		if err := sidecarEnum("postfill", st.Postfill, "nil", "none"); err != nil {
			return fmt.Errorf("node %s: %w", id, err)
		}
		if err := sidecarEnum("host_fields", st.HostFields, "carry"); err != nil {
			return fmt.Errorf("node %s: %w", id, err)
		}
		if err := sidecarEnum("timeout_axis", st.TimeoutAxis, "per_attempt", "total", "none"); err != nil {
			return fmt.Errorf("node %s: %w", id, err)
		}
		if st.Origin != "" {
			if err := sidecarEnum("origin", st.Origin, "declared", "policy", "unverified"); err != nil {
				return fmt.Errorf("node %s: %w", id, err)
			}
			if strings.TrimSpace(st.Evidence) == "" {
				return fmt.Errorf("%s: node %s: origin set without evidence", ErrCodeSemanticsSidecarInvalid, id)
			}
		} else if st.Evidence != "" {
			return fmt.Errorf("%s: node %s: evidence set without origin", ErrCodeSemanticsSidecarInvalid, id)
		}
	}
	return nil
}

// provenanceOriginFromSidecar maps sidecar stanza origins onto spec
// provenance origins. `declared` means the value is present on the wire
// (PRD §4.3's wire-present); `unverified` is OQ5's explicit unknown marker.
func provenanceOriginFromSidecar(o string) (ProvenanceOrigin, bool) {
	switch o {
	case "declared":
		return OriginWirePresent, true
	case "policy":
		return OriginPolicy, true
	case "unverified":
		return OriginUnverified, true
	}
	return "", false
}

func sidecarProvenance(sc *Sidecar, sidecarPath string) []ProvenanceRow {
	rows := []ProvenanceRow{{
		Field:    "semantics_version",
		Origin:   OriginWirePresent,
		Evidence: "sidecar:" + filepath.Base(sidecarPath) + "#semantics_version",
	}}
	ids := make([]string, 0, len(sc.Nodes))
	for id := range sc.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		st := sc.Nodes[id]
		if st.Origin == "" {
			continue
		}
		origin, ok := provenanceOriginFromSidecar(st.Origin)
		if !ok {
			continue // validated earlier; unreachable
		}
		rows = append(rows, ProvenanceRow{
			Node:     id,
			Field:    "nodes." + id + ".semantics",
			Origin:   origin,
			Evidence: st.Evidence,
		})
	}
	return rows
}

// loadSidecar applies the OQ5 decision table. It returns the rows contributed
// by the sidecar plus the effective semantics version. A nil sidecar with a
// non-empty version means degradation (missing file, v1 assumed).
func loadSidecar(o *decodeOptions, declared string) (*Sidecar, []ProvenanceRow, string, error) {
	if o == nil || o.sidecarPath == "" {
		return nil, nil, "", nil
	}
	raw, err := os.ReadFile(o.sidecarPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if declared != "" {
				return nil, nil, "", fmt.Errorf("%s: artifact %s declares semantics_version %q but sidecar %s is missing",
					ErrCodeSemanticsVersionInconsistent, o.artifactPath, declared, o.sidecarPath)
			}
			o.warnf("%s: %s: sidecar missing; assuming semantics v1", ErrCodeSemanticsVersionUnknown, o.artifactPath)
			return nil, []ProvenanceRow{{
				Field:    "semantics_version",
				Origin:   OriginUnverified,
				Evidence: "observed:" + o.artifactPath + "#missing-sidecar",
			}}, "v1", nil
		}
		return nil, nil, "", fmt.Errorf("%s: read %s: %w", ErrCodeSemanticsSidecarInvalid, o.sidecarPath, err)
	}
	var sc Sidecar
	if err := json.Unmarshal(raw, &sc); err != nil {
		return nil, nil, "", fmt.Errorf("%s: decode %s: %w", ErrCodeSemanticsSidecarInvalid, o.sidecarPath, err)
	}
	if strings.TrimSpace(sc.SemanticsVersion) == "" {
		return nil, nil, "", fmt.Errorf("%s: %s: sidecar exists but semantics_version is absent", ErrCodeSemanticsVersionMissing, o.sidecarPath)
	}
	if err := validateSemanticsVersion(sc.SemanticsVersion); err != nil {
		return nil, nil, "", fmt.Errorf("%s: %w", o.sidecarPath, err)
	}
	if declared != "" && declared != sc.SemanticsVersion {
		return nil, nil, "", fmt.Errorf("%s: spec declares semantics_version %q but sidecar %s declares %q",
			ErrCodeSemanticsVersionInconsistent, declared, o.sidecarPath, sc.SemanticsVersion)
	}
	if err := validateSidecarNodes(&sc); err != nil {
		return &sc, nil, "", err
	}
	return &sc, sidecarProvenance(&sc, o.sidecarPath), sc.SemanticsVersion, nil
}
