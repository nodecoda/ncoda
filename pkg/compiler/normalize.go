package compiler

// normalize.go — diagnostic normalization for the JSON contract, mirroring
// compile_cli.py _normalize_compile_diag (:26-44) and the SourceSpan /
// SourceLocation string forms (lang/tokens.py:200-220).
//
// Single source of truth: internal/compiler (closed multi-target build)
// delegates to these exported helpers so the dify wire shape cannot drift
// between the open and closed builds. The coze-biz specific error-class
// bridge (applyBackendErrClass) stays closed — it is the only piece that
// needs platforms/cozebiz types.

import (
	"strconv"

	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/token"
)

// NormalizeCompileDiag maps one frontend/backend diagnostic to the JSON
// wire shape. Python: code = enum member name, severity = lowercase name,
// message = str(message); location = str(SourceSpan) when the diagnostic
// carries a span ("{start}-{end}", each endpoint "{filename}:{line}:{col}").
// The file key is never emitted: Python's Diagnostic.filename is always None
// in the compile_nodecoda_result flow.
//
// Wire-code asymmetry: the Python parser emits Code diagnostics as bare ints
// (str(code) -> "1023".."1030"), while lexer/pass/lowering diagnostics carry
// the DSLDiagnosticCode enum (code.name). NumericWire marks the parser path
// so the two surfaces stay byte-identical to Python.
func NormalizeCompileDiag(d diag.Diagnostic) DiagJSON {
	code := d.Code.String()
	if d.NumericWire {
		code = strconv.Itoa(int(d.Code))
	}
	if d.AdapterError {
		// P4-4 depth-limit: byte-identical to Python's RecursionError catch
		// (compile_cli.py:84-90) — ADAPTER_ERROR, no location, error severity.
		return DiagJSON{Code: "ADAPTER_ERROR", Severity: "error", Message: d.Message}
	}
	out := DiagJSON{
		Code:     code,
		Severity: d.Severity.String(),
		Message:  d.Message,
	}
	// 错误类别（D1-D3）：由发出诊断的一方标注（不能从码反推）——pass 层
	// 直接带 class/capability/pending，此处原样透传。
	out.Class, out.Capability, out.Pending = d.Class, d.Capability, d.Pending
	// 码→类别缺省映射（仅在发出方未标注类别时生效）：TARGET_NOT_LOWERABLE 是
	// 「目标不支持该形状」的判定 ⇒ platform；LOWERING_INVARIANT 是 lowering
	// 自身不变量 ⇒ internal。其余码（1000-1054 语言码）保持缺省 = language，
	// 因此现有语言错诊断的 JSON 输出逐字节不变。
	if out.Class == "" {
		switch d.Code {
		case diag.TARGET_NOT_LOWERABLE:
			out.Class = diag.ClassPlatform
		case diag.LOWERING_INVARIANT:
			out.Class = diag.ClassInternal
		}
	}
	if out.Class != "" && out.Capability == "" && out.Class != diag.ClassLanguage {
		// 平台/内部类诊断至少带一个可检索的能力标识（脚按 (target, capability) 分流）。
		out.Capability = "unspecified"
	}
	if d.Line > 0 {
		out.Location = SpanString(d)
	}
	return out
}

// AttachDiagTarget 给带类别的诊断补上目标名（平台/内部/配置类都必须能回答
// 「这是对哪个 target 而言」；语言错不带类别、也不带 target，保持线格式不变）。
func AttachDiagTarget(ds []DiagJSON, target string) {
	for i := range ds {
		if ds[i].Class != "" && ds[i].Target == "" {
			ds[i].Target = target
		}
	}
}

// ApplyStrictFilter applies the strict-mode diagnostic contract (2026-09-01
// 决策): by default only error-severity diagnostics are kept; strict mode
// keeps warnings too. Warnings never flip the status.
func ApplyStrictFilter(strict bool, diags []DiagJSON) []DiagJSON {
	if strict {
		return diags
	}
	filtered := make([]DiagJSON, 0, len(diags))
	for _, d := range diags {
		if d.Severity != "warning" {
			filtered = append(filtered, d)
		}
	}
	return filtered
}

// SpanString renders the Python SourceSpan string for a diagnostic. The
// end location shares the start's filename (ast.Span endpoints always do;
// diag.Diagnostic keeps a single Filename). A missing end (programmatic
// diagnostic with only a start) degrades to start-only.
func SpanString(d diag.Diagnostic) string {
	start := token.Location{Filename: d.Filename, Line: d.Line, Column: d.Column, Offset: d.Offset}
	end := token.Location{Filename: d.Filename, Line: d.EndLine, Column: d.EndColumn, Offset: d.EndOffset}
	if end.Line <= 0 {
		end = start
	}
	return token.Span{Start: start, End: end}.String()
}
