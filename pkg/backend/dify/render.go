// render.go — IR→Dify renderer: the platform mapping table for the Dify
// backend (docs/WORKFLOW-IR.md, decisions D1/D2/D6). It consumes a
// platform-neutral ir.Graph and produces the Dify WorkflowAST; all
// semantics live upstream in the IR builder, this file only shapes neutral
// payloads into Dify node values (reusing the shared node factories).
//
// Stage A: parity harness target — output must be byte-identical with the
// legacy lowering (pkg/backend/dify/legacy) on the frozen corpus.
package dify

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/backend/llmprojection"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/tool"
)

// RenderError is the expressiveness-gap error for constructs the Dify
// renderer cannot shape yet (mirrors coze.ErrUnsupported).
type RenderError struct {
	Kind   string
	Reason string
}

func (e *RenderError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("dify render: unsupported %s: %s", e.Kind, e.Reason)
	}
	return fmt.Sprintf("dify render: unsupported node kind %s", e.Kind)
}

// RenderFromIR translates a platform-neutral IR graph into a Dify
// WorkflowAST. mode is the program mode ("workflow" | "advanced-chat"),
// mapped onto app.mode.
func RenderFromIR(irg *ir.Graph, mode string) (*WorkflowAST, error) {
	return RenderFromIRWithToolBindings(irg, mode, nil, nil)
}

// RenderFromIRWithToolBindings translates an IR graph and links logical Tool
// calls through a closed Dify binding manifest. A nil manifest preserves the
// legacy symbolic provider/tool fields.
func RenderFromIRWithToolBindings(irg *ir.Graph, mode string, toolBindings *ToolBindingManifest, registry *tool.Registry) (*WorkflowAST, error) {
	ast, _, err := render(irg, mode, toolBindings, registry)
	return ast, err
}

// RenderFromIRWithAudit renders like RenderFromIR and additionally returns
// the IR-node-id → Dify-node-id mapping the renderer used. The mapping is a
// clone; mutating it does not affect the AST. It exists for obligation
// reporting and must not alter rendered bytes.
func RenderFromIRWithAudit(irg *ir.Graph, mode string) (*WorkflowAST, map[string]string, error) {
	return render(irg, mode, nil, nil)
}

// RenderFromIRWithToolBindingsAndAudit is the audit variant of
// RenderFromIRWithToolBindings.
func RenderFromIRWithToolBindingsAndAudit(irg *ir.Graph, mode string, toolBindings *ToolBindingManifest, registry *tool.Registry) (*WorkflowAST, map[string]string, error) {
	return render(irg, mode, toolBindings, registry)
}

// render is the implementation shared by the public entry points. The second
// result is the audit id map; it is nil when rendering fails.
func render(irg *ir.Graph, mode string, toolBindings *ToolBindingManifest, registry *tool.Registry) (*WorkflowAST, map[string]string, error) {
	projection, err := flow.Project(irg)
	if err != nil {
		return nil, nil, err
	}
	llmPlan := llmprojection.Lower(projection, llmprojection.MetadataPolicy{
		ReasoningContent: true,
		Usage:            true,
		FinishReason:     true,
	})
	projection = llmPlan.Projection
	r := &irRenderer{
		ast:                newRenderAST(mode),
		mode:               mode,
		toolBindings:       toolBindings,
		registry:           registry,
		titles:             map[string]int{},
		difyIDs:            map[string]string{},
		inputNodeIDs:       map[string]string{},
		index:              map[string]ir.Node{},
		outputVars:         map[string]bool{},
		irEdges:            append([]flow.Edge(nil), projection.Edges...),
		inEdges:            map[string][]flow.Edge{},
		skipNodes:          map[string]bool{},
		branchEnds:         map[string][]branchEndRef{},
		qaEnds:             map[string][]qaEndRef{},
		switchHasDefault:   map[string]bool{},
		scheduledEdges:     map[irEdgeKey]bool{},
		iterationStarts:    map[string]string{},
		iterationFolded:    map[string]bool{},
		loopOutputUsed:     map[string]bool{},
		loopChildren:       map[string]bool{},
		pendingLoopEdges:   map[string][]EdgeDecl{},
		pendingLoopEntries: map[string][]EdgeDecl{},
		loopEscapes:        map[string]string{},
		portRedirect:       map[string]string{},
		typeRedirect:       map[string]string{},
		branchEntries:      map[string][]flow.Edge{},
		attemptEntries:     map[string][]flow.Edge{},
		selCases:           map[string]int{},
		answerFolds:        map[string][]answerFoldRef{},
		endReturns:         map[string][]ir.Ref{},
		premint:            map[string]string{},
		condOwner:          map[string]string{},
		condSelEdges:       map[string]flow.Edge{},
		jinjaTemplates:     map[string]string{},
		jinjaInputs:        map[string][]ir.Source{},
		extractionEdges:    map[string]EdgeDecl{},
		inlinePlans:        map[string]*ir.InlineStmt{},
		inlineEdges:        map[string][]EdgeDecl{},
		inlineResults:      map[string]bool{},
		envelopeAdapters:   llmPlan.Adapters,
	}
	for _, n := range projection.Nodes {
		r.index[n.NodeID()] = n
		if loop, ok := n.(*flow.ProjectedLoop); ok {
			for _, child := range loop.Children {
				r.index[child.NodeID()] = child
				r.loopChildren[child.NodeID()] = true
			}
		}
	}
	for _, n := range projection.Nodes {
		loop, ok := n.(*flow.ProjectedLoop)
		if !ok {
			continue
		}
		for _, other := range projection.Nodes {
			if other.NodeID() != loop.NodeID() && nodeReferencesProducer(other, loop.NodeID()) {
				r.loopOutputUsed[loop.NodeID()] = true
				break
			}
		}
	}
	for _, e := range projection.Edges {
		r.inEdges[e.TargetNodeID] = append(r.inEdges[e.TargetNodeID], e)
	}
	// Condition materializers: record each one's selector edge so the
	// renderer can (a) pre-mint the selector's id before the materializer's
	// (legacy GenerateNodeID order: if_2 < condition_3) and (b) emit the
	// materializer→selector edge before the materializer's incoming edges
	// (legacy buildIfBranchNode adds that edge before lowerIf connects the
	// entry tails).
	for _, e := range projection.Edges {
		if mat, ok := r.index[e.SourceNodeID].(*ir.CodeRun); ok && mat.Condition {
			if _, isSel := r.index[e.TargetNodeID].(*ir.Selector); isSel {
				r.condOwner[e.SourceNodeID] = e.TargetNodeID
				r.condSelEdges[e.SourceNodeID] = e
			}
		}
	}
	r.planStructured(irg.Root)
	r.planAnswerFolds()
	r.planImplicitEnds()
	r.planTernaryFolds()
	for _, cv := range irg.ConversationVars {
		r.ast.ConversationVariables = append(r.ast.ConversationVariables, ConversationVariable{
			ID:        ConversationVariableID(cv.Name),
			Name:      cv.Name,
			ValueType: valueTypeFor(cv.Type),
			Value:     cv.Default,
			Selector:  []string{"conversation", cv.Name},
		})
	}
	for _, n := range projection.Nodes {
		if r.skipNodes[n.NodeID()] {
			// a terminal end folded into an Answer node (direct template
			// return) emits at the end's node position; a shared end with
			// multiple branch exits folds into one Answer per exit.
			if folds, ok := r.answerFolds[n.NodeID()]; ok {
				r.emitAnswerFolds(n.NodeID(), folds)
			}
			continue
		}
		if err := r.emitNode(n); err != nil {
			return nil, nil, err
		}
		// A condition materializer emits its selector edge before its own
		// incoming edges (legacy edge order: materializer→if, then
		// start→materializer).
		if e, ok := r.condSelEdges[n.NodeID()]; ok {
			r.emitConditionEdge(e)
		}
		// A materialized-literal branch return emits its end edge before
		// the selector's incoming edge (legacy lowerReturnFromTails builds
		// the return's end edge, then connects the branch tail). Branches
		// with an intermediate node (llm/code) keep the incoming edge first.
		var branchEnds []branchEndRef
		if len(r.branchEnds[n.NodeID()]) > 0 {
			branchEnds = r.branchEnds[n.NodeID()]
		}
		pre, post := r.splitBranchEnds(branchEnds)
		for _, be := range pre {
			r.emitBranchEnd(be)
		}
		if err := r.emitInEdges(n.NodeID()); err != nil {
			return nil, nil, err
		}
		if edge, ok := r.extractionEdges[n.NodeID()]; ok {
			edge.ID = r.nextEdgeID()
			r.ast.Graph.Edges = append(r.ast.Graph.Edges, edge)
		}
		for _, edge := range r.inlineEdges[n.NodeID()] {
			edge.ID = r.nextEdgeID()
			r.ast.Graph.Edges = append(r.ast.Graph.Edges, edge)
		}
		if loop, ok := n.(*flow.ProjectedLoop); ok {
			if err := r.emitProjectedLoopEdges(loop); err != nil {
				return nil, nil, err
			}
		}
		// Decomposed branch returns: direct QA ends, per-producer ends, then
		// the structured QA/attempt entry schedules.
		for _, qe := range r.qaEnds[n.NodeID()] {
			r.emitQAEnd(n.(*ir.QA), qe)
		}
		for _, be := range post {
			r.emitBranchEnd(be)
		}
		for _, e := range r.branchEntries[n.NodeID()] {
			r.emitStructuredEntry(e)
		}
		for _, e := range r.attemptEntries[n.NodeID()] {
			r.emitStructuredEntry(e)
		}
	}
	ids := make(map[string]string, len(r.difyIDs))
	for k, v := range r.difyIDs {
		ids[k] = v
	}
	return r.ast, ids, nil
}

// planImplicitEnds drops implicit terminal Ends: the IR marks an end
// synthesized for a void function that never returns (a bare trailing if)
// as Implicit; the legacy lowering never emits that end, so the renderer
// skips it (dify side only).
func (r *irRenderer) planImplicitEnds() {
	for _, n := range r.index {
		if en, ok := n.(*ir.End); ok && en.Implicit {
			r.skipNodes[en.NodeID()] = true
		}
	}
}

// planStructured derives Dify-only branch scheduling from the Stmt tree.
// The old renderer inferred these relationships by scanning flat edges and
// node positions; the tree names the merge, branch and attempt terminals
// directly, so no positional inference is needed.
func (r *irRenderer) planStructured(root *ir.Block) {
	var walk func(*ir.Block)
	walk = func(block *ir.Block) {
		if block == nil {
			return
		}
		for _, stmt := range block.Stmts {
			switch s := stmt.(type) {
			case *ir.ReturnStmt:
				// 主流程分支 return（FlowEnd 共享 end）：记录出口值，
				// 供 advanced-chat 多出口 answer 折叠按入边 src 匹配。
				if s.End != nil && s.Value != nil {
					r.endReturns[s.End.ID] = append(r.endReturns[s.End.ID], ir.Ref{Producer: s.Value.NodeID(), Output: s.Value.Output()})
				}
			case *ir.IfStmt:
				r.planStructuredMerge(s.Merge)
				r.planTerminalIf(s)
				walk(s.Then)
				walk(s.Else)
			case *ir.QaStmt:
				r.planStructuredMerge(s.Merge)
				for _, branch := range s.Branches {
					walk(branch.Body)
				}
			case *ir.SwitchStmt:
				for _, br := range s.Branches {
					if br.Port == "default" {
						r.switchHasDefault[s.ID] = true
					}
				}
				r.planStructuredMerge(s.Merge)
				for _, branch := range s.Branches {
					walk(branch.Body)
				}
			case *ir.AttemptStmt:
				r.planAttemptBranch(s.ID, "source", s.SuccessEnd)
				r.planAttemptBranch(s.ID, "fail-branch", s.FailureEnd)
				walk(s.Success)
				walk(s.Failure)
			case *ir.LoopStmt:
				walk(s.Body)
			case *ir.ParallelStmt:
				for _, branch := range s.Branches {
					walk(branch)
				}
			case *ir.InlineStmt:
				r.planInline(s)
				walk(s.Body)
			}
		}
	}
	walk(root)
}

func (r *irRenderer) planInline(stmt *ir.InlineStmt) {
	if stmt == nil || stmt.Body == nil {
		return
	}
	for _, child := range stmt.Body.Stmts {
		nodeStmt, ok := child.(*ir.NodeStmt)
		if !ok {
			continue
		}
		if _, isCode := nodeStmt.Node.(*ir.CodeRun); isCode {
			r.inlinePlans[nodeStmt.Node.NodeID()] = stmt
			return
		}
	}
}

func (r *irRenderer) planTerminalIf(stmt *ir.IfStmt) {
	if stmt == nil || r.loopChildren[stmt.ID] {
		return
	}
	// Merge 存在时，有值分支由 Merge 汇合进共享 End；但以 bare return
	// 结束的 void 分支没有值可上传，必须单独补一条出边到合成 End
	// （形状适配：dify if-else 只有 true/false 两个出边）。
	if stmt.Merge != nil {
		r.planVoidReturnBranch(stmt.ID, "true", stmt.Then)
		r.planVoidReturnBranch(stmt.ID, "false", stmt.Else)
		return
	}
	if stmt.Relay != nil {
		r.skipNodes[stmt.Relay.NodeID()] = true
		for _, edge := range r.irEdges {
			if edge.SourceNodeID == stmt.Relay.NodeID() || edge.TargetNodeID == stmt.Relay.NodeID() {
				r.scheduleEdge(edge)
			}
		}
	}
	r.planTerminalBranch(stmt.ID, "true", stmt.Then)
	r.planTerminalBranch(stmt.ID, "false", stmt.Else)
}

func (r *irRenderer) planTerminalBranch(selectorID, port string, block *ir.Block) {
	if block == nil || len(block.Stmts) == 0 {
		return
	}
	ret, ok := block.Stmts[len(block.Stmts)-1].(*ir.ReturnStmt)
	if !ok || ret.End != nil {
		return
	}
	ref, ok := ret.ResultRef()
	if !ok {
		// bare return; 分支：无值——注册 void 分支 end（增广 return'）。
		r.planVoidReturnBranch(selectorID, port, block)
		return
	}
	if tp, ok := r.index[ref.Producer].(*ir.TextProcess); ok && tp.Answer {
		r.skipNodes[ref.Producer] = true
		r.answerFolds[ref.Producer] = append(r.answerFolds[ref.Producer], answerFoldRef{ref: ref})
		return
	}
	key := ref.Producer
	branchPort := ""
	if _, materialized := ret.MaterializedProducer(); !materialized {
		key = selectorID
		branchPort = port
	}
	r.branchEnds[key] = append(r.branchEnds[key], branchEndRef{ref: ref, src: key, port: branchPort})
}

// planVoidReturnBranch registers a branch that terminates in a bare
// `return;` (no value): the branch needs an out edge (dify if-else nodes
// only have true/false ports, a dangling branch trips IFELSE_NO_BRANCHES),
// so it is augmented with a no-output End the renderer emits as a
// dedicated terminal (program augmentation p → p′: all non-returning
// branch exits join the added return′ terminal).
func (r *irRenderer) planVoidReturnBranch(selectorID, port string, block *ir.Block) {
	if block == nil || len(block.Stmts) == 0 {
		return
	}
	ret, ok := block.Stmts[len(block.Stmts)-1].(*ir.ReturnStmt)
	if !ok || ret.End != nil || ret.Value != nil {
		return
	}
	r.branchEnds[selectorID] = append(r.branchEnds[selectorID], branchEndRef{
		src:  selectorID,
		port: port,
		void: true,
	})
}

func (r *irRenderer) planAttemptBranch(sourceID, port string, end *ir.End) {
	if end == nil {
		return
	}
	for _, edge := range r.irEdges {
		if edge.SourceNodeID != sourceID || edge.SourcePortID != port {
			continue
		}
		r.scheduleEdge(edge)
		r.attemptEntries[end.ID] = append(r.attemptEntries[end.ID], edge)
	}
}

// ── ternary → Jinja fold ──────────────────────────────────────────

// planTernaryFolds folds the runtime conditional the IR models for a
// ternary expression (Selector → literal TextProcess branches merged by a
// Variable Aggregator) back into the Jinja2 {% if %}/{% elif %} chain the
// legacy lowering inlines into template-transform text
// (lowering/shared_utils.go ternaryToJinja / templateStringToText). The
// conditional subgraph is dropped; the template-transform node keeps the
// top selector's entry edges, so the canvas stays start → template → end.
// Dify-only: the Coze backend renders the runtime conditional shape its
// checker accepts.
func (r *irRenderer) planTernaryFolds() {
	for _, n := range r.index {
		tp, ok := n.(*ir.TextProcess)
		if !ok || tp.Method != "concat" {
			continue
		}
		var keptIdx []int
		var roots []string
		rewritten := tp.Template
		for i, in := range tp.Inputs {
			if _, isAgg := r.index[in.BlockID].(*ir.Aggregate); !isAgg {
				keptIdx = append(keptIdx, i)
				continue
			}
			jinja, root, foldOK := r.foldTernary(in.BlockID)
			marker := "{{" + bindingMarker(in) + "}}"
			if !foldOK || !strings.Contains(rewritten, marker) {
				keptIdx = append(keptIdx, i)
				continue
			}
			rewritten = strings.ReplaceAll(rewritten, marker, jinja)
			roots = append(roots, root)
		}
		if len(roots) == 0 {
			continue
		}
		// No renumbering: the surviving markers are the binding names
		// themselves (T1: the placeholder key IS the node_inputs name), so
		// compacting the input list cannot shift any marker.
		kept := make([]ir.Source, 0, len(keptIdx))
		for _, i := range keptIdx {
			kept = append(kept, tp.Inputs[i])
		}
		r.jinjaTemplates[tp.ID] = rewritten
		r.jinjaInputs[tp.ID] = kept
		// the template takes the top selector's entry edges: the folded
		// subgraph's flow entry is the outermost selector, whose incoming
		// edges now target the template (legacy start → template).
		var entry []flow.Edge
		for _, root := range roots {
			for _, e := range r.inEdges[root] {
				e.TargetNodeID = tp.ID // retarget: the selector is skipped
				if !edgeIn(entry, e) {
					entry = append(entry, e)
				}
			}
		}
		r.inEdges[tp.ID] = entry
	}
}

func edgeIn(edges []flow.Edge, e flow.Edge) bool {
	for _, x := range edges {
		if x.SourceNodeID == e.SourceNodeID && x.SourcePortID == e.SourcePortID && x.TargetNodeID == e.TargetNodeID {
			return true
		}
	}
	return false
}

// foldTernary reconstructs the conditional tree feeding one aggregate and
// renders it as a Jinja2 chain. It returns the chain and the outermost
// selector (the flow entry of the folded subgraph); every node of the
// subgraph is marked skipped on success.
func (r *irRenderer) foldTernary(aggID string) (string, string, bool) {
	dropped := map[string]bool{}
	jinja, root, ok := r.foldTernaryValue(aggID, dropped)
	if !ok {
		return "", "", false
	}
	for id := range dropped {
		r.skipNodes[id] = true
	}
	return jinja, root, true
}

// foldTernaryValue renders one conditional-tree node: a literal
// TextProcess (the branch value) or an Aggregate (a conditional merge).
// The returned root is the outermost selector of the subtree, "" for a
// pure literal.
func (r *irRenderer) foldTernaryValue(nodeID string, dropped map[string]bool) (string, string, bool) {
	switch nd := r.index[nodeID].(type) {
	case *ir.TextProcess:
		if nd.Method != "concat" || len(nd.Inputs) != 0 || strings.Contains(nd.Template, "{{") {
			return "", "", false
		}
		dropped[nodeID] = true
		return nd.Template, "", true
	case *ir.Aggregate:
		dropped[nodeID] = true
		sel := r.aggregateSelector(nd)
		if sel == nil {
			return "", "", false
		}
		return r.foldSelector(sel, dropped)
	case *ir.Selector:
		// A branch targeting a nested selector directly (no intermediate
		// aggregate): the nested conditional is a plain selector chain.
		return r.foldSelector(nd, dropped)
	}
	return "", "", false
}

// foldSelector renders one selector's conditional: `{% if C %}T{% else
// %}E{% endif %}`; a conditional in the else slot splices as `{% elif
// ...` (the legacy ternaryToJinja nested splice, stripping the nested
// string's leading "{% if " — exactly 6 characters).
func (r *irRenderer) foldSelector(sel *ir.Selector, dropped map[string]bool) (string, string, bool) {
	tt, tf, ok := r.selectorTargets(sel)
	if !ok {
		return "", "", false
	}
	thenStr, _, ok := r.foldTernaryValue(tt, dropped)
	if !ok {
		return "", "", false
	}
	elseStr, _, ok := r.foldTernaryValue(tf, dropped)
	if !ok {
		return "", "", false
	}
	dropped[sel.ID] = true
	// A branch targeting a nested selector leaves its φ-merge aggregate
	// behind (the nested branches merge into it) — drop it too.
	for _, t := range []string{tt, tf} {
		if _, isSel := r.index[t].(*ir.Selector); isSel {
			if agg := r.mergeAggregate(t); agg != "" {
				dropped[agg] = true
			}
		}
	}
	cond := r.jinjaCond(sel)
	if strings.HasPrefix(elseStr, "{% if ") {
		return fmt.Sprintf("{%% if %s %%}%s{%% elif %s", cond, thenStr, elseStr[6:]), sel.ID, true
	}
	return fmt.Sprintf("{%% if %s %%}%s{%% else %%}%s{%% endif %%}", cond, thenStr, elseStr), sel.ID, true
}

// selectorTargets returns the single true/false branch targets of a
// selector; ok is false when either port fans out to multiple targets
// (a parallel branch, which the ternary fold does not handle).
func (r *irRenderer) selectorTargets(sel *ir.Selector) (string, string, bool) {
	var tt, tf string
	for _, e := range r.irEdges {
		if e.SourceNodeID != sel.ID {
			continue
		}
		switch e.SourcePortID {
		case "true":
			if tt != "" {
				return "", "", false
			}
			tt = e.TargetNodeID
		case "false":
			if tf != "" {
				return "", "", false
			}
			tf = e.TargetNodeID
		}
	}
	return tt, tf, tt != "" && tf != ""
}

// aggregateSelector finds the selector whose branches merge into the
// aggregate: refs[0] is the true-branch value and refs[1] the false-branch
// value (the builder's emitIf order).
func (r *irRenderer) aggregateSelector(agg *ir.Aggregate) *ir.Selector {
	if len(agg.Groups) > 0 {
		// Field-level phis are real grouped aggregators, not the legacy
		// two-arm scalar ternary shape that can be folded into Jinja.
		return nil
	}
	if len(agg.Refs) != 2 {
		return nil
	}
	thenProducer, elseProducer := agg.Refs[0].Producer, agg.Refs[1].Producer
	for _, n := range r.index {
		sel, ok := n.(*ir.Selector)
		if !ok {
			continue
		}
		tt, tf, ok := r.selectorTargets(sel)
		if !ok {
			continue
		}
		if r.foldBranchResolves(tt, thenProducer) && r.foldBranchResolves(tf, elseProducer) {
			return sel
		}
	}
	return nil
}

// foldBranchResolves reports whether a selector branch target resolves to
// the given producer: a direct match, or a nested selector whose branch
// tails are exactly the producer aggregate's in-edge sources.
func (r *irRenderer) foldBranchResolves(target, producer string) bool {
	if target == producer {
		return true
	}
	if _, isSel := r.index[target].(*ir.Selector); !isSel {
		return false
	}
	branchTails := map[string]bool{}
	for _, e := range r.irEdges {
		if e.SourceNodeID == target {
			branchTails[e.TargetNodeID] = true
		}
	}
	if len(branchTails) == 0 || len(r.inEdges[producer]) == 0 {
		return false
	}
	for _, e := range r.inEdges[producer] {
		if !branchTails[e.SourceNodeID] {
			return false
		}
	}
	return true
}

// mergeAggregate returns the aggregate that collects every branch tail of
// the selector (the φ-merge of a nested conditional), "" when the selector
// does not merge into a single aggregate.
func (r *irRenderer) mergeAggregate(selID string) string {
	branchTails := map[string]bool{}
	for _, e := range r.irEdges {
		if e.SourceNodeID == selID {
			branchTails[e.TargetNodeID] = true
		}
	}
	if len(branchTails) == 0 {
		return ""
	}
	for _, e := range r.irEdges {
		if _, isAgg := r.index[e.TargetNodeID].(*ir.Aggregate); !isAgg {
			continue
		}
		aggSources := map[string]bool{}
		for _, ie := range r.inEdges[e.TargetNodeID] {
			aggSources[ie.SourceNodeID] = true
		}
		if len(aggSources) != len(branchTails) {
			continue
		}
		all := true
		for t := range branchTails {
			if !aggSources[t] {
				all = false
				break
			}
		}
		if all {
			return e.TargetNodeID
		}
	}
	return ""
}

// jinjaCond renders a selector's conditions as a Jinja2 python expression,
// mirroring the legacy exprToPython rendering: identifiers by name,
// literals verbatim, `(left op right)` grouping. Multi-condition selectors
// join with and/or per the IR logic (1=or, 2=and).
func (r *irRenderer) jinjaCond(sel *ir.Selector) string {
	parts := make([]string, 0, len(sel.Conditions))
	for _, c := range sel.Conditions {
		parts = append(parts, fmt.Sprintf("(%s %s %s)", r.jinjaOperand(c.Left), jinjaSelectorOp(c.Operator), r.jinjaOperand(c.Right)))
	}
	if len(parts) == 0 {
		return "True"
	}
	if len(parts) == 1 {
		return parts[0]
	}
	join := " and "
	if sel.Logic == 1 {
		join = " or "
	}
	return "(" + strings.Join(parts, join) + ")"
}

// jinjaOperand renders one condition operand as Jinja text: refs by their
// IR port name (the start-param case matches the legacy source name),
// literals verbatim.
func (r *irRenderer) jinjaOperand(op ir.Operand) string {
	if op.Ref != nil {
		return op.Ref.Output
	}
	return operandValue(op)
}

// jinjaSelectorOp maps a SELECTOR_OPERATORS id onto the python comparison
// operator used in Jinja conditions.
func jinjaSelectorOp(id int) string {
	switch id {
	case 1:
		return "=="
	case 2:
		return "!="
	case 13:
		return ">"
	case 14:
		return ">="
	case 15:
		return "<"
	case 16:
		return "<="
	}
	return "=="
}

// planAnswerFolds detects terminal ends whose only input is an
// answer-flagged TextProcess (direct template return). Dify renders those
// as a single Answer node; both the TextProcess and the End are dropped
// and the template is folded into the answer with dify refs substituted.
//
// 主流程分支 return（FlowEnd）把多出口汇入共享 end 900001（多入边）。
// 平台形状契约（docs/DIFY-VS-COZE-GRAPH-SEMANTICS.md §6.3）：
//   - workflow：单 end 多入边（end 只是 RESPONSE 汇点），value_selector
//     取首个出口值 —— end 保留，各分支节点自然渲染并连入 end；
//   - advanced-chat：每个出口折叠为独立 Answer 节点（对话输出载体），
//     值引用按控制流入边 src 与 ReturnStmt 值匹配。
//
// 单入边 end 走既有单值折叠逻辑。
func (r *irRenderer) planAnswerFolds() {
	for _, end := range r.index {
		en, ok := end.(*ir.End)
		if !ok || r.skipNodes[end.NodeID()] {
			continue
		}
		if len(en.Inputs) == 0 {
			continue
		}
		if len(r.inEdges[end.NodeID()]) > 1 {
			// 共享 end 多入边（主流程分支 return 汇点）。
			if r.mode == "advanced-chat" {
				r.planSharedEndAnswers(end.NodeID(), en)
			}
			// workflow：保留 end（单 end 多入边契约），各分支节点自然
			// 渲染，emitInEdges(end) 汇入。
			continue
		}
		producer := r.index[en.Inputs[0].Producer]
		tp, isText := producer.(*ir.TextProcess)
		// Text style: a direct template-literal return (Answer-flagged) or
		// a pure literal return in advanced-chat mode folds into the answer
		// text; both the TextProcess and the End are dropped.
		if isText && (tp.Answer || (r.mode == "advanced-chat" && len(tp.Inputs) == 0)) {
			r.skipNodes[end.NodeID()] = true
			r.skipNodes[tp.ID] = true
			r.answerFolds[end.NodeID()] = append(r.answerFolds[end.NodeID()], answerFoldRef{ref: en.Inputs[0]})
			continue
		}
		// Output style: output(expr) 生成 ir.Output（dify AnswerNode 投影,
		// 模板内联 block_output refs）。终结点折叠时直接用 Output 的模板
		// 展开成 answer 文本,并 skip 掉 Output 节点 —— 避免 "answer 引用
		// answer" 的双层冗余（e2e 抓出的回归）。
		if out, isOutput := producer.(*ir.Output); isOutput {
			if r.mode == "workflow" {
				// workflow: output 渲染成 Dify output 节点(自己收集结果,
				// type: end), 跟随的隐式 end 冗余 → skip, 不折叠成 answer。
				r.skipNodes[end.NodeID()] = true
				continue
			}
			r.skipNodes[end.NodeID()] = true
			r.skipNodes[out.ID] = true
			r.answerFolds[end.NodeID()] = append(r.answerFolds[end.NodeID()], answerFoldRef{ref: en.Inputs[0]})
			continue
		}
		// Ref style: advanced-chat selector returns lower to an Answer node
		// referencing the producer's port; only the terminal End is dropped.
		if r.mode == "advanced-chat" && producer != nil {
			r.skipNodes[end.NodeID()] = true
			r.answerFolds[end.NodeID()] = append(r.answerFolds[end.NodeID()], answerFoldRef{ref: en.Inputs[0]})
		}
	}
}

// planSharedEndAnswers folds an advanced-chat shared end (multiple branch
// return exits) into one Answer per exit. Each control-flow in-edge
// src→endID pairs with the ReturnStmt whose value producer is src; a branch
// without a matching value (void return) aborts the fold (the end stays
// materialized so no branch dangles).
func (r *irRenderer) planSharedEndAnswers(endID string, en *ir.End) {
	rets := r.endReturns[endID]
	anyVoid := false
	folds := []answerFoldRef{}
	for _, e := range r.inEdges[endID] {
		matched := false
		for i := range rets {
			if rets[i].Producer == e.SourceNodeID {
				// TextProcess/Output producers fold into the answer text and
				// are dropped (like the single-exit Text/Output styles); the
				// answer takes the producer's in-edges. Other producers (LLM
				// etc.) stay materialized and the answer references them.
				if tp, isTP := r.index[rets[i].Producer].(*ir.TextProcess); isTP {
					if tp.Answer || r.mode == "advanced-chat" {
						r.skipNodes[rets[i].Producer] = true
					}
				} else if _, isOut := r.index[rets[i].Producer].(*ir.Output); isOut {
					r.skipNodes[rets[i].Producer] = true
				}
				folds = append(folds, answerFoldRef{ref: rets[i], src: e.SourceNodeID})
				matched = true
				break
			}
		}
		if !matched {
			anyVoid = true
			break
		}
	}
	if anyVoid || len(folds) == 0 {
		// 保守：保留 end 节点（void 出口无 answer 载体），各分支自然连 end。
		return
	}
	r.skipNodes[endID] = true
	r.answerFolds[endID] = folds
}

// answerFoldRef is one answer fold: the return ref plus (for shared-end
// multi-exit folds) the control-flow in-edge source whose edge feeds the
// answer. src == "" means the fold takes the producer's own in-edges.
type answerFoldRef struct {
	ref ir.Ref
	src string
}

// emitAnswerFolds emits one Answer node per fold (an answer-flagged
// template return, or — for an advanced-chat shared end — one answer per
// branch exit).
func (r *irRenderer) emitAnswerFolds(endID string, folds []answerFoldRef) {
	for _, f := range folds {
		r.emitAnswerFold(endID, f)
	}
}

// emitAnswerFold emits an Answer node folding an answer-flagged template
// return: the TextProcess template with dify refs substituted.
func (r *irRenderer) emitAnswerFold(endID string, fold answerFoldRef) {
	ref := fold.ref
	answerID := r.nextNodeID("answer") // answer node id prefix matches the Python/legacy wire
	var answer string
	var inEdges []flow.Edge
	if tp, ok := r.index[ref.Producer].(*ir.TextProcess); ok {
		answer = r.answerTemplate(tp)
		inEdges = r.inEdges[tp.ID]
	} else if out, ok := r.index[ref.Producer].(*ir.Output); ok {
		answer = r.rewriteTemplate(out.Template)
		inEdges = r.inEdges[out.ID]
	} else {
		answer = "{{#" + r.difyIDs[ref.Producer] + "." + r.portFor(ref.Producer, ref.Output) + "#}}"
		if fold.src != "" {
			// shared-end multi-exit fold: the answer takes only the in-edge
			// of its own branch exit (src → end), not every exit's edge.
			for _, e := range r.inEdges[endID] {
				if e.SourceNodeID == fold.src {
					inEdges = []flow.Edge{e}
					break
				}
			}
		} else {
			inEdges = r.inEdges[endID] // the End's incoming edge (producer → answer)
		}
	}
	node := MakeAnswerNode(answerID, answer)
	setDataTitle(node.Data, r.titleFor("answer"))
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, node)
	// the answer takes the folded producer's (or the End's) incoming edges;
	// terminal answers drop the outgoing edge, which targeted the skipped End.
	for _, e := range inEdges {
		src, ok := r.difyIDs[e.SourceNodeID]
		if !ok {
			continue
		}
		handles := r.edgeHandles(e)
		if escapeID := r.loopEscapes[e.SourceNodeID]; escapeID != "" {
			src = escapeID
			handles = []string{"false"}
		}
		// selector-sourced edges fan out per case (if_2_case_0) like
		// emitInEdges; plain tails keep the IR port.
		for _, h := range handles {
			r.ast.Graph.Edges = append(r.ast.Graph.Edges, EdgeDecl{
				ID:           r.nextEdgeID(),
				Source:       src,
				Target:       answerID,
				Type:         "custom",
				SourceHandle: h,
			})
		}
	}
}

// answerTemplate renders the answer-folded template text: {{varN}} markers
// become dify refs, ternary folds inline as Jinja2 chains (templateText).
func (r *irRenderer) answerTemplate(tp *ir.TextProcess) string {
	return r.templateText(tp)
}

// bindingMarker returns the template-placeholder key of a text-input binding:
// the path recorded on the IR Source, which is the same string the coze-biz
// adapter writes into node_inputs[].name (render_node.go renderSources). The
// key is produced by the IR builder as a function of the producer port, so
// reading it back (instead of assuming a positional {{varN}} marker) keeps the
// two adapters on one naming contract; the port itself travels in Source.Name.
func bindingMarker(in ir.Source) string {
	return strings.Join(in.Path, ".")
}

// templateText renders a TextProcess template: {{name}} markers become
// {{#node.port#}} dify refs (the variable list stays empty,
// translateTemplateVar passes nil), ternary folds inline as Jinja2 chains,
// and block_output refs rewrite through rewriteTemplate.
func (r *irRenderer) templateText(tp *ir.TextProcess) string {
	out := tp.Template
	if t, ok := r.jinjaTemplates[tp.ID]; ok {
		out = t
	}
	inputs := tp.Inputs
	if ins, ok := r.jinjaInputs[tp.ID]; ok {
		inputs = ins
	}
	for _, in := range inputs {
		marker := "{{" + bindingMarker(in) + "}}"
		if !strings.Contains(out, marker) {
			continue
		}
		ref := "{{#" + r.difyIDs[in.BlockID] + "." + r.portFor(in.BlockID, in.Name) + "#}}"
		out = strings.ReplaceAll(out, marker, ref)
	}
	return r.rewriteTemplate(out)
}

func (r *irRenderer) planStructuredMerge(agg *ir.Aggregate) {
	if agg == nil {
		return
	}
	endID := r.terminalEndFromAggregate(agg.ID, map[string]bool{})
	if endID == "" {
		return
	}
	aggID := agg.ID
	r.skipNodes[aggID] = true
	r.skipNodes[endID] = true
	end := r.index[endID].(*ir.End)
	if end.Implicit || len(agg.AllRefs()) == 0 {
		// Implicit terminal end: the function is void and the branch
		// values are discarded (a trailing if, e.g. 14) — legacy renders
		// only the branch nodes, neither the aggregate nor any end.
		// A value-less merge (refs == 0) is dropped the same way.
		return
	}
	// Pair each branch return ref with its aggregate in-edge, in
	// builder order (the IR builder emits one ref and one flow edge
	// per branch). The branch source is the in-edge's source.
	ins := r.inEdges[aggID]
	for i, ref := range agg.Refs {
		if _, nested := r.index[ref.Producer].(*ir.Aggregate); nested {
			continue
		}
		src := ""
		port := ""
		if i < len(ins) {
			src = ins[i].SourceNodeID
			port = ins[i].SourcePortID
		}
		_, srcIsQA := r.index[src].(*ir.QA)
		if srcIsQA && ref.Producer == src {
			// Direct branch return: the end connects straight from the
			// QA's action port (human_input approve/reject → end).
			r.qaEnds[src] = append(r.qaEnds[src], qaEndRef{ref: ref, port: port})
			continue
		}
		// advanced-chat: a literal/template branch return renders as an
		// Answer node in the producer's position (the legacy
		// lowerReturnFromTails answer path); the producer node itself
		// is dropped and the answer takes its incoming edge.
		if tp, isTP := r.index[ref.Producer].(*ir.TextProcess); isTP {
			if tp.Answer || r.mode == "advanced-chat" {
				r.skipNodes[ref.Producer] = true
				r.answerFolds[ref.Producer] = append(r.answerFolds[ref.Producer], answerFoldRef{ref: ref})
				continue
			}
		}
		// Direct branch return off a selector (value from an upstream
		// producer, no intermediate node): hang the end on the
		// selector's case port. Branches with an intermediate node use
		// the branch tail producer as the edge source.
		key := ref.Producer
		if src != "" && src != ref.Producer {
			key = src
		}
		r.branchEnds[key] = append(r.branchEnds[key], branchEndRef{ref: ref, src: key, port: port})
		// A branch body that materialized a node carries a QA-sourced
		// entry edge (QA action port → body node). The legacy
		// lowerReturnFromTails connects the branch tail after the
		// return body is emitted, so the structured schedule places these
		// entry edges after the producer's own end edge.
		for _, e := range r.inEdges[ref.Producer] {
			if _, isQA := r.index[e.SourceNodeID].(*ir.QA); isQA {
				r.scheduleEdge(e)
				r.branchEntries[ref.Producer] = append(r.branchEntries[ref.Producer], e)
			}
		}
	}
}

func (r *irRenderer) terminalEndFromAggregate(id string, seen map[string]bool) string {
	if seen[id] {
		return ""
	}
	seen[id] = true
	for _, edge := range r.irEdges {
		if edge.SourceNodeID != id {
			continue
		}
		switch r.index[edge.TargetNodeID].(type) {
		case *ir.End:
			return edge.TargetNodeID
		case *ir.Aggregate:
			if end := r.terminalEndFromAggregate(edge.TargetNodeID, seen); end != "" {
				return end
			}
		}
	}
	return ""
}

// newRenderAST builds the constant structural shell (envelope, features,
// viewport, empty variable lists) shared by every workflow on the frozen
// corpus — verified constant across all 65 goldens.
func newRenderAST(mode string) *WorkflowAST {
	return &WorkflowAST{
		Envelope: EnvelopeNode{
			Version: "0.6.0",
			Kind:    "app",
			App:     AppNode{Mode: mode, Name: ""},
		},
		Graph:                 GraphNode{Viewport: &ViewportNode{X: 0, Y: 0, Zoom: 1.0}},
		Features:              defaultFeatures(),
		ConversationVariables: []ConversationVariable{},
		EnvironmentVariables:  []EnvironmentVariable{},
		Dependencies:          []map[string]any{},
		RagPipelineVariables:  []string{},
	}
}

// defaultFeatures returns the constant features block shared by every
// workflow on the frozen corpus (verified identical across all 65 goldens).
func defaultFeatures() WorkflowFeatures {
	fu := NewFileUploadConfig()
	return WorkflowFeatures{
		FileUpload:                    &fu,
		SensitiveWordAvoidance:        FeatureToggle{Enabled: false},
		TextToSpeech:                  &TextToSpeechConfig{Enabled: false, Language: "", Voice: ""},
		OpeningStatement:              "",
		RetrieverResource:             FeatureToggle{Enabled: false},
		SpeechToText:                  FeatureToggle{Enabled: false},
		SuggestedQuestions:            []string{},
		SuggestedQuestionsAfterAnswer: FeatureToggle{Enabled: false},
	}
}

// irRenderer tracks the platform-visible naming state: global node/edge
// counters, per-type title counts, the IR-ID → Dify-ID map every dataflow
// reference must pass through, and the allocated end-output variable names.
type irRenderer struct {
	ast          *WorkflowAST
	mode         string
	toolBindings *ToolBindingManifest
	registry     *tool.Registry // closed-world capability definitions (schema)
	nodeSeq      int
	edgeSeq      int
	titles       map[string]int
	difyIDs      map[string]string // ir.NodeID → dify node id
	inputNodeIDs map[string]string // ir.NodeID → expanded entry node id
	index        map[string]ir.Node
	outputVars   map[string]bool

	irEdges            []flow.Edge
	inEdges            map[string][]flow.Edge     // ir target id → incoming edges
	skipNodes          map[string]bool            // decomposed aggregates + terminal ends
	branchEnds         map[string][]branchEndRef  // branch source → return refs (src+port for direct branches)
	qaEnds             map[string][]qaEndRef      // request_input QA id → direct branch ends
	switchHasDefault   map[string]bool            // switch id → has a default branch (dify rejects it)
	scheduledEdges     map[irEdgeKey]bool         // edges emitted by their owning structured statement
	iterationStarts    map[string]string          // projected loop id → Dify iteration-start id
	iterationFolded    map[string]bool            // direct yield of the iteration item needs no body node
	loopOutputUsed     map[string]bool            // projected loop output referenced outside its body
	loopChildren       map[string]bool            // IR nodes owned by projected loop bodies
	pendingLoopEdges   map[string][]EdgeDecl      // synthesized Dify Loop edges appended after the outer entry
	pendingLoopEntries map[string][]EdgeDecl      // replacement outer entry chains for synthesized Dify Loops
	loopEscapes        map[string]string          // loop IR id → return-check Dify id
	portRedirect       map[string]string          // IR producer id → synthesized output port
	typeRedirect       map[string]string          // IR producer id → synthesized Dify value type
	branchEntries      map[string][]flow.Edge     // branch tail → entry edges emitted after its return
	attemptEntries     map[string][]flow.Edge     // branch End → attempt entry edges emitted after the End
	selCases           map[string]int             // selector dify id → case count (edge handles)
	answerFolds        map[string][]answerFoldRef // folded end/producer id → return refs (multi-branch shared end: one per exit)
	endReturns         map[string][]ir.Ref        // shared end id → branch return values (IR-tree order)
	premint            map[string]string          // ir node id → pre-minted dify id (condition materializer + its selector)
	condOwner          map[string]string          // condition materializer ir id → selector ir id
	condSelEdges       map[string]flow.Edge       // condition materializer ir id → edge into its selector
	jinjaTemplates     map[string]string          // textprocess ir id → template with ternary Jinja substituted
	jinjaInputs        map[string][]ir.Source     // textprocess ir id → inputs after ternary folding
	extractionEdges    map[string]EdgeDecl        // extraction ir id → extractor-to-adapter edge
	inlinePlans        map[string]*ir.InlineStmt  // inline body code id → function boundary
	inlineEdges        map[string][]EdgeDecl      // inline body code id → synthesized internal edges
	inlineResults      map[string]bool            // inline body code id → result wrapper mapping
	envelopeAdapters   map[string]string          // semantic LLM id -> target-local assembly adapter id
}

// qaEndRef is one direct branch end of a request_input QA: the branch
// return value ref plus the IR source port (branch_i / default) the end's
// incoming edge fans out from.
type qaEndRef struct {
	ref  ir.Ref
	port string
}

// branchEndRef is one decomposed branch return: the value ref plus the
// branch source node and IR source port the end's incoming edge should
// connect from. A direct branch return (value comes from an upstream
// producer such as the start node, with no intermediate branch node) keeps
// src = the selector, so the end hangs off the selector's case port like
// the legacy lowering; a branch with an intermediate node (llm/code) uses
// src = the branch tail producer itself.
type branchEndRef struct {
	ref  ir.Ref
	src  string // IR node id of the end's incoming-edge source
	port string // IR source port ("" for plain tails, "true"/"false"/branch_i)
	// void marks a branch terminating in a bare return; (no value to
	// carry). The branch still needs an out edge — dify if-else nodes have
	// exactly true/false ports and cannot leave a branch dangling — so the
	// renderer synthesizes a no-output End for it (program augmentation:
	// the void return branch joins the augmented return' terminal).
	void bool
}

func (r *irRenderer) nextNodeID(kind string) string {
	r.nodeSeq++
	return r.nodeIDFor(kind, r.nodeSeq)
}

func (r *irRenderer) nodeIDFor(kind string, seq int) string {
	return fmt.Sprintf("%s_%d", kind, seq)
}

// idFor returns the dify node id of a real IR node. Nodes that take part
// in a condition-materializer pair carry a pre-minted id (the selector is
// minted before its materializer, legacy GenerateNodeID order); every
// other node keeps the emission-order id.
func (r *irRenderer) idFor(n ir.Node) string {
	if id, ok := r.premint[n.NodeID()]; ok {
		return id
	}
	return r.nextNodeID(nodeIDPrefix(n))
}

// reserveConditionID pre-mints the ids of a condition materializer and its
// selector at the materializer's emission point. The selector's node is
// emitted right after the materializer, but legacy mints it first
// (buildIfBranchNode: GenerateNodeID("if"), then the materializer), so the
// selector gets the lower number while the node list order stays put.
func (r *irRenderer) reserveConditionID(n *ir.CodeRun) {
	if _, ok := r.premint[n.NodeID()]; ok {
		return
	}
	selID := r.condOwner[n.NodeID()]
	if _, ok := r.premint[selID]; !ok {
		r.nodeSeq++
		r.premint[selID] = r.nodeIDFor("if", r.nodeSeq)
	}
	r.nodeSeq++
	r.premint[n.NodeID()] = r.nodeIDFor("condition", r.nodeSeq)
	// Register both ids up front: the materializer's selector edge is
	// emitted before the selector node itself, so the dify id must already
	// be resolvable (emitNode re-registers the same id later).
	r.difyIDs[selID] = r.premint[selID]
	r.difyIDs[n.NodeID()] = r.premint[n.NodeID()]
}

// nodeIDPrefix mirrors the id prefix each emit* function uses, so idFor's
// fallback reproduces exactly the emission-order ids the renderer already
// produces (only condition materializers diverge, via reserveConditionID).
func nodeIDPrefix(n ir.Node) string {
	switch nd := n.(type) {
	case *ir.Start:
		return "start"
	case *ir.End:
		// Structured returns (NamedReturn) and implicit ends share the "end"
		// prefix; the legacy continuing End ("output" prefix) is retired.
		return "end"
	case *ir.LLMCall:
		return "llm"
	case *ir.HTTPCall:
		return "http"
	case *ir.ToolCall:
		return "tool"
	case *ir.AgentCall:
		return "agent"
	case *ir.KnowledgeCall:
		return "knowledge"
	case *ir.CodeRun:
		if nd.Condition {
			return "condition"
		}
		return "code"
	case *ir.ListOp:
		return "list_operator"
	case *ir.Selector:
		return "if"
	case *ir.Aggregate:
		return "variable-aggregator"
	case *ir.QA:
		return "human_input"
	case *ir.Output:
		return "answer"
	case *ir.Assign:
		return "assigner"
	case *ir.TextProcess:
		switch nd.Method {
		case "split":
			return "code"
		default:
			if len(nd.Inputs) == 0 && !strings.Contains(nd.Template, "{{") {
				return "code"
			}
			return "template"
		}
	}
	return "node"
}

// emitConditionEdge emits a condition materializer's edge into its
// selector at the materializer's emission position (before the
// materializer's incoming edges), mirroring the legacy buildIfBranchNode
// AddEdge timing. emitInEdges skips these edges on the selector side.
func (r *irRenderer) emitConditionEdge(e flow.Edge) {
	r.ast.Graph.Edges = append(r.ast.Graph.Edges, EdgeDecl{
		ID:           r.nextEdgeID(),
		Source:       r.difyIDs[e.SourceNodeID],
		Target:       r.difyIDs[e.TargetNodeID],
		Type:         "custom",
		SourceHandle: sourceHandleFor(e.SourcePortID),
	})
}

func (r *irRenderer) nextEdgeID() string {
	r.edgeSeq++
	return fmt.Sprintf("edge_%d", r.edgeSeq)
}

// titleFor mirrors lowering GenerateTitle: base i18n title, numbered on
// repeat occurrences of the same node type.
func (r *irRenderer) titleFor(nodeType string) string {
	count := r.titles[nodeType] + 1
	r.titles[nodeType] = count
	base, ok := DefaultTitles[nodeType]
	if !ok {
		base = nodeType
	}
	if count <= 1 {
		return base
	}
	return fmt.Sprintf("%s %d", base, count)
}

// emitNode dispatches one IR node to its Dify shape. All nodes are
// "custom" React-Flow declarations (NodeDecl.NodeType = "custom").
func (r *irRenderer) emitNode(n ir.Node) error {
	if loop, ok := n.(*flow.ProjectedLoop); ok {
		return r.emitProjectedLoop(loop)
	}
	var node NodeDecl
	var kind string
	switch nd := n.(type) {
	case *ir.Start:
		kind, node = "start", r.emitStart(nd)
	case *ir.End:
		kind, node = "end", r.emitEnd(nd)
	case *ir.LLMCall:
		if nd.Extraction != nil {
			return r.emitExtraction(nd)
		}
		kind, node = "llm", r.emitLLM(nd)
	case *ir.HTTPCall:
		kind, node = "http-request", r.emitHTTP(nd)
	case *ir.ToolCall:
		kind = "tool"
		var err error
		node, err = r.emitTool(nd)
		if err != nil {
			return err
		}
	case *ir.AgentCall:
		kind, node = "agent", r.emitAgent(nd)
	case *ir.KnowledgeCall:
		kind, node = "knowledge-retrieval", r.emitKnowledge(nd)
	case *ir.CodeRun:
		if plan := r.inlinePlans[nd.NodeID()]; plan != nil {
			return r.emitInlineCall(nd, plan)
		}
		if nd.Condition {
			r.reserveConditionID(nd)
		}
		kind, node = "code", r.emitCode(nd)
	case *ir.ListOp:
		kind, node = "list-operator", r.emitListOpNode(nd)
	case *ir.Selector:
		kind, node = "if-else", r.emitSelector(nd)
	case *ir.Aggregate:
		kind, node = "variable-aggregator", r.emitAggregate(nd)
	case *ir.QA:
		kind, node = "human-input", r.emitQA(nd)
	case *ir.Switch:
		if r.switchHasDefault[nd.ID] {
			return &RenderError{Kind: "switch", Reason: "dify question-classifier has no default port (classify switch default branch cannot be rendered)"}
		}
		kind, node = "question-classifier", r.emitSwitch(nd)
	case *ir.Output:
		if r.mode == "workflow" {
			// workflow 模式: output(expr) 翻译为 Dify output 节点
			// (type: end)——平台"输出"概念; 最终结果收集是平台的事。
			kind, node = "output", r.emitWorkflowOutput(nd)
		} else {
			// chatflow 模式: 翻译为 answer 节点(内容投递载体)。
			kind, node = "answer", r.emitOutput(nd)
		}
	case *ir.Assign:
		kind, node = "assigner", r.emitAssign(nd)
	case *ir.TextProcess:
		kind, node = r.emitText(nd)
	case *ir.SubWorkflowCall:
		// SUBWORKFLOW-DECLARATION：dify 无子工作流节点，编译期硬报错
		// （不静默降级、不产出无法导入的产物）。
		return &RenderError{Kind: "sub_workflow", Reason: "dify has no sub-workflow node (SUBWORKFLOW-DECLARATION: subflow is coze-studio/coze-biz only)"}
	default:
		return &RenderError{Kind: string(n.Kind())}
	}
	// register dify id + auto-fill title (like lowering AddNode)
	r.difyIDs[n.NodeID()] = node.ID
	if getDataTitle(node.Data) == "" {
		setDataTitle(node.Data, r.titleFor(kind))
	}
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, node)
	return nil
}

// emitInEdges emits the incoming edges of the just-emitted node, in IR
// order. Dify orders edges by target emission (an edge appears when its
// target node is added), and selector sources fan out per case.
func (r *irRenderer) emitInEdges(irTarget string) error {
	if entries, ok := r.pendingLoopEntries[irTarget]; ok {
		for _, edge := range entries {
			edge.ID = r.nextEdgeID()
			r.ast.Graph.Edges = append(r.ast.Graph.Edges, edge)
		}
		return nil
	}
	for _, e := range r.inEdges[irTarget] {
		if r.isStructurallyScheduled(e) {
			continue
		}
		if _, isCond := r.condSelEdges[e.SourceNodeID]; isCond {
			continue // emitted at the materializer's position
		}
		src, ok := r.difyIDs[e.SourceNodeID]
		if !ok {
			return &RenderError{Kind: "edge", Reason: "unknown source node " + e.SourceNodeID}
		}
		tgt := r.difyIDs[e.TargetNodeID]
		if expanded := r.inputNodeIDs[e.TargetNodeID]; expanded != "" {
			tgt = expanded
		}
		handles := r.edgeHandles(e)
		for _, h := range handles {
			r.ast.Graph.Edges = append(r.ast.Graph.Edges, EdgeDecl{
				ID:           r.nextEdgeID(),
				Source:       src,
				Target:       tgt,
				Type:         "custom",
				SourceHandle: h,
			})
		}
	}
	return nil
}

// emitIterationInnerEdges renders the projected body flow after the outer
// edge into the Iteration container. The semantic loop entry becomes the
// Dify iteration-start node; the body-to-container projection edge only
// closes the semantic subgraph and has no Dify edge counterpart.
func (r *irRenderer) emitProjectedLoopEdges(loop *flow.ProjectedLoop) error {
	if edges, ok := r.pendingLoopEdges[loop.NodeID()]; ok {
		for _, edge := range edges {
			edge.ID = r.nextEdgeID()
			r.ast.Graph.Edges = append(r.ast.Graph.Edges, edge)
		}
		return nil
	}
	if r.iterationFolded[loop.NodeID()] {
		return nil
	}
	startID := r.iterationStarts[loop.NodeID()]
	for _, e := range loop.InnerEdges {
		if e.TargetNodeID == loop.NodeID() {
			continue
		}
		sourceID := r.difyIDs[e.SourceNodeID]
		handles := r.edgeHandles(e)
		if e.SourceNodeID == loop.NodeID() {
			sourceID = startID
			handles = []string{""}
		}
		targetID := r.difyIDs[e.TargetNodeID]
		if sourceID == "" || targetID == "" {
			return &RenderError{Kind: "iteration edge", Reason: "unknown projected child"}
		}
		for _, handle := range handles {
			r.ast.Graph.Edges = append(r.ast.Graph.Edges, EdgeDecl{
				ID:            r.nextEdgeID(),
				Source:        sourceID,
				Target:        targetID,
				Type:          "custom",
				SourceHandle:  handle,
				IsInIteration: boolPtr(true),
				IterationID:   r.difyIDs[loop.NodeID()],
			})
		}
	}
	return nil
}

// edgeHandles resolves the dify source handles for one IR edge: selector
// true-edges fan out per case (one for a single and-condition, one per
// or-condition), false-edges carry "false", everything else keeps the IR
// port (default → "").
func (r *irRenderer) edgeHandles(e flow.Edge) []string {
	switch e.SourcePortID {
	case "true":
		if _, ok := r.index[e.SourceNodeID].(*ir.Selector); ok {
			selID := r.difyIDs[e.SourceNodeID]
			count := r.selCases[selID]
			if count < 1 {
				count = 1
			}
			handles := make([]string, 0, count)
			for i := 0; i < count; i++ {
				handles = append(handles, fmt.Sprintf("%s_case_%d", selID, i))
			}
			return handles
		}
		return []string{"true"}
	case "false":
		return []string{"false"}
	default:
		if sw, ok := r.index[e.SourceNodeID].(*ir.Switch); ok {
			return []string{r.switchHandle(sw, e.SourcePortID)}
		}
		return []string{sourceHandleFor(e.SourcePortID)}
	}
}

// emitBranchEnd synthesizes one per-branch End node for a decomposed
// branch return (Dify terminates each return branch with its own end).
// splitBranchEnds partitions a node's decomposed branch ends into those
// that emit before the incoming edges (materialized-literal returns) and
// those that emit after (intermediate-node returns), preserving order.
func (r *irRenderer) splitBranchEnds(ends []branchEndRef) (pre, post []branchEndRef) {
	for _, be := range ends {
		if tp, ok := r.index[be.ref.Producer].(*ir.TextProcess); ok && tp.Answer {
			pre = append(pre, be)
			continue
		}
		if _, isText := r.index[be.ref.Producer].(*ir.TextProcess); isText &&
			len(r.index[be.ref.Producer].(*ir.TextProcess).Inputs) == 0 {
			pre = append(pre, be)
			continue
		}
		post = append(post, be)
	}
	return pre, post
}

func (r *irRenderer) emitBranchEnd(be branchEndRef) {
	if be.void {
		// void return 分支：无输出 End（与顶层 void return 的 end 节点同形）。
		r.emitVoidEndFor(be)
		return
	}
	// advanced-chat: every terminal branch renders an Answer node (the
	// legacy lowerReturnFromTails answer path: literal/template branches
	// fold via answerFolds, ref returns inline as {{#node.port#}}).
	if r.mode == "advanced-chat" {
		r.emitBranchAnswer(be)
		return
	}
	// branch return of a template literal folds into an Answer node.
	if tp, ok := r.index[be.ref.Producer].(*ir.TextProcess); ok && tp.Answer {
		r.emitAnswerFold("", answerFoldRef{ref: be.ref})
		return
	}
	r.emitEndFor(be, r.branchHandles(be))
}

// emitBranchAnswer renders one advanced-chat branch return as an Answer
// node: the value inlines as {{#producer.port#}} (TextProcess producers
// already fold through answerFolds; this covers ref returns off nodes such
// as LLM). The answer hangs off the branch tail producer, mirroring the
// legacy llm → answer edge.
func (r *irRenderer) emitBranchAnswer(be branchEndRef) {
	answerID := r.nextNodeID("answer") // answer node id prefix matches the Python/legacy wire
	answer := "{{#" + r.difyIDs[be.ref.Producer] + "." + r.portFor(be.ref.Producer, be.ref.Output) + "#}}"
	node := MakeAnswerNode(answerID, answer)
	setDataTitle(node.Data, r.titleFor("answer"))
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, node)
	src := be.src
	if src == "" {
		src = be.ref.Producer
	}
	id, ok := r.difyIDs[src]
	if !ok {
		return
	}
	handles := []string{sourceHandleFor(be.port)}
	if _, isSel := r.index[src].(*ir.Selector); isSel {
		handles = r.edgeHandles(flow.Edge{SourceNodeID: src, SourcePortID: be.port})
	}
	for _, h := range handles {
		r.ast.Graph.Edges = append(r.ast.Graph.Edges, EdgeDecl{
			ID:           r.nextEdgeID(),
			Source:       id,
			Target:       answerID,
			Type:         "custom",
			SourceHandle: h,
		})
	}
}

// branchHandles resolves the dify source handles for one decomposed branch
// end's incoming edge, mirroring edgeHandles (selector true-ports fan out
// per case; false/plain ports pass through).
func (r *irRenderer) branchHandles(be branchEndRef) []string {
	if _, ok := r.index[be.src].(*ir.Selector); ok {
		return r.edgeHandles(flow.Edge{SourceNodeID: be.src, SourcePortID: be.port})
	}
	return []string{sourceHandleFor(be.port)}
}

// emitVoidEndFor renders the augmented terminal for a void (bare-return)
// branch: a no-output end node with the branch port edge (selector
// true/false → end). Mirrors the top-level void return rendering
// (MakeOutputNode with no mappings → outputs: []).
func (r *irRenderer) emitVoidEndFor(be branchEndRef) {
	endID := r.nextNodeID("end")
	node := MakeOutputNode(endID, nil)
	setDataTitle(node.Data, r.titleFor("end"))
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, node)
	src := be.src
	if id, ok := r.difyIDs[be.src]; ok {
		src = id
	}
	for _, h := range r.branchHandles(be) {
		r.ast.Graph.Edges = append(r.ast.Graph.Edges, EdgeDecl{
			ID:           r.nextEdgeID(),
			Source:       src,
			Target:       endID,
			Type:         "custom",
			SourceHandle: h,
		})
	}
}

// emitQAEnd emits one direct branch end of a request_input QA node: the
// end connects straight from the human-input's action port
// (approve/reject/__timeout), mirroring the legacy human-input lowerer.
func (r *irRenderer) emitQAEnd(qa *ir.QA, qe qaEndRef) {
	r.emitEndFor(branchEndRef{ref: qe.ref, src: qa.NodeID(), port: qe.port}, []string{r.qaHandle(qa, qe.port)})
}

// emitEndFor synthesizes one End node for a decomposed branch return with
// its incoming edge(s) (src → end), emitted at the src's position.
func (r *irRenderer) emitEndFor(be branchEndRef, handles []string) {
	endID := r.nextNodeID("end")
	producer := r.difyIDs[be.ref.Producer]
	src := producer
	if be.src != "" {
		if id, ok := r.difyIDs[be.src]; ok {
			src = id
		}
	}
	node := MakeOutputNode(endID, []OutputMapping{{
		Variable:      r.allocOutput("output"),
		ValueType:     r.portType(be.ref.Producer, be.ref.Output),
		ValueSelector: r.outputSelector(be.ref.Producer, be.ref.Output),
	}})
	setDataTitle(node.Data, r.titleFor("end"))
	r.ast.Graph.Nodes = append(r.ast.Graph.Nodes, node)
	// edge(s) src → end, emitted at the end's emission point.
	for _, h := range handles {
		r.ast.Graph.Edges = append(r.ast.Graph.Edges, EdgeDecl{
			ID:           r.nextEdgeID(),
			Source:       src,
			Target:       endID,
			Type:         "custom",
			SourceHandle: h,
		})
	}
}

// qaHandle maps a QA branch port onto the Dify human-input action handle:
// branch_i → the i-th action member, default → __timeout.
func (r *irRenderer) qaHandle(qa *ir.QA, port string) string {
	if port == "default" {
		return "__timeout"
	}
	if i, ok := parseBranchIndex(port); ok && i < len(qa.Choices) {
		return qa.Choices[i]
	}
	return port
}

// switchHandle maps a switch branch port onto the Dify
// question-classifier class handle: branch_i → the i-th class
// name. Dify has no default port (each class has one out edge);
// the default branch is rejected during planStructured.
func (r *irRenderer) switchHandle(sw *ir.Switch, port string) string {
	if i, ok := parseBranchIndex(port); ok && i < len(sw.Intents) {
		return sw.Intents[i]
	}
	return port
}

func parseBranchIndex(port string) (int, bool) {
	if !strings.HasPrefix(port, "branch_") {
		return 0, false
	}
	i, err := strconv.Atoi(strings.TrimPrefix(port, "branch_"))
	if err != nil {
		return 0, false
	}
	return i, true
}

func (r *irRenderer) scheduleEdge(edge flow.Edge) {
	r.scheduledEdges[keyForEdge(edge)] = true
}

func (r *irRenderer) isStructurallyScheduled(edge flow.Edge) bool {
	return r.scheduledEdges[keyForEdge(edge)]
}

// emitStructuredEntry emits a branch entry at the point dictated by its
// owning If/Qa/Attempt statement rather than the target node's flat position.
func (r *irRenderer) emitStructuredEntry(e flow.Edge) {
	src, ok := r.difyIDs[e.SourceNodeID]
	if !ok {
		return
	}
	handle := sourceHandleFor(e.SourcePortID)
	if qa, ok := r.index[e.SourceNodeID].(*ir.QA); ok {
		handle = r.qaHandle(qa, e.SourcePortID)
	}
	if sw, ok := r.index[e.SourceNodeID].(*ir.Switch); ok {
		handle = r.switchHandle(sw, e.SourcePortID)
	}
	r.ast.Graph.Edges = append(r.ast.Graph.Edges, EdgeDecl{
		ID:           r.nextEdgeID(),
		Source:       src,
		Target:       r.difyIDs[e.TargetNodeID],
		Type:         "custom",
		SourceHandle: handle,
	})
}

type irEdgeKey struct {
	source, target, sourcePort, targetPort string
}

func keyForEdge(edge flow.Edge) irEdgeKey {
	return irEdgeKey{edge.SourceNodeID, edge.TargetNodeID, edge.SourcePortID, edge.TargetPortID}
}

// sourceHandleFor maps an IR source-port id to the Dify edge sourceHandle.
func sourceHandleFor(port string) string {
	switch port {
	case "", "default":
		return ""
	case "true", "false":
		return port
	default:
		return port
	}
}

// rewriteTemplate converts coze-style IR template refs into Dify template
// syntax: {{block_output_<irID>.<port>}} → {{#<difyID>.<port>#}}.
var blockOutputRe = regexp.MustCompile(`\{\{block_output_([0-9]+)(\.[^}]+)?\}\}`)

// conversationRefRe matches the IR's coze-style global variable reference
// ({{global_variable_app.<name>}}); Dify renders it as a conversation
// variable reference.
var conversationRefRe = regexp.MustCompile(`\{\{global_variable_app\.([A-Za-z0-9_]+)\}\}`)

func (r *irRenderer) rewriteTemplate(s string) string {
	s = conversationRefRe.ReplaceAllString(s, "{{#conversation.$1#}}")
	return blockOutputRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := blockOutputRe.FindStringSubmatch(m)
		irID := sub[1]
		id, ok := r.difyIDs[irID]
		if !ok {
			return m
		}
		port := sub[2]
		if port != "" {
			port = "." + r.portFor(irID, port[1:])
		}
		return "{{#" + id + port + "#}}"
	})
}

// ── per-kind translates ────────────────────────────────────────────

func (r *irRenderer) emitStart(s *ir.Start) NodeDecl {
	vars := make([]VariableDef, 0, len(s.Params))
	for _, p := range s.Params {
		inputType := inputTypeFor(p.Type)
		fileType := p.Type
		if fileType != nil && fileType.Kind == "list" {
			fileType = fileType.Elem
		}
		if fileType == nil {
			fileType = &ir.Type{}
		}
		vars = append(vars, VariableDef{
			Variable:                 p.Name,
			Label:                    p.Name,
			Type:                     inputType,
			Required:                 !p.Optional,
			Default:                  p.Default,
			AllowedFileTypes:         append([]string(nil), fileType.AllowedFileTypes...),
			AllowedFileExtensions:    append([]string(nil), fileType.AllowedFileExtensions...),
			AllowedFileUploadMethods: append([]string(nil), fileType.AllowedFileUploadMethods...),
		})
	}
	return MakeStartNode(r.idFor(s), vars)
}

func (r *irRenderer) emitEnd(e *ir.End) NodeDecl {
	outputs := make([]OutputMapping, 0, len(e.Inputs)+len(e.Outputs))
	for _, ref := range e.Inputs {
		outputs = append(outputs, OutputMapping{
			Variable:      r.allocOutput("output"),
			ValueType:     r.portType(ref.Producer, ref.Output),
			ValueSelector: r.outputSelector(ref.Producer, ref.Output),
		})
	}
	for _, o := range e.Outputs {
		outputs = append(outputs, OutputMapping{
			Variable:      o.Name,
			ValueType:     r.portType(o.Ref.Producer, o.Ref.Output),
			ValueSelector: r.outputSelector(o.Ref.Producer, o.Ref.Output),
		})
	}
	return MakeOutputNode(r.idFor(e), outputs)
}

func (r *irRenderer) outputSelector(producerID, output string) []string {
	if output == ir.LLMEnvelopeOutput {
		if adapterID := r.envelopeAdapters[producerID]; adapterID != "" {
			return []string{r.difyIDs[adapterID], ir.LLMEnvelopeOutput}
		}
	}
	path := []string{r.difyIDs[producerID]}
	path = append(path, strings.Split(r.portFor(producerID, output), ".")...)
	return path
}

// portFor maps an IR output port onto the Dify port of the producing
// node. The IR funnels single-output families (LLM/Code) onto "output";
// Dify names those ports "text" (LLM) / the output-dict key (Code).
func (r *irRenderer) portFor(producerID, irPort string) string {
	if port := r.portRedirect[producerID]; port != "" {
		return port
	}
	if r.inlineResults[producerID] {
		if irPort == "output" || irPort == "result" {
			return "result"
		}
		return "result." + irPort
	}
	n, ok := r.index[producerID]
	if !ok {
		return irPort
	}
	switch nd := n.(type) {
	case *flow.ProjectedLoop:
		if nd.LoopType == "by_array" {
			if _, ok := nd.StateTypes[irPort]; ok {
				return "item"
			}
		}
	case *ir.CodeRun:
		// json.parse<T> shim（G11）不再需要特判：IR 侧承载口与 dify code 节点的
		// 真实口同名（都是 `output`，见 pkg/ir/build_node.go 的三个形态）。
		// 曾经的 `output.<field>` → 逐字段口前缀剥离，是 IR 用「合成 dotted 口」
		// 表达时的适配；承载口合一后它会把 ref 剥成不存在的口（实测
		// `LOWERING_INVARIANT … port 'text' … available: ['output']`）。
		if irPort == "output" {
			if k, ok2 := singleTypeKey(nd.OutTypes); ok2 {
				return k
			}
			if k, ok2 := singleTypeKey(nd.Types); ok2 {
				return k
			}
		}
		if _, ok := nd.OutTypes[irPort]; ok {
			return irPort
		}
		// A structured code return is represented in the IR as named fields
		// under the single Dify code port `result`. Keep the semantic field
		// path when crossing that platform boundary.
		if nestedPortType(nd.OutTypes, "result."+irPort) != nil {
			return "result." + irPort
		}
	case *ir.LLMCall:
		if nd.Result != nil && nd.Result.PayloadSchema != nil && nd.Result.PayloadSchema.Kind != "string" {
			if irPort == "text" || irPort == "output" {
				return "structured_output.value"
			}
			if strings.HasPrefix(irPort, "text.") {
				return "structured_output.value." + strings.TrimPrefix(irPort, "text.")
			}
		}
		if irPort == "output" || irPort == "text" {
			return "text"
		}
	case *ir.KnowledgeCall:
		if irPort == "outputList" {
			return "result"
		}
	case *ir.HTTPCall:
		if irPort == "statusCode" {
			return "status_code"
		}
	case *ir.ToolCall:
		// Dify tools expose a single "result" port. The IR preserves the
		// full semantic member path on tool refs (registry-declared result
		// records); every member funnels onto the tool's result port.
		return "result"
	case *ir.Aggregate:
		// A grouped Dify aggregator exposes each semantic field as
		// `<group>.output`; the ungrouped legacy form exposes `output`.
		if len(nd.Groups) > 0 {
			return irPort + ".output"
		}
		return "output"
	case *ir.ListOp:
		if irPort == "output" {
			return "result"
		}
	case *ir.TextProcess:
		if nd.Method == "split" {
			return "result"
		}
		// a pure-literal text materializes as a code node whose single
		// output is "result".
		if irPort == "output" && len(nd.Inputs) == 0 && !strings.Contains(nd.Template, "{{") {
			return "result"
		}
		return "output"
	case *ir.QA:
		// a structured field's enum `.value` projection selects the field
		// itself in Dify (the human-input select already yields the wire
		// value).
		if strings.HasSuffix(irPort, ".value") {
			return strings.TrimSuffix(irPort, ".value")
		}
	case *ir.AgentCall:
		// OperationResult[agent] record fields are the agent node's output
		// ports (oracle: value_selector ["agent_2", "text"]).
		return irPort
	}
	return irPort
}

// singleTypeKey returns the sole key of a code-output map, if exactly one.
func singleTypeKey(m map[string]*ir.Type) (string, bool) {
	if len(m) != 1 {
		return "", false
	}
	for k := range m {
		return k, true
	}
	return "", false
}

// allocOutput mirrors lowering AllocateOutputVariable: first end gets the
// preferred name, later ones get a numeric suffix.
func (r *irRenderer) allocOutput(preferred string) string {
	candidate := preferred
	suffix := 2
	for r.outputVars[candidate] {
		candidate = fmt.Sprintf("%s_%d", preferred, suffix)
		suffix++
	}
	r.outputVars[candidate] = true
	return candidate
}

// portType resolves the dify value_type of a producer port. Falls back to
// "string" (the corpus default for untyped refs).
func (r *irRenderer) portType(producerID, port string) string {
	if port == ir.LLMEnvelopeOutput && r.envelopeAdapters[producerID] != "" {
		return "object"
	}
	if valueType := r.typeRedirect[producerID]; valueType != "" {
		return valueType
	}
	n, ok := r.index[producerID]
	if !ok {
		return "string"
	}
	switch nd := n.(type) {
	case *flow.ProjectedLoop:
		if port == "output" {
			return r.iterationOutputType(nd)
		}
		if nd.LoopType == "by_array" {
			if _, ok := nd.StateTypes[port]; ok {
				return r.iterationItemType(nd)
			}
		}
		if t, ok := nd.StateTypes[port]; ok {
			return codeValueTypeFor(t)
		}
	case *ir.Start:
		for _, p := range nd.Params {
			if p.Name == port {
				return startValueTypeFor(p.Type)
			}
		}
	case *ir.LLMCall:
		semanticPort := port
		if strings.HasPrefix(semanticPort, "structured_output.value") {
			semanticPort = "text" + strings.TrimPrefix(semanticPort, "structured_output.value")
		}
		if t, ok := nd.OutTypes[semanticPort]; ok {
			return valueTypeFor(t)
		}
		if t := nestedPortType(nd.OutTypes, semanticPort); t != nil {
			return valueTypeFor(t)
		}
		if port == "text" {
			return "string"
		}
	case *ir.CodeRun:
		// json.parse<T> shim（G11）：承载口 = 真实口 `output`（同 portFor 的说明），
		// 类型按 `output` 口的记录类型逐层解析，不需要前缀剥离特判。
		if port == "output" {
			if k, ok2 := singleTypeKey(nd.OutTypes); ok2 {
				if t, ok3 := nd.OutTypes[k]; ok3 {
					return codeValueTypeFor(t)
				}
			}
			if k, ok2 := singleTypeKey(nd.Types); ok2 {
				if t, ok3 := nd.Types[k]; ok3 {
					return codeValueTypeFor(t)
				}
			}
		}
		if t, ok := nd.OutTypes[port]; ok {
			return codeValueTypeFor(t)
		}
		if t := nestedPortType(nd.OutTypes, port); t != nil {
			return codeValueTypeFor(t)
		}
		if t := nestedPortType(nd.OutTypes, "result."+port); t != nil {
			return codeValueTypeFor(t)
		}
		if t, ok := nd.Types[port]; ok {
			return codeValueTypeFor(t)
		}
	case *ir.Aggregate:
		refs := nd.AllRefs()
		if len(nd.Groups) > 0 {
			for _, group := range nd.Groups {
				if group.Name == port {
					if group.Type != nil {
						return valueTypeFor(group.Type)
					}
					refs = group.Refs
					break
				}
			}
		}
		if len(refs) > 0 {
			return r.portType(refs[0].Producer, refs[0].Output)
		}
	case *ir.KnowledgeCall:
		if port == "outputList" {
			return "array[object]"
		}
	case *ir.QA:
		if strings.HasSuffix(port, ".value") {
			port = strings.TrimSuffix(port, ".value")
		}
		if t, ok := nd.OutTypes[port]; ok {
			// an enum-typed select field (object {value: string}) yields
			// the wire string.
			if t.Kind == "object" {
				if v, ok2 := t.Props["value"]; ok2 {
					return valueTypeFor(v)
				}
			}
			return valueTypeFor(t)
		}
	case *ir.TextProcess:
		if port == "text" || port == "value" || port == "result" {
			return "string"
		}
	case *ir.ListOp:
		if port == "result" || port == "output" {
			item := nd.ItemType
			if item == nil {
				item = ir.T("string")
			}
			return "array[" + codeValueTypeFor(item) + "]"
		}
	}
	return "string"
}

func nestedPortType(outputs map[string]*ir.Type, port string) *ir.Type {
	parts := strings.Split(port, ".")
	if len(parts) < 2 {
		return nil
	}
	t := outputs[parts[0]]
	for _, part := range parts[1:] {
		if t == nil || t.Props == nil {
			return nil
		}
		t = t.Props[part]
	}
	return t
}

func (r *irRenderer) iterationItemType(loop *flow.ProjectedLoop) string {
	if len(loop.Inputs) > 0 {
		inputType := r.portType(loop.Inputs[0].BlockID, loop.Inputs[0].Name)
		if strings.HasPrefix(inputType, "array[") && strings.HasSuffix(inputType, "]") {
			return strings.TrimSuffix(strings.TrimPrefix(inputType, "array["), "]")
		}
	}
	for _, t := range loop.StateTypes {
		return codeValueTypeFor(t)
	}
	return "string"
}

func (r *irRenderer) iterationOutputType(loop *flow.ProjectedLoop) string {
	if len(loop.OutputSources) == 0 {
		return ""
	}
	source := loop.OutputSources[0]
	elementType := "string"
	if source.BlockID == loop.NodeID() {
		elementType = r.iterationItemType(loop)
	} else {
		elementType = r.portType(source.BlockID, source.Name)
	}
	return "array[" + elementType + "]"
}

func nodeReferencesProducer(n ir.Node, producerID string) bool {
	refs := func(values []ir.Ref) bool {
		for _, ref := range values {
			if ref.Producer == producerID {
				return true
			}
		}
		return false
	}
	sources := func(values []ir.Source) bool {
		for _, source := range values {
			if source.BlockID == producerID {
				return true
			}
		}
		return false
	}

	switch nd := n.(type) {
	case *flow.ProjectedLoop:
		if sources(nd.Inputs) || sources(nd.OutputSources) {
			return true
		}
		for _, source := range nd.StateValues {
			if source.BlockID == producerID {
				return true
			}
		}
	case *ir.End:
		if nd.Implicit {
			return false
		}
		if refs(nd.Inputs) {
			return true
		}
		for _, output := range nd.Outputs {
			if output.Ref.Producer == producerID {
				return true
			}
		}
	case *ir.LLMCall:
		return sources(nd.Inputs)
	case *ir.ToolCall:
		for _, param := range nd.Params {
			if param.Ref != nil && param.Ref.BlockID == producerID {
				return true
			}
		}
	case *ir.KnowledgeCall:
		return sources(nd.Inputs)
	case *ir.TextProcess:
		return sources(nd.Inputs)
	case *ir.CodeRun:
		return sources(nd.Inputs)
	case *ir.ListOp:
		return sources(nd.Inputs)
	case *ir.Selector:
		for _, condition := range nd.Conditions {
			if (condition.Left.Ref != nil && condition.Left.Ref.Producer == producerID) ||
				(condition.Right.Ref != nil && condition.Right.Ref.Producer == producerID) {
				return true
			}
		}
	case *ir.Aggregate:
		return refs(nd.AllRefs())
	case *ir.Assign:
		return nd.Value.BlockID == producerID
	case *ir.QA:
		return sources(nd.Inputs)
	}
	return false
}

// startValueTypeFor maps IR port kinds onto the value_type a Dify start
// variable of inputTypeFor(t) yields. Dify start variables have no integer
// input type ("number" is the numeric port), so the port must mirror the
// variable's declared type instead of valueTypeFor's IR-faithful integer.
func startValueTypeFor(t *ir.Type) string {
	if t == nil {
		return "string"
	}
	switch t.Kind {
	case "string":
		return "string"
	case "integer", "number":
		return "number"
	case "boolean":
		return "string" // paragraph input -> string port
	case "file":
		return "file"
	case "list":
		if t.Elem != nil && t.Elem.Kind == "file" {
			return "array[file]"
		}
		return "array[string]"
	default:
		return "string"
	}
}

// inputTypeFor maps IR port kinds onto Dify start-variable input types.
func inputTypeFor(t *ir.Type) string {
	if t == nil {
		return "text-input"
	}
	switch t.Kind {
	case "string":
		return "text-input"
	case "integer", "number":
		return "number"
	case "boolean":
		return "paragraph"
	case "file":
		return "file"
	case "list":
		if t.Elem != nil && t.Elem.Kind == "file" {
			return "file-list"
		}
		return "array"
	default:
		return "text-input"
	}
}

// valueTypeFor maps IR port kinds onto Dify value_type strings.
func valueTypeFor(t *ir.Type) string {
	if t == nil {
		return "string"
	}
	switch t.Kind {
	case "string":
		return "string"
	case "integer":
		return "integer"
	case "number":
		return "number"
	case "boolean":
		return "boolean"
	case "file":
		return "file"
	case "list":
		if t.Elem != nil {
			return "array[" + valueTypeFor(t.Elem) + "]"
		}
		return "array[string]"
	default:
		return "object"
	}
}

// ── config helpers ─────────────────────────────────────────────────

func (r *irRenderer) configValue(cfg []ir.ConfigPair, key string) (any, bool) {
	for _, c := range cfg {
		if c.Key == key {
			return c.Value, true
		}
	}
	return nil, false
}

func (r *irRenderer) messages(cfg []ir.ConfigPair) []ir.Message {
	if v, ok := r.configValue(cfg, "messages"); ok {
		if msgs, ok2 := v.([]ir.Message); ok2 {
			return msgs
		}
	}
	return nil
}

// ── base-field access (Go simplification; NodeData hides the embedded
// DataNode, so title reads/writes go through reflection like lowering) ──

func dataField(data NodeData, name string) (reflect.Value, bool) {
	if data == nil {
		return reflect.Value{}, false
	}
	v := reflect.ValueOf(data)
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return reflect.Value{}, false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	f := v.FieldByName(name)
	if !f.IsValid() {
		return reflect.Value{}, false
	}
	return f, true
}

func getDataTitle(data NodeData) string {
	if f, ok := dataField(data, "Title"); ok && f.Kind() == reflect.String {
		return f.String()
	}
	return ""
}

func setDataTitle(data NodeData, title string) {
	f, ok := dataField(data, "Title")
	if !ok || f.Kind() != reflect.String || !f.CanSet() {
		return
	}
	f.SetString(title)
}
