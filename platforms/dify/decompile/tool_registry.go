package decompile

// tool_registry.go —— dify 工具能力契约（反编译侧注册表）。
//
// 前端对限定名工具调用（<ns>.<op>({params})）是闭集契约:无 ToolRegistry
// 时报 "Package namespace ... cannot be used as a value"。coze 侧能力契约
// 来自 collector.db 生成的头文件;dify 无 DB——契约就地取材:工具节点的
// tool_parameters 键即参数签名,output_schema 缺省时输出形状为
// {result: any}（开放世界默认,对齐 registryToolOutputs 的降级）。
//
// 用法:反编译一个含 tool 节点的 dify YAML 后,用 ToolRegistryForYAML 构建
// 注册表挂到管线（WithToolRegistry）,产物即可通过前端强类型检查。

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/nodecoda/ncoda/pkg/tool"
)

// toolRefRe 匹配模板引用 {{#<toolID>.<key>#}}（输出键扫描用）。
var toolRefRe = regexp.MustCompile(`\{\{#([a-zA-Z0-9_\-]+)\.([a-zA-Z0-9_\-]+)#\}\}`)

// ToolRegistryForYAML 扫描 dify YAML 的 tool 节点,构建工具能力注册表。
// 每个 tool 节点 → Definition{Provider: <tool_name>, Operation: <tool_name>,
// Parameters: tool_parameters 键（type any,接受任意值）,Outputs: 输出键
// = 全语料模板/选择器对该 tool 的引用键 ∪ {result}（type any;dify 无
// collector.db,契约就地取材——引用即使用契约,闭集强校验不误报）。
func ToolRegistryForYAML(text string) (*tool.Registry, error) {
	g, err := parseYAML(text)
	if err != nil {
		return nil, err
	}
	// 引用键扫描:{{#toolID.key#}}（模板）∪ value_selector [toolID, key]。
	// 先种子化全部 tool 节点 id——selector 引用（如 assigner value 里的
	// [toolID, key]）没有模板占位符,只靠 selector 收集。
	refKeys := map[string]map[string]bool{}
	for _, n := range g.nodes {
		if n.Type == "tool" {
			refKeys[n.ID] = map[string]bool{}
		}
	}
	for _, m := range toolRefRe.FindAllStringSubmatch(text, -1) {
		if refKeys[m[1]] == nil {
			refKeys[m[1]] = map[string]bool{}
		}
		refKeys[m[1]][m[2]] = true
	}
	for _, n := range g.nodes {
		collectSelectorKeys(n, refKeys)
	}
	// 同名工具去重:同一 provider/operation 的多个工具节点（如 3 个 jina_reader
	// 调用的不同语言变体）→ 合并参数/输出,避免 duplicate definition 注册失败
	// （注册失败 = 闭集校验缺失 = 限定名调用退化为 any 动态调用,编译报错）。
	byKey := map[string]int{} // SymbolKey → defs 下标
	var defs []tool.Definition
	for _, n := range g.nodes {
		if n.Type != "tool" {
			continue
		}
		name := toolSymbolName(asStr(n.Data["tool_name"]))
		if name == "" {
			name = toolSymbolName(asStr(n.Data["provider_name"]))
		}
		if name == "" {
			continue
		}
		var params []tool.Parameter
		pm := asMap(n.Data["tool_parameters"])
		for _, k := range sortedKeys(pm) {
			params = append(params, tool.Parameter{Name: k, Type: "any"})
		}
		// 输出 = 引用键 ∪ {result}（本函数 docstring 的契约）。seen **不能**
		// 预置 "result"：预置会让循环跳过它、又让下面那次补写永远为假 ⇒ 输出表
		// 退化成空。若该工具只被 `.result` 引用（如 builtin json_parse，2026-10-09
		// 一手），产物就成了 void ⇒ 前端 FIELD_NOT_FOUND。先收引用键，再补 result。
		seen := map[string]bool{}
		var outputs []tool.OutputField
		for _, k := range sortedMapKeys(refKeys[n.ID]) {
			if !seen[k] {
				seen[k] = true
				outputs = append(outputs, tool.OutputField{Name: k, Type: knownToolOutputType(name, k)})
			}
		}
		if !seen["result"] {
			outputs = append(outputs, tool.OutputField{Name: "result", Type: knownToolOutputType(name, "result")})
		}
		key := tool.SymbolKey(name, name)
		if idx, dup := byKey[key]; dup {
			defs[idx].Parameters = mergeParams(defs[idx].Parameters, params)
			defs[idx].Outputs = mergeOutputs(defs[idx].Outputs, outputs)
			continue
		}
		byKey[key] = len(defs)
		defs = append(defs, tool.Definition{
			Provider:   name,
			Operation:  name,
			Parameters: params,
			Outputs:    outputs,
		})
	}
	if len(defs) == 0 {
		return tool.NewRegistry() // 空注册表;无工具节点时不触发闭集
	}
	r, err := tool.NewRegistry(defs...)
	if err != nil {
		return nil, fmt.Errorf("dify decompile: tool registry: %w", err)
	}
	return r, nil
}

// collectSelectorKeys 收集所有节点 value_selector/value 数组里对工具节点
// 的引用键（模板之外的 selector 形态）。
func collectSelectorKeys(n *nodeInfo, refKeys map[string]map[string]bool) {
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			// value_selector 形态 [node, key, ...] → 首段是节点 id,次段是键。
			if len(x) >= 2 {
				if nid, ok := x[0].(string); ok {
					if key, ok := x[1].(string); ok && refKeys[nid] != nil {
						refKeys[nid][key] = true
					}
				}
			}
			for _, item := range x {
				walk(item)
			}
		case map[string]any:
			for _, item := range x {
				walk(item)
			}
		}
	}
	walk(n.Data)
}

// sortedMapKeys 确定性遍历 map[string]bool 键。
func sortedMapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// toolSymbolName 工具名 → 合法的限定名符号（import provider/操作名要求
// [a-z0-9_][a-z0-9_.-]*）:小写 + 非法字符转 '_'（"localScrape" → "localscrape";
// 中文 provider 名降级为 tool_name,再非法则空）。
func toolSymbolName(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '.':
			b.WriteRune(r)
		case r == '-':
			// 连字符工具名（generate-audio_post 等社区插件）:ncoda 标识符
			// 无 '-'（会解析成减法）,归一为 '_'。import/调用/注册表三处同源
			// 用 toolSymbolName,自洽;正编译 tool 绑定映射以注册表符号为准。
			b.WriteByte('_')
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return ""
	}
	return out
}

// knownToolOutputType 已知 dify 社区工具的输出字段类型。dify 导出的
// tool 节点不带 output_schema（workflow/内置插件工具均无）,输出形状无法
// 从 YAML 得知——对语料中已实证的工具字段做白名单（text → string 等）,
// 其余按 any（开放世界,记 docs/DIFY-DECOMPILE.md 覆盖清单）。键为
// toolSymbolName 归一后的工具名（小写 + 清洗）。
var knownToolOutputs = map[string]map[string]string{
	"localscrape":    {"text": "string"},
	"imagesearch":    {"text": "string"},
	"jina_reader":    {"text": "string"},
	"ddgo_translate": {"text": "string"},
	"sql_query":      {"text": "string"},
	"create_order":   {"files": "any", "text": "string"},
	// 2026-08-28 批量语料扩展（dify-workflows 272 语料实测）:text 型
	// 输出 → string;json 型（工具返回 JSON 字符串）→ string;files 型
	// → file（answer 模板 markdown 插值。array 形态见 docs 覆盖清单）。
	"current_time":               {"text": "string"},
	"generate_audio_post":        {"text": "string"},
	"generateimage":              {"text": "string"},
	"getmessage":                 {"text": "string"},
	"lambda_yaml_to_json":        {"text": "string"},
	"listmessages":               {"text": "string"},
	"parse":                      {"text": "string"},
	"perplexity":                 {"text": "string"},
	"scrape":                     {"text": "string"},
	"serper":                     {"text": "string"},
	"sql_execute":                {"text": "string"},
	"tavily_search":              {"text": "string"},
	"translate":                  {"text": "string"},
	"webscraper":                 {"text": "string"},
	"editimage":                  {"text": "string"},
	"getiplocation":              {"text": "string"},
	"ddgo_search":                {"json": "string"},
	"google_search":              {"json": "string"},
	"crawl":                      {"json": "string"},
	"bar_chart":                  {"files": "file"},
	"dalle3":                     {"files": "file"},
	"flux":                       {"files": "file"},
	"markdown_to_pptx_converter": {"files": "file"},
	"podcast_audio_generator":    {"files": "file"},
	"tts":                        {"files": "file"},
	"workflow":                   {"files": "file"},
	"line_chart":                 {"text": "string"},
	"pie_chart":                  {"text": "string"},
}

// knownToolOutputType 查询白名单;未知字段 → any。
func knownToolOutputType(toolName, field string) string {
	if m := knownToolOutputs[toolName]; m != nil {
		if t, ok := m[field]; ok {
			return t
		}
	}
	return "any"
}

// mergeParams 参数表合并（按名去重,保留顺序）。
func mergeParams(a, b []tool.Parameter) []tool.Parameter {
	seen := map[string]bool{}
	for _, p := range a {
		seen[p.Name] = true
	}
	for _, p := range b {
		if !seen[p.Name] {
			seen[p.Name] = true
			a = append(a, p)
		}
	}
	return a
}

// mergeOutputs 输出表合并（按名去重,保留顺序）。
func mergeOutputs(a, b []tool.OutputField) []tool.OutputField {
	seen := map[string]bool{}
	for _, o := range a {
		seen[o.Name] = true
	}
	for _, o := range b {
		if !seen[o.Name] {
			seen[o.Name] = true
			a = append(a, o)
		}
	}
	return a
}
