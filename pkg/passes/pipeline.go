package passes

// Pipeline implementation, mirroring base.py DSLPassPipeline
// (base.py:345-432): a fixed 15-stage order, error short-circuit semantics
// and a stage registry for incremental wiring.

import (
	"errors"
	"fmt"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/parser"
	"github.com/nodecoda/ncoda/pkg/tool"
)

// Stage identifies one pass in the fixed pipeline order. The ordering below
// mirrors base.py DSLPassPipeline.run pass_classes verbatim (base.py:403-419):
//
//	desugar → normalize → scope → definite_init → effect → type → parallel →
//	loop_state → yield_contract → response_contract → code_analysis → syntax →
//	semantic → target_validation → capability_gate
//
// Each pass task registers its stage via Pipeline.Register. StageScope is
// special: when it is unregistered the pipeline falls back to the default
// ScopePass (Task 14 D1), so the frontend contract — the scope phase always
// runs and Bindings is never nil — holds even with an empty registry.
type Stage int

const (
	StageDesugar Stage = iota
	StageNormalize
	StageScope // scope + symbol registration; default ScopePass fallback when unregistered (Task 14)
	StageDefiniteInit
	StageEffect
	StageType
	StageParallel
	StageLoopState
	StageYieldContract
	StageResponseContract
	StageCodeAnalysis
	StageSyntax
	StageSemantic
	StageTargetValidation
	StageCapabilityGate
)

var stageNames = map[Stage]string{
	StageDesugar:          "desugar",
	StageNormalize:        "normalize",
	StageScope:            "scope",
	StageDefiniteInit:     "definite_init",
	StageEffect:           "effect",
	StageType:             "type",
	StageParallel:         "parallel",
	StageLoopState:        "loop_state",
	StageYieldContract:    "yield_contract",
	StageResponseContract: "response_contract",
	StageCodeAnalysis:     "code_analysis",
	StageSyntax:           "syntax",
	StageSemantic:         "semantic",
	StageTargetValidation: "target_validation",
	StageCapabilityGate:   "capability_gate",
}

// String returns the stable stage name (base.py pass class name minus Pass).
func (s Stage) String() string {
	if name, ok := stageNames[s]; ok {
		return name
	}
	return fmt.Sprintf("Stage(%d)", int(s))
}

// stageOrder is the fixed 15-stage pipeline order, mirroring base.py
// pass_classes. It is the single source of truth for execution order; runs
// are deterministic because the array is constant and the registry is a
// map consulted in array order.
var stageOrder = [...]Stage{
	StageDesugar,
	StageNormalize,
	StageScope,
	StageDefiniteInit,
	StageEffect,
	StageType,
	StageParallel,
	StageLoopState,
	StageYieldContract,
	StageResponseContract,
	StageCodeAnalysis,
	StageSyntax,
	StageSemantic,
	StageTargetValidation,
	StageCapabilityGate,
}

// Pass is a single frontend pass, mirroring DSLPass.run(program, ctx).
type Pass interface {
	Run(p *ast.Program, ctx *Context)
}

// Pipeline runs registered passes in the fixed stage order with the
// reference short-circuit semantics: after every pass, an error diagnostic
// breaks the loop (base.py:424-425). Unregistered stages are skipped.
type Pipeline struct {
	registry map[Stage]Pass

	// toolRegistry optionally closes the open-world Tool namespace for
	// the type stage (typed tool() outputs from a semantic registry).
	toolRegistry *tool.Registry
}

// New returns an empty pipeline: no stage is registered yet. Pass tasks add
// their stage via Register.
func New() *Pipeline {
	return &Pipeline{registry: map[Stage]Pass{}}
}

// Register binds pass to stage. It fails when the stage is already
// registered or when pass is nil. StageScope is registrable like any other
// stage (Task 14 D1): a registered pass replaces the default ScopePass
// fallback.
func (p *Pipeline) Register(stage Stage, pass Pass) error {
	if pass == nil {
		return errors.New("passes: Register with a nil pass")
	}
	if _, exists := p.registry[stage]; exists {
		return fmt.Errorf("passes: stage %s already registered", stage)
	}
	p.registry[stage] = pass
	return nil
}

// FrontendResult is the frontend outcome, mirroring bindings.FrontendResult:
// the typed bound program plus every diagnostic collected from the reached
// stages. Program is nil when the source failed at the lexer or parser
// stage; the failure is signalled by error diagnostics carrying SYNTAX_ERROR.
type FrontendResult struct {
	Program     *binding.TypedBoundProgram
	Diagnostics []diag.Diagnostic
}

// HasErrors mirrors FrontendResult.has_errors.
func (r *FrontendResult) HasErrors() bool {
	return len(r.Errors()) > 0
}

// Errors returns the error diagnostics in collection order,
// mirroring FrontendResult.errors.
func (r *FrontendResult) Errors() []diag.Diagnostic {
	var errs []diag.Diagnostic
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error {
			errs = append(errs, d)
		}
	}
	return errs
}

// DefaultPipeline returns a Pipeline with every implemented stage
// registered in pipeline order (Task 17A wires yield_contract,
// response_contract, syntax and semantic alongside the earlier stages).
// New() keeps the empty registry for tests that need stage-level control;
// end-to-end harnesses (the pass tests, and later the difycodec frontend)
// use DefaultPipeline as the single source of wiring truth.
func DefaultPipeline() *Pipeline {
	p := New()
	register := func(stage Stage, pass Pass) {
		if err := p.Register(stage, pass); err != nil {
			panic("passes: DefaultPipeline wiring: " + err.Error())
		}
	}
	register(StageDesugar, &DesugarPass{})
	register(StageNormalize, &NormalizePass{})
	register(StageDefiniteInit, &DefiniteInitPass{})
	register(StageEffect, &EffectPass{})
	register(StageType, &TypePass{})
	register(StageParallel, &ParallelAnalysisPass{})
	register(StageLoopState, &LoopStatePass{})
	register(StageYieldContract, &YieldContractPass{})
	register(StageResponseContract, &ResponseContractPass{})
	register(StageCodeAnalysis, &CodeAnalysisPass{})
	register(StageSyntax, &SyntaxPass{})
	register(StageSemantic, &SemanticPass{})
	register(StageTargetValidation, &TargetValidationPass{})
	register(StageCapabilityGate, &CapabilityGatePass{})
	return p
}

// WithToolRegistry attaches a semantic Tool registry so the type stage can
// type tool() results from declared outputs. Nil (the default) preserves the
// open-world {result: any} behavior.
func (p *Pipeline) WithToolRegistry(registry *tool.Registry) *Pipeline {
	p.toolRegistry = registry
	return p
}

// Run compiles source with the default target profile: lexer → parser
// short-circuit → scope phase (Bind) → registered passes with short-circuit
// → TypeBind assembly of the TypedBoundProgram. It mirrors the frontend
// segment of pipeline.py:compile_dc_result and wires parser.ParseSource.
func Run(source, filename string) *FrontendResult {
	return New().Run(source, filename, nil)
}

// RunWithProfile is Run with an explicit target profile (nil selects the
// default), mirroring DSLPassPipeline.run(..., target_profile=...).
func RunWithProfile(source, filename string, profile *TargetProfile) *FrontendResult {
	return New().Run(source, filename, profile)
}

// Run compiles source through this pipeline (with its registered passes).
// Diagnostics are collected in deterministic order: parse errors, then scope
// diagnostics, then registered-pass diagnostics in stage order.
func (p *Pipeline) Run(source, filename string, profile *TargetProfile) *FrontendResult {
	if profile == nil {
		profile = DefaultTargetProfile()
	}
	program, perrs := parser.ParseSource(source, filename)
	if len(perrs) > 0 {
		diags := make([]diag.Diagnostic, 0, len(perrs))
		for _, perr := range perrs {
			diags = append(diags, parseErrorDiagnostic(perr))
		}
		return &FrontendResult{Diagnostics: diags}
	}

	ctx := NewContext(profile)
	ctx.ToolRegistry = p.toolRegistry
	p.runPipeline(program, ctx)

	if ctx.Bindings == nil {
		// A registered pre-scope pass broke the loop before the scope phase
		// ran. base.py publishes bindings unconditionally after the loop and
		// raises RuntimeError when the builder was never prepared; the Go
		// form binds on demand instead so the frontend result is always
		// publishable. The fallback's diagnostics are appended after the
		// pre-scope pass diagnostics, which is still stage order (desugar/
		// normalize precede scope).
		snap, diags := binding.Bind(program)
		ctx.Bindings = snap
		ctx.Diagnostics = append(ctx.Diagnostics, diags...)
	}

	// Deviation recorded (Task 15): the Go pipeline publishes a
	// TypedBoundProgram even when the type stage reported errors, so the
	// observable surface is the diagnostic set plus a best-effort program.
	// Python compile_nodecoda_result short-circuits validation failures and
	// returns ast=None; requireAggregates is disabled here under errors
	// (TypePass fills every map literal type, so the guard is belt-and-
	// braces for error paths only).
	typed := binding.TypeBindFull(program, ctx.Bindings, ctx.ExprTypes, ctx.VarTypes, ctx.EnumMembers, !ctx.HasErrors(), ctx.StructuralCodeRoots)
	// LoopStates wiring (task 23a): the type-bound publication carries the
	// loop_state facts LoopStatePass published on ctx (Python publishes
	// bindings.loop_states from ctx.loop_states after the pass loop). The
	// post-set keeps TypeBindFull's signature stable for existing callers.
	typed.LoopStates = copyLoopStates(ctx.LoopStates)
	// Agent wiring (task 25d): the type-bound publication carries the
	// validated_agents contracts TargetValidationPass published on ctx and
	// the target profile's agent_profiles (Python publishes
	// bindings.validated_agents from ctx.validated_agents after the pass
	// loop; the Go TypedBoundProgram carries AgentProfiles because it does
	// not embed the full target profile — task-11 skeleton note). Both maps
	// are copied, never shared.
	typed.ValidatedAgents = copyValidatedAgents(ctx.ValidatedAgents)
	// Classification wiring (task 25e): the type-bound publication carries
	// the validated_classifications contracts TargetValidationPass published
	// on ctx (Python publishes bindings.validated_classifications from
	// ctx.validated_classifications after the pass loop). The map is copied,
	// never shared (ValidatedAgents precedent).
	typed.ValidatedClassifications = copyValidatedClassifications(ctx.ValidatedClassifications)
	// Extraction wiring (task 25f): the type-bound publication carries the
	// validated_extractions contracts TargetValidationPass published on ctx
	// (Python publishes bindings.validated_extractions from
	// ctx.validated_extractions after the pass loop). The map is copied,
	// never shared (ValidatedAgents precedent).
	typed.ValidatedExtractions = copyValidatedExtractions(ctx.ValidatedExtractions)
	// Human-input wiring (task 37): the type-bound publication carries the
	// validated_human_inputs contracts TargetValidationPass published on
	// ctx (Python publishes bindings.validated_human_inputs from
	// ctx.validated_human_inputs after the pass loop). Copied, never shared
	// (ValidatedExtractions precedent).
	typed.ValidatedHumanInputs = copyValidatedHumanInputs(ctx.ValidatedHumanInputs)
	typed.AgentProfiles = copyAgentProfiles(ctx.Profile.AgentProfiles)
	return &FrontendResult{Program: typed, Diagnostics: ctx.Diagnostics}
}

// runPipeline walks the fixed stage order. The scope phase executes the
// Task 10 binder at its pipeline position; every other registered stage runs
// its pass, and an error diagnostic after any pass breaks the loop
// (base.py:424-425: `pass_cls().run(...); if ctx.has_errors(): break`).
func (p *Pipeline) runPipeline(program *ast.Program, ctx *Context) {
	// base.py:365-367: substitute const declarations before any analysis
	// pass runs, so every downstream pass sees literal values instead of
	// unresolved const identifier references (E1001 root cause).
	SubstituteConstants(program)
	// 常量调用折叠（2026-09-29，R-146）：`int("0")` / `float("0")` 编译期算完 ⇒
	// 不留 code shim 载体（不折叠的话，平台槽位只接变量引用/字面量 ⇒ 必须物化一个
	// 节点）。跟在常量代入之后：`const N = "0"; int(N)` 也折得到。
	FoldConstCalls(program)
	for _, stage := range stageOrder {
		pass, ok := p.registry[stage]
		if !ok {
			if stage != StageScope {
				continue // unregistered stage: skipped, no no-op stub
			}
			// Task 14 D1: an unregistered StageScope falls back to the
			// default ScopePass so the scope phase always runs (Bind +
			// E1033 cycle check; base.py:426-433) and Bindings is non-nil
			// after a clean pipeline, matching the pre-Task-14 frontend
			// contract.
			pass = ScopePass{}
		}
		pass.Run(program, ctx)
		if ctx.HasErrors() {
			return
		}
	}
}

// parseErrorDiagnostic converts a parser error to a diag.Diagnostic,
// mirroring pipeline.py:_parser_diagnostic: embedded codes are preserved,
// plain syntax errors are normalized to SYNTAX_ERROR. NumericWire passes
// through from the ParseError so coded errors keep the Python bare-int wire
// form (str(code)) while normalized SYNTAX_ERROR stays a symbol name (the
// Python _parser_diagnostic falls back to the DSLDiagnosticCode enum).
func parseErrorDiagnostic(e *parser.ParseError) diag.Diagnostic {
	code := e.Code
	if code == 0 {
		code = diag.SYNTAX_ERROR
	}
	return diag.Diagnostic{
		Code:         code,
		Severity:     diag.Error,
		Message:      e.Message,
		Filename:     e.Span.Start.Filename,
		Line:         e.Span.Start.Line,
		Column:       e.Span.Start.Column,
		Offset:       e.Span.Start.Offset,
		EndLine:      e.Span.End.Line,
		EndColumn:    e.Span.End.Column,
		EndOffset:    e.Span.End.Offset,
		NumericWire:  e.NumericWire,
		AdapterError: e.AdapterError,
	}
}

// copyValidatedAgents copies the pass-context agent-contract table so the
// typed program never shares the pipeline's mutable map (DESIGN D6
// convention, same as copyLoopStates). The copy is shallow — the
// AgentCallContract values are shared pointers (Python MappingProxyType
// shares the frozen dataclass values the same way). Task 26+ serialization
// should evaluate deep-copy/freeze if the typed-program surface ever
// becomes observable across compilations (25d review Minor 5).
// copyValidatedClassifications copies the pass-context classification-
// contract table so the typed program never shares the pipeline's mutable
// map (DESIGN D6 convention, same as copyValidatedAgents). The copy is
// shallow — the ValidatedClassificationContract values are shared pointers
// (Python MappingProxyType shares the frozen dataclass values the same
// way). Task 26+ serialization should evaluate deep-copy/freeze if the
// surface ever becomes observable across compilations (25d review Minor 5).
func copyValidatedClassifications(in map[binding.AstNodeID]*binding.ValidatedClassificationContract) map[binding.AstNodeID]*binding.ValidatedClassificationContract {
	if in == nil {
		return map[binding.AstNodeID]*binding.ValidatedClassificationContract{}
	}
	out := make(map[binding.AstNodeID]*binding.ValidatedClassificationContract, len(in))
	for id, contract := range in {
		out[id] = contract
	}
	return out
}

func copyValidatedAgents(in map[binding.AstNodeID]*binding.AgentCallContract) map[binding.AstNodeID]*binding.AgentCallContract {
	if in == nil {
		return map[binding.AstNodeID]*binding.AgentCallContract{}
	}
	out := make(map[binding.AstNodeID]*binding.AgentCallContract, len(in))
	for id, contract := range in {
		out[id] = contract
	}
	return out
}

// copyValidatedExtractions copies the pass-context extraction-contract
// table so the typed program never shares the pipeline's mutable map
// (DESIGN D6 convention, same as copyValidatedAgents). The copy is shallow
// — the ValidatedExtractionContract values are shared pointers (Python
// MappingProxyType shares the frozen dataclass values the same way). Task
// 26+ serialization should evaluate deep-copy/freeze if the surface ever
// becomes observable across compilations (25d review Minor 5).
// copyValidatedHumanInputs copies the pass-context human-input contract
// table so the typed program never shares the pipeline's mutable map
// (DESIGN D6 convention, copyValidatedExtractions precedent). Shallow copy
// — the ValidatedHumanInputContract values are shared pointers, exactly how
// Python's MappingProxyType shares the frozen dataclass values.
func copyValidatedHumanInputs(in map[binding.AstNodeID]*binding.ValidatedHumanInputContract) map[binding.AstNodeID]*binding.ValidatedHumanInputContract {
	if in == nil {
		return map[binding.AstNodeID]*binding.ValidatedHumanInputContract{}
	}
	out := make(map[binding.AstNodeID]*binding.ValidatedHumanInputContract, len(in))
	for id, contract := range in {
		out[id] = contract
	}
	return out
}

func copyValidatedExtractions(in map[binding.AstNodeID]*binding.ValidatedExtractionContract) map[binding.AstNodeID]*binding.ValidatedExtractionContract {
	if in == nil {
		return map[binding.AstNodeID]*binding.ValidatedExtractionContract{}
	}
	out := make(map[binding.AstNodeID]*binding.ValidatedExtractionContract, len(in))
	for id, contract := range in {
		out[id] = contract
	}
	return out
}

// copyAgentProfiles copies the pass profile's agent-profiles map so the
// typed program never shares the pipeline's mutable map (DESIGN D6
// convention, same as copyLoopStates). The copy is shallow — the
// AgentTargetProfile values are shared pointers (the Python reference
// freezes them via MappingProxyType + frozen dataclasses). Task 26+
// serialization should evaluate deep-copy/freeze if the surface becomes
// observable across compilations (25d review Minor 5).
func copyAgentProfiles(in map[string]*binding.AgentTargetProfile) map[string]*binding.AgentTargetProfile {
	if in == nil {
		return map[string]*binding.AgentTargetProfile{}
	}
	out := make(map[string]*binding.AgentTargetProfile, len(in))
	for alias, profile := range in {
		out[alias] = profile
	}
	return out
}

// copyLoopStates copies the pass-context loop-state table so the typed
// program never shares the pipeline's mutable map (DESIGN D6 convention,
// same as mergeEnumMembers in typed.go).
func copyLoopStates(in map[binding.AstNodeID][]binding.LoopStateFact) map[binding.AstNodeID][]binding.LoopStateFact {
	if in == nil {
		return map[binding.AstNodeID][]binding.LoopStateFact{}
	}
	out := make(map[binding.AstNodeID][]binding.LoopStateFact, len(in))
	for id, facts := range in {
		out[id] = append([]binding.LoopStateFact(nil), facts...)
	}
	return out
}
