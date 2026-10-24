// IR builder — 按语句/节点族拆分的文件之一（Tier-2 review #9:拆文件不拆包,
// diff 局部化）。同一包内符号共享;本文件族的归属见各函数注释。
package ir

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/pyast"
)

// emitReturn lowers a return statement. Inside a branch the value resolves
// to the branch junction (the End is merged at the function level); at the
// top level it creates the canonical End node.
func (g *Builder) emitReturn(s *ast.ReturnStmt, prevIDs []string, firstPort string) ([]string, string, error) {
	// Bare `return;` in a void function: the type pass already accepted it
	// (RETURN_TYPE otherwise), so there is no value to carry — lower it to
	// a no-output terminal instead of crashing on a nil value expression.
	if s.Value() == nil {
		return g.emitVoidReturn(prevIDs)
	}
	// `return template "<逐字模板>" [using {…}]`（W2）：答复端点（coze-biz
	// `terminatePlan: useAnswerContent`）的值侧。控制侧与 `return;` 完全同形
	// （端点没有可承载的返回值），值侧落在 End 的答复模板身份上。
	if tr, ok := s.Value().(*ast.TemplateValue); ok {
		return g.emitTemplateValue(tr, s.Streaming(), prevIDs)
	}
	if _, ok := s.Value().(*ast.NullLit); ok {
		// return null = 无值返回（coze End 节点无输入时反编译产生，X12
		// 实证）：归一为 void return，平台语义等价（End 不接 value）。
		return g.emitVoidReturn(prevIDs)
	}
	// 整值返回（`return v`，v 绑定到一个**多端口产出方**的值——http/plugin/
	// subflow……）：该值在平台上没有单一整值口（http 暴露 body/headers/
	// statusCode，没有 object 口；plugin 声明的是扁平字段口），**整值 = 这组端口
	// 组成的 record**。规范形与作者显式写出同一 record 时**相同**——因此这里只做
	// 一次**糖展开**，把 `return v` 改写成 `return {f1: v.f1, …}`（同一个值的两种
	// 写法），此后全走既有通路，不引入任何新 wire 形状或新判据。
	//
	// 产物证据（② 一手）：同一 producer 贡献多条具名 end 输入是现成形状（语料 5
	// 例，producer = llm/subflow 多口）；end 输入名 ≠ 端口名是常态（语料 396 条
	// 输入里 264 条重命名）。判据与字段来源见 ports.go wholeValueAtReturn。
	// 糖展开的**适用范围是平台流程出口**：`return v` ⟺ `return {f1: v.f1, …}`
	// 这条等价只在「值被指名交给平台」的位置需要落地（出口/臂内汇聚 → 逐字段
	// 具名绑定）；**内联函数体的 return 不是平台出口**（P5：子函数 return = 结束
	// 该函数，值在 IR 内回流给调用点，函数在平台上没有表示）——在那里展开会把
	// 同一个值重新物化成一个 record 节点（多一个 shim、且其参数顺序取决于语言层
	// 条目顺序 ⇒ 与反编译侧重建的规范顺序不一致 ⇒ 一轮往返后产物漂移）。
	// 调用点的字段投影本就直接解析到产出方端口（fieldPort：字段名即口名），
	// 无需中转物化。
	if nv, changed := g.normalizedExitValue(s.Value(), g.inlineName == ""); changed {
		s = ast.NewReturnStmt(nv, s.Streaming())
	}
	if m, ok := s.Value().(*ast.MapLit); ok && g.branchDepth == 0 {
		return g.emitMapReturn(m, prevIDs)
	}
	// 本出口携带的**值**（值身份 + record 逐字段承载引用）——在物化之前算：
	// 物化之后只剩「为平台引用形式造出的载体节点」，两条臂上的同一个值就无从
	// 比较了（`if (c) { return v; } return v;` 会铸出候选全等的废话汇聚单元，
	// 用户 2026-09-17 裁定 (a)）。解析不到既有引用 ⇒ 零值 ⇒ 保持既有通路。
	exitVal := flowExit{}
	if m, ok := s.Value().(*ast.MapLit); ok {
		if v, ok := g.recordExitValue(m); ok {
			v.armLocalValue = g.refsProducedInCurrentArm(v.outputs)
			exitVal = v
		}
	}
	var ref varRef
	var verr error
	if id, ok := s.Value().(*ast.IdentExpr); ok {
		if _, isConv := g.convVars[id.Name()]; isConv {
			// Conversation variables are runtime globals, not block outputs.
			// Materialize the return as an answer-bearing text process so the reference target
			// receives {{#conversation.<name>#}} rather than an empty node ref.
			nodeID := g.nextID()
			g.add(&TextProcess{
				ID:       nodeID,
				Method:   "concat",
				Template: "{{global_variable_app." + id.Name() + "}}",
				Answer:   true,
			})
			ref = varRef{nodeID: nodeID, output: "output", kind: "text"}
		} else {
			ref, verr = g.emitValueExpr(prevIDs, firstPort, false, s.Value())
		}
	} else {
		ref, verr = g.emitValueExpr(prevIDs, firstPort, false, s.Value())
	}
	if verr != nil {
		return nil, firstPort, verr
	}
	// 单口产出方的整值 = 它声明的那个口（ports.go wholeValueAtReturn ①）：
	// IR 的通用绑定名是 `output`，而平台声明的口可能另有其名（knowledge ⇒
	// outputList、subflow ⇒ 它自己的输出名）。出口是值被指名交给平台的位置，
	// 故在此改绑到**平台真正声明的口**；绑定已是字段投影路径时不动。
	// 与隐式 else 臂共用同一个归一化函数（rebindExitPort）。
	ref = g.rebindExitPort(s.Value(), ref)
	// A direct template-literal return carries answer semantics (reference target
	// renders it as an Answer node; an assigned-then-returned template is a
	// Template node). Mark the materialized TextProcess so the backend can
	// fold it instead of emitting a plain terminal.
	if _, ok := s.Value().(*ast.TemplateString); ok {
		for i := len(g.nodes) - 1; i >= 0; i-- {
			if g.nodes[i].NodeID() == ref.nodeID {
				if tp, ok2 := g.nodes[i].(*TextProcess); ok2 {
					tp.Answer = true
				}
				break
			}
		}
	}
	producer := prevIDs
	if materializedKind(ref.kind) && len(producer) > 0 && producer[len(producer)-1] != ref.nodeID {
		producer = []string{ref.nodeID}
	}
	// M17 (46_loop_escape): loop 体内的 return 不是普通函数返回 —— 它
	// 终止最内层 loop 并把值经 loop 输出端口传出（对齐参考目标渲染的
	// __returning/__return_value 机制）。生成 LoopReturn 节点:值引用
	// （迭代变量直引或任意表达式物化）作为节点输入,运行期写入
	// LoopState.ReturnValue + break 标记;loop.ReturnValue 登记供
	// flow.Project 生成 post-loop 检查分支。合成 return（三元表达式
	// 分支值上浮）除外 —— 那是表达式语义,不是函数返回。
	if len(g.loopStack) > 0 && g.syntheticTernary == 0 && !g.inBranchValueArm() {
		return g.emitLoopReturn(ref, producer, s)
	}
	if g.branchDepth > 0 {
		g.branchReturns = true
		if g.parallelDepth > 0 {
			// R15（2026-09-30 用户裁 甲；裁决单 = docs/five-step/rulings/
			// 2026-09-30-ruling-sheet-r15.md）：**子函数**（inline call）内的
			// `parallel` 臂里的**带值** `return` 没有良定义 —— 两条顶层契约在此
			// 相交且都不可违背：P5 #4「分支内 return = 终止整个流程」×
			// return 语义分层 #2「子函数内 return = 值回流」；而 `parallel` 的所有臂
			// **都执行**（AND 栅栏，不是互斥）⇒「哪条臂的 return 是函数的值」没有
			// 语言级答案（候选序是产物序）。今天静默走的是 P5 #4 那一支：作者写的
			// 调用点续段与栅栏后语句**静默消失**（撞「失配 ≠ 静默丢弃」）。
			//
			// 姊妹构造 `parallel for` 的同枚未定义性已判 E1037（D-L3(a)，2026-09-14
			// 用户拍板；实现 = pkg/passes/parallel_iteration_local.go）⇒ 不为
			// 同一个问题造第二套规则，用同一把尺子（同一诊断码、同一类别 = language）。
			//
			// 边界（= 裁决单 §7 的三条，都必须继续 SUCCEEDED）：
			//   · 主流程（`main`，inlineName == ""）内维持 P5 #4 不变（main 无
			//     「函数返回值」概念，出口值由汇聚 merge 唯一确定）；
			//   · 子函数内**不带值**的 `return;` 已在上面走 emitVoidReturn（不选值
			//     ⇒ 结果确定，与 `parallel for` 的 void return 同判）；
			//   · QA action / attempt 臂的 return 是**分支结果值**（P5 subscope
			//     return）、三元表达式的合成 return 是**表达式语义** —— 两者都由
			//     宿主汇聚，不属本判据（沿用既有豁免谓词，语义不变）。
			if g.inlineName != "" && g.syntheticTernary == 0 && !g.inBranchValueArm() {
				sp := s.Span().Start
				return nil, firstPort, &LanguageError{
					Code: diag.PARALLEL_EXTERNAL_EXIT,
					Message: "return inside a `parallel` branch of a subfunction has no defined meaning: the " +
						"branch return would both terminate the whole flow (P5) and reflow a value to the call " +
						"site (return layering), and every branch of a `parallel` barrier runs (AND join, not " +
						"exclusive) — so which branch's value becomes the function's result is not defined by " +
						"the language; compute into variables inside the `parallel` block and return after the " +
						"barrier instead",
					Line:   sp.Line,
					Column: sp.Column,
				}
			}
			g.branchFlowReturns = true
			// P5 #4: return inside a parallel branch terminates the WHOLE
			// flow — the branch tail wires straight to the canonical end
			// 900001 (ensureEnd: first write wins). The barrier does not
			// wait for this branch. Project/renderers exclude the branch
			// from the junction via the empty exit + end node.
			ref2 := ref
			g.tailRef = &ref2
			endID := g.ensureEndValueIdent(ref2, producer, exitVal)
			// consumeTreeNodes: the End is carried by the ReturnStmt below
			// — without the consume it would also flush as a bare NodeStmt
			// and the projection would wire the branch tail to the end
			// twice (duplicate edge). Mirrors the top-level emitReturn.
			g.consumeTreeNodes(endID)
			g.flushTreeNodes()
			end := g.find(endID).(*End)
			g.appendStmt(&ReturnStmt{ID: endID, Value: &ref2, End: end, SharedEnd: true, FlowEnd: true})
			return []string{endID}, "", nil
		}
		if g.inlineName != "" || g.syntheticTernary > 0 || g.inBranchValueArm() {
			// 子函数内 return：结束该函数，值经 tailRef 回流给调用方
			// （inline 边界消费）——不是流程终止。
			// QA action / attempt success-failure 分支 return：分支结果值
			// （汇聚进 Aggregate / 独立 SuccessEnd-FailureEnd），非函数
			// return（P5 subscope return 契约）。
			g.recordInlineFrameExit(ref)
			g.tailRef = &ref
			g.flushTreeNodes()
			g.appendStmt(&ReturnStmt{ID: "", Value: &ref})
			return producer, "", nil
		}
		g.branchFlowReturns = true
		// 主流程 if / intent 分支 return：ncoda 语义 = 流程终止 —— 主流程
		// 内任何 return 都回到唯一 end 900001（P5 多出口契约），**且带值出口
		// 必须经汇聚 merge 到 end**（2026-09-12 用户拍板；平台 end 只承载
		// 一个值绑定，直连会让第二条出口的值静默丢失）。
		//
		// 控制流照旧（出口仍终止于唯一 End），值侧把本次出口登记为 flow-exit
		// phi 候选（ensureEnd 漏斗），**同时**把值置为分支尾值 tailRef：
		// 宿主 junction（if/switch/QA/attempt）据此把该臂的值收进自己的
		// 汇聚单元（与「臂内赋值提升变量」同一通道、同一规则，无宿主特判），
		// 汇聚单元的 Group1 再由 End 消费 —— 平台权威形状
		// `merge(G1:[A,B]) -> end` 即由此递归展开。
		// 本出口所在的臂：多出口值汇聚（materializeFlowExitPhi）据此把
		// 「非默认位」候选门控进臂内（值产在臂外 ⇒ 恒被 merge 选中）。
		exitVal.arm = g.currentArmBlock()
		endID := g.ensureEndValueIdent(ref, producer, exitVal)
		g.tailRef = &ref
		g.consumeTreeNodes(endID)
		g.flushTreeNodes()
		end := g.find(endID).(*End)
		g.appendStmt(&ReturnStmt{ID: endID, Value: &ref, End: end, SharedEnd: true, FlowEnd: true})
		return []string{endID}, "", nil
	}
	endID := g.ensureEndValueIdent(ref, producer, exitVal)
	g.tailRef = nil
	end := g.find(endID).(*End)
	g.consumeTreeNodes(endID)
	g.flushTreeNodes()
	g.appendStmt(&ReturnStmt{ID: endID, Value: &ref, End: end, SharedEnd: true})
	return []string{endID}, "", nil
}

// emitTemplateValue lowers `return template "<模板>" using {k: v, …}` (W2).
//
// 位置判据（**与臂种类无关**，一条规则）：模板返回是**流程出口的值侧**，因此
// 只允许出现在真正的流程出口位置 ——
//
//	· 循环体内 ⇒ 拒绝（循环里的 return 是 M17「带值逃逸循环」，答复模板不是
//	  循环状态的一部分；平台端点在画布上只有一个，不属于循环体）；
//	· 子函数（inline call）内 ⇒ 拒绝（子函数 return 是**值回流**给调用点）；
//	· QA action / attempt success-failure 臂 ⇒ 拒绝（那是**分支结果值**，P5）。
//
// 三条都是「这里不是流程出口」的同一个事实，故共用一条判据、一个诊断码。
//
// 值侧身份：全部出口必须给出**同一身份**（模板文本 + 名额表逐字相同）。平台端点
// 是唯一节点、值侧只有一个 ⇒ 两个不同身份无法承载（拒绝，不静默取其一）。
func (g *Builder) emitTemplateValue(tr *ast.TemplateValue, streaming bool, prevIDs []string) ([]string, string, error) {
	if len(g.loopStack) > 0 || g.inlineName != "" || g.inBranchValueArm() {
		return nil, "", &LanguageError{
			Code: diag.TEMPLATE_RETURN_PLACEMENT,
			Message: "return template 只能出现在**流程出口**位置（顶层或 if/intent/parallel 臂内）：" +
				"循环体内（return 是带值逃逸循环）、子函数内（return 是值回流）、QA/attempt 分支结果值臂内都不是流程出口",
		}
	}
	answer, err := g.buildTemplateAnswer(tr, streaming)
	if err != nil {
		return nil, "", err
	}
	if g.templateAnswer == nil {
		g.templateAnswer = answer
	} else if !sameEndAnswer(g.templateAnswer, answer) {
		return nil, "", &LanguageError{
			Code: diag.TEMPLATE_RETURN_IDENTITY,
			Message: "同一流程的两个出口给出了不同的答复模板身份（模板文本或名额表不一致）：" +
				"平台的答复端点是唯一节点，值侧只能有一个身份 —— 请让所有 return template 逐字一致",
		}
	}
	return g.emitVoidReturn(prevIDs)
}

// sameEndAnswer 报告两个答复模板身份是否逐字相同（模板文本 + 有序名额表：
// 名字与生产者引用都要一致 —— 平台端点的 node_inputs 是**有序**表）。
func sameEndAnswer(a, b *EndAnswer) bool {
	// 投递开关是**身份的一部分**：平台的答复端点只有一个 `streamingOutput`，
	// 两个出口给出不同开关 ⇒ 无法承载（拒绝，不静默取其一）。
	if a == nil || b == nil || a.Template != b.Template || len(a.Bindings) != len(b.Bindings) || a.Streaming != b.Streaming {
		return false
	}
	for i := range a.Bindings {
		// 身份 = 名字 + 引用。**不比 `Type`**：它是由 Ref 派生的（portTypeOf 是
		// Ref 的函数），且两次解析可能给出不同指针而语义相同 —— 拿它进 `!=`
		// 会把「两条出口给出同一名额表」误判成不同（CB-10）。
		if a.Bindings[i].Name != b.Bindings[i].Name || a.Bindings[i].Ref != b.Bindings[i].Ref {
			return false
		}
	}
	return true
}

// inBranchValueArm 报告当前是否位于「**分支结果值臂**」内：QA action /
// attempt success-failure 臂（branchValueDepth > 0）且不是 intent/switch 臂
// （switchBranchDepth == 0）。这类臂里的 return 是**分支结果值**
// （P5 subscope return，汇聚进 junction 聚合），不是函数返回 ⇒ 既不触发 M17
// 循环逃逸，也不做流程终止。
//
// 一条谓词、三处共用（emitReturn 的三条判据）：手写同一表达式三遍正是形态
// 枚举的温床 —— 2026-09-13 实测的 §6.7 漏网即由此而来：循环内 return 的
// 判据写成 `branchValueDepth == 0`，而 intent/switch 臂**也**递增
// branchValueDepth（build_condition.go:979-980 同时递增两个计数），于是同一
// 个「循环内 return」在 if 臂走 M17、在 switch 臂走「流程终止」，后者把共享
// end 放进循环容器内并发出指向未发射节点的 `loop--output-->X` 边（编译
// SUCCEEDED + L1/L2 双 PASS、反编译 rc=1）。
//
// 判据与**臂种类无关**（D4：`return` 结束当前函数/流程，循环不构成边界）：
// 裸体 / if 臂 / switch 臂 / intent 臂 / 任意嵌套深度下，循环内 return 一律
// 走 M17；唯一的例外是上面的 QA/attempt 分支结果值。
func (g *Builder) inBranchValueArm() bool {
	return g.branchValueDepth > 0 && g.switchBranchDepth == 0
}

// emitLoopReturn lowers a return statement inside a loop body (M17):
// terminates the innermost loop with a LoopReturnNode carrying the value,
// and records the value on the loop so flow.Project can expose it.
func (g *Builder) emitLoopReturn(ref varRef, producer []string, s *ast.ReturnStmt) ([]string, string, error) {
	loopID := g.loopStack[len(g.loopStack)-1]
	name := "return_value"
	if id, ok := s.Value().(*ast.IdentExpr); ok {
		name = id.Name()
	}
	lr := &LoopReturnNode{
		ID: g.nextID(),
		Value: Source{
			Path:    []string{name},
			BlockID: ref.nodeID,
			Name:    ref.output,
		},
	}
	g.add(lr)
	g.consumeTreeNodes(lr.ID)
	g.flushTreeNodes()
	g.appendStmt(&NodeStmt{Node: lr})
	// 登记 return 值到最内层 loop（输出契约 + flow post-loop 检查信号）。
	// loop 容器节点此刻尚未入 g.nodes（body 编译隔离期）,经 loopReturns
	// 中转,emitLoopNode 末尾落盘;能 find 到（嵌套结构例外）也同步写。
	r := ref
	if g.loopReturns == nil {
		g.loopReturns = map[string]*varRef{}
	}
	g.loopReturns[loopID] = &r
	if loop, ok := g.find(loopID).(*Loop); ok {
		loop.ReturnValue = &r
	}
	return []string{lr.ID}, "", nil
}

// emitVoidReturn lowers a bare `return;` (void return) into a no-output
// terminal: the flow simply terminates with no value. At the top level it
// creates the canonical shared End (the reference target renders it as the workflow exit;
// coze keeps its required 900001 exit). Inside a branch the return is
// recorded on the branch junction and the branch carries no value.
func (g *Builder) emitVoidReturn(prevIDs []string) ([]string, string, error) {
	if g.branchDepth > 0 {
		g.branchReturns = true
		if g.parallelDepth > 0 {
			g.branchFlowReturns = true
			// P5 #4: bare return; inside a parallel branch is also flow-end
			// (terminates the whole flow, wires to the shared end).
			g.tailRef = nil
			endID := g.ensureEnd(nil, prevIDs)
			g.consumeTreeNodes(endID)
			end := g.find(endID).(*End)
			g.flushTreeNodes()
			g.appendStmt(&ReturnStmt{ID: endID, Value: nil, End: end, SharedEnd: true, FlowEnd: true})
			return []string{endID}, "", nil
		}
		if g.inlineName != "" || g.syntheticTernary > 0 || g.inBranchValueArm() {
			// 子函数 / QA / attempt 分支 void return：分支结果 / 函数返回。
			g.appendStmt(&ReturnStmt{ID: "", Value: nil})
			return prevIDs, "", nil
		}
		// 主流程 if / intent 分支 void return（return;）：流程终止，直连 end。
		g.branchFlowReturns = true
		g.branchVoidReturns = true
		g.tailRef = nil
		endID := g.ensureEnd(nil, prevIDs)
		g.consumeTreeNodes(endID)
		end := g.find(endID).(*End)
		g.flushTreeNodes()
		g.appendStmt(&ReturnStmt{ID: endID, Value: nil, End: end, SharedEnd: true, FlowEnd: true})
		return []string{endID}, "", nil
	}
	endID := g.ensureEnd(nil, prevIDs)
	g.tailRef = nil
	end := g.find(endID).(*End)
	g.consumeTreeNodes(endID)
	g.flushTreeNodes()
	g.appendStmt(&ReturnStmt{ID: endID, Value: nil, End: end, SharedEnd: true})
	return []string{endID}, "", nil
}

// emitMapReturn lowers a top-level record return (`return {k: v, …}`，以及糖展开
// 后的 `return v` —— 两者同形同路)。
//
// 形状由「这条出口在流程中的位置」决定，**一条规则**（A12-H3，2026-09-15）：
//
//	① 本出口建立 end（此前没有任何出口）    ⇒ 具名输出：每个字段一条
//	   `{name: 语义字段名, path: 端口名}`（语料 396 条 end 输入里 264 条 name ≠ path）。
//	② end 已存在但**尚无值**（先行的无值出口，如守卫子句 `if (c) { return; }`）
//	   ⇒ 同一形状挂到那个 end 上：本出口是该流程**唯一**带值出口。
//	③ end 已有带值出口 ⇒ 本出口是第 N(≥2) 条出口 ⇒ 进**流程出口值通道**
//	   （`ensureEndValue` / flow-exit phi）：平台 `end` 只承载**一个**值绑定，
//	   记录值以整值 ref 登记，由收敛单元逐字段（每个声明字段一个组）合并 ——
//	   与臂内 record 出口走**同一条**通道、同一个收敛规则（值通道的形状只由
//	   值类型决定，不由「哪条语法产出了它」决定）。
//
// 旧实现在 ②③ 两种位置都无条件另建一个 end 节点：值侧静默丢失（end 保留第一条
// 出口的绑定），结构侧往树里塞进第二个同 id 的 end（`collectIRNodes` 按 id 取
// 最后一个 ⇒ 流程出口 phi 失去消费者 ⇒ 判死 ⇒ 图级节点摘不掉 ⇒ `optimizeGraph`
// 不收敛 / 挂死）。
func (g *Builder) emitMapReturn(m *ast.MapLit, prevIDs []string) ([]string, string, error) {
	endID := g.endID()
	if end := g.recordFlowEnd(); end != nil && endCarriesValue(end) {
		return g.emitRecordExitThroughValueChannel(m, prevIDs, endID)
	}
	outputs, oerr := g.mapReturnOutputs(m, prevIDs)
	if oerr != nil {
		return nil, "", oerr
	}
	if end := g.recordFlowEnd(); end != nil {
		// ② 先行出口都是无值的（控制流已闭到 end）：本出口是唯一带值出口。
		end.Outputs = outputs
		end.NamedReturn = true
		g.tailRef = nil
		g.consumeTreeNodes(endID)
		g.flushTreeNodes()
		g.appendStmt(&ReturnStmt{ID: endID, End: end, SharedEnd: true})
		return []string{endID}, "", nil
	}
	end := &End{ID: endID, Outputs: outputs, NamedReturn: true}
	g.add(end)
	g.endCreated = true
	g.tailRef = nil
	g.consumeTreeNodes(endID)
	g.flushTreeNodes()
	g.appendStmt(&ReturnStmt{ID: endID, End: end, SharedEnd: true})
	return []string{endID}, "", nil
}

// recordFlowEnd 返回已存在的规范 End（900001）；尚未建立时为 nil。
func (g *Builder) recordFlowEnd() *End {
	if node := g.find(g.endID()); node != nil {
		if end, ok := node.(*End); ok {
			return end
		}
	}
	return nil
}

// endCarriesValue 报告该 End 是否已经承载了一个值出口（值绑定或收敛单元）。
// 它判定的不是「形状该长什么样」，而是**结构事实**：这个出口是不是流程的第一条。
func endCarriesValue(end *End) bool {
	return len(end.Inputs) > 0 || end.FlowExitMerge != nil || len(end.Outputs) > 0
}

// emitRecordExitThroughValueChannel 把一条 record 出口登记进流程出口值通道：
// 值以**整值 ref**（record 载体节点的整值口）登记，≥2 条出口时由 flow-exit phi
// 逐字段合并。载体节点由 emitValueExpr 物化（与臂内 record 出口同一个调用、
// 同一种节点）—— 平台没有「整值口」的产出方（http/plugin）必须经载体才有整值
// 引用，这正是 `junctionValueFieldGroups` 逐字段投影的输入。
func (g *Builder) emitRecordExitThroughValueChannel(m *ast.MapLit, prevIDs []string, endID string) ([]string, string, error) {
	ref, verr := g.emitValueExpr(prevIDs, "", false, m)
	if verr != nil {
		return nil, "", &UnsupportedError{Construct: fmt.Sprintf(
			"return map value: %s (assign the value to a variable first, or write foreign code)",
			constructOf(verr))}
	}
	ident := flowExit{}
	if v, ok := g.recordExitValue(m); ok {
		ident = v
	}
	endID = g.ensureEndValueIdent(ref, prevIDs, ident)
	g.tailRef = nil
	g.consumeTreeNodes(endID)
	g.flushTreeNodes()
	end := g.find(endID).(*End)
	g.appendStmt(&ReturnStmt{ID: endID, Value: &ref, End: end, SharedEnd: true})
	return []string{endID}, "", nil
}

// mapReturnOutputs 求值 record 字面量的每个字段：可解析的引用直接绑定，其余按
// **值位置唯一入口**（emitValueExpr）物化。字段名 = 语言层语义名，端口名 =
// 产出方声明的物理口。
//
// 此前这里直调 `emitExprCode`（通用表达式 shim），**绕过**了值位置的物化规则 ——
// 非空字符串常量落在最外层字段位置时因此变成常量 code shim，而同一条意思写成
// `let v1 = "abc"; return { "k": v1 };` 走 emitValueExpr ⇒ 原生 text 节点：
// 同一个意思两种产物，且 code 那侧不是往返不动点（S146 一手，2026-09-25；最小复现
// `return { "k": "abc" };` 与 `return { "k": "a" + "b" };`）。
// 平台一手同侧：`end` 槽位在语料 398 个输入里 396 是 ref、**0 是字面量** ⇒ 出口值
// 必须由产出节点承载，而常量在该平台的原生承载面就是 TextProcessor
// （`emitTextConst` 对非空常量本该走的那条路）。shim 成立标准也判它不成立：
// 裸常量没有位置必要性（L 假）⇒ 不许造 shim。
func (g *Builder) mapReturnOutputs(m *ast.MapLit, prevIDs []string) ([]EndOutput, error) {
	outputs := make([]EndOutput, 0, len(m.Entries()))
	for _, entry := range m.Entries() {
		name, ok := staticMapKeyName(entry.Key)
		if !ok {
			return nil, dynamicMapKeyError("return map literal", entry.Key)
		}
		if ref, ok := g.resolveValueRef(entry.Value); ok {
			outputs = append(outputs, EndOutput{Name: name, Ref: Ref{Producer: ref.nodeID, Output: ref.output}})
			continue
		}
		// G12-2: a json.parse<array<T>> element chain (items[0].field) has
		// no dotted-port form — emitValueExpr 对这条链落到同一处「物化成提取
		// shim」通路（IndexExpr / FieldAccessExpr 分支），故 G12-2 语义不变。
		ref, verr := g.emitValueExpr(prevIDs, "", false, entry.Value)
		if verr != nil {
			return nil, &UnsupportedError{Construct: fmt.Sprintf(
				"return map value: %s (assign the value to a variable first, or write foreign code)",
				constructOf(verr))}
		}
		outputs = append(outputs, EndOutput{Name: name, Ref: Ref{Producer: ref.nodeID, Output: ref.output}})
	}
	return canonicalEndOutputs(outputs), nil
}

// ensureEnd creates the canonical End node once. Structured ReturnStmt nodes
// carry the flow relationship; the builder does not materialize edges.
// endCreated 由 ensureEnd / emitMapReturn / emitVoidReturn 设置（三者创建
// 900001）。output(...) 语句降级为 OutputEmitter（不产生 continuing End）。
// 因此 endCreated=true 且 900001 尚未创建时（output 之后紧跟 return,M14
// 48_response_accumulation）必须补建,否则 find(endID) 返回 nil → panic。
// ensureEndValue registers a value-carrying exit from a varRef, preserving the
// ref KIND alongside the recorded (producer, port) pair: the exit phi may need
// to project the exit's record value onto per-field groups, and a producer's
// declared field port (`aggregateFieldPort`) is only recoverable with the kind.
func (g *Builder) ensureEndValue(ref varRef, producer []string) string {
	return g.ensureEndValueIdent(ref, producer, flowExit{})
}

// ensureEndValueIdent 同 ensureEndValue，另带该出口携带的**值**描述（identity
// / outputs）。值身份只在值表达式仍可见处（emitReturn / record exit through the
// value channel）能给出：物化之后只剩载体节点，两条臂的同一个值就无从比较了。
func (g *Builder) ensureEndValueIdent(ref varRef, producer []string, e flowExit) string {
	e.ref = Ref{Producer: ref.nodeID, Output: ref.output}
	if ref.kind != "" && ref.output != "" {
		e.kind = ref.kind
	}
	return g.ensureEndExits(producer, e)
}

func (g *Builder) ensureEnd(inputs []Ref, producer []string) string {
	exits := make([]flowExit, 0, len(inputs))
	for _, r := range inputs {
		exits = append(exits, flowExit{ref: r})
	}
	return g.ensureEndExits(producer, exits...)
}

func (g *Builder) ensureEndExits(_ []string, exits ...flowExit) string {
	// Every value-carrying main-flow exit funnels through here (top level,
	// if/intent/parallel arm, map-return): record it so the End binding can be
	// decided once all exits are known. Void exits pass nil and record
	// nothing — they keep entering the End directly.
	g.recordFlowExit(exits...)
	endID := g.endID()
	if g.find(endID) != nil {
		return endID
	}
	refs := make([]Ref, 0, len(exits))
	for _, e := range exits {
		refs = append(refs, e.ref)
	}
	g.add(&End{ID: endID, Inputs: refs})
	g.endCreated = true
	return endID
}

// flowExit = 一次流程出口携带的**值**（不是「为它物化出的那个节点」）。
//
// 两者必须分开，因为平台的引用形式要求一个值在**每条臂**上都有一个承载节点：
// `if (c) { return v; } return v;` 的两次出口携带同一个值 v，却各有自己的承载
// 节点（record 字面量 shim）。只比候选引用时它们是「两条出口」，于是铸出一个
// 候选全等的汇聚单元 —— 那个单元无语义（值本来就被绑定单独承载），
// 且它的文本形（提升变量 + 两臂赋同一个字段）再编译回来候选会塌成同一个
// 生产者，反编译拒收（`RS-MRG-001 … cannot prove candidate roles`）。
//
//	ref        φ 候选：多**值**汇聚时投给 merge 的引用（单值时不用）。
//	kind       候选的 varRef kind（record 值投影到逐字段组时靠它复原声明口）。
//	identity   值身份键：同键 ⇒ 同一个值。它现在的唯一用途是**整单元**判定
//	           （`sameFlowExitValue`：全部出口同值 ⇒ 不物化收敛单元）；
//	           出口**不**按它归并 —— 出口的单位是路径上的一份承载（见
//	           `recordFlowExit`）。「同值」既是剔除废话单元的依据，也是
//	           「该出口的载体可删」的依据（`dropCollapsedExitCarriers`）。
//	outputs    值是一条 record 时：逐字段具名绑定（由**承载引用**给出，供 End 直接绑定）。
type flowExit struct {
	ref      Ref
	kind     string
	identity string
	outputs  []EndOutput
	// arm = 本出口所在的 junction 臂块（nil = 不在任何臂内：顶层 fall-through
	// 出口 / parallel 分支出口）。它只在多出口值汇聚（materializeFlowExitPhi）
	// 里用一次：非默认位（非最后一条）出口的值若产在臂**外**，则它在该臂被走
	// 与否**都**存在 ⇒ merge 按位置恒取它 ⇒ 条件失效；门控 = 在该臂内物化一个
	// 恒等载体（arm_gate_candidate.go，与臂赋值通道同一条规则）。
	arm *Block
	// armLocalValue = 本出口携带的 record 值的逐字段引用**全部在本臂内产出**。
	// 它只在多出口 record 收敛的载体判定里用一次（materializeFlowExitPhi）：
	// 出口值在臂内产出 ⇒ 汇聚候选自己就带着臂归属，**载体是冗余的**；出口值来自
	// 臂外（上游）⇒ 臂归属只由载体给出。真假都不改变任何取值语义。
	armLocalValue bool
}

// recordFlowExit remembers value-carrying main-flow exits in source order.
// Two exits carrying the same **value** are one exit, not two: the same value
// named by two arms must not manufacture a two-candidate phi whose candidates
// are copies of one another.
func (g *Builder) recordFlowExit(exits ...flowExit) {
	if g.unreachable > 0 {
		// A dead statement is not part of the flow: its "exit" never runs, so
		// it is not an exit. Recording it would add a candidate whose producer
		// the projections cut (dangling ref), see Builder.unreachable.
		return
	}
	for _, e := range exits {
		if e.ref.Producer == "" {
			continue
		}
		// 空端口名 = 「整个节点的值」（具名组 merge 的整值引用，渲染为
		// `path: ""`）。它是**真值**，不是「没有值」——「没有值」是零候选
		// junction（`Refs` 与 `Groups` 皆空），那种单元根本不会被登记为出口。
		// 只对结构单元（Aggregate）放行：普通 producer 的空端口引用才是无意义
		// 的（它没有「整体值」这个口）。
		if e.ref.Output == "" {
			if _, ok := g.findAny(e.ref.Producer).(*Aggregate); !ok {
				continue
			}
		}
		if e.identity == "" {
			e.identity = refValueIdentity(e.ref)
		}
		// **不按值身份归并出口**（A12-H16 初版曾在登记处按 identity 去重，
		// 2026-09-19 撤回）：出口的单位是**路径上的一份承载**，不是「值」。
		// 两条出口携带同一个值（同一个臂外产出方）各自有**自己的载体**，
		// 归并掉一条 = 那条路径在汇聚里**没有候选**（平台 merge 对未执行节点
		// 的候选取 nil ⇒ 跳过）⇒ 该路径的出口值静默丢失（一手实证
		// exit_phi_shared/d0/s2：尾出口与真臂出口同值，归并后 n∉{3,4} 的路径
		// 零候选 ⇒ 读到零值）。用户裁定「两条分支写同一个值 ⇒ 废话 if 直接
		// 消掉」（2026-09-17 (a)）是**整个单元**的判据（全部出口同值），
		// 由 `sameFlowExitValue` 在物化处整单元消掉 —— 与「逐条归并」是两件
		// 不同的事：前者消掉的是整个单元（值由绑定单独承载，所有路径都可达），
		// 后者悄悄丢掉一条路径的候选。
		g.flowExits = append(g.flowExits, e)
	}
}

// refValueIdentity = 单口引用的值身份（默认身份：候选引用本身就是承载）。
func refValueIdentity(r Ref) string {
	return "ref\x00" + r.Producer + "\x00" + r.Output
}

// recordExitValue 给出「出口的值是一条 record，且每个入口都解析到**既有引用**」
// 时的值描述（值身份 + 逐字段具名绑定）。
//
// 判据与 `mapReturnOutputs`（顶层出口的逐字段绑定）同源：入口解析得到的是
// 产出方声明的端口引用，与「为平台引用形式物化出的 record 字面量 shim」是
// **同一个值的两种编码**。因此本函数不物化任何节点，也不引入新 wire 形状：
// 它只回答「这个值是不是已经被既有引用单独承载了」。
//
// 任一入口解析不到既有引用（如 `items[0].field` 链）⇒ ok=false：该值只能由
// 物化载体承载，没有独立于承载节点的身份，调用方保持既有通路。
//
// 身份键与 outputs 都用**同一条尺**（字段名升序）⇒ 与写法顺序无关（同一条规则的
// 两种写法 = 同一个值，也 = 同一张绑定表）。旧实现只规范化身份键、保入口序 ⇒
// 同一个值带着两种序进产物（R-41 一手：`end.node_inputs` 与 φ 组序跟书写序走）。
func (g *Builder) recordExitValue(m *ast.MapLit) (flowExit, bool) {
	if m == nil || len(m.Entries()) == 0 {
		return flowExit{}, false
	}
	outputs := make([]EndOutput, 0, len(m.Entries()))
	keys := make([]string, 0, len(m.Entries()))
	for _, entry := range m.Entries() {
		name, ok := staticMapKeyName(entry.Key)
		if !ok {
			return flowExit{}, false
		}
		ref, ok := g.resolveValueRef(entry.Value)
		if !ok {
			return flowExit{}, false
		}
		outputs = append(outputs, EndOutput{Name: name, Ref: Ref{Producer: ref.nodeID, Output: ref.output}})
		keys = append(keys, name+"\x00"+ref.nodeID+"\x00"+ref.output)
	}
	sortStrings(keys)
	// outputs 也走**规范序**（字段名升序）：身份键已经声明「与写法顺序无关」，
	// 绑定表却保入口序 = 同一个值在产物里带两种序（R-41；`canonicalEndOutputs` 头注）。
	return flowExit{identity: "record\x00" + strings.Join(keys, "\x01"), outputs: canonicalEndOutputs(outputs)}, true
}

// recordInlineFrameExit 登记**内联帧**的一次函数值出口（子函数体的 `return v`）。
//
// 只登记真正的函数值出口：QA action / attempt 臂的结果值与值位置三元的合成
// return 都有各自的宿主汇聚单元（P5 subscope return 契约），不是本帧的出口
// —— 同一判据（`inlineName != ""` ∧ 无宿主汇聚）已在 emitReturn 的值回流分支
// 里成立，这里只是同一事实的第二半。
func (g *Builder) recordInlineFrameExit(ref varRef) {
	if g.inlineName == "" || g.syntheticTernary > 0 || g.inBranchValueArm() {
		return
	}
	g.inlineExits = append(g.inlineExits, flowExit{
		ref:  Ref{Producer: ref.nodeID, Output: ref.output},
		kind: ref.kind,
		arm:  g.currentArmBlock(),
	})
}

// inlineFrameValuePhi 给内联帧的多值出口造**帧出口汇聚单元**（与流程出口 phi
// 同一条规则、同一个门控入口，只是宿主从流程换成帧）：
//
//	候选 = 各值出口的引用，源码序；臂归属（flowExit.arm）落在**非默认位**（非
//	最后一条）的候选上 —— 平台 merge 按位置取第一个存在且非零的候选，而「臂」
//	只在「未走臂里的节点不执行 ⇒ 它的候选不出现」这个意义上生效。默认位只有
//	一个 = 最后一条出口（能走到它 ⇒ 前面的臂都没走）。判据与
//	`materializeFlowExitPhi` 逐字同源（同一条规则，不另立第二份）。
//
// 帧尾（tail）不是最后一条出口时按默认位补进候选表（例如
// `if (c) { return a; } let v = b; return v;`：出口 = {a}，帧尾 = v）。
//
// 返回 nil = 单出口/零出口帧（不动既有形状）。
func (g *Builder) inlineFrameValuePhi(exits []flowExit, tail *varRef) *Aggregate {
	if len(exits) == 0 {
		return nil
	}
	cands := append([]flowExit{}, exits...)
	last := cands[len(cands)-1]
	if tail != nil && !(last.ref.Producer == tail.nodeID && last.ref.Output == tail.output) {
		cands = append(cands, flowExit{ref: Ref{Producer: tail.nodeID, Output: tail.output}, kind: tail.kind})
	}
	if len(cands) < 2 {
		return nil
	}
	armValues := make([]varRef, 0, len(cands))
	for i, e := range cands {
		ref := e.ref
		if i < len(cands)-1 {
			ref = g.gateArmCandidate(e.arm, ref, g.portTypeOf(ref.Producer, ref.Output), nil)
		}
		armValues = append(armValues, varRef{nodeID: ref.Producer, output: ref.Output, kind: e.kind})
	}
	agg := g.newValueAggregate(g.nextID(), armValues)
	g.nameUnnamedConvergenceUnit(agg)
	// Graph-level producer (like End.FlowExitMerge): the merge is not a tree
	// statement — the tree carries it as InlineStmt.Exit and the backend
	// projects it at the frame's exit entry.
	g.nodes = append(g.nodes, agg)
	g.remember(agg)
	return agg
}

// materializeFlowExitPhi gives the flow a single value source when it has more
// than one value-carrying exit. The platform's `end` node carries exactly ONE
// value binding, so N exits need a convergence node: real coze-biz exports
// wire `merge(G1:[A,B]) → end` and bind end.node_inputs to the merge. One exit
// keeps the direct binding (nothing to converge — folding a single-candidate
// phi is this function's zero-exception form of the same rule).
//
// Control flow is untouched: the exits still terminate at the canonical End;
// only the End's value binding and its entry wires move behind the merge.
func (g *Builder) materializeFlowExitPhi() {
	exits := g.uncoveredFlowExits()
	if len(exits) == 0 {
		return
	}
	node := g.find(g.endID())
	if node == nil {
		return
	}
	end, ok := node.(*End)
	if !ok || end.Answer != nil || end.NamedReturn || len(end.Outputs) > 0 {
		return
	}
	// 全部出口携带**同一个值** ⇒ 汇聚无语义：值已被该绑定单独承载，任何一条
	// 路径到达 End 时读到的都是它。单候选/零候选是这条规则在同一判据下的其它
	// 退化形（见下 `len(exits) == 1` 分支）。
	//
	// 为什么必须在这里消掉而不是留着（用户 2026-09-17 裁定 (a)：这种「两条分支
	// 写同一个值」的废话汇聚直接消掉）：平台的 merge 语义 = 「取第一个非零候选」，
	// 候选是**引用**；同一个引用出现两次的汇聚在文本形里退化成「提升变量 + 两臂
	// 赋同一字段」，再编译回来候选塌成同一个生产者 —— 反编译拒收（RS-MRG-001）。
	// 值本来就被绑定单独承载（绑定在分支**之前**，两条路径都可达），所以消掉
	// 汇聚不改变任何可观察行为，只把开发期脚手架（每臂一份的载体）去掉。
	if len(exits) > 1 && sameFlowExitValue(exits) {
		// End 只承载**一个**值：值侧改由 outputs 承载时，先前按「第一条出口」
		// 落下的 Inputs 必须清掉（渲染器把两者都发射成 node_inputs ⇒ 同一个
		// End 上出现两条互不相干的绑定，其中一条还指向臂内载体）。
		end.Inputs = nil
		if len(exits[0].outputs) > 0 {
			// record 值：End 逐字段具名绑定到**承载引用**（与 `emitMapReturn`
			// 的 ① 形逐字同形：`{name: 语义字段名, path: 端口名}`）。
			end.Outputs = exits[0].outputs
			end.NamedReturn = true
			g.dropCollapsedExitCarriers(exits)
			return
		}
		end.Inputs = []Ref{exits[0].ref}
		g.dropCollapsedExitCarriers(exits)
		return
	}
	if len(exits) == 1 {
		// One exit: the End binds it directly (a single-candidate phi is the
		// empty case of the same rule, not a node). If that exit already IS a
		// convergence unit — the host folded the arm values into
		// `merge(G1:[...])`, e.g. an else-less if whose trailing return is the
		// else arm — then the unit is the End's value AND its entry: the other
		// exit wires must join it instead of bypassing it straight into the
		// End (bypassing is what made the End read a raw producer while the
		// merge went unread).
		// 「这个出口就是某个汇聚单元的**整值**」有两种编码：legacy 单组
		// （`Group1`）与具名组 merge（空端口名 = 整个节点值，渲染为 `path: ""`）。
		// 两者同一条判据：单元就是 End 的值**并且**是它的入口，其余出口的
		// 控制流必须汇入该单元，而不是绕过它直连 End（绕过 = 单元只在一条
		// 路径上执行、End 读到它没有值：2026-09-14 实测 6/6,6/6,7/7 且 raising
		// 因「2 候选但只有一条入边」拒绝 RS-MRG-001）。
		// 具名组的**成员**口（`{merge, field}`）不是整值：End 只要那个成员，
		// 其余出口不并入该单元。
		// 该出口的值是一条 record，且逐字段承载引用**都在臂外**（identity 由
		// recordExitValue 给出）⇒ End 逐字段具名绑定那个绑定本身，而不是绑定
		// 为平台引用形式物化出的载体：载体属于**某一条臂**，另一条路径到达 End
		// 时它没有执行（读到零值），而绑定在分支之前、每条路径都可达。
		// 形状与 `emitMapReturn` ① 同形（顶层 `return v` 的既有形）。
		if len(exits[0].outputs) > 0 {
			end.Inputs = nil
			end.Outputs = exits[0].outputs
			end.NamedReturn = true
			g.dropCollapsedExitCarriers(exits)
			return
		}
		refs := []Ref{exits[0].ref}
		agg, isAgg := g.find(exits[0].ref.Producer).(*Aggregate)
		// 该出口读的是**整值**时才有下游汇聚可继续折叠；读成员口的出口到此为止。
		// 判据 = **编码**，不是口名（2026-09-18 族 1 收口，Z60 / X241 一手实测）：
		//   · 空口名 ⇒ 整值（多组件编码里「这个节点本身」的句柄，wire `path: ""`）；
		//   · 非空口名 ⇒ **只有单组件（legacy `Refs`）编码**下才是整值 —— 那时它
		//     唯一的那个口就是整值本身；多组件编码（`Groups`）下每个口都是**成员
		//     口**，读成员 ≠ 读整值（同文件 `endAggregatePortRefs` 头注、以及
		//     `junctionValueRef` 的同一编码判据）。
		// 旧写法是 `wholeValueExit := isAgg && (ref.Output != agg.SingleGroupName())`，
		// 而 `SingleGroupName()` 在**没有身份**（`RefName == ""`）时回落字面量
		// `"Group1"` —— 这些 phi 形状里 `RefName` 恒为 ""，于是那条判据退化成
		// **口名 == 字面量 `Group1` ⇔ 成员口**的命名巧合测试（一手 trace：
		// `exit.port="Group1" SingleGroupName="Group1" groups=[a b Group1]` 判成员
		// 恰对；`exit.port="zzz" SingleGroupName="Group1" groups=[a b zzz]` 判整值 ⇒ 错）。
		// 判整值的后果：End 把该汇聚**全部**口绑上，含同一交汇点另一个汇聚单元
		// （record 的逐字段组）的成员口 ⇒ 那些字段组由死变活（`optimizeGraph` 的
		// `outputConsumed` 看得见 End 的绑定），它们的死候选（零值 record 载体）
		// 一并留下 ⇒ 产物多一个节点、`compile∘raise` 一轮不动点破裂（Z60 实测
		// c0 12→13 节点、merge 6 组 ⇒ c1 17 节点；X241 / X245 同形；最小探针 = 本仓
		// `TestCozeBizExitWholeValueIsEncodingNotGroupName` 的两变体）。
		// 归属：**既有判据缺陷**，不是族 1 引入的形状 —— 族 1（汇聚单元组名 =
		// 单元身份）之前，自家产物里 phi 单元的口名恰好恒为字面量 `Group1`，
		// 该判据于是「碰巧对」；族 1 让口名变成语义名后巧合消失，缺陷才可观测。
		// 判据与形态无关（命名巧合不是编码）：`Groups` 与 `Refs` 两种编码同一条规则，
		// 无 carve-out（退出条件 = 两种编码合并）。
		wholeValueExit := isAgg && (exits[0].ref.Output == "" ||
			(len(agg.Groups) == 0 && exits[0].ref.Output == agg.SingleGroupName()))
		if !wholeValueExit {
			end.Inputs = refs
			return
		}
		// 零候选 junction 不会作为值出口登记到这里（见 resolvePendingElse：
		// 无值出口不挂尾值/不登记出口），故此处必有候选；候选为空只可能是
		// 候选**推断失败**的已登记缺口（untyped decl authority gap，
		// TestCozeBizUntypedDeclAuthorityGapPinned 逐字钉住），保持既有形状。
		end.FlowExitMerge = agg
		end.Inputs = endAggregatePortRefs(agg)
		return
	}
	// 多出口 + **每条**出口的值都是一条 record、且其逐字段承载引用都是既有引用
	// （`recordExitValue` 已给出）⇒ 收敛单元直接按字段收各出口的既有引用，
	// **不物化每臂一份的 record 装配载体**。
	//
	// 这是单出口路径那条规则的 N 元形（`end.Outputs = exits[0].outputs` 用的是
	// 同一组引用、同一个判据），也是平台一手形状：322 份真实导出的 492 条汇聚候选里，
	// 直引产出方的占绝大多数，引 `code` 节点的仅 10 条（逐类型直方图见
	// `docs/five-step/evidence/exit-convergence.json#merge_candidate_producer_types`，
	// 复算 = `scripts/five-step/evidence_exit_convergence.py`）⇒「候选 = 产出方声明口」
	// 是常态、「候选 = 逐臂 record 装配 shim 的成员口」是我们自己造的形状
	//（它让 c0 比 c1 多 N 个节点 ⇒ `C∘R∘C ≠ C`，一手实测 exit_phi 家族）。
	//
	// 载体确实只为「平台引用形式」存在（值在每条臂上要有一个承载节点）；但逐字段
	// 汇聚**不需要**它——组候选就是各臂该字段的既有引用。载体随后由
	// dropCollapsedExitCarriers 收走（与单出口路径同一个收尾）。
	if groups, ok := g.exitRecordFieldGroups(exits); ok {
		agg := &Aggregate{ID: g.nextID(), Groups: groups}
		g.nodes = append(g.nodes, agg)
		g.remember(agg)
		end.FlowExitMerge = agg
		end.Inputs = endAggregatePortRefs(agg)
		g.dropCollapsedExitCarriers(exits)
		return
	}
	armValues := make([]varRef, 0, len(exits))
	for i, e := range exits {
		ref := e.ref
		// 臂门控（arm_gate_candidate.go）：merge 按**位置**选候选，而「臂」只在
		// 「未走臂里的节点不执行 ⇒ 它的候选不出现」这个意义上生效。故非默认位
		// （非最后一条出口）的候选必须满足「候选存在 ⟺ 本出口所属的臂被执行」：
		// 值产在臂外（上游/入口参数）⇒ 该候选在任何控制流上都存在 ⇒ 恒被选中
		// ⇒ 条件对取值完全失效（一手实测 `if (n==2) { return b.body; } else
		// { return a.body; }`：merge 恒取 b.body，`a.body` 永远到不了 end）。
		// 默认位只有**一个** = 最后一条出口（能走到它 ⇒ 前面的臂都没走），
		// 故最后一条保持既有形状（与 exitRecordFieldGroups 的同一判据同源）。
		if i < len(exits)-1 {
			// rec = nil：本通道的候选是**整值**（记录/标量皆整体交付），不是
			// 「记录的一个字段组」（逐字段投影发生在 newValueAggregate 内部），
			// 故门控粒度仍是值的粒度。
			ref = g.gateArmCandidate(e.arm, ref, g.portTypeOf(ref.Producer, ref.Output), nil)
		}
		armValues = append(armValues, varRef{nodeID: ref.Producer, output: ref.Output, kind: e.kind})
	}
	agg := g.newValueAggregate(g.nextID(), armValues)
	// 流程出口 phi 没有作者名（出口语句不是声明）⇒ 平台无名组拼写，且不得与
	// 别的单元重名（R-85）。它此刻只能取「最小**可用**号」——先物化的语句级单元
	// 可能已占掉 `Group1`；那个占号者若被 IR 优化剪掉，号会在
	// `canonicalizeFlowExitGroupName`（Build 末位，优化之后）释放给出口单元。
	g.nameUnnamedConvergenceUnit(agg)
	g.flowExitPhiUnit = agg
	// Graph-level producer only: the merge is not a tree statement. Its
	// position in the structured tree is the End's entry, which backends
	// project (see ir.End.FlowExitMerge).
	g.nodes = append(g.nodes, agg)
	g.remember(agg)
	end.FlowExitMerge = agg
	end.Inputs = endAggregatePortRefs(agg)
}

// exitRecordFieldGroups 把「每条出口的值都是一条 record、且逐字段承载引用都是
// 既有引用」的出口集合投影成收敛单元的**逐字段组**：
//
//	组名 = 语言层字段名（recordExitValue 给出的 EndOutput.Name）
//	候选 = 各出口该字段的既有引用（产出方声明的口）
//
// 组名保留字段名（不是候选的口名）：两个名字是两件事 —— 字段名是**语言层语义
// 锚**（出口按它具名，见 flowExitBindingName），口名是**物理承载**。两者在
// `body`/`headers` 上恰好重合、在 `status_code`/`statusCode` 上分离，正是这条
// 区分可观测的地方（语料实证组名 ≠ 口名：`wenan ← story`）。
//
// 类型证据不足（任一出口没有 record 值描述、或字段名序列不一致）⇒ ok=false，
// 调用方保持既有载体通道 —— 与 junctionValueFieldGroups 同一条纪律：证据不足时
// 不改变既有形状，也不伪造 record 语义（这不是推断型兜底）。
func (g *Builder) exitRecordFieldGroups(exits []flowExit) ([]AggregateGroup, bool) {
	if len(exits) < 2 || len(exits[0].outputs) == 0 {
		return nil, false
	}
	// **候选必须自带臂归属（一条规则，不看出口条数、不看臂种类）**：
	// 收敛单元的候选是**有序**的（平台 `variable_merge` 取第一个非零候选），
	// 所以「第 i 个候选」必须意味着「第 i 条出口所在的那条控制流路径被走通」。
	//   - 出口的值在本臂内产出 ⇒ 候选只有在**这条臂**被走通时才产得出值 ⇒
	//     自带臂归属（载体给出的结论与它一致）⇒ 载体冗余；
	//   - 出口的值在臂**外**产出（上游，或顶层 fall-through）⇒ 它在本臂被走通
	//     与否都照常产得出值 ⇒ 在该位置上**没有选择性**，只能充当**默认位**；
	//     而默认位只有一个 = **最后一条出口**（其余出口各自被上游互斥臂覆盖，
	//     能走到最后一条 ⇒ 前面的臂都没走）。
	// 违反者 = 取值时刻在控制流上不可判定（一手实证：`let v0=http(); if(n==1)
	// {return v0;} if(n==2){let v2=http(); return v2;}` 若直引，候选序 =
	// [v0, v2]，而 v0 在两条路径上都产出且非零 ⇒ merge 恒取 v0，`return v2`
	// 的值永远到不了 end；带载体时载体落在各自臂内，取值时刻可判定）。
	// 判据不成立 ⇒ 保留载体通道（信息保全；这不是形态守卫，臂归属是 wire 上
	// 的**事实**，载体正是承载该事实的手段）。
	for i, e := range exits {
		if i < len(exits)-1 && !e.armLocalValue {
			return nil, false
		}
	}
	// ② **取值时机必须能被 raising 读回**（当前能力前沿，见下方 guard-exempt）：
	// 「全臂内产出」（无默认位）恒可读回；「一条臂内出口 + 一条臂外默认」可读回；
	// 「≥2 条臂内出口 + 臂外默认」当前**不可**——raising 的逐字段 phi 提升把
	// 臂外的前置值重物化进臂内、原绑定退化成游离语句（一手实测：`let v1=http();
	// if(n==2){let v3=http(); return v3;} if(n==4){let v5=http(); return v5;}
	// return v1;` 在 raising 侧出 `http(...);` 丢弃值的语句 + 逐轮膨胀）。
	// lowering 不得产出 raising 读不回的 wire（双向镜像 = 第 5 步不变量），故此处
	// 降级回载体通道。
	//
	//@guard-exempt: 2026-09-18 —— 这不是「仅当 X 形态才…」的规则分支，而是
	//「wire 必须可被对向读回」这条不变量在当前能力下的**前沿**：判据是「对向能不能
	// 逆」，不是出口的条数/形状本身。退出条件（下一节切片，已登记）＝ raising 侧认领
	// **多组（记录）流程出口汇聚单元**（P20 单值单元判据放宽为「Exit 读该 merge 声明的
	// 全部端口」+ 逐落点收拢各字段成整值），届时本条件整体删除、载体消除对全形态生效。
	armLocal, foreign := 0, 0
	for _, e := range exits {
		if e.armLocalValue {
			armLocal++
		} else {
			foreign++
		}
	}
	if foreign > 0 && armLocal > 1 {
		return nil, false
	}
	names := make([]string, 0, len(exits[0].outputs))
	for _, o := range exits[0].outputs {
		names = append(names, o.Name)
	}
	for _, e := range exits[1:] {
		// 字段集一致才投影（顺序也一致：同一 record 类型的字段序是同一个函数）。
		if len(e.outputs) != len(names) {
			return nil, false
		}
		for i, o := range e.outputs {
			if o.Name != names[i] {
				return nil, false
			}
		}
	}
	groups := make([]AggregateGroup, 0, len(names))
	for i, name := range names {
		grp := AggregateGroup{Name: name}
		for _, e := range exits {
			grp.Refs = append(grp.Refs, e.outputs[i].Ref)
		}
		// 组类型：取首条出口该字段引用的声明类型（与 junctionValueFieldGroups
		// 的 Type 来源同族：端口声明的类型）。取不到 ⇒ nil（渲染器按声明口类型
		// 走既有通路）。
		if t := g.portTypeOf(grp.Refs[0].Producer, grp.Refs[0].Output); t != nil {
			grp.Type = t
		}
		groups = append(groups, grp)
	}
	return groups, true
}

// endAggregatePortRefs 给出 End 读某个汇聚单元时的绑定 —— **逐口具名**，一条
// 绑定 = 该单元声明的一个输出口：
//
//   - 逐字段组（`Groups`，记录值）⇒ 每个字段一条绑定，port = 该字段的组口名
//     （组口名同时写进 merge 的 `node_outputs`，故它是**被声明的口**）；
//   - legacy 单组 ⇒ 一条 `Group1`。
//
// 为什么不能整值（`Output: ""` ⇒ wire `path: ""`）：平台一手证据里 end 的 ref
// 形态输入 **396/396 path 非空**，`end ← merge` 的 12 例**全部**读汇聚节点声明
// 的口名（`path: "Group1"`）；整值引用（all-to-all）只出现在**容器**的
// `node_outputs.*.value.value`（语料 8 例），end 位置 **0 例**。平台语义上
// `path` 就是「被引用节点的字段路径」（coze-studio `nodes/utils.go`
// TakeMapValue(输出, nodeKey + fromPath…))，空路径 = 取整个输出 —— 有定义，
// 但没有先例，且会让「单条出口 = N 条具名绑定、多条出口 = 1 条整值」自相矛盾。
func endAggregatePortRefs(agg *Aggregate) []Ref {
	if agg == nil {
		return nil
	}
	//@guard-exempt: 2026-09-15 —— 编码分支不是形态守卫：`Groups` 非空 ⇔ 渲染器
	//声明的是**各组成员端口**、`Refs` 非空 ⇔ 声明单个 `Group1`；两条分支都产出
	//「该单元声明的口名」，退出条件 = 两种编码合并（同一 pattern，不得 carve-out）。
	if len(agg.Groups) > 0 {
		refs := make([]Ref, 0, len(agg.Groups))
		for _, grp := range agg.Groups {
			refs = append(refs, Ref{Producer: agg.ID, Output: grp.Name})
		}
		return refs
	}
	return []Ref{agg.SingleGroupRef()}
}

// uncoveredFlowExits drops exits whose value a junction aggregate already
// carries. An arm's `return v` is recorded when the exit is emitted, but the
// enclosing host may then fold that same value into its own convergence unit
// (the arm is a junction candidate — `merge(G1:[v, w])`); the unit's Group1 is
// itself an exit. Keeping both would wrap a phi inside a phi of the same value
// (`merge(G1:[v, merge(G1:[v,w])])`) instead of the flat platform shape.
// "Already a candidate of another exit" is a structural fact about the
// recorded refs, not a shape guess, so the rule has no host/arity guards.
func (g *Builder) uncoveredFlowExits() []flowExit {
	covered := map[Ref]bool{}
	// Closure, not one level: an exit ref may point at a junction whose own
	// candidate is another junction (the escape-point phi of a nested
	// branch). The value the inner junction carries is carried by the outer
	// one, so the "already covered" set must be taken transitively —
	// otherwise the inner producer is re-listed as an exit of its own and the
	// End reads a merge-of-merge carrying the same value twice (the nested
	// artifact then has no faithful ncoda read: the duplicated producer is
	// emitted as a second, unreachable exit).
	queue := []*Aggregate{}
	for _, e := range g.flowExits {
		if agg, ok := g.find(e.ref.Producer).(*Aggregate); ok {
			queue = append(queue, agg)
		}
	}
	// 值通道吸收的整值也要算「已携带」：record 值被**逐字段**携带，故那些
	// 整值 ref 不再是候选（`AllRefs()` 看不到它们），但它们确实由本单元消费
	// —— 漏掉这一步，同一值会被登记为第二个出口，End 读到裸 producer，
	// 另一臂的值静默丢失（2026-09-14 实测）。
	for _, e := range g.flowExits {
		if agg, ok := g.findAny(e.ref.Producer).(*Aggregate); ok {
			for _, v := range agg.ValueSources {
				covered[v] = true
			}
		}
	}
	for len(queue) > 0 {
		agg := queue[0]
		queue = queue[1:]
		for _, c := range agg.AllRefs() {
			if covered[c] {
				continue
			}
			covered[c] = true
			if inner, ok := g.find(c.Producer).(*Aggregate); ok {
				queue = append(queue, inner)
			}
		}
	}
	out := make([]flowExit, 0, len(g.flowExits))
	for _, e := range g.flowExits {
		if covered[e.ref] {
			continue
		}
		out = append(out, e)
	}
	return out
}

// sameFlowExitValue 报告这组出口是否**全部携带同一个值**（值身份相同）。
// 调用方已保证 len >= 1；判据只有一条，没有形态/元数守卫。
func sameFlowExitValue(exits []flowExit) bool {
	for _, e := range exits[1:] {
		if e.identity != exits[0].identity {
			return false
		}
	}
	return true
}

// dropCollapsedExitCarriers 删掉「为流程出口物化出、而该出口的值本来就被
// **臂外既有引用**单独承载」的载体节点。
//
// 它们是脚手架：物化的唯一用处是给平台的引用形式提供一个「整值」引用，汇聚被
// 消掉之后（见 materializeFlowExitPhi 的同值条款）没有任何消费者。判据与
// PruneDeadPureNodes 同一条（编译器自建纯 shim ∧ 无消费者），但必须在这里落：
// 这些载体被 `ReturnStmt.Value` 当成语句级数据通道引用（statementLevelProducers
// 的保护区），而那条通道是为「分支尾值 / inline 体尾」设的 —— 对「值已由绑定
// 承载的出口」不再成立，保留它等于把死节点留在产物里（实测：c0 多两个 code
// 节点 ⇒ c0 ≠ c1 ⇒ 一轮文本漂移）。
//
// 只删**确认无消费者**的（保守：被宿主汇聚折叠过的候选不删，交给 IR 优化阶段
// 的既有规则）。id 足迹由「分配过 − 落地」自动闭合
// （backend/cozebiz/compact_pruned_ids.go），无需手工回收。
func (g *Builder) dropCollapsedExitCarriers(exits []flowExit) {
	victims := map[string]bool{}
	consumers := collectIRConsumers(g.graph, collectIRNodes(g.graph))
	for _, e := range exits {
		if len(e.outputs) == 0 {
			// 值不是 record（单口承载）⇒ 候选就是值本身，不是脚手架。
			continue
		}
		for _, cand := range []Ref{e.ref} {
			id := cand.Producer
			if id == "" || victims[id] {
				continue
			}
			cr, ok := g.find(id).(*CodeRun)
			if !ok || !cr.Synthetic || !strings.HasPrefix(cr.Code, pyast.ShimMarker) {
				continue
			}
			if consumed(consumers, id) {
				continue
			}
			victims[id] = true
		}
	}
	if len(victims) == 0 {
		return
	}
	kept := g.nodes[:0:0]
	for _, n := range g.nodes {
		if !victims[n.NodeID()] {
			kept = append(kept, n)
		}
	}
	g.nodes = kept
	published := g.allNodes[:0:0]
	for _, n := range g.allNodes {
		if !victims[n.NodeID()] {
			published = append(published, n)
		}
	}
	g.allNodes = published
	// 语句级引用（ReturnStmt.Value）与树里的 NodeStmt 一并摘掉：留一个指向已删
	// 节点的 Value 会让下游（参考目标的出口值匹配）读到悬空生产者。
	forEachStmt(g.root, func(st Stmt) {
		if rs, ok := st.(*ReturnStmt); ok && rs.Value != nil && victims[rs.Value.nodeID] {
			rs.Value = nil
		}
	})
	dropNodeStmts(g.root, victims)
}

// resolvePendingElse fills the else arm of an else-less if with the value
// of the next return, then merges both branches through an aggregator into
// the End node.
func (g *Builder) resolvePendingElse(value ast.Expr) ([]string, error) {
	p := g.pendingElse
	g.pendingElse = nil
	// 隐式 else 臂是**语句**（`return <expr>`），不是值表达式。
	//
	// 判据：then 臂**终止本流程**（thenFlowEnd）⇒ 尾 return 在**同一上下文**
	// 里也终止（同一块、同一循环/函数嵌套 ⇒ 同一降级通路）⇒ 本 if 没有续接
	// 路径（P5 无落点）。此时尾 return 走 **emitReturn 这条唯一的语句通路**
	// ——登记流程出口、由最外逃逸点的出口 phi 收口——而**不在此处另造汇聚
	// 单元**。这正是显式写法的行为：`if (…) { return x; } else { return y; }`
	// 两个 if 都不带汇聚单元、两条出口平铺进流程出口 phi（2026-09-15 一手
	// 实测：显式 3 出口嵌套 → 1 个 variable_merge，候选 = 三条记录字面量）。
	//
	// 旧行为（A12-H12 出生点，2026-09-15 一手实测）：隐式臂只做 emitValueExpr
	// ⇒ 尾 return 的**值不登记为出口**，本处另造的 junction 遂成为该值唯一
	// 载体而存活；每层 else-less if 各造一个 junction，出口 phi 又以**整值**
	// （空端口名）引用最外层 junction ⇒ 产物 = 链式 merge + 「merge 整值引用
	// merge」= 平台无此形状，反编译拒收（UNKNOWN MERGE … class-B）。
	if p.thenFlowEnd {
		ids, _, err := g.emitReturn(ast.NewReturnStmt(value), []string{p.selID}, "false")
		return ids, err
	}
	// A bare `return;` as the pending else arm carries no value — the
	// unmatched path simply terminates (void). The merge then holds only
	// the then-branch value (if any).
	var elseRef *varRef
	if value != nil {
		// 隐式 else 臂是**出口位置**：与显式 `return v` 同一条值归一化
		//（糖展开 + 端口改绑），否则同一个源构造成在两条通路上给出不同形状
		//（A12-H10：发明端口 `output` ⇒ 产物悬空 ARTIFACT_GATE）。
		if nv, changed := g.normalizedExitValue(value, g.inlineName == ""); changed {
			value = nv
		}
		ref, verr := g.emitValueExpr([]string{p.selID}, "false", false, value)
		if verr != nil {
			return nil, verr
		}
		ref = g.rebindExitPort(value, ref)
		elseRef = &ref
	}
	var armValues []varRef
	if p.thenProduced && p.thenRef != nil {
		armValues = append(armValues, *p.thenRef)
	}
	if elseRef != nil {
		armValues = append(armValues, *elseRef)
	}
	aggID := g.nextID()
	agg := g.newValueAggregate(aggID, armValues)
	g.nameUnnamedConvergenceUnit(agg)
	g.add(agg)
	// 零候选 junction 承载的是**控制汇合**，不是值（`refs` 为空 ⇔ 两臂都没有
	// 产出值）。此时按「仅副作用、没有值的出口直连 End」读（用户 2026-09-12
	// 裁定）：不挂尾值、不登记流程出口、不把 End 绑到它。给它挂 `{aggID,
	// Group1}` 尾值会**合成一个值消费点**——空聚合因「被消费」而免于
	// optimizeGraph 的零候选死结构删除，产物里留下平台无意义的空聚合
	// （L1/L2 全绿、门禁看不见；第七次 S2-gate C5 即此缺口）。控制流照旧
	// 汇合到共享 End，值侧不参与。
	//@guard-exempt: 2026-09-14 —— 这不是形态豁免，而是**同一判据的两种编码**读法
	//（legacy 单组 `Refs` / 具名组 `Groups`）：AGENTS.md「汇聚活性 = 被数据引用」
	// 明令禁止对任一编码做 carve-out，故「有没有候选」必须同时读两侧。
	// 退出条件 = 两种编码合并为一种（与账本 P01 的 D-P01-1 同一退出面）。
	// 一手实测（2026-09-24，`residuals.yaml#R-34`）：把本行钉成恒真 ⇒ **产物中性** ——
	// 全仓 62 包测试 + 生成器 397 例 + 仓内 27 导出**逐字节相同**（0 diff）。原因也已定位
	// （`#R-32`）：本 route 上 `g.tailRef` 在被消费前被重置（`build.go` 的两个绑定点探针均不触发），
	// 该形状的 `return;` 被 `emitBlock` 当作 junction 的 else 臂消费后 `continue` ⇒ junction 只承载控制流，
	// 「设成伪尾值」与「设成 nil」**不可区分**；真正拦住零候选聚合物化的是 `optimizeGraph` 的零候选删除段。
	// ⇒ 本 else 分支今天**不产生可观察差异**（不可观测的是「伪尾值 vs nil」这**一对取值的差异**，
	// 不是分支本身 —— 该分支**真的执行**：`for (…) { if (c) { break; } return; }` 这条形状两臂皆无值
	// ⇒ 走 else；一手插桩 `ZERO-CAND-AGG-BUILT agg=100006` 即此形状）。
	// **已裁（2026-09-25，`residuals.yaml#R-34` 销账）：保留。** 理由三条：
	//   ① 删它的语义 = 对零候选聚合写 `g.tailRef = &ref`，即在 IR 状态里声明「这个聚合承载值」
	//      —— 一句**假话**。它今天不产生差异，只是因为**下游** `optimizeGraph` 把该聚合当零数据
	//      消费者删掉（一手：`OPT-ZEROCAND agg=100006 consumed=false`）⇒ 删 = 把不变量**外包给
	//      后面的 pass**，与「根因优先 / 最少例外」相反：本地能成立的事实不往后传。
	//   ② 该不变量**已有机器判据兜底**（产物里不得出现合成口名 `Group1` + L1 图不变量 + 一轮往返；
	//      撤 `optimizeGraph` 的零候选段 ⇒ 三例全红）⇒ 保留它**不是**「无判据的守卫」。
	//   ③ 于是 R-34 原列的清理前置（「证明消费点在所有 route 上不可达」）**不再需要** ——
	//      收口的依据不是「不可达」，而是「就地成立 vs 外包给下游」这条取舍。
	// 分支**可达性一手实测**（2026-09-25）：把本 else 插桩成 `panic` ⇒
	// `go test ./internal/compiler/ -run TestCoZeBizLoopTerminatedElseLessIfZeroCandidate` **立刻 panic**
	// （子例 `break_terminated_then_arm_plus_valueless_return`）⇒ 分支真的被走到，「死分支」一说不成立。
	// 含撤实现必红的产物层判据 = `internal/compiler/cozebiz_loop_break_eless_if_zero_candidate_contract_test.go`。
	carriesValue := len(agg.Refs) > 0 || len(agg.Groups) > 0
	ref := junctionValueRef(agg)
	if carriesValue {
		g.tailRef = &ref
	} else {
		g.tailRef = nil
	}
	// ── tree: the pending return fills the IfStmt's Else block ─────────
	if p.treeIf != nil {
		g.consumeTreeNodes(aggID) // the merge is owned by the IfStmt
		elseBlock := &Block{}
		g.blockStack = append(g.blockStack, elseBlock)
		g.flushTreeNodes() // else-arm materialization → leaf stmts
		if elseRef != nil {
			g.appendStmt(&ReturnStmt{ID: "", Value: elseRef})
		}
		g.blockStack = g.blockStack[:len(g.blockStack)-1]
		p.treeIf.Else = elseBlock
		p.treeIf.Merge = g.find(aggID).(*Aggregate)
	}
	if g.branchDepth > 0 {
		return []string{aggID}, nil
	}
	var endID string
	if carriesValue {
		endID = g.ensureEndValue(ref, []string{aggID})
	} else {
		// 无值出口：End 不带值绑定（`ensureEnd(nil, …)`）。
		endID = g.ensureEnd(nil, []string{aggID})
	}
	g.tailRef = nil
	if p.treeIf != nil {
		// Top-level: the coalescing end is the shared End (ensureEnd).
		// The tree carries it as a trailing return so Project recreates
		// the exact edge (agg → end) with a stable end id.
		end := g.find(endID).(*End)
		if carriesValue {
			g.appendStmt(&ReturnStmt{ID: endID, Value: &ref, End: end, SharedEnd: true})
		} else {
			g.appendStmt(&ReturnStmt{ID: endID, Value: nil, End: end, SharedEnd: true})
		}
		g.consumeTreeNodes(endID)
	}
	return []string{endID}, nil
}

// emitIf maps `if (cond) { then } else { else }` to the conditional idiom:
// Selector (branch_0 / default ports) → branch subgraph → Variable
// Aggregator. Both branches run their own nodes; the selector picks the
// active branch at runtime, exactly mirroring ncoda's exclusive-branch
// semantics. The aggregator merges the branch values (ana2 φ-merge).
func (g *Builder) emitIf(stmt *ast.IfStmt, prevIDs []string, firstPort string, valueSlot string) ([]string, string, error) {
	// fail-loud 不变量（S107 族，2026-09-08）：canonical AST 进入 IR build 前
	// elseIfs 必已清空（desugar 全容器递归展平；collectAssignedVars 同处读
	// ElseIfs = 该假定的既有证据）。emitIf 只消费 ElseBody；任何残留 elif 中臂
	// 都会静默丢失（曾经 SUCCEEDED + 错码）。这里把静默腐化转爆炸失败：
	// 未来任何新的递归盲区都会在此暴露，而非靠 roundtrip 发散反推。
	if n := len(stmt.ElseIfs()); n > 0 {
		return nil, firstPort, &UnsupportedError{Construct: fmt.Sprintf("else-if chain (%d branch(es)) reaching emitIf — desugar 未展平（canonical AST 违约）", n)}
	}

	selID := g.nextID()
	// Keep the value bindings visible before either branch. A mutable record
	// declared before the if supplies the zero/default candidate for a field
	// that the other branch does not assign (copy semantics -> platform phi).
	outerVars := cloneRefs(g.varToOutput)
	// outerDeclared = junction 之前的**声明**快照（成员判据的第二轴，2026-09-12
	// C7 收口）：`T v;`（带类型无初值）是 ncoda 的提升idiom——它只登记
	// hoistedVars/hoistedTypes、既不物化节点也不登记绑定（build_call.go
	// emitVarDecl init==nil 分支）⇒ 只按 outerVars（绑定快照）判成员会把
	// 「junction 之前声明过」误判成「不是逃逸变量」，零候选 junction ⇒ 单臂
	// producer 被下游直引、另一臂写值静默丢失（switch/QA/attempt 三宿主实测；
	// if 宿主靠宿主专属尾值通道侥幸正确 = 同一规则四种形状）。成员判据 =
	// 「junction 之前**声明**」（本轴）∧ 某臂真的绑定了它（臂轴）。
	// 口径宽松度与 emitBranch 的分支绑定抬出（:844-850，全局单调、first-write
	// -wins）刻意一致：绑定算、声明不算的不对称才是本缺陷的形态。
	outerDeclared := cloneNames(g.hoistedVars)
	flow := append([]string{}, prevIDs...)
	// hoist user-fn calls in the condition: they become producer refs, so a
	// call that previously errored now compiles — and the rewritten
	// condition (fresh AST, no pass-0 mark) re-enters the native decision
	// dynamically (e.g. `if (helper(s) == "x")` → helper node + native
	// Selector on `_h1 == "x"`). §5.1.
	cond, herr := g.hoistUserCalls(stmt.Condition(), prevIDs, firstPort)
	if herr != nil {
		return nil, firstPort, herr
	}
	conds, logic, err := conditionToSelector(cond, g, &flow)
	if err != nil {
		return nil, firstPort, err
	}
	g.add(&Selector{ID: selID, Conditions: conds, Logic: logic})
	// The selector belongs to the IfStmt; flush any condition
	// materialization (code nodes) into the enclosing block first so the
	// branch subblocks only receive their own nodes.
	g.consumeTreeNodes(selID)
	g.flushTreeNodes()

	thenBlock := &Block{}
	thenTailHoisted := map[string]bool{}
	// armBase = 本 junction 的**臂入口作用域快照**（条件物化与宿主绑定已就位，
	// 且尚无任何臂的抬出）；junctionScope = 宿主**包围作用域对象**，本 junction
	// 全部臂的抬出 delta 都提交进它（跨臂 first-write-wins）。
	junctionScope := g.varToOutput
	armBase := cloneRefs(junctionScope)
	// 臂入口视图 = **junction 入口作用域快照**（armBase，含条件物化与宿主绑定）：
	// 臂的抬出 delta 只提交进 junctionScope（= 宿主包围作用域对象，跨臂
	// first-write-wins），**不得**进入本臂入口视图 —— 否则臂内**嵌套 junction**
	// 会把互斥兄弟臂的 producer 当成「junction 前绑定」（复制语义兜底候选）
	// ⇒ 跨互斥臂引用（P2 违规，L2 实测 2026-09-12）。一条规则、无宿主/臂数/
	// 形态守卫，四宿主同构。
	g.varToOutput = cloneRefs(armBase)
	thenIDs, thenRef, thenHad, thenProduced, _, err := g.emitBranch(thenBlock, stmt.ThenBody(), selID, "true", thenTailHoisted, junctionScope)
	if err != nil {
		return nil, firstPort, err
	}
	thenVars := cloneRefs(g.lastBranchVars)
	thenReturned := g.lastBranchFlowReturned
	thenVoidReturned := g.lastBranchVoidReturned
	treeIf := &IfStmt{ID: selID, Cond: g.find(selID).(*Selector), Then: thenBlock}
	// synthElse 携带 else-less live 臂的合成空 else（统一汇聚规则，RED C/D）：
	// 非 nil 时共享汇聚机制以空 else 臂执行（空臂不写任何变量 → 对侧 pre-if
	// 兜底/分支 shim 生效）；nil = 真实 else body。
	var synthElse *ast.Block
	if stmt.ElseBody() == nil {
		// else-less if: the next return in the enclosing body IS the else
		// arm; the unmatched path falls through the selector's false port.
		// 值空判据的**第二条通道**（2026-09-21，与 A12-H4 同一条判据、同一个位置）：
		// 「该臂在值上是空的」除「平台承载不了整值」之外，还有「该值**可证类型零**」
		// 这一种 —— 此时中继承载的是一个零值，而 merge 的 zero 语义（候选缺省 =
		// 类型零）已经把它承担掉了（J3/P04：不为没写臂物化零值载体 = 消灭死载体）。
		// 谓词 = isZeroCarrierNode，与 materializeBranchDefault 的零值豁免**同一个**
		// （`build_return.go` 下游那处注释原话：「与 gateArmCandidate 同一条判据、
		// 同一个理由，不得分叉」）—— 本处只是把同一句话补到第二个物化点。
		// 不收的实测代价：中继的消费者为零 ⇒ 死纯节点清理（TextProcess）会把它删掉，
		// 下一轮 compile 又长回来 ⇒ R∘C 无不动点。证据与复跑命令：
		// docs/five-step/evidence/textprocess-liveness-oscillation.md
		//
		// 第三条通道（**纯 no-op 臂**，2026-09-24，与上一条同族、同一个理由）：
		// 中继的**全部**存在理由 = 给「本臂的值」一个**臂内**载体。本臂零节点
		// （`!thenHad`）∧ **零重绑定**（臂相对视图 `thenVars` 为空）∧ 无值 return
		// ⇒ 该臂在值上什么都没做（两侧绑定同一个 producer，值保全）⇒ **没有承载
		// 对象**，中继建成即有消费者为零的死载体。不建 ⇒ 与真·空臂同形（真口边由
		// P15 端口义务表闭到续接点；「两臂都空也不能算错」= 2026-09-13 裁定）。
		// 一手（**与本轮记录折叠无关**：HEAD 二进制即可复现，纯叶类型）：
		//   `file<audio> v = fp1; if (c) { v = fp1; } return v;`
		//   ⇒ 旧行为铸出无消费者的 `text {{v}}`（c0 = start/condition/**text**/end），
		//     反编译长出一句臂内 `let v1 = \`${fp1}\``，下一轮该句带节点 ⇒ c1 少一个
		//     节点 ⇒ **C∘R 无不动点**。证据与复跑 = docs/five-step/evidence/arm-relay-noop-arm.md
		armNoOp := !thenProduced && !thenReturned && !thenVoidReturned && len(thenVars) == 0
		if !thenHad && !armNoOp && thenRef != nil && thenRef.output != wholeValueMarker &&
			!isZeroCarrierNode(g.findAny(thenRef.nodeID)) {
			// no-node branch: forward the value through a TextProcessor so
			// the selector's true port has a concrete edge.
			//
			// **中继只承载「平台承载得了的值」**（A12-H4 连带，2026-09-17）：
			// 这里的臂尾值是**进入该臂之前**的尾值，而整值绑定（`@whole`：http /
			// plugin / 结构性 foreign code —— 平台没有「整个 record」那个口）没有
			// 单一承载口 ⇒ 文本中继把它写成一个产出方不存在的口。此时该臂在**值
			// 上**是空的（作者也没引用那个值）⇒ 不建中继：空臂是合法结构，真端口
			// 的义务由 lowering 的端口义务表闭到**续接点**（P15「臂内无节点 ⇒ 该臂
			// 欠该端口一条显式边」；2026-09-13 用户裁定「两臂都空也不能算错」）。
			// 判据 = 「平台有没有承载形式」（与 A12-H4 同一条），不是形态枚举。
			//
			// 一手证据：`let a = http(…); if (a.status_code == 200) { }` 在本规则
			// 之前同样编不过 —— 中继读的 `output` 口不存在（产物门禁
			// `ref 100002.output does not resolve`，把我们的缺陷说成平台问题）；
			// 插件恰好声明同名口 `output` 时旧行为侥幸通过，A12-H4 把绑定口统一成
			// 整值标记后暴露成语言错（语料夹具 d2_x20 二轮 R∘C 实测）。
			id := g.nextID()
			g.add(&TextProcess{
				ID:       id,
				Method:   "concat",
				Template: "{{v}}",
				Inputs:   []Source{{Path: []string{"v"}, BlockID: thenRef.nodeID, Name: thenRef.output}},
			})
			thenIDs = []string{id}
			thenHad = true
			// The relay is part of the IfStmt structure (its edge is the
			// selector's true port), not a sequential then-block statement.
			treeIf.Relay = g.find(id)
			g.consumeTreeNodes(id)
		}
		// R2（family A, 2026-09-08）：else-less if 的 pendingElse（尾 return =
		// 排他 else 臂）只在 then 臂**不延续到本块下一语句**（终止）时成立：
		// 终止时 false 路径是到达尾 return 的唯一活路径。then 臂 live（void
		// 副作用延续）时，尾 return 是双臂**公共延续** —— 不得设 pendingElse，
		// 否则尾 return 被 emitValueExpr 装箱成 false 臂 code shim + 单候选
		// merge，end 引用整组 record（X265.min 实证：N2 退化
		// `v6={"Group1":{...}}; return v6.Group1;`）。
		// "终止"判据（递归封闭，无形态枚举；独立编译器专家复核 2026-09-08
		// 补全三终止维度）：
		//   ① flow-end return（thenReturned/thenVoidReturned —— 主流程/
		//      parallel 分支终止，P5）；
		//   ② 子作用域值 return（thenProduced 且无 flow-end 标志 —— inline
		//      函数 / QA / attempt 分支值 return：终止分支、值回流给宿主，
		//      不落回本块；保持既有 pendingElse 语义）；
		//   ③ break/continue 循环终止（thenIDs 全为 breakish —— pendingElse
		//      reset 路径的 isBreakish 剔除语义保持一致）。
		// 三者皆非 = live 臂（M2/x265 形状）→ 公共延续。
		armTerminated := thenReturned || thenVoidReturned || thenProduced
		if !armTerminated && len(thenIDs) > 0 {
			armTerminated = true
			for _, id := range thenIDs {
				if !g.isBreakish(id) {
					armTerminated = false
					break
				}
			}
		}
		if !armTerminated {
			// 统一汇聚规则（merge 框架 RED C/D, 2026-09-08）：else-less **live**
			// 臂单臂写 hoisted 变量（`if (c) { agg = "x"; } use(agg);`）——
			// 分支末绑定 ≠ if 前绑定，post-if 读必须拿到逐路径值。旧路径把 if
			// 当 statement-only 直接早退：汇聚机制未走到 → 分支写变孤儿、
			// post-if 读绑 pre-if 零值（数据丢失；标量/record 探针实证 0
			// Aggregate）。判据与 if/else 相同 = changedHoisted（标量∪record，
			// else 侧 = outer 自身不改变量）；纯 void 副作用臂（无 changed
			// 变量，throw_error 等）保持 statement-only 原样。
			changedScalars, err := g.changedHoistedScalars(outerDeclared, outerVars, thenVars, outerVars)
			if err != nil {
				return nil, firstPort, err
			}
			changedRecords, err := g.changedHoistedRecords(outerDeclared, outerVars, thenVars, outerVars)
			if err != nil {
				return nil, firstPort, err
			}
			if len(changedScalars) == 0 && len(changedRecords) == 0 {
				g.appendStmt(treeIf)
				return thenIDs, "false", nil
			}
			// 合成空 else → 落入下方共享汇聚机制（与 if/else 同一条路径）：
			// then 臂写 = 真 producer 候选；空 else 臂不写 → 核心按 junction 前
			// 绑定兜底（materializeBranchDefault / 可证零值真相）—— 平台
			// first_not_null 只会在 then producer 未执行的 false 路径选中它
			// （复制语义保留原值）。
			synthElse = ast.NewBlock(nil)
		}
		if synthElse == nil {
			// 仅 terminated 臂进入 pendingElse（尾 return = 排他 else 臂）。
			// live+changed 臂（synthElse != nil）已落入下方共享汇聚机制，
			// 不得再设 pendingElse 把公共延续当排他 else（X265.min 教训）。
			// Statement-only then-branches (void tool calls — throw_error/
			// add_records) produce no value: the pending-else merge must not
			// carry the branch's fallback ref (the pre-if tail — a port the
			// branch never set), or the aggregate names a port the producer
			// does not declare (引用变量不存在). The continuation tail stays
			// at the pre-if value (the unmatched path falls through).
			var pendingThen *varRef
			if thenProduced {
				pendingThen = thenRef
			}
			g.pendingElse = &pendingElseState{selID: selID, thenIDs: thenIDs, thenRef: pendingThen, thenProduced: thenProduced,
				thenFlowEnd: thenReturned || thenVoidReturned, treeIf: treeIf}
			if pendingThen != nil {
				g.tailRef = pendingThen
			}
			g.appendStmt(treeIf)
			return thenIDs, "false", nil
		}
	}
	elseBlock := &Block{}
	elseTailHoisted := map[string]bool{}
	elseBody := stmt.ElseBody()
	if synthElse != nil {
		elseBody = synthElse
	}
	// 臂入口视图 = **junction 入口作用域快照**（armBase，含条件物化与宿主绑定）：
	// 臂的抬出 delta 只提交进 junctionScope（= 宿主包围作用域对象，跨臂
	// first-write-wins），**不得**进入本臂入口视图 —— 否则臂内**嵌套 junction**
	// 会把互斥兄弟臂的 producer 当成「junction 前绑定」（复制语义兜底候选）
	// ⇒ 跨互斥臂引用（P2 违规，L2 实测 2026-09-12）。一条规则、无宿主/臂数/
	// 形态守卫，四宿主同构。
	g.varToOutput = cloneRefs(armBase)
	elseIDs, elseRef, _, elseProduced, _, err := g.emitBranch(elseBlock, elseBody, selID, "false", elseTailHoisted, junctionScope)
	if err != nil {
		return nil, firstPort, err
	}
	elseVars := cloneRefs(g.lastBranchVars)
	elseReturned := g.lastBranchFlowReturned
	elseVoidReturned := g.lastBranchVoidReturned
	treeIf.Else = elseBlock

	// If every exclusive arm returned, there is no live convergence value:
	// assignments in the arms are side-effect bindings, not phi candidates.
	// The whole IfStmt terminates; any statement after it is unreachable.
	// Creating an aggregate here would turn those local bindings into a
	// synthetic merge and roundtrip back as a value-producing arm.
	if thenReturned && elseReturned && thenVoidReturned && elseVoidReturned {
		g.branchReturns = true
		g.branchFlowReturns = true
		g.appendStmt(treeIf)
		return nil, "", nil
	}
	// 内联帧：两臂都以**函数值出口**结束（`if (c) { return a; } else { return b; }`）
	// ⇒ 本 if 没有活的续接臂，值由帧出口 phi 统一收（两臂的值都已在 emitReturn
	// 登记）。不建本处的 junction —— 建了就与帧出口 phi 重复：产物里会出现
	// 「merge 读 merge」（平台无此形状）。读法与上一条主流程裁定**同源**：
	// 「两臂都终止 ⇒ 没有活的汇聚值」，只是终止的判据在内联帧是 thenProduced /
	// elseProduced（子作用域值 return）而不是 flow-end return。
	if g.inlineName != "" && thenProduced && elseProduced {
		g.appendStmt(treeIf)
		return nil, "", nil
	}
	// A branch whose explicit return terminates the whole flow is not a live
	// convergence arm.  If only one arm returns, assignments in the continuing
	// arm stay bindings in the enclosing block; any real mutual exclusion with
	// an outer if is materialized at that outer escape point.  Creating a phi
	// here would let a nested variable_merge masquerade as a branch value and
	// roundtrip back as a synthetic record wrapper.
	if thenReturned != elseReturned {
		continuingVars := thenVars
		continuingIDs := thenIDs
		if thenReturned {
			continuingVars = elseVars
			continuingIDs = elseIDs
		}
		for name, ref := range continuingVars {
			if g.hoistedVars[name] {
				g.varToOutput[name] = ref
			}
		}
		g.branchReturns = false // the continuing arm leaves this if live
		g.branchFlowReturns = false
		g.appendStmt(treeIf)
		return continuingIDs, "", nil
	}
	// Statement-only branches (void tool calls — throw_error/add_records)
	// produce no value; only branches that set their own tailRef contribute
	// an aggregate ref. The platform rejects a merge variable that names a
	// port the producer does not declare (引用变量不存在).
	// 臂门控（arm_gate_candidate.go）：臂尾值若是臂外 producer（`return b;` /
	// 值位置三元的臂外值），直引会被 merge 按位置恒取 ⇒ 条件失效。
	// 门控走 armTailValues（**保留 kind**）：kind 是端口模型的输入
	// （`aggregateFieldPort` 按它决定「字段名是口名 / 是口下的子路径」），
	// 用裸 varRef{nodeID, output} 重建会把它清空 ⇒ 记录值的逐字段组投影退化
	// 成整值口（一手实证 w1：`let p = (c) ? {left,right} : {left,right}; x = p.left`
	// ⇒ 候选口由 `left` 变 `result`，读回 RS-MRG-002 类型不一致）。
	//
	// **候选按臂序收齐后统一门控**（与 junction phi / 流程出口汇聚 / switch·QA·
	// attempt 臂尾值**同一条**规则与同一个入口）：门控判据含**位置**，而位置只有
	// 整组候选收齐后才成立。逐臂即时门控会把**默认位**（最后一条）也包一层载体
	// ——默认位的臂外产出方正是合法默认值，多包一层 = 多一个节点、多一层控制
	// 依赖，且让两条同义拼写产出不同 wire（一手实测：`let x = a != "" ? a : b;`
	// 多一个 code 载体，而 `string x; if (a != "") { x = a; } else { x = b; }`
	// 不多 —— 同语义两形状，违反「唯一表示」）。
	var armTails []armTailValue
	if thenProduced && thenRef != nil {
		armTails = append(armTails, armTailValue{arm: thenBlock, v: *thenRef})
	}
	if elseProduced && elseRef != nil {
		armTails = append(armTails, armTailValue{arm: elseBlock, v: *elseRef})
	}
	// φ 核心（唯一实现）：组装序列收在 assembleArmJunction（junction_phi.go），
	// 本宿主只送数据 —— 臂集合（端口 / 臂体 / 臂末视图 / 臂尾绑定）· 臂尾值通道 ·
	// junction 前视图（绑定与声明）。尾值口径（「零候选 ⇒ 没有值」）与重定向口径
	// 见该函数头注；本函数内**不再**持有任何汇聚组装序列。
	//
	// 本刀（2026-09-20，第 1 步 φ 五宿主合一第三刀）删掉的是本宿主自带的**第二套
	// 组装序列**：`aggPlans`（record 逐字段规划的独立实现）· `scalarPhiGroups` /
	// `scalarHoistedCandidates`（标量通道的独立实现）· 以及它们各自的
	// `varToOutput` 重定向循环。这些与 `planArmJunctionPhi` 是同一规则的两份实现
	// （同一批 helper：`refForAggregateField` / `materializeBranchDefault` /
	// `armGateCandidates` / `junctionPhiType`），分歧史见 J §10.1。
	arms := []junctionArm{
		{Port: "true", Block: thenBlock, Vars: thenVars, TailBound: thenTailHoisted},
		{Port: "false", Block: elseBlock, Vars: elseVars, TailBound: elseTailHoisted},
	}
	res, err := g.assembleArmJunction(arms, outerDeclared, outerVars, armTails, valueSlot)
	if err != nil {
		return nil, "", err
	}
	treeIf.Merge = res.Primary
	g.consumeTreeNodes(res.Primary.ID)
	g.flushTreeNodes()
	g.appendStmt(treeIf)
	g.appendChainedArmJunctions(res)
	return res.FlowIDs(), "", nil
}

// junctionValueRef reports the VALUE binding a junction exposes to whoever
// consumes the junction's result as a value (value-position ternary and the
// main-flow exit convergence; both are entries of the same value channel).
//
// 组形状 = f(值类型)（同 `junctionValueRef` 的调用者 emitIf 的通道选择）：
//   - 具名组形态（含 record 逐字段组）：平台 merge 声明的是**各组成员端口**，
//     整值就是 merge 节点本身（渲染成 `{ref_node, path: ""}`，与流程出口汇聚
//     的整记录消费同形）；
//   - legacy 单组通道（标量值）：整值是渲染器声明的 `Group1` 口。
//
// 本函数是**唯一**给出「junction 整值口名」的地方：值位置的调用点曾经各自
// 硬编码 `Group1`（`build_call.go` / `build_expr.go` ×2），record 值下即指向
// 不存在的端口——CB-1 的出生形态。禁止在调用点重新硬编码端口名。
func junctionValueRef(agg *Aggregate) varRef {
	if agg == nil {
		return varRef{}
	}
	//@guard-exempt: 2026-09-14 —— 同上：`Groups` 非空 ⇔ 渲染器声明的是**各组成员端口**
	//（整值 = merge 节点本身，wire 上 `path: ""`），否则是 legacy 单组 `Group1`。
	// 两种编码必须同判据读（禁止 carve-out）。退出条件 = 编码合并（同 D-P01-1）。
	if len(agg.Groups) > 0 {
		return varRef{nodeID: agg.ID, output: "", kind: "aggregate"}
	}
	return varRef{nodeID: agg.ID, output: agg.SingleGroupName()}
}

// junctionValueFieldGroups projects the values a junction's arms produce onto
// per-field convergence groups when **every** producing arm's value is a
// declared record.  It is the value-channel counterpart of the hoisted-var
// plan (`planArmJunctionPhi`): the platform shape is a function of the VALUE
// TYPE, never of the syntax that produced the junction.
//
// 候选 = 各臂产出该字段的**声明端口**（`refForAggregateField` / P07 端口模型，
// 与逐字段 phi 逐字同判据）；不声明该字段的臂不贡献候选（绝不发明端口）。
// 返回 nil = 不投影（无 record 证据 / 字段集不一致 / 无可解析候选），调用方
// 保持既有通道 —— 这不是推断型兜底：类型证据不足时我们不改变既有形状，也不
// 伪造 record 语义。
func (g *Builder) junctionValueFieldGroups(arms []varRef) []AggregateGroup {
	if len(arms) == 0 {
		return nil
	}
	// 参照形状 = **首臂**声明的记录类型；其余臂逐个比对（不设循环内哨兵：
	// 「本臂就是参照」由切片位置直接表达）。
	ref := g.portTypeOf(arms[0].nodeID, arms[0].output)
	if ref == nil || len(ref.Props) == 0 {
		return nil
	}
	for _, a := range arms[1:] {
		t := g.portTypeOf(a.nodeID, a.output)
		if t == nil || len(t.Props) == 0 {
			return nil
		}
		// 字段集一致才投影：不一致的臂产出（异型值选择）没有单一声明形状可依，
		// 逐字段组会指向某臂未声明的端口 ⇒ 保持原通道。
		if len(t.Props) != len(ref.Props) {
			return nil
		}
		for field := range ref.Props {
			if t.Props[field] == nil {
				return nil
			}
		}
	}
	var groups []AggregateGroup
	for _, field := range sortedTypeFields(ref) {
		grp := AggregateGroup{Name: field, Type: ref.Props[field]}
		for _, a := range arms {
			// 候选端口 = P07 端口模型给出的声明端口（`aggregateFieldPort`）：
			// 记录字面量 shim 的成员口由渲染器展开成扁平端口（`left`），
			// 其余 record 值口（llm payload `text` 等）保留基段成 dotted 路径。
			// 字段集已在上方按各臂声明形状校验一致，故这里不再二次过滤——
			// 二次过滤会因「展开成扁平端口的成员不在 IR OutTypes 里」而误删
			// 合法候选（实测：全字段候选被清空 ⇒ 投影静默失效）。
			grp.Refs = append(grp.Refs, g.refForAggregateField(a, field))
		}
		if len(grp.Refs) > 0 {
			groups = append(groups, grp)
		}
	}
	return groups
}

// newValueAggregate builds the convergence unit for a junction whose arms
// produced VALUES (the value channel: value-position ternary, else-less-if
// tail value, switch/QA/attempt arm values, main-flow exit phi).
//
// 组形状 = f(值类型)，一处实现（v 同 `junctionValueFieldGroups`）：
// 记录型值 ⇒ 逐字段组（候选 = 各臂的声明字段端口）；其余 ⇒ legacy 单组通道
// （`Refs`，渲染为 `Group1`）。**禁止**在宿主/源语法侧再分叉一次形状
// （CB-1 的出生形态：同一语义按「哪条语法产出了它」选两种平台形状）。
func (g *Builder) newValueAggregate(aggID string, armValues []varRef) *Aggregate {
	agg := &Aggregate{ID: aggID}
	for _, a := range armValues {
		agg.Refs = append(agg.Refs, Ref{Producer: a.nodeID, Output: a.output})
	}
	if groups := g.junctionValueFieldGroups(armValues); len(groups) > 0 {
		agg.ValueSources = agg.Refs
		agg.Refs = nil
		agg.Groups = groups
	}
	return agg
}

// junctionValueRefOf reports the value binding of the junction node `id`
// (the aggregate `emitIf` just built).  Fail loud when the node is not a
// convergence unit: a value position whose junction produced no value has no
// value to bind, and a fabricated port reference would ship an illegal
// product instead of an error.
func (g *Builder) junctionValueRefOf(id string) (varRef, error) {
	agg, ok := g.findAny(id).(*Aggregate)
	if !ok {
		return varRef{}, &UnsupportedError{
			Construct: fmt.Sprintf("value-position junction %s is not a convergence unit (no value produced)", id),
		}
	}
	return junctionValueRef(agg), nil
}

// emitBranch lowers one if/else branch body starting from the selector's
// branch port. It returns the branch junction, the value the branch
// produces, whether any node was created, whether the branch itself
// produced a value (tailRef was set INSIDE the branch), and any unconsumed
// port. Branch variables are scoped (ana2 snapshot/restore).
//
// The value fallback to the pre-branch tailRef is ONLY for block
// continuation semantics — a statement-only branch (e.g. ending in a void
// tool call) must not leak the outer tailRef into aggregate refs: the
// platform rejects a merge reference to a port that node does not declare
// (引用变量不存在). Callers use `produced` to decide aggregate inclusion.
func (g *Builder) emitBranch(tb *Block, block *ast.Block, entryID, portID string, hoistedTailVars map[string]bool, junctionScope map[string]varRef) ([]string, *varRef, bool, bool, string, error) {
	g.branchDepth++
	// junctionScope = **宿主（if / switch / ask-QA / attempt）的一个 junction 的
	// 包围作用域对象**，本臂的「抬出 delta」写进它，junction 结束时它就是
	// `g.varToOutput`（自然提交，无需额外收口点）。它是**同一个 map 对象**在
	// 本 junction 的全部臂之间共享 ⇒ 跨臂 first-write-wins（与历史逐字一致），
	// 而**臂入口视图**（armEntry）由宿主按 junction 入口快照重置 ⇒ 兄弟臂的
	// 抬出永不进入下一个臂的入口视图（内层 junction 的 `outerVars` 因此不含
	// 互斥兄弟臂的 producer；旧实现把抬出写进「本臂入口快照对象」，而那个对象
	// 正是下一个臂的入口 ⇒ 跨互斥臂候选 = P2 违规，2026-09-12 S2-gate 实测）。
	g.varToOutput = cloneRefs(g.varToOutput)
	// armEntry = 本臂**入口**绑定（emitStmts 之前）。臂视图（lastBranchVars）
	// 是**臂相对**的重绑定集合，由入口/出口 diff 得出，见函数尾。
	armEntry := cloneRefs(g.varToOutput)
	tailSnapshot := g.tailRef
	// Track returns relative to this branch.  branchReturns is also used by
	// the function-level implicit-end decision, so a previous sibling branch
	// may already have set it.  More importantly, a branch-local `let
	// p.op(...)` is a value binding in the lexical environment but is not a
	// branch result for if/intent/QA aggregation.  Only an explicit return
	// (or a nested branch that returns) may make the branch's tail a merge
	// candidate.  Without this boundary a side-effect plugin is projected as
	// a pseudo `output` port and round2 fails with FIELD_NOT_FOUND.
	savedBranchReturns := g.branchReturns
	savedBranchFlowReturns := g.branchFlowReturns
	savedBranchVoidReturns := g.branchVoidReturns
	g.branchReturns = false
	g.branchFlowReturns = false
	g.branchVoidReturns = false
	// A branch does NOT inherit the outer block's current value as its own:
	// only statements executed INSIDE the branch set its value. Otherwise a
	// statement-only branch (void tool call / void inline helper) would
	// leak the pre-branch tailRef into the if/else aggregate — the
	// platform rejects a merge reference to a port the producer does not
	// declare (引用变量不存在).
	g.tailRef = nil
	nodeCount := len(g.nodes)
	g.blockStack = append(g.blockStack, tb)
	g.armStarts = append(g.armStarts, 100000+g.nextNodeID)
	g.armBlocks = append(g.armBlocks, tb)
	defer func() {
		g.armStarts = g.armStarts[:len(g.armStarts)-1]
		g.armBlocks = g.armBlocks[:len(g.armBlocks)-1]
	}()

	junction, unused, err := g.emitStmts(block.Stmts(), []string{entryID}, portID)

	// 分支尾值提升变量收集（emitIf 重定向用）：分支内赋值 hoisted 变量且其
	// 绑定 == 分支尾值（tailRef）→ 记入 hoistedTailVars。emitIf 据此把聚合
	// 器输出重定向回这些变量（record/return 下游引用解析到 if/else 汇聚值，
	// 而非首个分支绑定/零值节点——X212 agg、hoist-zero-default 实证）。分支
	// 无值（尾值 nil）→ 无变量匹配；仅当绑定==尾值才记（分支尾后另有语句
	// 的变量不重定向，保持其最后赋值语义）。
	if hoistedTailVars != nil {
		for name, ref := range g.varToOutput {
			if g.hoistedVars[name] && g.tailRef != nil &&
				ref.nodeID == g.tailRef.nodeID && ref.output == g.tailRef.output {
				hoistedTailVars[name] = true
			}
		}
	}

	// 分支变量提升（hoistEscapedBranchVars）：分支内对裸 var v 的赋值
	// （v = expr）物化的节点引用跨分支保留 —— coze 画布变量全局可见，
	// 分支外 coalesce/merge 引用 v.<field> 需要可寻址（否则 condition
	// materialization operand 不可解析）。非 hoisted 变量维持分支作用域
	// （快照回滚，分支变量不泄漏到外层）。
	for name, ref := range g.varToOutput {
		if g.hoistedVars[name] {
			if _, had := junctionScope[name]; !had {
				junctionScope[name] = ref
			}
		}
	}
	// 臂视图（lastBranchVars）= **臂相对**的重绑定集合：名字进视图 ⇔ 本臂出口
	// 绑定 ≠ 本臂**入口**绑定（含本臂新引入的名字）。它**不是**出口作用域快照。
	//
	// 为什么必须臂相对：分支绑定抬出（hoistEscapedBranchVars）对**无 junction
	// 前绑定**的名字把本臂的写提交进 junction 包围作用域（first-write-wins），
	// 而**出口作用域**（`g.varToOutput`）里带着**本 junction 其它臂**的绑定
	// ⇒ 若拿出口快照当臂视图，未写臂会被判成写过。junction 规划的每条谓词都
	// 拿臂视图问「本臂写了吗」
	// （armGateCandidates / record 通道 thenWrote/elseWrote —— 今日唯一实现 =
	// `planArmJunctionPhi`），于是未写臂被误判成写过，产出指向
	// **兄弟臂 producer** 的幻影候选（2026-09-12 M2 实测）：
	//   - 同组重复候选（语料 232 组 0 例 = 非平台形状），且
	//   - raising「同臂同位重复候选 → 角色不可证」⇒ RS-MRG-001 拒收自家产物。
	// 用入口 diff 把「本臂写没写」还原成一个**臂级事实**（无宿主/臂数/形态枚举
	// 守卫；四宿主 if/switch/QA/attempt 自动同构）。
	//
	// 判据是**候选集合**不是源码文本：臂把名字写成**与入口同一个 producer**
	// （如两臂都 `v = y`）时该臂不算重绑定——候选仍是同一个 producer，值保全
	// （被丢的那条只是同组后位重复），但 r0 里那一行文本不会回到 r1。
	armVars := make(map[string]varRef, len(g.varToOutput))
	for name, ref := range g.varToOutput {
		if e, ok := armEntry[name]; ok && e.nodeID == ref.nodeID && e.output == ref.output {
			continue
		}
		armVars[name] = ref
	}
	g.lastBranchVars = armVars
	g.varToOutput = junctionScope
	hadNodes := len(g.nodes) > nodeCount
	returned := g.branchReturns
	flowReturned := g.branchFlowReturns
	voidReturned := g.branchVoidReturns
	g.lastBranchFlowReturned = flowReturned
	g.lastBranchVoidReturned = voidReturned
	// Preserve the function-level fact while keeping the per-branch result
	// local to this emitBranch invocation.  Nested emitBranch calls restore
	// their own fact before returning, so this also composes recursively.
	g.branchReturns = savedBranchReturns || returned
	g.branchFlowReturns = savedBranchFlowReturns || flowReturned
	g.branchVoidReturns = savedBranchVoidReturns || voidReturned
	produced := returned && g.tailRef != nil
	value := g.tailRef
	if value == nil {
		value = tailSnapshot
	}
	g.tailRef = tailSnapshot
	g.branchDepth--
	if err != nil {
		return nil, nil, false, false, unused, err
	}
	// Flush after restoring tailRef: branch tail values are carried by the
	// container statement (IfStmt/AttemptStmt/QaStmt aggregates), so the
	// leaf NodeStmt values here point only at outer-scope bindings.
	g.flushTreeNodes()
	g.blockStack = g.blockStack[:len(g.blockStack)-1]
	return junction, value, hadNodes, produced, unused, nil
}

// refsProducedInCurrentArm 报告一组 record 出口的逐字段引用是否**全部在本臂内
// 产出**（构造期水量线判据：节点 id 在本臂入口 `nextNodeID` 之后分配）。
//
// 判据不猜任何东西：构造顺序 = 结构化树的深度优先展开序，故「本臂内产出」与
// 「本臂块子树里产出」同构。水量线与 node id 都在 **id 空间**（`100000+序号`，
// 与 nextID 的拼写同一把尺子 —— 两把尺子混用会让判据恒真）。不在任何臂内
// （顶层出口）⇒ false —— 顶层出口本来就没有臂可归属。
func (g *Builder) refsProducedInCurrentArm(outputs []EndOutput) bool {
	if len(g.armStarts) == 0 || len(outputs) == 0 {
		return false
	}
	start := g.armStarts[len(g.armStarts)-1]
	for _, o := range outputs {
		if nodeIDValue(o.Ref.Producer) < start {
			return false
		}
	}
	return true
}

// nodeIDValue 把构造期 node id（`1000NN` / `900001`）还原成整数，用于与水量线
// 比较。非数字（不该出现）⇒ -1（保守判为「臂外」，不给载体消除开口子）。
func nodeIDValue(id string) int {
	n, err := strconv.Atoi(id)
	if err != nil {
		return -1
	}
	return n
}

// varAggPlan is one hoisted record variable's per-field convergence plan for
// an if/else junction. A single Aggregate models ONE value; when different
// branches lift DIFFERENT records as their tail values (S40/S113 双 LLM
// LLMResponse：text string vs text record{wenan}），the junction needs one
// Aggregate per variable — field-named merge groups of two records must not
// share one variable_merge node (output ports are keyed by field name, and
// the two variables' same-named fields carry different types).
type varAggPlan struct {
	name   string
	groups []AggregateGroup
}

// cloneNames snapshots a name set.  Junction phi membership is decided on two
// axes — 声明（junction 之前声明过：`T v;` 的提升 idiom 不产生绑定）与绑定
// （junction 之前的 varToOutput）——后者用 cloneRefs，前者用本助手（C7，
// 2026-09-12）。两者口径必须同源：任何一个轴单独生效都会让规则变成形态枚举。
func cloneNames(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// sortedTypeFields returns record fields in source-independent order.
func sortedTypeFields(t *Type) []string {
	if t == nil || len(t.Props) == 0 {
		return nil
	}
	out := make([]string, 0, len(t.Props))
	for name := range t.Props {
		out = append(out, name)
	}
	sortNamesNatural(out)
	return out
}

// aggregateFieldPort maps (whole-record binding, record field) onto the
// semantic output port of the record field.  A whole-value binding (empty
// output, or the transparent whole-dict/whole-envelope carriers result /
// output / response) exposes its record fields as top-level ports: the
// field name IS the port (fieldPort).  A binding that already points at a
// specific record-typed MEMBER port (llm payload `text`, a code shim's
// record-typed field port) keeps that base segment: its fields live UNDER
// the port, so the candidate must be the dotted path (text.wenan), not the
// bare field (wenan).  Emitting the bare field loses the record boundary:
// decompile reverses the producer to a record literal that does not declare
// the bare field at top level (or an llm payload whose single-field ref is
// re-read as the whole payload), and the next compile fails with
// TYPE_MISMATCH / FIELD_NOT_FOUND (S40/S113 族 C record|string 异型 phi
// @b4, 2026-09-04: `agg = <record>` into a string-declared hoist).
//
// The "is the base a container of this record?" question is answered by the
// producer's DECLARED PORT TYPE, never by whether some port happens to carry
// the leaf field name (S119×2 / probe E, 2026-09-10): a multi-port code node
// declares one port per field, so `image` is a *sibling flat port*, not a
// container of the record — while a producer that declares both `text: RT2`
// and a leaf `wenan: string` must still yield `text.wenan` for the record
// bound at `text`.  Name-matching alone cannot tell those two apart.
func (g *Builder) aggregateFieldPort(ref varRef, field string) string {
	switch ref.kind {
	case "llm":
		// Semantic llm payload ref (llm<RT3>.text whole record): member
		// fields live under the payload, so compose the dotted path.
		// The whole LLMResponse envelope (LLMEnvelopeOutput "response")
		// keeps bare metadata/payload field ports (text/usage/...).
		//
		// 基段是**payload 下方的路径**（`text.date_info`）时同样成立（2026-09-24
		// R-58 一手 S161）：判据是「首段 == payload 口」，不是「整段 == text」。
		// 只认整段会把 `v = r.text.date_info;` 后的 `v.date` 退化成**裸字段口**
		// `date` ⇒ 后端投影加不上物理前缀（`value.`）⇒ 产物引用悬空
		// （`ref 100002.date does not resolve: node 100002 declares ports [value]`）。
		// 与 `internal/backend/cozebiz/（商业仓）render.go`（`strings.HasPrefix(path, "text.")`）
		// 和 `render_loop.go` 的同族判据同一把尺子：**首段是 payload 口 ⇒ 下钻**。
		if ref.output == LLMPayloadOutput || strings.HasPrefix(ref.output, LLMPayloadOutput+".") {
			return ref.output + "." + field
		}
		return fieldPort(ref, field)
	case extractPayloadKind:
		// `extract<T>` 的**载荷记录**绑定：成员活在载荷口 `value` 下方（口名 =
		// 语言层字段名，见 ExtractValuePort）⇒ 首段是口（或已在口下方）时下钻
		// （`p.city` → `value.city`）。与 llm 分支同一条纪律，判据同样是「首段是
		// 载荷口」而不是「整段等于口名」（R-58 先例）；调用值 kind `"extract"`
		// 不在此分支（它的成员是信封选择器，走 fieldPort 恒等）。
		if ref.output == ExtractValuePort || strings.HasPrefix(ref.output, ExtractValuePort+".") {
			return ref.output + "." + field
		}
		return fieldPort(ref, field)
	case "code":
		// Structural code outputs (CodeRun.OutTypes, one port per field for
		// multi-port foreign code) are FLAT: the field name is its own port
		// (fieldPort's code contract, identical to the direct-read path).
		// Only a base port the producer **declares** with a record type is a
		// real container whose members live under it.
		//
		// 2026-09-12/13（产物门禁接线暴露）：判据**不得**一律被「ref.output
		// 是不是 result/output」短路（旧的 `!whole` 守卫）——记录型 foreign
		// code 的整体值口正是 `result`，其成员**不在**扁平端口里，短路会发射
		// `100006.data` 这种**未声明端口**的引用（门禁 L2 实测；语料 2227/2227
		// 个 code 引用全部命中声明口，0 例扁平字段直引）。
		//
		// 但「整体值绑定」有两种生产者，必须分治（同批收敛测试实测）：
		//   - **物化 record 字面量的 shim**（Synthetic 单 `result` object 口）：
		//     渲染器把成员**展开成扁平顶层端口**，共享 field-port 契约与反编译
		//     侧都按裸叶子读写 ⇒ 保留基段会抬回 `v1.result.f0`（RT1 无 result
		//     字段 → FIELD_NOT_FOUND，roundtrip_property seed=1/3/17/… 实测）。
		//   - 其余声明了 record 型整体口的生产者（foreign code）：保留基段。
		// 判据 = 生产者**声明的端口模型**（成员是否被展开成扁平端口），
		// 与绑定形状、文件名无关 —— 且与**直读通路**（resolveValueRef 的
		// codeMemberPort）共用同一条规则、同一个实现：两条通路各判一次必然
		// 漂移（X262 一手实证，见 codeMemberPort 头注）。
		if port, ok := g.codeMemberPort(ref, field); ok {
			return port
		}
		return fieldPort(ref, field)
	default:
		return fieldPort(ref, field)
	}
}

// basePortIsRecordContainer reports whether the producer exposes `port` as a
// RECORD-typed (object) output port, i.e. this record's fields live UNDER that
// port and the candidate path must keep the base segment.
//
// A port that the producer does not expose, or exposes with a non-record type
// (a multi-port code node's flat `image: array<string>`), is not a container:
// the field name is then its own port.  The predicate reads the producer's
// declared output ports only — CodeRun.Types carries INPUT binding types and
// is deliberately not consulted.
func (g *Builder) basePortIsRecordContainer(nodeID, port string) bool {
	t := g.effectivePortType(nodeID, port)
	return t != nil && t.Kind == "object"
}

// expandsRecordMembersFlat reports whether the producer's record members are
// rendered as FLAT top-level ports rather than as members under a container
// port.  Only materialised record literals (`v = {...}` → Synthetic CodeRun
// with the single whole-dict port `result`) are expanded that way: every
// renderer emits one node_output per member (render_node.go), and both the
// shared field-port contract (compile) and the decompiler read those members
// as bare leaf paths.  A foreign code node that returns a dict declares only
// `result` (+ the platform default `output`), so its members MUST be addressed
// as `result.<member>`.
func (g *Builder) expandsRecordMembersFlat(nodeID, port string) bool {
	if port != "result" {
		// A member base (llm payload `text`, a shim's record-typed field port)
		// is a real container even on a materialised shim: only the shim's own
		// whole-dict port is expanded into flat member ports.
		return false
	}
	nd, ok := g.find(nodeID).(*CodeRun)
	return ok && nd.Synthetic && len(nd.OutTypes) == 1 && isObjectType(nd.OutTypes["result"])
}

// isObjectType reports whether t is a record (object) type.
func isObjectType(t *Type) bool { return t != nil && t.Kind == "object" }

// effectivePortType resolves the type of an output port in the **effective**
// (rendered) port model of the producer, not merely the raw IR OutTypes map.
//
// The two differ for a Synthetic single-`result`-object record shim
// (`let r = {...}` / `v = <record literal>` → CodeRun{OutTypes: {result: RT},
// Synthetic: true}): every renderer expands that record's members into
// top-level ports, because the shared field-port contract addresses shim
// members as flat paths (`r.G1` → from_path G1; the reference target down-drills `result.` in
// its ref mapper, coze-biz expands node_outputs, render_node.go).  Judging
// containment on the raw map alone therefore contradicts the ref model the
// rest of the compiler already emits:
//
//	RT6 v1; v1 = {...}; agg = v1.text; return agg.title;
//	  declared wire ports = [output, result, finish_reason, reasoning_content,
//	                         text, usage]   ← `text` IS a port of type RT1
//	  raw map            = [result]          ← `text` looks undeclared
//
// With the raw map the field-phi candidate degenerates to the bare leaf
// `title`, which no producer declares ⇒ dangling ref (L2 refs-resolve) and
// raising can only restore the legacy envelope member `v1.title` ⇒ the next
// compile fails TYPE_MISMATCH "Legacy llm record field access" (X211 / S93 /
// S130 deep-round c3, 2026-09-10).  Reading the effective model yields the
// real container path `text.title`, which is exactly what the direct-read
// path (fieldPort) emits for the same binding.
//
// A dotted base is followed through the schema (base "a.b" ⇒ type of b),
// matching the dotted-path refs fieldPort composes.  Undeclared ⇒ nil: the
// caller falls back to the flat leaf port and never guesses a dotted path.
func (g *Builder) effectivePortType(nodeID, port string) *Type {
	if port == "" {
		return nil
	}
	nd, ok := g.find(nodeID).(*CodeRun)
	if !ok {
		// kind=="code" refs are produced by CodeRun nodes only
		// (build_call.go / build_return.go shims); a different node here is a
		// builder invariant violation, and the honest answer is "no evidence
		// this port is a container" — never a guessed dotted path.
		return nil
	}
	segs := strings.Split(port, ".")
	t, found := nd.OutTypes[segs[0]]
	if !found {
		t = expandedResultPort(nd, segs[0])
	}
	for _, seg := range segs[1:] {
		if t == nil || t.Kind != "object" {
			return nil
		}
		t = t.Props[seg]
	}
	return t
}

// expandedResultPort returns the type a Synthetic single-`result`-object record
// shim exposes for member `name` once the renderers expand that record into
// top-level ports; nil when the node is not such a shim.
//
// 有效口集里**没有**「平台默认 output:string」这一口（2026-09-19 更正）：那口
// 曾是渲染器无条件追加的兜底声明，随 N3/N4 根因修复一并删除（口集 = 声明集，
// 逐字相等，见 backend/cozebiz/render_node.go）。故这里也不再对 `output` 特判：
// 记录键真叫 `output`（零值载体 `{"output": "", "result": 0}`）时它就是该记录的
// 一个真字段，必须与其它成员同规则解析。
func expandedResultPort(nd *CodeRun, name string) *Type {
	if nd == nil || !nd.Synthetic || len(nd.OutTypes) != 1 {
		return nil
	}
	rt, ok := nd.OutTypes["result"]
	if !ok || rt == nil || rt.Kind != "object" || len(rt.Props) == 0 {
		return nil
	}
	return rt.Props[name]
}

// refForAggregateField converts a whole-record binding into a platform
// candidate for one semantic field.
func (g *Builder) refForAggregateField(ref varRef, field string) Ref {
	return Ref{Producer: ref.nodeID, Output: g.aggregateFieldPort(ref, field)}
}

// changedHoistedByShape is the shared core of changedHoistedScalars /
// changedHoistedRecords (COPY-REFERENCE-SEMANTICS per-variable phi units):
// it reports escaping hoisted variables whose branch-final binding differs
// from the pre-if binding on at least one arm.  Only variables bound before
// the if participate — branch-local declarations do not escape an if in
// ncoda (UNDEFINED on post-if reads is a decompile-scope defect, not a
// compile phi).  wantRecord=false keeps true scalars (no record props);
// wantRecord=true keeps record-shaped variables only.
func (g *Builder) changedHoistedByShape(outerDeclared map[string]bool, outerVars, thenVars, elseVars map[string]varRef, wantRecord bool) ([]string, error) {
	return g.changedHoistedByShapeArms(outerDeclared, outerVars, []map[string]varRef{thenVars, elseVars}, wantRecord)
}

// changedHoistedByShapeArms is the arm-count agnostic core: a junction is a
// set of mutually exclusive arms (if/else = 2 arms, switch = N arms, ask =
// action arms + optional timeout arm).  The escaping-variable rule is the
// same for every host: a variable declared BEFORE the junction whose
// arm-final binding differs from the pre-junction binding on at least one
// arm is one phi unit (COPY-REFERENCE-SEMANTICS).
func (g *Builder) changedHoistedByShapeArms(outerDeclared map[string]bool, outerVars map[string]varRef, armVars []map[string]varRef, wantRecord bool) ([]string, error) {
	changed := map[string]bool{}
	mark := func(name string, branchVars map[string]varRef) error {
		outer, hasOuter := outerVars[name]
		// 成员判据（C7 收口，2026-09-12）：**声明**先行 ∨ **绑定**先行。
		// 旧写法只问绑定 ⇒ `T v;`（提升 idiom，无绑定）在 switch/QA/attempt
		// 三宿主上被排除出 phi 单元 ⇒ 零候选 junction ⇒ 下游直引首臂 producer，
		// 其余臂的写值静默消失（臂内 producer 成孤儿；L1/L2 全绿）。
		if !hasOuter && !outerDeclared[name] {
			return nil
		}
		if !g.hoistedVars[name] {
			return nil
		}
		b, ok := branchVars[name]
		if !ok {
			return nil
		}
		// 「改变量」比较只在存在分支前绑定时成立；无绑定 ⇒ 零基（该臂的写
		// 即 phi 候选），与 merge 的 zero 语义（AGENTS.md，候选缺省 = 零值）
		// 同一口径——缺省候选不物化显式零值载体。
		if hasOuter && b.nodeID == outer.nodeID && b.output == outer.output {
			return nil
		}
		// 类型权威 = 一条三级链（声明 → 前绑定端口 → ③ 臂内真写值的声明端口）：
		// 旧写法把 2 级链复制在 6 处，`var v;`（无类型权威、无前绑定）在六级上
		// 全部取不到类型 ⇒ phi 单元不成立 ⇒ 零候选 junction ⇒ 死脚手架被合成
		// End 消费（M2 族 2026-09-12）。③ 是严格增量（仅 ①②皆 nil 时咨询）。
		// 槽类型权威（J §31）：链的**判定**只有一处（`slot_type_authority.go`
		// 的 `firstSlotType`，经 `junctionPhiType` 消费）——门与组装侧用**同一个**
		// 数据源，不再各写一遍。
		//
		// **无权威 ⇒ fail loud**（2026-09-24 用户拍板「甲先行」，`J §31.7`）：
		// 旧行为 = 该变量不做 phi 单元 ⇒ 零候选 junction ⇒ 该臂写值**静默消失**
		// （编译 SUCCEEDED、L1/L2 全绿）——AGENTS.md「失配 ≠ 静默丢弃」。
		// 拒收是安全网：产物无法承载作者写的语义时，宁缺毋滥，作者加一个类型注解
		// 即可（typed oracle 已证有权威时产物正确）。根因面的补权威（乙）另立项。
		levels := g.junctionPhiLevels(name, outerVars, armVars)
		t := firstSlotType(levels)
		if t == nil {
			return missingSlotTypeError("convergence slot", name, levels)
		}
		if wantRecord && len(t.Props) == 0 {
			return nil // 标量走标量通道
		}
		if !wantRecord && len(t.Props) > 0 {
			return nil // record/容器走 record 汇聚（字段组）或既有通道
		}
		changed[name] = true
		return nil
	}
	for _, arm := range armVars {
		for name := range arm {
			if err := mark(name, arm); err != nil {
				return nil, err
			}
		}
	}
	if len(changed) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(changed))
	for name := range changed {
		out = append(out, name)
	}
	sortNamesNatural(out)
	return out, nil
}

// changedHoistedScalars reports escaping hoisted scalar (non-record)
// variables that some arm writes (COPY-REFERENCE-SEMANTICS per-variable phi
// units).  Membership = declared before the junction (outerDeclared; a
// pre-junction binding，若存在，即 outerVars 中该名) — branch-local
// declarations do not escape a junction in ncoda (UNDEFINED on post-junction
// reads is a decompile-scope defect, not a compile phi).  A declared-but-
// unbound variable (`T v;`) counts as changed on any arm that binds it: its
// pre-junction value is the type zero (C7, 2026-09-12).
func (g *Builder) changedHoistedScalars(outerDeclared map[string]bool, outerVars, thenVars, elseVars map[string]varRef) ([]string, error) {
	return g.changedHoistedByShape(outerDeclared, outerVars, thenVars, elseVars, false)
}

// changedHoistedRecords reports escaping hoisted RECORD variables whose
// branch-final binding differs from the pre-if binding on at least one arm.
// Mirrors changedHoistedScalars for record shapes (RED A, merge 专项
// 2026-09-08): a branch-MID whole-record write followed by a scalar tail
// (`rec = {...}; agg = "x";`) leaves rec's last write as that arm's phi
// value.  Tail-only collection (hoistedTailVars — binding == branch tail)
// would skip such records and drop the write: the branch producer becomes an
// orphan and post-if rec.field reads bind to the pre-if zero value.
func (g *Builder) changedHoistedRecords(outerDeclared map[string]bool, outerVars, thenVars, elseVars map[string]varRef) ([]string, error) {
	return g.changedHoistedByShape(outerDeclared, outerVars, thenVars, elseVars, true)
}

// mergeHoistedNames returns the sorted union of two sorted name sets.
func mergeHoistedNames(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	if len(a) == 0 {
		return b
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, n := range a {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, n := range b {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sortNamesNatural(out)
	return out
}

// scalarShaped reports whether a changed hoisted variable is a TRUE scalar
// (no element/member semantics): only then may the legacy single-scalar
// junction (merge output Group1, plain alias binding) absorb it.  list /
// object-with-props shapes carry composite semantics and must converge
// through per-variable named groups instead (the named channel of
// planArmJunctionPhi; 2026-09-04 S115/read_book R3).
func scalarShaped(name string, outerVars map[string]varRef, armVars []map[string]varRef, g *Builder) bool {
	if t := g.junctionPhiType(name, outerVars, armVars); t != nil {
		return t.Kind != "list" && len(t.Props) == 0
	}
	return true // 无类型权威：保持既有 legacy 让位（保守；通道选择不是类型结论）
}

// phiPortName derives the merge group / output-port name for a per-variable
// scalar phi from the ncoda variable name.  The coze-biz merge group name is
// the output port name; decompile reuses agg_-prefixed group names verbatim,
// so keeping the name == variable name makes deep compile↔decompile chains
// naming-stable.
func phiPortName(name string) string {
	if name == "" {
		return ""
	}
	out := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		ok := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && c >= '0' && c <= '9')
		if !ok {
			return "" // 非法标识符：不虚构端口名，跳过该 phi
		}
		out = append(out, c)
	}
	if out[0] >= '0' && out[0] <= '9' {
		return ""
	}
	return string(out)
}

// scalarBranchDefault materializes a branch-local default for a hoisted
// scalar arm that does not reassign the variable: an identity relay shim
// reading the pre-if binding (copy semantics).  The shim runs only on this
// arm's control path so the merge never observes the pre-if value on the
// wrong path.
//
// **无 junction 前绑定 ⇒ 不产载体**（phiDefaultWanted 政策，2026-09-12 M2 族）：
// 零值吸收由 merge zero 语义在运行时完成（未执行候选 = 零值被跳过），物化显式
// 零值 shim = P04 禁止的死载体（X23 家族复发根因）。
func (g *Builder) scalarBranchDefault(block *Block, base varRef, typ *Type) (Ref, bool) {
	if block == nil || base.nodeID == "" {
		return Ref{}, false
	}
	id := g.nextID()
	node := &CodeRun{
		ID:        id,
		Language:  "python",
		Types:     map[string]*Type{},
		OutTypes:  map[string]*Type{"result": typ},
		OutOrder:  []string{"result"},
		Synthetic: true,
		Identity:  true, // 恒等复制：result ≡ 唯一输入（见 fold_zero_identity_candidates.go）
	}
	node.Code = wrapShim([]pyast.Param{{Name: "arg1"}}, &pyast.NameExpr{Name: "arg1"})
	node.Inputs = []Source{{Path: []string{"arg1"}, BlockID: base.nodeID, Name: base.output}}
	node.Types["arg1"] = typ
	g.nodes = append(g.nodes, node)
	g.remember(node)
	r := varRef{nodeID: id, output: "result", kind: "code"}
	block.Stmts = append([]Stmt{&NodeStmt{Node: node, Value: &r}}, block.Stmts...)
	return Ref{Producer: id, Output: "result"}, true
}

func (g *Builder) materializeBranchDefault(block *Block, base varRef, field string, typ *Type) (Ref, bool) {
	if block == nil {
		return Ref{}, false
	}
	// 无 junction 前绑定 ⇒ 不产载体（同 scalarBranchDefault 的政策；P04）。
	if base.nodeID == "" {
		return Ref{}, false
	}
	id := g.nextID()
	node := &CodeRun{
		ID:        id,
		Language:  "python",
		Types:     map[string]*Type{},
		OutTypes:  map[string]*Type{"result": typ},
		OutOrder:  []string{"result"},
		Synthetic: true,
		Identity:  true, // 恒等复制：result ≡ 唯一输入（见 fold_zero_identity_candidates.go）
	}
	inputName := g.aggregateFieldPort(base, field)
	// 零值豁免（与 arm_gate_candidate.go 的 gateArmCandidate **同一条判据、
	// 同一个理由**，不得分叉）：前绑定**可证类型零**时不需要臂内载体 ——
	// merge 的 zero 语义（first_non_zero）本就把零候选跳过，「候选缺省 =
	// 类型零」与「候选是零值」取值相同，载体是死载体。
	//
	// 更要紧的是**可识别性**：直引使各**字段组**的同位候选指向**同一个**
	// 生产者（记录字面量节点本身，其口名就是字段名），而逐字段 relay 会把
	// 同一语义在 wire 上拼成两种写法 —— 写臂 = 记录装配节点（口名 = 字段名），
	// 未写臂 = N 个逐字段 relay（口名 = result）。record 单元判据 ① 要求
	// 「候选口名末段 == 组名」，relay 拼写不满足 ⇒ 整张 merge 退化成逐组标量，
	// 读回的文本再编译又长出载体（r1≠r2）。
	// **候选仍然存在**，只是**直引**前绑定的同名字段口（= FoldZeroIdentityCandidates
	// 的重定向终点的原地形态）：`fold_zero_identity_candidates.go` 的规则是
	// 「重定向 relay 候选到链尾零值真相，候选数**不变**」——候选数必须等于臂数
	// （删候选实测 `RS-MRG-001 flow exit value has no rendering site`，且
	// `variable_merge` 的「候选 ↔ 臂」对应是 wire 结构契约）。零值豁免若直接
	// 不产候选，就是把「重定向」误做成「删除」⇒ 单候选 merge（既有门禁
	// `TestRenderIfAggregateNoPseudoPort`：单候选 merge 不是 φ）+ raising 读回
	// 时 record 单元判据失真 ⇒ 逐轮裹层（一手实证 zero_identity 形状 r1≠r2）。
	// 一手实证 = 生成器 exit_phi_shared d≥1（未写臂 = base 的逐字段 relay）。
	if isZeroCarrierNode(g.findAny(base.nodeID)) {
		return g.refForAggregateField(base, field), true
	}
	node.Code = wrapShim([]pyast.Param{{Name: "arg1"}}, &pyast.NameExpr{Name: "arg1"})
	node.Inputs = []Source{{Path: []string{"arg1"}, BlockID: base.nodeID, Name: inputName}}
	node.Types["arg1"] = typ
	g.nodes = append(g.nodes, node)
	g.remember(node)
	ref := varRef{nodeID: id, output: "result", kind: "code"}
	block.Stmts = append([]Stmt{&NodeStmt{Node: node, Value: &ref}}, block.Stmts...)
	return Ref{Producer: id, Output: "result"}, true
}

// zeroRecordShimCode 返回 shape 全零 record 的规范 shim code 字符串
// （与 lowerMapPyast 对全零字面量的输出同构：直接 return dict、键序 =
// sortedTypeFields）。用于判定某 CodeRun 是否承载"同形状零值 record"。
func zeroRecordShimCode(shape *Type) string {
	items := make([]pyast.DictItem, 0, len(shape.Props))
	for _, f := range sortedTypeFields(shape) {
		items = append(items, pyast.DictItem{
			Key:   &pyast.StringExpr{Value: f},
			Value: zeroValuePyast(shape.Props[f]),
		})
	}
	mod := &pyast.Module{Body: []pyast.Stmt{&pyast.ReturnStmt{Value: &pyast.DictExpr{Items: items}}}}
	return ShimMarker + strings.TrimSuffix(pyast.Serialize(mod), "\n")
}

// isZeroRecordLiteral 判定字面量 MapLit 与 shape 同构（字段集一致且每字段
// 是类型零值字面量）。
func isZeroRecordLiteral(m *ast.MapLit, shape *Type) bool {
	if shape == nil || len(shape.Props) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, e := range m.Entries() {
		k, ok := staticMapKeyName(e.Key)
		if !ok || seen[k] {
			return false
		}
		ft, has := shape.Props[k]
		if !has {
			return false
		}
		seen[k] = true
		if !isZeroLiteralFor(e.Value, ft) {
			return false
		}
	}
	return len(seen) == len(shape.Props)
}

// zeroRecordLiteralFields 返回 record 字面量 m 里**逐字段取值为该字段类型零值**的
// 字段名（按规范键序）。与 `isZeroRecordLiteral` **同一把尺**（同一个
// `isZeroLiteralFor` 谓词）：后者问「整条字面量是不是类型零」，本函数问「哪几个
// 字段是类型零」—— 平台 merge 是**逐字段**取候选，所以臂门控要能看见字段级的零
// （见 `CodeRun.ZeroFields` 头注的一手证据）。
//
// 非字面量字段（引用 / 表达式）**不入表**（不可证 ⇒ 不标注）：fail-safe 方向是
// 「不当作零」——把非零当零会让门控脱落，把零当非零只会多一次门控（等价变换）。
//
// @guard-exempt: 2026-09-28 —— 下一行是 **nil 安全 + 类型权威前置**（没有声明记录形状就
// 没有「字段零值」这一说），不是「仅当 X 形态才…」的形态枚举守卫：判据对**任意**记录形状
// 同解（与 `isZeroRecordLiteral` 同一把尺、同一个 `isZeroLiteralFor` 谓词）。
// 退出条件 = 调用方把「shape 非空」固化成签名级前置（或本函数并入接受 `*Type` 的零值判据）时删除本行。
func zeroRecordLiteralFields(m *ast.MapLit, shape *Type) []string {
	if m == nil || shape == nil || len(shape.Props) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range m.Entries() {
		k, ok := staticMapKeyName(e.Key)
		if !ok || seen[k] {
			continue
		}
		ft, has := shape.Props[k]
		if !has {
			continue
		}
		seen[k] = true
		if isZeroLiteralFor(e.Value, ft) {
			out = append(out, k)
		}
	}
	return out
}

func isZeroLiteralFor(e ast.Expr, t *Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case "string":
		lit, ok := e.(*ast.StringLit)
		return ok && lit.Value() == ""
	case "integer", "number":
		// 闭集复用（R-95）：`-2` 与 `2` 是同一族 ⇒ `-0` 认零、`-2` 不认零，
		// 与正字面量同一把尺（旧实现按 go 类型 switch ⇒ `int v = -0;` 拿不到
		// ZeroOf，而 `int v = 0;` 拿得到 = 同一意思两种产物）。
		v, ok := literalConditionValue(e)
		if !ok {
			return false
		}
		switch x := v.(type) {
		case int64:
			return x == 0
		case float64:
			return x == 0
		}
		return false
	case "boolean":
		lit, ok := e.(*ast.BoolLit)
		return ok && !lit.Value()
	case "list", "array":
		lit, ok := e.(*ast.ArrayLit)
		return ok && len(lit.Elements()) == 0
	case "file":
		// file 类型的零值 = **静态空文件 cast** `file("")`（零值集合必须与
		// `typeZeroExpr` / raising 侧 `zeroConstExpr` 逐字同尺——三处任一落后，
		// 含 file 字段的 record 零值字面量就认不出零 ⇒ 该节点拿不到 ZeroOf ⇒
		// 零候选吸收与臂门控零豁免同时失效（一手实证：familyC r0 的
		// `v11 = { "data": file(""), … }` 被臂门控物化恒等载体 ⇒ 载体多出一个
		// 消费者 ⇒ raising 的 deadZeroCarrierMergeRef 判「有真实消费」⇒ 零载体
		// 不被吸收 ⇒ R∘C 落到更大的坏固定点 42246 而到不了 canonical 42015）。
		// 判据是**静态**零：`file(<非字面量>)` / `file("http://…")` 都是真实值。
		call, ok := e.(*ast.CallExpr)
		if !ok || len(call.Args()) != 1 || calleeName(call.Func()) != "file" {
			return false
		}
		return isZeroLiteralFor(call.Args()[0], T("string"))
	case "object":
		lit, ok := e.(*ast.MapLit)
		if !ok {
			return false
		}
		// 不透明 object（无字段声明）：类型零 = 空 map `{}`（零值表 map/object
		// → {}）。字段结构未知 ⇒ **非空**字面量不可证零（不猜，fail 侧安全）。
		// 缺这支 = 含不透明 object 字段的 record 零值字面量认不出零 ⇒ 该节点
		// 拿不到 ZeroOf ⇒ 下游的零候选吸收 / 臂门控零豁免全部失效
		// （一手实证：`http` 结果的 headers 字段 = 不透明 object ⇒ 生成器
		// exit_phi_shared 的未写臂被迫物化逐字段 relay）。
		if len(t.Props) == 0 {
			return len(lit.Entries()) == 0
		}
		return isZeroRecordLiteral(lit, t)
	}
	return false
}

// currentBindingIsZeroRecord 判定 hoisted var 的当前绑定节点承载同形状
// 零值 record（函数级 hoist 零值 code，从未被真实赋值覆盖）。
func (g *Builder) currentBindingIsZeroRecord(name string, shape *Type) bool {
	ref, ok := g.varToOutput[name]
	if !ok || ref.nodeID == "" {
		return false
	}
	nd := g.find(ref.nodeID)
	cr, ok := nd.(*CodeRun)
	if !ok {
		return false
	}
	return cr.Code == zeroRecordShimCode(shape)
}

func zeroValuePyast(t *Type) pyast.Expr {
	if t == nil {
		return &pyast.NoneExpr{}
	}
	switch t.Kind {
	case "string":
		return &pyast.StringExpr{Value: ""}
	case "integer", "number":
		return &pyast.IntExpr{Value: 0}
	case "boolean":
		return &pyast.BoolExpr{Value: false}
	case "list", "array":
		return &pyast.ListExpr{}
	case "object":
		items := make([]pyast.DictItem, 0, len(t.Props))
		for _, name := range sortedTypeFields(t) {
			items = append(items, pyast.DictItem{
				Key:   &pyast.StringExpr{Value: name},
				Value: zeroValuePyast(t.Props[name]),
			})
		}
		return &pyast.DictExpr{Items: items}
	default:
		return &pyast.NoneExpr{}
	}
}

// conditionToSelector flattens a condition expression into selector
// conditions. `||` / `&&` nesting expands into the conditions array with
// logic 1 (or) / 2 (and); comparison operators map onto SELECTOR_OPERATORS.
// booleanShimCondition 把「材料化布尔 shim 作为条件」统一成平台单目
// isTrue（op 11）：shim 由 materializeCondition 建成，输出口**声明**就是
// boolean（该走法唯一的形状），所以这是类型结论而非推断——不需要查表回退。
//
// 历史形态是 `Operator: 1, Right: Operand{Val: "true"}` ⇒ wire 上是
// `type: string, value: "true"`，即把布尔当真值做**字符串**比较：反编译读回
// `v == "true"`（ncoda 里 bool 与 string 不可比，r0≍c0 语义 + 类型双漂移，
// 且再编译报类型错）——R8 的最后一腿。平台侧同一语义有原生单目 op
// （capability: condition operand surface；语料实证 8 例 op 11）。
func booleanShimCondition(ref varRef) Condition {
	return Condition{Left: Operand{Ref: &Ref{Producer: ref.nodeID, Output: ref.output}}, Operator: 11}
}

// logicDroppedByFlatten 报告 conditionToSelector 子结果的 logic 与本层 logic
// **冲突**：扁平并表会把子表达式的 logic 丢掉（0 = 单条，无逻辑可丢）。
func logicDroppedByFlatten(subLogic, wantLogic int) bool {
	return subLogic != 0 && subLogic != wantLogic
}

// materializeMixedLogicCondition 把平台扁平条件表装不下的混合布尔守卫
// （`(a || b) && c` / `(a && b) || c`）物化成**我们自己的**布尔 shim：
// 语义原样保留（不再静默丢 logic），反编译侧按既有 shim 通道精确还原为同一表达式
// （shim 契约：我们造的 shim 我们负责互逆）。
func materializeMixedLogicCondition(e ast.Expr, g *Builder, flow *[]string, wantLogic int) ([]Condition, int, error) {
	ref, err := g.materializeCondition(e, flow)
	if err != nil {
		return nil, 0, err
	}
	g.recordPlacement("", placementConstruct("condition", "mixed-logic"),
		PlacementShim, placementReason(PlacementCauseNoNativeNode,
			fmt.Sprintf("mixed &&/|| guard is not representable in the platform's flat condition table (one logic per group; want %d) → boolean shim", wantLogic)))
	return []Condition{booleanShimCondition(ref)}, 0, nil
}

func conditionToSelector(cond ast.Expr, g *Builder, flow *[]string) ([]Condition, int, error) {
	// R-84：裸布尔常量条件（`if (true)` / `if (false)`）与「布尔操作数的真值
	// 测试」**同一载体**（平台单目 isTrue，op 11）⇒ `if (true)` ≡
	// `if (true == true)` ≡ `let b = true; if (b)`（同一份产物）。常量真假
	// 一律 op 11（isTrue）：op 12 是 isFalse，`if (false)` 用它取反会错。
	if bl, ok := cond.(*ast.BoolLit); ok {
		g.recordPlacement("", placementConstruct("condition", "bool-literal"), PlacementNative, placementReason(PlacementCauseCapability, "boolean literal truth test (op 11)"))
		return []Condition{{Left: Operand{Val: bl.Value()}, Operator: 11}}, 0, nil
	}
	if isPlainConditionOperand(cond) {
		left, err := operandOf(cond, g, flow)
		if err != nil {
			// G12-2: a json.parse<array<T>> element chain (items[0].field)
			// is a plain-looking operand with no dotted-port form — bind it
			// as a truth test of a materialized boolean shim instead.
			ref, merr := g.materializeCondition(cond, flow)
			if merr != nil {
				return nil, 0, err
			}
			g.recordPlacement("", placementConstruct("condition", "plain-array-element"), PlacementShim, placementReason(PlacementCauseNoNativeNode, "no dotted-port form → boolean extraction shim (G12-2)"))
			return []Condition{booleanShimCondition(ref)}, 0, nil
		}
		g.recordPlacement("", placementConstruct("condition", "plain-operand"), PlacementNative, placementReason(PlacementCauseCapability, "plain selector truth test"))
		// A boolean-typed plain operand lowers to the platform's unary
		// isTrue check (op 11, no right operand — real exports: X12
		// 149417 `is_text_corr_on` → op 11). Non-boolean plain operands
		// keep the equals-true literal form.
		//
		// R-84：判据是**类型是不是 boolean**，不是「有没有 ref 地址」——
		// 布尔**字面量**（`let b = true; if (b)`）此前落到 equals-true 的
		// **字符串**形态（`right: {type: string, value: "true"}`），反编译
		// 读回 `true == "true"` ⇒ 再编译成 shim `(True == "true")` = False
		// ⇒ 一轮后语义翻转（静默：编译 SUCCEEDED、零诊断）。
		if isBooleanConditionOperand(left, g) {
			return []Condition{{Left: left, Operator: 11}}, 0, nil
		}
		return []Condition{{
			Left:     left,
			Operator: 1,
			Right:    Operand{Val: "true"},
		}}, 0, nil
	}
	switch e := cond.(type) {
	case *ast.CallExpr:
		if fa, ok := e.Func().(*ast.FieldAccessExpr); ok {
			switch fa.FieldName() {
			case "contains", "not_contains":
				if len(e.Args()) != 1 {
					return nil, 0, &UnsupportedError{Construct: fmt.Sprintf("method %q arity", fa.FieldName())}
				}
				left, err := operandOf(fa.Obj(), g, flow)
				if err != nil {
					return nil, 0, err
				}
				right, err := operandOf(e.Args()[0], g, flow)
				if err != nil {
					return nil, 0, err
				}
				opStr := "contain"
				if fa.FieldName() == "not_contains" {
					opStr = "not_contain"
				}
				return []Condition{{Left: left, Operator: selectorOpToInt(opStr), Right: right}}, 0, nil
			}
		}
		// G-empty (2026-09-01, EMPTY-CHECK-DESIGN): `empty(x)` 条件位原生
		// 映射平台单目 isEmpty（op 9）——coze Selector 原生支持，绕过
		// boolean shim（materialize 会退化成 code 节点 + equals-true）。
		// 参数必须可解析为 operand（ident/field access/字面量）；复杂参数
		// 自然 fall through 到下方 boolean calc shim 兜底。
		if id, ok := e.Func().(*ast.IdentExpr); ok && id.Name() == "empty" {
			if len(e.Args()) != 1 {
				return nil, 0, &UnsupportedError{Construct: "call \"empty\" expects 1 argument"}
			}
			left, lerr := operandOf(e.Args()[0], g, flow)
			if lerr == nil {
				g.recordPlacement("", placementConstruct("condition", "empty"), PlacementNative, placementReason(PlacementCauseCapability, "empty(x) → platform isEmpty (op 9)"))
				return []Condition{{Left: left, Operator: 9}}, 0, nil
			}
			// operandOf 失败（如 empty(a+b)）→ boolean calc shim 兜底。
		}
		// G10: function-form calc calls whose output is boolean (contains)
		// fill a condition position by materializing the boolean shim and
		// comparing `equals true` — the same pass-2 path arithmetic
		// comparisons use. Boolean-typed is the hard gate: a condition is a
		// truth test, so len/join/trim/to_string/split (non-boolean) keep
		// the rejection below.
		if id, ok := e.Func().(*ast.IdentExpr); ok && calcBuiltinOutType(id.Name()).Kind == "boolean" {
			ref, err := g.materializeCondition(e, flow)
			if err != nil {
				return nil, 0, err
			}
			// 条件位用**布尔**真值测试（op 11），不是字符串 "true" 比较：
			// `contains(x, y)` / `empty(a+b)` 的 shim 输出口声明就是 boolean。
			return []Condition{booleanShimCondition(ref)}, 0, nil
		}
		return nil, 0, &UnsupportedError{Construct: fmt.Sprintf("condition call %q", calleeName(e.Func()))}
	case *ast.UnaryExpr:
		if e.Op() == "!" {
			// G-empty: `!empty(x)` 条件位原生映射平台单目 isNotEmpty（op 10）。
			if call, ok := e.Operand().(*ast.CallExpr); ok {
				if id, ok := call.Func().(*ast.IdentExpr); ok && id.Name() == "empty" && len(call.Args()) == 1 {
					if left, lerr := operandOf(call.Args()[0], g, flow); lerr == nil {
						g.recordPlacement("", placementConstruct("condition", "not-empty"), PlacementNative, placementReason(PlacementCauseCapability, "!empty(x) → platform isNotEmpty (op 10)"))
						return []Condition{{Left: left, Operator: 10}}, 0, nil
					}
				}
			}
			// R-83：`!(<比较>)` 折成**否定算子**（`!(a == b)` ≡ `a != b` ⇒ 同一份产物）。
			// 放在 op 12 兜底**之前**：平台对每个比较算子都有否定算子（宿存在）⇒ 不该造
			// shim 也不该借 `isFalse` 绕过；op 12 兜底只留给「操作数本身已是一个布尔值」
			// 的形态（`!b` / `!x[0]` / `!u.flag`），那些形状今天就是这样，一步不动。
			// 折算后**再入** conditionToSelector：算子表 / 原生判定 / 落账都在原处（一处定义），
			// 新节点不在 pass-0 mark 表里 ⇒ 回落 `conditionNativeDecision`（与 mark 同一函数，同解）。
			if neg, ok := negatedComparison(e.Operand()); ok {
				return conditionToSelector(neg, g, flow)
			}
			left, err := operandOf(e.Operand(), g, flow)
			if err != nil {
				return nil, 0, err
			}
			// isFalse (op 12) is unary — no right operand (real exports).
			return []Condition{{Left: left, Operator: 12}}, 0, nil
		}
		return nil, 0, &UnsupportedError{Construct: fmt.Sprintf("condition unary op %q", e.Op())}
	case *ast.BinaryExpr:
		// 平台的布尔条件表是**扁平**的：一个 `{conditions: [...], logic: N}`，只有
		// 一个 logic（语料 322 份 yaml 实测：条件最多 3 条、logic ∈ {1,2}、
		// **零嵌套**）⇒ `(a || b) && c` / `(a && b) || c` **装不下**。
		//
		// 装不下的后果曾经是**静默错译**（2026-09-30 一手，R-172 收口时暴露）：
		// 两个 case 都返回**自己的** logic，而合并时把子表达式的 logic 丢掉
		// （`left, _, err :=`）⇒ `(n == 2 || m == 3) && k == 5` 落成
		// `logic: 2, conditions: [n==2, m==3, k==5]` = `n==2 && m==3 && k==5`，
		// 编译 SUCCEEDED、零诊断、语义已变（撞「失配 ≠ 静默丢弃」）。
		// ⇒ 判据 = 子结果的 logic 与本层 logic **冲突时不得并表**，改由我们自己的
		// 布尔 shim 承载整条守卫（平台装得下的照旧走原生扁平表，见负侧判据）。
		switch e.Op() {
		case "||":
			left, llogic, err := conditionToSelector(e.Left(), g, flow)
			if err != nil {
				return nil, 0, err
			}
			right, rlogic, err := conditionToSelector(e.Right(), g, flow)
			if err != nil {
				return nil, 0, err
			}
			if logicDroppedByFlatten(llogic, 1) || logicDroppedByFlatten(rlogic, 1) {
				return materializeMixedLogicCondition(e, g, flow, 1)
			}
			return append(left, right...), 1, nil
		case "&&":
			left, llogic, err := conditionToSelector(e.Left(), g, flow)
			if err != nil {
				return nil, 0, err
			}
			right, rlogic, err := conditionToSelector(e.Right(), g, flow)
			if err != nil {
				return nil, 0, err
			}
			if logicDroppedByFlatten(llogic, 2) || logicDroppedByFlatten(rlogic, 2) {
				return materializeMixedLogicCondition(e, g, flow, 2)
			}
			return append(left, right...), 2, nil
		case "in":
			// `x in arr` ≡ `arr.contains(x)` —— **同一个平台算子（op 7）**，只是操作数
			// 左右互换：平台 op 7 的**左**操作数是容器（coze-studio
			// `nodes/selector/operator.go::WillAccept` 逐条钉住 —— string=子串 /
			// slice=元素 / map=键）。归一到**方法形态**（= 反编译器读回 op 7 时写出的
			// 那一种拼写 ⇒ 规范拼写），使两种拼写走**同一条**通道：单叶落
			// 原生 op 7；混合守卫落同一个值形态归一（`conditionPredicateValueForm`）。
			// **不是语言扩展**：`in` 是既有 ncoda 二元算子（值位今天落 shim
			// `(x in arr)`，本刀不动它）——这里只补上 lowering 的**原生承载面**。
			// 折成 `arr.contains(x)` 的**准入**与 `CallExpr` 支同解：**容器**
			// （= contains 的左操作数，落平台 selector 槽）必须是可引用 selector
			// （字面量容器装不下）；**成员**（= 参数）既可是 selector **也可是
			// 标量字面量**（`literalConditionValue` 闭集，与比较支同一张表）——
			// 漏掉字面量这一侧就会造出「`"a" in arr` 打 shim 而 `arr.contains("a")`
			// 走原生 op 7」这种同一语义两种产物（一手实测）。
			_, memberIsLiteral := literalConditionValue(e.Left())
			if isPlainConditionOperand(e.Right()) && (isPlainConditionOperand(e.Left()) || memberIsLiteral) {
				return conditionToSelector(
					ast.NewCallExpr(ast.NewFieldAccessExpr(e.Right(), "contains"), nil, []ast.Expr{e.Left()}),
					g, flow)
			}
			// 容器不是可引用 selector（列表字面量 `x in ["a","b"]` / 表达式）⇒ 平台
			// selector 的槽位装不下（只收标量字面量与可求值引用）⇒ 物化 shim ——
			// **第三条出路**（既不静默错译、也不拒收；与 `a == "x"+"y"` / 结构字面量
			// 操作数同一走法）。
			ref, merr := g.materializeCondition(e, flow)
			if merr != nil {
				return nil, 0, merr
			}
			g.recordPlacement("", placementConstruct("condition", "in"), PlacementShim, placementReason(PlacementCauseNoNativeNode, "container not a selector → boolean shim"))
			return []Condition{booleanShimCondition(ref)}, 0, nil
		}
		// R-64 规范形（与判据共用一处 = canonicalConditionExpr）：**对称算子**
		// （== / !=）的操作数序**不进产物** —— 值路径在左、字面量在右；
		// 非对称算子的序**原样保留**（那是语义，不是自由度）。规范化在比较分支的
		// 入口做一次，下方全部读点（空串折叠 / 布尔字面量 / 原生算子 / 物化兜底）
		// 因此看到**同一个**操作数序。
		e = canonicalConditionExpr(e)
		// Empty-string comparisons fold to the platform's unary checks:
		// `x == ""` → op 9 (isEmpty), `x != ""` → op 10 (isNotEmpty).
		// A literal empty-string RIGHT operand is rejected at import
		// (变量值不可为空) — real exports carry the unary form with no
		// right operand at all (X12 149417 data op 10, X97 op 9).
		if lit, ok := e.Right().(*ast.StringLit); ok && lit.Value() == "" {
			left, lerr := operandOf(e.Left(), g, flow)
			if lerr != nil {
				return nil, 0, lerr
			}
			switch e.Op() {
			case "==":
				return []Condition{{Left: left, Operator: 9}}, 0, nil
			case "!=":
				return []Condition{{Left: left, Operator: 10}}, 0, nil
			}
		}
		// Boolean literal comparisons: `x == true` → op 11 (isTrue),
		// `x == false` → op 12 (isFalse), `x != true` → op 12,
		// `x != false` → op 11. Unary, no right operand (real exports:
		// X12 149417 is_text_corr_on op 11).
		if bl, ok := e.Right().(*ast.BoolLit); ok {
			left, lerr := operandOf(e.Left(), g, flow)
			if lerr != nil {
				return nil, 0, lerr
			}
			op := 0
			switch {
			case e.Op() == "==" && bl.Value():
				op = 11
			case e.Op() == "==":
				op = 12
			case e.Op() == "!=" && bl.Value():
				op = 12
			case e.Op() == "!=":
				op = 11
			}
			return []Condition{{Left: left, Operator: op}}, 0, nil
		}
		op, ok := selectorOperator(e.Op())
		if !ok {
			return nil, 0, &UnsupportedError{Construct: fmt.Sprintf("condition operator %q", e.Op())}
		}
		// B1 (2026-09-01, coze-condition-operator-matrix.md): `len(x)` 单参
		// 调用包比较 `> >= < <=` → 平台原生长度算子 op 3-6（string/array
		// 长度比较；与 empty(x) → op 9 原生映射同构）。此前 `len(x) > n`
		// 走 code 节点 boolean shim + equals-true（形状退化）。`len(x) == n`
		// 无原生 op（平台 string/array 无长度相等算子），保持 shim。
		if lenID, ok := lenCompareOperator(e.Left(), e.Op()); ok {
			if left, lerr := operandOf(e.Left().(*ast.CallExpr).Args()[0], g, flow); lerr == nil {
				if right, rerr := operandOf(e.Right(), g, flow); rerr == nil {
					g.recordPlacement("", placementConstruct("condition", "len-compare"), PlacementNative, placementReason(PlacementCauseCapability, fmt.Sprintf("len(x) %s → 原生长度算子 op %d", e.Op(), lenID)))
					return []Condition{{Left: left, Operator: lenID, Right: right}}, 0, nil
				}
			}
			// operandOf 失败（复杂参数/右值）→ 落到下方 shim 兜底。
		}
		// Pass 1 (native): lower to the platform's native condition surface
		// when the pass-0 mark says the operands are hostable — left must be
		// a plain selector (ident/field access); right is a literal always,
		// and additionally a plain selector when the capability table says
		// ref×ref is native (coze Selector resolves both operands as runtime
		// values — see capability package). Everything else — arithmetic,
		// nested expressions — is pass 2: materialized into a Synthetic code
		// node whose boolean result feeds a single `equals true` case.
		if !g.conditionNative(e) {
			ref, err := g.materializeCondition(e, flow)
			if err != nil {
				return nil, 0, err
			}
			g.recordPlacement("", placementConstruct("condition", e.Op()), PlacementShim, placementReason(PlacementCauseNoNativeNode, "beyond native condition surface → boolean shim (op 11 isTrue)"))
			return []Condition{booleanShimCondition(ref)}, 0, nil
		}
		left, lerr := operandOf(e.Left(), g, flow)
		if lerr == nil {
			right, rerr := operandOf(e.Right(), g, flow)
			if rerr == nil {
				g.recordPlacement("", placementConstruct("condition", e.Op()), PlacementNative, placementReason(PlacementCauseCapability, "condition operand surface"))
				return []Condition{{Left: left, Operator: op, Right: right}}, 0, nil
			}
		}
		// G12-2: a syntactically-plain operand with no dotted-port form
		// (json.parse<array<T>> element chain — items[0].field) can't bind
		// natively — the pass-0 mark is syntactic, so drop to the
		// materialized boolean shim at emission time instead of failing.
		g.recordPlacement("", placementConstruct("condition", e.Op()), PlacementShim, placementReason(PlacementCauseNoNativeNode, "no dotted-port form → boolean shim (G12-2)"))
		ref, merr := g.materializeCondition(e, flow)
		if merr != nil {
			return nil, 0, lerr
		}
		return []Condition{booleanShimCondition(ref)}, 0, nil
	}
	return nil, 0, &UnsupportedError{Construct: fmt.Sprintf("condition %T", cond)}
}

// selectorOperator maps ncoda comparison ops onto SELECTOR_OPERATORS ids.
func selectorOperator(op string) (int, bool) {
	switch op {
	case "==":
		return 1, true
	case "!=":
		return 2, true
	case ">":
		return 13, true
	case ">=":
		return 14, true
	case "<":
		return 15, true
	case "<=":
		return 16, true
	}
	return 0, false
}

// negatedComparison rewrites「取反的比较」成等价的**否定算子**比较，返回可再入
// `conditionToSelector` 的表达式（ok=false ⇒ 调用方保持既有路径）。
//
// 为什么折而不是物化：平台对下表每个算子**都有否定算子**（宿存在）⇒ 按 SHIM-ESCAPE
// 契约的 `¬∃c host` 一项，这里不该造 shim；折完两拼写落在**同一份产物**
// （`!(a == b)` 与 `a != b` 逐字节相同）。
//
// 表与 `selectorOperator` 的 id 表同源（不另立权威）：`==`↔`!=` · `>`↔`<=` ·
// `>=`↔`<`。**顺序不换**（`!(a > b)` ≡ `a <= b`，操作数序不变）。
// `empty(x)` 不在表内：`!empty(x)` 由上方专用分支落 op 10（isNotEmpty），更直接。
// `!!X` → X（双重取反）。
func negatedComparison(e ast.Expr) (ast.Expr, bool) {
	switch x := e.(type) {
	case *ast.BinaryExpr:
		switch x.Op() {
		case "==":
			return ast.NewBinaryExpr("!=", x.Left(), x.Right()), true
		case "!=":
			return ast.NewBinaryExpr("==", x.Left(), x.Right()), true
		case ">":
			return ast.NewBinaryExpr("<=", x.Left(), x.Right()), true
		case ">=":
			return ast.NewBinaryExpr("<", x.Left(), x.Right()), true
		case "<":
			return ast.NewBinaryExpr(">=", x.Left(), x.Right()), true
		case "<=":
			return ast.NewBinaryExpr(">", x.Left(), x.Right()), true
		}
	case *ast.UnaryExpr:
		if x.Op() == "!" {
			return x.Operand(), true
		}
	case *ast.CallExpr:
		if fa, ok := x.Func().(*ast.FieldAccessExpr); ok {
			switch fa.FieldName() {
			case "contains":
				return ast.NewCallExpr(ast.NewFieldAccessExpr(fa.Obj(), "not_contains"), x.TypeArgs(), x.Args()), true
			case "not_contains":
				return ast.NewCallExpr(ast.NewFieldAccessExpr(fa.Obj(), "contains"), x.TypeArgs(), x.Args()), true
			}
		}
	}
	return nil, false
}

// selectorOpToInt maps a selector operator string onto its id.
func selectorOpToInt(op string) int {
	switch op {
	case "contain":
		return 7
	case "not_contain":
		return 8
	}
	return 0
}

// lenCompareOperator 识别 `len(arg)` 包比较 `> >= < <=` 的长度比较形态，
// 返回平台原生长度算子 id（op 3-6，coze SELECTOR_OPERATORS）。只认
// len 单参 ident 调用；`len(x) == n` 返回 0,false（平台 string/array 无
// 长度相等算子，走 boolean shim 兜底）。
func lenCompareOperator(left ast.Expr, op string) (int, bool) {
	call, ok := left.(*ast.CallExpr)
	if !ok {
		return 0, false
	}
	id, ok := call.Func().(*ast.IdentExpr)
	if !ok || id.Name() != "len" || len(call.Args()) != 1 {
		return 0, false
	}
	switch op {
	case ">":
		return 3, true
	case ">=":
		return 4, true
	case "<":
		return 5, true
	case "<=":
		return 6, true
	}
	return 0, false
}

// operandOf resolves a condition operand: identifier → block-output ref,
// 标量字面量 → literal（闭集由 literalConditionValue 一处给出）。
func operandOf(e ast.Expr, g *Builder, flow *[]string) (Operand, error) {
	if v, ok := literalConditionValue(e); ok {
		return Operand{Val: v}, nil
	}
	switch x := e.(type) {
	case *ast.IdentExpr:
		if ref, ok := g.varToOutput[x.Name()]; ok {
			return Operand{Ref: &Ref{Producer: ref.nodeID, Output: ref.output}}, nil
		}
		// G10: a literal-bound variable (let ok = true / let n = 400)
		// renders as a constant condition operand instead of failing
		// "not resolvable" (literals live in litVars, no output node).
		if lv, ok := g.litVars[x.Name()]; ok {
			switch lv.typ {
			case "integer":
				if v, err := strconv.ParseInt(lv.value, 10, 64); err == nil {
					return Operand{Val: v}, nil
				}
			case "number":
				if v, err := strconv.ParseFloat(lv.value, 64); err == nil {
					return Operand{Val: v}, nil
				}
			case "boolean":
				if lv.value == "true" {
					return Operand{Val: true}, nil
				}
				return Operand{Val: false}, nil
			}
		}
		return Operand{}, &UnsupportedError{Construct: fmt.Sprintf("condition operand %q not resolvable", x.Name())}
	case *ast.FieldAccessExpr, *ast.IndexExpr:
		if ref, ok := g.resolveValueRef(x); ok {
			return Operand{Ref: &Ref{Producer: ref.nodeID, Output: ref.output}}, nil
		}
		return Operand{}, &UnsupportedError{Construct: fmt.Sprintf("condition operand %T not resolvable", x)}
	case *ast.BinaryExpr:
		if x.Op() == "+" {
			ref, err := g.emitTextConcat(*flow, "", false, x)
			if err != nil {
				return Operand{}, err
			}
			*flow = []string{ref.nodeID}
			return Operand{Ref: &Ref{Producer: ref.nodeID, Output: ref.output}}, nil
		}
		return Operand{}, &UnsupportedError{Construct: fmt.Sprintf("condition operand op %q", x.Op())}
	}
	return Operand{}, &UnsupportedError{Construct: fmt.Sprintf("condition operand %T", e)}
}

// isBooleanConditionOperand 判定一个已解析的条件操作数**类型是不是 boolean**：
// ref ⇒ 查端口类型；字面量 ⇒ 看值自身的 Go 类型（R-84：字面量也算，否则布尔
// 字面量会落到 equals-true 的**字符串**形态上）。
func isBooleanConditionOperand(left Operand, g *Builder) bool {
	if left.Ref != nil {
		t := g.portTypeOf(left.Ref.Producer, left.Ref.Output)
		// 下面这行是**类型谓词**（操作数类型是不是 boolean），不是「仅当 X 形态才…」的
		// 形态守卫 —— R-84 恰恰是**删掉**了那条形态守卫（`left.Ref != nil` 这一个守卫）。
		//@guard-exempt: 类型判定，非形态守卫（2026-09-25；退出条件 = 本仓出现通用的「类型是不是 boolean」谓词后改调它）
		return t != nil && t.Kind == "boolean"
	}
	_, ok := left.Val.(bool)
	return ok
}

// isPlainConditionOperand reports whether expr is a selector-backed
// condition operand (ident or field access chain), mirroring the legacy
// isIdentOrFieldAccess shape check.
func isPlainConditionOperand(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.IdentExpr, *ast.FieldAccessExpr:
		return true
	case *ast.IndexExpr:
		// 字符串字面量下标 = 成员字段访问（反编译器对关键字字段渲染
		// obj["field"]；resolveValueRef 同语义解析为 dotted port）。
		_, ok := e.Index().(*ast.StringLit)
		return ok
	}
	return false
}

// literalConditionValue 是「字面量 ↔ 条件槽标量」的**唯一**闭集：返回该字面量
// 落条件槽时的值。判据侧（isLiteralConditionOperand，决定原生 vs 物化）与发射侧
// （operandOf，真的往槽里填值）**共用本函数** —— 同一事实两处实现 = 两处真相，
// 而两处一旦不同解，症状就是「判据说能落、发射落不下 ⇒ 静默回落到物化」这种
// 看不出来的半档（R-84 布尔、R-94 数值即此形态）。
//
//	`"x"` / `1` / `1.5` / `true` → 标量直填
//	`-2` / `-1.5` → 负字面量（解析器把 `-2` 读成 UnaryExpr 套字面量，
//	                 见 `pkg/parser/expr.go`；**只认字面量操作数** ——
//	                 `-m` 是对 ref 取反，不是槽能装的标量，落物化家族）
//
// `null` **不在**本闭集里：它在判据侧只用于「字面量在右」的规范序（见
// canonicalConditionExpr），发射面没有它的槽形态（coze-biz 是 nil-less 平台）。
func literalConditionValue(expr ast.Expr) (any, bool) {
	switch x := expr.(type) {
	case *ast.StringLit:
		return x.Value(), true
	case *ast.IntLit:
		return x.Value(), true
	case *ast.FloatLit:
		return x.Value(), true
	case *ast.BoolLit:
		return x.Value(), true
	case *ast.UnaryExpr:
		if x.Op() != "-" {
			return nil, false
		}
		switch lit := x.Operand().(type) {
		case *ast.IntLit:
			return -lit.Value(), true
		case *ast.FloatLit:
			return -lit.Value(), true
		}
	}
	return nil, false
}

// isLiteralConditionOperand reports whether expr is a literal the legacy
// selector encoding accepts as a comparison value (condition.go:124-135).
func isLiteralConditionOperand(expr ast.Expr) bool {
	if _, ok := literalConditionValue(expr); ok {
		return true
	}
	// null 参与「字面量在右」的规范序判定，但发射面不支持它落槽（走物化）。
	_, ok := expr.(*ast.NullLit)
	return ok
}

// ── pass 0: graph traversal + marking (P0') ────────────────────────────
//
// markLoweringPass walks every reachable function body BEFORE emission and
// records, per comparison expression, whether the target can host it as a
// native condition (single decision source). Emission (conditionToSelector)
// consumes the marks; the inline fallback in conditionNative covers
// emission-synthesized conditions (ternary/while guards) that reuse the
// same AST pointers, and any construct the walker missed.
func (g *Builder) markLoweringPass() {
	g.condMarks = map[*ast.BinaryExpr]bool{}
	mark := func(body *ast.Block) {
		if body == nil {
			return
		}
		ast.Walk(conditionMarker{g: g}, body)
	}
	if g.entry != nil {
		mark(g.entry.Body())
	}
	for _, fn := range g.funcs {
		mark(fn.Body())
	}
}

// conditionMarker marks every if/ternary/while condition reachable in the
// program. While guards reuse s.Condition() in the synthesized if, so the
// mark carries over.
type conditionMarker struct{ g *Builder }

func (m conditionMarker) Visit(n ast.Node) ast.Visitor {
	switch x := n.(type) {
	case *ast.IfStmt:
		m.g.markCondition(x.Condition())
	case *ast.TernaryExpr:
		m.g.markCondition(x.Condition())
	case *ast.WhileStmt:
		m.g.markCondition(x.Condition())
	}
	return m
}

// markCondition records the native/materialize decision for every
// comparison inside a condition tree (&& / || recurse into operands, the
// same way conditionToSelector splits them).
func (g *Builder) markCondition(e ast.Expr) {
	x, ok := e.(*ast.BinaryExpr)
	if !ok {
		return
	}
	switch x.Op() {
	case "&&", "||":
		g.markCondition(x.Left())
		g.markCondition(x.Right())
	default:
		g.condMarks[x] = g.conditionNativeDecision(x)
	}
}

// phiDefaultWanted reports whether an if/else junction arm that does NOT
// reassign an escaped hoisted variable still contributes a default candidate.
//
// 政策（2026-09-12 M2 族，site 级谓词）：**无 junction 前绑定 ⇒ 该臂不贡献候选**
// ——候选集合只由「真写臂（`bound != base` 且 `bound.nodeID != ""`）」与「未写臂
// 但有前绑定（直引前绑定，复制语义，`base.nodeID != ""`）」构成。未执行候选由
// merge zero 语义吸收（AGENTS.md：未执行分支被类型零值吸收），故**不为「没写臂」
// 物化零值/转发载体**（P04：死载体 = X23 家族复发根因）。
//
// 该谓词吸收了原 M1/R5 的 `hoistedOptional` 子句：`base != ""` 时两者同为 true、
// `base == ""` 时两者同为 false ⇒ 语义完全一致（不再依赖声明派生标记，改成
// 「以绑定为准」的 site 级判据——`v = null` 对 hoisted 变量早退、不改绑定
// （build_call.go），故在站点上与未写臂等价，一并被本谓词拒绝）。
func (g *Builder) phiDefaultWanted(name string, base varRef) bool {
	return base.nodeID != ""
}

// anyStmtEndsFlow reports whether any of the just-lowered statements ends every
// path of the flow (see stmtEndsFlow). Statements after such a statement are
// dead code.
func anyStmtEndsFlow(stmts []Stmt) bool {
	for _, s := range stmts {
		if stmtEndsFlow(s) {
			return true
		}
	}
	return false
}

// stmtEndsFlow reports whether a structured statement unconditionally ends the
// flow that contains it — every path through it reaches a main-flow return
// (which terminates the flow at the canonical End).
//
// This is the structured-tree form of the projections' dead-path cut ("a
// terminating tail emptied the junction, every later statement in this block
// is unreachable"): a statement the flow cannot reach past makes the rest of
// its block dead. The rule is recursive over statement kinds and has no
// per-shape special case; a kind that cannot be proven to end the flow
// answers false, which marks strictly fewer statements dead.
func stmtEndsFlow(s Stmt) bool {
	switch v := s.(type) {
	case *ReturnStmt:
		// A main-flow return carries the canonical End (it terminates the
		// flow); an inline sub-function return has no End — it hands a value
		// back to its caller and the flow continues.
		return v.End != nil
	case *IfStmt:
		// Both exclusive arms ending ends the if; an else-less if always has
		// the unmatched path (the pending-else return or the selector's false
		// port) still live.
		return v.Else != nil && blockEndsFlow(v.Then) && blockEndsFlow(v.Else)
	case *ParallelStmt:
		// The barrier waits for every branch: all branches ending ends the
		// parallel (P5 #4 — a branch return terminates the whole flow).
		if len(v.Branches) == 0 {
			return false
		}
		for _, b := range v.Branches {
			if !blockEndsFlow(b) {
				return false
			}
		}
		return true
	case *SwitchStmt:
		// Every arm ending ends the switch — but only when an arm is entered
		// for every value (a default arm); without one the unmatched path
		// falls through.
		if len(v.Branches) == 0 {
			return false
		}
		hasDefault := false
		for _, br := range v.Branches {
			if br.Port == "default" {
				hasDefault = true
			}
			if !blockEndsFlow(br.Body) {
				return false
			}
		}
		return hasDefault
	case *QaStmt:
		if len(v.Branches) == 0 {
			return false
		}
		for _, br := range v.Branches {
			if !blockEndsFlow(br.Body) {
				return false
			}
		}
		return true
	case *AttemptStmt:
		return v.Success != nil && v.Failure != nil &&
			blockEndsFlow(v.Success) && blockEndsFlow(v.Failure)
	case *InlineStmt:
		// An inlined body ends the flow only if it ends it itself (its own
		// returns are value returns, so this is conservative in practice).
		return blockEndsFlow(v.Body)
	}
	return false
}

// blockEndsFlow reports whether a block unconditionally ends the flow: some
// statement in it ends every path, and no statement before it can be skipped
// around it (a linear block has no such skip).
func blockEndsFlow(b *Block) bool {
	if b == nil {
		return false
	}
	for _, s := range b.Stmts {
		if stmtEndsFlow(s) {
			return true
		}
	}
	return false
}
