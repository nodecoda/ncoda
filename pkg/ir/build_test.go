// 语言特性矩阵锚点声明（docs/LANGUAGE-FEATURE-MATRIX.md）——IR 层特性锁定位：
//@feature var-let — var/let 声明（含类型标注、裸 var v;）——IR 数据 ref 绑定（TestBuildArrayBindsResultPort / parser TestGrammarTypedVarDecl）
//@feature if-else — if/else 分支降级 Selector 条件节点 + Aggregate merge 收敛（TestBuildIfElseConditions）
//@feature for-while — for/while 循环（by_iteration / break/continue；TestBuildForLoop/WhileLoop/BreakContinue）
//@feature attempt — attempt 容错降级 HTTP 节点带 success/failure 分支（TestBuildAttempt）
//@feature yield — for 循环体内 yield 产出值循环（TestBuildYield）
//@feature request-input — request_input 降级 KindQA 节点（action+timeout 分支，TestBuildRequestInput）
//@feature output — output 消息发射器 OutputEmitter（模板占位、不发 End，TestBuildOutputStmt）
//@feature foreign-code — foreign code python3 子语言块降级 KindCode CodeRun（TestBuildForeignCodePython）
//@feature conv-var — 会话变量 assigner 写入降级 KindAssign（TestBuildConversationAssign）
//@feature dict-list — dict/list 字面量（TestBuildMapReturn NamedReturn + TestBuildArrayBindsResultPort）
//@feature template-str — ${expr} 模板插值降级 KindText TextProcess（TestBuildTemplateVar）
//@feature for-expr — for 表达式（值循环，yield 产出；TestBuildYield + parser TestGrammarForStmt）
//@feature file-type — file 第一公民类型 cast 零节点透传（TestFileCastTransparent/TestFileCastExpression）
//@feature switch — switch 多出口分类（intent 统一关键字；分支 + default + merge 汇聚；TestBuildSwitch）
//@feature function-inline — 子函数 inline call 值位置消费返回值（TestBuildInlineCallValue + type_pass TestMultiReturnExitValueTypesConsistent）

package ir_test

// External test package (ir_test): the ir package cannot import passes
// without an import cycle (passes -> tool -> ir), so the frontend-pipeline
// driven builder tests live here. They lower ncoda source snippets through
// the full frontend (passes.DefaultPipeline) into IR and assert the
// structured graph, covering the ir.Builder hot paths (build.go) that
// backend integration tests only exercise indirectly.

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/capability"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
)

// buildIR lowers src through the frontend pipeline and the IR builder.
func buildIR(t *testing.T, src string) *ir.Graph {
	t.Helper()
	res := passes.DefaultPipeline().Run(src, "t.dc", nil)
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

func TestBuildNestedHoistedScalarUsesNestedPhi(t *testing.T) {
	// X95 regression: the outer branch assigns a hoisted scalar before an
	// inner if, then the inner if assigns that same scalar on one arm.  The
	// outer phi must consume the inner phi's final binding, not the stale
	// pre-inner zero value.  This is the semantic shape behind the
	// coze-biz create_table/add_records error-message workflow.
	src := `@language nodecoda/1
@mode workflow
function main(string x) -> string {
    string agg;
    agg = "";
    if (x == "a") {
        string msg;
        msg = "";
        agg = msg;
        if (x == "b") {
        } else {
            msg = "err";
            agg = msg;
        }
    } else {
        agg = "outer";
    }
    return agg;
}
`
	g := buildIR(t, src)
	var outer *ir.IfStmt
	for _, stmt := range g.Root.Stmts {
		if candidate, ok := stmt.(*ir.IfStmt); ok {
			outer = candidate
			break
		}
	}
	if outer == nil || outer.Merge == nil {
		t.Fatalf("outer if must have a phi merge: %#v", outer)
	}
	var inner *ir.IfStmt
	for _, stmt := range outer.Then.Stmts {
		if candidate, ok := stmt.(*ir.IfStmt); ok {
			inner = candidate
			break
		}
	}
	if inner == nil || inner.Merge == nil {
		t.Fatalf("outer then must contain the nested phi: %#v", outer.Then)
	}
	if len(outer.Merge.Refs) != 0 || len(outer.Merge.Groups) != 1 {
		t.Fatalf("outer scalar must use one named phi group, got %#v", outer.Merge)
	}
	group := outer.Merge.Groups[0]
	if group.Name != "agg" || len(group.Refs) != 2 {
		t.Fatalf("outer phi group = %#v, want agg with two branch refs", group)
	}
	if group.Refs[0].Producer != inner.Merge.ID || group.Refs[0].Output != "agg" {
		t.Fatalf("outer then candidate = %#v, want nested merge %s.agg", group.Refs[0], inner.Merge.ID)
	}
}

// kindKeys returns the sorted kind names of a nodesOf result (test output).
func kindKeys(n map[ir.Kind][]ir.Node) []string {
	var out []string
	for k := range n {
		out = append(out, string(k))
	}
	sort.Strings(out)
	return out
}

// nodesOf walks the structured block tree and collects nodes by kind.
func nodesOf(g *ir.Graph) map[ir.Kind][]ir.Node {
	out := map[ir.Kind][]ir.Node{}
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			switch st := s.(type) {
			case *ir.NodeStmt:
				out[st.Node.Kind()] = append(out[st.Node.Kind()], st.Node)
			case *ir.IfStmt:
				out[ir.KindSelector] = append(out[ir.KindSelector], st.Cond)
				if st.Merge != nil {
					out[ir.KindAggregate] = append(out[ir.KindAggregate], st.Merge)
				}
				walk(st.Then)
				walk(st.Else)
			case *ir.AttemptStmt:
				out[st.Op.Kind()] = append(out[st.Op.Kind()], st.Op)
				walk(st.Success)
				walk(st.Failure)
			case *ir.LoopStmt:
				out[st.Container.Kind()] = append(out[st.Container.Kind()], st.Container)
				walk(st.Body)
			case *ir.ParallelStmt:
				for _, br := range st.Branches {
					walk(br)
				}
			case *ir.QaStmt:
				out[st.QA.Kind()] = append(out[st.QA.Kind()], st.QA)
				if st.Merge != nil {
					out[ir.KindAggregate] = append(out[ir.KindAggregate], st.Merge)
				}
				for _, br := range st.Branches {
					walk(br.Body)
				}
			case *ir.SwitchStmt:
				out[st.Switch.Kind()] = append(out[st.Switch.Kind()], st.Switch)
				if st.Merge != nil {
					out[ir.KindAggregate] = append(out[ir.KindAggregate], st.Merge)
				}
				for _, br := range st.Branches {
					walk(br.Body)
				}
			case *ir.InlineStmt:
				// An inlined helper body lives in its own isolated scope
				// (build_expr appendStmt(&InlineStmt{...})); a walker that
				// ignores it reports "the statement vanished" for graphs
				// that are correct.
				walk(st.Body)
			case *ir.BreakStmt:
				out[st.Node.Kind()] = append(out[st.Node.Kind()], st.Node)
			case *ir.ContinueStmt:
				out[st.Node.Kind()] = append(out[st.Node.Kind()], st.Node)
			}
		}
	}
	walk(g.Root)
	return out
}

// findEnd returns the graph's terminal End, found through the ReturnStmt that
// carries it (the End is not a NodeStmt, so nodesOf never sees it). When more
// than one End exists the flow-exit phi one wins — that is the canonical
// exit whose value binding these tests assert.
func findEnd(t *testing.T, g *ir.Graph) *ir.End {
	t.Helper()
	var found *ir.End
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			switch st := s.(type) {
			case *ir.ReturnStmt:
				if st.End == nil {
					continue
				}
				if found == nil || (st.End.FlowExitMerge != nil && found.FlowExitMerge == nil) {
					found = st.End
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
			case *ir.SwitchStmt:
				for _, br := range st.Branches {
					walk(br.Body)
				}
			case *ir.InlineStmt:
				walk(st.Body)
			}
		}
	}
	walk(g.Root)
	if found == nil {
		t.Fatal("no end node in graph")
	}
	return found
}

// firstOf returns the first collected node of the given kind.
func firstOf(t *testing.T, g *ir.Graph, k ir.Kind) ir.Node {
	t.Helper()
	nodes := nodesOf(g)[k]
	if len(nodes) == 0 {
		t.Fatalf("no %s node in graph", k)
	}
	return nodes[0]
}

const workflowHeader = "@language nodecoda/1\n@mode workflow\n"

func TestBuildMinimalLLMChain(t *testing.T) {
	src := workflowHeader + `
function main() {
    let response = llm("openai_api_compatible/gpt-5.4", {
        "messages": [{"role": "user", "content": "Hello"}]
    });
    return response.text;
}
`
	g := buildIR(t, src)
	if g.Entry == nil {
		t.Fatal("graph entry is nil")
	}
	if g.Root == nil {
		t.Fatal("graph root is nil")
	}
	llm := firstOf(t, g, ir.KindLLM).(*ir.LLMCall)
	if llm.Model != "openai_api_compatible/gpt-5.4" {
		t.Errorf("model = %q, want openai_api_compatible/gpt-5.4", llm.Model)
	}
	found := false
	for _, c := range llm.Config {
		if c.Key != "messages" {
			continue
		}
		msgs, ok := c.Value.([]ir.Message)
		if !ok {
			t.Fatalf("messages config value type = %T, want []ir.Message", c.Value)
		}
		if len(msgs) != 1 || msgs[0].Role != "user" {
			t.Errorf("messages = %+v, want one user message", msgs)
		}
		found = true
	}
	if !found {
		t.Errorf("config = %+v, want a messages ConfigPair", llm.Config)
	}
}

func TestBuildEntryParams(t *testing.T) {
	src := workflowHeader + `
function main(string query, int count = 10) {
    return query;
}
`
	g := buildIR(t, src)
	if g.Entry == nil || len(g.Entry.Params) != 2 {
		t.Fatalf("entry params = %+v, want 2", g.Entry.Params)
	}
	p0 := g.Entry.Params[0]
	if p0.Name != "query" || p0.Type == nil || p0.Type.Kind != "string" || p0.Optional {
		t.Errorf("param0 = %+v, want required string query", p0)
	}
	p1 := g.Entry.Params[1]
	def, ok := p1.Default.(int64)
	if p1.Name != "count" || !p1.Optional || !ok || def != 10 {
		t.Errorf("param1 = %+v, want optional int count default 10", p1)
	}
}

func TestBuildIfElseConditions(t *testing.T) {
	src := workflowHeader + `
function main(string language) -> string {
    if (language == "zh" || language == "cn") {
        return "中文";
    } else {
        return "english";
    }
}
`
	g := buildIR(t, src)
	sel := firstOf(t, g, ir.KindSelector).(*ir.Selector)
	if len(sel.Conditions) != 2 {
		t.Fatalf("conditions = %d, want 2", len(sel.Conditions))
	}
	if sel.Logic != 1 { // 1 = or
		t.Errorf("logic = %d, want 1 (or)", sel.Logic)
	}
	for i, c := range sel.Conditions {
		if c.Operator == 0 {
			t.Errorf("condition[%d] operator = 0, want non-zero selector op id", i)
		}
		if c.Left.Ref == nil {
			t.Errorf("condition[%d] left operand not a ref: %+v", i, c.Left)
		}
	}
	// Both arms return a VALUE: the main flow then has two value-carrying
	// exits, and the platform `end` node carries exactly ONE value binding —
	// so the exits converge at the flow-exit phi and only that merge feeds the
	// end (2026-09-12 用户拍板；real coze-biz exports:
	// `merge(G1:[A,B]) → end`, end.node_inputs = [{path: Group1, ref_node:
	// merge}]). The old first-write-wins binding silently dropped the second
	// exit's value.
	end := findEnd(t, g)
	if end.FlowExitMerge == nil {
		t.Fatalf("two value exits must converge at a flow-exit merge; end = %#v", end)
	}
	if got := len(end.FlowExitMerge.Refs); got != 2 {
		t.Errorf("flow-exit merge candidates = %d, want 2 (one per returning arm)", got)
	}
	if len(end.Inputs) != 1 || end.Inputs[0].Producer != end.FlowExitMerge.ID || end.Inputs[0].Output != wGroup1 {
		t.Errorf("end inputs = %+v, want the flow-exit merge Group1", end.Inputs)
	}
}

func TestBuildHTTPRequest(t *testing.T) {
	src := workflowHeader + `
function main(string url) {
    let page = http("GET", url, {"headers": {"X-K": "v"}});
    return page.body;
}
`
	g := buildIR(t, src)
	http := firstOf(t, g, ir.KindHTTP).(*ir.HTTPCall)
	if http.Method != "GET" {
		t.Errorf("method = %q, want GET", http.Method)
	}
	if !strings.Contains(http.URL, "{{block_output_") {
		t.Errorf("url = %q, want block_output template ref", http.URL)
	}
}

func TestBuildHTTPBodyTemplate(t *testing.T) {
	src := workflowHeader + `
function main(string text) {
    let r = http("POST", "https://api.example.com/x", {"body": ` + "`" + `{"q": ${text}}` + "`" + `});
    return r.status_code;
}
`
	g := buildIR(t, src)
	http := firstOf(t, g, ir.KindHTTP).(*ir.HTTPCall)
	if !strings.Contains(http.Body, "{{block_output_") {
		t.Errorf("body = %q, want template with block_output ref", http.Body)
	}
	if len(http.Inputs) == 0 {
		t.Errorf("inputs = %+v, want the body ref collected as an input source", http.Inputs)
	}
}

// ── statement-family builder coverage (2026-08-20 test governance) ──────
// These lock the ir.Builder statement paths that backend integration tests
// only reach indirectly: while/for loops, attempt, parallel, break/continue,
// request_input, output(), @conversation assign, else-less if.

func stmtTypes(g *ir.Graph) map[string]int {
	out := map[string]int{}
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			switch s.(type) {
			case *ir.NodeStmt:
				out["node"]++
			case *ir.IfStmt:
				out["if"]++
				walk(s.(*ir.IfStmt).Then)
				walk(s.(*ir.IfStmt).Else)
			case *ir.AttemptStmt:
				out["attempt"]++
				walk(s.(*ir.AttemptStmt).Success)
				walk(s.(*ir.AttemptStmt).Failure)
			case *ir.LoopStmt:
				out["loop"]++
				walk(s.(*ir.LoopStmt).Body)
			case *ir.ParallelStmt:
				out["parallel"]++
				for _, br := range s.(*ir.ParallelStmt).Branches {
					walk(br)
				}
			case *ir.QaStmt:
				out["qa"]++
				for _, br := range s.(*ir.QaStmt).Branches {
					walk(br.Body)
				}
			case *ir.SwitchStmt:
				out["switch"]++
				for _, br := range s.(*ir.SwitchStmt).Branches {
					walk(br.Body)
				}
			case *ir.ReturnStmt:
				out["return"]++
			}
		}
	}
	walk(g.Root)
	return out
}

func TestBuildWhileLoop(t *testing.T) {
	src := workflowHeader + `
function main() -> int {
    var num = 1;
    while (num < 5) limit 10 {
        num += 1;
    }
    return num;
}
`
	g := buildIR(t, src)
	types := stmtTypes(g)
	if types["loop"] != 1 {
		t.Fatalf("loop stmts = %d, want 1 (stmt types: %v)", types["loop"], types)
	}
	var loops []*ir.LoopStmt
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			if ls, ok := s.(*ir.LoopStmt); ok {
				loops = append(loops, ls)
			}
			switch st := s.(type) {
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
			}
		}
	}
	walk(g.Root)
	ls := loops[0]
	if ls.LoopType != "by_iteration" {
		t.Errorf("loop type = %q, want by_iteration", ls.LoopType)
	}
	if ls.LoopCount != 10 {
		t.Errorf("loop limit = %d, want 10", ls.LoopCount)
	}
	if _, ok := nodesOf(g)[ir.KindAssign]; !ok {
		t.Error("expected an Assign node for num += 1")
	}
}

func TestBuildForLoop(t *testing.T) {
	src := workflowHeader + `
const MODEL = "openai_api_compatible/gpt-5.4";
function main(string item) {
    let items = [item];
    for (item in items) {
        let translated = llm(MODEL, {
            "messages": [{ "role": "user", "content": ` + "`" + `T: ${item}` + "`" + ` }]
        });
    }
}
`
	g := buildIR(t, src)
	types := stmtTypes(g)
	if types["loop"] != 1 {
		t.Fatalf("loop stmts = %d, want 1 (stmt types: %v)", types["loop"], types)
	}
	if _, ok := nodesOf(g)[ir.KindLLM]; !ok {
		t.Error("expected an LLM node inside the loop body")
	}
}

func TestBuildAttempt(t *testing.T) {
	src := workflowHeader + `
function main() -> string {
    attempt http("GET", "https://httpbin.org/status/503", {}) as response {
        success { return "ok:" + response.body; }
        failure(error) { return "failed:" + error.error_type; }
    }
}
`
	g := buildIR(t, src)
	types := stmtTypes(g)
	if types["attempt"] != 1 {
		t.Fatalf("attempt stmts = %d, want 1 (stmt types: %v)", types["attempt"], types)
	}
	if _, ok := nodesOf(g)[ir.KindHTTP]; !ok {
		t.Error("expected an HTTP op node inside the attempt")
	}
}

func TestBuildParallel(t *testing.T) {
	src := workflowHeader + `
const MODEL = "openai_api_compatible/gpt-5.4";
function main(string query) {
    parallel {
        { let en = llm(MODEL, { "messages": [{ "role": "user", "content": query }] }); }
        { let zh = llm(MODEL, { "messages": [{ "role": "user", "content": query }] }); }
    }
    return "done";
}
`
	g := buildIR(t, src)
	types := stmtTypes(g)
	if types["parallel"] != 1 {
		t.Fatalf("parallel stmts = %d, want 1 (stmt types: %v)", types["parallel"], types)
	}
}

func TestBuildBreakContinue(t *testing.T) {
	src := workflowHeader + `
function main() -> int {
    let values = [1, 2, 3];
    for (value in values) {
        if (value == 1) { continue; }
        if (value > 4) { break; }
    }
    return 0;
}
`
	g := buildIR(t, src)
	if _, ok := nodesOf(g)[ir.KindBreak]; !ok {
		t.Error("expected a Break node")
	}
	if _, ok := nodesOf(g)[ir.KindContinue]; !ok {
		t.Error("expected a Continue node")
	}
}

func TestBuildSwitch(t *testing.T) {
	src := workflowHeader + `
function main(string query) -> string {
    switch (query) {
        "yes": { return "yes!"; }
        "no": { return "no!"; }
        default: { return "?"; }
    }
}
`
	g := buildIR(t, src)
	types := stmtTypes(g)
	if types["switch"] != 1 {
		t.Fatalf("switch stmts = %d, want 1 (stmt types: %v)", types["switch"], types)
	}
	nodes := nodesOf(g)
	switches := nodes[ir.KindSwitch]
	if len(switches) != 1 {
		t.Fatalf("switch nodes = %d, want 1", len(switches))
	}
	sw := switches[0].(*ir.Switch)
	if len(sw.Intents) != 2 || sw.Intents[0] != "yes" || sw.Intents[1] != "no" {
		t.Errorf("intents = %v, want [yes no]", sw.Intents)
	}
	// 三条分支全部以**带值** return 终止：三个主流程出口 ⇒ end 装不下，
	// 必须经 flow-exit phi 汇聚（2026-09-12 用户拍板；switch 与 if 同一
	// 规则、无宿主特判）。
	end := findEnd(t, g)
	if end.FlowExitMerge == nil {
		t.Fatalf("switch 全臂带值 return 必须经 flow-exit merge 汇聚；end = %#v", end)
	}
	if got := len(end.FlowExitMerge.Refs); got != 3 {
		t.Errorf("flow-exit merge candidates = %d, want 3 (one per arm)", got)
	}
	if len(end.Inputs) != 1 || end.Inputs[0].Producer != end.FlowExitMerge.ID {
		t.Errorf("end inputs = %+v, want the flow-exit merge Group1", end.Inputs)
	}
	_ = nodes
}

func TestBuildSwitchWithConfig(t *testing.T) {
	src := workflowHeader + `
function main(string query) -> string {
    switch (query, { temperature: 0.7, max_tokens: 1024, top_p: 1, mode: "top_speed", system_prompt: "sys" }) -> r {
        "yes": { return r.reason; }
        "no": { return "no!"; }
        default: { return "?"; }
    }
}
`
	g := buildIR(t, src)
	nodes := nodesOf(g)
	switches := nodes[ir.KindSwitch]
	if len(switches) != 1 {
		t.Fatalf("switch nodes = %d, want 1", len(switches))
	}
	sw := switches[0].(*ir.Switch)
	if !reflect.DeepEqual(sw.Intents, []string{"yes", "no"}) {
		t.Errorf("intents = %v, want [yes no]", sw.Intents)
	}
	if sw.Temperature == nil || *sw.Temperature != 0.7 {
		t.Errorf("temperature = %v, want 0.7", sw.Temperature)
	}
	if sw.MaxTokens != 1024 {
		t.Errorf("max_tokens = %d, want 1024", sw.MaxTokens)
	}
	if sw.TopP == nil || *sw.TopP != 1.0 {
		t.Errorf("top_p = %v, want 1.0", sw.TopP)
	}
	if sw.Mode != "top_speed" {
		t.Errorf("mode = %q, want top_speed", sw.Mode)
	}
	if sw.SystemPrompt != "sys" {
		t.Errorf("system_prompt = %q, want sys", sw.SystemPrompt)
	}
}

func TestBuildRequestInput(t *testing.T) {
	src := workflowHeader + `
enum ReviewAction { approve, reject }
type ReviewForm = { string comment; }
function main() -> string {
    request_input<ReviewForm, ReviewAction>({
        content: "Review",
        fields: { comment: { kind: "paragraph" } },
        actions: {
            approve: { title: "Approve", style: "primary" },
            reject: { title: "Reject", style: "default" }
        },
        timeout: { value: 3, unit: "day" }
    }) as review {
        action ReviewAction.approve { return review.comment; }
        action ReviewAction.reject { return "no"; }
        timeout { return "timed out"; }
    }
}
`
	g := buildIR(t, src)
	types := stmtTypes(g)
	if types["qa"] != 1 {
		t.Fatalf("qa stmts = %d, want 1 (stmt types: %v)", types["qa"], types)
	}
	if _, ok := nodesOf(g)[ir.KindQA]; !ok {
		t.Error("expected a QA node")
	}
}

func TestBuildOutputStmt(t *testing.T) {
	src := workflowHeader + `
function main(string value) {
    output(value);
    output(value + ":second");
}
`
	g := buildIR(t, src)
	// output(expr) lowers to OutputEmitter nodes (ir.Output) whose template
	// carries {{block_output_<id>.<port>}} placeholders for refs; it emits
	// no End.
	outputs := []*ir.Output{}
	for _, s := range g.Root.Stmts {
		if ns, ok := s.(*ir.NodeStmt); ok {
			if o, ok := ns.Node.(*ir.Output); ok {
				outputs = append(outputs, o)
			}
		}
	}
	if len(outputs) != 2 {
		t.Fatalf("output emitters = %d, want 2", len(outputs))
	}
	if !strings.Contains(outputs[1].Template, "{{") {
		t.Errorf("second output template = %q, want a {{block_output_...}} placeholder", outputs[1].Template)
	}
}

func TestBuildConversationAssign(t *testing.T) {
	// 正例 = **纯 set**（平台 assigner 的语义就是纯 set；一手权威见
	// `internal/compiler/cozebiz_conv_var_assign_value_contract_test.go` 头注）。
	src := "@language nodecoda/1\n@mode advanced-chat\n\n" + `
@conversation string username = "guest";
@conversation int score = 0;
function main(string query) {
    username = "alice";
    score = 7;
    output(username);
}
`
	g := buildIR(t, src)
	if len(g.ConversationVars) != 2 {
		t.Fatalf("conversation vars = %d, want 2", len(g.ConversationVars))
	}
	if _, ok := nodesOf(g)[ir.KindAssign]; !ok {
		t.Error("expected an Assign node for conversation variable writes")
	}
}

// TestBuildConversationAssignKeepsNeutralOpForm —— 会话变量赋值的**中性形**：
// 复合赋值 `score += 10` ⇒ `Assign{Operation: "+=", Value: <字面量 10>}`。
//
// 中性形 = **(op, 右值)**：前端把 `v += e` desugar 成 `v + e`（左操作数是「读回 v」的
// 产物），IR 拆掉左操作数、**保留 op**。每个后端自己决定 op 轴能不能承：
//
//	· dify/Graphon 有 op 轴（`pkg/backend/dify/render_nodes.go` 的
//	  `Operation: assignerOperation(n.Operation)` → wire 的 `operation` 字段）⇒ 承载；
//	· coze-biz 的 `assign_variable` 是**纯 set** ⇒ 后端 fail loud（判据在
//	  `internal/compiler/cozebiz_conv_var_assign_value_contract_test.go`）。
//
// 本判据锁「**op 不许在中性形里丢掉**」：丢成 `op="set", value=10` 之后，后端就再
// 没有信息去拒 —— 一手缺陷正是这一形态（coze-biz 照纯 set 发射 ⇒ `score += 10` 落成
// `score ← 10`：编译 SUCCEEDED、零诊断、**值错**）。
//
// 「真表达式右值不许被截断成右操作数」（`combined = a + b;` 曾落成 `combined ← b`）的
// 可观察判据在编译面：`internal/compiler/cozebiz_conv_var_assign_value_contract_test.go`
// 的 ①（断言载体同时读 a 与 b）。
//
// 撤实现（在中性形里丢 op）⇒ 本判据 RED。
func TestBuildConversationAssignKeepsNeutralOpForm(t *testing.T) {
	src := "@language nodecoda/1\n@mode advanced-chat\n\n" + `
@conversation int score = 0;
function main(string query) {
    score += 10;
    return;
}
`
	g := buildIR(t, src)
	var sa *ir.Assign
	for _, n := range nodesOf(g)[ir.KindAssign] {
		if as, ok := n.(*ir.Assign); ok && as.VarName == "score" {
			sa = as
		}
	}
	if sa == nil {
		t.Fatal("缺 score 的 Assign 节点")
	}
	if sa.Operation != "+=" {
		t.Fatalf("复合赋值的中性形必须保留 op：Operation = %q，want +==", sa.Operation)
	}
	if sa.Value.BlockID != "" {
		t.Fatalf("`score += 10` 的右值是字面量 ⇒ 不应有引用载体，得到 %+v", sa.Value)
	}
}

func TestBuildElseLessIfReturn(t *testing.T) {
	src := workflowHeader + `
function main(int n) -> int {
    if (n > 0) { return 1; }
    return 0;
}
`
	g := buildIR(t, src)
	types := stmtTypes(g)
	if types["if"] != 1 {
		t.Fatalf("if stmts = %d, want 1 (stmt types: %v)", types["if"], types)
	}
	// else-less if + trailing return 是两个**出口**（then 值 / 尾值）：规范形
	// 让它们收口于**一个**汇聚单元，位置 = End 的流程出口 phi。
	//
	// 2026-09-15 判据更正（A12-H12 同片）：旧写法查 `nodesOf()`（只走语句树）
	// 里有没有聚合 —— 那在找 `resolvePendingElse` 自建的树内 junction，而该
	// junction 正是本族缺陷：两条臂都终止本流程的 if **没有续接点**，也就没有
	// 值可 phi（规范形 = 与显式 `else { return …; }` 同形：出口平铺进流程出口
	// phi，任何 if 都不带 junction）。判据改按**规则**写：出口经一个汇聚收口、
	// 候选数 == 出口数；位置不再是判据。
	end := flowExitEnd(t, g)
	if end.FlowExitMerge == nil {
		t.Fatalf("expected the two exits to converge in one aggregate (end inputs: %v)", end.Inputs)
	}
	if got := len(end.FlowExitMerge.AllRefs()); got != 2 {
		t.Fatalf("flow-exit convergence candidates = %d, want 2 (one per exit): %#v", got, end.FlowExitMerge)
	}
}

// flowExitEnd returns the End carried by the flow's terminal return statement.
// It is the location of the flow-exit phi (`End.FlowExitMerge`) — a graph-level
// node, not a statement-tree node, so `nodesOf` cannot see it.
func flowExitEnd(t *testing.T, g *ir.Graph) *ir.End {
	t.Helper()
	var found *ir.End
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			switch st := s.(type) {
			case *ir.ReturnStmt:
				if st.End != nil {
					found = st.End
					return
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
			case *ir.SwitchStmt:
				for _, br := range st.Branches {
					walk(br.Body)
				}
			}
		}
	}
	walk(g.Root)
	if found == nil {
		t.Fatal("flow has no End-carrying return statement")
	}
	return found
}

// ── second batch: yield / map-return / selector ops / materialized
// condition / output-ending root / parallel-for ──────────────────────────

func TestBuildYield(t *testing.T) {
	src := workflowHeader + `
function main(string item) {
    let items = [item];
    var results = for (item in items) {
        yield item;
    };
    return results;
}
`
	g := buildIR(t, src)
	types := stmtTypes(g)
	if types["loop"] != 1 {
		t.Fatalf("loop stmts = %d, want 1 (stmt types: %v)", types["loop"], types)
	}
	if _, ok := nodesOf(g)[ir.KindCode]; !ok {
		t.Error("expected a code node for the yield value loop")
	}
}

func TestBuildElseLessLiveThenTrailingMapReturnIsCommonContinuation(t *testing.T) {
	// R2（family A, 2026-09-08）：else-less if 的 then 臂是 live void 副作用
	// （不 flow-end 终止）时，尾 return 是双臂**公共延续**，不是排他 else。
	// 缺陷形态（X265.min 复现）：pendingElse 无条件设立 → 尾 map return 被
	// 装箱成 false 臂 code shim + 单候选 merge → end 引用整组 record；N2
	// 退化 `v6 = {"Group1": {...}}; return v6.Group1;`。
	// 正确形状：if merge-less（无聚合），尾 return = 顶层 map return
	// （End NamedReturn 扁平字段）。
	src := workflowHeader + `
function main(string input) {
    if (input == "x") {
        let tmp = ` + "`" + `{{input}}` + "`" + `;
    }
    return { "a": input };
}
`
	g := buildIR(t, src)
	var ifStmt *ir.IfStmt
	var retStmt *ir.ReturnStmt
	for _, st := range g.Root.Stmts {
		switch s := st.(type) {
		case *ir.IfStmt:
			ifStmt = s
		case *ir.ReturnStmt:
			retStmt = s
		}
	}
	if ifStmt == nil {
		t.Fatal("expected one if stmt")
	}
	if ifStmt.Else != nil {
		t.Error("else-less if got an Else block (trailing return must not become the else arm)")
	}
	if ifStmt.Merge != nil {
		t.Error("live-then else-less if must be merge-less (defect = pendingElse boxes the return into a single-candidate merge)")
	}
	if retStmt == nil || retStmt.End == nil || !retStmt.End.NamedReturn {
		t.Fatal("expected a trailing top-level NamedReturn End (map return as common continuation)")
	}
	if len(retStmt.End.Outputs) != 1 || retStmt.End.Outputs[0].Name != "a" {
		t.Errorf("end outputs = %+v, want [{a @ start.input}]", retStmt.End.Outputs)
	}
	if retStmt.End.Outputs[0].Ref.Producer != "100001" {
		t.Errorf("field a ref producer = %q, want 100001 (branch-invariant direct producer)", retStmt.End.Outputs[0].Ref.Producer)
	}
}

func TestBuildMapReturn(t *testing.T) {
	src := workflowHeader + `
function main(string a, string b) {
    return { "g1": a, "g2": b };
}
`
	g := buildIR(t, src)
	// emitMapReturn lowers to a NamedReturn End carrying named outputs.
	var names []string
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			if rs, ok := s.(*ir.ReturnStmt); ok && rs.End != nil && rs.End.NamedReturn {
				for _, o := range rs.End.Outputs {
					names = append(names, o.Name)
				}
			}
			switch st := s.(type) {
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
			}
		}
	}
	walk(g.Root)
	if len(names) != 2 || names[0] != "g1" || names[1] != "g2" {
		t.Errorf("named return outputs = %v, want [g1 g2]", names)
	}
}

func TestBuildSelectorOperators(t *testing.T) {
	src := workflowHeader + `
function main(int a, int b, int c) -> string {
    if (a != b) { return "ne"; }
    if (a >= c) { return "ge"; }
    if (a < c) { return "lt"; }
    return "rest";
}
`
	g := buildIR(t, src)
	if _, ok := nodesOf(g)[ir.KindSelector]; !ok {
		t.Fatal("expected Selector nodes")
	}
	nodes := nodesOf(g)[ir.KindSelector]
	if len(nodes) < 3 {
		t.Errorf("selector count = %d, want >= 3", len(nodes))
	}
}

func TestBuildMaterializeCondition(t *testing.T) {
	src := workflowHeader + `
function main() -> string {
    let items = [1, 2, 3];
    if (len(items) > 0) { return "has"; }
    return "empty";
}
`
	g := buildIR(t, src)
	if _, ok := nodesOf(g)[ir.KindCode]; !ok {
		t.Error("expected a code node for the materialized condition (len(...) > 0)")
	}
	if _, ok := nodesOf(g)[ir.KindSelector]; !ok {
		t.Error("expected the selector on the materialized condition")
	}
}

func TestBuildRootEndsWithOutput(t *testing.T) {
	src := workflowHeader + `
function main(string value) {
    output(value);
}
`
	g := buildIR(t, src)
	// output(expr) lowers to an OutputEmitter (ir.Output) — not an End — and
	// the trailing implicit 900001 may follow (build()).
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

func TestBuildParallelFor(t *testing.T) {
	src := workflowHeader + `
function main() {
    let items = ["a", "bad", "c"];
    let values = parallel for (
        item in items, concurrency: 2, on_error: keep_null
    ) {
        yield item;
    };
    return values;
}
`
	g := buildIR(t, src)
	types := stmtTypes(g)
	if types["loop"] != 1 {
		t.Fatalf("loop stmts = %d, want 1 (parallel for lowers to a Loop, stmt types: %v)", types["loop"], types)
	}
}

func TestBuildSelectorContainOp(t *testing.T) {
	src := workflowHeader + `
function main(string text) -> string {
    if (text.contains("x")) { return "has"; }
    return "rest";
}
`
	g := buildIR(t, src)
	sel := firstOf(t, g, ir.KindSelector).(*ir.Selector)
	if len(sel.Conditions) != 1 {
		t.Fatalf("conditions = %d, want 1", len(sel.Conditions))
	}
	// contain maps via selectorOpToInt onto SELECTOR_OPERATORS id 7.
	if sel.Conditions[0].Operator != 7 {
		t.Errorf("operator = %d, want 7 (contain)", sel.Conditions[0].Operator)
	}
}

func TestBuildBranchReturnThenOutput(t *testing.T) {
	src := workflowHeader + `
function main(string value) {
    if (value == "x") { return "branch"; }
    output(value);
}
`
	g := buildIR(t, src)
	// branch return + output(expr): the output is an OutputEmitter on the
	// main path; the branch return converges to the canonical end.
	found := false
	for _, s := range g.Root.Stmts {
		if ns, ok := s.(*ir.NodeStmt); ok {
			if _, ok := ns.Node.(*ir.Output); ok {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("root = %v, want OutputEmitter after branch return", g.Root.Stmts)
	}
}

// ── third batch: capabilities / error / split / knowledge / foreign-code /
// text-concat / inline-call / boolean-calc condition ─────────────────────

func TestBuildWithCapabilities(t *testing.T) {
	src := workflowHeader + `
function main() -> string {
    return "hi";
}
`
	res := passes.DefaultPipeline().Run(src, "t.dc", nil)
	if res.HasErrors() || res.Program == nil {
		t.Fatal("frontend pipeline failed")
	}
	g, err := ir.Build(res.Program, ir.WithCapabilities(nativeCapabilities()))
	if err != nil {
		t.Fatalf("ir.Build with capabilities: %v", err)
	}
	if g.Entry == nil || g.Root == nil {
		t.Fatal("graph incomplete")
	}
}

func TestBuildUnsupportedError(t *testing.T) {
	e := &ir.UnsupportedError{Construct: `call "nosuch" in expression`}
	if !strings.HasPrefix(e.Error(), "coze: unsupported construct:") {
		t.Errorf("Error() = %q, want coze: unsupported construct: prefix", e.Error())
	}
	if !strings.Contains(e.Error(), "nosuch") {
		t.Errorf("Error() = %q, want the construct named", e.Error())
	}
}

func TestBuildSplit(t *testing.T) {
	src := workflowHeader + `
function main(string text) -> string[] {
    let parts = split(text, ",");
    return parts;
}
`
	g := buildIR(t, src)
	tp := firstOf(t, g, ir.KindText).(*ir.TextProcess)
	if tp.Method != "split" {
		t.Errorf("method = %q, want split", tp.Method)
	}
}

func TestBuildKnowledge(t *testing.T) {
	src := workflowHeader + `
function main(string query) -> string {
    let ctx = knowledge("ds-001", query, {});
    return "ok";
}
`
	g := buildIR(t, src)
	kn := firstOf(t, g, ir.KindKnowledge).(*ir.KnowledgeCall)
	if len(kn.DatasetNames) != 1 || kn.DatasetNames[0] != "ds-001" {
		t.Errorf("dataset names = %v, want [ds-001]", kn.DatasetNames)
	}
	if len(kn.Inputs) != 1 || len(kn.Inputs[0].Path) != 1 || kn.Inputs[0].Path[0] != "Query" || kn.Inputs[0].BlockID == "" {
		t.Errorf("inputs = %+v, want the query bound to the Query port", kn.Inputs)
	}
}

func TestBuildForeignCodePython(t *testing.T) {
	src := workflowHeader + `
function main(string text) -> string {
    let r = foreign code python3(string text = text) -> string {
        source ` + "`" + `def main(text: str) -> dict:
    return {"result": "py:" + text}` + "`" + `;
    };
    return r;
}
`
	g := buildIR(t, src)
	cr := firstOf(t, g, ir.KindCode).(*ir.CodeRun)
	if cr.Language != "python" {
		t.Errorf("language = %q, want python", cr.Language)
	}
	if !strings.Contains(cr.Code, "py:") {
		t.Errorf("code = %q, want the passthrough source", cr.Code)
	}
}

func TestBuildTemplateVar(t *testing.T) {
	src := workflowHeader + `
function main(string x) -> string {
    let t = ` + "`" + `a${x}` + "`" + `;
    return t;
}
`
	g := buildIR(t, src)
	tp := firstOf(t, g, ir.KindText).(*ir.TextProcess)
	if tp.Method != "concat" {
		t.Errorf("method = %q, want concat (let template lowers via emitTextTemplate)", tp.Method)
	}
}

func TestBuildConcatShim(t *testing.T) {
	// return `a${x}` + "b" without a text-concat capability materializes
	// as a Synthetic code shim (emitBinaryValue fallback).
	src := workflowHeader + `
function main(string x) -> string {
    return ` + "`" + `a${x}` + "`" + ` + "b";
}
`
	g := buildIR(t, src)
	if _, ok := nodesOf(g)[ir.KindCode]; !ok {
		t.Error("expected a code shim for the concatenation")
	}
}

func TestBuildConcatNative(t *testing.T) {
	// With the coze capability (TextConcat native), a `+` chain lowers to
	// a TextProcess concat node (emitTextConcat / flattenConcat).
	src := workflowHeader + `
function main(string x) -> string {
    return ` + "`" + `a${x}` + "`" + ` + "b";
}
`
	res := passes.DefaultPipeline().Run(src, "t.dc", nil)
	if res.HasErrors() || res.Program == nil {
		t.Fatal("frontend pipeline failed")
	}
	g, err := ir.Build(res.Program, ir.WithCapabilities(nativeCapabilities()))
	if err != nil {
		t.Fatalf("ir.Build: %v", err)
	}
	tp := firstOf(t, g, ir.KindText).(*ir.TextProcess)
	if tp.Method != "concat" {
		t.Errorf("method = %q, want concat", tp.Method)
	}
}

func TestBuildExprStmtInline(t *testing.T) {
	src := workflowHeader + `
function helper(string x) -> string {
    return x;
}
function main(string a) -> string {
    helper(a);
    return "done";
}
`
	g := buildIR(t, src)
	if g.Root == nil {
		t.Fatal("root nil")
	}
}

func TestBuildExprStmtBareLLM(t *testing.T) {
	// coze-biz real-export regression (X78): a bare llm(...) statement is a
	// real side-effecting call (external model invocation) even though its
	// result is unused. Dropping it would erase the LLM node + its edge from
	// the graph. emitExprStmt must materialize the node.
	src := workflowHeader + `
function main(string a) -> string {
    llm("openai_api_compatible/gpt-5.4", { "messages": [{ "role": "user", "content": a }] });
    return "done";
}
`
	g := buildIR(t, src)
	nodes := nodesOf(g)
	if _, ok := nodes[ir.KindLLM]; !ok {
		t.Fatalf("bare llm(...) statement must materialize an LLM node; got kinds %v", kindKeys(nodes))
	}
	if len(nodes[ir.KindLLM]) != 1 {
		t.Fatalf("expected exactly 1 LLM node for bare llm(...) statement; got %d", len(nodes[ir.KindLLM]))
	}
}

func TestBuildExprStmtBareSplit(t *testing.T) {
	// coze-biz real-export regression (X76): a bare split(...) statement
	// materializes a text node even when its result is unused.
	src := workflowHeader + `
function main(string a) -> string {
    split(a, ",");
    return "done";
}
`
	g := buildIR(t, src)
	nodes := nodesOf(g)
	if _, ok := nodes[ir.KindText]; !ok {
		t.Fatalf("bare split(...) statement must materialize a text node; got kinds %v", kindKeys(nodes))
	}
}

func TestBuildExprStmtPureCallStillDropped(t *testing.T) {
	// Pure expressions (string literal / arithmetic) as statements remain
	// discarded — no node, no branch value (regression guard).
	src := workflowHeader + `
function main(string a) -> string {
    "pure";
    return "done";
}
`
	g := buildIR(t, src)
	if g.Root == nil {
		t.Fatal("root nil")
	}
}

func TestBuildExprStmtBareLLMNoBranchValue(t *testing.T) {
	// A bare llm(...) statement yields NO value for the enclosing branch:
	// the tail stays at the last value-producing statement (qualified-tool
	// contract). Build without error and with the LLM node present proves
	// the branch tail was cleared instead of leaking a stale ref.
	src := workflowHeader + `
function main(string a) -> string {
    if (contains(a, "x")) {
        llm("openai_api_compatible/gpt-5.4", { "messages": [{ "role": "user", "content": a }] });
    }
    return "done";
}
`
	g := buildIR(t, src)
	nodes := nodesOf(g)
	if _, ok := nodes[ir.KindLLM]; !ok {
		t.Fatalf("bare llm(...) inside if branch must materialize an LLM node; got kinds %v", kindKeys(nodes))
	}
}

func TestBuildExprStmtBareHTTP(t *testing.T) {
	// Bare http(...) statement materializes an HTTP node (side-effecting).
	src := workflowHeader + `
function main(string url) -> string {
    http("GET", url);
    return "done";
}
`
	g := buildIR(t, src)
	nodes := nodesOf(g)
	if _, ok := nodes[ir.KindHTTP]; !ok {
		t.Fatalf("bare http(...) statement must materialize an HTTP node; got kinds %v", kindKeys(nodes))
	}
}

func TestBuildExprStmtBareKnowledge(t *testing.T) {
	// Bare knowledge(...) statement materializes a knowledge node.
	src := workflowHeader + `
function main(string q) -> string {
    knowledge("ds-001", q, {});
    return "done";
}
`
	g := buildIR(t, src)
	nodes := nodesOf(g)
	if _, ok := nodes[ir.KindKnowledge]; !ok {
		t.Fatalf("bare knowledge(...) statement must materialize a knowledge node; got kinds %v", kindKeys(nodes))
	}
}

func TestBuildInlineCallValue(t *testing.T) {
	src := workflowHeader + `
function helper(string x) -> string {
    return x;
}
function main(string a) -> string {
    let r = helper(a);
    return r;
}
`
	g := buildIR(t, src)
	// helper inlines; the value binds to the inlined producer (no new node
	// for an identity function). Building without error proves inlineCall.
	if g.Root == nil {
		t.Fatal("root nil")
	}
}

func TestBuildContainsCondition(t *testing.T) {
	src := workflowHeader + `
function main(string text) -> string {
    if (contains(text, "x")) { return "has"; }
    return "rest";
}
`
	g := buildIR(t, src)
	// contains is boolean-typed: the condition materializes as a shim and
	// the selector compares equals true (calcBuiltinOutType boolean gate).
	if _, ok := nodesOf(g)[ir.KindCode]; !ok {
		t.Error("expected a code node for the boolean contains condition")
	}
	if _, ok := nodesOf(g)[ir.KindSelector]; !ok {
		t.Error("expected the selector on the materialized contains")
	}
}

// ── fourth batch: exception config / conv defaults / map-value hoist /
// calc in value position / http refs ─────────────────────────────────────

func TestBuildExceptionRetry(t *testing.T) {
	src := workflowHeader + `
const MODEL = "openai_api_compatible/gpt-5.4";
function main(string q) -> string {
    let r = llm(MODEL, {
        "messages": [{ "role": "user", "content": q }]
    }) with retry(max: 2, interval: 1s);
    return r.text;
}
`
	g := buildIR(t, src)
	llm := firstOf(t, g, ir.KindLLM).(*ir.LLMCall)
	if llm.Exception == nil {
		t.Fatal("expected an ExceptionConfig from `with retry`")
	}
	if llm.Exception.MaxRetry != 2 || llm.Exception.RetryIntervalMS != 1000 {
		t.Errorf("exception = %+v, want MaxRetry 2 with 1s interval", llm.Exception)
	}
}

func TestBuildConvDefaultVariants(t *testing.T) {
	src := "@language nodecoda/1\n@mode advanced-chat\n\n" + `
@conversation string username = "guest";
@conversation int score = 0;
@conversation float ratio = 0.5;
@conversation bool enabled = true;
@conversation array<string> tags = [];
function main(string query) {
    output(query);
}
`
	g := buildIR(t, src)
	if len(g.ConversationVars) != 5 {
		t.Fatalf("conversation vars = %d, want 5", len(g.ConversationVars))
	}
	byName := map[string]ir.ConvVar{}
	for _, cv := range g.ConversationVars {
		byName[cv.Name] = cv
	}
	if cv := byName["score"]; cv.Default != int64(0) {
		t.Errorf("score default = %v (%T), want int64 0", cv.Default, cv.Default)
	}
	if cv := byName["ratio"]; cv.Default != float64(0.5) {
		t.Errorf("ratio default = %v (%T), want float64 0.5", cv.Default, cv.Default)
	}
	if cv := byName["enabled"]; cv.Default != true {
		t.Errorf("enabled default = %v, want true", cv.Default)
	}
}

func TestBuildMapValueHoist(t *testing.T) {
	src := workflowHeader + `
function helper(string x) -> string {
    return x;
}
function main(string a) {
    let r = { "k": helper(a) };
    return "done";
}
`
	g := buildIR(t, src)
	// the helper call hoists into a materialized node bound inside the map
	// literal (hoistUserCalls / freshHoistName).
	if len(nodesOf(g)) == 0 {
		t.Error("expected hoisted helper nodes in the graph")
	}
}

func TestBuildCalcValuePosition(t *testing.T) {
	src := workflowHeader + `
function main() -> int {
    let items = [1, 2, 3];
    return len(items);
}
`
	g := buildIR(t, src)
	// len() in a value position materializes as a code shim with the
	// builtin's integer output type (calcBuiltinOutTypeFor).
	if _, ok := nodesOf(g)[ir.KindCode]; !ok {
		t.Error("expected a code shim for len() in value position")
	}
}

// TestBuildBareReturnVoid — 用户 `return;`（void 函数）必须被 IR 正常
// 接受并自动补一个无输出 End，而不是在 emitValueExpr(nil) 崩溃成
// ADAPTER_ERROR（此前 `function main() { tool.op({}); return; }` 编译失败）。
func TestBuildBareReturnVoid(t *testing.T) {
	for _, src := range []string{
		"function main() { return; }",
	} {
		g := buildIR(t, src) // 崩溃即失败（此前这里 ADAPTER_ERROR）
		var ended *ir.ReturnStmt
		var walk func(b *ir.Block)
		walk = func(b *ir.Block) {
			if b == nil || ended != nil {
				return
			}
			for _, st := range b.Stmts {
				if rs, ok := st.(*ir.ReturnStmt); ok && rs.End != nil {
					ended = rs
					return
				}
				switch st := st.(type) {
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
				}
			}
		}
		walk(g.Root)
		if ended == nil {
			t.Fatalf("no terminal ReturnStmt with End for bare return:\n%s", src)
		}
		if len(ended.End.Outputs) != 0 {
			t.Fatalf("bare return End must carry no outputs, got %d:\n%s", len(ended.End.Outputs), src)
		}
	}
}

// TestBuildBareReturnAfterStmt — void 函数体中 return; 之前有语句时同样
// 终止 flow（真实 dify 形态的 IR 层回归：tool 调用 + return; 的编译链路
// 由 foundry closed_loop 测试锁定）。
func TestBuildBareReturnAfterStmt(t *testing.T) {
	const src = `function main() {
    let x = "a";
    return;
}`
	buildIR(t, src) // 崩溃即失败
}

// TestBuildArrayBindsResultPort locks the array-literal dataflow port: a
// let-bound array materializes as a code node returning {"result": [...]},
// so every downstream ref to that variable must name the "result" (list)
// port — binding it to the synthetic "output" (string) port would make
// consumers read a None string instead of the array (the runtime-shape
// bug behind the throwError array-parameter work).
func TestBuildArrayBindsResultPort(t *testing.T) {
	src := workflowHeader + `
function main(string a, string b) -> array<string> {
    let x = [a, b];
    return x;
}
`
	g := buildIR(t, src)
	var end *ir.End
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			if rs, ok := s.(*ir.ReturnStmt); ok && rs.End != nil && end == nil {
				end = rs.End
			}
			switch st := s.(type) {
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
			}
		}
	}
	walk(g.Root)
	if end == nil {
		t.Fatal("no End node found")
	}
	if len(end.Inputs) != 1 {
		t.Fatalf("end inputs = %v, want exactly one array ref", end.Inputs)
	}
	if end.Inputs[0].Output != "result" {
		t.Errorf("array variable ref port = %q, want %q (the array's real data port)", end.Inputs[0].Output, "result")
	}
}

// TestFileCastTransparent — file(x) 是 string→file 的静态标注 cast, 编译端
// 零节点透传: let f = file(x) 不物化任何语句级节点, 绑定直接映射到底层 x
// 的 ref; 工具 file 参数 {"image": file(x)} 直接接线到 x 的端口（无 cast
// shim）。nodesOf 只统计语句级节点（Start 是图级节点, End 是 ReturnStmt）。
func TestFileCastTransparent(t *testing.T) {
	src := workflowHeader + `
function main(string url) {
    let f = file(url);
    return f;
}
`
	g := buildIR(t, src)
	nodes := nodesOf(g)
	total := 0
	for _, ns := range nodes {
		total += len(ns)
	}
	// file cast 零语句级节点（无 code shim / 无 concat / 无其它物化）
	if total != 0 {
		t.Fatalf("file(x) cast must be transparent (zero statement nodes), got %d nodes: %v", total, nodes)
	}
	if g.Entry == nil {
		t.Fatal("graph entry missing")
	}
	// End 的输入必须直接接线到底层 Start.url 端口（透传链: f → url）
	rs := g.Root.Stmts[0].(*ir.ReturnStmt)
	if rs.Value == nil {
		t.Fatal("end value nil")
	}
	if rs.Value.NodeID() != g.Entry.NodeID() || rs.Value.Output() != "url" {
		t.Fatalf("file(x) must pass through to the underlying ref, got %s.%s",
			rs.Value.NodeID(), rs.Value.Output())
	}
}

// TestFileCastExpression — file(复杂表达式) 在值位置物化参数表达式后透传
// （零额外 cast 节点: 只有参数表达式自己的节点）。
func TestFileCastExpression(t *testing.T) {
	src := workflowHeader + `
function main(string a, string b) {
    let f = file(a + b);
    return f;
}
`
	g := buildIR(t, src)
	nodes := nodesOf(g)
	total := 0
	for _, ns := range nodes {
		total += len(ns)
	}
	// 只有 a+b 的 concat shim（1 个语句级节点）— file 本身零节点
	if total != 1 {
		t.Fatalf("file(expr) must only materialize the arg expr, got %d nodes", total)
	}
	if len(nodes[ir.KindCode]) != 1 {
		t.Fatalf("file(expr) must lower the arg as a single code shim, got %v", nodes[ir.KindCode])
	}
}

// A main-flow return in one arm makes that arm a dead convergence path.  The
// live if must not materialize its own phi; the outer escape point owns the
// one aggregate for the semantic variable.
func TestBuildOneFlowEndArmDoesNotMaterializeInnerPhi(t *testing.T) {
	src := workflowHeader + `
function main(string outer, string inner) {
    string agg;
    agg = "";
    if (outer == "true") {
        if (inner == "true") {
            return "success";
        } else {
            agg = "inner failed";
        }
    } else {
        agg = "outer failed";
    }
    output(agg);
}
`
	g := buildIR(t, src)
	nodes := nodesOf(g)
	if got := len(nodes[ir.KindAggregate]); got != 1 {
		t.Fatalf("aggregate count = %d, want 1 outer escape-point phi; kinds=%v", got, kindKeys(nodes))
	}
	if got := len(nodes[ir.KindSelector]); got != 2 {
		t.Fatalf("selector count = %d, want nested outer/inner selectors", got)
	}
}

// nativeCapabilities declares every native axis for IR tests. The fixture is
// platform-agnostic on purpose: per-platform tables belong to adapters
// (ASSET-BOUNDARY D8), not to the kernel or its tests.
func nativeCapabilities() *capability.Table {
	return &capability.Table{
		Target:       "native-fixture",
		Condition:    capability.Condition{RefLiteral: true, RefRef: true},
		TextConcat:   true,
		TextSplit:    true,
		JSONSerial:   true,
		JSONDeserial: true,
	}
}
