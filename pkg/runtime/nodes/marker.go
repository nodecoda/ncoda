package nodes

// marker.go — break / continue sink 节点正式实现（从 compile stub 迁移，
// M2.1;M2.3 状态面）。D5:体子图汇入 END 的 sink。控制标记双通道:
//   - 输出端口 "$break"/"$continue"（输出形状契约,测试断言用）;
//   - RuntimeState 最内层 loop 的 Break/Continue 标记（M2.3 起驱动器
//     读状态而非扫描端口;非图上下文无状态时静默 no-op,端口仍发）。

import (
	"context"

	"github.com/cloudwego/eino/compose"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/state"
)

// Break is a loop-body exit sink.
type Break struct{}

func (Break) Kind() string { return spec.KindBreak }

func (Break) Build(ctx context.Context, n *spec.NodeSpec, _ Deps) (NodeImpl, error) {
	return &markerNode{marker: MarkerBreak}, nil
}

// Continue is a loop-body iteration-skip sink.
type Continue struct{}

func (Continue) Kind() string { return spec.KindContinue }

func (Continue) Build(ctx context.Context, n *spec.NodeSpec, _ Deps) (NodeImpl, error) {
	return &markerNode{marker: MarkerContinue}, nil
}

type markerNode struct{ marker string }

func (m *markerNode) Invoke(ctx context.Context, _ map[string]any) (map[string]any, error) {
	if box := state.IterMarkersFrom(ctx); box != nil {
		// parallel iteration: markers stay local to this worker.
		if m.marker == MarkerBreak {
			box.Break = true
		} else {
			box.Continue = true
		}
		return map[string]any{m.marker: true}, nil
	}
	_ = compose.ProcessState[*state.RuntimeState](ctx, func(_ context.Context, rs *state.RuntimeState) error {
		if ls := rs.TopLoop(); ls != nil {
			if m.marker == MarkerBreak {
				ls.Break = true
			} else {
				ls.Continue = true
			}
		}
		return nil
	})
	return map[string]any{m.marker: true}, nil
}
