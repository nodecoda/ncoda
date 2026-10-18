package ast

// This file ports the expression nodes and their helper ProcNode classes
// from src/nclang/lang/ast.py (expressions section). Field-for-field parity:
// every Python dataclass field becomes a Go field (value or child node) with
// the same name in camelCase, plus the embedded identity base.

// ─── literals ───────────────────────────────────────────────────────────

// IntLit is an integer literal.
type IntLit struct {
	base
	value int64
}

// NewIntLit constructs an IntLit.
func NewIntLit(value int64) *IntLit { return &IntLit{base: newBase(), value: value} }

// Value returns the literal value.
func (n *IntLit) Value() int64 { return n.value }

func (n *IntLit) exprNode()      {}
func (n *IntLit) String() string { return reprNode(n) }

// FloatLit is a floating-point literal.
type FloatLit struct {
	base
	value float64
}

// NewFloatLit constructs a FloatLit.
func NewFloatLit(value float64) *FloatLit { return &FloatLit{base: newBase(), value: value} }

// Value returns the literal value.
func (n *FloatLit) Value() float64 { return n.value }

func (n *FloatLit) exprNode()      {}
func (n *FloatLit) String() string { return reprNode(n) }

// StringLit is a string literal.
type StringLit struct {
	base
	value string
}

// NewStringLit constructs a StringLit.
func NewStringLit(value string) *StringLit { return &StringLit{base: newBase(), value: value} }

// Value returns the literal value.
func (n *StringLit) Value() string { return n.value }

func (n *StringLit) exprNode()      {}
func (n *StringLit) String() string { return reprNode(n) }

// BoolLit is a boolean literal.
type BoolLit struct {
	base
	value bool
}

// NewBoolLit constructs a BoolLit.
func NewBoolLit(value bool) *BoolLit { return &BoolLit{base: newBase(), value: value} }

// Value returns the literal value.
func (n *BoolLit) Value() bool { return n.value }

func (n *BoolLit) exprNode()      {}
func (n *BoolLit) String() string { return reprNode(n) }

// NullLit is the null literal. It carries no syntax fields.
type NullLit struct{ base }

// NewNullLit constructs a NullLit.
func NewNullLit() *NullLit { return &NullLit{base: newBase()} }

func (n *NullLit) exprNode()      {}
func (n *NullLit) String() string { return reprNode(n) }

// TemplatePart is one element of a TemplateString: either a literal string
// (Text) or an interpolated expression (Expr). Mirrors the alternating
// str|Expr entries of ast.py TemplateString.parts.
type TemplatePart struct {
	Text string
	Expr Expr
}

// TemplateString is a backtick template string with ${expr} interpolation.
// Parts alternate literal strings and expressions, always starting and
// ending with a string (see ast.py TemplateString).
type TemplateString struct {
	base
	parts []TemplatePart
}

// NewTemplateString constructs a TemplateString.
func NewTemplateString(parts []TemplatePart) *TemplateString {
	return &TemplateString{base: newBase(), parts: parts}
}

// Parts returns the alternating literal/expression parts.
func (n *TemplateString) Parts() []TemplatePart { return n.parts }

func (n *TemplateString) exprNode()      {}
func (n *TemplateString) String() string { return reprNode(n) }

// ArrayLit is an array literal: [a, b, c].
type ArrayLit struct {
	base
	elements []Expr
}

// NewArrayLit constructs an ArrayLit.
func NewArrayLit(elements []Expr) *ArrayLit { return &ArrayLit{base: newBase(), elements: elements} }

// Elements returns the element expressions.
func (n *ArrayLit) Elements() []Expr { return n.elements }

func (n *ArrayLit) exprNode()      {}
func (n *ArrayLit) String() string { return reprNode(n) }

// MapEntry is one key/value pair of a MapLit. Mirrors the (key, value)
// tuples of ast.py MapLit.entries.
type MapEntry struct {
	Key   Expr
	Value Expr
}

// StaticMapKey returns the static key name of a map entry and whether the key
// is static at all. The language rule (single source of truth; previously
// duplicated as passes.staticMapKey / TypePass.staticMapKey, and violated by
// StringLit-only readers in the IR builder — D-2, 2026-09-14):
//
//	key = IDENTIFIER  → the name itself (a *label*, not a reference:
//	                    binder.visitMapLit never resolves it as a variable)
//	key = STRING      → the literal value
//	anything else     → dynamic key (second result false; callers that need a
//	                    static platform name must fail loud, not skip)
//
// `map_key = expression | "timeout"` (spec/grammar.ebnf) makes any expression
// grammatical; the contextual `timeout` word is parsed as IdentExpr, so it
// lands on the identifier arm. An empty string key is a *static* key named ""
// — never use `name != ""` as the dynamic-key test (that equivalence is how
// the pre-fix readers silently dropped keys).
func StaticMapKey(e Expr) (string, bool) {
	switch k := e.(type) {
	case *IdentExpr:
		return k.Name(), true
	case *StringLit:
		return k.Value(), true
	}
	return "", false
}

// MapLit is a map literal: { "k": v, ... }.
type MapLit struct {
	base
	entries []MapEntry
}

// NewMapLit constructs a MapLit.
func NewMapLit(entries []MapEntry) *MapLit { return &MapLit{base: newBase(), entries: entries} }

// Entries returns the key/value pairs.
func (n *MapLit) Entries() []MapEntry { return n.entries }

func (n *MapLit) exprNode()      {}
func (n *MapLit) String() string { return reprNode(n) }

// ─── identifier / access / call ─────────────────────────────────────────

// IdentExpr is a simple identifier reference: x, query.
type IdentExpr struct {
	base
	name string
	// escaped marks a $-escaped keyword identifier (NCODA-DOLLAR-ESCAPE-SPEC):
	// the lexer strips the '$' and emits a plain IDENTIFIER, so an IDENTIFIER
	// whose lexeme is a keyword spelling can only come from '$'-escape. The
	// binder uses the flag to reject escaped keywords that would silently
	// resolve to a keyword-builtin (e.g. $file(...) must not alias the file()
	// cast). IR/platform keep the raw contract name; the flag is a binding-time
	// guard only.
	escaped bool
}

// NewIdentExpr constructs an IdentExpr.
func NewIdentExpr(name string) *IdentExpr { return &IdentExpr{base: newBase(), name: name} }

// Name returns the identifier text.
func (n *IdentExpr) Name() string { return n.name }

// Escaped reports whether the identifier came from a '$'-escaped keyword
// spelling ($type → IDENTIFIER("type"), Escaped=true).
func (n *IdentExpr) Escaped() bool { return n.escaped }

// SetEscaped marks the identifier as $-escaped.
func (n *IdentExpr) SetEscaped(v bool) { n.escaped = v }

func (n *IdentExpr) exprNode()      {}
func (n *IdentExpr) String() string { return reprNode(n) }

// FieldAccessExpr is a field access: obj.field.
type FieldAccessExpr struct {
	base
	obj       Expr
	fieldName string
}

// NewFieldAccessExpr constructs a FieldAccessExpr.
func NewFieldAccessExpr(obj Expr, fieldName string) *FieldAccessExpr {
	return &FieldAccessExpr{base: newBase(), obj: obj, fieldName: fieldName}
}

// Obj returns the accessed object expression.
func (n *FieldAccessExpr) Obj() Expr { return n.obj }

// FieldName returns the accessed field name.
func (n *FieldAccessExpr) FieldName() string { return n.fieldName }

func (n *FieldAccessExpr) exprNode()      {}
func (n *FieldAccessExpr) String() string { return reprNode(n) }

// IndexExpr is an index access: arr[0], map["key"].
type IndexExpr struct {
	base
	obj   Expr
	index Expr
}

// NewIndexExpr constructs an IndexExpr.
func NewIndexExpr(obj, index Expr) *IndexExpr {
	return &IndexExpr{base: newBase(), obj: obj, index: index}
}

// Obj returns the indexed object expression.
func (n *IndexExpr) Obj() Expr { return n.obj }

// Index returns the index expression.
func (n *IndexExpr) Index() Expr { return n.index }

func (n *IndexExpr) exprNode()      {}
func (n *IndexExpr) String() string { return reprNode(n) }

// CallExpr is a function call: func(arg1, arg2), optionally with type
// arguments: extract<Travel>(...).
type CallExpr struct {
	base
	func_    Expr
	typeArgs []TypeRef
	args     []Expr
}

// NewCallExpr constructs a CallExpr.
func NewCallExpr(func_ Expr, typeArgs []TypeRef, args []Expr) *CallExpr {
	return &CallExpr{base: newBase(), func_: func_, typeArgs: typeArgs, args: args}
}

// Func returns the callee expression.
func (n *CallExpr) Func() Expr { return n.func_ }

// TypeArgs returns the explicit type arguments (empty if none).
func (n *CallExpr) TypeArgs() []TypeRef { return n.typeArgs }

// Args returns the call arguments.
func (n *CallExpr) Args() []Expr { return n.args }

func (n *CallExpr) exprNode()      {}
func (n *CallExpr) String() string { return reprNode(n) }

// ─── operation policies ─────────────────────────────────────────────────

// OperationPolicy is implemented by one typed workflow-operation policy:
// RetryPolicy, TimeoutPolicy, or DefaultPolicy. Mirrors ast.py
// OperationPolicy.
type OperationPolicy interface {
	Node
	operationPolicy()
}

// RetryPolicy is a retry policy: max_retries, interval_ms.
type RetryPolicy struct {
	base
	maxRetries int
	intervalMs int
	// intervalExplicit records whether the source **wrote** the `interval:`
	// option (provenance, not value). `interval` omitted still resolves to
	// the language default 1000ms (see the parser), so the value alone cannot
	// tell an author-written axis from a filled-in default — and only the
	// author-written one may be reported as dropped on a target that has no
	// retry-interval carrier (residuals R-90, 正本 `spec/F3-error-policy-value-axes.md` §③).
	// Recording it changes neither the syntax nor the semantics of ncoda.
	intervalExplicit bool
}

// NewRetryPolicy constructs a RetryPolicy (interval omitted ⇒ default 1000ms,
// intervalExplicit = false).
func NewRetryPolicy(maxRetries, intervalMs int) *RetryPolicy {
	return &RetryPolicy{base: newBase(), maxRetries: maxRetries, intervalMs: intervalMs}
}

// MaxRetries returns the maximum retry count.
func (n *RetryPolicy) MaxRetries() int { return n.maxRetries }

// IntervalMs returns the retry interval in milliseconds.
func (n *RetryPolicy) IntervalMs() int { return n.intervalMs }

// IntervalExplicit reports whether the source wrote the `interval:` option
// (as opposed to the policy carrying the language default).
func (n *RetryPolicy) IntervalExplicit() bool { return n.intervalExplicit }

// SetIntervalExplicit marks the policy as carrying an author-written
// `interval:` option.
func (n *RetryPolicy) SetIntervalExplicit(v bool) { n.intervalExplicit = v }

func (n *RetryPolicy) operationPolicy() {}
func (n *RetryPolicy) String() string   { return reprNode(n) }

// TimeoutPolicy is a timeout policy: timeout_ms.
type TimeoutPolicy struct {
	base
	timeoutMs int
}

// NewTimeoutPolicy constructs a TimeoutPolicy.
func NewTimeoutPolicy(timeoutMs int) *TimeoutPolicy {
	return &TimeoutPolicy{base: newBase(), timeoutMs: timeoutMs}
}

// TimeoutMs returns the timeout in milliseconds.
func (n *TimeoutPolicy) TimeoutMs() int { return n.timeoutMs }

func (n *TimeoutPolicy) operationPolicy() {}
func (n *TimeoutPolicy) String() string   { return reprNode(n) }

// DefaultPolicy is a fallback-value policy: value.
type DefaultPolicy struct {
	base
	value Expr
}

// NewDefaultPolicy constructs a DefaultPolicy.
func NewDefaultPolicy(value Expr) *DefaultPolicy {
	return &DefaultPolicy{base: newBase(), value: value}
}

// Value returns the fallback value expression.
func (n *DefaultPolicy) Value() Expr { return n.value }

func (n *DefaultPolicy) operationPolicy() {}
func (n *DefaultPolicy) String() string   { return reprNode(n) }

// OperationPolicyExpr attaches typed policies to one workflow operation:
// op with retry/timeout/default.
type OperationPolicyExpr struct {
	base
	operation Expr
	policies  []OperationPolicy
}

// NewOperationPolicyExpr constructs an OperationPolicyExpr.
func NewOperationPolicyExpr(operation Expr, policies []OperationPolicy) *OperationPolicyExpr {
	return &OperationPolicyExpr{base: newBase(), operation: operation, policies: policies}
}

// Operation returns the wrapped operation expression.
func (n *OperationPolicyExpr) Operation() Expr { return n.operation }

// Policies returns the typed policies.
func (n *OperationPolicyExpr) Policies() []OperationPolicy { return n.policies }

func (n *OperationPolicyExpr) exprNode()      {}
func (n *OperationPolicyExpr) String() string { return reprNode(n) }

// ─── unary / binary / ternary / lambda ──────────────────────────────────

// UnaryExpr is a unary operation: !expr, -expr.
type UnaryExpr struct {
	base
	op      string
	operand Expr
}

// NewUnaryExpr constructs a UnaryExpr.
func NewUnaryExpr(op string, operand Expr) *UnaryExpr {
	return &UnaryExpr{base: newBase(), op: op, operand: operand}
}

// Op returns the unary operator text.
func (n *UnaryExpr) Op() string { return n.op }

// Operand returns the operand expression.
func (n *UnaryExpr) Operand() Expr { return n.operand }

func (n *UnaryExpr) exprNode()      {}
func (n *UnaryExpr) String() string { return reprNode(n) }

// BinaryExpr is a binary operation: left op right.
type BinaryExpr struct {
	base
	op    string
	left  Expr
	right Expr
}

// NewBinaryExpr constructs a BinaryExpr.
func NewBinaryExpr(op string, left, right Expr) *BinaryExpr {
	return &BinaryExpr{base: newBase(), op: op, left: left, right: right}
}

// Op returns the binary operator text.
func (n *BinaryExpr) Op() string { return n.op }

// Left returns the left operand.
func (n *BinaryExpr) Left() Expr { return n.left }

// Right returns the right operand.
func (n *BinaryExpr) Right() Expr { return n.right }

func (n *BinaryExpr) exprNode()      {}
func (n *BinaryExpr) String() string { return reprNode(n) }

// TernaryExpr is a conditional expression: cond ? then_val : else_val.
type TernaryExpr struct {
	base
	condition Expr
	thenVal   Expr
	elseVal   Expr
}

// NewTernaryExpr constructs a TernaryExpr.
func NewTernaryExpr(condition, thenVal, elseVal Expr) *TernaryExpr {
	return &TernaryExpr{base: newBase(), condition: condition, thenVal: thenVal, elseVal: elseVal}
}

// Condition returns the condition expression.
func (n *TernaryExpr) Condition() Expr { return n.condition }

// ThenVal returns the true-branch value.
func (n *TernaryExpr) ThenVal() Expr { return n.thenVal }

// ElseVal returns the false-branch value.
func (n *TernaryExpr) ElseVal() Expr { return n.elseVal }

func (n *TernaryExpr) exprNode()      {}
func (n *TernaryExpr) String() string { return reprNode(n) }

// LambdaExpr is an anonymous function: (params) -> expr.
type LambdaExpr struct {
	base
	params []string
	body   Expr
}

// NewLambdaExpr constructs a LambdaExpr.
func NewLambdaExpr(params []string, body Expr) *LambdaExpr {
	return &LambdaExpr{base: newBase(), params: params, body: body}
}

// Params returns the parameter names.
func (n *LambdaExpr) Params() []string { return n.params }

// Body returns the lambda body expression.
func (n *LambdaExpr) Body() Expr { return n.body }

func (n *LambdaExpr) exprNode()      {}
func (n *LambdaExpr) String() string { return reprNode(n) }

// ─── for / parallel / foreign code ──────────────────────────────────────

// ForExpr is a value-producing for expression:
//
//	var results = for (item in items) { item.text; }
type ForExpr struct {
	base
	varNames []string  // 循环变量（单变量 len=1；zip 多变量）
	varTypes []TypeRef // 对应变量类型注解（可 nil）
	sources  []Expr    // 循环源（单源 len=1；zip 多源）
	body     *Block
}

// NewForExpr constructs a ForExpr.
func NewForExpr(varName string, varType TypeRef, iterable Expr, body *Block) *ForExpr {
	if body == nil {
		body = NewBlock(nil)
	}
	return &ForExpr{base: newBase(), varNames: []string{varName}, varTypes: []TypeRef{varType}, sources: []Expr{iterable}, body: body}
}

// NewZipForExpr constructs a zip ForExpr ("for (a, b in zip(x, y)) { ... }").
// 变量数必须等于源数；每个变量名即该输入端口名。
func NewZipForExpr(varNames []string, varTypes []TypeRef, sources []Expr, body *Block) *ForExpr {
	if body == nil {
		body = NewBlock(nil)
	}
	return &ForExpr{base: newBase(), varNames: varNames, varTypes: varTypes, sources: sources, body: body}
}

// VarName returns the first loop variable name.
func (n *ForExpr) VarName() string {
	if len(n.varNames) == 0 {
		return ""
	}
	return n.varNames[0]
}

// VarType returns the first loop variable type (nil = inferred).
func (n *ForExpr) VarType() TypeRef {
	if len(n.varTypes) == 0 {
		return nil
	}
	return n.varTypes[0]
}

// Iterable returns the first loop source.
func (n *ForExpr) Iterable() Expr {
	if len(n.sources) == 0 {
		return nil
	}
	return n.sources[0]
}

// IsZip reports whether the loop is a zip loop (multi-source pairing).
func (n *ForExpr) IsZip() bool { return len(n.sources) > 1 }

// LoopVars returns all loop variable names (single or zip).
func (n *ForExpr) LoopVars() []string { return n.varNames }

// LoopTypes returns all loop variable type annotations (may contain nils).
func (n *ForExpr) LoopTypes() []TypeRef { return n.varTypes }

// LoopSources returns all loop sources (single or zip).
func (n *ForExpr) LoopSources() []Expr { return n.sources }

// Body returns the loop body block.
func (n *ForExpr) Body() *Block { return n.body }

func (n *ForExpr) exprNode()      {}
func (n *ForExpr) String() string { return reprNode(n) }

// ParallelForExpr is a ForExpr with bounded concurrency and an error mode.
// Mirrors ast.py `class ParallelForExpr(ForExpr)` by embedding ForExpr.
type ParallelForExpr struct {
	ForExpr
	concurrency int
	onError     string
}

// NewParallelForExpr constructs a ParallelForExpr. concurrency <= 0 / onError
// == "" mean the author omitted the key: the target default applies (single
// source of truth = `pkg/ir/parallel_for_defaults.go`). The AST keeps
// "omitted" distinct from "written" only by these sentinels — there is no
// "written the default" state, which is what makes the canonical form
// (defaults are not written) checkable.
func NewParallelForExpr(varName string, varType TypeRef, iterable Expr, body *Block, concurrency int, onError string) *ParallelForExpr {
	if body == nil {
		body = NewBlock(nil)
	}
	return &ParallelForExpr{
		ForExpr:     ForExpr{base: newBase(), varNames: []string{varName}, varTypes: []TypeRef{varType}, sources: []Expr{iterable}, body: body},
		concurrency: concurrency,
		onError:     onError,
	}
}

// NewZipParallelForExpr constructs a zip ParallelForExpr
// ("parallel for (a, b in zip(x, y)[, concurrency: N][, on_error: mode])").
// Same omission sentinels as NewParallelForExpr.
func NewZipParallelForExpr(varNames []string, varTypes []TypeRef, sources []Expr, body *Block, concurrency int, onError string) *ParallelForExpr {
	if body == nil {
		body = NewBlock(nil)
	}
	return &ParallelForExpr{
		ForExpr:     ForExpr{base: newBase(), varNames: varNames, varTypes: varTypes, sources: sources, body: body},
		concurrency: concurrency,
		onError:     onError,
	}
}

// Concurrency returns the maximum parallel iterations.
func (n *ParallelForExpr) Concurrency() int { return n.concurrency }

// OnError returns the error mode as written; "" = omitted (target default).
func (n *ParallelForExpr) OnError() string { return n.onError }

func (n *ParallelForExpr) exprNode()      {}
func (n *ParallelForExpr) String() string { return reprNode(n) }

// CodeInputBinding is one typed local name bound to a normal DSL expression
// (ast.py CodeInputBinding).
type CodeInputBinding struct {
	base
	name    string
	typeRef TypeRef
	value   Expr
}

// NewCodeInputBinding constructs a CodeInputBinding.
func NewCodeInputBinding(name string, typeRef TypeRef, value Expr) *CodeInputBinding {
	if typeRef == nil {
		typeRef = NewPrimTypeRef("any")
	}
	return &CodeInputBinding{base: newBase(), name: name, typeRef: typeRef, value: value}
}

// Name returns the binding name.
func (n *CodeInputBinding) Name() string { return n.name }

// TypeRef returns the binding type.
func (n *CodeInputBinding) TypeRef() TypeRef { return n.typeRef }

// Value returns the bound expression.
func (n *CodeInputBinding) Value() Expr { return n.value }

func (n *CodeInputBinding) String() string { return reprNode(n) }

// CodeOutputContract is implemented by the two distinct Code result models:
// CodeDirectOutput and CodeStructuralOutput (ast.py CodeOutputContract).
type CodeOutputContract interface {
	Node
	codeOutputContract()
}

// CodeDirectOutput is a single direct value exposed through the implicit
// result port (ast.py CodeDirectOutput).
type CodeDirectOutput struct {
	base
	typeRef TypeRef
}

// NewCodeDirectOutput constructs a CodeDirectOutput.
func NewCodeDirectOutput(typeRef TypeRef) *CodeDirectOutput {
	if typeRef == nil {
		typeRef = NewPrimTypeRef("any")
	}
	return &CodeDirectOutput{base: newBase(), typeRef: typeRef}
}

// TypeRef returns the direct output type.
func (n *CodeDirectOutput) TypeRef() TypeRef { return n.typeRef }

func (n *CodeDirectOutput) codeOutputContract() {}
func (n *CodeDirectOutput) String() string      { return reprNode(n) }

// CodeOutputField is one physical output port in a structural Code result
// (ast.py CodeOutputField).
type CodeOutputField struct {
	base
	name    string
	typeRef TypeRef
}

// NewCodeOutputField constructs a CodeOutputField.
func NewCodeOutputField(name string, typeRef TypeRef) *CodeOutputField {
	if typeRef == nil {
		typeRef = NewPrimTypeRef("any")
	}
	return &CodeOutputField{base: newBase(), name: name, typeRef: typeRef}
}

// Name returns the output port name.
func (n *CodeOutputField) Name() string { return n.name }

// TypeRef returns the output port type.
func (n *CodeOutputField) TypeRef() TypeRef { return n.typeRef }

func (n *CodeOutputField) String() string { return reprNode(n) }

// CodeStructuralOutput is an anonymous structural result backed by multiple
// physical ports (ast.py CodeStructuralOutput).
type CodeStructuralOutput struct {
	base
	fields []*CodeOutputField
}

// NewCodeStructuralOutput constructs a CodeStructuralOutput.
func NewCodeStructuralOutput(fields []*CodeOutputField) *CodeStructuralOutput {
	return &CodeStructuralOutput{base: newBase(), fields: fields}
}

// Fields returns the physical output ports.
func (n *CodeStructuralOutput) Fields() []*CodeOutputField { return n.fields }

func (n *CodeStructuralOutput) codeOutputContract() {}
func (n *CodeStructuralOutput) String() string      { return reprNode(n) }

// ForeignCodeExpr is a strongly typed foreign-code expression
// (ast.py ForeignCodeExpr): language, inputs, output contract, source.
type ForeignCodeExpr struct {
	base
	language string
	inputs   []*CodeInputBinding
	output   CodeOutputContract
	source   *TemplateString
}

// NewForeignCodeExpr constructs a ForeignCodeExpr.
func NewForeignCodeExpr(language string, inputs []*CodeInputBinding, output CodeOutputContract, source *TemplateString) *ForeignCodeExpr {
	if output == nil {
		output = NewCodeDirectOutput(nil)
	}
	if source == nil {
		source = NewTemplateString(nil)
	}
	return &ForeignCodeExpr{base: newBase(), language: language, inputs: inputs, output: output, source: source}
}

// Language returns the foreign code language ("python3" by default).
func (n *ForeignCodeExpr) Language() string { return n.language }

// Inputs returns the typed input bindings.
func (n *ForeignCodeExpr) Inputs() []*CodeInputBinding { return n.inputs }

// Output returns the output contract (direct or structural).
func (n *ForeignCodeExpr) Output() CodeOutputContract { return n.output }

// Source returns the source template string.
func (n *ForeignCodeExpr) Source() *TemplateString { return n.source }

func (n *ForeignCodeExpr) exprNode()      {}
func (n *ForeignCodeExpr) String() string { return reprNode(n) }

// ─── flow answer template (W2) ──────────────────────────────────────────

// TemplateBinding binds one named slot of a verbatim platform template
// (`return template "…{{k}}…" using { k: <value-path> }`).
//
// 名字不是模板里的占位符配对关系的判据 —— 编译器**不读**模板文本
// （W2 零解析原则，用户 2026-09-17 两次拍板）：名字只是交给平台的**名额名**
// （wire `node_inputs[].name`），模板如何引用它由平台运行期决定。
type TemplateBinding struct {
	Name  string
	Value Expr
}

// TemplateValue 是**模板串**（模板串规范型的语言载体）：
//
//	template "<逐字文本>" [ using { name: value-path, ... } ]
//
// 它是一般**表达式**（不是「返回」专属）——三个位点共用同一个载体：
//
//	| 位点 | 拼写 | 落点（coze-biz） |
//	| 流程答复端点 | `return template …;` | `end{terminatePlan: useAnswerContent}` |
//	| 中间消息 | `output template …;` | `output` 节点（type 13） |
//	| llm message content | `content: template …`（值位） | 节点 `prompt` / `systemPrompt` 槽 |
//
// 语义（W2 §1.2 / §8）：
//   - **文本槽逐字承载、零解析**：编译器不看占位符、不校验「文本 ↔ 名额名」的配对、
//     也不因为模板里出现名额表之外的名字而报错或提示（那是 runtime/作者的事，
//     用户 2026-09-17 两次拍板）。写错名字 ⇒ 编译期静默、运行期坏死（明确接受的代价）。
//   - **名额值是值路径**（`IDENT(.IDENT)*`），只有名字这半是作者写死的源信息；
//     文本那半是「平台数据的搬运」，不是 ncoda 的语义对象（W2 §8.3.2）。
//   - 位置语义由**宿主**决定：`return` 位 = 流程答复通道（与值通道二选一，同一流程
//     的所有出口必须同身份）；`output` 位 = 非终止的中间消息；content 位 = 一个字符串。
//     这三个位点不是「同一语义的三种拼写」——它们只是共享同一个**模板串**载体。
//
// 与内插字符串（“ `…${expr}…` “）的关系 = **模板串规范型的两个拼写**（名字归谁）：
// 内插 = 名字由编译器从值落地的载体派生（匿名名额）；`template … using` = 名字由作者
// 写死（具名名额）。**同一 wire 只有一种规范拼写**，判定式 = W2 §8.3 三句式。
// 正本 = docs/five-step/spec/TEMPLATE-STRING-CANONICAL-FORM.md。
type TemplateValue struct {
	base
	text     string
	bindings []*TemplateBinding
}

// NewTemplateValue constructs a TemplateValue.
func NewTemplateValue(text string, bindings []*TemplateBinding) *TemplateValue {
	return &TemplateValue{base: newBase(), text: text, bindings: bindings}
}

// Text returns the verbatim template text.
func (n *TemplateValue) Text() string { return n.text }

// Bindings returns the named slots in wire order (the platform's node_inputs
// order is what the roundtrip must preserve).
func (n *TemplateValue) Bindings() []*TemplateBinding { return n.bindings }

func (n *TemplateValue) exprNode()      {}
func (n *TemplateValue) String() string { return reprNode(n) }
