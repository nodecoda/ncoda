package nodes

// subworkflow.go — SubWorkflow 节点正式实现（SUBWORKFLOW-DECLARATION）。
//
// 语义：
//   - 执行通道：Deps.SubWorkflows（SubWorkflowRunner）。nil → RNodeError。
//   - 身份：WorkflowID + WorkflowVersion 标识外部子工作流（coze-biz/coze-studio）。
//   - 输入：装配阶段按 Source.Path 组织进 `in`（key = input 契约字段名），
//     直接透传给 runner。
//   - 输出端口：runner 返回 map 按输出端口 key 组织（与 OutTypes 声明一致）。
//   - 依赖缺失（无 SubWorkflowRunner）→ RNodeError（fail-closed，与 tool/knowledge 一致）。

import (
	"context"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// SubWorkflow invokes an external subworkflow by platform identity.
type SubWorkflow struct{}

func (SubWorkflow) Kind() string { return spec.KindSubWorkflow }

func (SubWorkflow) Build(ctx context.Context, n *spec.NodeSpec, deps Deps) (NodeImpl, error) {
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	swc := cfg.(*spec.SubWorkflowConfig)
	if deps.SubWorkflows == nil {
		return nil, diag.New(diag.RNodeError,
			"node %s: subworkflow %q (v%s) requires a SubWorkflowRunner (WithDeps)",
			n.ID, swc.WorkflowID, swc.WorkflowVersion)
	}
	return &subWorkflowNode{cfg: swc, runner: deps.SubWorkflows}, nil
}

type subWorkflowNode struct {
	cfg    *spec.SubWorkflowConfig
	runner SubWorkflowRunner
}

func (n *subWorkflowNode) Invoke(ctx context.Context, in map[string]any) (map[string]any, error) {
	// 装配输入已按 subflow 声明 input 契约字段名 keyed 进 in；
	// 只透传声明过的输入（不把运行期噪音键泄露给外部）。
	inputs := map[string]any{}
	for _, src := range n.cfg.Inputs {
		name := ""
		if len(src.Path) > 0 {
			name = src.Path[0]
		} else {
			name = src.Name
		}
		if name == "" {
			continue
		}
		inputs[name] = in[name]
	}
	return n.runner.Run(ctx, SubWorkflowRequest{
		WorkflowID:      n.cfg.WorkflowID,
		WorkflowVersion: n.cfg.WorkflowVersion,
		SpaceID:         n.cfg.SpaceID,
		Inputs:          inputs,
	})
}
