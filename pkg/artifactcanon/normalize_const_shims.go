package artifactcanon

// normalize_const_shims.go —— 规范形前的**纯常量载体归一**。
//
// 事实（一手，2026-10-17 S68 批次 r2 漂移族）：平台导出与我们的编译器都会
// 为「提升变量的类型零值」物化若干 `# ncoda:shim 1` 的**无输入** code 节点
// （`return {"result": []}` / `""` / `0` …）。它们的值只由 code 决定；输出口
// 类型提示只是给空容器标注元素类型的附注。
//
// 但两侧的**物化序**不同：平台导出按作者声明序，我们按 merge 组序（组名排序）
// ⇒ 同一份语义两轮拿到不同的 blockID，且类型提示在候选槽之间互换，canon 文本
// 因 `blockID: n16` vs `n14` 与候选 `schema` 互换而不同（ARTIFACT_DRIFT/PARAM）。
//
// 规则（一条，与 IR 自己的 `zeroShimKey` / P28 池身份同尺）：
//
//	在**每一个** `nodes` 序列（顶层 + loop/batch 的内联子图，递归）内，
//	**无输入**且 code 以 `# ncoda:shim 1` 开头的 code 节点，按 **code 逐字**
//	归为一类；每类只保留一个节点（文档序首个），其余节点删除，所有引用改指
//	保留者；保留者的 `node_outputs` 各口类型提示清空；任何「候选/输入映射」
//	若其 `value.content.blockID` 指向该常量，删除其 `schema`（类型提示对常量
//	值无信息）。
//
// 为什么是等价变换：这些节点无输入、编译器自建、纯常量 ⇒ 两条同 code 的节点
// 取值逐字相同；互换身份、清空输出类型附注、合并同类节点都不改变任何值或
// 控制流（它们没有入边/出边，只被候选槽按 blockID 引用）。与 P28「同键只物化
// 一个节点」同一把尺，只是这里发生在**比较面**（产物已成为 YAML）。
//
// 为什么必须递归：容器（loop / batch / subflow）的内联子图是**独立**的
// `nodes` 序列，零值载体就物化在里面；只扫顶层会漏（一手：S132menghe 的
// 空串载体 100015/100021 都在 loop 100012 的子图里）。

import (
	"strings"

	"gopkg.in/yaml.v3"
)

const shimMarker = "# ncoda:shim 1"

// normalizeConstShims 就地归一 root 下所有 `nodes` 序列里的常量 shim。返回是否改动。
func normalizeConstShims(root *yaml.Node) bool {
	if root == nil || root.Kind != yaml.MappingNode {
		return false
	}
	remap := map[string]string{} // 被删 id -> 保留 id
	kept := map[string]bool{}    // 保留 id（含未被合并的常量载体）
	changed := false

	// 第一遍：逐层 `nodes` 序列归并同类常量；同时收集全局引用改写表。
	var merge func(n *yaml.Node)
	merge = func(n *yaml.Node) {
		if n == nil {
			return
		}
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				if k.Value == "nodes" && v.Kind == yaml.SequenceNode {
					if mergeConstShimsInSeq(v, remap, kept) {
						changed = true
					}
				}
				merge(v)
			}
		case yaml.SequenceNode:
			for _, c := range n.Content {
				merge(c)
			}
		}
	}
	merge(root)
	if !changed {
		return false
	}

	// 第二遍：引用改写 + 候选 schema 清除。
	var rewrite func(n *yaml.Node)
	rewrite = func(n *yaml.Node) {
		if n == nil {
			return
		}
		switch n.Kind {
		case yaml.MappingNode:
			// 该 mapping 是否是指向常量 shim 的候选/输入（value.content.blockID）。
			if hk := mapValueNode(n, "value"); hk != nil && hk.Kind == yaml.MappingNode {
				if c := mapValueNode(hk, "content"); c != nil && c.Kind == yaml.MappingNode {
					if b := mapValueNode(c, "blockID"); b != nil {
						if kept[b.Value] || remap[b.Value] != "" {
							removeMappingKey(n, "schema")
						}
					}
				}
			}
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				switch k.Value {
				case "blockID", "ref_node", "source_node", "target_node":
					if to, ok := remap[v.Value]; ok {
						v.Value = to
					}
				}
				rewrite(v)
			}
		case yaml.SequenceNode:
			for _, c := range n.Content {
				rewrite(c)
			}
		}
	}
	rewrite(root)
	return true
}

// mergeConstShimsInSeq 在**一个** `nodes` 序列内归并同类纯常量 code 节点。
// 返回是否删除了节点。删除/保留的 id 记入 remap / kept（全局引用改写用）。
func mergeConstShimsInSeq(seq *yaml.Node, remap map[string]string, kept map[string]bool) bool {
	entries := make([]*yaml.Node, 0, len(seq.Content))
	collectNodeMappings(seq, &entries)
	if len(entries) == 0 {
		return false
	}

	constKey := func(e *yaml.Node) (string, bool) {
		if e == nil || scalarField(e, "type") != "code" {
			return "", false
		}
		params := mapValueNode(e, "parameters")
		if params == nil {
			return "", false
		}
		code := scalarField(params, "code")
		if !strings.HasPrefix(code, shimMarker) {
			return "", false
		}
		if in := mapValueNode(params, "node_inputs"); in != nil && in.Kind == yaml.SequenceNode && len(in.Content) > 0 {
			return "", false // 有输入 = 携载真实值，不是纯常量
		}
		return code, true
	}

	keeper := map[string]string{} // code -> 保留节点 id
	dropIDs := map[string]bool{}
	seqRemap := map[string]string{}
	found := 0
	for _, e := range entries {
		key, ok := constKey(e)
		if !ok {
			continue
		}
		id := scalarField(e, "id")
		if id == "" {
			continue
		}
		found++
		if keep, seen := keeper[key]; seen {
			seqRemap[id] = keep
			dropIDs[id] = true
			continue
		}
		keeper[key] = id
		kept[id] = true
	}
	if found == 0 {
		return false
	}
	for from, to := range seqRemap {
		remap[from] = to
	}

	// 保留者：清空各输出口的类型提示（值由 code 决定）。
	for _, e := range entries {
		id := scalarField(e, "id")
		if !kept[id] {
			continue
		}
		params := mapValueNode(e, "parameters")
		if params == nil {
			continue
		}
		if outs := mapValueNode(params, "node_outputs"); outs != nil && outs.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(outs.Content); i += 2 {
				outs.Content[i+1] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			}
		}
	}

	filterNodesSeq(seq, dropIDs)
	return true // found > 0：即使无重复节点，类型提示清空也是改动
}

// filterNodesSeq 从 `nodes` 序列里移除被合并的常量 shim 条目。
func filterNodesSeq(seq *yaml.Node, drop map[string]bool) {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return
	}
	out := seq.Content[:0]
	for _, n := range seq.Content {
		if n.Kind == yaml.MappingNode {
			if id := scalarField(n, "id"); id != "" && drop[id] {
				continue
			}
		}
		out = append(out, n)
	}
	seq.Content = out
}

// removeMappingKey 删除 mapping 里的一个键（不存在 = 无操作）。
func removeMappingKey(n *yaml.Node, key string) {
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	out := make([]*yaml.Node, 0, len(n.Content))
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			continue
		}
		out = append(out, n.Content[i], n.Content[i+1])
	}
	n.Content = out
}
