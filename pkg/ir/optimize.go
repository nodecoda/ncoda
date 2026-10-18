package ir

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// optimizeGraph is the platform-independent IR optimization phase (L4.5).
// It currently owns one recursive rule: an Aggregate is a value convergence
// node, so it is live only when some downstream data reference consumes one
// of its output ports.  An unconsumed Aggregate is dead scaffolding; deleting
// it never deletes the candidate producers themselves.
//
// 收敛不变量（A12-H8，2026-09-16）：**判死 ⇒ 必须摘得掉**。
// 本函数是「重算消费视图 → 判死 → 删除」的不动点循环，`changed` 只能由**真实
// 发生的变化**驱动。判死集合由 collectIRNodes 的**节点视图**给出，而删除由
// pruneDeadAggregates* 的**结构树遍历**执行 —— 两者必须覆盖同一个集合。视图里
// 有一类聚合**不挂在结构树的语句上**，而是挂在某个节点的字段上（图级载体，例如
// ReturnStmt.End 的 FlowExitMerge）：删除器漏掉这类载体时，该聚合每轮都被判死、
// 每轮都摘不掉 ⇒ `changed` 恒真 ⇒ **挂死**（实测形态：三条 record 出口 + 两个同
// id 的 end ⇒ collectIRNodes 按 id 取最后一个 ⇒ 流程出口 phi 的消费者看不见）。
// 所以删除之后必须**复核**：判死的聚合若仍出现在视图里，就是「删除器的覆盖面」
// 与「节点视图」不一致 —— 既不能继续留在循环里（挂死），也不能当它已经被删
// （那会丢掉真实的出口绑定）⇒ fail loud（本函数是内部不变量，不是用户诊断面，
// 故用 panic 而不是 error：调用方 build() 无法对「IR 内部视图自相矛盾」做任何
// 有意义的恢复，早死早超生）。
//
// 判据的覆盖面由 `optimize_deleter_coverage_test.go` 机器核对：**凡是节点视图
// 看得见的聚合槽位，判死之后都必须摘得掉**（逐槽位 + 逐语句类型的反射扫描，
// 新增一个 `*Aggregate` 字段而不管删除面 ⇒ 该门禁直接红）。
func optimizeGraph(g *Graph) {
	for changed := true; changed; {
		changed = false
		nodes := collectIRNodes(g)
		consumers := collectIRConsumers(g, nodes)
		dead := map[string]bool{}
		for id, node := range nodes {
			agg, ok := node.(*Aggregate)
			if !ok {
				continue
			}
			// Candidate-less junction removal (R7-a, 2026-09-11): an
			// Aggregate that carries no phi candidate at all (`Refs` and
			// `Groups` both empty) is not a value convergence point — it is
			// only the control-flow join of an if/switch/QA/attempt whose
			// arms produce nothing.  Nothing in ncoda needs it, and no real
			// platform wire contains one: in the 350-workflow corpus every
			// `variable_merge` declares at least one candidate (50
			// open-source exports + 214 commercial wires; zero-candidate
			// merges occur only in our own generated output, identified by
			// the synthetic `x = row*340, y = 0` positions).  Keeping it
			// also made `compile∘raise` non-idempotent: the junction is
			// materialised on the first round, raising ignores it, and the
			// second compile re-emits it — the wire never reaches a fixed
			// point (s08: w1 4 nodes, w2 5 nodes).
			//
			// Liveness = a real DATA consumer (every reference channel
			// counted by collectIRConsumers: node inputs, templates, loop
			// state/output sources, `end` bindings, selector operands and
			// the candidate refs of enclosing aggregates).  The junction's
			// structural host (IfStmt.Merge / SwitchStmt.Merge /
			// QaStmt.Merge / AttemptStmt.Exit) is NOT a consumer: both
			// renderers project a host whose junction is gone through their
			// merge-less path, carrying the arm tails and the explicit
			// branch ports into the continuation instead (P4 — a branch
			// port edge is never dropped, it only changes target).
			// Non-`Groups` aggregates — the legacy single-group channel
			// (`Refs`, wire group "Group1": the branch tail-value channel and the
			// hoisted-scalar phi) **and** the candidate-less junction — follow
			// exactly the liveness rule this file opens with: **live iff a real
			// data reference reads one of its output ports**.  The structural
			// host pointer (IfStmt.Merge / SwitchStmt.Merge / QaStmt.Merge /
			// AttemptStmt.Exit) is NOT a consumer: both renderers project a host
			// whose junction is gone through their merge-less path, carrying the
			// arm tails and the explicit branch ports into the continuation
			// instead (P4/P15 — a branch port edge is never dropped, it only
			// changes target).  The second family (empty arms, 2026-09-13)
			// proved that path end to end, including at container depth.
			//
			// The flow-exit phi (End.FlowExitMerge) needs **no** carve-out here:
			// it is referenced as data by `End.Inputs` (`build_return.go`), and
			// `case *End: addRefs(v.Inputs)` counts that reference, so it is
			// live by the same predicate.  Neither does an escape-point phi
			// whose only consumer is that flow-exit phi: `case *Aggregate:
			// addRefs(v.AllRefs())` counts the candidate reference.
			//
			// Why the exemption existed and why it is gone: "legacy `Refs`
			// aggregates also serve as structural branch joins" was a
			// conservative default from the first L4.5 rule (2026-09-09), from
			// the era before the four hosts had merge-less paths.  It left every
			// "written but never read" hoisted variable with an unconsumed
			// `variable_merge` in the product (first-hand baseline: 6 per round
			// on both B1 and B2), i.e. the very dead-phi scaffolding the file
			// header promises to delete — and it made `compile∘raise`
			// non-idempotent for that shape.
			if len(agg.Groups) == 0 {
				if !nodeConsumed(consumers, id) {
					dead[id] = true
				}
				continue
			}
			// Dead-scaffolding removal at OWNER granularity (family3/X4,
			// 2026-09-09): the convergence unit of one junction is the
			// ncoda hoisted VARIABLE (docs/ideas-todo/
			// 2026-09-08-merge-convergence-complete-algorithm.md §3/§4) —
			// scalar variables own one group (name == port), a record
			// variable owns one group per declared field.  A variable is
			// live when ANY of its groups on this junction has a downstream
			// data consumer; a variable with zero consumers is dead
			// scaffolding and every group it owns here is pruned.  Owner
			// granularity keeps a partially-consumed record's full declared
			// field set on the platform merge (dify variable-aggregator
			// field contract), while an entirely unconsumed carrier variable
			// (mirror group chained through nested junctions —
			// X4/X12/X20: dead carrier groups regrew names round over round
			// and flipped twin-assignment order once) is dropped whole.  The
			// fixpoint is bottom-up: dropping a dead owner's groups removes
			// its candidate refs, which can orphan an upstream mirror group
			// that was consumed only through this group.  Legacy groups
			// without an Owner fall back to per-port granularity.
			whole := wholeValueConsumer(consumers, id, agg.Groups)
			ownerLive := map[string]bool{}
			for _, grp := range agg.Groups {
				if whole || outputConsumed(consumers, id, grp.Name) {
					if grp.Owner != "" {
						ownerLive[grp.Owner] = true
					}
				}
			}
			kept := agg.Groups[:0]
			for _, grp := range agg.Groups {
				live := whole
				if !live && grp.Owner != "" {
					live = ownerLive[grp.Owner]
				} else if !live {
					live = outputConsumed(consumers, id, grp.Name)
				}
				if live {
					kept = append(kept, grp)
					continue
				}
				changed = true
			}
			agg.Groups = kept
			if len(kept) == 0 {
				dead[id] = true // every convergence unit died → whole node is scaffolding
			}
		}
		if len(dead) > 0 {
			pruneDeadAggregates(g.Root, dead)
			if linger := deadAggregatesStillVisible(g, dead); len(linger) > 0 {
				panic("ir optimize: dead aggregate is not removable from the node view " +
					"(the delete path does not cover its carrier, so the fixpoint would " +
					"never converge): " + strings.Join(linger, ", "))
			}
			changed = true
		}
		if !changed {
			break
		}
	}
}

// deadAggregatesStillVisible 复核「本轮判死的聚合是否真的从节点视图里消失」。
//
// 这是上面那条收敛不变量的**机器形式**：删除器（遍历结构树）与节点视图
// （collectIRNodes，按 id 收全部节点）不同源，覆盖率只能**事后核对**。核对
// 通过 ⇒ 每轮 `changed` 都对应一次真实删除 ⇒ 单调收敛；核对失败 ⇒ 调用方
// fail loud，不允许退化成挂死。
//
// 返回排序后的 `id(Type)` 列表（稳定输出，便于诊断比对）。
func deadAggregatesStillVisible(g *Graph, dead map[string]bool) []string {
	var out []string
	for id, node := range collectIRNodes(g) {
		if !dead[id] {
			continue
		}
		out = append(out, fmt.Sprintf("%s(%T)", id, node))
	}
	sort.Strings(out)
	return out
}

// nodeConsumed reports whether any downstream data reference reads any
// output port of producer id.
func nodeConsumed(consumers map[string]map[string]int, id string) bool {
	for _, n := range consumers[id] {
		if n > 0 {
			return true
		}
	}
	return false
}

// outputConsumed reports whether any downstream data reference reads the
// named output port of producer id. Port matching uses the first dotted
// segment: backend refs may carry member paths past the merge group name.
func outputConsumed(consumers map[string]map[string]int, id, port string) bool {
	ps, ok := consumers[id]
	if !ok {
		return false
	}
	return ps[port] > 0
}

// wholeValueConsumer reports whether any downstream reference consumes the
// aggregate as a whole value rather than one named group port.  Whole-value
// consumers bind the record/variable itself — loop output sources carry an
// empty producer port (yield of a whole record phi), and synthetic aggregate
// handles keep Output="" — so every group stays live: pruning a field group
// would silently drop data from the whole record export (loop-state family,
// ir_optimize loop-output locks).
func wholeValueConsumer(consumers map[string]map[string]int, id string, groups []AggregateGroup) bool {
	ps, ok := consumers[id]
	if !ok || len(ps) == 0 {
		return false
	}
	for port := range ps {
		if port == "" {
			return true
		}
		match := false
		for _, g := range groups {
			if g.Name == port {
				match = true
				break
			}
		}
		if !match {
			return true // a port this aggregate does not declare → whole-value handle
		}
	}
	return false
}

func isNilIRNode(n Node) bool {
	if n == nil {
		return true
	}
	value := reflect.ValueOf(n)
	return value.Kind() == reflect.Pointer && value.IsNil()
}

func collectIRNodes(g *Graph) map[string]Node {
	nodes := map[string]Node{}
	add := func(n Node) {
		if isNilIRNode(n) || n.NodeID() == "" {
			return
		}
		nodes[n.NodeID()] = n
	}
	if g.Entry != nil {
		add(g.Entry)
	}
	var stmt func(s Stmt)
	var block func(b *Block)
	stmt = func(s Stmt) {
		if s == nil {
			return
		}
		switch v := s.(type) {
		case *NodeStmt:
			add(v.Node)
		case *IfStmt:
			add(v.Cond)
			add(v.Merge)
			add(v.Relay)
			block(v.Then)
			block(v.Else)
		case *AttemptStmt:
			add(v.Op)
			add(v.SuccessEnd)
			add(v.FailureEnd)
			// The attempt's exit junction is an Aggregate like any other:
			// leaving it out of the node set made its own candidate refs
			// invisible to collectIRConsumers (so its producers looked
			// unconsumed and live groups could be pruned) and hid it from
			// the junction rules below.
			add(v.Exit)
			block(v.Success)
			block(v.Failure)
		case *InlineStmt:
			// An inlined body is an isolated return scope, not a separate
			// program: its junctions belong to this graph and must be
			// visited at this depth too (same rule, every nesting).
			// The frame's exit phi (InlineStmt.Exit) is a **graph-level**
			// producer attached to the statement — exactly like
			// End.FlowExitMerge: leaving it out of the node set makes its
			// candidate refs invisible to collectIRConsumers, so the arm
			// relays feeding it look unconsumed and are pruned.
			add(v.Exit)
			block(v.Body)
		case *LoopStmt:
			add(v.Container)
			block(v.Body)
		case *ParallelStmt:
			for _, branch := range v.Branches {
				block(branch)
			}
		case *QaStmt:
			add(v.QA)
			add(v.Merge)
			for _, branch := range v.Branches {
				block(branch.Body)
			}
		case *SwitchStmt:
			add(v.Switch)
			add(v.Merge)
			for _, branch := range v.Branches {
				block(branch.Body)
			}
		case *ReturnStmt:
			add(v.End)
			// The flow-exit phi (ir.End.FlowExitMerge) is a graph-level
			// producer attached to the terminal statement, exactly like
			// IfStmt.Merge / SwitchStmt.Merge / QaStmt.Merge /
			// AttemptStmt.Exit. Leaving it out of the node set made its
			// candidate refs invisible to collectIRConsumers, so a junction
			// whose only consumer is the flow-exit phi (the escape-point phi
			// of a nested branch: `end` reads merge(G1:[x, <junction>.v]))
			// looked unconsumed and lost its groups — the surviving ref then
			// dangled (`RS-NORM-003 data reference producer does not exist`).
			if v.End != nil {
				add(v.End.FlowExitMerge)
			}
		}
	}
	block = func(b *Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			stmt(s)
		}
	}
	block(g.Root)
	return nodes
}

// LegacySingleGroupNames reports, for every aggregate that renders through the
// **legacy single-group channel** (`Refs` non-empty, `Groups` empty), the wire
// port name that channel declares.  It is the machine-readable counterpart of
// `Aggregate.SingleGroupName()` for consumers that only see the rendered wire
// (cozebiz's merge type-sync pass, which must echo a legacy group's physical
// type from its candidates' producer declarations — and therefore has to know
// **which** group is the legacy one, now that the name is no longer the
// constant "Group1").
//
// Enumeration rides `collectIRNodes` (the census-complete node view, itself
// gated by optimize_deleter_coverage_test.go), so a slot that the view sees is
// a slot this function sees — same completeness contract as the optimization
// phase that owns the view.
func LegacySingleGroupNames(g *Graph) map[string]string {
	out := map[string]string{}
	if g == nil {
		return out
	}
	for id, n := range collectIRNodes(g) {
		agg, ok := n.(*Aggregate)
		if !ok || len(agg.Groups) > 0 || len(agg.Refs) == 0 {
			continue
		}
		out[id] = agg.SingleGroupName()
	}
	return out
}

// 模板槽（2026-09-13 收口）：产物里以**字符串模板**形式出现的槽位
// （http url/body、text concatResult、QA/Output/EndAnswer 模板、intent
// systemPrompt），其 {{block_output_<id>.<port>}} 占位符是一条**真实数据
// 依赖**——渲染期 rewriteTemplateRefs 会把它变成该节点的 node_inputs 绑定。
// 所以每个模板槽都必须登记到消费表里：漏登记 = 下游删掉仍被引用的 producer。
//
// 规则按「凡是会原样进产物的模板槽」穷举，而不是按「我们记得的节点类型」
// ——本轮 HTTP url 就是这么漏掉的（合法源码被 artifact gate 判悬空 ref）。
// 新增模板槽时必须在这里登记（契约测试见 internal/compiler 的
// dead-carrier 族与 internal/backend/cozebiz 的 render 族）。
func collectIRConsumers(g *Graph, nodes map[string]Node) map[string]map[string]int {
	consumers := map[string]map[string]int{}
	add := func(producer, output string) {
		if producer == "" {
			return
		}
		if consumers[producer] == nil {
			consumers[producer] = map[string]int{}
		}
		// Port match on the first dotted segment: refs may carry member
		// paths past a merge group / output port name.
		//
		// The value is the number of distinct reference SITES, not a flag:
		// callers that must prove "every consumer of this port is one I am
		// about to rewrite" (flattenChainAggregates) need identity, not a
		// port-name collision — two unrelated consumers on the same port name
		// are indistinguishable in a set.
		consumers[producer][portName(output)]++
	}
	addRef := func(r Ref) { add(r.Producer, r.Output) }
	addSource := func(s Source) { add(s.BlockID, s.Name) }
	addRefs := func(rs []Ref) {
		for _, r := range rs {
			addRef(r)
		}
	}
	addSources := func(ss []Source) {
		for _, s := range ss {
			addSource(s)
		}
	}
	addTemplate := func(tmpl string) {
		for _, m := range blockOutputRefRe.FindAllStringSubmatch(tmpl, -1) {
			add(m[1], m[2])
		}
	}
	// addValueTree walks a structured VALUE TREE (map/array literal that
	// carries ref leaves).  Builtin tool parameters are the channel: a map
	// argument whose leaves are variables is encoded as wire-shaped
	// `{path, ref_node}` maps (toolValueTree in build_node.go, mirrored by
	// the backend's wireRefValue + builtinVarInputs).  A ref that only lives
	// in such a leaf is a real data dependency — leaving it out of the
	// census made live producers look unconsumed.
	//
	// 第一手证据（2026-09-13，第三族收口后全量批跑）：一个内置工具调用
	// 拿**聚合变量**当实参、且该实参落在嵌套 map 字面量的叶子上时，值树
	// 叶子 ref 未被统计 ⇒ 被引用的聚合被删 ⇒ 产物 `ref_node` 悬空 ⇒
	// ARTIFACT_GATE 判非法产物（"ref to unknown node N (port Group1)"，
	// 命中 read_book / S115_read_book_mihe）。普查必须与渲染器认的是同一组
	// 叶子，否则「该死」与「真死」不是同一件事。
	var addValueTree func(v any)
	addValueTree = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			// 引用叶子判据与渲染器 wireRefValue 逐字一致（恰好 {path,
			// ref_node} 两个 string）——判据不一致就会一边当引用发绑定、
			// 一边当字面量渲染。
			if len(t) == 2 {
				p, ok1 := t["path"].(string)
				r, ok2 := t["ref_node"].(string)
				if ok1 && ok2 {
					add(r, p)
					return
				}
			}
			for _, e := range t {
				addValueTree(e)
			}
		case []any:
			for _, e := range t {
				addValueTree(e)
			}
		}
	}
	markContainerOutputConsumers(g.Root, consumers)
	for _, node := range nodes {
		switch v := node.(type) {
		case *Aggregate:
			addRefs(v.AllRefs())
			// 值通道吸收的整值（record 值逐字段携带时不在 AllRefs 里）：
			// 它们由本单元消费，producer 因此是活的（普查按槽位穷举，
			// 不按「名义候选」记忆）。
			addRefs(v.ValueSources)
		case *Selector:
			for _, c := range v.Conditions {
				if c.Left.Ref != nil {
					addRef(*c.Left.Ref)
				}
				if c.Right.Ref != nil {
					addRef(*c.Right.Ref)
				}
			}
		case *CodeRun:
			addSources(v.Inputs)
		case *TextProcess:
			addSources(v.Inputs)
			// 模板槽：concatResult 模板里的 {{block_output_<id>.<port>}}
			// 就是一条真实数据依赖（渲染期 rewriteTemplateRefs 把它变成
			// node_inputs 绑定）。见下方「模板槽」说明。
			addTemplate(v.Template)
		case *ListOp:
			addSources(v.Inputs)
		case *LLMCall:
			addSources(v.Inputs)
			// 模板槽的**作者名额表**（R-68 甲：`content: template "…" using {…}`）
			// 同样是真实数据依赖 —— 渲染期它变成节点 node_inputs 绑定。漏扫 ⇒
			// 生产者端口被判「无消费者」而剪掉 ⇒ 产物悬空 ref（一手：S116 的
			// merge 组 `agg_v1_g0` 只被名额表引用，剪掉后 ARTIFACT_GATE 硬报错
			// 「node 100005 declares ports [agg_v1_g1]」）。
			for _, cfg := range v.Config {
				if cfg.Key != "messages" {
					continue
				}
				msgs, ok := cfg.Value.([]Message)
				if !ok {
					continue
				}
				for _, m := range msgs {
					for _, b := range m.Bindings {
						addRef(b.Ref)
					}
				}
			}
		case *HTTPCall:
			addSources(v.Inputs)
			// 模板槽：url 是模板（P12），body 是 RAW_TEXT 模板——两者都
			// 承载 {{block_output_<id>.<port>}} 占位符。漏扫 url 是 2026-09-13
			// 实测到的盲区：`http("GET", `+"`"+`https://x/${len(a)}`+"`"+`, {})` 的
			// 参数 shim 被死节点清理删掉 ⇒ 产物悬空 ref ⇒ 合法源码编译失败。
			addTemplate(v.URL)
			addTemplate(v.Body)
		case *SubWorkflowCall:
			addSources(v.Inputs)
		case *KnowledgeCall:
			addSources(v.Inputs)
		case *ToolCall:
			for _, p := range v.Params {
				if p.Ref != nil {
					addSource(*p.Ref)
					continue
				}
				// 值树（builtin map/array 参数）：叶子可以是 ref。
				addValueTree(p.Value)
			}
		case *AgentCall:
			if v.Query != nil {
				addSource(*v.Query)
			}
		case *LoopReturnNode:
			addSource(v.Value)
		case *Start, *HistoryCall, *BreakNode, *ContinueNode:
			// 无数据引用通道：`start` 只有字面量默认值；`history` 无输入；
			// break/continue 是迭代终结子（P4）。显式列出 = 新增节点类型
			// 必须在此做决定，不能靠沉默跳过（optimize_census_test.go
			// 逐个类型核对）。
		case *AttemptStmt:
			// 语句形态的 Node（见 censusStatementKinds）：`collectIRNodes`
			// 从不把它自己放进节点集，只登记它持有的节点（op / 两个臂终端 /
			// exit junction），所以这个分叉**不会**在遍历 nodes 时命中；
			// 它在这里只为让类型 switch 对 Node 实现集保持「逐类型有交代」
			// （optimize_census_test.go 强制）。真正的引用通道登记在
			// markContainerOutputConsumers 的语句遍历里。
		case *Loop:
			// 循环节点：输入/状态/输出源由 markContainerOutputConsumers
			// 按 LoopStmt 统计（同一批值，两条路径都计入 ⇒ 只多不少）。
			addSources(v.Inputs)
			for _, src := range v.StateValues {
				addSource(src)
			}
			addSources(v.OutputSources)
		case *Output:
			addTemplate(v.Template)
			// 同上：`output template "…" using {…}` 的名额表是真实数据依赖
			// （渲染期 → 节点 node_inputs），不与 Template 的占位符重复也不互相取代。
			for _, b := range v.Bindings {
				addRef(b.Ref)
			}
		case *QA:
			addSources(v.Inputs)
			addTemplate(v.Template)
			// content 的**作者名额表**（`using {k: v}`）同样是真实数据依赖：
			// 渲染期它变成 question 节点的 node_inputs 绑定。漏扫 ⇒ 只被名额表
			// 引用的 producer 被判无消费者而剪掉 ⇒ 产物 `ID_SPACE_DANGLING_REFERENCE`
			// （一手：A16-H1 ① `let x = q + "!"; using { q: x }`）。
			for _, b := range v.Bindings {
				addRef(b.Ref)
			}
			// `dynamic_options: <值表达式>` 是一条**真实数据依赖**（选项来源节点
			// 必须算活，否则被纯节点清扫删掉 ⇒ 产物 `dynamic_option` 引用悬空）。
			// 与 `*Switch` 的 `Query` 同族（同样是一个 Source 值通道）。
			if v.DynamicOptions != nil {
				addSource(*v.DynamicOptions)
			}
		case *Switch:
			// 分类查询本身是一条数据引用（B1，2026-09-13 普查收口）：
			// `switch (query) { ... }` 的 Query 指向被分类的值——经典形状
			// 就是「提升变量 + 两臂赋值」的 phi（合法源码
			// `string query; if (c) { query = "yes"; } else { query = "no"; }
			// switch (query) {...}`）。漏登记 = 该 phi 被判无消费者而删除、
			// query 指向已消失的节点（悬空 ref）。
			addSource(v.Query)
			addTemplate(v.SystemPrompt)
		case *Assign:
			addSource(v.Value)
			for _, p := range v.ExtraPairs {
				addSource(p.Value)
			}
		case *End:
			addRefs(v.Inputs)
			if v.Answer != nil {
				addTemplate(v.Answer.Template)
			}
			for _, o := range v.Outputs {
				addRef(o.Ref)
			}
			if v.Answer != nil {
				for _, b := range v.Answer.Bindings {
					addRef(b.Ref)
				}
			}
		}
	}
	return consumers
}

func markContainerOutputConsumers(b *Block, consumers map[string]map[string]int) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		switch v := s.(type) {
		case *LoopStmt:
			// A loop's output/state sources consume the named producer port.
			for _, src := range v.OutputSources {
				addSourceConsumer(consumers, src)
			}
			for _, src := range v.StateValues {
				addSourceConsumer(consumers, src)
			}
			for _, src := range v.Inputs {
				addSourceConsumer(consumers, src)
			}
			markContainerOutputConsumers(v.Body, consumers)
		case *InlineStmt:
			// An inlined body is a statement container like any other: its
			// loops carry inputs / state / output sources that CONSUME named
			// producers. Leaving it out hid those consumers, so a producer
			// feeding only an inline-body loop looked unconsumed (2026-09-13:
			// `for (x in items)` inside an inlined helper — the array shim
			// was read by the loop input yet the census missed it, and the
			// new dead-pure-node cleanup deleted it, producing a product with
			// a dangling `ref_node` that the artifact gate rejected).
			markContainerOutputConsumers(v.Body, consumers)
		case *IfStmt:
			markContainerOutputConsumers(v.Then, consumers)
			markContainerOutputConsumers(v.Else, consumers)
		case *AttemptStmt:
			// **渲染期才产生的引用**：当 attempt 是流程尾（`FlowExit`）时，
			// 它的 exit junction 的尾值通道（`junctionTailRef`：legacy 单值
			// Group1）会被渲染器绑进规范 End 的 `node_inputs`（cozebiz
			// `renderAttempt`：`endInputs = [{Producer: merge, Output:
			// "Group1"}]`）——产物里看得见的一条真实数据引用，只是它的载体
			// （End）由渲染器在渲染期合成、IR 侧不持有，所以它不是任何
			// 节点字段里的 `Ref`。
			//
			// 不登记它就等于「该死」与「真死」不是同一件事：exit junction
			// 被判无消费者而删除 ⇒ End 的返回值绑定静默消失（node_inputs
			// 由 [Group1] 变 []）⇒ raising 拒绝还原（RS-ERR-002「两臂共享
			// 子图」）。第一手证据：G-A 性质测试 seed=14/24/40
			// （`attempt{success{return} failure{return}}`，两臂都 return）
			// ——671 份语料的批跑没覆盖这个形状，是每轮 48 例的形状生成器
			// 先撞上的。
			//
			// 判据与渲染器**同源**（同一个 `junctionTailRef`），不是重写一遍。
			if v.FlowExit {
				if tail := junctionTailRef(v.Exit); tail != nil {
					addSourceConsumer(consumers, Source{BlockID: tail.nodeID, Name: tail.output})
				}
			}
			markContainerOutputConsumers(v.Success, consumers)
			markContainerOutputConsumers(v.Failure, consumers)
		case *ParallelStmt:
			for _, branch := range v.Branches {
				markContainerOutputConsumers(branch, consumers)
			}
		case *QaStmt:
			for _, branch := range v.Branches {
				markContainerOutputConsumers(branch.Body, consumers)
			}
		case *SwitchStmt:
			for _, branch := range v.Branches {
				markContainerOutputConsumers(branch.Body, consumers)
			}
		}
	}
}

func pruneDeadAggregates(b *Block, dead map[string]bool) {
	if b == nil {
		return
	}
	out := make([]Stmt, 0, len(b.Stmts))
	for _, s := range b.Stmts {
		if ns, ok := s.(*NodeStmt); ok {
			if dead[ns.Node.NodeID()] {
				continue
			}
		}
		pruneDeadAggregatesIn(s, dead)
		out = append(out, s)
	}
	b.Stmts = out
}

func pruneDeadAggregatesIn(s Stmt, dead map[string]bool) {
	if s == nil {
		return
	}
	switch v := s.(type) {
	case *IfStmt:
		if v.Merge != nil && dead[v.Merge.ID] {
			v.Merge = nil
		}
		pruneDeadAggregates(v.Then, dead)
		pruneDeadAggregates(v.Else, dead)
	case *AttemptStmt:
		if v.Exit != nil && dead[v.Exit.ID] {
			v.Exit = nil
		}
		pruneDeadAggregates(v.Success, dead)
		pruneDeadAggregates(v.Failure, dead)
	case *InlineStmt:
		// 帧出口 phi 是**图级载体**（同 End.FlowExitMerge）：判死即摘，否则
		// 每轮判死又摘不掉 ⇒ changed 恒真 ⇒ 挂死（见 ReturnStmt 分支的同条注释）。
		if v.Exit != nil && dead[v.Exit.ID] {
			v.Exit = nil
		}
		pruneDeadAggregates(v.Body, dead)
	case *LoopStmt:
		pruneDeadAggregates(v.Body, dead)
	case *ParallelStmt:
		for _, branch := range v.Branches {
			pruneDeadAggregates(branch, dead)
		}
	case *QaStmt:
		if v.Merge != nil && dead[v.Merge.ID] {
			v.Merge = nil
		}
		for i := range v.Branches {
			pruneDeadAggregates(v.Branches[i].Body, dead)
		}
	case *SwitchStmt:
		if v.Merge != nil && dead[v.Merge.ID] {
			v.Merge = nil
		}
		for i := range v.Branches {
			pruneDeadAggregates(v.Branches[i].Body, dead)
		}
	case *ReturnStmt:
		// **图级载体**（A12-H8，2026-09-16）：流程出口 phi（`End.FlowExitMerge`）
		// 挂在 End 节点上，**不是**结构树里的语句 —— 节点视图（collectIRNodes 的
		// `case *ReturnStmt: add(v.End.FlowExitMerge)`）收它，删除器就必须也能摘它。
		// 两者不一致时，该聚合每轮都被判死、每轮都摘不掉 ⇒ `changed` 恒真 ⇒
		// **挂死**（实测触发路径：产生两个同 id 的 end ⇒ collectIRNodes 按 id 取
		// 最后一个 ⇒ 视图里看不见它的消费者）。摘掉它是**语义正确**的动作：判死
		// 的前提就是「没有任何真实数据引用读它的输出端口」（本文件开头的存活判据），
		// 与结构树上的汇聚同一条规则、无形态守卫。
		if v.End != nil && v.End.FlowExitMerge != nil && dead[v.End.FlowExitMerge.ID] {
			v.End.FlowExitMerge = nil
		}
	}
}

// portName returns the first dotted segment of an output-port reference:
// the producer output port itself (merge group names / declared ports are
// single-segment; member paths past the port are backend concern).
func portName(output string) string {
	if i := strings.IndexByte(output, '.'); i >= 0 {
		return output[:i]
	}
	return output
}

// addSourceConsumer records a Source's producer-port consumption.
func addSourceConsumer(consumers map[string]map[string]int, src Source) {
	if src.BlockID == "" {
		return
	}
	if consumers[src.BlockID] == nil {
		consumers[src.BlockID] = map[string]int{}
	}
	consumers[src.BlockID][portName(src.Name)]++
}
