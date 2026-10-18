package artifactcanon

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// 本文件 = 规范形的**不变量判据**（R-21 第二半，2026-09-23）。
//
// 命题：`Canonical` 必须是「图同构类」的函数 —— **同一份产物把节点 id 全部换一套
// 编号（结构、内容、引用关系逐字不变），规范形必须逐字节相同**。
//
// 为什么这是判据而不是「实现细节」：产物 id 是**构造期分配序的像**（不是契约），
// 而两轮编译的编号必然不同。规范形只要残留对原始 id 的依赖，判据就会把「换编号」
// 报成漂移 —— 假红。一手代价（2026-09-23 实测）：examples 面 X12 / X4 两条红，
// 根因 = 签名里的序列**按原始 id 排序后才掩码**（顺序被 id 决定，掩码抹不掉顺序）。
//
// 两条锁必须同时存在（缺一侧就会出现「一律返回空串」也能过的退化）：
//	① 换编号 ⇒ 必须相同（本文件 TestCanonInvariantUnderIDRelabel / TestStructKeyInvariant）；
//	② 真内容变了 ⇒ 必须不同（TestCanonStillCatchesContentChange）。

// invariantDoc：最小复现形状 —— **容器节点自带 `edges` / `nodes` 子序列**
// （X12 的 `batch` 就是这个形状）。容器把整棵子树的边**内联**在自己的映射里，
// 于是「序列怎么排序」直接进了它的结构签名。
const invariantDoc = `schema_version: 1.0.0
name: "w"
nodes:
  - id: "100001"
    type: start
  - id: "100002"
    type: batch
    parameters:
      batchSize: 100
    edges:
      - source_node: "100003"
        source_port: "false"
        target_node: "100006"
      - source_node: "100003"
        source_port: "true"
        target_node: "100007"
      - source_node: "100005"
        target_node: "100010"
      - source_node: "100006"
        target_node: "100004"
      - source_node: "100007"
        target_node: "100009"
      - source_node: "100008"
        target_node: "100011"
    nodes:
      - id: "100003"
        type: condition
        parameters:
          condition: "x == 1"
      - id: "100004"
        type: code
        parameters:
          code: "def main(): return 1"
      - id: "100005"
        type: code
        parameters:
          code: "def main(): return 2"
      - id: "100006"
        type: variable_merge
        mergeGroups:
          - name: G1
            variables:
              - type: string
                value:
                  content:
                    blockID: "100005"
                    name: result
                    source: block-output
                  type: ref
      - id: "100007"
        type: variable_merge
        mergeGroups:
          - name: G1
            variables:
              - type: string
                value:
                  content:
                    blockID: "100009"
                    name: result
                    source: block-output
                  type: ref
      - id: "100008"
        type: code
        parameters:
          code: "def main(): return 3"
      - id: "100009"
        type: code
        parameters:
          code: "def main(): return 4"
      - id: "100010"
        type: text
        parameters:
          text: "{{block_output_100004.G1}}"
      - id: "100011"
        type: text
        parameters:
          text: "{{block_output_100009.G1}}"
  - id: "100012"
    type: end
    node_inputs:
      - path: G1
        ref_node: "100006"
edges:
  - source_node: "100001"
    target_node: "100002"
  - source_node: "100002"
    source_port: "batch-function-inline-output"
    target_node: "100004"
  - source_node: "100002"
    source_port: "batch-function-inline-output"
    target_node: "100009"
  - source_node: "100006"
    target_node: "100012"
`

// relabelDoc：把所有 4 位以上数字串（= 产物 id 的形态）按 perm 换成另一套编号。
// perm 必须**是双射**；未登记的编号原样保留（用例全部登记，免得留下悬空引用）。
func relabelDoc(t *testing.T, doc string, perm map[string]string) string {
	t.Helper()
	re := regexp.MustCompile(`\b\d{4,}\b`)
	seen := map[string]bool{}
	out := re.ReplaceAllStringFunc(doc, func(s string) string {
		if to, ok := perm[s]; ok {
			seen[s] = true
			return to
		}
		return s
	})
	// 模板占位符 `{{block_output_<id>.<port>}}`：`_` 是词字符 ⇒ `\b` 咬不住，
	// 单独扫一遍（漏了它就会留下**悬空引用**，测的就不是"换编号"而是"改坏了"）。
	out = regexp.MustCompile(`block_output_(\d{4,})`).ReplaceAllStringFunc(out, func(s string) string {
		id := s[len("block_output_"):]
		if to, ok := perm[id]; ok {
			seen[id] = true
			return "block_output_" + to
		}
		return s
	})
	if len(seen) != len(perm) {
		t.Fatalf("重编号没覆盖全部 id：登记 %d，命中 %d", len(perm), len(seen))
	}
	return out
}

// reversedPerm：把 id 集合按字典序反转映射 —— 专挑「排序顺序必然翻转」的那套编号
// （这正是旧实现会翻车的地方：它按原始 id 排序）。
func reversedPerm(t *testing.T, doc string) map[string]string {
	t.Helper()
	// id 集合**只能从解析后的节点表取**：产物里还有 4 位以上的**内容**数字
	// （实测 X12：`value: "7484140117972631586"`），把它们一起换掉就不是「换编号」，
	// 而是改内容 —— 那条用例的断言会因此失真（反向锁反而被破坏）。
	_, nodeIDs, err := indexDoc(doc)
	if err != nil {
		t.Fatalf("产物不可解析：%v", err)
	}
	set := map[string]bool{}
	for _, id := range nodeIDs {
		set[id] = true
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	perm := map[string]string{}
	for i, id := range ids {
		perm[id] = fmt.Sprintf("2%05d", len(ids)-1-i)
	}
	return perm
}

// ① 同一份产物换一套编号 ⇒ 规范形逐字节相同。
func TestCanonInvariantUnderIDRelabel(t *testing.T) {
	perm := reversedPerm(t, invariantDoc)
	other := relabelDoc(t, invariantDoc, perm)
	a, err := Canonical(invariantDoc)
	if err != nil {
		t.Fatalf("规范形不可判定（不该）：%v", err)
	}
	b, err := Canonical(other)
	if err != nil {
		t.Fatalf("规范形不可判定（不该）：%v", err)
	}
	if a != b {
		t.Fatalf("id 重编号不是结构差异：规范形必须逐字节相同（实测 2026-09-23 X12/X4 就是这么假红的）\n%s",
			firstLineDiff(a, b))
	}
}

// ② 结构签名（结构色的输入）也必须换编号不变 —— 这是 ① 的因，钉在最近的一层：
// 容器节点的签名里嵌着它自己的 `edges` 序列，若那份序列按原始 id 排序，签名就随编号漂。
func TestStructKeyInvariantUnderIDRelabel(t *testing.T) {
	perm := reversedPerm(t, invariantDoc)
	other := relabelDoc(t, invariantDoc, perm)
	base, err := nodeSigs(invariantDoc)
	if err != nil {
		t.Fatal(err)
	}
	moved, err := nodeSigs(other)
	if err != nil {
		t.Fatal(err)
	}
	for id, sig := range base {
		to, ok := perm[id]
		if !ok {
			t.Fatalf("id %s 未登记重编号", id)
		}
		if moved[to] != sig {
			t.Fatalf("节点 %s（→%s）的结构签名随编号变了：容器内联的序列被原始 id 定了序", id, to)
		}
	}
}

// 反向锁：真内容差异**必须仍然**判不等（否则「一律返回空串」也能过 ①②）。
func TestCanonStillCatchesContentChange(t *testing.T) {
	a, err := Canonical(invariantDoc)
	if err != nil {
		t.Fatal(err)
	}
	changed := regexp.MustCompile(`return 2`).ReplaceAllString(invariantDoc, "return 22")
	b, err := Canonical(changed)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("code 正文变了必须判不等")
	}
	// 拓扑也同理：删掉一条容器内边。
	cut := regexp.MustCompile(`(?m)^      - source_node: "100008"\n        target_node: "100011"\n`).
		ReplaceAllString(invariantDoc, "")
	if cut == invariantDoc {
		t.Fatal("用例失效：没有匹配到要删掉的内边")
	}
	c, err := Canonical(cut)
	if err != nil {
		t.Fatal(err)
	}
	if a == c {
		t.Fatal("容器内边少了必须判不等")
	}
}

// indexDoc：产物文本 → (id → 节点映射, 文档序 id 表)。与 CanonicalResult 的取法逐字一致
// （走 nodes 序列递归收集），保证测的是**同一条**「什么是节点」的判据。
func indexDoc(doc string) (map[string]*yaml.Node, []string, error) {
	var d yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &d); err != nil {
		return nil, nil, err
	}
	root := docRootNode(&d)
	var all []*yaml.Node
	collectNodeMappings(mapValueNode(root, "nodes"), &all)
	byID := map[string]*yaml.Node{}
	var order []string
	for _, n := range all {
		id := scalarField(n, "id")
		if id == "" {
			continue
		}
		byID[id] = n
		order = append(order, id)
	}
	return byID, order, nil
}

// nodeSigs：每个节点 id → 结构签名（与定名无关）。
func nodeSigs(doc string) (map[string]string, error) {
	byID, order, err := indexDoc(doc)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, id := range order {
		out[id] = structKey(id, byID)
	}
	return out, nil
}

func firstLineDiff(a, b string) string {
	la := regexp.MustCompile("\n").Split(a, -1)
	lb := regexp.MustCompile("\n").Split(b, -1)
	for i := 0; i < len(la) && i < len(lb); i++ {
		if la[i] != lb[i] {
			return fmt.Sprintf("L%d  A=%q\nL%d  B=%q", i+1, la[i], i+1, lb[i])
		}
	}
	return fmt.Sprintf("行数不同：%d vs %d", len(la), len(lb))
}

// TestCanonInvariantOnRealProduct：**实物**锁（不是合成 fixture）。
//
// testdata/X12_batch_product.yaml = `examples/coze-reference/X12_Tdouyin4_table_1`
// 一轮编译产物的原文（65KB，含 batch 容器内联的 edges / nodes 子序列）。它正是
// 2026-09-23 报「ARTIFACT_DRIFT/PARAM@r2」的那份产物 —— 实测：旧实现（序列按原始
// id 定序）把它换一套编号后，规范形 34/45 个规范名换位（100030 由 n10 变 n13，
// 与报出的首条差异逐字同症状）；现在必须逐字节不变。
func TestCanonInvariantOnRealProduct(t *testing.T) {
	const path = "testdata/X12_batch_product.yaml"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不到实物：%v", err)
	}
	doc := string(raw)
	perm := reversedPerm(t, doc)
	other := relabelDoc(t, doc, perm)
	a, err := Canonical(doc)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Canonical(other)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("实测产物换编号后规范形变了（R-21 假红的原始形态）：\n%s", firstLineDiff(a, b))
	}
	// 反向锁：这份实物**内容**一改，规范形必须跟着变（否则上面那条只会锁住"恒等"）。
	changed := strings.Replace(doc, "title: 代码_7", "title: 代码_7X", 1)
	if changed == doc {
		t.Fatalf("%s 里没有预期的模板文本，反向锁失效", path)
	}
	c, err := Canonical(changed)
	if err != nil {
		t.Fatal(err)
	}
	if c == a {
		t.Fatal("改掉模板文本必须判不等")
	}
}

// ③ **标量的书写风格是表示，不是内容**（判据口径逐字：抹 id / 数组顺序 / 键序 / **格式**）。
//
// 一手缺陷（2026-09-24，R-39；语料 `7531673552438263834_X182_P_zhishi_card_599_1`）：
// `c1.yaml` 的 `content: |4-` + `{{text}}` 与 `c2.yaml` 的 `content: '{{text}}'`
// **解码后逐字相同**（都 = `{{text}}`，`yaml.safe_load` 实测），旧规范形却报
// `ARTIFACT_OSCILLATION@r3` —— 红的不是实现，是**判据自己**（假红会把真信号一起埋掉）。
//
// 三条同值异写必须逐字节相同：plain / 单引号 / 双引号；块标量 vs 转义双引号（多行值）；
// 块标量 vs 单引号（真实一手形状）。反向锁：**值真变**与**标签变**（`'123'` vs `123`）
// 必须仍然判不等 —— 只锁绿侧会被「一律返回空串」的错实现满足。
func TestCanonAbsorbsScalarStyle(t *testing.T) {
	doc := func(v string) string {
		return "nodes:\n  - id: \"100001\"\n    type: text\n    parameters:\n      content:\n" +
			"        type: literal\n        value: " + v + "\nedges: []\n"
	}
	same := map[string][]string{
		"简单值":  {"abc", "'abc'", `"abc"`},
		"多行值":  {"|-\n          a\n          b", `"a\nb"`},
		"一手形状": {"'{{text}}'", "|-\n          {{text}}"},
	}
	for name, variants := range same {
		base, err := Canonical(doc(variants[0]))
		if err != nil {
			t.Fatalf("%s：规范形不可判定（不该）：%v", name, err)
		}
		for _, v := range variants[1:] {
			got, err := Canonical(doc(v))
			if err != nil {
				t.Fatalf("%s：规范形不可判定（不该）：%v", name, err)
			}
			if got != base {
				t.Fatalf("%s：同一解码值换一种书写风格 ⇒ 规范形必须相同（格式不是内容）\n写法 A: %q\n写法 B: %q\n%s",
					name, variants[0], v, firstLineDiff(base, got))
			}
		}
	}
	// 反向锁①：值真变 ⇒ 必须不等。
	if a, _ := Canonical(doc("'abc'")); a == canonMust(t, doc("'abd'")) {
		t.Fatal("值真变（abc → abd）必须判不等")
	}
	// 反向锁②：标签变（字符串 '123' vs 整数 123）⇒ 必须不等 —— 清风格**不得**把标签一起抹了。
	if a, b := canonMust(t, doc("'123'")), canonMust(t, doc("123")); a == b {
		t.Fatal("标签变（字符串 \"123\" vs 整数 123）必须判不等")
	}
}

func canonMust(t *testing.T, doc string) string {
	t.Helper()
	c, err := Canonical(doc)
	if err != nil {
		t.Fatalf("规范形不可判定（不该）：%v", err)
	}
	return c
}

// ── R-106：节点标题的**去重后缀**是发射序的函数 ⇒ 属表示层（2026-09-26）──
//
// 绿侧：两个同基名的重名节点互换「谁拿基名」⇒ 规范形必须逐字节相同。
// 红侧：**基名**变（类型投影错）必须仍然判不等 —— 否则「一律把 title 抹成空」也能过绿侧。
func titleDedupeDoc(t1, t2 string) string {
	return "schema_version: 1.0.0\nid: w1\nname: 标题用例\nnodes:\n" +
		"  - id: \"100001\"\n    type: start\n    title: 开始\n" +
		"  - id: \"100002\"\n    type: text\n    title: " + t1 + "\n" +
		"  - id: \"100003\"\n    type: text\n    title: " + t2 + "\n" +
		"  - id: \"900001\"\n    type: end\n    title: 结束\n" +
		"edges:\n  - source_node: \"100001\"\n    target_node: \"100002\"\n" +
		"  - source_node: \"100002\"\n    target_node: \"100003\"\n" +
		"  - source_node: \"100003\"\n    target_node: \"900001\"\n"
}

func TestCanonAbsorbsTitleDedupeSuffixAssignment(t *testing.T) {
	a, err := Canonical(titleDedupeDoc("文本处理", "文本处理_2"))
	if err != nil {
		t.Fatalf("规范形不可判定（不该）：%v", err)
	}
	b, err := Canonical(titleDedupeDoc("文本处理_2", "文本处理"))
	if err != nil {
		t.Fatalf("规范形不可判定（不该）：%v", err)
	}
	if a != b {
		t.Fatalf("哪一个重名节点拿基名是发射序的函数（表示层），不是差异\n%s", firstLineDiff(a, b))
	}
	// 编号本身也同理：2 → 3 只是同一次编号的两次取值。
	c, err := Canonical(titleDedupeDoc("文本处理", "文本处理_3"))
	if err != nil {
		t.Fatalf("规范形不可判定（不该）：%v", err)
	}
	if a != c {
		t.Fatalf("去重编号的取值不是差异\n%s", firstLineDiff(a, c))
	}
}

func TestCanonStillCatchesTitleBaseChange(t *testing.T) {
	a, err := Canonical(titleDedupeDoc("文本处理", "文本处理_2"))
	if err != nil {
		t.Fatalf("规范形不可判定（不该）：%v", err)
	}
	// 类型投影变了（文本处理 ⇒ 大模型）：基名不同，必须红。
	b, err := Canonical(titleDedupeDoc("大模型", "大模型_2"))
	if err != nil {
		t.Fatalf("规范形不可判定（不该）：%v", err)
	}
	if a == b {
		t.Fatal("节点标题的**基名**变了必须判不等（否则抹后缀就退化成「不比较 title」）")
	}
	// 节点被抹成空标题同样必须红（平台侧空标题有语义：不参与去重）。
	c, err := Canonical(titleDedupeDoc("文本处理", ""))
	if err != nil {
		t.Fatalf("规范形不可判定（不该）：%v", err)
	}
	if a == c {
		t.Fatal("标题被抹空必须判不等")
	}
}
