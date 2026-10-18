package decompile

// graph.go —— dify YAML → 轻量图（Phase A 解析层）。
//
// 只提取反编译需要的字段（id/type/data/边端口），跳过布局（position/
// viewport/features）——dify 工作流 YAML 的唯一真相是 graph 结构与节点
// data。参照 ncoda-dify parser.py 的节点解析语义（字段名/缺省值对齐）。

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// nodeInfo 一个 dify 图节点（反编译视图）。
type nodeInfo struct {
	ID    string
	Type  string // data.type（start/llm/if-else/code/...）
	Title string
	Data  map[string]any // data 原文（按类型提取）
}

// edgeInfo 一条图边（端口语义：sourceHandle 决定分支/顺序）。
type edgeInfo struct {
	Source       string
	SourceHandle string
	Target       string
	TargetHandle string
}

// convVarDecl 一个 dify 会话变量声明（conversation_variables 块）。
type convVarDecl struct {
	Type  string // dify value_type（number/string/boolean/file/array[...]）
	Value any    // dify 初始值（number → int/float,array → []any）
}

// envVarDecl 一个 dify 环境变量声明（environment_variables,工作流只读配置）。
type envVarDecl struct {
	Type  string // dify value_type（string/number/boolean/secret）
	Value any    // 值
}

// wfGraph 反编译视图的图。
type wfGraph struct {
	nodes    map[string]*nodeInfo
	edges    []edgeInfo
	start    *nodeInfo              // data.type == "start"（dify 工作流有且仅有一个）
	order    []string               // 拓扑序（从 start 出发的可达节点,确定性排序）
	mode     string                 // app.mode（workflow/advanced-chat）
	convVars map[string]convVarDecl // workflow.conversation_variables 声明（name → 类型/初值）
	envVars  map[string]envVarDecl  // workflow.environment_variables 声明（name → 类型/值,只读 const）
}

// parseYAML 解析 dify 工作流 YAML → wfGraph。
func parseYAML(text string) (*wfGraph, error) {
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(text), &raw); err != nil {
		return nil, fmt.Errorf("dify decompile: yaml: %w", err)
	}
	wf, _ := raw["workflow"].(map[string]any)
	if wf == nil {
		return nil, fmt.Errorf("dify decompile: missing workflow key")
	}
	g, _ := wf["graph"].(map[string]any)
	if g == nil {
		return nil, fmt.Errorf("dify decompile: missing workflow.graph")
	}
	gr := &wfGraph{nodes: map[string]*nodeInfo{}, convVars: map[string]convVarDecl{}, envVars: map[string]envVarDecl{}}
	if app, ok := raw["app"].(map[string]any); ok {
		gr.mode = asStr(app["mode"])
	}
	for _, rv := range asSlice(wf["conversation_variables"]) {
		vm := asMap(rv)
		name := asStr(vm["name"])
		if name == "" {
			continue
		}
		gr.convVars[name] = convVarDecl{Type: asStr(vm["value_type"]), Value: vm["value"]}
	}
	for _, rv := range asSlice(wf["environment_variables"]) {
		vm := asMap(rv)
		name := asStr(vm["name"])
		if name == "" {
			continue
		}
		gr.envVars[name] = envVarDecl{Type: asStr(vm["value_type"]), Value: vm["value"]}
	}
	for i, rn := range asSlice(g["nodes"]) {
		m := asMap(rn)
		id := asStr(m["id"])
		if id == "" {
			return nil, fmt.Errorf("dify decompile: node %d missing id", i)
		}
		if _, dup := gr.nodes[id]; dup {
			return nil, fmt.Errorf("dify decompile: duplicate node id %q", id)
		}
		data := asMap(m["data"])
		typ := asStr(data["type"])
		if typ == "" {
			// dify 导出中 note 节点（画布注释）type 为空串,特征 = author/text/
			// theme（无执行语义、无出边）。归一化为 "note",转换层跳过。
			if _, ok := data["author"]; ok {
				if _, ok2 := data["text"]; ok2 {
					typ = "note"
				}
			}
			if typ == "" {
				return nil, fmt.Errorf("dify decompile: node %q missing data.type", id)
			}
		}
		ni := &nodeInfo{ID: id, Type: typ, Title: asStr(data["title"]), Data: data}
		gr.nodes[id] = ni
		if typ == "start" {
			if gr.start != nil {
				return nil, fmt.Errorf("dify decompile: multiple start nodes (%q, %q)", gr.start.ID, id)
			}
			gr.start = ni
		}
	}
	for i, re := range asSlice(g["edges"]) {
		m := asMap(re)
		e := edgeInfo{
			Source:       asStr(m["source"]),
			SourceHandle: asStr(m["sourceHandle"]),
			Target:       asStr(m["target"]),
			TargetHandle: asStr(m["targetHandle"]),
		}
		// dify 线性边可省略 sourceHandle（默认 "source" 端口）;空 handle
		// 归一化为 "source",使 succ() 的端口匹配稳定。
		if e.SourceHandle == "" {
			e.SourceHandle = "source"
		}
		if e.Source == "" || e.Target == "" {
			return nil, fmt.Errorf("dify decompile: edge %d missing endpoint", i)
		}
		if _, ok := gr.nodes[e.Source]; !ok {
			return nil, fmt.Errorf("dify decompile: edge %d source %q not found", i, e.Source)
		}
		if _, ok := gr.nodes[e.Target]; !ok {
			return nil, fmt.Errorf("dify decompile: edge %d target %q not found", i, e.Target)
		}
		gr.edges = append(gr.edges, e)
	}
	if gr.start == nil {
		return nil, fmt.Errorf("dify decompile: no start node")
	}
	gr.toposort()
	return gr, nil
}

// toposort 从 start 出发 BFS 拓扑排序（确定性：按边 id 原序扩展,可达
// 集合稳定）。不可达节点（dead node）不参与生成——dify 允许画布上有
// 未连线节点。
func (g *wfGraph) toposort() {
	adj := map[string][]string{}
	for _, e := range g.edges {
		adj[e.Source] = append(adj[e.Source], e.Target)
	}
	seen := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		g.order = append(g.order, id)
		for _, t := range adj[id] {
			walk(t)
		}
	}
	walk(g.start.ID)
}

// succ 返回节点在指定 sourceHandle 上的后继（按边出现序;同端口多后继
// = 并行 fan-out,由调用方处理）。
func (g *wfGraph) succ(id, handle string) []string {
	var out []string
	for _, e := range g.edges {
		if e.Source == id && e.SourceHandle == handle {
			out = append(out, e.Target)
		}
	}
	return out
}

// asMap/asSlice/asStr 安全类型断言（dify YAML 由 yaml.v3 解码为
// map[string]any / []any / string）。
func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	if s == nil {
		return nil
	}
	return s
}

func asStr(v any) string {
	s, _ := v.(string)
	return s
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func asInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		if iv, err := strconv.Atoi(x); err == nil {
			return iv
		}
	}
	return 0
}

func asFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	}
	return 0
}

// sanitizeID 把 dify 节点 id 变成合法 ncoda 标识符片段:
// 保留 [a-zA-Z0-9_],其余 → '_'（"llm-1" → "llm_1"）。
func sanitizeID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// varName 节点 id → ncoda 变量名（v + sanitize,防数字开头）。
func varName(id string) string {
	return "v" + sanitizeID(id)
}

// sortedKeys 确定性遍历 map 键。
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
