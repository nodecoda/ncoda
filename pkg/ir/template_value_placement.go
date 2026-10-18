// IR builder — `template "…" [using {…}]` 的**位置判据**（R-78，2026-09-25）。
//
// 为什么需要它：这个构造 = **逐字文本 + 作者具名名额表**，是**节点级**载体
// （文本落点 + 名额表落点，正本 docs/five-step/spec/TEMPLATE-STRING-CANONICAL-FORM.md §0），
// 所以只有三个**位点**装得下它（同正本 §4）：
//
//	· `return template …`                 —— 流程答复端点（`terminatePlan: useAnswerContent`）
//	· `output(template …)`                —— 中间消息节点（同名节点的另一种名额来源）
//	· llm message 的 `content: template …` —— 整节点 `node_inputs` 一张表（两个文本槽共用）
//
// 其余位置都是**值通道**（let 初始化 / 数组元素 / 条件 / 拼接操作数 / 调用实参……）：
// 那里只物化「一个值」，没有放置名额表的地方 ⇒ 位置错（class = language：作者要改
// 自己的源码），不是平台不支持，也不是编译器缺陷。
//
// 修前实测（R-78 一手，探针 /tmp/tplnorm p5–p8）：同一个事实在四条值物化路径上各写
// 一遍通用兜底 ⇒ 四种互不相同的说法（`let s = non-call expression` /
// `array element: condition materialization *ast.TemplateValue …` /
// `if condition: …` / `concat operand *ast.TemplateValue`），**每一条都看不出病根**，
// 其中两条还给出「assign the value to a variable first」这个**正是失败那条路**的死
// 建议（`string s = template …;` 会落到同一个洞）。⇒ 本条判据把四处收成**一条**
// 规则、**一个**码、**一条**可操作消息。
//
// 判据形态 = **白名单剪枝**（与 pkg/passes/capability_gate.go::visitReturnStmt
// 对 `return template` 的剪枝同源）：在承载位剪枝、别处一律报错 —— 反过来写成「在
// 值通道入口拉黑」的话，今后新增一条值物化路径就会悄悄漏过去（四条路径各写一遍正是
// 这次要消灭的形态）。
package ir

import (
	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/diag"
)

// checkTemplateValuePlacement 报告**不在承载位**的 `template "…" [using {…}]`。
// 只扫 IR 真正降级的面（入口函数 + 内联函数体，与 usage_prescan 的扫法同源）：
// 子流程声明是**引用**（workflow id/version），没有可降级的体；`const` 值只收字面量
// ⇒ 解析期就进不来这个形态。
func checkTemplateValuePlacement(prog *ast.Program) error {
	if prog == nil {
		return nil
	}
	c := &templatePlacementCheck{allowed: map[*ast.TemplateValue]bool{}}
	fns := prog.Functions()
	if entry := prog.Entry(); entry != nil {
		fns = append(append([]*ast.FunctionDef{}, fns...), entry)
	}
	for _, fn := range fns {
		if fn == nil || fn.Body() == nil {
			continue
		}
		ast.Walk(c, fn.Body())
		if c.violation != nil {
			return c.violation
		}
	}
	return nil
}

type templatePlacementCheck struct {
	// allowed = 落在承载位的节点对象（先访问父节点、后访问子节点，所以标记总在
	// 命中之前完成）。
	allowed map[*ast.TemplateValue]bool
	// violation = 第一条（按源码序）非承载位的出现。
	violation error
}

func (c *templatePlacementCheck) Visit(n ast.Node) ast.Visitor {
	switch t := n.(type) {
	case *ast.ReturnStmt:
		c.allowDirect(t.Value())
	case *ast.OutputStmt:
		c.allowDirect(t.Value())
	case *ast.VarDeclStmt:
		// 值位置的**条件**具名形态（R-163，2026-09-29 用户裁 甲）：
		// `let s = template "…" using {…};` 里至少一个 token 的名字**不是**名额名
		// （带下标 / 子路径，如 `{{String1[2].authors}}`）⇒ 内插结构上写不出它
		// （内插的名额名由值派生，恒是裸标识符）⇒ 具名形态是**唯一**能同时保住
		// 「逐字文本 + 名额名」的承载面，落点 = 原生 text 节点（见
		// pkg/ir/build_call.go::emitNamedTextTemplate）。
		//
		// 条件只认**直接** init（`let s = f(template …)` 之类不在内：名额表没有落点），
		// 且全 token 都是裸名额名时**仍然拒收** —— 那种 wire 内插装得下，允许它只会
		// 给同一条 wire 添第二种写法（R-78 主结论不动）。
		if init := t.Init(); init != nil {
			if tr, ok := init.(*ast.TemplateValue); ok && templateNeedsNamedForm(tr) {
				c.allowed[tr] = true
			}
		}
	case *ast.CallExpr:
		if calleeName(t.Func()) == "llm" {
			c.allowLLMMessageContents(t)
		}
	case *ast.RequestInputStmt:
		// `ask<F>({content: template "…" using {…}})` 的 content 是**唯一**能逐字
		// 承载平台 `question` 名额表（`node_inputs[]`）的构造 —— 名字逐字来自 wire，
		// 而内插形态的名字由值派生 ⇒ 装不下平台自由名。与 llm message content 同一位。
		c.allowMapKey(t.Config(), "content")
	case *ast.TemplateValue:
		if c.allowed[t] {
			// 承载位：不再下降 —— 名额值（`using { k: <值路径> }`）由
			// resolveTemplateBindings 自己判（TEMPLATE_BINDING_INVALID），
			// 本条判据只管**这个构造落在哪**。
			return nil
		}
		if c.violation == nil {
			c.violation = templatePlacementError(t)
		}
		return nil
	}
	return c
}

// templateNeedsNamedForm 报告这条具名模板在**值位置**是不是唯一承载形态：文本里至少
// 有一个 token 的名字**不在**名额表里 —— 那种 token（带下标 / 子路径，一手
// `{{String1[2].authors}}`）正是内插写不出来的东西（内插的名额名由值派生 ⇒ 恒是裸标识符，
// 值导航又受「记录数组非 0 下标」的闭集限制 ⇒ 折不出来）。
//
// 反向读法同样重要：这条判据把「全 token 都是裸名额名」的具名形态**挡在值位置之外**
// ——那正是 R-78 裁过的形态（内插装得下，名字由值派生），允许它只会让同一条 wire 多一种
// 写法。⇒ 值位置的门槛是「内插拼不出」，不是「作者想具名」。
func templateNeedsNamedForm(tr *ast.TemplateValue) bool {
	if tr == nil || len(tr.Bindings()) == 0 {
		// 没有名额表的 `template ""` 在值位置只是「一段逐字文本」：那是普通字符串
		// 字面量就能承载的东西，不借具名形态表达。
		return false
	}
	names := map[string]bool{}
	for _, b := range tr.Bindings() {
		names[b.Name] = true
	}
	for _, m := range literalPlaceholderRe.FindAllStringSubmatch(tr.Text(), -1) {
		if !names[m[1]] {
			return true
		}
	}
	return false
}

// allowDirect 标记「值本身就是承载形态」的那一个节点（`return template …` /
// `output(template …)`）。值是别的表达式时不标记 —— 里面的 template 仍是值位置。
func (c *templatePlacementCheck) allowDirect(e ast.Expr) {
	if tr, ok := e.(*ast.TemplateValue); ok {
		c.allowed[tr] = true
	}
}

// allowLLMMessageContents 标记 llm 调用配置里 `messages[].content` 的模板值
// （与 pkg/ir/build_node.go 的承接点一一对应；nodes 之外没有第二个模板槽）。
// allowMapKey 允许 map 字面量里指定键的值落在承载位（llm messages / ask content）。
func (c *templatePlacementCheck) allowMapKey(m *ast.MapLit, key string) {
	if m == nil {
		return
	}
	for _, e := range m.Entries() {
		k, ok := staticMapKeyName(e.Key)
		if !ok || k != key {
			continue
		}
		c.allowDirect(e.Value)
	}
}

func (c *templatePlacementCheck) allowLLMMessageContents(call *ast.CallExpr) {
	for _, a := range call.Args() {
		cfg, ok := a.(*ast.MapLit)
		if !ok {
			continue
		}
		for _, e := range cfg.Entries() {
			key, ok := staticMapKeyName(e.Key)
			if !ok || key != "messages" {
				continue
			}
			arr, ok := e.Value.(*ast.ArrayLit)
			if !ok {
				continue
			}
			for _, el := range arr.Elements() {
				msg, ok := el.(*ast.MapLit)
				if !ok {
					continue
				}
				for _, me := range msg.Entries() {
					mk, ok := staticMapKeyName(me.Key)
					if !ok || mk != "content" {
						continue
					}
					c.allowDirect(me.Value)
				}
			}
		}
	}
}

// templatePlacementError 组一条可操作的位置诊断：说清**这是什么**（节点级载体，
// 值通道装不下）、**两条活路**（内插 / 移到承载位）。位置取该构造自身的 span
// （LanguageError 的可选字段；拿不到就留空，绝不猜一个行号）。
func templatePlacementError(tr *ast.TemplateValue) error {
	sp := tr.Span().Start
	return &LanguageError{
		Code: diag.TEMPLATE_VALUE_PLACEMENT,
		Message: "`template \"…\" [using {…}]` 只能落在三个承载位：`return template …`（流程答复端点）· " +
			"`output(template …)`（中间消息）· llm message 的 `content: template …`（整节点一张名额表）。" +
			"这里是**值通道**（let 初始化 / 数组元素 / 条件 / 拼接操作数 / 调用实参 …）：它只物化一个值，" +
			"没有放置「逐字文本 + 作者具名名额表」的地方 ⇒ 拒收（位置错，不是平台不支持）。" +
			"两条活路：① 要一个由值拼出来的字符串 ⇒ 写内插 \"…${expr}…\"（编译器拆解、名字由值派生，同一个 wire 形状）；" +
			"② 名额表必须由作者写死（行名带下标 / 行名 ≠ 口名 / 行不被文本引用）⇒ 把它移到上面三个承载位之一。",
		Line:   sp.Line,
		Column: sp.Column,
	}
}
