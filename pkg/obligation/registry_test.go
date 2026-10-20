package obligation

import (
	"errors"
	"strings"
	"testing"
)

const validLoweringYAML = `schema: ncoda.obligations-lowering-registry/v1
entries:
  - target: dify
    rewrite: emitProjectedWhile
    input: ["while"]
    output: ["iteration"]
    why-not-upstream: "Dify has no while node."
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
	reg, err := ParseLoweringRegistry([]byte(validLoweringYAML))
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(reg.Entries))
	}
	if !reg.Declared("runtime", "elseLessJunctionMerge") {
		t.Fatal("runtime/elseLessJunctionMerge not declared")
	}
	if reg.Declared("dify", "missing") {
		t.Fatal("undeclared lowering reported as declared")
	}
}

func TestParseLoweringRegistryRejectsUnknownField(t *testing.T) {
	bad := strings.Replace(validLoweringYAML, "    test-id: TC-17", "    test-id: TC-17\n    surprise: true", 1)
	assertRegistryCode(t, bad, CodeLoweringRegistryInvalid)
}

func TestParseLoweringRegistryRejectsSuccessorOutsideOutput(t *testing.T) {
	bad := strings.Replace(validLoweringYAML, `canonical_successor: "T"`, `canonical_successor: "N"`, 1)
	assertRegistryCode(t, bad, CodeLoweringRegistryInvalid)
}

func TestParseLoweringRegistryAllowsMissingSuccessor(t *testing.T) {
	if _, err := ParseLoweringRegistry([]byte(validLoweringYAML)); err != nil {
		t.Fatalf("omitted canonical_successor must be legal: %v", err)
	}
}

func TestParseLoweringRegistryRejectsDuplicateRewrite(t *testing.T) {
	dup := strings.Replace(validLoweringYAML, "  - target: runtime", "  - target: dify\n    rewrite: emitProjectedWhile\n    input: [\"x\"]\n    output: [\"y\"]\n    why-not-upstream: \"dup\"\n    test-id: TC-1\n  - target: runtime", 1)
	assertRegistryCode(t, dup, CodeLoweringRegistryInvalid)
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
	assertRegistryCode(t, bad, CodeQuirkWhitelistExpired)
}

func TestParseQuirkRegistryRejectsUnknownField(t *testing.T) {
	bad := `schema: ncoda.obligations-quirks/v1
max_total: 5
entries: []
surprise: 1
`
	assertRegistryCode(t, bad, CodeQuirkWhitelistExpired)
}

func assertRegistryCode(t *testing.T, data, code string) {
	t.Helper()
	var err error
	if strings.HasPrefix(data, "schema: ncoda.obligations-lowering-registry") {
		_, err = ParseLoweringRegistry([]byte(data))
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
