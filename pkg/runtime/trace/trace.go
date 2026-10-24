// Package trace defines the execution-semantics trace record.
//
// The canonical machine schema is docs/five-step/EXEC-SEMANTICS.trace.schema.json
// (PRD §4.6). The minimal record required by P1a-1 is
// stage/rule_id/origin/evidence/deadline_ms; the remaining fields are the
// optional full-record extension consumed by P5.
package trace

import (
	"fmt"
	"regexp"
)

// Rule identifiers owned by this slice.
const (
	// RuleTimeout is the code-node timeout-decision rule.
	RuleTimeout = "SEM-TIMEOUT-001"
	// RuleFill is the resolve/coerce/fill rule for declared inputs.
	RuleFill = "SEM-FILL-001"
	// RulePostFill is the output-port materialisation rule.
	RulePostFill = "SEM-POSTFILL-001"
	// RuleRetry is the attempt/axis record emitted while retrying.
	RuleRetry = "SEM-RETRY-001"
	// RuleHost records that an http node carried its host physical fields
	// (headers/query) from the source onto the wire.
	RuleHost = "SEM-HOST-001"
	// RuleMerge records a merge (aggregate) first_non_zero decision: which
	// zero candidates were skipped and in what arm order (ES-102/ES-111).
	RuleMerge = "SEM-MERGE-001"
	// RuleCollect records one loop iteration's output-collection decision
	// (ES-045: absent_skip / null_append / zero_append / null_error).
	RuleCollect = "SEM-COLLECT-001"
	// RuleCodeSyntax records the load-time compile-only verdict on a
	// user-written foreign code node (SEM-CODE-001). The source text is
	// user-owned and escapes the typed IR: the only thing the semantic model
	// asserts about it is well-formedness under (language, interpreter
	// version), and that verdict is branch-independent.
	RuleCodeSyntax = "SEM-CODE-001"
	// RuleCodeImport records the platform module policy verdict for a Python
	// code node (SEM-CODE-002). The blacklist is platform policy (F3), so the
	// record origin is policy, not wire-present.
	RuleCodeImport = "SEM-CODE-002"
	// RuleCodeVersion records an observable mismatch between the declared
	// target language version and the interpreter actually used. It is a
	// gap, never a syntax error: reporting skew as a syntax failure would
	// turn an environment difference into a phantom source defect.
	RuleCodeVersion = "SEM-CODE-003"
)

// Origin is PRD §4.3's closed origin set (trace subset).
type Origin string

const (
	OriginWirePresent Origin = "wire-present"
	OriginWireAbsent  Origin = "wire-absent"
	OriginPolicy      Origin = "policy"
	OriginHarness     Origin = "harness"
)

// Origins returns the closed origin set in schema order.
func Origins() []Origin {
	return []Origin{OriginWirePresent, OriginWireAbsent, OriginPolicy, OriginHarness}
}

// Valid reports whether o is in the closed set.
func (o Origin) Valid() bool {
	switch o {
	case OriginWirePresent, OriginWireAbsent, OriginPolicy, OriginHarness:
		return true
	}
	return false
}

// Record is one trace event. Field names/tags mirror the schema exactly.
type Record struct {
	// Minimal set (required).
	Stage      string `json:"stage"`
	RuleID     string `json:"rule_id"`
	Origin     Origin `json:"origin"`
	Evidence   string `json:"evidence"`
	DeadlineMS int    `json:"deadline_ms"`

	// Optional full-record fields (P5).
	SemanticsVersion string `json:"semantics_version,omitempty"`
	Profile          string `json:"profile,omitempty"`
	Workflow         string `json:"workflow,omitempty"`
	Node             string `json:"node,omitempty"`
	Kind             string `json:"kind,omitempty"`
	FillRule         string `json:"fill_rule,omitempty"`
	CollectRule      string `json:"collect_rule,omitempty"`
	CheckRule        string `json:"check_rule,omitempty"`
	Attempt          int    `json:"attempt,omitempty"`
	Axis             string `json:"axis,omitempty"`
	Input            string `json:"input,omitempty"`
	Output           string `json:"output,omitempty"`
	Decision         string `json:"decision,omitempty"`
	Provenance       string `json:"provenance,omitempty"`
}

var (
	ruleIDPattern   = regexp.MustCompile(`^[A-Z][A-Z0-9]*(-[A-Z0-9]+)+$`)
	fillRulePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	evidencePattern = regexp.MustCompile(`^(platform_model\.yaml#[^\s#]+|docs/[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*#[^\s#]+|oss:[0-9a-f]{7,40}:[A-Za-z0-9_./-]+:[0-9]+|observed:[A-Za-z0-9_.:/#-]+|sidecar:[A-Za-z0-9_./#-]+|test:[A-Za-z0-9_./-]+|hand:[A-Za-z0-9_./-]+:[0-9]+)$`)
)

// Validate checks the minimal record contract.
func (r Record) Validate() error {
	if r.Stage == "" {
		return fmt.Errorf("trace: missing stage")
	}
	if !ruleIDPattern.MatchString(r.RuleID) {
		return fmt.Errorf("trace: invalid rule_id %q", r.RuleID)
	}
	if !r.Origin.Valid() {
		return fmt.Errorf("trace: invalid origin %q", r.Origin)
	}
	if r.Evidence == "" {
		return fmt.Errorf("trace: missing evidence")
	}
	if r.DeadlineMS < 0 {
		return fmt.Errorf("trace: negative deadline_ms %d", r.DeadlineMS)
	}
	if !evidencePattern.MatchString(r.Evidence) {
		return fmt.Errorf("trace: evidence %q is not a resolvable pointer", r.Evidence)
	}
	if r.FillRule != "" && !fillRulePattern.MatchString(r.FillRule) {
		return fmt.Errorf("trace: invalid fill_rule %q", r.FillRule)
	}
	if r.CollectRule != "" && !collectRuleAllowed(r.CollectRule) {
		return fmt.Errorf("trace: invalid collect_rule %q (want absent_skip|null_append|zero_append|null_error)", r.CollectRule)
	}
	if r.CheckRule != "" && !checkRuleAllowed(r.CheckRule) {
		return fmt.Errorf("trace: invalid check_rule %q (want syntax|import|version)", r.CheckRule)
	}
	if r.Attempt < 0 {
		return fmt.Errorf("trace: negative attempt %d", r.Attempt)
	}
	if r.Axis != "" && r.Axis != "per_attempt" && r.Axis != "total" {
		return fmt.Errorf("trace: invalid axis %q", r.Axis)
	}
	return nil
}

// Writer consumes trace records. Implementations may drop records when trace
// collection is disabled; callers treat a nil writer as "trace off".
type Writer interface {
	Write(Record) error
}

// collectRuleAllowed is the closed ES-045 set.
func collectRuleAllowed(v string) bool {
	switch v {
	case "absent_skip", "null_append", "zero_append", "null_error":
		return true
	}
	return false
}

// ValidateFull checks the P5 full-record contract on top of Validate:
// attempt/axis/input/output are always required, plus fill_rule (or
// collect_rule for SEM-COLLECT-001). ES-060b deletes any one of them and
// expects red; the rule-specific branch mirrors the schema's if/then/else.
func (r Record) ValidateFull() error {
	if err := r.Validate(); err != nil {
		return err
	}
	// Common full-record fields first; the rule-specific branch only decides
	// fill_rule vs collect_rule (mirrors the schema's allOf).
	if r.Attempt < 1 {
		return fmt.Errorf("trace: full record missing attempt (want >= 1)")
	}
	if r.Axis == "" {
		return fmt.Errorf("trace: full record missing axis")
	}
	if r.Input == "" {
		return fmt.Errorf("trace: full record missing input")
	}
	if r.Output == "" {
		return fmt.Errorf("trace: full record missing output")
	}
	if r.RuleID == RuleCollect {
		if r.CollectRule == "" {
			return fmt.Errorf("trace: collect record missing collect_rule")
		}
		return nil
	}
	if IsCodeCheckRule(r.RuleID) {
		if r.CheckRule == "" {
			return fmt.Errorf("trace: code-check record missing check_rule (rule %s)", r.RuleID)
		}
		return nil
	}
	if r.FillRule == "" {
		return fmt.Errorf("trace: full record missing fill_rule")
	}
	return nil
}

// IsCodeCheckRule reports whether ruleID belongs to the foreign-code
// well-formedness family (SEM-CODE-00x). Those records carry check_rule
// instead of fill_rule: they judge a value the model never fills.
func IsCodeCheckRule(ruleID string) bool {
	switch ruleID {
	case RuleCodeSyntax, RuleCodeImport, RuleCodeVersion:
		return true
	}
	return false
}

// checkRuleAllowed is the closed SEM-CODE-00x set.
func checkRuleAllowed(v string) bool {
	switch v {
	case "syntax", "import", "version":
		return true
	}
	return false
}
