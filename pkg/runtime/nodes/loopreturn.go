package nodes

// loopreturn.go — loop_return 节点（M17, 46_loop_escape）。
//
// 终止最内层 loop 并携带返回值:Invoke 把值写入 RuntimeState 最内层
// loop 的 Returned/ReturnValue,同时置 Break 标记（停止迭代）。loop 驱动
// 器随后把 returned/return_value 输出到 loop 输出端口,spec 层短路分支
// （selector loop.returned → aggregate）据此把流程导向返回值而非正常
// 尾部。对齐参考目标 渲染的 __returning / __return_value 机制。
//
// 与 marker（break/continue）同模式:输出端口 "$return"（输出形状契约,
// 测试断言用）+ RuntimeState 写入（驱动器读状态而非扫端口;非图上下文
// 无状态时静默 no-op,端口仍发）。

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/state"
)

// LoopReturn is a loop-body return sink carrying the returned value.
type LoopReturn struct{}

func (LoopReturn) Kind() string { return spec.KindLoopReturn }

func (LoopReturn) Build(ctx context.Context, n *spec.NodeSpec, _ Deps) (NodeImpl, error) {
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	lc, ok := cfg.(*spec.LoopReturnConfig)
	if !ok {
		return nil, fmt.Errorf("loop_return %s: config is %T, want LoopReturnConfig", n.ID, cfg)
	}
	return &loopReturnNode{cfg: lc}, nil
}

type loopReturnNode struct{ cfg *spec.LoopReturnConfig }

func (r *loopReturnNode) Invoke(ctx context.Context, in map[string]any) (map[string]any, error) {
	v := in["value"] // assembled under Path "value" (Sources contract)
	_ = compose.ProcessState[*state.RuntimeState](ctx, func(_ context.Context, rs *state.RuntimeState) error {
		if ls := rs.TopLoop(); ls != nil {
			// loop state 端口的实时值优先:`i += 1; return i;` 应返回
			// assign 更新后的新值,而 body 子图的 loop 端口绑定是迭代
			// 开始快照（bind 静态）——Vars 是实时面。迭代变量（46 的
			// value,不在 Vars）回退到装配值。
			if rv, ok := ls.Vars[r.cfg.Value.Name]; ok {
				v = rv
			}
			ls.Returned = true
			ls.ReturnValue = v
			ls.Break = true
		}
		return nil
	})
	return map[string]any{"$return": v}, nil
}
