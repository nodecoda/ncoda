package compile

// qa_arm_return_value_contract_test.go — N-RET-1 的 QA 相位（§5.8）：
// `ask ... { action X { return v; } }` 的臂内 return = 该分支的结果值。
//
// 夹具 = testdata/obligations/ask/return_arms.ncoda（真实 P5 构造语料）：
// approve 臂返回表单字段 review.comment；reject 臂返回常量 "rejected"。
// 走真实 HITL 链：首次 Invoke → interrupt/checkpoint → ResumeWithData →
// 第二次 Invoke 得到臂值。这样判的是「源文本在本地 runtime 上的值」，
// 不是平台 wire 形状。

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/compose"
)

func qaArmReturnRunnable(t *testing.T, store *qaMemStore) Runnable {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "obligations", "ask", "return_arms.ncoda"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return qaRunnableFromSource(t, string(src), store)
}

// qaRunnableFromSource 让同一套 HITL 判据也能跑**往返后的文本**：P5 的
// ask 结构 gap 必须在运行期证伪/证实，而不是只看源侧。
func qaRunnableFromSource(t *testing.T, src string, store *qaMemStore) Runnable {
	t.Helper()
	s := qaLoopFromSource(t, src)
	return mustCompile(t, New(testRegistry()), s, compose.WithCheckPointStore(store))
}

func runQAArmReturn(t *testing.T, cpID string, answer map[string]any) map[string]any {
	t.Helper()
	return runQASource(t, cpID, answer, func(store *qaMemStore) Runnable {
		return qaArmReturnRunnable(t, store)
	})
}

// runQASource runs one ask source to completion through interrupt → resume and
// returns the workflow output. build decides which source text is under test.
func runQASource(t *testing.T, cpID string, answer map[string]any, build func(*qaMemStore) Runnable) map[string]any {
	t.Helper()
	store := &qaMemStore{}
	r := build(store)
	if _, err := r.Invoke(context.Background(), map[string]any{}, compose.WithCheckPointID(cpID)); err == nil {
		t.Fatal("expected QA interrupt, got nil error")
	} else if info, ok := compose.ExtractInterruptInfo(err); !ok {
		t.Fatalf("expected interrupt, got %v", err)
	} else if len(info.InterruptContexts) != 1 {
		t.Fatalf("interrupt contexts = %d, want 1", len(info.InterruptContexts))
	} else {
		rctx := compose.ResumeWithData(context.Background(), info.InterruptContexts[0].ID, answer)
		out, err := r.Invoke(rctx, map[string]any{}, compose.WithCheckPointID(cpID))
		if err != nil {
			t.Fatalf("resume: %v", err)
		}
		return UnwrapOutput(out)
	}
	return nil
}

func TestQAArmReturnIsValue(t *testing.T) {
	if got := runQAArmReturn(t, "qa-arm-approve", map[string]any{"choice": "approve", "comment": "looks good"}); got["output"] != "looks good" {
		t.Errorf("approve 臂 return review.comment ⇒ %v, want looks good", got["output"])
	}
	if got := runQAArmReturn(t, "qa-arm-reject", map[string]any{"choice": "reject"}); got["output"] != "rejected" {
		t.Errorf("reject 臂 return \"rejected\" ⇒ %v, want rejected", got["output"])
	}
}
