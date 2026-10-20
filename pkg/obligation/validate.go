package obligation

import (
	"fmt"
	"sort"
)

// Validate is the P1 skeleton of the single obligation validator described by
// docs/GRAPH-OBLIGATION-KERNEL.md v2 §5.4. It is warning-only during P1:
// callers decide whether the returned violations fail a build.
//
// The validator is intentionally split between plan facts (Semantics),
// target observations (Report), and the narrow GraphView. It never sees a
// backend projection type, which is what keeps pkg/obligation a leaf package.
//
// A nil report skips target-observation checks (useful for plan-only probes);
// a nil view skips projection checks.
func Validate(view GraphView, plan *Plan, report *Report) []Violation {
	var out []Violation
	if plan == nil || plan.Semantics == nil {
		out = append(out, Violation{
			Code:    CodePlanDiverged,
			Message: "plan or plan.semantics is nil",
		})
		return orderViolations(out)
	}
	if plan.Schema != PlanSchemaV2 {
		out = append(out, Violation{
			Code:     CodePlanDiverged,
			Message:  fmt.Sprintf("unexpected plan schema %q, want %q", plan.Schema, PlanSchemaV2),
			Expected: PlanSchemaV2,
			Actual:   plan.Schema,
		})
	}

	sem := plan.Semantics
	// graphNodes are ids the projection keeps but the Semantics domain does
	// not own (the entry Start node and IR plumbing). A value closure or ref
	// may legally read the entry node (a function parameter), so producer
	// resolution must accept them; validateView still keeps the stricter
	// "every product node is in Semantics" contract.
	graphNodes := map[string]bool{}
	if view != nil {
		for _, id := range view.NodeIDs() {
			graphNodes[id] = true
		}
	}
	constructIDs := map[string]int{}
	mergeIDs := map[string]bool{}
	nodeIDs := map[string]bool{}
	known := map[string]bool{}

	// ── constructs / arms / merges / loops ───────────────────────────────
	for _, c := range sem.Constructs {
		if c.ID == "" {
			out = append(out, Violation{Code: CodePlanDiverged, Message: "construct with empty id"})
			continue
		}
		constructIDs[c.ID]++
		if constructIDs[c.ID] > 1 {
			out = append(out, Violation{
				Code:        CodeIDCollision,
				ConstructID: c.ID,
				Message:     fmt.Sprintf("construct id %q declared more than once", c.ID),
			})
		}
		known[c.ID] = true

		seenArmPorts := map[string]bool{}
		for _, a := range c.Arms {
			if a.Port == "" {
				out = append(out, Violation{
					Code:        CodeArmPortMissing,
					ConstructID: c.ID,
					Arm:         a.Name,
					Message:     "arm declares no port",
				})
				continue
			}
			if seenArmPorts[a.Port] {
				out = append(out, Violation{
					Code:        CodeArmPortDuplicate,
					ConstructID: c.ID,
					Arm:         a.Name,
					Port:        a.Port,
					Message:     fmt.Sprintf("arm port %q declared more than once", a.Port),
				})
			}
			seenArmPorts[a.Port] = true
			if !a.Empty && a.Entry == nil {
				out = append(out, Violation{
					Code:        CodeArmPortMissing,
					ConstructID: c.ID,
					Arm:         a.Name,
					Port:        a.Port,
					Message:     "non-empty arm has no entry node",
				})
			}
		}

		if c.Loop != nil {
			if c.Loop.Entry == "" {
				out = append(out, Violation{
					Code:        CodeLoopEntryAmbiguous,
					ConstructID: c.ID,
					Message:     "loop has no unique entry",
				})
			}
			if len(c.Loop.Back) == 0 {
				out = append(out, Violation{
					Code:        CodeLoopBackMissing,
					ConstructID: c.ID,
					Message:     "loop has no back edge",
				})
			}
			for _, id := range c.Loop.Body {
				known[id] = true
			}
		}

		if c.Merge != nil {
			if c.Merge.ID == "" {
				out = append(out, Violation{
					Code:        CodePlanDiverged,
					ConstructID: c.ID,
					Message:     "merge with empty id",
				})
			} else {
				if mergeIDs[c.Merge.ID] {
					out = append(out, Violation{
						Code:        CodeIDCollision,
						ConstructID: c.ID,
						Message:     fmt.Sprintf("merge id %q declared more than once", c.Merge.ID),
					})
				}
				mergeIDs[c.Merge.ID] = true
				known[c.Merge.ID] = true
			}
			groups := sortedKeys(c.Merge.ValueClosure)
			for _, g := range groups {
				vc := c.Merge.ValueClosure[g]
				derived := closureKind(len(vc.Candidates))
				if vc.Kind != derived {
					out = append(out, Violation{
						Code:        CodeMergeAliasUnresolved,
						ConstructID: c.ID,
						Message: fmt.Sprintf("group %q kind %q does not match %d candidate(s) (want %q)",
							g, vc.Kind, len(vc.Candidates), derived),
						Expected: derived,
						Actual:   vc.Kind,
					})
				}
				for i, cand := range vc.Candidates {
					if cand.Producer == "" || cand.Output == "" {
						out = append(out, Violation{
							Code:        CodeMergeAliasUnresolved,
							ConstructID: c.ID,
							Message:     fmt.Sprintf("group %q candidate[%d] has no producer/output", g, i),
						})
					}
				}
			}
		}
	}

	for _, n := range sem.Nodes {
		if n.ID == "" {
			out = append(out, Violation{Code: CodePlanDiverged, Message: "node with empty id"})
			continue
		}
		if nodeIDs[n.ID] {
			out = append(out, Violation{
				Code:    CodeIDCollision,
				Message: fmt.Sprintf("node id %q declared more than once", n.ID),
			})
		}
		nodeIDs[n.ID] = true
		known[n.ID] = true
	}

	// ── obligations ──────────────────────────────────────────────────────
	for _, ob := range sem.Obligations {
		if ob.Status == "" || ob.Status != ObligationClosed {
			out = append(out, Violation{
				Code:        CodeArmPortDangling,
				ConstructID: ob.Selector,
				Port:        ob.Port,
				Message:     fmt.Sprintf("obligation (selector %q, port %q, group %q) is %q", ob.Selector, ob.Port, ob.Group, statusOrOpen(ob.Status)),
				Expected:    ObligationClosed,
				Actual:      statusOrOpen(ob.Status),
			})
		}
		if ob.Selector == "" || ob.Port == "" {
			out = append(out, Violation{
				Code:        CodeArmPortMissing,
				ConstructID: ob.Selector,
				Port:        ob.Port,
				Message:     "obligation has no selector/port",
			})
		}
	}

	// ── value closure resolution + cycles ────────────────────────────────
	for _, c := range sem.Constructs {
		if c.Merge == nil {
			continue
		}
		for _, g := range sortedKeys(c.Merge.ValueClosure) {
			for _, cand := range c.Merge.ValueClosure[g].Candidates {
				if cand.Producer == "" {
					continue
				}
				if !known[cand.Producer] && !graphNodes[cand.Producer] {
					out = append(out, Violation{
						Code:        CodeMergeAliasUnresolved,
						ConstructID: c.ID,
						Message:     fmt.Sprintf("group %q candidate producer %q is not a known node/merge", g, cand.Producer),
					})
				}
			}
		}
	}
	for _, id := range mergeAliasCycles(sem, mergeIDs) {
		out = append(out, Violation{
			Code:        CodeMergeAliasCycle,
			ConstructID: id,
			Message:     fmt.Sprintf("merge alias closure cycles through %q", id),
		})
	}

	// ── GraphView checks ─────────────────────────────────────────────────
	if view != nil {
		out = append(out, validateView(view, known)...)
	}

	// ── report checks ────────────────────────────────────────────────────
	if report != nil {
		out = append(out, validateReport(report, sem)...)
	}

	return orderViolations(out)
}

func validateView(view GraphView, known map[string]bool) []Violation {
	var out []Violation
	ids := view.NodeIDs()
	seen := map[string]bool{}
	adj := map[string][]string{}
	for _, id := range ids {
		if id == "" {
			out = append(out, Violation{Code: CodeIDCollision, Message: "graph view contains an empty node id"})
			continue
		}
		if seen[id] {
			out = append(out, Violation{Code: CodeIDCollision, Message: fmt.Sprintf("graph view node id %q repeated", id)})
			continue
		}
		seen[id] = true
	}

	edgeKeys := map[string]bool{}
	for _, e := range view.Edges() {
		if e.Source == "" || e.Target == "" {
			out = append(out, Violation{
				Code:    CodeArmPortDangling,
				Message: fmt.Sprintf("edge %q -> %q has an empty endpoint", e.Source, e.Target),
			})
			continue
		}
		adj[e.Source] = append(adj[e.Source], e.Target)
		key := e.Source + "\x00" + e.SourcePort + "\x00" + e.Target + "\x00" + e.TargetPort
		if edgeKeys[key] {
			out = append(out, Violation{
				Code:    CodeArmPortDuplicate,
				Message: fmt.Sprintf("duplicate edge %q --%s--> %q", e.Source, e.SourcePort, e.Target),
			})
		}
		edgeKeys[key] = true
	}

	if len(ids) > 0 {
		reachable := map[string]bool{}
		queue := []string{ids[0]}
		reachable[ids[0]] = true
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, next := range adj[cur] {
				if !reachable[next] {
					reachable[next] = true
					queue = append(queue, next)
				}
			}
		}
		for _, id := range ids {
			if s := seen[id]; s && !reachable[id] {
				out = append(out, Violation{
					Code:    CodeUnreachableNode,
					Message: fmt.Sprintf("node %q is unreachable from the view entry %q", id, ids[0]),
				})
			}
		}
	}

	for _, r := range view.Refs() {
		if r.Producer == "" || (!known[r.Producer] && !seen[r.Producer]) {
			out = append(out, Violation{
				Code:    CodeRefUnresolved,
				Message: fmt.Sprintf("reference producer %q is unresolved", r.Producer),
			})
		}
	}
	return out
}

func validateReport(report *Report, sem *Semantics) []Violation {
	var out []Violation
	if report.Schema != ReportSchemaV2 {
		out = append(out, Violation{
			Code:     CodeIDMapInvalid,
			Message:  fmt.Sprintf("unexpected report schema %q, want %q", report.Schema, ReportSchemaV2),
			Expected: ReportSchemaV2,
			Actual:   report.Schema,
		})
	}

	productIDs := map[string]string{}
	for _, id := range sortedKeys(report.IDMap) {
		entry := report.IDMap[id]
		switch entry.State {
		case IDMapped:
			if entry.ProductID == nil || *entry.ProductID == "" {
				out = append(out, Violation{
					Code:        CodeIDMapInvalid,
					ConstructID: id,
					Message:     fmt.Sprintf("id_map[%q] is mapped but has no product_id", id),
				})
				continue
			}
			if prev, ok := productIDs[*entry.ProductID]; ok {
				out = append(out, Violation{
					Code:        CodeIDMapInvalid,
					ConstructID: id,
					Message:     fmt.Sprintf("product_id %q maps to both %q and %q", *entry.ProductID, prev, id),
				})
				continue
			}
			productIDs[*entry.ProductID] = id
		case IDElided, IDLowered:
			if entry.Reason == "" {
				out = append(out, Violation{
					Code:        CodeIDMapInvalid,
					ConstructID: id,
					Message:     fmt.Sprintf("id_map[%q] state %q requires a reason", id, entry.State),
				})
			}
			if entry.State == IDLowered && entry.RewriteRef == "" {
				out = append(out, Violation{
					Code:        CodeIDMapInvalid,
					ConstructID: id,
					Message:     fmt.Sprintf("id_map[%q] is lowered but has no rewrite_ref", id),
				})
			}
		default:
			out = append(out, Violation{
				Code:        CodeIDMapInvalid,
				ConstructID: id,
				Message:     fmt.Sprintf("id_map[%q] has invalid state %q", id, entry.State),
			})
		}
	}

	// Coverage: id_map domain = constructs ∪ nodes.
	for _, c := range sem.Constructs {
		if _, ok := report.IDMap[c.ID]; !ok {
			out = append(out, Violation{
				Code:        CodeIDMapInvalid,
				ConstructID: c.ID,
				Message:     fmt.Sprintf("id_map does not cover construct %q", c.ID),
			})
		}
	}
	for _, n := range sem.Nodes {
		if _, ok := report.IDMap[n.ID]; !ok {
			out = append(out, Violation{
				Code:        CodeIDMapInvalid,
				ConstructID: n.ID,
				Message:     fmt.Sprintf("id_map does not cover node %q", n.ID),
			})
		}
	}

	out = append(out, validateIDMapUniverse(report, sem, productIDs)...)

	seenEdges := map[string]bool{}
	for _, e := range report.Edges {
		if e.Selector == "" || e.Port == "" {
			out = append(out, Violation{
				Code:        CodeArmPortMissing,
				ConstructID: e.Selector,
				Port:        e.Port,
				Message:     "reported edge has no selector/port",
			})
		}
		if e.EdgeCount <= 0 {
			out = append(out, Violation{
				Code:        CodeArmPortDangling,
				ConstructID: e.Selector,
				Port:        e.Port,
				Message:     fmt.Sprintf("reported edge has edge_count %d", e.EdgeCount),
			})
		}
		if e.TargetKind == "merge" && e.CanonicalLanding == "" {
			out = append(out, Violation{
				Code:        CodeCanonicalLandingAmbiguous,
				ConstructID: e.Selector,
				Port:        e.Port,
				Message:     fmt.Sprintf("merge target %q has no canonical landing", e.Target),
			})
		}
		key := e.Selector + "\x00" + e.Port + "\x00" + e.Group + "\x00" + e.Target
		if e.EdgeCount > 1 || seenEdges[key] {
			if !seenEdges[key] || e.EdgeCount > 1 {
				out = append(out, Violation{
					Code:        CodeArmPortDuplicate,
					ConstructID: e.Selector,
					Port:        e.Port,
					Message:     fmt.Sprintf("duplicate reported edge key (%q,%q,%q)->%q", e.Selector, e.Port, e.Group, e.Target),
				})
			}
		}
		seenEdges[key] = true
	}

	for _, le := range report.LoopEdges {
		if le.Port == "loop-back" && le.CanonicalLanding == "" {
			out = append(out, Violation{
				Code:        CodeLoopBackMissing,
				ConstructID: le.Construct,
				Port:        le.Port,
				Message:     "loop-back observation has no canonical landing",
			})
		}
		if le.Port == "loop-entry" && le.CanonicalLanding == "" {
			out = append(out, Violation{
				Code:        CodeLoopEntryAmbiguous,
				ConstructID: le.Construct,
				Port:        le.Port,
				Message:     "loop-entry observation has no canonical landing",
			})
		}
	}
	return out
}

// validateIDMapUniverse enforces docs v2 §8.3: the id_map domain covers the
// Semantics universe (constructs, nodes, ref endpoints, candidate producers),
// extras are limited to declared lowered nodes, emitted_nodes are synthetic
// and disjoint from mapped product ids, and every observed endpoint resolves.
func validateIDMapUniverse(report *Report, sem *Semantics, productIDs map[string]string) []Violation {
	var out []Violation
	universe := map[string]bool{}
	add := func(id string) {
		if id != "" {
			universe[id] = true
		}
	}
	for _, c := range sem.Constructs {
		add(c.ID)
		if c.Merge != nil {
			add(c.Merge.ID)
		}
		for _, a := range c.Arms {
			if a.Entry != nil {
				add(*a.Entry)
			}
			for _, id := range a.Exits {
				add(id)
			}
		}
		if c.Loop != nil {
			add(c.Loop.Entry)
			add(c.Loop.Exit)
			for _, id := range c.Loop.Back {
				add(id)
			}
			for _, id := range c.Loop.Body {
				add(id)
			}
		}
	}
	for _, n := range sem.Nodes {
		add(n.ID)
	}
	for _, r := range sem.Refs {
		add(r.Producer)
		add(r.Consumer)
		for _, c := range r.Resolved {
			add(c.Producer)
		}
	}
	for _, ob := range sem.Obligations {
		add(ob.Selector)
		add(ob.Target)
		for _, c := range ob.Candidates {
			add(c.Producer)
		}
	}

	mappedCount := 0
	for _, id := range sortedKeys(report.IDMap) {
		entry := report.IDMap[id]
		if entry.State == IDMapped {
			mappedCount++
		}
		if universe[id] {
			continue
		}
		// An id outside Semantics is legal only as a declared lowering
		// (Relay and similar non-semantic IR nodes).
		if entry.State != IDLowered || entry.Reason == "" || entry.RewriteRef == "" {
			out = append(out, Violation{
				Code:        CodeIDMapInvalid,
				ConstructID: id,
				Message:     fmt.Sprintf("id_map[%q] is outside the Semantics universe but is not a declared lowering", id),
			})
		}
	}
	for _, id := range sortedKeys(universe) {
		if _, ok := report.IDMap[id]; !ok {
			out = append(out, Violation{
				Code:        CodeIDMapInvalid,
				ConstructID: id,
				Message:     fmt.Sprintf("id_map does not cover Semantics id %q", id),
			})
		}
	}

	emitted := map[string]bool{}
	for _, n := range report.EmittedNodes {
		if n.ProductID == "" {
			out = append(out, Violation{Code: CodeIDMapInvalid, Message: "emitted node has no product_id"})
			continue
		}
		if emitted[n.ProductID] {
			out = append(out, Violation{Code: CodeIDMapInvalid, ConstructID: n.ProductID,
				Message: fmt.Sprintf("emitted product_id %q declared more than once", n.ProductID)})
		}
		emitted[n.ProductID] = true
		if _, clash := productIDs[n.ProductID]; clash {
			out = append(out, Violation{Code: CodeIDMapInvalid, ConstructID: n.ProductID,
				Message: fmt.Sprintf("emitted product_id %q collides with a mapped id", n.ProductID)})
		}
		if !n.Synthetic {
			out = append(out, Violation{Code: CodeIDMapInvalid, ConstructID: n.ProductID,
				Message: fmt.Sprintf("emitted node %q is not marked synthetic", n.ProductID)})
		}
		if n.Provenance == "" {
			out = append(out, Violation{Code: CodeIDMapInvalid, ConstructID: n.ProductID,
				Message: fmt.Sprintf("emitted node %q has no provenance", n.ProductID)})
		}
		if n.RewriteRef == "" {
			out = append(out, Violation{Code: CodeIDMapInvalid, ConstructID: n.ProductID,
				Message: fmt.Sprintf("emitted node %q has no rewrite_ref", n.ProductID)})
		}
	}
	if len(productIDs) != mappedCount {
		out = append(out, Violation{Code: CodeIDMapInvalid,
			Message: fmt.Sprintf("inverse mapping size %d does not match mapped entries %d", len(productIDs), mappedCount)})
	}

	resolvable := func(id string) bool {
		if id == "" {
			return false
		}
		if _, ok := report.IDMap[id]; ok {
			return true
		}
		if _, ok := productIDs[id]; ok {
			return true
		}
		return emitted[id]
	}
	for _, e := range report.Edges {
		if !resolvable(e.Selector) {
			out = append(out, Violation{Code: CodeIDMapInvalid, ConstructID: e.Selector, Port: e.Port,
				Message: fmt.Sprintf("edge selector %q is not resolvable", e.Selector)})
		}
		if !resolvable(e.Target) {
			out = append(out, Violation{Code: CodeIDMapInvalid, ConstructID: e.Selector, Port: e.Port,
				Message: fmt.Sprintf("edge target %q is not resolvable", e.Target)})
		}
	}
	for _, r := range report.Refs {
		if !resolvable(r.Producer) {
			out = append(out, Violation{Code: CodeRefUnresolved, ConstructID: r.Consumer,
				Message: fmt.Sprintf("report ref producer %q is not resolvable", r.Producer)})
		}
		if !resolvable(r.Consumer) {
			out = append(out, Violation{Code: CodeIDMapInvalid, ConstructID: r.Consumer,
				Message: fmt.Sprintf("report ref consumer %q is not resolvable", r.Consumer)})
		}
	}
	for _, le := range report.LoopEdges {
		if !resolvable(le.Construct) {
			out = append(out, Violation{Code: CodeIDMapInvalid, ConstructID: le.Construct, Port: le.Port,
				Message: fmt.Sprintf("loop edge construct %q is not resolvable", le.Construct)})
		}
	}
	return out
}

func mergeAliasCycles(sem *Semantics, mergeIDs map[string]bool) []string {
	aliases := map[string][]string{}
	for _, c := range sem.Constructs {
		if c.Merge == nil || c.Merge.ID == "" {
			continue
		}
		for _, g := range sortedKeys(c.Merge.ValueClosure) {
			for _, cand := range c.Merge.ValueClosure[g].Candidates {
				if mergeIDs[cand.Producer] {
					aliases[c.Merge.ID] = append(aliases[c.Merge.ID], cand.Producer)
				}
			}
		}
	}
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := map[string]int{}
	var cycles []string
	var visit func(string) bool
	visit = func(id string) bool {
		switch color[id] {
		case grey:
			return true
		case black:
			return false
		}
		color[id] = grey
		for _, next := range aliases[id] {
			if visit(next) {
				color[id] = black
				return true
			}
		}
		color[id] = black
		return false
	}
	for _, id := range sortedKeys(aliases) {
		if visit(id) {
			cycles = append(cycles, id)
		}
	}
	return cycles
}

func closureKind(n int) string {
	switch {
	case n == 0:
		return ClosureDead
	case n == 1:
		return ClosureAlias
	default:
		return ClosurePhi
	}
}

func statusOrOpen(s string) string {
	if s == "" {
		return ObligationOpen
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func orderViolations(in []Violation) []Violation {
	out := append([]Violation(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		if out[i].ConstructID != out[j].ConstructID {
			return out[i].ConstructID < out[j].ConstructID
		}
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Message < out[j].Message
	})
	return out
}
