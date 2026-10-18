package ir_test

// http 参数槽（url / method）物化与 fail-loud 契约（P12，2026-09-11）。
//
// 契约（S2-gate 2026-09-11 定案，无例外规则）：
//
//   - **url 槽 = 可模板槽**：与 body / headers / 文本模板同级，接受任意
//     string 表达式，经「先 hoist 表达式效果 → 再走模板入口」渲染为平台模板：
//     字面量原样、值引用 `{{block_output_<id>.<port>}}`、模板串插值、`+` 链
//     拼接；模板入口不认识的形态物化为 producer 节点后以单个 ref 引用；
//     仍不可表达 → **fail loud**（绝不写空串）。
//   - **method 槽 = 常量槽**：平台 method 字段不是模板（物化成 ref 会产出
//     不可导入的产物），因此只接受字面量 / `const`；解析失败 → **fail loud**，
//     禁止静默默认 `"GET"`。
//
// 修复前的实测（RED）：`http("GET", build_url(u))` ⇒ `url: ""` 且 build_url
// 节点完全消失；`http("GET", "https://x/" + u)` ⇒ 同样空串且拼接蒸发；
// `http(m, u)`（m 为变量）⇒ 静默 `method: GET`（可能把 POST 请求发成 GET）。

import (
	"regexp"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

var (
	httpURLRefRe = regexp.MustCompile(`^\{\{block_output_\d+\.[A-Za-z0-9_.]+\}\}$`)
	httpURLURe   = regexp.MustCompile(`^\{\{block_output_\d+\.u\}\}$`)
)

func httpSlotSrc(urlExpr, methodExpr string) string {
	return workflowHeader + `
const API = "https://x/api";
const API_METHOD = "POST";

function build_url(string base) -> string {
    return base + "/api";
}

function main(string u) -> string {
    let r = http(` + methodExpr + `, ` + urlExpr + `);
    return r.body;
}
`
}

func firstHTTPURL(t *testing.T, src string) (string, map[ir.Kind][]ir.Node) {
	t.Helper()
	nodes := nodesOf(buildIR(t, src))
	hs := nodes[ir.KindHTTP]
	if len(hs) != 1 {
		t.Fatalf("want exactly 1 http node, got %d for source:\n%s", len(hs), src)
	}
	h, ok := hs[0].(*ir.HTTPCall)
	if !ok {
		t.Fatalf("http node type = %T", hs[0])
	}
	return h.URL, nodes
}

// TestHTTPURLSlotRendersEveryStringShape is the url half: every string shape
// the language admits must land as a template, never as "".
func TestHTTPURLSlotRendersEveryStringShape(t *testing.T) {
	cases := []struct {
		name    string
		urlExpr string
		want    *regexp.Regexp
		// wantExtra: minimum extra producer nodes over the bare-variable
		// control (nested expressions must materialize, not vanish).
		wantExtra int
	}{
		{"literal", `"https://x/api"`, regexp.MustCompile(`^https://x/api$`), 0},
		{"const", "API", regexp.MustCompile(`^https://x/api$`), 0},
		{"bare-var", "u", httpURLURe, 0},
		{"concat-literal-var", `"https://x/" + u`, regexp.MustCompile(`^https://x/\{\{block_output_\d+\.u\}\}$`), 0},
		{"template-string", "`https://x/${u}`", regexp.MustCompile(`^https://x/\{\{block_output_\d+\.u\}\}$`), 0},
		{"concat-two-vars", `u + u`, regexp.MustCompile(`^\{\{block_output_\d+\.u\}\}\{\{block_output_\d+\.u\}\}$`), 0},
		{"user-fn", "build_url(u)", httpURLRefRe, 1},
		{"user-fn-depth2", "build_url(build_url(u))", httpURLRefRe, 1},
		{"ternary", `u == "" ? "https://a" : "https://b"`, httpURLRefRe, 1},
	}

	controlURL, controlNodes := firstHTTPURL(t, httpSlotSrc("u", `"GET"`))
	if !httpURLURe.MatchString(controlURL) {
		t.Fatalf("control url = %q, want a block-output ref (harness drift)", controlURL)
	}
	controlCount := len(nodeIDSet(buildIR(t, httpSlotSrc("u", `"GET"`))))
	_ = controlNodes

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			src := httpSlotSrc(tc.urlExpr, `"GET"`)
			got, _ := firstHTTPURL(t, src)
			if got == "" {
				t.Fatalf("url slot rendered empty — the argument expression %s was dropped", tc.urlExpr)
			}
			if !tc.want.MatchString(got) {
				t.Fatalf("url = %q, want match %s (source: %s)", got, tc.want, tc.urlExpr)
			}
			if extra := len(nodeIDSet(buildIR(t, src))) - controlCount; extra < tc.wantExtra {
				t.Fatalf("nested expression in url slot produced %d extra nodes, want >= %d "+
					"(the expression's producer must exist, not vanish)", extra, tc.wantExtra)
			}
		})
	}
}

// TestHTTPURLSlotFieldAccessOfUpstreamOperation: a dotted read of an upstream
// operation port is a ref like any other.
func TestHTTPURLSlotFieldAccessOfUpstreamOperation(t *testing.T) {
	src := workflowHeader + `
function main(string u) -> string {
    let first = http("GET", u);
    let second = http("GET", first.body);
    return second.body;
}
`
	g := buildIR(t, src)
	hs := nodesOf(g)[ir.KindHTTP]
	if len(hs) != 2 {
		t.Fatalf("want 2 http nodes, got %d", len(hs))
	}
	second := hs[1].(*ir.HTTPCall)
	if !regexp.MustCompile(`^\{\{block_output_\d+\.body\}\}$`).MatchString(second.URL) {
		t.Fatalf("second url = %q, want a ref to the upstream body port", second.URL)
	}
}

// TestHTTPURLSlotAcrossCarriersAndContainers: one rule in one producer
// (`buildHTTP`), so let / statement / policy / attempt carriers and nested
// containers must all render identically (语言递归自洽).
func TestHTTPURLSlotAcrossCarriersAndContainers(t *testing.T) {
	const call = `http("GET", build_url(u))`
	const concatCall = `http("GET", "https://x/" + u)`
	bodies := []struct {
		name string
		body string
		// wantRef: the slot must render to a single materialized reference
		// (a nested call), false = a plain template (literal + variable).
		wantRef bool
	}{
		{"let-carrier", "    let r = " + call + ";\n    return r.body;", true},
		{"statement-carrier", "    " + call + ";\n    return \"x\";", true},
		{"policy-carrier", "    let r = " + call + " with retry(max: 1, interval: 1s);\n    return r.body;", true},
		{"if-container", "    if (u != \"\") {\n        let r = " + call + ";\n    }\n    return \"x\";", true},
		{"attempt-carrier", "    attempt " + call + " as a {\n        success {\n            return a.body;\n        }\n        failure(e) {\n            return \"\";\n        }\n    }", true},
		// Loop containers: the slot rule must hold there too. The inline form
		// is covered by a separate RED anchor (`loop_inline_value_contract_test.go`)
		// — a user-function inline inside a loop body currently yields no
		// value, a pre-existing gap unrelated to this slot rule.
		{"for-container", "    array<string> xs = [\"a\"];\n    for (x in xs) {\n        let r = " + concatCall + ";\n        let s = r.body;\n    }\n    return \"x\";", false},
		{"while-container", "    int i = 0;\n    while (i < 2) limit 5 {\n        let r = " + concatCall + ";\n        let s = r.body;\n        i += 1;\n    }\n    return \"x\";", false},
	}

	for _, b := range bodies {
		b := b
		t.Run(b.name, func(t *testing.T) {
			src := workflowHeader + `
function build_url(string base) -> string {
    return base + "/api";
}

function main(string u) -> string {
` + b.body + `
}
`
			got, nodes := firstHTTPURL(t, src)
			if got == "" {
				t.Fatalf("%s: url slot empty (the nested expression was dropped)", b.name)
			}
			if b.wantRef {
				if !httpURLRefRe.MatchString(got) {
					t.Fatalf("%s: url = %q, want a single block-output ref to the materialized call", b.name, got)
				}
			} else if !regexp.MustCompile(`^https://x/\{\{block_output_\d+\.u\}\}$`).MatchString(got) {
				t.Fatalf("%s: url = %q, want the literal+ref template", b.name, got)
			}
			if !b.wantRef {
				return
			}
			if n := len(nodes[ir.KindCode]); n < 1 && len(nodes[ir.KindLLM]) < 1 {
				// The inlined helper must leave a producer behind (code shim
				// or an equivalent materialized node) — count every extra kind.
				total := 0
				for k, v := range nodes {
					if k != ir.KindHTTP && k != ir.KindStart && k != ir.KindEnd {
						total += len(v)
					}
				}
				if total == 0 {
					t.Fatalf("%s: no producer node for the inlined helper (nodes: %v)", b.name, nodes)
				}
			}
		})
	}
}

// TestHTTPMethodSlotResolvesConstantAndFailsLoud: the platform method field
// is not a template, so a non-constant method must be a compile error — never
// a silent "GET".
func TestHTTPMethodSlotResolvesConstantAndFailsLoud(t *testing.T) {
	// Positive anchor: const resolves (today it silently degrades to GET).
	literalURL, _ := firstHTTPURL(t, httpSlotSrc("u", `"POST"`))
	_ = literalURL
	g := buildIR(t, httpSlotSrc("u", "API_METHOD"))
	if h := nodesOf(g)[ir.KindHTTP][0].(*ir.HTTPCall); h.Method != "POST" {
		t.Fatalf("const method resolved to %q, want POST", h.Method)
	}

	// Negative anchor: a runtime method is not expressible on the platform.
	bad := workflowHeader + `
function pick() -> string {
    return "GET";
}

function main(string u, string m) -> string {
    let r = http(m, u);
    return r.body;
}
`
	if msgs := buildIRErrs(t, bad); !containsAny(msgs, "method") {
		t.Fatalf("non-constant http method must fail loud, got diagnostics: %v", msgs)
	}
}

func containsAny(msgs []string, needle string) bool {
	for _, m := range msgs {
		if regexp.MustCompile(needle).MatchString(m) {
			return true
		}
	}
	return false
}
