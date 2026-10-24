// The IR builder: lowers a TypedBoundProgram into a platform-neutral
// workflow graph (docs/WORKFLOW-IR.md). The lowering state machine was
// migrated from the Coze generator (internal/backend/coze/gen.go) so the
// Coze backend becomes a pure IR→Canvas renderer — semantics live in one
// place, platform shape in the backend mapping table.
package ir

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/builtin"
	"github.com/nodecoda/ncoda/pkg/capability"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/importns"
	"github.com/nodecoda/ncoda/pkg/llm"
	"github.com/nodecoda/ncoda/pkg/types"
)

// UnsupportedError is the expressiveness-gap error. Its message keeps the
// historical "coze: unsupported construct:" prefix (diagnostics are a wire
// contract); the Coze backend re-wraps it as its own ErrUnsupported.
type UnsupportedError struct{ Construct string }

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("coze: unsupported construct: %s", e.Construct)
}

// LanguageError is a source-level rejection raised by the IR phase: the
// program violates a language rule, so the author must change the source.
// It is NOT an expressiveness gap (UnsupportedError ⇒ internal) and NOT a
// platform limitation — the compiler maps it to a `class=language` diagnostic
// carrying Code (pkg/diag) verbatim. 零值 Code = 不指定码（调用方自行
// 决定；常规语言错一律带码）。
type LanguageError struct {
	Code    diag.Code
	Message string
	// Name/Line/Column 可选：IR 只在能给出权威位置时才填（post-build 判据
	// 拿不到 span ⇒ 留空，绝不猜一个行号）。
	Name   string
	Line   int
	Column int
}

func (e *LanguageError) Error() string { return e.Message }

// MissingDeclarationError 是**输入配置**缺失（不是源码错、不是平台限制、
// 不是编译器缺陷）：编码这个值需要的声明不在能力注册表 / 能力头文件里
// （典型：插件声明了操作但没声明它的输出表）。作者无从改自己的代码 ——
// 正确动作是补能力声明（或写明字段）。
//
// 与 LanguageError 分开是**类别契约**的要求（AGENTS.md「错误分类契约」：
// 类别由发出诊断的一方声明）：两者的用户动作完全不同，合并会把「你写错了」
// 与「你的注册表缺一条」混成同一句话。
type MissingDeclarationError struct {
	// Capability 是机器可读的能力名（诊断的 capability 字段）。
	Capability string
	Message    string
}

func (e *MissingDeclarationError) Error() string { return e.Message }

// constructOf unwraps an error chain to its user-facing construct text
// WITHOUT the platform "coze: unsupported construct:" prefix, so entry
// points (if condition / map value / array element) can build one clean
// guided sentence instead of nested prefixed messages (§5.1).
func constructOf(err error) string {
	if ue, ok := err.(*UnsupportedError); ok {
		return ue.Construct
	}
	return err.Error()
}

// varRef is a build-time variable binding: producer node, output port
// (dotted for nested field paths), family kind, and optional parallel-record
// fields.
type varRef struct {
	nodeID string
	output string
	kind   string
	// typ is the semantic result shape when the producer is a capability
	// call. The backend manifest finally renders physical ports, but the IR
	// needs this shape to map a converged record field (for example
	// data.duration) to the real nested producer port.
	typ *Type
	// jsonParseArray marks a var whose producer is a json.parse<array<T>>
	// top-level array shim (G12-2). Element access (items[i].field, any
	// constant i) must NOT resolve onto dotted output ports — the shim
	// exposes the whole list on one port with no member fields — so index
	// chains over it materialize as guarded extraction shims instead
	// (E5-b item-0 dotted ports apply to record-typed outputs, not to
	// top-level array shims).
	jsonParseArray bool
	// ports 记录值循环聚合变量（let a = for/parallel for {...}）在构建时的
	// 声明输出端口（sortedKeys(OutTypes)）。聚合变量上的成员链 = 逐元素
	// 投影，compile 必须组合成 <端口>.<成员...> dotted path（永不吞段）；
	// 多口循环第一段若是声明端口 = selector。非循环聚合的 ref 不设置。
	ports []string
}

// NodeID / Output export the loop return-value ref to the backend layers
// (spec.Build short-circuit + dify render need the producer/port pair).
func (r varRef) NodeID() string { return r.nodeID }
func (r varRef) Output() string { return r.output }
func (r varRef) Kind() string   { return r.kind }

// litVar is a scalar literal binding not yet materialized into a node; it
// seeds loop-state variableParameters when the variable becomes a loop
// intermediate.
type litVar struct {
	value string
	typ   string
}

// pendingElseState carries the then-branch result of an else-less if until
// the following return statement arrives to fill the else arm. treeIf is
// the structured IfStmt under construction (its Else block is filled when
// the pending return resolves the unmatched path).
type pendingElseState struct {
	selID        string
	thenIDs      []string
	thenRef      *varRef
	thenProduced bool
	// thenFlowEnd 报告 then 臂是否**终止本流程**（主流程 return / void
	// return；QA·attempt 分支结果值与 breakish 都不设置它，仍走 junction
	// 通路）。它决定尾 return 的**降级通路**：终止 ⇒ 本 if 无续接路径，
	// 尾 return 是一条**出口语句**（emitReturn），与显式 `else { return v; }`
	// 逐字同规则；不终止 ⇒ 尾 return 是 else 臂的**值**，由汇聚单元收口
	// （A12-H12，2026-09-15）。
	thenFlowEnd bool
	treeIf      *IfStmt
}

// textBinding pairs a template placeholder with its block-output ref.
//
// key  = **去重键**（被引用的源码变量名：同一变量多次引用共用一条绑定）；
// name = **绑定名**（语义锚 = 生产者端口，见 binding_label.go）。
// 两者必须分开：键取身份、名取可逆性 —— 用源码名当名字就是往返改名的根因。
type textBinding struct {
	key  string
	name string
	ref  varRef
}

// Builder lowers a TypedBoundProgram into a platform-neutral IR graph.
type Builder struct {
	prog  *binding.TypedBoundProgram
	graph *Graph
	nodes []Node // private flat baseline retained during D9 migration
	// armGateMemo memoizes armSubtreeNodeIDs per arm block (arm_gate_candidate.go).
	armGateMemo map[*Block]map[string]bool
	// recordArmRelayMemo memoizes the per-arm whole-record carrier materialized by
	// the arm gate (arm_gate_candidate.go::recordRelayInArm): one carrier per
	// (arm, record source), shared by every member candidate of that record.
	recordArmRelayMemo map[recordArmRelayKey]string
	consts             map[string]string
	nextNodeID         int
	// allocatedIDs 记录构造期分配过的**全部** node id（按分配顺序）。id 是
	// 产物契约的一部分：分配了却没落地的 id 会在产物里留下空洞，同一语义在
	// 「有该形状」与「没有该形状」两轮之间整块错位（C∘R∘C 字节闭环破裂）。
	// 记录分配集合 ⇒ 下游能算出「未落地足迹」并一次性压缩掉（见
	// backend/cozebiz/compact_pruned_ids.go）。
	allocatedIDs []string
	varToOutput  map[string]varRef
	tailRef      *varRef
	pendingElse  *pendingElseState
	// flowExits collects the main-flow value-carrying exits (`return v`) in
	// source order. The End binding is decided once ALL exits are known
	// (materializeFlowExitPhi): the old first-write-wins rule let the first
	// exit statically own end.node_inputs and silently dropped every later
	// value. Void exits contribute nothing — a side-effect-only exit may
	// wire straight into the End.
	//
	// 登记的是**值**（flowExit），不是「为它物化的那个节点」：同一个值可以有
	// 多条出口命名它（`if (c) { return v; } return v;`），而平台的引用形式要求
	// 每条臂各有一个承载节点 ⇒ 值相同、候选节点不同。值身份相同的出口是**一条**
	// 出口（判据见 flowExit）；kind 随候选一起记（`flowExit.kind`），因为
	// record 值投影到逐字段组时只靠 Ref 无法复原生产者声明的字段口。
	flowExits []flowExit
	// inlineExits 是**当前内联帧**的值出口表（子函数体的每个 `return v` 一条，
	// 源码序）。与 flowExits 同一读法、同一单位（值 + 臂归属），只是汇聚点从
	// 「流程出口 phi」换成「帧出口 phi」（InlineStmt.Exit）：子函数在平台上没有
	// 表示 ⇒ 它的值由调用点的一条汇聚承载。
	//
	// 帧是**词法**范围：inlineCall 进入时快照/清空、离开时恢复（嵌套内联各得
	// 其帧，不互相看见），与 loopStack / inlineName 同一处屏障。
	inlineExits []flowExit
	// templateAnswer = 本流程的答复模板身份（`return template … using {…}`，W2）。
	// 值侧身份由**流程出口语句**给出，全部出口必须同身份（见 emitTemplateValue）；
	// 构建结束时挂到规范 End（attachTemplateAnswer）。
	templateAnswer *EndAnswer
	// unreachable > 0 while lowering a statement no execution can reach (dead
	// code after a statement that ends every path — see stmtEndsFlow). Such a
	// statement is still lowered (the projections cut it structurally), but it
	// is NOT a flow exit: recording its value would put a never-executed
	// producer into the flow-exit phi, and the projection — which cuts the
	// dead node — would then emit a dangling ref (RDANGLING_REF).
	unreachable int
	branchDepth int
	// armStarts = 每个在展开中的**分支臂**的构造期水量线（入口处的 id 水位，即
	// `100000+nextNodeID`），由 emitBranch 压栈/出栈。它只回答一个问题：某个引用
	// 指向的节点是不是**在本臂内产出**的（id ≥ 水量线）。多出口 record 收敛据此判断「臂 → 候选」映射是否还
	// 留在 wire 上（见 materializeFlowExitPhi）；别处不得据此做任何推断。
	armStarts []int
	// armBlocks = 与 armStarts 同序压栈的**臂块**（emitBranch 维护）：门控
	// （arm_gate_candidate.go）要在「本臂内」物化恒等载体，需要一个可前插语句的
	// 块。两者必须同进同出——水量线回答「是不是本臂产出的」，块回答「物化到哪里」，
	// 同一事实的两半，不许多一处维护。
	armBlocks []*Block
	// parallelDepth:parallel 分支内 > 0（emitParallel 维护）。分支内 return
	// 是 flow-end（P5 #4 流程结束、直连 end）。主流程（非子函数 inline）
	// 的 if/attempt/QA/intent 分支 return 同为 flow-end（ncoda 语义：主流程
	// 任何 return 都回到唯一 end 900001）；仅子函数内 return 是值回流。
	parallelDepth          int
	endCreated             bool
	entryReturns           bool // entry function has a return type
	branchReturns          bool // an explicit return occurred inside a branch
	branchFlowReturns      bool // a main-flow flow-end return occurred in the current branch
	lastBranchFlowReturned bool
	branchVoidReturns      bool // the branch ended in a value-less main-flow return
	lastBranchVoidReturned bool
	convVars               map[string]string
	// hoistedVars:反编译分支变量提升的裸声明（var v;）。赋值语句
	// v = <expr> 在该处物化节点（画布结构不变），var v; 本身零节点。
	// coze 画布允许跨分支引用（未执行分支 = null，merge 显式处理）。
	hoistedVars map[string]bool
	// hoistedTypes preserves the declared record shape for mutable variables
	// lifted across an if. It is used to build field-level phi groups.
	hoistedTypes map[string]*Type
	// foldedRecordFields / foldedRecordTypes / foldDecided：记录整值折叠（J §42.3 A 轴，
	// 见 record_fold.go）。值 = 叶单元承载的字段名；
	// 三态缓存：未裁决 / 裁决为折（表内有值）/ 裁决为不折（decided 为真而表内无值）。
	foldedRecordFields map[string]string
	foldedRecordTypes  map[string]*Type
	foldDecided        map[string]bool
	// claimedGroupNames = 本流程**已被占用**的汇聚组名：作者写过的 `Group<数字>`
	// 标识符（任何位置，见 unnamed_group_name.go）∪ 本流程分配出去的无名组名。
	// 无身份汇聚单元取其中**最小可用序号**（R-85：两个单元同名 ⇒ 一轮不动点破裂）。
	claimedGroupNames map[string]bool
	// unnamedUnits = 本流程**分配过无名组名**的单元（创建序），供上线后重排/自查
	// （见 canonicalizeFlowExitGroupName）。flowExitPhiUnit = 流程出口汇聚单元
	// （`End.FlowExitMerge` 的 legacy 单组形），它要拿最小可用号。
	unnamedUnits    []*Aggregate
	flowExitPhiUnit *Aggregate
	// usages：使用面账单（物化**前**预扫，usage_prescan.go）——记录折叠判据的输入。
	// 按绑定名聚合（近似：同名跨作用域合并；保守方向 = 合并后 P1/P2 更难成立）。
	usages map[string]*BindingUsage
	// hoistedOptional:裸声明带 Optional 类型（`RT7? v;`）的 hoisted 变量
	// （M1/R5，2026-09-05）。Optional 提升 = 未赋路径 null（平台 ref null
	// 跳过）；if 汇聚点对"未赋值分支 + 无 pre-if producer"（null base）不
	// 贡献零值默认候选（α 零值只归非 Optional）。typeOf 会解包 Optional
	// （IR Type 无 optional 位），故此处单独保留 Optional 信号。
	hoistedOptional map[string]bool
	// varDeclTypes:一切带类型注释的变量声明（`T v;` / `T v = expr;`，无论
	// mutable 与否、init 有无）的声明类型。emitVarDecl 顶部统一登记；循环
	// 状态变量分类（emitLoopNode）据此取声明类型权威（F1/F2/F4，2026-09-10）：
	// 状态 echo 类型轴必须复真声明类型（array<object> 态 / float 声明 + int
	// 字面量），禁止静默退化 string。hoistedTypes 仅覆盖"裸可变声明"子集且
	// 携带分支 phi 语义，不可作 init 声明/不可变声明的类型源。
	varDeclTypes   map[string]*Type
	lastBranchVars map[string]varRef
	// allNodes:全局节点索引（跨容器作用域**不**隔离）。容器隔离只作用于
	// 「结构归属」（节点属于哪个块 / 容器），不作用于「类型知识」——producer
	// 端口类型是全局事实（loop 翻译完整算法 §3 不变量 2，
	// docs/ideas-todo/2026-09-11-loop-translation-complete-algorithm.md）。
	// portTypeOf 在作用域注册表未命中时回查本索引，使循环体内对**外层
	// producer / 聚合 / 可寻址字面量写**的引用也能解析类型，而不是因为
	// `g.nodes = nil`（emitLoopNode 的体隔离）而 fail loud。禁止删除：删掉
	// 会把「同一引用在体内/体外可编译性不同」的形态特例重新引入。
	allNodes []Node
	// zeroShims:同臂同码零值默认 shim 复用池——同一分支块内多个 merge 组
	// 缺同一类型零值兜底（或同一直线区多个 `v = ""` 提升初始化）只物化一个
	// 纯常量节点（同控制路径同值，复用不出块 ⇒ 生产者恒在消费者上游，
	// P2 可达性不变，无新边）。键 = 分支块 × shim code。
	zeroShims  map[zeroShimKey]string
	loopStack  []string
	loopStates []map[string]bool
	// loopBodyTopDepth:每个循环体顶层的 branchDepth 快照（与 loopStack 同步
	// 压栈/出栈）。emitYield 据此判定 yield 是否位于"循环体顶层"（branchDepth
	// == 顶层快照）：循环体被 emitLoopNode 的 branchDepth++ 抬高 1 层,历史
	// `branchDepth == 0` 守卫把所有循环内 yield 都丢弃（命名口丢失 + 整节点
	// 引用回退 firstOutputPort 收窄 → round2 类型失真,W1/B8 契约断裂）。
	// if/else/parallel 分支内 yield 由分支聚合器合并（tailRef 回流）,不得
	// 注册（last-wins 绕过聚合器）。
	loopBodyTopDepth []int
	// loopReturns:loop body 内的 return 值引用,按 loopID 登记。loop 容器
	// 节点在 body 编译后才 add 到 g.nodes（emitLoopReturn 时 g.find 找不到
	// loop）,故经此 map 中转,emitLoopNode 末尾落盘到 loop.ReturnValue。
	loopReturns map[string]*varRef
	// syntheticTernary:三元表达式（cond ? a : b）编译为合成 return 的
	// 嵌套深度。合成 return 只是"分支值上浮"机制,不是用户的函数返回
	// —— 不得触发 emitLoopReturn（否则 loop body 内的三元表达式会被
	// 误判为 loop 内提前返回,M17 回归 X12/X20 的 batch 体）。
	syntheticTernary int
	// branchValueDepth:QA action / attempt success/failure 分支的 return
	// 是"分支结果值"（汇聚进 Aggregate）,不是函数返回 —— 与三元同类,
	// 不触发 emitLoopReturn（hitlLoopSrc 的 action ok { return "ok"; }
	// 回归:M16-2 HITL-in-loop 用例不得被 M17 语义改变）。
	branchValueDepth int
	// switchBranchDepth:intent 分支内 > 0（emitIntent 维护）。与 QA/attempt
	// 不同,intent 分支 return 是函数级 return（主流程 = 流程终止、直连
	// 唯一 end；S188 default→End 实证）—— emitReturn 据此与 branchValueDepth
	// 联合区分容器值回流（QA/attempt）与流程终止（if/intent）。
	switchBranchDepth int
	litVars           map[string]litVar
	funcs             map[string]*ast.FunctionDef
	codeFuncs         map[string]bool
	subflows          map[string]*ast.SubflowDecl
	// loopYields: 当前循环已发布的输出口（port -> per-iteration 值引用）。
	// 命名 yield（yield port = expr）每口一条；裸 yield（yield expr）落到
	// 默认口 "output"。同一循环体多口并存（coze loop/batch 多输出口）。
	loopYields map[string]*varRef
	// loopYieldEnvelope: 当前循环「行值信封」口（port -> 成员名 -> wire 口名）。
	// R-168：平台把容器口的元素声明成 record（键 = 体内产出端口名），
	// `yield out = { "k1": v1, … };` 的 record 字面量是**信封声明**而非运行期构造
	// （成员名只是那个产出端口的名字）⇒ lowering 折回产出者口本身，并把口的元素
	// 类型钉成 record{k1: T(v1), …}（不物化 record shim、不多造 φ 节点）。
	loopYieldEnvelope map[string]map[string]string
	inlineName        string
	types             map[string]*ast.TypeDecl
	enums             map[string]*ast.EnumDecl
	entry             *ast.FunctionDef

	// namespaces (TOOL-DIRECT-CALL-FORM): the set of imported provider
	// identifiers whose qualified member calls p.op({...}) lower to
	// ir.ToolCall. Built from prog.Program.Imports() in newBuilder,
	// mirroring Task 1's binder registration of package namespace symbols.
	// Provider names are arbitrary, so unlike std dispatch (which uses a
	// "std." prefix) this is a set-membership gate; `std` is never in the
	// set, so std.* calls always stay stdlib calls.
	namespaces map[string]bool

	// caps is the target platform's native-capability table (P0: native-first
	// lowering is data-driven). Nil means conservative (materialize anything
	// beyond ref × literal conditions).
	caps *capability.Table
	// wholeValueFieldsByNode 记录「产出方整值的语言字段表」权威（A12-H4 的
	// 分类判据用；键 = 产出方节点 id）。缺失 = 输入配置没声明 ⇒ config 类。
	wholeValueFieldsByNode map[string][]string

	// condMarks is the pass-0 marking result: per comparison expression,
	// whether the target can host it as a native condition operand (P0' —
	// graph traversal marks native vs materialize BEFORE emission; emission
	// consumes the marks as the single decision source).
	condMarks map[*ast.BinaryExpr]bool

	// placements is the native-vs-materialize decision log (direction 2,
	// PLACEMENT-PASS §5): one record per placement point, copied onto the
	// Graph at the end of build().
	placements []Placement

	// ── structured tree building (D9) ─────────────────────────────
	root       *Block   // top-level program tree
	blockStack []*Block // open block scopes (innermost last)
	treeNodes  []Node   // builder-created nodes not yet claimed by a Stmt
}

// BuildOption configures the IR build (target capabilities etc.).
type BuildOption func(*Builder)

// WithCapabilities supplies the target platform's native-capability table
// (P0: native-first lowering is data-driven; pass 1 maps what the platform
// natively hosts, pass 2 materializes the residual as Synthetic shims).
func WithCapabilities(t *capability.Table) BuildOption {
	return func(b *Builder) { b.caps = t }
}

// Build lowers a TypedBoundProgram into a platform-neutral IR graph.
func Build(prog *binding.TypedBoundProgram, opts ...BuildOption) (*Graph, error) {
	if prog == nil || prog.Program == nil {
		return nil, &UnsupportedError{Construct: "missing typed bound program"}
	}
	// 位置判据（R-78）：`template "…" [using {…}]` 只在三个承载位成立，落在值通道
	// 一律拒收 —— 一条规则、一个码、一条可操作消息（修前四条值物化路径各写一遍
	// 通用兜底，四种说法都看不出病根）。见 template_value_placement.go。
	if err := checkTemplateValuePlacement(prog.Program); err != nil {
		return nil, err
	}
	g := newBuilder(prog)
	for _, o := range opts {
		o(g)
	}
	g.markLoweringPass()
	return g.build()
}

// zeroShimKey：零值默认 shim 复用池键（分支块 × shim code）。块指针 = 同一
// if 臂 / 直线区的唯一身份（Block 无 ID 字段）；跨块共享破坏 P2 可达性，
// 故键含块。
type zeroShimKey struct {
	blk  *Block
	code string
}

// zeroStringShim returns the pooled empty-string constant for a block,
// creating it on first use. N hoisted `v = ""` initializers in the same
// straight-line region collapse onto one pure constant node: same value, no
// inputs, reuse confined to the same Block so the producer stays upstream of
// every consumer (P2). The node is a no-input CodeRun
// (`return {"result": ""}`), identical in code to the branch-default zero
// shim (shared pool via zeroShims).
func (g *Builder) zeroStringShim(block *Block) (varRef, bool) {
	if block == nil {
		return varRef{}, false
	}
	code := wrapShim(nil, zeroValuePyast(T("string")))
	key := zeroShimKey{blk: block, code: code}
	if id, ok := g.zeroShims[key]; ok {
		return varRef{nodeID: id, output: "result", kind: "text"}, true
	}
	id := g.nextID()
	g.add(&CodeRun{
		ID:        id,
		Language:  "python",
		Synthetic: true,
		Types:     map[string]*Type{},
		OutTypes:  map[string]*Type{"result": T("string")},
		ZeroOf:    T("string"),
		OutOrder:  []string{"result"},
		Code:      code,
	})
	g.zeroShims[key] = id
	return varRef{nodeID: id, output: "result", kind: "text"}, true
}

func newBuilder(prog *binding.TypedBoundProgram) *Builder {
	g := &Builder{
		prog:         prog,
		graph:        &Graph{Mode: prog.Program.Mode()},
		consts:       map[string]string{},
		varToOutput:  map[string]varRef{},
		convVars:     map[string]string{},
		hoistedVars:  map[string]bool{},
		hoistedTypes: map[string]*Type{},

		foldedRecordFields: map[string]string{},
		foldedRecordTypes:  map[string]*Type{},
		foldDecided:        map[string]bool{},
		// 保留集必须在任何物化之前算（R-85）：作者写过的 `Group<数字>` 标识符
		// 先占位 —— 无名单元不得从作者手里夺名（作者的工作流先存在，生成节点后追加）。
		claimedGroupNames: authoredGroupSpellings(prog.Program),
		// 使用面账单必须在任何物化之前算（`L` 的事前面）：Build 入口一次，之后只读。
		usages:          PrescanBindingUsages(prog),
		varDeclTypes:    map[string]*Type{},
		hoistedOptional: map[string]bool{},
		lastBranchVars:  map[string]varRef{},
		litVars:         map[string]litVar{},
		zeroShims:       map[zeroShimKey]string{},
		funcs:           map[string]*ast.FunctionDef{},
		codeFuncs:       map[string]bool{},
		subflows:        map[string]*ast.SubflowDecl{},
		types:           map[string]*ast.TypeDecl{},
		enums:           map[string]*ast.EnumDecl{},
		namespaces:      map[string]bool{},
	}
	program := prog.Program
	// Tool-direct-call-form namespace set: every imported provider is a
	// namespace whose qualified calls lower to ToolCall (mirrors Task 1's
	// binder registration). Only Imports() feed the set — std is never a
	// namespace here, so std.v1.* dispatch is untouched.
	for _, imp := range program.Imports() {
		// Platform-qualified imports ("coze-biz.web_search") register the
		// bare provider as the call namespace — the qualifier selects the
		// platform binding, never the source-level symbol
		// (docs/PLATFORM-MECHANISM.md §5.1). importns is the dependency-free
		// shared source of truth (tool → ir → binding would cycle).
		bare := imp.Provider()
		if _, name, err := importns.Split(bare); err == nil {
			bare = name
		}
		g.namespaces[bare] = true
	}
	for _, fn := range program.Functions() {
		g.funcs[fn.Name()] = fn
	}
	for _, fn := range program.CodeFunctions() {
		g.funcs[fn.Name()] = &fn.FunctionDef
		g.codeFuncs[fn.Name()] = true
	}
	for _, cv := range program.ConversationDecls() {
		g.convVars[cv.Name()] = typeName(cv.TypeRef())
	}
	for _, td := range program.TypeDecls() {
		g.types[td.Name()] = td
	}
	for _, ed := range program.EnumDecls() {
		g.enums[ed.Name()] = ed
	}
	for _, sd := range program.SubflowDecls() {
		g.subflows[sd.Name()] = sd
	}
	for _, cd := range program.ConstDecls() {
		if s, ok := stringValue(cd.Value()); ok {
			g.consts[cd.Name()] = s
		}
	}
	g.entry = program.Entry()
	return g
}

func (g *Builder) build() (*Graph, error) {
	// entry function body
	entry := g.entryFunc()
	if entry == nil {
		return nil, &UnsupportedError{Construct: "missing entry function"}
	}
	g.entryReturns = entry.ReturnType() != nil

	g.root = &Block{}
	g.blockStack = []*Block{g.root}

	// Conversation declarations are carried as graph-level semantic
	// payload (the backend renders its variable table).
	if g.prog != nil && g.prog.Program != nil {
		for _, cv := range g.prog.Program.ConversationDecls() {
			g.graph.ConversationVars = append(g.graph.ConversationVars, ConvVar{
				Name:    cv.Name(),
				Type:    g.convVarType(cv.TypeRef()),
				Default: convDefaultValue(cv.Default()),
			})
		}
	}

	startID := g.nextID()
	// Start inputs are output ports on the entry node; downstream references
	// them as block-output refs to blockID=startID. Params with a default are
	// optional (Type.Required).
	start := &Start{ID: startID}
	for _, p := range entry.Params() {
		var defVal any
		if d := p.Default(); d != nil {
			defVal = entryParamDefault(d)
		}
		start.Params = append(start.Params, Param{
			Name:     p.Name(),
			Type:     g.typeOf(p.TypeRef()),
			Optional: p.Default() != nil,
			Default:  defVal,
		})
		// kind="entry"：入口参数是 start 节点的**输出口**（口名 = 参数名，声明类型 = 参数
		// 类型）—— 成员读必须保留基段（`p.left`，见 fieldPort 的 entry 分支）。旧形不带
		// kind ⇒ 落到 fieldPort 末尾的 `return ref.output` 兜底 ⇒ **字段被吞**：
		// `function main(Pair p) -> string { return p.left; }` 的产物把整个 record 绑给
		// 声明为 string 的出口（一手复现 2026-09-27，CB-5）。
		g.varToOutput[p.Name()] = varRef{nodeID: startID, output: p.Name(), kind: "entry"}
	}
	g.add(start)
	g.graph.Entry = start
	g.treeNodes = nil // start is a graph-level node, not a statement

	_, _, err := g.emitStmts(entry.Body().Stmts(), []string{startID}, "")
	if err != nil {
		return nil, err
	}
	// Dangling else-less if: the unmatched path falls through the selector's
	// false port into the (synthesized) End.
	if g.pendingElse != nil {
		g.pendingElse = nil
	}
	// A function whose return lives inside branches never hits a top-level
	// ReturnStmt; synthesize the End node from the tail value so the canvas
	// always terminates at the canonical 900001 exit. A void function
	// without explicit returns (a bare trailing if, e.g. 14) emits no end
	// at all — matching the legacy flow legacy.
	if !g.endCreated {
		endID := g.endID()
		var inputs []Ref
		var outputs []EndOutput
		namedReturn := false
		if g.tailRef != nil {
			// 出口位置 != 只有显式 `return` 一处：这里的尾值绑定同样是**值被指名
			// 交给平台**的位置（隐式出口 = 流程最后一个语句的值），故与显式
			// `return v;` **共用同一条整值规范化**（A12-H4 根因，2026-09-17）：
			// 值由多个端口承载（http/tool/结构性 foreign code）⇒ 平台没有整值口
			// ⇒ 逐字段具名输出（键 = 语言字段名、path = 该字段的物理口），正是
			// 作者手写 `return {f: v.f, …}` 时走的那条通路。
			//
			// 旧实现直接绑 `ref.output`：多端口产出方在那里是**空口名**（http 的
			// `output`／整值标记），产物要么悬空（ARTIFACT_GATE，`let v = http(…)`
			// 收尾的流程实测失败），要么在插件恰好声明了同名口时**静默把字段当整值**
			// 绑到出口。两条都不是作者能据以行动的信息。
			if _, fields := g.wholeValueAtReturn(*g.tailRef); len(fields) > 0 {
				for _, f := range fields {
					outputs = append(outputs, EndOutput{
						Name: f,
						Ref:  Ref{Producer: g.tailRef.nodeID, Output: fieldPort(*g.tailRef, f)},
					})
				}
				// 字段序落规范序（与显式 `return {a: …, b: …}` 同一条尺）：
				// 尾值出口与具名出口在平台上是同一种绑定表，序必须只由字段名决定
				// （否则「尾值写整值」与「手写具名」两种写法给出两种产物，R-41）。
				outputs = canonicalEndOutputs(outputs)
				namedReturn = true
			} else {
				inputs = append(inputs, Ref{Producer: g.tailRef.nodeID, Output: g.tailRef.output})
			}
		}
		// The tail end is implicit (dify drops it) when the function never
		// returns: no return type and no explicit branch return.
		implicit := !g.entryReturns && (!g.branchReturns || g.rootEndsWithOutput())
		end := &End{ID: endID, Inputs: inputs, Outputs: outputs, NamedReturn: namedReturn, Implicit: implicit}
		g.add(end)
		// The synthesized terminal is a trailing implicit return in the
		// tree: Project reuses this exact End node (stable ids) and wires
		// the junction into it. 具名输出时树上的 ReturnStmt 不携带值（值在
		// End.Outputs 里）——与 emitMapReturn 的具名出口同形。
		var tv *varRef
		if g.tailRef != nil && !namedReturn {
			tv = g.tailRef
		}
		g.appendStmt(&ReturnStmt{ID: endID, Value: tv, End: end, SharedEnd: false, Implicit: true})
		g.consumeTreeNodes(endID)
	}
	// M17（循环体内 return）的目标物化：coze-biz 没有「从循环体返回整个流程」
	// 的原生节点，把该形状改写成「循环状态对 + 体内写状态并 break + 循环后
	// 门控」的普通语句结构（见 loop_return_materialize.go）。必须在 flow-exit
	// phi 之前：门控真臂的 return 要作为正常值出口参与收敛。
	g.materializeLoopReturns()
	g.materializeFlowExitPhi()
	if err := g.attachTemplateAnswer(); err != nil {
		return nil, err
	}
	g.graph.Root = g.root
	g.graph.AllocatedNodeIDs = g.allocatedIDs
	g.graph.Placements = g.placements
	markLLMEnvelopeUsage(g.graph, g.nodes)
	for _, node := range g.nodes {
		call, ok := node.(*LLMCall)
		if !ok || call.Result == nil || !call.Result.WholeEnvelopeConsumed || g.caps == nil {
			continue
		}
		// The placement decision is still keyed by target identity because the
		// open kernel cannot add a capability axis without breaking the
		// commercial module's v0.6.0 pin (cross-repo release lag, §5.1).
		// Moving this to a Table axis is a P2f follow-up.
		switch g.caps.Target {
		case "dify-1.16-graphon-0.6", "coze-studio-flow-1.0", "coze-1.0", "coze-biz-1.0":
			g.recordPlacement(call.ID, placementConstruct("llm", "whole-envelope"), PlacementShim, placementReason(PlacementCauseContract, "target assembles LLMResponse<T> from native payload/metadata paths and literal nulls"))
		case "coze-runtime-1.0":
			g.recordPlacement(call.ID, placementConstruct("llm", "whole-envelope"), PlacementNative, placementReason(PlacementCauseContract, "runtime exposes the normalized LLMResponse<T> directly"))
		}
	}
	g.graph.Placements = g.placements
	optimizeGraph(g.graph)
	// 优化之后：没有接收方的引用不该留在树上（引用 = 生产者 + 接收方）。臂终端
	// End 的 Inputs 是「这条臂逃出的值」的**副本**，唯一接收方是该语句的汇聚单元；
	// 汇聚单元被判死摘掉后它就是孤儿引用，而树级读数（含反射遍历）会把它当读者
	// ⇒ 合成出没人读的载体（R-04：`gen_parallel_d3_s3_seed0` 的 llm 信封节点，
	// nodes 84 → 83 的 ARTIFACT_DRIFT/TOPOLOGY@r2）。见 orphan_arm_carrier_refs.go。
	dropOrphanArmCarrierRefs(g.graph)
	// 上线后重排（R-85）：出口汇聚单元的号要取「产物里**没有**单元在用的最小号」
	// —— 先物化、随后被剪掉的死脚手架占过的号必须释放（见
	// canonicalizeFlowExitGroupName）。必须在 optimizeGraph 之后（那时才知道谁上线）。
	g.canonicalizeFlowExitGroupName()
	// 语言层判据（A12-H4）：record 值没有单一承载口时只能在流程出口出现。
	// 必须在 IR 优化**之后**：优化会摘掉无消费者的死脚手架（未使用的字面量
	// shim、尝试臂终端…），而那些脚手架**不是作者写的引用**——判据只能看
	// 幸存下来的（真实的）引用。
	if err := g.checkWholeValueRefs(); err != nil {
		return nil, err
	}
	// 内部不变量（R-85）：无名汇聚单元必须有身份。漏一个物化出口 = 两个单元同名
	// = 静默漂移，故这里硬报错而不是等语料/生成器发现。
	g.assertUnnamedUnitsAreNamed()
	// 端点绑定的**被引用值类型**（CB-10）：必须在所有 End 改名 / 重排之后
	// （canonicalizeFlowExitGroupName 会换组名，而类型按含组名的 Ref 取）。
	// 平台 `input.type` 是运行期读的**语义**（填零 / 归一），故按语言类型投影；
	// 取不到 ⇒ 不发射（渲染侧判 nil）。
	g.stampEndPortTypes()
	return g.graph, nil
}

// attachTemplateAnswer 把流程出口的 `return template … using {…}`（W2）挂到
// 规范 End（900001）上：coze-biz 端点 = `terminatePlan: useAnswerContent`
// + **逐字模板** + **名额表**（wire `node_inputs`）。
//
// 这一层只做三件事，**不读模板文本**（W2 §2 零解析边界，用户 2026-09-17 拍板）：
//
//	· 名额名 → 生产者引用（值路径解析，失败 ⇒ 语言错，不猜端口）；
//	· 「模板与名额必须配对」的旧判据**整条删除**（占位符是 runtime 的事）；
//	· 「同一流程只能用一种出口值身份」的共存判据（与带值 return 不并存）。
//
// 值侧身份由**语句**给出（`return template`），不再由文件头声明给出：答复端点
// 是**流程出口**，不是语言里的全局声明（W1-D6 / W2 §4 载体归属）。
func (g *Builder) attachTemplateAnswer() error {
	if g.templateAnswer == nil {
		return nil
	}
	end, ok := g.find(g.endID()).(*End)
	if !ok || end == nil {
		return &UnsupportedError{Construct: "return template requires a terminal end node"}
	}
	if len(g.flowExits) > 0 {
		// 平台的答复端点只有一个出口通道（`terminatePlan` 二选一）：答复模板
		// 与具名变量出口**不能共存**（`docs/five-step/spec/W2-template-return.md`
		// §1.2）。混用 ⇒ 语言错（作者要改代码），不静默选一边。
		return &LanguageError{
			Code: diag.TEMPLATE_RETURN_MIXED,
			Message: "return template（答复模板）与 return <值>（具名变量出口）不能出现在同一流程：" +
				"平台的流程出口通道二选一（terminatePlan），两者混用没有可承载的产物形状",
		}
	}
	end.Answer = g.templateAnswer
	return nil
}

// buildTemplateAnswer 把一条 `return template` 语句解析成 End 的值侧身份：
// 逐字模板 + 有序名额表（名额名 → 生产者引用）。
func (g *Builder) buildTemplateAnswer(tr *ast.TemplateValue, streaming bool) (*EndAnswer, error) {
	bindings, err := g.resolveTemplateBindings(tr, "return template")
	if err != nil {
		return nil, err
	}
	return &EndAnswer{Template: tr.Text(), Bindings: bindings, Streaming: streaming}, nil
}

// resolveTemplateBindings 把一条 `using {…}` 的每个名额解析成**生产者引用**
// （值路径），三个位点共用同一判据、同一诊断码、同一实现：答复端点
// （`return template`）· 中间消息（`output template`，R-67 甲）· 表达式位
// （llm message content，R-68 甲）。三个位点各写一遍正是「同一规则两处实现」
// 的温床，故**一处定义**（where 只影响诊断文案，不影响判据）。
func (g *Builder) resolveTemplateBindings(tr *ast.TemplateValue, where string) ([]TemplateBinding, error) {
	var out []TemplateBinding
	for _, b := range tr.Bindings() {
		ref, err := g.resolveBindingPath(b.Value)
		if err != nil {
			return nil, &LanguageError{
				Code:    diag.TEMPLATE_BINDING_INVALID,
				Message: fmt.Sprintf("%s: 名额 %q 的值必须是值路径（IDENT(.IDENT)*）：%v", where, b.Name, err),
			}
		}
		out = append(out, TemplateBinding{Name: b.Name, Ref: ref})
	}
	return out, nil
}

// refString 渲染一个引用（诊断文案用，不参与判据）。
func refString(r Ref) string {
	return r.Producer + "." + r.Output
}

// resolveBindingPath resolves a value path expression (`ident(.ident)*`) to a
// producer ref via the same varToOutput resolution the return lowering uses.
func (g *Builder) resolveBindingPath(e ast.Expr) (Ref, error) {
	if e == nil {
		return Ref{}, fmt.Errorf("空值表达式")
	}
	ref, ok := g.resolveValueRef(e)
	if !ok {
		return Ref{}, fmt.Errorf("未解析的值路径（%s）", bindingPathString(e))
	}
	return Ref{Producer: ref.nodeID, Output: ref.output}, nil
}

// bindingPathString 渲染名额值的可读形态（诊断用；只认值路径链，其余给类型名）。
func bindingPathString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.IdentExpr:
		return v.Name()
	case *ast.FieldAccessExpr:
		return bindingPathString(v.Obj()) + "." + v.FieldName()
	}
	return fmt.Sprintf("%T", e)
}

// junctionTailRef reports the VALUE a junction (if / switch / QA / attempt)
// yields to its enclosing consumer — the function's synthesized tail End, a
// parent junction's candidate, or the loop output-source fallback.
//
// 零候选 junction（`Groups` 与 `Refs` 皆空：所有臂都不产出值）**没有值**——
// 「没有值」不是「零值的值」。给它挂 `{junction}.Group1` 会合成出一个值消费点
// （隐式 End 绑定 / 上层 junction 候选 / loop 输出源），于是 IR 优化阶段
// （optimizeGraph 的零候选 junction 删除，判据 = 有无真实数据消费者）看到
// 「被消费」而保留这具死脚手架：产物里出现一个 `mergeGroups=[{name:Group1,
// variables:[]}]` 的空聚合，反编译只能把它读成显式零值投影（`v1 = {"Group1": ""}`
// + 凭空 `type RT1` + `return v1.Group1`）⇒ `r0 ≍ c0` 被破坏，且该坏形状在
// 后续轮次自我稳定（不是发散，而是坏固定点）。
//
// Lifecycle of the two live forms:
//   - 具名组形态（`len(Groups) > 0`，逐变量/逐字段 phi）与 record plan 形态：
//     平台 merge 只声明各组成员端口、**不声明**合成 Group1 ⇒ 必须 nil
//     （否则悬空 ref，X16/X4/X5/R5 教训）；
//   - legacy 单值通道（`Groups == 0 && Refs > 0`）：平台 merge 声明 Group1，
//     该通道是真实存在的值 ⇒ 返回 `{junction, Group1}`。
//
// **管辖范围（第六次 S2-gate 收窄 2026-09-11；第七/八次 gate 二次收窄）**：
// 本 helper 只覆盖 **tailRef 通道**——四个宿主（if / switch / QA / attempt）共用
// 本判据 = 少一个例外。同一条普遍规则「零候选 junction（`Groups==0 && Refs==0`）
// 必须被当作『没有值』处理」另有若干条**登记通道**（下表），各自需要 S0-S6 +
// 独立 gate + 涉及值位语义者需用户可见批准；**不得**以「再加一个形状守卫」的方式
// 就地推广（那正是本族反复复发的机制）。
//
// **2026-09-23 一手复核（逐条复跑最小复现；旧表已漂，见 J 正本 §30.7）**：
//   - ① 本 helper（tailRef，四宿主）= 已收口；
//   - ② `planNames → varToOutput` 重定向（M2）= **已收口**（§18，2026-09-12）；
//     复核：裸 `var v;` 两臂赋值 → 产物 `mergeGroups=[{name:"v",2 候选}]` +
//     `end.node_inputs=[{path:"v",ref_node:<merge>}]`（不再有零候选/伪 Group1）；
//   - ③ loop 输出源回退（R13）= **今天不可达**：旧最小复现（臂内 `yield`）现在是
//     **语言错** `YIELD_CONTRACT`（语言已有 yield 位置规则）⇒ 旧登记口径过期；
//   - ⑤ void 调用在值位置（C6）= **今天 fail loud**：`ADAPTER_ERROR`
//     （能力输出表未声明 ⇒ 不知道值由哪些字段承载）⇒ 不再「编译成功但产物非法」；
//   - ④/⑥ 同表已收口（C5 = 族 P20；C7 = §17）；
//   - ⑦ 零候选 legacy 聚合的**类型**静默兜底（C8，`build_condition.go`）= 未收口
//     （推断型静默 fallback；未构造出改变产物的形状）；
//   - **N1（attempt flow-tail 判据分叉）= 本日收口**：渲染器不再手写
//     `len(Groups)==0`，改为消费 `Aggregate.TailValueRef()`（本判据的包外出口）；
//     包外重判 = 红，判据 = `internal/backend/cozebiz/junction_tail_value_single_source_contract_test.go`。
//
// 该通道表**目前没有「账本新鲜」的机器判据** —— 本日复核即发现它与事实已漂
// （②列为未收口而实际已收口、③/⑤ 的形态已分别变语言错 / 变 fail loud）。
// 登记为残余，不掩盖。
// **通道账的活落点 = `docs/five-step/spec/J-join-canonical-form.md` §30.7**（本规则有
// 哪些通道、各是什么状态；历史清单在遗产隔离面内 ⇒ 不可维护、不得引用）。
// `var v;` 是合法源码且是 raising 的合法出口 `hoistedVarDecl`，今日语料未命中 ≠ 不可达。
func junctionTailRef(agg *Aggregate) *varRef {
	// nil 契约（B2，2026-09-13）：`*Aggregate` 的 nil 是**合法状态**——
	// ①宿主的 exit/merge 汇聚被 IR 优化阶段判死删除后，宿主指针被置 nil；
	// ②`optimizeGraph` 是不动点循环，第二轮的宿主指针可能已被上一轮清空。
	// 「没有汇聚」=「没有尾值」，与零候选（`len(Groups)==0`）同侧返回 nil，
	// 判据下沉到此处，所有调用点（builder 四处 + 优化器普查一处）一致。
	if agg == nil {
		return nil
	}
	if len(agg.Groups) == 0 && len(agg.Refs) > 0 {
		// 口名 = 汇聚单元的**身份**（`Aggregate.SingleGroupName()`：有身份用身份名、
		// 无身份才回落平台无名组拼写 `Group1`）。此处曾硬编码字面量 `Group1`
		// （族 1 之前「legacy 单组口名恒为 Group1」的巧合）：族 1 把口名改成变量
		// 身份名之后，硬编码就与渲染器分叉 —— 产物里 merge 声明口 `v`、引用写
		// `Group1` ⇒ ARTIFACT_GATE 硬报错（`ref 100003.Group1 does not resolve:
		// node 100003 declares ports [v]`），合法源码 `if (c) { }  return v;`
		// （空臂）编译直接失败（2026-09-21 一手复现，见 §16.2 N1）。
		// 判据来源单一：口名的定义处只有 `SingleGroupName()` 一个。
		return &varRef{nodeID: agg.ID, output: agg.SingleGroupName()}
	}
	return nil
}

func (g *Builder) rootEndsWithOutput() bool {
	if g.root == nil || len(g.root.Stmts) == 0 {
		return false
	}
	ret, ok := g.root.Stmts[len(g.root.Stmts)-1].(*ReturnStmt)
	return ok && ret.End != nil && len(ret.End.Outputs) > 0
}

func (g *Builder) entryFunc() *ast.FunctionDef {
	// Entry is resolved by the frontend pipeline (program.Entry()).
	// Reaching the entry function is the builder's job; callers pass the
	// TypedBoundProgram whose Program carries the resolved entry.
	return g.entry
}

func (g *Builder) add(n Node) {
	g.nodes = append(g.nodes, n)
	g.treeNodes = append(g.treeNodes, n)
	g.remember(n)
}

// remember publishes a node into the global (scope-independent) node index.
// Structural ownership stays scoped; type knowledge does not (loop algorithm
// §3 invariant 2). Call it wherever a node becomes a permanent producer —
// `add` covers the tree/registry funnel, and the junction helpers that append
// straight to `g.nodes` call it directly.
func (g *Builder) remember(n Node) {
	if n == nil {
		return
	}
	g.allNodes = append(g.allNodes, n)
}

// findAny resolves a producer by id across the global index: the scoped
// registry first (structural owner), then every node ever published. Used for
// type resolution only — never as an ownership lookup.
func (g *Builder) findAny(id string) Node {
	if n := g.find(id); n != nil {
		return n
	}
	for _, n := range g.allNodes {
		if n.NodeID() == id {
			return n
		}
	}
	return nil
}

// recordPlacement logs one native-vs-materialize lowering decision
// (PLACEMENT-PASS §5): nodeID is the owning node ("" for decisions not
// bound to a node yet), construct names the family, decision is a
// Placement* constant, reason explains why (contract / capability /
// purity / no-native-node).
func (g *Builder) recordPlacement(nodeID, construct, decision, reason string) {
	g.placements = append(g.placements, Placement{
		NodeID:    nodeID,
		Construct: construct,
		Decision:  decision,
		Reason:    reason,
	})
}

func (g *Builder) find(id string) Node {
	for _, node := range g.nodes {
		if node.NodeID() == id {
			return node
		}
	}
	return nil
}

// ── structured tree building (D9) ────────────────────────────────────────

// curBlock returns the innermost open block.
func (g *Builder) curBlock() *Block {
	return g.blockStack[len(g.blockStack)-1]
}

// popBlock closes the current block scope, flushing any unconsumed nodes
// as trailing leaf statements.
func (g *Builder) popBlock() *Block {
	b := g.curBlock()
	g.flushTreeNodes()
	g.blockStack = g.blockStack[:len(g.blockStack)-1]
	return b
}

// appendStmt appends a structured statement to the current block.
func (g *Builder) appendStmt(s Stmt) {
	g.curBlock().Stmts = append(g.curBlock().Stmts, s)
}

// consumeTreeNodes removes builder-created nodes from the pending node
// queue; container statements own the nodes they reference (selector,
// merge, loop container, ends) so they are not re-wrapped as leaf stmts.
func (g *Builder) consumeTreeNodes(ids ...string) {
	skip := map[string]bool{}
	for _, id := range ids {
		skip[id] = true
	}
	keep := g.treeNodes[:0]
	for _, n := range g.treeNodes {
		if !skip[n.NodeID()] {
			keep = append(keep, n)
		}
	}
	g.treeNodes = keep
}

// flushTreeNodes wraps pending leaf nodes as NodeStmt in the current
// block. The build-time tail value is stamped on the node it points to so
// the structured tree preserves the value state machine Project replays.
func (g *Builder) flushTreeNodes() {
	if len(g.treeNodes) == 0 {
		return
	}
	var tailID string
	if g.tailRef != nil {
		tailID = g.tailRef.nodeID
	}
	for _, n := range g.treeNodes {
		var v *varRef
		if tailID != "" && n.NodeID() == tailID {
			v = g.tailRef
		}
		g.appendStmt(&NodeStmt{Node: n, Value: v})
	}
	g.treeNodes = nil
}

func (g *Builder) nextID() string {
	g.nextNodeID++
	id := fmt.Sprintf("%d", 100000+g.nextNodeID)
	g.allocatedIDs = append(g.allocatedIDs, id)
	return id
}

func (g *Builder) endID() string {
	// Coze convention: the End node id is 900001, distinct from the
	// 100001..899999 range used for Start/intermediate nodes.
	return "900001"
}

// emitStmts lowers a statement list, threading the flow junction.
func (g *Builder) emitStmts(stmts []ast.Stmt, prevIDs []string, firstPort string) ([]string, string, error) {
	current := prevIDs
	port := firstPort
	// block is this sequence's own tree block: the dead-statement test below
	// reads back the statements this loop just appended (the lowering's own
	// answer to "did every path end here?"), so the reachability rule is the
	// same one the projections cut with — not a second, parallel analysis.
	block := g.blockStack[len(g.blockStack)-1]
	dead := false
	for idx, stmt := range stmts {
		lastInBlock := idx == len(stmts)-1
		before := len(block.Stmts)
		if dead {
			g.unreachable++
		}
		// An else-less if defers its unmatched path: a following return IS
		// the else arm; any other statement falls through the selector's
		// false port.
		if g.pendingElse != nil {
			// 内联帧：以值出口结束的臂**不建汇聚单元** —— 值已在 emitReturn
			// 登记进帧出口表（InlineStmt.Exit 统一汇聚），本处只把未匹配路径
			// 交给选择器的 false 端口续接。与主流程 `thenFlowEnd` 早退是**同一
			// 读法**（尾 return 是唯一活路径 ⇒ 本 if 不另造 junction），差别只在
			// 值的汇聚点：主流程 = 流程出口 phi，内联帧 = 帧出口 phi。
			if g.inlineName != "" && g.pendingElse.thenProduced && g.pendingElse.thenRef != nil {
				// fall through to the common "drop" path below (the pending
				// else's value is not a junction candidate here).
			} else if rs, ok := stmt.(*ast.ReturnStmt); ok {
				last, err := g.resolvePendingElse(rs.Value())
				if err != nil {
					return nil, port, err
				}
				current = last
				port = ""
				if dead {
					g.unreachable--
				}
				dead = dead || anyStmtEndsFlow(block.Stmts[before:])
				continue
			}
			p := g.pendingElse
			g.pendingElse = nil
			seen := map[string]bool{p.selID: true}
			merged := []string{}
			for _, id := range p.thenIDs {
				if id == p.selID || seen[id] || g.isBreakish(id) {
					continue
				}
				seen[id] = true
				merged = append(merged, id)
			}
			current = append(merged, p.selID)
			port = "false"
		}
		var result []string
		var err error
		switch s := stmt.(type) {
		case *ast.VarDeclStmt:
			result, port, err = g.emitVarDecl(s, current, port)
		case *ast.IfStmt:
			result, port, err = g.emitIf(s, current, port, "")
		case *ast.ReturnStmt:
			result, port, err = g.emitReturn(s, current, port)
		case *ast.ParallelStmt:
			result, port, err = g.emitParallel(s, current, port)
		case *ast.AssignStmt:
			result, port, err = g.emitAssign(s, current, port)
		case *ast.OutputStmt:
			result, port, err = g.emitOutputStmt(s.Value(), s.Streaming(), current, port)
		case *ast.ExprStmt:
			result, port, err = g.emitExprStmt(s, current, port)
		case *ast.ForStmt:
			result, port, err = g.emitForStmt(s, current, port)
		case *ast.WhileStmt:
			result, port, err = g.emitWhileStmt(s, current, port)
		case *ast.BreakStmt:
			result, port, err = g.emitBreak(current, port)
		case *ast.ContinueStmt:
			result, port, err = g.emitContinue(current, port)
		case *ast.YieldStmt:
			result, port, err = g.emitYield(s, current, port)
		case *ast.RequestInputStmt:
			result, port, err = g.emitRequestInput(s, current, port)
		case *ast.SwitchStmt:
			result, port, err = g.emitSwitch(s, current, port)
		case *ast.AttemptStmt:
			result, port, err = g.emitAttempt(s, current, port, lastInBlock)
		default:
			return nil, port, &UnsupportedError{Construct: fmt.Sprintf("statement %T", stmt)}
		}
		if err != nil {
			return nil, port, err
		}
		g.flushTreeNodes()
		current = result
		if dead {
			g.unreachable--
		}
		dead = dead || anyStmtEndsFlow(block.Stmts[before:])
	}
	return current, port, nil
}

// emitExprStmt lowers a bare expression statement. A side-effecting tool
// call used as a statement (e.g. throw_error.raise({...})) materializes a
// ToolCall node even though its result is unused — dropping it would
// silently erase error-raising semantics. A call to a user-defined
// function is inlined: its body may contain tool calls / side effects
// (e.g. a for-loop body that calls a table-write helper), so dropping it
// would silently erase the helper's semantics from the graph. Other pure
// expressions are discarded.
func (g *Builder) emitExprStmt(s *ast.ExprStmt, prevIDs []string, firstPort string) ([]string, string, error) {
	if pe, ok := s.Expr().(*ast.OperationPolicyExpr); ok {
		// A policy-wrapped operation used as a STATEMENT (`op(...) with
		// retry/timeout/default;`) is a real side-effecting call: the raising
		// side deliberately produces this shape (unused_drop rewrites
		// `let v = op(...) with p;` into a bare ExprStmt carrying the policy),
		// so dropping it silently erases the node AND the author's policy.
		// The producer is shared with the value position — no second shape
		// enumeration lives here.
		id := g.nextID()
		if _, _, perr := g.materializePolicyOperation(id, pe, prevIDs, firstPort); perr != nil {
			return nil, firstPort, perr
		}
		// A statement yields no value for the enclosing branch: clearing the
		// tail keeps a stale struct handle out of an if/else aggregate.
		g.tailRef = nil
		return []string{id}, "", nil
	}
	if fc, ok := s.Expr().(*ast.ForeignCodeExpr); ok {
		// A Dify code node may be intentionally used only for its side effect
		// (for example, an iteration updates a conversation variable and its
		// collected output is not consumed). Preserve execution without
		// inventing an unused language binding.
		id := g.nextID()
		node, err := g.buildCode(id, fc)
		if err != nil {
			return nil, firstPort, err
		}
		g.add(node)
		g.flushTreeNodes()
		g.tailRef = nil
		return []string{id}, "", nil
	}
	call, ok := s.Expr().(*ast.CallExpr)
	if !ok {
		// A non-call statement root — array / record literal, template string,
		// binary or unary expression — may still WRAP a node-producing
		// operation (`[op(...)];`, `` `${op(...)}`; ``, `op(...) == "";`).
		// Evaluate the expression for its effects, then drop the value.
		return g.discardExprEffects(s.Expr(), prevIDs, firstPort)
	}
	name := calleeName(call.Func())
	// Bare operation calls (llm/http/knowledge/split/extract) are real
	// side-effecting calls — they invoke an external model / HTTP endpoint /
	// knowledge retrieval / text transform. A bare statement form must still
	// materialize the node: dropping it would silently erase the node and its
	// edge from the graph (coze-biz real-export edge equivalence: X78 bare
	// llm(...), X76 bare split(...)). Same branch shape as the qualified tool
	// call below: a statement yields no branch value (junction = the node id,
	// port = ""), and the branch tail is cleared so no stale struct handle
	// lands in an if/else aggregate.
	switch name {
	case "llm", "http", "knowledge", "split", "extract":
		junction, _, merr := g.materializeCall(call, prevIDs, firstPort)
		if merr != nil {
			return nil, firstPort, merr
		}
		g.tailRef = nil
		return junction, "", nil
	}
	// Side-effecting capability calls: the qualified namespace form
	// p.op({...}) (TOOL-DIRECT-CALL-FORM). A bare qualified call must
	// materialize its ToolCall (tool calls can raise errors);
	// isQualifiedToolCall gates so x.contains(...) / record-field calls
	// never reach buildTool.
	// A bare statement yields no VALUE for the enclosing branch, even when
	// it materializes a side-effecting node: the branch tail stays at the
	// last value-producing statement, and a stale struct handle
	// ({plugin, "output"}) would land in the if/else aggregate as a ref to
	// a port the plugin does not declare (引用变量不存在). Clearing the
	// tail here keeps statement-only branch tails void.
	if g.isQualifiedToolCall(call) {
		id := g.nextID()
		tool, terr := g.buildTool(id, call)
		if terr != nil {
			return nil, firstPort, terr
		}
		g.add(tool)
		g.tailRef = nil
		return []string{id}, "", nil
	}
	if fn, ok := g.funcs[name]; ok {
		junction, retPort, ref, ierr := g.inlineCall(fn, call.Args(), prevIDs, firstPort)
		if ierr != nil {
			return nil, firstPort, ierr
		}
		// A void helper (no return) produces no branch value either.
		if ref.nodeID == "" {
			g.tailRef = nil
		}
		return junction, retPort, nil
	}
	// The call root did not materialize anything (a declarative primitive such
	// as json.stringify / json.parse, or an unknown callee). Its ARGUMENTS may
	// still wrap operations — same rule, same helper.
	return g.discardExprEffects(s.Expr(), prevIDs, firstPort)
}

// discardExprEffects evaluates an expression used as a statement for its
// effects and drops its value: every node-producing operation nested in the
// expression (builtin / qualified tool call, policy-wrapped operation, user
// function inline) is hoisted into a producer node by the shared
// materialization entry, and the residual pure value is discarded. A statement
// yields no value for the enclosing branch, so the tail is cleared and the
// port reset.
//
// Pure statements (`1 + 2;`, `json.parse<object>(x);`) hoist nothing and keep
// the pre-existing behavior exactly: no node, no junction change — an unused
// pure value must not become a dead shim (the IR optimizer only removes dead
// aggregates, not dead shims).
func (g *Builder) discardExprEffects(expr ast.Expr, prevIDs []string, firstPort string) ([]string, string, error) {
	pending := len(g.treeNodes)
	if _, err := g.hoistUserCalls(expr, prevIDs, firstPort); err != nil {
		return nil, firstPort, err
	}
	if len(g.treeNodes) == pending {
		return prevIDs, firstPort, nil
	}
	ids := make([]string, 0, len(g.treeNodes)-pending)
	for _, n := range g.treeNodes[pending:] {
		ids = append(ids, n.NodeID())
	}
	g.tailRef = nil
	return ids, "", nil
}

// isBreakish reports whether the node id belongs to a Break/Continue node.
func (g *Builder) isBreakish(id string) bool {
	n := g.find(id)
	switch n.(type) {
	case *BreakNode, *ContinueNode:
		return true
	}
	return false
}

// cloneRefs copies a var-ref table (branch scoping: ana2-style snapshot).
func cloneRefs(m map[string]varRef) map[string]varRef {
	out := make(map[string]varRef, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ── expression helpers (migrated from the Coze generator) ────────────────

// typeName maps ncoda primitive type names onto neutral type kind strings
// (mirrors the Coze canvas type ids).
func typeName(t ast.TypeRef) string {
	if p, ok := t.(*ast.PrimTypeRef); ok {
		switch p.Name() {
		case "int", "integer":
			return "integer"
		case "float", "number":
			return "number"
		case "bool", "boolean":
			return "boolean"
		case "list", "array":
			return "list"
		case "object", "map":
			return "object"
		}
	}
	return "string"
}

// typeOf maps a TypeRef onto a neutral Type for output schemas. Records
// become objects; **enums become `string`** —— 枚举在平台上的载体就是成员串
// （规范形正本 = docs/five-step/spec/type-identity-canonical-form.md §4；R-96
// 收口）：先前把枚举映成 `TObject{value: string}` 只是为了给 `t.value` 找一个
// 可落的字段口，代价是**声明与值不同形**（口声明 object{value}、shim body 只
// 产出一个裸串）⇒ 展平口 `value` 无生产者（运行期静默丢值）、反编译按声明登记
// 出无人引用的类型声明（I1 破裂）。`.value` 投影的真身是**恒等**
// （`resolveValueRef` 的枚举投影分支），不需要那个虚构的记录。
func (g *Builder) typeOf(t ast.TypeRef) *Type {
	switch tr := t.(type) {
	case *ast.ArrayTypeRef:
		return TList(g.typeOf(tr.Element()))
	case *ast.FileTypeRef:
		c := tr.Constraints()
		return &Type{
			Kind:                     "file",
			AllowedFileTypes:         append([]string(nil), c.Types...),
			AllowedFileExtensions:    append([]string(nil), c.Extensions...),
			AllowedFileUploadMethods: append([]string(nil), c.UploadMethods...),
		}
	case *ast.MapTypeRef:
		// Dify and the neutral IR represent maps as object values. Map keys are
		// not expressible in Dify output schemas, so retain the value shape.
		return TObject(map[string]*Type{})
	case *ast.OptionalTypeRef:
		inner := g.typeOf(tr.Value())
		if inner == nil {
			return nil
		}
		inner.Optional = true
		return inner
	case *ast.PrimTypeRef:
		switch tr.Name() {
		case "int", "integer":
			return T("integer")
		case "float", "number":
			return T("number")
		case "bool", "boolean":
			return T("boolean")
		case "list", "array":
			return TList(T("string"))
		case "object", "map":
			return TObject(map[string]*Type{})
		case "file":
			return T("file")
		}
		return T("string")
	case *ast.NamedTypeRef:
		if _, ok := g.enums[tr.Name()]; ok {
			return T("string")
		}
		if td, ok := g.types[tr.Name()]; ok {
			props := map[string]*Type{}
			for _, f := range td.Fields() {
				props[f.Name()] = g.typeOf(f.TypeRef())
			}
			return TObject(props)
		}
	}
	return T("string")
}

// llmSchemaOf resolves a source type reference into the lossless semantic
// schema used by LLMCall.Result. It intentionally bypasses neutral wire Type,
// whose object shape cannot retain map keys, optionality, enum values, or
// record requiredness.
func (g *Builder) llmSchemaOf(t ast.TypeRef) (*llm.LLMSchema, error) {
	switch tr := t.(type) {
	case *ast.PrimTypeRef:
		switch tr.Name() {
		case "string":
			return llm.String(), nil
		case "bool", "boolean":
			return llm.Bool(), nil
		case "int", "integer":
			return llm.Int64(), nil
		case "float", "number":
			return llm.Float64(), nil
		case "object":
			// open object: runtime verifies it is a JSON object; keys/values
			// not statically committed (ideas-todo/LLM-OPEN-OBJECT.md).
			return llm.Object(), nil
		default:
			return nil, fmt.Errorf("llm payload type %q is not admitted", tr.Name())
		}
	case *ast.ArrayTypeRef:
		e, err := g.llmSchemaOf(tr.Element())
		if err != nil {
			return nil, err
		}
		return llm.Array(e), nil
	case *ast.MapTypeRef:
		key, ok := tr.Key().(*ast.PrimTypeRef)
		if !ok || key.Name() != "string" {
			return nil, fmt.Errorf("llm map keys must be string")
		}
		v, err := g.llmSchemaOf(tr.Value())
		if err != nil {
			return nil, err
		}
		return llm.MapString(v), nil
	case *ast.OptionalTypeRef:
		v, err := g.llmSchemaOf(tr.Value())
		if err != nil {
			return nil, err
		}
		return llm.Optional(v), nil
	case *ast.NamedTypeRef:
		if e, ok := g.enums[tr.Name()]; ok {
			values := make([]string, 0, len(e.Members()))
			for _, m := range e.Members() {
				values = append(values, m.Name())
			}
			return llm.Enum(e.Name(), values), nil
		}
		if td, ok := g.types[tr.Name()]; ok {
			fields := make([]llm.LLMField, 0, len(td.Fields()))
			for _, f := range td.Fields() {
				s, err := g.llmSchemaOf(f.TypeRef())
				if err != nil {
					return nil, fmt.Errorf("field %q: %w", f.Name(), err)
				}
				fields = append(fields, llm.LLMField{Name: f.Name(), Required: !isOptionalRef(f.TypeRef()), Schema: s})
			}
			return llm.Record(td.Name(), fields), nil
		}
		return nil, fmt.Errorf("llm payload type %q is unresolved", tr.Name())
	default:
		return nil, fmt.Errorf("llm payload type is not admitted")
	}
}

func isOptionalRef(t ast.TypeRef) bool { _, ok := t.(*ast.OptionalTypeRef); return ok }

// constantSlotString resolves a **constant config slot** — a call position
// whose platform field carries configuration (an enum, a model name, a list of
// dataset ids), not a template — to its compile-time string.
//
// Accepted: a literal or a resolved `const`. Anything else is a compile error:
// the platform field has no runtime carrier, so taking the expression would
// mean either inventing a platform default (the author's value silently
// disappears from the product — the pre-2026-09-24 behaviour of `llm(m, {...})`,
// which emitted modelType "1" + modelName "" with zero diagnostics) or
// emitting an unimportable artifact.
//
// Slots on this ruler: http() method, llm() model, extract() model (also
// pinned one pass earlier by target_validation "Extraction model must be a
// static string"), knowledge() dataset ids, std.v1.rag_answer /
// std.v1.fetch_and_summarize model + dataset positions.
func (g *Builder) constantSlotString(slot string, expr ast.Expr) (string, error) {
	if s, ok := stringValue(expr); ok {
		return s, nil
	}
	if id, ok := expr.(*ast.IdentExpr); ok {
		if s, ok := g.consts[id.Name()]; ok {
			return s, nil
		}
	}
	return "", &UnsupportedError{Construct: fmt.Sprintf(
		"%s %s is not a compile-time string (the platform field on this slot is configuration, not a template; pass a literal, or a `const`)",
		slot, exprLabel(expr))}
}

// staticString resolves the configuration-time string subset accepted by
// target-native text nodes: a literal or a string const. Runtime values are
// deliberately excluded because the platform parameter is not dynamic.
func (g *Builder) staticString(expr ast.Expr) (string, bool) {
	if lit, ok := expr.(*ast.StringLit); ok {
		return lit.Value(), true
	}
	if id, ok := expr.(*ast.IdentExpr); ok {
		s, ok := g.consts[id.Name()]
		return s, ok
	}
	return "", false
}

// staticSeparators resolves the split SEPARATOR slot: either one compile-time
// string (`","` / a resolved const) or a compile-time list of strings
// (`[",", "；"]`). The platform field (`splitParams.delimiters`) is itself a
// list and its **order is semantic** (the runtime splits sequentially: split by
// d₁, then split every piece by d₂, then flatten), so the list spelling is the
// canonical carrier for the multi-delimiter case; a single delimiter keeps the
// scalar spelling because both spellings land on the same one-element wire.
// Anything not statically known is rejected so the caller can keep the exact
// generic shim instead of silently collapsing to one delimiter.
func (g *Builder) staticSeparators(expr ast.Expr) ([]string, bool) {
	if lit, ok := expr.(*ast.ArrayLit); ok {
		if len(lit.Elements()) == 0 {
			return nil, false
		}
		out := make([]string, 0, len(lit.Elements()))
		for _, el := range lit.Elements() {
			s, ok := g.staticString(el)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	s, ok := g.staticString(expr)
	if !ok {
		return nil, false
	}
	return []string{s}, true
}

// staticStringArray resolves a **literal array of compile-time strings**
// (`["a", "b"]`) — the first argument of `join(list, sep)` when the list is
// spelled inline. Returns false for anything that is not an array literal
// (a value ref stays a ref; a scalar is not an array). The platform's
// text-concat node carries such a literal list directly in node_inputs
// (no producer), so lowering to a native node must not invent one.
func (g *Builder) staticStringArray(expr ast.Expr) ([]any, bool) {
	lit, ok := expr.(*ast.ArrayLit)
	if !ok {
		return nil, false
	}
	out := make([]any, 0, len(lit.Elements()))
	for _, el := range lit.Elements() {
		s, ok := g.staticString(el)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// resolveHTTPMethod resolves the http() METHOD slot. The platform's method
// field is a scalar, not a template (coze-studio's parser only scans url /
// body for placeholders and its build validator requires a non-empty method),
// so the slot accepts only compile-time strings — a literal or a resolved
// const. Anything else is a compile error: defaulting to "GET" turned a
// runtime method into a silently wrong request.
func (g *Builder) resolveHTTPMethod(call *ast.CallExpr) (string, error) {
	if len(call.Args()) == 0 {
		return "GET", nil
	}
	return g.constantSlotString("http() method", call.Args()[0])
}

// resolveHTTPTemplate renders a configuration-time TEMPLATE slot (the http url
// argument, fetch_and_summarize's url) into the platform template text.
//
// The slot accepts any string expression the language admits — the language
// types it as `string`, and every target renders it as a template (coze-biz /
// coze-studio carry the refs as {{block_output_<id>.<port>}} placeholders,
// dify rewrites them to {{#node.port#}}, runtime renders the same template):
//
//   - literal / const → verbatim text;
//   - value reference → {{block_output_<id>.<port>}};
//   - template string  → literal parts plus interpolated refs;
//   - `+` chain        → concatenated template;
//   - anything else (a nested call, a ternary, an index) → hoisted into a
//     producer node first, then referenced.
//
// A shape that still cannot be rendered is a compile error: the slot must
// never silently degrade. Before this rule the whole argument was dropped
// (url: "") and, with it, every operation nested inside it.
func (g *Builder) resolveHTTPTemplate(expr ast.Expr, prevIDs []string, firstPort, slot string) (string, error) {
	if expr == nil {
		return "", nil
	}
	hoisted, err := g.hoistUserCalls(expr, prevIDs, firstPort)
	if err != nil {
		return "", err
	}
	if text, terr := g.templateExpr(hoisted, prevIDs, firstPort); terr == nil {
		return text, nil
	}
	// The template entry does not know this shape (declarative primitive call
	// root, unsupported operator): materialize a producer and reference its
	// single output — the same "produce then reference" rule the template
	// interpolations themselves use.
	ref, merr := g.emitValueExpr(prevIDs, firstPort, false, hoisted)
	if merr != nil {
		return "", &UnsupportedError{Construct: fmt.Sprintf(
			"http() %s argument %s is not a usable string value (assign it to a variable first, or wrap it in a `foreign code` block)",
			slot, exprLabel(expr))}
	}
	return fmt.Sprintf("{{block_output_%s.%s}}", ref.nodeID, ref.output), nil
}

// exprLabel names an expression shape in the user's terms — diagnostics must
// be navigable, never a Go type name (§5.1).
func exprLabel(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.CallExpr:
		return fmt.Sprintf("call to %q", calleeName(e.Func()))
	case *ast.IdentExpr:
		return fmt.Sprintf("variable %q", e.Name())
	case *ast.BinaryExpr:
		return fmt.Sprintf("%q expression", e.Op())
	case *ast.TernaryExpr:
		return "conditional expression"
	case *ast.LambdaExpr:
		return "lambda"
	case *ast.StringLit:
		return "string literal"
	case *ast.ArrayLit:
		return "array literal"
	case *ast.MapLit:
		return "map literal"
	}
	return "expression"
}

// literalValue extracts a literal's native value (config surface payload).
func literalValue(expr ast.Expr) any {
	switch lit := expr.(type) {
	case *ast.StringLit:
		return lit.Value()
	case *ast.IntLit:
		return lit.Value()
	case *ast.FloatLit:
		return lit.Value()
	case *ast.BoolLit:
		return lit.Value()
	case *ast.UnaryExpr:
		// -N / -1.5: signed numeric literals are a common parameter form
		// (real exports: X97 transform_x/transform_y carry negative
		// offsets); without this, the parameter was silently dropped.
		if lit.Op() != "-" {
			return nil
		}
		switch o := lit.Operand().(type) {
		case *ast.IntLit:
			return -o.Value()
		case *ast.FloatLit:
			return -o.Value()
		}
	}
	return nil
}

// entryParamDefault 提取入口参数默认值的**平台承载值**（回写 start 声明的
// `default_value`）。
//
// 与 literalValue 只差两处，且都只在**值位**（会被回写平台）成立：
//   - `file(<静态字面量>)`：file 无字面量（file-family-canonical-form §①），
//     反编译侧 file 值一律走标注 cast（语料 298 处 `file(""`）⇒ 这里必须解包成
//     底层字符串，否则 `file<audio> bgm = file("https://…")` 的默认值在产物里
//     消失（r1 无值 ⇒ c1 ≠ c0，非不动点）；
//   - 数组字面量：`array<file<image>>` 的默认值是 `[file(…), …]`，逐元素同解。
//
// 任一元素取不到字面量 ⇒ 整表不作承载（不发明值；同 L28「值不可表达」姿态）。
//
// 为什么不直接放宽 literalValue：它有 12 处消费者（工具参数 / 条件 / 语句值位），
// 放宽会连带改那些位点的行为；入口默认值这一位点的语义由本函数独占。
func entryParamDefault(expr ast.Expr) any {
	switch d := expr.(type) {
	case *ast.CallExpr:
		if calleeName(d.Func()) == "file" && len(d.Args()) == 1 {
			return entryParamDefault(d.Args()[0])
		}
	case *ast.ArrayLit:
		elems := d.Elements()
		out := make([]any, 0, len(elems))
		for _, e := range elems {
			v := entryParamDefault(e)
			if v == nil {
				return nil
			}
			out = append(out, v)
		}
		return out
	}
	return literalValue(expr)
}

// stringValue extracts a string literal value from an expression.
// staticMapKeyName returns the **static key name** of a map entry: an
// identifier's name or a string literal's value (the single rule,
// ast.StaticMapKey). The bool is false for a dynamic key expression.
//
// Why this exists next to stringValue (D-2, 2026-09-14): stringValue is
// value-oriented — it only accepts literals — so using it on a map *key*
// silently drops `{ lyric: l }`, the key form the standard examples use
// (spec/examples/nodecoda/64_subflow_decl.ncoda:17). Two hosts were losing
// arguments that way (subflow call bindings, tool parameters), and two more
// were losing *string* keys because they hand-rolled an IdentExpr-only test.
// Every map-key read in the IR builder must go through this function; a
// value-position read still uses stringValue.
func staticMapKeyName(expr ast.Expr) (string, bool) { return ast.StaticMapKey(expr) }

// dynamicMapKeyError is the fail-loud diagnostic for a position that needs a
// static platform parameter name but was given a dynamic key expression
// (e.g. `{ f(x): v }`). Silently skipping such an entry drops user data —
// the defect class this family removes; the platform wire has no dynamic keys.
func dynamicMapKeyError(where string, key ast.Expr) error {
	return &UnsupportedError{Construct: fmt.Sprintf(
		"%s: map key %T is not a static name (use an identifier or a string literal — the platform parameter name is static)", where, key)}
}

func stringValue(expr ast.Expr) (string, bool) {
	switch lit := expr.(type) {
	case *ast.StringLit:
		return lit.Value(), true
	case *ast.IntLit:
		return strconv.FormatInt(lit.Value(), 10), true
	case *ast.FloatLit:
		return strconv.FormatFloat(lit.Value(), 'f', -1, 64), true
	case *ast.BoolLit:
		if lit.Value() {
			return "true", true
		}
		return "false", true
	}
	return "", false
}

// exprIsEnumTyped 判定一个表达式的**类型结论**是不是一个枚举。类型层为每个 Expr
// 发布结论（pkg/passes/type_pass.go::visit ⇒ ctx.ExprTypes），枚举变量的
// IdentExpr 因此也带 Enum 描述符 ⇒ 这是「这个值是枚举」的**唯一**权威判据（不从
// 拼写 / 节点形状猜）。判定用类型层**已有的**普遍规则 `types.IsWellFormedEnum`
// （pkg/types/rules.go 一处定义），不在调用点重抄一遍枚举的构成条件。
func (g *Builder) exprIsEnumTyped(expr ast.Expr) bool {
	if g.prog == nil || g.prog.Snapshot == nil {
		return false
	}
	id, ok := g.prog.Snapshot.NodeIDOf(expr)
	if !ok {
		return false
	}
	return types.IsWellFormedEnum(g.prog.NodeTypes[id])
}

// enumMemberValue resolves the canonical wire value of an enum-member
// expression. The type pass publishes the fact on `Color.red`; an explicit
// `.value` projection therefore consults its object expression.
func (g *Builder) enumMemberValue(expr ast.Expr) (string, bool) {
	if g.prog == nil || g.prog.Snapshot == nil {
		return "", false
	}
	lookup := func(node ast.Node) (string, bool) {
		id, ok := g.prog.Snapshot.NodeIDOf(node)
		if !ok {
			return "", false
		}
		fact, ok := g.prog.EnumMembers[id]
		if !ok {
			return "", false
		}
		return fact.WireValue, true
	}
	if value, ok := lookup(expr); ok {
		return value, true
	}
	if field, ok := expr.(*ast.FieldAccessExpr); ok && field.FieldName() == "value" {
		return lookup(field.Obj())
	}
	return "", false
}

// literalString renders any scalar literal to its string form.
func literalString(expr ast.Expr) string {
	switch lit := expr.(type) {
	case *ast.StringLit:
		return lit.Value()
	case *ast.IntLit:
		return strconv.FormatInt(lit.Value(), 10)
	case *ast.FloatLit:
		return strconv.FormatFloat(lit.Value(), 'f', -1, 64)
	case *ast.BoolLit:
		if lit.Value() {
			return "true"
		}
		return "false"
	}
	return ""
}

// calleeName returns the callee identifier of a call expression.
func calleeName(expr ast.Expr) string {
	if id, ok := expr.(*ast.IdentExpr); ok {
		return id.Name()
	}
	if fa, ok := expr.(*ast.FieldAccessExpr); ok {
		base := calleeName(fa.Obj())
		if base == "" {
			return ""
		}
		return base + "." + fa.FieldName()
	}
	return ""
}

// resolveValueRef resolves an expression to a (nodeID, output) pair.
// Supported: identifier, and field access (obj.field) where the field is
// mapped to the node output.
//
// 它是 Builder 的方法（而非自由函数）：成员链的首段要按**产出方声明的端口
// 模型**判「口名 vs 口下方的子路径」，而那需要查图（effectivePortType /
// basePortIsRecordContainer）。绑定表恒为当前作用域的 g.varToOutput。
func (g *Builder) resolveValueRef(expr ast.Expr) (varRef, bool) {
	vars := g.varToOutput
	switch e := expr.(type) {
	case *ast.IdentExpr:
		ref, ok := vars[e.Name()]
		return ref, ok
	case *ast.CallExpr:
		// file(x) — string→file 显式标注 cast: 零节点透传, 解包为 x 的
		// ref（coze file 运行时就是 URL 字符串）。任何走 resolveValueRef
		// 的值位置（let / return / 模板插值 / map 值 / 工具参数 / 拼接 /
		// output）自动透明, 不物化节点。非 file 调用返回 false 走原路径。
		if name := calleeName(e.Func()); name == "file" && len(e.Args()) == 1 {
			return g.resolveValueRef(e.Args()[0])
		}
	case *ast.FieldAccessExpr, *ast.IndexExpr:
		// 枚举值的 `.value` 投影 = **恒等**（R-96）。两句判据各自一处定义：
		//   · 类型层：`.value` 是枚举上**唯一**可用的成员，类型 = string
		//     （pkg/passes/type_infer.go 的 `case types.Enum`）；
		//   · 平台层：枚举的载体**就是**那个成员串（`typeOf` / 语义层映射，
		//     规范形 = docs/five-step/spec/type-identity-canonical-form.md §4）。
		// ⇒ 投影读的是**同一个值** ⇒ 恒等，不物化任何节点、不发明字段口。
		// 反例（修前形态）：把枚举映成 `TObject{value: string}` 载体 ⇒ 口声明
		// object{value} 而 shim body 只产出裸串 ⇒ 展平口 `value` 无生产者
		// （运行期静默丢值）+ 反编译按声明登记出无人引用的类型声明（I1 破裂，
		// 一手：生成器形状 `type_decl` 12/12 源 r1≠r2）。
		if fa, ok := expr.(*ast.FieldAccessExpr); ok && fa.FieldName() == "value" && g.exprIsEnumTyped(fa.Obj()) {
			return g.resolveValueRef(fa.Obj())
		}
		// Collect the full access chain (obj.a.b → fields [a,b], with
		// constant-0 IndexExpr segments, doc_results[0].url) so nested
		// member paths resolve onto a dotted output port ("a.b"); the Coze
		// parser splits ref names on "." into from_path for object members.
		// Constant-0 IndexExpr segments denote element access on a record
		// array (E5-b): backends render it as item-0 member access, so the
		// index is dropped from the output path. Only constant 0 is
		// accepted; non-zero indices are rejected at the type layer
		// (INDEX_TYPE) and stay unresolved here. §5.3 widened this to every
		// materialized kind (knowledge(...).result[0].output).
		//
		// A ROOT IndexExpr with no member fields (scalar arrays — r[0],
		// split(...)[0]) has no dotted-port form on the platform: it stays
		// unresolved so the value materializes as a Python index shim
		// (renderConditionPython IndexExpr branch) instead of an error.
		fields := []string{}
		base := ast.Expr(e)
		for {
			switch b := base.(type) {
			case *ast.FieldAccessExpr:
				fields = append([]string{b.FieldName()}, fields...)
				base = b.Obj()
			case *ast.IndexExpr:
				switch idx := b.Index().(type) {
				case *ast.IntLit:
					if idx.Value() != 0 {
						return varRef{}, false
					}
					// Constant-0 element access on record arrays (E5-b):
					// index dropped from the output path.
					base = b.Obj()
				case *ast.StringLit:
					// 字符串下标 = 成员字段访问（反编译器对关键字字段
					// code/msg/output 等渲染 obj["field"]，与 .field 等价；
					// 不解此等价，关键字字段访问会退化成 Python shim）。
					fields = append([]string{idx.Value()}, fields...)
					base = b.Obj()
				default:
					return varRef{}, false
				}
			default:
				goto chainDone
			}
		}
	chainDone:
		if id, ok := base.(*ast.IdentExpr); ok {
			// 折成叶单元的记录绑定（J §42.3 A 轴）：它的整值语义**不可观察**（P1）
			// ⇒ 成员路径没有可落的字段口，成员读 = 读单元本身。
			if _, folded := g.foldedRecordFields[id.Name()]; folded {
				if ref, ok := vars[id.Name()]; ok {
					return ref, true
				}
			}
			if ref, ok := vars[id.Name()]; ok {
				if ref.kind == "tool" {
					// plugin/tool 输出字段名 = 平台物理名（$ 转义词法已剥，IR/类型/
					// 平台见原始键名；转换契约 = type ↔ $type，见
					// docs/ideas-todo/2026-09-09-cleanup-drop-semantic-renames-type_.md）。
					// 无任何语义→物理逆变：type_ 不是 type，字段名原样进入 ref path。
				}
				// G12-2 曾在此**按生产者身份**（json.parse<array<T>> 载体）返回
				// false：一切元素访问（含常量 0）都物化成 Python 守卫 shim。那是
				// 与 E5-b 并存的**第二套写法** —— 同一个 `items[0].field` 的落点
				// 由「基段是不是 json.parse」决定，而不是由「平台装不装得下」决定：
				// 反编译把 shim 折成 `X.$output[0].field`（A 轴规范形）后，同一
				// 语义重编译落到 dotted 分档 ⇒ 一轮漂移（X23 一手：c0 code×19 /
				// c1 code×18；最小夹具 code×2 / code×1）。
				// 规范形（一处定义，判据 = 平台承载面）：**常量 0 + 成员字段**的
				// 元素读 = dotted 端口路径（`output.field`，索引不进 path），与
				// 非 list 基段同一条 E5-b 规则；平台侧承载面一手 = 672 份真实导出
				// 里 129 处「基段声明为 list、路径继续带成员」的引用（其中 34 处
				// 指向 code 节点，且消费者按成员类型 `integer` 接收）。shim 只在
				// 平台装不下时保留：根索引 `items[0]`（无 dotted 形）、`i≠0`、
				// 非常量索引 —— 由下方 len(fields)==0 与合链处的常量 0 判定兜住。
				if len(fields) == 0 {
					// pure array element access: no member path → let the
					// Python materializer handle it.
					return varRef{}, false
				}
				if ref.ports != nil {
					// 值循环聚合变量成员链：组合完整 dotted path，永不吞段
					// （loopAggOutput；含多口 selector 判定）。yield 字面量
					// 端口 schema 已在同族修复（bind typed result port），
					// selector 修正不再 unmask string↔list<string> 翻转。
					out := ref
					out.output = loopAggOutput(ref, fields)
					return out, true
				}
				// 成员链首段是**口名**，还是**口下方的子路径**？产出方是 code
				// 节点（合成 shim / 用户 foreign code / json 系列）时，判据
				// 只有一条 —— 产出方声明的端口模型（codeMemberPort），与出口
				// 汇聚通路 aggregateFieldPort **共用同一把尺子**。其余 kinds
				// 沿用各自的语义→物理映射表（llm 信封 / http 结果口 / 工具
				// 注册表…），那些映射由注册表声明，不在此处猜。
				// 首段的**唯一规则表**按 kind 分派（两处实现必然漂移，
				// 2026-09-24 S74 一手）：
				//   · llm —— 基段是语义口（信封口 `response` / payload 口
				//     `text`）时，payload 的字段活在口**下方** ⇒ 与出口汇聚
				//     通路 `aggregateFieldPort` **同一把尺子**（那里已是唯一
				//     实现）。裸用 `fieldPort` 会把 payload 字段读成**裸字段口**
				//     ⇒ 后端投影加不上物理前缀 `value.` ⇒ 产物引用悬空：
				//     `ref 100004.content does not resolve: node 100004 declares
				//     ports [value]`（S74 一手：`v = v_src.text;` + `v.content`，
				//     记录单元在臂内逃逸时的成员读）。
				//   · code —— 端口模型由 `codeMemberPort` 判（同族既有收口）。
				first := fieldPort(ref, fields[0])
				outKind := ref.kind
				switch ref.kind {
				case "llm":
					first = g.aggregateFieldPort(ref, fields[0])
				case "extract":
					// `extract<T>` 调用值（Extraction[T]）的成员选择器：消费载荷口名
					// `value` ⇒ 结果 ref 是**载荷记录 T**，kind 随之切换 —— 否则
					// `let p = e.value;` 与调用值 `e` 同形（编码不单射），`p.city`
					// 会退化成裸字段口 ⇒ 产物引用悬空（见 extractPayloadKind 头注）。
					// 其余信封字段（ok / reason / usage）沿用 fieldPort 恒等。
					if fields[0] == ExtractValuePort {
						outKind = extractPayloadKind
					}
				case extractPayloadKind:
					// 载荷记录的成员活在载荷口下方 ⇒ 与 llm 的 `text.<field>` 同尺下钻。
					first = g.aggregateFieldPort(ref, fields[0])
				case "code":
					port, ok := g.codeMemberPort(ref, fields[0])
					if !ok {
						return varRef{}, false
					}
					first = port
				}
				output := first
				if len(fields) > 1 {
					output = first + "." + strings.Join(fields[1:], ".")
				}
				return varRef{nodeID: ref.nodeID, output: output, kind: outKind, typ: ref.typ}, true
			}
		}
	}
	return varRef{}, false
}

// loopAggOutput composes the full dotted producer path for a member chain on
// a VALUE-loop aggregate variable (let a = for/parallel for {...} { yield ... }).
// The aggregate variable is bound to the loop's collected output port(s):
//   - single declared port P: every member segment is an element-field
//     projection onto P (a.data.link → P.data.link);
//   - multiple declared ports: the first segment is a PORT selector when it
//     names a declared port (a.duration_list → duration_list; the raised
//     ncoda form keeps non-"output" port names as the leading member),
//     otherwise the chain projects onto the aggregate's default port.
//
// Raising mirrors this 1:1 (convert.go loop-aggregate raise: single-port
// loops drop the port name, multi-port loops keep non-"output" port names),
// so compile must NEVER drop a segment — the old fieldPort fallback ate
// fields[0] and each compile round truncated one path segment (family C:
// S13yangsheng agg_2.link → agg_2). Multi-port selector activation required
// the family-D yield-literal port-schema fix (X212/S2 caption_list flapping
// string vs list<string> once the correct port was bound).
func loopAggOutput(ref varRef, fields []string) string {
	base := ref.output
	rest := fields
	if len(ref.ports) > 1 {
		for _, p := range ref.ports {
			if p == fields[0] {
				base = p
				rest = fields[1:]
				break
			}
		}
	}
	if len(rest) == 0 {
		return base
	}
	return base + "." + strings.Join(rest, ".")
}

// IRPortVocabularyTarget 声明 **IR 的端口词汇表取自哪个目标的契约**。
//
// 历史事实：IR 的端口词汇表一直是 coze-biz 的（本函数此前把 coze-biz 的物理口名
// 直接写死在此，dify 后端再把 IR 口名映射回自己的口名）。本常量把那件事实显式化，
// 不新增语义：语言字段 → IR 口名走本目标的契约；**IR 口名 → 某平台的口名**由该
// 平台后端用同一份契约完成（经语言字段中转，见 builtin.LangOf/PortOf）。
//
// 残余设计债（已登记，需独立切片）：IR 携带平台物理名本身违背「IR = 语义定锚」，
// 目标形态是 **IR 只携带语言层名、平台名下沉到各后端**。本切片只做「契约声明化」。
// 用函数而非包级 var：闭源契约由下游模块在 init() 注册，包级 var 会在下游
// init 之前定格 ⇒ 多平台构建会误选 dify。
func IRPortVocabularyTarget() string { return choosePortVocabularyTarget() }

// choosePortVocabularyTarget 选本构建里**实际存在**的端口词汇表目标：
// 多平台构建（含 coze-biz 契约）仍取 coze-biz（历史口径不变）；开放仓只带
// dify 契约时取 dify —— 否则查不到契约会退化为 ref.output，静默丢端口映射。
func choosePortVocabularyTarget() string {
	if _, ok := builtin.Contract("coze-biz-1.0")["http"]; ok {
		return "coze-biz-1.0"
	}
	for _, t := range builtin.DeclaredTargets() {
		if _, ok := builtin.Contract(t)["http"]; ok {
			return t
		}
	}
	return "coze-biz-1.0"
}

// fieldPort maps an ncoda field name onto the output port of the producing
// node family. Single-output families (LLM/Code) funnel every field onto
// their "output" port; HTTP exposes body/statusCode/headers.
func fieldPort(ref varRef, field string) string {
	switch ref.kind {
	case "llm":
		// LLM refs remain semantic in neutral IR. Target backends own the
		// mapping from text/metadata paths to physical output ports.
		return field
	case "attempt-error":
		// OperationError record fields are the operation's semantic error
		// output ports (dify http-request fail-branch exposes them as
		// node ports; Coze maps these onto default/branch_error.
		if field == "error_type" || field == "error_message" {
			return field
		}
		return field
	case "qa", "extract", "agent", "intent", "sub_workflow":
		// QuestionAnswer node outputs are the structured form fields
		// (comment, priority.value, USER_RESPONSE) — the field name IS
		// the output port. Agent's OperationResult[agent] record
		// ({text: string}) likewise exposes the field as the port.
		// IntentDetector exposes classificationId / reason as ports.
		return field
	case "knowledge":
		// 端口权威 = **内建节点结果契约**（注册表声明，lowering/raising 双向共读，
		// 见 internal/builtin）：语言字段 `result` ⇄ 物理口 `outputList`。
		// 契约未声明该字段 ⇒ 保持既有缺省（ref.output），不发明名字。
		if port, ok := builtin.PortOf(IRPortVocabularyTarget(), "knowledge", field); ok {
			return port.Port
		}
		return ref.output
	case "http":
		// 端口权威 = **内建节点结果契约**（注册表声明，lowering/raising 双向共读，
		// 见 internal/builtin）。语言字段名 → 平台物理口名由契约给出，
		// 不在此处按名字形态猜、也不再各处手抄一份。
		// 契约未声明该字段 ⇒ 保持既有缺省（ref.output），不发明名字。
		if port, ok := builtin.PortOf(IRPortVocabularyTarget(), "http", field); ok {
			return port.Port
		}
		return ref.output
	case "code":
		// structural code outputs expose one port per field (the field
		// name IS the port); direct outputs ignore the field.
		return field
	case "json":
		// JsonDeserialization (json.parse<T>) exposes the whole parsed
		// object on its single "output" port; field access resolves as a
		// dotted path output.title (coze splits ref names on "." into
		// from_path). G11.
		return ref.output + "." + field
	case "loop":
		// Loop iteration variable (r): the item port carries the element
		// record; member access resolves as a dotted path r.name so loop
		// internals reference the per-iteration field (G13/H4).
		return ref.output + "." + field
	case "tool":
		// Tool results are structured records declared by the capability
		// registry (definitions.Outputs are dotted member paths). Preserve
		// the full semantic member path so each target renderer can rebuild
		// the platform output reference (Coze: the plugin's object-typed
		// output port; Dify: the tool's single result).
		if ref.typ != nil && ref.typ.Kind == "object" {
			if _, ok := ref.typ.Props[field]; ok {
				return field
			}
			// Registry outputs commonly place payload fields below a physical
			// object port, e.g. data.duration. A phi group contains the
			// semantic leaf, so recover the real producer path here.
			for root, child := range ref.typ.Props {
				if child != nil && child.Kind == "object" {
					if _, ok := child.Props[field]; ok {
						return root + "." + field
					}
				}
			}
		}
		return field
	case "entry":
		// 入口参数（`function main(Pair p)`）的成员读：口名 = 参数名，声明类型 = 参数
		// 类型，成员活在**口下方** ⇒ 保留基段（`p.left`）。与 aggregate / json / loop
		// 的「容器口下钻」同一条规则；裸字段名会引用一个 start 从未声明的口。
		//
		// 一手（2026-09-27，CB-5）：修前 `return p.left;` 的产物 = 出口绑定
		// `{path: "p", ref_node: "100001"}`（**整个 record** 绑给声明为 `string` 的
		// 返回值）⇒ 反编译回来 `function main(string p) { return p; }`，字段与参数类型
		// 双丢（往返 3 轮仍 ✗）。
		return ref.output + "." + field
	case "aggregate":
		// 组级 phi 的成员读取按绑定形态分叉（2026-09-04 S43/R2 实证）：
		// - output==""（record 逐字段 plan / 整值绑定）：字段名 IS 组端口名
		//   （record 每字段一个组），裸字段即端口，后端直接映射组端口。
		// - output=="<组名>"（逐变量标量组 scalarPort：变量整值挂在具名组口，
		//   组值可为标量/数组/record）：成员读取必须保留组口基段合成 dotted
		//   路径 "<组名>.<field>"。裸 field 会丢组口基段 → 悬空 ref
		//   （merge 不声明裸字段端口；S43 b2 实证 code-shim 参数
		//   `agg_v100010_data.url`（list<record> 组元素字段，E5-b 隐式 [0]）
		//   投影成 {merge, url} 端口不存在 → round3 EMPTY_AGGREGATE_TYPE；
		//   与 b1 真导出 {merge, data.url} 同构的 dotted 形态才能闭合）。
		if ref.output != "" {
			return ref.output + "." + field
		}
		return field
	}
	// llm / text: single output port.
	return ref.output
}

// materializedKind reports whether a varRef kind denotes a node the builder
// just materialized (vs. a plain alias to an existing producer).
func materializedKind(kind string) bool {
	return kind == "text" || kind == "code"
}

// datasetID maps a symbolic dataset name onto a deterministic int64
// placeholder id: trailing digits are kept ("ds-001" → "1"), otherwise a
// stable FNV hash. Placeholder — real deployments replace with platform ids.
//
// 返回的串必须落在**平台 schema 的域**里：coze-studio 的 knowledge 节点逐元素
// `cast.ToInt64E`（backend/domain/workflow/internal/nodes/knowledge/
// knowledge_retrieve.go:64-71）⇒ dataset id 是 **int64**，`"001"` 与 `"1"` 是同一个
// id 的两种拼写。只取尾部数字串而不规范化 ⇒ **写侧铸 `"001"`、读侧按 schema 读回
// `"1"`** ⇒ 两轮不同（一手 = examples/nodecoda/{02,10,41} 三源，逐字节首差异
// `- "001"` vs `- "1"`）。契约（本函数头注）本来写的就是 `"1"` —— 实现违背了自己
// 的契约；改实现，不改读侧（读侧 `canonicalInt64` 是平台解析器的忠实镜像）。
// 副作用即所需：`datasetID(datasetID(x)) == datasetID(x)`（幂等 ⇒ 一轮不动点）。
//
// 超出 int64 域的纯数字名（30 位数字）平台自己也 `cast` 不了 ⇒ 与无名同路走哈希
// （仍然确定、仍在域内），绝不把平台加载不了的 id 发出去。
func datasetID(name string) string {
	re := regexp.MustCompile(`\d+$`)
	if m := re.FindString(name); m != "" {
		if n, err := strconv.ParseInt(m, 10, 64); err == nil {
			return strconv.FormatInt(n, 10)
		}
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return strconv.FormatUint(uint64(h.Sum32()), 10)
}

// templateText flattens a template string; the bool reports whether any
// interpolation was present (code sources are expected to be plain text).
func templateText(ts *ast.TemplateString) (string, bool) {
	if ts == nil {
		return "", false
	}
	var sb strings.Builder
	interp := false
	for _, part := range ts.Parts() {
		sb.WriteString(part.Text)
		if part.Expr != nil {
			interp = true
		}
	}
	return sb.String(), interp
}

// collectAssignedVars gathers every identifier assigned inside a statement
// list (directly or inside nested control flow).
// 判据只有一条：**整棵子树里的赋值**。禁止按语句类型枚举「含块的容器」——
// 容器家族（if / for / while / parallel / attempt / switch / ask，以及出现在
// **表达式**里的 loop-expr / parallel-for）是开放的，枚举漏一个就是一条**静默
// 丢值**通道：变量不被判为循环状态 ⇒ 循环体内的写入不进容器反馈口 ⇒ 循环后
// 读到循环前的值、产物里该汇聚无消费者被 IR 优化删除 ⇒ 反编译读回时宿主臂为
// 空、出码语法非法（2026-09-13 实测漏 switch / attempt / parallel / ask 四类，
// 见 pkg/ir/loop_state_nested_host_test.go）。
//
// 因此用通用遍历器 ast.Walk —— 与 passes 层 `LoopStatePass.collectWrites`
// （pkg/passes/loop_state.go:251-256）以及本层 `conditionMarker`
// （build_return.go）同一机制。同族教训：S107 desugar 手枚举漏容器 ⇒ elif 中臂
// 丢失（pkg/passes/desugar_container_closure_test.go 头注）。
func collectAssignedVars(stmts []ast.Stmt, out map[string]bool) {
	c := &assignedVarCollector{out: out}
	for _, st := range stmts {
		ast.Walk(c, st)
	}
}

// assignedVarCollector 记下子树里每一个赋值目标（`*ast.IdentExpr`）。返回自身
// ⇒ 遍历一路下钻到所有嵌套容器与表达式子树。
type assignedVarCollector struct{ out map[string]bool }

func (c *assignedVarCollector) Visit(n ast.Node) ast.Visitor {
	if a, ok := n.(*ast.AssignStmt); ok {
		if id, ok := a.Target().(*ast.IdentExpr); ok {
			c.out[id.Name()] = true
		}
	}
	return c
}
