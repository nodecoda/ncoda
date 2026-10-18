package passes

// LoopStatePass proves ownership and initialization for mutable NodeCoda
// loop state, ported 1:1 from src/nclang/lang/passes/loop_state_pass.py
// (154 lines):
//
//	_Write               :28-33    writeEntry (unexported)
//	LoopStatePass.run    :38-79
//	_collect_writes      :81-110
//	_statement_positions :112-116
//	_nodes               :118-130
//
// E1058 INVALID_STATE message is byte-for-byte the Python constant
// (loop_state_pass.py:66-68).
//
// D1 (conversation vars): Python consults ctx.conversation_var_ids
// (loop_state_pass.py:105); Go reads Context.ConversationVarIDs, published
// by ScopePass from the binder's snapshot set.
//
// Recorded deviation (2026-09-19, user ruling): _collect_writes' in_parallel
// bit is no longer consumed here — 并行分支写「体外声明」的绑定是**普通写**
// （合法，见 AGENTS.md P5 2026-09-19 拍板）；跨分支冲突由 ParallelAnalysisPass
// 的 E1036 PARALLEL_ACCESS_HAZARD 判定（旧实现让该情形借本 pass 的 loop-state
// 文案与判据发射 = 搭便车，文案还指向一个不存在的 while）。while-owner 传播与
// 其余规则逐字保留。
//
// D3 (snapshot-only consumption): the pass reads ctx.Bindings (the
// published snapshot) — stmt_symbols=DeclarationSymbols(),
// expr_symbols=ReferenceSymbols(), ast_id=NodeIDOf,
// block_scopes=ScopeBindings() — and never re-runs Bind. The Python
// scope_id(owner_scope) indirection collapses to the direct ScopeID the
// snapshot already carries (owner_scope_id = ScopeBindings[owner.body]).

import (
	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/types"
)

// LoopStatePass publishes LoopStateFacts only when one mutable binding has
// one literal initializer and one owning while loop, mirroring
// loop_state_pass.py:38-79.
type LoopStatePass struct{}

// Run collects every assignment, validates the state contract per symbol
// and publishes Context.LoopStates keyed by the owning while loop's
// AstNodeID.
func (LoopStatePass) Run(program *ast.Program, ctx *Context) {
	snap := ctx.Bindings

	// Python declarations = {symbol_id: node} over every VarDeclStmt that
	// has a declaration symbol (stmt_symbols). Go: DeclarationSymbols.
	declarations := map[binding.SymbolID]*ast.VarDeclStmt{}
	ast.Walk(&varDeclCollector{snap: snap, out: declarations}, program)

	positions := statementPositions(program)
	writes := collectWrites(program, snap, ctx.ConversationVarIDs)

	// bySymbol groups writes per symbol, preserving first-write order
	// (Python by_symbol dict insertion order).
	bySymbol := map[binding.SymbolID][]writeEntry{}
	bySymbolOrder := []binding.SymbolID{}
	for _, write := range writes {
		if _, seen := bySymbol[write.symbolID]; !seen {
			bySymbolOrder = append(bySymbolOrder, write.symbolID)
		}
		bySymbol[write.symbolID] = append(bySymbol[write.symbolID], write)
	}

	facts := map[binding.AstNodeID][]binding.LoopStateFact{}
	for _, symbolID := range bySymbolOrder {
		symbolWrites := bySymbol[symbolID]
		declaration := declarations[symbolID]
		// 无初值的可变态量声明（`var x;` 分支提升、`string agg_x;` 分支聚合
		// 变量）不是可变 workflow state —— 其赋值只是分支内节点物化/聚合
		// 提取，跳过 state 校验（否则循环体内的提升/聚合模式误报
		// INVALID_STATE）。状态变量必须带字面量初值（声明时），语义与
		// "one literal initializer before its owning while loop" 一致。
		if declaration != nil && declaration.Init() == nil && declaration.IsMutable() {
			continue
		}

		// owners: the unique innermost while owners across the writes
		// (Python dict keyed by id(while_owners[-1])).
		ownerSet := map[*ast.WhileStmt]bool{}
		for _, write := range symbolWrites {
			if len(write.whileOwners) > 0 {
				ownerSet[write.whileOwners[len(write.whileOwners)-1]] = true
			}
		}
		// 规则域（A 级，2026-09-19 用户拍板）：本条规则的**前提**是「该符号被某个
		// while 拥有」（文案自己写的就是 "before its owning while loop"）。
		// 无 owning while ⇒ 不是可变 workflow state ⇒ 本条**不表态**，由 IR 的
		// 通用规则处理普通可变本地（emitAssign 的可寻址本地分派）。
		// 旧实现把选择谓词写成「可变 ∧ 声明处有字面量初值」——**宽于**规则域
		// ⇒ 没有 while 的程序也报 E1058，文案却指向一个**不存在的** while
		// （把必要条件写成了存在性断言）。实测：`string v = ""; v = "A"; return v;`
		// （无 if 无 while）同样被拒。
		if len(ownerSet) == 0 {
			continue
		}

		var owner *ast.WhileStmt
		if len(ownerSet) == 1 {
			for candidate := range ownerSet {
				owner = candidate
			}
		}
		invalid := declaration == nil || len(ownerSet) != 1
		for _, write := range symbolWrites {
			if len(write.whileOwners) == 0 {
				invalid = true
			}
		}

		if !invalid {
			declarationPosition, hasDeclPosition := positions[declaration]
			ownerPosition, hasOwnerPosition := positions[owner]
			invalid = !hasDeclPosition || !hasOwnerPosition ||
				declarationPosition.block != ownerPosition.block ||
				declarationPosition.index >= ownerPosition.index ||
				!isLiteralInitializer(declaration.Init())
		}

		if invalid || owner == nil || declaration == nil {
			first := symbolWrites[0].statement
			ctx.Emit(diag.INVALID_STATE, diag.Error,
				"Mutable workflow state must have one literal initializer before its owning while loop and may be written only by that loop",
				first.Span())
			continue
		}

		ownerID := mustNodeID(snap, owner)
		bodyID := mustNodeID(snap, owner.Body())
		ownerScope, found := snap.ScopeBindings().Get(bodyID)
		if !found {
			panic("passes: loop body has no published scope")
		}
		writeSites := make([]binding.AstNodeID, 0, len(symbolWrites))
		for _, write := range symbolWrites {
			writeSites = append(writeSites, mustNodeID(snap, write.statement))
		}
		declID := mustNodeID(snap, declaration)
		fact := binding.LoopStateFact{
			SymbolID:      symbolID,
			OwnerScopeID:  ownerScope,
			InitializerID: declID,
			InitialValue:  literalValue(declaration.Init()),
			TypeInfo:      symbolTypeInfo(ctx, symbolID),
			WriteSites:    writeSites,
		}
		facts[ownerID] = append(facts[ownerID], fact)
	}

	ctx.LoopStates = facts
}

// varDeclCollector collects every VarDeclStmt node that declares a symbol
// (Python _nodes(program) filtered by stmt_symbols).
type varDeclCollector struct {
	snap *binding.Snapshot
	out  map[binding.SymbolID]*ast.VarDeclStmt
}

func (c *varDeclCollector) Visit(n ast.Node) ast.Visitor {
	if decl, ok := n.(*ast.VarDeclStmt); ok {
		id, found := c.snap.NodeIDOf(decl)
		if found {
			if symbolID, has := c.snap.DeclarationSymbols().Get(id); has {
				c.out[symbolID] = decl
			}
		}
	}
	return c
}

// writeEntry mirrors the frozen _Write dataclass (loop_state_pass.py:28-33).
type writeEntry struct {
	statement   *ast.AssignStmt
	symbolID    binding.SymbolID
	whileOwners []*ast.WhileStmt
}

// stmtPosition is one statement's (block, index) slot (Python
// positions[id(stmt)] = (node, index), loop_state_pass.py:112-116).
type stmtPosition struct {
	block *ast.Block
	index int
}

// statementPositions maps every statement node to its enclosing block and
// index, mirroring _statement_positions (loop_state_pass.py:112-116).
func statementPositions(program *ast.Program) map[ast.Node]stmtPosition {
	positions := map[ast.Node]stmtPosition{}
	c := &blockScanner{out: positions}
	ast.Walk(c, program)
	return positions
}

// blockScanner records each statement's slot when visiting a Block.
type blockScanner struct {
	out map[ast.Node]stmtPosition
}

func (c *blockScanner) Visit(n ast.Node) ast.Visitor {
	if block, ok := n.(*ast.Block); ok {
		for index, stmt := range block.Stmts() {
			c.out[stmt] = stmtPosition{block: block, index: index}
		}
	}
	return c
}

// writeCollector mirrors _collect_writes' visit closure
// (loop_state_pass.py:82-108): it threads the enclosing while owners and
// the in-parallel flag through the traversal, prunes ParallelForExpr
// subtrees entirely, and records AssignStmt writes to non-conversation
// symbols.
type writeCollector struct {
	snap   *binding.Snapshot
	conv   map[binding.SymbolID]bool
	out    *[]writeEntry
	owners []*ast.WhileStmt
}

// Visit records writes and returns the state-carrying visitor for the
// children (Python's next_owners / next_parallel propagation).
func (c *writeCollector) Visit(n ast.Node) ast.Visitor {
	switch n := n.(type) {
	case *ast.ParallelForExpr:
		return nil // pruned (loop_state_pass.py:104)
	case *ast.AssignStmt:
		if target, ok := n.Target().(*ast.IdentExpr); ok {
			id, found := c.snap.NodeIDOf(target)
			if found {
				if symbolID, has := c.snap.ReferenceSymbols().Get(id); has && !c.conv[symbolID] {
					*c.out = append(*c.out, writeEntry{
						statement:   n,
						symbolID:    symbolID,
						whileOwners: append([]*ast.WhileStmt(nil), c.owners...),
					})
				}
			}
		}
	}

	next := *c // shallow copy: shares the out pointer, replaces owners below
	switch n := n.(type) {
	case *ast.WhileStmt:
		next.owners = append(append([]*ast.WhileStmt(nil), c.owners...), n)
	case *ast.ForStmt, *ast.ForExpr:
		// Python resets next_owners to the incoming owners: a for loop
		// never adds itself as an owner, but writes inside a for nested in
		// a while still carry the enclosing while.
		next.owners = append([]*ast.WhileStmt(nil), c.owners...)
	}
	return &next
}

// collectWrites returns every recorded assignment in traversal order,
// mirroring _collect_writes (loop_state_pass.py:81-110).
func collectWrites(program *ast.Program, snap *binding.Snapshot, conversationVarIDs map[binding.SymbolID]bool) []writeEntry {
	out := []writeEntry{}
	c := &writeCollector{snap: snap, conv: conversationVarIDs, out: &out}
	ast.Walk(c, program)
	return out
}

// isLiteralInitializer reports whether init is a String/Int/Float/Bool
// literal (loop_state_pass.py:57-60).
func isLiteralInitializer(init ast.Expr) bool {
	switch init.(type) {
	case *ast.StringLit, *ast.IntLit, *ast.FloatLit, *ast.BoolLit:
		return true
	}
	return false
}

// literalValue returns the literal's Python-equivalent value
// (declaration.init.value, loop_state_pass.py:73). The Go value types
// (int64/float64/string/bool) mirror the Python str|int|float|bool union.
func literalValue(init ast.Expr) any {
	switch lit := init.(type) {
	case *ast.StringLit:
		return lit.Value()
	case *ast.IntLit:
		return lit.Value()
	case *ast.FloatLit:
		return lit.Value()
	case *ast.BoolLit:
		return lit.Value()
	}
	panic("passes: literalValue on non-literal initializer")
}

// symbolTypeInfo returns the declared symbol's current type: the VarTypes
// mutation surface first (Python stmt_symbols[decl_id].type_info after
// TypePass mutations), then the snapshot's frozen declaration type.
// symbolID always comes from the pass's declarations map (symbols with a
// VarDeclStmt declaration record), so the declaration lookup cannot miss —
// Python's equivalent is a KeyError-raising dict read
// (loop_state_pass.py:74-75); the Go form panics loudly on the same
// invariant violation instead of falling back to any.
func symbolTypeInfo(ctx *Context, symbolID binding.SymbolID) *types.Descriptor {
	if inferred, ok := ctx.VarTypes[symbolID]; ok {
		return inferred
	}
	decl, ok := ctx.Bindings.Declarations().Get(symbolID)
	if !ok {
		panic("passes: loop-state symbol has no declaration record")
	}
	return decl.Type
}
