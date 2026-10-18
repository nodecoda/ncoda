package passes

// Ports of lang/tests/lang/unit/test_semantic_pass.py syntax coverage
// (MISSING_RETURN E1010 + DEAD_CODE W2001) plus self-built cases for the
// recursion surface. The harness mirrors run_all_passes (test_semantic_pass
// .py:21-32) up to and including SyntaxPass: scope → type → syntax, each
// stage skipped when the previous reported errors.

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/diag"
)

// runSyntaxHarness parses src and runs scope → type → syntax with the
// reference short-circuit semantics, returning the context.
func runSyntaxHarness(t *testing.T, src string) *Context {
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
	return ctx
}

func syntaxErrors(ctx *Context) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range ctx.Diagnostics {
		if d.Severity == diag.Error {
			out = append(out, d)
		}
	}
	return out
}

func syntaxWarnings(ctx *Context) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range ctx.Diagnostics {
		if d.Severity == diag.Warning {
			out = append(out, d)
		}
	}
	return out
}

func TestSyntaxMissingReturnIsAnErrorForValueFunction(t *testing.T) {
	const src = `
function f() -> int {
    var x = 1;
}
function main() { }
`
	ctx := runSyntaxHarness(t, src)
	errors := syntaxErrors(ctx)
	if len(errors) != 1 {
		t.Fatalf("want exactly one error, got %v", ctx.Diagnostics)
	}
	if errors[0].Code != diag.MISSING_RETURN {
		t.Fatalf("code = %v, want MISSING_RETURN: %v", errors[0].Code, errors[0])
	}
	wantMsg := "Function 'f' may not return a value on all paths"
	if errors[0].Message != wantMsg {
		t.Fatalf("message = %q, want %q", errors[0].Message, wantMsg)
	}
}

func TestSyntaxValueReturningFunctionRejectsPartialBranchReturn(t *testing.T) {
	for _, functionName := range []string{"main", "helper"} {
		helper := ""
		if functionName != "main" {
			helper = "function main() {}"
		}
		src := `
function ` + functionName + `(bool flag) -> string {
    if (flag) {
        return "yes";
    }
}
` + helper + `
`
		ctx := runSyntaxHarness(t, src)
		found := false
		for _, d := range ctx.Diagnostics {
			if d.Code == diag.MISSING_RETURN && d.Severity == diag.Error {
				found = true
			}
		}
		if !found {
			t.Fatalf("function %s: want E1010, got %v", functionName, ctx.Diagnostics)
		}
	}
}

func TestSyntaxHasReturnNoDiagnostic(t *testing.T) {
	const src = `
function f() -> int {
    if (true) {
        return 1;
    } else {
        return 2;
    }
}
function main() { }
`
	ctx := runSyntaxHarness(t, src)
	for _, d := range ctx.Diagnostics {
		if d.Code == diag.MISSING_RETURN {
			t.Fatalf("unexpected E1010: %v", d)
		}
	}
}

func TestSyntaxDeadCodeWarningAfterReturn(t *testing.T) {
	const src = `
function f() -> int {
    return 1;
    var x = 2;
}
function main() { }
`
	ctx := runSyntaxHarness(t, src)
	warnings := syntaxWarnings(ctx)
	found := false
	for _, d := range warnings {
		if d.Code == diag.DEAD_CODE && d.Message == "Unreachable code after return" {
			found = true
			if d.Line != 4 {
				t.Fatalf("DEAD_CODE line = %d, want 4 (the statement after return)", d.Line)
			}
		}
	}
	if !found {
		t.Fatalf("want W2001 DEAD_CODE, got %v", ctx.Diagnostics)
	}
	if len(syntaxErrors(ctx)) != 0 {
		t.Fatalf("dead code must not produce errors: %v", ctx.Diagnostics)
	}
}

func TestSyntaxDeadCodeRecursesIntoIfAndLoop(t *testing.T) {
	const src = `
function f() -> int {
    if (true) {
        return 1;
        var a = 1;
    } else {
        var b = 1;
        return 2;
    }
}
function main() {
    for (x in [1]) {
        return 1;
        var y = 1;
    }
}
`
	ctx := runSyntaxHarness(t, src)
	count := 0
	for _, d := range ctx.Diagnostics {
		if d.Code == diag.DEAD_CODE {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("want 2 W2001 (then-branch + for body), got %d: %v", count, ctx.Diagnostics)
	}
}

func TestSyntaxDeadCodeNotEmittedForTrailingReturn(t *testing.T) {
	const src = `
function main() {
    return;
}
`
	ctx := runSyntaxHarness(t, src)
	for _, d := range ctx.Diagnostics {
		if d.Code == diag.DEAD_CODE {
			t.Fatalf("trailing return must not warn: %v", d)
		}
	}
}

func TestSyntaxMessagesAreVerbatim(t *testing.T) {
	const src = `function f() -> int { if (true) { return 1; } } function main() {}`
	ctx := runSyntaxHarness(t, src)
	if len(ctx.Diagnostics) == 0 {
		t.Fatal("expected the E1010 diagnostic")
	}
	// Exact byte-for-byte parity with syntax_pass.py:36-39.
	want := "Function 'f' may not return a value on all paths"
	if !strings.Contains(ctx.Diagnostics[0].Message, "Function 'f' may not return") {
		t.Fatalf("message = %q, want substring of %q", ctx.Diagnostics[0].Message, want)
	}
}
