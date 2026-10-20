//go:build !plan

package main

import (
	"fmt"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
)

// planProjection is unavailable in snapshot-only builds: the tool must
// compile at the pre-solver baseline, where flow.Plan does not exist yet.
func planProjection(g *ir.Graph) (*flow.Projection, error) {
	return nil, fmt.Errorf("this binary was built without -tags plan; rebuild at the solver revision")
}
