package nodes

// end.go — end 节点正式实现（从 compile stub 迁移，M2.1）。
//
// 终止语义（D3/PRD U2.1）:Inputs 喂规范 "output" 结果键;Outputs 喂
// 命名结果键。named_return 的 TerminatePlan 声明由 compile/CLI 层消费,
// 节点本身只做端口聚合。

import (
	"context"

	"github.com/nodecoda/ncoda/pkg/expr"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// End aggregates the terminal result ports.
type End struct{}

func (End) Kind() string { return spec.KindEnd }

func (End) Build(ctx context.Context, n *spec.NodeSpec, _ Deps) (NodeImpl, error) {
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	return &endNode{cfg: cfg.(*spec.EndConfig)}, nil
}

type endNode struct{ cfg *spec.EndConfig }

func (e *endNode) Invoke(_ context.Context, in map[string]any) (map[string]any, error) {
	if e.cfg.Answer != nil {
		return map[string]any{"output": expr.RenderTemplate(e.cfg.Answer.Template, in)}, nil
	}
	out := map[string]any{}
	if v, ok := in["output"]; ok {
		out["output"] = v
	}
	for _, o := range e.cfg.Outputs {
		if v, ok := in[o.Name]; ok {
			out[o.Name] = v
		}
	}
	return out, nil
}
