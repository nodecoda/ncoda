package spec

// build.go — IR+Projection → spec.v1 conversion, normalization, hashing
// and dataflow-source extraction.

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/llm"
)

// DeriveTimeoutAxes computes the production axis map for a projection.
//
// The axis is **not** an IR fact (ES-105: `ir.ExceptionConfig` must not grow an
// axis field), so the caller supplies it. For sources that carry no artifact
// sidecar this function is the single derivation:
//
//   - http with a declared timeout ⇒ per_attempt. The physical carrier of an
//     author-written `with timeout(D)` on an http node is `setting.timeout`,
//     which the platform applies **per attempt** (ES-023b/ES-100).
//   - http without a declared timeout ⇒ total. Its default carrier is
//     `settingOnError.timeoutMs` (a shared budget) and the default budget is
//     only meaningful once retries exist (ES-113).
//   - every other family ⇒ total (`settingOnError.timeoutMs` is shared across
//     attempts; code already executes it that way).
//
// Known boundary: when an export put its budget in `settingOnError.timeoutMs`
// *and* the node retries, IR cannot tell that apart from `setting.timeout`
// (both land in `Exception.TimeoutMS`). Per-node overrides from an artifact
// sidecar are the authoritative source for those cases and win over this
// derivation once the read path supplies them (P5 conformance).
func DeriveTimeoutAxes(p *flow.Projection) map[string]ExceptionTimeoutAxis {
	if p == nil {
		return nil
	}
	axes := map[string]ExceptionTimeoutAxis{}
	var walk func(nodes []ir.Node)
	walk = func(nodes []ir.Node) {
		for _, n := range nodes {
			id := n.NodeID()
			if hc, ok := n.(*ir.HTTPCall); ok {
				if hc.Exception != nil && hc.Exception.TimeoutMS > 0 {
					axes[id] = TimeoutAxisPerAttempt
				} else {
					axes[id] = TimeoutAxisTotal
				}
				continue
			}
			axes[id] = TimeoutAxisTotal
			if loop, ok := n.(*flow.ProjectedLoop); ok {
				walk(loop.Children)
			}
		}
	}
	walk(p.Nodes)
	return axes
}

// ExceptionTimeoutAxis names one timeout axis for a node. It is carried in
// BuildOptions now (G4 dataflow) but deliberately not consumed until P3.
type ExceptionTimeoutAxis string

const (
	TimeoutAxisPerAttempt ExceptionTimeoutAxis = "per_attempt"
	TimeoutAxisTotal      ExceptionTimeoutAxis = "total"
	TimeoutAxisNone       ExceptionTimeoutAxis = "none"
)

// BuildOptions carries execution-semantics projection facts that are not
// derivable from the IR graph alone.
//
// TimeoutAxes maps a node id to the semantics of its timeout carrier
// (ES-022a, P3). It is projected into ExceptionConfig.TimeoutAxis for every
// node kind that carries an exception stanza, and into
// HTTPConfig.SettingTimeoutSec for the http per-attempt carrier. Missing
// entries mean "none" (no node-level timeout). An invalid axis value or an id
// that is not a node of the graph fails Build: projection is keyed by node id,
// so a typo would otherwise be silently dropped.
type BuildOptions struct {
	SemanticsVersion string
	Provenance       []ProvenanceRow
	TimeoutAxes      map[string]ExceptionTimeoutAxis
}

// Build derives the execution-subset spec from the structured IR graph and
// its backend projection. The projection carries the flattened edge set
// (incl. loop containers with nested children/inner edges) and the IR
// carries entry params + conversation vars.
//
// The signature is variadic so every existing call site keeps compiling.
// Build derives timeout provenance rows from the IR itself and lets explicit
// BuildOptions rows override them on (node, field) conflicts.
func Build(g *ir.Graph, p *flow.Projection, opts ...BuildOptions) (*Spec, error) {
	if g == nil || p == nil {
		return nil, fmt.Errorf("spec: build: nil graph or projection")
	}
	if len(opts) > 1 {
		return nil, fmt.Errorf("spec: build: at most one BuildOptions, got %d", len(opts))
	}
	var bo BuildOptions
	if len(opts) == 1 {
		bo = opts[0]
	}
	s := &Spec{Version: Version, Mode: g.Mode, SemanticsVersion: bo.SemanticsVersion}
	if g.Entry != nil {
		s.Entry = &EntrySpec{ID: g.Entry.ID}
		for _, pm := range g.Entry.Params {
			s.Entry.Params = append(s.Entry.Params, ParamSpec{
				Name: pm.Name, Type: fromType(pm.Type), Optional: pm.Optional, Default: pm.Default,
			})
		}
	}
	for _, cv := range g.ConversationVars {
		s.ConvVars = append(s.ConvVars, ConvVarSpec{Name: cv.Name, Type: fromType(cv.Type), Default: cv.Default})
	}
	// 平台端口归一化:parallel for 投影为 coze batch 容器时内边端口是
	// "batch-function-inline-*"（平台形状,见 flow.Project deriveLoop）;
	// runtime 契约只认规范 "loop-function-inline-*"（PortLoopInline*）。
	// 在 spec 层归一化（spec 是执行子集的唯一契约,平台无关）——否则并行
	// loop 的 loop-back 边不被吸收,body 无 exit junction（RLOOP_NO_EXIT）。
	p = normalizeLoopPorts(p)
	nodes, edges, err := shortcutLoopReturns(p)
	if err != nil {
		return nil, err
	}
	// else-less if 中段汇合:selector 的 false 端口边与 then 分支尾的普通边
	// 汇入同一个续节点 T,使 T 成为直接分支目标 —— runtime 分支门数据隔离
	// 把 T 的 refs 改成走 echo（拿不到 then 分支产物,S147 全 nil）且跨分支
	// 普通边被 drop（X245 true 路径 dead-end）。物化显式 merge M:
	// then 尾 → M、S --false--> M、M → T,使 M 成为分支目标、T 变普通节点
	// （refs 走数据边正常解析）。与 with-else 的 merge 形状对齐。
	nodes, edges, err = elseLessJunctionMerge(nodes, edges)
	if err != nil {
		return nil, err
	}
	if err := validateTimeoutAxes(nodes, bo.TimeoutAxes); err != nil {
		return nil, err
	}
	// N2 (docs/NCODA-OUTPUT-MODEL.md §1/§2/§5): `output(expr)` is a
	// non-terminating message; it is never the flow's structured exit value.
	// The shared IR's implicit end may still carry the emitted text as an
	// input ref because at least two consumers read it (the runtime's
	// advanced-chat turn answer and the reference target's Answer projection); for every
	// other mode the runtime exit must not bind it. Drop those refs at the
	// projection boundary — the same rule the platform writer applies
	// (cozebiz.dropUnrepresentableEndInputs) — and say so here instead of
	// silently discarding the fact: the IR keeps it, this surface does not.
	var outputExitRefs map[string]bool
	if g.Mode != "advanced-chat" {
		outputExitRefs = map[string]bool{}
		for _, n := range nodes {
			if _, ok := n.(*ir.Output); ok {
				outputExitRefs[n.NodeID()] = true
			}
		}
	}
	for _, n := range nodes {
		ns, err := fromNodeWithAxes(n, bo.TimeoutAxes, outputExitRefs)
		if err != nil {
			return nil, err
		}
		s.Nodes = append(s.Nodes, ns)
	}
	for _, e := range edges {
		s.Edges = append(s.Edges, EdgeSpec{
			Source: e.SourceNodeID, Target: e.TargetNodeID,
			SourcePort: e.SourcePortID, TargetPort: e.TargetPortID,
		})
	}
	derived := codeTimeoutProvenance(nodes)
	prov, err := MergeProvenance(derived, bo.Provenance)
	if err != nil {
		return nil, fmt.Errorf("spec: build: provenance: %w", err)
	}
	s.Provenance = prov
	return s, nil
}

// codeTimeoutProvenance derives the timeout_ms provenance row for every code
// node. Presence of the `settingOnError` stanza is the wire fact: a present
// stanza is wire-present (even when the declared value is 0), an absent one
// is wire-absent (PRD §4.3 / ES-020 / ES-021). The evidence pointer names the
// IR join key, never a hand-written second definition.
func codeTimeoutProvenance(nodes []ir.Node) []ProvenanceRow {
	var rows []ProvenanceRow
	for _, n := range nodes {
		cr, ok := n.(*ir.CodeRun)
		if !ok || cr == nil {
			continue
		}
		field := "nodes." + cr.ID + ".config.exception.timeout_ms"
		row := ProvenanceRow{
			Node:     cr.ID,
			Field:    field,
			Origin:   OriginWireAbsent,
			Evidence: "observed:ir:" + cr.ID + "#settingOnError.absent",
		}
		if cr.Exception != nil {
			row.Origin = OriginWirePresent
			row.Evidence = "observed:ir:" + cr.ID + "#settingOnError.timeoutMs"
		}
		rows = append(rows, row)
	}
	return rows
}

// shortcutLoopReturns rewrites the projection around loops that carry a
// return value (M17, 46_loop_escape). A loop whose body contains a
// `return x` exposes returned/return_value output ports; the runtime must
// short-circuit to the return value instead of unconditionally running the
// normal post-loop tail (the function's default return). The rewrite
// inserts, after the loop:
//
//	loop ──→ selector(loop.returned == true)
//	           true  ──→ aggregate(return_value | tail)
//	           false ──→ tail node ──→ aggregate
//	                                ──→ tail's successors
//
// The aggregate picks the first non-zero ref (return_value when the loop
// returned early, the tail node's output otherwise; an early return of a
// zero value still wins because the gate skips the tail arm, so every other
// candidate is also zero); the branch gate
// skips the tail node on the true path. This mirrors the reference-target render
// __returning / __return_value escape (emitProjectedExitfulFor /
// emitProjectedWhile). The loop's direct out edge is absorbed.
// elseLessJunctionMerge 把 else-less if 的中段汇合物化为显式 merge。
//
// 形状识别:selector S 有 false 端口边 S--false-->T,且 T 还有来自 S 分支
// 区域（从 S 可达、非 S 直接 target）的普通入边 X→T —— 这是 pendingIf
// junction（then 尾 + selector false 端口都进入续节点）。重写:
//
//	X → M、S --false--> M、M → T（原 X→T 边改写为 X→M）
//
// M 是 ir.Aggregate（Group1 = 首个非 nil 尾值,续节点不读,纯控制 join）。
// 注意:只处理 T 为普通节点的情况;T 为 End 时分支门已豁免（S32/S200）。
func elseLessJunctionMerge(nodes []ir.Node, edges []flow.Edge) ([]ir.Node, []flow.Edge, error) {
	byID := map[string]ir.Node{}
	isSel := map[string]bool{}
	for _, n := range nodes {
		byID[n.NodeID()] = n
		if _, ok := n.(*ir.Selector); ok {
			isSel[n.NodeID()] = true
		}
	}
	// 入边索引:node → plain 入边源
	plainIn := map[string][]string{}
	portOut := map[string][]flow.Edge{} // selector → port 出边
	for _, e := range edges {
		if e.SourcePortID != "" {
			portOut[e.SourceNodeID] = append(portOut[e.SourceNodeID], e)
			continue
		}
		plainIn[e.TargetNodeID] = append(plainIn[e.TargetNodeID], e.SourceNodeID)
	}
	// reachable:从 S 沿所有边（排除 T 防环）可达的节点集。
	reachable := func(s, excludeT string) map[string]bool {
		seen := map[string]bool{}
		var walk func(id string)
		walk = func(id string) {
			if seen[id] {
				return
			}
			seen[id] = true
			for _, e := range edges {
				if e.SourceNodeID != id || e.TargetNodeID == excludeT {
					continue
				}
				walk(e.TargetNodeID)
			}
		}
		walk(s)
		return seen
	}
	// 收集重写:T → (M, 尾节点列表)
	type junction struct{ sel, target, agg string }
	rewrites := map[string]junction{} // targetID → junction
	seenAgg := map[string]bool{}
	// 按确定顺序遍历节点,避免 map 迭代随机性。
	selIDs := make([]string, 0, len(isSel))
	for id := range isSel {
		selIDs = append(selIDs, id)
	}
	sort.Strings(selIDs)
	for _, sid := range selIDs {
		for _, e := range portOut[sid] {
			if e.SourcePortID != "false" || e.TargetNodeID == "" {
				continue
			}
			t := e.TargetNodeID
			if _, ok := byID[t]; !ok {
				continue // 外部节点（未物化）——跳过
			}
			if _, isE := byID[t].(*ir.End); isE {
				// End 是叶子（900001）,分支门已豁免 End 目标（S32/S200）;
				// 物化 merge 会引入 M→End 且改写 then 尾 X→M,破坏
				// 已确定的 End 直连形状,跳过。
				continue
			}
			if _, isA := byID[t].(*ir.Aggregate); isA {
				continue // 已是 merge（with-else 形状）
			}
			if _, done := rewrites[t]; done {
				continue
			}
			// T 是否有来自 S 分支区域的普通入边
			region := reachable(sid, t)
			var tails []string
			for _, x := range plainIn[t] {
				if region[x] && !isSel[x] {
					tails = append(tails, x)
				}
			}
			if len(tails) == 0 {
				continue
			}
			sort.Strings(tails)
			aggID := "elseless_merge_" + sid
			if seenAgg[aggID] {
				// 同一 S 多个 false target（罕见）——避免 ID 冲突
				aggID = aggID + "_" + t
			}
			seenAgg[aggID] = true
			rewrites[t] = junction{sel: sid, target: t, agg: aggID}
		}
	}
	if len(rewrites) == 0 {
		return nodes, edges, nil
	}
	// 应用:重建 edges（改写 X→T 为 X→M,插入 S--false-->M 与 M→T）,
	// 并把 M 追加到 nodes。
	aggIDs := map[string]junction{}
	for _, j := range rewrites {
		aggIDs[j.agg] = j
	}
	var out []flow.Edge
	for _, e := range edges {
		if j, ok := rewrites[e.TargetNodeID]; ok && e.SourcePortID == "" && e.SourceNodeID != j.sel {
			// 分支区域普通入边 → M
			out = append(out, flow.Edge{SourceNodeID: e.SourceNodeID, TargetNodeID: j.agg})
			continue
		}
		if j, ok := rewrites[e.TargetNodeID]; ok && e.SourcePortID == "false" && e.SourceNodeID == j.sel {
			// S --false--> M
			out = append(out, flow.Edge{SourceNodeID: e.SourceNodeID, TargetNodeID: j.agg, SourcePortID: "false"})
			continue
		}
		out = append(out, e)
	}
	for aggID, j := range aggIDs {
		// 尾节点 refs（Group1 值无人消费,取 "output" 端口;nil 无害）
		var refs []ir.Ref
		for _, x := range plainIn[j.target] {
			if _, ok := byID[x]; ok && x != j.sel {
				refs = append(refs, ir.Ref{Producer: x, Output: "output"})
			}
		}
		sort.SliceStable(refs, func(a, b int) bool { return refs[a].Producer < refs[b].Producer })
		nodes = append(nodes, &ir.Aggregate{ID: aggID, Refs: refs})
		out = append(out, flow.Edge{SourceNodeID: aggID, TargetNodeID: j.target})
	}
	return nodes, out, nil
}

func shortcutLoopReturns(p *flow.Projection) ([]ir.Node, []flow.Edge, error) {
	nodes := make([]ir.Node, 0, len(p.Nodes)+2)
	edges := make([]flow.Edge, 0, len(p.Edges)+3)
	rewritten := map[string]string{} // loopID → aggregate node ID
	tailToAgg := map[string]string{} // tail node ID (T) → aggregate node ID
	loopOutTo := map[string]string{} // loopID → its direct out target (T)
	for _, n := range p.Nodes {
		loop, ok := n.(*flow.ProjectedLoop)
		if !ok || loop.ReturnValue == nil {
			nodes = append(nodes, n)
			continue
		}
		// find the loop's single direct out edge (loop-back absorbed by
		// the driver; multi-out or branch-port loops stay unsupported —
		// fail fast rather than silently changing semantics).
		var target string
		var targetPort string
		outCount := 0
		for _, e := range p.Edges {
			if e.SourceNodeID != loop.ID || e.TargetNodeID == loop.ID || e.TargetPortID == PortLoopInlineInput {
				continue
			}
			outCount++
			target = e.TargetNodeID
			targetPort = e.SourcePortID
		}
		if outCount != 1 {
			return nil, nil, fmt.Errorf("spec: build: loop %s with return value has %d out edges (want 1)", loop.ID, outCount)
		}
		if targetPort != "" {
			return nil, nil, fmt.Errorf("spec: build: loop %s with return value branches on port %q (unsupported)", loop.ID, targetPort)
		}
		selID := "lr_sel_" + loop.ID
		aggID := "lr_agg_" + loop.ID
		sel := &ir.Selector{ID: selID, Logic: 0, Conditions: []ir.Condition{{
			Left:     ir.Operand{Ref: &ir.Ref{Producer: loop.ID, Output: "returned"}},
			Operator: 1, // equals
			Right:    ir.Operand{Val: true},
		}}}
		nodes = append(nodes, loop, sel)
		// The aggregate merges return_value (r0) with the tail node's
		// output (r1). The tail port is inferred from the end node's
		// inputs when available, else the canonical "output".
		tailPort := "output"
		for _, en := range p.Nodes {
			end, ok := en.(*ir.End)
			if !ok {
				continue
			}
			for _, r := range end.Inputs {
				if r.Producer == target {
					tailPort = r.Output
				}
			}
			for _, o := range end.Outputs {
				if o.Ref.Producer == target {
					tailPort = o.Ref.Output
				}
			}
		}
		agg := &ir.Aggregate{ID: aggID, Refs: []ir.Ref{
			{Producer: loop.ID, Output: "return_value"},
			{Producer: target, Output: tailPort},
		}}
		nodes = append(nodes, agg)
		rewritten[loop.ID] = aggID
		tailToAgg[target] = aggID
		loopOutTo[loop.ID] = target
	}
	if len(rewritten) == 0 {
		// fast path: no loop-return loops — projection unchanged
		for _, e := range p.Edges {
			edges = append(edges, e)
		}
		return nodes, edges, nil
	}
	for _, e := range p.Edges {
		switch {
		case e.TargetPortID == PortLoopInlineInput:
			// loop-back absorbed by the driver — keep the projection edge
			// (spec consumers skip it when building the Eino graph).
			edges = append(edges, e)
		case loopOutTo[e.SourceNodeID] == e.TargetNodeID:
			// loop's direct out edge → rewritten to selector
			selID := "lr_sel_" + e.SourceNodeID
			edges = append(edges, flow.Edge{SourceNodeID: e.SourceNodeID, TargetNodeID: selID})
		case tailToAgg[e.SourceNodeID] != "":
			// tail node's out edges → aggregate's out edges
			edges = append(edges, flow.Edge{SourceNodeID: tailToAgg[e.SourceNodeID], TargetNodeID: e.TargetNodeID, SourcePortID: e.SourcePortID, TargetPortID: e.TargetPortID})
		default:
			edges = append(edges, e)
		}
	}
	// insert the branch edges + tail→aggregate data edge for each rewritten loop
	for loopID, aggID := range rewritten {
		selID := "lr_sel_" + loopID
		target := loopOutTo[loopID]
		edges = append(edges,
			flow.Edge{SourceNodeID: selID, TargetNodeID: aggID, SourcePortID: "true"},
			flow.Edge{SourceNodeID: selID, TargetNodeID: target, SourcePortID: "false"},
			flow.Edge{SourceNodeID: target, TargetNodeID: aggID},
		)
	}
	// end node inputs referencing the tail node → aggregate output
	for _, n := range p.Nodes {
		end, ok := n.(*ir.End)
		if !ok {
			continue
		}
		mutated := false
		for i := range end.Inputs {
			if aggID := tailToAgg[end.Inputs[i].Producer]; aggID != "" {
				end.Inputs[i].Producer = aggID
				end.Inputs[i].Output = "Group1"
				mutated = true
			}
		}
		for i := range end.Outputs {
			if aggID := tailToAgg[end.Outputs[i].Ref.Producer]; aggID != "" {
				end.Outputs[i].Ref.Producer = aggID
				end.Outputs[i].Ref.Output = "Group1"
				mutated = true
			}
		}
		if mutated {
			_ = end // nodes slice already carries the mutation via pointer
		}
	}
	return nodes, edges, nil
}

func fromType(t *ir.Type) *TypeSpec {
	if t == nil {
		return nil
	}
	ts := &TypeSpec{Kind: t.Kind, Required: t.Required}
	if t.Elem != nil {
		ts.Elem = fromType(t.Elem)
	}
	if len(t.Props) > 0 {
		ts.Props = map[string]*TypeSpec{}
		for k, v := range t.Props {
			ts.Props[k] = fromType(v)
		}
	}
	return ts
}

// fromNode dispatches one IR node to its spec config DTO without timeout-axis
// facts (compatibility surface for callers that do not build with
// BuildOptions). Build itself goes through fromNodeWithAxes so every
// exception-carrying kind receives the node id and the axis map.
func fromNode(n ir.Node) (NodeSpec, error) { return fromNodeWithAxes(n, nil, nil) }

// fromNodeWithAxes dispatches one IR node to its spec config DTO. All 19 IR
// kinds are covered; unknown kinds are a hard error (the IR kind set is
// closed).
// outputExitRefs is the set of OutputEmitter node ids the runtime structured
// exit must not bind (nil = keep every ref, e.g. advanced-chat turn answers).
func fromNodeWithAxes(n ir.Node, axes map[string]ExceptionTimeoutAxis, outputExitRefs map[string]bool) (NodeSpec, error) {
	var cfg any
	switch v := n.(type) {
	case *ir.Start:
		sc := &StartConfig{}
		for _, p := range v.Params {
			sc.Params = append(sc.Params, ParamSpec{
				Name: p.Name, Type: fromType(p.Type),
				Optional: p.Optional, Default: p.Default,
			})
		}
		cfg = sc
	case *ir.End:
		ec := &EndConfig{NamedReturn: v.NamedReturn}
		for _, r := range v.Inputs {
			if outputExitRefs[r.Producer] {
				continue // N2: output is a message, not the exit value
			}
			ec.Inputs = append(ec.Inputs, fromRef(r))
		}
		for _, o := range v.Outputs {
			ec.Outputs = append(ec.Outputs, EndOutput{Name: o.Name, Ref: fromRef(o.Ref)})
		}
		if v.Answer != nil {
			ea := &EndAnswerConfig{Template: v.Answer.Template}
			for _, b := range v.Answer.Bindings {
				ea.Bindings = append(ea.Bindings, EndOutput{Name: b.Name, Ref: fromRef(b.Ref)})
			}
			ec.Answer = ea
		}
		cfg = ec
	case *ir.LLMCall:
		lc := &LLMConfig{Model: v.Model, Inputs: fromSources(v.Inputs), OutputFormat: v.OutputFormat, ResponseFormat: v.ResponseFormat, Exception: fromException(v.Exception, v.ID, axes)}
		if v.Result != nil {
			lc.Result = fromLLMResult(v.Result)
		}
		// extract(..., vision: img)：vision 操作数走独立输入口，运行时把它
		// 变成 user 消息上的 image part（而不是混进 prompt 文本）。
		if v.Extraction != nil && v.Extraction.Vision != nil {
			vs := fromSource(*v.Extraction.Vision)
			lc.Vision = &vs
			port := vs
			port.Path = []string{"vision"}
			dup := false
			for _, in := range lc.Inputs {
				if in.BlockID == port.BlockID && in.Name == port.Name && len(in.Path) == 1 && in.Path[0] == "vision" {
					dup = true
					break
				}
			}
			if !dup {
				lc.Inputs = append(lc.Inputs, port)
			}
		}
		cfg = lc
		for _, c := range v.Config {
			cfg.(*LLMConfig).Config = append(cfg.(*LLMConfig).Config, ConfigPair{Key: c.Key, Value: c.Value})
		}
	case *ir.HTTPCall:
		hc := &HTTPConfig{Method: v.Method, URL: v.URL, Inputs: fromSources(v.Inputs), Body: v.Body, Exception: fromException(v.Exception, v.ID, axes)}
		// ES-022a: `setting.timeout` (seconds, per attempt) is the http-only
		// carrier. It is projected only when the axis says per_attempt; the
		// total axis keeps TimeoutMS as its shared budget and never gets a
		// second (stacked) carrier. A non-whole-second carrier would be
		// truncated silently, so it is a hard error.
		if hc.Exception != nil && hc.Exception.TimeoutAxis == string(TimeoutAxisPerAttempt) {
			if ms := hc.Exception.TimeoutMS; ms%1000 != 0 {
				return NodeSpec{}, fmt.Errorf("spec: build: node %s: http per_attempt timeout_ms %d is not a whole number of seconds", v.ID, ms)
			} else if ms > 0 {
				hc.SettingTimeoutSec = ms / 1000
			}
		}
		// P2 host slice: headers/query are ordered wire channels. Project
		// entry-by-entry in author order; template strings stay verbatim
		// (rendering belongs to the runtime host node).
		for _, hv := range v.Headers {
			hc.Headers = append(hc.Headers, HTTPKV{Name: hv.Name, Value: hv.Value})
		}
		for _, pv := range v.Params {
			hc.Params = append(hc.Params, HTTPKV{Name: pv.Name, Value: pv.Value})
		}
		cfg = hc
	case *ir.ToolCall:
		tc := &ToolConfig{ToolName: v.ToolName, ProviderID: v.ProviderID, Operation: v.Operation, Exception: fromException(v.Exception, v.ID, axes)}
		seq := 0
		for _, pm := range v.Params {
			value, refs := liftNestedToolRefs(v.ID, pm.Key, pm.Value, &seq)
			tp := ToolParam{Key: pm.Key, Value: value}
			if pm.Ref != nil {
				r := fromSource(*pm.Ref)
				tp.Ref = &r
			}
			tc.Params = append(tc.Params, tp)
			tc.NestedRefs = append(tc.NestedRefs, refs...)
		}
		cfg = tc
	case *ir.KnowledgeCall:
		cfg = &KnowledgeConfig{DatasetIDs: v.DatasetIDs, DatasetNames: v.DatasetNames, Inputs: fromSources(v.Inputs), QueryName: v.QueryName}
	case *ir.SubWorkflowCall:
		cfg = &SubWorkflowConfig{
			WorkflowID:      v.WorkflowID,
			WorkflowVersion: v.WorkflowVersion,
			SpaceID:         v.SpaceID,
			Inputs:          fromSources(v.Inputs),
			OutTypes:        fromTypeMap(v.OutTypes),
		}
	case *ir.HistoryCall:
		cfg = &ConversationConfig{}
	case *ir.AgentCall:
		ac := &AgentConfig{Model: v.Model, Instruction: v.Instruction, Strategy: v.Strategy, MaxIteration: v.MaxIteration, Memory: v.Memory, MemoryWindow: v.MemoryWindow, Exception: fromException(v.Exception, v.ID, axes)}
		if len(v.KnowledgeDatasetIDs) > 0 {
			ac.Knowledge = &AgentKnowledgeConfig{DatasetIDs: v.KnowledgeDatasetIDs, TopK: v.KnowledgeTopK}
		}
		if v.Query != nil {
			q := fromSource(*v.Query)
			ac.Query = &q
		}
		for _, tw := range v.Tools {
			ac.Tools = append(ac.Tools, AgentToolWire{Alias: tw.Alias, PluginUniqueID: tw.PluginUniqueID, ProviderName: tw.ProviderName, ToolName: tw.ToolName, ProviderType: tw.ProviderType})
		}
		cfg = ac
	case *ir.TextProcess:
		cfg = &TextConfig{Method: v.Method, Template: v.Template, ConcatChar: v.ConcatChar, Separators: v.Separators, Inputs: fromSources(v.Inputs), Answer: v.Answer}
	case *ir.CodeRun:
		// KeepWhole: json.parse<Record> shim（JSONSchema 为 object schema,
		// 非 array/result 单值形态）→ output 端口保留整体 dict 拷贝,否则
		// 单字段 record 被折叠成值,output.<field> dotted ref 断链。
		cfg = &CodeConfig{Code: v.Code, Language: v.Language, Inputs: fromSources(v.Inputs), Types: fromTypeMap(v.Types), OutTypes: fromTypeMap(v.OutTypes), OutOrder: v.OutOrder, Condition: v.Condition, Synthetic: v.Synthetic, KeepWhole: v.Synthetic && v.JSONSchema != nil && v.JSONSchema.Kind == "object", Exception: fromException(v.Exception, v.ID, axes)}
	case *ir.ListOp:
		cfg = &ListOpConfig{Operation: v.Operation, Inputs: fromSources(v.Inputs), FilterKey: v.FilterKey, FilterOp: v.FilterOp, FilterVal: v.FilterVal, OrderKey: v.OrderKey, OrderVal: v.OrderVal, Limit: v.Limit}
	case *ir.Selector:
		sel := &SelectorConfig{Logic: v.Logic}
		for _, c := range v.Conditions {
			sel.Conditions = append(sel.Conditions, Condition{Left: fromOperand(c.Left), Operator: c.Operator, Right: fromOperand(c.Right)})
		}
		cfg = sel
	case *ir.Aggregate:
		acfg := &AggregateConfig{Refs: fromRefs(v.Refs), Groups: fromAggregateGroups(v.Groups)}
		if len(v.Groups) == 0 {
			acfg.LegacyGroupName = v.SingleGroupName()
		}
		cfg = acfg
	case *ir.Assign:
		cfg = &AssignConfig{VarName: v.VarName, Value: fromSource(v.Value), Operation: v.Operation, LoopState: v.LoopState, LoopID: v.LoopID}
	case *ir.Switch:
		sc := &SwitchConfig{Query: fromSource(v.Query), Intents: v.Intents,
			Temperature: v.Temperature, MaxTokens: v.MaxTokens, TopP: v.TopP,
			Mode: v.Mode, SystemPrompt: v.SystemPrompt}
		cfg = sc
	case *ir.QA:
		qa := &QaConfig{Choices: v.Choices, Inputs: fromSources(v.Inputs), Template: v.Template, Timeout: v.Timeout}
		for _, f := range v.Fields {
			qa.Fields = append(qa.Fields, QaField{Name: f.Name, Kind: f.Kind, Options: f.Options})
		}
		cfg = qa
	case *ir.Output:
		cfg = &OutputConfig{Template: v.Template}
	case *ir.BreakNode:
		cfg = &BreakConfig{}
	case *ir.ContinueNode:
		cfg = &ContinueConfig{}
	case *ir.LoopReturnNode:
		cfg = &LoopReturnConfig{Value: fromSource(v.Value)}
	case *flow.ProjectedLoop:
		lc := &LoopConfig{
			VarName: v.VarName, LoopType: v.LoopType, LoopCount: v.LoopCount,
			InputArrays: v.InputArrays, Inputs: fromSources(v.Inputs),
			StateValues:   fromSourceMap(v.StateValues),
			OutputSources: fromSources(v.OutputSources),
			FlattenOutput: v.FlattenOutput, IsParallel: v.IsParallel,
			ParallelNums: v.ParallelNums, ErrorHandleMode: v.ErrorHandleMode,
			Types: fromTypeMap(v.Types), StateTypes: fromTypeMap(v.StateTypes),
			OutTypes: fromTypeMap(v.OutTypes),
		}
		if v.ReturnValue != nil {
			rv := fromSource(ir.Source{BlockID: v.ReturnValue.NodeID(), Name: v.ReturnValue.Output(), Path: []string{v.ReturnValue.Output()}})
			lc.ReturnValue = &rv
		}
		for _, c := range v.Children {
			cs, err := fromNodeWithAxes(c, axes, outputExitRefs)
			if err != nil {
				return NodeSpec{}, err
			}
			lc.Children = append(lc.Children, cs)
		}
		for _, e := range v.InnerEdges {
			lc.InnerEdges = append(lc.InnerEdges, EdgeSpec{
				Source: e.SourceNodeID, Target: e.TargetNodeID,
				SourcePort: e.SourcePortID, TargetPort: e.TargetPortID,
			})
		}
		cfg = lc
	default:
		return NodeSpec{}, fmt.Errorf("spec: build: unsupported IR node type %T", n)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return NodeSpec{}, fmt.Errorf("spec: build: marshal config for %s (%s): %w", n.NodeID(), n.Kind(), err)
	}
	return NodeSpec{ID: n.NodeID(), Kind: string(n.Kind()), Config: raw}, nil
}

func fromLLMResult(r *ir.LLMResultContract) *LLMResultConfig {
	if r == nil {
		return nil
	}
	return &LLMResultConfig{
		PayloadSchema: r.PayloadSchema,
		Metadata: LLMMetadataConfig{
			ReasoningContent: r.Metadata.ReasoningContent,
			Usage: LLMUsageConfig{
				PromptTokens:     r.Metadata.Usage,
				CompletionTokens: r.Metadata.Usage,
				TotalTokens:      r.Metadata.Usage,
			},
			FinishReason: r.Metadata.FinishReason,
		},
		Validation: LLMValidationConfig{
			Mode:          r.Validation.Mode,
			ClosedRecords: r.Validation.ClosedRecord,
			DuplicateKeys: "last-value-wins",
			NonFiniteNums: "reject",
			AttemptScope:  r.Validation.AttemptScope,
		},
	}
}

func defaultLLMResult() *LLMResultConfig {
	return &LLMResultConfig{
		PayloadSchema: llm.String(),
		Validation: LLMValidationConfig{
			Mode: "none", ClosedRecords: true, DuplicateKeys: "last-value-wins",
			NonFiniteNums: "reject", AttemptScope: "model-only",
		},
	}
}

// operandPath mirrors the coze exporter input-path convention for
// selector operands: single condition → ["0","left"/"right"],
// multi-condition → ["0",i,"left"/"right"].
func operandPath(idx int, side string) []string {
	if idx == 0 {
		return []string{"0", side}
	}
	return []string{"0", fmt.Sprintf("%d", idx), side}
}

// liftNestedToolRefs clones a structured tool-argument value tree and, for
// every {path, ref_node} leaf produced by the frontend, adds a marker plus a
// dataflow Source. The marker makes the runtime substitution unambiguous even
// inside arrays (where map-path addressing is not available).
func liftNestedToolRefs(nodeID, paramKey string, value any, seq *int) (any, []Source) {
	switch v := value.(type) {
	case map[string]any:
		rn, hasNode := v["ref_node"].(string)
		path, hasPath := v["path"].(string)
		if hasNode && hasPath && rn != "" && path != "" {
			*seq++
			base := nodeID
			if base == "" {
				base = "tool"
			}
			token := fmt.Sprintf("%s:%s:%d", base, paramKey, *seq)
			clone := make(map[string]any, len(v)+1)
			for k, vv := range v {
				clone[k] = vv
			}
			clone[NestedRefMarker] = token
			src := Source{Path: []string{NestedRefMapKey, token}, BlockID: rn, Name: path}
			return clone, []Source{src}
		}
		clone := make(map[string]any, len(v))
		var refs []Source
		// Deterministic order: Go map iteration is randomized, and the
		// marker token embeds the visit sequence. Without sorting the same
		// source emits different spec bytes run to run (measured: 79/869
		// corpus files drift between two runs of the same binary).
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			cv, cr := liftNestedToolRefs(nodeID, paramKey, v[k], seq)
			clone[k] = cv
			refs = append(refs, cr...)
		}
		return clone, refs
	case []any:
		clone := make([]any, len(v))
		var refs []Source
		for i, vv := range v {
			cv, cr := liftNestedToolRefs(nodeID, paramKey, vv, seq)
			clone[i] = cv
			refs = append(refs, cr...)
		}
		return clone, refs
	default:
		return value, nil
	}
}

func fromRefs(refs []ir.Ref) []Ref {
	out := make([]Ref, 0, len(refs))
	for _, r := range refs {
		out = append(out, fromRef(r))
	}
	return out
}

func fromAggregateGroups(groups []ir.AggregateGroup) []AggregateGroup {
	if len(groups) == 0 {
		return nil
	}
	out := make([]AggregateGroup, 0, len(groups))
	for _, group := range groups {
		out = append(out, AggregateGroup{
			Name: group.Name,
			Refs: fromRefs(group.Refs),
			Type: fromType(group.Type),
		})
	}
	return out
}

func fromRef(r ir.Ref) Ref { return Ref{Producer: r.Producer, Output: r.Output} }

// blockOutputRe matches IR block references inside output templates
// ({{block_output_<id>.<port>}}; port may be a dotted record path).
var blockOutputRe = regexp.MustCompile(`\{\{block_output_([0-9]+)\.([^}]+)\}\}`)

func fromSources(ss []ir.Source) []Source {
	out := make([]Source, 0, len(ss))
	for _, s := range ss {
		out = append(out, fromSource(s))
	}
	return out
}

// fromSource 把 IR 引用降到 runtime 契约的**端口名空间**（`Source.Name` 的
// 唯一写点）。两类名字在这里分道：
//
//   - 产出方**声明的端口名** ⇒ 原样（下游按字段/口名解析）；
//   - IR 的**整值标记**（`ir.WholeValuePortName()`，值由一组字段口承载、平台/运行时
//     没有任何一个口承载整个 record）⇒ 降为空名。运行时的既有约定就是「空名 =
//     整节点值」（`compile.lookupDotted(producer, "")` 返回整个节点输出），与
//     coze-biz 渲染器把整值绑定写成 `path: ""` 是**同一事实的两处投影**。
//
// 不退让的理由（一手，672 源跑批）：标记原名直接穿透时，驱动器按**字段名**去查
// `resolveRef(bodyOut, blockID, "@whole")` ⇒ 查不到 ⇒ 循环收集到 `nil` ⇒ 下游用户
// code 读 `item.code` 崩（`TypeError: Cannot read properties of null (reading 'code')`）。
// 判据 = `internal/compiler/cozebiz_whole_value_runtime_carrier_contract_test.go`。
func fromSource(s ir.Source) Source {
	name := s.Name
	if name == ir.WholeValuePortName() {
		name = ""
	}
	return Source{Path: s.Path, BlockID: s.BlockID, Name: name, Val: s.Val}
}

func fromSourceMap(m map[string]ir.Source) map[string]Source {
	if len(m) == 0 {
		return nil
	}
	out := map[string]Source{}
	for k, v := range m {
		out[k] = fromSource(v)
	}
	return out
}

func fromTypeMap(m map[string]*ir.Type) map[string]*TypeSpec {
	if len(m) == 0 {
		return nil
	}
	out := map[string]*TypeSpec{}
	for k, v := range m {
		out[k] = fromType(v)
	}
	return out
}

func fromOperand(o ir.Operand) Operand {
	op := Operand{Val: o.Val}
	if o.Ref != nil {
		r := fromRef(*o.Ref)
		op.Ref = &r
	}
	return op
}

// fromException projects one IR exception stanza and its timeout axis.
// nodeID is the spec node id (the axis map key) and axes is
// BuildOptions.TimeoutAxes; a missing/empty entry means "none". The axis is
// carried, never inferred from the IR: the same TimeoutMS bytes mean total
// for llm/tool/code and per_attempt for the http setting.timeout carrier.
func fromException(e *ir.ExceptionConfig, nodeID string, axes map[string]ExceptionTimeoutAxis) *ExceptionConfig {
	if e == nil {
		return nil
	}
	ec := &ExceptionConfig{
		MaxRetry: e.MaxRetry, RetryIntervalMS: e.RetryIntervalMS, TimeoutMS: e.TimeoutMS,
		ProcessType: e.ProcessType, FailBranch: e.FailBranch,
		TimeoutAxis: string(axes[nodeID]),
	}
	// ES-046/PT2: `with default(<record>)` 的失败载荷是 IR 事实，必须在
	// spec 里原样出现；运行期据此返回缺省值而不是伪造一个。空载荷保持
	// nil（= 策略在但载荷无法静态携带 ⇒ 运行期 fail loud），不发明空 map。
	for _, f := range e.DefaultFields {
		ec.DataOnErr = append(ec.DataOnErr, DefaultField{Name: f.Name, Value: f.Value})
	}
	return ec
}

// validateTimeoutAxes fails loud on axis facts that cannot be applied (ES-022a):
// a value outside the closed axis set, or an id that is not a node of this
// graph (including loop children). Projection is keyed by node id, so an
// unknown id would otherwise vanish without a diagnostic.
func validateTimeoutAxes(nodes []ir.Node, axes map[string]ExceptionTimeoutAxis) error {
	if len(axes) == 0 {
		return nil
	}
	known := map[string]bool{}
	var collect func([]ir.Node)
	collect = func(ns []ir.Node) {
		for _, n := range ns {
			known[n.NodeID()] = true
			if pl, ok := n.(*flow.ProjectedLoop); ok {
				collect(pl.Children)
			}
		}
	}
	collect(nodes)
	ids := make([]string, 0, len(axes))
	for id := range axes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		switch axis := axes[id]; axis {
		case "", TimeoutAxisNone, TimeoutAxisPerAttempt, TimeoutAxisTotal:
		default:
			return fmt.Errorf("spec: build: node %q: unknown timeout axis %q (want total|per_attempt|none)", id, axis)
		}
		if !known[id] {
			return fmt.Errorf("spec: build: timeout axis targets unknown node %q (not a node of this graph)", id)
		}
	}
	return nil
}

// ── normalization / hashing ──────────────────────────────────────────────

// Normalize decodes every node config by kind and re-marshals it into
// canonical bytes: field order follows the DTO, map keys are sorted by
// encoding/json, unknown/extra fields are dropped. It also validates the
// version and rejects unknown kinds. Idempotent: Normalize(Normalize(s))
// is byte-identical.
func (s *Spec) Normalize() (*Spec, error) {
	if s == nil {
		return nil, fmt.Errorf("spec: missing version")
	}
	if s.Version == "" {
		return nil, fmt.Errorf("spec: missing version")
	}
	if s.Version != VersionV1 && s.Version != VersionV2 {
		return nil, fmt.Errorf("spec: unsupported version %q (want %q or %q)", s.Version, VersionV1, VersionV2)
	}
	legacy := s.Version == VersionV1
	out := &Spec{
		Version:          VersionV2,
		SemanticsVersion: s.SemanticsVersion,
		Provenance:       CloneProvenance(s.Provenance),
		Mode:             s.Mode,
		Entry:            s.Entry,
		ConvVars:         s.ConvVars,
		Edges:            s.Edges,
	}
	if out.SemanticsVersion != "" {
		if err := validateSemanticsVersion(out.SemanticsVersion); err != nil {
			return nil, fmt.Errorf("spec: normalize semantics_version: %w", err)
		}
	}
	if err := ValidateProvenance(out.Provenance); err != nil {
		return nil, fmt.Errorf("spec: normalize provenance: %w", err)
	}
	kinds := make(map[string]string, len(s.Nodes))
	for _, n := range s.Nodes {
		kinds[n.ID] = n.Kind
	}
	for _, n := range s.Nodes {
		cfg, err := decodeConfig(n.Kind, n.Config)
		if err != nil {
			return nil, fmt.Errorf("spec: normalize node %s (%s): %w", n.ID, n.Kind, err)
		}
		if lc, ok := cfg.(*LoopConfig); ok {
			// recursive normalize of nested children
			norm, nerr := (&Spec{Version: s.Version, Nodes: lc.Children}).Normalize()
			if nerr != nil {
				return nil, nerr
			}
			lc.Children = norm.Nodes
		}
		if lc, ok := cfg.(*LLMConfig); ok {
			if legacy && lc.Result == nil {
				lc.Result = defaultLLMResult()
			}
			if lc.Result == nil || lc.Result.PayloadSchema == nil {
				return nil, fmt.Errorf("spec: normalize node %s (llm): payload schema is missing", n.ID)
			}
			if err := lc.Result.PayloadSchema.ValidateDefinition(); err != nil {
				return nil, fmt.Errorf("spec: normalize node %s (llm): %w", n.ID, err)
			}
		}
		if legacy {
			normalizeLegacyLLMRefs(cfg, kinds)
		}
		raw, err := json.Marshal(cfg)
		if err != nil {
			return nil, fmt.Errorf("spec: normalize re-marshal %s: %w", n.ID, err)
		}
		out.Nodes = append(out.Nodes, NodeSpec{ID: n.ID, Kind: n.Kind, Config: raw})
	}
	return out, nil
}

// DecodeJSON performs version-first dispatch and returns the version-neutral
// normalized execution plan. This is the only JSON loader used by the CLI;
// the artifact sidecar is loaded here (via WithSidecar), never from a second
// parser in the CLI.
func DecodeJSON(data []byte, opts ...DecodeOption) (*Spec, error) {
	o := &decodeOptions{warn: func(msg string) { fmt.Fprintln(os.Stderr, msg) }}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(o); err != nil {
			return nil, err
		}
	}
	var header struct {
		Version json.RawMessage `json:"version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, fmt.Errorf("spec: malformed JSON: %w", err)
	}
	if len(header.Version) == 0 || bytes.Equal(header.Version, []byte("null")) {
		return nil, fmt.Errorf("spec: missing version")
	}
	var version string
	if err := json.Unmarshal(header.Version, &version); err != nil || version == "" {
		return nil, fmt.Errorf("spec: malformed version")
	}
	if version != VersionV1 && version != VersionV2 {
		return nil, fmt.Errorf("spec: unsupported version %q (want %q or %q)", version, VersionV1, VersionV2)
	}
	var s Spec
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("spec: decode %s: %w", version, err)
	}
	_, sidecarRows, effectiveVersion, err := loadSidecar(o, s.SemanticsVersion)
	if err != nil {
		return nil, err
	}
	if effectiveVersion != "" {
		s.SemanticsVersion = effectiveVersion
		merged, merr := MergeProvenance(sidecarRows, s.Provenance)
		if merr != nil {
			return nil, fmt.Errorf("spec: decode provenance: %w", merr)
		}
		s.Provenance = merged
	}
	return s.Normalize()
}

func normalizeLegacyLLMRefs(cfg any, kinds map[string]string) {
	rewriteSource := func(s *Source) {
		if s != nil && s.Name == "output" && kinds[s.BlockID] == string(ir.KindLLM) {
			s.Name = "text"
		}
	}
	rewriteRef := func(r *Ref) {
		if r != nil && r.Output == "output" && kinds[r.Producer] == string(ir.KindLLM) {
			r.Output = "text"
		}
	}
	rewriteSources := func(ss []Source) {
		for i := range ss {
			rewriteSource(&ss[i])
		}
	}
	switch c := cfg.(type) {
	case *EndConfig:
		for i := range c.Inputs {
			rewriteRef(&c.Inputs[i])
		}
		for i := range c.Outputs {
			rewriteRef(&c.Outputs[i].Ref)
		}
		if c.Answer != nil {
			for i := range c.Answer.Bindings {
				rewriteRef(&c.Answer.Bindings[i].Ref)
			}
		}
	case *LLMConfig:
		rewriteSources(c.Inputs)
	case *HTTPConfig:
		rewriteSources(c.Inputs)
	case *ToolConfig:
		for i := range c.Params {
			rewriteSource(c.Params[i].Ref)
		}
	case *KnowledgeConfig:
		rewriteSources(c.Inputs)
	case *AgentConfig:
		rewriteSource(c.Query)
	case *TextConfig:
		rewriteSources(c.Inputs)
	case *CodeConfig:
		rewriteSources(c.Inputs)
	case *ListOpConfig:
		rewriteSources(c.Inputs)
	case *SelectorConfig:
		for i := range c.Conditions {
			rewriteRef(c.Conditions[i].Left.Ref)
			rewriteRef(c.Conditions[i].Right.Ref)
		}
	case *AggregateConfig:
		for i := range c.Refs {
			rewriteRef(&c.Refs[i])
		}
		for i := range c.Groups {
			for j := range c.Groups[i].Refs {
				rewriteRef(&c.Groups[i].Refs[j])
			}
		}
	case *AssignConfig:
		rewriteSource(&c.Value)
	case *QaConfig:
		rewriteSources(c.Inputs)
	case *SwitchConfig:
		rewriteSource(&c.Query)
	case *LoopConfig:
		rewriteSources(c.Inputs)
		for k, value := range c.StateValues {
			rewriteSource(&value)
			c.StateValues[k] = value
		}
		rewriteSources(c.OutputSources)
		rewriteSource(c.ReturnValue)
	case *LoopReturnConfig:
		rewriteSource(&c.Value)
	}
}

// Marshal returns the canonical JSON bytes of the normalized spec.
func (s *Spec) Marshal() ([]byte, error) {
	norm, err := s.Normalize()
	if err != nil {
		return nil, err
	}
	return json.Marshal(norm)
}

// SpecHash returns sha256 of the canonical normalized spec bytes — the
// cache-key input (D1: spec-hash + compiler version).
func (s *Spec) SpecHash() ([sha256.Size]byte, error) {
	b, err := s.Marshal()
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(b), nil
}

// decodeConfig parses a node config RawMessage into the kind's DTO
// pointer. Unknown kinds are rejected here (closed kind set).
func decodeConfig(kind string, raw json.RawMessage) (any, error) {
	var cfg any
	switch kind {
	case string(ir.KindStart):
		cfg = &StartConfig{}
	case string(ir.KindEnd):
		cfg = &EndConfig{}
	case string(ir.KindLLM):
		cfg = &LLMConfig{}
	case string(ir.KindHTTP):
		cfg = &HTTPConfig{}
	case string(ir.KindTool):
		cfg = &ToolConfig{}
	case string(ir.KindKnowledge):
		cfg = &KnowledgeConfig{}
	case string(ir.KindSubWorkflow):
		cfg = &SubWorkflowConfig{}
	case string(ir.KindAgent):
		cfg = &AgentConfig{}
	case string(ir.KindConversation):
		cfg = &ConversationConfig{}
	case string(ir.KindText):
		cfg = &TextConfig{}
	case string(ir.KindCode):
		cfg = &CodeConfig{}
	case string(ir.KindListOp):
		cfg = &ListOpConfig{}
	case string(ir.KindSelector):
		cfg = &SelectorConfig{}
	case string(ir.KindAggregate):
		cfg = &AggregateConfig{}
	case string(ir.KindAssign):
		cfg = &AssignConfig{}
	case KindLoopAssign:
		cfg = &AssignConfig{}
	case string(ir.KindQA):
		cfg = &QaConfig{}
	case string(ir.KindSwitch):
		cfg = &SwitchConfig{}
	case string(ir.KindOutput):
		cfg = &OutputConfig{}
	case string(ir.KindLoop):
		cfg = &LoopConfig{}
	case string(ir.KindBreak):
		cfg = &BreakConfig{}
	case string(ir.KindContinue):
		cfg = &ContinueConfig{}
	case string(ir.KindLoopReturn):
		cfg = &LoopReturnConfig{}
	default:
		return nil, fmt.Errorf("unknown kind %q", kind)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, cfg); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// ── dataflow source extraction ───────────────────────────────────────────

// Sources returns every dataflow source a node consumes, recursively
// including loop-body descendants (the outer driver must receive all
// producers referenced inside its body). Refs to the node itself (loop
// output/state ports) are excluded.
func Sources(n *NodeSpec) ([]Source, error) {
	cfg, err := decodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	var out []Source
	switch c := cfg.(type) {
	case *LLMConfig:
		out = append(out, c.Inputs...)
	case *HTTPConfig:
		out = append(out, c.Inputs...)
	case *ToolConfig:
		for _, p := range c.Params {
			if p.Ref != nil {
				src := *p.Ref
				if len(src.Path) == 0 {
					src.Path = []string{p.Key} // assemble under the param key
				}
				out = append(out, src)
			}
		}
		out = append(out, c.NestedRefs...)
	case *KnowledgeConfig:
		out = append(out, c.Inputs...)
	case *SubWorkflowConfig:
		out = append(out, c.Inputs...)
	case *AgentConfig:
		if c.Query != nil {
			out = append(out, *c.Query)
		}
	case *TextConfig:
		out = append(out, c.Inputs...)
	case *CodeConfig:
		out = append(out, c.Inputs...)
	case *ListOpConfig:
		out = append(out, c.Inputs...)
	case *SelectorConfig:
		for i, cond := range c.Conditions {
			lp := operandPath(i, "left")
			rp := operandPath(i, "right")
			if cond.Left.Ref != nil {
				out = append(out, Source{Path: lp, BlockID: cond.Left.Ref.Producer, Name: cond.Left.Ref.Output})
			} else {
				out = append(out, Source{Path: lp, Val: cond.Left.Val})
			}
			if cond.Right.Ref != nil {
				out = append(out, Source{Path: rp, BlockID: cond.Right.Ref.Producer, Name: cond.Right.Ref.Output})
			} else {
				out = append(out, Source{Path: rp, Val: cond.Right.Val})
			}
		}
	case *OutputConfig:
		// 模板里的 {{block_output_<id>.<port>}} 占位符 → 数据源引用。
		// output 节点用 expr.RenderTemplate 按占位符全文键取值（与
		// Text concat 同契约;output(expr) 走 emitOutputStmt 生成
		// Output 节点,此前 Sources 缺此分支导致模板占位符恒 <missing>）。
		for _, m := range blockOutputRe.FindAllStringSubmatch(c.Template, -1) {
			// Path = 占位符键（无花括号）,与 RenderTemplate 解析出的
			// key（"block_output_<id>.<port>"）一致;Text concat 的
			// {{varN}} 同款约定。
			out = append(out, Source{Path: []string{"block_output_" + m[1] + "." + m[2]}, BlockID: m[1], Name: m[2]})
		}
	case *AggregateConfig:
		for i, r := range c.Refs {
			out = append(out, Source{Path: []string{fmt.Sprintf("r%d", i)}, BlockID: r.Producer, Name: r.Output})
		}
		for _, group := range c.Groups {
			for i, r := range group.Refs {
				out = append(out, Source{Path: []string{group.Name, fmt.Sprintf("r%d", i)}, BlockID: r.Producer, Name: r.Output})
			}
		}
	case *AssignConfig:
		out = append(out, Source{Path: []string{"value"}, BlockID: c.Value.BlockID, Name: c.Value.Name, Val: c.Value.Val})
	case *LoopReturnConfig:
		// 节点契约:return 值固定装配在 "value" 键（loop_return 实现按
		// 此读取）,不保留源 Path（标识符名或任意表达式路径都可能不是
		// "value",如 13 的 Path=["i"]）。
		v := c.Value
		v.Path = []string{"value"}
		out = append(out, v)
	case *QaConfig:
		out = append(out, c.Inputs...)
	case *SwitchConfig:
		if c.Query.BlockID != "" || c.Query.Val != nil || c.Query.Path != nil {
			out = append(out, c.Query)
		}
	case *EndConfig:
		for _, r := range c.Inputs {
			out = append(out, Source{Path: []string{"output"}, BlockID: r.Producer, Name: r.Output})
		}
		for _, o := range c.Outputs {
			out = append(out, Source{Path: []string{o.Name}, BlockID: o.Ref.Producer, Name: o.Ref.Output})
		}
		if c.Answer != nil {
			for _, b := range c.Answer.Bindings {
				out = append(out, Source{Path: []string{b.Name}, BlockID: b.Ref.Producer, Name: b.Ref.Output})
			}
		}
	case *LoopConfig:
		out = append(out, c.Inputs...)
		for _, v := range c.StateValues {
			out = append(out, v)
		}
		for _, child := range c.Children {
			childOut, cerr := Sources(&child)
			if cerr != nil {
				return nil, cerr
			}
			out = append(out, childOut...)
		}
	}
	return out, nil
}

// DecodeConfig parses a node config RawMessage into the kind's DTO
// (exported for compile/loop-driver consumption).
func DecodeConfig(kind string, raw json.RawMessage) (any, error) {
	return decodeConfig(kind, raw)
}

// normalizeLoopPorts 把 batch 容器端口（parallel for 的平台投影形状）归一化
// 为规范 loop 端口。只作用于投影边（TopPort/端口名）,不动节点——runtime
// 契约层与平台形状解耦:coze-biz YAML 渲染仍输出 batch 端口,spec 内部统一。
func normalizeLoopPorts(p *flow.Projection) *flow.Projection {
	const batchIn, batchOut = "batch-function-inline-input", "batch-function-inline-output"
	changed := false
	for _, e := range p.Edges {
		if e.TargetPortID == batchIn || e.SourcePortID == batchOut {
			changed = true
			break
		}
	}
	if !changed {
		for _, n := range p.Nodes {
			if pl, ok := n.(*flow.ProjectedLoop); ok {
				for _, e := range pl.InnerEdges {
					if e.TargetPortID == batchIn || e.SourcePortID == batchOut {
						changed = true
						break
					}
				}
			}
			if changed {
				break
			}
		}
	}
	if !changed {
		return p
	}
	nodes := make([]ir.Node, len(p.Nodes))
	for i, n := range p.Nodes {
		// 并行 loop 的 InnerEdges（容器内边）同样带 batch 端口 —— 归一化
		// 必须覆盖节点内嵌边,否则 body 子图构建仍拿不到 loop-back。
		if pl, ok := n.(*flow.ProjectedLoop); ok {
			cl := *pl
			ie := make([]flow.Edge, len(pl.InnerEdges))
			for j, e := range pl.InnerEdges {
				if e.TargetPortID == batchIn {
					e.TargetPortID = PortLoopInlineInput
				}
				if e.SourcePortID == batchOut {
					e.SourcePortID = PortLoopInlineOutput
				}
				ie[j] = e
			}
			cl.InnerEdges = ie
			nodes[i] = &cl
			continue
		}
		nodes[i] = n
	}
	edges := make([]flow.Edge, len(p.Edges))
	for i, e := range p.Edges {
		if e.TargetPortID == batchIn {
			e.TargetPortID = PortLoopInlineInput
		}
		if e.SourcePortID == batchOut {
			e.SourcePortID = PortLoopInlineOutput
		}
		edges[i] = e
	}
	return &flow.Projection{Nodes: nodes, Edges: edges}
}
