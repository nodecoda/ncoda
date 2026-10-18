package artifactcanon

// normalize_const_shims_test.go —— 纯常量载体归一的契约锚（2026-10-17 S132menghe）。
//
// 判据（一句可判真假）：**无输入**且 code 以 `# ncoda:shim 1` 开头的 code 节点，
// 其身份 = code 逐字；输出口类型提示与候选 schema 是**附注**（对常量值无信息）。
// 因此同一 `nodes` 序列（含容器内联子图）内同 code 的多个载体必须归一为一个，
// 且归一前后的值/控制流不变。
//
// 撤实现必红：
//   - 删 `normalizeConstShims` 的调用/函数 ⇒ ① 不合并、② 类型提示不清空；
//   - 只扫顶层 `nodes`（首版实现的洞）⇒ ③ 嵌套容器内不合并；
//   - 把「有输入的 code」也算纯常量 ⇒ ④（恒等 shim）被误合并，红。

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// shimMarked 造一个 code 节点条目；inputs 为空 = 纯常量载体。
func shimDoc(t *testing.T, ids []string, codes []string, inputs bool, schema string) (string, *yaml.Node) {
	t.Helper()
	var b strings.Builder
	b.WriteString("schema_version: 1.0.0\nname: w\nnodes:\n")
	for i, id := range ids {
		b.WriteString("  - id: \"" + id + "\"\n    type: code\n    parameters:\n")
		b.WriteString("      code: " + yamlScalar(codes[i]) + "\n")
		if inputs {
			b.WriteString("      node_inputs:\n        - name: arg1\n          input:\n            value:\n              content: \"x\"\n              type: literal\n")
		}
		b.WriteString("      node_outputs:\n        result:\n          type: list\n          items:\n            type: object\n")
	}
	// 候选槽引用第一个载体；schema 是类型附注。
	b.WriteString("  - id: \"100900\"\n    type: variable_merge\n    parameters:\n      mergeGroups:\n        - name: g\n          variables:\n")
	for _, id := range ids {
		b.WriteString("            - type: string\n              schema: " + schema + "\n              value:\n                content:\n                  blockID: \"" + id + "\"\n                  name: result\n                  source: block-output\n                type: ref\n")
	}
	src := b.String()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("夹具不是合法 YAML：%v\n%s", err, src)
	}
	return src, &doc
}

func yamlScalar(s string) string {
	var b strings.Builder
	b.WriteString("|\n")
	for _, line := range strings.Split(s, "\n") {
		b.WriteString("        " + line + "\n")
	}
	return b.String()
}

// pureShims 统计（递归）纯常量载体条目数。
func countPureShims(root *yaml.Node) int {
	n := 0
	var walk func(*yaml.Node)
	walk = func(v *yaml.Node) {
		if v == nil {
			return
		}
		switch v.Kind {
		case yaml.MappingNode:
			if scalarField(v, "type") == "code" {
				params := mapValueNode(v, "parameters")
				if params != nil && strings.HasPrefix(scalarField(params, "code"), shimMarker) {
					in := mapValueNode(params, "node_inputs")
					if in == nil || in.Kind != yaml.SequenceNode || len(in.Content) == 0 {
						n++
					}
				}
			}
			for i := 0; i+1 < len(v.Content); i += 2 {
				walk(v.Content[i+1])
			}
		case yaml.SequenceNode:
			for _, c := range v.Content {
				walk(c)
			}
		}
	}
	walk(root)
	return n
}

func outputTypeHints(root *yaml.Node) int {
	n := 0
	var walk func(*yaml.Node)
	walk = func(v *yaml.Node) {
		if v == nil {
			return
		}
		switch v.Kind {
		case yaml.MappingNode:
			if scalarField(v, "type") == "code" {
				params := mapValueNode(v, "parameters")
				if outs := mapValueNode(params, "node_outputs"); outs != nil && outs.Kind == yaml.MappingNode {
					for i := 0; i+1 < len(outs.Content); i += 2 {
						if mapValueNode(outs.Content[i+1], "type") != nil {
							n++
						}
					}
				}
			}
			for i := 0; i+1 < len(v.Content); i += 2 {
				walk(v.Content[i+1])
			}
		case yaml.SequenceNode:
			for _, c := range v.Content {
				walk(c)
			}
		}
	}
	walk(root)
	return n
}

func candidateSchemaCount(root *yaml.Node) int {
	n := 0
	var walk func(*yaml.Node)
	walk = func(v *yaml.Node) {
		if v == nil {
			return
		}
		switch v.Kind {
		case yaml.MappingNode:
			if v2 := mapValueNode(v, "value"); v2 != nil && v2.Kind == yaml.MappingNode {
				if c := mapValueNode(v2, "content"); c != nil && c.Kind == yaml.MappingNode {
					if b := mapValueNode(c, "blockID"); b != nil {
						if mapValueNode(v, "schema") != nil {
							n++
						}
					}
				}
			}
			for i := 0; i+1 < len(v.Content); i += 2 {
				walk(v.Content[i+1])
			}
		case yaml.SequenceNode:
			for _, c := range v.Content {
				walk(c)
			}
		}
	}
	walk(root)
	return n
}

// ① 同 code 的纯常量载体：合并为一个，引用全部改指保留者。
func TestConstShimMergesSameCode(t *testing.T) {
	code := "# ncoda:shim 1\ndef main() -> dict:\n    return {\"result\": []}"
	_, doc := shimDoc(t, []string{"100011", "100012"}, []string{code, code}, false, "array<object>")
	root := docRootNode(doc)
	if !normalizeConstShims(root) {
		t.Fatal("两个同 code 纯常量载体必须触发归一")
	}
	if got := countPureShims(root); got != 1 {
		t.Fatalf("同 code 常量载体应归并为 1，got %d", got)
	}
	if got := candidateSchemaCount(root); got != 0 {
		t.Fatalf("指向常量载体的候选 schema 是类型附注，必须清空，got %d", got)
	}
	if got := outputTypeHints(root); got != 0 {
		t.Fatalf("常量载体的输出口类型提示无信息，必须清空，got %d", got)
	}
}

// ② 不同 code：不是同一个值 ⇒ 不得合并。
func TestConstShimKeepsDistinctCodes(t *testing.T) {
	_, doc := shimDoc(t,
		[]string{"100011", "100012"},
		[]string{"# ncoda:shim 1\ndef main() -> dict:\n    return {\"result\": []}", "# ncoda:shim 1\ndef main() -> dict:\n    return {\"result\": 0}"},
		false, "array<object>")
	root := docRootNode(doc)
	normalizeConstShims(root)
	if got := countPureShims(root); got != 2 {
		t.Fatalf("不同 code 的常量载体不是同一个值，必须保持 2 个，got %d", got)
	}
}

// ③ 容器内联子图：归一必须递归到每一层 `nodes`（首版实现的洞）。
func TestConstShimMergesInsideNestedNodes(t *testing.T) {
	const codeBlock = `          code: |
            # ncoda:shim 1
            def main() -> dict:
                return {"result": ""}
`
	src := `schema_version: 1.0.0
name: w
nodes:
  - id: "100001"
    type: batch
    nodes:
      - id: "100011"
        type: code
        parameters:
` + codeBlock + `      - id: "100012"
        type: code
        parameters:
` + codeBlock
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("夹具不是合法 YAML：%v", err)
	}
	root := docRootNode(&doc)
	if !normalizeConstShims(root) {
		t.Fatal("嵌套容器内的同 code 常量载体也必须触发归一")
	}
	if got := countPureShims(root); got != 1 {
		t.Fatalf("嵌套容器内应归并为 1，got %d", got)
	}
}

// ④ 有输入的 code（恒等 shim）：携载真实值 ⇒ 不是纯常量，不得合并。
func TestConstShimKeepsIdentityShimsWithInputs(t *testing.T) {
	code := "# ncoda:shim 1\ndef main(arg1) -> dict:\n    return {\"result\": arg1}"
	_, doc := shimDoc(t, []string{"100011", "100012"}, []string{code, code}, true, "array<object>")
	root := docRootNode(doc)
	normalizeConstShims(root)
	if got := countPureShims(root); got != 0 {
		t.Fatalf("有输入的恒等 shim 不是纯常量，got %d", got)
	}
}
