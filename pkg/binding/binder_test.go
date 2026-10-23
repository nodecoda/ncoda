package binding

// Ports of the reference binder/scope-pass suites:
//   - lang/tests/lang/unit/test_scope_pass.py (TestScopePass, 7 cases)
//   - lang/tests/lang/unit/test_dsl_symbol_table.py (TestSymbol +
//     TestDSLBuiltinRegistry, 3 cases)
// plus the self-built determinism regression required by the task brief:
// two Bind runs over the same logical input must mint identical
// AstNodeID/ScopeID/SymbolID/FunctionID sequences (per-compile traversal
// ordinals, independent of the process-global ast.NodeID counter).

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/parser"
	"github.com/nodecoda/ncoda/pkg/types"
)

// ── helpers ───────────────────────────────────────────────────────────

func parseSource(t *testing.T, source string) *ast.Program {
	t.Helper()
	prog, errs := parser.ParseSource(source, "<test>.ncoda")
	if len(errs) != 0 {
		t.Fatalf("parse errors for %q: %v", source, errs)
	}
	return prog
}

func errorDiagnostics(t *testing.T, source string) ([]diag.Diagnostic, *Snapshot) {
	t.Helper()
	snap, diags := Bind(parseSource(t, source))
	var errs []diag.Diagnostic
	for _, d := range diags {
		if d.Severity == diag.Error {
			errs = append(errs, d)
		}
	}
	return errs, snap
}

func hasMessage(errs []diag.Diagnostic, substr string) bool {
	for _, d := range errs {
		if strings.Contains(d.Message, substr) {
			return true
		}
	}
	return false
}

func hasCode(errs []diag.Diagnostic, code diag.Code) bool {
	for _, d := range errs {
		if d.Code == code {
			return true
		}
	}
	return false
}

// emitMap serializes one snapshot map in sorted-key order (map iteration in
// Go is random, so keys must be sorted for a stable fingerprint).
func emitMap[K comparable, V any](b *strings.Builder, name string, m *RoMap[K, V], keyFn func(K) string, valFn func(V) string) {
	type row struct{ k, v string }
	var rows []row
	m.Range(func(k K, v V) bool {
		rows = append(rows, row{keyFn(k), valFn(v)})
		return true
	})
	sort.Slice(rows, func(i, j int) bool { return rows[i].k < rows[j].k })
	for _, r := range rows {
		fmt.Fprintf(b, "%s[%s]=%s\n", name, r.k, r.v)
	}
}

func numKey[K ~int](k K) string { return fmt.Sprintf("%d", k) }
func numVal[V ~int](v V) string { return fmt.Sprintf("%d", v) }

// fingerprint serializes every snapshot map in sorted-key order so two
// snapshots can be compared for full equality (same keys, same values, same
// ID assignments).
func fingerprint(s *Snapshot) string {
	var b strings.Builder
	emitMap(&b, "ast", s.AstNodes(), numKey[AstNodeID], func(v NodeInfo) string {
		return fmt.Sprintf("%d|%d|%s", v.Span.Start.Offset, v.Span.End.Offset, v.Kind)
	})
	emitMap(&b, "declsym", s.DeclarationSymbols(), numKey[AstNodeID], numVal[SymbolID])
	emitMap(&b, "refsym", s.ReferenceSymbols(), numKey[AstNodeID], numVal[SymbolID])
	emitMap(&b, "scope", s.ScopeBindings(), numKey[AstNodeID], numVal[ScopeID])
	emitMap(&b, "funcs", s.Functions(), numKey[AstNodeID], numVal[FunctionID])
	emitMap(&b, "decls", s.Declarations(), numKey[SymbolID], func(v SymbolDecl) string {
		return fmt.Sprintf("%s|%s|%d|%s", v.Name, v.Kind, v.Scope, v.Type.Kind)
	})
	emitMap(&b, "scopes", s.Scopes(), numKey[ScopeID], func(v ScopeInfo) string {
		return fmt.Sprintf("%d|%v", v.Parent, v.Decls)
	})
	emitMap(&b, "exits", s.ExitTargets(), numKey[AstNodeID], func(v ExitTarget) string {
		return fmt.Sprintf("%s|%d", v.Kind, v.ScopeID)
	})
	emitMap(&b, "calls", s.CallTargets(), numKey[AstNodeID], func(v CallTarget) string {
		return fmt.Sprintf("%d|%d|%s", v.Kind, v.Function, v.Name)
	})
	return b.String()
}

// ── TestScopePass (test_scope_pass.py) ─────────────────────────────────

func TestScopePassNoErrorsSimple(t *testing.T) {
	source := `
        function main(string query) {
            var x = query;
            output(x);
            let rendered = to_string(x);
        }
        `
	errs, snap := errorDiagnostics(t, source)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	// Snapshot must be fully populated: builtins + std + entry + param +
	// local symbols, scope tree, function binding, references, call target.
	if snap.Declarations().Len() < 5 {
		t.Fatalf("Declarations().Len() = %d, want >= 5 (builtins, std, main, query, x)", snap.Declarations().Len())
	}
	if snap.Functions().Len() != 1 {
		t.Fatalf("Functions().Len() = %d, want 1 (main)", snap.Functions().Len())
	}
	if snap.Scopes().Len() < 2 {
		t.Fatalf("Scopes().Len() = %d, want >= 2 (global + entry)", snap.Scopes().Len())
	}
	if snap.ReferenceSymbols().Len() == 0 {
		t.Fatalf("ReferenceSymbols() empty: query/print references were not recorded")
	}
	if snap.CallTargets().Len() == 0 {
		t.Fatalf("CallTargets() empty: to_string(x) call target was not recorded")
	}
}

func TestScopePassUndefinedVariable(t *testing.T) {
	source := `
        function main() {
            var x = y;
        }
        `
	errs, _ := errorDiagnostics(t, source)
	if !hasMessage(errs, "Undefined variable") {
		t.Fatalf("expected an Undefined variable diagnostic, got %v", errs)
	}
	if !hasCode(errs, diag.UNDEFINED_VAR) {
		t.Fatalf("expected UNDEFINED_VAR code, got %v", errs)
	}
}

func TestScopePassDuplicateVariable(t *testing.T) {
	source := `
        function main() {
            var x = 1;
            var x = 2;
        }
        `
	errs, _ := errorDiagnostics(t, source)
	if !hasMessage(errs, "Duplicate") {
		t.Fatalf("expected a Duplicate diagnostic, got %v", errs)
	}
	if !hasCode(errs, diag.DUPLICATE_DEF) {
		t.Fatalf("expected DUPLICATE_DEF code, got %v", errs)
	}
}

func TestScopePassFunctionForwardRef(t *testing.T) {
	source := `
        function greet() { }
        function main() {
            greet();
        }
        `
	errs, snap := errorDiagnostics(t, source)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	// Both functions must be bound, and the greet() call must target the
	// user function (forward reference resolves to a FunctionID, not a
	// BuiltinId).
	if snap.Functions().Len() != 2 {
		t.Fatalf("Functions().Len() = %d, want 2 (greet, main)", snap.Functions().Len())
	}
	userCalls := 0
	snap.CallTargets().Range(func(_ AstNodeID, tgt CallTarget) bool {
		if tgt.Kind == CallFunction {
			userCalls++
		}
		return true
	})
	if userCalls != 1 {
		t.Fatalf("user-function call targets = %d, want 1 (greet)", userCalls)
	}
}

func TestScopePassBuiltinVisible(t *testing.T) {
	source := `
        function main() {
            var x = llm("gpt-4", {});
        }
        `
	errs, _ := errorDiagnostics(t, source)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

func TestScopePassForLoopVariableScoped(t *testing.T) {
	source := `
        function main() {
            for (var item in items) {
                output(item);
            }
        }
        `
	errs, _ := errorDiagnostics(t, source)
	// 'items' is undefined (not passed as input), but 'item' inside the loop
	// must be defined: no "Undefined variable: 'item'" diagnostic.
	if hasMessage(errs, "'item'") && hasMessage(errs, "Undefined") {
		t.Fatalf("loop variable should be in scope inside the loop, got %v", errs)
	}
	if !hasMessage(errs, "'items'") {
		t.Fatalf("expected 'items' to be reported undefined, got %v", errs)
	}
}

func TestScopePassShadowOuterVariable(t *testing.T) {
	source := `
        function main() {
            var x = 1;
            if (true) {
                var x = 2;
            }
        }
        `
	errs, _ := errorDiagnostics(t, source)
	// Inner x is in a BLOCK scope, outer x in the ENTRY scope: shadowing
	// across scopes is allowed, so no Duplicate diagnostic.
	if hasMessage(errs, "Duplicate") {
		t.Fatalf("shadowing across scopes must be allowed, got %v", errs)
	}
}

// ── TestSymbol (test_dsl_symbol_table.py) ──────────────────────────────

func TestSymbolCreation(t *testing.T) {
	sym := NewSymbol("x", SymbolVariable, types.IntType(), true, false)
	if sym.Name != "x" {
		t.Errorf("name = %q, want %q", sym.Name, "x")
	}
	if sym.Kind != SymbolVariable {
		t.Errorf("kind = %v, want VARIABLE", sym.Kind)
	}
	if sym.TypeInfo.Kind != types.Int {
		t.Errorf("type base = %v, want INT", sym.TypeInfo.Kind)
	}
}

func TestSymbolRepr(t *testing.T) {
	sym := NewSymbol("x", SymbolVariable, types.AnyType(), true, false)
	if !strings.Contains(sym.String(), "x") {
		t.Errorf("repr %q should contain the symbol name", sym.String())
	}
}

// ── TestDSLBuiltinRegistry (test_dsl_symbol_table.py) ──────────────────

//@feature empty-check — language builtin `empty(any) -> bool`; platform lowering
// (op 9/10, native vs shim) is locked by the backend tests in this repository
// and in the commercial backends.

func TestRegisterAll(t *testing.T) {
	chain := NewChain()
	RegisterBuiltins(chain)

	llm := chain.ResolveSymbol("llm")
	if llm == nil {
		t.Fatal("llm not registered")
	}
	if !llm.IsBuiltin {
		t.Errorf("llm should be a builtin")
	}
	if llm.Kind != SymbolBuiltinFunction {
		t.Errorf("llm kind = %v, want BUILTIN_FUNCTION", llm.Kind)
	}

	// TOOL-DIRECT-CALL-FORM decision 5 (Task 5): the tool() builtin is
	// deleted with zero compatibility — the qualified p.op({...}) form is
	// the only capability-call surface, so `tool` must NOT resolve.
	if got := chain.ResolveSymbol("tool"); got != nil {
		t.Errorf("tool should not be a registered builtin after deletion, got %v", got)
	}
	// A12-H17 (2026-09-17 用户裁定 (a)): `print` is deleted with zero
	// compatibility — a debug helper whose "use" was silently dropped by the
	// IR statement dispatch (compile SUCCEEDED, nothing in the product).
	// Output is expressed with `output(...)`.
	if got := chain.ResolveSymbol("print"); got != nil {
		t.Errorf("print should not be a registered builtin after deletion, got %v", got)
	}
	if chain.ResolveSymbol("output") == nil {
		t.Errorf("output not registered")
	}
	if empty := chain.ResolveSymbol("empty"); empty == nil || !empty.IsBuiltin {
		t.Errorf("empty builtin not registered (EMPTY-CHECK-DESIGN)")
	}
}

func TestBuiltinTypes(t *testing.T) {
	chain := NewChain()
	RegisterBuiltins(chain)

	llm := chain.ResolveSymbol("llm")
	if llm == nil || llm.TypeInfo.Kind != types.Function {
		t.Errorf("llm type = %v, want FUNCTION", llm)
	}
	length := chain.ResolveSymbol("len")
	if length == nil || length.TypeInfo.Kind != types.Function {
		t.Errorf("len type = %v, want FUNCTION", length)
	}
}

func TestBuiltinsNotOverwritten(t *testing.T) {
	chain := NewChain()
	RegisterBuiltins(chain)
	RegisterBuiltins(chain) // registering again must not fail or overwrite
	if chain.ResolveSymbol("llm") == nil {
		t.Errorf("llm lost after second registration")
	}
}

// ── determinism regression (task brief, Task 9 watch item) ─────────────

func TestBindDeterministicSameProgramTwice(t *testing.T) {
	prog := parseSource(t, `
        function main(string query) {
            var x = query;
            if (x != "") { output(x); }
            for (var item in split(x, ",")) { output(item); }
        }
        function helper(string a, string b) { return a + b; }
        `)
	snap1, diags1 := Bind(prog)
	if snap1.Declarations().Len() == 0 || snap1.AstNodes().Len() == 0 ||
		snap1.Scopes().Len() == 0 || snap1.Functions().Len() == 0 {
		t.Fatalf("snapshot maps must be populated before determinism can be meaningful")
	}
	snap2, diags2 := Bind(prog)
	if fingerprint(snap1) != fingerprint(snap2) {
		t.Fatalf("two Bind runs over the same program produced different snapshots")
	}
	if len(diags1) != len(diags2) {
		t.Fatalf("diagnostic count differs across runs: %d vs %d", len(diags1), len(diags2))
	}
	for i := range diags1 {
		if diags1[i].Code != diags2[i].Code || diags1[i].Message != diags2[i].Message {
			t.Fatalf("diagnostic %d differs across runs: %v vs %v", i, diags1[i], diags2[i])
		}
	}
}

func TestBindDeterministicFreshPrograms(t *testing.T) {
	source := `
        function main(string query) {
            var x = query;
            var y = x + "!";
            output(y);
        }
        function helper(string a, string b) { return a + b; }
        `
	// Two separately parsed programs: ast.NodeID counters differ between the
	// parses, so identical snapshots prove AstNodeID assignment is a
	// per-compile traversal enumeration, not the ast.NodeID counter.
	snap1, _ := Bind(parseSource(t, source))
	snap2, _ := Bind(parseSource(t, source))
	if fingerprint(snap1) != fingerprint(snap2) {
		t.Fatalf("freshly parsed identical programs produced different snapshots")
	}
}

func TestAstNodeIDsAreContiguousTraversalOrdinals(t *testing.T) {
	prog := parseSource(t, `function main() { var x = 1; }`)
	snap, _ := Bind(prog)
	keys := snap.AstNodes().Keys()
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	if len(keys) == 0 {
		t.Fatal("ast_nodes map is empty")
	}
	for i, k := range keys {
		if want := AstNodeID(i + 1); k != want {
			t.Fatalf("ast node id at position %d = %d, want contiguous %d", i, k, want)
		}
	}
}

// ── R1 review regressions ─────────────────────────────────────────────
//
// R1-Important-1: types.py type_ref_to_descriptor (line 513) recurses
// through array/map/optional carrying the diagnostics list, so an unknown
// named type nested inside a composite type reference reports UNKNOWN_TYPE
// (1015) at the named ref's own span. The pre-fix binder only checked the
// top-level ref and silently recovered to ANY for nested unknowns.

func TestUnknownTypeNestedInTypeRefs(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   int
	}{
		{
			name:   "record-field",
			source: `type A = { array<Missing> f; } function main() { }`,
			want:   1,
		},
		{
			name: "conversation-var",
			source: `@mode advanced-chat
@conversation array<Missing> memo;
function main() { }`,
			want: 1,
		},
		{
			name:   "var-decl",
			source: `function main() { array<Missing> x = []; }`,
			want:   1,
		},
		{
			name:   "function-param",
			source: `function main(array<Missing> x) { }`,
			want:   1,
		},
		{
			name:   "map-value",
			source: `type A = { map<string, Missing> m; } function main() { }`,
			want:   1,
		},
		{
			name:   "deep-nesting",
			source: `function main() { array<array<Missing>> x = []; }`,
			want:   1,
		},
		{
			name:   "known-types-clean",
			source: `type A = { array<int> f; } function main() { array<int> x = []; }`,
			want:   0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs, _ := errorDiagnostics(t, tc.source)
			if len(errs) != tc.want {
				t.Fatalf("got %d diagnostics, want %d: %v", len(errs), tc.want, errs)
			}
			if tc.want > 0 {
				if !hasCode(errs, diag.UNKNOWN_TYPE) {
					t.Fatalf("expected UNKNOWN_TYPE (1015), got %v", errs)
				}
				if !hasMessage(errs, "Unknown type: 'Missing'") {
					t.Fatalf("missing unknown-type message, got %v", errs)
				}
			}
		})
	}
}

// R1-Important-2: the parallel-export join iterated a Go map for conflict
// names, so with two or more conflicting exports the E1035 diagnostic order
// flipped between Bind runs. Python iterates a dict (insertion order = first
// appearance in branch walk order). The join must emit in that same stable
// order; this test repeats Bind many times and asserts both the message
// content (Minor: compact Type(kind) rendering) and the x-before-y order.
func TestParallelExportConflictDeterministic(t *testing.T) {
	source := `
        function main() {
            parallel {
                { int x = 1; string y = "a"; }
                { string x = "b"; int y = 2; }
            }
        }
        `
	for i := 0; i < 30; i++ {
		errs, _ := errorDiagnostics(t, source)
		if len(errs) != 2 {
			t.Fatalf("run %d: got %d diagnostics, want 2: %v", i, len(errs), errs)
		}
		for _, d := range errs {
			if d.Code != diag.PARALLEL_EXPORT_CONFLICT {
				t.Fatalf("run %d: unexpected code %d, want 1035: %v", i, d.Code, errs)
			}
		}
		// typesStr is sorted, so both int/string conflicts render
		// "Type(int), Type(string)" — the distinguishing feature is the
		// variable name and its position (insertion order: x before y).
		if !strings.HasPrefix(errs[0].Message, "Variable 'x' is exported from multiple parallel branches with incompatible types: Type(int), Type(string)") {
			t.Fatalf("run %d: first conflict must be x (insertion order), got: %q", i, errs[0].Message)
		}
		if !strings.HasPrefix(errs[1].Message, "Variable 'y' is exported from multiple parallel branches with incompatible types: Type(int), Type(string)") {
			t.Fatalf("run %d: second conflict must be y, got: %q", i, errs[1].Message)
		}
	}
}

// R1-Minor-3: regression lock for the stdlib call path. A qualified
// std.v1.rag_answer(...) call must map to a CallStdlib target carrying the
// full qualified name (previously only covered by manual parity runs).
func TestStdlibCallPathTarget(t *testing.T) {
	_, snap := errorDiagnostics(t, `function main() { var x = std.v1.rag_answer("q"); }`)
	found := false
	snap.CallTargets().Range(func(id AstNodeID, ct CallTarget) bool {
		if ct.Kind == CallStdlib && ct.Name == "std.v1.rag_answer" {
			found = true
		}
		return true
	})
	if !found {
		t.Fatalf("no StdlibCallTarget(std.v1.rag_answer) in call_targets: %v", dumpCallTargets(snap))
	}
}

func dumpCallTargets(s *Snapshot) string {
	var b strings.Builder
	s.CallTargets().Range(func(id AstNodeID, ct CallTarget) bool {
		fmt.Fprintf(&b, "[%d] kind=%d name=%q function=%d\n", id, ct.Kind, ct.Name, ct.Function)
		return true
	})
	return b.String()
}

// ── TOOL-DIRECT-CALL-FORM task 1: package namespaces ──────────────────

// A variable declared with an imported package namespace name is a compile
// error (SHADOWED_NAMESPACE) — decision 6 of TOOL-DIRECT-CALL-FORM: no
// silent preference between the namespace and the variable.
func TestNamespaceShadowingError(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
import "dify.tts";
function main() {
    let tts = 1;
    return "ok";
}`
	prog, perrs := parser.ParseSource(src, "shadow.ncoda")
	if len(perrs) > 0 {
		t.Fatalf("parse: %v", perrs[0].Message)
	}
	_, diags := Bind(prog)
	found := false
	for _, d := range diags {
		if d.Code == diag.SHADOWED_NAMESPACE {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected SHADOWED_NAMESPACE, got %v", diags)
	}
}

// Import declarations register a SymbolNamespace in the global scope: the
// snapshot must declare a namespace symbol named after the provider,
// mirroring the std namespace registration.
func TestNamespaceSymbolRegistered(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
import "dify.tts";
function main() { return "ok"; }`
	prog, _ := parser.ParseSource(src, "ns.ncoda")
	snap, diags := Bind(prog)
	if len(diags) > 0 {
		t.Fatalf("unexpected diags: %v", diags)
	}
	found := false
	snap.Declarations().Range(func(id SymbolID, d SymbolDecl) bool {
		if d.Kind == SymbolNamespace && d.Name == "tts" {
			found = true
		}
		return true
	})
	if !found {
		t.Fatalf("expected namespace declaration for tts in snapshot")
	}
}

// Every variable-declaration path must reject a name that collides with an
// imported package namespace: let/var, function parameters, loop variables
// and parallel-branch lets (parallel lets flow through visitVarDeclStmt).
func TestNamespaceShadowingAllDeclarationPaths(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"let", `@language nodecoda/1
@mode workflow
import "dify.tts";
function main() { let tts = 1; return "ok"; }`},
		{"var", `@language nodecoda/1
@mode workflow
import "dify.tts";
function main() { var tts = 1; return "ok"; }`},
		{"param", `@language nodecoda/1
@mode workflow
import "dify.tts";
function helper(string tts) { return "ok"; }
function main() { return helper("x"); }`},
		{"entry-input", `@language nodecoda/1
@mode workflow
import "dify.tts";
function main(string tts) { return "ok"; }`},
		{"loop-var", `@language nodecoda/1
@mode workflow
import "dify.tts";
function main() { for (var tts in items) { } return "ok"; }`},
		{"parallel-let", `@language nodecoda/1
@mode workflow
import "dify.tts";
function main() { parallel { { let tts = 1; } { } } return "ok"; }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs, _ := errorDiagnostics(t, tc.src)
			if !hasCode(errs, diag.SHADOWED_NAMESPACE) {
				t.Fatalf("expected SHADOWED_NAMESPACE, got %v", errs)
			}
		})
	}
}

// request_input result bindings declare a variable in the human-input
// action scope, so they must get the same namespace shadow check as
// let/var/params (TOOL-DIRECT-CALL-FORM decision 6).
func TestRequestInputResultBindingNamespaceShadow(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
import "dify.tts";
enum ReviewAction { approve }
type ReviewForm = { string comment; }
function main() -> string {
    request_input<ReviewForm, ReviewAction>({
        content: "Review",
        fields: { comment: { kind: "paragraph" } },
        actions: { approve: { title: "Approve" } },
        timeout: { value: 3, unit: "day" }
    }) as tts {
        action ReviewAction.approve { return "ok"; }
        timeout { return "timed out"; }
    }
    return "ok";
}`
	errs, _ := errorDiagnostics(t, src)
	if !hasCode(errs, diag.SHADOWED_NAMESPACE) {
		t.Fatalf("expected SHADOWED_NAMESPACE for request_input result binding, got %v", errs)
	}
}

// The std namespace shadow error fires too (std is a namespace, no silent
// shadowing), but the message must not claim std came from an import: std
// is a builtin namespace, not an imported package.
func TestStdNamespaceShadowMessageAccurate(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main() {
    let std = 1;
    return "ok";
}`
	errs, _ := errorDiagnostics(t, src)
	found := false
	for _, d := range errs {
		if d.Code == diag.SHADOWED_NAMESPACE {
			found = true
			if strings.Contains(d.Message, `import "std"`) {
				t.Fatalf("std shadow message must not claim an import, got: %q", d.Message)
			}
			if !strings.Contains(d.Message, "shadows a namespace") {
				t.Fatalf("std shadow message must use the generic namespace wording, got: %q", d.Message)
			}
		}
	}
	if !found {
		t.Fatalf("expected SHADOWED_NAMESPACE for std, got %v", errs)
	}
}

// Function and type declarations do NOT shadow namespaces: they collide
// through the existing global-scope DUPLICATE_DEF/DUPLICATE_TYPE paths, not
// SHADOWED_NAMESPACE.
func TestNamespaceNotShadowedByFunctionOrType(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"function", `@language nodecoda/1
@mode workflow
import "dify.tts";
function tts() { return "ok"; }`},
		{"type", `@language nodecoda/1
@mode workflow
import "dify.tts";
type tts = { string s; }`},
		{"enum", `@language nodecoda/1
@mode workflow
import "dify.tts";
enum tts { a, b }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs, _ := errorDiagnostics(t, tc.src)
			if hasCode(errs, diag.SHADOWED_NAMESPACE) {
				t.Fatalf("%s declaration must not emit SHADOWED_NAMESPACE, got %v", tc.name, errs)
			}
			if !hasCode(errs, diag.DUPLICATE_DEF) && !hasCode(errs, diag.DUPLICATE_TYPE) {
				t.Fatalf("expected DUPLICATE_DEF or DUPLICATE_TYPE for %s colliding with namespace, got %v", tc.name, errs)
			}
		})
	}
}

// ── I2: explicit namespace discrimination (builtin vs import) ─────────

// An import whose provider collides with a builtin namespace (std, json)
// is an explicit DUPLICATE_DEF error — the parser already rejects
// duplicate imports, so this is the only silent-swallow surface left.
// defineIfFree would return the builtin namespace and the type pass would
// then route the name through the builtin record type while the IR
// import-namespace set still contained it (divergent lowering); rejecting
// it keeps the three discriminants agreeing by construction.
func TestImportConflictsWithBuiltinNamespace(t *testing.T) {
	for _, provider := range []string{"dify.std", "dify.json"} {
		t.Run(provider, func(t *testing.T) {
			src := "@language nodecoda/1\n@mode workflow\nimport \"" + provider + "\";\nfunction main() { return \"ok\"; }"
			errs, _ := errorDiagnostics(t, src)
			if !hasCode(errs, diag.DUPLICATE_DEF) {
				t.Fatalf("expected DUPLICATE_DEF for import %q colliding with builtin namespace, got %v", provider, errs)
			}
			if !hasMessage(errs, "conflicts with the builtin namespace") {
				t.Fatalf("expected builtin-conflict message, got %v", errs)
			}
		})
	}
}

// The builtin std/json namespaces carry NamespaceBuiltin; an import
// provider carries NamespaceImport. The explicit kind is what the type
// pass consults instead of the Type.Kind == Any heuristic.
func TestNamespaceSymbolKindDiscriminatesBuiltinVsImport(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
import "dify.tts";
function main() { return "ok"; }`
	errs, snap := errorDiagnostics(t, src)
	if len(errs) > 0 {
		t.Fatalf("unexpected diags: %v", errs)
	}
	kindOf := func(name string) (NamespaceKind, bool) {
		found := false
		var kind NamespaceKind
		snap.Declarations().Range(func(id SymbolID, d SymbolDecl) bool {
			if d.Kind == SymbolNamespace && d.Name == name {
				kind = d.NS
				found = true
				return false
			}
			return true
		})
		return kind, found
	}
	for _, name := range []string{"std", "json"} {
		kind, found := kindOf(name)
		if !found {
			t.Fatalf("builtin namespace %q missing from snapshot declarations", name)
		}
		if kind != NamespaceBuiltin {
			t.Errorf("namespace %q NS = %v, want NamespaceBuiltin", name, kind)
		}
	}
	kind, found := kindOf("tts")
	if !found {
		t.Fatal("import namespace tts missing from snapshot declarations")
	}
	if kind != NamespaceImport {
		t.Errorf("import namespace tts NS = %v, want NamespaceImport", kind)
	}
}

// Symbol spec v1 (docs/SYMBOL-SPEC.md): a bare import is legal — the
// platform is a build-time concern (--target selects the binding), not a
// language concept. Unknown qualifiers are still rejected so a typo cannot
// silently misroute bindings.
func TestBareImportIsLegal(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
import "tts";
function main() { return tts.say("ok"); }`
	prog, perrs := parser.ParseSource(src, "bare.ncoda")
	if len(perrs) > 0 {
		t.Fatalf("parse: %v", perrs[0].Message)
	}
	_, diags := Bind(prog)
	for _, d := range diags {
		if d.Code == diag.IMPORT_PLATFORM_REQUIRED || d.Code == diag.UNDEFINED_VAR {
			t.Fatalf("bare import must be legal, got: %v", d)
		}
	}
}

func TestUnknownImportPlatformRejected(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
import "windows.tts";
function main() { return "ok"; }`
	errs, _ := errorDiagnostics(t, src)
	if !hasCode(errs, diag.IMPORT_PLATFORM_REQUIRED) {
		t.Fatalf("expected IMPORT_PLATFORM_REQUIRED for unknown platform, got %v", errs)
	}
	if !hasMessage(errs, "unknown import platform") {
		t.Fatalf("expected unknown-platform message, got %v", errs)
	}
}

func TestPlatformQualifiedImportRegistersBareNamespace(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
import "coze-biz.tts";
function main() { return tts.say("ok"); }`
	prog, perrs := parser.ParseSource(src, "plat.ncoda")
	if len(perrs) > 0 {
		t.Fatalf("parse: %v", perrs[0].Message)
	}
	_, diags := Bind(prog)
	for _, d := range diags {
		if d.Code == diag.UNDEFINED_VAR || d.Code == diag.IMPORT_PLATFORM_REQUIRED {
			t.Fatalf("unexpected diag: %v", d)
		}
	}
}
