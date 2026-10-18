package passes

// Const-substitution pre-pass, ported from
// stmt_translator.py:_substitute_constants (stmt_translator.py:230-363):
// before any analysis pass runs, every IdentExpr reference to a top-level
// const declaration is replaced by the declaration's value node inside the
// entry, function and code-function bodies. Downstream passes (scope, type,
// syntax, semantic, target validation) then see literal values instead of
// unresolved identifier references — the root cause of the pre-Task-24a
// "Undefined variable" (E1001) on const-using sources (08/09 fixtures had
// to inline the literal model strings).
//
// Rebuild strategy (immutable SourceAST): every touched node is rebuilt with
// a fresh NodeID and the original span via SetSpan, and the rebuilt bodies
// are handed back to the original *ast.Program through the exported option
// setters — the rewriteProgramBodies pattern (desugar.go). Const VALUE
// nodes are SHARED across substitution sites (the same pointer everywhere).
// Node_id parity is only exact for composite const values: Python
// _copy_literal (stmt_translator.py:207-226) mints a FRESH node (new
// node_id) per site for scalar literals (StringLit/IntLit/FloatLit/BoolLit/
// NullLit), and only falls back to copy.deepcopy — which preserves node_id
// — for other expression types such as TemplateString. So for scalars Go's
// shared pointer differs from Python (one node_id across sites vs one per
// site); for composite values it is the node_id equivalent. Observable
// artifact in the composite case: the Go binder enumerates the shared node
// once per tree position (no visited tracking), so the snapshot's ast_nodes
// debug table carries N+1 entries (decl value + N sites; e.g. 3 for a
// TemplateString const referenced twice) where Python's node_id-keyed dict
// keeps exactly 1. Binding identity is unaffected — the binder assigns
// per-compile traversal ordinals and every pass resolves via the
// pointer-keyed Snapshot.NodeIDOf (last-wins), so the shared node always
// maps to one consistent AstNodeID.
//
// Scope handling (I1, post-parity): the reference substituted by NAME only
// — an IdentExpr whose name equals a const was replaced even when a local
// variable of the same name shadows the const (stmt_translator.py:259-261:
// `expr.name in const_map` is the whole test). That parity behavior is a
// correctness bug (`const x = "a"; function main() { let x = "b"; return
// x; }` returned "a"), and its reason for existing — byte parity with the
// retired Python oracle — is gone. The Go port now tracks a lexical
// shadow set mirroring the binder's scope model: function/entry parameters,
// let/var declarations (in statement order, like the binder's define order),
// loop variables, lambda parameters, attempt result/error bindings and
// parallel-branch exports all suppress substitution for identifiers that
// resolve to them. Blocks (if/else/while/loop/lambda/parallel/attempt
// branches) get a COPY of the enclosing shadow set, so a block-local
// declaration never leaks outside its block — exactly the binder's
// per-block ScopeBlock model. Substitution stays a pre-binding pass, so
// node identity, span and shared-node behavior are unchanged; only the
// name-matching decision is now scope-aware.
//
// Key positions (I2, post-parity): the reference substituted map-literal KEYS
// too (`{ K: 1 }` with `const K = "a"` produced key "a"). A key is a name
// position — a label (`ast.StaticMapKey`) or a string/dynamic expression, never
// a reference — so substitution must rewrite only the VALUE. Landing the fix
// makes `{ K: 1 }` mean the label K whether or not an unrelated const named K
// exists (previously the map's field name changed with the const's presence:
// `m.K` FIELD_NOT_FOUND + `m.a` resolving). Direction = the settled "键是标签，
// 不做常量折叠" ruling; judge = TestSubstituteConstantsDoesNotRewriteMapKeyLabel
// plus the label expectation in TestSubstituteConstantsNestedShapes.
//
// Deviations from the reference, all cosmetic: ParallelForExpr is a Go
// sibling of ForExpr (Python subclasses ForExpr, so isinstance already
// matched it — the Go port adds an explicit case); ForeignCodeExpr source
// and CodeInputBinding values are rebuilt (Python mutates in place).
//
// No-op when the program declares no constants (stmt_translator.py:243-246
// early return). Const declaration VALUES are not traversed
// (stmt_translator.py:244-246: the const_map is built FROM them).

import "github.com/nodecoda/ncoda/pkg/ast"

// substituter carries the const table and the lexical shadow set for one
// function body walk. shadow records every name declared in the current
// lexical walk that would resolve to a local binding (params, let/var,
// loop variables, lambda params, attempt bindings, parallel exports);
// identifiers in the shadow set are never substituted, mirroring the
// binder's scope resolution.
type substituter struct {
	constMap map[string]ast.Expr
	shadow   map[string]bool
}

// SubstituteConstants rewrites the program in place, replacing const
// references in the entry, function and code-function bodies with the
// const value node — except where a local declaration shadows the const
// (see the scope-handling note in the header). It mirrors
// _substitute_constants (stmt_translator.py:230-363) and is invoked at the
// top of runPipeline before the stage loop (base.py:365-367).
func SubstituteConstants(program *ast.Program) {
	decls := program.ConstDecls()
	if len(decls) == 0 {
		return
	}
	s := &substituter{constMap: make(map[string]ast.Expr, len(decls)), shadow: map[string]bool{}}
	for _, cdecl := range decls {
		s.constMap[cdecl.Name()] = cdecl.Value()
	}
	if e := program.Entry(); e != nil {
		ast.WithEntry(s.rebuildFunction(e))(program)
	}
	if funcs := program.Functions(); len(funcs) > 0 {
		rebuilt := make([]*ast.FunctionDef, len(funcs))
		for i, fn := range funcs {
			rebuilt[i] = s.rebuildFunction(fn)
		}
		ast.WithFunctions(rebuilt)(program)
	}
	if cfs := program.CodeFunctions(); len(cfs) > 0 {
		rebuilt := make([]*ast.CodeFunctionDef, len(cfs))
		for i, fn := range cfs {
			rebuilt[i] = s.rebuildCodeFunction(fn)
		}
		ast.WithCodeFunctions(rebuilt)(program)
	}
}

// rebuildFunction substitutes one entry/function body. Function parameters
// shadow const names across the whole body (binder: params are defined in
// the function scope before the body walks), so they seed the shadow set.
// Parameter DEFAULTS pass through unchanged, as before — the reference
// substitutes only the body block.
func (s *substituter) rebuildFunction(fn *ast.FunctionDef) *ast.FunctionDef {
	saved := s.shadow
	s.shadow = map[string]bool{}
	for _, p := range fn.Params() {
		s.shadow[p.Name()] = true
	}
	body := s.block(fn.Body())
	s.shadow = saved
	nb := ast.NewFunctionDef(fn.Name(), fn.Params(), fn.ReturnType(), body)
	nb.SetSpan(fn.Span())
	return nb
}

// rebuildCodeFunction mirrors rebuildFunction for code functions.
func (s *substituter) rebuildCodeFunction(fn *ast.CodeFunctionDef) *ast.CodeFunctionDef {
	saved := s.shadow
	s.shadow = map[string]bool{}
	for _, p := range fn.Params() {
		s.shadow[p.Name()] = true
	}
	body := s.block(fn.Body())
	s.shadow = saved
	nb := ast.NewCodeFunctionDef(fn.Name(), fn.Params(), fn.ReturnType(), body)
	nb.SetSpan(fn.Span())
	return nb
}

// block substitutes every statement of a block in order, mutating the
// current shadow set as declarations are encountered (the binder defines
// let/var names at the statement walk point, so a reference before the
// declaration still resolves to the const — statement-order tracking
// mirrors that exactly).
func (s *substituter) block(block *ast.Block) *ast.Block {
	stmts := block.Stmts()
	subbed := make([]ast.Stmt, len(stmts))
	for i, stmt := range stmts {
		subbed[i] = s.stmt(stmt)
	}
	nb := ast.NewBlock(subbed)
	nb.SetSpan(block.Span())
	return nb
}

// scopedBlock walks a nested block against a COPY of the current shadow
// set: declarations inside the block (if/else branch, loop body, lambda
// body, parallel branch, attempt branch) shadow consts within the block
// only, never leaking to the enclosing scope — the binder's per-block
// ScopeBlock model.
func (s *substituter) scopedBlock(block *ast.Block) *ast.Block {
	saved := s.shadow
	s.shadow = cloneShadow(saved)
	nb := s.block(block)
	s.shadow = saved
	return nb
}

// loopBlock mirrors scopedBlock plus the loop variable: the iterable is
// substituted against the OUTER shadow (binder: visitForStmt walks the
// iterable before entering the loop scope), then the body runs under a
// shadow copy that includes the loop variable.
func (s *substituter) loopBlock(varName string, iterable ast.Expr, body *ast.Block) (ast.Expr, *ast.Block) {
	iter := s.expr(iterable)
	saved := s.shadow
	s.shadow = cloneShadow(saved)
	s.shadow[varName] = true
	nb := s.block(body)
	s.shadow = saved
	return iter, nb
}

// zipLoop 是 loopBlock 的多变量孪生：所有源在**外层**影子里代入（binder 先走
// 源再进循环作用域），体内在含**全部**循环变量的影子副本下走。缺它 = zip 循环
// 的其余变量/源静默丢失（见 stmt 的 ForStmt 分支注释）。
func (s *substituter) zipLoop(varNames []string, sources []ast.Expr, body *ast.Block) ([]ast.Expr, *ast.Block) {
	srcs := make([]ast.Expr, len(sources))
	for i, src := range sources {
		srcs[i] = s.expr(src)
	}
	saved := s.shadow
	s.shadow = cloneShadow(saved)
	for _, v := range varNames {
		s.shadow[v] = true
	}
	nb := s.block(body)
	s.shadow = saved
	return srcs, nb
}

func cloneShadow(src map[string]bool) map[string]bool {
	out := make(map[string]bool, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// stmt mirrors _sub_stmt (stmt_translator.py:312-353): every statement
// shape is rebuilt with substituted children. VarDeclStmt registers its
// name in the shadow set BEFORE substituting the init, matching the
// binder's define-then-walk order (a self-referencing `let x = x` resolves
// x to the local, not the const).
func (s *substituter) stmt(stmt ast.Stmt) ast.Stmt {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		s.shadow[n.Name()] = true
		if n.Init() == nil {
			return n
		}
		nb := ast.NewVarDeclStmt(n.Name(), n.TypeRef(), s.expr(n.Init()), n.IsMutable())
		nb.SetSpan(n.Span())
		return nb
	case *ast.AssignStmt:
		nb := ast.NewAssignStmt(s.expr(n.Target()), s.expr(n.Value()), n.Operation())
		nb.SetSpan(n.Span())
		return nb
	case *ast.ExprStmt:
		nb := ast.NewExprStmt(s.expr(n.Expr()))
		nb.SetSpan(n.Span())
		return nb
	case *ast.ReturnStmt:
		if n.Value() == nil {
			return n
		}
		// 投递开关是**语句身份**的一部分：重写值不得顺手把它抹掉。
		nb := ast.NewReturnStmt(s.expr(n.Value()), n.Streaming())
		nb.SetSpan(n.Span())
		return nb
	case *ast.IfStmt:
		elseIfs := n.ElseIfs()
		newElseIfs := make([]*ast.ElseIfBranch, len(elseIfs))
		for i, br := range elseIfs {
			nb := ast.NewElseIfBranch(s.expr(br.Condition()), s.scopedBlock(br.Body()))
			nb.SetSpan(br.Span())
			newElseIfs[i] = nb
		}
		nb := ast.NewIfStmt(s.expr(n.Condition()), s.scopedBlock(n.ThenBody()),
			newElseIfs, s.scopedBlockOrNil(n.ElseBody()))
		nb.SetSpan(n.Span())
		return nb
	case *ast.ForStmt:
		// zip 形（多变量 / 多源）：**逐条**代入并逐个登记影子变量。单变量访问器
		// （VarName/Iterable）只看第一个 ⇒ 走它们会把其余变量与源**静默丢掉**，
		// binder 随后对丢掉的循环变量报 UNDEFINED_VAR（一手：`const N = 1;` +
		// `for (a, b in zip(xs, ys))` ⇒ `Undefined variable: 'b'`；无 const 的程序
		// 不经过本 pass 故看不出）。修法与 desugar/normalize 的 zip 分支同一形状。
		if n.IsZip() {
			srcs, body := s.zipLoop(n.LoopVars(), n.LoopSources(), n.Body())
			var nb *ast.ForStmt
			if n.IsParallel() {
				nb = ast.NewZipParallelForStmt(n.LoopVars(), n.LoopTypes(), srcs, body, n.Concurrency(), n.OnError())
			} else {
				nb = ast.NewZipForStmt(n.LoopVars(), n.LoopTypes(), srcs, body)
			}
			nb.SetSpan(n.Span())
			return nb
		}
		iter, body := s.loopBlock(n.VarName(), n.Iterable(), n.Body())
		if n.IsParallel() {
			nb := ast.NewParallelForStmt(n.VarName(), n.VarType(), iter, body, n.Concurrency(), n.OnError())
			nb.SetSpan(n.Span())
			return nb
		}
		nb := ast.NewForStmt(n.VarName(), n.VarType(), iter, body)
		nb.SetSpan(n.Span())
		return nb
	case *ast.WhileStmt:
		nb := ast.NewWhileStmt(s.expr(n.Condition()), n.Limit(), s.scopedBlock(n.Body()))
		nb.SetSpan(n.Span())
		return nb
	case *ast.OutputStmt:
		nb := ast.NewOutputStmt(s.expr(n.Value()), n.Streaming())
		nb.SetSpan(n.Span())
		return nb
	case *ast.YieldStmt:
		if n.Value() == nil {
			return n
		}
		// 命名口保形（与 fold_const_calls / desugar 同一把尺子）：单形构造器会把
		// `yield img = x` 压成默认口 "output"，多口聚合静默退化成单口。
		var nb *ast.YieldStmt
		if n.Port() == "" {
			nb = ast.NewYieldStmt(s.expr(n.Value()))
		} else {
			nb = ast.NewNamedYieldStmt(n.Port(), s.expr(n.Value()))
		}
		nb.SetSpan(n.Span())
		return nb
	case *ast.ParallelStmt:
		// 分支名保形：`parallel (a: {...} b: {...})` 的标签是**多形字段**
		// （`BranchNames()`，与 Branches 按索引对齐），漏传即静默丢标签。
		nb := ast.NewParallelStmt(s.parallelBranches(n.Branches()), n.BranchNames()...)
		nb.SetSpan(n.Span())
		return nb
	case *ast.AttemptStmt:
		// Result/error bindings pass through unchanged (the reference
		// substitutes only operation + success/failure bodies), but their
		// NAMES shadow consts inside the respective branch bodies, exactly
		// like the binder's visitAttemptBranch (visitVarDeclStmt registers
		// the binding in the branch scope before the body walks).
		success := s.attemptBranch(n.ResultBinding(), n.SuccessBody())
		failure := s.attemptBranch(n.ErrorBinding(), n.FailureBody())
		nb := ast.NewAttemptStmt(s.expr(n.Operation()), n.ResultBinding(),
			success, n.ErrorBinding(), failure)
		nb.SetSpan(n.Span())
		return nb
	case *ast.SwitchStmt:
		branches := make([]*ast.SwitchBranch, len(n.Branches()))
		for i, br := range n.Branches() {
			nb := ast.NewSwitchBranch(br.Label(), s.scopedBlock(br.Body()))
			nb.SetSpan(br.Span())
			branches[i] = nb
		}
		var defBody *ast.Block
		if n.DefaultBody() != nil {
			defBody = s.scopedBlock(n.DefaultBody())
		}
		nb := ast.NewSwitchStmt(s.expr(n.Query()), n.Config(), branches, defBody, n.Result())
		nb.SetSpan(n.Span())
		return nb
	default:
		return stmt
	}
}

// attemptBranch walks one attempt branch (success/failure body) under a
// shadow copy seeded with the branch's result/error binding name (when the
// binding is non-nil).
func (s *substituter) attemptBranch(binding *ast.VarDeclStmt, body *ast.Block) *ast.Block {
	saved := s.shadow
	s.shadow = cloneShadow(saved)
	if binding != nil {
		s.shadow[binding.Name()] = true
	}
	nb := s.block(body)
	s.shadow = saved
	return nb
}

// parallelBranches walks every parallel branch under its own shadow copy
// (binder: fresh ScopeBlock per branch), then mirrors the binder's join:
// names declared directly in a branch are exported to the enclosing scope
// (parallel export), so they suppress const substitution for subsequent
// statements — publish skips names the parent already binds
// (ResolveSymbolLocal), which the shadow pre-check mirrors.
func (s *substituter) parallelBranches(branches []*ast.Block) []*ast.Block {
	saved := s.shadow
	declared := []string{}
	seen := map[string]bool{}
	out := make([]*ast.Block, len(branches))
	for i, br := range branches {
		s.shadow = cloneShadow(saved)
		out[i] = s.block(br)
		for name := range s.shadow {
			if !saved[name] && !seen[name] {
				seen[name] = true
				declared = append(declared, name)
			}
		}
	}
	s.shadow = cloneShadow(saved)
	for _, name := range declared {
		s.shadow[name] = true
	}
	return out
}

func (s *substituter) scopedBlockOrNil(block *ast.Block) *ast.Block {
	if block == nil {
		return nil
	}
	return s.scopedBlock(block)
}

// expr mirrors _sub_expr (stmt_translator.py:253-310): an IdentExpr naming
// a const becomes the const's value node (shared pointer) UNLESS the name
// is in the lexical shadow set (a local declaration shadows the const —
// I1); composite expressions are rebuilt with substituted children; leaf
// expressions (literals, non-const identifiers) pass through unchanged.
func (s *substituter) expr(expr ast.Expr) ast.Expr {
	if expr == nil {
		return nil
	}
	if ident, ok := expr.(*ast.IdentExpr); ok {
		if s.shadow[ident.Name()] {
			return ident // local declaration shadows the const — leave untouched
		}
		if value, found := s.constMap[ident.Name()]; found {
			return value // shared node: node_id parity only for composite const values (Python _copy_literal 207-226)
		}
		return ident
	}
	switch e := expr.(type) {
	case *ast.FieldAccessExpr:
		nb := ast.NewFieldAccessExpr(s.expr(e.Obj()), e.FieldName())
		nb.SetSpan(e.Span())
		return nb
	case *ast.IndexExpr:
		nb := ast.NewIndexExpr(s.expr(e.Obj()), s.expr(e.Index()))
		nb.SetSpan(e.Span())
		return nb
	case *ast.BinaryExpr:
		nb := ast.NewBinaryExpr(e.Op(), s.expr(e.Left()), s.expr(e.Right()))
		nb.SetSpan(e.Span())
		return nb
	case *ast.UnaryExpr:
		nb := ast.NewUnaryExpr(e.Op(), s.expr(e.Operand()))
		nb.SetSpan(e.Span())
		return nb
	case *ast.TernaryExpr:
		nb := ast.NewTernaryExpr(s.expr(e.Condition()), s.expr(e.ThenVal()), s.expr(e.ElseVal()))
		nb.SetSpan(e.Span())
		return nb
	case *ast.CallExpr:
		nb := ast.NewCallExpr(s.expr(e.Func()), e.TypeArgs(), s.exprs(e.Args()))
		nb.SetSpan(e.Span())
		return nb
	case *ast.ArrayLit:
		nb := ast.NewArrayLit(s.exprs(e.Elements()))
		nb.SetSpan(e.Span())
		return nb
	case *ast.MapLit:
		// KEY = name position, never a reference: a map key is a *label*
		// (ast.StaticMapKey: "a label, not a reference") or a string/free
		// expression — substituting a top-level const there silently changed
		// the key itself (`const K = "a"; let m = { K: 1 };` produced key
		// "a", so `m.K` was FIELD_NOT_FOUND while `m.a` resolved: the same
		// spelling meant two different keys depending on whether an
		// unrelated same-named const existed). Same rule as the TemplateValue
		// branch below (`using {…}` binding names are labels too) and as
		// LANGUAGE-REFERENCE §1.6 (name positions are lexical, all of them).
		// Only the VALUE is a reference position.
		entries := e.Entries()
		newEntries := make([]ast.MapEntry, len(entries))
		for i, en := range entries {
			newEntries[i] = ast.MapEntry{Key: en.Key, Value: s.expr(en.Value)}
		}
		nb := ast.NewMapLit(newEntries)
		nb.SetSpan(e.Span())
		return nb
	case *ast.TemplateString:
		nb := ast.NewTemplateString(s.templateParts(e.Parts()))
		nb.SetSpan(e.Span())
		return nb
	case *ast.LambdaExpr:
		// Lambda parameters shadow const names inside the lambda body
		// (binder: visitLambdaExpr defines params in a fresh block scope).
		saved := s.shadow
		s.shadow = cloneShadow(saved)
		for _, p := range e.Params() {
			s.shadow[p] = true
		}
		body := s.expr(e.Body())
		s.shadow = saved
		nb := ast.NewLambdaExpr(e.Params(), body)
		nb.SetSpan(e.Span())
		return nb
	case *ast.ForExpr:
		if e.IsZip() {
			srcs, body := s.zipLoop(e.LoopVars(), e.LoopSources(), e.Body())
			nb := ast.NewZipForExpr(e.LoopVars(), e.LoopTypes(), srcs, body)
			nb.SetSpan(e.Span())
			return nb
		}
		iter, body := s.loopBlock(e.VarName(), e.Iterable(), e.Body())
		nb := ast.NewForExpr(e.VarName(), e.VarType(), iter, body)
		nb.SetSpan(e.Span())
		return nb
	case *ast.ParallelForExpr:
		// Python ParallelForExpr subclasses ForExpr, so the reference's
		// `isinstance(expr, ForExpr)` branch already matched; Go needs the
		// explicit sibling case.
		if e.IsZip() {
			srcs, body := s.zipLoop(e.LoopVars(), e.LoopSources(), e.Body())
			nb := ast.NewZipParallelForExpr(e.LoopVars(), e.LoopTypes(), srcs, body, e.Concurrency(), e.OnError())
			nb.SetSpan(e.Span())
			return nb
		}
		iter, body := s.loopBlock(e.VarName(), e.Iterable(), e.Body())
		nb := ast.NewParallelForExpr(e.VarName(), e.VarType(), iter, body, e.Concurrency(), e.OnError())
		nb.SetSpan(e.Span())
		return nb
	case *ast.ForeignCodeExpr:
		inputs := e.Inputs()
		newInputs := make([]*ast.CodeInputBinding, len(inputs))
		for i, in := range inputs {
			nb := ast.NewCodeInputBinding(in.Name(), in.TypeRef(), s.expr(in.Value()))
			nb.SetSpan(in.Span())
			newInputs[i] = nb
		}
		src := e.Source()
		newSrc := ast.NewTemplateString(s.templateParts(src.Parts()))
		newSrc.SetSpan(src.Span())
		nb := ast.NewForeignCodeExpr(e.Language(), newInputs, e.Output(), newSrc)
		nb.SetSpan(e.Span())
		return nb
	case *ast.OperationPolicyExpr:
		policies := e.Policies()
		newPolicies := make([]ast.OperationPolicy, len(policies))
		for i, pol := range policies {
			if dp, ok := pol.(*ast.DefaultPolicy); ok && dp.Value() != nil {
				newPolicies[i] = ast.NewDefaultPolicy(s.expr(dp.Value()))
			} else {
				newPolicies[i] = pol // Retry/Timeout policies carry no Expr children
			}
		}
		nb := ast.NewOperationPolicyExpr(s.expr(e.Operation()), newPolicies)
		nb.SetSpan(e.Span())
		return nb
	case *ast.TemplateValue:
		// `return template "…" using {…}`（W2）：模板文本**逐字承载**（不是表达式，
		// 不参与常量代入），名额值是表达式 ⇒ 逐个递归重建。
		bindings := e.Bindings()
		rebuilt := make([]*ast.TemplateBinding, 0, len(bindings))
		for _, bd := range bindings {
			nb := &ast.TemplateBinding{Name: bd.Name, Value: s.expr(bd.Value)}
			rebuilt = append(rebuilt, nb)
		}
		nt := ast.NewTemplateValue(e.Text(), rebuilt)
		nt.SetSpan(e.Span())
		return nt
	default:
		// Literals (StringLit/IntLit/FloatLit/BoolLit/NullLit) and any node
		// kind the reference leaves untouched: unchanged.
		return expr
	}
}

func (s *substituter) exprs(exprs []ast.Expr) []ast.Expr {
	out := make([]ast.Expr, len(exprs))
	for i, e := range exprs {
		out[i] = s.expr(e)
	}
	return out
}

// templateParts mirrors the TemplateString part walk
// (stmt_translator.py:283-284): only Expr parts are substituted, text parts
// pass through.
func (s *substituter) templateParts(parts []ast.TemplatePart) []ast.TemplatePart {
	out := make([]ast.TemplatePart, len(parts))
	for i, p := range parts {
		if p.Expr != nil {
			out[i] = ast.TemplatePart{Text: p.Text, Expr: s.expr(p.Expr)}
		} else {
			out[i] = p
		}
	}
	return out
}
