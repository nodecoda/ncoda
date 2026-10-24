// Package observation defines the target-agnostic reverse-extraction view
// used by the obligation measurement layer.
//
// A target backend renders its artifact and reports the product nodes, edges
// and canonical→product id mapping it can prove. The obligation package turns
// that view into a Report; it never imports a backend itself.
package observation

import (
	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
)

// Node is one artifact node observed after rendering.
type Node struct {
	ID   string
	Kind string
}

// Edge is one artifact edge. Port is already normalized to the canonical
// vocabulary by the observer.
type Edge struct {
	Source     string
	Target     string
	Port       string
	TargetPort string
	Fanout     bool // 参考目标 OR-case fan-out of one IR true edge
}

// Observation is the raw reverse-extracted artifact view before id_map and
// Report semantics are applied.
type Observation struct {
	Target             string
	Nodes              []Node
	Edges              []Edge
	CanonicalToProduct map[string]string
	Rewrites           map[string]string // name -> detail
}

// New returns an empty observation for target.
func New(target string) *Observation {
	return &Observation{
		Target:             target,
		CanonicalToProduct: map[string]string{},
		Rewrites:           map[string]string{},
	}
}

// AddRewrite records the first detail for a lowering name.
func (o *Observation) AddRewrite(name, detail string) {
	if name == "" {
		return
	}
	if _, ok := o.Rewrites[name]; !ok {
		o.Rewrites[name] = detail
	}
}

// ProductIDs returns the set of product ids present in the artifact.
func (o *Observation) ProductIDs() map[string]bool {
	out := make(map[string]bool, len(o.Nodes))
	for _, n := range o.Nodes {
		out[n.ID] = true
	}
	return out
}

// IndexIdentityMap maps every projected IR id that survived into the
// artifact to itself (targets that preserve IR ids).
func (o *Observation) IndexIdentityMap(projection *flow.Projection) {
	if projection == nil {
		return
	}
	present := o.ProductIDs()
	seen := map[string]bool{}
	var add func(ir.Node)
	add = func(n ir.Node) {
		if n == nil {
			return
		}
		id := n.NodeID()
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		if present[id] {
			o.CanonicalToProduct[id] = id
		}
		if loop, ok := n.(*flow.ProjectedLoop); ok {
			for _, child := range loop.Children {
				add(child)
			}
		}
	}
	for _, n := range projection.Nodes {
		add(n)
	}
}
