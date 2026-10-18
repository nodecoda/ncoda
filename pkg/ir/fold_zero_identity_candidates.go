package ir

import (
	"sort"
	"strings"

	"github.com/nodecoda/ncoda/pkg/pyast"
)

// fold_zero_identity_candidates.go — IR 优化阶段：**恒等转发零值候选**的重定向。
//
// 背景（2026-09-13，docs/ideas-todo/2026-09-13-identity-relay-zero-candidate-place.md）：
//
//	一行源码 `if (c) { v = "x"; }`（v 在 junction 之前已绑定为某个**零值**载体）
//	在 lowering 里会为「未写臂」物化两跳死重量：一个零值常量载体 + 该载体之上的
//	**臂内恒等转发 relay**，并把 relay 登记成 merge 候选。全量语料实测：53 份 /
//	**207 条**这样的 relay，只出现在我们的首编产物里（二编就没有了），是
//	「一轮往返不是不动点」的头号来源。
//
// 规则（一条，普遍、无形态守卫 —— 证据见下）：
//
//	汇聚（`*Aggregate` 的每个组 / legacy `Refs`）的一个候选若指向**恒等转发
//	relay**，且沿它的唯一输入链回溯**终结于一个可证恒为零值的载体**，则该候选
//	**重定向到链尾的零值真相**。删掉的是「多余的一跳」，候选数**不变**。
//
// 为什么是重定向而不是删除候选（两条独立证据，都是第一手）：
//
//  1. **删除候选 ⇒ 拒收自家产物**：手工把候选数从 2 减到 1 后反编译直接
//     `RS-MRG-001 flow exit value has no rendering site` fail loud。合并组
//     的「候选 ↔ 臂」对应关系是 wire 的结构契约，候选数不得凭优化变少。
//  2. **重定向 = 编译器自己已经到达的形状**：第 2 轮产物（B2）本来就是
//     「relay 没了、候选直引零值源、候选数不变」；实测把 B1 手工改成同一形状
//     后反编译读回合法 ncoda、重编译 SUCCEEDED、且一轮即不动点。
//     ⇒ 本 pass 做的就是「把第 2 轮的结果提前到第 1 轮」。
//
// 语义等价性论证（不是近似）：merge 的运行时语义是 first_non_zero（候选缺省 =
// 类型 zero、返回第一个非 zero、全 zero ⇒ zero）。relay 是 `return {"result":
// arg1}` 的恒等复制 ⇒ 该候选的值**逐字等于**它的输入源的值。把候选从 relay
// 改指到输入源，读到的值**完全相同**（同一条数据依赖，只是少一跳 shim）。
// 反向不成立：源**非**零时 relay 在臂控制路径上的位置是语义承载的（junction
// 前的绑定恒可读，未执行的臂则被 zero 吸收）——所以本规则**只**在链尾可证为零
// 时触发，非零源一律原样保留（存活侧锚点）。
//
// 为什么在 IR 优化阶段（用户 2026-09-13 拍板）：判据是「值可证为零」= 全局 +
// 传递（relay 链、record 逐字段、嵌套 dict 全零、LoopReturn 零值 shim 等多个
// 出生点）；出生点各加一份必然漂移（R-F2 复发机制）。本 pass 与
// PruneDeadPureNodes 同层同尺（见下），在 coze-biz lowering 入口调用；重定向后
// relay 失去唯一消费者 ⇒ 由既有 P23 删除 ⇒ 由既有 compact_pruned_ids 闭合 id
// 足迹（三步都不新增机制）。
//
// 判据的「尺」与 P23 完全一致（`*CodeRun ∧ Synthetic ∧ shim marker`）：
// 用户手写的 foreign code（Synthetic=false）即使算出来是全零也**永不**被当作
// 零值载体（全量语料实测 68 个用户手写全零 code / 14 份文件，误判即改语义）。
func FoldZeroIdentityCandidates(g *Graph) bool {
	if g == nil {
		return false
	}
	nodes := collectIRNodes(g)
	// 作用面 = **全部**汇聚，无形态守卫（2026-09-13，S2-gate 阻断项 B1 处置后；
	// 证据见 docs/ideas-todo/2026-09-13-identity-relay-zero-candidate-place.md §9.2）。
	// 曾短暂收窄为「只折叠循环容器**外**的汇聚」，理由是实测
	// `X2_W05_douyinweidu_word` / `interview_book_video` 在读回后重编译时出现悬空
	// 引用（`ARTIFACT_GATE … ref to unknown node`）。**该理由本次以 671 份语料
	// A/B 复测不成立**：带/不带收窄的三层数字、差异文件集合、编译状态与诊断
	// 直方图**逐项相同**，而删掉收窄让循环体内 2 份文件的首编relay 也一并消失
	// （更接近不动点）。形态守卫一律不留（AGENTS.md P24 / R-F2）。
	// 计划阶段（先算出全部重写点，再统一落地——`nodes` 的遍历顺序不影响结果）：
	// aggID → 候选下标（按 `Refs` 后接各 `Groups` 的拼接序）→ 链尾零值真相。
	planned := map[string]map[int]Ref{}
	for id, node := range nodes {
		agg, ok := node.(*Aggregate)
		if !ok {
			continue
		}
		planned[id] = map[int]Ref{}
		idx := 0
		consider := func(refs []Ref) {
			for _, r := range refs {
				if truth, ok := zeroTruthOf(nodes, r); ok {
					planned[id][idx] = truth
				}
				idx++
			}
		}
		consider(agg.Refs)
		for _, grp := range agg.Groups {
			consider(grp.Refs)
		}
	}
	changed := false
	for id, node := range nodes {
		agg, ok := node.(*Aggregate)
		if !ok {
			continue
		}
		idx := 0
		rewrite := func(refs []Ref) []Ref {
			for i := range refs {
				plan := planned[id][idx]
				idx++
				if plan.Producer == "" || plan == refs[i] {
					continue
				}
				refs[i] = plan
				changed = true
			}
			return refs
		}
		agg.Refs = rewrite(agg.Refs)
		for i := range agg.Groups {
			agg.Groups[i].Refs = rewrite(agg.Groups[i].Refs)
		}
	}
	return changed
}

// zeroTruthOf 沿 identity relay 的唯一输入链回溯，返回链尾的 (producer, port)。
//
// 只有当链**终结于一个可证恒为零值的载体**时才返回 true：整条链的每个 relay
// 都是恒等复制，链尾是零值 ⇒ 链上每一点的值都是零 ⇒ 候选重定向到链尾保真。
// 链尾是非零节点（或链不完整 / 有环）⇒ 不重定向（保守一侧；relay 的位置是
// 语义承载的）。
func zeroTruthOf(nodes map[string]Node, ref Ref) (Ref, bool) {
	cur := ref
	for depth := 0; depth < 64; depth++ {
		n := nodes[cur.Producer]
		if n == nil {
			return Ref{}, false
		}
		if isZeroCarrierNode(n) {
			return cur, true
		}
		src, ok := identityRelaySource(n)
		if !ok {
			return Ref{}, false
		}
		if src.BlockID == "" {
			return Ref{}, false
		}
		cur = Ref{Producer: src.BlockID, Output: src.Name}
	}
	return Ref{}, false
}

// isZeroCarrierNode 报告节点是否「可证恒为零值」的纯载体：编译器自建的封闭
// 子集 shim（与 prune_dead_pure_nodes 的 pure 同一把尺）且带零值标注。
// 未知形态一律 false（fail-safe：不优化不是错误，误判才会改语义）。
func isZeroCarrierNode(n Node) bool {
	cr, ok := n.(*CodeRun)
	if !ok || cr.ZeroOf == nil {
		return false
	}
	// 「无输入」= 纯常量发射器：有输入引用的 shim 携载真实臂值（record 裹层
	// 等），不是零载体。这一条与 raising 侧 `deadZeroCarrierMergeRef` 逐字同尺
	// （platforms/cozebiz/decompile/generate.go「无输入（纯常量发射器）」）。
	if len(cr.Inputs) != 0 {
		return false
	}
	return cr.Synthetic && strings.HasPrefix(cr.Code, pyast.ShimMarker)
}

// refValueProvablyZero 报告「这个**候选取到的值**可证为零」——比
// `isZeroCarrierNode`（整节点零载体）宽一层：它还认**成员口**的零
// （`CodeRun.ZeroFields`，record 字面量里作者写的 `0` / `""` / `false` / 空容器）。
//
// 为什么要成员口这一层：平台 merge 是**逐字段**取候选（record 变量一个字段一个
// 组），故「本组结局里可能出现作者写的零值」这个门控判据必须看得见字段级的零。
// 一手反例（2026-09-28，p30/r3）：`RC r = {"a":k,"b":"x"}; if (q=="a") { if (k>3)
// { r = {"a":0,"b":"y"}; } else { } } else { } return r.a;` —— 手臂写的是**一条
// 整值非零的 record 字面量**，只有字段 `a` 是零；按节点粒度看不见它 ⇒ 臂外的
// 非零基座在「作者写零」那条路径上胜出 = 作者的零被静默丢掉（撞「失配 ≠ 静默
// 丢弃」不变量）。
//
// fail-safe 方向：非字面量（引用 / 表达式）成员口**不认**零 —— 不确定时按
// 「不是零」处理只会少一次门控（少一次等价变换），而按「是零」处理会把非零候选
// 当零 ⇒ 门控脱落 ⇒ 静默改值。
func refValueProvablyZero(n Node, port string) bool {
	if isZeroCarrierNode(n) {
		return true
	}
	cr, ok := n.(*CodeRun)
	if !ok || cr == nil || len(cr.ZeroFields) == 0 || port == "" {
		return false
	}
	last := port
	if i := strings.LastIndex(last, "."); i >= 0 {
		last = last[i+1:]
	}
	for _, f := range cr.ZeroFields {
		if f == last {
			return true
		}
	}
	return false
}

// canonicalIdentityRelayCode 是恒等转发 relay 的**规范** shim 文本（唯一生产者 =
// build_return.go 的 scalarBranchDefault / materializeBranchDefault，两者都写
// `wrapShim([arg1], NameExpr{arg1})`）。
func canonicalIdentityRelayCode() string {
	return wrapShim([]pyast.Param{{Name: "arg1"}}, &pyast.NameExpr{Name: "arg1"})
}

// IdentityRelayInvariantViolations 是**机器门禁**：返回「标了 Identity 但 Code
// 不是规范恒等 shim**或**输入数 ≠ 1」的节点 id。Identity 是本 pass 识别 relay 的结构标注，标注
// 打错 = 把非恒等节点当成恒等复制折叠 ⇒ 改语义。判据是文本逐字等式（规范化
// 空白后），不依赖任何人工记忆；`internal/ir` 的契约测试在真实编译产物上断言
// 该集合为空，并用手工篡改的节点验证门禁**不是瞎的**。
func IdentityRelayInvariantViolations(g *Graph) []string {
	var out []string
	canon := canonicalIdentityRelayCode()
	for id, n := range collectIRNodes(g) {
		cr, ok := n.(*CodeRun)
		if !ok || !cr.Identity {
			continue
		}
		// 判据两条：① Code 逐字等于规范恒等 shim；② **恰好一个输入**——恒等转发的
		// 定义就是 arg1 → result 的单输入转发，输入数 ≠ 1 的 Identity 标注是误标
		// （`identityRelaySource` 会拒绝折叠它 ⇒ 既不被折叠、又不被报出 = 静默盲点）。
		if cr.Code != canon || len(cr.Inputs) != 1 {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// identityRelaySource 报告节点是否是恒等转发 relay 并返回它的唯一输入。
// 结构判据（不是文本判据）：Identity 标注 + 恰好一个输入 + 单形参路径。
func identityRelaySource(n Node) (Source, bool) {
	cr, ok := n.(*CodeRun)
	if !ok || !cr.Identity || len(cr.Inputs) != 1 {
		return Source{}, false
	}
	in := cr.Inputs[0]
	if in.BlockID == "" || len(in.Path) != 1 {
		return Source{}, false
	}
	return in, true
}
