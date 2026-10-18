package passes

// ParallelAnalysisPass merges the reference ParallelAccessCheckPass and
// ParallelExportCheckPass into one AST traversal, ported 1:1 from
// src/nclang/lang/passes/parallel_analysis.py (428 lines):
//
//	ParallelSymbolAccess      :48-58   ParallelSymbolAccess
//	ParallelExitAccess        :60-68   ParallelExitAccess
//	ParallelBranchFacts       :70-96   ParallelBranchFacts
//	ParallelFacts             :98-107  ParallelFacts
//	ParallelAnalysisPass.run  :114-118
//	visit_parallel_stmt       :121-133
//	visit_parallel_for_expr   :136-149
//	visit_parallel_expr       :152-163
//	analyze_parallel_stmt     :179-184
//	analyze_parallel_blocks   :186-201
//	_emit_export_conflicts    :211-260
//	_BranchAccessCollector    :269-333
//	_assignment_root          :335-341
//	_scope_is_within          :343-351
//	_is_candidate_export      :353-363
//	_emit_hazards             :366-407
//	_first_site               :409-412
//	_format_location          :414-416
//
// E1036 PARALLEL_ACCESS_HAZARD and E1035 PARALLEL_EXPORT_CONFLICT messages
// are byte-for-byte the Python f-string results (see the emitter functions).
//
// Deviation (recorded): the collector and pass traversal use ast.Walk, which
// visits a superset of the Python ProcASTVisitor child coverage (params,
// return types, call type_args, policies, const-decl values, ...). No
// symbol-referencing construct that matters to parallel analysis lives in
// those extra children (parallel constructs appear only in function bodies),
// so the observable surface is identical for reachable programs.
//
// Immutability (D3): Python dataclasses are frozen (slots=True); Go has no
// FrozenInstanceError, so the fact AGGREGATE structs (ParallelFacts /
// ParallelBranchFacts) keep their fields unexported and expose read-only
// accessors that return fresh slice copies; the leaf access structs
// (ParallelSymbolAccess / ParallelExitAccess) keep exported value fields
// and are isolated from mutation by those accessor copies. Set views
// (read_symbols / write_symbols / exit_targets / exports) are Python
// frozensets; Go renders them as sorted, de-duplicated slices so fact
// equality and diagnostics are deterministic.

import (
	"sort"
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
)

// ParallelSymbolAccess is one stable read or write site in a parallel
// branch, mirroring the frozen ParallelSymbolAccess dataclass
// (parallel_analysis.py:48-58). A zero Span plays the role of the Python
// None span.
type ParallelSymbolAccess struct {
	SymbolID binding.SymbolID
	NodeID   binding.AstNodeID
	Span     ast.Span
}

// ParallelExitAccess is one bound control exit observed in a parallel
// branch, mirroring the frozen ParallelExitAccess dataclass
// (parallel_analysis.py:60-68).
type ParallelExitAccess struct {
	Target binding.ExitTarget
	NodeID binding.AstNodeID
	Span   ast.Span
}

// ParallelBranchFacts holds the immutable R/W/E facts computed from one
// branch entry snapshot, mirroring the frozen ParallelBranchFacts dataclass
// (parallel_analysis.py:70-96). Fields are unexported; accessors return
// copies so no mutation path survives construction (Go's form of the Python
// frozen contract — see the header deviation note).
type ParallelBranchFacts struct {
	scopeID binding.ScopeID
	reads   []ParallelSymbolAccess
	writes  []ParallelSymbolAccess
	exits   []ParallelExitAccess
	exports []binding.SymbolID // frozenset, rendered sorted+de-duplicated
}

// ScopeID returns the branch scope (Python ParallelBranchFacts.scope_id).
func (f ParallelBranchFacts) ScopeID() binding.ScopeID { return f.scopeID }

// Reads returns a copy of the read accesses in traversal order (Python
// reads tuple).
func (f ParallelBranchFacts) Reads() []ParallelSymbolAccess {
	return append([]ParallelSymbolAccess(nil), f.reads...)
}

// Writes returns a copy of the write accesses in traversal order (Python
// writes tuple).
func (f ParallelBranchFacts) Writes() []ParallelSymbolAccess {
	return append([]ParallelSymbolAccess(nil), f.writes...)
}

// Exits returns a copy of the exit accesses in traversal order (Python
// exits tuple).
func (f ParallelBranchFacts) Exits() []ParallelExitAccess {
	return append([]ParallelExitAccess(nil), f.exits...)
}

// Exports returns the exported symbol IDs sorted and de-duplicated (Python
// exports frozenset, rendered sorted for Go determinism).
func (f ParallelBranchFacts) Exports() []binding.SymbolID {
	return append([]binding.SymbolID(nil), f.exports...)
}

// ReadSymbols returns the distinct read symbol IDs, sorted (Python
// ParallelBranchFacts.read_symbols frozenset property).
func (f ParallelBranchFacts) ReadSymbols() []binding.SymbolID {
	return sortedSymbolIDs(accessSymbols(f.reads))
}

// WriteSymbols returns the distinct write symbol IDs, sorted (Python
// ParallelBranchFacts.write_symbols frozenset property).
func (f ParallelBranchFacts) WriteSymbols() []binding.SymbolID {
	return sortedSymbolIDs(accessSymbols(f.writes))
}

// ExitTargets returns the distinct exit targets, sorted by (Kind, ScopeID)
// (Python ParallelBranchFacts.exit_targets frozenset property,
// parallel_analysis.py:92-94). The full ExitTarget identity — kind AND the
// bound target scope — is preserved, so two breaks bound to different loop
// scopes stay distinct (a kind-only view would collapse them).
func (f ParallelBranchFacts) ExitTargets() []binding.ExitTarget {
	set := map[binding.ExitTarget]bool{}
	for _, e := range f.exits {
		set[e.Target] = true
	}
	out := make([]binding.ExitTarget, 0, len(set))
	for target := range set {
		out = append(out, target)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].ScopeID < out[j].ScopeID
	})
	return out
}

// ParallelFacts holds the stable facts for all branches of one parallel
// construct, mirroring the frozen ParallelFacts dataclass
// (parallel_analysis.py:98-107).
type ParallelFacts struct {
	nodeID   binding.AstNodeID
	branches []ParallelBranchFacts
}

// NodeID returns the owning node's AstNodeID (Python ParallelFacts.node_id).
func (f ParallelFacts) NodeID() binding.AstNodeID { return f.nodeID }

// Branches returns a copy of the per-branch facts (Python branches tuple).
func (f ParallelFacts) Branches() []ParallelBranchFacts {
	return append([]ParallelBranchFacts(nil), f.branches...)
}

// accessSymbols extracts the distinct symbol IDs from a symbol-access slice.
func accessSymbols(accesses []ParallelSymbolAccess) []binding.SymbolID {
	set := map[binding.SymbolID]bool{}
	for _, a := range accesses {
		set[a.SymbolID] = true
	}
	out := make([]binding.SymbolID, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	return out
}

// sortedSymbolIDs returns a sorted copy of ids.
func sortedSymbolIDs(ids []binding.SymbolID) []binding.SymbolID {
	out := append([]binding.SymbolID(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ParallelAnalysisPass runs the E1036 access-hazard + E1035 export-conflict
// checks in one traversal, mirroring ParallelAnalysisPass
// (parallel_analysis.py:101-163). Facts is the collected per-construct
// publication (Python ParallelAnalysisPass.facts, reset per run).
type ParallelAnalysisPass struct {
	Facts []ParallelFacts
}

// Run visits program and appends the parallel facts and diagnostics,
// mirroring ParallelAnalysisPass.run (parallel_analysis.py:114-118).
func (p *ParallelAnalysisPass) Run(program *ast.Program, ctx *Context) {
	p.Facts = nil
	snap := ctx.Bindings
	t := &parallelTraverser{pass: p, ctx: ctx, snap: snap, mustIDFn: mustNodeIDFn(snap)}
	ast.Walk(t, program)
	// 第二遍：`parallel for` 迭代体作用域语义（S3 迭代局部写 / S4 控制转移，
	// D-L1..D-L3，用户拍板 2026-09-14）——见 parallel_iteration_local.go。
	if snap != nil {
		analyzeParallelForIterationScope(program, ctx, snap, t.mustIDFn)
	}
}

// parallelTraverser is the pass's AST visitor: the three parallel node
// handlers mirror visit_parallel_stmt / visit_parallel_for_expr /
// visit_parallel_expr; every other node continues the ast.Walk traversal
// (the Python generic_visit/visit_* surface — see the header deviation).
type parallelTraverser struct {
	pass     *ParallelAnalysisPass
	ctx      *Context
	snap     *binding.Snapshot
	mustIDFn func(ast.Node) binding.AstNodeID
}

// mustNodeIDFn adapts the shared mustNodeID helper (which takes the snapshot)
// into the closure shape the iteration-scope scanner takes.
func mustNodeIDFn(snap *binding.Snapshot) func(ast.Node) binding.AstNodeID {
	return func(n ast.Node) binding.AstNodeID { return mustNodeID(snap, n) }
}

// mustID resolves n's AstNodeID, panicking when the node was not enumerated
// (Python ctx.ast_id; every visited node is bound by the scope pass).
func (t *parallelTraverser) mustID(n ast.Node) binding.AstNodeID {
	return t.mustIDFn(n)
}

func (t *parallelTraverser) Visit(n ast.Node) ast.Visitor {
	switch n := n.(type) {
	case *ast.ParallelStmt:
		// visit_parallel_stmt (parallel_analysis.py:121-133): analyze, emit
		// E1036 hazards, emit E1035 export conflicts, then continue into
		// the branch blocks (nested parallels are found recursively).
		facts := analyzeParallelStmt(n, t.snap, t.mustID)
		t.pass.Facts = append(t.pass.Facts, facts)
		emitHazards(facts, t.snap, t.ctx)
		emitExportConflicts(n, facts, t.snap, t.ctx)
		return t
	case *ast.ParallelForExpr:
		// 语言级改动（D-L1(a)，2026-09-14 用户拍板）：旧规则「迭代体内写共享
		// 变量 = E1036」被判定**太严**并废止 —— 迭代体的写是**迭代局部、不逃逸**
		// （死写无害、按迭代收集是最自然的写法）。新判据（逃逸读 E1036 / 迭代体
		// 内 break·return E1037）在 analyzeParallelForIterationScope 里对**语句
		// 形态与表达式形态同一把尺子**实现，本分支只负责继续递归。
	}
	return t
}

// analyzeParallelStmt computes deterministic branch facts without consulting
// mutable scope state, mirroring analyze_parallel_stmt
// (parallel_analysis.py:179-184).
func analyzeParallelStmt(node *ast.ParallelStmt, snap *binding.Snapshot, nodeID func(ast.Node) binding.AstNodeID) ParallelFacts {
	return analyzeParallelBlocks(nodeID(node), node.Branches(), snap, nodeID)
}

// analyzeParallelBlocks computes one ParallelBranchFacts per branch block,
// mirroring analyze_parallel_blocks (parallel_analysis.py:186-201).
func analyzeParallelBlocks(nodeID binding.AstNodeID, blocks []*ast.Block, snap *binding.Snapshot, nodeIDFn func(ast.Node) binding.AstNodeID) ParallelFacts {
	branches := make([]ParallelBranchFacts, 0, len(blocks))
	for _, branch := range blocks {
		branchID := nodeIDFn(branch)
		scopeID, found := snap.ScopeBindings().Get(branchID)
		if !found {
			panic("passes: parallel branch has no published scope")
		}
		collector := newBranchAccessCollector(snap, nodeIDFn, scopeID)
		collector.collect(branch)

		// Python iterates snapshot.declaration_symbols.items() (insertion
		// order); Go sorts the declaration node IDs so the frozenset
		// construction is deterministic regardless of map iteration order.
		declarationIDs := snap.DeclarationSymbols().Keys()
		sort.Slice(declarationIDs, func(i, j int) bool { return declarationIDs[i] < declarationIDs[j] })
		exports := []binding.SymbolID{}
		for _, declarationID := range declarationIDs {
			symbolID, _ := snap.DeclarationSymbols().Get(declarationID)
			if !isCandidateExport(symbolID, scopeID, snap) {
				continue
			}
			info, ok := snap.AstNodes().Get(declarationID)
			if !ok || info.Kind != "VarDeclStmt" {
				continue
			}
			exports = append(exports, symbolID)
		}
		sort.Slice(exports, func(i, j int) bool { return exports[i] < exports[j] })

		branches = append(branches, ParallelBranchFacts{
			scopeID: scopeID,
			reads:   collector.reads,
			writes:  collector.writes,
			exits:   collector.exits,
			exports: exports,
		})
	}
	return ParallelFacts{nodeID: nodeID, branches: branches}
}

// declarationName resolves a symbol ID to its declaration name (Python
// snapshot.symbol_declarations[id].name).
func declarationName(snap *binding.Snapshot, symbolID binding.SymbolID) string {
	decl, found := snap.Declarations().Get(symbolID)
	if !found {
		panic("passes: symbol has no declaration record")
	}
	return decl.Name
}

// ── export conflict logic (parallel_export_check) ─────────────────────

// emitExportConflicts emits E1035 for duplicate exports from parallel
// branches with incompatible inferred types, mirroring
// _emit_export_conflicts (parallel_analysis.py:211-260). The message
// preserves Python's first-seen name order and the sorted concrete-type
// rendering; locations render stmt.span.start (Python str(SourceLocation)).
func emitExportConflicts(node *ast.ParallelStmt, facts ParallelFacts, snap *binding.Snapshot, ctx *Context) {
	// branchTypeMap: per branch, exported name -> (type string, span).
	type branchType struct {
		typeStr string
		span    ast.Span
	}
	branchTypeMap := make([]map[string]branchType, len(node.Branches()))

	for branchIndex, branch := range node.Branches() {
		branchTypes := map[string]branchType{}
		for _, stmt := range branch.Stmts() {
			decl, ok := stmt.(*ast.VarDeclStmt)
			if !ok || decl.Init() == nil {
				continue
			}
			declID, found := snap.NodeIDOf(decl)
			if !found {
				panic("passes: branch declaration not enumerated")
			}
			symbolID, hasSymbol := snap.DeclarationSymbols().Get(declID)
			if !hasSymbol || !containsSymbolID(facts.branches[branchIndex].exports, symbolID) {
				continue
			}
			initID := mustNodeID(snap, decl.Init())
			typeStr := "any"
			if resolved, ok := ctx.ExprTypes[initID]; ok {
				typeStr = resolved.String()
			}
			branchTypes[decl.Name()] = branchType{typeStr: typeStr, span: decl.Span()}
		}
		branchTypeMap[branchIndex] = branchTypes
	}

	// Collect all names exported from multiple branches, preserving
	// first-seen order (Python dict insertion order).
	type nameEntry struct {
		typeStr   string
		branchIdx int
		span      ast.Span
	}
	allNames := map[string][]nameEntry{}
	allNameOrder := []string{}
	for branchIdx, branchTypes := range branchTypeMap {
		for name, bt := range branchTypes {
			if _, seen := allNames[name]; !seen {
				allNameOrder = append(allNameOrder, name)
			}
			allNames[name] = append(allNames[name], nameEntry{typeStr: bt.typeStr, branchIdx: branchIdx, span: bt.span})
		}
	}

	for _, name := range allNameOrder {
		entries := allNames[name]
		if len(entries) < 2 {
			continue
		}
		types := map[string]bool{}
		for _, e := range entries {
			types[e.typeStr] = true
		}
		// R2: a name exported from multiple parallel branches is ambiguous
		// downstream — each branch has its OWN producer, so a bare-name
		// reference cannot resolve to a single value. This fires for ANY
		// same-name export (even same-typed): rename the branch locals.
		concrete := make([]string, 0, len(types))
		for typeStr := range types {
			concrete = append(concrete, typeStr)
		}
		sort.Strings(concrete)
		locations := make([]string, 0, len(entries))
		for _, e := range entries {
			locations = append(locations, formatBranchLocation(e.branchIdx+1, e.span))
		}
		ctx.Emit(diag.PARALLEL_EXPORT_CONFLICT, diag.Error,
			"Variable '"+name+"' is exported from multiple parallel branches; "+
				"rename the branch locals (ambiguous downstream reference): "+
				strings.Join(concrete, ", ")+"; "+strings.Join(locations, "; "), node.Span())
	}
}

// containsSymbolID reports whether ids contains id.
func containsSymbolID(ids []binding.SymbolID, id binding.SymbolID) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

// formatBranchLocation renders "branch N at <loc>" (Python f-string with
// span.start; a zero span plays the Python None -> '<unknown>').
func formatBranchLocation(branchIdx int, span ast.Span) string {
	location := "<unknown>"
	if span != (ast.Span{}) {
		location = span.Start.String()
	}
	return "branch " + strconv.Itoa(branchIdx) + " at " + location
}

// ── access hazard internals (parallel_access_check) ───────────────────

// branchAccessCollector mirrors _BranchAccessCollector
// (parallel_analysis.py:269-333): one pass over a branch block recording
// stable reads, writes and bound exits.
type branchAccessCollector struct {
	snap          *binding.Snapshot
	nodeIDFn      func(ast.Node) binding.AstNodeID
	branchScopeID binding.ScopeID
	reads         []ParallelSymbolAccess
	writes        []ParallelSymbolAccess
	exits         []ParallelExitAccess
}

func newBranchAccessCollector(snap *binding.Snapshot, nodeIDFn func(ast.Node) binding.AstNodeID, branchScopeID binding.ScopeID) *branchAccessCollector {
	return &branchAccessCollector{snap: snap, nodeIDFn: nodeIDFn, branchScopeID: branchScopeID}
}

// collect walks the branch block, mirroring collector.visit(branch).
func (c *branchAccessCollector) collect(branch *ast.Block) {
	ast.Walk(c, branch)
}

// Visit implements the collection rules (visit_ident_expr /
// visit_assign_stmt / visit_return_stmt / visit_break_stmt /
// visit_continue_stmt).
func (c *branchAccessCollector) Visit(n ast.Node) ast.Visitor {
	switch n := n.(type) {
	case *ast.IdentExpr:
		c.recordSymbol(n, &c.reads)
		return c
	case *ast.AssignStmt:
		// visit_assign_stmt: the target root is a write, assignment
		// indices are visited as reads, and the value subtree is visited;
		// the non-root obj chain is never visited (Python does not call
		// super().visit_assign_stmt).
		root := assignmentRoot(n.Target())
		if root == nil {
			ast.Walk(c, n.Target())
		} else {
			c.recordSymbol(root, &c.writes)
			c.visitAssignmentIndices(n.Target())
		}
		ast.Walk(c, n.Value())
		return nil // children handled manually above
	case *ast.ReturnStmt:
		c.recordExit(n)
		ast.Walk(c, n.Value())
		return nil
	case *ast.BreakStmt:
		c.recordExit(n)
		return nil
	case *ast.ContinueStmt:
		c.recordExit(n)
		return nil
	}
	return c
}

// recordSymbol mirrors _record_symbol (parallel_analysis.py:294-304): the
// node's reference symbol is appended to destination when it resolves to a
// shared data symbol.
func (c *branchAccessCollector) recordSymbol(node *ast.IdentExpr, destination *[]ParallelSymbolAccess) {
	nodeID := c.nodeIDFn(node)
	symbolID, found := c.snap.ReferenceSymbols().Get(nodeID)
	if !found || !c.isSharedDataSymbol(symbolID) {
		return
	}
	*destination = append(*destination, ParallelSymbolAccess{
		SymbolID: symbolID,
		NodeID:   nodeID,
		Span:     nodeInfoSpan(c.snap, nodeID),
	})
}

// recordExit mirrors _record_exit (parallel_analysis.py:306-315): the exit
// is recorded only when the snapshot bound an exit target for the node.
func (c *branchAccessCollector) recordExit(node ast.Node) {
	nodeID := c.nodeIDFn(node)
	target, found := c.snap.ExitTargets().Get(nodeID)
	if !found {
		return
	}
	c.exits = append(c.exits, ParallelExitAccess{
		Target: target,
		NodeID: nodeID,
		Span:   nodeInfoSpan(c.snap, nodeID),
	})
}

// isSharedDataSymbol mirrors _is_shared_data_symbol
// (parallel_analysis.py:317-326): VARIABLE/PARAMETER/INPUT symbols whose
// declaration scope is not within the branch scope.
func (c *branchAccessCollector) isSharedDataSymbol(symbolID binding.SymbolID) bool {
	declaration, found := c.snap.Declarations().Get(symbolID)
	if !found {
		panic("passes: referenced symbol has no declaration")
	}
	switch declaration.Kind {
	case binding.SymbolVariable, binding.SymbolParameter, binding.SymbolInput:
	default:
		return false
	}
	return !scopeIsWithin(declaration.Scope, c.branchScopeID, c.snap)
}

// visitAssignmentIndices mirrors _visit_assignment_indices
// (parallel_analysis.py:328-333): every IndexExpr index on the target's obj
// chain is visited; the chain is walked to its root.
func (c *branchAccessCollector) visitAssignmentIndices(target ast.Expr) {
	current := target
	for {
		switch t := current.(type) {
		case *ast.FieldAccessExpr:
			current = t.Obj()
		case *ast.IndexExpr:
			ast.Walk(c, t.Index())
			current = t.Obj()
		default:
			return
		}
	}
}

// nodeInfoSpan returns the snapshot's span for a node (Python
// snapshot.ast_nodes[node_id].span).
func nodeInfoSpan(snap *binding.Snapshot, nodeID binding.AstNodeID) ast.Span {
	info, found := snap.AstNodes().Get(nodeID)
	if !found {
		return ast.Span{}
	}
	return info.Span
}

// mustNodeID resolves n's AstNodeID from the snapshot (Python ctx.ast_id).
func mustNodeID(snap *binding.Snapshot, n ast.Node) binding.AstNodeID {
	id, ok := snap.NodeIDOf(n)
	if !ok {
		panic("passes: node not enumerated")
	}
	return id
}

// assignmentRoot mirrors _assignment_root (parallel_analysis.py:335-341):
// the IdentExpr at the bottom of a FieldAccess/Index target chain, or nil.
func assignmentRoot(target ast.Expr) *ast.IdentExpr {
	current := target
	for {
		switch t := current.(type) {
		case *ast.FieldAccessExpr:
			current = t.Obj()
		case *ast.IndexExpr:
			current = t.Obj()
		default:
			if id, ok := current.(*ast.IdentExpr); ok {
				return id
			}
			return nil
		}
	}
}

// scopeIsWithin mirrors _scope_is_within (parallel_analysis.py:343-351):
// whether candidate is the ancestor scope or one of its descendants
// (walking scope_parents upward). A missing scope record is a snapshot
// invariant violation (every non-zero scope's parent is registered by the
// binder) and panics loudly, mirroring the Python KeyError from
// snapshot.scope_parents[current] (parallel_analysis.py:349) — the shared
// scopeIsWithin has no false-returning defensive branch.
func scopeIsWithin(candidate, ancestor binding.ScopeID, snap *binding.Snapshot) bool {
	current := candidate
	for current != 0 {
		if current == ancestor {
			return true
		}
		info, found := snap.Scopes().Get(current)
		if !found {
			panic("passes: scope chain broken; missing scope record")
		}
		current = info.Parent
	}
	return false
}

// isCandidateExport mirrors _is_candidate_export
// (parallel_analysis.py:353-363): the symbol is declared directly in the
// branch scope and no other symbol with the same name is declared in the
// branch scope or an ancestor scope.
func isCandidateExport(symbolID binding.SymbolID, branchScopeID binding.ScopeID, snap *binding.Snapshot) bool {
	declaration, found := snap.Declarations().Get(symbolID)
	if !found {
		return false
	}
	if declaration.Scope != branchScopeID {
		return false
	}
	other := false
	snap.Declarations().Range(func(otherID binding.SymbolID, otherDecl binding.SymbolDecl) bool {
		if otherID == symbolID || otherDecl.Name != declaration.Name {
			return true
		}
		if scopeIsWithin(branchScopeID, otherDecl.Scope, snap) {
			other = true
			return false
		}
		return true
	})
	return !other
}

// emitHazards emits E1036 access hazards for every conflicting branch pair,
// mirroring _emit_hazards (parallel_analysis.py:366-407): pairs iterate in
// 1-based branch order, symbols are sorted by SymbolID value, and each
// message carries the first access site of each branch in record order.
func emitHazards(facts ParallelFacts, snap *binding.Snapshot, ctx *Context) {
	// Python computes (left.write_symbols & right.write_symbols) |
	// (left.write_symbols & right.read_symbols) |
	// (left.read_symbols & right.write_symbols) as frozenset operations and
	// sorts the result by SymbolId.value; Go mirrors the same sets with maps.
	toSet := func(ids []binding.SymbolID) map[binding.SymbolID]bool {
		out := map[binding.SymbolID]bool{}
		for _, id := range ids {
			out[id] = true
		}
		return out
	}
	for i := 0; i < len(facts.branches); i++ {
		for j := i + 1; j < len(facts.branches); j++ {
			left := facts.branches[i]
			right := facts.branches[j]
			leftIndex, rightIndex := i+1, j+1

			leftWrites := toSet(left.WriteSymbols())
			leftReads := toSet(left.ReadSymbols())
			rightWrites := toSet(right.WriteSymbols())
			rightReads := toSet(right.ReadSymbols())

			symbols := map[binding.SymbolID]bool{}
			for s := range leftWrites {
				if rightWrites[s] || rightReads[s] {
					symbols[s] = true
				}
			}
			for s := range leftReads {
				if rightWrites[s] {
					symbols[s] = true
				}
			}
			sorted := make([]binding.SymbolID, 0, len(symbols))
			for s := range symbols {
				sorted = append(sorted, s)
			}
			sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })

			for _, symbolID := range sorted {
				var hazard string
				var leftSite, rightSite ParallelSymbolAccess
				var leftKind, rightKind string
				if leftWrites[symbolID] && rightWrites[symbolID] {
					hazard = "write/write"
					leftSite = firstSite(left.writes, symbolID)
					rightSite = firstSite(right.writes, symbolID)
					leftKind, rightKind = "write", "write"
				} else if leftWrites[symbolID] {
					hazard = "write/read"
					leftSite = firstSite(left.writes, symbolID)
					rightSite = firstSite(right.reads, symbolID)
					leftKind, rightKind = "write", "read"
				} else {
					hazard = "write/read"
					leftSite = firstSite(left.reads, symbolID)
					rightSite = firstSite(right.writes, symbolID)
					leftKind, rightKind = "read", "write"
				}
				name := declarationName(snap, symbolID)
				ctx.Emit(diag.PARALLEL_ACCESS_HAZARD, diag.Error,
					"Parallel branches "+strconv.Itoa(leftIndex)+" and "+strconv.Itoa(rightIndex)+" have a "+hazard+" hazard "+
						"for shared variable '"+name+"': branch "+strconv.Itoa(leftIndex)+" "+leftKind+" at "+
						formatLocation(leftSite.Span)+"; branch "+strconv.Itoa(rightIndex)+" "+rightKind+" at "+
						formatLocation(rightSite.Span), leftSite.Span)
			}
		}
	}
}

// firstSite mirrors _first_site (parallel_analysis.py:409-412): the first
// access in record order for the symbol.
func firstSite(accesses []ParallelSymbolAccess, symbolID binding.SymbolID) ParallelSymbolAccess {
	for _, access := range accesses {
		if access.SymbolID == symbolID {
			return access
		}
	}
	panic("passes: no access site for hazard symbol")
}

// formatLocation mirrors _format_location (parallel_analysis.py:414-416):
// str(span.start), or "<unknown>" for a None/zero span.
func formatLocation(span ast.Span) string {
	if span == (ast.Span{}) {
		return "<unknown>"
	}
	return span.Start.String()
}
