package ir

// arm_carrier_uniqueness_contract_test.go — **链式折叠门控的载体唯一性**（R-50，
// 2026-09-24）。
//
// 规则（一条）：候选的身份 = **(值的来源, 门控点)**；同一条臂里同一个来源只允许
// **一份**恒等载体，同一个值在候选表里只允许**出现一次**。
//
// 为什么需要它（一手实测，`nested_junction_prebinding/d2/s1`）：
//
//	int pv1; pv1 = 0;
//	if (n == 11) { pv1 = n; if (n == 20) { if (n == 21) { pv1 = 101; } } }
//	return pv1;
//
// 链式折叠把内层聚合的候选逐层搬进外层组。深度 ≥ 2 时外层组要搬**两次**，而两次
// 搬运都会带上内层聚合的**继承前绑定候选**（同一个 `start.n`）—— 修前第二次搬运
// 又铸一份同形 relay ⇒ 组候选 = `[101, relay←n, relay←n, 0]`（4 条），raising 侧
// 把这种形态读成单字段记录单元 ⇒ **`n` 那一臂整条消失**（`n == 11` 返回 `0`，
// 期望 `11`）。而且「先折外层还是先折内层」由 Go map 迭代序决定 ⇒ 同一份源码
// 两次编译得到**两个不同产物**（12 次实测 2 个 sha，1 份 / 2 份 relay 各半）。
//
// 本文件锁**规则本身**（与遍历序无关）：`canonicalizeSplicedCandidates` 必须在
// 门控后按值的来源身份去重，且同臂同源已有载体时必须**复用**而不是再铸一份。
// 端到端面（值表 + 逐字节确定性）见 internal/runtime/（商业仓）compile 的
// `nested_chain_depth2_contract_test.go`；正本 = J §46。

import "testing"

func TestSplicedArmCarrierReusedNotDuplicated(t *testing.T) {
	src := Ref{Producer: "100001", Output: "n"}
	zero := Ref{Producer: "100002", Output: "result"}
	typ := T("integer")

	arm := &Block{}
	alloc := newGraphIDAlloc(map[string]Node{})
	first := materializeArmCarrier(alloc, arm, src, typ)
	if len(arm.Stmts) != 1 {
		t.Fatalf("首次物化应前插一条语句，实测 %d", len(arm.Stmts))
	}

	// 同一个来源又被搬进这条臂（第二次 splice 的继承前绑定候选，落在非默认位）。
	refs := []Ref{first, src, zero}
	out := canonicalizeSplicedCandidates(alloc, arm, refs, []armRelayNeed{{idx: 1, src: src, typ: typ}})

	if len(arm.Stmts) != 1 {
		t.Errorf("同臂同源已有载体时又铸了一份（臂内语句 %d，期望 1）—— 复用规则失效", len(arm.Stmts))
	}
	if len(out) != 2 {
		t.Fatalf("候选表应去重为 [载体, 零] 两条，实测 %d 条：%#v", len(out), out)
	}
	if out[0] != first {
		t.Errorf("第 1 位应复用既有载体 %v，实测 %v", first, out[0])
	}
	if out[1] != zero {
		t.Errorf("第 2 位应保持零载体 %v，实测 %v", zero, out[1])
	}
}

func TestSplicedArmCarrierDedupedWithinOneSplice(t *testing.T) {
	src := Ref{Producer: "100001", Output: "n"}
	zero := Ref{Producer: "100002", Output: "result"}
	typ := T("integer")

	arm := &Block{}
	alloc := newGraphIDAlloc(map[string]Node{})
	// 同一次搬运把同一个来源放在两个位置（内层聚合自己也有重复候选的形态）。
	refs := []Ref{src, src, zero}
	out := canonicalizeSplicedCandidates(alloc, arm, refs, []armRelayNeed{
		{idx: 0, src: src, typ: typ},
		{idx: 1, src: src, typ: typ},
	})

	if len(arm.Stmts) != 1 {
		t.Errorf("同一次搬运里的同一个来源只该铸一份载体，实测臂内语句 %d", len(arm.Stmts))
	}
	if len(out) != 2 {
		t.Fatalf("候选表应去重为两条，实测 %d 条：%#v", len(out), out)
	}
	if out[0] != out[1] && out[1] != zero {
		t.Errorf("去重后应保留载体与零载体：%#v", out)
	}
	if out[0].Producer == out[1].Producer {
		t.Errorf("同一个值的来源在候选表里出现了两次：%#v", out)
	}
}
