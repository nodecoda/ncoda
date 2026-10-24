package ir

import (
	"sort"
	"strconv"
)

// flattenChainAggregates 折叠「候选 = 另一聚合输出」的链式聚合（S61 族，
// zero 语义 2026-09-09）。
//
// canonical（零值提升声明 + 嵌套 if 终止叶写）在 IR 构建期逐 if 物化 phi：
// 嵌套 if 的内层 join 成为外层 merge 的一个候选（聚合输出被聚合候选引用 =
// 数据链 phi）。coze-biz 平台真实导出形态 = 消费点单 merge（候选 = 终止叶写
// 直引；数据链 merge 语料实证 = 0）——内层 join 是纯脚手架：其 if 只赋值不
// 汇聚（「最外逃逸点 flat merge、内层只赋值不汇聚」）。
//
// 适用边界（2026-09-09 修正）：**coze-biz 专属规范化**，不在共享
// optimizeGraph 内运行 —— reference target/coze-studio 的链式聚合是真实 canvas 形状
// （flow.Project 双遍投影 / golden 锁定），共享 IR 删除内层 junction 会改变
// 其既有结果（违反共享层契约）。调用点 = internal/backend/cozebiz generate()
// 入口（compiler.Compile coze-biz-1.0 与 cozebiz 渲染测试共用此路径）。
//
// 规则（无形态枚举守卫）：聚合 B 的数据消费 = 另一聚合 A 的候选引用（整组
// 端口、无成员路径余段），且 B 所属**宿主**控制包含于 A 所属**宿主**的一个
// 臂内 → 把 B 的组候选按引用位置原位展开进 A（first_non_zero 保序替换 = 值
// 保持），删除 B 并让 B 的宿主变 merge-less（臂尾经外层汇聚汇合，控制流不
// 变）。重复到不动点（3 层嵌套链逐层外展）。
//
// 2026-09-20（生成本发现，家族分裂收口）：本 pass 的头注一直写「无形态枚举
// 守卫」，而实现里**只有 IfStmt 宿主**能折叠 —— `QaStmt`/`SwitchStmt` 的
// Merge 被显式写成 `ownerIf[..] = nil`（占位拒绝），`AttemptStmt.Exit` 压根
// 没登记。后果：**同一形状换宿主就换产物**。
//
//	`if (n==1) { switch (k) { … 两臂都写 doc1 … } } else { }`
//	  ⇒ 内层 switch 的 merge 不折叠，wire 上出现**两个组名同为 `doc1` 的
//	    merge**（内层 100007 候选 = 两臂 producer；外层 100008 候选 = 100007.doc1
//	    + 零值前绑定）⇒ raising 侧「同作用域两个汇聚单元抢同一个名字」判据
//	    被迫把两者都转义成 `agg_v1_g0`/`agg_v2_g0` ⇒ 再编译组名 = 转义名
//	    ≠ `doc1` ⇒ I1b ARTIFACT_DRIFT/PARAM（一轮就不动点，值本身没错，**名字
//	    与结构**错）。把同一个 `switch` 换成内层 `if` ⇒ 折叠生效、单 merge、
//	    逐字节不动点。attempt 宿主实测同一形态同一漂移（`/tmp` 复现见提交
//	    信息；生成器形状 `phi_switch` 9/9 红、`phi_ternary_in_concat` 同族）。
//	⇒ 判据 = 宿主差异只能是**数据**（J §4）：折叠规则的输入只有「宿主指针 +
//	  它的臂区」，所以实现里只允许出现 `junctionHostStmt` 这一个共同面，
//	  不允许出现宿主类型枚举。登记缺口（不假装）：值位置三元的汇聚单元**没有
//	  宿主语句**（它是表达式值）⇒ 本 pass 不覆盖它，由它的值通道负责。
//
// 消费普查补洞（2026-09-09）：collectIRConsumers 是 port 粒度且不读
// ReturnStmt.Value（subscope return 尾 varRef，End==nil）——只按 A-site 端口
// 覆盖判活会漏掉「B 的输出仍被包围臂的 subscope ReturnStmt 当值载体引用」
// （07_template 三元实证：ghost edge）。折叠前扫描：紧邻 B 所属 if 之后、同
// Block 内的 End==nil ReturnStmt 以 B 为尾值 = 包围臂的规范化值载体（折叠后
// 置空其 Value → 透传 junction 出口 = B 组候选保序，语义保持）；任何其它
// 位置/形态引用 B 的不可见消费（含 End!=nil 的 End ref）→ 拒绝折叠（保守，
// 链形态保留 = 折叠前行为）。
// junctionHostStmt 是「承载结构汇聚单元（Primary Aggregate）的宿主语句」的
// 共同面：折叠规则需要且只需要两件数据 —— 宿主指针（读 / 清 Merge）与它的
// **臂区**（控制包含判据的搜索域）。四个语句宿主（if / switch / ask /
// attempt）实现同一组方法 ⇒ 规则里不再有宿主枚举（一处定义，J §4）。
//
// 谁**不**实现它：值位置三元（第 5 宿主）的汇聚单元是**表达式值**，没有宿主
// 语句；NodeStmt 位聚合是走廊链节点（折叠破坏控制顺序，见 nodeStmtAggs）。
// 两者都不参与折叠 —— 这是「没有宿主指针」的事实，不是宿主名单。
type junctionHostStmt interface {
	Stmt
	junctionMerge() *Aggregate
	clearJunctionMerge()
	junctionArmBlocks() []*Block
	// junctionFoldAdmitted —— 本宿主的「臂 join」折掉之后，**自家 raising 今天
	// 读不读得回**（两半同步的状态位，不是形态偏好、也不是宿主名单的审美）。
	//
	// 一手实测（2026-09-20，生成器形状 `phi_switch` 触发；四个最小源在
	// `/tmp/ps/`，判据 = `five-step-rt --compiler/--decompiler`）：
	//   · if / switch：raising 的条件结构识别**按端口归属候选**（condition /
	//     intent 的臂端口 + 臂集 inside 递归），臂 producer 直接进外层汇聚时
	//     照样认得出 ⇒ 折叠后 3 轮不动点（`d_ifsw` 绿，`r1` = 规范形文本）。
	//   · ask / attempt：raising 侧**要求臂自己的 join 在场**才认得出宿主
	//     （attempt = RS-ERR-002「PT3 未被 attempt 识别认领」+ RS-MRG-001；
	//     ask = QA 结构识别与 form 记录单元）。折叠删掉 join 之后**自家产物
	//     读不回**：`h_ifqa` 由 [TEXT_DRIFT/STRUCTURAL + ARTIFACT_DRIFT/TOPOLOGY
	//     + MATERIALIZED_GROWTH]@r2 **恶化**成 DECOMPILE_FAIL@r1；`g_ifatt` 由
	//     ARTIFACT_DRIFT/PARAM@r2 **恶化**成 DECOMPILE_FAIL@r1。按棘轮口径
	//     （「类别恶化 = 红」）不得折叠 —— 宁可保留已知漂移（已登记为缺口）。
	//   · **2026-10-12 复核 · 本条仍承重（不是过期保守）**：把 QaStmt / AttemptStmt
	//     的 `junctionFoldAdmitted()` 一并置 true 复跑（变体编译器）⇒ 生成器 seed0 的
	//     `gen_ask_d{1,2,3}_s*_seed0` **9 源** `DECOMPILE_FAIL@r1`（棘轮 0→9）；语料 684 里
	//     `7644029608732885046_S225_sd2_dianshang_tupian_shipin` **1 源** `COMPILE_FAIL@r2`
	//     （`UNDEFINED_VAR v10`）⇒ 退出条件**未达成**（合成形 + 真实语料**两处**都炸），本条保留。
	//     （同一轮把 if/switch 的「臂数上界」证伪并删除 —— 见
	//     `docs/five-step/residuals.yaml#R-205`；两条 guard 同形、结论相反。）
	// 退出条件（登记，不假装）：raising 按 `joinKind` 认规范形（J §7 第 4 步）
	// 落地后这两个宿主与 if/switch 同判 ⇒ 删掉本方法，四宿主一起折。
	junctionFoldAdmitted() bool
}

func (s *IfStmt) junctionMerge() *Aggregate  { return s.Merge }
func (s *IfStmt) clearJunctionMerge()        { s.Merge = nil }
func (s *IfStmt) junctionFoldAdmitted() bool { return true }
func (s *IfStmt) junctionArmBlocks() []*Block {
	return []*Block{s.Then, s.Else}
}

func (s *SwitchStmt) junctionMerge() *Aggregate  { return s.Merge }
func (s *SwitchStmt) clearJunctionMerge()        { s.Merge = nil }
func (s *SwitchStmt) junctionFoldAdmitted() bool { return true }
func (s *SwitchStmt) junctionArmBlocks() []*Block {
	out := make([]*Block, 0, len(s.Branches))
	for _, br := range s.Branches {
		out = append(out, br.Body)
	}
	return out
}

func (s *QaStmt) junctionMerge() *Aggregate  { return s.Merge }
func (s *QaStmt) clearJunctionMerge()        { s.Merge = nil }
func (s *QaStmt) junctionFoldAdmitted() bool { return false }
func (s *QaStmt) junctionArmBlocks() []*Block {
	out := make([]*Block, 0, len(s.Branches))
	for _, br := range s.Branches {
		out = append(out, br.Body)
	}
	return out
}

func (s *AttemptStmt) junctionMerge() *Aggregate  { return s.Exit }
func (s *AttemptStmt) clearJunctionMerge()        { s.Exit = nil }
func (s *AttemptStmt) junctionFoldAdmitted() bool { return false }
func (s *AttemptStmt) junctionArmBlocks() []*Block {
	return []*Block{s.Success, s.Failure}
}

// 臂数上界（旧 `junctionArmsAdmitted`，`内层臂数 <= 2`）已于 2026-10-12 **移除**
// （嵌套 switch 漂移族一刀，证据见 `docs/five-step/residuals.yaml#R-205`）。
//
// 历史：2026-09-20 为挡 RS-MRG-001（折叠把内层 3 臂候选搬进外层同一条臂 ⇒
// raising 的臂归属证明要求「同臂多候选落在互不相同的嵌套位点」，而当时内层臂
// 尚未生成成语句 ⇒ 位点不可证 ⇒ 反编译器拒收自家产物）加了 `臂数 <= 2` 上界。
//
// 该前提**已失效**（一手，2026-10-12）：内层 3/4/8 臂 switch 折进外层宿主后往返
// **定点**、无 RS-MRG-001 —— 反编译器已能按**端口归属**证明同臂候选位点
// （`/tmp/fam/G1_switch3_in_if.ncoda` = 注释里那条 `z_sw3_in_if` 的最小复现，
// 现行折叠 vs 不折叠均绿；F3/F4/F6/F7/F8/F9 族全绿）。且 `emitSwitch` **无条件
// 合成 default 臂** ⇒ 任何 ≥2 case 的 switch 都是 ≥3 臂 ⇒ 旧上界把嵌套 switch
// 的常见形态**整体**挡在折叠之外（F3：`switch{a:{switch{x,y}}} default:{}`，
// 一轮 8→9 节点 `MATERIALIZED_GROWTH`）。
//
// 折叠的普遍条件仍在：`junctionFoldAdmitted`（Qa/Attempt 不折）+「全候选站点同源」
// + 控制包含 + 臂门控回施。臂数是**数值**守卫、与形态耦合，删之；不再补新阈值。

func FlattenChainAggregates(g *Graph) {
	if g == nil || g.Root == nil {
		return
	}
	flattenChainAggregates(g)
}

func flattenChainAggregates(g *Graph) {
	for changed := true; changed; {
		changed = false
		nodes := collectIRNodes(g)
		aggByID := map[string]*Aggregate{}
		for id, n := range nodes {
			if a, ok := n.(*Aggregate); ok {
				aggByID[id] = a
			}
		}
		if len(aggByID) == 0 {
			return
		}
		// 聚合 → 所属宿主语句（四个语句宿主同构，无名单）。NodeStmt 位聚合是
		// 走廊链节点，折叠破坏控制顺序 → 拒（nodeStmtAggs）。
		ownerHost := map[string]junctionHostStmt{}
		nodeStmtAggs := map[string]bool{}
		var walkS func(s Stmt)
		var walkB func(b *Block)
		walkS = func(s Stmt) {
			if s == nil {
				return
			}
			switch v := s.(type) {
			case *NodeStmt:
				if _, ok := v.Node.(*Aggregate); ok {
					nodeStmtAggs[v.Node.NodeID()] = true
				}
			case *IfStmt:
				registerJunctionHost(ownerHost, v)
				walkB(v.Then)
				walkB(v.Else)
			case *AttemptStmt:
				registerJunctionHost(ownerHost, v)
				walkB(v.Success)
				walkB(v.Failure)
			case *LoopStmt:
				walkB(v.Body)
			case *ParallelStmt:
				for _, br := range v.Branches {
					walkB(br)
				}
			case *QaStmt:
				registerJunctionHost(ownerHost, v)
				for _, br := range v.Branches {
					walkB(br.Body)
				}
			case *SwitchStmt:
				registerJunctionHost(ownerHost, v)
				for _, br := range v.Branches {
					walkB(br.Body)
				}
			case *InlineStmt:
				walkB(v.Body)
			}
		}
		walkB = func(b *Block) {
			if b == nil {
				return
			}
			for _, s := range b.Stmts {
				walkS(s)
			}
		}
		walkB(g.Root)

		// sites: 聚合输出被哪些聚合候选引用。
		type site struct {
			outer *Aggregate
			gi    int // -1 = legacy outer.Refs
			ri    int
		}
		sites := map[string][]site{}
		for _, a := range aggByID {
			addRef := func(gi, ri int, r Ref) {
				if aggByID[r.Producer] != nil {
					sites[r.Producer] = append(sites[r.Producer], site{outer: a, gi: gi, ri: ri})
				}
			}
			if len(a.Groups) == 0 {
				for ri, r := range a.Refs {
					addRef(-1, ri, r)
				}
			}
			for gi := range a.Groups {
				for ri, r := range a.Groups[gi].Refs {
					addRef(gi, ri, r)
				}
			}
		}

		// consumers: 全图数据消费（含容器输出/普通节点输入/候选引用）。
		consumers := collectIRConsumers(g, nodes)

		for _, bid := range sortedAggregateIDs(aggByID) {
			b := aggByID[bid]
			ss := sites[bid]
			if len(ss) == 0 {
				continue
			}
			owner, ok := ownerHost[bid]
			if !ok || nodeStmtAggs[bid] || !owner.junctionFoldAdmitted() {
				continue
			}
			// 全部候选消费点必须落在同一外层聚合 A 上，且每处引用 B 的整组
			// 端口（portName 无成员路径余段；B.Groups 需含该组）。
			outerID := ""
			okAll := true
			for _, s := range ss {
				if outerID == "" {
					outerID = s.outer.ID
				} else if s.outer.ID != outerID {
					okAll = false
					break
				}
				var r Ref
				if s.gi < 0 {
					r = s.outer.Refs[s.ri]
				} else {
					r = s.outer.Groups[s.gi].Refs[s.ri]
				}
				port := portName(r.Output)
				if r.Output != port || !bGroupExists(b, port) {
					okAll = false
					break
				}
			}
			if !okAll {
				continue
			}
			// B 不得有候选引用之外的其它数据消费（普通节点输入 / 容器输出）。
			//
			// 判据是**站点数**而不是端口名集合（2026-09-11，S151/S175 实证）：
			// 折叠只重写 A 的候选槽，B 的其它消费站点**不会**被改写——删除 B
			// 后它们就是悬空引用。按端口名做子集判断会被"同名不同站点"骗过：
			// 臂内消费者（change 节点读 B 的 legacy Group1）与 A 的候选槽同用
			// Group1 端口名，于是折叠放行 → B 被删 → 消费者 ref 指向幽灵节点
			// （L2 产物 `ref_node` 不存在，L3 报 RS-NORM-003）。
			// 站点数是**可达性契约**的直接表达：每个端口的消费站点数必须恰好
			// 等于我们要重写的候选槽数，多一个就是外部消费 → 拒绝折叠。
			// 这不加任何形态守卫：它是"折叠只对 A-site 消费负责"的普遍条件。
			sitesForPort := map[string]int{}
			for _, s := range ss {
				if s.gi < 0 {
					sitesForPort[portName(s.outer.Refs[s.ri].Output)]++
				} else {
					sitesForPort[portName(s.outer.Groups[s.gi].Refs[s.ri].Output)]++
				}
			}
			ps, hasCons := consumers[bid]
			if hasCons {
				for p, count := range ps {
					if count != sitesForPort[p] {
						okAll = false
						break
					}
				}
			}
			// ss 里出现的端口必须都被 consumer 普查看见（普查覆盖不全时宁可
			// 保守：不折叠），否则站点数比较会因漏计而误判为大。
			for p, n := range sitesForPort {
				if ps[p] != n {
					okAll = false
					break
				}
			}
			if !okAll {
				continue
			}
			// 控制包含：B 的 if 必须嵌套在 A 的 if 的一个臂内（生产者沿外层
			// 汇聚路径可达 —— 跨路径候选是编译错误）。
			outerOwner, hasOuterOwner := ownerHost[outerID]
			if !hasOuterOwner || !hostNestedIn(owner, outerOwner) {
				continue
			}
			// 消费普查补洞：扫描 B 的不可见尾值载体（subscope ReturnStmt）。
			// 仅接受「紧邻 owner-if 之后的 End==nil 值载体」形态；其它引用
			// B 的不可见消费 → 拒绝折叠（保守保链，避免 ghost 引用）。
			imm, foreign := tailCarrierScan(g.Root, owner, bid)
			if foreign {
				continue
			}
			// 解释边界（2026-09-09）：折叠目标 A 若被编译器 shim CodeRun
			// 消费（record 装配 / or-吸收「纯读级联」读取 merge 组端口，
			// collapse pass 的旧产物兼容域 = 作者 `??` lowering，D2 立项
			// 内不实现不改变），本 pass 拒绝折叠——IR 级折平会改变折叠
			// pass 的输入链形态，令其无法把级联吸收到消费边界（X23
			// 复现实证：单 merge 4 组 + 4 个未吸收 shim）。折叠只服务于
			// 直接消费（plugin/end/候选链）的 canonical 形态；shim 级联
			// 保持链形态交还原折叠 pass。
			if syntheticCodeConsumes(g.Root, outerID) {
				continue
			}
			// 展开：B 组候选原位替换外层候选（保序 → first_non_zero 语义保持），
			// 并按**最终位置**重新施加臂门控（R-48 值保持：搬运改变候选的求值
			// 位置 ⇒ 默认位豁免不再成立；规则与物化点见 flatten_arm_regate.go）。
			//
			// 三段式（判定无副作用 ⇒ 证据不足时整次折叠放弃，不留半截产物）：
			//  1a 纯列表运算求出每个（聚合, 组）的最终候选表；
			//  1b 按最终位置判定需要臂内载体的候选（无类型权威 / 记录字段组 ⇒ 拒折）；
			//  2  物化载体并写回。
			type candGroup struct {
				agg *Aggregate
				gi  int // -1 = legacy single-group (agg.Refs)
			}
			final := map[candGroup][]Ref{}
			groupRefs := func(k candGroup) []Ref {
				if refs, ok := final[k]; ok {
					return refs
				}
				if k.gi < 0 {
					return k.agg.Refs
				}
				if k.gi >= len(k.agg.Groups) {
					return nil
				}
				return k.agg.Groups[k.gi].Refs
			}
			foldOK := true
			for _, s := range ss {
				k := candGroup{agg: s.outer, gi: s.gi}
				cur := groupRefs(k)
				if s.ri >= len(cur) {
					foldOK = false
					break
				}
				final[k] = spliceRefsAt(cur, s.ri, innerAggCands(b, portName(cur[s.ri].Output)))
			}
			outerArm := hostArmBlockContaining(outerOwner, owner)
			inOuterArms := hostArmNodeIDs(outerOwner)
			alloc := newGraphIDAlloc(nodes)
			type plannedGroup struct {
				k     candGroup
				needs []armRelayNeed
			}
			var plan []plannedGroup
			// 规划/物化按**确定性**的组序（id, gi）：载体 id 的分配顺序是产物的一部分，
			// 按 map 迭代序分配 = 同一输入两种产物（见 sortedAggregateIDs 注释）。
			keys := make([]candGroup, 0, len(final))
			for k := range final {
				keys = append(keys, k)
			}
			sort.Slice(keys, func(i, j int) bool {
				if keys[i].agg.ID != keys[j].agg.ID {
					return lessDottedID(keys[i].agg.ID, keys[j].agg.ID)
				}
				return keys[i].gi < keys[j].gi
			})
			if foldOK {
				for _, k := range keys {
					refs := final[k]
					needs, ok := planSplicedArmRegate(nodes, outerArm, inOuterArms, refs)
					if !ok {
						foldOK = false
						break
					}
					plan = append(plan, plannedGroup{k: k, needs: needs})
				}
			}
			if !foldOK {
				continue
			}
			for _, p := range plan {
				final[p.k] = canonicalizeSplicedCandidates(alloc, outerArm, final[p.k], p.needs)
			}
			for _, k := range keys {
				refs := final[k]
				if k.gi < 0 {
					k.agg.Refs = refs
					continue
				}
				k.agg.Groups[k.gi].Refs = refs
			}
			// 删除 B：nil 所属 if 的 Merge → 该 if merge-less（臂尾汇入外层
			// junction）；B 的 Group Owner（hoisted 变量名）保留在外层组上
			// （同一变量汇聚），无需迁移。紧邻值载体先置空 Value（透传
			// junction 出口），否则其 MaterializedProducer 仍指已删 B。
			if imm != nil {
				imm.Value = nil
			}
			owner.clearJunctionMerge()
			pruneDeadAggregates(g.Root, map[string]bool{bid: true})
			changed = true
			break // 结构已变 → 重扫
		}
	}
}

// tailCarrierScan 扫描聚合 B 的「不可见尾值载体」：collectIRConsumers 的
// port 粒度普查不读 ReturnStmt.Value（End==nil 的 subscope return 尾 varRef），
// 紧邻 B 所属 if 之后、同 Block 内的 End==nil ReturnStmt 以 B 为尾值 = 包围臂
// 的规范化值载体（「臂值经 subscope return 上浮到包围 merge」，值表达式 if
// 形态）——折叠后置空其 Value 即透传 junction 出口（= B 组候选保序），语义
// 保持。返回 (紧邻载体, 是否有其它不可见消费)。其它不可见消费（非紧邻位置
// 的 B 尾值、或任何 End ref 指向 B）→ foreign=true，调用方拒绝折叠。
func tailCarrierScan(root *Block, owner Stmt, bid string) (*ReturnStmt, bool) {
	var imm *ReturnStmt
	foreign := false
	var walkB func(b *Block)
	consider := func(b *Block, i int, s Stmt) {
		rs, ok := s.(*ReturnStmt)
		if !ok {
			return
		}
		if rs.End != nil {
			// End ref 指向 B 的返回：普查按 port 粒度可能放行（端口被
			// A-site 覆盖），删除 B 会留 ghost ref —— 保守拒绝。
			if rs.Value != nil && rs.Value.nodeID == bid {
				foreign = true
			}
			if rs.End != nil {
				for _, r := range rs.End.Inputs {
					if r.Producer == bid {
						foreign = true
					}
				}
				for _, o := range rs.End.Outputs {
					if o.Ref.Producer == bid {
						foreign = true
					}
				}
				if rs.End.Answer != nil {
					for _, bd := range rs.End.Answer.Bindings {
						if bd.Ref.Producer == bid {
							foreign = true
						}
					}
				}
			}
			return
		}
		if rs.Value == nil || rs.Value.nodeID != bid {
			return
		}
		// End==nil 且尾值 = B：仅接受「紧邻 owner 宿主之后」的规范化值载体
		// （owner 只作**语句指针**用 ⇒ 与宿主类型无关）。
		if i > 0 && b.Stmts[i-1] == owner {
			if imm != nil {
				foreign = true
			}
			imm = rs
			return
		}
		foreign = true
	}
	walkB = func(b *Block) {
		if b == nil {
			return
		}
		for i, s := range b.Stmts {
			consider(b, i, s)
			switch v := s.(type) {
			case *IfStmt:
				walkB(v.Then)
				walkB(v.Else)
			case *AttemptStmt:
				walkB(v.Success)
				walkB(v.Failure)
			case *LoopStmt:
				walkB(v.Body)
			case *ParallelStmt:
				for _, br := range v.Branches {
					walkB(br)
				}
			case *QaStmt:
				for _, br := range v.Branches {
					walkB(br.Body)
				}
			case *SwitchStmt:
				for _, br := range v.Branches {
					walkB(br.Body)
				}
			case *InlineStmt:
				walkB(v.Body)
			}
		}
	}
	walkB(root)
	return imm, foreign
}

// syntheticCodeConsumes reports whether any compiler-generated shim CodeRun
// reads an output port of aggregate id (interpretation boundary: the merge
// output is re-read/re-bundled by python shim code instead of being consumed
// directly). Such cascades belong to the YAML-level collapse pass (legacy /
// authored-`??` domain, D2); IR-level chain folding must not preempt them.
func syntheticCodeConsumes(root *Block, aggID string) bool {
	found := false
	var walkB func(b *Block)
	walkB = func(b *Block) {
		if b == nil || found {
			return
		}
		for _, s := range b.Stmts {
			if found {
				return
			}
			switch v := s.(type) {
			case *NodeStmt:
				if cr, ok := v.Node.(*CodeRun); ok && cr.Synthetic {
					for _, in := range cr.Inputs {
						if in.BlockID == aggID {
							found = true
							return
						}
					}
				}
			case *IfStmt:
				walkB(v.Then)
				walkB(v.Else)
			case *AttemptStmt:
				walkB(v.Success)
				walkB(v.Failure)
			case *LoopStmt:
				walkB(v.Body)
			case *ParallelStmt:
				for _, br := range v.Branches {
					walkB(br)
				}
			case *QaStmt:
				for _, br := range v.Branches {
					walkB(br.Body)
				}
			case *SwitchStmt:
				for _, br := range v.Branches {
					walkB(br.Body)
				}
			case *InlineStmt:
				walkB(v.Body)
			}
		}
	}
	walkB(root)
	return found
}

// bGroupExists reports whether aggregate b declares the named group (or is
// the legacy single "Group1" Refs form referenced by that same name).
func bGroupExists(b *Aggregate, port string) bool {
	if len(b.Groups) == 0 {
		// 口名 = 该汇聚自身的身份（单候选 phi 单元 = 它承载的变量名）；
		// 没有身份时才是平台「无名组」拼写（族 1）。
		return port == b.SingleGroupName()
	}
	for _, grp := range b.Groups {
		if grp.Name == port {
			return true
		}
	}
	return false
}

// sortedAggregateIDs 是折叠 pass 的**确定性**遍历序。
//
// 为什么必须有：pass 每轮只做**一次**成功折叠（`changed = true; break`），所以
// 「哪一个 (外层聚合 A, 内层聚合 B) 对先折」决定后续迭代的形态演化与节点 id 分配
// 顺序。按 map 迭代序（Go 随机化）取 = 同一个输入产出**两个不同的产物**。
//
// 一手实测（R-50，2026-09-24，**同提交的去重落地之前**）：`nested_junction_prebinding/d2/s1`
// 连编 12 次得到 **2 个 sha**（一半 1 份 relay / 一半 2 份同形 relay）—— 规范形必须是函数，
// 故此处排序键 = 聚合 id（数值优先，回退字典序）。
//
// 注意（如实记一笔）：去重落地之后，这一处的**可观测性**消失了 —— 撤掉排序，12 格与 27 个
// coze-reference 源各 8–20 次编译仍单 sha（一手，J §46.7 缺口 1）。排序因此是**构造保证**
// （遍历序写死），不是当前夹具可判的性质；不要据此以为它多余。
func sortedAggregateIDs(byID map[string]*Aggregate) []string {
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return lessDottedID(ids[i], ids[j]) })
	return ids
}

// lessDottedID 是 id 的自然序：两边都能解析成整数时按数值比（`100002` < `100010`），
// 否则字典序（IR 层 id 不保证是数字 —— 反编译/手工构造的图可以是任何标签）。
func lessDottedID(a, b string) bool {
	na, ea := strconv.Atoi(a)
	nb, eb := strconv.Atoi(b)
	if ea == nil && eb == nil && na != nb {
		return na < nb
	}
	return a < b
}

// innerAggCands returns the candidate list of aggregate b for the named
// group (legacy Refs when b has no Groups).
func innerAggCands(b *Aggregate, port string) []Ref {
	if len(b.Groups) == 0 {
		return append([]Ref(nil), b.Refs...)
	}
	for _, grp := range b.Groups {
		if grp.Name == port {
			return append([]Ref(nil), grp.Refs...)
		}
	}
	return nil
}

// spliceRefsAt replaces refs[i] with repl, preserving order.
//
// The expanded list is deduplicated (first occurrence wins).  The inner
// aggregate's candidate list and the outer slot can name the SAME producer:
// when an arm writes nothing, the junction contributes the pre-arm binding as
// its fallback candidate, and the enclosing slot may already carry that same
// pre-binding — so the in-place expansion produced the same `blockID.name`
// twice (measured: `merge 100011 组 v4: 100003.result×2`; B1 3 groups / B2 16
// groups over the corpus).  A repeated candidate selects the same value under
// the platform's first_non_zero merge semantics, so dropping it is an
// equivalence transform; real exports never repeat one (1324 workflows /
// 964 candidates / 0 duplicates).
func spliceRefsAt(refs []Ref, i int, repl []Ref) []Ref {
	if len(repl) == 0 {
		return append(refs[:i], refs[i+1:]...)
	}
	out := make([]Ref, 0, len(refs)+len(repl)-1)
	out = append(out, refs[:i]...)
	out = append(out, repl...)
	return dedupeRefs(append(out, refs[i+1:]...))
}

// dedupeRefs drops repeated (Producer, Output) candidates, keeping the first
// occurrence — candidate order IS first_non_zero semantics, so the earliest
// occurrence is the one that decides the value.
func dedupeRefs(refs []Ref) []Ref {
	if len(refs) < 2 {
		return refs
	}
	seen := make(map[Ref]bool, len(refs))
	out := make([]Ref, 0, len(refs))
	for _, r := range refs {
		if seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

// registerJunctionHost 登记一个宿主的汇聚单元（有才登记 —— 没挂汇聚单元
// 的宿主与折叠无关）。
func registerJunctionHost(m map[string]junctionHostStmt, h junctionHostStmt) {
	if agg := h.junctionMerge(); agg != nil {
		m[agg.ID] = h
	}
}

// hostNestedIn reports whether inner's owning host is nested inside one arm
// of outer's owning host (structural containment by pointer). 判据只看两个
// 宿主的**臂区**（数据），不看宿主类型 —— 同一形状换宿主必须同判。
func hostNestedIn(inner, outer junctionHostStmt) bool {
	if inner == nil || outer == nil || inner == outer {
		return false
	}
	var find func(b *Block) bool
	find = func(b *Block) bool {
		if b == nil {
			return false
		}
		for _, s := range b.Stmts {
			if s == inner {
				return true
			}
			if containsStmtPtr(s, inner, find) {
				return true
			}
		}
		return false
	}
	for _, arm := range outer.junctionArmBlocks() {
		if find(arm) {
			return true
		}
	}
	return false
}

// containsStmtPtr walks stmt s for the pointer target, using blockFind to
// descend into nested blocks.
func containsStmtPtr(s Stmt, target Stmt, blockFind func(*Block) bool) bool {
	if s == nil {
		return false
	}
	switch v := s.(type) {
	case *IfStmt:
		return blockFind(v.Then) || blockFind(v.Else)
	case *AttemptStmt:
		return blockFind(v.Success) || blockFind(v.Failure)
	case *LoopStmt:
		return blockFind(v.Body)
	case *ParallelStmt:
		for _, br := range v.Branches {
			if blockFind(br) {
				return true
			}
		}
	case *QaStmt:
		for _, br := range v.Branches {
			if blockFind(br.Body) {
				return true
			}
		}
	case *SwitchStmt:
		for _, br := range v.Branches {
			if blockFind(br.Body) {
				return true
			}
		}
	case *InlineStmt:
		return blockFind(v.Body)
	}
	return false
}
