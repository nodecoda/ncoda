package ir

// fuseBranchChains 把 `else if` 链（canonical AST 展平后的**嵌套 if**）融成
// **一个 N 臂条件节点**（平台自己的多分支条件节点形状）。
//
// ── 为什么（一手，2026-10-01）──────────────────────────────────────────
// 平台导出：`7531678280082210855_X212_V_book_video_v3_1` 节点 `110750` =
// **1 个** condition 节点，`branches` 两条，出端口 `true` / `true_1` / `false`
// （coze-studio `nodes/selector/selector.go::ExpectPorts` 逐字：
// `expects[0]="false"` · `expects[1]="true"` · `expects[i+1]="true_"+i`；
// `Invoke` 逐条求值、第一个为真即走该臂）。
// 我们的往返：读回 = `if … else if … else …`（读得懂）；再编 = **2 个**单分支
// condition 节点（写不回）。⇒ 本 pass 补齐**条件头**这一侧。
//
// **汇聚面早就是 N 元的**：`FlattenChainAggregates`（同入口、更早运行）把内层
// join 折成消费点单 merge ⇒ 本 pass 只动**条件头**，候选表一字不改（语义保持）。
//
// ── 规则（无形态枚举；准入只有结构与「内层 join 已折掉」两件事）────────
// 宿主 H 的 Else 臂块**恰好一条语句**且该语句是 IfStmt B，且 B 的 join 已不在
// 场（`B.Merge == nil` —— 内层 junction 是纯脚手架，折掉后 B 的臂写直接进 H 的
// 汇聚；join 还在 ⇒ 它的节点是真的，不动）、两处都没有 Relay ⇒ 把 B 的条件并入
// H 的臂表（B.Then 成为 H 的中臂，H 的兜底臂 = B.Else），H.Else 块与 B 一起退场。
// 递归到不动点（3 层链逐层外展）。
//
// ── 谁不需要改 ────────────────────────────────────────────────────────
// 本 pass **不**在共享 `optimizeGraph` 内运行（调用点 = coze-biz `generate()`
// 的专属规范化段、渲染前最后一站）：参考目标 / coze-studio / runtime 的语句树里
// `ChainArms` 恒为 nil（`walkStmtBlocks` 的 ChainArms 支只在有生产者时才有输入），
// 既有结果一字不变。
//
// ── 与 id 空间的关系 ──────────────────────────────────────────────────
// B 的条件节点 id 退场（它的臂被提级），**不重编号任何其它节点**：产物 id 集 =
// 「分配过 − 落地」的压缩，与 `PruneDeadPureNodes` 同一约定。
func FuseBranchChains(g *Graph) {
	if g == nil || g.Root == nil {
		return
	}
	fuseBranchChainsBlock(g.Root)
}

func fuseBranchChainsBlock(b *Block) {
	if b == nil {
		return
	}
	for _, stmt := range b.Stmts {
		// 先递归（自底向上）：内层链先融，外层再吸收 —— 3 层链因此一趟到底。
		walkStmtBlocks(stmt, fuseBranchChainsBlock)
		if s, ok := stmt.(*IfStmt); ok {
			fuseBranchChainInto(s)
		}
	}
}

// fuseBranchChainInto 尝试把 s 的 Else 臂里那条链融进 s（一次一条，直到不可融）。
func fuseBranchChainInto(s *IfStmt) {
	for {
		b, ok := chainTail(s)
		if !ok {
			return
		}
		// B 的条件 + B 的中臂，按序接到 s 的臂表尾部（s.Else 之后被 B.Else 取代）。
		s.ChainArms = append(s.ChainArms, &ChainArm{Cond: b.Cond.Conditions, Logic: b.Cond.Logic, Block: b.Then})
		s.ChainArms = append(s.ChainArms, b.ChainArms...)
		s.Else = b.Else
	}
}

// chainTail 判定「s 的 Else 臂是不是一条可融的链尾」，是则返回内层 IfStmt。
//
// 准入（每一条都是**结构事实**或**已经成立的规范化前提**，不是形态偏好）：
//   - Else 臂块恰好一条语句（多了 ⇒ 该块不是纯链尾，融了会打乱块内序）；
//   - 那条语句是 IfStmt（唯一可能的链尾形状）；
//   - 内层 join 不在场（Merge == nil）：join 还在 ⇒ 它是要发到产物的真节点；
//   - 两侧都没有 Relay：Relay 的**全部结构角色** = 无节点臂的真口靶子，重定向它
//     需要把「真口靶子」一并搬到新端口上 —— 那是另一件事，本 pass 只做保守形态，
//     遇到就**不融**（保持既有两节点形状，语义与今天逐字相同）。
func chainTail(s *IfStmt) (*IfStmt, bool) {
	if s.Else == nil || len(s.Else.Stmts) != 1 || s.Relay != nil {
		return nil, false
	}
	b, ok := s.Else.Stmts[0].(*IfStmt)
	if !ok || b.Merge != nil || b.Relay != nil {
		return nil, false
	}
	// B 无兜底臂（`else if` 无尾 else）也可融：融掉后宿主自己变成 else-less，
	// 它的 `false` 端口义务落到**与 B 原来完全相同**的续接点（B 的未匹配路径
	// 本来就出 else 臂、汇入宿主之后的那一条续接）⇒ 端口义务与汇聚候选表
	// 逐字沿用旧规则。两侧的 false 义务最终由同一个 `owe/drain` 机制落到同一个
	// 节点上（渲染期一条链路，不是第二份判定）。
	if b.Cond == nil {
		return nil, false
	}
	return b, true
}
