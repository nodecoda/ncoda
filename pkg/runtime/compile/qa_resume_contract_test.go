package compile

// qa_resume_contract_test.go — M15-2 回归: resume 输出契约（真实 graph
// roundtrip）。锁定 qa 节点 resume 语义:
//   - form 字段平铺为输出端口（review.comment → comment 端口）
//   - choice(action 名) → branch_<i> 端口映射（spec 边端口域）
//   - 分支路由后分支 body 引用 QA 字段端口求值
//
// 节点级无法注入 resume context（GetResumeContext 依赖 runner 地址匹配）,
// 因此用 compile 层真实 roundtrip:首轮 interrupt → checkpoint → resume。

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/compose"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// resumeQASpec 构建 start → qa(approve/reject) → branch(comment/text) → end。
// approve 分支返回 QA 的 comment 字段端口;reject 分支返回常量 "rejected"。
func resumeQASpec() *spec.Spec {
	return &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("ask", spec.KindQA, &spec.QaConfig{Template: "Review?", Choices: []string{"approve", "reject"}}),
			nodeSpec("comment", spec.KindText, &spec.TextConfig{Method: "concat", Template: "{{c}}", Inputs: []spec.Source{{Path: []string{"c"}, BlockID: "ask", Name: "comment"}}}),
			nodeSpec("rejected", spec.KindText, &spec.TextConfig{Method: "concat", Template: "rejected"}),
			nodeSpec("agg", spec.KindAggregate, &spec.AggregateConfig{Refs: []spec.Ref{
				{Producer: "comment", Output: "output"},
				{Producer: "rejected", Output: "output"},
			}}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "agg", Output: wGroup1}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "ask"},
			{Source: "ask", Target: "comment", SourcePort: "branch_0"},
			{Source: "ask", Target: "rejected", SourcePort: "branch_1"},
			{Source: "comment", Target: "agg"},
			{Source: "rejected", Target: "agg"},
			{Source: "agg", Target: "e1"},
		},
	}
}

func runQAInterrupt(t *testing.T, store *qaMemStore) *compose.InterruptInfo {
	t.Helper()
	r := mustCompile(t, New(testRegistry()), resumeQASpec(), compose.WithCheckPointStore(store))
	_, err := r.Invoke(context.Background(), map[string]any{}, compose.WithCheckPointID("qa-cp"))
	info, ok := compose.ExtractInterruptInfo(err)
	if !ok {
		t.Fatalf("expected interrupt, got %v", err)
	}
	if len(info.InterruptContexts) != 1 {
		t.Fatalf("interrupt contexts = %d, want 1", len(info.InterruptContexts))
	}
	return info
}

func resumeQA(t *testing.T, store *qaMemStore, id string, data map[string]any) map[string]any {
	t.Helper()
	r := mustCompile(t, New(testRegistry()), resumeQASpec(), compose.WithCheckPointStore(store))
	rctx := compose.ResumeWithData(context.Background(), id, data)
	out, err := r.Invoke(rctx, map[string]any{}, compose.WithCheckPointID("qa-cp"))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	return UnwrapOutput(out)
}

func TestQAResumeActionToBranchPort(t *testing.T) {
	store := &qaMemStore{}
	info := runQAInterrupt(t, store)
	id := info.InterruptContexts[0].ID

	// approve → branch_0 → comment 字段端口（review.comment）
	got := resumeQA(t, store, id, map[string]any{"choice": "approve", "comment": "looks good"})
	if got["output"] != "looks good" {
		t.Fatalf("approve output = %v, want looks good (comment port)", got["output"])
	}

	// reject → branch_1 → 常量 "rejected"
	got = resumeQA(t, store, id, map[string]any{"choice": "reject"})
	if got["output"] != "rejected" {
		t.Fatalf("reject output = %v, want rejected", got["output"])
	}

	// 缺省（无 choice）→ default 端口;本 spec 无 default 出边 → 分支
	// 路由失败（诚实 fail-fast,与 coze 缺省分支语义一致）。
	r := mustCompile(t, New(testRegistry()), resumeQASpec(), compose.WithCheckPointStore(store))
	rctx := compose.ResumeWithData(context.Background(), id, map[string]any{"comment": "x"})
	if _, err := r.Invoke(rctx, map[string]any{}, compose.WithCheckPointID("qa-cp")); err == nil {
		t.Fatal("no-choice resume must fail: default port has no target")
	}
}

func TestQAResumeFlattenedFieldPorts(t *testing.T) {
	// 回归: resume 数据 form 字段必须平铺为 QA 输出端口 —— 分支 body 的
	// review.comment 引用按端口解析（IR fieldPort(qa)=field 契约）,不是
	// 只存在于 USER_RESPONSE 里。
	store := &qaMemStore{}
	info := runQAInterrupt(t, store)
	id := info.InterruptContexts[0].ID

	// 字段 comment 带嵌套结构（priority.value 风格 dotted 引用可解析）
	got := resumeQA(t, store, id, map[string]any{
		"choice": "approve", "comment": "nested ok", "priority": map[string]any{"value": "high"},
	})
	if got["output"] != "nested ok" {
		t.Fatalf("output = %v, want nested ok", got["output"])
	}
}
