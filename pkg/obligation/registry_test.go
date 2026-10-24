package obligation

import (
	"errors"
	"strings"
	"testing"
)

const validLoweringYAML = `schema: ncoda.obligations-lowering-registry/v1
entries:
  - target: adapter
    rewrite: emitProjectedWhile
    input: ["while"]
    output: ["iteration"]
    why-not-upstream: "the reference carrier has no while node."
    test-id: TC-17
  - target: runtime
    rewrite: elseLessJunctionMerge
    input: ["S", "T"]
    output: ["M", "T"]
    why-not-upstream: "The IR has no merge node for an else-less if."
    test-id: TC-41
    canonical_successor: "T"
`

func TestParseLoweringRegistryValid(t *testing.T) {
	reg, err := ParseLoweringRegistryFor([]byte(validLoweringYAML), TargetRuntime, "adapter")
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(reg.Entries))
	}
	if !reg.Declared("runtime", "elseLessJunctionMerge") {
		t.Fatal("runtime/elseLessJunctionMerge not declared")
	}
	if reg.Declared("adapter", "missing") {
		t.Fatal("undeclared lowering reported as declared")
	}
}

func TestParseLoweringRegistryRejectsUnknownField(t *testing.T) {
	bad := strings.Replace(validLoweringYAML, "    test-id: TC-17", "    test-id: TC-17\n    surprise: true", 1)
	assertRegistryCodeFor(t, bad, CodeLoweringRegistryInvalid)
}

func TestParseLoweringRegistryRejectsSuccessorOutsideOutput(t *testing.T) {
	bad := strings.Replace(validLoweringYAML, `canonical_successor: "T"`, `canonical_successor: "N"`, 1)
	assertRegistryCodeFor(t, bad, CodeLoweringRegistryInvalid)
}

func TestParseLoweringRegistryAllowsMissingSuccessor(t *testing.T) {
	if _, err := ParseLoweringRegistryFor([]byte(validLoweringYAML), TargetRuntime, "adapter"); err != nil {
		t.Fatalf("omitted canonical_successor must be legal: %v", err)
	}
}

func TestParseLoweringRegistryRejectsDuplicateRewrite(t *testing.T) {
	dup := strings.Replace(validLoweringYAML, "  - target: runtime", "  - target: adapter\n    rewrite: emitProjectedWhile\n    input: [\"x\"]\n    output: [\"y\"]\n    why-not-upstream: \"dup\"\n    test-id: TC-1\n  - target: runtime", 1)
	assertRegistryCodeFor(t, dup, CodeLoweringRegistryInvalid)
}

const validQuirksYAML = `schema: ncoda.obligations-quirks/v1
max_total: 5
entries: []
`

func TestParseQuirkRegistryEmpty(t *testing.T) {
	reg, err := ParseQuirkRegistry([]byte(validQuirksYAML))
	if err != nil {
		t.Fatal(err)
	}
	if reg.Matches("anything") {
		t.Fatal("empty whitelist matched an edge")
	}
}

func TestParseQuirkRegistryRejectsOverBudget(t *testing.T) {
	bad := `schema: ncoda.obligations-quirks/v1
max_total: 1
entries:
  - edge: "a"
    reason: "r"
    owner: "o"
    review_by: "2026-12-31"
    evidence: "e"
    max_entries: 2
`
	assertRegistryCodeFor(t, bad, CodeQuirkWhitelistExpired)
}

func TestParseQuirkRegistryRejectsUnknownField(t *testing.T) {
	bad := `schema: ncoda.obligations-quirks/v1
max_total: 5
entries: []
surprise: 1
`
	assertRegistryCodeFor(t, bad, CodeQuirkWhitelistExpired)
}

// assertRegistryCodeFor validates a lowering document against the test target
// set (runtime + the neutral adapter target); quirk documents take no set.
func assertRegistryCodeFor(t *testing.T, data, code string) {
	t.Helper()
	var err error
	if strings.HasPrefix(data, "schema: ncoda.obligations-lowering-registry") {
		_, err = ParseLoweringRegistryFor([]byte(data), TargetRuntime, "adapter")
	} else {
		_, err = ParseQuirkRegistry([]byte(data))
	}
	if err == nil {
		t.Fatalf("expected %s, got nil", code)
	}
	var rerr *RegistryError
	if !errors.As(err, &rerr) || rerr.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

// TestParseLoweringRegistryDefaultsToRuntimeOnly locks the kernel default: a
// registry naming an adapter target is rejected unless the caller declares
// that target. Platform identities belong to adapters (ASSET-BOUNDARY D8).
func TestParseLoweringRegistryDefaultsToRuntimeOnly(t *testing.T) {
	_, err := ParseLoweringRegistry([]byte(validLoweringYAML))
	if err == nil {
		t.Fatal("default parse must reject an adapter-owned target")
	}
	var rerr *RegistryError
	if !errors.As(err, &rerr) || rerr.Code != CodeLoweringRegistryInvalid {
		t.Fatalf("got %v, want %s", err, CodeLoweringRegistryInvalid)
	}
	if !strings.Contains(rerr.Message, "unknown target") {
		t.Fatalf("message = %q, want it to name the unknown target", rerr.Message)
	}
}

// TestParseLoweringRegistryForAcceptsDeclaredAdapterTarget is the positive
// half: once the caller declares the target, the same document parses.
func TestParseLoweringRegistryForAcceptsDeclaredAdapterTarget(t *testing.T) {
	reg, err := ParseLoweringRegistryFor([]byte(validLoweringYAML), TargetRuntime, "adapter")
	if err != nil {
		t.Fatalf("declared target must parse: %v", err)
	}
	if !reg.Declared("adapter", "emitProjectedWhile") {
		t.Fatal("adapter/emitProjectedWhile not declared")
	}
}
