package ir

// whole_value_refs.go —— 「record 值必须由单一端口承载，否则只能在流程出口出现」
// 的判据（A12-H4，2026-09-17 用户裁定 (b)）。
//
// 规则（一条，无形态守卫、无产出方分叉）：
//
//	产出方的值由**多个端口**承载时，平台画布上没有「整个值」这个端口——值的
//	承载就是那组字段端口。因此这种值的引用只有一种合法位置：**流程出口**
//	（`return v;`，出口把整值糖展开成作者手写同一 record 的字面量
//	`return {f1: v.f1, …}`，见 ports.go wholeRecordReturnLiteral）。
//	其余位置（模板槽 `${v}`、标量实参、比较操作数、`output(v)`、`json.stringify(v)`…）
//	引用它 = **语言错**：作者必须写明字段（`v.body`）。
//
// 为什么判据落在**消费表**上而不是绑定点：绑定点（`let v = http(...)`）只知道
// 产出方的形状，不知道这个值会不会被消费——「绑了不用」是合法程序。而
// `collectIRConsumers` 是 IR 里「谁读了谁」的唯一普查表（模板槽 / 值树叶子 /
// 循环状态 / 出口绑定全在其中，覆盖面无形态盲区），所以判据 = 在消费表上问一句
// 「有没有人读了这个产出方的**整值标记口**」。
//
// 为什么不能靠产物门禁兜底（一手实测，2026-09-15）：
//
//	位置            产出方 = http（三端口）        产出方 = 结构性 foreign code
//	模板槽 ${v}     ARTIFACT_GATE（class=internal） **SUCCEEDED + 静默只绑首字段 a**
//	标量实参        ARTIFACT_GATE                   **SUCCEEDED + 静默丢值**
//	比较操作数      ARTIFACT_GATE                   **SUCCEEDED**
//	output(v)       ARTIFACT_GATE                   **SUCCEEDED**
//
// 即：http 侧「响但把语言错伪装成编译器缺陷（internal）」，foreign code 侧
// 「完全静默、值丢失且门禁全绿」。两条都不是作者能据以行动的信息，故判据必须
// 在语言层给出（`class=language`），且必须覆盖**全部消费位置**。
//
// 与 ports.go 的关系：`the port that carries a call's value` 的唯一规则表在
// ports.go（wholeValueAtReturn / wholeValueFields）。本文件只补两件事：
//   ① codeWholeValuePort —— 绑定点该写哪个端口名（真承载口 / 整值标记）；
//   ② wholeValueHasNoCarrier —— 哪些产出方**没有**单一承载口（判据的唯一输入）。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/builtin"
	"github.com/nodecoda/ncoda/pkg/diag"
)

// wholeValueMarker 是 IR 里「整值绑定」的端口名。
//
// 它不是平台端口名，而是 IR 的内部承载名：产出方的值由多个端口承载时，绑定
// 写这个名字，含义是「整个值，而不是某个字段口」。出口位置由 wholeValueAtReturn
// 逐字段展开；其余位置消费它 = 语言错（本文件判据）。产出方**有**单一承载口时，
// 绑定写真口名（`result` / `response` / `output`…），下游照常解析。
//
// **这个常量必须是「端口名空间的补集」，不能是某个合法端口名**（A12-H4 根因，
// 一手实证 2026-09-17）：它曾是字面量 `"output"` —— 而插件的声明输出里**可以有
// 一个字段就叫 `output`**（能力头文件 `{string $output;}`：copy_check / wujie /
// capcut …）。于是同一个字符串在 IR 里承载两种含义（「整个 record」与「名为
// output 的字段」），两者在 Source 上**逐字同形**（`checked` 与 `checked.$output`
// 都编码成 `{Name: "output"}`）⇒ 判据把作者写的**字段读**读成整值读，把 12 份
// 真实导出语料判成语言错。
//
// 两条含义必须**可区分**（编码必须单射），否则判据不可能正确：字段口名来自
// 语言层标识符（`fieldPort` 返回字段名本身）/ 内建契约（`statusCode`、`outputList`…），
// 而 `@` 不是标识符成分 ⇒ `"@whole"` 永远不等于任何字段口名。（若它意外漏进
// 产物，L1/L2 的 refs-resolve 门禁会 fail loud —— 没有静默通道。）
const wholeValueMarker = "@whole"

// WholeValuePortName 暴露整值标记的**拼写**给 IR 之外的边界层（判据与
// lowering 目标：runtime spec 的端口名空间里没有这个口，跨越边界时必须翻译成
// 目标自己的整值表达）。只暴露拼写，不暴露语义 —— 语义仍在
// wholeValueBindingPort / codeWholeValuePort 两个规则表里。
func WholeValuePortName() string { return wholeValueMarker }

// wholeValueBindingPort 是「一次调用的**整值绑定**该写哪个端口名」的唯一规则表
// （A12-H4，2026-09-17）。
//
//	· 值由**一组字段口**承载（平台没有任何一个口承载整个 record）⇒ 整值标记
//	  `wholeValueMarker` —— http（body/statusCode/headers 三口，语料 3/3）与
//	  plugin（声明式扁平字段口，语料 4665 节点 0 例整值包装口）都在此列；
//	· 值就是某个端口上的值（llm 信封 / text / knowledge / 子流程单口…）⇒ 该口名。
//
// 判据是**产出方的端口表形状**，与调用点无关：三处绑定（let 初始化、attempt
// 操作、带策略的调用）共用本函数，规则不复制。
func wholeValueBindingPort(kind string) string {
	switch kind {
	case "llm":
		return LLMEnvelopeOutput
	case "http", "tool":
		return wholeValueMarker
	}
	return "output"
}

// codeWholeValuePort 返回 `foreign code` 块的整值绑定该写的端口名。
//
//	· 直出（CodeDirectOutput，声明唯一口 `result`）⇒ 该真口（下游任何位置都能
//	  解析，无损）；
//	· 结构性输出（CodeStructuralOutput，每个声明口是一个**字段**）⇒ 整值标记
//	  wholeValueMarker（消费位置据此报语言错，出口由 wholeValueAtReturn 逐字段展开）。
//
// 现状（改动前）由 firstOutputPort 代替本判据：它取 OutOrder 的**第一个口**，
// 对单口节点恰好正确，对结构性输出则把「第一个字段」冒充成「整值」——这正是
// foreign code 侧静默丢值的出生点（一手实测：`return t;` 与 `return t.a;`
// 逐字节同产物，`${t}` / `f(t)` 静默只绑首字段 `a`）。
// callBindRef 构造「一次调用的值绑进变量」的 varRef —— 三处绑定（let 初始化 /
// attempt 操作 / 带策略的调用）**共用同一处**（与 bindPortOf 同源：端口规则与
// 绑定规则各一处，不复制）。
//
// 为什么绑定形状不只是端口名（2026-09-22 收口，一手实测）：tool 调用的值是一个
// **声明式记录**——成员活在口下方（`data.id` / `data.url`），而 fieldPort /
// aggregateFieldPort 的 tool 分支**靠 ref.typ 才能把语义叶子还原成产出方真实
// 路径**（扫 typ.Props 找出唯一承载该叶子的 object 子口，拼 `data.url`）。
// 类型权威缺席 ⇒ 该分支退回裸字段名 ⇒ **容器前缀被吞**。
//
// 一手对照（本仓最小形，其余逐字相同）：
//
//	`let r = plugin(...);` + `d = r.data;` … `d.url`
//	    ⇒ ref = {path: "data.url"}                              ✓
//	`attempt plugin(...) as r { success { d = r.data; } … }` … `d.url`
//	    ⇒ ref = {path: "url"}                                    ✗
//	    ⇒ ARTIFACT_GATE `ref 100003.url does not resolve: node 100003
//	      declares ports [code data errorBody isSuccess message]`
//
// 这正是「同一条规则的第二个实现必然漂移」的又一例（build_call.go 的 let 路径设了
// typ + noteWholeValueFields，build_stmt.go 的 attempt 路径漏了两者）——故收成
// **一个构造函数**，两条路径只许调用它。
func (g *Builder) callBindRef(id string, node Node, kind string, call *ast.CallExpr) varRef {
	ref := varRef{nodeID: id, output: bindPortOf(node, kind), kind: kind}
	//@guard-exempt: 2026-09-22 —— **族条件，不是形态守卫**：只有 tool 族的成员路径还原需要绑定点携带的字段表
	//（fieldPort 的 tool 分支是唯一「扫 ref.typ.Props 找回真实承载口」的分支，`r.data.url` 的 `data` 由它给出）；
	// llm / http / knowledge / code 的成员路径由内建契约 / 端口名恒等 / 声明端口模型给出。一手实测：去掉本条件 ⇒
	// TestLLMIRDistinguishesWholeEnvelopeFromText panic。退出条件 = fieldPort 不再按族分派成员路径时删除。
	if kind == "tool" && call != nil {
		ref.typ = g.semanticExprType(call)
		g.noteWholeValueFields(id, ref)
	}
	return ref
}

func codeWholeValuePort(cr *CodeRun) string {
	if len(cr.OutTypes) > 1 {
		return wholeValueMarker
	}
	return firstOutputPort(cr)
}

// codeMemberPort 是「code 节点的成员链首段：口名，还是口下方的子路径」的
// **唯一**判据 —— 直读通路（resolveValueRef）与出口汇聚通路（aggregateFieldPort）
// **共用同一把尺子**（两条通路各判一次必然漂移；X262 一手实证：直读吐
// `merged_image_parts.texts`、出口吐 `result.merged_image_parts.texts` ⇒
// 产物门禁 `ref … does not resolve`）。
//
// 一条规则，无形态枚举（判据只读产出方**声明的端口模型**）：
//
//	① 产出方的**成员被渲染器展平**成顶层口（物化 record 字面量 shim，见
//	   expandsRecordMembersFlat / expandedResultPort）⇒ 该段本身就是口名；
//	② 否则若**绑定点指向的口**本身是一个 **record 容器口**（成员活在它下面，
//	   典型 = 用户 foreign code 直接形把值挂在唯一口 `result` 之下；或 llm payload
//	   的 `text`。判据是**绑定指向谁**，不是「有没有同名的兄弟口」—— 一个节点
//	   同时声明 record 型成员口 `text: RT` 与叶子口 `wenan: string` 时，绑在
//	   `text` 上的链 `v.wenan` 必须给 `text.wenan`，叶子口不得抢答）⇒ 前缀容器口；
//	③ 否则该段是产出方声明的某个口（多口 code 节点的扁平字段口，如 json.parse
//	   记录形的 `data`）⇒ 它就是口名；
//	④ 都不是 ⇒ 该段**谁也产不出** ⇒ 不可解析（ok=false）：调用方据此 fail loud，
//	   绝不发明一个名字（禁止推断型静默 fallback）。
//
// 平台依据（② 一手复算 2026-09-19）：672 份真实导出里指向 code 节点的 2227 处
// 引用**全部命中该节点声明的口**（0 例扁平字段直引、0 例指向未声明的 `output`）；
// coze-studio OSS 计算引擎同判（`nodes.ConvertInputs(ctx, result, outputConfig)`
// —— code 节点的输出口 = 返回 dict 的键，没有隐式的 `output` 口）。
func (g *Builder) codeMemberPort(ref varRef, field string) (string, bool) {
	if _, ok := g.find(ref.nodeID).(*CodeRun); !ok {
		// **无端口模型证据**（产出方不在本图 / 手工构造的 Builder）⇒ 沿用历史
		// 规则：该段本身就是口名（fieldPort 的 code 契约）。这是同一条规则的
		// 缺省分支，不是虚构端口 —— 只有产出方在场、声明口集已知时，才谈得上
		// 「这一段是口名，还是口下方的子路径」。生产路径上引用恒指向图中的
		// 产出方，故本分支只覆盖无证据的情形。
		return field, true
	}
	// ① 产出方的成员被**展平**成顶层口（物化 record 字面量 shim，
	//    `let r = {...}` / `v = {...}`）⇒ 该段本身就是口名，**不带**容器前缀。
	//    这一支必须在 ② 之前：展平 shim 的整值口 `result` 类型也「含」这些成员，
	//    按 ② 读会抬回 `result.G1`（RT1 无 result 字段 ⇒ FIELD_NOT_FOUND，实测）。
	if g.expandsRecordMembersFlat(ref.nodeID, ref.output) {
		if g.effectivePortType(ref.nodeID, field) != nil {
			return field, true
		}
		return "", false
	}
	// ② 绑定点指向的口本身是 **record 容器**，且该记录**真的含**这一段 ⇒
	//    成员活在口下方。判据是「口的类型里有没有这个字段」，因此
	//      · 同名叶子口**不得抢答**（`{ text: RT2; wenan: string; }` 绑在 `text`
	//        上的 `v.wenan` 必须给 `text.wenan`）；
	//      · 而当这一段**就是**绑定口本身的名字时（单口结构输出 `-> { merged: RT; }`
	//        绑定口 = `merged`，链 `v.merged.title`），② 自然不成立 ⇒ 落到 ③ 给
	//        `merged`，不重复加前缀（`merged.merged.title` 是自加前缀的产物）。
	//    与「有没有同名兄弟口」无关：判据只读**绑定点指向谁**。
	if t := g.effectivePortType(ref.nodeID, ref.output); t != nil && t.Kind == "object" {
		if _, ok := t.Props[field]; ok {
			return ref.output + "." + field, true
		}
	}
	// ③ 该段是产出方**声明的某个口** ⇒ 它就是口名（多口 code 节点的扁平字段口）。
	if g.effectivePortType(ref.nodeID, field) != nil {
		return field, true
	}
	// ④ 都不是 ⇒ 该段谁也产不出 ⇒ 不可解析（调用方据此 fail loud，
	//    绝不发明一个名字：禁止推断型静默 fallback）。
	return "", false
}

// bindPortOf 是「一次调用的值**绑进变量**时该写哪个端口名」的唯一规则。
//
// 判据是**产出方节点的实际声明口**，不是被调用者的种类名（kind）：
//
//	· 产出方是 CodeRun（合成 shim / 用户 foreign code）⇒ 它的真口
//	  （codeWholeValuePort：shim 的口名 = 它生成的 python 实际写出的键）；
//	· 其余原生产出方沿用既有 kind 规则（llm 信封 / http·tool 整值标记 / 原生节点口）。
//
// 为什么不能按 kind 枚举（一手实证，2026-09-19）：json.stringify / json.parse /
// 数组·映射字面量等**合成 shim** 的 kind 落在 wholeValueBindingPort 的缺省分支
// ⇒ 绑定口被写成字面量 `"output"`，而 shim 声明的口是 `result`（python 写出的键）。
// 产物里于是出现「ref 指向一个谁也产不出的口」——它今天不报错，仅仅因为
// coze-biz 渲染器**无条件**给每个 code 节点追加了一个 `output: string` 声明
// （render_node.go 的历史兜底）⇒ 该 ref 解析到一个运行期恒为零值的幽灵口
// （静默丢值），且把 code 节点的声明口集污染成语料 0 例的形状（672 份真实导出里
// 没有任何 code 节点同时在 `output` 之外还有别的口）。同一根因在 buildMap 处
// 曾被就地修过一次（build_call.go 的 MapLit 注释），但其余站点照旧 ——
// 形态枚举式推广的反模式；本函数把它收成一条规则。
func bindPortOf(node Node, kind string) string {
	if cr, ok := node.(*CodeRun); ok {
		return codeWholeValuePort(cr)
	}
	return wholeValueBindingPort(kind)
}

// 整值**字段表**的权威（判据用来分「作者写错了」与「注册表缺声明」两类）。
//
// 分类判据（A12-H4）：
//
//	字段表已知 ⇒ 该值确实是多端口 record ⇒ 消费位置引用整值 = **作者写错了**
//	             （LanguageError，class 缺省 = language）；
//	字段表未知 ⇒ 输入配置没声明这个产出方的输出（典型：能力头文件/注册表里的
//	             plugin 只声明了操作、没声明 outputs）⇒ 我们**编码不出**逐字段
//	             具名出口 ⇒ MissingDeclarationError（class = config）。
//
// 两者**都不能静默**：前者若静默就是把作者的整值引用绑成一个不存在的口（or
// 恰好同名的字段口）；后者若静默就是发明一个口名（"禁推断型静默 fallback"）。
// 从绑定点记录（那里的 ref 带语义类型），因为普查处在节点视图上没有 ref。
// noteWholeValueFields 在**绑定点**记录该产出方整值的语言字段表权威
// （plugin 的字段表在能力注册表里，IR 侧未挂到节点上 —— 登记残余 ①；
// 这里的记录只解决「普查怎么拿到它」，不解决「节点自己携带它」）。
func (g *Builder) noteWholeValueFields(id string, ref varRef) {
	if ref.typ == nil || ref.typ.Kind != "object" || len(ref.typ.Props) == 0 {
		return // 权威未知：不记录 ⇒ 普查按「缺声明」分类（config）
	}
	fields := make([]string, 0, len(ref.typ.Props))
	for name := range ref.typ.Props {
		fields = append(fields, name)
	}
	sort.Strings(fields)
	if g.wholeValueFieldsByNode == nil {
		g.wholeValueFieldsByNode = map[string][]string{}
	}
	g.wholeValueFieldsByNode[id] = fields
}

// wholeValueHasNoCarrier 判定产出方的整值**没有**单一承载口（判据的唯一输入）。
//
// 判据保守：只有一手证据的三个族返回 true，其余族一律 false（绝不猜）——猜错
// 方向 = 把合法程序判成语言错。
func wholeValueHasNoCarrier(nd Node) bool {
	switch n := nd.(type) {
	case *HTTPCall, *ToolCall:
		return true
	case *CodeRun:
		// 只判**用户写的** `foreign code` 块。编译自建的 shim（json.parse /
		// list op / 记录字面量 / 零值 / LLM 信封）都声明单一整值口（`result` /
		// `response` / `output`），且它们的字段投影是 **dotted 路径**
		// （`output.title`，见 fieldPort 的 "json" 分支）——dotted 路径的
		// 首段恰好等于整值标记，故判据必须按节点身份（Synthetic）而不是
		// 只看端口名，否则 json.parse 的合法字段访问会被误判（实测）。
		if n.Synthetic || len(n.OutTypes) <= 1 {
			return false
		}
		_, ok := n.OutTypes[wholeValueMarker]
		return !ok
	}
	return false
}

// checkWholeValueRefs 是规则的语言层闸口：在消费表上找出「读了没有承载口的
// 整值」的产出方。
//
// 调用点 = IR 修订之后（build() 末尾）。判据看的是**幸存的真实引用**：
// 未被消费的绑定（`let v = http(…)` 之后没用）与只被死脚手架读到的绑定都不算
// ——「绑了不用」是合法程序。
func (g *Builder) checkWholeValueRefs() error {
	nodes := collectIRNodes(g.graph)
	// 编译自建的脚手架不是「读者」（见 attemptScaffoldEndIDs）：把它们先从
	// 节点视图里摘掉，它们的 Inputs 就不进消费表。
	for id := range g.attemptScaffoldEndIDs() {
		delete(nodes, id)
	}
	consumers := collectIRConsumers(g.graph, nodes)
	// 豁免：**容器产出绑定**是平台有承载形式的整值位置（见
	// containerOutputWholeValueSites 的语料证据），不是「作者写错了」。
	// 计数口径与 collectIRConsumers 逐字一致（它会经两条路径各数一次），
	// 否则豁免会多减/少减。
	for id, ports := range consumers {
		if n := ports[wholeValueMarker]; n > 0 {
			ports[wholeValueMarker] = n - g.containerOutputWholeValueSites(id)
		}
	}
	// 诊断必须可复现（map 迭代顺序是随机的）：按产出方 id 排序。
	var offenders []Node
	seen := map[string]bool{}
	for id, ports := range consumers {
		if ports[wholeValueMarker] == 0 {
			continue
		}
		nd := nodes[id]
		if nd == nil || !wholeValueHasNoCarrier(nd) {
			continue
		}
		key := fmt.Sprintf("%T", nd)
		if seen[key] {
			continue
		}
		seen[key] = true
		offenders = append(offenders, nd)
	}
	if len(offenders) == 0 {
		return nil
	}
	sort.Slice(offenders, func(i, j int) bool { return offenders[i].NodeID() < offenders[j].NodeID() })
	// 分类（见 wholeValueFieldsByNode 头注）：字段表未知的产出方排在前面 ——
	// 「补声明」是「改代码」的前置（改完还得有字段名可写）。
	var declared, undeclared []Node
	for _, nd := range offenders {
		if _, known := g.wholeValueFieldsOf(nd); known {
			declared = append(declared, nd)
			continue
		}
		undeclared = append(undeclared, nd)
	}
	if len(undeclared) > 0 {
		return &MissingDeclarationError{
			Capability: "config.capability_outputs_missing",
			Message:    wholeValueMissingDeclMessage(undeclared),
		}
	}
	return &LanguageError{
		Code:    diag.RECORD_FIELD_REQUIRED,
		Message: wholeValueRefMessage(declared),
	}
}

// attemptScaffoldEndIDs 收集**编译自建的尝试臂终端**（AttemptStmt 的
// SuccessEnd / FailureEnd）的节点 id。
//
// 它们不是「读者」：Inputs 里那条引用的语义是**语句自身的值**——若某个臂真的
// 把它交出去，出口的糖展开（wholeRecordReturnLiteral）早已把它改写成逐字段
// 引用；臂里没写 return 时它就是死脚手架（不渲染、IR 优化摘掉）。把它当消费
// 者会把「作者根本没提这个值」的合法程序判成语言错（一手实测：
// `attempt http(…) as r { success { out = "…"; } }` —— 作者只用了副作用）。
func (g *Builder) attemptScaffoldEndIDs() map[string]bool {
	out := map[string]bool{}
	if g.graph == nil {
		return out
	}
	forEachStmt(g.graph.Root, func(s Stmt) {
		a, ok := s.(*AttemptStmt)
		if !ok {
			return
		}
		if a.SuccessEnd != nil {
			out[a.SuccessEnd.ID] = true
		}
		if a.FailureEnd != nil {
			out[a.FailureEnd.ID] = true
		}
	})
	return out
}

// wholeValueRefMessage 把判据结果写成作者能据以行动的一句话：错在哪 + 怎么改。
//
// 反面教材（本判据要消灭的两种文案）：`ARTIFACT_GATE … ref 100002.output does
// not resolve`（内部术语 + 节点号，作者无从下手）与「编译 SUCCEEDED 而值丢了」。
func wholeValueRefMessage(offenders []Node) string {
	parts := make([]string, 0, len(offenders))
	for _, nd := range offenders {
		fields := wholeValueFieldNames(nd)
		switch {
		case len(fields) > 0:
			parts = append(parts, fmt.Sprintf("%s 的结果由 %d 个字段承载（%s）",
				wholeValueProducerName(nd), len(fields), strings.Join(fields, " / ")))
		default:
			parts = append(parts, fmt.Sprintf("%s 的结果由多个字段承载", wholeValueProducerName(nd)))
		}
	}
	return fmt.Sprintf(
		"record 值没有单一承载口：%s，平台上没有「整个 record」这个端口。"+
			"此处必须写明要用的字段（例如 `v1.body`）；"+
			"要把整个 record 交出去，则先绑定到变量再在 `return` 里交出"+
			"（`let v = …; return v;` —— 流程出口会把整值逐字段具名）",
		strings.Join(parts, "；"))
}

// containerOutputWholeValueSites 数出「容器产出绑定」通道上读 `id` 的整值多少处
// ——这条通道**平台有承载形式**，故整值引用在此合法。
//
// 平台证据（② 一手，2026-09-17 全语料实测）：
//
//	`path: ""`（整节点引用）在 322 份 coze-biz 真实导出里出现 **8 处，全部**位于
//	loop/batch 节点的 `node_outputs` 绑定内（end 节点 0/322）；语料样本
//	`S1_xuanku_shudan` 的 batch 产出 `{type: list, items: object{infos: list<string>},
//	value: {path: "", ref_node: <plugin>}}` —— 元素就是**整节点 record**。
//
// 为什么必须豁免（不是放宽判据）：raising 侧对同一形状产出的是整节点 yield
// （同一份语料反编译 ⇒ `let v12 = plugin.str_to_list({...}); yield output = v12;`），
// 若不豁免，`decompile ∘ compile` 在**真实语料**上直接断裂（一手实测：未豁免时
// 该源编译失败 RECORD_FIELD_REQUIRED）—— 而 C∘R∘C 闭环比「整值只在出口」这句
// 更基础。位置判据因此是**「平台有没有承载形式」**，不是「是不是 return」。
//
// 计数路径必须与 collectIRConsumers 一致：它对这个通道计两次（语句遍历
// markContainerOutputConsumers 一次、`*Loop` 节点分叉一次，见 optimize.go 注释
// 「两条路径都计入 ⇒ 只多不少」），故这里同样按两条路径各数一次。
func (g *Builder) containerOutputWholeValueSites(id string) int {
	if id == "" {
		return 0
	}
	count := 0
	countSources := func(srcs []Source) {
		for _, src := range srcs {
			if src.BlockID == id && portName(src.Name) == wholeValueMarker {
				count++
			}
		}
	}
	// 路径 ①：语句树上的 `*LoopStmt`（= collectIRConsumers 的
	// markContainerOutputConsumers 分支）。
	forEachStmt(g.graph.Root, func(s Stmt) {
		if ls, ok := s.(*LoopStmt); ok {
			countSources(ls.OutputSources)
		}
	})
	// 路径 ②：节点视图里的 `*Loop` 容器节点（collectIRNodes 把 `v.Container`
	// 放进节点集，collectIRConsumers 对它再计一次同一批 OutputSources）。
	for _, nd := range collectIRNodes(g.graph) {
		if lp, ok := nd.(*Loop); ok {
			countSources(lp.OutputSources)
		}
	}
	return count
}

// wholeValueFieldsOf 返回普查用的「该产出方整值的语言字段表」与权威是否存在。
//
// 三个受判族的字段表来源与 wholeValueFields（出口用）**同源**：http = 内建结果
// 契约；结构性 foreign code = 声明口；plugin = 能力注册表（在**绑定点**经语义
// 类型记录，见 noteWholeValueFields）。
func (g *Builder) wholeValueFieldsOf(nd Node) ([]string, bool) {
	switch n := nd.(type) {
	case *HTTPCall:
		return builtin.LangFields(IRPortVocabularyTarget(), "http"), true
	case *CodeRun:
		return append([]string(nil), n.OutOrder...), true
	case *ToolCall:
		fields, ok := g.wholeValueFieldsByNode[n.ID]
		return fields, ok
	}
	return nil, false
}

// wholeValueMissingDeclMessage 写「缺什么声明、谁来补」：作者动作是先补能力
// 声明（或直接写明字段），不是改自己的流程逻辑。
func wholeValueMissingDeclMessage(offenders []Node) string {
	parts := make([]string, 0, len(offenders))
	for _, nd := range offenders {
		parts = append(parts, wholeValueProducerName(nd))
	}
	return fmt.Sprintf(
		"无法编码整值引用：%s 的**输出表没有声明**（能力头文件 / 能力注册表缺 outputs）"+
			"⇒ 编译器不知道这个值由哪些字段承载，既不能逐字段具名、也不能确认没有整值口。"+
			"请补该能力声明的 outputs（或在源码里写明要用的字段 `v.<字段>`）",
		strings.Join(parts, "；"))
}

// wholeValueFieldNames 返回产出方整值的语言层字段名（有序；无权威 ⇒ nil）。
func wholeValueFieldNames(nd Node) []string {
	if cr, ok := nd.(*CodeRun); ok {
		return append([]string(nil), cr.OutOrder...)
	}
	return declaredWholeValueFields(nd)
}

// wholeValueProducerName 是诊断里对产出方的称呼（作者写的调用形式，而非内部类型名）。
func wholeValueProducerName(nd Node) string {
	switch n := nd.(type) {
	case *HTTPCall:
		return "http(...)"
	case *ToolCall:
		return fmt.Sprintf("插件调用 %q", n.ToolName)
	case *CodeRun:
		return "`foreign code` 块"
	}
	return string(nd.Kind())
}
