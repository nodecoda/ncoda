package ir_test

// 语言特性矩阵锚点声明（docs/LANGUAGE-FEATURE-MATRIX.md）——IR 层缺口锁定位：
//@feature null-coalesce — route-1a R4 `??` first-not-null：编译物化 python
//  `x if x is not None else y` 三目 shim，绝不产生 truthy python `or`
// （TestBuildCoalesceOrValue 已迁移为 ?? 断言 + R1 错误断言）
//@feature bool-or — route-1a R1 `||` 纯布尔收紧：非 bool 操作数 = BINARY_TYPE
//  编译错误 + `use '??'` hint（TestBuildCoalesceOrValue 尾部 R1 断言）
//@feature any-strategy — 显式 any 类型透传缩（TestBuildAnyTypePassThrough + TestBuildAnyFieldAccessAny）
//@feature import — import 声明 → namespace gate → ToolCall 降级（TestBuildImportQualifiedToolCall）
//@feature env-const — const 编译期替换（TestBuildConstEnvReplacement）
//@feature explicit-cast — int(x)/string(x) 通用 cast 形态 code shim（TestBuildExplicitCast）

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/tool"
)

// ── null-coalesce / bool-or（route-1a：?? first-not-null / || 纯布尔） ───

func TestBuildCoalesceOrValue(t *testing.T) {
	// route-1a（2026-09-05 裁决）：`||` 收紧为纯布尔；值兜底迁往 `??`
	// （first-not-null），编译物化为 python `is not None` 三目 shim
	// （通道 B 表达式深处形态；通道 A variable_merge 物化见 P1b）。
	src := workflowHeader + `
function main(string a, string b) -> string {
    let x = a ?? "default";
    return x;
}
`
	g := buildIR(t, src)
	codes := nodesOf(g)[ir.KindCode]
	if len(codes) == 0 {
		t.Fatal("expected code shim for ?? in value position")
	}
	cr := codes[0].(*ir.CodeRun)
	if !strings.Contains(cr.Code, "is not None") {
		t.Errorf("code shim should contain first-not-null guard 'is not None', got: %s", cr.Code)
	}
	if strings.Contains(cr.Code, " or ") {
		t.Errorf("code shim must not use truthy python 'or' for ?? (R6), got: %s", cr.Code)
	}
	// 值位置 `||`（string 操作数）→ 编译错误 + hint（R1 收紧）。
	bad := workflowHeader + `
function main(string a) -> string {
    let x = a || "default";
    return x;
}
`
	res := passes.DefaultPipeline().Run(bad, "t.dc", nil)
	if !res.HasErrors() {
		t.Fatal("value-position string || must be a compile error under R1")
	}
	joined := ""
	for _, d := range res.Diagnostics {
		joined += d.Message
	}
	if !strings.Contains(joined, "use '??'") {
		t.Errorf("|| 收紧错误应带 'use ??' hint, got: %s", joined)
	}
}

// ── any-strategy（any 透传/收缩） ─────────────────────────────────────

func TestBuildAnyTypePassThrough(t *testing.T) {
	// ANY-SHRINK-STRATEGY: 显式 any 是合法开放扩展点（用户写出的类型），
	// 透传不报错、不收缩。此测试锁定显式 any 返回类型的构建路径。
	src := workflowHeader + `
function main() -> any {
    return "value";
}
`
	g := buildIR(t, src)
	if g == nil {
		t.Fatal("nil graph")
	}
	// any 返回值直接透传 —— 不产生 code shim（无操作，恒透传）。
	if codes := nodesOf(g)[ir.KindCode]; len(codes) != 0 {
		t.Errorf("any pass-through must not materialize code shims, got %d", len(codes))
	}
}

func TestBuildAnyFieldAccessAny(t *testing.T) {
	// Field access on an object-typed value returns any (open-object, not
	// narrowed). The IR builder leaves the field access as a code shim.
	src := workflowHeader + `
function main(object v) -> string {
    return v.text;
}
`
	// object fields are open; field access on an object returns any.
	// Assigning any to string is a compile error.
	res := passes.DefaultPipeline().Run(src, "t.dc", nil)
	if res.HasErrors() {
		// EXPECTED: type error — any cannot be narrowed to string
		diagText := ""
		for _, d := range res.Diagnostics {
			diagText += d.Message
		}
		if !strings.Contains(diagText, "Any") && !strings.Contains(diagText, "any") {
			t.Fatalf("unexpected diag: %s", diagText)
		}
		return
	}
	// If it somehow compiles, the graph must have a code shim for the
	// dynamic field access, not a native selector.
	g, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("ir.Build: %v", err)
	}
	if _, ok := nodesOf(g)[ir.KindCode]; !ok {
		t.Error("expected code shim for field access on any")
	}
}

// ── import（前端/binding 构造） ─────────────────────────────────────────

func TestBuildImportQualifiedToolCall(t *testing.T) {
	// google_search registry mirrors the capability header with
	// query parameter.
	reg, err := tool.NewRegistry(tool.Definition{
		Provider:   "google_search",
		Operation:  "search",
		Parameters: []tool.Parameter{{Name: "query", Type: "any"}},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	src := workflowHeader + `
import "acme.google_search";
function main(string q) -> string {
    let r = google_search.search({ query: q });
    return "ok";
}
`
	p := passes.DefaultPipeline().WithToolRegistry(reg)
	res := p.Run(src, "t.dc", nil)
	if res.HasErrors() {
		for _, d := range res.Diagnostics {
			t.Logf("diag %d %s: %s", d.Code, d.Severity, d.Message)
		}
		t.Fatalf("pipeline errors")
	}
	g, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("ir.Build: %v", err)
	}
	tools := nodesOf(g)[ir.KindTool]
	if len(tools) == 0 {
		t.Fatal("expected ToolCall for qualified import call")
	}
	tc := tools[0].(*ir.ToolCall)
	if tc.ProviderID != "google_search" || tc.Operation != "search" {
		t.Errorf("ToolCall provider/operation = %s/%s, want google_search/search",
			tc.ProviderID, tc.Operation)
	}
}

// ── env-const（const 编译期替换） ──────────────────────────────────────

func TestBuildConstEnvReplacement(t *testing.T) {
	src := workflowHeader + `
const M = "openai_api_compatible/gpt-5.4";
function main() {
    let response = llm(M, {
        "messages": [{"role": "user", "content": "hi"}]
    });
}
`
	g := buildIR(t, src)
	llmNode := firstOf(t, g, ir.KindLLM).(*ir.LLMCall)
	if llmNode.Model != "openai_api_compatible/gpt-5.4" {
		t.Errorf("model = %q, want openai_api_compatible/gpt-5.4", llmNode.Model)
	}
}

// ── explicit-cast（通用 cast 形态） ────────────────────────────────────

func TestBuildExplicitCast(t *testing.T) {
	src := workflowHeader + `
function main(string status) -> string {
    let a = int(status);
    return "x";
}
`
	g := buildIR(t, src)
	codes := nodesOf(g)[ir.KindCode]
	if len(codes) == 0 {
		t.Fatal("expected code shim for int() cast")
	}
	cr := codes[0].(*ir.CodeRun)
	if !strings.Contains(cr.Code, "int(status)") {
		t.Errorf("code shim should contain int(), got: %s", cr.Code)
	}
	if cr.OutTypes["result"] == nil {
		t.Fatal("int() cast must have OutTypes")
	}
}

func TestIROptionalNullBaseDoesNotMaterializeProducer(t *testing.T) {
	src := workflowHeader + `
function main(string? input) -> string {
    string? value;
    value = null;
    if (input == "x") {
        value = input;
    }
    let result = value ?? "fallback";
    return result;
}
`
	g := buildIR(t, src)
	codes := nodesOf(g)[ir.KindCode]
	if len(codes) != 1 {
		t.Fatalf("explicit null base must not create a producer; want one ?? shim, got %d", len(codes))
	}
	cr := codes[0].(*ir.CodeRun)
	if !strings.Contains(cr.Code, "is not None") {
		t.Fatalf("?? shim lost null test: %q", cr.Code)
	}
}
