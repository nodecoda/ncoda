// IR builder — 按语句/节点族拆分的文件之一（Tier-2 review #9:拆文件不拆包,
// diff 局部化）。同一包内符号共享;本文件族的归属见各函数注释。
package ir

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/llm"
	"github.com/nodecoda/ncoda/pkg/pyast"
)

// ── statement lowering (migrated from the Coze generator) ────────────────

// materializeCall lowers a call expression to a producer node and returns
// the flow ids plus the ref to bind (the shared producer pipeline —
// §5.3 stage A: emitVarDecl CallExpr path; stage C: hoistUserCalls will
// reuse it to materialize bare operation calls in expressions). Mirror of
// the former emitVarDecl CallExpr branch, kept line-for-line so node-id
// allocation order is unchanged — any drift shows up as a 27-artifact sha
// delta.
func (g *Builder) materializeCall(in *ast.CallExpr, prevIDs []string, firstPort string) ([]string, varRef, error) {
	// file(x) — string→file 标注 cast 零节点透传：可解析参数直接解包为
	// 底层 ref（let f = file(x) 在 emitVarDecl 顶部的 resolveValueRef 已
	// 拦截, 到不了这里）; 不可解析参数（file(a+b) / file(split(s)[0])）
	// 先物化参数表达式, 再把其 ref 透传为绑定值。不产生新节点。
	if calleeName(in.Func()) == "file" {
		if len(in.Args()) != 1 {
			return nil, varRef{}, &UnsupportedError{Construct: "call \"file\" expects 1 argument"}
		}
		if ref, ok := g.resolveValueRef(in.Args()[0]); ok {
			return prevIDs, ref, nil
		}
		ref, verr := g.emitValueExpr(prevIDs, firstPort, false, in.Args()[0])
		if verr != nil {
			return nil, varRef{}, verr
		}
		return prevIDs, ref, nil
	}
	if isListOpName(calleeName(in.Func())) {
		ref, lerr := g.emitListOp(in, prevIDs, firstPort)
		if lerr != nil {
			return nil, varRef{}, lerr
		}
		g.recordPlacement(ref.nodeID, placementConstruct("list-op", calleeName(in.Func())), PlacementShim, placementReason(PlacementCauseNoNativeNode, "lambda op materializes as python (no native collection node)"))
		return []string{ref.nodeID}, ref, nil
	}
	// Calculation builtins have two lowering paths. Resolve the native text
	// subset before allocating an id so a shim fallback does not create an
	// observable id gap in the deterministic graph.
	switch calleeName(in.Func()) {
	case "join":
		if _, ok := g.buildJoinNative("", in); ok {
			id := g.nextID()
			node, _ := g.buildJoinNative(id, in)
			g.add(node)
			g.recordPlacement(id, placementConstruct("builtin", "join"), PlacementNative, placementReason(PlacementCauseCapability, "TextProcessor concat array<string> + static separator"))
			return []string{id}, varRef{nodeID: id, output: "output", kind: "text"}, nil
		}
		ref, cerr := g.emitExprCodeOut(prevIDs, firstPort, false, in, g.calcBuiltinOutTypeFor("join", in))
		if cerr != nil {
			return nil, varRef{}, cerr
		}
		g.recordPlacement(ref.nodeID, placementConstruct("calc", "join"), PlacementShim, placementReason(PlacementCauseNoNativeNode, "no native node for generic form"))
		return []string{ref.nodeID}, ref, nil
	case "split":
		if _, err := g.buildSplit("", in); err == nil {
			id := g.nextID()
			node, _ := g.buildSplit(id, in)
			g.add(node)
			g.recordPlacement(id, placementConstruct("builtin", "split"), PlacementNative, placementReason(PlacementCauseCapability, "TextProcessor split with static separator"))
			return []string{id}, varRef{nodeID: id, output: "output", kind: "text"}, nil
		}
		ref, cerr := g.emitExprCodeOut(prevIDs, firstPort, false, in, g.calcBuiltinOutTypeFor("split", in))
		if cerr != nil {
			return nil, varRef{}, cerr
		}
		g.recordPlacement(ref.nodeID, placementConstruct("calc", "split"), PlacementShim, placementReason(PlacementCauseNoNativeNode, "no native node for generic form"))
		return []string{ref.nodeID}, ref, nil
	}
	id := g.nextID()
	var node Node
	var kind string
	if g.isQualifiedToolCall(in) {
		// Qualified capability call p.op({...}) (TOOL-DIRECT-CALL-FORM):
		// lowers to the ToolCall the legacy tool("p","op",{...}) produced.
		// gated so method/record-field/std calls are untouched.
		tool, terr := g.buildTool(id, in)
		if terr != nil {
			return nil, varRef{}, terr
		}
		node = tool
		kind = "tool"
		g.recordPlacement(id, placementConstruct("tool", calleeName(in.Func())), PlacementNative, placementReason(PlacementCauseCapability, "workflow operation: capability node"))
	} else {
		switch calleeName(in.Func()) {
		case "json.stringify":
			n, jerr := g.buildStringify(id, in, prevIDs, firstPort)
			if jerr != nil {
				return nil, varRef{}, jerr
			}
			node = n
			kind = "json"
			g.recordJSONBuiltinPlacement(id, "stringify", n)
		case "json.parse":
			n, jerr := g.buildJSONParse(id, in, prevIDs, firstPort)
			if jerr != nil {
				return nil, varRef{}, jerr
			}
			node = n
			kind = "json"
			g.recordJSONBuiltinPlacement(id, "parse", n)
		case "llm":
			lc, lerr := g.buildLLMCall(id, in, prevIDs, firstPort)
			if lerr != nil {
				return nil, varRef{}, lerr
			}
			node = lc
			kind = "llm"
			g.applyLLMTimeout(node, in)
			g.recordPlacement(id, placementConstruct("workflow", "llm"), PlacementNative, placementReason(PlacementCauseCapability, "workflow operation: native LLM node"))
		case "http":
			hc, herr := g.buildHTTP(id, in, prevIDs, firstPort)
			if herr != nil {
				return nil, varRef{}, herr
			}
			node = hc
			kind = "http"
			g.recordPlacement(id, placementConstruct("workflow", "http"), PlacementNative, placementReason(PlacementCauseCapability, "workflow operation: native HTTP node"))
		case "knowledge":
			kn, kerr := g.buildKnowledge(id, in, prevIDs, firstPort)
			if kerr != nil {
				return nil, varRef{}, kerr
			}
			node = kn
			kind = "knowledge"
			g.recordPlacement(id, placementConstruct("workflow", "knowledge"), PlacementNative, placementReason(PlacementCauseCapability, "workflow operation: native knowledge node"))
		case "extract":
			ln, xerr := g.buildExtract(id, in)
			if xerr != nil {
				return nil, varRef{}, xerr
			}
			g.add(ln)
			g.recordPlacement(id, placementConstruct("workflow", "extract"), PlacementNative, placementReason(PlacementCauseCapability, "workflow operation: native LLM responseFormat node"))
			// 调用值 = 整个 `Extraction[T]`：kind 是 "extract"，口名是规范载荷口
			// （`e.value` 的选择器 = `fieldPort(ref,"value")` 恒等 ⇒ 逐字相同）。
			// 载荷记录**本身**的绑定带另一个 kind（见 extractPayloadKind）。
			return []string{id}, varRef{nodeID: id, output: ExtractValuePort, kind: "extract"}, nil
		case "agent":
			ac, aerr := g.buildAgent(id, in)
			if aerr != nil {
				return nil, varRef{}, aerr
			}
			node = ac
			kind = "agent"
			g.recordPlacement(id, placementConstruct("workflow", "agent"), PlacementNative, placementReason(PlacementCauseCapability, "workflow operation: native agent node (profile-pinned)"))
		case "run":
			// @mode agent entry call: run(query) lowers to the declared
			// agent application (workflow sugar — start → agent → end).
			if g.prog.Program.Mode() != "agent" {
				return nil, varRef{}, &UnsupportedError{Construct: fmt.Sprintf("call %q outside @mode agent", "run")}
			}
			ac, aerr := g.buildAgentRun(id, in)
			if aerr != nil {
				return nil, varRef{}, aerr
			}
			node = ac
			kind = "agent"
			g.recordPlacement(id, placementConstruct("agent-mode", "run"), PlacementNative, placementReason(PlacementCauseCapability, "agent-mode entry: @agent declaration → native agent node"))
		case "conversation_history":
			// @mode advanced-chat entry call: conversation_history() lowers
			// to a conversation node (M13 chatflow — rendered history).
			if g.prog.Program.Mode() != "advanced-chat" {
				return nil, varRef{}, &UnsupportedError{Construct: fmt.Sprintf("call %q outside @mode advanced-chat", "conversation_history")}
			}
			g.add(&HistoryCall{ID: id})
			g.recordPlacement(id, placementConstruct("advanced-chat", "history"), PlacementNative, placementReason(PlacementCauseCapability, "advanced-chat entry: conversation history → native conversation node"))
			// 输出端口是 history（节点实现输出键）;不能落到 case 外统一的
			// "output" 默认端口,否则下游模板取 {{...output}} 恒为空（M13
			// e2e 抓出的真实 bug,与 emitValueExpr 值位置分支对齐）。
			return []string{id}, varRef{nodeID: id, output: "history", kind: "conversation"}, nil
		default:
			// SubWorkflow declaration reference (SUBWORKFLOW-DECLARATION):
			// <name>({ input bindings }) lowers to a SubWorkflowCall carrying
			// the declared platform identity + output contract.
			if decl, ok := g.subflows[calleeName(in.Func())]; ok {
				sc, serr := g.buildSubWorkflow(id, in, decl, prevIDs, firstPort)
				if serr != nil {
					return nil, varRef{}, serr
				}
				g.add(sc)
				g.recordPlacement(id, placementConstruct("sub_workflow", decl.Name()), PlacementNative, placementReason(PlacementCauseCapability, "subflow declaration call: native subworkflow node"))
				ref := varRef{nodeID: id, output: "output", kind: "sub_workflow"}
				return []string{id}, ref, nil
			}
			if isCalcBuiltin(calleeName(in.Func())) {
				// G10/G14: pure calculation builtin (len/join/trim/to_string/float/
				// contains/split) materializes as a Synthetic code shim with
				// the builtin's real output type — no native platform node
				// exists for the generic form (join of a dynamic array,
				// len, strip, str()).
				ref, cerr := g.emitExprCodeOut(prevIDs, firstPort, false, in, g.calcBuiltinOutTypeFor(calleeName(in.Func()), in))
				if cerr != nil {
					return nil, varRef{}, cerr
				}
				g.recordPlacement(ref.nodeID, placementConstruct("calc", calleeName(in.Func())), PlacementShim, placementReason(PlacementCauseNoNativeNode, "no native node for generic form"))
				return []string{ref.nodeID}, ref, nil
			}
			if fn, ok := g.funcs[calleeName(in.Func())]; ok {
				junction, _, ref, ierr := g.inlineCall(fn, in.Args(), prevIDs, firstPort)
				if ierr != nil {
					return nil, varRef{}, ierr
				}
				if ref.nodeID == "" {
					return nil, varRef{}, &UnsupportedError{Construct: fmt.Sprintf("function %q produces no value", calleeName(in.Func()))}
				}
				return junction, ref, nil
			}
			if strings.HasPrefix(calleeName(in.Func()), "std.") {
				junction, ref, serr := g.emitStdlibCall(in, prevIDs, firstPort)
				if serr != nil {
					return nil, varRef{}, serr
				}
				return junction, ref, nil
			}
			return nil, varRef{}, &UnsupportedError{Construct: fmt.Sprintf("call %q in let", calleeName(in.Func()))}
		}
	}
	g.add(node)
	// 绑定形状的唯一构造函数（见 callBindRef 头注：typ 是路径还原的权威，
	// 漏设即吞容器前缀）。
	ref := g.callBindRef(id, node, kind, in)
	if cr, ok := node.(*CodeRun); ok && cr.IsJSONParseShim() &&
		cr.JSONSchema != nil && (cr.JSONSchema.Kind == "list" || cr.JSONSchema.Kind == "array") {
		ref.jsonParseArray = true
	}
	return []string{id}, ref, nil
}

// emitVarDecl lowers one declaration (or one write materialized as a
// declaration — see emitMutableLocalWrite). A resolvable init (ident / field
// access) is a zero-node alias; calls / code / literal / template inits
// materialize a node.
func (g *Builder) emitVarDecl(s *ast.VarDeclStmt, prevIDs []string, firstPort string) ([]string, string, error) {
	// 语言判定（A 级，2026-09-19 用户拍板）：`T v = <init>;` 与 `T v; v = <init>;`
	// **等价**（本语言不区分构造与赋值）⇒ 带初值的可变声明也是**提升声明**：
	// 登记 hoistedVars（供 emitAssign 分派 + emitVarDeclAs 顶部的 addressable
	// 判定）与类型权威（供 junction phi 的类型三级链）。
	//
	// **登记必须先于初值物化**（2026-09-21 收口；此前登记在物化之后）：addressable
	// 在 emitVarDeclAs 顶部读 hoistedVars ⇒ 登记晚一步，两条**等价拼写**就分走两条
	// 路 —— `T v = <lit>;` 折进 litVars（无 producer 地址），`T v; v = <lit>;` 物化
	// 常量 shim。实测（本仓最小形，HEAD 两二进制 A/B）：joined 拼写产物 3610 字符
	// vs bare 拼写 4510 字符 —— 差的就是基座常量载体（joined 那侧产物里连
	// 基座节点都没有）；
	// 更重的是**静默丢值** —— `int v = 5; if (u == "a") { v = 7; } return v;` 的
	// 汇聚只有臂候选（基座不在 phi 视图里）⇒ 条件不成立时产物读出 0 而不是 5，
	// 且反编译产物被合成记录类型 `RT_vInt` 整体改写（编译 SUCCEEDED、L1/L2 全绿）。
	// string 无此病（下方 StringLit 分支**无条件**物化常量节点）—— 这个不对称正是
	// 出生处：语言递归自洽要求 int/float/bool 与 string 同一把尺子。
	if s.Init() != nil && s.IsMutable() {
		g.hoistedVars[s.Name()] = true
		if s.TypeRef() != nil {
			g.hoistedTypes[s.Name()] = g.typeOf(s.TypeRef())
			if isOptionalRef(s.TypeRef()) {
				g.hoistedOptional[s.Name()] = true
			}
		}
	}
	return g.emitVarDeclAs(s, prevIDs, firstPort)
}

func (g *Builder) emitVarDeclAs(s *ast.VarDeclStmt, prevIDs []string, firstPort string) ([]string, string, error) {
	// 记录整值折叠（J §42.3 A 轴，2026-09-24；判据见 record_fold.go）：整值语义不可观察的
	// 单叶字段记录本地**不物化容器**，绑定直接承载字段值（单元名 = 变量名）。
	// 三件事一起做，缺一即半接线：
	//   ① 类型权威登记为**叶类型** —— 分支汇聚按标量通道走（组名 = 变量名，C 轴第一优先），
	//      且 junctionPhiLevels 的①级（声明类型）拿到的是叶而不是记录；
	//   ② 记录字面量写重写为**字段值**（值路径原样复用：ref/alias/常量 shim 一把尺子）；
	//   ③ 成员读经 resolveValueRef 落在单元本身上。
	declType := (*Type)(nil)
	if s.TypeRef() != nil {
		declType = g.typeOf(s.TypeRef())
	}
	// Optional 记录不折（折掉叶单元会丢 `T?` 标记）——与 record_fold.go 的「本刀不做」一致。
	if s.TypeRef() != nil && isOptionalRef(s.TypeRef()) {
		declType = nil
	}
	if field, fieldType, folded := g.recordFold(s.Name(), declType); folded {
		declType = fieldType
		if ml, isLit := s.Init().(*ast.MapLit); isLit {
			if v, vok := recordLiteralFieldValue(ml, field); vok {
				s = ast.NewVarDeclStmt(s.Name(), nil, v, s.IsMutable())
			}
		}
	}
	// 类型登记在一切分支之前：`T v;`（裸）与 `T v = expr;`（带 init）都必须
	// 记录声明类型——循环状态分类、类型轴复真以此为准（varDeclTypes 2026-09-10）。
	// init==nil 分支下方仍写 hoistedTypes（其分支 phi/merge 语义消费方不变）。
	if s.TypeRef() != nil {
		g.varDeclTypes[s.Name()] = declType
	} else if declType != nil {
		// 折叠后的写路径（TypeRef == nil）也要让声明类型保持叶类型（否则②级类型链
		// 退化到口类型；③级不受影响）。
		g.varDeclTypes[s.Name()] = declType
	}
	// 可寻址可变本地（A 级，2026-09-19 用户拍板）：**可变本地 = 有地址的绑定**
	// —— 裸声明与带初值声明（登记见 emitVarDecl）都是提升声明，其后的写必须
	// 物化 producer（可寻址）；`let`（不可变）保持常量折叠。
	addressable := g.hoistedVars[s.Name()]
	init := s.Init()
	if init == nil {
		// 裸声明（var v; / let v; / string v;）零节点：var v; 是反编译
		// 分支变量提升的作用域声明，节点在后续赋值处物化。登记
		// hoistedVars（可变态量，含带类型聚合变量 string agg_x; —— 分支
		// 聚合消解 X212 模式）供 emitAssign 分派；其余零节点。while 状态
		// 变量有 LoopStateFact → loopStates 分派先于 hoistedVars，不受影响。
		if s.IsMutable() {
			g.hoistedVars[s.Name()] = true
			if s.TypeRef() != nil {
				// declType 已按折叠结果取叶类型（record_fold.go）——裸声明是折叠
				// 的类型权威落点，写入端（TypeRef == nil）靠这张表回查。
				g.hoistedTypes[s.Name()] = declType
				if isOptionalRef(s.TypeRef()) {
					g.hoistedOptional[s.Name()] = true
				}
			}
		}
		return prevIDs, firstPort, nil
	}
	// hoist user-fn calls in any init position (arithmetic, template,
	// literal, ...) so a pure call inside a let expression compiles
	// (e.g. `let r = helper(s) + "!"` → helper producer + concat). §5.1/5.2.
	// Top-level call inits (`let x = helper(s)`) are NOT hoisted: they keep
	// the dedicated CallExpr branch below (same producer, stable node ids).
	if _, isCall := init.(*ast.CallExpr); !isCall {
		hoisted, herr := g.hoistUserCalls(init, prevIDs, firstPort)
		if herr != nil {
			return nil, firstPort, herr
		}
		init = hoisted
	}
	if ref, ok := g.resolveValueRef(init); ok {
		g.varToOutput[s.Name()] = ref
		g.tailRef = &ref
		return prevIDs, firstPort, nil
	}
	// F1 ②（字面量局部引用，2026-09-11）：`let v1 = 7; g = v1;` —— v1 是字面量
	// 局部，自身没有 producer 地址（litVars 无节点），下面 switch 会落到
	// 「非调用表达式」硬错误。提升变量的写必须可寻址 ⇒ 其操作数亦然：在此按
	// v1 的字面量值物化常量 shim（与就地字面量写 ① 同一结果、同一把尺子）。
	if id, ok := init.(*ast.IdentExpr); ok {
		if lv, isLit := g.litVars[id.Name()]; isLit {
			if addressable {
				ref := g.emitLitConstShim(lv)
				g.varToOutput[s.Name()] = ref
				g.tailRef = &ref
				return []string{ref.nodeID}, "", nil
			}
			// 不可变绑定（`let w = v;`，v 是字面量局部）：本语言不区分构造与
			// 赋值 ⇒ 这是**绑定到同一个常量**，不物化任何节点（与 string 初值
			// 的 `let` 同一条路：那里 StringLit 物化常量节点、再引用它）。
			// 缺这支 = `let w = v;` 落到「非调用表达式」硬错误（既有缺口，
			// 由臂门控的恒等载体读回首次触发：`let v4 = v1; failed = v4;`）。
			g.litVars[s.Name()] = lv
			return prevIDs, firstPort, nil
		}
	}
	switch in := init.(type) {
	case *ast.NullLit:
		// M1/R5（2026-09-05）：hoisted Optional 变量 `v = null`（反编译两行式
		// 提升 `T? v; v = null;`）—— null 初值不物化 producer：未赋路径在
		// 平台 = 无 producer ref = null 跳过（与真实 merge 候选直引同构），
		// 零值/恒等 shim 不产生。变量保持 hoisted 无绑定，由 if 汇聚点按
		// "Optional 未赋值分支不贡献候选"处理（emitIf）。非 hoisted 的
		// 2026-09-25 用户裁决（甲）：**non-hoisted 同解**。合并前这里是 fail-loud
		// （`let x = null` 单行声明）。理由 = coze-biz 被显式定义为**不支持
		// nil/null 的平台**（AGENTS.md「null / undefined 语义契约」）⇒ 显式 null
		// 按平台语义**吸收**（无 producer；消费点由平台 FillZero 兜零值），
		// 不得拒收合法源码（`null` 只在作者显式书写处出现 = 合法 ncoda）。
		// 降级由 `target_validation` 报账（TARGET_CONTRACT_DEGRADED）。
		// **读点不变**：`null` 的推导类型就是 `null`（type_pass），没有可物化的
		// 零值 ⇒ 读它仍是 fail-loud（既有缺口，登记在 `residuals.yaml#R-65`）。
		//
		// **这条设计有前提**（登记 `residuals.yaml#R-66`）：本仓三个目标**都不承载**
		// 显式 null ⇒ 共享 IR 里 null 值**没有表示**（无 NullValue 节点）。若日后某目标
		// 经一手验证可承载显式 null，正确做法 = IR **携带** null 值 + 各目标各自实现
		// （能承载 ⇒ 发射 null；不能 ⇒ 吸收 + 报账），**不是**在渲染层加分支
		// （渲染层看不到 null：无绑定即无形状）。
		_ = addressable
		return prevIDs, firstPort, nil
	case *ast.IntLit, *ast.FloatLit, *ast.BoolLit:
		lv, _ := litVarOfScalarLiteral(in) // 本 case 的三个类型恒在闭集内
		// F1（2026-09-11，S2-gate D1 根因）：**提升变量**（可变态量）的每一次写
		// 都是一次绑定变更，必须可寻址——与上面的 StringLit 走同一条路
		// （物化常量 producer + 写 varToOutput）。字面量只进 litVars 时没有
		// producer 地址，交汇点 phi 规划的「前绑定 / 臂末绑定」视图（varToOutput
		// 快照）看不见它 ⇒ 汇聚点候选为空 ⇒ 分支语义静默消失
		// （`int g; g = 0; if(..){g=7;} else {g=9;}` ⇒ merge variables: []）。
		// 规则无例外：int/float/bool/string 同一把尺子（string 早已可寻址）；
		// 非提升局部仍走 litVars 常量折叠（无交汇点，不需要地址）。
		if addressable {
			ref := g.literalWriteRef(lv)
			g.varToOutput[s.Name()] = ref
			g.tailRef = &ref
			return []string{ref.nodeID}, "", nil
		}
		g.litVars[s.Name()] = lv
		return prevIDs, firstPort, nil
	case *ast.CallExpr:
		ids, ref, merr := g.materializeCall(in, prevIDs, firstPort)
		if merr != nil {
			return nil, firstPort, merr
		}
		g.varToOutput[s.Name()] = ref
		g.tailRef = &ref
		return ids, "", nil
	case *ast.ForeignCodeExpr:
		id := g.nextID()
		node, cerr := g.buildCode(id, in)
		if cerr != nil {
			return nil, firstPort, cerr
		}
		g.add(node)
		ref := varRef{nodeID: id, output: codeWholeValuePort(node.(*CodeRun)), kind: "code"}
		g.varToOutput[s.Name()] = ref
		g.tailRef = &ref
		return []string{id}, "", nil
	case *ast.StringLit:
		ref, verr := g.emitTextConst(prevIDs, firstPort, false, in.Value())
		if verr != nil {
			return nil, firstPort, verr
		}
		g.varToOutput[s.Name()] = ref
		g.tailRef = &ref
		return []string{ref.nodeID}, "", nil
	case *ast.TemplateString:
		ref, verr := g.emitTextTemplate(prevIDs, firstPort, false, in)
		if verr != nil {
			return nil, firstPort, verr
		}
		g.varToOutput[s.Name()] = ref
		g.tailRef = &ref
		return []string{ref.nodeID}, "", nil
	case *ast.TemplateValue:
		// 值位置的**条件**具名形态（R-163 甲）：`let s = template "…" using {…}` 里
		// 至少一个 token 的名字不是名额名（`{{String1[2].authors}}`）⇒ 内插拼不出
		// ⇒ 具名形态是唯一承载面（位置判据 = template_value_placement.go 同一条件）。
		ref, verr := g.emitNamedTextTemplate(prevIDs, firstPort, false, in)
		if verr != nil {
			return nil, firstPort, verr
		}
		g.varToOutput[s.Name()] = ref
		g.tailRef = &ref
		return []string{ref.nodeID}, "", nil
	case *ast.BinaryExpr:
		// any binary operator: the type layer already gated semantics (P3).
		// String `+` on a target with a native text-concat node maps to
		// TextProcess (pass 1, native-first); arithmetic/logic and targets
		// without the node materialize as Synthetic code shims (pass 2).
		ref, verr := g.emitBinaryValue(prevIDs, firstPort, false, in)
		if verr != nil {
			return nil, firstPort, verr
		}
		g.varToOutput[s.Name()] = ref
		g.tailRef = &ref
		return []string{ref.nodeID}, "", nil
	case *ast.IndexExpr, *ast.FieldAccessExpr:
		// 枚举成员 = **它的线值字符串字面量**（R-62 的口径：枚举成员是字面量、
		// 中性 IR 把 `enum → string`）⇒ 物化路径必须与作者书写 `"<线值>"` 的
		// 路径**逐字同形**（`emitTextConst`，见下面的 StringLit 分支）。缺这支 =
		// 同一常量两种产物（书写字面量 ⇒ 原生 text 节点；枚举成员 ⇒ 自造的常量
		// code shim）⇒ 一次往返后拓扑变化（I1b 破裂；一手：生成器形状 `type_decl`
		// 12/12 源）。类型结论侧同解（`typeOf` / `semanticDescriptorType` 的枚举
		// 分支都给 string），故这里不发明任何新形状。
		if value, ok := g.enumMemberValue(in); ok {
			ref, verr := g.emitTextConst(prevIDs, firstPort, false, value)
			if verr != nil {
				return nil, firstPort, verr
			}
			g.varToOutput[s.Name()] = ref
			g.tailRef = &ref
			return []string{ref.nodeID}, "", nil
		}
		// Array element access without a dotted-port form (scalar arrays —
		// split(...)[0], r[0]; json.parse<array<T>> element chains —
		// items[0].field, G12-2): no native node, so the value materializes
		// as a Synthetic code shim (Python index / guarded .get). Record-
		// array member chains resolve earlier via resolveValueRef (E5-b
		// dotted ports).
		ref, verr := g.emitExprCode(prevIDs, firstPort, false, in)
		if verr != nil {
			return nil, firstPort, verr
		}
		g.varToOutput[s.Name()] = ref
		g.tailRef = &ref
		return []string{ref.nodeID}, "", nil
	case *ast.TernaryExpr:
		// cond ? X : Y lowers through the if/else machinery: synthesized
		// branch returns merge into a VariableAggregator whose Group1
		// output becomes the binding.
		// syntheticTernary 保护必须与 emitValueExpr 的三元路径一致：合成
		// return 只是"分支值上浮"机制,不是用户函数返回 —— 否则 loop body
		// 内的 `let x = cond ? a : b` 会把分支 return 误判为 loop 内提前
		// 返回（M17），触发 loop_return（coze-biz 不支持）。回归: X4 batch
		// 体 + loop-ternary-var 用例。
		// 槽名 = 本次声明的目标名（`T v = (c) ? A : B;`；写位置 `v = (c) ? A : B;`
		// 经 emitAssign → emitVarDecl 走**同一条**路径，见 build_stmt.go）——
		// 否则同语义的 if 形态产出槽名 `v`、三元形态产出 `Group1`（U6）。
		ids, terr := g.emitTernaryJunction(prevIDs, firstPort, in.Condition(), in.ThenVal(), in.ElseVal(), s.Name())
		if terr != nil {
			return nil, firstPort, terr
		}
		// 三元结果 = 该 junction 的**整值**，由 `junctionValueRef` 按组形状给出
		// （record 值 ⇒ 具名逐字段组 ⇒ 整值 = merge 节点本身；标量值 ⇒ legacy
		// `Group1` 口）。旧实现此处**硬编码 `Group1`**，即假设「臂必产值且值是
		// 标量口」—— record 值下指向不存在的端口语义（CB-1：`-> string` 的函数
		// 返回整个 record），void 值下产物被自家 L2 判非法（C6）。
		// 附带收口（第七次 S2-gate C6 / §16.2 登记的「值位置 junction 无值」）：
		// junction 没有产出值（零候选 = 两臂皆 void）时**不再伪造端口**，改为
		// fail loud（`junctionValueRefOf`）——「值位置拒绝 void」这条更上游的
		// 语言规则仍待立项，但伪造引用必须先停。
		if len(ids) == 0 {
			return nil, firstPort, &UnsupportedError{Construct: "ternary value position: junction produced no value"}
		}
		ref, verr := g.junctionValueRefOf(ids[0])
		if verr != nil {
			return nil, firstPort, verr
		}
		g.varToOutput[s.Name()] = ref
		g.tailRef = &ref
		return ids, "", nil
	case *ast.UnaryExpr:
		// R-95：`-<数值字面量>` = 正字面量的**取负写法**（闭集 = literalConditionValue
		// 一处定义）⇒ 与上面的 IntLit/FloatLit 分支**同一条路**：非提升局部折进
		// litVars（不建节点），提升变量的写物化为可寻址常量。缺这支 ⇒ 同一意思两种
		// 产物（`if (n > -2)` 1 个载体 vs `let k = -2; if (n > k)` 2 个载体，一手
		// 实测 2026-09-26）。操作数不是字面量（对 ref 取反的 `-m`、逻辑非 `!x`）
		// 不在闭集里 ⇒ 仍走普通值物化。
		if lv, ok := litVarOfScalarLiteral(in); ok {
			if addressable {
				ref := g.literalWriteRef(lv)
				g.varToOutput[s.Name()] = ref
				g.tailRef = &ref
				return []string{ref.nodeID}, "", nil
			}
			g.litVars[s.Name()] = lv
			return prevIDs, firstPort, nil
		}
		ref, uerr := g.emitExprCode(prevIDs, firstPort, false, in)
		if uerr != nil {
			return nil, firstPort, uerr
		}
		g.varToOutput[s.Name()] = ref
		g.tailRef = &ref
		return []string{ref.nodeID}, "", nil
	case *ast.ParallelForExpr:
		// 族5 S2-gate v2 的「同款状态分类」**不适用于并发循环**：`parallel for` 的
		// 迭代彼此独立（S1），体内外层写是**迭代局部、不逃逸**（S3）⇒ 没有循环状态
		// （stateVars=nil），与语句形态 `emitForStmt` 同一判据（D-L4(a)，2026-09-14
		// 用户拍板）。顺序值循环（*ast.ForExpr）保持 loop state 不动。
		ids, _, oref, ferr := g.emitLoopNode(in.LoopVars(), in.LoopSources(), in.Body().Stmts(), nil, prevIDs, firstPort, true, false, nil)
		if ferr != nil {
			return nil, firstPort, ferr
		}
		mode, ok := onErrorWire(g.caps, in.OnError())
		if !ok {
			return nil, firstPort, &UnsupportedError{Construct: "parallel for on_error " + in.OnError()}
		}
		g.markParallelLoop(oref.nodeID, in.Concurrency(), mode)
		g.varToOutput[s.Name()] = oref
		g.tailRef = &oref
		return ids, "", nil
	case *ast.ForExpr:
		// value-producing loop: for (x in items) { yield v } → Loop
		// whose output port carries the collected per-iteration values.
		// 族5 S2-gate v2：stateVars 同款分类（先前 nil → 提升态体内写静默丢写）。
		ids, _, oref, ferr := g.emitLoopNode(in.LoopVars(), in.LoopSources(), in.Body().Stmts(), nil, prevIDs, firstPort, true, s.IsMutable(), g.loopStateVars(in.Body().Stmts()))
		if ferr != nil {
			return nil, firstPort, ferr
		}
		g.varToOutput[s.Name()] = oref
		g.tailRef = &oref
		return ids, "", nil
	case *ast.ArrayLit:
		hoisted, herr := g.hoistUserCalls(in, prevIDs, firstPort)
		if herr != nil {
			return nil, firstPort, herr
		}
		id := g.nextID()
		node, aerr := g.buildArray(id, hoisted.(*ast.ArrayLit))
		if aerr != nil {
			return nil, firstPort, aerr
		}
		g.add(node)
		ref := varRef{nodeID: id, output: "result", kind: "code"}
		g.varToOutput[s.Name()] = ref
		g.tailRef = &ref
		return []string{id}, "", nil
	case *ast.MapLit:
		hoisted, herr := g.hoistUserCalls(in, prevIDs, firstPort)
		if herr != nil {
			return nil, firstPort, herr
		}
		id := g.nextID()
		node, merr := g.buildMap(id, hoisted.(*ast.MapLit))
		if merr != nil {
			return nil, firstPort, merr
		}
		g.add(node)
		// buildMap 产物是 G8 record shim（CodeRun，OutTypes={result: object}），
		// 整记录值端口 = result（与上方 ArrayLit→result 同构）；渲染器把
		// result.object 字段展开为顶层端口供字段访问（fieldPort kind code →
		// 字段名），整记录绑定（loop yield / merge 候选 / code 输入）必须指向
		// result。历史写 "output" 会撞上 code 节点平台默认 output:string 端口
		// （renderCode 兜底声明）：round2 把整记录 yield 绑到 output 字符串口
		// → 下游元素类型失真 / FIELD_NOT_FOUND（X16 整记录 batch yield 实证）。
		ref := varRef{nodeID: id, output: "result", kind: "code"}
		g.varToOutput[s.Name()] = ref
		g.tailRef = &ref
		return []string{id}, "", nil
	case *ast.OperationPolicyExpr:
		// retry/timeout/default policy on a call → node + ExceptionConfig.
		// 节点构造由 materializePolicyOperation 单点生产（值位置 `let x = …` 与
		// 语句位置 `…;` 共用同一实现，防两套形态枚举漂移）。
		id := g.nextID()
		kind, output, merr := g.materializePolicyOperation(id, in, prevIDs, firstPort)
		if merr != nil {
			return nil, firstPort, merr
		}
		ref := varRef{nodeID: id, output: output, kind: kind}
		g.varToOutput[s.Name()] = ref
		g.tailRef = &ref
		return []string{id}, "", nil
	}
	return nil, firstPort, &UnsupportedError{Construct: fmt.Sprintf("let %s = non-call expression", s.Name())}
}

// materializePolicyOperation builds the node for a policy-wrapped operation
// (`op(...) with retry/timeout/default`) and attaches its node-level policy.
// It is the single producer for BOTH carriers:
//
//   - value position:     `let x = op(...) with …;`  (emitVarDecl binds the var)
//   - statement position: `op(...) with …;`          (emitExprStmt drops the value)
//
// The carrier is the position, not the AST shape: the operation itself may be a
// qualified capability call, a built-in operation (llm/http), or a `foreign
// code` block (CodeRunner) — the judgement stays "expression form that can carry
// a node-level policy", never an AST type enumeration. Only the node + policy
// are produced here; binding a variable (varToOutput/tailRef) belongs to the
// value-position caller.
func (g *Builder) materializePolicyOperation(id string, in *ast.OperationPolicyExpr, prevIDs []string, firstPort string) (kind, output string, err error) {
	if fc, ok := in.Operation().(*ast.ForeignCodeExpr); ok {
		node, cerr := g.buildCode(id, fc)
		if cerr != nil {
			return "", "", cerr
		}
		setException(node, exceptionConfig(in.Policies(), false))
		g.add(node)
		cr, ok := node.(*CodeRun)
		if !ok {
			return "", "", &UnsupportedError{Construct: "operation policy on non-code"}
		}
		return "code", codeWholeValuePort(cr), nil
	}
	op, ok := in.Operation().(*ast.CallExpr)
	if !ok {
		return "", "", &UnsupportedError{Construct: "operation policy on non-call"}
	}
	var node Node
	if g.isQualifiedToolCall(op) {
		// Qualified capability call under an operation policy
		// (TOOL-DIRECT-CALL-FORM), e.g. `let x = p.op({...}) with retry(...)`
		// — same ToolCall as the qualified let form.
		tool, terr := g.buildTool(id, op)
		if terr != nil {
			return "", "", terr
		}
		node, kind = tool, "tool"
	} else {
		switch calleeName(op.Func()) {
		case "llm":
			lc, lerr := g.buildLLMCall(id, op, prevIDs, firstPort)
			if lerr != nil {
				return "", "", lerr
			}
			node, kind = lc, "llm"
		case "http":
			hc, herr := g.buildHTTP(id, op, prevIDs, firstPort)
			if herr != nil {
				return "", "", herr
			}
			node, kind = hc, "http"
		default:
			return "", "", &UnsupportedError{Construct: fmt.Sprintf("call %q with operation policy", calleeName(op.Func()))}
		}
	}
	setException(node, exceptionConfig(in.Policies(), false))
	g.applyLLMTimeout(node, op)
	g.add(node)
	return kind, bindPortOf(node, kind), nil
}

// buildLLMCall constructs the target-neutral LLM node shared by direct,
// policy-wrapped, and attempt operations. Control-flow-specific exception
// policy and timeout handling remain with each caller.
func (g *Builder) buildLLMCall(id string, call *ast.CallExpr, prevIDs []string, firstPort string) (*LLMCall, error) {
	// MODEL slot = constant config slot (same ruler as http() method): the
	// platform carries it as configuration (modelType enum + display name), so
	// a runtime expression has no carrier — a silent default would run the
	// workflow on a model the author never asked for.
	if len(call.Args()) == 0 {
		return nil, &UnsupportedError{Construct: "llm() requires a model argument (constant config slot)"}
	}
	model, merr := g.constantSlotString("llm() model", call.Args()[0])
	if merr != nil {
		return nil, merr
	}
	lc := &LLMCall{ID: id, Model: model}
	cfg, inputs, err := g.llmConfig(call, prevIDs, firstPort)
	if err != nil {
		return nil, err
	}
	lc.Config = cfg
	lc.Inputs = inputs
	if len(call.TypeArgs()) > 1 {
		return nil, &UnsupportedError{Construct: "llm expects at most one type argument"}
	}
	payload := ast.TypeRef(ast.NewPrimTypeRef("string"))
	if len(call.TypeArgs()) == 1 {
		payload = call.TypeArgs()[0]
	}
	schema, err := g.llmSchemaOf(payload)
	if err != nil {
		return nil, &UnsupportedError{Construct: err.Error()}
	}
	lc.Result = &LLMResultContract{
		PayloadSchema: schema,
		Metadata: LLMMetadataContract{
			ReasoningContent: true,
			Usage:            true,
			FinishReason:     true,
		},
		Validation: LLMValidationContract{
			Mode:         "strict-json",
			ClosedRecord: true,
			AttemptScope: "model-and-decode",
		},
	}
	lc.OutTypes = llmTextOutTypes(g.typeOf(payload))
	return lc, nil
}

// llmTextOutTypes —— LLM 节点的**结果端口表**，唯一键 `text`（= `LLMResponse.text` 的
// 语言类型；wire 上映射为物理口 `output`）。
//
// 为什么是一处函数而不是各处的 map 字面量：LLM 节点有**两条**构造路径 —— 通用
// `llm(...)` 表达式（本文件）与 stdlib 宏展开（`build_expr.go` 的 `emitRagAnswer` /
// `emitFetchAndSummarize`）。漏写 `OutTypes` 的一方会让 `portTypeOf(llm,"text")`
// **返回 nil**：端点名额 `input.type` 取不到值（CB-10 的回声位静默退回 bare）、
// 下游端口查询降级 —— 一手实证（2026-09-27）：`return std.v1.rag_answer(...)` 的
// 产物端点名额无 `type`，而同一语义的显式写法有 ⇒ `C∘R∘C` 一次往返即漂移
// （I1b `ARTIFACT_DRIFT/PARAM@2`）。「同一规则两处实现」的典型，故收成一处。
func llmTextOutTypes(payload *Type) map[string]*Type {
	return map[string]*Type{"text": payload}
}

func stringLLMResultContract() *LLMResultContract {
	return &LLMResultContract{
		PayloadSchema: llm.String(),
		Metadata: LLMMetadataContract{
			ReasoningContent: true,
			Usage:            true,
			FinishReason:     true,
		},
		Validation: LLMValidationContract{
			Mode:         "none",
			ClosedRecord: true,
			AttemptScope: "model-only",
		},
	}
}

func (g *Builder) markParallelLoop(id string, concurrency int, errorMode string) {
	if loop, ok := g.find(id).(*Loop); ok {
		loop.IsParallel = true
		loop.ParallelNums = concurrency
		loop.ErrorHandleMode = errorMode
	}
	block := g.curBlock()
	if block == nil || len(block.Stmts) == 0 {
		return
	}
	if stmt, ok := block.Stmts[len(block.Stmts)-1].(*LoopStmt); ok && stmt.ID == id {
		stmt.IsParallel = true
		stmt.ParallelNums = concurrency
		stmt.ErrorHandleMode = errorMode
	}
}

// setException attaches an ExceptionConfig to a node with exception support.
func setException(nd Node, ec *ExceptionConfig) {
	switch n := nd.(type) {
	case *LLMCall:
		n.Exception = ec
	case *HTTPCall:
		n.Exception = ec
	case *ToolCall:
		n.Exception = ec
	case *CodeRun:
		// `foreign code ... with retry/timeout/default`：CodeRunner 是带
		// settingOnError 的可失败副作用节点（与前缀插件同族的结构事实）。
		// Synthetic shim 由编译器自建、不带策略（渲染端不会去读它的
		// Exception）。
		n.Exception = ec
	}
}

func exceptionConfig(policies []ast.OperationPolicy, failBranch bool) *ExceptionConfig {
	ec := &ExceptionConfig{FailBranch: failBranch}
	for _, policy := range policies {
		switch p := policy.(type) {
		case *ast.RetryPolicy:
			ec.MaxRetry = p.MaxRetries()
			ec.RetryIntervalMS = p.IntervalMs()
		case *ast.TimeoutPolicy:
			ec.TimeoutMS = p.TimeoutMs()
		case *ast.DefaultPolicy:
			processType := 2
			ec.ProcessType = &processType
			ec.DefaultFields = defaultPayloadFields(p.Value())
		}
	}
	return ec
}

// defaultPayloadFields 把 `with default(<记录字面量>)` 的失败缺省载荷转成 IR
// 字段表（保持源码顺序 = 渲染确定）。值必须是静态字面量：非字面量（变量、
// 表达式、嵌套 ref）返回 nil——策略存在而载荷不可静态承载，后端必须 fail
// loud（这里绝不伪造零值兜底）。
func defaultPayloadFields(expr ast.Expr) []DefaultField {
	m, ok := expr.(*ast.MapLit)
	if !ok {
		return nil
	}
	fields := make([]DefaultField, 0, len(m.Entries()))
	for _, e := range m.Entries() {
		name, ok := staticMapKeyName(e.Key)
		if !ok {
			return nil
		}
		value := literalValueTree(e.Value)
		if value == nil {
			return nil
		}
		fields = append(fields, DefaultField{Name: name, Value: derefNullTree(value)})
	}
	return fields
}

// llmTimeoutMS reads the optional "timeout_ms" option from an llm() call's
// options map. Commercial LLM nodes carry their request timeout in
// settingOnError.timeoutMs (real exports: 180000 or 600000); the corpus
// expresses it as an llm() option.
func llmTimeoutMS(call *ast.CallExpr) (int, bool) {
	if len(call.Args()) < 2 {
		return 0, false
	}
	m, ok := call.Args()[1].(*ast.MapLit)
	if !ok {
		return 0, false
	}
	for _, e := range m.Entries() {
		key, ok := staticMapKeyName(e.Key)
		if !ok || key != "timeout_ms" {
			continue
		}
		switch v := literalValue(e.Value).(type) {
		case int:
			if v > 0 {
				return v, true
			}
		case int64:
			if v > 0 {
				return int(v), true
			}
		case float64:
			if v > 0 {
				return int(v), true
			}
		}
	}
	return 0, false
}

// applyLLMTimeout attaches an llm() "timeout_ms" option to the lowered
// node's ExceptionConfig (run after setException so the explicit option
// wins over an attempt/let policy's TimeoutMS).
func (g *Builder) applyLLMTimeout(node Node, call *ast.CallExpr) {
	lc, ok := node.(*LLMCall)
	if !ok {
		return
	}
	if ms, ok := llmTimeoutMS(call); ok {
		if lc.Exception == nil {
			lc.Exception = &ExceptionConfig{}
		}
		lc.Exception.TimeoutMS = ms
	}
}

// httpURL extracts the http(...) url argument (the method slot is resolved by
// resolveHTTPMethod).
func httpURL(call *ast.CallExpr) ast.Expr {
	if len(call.Args()) > 1 {
		return call.Args()[1]
	}
	return nil
}

// buildHTTP builds the target-neutral HTTP node. Both configuration slots go
// through the shared slot rules (P12): the URL is a TEMPLATE slot (any string
// expression the language admits is rendered into the platform template —
// literals verbatim, references as {{block_output_<id>.<port>}}, nested
// operations materialized as producers first), while the METHOD is a CONSTANT
// slot (the platform method field is not a template, so anything that is not a
// compile-time string is a compile error rather than a silent "GET").
func (g *Builder) buildHTTP(id string, call *ast.CallExpr, prevIDs []string, firstPort string) (*HTTPCall, error) {
	method, err := g.resolveHTTPMethod(call)
	if err != nil {
		return nil, err
	}
	url, err := g.resolveHTTPTemplate(httpURL(call), prevIDs, firstPort, "url")
	if err != nil {
		return nil, err
	}
	node := &HTTPCall{ID: id, Method: method, URL: url}
	if len(call.Args()) > 2 {
		seen := map[string]bool{}
		opts, ok := call.Args()[2].(*ast.MapLit)
		if ok {
			// The "body" key carries a RAW_TEXT template whose ${expr}
			// placeholders are emitted as {{block_output_<id>.<port>}}
			// and registered as input sources; "headers" and "params"
			// carry their name→value maps the same way. **All three are
			// template slots (P12).**
			//
			// A key outside that vocabulary is a hard error, never a
			// silent no-op: the old code fell through to the generic ref
			// collector, whose Sources land in HTTPCall.Inputs — a
			// carrier only the reference renderer reads (its `variables` list).
			// The coze targets synthesize http node_inputs from the
			// template placeholders instead, so on those targets the
			// entry AND its data reference simply vanished while the
			// compile reported SUCCEEDED (零诊断, C2-H3 一手实测).
			//
			// Carrying an undeclared key instead is not an option either:
			// the language's http() option vocabulary is a language
			// decision. `params` joined it on 2026-10-22 (user ruling, PRD
			// v1.8 P2): the wire shape comes from platform code, and the
			// corpus has zero non-empty `parameters.params` instances — a
			// boundary recorded in ir.HTTPConfigKeys's doc and ES-115.
			for _, entry := range opts.Entries() {
				key, ok := staticMapKeyName(entry.Key)
				if !ok {
					return nil, dynamicMapKeyError("http option", entry.Key)
				}
				switch key {
				case "body":
					text, berr := g.httpConfigValue(entry.Value, &node.Inputs, seen, "body")
					if berr != nil {
						return nil, berr
					}
					node.Body += text
				case "headers":
					if herr := g.httpHeaders(entry.Value, node, seen); herr != nil {
						return nil, herr
					}
				case "params":
					if perr := g.httpParams(entry.Value, node, seen); perr != nil {
						return nil, perr
					}
				default:
					return nil, &UnsupportedError{Construct: fmt.Sprintf(
						"http option %q: http() config accepts %s; an option outside that vocabulary would be dropped silently, so it is rejected instead (declare it in the language first)",
						key, strings.Join(HTTPConfigKeys, ", "))}
				}
			}
		} else {
			// The config must be a map literal. A config carried through a
			// variable cannot be read statically, and the old fallback
			// collected its refs into HTTPCall.Inputs — a carrier only the
			// reference renderer reads ⇒ on the coze targets the whole config
			// (body AND headers) vanished with zero diagnostics. Same rule
			// as the value layer below: what we cannot carry must fail loud.
			return nil, &UnsupportedError{Construct: fmt.Sprintf(
				"http() config %s is not a map literal (the body/headers options are static platform keys; write the config inline)",
				exprLabel(call.Args()[2]))}
		}
	}
	return node, nil
}

// ── http() config values: one total template-slot rule ──────────────────
//
// Every http() config value is a **template slot** (P12): text with zero or
// more interpolated references. The slot admits exactly three renderings and
// no fourth —
//
//	· literal text          → verbatim;
//	· a reference, however spelled (`q` / `${q}` / a field path)
//	                        → {{block_output_<id>.<port>}} + its input source;
//	· any other expression  → materialized as a producer, then referenced —
//
// and whatever the slot cannot carry is a **compile error**, never a silent
// drop. The previous implementation enumerated expression shapes
// (`StringLit` | `TemplateString` | else ⇒ ref collection) and discarded the
// whole entry for every other shape: a naked value lost its header
// (`{"headers": {"X-A": q}}`), a naked body emptied the body (`{"body": q}`),
// a non-static header name was skipped, a non-map `headers` value vanished
// and a failed materialization was skipped — all with zero diagnostics and
// `SUCCEEDED` (C2-H8 一手实测). Shape enumeration is the recurrence
// mechanism, not the rule: the slot semantic is one, so its renderer must be
// total.

// httpConfigValue renders one config VALUE (see the block comment above).
func (g *Builder) httpConfigValue(expr ast.Expr, inputs *[]Source, seen map[string]bool, slot string) (string, error) {
	if lit, ok := expr.(*ast.StringLit); ok {
		return lit.Value(), nil
	}
	if ts, ok := expr.(*ast.TemplateString); ok {
		var sb strings.Builder
		for _, part := range ts.Parts() {
			if part.Expr == nil {
				sb.WriteString(part.Text)
				continue
			}
			ph, err := g.httpConfigRef(part.Expr, inputs, seen, slot)
			if err != nil {
				return "", err
			}
			sb.WriteString(ph)
		}
		return sb.String(), nil
	}
	return g.httpConfigRef(expr, inputs, seen, slot)
}

// httpConfigRef renders one referenced value inside a config template slot.
// A naked value (`{"body": q}`) and an interpolated one (`{"body": "${q}"}`)
// are the *same* data dependency, so they take the same path and produce the
// same placeholder and the same input source — that equality is the rule, not
// a coincidence to be special-cased.
func (g *Builder) httpConfigRef(expr ast.Expr, inputs *[]Source, seen map[string]bool, slot string) (string, error) {
	if ref, ok := g.resolveValueRef(expr); ok {
		g.noteHTTPInput(ref, inputs, seen)
		return fmt.Sprintf("{{block_output_%s.%s}}", ref.nodeID, ref.output), nil
	}
	// Not a bound port: materialize the expression as a producer, then
	// reference its single output — the same "produce then reference" rule
	// the url slot uses (resolveHTTPTemplate).
	ref, err := g.emitValueExpr(nil, "", false, expr)
	if err != nil {
		// A value the slot cannot carry is a build error; writing the entry
		// away silently is the defect this family removes.
		return "", &UnsupportedError{Construct: fmt.Sprintf(
			"http() %s value %s is not a usable string value (assign it to a variable first, or wrap it in a `foreign code` block)",
			slot, exprLabel(expr))}
	}
	g.noteHTTPInput(ref, inputs, seen)
	return fmt.Sprintf("{{block_output_%s.%s}}", ref.nodeID, ref.output), nil
}

// noteHTTPInput registers the (producer, port) pair a placeholder depends on.
// HTTPCall.Inputs is the reference renderer's `variables` carrier; the coze
// renderers synthesize node_inputs from the placeholders themselves, so this
// registration is what keeps both targets reading the same dependency.
func (g *Builder) noteHTTPInput(ref varRef, inputs *[]Source, seen map[string]bool) {
	if inputs == nil || seen == nil {
		return
	}
	key := ref.nodeID + "\x00" + ref.output
	if seen[key] {
		return
	}
	seen[key] = true
	*inputs = append(*inputs, Source{BlockID: ref.nodeID, Name: ref.output})
}

// httpPairs renders a `{name: value}` config map into ordered (name, template)
// pairs. Both request channels the platform reads share this one shape — the
// static key is the wire name, the value is a template slot (httpConfigValue).
// A non-static name is the fail-loud diagnostic (dynamicMapKeyError), never a
// silently skipped entry.
func (g *Builder) httpPairs(expr ast.Expr, inputs *[]Source, seen map[string]bool, channel, item string) ([][2]string, error) {
	m, ok := expr.(*ast.MapLit)
	if !ok {
		return nil, &UnsupportedError{Construct: fmt.Sprintf(
			"http() %s value %s is not a map literal (the platform %s list is a static {name: value} map)",
			channel, exprLabel(expr), channel)}
	}
	out := make([][2]string, 0, len(m.Entries()))
	for _, entry := range m.Entries() {
		name, ok := staticMapKeyName(entry.Key)
		if !ok {
			return nil, dynamicMapKeyError("http "+item+" name", entry.Key)
		}
		if name == "" {
			return nil, &UnsupportedError{Construct: "http " + item + " name must not be empty"}
		}
		value, verr := g.httpConfigValue(entry.Value, inputs, seen, item+" "+strconv.Quote(name))
		if verr != nil {
			return nil, verr
		}
		out = append(out, [2]string{name, value})
	}
	return out, nil
}

// httpHeaders renders the `headers` config value.
func (g *Builder) httpHeaders(expr ast.Expr, node *HTTPCall, seen map[string]bool) error {
	pairs, err := g.httpPairs(expr, &node.Inputs, seen, "headers", "header")
	if err != nil {
		return err
	}
	for _, p := range pairs {
		node.Headers = append(node.Headers, HTTPHeader{Name: p[0], Value: p[1]})
	}
	return nil
}

// httpParams renders the `params` config value (query parameters). Order is
// the author's order; the runtime appends each entry to the URL query only when
// the rendered URL does not already carry that key (same canonical rule the
// decompiler uses when the platform kept a query in the URL template).
func (g *Builder) httpParams(expr ast.Expr, node *HTTPCall, seen map[string]bool) error {
	pairs, err := g.httpPairs(expr, &node.Inputs, seen, "params", "param")
	if err != nil {
		return err
	}
	for _, p := range pairs {
		node.Params = append(node.Params, HTTPParam{Name: p[0], Value: p[1]})
	}
	return nil
}

// buildSubWorkflow lowers a subflow declaration call <name>({...}) to a
// SubWorkflowCall. The identity (workflowId/version/spaceId) comes from the
// declaration; the input binding map literal resolves each entry to a live
// dataflow ref (or keeps a literal value); OutTypes mirror the declared
// output contract fields for target rendering (node_outputs in coze-biz,
// outputs ports in coze-studio).
func (g *Builder) buildSubWorkflow(id string, call *ast.CallExpr, decl *ast.SubflowDecl, prevIDs []string, firstPort string) (*SubWorkflowCall, error) {
	sc := &SubWorkflowCall{
		ID:              id,
		WorkflowID:      decl.WorkflowID(),
		WorkflowVersion: decl.WorkflowVersion(),
		SpaceID:         decl.SpaceID(),
		OutTypes:        map[string]*Type{},
	}
	for _, f := range decl.OutputFields() {
		sc.OutTypes[f.Name()] = g.typeOf(f.TypeRef())
	}
	// Declared input contract (names + types as written) — the output side's
	// mirror. The product publishes this; the call-site bindings only fill it.
	// A declared field with an unresolvable type is a compiler-internal failure
	// (the declaration grammar requires a type), and it must NOT be papered
	// over: forging a type here is exactly the D-2 defect (the forged type is
	// read back by raising). Fail loud instead.
	for _, f := range decl.InputFields() {
		ft := g.typeOf(f.TypeRef())
		if ft == nil {
			return nil, &UnsupportedError{Construct: fmt.Sprintf(
				"subflow %s input %q: declared type did not resolve — the declaration is the input contract's authority; refusing to forge a type",
				decl.Name(), f.Name())}
		}
		sc.InFields = append(sc.InFields, SubflowField{Name: f.Name(), Type: ft})
	}
	// Input bindings: each map literal entry becomes a Source (ref or
	// literal), mirroring collectToolParams semantics.
	for _, a := range call.Args() {
		ml, ok := a.(*ast.MapLit)
		if !ok {
			continue
		}
		for _, e := range ml.Entries() {
			key, ok := staticMapKeyName(e.Key)
			if !ok {
				return nil, dynamicMapKeyError("subflow input", e.Key)
			}
			switch e.Value.(type) {
			case *ast.IdentExpr, *ast.FieldAccessExpr, *ast.CallExpr:
				if ref, ok := g.resolveValueRef(e.Value); ok {
					sc.Inputs = append(sc.Inputs, Source{Path: []string{key}, BlockID: ref.nodeID, Name: ref.output})
					continue
				}
				// 字面量局部的引用（`let m = 0; sub({p: m})`）：litVars 里就是这个
				// **常量**，绑定直接收字面量 ⇒ 就地内联（不物化载体）。
				if id, ok := e.Value.(*ast.IdentExpr); ok {
					if lv, isLit := g.litVars[id.Name()]; isLit {
						sc.Inputs = append(sc.Inputs, Source{Path: []string{key}, Val: litVarNative(lv)})
						continue
					}
				}
			}
			if v := literalValue(e.Value); v != nil {
				sc.Inputs = append(sc.Inputs, Source{Path: []string{key}, Val: v})
				continue
			}
			if v := literalValueTree(e.Value); v != nil {
				sc.Inputs = append(sc.Inputs, Source{Path: []string{key}, Val: derefNullTree(v)})
				continue
			}
			return nil, &UnsupportedError{Construct: fmt.Sprintf(
				"subflow input %q: value must be a variable reference or a literal (assign complex values to a variable first)", key)}
		}
	}
	return sc, nil
}

// litVarNative 把 litVar 的**线文本 + 类型**还原为原生值 —— ToolParam.Value /
// Source.Val 要的是原生值（int64 / float64 / bool / string），不是格式化文本。
// litVar.value 由本仓自己的格式化器产出（`strconv.FormatInt` / `pyast.FloatLiteralText`），
// 故还原是全函数；解析意外失败时退回文本（不 panic、不谎报类型）。
func litVarNative(lv litVar) any {
	switch lv.typ {
	case "integer":
		if i, err := strconv.ParseInt(lv.value, 10, 64); err == nil {
			return i
		}
	case "number":
		if f, err := strconv.ParseFloat(lv.value, 64); err == nil {
			return f
		}
	case "boolean":
		return lv.value == "true"
	}
	return lv.value
}

// litVarOfScalarLiteral 把「一个**字面量表达式**」折成 litVar（不建节点）。
// 这个 `litVar` 同时是：(a) 值位折叠的载体（进 `litVars`）；(b) 常量 producer
// （`literalWriteRef` → `emitLitConstShim`）的**类型与正文**的唯一来源
// （F1：提升变量字面量写的可寻址化，与 litVar.typ 同一把尺子 —— 不再另判一次
// 类型/拼写）。
// 闭集 = `literalConditionValue`（`"x"` / `1` / `1.5` / `true` / `-2` / `-1.5`
// 的**唯一**定义，见 build_return.go）：解析器把 `-2` 读成 UnaryExpr 套字面量，
// 它与 `2` 是**同一个值的两种写法** ⇒ 必须折进同一张表（R-95：缺这支 ⇒
// `if (n > -2)` 1 个载体 vs `let k = -2; if (n > k)` 2 个载体 = 同一意思两种产物）。
// 返回 false = 该表达式不是槽可装的标量字面量（如对 ref 取反的 `-m`）。
func litVarOfScalarLiteral(e ast.Expr) (litVar, bool) {
	v, ok := literalConditionValue(e)
	if !ok {
		return litVar{}, false
	}
	switch x := v.(type) {
	case string:
		return litVar{value: x, typ: "string"}, true
	case bool:
		if x {
			return litVar{value: "true", typ: "boolean"}, true
		}
		return litVar{value: "false", typ: "boolean"}, true
	case int64:
		return litVar{value: strconv.FormatInt(x, 10), typ: "integer"}, true
	case float64:
		return litVar{value: pyast.FloatLiteralText(x), typ: "number"}, true
	}
	return litVar{}, false
}

// literalWriteRef 把一次「提升变量的字面量写」物化成可寻址的常量 producer
// （F1）。操作数有两种来源，规则同一：
//
//	① 就地字面量        `g = 0`            → 由该字面量建 shim；
//	② 字面量局部引用    `let v1 = 7; g = v1;` → v1 自身没有 producer 地址
//	   （litVars 无节点），在此按 v1 的字面量值建 shim。
//
// ② 不是特例而是同一规则的闭包：提升变量的写必须可寻址 ⇒ 它的操作数也必须
// 可寻址。string 侧早已如此（`let v = ""` 物化常量 shim，见 emitTextConst），
// 这里把 int/float/bool 拉齐（S2-gate D1：不对称正是静默丢失的出生处）。
func (g *Builder) literalWriteRef(lv litVar) varRef {
	// 入参是**折叠值**（`litVarOfScalarLiteral`，与值位折叠同一处定义），不是原始
	// 表达式 —— 正文由**值**给出，于是同一个值只有一种正文，与「字面量局部引用」
	// 那条路（`let k = -2; int v = k;`，同一构造器）逐字节同源。
	//
	// 旧实现把表达式交给通用渲染器：`int v = -2;` 的正文写成 `(-2)`（表达式的
	// 括号），而别名路写 `-2` ⇒ 同一个值两种拼写 ⇒ 两条拼写各自都能收敛，但 c0
	// 不同（R-97 一手 2026-09-26：别名路上 `I1b ARTIFACT_DRIFT/PARAM`；闭集唯一
	// 实现 = `literalConditionValue`，负号那一档必须同解）。
	//
	// 零值标注由 `emitLitConstShim` 一并给出（同一条政策：只有字面量本身就是该
	// 类型零值时才标；缺它 = 标量零值写（`failed = false;` / `n = 0;`）的载体拿不到
	// ZeroOf ⇒ 下游零候选重定向与臂门控零豁免全失效）。
	return g.emitLitConstShim(lv)
}

// emitLitConstShim 物化一个字面量常量 shim（无输入、结果口即该字面量）。
// shim 反向（shim_reverse.mapShimExpr）把 IntExpr/FloatExpr/BoolExpr 精确还原
// 成字面量 ⇒ `compile∘reverse` 字节闭合，多轮不膨胀。
func (g *Builder) emitLitConstShim(lv litVar) varRef {
	var expr pyast.Expr
	var out *Type
	switch lv.typ {
	case "integer":
		v, _ := strconv.ParseInt(lv.value, 10, 64)
		expr, out = &pyast.IntExpr{Value: v}, T("integer")
	case "number":
		v, _ := strconv.ParseFloat(lv.value, 64)
		expr, out = &pyast.FloatExpr{Value: v}, T("number")
	case "boolean":
		expr, out = &pyast.BoolExpr{Value: lv.value == "true"}, T("boolean")
	default:
		expr, out = &pyast.StringExpr{Value: lv.value}, T("string")
	}
	id := g.nextID()
	node := &CodeRun{
		ID:        id,
		Code:      wrapShim(nil, expr),
		Language:  "python",
		OutTypes:  map[string]*Type{"result": out},
		OutOrder:  []string{"result"},
		Synthetic: true,
	}
	// 零值标注（IR 优化阶段的输入，见 fold_zero_identity_candidates.go）：
	// 只有字面量本身**就是**该类型零值时才标；非零字面量是真实值，标注即撒谎。
	switch out.Kind {
	case "integer", "number":
		node.ZeroOf = out
		if v, err := strconv.ParseFloat(lv.value, 64); err != nil || v != 0 {
			node.ZeroOf = nil
		}
	case "boolean":
		node.ZeroOf = out
		if lv.value != "false" {
			node.ZeroOf = nil
		}
	default:
		node.ZeroOf = out
		if lv.value != "" {
			node.ZeroOf = nil
		}
	}
	g.add(node)
	return varRef{nodeID: id, output: "result", kind: "code"}
}
