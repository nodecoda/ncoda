package ir

import (
	"strings"

	"github.com/nodecoda/ncoda/pkg/pyast"
)

// sink_base_carriers.go — IR 优化阶段：汇聚单元的**臂外常量前绑定载体**下沉到
// 「未写臂」内（= 「前绑定常量」与「臂内写值」在 wire 上归一到同一个形状）。
//
// 背景（2026-09-21，J §8 U8 收口；最小形一手实测）：
//
//	`int v = 5; if (u == "a") { v = 7; } return v;` 首编把常量 5 物化在**条件之前**
//	（前绑定位，臂外候选）：
//
//	  start → code(5) → condition ; true → code(7) → merge ; false → merge
//
//	而 raising 读同一张 wire 时，臂外候选只能渲染成「条件前 `let v1 = 5;` +
//	兜底臂内中继 `v = v1;`」，其中 `let` 字面量在**再编译**时被常量折叠、在臂内
//	重建：
//
//	  start → condition ; true → code(7) → merge ; false → code(5) → merge
//
//	⇒ 首编 ≠ 二编（I1b ARTIFACT_DRIFT/TOPOLOGY@r2），且 r1 ≠ r2（I1a
//	TEXT_DRIFT/STRUCTURAL@r2）。实测（本仓最小形）：r1 207B / r2 211B，
//	c1 3780B / c2 3780B（节点数 6→6，拓扑不同）。
//
// 规则（一条，无宿主枚举）：**臂外**（不落在任何臂的子树里）的**常量基座候选**
// 必须被门控进那条未写臂。两条形态按「能不能平移」分区，语义同一个：
//
//	④ A 中**恰好一条**臂的块内不含该组的任何候选生产者（未写臂）；
//	然后看该组的臂外候选 X（落在两条臂之外的那个候选，= 分支前绑定常量）：
//	  ① X 是可证常量发射器（编译器自建 shim ∧ `# ncoda:shim` marker ∧ **无输入**
//	     ∧ ZeroOf == nil —— 有输入 = 携载真实臂值；ZeroOf ≠ nil = 零载体，归 P28），
//	  ② X 作为 H 所在块的直接语句出现在 H **之前**（前绑定位）∧
//	  ③ X 的全部消费都是 A 的候选 ref（别无他人读它 —— 平移不让任何引用落空）
//	     ⇒ **平移**原件（id 不变；2026-09-21 起的既有形状，逐字节不变）；
//	  ②′ 或 ③′ 不成立（**共享**：同一个基座常量被别的 φ 也引用；或它住在**外层块**）
//	     ⇒ 在未写臂首**铸造一份等价副本**并让该候选直引副本（原件若因此失去全部
//	     消费者，由既有 ③ `PruneDeadPureNodes` 收尾）。
//
//	**②′/③′ 的适用条件**（2026-09-28 收口）：只在「本组结局里**可能出现作者写的
//	类型零值**」时才铸副本 —— 判据 = `groupHasAuthoredZeroDeep`（本文件下方；候选链
//	里有可证恒零载体，含臂内**嵌套汇聚**的同类组）。没有零写时，臂外基座停在
//	**无条件位正是规范位**（J §8 U12：上游基座在每条执行路径上都有值 ⇒ 只能在提升
//	变量的初值位），门控它只会把形状改坏，并破共享纯常量载体的「带类型存储」形态
//	（J §35.6 / platforms/cozebiz/decompile/shared_carrier_decl_contract_test.go）。
//
// **为什么必须有 ②′/③′ 这一支**（一手，2026-09-28，残余 R-124 ①；裁决单
// rulings/2026-09-28-ruling-sheet-r25.md）：嵌套 junction 里**同一个**「分支前绑定
// 常量」同时是内层 φ 与外层 φ 的兜底候选（它在 A 的候选表里出现 1 次，而全图消费
// 者 2 个）⇒ 规则③ 把它判成「平移会让别的引用落空」而整支放弃 ⇒ 基座停在
// 「无条件执行」位 ⇒ 平台 first_non_zero 让非零基座吞掉作者在臂里写的**零值**
// （`int v = 1; if (q=="a") { if (q=="b") { v = 0; } else { } } else { } return v;`
// 三条路径都返回 1 = 作者要 0 的那条丢值），且读回不收敛（7 → 5 节点）。
// （注意这一支的价值只在「作者写了零」时成立 —— 这正是下面那条适用条件的来处：
// 没有零写时基座停在无条件位与语言语义逐路径同值。）
// 副本与原件**同一个值**（纯常量发射器：无输入 ∧ 无副作用 ⇒「无条件执行」换成
// 「该臂执行时执行」不改值）——所以复制是**等价变换**，不是近似：
// 「每一条未写路径各自承载一份基座」正是本规则在两臂形状上早已成立的那件事。
//
// 为什么这是等价变换（不是近似）：
//
//   - X 无输入、Synthetic、封闭子集 shim ⇒ 纯常量发射器：**把「无条件执行」换成
//     「该臂执行时执行」不改变任何值**（没有副作用可重排，也没有臂内数据被它捕获）。
//   - 候选的位置语义 = 该臂是否执行（first_non_zero：臂没执行 ⇒ 候选为零被跳过）。
//     前绑定常量在本臂不执行时**照样有值**（无条件执行的节点）——这与「该臂没写」
//     在值上不同（前者让兜底常量在臂产出零值时胜出，后者不会）。作者写的
//     `if (c) { v = 7; }` 里基座 5 是**臂内写值**语义（J §8 U8：不动点那一支），
//     故下沉方向与语言语义一致，不是形状妥协。
//   - 候选序不变（R 原位、原序）⇒ first_non_zero 的选择不变。
//
// 为什么在 IR 优化阶段而不是 lowering 出生点：同一规则已在两个兄弟 pass 里按
// 「值可证为零 / 其余常量」分区收口（P27 恒等转发重定向、P28 臂内零载体上提），
// 前绑定常量是同一把尺的第三个位点；出生点各写一份必然漂移。
//
// 与 P28（HoistZeroArmCarriers）的分工（两条规则各自管一跳，按值分区不重叠）：
//
//	· P28 = 候选指向**臂内**的可证恒零载体 ⇒ 载体上提到宿主之前（零候选永不胜出
//	  ⇒「臂写零」≡「臂没写」，wire 上只留一种形状）。
//	· 本条 = 候选指向**宿主之前**的可证**非零**常量载体 ⇒ 载体下沉到未写臂
//	  （它是该臂的真实值）。
func SinkBaseCarriers(g *Graph) bool {
	if g == nil || g.Root == nil {
		return false
	}
	nodes := collectIRNodes(g)
	consumers := collectIRConsumers(g, nodes)
	changed := false
	var walk func(b *Block)
	walk = func(b *Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			if host, ok := s.(junctionHostStmt); ok {
				if agg := host.junctionMerge(); agg != nil {
					if sinkBaseCarriersOne(g, b, s, host, agg, nodes, consumers) {
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

// sinkBaseCarriersOne 对宿主 s 执行一次「臂外常量基座 ⇒ 未写臂」门控（平移或铸副本）。
func sinkBaseCarriersOne(g *Graph, b *Block, host Stmt, h junctionHostStmt, agg *Aggregate, nodes map[string]Node, consumers map[string]map[string]int) bool {
	arms := h.junctionArmBlocks()
	if len(arms) != 2 {
		// 两臂以上的宿主（switch / QA / attempt）没有「另一臂」的对偶形状：
		// 未写臂的归属不唯一 ⇒ 不动（fail-safe，登记为 J §8 未决项而非猜）。
		return false
	}
	armIDs := make([]map[string]bool, len(arms))
	for i, arm := range arms {
		armIDs[i] = blockContainedNodeIDs(arm)
	}
	hostIdx := indexOfStmt(b.Stmts, host)
	if hostIdx < 0 {
		return false
	}
	changed := false
	// perArm 收集「未写臂 ⇒ 该臂要承接的载体」的归属（同一臂两个不同载体 ⇒ 放弃本宿主）。
	type target struct {
		stmt  *NodeStmt
		nodes []string
	}
	perArm := make([]*target, len(arms))
	type clonePlan struct {
		arm  int
		node Node
	}
	type repoint struct {
		gi, ri int
		id     string
	}
	// ②′ 记录字段组的基座候选归一（**先于**逐组处置）：候选由**臂内恒等 relay** 承载时
	//    看穿到基座本身。这一步把「记录基座」与「标量基座」归到**同一条**规则 ——
	//    「未写臂的候选 = 分支前基座」在两种值类型上必须是同一形状（否则一个意思两种
	//    产物，R-124 记录半一手：记录基座停在入口 + 逐字段双跳 relay ⇒ 反编译读成
	//    「入口前绑定 + 臂内赋值」⇒ 二编换成「基座直落未写臂」= 12 ≠ 10 节点）。
	if sinkRecordBaseCarriersOnce(g, b, hostIdx, arms, armIDs, agg, nodes, consumers) {
		changed = true
	}
	var clones []clonePlan
	var repoints []repoint
	// 副本按 **(基座, 臂)** 去重：同一基座的**全部字段组候选**必须改指**同一份**
	// 副本 —— 各组同位候选同生产者是**记录单元**成立的前提，逐字段各铸一份会把
	// 记录拆成互不相关的标量组（一手 p30/r3：逐字段各铸一份 ⇒ 外层 merge 的 a 组
	// 有副本而 b 组仍引臂外基座 ⇒ 组间候选生产者序列不一致 ⇒ 反编译读成标量
	// `agg = v1.a` ⇒ 二编 7 节点 ≠ 首编 9 节点）。
	type cloneSlot struct {
		producer string
		arm      *Block
	}
	cloneByKey := map[cloneSlot]string{}
	for gi := range agg.Groups {
		grp := &agg.Groups[gi]
		// 未写臂 = 本组内没有任何候选生产者的臂。
		unwritten := []int{}
		for ai := range arms {
			if armHasGroupCandidate(grp, armIDs[ai]) {
				continue
			}
			unwritten = append(unwritten, ai)
		}
		if len(unwritten) != 1 {
			continue
		}
		ai := unwritten[0]
		for ri := range grp.Refs {
			r := grp.Refs[ri]
			// 落在**某个**臂的子树里的候选 = 已门控（该臂执行它才存在）。
			inArm := false
			for aj := range arms {
				if armIDs[aj][r.Producer] {
					inArm = true
					break
				}
			}
			if inArm {
				continue
			}
			n := nodes[r.Producer]
			if !isSinkableDirectEmitterNode(n, grp, lookupNode(nodes)) {
				continue
			}
			idx := nodeStmtIndexBefore(b, hostIdx, r.Producer)
			if idx >= 0 && len(candidateRefsTo(agg, r.Producer)) == totalConsumers(consumers, r.Producer) {
				ns := b.Stmts[idx].(*NodeStmt)
				if perArm[ai] == nil {
					perArm[ai] = &target{}
				} else if perArm[ai].stmt != ns {
					// 同一臂要承接两个不同载体 ⇒ 归属不唯一，放弃本宿主。
					return false
				}
				perArm[ai].stmt = ns
				perArm[ai].nodes = append(perArm[ai].nodes, r.Producer)
				continue
			}
			// 共享 / 臂外的常量基座 ⇒ 铸副本门控 —— **但仅在「本组结局里可能出现作者写的
			// 类型零值」时**（判据 = `groupHasAuthoredZeroDeep`，理由见其头注）。没有零写
			// 时臂外基座停在入口位是规范位（J §8 U12），门控它只会改形状。
			if !groupHasAuthoredZeroDeep(grp, lookupNode(nodes), map[string]bool{}) {
				continue
			}
			key := cloneSlot{producer: r.Producer, arm: arms[ai]}
			if id, ok := cloneByKey[key]; ok {
				repoints = append(repoints, repoint{gi: gi, ri: ri, id: id})
				continue
			}
			// 副本 id 与构造期同段并登记进分配集合，故产物出口的 id 空间压缩自动覆盖它。
			cp := cloneConstantEmitter(n, g.AllocNodeID())
			if cp == nil {
				continue
			}
			clones = append(clones, clonePlan{arm: ai, node: cp})
			cloneByKey[key] = cp.NodeID()
			repoints = append(repoints, repoint{gi: gi, ri: ri, id: cp.NodeID()})
		}
	}
	moved := false
	for ai, tgt := range perArm {
		if tgt == nil || tgt.stmt == nil {
			continue
		}
		idx := indexOfStmt(b.Stmts, tgt.stmt)
		if idx < 0 {
			continue
		}
		out := make([]Stmt, 0, len(b.Stmts)-1)
		out = append(out, b.Stmts[:idx]...)
		out = append(out, b.Stmts[idx+1:]...)
		b.Stmts = out
		arms[ai].Stmts = append([]Stmt{tgt.stmt}, arms[ai].Stmts...)
		moved = true
	}
	for _, c := range clones {
		arms[c.arm].Stmts = append([]Stmt{&NodeStmt{Node: c.node}}, arms[c.arm].Stmts...)
		moved = true
	}
	// 候选改指副本：只换生产者的**身份**，端口名（`Output`）与候选序都不动 ⇒
	// first_non_zero 的选择序不变。
	for _, rp := range repoints {
		agg.Groups[rp.gi].Refs[rp.ri].Producer = rp.id
	}
	return moved || changed
}

// sinkRecordBaseCarriersOnce —— 记录字段组的基座候选归一（**同一把尺**的第四个位点，
// 2026-09-28，R-124 记录半一手）。
//
// ── 一手（a3，12 → 10 节点、I1a/I1b@2）──────────────────────────────────
//
//	R r = {"a":1,"b":"x"};
//	if (q == "a") { r = {"a":5,"b":"x"}; if (q == "b") { r = {"a":2,"b":"y"}; } else { } } else { }
//	return r.a;
//
// 首编：基座记录（`{"a":1,"b":"x"}` 的 shim）停在**入口**，两条未写臂各自挂**逐字段
// 恒等 relay**（`relay(基座.a)` / `relay(基座.b)`）；反编译把「入口前绑定 + 臂内 relay」
// 读成「臂内赋值 `agg = v1;`」⇒ 二编把基座**直落未写臂**（relay 全消）⇒ 12 ≠ 10 节点、
// r1 ≠ r2 ⇒ I1 一轮不动点破裂。
//
// ── 规则（与标量支**逐字同一条**）────────────────────────────────────────
//
// 「未写臂的候选 = 分支前基座常量」的规范形状 = **基座落在那条未写臂里**（标量支
// 2026-09-21 起就是这个形状：`int v = 5; if (u=="a") { v = 7; } return v;` 的 5 要搬进
// 未写臂）。记录与标量的差别只是**候选的拼写**：标量的候选直引基座口，记录的候选由
// 臂内逐字段 relay 承载（臂门控只能逐字段表达）⇒ 把「记录候选的 relay 跳」看穿到基座，
// 两种值类型就落回同一条规则、同一个形状。
//
// 平移（不铸副本）的前提与标量支相同：基座是宿主**之前的直接语句**（前绑定位）∧ 它的
// 全部消费**正是**这些 relay 的输入（口名多重集相等 —— 引用身份在 census 里只到
// 「口名计数」这一粒度）。否则铸一份等价副本进未写臂（基座被别的 φ 读走 ⇒ 不能平移）。
// 两种处置都是**等价变换**：基座是纯常量发射器（无输入 ⇒ 把「无条件执行」换成「该臂
// 执行时执行」不改值），候选口名与候选序一字不动 ⇒ first_non_zero 的选择序不变。
//
// 为什么只认**记录成员口**（`recordMemberRelayBase`）：臂内恒等 relay 还有一个合法
// 来源 —— **臂内真写**（`if (c) { v = <臂外值>; }`，`gateArmCandidate` 物化的恒等载体）。
// 那种 relay 承载的是**整值口**、不是记录成员口；把它也看穿会把「写了」误判成「没写」
// （两条臂都判未写 ⇒ 归属不唯一 ⇒ 本函数 return false 兜底，但语义上本就该是「写了」）。
// 判据 = 基座声明的口里**有一个容器口**（记录发射器）∧ 被承载的口是它的**非容器**口。
//
// relay 收尾：候选改指基座口之后，这些臂内 relay 不再有消费者 ⇒ 由既有
// `PruneDeadPureNodes` 删除（与标量支同一条收尾，不新增清理器）。
func sinkRecordBaseCarriersOnce(g *Graph, b *Block, hostIdx int, arms []*Block, armIDs []map[string]bool, agg *Aggregate, nodes map[string]Node, consumers map[string]map[string]int) bool {
	// 逐字段组才走这里：单组 / legacy 通道是标量族（基座直引候选口），由本函数之后
	// 的既有两支覆盖。判据 = 单元形状（一个 junction 一个变量的 φ 才有逐字段组）。
	if agg == nil || len(agg.Groups) < 2 {
		return false
	}
	type viaRef struct {
		gi, ri int
		info   relayCarriedBase
	}
	var vias []viaRef
	genuine := make([]bool, len(arms))
	for gi := range agg.Groups {
		for ri, r := range agg.Groups[gi].Refs {
			if info, ok := recordMemberRelayBase(nodes, armIDs, r); ok {
				vias = append(vias, viaRef{gi: gi, ri: ri, info: info})
				continue
			}
			for ai := range arms {
				if armIDs[ai][r.Producer] {
					genuine[ai] = true
				}
			}
		}
	}
	if len(vias) == 0 {
		return false
	}
	// 未写臂 = 既无**真写**候选、又承载全部 relay 的那条臂；归属不唯一 ⇒ 放弃本宿主
	// （fail-safe：不动任何东西，形状保持原样）。
	target := -1
	for ai := range arms {
		if genuine[ai] {
			continue
		}
		if target >= 0 {
			return false
		}
		target = ai
	}
	if target < 0 {
		return false
	}
	for _, v := range vias {
		if v.info.armIdx != target {
			return false
		}
	}
	// 按基座聚合：同一基座的全部字段候选一起改指（各组同位候选同生产者 = 记录单元前提，
	// 逐字段各铸一份会拆掉记录单元 ⇒ R-13 的老毛病）。
	byBase := map[string][]viaRef{}
	order := []string{}
	for _, v := range vias {
		if _, seen := byBase[v.info.base]; !seen {
			order = append(order, v.info.base)
		}
		byBase[v.info.base] = append(byBase[v.info.base], v)
	}
	type plan struct {
		stmt  *NodeStmt // 非 nil ⇒ 平移这只语句
		node  Node      // 非 nil ⇒ 铸副本
		newID string
		refs  []viaRef
	}
	var plans []plan
	for _, x := range order {
		vs := byBase[x]
		ports := map[string]int{}
		for _, v := range vs {
			ports[v.info.field]++
		}
		newID := x
		if idx := nodeStmtIndexBefore(b, hostIdx, x); idx >= 0 && portCountsEqual(consumers[x], ports) {
			ns, _ := b.Stmts[idx].(*NodeStmt)
			if ns == nil {
				continue
			}
			plans = append(plans, plan{stmt: ns, newID: newID, refs: vs})
			continue
		}
		cp := cloneConstantEmitter(nodes[x], g.AllocNodeID())
		if cp == nil {
			continue
		}
		plans = append(plans, plan{node: cp, newID: cp.NodeID(), refs: vs})
	}
	if len(plans) == 0 {
		return false
	}
	for _, pl := range plans {
		if pl.stmt != nil {
			idx := indexOfStmt(b.Stmts, pl.stmt)
			if idx < 0 {
				continue
			}
			out := make([]Stmt, 0, len(b.Stmts)-1)
			out = append(out, b.Stmts[:idx]...)
			out = append(out, b.Stmts[idx+1:]...)
			b.Stmts = out
			arms[target].Stmts = append([]Stmt{pl.stmt}, arms[target].Stmts...)
		} else {
			arms[target].Stmts = append([]Stmt{&NodeStmt{Node: pl.node}}, arms[target].Stmts...)
		}
		for _, v := range pl.refs {
			agg.Groups[v.gi].Refs[v.ri] = Ref{Producer: pl.newID, Output: v.info.field}
		}
	}
	return true
}

// relayCarriedBase = 「臂内恒等 relay 承载的记录成员口」的看穿结果。
type relayCarriedBase struct {
	base   string // 基座常量发射器 id（落在**全部臂之外**）
	field  string // 基座上的**成员**口名
	relay  string // 承载该候选的臂内恒等 relay id
	armIdx int    // 该 relay 所在的臂下标
}

// recordMemberRelayBase 判定候选 r 是否「由臂内恒等 relay 承载的记录成员口」：是 ⇒ 返回
// 看穿结果（基座 + 成员口 + 承载 relay + 所在臂）。任一条不成立 ⇒ ok=false（不看穿，
// 形状保持原样）。
//
// 判据逐条（每条都必要）：
//
//	· 候选生产者是**恒等 relay**（`identityRelaySource` 同尺：Identity ∧ 恰好一个输入
//	  ∧ 输入是 block-output 引用）—— 非 relay ⇒ 是臂内真写；
//	· relay 落在**某一条臂**内（臂外 relay 不存在于本 pass 的域）；
//	· 基座 = relay 的唯一输入所指节点，且**落在全部臂之外**（臂内的基座已由臂门控）；
//	· 基座是**记录发射器**：声明的口里有一个容器口，且被承载的口是它的**非容器**口
//	  —— 这一条把「臂内真写的整值 relay」排除在外（那些 relay 承载 `result` 这样的
//	  整值口，不是成员口）。
func recordMemberRelayBase(nodes map[string]Node, armIDs []map[string]bool, r Ref) (relayCarriedBase, bool) {
	n := nodes[r.Producer]
	in, ok := identityRelaySource(n)
	if !ok {
		return relayCarriedBase{}, false
	}
	baseN := nodes[in.BlockID]
	cr, ok := baseN.(*CodeRun)
	if !ok || !isSinkableRelayEmitterNode(baseN) {
		return relayCarriedBase{}, false
	}
	if !recordEmitterMemberPort(cr, in.Name) {
		return relayCarriedBase{}, false
	}
	for _, ids := range armIDs {
		if ids[in.BlockID] {
			return relayCarriedBase{}, false
		}
	}
	armIdx := -1
	for ai, ids := range armIDs {
		if ids[r.Producer] {
			armIdx = ai
			break
		}
	}
	if armIdx < 0 {
		return relayCarriedBase{}, false
	}
	return relayCarriedBase{base: in.BlockID, field: in.Name, relay: r.Producer, armIdx: armIdx}, true
}

// recordEmitterMemberPort 报告「cr 是记录发射器 ∧ field 是它的一个**成员**口」：
// 判据 = cr 声明的某个口是**容器**（带字段描述 —— 记录字面量 shim 的整值口形状）且
// field 是它的一个字段。**只用端口类型形状判定，不用任何口名拼写**（成员口在 IR 里
// 不单独声明：渲染层才把容器口的字段展开成扁平口）。
func recordEmitterMemberPort(cr *CodeRun, field string) bool {
	if cr == nil || field == "" {
		return false
	}
	for _, p := range cr.OutOrder {
		t := cr.OutTypes[p]
		if t == nil || len(t.Props) == 0 {
			continue
		}
		if t.Props[field] != nil {
			return true
		}
	}
	return false
}

// portCountsEqual 比较「口名 → 引用站点数」两张计数表（口名多重集相等）。
func portCountsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// cloneConstantEmitter 复制一只**纯常量发射器**并给它一个新的 node id：值、端口、
// 类型一字不改（只有身份变）。用于「共享 / 臂外的基座常量」的臂门控 —— 原件可能被
// 别的 φ 引用（或住在别的块），故不能平移，只能在未写臂内铸一份等价副本。
//
// 闭集与 `isSinkableRelayEmitterNode` **同一把尺**（这里只复制，不判断）：两支一一对应；
// 复制**连输入一起**（`c.Inputs = v.Inputs`）⇒ 有输入的纯 shim 也能铸等价副本。
// 加支必须同时改那一边，否则出现「判得出、复制不出」的半接线。
func cloneConstantEmitter(n Node, id string) Node {
	switch v := n.(type) {
	case *CodeRun:
		if v == nil {
			return nil
		}
		c := *v
		c.ID = id
		c.Inputs = append([]Source(nil), v.Inputs...)
		c.OutOrder = append([]string(nil), v.OutOrder...)
		c.Types = cloneEmitTypeMap(v.Types)
		c.OutTypes = cloneEmitTypeMap(v.OutTypes)
		return &c
	case *TextProcess:
		if v == nil {
			return nil
		}
		c := *v
		c.ID = id
		c.Inputs = append([]Source(nil), v.Inputs...)
		c.Separators = append([]string(nil), v.Separators...)
		return &c
	}
	return nil
}

// cloneEmitTypeMap 复制端口类型表（表本身独立；*Type 描述符按值语义只读共享 ——
// 与本仓其它克隆点同一个口径）。nil ⇒ nil（保持「无声明」这一事实，不造空表）。
func cloneEmitTypeMap(m map[string]*Type) map[string]*Type {
	if m == nil {
		return nil
	}
	out := make(map[string]*Type, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// groupHasAuthoredZeroDeep 报告本汇聚组的候选里是否存在**作者写的类型零值**
// （= 一只可证恒零载体，`isZeroCarrierNode`）——**含臂内嵌套汇聚的同类组**（递归位）。
//
// 为什么基座门控以它为条件（2026-09-28，R-124 一手；判据 = 既有 4 条契约）：
//
//	平台 merge = first_non_zero ⇒ 「候选取值可证为零」在 wire 上是**唯一**能把「作者写的
//	零值」承载到结局的形状（全候选零/未执行 ⇒ 结局 = 零）。而**无条件**的臂外基座（非零
//	常量）在**每一条**路径上都有值 ⇒ 它在「作者写了零」的那条路径上胜出 = 把作者的写静默
//	丢掉（「失配 ≠ 静默丢弃」要报的账）。基座改落在**每一条未写路径**里时，零写路径上
//	所有候选都不可用（未写臂 = 零）⇒ 结局 = 零 = 作者写的值。这就是本规则的**值语义**。
//
//	没有零写时，臂外基座停在宿主之前的无条件位**正是规范位**（J §8 U12：上游基座在每条
//	执行路径上都有值 ⇒ 只能在「提升变量的初值位」；见
//	internal/compiler/cozebiz_upstream_base_placement_contract_test.go ①）。门控它反而会
//	把上游基座塞进臂内 ⇒ 破 U12 的形态，也破
//	platforms/cozebiz/decompile/shared_carrier_decl_contract_test.go 的「带类型存储」形态
//	（共享纯常量载体在单汇聚单元里是存储载体，不是候选常量）。
//
// 递归只跨**候选生产者本身是嵌套宿主的汇聚单元**（= 内层臂的结局）；组名按候选口名
// 匹配（嵌套宿主内外同名：一手 `out="v"` ⇒ 内层 `Groups[0].Name == "v"`）。口名对不上
// 任何组名（形态不确定）⇒ 全组求值（宁可门控，不静默丢值）。
func groupHasAuthoredZeroDeep(grp *AggregateGroup, lookup func(string) Node, seen map[string]bool) bool {
	if grp == nil {
		return false
	}
	for _, r := range grp.Refs {
		n := lookup(r.Producer)
		// 取值可证为零：整节点零载体 **或** 字面量成员口的零（`CodeRun.ZeroFields`）。
		// 成员口那一层是 2026-09-28 补的（一手 p30/r3：arm 写整值非零、字段为零的
		// record 字面量，按节点粒度看不见 ⇒ 基座门控脱落 ⇒ 作者的零被静默丢掉）。
		if refValueProvablyZero(n, r.Output) {
			return true
		}
		inner, ok := n.(*Aggregate)
		if !ok || inner == nil {
			continue
		}
		if len(inner.Groups) == 0 {
			key := inner.ID + "\x00"
			if seen[key] {
				continue
			}
			seen[key] = true
			if groupHasAuthoredZeroDeep(&AggregateGroup{Refs: inner.Refs}, lookup, seen) {
				return true
			}
			continue
		}
		matched := false
		for gi := range inner.Groups {
			g := &inner.Groups[gi]
			if r.Output != "" && g.Name != r.Output {
				continue
			}
			matched = true
			key := inner.ID + "\x00" + g.Name
			if seen[key] {
				continue
			}
			seen[key] = true
			if groupHasAuthoredZeroDeep(g, lookup, seen) {
				return true
			}
		}
		if !matched && r.Output != "" {
			for gi := range inner.Groups {
				g := &inner.Groups[gi]
				key := inner.ID + "\x00" + g.Name
				if seen[key] {
					continue
				}
				seen[key] = true
				if groupHasAuthoredZeroDeep(g, lookup, seen) {
					return true
				}
			}
		}
	}
	return false
}

// isSinkableRelayEmitterNode 报告节点是否是**可下沉的纯发射器**（relay 承载路径）：编译器自建 shim、
// 非零值标注、纯（无副作用）。
//
// **判据是「纯」，不是「无输入」（2026-09-28 放宽，R-124 记录半一手）**：
// 旧口径要求「无输入」——把「有输入」当成「携载真实臂值」的证据。那是**过宽的代理**：
// 本 pass 只下沉**宿主之前**的语句（`nodeStmtIndexBefore` 前提），它的输入生产者因此
// 也在宿主之前 ⇒ 搬进未写臂后输入**仍可读**，而 shim 是封闭纯子集（算术 / 内建 / 聚合 /
// json 守卫 / 三元）⇒ 「无条件执行」换成「该臂执行时执行」不改值。
// 一手（2026-09-28）：`RC r = { "a": k, "b": "x" }; if (q=="a") { r = {…}; } return r.a;`
// 在旧口径下不下沉 ⇒ 首编 8 节点 / 二编 6 节点（I1b@2）；放宽后 **ok:true**。
// 仍排除：`ZeroOf ≠ nil`（零载体 = P28 的域，方向相反）、非 shim（用户 foreign code
// 可能不纯 ⇒ 搬进条件臂会改副作用）、以及任何非本宿主之前的语句。
//
// **闭集 = 目标 lowering 的纯发射面**（字符串字面量 → `*TextProcess`，其余标量 /
// 容器字面量 → `*CodeRun` shim）——加一支必须同时说明该节点是「纯发射器」
// （无副作用 ⇒ 把「无条件执行」换成「该臂执行时执行」不改值）。
func isSinkableRelayEmitterNode(n Node) bool {
	switch v := n.(type) {
	case *CodeRun:
		if v == nil || !v.Synthetic || v.ZeroOf != nil {
			return false
		}
		return strings.HasPrefix(v.Code, pyast.ShimMarker)
	case *TextProcess:
		// 字符串字面量在 coze-biz lowering 落成 TextProcess（`renderTextNode`），
		// 不是 CodeRun —— 只认 CodeRun 会让**字符串基座**整支漏掉（一手实测：
		// `string v = "z"; if (q == "a") { v = ""; } return v;` 不下沉 ⇒ 首编
		// 6 节点 / 二编 4 节点，且两条路径都返回 "z"）。判据与 CodeRun 支同尺：
		// 纯发射面（concat / const；split 产出列表、依赖分隔符语义 ⇒ 不在面内）。
		if v == nil {
			return false
		}
		return v.Method == "concat" || v.Method == "const"
	}
	return false
}

// isSinkableDirectEmitterNode = **直接候选**路径（`SinkBaseCarriers` 的逐组处置，非 relay 承载）
// 的判据：与 relay 路径的区别 = 候选**直接**引用基座口（没有臂内恒等载体这一层）。
// 今天两路判据**同尺**（都放宽到「纯 shim」）；本函数保留为**同位点单列**，
// 便于任一方向单独收紧时不动另一路（2026-09-28 一手：relay 路径必须放宽 —— 记录字面量
// 含变量字段的漂移；直接路径放宽的必要性由 §7.2 的 s3 一手支撑）。
// lookupNode 把「id → Node」表包成查表函数（`groupHasAuthoredZeroDeep` 的入参）：
// builder 侧传 `g.findAny`（未成图），pass 侧传本函数包的快照表。
func lookupNode(nodes map[string]Node) func(string) Node {
	return func(id string) Node { return nodes[id] }
}

func isSinkableDirectEmitterNode(n Node, grp *AggregateGroup, lookup func(string) Node) bool {
	switch v := n.(type) {
	case *CodeRun:
		if v == nil || !v.Synthetic || v.ZeroOf != nil {
			return false
		}
		if !strings.HasPrefix(v.Code, pyast.ShimMarker) {
			return false
		}
	case *TextProcess:
		if v == nil {
			return false
		}
		if v.Method != "concat" && v.Method != "const" {
			return false
		}
	default:
		return false
	}
	// 无输入的纯发射器 = 恒等下沉（两条路径同尺）。
	if len(nodeInputs(n)) == 0 {
		return true
	}
	// 有输入 ⇒ **只在值语义要求按路径门控时**下沉：本组结局里可能出现作者写的类型零值
	// （`groupHasAuthoredZeroDeep`）时，无条件的非零基座会在零写路径上胜出
	// （平台 merge = first_non_zero 跳过零候选）= 作者写的零被静默丢掉 ⇒ 必须搬进臂里。
	// 无零写时基座停在无条件位正是规范位（J §8 U12）⇒ 不动（一手：`f1f_alias_else_upstream`
	// 的 C 型形状由 `f1f_alias_arm_merge_claim_contract_test.go` 锁住，放宽即红）。
	return groupHasAuthoredZeroDeep(grp, lookup, map[string]bool{})
}

// nodeInputs 返回节点的输入源表（两支纯发射器的输入）。
func nodeInputs(n Node) []Source {
	switch v := n.(type) {
	case *CodeRun:
		return v.Inputs
	case *TextProcess:
		return v.Inputs
	}
	return nil
}

// nodeStmtIndexBefore 返回宿主块内**宿主之前**承载 nodeID 的直接 NodeStmt 下标
// （前绑定位）；不存在 → -1。
func nodeStmtIndexBefore(b *Block, hostIdx int, nodeID string) int {
	for i := 0; i < hostIdx && i < len(b.Stmts); i++ {
		if ns, ok := b.Stmts[i].(*NodeStmt); ok && ns.Node != nil && ns.Node.NodeID() == nodeID {
			return i
		}
	}
	return -1
}

// blockContainedNodeIDs 收集一个块**内含**的全部节点 id（直接语句 + 语句自身的
// 节点槽 + 嵌套块递归）。归属判据「该臂有没有这个候选的生产者」必须按**包含**
// 判（生产者可以落在臂内的嵌套子分支里），过宽只会让本规则放弃（fail-safe）。
func blockContainedNodeIDs(b *Block) map[string]bool {
	out := map[string]bool{}
	if b == nil {
		return out
	}
	add := func(n Node) {
		if isNilIRNode(n) || n.NodeID() == "" {
			return
		}
		out[n.NodeID()] = true
	}
	var stmt func(s Stmt)
	stmt = func(s Stmt) {
		if s == nil {
			return
		}
		switch v := s.(type) {
		case *NodeStmt:
			add(v.Node)
		case *IfStmt:
			add(v.Cond)
			add(v.Merge)
			add(v.Relay)
			blockContainedNodeIDsInto(v.Then, out)
			blockContainedNodeIDsInto(v.Else, out)
		case *AttemptStmt:
			add(v.Op)
			add(v.SuccessEnd)
			add(v.FailureEnd)
			add(v.Exit)
			blockContainedNodeIDsInto(v.Success, out)
			blockContainedNodeIDsInto(v.Failure, out)
		case *InlineStmt:
			add(v.Exit)
			blockContainedNodeIDsInto(v.Body, out)
		case *LoopStmt:
			add(v.Container)
			blockContainedNodeIDsInto(v.Body, out)
		case *ParallelStmt:
			for _, br := range v.Branches {
				blockContainedNodeIDsInto(br, out)
			}
		case *QaStmt:
			add(v.QA)
			add(v.Merge)
			for _, br := range v.Branches {
				blockContainedNodeIDsInto(br.Body, out)
			}
		case *SwitchStmt:
			add(v.Switch)
			add(v.Merge)
			for _, br := range v.Branches {
				blockContainedNodeIDsInto(br.Body, out)
			}
		case *ReturnStmt:
			add(v.End)
			if v.End != nil {
				add(v.End.FlowExitMerge)
			}
		}
	}
	for _, s := range b.Stmts {
		stmt(s)
	}
	return out
}

func blockContainedNodeIDsInto(b *Block, into map[string]bool) {
	if b == nil {
		return
	}
	for id := range blockContainedNodeIDs(b) {
		into[id] = true
	}
}

// armHasGroupCandidate 报告该臂内是否含有本组的任一候选生产者。
func armHasGroupCandidate(grp *AggregateGroup, armIDs map[string]bool) bool {
	for _, r := range grp.Refs {
		if r.Producer != "" && armIDs[r.Producer] {
			return true
		}
	}
	return false
}

// candidateRefsTo 统计 A 的全部候选槽里指向 producer 的 ref 条数。
func candidateRefsTo(agg *Aggregate, producer string) []Ref {
	var out []Ref
	for _, r := range agg.Refs {
		if r.Producer == producer {
			out = append(out, r)
		}
	}
	for _, grp := range agg.Groups {
		for _, r := range grp.Refs {
			if r.Producer == producer {
				out = append(out, r)
			}
		}
	}
	for _, r := range agg.ValueSources {
		if r.Producer == producer {
			out = append(out, r)
		}
	}
	return out
}

// totalConsumers 返回全图引用 census 里指向 producer 的引用站点总数
// （含本汇聚单元自己的候选槽）。
func totalConsumers(consumers map[string]map[string]int, producer string) int {
	total := 0
	for _, n := range consumers[producer] {
		total += n
	}
	return total
}
