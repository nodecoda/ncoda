//@feature operation-policy — 裸语句策略算子必须物化（P11-c；S2-gate 2026-09-11）

package ir_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

// nodeIDSet collects every node id in the graph (walking nested containers) so
// the statement-position fix is asserted as "identical node set" rather than
// merely "an http node exists somewhere".
func nodeIDSet(g *ir.Graph) []string {
	var out []string
	for _, ns := range nodesOf(g) {
		for _, n := range ns {
			out = append(out, n.NodeID())
		}
	}
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out
}

// TestPolicyStatementMaterializesAcrossPositions is the execution anchor for
// P11-c: a policy-wrapped operation used as a STATEMENT (`op(...) with
// retry/timeout/default;`) must produce exactly the same graph as the same
// operation without the policy suffix. The former is the raising-declared shape
// (`unused_drop` rewrites `let v = op(...) with p;` into a bare ExprStmt
// carrying the policy), so dropping it silently erases side effects.
//
// The rule is position-independent (single emitStmt path), so the table covers
// isolated → nested → container → depth 2 → inline expansion; a fix that only
// works at the top level is not a fix.
func TestPolicyStatementMaterializesAcrossPositions(t *testing.T) {
	const call = `http("GET", u)`
	const policy = ` with retry(max: 1, interval: 1s)`

	positions := []struct {
		name string
		body func(stmt string) string
	}{
		{"toplevel", func(s string) string {
			return "function main(string u) -> string {\n" + s + "\n    return \"x\";\n}"
		}},
		{"if-then", func(s string) string {
			return "function main(string u) -> string {\n    if (u != \"\") {\n" + s + "\n    }\n    return \"x\";\n}"
		}},
		{"if-else", func(s string) string {
			return "function main(string u) -> string {\n    if (u != \"\") {\n        let a = u;\n    } else {\n" + s + "\n    }\n    return \"x\";\n}"
		}},
		{"for-body", func(s string) string {
			return "function main(string u) -> string {\n    array<string> xs = [\"a\"];\n    for (x in xs) {\n" + s + "\n    }\n    return \"x\";\n}"
		}},
		{"while-body", func(s string) string {
			return "function main(string u) -> string {\n    int i = 0;\n    while (i < 2) limit 5 {\n" + s + "\n        i += 1;\n    }\n    return \"x\";\n}"
		}},
		{"parallel-branch", func(s string) string {
			return "function main(string u) -> string {\n    parallel {\n        {\n" + s + "\n        }\n    }\n    return \"x\";\n}"
		}},
		{"for-if-depth2", func(s string) string {
			return "function main(string u) -> string {\n    array<string> xs = [\"a\"];\n    for (x in xs) {\n        if (u != \"\") {\n" + s + "\n        }\n    }\n    return \"x\";\n}"
		}},
		{"inline-helper", func(s string) string {
			return "function helper(string u) -> string {\n" + s + "\n    return u;\n}\nfunction main(string u) -> string {\n    helper(u);\n    return \"x\";\n}"
		}},
	}

	for _, p := range positions {
		p := p
		t.Run(p.name, func(t *testing.T) {
			withPolicy := buildIR(t, workflowHeader+"\n"+p.body("    "+call+policy+";"))
			plain := buildIR(t, workflowHeader+"\n"+p.body("    "+call+";"))

			if n := len(nodesOf(withPolicy)[ir.KindHTTP]); n != 1 {
				t.Fatalf("bare policy statement materialized %d http nodes, want 1 "+
					"(statement-position policy operation was dropped)", n)
			}
			got, want := nodeIDSet(withPolicy), nodeIDSet(plain)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("policy statement graph differs from the policy-less control:\n got %v\nwant %v", got, want)
			}
		})
	}
}

// TestPolicyStatementCarriesPolicyConfig guards the second half of the contract:
// materializing is not enough — the statement form must keep the author's policy
// (retry/timeout/default), otherwise the node silently loses its error policy.
func TestPolicyStatementCarriesPolicyConfig(t *testing.T) {
	src := workflowHeader + `
function main(string u) -> string {
    http("GET", u) with retry(max: 3, interval: 2s);
    return "x";
}
`
	g := buildIR(t, src)
	h := firstOf(t, g, ir.KindHTTP).(*ir.HTTPCall)
	if h.Exception == nil {
		t.Fatal("bare policy statement must keep the ExceptionConfig from `with retry`")
	}
	if h.Exception.MaxRetry != 3 || h.Exception.RetryIntervalMS != 2000 {
		t.Errorf("exception = %+v, want MaxRetry 3 with 2s interval", h.Exception)
	}
}

// TestPolicyStatementProducesNoBranchValue locks the phi/junction side of the
// rule: a bare statement must not leak a value into an enclosing if/else
// aggregate (the branch tail stays void).
func TestPolicyStatementProducesNoBranchValue(t *testing.T) {
	src := workflowHeader + `
function main(string u) -> string {
    string g;
    g = "";
    if (u != "") {
        http("GET", u) with retry(max: 1, interval: 1s);
        g = u;
    } else {
        g = "b";
    }
    return g;
}
`
	plain := buildIR(t, workflowHeader+`
function main(string u) -> string {
    string g;
    g = "";
    if (u != "") {
        http("GET", u);
        g = u;
    } else {
        g = "b";
    }
    return g;
}
`)
	g := buildIR(t, src)
	if got, want := nodeIDSet(g), nodeIDSet(plain); !reflect.DeepEqual(got, want) {
		t.Fatalf("policy statement polluted the branch graph:\n got %v\nwant %v", got, want)
	}
}

// TestPolicyValuePositionUnchanged is the value-position regression guard: the
// refactor that shares the node producer between both positions must not change
// `let x = op(...) with ...` (binding + policy + node).
func TestPolicyValuePositionUnchanged(t *testing.T) {
	src := workflowHeader + `
function main(string u) -> string {
    let r = http("GET", u) with retry(max: 1, interval: 1s);
    return r.body;
}
`
	g := buildIR(t, src)
	h := firstOf(t, g, ir.KindHTTP).(*ir.HTTPCall)
	if h.Exception == nil {
		t.Fatal("value position lost the ExceptionConfig")
	}
	if _, ok := nodesOf(g)[ir.KindHTTP]; !ok {
		t.Fatal("value position dropped the HTTP node")
	}
}
