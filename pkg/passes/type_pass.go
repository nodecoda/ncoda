package passes

// TypePass — type inference and type checking for all expressions, ported
// from src/nclang/lang/passes/type_pass.py (1882 lines, TypePass :117-1814).
// This file carries the pass skeleton: run(), the visit dispatch (with the
// expr-type recording contract), function/block scope replay (Task 15 T5),
// the expected/narrowed type stacks (T7), and the statement visitors.
// Expression inference lives in type_infer.go; the operation-spec table in
// type_ops.go.
//
// Scope replay (T5): Python activates the recorded DSLScope objects
// (ctx.function_scopes / ctx.block_scopes) so scope_chain.resolve works
// during the type pass. The Go Snapshot is immutable, so TypePass keeps its
// own scope stack of activated recorded scopes (ScopeBindings[node] →
// ScopeID) and replays name resolution along the published Parent chain
// (snap.Scopes + snap.Declarations). The stack mirrors Python's nested
// activate()/exit() exactly: functions push the function scope, blocks with
// a recorded scope push theirs, lambdas never push (Python never activates
// the lambda scope in the type pass).
//
// Symbol-type mutation (T2): Python mutates Symbol.type_info in place
// (type_pass.py:299-301 etc.); the Go Context.VarTypes map is the mutation
// surface (symbolType prefers it over the snapshot's frozen declaration
// types).

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/types"
)

// TypePass implements the type stage (type_pass.py:117).
type TypePass struct {
	// _returnType mirrors TypePass._return_type: the current function's
	// resolved return type, set per _check_function (:160-163).
	_returnType *types.Descriptor

	// _inferredReturnType anchors the implicit return type of a function
	// WITHOUT a declared `->` return type: the first value-return exit
	// fixes it, and every later value-return exit must match (multi-return
	// exits are a language feature — C-style — but their value types must
	// stay consistent so a single platform end/leaf can carry the output).
	// Reset per _check_function. Bare `return;` (no value) never anchors.
	_inferredReturnType *types.Descriptor

	// _subscopeDepth counts nested constructs whose `return` exits are NOT
	// function exits: for/while/for-expr bodies and request_input (QA)
	// action/timeout branches return their value to the enclosing loop/QA
	// node (subscope return, e.g. `for (item in items) { request_input ... {
	// action { return "ok"; } } yield ...; }`). if/else, attempt and
	// parallel branch returns ARE function exits (C-style multi-return) and
	// stay anchored. The implicit-return-type consistency anchor only
	// applies at depth 0.
	_subscopeDepth int

	// _lenientTemplate 标记当前表达式处于 LLM 模板字符串插值（${...}）内。
	// 平台模板引擎对插值运行期求值且容错（路径不存在渲染空），强类型字段
	// 检查会误报（如 file 迭代变量 .output —— 平台容错为 null）→ 插值内
	// 字段访问缺失字段返回 Any 不报错（S22_gushici 等 8 例 FIELD_NOT_FOUND
	// 实证，2026-08-26）。
	_lenientTemplate bool

	// _expectedTypes / _narrowedTypes mirror TypePass._expected_types /
	// _narrowed_types (:118-119, 206-280).
	_expectedTypes []*types.Descriptor
	_narrowedTypes []map[binding.SymbolID]*types.Descriptor

	// scopeStack is the Go analogue of the nested scope_chain.activate
	// stack (T5): the innermost element is the currently activated scope.
	scopeStack []binding.ScopeID

	// pendingForPorts 是 for-expr 多口聚合的临时通道：visitForExpr 把
	// 端口类型挂在 for 表达式节点 ID 下，visitVarDeclStmt 消费后迁移到
	// ctx.LoopPortTypes（聚合变量 symbol → 端口名 → 元素类型）。
	pendingForPorts map[binding.AstNodeID]map[string]*types.Descriptor

	// subflowInputNames maps a subflow declaration's SymbolID to its name
	// (D-P31-1). The call-site argument map is the DECLARED input contract as
	// a record (binder.go 2.6), so the record-literal rules decide which keys
	// exist; this map tells visitMapLit that the map being visited IS a
	// subflow call-site argument, which downgrades a declared-but-unbound
	// field from a language error to a target-contract degradation warning
	// (the target feeds its own default) and scopes value checking away
	// (the platform input boundary takes the bound value as-is). Identity,
	// not name or shape: a shadowing local function is not a subflow.
	subflowInputNames map[binding.SymbolID]string

	// subflowInputPending carries the subflow name whose argument MapLit is
	// about to be visited (set by visitCallExpr, consumed once by visitMapLit
	// so nested map literals are unaffected). Empty = not a subflow argument.
	subflowInputPending string

	ctx  *Context
	snap *binding.Snapshot
}

// Run mirrors TypePass.run (type_pass.py:130-152): conversation defaults,
// then the entry, code functions and declared functions.
func (p *TypePass) Run(program *ast.Program, ctx *Context) {
	if ctx.Bindings == nil {
		panic("passes: TypePass requires a published binding snapshot (run ScopePass first)")
	}
	p.ctx = ctx
	p.snap = ctx.Bindings
	p.subflowInputNames = subflowSymbolNames(program, ctx.Bindings)
	p.subflowInputPending = ""
	p._returnType = nil
	p._expectedTypes = p._expectedTypes[:0]
	p._narrowedTypes = p._narrowedTypes[:0]
	p.scopeStack = p.scopeStack[:0]
	if p.pendingForPorts == nil {
		p.pendingForPorts = map[binding.AstNodeID]map[string]*types.Descriptor{}
	} else {
		clear(p.pendingForPorts)
	}

	for _, declaration := range program.ConversationDecls() {
		if declaration.Default() == nil {
			continue
		}
		expected := p.typeRefToDescriptor(declaration.TypeRef())
		actual := p.visitWithExpected(declaration.Default(), expected)
		if !types.IsAssignable(actual, expected) {
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
				"Conversation variable '%s' default expected %s, got %s",
				declaration.Name(), expected, actual), declaration.Default().Span())
		}
	}
	if program.Entry() != nil {
		p.checkFunction(program.Entry())
	}
	for _, cf := range program.CodeFunctions() {
		p.checkFunction(&cf.FunctionDef)
	}
	for _, fn := range program.Functions() {
		p.checkFunction(fn)
	}
}

// subflowSymbolNames collects SymbolID -> subflow name for the top-level
// subflow declarations (D-P31-1). The binder registers each subflow name as a
// global function whose single parameter is the declared input record; the
// type pass needs the identity to distinguish a subflow call-site argument map
// from an ordinary record literal. Nil-safe: an unbindable declaration is
// simply absent.
func subflowSymbolNames(program *ast.Program, snap *binding.Snapshot) map[binding.SymbolID]string {
	if program == nil || snap == nil {
		return nil
	}
	out := map[binding.SymbolID]string{}
	for _, sd := range program.SubflowDecls() {
		nodeID, ok := snap.NodeIDOf(sd)
		if !ok {
			continue
		}
		symID, ok := snap.DeclarationSymbols().Get(nodeID)
		if !ok {
			continue
		}
		out[symID] = sd.Name()
	}
	return out
}

// subflowCalleeName returns the subflow name when callee resolves to a
// subflow symbol, else "".
func (p *TypePass) subflowCalleeName(callee ast.Expr) string {
	if len(p.subflowInputNames) == 0 {
		return ""
	}
	symID, ok := p.referenceSymbol(callee)
	if !ok {
		return ""
	}
	return p.subflowInputNames[symID]
}

// visit dispatches to the per-node handler and records inferred expression
// types, mirroring TypePass.visit (type_pass.py:153-158): every Expr whose
// handler returns a descriptor publishes ctx.ExprTypes[nodeID] = result.
// Statements and blocks return nothing.
func (p *TypePass) visit(node ast.Node) *types.Descriptor {
	switch n := node.(type) {
	case *ast.Block:
		p.visitBlock(n)
		return nil
	case *ast.ElseIfBranch:
		// ElseIfBranch is a Node but not a Stmt (no stmtNode marker); the
		// Python visitor dispatches it via visit_else_if_branch
		// (type_pass.py:452-456).
		p.visitElseIfBranch(n)
		return nil
	case ast.Stmt:
		p.visitStmt(n)
		return nil
	case ast.Expr:
		d := p.visitExpr(n)
		if id, ok := p.nodeID(n); ok {
			p.ctx.ExprTypes[id] = d
		}
		return d
	default:
		panic("passes: TypePass.visit: unexpected node " + nodeKind(n))
	}
}

// visitStmt dispatches statement visitors (type_pass.py:282-634). Nodes
// without an explicit visitor in the reference (YieldStmt) fall
// back to ProcASTVisitor.generic_visit, which walks their children — the
// Go switch mirrors that by visiting the value expression.
func (p *TypePass) visitStmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.VarDeclStmt:
		p.visitVarDeclStmt(n)
	case *ast.AssignStmt:
		p.visitAssignStmt(n)
	case *ast.ExprStmt:
		if n.Expr() != nil {
			p.visit(n.Expr())
		}
	case *ast.IfStmt:
		p.visitIfStmt(n)
	case *ast.ForStmt:
		p.visitForStmt(n)
	case *ast.WhileStmt:
		p.visitWhileStmt(n)
	case *ast.ReturnStmt:
		p.visitReturnStmt(n)
	case *ast.YieldStmt:
		if n.Value() != nil {
			if al, ok := n.Value().(*ast.ArrayLit); ok && len(al.Elements()) == 0 {
				// 反编译状态变量空数组初值（X201：yield infos = []）：
				// ncoda 无数组类型注解语法，yield 位置无法推断元素类型，
				// 按 array<any> 放行（不报 EMPTY_AGGREGATE_TYPE）；渲染端
				// loop 输出口 schema 恢复泛型 object（原始 canvas 即
				// {"schema": [], "type": "object"}）。
				p.visitWithExpected(al, types.ArrayType(types.AnyType()))
			} else {
				p.visit(n.Value())
			}
		}
	case *ast.OutputStmt:
		p.visit(n.Value())
	case *ast.ParallelStmt:
		p.visitParallelStmt(n)
	case *ast.AttemptStmt:
		p.visitAttemptStmt(n)
	case *ast.RequestInputStmt:
		p.visitRequestInputStmt(n)
	case *ast.SwitchStmt:
		p.visitSwitchStmt(n)
	case *ast.BreakStmt, *ast.ContinueStmt:
		// pass (type_pass.py:624-629)
	default:
		// Python generic_visit walks children of any other statement; the
		// current AST has no other statement types reached here.
	}
}

// visitExpr dispatches expression visitors; every case returns the inferred
// descriptor (type_pass.py:635-1814 expression section).
func (p *TypePass) visitExpr(e ast.Expr) *types.Descriptor {
	switch n := e.(type) {
	case *ast.IntLit:
		return types.IntType()
	case *ast.FloatLit:
		return types.FloatType()
	case *ast.StringLit:
		return types.StringType()
	case *ast.BoolLit:
		return types.BoolType()
	case *ast.NullLit:
		return types.NullType()
	case *ast.TemplateString:
		return p.visitTemplateString(n)
	case *ast.TemplateValue:
		// `template "<逐字文本>" [using {…}]` 作为**表达式**（R-68 甲：llm message
		// content 位）。文本不参与类型（逐字承载、零解析），名额值是**读位置** ⇒
		// 逐个按普通值检查（未定义 / 未初始化 / 类型错照常报）。类型 = string：
		// 该形态只允许落在字符串承载面（平台的 prompt 槽是一个字符串），
		// 别的期望类型由这里的返回描述符与期望比对自然报错。
		for _, b := range n.Bindings() {
			if b.Value != nil {
				p.visit(b.Value)
			}
		}
		return types.StringType()
	case *ast.ArrayLit:
		return p.visitArrayLit(n)
	case *ast.MapLit:
		return p.visitMapLit(n)
	case *ast.IdentExpr:
		return p.visitIdentExpr(n)
	case *ast.FieldAccessExpr:
		return p.visitFieldAccessExpr(n)
	case *ast.IndexExpr:
		return p.visitIndexExpr(n)
	case *ast.CallExpr:
		return p.visitCallExpr(n)
	case *ast.OperationPolicyExpr:
		return p.visitOperationPolicyExpr(n)
	case *ast.UnaryExpr:
		return p.visitUnaryExpr(n)
	case *ast.BinaryExpr:
		return p.visitBinaryExpr(n)
	case *ast.TernaryExpr:
		return p.visitTernaryExpr(n)
	case *ast.LambdaExpr:
		return p.visitLambdaExpr(n)
	case *ast.ForExpr:
		return p.visitForExpr(n, n)
	case *ast.ParallelForExpr:
		return p.visitParallelForExpr(n)
	case *ast.ForeignCodeExpr:
		return p.visitForeignCodeExpr(n)
	default:
		panic("passes: TypePass.visitExpr: unexpected expression " + nodeKind(n))
	}
}

// nodeKind renders a node's Go type name for panic messages.
func nodeKind(n ast.Node) string {
	return fmt.Sprintf("%T", n)
}

// ── function / block traversal (type_pass.py:160-204) ──────────────────

// checkFunction mirrors _check_function (type_pass.py:160-195): omitted
// return types are intentionally unconstrained; parameter defaults are
// visited inside the function scope; the body statements are visited
// directly (the body block's recorded scope is the function scope itself).
func (p *TypePass) checkFunction(fn *ast.FunctionDef) {
	if fn.ReturnType() != nil {
		p._returnType = p.typeRefToDescriptor(fn.ReturnType())
	} else {
		p._returnType = nil
		p._inferredReturnType = nil
	}
	recorded, found := p.scopeOf(fn)
	if !found {
		for _, stmt := range fn.Body().Stmts() {
			p.visit(stmt)
		}
		return
	}
	p.pushScope(recorded)
	defer p.popScope()
	for _, parameter := range fn.Params() {
		if parameter.Default() == nil {
			continue
		}
		expected := p.typeRefToDescriptor(parameter.TypeRef())
		actual := p.visitWithExpected(parameter.Default(), expected)
		if !types.IsAssignable(actual, expected) {
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
				"Parameter '%s' default expected %s, got %s",
				parameter.Name(), expected, actual), parameter.Default().Span())
		}
	}
	for _, stmt := range fn.Body().Stmts() {
		p.visit(stmt)
	}
}

// checkBlockWithScope mirrors _check_block_with_scope (type_pass.py:196-204):
// walk a block using its recorded scope, falling back to a scope-less walk
// when the block never received a scope binding.
func (p *TypePass) checkBlockWithScope(block *ast.Block) {
	if block == nil {
		return
	}
	recorded, found := p.scopeOf(block)
	if !found {
		p.visitBlock(block)
		return
	}
	p.pushScope(recorded)
	defer p.popScope()
	p.visitBlock(block)
}

// visitBlock mirrors visit_block (type_pass.py:630-634).
func (p *TypePass) visitBlock(block *ast.Block) {
	for _, stmt := range block.Stmts() {
		p.visit(stmt)
	}
}

func (p *TypePass) pushScope(sid binding.ScopeID) { p.scopeStack = append(p.scopeStack, sid) }
func (p *TypePass) popScope()                     { p.scopeStack = p.scopeStack[:len(p.scopeStack)-1] }

// ── expected / narrowed type stacks (T7, type_pass.py:206-281) ─────────

// withNarrowed mirrors _with_narrowed_types (type_pass.py:206-214).
func (p *TypePass) withNarrowed(narrowed map[binding.SymbolID]*types.Descriptor, fn func()) {
	p._narrowedTypes = append(p._narrowedTypes, narrowed)
	defer func() { p._narrowedTypes = p._narrowedTypes[:len(p._narrowedTypes)-1] }()
	fn()
}

// conditionNarrowing mirrors _condition_narrowing (type_pass.py:216-238):
// only `ident == null` / `ident != null` binary conditions narrow an
// optional symbol to its value type.
func (p *TypePass) conditionNarrowing(condition ast.Expr) (thenNarrowed, elseNarrowed map[binding.SymbolID]*types.Descriptor) {
	thenNarrowed = map[binding.SymbolID]*types.Descriptor{}
	elseNarrowed = map[binding.SymbolID]*types.Descriptor{}
	bin, ok := condition.(*ast.BinaryExpr)
	if !ok || (bin.Op() != "==" && bin.Op() != "!=") {
		return thenNarrowed, elseNarrowed
	}
	var ident *ast.IdentExpr
	if l, ok := bin.Left().(*ast.IdentExpr); ok {
		if _, ok := bin.Right().(*ast.NullLit); ok {
			ident = l
		}
	} else if r, ok := bin.Right().(*ast.IdentExpr); ok {
		if _, ok := bin.Left().(*ast.NullLit); ok {
			ident = r
		}
	}
	if ident == nil {
		return thenNarrowed, elseNarrowed
	}
	symID, found := p.referenceSymbol(ident)
	if !found {
		return thenNarrowed, elseNarrowed
	}
	symType := p.symbolType(symID)
	if symType == nil || symType.Kind != types.Optional || symType.Elem == nil {
		return thenNarrowed, elseNarrowed
	}
	narrowed := map[binding.SymbolID]*types.Descriptor{symID: symType.Elem}
	if bin.Op() == "!=" {
		return narrowed, elseNarrowed
	}
	return thenNarrowed, narrowed
}

// validateConditionType mirrors _validate_condition_type (type_pass.py:240-255).
func (p *TypePass) validateConditionType(condition ast.Expr, t *types.Descriptor) {
	switch {
	case t.Kind == types.Any:
		p.emit(diag.ANY_NOT_NARROWED,
			"Condition requires a proven bool; narrow or convert the any value first", spanOf(condition))
	case t.Kind == types.Optional:
		p.emit(diag.OPTIONAL_REQUIRED,
			"Condition requires a non-optional bool; check the value against null first", spanOf(condition))
	case t.Kind != types.Bool:
		p.emit(diag.CONDITION_NOT_BOOL,
			fmt.Sprintf("Condition must be bool, got %s", t), spanOf(condition))
	}
}

// visitWithExpected mirrors _visit_with_expected (type_pass.py:266-281).
func (p *TypePass) visitWithExpected(node ast.Expr, expected *types.Descriptor) *types.Descriptor {
	if node == nil {
		return types.AnyType()
	}
	if expected == nil {
		return p.visit(node)
	}
	p._expectedTypes = append(p._expectedTypes, expected)
	defer func() { p._expectedTypes = p._expectedTypes[:len(p._expectedTypes)-1] }()
	return p.visit(node)
}

// ── statement visitors (type_pass.py:282-634) ──────────────────────────

func (p *TypePass) visitVarDeclStmt(n *ast.VarDeclStmt) {
	var expected *types.Descriptor
	if n.TypeRef() != nil {
		expected = p.typeRefToDescriptor(n.TypeRef())
	}
	if n.Init() == nil {
		return
	}
	initType := p.visitWithExpected(n.Init(), expected)
	// 展平值循环绑定（`var x = for (…) { yield <list>; };`）：绑定的值**就是**该口
	// （`list<T>`），不再包一层 —— 与 IR 同解（`pkg/ir/build_stmt.go` 的
	// `if flattenOutput && itemType.Kind == "list" { outputType = itemType }`；
	// flattenOutput 的来源 = 绑定是 `var`，见 pkg/ir/build_call.go）。
	// 没有这一条，ncoda 自己声明的展平语义在**类型层**不成立：一手 2026-09-27
	// `var results = for (item in [1,2,3]) { yield [item, item*2]; }; return results;`
	// 配 `-> array<int>` 被判 RETURN_TYPE（expected array<int>，got
	// array<array<int>>），而产物端点名额与 IR 都是 array<int> —— 同一语义两把尺。
	if n.IsMutable() {
		if flat, ok := p.flattenForBindingType(n.Init()); ok {
			initType = flat
		}
	}
	if expected != nil && !types.IsAssignable(initType, expected) {
		p.emit(diag.TYPE_MISMATCH, fmt.Sprintf("Cannot assign %s to %s", initType, expected), n.Span())
	}
	symID, found := p.stmtSymbol(n)
	if !found {
		return
	}
	// 多口 for-expr 聚合：把 visitForExpr 暂存的端口类型挂到承载符号
	// （agg_<loop>.<port> 字段访问通过 LoopPortTypes 解析）。
	p.bindForPorts(symID, n.Init())
	if expected == nil {
		p.ctx.VarTypes[symID] = initType
	}
	if p.isDirectCodeValue(n.Init()) {
		p.ctx.MarkDirectCodeSymbol(symID)
	}
}

// flattenForBindingType 返回展平值循环绑定的类型（`var x = for (…) { … }` 且该口
// 元素是数组 ⇒ 绑定类型 = 该口元素类型本身）。
//
// 端口选择与 decompile 侧 `forExprValueType`（platforms/cozebiz/decompile）**同一把
// 尺**：有 `output` 口取它，否则唯一口；多口且无主口 ⇒ 不判（值类型落 any，与本
// pass 既有口径同侧）。`ok=false` ⇒ 调用方保持 `array<elem>`（普通值循环）。
func (p *TypePass) flattenForBindingType(value ast.Expr) (*types.Descriptor, bool) {
	if _, ok := value.(*ast.ForExpr); !ok {
		return nil, false
	}
	nid, ok := p.nodeID(value)
	if !ok {
		return nil, false
	}
	ports, ok := p.pendingForPorts[nid]
	if !ok || len(ports) == 0 {
		return nil, false
	}
	elem, ok := ports["output"]
	if !ok {
		if len(ports) != 1 {
			return nil, false
		}
		for _, t := range ports {
			elem = t
		}
	}
	if elem == nil || elem.Kind != types.Array {
		return nil, false
	}
	return elem, true
}

// bindForPorts 把多口 for/parallel-for 表达式暂存的端口类型挂到**承载符号**
// （`agg_<loop>.<port>` 字段访问由 type_infer 的 types.Array + LoopPortTypes
// 分支解析）。端口身份属于**符号**，与绑定形态无关 ⇒ 两种形态必须同解：
//
//	① 声明位绑定 `let agg = for(…) { … }`   —— visitVarDeclStmt（语法糖）；
//	② 赋值位绑定 `var agg; agg = for(…) { … }` —— **变量提升**的物化形态：
//	   P5 栅栏逃逸（臂内写、臂外读）要求「外层声明 + 臂内赋值」，编译器自己的
//	   raising（platforms/cozebiz/decompile 的 hoistParallelEscapes）就产出
//	   这一形态，且多口聚合的类型在提升后无处安放（裸 `var`）。
//
// 只认形态 ① 会让形态 ② 的端口访问**静默退化成 any**（一手 2026-09-22：
// X38 的 `agg_1.otherInfos_list` ⇒ ANY_NOT_NARROWED；同批另有 13 源同为无类型
// 提升，其中 6 源因消费方接受 any 而侥幸通过 —— 静默降级不报错）。
func (p *TypePass) bindForPorts(symID binding.SymbolID, value ast.Expr) {
	switch value.(type) {
	case *ast.ForExpr, *ast.ParallelForExpr:
	default:
		return
	}
	nid, ok := p.nodeID(value)
	if !ok {
		return
	}
	if ports, found := p.pendingForPorts[nid]; found {
		p.ctx.LoopPortTypes[symID] = ports
	}
}

func (p *TypePass) visitAssignStmt(n *ast.AssignStmt) {
	targetType := p.visit(n.Target())
	if n.Operation() == "<<" {
		elemType := arrayElementType(targetType)
		valueType := p.visit(n.Value())
		if !types.IsAssignable(valueType, elemType) {
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf("Cannot append %s to %s", valueType, targetType), spanOfOr(n.Value(), n.Span()))
		}
		return
	}
	valueType := p.visitWithExpected(n.Value(), targetType)
	assignable := types.IsAssignable(valueType, targetType)
	var joinedType *types.Descriptor
	if !assignable {
		// 裸声明变量（var x; 无类型）流类型放宽：分支聚合消解赋值可能
		// 单向兼容（file vs string，X174：img(file) / v165070.data(string)），
		// JoinAggregateTypes 取公共父类型（file+string→string）后放行并
		// 收窄为宽类型；真不兼容（int vs string）join 仍为 nil → 保持报错。
		if ident, ok := n.Target().(*ast.IdentExpr); ok {
			if symID, found := p.referenceSymbol(ident); found {
				decl, hasDecl := p.snap.Declarations().Get(symID)
				existing, hasVar := p.ctx.VarTypes[symID]
				joined := types.JoinAggregateTypes(existing, valueType)
				if hasDecl && (decl.Type == nil || decl.Type.Kind == types.Any) && hasVar {
					if joined != nil {
						assignable = true
						joinedType = joined
					}
				}
			}
		}
	}
	if !assignable {
		p.emit(diag.TYPE_MISMATCH, fmt.Sprintf("Cannot assign %s to %s", valueType, targetType), spanOfOr(n.Value(), n.Span()))
	}
	// 流敏感：赋值成功后更新变量类型。分支变量提升模式
	// （var v; + 分支内 v = <expr>）下，v 的后续引用（coze 跨分支引用，
	// merge/coalesce）需要最新类型而非声明时的 any。
	if assignable {
		if ident, ok := n.Target().(*ast.IdentExpr); ok {
			if symID, found := p.referenceSymbol(ident); found {
				// 赋值位绑定多口 for 聚合：与声明位同解（见 bindForPorts）。
				p.bindForPorts(symID, n.Value())
				// An explicit declaration is a stable type contract. Do not let
				// an earlier literal assignment (for example `float x; x = 0`)
				// narrow the symbol to int and make a later, correctly typed
				// plugin output (`x = result.data.duration`) fail. Flow-sensitive
				// updates are only for inferred/hoisted declarations.
				decl, hasDecl := p.snap.Declarations().Get(symID)
				if !hasDecl || decl.Type == nil || decl.Type.Kind == types.Any {
					if joinedType != nil {
						p.ctx.VarTypes[symID] = joinedType
					} else {
						p.ctx.VarTypes[symID] = valueType
					}
				}
			}
		}
	}
}

func (p *TypePass) visitAttemptStmt(n *ast.AttemptStmt) {
	resultType := p.visit(n.Operation())
	spec := operationSpec(n.Operation(), p.ctx)
	if spec == nil || !spec.Policies.FailureBranch {
		p.emit(diag.OPERATION_POLICY,
			"Attempted operation does not support a failure branch", spanOfOr(n.Operation(), n.Span()))
	}
	if op, ok := n.Operation().(*ast.OperationPolicyExpr); ok {
		for _, policy := range op.Policies() {
			if _, ok := policy.(*ast.DefaultPolicy); ok {
				p.emit(diag.OPERATION_POLICY,
					"default policy cannot be combined with an explicit failure branch", op.Span())
				break
			}
		}
	}
	if symID, found := p.stmtSymbol(n.ResultBinding()); found {
		p.ctx.VarTypes[symID] = resultType
	}
	if symID, found := p.stmtSymbol(n.ErrorBinding()); found {
		p.ctx.VarTypes[symID] = operationErrorType()
	}
	p.checkBlockWithScope(n.SuccessBody())
	p.checkBlockWithScope(n.FailureBody())
}

func (p *TypePass) visitRequestInputStmt(n *ast.RequestInputStmt) {
	p.visitHumanInputConfigMap(n.Config())
	formType := p.typeRefToDescriptor(n.FormType())
	_, formIsNamed := n.FormType().(*ast.NamedTypeRef)
	validForm := formIsNamed && formType.Kind == types.Record
	if !validForm {
		p.emit(diag.TYPE_MISMATCH, "Human Input form type must be a nominal record", n.Span())
	}
	// **无动作形态**（S1，`ask<Form>({content, fields}) as r;`）：没有 action
	// 类型可校验 ⇒ 不查 enum、不查成员覆盖、不查重复臂（臂集合恒为 ∅，由
	// parser 保证）。有动作形态一字不改。
	var actionType *types.Descriptor
	validAction := false
	if at := n.ActionType(); at != nil {
		actionType = p.typeRefToDescriptor(at)
		_, actionIsNamed := at.(*ast.NamedTypeRef)
		validAction = actionIsNamed && actionType.Kind == types.Enum && types.IsWellFormedEnum(actionType)
		if !validAction {
			p.emit(diag.TYPE_MISMATCH, "Human Input action type must be a nominal enum", n.Span())
		}
	}

	members := []string{}
	for _, branch := range n.ActionBranches() {
		if branch.ActionExpr() != nil {
			p.visit(branch.ActionExpr())
		}
		var fact binding.EnumMemberFact
		found := false
		if branch.ActionExpr() != nil {
			if id, ok := p.nodeID(branch.ActionExpr()); ok {
				fact, found = p.ctx.EnumMembers[id]
			}
		}
		if !found || !validAction || !fact.EnumType.Equal(actionType) {
			p.emit(diag.TYPE_MISMATCH,
				"Human Input action branch must name a member of the declared action enum", branch.Span())
		} else {
			members = append(members, fact.Member)
		}
	}
	duplicateMembers := duplicatesSorted(members)
	if len(duplicateMembers) > 0 {
		p.emit(diag.TYPE_MISMATCH,
			"Human Input has duplicate action branches: "+strings.Join(duplicateMembers, ", "), n.Span())
	}
	if validAction {
		missing := missingSorted(actionType.EnumMembers, members)
		if len(missing) > 0 {
			p.emit(diag.TYPE_MISMATCH,
				"Human Input is missing action branches: "+strings.Join(missing, ", "), n.Span())
		}
	}
	for _, branch := range n.ActionBranches() {
		p._subscopeDepth++
		p.checkBlockWithScope(branch.Body())
		p._subscopeDepth--
	}
	p._subscopeDepth++
	p.checkBlockWithScope(n.TimeoutBody())
	p._subscopeDepth--
}

// visitHumanInputConfigMap mirrors _visit_human_input_config_map
// (type_pass.py:428-439): type static configuration leaves without inferring
// one heterogeneous map value type.
func (p *TypePass) visitHumanInputConfigMap(n *ast.MapLit) {
	for _, entry := range n.Entries() {
		if id, ok := p.nodeID(entry.Key); ok {
			p.ctx.ExprTypes[id] = types.StringType()
		}
		if ml, ok := entry.Value.(*ast.MapLit); ok {
			p.visitHumanInputConfigMap(ml)
		} else if entry.Value != nil {
			p.visit(entry.Value)
		}
	}
	if id, ok := p.nodeID(n); ok {
		p.ctx.ExprTypes[id] = types.MapType(types.StringType(), types.AnyType())
	}
}

func (p *TypePass) visitSwitchStmt(n *ast.SwitchStmt) {
	queryType := p.visit(n.Query())
	// 平台 query 是 string ref；非标量（数组/record/map）无法作为意图
	// 分类输入 → 硬错。any/string/模板串放行（反编译 v<id>.data 等 any
	// 输出是常见来源，X166 实证）。
	switch queryType.Kind {
	case types.Array, types.Record, types.Map:
		p.emit(diag.TYPE_MISMATCH,
			fmt.Sprintf("intent query must be a string-compatible value, got %s", queryType), n.Span())
	}
	p.visitSwitchConfigMap(n.Config())
	for _, br := range n.Branches() {
		p._subscopeDepth++
		p.checkBlockWithScope(br.Body())
		p._subscopeDepth--
	}
	if n.DefaultBody() != nil {
		p._subscopeDepth++
		p.checkBlockWithScope(n.DefaultBody())
		p._subscopeDepth--
	}
}

// visitSwitchConfigMap 静态配置 map：与 visitHumanInputConfigMap 一致，
// 类型标注不推断异构值类型。
func (p *TypePass) visitSwitchConfigMap(n *ast.MapLit) {
	for _, entry := range n.Entries() {
		if id, ok := p.nodeID(entry.Key); ok {
			p.ctx.ExprTypes[id] = types.StringType()
		}
		if ml, ok := entry.Value.(*ast.MapLit); ok {
			p.visitSwitchConfigMap(ml)
		} else if entry.Value != nil {
			p.visit(entry.Value)
		}
	}
	if id, ok := p.nodeID(n); ok {
		p.ctx.ExprTypes[id] = types.MapType(types.StringType(), types.AnyType())
	}
}

func (p *TypePass) visitIfStmt(n *ast.IfStmt) {
	conditionType := p.visit(n.Condition())
	p.validateConditionType(n.Condition(), conditionType)
	thenNarrowed, elseNarrowed := p.conditionNarrowing(n.Condition())
	p.withNarrowed(thenNarrowed, func() { p.checkBlockWithScope(n.ThenBody()) })
	for _, ei := range n.ElseIfs() {
		p.visit(ei)
	}
	if n.ElseBody() != nil {
		p.withNarrowed(elseNarrowed, func() { p.checkBlockWithScope(n.ElseBody()) })
	}
}

func (p *TypePass) visitElseIfBranch(n *ast.ElseIfBranch) {
	conditionType := p.visit(n.Condition())
	p.validateConditionType(n.Condition(), conditionType)
	p.checkBlockWithScope(n.Body())
}

func (p *TypePass) visitForStmt(n *ast.ForStmt) {
	p.bindLoopVars(n, n.LoopSources(), n.LoopVars(), n.Body(), false)
	p._subscopeDepth++
	defer func() { p._subscopeDepth-- }()
	p.checkBlockWithScope(n.Body())
}

// bindLoopVars 绑定循环变量类型：单变量走 stmtSymbol（保持原语义），
// zip 多变量逐源绑定（zipVarSymbol 按 body scope 声明名解析）。
func (p *TypePass) bindLoopVars(loopNode ast.Node, sources []ast.Expr, vars []string, body *ast.Block, isExpr bool) {
	for i, src := range sources {
		iterType := p.visit(src)
		what := "For loop iteration"
		if isExpr {
			what = "For expression iteration"
		}
		switch iterType.Kind {
		case types.Array:
			var symID binding.SymbolID
			var found bool
			if len(sources) > 1 {
				symID, found = p.zipVarSymbol(body, vars[i])
			} else {
				symID, found = p.stmtSymbol(loopNode)
			}
			if found {
				p.ctx.VarTypes[symID] = iterType.Elem
			}
		case types.Any:
			p.emit(diag.ANY_NOT_NARROWED, what+" requires a proven array, not any", spanOf(src))
		default:
			p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(what+" must be array, got %s", iterType), spanOf(src))
		}
	}
}

// zipVarSymbol 在循环体 scope 的 Decls 里按名字找 zip 循环变量 symbol
// （binder 多变量注册时 DeclarationSymbols[loop] 只保留最后一个，zip
// 其余变量须按名解析）。
func (p *TypePass) zipVarSymbol(body *ast.Block, name string) (binding.SymbolID, bool) {
	sid, ok := p.scopeOf(body)
	if !ok {
		return 0, false
	}
	info, ok := p.snap.Scopes().Get(sid)
	if !ok {
		return 0, false
	}
	for _, declID := range info.Decls {
		if decl, ok := p.snap.Declarations().Get(declID); ok && decl.Name == name {
			return declID, true
		}
	}
	return 0, false
}

// visitForExpr mirrors visit_for_expr (type_pass.py:482-537): returns
// ArrayType of the body's owned-yield value types. forNode is the loop node
// that carries the loop-variable declaration in the snapshot: the ForExpr
// itself when visiting a bare for-expr, the ParallelForExpr when the
// parallel-for visitor delegates here (Python visit_parallel_for_expr
// delegates with the ParallelForExpr node, whose DeclarationSymbols entry
// the binder registers against the parallel-for node).
func (p *TypePass) visitForExpr(n *ast.ForExpr, forNode ast.Node) *types.Descriptor {
	p.bindLoopVars(forNode, n.LoopSources(), n.LoopVars(), n.Body(), true)
	if len(n.Body().Stmts()) == 0 {
		p.emitWarning(diag.EMPTY_FOR_BODY, "For expression body is empty", n.Span())
	}
	p._subscopeDepth++
	defer func() { p._subscopeDepth-- }()
	p.checkBlockWithScope(n.Body())
	type typedYield struct {
		statement *ast.YieldStmt
		typ       *types.Descriptor
		port      string
	}
	typedYields := []typedYield{}
	for _, statement := range p.ownedYields(n.Body()) {
		if statement.Value() == nil {
			continue
		}
		t := types.AnyType()
		if id, ok := p.nodeID(statement.Value()); ok {
			if inferred, found := p.ctx.ExprTypes[id]; found {
				t = inferred
			}
		}
		port := statement.Port()
		if port == "" {
			port = "output"
		}
		typedYields = append(typedYields, typedYield{statement: statement, typ: t, port: port})
	}
	// 多口聚合（roundtrip loop/batch outputs 多端口）：join 按端口分组，
	// 不同命名口不互相 join（yield output = string; yield duration_list = int
	// 是合法多口发布）。返回值取主口 "output"（无 output 口时取唯一口）。
	bodyElemType := types.AnyType()
	if len(typedYields) > 0 {
		portTypes := map[string]*types.Descriptor{}
		for _, pair := range typedYields {
			if cur, ok := portTypes[pair.port]; ok {
				joined := types.JoinAggregateTypes(cur, pair.typ)
				if joined == nil {
					p.emit(diag.TYPE_MISMATCH, fmt.Sprintf(
						"Yield value has incompatible type %s; expected %s", pair.typ, cur), pair.statement.Span())
					portTypes[pair.port] = types.AnyType()
				} else {
					portTypes[pair.port] = joined
				}
			} else {
				portTypes[pair.port] = pair.typ
			}
		}
		if t, ok := portTypes["output"]; ok {
			bodyElemType = t
		} else if len(portTypes) == 1 {
			for _, t := range portTypes {
				bodyElemType = t
			}
		}
		// 发布附加聚合端口（多口 roundtrip）：agg_<loop>.<port> 字段访问
		// 经 visitVarDeclStmt 迁移到 LoopPortTypes（聚合变量 symbol）后
		// 由 type_infer 解析端口元素类型。
		if len(portTypes) > 0 {
			if nid, ok := p.nodeID(forNode); ok {
				p.pendingForPorts[nid] = portTypes
			}
		}
	}
	return types.ArrayType(bodyElemType)
}

// visitParallelForExpr mirrors visit_parallel_for_expr (type_pass.py:538-562).
func (p *TypePass) visitParallelForExpr(n *ast.ParallelForExpr) *types.Descriptor {
	result := p.visitForExpr(&n.ForExpr, n)
	// 配置子句可省（2026-09-29 语言级放宽）：`Concurrency() == 0` = 作者没写 ⇒ 用
	// 目标缺省（取值唯一处 = `pkg/ir/parallel_for_defaults.go`），不由本 pass
	// 填补。显式写的非正并发度在**语法层**就拒了（0 与「未设」在 AST 上不可分辨，
	// 见 `pkg/parser/parallel_for_config.go`）。
	if n.Concurrency() > p.ctx.Profile.MaxParallelism {
		p.emit(diag.TARGET_NOT_LOWERABLE, fmt.Sprintf(
			"Parallel-for concurrency %d exceeds target maximum %d",
			n.Concurrency(), p.ctx.Profile.MaxParallelism), n.Span())
	}
	if n.OnError() == "keep_null" && result.Kind == types.Array {
		return types.ArrayType(types.OptionalType(result.Elem))
	}
	return result
}

func (p *TypePass) visitWhileStmt(n *ast.WhileStmt) {
	conditionType := p.visit(n.Condition())
	p.validateConditionType(n.Condition(), conditionType)
	thenNarrowed, _ := p.conditionNarrowing(n.Condition())
	p.withNarrowed(thenNarrowed, func() {
		p._subscopeDepth++
		defer func() { p._subscopeDepth-- }()
		p.checkBlockWithScope(n.Body())
	})
}

func (p *TypePass) visitReturnStmt(n *ast.ReturnStmt) {
	// `return template "…" [using {…}]`（W2）：值侧是**流程答复端点**的模板 +
	// 名额表，不走值通道（平台 `terminatePlan` 二选一）⇒ 不参与返回类型契约
	// （既不与 `-> T` 比对，也不锚定隐式返回类型）。名额值仍是**读位置**，
	// 逐个按普通值检查（未定义/未初始化/类型错照常报）。
	if tr, ok := n.Value().(*ast.TemplateValue); ok {
		for _, b := range tr.Bindings() {
			if b.Value != nil {
				p.visit(b.Value)
			}
		}
		return
	}
	var valueType *types.Descriptor
	if n.Value() != nil {
		if inner, ok := exitDefaultKeyedSugar(n.Value()); ok {
			// A12-H20（2026-09-17 用户裁定 (a)）：**出口位置**的单条默认键形是糖 ——
			// `return { $output: x };` ≡ `return x;` 在**类型层**也成立。否则作者不能自由
			// 切换两种写法：同一函数里两种写法并列 ⇒ RETURN_TYPE；写了 `-> T` 时用显式形
			// 也报错（一手实测见 docs/five-step/rules/A12-end-binding.yaml 的 A12-H20）。
			//
			// 判据与 lowering 的归一化点（pkg/ir/ports.go `soleDefaultKeyedExit`）以及
			// raising 的读回糖形同源：**单条**入口（多入口是一张真的具名输出表，其中名为
			// `output` 的入口只是一个名字）∧ 键 = 语言常量 `ast.FlowExitDefaultKey`。
			// 这是**位置规则**：等价只在流程出口位置成立；别的表达式位置上
			// `{ $output: x }` 仍是一张普通的单字段 record。
			//
			// 值的**读检查**（未定义 / 未初始化 / 类型错）落在内层表达式上，与糖形逐字同一
			// 条通路 ⇒ 两种写法给出同一张诊断表。
			valueType = p.visitWithExpected(inner, p._returnType)
			p.publishExitSugarMapType(n.Value(), valueType)
		} else {
			valueType = p.visitWithExpected(n.Value(), p._returnType)
		}
	}
	if p._returnType == nil {
		// No declared return type: the return-value type contract is
		// enforced across exits instead of against a declaration. The
		// first value-return anchors the implicit type; later exits must
		// be mutually assignable (int/float do NOT widen to each other, so
		// a mixed int/float function is rejected). Bare `return;` exits
		// carry no value and never anchor or constrain.
		if n.Value() != nil && p._subscopeDepth == 0 {
			if p._inferredReturnType == nil {
				p._inferredReturnType = valueType
			} else if !types.IsAssignable(valueType, p._inferredReturnType) ||
				!types.IsAssignable(p._inferredReturnType, valueType) {
				p.emit(diag.RETURN_TYPE,
					fmt.Sprintf("Return type mismatch: function return type inferred as %s from an earlier return, got %s",
						p._inferredReturnType, valueType), n.Span())
			}
		}
		return
	}
	returnType := p._returnType
	if valueType == nil {
		if returnType.Kind != types.Void {
			p.emit(diag.RETURN_TYPE,
				fmt.Sprintf("Return value required for function returning %s", returnType), n.Span())
		}
		return
	}
	if !types.IsAssignable(valueType, returnType) {
		p.emit(diag.RETURN_TYPE,
			fmt.Sprintf("Return type mismatch: expected %s, got %s", returnType, valueType), n.Span())
	}
}

// exitDefaultKeyedSugar 判定「单条入口 ∧ 静态键 = 语言默认键」的**出口** map 字面量，
// 返回其值表达式（A12-H20 / W1-D2′ 裁定 (II) 的**类型侧**判据）。
//
// 与 `pkg/ir.soleDefaultKeyedExit`（lowering 归一化）逐字同源：「单条」是判据的
// 一部分 —— 多入口的 map 是一张真的具名输出表，其中名为 `output` 的入口只是一个名字；
// 键的比较对象是语言常量（`ast.FlowExitDefaultKey`），不是拼写（`$output` 的 `$` 已由
// 词法剥掉，见 internal/lexer）。
func exitDefaultKeyedSugar(value ast.Expr) (ast.Expr, bool) {
	m, ok := value.(*ast.MapLit)
	if !ok || m == nil || len(m.Entries()) != 1 {
		return nil, false
	}
	entry := m.Entries()[0]
	name, ok := ast.StaticMapKey(entry.Key)
	if !ok || name != ast.FlowExitDefaultKey {
		return nil, false
	}
	return entry.Value, true
}

// publishExitSugarMapType 给糖形的 map 字面量节点补一条类型事实。
//
// 为什么必须补：binder 的 `requireCompleteAggregateFacts`（pkg/binding/typed.go）
// 要求**每个** MapLit 节点都发布事实，而糖形的节点不再走 `visitMapLit`（它的值的类型
// 就是返回值的类型，不该再与 `-> T` 比对一次）。这里发布的就是它在语言层的形状：
// 单字段 record，字段名 = 语言默认键，字段类型 = 值的类型 —— 与走 `visitMapLit` 时
// 得到的形状一致（不引入新的下游差异），并把静态键的类型登记为 string，与
// `p.staticMapKey` 的副作用对齐。
func (p *TypePass) publishExitSugarMapType(value ast.Expr, fieldType *types.Descriptor) {
	m, ok := value.(*ast.MapLit)
	if !ok || m == nil {
		return
	}
	for _, entry := range m.Entries() {
		if id, ok := p.nodeID(entry.Key); ok {
			p.ctx.ExprTypes[id] = types.StringType()
		}
	}
	if id, ok := p.nodeID(m); ok {
		p.ctx.ExprTypes[id] = types.RecordType("", []types.RecordField{
			{Name: ast.FlowExitDefaultKey, Type: fieldType},
		})
	}
}

func (p *TypePass) visitOutputStmt(n *ast.OutputStmt) {
	p.visit(n.Value())
}

func (p *TypePass) visitParallelStmt(n *ast.ParallelStmt) {
	for _, branch := range n.Branches() {
		p.checkBlockWithScope(branch)
	}
}

// ── symbol / scope helpers (Task 15 T2/T4/T5) ──────────────────────────

// nodeID resolves the snapshot AstNodeID for n (exact enumeration lookup).
func (p *TypePass) nodeID(n ast.Node) (binding.AstNodeID, bool) {
	if n == nil {
		return 0, false
	}
	return p.snap.NodeIDOf(n)
}

// stmtSymbol mirrors ctx.stmt_symbols (DeclarationSymbols): the symbol
// declared by the node.
func (p *TypePass) stmtSymbol(n ast.Node) (binding.SymbolID, bool) {
	id, ok := p.nodeID(n)
	if !ok {
		return 0, false
	}
	return p.snap.DeclarationSymbols().Get(id)
}

// referenceSymbol mirrors ctx.expr_symbols (ReferenceSymbols): the symbol a
// reference expression resolved to.
func (p *TypePass) referenceSymbol(n ast.Node) (binding.SymbolID, bool) {
	id, ok := p.nodeID(n)
	if !ok {
		return 0, false
	}
	return p.snap.ReferenceSymbols().Get(id)
}

// symbolType returns the current type of a symbol: the VarTypes mutation
// surface first (T2, Python symbol.type_info after type-pass mutation),
// falling back to the snapshot's frozen declaration type.
func (p *TypePass) symbolType(symID binding.SymbolID) *types.Descriptor {
	if inferred, ok := p.ctx.VarTypes[symID]; ok {
		return inferred
	}
	decl, ok := p.snap.Declarations().Get(symID)
	if !ok {
		return types.AnyType()
	}
	return decl.Type
}

// scopeOf mirrors ctx.function_scopes / ctx.block_scopes lookups
// (ScopeBindings).
func (p *TypePass) scopeOf(n ast.Node) (binding.ScopeID, bool) {
	id, ok := p.nodeID(n)
	if !ok {
		return 0, false
	}
	return p.snap.ScopeBindings().Get(id)
}

// resolveSymbol mirrors ctx.scope_chain.resolve(name) (T4/T5): starting at
// the innermost activated recorded scope, walk the published Parent chain
// and return the SymbolID of the first declaration named name. The replay
// uses snap.Scopes (ScopeInfo.Parent + Decls) and snap.Declarations (name),
// the snapshot analogue of the live DSLScope.symbols dict. Deviation
// recorded: parallel-branch exported names (PublishSymbol) are not part of
// the snapshot's scope Decls, so resolution misses them (Python's live
// scope dict includes them); no ported test exercises that edge.
func (p *TypePass) resolveSymbol(name string) (binding.SymbolID, bool) {
	if len(p.scopeStack) == 0 {
		return 0, false
	}
	sid := p.scopeStack[len(p.scopeStack)-1]
	for sid != 0 {
		info, ok := p.snap.Scopes().Get(sid)
		if !ok {
			return 0, false
		}
		for _, declID := range info.Decls {
			if decl, ok := p.snap.Declarations().Get(declID); ok && decl.Name == name {
				return declID, true
			}
		}
		sid = info.Parent
	}
	return 0, false
}

// isDirectCodeValue mirrors _is_direct_code_value (type_pass.py:1860-1866):
// a ForeignCodeExpr with a direct output, or an identifier resolving to a
// marked direct-code symbol.
func (p *TypePass) isDirectCodeValue(node ast.Expr) bool {
	switch n := node.(type) {
	case *ast.ForeignCodeExpr:
		_, ok := n.Output().(*ast.CodeDirectOutput)
		return ok
	case *ast.IdentExpr:
		symID, found := p.resolveSymbol(n.Name())
		return found && p.ctx.DirectCodeSymbols[symID]
	}
	return false
}

// typeRefToDescriptor resolves a type reference against the Context's
// named-type environment (T1), the quiet variant (no UNKNOWN_TYPE
// diagnostics — the binder already reported unknown types at the scope
// stage, so the type pass only runs on clean binds).
func (p *TypePass) typeRefToDescriptor(ref ast.TypeRef) *types.Descriptor {
	return types.TypeRefToDescriptor(ref, func(name string) *types.Descriptor {
		return p.ctx.NamedTypes[name]
	})
}

// emit mirrors ctx.emit_diagnostic with Severity.ERROR.
func (p *TypePass) emit(code diag.Code, message string, span ast.Span) {
	p.ctx.Emit(code, diag.Error, message, span)
}

// emitWarning mirrors ctx.emit_diagnostic with Severity.WARNING (the only
// warning the type pass emits is EMPTY_FOR_BODY, type_pass.py:517-521).
func (p *TypePass) emitWarning(code diag.Code, message string, span ast.Span) {
	p.ctx.Emit(code, diag.Warning, message, span)
}

// spanOf returns node's span, or the zero span for a nil node. Callers
// that need the Python `x.span if x is not None else node.span` fallback
// must use spanOfOr with the statement span (visitAssignStmt /
// visitAttemptStmt).
func spanOf(n ast.Node) ast.Span {
	if n == nil {
		return ast.Span{}
	}
	return n.Span()
}

// spanOfOr mirrors Python's `x.span if x is not None else node.span`:
// fall back to the enclosing statement's span when the child is nil.
func spanOfOr(n ast.Node, statementSpan ast.Span) ast.Span {
	if n == nil {
		return statementSpan
	}
	return n.Span()
}

// duplicatesSorted mirrors `sorted(member for member in set(members) if
// members.count(member) > 1)`.
func duplicatesSorted(members []string) []string {
	counts := map[string]int{}
	for _, m := range members {
		counts[m]++
	}
	out := []string{}
	for _, m := range members {
		if counts[m] > 1 {
			out = append(out, m)
		}
	}
	out = dedupeSorted(out)
	return out
}

// missingSorted mirrors `sorted(set(all) - set(present))`.
func missingSorted(all, present []string) []string {
	have := map[string]bool{}
	for _, m := range present {
		have[m] = true
	}
	out := []string{}
	for _, m := range all {
		if !have[m] {
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

// dedupeSorted dedupes and sorts a string slice (the set() projection in
// the duplicate-members computation).
func dedupeSorted(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, m := range in {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}
