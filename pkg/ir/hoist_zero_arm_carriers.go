package ir

import "strings"

// hoist_zero_arm_carriers.go — IR 优化阶段：junction **臂内**「可证恒零载体」的
// 上提（= 「臂写了类型零值」与「臂什么都没写」在 wire 上归一到同一个形状）。
//
// 背景（2026-09-20，生成器形状 `phi_ternary_in_concat` / `phi_switch` s3 触发）：
//
//	`string v; if (c) { v = ""; } else { v = "b"; }` 首编把零值载体物化在**臂内**：
//
//	  start → condition ; true → code("") → merge ; false → text("b") → merge
//
//	而反编译读同一张 wire 时，按 merge 的 first_non_zero 语义把它读成「这条臂
//	没写 + 宿主入口的零值前绑定」（raising 侧唯一可用的信号只有候选位置与产出
//	方落在谁的子树里，J §9.1）；再编译那份文本得到的是**宿主入口**形态：
//
//	  start → code("") → condition ; true → merge ; false → text("b") → merge
//
//	⇒ 首编 ≠ 二编（I1b ARTIFACT_DRIFT/TOPOLOGY@r2），且这个差值**与宿主类型无关**
//	（if 的 then 臂 / else 臂、switch 臂、值位置三元在拼接里的臂，四种最小源同一
//	结论：`/tmp/ps/p_if_else_zero.ncoda`、`s_zero_first`、`v_tern_zero_first`、
//	`t_prebind_and_zero`）。
//
// 规则（一条，无宿主枚举、无形态守卫）：
//
//	汇聚单元 A 的**候选** ref R 若指向节点 X，且
//	  ① X 是可证恒为零值的纯载体（`isZeroCarrierNode`：编译器自建 shim ∧
//	     `ZeroOf ≠ nil` ∧ 无输入 —— 与 P27 / raising 零载体判据逐字同尺），
//	  ② X 落在**宿主语句任一臂块的子树里**（`NodeStmt`；不限一层，见下「子树闭包」），
//	则把 X 上提到宿主语句**之前**（同一 Block，紧邻宿主）；候选 ref **不变**
//	（它现在读的是宿主入口处的零值真相 = 汇聚的缺省位），臂因而不写该槽。
//
// **子树闭包**（2026-09-28，R-124 一手）：同一条规则必须对**递归情形**同解 ——
// 「臂写了零 ≡ 臂没写」在一层臂上成立，在嵌套宿主里就必须一路推到最外宿主的入口：
// 一个仍留在**中间层臂内**的零载体读起来正是「这条臂写了零」，恰是本规则要消灭的
// 形状。只看臂的**直接**语句（一跳）时，深度 ≥ 3 的嵌套会把它停在中间层
// （一手 `if (a) { if (b) { if (c) { v = 0; } } }`：节点数 10→10、12→12，仅落点
// 不同 ⇒ I1b TOPOLOGY；正本 = docs/five-step/rulings/2026-09-28-ruling-sheet-r25.md）。
// 递归**只跨宿主的臂区**（宿主差异只能是数据，J §4）：循环体 / 并行分支 / 内联体
// 的存在性语义不是臂门控（迭代次数 / 并行度）⇒ 一律不跨（fail-safe）。
//
// 为什么这是等价变换（不是近似）：
//
//   - merge = first_non_zero ⇒ **恒零候选永不胜出、也不改结局**：该候选的值与
//     「臂没写 ⇒ 走缺省零」逐字同值（P27 的同一把尺）。
//   - X 无输入、Synthetic、封闭子集 shim ⇒ 纯常量发射器，**把「条件执行」换成
//     「无条件执行」不改变任何值**（没有副作用可重排，也没有臂内数据被它捕获）。
//   - **候选序不变**（R 原位、原序）⇒ first_non_zero 的选择不变。
//   - 臂空化不改控制流：该臂欠的是一条**端口边**，由既有义务表销账（J5）。
//
// 为什么是「上提」而不是 P27 那样的「重定向候选到已存在的零值真相」：宿主入口
// 上**可能根本没有**零载体（`string v;` 只有声明、没有前绑定）——此时零值真相
// 尚未存在，唯一不新增节点、不新增代码文本的物化方式就是把臂内那个载体搬上去。
// 入口**已经有**同值载体时（`v = "";` 前绑定 + 臂内再写零），上提产生的重复载体
// 由既有 P23（`pure ∧ ¬consumed ∧ ¬structuralCarrier`）收尾 —— 不引入第二步规则。
//
// 为什么在 IR 优化阶段而不是 lowering 出生点（与 P27 / P23 同层同尺）：「可证恒
// 零」是**全局 + 传递**判据（常量载体、record 逐字段零、嵌套 dict 全零……），出生
// 点各写一份必然漂移（R-F2 复发机制）；本 pass 在 coze-biz lowering 入口调用，
// 与链式聚合折叠、死纯节点清理同序收尾。
//
// 与 P27 的分工（两条规则各自管一跳，不重叠）：
//
//	· P27 = 候选指向**恒等转发 relay** ⇒ 重定向到链尾零值真相（不改控制位置）。
//	· 本条 = 候选**直接**指向臂内的零值载体 ⇒ 载体上提到宿主入口。
func HoistZeroArmCarriers(g *Graph) bool {
	if g == nil || g.Root == nil {
		return false
	}
	return hoistZeroArmCarriers(g)
}

func hoistZeroArmCarriers(g *Graph) bool {
	nodes := collectIRNodes(g)
	changed := false
	var walk func(b *Block)
	walk = func(b *Block) {
		if b == nil {
			return
		}
		// 迭代的是 b.Stmts 的**旧切片头**：上提会替换 b.Stmts（新底层数组），
		// 因此本轮的宿主指针与后续元素身份都不受影响。
		for _, s := range b.Stmts {
			if host, ok := s.(junctionHostStmt); ok {
				if agg := host.junctionMerge(); agg != nil {
					if hoistArmZeroCarriers(g, b, s, host, agg, nodes) {
						changed = true
					}
				}
			}
			walkStmtBlocks(s, walk)
		}
	}
	walk(g.Root)
	return changed
}

// hoistArmZeroCarriers 把宿主 s 的臂内「本汇聚候选所指向的可证恒零载体」上提到
// s 之前。返回是否发生改动。
func hoistArmZeroCarriers(g *Graph, b *Block, host Stmt, h junctionHostStmt, agg *Aggregate, nodes map[string]Node) bool {
	cands := map[string]bool{}
	addRefs := func(refs []Ref) {
		for _, r := range refs {
			cands[r.Producer] = true
		}
	}
	addRefs(agg.Refs)
	for _, grp := range agg.Groups {
		addRefs(grp.Refs)
	}
	// ValueSources = 该单元吸收的**整值**候选（值位置三元 / 无 else 尾值 /
	// switch·QA·attempt 臂值）；它们同样是候选，同样受本规则约束。
	addRefs(agg.ValueSources)
	if len(cands) == 0 {
		return false
	}
	// 收集 = 臂区**子树闭包**，不限一层（口径与依据见文件头「子树闭包」）。
	var ins []Stmt
	var harvest func(b *Block)
	harvest = func(b *Block) {
		if b == nil {
			return
		}
		var kept []Stmt
		for _, s := range b.Stmts {
			if ns, ok := s.(*NodeStmt); ok && ns.Node != nil &&
				cands[ns.Node.NodeID()] && isZeroCarrierNode(ns.Node) {
				ins = append(ins, ns)
				continue
			}
			if hh, ok := s.(junctionHostStmt); ok {
				for _, arm := range hh.junctionArmBlocks() {
					harvest(arm)
				}
			}
			kept = append(kept, s)
		}
		if len(kept) != len(b.Stmts) {
			b.Stmts = kept
		}
	}
	for _, arm := range h.junctionArmBlocks() {
		harvest(arm)
	}
	if len(ins) == 0 {
		return false
	}
	// 同值折一（R-14 收口，2026-09-23）：上提把**每条臂各自的**零载体搬到同一个
	// Block（宿主之前）⇒ 目的地 Block 里出现两个逐字同值的零值 shim。而 builder 的
	// `zeroShims` 池**已经**把「同一个 Block 内同码零值 shim 只物化一个节点」定成
	// 不变量（`zeroShimKey{blk, code}`，build.go::zeroStringShim）—— 跨块搬迁必须
	// 把这条不变量带到目的地：否则同一份值在同一位置被物化两次，读回渲染成两条
	// `let v = "";`、再编译被池折成一个 ⇒ 文本**一轮不落定点**
	// （一手：/tmp/z2.ncoda，c0 6 节点 → c1 5 节点，I1a+I1b 双红 = R-14）。
	//
	// 等价性（不是近似）：① 候选仍指向**恒零**载体 ⇒ merge = first_non_zero 下恒零
	// 永远不胜出（与 J §16.2 判据 4「候选序 ↔ 臂序」不冲突：槽数与槽位都不变，只换
	// 生产者 id）；② 两条臂互斥 ⇒ 该槽存在 ⟺ 该臂执行，臂门控语义由**槽**承载，
	// 不由生产者 id 承载（槽没动）；③ 被折掉的节点必须**除这些候选槽外无消费者**
	// （消费普查自证，否则不折 —— 宁可留重复，不许悬空引用）。
	ins = foldSameValueHoistedCarriers(g, nodes, agg, ins,
		collectIRConsumers(g, nodes), armCarrierSites(g))
	idx := indexOfStmt(b.Stmts, host)
	if idx < 0 {
		return false
	}
	out := make([]Stmt, 0, len(b.Stmts)+len(ins))
	out = append(out, b.Stmts[:idx]...)
	out = append(out, ins...)
	out = append(out, b.Stmts[idx:]...)
	b.Stmts = out
	return true
}

// indexOfStmt 按**语句指针**定位（不是按类型/序号 —— 上提会移位，序号会腐烂）。
func indexOfStmt(stmts []Stmt, target Stmt) int {
	for i, s := range stmts {
		if s == target {
			return i
		}
	}
	return -1
}

// walkStmtBlocks 递归进入语句持有的嵌套块（臂区 / 循环体 / 内联体）。
// 与折叠 pass 的走法同构：宿主**类型**只决定「块在哪」，不决定规则。
func walkStmtBlocks(s Stmt, walk func(*Block)) {
	switch v := s.(type) {
	case *IfStmt:
		walk(v.Then)
		// ChainArms = `else if` 链融成单节点后的中臂（FuseBranchChains 生产）。
		// 既有 pass 见到的 ChainArms 恒为 nil，并入只是让这条 walker 对
		// 「语句树的完整形状」保持**单一实现**（漏走 = 静默丢节点）。
		for _, a := range v.ChainArms {
			walk(a.Block)
		}
		walk(v.Else)
	case *AttemptStmt:
		walk(v.Success)
		walk(v.Failure)
	case *LoopStmt:
		walk(v.Body)
	case *ParallelStmt:
		for _, br := range v.Branches {
			walk(br)
		}
	case *QaStmt:
		for _, br := range v.Branches {
			walk(br.Body)
		}
	case *SwitchStmt:
		for _, br := range v.Branches {
			walk(br.Body)
		}
	case *InlineStmt:
		walk(v.Body)
	}
}

// foldSameValueHoistedCarriers 把同一次上提里**同码**的零值载体折成一个：保留
// 首个，其余把候选槽的 Producer 重定向到保留者、并不再上提。返回折叠后的列表。
//
// 折叠条件（两个都满足才折，否则原样返回 = 不改变行为）：
//
//	· 与保留者同码（`zeroShimKey` 的同一个键口径：Code + Language + 端口模型）；
//	· 该节点**除这些候选槽外没有别的消费者**（`consumerSiteCount` == 重定向到的
//	  槽数）。有别的消费者 ⇒ 折掉会让引用悬空 ⇒ 保留（重复仍在，退化成今天的行为）。
func foldSameValueHoistedCarriers(g *Graph, nodes map[string]Node, agg *Aggregate, ins []Stmt,
	consumers, carriers map[string]map[string]int) []Stmt {
	if agg == nil || len(ins) < 2 {
		return ins
	}
	kept := make([]Stmt, 0, len(ins))
	keeper := map[string]string{} // 同码键 ⇒ 保留者的节点 id
	for _, s := range ins {
		ns, ok := s.(*NodeStmt)
		if !ok {
			kept = append(kept, s)
			continue
		}
		cr, ok := ns.Node.(*CodeRun)
		if !ok {
			kept = append(kept, s)
			continue
		}
		key := zeroCarrierPoolKey(cr)
		keepID, seen := keeper[key]
		if !seen {
			keeper[key] = ns.Node.NodeID()
			kept = append(kept, s)
			continue
		}
		redirected := redirectJunctionRefs(g, nodes, ns.Node.NodeID(), keepID)
		if redirected == 0 || nonCarrierSites(consumers, carriers, ns.Node.NodeID()) != redirected {
			// 不该折（重定向覆盖不到它的全部消费者）⇒ 保留，行为同今天。
			kept = append(kept, s)
		}
	}
	return kept
}

// zeroCarrierPoolKey 与 builder 的 `zeroShimKey{blk, code}` **同键口径**（Block 由
// 调用点保证是同一个目的地 Block）：Code 逐字 + 语言 + 输出端口声明序（端口模型）。
func zeroCarrierPoolKey(cr *CodeRun) string {
	return cr.Code + "\x00" + cr.Language + "\x00" + strings.Join(cr.OutOrder, "\x1f")
}

// redirectJunctionRefs 把**图上每个汇聚单元**里指向 from 的候选槽改指 to，返回改了几槽。
//
// 为什么不是「只改宿主的主汇聚」：一手实测（/tmp/z2.ncoda）—— 出口承载单元里同一个
// 载体同时是**主汇聚**（merge 组槽）与 **exit junction** 的候选（attempt 是流程尾时
// 两条通道都指向臂尾值）⇒ 只改主汇聚会留下第二个站点 ⇒ 消费普查判「还有别的消费者」
// ⇒ 折不掉（实测 redirected=1 / sites=2）。两条通道都改，普查才证得成「它的全部
// 消费者就是这些候选槽」。
func redirectJunctionRefs(g *Graph, nodes map[string]Node, from, to string) int {
	n := 0
	for _, node := range nodes {
		agg, ok := node.(*Aggregate)
		if !ok || agg == nil {
			continue
		}
		n += redirectAggregateRefs(agg, from, to)
	}
	return n
}

// redirectAggregateRefs 把该汇聚单元里所有指向 from 的候选槽改指 to，返回改了几槽。
func redirectAggregateRefs(agg *Aggregate, from, to string) int {
	n := 0
	fix := func(refs []Ref) []Ref {
		for i := range refs {
			if refs[i].Producer == from {
				refs[i].Producer = to
				n++
			}
		}
		return refs
	}
	agg.Refs = fix(agg.Refs)
	for i := range agg.Groups {
		agg.Groups[i].Refs = fix(agg.Groups[i].Refs)
	}
	agg.ValueSources = fix(agg.ValueSources)
	return n
}

// nonCarrierSites 该节点的「**臂终端载体之外**的引用站点」总数。
//
// 为什么扣掉臂终端：臂终端 End（`AttemptStmt.SuccessEnd` / `FailureEnd`）是**控制流
// 载体**，渲染器把它投影成端口边、不物化成数据绑定 ⇒ 它的 `Inputs` **不是读者**
// （J §15 / `prune_dead_pure_nodes.go::armCarrierSites` 头注，同一把尺、同一个函数，
// 不另写一份）。不扣它就永远证不成「它的消费者只有那些候选槽」——一手实测
// /tmp/z2.ncoda：`redirected=1` 而 `sites=2`，差的那 1 个正是臂终端。
func nonCarrierSites(consumers, carriers map[string]map[string]int, id string) int {
	n := 0
	for port, c := range consumers[id] {
		if d := c - carriers[id][port]; d > 0 {
			n += d
		}
	}
	return n
}
