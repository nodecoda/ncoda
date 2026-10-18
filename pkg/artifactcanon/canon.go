package artifactcanon

// canon.go —— 产物的**拓扑规范形**（**唯一实现**：tools/five-step-roundtrip 与
// platforms/cozebiz/decompile 的 C∘R∘C 判据都调这里，R-21 收口 2026-09-23 ——
// 「判据按原始 id 比」的根因就是**同一事实两处实现**，一处抹 id、一处没抹）：抹掉 id / 文档顺序 / 键序 / 格式，只留
// 「拓扑 + 内容」。
//
// 为什么需要它（2026-09-15 用户拍板）：
//
//	「节点不会相同的，不要考虑这个比较了，采用拓扑比较吧」
//
// 逐字节比较（c0 == c1）把**非契约**的表示层当成欠账：同一条语义两轮拿到不同的
// node id 是**必然**（id 是构造期分配序的像），据此判红会让判据长期失真——更糟的是
// 它让「表示层差异」与「语义差异」混在一个数字里，真正要看的东西（拓扑与参数是否
// 一致）反而看不见。id 不是契约（AGENTS.md 已有此纪律），所以：
//
//	判据 = Canonical(c0) == Canonical(c1)
//
// 规范化四步：
//  ① 节点 id 按**结构遍历**重命名 `n1..nk`：从 `start` 出发 BFS，后继按
//     （源端口名, 目标类型, id）定序 ⇒ 同构的两张图得到同一套名字；
//  ② 一切 id 引用随之改写：`source_node` / `target_node` / `ref_node` / `blockID`
//     以及模板占位符 `{{block_output_<id>.<port>}}`；
//  ③ 节点序列 / 边序列按规范名排序、mapping 键序统一 —— 产物里的「数组顺序、
//     键序」是序列化细节，不是语义；
//  ④ 顶层 `id`（工作流身份，由内容派生的雪花号）不参与比较。
//
// 保留的判据强度：**参数逐字**。抹掉 id 不等于抹掉内容 —— 同一个拓扑下模板文本、
// 常量、模型参数、端口声明若变了，规范形就不同（这才是要抓的欠账）。
// 到达不了的节点（无入边）同样参与比较 —— 按**结构键**定名（不是文档序，
// 见 canonNames 相位 2）：产物的结构事实，不比就会看不见。

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// canonEdge 是遍历用的一条出边（只看结构，不看顺序）。
type canonEdge struct {
	port   string
	target string
}

// Result = 规范形 + 定名映射（原始 id → 规范名）。映射只供**报错可读**用：
// 判据面一律用规范名（原始 id 不是契约，不得参与比较 —— R-21）。
type Result struct {
	Text string
	// Names：原始 id → 规范名（n1..nk）。
	Names map[string]string
	// RawTieBreaks：**自同构孪生**（结构色相同的并列兄弟）对数。非零 = 该产物里存在
	// 「结构 + 内容全同」的孪生 ⇒ 定名需要 min-over-branches（见 CanonicalResult）；
	// 它是**可观测面**（不是静默兜底）。
	RawTieBreaks int
	// TieLeaves：min-over-branches 实际求值的分支数（无并列 = 1）。**0 且
	// RawTieBreaks > 0** = 组合数超过 canonTieRunLimit、退回 id 序（残余，见 CANON-FORM §6-1）。
	TieLeaves int
}

// Canonical 返回产物的拓扑规范形（逐字节可比较的 YAML 文本）。
func Canonical(y string) (string, error) {
	r, err := CanonicalResult(y)
	return r.Text, err
}

// CanonicalResult 返回规范形与定名映射（见 Result）。
func CanonicalResult(y string) (Result, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(y), &doc); err != nil {
		return Result{}, err
	}
	root := docRootNode(&doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return Result{}, errors.New("产物不是 mapping")
	}
	// 纯常量 shim 的物化序/输出类型附注不是契约（见 normalize_const_shims.go）：
	// 按 code 合并同类节点、清空类型提示，再进入 id 定名与拓扑比较。
	normalizeConstShims(root)
	nodesSeq := mapValueNode(root, "nodes")
	if err := validateNodeEntries(nodesSeq, true); err != nil {
		return Result{}, err
	}
	var entries []*yaml.Node
	collectNodeMappings(nodesSeq, &entries)

	var order []string
	byID := map[string]*yaml.Node{}
	for _, e := range entries {
		if id := scalarField(e, "id"); id != "" {
			order = append(order, id)
			byID[id] = e
		}
	}

	out := map[string][]canonEdge{}
	collectCanonEdges(nodesSeq, out)
	if rootEdges := mapValueNode(root, "edges"); rootEdges != nil {
		collectCanonEdgesFromSeq(rootEdges, out)
	}

	refIn := map[string][]string{}
	collectRefIn(root, refIn)

	render := func(names map[string]string) (string, error) {
		b, err := yaml.Marshal(canonValue(root, "", names))
		if err != nil {
			return "", err
		}
		return string(b), nil
	}

	// **min-over-branches**（2026-09-27）：并列兄弟（同端口 ∧ 同结构色 = 自同构孪生）
	// 的「谁拿小号」在结构上不可判定 ⇒ 逐排列试、取规范形文本最小者。
	//
	// 为什么这样就与编号无关：并列组的成员集合、「基序」（按 id 升序）与**全部排列**
	// 都由结构决定（id 只影响「哪个排列给哪份文本」，不影响可得的文本集合）⇒ 同一
	// 同构类的两份产物算出的**候选集合相同**，最小值也相同。没有并列时只有一遍，
	// 输出与旧实现逐字节相同（常见路径零风险）。
	//
	// 运行数上界 = Π(并列组大小!)；超过 `canonTieRunLimit` 时退回**id 序**（历史行为）
	// 并把并列计入 `RawTieBreaks`（残余登记在 CANON-FORM §6-1，不是静默兜底）。
	rawTies := 0
	var best string
	var bestNames map[string]string
	type branch struct{ choices [][]int }
	stack := []branch{{}}
	runs := 0
	for len(stack) > 0 {
		b := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		runs++
		if runs > canonTieRunLimit {
			fb := canonNames(order, byID, out, refIn, nil, false)
			if fb.rawTies > rawTies {
				rawTies = fb.rawTies
			}
			return Result{Text: string(canonMustText(render, fb.names)), Names: fb.names, RawTieBreaks: rawTies, TieLeaves: 0}, nil
		}
		res := canonNames(order, byID, out, refIn, b.choices, true)
		if res.rawTies > rawTies {
			rawTies = res.rawTies
		}
		if len(res.pending) > 0 {
			for _, perm := range tiePermutations(len(res.pending)) {
				cp := make([][]int, 0, len(b.choices)+1)
				cp = append(cp, b.choices...)
				cp = append(cp, perm)
				stack = append(stack, branch{cp})
			}
			continue
		}
		text, err := render(res.names)
		if err != nil {
			return Result{}, err
		}
		if best == "" || text < best {
			best, bestNames = text, res.names
		}
	}
	if best == "" {
		return Result{}, errors.New("规范形为空（定名没有产生任何分支）")
	}
	return Result{Text: best, Names: bestNames, RawTieBreaks: rawTies, TieLeaves: runs}, nil
}

// canonTieRunLimit：min-over-branches 的运行数上限（超限 ⇒ 退回 id 序 + 报 RawTieBreaks）。
const canonTieRunLimit = 24

// tiePermutations 返回 n 的**全部排列**（n ≤ 5 时最多 120 个；n 更大时只给恒等排列，
// 因为调用方会因超限而走兜底路径 —— 不在这里静默截断成「随便一种」）。
func tiePermutations(n int) [][]int {
	if n <= 1 {
		return [][]int{{0}}
	}
	if n > 5 {
		id := make([]int, n)
		for i := range id {
			id[i] = i
		}
		return [][]int{id}
	}
	var out [][]int
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	var rec func(k int)
	rec = func(k int) {
		if k == n {
			cp := append([]int(nil), idx...)
			out = append(out, cp)
			return
		}
		for i := k; i < n; i++ {
			idx[k], idx[i] = idx[i], idx[k]
			rec(k + 1)
			idx[k], idx[i] = idx[i], idx[k]
		}
	}
	rec(0)
	return out
}

func canonMustText(render func(map[string]string) (string, error), names map[string]string) string {
	t, err := render(names)
	if err != nil {
		return ""
	}
	return t
}

// validateNodeEntries：节点的结构事实必须完整**才能**比较。
//
// 缺 id/type、节点不是 mapping、顶层节点表为空 ⇒ 一律 fail loud。这是判据最危险
// 的失效形态的反面：静默把「没法判定」当「相等」，会把不可导入的产物报成绿
// （与 parseGraph 同一判据；两处不可分裂，否则一条通道 fail loud、另一条瞎）。
// 容器子节点同样校验（空容器体允许 —— 空臂不变量：清空不是错误）。
func validateNodeEntries(seq *yaml.Node, top bool) error {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return errors.New("产物缺 nodes 序列")
	}
	if top && len(seq.Content) == 0 {
		return errors.New("产物 nodes 为空（无可比较结构）")
	}
	for _, n := range seq.Content {
		if n.Kind != yaml.MappingNode {
			return fmt.Errorf("节点不是映射（yaml kind=%d）", n.Kind)
		}
		if scalarField(n, "id") == "" || scalarField(n, "type") == "" {
			return errors.New("节点缺 id 或 type")
		}
		if kids := mapValueNode(n, "nodes"); kids != nil {
			if err := validateNodeEntries(kids, false); err != nil {
				return fmt.Errorf("容器子节点：%w", err)
			}
		}
	}
	return nil
}

// canonNames 按结构遍历给节点分配规范名。同构 ⇒ 同一套名字。
//
// 两个相位，都**只由结构决定**（文档序不得参与 —— 否则「顺序不是语义」在
// 不可达节点上失效：实测 2026-09-15，反序文档得到 u1/u2 互换 ⇒ 假漂移）：
//
//	相位 1：从 start 出发 BFS，后继按（源端口, 目标类型, id）定序；
//	相位 2：起不到（无入边）的节点按**结构键**定序（被谁引用 → 类型 → 内容签名，
//	        签名里的残余数字 id 一律掩成 #），逐个作为新分量根再 BFS。
//
// 残余（登记）：相位 2 里「互相引用且无外部引用的完全同构孪生」结构键相同，
// 仍按文档序定名 —— 图规范标号（canonical labeling）本身是难问题；本仓产物由
// 产物门禁保证「无孤儿节点」，该分支是防御路径。
// canonNameResult = 定名结果（见 Result）。
type canonNameResult struct {
	names   map[string]string
	rawTies int
	// pending = 第一个**未给排列**的并列组（自同构孪生）成员，按原始 id 升序的基序。
	// 非空 ⇒ 本次运行在该并列处停下，调用方须逐排列重跑（min-over-branches）。
	pending []string
}

// canonNames 给每个节点定规范名（`n1..nk`）。三条纪律：
//
//	① 定序键**不含原始 id**（R-21 的根因）—— 兄弟按（源端口 → 结构色）定序；
//	② 结构色也打平 = **自同构孪生**：此时**没有任何输入派生序可判**「谁拿小号」。
//	   按原始 id 兜底会让命名随编号换位 ⇒ 规范形不等 ⇒ **假红**（2026-09-27 单轴扰动
//	   实测：画布坐标按轴表掩掉后，X12 的两个对称 `code` 节点成为真孪生，旧的 id
//	   兜底立刻把整份产物判成「换编号不一致」）；
//	③ 故并列组交给调用方：`enumerate` 模式遇到**第一个未给排列**的并列组就停下并
//	   报回成员（`pending`）⇒ 调用方逐排列重跑、取规范形文本最小者（min-over-branches，
//	   见 `CanonicalResult`）；非 enumerate = 历史行为（id 序），并列对数计入 `rawTies`。
func canonNames(order []string, byID map[string]*yaml.Node, out map[string][]canonEdge,
	refIn map[string][]string, choices [][]int, enumerate bool) canonNameResult {
	names := map[string]string{}
	next := 1
	colors := colorRefine(order, byID, out)
	rawTies := 0
	tieIdx := 0

	// visit 从一个根开始 BFS 定名。返回（pending, true）= 停在第一个未定并列组。
	visit := func(root string) ([]string, bool) {
		names[root] = fmt.Sprintf("n%d", next)
		next++
		queue := []string{root}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			seen := map[string]bool{}
			var cands []canonEdge
			for _, ed := range out[cur] {
				if ed.target == "" || names[ed.target] != "" || seen[ed.target] {
					continue
				}
				seen[ed.target] = true
				cands = append(cands, ed)
			}
			// 主键 = （源端口 → 结构色）；**不**含原始 id。并列组随后单独处理。
			sort.Slice(cands, func(i, j int) bool {
				if cands[i].port != cands[j].port {
					return cands[i].port < cands[j].port
				}
				return colors[cands[i].target] < colors[cands[j].target]
			})
			for i := 0; i < len(cands); {
				j := i + 1
				for j < len(cands) && cands[j].port == cands[i].port &&
					colors[cands[j].target] == colors[cands[i].target] {
					j++
				}
				if j-i > 1 {
					members := cands[i:j]
					for a := 0; a < len(members); a++ {
						for b := a + 1; b < len(members); b++ {
							rawTies++
						}
					}
					// 基序 = 原始 id 升序（只作**排列的下标基准**：全部排列都会被试到，
					// 故基序本身不进入结果 —— 这正是它与编号无关的原因）。
					sort.Slice(members, func(a, b int) bool { return members[a].target < members[b].target })
					if enumerate {
						if tieIdx >= len(choices) {
							ids := make([]string, len(members))
							for k, m := range members {
								ids[k] = m.target
							}
							return ids, true
						}
						perm := choices[tieIdx]
						tieIdx++
						reordered := make([]canonEdge, len(members))
						for k, src := range perm {
							reordered[k] = members[src]
						}
						copy(members, reordered)
					}
				}
				i = j
			}
			for _, c := range cands {
				names[c.target] = fmt.Sprintf("n%d", next)
				next++
				queue = append(queue, c.target)
			}
		}
		return nil, false
	}

	start := ""
	for _, id := range order {
		if scalarField(byID[id], "type") == "start" {
			start = id
			break
		}
	}
	if start == "" && len(order) > 0 {
		start = order[0]
	}
	if start != "" {
		if ids, stop := visit(start); stop {
			return canonNameResult{names: names, rawTies: rawTies, pending: ids}
		}
	}
	for {
		var rest []string
		for _, id := range order {
			if names[id] == "" {
				rest = append(rest, id)
			}
		}
		if len(rest) == 0 {
			return canonNameResult{names: names, rawTies: rawTies}
		}
		keys := map[string]string{}
		for _, id := range rest {
			keys[id] = restKey(id, byID, names, refIn, colors)
		}
		sort.SliceStable(rest, func(i, j int) bool { return keys[rest[i]] < keys[rest[j]] })
		visit(rest[0])
	}
}

// colorRefine = 1-WL 颜色细化（Weisfeiler–Lehman）：col₀ = hash(类型 + 内容签名)，
// col_{k+1} = hash(col_k, （端口, col_k(子)）多重集的排序串)。**只由结构 + 内容决定**
// ⇒ 同构的两张图得到同一套颜色（这正是「兄弟定序不得靠原始 id」所需要的不变量）。
//
// 两条实现纪律（都有一手教训）：
//
//	① **每轮必须哈希成定长**：把「上一轮色」原样嵌进本轮色，串长会随轮数翻倍
//	   （子色嵌在父色里）⇒ 几十轮后内存爆掉（实测：83 节点的生成器源直接把进程 OOM）。
//	② 不动点判据 = **划分**稳定，不是值相等（每轮再哈希，值必然变）；划分相同即
//	   已无法再区分，提前退出（上界仍是 len(order) 轮，工作量 O(V·E)）。
func colorRefine(order []string, byID map[string]*yaml.Node, out map[string][]canonEdge) map[string]string {
	hash := func(s string) string {
		sum := sha256.Sum256([]byte(s))
		return hex.EncodeToString(sum[:8])
	}
	col := map[string]string{}
	for _, id := range order {
		col[id] = hash(structKey(id, byID))
	}
	partOf := func(c map[string]string) string {
		keys := make([]string, 0, len(c))
		for _, v := range c {
			keys = append(keys, v)
		}
		sort.Strings(keys)
		rank := map[string]int{}
		for _, k := range keys {
			if _, ok := rank[k]; !ok {
				rank[k] = len(rank)
			}
		}
		var b strings.Builder
		for _, id := range order {
			fmt.Fprintf(&b, "%s=%d;", id, rank[c[id]])
		}
		return b.String()
	}
	prevPart := partOf(col)
	for round := 0; round < len(order); round++ {
		next := make(map[string]string, len(col))
		for _, id := range order {
			parts := make([]string, 0, len(out[id]))
			for _, e := range out[id] {
				parts = append(parts, e.port+"\x00"+col[e.target])
			}
			sort.Strings(parts)
			next[id] = hash(col[id] + "\x01" + strings.Join(parts, "\x02"))
		}
		col = next
		part := partOf(col)
		if part == prevPart {
			break
		}
		prevPart = part
	}
	return col
}

// structKey = 节点自身的**结构 + 内容键**（类型 + 已掩码的内容签名，不含原始 id）。
//
// **key 必须是 `"nodes"`，不能是 `"id"`**（2026-09-27 单轴扰动实测的根因）：`canonValue`
// 的「节点条目」判据逐字 = 「它在 `nodes:` 序列里」（`key == "nodes"`）。传 `"id"` 时
// `isNodeEntry == false` ⇒ 节点条目的三条掩码**全部失效**：`position` 没被丢、
// `title` 去重后缀没被抹、`id` 只靠 `maskIDs` 兜底。后果是**假红**（不是漏报）：
// 只手改画布坐标（平台导出里最常见的差异）⇒ 结构色变 ⇒ 兄弟定序变 ⇒ 定名换位 ⇒
// 规范形不等 —— 与 R-21「排序发生在掩码之前」是同一个病（表示层从后门回到比较面）。
func structKey(id string, byID map[string]*yaml.Node) string {
	sig, err := yaml.Marshal(canonValue(byID[id], "nodes", map[string]string{}))
	if err != nil {
		sig = []byte("?")
	}
	return scalarField(byID[id], "type") + "\x00" + maskIDs(string(sig))
}

// restKey：不可达节点的结构定序键（被谁引用 → 类型 → 内容签名 → 结构色）。
// 签名一律**与定名无关**（names 传空 map ⇒ 引用被掩成 `?#`），否则签名随定名进程
// 变化 ⇒ 又把顺序从后门放回来。
func restKey(id string, byID map[string]*yaml.Node, names map[string]string, refIn map[string][]string,
	colors map[string]string) string {
	refs := append([]string(nil), refIn[id]...)
	sort.Strings(refs)
	return strings.Join(refs, ",") + "\x00" + structKey(id, byID) + "\x00" + colors[id]
}

// allDigits：纯数字串（产物 id 的形态）。
func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// canonIDRun：4 位以上的数字串（产物 id 的形态）—— 只用于**签名掩码**，
// 不参与被比较的规范形输出。
var canonIDRun = regexp.MustCompile(`[0-9]{4,}`)

func maskIDs(s string) string { return canonIDRun.ReplaceAllString(s, "#") }

// collectRefIn：登记「谁在哪个槽位引用了哪个 id」（边不算 —— 边已进 out）。
func collectRefIn(root *yaml.Node, refIn map[string][]string) {
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				if (k.Value == "ref_node" || k.Value == "blockID") && v.Kind == yaml.ScalarNode && v.Value != "" {
					refIn[v.Value] = append(refIn[v.Value], k.Value)
				}
				walk(v)
			}
		case yaml.SequenceNode:
			for _, c := range n.Content {
				walk(c)
			}
		case yaml.ScalarNode:
			for _, m := range canonTemplateRef.FindAllStringSubmatch(n.Value, -1) {
				refIn[m[1]] = append(refIn[m[1]], "template")
			}
		}
	}
	walk(root)
}

var canonTemplateRef = regexp.MustCompile(`block_output_([0-9]+)\.`)

// canonValue 递归改写 + 归一化：key 是当前值在父 mapping 里的键名（根为 ""）。
func canonValue(n *yaml.Node, key string, names map[string]string) *yaml.Node {
	switch n.Kind {
	case yaml.MappingNode:
		// 「是节点条目」的判据 = **它在 `nodes:` 序列里**（key 即该序列的键名），
		// 与 parseGraph / collectNodeMappings 的定义**逐字一致**：两个判据对「什么是
		// 节点」必须给同一个答案，否则一边看得见、另一边看不见（E-9 的同类）。
		// 只按「有 id+type 两个键」判会把值树里同形的对象也当节点条目 ——
		// 实测语料：**222/322 份 workflow.json** 的 `nodes[i].blocks[j]` 就是这种形状
		// （1161 个，全是 6 位数字 id 且**无一**与节点 id 重合）⇒ 值树里的 id 会被
		// 当引用改写。值树里的同名字段是**内容**，不是引用。
		isNodeEntry := key == "nodes" && mapValueNode(n, "id") != nil && mapValueNode(n, "type") != nil
		type pair struct{ k, v *yaml.Node }
		var pairs []pair
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if key == "" && k.Value == "id" {
				continue // 顶层工作流身份：内容派生，不参与比较
			}
			// `position` 是**画布坐标**（前端元数据），不是产物语义：平台侧它由
			// 前端持有（coze-studio OSS `backend/domain/workflow/entity/vo/canvas.go`
			// 「Meta holds meta data used by frontend, such as the node's position
			// within canvas」，执行域无消费者），语料实证是自由浮点
			// （`7542708432375382016_File2URL`：x=-459.36260992434626）。
			// 我方 lowering 也只是按**发射序**给的布局行
			// （`internal/backend/cozebiz/render*.go`：`Position = &Position{X: row*340}`）
			// ⇒ 它是**已被抹除的文档顺序**的函数，把它留在规范形里等于让顺序噪声
			// 从后门回到比较面（实测：03_conditional_branch 的三节点坐标轮换
			// 1700/1020/1360 只有顺序变了，语义一字未改）。
			//
			// 与 id 同一类：抹的是**位置层**，不是内容 —— 判据范围严格限定在
			// **节点条目**（`nodes:` 里的映射）上；`parameters` 里的同名键（插件的
			// 坐标参数就是 `x`/`y` 这种形状）是**内容**，一律逐字保留。
			if isNodeEntry && k.Value == "position" {
				continue
			}
			if isNodeEntry && k.Value == "type" {
				// type 是规范名排序的输入，原样保留
			}
			if isNodeEntry && k.Value == "title" {
				pairs = append(pairs, pair{k, canonNodeTitle(v)})
				continue
			}
			switch {
			case k.Value == "id" && isNodeEntry:
				pairs = append(pairs, pair{k, canonRefName(v, names)})
			case k.Value == "source_node" || k.Value == "target_node" ||
				k.Value == "ref_node" || k.Value == "blockID":
				pairs = append(pairs, pair{k, canonRefName(v, names)})
			default:
				pairs = append(pairs, pair{k, canonValue(v, k.Value, names)})
			}
		}
		sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].k.Value < pairs[j].k.Value })
		res := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, p := range pairs {
			res.Content = append(res.Content, canonStyleNeutral(p.k), p.v)
		}
		return res
	case yaml.SequenceNode:
		res := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, c := range n.Content {
			res.Content = append(res.Content, canonValue(c, key, names))
		}
		switch key {
		case "nodes", "edges":
			// 序列定序 = **渲染后的元素文本**（结构 + 内容决定）。
			// 曾经的写法是「按原始 id 排序」（sortSeqById / sortSeqByEdge）——
			// 那是 R-21 的第二半根因（2026-09-23 实测）：排序发生在**掩码之前**，
			// 掩码把 id 抹成 `#` 却抹不掉**由 id 决定的那份顺序** ⇒ 同一张图只要
			// 编号换位，签名文本就不同 ⇒ 结构色不同 ⇒ 定名换位 ⇒ **假红**
			// （一手复现：examples X12 的 c0 做 id 重编号后，canon 文本 34/45 个
			// 名字换位，100030 由 n10 变 n13 —— 与报出的那条红逐字同症状）。
			//
			// 文本相同者互换位置**不改变输出**（字符串逐字相等）⇒ 并列无需 id 兜底。
			sortSeqByRender(res)
		}
		return res
	case yaml.ScalarNode:
		return canonStyleNeutral(canonScalar(n, names))
	}
	return n
}

// canonStyleNeutral：标量的**书写风格**（plain / 单引号 / 双引号 / 块标量）是**表示**，
// 不是内容 —— 判据口径逐字是「抹 id / 数组顺序 / 键序 / **格式**」。
//
// 做法 = **按值重编码**：只清 `Style`、保留 `Value` 与 `Tag`，书写形式交给编码器重定。
// 两条反向锁因此仍然成立：值真变 ⇒ `Value` 不同；**标签变**（`'123'` 字符串 vs
// `123` 整数）⇒ `Tag` 不同（清风格不得把标签一起抹了）。
//
// 一手缺陷（2026-09-24，R-39）：语料 `7531673552438263834_X182_P_zhishi_card_599_1`
// 的 `c1.yaml`（`content: |4-` + `{{text}}`）与 `c2.yaml`（`content: '{{text}}'`）
// **解码后逐字相同**，旧实现却报 `ARTIFACT_OSCILLATION@r3` —— 假红落在**实现没变**的地方，
// 真信号被一起埋掉（本仓 2026-09-19 退役令的原话：判据红了没人看得懂 ⇒ 门禁被无视）。
func canonStyleNeutral(n *yaml.Node) *yaml.Node {
	if n == nil || n.Kind != yaml.ScalarNode || n.Style == 0 {
		return n
	}
	c := *n
	c.Style = 0
	return &c
}

// canonRefName 把 id 引用改写成规范名（未知 id 原样保留，且显式可见）。
func canonRefName(n *yaml.Node, names map[string]string) *yaml.Node {
	if n == nil || n.Kind != yaml.ScalarNode {
		return n
	}
	if to, ok := names[n.Value]; ok {
		c := *n
		c.Value = to
		return canonStyleNeutral(&c)
	}
	if allDigits(n.Value) && n.Value != "" {
		c := *n
		c.Value = "?" + n.Value
		return canonStyleNeutral(&c)
	}
	return canonStyleNeutral(n)
}

// canonNodeTitle：节点条目的 `title` —— **基名是内容，去重后缀 `_<n>` 不是**。
//
// 理由与 `position` 逐字同源（见 canonValue 里那段注释）：后缀是**发射序的函数**
// —— 唯一生产者 `internal/backend/cozebiz/render.go::dedupeNodeTitles` 按
// `doc.Nodes` 的**列序**给重名节点编号，而列序正是 canon 要抹掉的文档顺序
// （「把它留在规范形里等于让顺序噪声从后门回到比较面」，2026-09-15 拍板）。
//
// 一手证据（R-106，2026-09-26）：examples/nodecoda/10_complex_multi_step 的两个
// `variable_merge` 在首轮/次轮互换 `变量聚合` / `变量聚合_2` ⇒ 报
// ARTIFACT_DRIFT/PARAM。而「哪一个重名节点拿基名」**在任何输入派生序下都不稳定**：
//
//	① 按发射序：首轮 `return` 写在臂里 ⇒ flow-exit φ 与 `end` 在兄弟臂渲染完之前
//	   物化（renderIf 的臂作用域终止协议）⇒ 列序与次轮不同；
//	② 按 id 升序（= IR 构造序）：同源的 examples/nodecoda/20_array_iteration_formatting
//	   里顶层 `code` 与 `loop` 的 id 在两轮之间互换（100002/100003）⇒ 改按 id 序编号
//	   会把该源打红（2026-09-26 实测，非推测）。
//
// ⇒ 这条自由度**写不出规范型**（两种写法语义同一，AGENTS.md：写不出两种写法的就不
// 需要规范型），按定义属表示层：比较基名。类型投影错（`代码` vs `大模型`）照样红；
// 节点数 / 拓扑变了照样红。平台自身导出同样带 `_N` 后缀（dedupeNodeTitles 头注），
// 故本规则对语料方向也是同一口径。
var titleDedupeSuffix = regexp.MustCompile(`_[0-9]+$`)

func canonNodeTitle(n *yaml.Node) *yaml.Node {
	if n == nil || n.Kind != yaml.ScalarNode {
		return n
	}
	if !titleDedupeSuffix.MatchString(n.Value) {
		return canonStyleNeutral(n)
	}
	c := *n
	c.Value = titleDedupeSuffix.ReplaceAllString(n.Value, "")
	return canonStyleNeutral(&c)
}

func canonScalar(n *yaml.Node, names map[string]string) *yaml.Node {
	if n.Kind != yaml.ScalarNode || !strings.Contains(n.Value, "block_output_") {
		return n
	}
	c := *n
	c.Value = canonTemplateRef.ReplaceAllStringFunc(n.Value, func(m string) string {
		id := canonTemplateRef.FindStringSubmatch(m)[1]
		if to, ok := names[id]; ok {
			return "block_output_" + to + "."
		}
		return m
	})
	return &c
}

// sortSeqByRender：序列元素按**渲染后（并掩码 id 后）的文本**定序 —— 主键只由
// 结构 + 内容决定，同类元素换编号 ⇒ 同一份顺序。渲染只在排序前算一次
// （容器元素含整棵子树，比较器里反复 Marshal 在 672 源跑批上是实打实的开销）。
//
// **两个键**（顺序不可颠倒）：
//
//	① 掩码文本 = 结构 + 内容（原始 id 一律 `#`）——不变量，负责定序；
//	② 原样文本 = 读得懂的兜底（仅当 ① 打平时才参与）。
//
// ② 只在 ① 相同时生效 ⇒ 它引用的编号**不可能**影响规范形文本：① 打平意味着两个
// 元素的掩码文本逐字相等，换位后掩码文本仍是同一串（签名路径最后整体掩码）。
// 反之若只用 ① 而不用 ②：输出面（引用已是规范名）里含 4 位以上数字的元素会
// 并列、退化成文档序——那就把顺序噪声从后门放回来了。
func sortSeqByRender(seq *yaml.Node) {
	type item struct {
		masked string
		raw    string
		n      *yaml.Node
	}
	items := make([]item, 0, len(seq.Content))
	for _, c := range seq.Content {
		b, err := yaml.Marshal(c)
		if err != nil {
			b = []byte("?")
		}
		items = append(items, item{masked: maskIDs(string(b)), raw: string(b), n: c})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].masked != items[j].masked {
			return items[i].masked < items[j].masked
		}
		return items[i].raw < items[j].raw
	})
	for i := range items {
		seq.Content[i] = items[i].n
	}
}

// ── yaml.Node 小工具 ────────────────────────────────────────────────────

func docRootNode(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		return doc.Content[0]
	}
	return doc
}

func mapValueNode(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func scalarField(m *yaml.Node, key string) string {
	v := mapValueNode(m, key)
	if v == nil || v.Kind != yaml.ScalarNode {
		return ""
	}
	return v.Value
}

// collectNodeMappings 递归收集一切节点条目（容器子节点在容器节点的 `nodes:` 里）。
func collectNodeMappings(seq *yaml.Node, out *[]*yaml.Node) {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return
	}
	for _, n := range seq.Content {
		if n.Kind != yaml.MappingNode {
			continue
		}
		*out = append(*out, n)
		if kids := mapValueNode(n, "nodes"); kids != nil {
			collectNodeMappings(kids, out)
		}
	}
}

// collectCanonEdges 递归收集一切边（容器内部的边在容器的 `edges:` 里）。
func collectCanonEdges(seq *yaml.Node, out map[string][]canonEdge) {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return
	}
	for _, n := range seq.Content {
		if n.Kind != yaml.MappingNode {
			continue
		}
		if e := mapValueNode(n, "edges"); e != nil {
			collectCanonEdgesFromSeq(e, out)
		}
		if kids := mapValueNode(n, "nodes"); kids != nil {
			collectCanonEdges(kids, out)
		}
	}
}

func collectCanonEdgesFromSeq(seq *yaml.Node, out map[string][]canonEdge) {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return
	}
	for _, e := range seq.Content {
		if e.Kind != yaml.MappingNode {
			continue
		}
		out[scalarField(e, "source_node")] = append(out[scalarField(e, "source_node")],
			canonEdge{port: scalarField(e, "source_port"), target: scalarField(e, "target_node")})
	}
}
