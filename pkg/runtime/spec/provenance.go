package spec

// provenance.go — `spec.Provenance` row carrier (PRD §4.3 / §4.4).
//
// A provenance row says *where* a carried execution fact came from and *which
// pointer* proves it. It is a derived projection: the definition of platform
// policy stays in `docs/five-step/platform_model.yaml#node_kinds` (or the
// artifact itself for wire facts), never in this package.
//
// The semantic origin set is exactly PRD §4.3's four values. OQ5 additionally
// requires the missing-sidecar degradation to record `origin=unverified`
// (an explicit "we could not verify" marker, not a fifth semantic origin).
// `OriginUnverified` is therefore accepted by validation but is deliberately
// excluded from `SemanticOrigins`.

import (
	"fmt"
	"sort"
	"strings"
)

// ProvenanceOrigin is a closed-set origin spelling.
type ProvenanceOrigin string

const (
	// OriginWirePresent: the artifact/IR contains the value (author or tool).
	OriginWirePresent ProvenanceOrigin = "wire-present"
	// OriginWireAbsent: the artifact/IR omits the value.
	OriginWireAbsent ProvenanceOrigin = "wire-absent"
	// OriginPolicy: the value comes from the profile / node-kind policy.
	OriginPolicy ProvenanceOrigin = "policy"
	// OriginHarness: the value comes from an echo/stub/fixture harness.
	OriginHarness ProvenanceOrigin = "harness"
	// OriginUnverified is OQ5's explicit unknown marker for a missing sidecar.
	// It is not a semantic origin (see SemanticOrigins).
	OriginUnverified ProvenanceOrigin = "unverified"
)

// SemanticOrigins is PRD §4.3's closed four-value origin set.
func SemanticOrigins() []ProvenanceOrigin {
	return []ProvenanceOrigin{OriginWirePresent, OriginWireAbsent, OriginPolicy, OriginHarness}
}

// IsSemanticOrigin reports whether o is one of PRD §4.3's four origins.
func IsSemanticOrigin(o ProvenanceOrigin) bool {
	switch o {
	case OriginWirePresent, OriginWireAbsent, OriginPolicy, OriginHarness:
		return true
	}
	return false
}

// IsProvenanceOrigin reports whether o is accepted by a provenance row.
func IsProvenanceOrigin(o ProvenanceOrigin) bool {
	return IsSemanticOrigin(o) || o == OriginUnverified
}

// ProvenanceRow is one (node, field) → origin/evidence record.
//
// Join key = node_id + json path (PRD §4.4). `Node` is empty for workflow-level
// facts (e.g. `semantics_version`). `Evidence` must be a resolvable pointer
// (sidecar:/observed:/hand:/platform_model.yaml#/…).
type ProvenanceRow struct {
	Node     string           `json:"node,omitempty"`
	Field    string           `json:"field"`
	Origin   ProvenanceOrigin `json:"origin"`
	Evidence string           `json:"evidence"`
}

// Key is the merge identity (node + field).
func (r ProvenanceRow) Key() string {
	return r.Node + "\x00" + r.Field
}

// Validate checks the closed origin set and the evidence requirement.
func (r ProvenanceRow) Validate() error {
	if strings.TrimSpace(r.Field) == "" {
		return fmt.Errorf("provenance: empty field")
	}
	if !IsProvenanceOrigin(r.Origin) {
		return fmt.Errorf("provenance: field %q: invalid origin %q (want wire-present|wire-absent|policy|harness|unverified)", r.Field, r.Origin)
	}
	if strings.TrimSpace(r.Evidence) == "" {
		return fmt.Errorf("provenance: field %q: empty evidence pointer", r.Field)
	}
	return nil
}

// ValidateProvenance validates every row.
func ValidateProvenance(rows []ProvenanceRow) error {
	for i, r := range rows {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("row %d: %w", i, err)
		}
	}
	return nil
}

// CloneProvenance returns a defensive copy.
func CloneProvenance(rows []ProvenanceRow) []ProvenanceRow {
	if len(rows) == 0 {
		return nil
	}
	return append([]ProvenanceRow(nil), rows...)
}

// MergeProvenance validates and merges row groups. Later groups win on a
// duplicate (node, field) key, and the result is sorted by node then field so
// the canonical bytes are deterministic.
func MergeProvenance(groups ...[]ProvenanceRow) ([]ProvenanceRow, error) {
	byKey := map[string]ProvenanceRow{}
	order := make([]string, 0)
	for _, g := range groups {
		if err := ValidateProvenance(g); err != nil {
			return nil, err
		}
		for _, r := range g {
			k := r.Key()
			if _, seen := byKey[k]; !seen {
				order = append(order, k)
			}
			byKey[k] = r
		}
	}
	out := make([]ProvenanceRow, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Node != out[j].Node {
			return out[i].Node < out[j].Node
		}
		return out[i].Field < out[j].Field
	})
	return out, nil
}
