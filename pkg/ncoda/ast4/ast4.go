// Package decompile —— 结构树 → 语句列表（对应 ana2 decompiler/ast4_builder.py +
// statement_generation.py 的最小骨架：AST4 模型 + 结构分派 + 节点通用转换）。
package ast4

// ast4.go —— AST4 语句/表达式模型（对应 ana2 ast4/stmt.py + ast4/expr.py 子集）。

// Stmt 语句节点。
type Stmt interface{ stmt() }

// Block 语句块。
type Block struct{ Stmts []Stmt }

func (b *Block) stmt() {}

// VarDecl 变量声明（init 非 nil 时为初始化）。Mutable=true 且 Init 非 nil
// 时渲染 `var <name> = <init>;`（可改绑定,dify loop 状态）;否则 `let`。
type VarDecl struct {
	Name            string
	Type            string // 类型字符串（如 "string"/"list"）
	Init            Expr
	Mutable         bool
	MigrationOrigin string // migration-only provenance; rendered as an inert comment
	// WireGroupName: 该声明是**汇聚单元的提升变量**时，其 `mergeGroups[].name`
	// （wire 事实，A13-H2）。canonicalizer 据此决定是否采纳为变量名（见
	// convergence_unit_name.go）。与 For.WireIterVar 同性质：生成侧把 wire 名
	// 随节点带走，采纳判据在 canonicalizer 的 collect 遍定案。
	WireGroupName string
	// WireGroupTable: 同一张 merge 表的**全部**组名（`mergeGroups[].name`，wire 序），
	// 仅在该表整体可采纳时非空（见 convergenceUnitMergeTableAdoptable）。采纳是
	// **整表**动作：任何一组转义都会让组发射序（变量名的函数）与 wire 序不一致 ⇒
	// 逐轮重排，故 canonicalizer 的捕获检查（判据 ④）也必须按整表判（一个名字被
	// 同作用域捕获 ⇒ 该表全组转义）。
	WireGroupTable []string
	// SharedStorage: 该声明在 wire 上是**共享载体**（节点输出被 ≥2 个输入槽位
	// 引用 ⇒ 它是存储，不是可折叠的值）。渲染层必须保留类型（`<T> v = …;`），
	// 不得压成值绑定 `let v = …` —— 纯常量的 `let` 在再编译时被折叠进消费者、
	// 载体节点消失、兜底臂内重建一份常量 ⇒ 产物漂移（C §6.1 A9 / J §8 U11
	// 一手实测：c0 与 c1 拓扑/节点名不同，`I1a TEXT_DRIFT` + `I1b ARTIFACT_DRIFT`）。
	SharedStorage bool
}

func (v *VarDecl) stmt() {}

// ExprStmt 表达式语句（无输出的调用）。
type ExprStmt struct{ Expr Expr }

func (e *ExprStmt) stmt() {}

// If 条件结构。
type If struct {
	Cond Expr
	Then *Block
	Else *Block // nil 表示无 else
	// WireNode = 本 If 对应的平台 **condition 节点 id**（内部 AST 补全，2026-09-26；
	// R-110）。汇聚折叠要把「被臂内嵌套条件门控的候选写」放进那道门的块里，
	// 需要能按 wire 节点认回渲染出的 If（条件表达式字符串不是身份）。
	// 只读字段：不参与渲染，也不进产物。
	WireNode string
}

func (i *If) stmt() {}

// While 循环（while ( <cond> ) limit <int> { ... }）——dify loop 的
// count + break 形态：cond 为继续条件（break 条件的否定），limit 为最大
// 迭代数（dify loop_count）。可变循环状态（var <label> = <init>;）由
// 前端 LoopStatePass 校验必须由所属 while 写（INVALID_STATE 门禁）。
type While struct {
	Cond  Expr
	Limit int
	Body  *Block
}

func (w *While) stmt() {}

// ParallelStmtStmt 并行块（parallel { ... }）：多条分支并行执行，全部
// 完成后继续。反编译产物：coze 条件节点同端口多后继（并行 fan-out）。
type ParallelStmtStmt struct {
	Blocks []*Block
}

func (p *ParallelStmtStmt) stmt() {}

// For 循环结构。
type For struct {
	Var      string // 迭代变量（zip 多源时为首变量）
	Iterable Expr   // 迭代源（nil 表示 count/infinite 循环占位）
	// Vars/Sources zip 多源逐项配对（coze batch 多 InputSources：
	// for (a, b in zip(x, y))）。len(Sources) > 1 时生效；变量名即端口名。
	Vars    []string
	Sources []Expr
	Body    *Block
	// Parallel 并行循环（coze Batch）：正编译渲染 batch 容器而非串行 loop。
	// Concurrency 并行度（batch concurrentSize）；0 = 平台上没有该键 ⇒ **未设**
	// （规范形不写该键，正编译物化目标缺省 —— pkg/ir/parallel_for_defaults.go）。
	// OnError 迭代失败策略：coze Batch **无容器级错误开关**，平台行为恒等于
	// `terminate`（= 语言缺省）⇒ 反编译合成 `terminate`，写回文本时按规范形省略
	// （render_ncoda.go::renderParallelForConfig；正编译不消费该值）。
	Parallel    bool
	Concurrency int
	OnError     string
	// WireIterVar: 该容器**迭代源输入口在 wire 上的字段名**（T3）。lowering 把它
	// 写成容器的 `node_inputs[0].name`（internal/backend/cozebiz/render_loop.go:
	// `inputs[0].Name = s.VarName`），体内对该元素的引用是
	// `{ref_node: <容器>, path: [<该名字>, …]}` —— 即它是**局部键**（322 份真实
	// 导出 · 488 条容器输入里只有 96 条与源端口 path 相等 ⇒ 平台不要求它等于任何
	// 外部身份）。raising 的命名 pass 据此把自造名 `item_<容器 id>` 还原为 wire 名
	// （可拼写且不捕获体内引用时；判据见 iter_var_name.go / raising_canonical.go）。
	// 空 = 没有可采纳的 wire 名（zip 多源 / 定次循环 / 名字不可拼写）。
	WireIterVar string
}

// FlattenValueLoop 报告该绑定初值是不是**展平值循环**（其被展平的口非空）。
// 读点：render_ncoda 的 `var`/`let`、unused_drop 的保留判据；写点只有一处
// （generateLoop 按 g.flattenedLoops 写入 FlattenPorts）——一处定义。
func FlattenValueLoop(init Expr) bool {
	fe, ok := init.(*ForExpr)
	return ok && fe != nil && len(fe.FlattenPorts) > 0
}

func (f *For) stmt() {}

// IsZip 报告该循环是否为 zip 多源循环。
func (f *For) IsZip() bool { return len(f.Sources) > 1 }

// LoopVars 返回全部变量名（单源时 [Var]）。
func (f *For) LoopVars() []string {
	if f.IsZip() {
		return f.Vars
	}
	if f.Var == "" {
		return nil
	}
	return []string{f.Var}
}

// LoopSources 返回全部源（单源时 [Iterable]）。
func (f *For) LoopSources() []Expr {
	if f.IsZip() {
		return f.Sources
	}
	return []Expr{f.Iterable}
}

// ForExpr 值循环表达式（for (x in items) { body }）——作为 VarDecl init：
// 循环表达式结果 = 循环聚合输出（coze loop/batch 的 output 口）。循环体含
// 命名 yield（Yield.Port）时逐口聚合，正编译渲染 loop.outputs 多条目。
type ForExpr struct {
	Var      string
	Iterable Expr
	// Vars/Sources zip 多源（同 For）。
	Vars    []string
	Sources []Expr
	Body    *Block
	// Parallel 并行值循环（coze Batch）：语法 parallel for (...)，同 For
	// （Concurrency 0 / OnError 空 = 未设，规范形不写）。
	Parallel    bool
	Concurrency int
	OnError     string
	// WireIterVar: 该容器**迭代源输入口在 wire 上的字段名**（T3）。lowering 把它
	// 写成容器的 `node_inputs[0].name`（internal/backend/cozebiz/render_loop.go:
	// `inputs[0].Name = s.VarName`），体内对该元素的引用是
	// `{ref_node: <容器>, path: [<该名字>, …]}` —— 即它是**局部键**（322 份真实
	// 导出 · 488 条容器输入里只有 96 条与源端口 path 相等 ⇒ 平台不要求它等于任何
	// 外部身份）。raising 的命名 pass 据此把自造名 `item_<容器 id>` 还原为 wire 名
	// （可拼写且不捕获体内引用时；判据见 iter_var_name.go / raising_canonical.go）。
	// 空 = 没有可采纳的 wire 名（zip 多源 / 定次循环 / 名字不可拼写）。
	WireIterVar string
	// FlattenPorts = 该值循环**被展平**的输出口集合（wire 上由编译器自建的
	// 展平垫片承载，raising 折回后落在这里；见 flatten_adapter_fold.go）。
	// ncoda 拼写 = 绑定用 `var` 而非 `let`（编译端
	// pkg/ir/build_call.go：`flattenOutput = s.IsMutable()`），
	// 因此它同时决定 ① `var` 关键字（render_ncoda.go）与 ② 该循环表达式的
	// **值类型**（`array<list<T>>` → `list<T>`，见 forExprValueType）。
	// 空 = 普通值循环（`let`，收集不展平）。
	FlattenPorts map[string]bool
}

func (f *ForExpr) expr() {}

// IsZip 报告该循环是否为 zip 多源循环。
func (f *ForExpr) IsZip() bool { return len(f.Sources) > 1 }

// LoopVars 返回全部变量名（单源时 [Var]）。
func (f *ForExpr) LoopVars() []string {
	if f.IsZip() {
		return f.Vars
	}
	if f.Var == "" {
		return nil
	}
	return []string{f.Var}
}

// LoopSources 返回全部源（单源时 [Iterable]）。
func (f *ForExpr) LoopSources() []Expr {
	if f.IsZip() {
		return f.Sources
	}
	return []Expr{f.Iterable}
}

// Yield 循环体发布语句：Port 命名输出口（coze loop/batch outputs 端口名，
// 空串 = 默认口 "output"）；Value 为该口每迭代聚合的源值。
type Yield struct {
	Port  string
	Value Expr
}

func (y *Yield) stmt() {}

// OutputStmt 中间消息发射语句：output(<expr>) [streaming];。与 Return 不同，它
// 继续控制流；dify 链式 answer 的前序节点使用该语义。
//
// Value 是**值位**：既可以是内插串（名字由文本反推），也可以是具名名额表
// （`template "…" using {…}`，作者的显式名额）。两条 wire 形状共用**一个**
// 语句载体 —— 语句只有一种拼写（`output(...)`），载体分家会让每个遍历漏掉
// 其中一支（2026-09-27 归一：此前内插形态走 ExprStmt+`output` 调用）。
//
// Streaming = 节点自己的投递开关（平台 `streamingOutput`，用户 2026-09-27
// 裁决 甲）：true ⇒ 回写 ` streaming`。
type OutputStmt struct {
	Value     Expr
	Streaming bool
}

func (o *OutputStmt) stmt() {}

// Return 返回语句。Streaming = 答复端点的投递开关（平台 `streamingOutput`）：
// 只在模板值返回上成立（`return template "…" streaming;`）。
type Return struct {
	Value     Expr
	Streaming bool
}

func (r *Return) stmt() {}

// Break 中断。
type Break struct{}

func (b *Break) stmt() {}

// Continue 继续。
type Continue struct{}

func (c *Continue) stmt() {}

// Comment 注释。
type Comment struct{ Text string }

func (c *Comment) stmt() {}

// ImportStmt 平台限定 import（顶层声明，渲染时提升到 @mode 之后）。
type ImportStmt struct{ Path string }

func (i *ImportStmt) stmt() {}

// Expr 表达式节点。
type Expr interface{ expr() }

// IdentExpr 标识符。
type IdentExpr struct{ Name string }

func (i *IdentExpr) expr() {}

// LiteralExpr 字面量。
//
// SpellType 标记「这个标量的**拼写就是它的类型载体**」——取整浮点必须补 `.0`，
// 否则 ncoda lexer 读回 INT_LITERAL ⇒ 值类型在往返中丢失（R-98：
// `{type: float, value: 1}` → `1` → `{type: integer, value: 1}`）。
// 命中位点（判据 = 「重编译后 wire 的 `type` 由谁决定」）：
//   - 平台 wire `{type, value}` 信封的字面量输入（`convertSource`）；
//   - shim 正文里的 python 字面量（`shim_reverse.go`：反向词法按拼写分 int/float）。
//
// 其余位置该位为 false —— 类型由**别处**承载，拼写只求与既有文本 / 编译器再
// 序列化逐字一致：JSON 值树成员（JSON 只有一种 number）、具名 schema 键
// （llm `temperature` / `top_p`：键名即类型）。
type LiteralExpr struct {
	Value     any
	SpellType bool
}

func (l *LiteralExpr) expr() {}

// CallExpr 函数调用。Receiver 非 nil 时渲染为 <receiver>.<callee>(...)（限定名调用）。
type CallExpr struct {
	Callee   string
	Receiver Expr     // 可选：限定名接收者（如插件 slug）
	TypeArgs []string // 可选：调用类型实参（llm<array<string>> 等）
	Args     []Expr
}

func (c *CallExpr) expr() {}

// OperationPolicyExpr 是算子调用的**错误策略后缀**——ncoda 既有语法的
// `op(...) with retry(max:2), timeout(30s)`：操作的失败处理与韧性修饰挂在
// 调用点上（策略是"这一次调用怎么做"，不是算子类型的属性）。
//
// 只有能承载策略的算子才允许被包装（编译端 pkg/passes/type_ops.go 的
// OperationPolicySupport 硬校验，违规是 1045 OPERATION_POLICY 错误），因此
// 构造责任在 convertNode 的策略派生处，不在各节点转换器里。
//
// Retry / Timeout 为 0 表示该策略缺席（平台无此字段或值等于 lowering 的
// 无策略默认——默认不写，见 cozebiz.ErrorPolicyDefaults）。
type OperationPolicyExpr struct {
	Base    Expr
	Retry   int // with retry(max:N)；N > 0
	Timeout int // with timeout(<D>ms)；D > 0 且为整秒（编译端要求）
	// Default 是 `with default(<记录字面量>)` 的失败缺省载荷（PT2 还原；
	// nil = 无该策略）。
	Default Expr
}

func (o *OperationPolicyExpr) expr() {}

// LambdaExpr 匿名函数 `(params) -> body`（ncoda 既有语法;dify list-operator
// 反编译 filter 谓词用）。Body 为单个表达式。
type LambdaExpr struct {
	Params []string
	Body   Expr
}

func (l *LambdaExpr) expr() {}

// MemberExpr 字段访问 obj.field。
type MemberExpr struct {
	Obj   Expr
	Field string
}

func (m *MemberExpr) expr() {}

// DictExpr 字典字面量（有序）。
type DictExpr struct {
	Keys   []string
	Values []Expr
}

func (d *DictExpr) expr() {}

// ListExpr 列表字面量。
type ListExpr struct{ Items []Expr }

func (l *ListExpr) expr() {}

// DictExprBuilder 构造有序字典。
func newDict() *DictExpr { return &DictExpr{} }

func (d *DictExpr) Put(key string, value Expr) {
	d.Keys = append(d.Keys, key)
	d.Values = append(d.Values, value)
}

// BinaryExpr 二元运算（==/!=/>/>=/</<=/&&/||；len 比较的 left 为 CallExpr len）。
type BinaryExpr struct {
	Op    string
	Left  Expr
	Right Expr
}

func (b *BinaryExpr) expr() {}

// UnaryExpr 一元表达式 `!x`（2026-09-01, EMPTY-CHECK-DESIGN）：
// `!empty(x)` 反编译形态（not_empty 单目检查的 ncoda 第一性表达）。
type UnaryExpr struct {
	Op      string // "!"
	Operand Expr
}

func (u *UnaryExpr) expr() {}

// TernaryExpr 三元表达式 `cond ? then : else`（ncoda 既有语法；shim 可逆
// 机制还原编译器 Python 三元的 ncoda 第一性形态，2026-09-02）。
type TernaryExpr struct {
	Cond Expr
	Then Expr
	Else Expr
}

func (t *TernaryExpr) expr() {}

// IndexExpr 下标访问 obj[index]（E5-b 数组元素访问）。
type IndexExpr struct {
	Obj   Expr
	Index Expr
}

func (i *IndexExpr) expr() {}

// TemplateString 模板串（ncoda 反引号 `${expr}` 形式）：parts 交替
// 字面文本/插值表达式，与 ncoda ast.TemplateString 对齐。
type TemplateString struct {
	Parts []TemplatePart
}

func (t *TemplateString) expr() {}

// TemplateValue 模板串的**具名名额**形态（表达式位）——正本
// `docs/five-step/spec/TEMPLATE-STRING-CANONICAL-FORM.md`：
//
//	template "<逐字文本>" using { name: <值路径>, ... }
//
// 三个位点共用这一个**模板串载体**（文本 + 名额表），宿主各不相同：
//
//	| 宿主 | 拼写 | 落点（coze-biz） |
//	| `Return{Value: …}` | `return template …;` | `end{terminatePlan: useAnswerContent}` |
//	| `OutputStmt{Value: …}` | `output template …;` | `output` 节点（type 13） |
//	| llm message content（值位） | `content: template …` | 节点 `prompt`/`systemPrompt` 槽 |
//
// 文本逐字承载（零解析），名额名由作者写死（具名名额）；判定式 = 正本 §2 三句式。
type TemplateValue struct {
	Text     string
	Bindings []TemplateSlot
}

func (t *TemplateValue) expr() {}

// TemplatePart 模板串片段：Text 为前置字面文本，Expr 非 nil 时插入 ${expr}。
type TemplatePart struct {
	Text string
	Expr Expr
}

// ── Foreign code 表达（coze CodeRunner → ncoda foreign code）──

// ForeignCodeOutputField 输出契约字段（name: type）。
type ForeignCodeOutputField struct {
	Name string
	Type string
}

// ForeignCodeExpr foreign code 调用：
// foreign code <lang>(<typed inputs>) -> <output> { source `<code>`; }
type ForeignCodeExpr struct {
	Language   string // "python3" | "javascript"
	Inputs     []CodeInputBinding
	Output     []ForeignCodeOutputField // 空 + Direct 时用 DirectType
	Direct     bool                     // true: direct output（隐式 result 端口）
	DirectType string                   // direct 输出类型（Direct 时有效）
	Source     *TemplateString
}

// CodeInputBinding 输入绑定：`<type> <name> = <expr>`。
type CodeInputBinding struct {
	Type string
	Name string
	Expr Expr
}

func (f *ForeignCodeExpr) expr() {}

// AssignStmt 赋值语句（条件分支变量提升：var v; 在外层声明，分支内
// v = <expr> 赋值 —— coze 画布允许跨分支引用，ncoda 块作用域不允许，
// 提升是作用域修复手段，正编译按赋值位置物化节点，画布结构不变）。
type AssignStmt struct {
	Target string
	Op     string // 赋值操作符（"" = 直赋;"<<" = 数组追加;其余二元复合操作符原样）
	Value  Expr
}

func (a *AssignStmt) stmt() {}

// TypeDecl 顶层命名 record 类型声明（object schema → ncoda 类型）。
// 渲染时提升到 function main 之前（imports 之后）。
type TypeDecl struct {
	Name   string
	Fields []TypeField
}

func (t *TypeDecl) stmt() {}

// SubflowDecl 顶层子工作流声明（SUBWORKFLOW-DECLARATION）：
//
//	subflow <name> = ("<workflowId>", "<version>") {
//	    input:  { <type> <field>; ... },
//	    output: { <type> <field>; ... }
//	};
//
// 身份 = workflowId + workflowVersion（平台没有名字，名字是 ncoda 别名）；
// input/output 契约来自 yaml inputDefs 与 node_outputs（声明式节点, 静态
// 完整）。渲染时提升到 function main 之前（imports 之后, type 声明之后）。
type SubflowDecl struct {
	Name            string
	WorkflowID      string
	WorkflowVersion string
	SpaceID         string
	InputFields     []TypeField
	OutputFields    []TypeField
}

func (s *SubflowDecl) stmt() {}

// TemplateSlot 模板串的一个名额：wire `node_inputs[]` 的一条（名字 + 值路径）。
// 顺序 = wire 顺序（保真的必要条件）；名字**只**来自 wire `node_inputs[].name`
// （零解析：不看文本里的占位符 —— 旧 `@answer` 通路拿模板反推绑定 ⇒ 46 份真实导出
// 整份读不了，rules/A13-return-template.yaml#A13-H6）。
type TemplateSlot struct {
	Name  string
	Value Expr
}

// EnumDecl 顶层命名枚举类型（classify<EnumT>() 的枚举实参）。
// 渲染为 `enum <Name> { <m1>, <m2> };`（ncoda 原生枚举,成员名需合法
// 标识符且互不相同）。
type EnumDecl struct {
	Name    string
	Members []string
}

func (e *EnumDecl) stmt() {}

// TypeField record 类型字段。
type TypeField struct {
	Type string
	Name string
}

// Switch switch 多出口语句（coze IntentDetector type 22）：
//
//	switch ( <query> ) {
//	    "<label>": { ... }
//	    ...
//	    default: { ... }
//	}
//
// Dangling 分支（canvas 声明分支但无对应 branch 边，或分支直达汇聚 =
// 空体）→ 反编译显式 FAILED 标记（Comment），case 不生成（空分支 =
// ncoda 错误，user rule）。
type Switch struct {
	Query Expr
	// Config = 分类器配置（`switch (q, { … })`），nil / 空 = 全部按平台默认
	// （§5-5「默认值 → 省略 config」）。**只装本语言承载面装得下的键**
	// （temperature / max_tokens / top_p / mode / system_prompt）；平台写了
	// 而本语言装不下的键**不进 config、由 RS-SWITCH-001 报账**（R-70：
	// 装不下可以，静默不可以）—— 所以 config 的缺席 ≠ "平台没写"。
	Config      *DictExpr
	Result      string // 结果绑定名（"" = 无绑定）
	Branches    []SwitchBranch
	HasDefault  bool
	DefaultBody *Block
}

func (s *Switch) stmt() {}

// SwitchBranch 单个 case。
type SwitchBranch struct {
	Label string
	Body  *Block
	// Dangling 标记：canvas 声明该意图但无执行路径（悬挂）。
	Dangling bool
}

func (b *SwitchBranch) stmt() {}

// AttemptStmt 副作用操作的两臂错误分支（coze-biz settingOnError.processType=3
// 的 default / branch_error 双臂菱形 → ncoda attempt 语句）：
//
//	attempt <Operation> as <Result> {
//	    success { ... }
//	    failure(<Error>) { ... }
//	}
//
// Operation 是操作节点的调用表达式（`with retry(...)` / `with timeout(...)`
// 策略后缀属于它，挂载见 convert_error_policy.go）；Result / Error 是绑定名
// （臂体内对操作节点的引用分别解析到它们，见 Generator.attemptErrorAlias）。
// 两臂都是必填（ncoda 语法如此，空臂 = 空 Block）。
type AttemptStmt struct {
	Operation Expr
	Result    string
	Error     string
	Success   *Block
	Failure   *Block
}

func (a *AttemptStmt) stmt() {}

// RequestInputStmt 挂起式人工输入（dify human-input / coze QA → ncoda ask）：
//
//	ask<Form, Action>({config}) as <result> {
//	    action Action.<member> { ... }
//	    ...
//	    timeout { ... }   // 可选（无超时边时缺省）
//	}
//
// FormType 是 record 类型名（表单字段声明），ActionType 是 enum 类型名
// （动作按钮枚举）；config 的 content/fields/actions/timeout 是平台中性
// 语义载荷（dify form_content/inputs/user_actions/timeout 的归一化形态）。
// 渲染时 form/action 类型声明（TypeDecl/EnumDecl）由调用方提升到文件头。
type RequestInputStmt struct {
	FormType       string               // record 类型名
	ActionType     string               // enum 类型名
	Content        Expr                 // content：静态字符串（*LiteralExpr）或带作者名额表的模板（*TemplateValue）
	Fields         []RequestInputField  // 表单字段
	Actions        []RequestInputAction // 动作按钮
	TimeoutValue   int                  // 0 = 无超时
	TimeoutUnit    string               // "" = 无超时
	ResultBinding  string               // as <result> 绑定名
	ActionBranches []RequestInputBranch // action 分支（动作名 → body）
	TimeoutBody    *Block               // 可选 timeout 分支体

	// ── coze-biz `question` 参数面 ②③（2026-10-10 用户批准「丙·扩语言」）──
	// 平台 `question` 的参数面三块：① content(+bindings) ② limit/extra_output
	// ③ llmParam。②③ 在语言面无槽位时往返整块蒸发（一手：唯一源
	// `7574994076144779304_calendar_helper`）⇒ 加槽承载。
	Limit       int              // 平台 `limit`（0 = 缺席，与平台 `omitempty` 同义）
	ExtraOutput bool             // 平台 `extra_output`（false = 缺席）
	LLM         *RequestInputLLM // 平台 `llmParam`（nil = 该节点没有这个键）
	// DynamicOptions = 平台 `option_type: dynamic` 的选项来源（`dynamic_option`
	// 引用身份还原出的值表达式）。nil = 静态选项 / 文本型。
	DynamicOptions Expr
}

// RequestInputLLM 是 ask 宿主节点**自带**的模型配置（平台 coze-biz `question`
// 的 `llmParam`，平台类型 `SimpleLLMParam` = 扁平标量表）。
//
// **指针 = presence**：nil = 平台导出没写这个键 ⇒ 语言面也不写（平台会写
// `systemPrompt: ""` 这种「写了、值空」的键，值类型分辨不出 ⇒ 必须用 presence）。
// 键词汇 = `llm()` 的旋钮词汇（一处定义 = docs/UNIFIED-LLM-SEMANTICS.md §1/R-70）。
type RequestInputLLM struct {
	Model               *string  // `model` = "coze:<modelType>"（wire `modelType` 是整数）
	ModelName           *string  // `model_name`
	Temperature         *float64 // `temperature`
	MaxTokens           *int     // `max_tokens`
	TopP                *float64 // `top_p`
	ResponseFormat      *int     // `response_format`
	GenerationDiversity *string  // `generation_diversity`
	SystemPrompt        *string  // `system_prompt`
}

func (r *RequestInputStmt) stmt() {}

// RequestInputField 一个表单字段（dify inputs[i] / coze node_outputs）。
type RequestInputField struct {
	Name    string   // 字段名（output variable）
	Kind    string   // "paragraph" | "select"
	Options []string // select 选项（paragraph 为空）
}

// RequestInputAction 一个动作按钮（dify user_actions[i]）。
type RequestInputAction struct {
	Member string // enum 成员名（dify action id）
	Title  string
	Style  string
}

// RequestInputBranch 一个 action 分支（选中该动作后续执行的 body）。
type RequestInputBranch struct {
	Member string
	Body   *Block
}
