package passes

// Ports of lang/tests/lang/unit/test_scope_pass.py (TestScopePass, 7 cases)
// plus the E1033 recursion-rejection cases from
// lang/tests/lang/regression/test_function_call_correctness.py:1-32, run
// through ScopePass (thin wrapper over binding.Bind, Task 10) and, for the
// cycle cases, through the pipeline to prove the base.py:426-433 wiring.

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/diag"
)

func runScopePass(t *testing.T, src string) *Context {
	t.Helper()
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	return ctx
}

func errorMessages(ctx *Context) []string {
	var out []string
	for _, d := range ctx.Diagnostics {
		if d.Severity == diag.Error {
			out = append(out, d.Message)
		}
	}
	return out
}

func hasMessage(messages []string, substr string) bool {
	for _, m := range messages {
		if strings.Contains(m, substr) {
			return true
		}
	}
	return false
}

func TestScopePassNoErrorsSimple(t *testing.T) {
	ctx := runScopePass(t, `
        function main(string query) {
            var x = query;
            output(x);
        }
    `)
	if msgs := errorMessages(ctx); len(msgs) != 0 {
		t.Fatalf("unexpected errors: %v", msgs)
	}
}

func TestScopePassUndefinedVariable(t *testing.T) {
	ctx := runScopePass(t, `
        function main() {
            var x = y;
        }
    `)
	if !hasMessage(errorMessages(ctx), "Undefined variable") {
		t.Fatalf("want an undefined-variable error, got %v", ctx.Diagnostics)
	}
}

func TestScopePassDuplicateVariable(t *testing.T) {
	ctx := runScopePass(t, `
        function main() {
            var x = 1;
            var x = 2;
        }
    `)
	if !hasMessage(errorMessages(ctx), "Duplicate") {
		t.Fatalf("want a duplicate-declaration error, got %v", ctx.Diagnostics)
	}
}

func TestScopePassFunctionForwardRef(t *testing.T) {
	ctx := runScopePass(t, `
        function greet() { }
        function main() {
            greet();
        }
    `)
	if msgs := errorMessages(ctx); len(msgs) != 0 {
		t.Fatalf("unexpected errors: %v", msgs)
	}
}

func TestScopePassBuiltinVisible(t *testing.T) {
	ctx := runScopePass(t, `
        function main() {
            var x = llm("gpt-4", {});
        }
    `)
	if msgs := errorMessages(ctx); len(msgs) != 0 {
		t.Fatalf("unexpected errors: %v", msgs)
	}
}

func TestScopePassForLoopVariableScoped(t *testing.T) {
	ctx := runScopePass(t, `
        function main() {
            for (var item in items) {
                output(item);
            }
        }
    `)
	// 'items' is undefined (not passed as input), but 'item' inside the loop
	// must be defined; no error may mention 'item'.
	for _, d := range ctx.Diagnostics {
		if d.Severity == diag.Error && strings.Contains(d.Message, "'item'") && strings.Contains(d.Message, "Undefined") {
			t.Fatalf("loop variable 'item' reported undefined: %v", d)
		}
	}
}

func TestScopePassShadowOuterVariable(t *testing.T) {
	ctx := runScopePass(t, `
        function main() {
            var x = 1;
            if (true) {
                var x = 2;
            }
        }
    `)
	// Inner x lives in a BLOCK scope, outer x in the ENTRY scope:
	// shadowing across scopes is allowed, so no "Duplicate" error.
	if hasMessage(errorMessages(ctx), "Duplicate") {
		t.Fatalf("shadowing across scopes must not be a duplicate, got %v", ctx.Diagnostics)
	}
}

// ── E1033 recursive call cycle (regression/test_function_call_correctness.py) ──

func runPipelineWithScope(t *testing.T, src string) *FrontendResult {
	t.Helper()
	p := New()
	return p.Run(src, "recursive.ncoda", nil)
}

func TestScopePassDirectRecursionIsOneValidationDiagnostic(t *testing.T) {
	res := runPipelineWithScope(t,
		"function a() -> string { return a(); } function main() -> string { return a(); }")
	if res.Program == nil || res.Program.Snapshot == nil {
		t.Fatal("E1033 must still publish a snapshot and typed program")
	}
	if len(res.Errors()) != 1 {
		t.Fatalf("want exactly one error, got %v", res.Diagnostics)
	}
	if res.Errors()[0].Code != diag.RECURSIVE_CALL {
		t.Fatalf("code = %v, want RECURSIVE_CALL (E1033)", res.Errors()[0].Code)
	}
	if !strings.Contains(res.Errors()[0].Message, "a -> a") {
		t.Fatalf("message = %q, want cycle path 'a -> a'", res.Errors()[0].Message)
	}
	if res.Errors()[0].Line < 1 || res.Errors()[0].Column < 1 {
		t.Fatalf("E1033 must point at the cycle call site: %+v", res.Errors()[0])
	}
}

func TestScopePassIndirectRecursionIsOneValidationDiagnostic(t *testing.T) {
	res := runPipelineWithScope(t,
		"function a() -> string { return b(); } function b() -> string { return a(); } "+
			"function main() -> string { return a(); }")
	if res.Program == nil || res.Program.Snapshot == nil {
		t.Fatal("E1033 must still publish a snapshot and typed program")
	}
	if len(res.Errors()) != 1 {
		t.Fatalf("want exactly one error, got %v", res.Diagnostics)
	}
	if res.Errors()[0].Code != diag.RECURSIVE_CALL {
		t.Fatalf("code = %v, want RECURSIVE_CALL (E1033)", res.Errors()[0].Code)
	}
	if !strings.Contains(res.Errors()[0].Message, "a -> b -> a") {
		t.Fatalf("message = %q, want cycle path 'a -> b -> a'", res.Errors()[0].Message)
	}
}
