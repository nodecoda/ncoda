// IR builder — 按语句/节点族拆分的文件之一（Tier-2 review #9:拆文件不拆包,
// diff 局部化）。同一包内符号共享;本文件族的归属见各函数注释。
package ir

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
)

// emitParallel lowers a parallel statement: every branch fans out from the
// previous junction and runs concurrently; the junction is the collection
// of all branch ends (no merge node is inserted).
func (g *Builder) emitParallel(stmt *ast.ParallelStmt, prevIDs []string, firstPort string) ([]string, string, error) {
	outer := g.varToOutput
	junction := []string{}
	treeBranches := []*Block{}
	for _, blk := range stmt.Branches() {
		port := firstPort
		g.branchDepth++
		g.parallelDepth++
		g.varToOutput = cloneRefs(outer)
		branchBlock := &Block{}
		g.blockStack = append(g.blockStack, branchBlock)
		bj, _, err := g.emitStmts(blk.Stmts(), prevIDs, port)
		for k, v := range g.varToOutput {
			outer[k] = v
		}
		g.varToOutput = outer
		g.branchDepth--
		g.parallelDepth--
		if err != nil {
			g.blockStack = g.blockStack[:len(g.blockStack)-1]
			return nil, firstPort, err
		}
		g.flushTreeNodes()
		g.blockStack = g.blockStack[:len(g.blockStack)-1]
		treeBranches = append(treeBranches, branchBlock)
		junction = append(junction, bj...)
	}
	g.appendStmt(&ParallelStmt{Branches: treeBranches})
	return junction, firstPort, nil
}

// emitAssign lowers an assignment to a conversation variable into a
// VariableAssigner; loop-state assignment uses VariableAssignerWithinLoop.
func (g *Builder) emitAssign(s *ast.AssignStmt, prevIDs []string, firstPort string) ([]string, string, error) {
	target, ok := s.Target().(*ast.IdentExpr)
	if !ok {
		return nil, firstPort, &UnsupportedError{Construct: "assign to non-identifier"}
	}
	if len(g.loopStates) > 0 && g.loopStates[len(g.loopStates)-1][target.Name()] {
		return g.emitLoopStateAssign(target.Name(), s.Value(), prevIDs, firstPort)
	}
	if _, isConv := g.convVars[target.Name()]; isConv {
		src := Source{Path: []string{target.Name()}}
		op := s.Operation()
		value := s.Value()
		// 复合赋值的中性形 = **(op, 右值)**：前端把 `v += e` desugar 成 `v + e`
		// （`pkg/parser/stmt.go`），左操作数是**读回 v** 的产物 ⇒ 这里把它
		// 拆掉、只留右值 + op。**每个后端自己决定这个 op 轴能不能承**：
		//   · dify/Graphon 有 op 轴（`internal/backend/dify/（商业仓）render_nodes.go` 的
		//     `Operation: assignerOperation(n.Operation)`）⇒ 直接承载；
		//   · coze-biz 的 assigner 是**纯 set**（一手：coze-studio
		//     `internal/nodes/variableassigner/variable_assign.go` 的 `Invoke`
		//     = 一句 `appVS.Set(...)`）⇒ 后端 fail loud（见 `renderAssignNode`）。
		// 只对这四个 op 拆 —— 曾写成「任何 `*ast.BinaryExpr` 都取 `be.Right()`」，
		// 于是 **`set` 的真表达式**也被截断：`combined = a + b;` 落成 `combined ← b`
		// （`+ a` 静默消失，编译 SUCCEEDED、零诊断）。
		switch op {
		case "+=", "-=", "*=", "/=":
			if be, isBin := value.(*ast.BinaryExpr); isBin {
				value = be.Right()
			}
		}
		if ref, ok := g.resolveValueRef(value); ok {
			src.BlockID = ref.nodeID
			src.Name = ref.output
		} else if lit := literalValue(value); lit != nil {
			src.Val = lit
		} else {
			// 表达式右值：一律走值物化（与本地变量/循环状态同一把尺子）。
			// 解析不到（= 需要读回会话变量本身）⇒ 物化器 fail loud。
			ref, err := g.emitValueExpr(prevIDs, firstPort, false, value)
			if err != nil {
				return nil, firstPort, err
			}
			src.BlockID = ref.nodeID
			src.Name = ref.output
		}
		// id 在物化**之后**分配：产者编号必须小于消费者（与其它物化路径同序）。
		id := g.nextID()
		g.add(&Assign{ID: id, VarName: target.Name(), Value: src, Operation: op})
		return []string{id}, "", nil
	}
	// 提升变量（可变本地）：裸声明 `T v;` 与带初值声明 `T v = <init>;` 都在
	// emitVarDecl 登记 hoistedVars（语言判定两者等价，A 级 2026-09-19）⇒ 对它们
	// 的写走同一条可寻址物化路径。旧实现只认裸声明 ⇒ `T v = <init>;` 之后的写落
	// UnsupportedError「assign to local variable」，而前端一条域外判据（E1058）
	// 把这批程序提前拒掉 ⇒ 缺口不可见（2026-09-19 收口）。
	if g.hoistedVars[target.Name()] {
		// 冗余零值 record 赋值消除（X4 2-周期极限环破环，2026-09-04）：
		// 分支内给 hoisted record 赋与其当前零值绑定同构的全零 record
		// （如 else 分支 `r2 = {"Group1": ""}`，r2 从未被真实赋值）——
		// 值不变，跳过物化。否则该赋值成为分支尾值 → 跨 if record phi
		// 物化（独立零值 code + 对侧默认 relay），decompile 渲染成
		// "else 冗余赋值"文本，再编译再物化 → Y20↔Y22 交替无不动点。
		// 语义安全：绑定不变（保持 hoist 零值），后续读/聚合取同值。
		if ml, ok := s.Value().(*ast.MapLit); ok {
			if shape := g.hoistedTypes[target.Name()]; shape != nil &&
				g.currentBindingIsZeroRecord(target.Name(), shape) &&
				isZeroRecordLiteral(ml, shape) {
				return prevIDs, firstPort, nil
			}
		}
		// 分支变量提升：v = <expr> 按声明语义物化节点（与 let v = <expr>
		// 同链），赋值位置 = 原节点在画布分支内的位置；写是可寻址的（字面量
		// 写物化 producer，不折进 litVars —— F1 同一把尺子）。
		return g.emitVarDecl(ast.NewVarDeclStmt(target.Name(), nil, s.Value(), true), prevIDs, firstPort)
	}
	return nil, firstPort, &UnsupportedError{Construct: fmt.Sprintf("assign to local variable %q", target.Name())}
}

// convVarType maps a @conversation declaration type onto the neutral
// Type. Array declarations (array<string>) resolve their element type
// (typeOf's generic fallback would flatten them to string).
func (g *Builder) convVarType(t ast.TypeRef) *Type {
	if at, ok := t.(*ast.ArrayTypeRef); ok {
		return TList(g.typeOf(at.Element()))
	}
	return g.typeOf(t)
}

// convDefaultValue extracts the literal default of a @conversation
// declaration (scalar literals; arrays literalize recursively).
func convDefaultValue(expr ast.Expr) any {
	switch d := expr.(type) {
	case nil:
		return ""
	case *ast.StringLit:
		return d.Value()
	case *ast.IntLit:
		return d.Value()
	case *ast.FloatLit:
		return d.Value()
	case *ast.BoolLit:
		return d.Value()
	case *ast.ArrayLit:
		out := []any{}
		for _, e := range d.Elements() {
			out = append(out, convDefaultValue(e))
		}
		return out
	}
	return ""
}

// emitOutputStmt lowers output(<expr>) to an OutputEmitter: the value
// becomes the content template with refs inlined as
// {{block_output_<id>.<port>}} (intermediate message; non-terminating —
// model: docs/NCODA-OUTPUT-MODEL.md).
func (g *Builder) emitOutputStmt(value ast.Expr, streaming bool, prevIDs []string, firstPort string) ([]string, string, error) {
	// 作者显式名额表形态（`output template "<逐字文本>" using {…}`，R-67 甲）：
	// 与 `output(<expr>)` **不是同一语义的两种拼写**，而是同一节点的两种名额来源。
	if tr, ok := value.(*ast.TemplateValue); ok {
		return g.emitTemplateOutput(tr, streaming, prevIDs, firstPort)
	}
	tpl, terr := g.templateExpr(value, prevIDs, firstPort)
	if terr != nil {
		return nil, firstPort, terr
	}
	id := g.nextID()
	g.add(&Output{ID: id, Template: tpl, Streaming: streaming})
	// 设置 tailRef 指向本 Output 节点的 output 端口,让隐式 end 收渲染
	// 结果,而不是泄漏的旧 tailRef（M13 e2e 抓出的 bug——此前 end 错误地
	// 收了 conversation 历史端口）。
	//
	// 注意（2026-09-12 门禁接线复盘）：这条 tailRef 是**共享语义**，三个
	// target 共用——runtime 的 advanced-chat 记忆（chatflowRunnable 读
	// end 的 output 绑定当 answer）与 dify 的 Answer 投影都靠它。coze-biz
	// 承载不了这个引用（平台的 OutputEmitter 不声明任何输出口，语料
	// 275/275 个 output 节点无 node_outputs、398 个 end 输入 0 处引用它），
	// 该差异**在 coze-biz lowering 侧消化**（renderEndInputs 丢弃指向
	// Output 节点的绑定），不得反向修改这里的共享语义。
	g.tailRef = &varRef{nodeID: id, output: "output", kind: "output"}
	return []string{id}, "", nil
}

// emitTemplateOutput lowers `output template "<逐字文本>" using {…}` (R-67 甲):
// 中间消息的**逐字模板 + 作者名额表** —— 与答复端点（emitTemplateValue 的
// EndAnswer）**同一条承载路线**（literal 文本 + 按名字段生成 node_inputs），
// 差别只在它是**非终止**的普通节点：可以有多个，也不受流程出口位置约束
// （`output()` 的放置纪律在 response_contract 里，与形态无关）。
//
// 为什么必须有这条路（W2 §8.3 判定式的右半）：`output(<expr>)` 的名额由**文本
// 推导**（模板串里每个 `${}` 生成一个名字），结构上装不下三类名额 —— 行名带下标
// （`{{image[0]}}`）· 行名 ≠ 文本里的名字（`{{draft_url}}`）· 行**一次都不被文本
// 引用**（llm 位同形状 33 条一手：只能丢 + RS-TPL-002）。作者显式名额表是唯一能
// 装下它们的形态。
func (g *Builder) emitTemplateOutput(tr *ast.TemplateValue, streaming bool, prevIDs []string, firstPort string) ([]string, string, error) {
	bindings, err := g.resolveTemplateBindings(tr, "output template")
	if err != nil {
		return nil, firstPort, err
	}
	id := g.nextID()
	g.add(&Output{ID: id, Template: tr.Text(), Bindings: bindings, Streaming: streaming})
	// 与 output(<expr>) 同一条 tailRef 语义（见 emitOutputStmt 的长注）：隐式 end
	// 收的是本节点的 output 端口；coze-biz 的差异在 lowering 侧消化。
	g.tailRef = &varRef{nodeID: id, output: "output", kind: "output"}
	return []string{id}, "", nil
}

// templateExpr renders a value expression into an output template:
// literals verbatim, refs as {{block_output_<id>.<port>}}. Operation calls
// in the value are hoisted first (mirrors emitValueExpr): a hoisted call
// becomes a bound producer, so a scalar-array interpolation operand
// (split(...)[0]) later materializes as a Synthetic code shim referenced by
// the template instead of an unresolvable construct error.
func (g *Builder) templateExpr(expr ast.Expr, prevIDs []string, firstPort string) (string, error) {
	if _, isCall := expr.(*ast.CallExpr); !isCall {
		hoisted, herr := g.hoistUserCalls(expr, prevIDs, firstPort)
		if herr != nil {
			return "", herr
		}
		expr = hoisted
	}
	if ref, ok := g.resolveValueRef(expr); ok {
		return fmt.Sprintf("{{block_output_%s.%s}}", ref.nodeID, ref.output), nil
	}
	if id, ok := expr.(*ast.IdentExpr); ok {
		if _, isConv := g.convVars[id.Name()]; isConv {
			return fmt.Sprintf("{{global_variable_app.%s}}", id.Name()), nil
		}
	}
	switch e := expr.(type) {
	case *ast.StringLit:
		return e.Value(), nil
	case *ast.TemplateString:
		var sb strings.Builder
		for _, part := range e.Parts() {
			sb.WriteString(part.Text)
			if part.Expr == nil {
				continue
			}
			if ref, ok := g.resolveValueRef(part.Expr); ok {
				sb.WriteString(fmt.Sprintf("{{block_output_%s.%s}}", ref.nodeID, ref.output))
				continue
			}
			if lit, ok := stringValue(part.Expr); ok {
				sb.WriteString(lit)
				continue
			}
			// §5.3 P0 fallback（R-80，2026-09-25）：物化 = **值位置唯一入口** `emitValueExpr`
			// （它自挑载体：原生节点 / Synthetic shim）。此前直调 `emitExprCode`（只认封闭
			// shim 子集）⇒ 结构字面量在此红、而经绑定的同一语义绿；`split` / 三元也在
			// 裸写法与内插之间分叉（一手：模板里的三元第二轮 `UNDEFINED_VAR`）。
			ref, verr := g.emitValueExpr(prevIDs, firstPort, false, part.Expr)
			if verr != nil {
				return "", &UnsupportedError{Construct: fmt.Sprintf("template interpolation %T", part.Expr)}
			}
			sb.WriteString(fmt.Sprintf("{{block_output_%s.%s}}", ref.nodeID, ref.output))
		}
		return sb.String(), nil
	case *ast.BinaryExpr:
		if e.Op() == "+" {
			var sb strings.Builder
			if err := g.flattenTemplateConcat(e, &sb, prevIDs, firstPort); err != nil {
				return "", err
			}
			return sb.String(), nil
		}
	}
	// R-77（2026-09-25）：**裸写法**的兜底与内插分支**同源** —— 同一语义的两种拼写
	// 必须同产物，否则「一种红一种绿」就是 I1 破裂：
	//
	//	① 常量折叠：字面量（int/float/bool/string）⇒ 逐字文本（`output(3)` ⇒ content `3`）；
	//	② 否则走**值位置唯一入口** `emitValueExpr` 物化一个值载体 ⇒ 模板按块口引用它
	//	   （后端渲染成 `{{<名字>}}` + node_inputs 一行）。
	//	   入口由 R-80（同日）收敛：内插分支与这里现在调的是**同一个函数**，它自挑载体
	//	   （builtin / 算术 / 比较 ⇒ Synthetic shim；`split` ⇒ 原生 text 节点；
	//	   三元 ⇒ junction；结构字面量 ⇒ 记录 / 数组 shim），声明类型取类型结论，不伪造。
	//
	// 此前这里直接落 `output value %T` 通用兜底 ⇒ 语言合法（docs/NCODA-OUTPUT-MODEL.md §5
	// 明写表达式「必须可渲染为文本」）、平台承载面机器已有，却报成「平台不支持」——
	// 分类错，且与内插拼写的结果不一致（`output(trim(q))` 红 / `output(`` `x${trim(q)}` ``)` 绿）。
	if lit, ok := stringValue(expr); ok {
		return lit, nil
	}
	ref, verr := g.emitValueExpr(prevIDs, firstPort, false, expr)
	if verr != nil {
		return "", &UnsupportedError{Construct: fmt.Sprintf("output value %T", expr)}
	}
	return fmt.Sprintf("{{block_output_%s.%s}}", ref.nodeID, ref.output), nil
}

// flattenTemplateConcat flattens a `+` chain into an output template.
func (g *Builder) flattenTemplateConcat(e ast.Expr, sb *strings.Builder, prevIDs []string, firstPort string) error {
	switch x := e.(type) {
	case *ast.StringLit:
		sb.WriteString(x.Value())
		return nil
	case *ast.BinaryExpr:
		if x.Op() != "+" {
			return &UnsupportedError{Construct: fmt.Sprintf("binary op %q in concat", x.Op())}
		}
		if err := g.flattenTemplateConcat(x.Left(), sb, prevIDs, firstPort); err != nil {
			return err
		}
		return g.flattenTemplateConcat(x.Right(), sb, prevIDs, firstPort)
	case *ast.TemplateString:
		for _, part := range x.Parts() {
			sb.WriteString(part.Text)
			if part.Expr == nil {
				continue
			}
			if ref, ok := g.resolveValueRef(part.Expr); ok {
				sb.WriteString(fmt.Sprintf("{{block_output_%s.%s}}", ref.nodeID, ref.output))
				continue
			}
			if lit, ok := stringValue(part.Expr); ok {
				sb.WriteString(lit)
				continue
			}
			// §5.3 P0 fallback（R-80，2026-09-25）：物化 = **值位置唯一入口** `emitValueExpr`
			// （它自挑载体：原生节点 / Synthetic shim）。此前直调 `emitExprCode`（只认封闭
			// shim 子集）⇒ 结构字面量在此红、而经绑定的同一语义绿；`split` / 三元也在
			// 裸写法与内插之间分叉（一手：模板里的三元第二轮 `UNDEFINED_VAR`）。
			ref, verr := g.emitValueExpr(prevIDs, firstPort, false, part.Expr)
			if verr != nil {
				return &UnsupportedError{Construct: fmt.Sprintf("concat template interpolation %T", part.Expr)}
			}
			sb.WriteString(fmt.Sprintf("{{block_output_%s.%s}}", ref.nodeID, ref.output))
		}
		return nil
	default:
		if ref, ok := g.resolveValueRef(x); ok {
			sb.WriteString(fmt.Sprintf("{{block_output_%s.%s}}", ref.nodeID, ref.output))
			return nil
		}
		if lit, ok := stringValue(x); ok {
			sb.WriteString(lit)
			return nil
		}
		// §5.3 P0 fallback（R-80，2026-09-25）：物化 = **值位置唯一入口** `emitValueExpr`
		// （它自挑载体：原生节点 / Synthetic shim）。此前直调 `emitExprCode`（只认封闭
		// shim 子集）⇒ 结构字面量在此红、而经绑定的同一语义绿；`split` / 三元也在
		// 裸写法与内插之间分叉（一手：模板里的三元第二轮 `UNDEFINED_VAR`）。
		ref, verr := g.emitValueExpr(prevIDs, firstPort, false, x)
		if verr != nil {
			return &UnsupportedError{Construct: fmt.Sprintf("concat operand %T", x)}
		}
		sb.WriteString(fmt.Sprintf("{{block_output_%s.%s}}", ref.nodeID, ref.output))
		return nil
	}
}

// emitAttempt lowers an attempt statement: the operation node carries
// Exception.FailBranch=true, the success branch flows from the operation's
// normal output ("source"), the failure branch from its "fail-branch" port
// with the OperationError record bound ({error_message, error_type} →
// error_type/error_message semantic ports). Each branch terminates in its
// own End (legacy lowerAttempt renders two independent ends, e.g. end_4 and
// end_6 in attempt_recovery). Platform-neutral: the failure branch is the
// operation's error control flow; the backend maps it onto its own error
// strategy shape (dify "fail-branch"; Coze maps processType=3 and
// default/branch_error in its renderer).
func (g *Builder) emitAttempt(s *ast.AttemptStmt, prevIDs []string, firstPort string, lastInBlock bool) ([]string, string, error) {
	// flowExit: the attempt is the flow's tail statement, so its exit merge is
	// the flow's return value (see AttemptStmt.FlowExit). Inside a branch, a
	// loop body or an inlined function body the statement is not the flow's
	// tail — the enclosing structure owns the continuation.
	flowExit := lastInBlock && g.branchDepth == 0 && len(g.loopStack) == 0 &&
		g.branchValueDepth == 0 && g.inlineName == ""
	op := s.Operation()
	policies := []ast.OperationPolicy{}
	if wrapped, ok := op.(*ast.OperationPolicyExpr); ok {
		op = wrapped.Operation()
		policies = wrapped.Policies()
	}
	call, ok := op.(*ast.CallExpr)
	if !ok {
		return nil, firstPort, &UnsupportedError{Construct: fmt.Sprintf("attempt operation %T", op)}
	}
	id := g.nextID()
	var node Node
	var kind string
	if g.isQualifiedToolCall(call) {
		// Qualified capability call as an attempt operation
		// (TOOL-DIRECT-CALL-FORM), e.g. `attempt { p.op({...}) }` — same
		// ToolCall as the qualified let form.
		tool, terr := g.buildTool(id, call)
		if terr != nil {
			return nil, firstPort, terr
		}
		node = tool
		kind = "tool"
	} else {
		switch calleeName(call.Func()) {
		case "llm":
			lc, lerr := g.buildLLMCall(id, call, prevIDs, firstPort)
			if lerr != nil {
				return nil, firstPort, lerr
			}
			node = lc
			kind = "llm"
		case "http":
			hc, herr := g.buildHTTP(id, call, prevIDs, firstPort)
			if herr != nil {
				return nil, firstPort, herr
			}
			node = hc
			kind = "http"
		case "knowledge":
			kn, kerr := g.buildKnowledge(id, call, prevIDs, firstPort)
			if kerr != nil {
				return nil, firstPort, kerr
			}
			node = kn
			kind = "knowledge"
		case "split":
			tn, serr := g.buildSplit(id, call)
			if serr != nil {
				return nil, firstPort, serr
			}
			node = tn
			kind = "text"
		default:
			return nil, firstPort, &UnsupportedError{Construct: fmt.Sprintf("attempt operation %q", calleeName(call.Func()))}
		}
	}
	setException(node, exceptionConfig(policies, true))
	g.applyLLMTimeout(node, call)
	g.add(node)
	g.consumeTreeNodes(id)
	g.flushTreeNodes()

	// Junction planning state (mirrors the if/switch arm-junction path): the
	// arm-end variable views + their tail bindings let the exit aggregate
	// become a real phi when the arms WRITE hoisted variables instead of
	// returning a tail value (`success { msg = "ok"; }`). outerVars is
	// captured before the arms run and before the r/e bindings are added, so
	// the attempt's own bindings are never phi candidates.
	outerVars := cloneRefs(g.varToOutput)
	outerDeclared := cloneNames(g.hoistedVars)
	// junctionScope = 宿主**包围作用域对象**（本 junction 两臂的抬出 delta 都提交
	// 进它，跨臂 first-write-wins）；armBase = **臂入口作用域快照**（junction 入口，
	// 尚无任何臂抬出）。宿主绑定（`as r` / `failure(e)`）**按臂**叠加在 armBase 上：
	// 它们是本臂特有的绑定（互斥臂不可见，前端亦拒绝跨臂/越界引用），不进
	// junctionScope 也不进兄弟臂视图。
	junctionScope := g.varToOutput
	armBase := cloneRefs(junctionScope)
	arms := make([]junctionArm, 0, 2)

	// Success branch: result binding is the operation's normal output.
	resultName := ""
	if s.ResultBinding() != nil {
		resultName = s.ResultBinding().Name()
	}
	successBlock := &Block{}
	g.branchValueDepth++
	successTailBound := map[string]bool{}
	// 臂入口视图 = **junction 入口作用域快照**（armBase，含条件物化与宿主绑定）：
	// 臂的抬出 delta 只提交进 junctionScope（= 宿主包围作用域对象，跨臂
	// first-write-wins），**不得**进入本臂入口视图 —— 否则臂内**嵌套 junction**
	// 会把互斥兄弟臂的 producer 当成「junction 前绑定」（复制语义兜底候选）
	// ⇒ 跨互斥臂引用（P2 违规，L2 实测 2026-09-12）。一条规则、无宿主/臂数/
	// 形态守卫，四宿主同构。
	g.varToOutput = cloneRefs(armBase)
	if resultName != "" {
		// 宿主绑定（`as r`）与 let 初始化是**同一次调用的同一个值** ⇒ 必须走同一个
		// 构造函数（callBindRef）。曾在此处就地重写一遍 ⇒ 漏了 typ/整值字段登记
		// ⇒ 经 `as r` 的成员链吞容器前缀（`r.data` 后的 `.url` 投影成裸 `url`）。
		bind := g.callBindRef(id, node, kind, call)
		g.varToOutput[resultName] = bind
		tailBind := bind
		g.tailRef = &tailBind
	}
	successIDs, successRef, successHad, successProduced, _, serr := g.emitBranch(successBlock, s.SuccessBody(), id, "source", successTailBound, junctionScope)
	g.branchValueDepth--
	if serr != nil {
		return nil, firstPort, serr
	}
	arms = append(arms, junctionArm{
		Port:      "default",
		Block:     successBlock,
		Vars:      cloneRefs(g.lastBranchVars),
		TailBound: successTailBound,
	})
	// The success branch terminates in its own End, emitted right after its
	// branch tail (legacy: code_3 → end_4 before the failure branch).
	var tails []string
	var successEnd *End
	if successRef != nil {
		if flowExit {
			g.endCreated = true
		}
		endID := g.nextID()
		successEnd = &End{ID: endID, Inputs: []Ref{{Producer: successRef.nodeID, Output: successRef.output}}}
		g.add(successEnd)
		g.consumeTreeNodes(endID) // the branch terminal belongs to the AttemptStmt
		tails = append(tails, endID)
	} else if successHad {
		tails = append(tails, successIDs...)
	}

	// Failure branch: error binding is the OperationError record exposed
	// through the operation's error_type/error_message semantic ports.
	errName := ""
	if s.ErrorBinding() != nil {
		errName = s.ErrorBinding().Name()
	}
	failBlock := &Block{}
	g.branchValueDepth++
	failTailBound := map[string]bool{}
	// 臂入口视图 = **junction 入口作用域快照**（armBase，含条件物化与宿主绑定）：
	// 臂的抬出 delta 只提交进 junctionScope（= 宿主包围作用域对象，跨臂
	// first-write-wins），**不得**进入本臂入口视图 —— 否则臂内**嵌套 junction**
	// 会把互斥兄弟臂的 producer 当成「junction 前绑定」（复制语义兜底候选）
	// ⇒ 跨互斥臂引用（P2 违规，L2 实测 2026-09-12）。一条规则、无宿主/臂数/
	// 形态守卫，四宿主同构。
	g.varToOutput = cloneRefs(armBase)
	if errName != "" {
		g.varToOutput[errName] = varRef{nodeID: id, output: "error", kind: "attempt-error"}
		g.tailRef = &varRef{nodeID: id, output: "error", kind: "attempt-error"}
	}
	failIDs, failRef, failHad, failProduced, _, ferr := g.emitBranch(failBlock, s.FailureBody(), id, "fail-branch", failTailBound, junctionScope)
	g.branchValueDepth--
	if ferr != nil {
		return nil, firstPort, ferr
	}
	arms = append(arms, junctionArm{
		Port:      "branch_error",
		Block:     failBlock,
		Vars:      cloneRefs(g.lastBranchVars),
		TailBound: failTailBound,
	})
	// The failure branch terminates in its own End, emitted right after its
	// branch tail (legacy: code_5 → end_6). Both ends are explicit terminals
	// (endCreated) so the top-level build() tail does not synthesize the
	// canonical 900001 end on top of the attempt's own ends.
	var failEnd *End
	if failRef != nil {
		if flowExit {
			g.endCreated = true
		}
		endID := g.nextID()
		failEnd = &End{ID: endID, Inputs: []Ref{{Producer: failRef.nodeID, Output: failRef.output}}}
		g.add(failEnd)
		g.consumeTreeNodes(endID) // the branch terminal belongs to the AttemptStmt
		tails = append(tails, endID)
	} else if failHad {
		tails = append(tails, failIDs...)
	}
	// ── tree: the attempt owns the op and both branch terminals ────────
	consumeIDs := []string{id}
	if successEnd != nil {
		consumeIDs = append(consumeIDs, successEnd.ID)
	}
	if failEnd != nil {
		consumeIDs = append(consumeIDs, failEnd.ID)
	}
	g.consumeTreeNodes(consumeIDs...)
	g.flushTreeNodes()

	// Exit aggregate: the attempt's value = phi(arm values). Same rule as the
	// if/switch junction (one rule, arm-count agnostic): explicit arm tail
	// values become the candidates; when the arms write hoisted variables
	// instead, the per-variable phi plan supplies the groups and the
	// downstream references are redirected onto the aggregate. Without this
	// the statement's value stayed on whichever binding the last arm set
	// (`{op,"error"}`), leaking the error payload into the enclosing branch.
	// produced 是必须的（与 if/else 同一判据）：分支尾部只是**绑定**（臂内
	// 赋值 `success { msg = "ok"; }`）时 emitBranch 返回的是"回落到分支前尾值"
	// 的 ref，把它当候选会让汇聚收集到一个臂从未产出的端口，同时掐掉逐变量
	// phi 通道——臂内真 producer 成孤儿、下游读分支前零值绑定（N4b/Step3b 实测：
	// `attempt ... { success { msg = "ok"; } } return msg;` 的 End 读回
	// 初始 "" 而丢弃臂内写值）。只有**分支自己产出**尾值才算候选。
	var armTails []armTailValue
	if successRef != nil && successProduced {
		// 臂门控（arm_gate_candidate.go）：见 emitAttempt 头注 —— 四宿主同一条
		// 规则，臂尾值产在臂外时在该臂内物化恒等载体。候选按臂序收齐后统一
		// 门控（默认位 = 最后一条不门控）。
		armTails = append(armTails, armTailValue{arm: successBlock, v: *successRef})
	}
	if failRef != nil && failProduced {
		armTails = append(armTails, armTailValue{arm: failBlock, v: *failRef})
	}
	// φ 核心（唯一实现）：组装序列收在 assembleArmJunction（junction_phi.go），
	// 本宿主只送数据 —— 臂集合 / 臂尾值通道 / junction 前视图；尾值与重定向口径
	// 见该函数头注（「零候选 ⇒ 没有值」）。
	// 槽名留空：本宿主的槽名由提升变量规划给出（`plan.RefName` = 变量名），
	// 没有「junction 之后才绑上去的目标名」这种形态（那是值位置三元的 U6 面）。
	res, err := g.assembleArmJunction(arms, outerDeclared, outerVars, armTails, "")
	if err != nil {
		return nil, "", err
	}
	g.consumeTreeNodes(res.Primary.ID)
	g.flushTreeNodes()
	g.appendStmt(&AttemptStmt{ID: id, Op: node, Success: successBlock, Failure: failBlock, SuccessEnd: successEnd, FailureEnd: failEnd, FlowExit: flowExit, Exit: res.Primary})
	g.appendChainedArmJunctions(res)
	return tails, "", nil
}

// ── loop lowering (ana2 _emit_for / _emit_while / _emit_subgraph) ────────

// loopStateVars classifies which assigned variables inside a loop body are
// loop-state variables: assignments to variables that already carry a value
// (literal-bound or producer-bound). Such writes are loop state — each
// iteration assigns a fresh value that later iterations read. 值循环与语句
// for/while 共用同一定义（族5 S2-gate v2：ForExpr 先前漏传 stateVars=nil →
// 提升态体内写静默丢写）。
func (g *Builder) loopStateVars(stmts []ast.Stmt) map[string]bool {
	assigned := map[string]bool{}
	collectAssignedVars(stmts, assigned)
	stateVars := map[string]bool{}
	for v := range assigned {
		if _, ok := g.litVars[v]; ok {
			stateVars[v] = true
		} else if _, ok := g.varToOutput[v]; ok {
			stateVars[v] = true
		}
	}
	return stateVars
}

// emitForStmt lowers a for statement into a Loop container. A
// statement-level parallel for ("parallel for (...)" with concurrency/
// on_error) marks the container IsParallel so the coze-biz backend renders
// a commercial batch node instead of a serial loop.
func (g *Builder) emitForStmt(s *ast.ForStmt, prevIDs []string, firstPort string) ([]string, string, error) {
	stateVars := g.loopStateVars(s.Body().Stmts())
	if s.IsParallel() {
		// ``parallel for`` = 迭代彼此独立（S1）：迭代之间**不存在**「每迭代写、
		// 后继迭代读」的状态通道，因此语言层面就没有循环状态可言 —— 体内对外层
		// 名字的写是**迭代局部、不逃逸**（S3，D-L1(a)/D-L4(a)，2026-09-14 用户
		// 拍板）。这里传 nil 就是该语义事实的表达：写退回普通体内绑定（写后读读
		// 本迭代值），而不是被编码成平台 loop 的 intermediate store
		// （`LoopState/LoopID` → 渲染成 loop 专用的 `variableParameters` +
		// `variable_assign`）。
		//
		// 平台一手证据：真实导出 463 个 batch 容器 **0 例** variableParameters /
		// 0 例体内 set-variable；`InitIntermediateVars` 只由 loop 调用
		// （batch.go 的 `intermediate` 0 命中）⇒ batch 根本没有该通道，照抄 loop
		// 形状只会产出平台拒收的产物（最小例：死写被物化 ⇒ 反编译
		// `loop variable assigner destination is not parent_intermediate`）。
		stateVars = nil
	}
	ids, port, oref, err := g.emitLoopNode(s.LoopVars(), s.LoopSources(), s.Body().Stmts(), nil, prevIDs, firstPort, false, false, stateVars)

	if err != nil {
		return nil, "", err
	}
	if s.IsParallel() {
		mode, ok := onErrorWire(g.caps, s.OnError())
		if !ok {
			return nil, "", &UnsupportedError{Construct: "parallel for on_error " + s.OnError()}
		}
		g.markParallelLoop(oref.nodeID, s.Concurrency(), mode)
	}
	return ids, port, err
}

// emitWhileStmt lowers a while statement. Coze has no while condition; the
// loop is projected as by_iteration capped at limit with a top guard:
//
//	if (cond) { body } else { break }
func (g *Builder) emitWhileStmt(s *ast.WhileStmt, prevIDs []string, firstPort string) ([]string, string, error) {
	guard := ast.NewIfStmt(s.Condition(), s.Body(), nil, ast.NewBlock([]ast.Stmt{ast.NewBreakStmt()}))
	stateVars := g.loopStateVars([]ast.Stmt{guard})
	count := s.Limit()
	ids, _, _, err := g.emitLoopNode(nil, nil, []ast.Stmt{guard}, &count, prevIDs, firstPort, false, false, stateVars)
	return ids, "", err
}

// emitLoopNode is the shared for/while lowering: it builds the Loop
// container and its structured body, then records the loop output semantics.
// varNames/iterables 单源时各 len 1；zip 多源（for (a, b in zip(x, y))）
// 逐源建 Input：zip 的变量名即端口名（InputArrays/Inputs.Path），正编译
// 还原 batch 多 InputSources 的输入字段名（roundtrip 字段名保真）。
func (g *Builder) emitLoopNode(varNames []string, iterables []ast.Expr, body []ast.Stmt, iterCount *int, prevIDs []string, firstPort string, wantOutput, flattenOutput bool, stateVars map[string]bool) ([]string, string, varRef, error) {
	loopID := g.nextID()
	varName := ""
	if len(varNames) > 0 {
		varName = varNames[0]
	}
	loop := &Loop{ID: loopID, InlineName: g.inlineName, VarName: varName, StateTypes: map[string]*Type{}, FlattenOutput: flattenOutput}
	if len(iterables) > 0 {
		for i, iterable := range iterables {
			if iterable == nil {
				continue
			}
			ref, ok := g.resolveValueRef(iterable)
			if ok {
				g.recordPlacement(loopID, placementConstruct("loop", "iterable-ident"), PlacementNative, placementReason(PlacementCauseContract, "loop input direct ref (contract: for stays a platform container)"))
			}
			if !ok {
				// G16: a call-expression iterable (for (x in enumerate(items)))
				// materializes as a lightweight node — the enumerate pairing
				// shim is a pure data transform — and the loop iterates its
				// output. Heavier iterables (llm(...).result) are rejected by
				// the same purity gate the calc builtin renderer enforces.
				mref, merr := g.emitValueExpr(prevIDs, firstPort, false, iterable)
				if merr != nil {
					return nil, firstPort, varRef{}, merr
				}
				ref = mref
				g.recordPlacement(mref.nodeID, placementConstruct("loop", "iterable-call"), PlacementShim, placementReason(PlacementCauseContract, "pure transform materializes as python (G16 enumerate)"))
			}
			srcName := "input"
			if id, ok := iterable.(*ast.IdentExpr); ok {
				srcName = id.Name()
			}
			// zip 多源：变量名即端口名（roundtrip 输入字段名保真）。
			if len(iterables) > 1 && i < len(varNames) && varNames[i] != "" {
				srcName = varNames[i]
			}
			loop.LoopType = "by_array"
			loop.InputArrays = append(loop.InputArrays, srcName)
			loop.Inputs = append(loop.Inputs, Source{Path: []string{srcName}, BlockID: ref.nodeID, Name: ref.output})
			iterableType := g.portTypeOf(ref.nodeID, ref.output)
			if iterableType == nil || iterableType.Kind != "list" {
				iterableType = TList(T("string"))
			}
			if loop.Types == nil {
				loop.Types = map[string]*Type{}
			}
			loop.Types[srcName] = iterableType
			vname := ""
			if i < len(varNames) {
				vname = varNames[i]
			}
			if vname != "" {
				loop.StateTypes[vname] = T("string")
				if iterableType.Elem != nil {
					loop.StateTypes[vname] = iterableType.Elem
				}
			}
		}
	} else if iterCount != nil {
		loop.LoopType = "by_iteration"
		loop.LoopCount = *iterCount
	} else {
		loop.LoopType = "infinite"
		loop.LoopCount = 10
	}
	for v := range stateVars {
		lv, isLit := g.litVars[v]
		ref, hasRef := g.varToOutput[v]
		// 类型权威链（F1/F2/F4，2026-09-10；loop-state 类型轴家族修复）——
		// **级别序列 = 本宿主的数据**，判定与失败形态在 `slot_type_authority.go`
		// （一处实现，J §31）：
		//   1. 声明类型（varDeclTypes/hoistedTypes）——语言层权威，float 声明
		//      + int 字面量态 → float（F4 防 N2 静默 int 漂移）；array<object>
		//      提升态 → list<object>（F3 空列表初值同源）；
		//   2. 字面量 kind（无声明时：`f = 0.5` → number）；
		//   3. producer 口类型（producer-bound 且无声明：`x = v1.audios` →
		//      portTypeOf，F1）；
		//   4. 全缺 → fail loud（对齐 RS-LOOP-002 先例）——禁止静默伪造 string，
		//      那会把 scalar/list 状态降级成 string 并在回声/参数轴逐轮漂移
		//      （X212/S144/S146 族 TYPE_MISMATCH/EMPTY_AGGREGATE_TYPE 根因）。
		levels := []slotTypeLevel{
			{name: "声明类型", typ: func() *Type {
				if dt := g.varDeclTypes[v]; dt != nil {
					return dt
				}
				return g.hoistedTypes[v]
			}},
			{name: "字面量 kind", typ: func() *Type {
				if !isLit {
					return nil
				}
				switch lv.typ {
				case "integer":
					return T("integer")
				case "number":
					return T("number")
				case "boolean":
					return T("boolean")
				}
				return nil
			}},
			{name: "生产者口", typ: func() *Type {
				if !hasRef {
					return nil
				}
				return g.portTypeOf(ref.nodeID, ref.output)
			}},
		}
		ti := firstSlotType(levels)
		if ti == nil {
			return nil, firstPort, varRef{}, missingSlotTypeError("loop state", v, levels)
		}
		loop.StateTypes[v] = ti
		if isLit {
			if loop.StateValues == nil {
				loop.StateValues = map[string]Source{}
			}
			loop.StateValues[v] = Source{Val: lv.value}
		} else if ref, ok := g.varToOutput[v]; ok {
			if loop.StateValues == nil {
				loop.StateValues = map[string]Source{}
			}
			loop.StateValues[v] = Source{BlockID: ref.nodeID, Name: ref.output}
		}
	}

	// 容器类型面前置发布（loop 完整算法 §3 不变量 2，2026-09-11）：结构隔离
	// （g.nodes = nil）不得让「类型知识」一起消失——循环容器自己的迭代口/状态
	// 口类型（StateTypes/OutTypes）在体内被读时（`if (num < 5)`、`num + 1`、
	// typed 调用参数）必须可解析，而容器节点要到 body 生成后才 add 到 g.nodes
	// （:845），届时 g.find/portTypeOf 在体内都查不到它。故在体生成**之前**把
	// loop 节点发布进全局类型索引（g.remember），配合 portTypeOfNode(*Loop)
	// 的 StateTypes 分支——循环状态读与迭代变量读走同一条「容器口 = 类型源」
	// 规则，不再依赖逐点 ref.typ 补戳（迭代变量 :711 的补戳是同一规则的
	// 形态特例；缺了状态变量 :716 就是 R1/R2 的直接出生点）。
	g.remember(loop)

	// ── subgraph: isolated node registry, scoped var table ───────────────
	// treeNodes is isolated too: any node materialized for the iterable
	// (G16 enumerate(...) shim) must stay in the OUTER block, never leak
	// into the loop body via the shared pending-leaf queue.
	savedNodes := g.nodes
	savedTree := g.treeNodes
	g.nodes = nil
	g.treeNodes = nil
	savedVars := g.varToOutput
	g.varToOutput = cloneRefs(savedVars)
	savedYield := g.loopYields
	g.loopYields = nil
	savedYieldEnv := g.loopYieldEnvelope
	g.loopYieldEnvelope = nil
	savedTail := g.tailRef
	savedDepth := g.branchDepth
	savedLoops := g.loopStack
	g.loopStack = append(g.loopStack, loopID)
	savedStates := g.loopStates
	g.loopStates = append(g.loopStates, stateVars)
	// 循环体顶层 branchDepth 快照：body 在 branchDepth++ 之后运行,顶层
	// yield 的 branchDepth == savedDepth+1（见 emitYield 守卫）。
	savedLoopTop := g.loopBodyTopDepth
	g.loopBodyTopDepth = append(g.loopBodyTopDepth, savedDepth+1)
	for i, vname := range varNames {
		// kind "loop": the iteration variable is the loop container's item
		// port; field access resolves as a dotted member path (r.name →
		// "r.name"), mirroring the json.parse<Record> member convention so
		// coze loop internals bind {{item}} to the dotted source. zip
		// 多源：每个变量都注册（变量名即端口名）。
		if vname != "" {
			// Stamp the iteration variable's element type (resolved from the
			// iterable when the loop was configured, StateTypes[VarName]).
			// The loop container node lives in the OUTER node registry, but
			// body refs resolve inside the isolated body subgraph where
			// g.find(loopID) is nil — carrying the shape on the ref keeps the
			// body materialization type-correct (coze-biz loop state/output
			// contract) instead of silently degrading to string.
			g.varToOutput[vname] = varRef{nodeID: loopID, output: vname, kind: "loop", typ: loop.StateTypes[vname]}
		}
		_ = i
	}
	for v := range stateVars {
		g.varToOutput[v] = varRef{nodeID: loopID, output: v}
	}
	g.branchDepth++
	bodyBlock := &Block{}
	g.blockStack = append(g.blockStack, bodyBlock)
	junction, _, err := g.emitStmts(body, []string{loopID}, "loop-function-inline-output")
	g.branchDepth = savedDepth
	g.loopStack = savedLoops
	g.loopStates = savedStates
	g.loopBodyTopDepth = savedLoopTop
	if g.pendingElse != nil {
		// dangling else-less if at the loop tail: the unmatched path falls
		// through the selector's false port into the container.
		p := g.pendingElse
		g.pendingElse = nil
		seen := map[string]bool{p.selID: true}
		for _, id := range p.thenIDs {
			if id == p.selID || seen[id] || g.isBreakish(id) {
				continue
			}
			seen[id] = true
			junction = append(junction, id)
		}
		junction = append(junction, p.selID)
	}
	childNodes := g.nodes
	g.nodes = savedNodes
	g.varToOutput = savedVars
	loopYield := g.loopYields
	g.loopYields = savedYield
	loopYieldEnv := g.loopYieldEnvelope
	g.loopYieldEnvelope = savedYieldEnv
	g.tailRef = savedTail
	g.popBlock() // flush body leaves into the structured body block
	g.treeNodes = savedTree
	if err != nil {
		return nil, firstPort, varRef{}, err
	}

	// ── loop output port ───────────────────────────────────────────────
	// VALUE loops (wantOutput=true — for-in expressions) expose an output
	// port bound to the body tail; STATEMENT loops (wantOutput=false) are
	// void and carry NO output port — real exports (X12 batch 170020,
	// X3 void-batch) omit node_outputs entirely, and a bogus output ref
	// would be rejected at import (引用变量不存在).
	if wantOutput {
		// 命名多口：每个 yield 的口 → 一条 OutputSource（渲染端逐条写
		// loop.outputs）。无 yield → 回退 body 尾值（默认口 "output"）。
		if len(loopYield) > 0 {
			loop.OutTypes = map[string]*Type{}
			for _, port := range sortedKeys(loopYield) {
				yv := loopYield[port]
				var itemType *Type
				for _, child := range childNodes {
					if child.NodeID() == yv.nodeID {
						itemType = portTypeOfNode(child, yv.output)
						break
					}
				}
				// A named yield may publish an outer value (the common Coze
				// loop-state/output shape): its producer is intentionally outside
				// the loop body, so it is not present in childNodes.  Resolve the
				// producer through the builder's complete node table before
				// falling back to string.  Losing this type here makes the
				// renderer emit items:string even when the source is list<object>,
				// and the next decompile/compile pass then changes the contract.
				if itemType == nil {
					itemType = g.portTypeOf(yv.nodeID, yv.output)
				}
				// R-168：该口来自折叠过的行值信封 `yield out = { "k1": v1, … };`
				// ⇒ 元素类型是 record{k1: T(v1), …, kn: T(vn)}（值引用仍是那个
				// 产出者节点；成员类型逐个按同一套两档解析求，与未折叠路径同源）。
				// 任一成员类型求不出 ⇒ 不发明形状，保持未折叠的类型（登记在
				// R-168 尾注）。
				if members := loopYieldEnv[port]; len(members) > 0 {
					props := make(map[string]*Type, len(members))
					for _, m := range sortedKeys(members) {
						mt := g.portTypeResolved(childNodes, yv.nodeID, members[m])
						if mt == nil {
							props = nil
							break
						}
						props[m] = mt
					}
					if props != nil {
						itemType = TObject(props)
					}
				}
				outputType := TList(T("string"))
				if itemType != nil {
					if flattenOutput && itemType.Kind == "list" {
						outputType = itemType
						markFlattenPort(loop, port)
					} else {
						outputType = TList(itemType)
					}
				}
				loop.OutTypes[port] = outputType
				loop.OutputSources = append(loop.OutputSources,
					Source{Path: []string{port}, BlockID: yv.nodeID, Name: yv.output})
			}
		} else {
			srcNode, srcPort := "", ""
			for i := len(childNodes) - 1; i >= 0; i-- {
				cn := childNodes[i]
				if !g.childIsBreakish(childNodes, cn.NodeID()) {
					srcNode, srcPort = cn.NodeID(), firstOutputPort(cn)
					break
				}
			}
			if srcNode != "" {
				var itemType *Type
				for _, child := range childNodes {
					if child.NodeID() == srcNode {
						itemType = portTypeOfNode(child, srcPort)
						break
					}
				}
				outputType := TList(T("string"))
				if itemType != nil {
					if flattenOutput && itemType.Kind == "list" {
						outputType = itemType
						markFlattenPort(loop, "output")
					} else {
						outputType = TList(itemType)
					}
				}
				loop.OutTypes = map[string]*Type{"output": outputType}
				loop.OutputSources = []Source{{Path: []string{"output"}, BlockID: srcNode, Name: srcPort}}
			}
		}
	}
	// M17 (46_loop_escape):loop body 含 return 时,loop 输出额外暴露
	// "returned"(bool,loop 是否因 return 终止)与 "return_value"(终止时
	// 的返回值)端口 —— 运行期由驱动器从 LoopState 填充;spec 层短路与
	// dify 渲染的 __returning / __return_value 机制都基于这两个端口。
	// body 编译期经 loopReturns 中转的 return 值在此（端口装配前）落盘。
	if rv, ok := g.loopReturns[loopID]; ok && loop.ReturnValue == nil {
		loop.ReturnValue = rv
	}
	if loop.ReturnValue != nil {
		if loop.OutTypes == nil {
			loop.OutTypes = map[string]*Type{}
		}
		rvType := g.portTypeOf(loop.ReturnValue.nodeID, loop.ReturnValue.output)
		if rvType == nil {
			rvType = T("string")
		}
		loop.OutTypes["returned"] = T("boolean")
		loop.OutTypes["return_value"] = rvType
		loop.OutputSources = append(loop.OutputSources,
			Source{Path: []string{"returned"}, BlockID: loopID, Name: "returned"},
			Source{Path: []string{"return_value"}, BlockID: loopID, Name: "return_value"},
		)
	}

	oref := varRef{nodeID: loopID, output: "output"}
	if loop.OutTypes != nil {
		if _, hasOut := loop.OutTypes["output"]; !hasOut {
			// 无 "output" 口的命名口循环（如 MP3_urls/mp3_druaction）：
			// 循环表达式值 = 确定性首口（sorted），下游按 agg.<port> 寻址。
			if ports := sortedKeys(loop.OutTypes); len(ports) > 0 {
				oref = varRef{nodeID: loopID, output: ports[0]}
			}
		}
	}
	for _, v := range sortedKeys(stateVars) {
		if loop.OutTypes == nil {
			loop.OutTypes = map[string]*Type{}
		}
		loop.OutTypes[v] = loop.StateTypes[v]
		loop.OutputSources = append(loop.OutputSources, Source{Path: []string{v}, BlockID: loopID, Name: v})
		g.varToOutput[v] = varRef{nodeID: loopID, output: v}
		delete(g.litVars, v)
	}

	g.add(loop)
	// Statement loops (wantOutput=false) yield nothing as a VALUE: clear
	// the block tail so the enclosing if/else aggregate never picks up the
	// pre-loop binding (a stale whole-node handle {plugin, "output"} the
	// platform rejects — 引用变量不存在). X12 batch-loop prelude: the
	// String2Array binding before a side-effect parallel-for leaked into
	// the outer if merge; the loop's own control flow is carried by the
	// rendered container edges, not by a tail value. Value loops
	// (wantOutput=true) set tailRef via their oref binding in emitVarDecl.
	if !wantOutput {
		g.tailRef = nil
	}
	// ── tree: the loop owns its container; body nodes stay nested ──────
	if wantOutput && loop.OutTypes != nil {
		oref.ports = sortedKeys(loop.OutTypes)
	}
	orefCopy := oref
	g.consumeTreeNodes(loopID)
	g.flushTreeNodes()
	g.appendStmt(&LoopStmt{
		ID:            loopID,
		InlineName:    loop.InlineName,
		VarName:       loop.VarName,
		LoopType:      loop.LoopType,
		LoopCount:     loop.LoopCount,
		InputArrays:   loop.InputArrays,
		Inputs:        loop.Inputs,
		Types:         loop.Types,
		StateTypes:    loop.StateTypes,
		StateValues:   loop.StateValues,
		OutTypes:      loop.OutTypes,
		OutputSources: loop.OutputSources,
		FlattenOutput: loop.FlattenOutput,
		FlattenPorts:  loop.FlattenPorts,
		Body:          bodyBlock,
		Container:     loop,
		Value:         &orefCopy,
		ReturnValue:   loop.ReturnValue,
	})
	return []string{loopID}, "", oref, nil
}

// markFlattenPort 记下「该输出口的展平语义真正生效」——语义类型已取成元素类型
// （= 展平），而平台 loop 收集的是 list<元素> ⇒ lowering 必须物化展平垫片。
// 判据与 outputType 的取值同处一地（同一处定义，不在别处重算）。
func markFlattenPort(loop *Loop, port string) {
	if loop.FlattenPorts == nil {
		loop.FlattenPorts = map[string]bool{}
	}
	loop.FlattenPorts[port] = true
}

// sortedKeys returns sorted map keys for deterministic slice emission.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// childIsBreakish reports whether a child node (by id) is Break/Continue.
func (g *Builder) childIsBreakish(nodes []Node, id string) bool {
	for _, n := range nodes {
		if n.NodeID() == id {
			switch n.(type) {
			case *BreakNode, *ContinueNode:
				return true
			}
			return false
		}
	}
	return false
}

// childRefsContainer reports whether a child node's inputs reference the
// loop container (iteration-variable reads via parent_intermediate).
func ChildRefsContainer(nd Node, loopID string) bool {
	switch n := nd.(type) {
	case *LLMCall:
		for _, f := range n.Inputs {
			if f.BlockID == loopID {
				return true
			}
		}
	case *CodeRun:
		for _, f := range n.Inputs {
			if f.BlockID == loopID {
				return true
			}
		}
	case *Selector:
		for _, c := range n.Conditions {
			if (c.Left.Ref != nil && c.Left.Ref.Producer == loopID) ||
				(c.Right.Ref != nil && c.Right.Ref.Producer == loopID) {
				return true
			}
		}
	case *TextProcess:
		for _, f := range n.Inputs {
			if f.BlockID == loopID {
				return true
			}
		}
	case *Aggregate:
		for _, r := range n.AllRefs() {
			if r.Producer == loopID {
				return true
			}
		}
	case *KnowledgeCall:
		for _, f := range n.Inputs {
			if f.BlockID == loopID {
				return true
			}
		}
	case *Assign:
		if n.Value.BlockID == loopID {
			return true
		}
	case *Loop:
		for _, f := range n.Inputs {
			if f.BlockID == loopID {
				return true
			}
		}
	}
	return false
}

// firstOutputPort returns the primary output port name of a node.
func firstOutputPort(nd Node) string {
	switch n := nd.(type) {
	case *CodeRun:
		if len(n.OutOrder) > 0 {
			return n.OutOrder[0]
		}
		if name, ok := singleOutputKey(n.OutTypes); ok {
			return name
		}
		return "output"
	case *Aggregate:
		return "Group1"
	case *Start:
		return "output"
	default:
		return "output"
	}
}

// emitBreak lowers a break statement into a BreakNode inside the loop
// subgraph. The parser synthesizes the child→container edge.
func (g *Builder) emitBreak(prevIDs []string, firstPort string) ([]string, string, error) {
	if len(g.loopStack) == 0 {
		return nil, firstPort, &UnsupportedError{Construct: "break outside loop"}
	}
	id := g.nextID()
	bn := &BreakNode{ID: id}
	g.add(bn)
	g.consumeTreeNodes(id)
	g.flushTreeNodes()
	g.appendStmt(&BreakStmt{Node: bn})
	return []string{id}, "", nil
}

// emitContinue lowers a continue statement into a ContinueNode (type 29).
func (g *Builder) emitContinue(prevIDs []string, firstPort string) ([]string, string, error) {
	if len(g.loopStack) == 0 {
		return nil, firstPort, &UnsupportedError{Construct: "continue outside loop"}
	}
	id := g.nextID()
	cn := &ContinueNode{ID: id}
	g.add(cn)
	g.consumeTreeNodes(id)
	g.flushTreeNodes()
	g.appendStmt(&ContinueStmt{Node: cn})
	return []string{id}, "", nil
}

// refPortMatchesEnvelopeKey 报告「折出来的 wire 口名」是否就是信封成员名。
// 平台的容器行值恒等式是「成员名 = 体内产出端口名」，所以这是折叠的正确性守卫；
// 唯一的别名是 LLM：语义口 `text` 在渲染端落物理口 output/value
// （internal/backend/cozebiz/（商业仓）render_loop.go 里那一对，语料实证 X70 原生导出 =
// 声明 {output} + 绑定 path 落 output）—— 按同一对放行，不另立映射。
func refPortMatchesEnvelopeKey(refOut, key string) bool {
	if refOut == key {
		return true
	}
	switch refOut {
	case "text":
		return key == "output" || key == "value"
	case "output", "value":
		return key == "text"
	}
	return false
}

// refLikeMapValue 报告表达式是否是**纯引用**形态（标识符 / 字段访问 / 下标链）。
// 信封折叠（R-168）只在这类值上成立：折出来的 wire 口名与键的比对才有意义，且
// 折叠失败回落时不会因为「先发射内层表达式」而留下孤儿物化节点。
func refLikeMapValue(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.IdentExpr:
		return true
	case *ast.FieldAccessExpr:
		return refLikeMapValue(v.Obj())
	case *ast.IndexExpr:
		return refLikeMapValue(v.Obj())
	}
	return false
}

// tryFoldRowEnvelope 判定并折叠一枚「行值信封」record 字面量：
// `{ "k1": <产出者口1>, …, "kn": <产出者口n> }`（n ≥ 1）。成功时置 *ref = 首个
// 成员的口引用、返回 成员名 → **wire 口名** 的表；失败返回 nil（调用方回落既有
// 物化路径）。成员个数不影响判定 —— 平台的元素 record 有几个成员，信封就有几个
// 成员（X70 = 1 个 `{output}`，X86 = 2 个 `{output, reasoning_content}`）。
//
// 三条守卫全是**机械可判**的条件，任一不满足即不定形：
//   - 每个成员的值都是纯引用（复杂值物化才是对的，见 refLikeMapValue）；
//   - 每个成员的键是静态字符串，且与折出来的 wire 口名一致
//     （refPortMatchesEnvelopeKey，唯一别名 = LLM 的 text ↔ output/value）；
//   - 所有成员来自**同一个**产出者节点（跨节点折成一个口引用 = 丢信息）。
func (g *Builder) tryFoldRowEnvelope(prevIDs []string, firstPort string, d *ast.MapLit, ref *varRef) map[string]string {
	entries := d.Entries()
	if len(entries) == 0 {
		return nil
	}
	members := make(map[string]string, len(entries))
	refs := make(map[string]varRef, len(entries))
	producer := ""
	for _, e := range entries {
		if !refLikeMapValue(e.Value) {
			return nil
		}
		key, ok := ast.StaticMapKey(e.Key)
		if !ok || key == "" {
			return nil
		}
		r, err := g.emitValueExpr(prevIDs, firstPort, false, e.Value)
		if err != nil || !refPortMatchesEnvelopeKey(r.output, key) {
			return nil
		}
		if producer == "" {
			producer = r.nodeID
		} else if r.nodeID != producer {
			return nil
		}
		members[key] = r.output
		refs[key] = r
	}
	if producer == "" {
		return nil
	}
	bind, ok := envelopeBindingKey(members)
	if !ok {
		return nil
	}
	*ref = refs[bind]
	return members
}

// envelopeBindingKey 选「容器口的值绑定」用**哪一个成员的口引用**。
//
// 口径 = **成员集合的函数**（与字面量里的书写顺序无关）—— 端口绑定是容器口
// 唯一的 `{path, ref_node}`，两轮必须给出同一条，否则 `c0 != c1`（一手：S38 的
// `path: ""` vs `path: output` 就是这条）。两条优先级：
//  1. **载荷口优先**：LLM 的语义口 `text` 在渲染端落物理口 `output`/`value`，
//     只有载荷口**保证**在渲染后的产出者上存在；若按成员名取「第一个」，被选中的
//     可能是元数据口（`reasoning_content`），渲染端找不到该口 ⇒ 走「整节点绑定」
//     兜底 ⇒ `path: ""` 且元素 schema 被改写成整节点形状（信封当场丢）。同一
//     别名对见 refPortMatchesEnvelopeKey（不新立映射）。
//  2. 其余情况取字典序最小的成员 —— 只为**确定性**，不表达偏好。
func envelopeBindingKey(members map[string]string) (string, bool) {
	keys := sortedKeys(members)
	for _, k := range keys {
		switch members[k] {
		case "text", "output", "value":
			return k, true
		}
	}
	if len(keys) == 0 {
		return "", false
	}
	return keys[0], true
}

// portTypeResolved 解析 (nodeID, output) 的类型：优先走**体内节点表**，再回落
// builder 的完整节点表 —— 与 emitLoopNode 里既有的两档解析同源（体内节点不在
// 完整表里时只有前者能答；外举的产出者只有后者能答）。
func (g *Builder) portTypeResolved(childNodes []Node, nodeID, output string) *Type {
	for _, child := range childNodes {
		if child.NodeID() == nodeID {
			if t := portTypeOfNode(child, output); t != nil {
				return t
			}
			break
		}
	}
	return g.portTypeOf(nodeID, output)
}

// emitYield records the per-iteration published value of the current loop.
func (g *Builder) emitYield(s *ast.YieldStmt, prevIDs []string, firstPort string) ([]string, string, error) {
	if len(g.loopStack) == 0 {
		// parallel statements have no collection target: `yield` is legal
		// only inside a value-producing for / parallel-for (the parallel
		// expression form was removed; arrays are authored explicitly).
		return nil, firstPort, &UnsupportedError{Construct: "yield outside loop"}
	}
	// R-168 行值信封（lowering 半）：单成员 record 字面量 `{ "k": v }` 在这个
	// 位置是**信封声明**而不是运行期构造 —— 平台的容器行值由两件独立的事拼成：
	// 元素类型（容器自己声明）+ 值引用（体内产出端口的 ref）。成员名只是那个
	// 端口的名字，所以折回 `ref(v)` 本身、把元素类型钉成 record{k: T(v)}
	// （不物化 record 字面量、不造 shim、不多造 φ）。折叠**有守卫**：折出来的
	// wire 口名必须正好是那个键（成员名 = 产出端口名才是平台那条恒等式），
	// 否则不定形 —— 回落既有物化路径。
	valExpr := s.Value()
	var ref varRef
	var verr error
	var foldedEnv map[string]string
	if d, ok := valExpr.(*ast.MapLit); ok {
		foldedEnv = g.tryFoldRowEnvelope(prevIDs, firstPort, d, &ref)
	}
	if foldedEnv == nil {
		ref, verr = g.emitValueExpr(prevIDs, firstPort, false, valExpr)
		if verr != nil {
			return nil, firstPort, verr
		}
	}
	loopID := g.loopStack[len(g.loopStack)-1]
	if ref.nodeID == loopID {
		// 占位符名 ≡ 绑定名 ≡ 容器的**端口名**（迭代口 = 循环变量名、状态口 =
		// 状态变量名；render_loop.go 把 inputs[0].Name 钉成 VarName），**不是**
		// 源码里的标识符拼写：`yield item;` 与别名写法 `let v = item; yield v;`
		// 是同一件事，必须发射同一条 wire。
		// 曾用 `s.Value().(*ast.IdentExpr).Name()` ⇒ 别名情形发射
		// `{{v1}}` + path:item 的**混合形状**（占位符名 ≠ 端口名），读回再编译
		// 得 `{{v1}}` 而 c0 是 `{{item}}` ⇒ C∘R∘C 结构面漂移
		// （TestChainedBarrierRaisingRoundtrip: barrier-then-barrier-with-yield-tail）。
		name := ref.output
		if name == "" {
			name = "item"
		}
		id := g.nextID()
		g.add(&TextProcess{
			ID:       id,
			Method:   "concat",
			Template: "{{" + name + "}}",
			Inputs:   []Source{{Path: []string{name}, BlockID: ref.nodeID, Name: ref.output}},
		})
		ref = varRef{nodeID: id, output: "output", kind: "text"}
	}
	port := s.Port()
	if port == "" {
		port = "output"
	}
	// 口名与循环状态名撞名 = 不可还原形态，**在 lowering 边界就 fail loud**
	// （AGENTS.md 覆盖承诺节：不得静默产出无法导入/无法还原的产物）。平台的
	// variableParameters（状态）与 node_outputs（收集口）是两个可同名并存的
	// 命名空间，而 ncoda 只有**一个标识符空间**：`yield acc = …` 与状态变量
	// `acc` 同名时，反编译侧无法无歧义还原（该名字在 wire 上既指状态又指收集
	// 口，静默择一 = R0≍C0 语义 + 类型双漂移，见 RS-LOOP-003）。这里与反编译
	// 侧对称拒绝，让作者在编译期就改掉名字。
	if len(g.loopStates) > 0 {
		if states := g.loopStates[len(g.loopStates)-1]; states[port] {
			return nil, firstPort, &UnsupportedError{Construct: fmt.Sprintf(
				"yield port %q collides with the loop state variable of the same name: ncoda has a single "+
					"identifier space for loop state and named collect ports, so the platform shape cannot be "+
					"raised back — rename the state variable or the yield port", port)}
		}
	}
	// loopYields 只登记**循环体顶层**顺序发布的命名口。判定"顶层"用
	// loopBodyTopDepth 快照（branchDepth == 顶层值）：循环体自身被
	// emitLoopNode 抬高 1 层（branchDepth >= 1），历史 `branchDepth == 0`
	// 守卫把全部循环内 yield 都丢弃（W1/B8 契约断裂：命名口丢失、整节点
	// 引用回退 firstOutputPort 收窄）。
	//
	// 位置契约（2026-09-15 用户重申的原始设计）：yield 是循环体的**独立
	// 语句**，不得嵌在臂内 —— 嵌套形态由前端 YieldContractPass 以 E1043
	// 拒绝（pkg/passes/yield_contract.go）。所以 `branchDepth > 顶层值`
	// 这一支是**不可达的防御分支**，不是"合法但走别的通道"：
	// 此前的注释声称"臂内 yield 值经 emitIf 尾部 Aggregate 合并（tailRef
	// 回流）"，与实现不符 —— build_return.go 的 `produced` 只认显式 return，
	// 臂内 yield 的值当时会静默丢失（恒零候选 merge，循环输出读空值；
	// /tmp 复现 + coze-reference X13 实证）。语言层禁止该形态即从根上
	// 消掉这条静默通道。
	if len(g.loopBodyTopDepth) > 0 && g.branchDepth == g.loopBodyTopDepth[len(g.loopBodyTopDepth)-1] {
		if g.loopYields == nil {
			g.loopYields = map[string]*varRef{}
		}
		if _, dup := g.loopYields[port]; dup {
			return nil, firstPort, fmt.Errorf("duplicate yield port %q in loop (each port publishes once per iteration)", port)
		}
		g.loopYields[port] = &ref
		if len(foldedEnv) > 0 {
			if g.loopYieldEnvelope == nil {
				g.loopYieldEnvelope = map[string]map[string]string{}
			}
			g.loopYieldEnvelope[port] = foldedEnv
		}
	}
	// yield 值也是分支/循环体的产出值（与分支 return 同构）：tailRef
	// 供 emitIf 聚合与循环输出回退寻址；循环顶层 yield 的 loopYields
	// 登记优先于 tailRef 回退（显式口名）。
	g.tailRef = &ref
	producer := prevIDs
	if materializedKind(ref.kind) && len(producer) > 0 && producer[len(producer)-1] != ref.nodeID {
		producer = []string{ref.nodeID}
	}
	return producer, "", nil
}

// emitLoopStateAssign lowers a loop-state assignment (e.g. num += 1 inside a
// while body) into a VariableAssignerWithinLoop: left is the parent
// intermediate ref (loop container), right is the computed value.
//
// RHS 编码契约（S2b-2 定稿，2026-09-08，S2-gate 修正 1-3 并入）：
//   - 外部 producer ref（nodeID != 容器）→ {BlockID, Name}（wire {path, ref_node}）；
//   - 本容器元素口 ref（nodeID == 容器 ∧ kind=="loop"，如 `inner = it`）→ 数据引用，同样
//     编码 {BlockID: 容器, Name: 元素名}——与 body 其它位置消费元素的 wire 同构；旧守卫按
//     "nodeID==容器"一律丢弃 → 空 Source → wire right:null → raising literal content:nil 崩
//     （元素口与状态口都挂在容器上，必须按 kind 区分，不得一刀切）；
//   - 状态自赋（nodeID==容器 ∧ kind≠loop ∧ output==name，如 `out = out`）→ 语义 no-op，
//     跳过不物化（X4 冗余消除先例；合法程序不因无用自赋报错）；
//   - 同容器跨名状态读（num2 = num1）→ 平台无 point-in-time 读证据，fail loud（合同 §7
//     残差），禁止静默 null；
//   - 字面量（string/int/float/bool）→ {Val: <原生值>}（wire 层按类型发外层 type，float
//     保真）；旧 stringValue 分支用 {nodeID: 容器, kind: "lit"} 假 ref 占位且不携带值 →
//     静默 null；
//   - numeric += → increment 节点 ref；string 累加右选择器 ref → 与 resolveValueRef 直命中
//     共用同一编码判据（encodeRef）——concat 右=元素随本族同愈。
func (g *Builder) emitLoopStateAssign(name string, value ast.Expr, prevIDs []string, firstPort string) ([]string, string, error) {
	loopID := g.loopStack[len(g.loopStack)-1]
	// encodeRef 把 ref 形态 RHS 编码成状态赋值 Source。skip=true = 语义 no-op（状态
	// 自赋），调用方直接返回当前 junction 不物化 assigner。
	encodeRef := func(r varRef) (Source, bool, error) {
		src := Source{Path: []string{name}}
		if r.nodeID == loopID && r.kind != "loop" {
			if r.output == name {
				return src, true, nil
			}
			return src, false, &UnsupportedError{Construct: fmt.Sprintf("loop-state assign reads sibling state %q (no point-in-time read evidence)", r.output)}
		}
		src.BlockID = r.nodeID
		src.Name = r.output
		return src, false, nil
	}
	var src Source
	skip := false
	if ref, ok := g.resolveValueRef(value); ok {
		var err error
		src, skip, err = encodeRef(ref)
		if err != nil {
			return nil, firstPort, err
		}
	} else if lit := literalValue(value); lit != nil {
		// 字面量直携原生值（string/int/float/bool），wire 层按类型发射外层 type。
		src = Source{Path: []string{name}, Val: lit}
	} else {
		// 值表达式 RHS（算术 / 模板串 / 三元 / 成员链 / 纯变换调用…）：与
		// **任何其它值位置**同一条规则——经 emitValueExpr 物化后按 ref 编码。
		// 此前一律 `loop-state assign value %T` fail loud，使 `acc = `${acc}-${x}`;`
		// 这类循环状态累加在 lowering 层不可编译（循环族轴 6 状态读写位置的
		// 覆盖缺口）。物化只走既有纯变换/文本通道，不引入新平台形状；副作用
		// 调用仍由 emitValueExpr 的既有门禁拒绝。
		//
		// 增量轴向（RHS = `状态名 + <整数字面量>`）**曾经**是一条独立旁路
		// （`buildIncrementWithLeft`）：它把 shim 的 `Types`/`OutTypes` 硬写成
		// `integer`，与状态变量的**声明类型**无关 ⇒ 声明为 float 的状态产出
		// 「状态口 float / 算它的 shim 出口 integer」的自相矛盾产物，且一轮
		// 往返后类型就变（2026-09-29 一手：`float f = 1.5; for (…) { f = f + 1; }`
		// ⇒ `ARTIFACT_DRIFT/PARAM@r2`；整数声明看不见这条错 ⇒ 潜伏至今）。
		// 旁路已删（R-101「少一条并行通路」在本格的最小实例）：类型的唯一权威
		// 是声明类型链（`slot_type_authority.go`），RHS 的字面量 kind 不是。
		// 旁路当年想守的字节约定（与通用 shim 「恒加括号」同尺）由通用通路
		// 自身承担，判据 = `cozebiz_shim_print_single_ruler_contract_test.go`。
		ref, verr := g.emitValueExpr(prevIDs, firstPort, false, value)
		if verr != nil {
			return nil, firstPort, verr
		}
		var err error
		src, skip, err = encodeRef(ref)
		if err != nil {
			return nil, firstPort, err
		}
	}
	if skip {
		return prevIDs, firstPort, nil
	}
	id := g.nextID()
	g.add(&Assign{ID: id, VarName: name, Value: src, LoopState: true, LoopID: loopID})
	return []string{id}, "", nil
}
