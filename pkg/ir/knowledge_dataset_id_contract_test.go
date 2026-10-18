package ir_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

// knowledge dataset id 的**值域契约**（2026-09-26 一手收口）。
//
// 规则（一处定义）：`pkg/ir/build.go::datasetID` 铸出的 id 必须落在**平台
// schema 的域**里 —— coze-studio 的 knowledge 节点逐元素 `cast.ToInt64E`
// （backend/domain/workflow/internal/nodes/knowledge/knowledge_retrieve.go:64-71）
// ⇒ dataset id 是 **int64**，`"001"` 与 `"1"` 是同一个 id 的两种拼写。
//
// 推论 = **幂等**：`datasetID(datasetID(x)) == datasetID(x)`。这条推论正是往返
// 所需的性质 —— 写侧铸 id、读侧按平台 schema 读回 id、再写必须同值。
//
// 修前缺陷（一手）：实现「原样取尾部数字」而头注契约写的是 `"ds-001" → "1"` ——
// 写侧铸 `"001"`、读侧读回 `"1"` ⇒ 逐轮 `ARTIFACT_DRIFT/PARAM`
// （examples/nodecoda/{02,10,41} 三源，逐字节首差异 `- "001"` vs `- "1"`）。
//
// 撤实现（退回「原样取尾部数字，不做 int64 规范化」）⇒ 本文件红。

var decimalIDRe = regexp.MustCompile(`^[0-9]+$`)

// knowledgeIDs 走公共面（buildIR）取 `knowledge(<name>, q)` 的 DatasetIDs ——
// `datasetID` 是包内私有，判据只认可观察行为。
func knowledgeIDs(t *testing.T, name string) []string {
	t.Helper()
	src := workflowHeader + fmt.Sprintf(`
function main(string q) -> string {
    let k = knowledge(%q, q);
    return "x";
}
`, name)
	nodes := nodesOf(buildIR(t, src))
	ks := nodes[ir.KindKnowledge]
	if len(ks) != 1 {
		t.Fatalf("knowledge(%q): want 1 knowledge node, got %d", name, len(ks))
	}
	ids := ks[0].(*ir.KnowledgeCall).DatasetIDs
	if len(ids) == 0 {
		t.Fatalf("knowledge(%q): DatasetIDs is empty", name)
	}
	return ids
}

// 正面锚：id 落在 int64 十进制域，且**不带前导零**（前导零 = 平台 schema 之外
// 的另一种拼写 ⇒ 读侧规范化后同名不同串）。
func TestKnowledgeDatasetIDLandsInPlatformInt64Domain(t *testing.T) {
	got := knowledgeIDs(t, "ds-001,ds-002")
	want := []string{"1", "2"}
	if len(got) != len(want) {
		t.Fatalf("DatasetIDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("DatasetIDs[%d] = %q, want %q（`\"001\"` 与 `\"1\"` 是同一个 int64 id 的两种拼写，写侧必须只铸一种）", i, got[i], want[i])
		}
	}
	for i, id := range got {
		if !decimalIDRe.MatchString(id) {
			t.Errorf("DatasetIDs[%d] = %q：不在平台的 int64 十进制域内", i, id)
		}
		if len(id) > 1 && id[0] == '0' {
			t.Errorf("DatasetIDs[%d] = %q：带前导零 ⇒ 与读侧 canonicalInt64 的规范形不同名", i, id)
		}
	}
}

// 幂等（= 一轮不动点）：把写侧铸出的 id 当名字再喂一遍，必须同值。
// 这是往返面的最小机器判据（不必跑 672 源）。
func TestKnowledgeDatasetIDIsIdempotent(t *testing.T) {
	for _, name := range []string{
		"ds-001",    // 尾部数字带前导零 —— 本条的出生点
		"ds-010",    // 前导零 + 进位
		"0010",      // 名字本身就是带前导零的数字串
		"ds-000",    // 全零尾部 ⇒ int64 0
		"1",         // 已经是规范形
		"0",         // 零
		"kb",        // 尾部无数字 ⇒ FNV 哈希档
		"kb-lookup", // 连字符名 ⇒ FNV 哈希档
	} {
		first := knowledgeIDs(t, name)[0]
		second := knowledgeIDs(t, first)[0]
		if first != second {
			t.Errorf("dataset id 不幂等：name=%q ⇒ first=%q ⇒ second=%q（写侧与读侧不共尺）", name, first, second)
		}
		if !decimalIDRe.MatchString(first) {
			t.Errorf("name=%q ⇒ id=%q 不在平台的 int64 十进制域内", name, first)
		}
	}
}

// 哈希档也必须落在同一域（无尾数字名的路径不构成例外）。
func TestKnowledgeDatasetIDHashFallbackStaysInDomain(t *testing.T) {
	id := knowledgeIDs(t, "kb-lookup")[0]
	if !decimalIDRe.MatchString(id) {
		t.Fatalf("哈希档 id = %q：不在平台的 int64 十进制域内", id)
	}
	// 确定性：同名两次同值（FNV 无随机源）。
	if again := knowledgeIDs(t, "kb-lookup")[0]; again != id {
		t.Fatalf("哈希档不确定：%q vs %q", id, again)
	}
}
