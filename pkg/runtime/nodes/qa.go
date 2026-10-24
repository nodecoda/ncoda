package nodes

// qa.go — question_answer 节点正式实现（M2.2，PRD U2.7 / D2）。
//
// HITL 语义:
//   - 首次执行:compose.StatefulInterrupt 抛出中断（带渲染后的问题文案）。
//     Eino runner 把它作为中断 err 传播（CLI 层 ExtractInterruptInfo →
//     --resume 持久化 → 退出码 4）。
//   - resume:同一节点经 checkpoint 恢复执行,GetResumeContext 携带注入
//     数据 → 输出 USER_RESPONSE（结果端口,coze 契约）+ 分支标记
//     （branch_<i> / default,供 action-branch 边路由）。
//
// loop 内 resume（M16-2 v2 已落地）:loop 驱动器每迭代以独立 checkpoint
// id 驱动 body,body 中断经 CompositeInterrupt 提升让外层写主 checkpoint
// （含 RuntimeState 进度快照）;resume 从 NextIter 快进,一次 resume 放行
// 一轮,下一迭代重新中断（每迭代确认语义）。qa 节点本身无需感知 loop:
// 中断/恢复全由 eino compose + loopDriver 编排提供。
//
// 依赖:无（中断/恢复由 Eino compose 提供;不依赖 Deps）。

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"

	"github.com/nodecoda/ncoda/pkg/expr"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// QA asks a human question (HITL, loop-external v1).
type QA struct{}

func (QA) Kind() string { return spec.KindQA }

func (QA) Build(ctx context.Context, n *spec.NodeSpec, _ Deps) (NodeImpl, error) {
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	return &qaNode{cfg: cfg.(*spec.QaConfig)}, nil
}

type qaNode struct{ cfg *spec.QaConfig }

func (q *qaNode) Invoke(ctx context.Context, in map[string]any) (map[string]any, error) {
	question := expr.RenderTemplate(q.cfg.Template, in)

	if isResume, hasData, data := compose.GetResumeContext[map[string]any](ctx); isResume && hasData {
		out := map[string]any{"USER_RESPONSE": data}
		// M15-2: form 字段平铺为输出端口（review.comment → comment 端口;
		// 分支体对 QA 的字段引用按端口解析,见 IR fieldPort(qa)=field）。
		for k, v := range data {
			if k == "choice" {
				continue
			}
			out[k] = v
		}
		// action-branch routing: resume data 的 choice 是 action 名
		// （approve/reject）或端口名（branch_0/branch_1/default）;
		// action 名映射到声明的 branch_<i> 端口（spec 边端口域）。
		out[MarkerBranch] = qaBranchKey(data["choice"], q.cfg.Choices)
		return out, nil
	}

	if err := compose.StatefulInterrupt(ctx, question, map[string]any{}); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("qa %s: unreachable after interrupt", q.cfg.Template)
}

// qaBranchKey 把 resume choice（action 名或端口名）映射到 spec 分支端口域
// （branch_<i> / default）。空值/未知 → default（对齐 coze 缺省分支）。
func qaBranchKey(choice any, choices []string) string {
	name, _ := choice.(string)
	if name == "" || name == "default" {
		return "default"
	}
	for i, c := range choices {
		if name == c {
			return fmt.Sprintf("branch_%d", i)
		}
	}
	// 已是端口名（branch_N）或自定义端口 → 透传
	return name
}
