package ast

// This file ports the statement nodes from src/nclang/lang/ast.py
// (statements section). Field-for-field parity with the Python dataclasses.

// Block is a statement sequence: { <stmts> }.
type Block struct {
	base
	stmts []Stmt
}

// NewBlock constructs a Block.
func NewBlock(stmts []Stmt) *Block { return &Block{base: newBase(), stmts: stmts} }

// Stmts returns the contained statements.
func (n *Block) Stmts() []Stmt { return n.stmts }

func (n *Block) stmtNode()      {}
func (n *Block) String() string { return reprNode(n) }

// VarDeclStmt is a variable declaration:
//
//	<type> <name> = <expr>;  (typed)   or   var/let <name> = <expr>;  (inferred)
type VarDeclStmt struct {
	base
	name      string
	typeRef   TypeRef
	init      Expr
	isMutable bool
}

// NewVarDeclStmt constructs a VarDeclStmt.
func NewVarDeclStmt(name string, typeRef TypeRef, init Expr, isMutable bool) *VarDeclStmt {
	return &VarDeclStmt{base: newBase(), name: name, typeRef: typeRef, init: init, isMutable: isMutable}
}

// Name returns the declared name.
func (n *VarDeclStmt) Name() string { return n.name }

// TypeRef returns the declared type (nil = var/inferred).
func (n *VarDeclStmt) TypeRef() TypeRef { return n.typeRef }

// Init returns the initializer expression (nil = none).
func (n *VarDeclStmt) Init() Expr { return n.init }

// IsMutable reports whether the binding is mutable (false for 'let').
func (n *VarDeclStmt) IsMutable() bool { return n.isMutable }

func (n *VarDeclStmt) stmtNode()      {}
func (n *VarDeclStmt) String() string { return reprNode(n) }

// AssignStmt is an assignment: <target> = <value>; or <target> += <value>;.
type AssignStmt struct {
	base
	target    Expr
	value     Expr
	operation string
}

// NewAssignStmt constructs an AssignStmt.
func NewAssignStmt(target, value Expr, operation string) *AssignStmt {
	return &AssignStmt{base: newBase(), target: target, value: value, operation: operation}
}

// Target returns the assignment target.
func (n *AssignStmt) Target() Expr { return n.target }

// Value returns the assigned value.
func (n *AssignStmt) Value() Expr { return n.value }

// Operation returns the operation ("set" for plain assignment).
func (n *AssignStmt) Operation() string { return n.operation }

func (n *AssignStmt) stmtNode()      {}
func (n *AssignStmt) String() string { return reprNode(n) }

// ExprStmt is an expression used as a statement: <expr>;.
type ExprStmt struct {
	base
	expr Expr
}

// NewExprStmt constructs an ExprStmt.
func NewExprStmt(expr Expr) *ExprStmt { return &ExprStmt{base: newBase(), expr: expr} }

// Expr returns the wrapped expression.
func (n *ExprStmt) Expr() Expr { return n.expr }

func (n *ExprStmt) stmtNode()      {}
func (n *ExprStmt) String() string { return reprNode(n) }

// IfStmt is an if statement with optional else-if branches and else body.
type IfStmt struct {
	base
	condition Expr
	thenBody  *Block
	elseIfs   []*ElseIfBranch
	elseBody  *Block
}

// NewIfStmt constructs an IfStmt.
func NewIfStmt(condition Expr, thenBody *Block, elseIfs []*ElseIfBranch, elseBody *Block) *IfStmt {
	if thenBody == nil {
		thenBody = NewBlock(nil)
	}
	return &IfStmt{base: newBase(), condition: condition, thenBody: thenBody, elseIfs: elseIfs, elseBody: elseBody}
}

// Condition returns the if condition.
func (n *IfStmt) Condition() Expr { return n.condition }

// ThenBody returns the then-branch block.
func (n *IfStmt) ThenBody() *Block { return n.thenBody }

// ElseIfs returns the else-if branches (empty if none).
func (n *IfStmt) ElseIfs() []*ElseIfBranch { return n.elseIfs }

// ElseBody returns the else block (nil if none).
func (n *IfStmt) ElseBody() *Block { return n.elseBody }

func (n *IfStmt) stmtNode()      {}
func (n *IfStmt) String() string { return reprNode(n) }

// ElseIfBranch is one else-if branch: condition + body (ast.py ElseIfBranch).
type ElseIfBranch struct {
	base
	condition Expr
	body      *Block
}

// NewElseIfBranch constructs an ElseIfBranch.
func NewElseIfBranch(condition Expr, body *Block) *ElseIfBranch {
	if body == nil {
		body = NewBlock(nil)
	}
	return &ElseIfBranch{base: newBase(), condition: condition, body: body}
}

// Condition returns the branch condition.
func (n *ElseIfBranch) Condition() Expr { return n.condition }

// Body returns the branch body block.
func (n *ElseIfBranch) Body() *Block { return n.body }

func (n *ElseIfBranch) String() string { return reprNode(n) }

// ForStmt is a for statement: for (<var> in <iterable>) { ... }.
// Zip 形式（多输入源逐项配对，coze batch 多 InputSources）：
// for (a, b in zip(x, y)) { ... } —— 变量名即端口名（正编译还原输入字段名）。
type ForStmt struct {
	base
	varNames    []string  // 循环变量（单变量 len=1；zip 多变量）
	varTypes    []TypeRef // 对应变量类型注解（可 nil）
	sources     []Expr    // 循环源（单源 len=1；zip 多源）
	body        *Block
	isParallel  bool
	concurrency int
	onError     string
}

// NewForStmt constructs a ForStmt.
func NewForStmt(varName string, varType TypeRef, iterable Expr, body *Block) *ForStmt {
	if body == nil {
		body = NewBlock(nil)
	}
	return &ForStmt{base: newBase(), varNames: []string{varName}, varTypes: []TypeRef{varType}, sources: []Expr{iterable}, body: body}
}

// NewZipForStmt constructs a zip ForStmt ("for (a, b in zip(x, y)) { ... }").
// 变量数必须等于源数；每个变量名即该输入端口名。
func NewZipForStmt(varNames []string, varTypes []TypeRef, sources []Expr, body *Block) *ForStmt {
	if body == nil {
		body = NewBlock(nil)
	}
	return &ForStmt{base: newBase(), varNames: varNames, varTypes: varTypes, sources: sources, body: body}
}

// NewParallelForStmt constructs a statement-level parallel for
// ("parallel for (x in xs[, concurrency: N][, on_error: mode]) { ... }").
// concurrency <= 0 / onError == "" = the key was omitted ⇒ target default
// (single source of truth = `pkg/ir/parallel_for_defaults.go`).
func NewParallelForStmt(varName string, varType TypeRef, iterable Expr, body *Block, concurrency int, onError string) *ForStmt {
	if body == nil {
		body = NewBlock(nil)
	}
	return &ForStmt{base: newBase(), varNames: []string{varName}, varTypes: []TypeRef{varType}, sources: []Expr{iterable}, body: body,
		isParallel: true, concurrency: concurrency, onError: onError}
}

// NewZipParallelForStmt constructs a zip parallel for
// ("parallel for (a, b in zip(x, y)[, concurrency: N][, on_error: mode])").
func NewZipParallelForStmt(varNames []string, varTypes []TypeRef, sources []Expr, body *Block, concurrency int, onError string) *ForStmt {
	if body == nil {
		body = NewBlock(nil)
	}
	return &ForStmt{base: newBase(), varNames: varNames, varTypes: varTypes, sources: sources, body: body,
		isParallel: true, concurrency: concurrency, onError: onError}
}

// VarName returns the first loop variable name.
func (n *ForStmt) VarName() string {
	if len(n.varNames) == 0 {
		return ""
	}
	return n.varNames[0]
}

// VarType returns the first loop variable type (nil = inferred).
func (n *ForStmt) VarType() TypeRef {
	if len(n.varTypes) == 0 {
		return nil
	}
	return n.varTypes[0]
}

// Iterable returns the first loop source.
func (n *ForStmt) Iterable() Expr {
	if len(n.sources) == 0 {
		return nil
	}
	return n.sources[0]
}

// IsZip reports whether the loop is a zip loop (multi-source pairing).
func (n *ForStmt) IsZip() bool { return len(n.sources) > 1 }

// LoopVars returns all loop variable names (single or zip).
func (n *ForStmt) LoopVars() []string { return n.varNames }

// LoopTypes returns all loop variable type annotations (may contain nils).
func (n *ForStmt) LoopTypes() []TypeRef { return n.varTypes }

// LoopSources returns all loop sources (single or zip).
func (n *ForStmt) LoopSources() []Expr { return n.sources }

// Body returns the loop body block.
func (n *ForStmt) Body() *Block { return n.body }

// IsParallel reports whether the loop is a statement-level parallel for
// (renders as a commercial batch container).
func (n *ForStmt) IsParallel() bool { return n.isParallel }

// Concurrency returns the batch concurrency as written; <= 0 = omitted
// (parallel for only).
func (n *ForStmt) Concurrency() int { return n.concurrency }

// OnError returns the parallel-for error mode as written; "" = omitted.
func (n *ForStmt) OnError() string { return n.onError }

func (n *ForStmt) stmtNode()      {}
func (n *ForStmt) String() string { return reprNode(n) }

// WhileStmt is a while statement: while (<condition>) limit <count> { ... }.
type WhileStmt struct {
	base
	condition Expr
	limit     int
	body      *Block
}

// NewWhileStmt constructs a WhileStmt.
func NewWhileStmt(condition Expr, limit int, body *Block) *WhileStmt {
	if body == nil {
		body = NewBlock(nil)
	}
	return &WhileStmt{base: newBase(), condition: condition, limit: limit, body: body}
}

// Condition returns the loop condition.
func (n *WhileStmt) Condition() Expr { return n.condition }

// Limit returns the iteration limit (0 = none).
func (n *WhileStmt) Limit() int { return n.limit }

// Body returns the loop body block.
func (n *WhileStmt) Body() *Block { return n.body }

func (n *WhileStmt) stmtNode()      {}
func (n *WhileStmt) String() string { return reprNode(n) }

// ReturnStmt returns a value: return <expr>; or return;.
type ReturnStmt struct {
	base
	value     Expr
	streaming bool
}

// NewReturnStmt constructs a ReturnStmt. `streaming` is the delivery flag of the
// answer endpoint (`return template "…" [using {…}] streaming;` — 尾置上下文词,
// 用户 2026-09-27 裁决 甲); it is only meaningful when the value is a template
// (the parser accepts it on that production alone). Variadic so the ordinary
// (flagless) construction sites stay one-liner — but **every copy site must
// pass it through** (`Streaming()`), otherwise the flag is silently dropped;
// the pipeline判据 (output/return streaming round-trips through desugar /
// const-substitution / loop-body deepCopy) locks that.
func NewReturnStmt(value Expr, streaming ...bool) *ReturnStmt {
	return &ReturnStmt{base: newBase(), value: value, streaming: len(streaming) > 0 && streaming[0]}
}

// Value returns the returned expression (nil for bare return).
func (n *ReturnStmt) Value() Expr { return n.value }

// Streaming reports the answer endpoint's delivery flag (`streaming`).
func (n *ReturnStmt) Streaming() bool { return n.streaming }

func (n *ReturnStmt) stmtNode()      {}
func (n *ReturnStmt) String() string { return reprNode(n) }

// YieldStmt publishes one value from a value-producing container.
// Port 是命名输出口（coze loop/batch outputs 的端口名）；空串 = 默认口
// "output"（裸 yield v 与既有行为一致）。同一循环体内多个 yield 各占一个口；
// 同名口重复发布 → 编译错误（显式，不静默覆盖）。
type YieldStmt struct {
	base
	value Expr
	port  string
}

// NewYieldStmt constructs a YieldStmt publishing to the default port "output".
func NewYieldStmt(value Expr) *YieldStmt { return &YieldStmt{base: newBase(), value: value} }

// NewNamedYieldStmt constructs a YieldStmt publishing to a named port.
func NewNamedYieldStmt(port string, value Expr) *YieldStmt {
	return &YieldStmt{base: newBase(), value: value, port: port}
}

// Value returns the published expression.
func (n *YieldStmt) Value() Expr { return n.value }

// Port returns the named output port ("" = default "output").
func (n *YieldStmt) Port() string { return n.port }

func (n *YieldStmt) stmtNode()      {}
func (n *YieldStmt) String() string { return reprNode(n) }

// OutputStmt publishes one intermediate message to the response stream:
// output(<expr>); — non-terminating (model: docs/NCODA-OUTPUT-MODEL.md).
type OutputStmt struct {
	base
	value     Expr
	streaming bool
}

// NewOutputStmt constructs an OutputStmt. `streaming` is the intermediate
// message's delivery flag (`output(<expr>) streaming;` — 尾置上下文词, 用户
// 2026-09-27 裁决 甲). See NewReturnStmt for the copy-site discipline.
func NewOutputStmt(value Expr, streaming ...bool) *OutputStmt {
	return &OutputStmt{base: newBase(), value: value, streaming: len(streaming) > 0 && streaming[0]}
}

// Value returns the message expression.
func (n *OutputStmt) Value() Expr { return n.value }

// Streaming reports the intermediate message's delivery flag (`streaming`).
func (n *OutputStmt) Streaming() bool { return n.streaming }

func (n *OutputStmt) stmtNode()      {}
func (n *OutputStmt) String() string { return reprNode(n) }

// ParallelStmt is a parallel statement:
// parallel { <block>; <block>; ... } or parallel { <name>: <block>; ... }.
// Branch names are source-level labels only (transparent scope: every
// branch-local binding is published to the join, so downstream code
// reaches values by their variable names, never by branch name).
type ParallelStmt struct {
	base
	branches []*Block
	names    []string // optional per-branch labels; "" when unnamed
}

// NewParallelStmt constructs a ParallelStmt. Branch names are optional
// labels aligned by index with branches ("" for an unnamed branch).
func NewParallelStmt(branches []*Block, names ...string) *ParallelStmt {
	return &ParallelStmt{base: newBase(), branches: branches, names: names}
}

// Branches returns the parallel branch blocks.
func (n *ParallelStmt) Branches() []*Block { return n.branches }

// BranchNames returns the per-branch labels ("" for unnamed branches);
// the slice is aligned with Branches by index.
func (n *ParallelStmt) BranchNames() []string { return n.names }

func (n *ParallelStmt) stmtNode()      {}
func (n *ParallelStmt) String() string { return reprNode(n) }

// AttemptStmt is structured success/failure control flow for one workflow
// operation. Result/error bindings default to immutable VarDeclStmts and
// bodies to empty Blocks (ast.py AttemptStmt).
type AttemptStmt struct {
	base
	operation     Expr
	resultBinding *VarDeclStmt
	successBody   *Block
	errorBinding  *VarDeclStmt
	failureBody   *Block
}

// NewAttemptStmt constructs an AttemptStmt.
func NewAttemptStmt(operation Expr, resultBinding *VarDeclStmt, successBody *Block, errorBinding *VarDeclStmt, failureBody *Block) *AttemptStmt {
	if resultBinding == nil {
		resultBinding = NewVarDeclStmt("", nil, nil, false)
	}
	if errorBinding == nil {
		errorBinding = NewVarDeclStmt("", nil, nil, false)
	}
	if successBody == nil {
		successBody = NewBlock(nil)
	}
	if failureBody == nil {
		failureBody = NewBlock(nil)
	}
	return &AttemptStmt{base: newBase(), operation: operation, resultBinding: resultBinding, successBody: successBody, errorBinding: errorBinding, failureBody: failureBody}
}

// Operation returns the wrapped operation expression.
func (n *AttemptStmt) Operation() Expr { return n.operation }

// ResultBinding returns the success result binding.
func (n *AttemptStmt) ResultBinding() *VarDeclStmt { return n.resultBinding }

// SuccessBody returns the success branch body.
func (n *AttemptStmt) SuccessBody() *Block { return n.successBody }

// ErrorBinding returns the failure error binding.
func (n *AttemptStmt) ErrorBinding() *VarDeclStmt { return n.errorBinding }

// FailureBody returns the failure branch body.
func (n *AttemptStmt) FailureBody() *Block { return n.failureBody }

func (n *AttemptStmt) stmtNode()      {}
func (n *AttemptStmt) String() string { return reprNode(n) }

// HumanActionBranch is one action expression and the body resumed when that
// action is selected (ast.py HumanActionBranch).
type HumanActionBranch struct {
	base
	actionExpr Expr
	body       *Block
}

// NewHumanActionBranch constructs a HumanActionBranch.
func NewHumanActionBranch(actionExpr Expr, body *Block) *HumanActionBranch {
	if body == nil {
		body = NewBlock(nil)
	}
	return &HumanActionBranch{base: newBase(), actionExpr: actionExpr, body: body}
}

// ActionExpr returns the action expression.
func (n *HumanActionBranch) ActionExpr() Expr { return n.actionExpr }

// Body returns the resumed body block.
func (n *HumanActionBranch) Body() *Block { return n.body }

func (n *HumanActionBranch) String() string { return reprNode(n) }

// RequestInputStmt suspends for typed human input, then resumes one action
// or the timeout branch (ast.py RequestInputStmt).
//
// 两种形态（规范形，一语义一拼写）：
//   - `ask<Form, Action>({content, fields, actions[, timeout]}) as r { action … }`
//     —— 有 N≥1 个动作（actionType 非 nil，actionBranches 非空）。
//   - `ask<Form>({content, fields}) as r;` —— **无动作**（actionType == nil，
//     无臂、无块）；平台对应 `answer_type: text` + 单条无标签推进口。
type RequestInputStmt struct {
	base
	formType       TypeRef
	actionType     TypeRef
	config         *MapLit
	resultBinding  *VarDeclStmt
	actionBranches []*HumanActionBranch
	timeoutBody    *Block
}

// NewRequestInputStmt constructs a RequestInputStmt.
//
// actionType == nil **不是**「缺省替成 any」，而是**无动作形态**（S1，
// `ask<Form>({…}) as r;`，用户 2026-10-01 批准）：平台 `answer_type: text`
// 的问答节点 = 无选项、无分支口、单条无标签推进口。nil 保持 nil —— 构造器
// 若把它兜成 `any`，形态判据就得在第二处重新发明（同一规则不两处定义）。
func NewRequestInputStmt(formType, actionType TypeRef, config *MapLit, resultBinding *VarDeclStmt, actionBranches []*HumanActionBranch, timeoutBody *Block) *RequestInputStmt {
	if formType == nil {
		formType = NewPrimTypeRef("any")
	}
	if config == nil {
		config = NewMapLit(nil)
	}
	if resultBinding == nil {
		resultBinding = NewVarDeclStmt("", nil, nil, false)
	}
	if timeoutBody == nil {
		timeoutBody = NewBlock(nil)
	}
	return &RequestInputStmt{base: newBase(), formType: formType, actionType: actionType, config: config, resultBinding: resultBinding, actionBranches: actionBranches, timeoutBody: timeoutBody}
}

// FormType returns the requested form type.
func (n *RequestInputStmt) FormType() TypeRef { return n.formType }

// ActionType returns the requested action type.
//
// nil = **无动作形态**（S1）：`ask<Form>({…}) as r;` —— 没有臂、没有 action
// 类型、没有超时；绑定落语句所在作用域（与 switch 结果绑定同一条，R-71）。
func (n *RequestInputStmt) ActionType() TypeRef { return n.actionType }

// Config returns the request configuration map.
func (n *RequestInputStmt) Config() *MapLit { return n.config }

// ResultBinding returns the result binding.
func (n *RequestInputStmt) ResultBinding() *VarDeclStmt { return n.resultBinding }

// ActionBranches returns the action branches.
func (n *RequestInputStmt) ActionBranches() []*HumanActionBranch { return n.actionBranches }

// TimeoutBody returns the timeout branch body.
func (n *RequestInputStmt) TimeoutBody() *Block { return n.timeoutBody }

func (n *RequestInputStmt) stmtNode()      {}
func (n *RequestInputStmt) String() string { return reprNode(n) }

// BreakStmt is a bare break statement.
type BreakStmt struct{ base }

// NewBreakStmt constructs a BreakStmt.
func NewBreakStmt() *BreakStmt { return &BreakStmt{base: newBase()} }

func (n *BreakStmt) stmtNode()      {}
func (n *BreakStmt) String() string { return reprNode(n) }

// ContinueStmt is a bare continue statement.
type ContinueStmt struct{ base }

// NewContinueStmt constructs a ContinueStmt.
func NewContinueStmt() *ContinueStmt { return &ContinueStmt{base: newBase()} }

func (n *ContinueStmt) stmtNode()      {}
func (n *ContinueStmt) String() string { return reprNode(n) }

// SwitchBranch is one intent case: a string label and its branch body.
// The label is extracted from the case tag (deduplicated, order preserved)
// and becomes the platform intent name (branch_<i> edge).
type SwitchBranch struct {
	base
	label string
	body  *Block
}

// NewSwitchBranch constructs an SwitchBranch.
func NewSwitchBranch(label string, body *Block) *SwitchBranch {
	if body == nil {
		body = NewBlock(nil)
	}
	return &SwitchBranch{base: newBase(), label: label, body: body}
}

// Label returns the case label (intent name).
func (n *SwitchBranch) Label() string { return n.label }

// Body returns the branch body block.
func (n *SwitchBranch) Body() *Block { return n.body }

func (n *SwitchBranch) String() string { return reprNode(n) }

// SwitchStmt is the intent-detection multi-exit branch statement:
//
//	intent ( <query> [, <config-map> ]) [-> <result>] {
//	    "<label>": { ... }          // branch_0..branch_{N-1} (case order)
//	    ...
//	    default: { ... }            // default edge (optional)
//	}
//
// The intent list is extracted from the case labels (order = case order);
// empty branch bodies are rejected (悬挂 → runtime uncertainty, user rule).
// The result binding exposes classificationId (int) / reason (string).
type SwitchStmt struct {
	base
	query    Expr
	config   *MapLit
	branches []*SwitchBranch
	defBody  *Block
	result   *VarDeclStmt
}

// NewSwitchStmt constructs an SwitchStmt.
func NewSwitchStmt(query Expr, config *MapLit, branches []*SwitchBranch, defBody *Block, result *VarDeclStmt) *SwitchStmt {
	if config == nil {
		config = NewMapLit(nil)
	}
	if result == nil {
		result = NewVarDeclStmt("", nil, nil, false)
	}
	return &SwitchStmt{base: newBase(), query: query, config: config, branches: branches, defBody: defBody, result: result}
}

// Query returns the intent query expression.
func (n *SwitchStmt) Query() Expr { return n.query }

// Config returns the LLM configuration map (may be empty).
func (n *SwitchStmt) Config() *MapLit { return n.config }

// Branches returns the case branches in declaration order.
func (n *SwitchStmt) Branches() []*SwitchBranch { return n.branches }

// DefaultBody returns the default branch body (nil if absent).
func (n *SwitchStmt) DefaultBody() *Block { return n.defBody }

// HasDefault reports whether a default branch was declared.
func (n *SwitchStmt) HasDefault() bool { return n.defBody != nil }

// Result returns the result binding (`-> name`), empty name if absent.
func (n *SwitchStmt) Result() *VarDeclStmt { return n.result }

func (n *SwitchStmt) stmtNode()      {}
func (n *SwitchStmt) String() string { return reprNode(n) }
