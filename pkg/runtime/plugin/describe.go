package plugin

// describe.go — 工具 JSON Schema 描述（nodes.ToolDescriber 实现）。
//
// agent 节点通过 ToolDescriber 把注册表里的工具参数声明投影为模型可见的
// JSON Schema（对齐参考目标 PromptMessageTool：name/description/parameters）。

import (
	"context"

	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
)

var _ nodes.ToolDescriber = (*Manager)(nil)

// Describe 返回工具的工具描述（JSON Schema 参数）。name 支持完整逻辑名
// 与退化名（与 Call 同语义）。
func (m *Manager) Describe(ctx context.Context, name string) (nodes.ToolSpec, bool) {
	entry, err := m.lookupLenient(name)
	if err != nil {
		return nodes.ToolSpec{}, false
	}
	t := entry.Tool
	props := map[string]any{}
	var required []string
	for _, p := range t.Params {
		prop := map[string]any{
			"type":        jsonSchemaPrimitive(p.Type),
			"description": p.Desc,
		}
		// parameter_rules（对齐参考目标 ToolParameter options/min/max）:约束
		// 投影进 JSON Schema,agent 模型可见 → 生成合法参数。
		if len(p.Enum) > 0 {
			prop["enum"] = p.Enum
		}
		if p.Min != nil {
			prop["minimum"] = *p.Min
		}
		if p.Max != nil {
			prop["maximum"] = *p.Max
		}
		props[p.Name] = prop
		if p.Required {
			required = append(required, p.Name)
		}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return nodes.ToolSpec{Name: t.Name, Description: t.Desc, Parameters: schema}, true
}

// jsonSchemaPrimitive 映射语义类型 → JSON Schema primitive。
func jsonSchemaPrimitive(t string) string {
	switch t {
	case "int":
		return "integer"
	case "number":
		return "number"
	case "bool":
		return "boolean"
	case "object":
		return "object"
	case "array":
		return "array"
	default:
		return "string"
	}
}
