package astwalk

// stmt_children.go —— **容器语句的直接子块**（cd-AST 树递归的唯一实现）。
//
// 为什么只能有一处：本包的 cd-AST 有 20+ 种语句类型，历史上每个遍历器都自己
// `switch st.(type)` 逐类型列举容器，于是「新增一种容器语句」时，凡没被列举的
// 遍历器都**静默漏看**它。2026-09-22 一手实测的后果（J §8 U2 的收口面）：
// `hoistEscapedBranchVars` 的**收集**段进了 `For` 而**重写**段不进，且两段都
// 漏 `While` / `AttemptStmt` ⇒
//   · 漏收集 ⇒ 声明被判「不逃逸」⇒ 容器外的引用无声明（首编 UNDEFINED_VAR）；
//   · 收集进、重写不进 ⇒ 容器外多一份零值声明、容器内原件仍在 ⇒ **同名两份**
//     （内层遮蔽外层 ⇒ 容器外读到的永远是零值 = 值静默丢失）。
// 两条判据见 hoist_escape_subtree_contract_test.go。
//
// 规则依据 = AGENTS.md「循环状态的收集必须子树闭包，禁止按容器类型枚举」
// （不可违背）。故这里的列举是全包**唯一**一处，并由
// stmt_walker_census_test.go 的 G1/G3 钉住：新增容器类型不在这里登记 ⇒ 判据红。
//
// 返回 nil 表示该语句没有子块；调用方**不得**假设返回的块非 nil（nil 块已被
// 过滤掉，见 appendNonNilBlock）。

func stmtChildBlocks(st Stmt) []*Block {
	switch v := st.(type) {
	case *Block:
		return []*Block{v}
	case *If:
		out := make([]*Block, 0, 2)
		out = appendNonNilBlock(out, v.Then)
		out = appendNonNilBlock(out, v.Else)
		return out
	case *While:
		return appendNonNilBlock(nil, v.Body)
	case *For:
		return appendNonNilBlock(nil, v.Body)
	case *ParallelStmtStmt:
		out := make([]*Block, 0, len(v.Blocks))
		for _, blk := range v.Blocks {
			out = appendNonNilBlock(out, blk)
		}
		return out
	case *Switch:
		out := make([]*Block, 0, len(v.Branches)+1)
		for _, br := range v.Branches {
			out = appendNonNilBlock(out, br.Body)
		}
		return appendNonNilBlock(out, v.DefaultBody)
	case *AttemptStmt:
		out := make([]*Block, 0, 2)
		out = appendNonNilBlock(out, v.Success)
		return appendNonNilBlock(out, v.Failure)
	case *RequestInputStmt:
		out := make([]*Block, 0, len(v.ActionBranches)+1)
		for _, br := range v.ActionBranches {
			out = appendNonNilBlock(out, br.Body)
		}
		return appendNonNilBlock(out, v.TimeoutBody)
	}
	return nil
}

func appendNonNilBlock(out []*Block, blk *Block) []*Block {
	if blk != nil {
		out = append(out, blk)
	}
	return out
}

// loopScopeStmt 报告该语句是否是**自成一体的循环作用域**（For / While）。
//
// 语义（不是实现细节）：循环体是它自己的作用域边界 —— 体内声明由容器边界自己
// 规范化（体内序列由 generateSequence 生成，其自身的提升调用已经跑过；跨容器
// 可见的循环值走**循环状态**通道：`variableParameters` 在容器外声明）。**外层**
// 的提升遍历不得进体内搬声明：2026-09-22 一手实测（语料 X22/X23 batch 族 3 源）
// 让重写侧进 `For` 体后新增 `MATERIALIZED_GROWTH@r2` = 收敛恶化。
//
// 该判定被**收集与重写两侧同时**使用（同源），故不会出现「收集进、重写不进」
// 的不对称（那正是本文件要消灭的缺陷族）。
func loopScopeStmt(st Stmt) bool {
	switch st.(type) {
	case *For, *While:
		return true
	}
	return false
}

// stmtBindings 返回语句在**本作用域**绑定/声明的名字（各体内部的局部名由递归
// 到该体时另收）。与 stmtChildBlocks 同源：两半合起来才是「语句树的完整形状」
// —— 遍历器用之即自动覆盖新增语句类型。
//
// `attempt … as r`（AttemptStmt.Result）与 `ask<F,A>(…) as r`
// （RequestInputStmt.ResultBinding）的绑定属于本作用域（该语句之后的兄弟语句
// 能读它），故登记；`failure(e)` 的 e 只在其体内可见，不登记。
//
// 一手依据（2026-09-22 S225）：branchVarNames 曾按类型枚举且漏 AttemptStmt
// ⇒ 臂内「重试调用 → 汇聚候选」被判「本臂无源」⇒ 条件汇聚整组不提升 ⇒
// RS-MRG-001（形状合法却报红）。
func stmtBindings(st Stmt) []string {
	switch v := st.(type) {
	case *VarDecl:
		return []string{v.Name}
	case *AttemptStmt:
		if v.Result != "" {
			return []string{v.Result}
		}
	case *RequestInputStmt:
		if v.ResultBinding != "" {
			return []string{v.ResultBinding}
		}
	}
	return nil
}

// stmtResultBinding 返回语句**自身**的结果绑定名 —— 该名字的右值就是这条语句
// 的结果（`attempt <op> as r { … }`、`ask<F,A>(…) as r { … }`）。
//
// 与 stmtBindings 的分工：stmtBindings = 本作用域**声明了哪些名字**（含 VarDecl）；
// 本函数 = 「这条语句把结果写进了哪个名字」—— 赋值落点算法（appendBranchAssign
// 的定义点判定）用它。缺它 ⇒ 值由 attempt 绑定的候选在臂内**找不到定义点**
// ⇒ 赋值静默不发射（2026-09-22 S225 一手实证：臂尾 attempt 的重试值整条丢失）。
//
// VarDecl/AssignStmt 的结果位有各自的落点规则（裸声明 / 零值 hoist 对等），
// 不走这里。
func stmtResultBinding(st Stmt) string {
	switch v := st.(type) {
	case *AttemptStmt:
		return v.Result
	case *RequestInputStmt:
		return v.ResultBinding
	}
	return ""
}
