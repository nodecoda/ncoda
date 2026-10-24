package ir

import "github.com/nodecoda/ncoda/pkg/capability"

// loop_return_materialize.go — M17（循环体内 return）的 coze-biz 物化
// （2026-09-13，E15 主体；设计见
// docs/ideas-todo/2026-09-13-cozebiz-loop-return-lowering-design.md）。
//
// 语义（D4，2026-09-13 用户拍板）：`return` 结束**当前函数/流程**，循环不构成
// 边界。coze-biz 上没有「从循环体返回整个流程」的原生节点，但有两条真通道：
//
//   - 循环状态变量（`variableParameters` + 体内状态写节点）：平台实现
//     （coze-studio `variable_assign_in_loop.go`）对每个 (left,right) 对**执行
//     即写** parent intermediate store；
//   - `break`：置 $break，循环在**本轮迭代结束后**检查并跳出。
//
// 因此物化 = **循环状态对**（`returned: bool` = 已逃逸标志、`return_value: T`
// = 逃逸值）+ body 内每个 return 站点写状态并 break + **循环后门控**
// （`if (returned == true) { return <逃逸值>; }`）。门控是一个**普通 IfStmt**，
// 其真臂是一个**普通 ReturnStmt** ⇒ 值侧与端口侧全部落回既有机制（flow-exit
// phi / 分支端口义务 / P5 流程终止）：本文件不需要任何新的渲染器义务、新的节点
// 类型或新的锁账点。
//
// **为什么真臂必须有节点**（S2-gate 阻断项 1 + 平台源码一手复核）：平台 merge
// 对候选的采纳判据是「候选的**来源节点是否执行过**」
// （coze-studio `nodes/stream.go`：`skipped = !nodeExecuted.NodeExecuted(...)`
// ⇒ FieldSkipped ⇒ `variableaggregator/variable_aggregator.go` 跳过），而
// **已执行**来源的空串/零值是**合法取值**（同文件 `value != nil` 即选中）。
// 循环容器在「未命中」路径上也执行过 ⇒ 把裸的循环回声口 `return_value` 直接当
// flow-exit 候选，会在未命中时被**恒选中**（取到状态初值）⇒ 流程返回零值而不是
// 循环后的尾值，且 L1/L2 全绿（静默错语义）。故真臂用**单候选汇聚单元**（原生
// variable_merge）承载逃逸值：它只在该臂执行 ⇒ 未命中时其来源被跳过（与 dify 的
// `makeLoopReturnAssigner`、runtime 目标 `shortcutLoopReturns` 的「路径上的
// 物化节点」同一规则）。
//
// **目标门控**：本文件只在 coze-biz 目标实例化（`g.caps.Target`，与 LLM 信封
// 放置同一处目标分派）。dify / runtime 各自已有实现（dify 的
// `__returning/__return_value` + 循环后 if-else；runtime 的
// `shortcutLoopReturns`），不动它们；ncoda 语言语义与 IR 节点类型均不变——这
// 只是一次**目标内**的语句重写。
//
// **不做 batch**（`parallel for`）：batch 的分支是并发调度，「提前返回」的语义与
// 实现另案（docs/PLATFORM-CAPABILITY.md 的能力行 + lowering fail loud），本文件
// 跳过 IsParallel 容器（跳过 = 该形状继续走 fail loud，不是静默降级）。

// loopReturnStateFlag / loopReturnStateValue：逃逸标志与逃逸值的循环状态名。
// **名字即端口名**：renderLoop 的回声分支判据是 `s.StateTypes[<端口名>]`，两个
// 名字必须同名，否则回声口落统一 list-wrap 路径 ⇒ 端口声明变成 `list<bool>`，
// 而门控拿 `list<bool>` 和 `true` 比较（L1/L2 都会放行 = 静默错产物）。
const (
	loopReturnStateFlag  = "returned"
	loopReturnStateValue = "return_value"
)

// loopReturnNeedsStateGate reports whether this target needs the M17
// statement rewrite. The shape is declared by the adapter
// (capability.Table.LoopReturnShape); the legacy name bridge keeps pre-
// declaration tables on their old shape.
func loopReturnNeedsStateGate(caps *capability.Table) bool {
	if caps == nil {
		return false
	}
	if caps.LoopReturnShape == capability.LoopReturnStateGate {
		return true
	}
	return legacyLoopReturnStateGate(caps.Target)
}

// materializeLoopReturns 在 IR 建成后、flow-exit phi 物化前，把目标平台无法直接
// 表达的 M17 形状改写成「循环状态 + break + 循环后门控」。规则无形态守卫：容器
// 种类（for / while）、return 站点所在的臂（裸体 / if 臂 / 任意嵌套）、站点数量、
// 值类型一律同一处理；唯一例外是 batch（另案，见文件头）。
func (g *Builder) materializeLoopReturns() {
	if !loopReturnNeedsStateGate(g.caps) {
		return
	}
	g.materializeLoopReturnsIn(g.root)
}

// materializeLoopReturnsIn 递归处理一个块（含嵌套控制结构的臂体），并在每个含
// return 的循环容器**之后**插入门控（门控与循环同块 —— 循环位于宿主臂内时，
// 门控也落在该臂内，规则不依赖「循环在根层」）。
func (g *Builder) materializeLoopReturnsIn(b *Block) {
	if b == nil {
		return
	}
	out := make([]Stmt, 0, len(b.Stmts))
	for _, st := range b.Stmts {
		g.materializeLoopReturnsInStmt(st)
		if loop, ok := st.(*LoopStmt); ok && !loop.IsParallel {
			pre, gate := g.materializeLoopReturn(loop)
			if gate != nil {
				out = append(out, pre...)
				out = append(out, st)
				out = append(out, gate...)
				continue
			}
		}
		out = append(out, st)
	}
	b.Stmts = out
}

// materializeLoopReturnsInStmt 递归进入一个语句的所有子块（容器内的循环体也在
// 其内；嵌套容器平台不可表达，但规则不依赖这一点）。
func (g *Builder) materializeLoopReturnsInStmt(st Stmt) {
	switch s := st.(type) {
	case *IfStmt:
		g.materializeLoopReturnsIn(s.Then)
		g.materializeLoopReturnsIn(s.Else)
	case *AttemptStmt:
		g.materializeLoopReturnsIn(s.Success)
		g.materializeLoopReturnsIn(s.Failure)
	case *ParallelStmt:
		for _, br := range s.Branches {
			g.materializeLoopReturnsIn(br)
		}
	case *QaStmt:
		for _, arm := range s.Branches {
			g.materializeLoopReturnsIn(arm.Body)
		}
	case *SwitchStmt:
		for _, arm := range s.Branches {
			g.materializeLoopReturnsIn(arm.Body)
		}
	case *InlineStmt:
		g.materializeLoopReturnsIn(s.Body)
	case *LoopStmt:
		g.materializeLoopReturnsIn(s.Body)
	}
}

// materializeLoopReturn 把一个含 return 的循环容器改写成「状态对 + 体内写状态并
// break + 循环后门控」。返回值 = （循环**之前**要插入的语句（零值载体），循环
// **之后**要插入的语句（门控））；无 return 的循环返回 (nil, nil)。
func (g *Builder) materializeLoopReturn(loop *LoopStmt) ([]Stmt, []Stmt) {
	hasValue, hasAny := loopReturnSiteKinds(loop.Body)
	if !hasAny {
		return nil, nil
	}
	if loop.StateTypes == nil {
		loop.StateTypes = map[string]*Type{}
	}
	if loop.StateValues == nil {
		loop.StateValues = map[string]Source{}
	}
	// 逃逸标志恒声明（void return 也要停流程），逃逸值按需声明。
	loop.StateTypes[loopReturnStateFlag] = T("boolean")
	loop.StateValues[loopReturnStateFlag] = Source{Val: false}
	var pre []Stmt
	if hasValue {
		// 类型权威 = IR 已装配的 OutTypes["return_value"]（build_stmt.go 由
		// return 站点 producer 的口类型装配；缺失才回退 string）。
		rvType := loop.OutTypes[loopReturnStateValue]
		if rvType == nil {
			rvType = T("string")
		}
		loop.StateTypes[loopReturnStateValue] = rvType
		// 状态初值 = 该类型的零值，物化成循环**上游**的纯常量节点（与既有
		// loop-state「string 初值走 producer ref」同一条通道；P2 要求载体在
		// 消费点上游）。初值在语义上不可观测（标志为真 ⇔ 某个站点写过值），
		// 但平台导入要校验声明类型与初值形状一致 ⇒ 必须按类型给零值。
		if zero, ok := g.loopReturnZeroShim(rvType); ok {
			pre = append(pre, &NodeStmt{Node: g.find(zero.nodeID), Value: &zero})
			loop.StateValues[loopReturnStateValue] = Source{BlockID: zero.nodeID, Name: zero.output}
		}
	}
	// 状态回声口：门控在**容器外**读 `returned`（`{path: returned, ref_node: loop}`），
	// 平台契约要求被引用的端口已在容器的 `node_outputs` 里声明（P2 / 端口已声明）。
	// 值侧由 build_stmt.go 的既有回声装配（`loop.ReturnValue != nil` 分支）声明了
	// 一对口；**void 侧没有 `ReturnValue`** ⇒ 该分支不跑 ⇒ 门控读一个未声明端口
	//（抬回侧 `RS-NORM-004`；平台侧该引用无端口可解析）。两侧必须同形，故在此
	// 按「本次物化声明了哪些状态」补齐回声口（幂等：已声明则不动）。
	g.ensureLoopStateEchoPort(loop, loopReturnStateFlag)
	if hasValue {
		g.ensureLoopStateEchoPort(loop, loopReturnStateValue)
	}

	// body 内站点 → 写状态 + break。
	g.rewriteLoopReturnSites(loop.Body, loop.ID, hasValue)

	// 门控：if (loop.returned == true) { <真臂> }；真臂 = （可选值载体）+ 流程
	// 终止 return（共享 end）。值载体只在本臂执行 ⇒ 未命中路径上被平台跳过。
	condID := g.nextID()
	selector := &Selector{ID: condID, Logic: 0, Conditions: []Condition{{
		Left:     Operand{Ref: &Ref{Producer: loop.ID, Output: loopReturnStateFlag}},
		Operator: 1, // equals
		Right:    Operand{Val: true},
	}}}
	g.nodes = append(g.nodes, selector)
	g.remember(selector)

	thenStmts := make([]Stmt, 0, 2)
	var endRefs []Ref
	var retValue *varRef
	if hasValue {
		aggID := g.nextID()
		agg := &Aggregate{ID: aggID, Refs: []Ref{{Producer: loop.ID, Output: loopReturnStateValue}}}
		g.nameUnnamedConvergenceUnit(agg)
		g.nodes = append(g.nodes, agg)
		g.remember(agg)
		port := agg.SingleGroupName()
		arm := varRef{nodeID: aggID, output: port, kind: "aggregate"}
		thenStmts = append(thenStmts, &NodeStmt{Node: agg, Value: &arm})
		endRefs = []Ref{{Producer: aggID, Output: port}}
		retValue = &arm
	}
	endID := g.ensureEnd(endRefs, nil)
	end, _ := g.find(endID).(*End)
	if end == nil {
		return nil, nil
	}
	thenStmts = append(thenStmts, &ReturnStmt{
		ID: endID, Value: retValue, End: end, SharedEnd: true, FlowEnd: true,
	})
	gate := []Stmt{&IfStmt{
		ID:   condID,
		Cond: selector,
		Then: &Block{Stmts: thenStmts},
		// 真臂流程终止、假臂为空 ⇒ 无汇聚候选（宿主 merge-less 路径）。
		Merge: nil,
	}}
	return pre, gate
}

// ensureLoopStateEchoPort 保证循环容器为该状态名声明回声口（OutTypes +
// 自绑定 OutputSources，与 build_stmt.go 的既有回声装配逐字同形）。已声明 ⇒ 不动。
func (g *Builder) ensureLoopStateEchoPort(loop *LoopStmt, name string) {
	if loop == nil || loop.StateTypes[name] == nil {
		return
	}
	if loop.OutTypes == nil {
		loop.OutTypes = map[string]*Type{}
	}
	if _, ok := loop.OutTypes[name]; !ok {
		loop.OutTypes[name] = loop.StateTypes[name]
	}
	for _, src := range loop.OutputSources {
		if len(src.Path) == 1 && src.Path[0] == name && src.BlockID == loop.ID {
			return
		}
	}
	loop.OutputSources = append(loop.OutputSources, Source{Path: []string{name}, BlockID: loop.ID, Name: name})
}

// loopReturnSiteKinds 扫描循环体：返回（是否有带值站点，是否有任意站点）。
// 带值站点 = M17 的 LoopReturnNode；void 站点 = 循环体内**流程终止**的
// `return;`（End 非 nil、无值）。QA/attempt 臂内的 return 是**分支结果值**
// （subscope，End 为 nil）⇒ 不是流程终止，不在此列；inline 子函数体内的
// return 同理（P13 边界屏障）。
func loopReturnSiteKinds(body *Block) (hasValue, hasAny bool) {
	walkLoopReturnSites(body, func(ns *NodeStmt, rs *ReturnStmt) {
		hasAny = true
		if ns != nil {
			hasValue = true
		}
	})
	return hasValue, hasAny
}

// walkLoopReturnSites 递归收集一个循环体内的 return 站点（不进入嵌套容器：
// 嵌套容器的站点属于它自己；平台不可嵌套，故这是完备的）。
func walkLoopReturnSites(b *Block, visit func(ns *NodeStmt, rs *ReturnStmt)) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		switch s := st.(type) {
		case *NodeStmt:
			if _, ok := s.Node.(*LoopReturnNode); ok {
				visit(s, nil)
			}
		case *ReturnStmt:
			if isFlowTerminatingReturn(s) {
				visit(nil, s)
			}
		case *IfStmt:
			walkLoopReturnSites(s.Then, visit)
			walkLoopReturnSites(s.Else, visit)
		case *AttemptStmt:
			walkLoopReturnSites(s.Success, visit)
			walkLoopReturnSites(s.Failure, visit)
		case *ParallelStmt:
			for _, br := range s.Branches {
				walkLoopReturnSites(br, visit)
			}
		case *QaStmt:
			for _, arm := range s.Branches {
				walkLoopReturnSites(arm.Body, visit)
			}
		case *SwitchStmt:
			for _, arm := range s.Branches {
				walkLoopReturnSites(arm.Body, visit)
			}
		case *InlineStmt:
			walkLoopReturnSites(s.Body, visit)
		}
	}
}

// isFlowTerminatingReturn 报告该 ReturnStmt 是否终止整个流程（带共享 End、
// 无值、非命名结果）。主流程内任何 return 都终止流程（P5）；subscope return
// （QA/attempt 臂、inline 子函数）不带 End。
func isFlowTerminatingReturn(s *ReturnStmt) bool {
	if s == nil || s.End == nil || s.Value != nil {
		return false
	}
	return !s.End.NamedReturn && s.End.Answer == nil
}

// rewriteLoopReturnSites 把循环体内的 return 站点就地改写成
// 「写状态 + break」。带值站点还要写逃逸值。
func (g *Builder) rewriteLoopReturnSites(b *Block, loopID string, hasValue bool) {
	if b == nil {
		return
	}
	out := make([]Stmt, 0, len(b.Stmts))
	for _, st := range b.Stmts {
		switch s := st.(type) {
		case *NodeStmt:
			if lr, ok := s.Node.(*LoopReturnNode); ok {
				// 右值 Source 的消费路径重写为状态名：渲染用 BlockID/Name 取
				// 生产者口，raising 用 Path[0] 把左值（状态名）与右值配对
				// （convertLoopStateAssigner 按 path 首段匹配 InputSources）。
				v := lr.Value
				v.Path = []string{loopReturnStateValue}
				out = append(out, g.loopReturnSiteStmts(loopID, hasValue, v)...)
				continue
			}
		case *ReturnStmt:
			if isFlowTerminatingReturn(s) {
				out = append(out, g.loopReturnSiteStmts(loopID, false, Source{})...)
				continue
			}
		case *IfStmt:
			g.rewriteLoopReturnSites(s.Then, loopID, hasValue)
			g.rewriteLoopReturnSites(s.Else, loopID, hasValue)
		case *AttemptStmt:
			g.rewriteLoopReturnSites(s.Success, loopID, hasValue)
			g.rewriteLoopReturnSites(s.Failure, loopID, hasValue)
		case *ParallelStmt:
			for _, br := range s.Branches {
				g.rewriteLoopReturnSites(br, loopID, hasValue)
			}
		case *QaStmt:
			for _, arm := range s.Branches {
				g.rewriteLoopReturnSites(arm.Body, loopID, hasValue)
			}
		case *SwitchStmt:
			for _, arm := range s.Branches {
				g.rewriteLoopReturnSites(arm.Body, loopID, hasValue)
			}
		case *InlineStmt:
			g.rewriteLoopReturnSites(s.Body, loopID, hasValue)
		}
		out = append(out, st)
	}
	b.Stmts = out
}

// loopReturnSiteStmts 生成一个 return 站点的物理语句：**一条** assigner
// 节点（`returned = true` [+ `return_value = <值>`] 两对写在同一个节点里，
// 语料 S89 实证 set_variable 单节点可写 3 对）后紧跟 `break`。顺序即语义：
// 状态写必须发生在 break 之前（平台在迭代边界检查 $break 并跳出）。
//
// 为什么是**一个**节点而不是两个：写作 = 语义 = 「逃逸」这一个事件的一次
// 记录（标志与值在任何观察点都成对出现）。拆成两个节点在平台上等价，但
// raising 就要发明「同一站点的连续 set_variable」这种位置耦合判据（设计
// §5/§6 的二选一），而单节点形态的还原判据只有一条：该节点同时写了状态对
// ⇒ 还原为 `return <right>`（设计 §6 判据 1+2）。
func (g *Builder) loopReturnSiteStmts(loopID string, withValue bool, value Source) []Stmt {
	pairs := []AssignPair{}
	if withValue {
		pairs = append(pairs, AssignPair{VarName: loopReturnStateValue, Value: value})
	}
	assign := &Assign{
		ID:         g.nextID(),
		VarName:    loopReturnStateFlag,
		Value:      Source{Path: []string{loopReturnStateFlag}, Val: true},
		LoopState:  true,
		LoopID:     loopID,
		ExtraPairs: pairs,
	}
	g.nodes = append(g.nodes, assign)
	g.remember(assign)
	brk := &BreakNode{ID: g.nextID()}
	g.nodes = append(g.nodes, brk)
	g.remember(brk)
	return []Stmt{&NodeStmt{Node: assign}, &BreakStmt{Node: brk}}
}

// loopReturnZeroShim 创建该类型的零值常量节点（复用既有的 shim 零值机制：
// `wrapShim(nil, zeroValuePyast(t))`，与分支默认零值 shim 同构、与反编译器
// 的零值 record 判定同构）。
func (g *Builder) loopReturnZeroShim(t *Type) (varRef, bool) {
	if t == nil {
		return varRef{}, false
	}
	id := g.nextID()
	node := &CodeRun{
		ID:        id,
		Language:  "python",
		Synthetic: true,
		Types:     map[string]*Type{},
		OutTypes:  map[string]*Type{"result": t},
		OutOrder:  []string{"result"},
		Code:      wrapShim(nil, zeroValuePyast(t)),
		ZeroOf:    t,
	}
	g.nodes = append(g.nodes, node)
	g.remember(node)
	return varRef{nodeID: id, output: "result", kind: "code", typ: t}, true
}
