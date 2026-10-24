package spec

import (
	"encoding/json"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/obligation"
	"github.com/nodecoda/ncoda/pkg/observation"
	"github.com/nodecoda/ncoda/pkg/tool"
)

// Observer reports the runtime spec surface: nodes (including loop children)
// and edges (including inner loop edges).
type Observer struct{}

// NewObserver returns the runtime spec observer.
func NewObserver() *Observer { return &Observer{} }

// Target implements observation.Observer.
func (*Observer) Target() string { return obligation.TargetRuntime }

// Observe implements observation.Observer.
func (*Observer) Observe(irg *ir.Graph, projection *flow.Projection, _ *tool.Registry) (*observation.Observation, error) {
	s, err := Build(irg, projection)
	if err != nil {
		return nil, err
	}
	o := observation.New(obligation.TargetRuntime)
	var walkNodes func([]NodeSpec)
	walkNodes = func(nodes []NodeSpec) {
		for _, n := range nodes {
			o.Nodes = append(o.Nodes, observation.Node{ID: n.ID, Kind: n.Kind})
			if n.Kind == string(ir.KindLoop) {
				var cfg LoopConfig
				if err := json.Unmarshal(n.Config, &cfg); err == nil {
					walkNodes(cfg.Children)
				}
			}
		}
	}
	walkNodes(s.Nodes)
	var walkEdges func([]EdgeSpec)
	walkEdges = func(edges []EdgeSpec) {
		for _, e := range edges {
			o.Edges = append(o.Edges, observation.Edge{
				Source:     e.Source,
				Target:     e.Target,
				Port:       e.SourcePort,
				TargetPort: e.TargetPort,
			})
		}
	}
	walkEdges(s.Edges)
	for _, n := range s.Nodes {
		if n.Kind == string(ir.KindLoop) {
			var cfg LoopConfig
			if err := json.Unmarshal(n.Config, &cfg); err == nil {
				walkEdges(cfg.InnerEdges)
			}
		}
	}
	o.IndexIdentityMap(projection)
	return o, nil
}
