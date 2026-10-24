package nodes

// output.go — output 节点正式实现（M2.1，PRD U2.9）。
//
// 模板渲染 → 输出端口 "output"。模板占位符形如
// {{block_output_<producer>.<port>}}（emitOutputStmt 内联的 ref 形态）;
// compile 层装配时把每个占位符解析成 producer 的实际值放入输入 map
// （键 = 完整占位符名）,节点只做确定性替换（expr.RenderTemplate）。

import (
	"context"

	"github.com/nodecoda/ncoda/pkg/expr"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// Output emits a rendered template value.
type Output struct{}

func (Output) Kind() string { return spec.KindOutput }

func (Output) Build(ctx context.Context, n *spec.NodeSpec, _ Deps) (NodeImpl, error) {
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	return &outputNode{cfg: cfg.(*spec.OutputConfig)}, nil
}

type outputNode struct{ cfg *spec.OutputConfig }

func (o *outputNode) Invoke(_ context.Context, in map[string]any) (map[string]any, error) {
	return map[string]any{"output": expr.RenderTemplate(o.cfg.Template, in)}, nil
}
