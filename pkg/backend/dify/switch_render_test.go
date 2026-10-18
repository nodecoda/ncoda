//go:build shelved

// 搁置面（AGENTS.md「开发主线与平台优先级」2026-09-11 拍板）：dify / coze-studio /
// python golden 语料面的测试结果**不是**主线（coze-biz）回归判据。本文件按门禁治理
// `内部治理文档（G-B 条）` G-B「快照/搁置面不阻塞默认门禁」移到默认门禁之外，
// 以免"常驻红基线"淹没主线真信号（实测：30 条常驻红里 23 条是搁置面）。
//
// 跑它：`go test -tags shelved ./...`（或 `make shelved`）；分类：`scripts/gov/triage.py`。
// 摘掉本 tag 的条件 = 该面重新成为主线判据（届时先修红再摘）。

package dify_test

//@feature switch — switch 统一多出口分类（dify 正编译：question-classifier 多出口渲染）

// G005 定向回归：switch（无 default）→ question-classifier 多出口渲染。
// 形状契约（与 oracle27 classify 一致）：
//   start → question-classifier（classes=标签列表）
//   question-classifier --<标签名>--> 分支体首节点（每类一条分支边，P4 端口显式）
//   分支体 → 共享 end（分支 return = 流程终止，P5 多出口契约）
// default 分支：dify question-classifier 无 default 端口 → 编译期 ADAPTER_ERROR。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
)

const switchNoDefaultSrc = `@language nodecoda/1
@mode workflow
function main(string query) -> string {
    switch (query) {
        "support": { return "support flow"; }
        "billing": { return "billing flow"; }
    }
}
`

const switchWithDefaultSrc = `@language nodecoda/1
@mode workflow
function main(string query) -> string {
    switch (query) {
        "support": { return "support flow"; }
        default: { return "default flow"; }
    }
}
`

func lowerSwitch(t *testing.T, src string) *dify.WorkflowAST {
	t.Helper()
	res := passes.DefaultPipeline().Run(src, "t.dc", nil)
	if res.HasErrors() {
		for _, d := range res.Diagnostics {
			t.Logf("pipeline diagnostic %d %s: %s", d.Code, d.Severity, d.Message)
		}
		t.Fatalf("frontend pipeline reported errors for source:\n%s", src)
	}
	graph, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("IR build: %v", err)
	}
	astOut, err := dify.RenderFromIR(graph, res.Program.Program.Mode())
	if err != nil {
		t.Fatalf("RenderFromIR: %v", err)
	}
	return astOut
}

func TestSwitchRenderQuestionClassifierMultiExit(t *testing.T) {
	wf := lowerSwitch(t, switchNoDefaultSrc)

	// 1. question-classifier 节点：classes = 标签顺序
	var classifier *dify.NodeDecl
	for i := range wf.Graph.Nodes {
		n := &wf.Graph.Nodes[i]
		if n.NodeType == "question-classifier" {
			classifier = n
			break
		}
	}
	if classifier == nil {
		t.Fatal("no question-classifier node emitted")
	}
	qc := classifier.Data.(*dify.QuestionClassifierDataNode)
	if qc.QueryVariableSelector == nil {
		t.Error("question-classifier missing query_variable_selector")
	} else if len(qc.QueryVariableSelector.Path) < 2 || qc.QueryVariableSelector.Path[1] != "query" {
		t.Errorf("query_variable_selector path = %v, want [<start>, query]", qc.QueryVariableSelector.Path)
	}
	if len(qc.Classes) != 2 {
		t.Fatalf("classes = %d, want 2", len(qc.Classes))
	}
	if qc.Classes[0].ID != "support" || qc.Classes[1].ID != "billing" {
		t.Errorf("class ids = [%s, %s], want [support, billing]", qc.Classes[0].ID, qc.Classes[1].ID)
	}
	if qc.Model == nil {
		t.Error("question-classifier missing model (serializer requires it)")
	}

	// 2. 分支边：每类一条，sourceHandle = 类名（P4 端口显式，无 default 边）
	handles := map[string]string{}
	for _, e := range wf.Graph.Edges {
		if e.Source == classifier.ID {
			handles[e.SourceHandle] = e.Target
		}
	}
	if len(handles) != 2 {
		t.Fatalf("branch edges = %d, want 2 (one per class)", len(handles))
	}
	if handles["support"] == "" || handles["billing"] == "" {
		t.Errorf("branch handles = %v, want support+billing edges", handles)
	}
	if handles["default"] != "" {
		t.Error("default branch edge must not exist (dify has no default port)")
	}

	// 3. 分支体 → end：分支 return = 流程终止（P5 多出口契约）
	ends := 0
	for _, e := range wf.Graph.Edges {
		if e.Target == "end_5" || strings.HasPrefix(e.Target, "end_") {
			ends++
		}
	}
	if ends < 2 {
		t.Errorf("branch→end edges = %d, want >= 2 (each branch returns to end)", ends)
	}
}

func TestSwitchRenderDefaultBranchRejected(t *testing.T) {
	res := passes.DefaultPipeline().Run(switchWithDefaultSrc, "t.dc", nil)
	if res.HasErrors() {
		t.Fatal("pipeline should accept switch+default (language level); rejection is dify-only")
	}
	graph, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("IR build: %v", err)
	}
	_, err = dify.RenderFromIR(graph, res.Program.Program.Mode())
	if err == nil {
		t.Fatal("dify render must reject switch default branch (no default port)")
	}
	if !strings.Contains(err.Error(), "no default port") {
		t.Errorf("error = %v, want no-default-port reason", err)
	}
}

func TestSwitchRenderVariableConfluence(t *testing.T) {
	// 分支后引用形态：分支体尾部值经 switch 隐含汇聚点（Aggregate）汇入
	// 后续语句 → dify variable-aggregator 汇聚（与 oracle27 classify 汇聚形状一致）。
	src := `@language nodecoda/1
@mode workflow
function main(string query) -> string {
    switch (query) {
        "support": { let a = "support flow"; }
        "billing": { let a = "billing flow"; }
    }
    return "done";
}
`
	wf := lowerSwitch(t, src)
	hasAggregator := false
	for _, n := range wf.Graph.Nodes {
		if n.NodeType == "variable-aggregator" {
			hasAggregator = true
			break
		}
	}
	if !hasAggregator {
		t.Error("branch-post-ref switch must render a variable-aggregator confluence")
	}
}

func TestRenderGroupedAggregateFields(t *testing.T) {
	// A mutable record crossing an if is a field-wise phi in the neutral IR.
	// Dify must retain that shape as grouped variable-aggregator data instead
	// of collapsing the record to a non-existent whole-value port.
	src := `@language nodecoda/1
@mode workflow
type Result = {
    string message;
    int status;
};
function main(bool flag) -> string {
    Result result;
    result = {"message": "before", "status": 0};
    if (flag) {
        result = {"message": "yes", "status": 1};
    } else {
        result = {"message": "no", "status": 2};
    }
    let message = result.message + "!";
    return message;
}
`
	wf := lowerSwitch(t, src)
	var aggregate *dify.NodeDecl
	for i := range wf.Graph.Nodes {
		if wf.Graph.Nodes[i].NodeType == "variable-aggregator" {
			aggregate = &wf.Graph.Nodes[i]
			break
		}
	}
	if aggregate == nil {
		t.Fatal("no grouped variable-aggregator emitted")
	}
	data, ok := aggregate.Data.(*dify.VariableAggregatorDataNode)
	if !ok || data.AdvancedSettings == nil {
		t.Fatalf("aggregate data = %#v, want grouped advanced_settings", aggregate.Data)
	}
	groups, ok := data.AdvancedSettings["groups"].([]map[string]any)
	if !ok {
		t.Fatalf("groups = %#v, want []map[string]any", data.AdvancedSettings["groups"])
	}
	seen := map[string]bool{}
	for _, group := range groups {
		name, _ := group["group_name"].(string)
		seen[name] = true
		vars, _ := group["variables"].([]any)
		if len(vars) != 2 {
			t.Errorf("group %q candidates = %#v, want two branch candidates", name, vars)
		}
	}
	if !seen["message"] || !seen["status"] {
		t.Errorf("group names = %v, want message and status", seen)
	}

	// The later field reference must target the group's value envelope, not
	// the old scalar aggregate output.
	foundFieldRef := false
	for _, n := range wf.Graph.Nodes {
		if code, ok := n.Data.(*dify.CodeDataNode); ok {
			for _, input := range code.Variables {
				if len(input.Path) == 3 && input.Path[1] == "message" && input.Path[2] == "output" {
					foundFieldRef = true
				}
			}
		}
	}
	if !foundFieldRef {
		t.Error("no Dify output selector addressed grouped message.output")
	}
}
