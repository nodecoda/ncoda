//@feature operation-policy — 表达式内嵌套副作用算子必须物化（P11-d；S2-gate 2026-09-11）

package ir_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

// statementMatrix: every statement-position expression root that merely
// WRAPS a side-effecting operation (or a user-function inline). The wrapper
// value is unused, so the wrapper itself must not be materialised — but the
// operations inside it must (they are the reason the statement exists at all).
//
// Empty-graph contract (P11-d): `emitExprStmt` dispatched purely on the
// statement ROOT shape and returned silently for anything else, so a wrapped
// operation lost its node AND its author-written policy with no diagnostic.
var statementMatrix = []struct {
	name    string
	stmt    func(op string) string
	control func(op string) string
}{
	{"wrapper-call", func(op string) string { return "json.stringify(" + op + ");" },
		func(op string) string { return op + ";" }},
	{"wrapper-depth2", func(op string) string { return "json.stringify(json.stringify(" + op + "));" },
		func(op string) string { return op + ";" }},
	{"array-literal", func(op string) string { return "[" + op + "];" },
		func(op string) string { return op + ";" }},
	{"template", func(op string) string { return "`${" + op + "}`;" },
		func(op string) string { return op + ";" }},
	{"binary", func(op string) string { return op + " == \"\";" },
		func(op string) string { return op + ";" }},
}

// TestStatementWrappedOperationMaterializes is the statement-position half of
// P11-d: `json.stringify(http(u));` must produce exactly the graph of the bare
// `http(u);` — the wrapper's (unused) pure value is discarded, the operation
// node stays. Both the policy-free and the policy-wrapped forms are covered;
// for the policy form the author's policy must survive on the node.
func TestStatementWrappedOperationMaterializes(t *testing.T) {
	const op = `http("GET", u)`
	const opPolicy = `http("GET", u) with retry(max: 2, interval: 3s)`

	for _, tc := range statementMatrix {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			control := buildIR(t, workflowHeader+"\nfunction main(string u) -> string {\n    "+tc.control(op)+"\n    return \"x\";\n}")
			plain := buildIR(t, workflowHeader+"\nfunction main(string u) -> string {\n    "+tc.stmt(op)+"\n    return \"x\";\n}")

			if got, want := nodeIDSet(plain), nodeIDSet(control); !reflect.DeepEqual(got, want) {
				t.Errorf("wrapped statement graph differs from the bare-operation control\n got %v\nwant %v", got, want)
			}
			if n := len(nodesOf(plain)[ir.KindHTTP]); n != 1 {
				t.Errorf("wrapped statement materialized %d http nodes, want 1 "+
					"(the operation inside the wrapper was dropped)", n)
			}

			wrapped := buildIR(t, workflowHeader+"\nfunction main(string u) -> string {\n    "+tc.stmt(opPolicy)+"\n    return \"x\";\n}")
			if got, want := nodeIDSet(wrapped), nodeIDSet(control); !reflect.DeepEqual(got, want) {
				t.Errorf("policy-wrapped statement graph differs from the bare-operation control\n got %v\nwant %v", got, want)
			}
			h, ok := firstHTTPOf(wrapped)
			if !ok {
				t.Fatal("policy-wrapped statement produced no http node")
			}
			if h.Exception == nil || h.Exception.MaxRetry != 2 || h.Exception.RetryIntervalMS != 3000 {
				t.Errorf("statement lost the author policy: exception = %+v, want MaxRetry 2 / 3000ms", h.Exception)
			}
		})
	}
}

// TestStatementWrappedOperationMaterializesAcrossPositions re-runs the rule
// inside every container: the fix is one rule in one dispatch point, so a
// version that only works at the top level is not a fix (语言递归自洽).
func TestStatementWrappedOperationMaterializesAcrossPositions(t *testing.T) {
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
			// depth 1 wrapper and depth 2 wrapper chain, policy-free and
			// policy-wrapped — all four must equal the bare control graph.
			for _, form := range []struct{ name, stmt string }{
				{"depth1", `    json.stringify(http("GET", u));`},
				{"depth1-policy", `    json.stringify(http("GET", u) with retry(max: 1, interval: 1s));`},
				{"depth2", `    json.stringify(json.stringify(http("GET", u)));`},
				{"depth2-policy", `    json.stringify([http("GET", u) with retry(max: 1, interval: 1s)]);`},
			} {
				got := nodeIDSet(buildIR(t, workflowHeader+"\n"+p.body(form.stmt)))
				want := nodeIDSet(buildIR(t, workflowHeader+"\n"+p.body(`    http("GET", u);`)))
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s/%s: wrapped statement graph differs from bare control\n got %v\nwant %v",
						p.name, form.name, got, want)
				}
			}
		})
	}
}

// TestStatementWrappedUserFunctionInlines: the same dispatch hole swallowed
// inlined user functions too (`json.stringify(helper(u));` lost the helper
// body's side effects). The wrapper must not change what the helper does.
func TestStatementWrappedUserFunctionInlines(t *testing.T) {
	const helper = "function helper(string u) -> string {\n    http(\"GET\", u);\n    return u;\n}\n"
	control := buildIR(t, workflowHeader+"\n"+helper+
		"function main(string u) -> string {\n    helper(u);\n    return \"x\";\n}")
	wrapped := buildIR(t, workflowHeader+"\n"+helper+
		"function main(string u) -> string {\n    json.stringify(helper(u));\n    return \"x\";\n}")
	if got, want := nodeIDSet(wrapped), nodeIDSet(control); !reflect.DeepEqual(got, want) {
		t.Fatalf("wrapped inline call differs from the bare call\n got %v\nwant %v", got, want)
	}
}

// TestNestedPolicyOperationValuePosition: the value-position twin (P11-d).
// hoistUserCalls — the single expression materialization entry — lacked the
// OperationPolicyExpr case, so `let r = json.stringify(op(...) with p);`
// failed loud (ADAPTER_ERROR) while the policy-free form compiled. The wrapped
// form must produce the same graph as the policy-free form, with the policy.
func TestNestedPolicyOperationValuePosition(t *testing.T) {
	forms := []struct {
		name string
		stmt func(op string) string
	}{
		// 三个夹具消费的是操作的**字段**（`(op).body`），不是整值：整个
		// `http` 结果是一个多字段 record，平台上没有「整个 record」这个端口
		// （`${op}` / `json.stringify(op)` / `return \`x${op}\`` 因此是**语言错**，
		// 见 internal/compiler 的 whole-record-scalar-position 契约与
		// pkg/ir/whole_value_refs.go）。本条规则要钉的是**策略不得随
		// 包装丢失**，与值位置取整值还是取字段无关。
		{"jsonstringify", func(op string) string { return `    let r = json.stringify((` + op + `).body);` + "\n    return r;" }},
		{"template", func(op string) string { return "    let r = `${(" + op + ").body}`;\n    return r;" }},
		{"return-position", func(op string) string { return "\n    return `x${(" + op + ").body}`;" }},
		{"map-value", func(op string) string { return "    let r = { \"k\": (" + op + ").body };\n    return r.k;" }},
		{"binary-operand", func(op string) string { return "    let r = (" + op + ").body + \"!\";\n    return r;" }},
	}

	for _, f := range forms {
		f := f
		t.Run(f.name, func(t *testing.T) {
			plain := buildIR(t, workflowHeader+"\nfunction main(string u) -> string {\n"+f.stmt(`http("GET", u)`)+"\n}")
			withPolicy := buildIR(t, workflowHeader+"\nfunction main(string u) -> string {\n"+f.stmt(`http("GET", u) with retry(max: 4, interval: 1s)`)+"\n}")

			if got, want := nodeIDSet(withPolicy), nodeIDSet(plain); !reflect.DeepEqual(got, want) {
				t.Errorf("policy form graph differs from the policy-free form\n got %v\nwant %v", got, want)
			}
			h, ok := firstHTTPOf(withPolicy)
			if !ok {
				t.Fatal("nested policy operation produced no http node")
			}
			if h.Exception == nil || h.Exception.MaxRetry != 4 {
				t.Errorf("nested policy lost: exception = %+v, want MaxRetry 4", h.Exception)
			}
		})
	}
}

// TestPureStatementStaysNodeFree is the negative anchor: statements whose
// value is unused and which contain no side-effecting operation must stay
// exactly as they are today (zero new nodes, no dead shims). The corpus
// contains exactly one non-operation statement root (`json.parse<object>(…)`),
// so this is the shape that must not regress.
func TestPureStatementStaysNodeFree(t *testing.T) {
	base := nodeIDSet(buildIR(t, workflowHeader+"\nfunction main(string u) -> string {\n    return \"x\";\n}"))
	for _, stmt := range []string{
		"    1 + 2;",
		`    json.parse<object>(u);`,
		`    u == "";`,
	} {
		got := nodeIDSet(buildIR(t, workflowHeader+"\nfunction main(string u) -> string {\n"+stmt+"\n    return \"x\";\n}"))
		if !reflect.DeepEqual(got, base) {
			t.Errorf("pure statement %q materialized nodes\n got %v\nwant %v", stmt, got, base)
		}
	}
}

// firstHTTPOf returns the graph's single http node.
func firstHTTPOf(g *ir.Graph) (*ir.HTTPCall, bool) {
	var out *ir.HTTPCall
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			switch st := s.(type) {
			case *ir.NodeStmt:
				if h, ok := st.Node.(*ir.HTTPCall); ok {
					out = h
				}
			case *ir.IfStmt:
				walk(st.Then)
				walk(st.Else)
			case *ir.LoopStmt:
				walk(st.Body)
			case *ir.InlineStmt:
				walk(st.Body)
			case *ir.ParallelStmt:
				for _, br := range st.Branches {
					walk(br)
				}
			}
		}
	}
	walk(g.Root)
	return out, out != nil
}

var _ = sort.Strings
