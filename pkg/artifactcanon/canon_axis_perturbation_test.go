package artifactcanon

// canon_axis_perturbation_test.go —— `CANON-FORM §4 自由度轴表` 的**单轴扰动判据**
//（2026-09-27；§6-1 自认「轴的**枚举完备性未验** ⇒ 判据 = 单轴扰动的生成器（未接线）」，
// 本文件把**比较侧**那半接上机器）。
//
// 为什么不能只写「canon 抹掉了这几条轴」：那是断言，不是判据。**逐轴扰动一次、
// 看规范形是否逐字节不动**才是证据 —— 每条扰动的构造理由都是「平台允许这样变、
// 而运行语义看不见它」；若规范形跟着变，就是把表示层当成了欠账（假红），
// 或者（反向）扰动其实改到了内容（那这条用例自己就是坏的）。
//
// 实物 = `testdata/X12_batch_product.yaml`（65KB 真实产物，含 `batch` 容器**内联**的
// `nodes` / `edges` 子序列 ⇒ 递归面也被覆盖），不是合成小夹具。
//
// 覆盖面（轴 → 判据）：
//   E 身份            = 既有 `TestCanonInvariantOnRealProduct`（整份产物换一套编号）
//   G 序列化写法       = 既有 `TestCanonAbsorbsScalarStyle`
//   I 标题去重后缀     = 既有 `TestCanonAbsorbsTitleDedupeSuffixAssignment`
//   **D 序 / 键序 / 顶层工作流身份 / 画布坐标 position = 本文件**（§4 里此前只有散文的四条）
//
// **不在本文件**的轴，其判据不在比较侧（这里写清理由，避免「漏了」与「不该有」被混为一谈）：
//   A 值形态 / B 承载落点 / C 命名 / F 类型身份 / H 模板串名额 —
//   它们是**生产侧**的规范位（lowering / raising 必须钉死一种形状），判据 = 各自的
//   契约测试；把两种形状在比较侧「掩掉」等于宣布它们是同一个类 ——
//   那正是 `J-join-canonical-form.md` §41.5 说的「把整表当无序 map = 抹掉语义」。

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// axisParse 返回**文档节点**（供编码）/ 由 `docRootNode` 取出的**根 mapping**（供扰动）。
// 注意：`yaml.Unmarshal` 到 `yaml.Node` 得到的是 DocumentNode（根 mapping 在 Content[0]）——
// 直接在 DocumentNode 上找键会一条都找不到（第一版就是全用例「扰动没生效」）。
func axisRoot(t *testing.T, document *yaml.Node) *yaml.Node {
	t.Helper()
	root := docRootNode(document)
	if root == nil {
		t.Fatal("产物没有根 mapping")
	}
	return root
}

// axisPerturb：单轴扰动的**唯一载体**（输入 = 产物原文，输出 = 扰动后的产物原文）。
type axisPerturb struct {
	axis  string
	why   string // 为什么这条轴语义不可观察（档位依据）
	apply func(n *yaml.Node)
}

func axisDoc(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/X12_batch_product.yaml")
	if err != nil {
		t.Fatalf("读不到实物：%v", err)
	}
	return string(raw)
}

func axisParse(t *testing.T, doc string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &n); err != nil {
		t.Fatalf("产物不可解析：%v", err)
	}
	return &n
}

func axisMarshal(t *testing.T, n *yaml.Node) string {
	t.Helper()
	b, err := yaml.Marshal(n)
	if err != nil {
		t.Fatalf("扰动后不可编码：%v", err)
	}
	return string(b)
}

// ── 四条单轴扰动（各自只动一个坐标） ─────────────────────────────────────────

// reverseNodesEdges：把 `nodes` / `edges` 序列**元素顺序整体反转**（含容器内联的那两份）。
// 为什么不可观察：序列顺序是序列化细节，图的语义由 `source_node`/`target_node` 决定
// （P3：边集合是 IR 结构树的确定性投影 —— 一张图不因为你怎么数它的边而变）。
func reverseNodesEdges(root *yaml.Node) {
	target := map[string]bool{"nodes": true, "edges": true}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				if v.Kind == yaml.SequenceNode && target[k.Value] && len(v.Content) > 1 {
					rev := make([]*yaml.Node, 0, len(v.Content))
					for j := len(v.Content) - 1; j >= 0; j-- {
						rev = append(rev, v.Content[j])
					}
					v.Content = rev
				}
				walk(v)
			}
		case yaml.SequenceNode:
			for _, c := range n.Content {
				walk(c)
			}
		}
	}
	walk(root)
}

// reverseMapKeys：把每个 mapping 的**键值对顺序整体反转**（深度递归）。
// 为什么不可观察：mapping 是无序集合，键序不是内容（判据口径逐字含「键序」）。
func reverseMapKeys(n *yaml.Node) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.MappingNode:
		kv := append([]*yaml.Node(nil), n.Content...)
		out := make([]*yaml.Node, 0, len(kv))
		for i := len(kv) - 2; i >= 0; i -= 2 {
			out = append(out, kv[i], kv[i+1])
		}
		n.Content = out
		for i := 1; i < len(out); i += 2 {
			reverseMapKeys(out[i])
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			reverseMapKeys(c)
		}
	}
}

// changeTopLevelID：改根 mapping 的 `id`（工作流身份）。
// 为什么不可观察：顶层 id 由内容派生（雪花号），不是契约 —— canon 明确不参与比较。
func changeTopLevelID(root *yaml.Node) {
	if root.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "id" {
			continue
		}
		v := root.Content[i+1]
		c := *v
		c.Value = "1" + c.Value // 同标签、不同值（保持 !!int / !!str 不变）
		root.Content[i+1] = &c
		return
	}
}

// shiftPositions：把所有**节点条目**里的 `position.x` / `position.y` 数值改掉。
// 为什么不可观察：position 是前端画布元数据（coze-studio OSS canvas.go 原话：
// 「Meta holds meta data used by frontend」），执行域无消费者；我方 lowering 也只是
// 按发射序给布局行（`Position = &Position{X: row*340}`）⇒ 它是文档顺序的函数。
func shiftPositions(root *yaml.Node) {
	var walk func(n *yaml.Node, inNodes bool)
	walk = func(n *yaml.Node, inNodes bool) {
		if n == nil {
			return
		}
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				childInNodes := inNodes || k.Value == "nodes"
				if inNodes && k.Value == "position" && v.Kind == yaml.MappingNode {
					for j := 0; j+1 < len(v.Content); j += 2 {
						c := *v.Content[j+1]
						switch c.Tag {
						case "!!int":
							c.Value = "987654"
						case "!!float":
							c.Value = "987654.25"
						default:
							continue
						}
						v.Content[j+1] = &c
					}
				}
				walk(v, childInNodes)
			}
		case yaml.SequenceNode:
			for _, c := range n.Content {
				walk(c, inNodes)
			}
		}
	}
	walk(root, false)
}

// ── 负锚（语义可见 ⇒ 必须判不等） ────────────────────────────────────────────

// dropLastEdge：删掉根 `edges` 的最后一条边（改的是图本身）。
func dropLastEdge(root *yaml.Node) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "edges" {
			continue
		}
		seq := root.Content[i+1]
		if seq.Kind == yaml.SequenceNode && len(seq.Content) > 0 {
			seq.Content = seq.Content[:len(seq.Content)-1]
		}
		return
	}
}

// flipFirstNodeType：把第一个节点条目的 `type` 改掉（改的是节点语义）。
func flipFirstNodeType(root *yaml.Node) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "nodes" {
			continue
		}
		seq := root.Content[i+1]
		if seq.Kind != yaml.SequenceNode || len(seq.Content) == 0 {
			return
		}
		entry := seq.Content[0]
		for j := 0; j+1 < len(entry.Content); j += 2 {
			if entry.Content[j].Value == "type" {
				c := *entry.Content[j+1]
				c.Value = "code"
				entry.Content[j+1] = &c
				return
			}
		}
		return
	}
}

// ── 判据 ────────────────────────────────────────────────────────────────────

func TestCanonAxisPerturbationRealProduct(t *testing.T) {
	doc := axisDoc(t)
	base := canonMust(t, doc)
	if base == "" {
		t.Fatal("基线规范形为空 ⇒ 下面的相等断言没有意义")
	}
	// 前提自检：实物 X12 **必须**含真孪生（两个对称 `code` 节点）—— 否则
	// 「自同构孪生定名」那条路没被行使，本判据对 min-over-branches 就是空锚
	// （一手 2026-09-27：该产物并列对 1、枚举叶子 3）。
	probe, err := CanonicalResult(doc)
	if err != nil {
		t.Fatal(err)
	}
	if probe.RawTieBreaks == 0 {
		t.Fatal("实物里没有真孪生 ⇒ 孪生定名路径未被行使（夹具已漂移，本判据退化成空锚）")
	}
	if probe.TieLeaves < 2 {
		t.Fatalf("真孪生存在但只求值了 %d 个分支 ⇒ min-over-branches 没生效", probe.TieLeaves)
	}

	same := []axisPerturb{
		{"D 序（nodes/edges 序列顺序）", "序列顺序是序列化细节，图由 source/target 决定（P3）", reverseNodesEdges},
		{"键序（mapping 键值对顺序）", "mapping 是无序集合，键序不是内容", reverseMapKeys},
		{"顶层工作流身份 id", "顶层 id 由内容派生，不是契约", changeTopLevelID},
		{"画布坐标 position", "前端元数据，执行域无消费者；我方按发射序生成", shiftPositions},
	}
	for _, c := range same {
		c := c
		t.Run("same/"+c.axis, func(t *testing.T) {
			n := axisParse(t, doc)
			c.apply(axisRoot(t, n))
			got := axisMarshal(t, n)
			if got == doc {
				t.Fatalf("扰动没生效（用例自己坏了）：%s", c.axis)
			}
			if canonMust(t, got) != base {
				t.Fatalf("单轴扰动改了规范形 ⇒ 表示层被当成欠账（假红）\n轴 = %s\n理由 = %s\n%s",
					c.axis, c.why, firstLineDiff(base, canonMust(t, got)))
			}
		})
	}

	diff := []axisPerturb{
		{"删掉一条边", "改的是图本身（P1/P3）", dropLastEdge},
		{"改掉节点 type", "改的是节点语义", flipFirstNodeType},
	}
	for _, c := range diff {
		c := c
		t.Run("diff/"+c.axis, func(t *testing.T) {
			n := axisParse(t, doc)
			c.apply(axisRoot(t, n))
			got := axisMarshal(t, n)
			if got == doc {
				t.Fatalf("扰动没生效（用例自己坏了）：%s", c.axis)
			}
			if canonMust(t, got) == base {
				t.Fatalf("语义可见的变化必须判不等（否则绿侧可被「一律返回空」满足）\n轴 = %s\n理由 = %s",
					c.axis, c.why)
			}
		})
	}

	// 四轴叠加（合流性：消元顺序无关 —— CANON-FORM §1.4 C3）：四条一起扰动仍必须逐字节相同。
	t.Run("same/四轴叠加", func(t *testing.T) {
		n := axisParse(t, doc)
		root := axisRoot(t, n)
		for _, c := range same {
			c.apply(root)
		}
		if canonMust(t, axisMarshal(t, n)) != base {
			t.Fatal("四条轴同时扰动后规范形变了 ⇒ 消元不满足顺序无关（C3）")
		}
	})
}
