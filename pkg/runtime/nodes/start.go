package nodes

// start.go — start 节点正式实现（从 compile stub 迁移，M2.1）。

import (
	"context"

	"github.com/nodecoda/ncoda/pkg/runtime/boundary"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// Start is the workflow entry: raw input passthrough + parameter defaults.
type Start struct{}

func (Start) Kind() string { return spec.KindStart }

func (Start) Build(ctx context.Context, n *spec.NodeSpec, _ Deps) (NodeImpl, error) {
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	return &startNode{params: cfg.(*spec.StartConfig).Params}, nil
}

type startNode struct{ params []spec.ParamSpec }

func (s *startNode) Invoke(_ context.Context, in map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	for _, p := range s.params {
		if _, ok := out[p.Name]; ok {
			continue
		}
		if p.Default != nil {
			out[p.Name] = p.Default
			continue
		}
		// 声明但未提供的参数 → 类型零值（参考目标 变量池语义:未提供 = 空,
		// 模板渲染空串而非 <missing>;M13 chatflow 的 query 缺省即此路径）。
		if v, ok := boundary.Zero(p.Type); ok {
			out[p.Name] = v
		}
	}
	return out, nil
}
