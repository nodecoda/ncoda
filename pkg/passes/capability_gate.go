package passes

// CapabilityGatePass enforces the Phase-0 capability rejections during
// frontend validation, ported from src/nclang/lang/passes/capability_gate.py
// (749 lines). Reachable surface:
//
//	can_lower_list_operator              :69-110
//	can_materialize_list_operator_code   :112-135
//	_is_code_output_element              :137-151
//	_is_code_output_value                :153-159
//	_is_code_materializable_collection   :161-207
//	_variable_has_runtime_array_source   :209-225
//	_is_runtime_array_source             :227-259
//	_has_unproven_array_input            :261-278  GATED (see below)
//	_list_operator_call_name             :280-299
//	_is_selector_backed_collection       :301-308
//	_list_operator_expression_type       :310-321
//	_is_order_literal                    :323-325
//	_is_native_filter                    :327-345
//	_is_native_contains                  :347-365
//	_filter_target                       :367-388
//	_supports_membership                 :390-398
//	_is_static_scalar                    :400-401
//	_literal_matches_filter_type         :403-414
//	_is_safe_graphon_number              :416-418
//	CapabilityGatePass.run               :444-452
//	visit_call_expr                      :456-489
//	_rejected                            :491-493
//	visit_for_expr                       :495-523
//	visit_for_stmt                       :525-547
//	_has_new_error                       :549-553
//	visit_if_stmt                        :555-557
//	visit_else_if_branch                 :559-561
//	visit_while_stmt                     :563-570
//	_reject_condition                    :572-613
//	_is_direct_condition                 :615-647
//	_is_bool_selector                    :649-654
//	_is_selector                         :656-669
//	visit_parallel_stmt                  :671-686
//	visit_attempt_stmt                   :688-699
//	visit_parallel_expr                  :701-715
//	visit_return_stmt                    :717-727
//	visit_break_stmt / visit_continue_   :729-731
//	_reject_loop_exit                    :733-745
//
// Traversal contract: the pass is an ast.Visitor (ast.Walk) that mirrors
// the Python ProcASTVisitor dispatch. Nodes with pre/post depth bookkeeping
// walk their children manually and return nil to prune (the code_analysis /
// semantic pattern); plain nodes return p so the exhaustive Go Walk
// continues. Go's Walk visits a superset of the Python base visitor's child
// coverage (type refs, call type_args, params, const-decl values and
// parameter defaults in the Program walk, which the Python base visitor
// omits — code_analysis-style D2 note); none of the extra children can host
// a list operator, loop, parallel, condition or exit, so the observable
// surface is identical for reachable programs.
//
// GATING (lowering-ctx branches): capability_gate.py's helpers branch on
// `hasattr(ctx, "expression_type")` (:182-207, :291-307, :310-321), a
// lowering-stage DSLPassContext face (typed_program / call_targets /
// find_node / expression_type) that the Go pass pipeline does not expose.
// Only the expr_symbols/expr_types branches are ported; the lowering
// branches are marked GATED at each site. _has_unproven_array_input
// (:261-278) depends on find_node/node_type and is not ported at all.
//
// DEVLNITS (Go equivalents): the reference resolves a VARIABLE symbol to
// its declaring VarDeclStmt through ctx.declaration_node
// (_variable_has_runtime_array_source :209-225); the Go pass has no
// declaration_node face, so Run pre-enumerates every program VarDeclStmt
// into a SymbolID → init map via DeclarationSymbols (buildDeclInits) — the
// recorded equivalent implementation.

import (
	"fmt"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/types"
)

// ── module constants (capability_gate.py:35-45) ─────────────────────────

var (
	ListOperatorNames        = map[string]bool{"filter": true, "sort": true, "sort_by": true, "take": true}
	codeArrayProducerNames   = map[string]bool{"split": true, "map": true, "filter": true, "flatMap": true}
	ListOperatorElementKinds = map[types.DSLType]bool{
		types.File: true, types.String: true, types.Int: true, types.Float: true, types.Bool: true,
	}
	FileKeys      = map[string]bool{"name": true, "type": true, "extension": true, "mime_type": true, "size": true, "transfer_method": true, "url": true, "related_id": true}
	fileEnumKeys  = map[string]bool{"type": true, "transfer_method": true}
	comparisonOps = map[string]bool{"==": true, "!=": true, "<": true, "<=": true, ">": true, ">=": true}
)

// ── shared context lookups ──────────────────────────────────────────────

// snapshotID resolves n's AstNodeID in the published snapshot (Python
// ctx.ast_id(n)).
func snapshotID(ctx *Context, n ast.Node) (binding.AstNodeID, bool) {
	if ctx.Bindings == nil {
		return 0, false
	}
	return ctx.Bindings.NodeIDOf(n)
}

// symbolDecl returns the declaration record and SymbolID for the symbol
// referenced by n, mirroring ctx.expr_symbols.get(ctx.ast_id(n)) followed by
// the symbol-table lookup (Python expr_symbols values are Symbol objects
// carrying kind/is_builtin/name; the Go snapshot renders them as
// Declarations records keyed by SymbolID).
func symbolDecl(ctx *Context, n ast.Node) (binding.SymbolDecl, binding.SymbolID, bool) {
	id, ok := snapshotID(ctx, n)
	if !ok {
		return binding.SymbolDecl{}, 0, false
	}
	symID, ok := ctx.Bindings.ReferenceSymbols().Get(id)
	if !ok {
		return binding.SymbolDecl{}, 0, false
	}
	decl, ok := ctx.Bindings.Declarations().Get(symID)
	if !ok {
		return binding.SymbolDecl{}, 0, false
	}
	return decl, symID, true
}

// isBareDescriptor is the Go form of Python `type_info.params is None`
// (types.py TypeDescriptor.params): no Array/Map/Optional/Record/Enum/
// Function payload. Go flattens the Python params into Elem/Key/Val/
// Fields/Params/Result; File constraints live in Descriptor.File (types.py
// file_constraints — separate from params) and do not count as payload.
func IsBareDescriptor(d *types.Descriptor) bool {
	return d.Elem == nil && d.Key == nil && d.Val == nil && len(d.Fields) == 0 &&
		d.Params == nil && d.Result == nil
}

// ── list-operator helpers (capability_gate.py:69-321) ───────────────────

// canLowerListOperator mirrors can_lower_list_operator (:69-110).
func canLowerListOperator(call *ast.CallExpr, ctx *Context, declInits map[binding.SymbolID]ast.Expr) bool {
	name := listOperatorCallName(call, ctx)
	if name == "" || len(call.Args()) == 0 {
		return false
	}
	collection := call.Args()[0]
	collectionType := listOperatorExpressionType(collection, ctx)
	if collectionType == nil || collectionType.Kind != types.Array || collectionType.Elem == nil {
		return false
	}
	elementType := collectionType.Elem
	if !ListOperatorElementKinds[elementType.Kind] || !IsBareDescriptor(elementType) {
		return false
	}
	if !isSelectorBackedCollection(collection, elementType, ctx, declInits) {
		return false
	}
	switch name {
	case "filter":
		if len(call.Args()) != 2 {
			return false
		}
		lam, ok := call.Args()[1].(*ast.LambdaExpr)
		return ok && IsNativeFilter(lam, elementType)
	case "sort":
		if len(call.Args()) != 2 {
			return false
		}
		return elementType.Kind != types.File && IsOrderLiteral(call.Args()[1])
	case "sort_by":
		if len(call.Args()) != 3 {
			return false
		}
		sl, ok := call.Args()[1].(*ast.StringLit)
		return elementType.Kind == types.File && ok && FileKeys[sl.Value()] && IsOrderLiteral(call.Args()[2])
	case "take":
		if len(call.Args()) != 2 {
			return false
		}
		_, ok := call.Args()[1].(*ast.IntLit)
		return ok
	}
	return false
}

// canMaterializeListOperatorCode mirrors can_materialize_list_operator_code
// (:112-135).
func canMaterializeListOperatorCode(call *ast.CallExpr, ctx *Context, declInits map[binding.SymbolID]ast.Expr) bool {
	name := listOperatorCallName(call, ctx)
	if !ListOperatorNames[name] || len(call.Args()) == 0 {
		return false
	}
	resultType := listOperatorExpressionType(call, ctx)
	if resultType == nil || resultType.Kind != types.Array || resultType.Elem == nil {
		return false
	}
	if !isCodeMaterializableCollection(call.Args()[0], ctx, declInits) {
		return false
	}
	// Graphon Code cannot publish file arrays, arbitrary values, or nested
	// arrays (capability_gate.py:126-127).
	if !isCodeOutputElement(resultType.Elem) {
		return false
	}
	switch name {
	case "sort_by":
		return false
	case "filter":
		if len(call.Args()) != 2 {
			return false
		}
		_, ok := call.Args()[1].(*ast.LambdaExpr)
		return ok
	case "sort", "take":
		return len(call.Args()) == 2
	}
	return false
}

// isCodeOutputElement mirrors _is_code_output_element (:137-151).
func isCodeOutputElement(t *types.Descriptor) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case types.String, types.Int, types.Float, types.Bool:
		return IsBareDescriptor(t)
	case types.Record:
		for _, f := range t.Fields {
			if f.Type == nil || !isCodeOutputValue(f.Type) {
				return false
			}
		}
		return true
	case types.Map:
		return t.Key != nil && t.Key.Kind == types.String && t.Val != nil && isCodeOutputValue(t.Val)
	}
	return false
}

// isCodeOutputValue mirrors _is_code_output_value (:153-159).
func isCodeOutputValue(t *types.Descriptor) bool {
	if t == nil {
		return false
	}
	if isCodeOutputElement(t) {
		return true
	}
	if t.Kind == types.Array && t.Elem != nil {
		return isCodeOutputElement(t.Elem)
	}
	return false
}

// isCodeMaterializableCollection mirrors
// _is_code_materializable_collection (:161-207).
func isCodeMaterializableCollection(expr ast.Expr, ctx *Context, declInits map[binding.SymbolID]ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.ArrayLit:
		return true
	case *ast.CallExpr:
		nestedName := listOperatorCallName(e, ctx)
		if nestedName != "" {
			return canLowerListOperator(e, ctx, declInits) || canMaterializeListOperatorCode(e, ctx, declInits)
		}
		// GATED (capability_gate.py:190-195): the lowering branch returns
		// True for every call when ctx has expression_type; the Go pass
		// context has no typed_program face, so the expr_symbols fallback
		// (:196-207) applies.
		return isRuntimeArraySource(e, ctx, declInits, map[binding.SymbolID]bool{})
	}
	root := expr
	for {
		fa, ok := root.(*ast.FieldAccessExpr)
		if !ok {
			break
		}
		if fa.Obj() == nil {
			return false
		}
		root = fa.Obj()
	}
	ie, ok := root.(*ast.IdentExpr)
	if !ok {
		return false
	}
	// GATED (capability_gate.py:200-207): the lowering branch resolves the
	// reference through ctx.resolve_reference + find_node; the Go pass
	// applies the expr_symbols branch only.
	decl, symID, ok := symbolDecl(ctx, ie)
	if !ok {
		return false
	}
	return decl.Kind == binding.SymbolVariable &&
		variableHasRuntimeArraySource(symID, ctx, declInits, map[binding.SymbolID]bool{})
}

// variableHasRuntimeArraySource mirrors
// _variable_has_runtime_array_source (:209-225). The Python first bails
// when the ctx lacks the declaration_node surface (hasattr check); the Go
// pass builds declInits in Run instead (buildDeclInits), so the equivalent
// branch is always reachable here — the recorded equivalent implementation.
func variableHasRuntimeArraySource(symID binding.SymbolID, ctx *Context, declInits map[binding.SymbolID]ast.Expr, seen map[binding.SymbolID]bool) bool {
	if seen[symID] {
		return false
	}
	seen[symID] = true
	init, found := declInits[symID]
	if !found {
		return false
	}
	return isRuntimeArraySource(init, ctx, declInits, seen)
}

// isRuntimeArraySource mirrors _is_runtime_array_source (:227-259).
func isRuntimeArraySource(expr ast.Expr, ctx *Context, declInits map[binding.SymbolID]ast.Expr, seen map[binding.SymbolID]bool) bool {
	switch e := expr.(type) {
	case *ast.ArrayLit, *ast.ForeignCodeExpr, *ast.ForExpr:
		return true
	case *ast.CallExpr:
		nestedName := listOperatorCallName(e, ctx)
		if nestedName != "" {
			return canLowerListOperator(e, ctx, declInits) || canMaterializeListOperatorCode(e, ctx, declInits)
		}
		resultType := listOperatorExpressionType(e, ctx)
		if resultType == nil || resultType.Kind != types.Array {
			return false
		}
		fid, ok := e.Func().(*ast.IdentExpr)
		if !ok {
			return false
		}
		producer, _, ok := symbolDecl(ctx, fid)
		if !ok {
			return false
		}
		return producer.Kind == binding.SymbolFunction ||
			(producer.Kind == binding.SymbolBuiltinFunction && codeArrayProducerNames[producer.Name])
	}
	root := expr
	for {
		fa, ok := root.(*ast.FieldAccessExpr)
		if !ok {
			break
		}
		if fa.Obj() == nil {
			return false
		}
		root = fa.Obj()
	}
	ie, ok := root.(*ast.IdentExpr)
	if !ok {
		return false
	}
	source, sourceID, ok := symbolDecl(ctx, ie)
	if !ok {
		return false
	}
	return source.Kind == binding.SymbolVariable && variableHasRuntimeArraySource(sourceID, ctx, declInits, seen)
}

// listOperatorCallName mirrors _list_operator_call_name (:280-299).
func listOperatorCallName(call *ast.CallExpr, ctx *Context) string {
	id, ok := call.Func().(*ast.IdentExpr)
	if !ok {
		return ""
	}
	// GATED (capability_gate.py:262-277): the lowering branch resolves the
	// target through typed_program.binding.call_targets; the Go pass
	// context exposes only the expr_symbols surface, which the binder
	// populates for every builtin reference (binder.go visitCallExpr).
	decl, _, ok := symbolDecl(ctx, id)
	if !ok || decl.Kind != binding.SymbolBuiltinFunction || !ListOperatorNames[decl.Name] {
		return ""
	}
	return decl.Name
}

// isSelectorBackedCollection mirrors _is_selector_backed_collection
// (:301-308). The lowering branch (:291-307) is GATED; only the
// expr_symbols branch is ported.
func isSelectorBackedCollection(expr ast.Expr, elementType *types.Descriptor, ctx *Context, declInits map[binding.SymbolID]ast.Expr) bool {
	if call, ok := expr.(*ast.CallExpr); ok {
		return canLowerListOperator(call, ctx, declInits)
	}
	root := expr
	for {
		fa, ok := root.(*ast.FieldAccessExpr)
		if !ok {
			break
		}
		if fa.Obj() == nil {
			return false
		}
		root = fa.Obj()
	}
	ie, ok := root.(*ast.IdentExpr)
	if !ok {
		return false
	}
	decl, symID, ok := symbolDecl(ctx, ie)
	if !ok {
		return false
	}
	// Entry parameters are lowered by the target translator directly into
	// Start variables: an exact array[file] entry selector is already a
	// runtime-backed collection; helper parameters are not Start selectors
	// (capability_gate.py:303-306).
	if decl.Kind == binding.SymbolInput {
		return elementType.Kind == types.File && IsBareDescriptor(elementType)
	}
	return decl.Kind == binding.SymbolVariable &&
		variableHasRuntimeArraySource(symID, ctx, declInits, map[binding.SymbolID]bool{})
}

// listOperatorExpressionType mirrors _list_operator_expression_type
// (:310-321). The lowering branch (ctx.expression_type) is GATED; the
// expr_types branch applies.
func listOperatorExpressionType(expr ast.Expr, ctx *Context) *types.Descriptor {
	id, ok := snapshotID(ctx, expr)
	if !ok {
		return nil
	}
	return ctx.ExprTypes[id]
}

// isOrderLiteral mirrors _is_order_literal (:323-325).
func IsOrderLiteral(expr ast.Expr) bool {
	sl, ok := expr.(*ast.StringLit)
	return ok && (sl.Value() == "asc" || sl.Value() == "desc")
}

// isNativeFilter mirrors _is_native_filter (:327-345).
func IsNativeFilter(lam *ast.LambdaExpr, elementType *types.Descriptor) bool {
	if len(lam.Params()) != 1 || lam.Body() == nil {
		return false
	}
	parameter := lam.Params()[0]
	body := lam.Body()
	if call, ok := body.(*ast.CallExpr); ok {
		return isNativeContains(call, parameter, elementType)
	}
	be, ok := body.(*ast.BinaryExpr)
	if !ok || be.Left() == nil || be.Right() == nil {
		return false
	}
	key, targetType, ok := filterTarget(be.Left(), parameter, elementType)
	if !ok {
		return false
	}
	if be.Op() == "in" {
		return supportsMembership(key, targetType, be.Right())
	}
	if !comparisonOps[be.Op()] || !literalMatchesFilterType(be.Right(), targetType) {
		return false
	}
	switch targetType.Kind {
	case types.String:
		return (be.Op() == "==" || be.Op() == "!=") && !fileEnumKeys[key]
	case types.Int, types.Float:
		return comparisonOps[be.Op()] && isSafeGraphonNumber(be.Right())
	case types.Bool:
		if be.Op() != "==" && be.Op() != "!=" {
			return false
		}
		_, ok := be.Right().(*ast.BoolLit)
		return ok
	}
	return false
}

// isNativeContains mirrors _is_native_contains (:347-365).
func isNativeContains(call *ast.CallExpr, parameter string, elementType *types.Descriptor) bool {
	fa, ok := call.Func().(*ast.FieldAccessExpr)
	if !ok || fa.FieldName() != "contains" || fa.Obj() == nil || len(call.Args()) != 1 {
		return false
	}
	if _, ok := call.Args()[0].(*ast.StringLit); !ok {
		return false
	}
	key, targetType, ok := filterTarget(fa.Obj(), parameter, elementType)
	return ok && targetType.Kind == types.String && !fileEnumKeys[key]
}

// filterTarget mirrors _filter_target (:367-388).
func filterTarget(expr ast.Expr, parameter string, elementType *types.Descriptor) (string, *types.Descriptor, bool) {
	if ie, ok := expr.(*ast.IdentExpr); ok && ie.Name() == parameter {
		return "", elementType, true
	}
	if elementType.Kind != types.File {
		return "", nil, false
	}
	fa, ok := expr.(*ast.FieldAccessExpr)
	if !ok {
		return "", nil, false
	}
	obj, ok := fa.Obj().(*ast.IdentExpr)
	if !ok || obj.Name() != parameter || !FileKeys[fa.FieldName()] {
		return "", nil, false
	}
	if fa.FieldName() == "size" {
		return fa.FieldName(), types.IntType(), true
	}
	return fa.FieldName(), types.StringType(), true
}

// supportsMembership mirrors _supports_membership (:390-398).
func supportsMembership(key string, targetType *types.Descriptor, value ast.Expr) bool {
	al, ok := value.(*ast.ArrayLit)
	if !ok || len(al.Elements()) == 0 {
		return false
	}
	if !fileEnumKeys[key] {
		return false
	}
	if targetType.Kind != types.String {
		return false
	}
	for _, e := range al.Elements() {
		if _, ok := e.(*ast.StringLit); !ok {
			return false
		}
	}
	return true
}

// isStaticScalar mirrors _is_static_scalar (:400-401).
func isStaticScalar(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.StringLit, *ast.IntLit, *ast.FloatLit, *ast.BoolLit:
		return true
	}
	return false
}

// literalMatchesFilterType mirrors _literal_matches_filter_type (:403-414).
func literalMatchesFilterType(expr ast.Expr, targetType *types.Descriptor) bool {
	if !isStaticScalar(expr) {
		return false
	}
	switch targetType.Kind {
	case types.String:
		_, ok := expr.(*ast.StringLit)
		return ok
	case types.Int, types.Float:
		if _, ok := expr.(*ast.IntLit); ok {
			return isSafeGraphonNumber(expr)
		}
		_, ok := expr.(*ast.FloatLit)
		return ok
	case types.Bool:
		_, ok := expr.(*ast.BoolLit)
		return ok
	}
	return false
}

// isSafeGraphonNumber mirrors _is_safe_graphon_number (:416-418): Graphon
// parses numeric filter values through float(), so ints beyond 2^53 are
// unsafe.
func isSafeGraphonNumber(expr ast.Expr) bool {
	il, ok := expr.(*ast.IntLit)
	if !ok {
		return true
	}
	v := il.Value()
	if v < 0 {
		v = -v
	}
	return v <= 1<<53
}

// ── the pass (capability_gate.py:444-745) ───────────────────────────────

// loopTarget records one open loop plus the parallel depth at its start,
// mirroring the run() _loop_targets stack entries (capability_gate.py:451).
type loopTarget struct {
	kind          string // "for_expr" | "for" | "while"
	parallelDepth int
}

// CapabilityGatePass implements CapabilityGatePass.run (capability_gate.py:
// 444-452): one traversal carrying the manifest, the depth stacks and the
// prebuilt declaration-initializer table, then ast.Walk over the program.
type CapabilityGatePass struct {
	ctx               *Context
	manifest          *CapabilityManifest
	declInits         map[binding.SymbolID]ast.Expr
	forExprDepth      int
	loopDepth         int
	loopTargets       []loopTarget
	parallelDepth     int
	listOperatorDepth int
}

// Run mirrors run (:444-452): initialize per-run state, then walk the
// program. State is reset so one pass instance can be reused across
// compilations.
func (p *CapabilityGatePass) Run(program *ast.Program, ctx *Context) {
	p.ctx = ctx
	// M2 (R1): nil-guard the profile surface — NewContext/DefaultTargetProfile
	// always install Capabilities, but a zero-value profile must not panic.
	p.manifest = DefaultCapabilityManifest()
	if ctx.Profile != nil && ctx.Profile.Capabilities != nil {
		p.manifest = ctx.Profile.Capabilities
	}
	p.declInits = buildDeclInits(program, ctx)
	p.forExprDepth = 0
	p.loopDepth = 0
	p.loopTargets = nil
	p.parallelDepth = 0
	p.listOperatorDepth = 0
	ast.Walk(p, program)
}

// buildDeclInits is the recorded Go equivalent of the reference
// ctx.declaration_node(symbol) surface: Python resolves a symbol to its
// declaring VarDeclStmt on demand (capability_gate.py:216-222); the Go pass
// has no declaration_node face, so Run pre-enumerates every VarDeclStmt in
// the program into a SymbolID → init-expression map via
// DeclarationSymbols. A variable declared without an initializer has no
// entry, reading exactly like the Python `declaration.init is None →
// False` branch. The enumeration reuses the loop_state pass's
// varDeclCollector (loop_state.go), which walks every declaration symbol.
func buildDeclInits(program *ast.Program, ctx *Context) map[binding.SymbolID]ast.Expr {
	out := map[binding.SymbolID]ast.Expr{}
	if ctx.Bindings == nil {
		return out
	}
	declarations := map[binding.SymbolID]*ast.VarDeclStmt{}
	ast.Walk(&varDeclCollector{snap: ctx.Bindings, out: declarations}, program)
	for symID, decl := range declarations {
		if decl.Init() != nil {
			out[symID] = decl.Init()
		}
	}
	return out
}

// Visit mirrors the visit_* dispatch (capability_gate.py:456-745).
func (p *CapabilityGatePass) Visit(n ast.Node) ast.Visitor {
	switch node := n.(type) {
	case *ast.CallExpr:
		return p.visitCallExpr(node)
	case *ast.ForExpr:
		return p.visitForExpr(node)
	case *ast.ParallelForExpr:
		return p.visitParallelForExpr(node)
	case *ast.ForStmt:
		return p.visitForStmt(node)
	case *ast.IfStmt:
		p.rejectCondition(node.Condition(), node.Span(), true)
		return p
	case *ast.ElseIfBranch:
		p.rejectCondition(node.Condition(), node.Span(), true)
		return p
	case *ast.WhileStmt:
		return p.visitWhileStmt(node)
	case *ast.ParallelStmt:
		return p.visitParallelStmt(node)
	case *ast.AttemptStmt:
		return p.visitAttemptStmt(node)
	case *ast.SwitchStmt:
		// 查询表达式不是条件；分支体作为普通区域走 ast.Walk 子访问。
		return p
	case *ast.ReturnStmt:
		p.visitReturnStmt(node)
		return nil
	case *ast.BreakStmt:
		p.rejectLoopExit("break", node.Span())
		return nil
	case *ast.ContinueStmt:
		p.rejectLoopExit("continue", node.Span())
		return nil
	}
	return p
}

// visitCallExpr mirrors visit_call_expr (:456-489). The Python base visitor
// walks func then args; the manual walk keeps that exact child coverage.
func (p *CapabilityGatePass) visitCallExpr(node *ast.CallExpr) ast.Visitor {
	diagnosticCount := len(p.ctx.Diagnostics)
	name := listOperatorCallName(node, p.ctx)
	if name == "" {
		ast.Walk(p, node.Func())
		for _, arg := range node.Args() {
			ast.Walk(p, arg)
		}
		return nil
	}
	p.listOperatorDepth++
	ast.Walk(p, node.Func())
	for _, arg := range node.Args() {
		ast.Walk(p, arg)
	}
	p.listOperatorDepth--
	if p.listOperatorDepth == 0 && !p.hasNewError(diagnosticCount) &&
		!canLowerListOperator(node, p.ctx, p.declInits) &&
		!canMaterializeListOperatorCode(node, p.ctx, p.declInits) {
		resultType := listOperatorExpressionType(node, p.ctx)
		// M1 (R1): Python's f-string renders a missing expr-type as "None";
		// the Go Descriptor.String() nil fallback ("Type(any)") must not
		// leak into the diagnostic.
		resultTypeText := "None"
		if resultType != nil {
			resultTypeText = resultType.String()
		}
		p.emitTargetNotLowerable(CapabilityShapeConditionCalculation,
			fmt.Sprintf("%s result type %s is not safely lowerable: this target requires a "+
				"proven runtime array source, and Code output cannot carry file, any, or "+
				"nested arrays", name, resultTypeText), node.Span())
	}
	return nil
}

// rejected mirrors _rejected (:491-493).
func (p *CapabilityGatePass) rejected(shape CapabilityShape) bool {
	return p.manifest.Lookup(shape).State == CapabilityStateRejected
}

// visitForExpr mirrors visit_for_expr (:495-523).
func (p *CapabilityGatePass) visitForExpr(node *ast.ForExpr) ast.Visitor {
	diagnosticCount := len(p.ctx.Diagnostics)
	p.forExprDepth++
	p.loopTargets = append(p.loopTargets, loopTarget{kind: "for_expr", parallelDepth: p.parallelDepth})
	for _, src := range node.LoopSources() {
		ast.Walk(p, src)
	}
	ast.Walk(p, node.Body())
	p.loopTargets = p.loopTargets[:len(p.loopTargets)-1]
	p.forExprDepth--
	if !p.hasNewError(diagnosticCount) && p.rejected(CapabilityShapeForExpressionWithYield) {
		p.emitTargetNotLowerable(CapabilityShapeForExpressionWithYield,
			"for expression is not supported without target runtime evidence (probe FOREXPR-01)", node.Span())
	}
	return nil
}

// visitParallelForExpr — I1 (R1): Python visit_parallel_for_expr
// (ast.py:686-688) delegates to self.visit_for_expr, so a parallel-for runs
// the full for-expression gate: loop_targets push ("for_expr",
// parallelDepth), FOR_EXPRESSION_WITH_YIELD rejection, and the depth
// bookkeeping.
//
// M4 (R2): the Go ParallelForExpr embeds ForExpr by value (ast/expr.go), so
// the outer node carries NO separate base — promoted SetSpan/Span read the
// embedded ForExpr's single base, which the parser sets from the
// `parallel` keyword. node.Span() therefore equals node.ForExpr.Span() and
// is the direct Go equivalent of Python's shared ProcNode span (Python
// ParallelForExpr(ForExpr) has one span too); the R1 note's "embedded
// ForExpr span stays zero" was a phantom defect and is retracted.
func (p *CapabilityGatePass) visitParallelForExpr(node *ast.ParallelForExpr) ast.Visitor {
	diagnosticCount := len(p.ctx.Diagnostics)
	p.forExprDepth++
	p.loopTargets = append(p.loopTargets, loopTarget{kind: "for_expr", parallelDepth: p.parallelDepth})
	for _, src := range node.LoopSources() {
		ast.Walk(p, src)
	}
	ast.Walk(p, node.Body())
	p.loopTargets = p.loopTargets[:len(p.loopTargets)-1]
	p.forExprDepth--
	if !p.hasNewError(diagnosticCount) && p.rejected(CapabilityShapeForExpressionWithYield) {
		p.emitTargetNotLowerable(CapabilityShapeForExpressionWithYield,
			"for expression is not supported without target runtime evidence (probe FOREXPR-01)", node.Span())
	}
	return nil
}

// visitForStmt mirrors visit_for_stmt (:525-547).
func (p *CapabilityGatePass) visitForStmt(node *ast.ForStmt) ast.Visitor {
	diagnosticCount := len(p.ctx.Diagnostics)
	p.loopDepth++
	p.loopTargets = append(p.loopTargets, loopTarget{kind: "for", parallelDepth: p.parallelDepth})
	for _, src := range node.LoopSources() {
		ast.Walk(p, src)
	}
	ast.Walk(p, node.Body())
	p.loopTargets = p.loopTargets[:len(p.loopTargets)-1]
	p.loopDepth--
	if !p.hasNewError(diagnosticCount) && p.rejected(CapabilityShapeForStatementPlain) {
		p.emitTargetNotLowerable(CapabilityShapeForStatementPlain,
			"for statement is not supported without target runtime evidence (probe FOR-01)", node.Span())
	}
	return nil
}

// hasNewError mirrors _has_new_error (:549-553): any error diagnostic
// collected since diagnosticCount suppresses the gate's own rejection.
func (p *CapabilityGatePass) hasNewError(diagnosticCount int) bool {
	for _, d := range p.ctx.Diagnostics[diagnosticCount:] {
		if d.Severity == diag.Error {
			return true
		}
	}
	return false
}

// visitWhileStmt mirrors visit_while_stmt (:563-570): while forbids logical
// conditions (allow_logical=False).
func (p *CapabilityGatePass) visitWhileStmt(node *ast.WhileStmt) ast.Visitor {
	p.rejectCondition(node.Condition(), node.Span(), false)
	p.loopDepth++
	p.loopTargets = append(p.loopTargets, loopTarget{kind: "while", parallelDepth: p.parallelDepth})
	ast.Walk(p, node.Condition())
	ast.Walk(p, node.Body())
	p.loopTargets = p.loopTargets[:len(p.loopTargets)-1]
	p.loopDepth--
	return nil
}

// emitTargetNotLowerable 发出「目标不支持该形状」的诊断，并带上错误类别
// （capabilityShapeVerdict）：平台不可约 = platform，编译器未实现 = internal+pending。
// 能力标识 = 形状名。所有 TARGET_NOT_LOWERABLE 出口都经此函数，避免各处漏标类别。
func (p *CapabilityGatePass) emitTargetNotLowerable(shape CapabilityShape, message string, span ast.Span) {
	class, pending := capabilityShapeVerdict(shape)
	p.ctx.EmitClassified(diag.TARGET_NOT_LOWERABLE, diag.Error, message, span,
		class, diag.CapabilityID(shape), pending)
}

// rejectCondition mirrors _reject_condition (:572-613).
func (p *CapabilityGatePass) rejectCondition(condition ast.Expr, fallbackSpan ast.Span, allowLogical bool) bool {
	var logical bool
	if be, ok := condition.(*ast.BinaryExpr); ok {
		logical = be.Op() == "&&" || be.Op() == "||"
	}
	if condition == nil || (p.isDirectCondition(condition) && (allowLogical || !logical)) {
		return false
	}
	effect := binding.EffectCalculation
	// M2 (R1): Python guards effect_facts with an explicit
	// `ctx.effect_facts is not None` check (capability_gate.py:588-591); the
	// Go Context defaults to the shared empty table, but a zero Context or a
	// nil EffectFacts from a whitebox harness must not panic.
	if p.ctx.EffectFacts != nil {
		if id, ok := snapshotID(p.ctx, condition); ok {
			if eff, ok := p.ctx.EffectFacts.NodeEffects().Get(id); ok {
				effect = eff
			}
		}
	}
	shape := CapabilityShapeConditionCalculation
	if effect == binding.EffectWorkflowOperation {
		shape = CapabilityShapeConditionEffectful
	}
	if !allowLogical || p.rejected(shape) {
		span := fallbackSpan
		if condition != nil && condition.Span() != (ast.Span{}) {
			span = condition.Span()
		}
		p.emitTargetNotLowerable(shape,
			"condition expression is not lowerable in this container for the selected target", span)
		return true
	}
	return false
}

// isDirectCondition mirrors _is_direct_condition (:615-647).
func (p *CapabilityGatePass) isDirectCondition(expr ast.Expr) bool {
	if p.isBoolSelector(expr) {
		return true
	}
	if ue, ok := expr.(*ast.UnaryExpr); ok && ue.Op() == "!" && ue.Operand() != nil && p.isBoolSelector(ue.Operand()) {
		return true
	}
	if be, ok := expr.(*ast.BinaryExpr); ok && (be.Op() == "&&" || be.Op() == "||") {
		return be.Left() != nil && be.Right() != nil &&
			p.isDirectCondition(be.Left()) && p.isDirectCondition(be.Right())
	}
	if be, ok := expr.(*ast.BinaryExpr); ok && comparisonOps[be.Op()] {
		return be.Left() != nil && isSelector(be.Left()) && isLiteralOrNull(be.Right())
	}
	if call, ok := expr.(*ast.CallExpr); ok {
		if fa, ok := call.Func().(*ast.FieldAccessExpr); ok && fa.FieldName() == "contains" &&
			fa.Obj() != nil && isSelector(fa.Obj()) && len(call.Args()) == 1 {
			if _, ok := call.Args()[0].(*ast.StringLit); ok {
				objectType := listOperatorExpressionType(fa.Obj(), p.ctx)
				return objectType != nil && objectType.Kind == types.String
			}
		}
	}
	return false
}

// isLiteralOrNull is the Go form of the Python
// `isinstance(expr.right, (StringLit, IntLit, FloatLit, BoolLit, NullLit))`
// direct-comparison literal check (:634-636).
func isLiteralOrNull(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.StringLit, *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.NullLit:
		return true
	}
	return false
}

// isBoolSelector mirrors _is_bool_selector (:649-654).
func (p *CapabilityGatePass) isBoolSelector(expr ast.Expr) bool {
	if !isSelector(expr) {
		return false
	}
	exprType := listOperatorExpressionType(expr, p.ctx)
	return exprType != nil && exprType.Kind == types.Bool
}

// isSelector mirrors _is_selector (:656-669).
func isSelector(expr ast.Expr) bool {
	if _, ok := expr.(*ast.IdentExpr); ok {
		return true
	}
	fa, ok := expr.(*ast.FieldAccessExpr)
	if !ok {
		return false
	}
	current := fa.Obj()
	for {
		inner, ok := current.(*ast.FieldAccessExpr)
		if !ok {
			break
		}
		current = inner.Obj()
	}
	_, ok = current.(*ast.IdentExpr)
	return ok
}

// visitParallelStmt mirrors visit_parallel_stmt (:671-686).
func (p *CapabilityGatePass) visitParallelStmt(node *ast.ParallelStmt) ast.Visitor {
	diagnosticCount := len(p.ctx.Diagnostics)
	p.parallelDepth++
	for _, branch := range node.Branches() {
		ast.Walk(p, branch)
	}
	p.parallelDepth--
	if p.hasNewError(diagnosticCount) {
		return nil
	}
	nonemptyBranches := 0
	for _, branch := range node.Branches() {
		if len(branch.Stmts()) > 0 {
			nonemptyBranches++
		}
	}
	if nonemptyBranches > 1 && p.rejected(CapabilityShapeParallelIsolated) {
		p.emitTargetNotLowerable(CapabilityShapeParallelIsolated,
			"multi-branch parallel is not supported without verified runtime semantics", node.Span())
	}
	return nil
}

// visitAttemptStmt mirrors visit_attempt_stmt (:688-699). The Python base
// visitor walks operation/result_binding/success_body/error_binding/
// failure_body; the manual walk keeps that coverage.
func (p *CapabilityGatePass) visitAttemptStmt(node *ast.AttemptStmt) ast.Visitor {
	diagnosticCount := len(p.ctx.Diagnostics)
	ast.Walk(p, node.Operation())
	ast.Walk(p, node.ResultBinding())
	ast.Walk(p, node.SuccessBody())
	ast.Walk(p, node.ErrorBinding())
	ast.Walk(p, node.FailureBody())
	if !p.hasNewError(diagnosticCount) && p.rejected(CapabilityShapeControlAttempt) {
		p.emitTargetNotLowerable(CapabilityShapeControlAttempt,
			"attempt/recovery control is not supported without verified target runtime semantics", node.Span())
	}
	return nil
}

// visitReturnStmt mirrors visit_return_stmt (:717-727). Python prunes the
// return value subtree (returns None without calling super), so the Go
// Visit returns nil after the checks.
func (p *CapabilityGatePass) visitReturnStmt(node *ast.ReturnStmt) {
	// `return template "…" [using {…}]`（W2）**不是**「循环内 return」那种
	// 可约未实现的能力缺口：它是流程答复端点（平台 `terminatePlan:
	// useAnswerContent`），出现在循环体/子函数/分支结果值臂内是**语言错**
	// （那里不是流程出口）⇒ 由 IR 报 TEMPLATE_RETURN_PLACEMENT（class =
	// language）。在这里按 loop.function_return（class = internal + pending）
	// 报会把「作者写错了位置」说成「我们没实现」——分类契约禁止。
	if _, isTemplate := node.Value().(*ast.TemplateValue); isTemplate {
		return
	}
	// A return inside a parallel branch terminates the WHOLE flow (the
	// branch tail wires straight to end 900001) — the barrier does not
	// wait for it. This is the flow-end semantic, not a branch exit, so it
	// is legal (no E1037). Only loop exits crossing the parallel region
	// (break/continue) remain rejected below.
	if p.loopDepth > 0 && p.rejected(CapabilityShapeLoopFunctionReturn) {
		p.emitTargetNotLowerable(CapabilityShapeLoopFunctionReturn,
			"return inside a loop body is not lowerable to this target yet (compiler gap, not a "+
				"platform limit): the platform's only in-loop early exit is `break` plus loop state "+
				"variables, and the status/break rewriting is not implemented; until it lands, guard "+
				"the rest of the body with a condition instead of returning early", node.Span())
	}
}

// rejectLoopExit mirrors _reject_loop_exit (:733-745), shared by
// visit_break_stmt and visit_continue_stmt (:729-731).
func (p *CapabilityGatePass) rejectLoopExit(keyword string, span ast.Span) bool {
	if len(p.loopTargets) == 0 {
		return false
	}
	target := p.loopTargets[len(p.loopTargets)-1]
	if p.parallelDepth > target.parallelDepth {
		p.ctx.Emit(diag.PARALLEL_EXTERNAL_EXIT, diag.Error,
			fmt.Sprintf("%s targeting a loop outside the parallel branch is not supported", keyword), span)
		return true
	}
	if target.kind == "for" && p.rejected(CapabilityShapeForStatementWithExit) {
		suffix := "early exit"
		if keyword == "continue" {
			suffix = "flow control"
		}
		p.emitTargetNotLowerable(CapabilityShapeForStatementWithExit,
			fmt.Sprintf("%s inside iteration is not supported; use a while-loop for %s", keyword, suffix), span)
		return true
	}
	return false
}
