package compile

// qa_loop_cli_repro_test.go — M16-2: 前端全链源（loop + request_input）的
// HITL-in-loop resume 回归。覆盖 by_array loop:resume 时 loop 节点输入
// 通道是零值（内部中断不持久化输入）,驱动器用 LoopState.ArrayInputs
// 存档恢复迭代计数 —— 这是 by_iteration 单元 spec 测不到的形态。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/compose"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// hitlLoopSrc:for (item in items) 内 request_input 每迭代确认,yield item。
// request_input 是语句（绑定仅分支内可见）,QA 返回不参与 loop 输出 ——
// 验证重点是每迭代中断/恢复链路,不是分支值。
const hitlLoopSrc = `@language nodecoda/1
@mode workflow

enum Confirm { ok }
type ReviewForm = { string comment; }

function main() {
    let items = ["a", "b"];
    var answers = for (item in items) {
        request_input<ReviewForm, Confirm>({
            content: "Confirm ${item}?",
            fields: { comment: { kind: "paragraph" } },
            actions: {
                ok: { title: "OK", style: "primary" }
            },
            timeout: { value: 3, unit: "day" }
        }) as ri {
            action Confirm.ok { return "ok"; }
            timeout { return "timeout"; }
        }
        yield item + "!";
    };
    return answers;
}
`

// qaLoopFromSource 跑前端全链（passes → ir → flow.Project → spec.v1）。
func qaLoopFromSource(t *testing.T, src string) *spec.Spec {
	t.Helper()
	res := passes.DefaultPipeline().Run(src, "repro.ncoda", nil)
	if res.HasErrors() {
		for _, d := range res.Errors() {
			t.Logf("frontend: %s", d.Message)
		}
		t.Fatalf("frontend errors")
	}
	g, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("ir build: %v", err)
	}
	p, err := flow.Project(g)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	s, err := spec.Build(g, p)
	if err != nil {
		t.Fatalf("spec build: %v", err)
	}
	return s
}

// 完整 HITL-in-loop:run1 中断(iter0) → resume → 中断(iter1) → resume →
// 完成,输出两个 yield 值。by_array 循环输入在 resume 时从
// LoopState.ArrayInputs 恢复（断言中断二次出现即证明迭代计数正确）。
func TestQALoopByArrayResumePerIteration(t *testing.T) {
	s := qaLoopFromSource(t, hitlLoopSrc)
	store := &qaMemStore{}
	r := mustCompile(t, New(nodes.StdRegistry()), s, compose.WithCheckPointStore(store))
	ctx := context.Background()

	// run 1:iter 0 中断
	_, err := r.Invoke(ctx, map[string]any{}, compose.WithCheckPointID("byarr"))
	info, ok := compose.ExtractInterruptInfo(err)
	if !ok {
		t.Fatalf("run1: expected interrupt, got %v", err)
	}
	id0 := info.InterruptContexts[0].ID

	// run 2:放行 iter 0 → iter 1 再次中断（HITL 每迭代）
	rctx := compose.ResumeWithData(ctx, id0, map[string]any{"choice": "ok", "comment": "c0"})
	_, err = r.Invoke(rctx, map[string]any{}, compose.WithCheckPointID("byarr"))
	info, ok = compose.ExtractInterruptInfo(err)
	if !ok {
		t.Fatalf("run2: expected iter-1 interrupt (by_array resume must restore iteration count), got %v", err)
	}
	id1 := info.InterruptContexts[0].ID
	if id1 == id0 {
		t.Fatalf("run2: interrupt id must be fresh")
	}

	// run 3:放行 iter 1 → 完成
	rctx = compose.ResumeWithData(ctx, id1, map[string]any{"choice": "ok", "comment": "c1"})
	out, err := r.Invoke(rctx, map[string]any{}, compose.WithCheckPointID("byarr"))
	if err != nil {
		t.Fatalf("run3: %v", err)
	}
	got := UnwrapOutput(out)
	b, _ := json.Marshal(got)
	for _, want := range []string{`"a!"`, `"b!"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("run3: output = %s, want contains %s", b, want)
		}
	}
	t.Logf("PASS by_array loop HITL per-iteration: output=%s", b)
}
