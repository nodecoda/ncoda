package obligation

// registry.go — strict loaders for the two machine-readable declaration
// files consumed by the P3a report pipeline (docs §8.4 / Q2 / Q3):
//
//   - testdata/obligations/lowering-registry.yaml — semantic-preserving
//     target rewrites that the differential gate may normalize away;
//   - testdata/obligations/quirks.yaml — the (budgeted) platform-quirk
//     whitelist.
//
// Both loaders reject unknown fields and reject structurally inconsistent
// entries with GOB_LOWERING_REGISTRY_INVALID / GOB_QUIRK_WHITELIST_EXPIRED.
// They are pure parsing/validation: no file system beyond the explicit path
// and no target imports, preserving the leaf-package boundary.

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	LoweringRegistrySchemaV1 = "ncoda.obligations-lowering-registry/v1"
	QuirkRegistrySchemaV1    = "ncoda.obligations-quirks/v1"
)

// TargetRuntime is the only target the open kernel names: the runtime ships
// in this repository, so "runtime" is a language-level identity. Platform
// targets are adapter-owned (ASSET-BOUNDARY D8): a caller that serves them
// passes its own list to LoadLoweringRegistryFor.
const TargetRuntime = "runtime"

// LoweringRegistry is the parsed lowering-registry document.
type LoweringRegistry struct {
	Schema  string          `yaml:"schema"`
	Entries []LoweringEntry `yaml:"entries"`
}

// LoweringEntry declares one semantic-preserving rewrite. Input/Output are
// fixture-scoped canonical tokens compared as sets, never product ids.
type LoweringEntry struct {
	Target             string   `yaml:"target"`
	Rewrite            string   `yaml:"rewrite"`
	Input              []string `yaml:"input"`
	Output             []string `yaml:"output"`
	WhyNotUpstream     string   `yaml:"why-not-upstream"`
	TestID             string   `yaml:"test-id"`
	CanonicalSuccessor string   `yaml:"canonical_successor"`
}

// QuirkRegistry is the parsed quirk whitelist document.
type QuirkRegistry struct {
	Schema   string       `yaml:"schema"`
	MaxTotal int          `yaml:"max_total"`
	Entries  []QuirkEntry `yaml:"entries"`
}

// QuirkEntry is one admitted platform quirk.
type QuirkEntry struct {
	Edge       string `yaml:"edge"`
	Reason     string `yaml:"reason"`
	Owner      string `yaml:"owner"`
	ReviewBy   string `yaml:"review_by"`
	Evidence   string `yaml:"evidence"`
	MaxEntries int    `yaml:"max_entries"`
}

// RegistryError carries the diagnostic code for a malformed declaration
// file. It satisfies error.
type RegistryError struct {
	Code    string
	Message string
}

func (e *RegistryError) Error() string { return e.Code + ": " + e.Message }

func registryErr(format string, args ...any) error {
	return &RegistryError{Code: CodeLoweringRegistryInvalid, Message: fmt.Sprintf(format, args...)}
}

func quirkErr(format string, args ...any) error {
	return &RegistryError{Code: CodeQuirkWhitelistExpired, Message: fmt.Sprintf(format, args...)}
}

// LoadLoweringRegistry reads and validates a lowering-registry YAML file
// whose entries target only the runtime.
func LoadLoweringRegistry(path string) (*LoweringRegistry, error) {
	return LoadLoweringRegistryFor(path, TargetRuntime)
}

// LoadLoweringRegistryFor reads and validates a lowering-registry YAML file
// for the given target set. Adapters pass their own target names; the kernel
// itself knows only TargetRuntime.
func LoadLoweringRegistryFor(path string, targets ...string) (*LoweringRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, registryErr("reading lowering registry: %v", err)
	}
	return ParseLoweringRegistryFor(data, targets...)
}

// ParseLoweringRegistry strictly decodes and validates a registry document
// whose entries target only the runtime.
func ParseLoweringRegistry(data []byte) (*LoweringRegistry, error) {
	return ParseLoweringRegistryFor(data, TargetRuntime)
}

// ParseLoweringRegistryFor strictly decodes and validates a registry document
// against the given target set.
func ParseLoweringRegistryFor(data []byte, targets ...string) (*LoweringRegistry, error) {
	var reg LoweringRegistry
	if err := strictYAML(data, &reg); err != nil {
		return nil, registryErr("%v", err)
	}
	if reg.Schema != LoweringRegistrySchemaV1 {
		return nil, registryErr("unexpected schema %q, want %q", reg.Schema, LoweringRegistrySchemaV1)
	}
	seen := map[string]bool{}
	for i, e := range reg.Entries {
		if !validTarget(e.Target, targets) {
			return nil, registryErr("entries[%d]: unknown target %q (accepted: %s)", i, e.Target, strings.Join(targets, ", "))
		}
		if strings.TrimSpace(e.Rewrite) == "" {
			return nil, registryErr("entries[%d]: rewrite is required", i)
		}
		if len(e.Input) == 0 || len(e.Output) == 0 {
			return nil, registryErr("entries[%d] (%s): input and output are required", i, e.Rewrite)
		}
		if strings.TrimSpace(e.WhyNotUpstream) == "" {
			return nil, registryErr("entries[%d] (%s): why-not-upstream is required", i, e.Rewrite)
		}
		if strings.TrimSpace(e.TestID) == "" {
			return nil, registryErr("entries[%d] (%s): test-id is required", i, e.Rewrite)
		}
		key := e.Target + "\x00" + e.Rewrite
		if seen[key] {
			return nil, registryErr("entries[%d]: duplicate (%s, %s)", i, e.Target, e.Rewrite)
		}
		seen[key] = true
		if e.CanonicalSuccessor != "" && !containsString(e.Output, e.CanonicalSuccessor) {
			return nil, registryErr("entries[%d] (%s): canonical_successor %q is not in output %v", i, e.Rewrite, e.CanonicalSuccessor, e.Output)
		}
	}
	return &reg, nil
}

// Lookup returns the declared entry for (target, rewrite).
func (r *LoweringRegistry) Lookup(target, rewrite string) (LoweringEntry, bool) {
	if r == nil {
		return LoweringEntry{}, false
	}
	for _, e := range r.Entries {
		if e.Target == target && e.Rewrite == rewrite {
			return e, true
		}
	}
	return LoweringEntry{}, false
}

// Declared reports whether (target, rewrite) is declared.
func (r *LoweringRegistry) Declared(target, rewrite string) bool {
	_, ok := r.Lookup(target, rewrite)
	return ok
}

// LoadQuirkRegistry reads and validates a quirk-whitelist YAML file.
func LoadQuirkRegistry(path string) (*QuirkRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, quirkErr("reading quirk registry: %v", err)
	}
	return ParseQuirkRegistry(data)
}

// ParseQuirkRegistry strictly decodes and validates a quirk whitelist.
func ParseQuirkRegistry(data []byte) (*QuirkRegistry, error) {
	var reg QuirkRegistry
	if err := strictYAML(data, &reg); err != nil {
		return nil, quirkErr("%v", err)
	}
	if reg.Schema != QuirkRegistrySchemaV1 {
		return nil, quirkErr("unexpected schema %q, want %q", reg.Schema, QuirkRegistrySchemaV1)
	}
	if reg.MaxTotal < 0 {
		return nil, quirkErr("max_total must be >= 0")
	}
	seen := map[string]bool{}
	budget := 0
	for i, e := range reg.Entries {
		for field, value := range map[string]string{
			"edge": e.Edge, "reason": e.Reason, "owner": e.Owner,
			"review_by": e.ReviewBy, "evidence": e.Evidence,
		} {
			if strings.TrimSpace(value) == "" {
				return nil, quirkErr("entries[%d]: %s is required", i, field)
			}
		}
		if _, err := strconv.Atoi(strings.ReplaceAll(e.ReviewBy, "-", "")); err != nil {
			return nil, quirkErr("entries[%d]: review_by %q is not an ISO date", i, e.ReviewBy)
		}
		if seen[e.Edge] {
			return nil, quirkErr("entries[%d]: duplicate edge %q", i, e.Edge)
		}
		seen[e.Edge] = true
		max := e.MaxEntries
		if max == 0 {
			max = 1
		}
		if max < 0 {
			return nil, quirkErr("entries[%d]: max_entries must be >= 1", i)
		}
		budget += max
	}
	if budget > reg.MaxTotal {
		return nil, quirkErr("entries admit %d edges, over max_total %d", budget, reg.MaxTotal)
	}
	return &reg, nil
}

// Matches reports whether the whitelist admits the exact edge descriptor.
func (r *QuirkRegistry) Matches(edge string) bool {
	if r == nil {
		return false
	}
	for _, e := range r.Entries {
		if e.Edge == edge {
			return true
		}
	}
	return false
}

func validTarget(target string, targets []string) bool {
	for _, t := range targets {
		if t == target {
			return true
		}
	}
	return false
}

func containsString(list []string, want string) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}

func strictYAML(data []byte, out any) error {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return err
	}
	return nil
}
