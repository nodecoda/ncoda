//go:build plan

package main

import (
	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
)

// planProjection is the new-path half of the dual run: Plan derives the
// semantics, ProjectFromPlan replays the frozen materialization.
func planProjection(g *ir.Graph) (*flow.Projection, error) {
	bundle, err := flow.Plan(g)
	if err != nil {
		return nil, err
	}
	return flow.ProjectFromPlan(bundle)
}
