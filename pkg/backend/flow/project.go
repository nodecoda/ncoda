package flow

// project.go — D9: the flattened Nodes/Edges projection is a derived view
// of the structured tree (docs/WORKFLOW-IR.md §11). The IR itself carries
// no connect/edge; backend renderers obtain their platform flow here.
//
// Derivation rules (byte-identical with the legacy builder edges):
//   - ir.Block order: stmt[i] tail → stmt[i+1] head (junction/port thread)
//   - If: selector → Then/Else subblocks (true/false ports); subblock
//     tails → Aggregate; else-less if defers its false port (pendingIf)
//   - Attempt: op source/fail-branch → branch subblocks; branch tails →
//     their own ir.End terminals
//   - ir.Loop: Body derives the child subgraph + container entry/exit,
//     fan-out and loop-output edges (legacy emitLoopNode special edges)
//   - Return: value → shared ir.End (top level) or flows upward (subscope)
//   - Edge order = structure depth-first = the legacy AddEdge order.

import (
	"fmt"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ir"
)

// Edge is an ephemeral backend control-flow arc.
type Edge struct {
	SourceNodeID string
	TargetNodeID string
	SourcePortID string
	TargetPortID string
}

// ProjectedLoop is the graph-shaped loop node presented to backends.
type ProjectedLoop struct {
	*ir.Loop
	Children   []ir.Node
	InnerEdges []Edge
}

// pendingIfState is a dangling else-less if whose unmatched path falls
// through the selector's false port into the following statement (or the
// enclosing block's consumer).
type pendingIfState struct {
	ifStmt    *ir.IfStmt
	thenExits []string
}

// portObligation 是**分支端口边义务**（P4「分支端口边永远显式」）：一个渲染不出
// 任何节点的 if 臂，其 selector 仍欠该臂端口一条出边，而这条边的落点（续接语句 /
// 宿主汇聚 / 流程出口）在投影该 if 时还不知道 ⇒ 记成义务，落点确定时 drain。
//
// 与 cozebiz 渲染器的 `renderEnv.pendingPorts` / `drainPorts` 同构 —— 同一事实
// （P4）的两处投影必须同解，否则两个后端对同一结构给出不同的图。一手反例
// （2026-09-28，语料 10 源，runtime 静态拒收）：
//
//	if (!empty(v.url)) { } else { <插件调用> }   // 空 then 臂
//	<续接语句>
//
// 旧投影把空臂的 selector 当**裸尾**塞进 junction，续接时被 connect 成一条
// **无端口**边 ⇒ 该 selector 只声明了 false 口，运行到 true 路径即
// `RSPEC_INVALID: $branch "true" has no declared target (have [false])`。
type portObligation struct {
	Selector string
	Port     string
}

// deriveCtx is the derivation state. pendingIf is intentionally a ctx-level
// field so a dangling else-less if inside a subblock propagates to the
// enclosing block exactly like the builder's pendingElse state machine.
type deriveCtx struct {
	nodes     []ir.Node
	edges     []Edge
	idToNode  map[string]ir.Node
	endSeen   map[string]bool
	pendingIf *pendingIfState
	// pendingPorts = 未落地的分支端口义务（空臂 selector 的未匹配端口边），
	// 落点确定时由 drainPorts 接上并清空。见 portObligation。
	pendingPorts []portObligation
	// scopeDepth 递归块深度（root=0，if/loop/helper 体 +1）。A1 分支
	// throw_error 只在根级物化 End 900001：嵌套作用域（loop/helper 内）
	// 的 throw_error 加 edge→900001 即可，End 由根级 ReturnStmt 物化
	// （提前 addNode 会占用 endSeen，根级 ReturnStmt 被跳过 → canvas
	// 缺 End 节点，X12 helper 回归）。
	scopeDepth int
	// flowExitWired marks Ends whose flow-exit merge → End edge was emitted.
	flowExitWired map[string]bool
	// flowExitMergeIDs marks the materialized flow-exit phi nodes so the
	// projection can emit them with the End they feed (a renderer emits a
	// node's incoming edges when the node is emitted, so a merge minted at the
	// first exit but fed by later exits must not be emitted early).
	flowExitMergeIDs map[string]bool
	// skippedMerge:一分支 flow-end 时被 pass-through 跳过的 merge
	// （mergeID → output/group name → IR 原始候选）。外层 aggregate/end 的
	// ref 可能指向被跳过的 merge；投影末尾按对应输出组重写到首个候选。
	skippedMerge map[string]map[string][]ir.Ref
}

// Projection is an ephemeral backend view derived from structured IR. It is
// deliberately separate from ir.Graph so graph-shaped platform data is never
// stored as program semantics.
type Projection struct {
	Nodes []ir.Node
	Edges []Edge
}

// Project rebuilds the backend projection from the structured tree. It
// reuses builder-created node objects (stable ids) and replays the legacy
// edge construction in tree order.
func Project(g *ir.Graph) (*Projection, error) {
	if g.Root == nil {
		return nil, fmt.Errorf("ir: derive: graph has no structured root")
	}
	d := &deriveCtx{idToNode: map[string]ir.Node{}, endSeen: map[string]bool{}}
	out := &Projection{}

	// The entry Start node is graph-level structured state, not a statement.
	start := g.Entry
	if start == nil {
		return nil, fmt.Errorf("ir: derive: missing start node")
	}
	d.addNode(start)

	exits, _, _, err := d.deriveBlock(g.Root, []string{start.NodeID()}, "")
	if err != nil {
		return nil, err
	}
	// pass-through 跳过的 merge 仍可能被下游 ref 引用（X4/X5/X16 暴露:
	// 外层 if-merge 的 Group1 指向被跳过的内层 merge）——重写为 merge 的
	// 首个值 ref（Group1 = 首非 nil = pass-through 语义）。
	d.fixupSkippedMergeRefs()
	// Top-level dangling else-less if: the unmatched path joins the end
	// junction as thenIDs + selector (legacy build() junction handling —
	// deliberately NOT de-duplicated).
	if d.pendingIf != nil {
		p := d.pendingIf
		d.pendingIf = nil
		// 主流程已以 flow-end return 终止（then 分支 return 建了共享 End）
		// 而函数尾无语句时，build 不再合成隐式 return —— else-less if 的
		// false 路径（条件为假 → 函数结束）必须显式连到共享 End（P4 分支
		// 端口边永远显式；P5 主流程任何路径都终止于唯一 end 900001）。
		// legacy void trailing if（投影无 End）保持原样不投影。
		if end := d.sharedEnd(); end != "" {
			entry := d.flowExitEntry(d.endNode(end))
			for _, tid := range filterEnds(p.thenExits, d) {
				if tid != entry {
					d.edges = append(d.edges, Edge{SourceNodeID: tid, TargetNodeID: entry})
				}
			}
			d.edges = append(d.edges, Edge{SourceNodeID: p.ifStmt.ID, TargetNodeID: entry, SourcePortID: "false"})
		}
		exits = append(append([]string{}, filterEnds(p.thenExits, d)...), p.ifStmt.ID)
	}

	// 文档尾有两件事，**都**必须做（协作渲染器 `render.go` 的文档尾是同一个形状：
	// `if prev != "" && prev != endID && prev != entry { addEdge(prev, entry) }` +
	// `drainPorts(env, entry, addEdgePort)`）。
	//
	// ① **残留臂尾接进流程出口入口**：IR 的最后一条语句以「臂尾」收尾时
	// （`if (c) { return; } else { throw_error.throw_error(…); }` —— else 臂的尾是
	// **插件调用**而不是 `return`），臂尾留在 `exits` 里、后面没有任何语句 ⇒ 不接
	// 就是**死端节点** ⇒ runtime `GraphRunError: no tasks to execute`（一手 17 源，
	// `7531294550921674779_X3_Tdouyinshuju_table_1` 等；平台 wire 与渲染器**都**把
	// 它接到 900001，只有这一处投影漏了 ⇒ 「同一事实的两处投影必须同解」的缺口）。
	// 但**不得造逆向边**（图论判据，与同文件 `exitWireTarget` 同一条）：`entry` 已经
	// 沿已建边可达 `tid` 时，再补 `tid → entry` 就是闭环 —— runtime 静态检查会把整份
	// spec 判成 `RSPEC_INVALID: DAG is invalid, has loop`（`Compile` 面表现为
	// ADAPTER_ERROR）。一手形状（死代码夹具 `UNREACHABLE_CODE`：
	// `return "ok"; output("never");` ⇒ IR 把不可达的 output 挂在 End 之后，投影出
	// `900001 → 100003`；补边即闭环）。
	if endID := d.sharedEnd(); endID != "" {
		if entry := d.flowExitEntry(d.endNode(endID)); entry != "" {
			for _, tid := range filterEnds(exits, d) {
				if tid == entry || tid == endID {
					continue
				}
				if d.reachesFrom(entry, []string{tid}) {
					continue
				}
				d.edges = append(d.edges, Edge{SourceNodeID: tid, TargetNodeID: entry})
			}
		}
	}
	// ② 文档尾 drain（第 4 个 drain 点同解）：仍未落地的端口义务接进流程出口入口。
	// **兜底不是许可证** —— 接不上就 fail loud：一条悬空的端口义务 = 一个 selector
	// 丢了分支口（P4），产物在 runtime 会以 `$branch … has no declared target` 拒收；
	// 此处爆炸比出厂一个坏产物便宜。
	if len(d.pendingPorts) > 0 {
		if end := d.sharedEnd(); end != "" {
			d.drainPorts(d.flowExitEntry(d.endNode(end)))
		}
	}
	if len(d.pendingPorts) > 0 {
		parts := make([]string, 0, len(d.pendingPorts))
		for _, ob := range d.pendingPorts {
			parts = append(parts, ob.Selector+"."+ob.Port)
		}
		return nil, fmt.Errorf("flow: 未落地的分支端口义务（P4 分支端口边永远显式）：%s", strings.Join(parts, ", "))
	}

	// End nodes are terminals (leaves): renderers emit a node's incoming
	// edges when the node is emitted, so every end must come last in the
	// node list — its incoming edges may reference any other node (incl.
	// flow-end returns from parallel branches, P5 #4, whose End is built
	// mid-tree). Stable partition: non-End nodes keep their derive order,
	// all Ends (shared 900001, attempt/QA terminals) move to the tail in
	// first-appearance order.
	out.Nodes = stableEndsLast(d.nodes, d.edges, d.flowExitMergeIDs)
	out.Edges = d.edges
	_ = exits
	return out, nil
}

// stableEndsLast reorders a small subset of TERMINAL End nodes to the tail
// of the projection, preserving relative order within each class (non-Ends
// keep their derive order; moved Ends keep their first-appearance order).
//
// Which Ends move? Only Ends that have an INCOMING edge whose source node
// derives AFTER the End itself (a forward reference). Renderers emit a
// node's incoming edges when the node is emitted, so such an End would
// reference an un-emitted source ("unknown source node"). This happens with
// P5 #4 flow-end returns inside a parallel branch: the shared 900001 End is
// minted mid-tree, then the other branch tails and the (unreachable)
// continuation still wire into it. The DAG is unchanged — only emit order
// moves, and only as far as needed.
//
// Ends with no forward-referencing incoming edges keep their derive
// position: moving them would break the frozen legacy/Python golden emit
// order (attempt/QA/if branch Ends sit right after their branch code — e.g.
// parity-golden/attempt_llm.yml). Ends with outgoing edges are mid-path
// nodes (output(...) chain semantics, M14 48_response_accumulation) and
// never move.
func stableEndsLast(nodes []ir.Node, edges []Edge, flowExitMerges map[string]bool) []ir.Node {
	pos := make(map[string]int, len(nodes))
	for i, n := range nodes {
		pos[n.NodeID()] = i
	}
	hasOut := make(map[string]bool, len(edges))
	inFromLater := make(map[string]bool)
	for _, e := range edges {
		hasOut[e.SourceNodeID] = true
		if pos[e.TargetNodeID] < pos[e.SourceNodeID] {
			inFromLater[e.TargetNodeID] = true
		}
	}
	// A flow-exit phi is minted the moment its FIRST exit is derived, but it
	// also takes wires from the later exits — so it always has a
	// forward-referencing incoming edge and must be emitted with the End it
	// feeds. Both move to the tail, merge before end (the merge is the end's
	// only predecessor among them).
	sortMergesFirst := func(ns []ir.Node) []ir.Node {
		merges, ends := make([]ir.Node, 0, len(ns)), make([]ir.Node, 0, len(ns))
		for _, n := range ns {
			switch {
			case flowExitMerges[n.NodeID()]:
				merges = append(merges, n)
			default:
				ends = append(ends, n)
			}
		}
		return append(merges, ends...)
	}
	// A flow-exit pair (the phi and the End it feeds) always belongs at the
	// tail: the phi is minted at the first exit but fed by the later ones, and
	// only the phi feeds the End. Deferring the pair together keeps the emit
	// order a valid topological order for renderers that resolve an edge's
	// source when the target is emitted.
	deferred := map[string]bool{}
	for _, n := range nodes {
		end, ok := n.(*ir.End)
		if !ok || end.FlowExitMerge == nil || hasOut[end.ID] {
			continue
		}
		deferred[end.ID] = true
		deferred[end.FlowExitMerge.ID] = true
	}
	first, last := make([]ir.Node, 0, len(nodes)), make([]ir.Node, 0, len(nodes))
	for _, n := range nodes {
		if deferred[n.NodeID()] {
			last = append(last, n)
			continue
		}
		if _, isEnd := n.(*ir.End); isEnd && !hasOut[n.NodeID()] && inFromLater[n.NodeID()] {
			last = append(last, n)
			continue
		}
		first = append(first, n)
	}
	return append(first, sortMergesFirst(last)...)
}

// addNode appends a node to the projection once (shared Ends are
// referenced by several ReturnStmts).
func (d *deriveCtx) addNode(n ir.Node) {
	if n == nil {
		return
	}
	if d.endSeen[n.NodeID()] {
		return
	}
	d.nodes = append(d.nodes, n)
	d.idToNode[n.NodeID()] = n
	d.endSeen[n.NodeID()] = true
}

// flowExitEntry returns the node a flow-exit wire must enter. When the flow has
// more than one value-carrying exit, the End is fed by a flow-exit phi
// (ir.End.FlowExitMerge) and only that merge may enter the End — real coze-biz
// exports carry `merge(G1:[A,B]) → end` with end.node_inputs bound to the
// merge (2026-09-12 用户拍板：平台 end 只承载一个值绑定，分支直连会让第二条
// 出口的值静默丢失). The merge is materialized here (it is a graph-level
// producer, not a tree statement) and the merge → End edge is emitted once.
func (d *deriveCtx) flowExitEntry(end *ir.End) string {
	if end == nil {
		return ""
	}
	if end.FlowExitMerge == nil {
		return end.ID
	}
	d.addNode(end.FlowExitMerge)
	if d.flowExitWired == nil {
		d.flowExitWired = map[string]bool{}
	}
	if d.flowExitMergeIDs == nil {
		d.flowExitMergeIDs = map[string]bool{}
	}
	d.flowExitMergeIDs[end.FlowExitMerge.ID] = true
	if !d.flowExitWired[end.ID] {
		d.flowExitWired[end.ID] = true
		d.edges = append(d.edges, Edge{SourceNodeID: end.FlowExitMerge.ID, TargetNodeID: end.ID})
	}
	return end.FlowExitMerge.ID
}

// exitWireTarget 解析 return 的控制边落点（返回「前驱列表, 落点」）。
//
// 默认落点 = 流程出口的入口（`flowExitEntry`：多条取值出口时是先物化的出口 φ），
// 前驱原样透传（既有行为，fresh φ 的形状不变）。
//
// 但该入口**已经是本 return 前驱的上游**时，把前驱接回去就是一条**逆向控制边**
// ⇒ 闭环 ⇒ runtime 静态拒收 `RSPEC_INVALID: DAG is invalid, has loop`。
// 一手形状（2026-09-28，`7571282400288145434_sora_jietoucaifang` 全链可复算）：
//
//	if/else 两臂各写 Group1 ──► 汇聚单元 M
//	add_videos({draft_id: Group1})            // 读 M 的整值 ⇒ M → add_videos
//	return Group1;                            // 单出口，值 = M 的整值读
//
// ⇒ `end.FlowExitMerge` **复用** M（`build_return.go` 的 wholeValueExit 路径：
// 单出口且出口值是既有汇聚单元的整值读 ⇒ 不另立 φ），于是本条 return 的前驱
// （add_videos）接回 M = 逆向边，与 `M → add_videos` 成环。
//
// 判据 = **图论（控制边是 DAG 的边）**，不是「仅当某形态」：
//   - 出口入口已在前驱上游 ⇒ 唯一正确的「return 之后」落点是 End 本身；
//     入口 → End 的连线仍由 `flowExitEntry` 给出（值连通不变）；
//   - 前驱里**就是**入口本身（自环，另一手形状：attempt 的出口 φ 既是臂汇聚、
//     又是流程出口入口，一手 = 本仓 `attempt http(…) as r { … }` 最小片段）⇒
//     自环不是控制关系 ⇒ 该前驱剔除（End 已由入口那条线喂着，值连通不变）。
func (d *deriveCtx) exitWireTarget(end *ir.End, preds []string) ([]string, string) {
	entry := d.flowExitEntry(end)
	if entry == "" || entry == end.ID {
		return preds, entry
	}
	isPred := false
	for _, p := range preds {
		if p == entry {
			isPred = true
			break
		}
	}
	if !isPred && !d.reachesFrom(entry, preds) {
		return preds, entry
	}
	keep := make([]string, 0, len(preds))
	for _, p := range preds {
		if p != entry {
			keep = append(keep, p)
		}
	}
	return keep, end.ID
}

// reachesFrom 报告 from 沿**已建**控制边能否经至少一条边到达 targets 之一
// （不含 from ∈ targets 的情形 —— 那是自环，由调用方另行处置）。
func (d *deriveCtx) reachesFrom(from string, targets []string) bool {
	want := make(map[string]bool, len(targets))
	for _, t := range targets {
		want[t] = true
	}
	adj := map[string][]string{}
	for _, e := range d.edges {
		adj[e.SourceNodeID] = append(adj[e.SourceNodeID], e.TargetNodeID)
	}
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, nxt := range adj[cur] {
			if want[nxt] {
				return true
			}
			if !seen[nxt] {
				seen[nxt] = true
				stack = append(stack, nxt)
			}
		}
	}
	return false
}

// sharedEnd 返回投影中已物化的共享 End（900001 或首个 End）节点 ID。
// 主流程 flow-end return 物化 End 后，函数尾悬空的 else-less if 的 false
// 路径要显式连回它；无 End（legacy void trailing if）返回空串。
// endNode returns the IR End with the given id (nil when absent).
func (d *deriveCtx) endNode(id string) *ir.End {
	if n, ok := d.idToNode[id].(*ir.End); ok {
		return n
	}
	for _, n := range d.nodes {
		if e, ok := n.(*ir.End); ok && e.ID == id {
			return e
		}
	}
	return nil
}

func (d *deriveCtx) sharedEnd() string {
	for _, n := range d.nodes {
		if end, ok := n.(*ir.End); ok {
			return end.ID
		}
	}
	return ""
}

// connect wires target from every prev node, mirroring Builder.connect:
// a branch/unmatched port must leave a ir.Selector source (the port is stamped
// on the selector's edge); otherwise first/last-edge stamping applies.
func (d *deriveCtx) connect(prevIDs []string, target, firstPort string, portOnLast bool) {
	// 端口义务先落地：本次 connect 的目标就是它们的落点（P4）。空 junction
	// （两臂都渲染不出的 if）也走这里 —— 那时 prevIDs 为空，义务边是唯一的控制边。
	d.drainPorts(target)
	selIdx := -1
	for i, pid := range prevIDs {
		if n, ok := d.idToNode[pid]; ok {
			if _, isSel := n.(*ir.Selector); isSel {
				selIdx = i
				break
			}
		}
	}
	for i, pid := range prevIDs {
		port := ""
		switch {
		case selIdx >= 0:
			// 嵌套 else-less if:junction 可含多个 selector（内层未匹配路径
			// 折入外层 junction,Z17）——分支端口边必须逐个显式（P4）。
			if d.isSelector(pid) {
				port = firstPort
			}
		case firstPort != "" && ((!portOnLast && i == 0) || (portOnLast && i == len(prevIDs)-1)):
			port = firstPort
		}
		d.edges = append(d.edges, Edge{SourceNodeID: pid, TargetNodeID: target, SourcePortID: port})
	}
}

// owePort 登记一条分支端口义务（渲染不出节点的臂 ⇒ selector 欠该臂端口一条边）。
func (d *deriveCtx) owePort(sel, port string) {
	d.pendingPorts = append(d.pendingPorts, portObligation{Selector: sel, Port: port})
}

// drainPorts 把所有未落地的端口义务接进 dst 并清空 —— 这是**唯一** drain 形状
// （协作渲染器的 drainPorts 同形：续接语句 / 分支汇聚 / 容器尾 / 文档尾四个落点）。
// 义务边是**分支端口边**（P4），不是普通控制边：每个端口各自显式。
func (d *deriveCtx) drainPorts(dst string) {
	d.drainPortsFrom(0, dst, "")
}

// drainPortsFrom 接的是 [mark:] 之后登记的义务（dstPort = 容器口时用），保留
// [0:mark) 的 —— 那些属于**外层** junction，落点不是本容器。
func (d *deriveCtx) drainPortsFrom(mark int, dst, dstPort string) {
	if dst == "" || mark >= len(d.pendingPorts) {
		return
	}
	rest := append([]portObligation{}, d.pendingPorts[mark:]...)
	d.pendingPorts = d.pendingPorts[:mark]
	for _, ob := range rest {
		if ob.Selector == "" || ob.Selector == dst {
			continue
		}
		d.edges = append(d.edges, Edge{SourceNodeID: ob.Selector, TargetNodeID: dst, TargetPortID: dstPort, SourcePortID: ob.Port})
	}
}

// deriveBlock derives one ordered block. It returns the block's junction
// (activity tails), the thread port, and whether any node was produced.
// A trailing dangling else-less if is left in d.pendingIf for the caller
// (the top-level build() junction, the loop tail, or the enclosing block's
// fallthrough) to consume.
func (d *deriveCtx) deriveBlock(b *ir.Block, entry []string, entryPort string) (exits []string, exitPort string, hadNode bool, err error) {
	return d.deriveBlockDepth(b, entry, entryPort, 0)
}

// deriveBlockDepth derives one block at a given nesting depth. scopeDepth
// increments for every recursive block (if/loop/helper bodies).
func (d *deriveCtx) deriveBlockDepth(b *ir.Block, entry []string, entryPort string, depth int) (exits []string, exitPort string, hadNode bool, err error) {
	prevDepth := d.scopeDepth
	d.scopeDepth = depth
	defer func() { d.scopeDepth = prevDepth }()
	current := entry
	port := entryPort
	// hadNode = 「本块是不是**什么都没产出**」——空臂端口义务（owePort）的**唯一**
	// 判据输入。
	//
	// **不得只用节点数增量当代理**（2026-09-28 根因，一手 = `if (c) { return; }
	// else { return; }`）：共享 `end`（900001）由**第一条**流出口的 `return` 物化，
	// 第二条流的 `return` 再 `addNode` 时被 `endSeen` 去重 ⇒ 节点数不变 ⇒ 该臂被判
	// 成空臂 ⇒ 走 `owePort`，而它的端口边**早就由 `*ir.ReturnStmt` 的 `d.connect`
	// 落过** ⇒ 落地时又补一条 ⇒ 同一 `(selector, port, end)` 三元组出现两次
	// （投影面：`100002 --false--> 900001` ×2；渲染器侧有逻辑去重，产物面看不出来
	// —— 正因如此它此前没人发现）。
	// 判据 = 「该臂产生了控制贡献」= 物化了节点**或**产生了边。两个增量取或：
	//   · 真空臂 `{ }`：两者都不动 ⇒ 欠端口边（P4 的核心义务，必须保住）；
	//   · `{ return; }` 落在共享 End 上：节点被去重、但边 +1 ⇒ 不欠（端口边已落地）。
	nodeCount := len(d.nodes)
	edgeCount := len(d.edges)
	for _, st := range b.Stmts {
		// Dead-path cut: a terminating tail emptied the junction — a
		// flow-end return inside a parallel branch (P5 #4: the branch
		// return terminates the WHOLE flow, the barrier does not wait for
		// it) or a throw_error tail (A1). Every later statement in this
		// block is unreachable; skip it so the projection carries only the
		// real control flow. Without the cut the ghost node has no
		// incoming edges and the runtime reachability gate (RUnreachableNode)
		// rejects the spec. A dangling else-less if still has a pending
		// junction (pendingIf), so the cut only fires on a truly empty path.
		// 未落地的端口义务也算「活路径」：两臂都渲染不出的 if 之后，junction
		// 为空但 selector 的端口边还没接（它才是续接语句的控制边）。
		if len(current) == 0 && len(d.pendingPorts) == 0 && d.pendingIf == nil {
			break
		}
		// ir.Loop-scope fallthrough of a dangling else-less if (the builder's
		// emitStmts pendingElse handling: the next non-return statement
		// runs from merged then-tails + selector on the false port).
		if rs, ok := st.(*ir.ReturnStmt); ok && rs.Implicit && d.pendingIf != nil {
			// The synthesized trailing return consumes the full junction
			// (thenIDs + selector, un-merged) — see the ir.ReturnStmt case.
		} else if d.pendingIf != nil {
			p := d.pendingIf
			d.pendingIf = nil
			seen := map[string]bool{p.ifStmt.ID: true}
			merged := []string{}
			for _, id := range p.thenExits {
				if id == p.ifStmt.ID || seen[id] || d.isBreakish(id) || d.isEnd(id) {
					continue
				}
				seen[id] = true
				merged = append(merged, id)
			}
			current = append(merged, p.ifStmt.ID)
			port = "false"
		}
		switch s := st.(type) {
		case *ir.NodeStmt:
			d.addNode(s.Node)
			d.connect(current, s.Node.NodeID(), port, false)
			current = []string{s.Node.NodeID()}
			port = ""
		case *ir.IfStmt:
			d.addNode(s.Cond)
			d.connect(current, s.ID, port, false)
			thenExits, _, thenHad, terr := d.deriveBlockDepth(s.Then, []string{s.ID}, "true", depth+1)
			if terr != nil {
				return nil, port, true, terr
			}
			if s.Else == nil {
				// else-less: the unmatched path waits for the next
				// statement (or the enclosing block's consumer).
				if s.Relay != nil {
					d.addNode(s.Relay)
					d.edges = append(d.edges, Edge{SourceNodeID: s.ID, TargetNodeID: s.Relay.NodeID(), SourcePortID: "true"})
					thenExits = []string{s.Relay.NodeID()}
					thenHad = true
				}
				// 嵌套 else-less if:then 块以另一个 else-less if 收尾时,
				// 内层未匹配路径必须折入外层 junction（内层 then 尾 +
				// 内层 selector false 端口）,否则内层 false 路径丢失
				// （Z17:msg!=success → 无后继边 → 运行时 $branch "false"
				// no declared target）。
				thenExits = d.foldPendingIf(thenExits)
				d.pendingIf = &pendingIfState{ifStmt: s, thenExits: thenExits}
				current = thenExits
				port = "false"
				break
			}
			elseExits, _, elseHad, eerr := d.deriveBlockDepth(s.Else, []string{s.ID}, "false", depth+1)
			if eerr != nil {
				return nil, port, true, eerr
			}
			thenPort, elsePort := "", ""
			thenSrcs, elseSrcs := thenExits, elseExits
			if !thenHad {
				thenPort = "true"
				thenSrcs = []string{s.ID}
			}
			if !elseHad {
				elsePort = "false"
				elseSrcs = []string{s.ID}
			}
			// A branch whose tail is a trailing else-less if leaves its
			// selector in the exit junction; that selector's unmatched
			// path joins the merge on the default (false) port.
			branchSrcPort := func(p, id string) string {
				if p == "" && d.isSelector(id) {
					return "false"
				}
				return p
			}
			// merge 只在两分支都有出口时投影（phi/互斥+null 汇聚）。
			// 主流程分支 return（FlowEnd）语义：分支出口为 []（直达
			// 共享 end 900001，不进入 merge）——此时若另一分支自然
			// 流出，直接透传其出口（无 phi 需求：能到达下游的路径
			// 只有该分支，下游引用仍可达，且 end 类型节点无 output
			// 端口，投影 merge 会制造 UNRESOLVED_REFERENCE 幽灵）；
			// 两分支都 flow-end 时 dead-path cut（后续语句不可达）。
			if s.Merge == nil {
				// A live if with one flow-end arm has no phi at this junction.
				// The continuing arms both fall through to the enclosing
				// consumer; this is the structured cut, not a rendering filter.
				//
				// 空臂（渲染不出任何节点的臂）**不作为裸尾**进入 junction：selector
				// 欠的是**端口边**（P4），落点在投影本 if 时还不知道 ⇒ 记义务，落点
				// 确定时 drain（与协作渲染器 renderEnv.pendingPorts 同解）。裸尾进
				// junction 会被 connect 成一条**无端口**边 —— 既违反 P4，又与端口边
				// 重复，且运行到该臂时 `$branch` 无 target。
				if !thenHad {
					d.owePort(s.ID, "true")
					thenSrcs = nil
				}
				if !elseHad {
					d.owePort(s.ID, "false")
					elseSrcs = nil
				}
				current = append(append([]string{}, thenSrcs...), elseSrcs...)
			} else if len(thenSrcs) > 0 && len(elseSrcs) > 0 {
				d.addNode(s.Merge)
				for _, tid := range thenSrcs {
					d.edges = append(d.edges, Edge{SourceNodeID: tid, TargetNodeID: s.Merge.ID, SourcePortID: branchSrcPort(thenPort, tid)})
				}
				for _, eid := range elseSrcs {
					d.edges = append(d.edges, Edge{SourceNodeID: eid, TargetNodeID: s.Merge.ID, SourcePortID: branchSrcPort(elsePort, eid)})
				}
				current = []string{s.Merge.ID}
			} else if len(thenSrcs) > 0 {
				// 仅 then 分支有出口（else 分支 flow-end）：透传。
				d.recordSkippedMerge(s.Merge)
				current = thenSrcs
			} else if len(elseSrcs) > 0 {
				// 仅 else 分支有出口（then 分支 flow-end）：透传。
				d.recordSkippedMerge(s.Merge)
				current = elseSrcs
			} else {
				// 两分支都以 flow-end return 终止（主流程 return 语义）：
				// 无分支出口汇入 —— merge 无入边，不投影（幽灵节点），
				// 后续语句不可达（dead-path cut）。
				current = []string{}
			}
			port = ""
		case *ir.AttemptStmt:
			d.addNode(s.Op)
			d.connect(current, s.ID, port, false)
			// Success branch first (its terminal is emitted before the
			// failure branch — legacy build order).
			//
			// aggEdges = 两臂各自**真实臂尾**（不只臂端 End）—— Exit φ 的入边
			// 与 QaStmt/SwitchStmt 的 Merge 同源（臂尾 → 汇聚单元）；空臂按同
			// 一条口径欠它自己的端口边（coze-biz `renderAttempt` 的
			// `addPort(opID, merge.ID, port)` 是同一事实的另一种投影）。
			var tails []string
			aggEdges := []aggEdge{}
			succExits, _, succHad, serr := d.deriveBlockDepth(s.Success, []string{s.ID}, "source", depth+1)
			if serr != nil {
				return nil, port, true, serr
			}
			// 臂端 End（`SuccessEnd`）是**控制流载体**：只有「臂里真有节点渲染
			// 出来」时才物化它（臂尾 → 臂端）。渲染不出节点的臂**不物化**臂端：
			//   ① 没有任何路径进入它 ⇒ runtime 静态检查 `RUNREACHABLE_NODE`
			//      拒收（一手：`100007 (end) has no incoming edges`）；
			//   ② 语料事实：`processType=3` 节点的端口边**没有一个指向 end**
			//      （9/9 都指向具体节点）⇒ 空臂的端口边必须落到出口 φ。
			//      协作渲染器同解：`if !succHasEntry { addPort(opID, merge.ID, "default") }`
			//      —— 同一事实的两处投影必须同解。
			if s.SuccessEnd != nil && succHad {
				d.addNode(s.SuccessEnd)
				tails = append(tails, s.SuccessEnd.ID)
				for _, tid := range succExits {
					d.edges = append(d.edges, Edge{SourceNodeID: tid, TargetNodeID: s.SuccessEnd.ID})
					aggEdges = append(aggEdges, aggEdge{src: tid})
				}
			} else if succHad {
				tails = append(tails, succExits...)
				for _, tid := range succExits {
					aggEdges = append(aggEdges, aggEdge{src: tid})
				}
			} else if s.Exit == nil {
				// merge-less（无出口 φ）：端口边没有落点 ⇒ 记义务（同 if/Qa/switch）。
				d.owePort(s.ID, "source")
			} else {
				aggEdges = append(aggEdges, aggEdge{src: s.ID, port: "source"})
			}
			failExits, _, failHad, ferr := d.deriveBlockDepth(s.Failure, []string{s.ID}, "fail-branch", depth+1)
			if ferr != nil {
				return nil, port, true, ferr
			}
			if s.FailureEnd != nil && failHad {
				d.addNode(s.FailureEnd)
				tails = append(tails, s.FailureEnd.ID)
				for _, tid := range failExits {
					d.edges = append(d.edges, Edge{SourceNodeID: tid, TargetNodeID: s.FailureEnd.ID})
					aggEdges = append(aggEdges, aggEdge{src: tid})
				}
			} else if failHad {
				tails = append(tails, failExits...)
				for _, tid := range failExits {
					aggEdges = append(aggEdges, aggEdge{src: tid})
				}
			} else if s.Exit == nil {
				d.owePort(s.ID, "fail-branch")
			} else {
				aggEdges = append(aggEdges, aggEdge{src: s.ID, port: "fail-branch"})
			}
			// Exit φ（`AttemptStmt.Exit`）—— 与 QaStmt / SwitchStmt 的 `s.Merge`
			// **同一条三分支投影规则**（nil ⇒ merge-less 透传臂尾；有臂尾 ⇒ 汇聚
			// 单元落地并续接；无臂尾 ⇒ 死路）。此前本宿主**只**投影 `s.Op` 与两条
			// 臂端 `End`，从不投影 `Exit` ⇒ 该 φ 的 id 被分配、臂外读提升变量的
			// 消费点（`if(failed || res…)` 条件 / 后续语句的 ref）指向它，产物里
			// 却没有这个节点 ⇒ runtime 静态检查 `RDANGLING_REF` 拒收（一手语料：
			// SC177 / SC193 / SC194 / SC197 / S225；节点 id 相邻两号、5 条引用）。
			if s.Exit == nil {
				current = tails
			} else if len(aggEdges) > 0 {
				d.addNode(s.Exit)
				for _, e := range aggEdges {
					if e.src == "" {
						continue
					}
					d.edges = append(d.edges, Edge{SourceNodeID: e.src, TargetNodeID: s.Exit.ID, SourcePortID: e.port})
				}
				current = []string{s.Exit.ID}
			} else {
				current = []string{}
			}
			port = ""
		case *ir.LoopStmt:
			ex, eerr := d.deriveLoop(s, current, port, depth+1)
			if eerr != nil {
				return nil, port, true, eerr
			}
			current = []string{s.ID}
			port = ""
			_ = ex
		case *ir.ParallelStmt:
			junction := []string{}
			for _, br := range s.Branches {
				brExits, _, _, berr := d.deriveBlockDepth(br, current, port, depth+1)
				if berr != nil {
					return nil, port, true, berr
				}
				// P5 #4: a branch that ends in a flow-end return (FlowEnd)
				// already wired its tail straight to the canonical end via
				// the ReturnStmt.End node; it must NOT re-enter the junction
				// (the barrier does not wait for it, and the end node is a
				// terminal — feeding it into the continuation would create
				// an edge out of a leaf). The junction holds only the
				// non-terminating branch tails.
				if len(brExits) == 1 && d.isEnd(brExits[0]) {
					continue
				}
				junction = append(junction, brExits...)
			}
			current = junction
			// parallel 是 AND 栅栏（P5）:汇合后无条件继续,不属于任何分支
			// 路径——出口端口必须重置。legacy emitParallel 返回 firstPort
			// 是线程端口贯通遗留:该端口只作用于同块下一条语句的入边
			// （所有调用方都丢弃 exitPort）,保留它会把外层分支端口泄漏
			// 到栅栏后的语句（S58:llm --false--> selector,分支门要求
			// llm 发 $branch 而它不发 → run-err）。分支入口端口不受影响
			// （deriveBlockDepth(br, current, port) 已正确带入）。
			port = ""
		case *ir.QaStmt:
			d.addNode(s.QA)
			d.connect(current, s.ID, port, false)
			aggEdges := []aggEdge{}
			for _, br := range s.Branches {
				brExits, _, brHad, berr := d.deriveBlockDepth(br.Body, []string{s.ID}, br.Port, depth+1)
				if berr != nil {
					return nil, port, true, berr
				}
				if len(brExits) == 0 {
					// FlowEnd 终止分支（void return / throw）：直连 end，
					// 不汇入 merge（end 是叶子；无出口分支不参与汇聚）。
					continue
				}
				if !brHad {
					// 渲染不出节点的臂：有汇聚单元时端口边落汇聚；merge-less
					// 时**没有落点** ⇒ 记义务（与 IfStmt 同解）—— 把宿主当裸尾
					// 塞进 current 会被 connect 成一条无端口边，端口标签消失，
					// 运行到该臂即 `$branch "x" has no declared target`。
					if s.Merge == nil {
						d.owePort(s.ID, br.Port)
					} else {
						aggEdges = append(aggEdges, aggEdge{src: s.ID, port: br.Port})
					}
					continue
				}
				for _, id := range brExits {
					aggEdges = append(aggEdges, aggEdge{src: id, port: ""})
				}
			}
			if s.Merge == nil {
				// junction 已被 IR 优化阶段删除（零候选：所有臂都不产出值
				// ⇒ 本宿主**没有值**）⇒ 按 merge-less 投影，与 IfStmt 的
				// `s.Merge == nil` 分支同型：真实臂尾透传给续接消费者，
				// 空臂把宿主自身留在 current（其端口边由下一条语句/外层
				// 消费者续接；P4 端口边只换目标、不消失）。
				// 注：merge-less 时宿主端口标签不逐端口回填（flow/dify/
				// coze-studio 侧适配缺口，见 docs/COZE-EXPRESSIVENESS.md）。
				current = mergeLessExits(aggEdges)
			} else if len(aggEdges) > 0 {
				d.addNode(s.Merge)
				for _, e := range aggEdges {
					d.edges = append(d.edges, Edge{SourceNodeID: e.src, TargetNodeID: s.Merge.ID, SourcePortID: e.port})
				}
				current = []string{s.Merge.ID}
			} else {
				current = []string{}
			}
			port = ""
		case *ir.SwitchStmt:
			// 与 QaStmt 同构：IntentDetector 节点 + branch_N/default 扇出 +
			// VariableAggregator 汇聚（结构化 intent 的隐含汇聚点）。
			d.addNode(s.Switch)
			d.connect(current, s.ID, port, false)
			iaggEdges := []aggEdge{}
			for _, br := range s.Branches {
				brExits, _, brHad, berr := d.deriveBlockDepth(br.Body, []string{s.ID}, br.Port, depth+1)
				if berr != nil {
					return nil, port, true, berr
				}
				if len(brExits) == 0 {
					// FlowEnd 终止分支（主流程分支 return / throw）：
					// 直连 end 900001，不汇入 merge（end 是叶子，且
					// void FlowEnd 分支的 brHad 因 end 已存在可能为
					// false —— 必须用 brExits 判定终止，否则 selector
					// 端口被误判为空分支直连 merge，产生幽灵入边）。
					continue
				}
				if !brHad {
					// 同 QaStmt：merge-less 时宿主不是这条端口边的落点 —— 记义务。
					// 一手实测（2026-09-28）：intent 空臂 + 续接语句，旧投影给
					// `Switch -[]-> 续接节点`（无端口）而 `branch_N` 无 target ⇒
					// runtime `RSPEC_INVALID: $branch "branch_1" has no declared
					// target (have [branch_0 default])`。
					if s.Merge == nil {
						d.owePort(s.ID, br.Port)
					} else {
						iaggEdges = append(iaggEdges, aggEdge{src: s.ID, port: br.Port})
					}
					continue
				}
				for _, id := range brExits {
					iaggEdges = append(iaggEdges, aggEdge{src: id, port: ""})
				}
			}
			if s.Merge == nil {
				// 同 QaStmt：junction 被 IR 优化阶段删掉 ⇒ merge-less 投影。
				// HEAD 在此处对 nil Merge 直接 `d.addNode(s.Merge)` ⇒
				// `(*Aggregate).NodeID` 空指针 panic（2026-09-11 实测：
				// `switch (x) { "a": { http } } <续接语句>` 编译 dify-1.16 /
				// coze-1.0 直接崩）。规则只换投影形状，不得让共享投影器崩。
				current = mergeLessExits(iaggEdges)
			} else if len(iaggEdges) > 0 {
				d.addNode(s.Merge)
				for _, e := range iaggEdges {
					d.edges = append(d.edges, Edge{SourceNodeID: e.src, TargetNodeID: s.Merge.ID, SourcePortID: e.port})
				}
				current = []string{s.Merge.ID}
			} else {
				current = []string{}
			}
			port = ""
		case *ir.ReturnStmt:
			if s.Implicit {
				// synthesized trailing return: wires the full junction
				// (un-merged thenIDs + selector) into the terminal.
				entryNow := current
				if d.pendingIf != nil {
					p := d.pendingIf
					d.pendingIf = nil
					entryNow = append(append([]string{}, p.thenExits...), p.ifStmt.ID)
				}
				// FlowEnd 分支出口（end 节点）不得作为后续语句/合成 end
				// 的前驱：end 是叶子。过滤后再接线。
				entryNow = filterEnds(entryNow, d)
				if s.End != nil {
					preds, entry := d.exitWireTarget(s.End, entryNow)
					d.addNode(s.End)
					// 空 junction（两臂都渲染不出的 if 打头）也要落地端口义务：
					// 那时 preds 为空，义务边是唯一的控制边（P4）。
					d.drainPorts(entry)
					if entry != "" && len(preds) > 0 {
						d.connect(preds, entry, port, false)
					}
					current = []string{s.End.ID}
				} else {
					current = entryNow
				}
				port = ""
				break
			}
			producer := current
			if id, ok := s.MaterializedProducer(); ok && len(producer) > 0 && producer[len(producer)-1] != id {
				producer = []string{id}
			}
			if s.End != nil {
				preds, entry := d.exitWireTarget(s.End, producer)
				d.addNode(s.End)
				d.drainPorts(entry) // 同上：空 junction 的端口义务必须落地
				if entry != "" && len(preds) > 0 {
					d.connect(preds, entry, port, false)
				}
				if s.FlowEnd {
					// P5 #4 + 主流程分支 return（ncoda 语义：主流程任何
					// return 都回到唯一 end 900001）：flow-end 终止当前
					// 路径 —— 后续语句不可达（dead-path cut），分支出口
					// 不进入 merge/junction（end 是叶子，汇入会产生
					// end 出边 / 幽灵节点）。
					current = []string{}
				} else {
					current = []string{s.End.ID}
				}
			} else {
				// subscope return: the value flows upward; the enclosing
				// block's end is created by its consumer.
				current = producer
			}
			port = ""
		case *ir.BreakStmt:
			d.addNode(s.Node)
			d.connect(current, s.Node.NodeID(), port, false)
			current = []string{s.Node.NodeID()}
			port = ""
		case *ir.ContinueStmt:
			d.addNode(s.Node)
			d.connect(current, s.Node.NodeID(), port, false)
			current = []string{s.Node.NodeID()}
			port = ""
		case *ir.InlineStmt:
			brExits, _, _, ierr := d.deriveBlockDepth(s.Body, current, port, depth+1)
			if ierr != nil {
				return nil, port, true, ierr
			}
			if d.pendingIf != nil {
				// Inline body boundary: a trailing else-less if inside the
				// inline body falls through to the statement after the call
				// (fold merged then-exits + selector into the inline's exit
				// junction, selector edge on the false port). Leaking it to
				// the caller would scramble the enclosing block's next
				// statement or else-branch entry (regression: statement
				// helper calls in loop/if bodies producing
				// branch_port_not_connected).
				p := d.pendingIf
				d.pendingIf = nil
				seen := map[string]bool{p.ifStmt.ID: true}
				for _, id := range brExits {
					seen[id] = true
				}
				for _, id := range p.thenExits {
					if id == p.ifStmt.ID || seen[id] || d.isBreakish(id) {
						continue
					}
					seen[id] = true
					brExits = append(brExits, id)
				}
				brExits = append(brExits, p.ifStmt.ID)
				port = "false"
			}
			current = brExits
		default:
			return nil, port, true, &ir.UnsupportedError{Construct: fmt.Sprintf("derive statement %T", st)}
		}
	}
	return current, port, len(d.nodes) > nodeCount || len(d.edges) > edgeCount, nil
}

// deriveLoop derives the loop container: the body subgraph (child nodes +
// inner edges) plus the container's entry/exit, fan-out and loop-output
// edges, exactly like emitLoopNode.
func (d *deriveCtx) deriveLoop(s *ir.LoopStmt, prevIDs []string, firstPort string, depth int) ([]string, error) {
	// parallel for → coze batch 容器（batch-function-inline-* 端口）；
	// 普通 for → loop 容器（loop-function-inline-*）。两容器形状相似，
	// 但平台调度语义不同（并发批处理 vs 串行循环）。
	inlineOut, inlineIn := "loop-function-inline-output", "loop-function-inline-input"
	if s.IsParallel {
		inlineOut, inlineIn = "batch-function-inline-output", "batch-function-inline-input"
	}
	nodeStart := len(d.nodes)
	edgeStart := len(d.edges)
	// portMark = 进入本容器时**已有**的端口义务数：它们属于外层 junction
	// （落点是本容器的入口），只有 [portMark:] 之后登记的才归体内。
	portMark := len(d.pendingPorts)
	bodyExits, _, _, err := d.deriveBlockDepth(s.Body, []string{s.ID}, inlineOut, depth+1)
	if err != nil {
		return nil, err
	}
	// Dangling else-less if at the loop tail: merged + selector join the
	// exit junction (legacy emitLoopNode tail handling).
	//
	// The then-exits already surfaced through deriveBlock's return value
	// (current = thenExits), so re-appending them here would emit the
	// loop-back edge twice (regression: X3 duplicate_edge 100013->100008).
	// Seed the seen set with the existing exits and only add the selector.
	if d.pendingIf != nil {
		p := d.pendingIf
		d.pendingIf = nil
		seen := map[string]bool{p.ifStmt.ID: true}
		for _, id := range bodyExits {
			seen[id] = true
		}
		for _, id := range p.thenExits {
			if id == p.ifStmt.ID || seen[id] || d.isBreakish(id) {
				continue
			}
			seen[id] = true
			bodyExits = append(bodyExits, id)
		}
		bodyExits = append(bodyExits, p.ifStmt.ID)
	}
	// Exit edges: junction → container on the inline input port.
	for _, jid := range bodyExits {
		if jid == s.ID || d.isBreakish(jid) {
			continue
		}
		srcPort := ""
		if d.isSelector(jid) {
			srcPort = "false"
		}
		d.edges = append(d.edges, Edge{SourceNodeID: jid, TargetNodeID: s.ID, TargetPortID: inlineIn, SourcePortID: srcPort})
	}
	// Fan-out entry edges: container → every child referencing it.
	childNodes := d.nodes[nodeStart:]
	hasEntry := map[string]bool{}
	for _, e := range d.edges[edgeStart:] {
		if e.SourceNodeID == s.ID {
			hasEntry[e.TargetNodeID] = true
		}
	}
	for _, cn := range childNodes {
		if hasEntry[cn.NodeID()] {
			continue
		}
		if ir.ChildRefsContainer(cn, s.ID) {
			d.edges = append(d.edges, Edge{SourceNodeID: s.ID, TargetNodeID: cn.NodeID(), SourcePortID: inlineOut})
		}
	}
	// 容器尾 drain（协作渲染器的第三个 drain 点）：**体内的**端口义务落到容器上
	// （inline-input 口）。一手对账 = coze-biz 产物同一形状：
	//
	//	100007 --"true"--> 100005 (target_port loop-function-inline-input)
	//
	// 义务不得穿出容器：它是体内节点的端口边，漏到容器外就是「边指向不属于本层的
	// 节点」（runtime 静态拒收 `RSPEC_INVALID: edge from unknown node`）。
	d.drainPortsFrom(portMark, s.ID, inlineIn)

	// Copy before truncating: the slices below share the ctx's backing
	// arrays, and the subsequent connect/addNode appends would silently
	// overwrite them.
	bodyNodes := append([]ir.Node{}, d.nodes[nodeStart:]...)
	bodyEdges := append([]Edge{}, d.edges[edgeStart:]...)
	d.nodes = d.nodes[:nodeStart]
	d.edges = d.edges[:edgeStart]

	// The derived projection builds a fresh container (same id) so
	// Project never mutates the builder's original ir.Loop node.
	loop := &ir.Loop{
		ID:              s.ID,
		InlineName:      s.InlineName,
		VarName:         s.VarName,
		LoopType:        s.LoopType,
		LoopCount:       s.LoopCount,
		InputArrays:     s.InputArrays,
		Inputs:          s.Inputs,
		Types:           s.Types,
		StateTypes:      s.StateTypes,
		StateValues:     s.StateValues,
		OutTypes:        s.OutTypes,
		OutputSources:   s.OutputSources,
		FlattenOutput:   s.FlattenOutput,
		FlattenPorts:    s.FlattenPorts,
		IsParallel:      s.IsParallel,
		ParallelNums:    s.ParallelNums,
		ErrorHandleMode: s.ErrorHandleMode,
		ReturnValue:     s.ReturnValue,
	}
	container := &ProjectedLoop{Loop: loop, Children: bodyNodes, InnerEdges: bodyEdges}
	d.addNode(container)
	d.connect(prevIDs, s.ID, firstPort, false)
	return []string{s.ID}, nil
}

// isBreakish reports whether the node id belongs to a Break/Continue node
// (consults the derivation's collected nodes).
func (d *deriveCtx) isBreakish(id string) bool {
	n, ok := d.idToNode[id]
	if !ok {
		return false
	}
	switch n.(type) {
	case *ir.BreakNode, *ir.ContinueNode:
		return true
	}
	return false
}

// isSelector reports whether the node id belongs to a ir.Selector.
func (d *deriveCtx) isSelector(id string) bool {
	n, ok := d.idToNode[id]
	if !ok {
		return false
	}
	_, isSel := n.(*ir.Selector)
	return isSel
}

// filterEnds drops terminal End ids from a junction (flow-end branch
// exits must never become control-flow predecessors — end is a leaf).
func filterEnds(ids []string, d *deriveCtx) []string {
	out := ids[:0]
	for _, id := range ids {
		if !d.isEnd(id) {
			out = append(out, id)
		}
	}
	return out
}

// isEnd reports whether the node id belongs to a terminal ir.End (the
// canonical 900001 exit or a branch flow-end terminal). End nodes are
// leaves: nothing may consume them as a control-flow predecessor.
func (d *deriveCtx) isEnd(id string) bool {
	n, ok := d.idToNode[id]
	if !ok {
		return false
	}
	_, isEnd := n.(*ir.End)
	return isEnd
}

// recordSkippedMerge 记住被 pass-through 跳过的 merge 的原始 Refs
// （值语义:Group1 = 首非 nil ref）。
func (d *deriveCtx) recordSkippedMerge(m *ir.Aggregate) {
	if m == nil {
		return
	}
	if d.skippedMerge == nil {
		d.skippedMerge = map[string]map[string][]ir.Ref{}
	}
	groups := map[string][]ir.Ref{}
	if len(m.Groups) > 0 {
		for _, group := range m.Groups {
			refs := make([]ir.Ref, len(group.Refs))
			copy(refs, group.Refs)
			groups[group.Name] = refs
		}
	} else {
		refs := make([]ir.Ref, len(m.Refs))
		copy(refs, m.Refs)
		groups["Group1"] = refs
	}
	d.skippedMerge[m.ID] = groups
}

// fixupSkippedMergeRefs 把指向被跳过 merge 的 ref 重写为其值语义等价项。
// merge 被 pass-through 跳过（单分支存活）时,该 merge 不再出现在投影里,
// 但下游 aggregate/end 仍以 {producer: merge, output: "Group1"} 引用它。
// Group1 = merge 的首个非 nil 输入 → 递归解析到首个值 ref 的生产者+端口。
// 只克隆受影响节点,不改 IR 共享对象（投影拥有自己的节点列表）。
func (d *deriveCtx) fixupSkippedMergeRefs() {
	if len(d.skippedMerge) == 0 {
		return
	}
	var resolve func(producer, output string) (string, string)
	resolve = func(producer, output string) (string, string) {
		groups, ok := d.skippedMerge[producer]
		if !ok {
			return producer, output
		}
		refs := groups[output]
		if len(refs) == 0 {
			refs = groups["Group1"]
		}
		if len(refs) == 0 {
			return producer, output
		}
		return resolve(refs[0].Producer, refs[0].Output)
	}
	rewriteRefs := func(refs []ir.Ref) ([]ir.Ref, bool) {
		out := make([]ir.Ref, len(refs))
		copy(out, refs)
		changed := false
		for j, r := range refs {
			np, no := resolve(r.Producer, r.Output)
			if np != r.Producer || no != r.Output {
				out[j] = ir.Ref{Producer: np, Output: no}
				changed = true
			}
		}
		return out, changed
	}
	for i, n := range d.nodes {
		switch v := n.(type) {
		case *ir.Aggregate:
			cl := *v
			changed := false
			if newRefs, refsChanged := rewriteRefs(v.Refs); refsChanged {
				cl.Refs = newRefs
				changed = true
			}
			if len(v.Groups) > 0 {
				cl.Groups = make([]ir.AggregateGroup, len(v.Groups))
				for gi, group := range v.Groups {
					cl.Groups[gi] = group
					if newRefs, groupChanged := rewriteRefs(group.Refs); groupChanged {
						cl.Groups[gi].Refs = newRefs
						changed = true
					}
				}
			}
			if changed {
				d.nodes[i] = &cl
			}
		case *ir.End:
			if newRefs, changed := rewriteRefs(v.Inputs); changed {
				cl := *v
				cl.Inputs = newRefs
				d.nodes[i] = &cl
			}
		}
	}
}

// foldPendingIf 把悬挂 else-less if 的 junction（then 尾 + selector,selector
// 的未匹配路径也流出）并入退出列表。嵌套场景:内层 else-less if 的 then 尾
// 与 selector 折入外层 else-less if 的 thenExits,消费时两个 selector 都发
// false 端口边（connect 的 per-selector 打端口）。
func (d *deriveCtx) foldPendingIf(tails []string) []string {
	p := d.pendingIf
	if p == nil {
		return tails
	}
	d.pendingIf = nil
	seen := map[string]bool{p.ifStmt.ID: true}
	for _, id := range tails {
		seen[id] = true
	}
	merged := make([]string, 0, len(tails)+len(p.thenExits)+1)
	merged = append(merged, tails...)
	for _, id := range p.thenExits {
		if id == p.ifStmt.ID || seen[id] || d.isBreakish(id) || d.isEnd(id) {
			continue
		}
		seen[id] = true
		merged = append(merged, id)
	}
	return append(merged, p.ifStmt.ID)
}

// mergeLessExits projects a merge-less branch host: its junction was pruned by
// the IR optimizer because no arm produces a value, so the host has no value.
// Real arm tails continue to the enclosing consumer; an arm that rendered no
// entry node contributes the HOST id (src == host) so its port obligation is
// settled by the next consumer instead of vanishing (P4 — a branch port edge
// never disappears, it only changes target). Same treatment as the IfStmt
// `s.Merge == nil` path.
func mergeLessExits(srcs []aggEdge) []string {
	out := make([]string, 0, len(srcs))
	for _, e := range srcs {
		if e.src == "" {
			continue
		}
		out = append(out, e.src)
	}
	return out
}

// aggEdge is one incoming edge of a branch host's junction: either a real arm
// tail (port "") or the host itself carrying the arm's port obligation for an
// arm that rendered no entry node (port = the platform port name).
type aggEdge struct{ src, port string }
