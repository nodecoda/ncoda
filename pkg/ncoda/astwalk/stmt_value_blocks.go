package astwalk

// stmt_value_blocks.go —— **表达式附体语句块**（收窄树的另一半）。
//
// 为什么需要它（一手 2026-09-24 · R-52 剩源 `S93_jianbihua_en`）：
// ncoda 的 `parallel for` / `for` 是**表达式**（`let agg = parallel for (…) { … }`），
// 循环体是挂在 `ForExpr` 上的 Block，而**不是**容器语句。于是同一棵语句树上存在
// 两类子块：
//
//	① 容器子块 —— `stmtChildBlocks`（本仓容器递归唯一实现）；
//	② 表达式附体块 —— `ForExpr.Body`（`stmtValueBlocks` 走值位表达式找到）。
//
// 收窄（U18）的**两半**必须看见同一棵树：声明/赋值收窄（narrow_single_port_unit.go
// 的 rw）与读点塌陷（substituteMemberPathStmt）。历史上两半各走各的：塌陷侧经
// `rewriteMemberPathExpr` 的 `*ForExpr` 递归进了表达式体，收窄侧只走容器语句
// ⇒ **半塌陷**：体内读点塌成直读（`v.data` → `v`）、体内声明没塌（仍是整只记录）
// ⇒ 产物把记录赋给 `file<image>`（`TYPE_MISMATCH`，S93 一手）。
//
// 与 `stmt_children.go` 的同一条纪律（AGENTS.md「循环状态的收集必须子树闭包，禁止按
// 容器类型枚举」）：**两半同源**。判据 = narrow_single_port_unit_contract_test.go
// 的 `TestU18NarrowingReachesExpressionBody`（撤 `stmtSubtreeBlocks` 的表达式侧 ⇒ 红）。
//
// 边界：本函数只回答「这条语句的值位表达式里挂着哪些语句块」，不回答「哪些语句有
// 值位以外的语义」；值位集合与 `substituteMemberPathStmt` 的列举**同集**（漏一种 ⇒
// 该语句的值位里的表达式体不进收窄树）。
func stmtValueBlocks(st Stmt) []*Block {
	switch v := st.(type) {
	case *VarDecl:
		return exprChildBlocks(v.Init)
	case *AssignStmt:
		return exprChildBlocks(v.Value)
	case *ExprStmt:
		return exprChildBlocks(v.Expr)
	case *Yield:
		return exprChildBlocks(v.Value)
	case *Return:
		return exprChildBlocks(v.Value)
	case *OutputStmt:
		return exprChildBlocks(v.Value)
	case *If:
		return exprChildBlocks(v.Cond)
	case *While:
		return exprChildBlocks(v.Cond)
	case *For:
		return exprChildBlocks(v.Iterable)
	case *Switch:
		return exprChildBlocks(v.Query)
	case *AttemptStmt:
		return exprChildBlocks(v.Operation)
	case *RequestInputStmt:
		// content 的值位（`ask<F>({content: template "…" using {…}})`）：与
		// *Return 的 Value 同集 —— 漏 ⇒ 名额值里挂的 `parallel for` 体不进
		// 收窄树（半塌陷）。
		return exprChildBlocks(v.Content)
	case *Block, *ParallelStmtStmt:
		// 容器：本层**无值位**（子块由 stmtChildBlocks 负责；两类子块在
		// stmtSubtreeBlocks 合流）。
	}
	// 其余语句（叶语句 Break/Continue/Comment/ImportStmt/TypeDecl/EnumDecl/
	// SubflowDecl/SwitchBranch）**没有值位** ⇒ 无表达式附体块。
	return nil
}

// exprChildBlocks 返回表达式树里**附带的语句块**（今天唯一形态 = `ForExpr.Body`）。
//
// **逐形态穷尽**（本包 15 种 Expr 形态；漏一种 ⇒ 该形态里的表达式体不进收窄树 ⇒
// 两半不对称）。判据 = member_path_substitute_census_test.go（exprCollectors 表）。
func exprChildBlocks(e Expr) []*Block {
	if e == nil {
		return nil
	}
	switch v := e.(type) {
	case *IdentExpr, *LiteralExpr:
		// 叶子：无子表达式、无附体块。
	case *MemberExpr:
		return exprChildBlocks(v.Obj)
	case *IndexExpr:
		return append(exprChildBlocks(v.Obj), exprChildBlocks(v.Index)...)
	case *CallExpr:
		out := exprChildBlocks(v.Receiver)
		for i := range v.Args {
			out = append(out, exprChildBlocks(v.Args[i])...)
		}
		return out
	case *OperationPolicyExpr:
		return append(exprChildBlocks(v.Base), exprChildBlocks(v.Default)...)
	case *DictExpr:
		var out []*Block
		for i := range v.Values {
			out = append(out, exprChildBlocks(v.Values[i])...)
		}
		return out
	case *ListExpr:
		var out []*Block
		for i := range v.Items {
			out = append(out, exprChildBlocks(v.Items[i])...)
		}
		return out
	case *BinaryExpr:
		return append(exprChildBlocks(v.Left), exprChildBlocks(v.Right)...)
	case *UnaryExpr:
		return exprChildBlocks(v.Operand)
	case *TernaryExpr:
		out := append(exprChildBlocks(v.Cond), exprChildBlocks(v.Then)...)
		return append(out, exprChildBlocks(v.Else)...)
	case *TemplateString:
		var out []*Block
		for i := range v.Parts {
			out = append(out, exprChildBlocks(v.Parts[i].Expr)...)
		}
		return out
	case *TemplateValue:
		var out []*Block
		for i := range v.Bindings {
			out = append(out, exprChildBlocks(v.Bindings[i].Value)...)
		}
		return out
	case *LambdaExpr:
		return exprChildBlocks(v.Body)
	case *ForExpr:
		out := exprChildBlocks(v.Iterable)
		for i := range v.Sources {
			out = append(out, exprChildBlocks(v.Sources[i])...)
		}
		if v.Body != nil {
			out = append(out, v.Body)
		}
		return out
	case *ForeignCodeExpr:
		var out []*Block
		for i := range v.Inputs {
			out = append(out, exprChildBlocks(v.Inputs[i].Expr)...)
		}
		if v.Source != nil {
			for i := range v.Source.Parts {
				out = append(out, exprChildBlocks(v.Source.Parts[i].Expr)...)
			}
		}
		return out
	}
	return nil
}

// stmtSubtreeBlocks 返回一条语句的**全部**子块：容器子块 ∪ 表达式附体块。
//
// 收窄（U18）的两半与任何「按语句树改写」的 pass 必须用它 —— 只用 stmtChildBlocks
// 会漏掉 `parallel for` 体（S93 一手）。
func stmtSubtreeBlocks(st Stmt) []*Block {
	out := stmtChildBlocks(st)
	return append(out, stmtValueBlocks(st)...)
}
