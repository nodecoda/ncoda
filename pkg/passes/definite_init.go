package passes

// Definite initialization pass implements the classic symbol-table/control-
// flow join for E1048 UNINITIALIZED_USE.  Lexical identity comes from the
// ScopePass binding snapshot (SymbolID), not from source spelling.  Flow
// states are per control-flow edge and are merged at exhaustive joins by
// intersection.  In particular, a branch-local shadow retires only its own
// SymbolID, never an outer variable that happens to have the same name.

import (
	"fmt"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
)

// DefiniteInitPass implements the definite-initialization stage
// (definite_init_pass.py:33).
type DefiniteInitPass struct{}

// Run checks every function (entry first, then the declared functions,
// mirroring definite_init_pass.py:42-44) for uses of variables that are
// declared but not definitely assigned in the same block.
func (DefiniteInitPass) Run(p *ast.Program, ctx *Context) {
	if ctx.Bindings == nil {
		panic("passes: DefiniteInitPass requires a published binding snapshot (run ScopePass first)")
	}
	functions := []*ast.FunctionDef{}
	if e := p.Entry(); e != nil {
		functions = append(functions, e)
	}
	functions = append(functions, p.Functions()...)
	checker := &definiteInit{ctx: ctx, snap: ctx.Bindings}
	for _, fn := range functions {
		checker.checkFunction(fn)
	}
}

// definiteInit carries the per-run state, mirroring DefiniteInitPass's
// _ctx plus the initialized/declared sets threaded through the checks.
type definiteInit struct {
	ctx  *Context
	snap *binding.Snapshot
}

// varKey is the flow-state identity for one declared variable.  The SymbolID
// is authoritative when ScopePass resolved the declaration; the source name
// remains only as a deterministic fallback for diagnostics/testing paths.
type varKey struct {
	sym    binding.SymbolID
	name   string
	hasSym bool
}

func (d *definiteInit) declKey(node ast.Node, name string) varKey {
	if node != nil {
		if id, ok := d.snap.NodeIDOf(node); ok {
			if symID, found := d.snap.DeclarationSymbols().Get(id); found {
				return varKey{sym: symID, name: name, hasSym: true}
			}
		}
	}
	return varKey{name: name}
}

func (d *definiteInit) refKey(e *ast.IdentExpr) varKey {
	if id, ok := d.snap.NodeIDOf(e); ok {
		if symID, found := d.snap.ReferenceSymbols().Get(id); found {
			return varKey{sym: symID, name: e.Name(), hasSym: true}
		}
	}
	return varKey{name: e.Name()}
}

// loopVarKeys resolves loop variables through the scope recorded on the loop
// body.  The binder records all loop-variable declarations in that scope; the
// loop node itself is not sufficient for zip loops because only one node-level
// symbol slot exists.
func (d *definiteInit) loopVarKeys(body *ast.Block, names []string) []varKey {
	keys := make([]varKey, 0, len(names))
	scopeSymbols := map[string]binding.SymbolID{}
	if body != nil {
		if bodyID, ok := d.snap.NodeIDOf(body); ok {
			if scopeID, ok := d.snap.ScopeBindings().Get(bodyID); ok {
				if info, ok := d.snap.Scopes().Get(scopeID); ok {
					for _, symID := range info.Decls {
						if decl, ok := d.snap.Declarations().Get(symID); ok {
							scopeSymbols[decl.Name] = symID
						}
					}
				}
			}
		}
	}
	for _, name := range names {
		if name == "" {
			continue
		}
		if symID, ok := scopeSymbols[name]; ok {
			keys = append(keys, varKey{sym: symID, name: name, hasSym: true})
		} else {
			keys = append(keys, varKey{name: name})
		}
	}
	return keys
}

// replaceState installs a joined map into the caller-owned flow-state map.
func replaceState(dst, src map[varKey]bool) {
	for k := range dst {
		delete(dst, k)
	}
	for k, v := range src {
		dst[k] = v
	}
}

// checkFunction mirrors _check_function: parameters are always initialized.
func (d *definiteInit) checkFunction(fn *ast.FunctionDef) {
	initialized := map[varKey]bool{}
	for _, param := range fn.Params() {
		initialized[d.declKey(param, param.Name())] = true
	}
	// 顶层函数体没有外层词法作用域：继承集为空。
	d.checkBlock(fn.Body(), initialized, nil)
}

// blockExit is the block-analysis result: the definite-initialized state at
// the end of a block and symbols declared directly in that block.  Only the
// first component may flow into a control-flow join; branch-local symbols
// must never escape their branch scope.
type blockExit struct {
	initialized map[varKey]bool
	declared    map[varKey]bool
	// fallsThrough = 该块**执行到末尾后能到达后继语句**。return / break /
	// continue 终结的块不能到达汇聚点 ⇒ 它**不参与** joinBranches 的交集。
	// 依据 = 标准 §3.1 的口径：「`T v;` …… **所有可达路径**上都必须先赋值」——
	// 终结臂的路径**不可达**后继读点，把它算进交集 = 要求一条不存在的路径
	// 赋值（过严的假红：`if (c) { return "A"; } else { v = "B"; } return v;`
	// 被判 UNINITIALIZED_USE）。
	fallsThrough bool
}

// checkBlock checks a block starting from the incoming initialized state and
// returns the state at its exit together with the symbols declared **directly
// in this block**.
//
// `inherited` is the set of declarations lexically visible from the enclosing
// blocks; it is copied into this block's visible set.  The distinction matters
// because `checkExpr`'s rule is "declared AND definitely assigned": a nested
// block that started from an empty declared set made that rule vacuous inside
// any nested block, so `string v; if (c) { use(v); }` compiled and only failed
// later in the renderer (ADAPTER_ERROR, not E1048).
//
// The **returned** `declared` is `own` = visible − inherited, because the only
// other consumer is joinBranches' retirement step, and retiring an inherited
// symbol would delete an outer variable from the joined state (false
// positives).  Branch-local symbols must never escape their branch scope.
func (d *definiteInit) checkBlock(block *ast.Block, initialized, inherited map[varKey]bool) blockExit {
	declared := copyVarSet(inherited)
	if block == nil {
		return blockExit{initialized: copyVarSet(initialized), declared: map[varKey]bool{}, fallsThrough: true}
	}
	state := copyVarSet(initialized)
	falls := true
	for _, stmt := range block.Stmts() {
		// `x && falls`：checkStmt 必须先求值（它做检查），再与已有值相与
		// —— 一旦某条语句不落入后继（return / break / continue），后面的
		// 语句都在不可达代码里，不能把它翻回 true。
		falls = d.checkStmt(stmt, state, declared) && falls
	}
	return blockExit{initialized: state, declared: ownVarSet(declared, inherited), fallsThrough: falls}
}

// ownVarSet returns the symbols declared in this block (visible minus
// inherited).  The result is always a fresh map: joinBranches deletes from the
// joined state using exit.declared as the retirement set, so a shared map
// would corrupt the caller's visibility set.
func ownVarSet(visible, inherited map[varKey]bool) map[varKey]bool {
	own := make(map[varKey]bool, len(visible))
	for k := range visible {
		if !inherited[k] {
			own[k] = true
		}
	}
	return own
}

// checkArmBlock checks one exclusive arm body of a host statement
// (attempt success/failure, ask action/timeout) and publishes the host's arm
// bindings (`as r` / `failure(e)`) into that arm's **block-local** scope: the
// binding exists only where the arm runs, so it is visible inside the arm and
// must never escape the junction (joinBranches retires branch-declared keys —
// that is why the bindings belong to `own`).
// Mirrors checkBlock otherwise: same inherited visibility, same own-only exit.
func (d *definiteInit) checkArmBlock(block *ast.Block, initialized, inherited map[varKey]bool, bindings ...*ast.VarDeclStmt) blockExit {
	state := copyVarSet(initialized)
	declared := copyVarSet(inherited)
	for _, b := range bindings {
		if b == nil || b.Name() == "" {
			continue
		}
		key := d.declKey(b, b.Name())
		declared[key] = true
		state[key] = true
	}
	falls := true
	if block != nil {
		for _, stmt := range block.Stmts() {
			falls = d.checkStmt(stmt, state, declared) && falls
		}
	}
	return blockExit{initialized: state, declared: ownVarSet(declared, inherited), fallsThrough: falls}
}

// anyFalls 报告一组臂出口里**是否有**能到达汇聚点的那个（用于判宿主语句
// 本身能否落入后继）。
func anyFalls(exits ...blockExit) bool {
	for _, e := range exits {
		if e.fallsThrough {
			return true
		}
	}
	return false
}

// joinBranches implements the meet operation for definite initialization:
// a value is definitely assigned after a join only when every possible branch
// reaches the join with it initialized.  Symbols declared inside any branch
// are branch-local and are removed before the state escapes that scope.
func joinBranches(base map[varKey]bool, exits ...blockExit) map[varKey]bool {
	// 只让**能到达汇聚点**的臂参与交集：终结臂（以 return / break / continue
	// 结束的臂）不落到汇聚点，它的状态不是「汇聚点上的可能状态」。
	reaching := make([]blockExit, 0, len(exits))
	for _, exit := range exits {
		if exit.fallsThrough {
			reaching = append(reaching, exit)
		}
	}
	if len(reaching) == 0 {
		// 全部臂都终结（或压根没有臂）⇒ 汇聚点不可达：没有可达路径需要判定，
		// 保持进入时的状态（不新增任何「已确定赋值」事实）。
		return copyVarSet(base)
	}
	joined := map[varKey]bool{}
	for key := range reaching[0].initialized {
		everywhere := true
		for _, exit := range reaching[1:] {
			if !exit.initialized[key] {
				everywhere = false
				break
			}
		}
		if everywhere {
			joined[key] = true
		}
	}
	for _, exit := range reaching {
		for key := range exit.declared {
			delete(joined, key)
		}
	}
	return joined
}

// checkStmt analyzes one statement against the current block state.  The
// initialized map is the flow state; the declared map is the lexical scope
// for this block and distinguishes declarations from assignments.
//
// 返回值 = 该语句**能否落入后继语句**（控制流事实，不是状态）。终结子
// （return / break / continue）与「所有臂都终结」的复合语句为 false；它是
// joinBranches「只让可达汇聚点的臂参与交集」的唯一输入（标准 §3.1：所有
// **可达路径**上先赋值 —— 终结臂的路径不可达读点）。
func (d *definiteInit) checkStmt(stmt ast.Stmt, initialized, declared map[varKey]bool) bool {
	falls := true
	switch s := stmt.(ast.Node).(type) {
	case *ast.VarDeclStmt:
		key := d.declKey(s, s.Name())
		declared[key] = true
		if s.Init() != nil {
			d.checkExpr(s.Init(), initialized, declared)
			initialized[key] = true
		} else if s.IsMutable() && s.TypeRef() == nil {
			// 裸 `var x;`（反编译分支变量提升产物）按“已声明、值可能为
			// null”处理：coze 画布允许跨分支引用（未执行分支 = null），
			// merge/coalesce 显式处理 null —— 不触发 UNINITIALIZED_USE。
			// 带类型的裸声明（string x;）保持未初始化语义。
			initialized[key] = true
		}
		// Without an init the variable is NOT initialized yet.
	case *ast.AssignStmt:
		// Check the RHS first, then publish the LHS.
		if s.Value() != nil {
			d.checkExpr(s.Value(), initialized, declared)
		}
		if ident, ok := s.Target().(*ast.IdentExpr); ok {
			initialized[d.refKey(ident)] = true
		} else if s.Target() != nil {
			d.checkExpr(s.Target(), initialized, declared)
		}
	case *ast.ReturnStmt:
		if s.Value() != nil {
			d.checkExpr(s.Value(), initialized, declared)
		}
		falls = false
	case *ast.ExprStmt:
		if s.Expr() != nil {
			d.checkExpr(s.Expr(), initialized, declared)
		}
	case *ast.AttemptStmt:
		// attempt 的臂集合是**物理穷尽**的：平台 PT3 恒有两个出口端口
		// （default = 成功、branch_error = 失败，语料 9/9），恰有一臂执行 ⇒
		// 两臂都是可达出口，交集即 junction 后的事实。这正是 AGENTS.md
		// 「junction phi 成员判据……四宿主 if/switch/QA/attempt 同构」在
		// definite-init 侧的缺失分支：`T v;` 提升 idiom + 臂内赋值此前被判
		// UNINITIALIZED_USE（带类型裸声明登记 declared 但永不 initialized）。
		if s.Operation() != nil {
			d.checkExpr(s.Operation(), initialized, declared)
		}
		incoming := copyVarSet(initialized)
		exits := []blockExit{
			d.checkArmBlock(s.SuccessBody(), incoming, declared, s.ResultBinding()),
			d.checkArmBlock(s.FailureBody(), incoming, declared, s.ErrorBinding()),
		}
		replaceState(initialized, joinBranches(incoming, exits...))
		falls = anyFalls(exits...)
	case *ast.RequestInputStmt:
		// ask 的臂集合 = 每条 action 臂 + **一条 default（timeout）臂**：平台
		// 对该节点恒投影 default 出口（缺省 timeout 也投影空 default 臂，
		// 见 switch_arm_phi_lowering_contract_test），所以 timeout 臂即使为
		// 空（出口 = 进入时的状态，未赋值）也必须参与交集——否则源码只写
		// action 臂时会把「default 臂未写值」这条真实物理路径判成已赋值
		// （false negative）。这与「if 无 else 不 join、switch 无 default
		// 不 join」是同一条规则：**只 join 物理穷尽的臂集合**。
		if s.Config() != nil {
			d.checkExpr(s.Config(), initialized, declared)
		}
		incoming := copyVarSet(initialized)
		exits := make([]blockExit, 0, len(s.ActionBranches())+1)
		exits = append(exits, d.checkArmBlock(s.TimeoutBody(), incoming, declared))
		for _, branch := range s.ActionBranches() {
			if branch.ActionExpr() != nil {
				d.checkExpr(branch.ActionExpr(), incoming, declared)
			}
			exits = append(exits, d.checkArmBlock(branch.Body(), incoming, declared, s.ResultBinding()))
		}
		replaceState(initialized, joinBranches(incoming, exits...))
		falls = anyFalls(exits...)
	case *ast.IfStmt:
		if s.Condition() != nil {
			d.checkExpr(s.Condition(), initialized, declared)
		}
		incoming := copyVarSet(initialized)
		exits := []blockExit{d.checkBlock(s.ThenBody(), incoming, declared)}
		for _, elseIf := range s.ElseIfs() {
			if elseIf.Condition() != nil {
				d.checkExpr(elseIf.Condition(), incoming, declared)
			}
			exits = append(exits, d.checkBlock(elseIf.Body(), incoming, declared))
		}
		if s.ElseBody() != nil {
			exits = append(exits, d.checkBlock(s.ElseBody(), incoming, declared))
			replaceState(initialized, joinBranches(incoming, exits...))
		}
		// Without else, fall-through is possible; branch-local assignments
		// and declarations cannot be definite after the join.
		//
		// 语句本身能否落入后继：无 `else` ⇒ 条件为假的那条路总是存在 ⇒ true；
		// 有 `else` ⇒ 看有没有**能落到汇聚点**的臂（全臂 return ⇒ false）。
		falls = s.ElseBody() == nil || anyFalls(exits...)
	case *ast.WhileStmt:
		if s.Condition() != nil {
			d.checkExpr(s.Condition(), initialized, declared)
		}
		// Zero iterations are possible.  Analyze the body with the incoming
		// state, but do not publish its assignments as post-loop facts.
		d.checkBlock(s.Body(), initialized, declared)
	case *ast.ForStmt:
		for _, src := range s.LoopSources() {
			if src != nil {
				d.checkExpr(src, initialized, declared)
			}
		}
		loopInit := copyVarSet(initialized)
		for _, key := range d.loopVarKeys(s.Body(), s.LoopVars()) {
			loopInit[key] = true
		}
		d.checkBlock(s.Body(), loopInit, declared)
	case *ast.ParallelStmt:
		incoming := copyVarSet(initialized)
		exits := make([]blockExit, 0, len(s.Branches()))
		for _, branch := range s.Branches() {
			exits = append(exits, d.checkBlock(branch, incoming, declared))
		}
		replaceState(initialized, joinBranches(incoming, exits...))
		falls = anyFalls(exits...)
	case *ast.SwitchStmt:
		if s.Query() != nil {
			d.checkExpr(s.Query(), initialized, declared)
		}
		incoming := copyVarSet(initialized)
		exits := make([]blockExit, 0, len(s.Branches())+1)
		for _, br := range s.Branches() {
			exits = append(exits, d.checkBlock(br.Body(), incoming, declared))
		}
		if s.DefaultBody() != nil {
			exits = append(exits, d.checkBlock(s.DefaultBody(), incoming, declared))
			replaceState(initialized, joinBranches(incoming, exits...))
		}
		// 无 `default` ⇒ 「没有分支匹配」那条路总是存在 ⇒ true。
		falls = s.DefaultBody() == nil || anyFalls(exits...)
	case *ast.YieldStmt:
		// yield 的 RHS 是表达式，必须沿结构递归（此前 yield/output 的
		// RHS 里的未初始化读完全逃过本 pass，只在渲染层炸成 ADAPTER_ERROR）。
		if s.Value() != nil {
			d.checkExpr(s.Value(), initialized, declared)
		}
	case *ast.OutputStmt:
		if s.Value() != nil {
			d.checkExpr(s.Value(), initialized, declared)
		}
	case *ast.Block:
		// 裸块语句 = 嵌套词法作用域：继承可见集，块内声明不逃逸，但块内
		// 对**外层**变量的赋值会逃逸（块总是执行）。解析器今日不产生该
		// 形态，此处按同一普遍规则处理（不留特例）。
		if exit := d.checkBlock(s, initialized, declared); exit.initialized != nil {
			replaceState(initialized, exit.initialized)
			falls = exit.fallsThrough
		}
	case *ast.BreakStmt, *ast.ContinueStmt:
		// 迭代终结子没有操作数，没有可检查的表达式（其控制流语义由容器
		// 承载，见 AGENTS.md P4）。它们同样**不落入**继程序位置。
		falls = false
	default:
		// fail loud（R-F2/复发拦截）：新增语句种类必须显式处理，不得静默
		// 逃过 definite-init。两次实证漏检（attempt/ask 臂、yield/output
		// 的 RHS）都是「类型 switch 无 default」造成的。
		panic(fmt.Sprintf("passes: DefiniteInitPass: unhandled statement type %T", stmt))
	}
	return falls
}

// checkExpr mirrors _check_expr (definite_init_pass.py:143-165) and extends
// it with structural recursion. The Python reference only inspects
// IdentExpr / ForExpr / LambdaExpr (all other expressions are conservative
// no-ops); that misses uninitialized receivers inside composite
// expressions — `R v; return v.text;` never visited v (03_conditional_branch
// 实证:分支后引用提升变量 `return vllm_zh.text;` 在 false 分支未赋值仍编译
// 通过,反编译器被迫用提升+单分支赋值产出语义错误形态). Definite-init 必须
// 沿表达式结构递归:字段访问/索引/调用/一元/二元/三元/模板插值/数组/map
// 值/策略操作数/foreign-code 输入都要检查子表达式中的 IdentExpr,否则未初始化
// 引用藏在复合表达式里就是漏检。Lambda/For/ParallelFor 保持独立作用域处理。
func (d *definiteInit) checkExpr(expr ast.Expr, initialized, declared map[varKey]bool) {
	switch e := expr.(type) {
	case *ast.IdentExpr:
		if d.isBuiltinOrFunction(e) {
			return
		}
		key := d.refKey(e)
		if declared[key] && !initialized[key] {
			d.ctx.Emit(diag.UNINITIALIZED_USE, diag.Error,
				"Variable '"+e.Name()+"' is used before being definitely assigned",
				e.Span())
		}
	case *ast.FieldAccessExpr:
		if e.Obj() != nil {
			d.checkExpr(e.Obj(), initialized, declared)
		}
	case *ast.IndexExpr:
		if e.Obj() != nil {
			d.checkExpr(e.Obj(), initialized, declared)
		}
		if e.Index() != nil {
			d.checkExpr(e.Index(), initialized, declared)
		}
	case *ast.CallExpr:
		if e.Func() != nil {
			d.checkExpr(e.Func(), initialized, declared)
		}
		for _, a := range e.Args() {
			if a != nil {
				d.checkExpr(a, initialized, declared)
			}
		}
	case *ast.UnaryExpr:
		if e.Operand() != nil {
			d.checkExpr(e.Operand(), initialized, declared)
		}
	case *ast.BinaryExpr:
		if e.Left() != nil {
			d.checkExpr(e.Left(), initialized, declared)
		}
		if e.Right() != nil {
			d.checkExpr(e.Right(), initialized, declared)
		}
	case *ast.TernaryExpr:
		if e.Condition() != nil {
			d.checkExpr(e.Condition(), initialized, declared)
		}
		if e.ThenVal() != nil {
			d.checkExpr(e.ThenVal(), initialized, declared)
		}
		if e.ElseVal() != nil {
			d.checkExpr(e.ElseVal(), initialized, declared)
		}
	case *ast.TemplateString:
		for _, p := range e.Parts() {
			if p.Expr != nil {
				d.checkExpr(p.Expr, initialized, declared)
			}
		}
	case *ast.ArrayLit:
		for _, el := range e.Elements() {
			if el != nil {
				d.checkExpr(el, initialized, declared)
			}
		}
	case *ast.MapLit:
		for _, en := range e.Entries() {
			if en.Value != nil {
				d.checkExpr(en.Value, initialized, declared)
			}
		}
	case *ast.OperationPolicyExpr:
		if e.Operation() != nil {
			d.checkExpr(e.Operation(), initialized, declared)
		}
		for _, p := range e.Policies() {
			if dp, ok := p.(*ast.DefaultPolicy); ok && dp.Value() != nil {
				d.checkExpr(dp.Value(), initialized, declared)
			}
		}
	case *ast.ForeignCodeExpr:
		for _, in := range e.Inputs() {
			if in.Value() != nil {
				d.checkExpr(in.Value(), initialized, declared)
			}
		}
	case *ast.ParallelForExpr:
		for _, src := range e.LoopSources() {
			if src != nil {
				d.checkExpr(src, initialized, declared)
			}
		}
		loopInit := copyVarSet(initialized)
		for _, key := range d.loopVarKeys(e.Body(), e.LoopVars()) {
			loopInit[key] = true
		}
		d.checkBlock(e.Body(), loopInit, declared)
	case *ast.ForExpr:
		for _, src := range e.LoopSources() {
			if src != nil {
				d.checkExpr(src, initialized, declared)
			}
		}
		loopInit := copyVarSet(initialized)
		for _, key := range d.loopVarKeys(e.Body(), e.LoopVars()) {
			loopInit[key] = true
		}
		d.checkBlock(e.Body(), loopInit, declared)
	case *ast.LambdaExpr:
		// lambda 体是**词法内嵌**的表达式（binder 把 lambda 作用域 parent
		// 设为当前作用域），所以它看到的是同一份可见集 + 同一份 flow 状态：
		// 没有「lambda 特例」。lambda 形参由 binder 绑定、且只进 initialized
		// 不进 declared（与函数形参同一处理），因此不会误报。
		if e.Body() != nil {
			d.checkExpr(e.Body(), copyVarSet(initialized), copyVarSet(declared))
		}
	case *ast.TemplateValue:
		// `return template "…" using {…}`（W2）：模板文本是**不透明文本**
		// （不读、不解析），名额值是**读位置** ⇒ 逐个按普通值检查（未初始化
		// 读照常报 E1048）。语句本身的返回值语义不在本 pass 管辖内。
		for _, b := range e.Bindings() {
			if b.Value != nil {
				d.checkExpr(b.Value, initialized, declared)
			}
		}
	default:
		// fail loud：新增表达式种类必须显式处理（叶子字面量除外，见
		// definite_init_exhaustiveness_test.go 的机器检查）。静默跳过正是
		// attempt/ask 与 yield/output 两次漏检的复发机制。
		switch expr.(type) {
		case *ast.StringLit, *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.NullLit:
			// 叶子字面量没有子表达式，无需递归。
		default:
			panic(fmt.Sprintf("passes: DefiniteInitPass: unhandled expression type %T", expr))
		}
	}
}

// isBuiltinOrFunction mirrors _is_builtin_or_function
// (definite_init_pass.py:167-172): resolve the identifier's reference
// symbol and skip BUILTIN_FUNCTION / FUNCTION / NAMESPACE kinds.
//
// Deviation (recorded): the Python check also short-circuits on
// `getattr(symbol, "is_builtin", False)`; the Go SymbolDecl carries no
// is_builtin field, so the kind test alone decides. The Python
// except (RuntimeError, AttributeError) → False fallback is mirrored by the
// found=false paths below.
func (d *definiteInit) isBuiltinOrFunction(expr ast.Expr) bool {
	id, ok := d.snap.NodeIDOf(expr)
	if !ok {
		return false
	}
	symID, found := d.snap.ReferenceSymbols().Get(id)
	if !found {
		return false
	}
	decl, found := d.snap.Declarations().Get(symID)
	if !found {
		return false
	}
	switch decl.Kind {
	case binding.SymbolBuiltinFunction, binding.SymbolFunction, binding.SymbolNamespace:
		return true
	}
	return false
}

func copyVarSet(src map[varKey]bool) map[varKey]bool {
	out := make(map[varKey]bool, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
