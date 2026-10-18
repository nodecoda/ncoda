package ir

import (
	"strings"

	"github.com/nodecoda/ncoda/pkg/pyast"
)

// prune_dead_pure_nodes.go — 「无消费者的纯节点」清理（IR 优化阶段的下半场）。
//
// 背景（2026-09-13 第一手，docs/ideas-todo/
// 2026-09-13-x23-identity-shim-and-merge-dup-analysis.md §6/§7）：
//
//	真实导出 672 工作流 **0** 个「没有任何数据引用」的节点；我们的自产产物
//	首编 671 份里 **1220** 个、二编 **1473** 个（Synthetic shim：臂前字面量写
//	的常量载体、臂内恒等 relay、零值 record 载体……只挂在控制流链上，产出
//	没人读）。优化期已有的规则只删「无消费者的 Aggregate」
//	（optimize.go 头注自陈 “not dead shims”）。
//
// 规则（一条，普遍、无形态守卫）：
//
//	删除 n ⟺ pure(n) ∧ ¬consumed(n) ∧ ¬structuralCarrier(n)
//
//	· pure：编译器自建的封闭子集 shim CodeRun（Synthetic ∧ 带单源 marker），
//	  以及原生纯表达式节点 TextProcess。**用户手写 foreign code（Synthetic=false）
//	  与插件/LLM/http 等有副作用的节点永不删除**——源码里那次调用真的发生了。
//	· consumed：任何真实引用站点读了它（共享消费表 collectIRConsumers 的全部
//	  通道 + 语句级尾值通道 + 容器输入/输出/循环状态）。判据永远是「有没有人读」，
//	  不是「长得像不像」。
//	· structuralCarrier：宿主节点（condition/switch/QA/attempt/loop 容器、
//	  merge、relay、end、Entry）以及语句级数据通道的生产者（见
//	  statementLevelProducers）。删掉它们 = 删掉控制流/契约本身，不是清理。
//
// 语义论证（等价变换，非近似）：纯节点无副作用、其输出无观察者 ⇒ 删掉后
// 程序的可观察行为与产物形状一一对应地不变（平台上「算了没人用」的节点
// 消失，控制流由渲染顺序重新接续）。
//
// 空臂条款（A 级，2026-09-13 用户裁定）：「在极端情况下，两臂都空也不能算错，
// 语义上无错误，语法上无错误」——空臂是**合法 ncoda 结构**，不是错误；编译器
// 自建的死纯节点是开发期脚手架。**删除规则里没有「块会变空就保守保留」这一条**：
//
//  1. 臂清空后，宿主的**端口义务**由 lowering 既有的**一个义务表**
//     （renderEnv.pendingPorts，P15）闭合——「臂渲染不出入口节点 ⇒ 该臂欠该
//     端口一条显式边」是既有规则（`armEntry(arm) == ""` ⇒ `owe(...)`），
//     五个销账点（下一节点 / 汇聚 / 容器尾 / 控制节点 / 文档尾）一个不少，
//     不需要死节点当靶子。真实导出实证：X97 `cond --true--> merge` 与
//     `cond --false--> merge`（两臂都空、两端口指向同一汇聚节点）。
//  2. 旧守卫（`len(candidates) >= renderable` ⇒ 整块放弃清理）的依据
//     「分支端口边将失去目标」经实测为**假**：被它保下来的 671 份首编里恰好
//     **251 个**死纯节点，100% 原因都是这一条，全在臂内
//     （`/loop-body/if-then` 120、`/loop-body/if-else/if-then` 60、`/if-then` 22…），
//     **没有一个**是容器体本身。
//  3. 容器体被清空时容器按现实渲染为零子节点的容器（语料 452 个真实
//     loop/batch 容器中 0 个空容器 ⇒ 该形状我们的自产物独有、无平台反证），
//     反编译读回 `for (...) { }`（合法 ncoda），再编译回同一产物；
//     递归形态（循环体内的空臂）由容器尾的 `closeBodyPorts` 闭合。
//
// 本 pass 内部交替运行 optimizeGraph（聚合判死 = 同一份 J6 活性判据的另一半：
// 删纯生产者 ⇒ 只喂它的聚合变死），使「纯节点清理」与「死聚合清理」在同一个
// 不动点里收敛 —— 两步互为输入，各自跑一次会停下半个级联。
//
// 调用点：coze-biz lowering 入口、**FlattenChainAggregates 之后**（那次折叠会
// 重写候选槽并删除内层聚合，从而新产生孤儿；早于它的清理会在第二轮重新长出
// 来——B2(1473) > B1(1220) 即此现象）。共享 IR 的 dify/coze-studio 路径不调用。

// PruneDeadPureNodes 删除所有「纯 ∧ 无消费者 ∧ 非结构承载」的节点，迭代到
// 不动点（删一个叶子可能让只喂它的上游变死）。返回**被删节点的 id**（按
// 删除轮次、同轮内按块内顺序；调用方用它做 id 空间中性化，见 render.go）。
func PruneDeadPureNodes(g *Graph) []string {
	if g == nil {
		return nil
	}
	var removed []string
	for {
		// 级联的另一半（2026-09-21）：删掉纯生产者会让「只喂它的聚合」变死
		// （J6：活性 = 被数据引用），而聚合判死是 optimizeGraph 的规则 —— 一处
		// 定义 ⇒ 复用它，不在这里重写。两步互为输入（聚合死了 ⇒ 它的候选生产者
		// 可能变死；生产者删了 ⇒ 聚合可能变死）⇒ 必须在**同一个**不动点里交替，
		// 直到两者都不再变化。少这一步的实测后果（生成器形状
		// `phi_ternary_in_concat/d0/s2`）：产物留下**无人消费的 variable_merge**
		// （D1 死载体）且级联停在半路（18 节点 = 死 merge + 宿主 + 已删中间生产
		// 者），二编再丢两个 ⇒ 三轮都不落点。
		optimizeGraph(g)
		nodes := collectIRNodes(g)
		consumers := collectIRConsumers(g, nodes)
		carriers := armCarrierSites(g)
		protected := statementLevelProducers(g)
		hosts := hostNodeIDs(g)

		dead := map[string]bool{}
		forEachBlock(g.Root, func(b *Block) {
			// 判据只有一条（pure ∧ 无消费者 ∧ 非结构承载），**没有**
			// 「删了块会变空就保守保留」这一条——见文件头的空臂条款。
			for _, s := range b.Stmts {
				ns, ok := s.(*NodeStmt)
				if !ok {
					continue
				}
				id := ns.Node.NodeID()
				if id == "" || consumedOutsideArmCarriers(consumers, carriers, id) || protected[id] || hosts[id] {
					continue
				}
				if !isPurePrunableNode(ns.Node) {
					continue
				}
				dead[id] = true
			}
		})
		if len(dead) == 0 {
			return removed
		}
		forEachBlock(g.Root, func(b *Block) {
			for _, s := range b.Stmts {
				if ns, ok := s.(*NodeStmt); ok && dead[ns.Node.NodeID()] {
					removed = append(removed, ns.Node.NodeID())
				}
			}
		})
		dropNodeStmts(g.Root, dead)
	}
}

// armCarrierSites 收集**臂终端 End**（`AttemptStmt.SuccessEnd` / `FailureEnd`）的
// Inputs 引用站点（producer → port → 站点数）。
//
// 为什么它不是读者（2026-09-22 收口；R-04 残余的一手根因）：
//
//	臂终端 End 是**控制流载体** —— 渲染器把臂终端投影成**端口边**（P4/P15：
//	「臂渲染不出入口节点 ⇒ 该臂欠该端口一条显式边」），**不**把它物化成产物里的
//	数据绑定。产物里臂尾值只有经**汇聚单元**（junction 的候选）才有接收方，而那条
//	通道已经由 `collectIRConsumers` 的 `case *Aggregate: addRefs(v.AllRefs())` 独立
//	统计 —— `AttemptStmt.Exit` 的头注写明：Refs = 各臂逃逸值，**与 SuccessEnd /
//	FailureEnd 携带的是同一批引用**。⇒ 汇聚单元不存在（该 attempt 的值整体无人消费）
//	时，载体的 Inputs 是**没有接收方的引用**；把它算作「被消费」= 把纯生产者留在
//	产物里（数据引用 0 次），降级侧读不回来 ⇒ I1b 一轮漂移。
//
// 一手（修前实测）：最小片段
//
//	attempt http(...) { success { let x = n + 1; } failure(e) { } }
//
// 该 shim 在 IR 里的唯一「消费者」= SuccessEnd.Inputs，产物 nodes 5 → 4；
// 生成器 `gen_parallel_d3_s3_seed0` nodes 85 → 83（`ARTIFACT_DRIFT/TOPOLOGY@r2`）。
//
// 同族既有收口（同一判据的第三次落地，方向一致，不是新规则）：
// `build_return.go::dropCollapsedExitCarriers` · `flatten_chain_aggregates.go::tailCarrierScan` ·
// 本文件 `statementLevelProducers` 的 phiLessArms（`End==nil ReturnStmt.Value` 形态）。
//
// 判据仍是「有没有人读」，不是「长得像不像」：这里只扣掉「载体**没有接收方**」的
// 那部分读数（载体仍被 ≥1 条非载体通道读 ⇒ 差值 > 0 ⇒ 保留）。载体身份按
// **End 载体字段**遍历（新增宿主只要带 End 载体就自动进入同一判据），
// 不是按臂/容器形态白名单。
func armCarrierSites(g *Graph) map[string]map[string]int {
	out := map[string]map[string]int{}
	addRef := func(r Ref) {
		if r.Producer == "" {
			return
		}
		if out[r.Producer] == nil {
			out[r.Producer] = map[string]int{}
		}
		out[r.Producer][portName(r.Output)]++
	}
	forEachStmt(g.Root, func(s Stmt) {
		v, ok := s.(*AttemptStmt)
		if !ok {
			return
		}
		for _, e := range []*End{v.SuccessEnd, v.FailureEnd} {
			if e == nil {
				continue
			}
			for _, r := range e.Inputs {
				addRef(r)
			}
		}
	})
	return out
}

// consumedOutsideArmCarriers 报告该节点是否被**臂终端 End 载体之外**的通道读过：
// 某端口的读数减去「臂终端 End 载体」的读数 > 0 ⇒ 真有接收方。
func consumedOutsideArmCarriers(consumers, carriers map[string]map[string]int, id string) bool {
	for port, n := range consumers[id] {
		if n-carriers[id][port] > 0 {
			return true
		}
	}
	return false
}

// consumed 报告该节点是否有任何真实消费者（任一端口被读即算）。
func consumed(consumers map[string]map[string]int, id string) bool {
	for _, n := range consumers[id] {
		if n > 0 {
			return true
		}
	}
	return false
}

// isPurePrunableNode 是纯度白名单：只收「编译器自建、封闭子集、确定性、
// 无副作用」的节点。未知类型一律保留（fail-safe；保留不是错误）。
func isPurePrunableNode(n Node) bool {
	switch v := n.(type) {
	case *CodeRun:
		// Synthetic = 编译器自建 shim（用户 foreign code 为 false）；
		// marker = 单表达式 shim 的封闭子集标记（编译与反编译共用的单源）。
		return v.Synthetic && strings.HasPrefix(v.Code, pyast.ShimMarker)
	case *TextProcess:
		// 原生 TextProcess（字符串格式化）是**纯值生产者**：无副作用、给定输入
		// 确定性产出 ⇒ 与 shim CodeRun 同族，删除条件同一条（J6 活性 = 被数据
		// 引用）。
		//
		// 2026-09-21 收口（此前两轮实验都因收敛性不成立而回滚）：本项的阻塞点从来
		// 不是它自己，而是**臂尾中继**在「臂尾值可证类型零」时仍被物化 —— 那个中继
		// 的消费者为零（实测：产物里 `text {{v}}` 节点不在 merge 候选内），本项一删
		// 它就没了、下一轮 compile 又长回来 ⇒ 299B ↔ 329B 无限 2-环。同日把
		// 「该臂在值上是空的」这条判据的**第二条通道**（值可证类型零）补到
		// build_return.go 的真口臂尾中继物化点（与 materializeBranchDefault 用
		// **同一个谓词** isZeroCarrierNode，不得分叉）之后，中继不再产生、空臂自身
		// 即不动点 ⇒ 本项可收。收口前后对照与复跑命令：
		// docs/five-step/evidence/textprocess-liveness-oscillation.md
		//
		// 它承担的其它角色各自有判据兜底，不需要「按角色保守保留」：
		//   · 分支尾值 relay 由 hostNodeIDs 保护（且零值形态已不再物化）；
		//   · 模板拼接 / answer 的内容由普查的**模板槽**（addTemplate）覆盖；
		//   · 臂入口靶子（端口义务）由 P15 的 pendingPorts 表闭合 ——「臂渲染
		//     不出入口节点 ⇒ 该臂欠该端口一条显式边」是既有规则（空臂条款，
		//     见本文件头注），不需要死节点当靶子。
		return true
	}
	return false
}

// hostNodeIDs 收集「结构承载点」：它们的存在由控制流结构决定，不是值生产者，
// 即使无人读也不得删除。
func hostNodeIDs(g *Graph) map[string]bool {
	hosts := map[string]bool{}
	add := func(id string) {
		if id != "" {
			hosts[id] = true
		}
	}
	if g.Entry != nil {
		add(g.Entry.NodeID())
	}
	forEachStmt(g.Root, func(s Stmt) {
		switch v := s.(type) {
		case *IfStmt:
			if v.Cond != nil {
				add(v.Cond.NodeID())
			}
			if v.Merge != nil {
				add(v.Merge.ID)
			}
			if v.Relay != nil {
				add(v.Relay.NodeID())
			}
		case *SwitchStmt:
			if v.Switch != nil {
				add(v.Switch.NodeID())
			}
			if v.Merge != nil {
				add(v.Merge.ID)
			}
		case *QaStmt:
			if v.QA != nil {
				add(v.QA.NodeID())
			}
			if v.Merge != nil {
				add(v.Merge.ID)
			}
		case *AttemptStmt:
			if v.Op != nil {
				add(v.Op.NodeID())
			}
			if v.Exit != nil {
				add(v.Exit.ID)
			}
			if v.SuccessEnd != nil {
				add(v.SuccessEnd.ID)
			}
			if v.FailureEnd != nil {
				add(v.FailureEnd.ID)
			}
		case *LoopStmt:
			if v.Container != nil {
				add(v.Container.NodeID())
			}
		case *ReturnStmt:
			if v.End != nil {
				add(v.End.ID)
			}
		}
	})
	return hosts
}

// statementLevelProducers 收集**语句级**数据通道的生产者：它们不经过
// collectIRConsumers（那张表覆盖节点参数与聚合候选），但下游真的会读
// （块尾值透传、容器值、循环状态、inline 返回值、agent 查询输入）。
// 保守起见一律计入「被消费」——这些通道只让清理更保守，不会误删。
func statementLevelProducers(g *Graph) map[string]bool {
	out := map[string]bool{}
	addRef := func(r *varRef) {
		if r != nil && r.nodeID != "" {
			out[r.nodeID] = true
		}
	}
	// 陈旧臂值载体（2026-09-21）：`End==nil ReturnStmt.Value` 是**臂的规范化值
	// 载体**——它存在的唯一理由是「把该臂的值交给宿主的汇聚单元（φ）」。宿主
	// 的 φ 一旦不在了（本来就没建，或已被判死删除），这条载体就没有接收方，
	// 它再保护臂内生产者 = 把死节点留在产物里（用户可见的症状：首编产物多出
	// 「死链的中间生产者」，降级侧读不回来 ⇒ I1b 一轮漂移）。
	//
	// 同一形态已经在本仓被独立修过两次、判定相同：`build_return.go` 的
	// `dropCollapsedExitCarriers`（「这些载体被 ReturnStmt.Value 当成语句级数据
	// 通道引用（statementLevelProducers 的保护区），而那条通道是为「分支尾值 /
	// inline 体尾」设的——对「值已由绑定承载的出口」不再成立」）与
	// `flatten_chain_aggregates.go` 的 `tailCarrierScan`（折叠后置空 Value）。
	// 本处是同一判据的**普遍化**：φ 缺失的宿主，其臂内的尾值载体不再算消费。
	phiLessArms := map[*Block]bool{}
	forEachStmt(g.Root, func(s Stmt) {
		var arms []*Block
		var hasPhi bool
		switch v := s.(type) {
		case *IfStmt:
			arms, hasPhi = []*Block{v.Then, v.Else}, v.Merge != nil
		case *SwitchStmt:
			hasPhi = v.Merge != nil
			for i := range v.Branches {
				arms = append(arms, v.Branches[i].Body)
			}
		case *QaStmt:
			hasPhi = v.Merge != nil
			for i := range v.Branches {
				arms = append(arms, v.Branches[i].Body)
			}
		case *AttemptStmt:
			arms, hasPhi = []*Block{v.Success, v.Failure}, v.Exit != nil
		default:
			return
		}
		if hasPhi {
			return
		}
		for _, a := range arms {
			if a != nil {
				phiLessArms[a] = true
			}
		}
	})
	addSource := func(s Source) {
		if s.BlockID != "" {
			out[s.BlockID] = true
		}
	}
	forEachBlock(g.Root, func(b *Block) {
		staleArm := phiLessArms[b]
		for _, s := range b.Stmts {
			switch v := s.(type) {
			case *NodeStmt:
				// 语句自身的尾值不算「消费者」：它指向的就是这条语句刚
				// 产出的节点本身（自引用）。别处产出的尾值（inline 体尾、
				// 分支尾）由各自的通道覆盖。
				if v.Value != nil && v.Value.nodeID != v.Node.NodeID() {
					addRef(v.Value)
				}
			case *ReturnStmt:
				// 无宿主的臂值载体 = 陈旧（见上 phiLessArms）。
				if v.End == nil && staleArm {
					continue
				}
				addRef(v.Value)
			case *InlineStmt:
				addRef(v.Value)
			case *LoopStmt:
				addRef(v.Value)
				addRef(v.ReturnValue)
			}
		}
	})
	forEachNode(g.Root, func(n Node) {
		switch v := n.(type) {
		case *AgentCall:
			if v.Query != nil {
				addSource(*v.Query)
			}
		case *LoopReturnNode:
			addSource(v.Value)
		}
	})
	return out
}

// dropNodeStmts 从块里摘掉命中的 NodeStmt（控制流由渲染顺序重连）。
func dropNodeStmts(b *Block, dead map[string]bool) {
	if b == nil {
		return
	}
	out := b.Stmts[:0:0]
	for _, s := range b.Stmts {
		if ns, ok := s.(*NodeStmt); ok && dead[ns.Node.NodeID()] {
			continue
		}
		dropNodeStmtsIn(s, dead)
		out = append(out, s)
	}
	b.Stmts = out
}

func dropNodeStmtsIn(s Stmt, dead map[string]bool) {
	switch v := s.(type) {
	case *IfStmt:
		dropNodeStmts(v.Then, dead)
		dropNodeStmts(v.Else, dead)
	case *AttemptStmt:
		dropNodeStmts(v.Success, dead)
		dropNodeStmts(v.Failure, dead)
	case *InlineStmt:
		dropNodeStmts(v.Body, dead)
	case *LoopStmt:
		dropNodeStmts(v.Body, dead)
	case *ParallelStmt:
		for _, br := range v.Branches {
			dropNodeStmts(br, dead)
		}
	case *QaStmt:
		for i := range v.Branches {
			dropNodeStmts(v.Branches[i].Body, dead)
		}
	case *SwitchStmt:
		for i := range v.Branches {
			dropNodeStmts(v.Branches[i].Body, dead)
		}
	}
}

// forEachBlock 深度优先遍历所有块（含嵌套容器/分支/inline 体）。
func forEachBlock(b *Block, fn func(*Block)) {
	if b == nil {
		return
	}
	fn(b)
	for _, s := range b.Stmts {
		forEachBlockIn(s, fn)
	}
}

func forEachBlockIn(s Stmt, fn func(*Block)) {
	switch v := s.(type) {
	case *IfStmt:
		forEachBlock(v.Then, fn)
		forEachBlock(v.Else, fn)
	case *AttemptStmt:
		forEachBlock(v.Success, fn)
		forEachBlock(v.Failure, fn)
	case *InlineStmt:
		forEachBlock(v.Body, fn)
	case *LoopStmt:
		forEachBlock(v.Body, fn)
	case *ParallelStmt:
		for _, br := range v.Branches {
			forEachBlock(br, fn)
		}
	case *QaStmt:
		for i := range v.Branches {
			forEachBlock(v.Branches[i].Body, fn)
		}
	case *SwitchStmt:
		for i := range v.Branches {
			forEachBlock(v.Branches[i].Body, fn)
		}
	}
}

// forEachStmt 深度优先遍历所有语句。
func forEachStmt(b *Block, fn func(Stmt)) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		fn(s)
		forEachStmtIn(s, fn)
	}
}

func forEachStmtIn(s Stmt, fn func(Stmt)) {
	switch v := s.(type) {
	case *IfStmt:
		forEachStmt(v.Then, fn)
		forEachStmt(v.Else, fn)
	case *AttemptStmt:
		forEachStmt(v.Success, fn)
		forEachStmt(v.Failure, fn)
	case *InlineStmt:
		forEachStmt(v.Body, fn)
	case *LoopStmt:
		forEachStmt(v.Body, fn)
	case *ParallelStmt:
		for _, br := range v.Branches {
			forEachStmt(br, fn)
		}
	case *QaStmt:
		for i := range v.Branches {
			forEachStmt(v.Branches[i].Body, fn)
		}
	case *SwitchStmt:
		for i := range v.Branches {
			forEachStmt(v.Branches[i].Body, fn)
		}
	}
}

// forEachNode 深度优先遍历所有语句承载的节点（含宿主节点）。
func forEachNode(b *Block, fn func(Node)) {
	forEachStmt(b, func(s Stmt) {
		switch v := s.(type) {
		case *NodeStmt:
			if v.Node != nil {
				fn(v.Node)
			}
		case *IfStmt:
			if v.Cond != nil {
				fn(v.Cond)
			}
			if v.Merge != nil {
				fn(v.Merge)
			}
			if v.Relay != nil {
				fn(v.Relay)
			}
		case *SwitchStmt:
			if v.Switch != nil {
				fn(v.Switch)
			}
			if v.Merge != nil {
				fn(v.Merge)
			}
		case *QaStmt:
			if v.QA != nil {
				fn(v.QA)
			}
			if v.Merge != nil {
				fn(v.Merge)
			}
		case *AttemptStmt:
			if v.Op != nil {
				fn(v.Op)
			}
			if v.Exit != nil {
				fn(v.Exit)
			}
			if v.SuccessEnd != nil {
				fn(v.SuccessEnd)
			}
			if v.FailureEnd != nil {
				fn(v.FailureEnd)
			}
		case *LoopStmt:
			if v.Container != nil {
				fn(v.Container)
			}
		case *ReturnStmt:
			if v.End != nil {
				fn(v.End)
			}
		}
	})
}
