package ir

// collapse_degenerate_parallels.go — 退化栅栏的坍缩（coze-biz 规范化段）。
//
// 背景（2026-10-11 一手，种子枚举 `gen_if/d3/s1/seed6` 的唯一残留）：
//
//	源 `if (n==1) { http } else { parallel { { if (n==4) {…} else {…} } { v7 } } }`
//	里，臂 2 的 `v7` 无人消费 ⇒ `PruneDeadPureNodes` 把该臂清空 —— 栅栏只剩
//	**一条有语句的臂**。渲染面（`renderParallel`）对此早已是**恒等**：没有一条
//	臂发射节点时，尾 = 扇出起源本身（空臂条款），单活臂的产物与「直接写那条
//	臂的语句」逐边相同。可**语句树**里那个 `ParallelStmt` 还在 ⇒ 外层 `IfStmt`
//	的 Else 块 = `[ParallelStmt]`（不是 `[IfStmt]`）⇒ 紧随其后的
//	`FuseBranchChains` 的准入（「Else 恰一条语句且是 IfStmt」）不成立 ⇒ 该融的
//	`else if` 链没融 ⇒ 首编 2 个 condition、二编 1 个 ⇒ `I1b ARTIFACT_DRIFT/
//	TOPOLOGY`（一轮往返不落点）。
//
// 规则（一条，普遍、无形态守卫）：
//
//	`ParallelStmt` 的**至多一条臂**还有语句 ⇒ 该栅栏是图上的**恒等**，用那条臂
//	的语句序列**就地替换**它（全空 ⇒ 整条删除）。
//
// 语义论证（等价变换，不是近似）：栅栏的汇聚 = 各臂尾值的并集，没有 merge 节点
// （`ParallelStmt` 头注：「the junction is the collection of all branch ends (no
// merge node)」）。空臂发射不出任何节点 ⇒ 它对并集与图都无贡献；只剩一条臂有
// 语句时，并集 = 那条臂的尾、控制流 = 「执行那条臂」⇒ 与顺序执行那串语句**逐边
// 相同**。这不是新语义，是 `renderParallel` 空臂条款在**语句树**上的同一个事实。
//
// 「有语句」= `len(branch.Stmts) > 0`，是**保守**判据：宁可少坍缩（留着栅栏，
// 行为与今天逐字相同），不误坍缩（`rendered` 的渲染期判据在语句树上没有对应物，
// 用它会把只在臂里 return 的形状算成空臂）。两条以上臂有语句 ⇒ 真栅栏，不动。
//
// 调用点：coze-biz lowering 入口，`PruneDeadPureNodes` **之后**（死臂要在坍缩前
// 先被清空）、`CanonicalZeroCarrierOrder` 与 `FuseBranchChains` **之前**：坍缩后
// 语句树与「同一意思的 else-if 源」逐句相同 ⇒ 其后所有规范化 pass 在**同一棵**
// 树上求值，首编产物与二编产物构造相同。
// 共享 IR 的参考目标 / coze-studio / runtime 路径不调用（那里的链式聚合是真实
// canvas 形状，删掉会改其既有结果）。

// CollapseDegenerateParallels 把「至多一条臂有语句」的并行栅栏就地坍缩为顺序
// 语句（自底向上一趟：内层栅栏先坍缩，外层臂的内容才成形）。
func CollapseDegenerateParallels(g *Graph) {
	if g == nil || g.Root == nil {
		return
	}
	collapseDegenerateParallelsBlock(g.Root)
}

func collapseDegenerateParallelsBlock(b *Block) {
	if b == nil {
		return
	}
	// 先递归进子块（先内后外）：内层栅栏坍缩后，外层臂的内容才是最终形状。
	for _, s := range b.Stmts {
		walkStmtBlocks(s, collapseDegenerateParallelsBlock)
	}
	out := make([]Stmt, 0, len(b.Stmts))
	for _, s := range b.Stmts {
		p, ok := s.(*ParallelStmt)
		if !ok {
			out = append(out, s)
			continue
		}
		var live *Block
		liveCount := 0
		for _, br := range p.Branches {
			if br != nil && len(br.Stmts) > 0 {
				live = br
				liveCount++
			}
		}
		if liveCount > 1 {
			out = append(out, s)
			continue
		}
		if live != nil {
			out = append(out, live.Stmts...)
		}
	}
	b.Stmts = out
}
