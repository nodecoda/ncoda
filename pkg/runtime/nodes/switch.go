package nodes

// switch.go — switch 节点正式实现（coze IntentDetector, type 22）。
//
// 语义：query 对声明的意图列表做确定性分类桩 —— 子串匹配（最长命中
// 优先）→ classificationId = 命中索引 / $branch = branch_<i>；无命中 →
// classificationId = -1 / $branch = default（对齐 coze 缺省分支端口）。
// reason 输出命中意图名（无命中 = ""）。
//
// 桩策略（与 echo model 同层）：L3 runtime 不调真实 LLM 分类，匹配是
// 确定性 + 可解释的；同一输入下 b/d 结果一致（round3 不动点前提）。
// 输入装配：query 固定挂 Source.Path=["query"]（emitSwitch 约定）。

import (
	"context"
	"fmt"
	"strings"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// Switch detects the classification class of a query.
type Switch struct{}

func (Switch) Kind() string { return spec.KindSwitch }

func (Switch) Build(ctx context.Context, n *spec.NodeSpec, _ Deps) (NodeImpl, error) {
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	return &switchNode{cfg: cfg.(*spec.SwitchConfig)}, nil
}

type switchNode struct{ cfg *spec.SwitchConfig }

func (it *switchNode) Invoke(_ context.Context, in map[string]any) (map[string]any, error) {
	query := anyString(in["query"])
	idx, reason := classifySwitch(query, it.cfg.Intents)
	port := "default"
	if idx >= 0 {
		port = fmt.Sprintf("branch_%d", idx)
	}
	return map[string]any{
		"classificationId": int64(idx),
		"reason":           reason,
		MarkerBranch:       port,
	}, nil
}

// classifySwitch returns the best-match label index (longest name wins,
// so "视频生成" beats "生成" when both match) or -1 with "" reason.
func classifySwitch(query string, intents []string) (int, string) {
	if query == "" {
		return -1, ""
	}
	best, bestLen := -1, 0
	for i, name := range intents {
		if name == "" {
			continue
		}
		if strings.Contains(query, name) && len(name) > bestLen {
			best, bestLen = i, len(name)
		}
	}
	if best >= 0 {
		return best, intents[best]
	}
	return -1, ""
}

func anyString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}
