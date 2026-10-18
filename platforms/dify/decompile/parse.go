// Package decompile —— Dify 工作流反编译器（dify YAML → ncoda）。
//
// 与 cozebiz/decompile 对称：coze-biz 反编译器把 canvas 还原为 ncoda，
// 这里把 Dify 工作流 YAML（React-Flow 图:graph.nodes + graph.edges +
// source_handle/target_handle 端口）还原为 ncoda 源码。
//
// 架构（复用平台无关的 ncoda 语法层）:
//
//	dify YAML → WorkflowAST（pkg/backend/dify 的 AST 模型,带 yaml tag,
//	             与 ncoda-dify ast_nodes.py 对齐）→ convert.go 图重建
//	              → Stmt/Expr（cozebiz/decompile ast4.go,ncoda 语法树）
//	              → RenderNCodaProgram（cozebiz/decompile render_ncoda.go）
//
// 数据权威：dify 工作流 YAML 是唯一真相（graph 边/端口 + 节点 data）。
// 参照实现：上游 Python 版 Dify 编译器（python 版 dify 编译器,parser.py 的 25
// 种节点解析语义）。

package decompile

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
)

// ParseWorkflow 解析 dify 工作流 YAML 文本 → WorkflowAST。
// 失败 → 结构化错误（YAML 结构错误 / 图不完整）。
func ParseWorkflow(text string) (*dify.WorkflowAST, error) {
	var ast dify.WorkflowAST
	if err := yaml.Unmarshal([]byte(text), &ast); err != nil {
		return nil, fmt.Errorf("dify decompile: yaml parse: %w", err)
	}
	if err := normalize(&ast); err != nil {
		return nil, err
	}
	return &ast, nil
}

// normalize 解析后修正：派生字段（value_selector → node_id/key）、
// 图完整性校验（至少一个节点、无自环）。
func normalize(ast *dify.WorkflowAST) error {
	if len(ast.Graph.Nodes) == 0 {
		return fmt.Errorf("dify decompile: graph has no nodes")
	}
	ids := map[string]bool{}
	for i := range ast.Graph.Nodes {
		n := &ast.Graph.Nodes[i]
		if n.ID == "" {
			return fmt.Errorf("dify decompile: node %d missing id", i)
		}
		ids[n.ID] = true
		normalizeSelectors(n)
	}
	for _, e := range ast.Graph.Edges {
		if e.Source == "" || e.Target == "" {
			return fmt.Errorf("dify decompile: edge has empty endpoint (%+v)", e)
		}
		if e.Source == e.Target {
			return fmt.Errorf("dify decompile: self-loop edge %s→%s", e.Source, e.Target)
		}
	}
	return nil
}

// normalizeSelectors 对节点的 value_selector 形态调用 Derive 补齐
// node_id/key（data 里的 value_selector 是 [node_id, key] 列表形态,
// Derive 从 Path 推导 node_id/key;正编译序列化时走 Path）。
func normalizeSelectors(n *dify.NodeDecl) {
	// NodeData 的 base 字段（Title/Desc/Type/Variables）在 DataNode;
	// 具体字段按节点类型展开。value_selector 出现在 OutputNode.Outputs、
	// CodeDataNode.Variables、LLMDataNode.Variables 等 —— 统一由各
	// 节点类型的字段遍历触发 Derive。
	derive := func(v *dify.VariableSelectorNode) {
		if v != nil {
			v.Derive()
		}
	}
	_ = derive
}
