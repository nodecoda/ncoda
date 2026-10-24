package toolbridge

// toolbridge.go — 运行期插件声明 → 编译期语义注册表的**唯一投影**。
//
// 运行期插件（pkg/runtime/plugin）与编译期注册表（本包）是同一份声明的两个
// 消费面：插件给 runtime 提供 Call/Describe，注册表给前端提供参数强检查与
// 结果形状。桥接必须无损耗——参数 Desc/Default/Enum/Min/Max 全部提升，
// 否则「声明约束」在桥接处悄悄消失（Tier-2 review #11 的实测事故）。
//
// 本文件在开放内核里：runtime lane（ncoda-run --local-plugins）与闭源适配器
// 共用同一份投影，避免两侧各写一份而漂移。

import (
	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
	"github.com/nodecoda/ncoda/pkg/tool"
)

// FromPluginManager 把运行期插件声明投影到编译期语义注册表，使脚本的
// import "provider" 与限定名调用 p.op({...}) 在类型阶段获得强参数检查与
// 结果形状。
//
// 类型名映射：ParamSchema.Type（JSON Schema 风格）→ 语义类型名。
func FromPluginManager(pm *plugin.Manager) (*tool.Registry, error) {
	var defs []tool.Definition
	for _, pid := range pm.PluginIDs() {
		pl, _ := pm.Plugin(pid)
		for _, t := range pl.Tools {
			def := tool.Definition{
				Provider:    pid,
				Operation:   t.Name,
				Description: t.Desc,
			}
			for _, ps := range t.Params {
				def.Parameters = append(def.Parameters, tool.Parameter{
					Name:     ps.Name,
					Type:     semanticType(ps.Type),
					Desc:     ps.Desc,
					Required: ps.Required,
					Default:  ps.Default,
					Enum:     ps.Enum,
					Min:      ps.Min,
					Max:      ps.Max,
				})
			}
			if len(t.Outputs) > 0 {
				for _, o := range t.Outputs {
					def.Outputs = append(def.Outputs, tool.OutputField{Name: o.Name, Type: o.Type})
				}
			} else {
				def.Outputs = []tool.OutputField{{Name: "result", Type: "any"}}
			}
			defs = append(defs, def)
		}
	}
	return tool.NewRegistry(defs...)
}

// semanticType 映射 JSON Schema 风格类型 → 语义类型名。
func semanticType(t string) string {
	switch t {
	case "object":
		return "dict"
	case "array":
		return "array<any>"
	case "int", "number", "string", "bool":
		return t
	default:
		if t == "" {
			return "any"
		}
		return t
	}
}

// MergeDefinitions 按 first-wins 合并语义工具定义（provider, operation 唯一）。
// 调用方把优先级高的组放前面。
//
// first-wins 只覆盖 transport/签名本体；policy 位（retry/failure_branch/
// timeout_ms/idempotent）不是插件声明面的一部分（plugin.Tool 没有这些字段），
// 因此不能被「显式 -plugin」静默清零 —— 否则能力头文件写了
// `@capability retry=true` 的算子，在挂上本地插件后调用点会被前端判成
// OPERATION_POLICY。政策位取首个非零声明。
func MergeDefinitions(defs []tool.Definition) []tool.Definition {
	index := make(map[string]int, len(defs))
	out := make([]tool.Definition, 0, len(defs))
	for _, def := range defs {
		key := tool.SymbolKey(def.Provider, def.Operation)
		if i, ok := index[key]; ok {
			mergeDefinitionPolicy(&out[i], def)
			continue
		}
		index[key] = len(out)
		out = append(out, def)
	}
	return out
}

// mergeDefinitionPolicy 把 src 的 policy 位补进 dst（dst 已声明的不覆盖）。
func mergeDefinitionPolicy(dst *tool.Definition, src tool.Definition) {
	if !dst.SupportsRetry {
		dst.SupportsRetry = src.SupportsRetry
	}
	if !dst.SupportsFailureBranch {
		dst.SupportsFailureBranch = src.SupportsFailureBranch
	}
	if dst.TimeoutMS == 0 {
		dst.TimeoutMS = src.TimeoutMS
	}
	if !dst.Idempotent {
		dst.Idempotent = src.Idempotent
	}
}
