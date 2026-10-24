package nodes

// selector.go — selector 节点正式实现（从 compile stub 迁移，M2.1）。
//
// 条件求值 → 分支键（复用 expr.Compare 的 coze SelectorOperators 语义）。
// 输出 {"$branch": "true"/"false"},由 compile 分支门读取（MarkerBranch）。
// 操作数按 AssembleInput 路径约定读取:["0",i,"left"/"right"] 或
// ["0","left"/"right"]（i==0 时无索引层）。

import (
	"context"
	"fmt"

	"github.com/nodecoda/ncoda/pkg/expr"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// Selector branches on conditions.
type Selector struct{}

func (Selector) Kind() string { return spec.KindSelector }

func (Selector) Build(ctx context.Context, n *spec.NodeSpec, _ Deps) (NodeImpl, error) {
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	return &selectorNode{cfg: cfg.(*spec.SelectorConfig)}, nil
}

type selectorNode struct{ cfg *spec.SelectorConfig }

// evalOperand reads an operand value from the assembled input map
// (path convention ["0",i,"left"/"right"] or ["0","left"/"right"]).
func (s *selectorNode) evalOperand(i int, side string, in map[string]any) any {
	m, ok := in["0"].(map[string]any)
	if !ok {
		return nil
	}
	if i > 0 {
		mm, ok := m[fmt.Sprintf("%d", i)].(map[string]any)
		if !ok {
			return nil
		}
		return mm[side]
	}
	return m[side]
}

func (s *selectorNode) Invoke(_ context.Context, in map[string]any) (map[string]any, error) {
	// logic: 0=and (all conditions), 1=or (any condition)。累加器按语义
	// 初始化:and 从 true 起(恒等元),or 从 false 起。
	res := s.cfg.Logic != 1 // and → 恒等元 true; or → 恒等元 false
	for i, cond := range s.cfg.Conditions {
		lv := s.evalOperand(i, "left", in)
		rv := s.evalOperand(i, "right", in)
		ok := expr.Compare(cond.Operator, lv, rv)
		if s.cfg.Logic == 1 { // or
			res = res || ok
		} else { // and
			res = res && ok
		}
	}
	port := "false"
	if res {
		port = "true"
	}
	return map[string]any{MarkerBranch: port}, nil
}
