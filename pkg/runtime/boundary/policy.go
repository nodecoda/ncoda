// Package boundary implements the execution-boundary value model.
//
// ValueBoundary is a pure function pipeline:
//
//	resolve → coerce → fill(required check inside) → [invoke] → postfill → publish
//
// The platform policy (prefill/postfill/required) is a per-node projection of
// docs/five-step/platform_model.yaml#node_kinds; it is injected so the same
// production constructor can be driven by a variant policy in negative
// controls (ES-071/ES-073/ES-074/ES-090).
package boundary

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// FillMode is the prefill policy for declared values that are absent or null.
type FillMode string

const (
	// PreFillZero fills scalars with their language zero and publishes
	// container zero as null (PROJECT-CONTRACT.md:423).
	PreFillZero FillMode = "zero"
	// PreFillNil publishes null for both absent and null values.
	PreFillNil FillMode = "nil"
	// PreFillNone leaves absent/null inputs untouched.
	PreFillNone FillMode = "none"
)

// PostFillMode is the output-port materialisation policy.
type PostFillMode string

const (
	// PostFillNil materialises every declared output port that is missing or
	// null as an explicit null value.
	PostFillNil PostFillMode = "nil"
	// PostFillNone leaves the output map as the node returned it.
	PostFillNone PostFillMode = "none"
)

const (
	// DefaultEvidence is the resolvable pointer for the code prefill policy.
	DefaultEvidence = "platform_model.yaml#node_kinds.code.prefill"
	// PostFillEvidence is the resolvable pointer for the code postfill policy.
	PostFillEvidence = "platform_model.yaml#node_kinds.code.postfill"
	// EnvPolicyPath selects an injected policy file at build time.
	EnvPolicyPath = "NCODA_EXEC_POLICY"
)

// Policy is the per-node execution-boundary policy.
//
// MergeAbsentNull is a negative-control knob: it makes absent and null take the
// same trace rule so ES-001 (tri-state distinction) goes red. It is parsed from
// an injected fixture only; the production default is false.
type Policy struct {
	PreFill         FillMode
	PostFill        PostFillMode
	EnforceRequired bool
	MergeAbsentNull bool
	Evidence        string
}

// DefaultPolicy is the coze-biz code-node projection: prefill zero, postfill
// nil, required left unverified (EnforceRequired=false).
func DefaultPolicy() Policy {
	return Policy{
		PreFill:  PreFillZero,
		PostFill: PostFillNil,
		Evidence: DefaultEvidence,
	}
}

func (p Policy) normalized() Policy {
	if p.PreFill == "" {
		p.PreFill = PreFillZero
	}
	if p.PostFill == "" {
		p.PostFill = PostFillNil
	}
	if p.Evidence == "" {
		p.Evidence = DefaultEvidence
	}
	return p
}

// ProductionPolicy is the policy consumed by the production code node. An
// empty NCODA_EXEC_POLICY means the built-in coze-biz projection; a set path is
// loaded and any failure is fail-loud (no silent fallback).
func ProductionPolicy() (Policy, error) {
	path := os.Getenv(EnvPolicyPath)
	if path == "" {
		return DefaultPolicy(), nil
	}
	return LoadPolicy(path)
}

type policyEntry struct {
	Prefill         string `yaml:"prefill"`
	Postfill        string `yaml:"postfill"`
	RequiredDefault *bool  `yaml:"required_default"`
	MergeAbsentNull *bool  `yaml:"merge_absent_null"`
	Evidence        string `yaml:"evidence"`
}

// policyFile accepts both the flat fixture form and the generated view form
// (node_kinds.code).
type policyFile struct {
	policyEntry `yaml:",inline"`
	NodeKinds   map[string]policyEntry `yaml:"node_kinds"`
}

// LoadPolicy reads one injected policy fixture. Missing fields are an error:
// deleting the policy must make the consumer fail loud (ES-071/ES-090).
func LoadPolicy(path string) (Policy, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, fmt.Errorf("execution policy %q: %w", path, err)
	}
	var f policyFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return Policy{}, fmt.Errorf("execution policy %q: %w", path, err)
	}
	entry := f.policyEntry
	if e, ok := f.NodeKinds["code"]; ok {
		entry = e
	}
	if entry.Prefill == "" {
		return Policy{}, fmt.Errorf("execution policy %q: missing prefill", path)
	}
	if entry.Postfill == "" {
		return Policy{}, fmt.Errorf("execution policy %q: missing postfill", path)
	}
	p := Policy{
		PreFill:  FillMode(entry.Prefill),
		PostFill: PostFillMode(entry.Postfill),
		Evidence: entry.Evidence,
	}
	switch p.PreFill {
	case PreFillZero, PreFillNil, PreFillNone:
	default:
		return Policy{}, fmt.Errorf("execution policy %q: unknown prefill %q", path, entry.Prefill)
	}
	switch p.PostFill {
	case PostFillNil, PostFillNone:
	default:
		return Policy{}, fmt.Errorf("execution policy %q: unknown postfill %q", path, entry.Postfill)
	}
	if entry.RequiredDefault != nil {
		p.EnforceRequired = *entry.RequiredDefault
	}
	if entry.MergeAbsentNull != nil {
		p.MergeAbsentNull = *entry.MergeAbsentNull
	}
	return p.normalized(), nil
}
