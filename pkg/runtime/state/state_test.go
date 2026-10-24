package state

// state_test.go — RuntimeState 结构语义单测（M2.3 状态面）。
//
// 锁:EnterLoop/PopLoop 栈语义（嵌套 loop 定位 break/continue 目标）、
// Vars 跨迭代持久、Loop 键隔离（嵌套 loop 互不覆盖）。

import "testing"

func TestEnterPopNested(t *testing.T) {
	rs := New()

	outer := rs.EnterLoop("outer")
	if rs.TopLoop() != outer {
		t.Fatal("top after outer enter != outer")
	}
	inner := rs.EnterLoop("inner")
	if rs.TopLoop() != inner {
		t.Fatal("top after inner enter != inner")
	}
	if rs.Loop("outer") != outer || rs.Loop("inner") != inner {
		t.Fatal("Loop() lookup broken")
	}

	// inner writes must not leak into outer's state object
	inner.Vars["x"] = 1
	if _, ok := outer.Vars["x"]; ok {
		t.Fatal("inner write leaked into outer Vars")
	}

	rs.PopLoop()
	if rs.TopLoop() != outer {
		t.Fatal("top after inner pop != outer")
	}
	rs.PopLoop()
	if rs.TopLoop() != nil {
		t.Fatal("top after outer pop != nil")
	}
	// 空栈 Pop 是 no-op（防 panic）
	rs.PopLoop()
}

func TestVarsPersistAcrossEnter(t *testing.T) {
	rs := New()
	ls := rs.EnterLoop("l")
	ls.Vars["acc"] = "a"
	rs.PopLoop()

	// 同一 loop 再次进入（模拟嵌套驱动循环?实际 per-run 只进一次;
	// 语义契约:Vars 不因 Enter/Pop 重置）
	ls2 := rs.EnterLoop("l")
	if ls2.Vars["acc"] != "a" {
		t.Fatalf("Vars reset on re-enter: %v", ls2.Vars)
	}
	rs.PopLoop()
}

func TestLoopIsolation(t *testing.T) {
	rs := New()
	a := rs.EnterLoop("a")
	b := rs.EnterLoop("b")
	if a == b {
		t.Fatal("distinct loops share state object")
	}
}

// TestRuntimeStateConcurrentLoopEntry 是 **R-129 的判据**（2026-09-28）：
// loop 驱动器**可以并发** —— eino 的并行分支各自 Invoke 一个 loopDriver ⇒
// 两个 loop 同时 EnterLoop/Loop/TopLoop/PopLoop。
//
// 一手（672 源跑批，2026-09-28）：未同步时命中
//
//	`fatal error: concurrent map writes` @ state.go `s.Loops[id] = ls`
//	（栈：state.(*RuntimeState).EnterLoop ← compile.(*loopDriver).Invoke）
//
// ⇒ 整趟 rt-l3 被内核 abort（RC=2），**全量判据面因此不可用**。
//
// 必须用 `-race` 跑才有判据力：`go test -race ./internal/runtime/state/`
// （入口 = Makefile `race-runtime`）。撤掉 mu 的三个锁 ⇒ 本用例当场 race 红。
func TestRuntimeStateConcurrentLoopEntry(t *testing.T) {
	rs := New()
	const n = 8
	start := make(chan struct{})
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			<-start
			id := "loop" + string(rune('a'+i))
			ls := rs.EnterLoop(id)
			ls.Vars["k"] = i // 各自 loop 的状态对象，互不共享
			if got := rs.Loop(id); got != ls {
				t.Errorf("Loop(%s) != 该 goroutine 的 LoopState", id)
			}
			_ = rs.TopLoop() // 与 EnterLoop 的 stack append 竞争
			rs.PopLoop()
			done <- struct{}{}
		}(i)
	}
	close(start)
	for i := 0; i < n; i++ {
		<-done
	}
	if got := len(rs.Loops); got != n {
		t.Fatalf("并发进 loop 后 Loops 应含 %d 个 id，实际 %d", n, got)
	}
}
