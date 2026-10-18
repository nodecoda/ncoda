package ir

import "strings"

// 互斥臂汇聚（junction phi）——**臂数无知**的一条规则（2026-09-10，P01 正向
// 覆盖扩展）。
//
// 一个 junction = 一组互斥臂：if/else = 2 臂（else-less = 空合成臂）、
// switch = N 臂（case + default）、request_input = action 臂 + 可选 timeout 臂。
// 语言规则对宿主无差别：
//
//	「junction 之前声明的提升变量，在任一臂内被重新赋值 ⇒ 该变量是一个 phi
//	 单元 = 一个汇聚候选集合；junction 之后的下游引用解析到汇聚单元。」
//
// 候选（按臂序发射，臂序 = 我们自己的发射序，**不依赖平台候选序**）：
//   - 该臂写了变量 → 臂内真实写值直引（record 形按字段合成 dotted 端口）；
//   - 该臂在**站点谓词**上未写（`wrote = ok && (bound != base)` 为假——含
//     `v = null` 这类无 producer 的写：build_call.go 对 hoisted 变量早退、
//     不改 varToOutput）且**有** junction 前绑定 → 直引前绑定（复制语义）；
//   - 未写且**无**前绑定 → **不贡献候选**（`phiDefaultWanted` 政策；等价于
//     AGENTS.md merge zero 语义「未执行候选 = 零值被跳过」与「未执行分支被类型
//     零值吸收」）。**禁止**为「没写臂」物化零值/转发载体（P04：死载体 = X23
//     家族复发根因）——2026-09-12 M2 族收口时删除了该物化路径。
//
// **类型权威（junctionPhiType）**：phi 单元的通道选择与组类型由一条三级链解析
// （声明注解 → junction 前绑定端口 → ③ 臂内真写值的**声明端口**）；③ **只参与
// 通道选择与组类型，不参与候选物化**（候选只由「真写臂 / 有前绑定的未写臂」给出）。
//
// 通道选择（与 if 宿主既有口径同一条规则）：
//   - 单个标量形变量 ∧ 每条臂都以该变量为臂尾值 ∧ 非强制具名
//     → legacy 单组通道（Aggregate.Refs / "Group1"）；
//   - 否则逐变量具名组（组名 = 变量名，名字有界；list/record 形必须走这里，
//     整值 Group1 通道会丢成员语义 —— S115/read_book R3）。

// junctionArm 是 junction 的一条互斥臂的可汇聚视图。
type junctionArm struct {
	Port      string            // 该臂的平台分支端口（branch_i / default / true / false）
	Block     *Block            // 臂体（未写臂的零值 shim 物化点）
	Vars      map[string]varRef // 臂末局部视图（仅提升变量逃逸）
	TailBound map[string]bool   // 变量绑定 == 臂尾值
}

// armJunctionPhi 是一个 junction 的汇聚规划。
type armJunctionPhi struct {
	Refs     []Ref             // legacy 单组候选（仅标量整值通道）
	RefName  string            // legacy 单组通道的 wire 组名（= 该 phi 单元的变量名）
	Groups   []AggregateGroup  // 逐变量具名组 / record 逐字段组
	Ports    map[string]string // 变量名 → 具名组端口名
	Redirect map[string]varRef // 变量名 → junction 之后的绑定（占位，由调用方补 aggID）
	// RecordOwners = 参与逐字段汇聚的 record 逃逸变量，按规划序。**每个** record
	// 变量是一个独立汇聚单元：平台 variable_merge 是「一组名 → 一个输出口」，
	// 两个 record 共享一个 merge 时同名字段组会塌到同一个输出口（S40/S113：
	// 一个 LLM 的 text 是 string、另一个是 record{wenan} ⇒ post-join 两个变量的
	// `v.text` 都解析到那个被单一类型占住的端口，round2 反编译合成
	// `agg.text || agg.text.wenan`，L4 TYPE_MISMATCH）。缺省（≤1）⇒ 单汇聚单元。
	RecordOwners []string
}

// planArmJunctionPhi plans every escaping hoisted variable of one junction.
// forceNamed 屏蔽 legacy 单组通道（调用方已有具名组占用同一 Aggregate 时）。
func (g *Builder) planArmJunctionPhi(outerDeclared map[string]bool, outerVars map[string]varRef, arms []junctionArm) (armJunctionPhi, error) {
	plan := armJunctionPhi{Ports: map[string]string{}, Redirect: map[string]varRef{}}
	if len(arms) == 0 {
		return plan, nil
	}
	armVars := make([]map[string]varRef, 0, len(arms))
	for _, a := range arms {
		armVars = append(armVars, a.Vars)
	}
	scalars, err := g.changedHoistedByShapeArms(outerDeclared, outerVars, armVars, false)
	if err != nil {
		return plan, err
	}
	records, err := g.changedHoistedByShapeArms(outerDeclared, outerVars, armVars, true)
	if err != nil {
		return plan, err
	}
	if len(scalars) == 0 && len(records) == 0 {
		return plan, nil
	}
	// 臂尾值绑定集合的并集（legacy 单标量通道判据之一）。
	tailNames := map[string]bool{}
	for _, a := range arms {
		for name := range a.TailBound {
			tailNames[name] = true
		}
	}
	// legacy 单组通道的**前置**：本交汇点没有 record 形逃逸变量。
	// 理由 = Aggregate 不能同时带字段组与 legacy Refs/伪 Group1（同 if 宿主
	// 自 2026-09-08 起的规则：record 字段组的存在不得被 legacy 通道掐死，
	// 见 mid_branch_record_phi_contract_test.go）。旧核心缺这一条前置 ⇒
	// 「一个 legacy 标量 + 一个 record 逃逸」时早退成纯 legacy Refs、
	// record 字段组整体丢失（switch/QA/attempt 三宿主的潜在丢值）。
	legacy := len(records) == 0 && len(scalars) == 1 && len(tailNames) == 1 && tailNames[scalars[0]] && scalarShaped(scalars[0], outerVars, armVars, g)
	if legacy {
		armAllTail := true
		for _, a := range arms {
			if !a.TailBound[scalars[0]] {
				armAllTail = false
				break
			}
		}
		if armAllTail {
			if refs := g.armCandidates(outerVars, arms, scalars[0], nil); len(refs) > 0 {
				plan.Refs = refs
				// 组名 = 汇聚单元身份（变量名）。与具名通道**同一把尺**：
				// 候选个数（1 / N）不改变身份的写法（族 1；见
				// docs/five-step/debt-classes.md §3）。不可拼写 ⇒ 留空，
				// 渲染器回落平台「无名组」拼写（不发明名字）。
				plan.RefName = phiPortName(scalars[0])
				if plan.RefName == "" {
					// 变量名不可拼成口名（理论上不可达：绑定层的变量名是标识符）
					// ⇒ 仍然必须有**身份**，否则与另一个无名单元同名（R-85）。
					plan.RefName = g.allocateUnnamedGroupName()
				}
				plan.Redirect[scalars[0]] = varRef{output: plan.RefName, kind: "legacy"}
				return plan, nil
			}
		}
	}
	// 具名组通道：逐变量（标量）或逐字段（record）。
	//
	// **组序 = 规范序**：record 逐字段组在前、标量逐变量组在后，各自按名排序。
	// 不得改成「两类名字合并排序」——实测（2026-09-20，familyC S130 前缀归一化）
	// 混合交汇（1 个中段 record + 1 个标量）下合并排序会把标量组排到 record 组
	// 之前（`agg_v1_g0` < `v12`），使每个汇聚单元拿到的组下标对调 ⇒ 产物与既有
	// canonical 逐字节不同（语义等价但**不是**同一表示；本仓判据是字节级固定点）。
	// 该序即 if 宿主自 2026-09-04 起的序（record 规划在前、scalarPhiGroups 在后）。
	sortNamesNatural(records)
	sortNamesNatural(scalars)
	names := append(append([]string{}, records...), scalars...)
	for _, name := range names {
		levels := g.junctionPhiLevels(name, outerVars, armVars)
		shape := firstSlotType(levels)
		if shape == nil {
			// 收口（J §31，2026-09-23；原 C7/R2 登记缺口）：**不再静默跳过**。
			// 静默跳过 = 「无类型权威就不建 phi 单元」⇒ junction 退化零候选 ⇒
			// 下游按单臂 producer 取值（另一臂赋值静默消失、end 只绑一条臂，
			// L1/L2 全绿）。同一把尺现在与循环状态槽一致：全缺 ⇒ fail loud。
			//
			// 本循环的**门**在上游（`changedHoistedByShapeArms::mark` 用同一条链
			// 过滤，见 `build_return.go`）—— 甲落地后门也 fail loud ⇒ 正常情况下
			// 先红在门外。留这一支是「同一规则的两个消费点必须同解」：判定一处
			// （`firstSlotType`）、失败形态一处（`missingSlotTypeError`）。
			return plan, missingSlotTypeError("convergence slot", name, levels)
		}
		if len(shape.Props) > 0 {
			// **单元级**作者零值判据（2026-09-28；一手 p30/r3）：记录单元的成立前提是
			// 「各组**同位候选同生产者**」（record_unit.go / R-13），故门控必须整条
			// 记录**一起做或一起不做** —— 逐字段各判一次会让 `a` 组被门控（物化 relay）、
			// `b` 组不被门控（仍引臂外基座）⇒ 组间候选生产者序列不一致 ⇒ 反编译认不出
			// 记录 ⇒ 读成逐字段标量 `agg = v1.a` ⇒ 二编 7 节点 ≠ 首编 9 节点。
			// 判据仍只看**作者写的零值**（`refValueProvablyZero`），只是在单元内取并集。
			fields := sortedTypeFields(shape)
			raw := make([][]armCandidate, len(fields))
			unitZero := false
			for i, field := range fields {
				raw[i] = g.armCandidatesRaw(outerVars, arms, name, &armFieldRef{field: field, typ: shape.Props[field], record: shape})
				if g.armUnitHasAuthoredZero(raw[i], field) {
					unitZero = true
				}
			}
			var groups []AggregateGroup
			for i, field := range fields {
				fieldType := shape.Props[field]
				group := AggregateGroup{Name: field, Type: fieldType, Owner: name}
				group.Refs = g.armGateCandidates(raw[i], unitZero)
				if len(group.Refs) > 0 {
					groups = append(groups, group)
				}
			}
			if len(groups) > 0 {
				plan.Groups = append(plan.Groups, groups...)
				plan.Redirect[name] = varRef{kind: "aggregate"} // 逐字段：{merge}.<field>
				plan.RecordOwners = append(plan.RecordOwners, name)
			}
			continue
		}
		port := phiPortName(name)
		if port == "" {
			// 收口（J §31）：同一把尺的另一半（`J §9.3` R2「或槽名不可拼写」）。
			// 旧处置 = 静默 `continue` ⇒ 不建组 ⇒ 该臂写值静默消失。修法不是
			// 虚构端口名（名字有界：反编译侧要按名字恢复同一个变量），而是拒绝。
			return plan, unspellableSlotNameError("convergence slot", name)
		}
		refs := g.armCandidates(outerVars, arms, name, nil)
		if len(refs) == 0 {
			continue
		}
		plan.Groups = append(plan.Groups, AggregateGroup{Name: port, Refs: refs, Type: shape, Owner: name})
		plan.Ports[name] = port
		plan.Redirect[name] = varRef{output: port, kind: "aggregate"}
	}
	return plan, nil
}

// assembleArmJunction —— **φ 核心的唯一实现**：给定一个互斥汇合的臂集合
// （`junctionArm`：端口 / 臂体 / 臂末视图 / 臂尾绑定）、junction 前的绑定与声明
// 视图、以及臂尾值通道，物化汇聚单元、提交逐变量重定向、并挂尾值。
//
// 宿主（`if` / `switch`(intent) / `ask`(QA) / `attempt` / 值位置三元）能影响结果的
// **唯一**途径是它送进来的**数据**：`arms` 的端口与区域、`armTails`（臂尾值通道）、
// `outerVars` / `outerDeclared`（junction 前的绑定与声明视图）。本函数内**禁止**
// 任何宿主分支 —— 判据 = `TestJunctionCoreHasSingleImplementation`（本包）+
// `docs/five-step/spec/J-join-canonical-form.md` §4 / §9.6。
//
// 尾值口径（一条规则、五宿主同）：「**零候选 ⇒ 没有值**」。具名组/字段组形态的
// merge 不声明合成 Group1 端口，挂 `{merge}.Group1` 会留下悬空 ref（X16/X4/X5 教训）；
// 零候选 junction 挂尾值更会合成出一个值消费点，使 `optimizeGraph` 认它「被消费」
// 而保留一具空聚合（产物里 `mergeGroups=[{name:Group1,variables:[]}]`，反编译只能
// 读成显式零值投影 ⇒ `compile∘raise` 每轮换形，s1.x 实测 5 轮才收敛）。
func (g *Builder) assembleArmJunction(arms []junctionArm, outerDeclared map[string]bool, outerVars map[string]varRef, armTails []armTailValue, valueSlot string) (armJunctionResult, error) {
	armValues := g.armTailValues(armTails)
	aggID := g.nextID()
	agg := g.newValueAggregate(aggID, armValues)
	// 槽名 = **宿主提供的赋值目标名**（J §4：宿主差异 = 数据）。值位置三元的整值
	// 由调用方交给 `T v = …` / `v = …` 的**目标**，承载它的 legacy 单组通道就该用
	// 这个名，而不是平台对「无名组」的拼写 `Group1` —— 否则同语义的
	// `v = (c) ? A : B;` 与 `if (c) { v = A; } else { v = B; }` 产出两种 wire
	// （U6，2026-09-20 由证据判定：唯一表示被违反 + `Group1` 是作者从未书写的
	// 标识符）。**只影响 legacy 单组通道**（`Groups` 为空）：具名/逐字段通道的组名
	// 来自变量名/字段名，值位置整值 = merge 节点本身（`path: ""`），与槽名无关。
	// 且只在**值通道**（`armValues` 非空）生效：臂无值 ⇒ 走提升变量规划，槽名由
	// 规划自己给出（`plan.RefName`），宿主不得覆盖。
	if len(armValues) > 0 && valueSlot != "" && len(agg.Groups) == 0 {
		agg.RefName = valueSlot
	}
	if len(armValues) == 0 {
		// 臂显式 return 的尾值通道为空 ⇒ 走提升变量 phi 规划（与 if 同序位）。
		plan, err := g.planArmJunctionPhi(outerDeclared, outerVars, arms)
		if err != nil {
			return armJunctionResult{}, err
		}
		if len(plan.RecordOwners) > 1 {
			// 多 record 逃逸 ⇒ 每个 record 一个汇聚单元（首个并入结构 Merge、
			// 其余线性链）。该规则自 2026-09-04（S40/S113）起只存在于 if 宿主，
			// 三宿主缺 ⇒ 本函数吸收（见 materializeMultiRecordJunction）。
			return g.materializeMultiRecordJunction(aggID, plan), nil
		}
		agg.Groups = plan.Groups
		if len(plan.Refs) > 0 {
			agg.Refs = plan.Refs
			agg.RefName = plan.RefName
		}
		for name, ref := range plan.Redirect {
			ref.nodeID = aggID
			g.varToOutput[name] = ref
		}
	}
	g.nameUnnamedConvergenceUnit(agg)
	g.add(agg)
	g.tailRef = junctionTailRef(agg)
	return armJunctionResult{Primary: agg}, nil
}

// armJunctionResult 是一次 junction 汇聚的物化结果。
//
// Primary = 结构 Merge（挂宿主语句：`IfStmt.Merge` / `SwitchStmt.Merge` /
// `AttemptStmt.Exit` / …）；Chained = 其余汇聚单元，按序在**宿主语句之后**线性
// 发射（见 appendChainedArmJunctions —— 宿主append 自己的语句后必须调它）。
type armJunctionResult struct {
	Primary *Aggregate
	Chained []*Aggregate
}

// FlowIDs 返回宿主语句之后的续接点（链尾优先）。
func (r armJunctionResult) FlowIDs() []string {
	if n := len(r.Chained); n > 0 {
		return []string{r.Chained[n-1].ID}
	}
	if r.Primary == nil {
		return nil
	}
	return []string{r.Primary.ID}
}

// appendChainedArmJunctions 按序把线性链汇聚单元发射成 NodeStmt。
// **前置**：宿主语句已经 append —— 链必须在宿主语句之后，否则控制流序倒置。
func (g *Builder) appendChainedArmJunctions(r armJunctionResult) {
	for _, a := range r.Chained {
		v := varRef{nodeID: a.ID, output: ""}
		g.appendStmt(&NodeStmt{Node: a, Value: &v})
	}
}

// materializeMultiRecordJunction 物化「每个 record 逃逸变量一个汇聚单元」的形态
// （规则源：if 宿主 2026-09-04 S40/S113 修复；本函数 = 唯一实现，四宿主共用）。
//
// 形状（逐字沿用该修复）：首个 record 的字段组 + **全部标量组**并入结构 Merge；
// 其余 record 各自成一个独立 Aggregate，按规划序线性链在宿主语句之后。多组 phi
// 是**语句型汇聚**（平台 merge 只声明各组成员端口、无合成 Group1）⇒ tailRef = nil
// （否则父级把内层当 produced 分支候选、引用不存在的 `Group1`：X16/X4/X5/R1 实证）。
func (g *Builder) materializeMultiRecordJunction(aggID string, plan armJunctionPhi) armJunctionResult {
	isRecord := map[string]bool{}
	for _, n := range plan.RecordOwners {
		isRecord[n] = true
	}
	byOwner := map[string][]AggregateGroup{}
	var scalarGroups []AggregateGroup
	for _, grp := range plan.Groups {
		if isRecord[grp.Owner] {
			byOwner[grp.Owner] = append(byOwner[grp.Owner], grp)
			continue
		}
		scalarGroups = append(scalarGroups, grp)
	}
	first := plan.RecordOwners[0]
	primary := &Aggregate{ID: aggID, Groups: append(byOwner[first], scalarGroups...)}
	g.add(primary)
	g.tailRef = nil // 语句型汇聚：无合成 Group1 尾值（X16/X4/X5）
	if ref, ok := plan.Redirect[first]; ok {
		ref.nodeID = aggID
		g.varToOutput[first] = ref
	}
	for name, ref := range plan.Redirect {
		if isRecord[name] {
			continue
		}
		ref.nodeID = aggID
		g.varToOutput[name] = ref
	}
	res := armJunctionResult{Primary: primary}
	chainedIDs := []string{aggID}
	for _, name := range plan.RecordOwners[1:] {
		id := g.nextID()
		a := &Aggregate{ID: id, Groups: byOwner[name]}
		g.add(a)
		if ref, ok := plan.Redirect[name]; ok {
			ref.nodeID = id
			g.varToOutput[name] = ref
		}
		res.Chained = append(res.Chained, a)
		chainedIDs = append(chainedIDs, id)
	}
	g.consumeTreeNodes(chainedIDs...)
	g.flushTreeNodes()
	return res
}

// armFieldRef 请求 record 字段形的候选端口合成。
type armFieldRef struct {
	field string
	typ   *Type
	// record = 本单元的**整值记录类型**（`planArmJunctionPhi` 的 `shape`）。
	// 端口模型给不出容器口时（扁平成员口拼写：用户 `foreign code` 的记录结果
	// 声明的是逐字段口、没有「整个 record」的口）它是**唯一**的记录类型权威 ——
	// 臂门控要把记录值的门控粒度做成「一条记录载体」（R-13），就必须知道记录
	// 的全部字段，否则退化成逐字段标量载体（同一语义两种产物，且反编译认不回）。
	record *Type
}

// armCandidates = 「单元 = 本组」通道（标量变量 / legacy 单组）：候选收集 + 门控，
// 作者零值判据就地按本组算（标量的单元就是它自己那一组）。
func (g *Builder) armCandidates(outerVars map[string]varRef, arms []junctionArm, name string, field *armFieldRef) []Ref {
	cands := g.armCandidatesRaw(outerVars, arms, name, field)
	return g.armGateCandidates(cands, g.armUnitHasAuthoredZero(cands, unitPortName(name, field)))
}

// unitPortName = 汇聚单元在平台 merge 上的**组名**（嵌套递归按它匹配）：
// record 字段组的组名 = 字段名，标量单元的组名 = 变量的端口名。
func unitPortName(name string, field *armFieldRef) string {
	if field != nil {
		return field.field
	}
	return phiPortName(name)
}

// armCandidatesRaw builds the ordered candidate list of one hoisted variable
// across every arm of the junction (臂序 = 发射序) —— **未施加臂门控**。
// 记录变量必须逐字段收集完全部原候选、算出**单元级**的作者零值判据之后，
// 再对每个字段统一门控（见 planArmJunctionPhi 的 record 支）。
func (g *Builder) armCandidatesRaw(outerVars map[string]varRef, arms []junctionArm, name string, field *armFieldRef) []armCandidate {
	base := outerVars[name]
	// 写臂计数（标量形零值豁免用）：某臂「真的重绑了」name（site 级谓词）。
	// merge 必须有 ≥2 候选才是 φ（单候选 merge 无承载位，`TestRenderIfAggregateNoPseudoPort`
	// / `RS-MRG-001`）⇒ 跳过「可证零值前绑定」候选只在「跳过后仍留 ≥2 真候选」时发生。
	writeArms := 0
	if field == nil {
		for _, arm := range arms {
			if b, ok := arm.Vars[name]; ok && (b.nodeID != base.nodeID || b.output != base.output) {
				writeArms++
			}
		}
	}
	// 候选按臂序收集，臂门控在**收集完之后**统一施加：判据是「位置」
	// （默认位 = 最后一条），位置只有在整组候选收齐后才成立。
	var cands []armCandidate
	for _, arm := range arms {
		// 臂门控（arm_gate_candidate.go）：非默认位的候选必须**只在本臂被执行时
		// 存在**。臂外 producer（分支前绑定 / 臂外真写值）直引会被 merge 按位置
		// 恒取 ⇒ 条件对取值失效 ⇒ 该臂内物化恒等载体（可证零值除外）。
		var candType *Type
		if field != nil {
			candType = field.typ
		} else {
			candType = g.junctionPhiType(name, outerVars, armVarsOf(arms))
		}
		bound, ok := arm.Vars[name]
		wrote := ok && (bound.nodeID != base.nodeID || bound.output != base.output)
		if wrote {
			ref := Ref{Producer: bound.nodeID, Output: bound.output}
			var recType *Type
			if field != nil {
				ref = g.refForAggregateField(bound, field.field)
				// 绑定值是**记录**（声明口带字段）⇒ 该字段候选的门控粒度 = 整值记录。
				recType = g.recordPortOf(bound)
			}
			cands = append(cands, armCandidate{arm: arm.Block, ref: ref, typ: candType, rec: recType})
			continue
		}
		// record 形未写臂（field != nil）：**必须物化分支内载体**
		// （`materializeBranchDefault`：可证类型零 ⇒ 直引零值真相；否则恒等
		// relay），**不得**直引 junction 前绑定 —— 不得在此处加「前绑定非空就
		// 直引」的捷径。该捷径曾使下面两支成为**死代码**（三宿主因此缺 relay），
		// 规则正本 = `fold_zero_identity_candidates_test.go`（2026-09-13，含
		// 存活侧锚点：前绑定非零时 relay 是语义承载的，折叠它即改语义）。
		if field != nil {
			if !g.phiDefaultWanted(name, base) {
				continue // 无前绑定 / Optional null base：不产载体（P04 死载体禁令）
			}
			// EXPERIMENT(R-125)：与标量支同拼写（直引臂外基座的字段口），交门控按位置处置。
			// 记录类型权威：端口模型的容器口优先，取不到则用单元的整值记录类型
			// （扁平成员口拼写的基座 —— 门控据此铸**一条记录载体**而不是逐字段标量载体）。
			rec := g.recordPortOf(base)
			if rec == nil {
				rec = field.record
			}
			cands = append(cands, armCandidate{arm: arm.Block, ref: g.refForAggregateField(base, field.field), typ: field.typ, rec: rec})
			continue
		}
		// 标量形未写臂：继承 junction 前绑定（复制语义）——非默认位同样必须臂
		// 门控：绑定在 junction 之前恒可读，排在首位时直引即恒取位置 0
		// （merge 不咨询臂）。
		if base.nodeID != "" {
			// 可证零值前绑定 + ≥2 真写臂 ⇒ 跳过（零值豁免的标量面，与
			// materializeBranchDefault / gateArmCandidate 同一条判据）：merge 的 zero
			// 语义运行时本就跳过零候选 ⇒ 候选冗余。物化它产生静默漂移（一手 2026-10-12）：
			// 反编译产出的 `string G; G = "";` 零初值 + **无 default 臂**的 switch（空 default
			// 臂直落汇聚点 = 反编译省略该臂以保往返），重编时空 default 臂把零初值继承成
			// 额外候选（+ 合成零载体）⇒ c0(2 候选) ≠ c1(3 候选) = ARTIFACT_DRIFT/TOPOLOGY +
			// MATERIALIZED_GROWTH（响亮→静默红线）。反编译器**必须**发零初值（否则
			// `UNINITIALIZED_USE`：无 default 臂时 G 未定赋值）⇒ 只能编译器侧豁免。
			// 两条成立条件（任一）——都保证「跳过后 merge 仍 ≥2 真候选 = φ」：
			//   · writeArms ≥ 2：本 junction 自己有 ≥2 真写臂（顶层 / 独立汇聚）；
			//   · switchBranchDepth ≥ 1：本 junction 位于**switch 臂内**。此时它必被
			//     `FlattenChainAggregates` 折叠进外层 switch 汇聚单元 A（宿主 switch
			//     `junctionFoldAdmitted` = true，B 的宿主控制包含于 A 的臂内），由 A 提供
			//     ≥2 真候选 ⇒ 本层的单候选（跳过后）是**瞬态**、不物化成非 φ merge。
			//     嵌套无 default 臂 switch（R-205）：内层只有 1 真写臂 + 零值未写臂，
			//     build 期**不能**豁免（保 φ，见 switch_arm_phi_convergence_contract_test.go
			//     的存活侧锚点）；不豁免则该零候选随折叠展开进 A ⇒ A 候选 3 ≠ 平台真源 2
			//     = ARTIFACT_DRIFT/TOPOLOGY。故豁免条件须覆盖「臂内」这一情形。
			//     （attempt / ask 臂**不含**：其 `junctionFoldAdmitted` = false，折叠不达 ⇒
			//     跳过后本层会留单候选 = 非 φ。一手锚：R_attempt_nested 保持 2 候选
			//     = /tmp 复现，见 residue R-205 note。）
			if (writeArms >= 2 || g.switchBranchDepth >= 1) && refValueProvablyZero(g.findAny(base.nodeID), base.output) {
				continue
			}
			cands = append(cands, armCandidate{arm: arm.Block, ref: Ref{Producer: base.nodeID, Output: base.output}, typ: candType})
			continue
		}
		if !g.phiDefaultWanted(name, base) {
			continue // Optional null base：未写臂不贡献零值默认候选（M1）
		}
		// 未写臂而**有**前绑定：恒等 relay 载体（复制语义，`base != ""`）。
		// 无前绑定在 `phiDefaultWanted` 处已被拒绝，故此处 base 恒非空。
		if r, ok := g.scalarBranchDefault(arm.Block, base, g.junctionPhiType(name, outerVars, armVarsOf(arms))); ok {
			cands = append(cands, armCandidate{ref: r})
		}
	}
	// 门控由调用方施加：见 armCandidates / planArmJunctionPhi（记录单元级判据）。
	return cands
}

// armVarsOf projects a junction's arms to their variable snapshots.
func armVarsOf(arms []junctionArm) []map[string]varRef {
	out := make([]map[string]varRef, 0, len(arms))
	for _, a := range arms {
		out = append(out, a.Vars)
	}
	return out
}

// junctionPhiType 是 φ 槽类型权威的**查询**：有权威 ⇒ 类型；无权威 ⇒ nil。
// 判定只有一处（`firstSlotType`，`slot_type_authority.go`）；**决定**（无权威时
// 怎么办）不在这里，而在两个消费点（`J §31`）：
//
//	· 组装点 `planArmJunctionPhi`：**fail loud**（不再静默跳过）；
//	· 门 `changedHoistedByShapeArms::mark`：**fail loud**（2026-09-24 用户拍板
//	  「甲先行」，`J §31.7`）—— 旧行为「静默不作为 phi 单元 ⇒ 该臂写值静默消失」
//	  已废；根因面的补权威（乙）另立项。
//
// 调用点若只要「有没有类型」这个事实（臂候选的通道选择 / 臂内默认载体），
// nil 是**合法答案**，不需要也不允许把它变成错误。
func (g *Builder) junctionPhiType(name string, outerVars map[string]varRef, armVars []map[string]varRef) *Type {
	return firstSlotType(g.junctionPhiLevels(name, outerVars, armVars))
}

// junctionPhiLevels 是 φ 槽的**权威链数据**（顺序即优先级；判定与失败形态在
// `slot_type_authority.go`，一处实现 —— J §31）。宿主差异只能是数据（J §4）：
// 换宿主 = 换这份序列，不是换一套判定。
func (g *Builder) junctionPhiLevels(name string, outerVars map[string]varRef, armVars []map[string]varRef) []slotTypeLevel {
	base := outerVars[name]
	return []slotTypeLevel{
		{name: "声明类型", typ: func() *Type { return g.hoistedTypes[name] }},
		{name: "生产者口（汇聚前绑定）", typ: func() *Type {
			if base.nodeID == "" {
				return nil
			}
			// ② 级仍用 portTypeOf（其零候选 legacy `T("string")` 兜底 = 已登记的
			// C8 缺口，收口属 §16.1-C8）；③ 级用净化的 declaredPortType。来源表
			// 临时不对称、precedence 不变 ⇒ 结构性保证「今日成立的 phi 单元不会
			// 因净化退化」（见 §17 收口记录）。
			return g.portTypeOf(base.nodeID, base.output)
		}},
		{name: "生产者口（臂内写的声明端口）", typ: func() *Type {
			for _, av := range armVars {
				b, ok := av[name]
				if !ok || b.nodeID == "" {
					continue
				}
				if b.nodeID == base.nodeID && b.output == base.output {
					continue // 未真实重绑定该名（含 `v = null` 早退后的原绑定）
				}
				if t := g.declaredPortType(b.nodeID, b.output); t != nil {
					return t
				}
			}
			return nil
		}},
	}
}

// declaredPortType resolves a producer port type from **declared ports only**.
// It is the ③-level authority of junctionPhiType (and the destination of the
// C8 hardening); it deliberately does NOT reproduce portTypeOf's historical
// fallbacks, all of which are port-name-agnostic or schema-less and would
// contradict the phi unit's own candidates (2026-09-12 S2-gate R1: ToolCall
// `TObject({})` for *any* port name turned a `string` candidate set into an
// `object` group).
//
// Allowed (declared): Start params; CodeRun.OutTypes exact hit (dotted resolves
// through the container's Props, never the container shortcut); LLMCall
// OutTypes / nested Props chain / envelope & metadata contract ports;
// TextProcess `output` (type by method); Switch classificationId|reason;
// KnowledgeCall outputList[.field]|output; HTTPCall body|headers|statusCode;
// QA and SubWorkflowCall OutTypes; ToolCall / HTTPCall / AttemptStmt error
// ports when FailBranch; Loop item / StateTypes / OutTypes.
// Forbidden ⇒ nil: port-agnostic fallbacks (ToolCall `TObject({})`, TextProcess
// for any non-`output` name), singleOutputKey, LLMCall `output|result` aliases,
// the generic dotted first-segment fallback, and the zero-candidate legacy
// `Group1` string (C8).
func (g *Builder) declaredPortType(nodeID, output string) *Type {
	n := g.findAny(nodeID)
	if n == nil {
		return nil
	}
	if agg, ok := n.(*Aggregate); ok {
		// Aggregate 是结构输出：具名组端口是声明端口（含 dotted 组字段）；
		// legacy 单组以首候选 producer 的声明为准（与 wire firstDecl 同源）；
		// 零候选 legacy 的 `Group1` **不是**声明端口（C8 伪造源）⇒ nil。
		if len(agg.Groups) > 0 {
			if i := strings.Index(output, "."); i >= 0 {
				for _, group := range agg.Groups {
					if group.Name == output[:i] {
						if ft := dottedAggGroupField(group.Type, output[i+1:]); ft != nil {
							return ft
						}
					}
				}
				return nil
			}
			for _, group := range agg.Groups {
				if group.Name == output {
					return group.Type
				}
			}
			return nil
		}
		if (output == agg.SingleGroupName() || output == "") && len(agg.Refs) > 0 {
			return g.declaredPortType(agg.Refs[0].Producer, agg.Refs[0].Output)
		}
		return nil
	}
	switch nd := n.(type) {
	case *Start:
		for _, p := range nd.Params {
			if p.Name == output {
				return p.Type
			}
		}
	case *CodeRun:
		if t, ok := nd.OutTypes[output]; ok {
			return t
		}
		if i := strings.Index(output, "."); i >= 0 {
			// Props 遍历：容器自身是声明端口，成员类型取自其 Props（成员读
			// 绝不返回容器类型——那是 CodeRun 的 OutTypes[首段] 捷径）。
			if container, ok := nd.OutTypes[output[:i]]; ok && container != nil {
				if ft := container.Props[output[i+1:]]; ft != nil {
					return ft
				}
			}
		}
	case *LLMCall:
		if t, ok := nd.OutTypes[output]; ok {
			return t
		}
		if t := nestedOutputType(nd.OutTypes, output); t != nil {
			return t
		}
		if output == LLMEnvelopeOutput || strings.HasPrefix(output, LLMEnvelopeOutput+".") {
			props := map[string]*Type{"text": nd.OutTypes["text"], "reasoning_content": T("string"), "finish_reason": T("string"), "usage": TObject(map[string]*Type{})}
			if output == LLMEnvelopeOutput {
				return TObject(props)
			}
			if ft, ok := props[strings.TrimPrefix(output, LLMEnvelopeOutput+".")]; ok {
				return ft
			}
		}
		switch {
		case output == "reasoning_content", output == "finish_reason":
			return T("string")
		case output == "usage":
			return TObject(map[string]*Type{})
		case strings.HasPrefix(output, "usage."):
			return T("integer")
		}
	case *QA:
		if t, ok := nd.OutTypes[output]; ok {
			return t
		}
	case *TextProcess:
		if output == "output" {
			if nd.Method == "split" {
				return TList(T("string"))
			}
			return T("string")
		}
	case *Switch:
		if output == "classificationId" || output == "reason" {
			return T("string")
		}
	case *KnowledgeCall:
		switch {
		case output == "outputList":
			return KnowledgeOutputListType()
		case output == "output":
			return T("string")
		case strings.HasPrefix(output, "outputList."):
			for _, f := range KnowledgeOutputListFields() {
				if strings.TrimPrefix(output, "outputList.") == f {
					return T("string")
				}
			}
		}
	case *HTTPCall:
		// 端口类型 = **语言层**声明（唯一处 = httpResultPortType，
		// builtin_result_ports.go）。此处曾按平台物理类型手抄一份
		// （body/headers 皆 string），与 portTypeOfNode 的语言层答案
		// （headers = map<string,any>）分歧 —— 同一个 (节点, 端口) 两个答案，
		// 正是本族要消灭的副本形态。同一条规则，不再有第二份。
		if t, ok := httpResultPortType(output); ok {
			return t
		}
		if nd.Exception != nil && nd.Exception.FailBranch {
			return attemptErrorType(output)
		}
	case *SubWorkflowCall:
		if t, ok := nd.OutTypes[output]; ok {
			return t
		}
	case *Loop:
		if t := loopItemType(nd, output); t != nil {
			return t
		}
		if t, ok := nd.StateTypes[output]; ok {
			return t
		}
		if t, ok := nd.OutTypes[output]; ok {
			return t
		}
	case *AttemptStmt:
		switch output {
		case "error":
			return TObject(map[string]*Type{"error_type": T("string"), "error_message": T("string")})
		case "error.error_type", "error_type", "error.error_message", "error_message":
			return T("string")
		}
	case *ToolCall:
		// ③ 级 = 能力注册表的**声明输出表**（`ResultType`，字段名 = 物理口名；
		// 与 CodeRun 同解：先查直口，再按 Props 解 dotted 成员 —— 容器自身是
		// 声明端口，成员类型取自其 Props，成员读绝不返回容器类型）。
		// attempt 错误端口是声明契约，允许。
		if nd.ResultType != nil {
			if t, ok := nd.ResultType.Props[output]; ok {
				return t
			}
			if t := nestedOutputType(nd.ResultType.Props, output); t != nil {
				return t
			}
		}
		if nd.Exception != nil && nd.Exception.FailBranch {
			return attemptErrorType(output)
		}
	}
	return nil
}

// KnowledgeOutputListType 是 `knowledge(...)` 检索结果端口的**唯一声明点**。
//
// 声明：`outputList = list<object{documentId: integer, output: string}>`；
// 另有裸 `output = string` 端口。三处消费（phi 单元类型、dotted 端口类型、
// 条件比较的字段窄化）与后端投影（coze-biz 的 node_outputs 渲染）一律读这里，
// 不得各自再写一份字面量——副本不同步正是「同一语义两处真相」的出生形态。
//
// 字段集 = **平台事实**（2026-10-17 用户裁定「按平台事实收敛」，K-H2 收口）：
// coze-studio 运行期对知识库检索结果构造 `{documentId: int64, output: string}`
// （backend/domain/workflow/internal/nodes/knowledge/knowledge_retrieve.go:215-220）。
// 旧的 `{content, title, url}` 是**无语料/无平台承载**的语义发明 ⇒ 用户写
// `result[0].output` 会拿到没有端口的成员引用，现退场。
func KnowledgeOutputListType() *Type {
	return TList(TObject(map[string]*Type{
		"documentId": T("integer"),
		"output":     T("string"),
	}))
}

// KnowledgeOutputListFields 是上面声明的元素字段集（顺序无关），供需要逐字段
// 分派的调用点使用，避免再抄一遍字面量。
func KnowledgeOutputListFields() []string { return []string{"documentId", "output"} }
