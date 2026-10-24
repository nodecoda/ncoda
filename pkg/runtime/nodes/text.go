package nodes

// text.go — text 节点正式实现（从 compile stub 迁移，M2.1）。
//
// 语义与 stub 完全一致:
//   - concat:模板渲染（expr.RenderTemplate，缺失键 "<missing>"）
//   - const:JSON 数组形态的模板产出 []any（loop 输入数组测试助手）;
//     否则原样字符串
//   - split:按 concat_char 切分 in["content"]
//   - 未知 method:空串（M1 契约，确定性）

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/nodecoda/ncoda/pkg/expr"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// Text applies a text process operation.
type Text struct{}

func (Text) Kind() string { return spec.KindText }

func (Text) Build(ctx context.Context, n *spec.NodeSpec, _ Deps) (NodeImpl, error) {
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	return &textNode{cfg: cfg.(*spec.TextConfig)}, nil
}

type textNode struct{ cfg *spec.TextConfig }

func (t *textNode) Invoke(_ context.Context, in map[string]any) (map[string]any, error) {
	switch t.cfg.Method {
	case "concat":
		return map[string]any{"output": expr.RenderTemplate(t.cfg.Template, in)}, nil
	case "const":
		// JSON-array-shaped const templates produce a list (test helper
		// for loop input arrays); otherwise a plain string.
		var arr []any
		if json.Unmarshal([]byte(t.cfg.Template), &arr) == nil {
			return map[string]any{"output": arr}, nil
		}
		return map[string]any{"output": t.cfg.Template}, nil
	case "split":
		return map[string]any{"output": t.split(in)}, nil
	}
	return map[string]any{"output": ""}, nil
}

// split 按**声明的输入路径**取文本，再按 delimiters（splitParams）切分。
//
// 两个契约点（2026-10-21，语料一手）：
//
//  1. 输入键来自 wire，不是实现自选：`node_inputs[].name` 由编译器落在
//     `TextConfig.Inputs[].Path`（真实导出 `name: input` / `name: String`），
//     运行时把值放在 `in[path 末段]`。旧实现读 `in["content"]`——该键在
//     *任何*导出里都不存在 ⇒ 恒切空串：空分隔符得 `[]`（`splitByAny` 之前
//     的 `strings.Split("", "")`），单分隔符得 `[""]`；下游 code 拿到的
//     元素不是 string（`.replace`/`.trim` 崩）。判据
//     `TestTextSplitReadsDeclaredInputPath`。
//  2. 多分隔符是平台语义：`splitParams.delimiters` 是列表（换行/制表/句号/
//     逗号/分号），旧实现只读 `concat_char`（单分隔）⇒ 列表中的分隔符被忽略。
//     判据 `TestTextSplitSeparators`。
func (t *textNode) split(in map[string]any) []string {
	src := t.splitInput(in)
	seps := t.cfg.Separators
	if len(seps) == 0 && t.cfg.ConcatChar != "" {
		seps = []string{t.cfg.ConcatChar}
	}
	return splitByAny(src, seps)
}

// splitInput 取切分源：先按声明路径（wire 事实），再兼容历史/手工节点
// 的 "content"（单测与早期 stub 形态）。键存在但非 string 时按空串处理
// （与平台 TextProcessor 的字符串化读法一致：不 panic、不伪造数据）。
func (t *textNode) splitInput(in map[string]any) string {
	for _, src := range t.cfg.Inputs {
		if len(src.Path) == 0 {
			continue
		}
		if v, ok := in[src.Path[len(src.Path)-1]]; ok {
			s, _ := v.(string)
			return s
		}
	}
	if v, ok := in["content"]; ok {
		s, _ := v.(string)
		return s
	}
	if v, ok := in["input"]; ok {
		s, _ := v.(string)
		return s
	}
	return ""
}

// splitByAny 按**任意**分隔符切分，保留空段（Python str.split 的显式分隔
// 语义：`"a,,b"` → ["a","","b"]），空分隔符跳过。无分隔符 = 原样单元素。
func splitByAny(s string, seps []string) []string {
	eff := make([]string, 0, len(seps))
	for _, sep := range seps {
		if sep != "" {
			eff = append(eff, sep)
		}
	}
	if len(eff) == 0 {
		return []string{s}
	}
	out := []string{}
	start := 0
	for i := 0; i < len(s); {
		matched := ""
		for _, sep := range eff {
			if strings.HasPrefix(s[i:], sep) {
				matched = sep
				break
			}
		}
		if matched == "" {
			i++
			continue
		}
		out = append(out, s[start:i])
		i += len(matched)
		start = i
	}
	return append(out, s[start:])
}
