package passes

// Ports of lang/tests/lang/unit/test_semantic_pass.py (201 lines) plus
// regression/test_stdlib_call_context.py (E1032) and
// unit/test_stdlib_expander.py E1031 coverage. The main harness mirrors
// run_all_passes (test_semantic_pass.py:21-32): scope → type → syntax →
// semantic with the reference short-circuit semantics.
//
// D1 (stdlib checks): the Python reference reaches E1031/E1032 through the
// full pipeline because its TypePass resolves stdlib calls to their spec
// function types (type_pass.py:1201-1206). The Go TypePass does not port
// the stdlib registry yet (recorded deviation in type_infer.go), so a
// std.* call would fail there with ANY_NOT_NARROWED before semantic runs.
// The E1031/E1032 tests therefore exercise the semantic pass directly on a
// scope-bound program (scope → semantic), which is exactly the surface the
// semantic pass itself owns.

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/diag"
)

// runSemanticHarness parses src and runs scope → type → syntax → semantic
// with the reference short-circuit semantics (test_semantic_pass.py:21-32).
func runSemanticHarness(t *testing.T, src string) *Context {
	t.Helper()
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	if ctx.HasErrors() {
		return ctx
	}
	(&TypePass{}).Run(prog, ctx)
	if ctx.HasErrors() {
		return ctx
	}
	(&SyntaxPass{}).Run(prog, ctx)
	if ctx.HasErrors() {
		return ctx
	}
	(&SemanticPass{}).Run(prog, ctx)
	return ctx
}

func semanticByCode(ctx *Context, code diag.Code) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range ctx.Diagnostics {
		if d.Code == code {
			out = append(out, d)
		}
	}
	return out
}

func TestSemanticNoEntry(t *testing.T) {
	ctx := runSemanticHarness(t, "function f() { }")
	errs := semanticByCode(ctx, diag.NO_ENTRY)
	if len(errs) != 1 {
		t.Fatalf("want one E1009, got %v", ctx.Diagnostics)
	}
	if !strings.Contains(strings.ToLower(errs[0].Message), "main") {
		t.Fatalf("message = %q, want it to mention main", errs[0].Message)
	}
}

func TestSemanticValidProgram(t *testing.T) {
	const src = `
function main(string query) {
    var x = query;
    output(x);
}
`
	ctx := runSemanticHarness(t, src)
	if errs := ctx.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

func TestSemanticBreakOutsideLoopErrors(t *testing.T) {
	ctx := runSemanticHarness(t, "function main() { break; }")
	errs := semanticByCode(ctx, diag.NOT_IN_LOOP)
	if len(errs) != 1 {
		t.Fatalf("want one E1016, got %v", ctx.Diagnostics)
	}
	if !strings.Contains(errs[0].Message, "break") {
		t.Fatalf("message = %q, want it to mention break", errs[0].Message)
	}
	if errs[0].Message != "'break' outside of loop" {
		t.Fatalf("message = %q, want %q", errs[0].Message, "'break' outside of loop")
	}
}

func TestSemanticContinueOutsideLoopErrors(t *testing.T) {
	ctx := runSemanticHarness(t, "function main() { continue; }")
	errs := semanticByCode(ctx, diag.NOT_IN_LOOP)
	if len(errs) != 1 {
		t.Fatalf("want one E1016, got %v", ctx.Diagnostics)
	}
	if !strings.Contains(errs[0].Message, "continue") {
		t.Fatalf("message = %q, want it to mention continue", errs[0].Message)
	}
	if errs[0].Message != "'continue' outside of loop" {
		t.Fatalf("message = %q, want %q", errs[0].Message, "'continue' outside of loop")
	}
}

func TestSemanticBreakInsideLoopIsLegal(t *testing.T) {
	const src = `
function main(int[] items) {
    for (x in items) {
        if (x > 5) {
            break;
        }
    }
}
`
	ctx := runSemanticHarness(t, src)
	if errs := semanticByCode(ctx, diag.NOT_IN_LOOP); len(errs) != 0 {
		t.Fatalf("break inside a loop must be legal: %v", errs)
	}
}

func TestSemanticAssignToImmutableErrors(t *testing.T) {
	ctx := runSemanticHarness(t, "function main(int x) { x = 42; }")
	errs := semanticByCode(ctx, diag.ASSIGN_IMMUTABLE)
	if len(errs) != 1 {
		t.Fatalf("want one E1018, got %v", ctx.Diagnostics)
	}
	want := "Cannot assign to immutable variable 'x'"
	if errs[0].Message != want {
		t.Fatalf("message = %q, want %q", errs[0].Message, want)
	}
}

func TestSemanticAssignToMutableIsLegal(t *testing.T) {
	const src = `
function main() {
    var x = 1;
    x = 42;
}
`
	ctx := runSemanticHarness(t, src)
	if errs := semanticByCode(ctx, diag.ASSIGN_IMMUTABLE); len(errs) != 0 {
		t.Fatalf("assigning to a mutable var must be legal: %v", errs)
	}
}

func TestSemanticUnusedVarWarning(t *testing.T) {
	const src = `
function main() {
    var unused = 42;
    var used = 1;
    output(used);
}
`
	ctx := runSemanticHarness(t, src)
	warnings := semanticByCode(ctx, diag.UNUSED_VAR)
	if len(warnings) != 1 {
		t.Fatalf("want one W2002, got %v", ctx.Diagnostics)
	}
	want := "Variable 'unused' is declared but never used"
	if warnings[0].Message != want {
		t.Fatalf("message = %q, want %q", warnings[0].Message, want)
	}
}

func TestSemanticEmptyEntryWarning(t *testing.T) {
	ctx := runSemanticHarness(t, "function main() { }")
	warnings := semanticByCode(ctx, diag.EMPTY_ENTRY)
	if len(warnings) != 1 {
		t.Fatalf("want one W2003, got %v", ctx.Diagnostics)
	}
	if warnings[0].Message != "Main function is empty" {
		t.Fatalf("message = %q", warnings[0].Message)
	}
}

func TestSemanticInvalidModeErrors(t *testing.T) {
	// The parser rejects unknown @mode values at parse time (parser.go:
	// 90-93), so this check is exercised on a constructed program — the
	// exact path the Python semantic pass defends (semantic_pass.py:36-41).
	prog := ast.NewProgram(
		ast.WithMode("bogus"),
		ast.WithEntry(ast.NewFunctionDef("main", nil, nil, ast.NewBlock(nil))),
	)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	(&SemanticPass{}).Run(prog, ctx)
	errs := semanticByCode(ctx, diag.INVALID_MODE)
	if len(errs) != 1 {
		t.Fatalf("want one E1019, got %v", ctx.Diagnostics)
	}
	want := "Unknown @mode 'bogus'. Expected: advanced-chat, workflow, agent"
	if errs[0].Message != want {
		t.Fatalf("message = %q, want %q", errs[0].Message, want)
	}
}

func TestSemanticStdlibStaticArgumentErrors(t *testing.T) {
	const src = `
function main(string query, string model) {
    let response = std.v1.rag_answer(query, "dataset", model);
    return response;
}
`
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("bind errors: %v", ctx.Diagnostics)
	}
	(&SemanticPass{}).Run(prog, ctx)
	errs := semanticByCode(ctx, diag.STDLIB_STATIC_ARGUMENT)
	if len(errs) != 1 {
		t.Fatalf("want one E1031, got %v", ctx.Diagnostics)
	}
	want := "std.v1.rag_answer parameter 'model' must be a string literal"
	if errs[0].Message != want {
		t.Fatalf("message = %q, want %q", errs[0].Message, want)
	}
	// The diagnostic sits on the offending argument (the model identifier
	// on line 3), mirroring test_stdlib_expander.py:84-96.
	if errs[0].Line != 3 {
		t.Fatalf("E1031 line = %d, want 3 (the model argument)", errs[0].Line)
	}
}

func TestSemanticStdlibStaticArgumentLiteralPasses(t *testing.T) {
	const src = `
function main(string query) {
    let response = std.v1.rag_answer(query, "dataset", "openai/gpt-4o");
    return response;
}
`
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("bind errors: %v", ctx.Diagnostics)
	}
	(&SemanticPass{}).Run(prog, ctx)
	if errs := semanticByCode(ctx, diag.STDLIB_STATIC_ARGUMENT); len(errs) != 0 {
		t.Fatalf("literal static args must pass: %v", errs)
	}
}

func TestSemanticNestedStdlibCallIsRejected(t *testing.T) {
	const src = `
function main(string query) -> string {
    let response = llm("openai/gpt-4o", {
        "prompt": std.v1.rag_answer(query, "dataset", "openai/gpt-4o")
    });
    return response.text;
}
`
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("bind errors: %v", ctx.Diagnostics)
	}
	(&SemanticPass{}).Run(prog, ctx)
	errs := semanticByCode(ctx, diag.STDLIB_CALL_CONTEXT)
	if len(errs) != 1 {
		t.Fatalf("want one E1032, got %v", ctx.Diagnostics)
	}
	want := "std.v1.rag_answer must be a variable initializer, expression statement, or direct return"
	if errs[0].Message != want {
		t.Fatalf("message = %q, want %q", errs[0].Message, want)
	}
}

func TestSemanticStdlibCallAsVarInitializerIsLegal(t *testing.T) {
	const src = `
function main(string query) {
    let response = std.v1.rag_answer(query, "dataset", "openai/gpt-4o");
    return response;
}
`
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("bind errors: %v", ctx.Diagnostics)
	}
	(&SemanticPass{}).Run(prog, ctx)
	if errs := semanticByCode(ctx, diag.STDLIB_CALL_CONTEXT); len(errs) != 0 {
		t.Fatalf("stdlib call as var initializer must be legal: %v", errs)
	}
}

func TestSemanticStdlibCallAsDirectReturnIsLegal(t *testing.T) {
	const src = `
function main(string url) -> string {
    return std.v1.fetch_and_summarize(url, "openai/gpt-4o");
}
`
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	if ctx.HasErrors() {
		t.Fatalf("bind errors: %v", ctx.Diagnostics)
	}
	(&SemanticPass{}).Run(prog, ctx)
	if errs := semanticByCode(ctx, diag.STDLIB_CALL_CONTEXT); len(errs) != 0 {
		t.Fatalf("stdlib call as direct return must be legal: %v", errs)
	}
}

func TestBreakInsideNestedLoopIsAllowed(t *testing.T) {
	// _BreakContinueCheck: break is legal at any loop nesting depth; a break
	// inside a for nested in a while must not emit NOT_IN_LOOP.
	const src = `function main(int[] items) {
    while (true) limit 10 {
        for (var item in items) {
            break;
        }
    }
}
`
	ctx := runSemanticHarness(t, src)
	for _, d := range ctx.Diagnostics {
		if d.Code == diag.NOT_IN_LOOP {
			t.Fatalf("unexpected NOT_IN_LOOP: %v", d)
		}
	}
}

// TestSemanticParallelForExitBackstop —— 语义阶段的 break/continue 兜底面
// （**D-L3(a)/S4，2026-09-14 用户拍板后的形态统一**）。
//
// 端到端（DefaultPipeline）上，并发迭代体内的 `break` 已由 parallel 阶段以
// `PARALLEL_EXTERNAL_EXIT`（E1037）先报（语言规则，与循环形态/嵌套深度无关），
// 本阶段的 NOT_IN_LOOP 只是**同义兜底**（本白盒harness 不跑 parallel 阶段）。
// 关键点是**形态同一把尺子**：`continue` 是迭代局部（结束本迭代）⇒ 在
// **表达式形态**（本测试，此前被 loopCtxParallel 拒）与**语句形态**
// （`parallel for (...) { }`，语义阶段一直按普通循环放行）都必须合法。
func TestSemanticParallelForExitBackstop(t *testing.T) {
	const breakSrc = `function main() {
    let items = [1, 2];
    var r = parallel for (x in items, concurrency: 2, on_error: terminate) {
        break;
    };
}`
	if errs := semanticByCode(runSemanticHarness(t, breakSrc), diag.NOT_IN_LOOP); len(errs) != 1 {
		t.Fatalf("break inside a parallel-for expression must keep the semantic backstop, got %d NOT_IN_LOOP", len(errs))
	}

	const continueSrc = `function main() {
    let items = [1, 2];
    var r = parallel for (x in items, concurrency: 2, on_error: terminate) {
        continue;
    };
}`
	if errs := semanticByCode(runSemanticHarness(t, continueSrc), diag.NOT_IN_LOOP); len(errs) != 0 {
		t.Fatalf("continue is iteration-local and must be legal in both forms, got %v", errs)
	}
}
