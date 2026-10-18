package passes

// parallel_iteration_local.go — `parallel for` 迭代体作用域语义
// （D-L1(a) / D-L2(a) / D-L3(a)，2026-09-14 用户拍板；语言模型见
// docs/ideas-todo/2026-09-14-parallel-for-language-semantics.md §1）。
//
//	(S1) 迭代独立：兄弟迭代之间不得有数据依赖（并发 = 数据竞争 = 编译错误）；
//	(S2) 循环外名字的**读** = 循环入口的**快照**（只读视图，无依赖）；
//	(S3) 循环外变量的**写** = **迭代局部、不逃逸**：本迭代后续语句可见，
//	     兄弟迭代看不见，循环之外也看不见 ⇒ 死写（无人读）无害、合法；
//	(S4) `break`（终止整个循环）/ **带值** `return <v>`（终止流程并选值）是
//	     **全局不确定效果**（已执行/未执行的迭代集合、哪个迭代的值胜出都随调度
//	     变化）⇒ 语言错；`yield` / `continue` 是迭代局部 ⇒ 合法；
//	     **void `return;`**（终止流程、不选值）⇒ 合法（不选值 ⇒ 结果确定，平台
//	     通道 = 容器内节点直连根 `end`；真实导出实证见 §9.3）。
//
// 本文件实现两条判据，**语句形态与表达式形态同一把尺子**（`ForStmt` +
// `IsParallel()` 与 `ParallelForExpr` 走同一个入口，禁形态分叉）：
//
//	E1036 PARALLEL_ACCESS_HAZARD —— 循环**之后**读到「被迭代写过」的名字：
//	      该读拿到的是循环前的值，而作者的写意图显然是迭代结果 ⇒ fail loud
//	      并给出改写法（`yield` 收集 / 顺序 `for`）。死写（循环外无人读）
//	      不报错（S3）。
//	E1037 PARALLEL_EXTERNAL_EXIT —— 迭代体内的 `break`（目标 = 该并发循环
//	      自身）或**带值** `return <v>`（任意嵌套深度；`return template …` 例外：
//	      它的值不是迭代产物而是流程答复端点 ⇒ 归 E1057 位置判据）。二者都是**选值 / 终止
//	      方式不确定**的控制转移；**void `return;` 合法**（不选值 ⇒ 结果确定，
//	      平台通道 = 容器内节点直连根 `end`，真实导出实证见 §9.3）。QA action /
//	      attempt 臂里的 `return` 是**分支结果值**（P5 subscope return），不属此列）。
//
// 判据实现（「迭代写标记」= trap）：
//
//	写集合 W = 迭代体**子树闭包**里对「循环外已绑定名字」的赋值
//	（ast.Walk 子树闭包，同 P24 —— 禁手写语句容器类型枚举；循环内任意
//	嵌套深度、任意宿主臂里的写一律算数）。
//	循环语句**结束之后**（同一语句序列内、或从外层语句继续向下）对该名字的
//	**读**命中 ⇒ E1036，每个符号只报一次（首个读点定位置）。
//	循环之外对该名字的**普通赋值**清除标记 —— 作者显式重设后读到的不是
//	迭代写的残留（真实数据流），与 S3 等价表述一致。
//
// 为什么不是「体内写共享变量即报错」（旧规则，用户已判「太严」）：那条规则
// 把「按迭代收集」这一最自然的写法（体内算、`yield` 出）与死写一起否掉；
// S3 的迭代局部定义既保住作者直觉，又由 S2 快照读保证结构上不存在跨迭代
// 依赖 —— 一条规则、无形态守卫、对嵌套/容器/任意深度封闭。

import (
	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
)

// iterationScopeKind 区分一张作用域帧的**语义**（不是语句容器枚举）：
// 循环（`break` 目标判定）与分支值臂（`return` 语义判定）。
type iterationScopeKind int

const (
	iterationScopeLoop iterationScopeKind = iota
	iterationScopeValueArm
)

// iterationScopeFrame 是一次进入循环 / 分支值臂的记录。
type iterationScopeFrame struct {
	kind        iterationScopeKind
	parallelFor bool // 该循环是并发迭代（`parallel for` / `parallel for` 表达式）
}

// iterationScopeScanner 实现上表两条判据。它按**语句序列**推进（`*ast.Block`
// 是唯一的语句持有者），并在循环构造处推送/弹出作用域帧。
type iterationScopeScanner struct {
	ctx    *Context
	snap   *binding.Snapshot
	mustID func(ast.Node) binding.AstNodeID

	// trapped = 「迭代写已经发生」的名字（后续读到的是循环前的值）。
	trapped map[binding.SymbolID]bool
	// pending = 语句帧栈：一条语句内部发现的迭代写先挂在帧上，语句结束后
	// 才提交（循环体内的读不得被自己的写标记命中，同一条语句内同理）。
	pending []map[binding.SymbolID]bool
	// scopes = 打开的作用域帧栈（循环 / 分支值臂）。
	scopes []iterationScopeFrame
	// reported = 已报过的符号（同一符号只报一次，避免多处读点刷屏）。
	reported map[binding.SymbolID]bool
}

// analyzeParallelForIterationScope 是 ParallelAnalysisPass 的第二遍遍历：
// 迭代体作用域语义（S3/S4）。放在独立遍历里而不是 parallelTraverser 里，
// 因为本判据需要**语句序列**位置信息（trap 的产生点与消费点之间的先后），
// 而 generic visitor 没有进入/退出回调。
func analyzeParallelForIterationScope(program *ast.Program, ctx *Context, snap *binding.Snapshot, mustID func(ast.Node) binding.AstNodeID) {
	if snap == nil {
		return
	}
	s := &iterationScopeScanner{
		ctx:      ctx,
		snap:     snap,
		mustID:   mustID,
		trapped:  map[binding.SymbolID]bool{},
		reported: map[binding.SymbolID]bool{},
	}
	ast.Walk(s, program)
}

// Visit 分派：只处理「语句序列」「循环」「分支值臂」「赋值」「标识符读」五类
// 需要语义区别的节点；其余节点继续 generic 递归（语句容器家族的开放性是靠
// ast.Walk 封闭的，不靠类型枚举）。
func (s *iterationScopeScanner) Visit(n ast.Node) ast.Visitor {
	switch node := n.(type) {
	case *ast.Block:
		// Block 是唯一的语句持有者：按顺序推进语句序列（trap 的
		// 产生/消费次序 = 程序的语句次序）。
		s.scanStmts(node.Stmts())
		return nil
	case *ast.ForStmt:
		parallelFor := node.IsParallel()
		s.pushScope(parallelFor)
		s.walkLoopHeader(node.LoopSources(), node.LoopTypes())
		ast.Walk(s, node.Body())
		s.popScope()
		if parallelFor {
			s.markIterationWrites(node.Body())
		}
		return nil
	case *ast.WhileStmt:
		s.pushScope(false)
		ast.Walk(s, node.Condition())
		ast.Walk(s, node.Body())
		s.popScope()
		return nil
	case *ast.ForExpr:
		s.pushScope(false)
		s.walkLoopHeader(node.LoopSources(), node.LoopTypes())
		ast.Walk(s, node.Body())
		s.popScope()
		return nil
	case *ast.ParallelForExpr:
		s.pushScope(true)
		s.walkLoopHeader(node.LoopSources(), node.LoopTypes())
		ast.Walk(s, node.Body())
		s.popScope()
		s.markIterationWrites(node.Body())
		return nil
	case *ast.AttemptStmt:
		// attempt success / failure 臂里的 return 是**分支结果值**
		// （P5 subscope return）——臂屏障内的 return 不构成流程终止。
		ast.Walk(s, node.Operation())
		ast.Walk(s, node.ResultBinding())
		ast.Walk(s, node.ErrorBinding())
		s.walkValueArm(node.SuccessBody())
		s.walkValueArm(node.FailureBody())
		return nil
	case *ast.RequestInputStmt:
		// ask 的 action / timeout 臂同理（分支结果值）。
		s.walkAskArms(node)
		return nil
	case *ast.AssignStmt:
		// 赋值不是「读」：目标根是写（**清除**迭代写标记），目标的索引
		// 位置与整个 RHS 仍是读。
		if root := assignmentRoot(node.Target()); root != nil {
			if symbolID, ok := s.symbolOf(root); ok {
				delete(s.trapped, symbolID)
			}
			s.walkAssignmentIndices(node.Target())
		} else {
			ast.Walk(s, node.Target())
		}
		ast.Walk(s, node.Value())
		return nil
	case *ast.ReturnStmt:
		// 值位置仍是读（逃逸读判据必须看见 `return v;` 里的 v），
		// 语义判定随后。
		ast.Walk(s, node.Value())
		s.checkReturn(node)
		return nil
	case *ast.BreakStmt:
		s.checkBreak(node)
		return nil
	case *ast.ContinueStmt:
		// continue = 结束本迭代剩余部分（迭代局部）⇒ 合法，不入判据。
		return nil
	case *ast.IdentExpr:
		s.checkRead(node)
	}
	return s
}

// ── 语句序列推进（trap 的产生点 / 消费点次序）─────────────────────────

func (s *iterationScopeScanner) scanStmts(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		s.pending = append(s.pending, map[binding.SymbolID]bool{})
		ast.Walk(s, stmt)
		frame := s.pending[len(s.pending)-1]
		s.pending = s.pending[:len(s.pending)-1]
		s.discharge(frame)
	}
}

// discharge 把一条语句内部产生的迭代写标记提交给外层：仍有语句帧时提交给
// 外层帧（同一外层语句内的后续语句），否则提交给活动标记表（同一语句序列内
// 的后续语句）。外层语句结束时继续向上提交，直到函数体顶层 —— 于是
// `if (c) { parallel for (…) { v = x; } } return v;` 的读同样被命中。
func (s *iterationScopeScanner) discharge(frame map[binding.SymbolID]bool) {
	if len(frame) == 0 {
		return
	}
	if len(s.pending) > 0 {
		target := s.pending[len(s.pending)-1]
		for symbolID := range frame {
			target[symbolID] = true
		}
		return
	}
	s.commit(frame)
}

// commit 把标记并入活动标记表。
func (s *iterationScopeScanner) commit(frame map[binding.SymbolID]bool) {
	for symbolID := range frame {
		s.trapped[symbolID] = true
	}
}

// markIterationWrites 登记一个并发迭代体的写集合：这些名字的写**不逃逸**
// 循环 ⇒ 循环之后读到它们即为「读到循环前的值」（E1036 判据的输入）。
// 写在体遍历**之后**登记：体内的读（含写后读）不得被自己的标记命中。
func (s *iterationScopeScanner) markIterationWrites(body *ast.Block) {
	writes := s.iterationWrites(body)
	if len(writes) == 0 {
		return
	}
	if len(s.pending) == 0 {
		s.commit(writes)
		return
	}
	s.discharge(writes)
}

// iterationWrites 求迭代体的写集合 W = 「子树闭包内对循环外已绑定名字的赋值」
// （ast.Walk 子树闭包；任意嵌套深度与任意宿主臂同规则）。
func (s *iterationScopeScanner) iterationWrites(body *ast.Block) map[binding.SymbolID]bool {
	bodyID := s.mustID(body)
	scopeID, found := s.snap.ScopeBindings().Get(bodyID)
	if !found {
		panic("passes: parallel-for body has no published scope")
	}
	collector := newBranchAccessCollector(s.snap, s.mustID, scopeID)
	collector.collect(body)
	out := map[binding.SymbolID]bool{}
	for _, write := range collector.writes {
		out[write.SymbolID] = true
	}
	return out
}

// ── 作用域帧 ─────────────────────────────────────────────────────────

func (s *iterationScopeScanner) pushScope(parallelFor bool) {
	s.scopes = append(s.scopes, iterationScopeFrame{kind: iterationScopeLoop, parallelFor: parallelFor})
}

func (s *iterationScopeScanner) popScope() {
	s.scopes = s.scopes[:len(s.scopes)-1]
}

func (s *iterationScopeScanner) walkLoopHeader(sources []ast.Expr, types []ast.TypeRef) {
	for i, src := range sources {
		ast.Walk(s, src)
		if i < len(types) {
			ast.Walk(s, types[i])
		}
	}
}

// walkValueArm 在分支值臂（QA action / attempt success-failure 臂）内遍历：
// 臂内的 return 是分支结果值，须屏蔽「跨并发迭代逃逸」判定。
func (s *iterationScopeScanner) walkValueArm(body *ast.Block) {
	s.scopes = append(s.scopes, iterationScopeFrame{kind: iterationScopeValueArm})
	ast.Walk(s, body)
	s.scopes = s.scopes[:len(s.scopes)-1]
}

// walkAskArms 遍历 ask 的 action 分支与 timeout 臂（同样的分支值臂语义）。
func (s *iterationScopeScanner) walkAskArms(node *ast.RequestInputStmt) {
	ast.Walk(s, node.FormType())
	ast.Walk(s, node.ActionType())
	ast.Walk(s, node.Config())
	ast.Walk(s, node.ResultBinding())
	for _, branch := range node.ActionBranches() {
		if branch == nil {
			continue
		}
		ast.Walk(s, branch.ActionExpr())
		s.walkValueArm(branch.Body())
	}
	if node.TimeoutBody() != nil {
		s.walkValueArm(node.TimeoutBody())
	}
}

// ── 判据 1：E1036 逃逸读 ─────────────────────────────────────────────

// checkRead 判定一处标识符读：读「被迭代写过」的名字 ⇒ 读到的是循环前的值，
// 而作者的写意图显然是迭代结果 ⇒ fail loud（每个符号只报一次）。
func (s *iterationScopeScanner) checkRead(node *ast.IdentExpr) {
	symbolID, ok := s.symbolOf(node)
	if !ok || !s.trapped[symbolID] || s.reported[symbolID] {
		return
	}
	s.reported[symbolID] = true
	name := declarationName(s.snap, symbolID)
	s.ctx.Emit(diag.PARALLEL_ACCESS_HAZARD, diag.Error,
		"Parallel-for iteration writes '"+name+"' only inside the iteration (iteration-local: the write "+
			"never escapes the loop), so this read outside the loop gets the pre-loop value, not the "+
			"per-iteration one; collect per-iteration values with 'yield' (array<T>) and read the loop "+
			"value, or use a sequential 'for' loop when a running last-write-wins value is intended", node.Span())
}

func (s *iterationScopeScanner) symbolOf(node *ast.IdentExpr) (binding.SymbolID, bool) {
	return s.snap.ReferenceSymbols().Get(s.mustID(node))
}

// walkAssignmentIndices 遍历赋值目标里的索引位置（它们是读），镜像
// branchAccessCollector.visitAssignmentIndices：目标根是写、索引是读。
func (s *iterationScopeScanner) walkAssignmentIndices(target ast.Expr) {
	current := target
	for {
		switch t := current.(type) {
		case *ast.FieldAccessExpr:
			current = t.Obj()
		case *ast.IndexExpr:
			ast.Walk(s, t.Index())
			current = t.Obj()
		default:
			return
		}
	}
}

// ── 判据 2：E1037 迭代体内 break / return ────────────────────────────

// checkBreak 判定 break 的目标循环：目标是**该并发循环本身** ⇒ 语言错
// （并发下「已执行/未执行的迭代集合」随调度变化）。目标是体内嵌套的**顺序**
// 循环（含任意深度）⇒ 迭代局部，合法；内层并发循环 ⇒ 同样报错。
func (s *iterationScopeScanner) checkBreak(node *ast.BreakStmt) {
	frame, ok := s.innermostScope()
	if !ok || frame.kind != iterationScopeLoop || !frame.parallelFor {
		return
	}
	s.ctx.Emit(diag.PARALLEL_EXTERNAL_EXIT, diag.Error,
		"break inside a parallel-for iteration would stop the whole loop while sibling iterations are "+
			"already in flight (which iterations have run is scheduler-dependent) — the language does not "+
			"define it; use 'continue' to end this iteration, or a sequential 'for' loop when a global "+
			"early exit is intended", node.Span())
}

// checkReturn 判定 return：迭代体内**带值** `return <v>` 终止整个流程
// （D4：循环不构成边界），而并发下「哪个迭代的值胜出」随调度变化 ⇒ 语言错。
//
// **void `return;` 合法**：它**不选值** ⇒ 可观察结果确定（流程结束），且平台
// **有通道** —— 容器内节点直连根 `end` 的边（`end` 自身仍在根画布；平台的容器
// 约束只限 node 的 parent，不限边的 target）。一手证据：语料 **5 份**真实导出
// （`X19_Tdouyin3` / `X12_Tdouyin4` / `X20_Tred2` / `X22_T_piliang2` /
// `X23_Tpiliang_table_2`）的 batch 体内 `throw_error → 900001 end`，且 HEAD 上
// 这 5 份编译成功、往返 `r0 ≍ r1` **逐字节相同**。
//
// 教训（2026-09-14，本规则初稿）：初稿把 `return` **整体**判成语言错 ⇒ 上述 5 份
// 从「往返不动点」变成「编译失败」（误伤，违反覆盖承诺）；收窄为「带值 return」
// 后语料恢复 670/670。详见
// docs/ideas-todo/2026-09-14-parallel-for-language-semantics.md §9.3。
//
// 唯一豁免（分支值臂）= QA action / attempt 臂的 return（P5 subscope return）。
func (s *iterationScopeScanner) checkReturn(node *ast.ReturnStmt) {
	if frame, ok := s.innermostScope(); ok && frame.kind == iterationScopeValueArm {
		// 分支值臂：该 return 归宿主节点（QA / attempt），不是流程终止。
		return
	}
	if node.Value() == nil {
		// void `return;` = 终止流程但**不选值** ⇒ 结果确定、平台有通道 ⇒ 合法。
		return
	}
	// `return template "…" using {…}`（W2）**不是**「带值逃逸循环」：它的值不是
	// 迭代产物，而是**流程答复端点**的值侧（`terminatePlan: useAnswerContent`）。
	// 位置由 `TEMPLATE_RETURN_PLACEMENT`（E1057，语言错）判，不由本判据判 ——
	// 与 `capability_gate.go::visitReturnStmt` 的既有分工一致（那里同样把模板
	// 返回让给 IR）。两条都报会把「作者写错位置」说成「并发语义未定义」：
	// 用户按码查文档会查到另一条规则（说明位置规则的那一节），而真正的规则是
	// 「模板返回只允许出现在流程出口位置」——一条与臂/循环种类**无关**的规则。
	if _, isTemplate := node.Value().(*ast.TemplateValue); isTemplate {
		return
	}
	for i := len(s.scopes) - 1; i >= 0; i-- {
		if s.scopes[i].kind != iterationScopeLoop || !s.scopes[i].parallelFor {
			continue
		}
		s.ctx.Emit(diag.PARALLEL_EXTERNAL_EXIT, diag.Error,
			"return inside a parallel-for iteration terminates the whole flow from one iteration while "+
				"sibling iterations are already in flight (which value wins is scheduler-dependent) — the "+
				"language does not define it; collect per-iteration values with 'yield' and return after "+
				"the loop instead", node.Span())
		return
	}
}

func (s *iterationScopeScanner) innermostScope() (iterationScopeFrame, bool) {
	if len(s.scopes) == 0 {
		return iterationScopeFrame{}, false
	}
	return s.scopes[len(s.scopes)-1], true
}
