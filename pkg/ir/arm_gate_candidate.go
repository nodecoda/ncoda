package ir

import (
	"fmt"
	"strings"

	"github.com/nodecoda/ncoda/pkg/pyast"
)

// arm_gate_candidate.go — φ 候选的**臂门控**（lowering 忠实性的前置条件）。
//
// 规则（一条，宿主/臂数/形态无关）：
//
//	一个 junction φ（if / switch / intent / ask / attempt 的臂内赋值，以及流程
//	出口汇聚）的每个候选，其产出方必须**只在该候选所属的臂被执行时才存在**；
//	不满足时**在该臂内物化一个恒等载体**（`# ncoda:shim` 单入单出，Identity），
//	候选改指该载体。
//
// 为什么（平台一手证据 + 反例）：
//
//	平台 `variable_merge` 只有一种策略：**按位置取第一个「存在且非 nil」的候选**
//	（coze-studio OSS `variableaggregator/variable_aggregator.go:206-214`，判据
//	`value != nil`）。「臂」根本不进取值判断 —— 臂之所以生效，**只因未走臂里的
//	节点不执行 ⇒ 它的候选不出现在输入里**。故候选的产出方若在 junction **之前**
//	（臂外），则任何臂被选中时它都存在 ⇒ **恒取位置 0** ⇒ 条件对取值没有作用。
//
//	一手反例（本树实测，全部编译 SUCCEEDED 且门禁全绿）：
//	  `function main(string audio, string fallback)…`
//	  `if (!empty(audio)) { sel = audio } else { sel = fallback }`
//	  产物: condition --true/false--> merge，候选 = [start.audio, start.fallback]
//	  ⇒ 运行期恒取 audio，条件被完全忽略（它今天「看起来对」只因该条件恰好是
//	  对所选值的零值测试）。同样失效的还有：`if (n==2) { v = b } else { v = a }`
//	  （a/b 在分支之前算好）、`string v = "x"; if (n==2) { } else { v = "B" }`
//	  （未写臂直引分支前的非零绑定）、多出口 `return <臂外值>`。
//
//	同一条判据早就在本仓的另一个位置成文（见 build_return.go 的
//	materializeBranchDefault 注释：「An upstream default node is always reachable,
//	so referencing it directly from variable_merge would win before an assignment
//	on the other branch; the copy must execute only on this path.」）——本文件只是
//	把同一条判据**也**施加到「臂真的写了、但写的是臂外 producer」与「未写臂继承
//	分支前绑定」这两个此前被直引绕过的入口上。
//
// 零值豁免（既有政策，非本文件新增）：值为**可证类型零**的候选不门控 ——
// 零值载体（`ZeroOf != nil` 的纯常量 shim，`isZeroCarrierNode` 同尺）在
// `FoldZeroIdentityCandidates` 里被重定向到零值真相，且由 merge 的「候选缺省 =
// 类型零」承担（P04：不为「没写臂」物化零值载体）。

// armSubtreeNodeIDs collects every node id that executes only inside this arm's
// subtree (the arm's statements, nested containers and their bodies).
//
// 完整性要求（漏登记一个节点 = 把活候选判成臂外 ⇒ 多物化一个冗余载体；
// 反向漏判 = 门控缺失 ⇒ 静默错值）。故遍历**按语句容器类型穷举**，与
// ast.Walk 在 AST 层的作用一致：新增 Stmt 实现时本函数必须同步（由
// TestArmSubtreeNodeIDsCoversEveryStmtKind 机器门禁守住）。
func armSubtreeNodeIDs(b *Block) map[string]bool {
	ids := map[string]bool{}
	collectArmSubtree(b, ids)
	return ids
}

func collectArmSubtree(b *Block, ids map[string]bool) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		if st == nil {
			continue
		}
		if id := st.stmtID(); id != "" {
			ids[id] = true
		}
		switch s := st.(type) {
		case *NodeStmt:
			if s.Node != nil {
				ids[s.Node.NodeID()] = true
			}
		case *IfStmt:
			if s.Cond != nil {
				ids[s.Cond.NodeID()] = true
			}
			if s.Relay != nil {
				ids[s.Relay.NodeID()] = true
			}
			if s.Merge != nil {
				ids[s.Merge.NodeID()] = true
			}
			collectArmSubtree(s.Then, ids)
			collectArmSubtree(s.Else, ids)
		case *AttemptStmt:
			if s.Op != nil {
				ids[s.Op.NodeID()] = true
			}
			if s.Exit != nil {
				ids[s.Exit.NodeID()] = true
			}
			if s.SuccessEnd != nil {
				ids[s.SuccessEnd.NodeID()] = true
			}
			if s.FailureEnd != nil {
				ids[s.FailureEnd.NodeID()] = true
			}
			collectArmSubtree(s.Success, ids)
			collectArmSubtree(s.Failure, ids)
		case *LoopStmt:
			if s.Container != nil {
				ids[s.Container.NodeID()] = true
			}
			collectArmSubtree(s.Body, ids)
		case *ParallelStmt:
			for _, br := range s.Branches {
				collectArmSubtree(br, ids)
			}
		case *QaStmt:
			if s.QA != nil {
				ids[s.QA.NodeID()] = true
			}
			if s.Merge != nil {
				ids[s.Merge.NodeID()] = true
			}
			for _, br := range s.Branches {
				collectArmSubtree(br.Body, ids)
			}
		case *SwitchStmt:
			if s.Switch != nil {
				ids[s.Switch.NodeID()] = true
			}
			if s.Merge != nil {
				ids[s.Merge.NodeID()] = true
			}
			for _, br := range s.Branches {
				collectArmSubtree(br.Body, ids)
			}
		case *ReturnStmt:
			if s.End != nil {
				ids[s.End.NodeID()] = true
			}
		case *BreakStmt:
			if s.Node != nil {
				ids[s.Node.NodeID()] = true
			}
		case *ContinueStmt:
			if s.Node != nil {
				ids[s.Node.NodeID()] = true
			}
		case *InlineStmt:
			collectArmSubtree(s.Body, ids)
		}
	}
}

// gateArmCandidate enforces the arm-gating invariant on one φ candidate.
//
// src  = 候选的来源值（臂内真写值 / 未写臂继承的分支前绑定）
// arm  = 该候选所属臂的块（载体物化点）
// typ  = 候选值的类型；nil ⇒ 取声明端口类型（portTypeOf），仍取不到则不物化
//
// rec  = 本候选所属的**记录值**（字段组候选：该候选是 rec 的一个字段投影）；
//
//	nil = 标量候选（或非记录绑定）⇒ 恒等载体粒度不变。
//
// 返回：src 本身（已在臂内 / 可证零值），或臂内新建的载体（标量 ⇒ 恒等载体；
// 记录字段 ⇒ 整值记录载体，见 recordRelayInArm）。
// 物化失败（arm == nil / 无类型权威）时返回 src —— 保持既有行为，不静默改判据。
func (g *Builder) gateArmCandidate(arm *Block, src Ref, typ *Type, rec *Type) Ref {
	if arm == nil || src.Producer == "" {
		return src
	}
	inArm := g.armGateIDs(arm)
	if typ == nil {
		typ = g.portTypeOf(src.Producer, src.Output)
	}
	if inArm[src.Producer] {
		return src // 产出方在臂内：存在性已被臂门控
	}
	if isZeroCarrierNode(g.findAny(src.Producer)) {
		return src // 可证类型零：由 merge「候选缺省 = 类型零」承担（P04）
	}
	if _, ok := identityRelaySource(g.findAny(src.Producer)); ok {
		return src // 已是恒等载体：重复物化 = 无谓一跳
	}
	// rec ≠ nil ⇒ 本候选是**记录值的一个字段组**的候选（`planArmJunctionPhi` 逐字段
	// 投影的产物）：门控粒度必须是**整值记录**，不是「每字段各留一份标量复制」。
	// 逐字段复制会让 merge 各字段组的同位候选生产者互不相同 ⇒ 反编译侧认不出这是
	// 一条记录（`recordUnitMembers` 要求各组件生产者同序）⇒ 记录单元折不回、臂交付
	// 的整值记录读不回（一手 R-13：`d = r.data;` 出口值整条消失，7 → 3 节点）。
	// 只有**字段组**候选走这条；标量变量的候选即使其值恰是成员路径（R-16 夹具：
	// `string x; x = v1.data.url;`）也保持既有形状 —— 门控粒度=f(绑定形状)，
	// 不是 f(引用拼写)。
	if rec != nil {
		recordField := false
		if base, member, ok2 := splitMemberPort(src.Output); ok2 &&
			g.portTypeOf(src.Producer, base) == rec {
			recordField = true
			if id, ok3 := g.recordRelayInArm(arm, src.Producer, base, rec); ok3 {
				return Ref{Producer: id, Output: member}
			}
		}
		// 扁平成员口拼写（`<node>.f1`，无点）：平台导出把记录口展开成**逐字段口**
		// 时，候选引用的就是成员口本身。记录值的门控粒度必须是**整值记录**
		// （一条记录载体，R-13）—— 逐字段标量载体是同一语义的第二种产物，且反编译
		// 认不回（各字段组候选生产者不同序 ⇒ 记录单元折不回 ⇒ 值静默丢失）。
		if _, isMember := rec.Props[src.Output]; isMember {
			recordField = true
			if id, ok3 := g.recordRelayInArm(arm, src.Producer, "", rec); ok3 {
				return Ref{Producer: id, Output: src.Output}
			}
		}
		if recordField {
			// 记录载体物化失败（缺字段类型权威等）⇒ 返回 src（= 既有「物化失败保持
			// 行为」政策），**不得**回退「逐字段标量恒等载体」：那条拼写（`result` 口
			// 承载成员值）是 2026-09-28 退役的**同一语义第二种产物**，反编译侧已不再
			// 认它（正本 = `platforms/cozebiz/decompile/record_unit.go` 判据①）。
			return src
		}
	}
	relay, ok := g.identityRelayInArm(arm, src, typ)
	if !ok {
		return src
	}
	return relay
}

// currentArmBlock = 正在展开的**最内层 junction 臂**块（if/switch/QA/attempt 的
// 臂），不在任何臂内时 nil。与 armStarts 同进同出（emitBranch 一处压栈）：
// 水量线回答「是不是本臂产出的」，本函数回答「物化到哪里」。
func (g *Builder) currentArmBlock() *Block {
	if len(g.armBlocks) == 0 {
		return nil
	}
	return g.armBlocks[len(g.armBlocks)-1]
}

// gatedArmVarRef 把「臂尾值」门控到臂内：值产在臂外 ⇒ 返回指向**臂内恒等载体**
// 的新 varRef（其余情形原样返回同一 varRef）。
//
// 用途 = junction 的臂尾值通道（if/switch/QA/attempt 的 armValues）。通道与
// 「臂内赋值」通道（armCandidates / scalarPhiGroups）遵守**同一条**规则：
// 候选存在 ⟺ 本臂被执行。两条通道都要门控，否则就是按「值怎么流到 junction」
// 分叉的形态枚举（同一语义两种形状）。
func (g *Builder) gatedArmVarRef(arm *Block, v *varRef) *varRef {
	if v == nil {
		return nil
	}
	ref := g.gateArmCandidate(arm, Ref{Producer: v.nodeID, Output: v.output}, nil, nil)
	if ref.Producer == v.nodeID && ref.Output == v.output {
		return v
	}
	out := *v
	out.nodeID, out.output = ref.Producer, ref.Output
	return &out
}

// identityRelayInArm materializes a branch-local identity relay carrying src's
// value and prepends it to the arm block, so the value exists only when this
// arm executes. Shape is identical to scalarBranchDefault /
// materializeBranchDefault (same canonical shim text, same `result` port,
// Identity annotation) — one construct, one spelling.
func (g *Builder) identityRelayInArm(arm *Block, src Ref, typ *Type) (Ref, bool) {
	if arm == nil || src.Producer == "" {
		return Ref{}, false
	}
	if typ == nil {
		return Ref{}, false // 无类型权威：不物化（不虚构类型）
	}
	id := g.nextID()
	node := &CodeRun{
		ID:        id,
		Language:  "python",
		Types:     map[string]*Type{},
		OutTypes:  map[string]*Type{"result": typ},
		OutOrder:  []string{"result"},
		Synthetic: true,
		Identity:  true, // 恒等复制：result ≡ 唯一输入
	}
	node.Code = canonicalIdentityRelayCode()
	node.Inputs = []Source{{Path: []string{"arg1"}, BlockID: src.Producer, Name: src.Output}}
	node.Types["arg1"] = typ
	g.nodes = append(g.nodes, node)
	g.remember(node)
	r := varRef{nodeID: id, output: "result", kind: "code"}
	arm.Stmts = append([]Stmt{&NodeStmt{Node: node, Value: &r}}, arm.Stmts...)
	return Ref{Producer: id, Output: "result"}, true
}

// armGateIDs memoizes armSubtreeNodeIDs per arm block: the same arm is queried
// once per (variable, field) candidate pair.
func (g *Builder) armGateIDs(arm *Block) map[string]bool {
	if arm == nil {
		return nil
	}
	if g.armGateMemo == nil {
		g.armGateMemo = map[*Block]map[string]bool{}
	}
	if ids, ok := g.armGateMemo[arm]; ok {
		return ids
	}
	ids := armSubtreeNodeIDs(arm)
	g.armGateMemo[arm] = ids
	return ids
}

// armCandidate = 一个尚未施加臂门控的候选（= 一条臂为该组贡献的取值点）。
type armCandidate struct {
	arm *Block // 该候选所属的臂（载体物化点）；nil = 顶层 fall-through 出口
	ref Ref    // 候选引用（臂内写值 / 未写臂继承的分支前绑定）
	typ *Type  // 候选类型；nil ⇒ 由 portTypeOf 兜（仍取不到则不物化）
	rec *Type  // 该候选所属的记录值类型（字段组候选）；nil = 标量候选
}

// armGateCandidates 给一组**按臂序**排列的候选施加臂门控。**默认位（最后一条）
// 只在「本单元没有作者写的零值」时豁免**。
//
// 为什么非默认位一律门控（平台一手语义，与 materializeFlowExitPhi /
// exitRecordFieldGroups 同一个判据 —— 本函数只是把它推广到全部 φ 通道）：
// `variable_merge` 按**位置**取第一个「存在且非 nil」的候选，而「臂」只在「未走臂
// 里的节点不执行 ⇒ 它的候选不存在」这个意义上生效。c_i（i < n-1）只有在「排在它
// 前面的候选全都不存在」时才可能胜出，而那是「本臂被执行、更早的臂都没被执行」
// ——故它必须**只在本臂执行时才存在**：产出方在臂外（分支前算好的值 / 入口参数）
// ⇒ 必须门控（物化臂内恒等载体）。
//
// 为什么默认位的豁免**以「本单元没有作者写的零值」为条件**（2026-09-28 收口，
// R-125 同族；判据 = 一手反例）：
// 豁免的论证是「走到默认位 ⇒ 前面的臂都没产出候选 ⇒ 取该臂自己的值」，而
// **「没产出候选」与「产出零值」在平台 merge 上同一结果**（first_non_zero：零候选
// 被跳过 ⇒ 继续往后取）。于是默认位若由**臂外**产出方充任（它在每条路径上都存在
// ⇒ 恒被选中），前面那条臂写的零值就被它吞掉。一手反例（`p30/s7`）：
//
//	int v = k; if (q == "a") { v = 0; } return v;   // 作者要 0 的那条路径拿到 k
//
// 首编：merge = [零载体（移到条件前） , 入口参数 k] ⇒ 两个候选都恒存在 ⇒ 全量取
// 第一个非零 ⇒ 恒取 k；反编译把这形态读成「空 if + 若参数包装成 record」⇒ r1 ≠ r2
// （作者的 `v = 0;` 在文本里**整条消失**）。把基座门控进未写臂后：走零写路径时
// 默认位候选不存在（未执行）⇒ 全候选零/缺省 ⇒ 结局 = 零 = 作者写的值。
// 这与 J §8 U12「**无零写时**臂外基座停在无条件位正是规范位」是同一条尺的两半：
// 有零写 ⇒ 必须按路径门控；没有 ⇒ 无条件位就是规范位（不外加载体）。
// authoredZero = 本**单元**有没有作者写的零值（记录变量按整条记录取并集，见
// planArmJunctionPhi 的 record 支）；由调用方算好传入 ⇒ 门控对整条记录同解。
func (g *Builder) armGateCandidates(cands []armCandidate, authoredZero bool) []Ref {
	out := make([]Ref, 0, len(cands))
	defaultExempt := !authoredZero
	for i, c := range cands {
		if i == len(cands)-1 && defaultExempt {
			out = append(out, c.ref) // 默认位：本单元无作者零值 ⇒ 臂外产出方是合法默认值
			continue
		}
		out = append(out, g.gateArmCandidate(c.arm, c.ref, c.typ, c.rec))
	}
	return out
}

// armUnitHasAuthoredZero 报告本单元的候选里是否出现**作者写的类型零值**
// （取值可证为零：整节点零载体 ⊕ 字面量成员口零，见 `refValueProvablyZero`）。
//
// 判据与 `groupHasAuthoredZeroDeep`（sink_base_carriers.go，基座下沉/铸副本的门控
// 条件）**同一把尺**：那里问「这条汇聚组要不要为作者写的零让路」，这里问「默认位
// 能不能继续豁免臂外产出方」——两个问题的答案都由「本单元有没有可证零值」决定。
func (g *Builder) armUnitHasAuthoredZero(cands []armCandidate, portName string) bool {
	if len(cands) == 0 {
		return false
	}
	refs := make([]Ref, 0, len(cands))
	for _, c := range cands {
		refs = append(refs, c.ref)
	}
	// 复用 `groupHasAuthoredZeroDeep`：它多一层**嵌套汇聚递归**（候选生产者本身是
	// 交汇点时按其组名下钻）—— 一手 p30/r3：外层交汇点的候选是内层汇聚节点的口，
	// 作者写的零藏在**内层**组的候选里；只看一层会漏 ⇒ 外层默认位不门控 ⇒ 组间
	// 候选生产者序列不一致 ⇒ 记录单元被拆散。
	return groupHasAuthoredZeroDeep(&AggregateGroup{Name: portName, Refs: refs}, g.findAny, map[string]bool{})
}

// armTailValue = 臂尾值通道的一个候选（switch / QA / attempt 的整值汇聚）。
type armTailValue struct {
	arm *Block
	v   varRef
}

// armTailValues 给臂尾值通道套用与「臂内赋值」通道**同一条**臂门控规则
// （见 armGateCandidates，候选的默认位豁免同样以「本单元没有作者写的零值」为条件）：
// 臂尾值若产在臂外，则在该臂内物化恒等载体；**默认位**（最后一条）只在本单元没有
// 可证零值时才保持既有形状。kind 逐字保留（它是声明口复原的依据）。
//
// 为什么默认位也要看「有没有作者写的零值」（2026-09-28，一手反例）：默认位豁免的
// 论证是「走到默认位 ⇒ 本臂的值就是结局」，而平台让「没产出候选」与「产出零值」
// 同结果（first_non_zero：零候选被跳过、继续往后取）⇒ 默认位若是**臂外**产出方，
// 它在每条路径上都存在、**恒被取中** ⇒ 另一条臂的作者零值被吞掉。实测（值位置三元）：
//
//	string v = "z"; string x = (q == "a") ? "" : v; return x;
//	⇒ merge x = ["", "z"]，两个候选都是臂外常量、全部恒存在 ⇒ 两条路径都返回 "z"
//	   （作者要 "")，且零诊断。把默认位（`v` 那条臂）门控进臂内后：
//	   `q=="a"` 路径上全候选零/缺省 ⇒ 结局 = "" = 作者写的值。
//
// 这与 armGateCandidates 的默认位判据是**同一条**（`armUnitHasAuthoredZero` 的
// 尾值版 `armTailsHaveAuthoredZero`）：一处定义，两个通道共用 —— 两个通道各写一份
// 默认位政策正是这条缝第一次裂开的地方（2026-09-28 实测：赋值通道已带条件，尾值
// 通道仍无条件豁免）。
func (g *Builder) armTailValues(cands []armTailValue) []varRef {
	authoredZero := g.armTailsHaveAuthoredZero(cands)
	out := make([]varRef, 0, len(cands))
	for i, c := range cands {
		if i == len(cands)-1 && !authoredZero {
			out = append(out, c.v)
			continue
		}
		out = append(out, *g.gatedArmVarRef(c.arm, &c.v))
	}
	return out
}

// armTailsHaveAuthoredZero 是 `armUnitHasAuthoredZero` 的**臂尾值通道版**：同一把尺
// （`groupHasAuthoredZeroDeep`：整节点零载体 ⊕ 字面量成员口零，含嵌套汇聚递归），
// 只是候选来自 `armTailValue` 而不是 `armCandidate`。
func (g *Builder) armTailsHaveAuthoredZero(cands []armTailValue) bool {
	if len(cands) == 0 {
		return false
	}
	refs := make([]Ref, 0, len(cands))
	for _, c := range cands {
		refs = append(refs, Ref{Producer: c.v.nodeID, Output: c.v.output})
	}
	return groupHasAuthoredZeroDeep(&AggregateGroup{Refs: refs}, g.findAny, map[string]bool{})
}

// splitMemberPort 把「容器口 + 成员」的引用切成 (base, member)：
// `data.url` → ("data", "url")；`structured_output.value.id` →
// ("structured_output.value", "id")。无点 / 空 base / 空 member ⇒ ok=false
// （裸口名 = 标量口，不是成员引用）。
func splitMemberPort(output string) (base, member string, ok bool) {
	i := strings.LastIndex(output, ".")
	if i <= 0 || i == len(output)-1 {
		return "", "", false
	}
	return output[:i], output[i+1:], true
}

// recordArmRelayKey = 「臂内整值记录载体」的同一性：同一臂里承载**同一个记录的
// 同一份复制**的载体只物化一次 —— 多字段共享一条载体是「记录单元」成立的前提
// （merge 各字段组的**同位候选生产者序列**必须一致，见 record_unit.go）。
type recordArmRelayKey struct {
	arm      *Block
	producer string
	port     string
}

// recordRelayInArm 在臂内物化**一条记录载体**，承载 producer 的 `base` 口所指的
// 整值记录：形状与记录字面量的 shim 同（Synthetic、非 Identity、单 `result`
// 容器口 ⇒ 渲染层把成员展开成扁平口），输入 = 源记录各成员的投影
// （`<base>.<field>`）。返回载体节点 id。已物化过（同 arm/producer/port）⇒ 复用。
//
// 为什么记录**引用**值必须走「一条记录载体」而不是「每字段一条标量恒等载体」
// （一手 R-13，`d = r.data;`）：
//
//	逐字段载体 ⇒ merge 各字段组的候选生产者互不相同 ⇒ 反编译侧
//	`recordUnitMembers` 的「各组件同位候选生产者同序」不成立 ⇒ 记录单元折不回 ⇒
//	臂交付的整值记录读不回、出口值整条消失（实测 7 → 3 节点、零诊断）。
//	记录**字面量**（`d = { … };`）因为本来就物化成一条记录 shim 而没有这个问题。
//	本函数把引用型记录值归一到同一形状：同一语义不得有两种产物。
//
// 与 gateArmCandidate 的关系：门控的仍是「候选只在本臂执行时存在」，只是把
// **粒度**从「每个成员各留一份复制」改正为「整值留一份复制」（形状 = f(值类型)，
// 与 junctionValueFieldGroups / newValueAggregate 同一条纪律）。
//
// 证据不足（无记录类型 / 某成员无类型权威）⇒ 不物化（返回 false，调用方退回
// 既有逐字段标量载体）：不虚构类型、不改变既有形状。
func (g *Builder) recordRelayInArm(arm *Block, producer, base string, rec *Type) (string, bool) {
	if arm == nil || producer == "" || rec == nil || len(rec.Props) == 0 {
		return "", false
	}
	key := recordArmRelayKey{arm: arm, producer: producer, port: base}
	if id, ok := g.recordArmRelayMemo[key]; ok {
		return id, true
	}
	names := []string{}
	sources := []Source{}
	types := map[string]*Type{}
	props := map[string]*Type{}
	items := make([]pyast.DictItem, 0, len(rec.Props))
	for i, field := range sortedTypeFields(rec) {
		ft := rec.Props[field]
		if ft == nil {
			return "", false
		}
		arg := fmt.Sprintf("arg%d", i+1)
		names = append(names, arg)
		// 口拼写 = 基座的**声明口模型**：容器口下的成员拼成 `<base>.<field>`；
		// 扁平成员口（base == ""）的口名**就是**字段名。
		inputName := field
		if base != "" {
			inputName = base + "." + field
		}
		sources = append(sources, Source{Path: []string{arg}, BlockID: producer, Name: inputName})
		types[arg] = ft
		props[field] = ft
		items = append(items, pyast.DictItem{Key: &pyast.StringExpr{Value: field}, Value: &pyast.NameExpr{Name: arg}})
	}
	mod := &pyast.Module{
		Params: namedParams(names),
		Body:   []pyast.Stmt{&pyast.ReturnStmt{Value: &pyast.DictExpr{Items: items}}},
	}
	node := &CodeRun{
		ID:        g.nextID(),
		Code:      ShimMarker + strings.TrimSuffix(pyast.Serialize(mod), "\n"),
		Language:  "python",
		Inputs:    sources,
		Types:     types,
		OutTypes:  map[string]*Type{"result": TObject(props)},
		OutOrder:  []string{"result"},
		Synthetic: true,
	}
	g.nodes = append(g.nodes, node)
	g.remember(node)
	r := varRef{nodeID: node.ID, output: "result", kind: "code"}
	arm.Stmts = append([]Stmt{&NodeStmt{Node: node, Value: &r}}, arm.Stmts...)
	if g.recordArmRelayMemo == nil {
		g.recordArmRelayMemo = map[recordArmRelayKey]string{}
	}
	g.recordArmRelayMemo[key] = node.ID
	return node.ID, true
}

// recordPortOf 返回绑定值所指的**记录型**声明口类型（声明口带字段才算记录）；
// 其余（标量口 / 无类型权威 / 非记录）⇒ nil。判据 = 声明类型，不是引用拼写。
func (g *Builder) recordPortOf(bound varRef) *Type {
	t := g.portTypeOf(bound.nodeID, bound.output)
	if t == nil || len(t.Props) == 0 {
		return nil
	}
	return t
}
