package observation

import (
	"fmt"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/tool"
)

// Observer renders one target and reports what the artifact preserved.
// Errors are wrapped in the obligation layer as GenerationError.
type Observer interface {
	Target() string
	Observe(irg *ir.Graph, projection *flow.Projection, registry *tool.Registry) (*Observation, error)
}

// Registry resolves target names to observers. Registration order is the
// deterministic target order used for `--target all`.
type Registry struct {
	order    []string
	byTarget map[string]Observer
}

// NewRegistry returns a registry containing observers in order. Duplicate or
// invalid observers are programmer errors and panic.
func NewRegistry(observers ...Observer) *Registry {
	r := &Registry{byTarget: map[string]Observer{}}
	for _, o := range observers {
		if err := r.Register(o); err != nil {
			panic(err)
		}
	}
	return r
}

// Register adds one observer. It rejects nil, empty and duplicate targets.
func (r *Registry) Register(o Observer) error {
	if o == nil {
		return fmt.Errorf("observation: nil observer")
	}
	target := o.Target()
	if target == "" {
		return fmt.Errorf("observation: observer with empty target")
	}
	if _, exists := r.byTarget[target]; exists {
		return fmt.Errorf("observation: duplicate observer for target %q", target)
	}
	r.byTarget[target] = o
	r.order = append(r.order, target)
	return nil
}

// Lookup returns the observer for target.
func (r *Registry) Lookup(target string) (Observer, bool) {
	if r == nil {
		return nil, false
	}
	o, ok := r.byTarget[target]
	return o, ok
}

// Targets returns the registered targets in registration order.
func (r *Registry) Targets() []string {
	if r == nil {
		return nil
	}
	return append([]string(nil), r.order...)
}
