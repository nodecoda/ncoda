package passes

// Ports of the reachable code_analysis cases from
// test_code_ffi_static_semantics.py and
// test_code_ffi_lowering_contract.py, mirroring code_analysis.py's
// lowerability surface AND the GATED source-validation segment (Task 35):
// Code input selector checks (E1022), structural region tracking, source
// interpolation detection (E2005 WARNING), and the restricted-scanner port
// of the embedded-Python contract checks (E1028 invalid source / missing
// main, E2006 parameter & return-key mismatch, E2005 unprovable return
// keys). The harness is the full pipeline (DefaultPipeline), same as
// yield_contract_test.go; case 6 additionally retains the Context to assert
// the StructuralCodeRoots publication (FrontendResult does not expose it).
//
// The three cases from test_code_ffi_static_semantics.py are ported here:
//
//  1. test_invalid_python_source_emits_e1028 — malformed Python source in a
//     Code template → E1028 CODE_SOURCE_INVALID (code_analysis.py:88-95).
//  2. test_unprovable_or_mismatched_source_warns_without_changing_types
//     (id=parameter-mismatch) — declared inputs ≠ main parameters → E2006
//     CODE_SOURCE_CONTRACT_MISMATCH (code_analysis.py:120-127).
//  3. ... (id=dynamic-return) — non-literal return keys → E2005
//     CODE_SOURCE_CONTRACT_UNVERIFIED (code_analysis.py:138-147).
//
// The embedded Python is validated by a restricted scanner (py_source_scan.go)
// that covers the parameter/return-key contract face without a Python parser;
// the E1028 syntax-error face is limited to the structural errors the scanner
// recognizes (see the scanner header for the exact deviation statement).

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/diag"
)

// runCodeAnalysisWithContext runs the full pipeline over ONE parse and
// returns the Context plus the program, so tests can inspect pass
// publications (StructuralCodeRoots) that FrontendResult does not expose.
// Pipeline.Run hides the Context; the same package can call the unexported
// runPipeline directly (the binding fallback and TypeBindFull assembly in
// Run are not needed for these assertions).
func runCodeAnalysisWithContext(t *testing.T, src string) (*Context, *ast.Program) {
	t.Helper()
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	DefaultPipeline().runPipeline(prog, ctx)
	return ctx, prog
}

// runCodeAnalysisWhiteBox runs scope + type (populating ExprTypes) and then
// ONLY the CodeAnalysisPass, after discarding the type stage's diagnostics.
// The full pipeline never reaches the E1022 "workflow value selector" branch
// for Function-typed Code inputs because the type stage rejects them first
// with E1021 (both Python and Go); this harness locks the branch logic in
// isolation (Python code_analysis.py:174-181).
func runCodeAnalysisWhiteBox(t *testing.T, src string) (*Context, *ast.Program) {
	t.Helper()
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	(&TypePass{}).Run(prog, ctx)
	ctx.Diagnostics = nil
	(&CodeAnalysisPass{}).Run(prog, ctx)
	return ctx, prog
}

func codeAnalysisErrors(res *FrontendResult) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range res.Diagnostics {
		if d.Severity == diag.Error && d.Code == diag.CODE_INPUT_SELECTOR {
			out = append(out, d)
		}
	}
	return out
}

func codeAnalysisWarnings(res *FrontendResult) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range res.Diagnostics {
		if d.Severity == diag.Warning && d.Code == diag.CODE_SOURCE_CONTRACT_UNVERIFIED {
			out = append(out, d)
		}
	}
	return out
}

// foreignCodeInitNamed returns the ForeignCodeExpr initializer of the
// variable named want, mirroring the reference test helpers' node lookup.
func foreignCodeInitNamed(t *testing.T, prog *ast.Program, want string) *ast.ForeignCodeExpr {
	t.Helper()
	var found *ast.ForeignCodeExpr
	ast.Walk(&matchVisitor{pred: func(n ast.Node) bool {
		vd, ok := n.(*ast.VarDeclStmt)
		if !ok || vd.Name() != want {
			return false
		}
		fc, ok := vd.Init().(*ast.ForeignCodeExpr)
		if !ok {
			return false
		}
		found = fc
		return true
	}}, prog)
	if found == nil {
		t.Fatalf("no foreign code initializer for %q", want)
	}
	return found
}

func TestCodeInputSelectorIdentIsLegal(t *testing.T) {
	// Port of the valid-input half of the lowering_contract cases: an
	// identifier selector must pass without E1022/E2005.
	const src = "function main(int value) {\n" +
		"    foreign code python3(int v = value) -> int {\n" +
		"        source `def main(v) -> dict:\n    return {\"result\": v}`;\n" +
		"    };\n" +
		"    return value;\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	if errs := res.Errors(); len(errs) != 0 {
		t.Fatalf("selector input must compile clean: %v", errs)
	}
	if errs := codeAnalysisErrors(res); len(errs) != 0 {
		t.Fatalf("unexpected E1022: %v", errs)
	}
	if warns := codeAnalysisWarnings(res); len(warns) != 0 {
		t.Fatalf("unexpected W2005: %v", warns)
	}
}

func TestOptionalCodeInputSelectorIsLegal(t *testing.T) {
	const src = "function main(string? value) {\n" +
		"    let out = foreign code python3(string? v = value) -> string {\n" +
		"        source `def main(v) -> dict:\n    return {\"result\": v or \"\"}`;\n" +
		"    };\n" +
		"    return out;\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "optional-code-input.ncoda", nil)
	if errs := res.Errors(); len(errs) != 0 {
		t.Fatalf("optional code input must compile clean: %v", errs)
	}
}

func TestForeignCodeNumericStringInputCoercionIsAllowed(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{
			name: "number-to-string",
			src: `function main(int value) {
    let out = foreign code javascript(string v = value) -> string {
        source ` + "`return {result: params.v};`" + `;
    };
    return out;
}`,
		},
		{
			name: "numeric-string-literal-to-int",
			src: `function main() {
    let out = foreign code javascript(int v = "42") -> int {
        source ` + "`return {result: params.v};`" + `;
    };
    return out;
}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if errs := DefaultPipeline().Run(tc.src, tc.name+".ncoda", nil).Errors(); len(errs) != 0 {
				t.Fatalf("numeric/string foreign-code boundary should compile: %v", errs)
			}
		})
	}
}

func TestForeignCodeOpaqueSelectorTypeMismatchIsAllowed(t *testing.T) {
	// Coze code ports are runtime/JSON boundaries rather than statically typed
	// function calls.  Keep the declared `string` contract, but do not reject
	// a workflow record passed to it: the foreign implementation and runtime
	// own interpretation of that value.
	const src = `function main() {
    let payload = { "name": "demo" };
    let out = foreign code javascript(string value = payload) -> string {
        source ` + "`return {result: params.value};`" + `;
    };
    return out;
}`
	if errs := DefaultPipeline().Run(src, "opaque-code-input.ncoda", nil).Errors(); len(errs) != 0 {
		t.Fatalf("opaque foreign-code selector mismatch should compile: %v", errs)
	}
}

func TestCodeInputFunctionValueShortCircuitsAtTypeStage(t *testing.T) {
	// The brief's case-2 source (int v = f, f = lambda) cannot reach the
	// E1022 "value selector" branch through the pipeline: the type stage
	// rejects the Function-typed input first with E1021 (CODE_INPUT_TYPE),
	// identically in Python (type_pass.py:994-996) and Go. Recorded
	// deviation from the brief expectation — the branch itself is locked by
	// TestCodeInputFunctionValueSelectorRejected below.
	const src = "function main(int value) {\n" +
		"    let f = (x) -> x;\n" + // line 2
		"    let out = foreign code python3(int v = f) -> int {\n" + // line 3
		"        source `def main(v) -> dict:\n    return {\"result\": v}`;\n" +
		"    };\n" +
		"    return out;\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	errs := res.Errors()
	if len(errs) != 1 || errs[0].Code != diag.CODE_INPUT_TYPE {
		t.Fatalf("want exactly one E1021 at the type stage, got %v", res.Diagnostics)
	}
	if errs[0].Line != 3 {
		t.Fatalf("E1021 line = %d, want 3", errs[0].Line)
	}
	if !strings.Contains(errs[0].Message, "Code input 'v' expected") {
		t.Fatalf("E1021 message = %q", errs[0].Message)
	}
}

func TestCodeInputFunctionValueSelectorRejected(t *testing.T) {
	// White-box: the E1022 "workflow value selector" branch
	// (code_analysis.py:174-181) fires when the input value's inferred type
	// is FUNCTION/VOID. Unreachable through the pipeline (E1021 first) but
	// locked here verbatim.
	const src = "function main(int value) {\n" +
		"    let f = (x) -> x;\n" + // line 2
		"    let out = foreign code python3(int v = f) -> int {\n" + // line 3
		"        source `def main(v) -> dict:\n    return {\"result\": v}`;\n" +
		"    };\n" +
		"    return out;\n" +
		"}\n"
	ctx, _ := runCodeAnalysisWhiteBox(t, src)
	errs := ctx.Errors()
	if len(errs) != 1 || errs[0].Code != diag.CODE_INPUT_SELECTOR {
		t.Fatalf("want exactly one E1022, got %v", ctx.Diagnostics)
	}
	want := "Code input 'v' cannot be lowered to a workflow value selector; bind a serializable value instead"
	if errs[0].Message != want {
		t.Fatalf("message = %q, want %q", errs[0].Message, want)
	}
	if errs[0].Line != 3 {
		t.Fatalf("E1022 line = %d, want 3 (the Code input binding)", errs[0].Line)
	}
}

func TestCodeInputLambdaValueSelectorRejected(t *testing.T) {
	// White-box: a direct LambdaExpr input takes the same E1022 branch
	// (code_analysis.py:174-181). Same pipeline unreachability as above.
	const src = "function main() {\n" +
		"    let out = foreign code python3(int v = (x) -> x) -> int {\n" + // line 2
		"        source `def main(v) -> dict:\n    return {\"result\": v}`;\n" +
		"    };\n" +
		"    return out;\n" +
		"}\n"
	ctx, _ := runCodeAnalysisWhiteBox(t, src)
	errs := ctx.Errors()
	if len(errs) != 1 || errs[0].Code != diag.CODE_INPUT_SELECTOR {
		t.Fatalf("want exactly one E1022, got %v", ctx.Diagnostics)
	}
	want := "Code input 'v' cannot be lowered to a workflow value selector; bind a serializable value instead"
	if errs[0].Message != want {
		t.Fatalf("message = %q, want %q", errs[0].Message, want)
	}
	if errs[0].Line != 2 {
		t.Fatalf("E1022 line = %d, want 2", errs[0].Line)
	}
}

func TestCodeInputNonSelectorExpressionRejected(t *testing.T) {
	// Port of test_pass_warning_precedes_literal_lowerability_error's E1022
	// half (test_compilation_result.py:63-70): a literal/expression input is
	// not a selector → "cannot be lowered directly to a workflow selector".
	// The W2005 that precedes it in the reference's fixture is the
	// dynamic-return warning (the reference source is `def main(v): pass`);
	// here the fixture uses a clean contract-matching source so only E1022
	// fires.
	const src = "function main() {\n" +
		"    let out = foreign code python3(int v = 1 + 2) -> int {\n" + // line 2
		"        source `def main(v) -> dict:\n    return {\"result\": v}`;\n" +
		"    };\n" +
		"    return out;\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	errs := codeAnalysisErrors(res)
	if len(errs) != 1 {
		t.Fatalf("want exactly one E1022, got %v", res.Diagnostics)
	}
	want := "Code input 'v' cannot be lowered directly to a workflow selector; assign it to a typed variable produced by a workflow node first"
	if errs[0].Message != want {
		t.Fatalf("message = %q, want %q", errs[0].Message, want)
	}
	if errs[0].Line != 2 {
		t.Fatalf("E1022 line = %d, want 2 (the Code input binding)", errs[0].Line)
	}
	if len(res.Errors()) != 1 {
		t.Fatalf("want E1022 to be the only error, got %v", res.Errors())
	}
}

func TestStructuralRootCannotEscapeParallelRegion(t *testing.T) {
	// Port of
	// test_code_ffi_lowering_contract.py::test_parallel_branch_structural_root_cannot_escape_its_definition_region
	// (regression/..., :357-380): a structural Code value defined inside a
	// parallel branch cannot be materialized outside it. The parallel
	// branch publishes `stats` to the enclosing scope (binder join export),
	// so the use resolves; the region prefix check then rejects it.
	const src = "function main() {\n" +
		"    parallel {\n" +
		"        {\n" +
		"            let stats = foreign code python3() -> {count: int; summary: string;} {\n" +
		"                source `def main() -> dict:\n    return {\"count\": 0, \"summary\": \"\"}`;\n" +
		"            };\n" +
		"        }\n" +
		"        { var other = \"ok\"; }\n" +
		"    }\n" +
		"    return stats;\n" + // line 10
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	errs := codeAnalysisErrors(res)
	if len(errs) != 1 {
		t.Fatalf("want exactly one E1022, got %v", res.Diagnostics)
	}
	want := "Structural Code value 'stats' cannot be materialized outside the control-flow region where it is defined"
	if errs[0].Message != want {
		t.Fatalf("message = %q, want %q", errs[0].Message, want)
	}
	if errs[0].Line != 11 {
		t.Fatalf("E1022 line = %d, want 11 (the escaping use)", errs[0].Line)
	}
}

func TestStructuralRootShallowDefDeepUseIsLegal(t *testing.T) {
	// Region prefix semantics (code_analysis.py:210-212): a use inside a
	// DEEPER region is legal as long as the definition region is a prefix
	// of it (use[:len(def)] == def) — the existing tests cover the same-level
	// legal case and the mismatch escape; this locks the prefix positive
	// direction: def at function-body level, use inside an if branch.
	const src = "function main(int flag) {\n" +
		"    let stats = foreign code python3() -> {count: int; summary: string;} {\n" +
		"        source `def main() -> dict:\n    return {\"count\": 0, \"summary\": \"\"}`;\n" +
		"    };\n" +
		"    if (flag == 1) {\n" +
		"        var x = stats;\n" + // deep use: [fn, if] vs def [fn] -> prefix match
		"        return x.count;\n" +
		"    }\n" +
		"    return stats.count;\n" + // field access — pruned, no extra mark
		"}\n"

	// Full pipeline: prefix-legal use compiles clean end-to-end.
	res := DefaultPipeline().Run(src, "t.dc", nil)
	if errs := res.Errors(); len(errs) != 0 {
		t.Fatalf("prefix-legal use must not error: %v", errs)
	}
	if errs := codeAnalysisErrors(res); len(errs) != 0 {
		t.Fatalf("unexpected E1022: %v", errs)
	}

	// White-box (scope+type, diagnostics reset, pass alone): the pass itself
	// marks the Code node as a structural root through the deep use.
	wctx, wprog := runCodeAnalysisWhiteBox(t, src)
	statsCode := foreignCodeInitNamed(t, wprog, "stats")
	statsID, ok := wctx.Bindings.NodeIDOf(statsCode)
	if !ok {
		t.Fatal("stats Code node not enumerated")
	}
	if !wctx.StructuralCodeRoots[statsID] {
		t.Fatalf("stats Code node must be marked as structural root (white-box): %v", wctx.StructuralCodeRoots)
	}
}

func TestStructuralRootInRegionUseIsLegalAndRecorded(t *testing.T) {
	// A structural Code value defined AND used inside the same if-branch
	// region must compile clean and mark the Code node as a structural
	// root (bindings.py:807-812 mark_structural_code_root). FrontendResult
	// does not expose the Context, so the pipeline runs with the retained
	// Context and the root publication is asserted directly.
	const src = "function main(int flag) {\n" +
		"    let fallback = foreign code python3() -> {count: int; summary: string;} {\n" +
		"        source `def main() -> dict:\n    return {\"count\": 0, \"summary\": \"\"}`;\n" +
		"    };\n" +
		"    if (flag == 1) {\n" +
		"        let stats = foreign code python3() -> {count: int; summary: string;} {\n" +
		"            source `def main() -> dict:\n    return {\"count\": 0, \"summary\": \"\"}`;\n" +
		"        };\n" +
		"        return stats;\n" + // in-region use
		"    }\n" +
		"    return fallback;\n" + // body-level use
		"}\n"
	ctx, prog := runCodeAnalysisWithContext(t, src)
	if errs := codeAnalysisErrors(&FrontendResult{Diagnostics: ctx.Diagnostics}); len(errs) != 0 {
		t.Fatalf("in-region use must not escape: %v", ctx.Diagnostics)
	}
	statsCode := foreignCodeInitNamed(t, prog, "stats")
	statsID, ok := ctx.Bindings.NodeIDOf(statsCode)
	if !ok {
		t.Fatal("stats Code node not enumerated")
	}
	if !ctx.StructuralCodeRoots[statsID] {
		t.Fatalf("stats Code node not recorded as structural root (StructuralCodeRoots=%v)", ctx.StructuralCodeRoots)
	}
	fallbackCode := foreignCodeInitNamed(t, prog, "fallback")
	fallbackID, ok := ctx.Bindings.NodeIDOf(fallbackCode)
	if !ok {
		t.Fatal("fallback Code node not enumerated")
	}
	if !ctx.StructuralCodeRoots[fallbackID] {
		t.Fatalf("fallback Code node not recorded as structural root (StructuralCodeRoots=%v)", ctx.StructuralCodeRoots)
	}
}

func TestStructuralFieldAccessDoesNotMaterializeRoot(t *testing.T) {
	// Port of test_structural_field_only_use_does_not_emit_root_adapter
	// (lowering_contract.py:304-322) at the diagnostic level: a structural
	// field use is a physical selector and must not request root
	// materialization (visit_field_access_expr prunes the subtree).
	const src = "function main() {\n" +
		"    let stats = foreign code python3() -> {count: int; summary: string;} {\n" +
		"        source `def main() -> dict:\n    return {\"count\": 0, \"summary\": \"\"}`;\n" +
		"    };\n" +
		"    return stats.count;\n" + // line 5
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	if errs := res.Errors(); len(errs) != 0 {
		t.Fatalf("field access must compile clean: %v", errs)
	}
	if errs := codeAnalysisErrors(res); len(errs) != 0 {
		t.Fatalf("field access must not emit E1022: %v", errs)
	}
}

func TestInterpolatedSourceWarnsUnverifiedContract(t *testing.T) {
	// Port of the interpolated-source case of
	// test_unprovable_or_mismatched_source_warns_without_changing_types
	// (static_semantics.py, id=interpolated-source): an interpolation in the
	// Code source makes the contract unverifiable → W2005, no error. This is
	// the only reachable half of the Python source-validation block (pure
	// AST shape check, code_analysis.py:99-102).
	const src = "function main(string generated) {\n" +
		"    let value = foreign code python3() -> int {\n" +
		"        source `${generated}`;\n" + // line 3
		"    };\n" +
		"    return value;\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	if errs := res.Errors(); len(errs) != 0 {
		t.Fatalf("interpolation must not error: %v", errs)
	}
	warns := codeAnalysisWarnings(res)
	if len(warns) != 1 {
		t.Fatalf("want exactly one W2005, got %v", res.Diagnostics)
	}
	want := "Code source contains interpolation; the declared contract remains authoritative"
	if warns[0].Message != want {
		t.Fatalf("message = %q, want %q", warns[0].Message, want)
	}
	if warns[0].Line != 3 {
		t.Fatalf("W2005 line = %d, want 3 (the source template)", warns[0].Line)
	}
}

func TestScalarDirectCodeAliasFieldAccessRegression(t *testing.T) {
	// Port of test_scalar_direct_code_alias_rejects_field_access
	// (static_semantics.py:126-138): a scalar direct Code value aliased to
	// `var alias` still rejects field access with E1014 FIELD_NOT_FOUND.
	// The diagnostic is owned by the type stage (type_pass.py:1860-1866
	// _is_direct_code_value + type_infer.go:313), which runs before
	// code_analysis and short-circuits; code_analysis must not mask or
	// duplicate it.
	const src = "function main() {\n" +
		"    let value = foreign code python3() -> int {\n" +
		"        source `def main() -> dict:\n    return {\"result\": 1}`;\n" +
		"    };\n" +
		"    var alias = value;\n" +
		"    return alias.result;\n" + // line 6
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	found := false
	for _, d := range res.Diagnostics {
		if d.Code == diag.FIELD_NOT_FOUND && d.Severity == diag.Error {
			found = true
			if d.Line != 7 {
				t.Fatalf("E1014 line = %d, want 7", d.Line)
			}
		}
	}
	if !found {
		t.Fatalf("expected E1014 FIELD_NOT_FOUND, got %v", res.Diagnostics)
	}
}

func TestCodeSourceInterpolatedStructuralValueMarksRoot(t *testing.T) {
	// R1 oracle-verified (DSLPassPipeline, structural_code_roots=1): a
	// structural Code value interpolated inside another Code's source
	// template runs the same IdentExpr region check and root
	// materialization as any other use — Python visit_foreign_code_expr
	// visits node.source (code_analysis.py:194) and base
	// visit_template_string (ast.py:816-820) traverses interpolated parts.
	// The first cut (c408e0b) pruned the source subtree (D1); this locks
	// the fix: the interpolated `stats` in-region use must mark the root.
	const src = "function main() {\n" +
		"    let stats = foreign code python3() -> {count: int; summary: string;} {\n" +
		"        source `def main() -> dict:\n    return {\"count\": 0, \"summary\": \"\"}`;\n" +
		"    };\n" +
		"    let other = foreign code python3() -> string {\n" +
		"        source `${stats}`;\n" + // line 6
		"    };\n" +
		"    return stats.count;\n" + // field access — pruned, no second mark
		"}\n"

	// Full pipeline: W2005 interpolation warning on the source template,
	// no E1022, no errors (Python oracle: has_errors=False).
	ctx, prog := runCodeAnalysisWithContext(t, src)
	res := &FrontendResult{Diagnostics: ctx.Diagnostics}
	if errs := res.Errors(); len(errs) != 0 {
		t.Fatalf("in-region interpolated use must not error: %v", errs)
	}
	warns := codeAnalysisWarnings(res)
	if len(warns) != 1 {
		t.Fatalf("want exactly one W2005 (interpolation), got %v", ctx.Diagnostics)
	}
	want := "Code source contains interpolation; the declared contract remains authoritative"
	if warns[0].Message != want {
		t.Fatalf("message = %q, want %q", warns[0].Message, want)
	}
	if warns[0].Line != 7 {
		t.Fatalf("W2005 line = %d, want 7 (the interpolated source template)", warns[0].Line)
	}
	// Python oracle: structural_code_roots=frozenset({AstNodeId(value=5)}) —
	// exactly one root (the stats Code node), despite `return stats.count`
	// being a field access.
	statsCode := foreignCodeInitNamed(t, prog, "stats")
	statsID, ok := ctx.Bindings.NodeIDOf(statsCode)
	if !ok {
		t.Fatal("stats Code node not enumerated")
	}
	if len(ctx.StructuralCodeRoots) != 1 {
		t.Fatalf("StructuralCodeRoots = %v, want exactly the stats node (oracle count=1)", ctx.StructuralCodeRoots)
	}
	if !ctx.StructuralCodeRoots[statsID] {
		t.Fatalf("stats Code node not recorded as structural root: %v", ctx.StructuralCodeRoots)
	}
}

func TestCodeSourceInterpolatedStructuralValueCannotEscape(t *testing.T) {
	// R1 oracle-verified (Python: E1022 line=1, structural_code_roots=∅):
	// a structural Code value defined inside a parallel branch and
	// interpolated into a Code source OUTSIDE the branch escapes its
	// definition region — the source walk surfaces the IdentExpr use and
	// the region prefix check rejects it. Template-inner idents carry a
	// fragment-local span in both implementations (Python parser.py
	// _parse_template_expr and Go parseTemplateExpr), so the diagnostic
	// lands on line 1 in the oracle; the Go assertion mirrors it.
	const src = "function main() {\n" +
		"    parallel {\n" +
		"        {\n" +
		"            let stats = foreign code python3() -> {count: int; summary: string;} {\n" +
		"                source `def main() -> dict:\n    return {\"count\": 0, \"summary\": \"\"}`;\n" +
		"            };\n" +
		"        }\n" +
		"        { var other = \"ok\"; }\n" +
		"    }\n" +
		"    let msg = foreign code python3() -> string {\n" +
		"        source `${stats}`;\n" + // line 11
		"    };\n" +
		"    return msg;\n" +
		"}\n"
	ctx, prog := runCodeAnalysisWithContext(t, src)
	res := &FrontendResult{Diagnostics: ctx.Diagnostics}
	errs := codeAnalysisErrors(res)
	if len(errs) != 1 {
		t.Fatalf("want exactly one E1022, got %v", ctx.Diagnostics)
	}
	want := "Structural Code value 'stats' cannot be materialized outside the control-flow region where it is defined"
	if errs[0].Message != want {
		t.Fatalf("message = %q, want %q", errs[0].Message, want)
	}
	// Template-inner idents use fragment-local spans (parity with the
	// Python oracle line=1); assert the actual line the parser assigned and
	// keep the parity note above.
	if errs[0].Line != 1 {
		t.Fatalf("E1022 line = %d, want 1 (template-inner ident fragment span, oracle parity)", errs[0].Line)
	}
	warns := codeAnalysisWarnings(res)
	if len(warns) != 1 {
		t.Fatalf("want exactly one W2005 (interpolation), got %v", ctx.Diagnostics)
	}
	if warns[0].Line != 12 {
		t.Fatalf("W2005 line = %d, want 12 (the interpolated source template)", warns[0].Line)
	}
	// The failed use must not mark a root (Python oracle: ∅).
	statsCode := foreignCodeInitNamed(t, prog, "stats")
	statsID, ok := ctx.Bindings.NodeIDOf(statsCode)
	if !ok {
		t.Fatal("stats Code node not enumerated")
	}
	if len(ctx.StructuralCodeRoots) != 0 {
		t.Fatalf("StructuralCodeRoots = %v, want empty (escaped use must not mark)", ctx.StructuralCodeRoots)
	}
	if ctx.StructuralCodeRoots[statsID] {
		t.Fatalf("stats Code node must not be a root after an escaped use: %v", ctx.StructuralCodeRoots)
	}
}

func TestDeclaredDirectTypeIsAuthoritativeOverPythonValue(t *testing.T) {
	// Port of test_declared_direct_type_is_authoritative_over_python_value
	// (static_semantics.py:67-96): the declared Code contracts type the
	// program; code_analysis must not introduce spurious diagnostics on a
	// legal program (the fixture sources match the reference's verbatim and
	// the source-validation scanner classifies them clean — no E1028/W2005/
	// E2006, matching the Python oracle's no-diagnostic result).
	const src = "function main() {\n" +
		"    let value = foreign code python3() -> int {\n" +
		"        source `def main() -> dict:\n    return {\"result\": \"not an int\"}`;\n" +
		"    };\n" +
		"    let copied = foreign code python3(float arg1 = value) -> float {\n" +
		"        source `def main(arg1) -> dict:\n    return {\"result\": arg1}`;\n" +
		"    };\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	if errs := res.Errors(); len(errs) != 0 {
		t.Fatalf("legal Code program must not error: %v", errs)
	}
	if errs := codeAnalysisErrors(res); len(errs) != 0 {
		t.Fatalf("unexpected E1022: %v", errs)
	}
}

// ── GATED source-validation port (Task 35) ─────────────────────────

// contractWarnings filters the diagnostics of res to the warnings carrying
// the given code, in pipeline order.
func contractWarnings(res *FrontendResult, code diag.Code) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range res.Diagnostics {
		if d.Severity == diag.Warning && d.Code == code {
			out = append(out, d)
		}
	}
	return out
}

func TestPyStrReprMatchesCPython(t *testing.T) {
	// R1 (Carson Minor6): pyStrRepr must reproduce CPython repr() exactly
	// for the E2006/E2005 message contract — quote selection when a value
	// contains a single quote, and \x/\u escapes for control and
	// non-printable characters. Expectations verified against
	// .venv/bin/python repr().
	cases := []struct {
		in   string
		want string
	}{
		{"it's", `"it's"`},
		{`say "hi"`, `'say "hi"'`},
		{"a\x01b", `'a\x01b'`},
		{"x\u2003y", `'x\u2003y'`},
		{`both'"`, `'both\'"'`},
		{"tab\there", `'tab\there'`},
		{"nl\nhere", `'nl\nhere'`},
		{"bs\\here", `'bs\\here'`},
		{"ctrl\x1f", `'ctrl\x1f'`},
		{"\xc3\xa9", "'\xc3\xa9'"},
		{"\x7f", `'\x7f'`},
		{"\U0001f600", "'\U0001f600'"}, // printable non-ASCII stays literal
		{"plain", "'plain'"},
	}
	for _, tc := range cases {
		if got := pyStrRepr(tc.in); got != tc.want {
			t.Errorf("pyStrRepr(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestInvalidPythonSourceEmitsE1028(t *testing.T) {
	// Port of test_invalid_python_source_emits_e1028 (static_semantics.py):
	// malformed Python source in a Code template → E1028 CODE_SOURCE_INVALID.
	// The reference's SyntaxError branch emits the diagnostic on the source
	// template span and still visits the Code children (no inputs here, so
	// no selector diagnostics follow — Python oracle: only E1028).
	const src = "function main() {\n" +
		"    let value = foreign code python3() -> int {\n" +
		"        source `def main(:\n" + // line 3
		"    return {\"result\": 1}`;\n" +
		"    };\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	var got []diag.Diagnostic
	for _, d := range res.Diagnostics {
		if d.Code == diag.CODE_SOURCE_INVALID {
			got = append(got, d)
		}
	}
	if len(got) != 1 {
		t.Fatalf("want exactly one E1028, got %v", res.Diagnostics)
	}
	want := "Invalid Python source: invalid syntax; fix the source template"
	if got[0].Message != want {
		t.Fatalf("message = %q, want %q", got[0].Message, want)
	}
	if got[0].Line != 3 {
		t.Fatalf("E1028 line = %d, want 3 (the source template span)", got[0].Line)
	}
}

func TestInvalidPythonSourceSkipsInputSelectorChecks(t *testing.T) {
	// R1 (Carson LOW2): with a structural E1028 syntax error, the
	// per-input selector checks are skipped (visitCodeChildren only walks
	// the binding values and the source template) — even for a
	// non-selector input that would normally fire CODE_INPUT_SELECTOR.
	// Oracle: Python emits exactly CODE_SOURCE_INVALID for this fixture.
	const src = "function main() {\n" +
		"    let out = foreign code python3(int v = 1 + 2) -> int {\n" + // line 2
		"        source `def main(:\n    return {\"result\": 1}`;\n" + // line 3
		"    };\n" +
		"    return out;\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	var codes []diag.Code
	for _, d := range res.Diagnostics {
		codes = append(codes, d.Code)
	}
	if len(codes) != 1 || codes[0] != diag.CODE_SOURCE_INVALID {
		t.Fatalf("diagnostics = %v, want exactly [%v]", codes, diag.CODE_SOURCE_INVALID)
	}
}

func TestSourceContractNoMainEmitsE1028(t *testing.T) {
	// Oracle-verified: a literal source without a top-level `main` → E1028
	// "Python source must define callable `main` ...". The reference then
	// falls through to the lowerability checks; with no inputs only the
	// E1028 fires (Python oracle: only CODE_SOURCE_INVALID).
	const src = "function main() {\n" +
		"    let value = foreign code python3() -> int {\n" +
		"        source `x = 1`;\n" + // line 3
		"    };\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	var got []diag.Diagnostic
	for _, d := range res.Diagnostics {
		if d.Code == diag.CODE_SOURCE_INVALID {
			got = append(got, d)
		}
	}
	if len(got) != 1 {
		t.Fatalf("want exactly one E1028, got %v", res.Diagnostics)
	}
	want := "Python source must define callable `main` matching the declared Code inputs"
	if got[0].Message != want {
		t.Fatalf("message = %q, want %q", got[0].Message, want)
	}
	if got[0].Line != 3 {
		t.Fatalf("E1028 line = %d, want 3 (the source template span)", got[0].Line)
	}
}

func TestSourceContractParameterMismatchWarns(t *testing.T) {
	// Port of test_unprovable_or_mismatched_source_warns_without_changing_types
	// (id=parameter-mismatch): declared inputs ≠ main parameters → E2006
	// CODE_SOURCE_CONTRACT_MISMATCH, message verbatim (Python oracle).
	const src = "function main(string query) {\n" +
		"    let value = foreign code python3(string text = query) -> string {\n" +
		"        source `def main(other) -> dict:\n" + // line 3
		"    return {\"result\": other}`;\n" +
		"    };\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	warns := contractWarnings(res, diag.CODE_SOURCE_CONTRACT_MISMATCH)
	if len(warns) != 1 {
		t.Fatalf("want exactly one E2006, got %v", res.Diagnostics)
	}
	want := "Declared Code inputs ['text'] do not match main parameters ['other']; the declaration remains authoritative"
	if warns[0].Message != want {
		t.Fatalf("message = %q, want %q", warns[0].Message, want)
	}
	if warns[0].Line != 3 {
		t.Fatalf("E2006 line = %d, want 3 (the source template span)", warns[0].Line)
	}
}

func TestSourceContractDynamicReturnWarns(t *testing.T) {
	// Port of ... (id=dynamic-return): a non-literal return value makes the
	// return keys unprovable → E2005 CODE_SOURCE_CONTRACT_UNVERIFIED,
	// message verbatim (Python oracle).
	const src = "function main() {\n" +
		"    let value = foreign code python3() -> int {\n" +
		"        source `def main() -> dict:\n" + // line 3
		"    return make_result()`;\n" +
		"    };\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	warns := contractWarnings(res, diag.CODE_SOURCE_CONTRACT_UNVERIFIED)
	if len(warns) != 1 {
		t.Fatalf("want exactly one W2005, got %v", res.Diagnostics)
	}
	want := "Python return keys cannot be proven statically; the declaration remains authoritative"
	if warns[0].Message != want {
		t.Fatalf("message = %q, want %q", warns[0].Message, want)
	}
	if warns[0].Line != 3 {
		t.Fatalf("W2005 line = %d, want 3 (the source template span)", warns[0].Line)
	}
}

func TestSourceContractOutputMismatchWarns(t *testing.T) {
	// Oracle-verified: two literal returns with different key sets vs a
	// direct `-> int` contract (declared outputs ['result']) → E2006 with
	// the full visible-return-keys repr in the message.
	const src = "function main() {\n" +
		"    let value = foreign code python3() -> int {\n" +
		"        source `def main() -> dict:\n" + // line 3
		"    return {\"result\": 1}\n" +
		"    return {\"other\": 2}`;\n" +
		"    };\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	warns := contractWarnings(res, diag.CODE_SOURCE_CONTRACT_MISMATCH)
	if len(warns) != 1 {
		t.Fatalf("want exactly one E2006, got %v", res.Diagnostics)
	}
	want := "Declared Code outputs ['result'] do not match visible return keys [['result'], ['other']]; the declaration remains authoritative"
	if warns[0].Message != want {
		t.Fatalf("message = %q, want %q", warns[0].Message, want)
	}
}

func TestSourceContractVarargInputsUnprovable(t *testing.T) {
	// Oracle-verified: *args in the main signature makes the visible inputs
	// unprovable (None) → E2006, message verbatim.
	const src = "function main() {\n" +
		"    let value = foreign code python3() -> int {\n" +
		"        source `def main(*args) -> dict:\n" + // line 3
		"    return {\"result\": 1}`;\n" +
		"    };\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	warns := contractWarnings(res, diag.CODE_SOURCE_CONTRACT_MISMATCH)
	if len(warns) != 1 {
		t.Fatalf("want exactly one E2006, got %v", res.Diagnostics)
	}
	want := "Declared Code inputs [] do not match main parameters None; the declaration remains authoritative"
	if warns[0].Message != want {
		t.Fatalf("message = %q, want %q", warns[0].Message, want)
	}
}

func TestSourceContractMultilineDictClean(t *testing.T) {
	// Oracle-verified: a structural Code whose main returns a multi-line dict
	// literal with the declared field names must produce no contract
	// diagnostics (Python oracle: SUCCEEDED, no CODE_* diagnostics).
	const src = "function main(string text) {\n" +
		"    let stats = foreign code python3(string text = text) -> {\n" +
		"        count: int;\n" +
		"        normalized: string;\n" +
		"    } {\n" +
		"        source `def main(text: str) -> dict:\n" + // line 6
		"    words = text.split()\n" +
		"    return {\n" +
		"        \"count\": len(words),\n" +
		"        \"normalized\": \" \".join(words).lower()\n" +
		"    }`;\n" +
		"    };\n" +
		"    return stats.count;\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	if errs := res.Errors(); len(errs) != 0 {
		t.Fatalf("multiline-dict structural Code must not error: %v", errs)
	}
	if warns := contractWarnings(res, diag.CODE_SOURCE_CONTRACT_UNVERIFIED); len(warns) != 0 {
		t.Fatalf("unexpected W2005: %v", warns)
	}
	if warns := contractWarnings(res, diag.CODE_SOURCE_CONTRACT_MISMATCH); len(warns) != 0 {
		t.Fatalf("unexpected E2006: %v", warns)
	}
}

func TestSourceBackslashContinuationRejected(t *testing.T) {
	// Oracle-verified (CPython tokenizer): a backslash outside a string is
	// only legal as a line continuation — `... dict:\n` (literal backslash
	// + 'n') raises "unexpected character after line continuation
	// character". The Go restricted scanner must emit the same E1028.
	const src = "function main() {\n" +
		"    let stats = foreign code python3() -> {count: int; summary: string;} {\n" +
		"        source `def main() -> dict:\\n    return {\"count\": 0, \"summary\": \"\"}`;\n" +
		"    };\n" +
		"    return stats;\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	var codes []diag.Code
	for _, d := range res.Diagnostics {
		codes = append(codes, d.Code)
	}
	if len(codes) != 1 || codes[0] != diag.CODE_SOURCE_INVALID {
		t.Fatalf("diagnostics = %v, want exactly [%v]", codes, diag.CODE_SOURCE_INVALID)
	}
	msg := res.Diagnostics[0].Message
	want := "Invalid Python source: unexpected character after line continuation character; fix the source template"
	if msg != want {
		t.Fatalf("message = %q, want %q", msg, want)
	}
}

// coze 代码节点 args 惯例（P3 补充，2026-09-01）：main(args) 单参 + 代码
// 经 params['x'] 按名取用输入是平台统一形态，声明输入 ⊆ 取用键 → 契约
// 吻合，不得报 E2006 惯例误报。
func TestSourceContractArgsConventionClean(t *testing.T) {
	const src = "function main(string text) {\n" +
		"    let stats = foreign code python3(string text = text) -> int {\n" +
		"        source `def main(args) -> dict:\n" + // line 3
		"    params = args.params\n" +
		"    words = params['text'].split()\n" +
		"    return {\"result\": len(words)}`;\n" +
		"    };\n" +
		"    return stats;\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	if errs := res.Errors(); len(errs) != 0 {
		t.Fatalf("args-convention Code must not error: %v", errs)
	}
	if warns := contractWarnings(res, diag.CODE_SOURCE_CONTRACT_MISMATCH); len(warns) != 0 {
		t.Fatalf("args convention must not warn E2006: %v", warns)
	}
}

// args 惯例但声明输入未在代码中取用 → 精确警告（比惯例误报更有诊断价值）。
func TestSourceContractArgsConventionUnusedInputWarns(t *testing.T) {
	const src = "function main(string text, string unused) {\n" +
		"    let stats = foreign code python3(string text = text, string unused = unused) -> int {\n" +
		"        source `def main(args) -> dict:\n" + // line 3
		"    params = args.params\n" +
		"    words = params['text'].split()\n" +
		"    return {\"result\": len(words)}`;\n" +
		"    };\n" +
		"    return stats;\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	warns := contractWarnings(res, diag.CODE_SOURCE_CONTRACT_MISMATCH)
	if len(warns) != 1 {
		t.Fatalf("want exactly one E2006 (unused input), got %v", res.Diagnostics)
	}
	want := "Declared Code inputs ['unused'] are not accessed via params in the source; the declaration remains authoritative"
	if warns[0].Message != want {
		t.Fatalf("message = %q, want %q", warns[0].Message, want)
	}
}

// main(args) 但声明无输入且代码也不取 params（退化惯例）→ 契约吻合无警告。
func TestSourceContractArgsConventionNoInputsClean(t *testing.T) {
	const src = "function main() {\n" +
		"    let stats = foreign code python3() -> int {\n" +
		"        source `def main(args) -> dict:\n" + // line 3
		"    ret: dict = {}\n" +
		"    return ret`;\n" +
		"    };\n" +
		"    return stats;\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	if warns := contractWarnings(res, diag.CODE_SOURCE_CONTRACT_MISMATCH); len(warns) != 0 {
		t.Fatalf("no-input args convention must not warn E2006: %v", warns)
	}
}

// 具名形参（非 args）仍是真实签名比对：集合不等 → 老消息。
func TestSourceContractNamedParamMismatchStillWarns(t *testing.T) {
	const src = "function main(string text) {\n" +
		"    let stats = foreign code python3(string text = text) -> int {\n" +
		"        source `def main(other: str) -> dict:\n" + // line 3
		"    words = other.split()\n" +
		"    return {\"result\": len(words)}`;\n" +
		"    };\n" +
		"    return stats;\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "t.dc", nil)
	warns := contractWarnings(res, diag.CODE_SOURCE_CONTRACT_MISMATCH)
	if len(warns) != 1 {
		t.Fatalf("want exactly one E2006 (named param mismatch), got %v", res.Diagnostics)
	}
	want := "Declared Code inputs ['text'] do not match main parameters ['other']; the declaration remains authoritative"
	if warns[0].Message != want {
		t.Fatalf("message = %q, want %q", warns[0].Message, want)
	}
}

func TestOptionalForeignCodeOutputContractsAreValid(t *testing.T) {
	const src = "function main() {\n" +
		"    let out = foreign code python3() -> { value: string?; items: array<string?>; } {\n" +
		"        source `def main() -> dict:\n    return {\"value\": None, \"items\": []}`;\n" +
		"    };\n" +
		"    return out.value;\n" +
		"}\n"
	res := DefaultPipeline().Run(src, "optional-code-output.ncoda", nil)
	if errs := res.Errors(); len(errs) != 0 {
		t.Fatalf("optional code output contracts must compile clean: %v", errs)
	}
}
