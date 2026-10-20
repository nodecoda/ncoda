package flow

// plan.go — P1 graph obligation plan bundle.
//
// Plan is the single entry point for the A-class graph semantics
// (docs/GRAPH-OBLIGATION-KERNEL.md v2 §5, PRD §R1.1). The public half is
// target-independent (obligation.Semantics). The private half
// (materialization) is a behavior-neutral snapshot of the existing Project
// output and is only readable through ProjectFromPlan; backends must not see
// it. Import direction stays obligation ← flow.

import (
	"fmt"

	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/obligation"
)

// PlanBundle is the P1 plan: public Semantics plus flow-private
// materialization. The mat field is deliberately unexported so targets can
// never read the projection through the plan.
type PlanBundle struct {
	Semantics *obligation.Semantics
	mat       *materialization
}

// materialization is the flow-private behavior-neutral snapshot. It is not
// part of the exported surface and never enters the Plan schema.
type materialization struct {
	nodes []ir.Node
	edges []Edge
	order []string
}

// Plan derives the semantics and freezes the current projection into the
// private materialization. It is behavior-neutral: it calls the existing
// Project and does not modify its derivation logic.
func Plan(g *ir.Graph) (*PlanBundle, error) {
	if g == nil {
		return nil, fmt.Errorf("flow: plan: nil graph")
	}
	proj, err := Project(g)
	if err != nil {
		return nil, err
	}
	sem, err := Semantics(g)
	if err != nil {
		return nil, err
	}
	mat := &materialization{
		nodes: append([]ir.Node(nil), proj.Nodes...),
		edges: append([]Edge(nil), proj.Edges...),
	}
	mat.order = make([]string, 0, len(proj.Nodes))
	for _, n := range proj.Nodes {
		if n == nil {
			continue
		}
		mat.order = append(mat.order, n.NodeID())
	}
	return &PlanBundle{Semantics: sem, mat: mat}, nil
}

// Semantics returns only the public, target-independent half of the plan.
// This is a P1 minimal structural extraction: constructs, arms, loops and
// merge value closures are recorded in source order; the full A1–A11 solver
// (obligation closure, phi expansion, reachability) belongs to G004.
func Semantics(g *ir.Graph) (*obligation.Semantics, error) {
	if g == nil {
		return nil, fmt.Errorf("flow: semantics: nil graph")
	}
	if g.Root == nil {
		return nil, fmt.Errorf("flow: semantics: graph has no structured root")
	}
	e := &semanticsExtractor{
		sem: &obligation.Semantics{
			Constructs:  make([]obligation.Construct, 0),
			Obligations: make([]obligation.Obligation, 0),
			Refs:        make([]obligation.RefFact, 0),
			Nodes:       make([]obligation.NodeFact, 0),
		},
		seenNodes: map[string]bool{},
		seenMerge: map[string]bool{},
	}
	e.walkBlock(g.Root)
	return e.sem, nil
}

// ProjectFromPlan rebuilds the exact projection frozen by Plan: the same
// nodes, the same edges, in the same order.
func ProjectFromPlan(b *PlanBundle) (*Projection, error) {
	if b == nil || b.mat == nil {
		return nil, fmt.Errorf("flow: project from plan: bundle has no materialization")
	}
	return &Projection{
		Nodes: append([]ir.Node(nil), b.mat.nodes...),
		Edges: append([]Edge(nil), b.mat.edges...),
	}, nil
}

// GraphView adapts a Projection to the narrow obligation.GraphView surface.
// It exposes ids and structural edges only; it never exposes materialization
// or product ids.
func (p *Projection) GraphView() obligation.GraphView {
	return projectionView{p: p}
}

type projectionView struct{ p *Projection }

func (v projectionView) NodeIDs() []string {
	if v.p == nil {
		return nil
	}
	ids := make([]string, 0, len(v.p.Nodes))
	for _, n := range v.p.Nodes {
		if n == nil {
			continue
		}
		ids = append(ids, n.NodeID())
	}
	return ids
}

func (v projectionView) Edges() []obligation.EdgeView {
	if v.p == nil {
		return nil
	}
	out := make([]obligation.EdgeView, 0, len(v.p.Edges))
	for _, e := range v.p.Edges {
		out = append(out, obligation.EdgeView{
			Source:     e.SourceNodeID,
			Target:     e.TargetNodeID,
			SourcePort: e.SourcePortID,
			TargetPort: e.TargetPortID,
		})
	}
	return out
}

func (v projectionView) Refs() []obligation.RefView { return nil }

// semanticsExtractor walks the structured tree in source order. It is
// read-only and deterministic: no map iteration reaches the output.
type semanticsExtractor struct {
	sem       *obligation.Semantics
	seenNodes map[string]bool
	seenMerge map[string]bool
}

func (e *semanticsExtractor) walkBlock(b *ir.Block) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		e.walkStmt(s)
	}
}

func (e *semanticsExtractor) walkStmt(s ir.Stmt) {
	switch st := s.(type) {
	case *ir.NodeStmt:
		if st.Node != nil {
			e.addNode(st.Node.NodeID(), string(st.Node.Kind()))
		}
	case *ir.IfStmt:
		e.addIf(st)
	case *ir.LoopStmt:
		e.addLoop(st)
	case *ir.AttemptStmt:
		e.addAttempt(st)
	case *ir.QaStmt:
		e.addQa(st)
	case *ir.SwitchStmt:
		e.addSwitch(st)
	case *ir.ParallelStmt:
		e.addParallel(st)
	case *ir.InlineStmt:
		e.walkBlock(st.Body)
	case *ir.ReturnStmt:
		if st.End != nil {
			e.addNode(st.End.ID, string(st.End.Kind()))
		}
	case *ir.BreakStmt:
		if st.Node != nil {
			e.addNode(st.Node.ID, string(st.Node.Kind()))
		}
	case *ir.ContinueStmt:
		if st.Node != nil {
			e.addNode(st.Node.ID, string(st.Node.Kind()))
		}
	}
}

func (e *semanticsExtractor) addIf(st *ir.IfStmt) {
	c := obligation.Construct{
		ID:    st.ID,
		Kind:  "if",
		Arms:  make([]obligation.ArmPlan, 0, 2+len(st.ChainArms)),
		Merge: e.mergePlan(st.Merge),
	}
	c.Arms = append(c.Arms,
		e.armPlan("then", "true", st.Then),
		e.armPlan("else", "false", st.Else),
	)
	for i, chain := range st.ChainArms {
		if chain == nil {
			continue
		}
		c.Arms = append(c.Arms, e.armPlan(fmt.Sprintf("elif_%d", i+1), fmt.Sprintf("true_%d", i+1), chain.Block))
	}
	var cond ir.Node
	if st.Cond != nil {
		cond = st.Cond
	}
	e.addConstruct(c, cond)
	e.addEmptyArmObligations(st)
	e.walkBlock(st.Then)
	e.walkBlock(st.Else)
	for _, chain := range st.ChainArms {
		if chain != nil {
			e.walkBlock(chain.Block)
		}
	}
}

func (e *semanticsExtractor) addEmptyArmObligations(st *ir.IfStmt) {
	if st.Merge == nil {
		return
	}
	group, candidates := firstClosure(st.Merge)
	for _, arm := range []struct {
		name string
		port string
		body *ir.Block
	}{
		{name: "then", port: "true", body: st.Then},
		{name: "else", port: "false", body: st.Else},
	} {
		if arm.body != nil && len(arm.body.Stmts) > 0 {
			continue
		}
		e.sem.Obligations = append(e.sem.Obligations, obligation.Obligation{
			Selector:   st.ID,
			Port:       arm.port,
			Group:      group,
			Target:     st.Merge.ID,
			TargetKind: "merge",
			Candidates: candidates,
			Status:     obligation.ObligationClosed,
		})
	}
}

func (e *semanticsExtractor) addLoop(st *ir.LoopStmt) {
	c := obligation.Construct{
		ID:   st.ID,
		Kind: "loop",
		Arms: make([]obligation.ArmPlan, 0),
		Loop: &obligation.LoopPlan{
			Entry: blockEntry(st.Body),
			Back:  make([]string, 0), // G004 solver computes real back edges.
			Exit:  "",
			Body:  blockIDs(st.Body),
		},
	}
	var container ir.Node
	if st.Container != nil {
		container = st.Container
	}
	e.addConstruct(c, container)
	e.walkBlock(st.Body)
}

func (e *semanticsExtractor) addAttempt(st *ir.AttemptStmt) {
	c := obligation.Construct{
		ID:    st.ID,
		Kind:  "attempt",
		Arms:  []obligation.ArmPlan{e.armPlan("ok", "success", st.Success), e.armPlan("err", "error", st.Failure)},
		Merge: e.mergePlan(st.Exit),
	}
	var op ir.Node
	if st.Op != nil {
		op = st.Op
	}
	e.addConstruct(c, op)
	e.walkBlock(st.Success)
	e.walkBlock(st.Failure)
}

func (e *semanticsExtractor) addQa(st *ir.QaStmt) {
	c := obligation.Construct{
		ID:    st.ID,
		Kind:  "ask",
		Arms:  make([]obligation.ArmPlan, 0, len(st.Branches)),
		Merge: e.mergePlan(st.Merge),
	}
	for _, b := range st.Branches {
		c.Arms = append(c.Arms, e.armPlan(b.Port, b.Port, b.Body))
	}
	var qa ir.Node
	if st.QA != nil {
		qa = st.QA
	}
	e.addConstruct(c, qa)
	for _, b := range st.Branches {
		e.walkBlock(b.Body)
	}
}

func (e *semanticsExtractor) addSwitch(st *ir.SwitchStmt) {
	c := obligation.Construct{
		ID:    st.ID,
		Kind:  "switch",
		Arms:  make([]obligation.ArmPlan, 0, len(st.Branches)),
		Merge: e.mergePlan(st.Merge),
	}
	for _, b := range st.Branches {
		c.Arms = append(c.Arms, e.armPlan(b.Port, b.Port, b.Body))
	}
	var sw ir.Node
	if st.Switch != nil {
		sw = st.Switch
	}
	e.addConstruct(c, sw)
	for _, b := range st.Branches {
		e.walkBlock(b.Body)
	}
}

func (e *semanticsExtractor) addParallel(st *ir.ParallelStmt) {
	c := obligation.Construct{
		ID:   st.ID,
		Kind: "parallel",
		Arms: make([]obligation.ArmPlan, 0, len(st.Branches)),
	}
	for i, b := range st.Branches {
		c.Arms = append(c.Arms, e.armPlan(fmt.Sprintf("branch_%d", i), fmt.Sprintf("branch_%d", i), b))
	}
	e.addConstruct(c, nil)
	for _, b := range st.Branches {
		e.walkBlock(b)
	}
}

func (e *semanticsExtractor) addConstruct(c obligation.Construct, node ir.Node) {
	e.sem.Constructs = append(e.sem.Constructs, c)
	e.addNode(c.ID, c.Kind)
	if node != nil {
		e.addNode(node.NodeID(), string(node.Kind()))
	}
	if c.Merge != nil {
		e.addMergeNode(c.Merge.ID)
	}
}

func (e *semanticsExtractor) addNode(id, kind string) {
	if id == "" || e.seenNodes[id] {
		return
	}
	e.seenNodes[id] = true
	e.sem.Nodes = append(e.sem.Nodes, obligation.NodeFact{ID: id, Kind: kind, Reachable: true})
}

func (e *semanticsExtractor) addMergeNode(id string) {
	if id == "" || e.seenMerge[id] {
		return
	}
	e.seenMerge[id] = true
	e.addNode(id, "merge")
}

func (e *semanticsExtractor) mergePlan(a *ir.Aggregate) *obligation.MergePlan {
	if a == nil {
		return nil
	}
	mp := &obligation.MergePlan{ID: a.ID, ValueClosure: map[string]obligation.ValueClosure{}}
	if len(a.Groups) > 0 {
		for _, g := range a.Groups {
			mp.ValueClosure[g.Name] = closureOf(g.Refs)
		}
		return mp
	}
	name := a.RefName
	if name == "" {
		name = a.SingleGroupName()
	}
	mp.ValueClosure[name] = closureOf(a.Refs)
	return mp
}

func (e *semanticsExtractor) armPlan(name, port string, b *ir.Block) obligation.ArmPlan {
	arm := obligation.ArmPlan{
		Name:     name,
		Port:     port,
		Exits:    make([]string, 0),
		Empty:    b == nil || len(b.Stmts) == 0,
		Terminal: "none",
	}
	if arm.Empty {
		return arm
	}
	if entry := blockEntry(b); entry != "" {
		arm.Entry = &entry
	}
	if exits := blockExits(b); len(exits) > 0 {
		arm.Exits = exits
	}
	arm.Terminal = "flow"
	if last := b.Stmts[len(b.Stmts)-1]; last != nil {
		if _, ok := last.(*ir.ReturnStmt); ok {
			arm.Terminal = "return"
		}
	}
	return arm
}

func closureOf(refs []ir.Ref) obligation.ValueClosure {
	cands := make([]obligation.ValueCandidate, 0, len(refs))
	for _, r := range refs {
		cands = append(cands, obligation.ValueCandidate{Producer: r.Producer, Output: r.Output})
	}
	kind := obligation.ClosureDead
	switch len(cands) {
	case 1:
		kind = obligation.ClosureAlias
	default:
		if len(cands) > 1 {
			kind = obligation.ClosurePhi
		}
	}
	return obligation.ValueClosure{Kind: kind, Candidates: cands}
}

func firstClosure(a *ir.Aggregate) (string, []obligation.ValueCandidate) {
	if a == nil {
		return "", make([]obligation.ValueCandidate, 0)
	}
	if len(a.Groups) > 0 {
		g := a.Groups[0]
		return g.Name, closureOf(g.Refs).Candidates
	}
	name := a.RefName
	if name == "" {
		name = a.SingleGroupName()
	}
	return name, closureOf(a.Refs).Candidates
}

// blockEntry returns the id of the first statement that owns a node.
func blockEntry(b *ir.Block) string {
	if b == nil {
		return ""
	}
	for _, s := range b.Stmts {
		if id := stmtID(s); id != "" {
			return id
		}
	}
	return ""
}

// blockExits returns the syntactic tail id of a block. The real A-class
// solver owns terminal-aware exits; this placeholder keeps the schema
// serializable for P1.
func blockExits(b *ir.Block) []string {
	if b == nil || len(b.Stmts) == 0 {
		return nil
	}
	if id := stmtID(b.Stmts[len(b.Stmts)-1]); id != "" {
		return []string{id}
	}
	return nil
}

func blockIDs(b *ir.Block) []string {
	if b == nil {
		return make([]string, 0)
	}
	ids := make([]string, 0, len(b.Stmts))
	for _, s := range b.Stmts {
		if id := stmtID(s); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// stmtID mirrors ir.Stmt.stmtID for external packages: the concrete structs
// expose the same ids as fields.
func stmtID(s ir.Stmt) string {
	switch st := s.(type) {
	case *ir.NodeStmt:
		if st.Node != nil {
			return st.Node.NodeID()
		}
	case *ir.IfStmt:
		return st.ID
	case *ir.LoopStmt:
		return st.ID
	case *ir.AttemptStmt:
		return st.ID
	case *ir.QaStmt:
		return st.ID
	case *ir.SwitchStmt:
		return st.ID
	case *ir.ParallelStmt:
		return st.ID
	case *ir.BreakStmt:
		if st.Node != nil {
			return st.Node.ID
		}
	case *ir.ContinueStmt:
		if st.Node != nil {
			return st.Node.ID
		}
	}
	return ""
}
