package astwalk

// 闭源侧仍按历史裸名调用；在此导出稳定入口（实现不变）。

func StmtChildBlocks(st Stmt) []*Block { return stmtChildBlocks(st) }

func LoopScopeStmt(st Stmt) bool { return loopScopeStmt(st) }

func StmtBindings(st Stmt) []string { return stmtBindings(st) }

func StmtResultBinding(st Stmt) string { return stmtResultBinding(st) }

func ExprChildBlocks(e Expr) []*Block { return exprChildBlocks(e) }

func StmtValueBlocks(st Stmt) []*Block { return stmtValueBlocks(st) }

func StmtSubtreeBlocks(st Stmt) []*Block { return stmtSubtreeBlocks(st) }
