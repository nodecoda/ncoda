package ir

import (
	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
)

// ── 使用集预扫（usage pre-scan）────────────────────────────────────────────
//
// 为什么需要它：`shim_required ⟺ L ∧ ¬∃c`（正本 = docs/five-step/spec/SHIM-ESCAPE-CONTRACT.md
// §0.1）里的 `L`（**位置必要性**）今天没有判据 —— 管线是「先物化、后清理」，事后只能删
// 「无消费者」的载体，删不掉「有一个平凡消费者」的载体（实测：带标记 shim 里 84~87%
// 不产生新值）。`L` 必须在**物化之前**由使用面判定，所以要有这个函数级预扫。
//
// 它只**测量**，不决策：把每个绑定名的使用面账单摆出来（整值读 / 成员读 / 写点 / 分支内写点）。
// 决策（R-43 A 档的四条前置）由调用方按正本 §42.3 施加 —— 本函数不替它选边。
//
// 口径（与正本 §0.1 的 `L` 三项一一对应）：
//   · 整值读   = 裸标识符出现在值位置（返回、实参、字面量元素、二元运算…）
//   · 成员读   = 标识符是成员访问链的**基**（`r1.slot`、`xs[0]`、`a.b.c` 都算）
//   · 写点     = 声明带初始化（`T v = e;`）/ 赋值（`v = e;`；成员目标另计）
//   · 分支内写 = 写点落在分支/循环/attempt/parallel/switch 体内（φ 的信号）
//   · 写点形态 = 记录字面量 / 成员写 / 其余（§42.3 A 轴 P2 的输入）
//
// 不被计为读的位置（否则函数名 / 命名空间会污染账单）：
//   · 调用者的 callee：`fmt(x)` 的 `fmt`、`json.stringify(x)` 的 `json`（两者都经
//     chainBase 剥到基再排除）
//   · 赋值目标：`v = e` / `v.f = e` 的 `v`（是写，不是读）；但 `arr[i] = e` 里的
//     `i` 仍是读（索引表达式照常走）
//   · 类型引用（是 TypeRef，本来就不是 IdentExpr）
//
// 已知口径边界（登记，不当决策依据）：
//   · for 的循环变量只记 `Declared`，其读点照常入账；调用方按「有写点」过滤即可排除。
//   · 同名绑定在不同作用域复用会合并成一条账单（本函数是**按名字**的近似，不建符号表）。

// BindingUsage 是一个绑定名的使用面账单。
//
// 写点按**值形态**三分类（`§42.3` A 轴 P2 的输入）：`Writes == LiteralWrites + MemberWrites + OtherWrites`
// —— 这个等式是账单的内部一致性不变量，判据锁住它。
type BindingUsage struct {
	Name          string
	Declared      bool
	WholeReads    int // 整值读
	MemberReads   int // 成员读（成员链的基）
	Writes        int // 写点（声明带初始化 + 赋值）
	LiteralWrites int // 其中：值是记录字面量（`v = { … };`）
	MemberWrites  int // 其中：以成员为目标（`v.f = e`）
	OtherWrites   int // 其中：其余（`v = <任意表达式>;`）
	BranchWrites  int // 其中：落在分支 / 循环 / attempt / parallel / switch 体内
}

// TotalReads 是读点总数（整值 + 成员）。
func (u *BindingUsage) TotalReads() int { return u.WholeReads + u.MemberReads }

// MemberOnlyReads 报告「每个读点都是成员读」——正本 §42.3 A 档的**第一**条前置（P1 的语言面）。
// 它**不**等于「不该物化」：还要叠加 P2 / P3 / P4。
func (u *BindingUsage) MemberOnlyReads() bool { return u.MemberReads > 0 && u.WholeReads == 0 }

// WholeValueObservable 报告整值语义**可观察**（P1 的反面）：整值口有消费者，或一次都没被读。
// 一次都没读 = 死载体分支（`¬L`），与「只读成员」是两个不同的处置，所以这里和
// MemberOnlyReads() 分开表达，不合并。
func (u *BindingUsage) WholeValueObservable() bool { return u.WholeReads > 0 || u.TotalReads() == 0 }

// P2 报告「写点全是记录字面量或成员写」——§42.3 A 轴 P2。任一写点不是这两种形态 ⇒ 该绑定
// **保持整值记录**（fail-safe：不折）。
func (u *BindingUsage) AllWritesAreLiteralOrMember() bool {
	return u.Writes > 0 && u.LiteralWrites+u.MemberWrites == u.Writes
}

// UsageAllowsWholeValueFold 是 §42.3 A 轴**使用面**的两条前置：P1 ∧ P2。
//
// **它不是「可折」的充分条件，别当成判决**。三条未覆盖的义务：
//
//	· 类型面：账单**不看类型** —— 调用方必须先确定声明类型是**记录**（`xs[0]` 在记录上不合法，
//	  所以「数组下标读」这类成员读会被类型条件排除；账单本身分不出 `r1.slot` 与 `xs[0]`）；
//	· P3（字段表达式只求值一次）与 P4（无别名 = P1 的推论）是**变换侧义务**，不是账单上的计数；
//	· 汇聚面：分支内写点（`BranchWrites > 0`）意味着 φ 组构造也要跟着折（§42.5 改动点 4）。
func (u *BindingUsage) UsageAllowsWholeValueFold() bool {
	return u.MemberOnlyReads() && u.AllWritesAreLiteralOrMember()
}

// PrescanBindingUsages 扫出入口函数的体，返回「绑定名 → 使用面账单」。
// 非绑定名（函数名、命名空间、平台内建）不会被记入 —— 见文件头注的排除面。
func PrescanBindingUsages(prog *binding.TypedBoundProgram) map[string]*BindingUsage {
	usages := map[string]*BindingUsage{}
	if prog == nil || prog.Program == nil {
		return usages
	}
	v := &usageVisitor{
		usages:     usages,
		skipIdent:  map[*ast.IdentExpr]bool{},
		memberBase: map[*ast.IdentExpr]bool{},
	}
	// 入口函数与子函数分别挂在 Program 的两个字段上（`Entry()` vs `Functions()`）——
	// 只扫 `Functions()` 会漏掉整个入口体（实测：账单为空）。两个都要扫。
	fns := prog.Program.Functions()
	if entry := prog.Program.Entry(); entry != nil {
		fns = append(append([]*ast.FunctionDef{}, fns...), entry)
	}
	for _, fn := range fns {
		if fn == nil || fn.Body() == nil {
			continue
		}
		ast.Walk(v, fn.Body())
	}
	return usages
}

type usageVisitor struct {
	usages      map[string]*BindingUsage
	skipIdent   map[*ast.IdentExpr]bool // 不是值读的标识符（callee 基 / 赋值目标）
	memberBase  map[*ast.IdentExpr]bool // 是成员链基的标识符
	branchDepth int
}

func (v *usageVisitor) child() *usageVisitor {
	c := *v
	c.branchDepth = v.branchDepth + 1
	return &c
}

func (v *usageVisitor) usage(name string) *BindingUsage {
	u := v.usages[name]
	if u == nil {
		u = &BindingUsage{Name: name}
		v.usages[name] = u
	}
	return u
}

// chainBase 剥到成员访问链的基（`a.b.c` / `xs[0].f` → `a` / `xs`）。
func chainBase(e ast.Expr) ast.Expr {
	for {
		switch x := e.(type) {
		case *ast.FieldAccessExpr:
			e = x.Obj()
		case *ast.IndexExpr:
			e = x.Obj()
		default:
			return e
		}
	}
}

func (v *usageVisitor) Visit(n ast.Node) ast.Visitor {
	switch x := n.(type) {
	case *ast.FunctionDef, *ast.CodeFunctionDef:
		return nil // 只扫体；函数/代码函数声明本身不是使用面
	case *ast.VarDeclStmt:
		if name := x.Name(); name != "" {
			u := v.usage(name)
			u.Declared = true
			if img := x.Init(); img != nil { // 零值声明（`T v;`）不是写点
				v.recordWrite(u, img, false)
			}
		}
		return v
	case *ast.AssignStmt:
		if id, ok := chainBase(x.Target()).(*ast.IdentExpr); ok {
			_, direct := x.Target().(*ast.IdentExpr)
			v.recordWrite(v.usage(id.Name()), x.Value(), !direct)
			v.skipIdent[id] = true // 目标是写，不是读
		}
		return v
	case *ast.CallExpr:
		// callee 不是值读：`fmt(x)` 的 `fmt`、`json.stringify(x)` 的 `json`。
		if id, ok := chainBase(x.Func()).(*ast.IdentExpr); ok {
			v.skipIdent[id] = true
		}
		return v
	case *ast.ForStmt:
		for _, name := range x.LoopVars() {
			if name != "" {
				v.usage(name).Declared = true
			}
		}
		return v.child()
	case *ast.FieldAccessExpr:
		v.markMemberBase(x.Obj())
		return v
	case *ast.IndexExpr:
		v.markMemberBase(x.Obj())
		return v
	case *ast.IdentExpr:
		if v.skipIdent[x] {
			return v
		}
		u := v.usage(x.Name())
		if v.memberBase[x] {
			u.MemberReads++
		} else {
			u.WholeReads++
		}
		return v
	case *ast.IfStmt, *ast.WhileStmt, *ast.AttemptStmt, *ast.ParallelStmt,
		*ast.SwitchStmt, *ast.HumanActionBranch, *ast.RequestInputStmt:
		return v.child() // 体内深度 +1（φ 的信号）
	}
	return v
}

// recordWrite 记一个写点并按值形态分类（`memberTarget` = 目标是 `v.f` 而不是裸 `v`）。
func (v *usageVisitor) recordWrite(u *BindingUsage, value ast.Expr, memberTarget bool) {
	u.Writes++
	if v.branchDepth > 0 {
		u.BranchWrites++
	}
	switch {
	case memberTarget:
		u.MemberWrites++
	case isRecordLiteral(value):
		u.LiteralWrites++
	default:
		u.OtherWrites++
	}
}

// isRecordLiteral 判「值是记录字面量」（§42.3 A 轴 P2 的形态面）。`{ "slot": x }` 与
// `{ slot: x }` 都是 `*ast.MapLit`；字面量里嵌套的值表达式不影响分类（P2 只要求**外层**是字面量）。
func isRecordLiteral(value ast.Expr) bool {
	if value == nil {
		return false
	}
	_, ok := value.(*ast.MapLit)
	return ok
}

// markMemberBase 把成员链的基标识符记为「成员读」。链上的中间节点（`a.b`）不是标识符，不受影响。
func (v *usageVisitor) markMemberBase(obj ast.Expr) {
	if id, ok := chainBase(obj).(*ast.IdentExpr); ok {
		v.memberBase[id] = true
	}
}
