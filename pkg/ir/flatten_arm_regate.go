package ir

import "strconv"

// flatten_arm_regate.go — 链式折叠的**搬运门控**（R-48 值保持，2026-09-24）。
//
// ── 为什么（一手反例）───────────────────────────────────────────────────
// 链形态里内层聚合 B 的候选只在「B 所属的那条臂执行」时才被求值 —— B 的 merge
// 物理上就在本臂内。把 B 的候选**原位搬进外层聚合 A 的候选表**之后，它们对 A 的
// **所有**臂可见 ⇒ 任何「产出方不比 B 更晚存在」的候选都会在别的臂里胜出。
//
//	string v; v = "";
//	if (c1) { v = x; if (c2) { v = "z"; } }   // x = 函数参数口（start 无条件产出）
//	return v;
//
//	折叠前：A 的候选 = [B(臂内), ""]；B 的候选 = ["z", x(继承前绑定)]。
//	        x 在 B 的**默认位**⇒ 免门控是合法的：B 的 merge 只在本臂内求值。
//	折叠后：A 的候选 = ["z", x, ""] —— x 落在**非默认位**，而它无条件存在
//	        ⇒ `!c1` 时外层 merge 照样取到 x（实测期望 `""`、实测 `"XX"`）。
//
// ── 规则（与 arm_gate_candidate.go 的臂门控**同一条**）──────────────────
// 候选存在 ⟺ 本臂执行。折叠只是换了候选的求值位置，所以**位置变了就必须重新
// 施加门控**：
//
//	A 的**最终**候选表里，除**最后一条**（默认位，与 armGateCandidates 同一豁免）
//	外的候选，其产出方必须存在于 A 的**某条臂**内；否则在 `arm`（= A 的**包含 B
//	的那条臂** —— 这正是链形态里 B 的执行条件）内物化**恒等载体**。
//
// 物化点为什么是「A 的包含 B 的那条臂」而不是「B 内部的某条臂」：链形态里该候选
// 的存在条件**就是**「B 的 merge 跑了一次」，而 B 的 merge 只在 A 的这条臂里跑
// 	⇒ 载体的存在条件与链形态逐字相同，这是**值保持**，不是新增形态守卫。
//
// 可证零值的载体不门控（P04：零值由 merge 的「候选缺省 = 类型零」承担，且
// first_non_zero 跳过它们 ⇒ 无条件存在不影响取值）。

// hostArmBlockContaining 返回 outer 宿主**哪条臂**包含 inner 宿主（控制包含判据
// 的反向查询，物化点查询；不在任何臂内 ⇒ nil）。
func hostArmBlockContaining(outer, inner junctionHostStmt) *Block {
	if outer == nil || inner == nil {
		return nil
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
			return arm
		}
	}
	return nil
}

// hostArmNodeIDs 是 outer 宿主**全部**臂的子树节点并集：候选产出方落在这里面 ⇒
// 它的存在性已经由臂门控决定（臂不执行 ⇒ 节点不产出），搬运后不需要再套载体。
func hostArmNodeIDs(outer junctionHostStmt) map[string]bool {
	out := map[string]bool{}
	if outer == nil {
		return out
	}
	for _, arm := range outer.junctionArmBlocks() {
		for id := range blockContainedNodeIDs(arm) {
			out[id] = true
		}
	}
	return out
}

// armRelayNeed 是一个**需要**在臂内物化恒等载体的候选（judge 阶段只收集，
// 不建节点 —— 判定失败时整次折叠放弃，不留半截产物）。
type armRelayNeed struct {
	idx int // 在最终候选表里的下标
	src Ref
	typ *Type
}

// planSplicedArmRegate 按**最终位置**判定哪些候选需要在 `arm` 内物化载体。
// ok=false ⇒ 证据不足以物化（无类型权威 / 记录字段组搬运未实现）⇒ 调用方
// **放弃折叠**（保链 = 值保持；折叠只是规范形，正确性优先）。
func planSplicedArmRegate(nodes map[string]Node, arm *Block, inOuterArms map[string]bool, refs []Ref) ([]armRelayNeed, bool) {
	var needs []armRelayNeed
	for i, r := range refs {
		if i == len(refs)-1 {
			continue // 默认位豁免（与 armGateCandidates 同一条判据：位置决定门控）
		}
		if inOuterArms[r.Producer] {
			continue // 产出方在外层宿主的某条臂内 ⇒ 存在性已被臂门控
		}
		if isZeroCarrierNode(nodes[r.Producer]) {
			continue // P04 零值豁免
		}
		// 记录字段组的候选（成员路径 + 该口是记录）：载体必须是**整值记录**
		// （跨字段组同位，R-13）—— 图级折叠期没有记录载体物化器 ⇒ 拒绝折叠。
		// 保守一侧：链形态保留 = 值不变（该形状的取值缺陷是同一族，另行登记）。
		if base, _, dotted := splitMemberPort(r.Output); dotted {
			if bt := portTypeOfNode(nodes[r.Producer], base); bt != nil && len(bt.Props) > 0 {
				return nil, false
			}
		}
		// 扁平成员口拼写（无点）：候选口是**记录发射器的成员口** ⇒ 同上仍属记录
		// 字段组（判据 = `recordEmitterMemberPort`，与 sink 侧同尺）。2026-09-28 起
		// 「逐字段标量恒等载体」拼写已退役（反编译读不回，正本 =
		// `platforms/cozebiz/decompile/record_unit.go` 判据①）⇒ 本 pass 没有记录
		// 载体物化器，只能拒折（保守一侧：链形态保留 = 值不变）。
		if cr, ok := nodes[r.Producer].(*CodeRun); ok && recordEmitterMemberPort(cr, r.Output) {
			return nil, false
		}
		typ := portTypeOfNode(nodes[r.Producer], r.Output)
		if typ == nil {
			return nil, false // 无类型权威 ⇒ 不虚构类型（保守：放弃折叠）
		}
		if arm == nil {
			return nil, false
		}
		needs = append(needs, armRelayNeed{idx: i, src: r, typ: typ})
	}
	return needs, true
}

// armRelayNode 在 arm 内物化恒等载体（形状与 Builder 的 identityRelayInArm 逐字
// 同：规范恒等 shim + Identity 标注 + 单 `result` 口 + 单 `arg1` 输入），返回候选
// 新引用。载体**前插**到臂首：输入是臂外值，臂一进就可读（与 Builder 同序）。
func armRelayNode(alloc *graphIDAlloc, arm *Block, src Ref, typ *Type) Ref {
	id := alloc.alloc()
	node := &CodeRun{
		ID:        id,
		Code:      canonicalIdentityRelayCode(),
		Language:  "python",
		Inputs:    []Source{{Path: []string{"arg1"}, BlockID: src.Producer, Name: src.Output}},
		Types:     map[string]*Type{"arg1": typ},
		OutTypes:  map[string]*Type{"result": typ},
		OutOrder:  []string{"result"},
		Synthetic: true,
		Identity:  true,
	}
	arm.Stmts = append([]Stmt{&NodeStmt{Node: node, Value: &varRef{nodeID: id, output: "result", kind: "code"}}}, arm.Stmts...)
	return Ref{Producer: id, Output: "result"}
}

// existingArmIdentityRelay 在 arm 的**顶层语句**里找一条已承载同一个值的恒等载体
// （单输入 = src），返回它以便**复用**（不新建节点）。
//
// 为什么必须复用：门控是候选身份的一部分。同一个值被门控到同一条臂之后，候选表里
// 只该出现**一份**载体 —— 这与 dedupeRefs 是同一条规范形要求（先门控，再按「值的
// 来源身份」去重）；spliceRefsAt 只在**裸引用**层面去重，看不见「裸引用 vs 已门控
// 载体」这一层等价。一手反例（R-50）：深度 ≥ 2 的链式聚合分两次 splice 进同一个
// 外层组，两次都搬进内层聚合的**继承前绑定候选**（同一个 `start` 口）⇒ 修前每条
// 各铸一份同形 relay（2 份载体 = 4 候选），而 raising 侧对这种形态读成单字段记录
// 单元 ⇒ `n` 那一臂整条消失（值级后果：`n == 11` 返回 `0`）。
//
// 只查**顶层**语句：嵌套臂里的载体门控在更深的位置（存在条件更窄），与本次要物化
// 的载体值域不同，复用会丢门控。
func existingArmIdentityRelay(arm *Block, src Ref) (Ref, bool) {
	if arm == nil {
		return Ref{}, false
	}
	for _, s := range arm.Stmts {
		ns, ok := s.(*NodeStmt)
		if !ok {
			continue
		}
		in, ok := identityRelaySource(ns.Node)
		if !ok {
			continue
		}
		if in.BlockID == src.Producer && in.Name == src.Output {
			return Ref{Producer: ns.Node.NodeID(), Output: "result"}, true
		}
	}
	return Ref{}, false
}

// materializeArmCarrier 在 arm 内**物化或复用**承载 src 的恒等载体：
// 同臂同源已有载体 ⇒ 复用（不新建节点）。
func materializeArmCarrier(alloc *graphIDAlloc, arm *Block, src Ref, typ *Type) Ref {
	if ex, ok := existingArmIdentityRelay(arm, src); ok {
		return ex
	}
	return armRelayNode(alloc, arm, src, typ)
}

// canonicalizeSplicedCandidates 是「搬运 + 门控」的收口：把 needs 指到的候选换成
// 载体（复用或新建），再按**值的来源身份**去重。
//
// 为什么去重必须在门控**之后**：候选的身份 = (值的来源, 门控点)。`spliceRefsAt`
// 只在**裸引用**层面去重，看不见「裸引用 == 已门控载体」这一层等价 —— 同一个
// `start` 口既可能以裸引用搬进来、也可能以本臂已物化的载体已在表里。两条都留着
// = 同一语义两种形状（规范形必须是函数），且实测会被 raising 读成单字段记录单元
// ⇒ 丢一臂值（R-50）。一手反例与证据 = J §46。
func canonicalizeSplicedCandidates(alloc *graphIDAlloc, arm *Block, refs []Ref, needs []armRelayNeed) []Ref {
	out := append([]Ref(nil), refs...)
	for _, n := range needs {
		if n.idx < 0 || n.idx >= len(out) {
			continue
		}
		out[n.idx] = materializeArmCarrier(alloc, arm, n.src, n.typ)
	}
	return dedupeRefs(out)
}

// graphIDAlloc 分配折叠期新建节点的 id：`100000+序号`（与 Builder 的 nextID 同族），
// 起点 = 本图现有最大数字 id + 1 ⇒ 本图内唯一（平台 wire 对 id 的唯一要求）。
// 产物 id 不是契约（AGENTS.md「产物 id 不是契约」），故与 Builder 的水位无关。
type graphIDAlloc struct{ next int }

func newGraphIDAlloc(nodes map[string]Node) *graphIDAlloc {
	max := 100000
	for id := range nodes {
		if n, err := strconv.Atoi(id); err == nil && n > max && n < 900000 {
			max = n
		}
	}
	return &graphIDAlloc{next: max + 1}
}

func (a *graphIDAlloc) alloc() string {
	id := strconv.Itoa(a.next)
	a.next++
	return id
}
