package ir

import (
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
)

// naturalLess orders generated identifier names by their numeric runs, so
// `g2` sorts before `g10` (plain string order puts "g10" first).
//
// Why this is a correctness requirement, not cosmetics (2026-09-10, X23×2
// 深轮周期振荡): coze-biz raising names a merge group's hoisted variable by
// its POSITION in the wire group list (`generate.go` `agg_<owner>_g<index>`,
// owner+slot identity by design).  The lowering therefore has to emit the
// groups in the ascending canonical order of those names for the numbering to
// reproduce itself; with plain string order (g10 before g2) every round
// re-applies one permutation, so the same semantic field walks g2 → g4 → g6 …
// and the text never reaches a fixed point (semantically equivalent, hence
// invisible to any single-round check).
func naturalLess(a, b string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		da, db := isASCIIDigit(a[i]), isASCIIDigit(b[j])
		if da && db {
			si, sj := i, j
			for i < len(a) && isASCIIDigit(a[i]) {
				i++
			}
			for j < len(b) && isASCIIDigit(b[j]) {
				j++
			}
			if less, decided := numericRunLess(a[si:i], b[sj:j]); decided {
				return less
			}
			continue
		}
		if a[i] != b[j] {
			return a[i] < b[j]
		}
		i++
		j++
	}
	return len(a) < len(b)
}

// numericRunLess compares two digit runs numerically.  `decided` is false when
// the runs are numerically equal (e.g. "07" vs "7"), in which case the caller
// keeps scanning so the remaining runs still disambiguate the names.
func numericRunLess(a, b string) (less, decided bool) {
	ta, tb := strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(ta) != len(tb) {
		return len(ta) < len(tb), true
	}
	if ta != tb {
		return ta < tb, true
	}
	if len(a) != len(b) {
		return len(a) < len(b), true
	}
	return false, false
}

func isASCIIDigit(c byte) bool { return c >= '0' && c <= '9' }

// sortNamesNatural sorts a generated-name list into the canonical emission
// order (see naturalLess).
func sortNamesNatural(names []string) {
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && naturalLess(names[j], names[j-1]); j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
}

// canonicalMapEntries returns a map literal's entries in the **canonical,
// source-independent key order** (naturalLess on the static key name — the same
// ruler as sortedTypeFields, so a literal's positional identity and a record
// type's field order agree).
//
// 为什么这是正确性要求（族 2 根因，2026-09-18 一手）：
// `pyast.Serialize` 的 DictExpr 分支**按键排序**渲染 dict 体，而 shim 的形参号
// （`arg%d`）与绑定标签（`output` / `output_1` …）由条目**源文本位置**分配 ⇒
// 同一个字段在两个只差键序的源里拿到不同形参号、绑定镜像翻转：
//
//	A: { "video_url": u, "title": w } → def main(arg1, arg2): return {"title": arg2, "video_url": arg1}
//	B: { "title": w, "video_url": u } → def main(arg1, arg2): return {"title": arg1, "video_url": arg2}
//
// 两源语义**完全相同**（record/map 按键取值），但产物不同 ⇒ 反编译读回的一定是
// 规范序字面量（读的是 dict 体序），再编译落回另一侧的形参号 ⇒ `c0 ≠ c1`
// （`ARTIFACT_DRIFT/PARAM@r2`，语料 18 份；最小源
// `7531332432999890959_X5_TDYwushuiyin_table_1`）。
//
// 规则（一条、无形态守卫）：**位置身份不得是源文本的函数**。键序是**表示**，
// 凡由键序派生的位置身份（形参号 / 绑定标签序）一律按规范序分配。
//
// 调用面 = map 字面量的**闭集**（每个「把条目序变成位置身份」的渲染器一处）：
// buildMap / lowerMapPyast（顶层 shim 的 `argN`）、lowerLiteralPyast /
// renderLiteralPython（嵌套字面量的绑定标签序）。新增字面量渲染器时同样要过这里。
// 字面量的**其余**顺序（node_inputs 数组序、End.Outputs 序）不是位置身份，
// 数组字面量的元素序是数据 —— 都不在此规则内。
func canonicalMapEntries(entries []ast.MapEntry) []ast.MapEntry {
	if len(entries) < 2 {
		return entries
	}
	out := make([]ast.MapEntry, len(entries))
	copy(out, entries)
	keys := make([]string, len(out))
	for i, e := range out {
		key, _ := staticMapKeyName(e.Key)
		keys[i] = key
	}
	// 稳定插入排序：动态键（staticMapKeyName 失败 ⇒ 空键）保持相对序，随后
	// 照旧在**同一条**上报 dynamicMapKeyError —— 排序不改错误面。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && naturalLess(keys[j], keys[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return out
}
