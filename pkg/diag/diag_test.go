package diag

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/token"
)

// TestCodeParity guards the diagnostic-code extraction: count must match
// the (retired) Python enum plus Go-native extensions and every code must
// be in the correct severity range. SHADOWED_NAMESPACE (1051) and
// IMPORT_PLATFORM_REQUIRED (1052) are Go-native extensions added after Go
// graduation (TOOL-DIRECT-CALL-FORM / platform mechanism v2); CHATFLOW_ENTRY
// (1054) is the M13 advanced-chat extension; CROSS_TYPE_EQUALITY (2011) is
// the corpus-driven cross-type equality warning (X211/H36, 平台接受).
// TEMPLATE_RETURN_PLACEMENT/MIXED/IDENTITY (1057-1059) + TEMPLATE_BINDING_INVALID
// (1060) are the W2 `return template … using { … }` language errors (载体 = 语句，
// 取代已删除的 @answer 文件头声明). TARGET_CONTRACT_DEGRADED (2013) is the
// Go-native file-contract soft-degradation warning (目标承载不了约束轴 ⇒ 放宽 /
// 丢弃必须报账；见 pkg/passes/target_validation.go)。
// TEMPLATE_VALUE_PLACEMENT (1062) is the R-78 position rule (值通道装不下
// `template … using {…}`；见 pkg/ir/template_value_placement.go)。
func TestCodeParity(t *testing.T) {
	if len(names) != 70 {
		t.Fatalf("names has %d entries, want 70 (57 mirror codes + SHADOWED_NAMESPACE + IMPORT_PLATFORM_REQUIRED + CHATFLOW_ENTRY + RECORD_FIELD_REQUIRED + CROSS_TYPE_EQUALITY + REDUNDANT_COALESCE - OUTPUT_KEY_TYPE + TEMPLATE_RETURN_PLACEMENT + TEMPLATE_RETURN_MIXED + TEMPLATE_RETURN_IDENTITY + TEMPLATE_BINDING_INVALID + CAPABILITY_OUTPUTS_UNDECLARED + TARGET_CONTRACT_DEGRADED + TEMPLATE_VALUE_PLACEMENT)", len(names))
	}
	for code := range names {
		if code.IsError() == (code < 1000 || code >= 2000) {
			t.Errorf("code %d misclassified by IsError", int(code))
		}
	}
	if !SYNTAX_ERROR.IsError() || DEAD_CODE.IsError() {
		t.Errorf("severity range logic wrong")
	}
}

func TestDiagnosticString(t *testing.T) {
	d := Diagnostic{Code: SYNTAX_ERROR, Severity: Error, Message: "bad", Filename: "x.dc", Line: 1, Column: 2}
	s := d.String()
	if s == "" {
		t.Fatal("empty diagnostic string")
	}
}

func TestCodeStringKnownAndUnknown(t *testing.T) {
	if got := SYNTAX_ERROR.String(); got != "SYNTAX_ERROR" {
		t.Errorf("known code String = %q, want SYNTAX_ERROR", got)
	}
	if got := LOWERING_INVARIANT.String(); got != "LOWERING_INVARIANT" {
		t.Errorf("known code String = %q, want LOWERING_INVARIANT", got)
	}
	if got := DEAD_CODE.String(); got != "DEAD_CODE" {
		t.Errorf("warning code String = %q, want DEAD_CODE", got)
	}
	if got := Code(9999).String(); got != "DSLDiagnosticCode(9999)" {
		t.Errorf("unknown code String = %q, want DSLDiagnosticCode(9999)", got)
	}
}

func TestIsErrorBoundary(t *testing.T) {
	// 1xxx errors; 999 / 2000 fall outside; 1000 and 1999 are the edges.
	if !Code(1000).IsError() || !Code(1099).IsError() || !Code(1999).IsError() {
		t.Error("1xxx codes must be errors")
	}
	if Code(999).IsError() || Code(2000).IsError() || Code(0).IsError() {
		t.Error("non-1xxx codes must not be errors")
	}
}

func TestSeverityString(t *testing.T) {
	if got := Error.String(); got != "error" {
		t.Errorf("Error.String = %q, want error", got)
	}
	if got := Warning.String(); got != "warning" {
		t.Errorf("Warning.String = %q, want warning", got)
	}
	if got := Severity(99).String(); got != "error" {
		t.Errorf("unknown severity String = %q, want error (default)", got)
	}
}

func TestSetSpan(t *testing.T) {
	var d Diagnostic
	d.SetSpan(token.Span{}) // zero span must leave location fields empty
	if d.Filename != "" || d.Line != 0 || d.Offset != 0 {
		t.Errorf("zero span populated location: %+v", d)
	}
	sp := token.Span{
		Start: token.Location{Filename: "f.dc", Line: 3, Column: 4, Offset: 10},
		End:   token.Location{Filename: "f.dc", Line: 5, Column: 6, Offset: 30},
	}
	d.SetSpan(sp)
	if d.Filename != "f.dc" || d.Line != 3 || d.Column != 4 || d.Offset != 10 {
		t.Errorf("start fields wrong: %+v", d)
	}
	if d.EndLine != 5 || d.EndColumn != 6 || d.EndOffset != 30 {
		t.Errorf("end fields wrong: %+v", d)
	}
}

func TestDiagnosticStringDefaultFilename(t *testing.T) {
	d := Diagnostic{Code: TYPE_MISMATCH, Severity: Error, Message: "boom", Line: 7, Column: 8}
	s := d.String()
	want := "<input>:7:8: error: boom (TYPE_MISMATCH)"
	if s != want {
		t.Errorf("String = %q, want %q", s, want)
	}
	// Explicit filename overrides the default.
	d.Filename = "real.dc"
	if got := d.String(); got[:8] != "real.dc:" {
		t.Errorf("String with filename = %q, want real.dc prefix", got)
	}
	// Warning severity renders as warning.
	d2 := Diagnostic{Code: UNUSED_VAR, Severity: Warning, Message: "m", Line: 1, Column: 1}
	if got := d2.String(); !strings.Contains(got, "warning:") {
		t.Errorf("warning String = %q, want warning severity", got)
	}
}
