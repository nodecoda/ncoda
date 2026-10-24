package nodes

// knowledge.go — knowledge 节点正式实现（M2.1 骨架,M14 解锁 RAG）。
//
// 语义（对齐参考目标 knowledge-retrieval）:
//   - 输入:Query（装配端口,来自 spec.Inputs 的 "Query" path;IR 层
//     knowledge(...) 第二个参数）。
//   - 输出:outputList（检索片段数组,每项 {documentId, output};
//     IR 的 docs.result 引用经 fieldPort 映射到该端口;参考目标渲染端把
//     outputList 映射为 result 端口）。
//   - 检索后端:Deps.Knowledge 注入（离线测试 stub / 真实向量库适配器）。
//     nil → Build 期 RKindRejected（D4 诚实 fail-fast:未配置检索能力时
//     工作流不可运行,而不是运行到一半报错）。
//
// 检索结果嵌入 prompt 时由模板渲染端处理（resolveBlockTemplate /
// RenderTemplate 对 []any 走 fmt.Sprint,确定性）。

import (
	"context"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// Knowledge retrieves from datasets (v1: injected backend required).
type Knowledge struct{}

func (Knowledge) Kind() string { return spec.KindKnowledge }

func (Knowledge) Build(ctx context.Context, n *spec.NodeSpec, deps Deps) (NodeImpl, error) {
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	if deps.Knowledge == nil {
		return nil, diag.New(diag.RKindRejected,
			"node %s: knowledge node requires a retrieval backend (Deps.Knowledge); none injected (D4) — pass --knowledge <index.jsonl> to ncoda run", n.ID)
	}
	return &knowledgeNode{cfg: cfg.(*spec.KnowledgeConfig), kr: deps.Knowledge}, nil
}

type knowledgeNode struct {
	cfg *spec.KnowledgeConfig
	kr  KnowledgeRetriever
}

func (k *knowledgeNode) Invoke(ctx context.Context, in map[string]any) (map[string]any, error) {
	query, _ := in["Query"].(string)
	chunks, err := k.kr.Retrieve(ctx, query, k.cfg.DatasetIDs)
	if err != nil {
		return nil, diag.New(diag.RNodeError, "knowledge %v: %v", k.cfg.DatasetIDs, err)
	}
	out := make([]any, 0, len(chunks))
	for _, c := range chunks {
		// 元素字段集 = 平台事实 {documentId, output}（K-H2 · 2026-10-17 用户裁定）；
		// Title/Score 仍是检索后端的内部信息，不进入语言可见的元素形状。
		out = append(out, map[string]any{
			"documentId": c.DocumentID,
			"output":     c.Content,
		})
	}
	return map[string]any{"outputList": out}, nil
}
