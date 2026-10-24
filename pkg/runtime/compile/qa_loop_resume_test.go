package compile

// qa_loop_resume_test.go — M16-2: loop 内 request_input HITL resume（v2）。
//
// D2 契约（qa.go 注释排期）:loop 内 resume 需 per-iteration checkpoint
// （spike 已证 plain conduit 失败、per-iter 变体 PASSES）。这里在真实
// loopDriver 上验证:
//   - 首轮:loop 迭代 0 的 QA 中断,checkpoint 落盘（body per-iter id +
//     主图 id）
//   - resume:已完成迭代从 checkpoint 恢复,中断迭代注入 data 后继续,
//     后续迭代照常执行（HITL 每迭代确认语义:一次 resume 放行一轮,
//     下一轮重新中断）
//   - 无 checkpoint store 时 per-iteration checkpoint 禁用,loop 照常跑
//     （回归:WithCheckPointID + nil store 会被 eino 硬报错）
//
// 与 compile/qa_roundtrip_test.go（loop 外）互补。

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/compose"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// qaLoopSpec 构造 start → loop(by_iteration 2) → end。
// loop body: QA(确认) → text(回显 answer)。每迭代 QA 中断一次。
func qaLoopSpec() *spec.Spec {
	children := []spec.NodeSpec{
		nodeSpec("100003", spec.KindQA, &spec.QaConfig{Template: "确认?", Choices: []string{"ok"}}),
		nodeSpec("100004", spec.KindText, &spec.TextConfig{
			Method:   "concat",
			Template: "ans:{{r}}",
			Inputs:   []spec.Source{{Path: []string{"r"}, BlockID: "100003", Name: "USER_RESPONSE"}},
		}),
	}
	inner := []spec.EdgeSpec{
		{Source: "100002", Target: "100003", SourcePort: wLoopFunctionInlineOutput},
		{Source: "100003", Target: "100004"},
		{Source: "100004", Target: "100002", TargetPort: wLoopFunctionInlineInput},
	}
	loopCfg := &spec.LoopConfig{
		LoopType:      "by_iteration",
		LoopCount:     2,
		OutTypes:      map[string]*spec.TypeSpec{"output": {Kind: "array"}},
		OutputSources: []spec.Source{{Path: []string{"output"}, BlockID: "100004", Name: "output"}},
		Children:      children,
		InnerEdges:    inner,
	}
	return &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("100002", spec.KindLoop, loopCfg),
			nodeSpec("900001", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "100002", Output: "output"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "100002"},
			{Source: "100002", Target: "900001"},
		},
	}
}

// storeKeys 返回 checkpoint store 的 id 列表（诊断用）。
func storeKeys(m map[string][]byte) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// 首轮:迭代 0 的 QA 中断,body per-iter checkpoint 与主图 checkpoint
// 都应落盘（v2 关键:body 有独立 checkpoint id 才能按迭代恢复）。
func TestQALoopFirstRunInterrupts(t *testing.T) {
	store := &qaMemStore{}
	r := mustCompile(t, New(testRegistry()), qaLoopSpec(), compose.WithCheckPointStore(store))
	_, err := r.Invoke(context.Background(), map[string]any{}, compose.WithCheckPointID("loop-run"))
	info, ok := compose.ExtractInterruptInfo(err)
	if !ok {
		t.Fatalf("expected interrupt from loop body QA, got %v", err)
	}
	if len(info.InterruptContexts) != 1 {
		t.Fatalf("interrupt contexts = %d, want 1 (loop body QA)", len(info.InterruptContexts))
	}
	// body 是 subgraph（ctx 带 loop 节点地址）:中断 checkpoint 不写 store,
	// 经 SubGraphs 随主 checkpoint 流动 —— 主图 checkpoint 必须落盘
	// （否则 resume 无状态可恢复）。
	if _, ok := store.m["loop-run"]; !ok {
		t.Fatalf("main checkpoint missing; store=%v", storeKeys(store.m))
	}
}

// HITL 每迭代确认:首轮中断 → resume 放行迭代 0 → 迭代 1 再次中断 →
// resume 放行迭代 1 → 完成,输出两个迭代的结果。
func TestQALoopResumePerIteration(t *testing.T) {
	store := &qaMemStore{}
	r := mustCompile(t, New(testRegistry()), qaLoopSpec(), compose.WithCheckPointStore(store))
	ctx := context.Background()

	// run 1:iter 0 中断
	_, err := r.Invoke(ctx, map[string]any{}, compose.WithCheckPointID("loop-run"))
	info, ok := compose.ExtractInterruptInfo(err)
	if !ok {
		t.Fatalf("run1: expected interrupt, got %v", err)
	}
	id0 := info.InterruptContexts[0].ID
	if id0 == "" {
		t.Fatal("run1: empty interrupt id")
	}

	// run 2:注入 iter 0 的确认 → 迭代 1 应再次中断（而非直接完成）
	rctx := compose.ResumeWithData(ctx, id0, map[string]any{"choice": "ok", "comment": "resp0"})
	_, err = r.Invoke(rctx, map[string]any{}, compose.WithCheckPointID("loop-run"))
	info, ok = compose.ExtractInterruptInfo(err)
	if !ok {
		t.Fatalf("run2: expected iter-1 interrupt (HITL per-iteration), got %v", err)
	}
	id1 := info.InterruptContexts[0].ID
	if id1 == "" || id1 == id0 {
		t.Fatalf("run2: interrupt id must be a fresh one, got %q (was %q)", id1, id0)
	}
	// 主 checkpoint 被迭代 1 的中断覆盖（SubGraphs 指向迭代 1 的 body
	// checkpoint,RuntimeState 快照含 NextIter=1 / IterOuts=[out0]）。
	if _, ok := store.m["loop-run"]; !ok {
		t.Fatalf("run2: main checkpoint missing; store=%v", storeKeys(store.m))
	}

	// run 3:注入 iter 1 的确认 → 完成
	rctx = compose.ResumeWithData(ctx, id1, map[string]any{"choice": "ok", "comment": "resp1"})
	out, err := r.Invoke(rctx, map[string]any{}, compose.WithCheckPointID("loop-run"))
	if err != nil {
		t.Fatalf("run3: final resume: %v", err)
	}
	got := UnwrapOutput(out)
	arr, _ := got["output"].([]any)
	if len(arr) != 2 {
		t.Fatalf("run3: output = %#v, want 2 iterations", got)
	}
	for i, want := range []string{"resp0", "resp1"} {
		s, _ := arr[i].(string)
		if !strings.Contains(s, want) {
			t.Fatalf("output[%d] = %q, want contains %q", i, s, want)
		}
	}
	t.Logf("PASS loop HITL per-iteration: outputs=%v", arr)
}

// 回归:无 checkpoint store 时 per-iteration checkpoint 禁用,loop 照常跑
// （v2 不破坏 M1 纯运行时路径）。
func TestQALoopNoStoreRegression(t *testing.T) {
	r := mustCompile(t, New(testRegistry()), qaLoopSpec())
	if _, err := r.Invoke(context.Background(), map[string]any{}); err == nil {
		t.Fatal("expected interrupt (no store, QA still interrupts)")
	} else if _, ok := compose.ExtractInterruptInfo(err); !ok {
		t.Fatalf("expected interrupt error, got %v", err)
	}
}
