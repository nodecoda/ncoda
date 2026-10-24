// 语言特性矩阵锚点声明（docs/LANGUAGE-FEATURE-MATRIX.md）——IR 层特性锁定位：
//@feature ternary — 三元表达式值位置降级条件 shim（TestBuildInlineCodeTernary / loop_ternary_regress）
//@feature record-type — 命名 record 类型声明（type X = { ... }，TestBuildJSONParseRecord）
//@feature enum — enum 类型（TestBuildEnumMemberValue + parser TestGrammarEnumDecl）
//@feature qualified-call — 限定调用 p.op({...}) 经 import 解析后落成 ToolCall（TestBuildToolCallParams / TestBuildToolStructuredParam）

package ir_test

// Tier-2 feature coverage (ROI #2): 补齐 pkg/ir 零覆盖率函数。
// 外部测试包,复用 build_test.go 的 buildIR/nodesOf/firstOf 助手,把 ncoda
// 片段走完整前端管线降到 IR,覆盖 stdlib / list-op / tool / extract /
// agent / return-template / loop-return / http-refs / enum / json.parse 等特征族。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/builtin"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/tool"
)

// buildIRErr runs the full frontend pipeline and asserts the IR build fails,
// returning the error text (for negative-path tests).
func buildIRErr(t *testing.T, src string) string {
	t.Helper()
	res := passes.DefaultPipeline().Run(src, "t.dc", nil)
	if res.HasErrors() {
		var parts []string
		for _, d := range res.Diagnostics {
			parts = append(parts, d.Message)
		}
		return strings.Join(parts, " | ")
	}
	if res.Program == nil {
		t.Fatal("frontend pipeline produced no TypedBoundProgram")
	}
	if _, err := ir.Build(res.Program); err == nil {
		t.Fatalf("ir.Build succeeded, want error for source:\n%s", src)
	} else {
		return err.Error()
	}
	return ""
}

// ── stdlib (emitStdlibCall / emitRagAnswer / emitFetchAndSummarize) ─────

func TestBuildStdlibRagAnswer(t *testing.T) {
	src := workflowHeader + `
function main(string query) -> string {
    return std.v1.rag_answer(query, "ds-001,ds-002", "openai/gpt-4o");
}
`
	g := buildIR(t, src)
	kn := firstOf(t, g, ir.KindKnowledge).(*ir.KnowledgeCall)
	if len(kn.DatasetNames) != 2 || kn.DatasetNames[0] != "ds-001" || kn.DatasetNames[1] != "ds-002" {
		t.Errorf("dataset names = %v, want [ds-001 ds-002]", kn.DatasetNames)
	}
	if kn.QueryName != "query" {
		t.Errorf("query name = %q, want the bound parameter query", kn.QueryName)
	}
	// The rag answer lowers to knowledge → llm; the llm input refs the
	// knowledge outputList.
	llms := nodesOf(g)[ir.KindLLM]
	if len(llms) == 0 {
		t.Fatal("no LLM node in rag_answer lowering")
	}
	ln := llms[0].(*ir.LLMCall)
	if ln.Model != "openai/gpt-4o" {
		t.Errorf("llm model = %q, want openai/gpt-4o", ln.Model)
	}
	found := false
	for _, s := range ln.Inputs {
		if s.BlockID == kn.ID && s.Name == "outputList" {
			found = true
		}
	}
	if !found {
		t.Errorf("llm inputs = %+v, want an outputList ref to the knowledge node %s", ln.Inputs, kn.ID)
	}
}

func TestBuildStdlibFetchAndSummarize(t *testing.T) {
	src := workflowHeader + `
function main() -> string {
    return std.v1.fetch_and_summarize("https://example.com/article", "openai/gpt-4o");
}
`
	g := buildIR(t, src)
	http := firstOf(t, g, ir.KindHTTP).(*ir.HTTPCall)
	if http.Method != "GET" || !strings.Contains(http.URL, "example.com") {
		t.Errorf("http = %s %s, want GET example.com", http.Method, http.URL)
	}
	llms := nodesOf(g)[ir.KindLLM]
	if len(llms) == 0 {
		t.Fatal("no LLM node in fetch_and_summarize lowering")
	}
	ln := llms[0].(*ir.LLMCall)
	if ln.Model != "openai/gpt-4o" {
		t.Errorf("llm model = %q, want openai/gpt-4o", ln.Model)
	}
	found := false
	for _, s := range ln.Inputs {
		if s.BlockID == http.ID && s.Name == "body" {
			found = true
		}
	}
	if !found {
		t.Errorf("llm inputs = %+v, want a body ref to the http node %s", ln.Inputs, http.ID)
	}
}

func TestBuildStdlibUnsupported(t *testing.T) {
	src := workflowHeader + `
function main() -> string {
    return std.v1.nosuch_thing("x");
}
`
	err := buildIRErr(t, src)
	if !strings.Contains(err, "nosuch_thing") {
		t.Errorf("error = %q, want the unknown stdlib named", err)
	}
}

func TestBuildStdlibRagAnswerArity(t *testing.T) {
	src := workflowHeader + `
function main(string query) -> string {
    return std.v1.rag_answer(query, "ds-001");
}
`
	err := buildIRErr(t, src)
	if !strings.Contains(err, "3 arguments") {
		t.Errorf("error = %q, want a 3-argument arity error", err)
	}
}

// ── list ops (emitListOp / listFilterItem / listFilterKey / listLiteralValue) ──

func TestBuildListOpFilterContains(t *testing.T) {
	src := workflowHeader + `
function main(string csv) -> string[] {
    let items = split(csv, ",");
    return filter(items, (item) -> item.contains("x"));
}
`
	g := buildIR(t, src)
	op := firstOf(t, g, ir.KindListOp).(*ir.ListOp)
	if op.Operation != "filter" {
		t.Errorf("operation = %q, want filter", op.Operation)
	}
	if op.FilterKey != "" || op.FilterOp != "contains" || op.FilterVal != "x" {
		t.Errorf("filter = %q %q %v, want '' contains x", op.FilterKey, op.FilterOp, op.FilterVal)
	}
	if len(op.Inputs) != 1 {
		t.Errorf("inputs = %+v, want the source list bound", op.Inputs)
	}
}

func TestBuildListOpFilterCompare(t *testing.T) {
	src := workflowHeader + `
function main(string csv) -> string[] {
    let items = split(csv, ",");
    return filter(items, (item) -> item == "x");
}
`
	g := buildIR(t, src)
	op := firstOf(t, g, ir.KindListOp).(*ir.ListOp)
	if op.Operation != "filter" {
		t.Errorf("operation = %q, want filter", op.Operation)
	}
	if op.FilterKey != "" || op.FilterOp != "is" || op.FilterVal != "x" {
		t.Errorf("filter = %q %q %v, want '' is x", op.FilterKey, op.FilterOp, op.FilterVal)
	}
}

func TestBuildListOpTake(t *testing.T) {
	src := workflowHeader + `
function main(string csv) -> string[] {
    let items = split(csv, ",");
    return take(items, 3);
}
`
	g := buildIR(t, src)
	op := firstOf(t, g, ir.KindListOp).(*ir.ListOp)
	if op.Operation != "take" || op.Limit != 3 {
		t.Errorf("take = %q limit %d, want take 3", op.Operation, op.Limit)
	}
}

func TestBuildListOpNested(t *testing.T) {
	src := workflowHeader + `
function main(string csv) -> string[] {
    let items = split(csv, ",");
    return filter(take(items, 5), (item) -> item.contains("a"));
}
`
	g := buildIR(t, src)
	ops := nodesOf(g)[ir.KindListOp]
	if len(ops) != 2 {
		t.Fatalf("list ops = %d, want 2 (nested filter over take)", len(ops))
	}
	// The inner take must be the input producer of the outer filter.
	byID := map[string]*ir.ListOp{}
	for _, o := range ops {
		lo := o.(*ir.ListOp)
		byID[lo.ID] = lo
	}
	var filter *ir.ListOp
	for _, o := range ops {
		lo := o.(*ir.ListOp)
		if lo.Operation == "filter" {
			filter = lo
		}
	}
	if filter == nil || len(filter.Inputs) == 0 {
		t.Fatal("outer filter has no input")
	}
	takeNode := byID[filter.Inputs[0].BlockID]
	if takeNode == nil || takeNode.Operation != "take" {
		t.Errorf("outer filter input = %s, want the inner take node", filter.Inputs[0].BlockID)
	}
}

func TestBuildListOpFilterErrors(t *testing.T) {
	// filter without a lambda
	err := buildIRErr(t, workflowHeader+`
function main(string csv) -> string[] {
    let items = split(csv, ",");
    return filter(items, csv);
}
`)
	if !strings.Contains(err, "lambda") && !strings.Contains(err, "filter") {
		t.Errorf("error = %q, want filter lambda error", err)
	}
}

// ── tool calls (buildTool / collectToolParams / literalValueTree) ───────

// toolPipeline wires a closed tool registry so the type stage can prove the
// qualified calls (the open-world default leaves tool outputs untyped).
func toolPipeline(reg *tool.Registry) *passes.Pipeline {
	return passes.DefaultPipeline().WithToolRegistry(reg)
}

func googleRegistry(t *testing.T) *tool.Registry {
	t.Helper()
	reg, err := tool.NewRegistry(
		tool.Definition{
			Provider:  "google_search",
			Operation: "search",
			Parameters: []tool.Parameter{
				{Name: "query", Type: "string", Required: true},
				{Name: "max_results", Type: "int", Required: true},
			},
			Outputs: []tool.OutputField{
				{Name: "result", Type: "string"},
			},
		},
		tool.Definition{
			Provider:  "xhs_article_extract",
			Operation: "extract_xhs_article_webinfo",
			Parameters: []tool.Parameter{
				{Name: "url", Type: "string", Required: true},
				{Name: "timelines", Type: "array", Required: false},
			},
			Outputs: []tool.OutputField{
				{Name: "baseInfo.videoUrl", Type: "string"},
			},
		},
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return reg
}

// buildToolIR lowers tool-call sources with the closed registry wired.
func buildToolIR(t *testing.T, src string) *ir.Graph {
	t.Helper()
	res := toolPipeline(googleRegistry(t)).Run(src, "t.dc", nil)
	if res.HasErrors() {
		for _, d := range res.Diagnostics {
			t.Logf("pipeline %d %s: %s", d.Code, d.Severity, d.Message)
		}
		t.Fatalf("frontend pipeline reported errors for source:\n%s", src)
	}
	if res.Program == nil {
		t.Fatal("frontend pipeline produced no TypedBoundProgram")
	}
	g, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("ir.Build: %v", err)
	}
	return g
}

// buildToolIRErr lowers tool-call sources and asserts the IR build fails.
func buildToolIRErr(t *testing.T, src string) string {
	t.Helper()
	res := toolPipeline(googleRegistry(t)).Run(src, "t.dc", nil)
	if res.HasErrors() {
		var parts []string
		for _, d := range res.Diagnostics {
			parts = append(parts, d.Message)
		}
		return strings.Join(parts, " | ")
	}
	if res.Program == nil {
		t.Fatal("frontend pipeline produced no TypedBoundProgram")
	}
	if _, err := ir.Build(res.Program); err == nil {
		t.Fatalf("ir.Build succeeded, want error for source:\n%s", src)
	} else {
		return err.Error()
	}
	return ""
}

const toolHeader = "@language nodecoda/1\n@mode workflow\nimport \"acme.google_search\";\n"

func TestBuildToolCallParams(t *testing.T) {
	src := toolHeader + `
function main(string query) -> string {
    let search = google_search.search({
        "query": query,
        "max_results": 5
    });
    return search.result;
}
`
	g := buildToolIR(t, src)
	tool := firstOf(t, g, ir.KindTool).(*ir.ToolCall)
	if tool.ProviderID != "google_search" || tool.Operation != "search" {
		t.Errorf("tool = %s.%s, want google_search.search", tool.ProviderID, tool.Operation)
	}
	if len(tool.Params) != 2 {
		t.Fatalf("params = %d, want 2 (query ref + max_results literal)", len(tool.Params))
	}
	if tool.Params[0].Key != "query" || tool.Params[0].Ref == nil {
		t.Errorf("param[0] = %+v, want a live ref for query", tool.Params[0])
	}
	if tool.Params[1].Key != "max_results" || tool.Params[1].Ref != nil || tool.Params[1].Value != int64(5) {
		t.Errorf("param[1] = %+v, want literal 5", tool.Params[1])
	}
}

func TestBuildToolStructuredParam(t *testing.T) {
	src := "@language nodecoda/1\n@mode workflow\nimport \"coze-studio.xhs_article_extract\";\n" + `
function main(string url) -> string {
    let xhs = xhs_article_extract.extract_xhs_article_webinfo({
        "url": url,
        "timelines": [{"start": 0, "end": 2000000}]
    });
    return xhs.baseInfo.videoUrl;
}
`
	g := buildToolIR(t, src)
	tool := firstOf(t, g, ir.KindTool).(*ir.ToolCall)
	if len(tool.Params) != 2 {
		t.Fatalf("params = %d, want 2 (url ref + timelines tree)", len(tool.Params))
	}
	tree := tool.Params[1]
	if tree.Key != "timelines" || tree.Ref != nil {
		t.Errorf("param[1] = %+v, want a value tree for timelines", tree)
	}
	arr, ok := tree.Value.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("timelines value = %#v, want a one-element array tree", tree.Value)
	}
	m, ok := arr[0].(map[string]any)
	if !ok || m["start"] != int64(0) || m["end"] != int64(2000000) {
		t.Errorf("timeline[0] = %#v, want {start:0, end:2000000}", arr[0])
	}
}

func TestBuildToolBadParam(t *testing.T) {
	src := toolHeader + `
function main(string query) -> string {
    let search = google_search.search({
        "query": query + "suffix",
        "max_results": 5
    });
    return search.result;
}
`
	err := buildToolIRErr(t, src)
	if !strings.Contains(err, "tool parameter") {
		t.Errorf("error = %q, want tool parameter error", err)
	}
}

// ── extract<T> (buildExtract / extractionType) ──────────────────────────

func TestBuildExtract(t *testing.T) {
	src := workflowHeader + `
type TravelRequest = {
    string city;
    float days;
    bool flexible;
    string[] tags;
}

function main(string text) -> string {
    let extracted = extract<TravelRequest>("openai_api_compatible/gpt-5.4", text, {
        instruction: "Extract a travel request",
        descriptions: {
            city: "Destination city",
            days: "Trip duration in days",
            flexible: "Whether travel dates are flexible",
            tags: "Travel preferences"
        },
        strategy: "prompt"
    });
    if (!extracted.ok) {
        return extracted.reason;
    }
    return extracted.value.city;
}
`
	g := buildIR(t, src)
	ln := firstOf(t, g, ir.KindLLM).(*ir.LLMCall)
	if ln.OutputFormat != "json" || ln.ResponseFormat != 2 {
		t.Errorf("format = %s/%d, want json/2", ln.OutputFormat, ln.ResponseFormat)
	}
	if ln.Model != "openai_api_compatible/gpt-5.4" {
		t.Errorf("model = %q, want openai_api_compatible/gpt-5.4", ln.Model)
	}
	if ln.Extraction == nil {
		t.Fatal("extraction spec missing")
	}
	if !strings.Contains(ln.Extraction.Instruction, "Extract") {
		t.Errorf("instruction = %q, want extract guidance", ln.Extraction.Instruction)
	}
	if len(ln.Extraction.Fields) != 4 {
		t.Fatalf("fields = %d, want 4 (city/days/flexible/tags)", len(ln.Extraction.Fields))
	}
	byName := map[string]*ir.ExtractionField{}
	for i := range ln.Extraction.Fields {
		f := &ln.Extraction.Fields[i]
		byName[f.Name] = f
	}
	if byName["city"] == nil || byName["city"].Type.Kind != "string" {
		t.Errorf("city field = %+v, want string type", byName["city"])
	}
	if byName["tags"] == nil || byName["tags"].Type.Kind != "list" {
		t.Errorf("tags field = %+v, want list type", byName["tags"])
	}
	if byName["flexible"] == nil || byName["flexible"].Type.Kind != "boolean" {
		t.Errorf("flexible field = %+v, want boolean type", byName["flexible"])
	}
	// The ok/reason/value envelope is exposed as structured outputs.
	if t0 := ln.OutTypes["ok"]; t0 == nil || t0.Kind != "boolean" {
		t.Errorf("out type ok = %+v, want boolean", t0)
	}
}

// ── return template (attachTemplateAnswer / resolveBindingPath, W2) ─────
//
// 载体 = **语句** `return template "<逐字文本>" [using { 名额: 值路径, … }]`
// （流程出口值侧；旧 `@answer` 文件头声明通路已删）。IR 层只做一件事：
// 把名额名解析成生产者引用（值路径 ⇒ ref），**不读模板文本**（零解析）。

func TestBuildReturnTemplateAnswer(t *testing.T) {
	src := workflowHeader + `
function main(string text) -> string {
    let res = foreign code python3(string input = text) -> { value: string; count: int; } {
        source ` + "`" + `def main(input: str) -> dict:
    return {"value": "v:" + input, "count": 1}` + "`" + `;
    };
    return template "got {{out}} and {{msg}}" using { out: res.value, msg: text };
}
`
	g := buildIR(t, src)
	var end *ir.End
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, st := range b.Stmts {
			switch st := st.(type) {
			case *ir.ReturnStmt:
				if st.End != nil && st.End.Answer != nil {
					end = st.End
				}
			case *ir.IfStmt:
				walk(st.Then)
				walk(st.Else)
			case *ir.AttemptStmt:
				walk(st.Success)
				walk(st.Failure)
			case *ir.LoopStmt:
				walk(st.Body)
			case *ir.ParallelStmt:
				for _, br := range st.Branches {
					walk(br)
				}
			case *ir.QaStmt:
				for _, br := range st.Branches {
					walk(br.Body)
				}
			}
		}
	}
	walk(g.Root)
	if end == nil {
		t.Fatal("no End node with an answer template")
	}
	// 模板逐字承载（零解析：两个字面量占位符都原样在）。
	if !strings.Contains(end.Answer.Template, "{{out}}") || !strings.Contains(end.Answer.Template, "{{msg}}") {
		t.Errorf("template = %q, want both placeholders verbatim", end.Answer.Template)
	}
	if len(end.Answer.Bindings) != 2 {
		t.Fatalf("bindings = %d, want 2", len(end.Answer.Bindings))
	}
	// 名额表保序（wire 序 = 源码序）。
	if end.Answer.Bindings[0].Name != "out" || end.Answer.Bindings[1].Name != "msg" {
		t.Errorf("binding order = %q,%q, want out,msg",
			end.Answer.Bindings[0].Name, end.Answer.Bindings[1].Name)
	}
	byName := map[string]*ir.EndAnswerBinding{}
	for i := range end.Answer.Bindings {
		b := &end.Answer.Bindings[i]
		byName[b.Name] = b
	}
	if byName["out"] == nil || byName["out"].Ref.Producer == "" {
		t.Errorf("binding out = %+v, want a resolved producer ref", byName["out"])
	}
	if byName["msg"] == nil || byName["msg"].Ref.Producer == "" {
		t.Errorf("binding msg = %+v, want a resolved producer ref", byName["msg"])
	}
}

// 名额值必须是值路径：字面量 / 表达式既没有生产者可引用，也不是平台名额的形状
// ⇒ 语言错（fail loud；旧实现按模板反推，把「模板没提到」的名额静默丢弃）。
func TestBuildReturnTemplateBindingValueMustBeValuePath(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"literal", `"字面量"`},
		{"concat", `text + "s"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := workflowHeader + `
function main(string text) -> string {
    return template "{{v}}" using { v: ` + tc.value + ` };
}
`
			err := buildIRErr(t, src)
			if !strings.Contains(err, "值路径") {
				t.Errorf("error = %q, want a value-path rejection", err)
			}
		})
	}
}

// ── loop return (emitLoopReturn) ────────────────────────────────────────

func TestBuildLoopReturn(t *testing.T) {
	src := workflowHeader + `
function main(string csv) -> int {
    let values = split(csv, ",");
    for (v in values) {
        if (v == "x") { return 42; }
    }
    return 0;
}
`
	g := buildIR(t, src)
	lrs := nodesOf(g)[ir.KindLoopReturn]
	if len(lrs) != 1 {
		t.Fatalf("loop return nodes = %d, want 1", len(lrs))
	}
	lr := lrs[0].(*ir.LoopReturnNode)
	if lr.Value.BlockID == "" || lr.Value.Name == "" {
		t.Errorf("loop return value = %+v, want a bound value source", lr.Value)
	}
	loop := firstOf(t, g, ir.KindLoop).(*ir.Loop)
	if loop.ReturnValue == nil {
		t.Error("loop return value not recorded on the loop container")
	}
}

// ── http config value slots (httpConfigValue / httpHeaders) ─────────────

// TestBuildHTTPHeaderRefs —— http() 的 config 值槽是**模板槽**（P12，规则表 C2-H8）：
// 作者写下的每个值都必须成文（占位符 + 输入源），而「裸引用」与「`${}` 模板」是
// **同一条**数据依赖 ⇒ 同一条渲染路径。旧实现按**表达式形状**枚举
// （StringLit | TemplateString | else ⇒ ref 收集），其余形状的头/体被整条静默丢掉：
// `"Authorization": token` 连头名一起消失、`"body": body` 让 body 蒸发，零诊断。
func TestBuildHTTPHeaderRefs(t *testing.T) {
	src := workflowHeader + `
function main(string token) -> int {
    let body = "payload";
    let r = http("POST", "https://api.example.com/x", {
        "headers": {
            "Authorization": token,
            "X-Trace": ` + "`" + `t-${token}` + "`" + `
        },
        "body": body
    });
    return r.status_code;
}
`
	g := buildIR(t, src)
	http := firstOf(t, g, ir.KindHTTP).(*ir.HTTPCall)
	// ① 两条头都必须成文：裸引用的那条也是占位符（不是被丢掉）。
	byName := map[string]string{}
	for _, h := range http.Headers {
		byName[h.Name] = h.Value
	}
	if len(http.Headers) != 2 {
		t.Fatalf("headers = %+v, want both Authorization and X-Trace（写下的头不得蒸发）", http.Headers)
	}
	if got := byName["Authorization"]; !strings.HasPrefix(got, "{{block_output_") || !strings.HasSuffix(got, ".token}}") {
		t.Errorf("Authorization = %q, want a {{block_output_<id>.token}} placeholder（裸引用 = 退化模板）", got)
	}
	if got, want := byName["X-Trace"], "t-"+byName["Authorization"]; got != want {
		t.Errorf("X-Trace = %q, want %q（`t-${token}` 与 `t-` + 裸引用必须同形）", got, want)
	}
	// ② body 同理：裸引用 body 变量必须成文（旧行为：body 蒸发成空串）。
	if !strings.HasPrefix(http.Body, "{{block_output_") || !strings.HasSuffix(http.Body, ".output}}") {
		t.Errorf("body = %q, want a {{block_output_<id>.output}} placeholder", http.Body)
	}
	// ③ 两种写法指向同一条数据依赖 ⇒ 该 producer 端口在 Inputs 里只登记一次。
	//    （Inputs = 参考目标渲染器的 variables 载体；coze 的 node_inputs 由占位符反向合成。）
	counts := map[string]int{}
	for _, in := range http.Inputs {
		counts[in.Name]++
	}
	if counts["token"] != 1 {
		t.Errorf("inputs = %+v, want the token port registered exactly once (deduped across both headers)", http.Inputs)
	}
	if counts["output"] != 1 {
		t.Errorf("inputs = %+v, want the body producer port registered exactly once", http.Inputs)
	}
}

// ── agent(...) profile-pinned (buildAgent) ──────────────────────────────

func TestBuildAgentCall(t *testing.T) {
	toolProfile, err := binding.NewAgentToolTargetProfile(
		"weather", "wx", "WeatherProvider", "builtin", "plugin-weather", "get_weather", nil)
	if err != nil {
		t.Fatalf("NewAgentToolTargetProfile: %v", err)
	}
	agentProfile, err := binding.NewAgentTargetProfile(
		"assistant", "provider-name", "ReActAgentStrategy", "ReAct Agent",
		"plugin-strategy", "2",
		[]binding.AgentParameterTarget{
			{Name: "model", ParameterType: "string", Required: true},
			{Name: "query", ParameterType: "string", Required: true},
			{Name: "instruction", ParameterType: "string", Required: false},
			{Name: "tools", ParameterType: "array", Required: true},
		},
		map[string]*binding.AgentToolTargetProfile{"weather": toolProfile})
	if err != nil {
		t.Fatalf("NewAgentTargetProfile: %v", err)
	}
	profile := passes.DefaultTargetProfile()
	profile.AgentProfiles["assistant"] = agentProfile

	src := workflowHeader + `
function main(string query) -> string {
    let result = agent("openai/gpt-4o", query, {
        profile: "assistant",
        instruction: "You are a helpful assistant",
        tools: ["weather"]
    });
    return result.text;
}
`
	res := passes.DefaultPipeline().Run(src, "t.dc", profile)
	if res.HasErrors() {
		for _, d := range res.Diagnostics {
			t.Logf("pipeline %d %s: %s", d.Code, d.Severity, d.Message)
		}
		t.Fatalf("frontend pipeline reported errors for source:\n%s", src)
	}
	if res.Program == nil {
		t.Fatal("frontend pipeline produced no TypedBoundProgram")
	}
	g, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("ir.Build: %v", err)
	}
	agents := nodesOf(g)[ir.KindAgent]
	if len(agents) != 1 {
		t.Fatalf("agent nodes = %d, want 1", len(agents))
	}
	ac := agents[0].(*ir.AgentCall)
	if ac.Model != "openai/gpt-4o" {
		t.Errorf("model = %q, want openai/gpt-4o", ac.Model)
	}
	if !strings.Contains(ac.Instruction, "helpful") {
		t.Errorf("instruction = %q, want pinned instruction", ac.Instruction)
	}
	if ac.StrategyName != "ReActAgentStrategy" || ac.StrategyProviderName != "provider-name" {
		t.Errorf("strategy = %s/%s, want ReActAgentStrategy/provider-name", ac.StrategyName, ac.StrategyProviderName)
	}
	if ac.PluginUniqueID != "plugin-strategy" {
		t.Errorf("plugin = %q, want plugin-strategy", ac.PluginUniqueID)
	}
	if len(ac.Tools) != 1 || ac.Tools[0].Alias != "weather" || ac.Tools[0].ToolName != "get_weather" {
		t.Errorf("tools = %+v, want weather→get_weather pinned", ac.Tools)
	}
	if ac.Query == nil {
		t.Error("agent query ref missing")
	}
}

// ── enum member value (enumMemberValue) ─────────────────────────────────

func TestBuildEnumMemberValue(t *testing.T) {
	src := workflowHeader + `
enum Priority { low, high }

function main() -> Priority {
    return Priority.low;
}
`
	g := buildIR(t, src)
	// R-96（2026-09-26）：枚举的载体 = 它的**线值字符串** ⇒ 枚举成员在值位置与书写
	// `"low"` 走**同一条**物化路径（原生 text 常量节点），**不再**是自造的常量 code
	// shim（旧断言锁的 `result` 口是虚构记录 `object{value}` 的产物：shim body 只产
	// 裸串 ⇒ 该口无生产者 = 运行期静默丢值）。规范形正本 =
	// `docs/five-step/spec/type-identity-canonical-form.md` §4.2。
	if _, ok := nodesOf(g)[ir.KindCode]; ok {
		t.Errorf("枚举成员不得物化成 code shim（R-96：载体 = 线值字符串字面量）")
	}
	tp := firstOf(t, g, ir.KindText).(*ir.TextProcess)
	if tp.Template != "low" {
		t.Errorf("text const template = %q, want %q（枚举成员物化 = 它的线值）", tp.Template, "low")
	}
	if len(tp.Inputs) != 0 {
		t.Errorf("常量 text 节点不得有输入：%+v", tp.Inputs)
	}
}

// ── inline code function with expression body (renderInlinePython) ──────

func TestBuildInlineCodeTernary(t *testing.T) {
	src := workflowHeader + `
code pick(bool flag, string a, string b) -> string {
    return flag ? a : b;
}

function main(string a) -> string {
    let r = pick(true, a, "b");
    return r;
}
`
	g := buildIR(t, src)
	cr := firstOf(t, g, ir.KindCode).(*ir.CodeRun)
	if !strings.Contains(cr.Code, "if ") && !strings.Contains(cr.Code, "?") {
		// ternary lowers to the python conditional expression; either the
		// pyast form ("X if C else Y") or the legacy template renders it.
		if !strings.Contains(cr.Code, "if") && !strings.Contains(cr.Code, ":") {
			t.Errorf("code = %q, want a conditional expression shim", cr.Code)
		}
	}
	if len(cr.Inputs) != 1 {
		t.Errorf("inputs = %+v, want the hoisted argument bound", cr.Inputs)
	}
}

// ── condition array-index bind (indexExprBindName) ──────────────────────

func TestBuildConditionArrayIndexBind(t *testing.T) {
	if _, ok := builtin.Contract("coze-biz-1.0")["knowledge"]; !ok {
		t.Skip("coze-biz knowledge 契约不在本构建（O2 开放仓只带参考目标契约）")
	}
	// A record-array element chain inside a non-native condition shape
	// (binary `+` left operand) forces materialization; the resolvable
	// IndexExpr binds through indexExprBindName instead of a plain index.
	src := workflowHeader + `
function main(string a, string query) -> string {
    let ctx = knowledge("ds-001", query, {});
    if (a + ctx.result[0].output != "x") {
        return "y";
    }
    return "n";
}
`
	g := buildIR(t, src)
	crs := nodesOf(g)[ir.KindCode]
	var condShim *ir.CodeRun
	for _, n := range crs {
		cr := n.(*ir.CodeRun)
		if cr.Condition && cr.Synthetic {
			condShim = cr
		}
	}
	if condShim == nil {
		t.Fatal("no synthetic condition shim materialized")
	}
	found := false
	for _, in := range condShim.Inputs {
		if len(in.Path) == 1 && in.Path[0] == "outputList_output" && in.Name == "outputList.output" {
			found = true
		}
	}
	if !found {
		t.Errorf("condition shim inputs = %+v, want the whole-chain dotted array-element ref bound (绑定名 = 端口路径 outputList_content → 端口 outputList.content)", condShim.Inputs)
	}
	if strings.Contains(condShim.Code, `["output"]`) || strings.Contains(condShim.Code, "ctx_result_0") {
		t.Errorf("condition shim 不得绑定整数组 + 字符串下标（[0] 丢失）:\n%s", condShim.Code)
	}
}

// ── root ends with output (rootEndsWithOutput) ──────────────────────────

func TestBuildRootEndsWithOutputBranch(t *testing.T) {
	// A void entry whose only returns live in a branch and whose root ends
	// with output(expr) (intermediate message; 2026-08-23 语义定稿后不再产生
	// continuing End)——output lowers to an OutputEmitter on the main path,
	// the branch return converges to the canonical end.
	src := workflowHeader + `
function main(string x) {
    if (x == "a") { return; }
    output("v");
}
`
	g := buildIR(t, src)
	found := false
	for _, s := range g.Root.Stmts {
		if ns, ok := s.(*ir.NodeStmt); ok {
			if _, ok := ns.Node.(*ir.Output); ok {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("root statements = %v, want an OutputEmitter", g.Root.Stmts)
	}
}

// ── json.parse shims (buildJSONParse / IsJSONParseShim / jsonDefaultLit) ──

func TestBuildJSONParseRecord(t *testing.T) {
	src := workflowHeader + `
type Resp = {
    string url;
    int status;
}

function main(string body) -> string {
    let parsed = json.parse<Resp>(body);
    return parsed.url;
}
`
	g := buildIR(t, src)
	cr := firstOf(t, g, ir.KindCode).(*ir.CodeRun)
	if !cr.IsJSONParseShim() {
		t.Errorf("code = %q, want the json.parse record shim shape", cr.Code)
	}
	if cr.JSONSchema == nil || cr.JSONSchema.Kind != "object" {
		t.Errorf("json schema = %+v, want object schema", cr.JSONSchema)
	}
	if !strings.Contains(cr.Code, "json.loads(") || !strings.Contains(cr.Code, "data.get(") {
		t.Errorf("code = %q, want json.loads + per-field data.get", cr.Code)
	}
	// int field default is 0 (jsonDefaultLit integer branch)
	if !strings.Contains(cr.Code, `"status": data.get("status", 0)`) {
		t.Errorf("code = %q, want status field default 0", cr.Code)
	}
}

func TestBuildJSONParseArrayShim(t *testing.T) {
	src := workflowHeader + `
function main(string body) -> string[] {
    let parsed = json.parse<string[]>(body);
    return parsed;
}
`
	g := buildIR(t, src)
	cr := firstOf(t, g, ir.KindCode).(*ir.CodeRun)
	if !cr.IsJSONParseShim() {
		t.Errorf("code = %q, want the json.parse array shim shape", cr.Code)
	}
	if cr.JSONSchema == nil || cr.JSONSchema.Kind != "list" {
		t.Errorf("json schema = %+v, want list schema", cr.JSONSchema)
	}
}
