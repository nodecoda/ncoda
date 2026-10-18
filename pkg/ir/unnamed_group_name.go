package ir

import (
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
)

// 未命名汇聚单元的**身份分配**（R-85）。
//
// 一条汇聚单元在 wire 上的身份 = 它的组名（`Aggregate.RefName` / 无身份时的
// 平台无名组拼写）。coze-biz 的 wire **没有**别的区分手段：`variable_merge`
// 的组名就是反编译读回来时的变量名（A13-H2：名字是单元身份的唯一载体）。
// 因此「同一个 wire 里两个单元同名」= 反编译无法把它们读成两个绑定 ⇒ 必逃逸
// 一个（`agg_v1_g0`）⇒ 再编译产物与首轮不同（`I1b · ARTIFACT_DRIFT/PARAM`）。
//
// 落下的规则只有一条：**无身份单元取平台自己生成器的拼写 `Group<k>`，k =
// 本流程内最小可用序号**（平台就是这么做的：`Group` + 最小可用序号）。既有
// 语料护栏因此不动 —— 第一个无名单元仍是 `Group1`。
//
// 「可用」不是「当前分配器没用过」：作者自己写的 `Group<k>`（变量名、参数名、
// 记录字段名……任何标识符）也在用这个名字。作者名**优先**（不夺名），分配器
// 跳过它 —— 与平台一致：作者的工作流先存在，生成节点后追加。
const unnamedGroupPrefix = "Group"

// authoredGroupSpellings 收集作者在源码里写过的、形如 `Group<数字>` 的
// **全部标识符**（声明位与引用位都算）。
//
// 为什么要扫全树而不是只扫声明：组名的来源不止变量声明 —— 记录字段名会成为
// 逐字段组名、参数名会成为 start 的口名、code 输出字段名会成为 code 节点的
// 口名。**任何**标识符都可能以口名/组名的形式落到 wire 上，所以保留集就是
// 「作者写过的同名标识符全集」。多收不危险（只会让分配器跳过更多编号），
// 漏收才会重新造出重名。
func authoredGroupSpellings(p *ast.Program) map[string]bool {
	out := map[string]bool{}
	if p == nil {
		return out
	}
	ast.Walk(groupSpellingCollector{out: out}, p)
	return out
}

type groupSpellingCollector struct{ out map[string]bool }

func (c groupSpellingCollector) Visit(n ast.Node) ast.Visitor {
	switch n := n.(type) {
	case *ast.VarDeclStmt:
		c.add(n.Name())
	case *ast.ParamDecl:
		c.add(n.Name())
	case *ast.FieldDecl:
		c.add(n.Name())
	case *ast.CodeOutputField:
		c.add(n.Name())
	case *ast.EnumMemberDecl:
		c.add(n.Name())
	case *ast.ConversationVarDecl:
		c.add(n.Name())
	case *ast.ConstDecl:
		c.add(n.Name())
	case *ast.FunctionDef:
		c.add(n.Name())
	case *ast.CodeFunctionDef:
		c.add(n.Name())
	case *ast.TypeDecl:
		c.add(n.Name())
	case *ast.EnumDecl:
		c.add(n.Name())
	case *ast.IdentExpr:
		c.add(n.Name())
	}
	return c
}

func (c groupSpellingCollector) add(name string) {
	if isGeneratedGroupSpelling(name) {
		c.out[name] = true
	}
}

// isGeneratedGroupSpelling reports whether name is exactly `Group` followed by
// a run of digits — the platform's generated-group spelling. Nothing else is a
// group name we could collide with (a generated group name is `Group<k>`;
// `agg_v1_g0` and field names are handled by the author-identifier rule).
func isGeneratedGroupSpelling(name string) bool {
	if !strings.HasPrefix(name, unnamedGroupPrefix) {
		return false
	}
	digits := name[len(unnamedGroupPrefix):]
	if digits == "" {
		return false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return false
		}
	}
	return true
}

// allocateUnnamedGroupName returns the smallest `Group<k>` (k ≥ 1) this program
// does not already use, and reserves it. Linear scan over the reserved set:
// the set is tiny (only authored `Group<digits>` identifiers + our own
// allocations), so this is O(units²) at worst and never a hot path.
func (g *Builder) allocateUnnamedGroupName() string {
	for k := 1; ; k++ {
		name := unnamedGroupPrefix + strconv.Itoa(k)
		if !g.claimedGroupNames[name] {
			g.claimedGroupNames[name] = true
			return name
		}
	}
}

// flowExitPreferredGroupName：平台真实导出里「流程出口汇聚单元」的拼写就是 `Group1`
// （`end.node_inputs = [{path: Group1, ref_node: merge}]`，语料实证）⇒ 出口单元
// **优先**取这一号，别的无身份单元让位。作者写过 `Group1` 时不夺名（回落最小可用号）。
func flowExitPreferredGroupName() string { return unnamedGroupPrefix + "1" }

// nameUnnamedConvergenceUnit gives a legacy single-group convergence unit its
// wire identity when it has none (author never named it: an expression-position
// junction, the flow-exit phi, a loop-return unit). Units the author named —
// including the value-position slot name (`v = c ? A : B;`) — are untouched:
// the author's name wins, we only fill the空缺。
//
// 调用点 = 每条「legacy 单组单元物化完毕」的出口（汇聚宿主 / 流程出口 phi /
// 隐式 else 臂 / 循环回值）。漏一处 ⇒ 两个单元同名 ⇒ 一轮不动点破裂；
// `assertUnnamedUnitsAreNamed` 在 Build 末尾把这种漏法变成硬报错，而不是静默漂移。
func (g *Builder) nameUnnamedConvergenceUnit(agg *Aggregate) {
	if agg == nil || agg.RefName != "" || !agg.LegacySingleGroupUnit() {
		return
	}
	agg.RefName = g.allocateUnnamedGroupName()
	// 记账（创建序）：出口单元的号要在**上线之后**重排（见 canonicalizeFlowExitGroupName）。
	g.unnamedUnits = append(g.unnamedUnits, agg)
}

// canonicalizeFlowExitGroupName —— 「编号按**上线的**单元在创建序上分配」。
//
// 出口汇聚单元在 build 末位才物化，而**先物化的语句级单元**可能已经占掉 `Group1`
// —— 实测那还是个随后被 IR 优化剪掉的**死脚手架**（`if c { return A; } else
// { return B; }` 的臂汇聚：两臂都终止 ⇒ 它没有消费者 ⇒ 不进产物）。死单元占的号
// 在产物里**没有任何承载**，白占；于是可见的出口汇聚退成 `Group2`，而平台自己
// 的导出在出口汇聚上是 `Group1`（10/10 语料导出的出口 merge 都是 `Group1`）。
//
// 判据 = **占号者还在不在产物里**（`collectIRNodes`，与 `assertUnnamedUnitsAreNamed`
// 同一把尺）：占号者已被剪掉 ⇒ 号释放，出口单元取它；占号者还在 ⇒ 那个号是它的
// 身份，出口单元不夺（两个都要有名字）。作者自己写的 `Group1` 不在此列（作者名
// 优先，`claimedGroupNames` 早已挡住无名分配）。
func (g *Builder) canonicalizeFlowExitGroupName() {
	phi := g.flowExitPhiUnit
	if phi == nil || phi.RefName == "" || !phi.LegacySingleGroupUnit() {
		return
	}
	preferred := flowExitPreferredGroupName()
	if phi.RefName == preferred {
		return
	}
	var holder *Aggregate
	for _, u := range g.unnamedUnits {
		if u != nil && u != phi && u.RefName == preferred {
			holder = u
			break
		}
	}
	if holder == nil || g.unitSurvives(holder) {
		return
	}
	old := phi.RefName
	phi.RefName = preferred
	g.rewriteAggregatePortName(phi, old, preferred)
}

// unitSurvives reports whether the unit is still on the graph after IR optimization.
func (g *Builder) unitSurvives(agg *Aggregate) bool {
	for _, n := range collectIRNodes(g.graph) {
		if n == Node(agg) {
			return true
		}
	}
	return false
}

// rewriteAggregatePortName 把「读该单元 `old` 口」的引用改成 `new` 口。
//
// 这是**内部不变量**的手术：出口汇聚单元的口名只有一处承载（`End.Inputs`，
// 由 `endAggregatePortRefs` 在物化时写入），所以原地改这一处即可。改完立即用
// **既有那把尺**（`collectIRConsumers` 引用普查）复核 —— 还有引用指着旧口名 ⇒
// 说明别处也承载了这个名字（新宿主/新引用通道），**硬报错**而不是静默产出悬空引用。
func (g *Builder) rewriteAggregatePortName(agg *Aggregate, old, new string) {
	if agg == nil || old == new {
		return
	}
	for _, n := range collectIRNodes(g.graph) {
		end, ok := n.(*End)
		if !ok || end == nil {
			continue
		}
		fix := func(refs []Ref) {
			for i := range refs {
				if refs[i].Producer == agg.ID && refs[i].Output == old {
					refs[i].Output = new
				}
			}
		}
		fix(end.Inputs)
		for i := range end.Outputs {
			r := end.Outputs[i].Ref
			if r.Producer == agg.ID && r.Output == old {
				end.Outputs[i].Ref.Output = new
			}
		}
	}
	consumers := collectIRConsumers(g.graph, collectIRNodes(g.graph))
	for port, n := range consumers[agg.ID] {
		if n > 0 && port == old {
			panic("ir: convergence unit " + agg.ID + " port " + old +
				" is still referenced elsewhere; port rename must cover every carrier — see R-85")
		}
	}
}

// assertUnnamedUnitsAreNamed 是上一条的**完备性判据**：Build 结束时，任何幸存的
// legacy 单组单元都必须有身份。它是内部不变量（编译器自己的锅），不是语言错，
// 所以 panic 而不是诊断 —— 与 `optimize.go` 的死聚合不可摘除同一处置。
func (g *Builder) assertUnnamedUnitsAreNamed() {
	// 看**幸存的**节点（IR 优化摘掉的死聚合不算：它们不进产物，也就无所谓身份）。
	for _, n := range collectIRNodes(g.graph) {
		agg, ok := n.(*Aggregate)
		if !ok || agg == nil || agg.RefName != "" || !agg.LegacySingleGroupUnit() {
			continue
		}
		panic("ir: legacy single-group convergence unit " + agg.ID +
			" reached the graph without an identity (wire group name); " +
			"every unnamed convergence unit must take Group<k> — see R-85")
	}
}
