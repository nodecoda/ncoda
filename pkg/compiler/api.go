// Package compiler is the open distribution's language lane: lexer → parser →
// binding/name resolution → type check → IR build → ncoda-ir/v1 dump.
//
// It has no platform backend, and that is the point. The target-facing
// compiler (参考目标 / coze / coze-biz rendering) lives in the commercial repo;
// the open repo answers only "does this program mean something in the
// language?". See docs/language/CLI-CONTRACT.md for the wire surface.
//
// 2026-10-25 (ASSET-BOUNDARY P2c): the reference target pipeline that used to live here
// moved to the commercial repo. What remains is the language lane plus the
// platform-neutral wire helpers the commercial compiler still shares.
package compiler

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/irdump"
	"github.com/nodecoda/ncoda/pkg/parser"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/tool"
)

// compilerVersion is the contract version reported on the wire. Bumping it is
// a release action.
const compilerVersion = "0.4.0"

// CompilerVersion returns the contract version string (the CLI --version
// field and the dump's compiler_version).
func CompilerVersion() string { return compilerVersion }

// Statuses. The language lane has exactly two outcomes:
//
//	"ok"            the program means something and the IR was built
//	COMPILE_ERROR   it does not (lexer/parser/type/P6/IR build)
//
// Platform outcomes (SUCCEEDED / YAML_VALIDATION_ERROR / ANALYSIS_ERROR /
// CAPABILITY_BLOCKED / UNSUPPORTED_BY_TARGET) belong to the adapter lane and
// live in the commercial compiler.
const (
	StatusOK            = "ok"
	StatusCompileError  = "COMPILE_ERROR"
	StatusUnknownTarget = "UNKNOWN_TARGET"
)

// Diagnostic codes the language lane itself can emit.
const (
	DiagCodeAdapterError         = "ADAPTER_ERROR" // IR construction failed
	DiagCodeCodeSyntax           = "CODE_SYNTAX"
	DiagCodeCodeSyntaxUnverified = "CODE_SYNTAX_UNVERIFIED"
)

// Options selects the language lane's inputs. Nil Tool inputs preserve the
// legacy open-world calls.
type Options struct {
	Target       string         // default "__none__" (TargetLanguageOnly)
	Filename     string         // default "<input>"
	ToolRegistry *tool.Registry // semantic capability declarations
	ToolPaths    []string       // search paths for source imports (closed world)
	// Strict keeps warning-severity diagnostics in the result. Errors are
	// always reported; warnings never flip the status.
	Strict bool
	// EmitIR requests the canonical ncoda-ir/v1 dump. It is the language
	// lane's only product, so the CLI defaults it on.
	EmitIR bool
}

// DiagJSON is the wire shape of one diagnostic (shared with the commercial
// compiler, whose DiagJSON is an alias of this type).
type DiagJSON struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Location string `json:"location,omitempty"`
	// NodeID/Path are the analysis-diagnostic wire fields; nil means the
	// Python side would emit JSON null.
	NodeID *string `json:"-"`
	Path   *string `json:"-"`
	// FromAnalysis marks diagnostics produced by an analyzer stage.
	FromAnalysis bool `json:"-"`
	// Class/Capability/Target/Pending are the error-classification fields
	// (2026-09-13 D1-D3); the commercial compiler fills them.
	Class      string `json:"class,omitempty"`
	Capability string `json:"capability,omitempty"`
	Target     string `json:"target,omitempty"`
	Pending    bool   `json:"pending,omitempty"`
}

// Result is the typed language-lane outcome.
type Result struct {
	Target          string
	CompilerVersion string
	Status          string
	OK              bool
	Diagnostics     []DiagJSON
	// IR is the canonical ncoda-ir/v1 dump (L0'): populated only when
	// Options.EmitIR is set and the program compiled.
	IR *irdump.IRDump
}

// Compile runs the language lane. An empty Target defaults to
// TargetLanguageOnly and an empty Filename to "<input>".
func Compile(source string, opts Options) *Result {
	target := opts.Target
	if target == "" {
		target = TargetLanguageOnly
	}
	filename := opts.Filename
	if filename == "" {
		filename = "<input>"
	}
	registry := opts.ToolRegistry
	if registry == nil {
		resolved, _, rerr := resolveToolRegistry(source, filename, opts.ToolPaths)
		if rerr != nil {
			return compileErrorResult(target, rerr)
		}
		registry = resolved
	}
	r := compileRaw(source, target, filename, registry, opts.EmitIR)
	r.Diagnostics = ApplyStrictFilter(opts.Strict, r.Diagnostics)
	return r
}

func compileErrorResult(target string, err error) *Result {
	return &Result{
		Target:          target,
		CompilerVersion: compilerVersion,
		Status:          StatusCompileError,
		Diagnostics: []DiagJSON{{
			Code:     StatusCompileError,
			Severity: "error",
			Message:  err.Error(),
		}},
	}
}

// resolveToolRegistry loads the Tool registry from the source's import
// declarations against the given search paths (closed world). Returns
// (nil, nil, nil) when no search paths are given, the program has no imports,
// or the program is invalid (the pipeline then reports parse errors itself).
func resolveToolRegistry(source, filename string, toolPaths []string) (*tool.Registry, []string, error) {
	if len(toolPaths) == 0 {
		return nil, nil, nil
	}
	program, perrs := parser.ParseSource(source, filename)
	if len(perrs) > 0 {
		return nil, nil, nil
	}
	imports := make([]string, 0, len(program.Imports()))
	for _, imp := range program.Imports() {
		imports = append(imports, imp.Provider())
	}
	if len(imports) == 0 {
		return nil, nil, nil
	}
	registry, err := tool.LoadCapabilityPackages(toolPaths, imports)
	if err != nil {
		return nil, nil, err
	}
	return registry, imports, nil
}

// compileRaw is the un-coerced core: target gate, frontend, P6 projection,
// IR build, dump. target/filename are used verbatim.
func compileRaw(source, target, filename string, toolRegistry *tool.Registry, emitIR bool) *Result {
	base := func(status string, ok bool, diags []DiagJSON) *Result {
		return &Result{
			Target:          target,
			CompilerVersion: compilerVersion,
			Status:          status,
			OK:              ok,
			Diagnostics:     diags,
		}
	}

	// ── Step 1: the language lane is the only target the open distribution
	// can compile. A platform target is a usage error, never a silent render.
	languageOnly := target == TargetLanguageOnly
	if !languageOnly {
		return base(StatusUnknownTarget, false, []DiagJSON{{
			Code:     StatusUnknownTarget,
			Severity: "error",
			Message:  fmt.Sprintf("'Unknown NodeCoda target profile: %s' (the open distribution ships no platform adapter)", target),
		}})
	}

	// ── Steps 2-3: frontend (lexer/parser/scope/type) on the neutral
	// profile. No capability model, no platform profile.
	frontend := passes.DefaultPipeline().WithToolRegistry(toolRegistry).Run(source, filename, neutralLanguageProfile())
	diags := make([]DiagJSON, 0, len(frontend.Diagnostics))
	for _, d := range frontend.Diagnostics {
		diags = append(diags, NormalizeCompileDiag(d))
	}
	AttachDiagTarget(diags, target)
	if frontend.HasErrors() || frontend.Program == nil {
		return base(StatusCompileError, false, diags)
	}

	// P6: foreign code is a user-owned payload outside the typed IR. Its
	// well-formedness is judged here as a projection (early feedback) and
	// authoritatively at runtime load (SEM-CODE-001).
	codeDiags := CodeSyntaxDiags(frontend.Program)
	if len(codeDiags) > 0 {
		AttachDiagTarget(codeDiags, target)
		diags = append(diags, codeDiags...)
		for _, d := range codeDiags {
			if strings.EqualFold(d.Severity, "error") {
				return base(StatusCompileError, false, diags)
			}
		}
	}

	// ── Step 4: IR build. Capabilities are a platform fact, so the language
	// lane builds without them.
	irg, err := ir.Build(frontend.Program)
	if err != nil {
		return base(StatusCompileError, false, append(diags, DiagJSON{
			Code:     DiagCodeAdapterError,
			Severity: "error",
			Message:  err.Error(),
		}))
	}
	if !emitIR {
		return base(StatusOK, true, diags)
	}
	imports := make([]string, 0, len(frontend.Program.Program.Imports()))
	for _, imp := range frontend.Program.Program.Imports() {
		imports = append(imports, imp.Provider())
	}
	r := base(StatusOK, true, diags)
	r.IR = irdump.BuildIRDump(irg, toolRegistry, imports, filename, compilerVersion)
	return r
}

// CompileToJSON returns the language-lane result as the JSON wire object.
func CompileToJSON(source, filename, target string) map[string]any {
	return CompileToJSONWithOptions(source, Options{Target: target, Filename: filename})
}

// CompileToJSONWithOptions returns the wire object. Target and Filename are
// passed through verbatim (an explicit empty value stays empty).
func CompileToJSONWithOptions(source string, opts Options) map[string]any {
	registry := opts.ToolRegistry
	if registry == nil {
		resolved, _, err := resolveToolRegistry(source, opts.Filename, opts.ToolPaths)
		if err != nil {
			return resultToJSON(compileErrorResult(opts.Target, err))
		}
		registry = resolved
	}
	r := compileRaw(source, opts.Target, opts.Filename, registry, opts.EmitIR)
	r.Diagnostics = ApplyStrictFilter(opts.Strict, r.Diagnostics)
	return resultToJSON(r)
}

func resultToJSON(r *Result) map[string]any {
	diags := make([]any, 0, len(r.Diagnostics))
	for _, d := range r.Diagnostics {
		m := map[string]any{
			"code":     d.Code,
			"severity": d.Severity,
			"message":  d.Message,
		}
		if d.Location != "" {
			m["location"] = d.Location
		}
		if d.Class != "" {
			m["class"] = d.Class
		}
		if d.Capability != "" {
			m["capability"] = d.Capability
		}
		if d.Target != "" {
			m["target"] = d.Target
		}
		if d.Pending {
			m["pending"] = true
		}
		if d.FromAnalysis {
			var nodeID, path any
			if d.NodeID != nil {
				nodeID = *d.NodeID
			}
			if d.Path != nil {
				path = *d.Path
			}
			m["node_id"] = nodeID
			m["path"] = path
		}
		diags = append(diags, m)
	}
	m := map[string]any{
		"target":           r.Target,
		"compiler_version": r.CompilerVersion,
		"status":           r.Status,
		"ok":               r.OK,
		"diagnostics":      diags,
	}
	if r.IR != nil {
		m["ir"] = r.IR
	}
	return m
}

// ensureASCII ports Python's json.dump(ensure_ascii=True) over the encoder's
// output: every non-ASCII rune becomes a \uXXXX escape (astral code points
// become the UTF-16 surrogate pair).
func ensureASCII(data []byte) []byte {
	var out []byte
	for len(data) > 0 {
		b := data[0]
		if b < 0x80 {
			out = append(out, b)
			data = data[1:]
			continue
		}
		r, size := utf8.DecodeRune(data)
		if r <= 0xFFFF {
			out = append(out, []byte(fmt.Sprintf("\\u%04x", r))...)
		} else {
			r -= 0x10000
			hi := 0xD800 + (r >> 10)
			lo := 0xDC00 + (r & 0x3FF)
			out = append(out, []byte(fmt.Sprintf("\\u%04x\\u%04x", hi, lo))...)
		}
		data = data[size:]
	}
	return out
}

// MarshalContractJSON encodes a result object with the CLI's exact byte shape
// (compact separators, HTML-safe, trailing newline, ensure_ascii).
func MarshalContractJSON(v any) ([]byte, error) {
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return ensureASCII([]byte(sb.String())), nil
}
