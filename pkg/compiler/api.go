// Package compiler exposes the NodeCoda→Dify compilation pipeline as a
// library API and as the JSON result contract of the nclang-compile CLI.
//
// This package is the OPEN (Apache-2.0) dify-only entry point: it has no
// build tag because it never links Coze / Coze-biz / runtime code. The
// closed multi-target variant lives in internal/compiler (//go:build !dify)
// and must keep this package's JSON contract byte-identical for the Dify
// target. Binaries built from pkg/compiler are what the open distribution
// ships (nclang-compile -tags dify).
package compiler

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
	"github.com/nodecoda/ncoda/pkg/backend/dify/analyzer"
	"github.com/nodecoda/ncoda/pkg/backend/dify/verifier"
	"github.com/nodecoda/ncoda/pkg/capability"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/irdump"
	"github.com/nodecoda/ncoda/pkg/parser"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/target/codec"
	"github.com/nodecoda/ncoda/pkg/tool"
)

// compilerVersion is anchored to the current release milestone (I6). It
// used to mirror nclang.__version__ = "0.1.0" for byte parity with the
// Python oracle — that oracle is retired (Go 毕业, 2026-08-19), so the
// parity rationale is gone and the string now tracks the v0.4.0 milestone
// (docs/RELEASE-v0.4.0.md). Bumping it is a release action: the contract
// golden snapshots and contracts/nclang-compile-v1.json must be re-frozen
// together (contract_test.sh asserts the registry sha256s).
const compilerVersion = "0.4.0"

// CompilerVersion returns the contract version string (mirrors
// nclang.__version__); used by the CLI --version flag and the JSON
// compiler_version field.
func CompilerVersion() string { return compilerVersion }

// Status strings (compile_cli.py compile_to_json statuses). ADAPTER_ERROR
// and SERIALIZE_ERROR are diagnostic codes inside COMPILE_ERROR /
// YAML_VALIDATION_ERROR, not statuses (Python never sets them as status).
const (
	StatusSucceeded         = "SUCCEEDED"
	StatusCompileError      = "COMPILE_ERROR"
	StatusAnalysisError     = "ANALYSIS_ERROR"
	StatusYAMLValidationErr = "YAML_VALIDATION_ERROR"
	StatusUnknownTarget     = "UNKNOWN_TARGET"
	StatusCapabilityBlocked = "CAPABILITY_BLOCKED"
	DiagCodeAdapterError    = "ADAPTER_ERROR"
	DiagCodeSerializeError  = "SERIALIZE_ERROR"
	// P6 foreign-code syntax projection (see code_syntax.go). CODE_SYNTAX is
	// an error; CODE_SYNTAX_UNVERIFIED is a warning (no interpreter = cannot
	// judge, never a fake pass).
	DiagCodeCodeSyntax           = "CODE_SYNTAX"
	DiagCodeCodeSyntaxUnverified = "CODE_SYNTAX_UNVERIFIED"
)

// Options selects the compilation target, source label, and optional Tool
// compile/link inputs. Nil Tool inputs preserve the legacy open-world calls
// and deterministic backend placeholders.
//
// DIFY-ONLY variant: the complete multi-target Options (CozeToolBindings /
// CozeBizBindings / CozeBizWorkflowName / CozeFlowSource) lives in api.go
// (!dify build).
type Options struct {
	Target           string                    // default "dify-1.16-graphon-0.6" (codec.TargetName)
	Filename         string                    // default "<input>"
	ToolRegistry     *tool.Registry            // semantic capability declarations
	ToolPaths        []string                  // P3.5: search paths for source imports (closed world)
	DifyToolBindings *dify.ToolBindingManifest // target-specific physical links
	// ShowPlacements requests the native-vs-materialize lowering decision
	// log (PLACEMENT-PASS §5) in the JSON output. When true, the result
	// carries a "placement" array of {node, construct, decision, reason}
	// records. Backward compatible: zero value = omitted.
	ShowPlacements bool
	// Strict requests strict-mode diagnostics (2026-09-01 决策): by default
	// warning-severity diagnostics (2xxx) are suppressed from the result —
	// for most users they are noise, not errors, and they never flip the
	// status. Strict keeps them in the diagnostics array for auditing.
	// Errors are always reported. Zero value = warnings suppressed.
	Strict bool
	// EmitIR requests the canonical ncoda-ir/v1 dump (L0'). The compile
	// pipeline is unchanged; the result carries the platform-neutral
	// projection as well. Zero value = no dump.
	EmitIR bool
}

// DiagJSON is the wire shape of one diagnostic. Compile diagnostics mirror
// compile_cli.py _normalize_compile_diag (:26-44): location present only
// when the diagnostic carries a source span, file always absent (Python
// never sets Diagnostic.filename in the compile_nodecoda_result flow).
// Analysis diagnostics mirror _normalize_analysis_diag (:46-54): node_id
// and path keys are ALWAYS emitted (null when empty) — FromAnalysis marks
// the two surfaces so CompileToJSON can tell them apart (compile
// diagnostics never carry node_id/path keys, exactly like Python).
type DiagJSON struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Location string `json:"location,omitempty"`
	// NodeID/Path are the analysis-diagnostic wire fields; nil means the
	// Python side would emit JSON null.
	NodeID *string `json:"-"`
	Path   *string `json:"-"`
	// FromAnalysis marks diagnostics produced by the analyzer stage (Step
	// 4-5); only these emit node_id/path keys in CompileToJSON.
	FromAnalysis bool `json:"-"`
	// Class/Capability/Target/Pending are the error-classification fields
	// (2026-09-13 用户拍板 D1-D3). Same wire shape as api.go (!dify): the
	// dify-only build must carry them because compile.go (shared) fills them.
	Class      string `json:"class,omitempty"`
	Capability string `json:"capability,omitempty"`
	Target     string `json:"target,omitempty"`
	Pending    bool   `json:"pending,omitempty"`
}

// Result is the typed compile outcome mirroring compile_to_json.
type Result struct {
	Target          string
	CompilerVersion string
	Status          string
	OK              bool
	YAML            string
	YAMLSHA256      string
	// Zip is the coze-biz platform import/export archive
	// (Workflow-<name>-draft-<rand>/MANIFEST.yml + workflow/<name>-draft.yaml
	// — the real platform export shape). Populated for the coze-biz-1.0
	// target only; nil for dify/coze targets.
	Zip []byte
	// ZipSHA256 is the hex sha256 of Zip ("" when no archive is produced).
	ZipSHA256 string
	// ZipFilename is the recommended zip file name (<root>.zip, matching the
	// real platform export naming). "" when no archive is produced.
	ZipFilename string
	// WorkflowName is the resolved workflow name of the packaged document
	// (archive root + name field). For coze-biz-1.0: explicit
	// Options.CozeBizWorkflowName, else the source filename base (the name
	// the ncoda file was created with), else the "workflow" placeholder.
	// "" for non-coze-biz targets.
	WorkflowName string
	Diagnostics  []DiagJSON
	// Placements is the native-vs-materialize lowering decision log
	// (PLACEMENT-PASS §5). Populated when the caller requests it via
	// Options.ShowPlacements; nil when omitted.
	Placements []ir.Placement
	// IR is the canonical ncoda-ir/v1 dump (L0'): the language lane's only
	// product, and an optional projection for target lanes. Populated only
	// when Options.EmitIR is set; nil otherwise.
	IR *irdump.IRDump
}

// Compile runs the full pipeline (frontend → IR → backend render → serialize) and
// returns the contract result, mirroring compile_cli.py compile_to_json
// (:57-154) step by step. It never returns nil and never swallows errors:
// every failure mode maps to an explicit status + diagnostics.
//
// Library-level conveniences: an empty Options.Target defaults to
// codec.TargetName and an empty Options.Filename defaults to "<input>".
// The CLI JSON surface (CompileToJSON) deliberately skips both coercions so
// explicitly-passed empty values (--target "" / --filename "") flow through
// verbatim, exactly like Python's argparse raw-string passthrough.
func Compile(source string, opts Options) *Result {
	target := opts.Target
	if target == "" {
		target = codec.TargetName
	}
	filename := opts.Filename
	if filename == "" {
		filename = "<input>"
	}
	registry := opts.ToolRegistry
	// P3.5: with search paths but no preloaded registry, resolve the
	// source's import declarations against the paths (closed world).
	var imports []string
	if registry == nil {
		resolved, parsedImports, rerr := resolveToolRegistry(source, filename, opts.ToolPaths)
		if rerr != nil {
			return &Result{
				Target:          target,
				CompilerVersion: compilerVersion,
				Status:          StatusCompileError,
				OK:              false,
				Diagnostics: []DiagJSON{{
					Code:     StatusCompileError,
					Severity: "error",
					Message:  rerr.Error(),
				}},
			}
		}
		registry = resolved
		imports = parsedImports
	}
	difyBindings, berr := resolvePackageBindings(imports, opts.ToolPaths, target, registry, opts.DifyToolBindings)
	if berr != nil {
		return &Result{
			Target:          target,
			CompilerVersion: compilerVersion,
			Status:          StatusCompileError,
			OK:              false,
			Diagnostics: []DiagJSON{{
				Code:     StatusCompileError,
				Severity: "error",
				Message:  berr.Error(),
			}},
		}
	}
	r := compileRaw(source, target, filename, registry, difyBindings, opts.ShowPlacements, opts.EmitIR)
	r.Diagnostics = ApplyStrictFilter(opts.Strict, r.Diagnostics)
	return r
}

// resolveToolRegistry loads the Tool registry from the source's import
// declarations against the given search paths (P3.5 closed world). Returns
// (nil, nil) when no search paths are given, the program has no imports, or
// the program is invalid (the pipeline then reports parse errors itself). A
// light parse extracts the imports; the pipeline parses again, which is
// acceptable because this path only runs when --tool-path is given.
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
		return nil, imports, err
	}
	return registry, imports, nil
}

// resolvePackageBindings collects per-package binding files
// ("<provider>.dify.yaml" next to each resolved header) and merges them
// into the Dify central binding manifest (P3.5 §5 绑定随包). Without
// imports or search paths the central manifest passes through unchanged.
// A merge error (provider mismatch, undeclared operation, central/package
// key conflict) is reported as a compile error.
//
// DIFY-ONLY variant: non-Dify targets fall through to the compileRaw target
// gate (UNKNOWN_TARGET), mirroring the complete build for targets this
// binary cannot render.
func resolvePackageBindings(imports, toolPaths []string, target string, registry *tool.Registry, difyCentral *dify.ToolBindingManifest) (*dify.ToolBindingManifest, error) {
	if len(imports) == 0 || len(toolPaths) == 0 {
		return difyCentral, nil
	}
	if target != codec.TargetName {
		return difyCentral, nil
	}
	targetPlatform := "dify"
	// 规范绑定表自动发现（docs/TOOL-REGISTRY-LAYOUT.md）：外部工具产出的
	// 规范根里 bindings/dify/dify-bindings.yaml 直接被 --tool-path 导入。
	// 显式中央清单（参数非 nil）优先。
	if difyCentral == nil {
		m, derr := discoverCentralBinding(toolPaths, targetPlatform, func(data []byte) (*dify.ToolBindingManifest, error) {
			return dify.LoadToolBindingManifestYAML(data)
		})
		if derr != nil {
			return nil, derr
		}
		difyCentral = m
	}
	for _, provider := range imports {
		platform, _, perr := tool.SplitImport(provider)
		if perr != nil {
			return difyCentral, perr
		}
		if platform != "" && platform != targetPlatform {
			return nil, fmt.Errorf("import %q targets platform %q but compile target is %q", provider, platform, target)
		}
	}
	files, err := tool.PackageBindingFiles(toolPaths, imports, "dify")
	if err != nil {
		return difyCentral, err
	}
	if len(files) == 0 {
		return difyCentral, nil
	}
	merged, err := dify.MergePackageBindings(difyCentral, files, "dify", registry)
	if err != nil {
		return nil, err
	}
	return merged, nil
}

// compileRaw is the un-coerced core shared by Compile and CompileToJSON:
// the target gate, frontend, IR/backend rendering, and serialize steps mirroring
// compile_cli.py compile_to_json. target/filename are used verbatim.
func compileRaw(source, target, filename string, toolRegistry *tool.Registry, difyToolBindings *dify.ToolBindingManifest, includePlacements, emitIR bool) *Result {
	base := func(status string, ok bool, yamlText string, diags []DiagJSON) *Result {
		sha := ""
		if ok {
			sum := sha256.Sum256([]byte(yamlText))
			sha = hex.EncodeToString(sum[:])
		}
		return &Result{
			Target:          target,
			CompilerVersion: compilerVersion,
			Status:          status,
			OK:              ok,
			YAML:            yamlText,
			YAMLSHA256:      sha,
			Diagnostics:     diags,
		}
	}

	// ── Step 1: target gate (get_target_profile KeyError → UNKNOWN_TARGET).
	// Python str(KeyError) wraps the message in repr quotes. Coze is the
	// second canonical target: it consumes the platform-neutral frontend AST
	// directly (semantic separation — language semantics upstream, platform
	// structure in internal/backend/coze), so the difycodec gate is relaxed
	// for coze.TargetName only.
	languageOnly := target == TargetLanguageOnly
	if !languageOnly && target != codec.TargetName {
		return base(StatusUnknownTarget, false, "", []DiagJSON{{
			Code:     StatusUnknownTarget,
			Severity: "error",
			Message:  fmt.Sprintf("'Unknown NodeCoda target profile: %s'", target),
		}})
	}

	// ── Steps 2-3: compile_nodecoda_result (frontend, then IR/backend
	// rendering). The Go frontend short-circuits on lexer/parser/validation
	// errors; post-render verification reports LOWERING_INVARIANT diagnostics.
	frontendProfile := passes.DefaultTargetProfile()
	if languageOnly {
		frontendProfile = neutralLanguageProfile()
	}
	frontend := passes.DefaultPipeline().WithToolRegistry(toolRegistry).Run(source, filename, frontendProfile)
	diags := make([]DiagJSON, 0, len(frontend.Diagnostics))
	for _, d := range frontend.Diagnostics {
		diags = append(diags, NormalizeCompileDiag(d))
	}
	AttachDiagTarget(diags, target)
	if frontend.HasErrors() || frontend.Program == nil {
		return base(StatusCompileError, false, "", diags)
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
				return base(StatusCompileError, false, "", diags)
			}
		}
	}

	// Both backends consume the same platform-neutral IR instance. When a
	// Tool registry is supplied, it closes the otherwise open-world Tool
	// namespace before either target performs platform-specific rendering.
	var irOpts []ir.BuildOption
	if !languageOnly {
		irOpts = append(irOpts, ir.WithCapabilities(capability.For(target)))
	}
	irg, err := ir.Build(frontend.Program, irOpts...)
	var placements []ir.Placement
	if err == nil && includePlacements {
		placements = irg.Placements
	}
	if err == nil && emitIR {
		imports := make([]string, 0, len(frontend.Program.Program.Imports()))
		for _, imp := range frontend.Program.Program.Imports() {
			imports = append(imports, imp.Provider())
		}
		r := base("ok", true, "", diags)
		r.IR = irdump.BuildIRDump(irg, toolRegistry, imports, filename, compilerVersion)
		return r
	}
	if err == nil && languageOnly {
		// The language lane ends at the IR: no registry validation, no
		// bindings, no backend. Those are platform facts.
		return base("ok", true, "", diags)
	}
	if err != nil {
		adapterDiag := DiagJSON{
			Code:     DiagCodeAdapterError,
			Severity: "error",
			Message:  err.Error(),
		}
		return base(StatusCompileError, false, "", []DiagJSON{adapterDiag})
	}
	if toolRegistry != nil {
		if err := toolRegistry.ValidateGraph(irg); err != nil {
			diags = append(diags, DiagJSON{
				Code:     StatusCapabilityBlocked,
				Severity: "error",
				Message:  err.Error(),
			})
			return base(StatusCapabilityBlocked, false, "", diags)
		}
		// P3.3 §16.2: binding↔registry two-layer check. Collection
		// mismatches and explicit interface_hash pin violations are hard
		// errors; unpinned bindings surface as informational warnings.
		for _, mv := range []struct {
			name string
			m    interface {
				ValidateAgainst(r *tool.Registry) ([]string, error)
			}
		}{
			{"dify", difyToolBindings},
		} {
			if mv.m == nil {
				continue
			}
			unpinned, verr := mv.m.ValidateAgainst(toolRegistry)
			if verr != nil {
				diags = append(diags, DiagJSON{
					Code:     StatusCapabilityBlocked,
					Severity: "error",
					Message:  fmt.Sprintf("%s bindings: %v", mv.name, verr),
				})
				return base(StatusCapabilityBlocked, false, "", diags)
			}
			for _, key := range unpinned {
				diags = append(diags, DiagJSON{
					Code:     "binding_unpinned",
					Severity: "warning",
					Message:  fmt.Sprintf("%s binding %q has no interface_hash (P3.3 §16.2)", mv.name, key),
				})
			}
		}
	}

	ast, err := dify.RenderFromIRWithToolBindings(irg, frontend.Program.Program.Mode(), difyToolBindings, toolRegistry)
	if err != nil {
		return base(StatusCompileError, false, "", []DiagJSON{{
			Code:     DiagCodeAdapterError,
			Severity: "error",
			Message:  err.Error(),
		}})
	}

	// Keep the post-lowering structural verification gate after the backend
	// switch. Renderer-independent rules validate the finished Dify graph;
	// semantic construction is locked separately by IR/legacy parity tests.
	verification := verifier.VerifyStructure(ast, frontend.Program.AgentProfiles, codec.Default())
	for _, d := range verification.Diagnostics {
		diags = append(diags, NormalizeCompileDiag(d))
	}
	if !verification.OK() {
		return base(StatusCompileError, false, "", diags)
	}

	// ── Steps 4-5: analyze (compile_cli.py:100-128 order). The analyzer
	// stage runs on the lowered graph; its diagnostics are appended after
	// the compile diagnostics, and any error diagnostic flips the result
	// to ANALYSIS_ERROR with an empty yaml — exactly like Python. The
	// analysis runs unconditionally once the graph exists (warnings do not
	// change the status).
	analysis := analyzer.Analyze(ast)
	for _, d := range analysis.Diagnostics {
		diags = append(diags, NormalizeAnalysisDiag(d))
	}
	if !analysis.Ok() {
		return base(StatusAnalysisError, false, "", diags)
	}

	// ── Step 6: serialize → YAML_VALIDATION_ERROR on failure.
	om, serr := dify.SerializeToDict(ast)
	if serr == nil {
		var yamlText string
		yamlText, serr = dify.SerializeYAML(om)
		if serr == nil {
			r := base(StatusSucceeded, true, yamlText, diags)
			r.Placements = placements
			return r
		}
	}
	diags = append(diags, DiagJSON{Code: DiagCodeSerializeError, Severity: "error", Message: serr.Error()})
	return base(StatusYAMLValidationErr, false, "", diags)
}

// CompileToJSON returns the compile result as the JSON wire object, so the
// CLI can marshal a map[string]any (Go sorts map keys — the same visible
// ordering as Python's json.dump(sort_keys=True)). Unlike the library
// Compile(Options{}) path, target and filename are passed through verbatim
// (Python compile_to_json coerces nothing): --target "" yields UNKNOWN_TARGET
// with an empty profile name, and --filename "" yields bare "4:17-4:17"
// locations (SourceLocation.__str__ omits an empty filename).
func CompileToJSON(source, filename, target string) map[string]any {
	return CompileToJSONWithOptions(source, Options{Target: target, Filename: filename})
}

// CompileToJSONWithOptions returns the CLI wire object with optional Tool
// compile/link inputs. Unlike Compile, Target and Filename are intentionally
// passed through verbatim so command-line empty-value behavior remains exact.
func CompileToJSONWithOptions(source string, opts Options) map[string]any {
	registry := opts.ToolRegistry
	var imports []string
	if registry == nil {
		resolved, parsedImports, err := resolveToolRegistry(source, opts.Filename, opts.ToolPaths)
		if err != nil {
			r := &Result{
				Target:          opts.Target,
				CompilerVersion: compilerVersion,
				Status:          StatusCompileError,
				OK:              false,
				Diagnostics: []DiagJSON{{
					Code:     StatusCompileError,
					Severity: "error",
					Message:  err.Error(),
				}},
			}
			return resultToJSON(r)
		}
		registry = resolved
		imports = parsedImports
	}
	difyBindings, berr := resolvePackageBindings(imports, opts.ToolPaths, opts.Target, registry, opts.DifyToolBindings)
	if berr != nil {
		r := &Result{
			Target:          opts.Target,
			CompilerVersion: compilerVersion,
			Status:          StatusCompileError,
			OK:              false,
			Diagnostics: []DiagJSON{{
				Code:     StatusCompileError,
				Severity: "error",
				Message:  berr.Error(),
			}},
		}
		return resultToJSON(r)
	}
	r := compileRaw(source, opts.Target, opts.Filename, registry, difyBindings, opts.ShowPlacements, opts.EmitIR)
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
		// Error classification (2026-09-13 D1-D3): emitted only when set, so
		// ordinary language diagnostics keep the exact legacy wire shape
		// (Class empty = language).
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
			// Analysis diagnostics always carry node_id/path keys (null
			// when empty), mirroring _normalize_analysis_diag.
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
		"yaml":             r.YAML,
		"yaml_sha256":      r.YAMLSHA256,
		"diagnostics":      diags,
	}
	if len(r.Zip) > 0 {
		m["zip"] = base64.StdEncoding.EncodeToString(r.Zip)
		m["zip_sha256"] = r.ZipSHA256
		m["zip_filename"] = r.ZipFilename
		m["workflow_name"] = r.WorkflowName
	}
	if r.IR != nil {
		m["ir"] = r.IR
	}
	if len(r.Placements) > 0 {
		pa := make([]any, 0, len(r.Placements))
		for _, p := range r.Placements {
			pa = append(pa, map[string]any{
				"node":      p.NodeID,
				"construct": p.Construct,
				"decision":  p.Decision,
				"reason":    p.Reason,
			})
		}
		m["placement"] = pa
	}
	return m
}

// ensureASCII ports Python's json.dump(ensure_ascii=True) over the encoder's
// output: every non-ASCII rune is replaced by its \uXXXX escape (astral
// code points become the UTF-16 surrogate pair, exactly like Python's
// _make_iterencode). ASCII bytes — including the encoder's own escape
// sequences — pass through untouched.
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
		// encoding/json has already replaced any invalid UTF-8 byte with the
		// U+FFFD replacement rune (DecodeRune size 3), so the raw-byte
		// passthrough branch for RuneError+size 1 is unreachable on the
		// encoder's output and is intentionally not ported.
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

// MarshalContractJSON encodes a result object with the CLI's exact byte
// shape (see the file header): compact separators, HTML-safe, trailing
// newline, then the ensure_ascii pass. Exported for the CLI.
func MarshalContractJSON(v any) ([]byte, error) {
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return ensureASCII([]byte(sb.String())), nil
}

// discoverCentralBinding 在规范工具根里自动发现平台绑定中央清单：
// <tool-path>/bindings/<platform>/<platform>-bindings.yaml。遍历 toolPaths
// 取第一个存在的（与 header 的 first-wins 一致）；全部缺失返回 (nil, nil)。
// 这是外部工具（ncoda-foundry export）产物的规范路径——编译器直接导入。
func discoverCentralBinding[E any](toolPaths []string, platform string, load func([]byte) (*E, error)) (*E, error) {
	for _, dir := range toolPaths {
		path := filepath.Join(dir, tool.BindingsDir, platform, tool.CentralBindingFileName(platform))
		data, err := os.ReadFile(path)
		if err != nil {
			// 只有"不存在"才跳过继续找下一个根（first-wins）；"存在但不可读"
			// （EACCES/EISDIR）是真实的配置错误，静默跳过会残留 placeholder
			// 泄漏面（评审 M2 / 776ab8a 同族）。
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("auto-discovered binding %s: %w", path, err)
		}
		m, err := load(data)
		if err != nil {
			return nil, fmt.Errorf("auto-discovered binding %s: %w", path, err)
		}
		return m, nil
	}
	return nil, nil
}
