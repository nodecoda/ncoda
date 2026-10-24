package compile

// loop_cancel_test.go — B 测试治理:loop 驱动器 ctx 取消终止防护。
//
// 背景:loop 无全局迭代上限（对齐 coze 语义,靠 body 内 break/return
// 退出）。驱动器每迭代检查 ctx.Err() —— 保证外部 cancel 总能终止循环,
// 即使 body 是纯进程内节点（assign,不感知外部信号）。本文件锁定防护:
//   1. 已取消 ctx → 驱动器在首次迭代前返回错误（不进入循环）;
//   2. 运行中超时 → 提前终止,不悬挂到迭代耗尽。
//
// 修复来源:loopDriver.Invoke 迭代循环原无 ctx 检查,超长 loop_count +
// 纯进程内 body 场景会悬挂到耗尽;ctx 取消是 loop 唯一的外部终止防护。

import (
	"context"
	"testing"
	"time"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// loopCancelSpec 构造超长 by_iteration 循环（body 为进程内 assign,无 IO、
// 不感知外部信号 —— 驱动器级检查是唯一能终止的路径）:
// start → loop(count=N) → end。
func loopCancelSpec(n int) *spec.Spec {
	return &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("loop", spec.KindLoop, &spec.LoopConfig{
				LoopType:  "by_iteration",
				LoopCount: n,
				Children: []spec.NodeSpec{
					nodeSpec("b1", spec.KindAssign, &spec.AssignConfig{
						VarName: "tick",
						Value:   spec.Source{Val: 1},
					}),
				},
				InnerEdges: []spec.EdgeSpec{
					{Source: "loop", Target: "b1", SourcePort: wLoopFunctionInlineOutput},
					{Source: "b1", Target: "loop", TargetPort: wLoopFunctionInlineInput},
				},
			}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "loop"},
			{Source: "loop", Target: "e1"},
		},
	}
}

// TestLoopCancelPreCancelled:已取消 ctx → 首次迭代前返回错误（直接证据）。
func TestLoopCancelPreCancelled(t *testing.T) {
	r := mustCompile(t, New(testRegistry()), loopCancelSpec(100000))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.Invoke(ctx, map[string]any{})
	if err == nil {
		t.Fatal("pre-cancelled ctx: want error, got nil (driver must check ctx before iterating)")
	}
	t.Logf("pre-cancelled: %v", err)
}

// TestLoopCancelMidRun:运行中超时 → 提前终止,不悬挂到迭代耗尽。
// 防护缺失时 500000 次纯进程内迭代会跑完全部（测试靠外部超时打死 = 挂起）。
func TestLoopCancelMidRun(t *testing.T) {
	r := mustCompile(t, New(testRegistry()), loopCancelSpec(500000))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := r.Invoke(ctx, map[string]any{})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("mid-run cancel: want error, got nil (loop ran to completion?)")
		}
		t.Logf("loop terminated on cancel: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("loop did not terminate after ctx timeout (suspension)")
	}
}
