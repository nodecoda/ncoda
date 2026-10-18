package ir

import (
	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/builtin"
)

// 本文件 = 「一个调用的值由哪些端口承载」的唯一读取点（整值绑定的规则表）。
//
// 规则只有一条：**值由产出方声明的物理端口承载**。
//
//	① 值恰好由一个端口承载 ⇒ 该口就是整值（code 的 output、llm 的 response、
//	   knowledge 的 outputList、subflow/QA 的单口、start 的入参口）。
//	② 值由多个互相独立的端口承载（http 的 body/headers/statusCode；plugin 的
//	   扁平字段口）⇒ **平台上没有「整值引用」这种形式**：值的承载就是这组字段，
//	   消费方必须按 record 语义展开——作者显式写 record 的两种写法是同义的：
//
//	       return v            ⟺   return { f1: v.f1, …, fn: v.fn }
//
// 为什么需要它：绑定的端口名曾由**消费方发明**——`materializeCall` 对所有调用
// 一律写 `output := "output"`，而 `output` 在 http / plugin 上根本不存在：
//
//	http   产物 ref 指向未声明端口 ⇒ ARTIFACT_GATE（响，但编译失败）
//	plugin 同上（plugin 声明的是扁平字段口：语料 4665 个 plugin 节点 0 例 output 包装口）
//
// 本规则**只在流程出口（`return`）落地**（见 wholeValueAtReturn）：出口是
// 「值必须被指名交给平台」的位置；其余位置（模板槽 / 实参 / print）今天走的是
// 既有通路，改动它们的端口含义会牵动已成立的产物形状（实测 43 例回归），
// 那些位置各自的缺口另行登记。
//
// 与 `ref.typ` 的关系：`materializeCall` 只为 plugin（kind=tool）盖语义类型戳——
// 该戳即产出方**声明的端口表**（`ToolCall.ResultType` = 能力注册表的 output 表，
// 见 `ir.go` 该字段注；R-28 · 2026-09-24 把它接到 φ 槽权威链的 ③ 级，R-130 ·
// 2026-09-28 接通整值读取）。本路径读的 `ref.typ` 与 `ResultType` **同值同源**
// （`buildTool` 与 `callBindRef` 都取自 `semanticExprType(call)`）—— 旧注「端口表在
// 能力注册表、IR 侧未挂」是接线前的化石，已删除。其余族保持既有推断链不动：
// `resolveValueRef` 会**保留基 ref 的 typ**，把容器类型盖到字段引用上
// （实测 dify E5001：`http_2.body` 被标成 object 而声明是 string）。

// wholeValueAtReturn 判定 `return v` 里 v 的整值在**流程出口**由什么承载。
//
// 返回三选一（互斥）：
//
//	port != ""            ⇒ 整值挂在端口 port 上：出口绑定该口（与 ref.output 不同
//	                        时才需要改绑，见 rebind）。
//	fields != nil         ⇒ 整值是**多端口 record**：出口必须逐字段具名输出
//	                        （wholeRecordReturnLiteral 把 `return v` 展开成
//	                        `return {f: v.f, …}`）。
//	都不满足（port=="" 且 fields==nil）⇒ 不是整值绑定或无字段权威：保持原路径。
//
// **只在绑定确实指向「整值」时**才给出结论：`ref.output` 若是产物内部的**投影
// 路径**（`output.data.doc_results.url` 一类的字段下钻），它指向的是字段值而不是
// 整值，本函数返回空（否则会把 `return url;` 误展开成一个单字段 record —
// 实测回归 E5-b）。
func (g *Builder) wholeValueAtReturn(ref varRef) (string, []string) {
	n := g.find(ref.nodeID)
	if n == nil {
		return "", nil
	}
	switch nd := n.(type) {
	case *CodeRun:
		// 结构性输出（CodeStructuralOutput，每个声明口 = 一个字段）= **多端口
		// record**：平台没有整值口 ⇒ 整值 = 这组字段，出口必须逐字段具名
		// （与 http / plugin 同族，走 wholeRecordReturnLiteral 的糖展开）。
		//
		// 旧实现把它当单口处理（`returnCarrier(ref, "output")` 对
		// `ref.output == "a"` 原样返回 "a"）⇒ `return t;` 只绑首字段、其余字段
		// **静默丢失**，且与 `return t.a;` 逐字节同产物（一手实测：A12-H1 的
		// 「整值逐字段具名」在结构性 code 上从未成立）。
		if cr, ok := n.(*CodeRun); ok && len(cr.OutTypes) > 1 {
			if ref.output != wholeValueMarker {
				// 字段投影绑定（`t.a`）：不是整值，原路径不动。
				return "", nil
			}
			return "", append([]string(nil), cr.OutOrder...)
		}
		// 直出（唯一口 `result`）：整值真口，出口绑该口。
		return g.returnCarrier(ref, "output"), nil
	case *LLMCall:
		// 整值 = LLMResponse 信封，由目标适配层装配到 `response` 口。
		//
		// 信封口名（`response`）是**我们的 IR 内部承载名**，不是语言层字段名：
		// 拿它当出口键 = 编译器发明一个作者写不出来的字段（W1-D2 禁止）。
		// LLMResponse 的语言字段（text / reasoning_content / usage /
		// finish_reason）由结果契约给出时按字段展开；契约未给出（ref.typ 未挂）
		// ⇒ 返回单口承载：出口键走**语言默认名**（ir.FlowExitDefaultKey），
		// 信封口 `response` 只作**路径**（物理承载），不进语言表面。
		if ref.output != LLMEnvelopeOutput {
			// 字段投影绑定（`v1.text` ⇒ ref.output = "text"）：那已是单字段值，
			// 不是整值 —— 展开它会把 `return v1.text;` 误读成整 record。
			return "", nil
		}
		if fields := g.wholeValueFields(ref, n); len(fields) > 0 {
			return "", fields
		}
		return g.returnCarrier(ref, LLMEnvelopeOutput), nil
	case *TextProcess:
		return g.returnCarrier(ref, "output"), nil
	case *KnowledgeCall:
		// 平台契约：knowledge 暴露单个口 `outputList`，它承载的语言字段是
		// **`result`**（内建节点结果契约；双向共读，见 internal/builtin）。
		//
		// 故整值 = 单字段 record，出口必须**具名** `result` —— 直接绑物理口
		// 会让出口键变成 `outputList`（物理口名当键 = 编译器发明字段，W1-D2 禁止；
		// 一手实测：`return v1` 曾产出 `{name: outputList, path: outputList}`）。
		// 契约缺声明 ⇒ 保持既有绑定（不发明名字），由具名闸门 fail loud。
		if ref.output != "output" {
			// 字段投影绑定（`v1.result` ⇒ ref.output = "outputList"）：同 LLM 分支。
			return "", nil
		}
		if fields := g.wholeValueFields(ref, n); len(fields) > 0 {
			return "", fields
		}
		if p, ok := builtin.PortOf(IRPortVocabularyTarget(), "knowledge", "result"); ok {
			return g.returnCarrier(ref, p.Port), nil
		}
		return g.returnCarrier(ref, ref.output), nil
	case *HTTPCall:
		// 平台契约：http 暴露 body/headers/statusCode 三个独立口（语料 3/3 逐字段
		// 相同），没有整值口 ⇒ 整值 = 这三个字段组成的 record。
		if ref.output != wholeValueMarker {
			return "", nil
		}
		return "", g.wholeValueFields(ref, n)
	case *ToolCall:
		// 平台契约：plugin 声明**扁平字段口**（语料 4665 个 plugin 节点：'infos'、
		// 'draft_id'、… ；0 例 `output` 包装口）。端口表 = 能力注册表的声明输出表，
		// **已挂在 IR 节点上**（`ToolCall.ResultType`，见 ir.go 该字段注；R-28
		// 2026-09-24 接通 φ 槽 ③ 级）；本函数读 `ref.typ`，与 `ResultType` 同值
		// （见 callBindRef）。
		if ref.output != wholeValueMarker {
			return "", nil
		}
		return "", g.wholeValueFields(ref, n)
	case *SubWorkflowCall:
		if ref.output != "output" {
			return "", nil
		}
		if name, ok := singlePortType(nd.OutTypes); ok {
			return name, nil
		}
		return "", g.wholeValueFields(ref, n)
	case *QA:
		if ref.output != "output" {
			return "", nil
		}
		if name, ok := singlePortType(nd.OutTypes); ok {
			return name, nil
		}
		return "", g.wholeValueFields(ref, n)
	}
	// 其余族（Aggregate 组口 / Start 入参 / Loop 输出 / Selector / Assign …）：
	// ref.output 本身就是承载口，沿用不动。
	return ref.output, nil
}

// returnCarrier 是①（单一整值口）族的判据：绑定口 == carrier ⇒ 沿用；
// carrier 不同（如 knowledge：绑定的 IR 名是 `output`，平台口是 `outputList`）
// ⇒ 出口改绑到平台真正声明的那个口；绑定口是别的路径（字段投影）⇒ 不动。
func (g *Builder) returnCarrier(ref varRef, carrier string) string {
	if ref.output == "output" || ref.output == carrier {
		return carrier
	}
	return ref.output
}

// singlePortType 返回「恰好一个声明端口」时该端口名。
func singlePortType(m map[string]*Type) (string, bool) {
	if len(m) != 1 {
		return "", false
	}
	return singleOutputKey(m)
}

// wholeValueFields 返回「多端口 record 值」的**语义字段名**（有序）。
//
// 两条来源，优先级固定：
//  1. 值的语义类型（ref.typ，TypePass）——plugin 专用：它的端口表在能力注册表、
//     IR 侧未挂，字段只能由语义类型给出；
//  2. 产出方**声明的物理端口**（见 declaredWholeValueFields），名字按语言层口径
//     归一（平台口 statusCode ⇒ 语言字段 status_code）。
//
// 取不到权威 ⇒ nil，调用方保持原路径（**绝不发明字段**）。
func (g *Builder) wholeValueFields(ref varRef, n Node) []string {
	if ref.typ != nil && ref.typ.Kind == "object" && len(ref.typ.Props) > 0 {
		return sortedTypeFields(ref.typ)
	}
	return declaredWholeValueFields(n)
}

// wholeValueRecordType 返回产出方**整值**（`@whole` 口，wholeValueMarker）的类型：
// 它的**声明端口表的整体**。
//
// 这是「一张声明端口表，三种读取」里的第三种 —— 与既有两个读取点同源，因此不可能
// 漂移：
//
//	wholeValueFields(ref, n)   读字段**名**（出口糖展开：`return v` ⇒ 逐字段）
//	portTypeOfNode(n, port)    读**单个口**的类型（`v.books` ⇒ list<Book>）
//	wholeValueRecordType(n)    读**整值口**的类型（`yield p = v` ⇒ 整个 record）
//
// 为什么必须有第三种读取（R-130，一手语料）：整值口不是任何一个声明字段口，拿
// 「单口读取」去查它必然查不到（`portFieldType(ResultType, "@whole")` ⇒ nil），
// 于是整值口退化成「结构化但无 schema」的空记录占位 ⇒ 聚合口元素类型丢字段表 ⇒
// 消费容器的输入声明只剩 `{type: object}` ⇒ 下一轮 raising 判不出成员是数组、
// 丢掉元素下标（`books[0].pic` → `books.pic`）⇒ 再编译 TYPE_MISMATCH。**占位在
// 这里不是「未知」而是「已知却报未知」**：权威就在产出方上（ResultType / OutTypes /
// 内建结果契约），只是没人按整值口问它。
//
// 覆盖面 = **绑定整值标记口的族**（wholeValueBindingPort / codeWholeValuePort
// 的取值域）：tool · http（多口 ⇒ 整值标记）· code（多口 ⇒ 整值标记）。
// 其余族（llm / knowledge / subflow / QA）的整值是**单一真口**（`response` /
// `outputList` / 声明单口），绑定写的就是真口名 ⇒ 由「单口读取」作答，不经本函数。
// 返回 nil = 该族没有整值口（调用方保持原路径，绝不发明字段）。
func wholeValueRecordType(n Node) *Type {
	switch nd := n.(type) {
	case *ToolCall:
		// plugin：声明输出表 = ResultType 的 Props（字段名即物理口名，与 wire 的
		// node_outputs 同源）。未声明输出的占位本身就是「结构化未知」，原样返回。
		return nd.ResultType
	case *CodeRun:
		// 结构性输出（每个声明口 = 一个字段）⇒ 整值 = 这张口表。
		if len(nd.OutTypes) == 0 {
			return nil
		}
		return TObject(nd.OutTypes)
	case *HTTPCall:
		// 内建结果契约（不手抄副本）：字段名走 builtin.LangFields，字段类型走
		// 既有 httpResultPortType —— 与 declaredWholeValueFields 同源同序。
		props := map[string]*Type{}
		for _, lang := range builtin.LangFields(IRPortVocabularyTarget(), "http") {
			if t, ok := httpResultPortType(lang); ok {
				props[lang] = t
			}
		}
		if len(props) == 0 {
			return nil
		}
		return TObject(props)
	}
	return nil
}

// declaredWholeValueFields = 产出方声明端口在**语言层**的名字（有序）。
//
// 只列「整值由多个端口承载」的两族（见各个 case 的 ② 证据）；名字取语言层口径
// 而不是平台口名，是为了让**出口的 name**（工作流输出名）由语言语义给出，
// 而 path（平台口名）由 aggregateFieldPort 独立映射——两者本就允许不同名
// （语料 396 条 end 输入里 264 条 name ≠ path）。
func declaredWholeValueFields(n Node) []string {
	switch nd := n.(type) {
	case *HTTPCall:
		// 语言层字段名来自**内建节点结果契约**（注册表声明；本处不手抄副本）。
		// 声明顺序即语言字段顺序（body / status_code / headers），与共享原型
		// HTTPResponse（pkg/binding/symbol.go）一致——原型是权威，门禁核对。
		return builtin.LangFields(IRPortVocabularyTarget(), "http")
	case *KnowledgeCall:
		// 同上（单字段 record：`result` ⇄ 物理口 `outputList`）。
		return builtin.LangFields(IRPortVocabularyTarget(), "knowledge")
	case *SubWorkflowCall:
		return sortedKeys(nd.OutTypes)
	case *QA:
		return sortedKeys(nd.OutTypes)
	}
	return nil
}

// wholeRecordReturnLiteral 把 `return v`（v 绑定到一个**多端口 record** 值）
// 翻译成作者显式写出同一 record 时的字面量：
//
//	return v   ⇒   return { f1: v.f1, …, fn: v.fn }
//
// 两条写法的值是同一个（平台没有「节点整值」这种引用形式，record 只能由逐字段
// ref 组成），因此这是语义等价的**糖展开**，不是形态适配：展开后的 AST 与作者
// 手写的完全同形，走既有全部通路（线性出口 ⇒ 逐字段具名输出；臂内 ⇒ record
// shim + 汇聚），不引入任何新 wire 形状、新节点类型或新判据。
//
// 平台证据（② 一手）：同一 producer 贡献多条具名 end 输入是现成形状（语料 5 例，
// producer = llm/subflow 多口）；end 输入名 ≠ 端口名是常态（语料 396 条输入中 264
// 条重命名），故 `name = 语义字段名 / path = 物理端口名` 落在既有形状空间内。
func (g *Builder) wholeRecordReturnLiteral(id *ast.IdentExpr) (*ast.MapLit, bool) {
	ref, ok := g.varToOutput[id.Name()]
	if !ok || ref.nodeID == "" {
		return nil, false
	}
	_, fields := g.wholeValueAtReturn(ref)
	if len(fields) == 0 {
		return nil, false
	}
	entries := make([]ast.MapEntry, 0, len(fields))
	for _, f := range fields {
		entries = append(entries, ast.MapEntry{
			Key:   ast.NewStringLit(f),
			Value: ast.NewFieldAccessExpr(ast.NewIdentExpr(id.Name()), f),
		})
	}
	return ast.NewMapLit(entries), true
}

// normalizedExitValue 是「出口位置的值表达式 → 规范形」的**唯一**归一化点。
//
// 出口位置有两个，但**源构造成只有一个**（`return <expr>`）：
//
//	(a) 显式 `return v;`（emitReturn）；
//	(b) else-less if 的隐式 else 臂 —— 下一条语句恰好是 `return v;`
//	    （resolvePendingElse）。
//
// 两处必须给出同一规范形，所以归一化只能有一份实现（每条通路各写一遍 = 规则漂移：
// 2026-09-15 实测 A12-H10 —— 隐式 else 臂只做 emitValueExpr，`return v` 的整值被登记
// 成**发明端口** `output`（http 不声明该口）⇒ 汇聚候选指向未声明端口 ⇒ 产物悬空
// `ARTIFACT_GATE`；显式写法却逐字段展开、正常）。
//
// 归一化只做**语义等价的糖展开**：`v` 的整值由多端口承载时（平台没有「节点整值」
// 引用形式）改写为作者手写同一 record 的字面量 `{f: v.f, …}`；其余表达式原样返回
// （第二个返回值为 false ⇒ 调用方保留原 AST 节点）。
func (g *Builder) normalizedExitValue(value ast.Expr, platformExit bool) (ast.Expr, bool) {
	// 第一步：**默认键的显式形先还原成裸值**。
	//
	// 语言把「无名出口」定义成一条糖：`return x;` ≡ `return { $output: x };`
	// （W1-D2′ 裁定 (II)，判据 = **单条**入口 ∧ 键 = 语言默认常量）。这条等价在
	// lowering 的落地就是**先把整值还原成裸值**，此后与糖形共用同一条通路 ——
	// 否则两条写法在「整值不是单一端口」的值上分叉：
	//
	//   · record（http 三端口）⇒ 显式形走具名通道直读产出方的**不存在的整值口**
	//     `output` ⇒ 产物门禁 fail loud（A12-H19：`ref 100002.output does not
	//     resolve: node 100002 declares ports [body headers statusCode]`）；
	//   · 分支内 ⇒ 两条通路的**材质**不同（显式形走 record 物化、糖形走标量
	//     物化）⇒ 同一个意图两种写法给出两条 wire（A12-H18）。
	//
	// 判据与 raising 侧读回糖形的判据**同源**（单条绑定且键 = 默认常量 ⇒ 读回
	// `return <表达式>;`），故两侧互为逆：一侧不还原，`compile∘decompile` 就在
	// 这条形状上不闭合。
	changed := false
	if inner, ok := soleDefaultKeyedExit(value); ok {
		value, changed = inner, true
	}
	id, ok := value.(*ast.IdentExpr)
	if !ok || !platformExit {
		return value, changed
	}
	// 逐字段糖展开只在**平台流程出口**应用（判据见 build_return.go 调用点：
	// 内联函数体的 return 是 IR 内的值回流，不是平台出口）。
	if m, ok := g.wholeRecordReturnLiteral(id); ok {
		return m, true
	}
	return value, changed
}

// soleDefaultKeyedExit 判定「单条入口 ∧ 键 = 语言默认常量」的出口 map 字面量，
// 返回该入口的值表达式。
//
// 这就是 W1-D2′ 裁定 (II) 定义的糖：`return { $output: x };` ≡ `return x;`
// ——**单条**是判据的一部分（多入口的 map 是一张真的具名输出表，其中名为 `output`
// 的入口只是一个名字），且键的比较对象是**语言常量** `FlowExitDefaultKey`，
// 不是拼写（`$output` 的转义在 `staticMapKeyName` 已剥）。
//
// 与 raising 的镜像关系：读回侧对「单条绑定且键 = 默认常量」的出口产出糖形
// （platforms/cozebiz/decompile/convert.go），故本函数是那条规则的 lowering 逆。
func soleDefaultKeyedExit(value ast.Expr) (ast.Expr, bool) {
	m, ok := value.(*ast.MapLit)
	if !ok || m == nil || len(m.Entries()) != 1 {
		return nil, false
	}
	entry := m.Entries()[0]
	name, ok := staticMapKeyName(entry.Key)
	if !ok || name != FlowExitDefaultKey {
		return nil, false
	}
	return entry.Value, true
}

// rebindExitPort 是出口归一化的**第二半**（与 normalizedExitValue 同一判据面，
// 两条 return 通路共用）：`return v` 的整值由**单一端口**承载而该口与 IR 的通用绑定
// 名（`output`）不同 ⇒ 出口改绑到平台真正声明的那个口（knowledge ⇒ outputList）；
// 绑定已是字段投影路径时不动。
func (g *Builder) rebindExitPort(value ast.Expr, ref varRef) varRef {
	if id, ok := value.(*ast.IdentExpr); ok {
		if port, ok := g.returnWholeValuePort(id); ok {
			ref.output = port
		}
	}
	return ref
}

// returnWholeValuePort 返回 `return v` 应当绑定的端口（v 的整值由单一端口承载
// 时）；第二个返回值 false ⇒ 不适用（多端口 record / 非整值绑定 / 无变化）。
func (g *Builder) returnWholeValuePort(id *ast.IdentExpr) (string, bool) {
	ref, ok := g.varToOutput[id.Name()]
	if !ok || ref.nodeID == "" {
		return "", false
	}
	port, fields := g.wholeValueAtReturn(ref)
	if port == "" || fields != nil || port == ref.output {
		return "", false
	}
	return port, true
}

// nodeKindOf 返回节点的 IR kind（诊断用；nil / 未知 ⇒ "unknown"）。
func nodeKindOf(n Node) string {
	if n == nil || isNilIRNode(n) {
		return "unknown"
	}
	return string(n.Kind())
}

// canonicalEndOutputs 把出口绑定表落到**规范序** —— 字段名升序。
//
// 规则一条：**序不是内容**（与 record_type_name.go 头注「字段**序**不是契约 ⇒
// 排序」同一把尺）。理由是同一条 record 值由两条通路投影到平台出口：
//
//	(a) 作者手写具名出口 `return {a: …, b: …}` ⇒ 入口书写序；
//	(b) 整值出口 `return v`（多端口承载：http / plugin / 结构性 code）⇒
//	    糖展开成 (a) 的形，字段序 = **产出方声明端口序**（declaredWholeValueFields）。
//
// 两条写法的值是同一个（同一个 record），但入口序不同 ⇒ 若不规范化，`end` 的
// `node_inputs` 列表序与 φ 的 `mergeGroups[*].name` 序列都跟着写法走 ⇒ 同一语义
// 两种写法两种产物（R-41 一手：`{body, status_code, headers}` 与它的反序编译出
// 逐字节不同的 yaml）。
//
// 排序键 = **字段名**（语言层语义名），与名字锚 recordAnchorFromSchema 的排序键
// 同源；**不是**端口名（`status_code`/`statusCode` 这类 name↔path 差异由
// aggregateFieldPort 独立映射，两者本就允许不同 —— 拿端口名排序会把「哪个字段」
// 这个语义锚换成物理拼写）。
//
// 不在这里做的是「让判据去抹序」：`node_inputs`/`mergeGroups` 是**具名列表**
// （语义上是 map），但同一层里还有**真有序**的列表（`mergeGroups[*].variables`
// 的候选优先序 = merge 取第一个非零候选的语义本体）⇒ 判据无法从形状区分两者，
// 一律抹序会把候选优先序一起抹掉。故序只能在**生产端**规范化（本条），不能放宽判据。
func canonicalEndOutputs(outputs []EndOutput) []EndOutput {
	for i := 1; i < len(outputs); i++ {
		for j := i; j > 0 && outputs[j].Name < outputs[j-1].Name; j-- {
			outputs[j], outputs[j-1] = outputs[j-1], outputs[j]
		}
	}
	return outputs
}

// sortStrings 就地对字符串切片排序（标准库调用点收敛在此）。
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
