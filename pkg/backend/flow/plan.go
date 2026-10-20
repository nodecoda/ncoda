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
	s := &semSolver{
		sem: &obligation.Semantics{
			Constructs:  make([]obligation.Construct, 0),
			Obligations: make([]obligation.Obligation, 0),
			Refs:        make([]obligation.RefFact, 0),
			Nodes:       make([]obligation.NodeFact, 0),
		},
		seenNodes: map[string]bool{},
		seenMerge: map[string]bool{},
		seenObl:   map[string]bool{},
		seenRef:   map[string]bool{},
	}
	s.block(g.Root, "")
	return s.sem, nil
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

// blockFlow is the target-independent control-flow summary of one block.
// It is derived from the structured IR only; no backend projection helper is
// consulted (P1b: one solver, no owePort/drainPorts/recordSkippedMerge).
type blockFlow struct {
	entry    string
	exits    []string
	terminal string // flow | return | break | continue | none
	empty    bool
}

// semSolver walks the IR in source order and records A-class facts. It is
// deterministic: all collections are slices, and map iteration never reaches
// the output.
type semSolver struct {
	sem       *obligation.Semantics
	seenNodes map[string]bool
	seenMerge map[string]bool
	seenObl   map[string]bool
	seenRef   map[string]bool
}

// block walks one structured block. tail is the id the block falls through to
// when its last statement has no sibling (docs §6 A2 drain points: the
// enclosing merge, the enclosing block continuation, or the document tail).
// "" means no known landing.
func (s *semSolver) block(b *ir.Block, tail string) blockFlow {
	if b == nil || len(b.Stmts) == 0 {
		return blockFlow{empty: true, terminal: "none"}
	}
	var entry string
	var last blockFlow
	for i, st := range b.Stmts {
		next := nextStmtID(b.Stmts[i+1:])
		if next == "" && i == len(b.Stmts)-1 {
			next = tail
		}
		f := s.stmt(st, next, tail)
		if entry == "" {
			entry = f.entry
		}
		last = f
		if f.terminal != "" && f.terminal != "flow" {
			for _, rest := range b.Stmts[i+1:] {
				s.unreachable(rest)
			}
			return blockFlow{entry: entry, exits: nil, terminal: f.terminal}
		}
	}
	if last.terminal == "" {
		last.terminal = "flow"
	}
	last.entry = entry
	return last
}

func (s *semSolver) stmt(st ir.Stmt, next, tail string) blockFlow {
	switch st := st.(type) {
	case *ir.NodeStmt:
		if st.Node == nil {
			return blockFlow{terminal: "flow"}
		}
		id := st.Node.NodeID()
		s.addNode(id, string(st.Node.Kind()), true)
		return blockFlow{entry: id, exits: []string{id}, terminal: "flow"}

	case *ir.IfStmt:
		return s.ifStmt(st, next, tail)

	case *ir.AttemptStmt:
		return s.attempt(st, next, tail)

	case *ir.QaStmt:
		return s.qa(st, next, tail)

	case *ir.SwitchStmt:
		return s.switchStmt(st, next, tail)

	case *ir.LoopStmt:
		return s.loop(st, next)

	case *ir.ParallelStmt:
		return s.parallel(st, next)

	case *ir.ReturnStmt:
		var id string
		if st.End != nil {
			id = st.End.ID
			s.addNode(id, "end", true)
		}
		return blockFlow{entry: id, terminal: "return"}

	case *ir.BreakStmt:
		if st.Node != nil {
			s.addNode(st.Node.ID, "break", true)
			return blockFlow{entry: st.Node.ID, terminal: "break"}
		}
		return blockFlow{terminal: "break"}

	case *ir.ContinueStmt:
		if st.Node != nil {
			s.addNode(st.Node.ID, "continue", true)
			return blockFlow{entry: st.Node.ID, terminal: "continue"}
		}
		return blockFlow{terminal: "continue"}

	case *ir.InlineStmt:
		return s.block(st.Body, next)
	}
	return blockFlow{terminal: "flow"}
}

func (s *semSolver) ifStmt(st *ir.IfStmt, next, tail string) blockFlow {
	armTail := next
	if armTail == "" {
		armTail = tail
	}
	if st.Merge != nil {
		armTail = st.Merge.ID
	}
	thenF := s.block(st.Then, armTail)
	elseF := s.block(st.Else, armTail)

	c := obligation.Construct{
		ID:    st.ID,
		Kind:  "if",
		Arms:  make([]obligation.ArmPlan, 0, 2+len(st.ChainArms)),
		Merge: s.mergePlan(st.Merge),
	}
	c.Arms = append(c.Arms,
		s.armPlanFromFlow("then", "true", thenF),
		s.armPlanFromFlow("else", "false", elseF),
	)
	for i, chain := range st.ChainArms {
		if chain == nil {
			continue
		}
		cf := s.block(chain.Block, armTail)
		c.Arms = append(c.Arms, s.armPlanFromFlow(
			fmt.Sprintf("elif_%d", i+1), fmt.Sprintf("true_%d", i+1), cf))
	}
	var cond ir.Node
	if st.Cond != nil {
		cond = st.Cond
	}
	s.addConstruct(c, cond)
	s.addMergeRefs(st.Merge)

	group := "Group1"
	if st.Merge != nil {
		if name, _ := firstClosure(st.Merge); name != "" {
			group = name
		}
	}
	if st.Merge != nil {
		s.addObligation(st.ID, "true", group, st.Merge.ID, "merge", true)
		if st.Else != nil || len(st.ChainArms) > 0 {
			s.addObligation(st.ID, "false", group, st.Merge.ID, "merge", true)
		}
		for i := range st.ChainArms {
			s.addObligation(st.ID, fmt.Sprintf("true_%d", i+1), group, st.Merge.ID, "merge", true)
		}
	} else {
		s.addObligation(st.ID, "true", group, next, "flow", next != "")
		s.addObligation(st.ID, "false", group, next, "flow", next != "")
		for i := range st.ChainArms {
			s.addObligation(st.ID, fmt.Sprintf("true_%d", i+1), group, next, "flow", next != "")
		}
	}

	switch {
	case st.Merge != nil && len(thenF.exits) > 0 && len(elseF.exits) > 0:
		return blockFlow{entry: st.ID, exits: []string{st.Merge.ID}, terminal: "flow"}
	case st.Merge != nil && len(thenF.exits) > 0:
		return blockFlow{entry: st.ID, exits: thenF.exits, terminal: "flow"}
	case st.Merge != nil && len(elseF.exits) > 0:
		return blockFlow{entry: st.ID, exits: elseF.exits, terminal: "flow"}
	case st.Merge == nil:
		exits := unionIDs(thenF.exits, elseF.exits)
		if st.Else == nil && next == "" && st.ID != "" {
			exits = appendUnique(exits, st.ID)
		}
		return blockFlow{entry: st.ID, exits: exits, terminal: "flow"}
	default:
		term := "none"
		if thenF.terminal != "flow" || elseF.terminal != "flow" {
			term = "return"
		}
		return blockFlow{entry: st.ID, terminal: term}
	}
}

func (s *semSolver) attempt(st *ir.AttemptStmt, next, tail string) blockFlow {
	target := next
	kind := "flow"
	if st.Exit != nil {
		target = st.Exit.ID
		kind = "merge"
	}
	armTail := target
	if armTail == "" {
		armTail = tail
	}
	succF := s.block(st.Success, armTail)
	failF := s.block(st.Failure, armTail)
	c := obligation.Construct{
		ID:    st.ID,
		Kind:  "attempt",
		Arms:  []obligation.ArmPlan{s.armPlanFromFlow("ok", "success", succF), s.armPlanFromFlow("err", "fail-branch", failF)},
		Merge: s.mergePlan(st.Exit),
	}
	var op ir.Node
	if st.Op != nil {
		op = st.Op
	}
	s.addConstruct(c, op)
	s.addMergeRefs(st.Exit)

	group := "Group1"
	if st.Exit != nil {
		if name, _ := firstClosure(st.Exit); name != "" {
			group = name
		}
	}
	closed := target != ""
	s.addObligation(st.ID, "success", group, target, kind, closed)
	s.addObligation(st.ID, "fail-branch", group, target, kind, closed)

	if st.Exit != nil && len(succF.exits) > 0 && len(failF.exits) > 0 {
		return blockFlow{entry: st.ID, exits: []string{st.Exit.ID}, terminal: "flow"}
	}
	return blockFlow{entry: st.ID, exits: unionIDs(succF.exits, failF.exits), terminal: "flow"}
}

func (s *semSolver) qa(st *ir.QaStmt, next, tail string) blockFlow {
	flows := make([]blockFlow, 0, len(st.Branches))
	c := obligation.Construct{
		ID:    st.ID,
		Kind:  "ask",
		Arms:  make([]obligation.ArmPlan, 0, len(st.Branches)),
		Merge: s.mergePlan(st.Merge),
	}
	target := next
	if st.Merge != nil {
		target = st.Merge.ID
	}
	armTail := target
	if armTail == "" {
		armTail = tail
	}
	for _, b := range st.Branches {
		f := s.block(b.Body, armTail)
		flows = append(flows, f)
		c.Arms = append(c.Arms, s.armPlanFromFlow(b.Port, b.Port, f))
	}
	var qa ir.Node
	if st.QA != nil {
		qa = st.QA
	}
	s.addConstruct(c, qa)
	s.addMergeRefs(st.Merge)

	if len(st.Branches) == 0 {
		return blockFlow{entry: st.ID, exits: []string{st.ID}, terminal: "flow"}
	}
	group := "Group1"
	if st.Merge != nil {
		if name, _ := firstClosure(st.Merge); name != "" {
			group = name
		}
	}
	kind := "flow"
	if st.Merge != nil {
		kind = "merge"
	}
	for _, b := range st.Branches {
		s.addObligation(st.ID, b.Port, group, target, kind, target != "")
	}
	exits := unionFlows(flows)
	if st.Merge != nil && len(exits) > 0 {
		exits = []string{st.Merge.ID}
	}
	return blockFlow{entry: st.ID, exits: exits, terminal: "flow"}
}

func (s *semSolver) switchStmt(st *ir.SwitchStmt, next, tail string) blockFlow {
	flows := make([]blockFlow, 0, len(st.Branches))
	c := obligation.Construct{
		ID:    st.ID,
		Kind:  "switch",
		Arms:  make([]obligation.ArmPlan, 0, len(st.Branches)),
		Merge: s.mergePlan(st.Merge),
	}
	target := next
	if st.Merge != nil {
		target = st.Merge.ID
	}
	armTail := target
	if armTail == "" {
		armTail = tail
	}
	for _, b := range st.Branches {
		f := s.block(b.Body, armTail)
		flows = append(flows, f)
		c.Arms = append(c.Arms, s.armPlanFromFlow(b.Port, b.Port, f))
	}
	var sw ir.Node
	if st.Switch != nil {
		sw = st.Switch
	}
	s.addConstruct(c, sw)
	s.addMergeRefs(st.Merge)

	group := "Group1"
	if st.Merge != nil {
		if name, _ := firstClosure(st.Merge); name != "" {
			group = name
		}
	}
	kind := "flow"
	if st.Merge != nil {
		kind = "merge"
	}
	for _, b := range st.Branches {
		s.addObligation(st.ID, b.Port, group, target, kind, target != "")
	}
	exits := unionFlows(flows)
	if st.Merge != nil && len(exits) > 0 {
		exits = []string{st.Merge.ID}
	}
	return blockFlow{entry: st.ID, exits: exits, terminal: "flow"}
}

func (s *semSolver) loop(st *ir.LoopStmt, next string) blockFlow {
	// A body statement falls through to the loop-back edge, so the container
	// is the drain point for a body ending without an explicit back/exit.
	bodyF := s.block(st.Body, st.ID)
	// A7: the body tail returns to the container through a loop-back edge
	// (cozebiz EdgeLoopBack: "body tail → container"); explicit continues are
	// additional back-edge sources. A body that only breaks/returns has none.
	back := collectContinues(st.Body)
	for _, exit := range bodyF.exits {
		back = appendUnique(back, exit)
	}
	c := obligation.Construct{
		ID:   st.ID,
		Kind: "loop",
		Arms: make([]obligation.ArmPlan, 0),
		Loop: &obligation.LoopPlan{
			Entry: bodyF.entry,
			Back:  back,
			Exit:  next,
			Body:  blockIDs(st.Body),
		},
	}
	var container ir.Node
	if st.Container != nil {
		container = st.Container
	}
	s.addConstruct(c, container)
	return blockFlow{entry: st.ID, exits: []string{st.ID}, terminal: "flow"}
}

func (s *semSolver) parallel(st *ir.ParallelStmt, next string) blockFlow {
	id := st.ID
	if id == "" {
		id = "parallel"
		if len(st.Branches) > 0 {
			if e := blockEntry(st.Branches[0]); e != "" {
				id = "parallel-" + e
			}
		}
	}
	flows := make([]blockFlow, 0, len(st.Branches))
	c := obligation.Construct{ID: id, Kind: "parallel", Arms: make([]obligation.ArmPlan, 0, len(st.Branches))}
	for i, b := range st.Branches {
		f := s.block(b, next)
		flows = append(flows, f)
		c.Arms = append(c.Arms, s.armPlanFromFlow(fmt.Sprintf("branch_%d", i), fmt.Sprintf("branch_%d", i), f))
	}
	s.addConstruct(c, nil)
	return blockFlow{entry: id, exits: unionFlows(flows), terminal: "flow"}
}

func (s *semSolver) armPlanFromFlow(name, port string, f blockFlow) obligation.ArmPlan {
	arm := obligation.ArmPlan{
		Name:     name,
		Port:     port,
		Exits:    append([]string(nil), f.exits...),
		Empty:    f.empty,
		Terminal: "none",
	}
	if !f.empty && f.entry != "" {
		entry := f.entry
		arm.Entry = &entry
	}
	if !f.empty {
		arm.Terminal = f.terminal
		if arm.Terminal == "" {
			arm.Terminal = "flow"
		}
	}
	return arm
}

func (s *semSolver) addConstruct(c obligation.Construct, node ir.Node) {
	s.sem.Constructs = append(s.sem.Constructs, c)
	if c.ID != "" {
		s.addNode(c.ID, c.Kind, true)
	}
	if node != nil {
		s.addNode(node.NodeID(), string(node.Kind()), true)
	}
	if c.Merge != nil {
		s.addMergeNode(c.Merge.ID)
	}
}

func (s *semSolver) addNode(id, kind string, reachable bool) {
	if id == "" {
		return
	}
	if _, ok := s.seenNodes[id]; ok {
		if reachable {
			for i := range s.sem.Nodes {
				if s.sem.Nodes[i].ID == id {
					s.sem.Nodes[i].Reachable = true
					break
				}
			}
		}
		return
	}
	s.seenNodes[id] = true
	s.sem.Nodes = append(s.sem.Nodes, obligation.NodeFact{ID: id, Kind: kind, Reachable: reachable})
}

func (s *semSolver) addMergeNode(id string) {
	if id == "" || s.seenMerge[id] {
		return
	}
	s.seenMerge[id] = true
	s.addNode(id, "merge", true)
}

func (s *semSolver) addObligation(selector, port, group, target, targetKind string, closed bool) {
	key := selector + "\x00" + port + "\x00" + group
	if s.seenObl[key] {
		return
	}
	s.seenObl[key] = true
	status := obligation.ObligationOpen
	if closed {
		status = obligation.ObligationClosed
	}
	s.sem.Obligations = append(s.sem.Obligations, obligation.Obligation{
		Selector:   selector,
		Port:       port,
		Group:      group,
		Target:     target,
		TargetKind: targetKind,
		Status:     status,
	})
}

func (s *semSolver) addMergeRefs(a *ir.Aggregate) {
	if a == nil {
		return
	}
	for _, nc := range closures(a) {
		for _, cand := range nc.closure.Candidates {
			key := a.ID + "\x00" + cand.Producer + "\x00" + cand.Output
			if s.seenRef[key] {
				continue
			}
			s.seenRef[key] = true
			s.sem.Refs = append(s.sem.Refs, obligation.RefFact{
				Producer: cand.Producer,
				Output:   cand.Output,
				Consumer: a.ID,
				Resolved: []obligation.ValueCandidate{cand},
			})
		}
	}
}

func (s *semSolver) unreachable(st ir.Stmt) {
	switch st := st.(type) {
	case *ir.NodeStmt:
		if st.Node != nil {
			s.addNode(st.Node.NodeID(), string(st.Node.Kind()), false)
		}
	case *ir.IfStmt:
		if st.ID != "" {
			s.addNode(st.ID, "if", false)
		}
		if st.Cond != nil {
			s.addNode(st.Cond.NodeID(), string(st.Cond.Kind()), false)
		}
		s.unreachableBlock(st.Then)
		s.unreachableBlock(st.Else)
		for _, c := range st.ChainArms {
			if c != nil {
				s.unreachableBlock(c.Block)
			}
		}
	case *ir.AttemptStmt:
		if st.ID != "" {
			s.addNode(st.ID, "attempt", false)
		}
		if st.Op != nil {
			s.addNode(st.Op.NodeID(), string(st.Op.Kind()), false)
		}
		s.unreachableBlock(st.Success)
		s.unreachableBlock(st.Failure)
	case *ir.QaStmt:
		if st.ID != "" {
			s.addNode(st.ID, "ask", false)
		}
		for _, b := range st.Branches {
			s.unreachableBlock(b.Body)
		}
	case *ir.SwitchStmt:
		if st.ID != "" {
			s.addNode(st.ID, "switch", false)
		}
		for _, b := range st.Branches {
			s.unreachableBlock(b.Body)
		}
	case *ir.LoopStmt:
		if st.ID != "" {
			s.addNode(st.ID, "loop", false)
		}
		s.unreachableBlock(st.Body)
	case *ir.ParallelStmt:
		for _, b := range st.Branches {
			s.unreachableBlock(b)
		}
	case *ir.InlineStmt:
		s.unreachableBlock(st.Body)
	case *ir.ReturnStmt:
		if st.End != nil {
			s.addNode(st.End.ID, "end", false)
		}
	}
}

func (s *semSolver) unreachableBlock(b *ir.Block) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		s.unreachable(st)
	}
}

func (s *semSolver) mergePlan(a *ir.Aggregate) *obligation.MergePlan {
	if a == nil {
		return nil
	}
	mp := &obligation.MergePlan{ID: a.ID, ValueClosure: map[string]obligation.ValueClosure{}}
	for _, nc := range closures(a) {
		mp.ValueClosure[nc.name] = nc.closure
	}
	return mp
}

type namedClosure struct {
	name    string
	closure obligation.ValueClosure
}

func closures(a *ir.Aggregate) []namedClosure {
	if a == nil {
		return nil
	}
	out := make([]namedClosure, 0, 1+len(a.Groups))
	if len(a.Groups) > 0 {
		for _, g := range a.Groups {
			out = append(out, namedClosure{name: g.Name, closure: closureOf(g.Refs)})
		}
		return out
	}
	name := a.RefName
	if name == "" {
		name = a.SingleGroupName()
	}
	refs := a.Refs
	if len(refs) == 0 && len(a.ValueSources) > 0 {
		refs = a.ValueSources
	}
	out = append(out, namedClosure{name: name, closure: closureOf(refs)})
	return out
}

func firstClosure(a *ir.Aggregate) (string, []obligation.ValueCandidate) {
	cs := closures(a)
	if len(cs) == 0 {
		return "", nil
	}
	return cs[0].name, cs[0].closure.Candidates
}

func closureOf(refs []ir.Ref) obligation.ValueClosure {
	cands := make([]obligation.ValueCandidate, 0, len(refs))
	for _, r := range refs {
		cands = append(cands, obligation.ValueCandidate{Producer: r.Producer, Output: r.Output})
	}
	kind := obligation.ClosureDead
	switch len(cands) {
	case 0:
		kind = obligation.ClosureDead
	case 1:
		kind = obligation.ClosureAlias
	default:
		kind = obligation.ClosurePhi
	}
	return obligation.ValueClosure{Kind: kind, Candidates: cands}
}

func unionIDs(groups ...[]string) []string {
	var out []string
	for _, g := range groups {
		for _, id := range g {
			out = appendUnique(out, id)
		}
	}
	return out
}

func appendUnique(out []string, id string) []string {
	if id == "" {
		return out
	}
	for _, x := range out {
		if x == id {
			return out
		}
	}
	return append(out, id)
}

func unionFlows(flows []blockFlow) []string {
	var out []string
	for _, f := range flows {
		out = unionIDs(out, f.exits)
	}
	return out
}

func nextStmtID(stmts []ir.Stmt) string {
	for _, st := range stmts {
		if id := stmtID(st); id != "" {
			return id
		}
	}
	return ""
}

func collectContinues(b *ir.Block) []string {
	var out []string
	var walk func(*ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, st := range b.Stmts {
			switch st := st.(type) {
			case *ir.ContinueStmt:
				if st.Node != nil {
					out = appendUnique(out, st.Node.ID)
				}
			case *ir.IfStmt:
				walk(st.Then)
				walk(st.Else)
				for _, c := range st.ChainArms {
					if c != nil {
						walk(c.Block)
					}
				}
			case *ir.AttemptStmt:
				walk(st.Success)
				walk(st.Failure)
			case *ir.QaStmt:
				for _, br := range st.Branches {
					walk(br.Body)
				}
			case *ir.SwitchStmt:
				for _, br := range st.Branches {
					walk(br.Body)
				}
			case *ir.LoopStmt:
				walk(st.Body)
			case *ir.ParallelStmt:
				for _, br := range st.Branches {
					walk(br)
				}
			case *ir.InlineStmt:
				walk(st.Body)
			}
		}
	}
	walk(b)
	return out
}

func blockEntry(b *ir.Block) string {
	if b == nil {
		return ""
	}
	return nextStmtID(b.Stmts)
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
	case *ir.ReturnStmt:
		// A2: a terminal return is a drain point (document tail); the
		// obligation of a preceding branch lands on the shared End.
		if st.End != nil {
			return st.End.ID
		}
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
