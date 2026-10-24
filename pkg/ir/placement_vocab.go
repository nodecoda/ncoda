package ir

// ── 决策日志的**受控词表**（R-44）─────────────────────────────────────────────
//
// 正本 = `docs/five-step/spec/SHIM-ESCAPE-CONTRACT.md` §5-1。
//
// 问题：`Placement.Construct` 曾是自由拼接（`"condition:" + e.Op()` · `"calc:" + calleeName(…)`），
// `Placement.Reason` 是中英混排散文（有些带票号）⇒ **任何「按决策类型统计」的判据都不可能有
// 稳定分母**：同一个意思能写出多种拼写，统计出来的数字不可复现。
//
// 规范形（本刀定死，两条）：
//
//	construct = `<家族>:<细节>`   —— 家族 ∈ **闭集**（下表）；细节自由（算子 / callee 名 / 子流程名）
//	reason    = `<cause>: <自由补充>` —— cause ∈ **闭集 4 值**（下）；补充自由
//
// 判据 = `pkg/ir/placement_vocab_contract_test.go`（**静态**：逐调用点读源码，
// 家族 / cause 必须来自本表；且本表**无死项**——声明了没人用的族 = 本仓已识别的病）。
//
// 为什么保留细节段而不是把 construct 直接换成 48 表键：细节（算子 / callee / 子流程名）
// 是**诊断信息**，删了就只剩「一次 shim」；而 48 键由本表的映射给出（见 `placementFamilies`），
// 统计时按映射分组即可 —— 诊断与可判两侧都要。

const (
	// PlacementFamilyCondition：**位置轴**（条件槽），宿主由上下文决定（`if` / `while` / 三元 / 返回）。
	PlacementFamilyCondition = "condition"
	// PlacementFamilyLoop：**位置轴**（迭代输入），宿主 = `for` / `while` / `for` 表达式。
	PlacementFamilyLoop = "loop"
	// PlacementFamilyBinary：`BinaryExpr`（含比较；细节 = 算子）。
	PlacementFamilyBinary = "binary"
	// PlacementFamilyCalc：`CallExpr` 的 **calc / 文本内建**面（细节 = callee）。
	PlacementFamilyCalc = "calc"
	// PlacementFamilyBuiltin：`CallExpr` 的**命名内建**（`join` / `split`；细节 = 内建名）。
	PlacementFamilyBuiltin = "builtin"
	// PlacementFamilyListOp：`CallExpr` 的**集合算子**（细节 = callee）。
	PlacementFamilyListOp = "list-op"
	// PlacementFamilyJSON：`CallExpr` 的 `json.parse` / `json.stringify`（细节 = 操作名）。
	PlacementFamilyJSON = "json"
	// PlacementFamilyLLM：`CallExpr` 的 `llm`（细节 = 决策形态，例 `whole-envelope`）。
	PlacementFamilyLLM = "llm"
	// PlacementFamilyWorkflow：`CallExpr` 的**工作流操作**（`http` / `knowledge` / `extract` / `agent`…）。
	PlacementFamilyWorkflow = "workflow"
	// PlacementFamilyTool：`CallExpr` 的**插件 / 工具**（细节 = callee）。
	PlacementFamilyTool = "tool"
	// PlacementFamilySubWorkflow：`CallExpr` 的**子流程声明调用**（细节 = 子流程名）。
	PlacementFamilySubWorkflow = "sub_workflow"
	// PlacementFamilyAgentMode：`CallExpr` 的 **agent-mode 入口**（细节 = 操作名）。
	PlacementFamilyAgentMode = "agent-mode"
	// PlacementFamilyAdvancedChat：`CallExpr` 的 **advanced-chat 入口**（细节 = 操作名）。
	PlacementFamilyAdvancedChat = "advanced-chat"
	// PlacementFamilyForeignCode：`ForeignCodeExpr`（用户手写的盒子，透传）。
	PlacementFamilyForeignCode = "foreign-code"
)

// placementFamilies 家族 → 它属于哪个**构造**。`""` = **位置轴**（48 构造表里没有
// 「条件槽 / 迭代输入」这两个构造——它们是**位置**，宿主由上下文决定）。
//
// 这张表是「按 48 构造统计决策」的唯一分母：统计时 `PlacementFamilyConstruct` 查一次即可。
var placementFamilies = map[string]string{
	PlacementFamilyCondition:    "",
	PlacementFamilyLoop:         "",
	PlacementFamilyBinary:       "BinaryExpr",
	PlacementFamilyCalc:         "CallExpr",
	PlacementFamilyBuiltin:      "CallExpr",
	PlacementFamilyListOp:       "CallExpr",
	PlacementFamilyJSON:         "CallExpr",
	PlacementFamilyLLM:          "CallExpr",
	PlacementFamilyWorkflow:     "CallExpr",
	PlacementFamilyTool:         "CallExpr",
	PlacementFamilySubWorkflow:  "CallExpr",
	PlacementFamilyAgentMode:    "CallExpr",
	PlacementFamilyAdvancedChat: "CallExpr",
	PlacementFamilyForeignCode:  "ForeignCodeExpr",
}

// Placement reason 的 **cause 闭集**（4 值）。四者的分界（一处定义，判据按此落）：
//
//	capability      平台**有**这个原生面，本次决策走的就是它（例：条件单目 op 11 ·
//	                TextProcessor concat · 原生 HTTP/LLM/知识库节点）
//	no-native-node  平台**没有**承载这个**值**的原生面（节点类型或口形态都没有）
//	                （例：无点分口形态 · 无原生算术节点 · 目标没有原生 json 节点（参考目标）·
//	                有节点但承载不了这个载荷 —— `json` 内建的两个子面见
//	                `Builder.recordJSONBuiltinPlacement`）
//	contract        由**本仓不变量**决定，与平台能力无关（例：P1 数据依赖 · 透传契约 ·
//	                循环保持平台容器）
//	purity          shim **纯度**义务（用户手写的盒子不改写、不半反向）
const (
	PlacementCauseCapability   = "capability"
	PlacementCauseNoNativeNode = "no-native-node"
	PlacementCauseContract     = "contract"
	PlacementCausePurity       = "purity"
)

// PlacementFamilyConstruct 家族 → 构造（48 构造表的键；`""` = 位置轴）。
// 未注册的家族 ⇒ `ok = false`（调用方必须 fail loud，不得猜）。
func PlacementFamilyConstruct(family string) (string, bool) {
	key, ok := placementFamilies[family]
	return key, ok
}

// Family 返回该决策的**家族**（`construct` 的第一段）。
func (p Placement) Family() string {
	return constructFamily(p.Construct)
}

// Cause 返回该决策的**原因闭集项**（`reason` 的第一段）。
func (p Placement) Cause() string {
	return reasonCause(p.Reason)
}

// placementConstruct 拼一条 construct：`<家族>:<细节>`（`细节 == ""` ⇒ 只有家族）。
// **拼写只有这一处** —— 调用点不得自己拼字符串（拼错 = 统计分母裂开）。
func placementConstruct(family, detail string) string {
	if detail == "" {
		return family
	}
	return family + ":" + detail
}

// placementReason 拼一条 reason：`<cause>: <自由补充>`（`note == ""` ⇒ 只有 cause）。
// **拼写只有这一处**。
func placementReason(cause, note string) string {
	if note == "" {
		return cause
	}
	return cause + ": " + note
}

// constructFamily 取 `construct` 的第一段（`"calc:len"` ⇒ `"calc"`）。
func constructFamily(construct string) string {
	for i := 0; i < len(construct); i++ {
		if construct[i] == ':' {
			return construct[:i]
		}
	}
	return construct
}

// reasonCause 取 `reason` 的第一段（`"contract: 纯变换"` ⇒ `"contract"`；无冒号 ⇒ 整条）。
// 分隔符 = `": "` **或** `":"`：历史散文里出现过 `note:` 形态，取第一段即可（判据按闭集判）。
func reasonCause(reason string) string {
	for i := 0; i < len(reason); i++ {
		if reason[i] == ':' {
			return reason[:i]
		}
	}
	return reason
}

// recordJSONBuiltinPlacement 记一条 json 内建的**落点**决策。
//
// json 是 **ncoda 内建**（`docs/five-step/spec/SHIM-ESCAPE-CONTRACT.md` §0.1 的
// `shim_required ⟺ L ∧ ¬∃c: host(c,v)`）：平台只是**恰巧**有承载节点，
// 所以「落原生还是落 shim」由两条**独立**事实共同决定，缺一不可 ——
//
//	① 平台**有没有**这个原生节点 —— 能力轴 `capability.Table.JSONSerial/JSONDeserial`
//	   （本函数只读 `g.caps`，不另立一份「平台知识」）；
//	② 这个**载荷**能不能逐字放进那个节点 —— `JSON*NativeHostable`（IR 侧可判）。
//
// 两条都成立 ⇒ **原生**（NATIVE / cause=capability）；任一不成立 ⇒ python shim
// （SHIM / cause=no-native-node：目标没有该节点，或没有能承载**这个值**的口形态 ——
// cause 的定义见上方「承载这个**值**的原生面」）。
//
// 为什么决策必须记在这里：`Placement` 是**产物落点**的记录，而落点由「能力轴 × 载荷」
// 决定。历史实现把它写死在构造期（恒 SHIM + 理由「coze re-hosts as type-58」）——
// 于是**日志与产物互相矛盾**（一手：coze-biz 上 `json.stringify`/`json.parse` 的产物
// 是原生 `to_json`/`from_json`，日志却写「平台没有承载面」）。判据 =
// `internal/compiler/cozebiz_json_builtin_placement_contract_test.go`（日志必须与产物同指）。
func (g *Builder) recordJSONBuiltinPlacement(id, op string, node Node) {
	hosts := false
	if g.caps != nil {
		switch op {
		case "stringify":
			hosts = g.caps.JSONSerial
		case "parse":
			hosts = g.caps.JSONDeserial
		}
	}
	hostable := false
	if cr, ok := node.(*CodeRun); ok {
		switch op {
		case "stringify":
			hostable = cr.JSONSerializeNativeHostable()
		case "parse":
			hostable = cr.JSONParseNativeHostable()
		}
	}
	target := "the target"
	if g.caps != nil && g.caps.Target != "" {
		target = g.caps.Target
	}
	axis := "capability.Table.JSONSerial"
	if op == "parse" {
		axis = "capability.Table.JSONDeserial"
	}
	switch {
	case hosts && hostable:
		g.recordPlacement(id, placementConstruct("json", op), PlacementNative,
			placementReason(PlacementCauseCapability,
				"ncoda json builtin hosted by the target's native JSON node ("+axis+"; ref-bound input + declarable schema)"))
	case !hosts:
		g.recordPlacement(id, placementConstruct("json", op), PlacementShim,
			placementReason(PlacementCauseNoNativeNode, target+" declares no native JSON node for this builtin ("+axis+"=false)"))
	default:
		g.recordPlacement(id, placementConstruct("json", op), PlacementShim,
			placementReason(PlacementCauseNoNativeNode,
				"target declares a native JSON node but it cannot carry this payload verbatim (needs a single ref-bound input and a declarable schema)"))
	}
}
