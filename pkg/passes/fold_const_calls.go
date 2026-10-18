package passes

// Constant-call folding pre-pass (2026-09-29, R-146).
//
// 折叠 `int(<数字串字面量>)` / `float(<数字串字面量>)` 为数值字面量。
//
// 为什么要有它（一手）：coze-biz 的画布参数槽**只接「变量引用或字面量」**
// （`internal/backend/cozebiz` 的 ADAPTER_ERROR 判据），所以 `int("0")` 不能内联进
// 槽位 —— 它必须先物化成一个 code shim（`let vX = int("0");`）再绑变量。实测
// `function main() -> int { let m = int("0"); … }` 真 node 4→4 但多一个 code 节点，
// 而**折叠后零节点**（不可变绑定的标量字面量走 IR 的 litVars 常量折叠，直接内联进
// 下游槽位）。同一个常量两种产物 ⇒ 一条「多余载体」的家族。
//
// 折叠的**判据 = 语言函数的语义**，不是猜：`int("0")` 的值就是 `0`，编译期算得出来
// ⇒ 算完。判据本体 = `NumericStringLiteralValue`（与本仓反编译侧参数边界的**常量
// 归一**调的是同一个函数 —— 一处定义）。**有损不折叠**（`int("0.5")` python 里是
// ValueError，`int("abc")` 同样）⇒ 原样留给运行期（不发明值）。
//
// 位置：`SubstituteConstants` 之后（`runPipeline` 顶部）——先代入常量，再折叠调用，
// 于是 `const N = "0"; let m = int(N);` 也能折叠（代入把字面量放到位）。
// 与 `const_substitution.go` 同一族：两者都是**分析阶段之前的 AST 重写**，都用
// 「重建节点 + SetSpan」的不可变策略（span 保留给诊断）。

import "github.com/nodecoda/ncoda/pkg/ast"

// FoldConstCalls rewrites the program in place, folding numeric builtin calls
// on numeric-string literals (entry + functions + code functions, mirroring
// SubstituteConstants' reach).
func FoldConstCalls(program *ast.Program) {
	f := &constCallFolder{}
	if e := program.Entry(); e != nil {
		ast.WithEntry(f.function(e))(program)
	}
	if funcs := program.Functions(); len(funcs) > 0 {
		rebuilt := make([]*ast.FunctionDef, len(funcs))
		for i, fn := range funcs {
			rebuilt[i] = f.function(fn)
		}
		ast.WithFunctions(rebuilt)(program)
	}
	if cfs := program.CodeFunctions(); len(cfs) > 0 {
		rebuilt := make([]*ast.CodeFunctionDef, len(cfs))
		for i, fn := range cfs {
			rebuilt[i] = f.codeFunction(fn)
		}
		ast.WithCodeFunctions(rebuilt)(program)
	}
}

type constCallFolder struct{}

func (f *constCallFolder) function(fn *ast.FunctionDef) *ast.FunctionDef {
	nb := ast.NewFunctionDef(fn.Name(), f.params(fn.Params()), fn.ReturnType(), f.block(fn.Body()))
	nb.SetSpan(fn.Span())
	return nb
}

func (f *constCallFolder) codeFunction(fn *ast.CodeFunctionDef) *ast.CodeFunctionDef {
	nb := ast.NewCodeFunctionDef(fn.Name(), f.params(fn.Params()), fn.ReturnType(), f.block(fn.Body()))
	nb.SetSpan(fn.Span())
	return nb
}

// params folds parameter defaults (`function main(int n = int("0"))`).
func (f *constCallFolder) params(params []*ast.ParamDecl) []*ast.ParamDecl {
	out := make([]*ast.ParamDecl, len(params))
	for i, p := range params {
		if p.Default() == nil {
			out[i] = p
			continue
		}
		nb := ast.NewParamDecl(p.Name(), p.TypeRef(), f.expr(p.Default()))
		nb.SetSpan(p.Span())
		out[i] = nb
	}
	return out
}

func (f *constCallFolder) block(block *ast.Block) *ast.Block {
	if block == nil {
		return nil
	}
	stmts := block.Stmts()
	out := make([]ast.Stmt, len(stmts))
	for i, s := range stmts {
		out[i] = f.stmt(s)
	}
	nb := ast.NewBlock(out)
	nb.SetSpan(block.Span())
	return nb
}

func (f *constCallFolder) stmt(stmt ast.Stmt) ast.Stmt {
	switch n := stmt.(type) {
	case *ast.Block:
		return f.block(n)
	case *ast.VarDeclStmt:
		if n.Init() == nil {
			return n
		}
		nb := ast.NewVarDeclStmt(n.Name(), n.TypeRef(), f.expr(n.Init()), n.IsMutable())
		nb.SetSpan(n.Span())
		return nb
	case *ast.AssignStmt:
		nb := ast.NewAssignStmt(f.expr(n.Target()), f.expr(n.Value()), n.Operation())
		nb.SetSpan(n.Span())
		return nb
	case *ast.ExprStmt:
		nb := ast.NewExprStmt(f.expr(n.Expr()))
		nb.SetSpan(n.Span())
		return nb
	case *ast.ReturnStmt:
		if n.Value() == nil {
			return n
		}
		nb := ast.NewReturnStmt(f.expr(n.Value()), n.Streaming())
		nb.SetSpan(n.Span())
		return nb
	case *ast.YieldStmt:
		if n.Value() == nil {
			return n
		}
		// 命名口必须保形：`yield img = x` 的端口名是**多形字段**（`Port()`），
		// 单形构造器 `NewYieldStmt` 会把它压成默认口 "output" —— 多口聚合因此
		// 退化成单口（一手：`for (x in xs) { yield img = x; yield audio = x; }`
		// 经本 pass 后 `agg.audio` 解析不到端口 ⇒ 静默变 any ⇒ 下游 TYPE_MISMATCH）。
		var nb *ast.YieldStmt
		if n.Port() == "" {
			nb = ast.NewYieldStmt(f.expr(n.Value()))
		} else {
			nb = ast.NewNamedYieldStmt(n.Port(), f.expr(n.Value()))
		}
		nb.SetSpan(n.Span())
		return nb
	case *ast.OutputStmt:
		nb := ast.NewOutputStmt(f.expr(n.Value()), n.Streaming())
		nb.SetSpan(n.Span())
		return nb
	case *ast.IfStmt:
		elseIfs := n.ElseIfs()
		newElseIfs := make([]*ast.ElseIfBranch, len(elseIfs))
		for i, br := range elseIfs {
			nb := ast.NewElseIfBranch(f.expr(br.Condition()), f.block(br.Body()))
			nb.SetSpan(br.Span())
			newElseIfs[i] = nb
		}
		nb := ast.NewIfStmt(f.expr(n.Condition()), f.block(n.ThenBody()), newElseIfs, f.block(n.ElseBody()))
		nb.SetSpan(n.Span())
		return nb
	case *ast.ForStmt:
		// zip 形（多变量 / 多源）必须逐条保留 —— 单变量构造器会把多源截断。
		var sources []ast.Expr
		if n.IsZip() {
			sources = f.exprs(n.LoopSources())
		} else {
			sources = []ast.Expr{f.expr(n.Iterable())}
		}
		body := f.block(n.Body())
		var nb *ast.ForStmt
		switch {
		case n.IsZip() && n.IsParallel():
			nb = ast.NewZipParallelForStmt(n.LoopVars(), n.LoopTypes(), sources, body, n.Concurrency(), n.OnError())
		case n.IsZip():
			nb = ast.NewZipForStmt(n.LoopVars(), n.LoopTypes(), sources, body)
		case n.IsParallel():
			nb = ast.NewParallelForStmt(n.VarName(), n.VarType(), sources[0], body, n.Concurrency(), n.OnError())
		default:
			nb = ast.NewForStmt(n.VarName(), n.VarType(), sources[0], body)
		}
		nb.SetSpan(n.Span())
		return nb
	case *ast.WhileStmt:
		nb := ast.NewWhileStmt(f.expr(n.Condition()), n.Limit(), f.block(n.Body()))
		nb.SetSpan(n.Span())
		return nb
	case *ast.ParallelStmt:
		branches := n.Branches()
		out := make([]*ast.Block, len(branches))
		for i, br := range branches {
			out[i] = f.block(br)
		}
		nb := ast.NewParallelStmt(out, n.BranchNames()...)
		nb.SetSpan(n.Span())
		return nb
	case *ast.AttemptStmt:
		nb := ast.NewAttemptStmt(f.expr(n.Operation()), n.ResultBinding(),
			f.block(n.SuccessBody()), n.ErrorBinding(), f.block(n.FailureBody()))
		nb.SetSpan(n.Span())
		return nb
	case *ast.SwitchStmt:
		branches := n.Branches()
		out := make([]*ast.SwitchBranch, len(branches))
		for i, br := range branches {
			nb := ast.NewSwitchBranch(br.Label(), f.block(br.Body()))
			nb.SetSpan(br.Span())
			out[i] = nb
		}
		nb := ast.NewSwitchStmt(f.expr(n.Query()), n.Config(), out, f.block(n.DefaultBody()), n.Result())
		nb.SetSpan(n.Span())
		return nb
	case *ast.RequestInputStmt:
		branches := n.ActionBranches()
		out := make([]*ast.HumanActionBranch, len(branches))
		for i, br := range branches {
			nb := ast.NewHumanActionBranch(f.expr(br.ActionExpr()), f.block(br.Body()))
			nb.SetSpan(br.Span())
			out[i] = nb
		}
		nb := ast.NewRequestInputStmt(n.FormType(), n.ActionType(), n.Config(),
			n.ResultBinding(), out, f.block(n.TimeoutBody()))
		nb.SetSpan(n.Span())
		return nb
	default:
		// Break/Continue 等无表达式子节点。
		return stmt
	}
}

func (f *constCallFolder) exprs(exprs []ast.Expr) []ast.Expr {
	out := make([]ast.Expr, len(exprs))
	for i, e := range exprs {
		out[i] = f.expr(e)
	}
	return out
}

// expr rebuilds an expression with folded children; a foldable call is
// replaced by its literal value (the literal carries the call's span).
func (f *constCallFolder) expr(expr ast.Expr) ast.Expr {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *ast.CallExpr:
		args := f.exprs(e.Args())
		if lit, ok := foldNumericStringCall(builtinCallName(e.Func()), args); ok {
			lit.SetSpan(e.Span())
			return lit
		}
		nb := ast.NewCallExpr(f.expr(e.Func()), e.TypeArgs(), args)
		nb.SetSpan(e.Span())
		return nb
	case *ast.FieldAccessExpr:
		nb := ast.NewFieldAccessExpr(f.expr(e.Obj()), e.FieldName())
		nb.SetSpan(e.Span())
		return nb
	case *ast.IndexExpr:
		nb := ast.NewIndexExpr(f.expr(e.Obj()), f.expr(e.Index()))
		nb.SetSpan(e.Span())
		return nb
	case *ast.BinaryExpr:
		nb := ast.NewBinaryExpr(e.Op(), f.expr(e.Left()), f.expr(e.Right()))
		nb.SetSpan(e.Span())
		return nb
	case *ast.UnaryExpr:
		nb := ast.NewUnaryExpr(e.Op(), f.expr(e.Operand()))
		nb.SetSpan(e.Span())
		return nb
	case *ast.TernaryExpr:
		nb := ast.NewTernaryExpr(f.expr(e.Condition()), f.expr(e.ThenVal()), f.expr(e.ElseVal()))
		nb.SetSpan(e.Span())
		return nb
	case *ast.ArrayLit:
		nb := ast.NewArrayLit(f.exprs(e.Elements()))
		nb.SetSpan(e.Span())
		return nb
	case *ast.MapLit:
		entries := e.Entries()
		out := make([]ast.MapEntry, len(entries))
		for i, en := range entries {
			// 键是**标签**（不做折叠 / 不改写），只折值 —— 与常量代入同一把尺子。
			out[i] = ast.MapEntry{Key: en.Key, Value: f.expr(en.Value)}
		}
		nb := ast.NewMapLit(out)
		nb.SetSpan(e.Span())
		return nb
	case *ast.TemplateString:
		parts := e.Parts()
		out := make([]ast.TemplatePart, len(parts))
		for i, p := range parts {
			if p.Expr != nil {
				out[i] = ast.TemplatePart{Text: p.Text, Expr: f.expr(p.Expr)}
			} else {
				out[i] = p
			}
		}
		nb := ast.NewTemplateString(out)
		nb.SetSpan(e.Span())
		return nb
	case *ast.TemplateValue:
		bindings := e.Bindings()
		out := make([]*ast.TemplateBinding, 0, len(bindings))
		for _, bd := range bindings {
			out = append(out, &ast.TemplateBinding{Name: bd.Name, Value: f.expr(bd.Value)})
		}
		nt := ast.NewTemplateValue(e.Text(), out)
		nt.SetSpan(e.Span())
		return nt
	case *ast.LambdaExpr:
		nb := ast.NewLambdaExpr(e.Params(), f.expr(e.Body()))
		nb.SetSpan(e.Span())
		return nb
	case *ast.ForExpr:
		body := f.block(e.Body())
		if e.IsZip() {
			nb := ast.NewZipForExpr(e.LoopVars(), e.LoopTypes(), f.exprs(e.LoopSources()), body)
			nb.SetSpan(e.Span())
			return nb
		}
		nb := ast.NewForExpr(e.VarName(), e.VarType(), f.expr(e.Iterable()), body)
		nb.SetSpan(e.Span())
		return nb
	case *ast.ParallelForExpr:
		body := f.block(e.Body())
		if e.IsZip() {
			nb := ast.NewZipParallelForExpr(e.LoopVars(), e.LoopTypes(), f.exprs(e.LoopSources()), body,
				e.Concurrency(), e.OnError())
			nb.SetSpan(e.Span())
			return nb
		}
		nb := ast.NewParallelForExpr(e.VarName(), e.VarType(), f.expr(e.Iterable()), body,
			e.Concurrency(), e.OnError())
		nb.SetSpan(e.Span())
		return nb
	case *ast.OperationPolicyExpr:
		policies := e.Policies()
		out := make([]ast.OperationPolicy, len(policies))
		for i, pol := range policies {
			if dp, ok := pol.(*ast.DefaultPolicy); ok && dp.Value() != nil {
				nd := ast.NewDefaultPolicy(f.expr(dp.Value()))
				nd.SetSpan(dp.Span())
				out[i] = nd
			} else {
				out[i] = pol
			}
		}
		nb := ast.NewOperationPolicyExpr(f.expr(e.Operation()), out)
		nb.SetSpan(e.Span())
		return nb
	case *ast.ForeignCodeExpr:
		inputs := e.Inputs()
		newInputs := make([]*ast.CodeInputBinding, len(inputs))
		for i, in := range inputs {
			nb := ast.NewCodeInputBinding(in.Name(), in.TypeRef(), f.expr(in.Value()))
			nb.SetSpan(in.Span())
			newInputs[i] = nb
		}
		nb := ast.NewForeignCodeExpr(e.Language(), newInputs, e.Output(), e.Source())
		nb.SetSpan(e.Span())
		return nb
	default:
		// 字面量 / 标识符 等叶子：原样。
		return expr
	}
}

// builtinCallName 取**语言内建**调用名（无 Receiver 的裸标识符）。限定名调用
// （`p.op(...)` / `json.stringify(...)`）返回 "" —— 不是内建，不折。
func builtinCallName(fn ast.Expr) string {
	if id, ok := fn.(*ast.IdentExpr); ok {
		// `$int(...)`（`$` 转义）= 用户标识符，**不是**语言内建 —— 折叠它等于把
		// 「escaped 关键字必须硬报错」这条不变量静默抹掉（一手：折叠后
		// `$int("3")` 编过了，`zz_dollar_escape_test.go` 的
		// TestDollarEscapedKeywordBuiltinCallIllegal 变红）。闭集漏一档 ⇒ 先问转义位。
		if id.Escaped() {
			return ""
		}
		return id.Name()
	}
	return ""
}

// foldNumericStringCall folds `int(<数字串字面量>)` / `float(<数字串字面量>)`
// into the corresponding numeric literal. Judge body = NumericStringLiteralValue
// (one definition with the decompiler-side parameter boundary normalization);
// lossy / non-numeric / unrepresentable ⇒ no fold (leave the call alone).
func foldNumericStringCall(name string, args []ast.Expr) (ast.Expr, bool) {
	if len(args) != 1 || (name != "int" && name != "float") {
		return nil, false
	}
	lit, ok := args[0].(*ast.StringLit)
	if !ok {
		return nil, false
	}
	kind := "int"
	if name == "float" {
		kind = "float"
	}
	val, ok := NumericStringLiteralValue(lit.Value(), kind)
	if !ok {
		return nil, false
	}
	switch v := val.(type) {
	case int64:
		return ast.NewIntLit(v), true
	case float64:
		return ast.NewFloatLit(v), true
	}
	return nil, false
}
