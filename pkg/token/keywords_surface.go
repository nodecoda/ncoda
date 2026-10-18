package token

import "sort"

// Keywords returns every reserved keyword spelling, sorted.
//
// 为什么要有这个出口（2026-09-16）：语言面必须**可机械枚举**（与形状空间台账
// 同一原则）。`keywords` 是私有 map，此前「实现有哪些关键字」只能靠人读 map
// 字面量 —— 于是「实现有、标准无」的缺口只能靠偶然发现（实测漂移：`$` 转义与
// `??` 在实现里活着、在标准 grammar.ebnf 里 0 命中，而这一对差异直到把标准仓的
// 示例接进验收面才被看见）。枚举出口把这件事变成可复算的对账。
func Keywords() []string {
	out := make([]string, 0, len(keywords))
	for k := range keywords {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
