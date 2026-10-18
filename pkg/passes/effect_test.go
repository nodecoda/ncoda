package passes

// Ports of lang/tests/lang/unit/test_effect_pass.py (3 cases) plus
// self-built coverage for the E1041 rejection paths and the RoMap
// immutability contract (MappingProxyType analogue).

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
)

func runEffect(t *testing.T, src string) *Context {
	t.Helper()
	ctx, _ := runEffectWithProgram(t, src)
	return ctx
}

// runEffectWithProgram runs the scope + effect passes over ONE parse of src
// and returns the context plus the program, so tests can look up nodes in
// the same snapshot the passes saw (a second parse would mint fresh AST
// nodes that were never enumerated).
func runEffectWithProgram(t *testing.T, src string) (*Context, *ast.Program) {
	t.Helper()
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	EffectPass{}.Run(prog, ctx)
	return ctx, prog
}

func effectErrors(ctx *Context) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range ctx.Diagnostics {
		if d.Severity == diag.Error && d.Code == diag.EFFECT_CONTEXT {
			out = append(out, d)
		}
	}
	return out
}

// matchVisitor collects the first node satisfying pred and prunes the rest
// of the walk; it returns nil (prune) after the match.
type matchVisitor struct {
	pred  func(n ast.Node) bool
	found ast.Node
}

func (v *matchVisitor) Visit(n ast.Node) ast.Visitor {
	if v.found != nil {
		return nil
	}
	if v.pred(n) {
		v.found = n
		return nil
	}
	return v
}

func findNode(t *testing.T, prog *ast.Program, pred func(n ast.Node) bool) ast.Node {
	t.Helper()
	v := &matchVisitor{pred: pred}
	ast.Walk(v, prog)
	if v.found == nil {
		t.Fatal("no matching node found")
	}
	return v.found
}

func TestEffectStandaloneLetOperationIsExplicitlyOrdered(t *testing.T) {
	const src = "function main() -> string {\n" +
		"let response = llm(\"gpt4\", {\"messages\": []});\n" +
		"return response.text;\n" +
		"}"
	ctx, prog := runEffectWithProgram(t, src)
	if errs := effectErrors(ctx); len(errs) != 0 {
		t.Fatalf("a complete let initializer must be allowed: %v", errs)
	}
	if ctx.EffectFacts == nil {
		t.Fatal("EffectPass must publish EffectFacts on the context")
	}
	// The llm call node itself must be classified WORKFLOW_OPERATION.
	call := findNode(t, prog, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return false
		}
		ident, ok := c.Func().(*ast.IdentExpr)
		return ok && ident.Name() == "llm"
	})
	id, ok := ctx.Bindings.NodeIDOf(call)
	if !ok {
		t.Fatal("llm CallExpr not enumerated")
	}
	eff, ok := ctx.EffectFacts.NodeEffects().Get(id)
	if !ok {
		t.Fatal("node effect missing for llm call")
	}
	if eff != binding.EffectWorkflowOperation {
		t.Fatalf("node effect = %q, want workflow-operation", eff)
	}
}

func TestEffectEffectfulHelperIsPublishedThroughCallGraph(t *testing.T) {
	src := `
    function fetch() -> string {
        let response = llm("gpt4", {"messages": []});
        return response.text;
    }
    function main() -> string { let value = fetch(); return value; }
    `
	ctx := runEffect(t, src)
	if errs := effectErrors(ctx); len(errs) != 0 {
		t.Fatalf("unexpected E1041: %v", errs)
	}
	names := map[string]binding.EffectKind{}
	ctx.Bindings.Functions().Range(func(nodeID binding.AstNodeID, fid binding.FunctionID) bool {
		if symID, ok := ctx.Bindings.DeclarationSymbols().Get(nodeID); ok {
			if decl, ok := ctx.Bindings.Declarations().Get(symID); ok {
				if eff, ok := ctx.EffectFacts.FunctionEffects().Get(fid); ok {
					names[decl.Name] = eff
				}
			}
		}
		return true
	})
	want := map[string]binding.EffectKind{
		"fetch": binding.EffectWorkflowOperation,
		"main":  binding.EffectWorkflowOperation,
	}
	for name, eff := range want {
		if names[name] != eff {
			t.Fatalf("function_effects[%s] = %q, want %q (all: %v)", name, names[name], eff, names)
		}
	}
}

func TestEffectDedicatedOperationStatementIsExplicitlyOrdered(t *testing.T) {
	ctx := runEffect(t, `function main() { http("POST", "https://example.com", {}); }`)
	if errs := effectErrors(ctx); len(errs) != 0 {
		t.Fatalf("a dedicated operation statement must be allowed: %v", errs)
	}
}

func TestEffectFactsAreImmutable(t *testing.T) {
	ctx := runEffect(t, `function main() { http("POST", "https://example.com", {}); }`)
	facts := ctx.EffectFacts
	if facts == nil {
		t.Fatal("EffectFacts missing")
	}
	var fid binding.FunctionID
	ctx.Bindings.Functions().Range(func(_ binding.AstNodeID, f binding.FunctionID) bool {
		fid = f
		return false
	})
	defer func() {
		if recover() == nil {
			t.Fatal("FunctionEffects().Set must panic (MappingProxyType contract)")
		}
	}()
	facts.FunctionEffects().Set(fid, binding.EffectCalculation)
}

// ── §5.3: bare operation calls in expressions are ALLOWED ───────────
//
// Stage B widened EFFECT_CONTEXT: bare operation calls (no strategy
// modifier) in materializable positions — let/var initializers, nested
// expression operands, literal values, conditions — are no longer
// rejected at the type layer; the lowering hoist materializes them into
// producer nodes (hoistUserCalls → materializeCall). var and let are
// equivalent at the lowering layer (assignments are not materializable),
// so mutable-var initializers are allowed too. The gate remains ONLY for
// strategy contexts (attempt/parallel) and code-function bodies.
func TestEffectOperationInExpressionAllowed(t *testing.T) {
	cases := []struct{ name, src string }{
		{"mutable-var-initializer", `function main() {
            var response = llm("gpt4", {"messages": []});
        }`},
		{"nested-operand", `function main() {
            let x = "pre" + llm("gpt4", {"messages": []}).text;
        }`},
		{"literal-value", `function main() {
            let rec = { "k": llm("gpt4", {"messages": []}).text };
        }`},
		{"if-condition", `function main() -> string {
            if (llm("gpt4", {"messages": []}).text == "x") { return "y"; }
            return "n";
        }`},
	}
	for _, c := range cases {
		ctx := runEffect(t, c.src)
		if errs := effectErrors(ctx); len(errs) != 0 {
			t.Errorf("%s: bare operation call in expression must be allowed (§5.3), got E1041: %v", c.name, errs)
		}
	}
}

func TestEffectForeignCodeIsWorkflowOperation(t *testing.T) {
	// Foreign code in a complete let initializer is allowed; the node must
	// be classified WORKFLOW_OPERATION.
	const src = "function main() -> string {\n" +
		"let produced = foreign code python3() -> string {\n" +
		"source `def main() -> dict:\n    return {\"result\": \"ok\"}`;\n" +
		"};\n" +
		"return produced;\n" +
		"}"
	ctx, prog := runEffectWithProgram(t, src)
	if errs := effectErrors(ctx); len(errs) != 0 {
		t.Fatalf("unexpected E1041: %v", errs)
	}
	foreign := findNode(t, prog, func(n ast.Node) bool {
		_, ok := n.(*ast.ForeignCodeExpr)
		return ok
	})
	id, ok := ctx.Bindings.NodeIDOf(foreign)
	if !ok {
		t.Fatal("ForeignCodeExpr not enumerated")
	}
	if eff, _ := ctx.EffectFacts.NodeEffects().Get(id); eff != binding.EffectWorkflowOperation {
		t.Fatalf("foreign code node effect = %q, want workflow-operation", eff)
	}
}

func TestEffectAttemptOperationIsExplicitlyOrdered(t *testing.T) {
	// attempt exposes its operation as a dedicated structured statement; the
	// operation itself and the success/failure bodies must not be rejected.
	ctx := runEffect(t, `function main() -> string {
            attempt llm("gpt4", {"messages": []}) as result {
                success { return result.text; }
                failure (err) { return "fallback"; }
            }
        }`)
	if errs := effectErrors(ctx); len(errs) != 0 {
		t.Fatalf("unexpected E1041: %v", errs)
	}
}
