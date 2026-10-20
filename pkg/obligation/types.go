// Package obligation defines the target-independent graph obligation schema
// for the graph obligation kernel (docs/GRAPH-OBLIGATION-KERNEL.md v2 §5).
//
// This package is a leaf: it must never import pkg/backend/flow or any
// backend. The dependency direction is flow → obligation only.
package obligation

// Schema versions carried by Plan and Report. They evolve independently.
const (
	PlanSchemaV2   = "ncoda.obligation-plan/v2"
	ReportSchemaV2 = "ncoda.obligation-report/v2"
)

// ValueClosureKind classifies a merge output group by candidate cardinality.
const (
	ClosureAlias = "alias" // exactly one candidate
	ClosurePhi   = "phi"   // two or more ordered candidates
	ClosureDead  = "dead"  // zero candidates (static dead path)
)

// Obligation status values (docs v2 §5.1).
const (
	ObligationOpen   = "open"
	ObligationClosed = "closed"
)

// IDMap states (docs v2 §5.3).
const (
	IDMapped  = "mapped"
	IDElided  = "elided"
	IDLowered = "lowered"
)

// Plan is the serialized Plan.Semantics document. Materialization is flow
// private and never part of this schema.
type Plan struct {
	Schema    string     `json:"schema"`
	Semantics *Semantics `json:"semantics"`
}

// Semantics is the cross-target contract: what the graph means, using only
// IR canonical ids (never product ids).
type Semantics struct {
	Constructs  []Construct  `json:"constructs"`
	Obligations []Obligation `json:"obligations"`
	Refs        []RefFact    `json:"refs"`
	Nodes       []NodeFact   `json:"nodes"`
}

// Construct is one control construct (if/loop/ask/switch/attempt/...).
type Construct struct {
	ID    string     `json:"id"`
	Kind  string     `json:"kind"`
	Arms  []ArmPlan  `json:"arms"`
	Merge *MergePlan `json:"merge,omitempty"`
	Loop  *LoopPlan  `json:"loop,omitempty"`
}

// ArmPlan is one declared control arm and its relevant entry/exit facts.
// Entry is a pointer so an empty arm serializes as JSON null.
type ArmPlan struct {
	Name     string   `json:"name"`
	Port     string   `json:"port"`
	Entry    *string  `json:"entry"`
	Exits    []string `json:"exits"`
	Empty    bool     `json:"empty"`
	Terminal string   `json:"terminal"`
}

// MergePlan is the convergence unit attached to a construct.
type MergePlan struct {
	ID           string                  `json:"id"`
	ValueClosure map[string]ValueClosure `json:"value_closure"`
}

// ValueClosure is an ordered candidate set for one merge output group.
type ValueClosure struct {
	Kind       string           `json:"kind"`
	Candidates []ValueCandidate `json:"candidates"`
}

// ValueCandidate names a real producer port. Candidates are ordered: the
// order is the first-non-zero path order and therefore semantic.
type ValueCandidate struct {
	Producer string `json:"producer"`
	Output   string `json:"output"`
}

// LoopPlan is the structural fact set of a loop container.
type LoopPlan struct {
	Entry string   `json:"entry"`
	Back  []string `json:"back"`
	Exit  string   `json:"exit"`
	Body  []string `json:"body"`
}

// Obligation is one branch-port edge obligation and its closure state.
type Obligation struct {
	Selector   string           `json:"selector"`
	Port       string           `json:"port"`
	Group      string           `json:"group"`
	Target     string           `json:"target"`
	TargetKind string           `json:"target_kind"`
	Candidates []ValueCandidate `json:"candidates"`
	Status     string           `json:"status"`
}

// RefFact is a data reference resolved against IR canonical ids.
type RefFact struct {
	Producer string           `json:"producer"`
	Output   string           `json:"output"`
	Consumer string           `json:"consumer"`
	Resolved []ValueCandidate `json:"resolved"`
}

// NodeFact records node reachability (diagnostic, A5/A10/A11).
type NodeFact struct {
	ID        string `json:"id"`
	Kind      string `json:"kind,omitempty"`
	Reachable bool   `json:"reachable"`
}

// Report is the target-side observation document (docs v2 §5.2).
type Report struct {
	Schema       string            `json:"schema"`
	Target       string            `json:"target"`
	IDMap        map[string]IDMap  `json:"id_map"`
	Constructs   []ReportConstruct `json:"constructs"`
	Nodes        []ReportNode      `json:"nodes"`
	Edges        []EdgeObs         `json:"edges"`
	LoopEdges    []LoopEdgeObs     `json:"loop_edges"`
	EmittedNodes []EmittedNode     `json:"emitted_nodes"`
	Refs         []ReportRef       `json:"refs"`
	Rewrites     []Rewrite         `json:"rewrites"`
	Violations   []Violation       `json:"violations"`
}

// IDMap maps one IR canonical id to a product id or an explicit elision.
type IDMap struct {
	ProductID  *string `json:"product_id"`
	State      string  `json:"state"`
	Reason     string  `json:"reason,omitempty"`
	RewriteRef string  `json:"rewrite_ref,omitempty"`
}

// ReportConstruct records the target's merge/loop decisions for a construct.
type ReportConstruct struct {
	ID        string    `json:"id"`
	MergeMode string    `json:"merge_mode,omitempty"`
	Loop      *LoopPlan `json:"loop,omitempty"`
}

// ReportNode records a product node's reachability.
type ReportNode struct {
	ID        string `json:"id"`
	Reachable bool   `json:"reachable"`
	Kind      string `json:"kind,omitempty"`
}

// EdgeObs is one observed branch-port edge. The comparison key is
// (Selector, Port, Group); repeat edges stay as separate entries and
// EdgeCount records the multiplicity.
type EdgeObs struct {
	Selector         string           `json:"selector"`
	Port             string           `json:"port"`
	Group            string           `json:"group"`
	Target           string           `json:"target"`
	TargetKind       string           `json:"target_kind,omitempty"`
	CanonicalLanding string           `json:"canonical_landing,omitempty"`
	Candidates       []ValueCandidate `json:"candidates"`
	EdgeCount        int              `json:"edge_count"`
}

// LoopEdgeObs is one observed loop structural edge.
type LoopEdgeObs struct {
	Construct        string `json:"construct"`
	Port             string `json:"port"`
	CanonicalLanding string `json:"canonical_landing"`
}

// EmittedNode is a target-synthesized node. Synthetic nodes never enter IDMap.
type EmittedNode struct {
	ProductID  string `json:"product_id"`
	Synthetic  bool   `json:"synthetic"`
	Provenance string `json:"provenance"`
	RewriteRef string `json:"rewrite_ref,omitempty"`
}

// ReportRef records whether a product-side reference resolved.
type ReportRef struct {
	Producer   string `json:"producer"`
	Output     string `json:"output"`
	Consumer   string `json:"consumer"`
	Resolvable bool   `json:"resolvable"`
}

// Rewrite is one declared lowering performed by a target.
type Rewrite struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
}

// Violation is one diagnostic produced by Validate or a target reporter.
type Violation struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	ConstructID string `json:"construct_id,omitempty"`
	Span        string `json:"span,omitempty"`
	Arm         string `json:"arm,omitempty"`
	Port        string `json:"port,omitempty"`
	Expected    string `json:"expected,omitempty"`
	Actual      string `json:"actual,omitempty"`
	EdgeClass   string `json:"edge_class,omitempty"`
}

// EdgeView is the narrow edge fact Validate needs. It is deliberately
// backend-neutral: no product ids, no platform types.
type EdgeView struct {
	Source     string
	Target     string
	SourcePort string
	TargetPort string
}

// RefView is the narrow reference fact Validate needs.
type RefView struct {
	Producer string
	Output   string
	Consumer string
}

// GraphView is the narrow projection surface implemented by pkg/backend/flow.
// Keeping it here (rather than importing flow) preserves the acyclic
// dependency direction obligation ← flow.
type GraphView interface {
	NodeIDs() []string
	Edges() []EdgeView
	Refs() []RefView
}
