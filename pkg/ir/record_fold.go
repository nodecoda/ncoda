package ir

import (
	"github.com/nodecoda/ncoda/pkg/ast"
)

// ── 记录整值折叠（J §42.3 A 轴）─────────────────────────────────────────────
//
// 正本 = `docs/five-step/spec/J-join-canonical-form.md` §42.3（规范形两条轴）与 §42.5（实现切点）；
// 账单输入 = `pkg/ir/usage_prescan.go`（**物化前**的使用面预扫）。
//
// 规则（A 轴原文）：**记录类型的可变本地，若整值语义不可观察 ⇒ 不物化容器**，取「逐字段单元」形态；
// 否则保持整值记录（R-13）。
//
// 前置（§42.3 A 轴 P1–P4 + **S1/S2 形状前置**；2026-09-26 起两条形状前置已写进正本，不再是本刀私注）：
//
//	P1 全部消费者是成员路径（整值口无消费者）   —— 账单 `MemberOnlyReads()`
//	P2 写全是记录字面量（或成员写）             —— 账单 `AllWritesAreLiteralOrMember()`
//	P3 字段表达式只求值一次                    —— **变换侧义务**：字段值表达式原样成为单元值，求值一次
//	P4 无别名                                 —— P1 的推论
//	S1 记录**只有一个**字段                     —— 单元名 = 源变量名（C 轴第一优先，E1/E2 已证 raise 原样回收）
//	S2 该字段是**叶**类型（无 Props / 无 Elem）  —— 嵌套容器由它自己那一层决定
//
// **不做**（登记 = `docs/five-step/residuals.yaml#R-92`；退出条件 = 正本 §42.5「前置 / 拒绝」）：
//   - 多字段记录（逐字段单元的**命名**属 C 轴未决项：源变量名只有一个，本刀不猜）；
//   - 叶字段本身是容器（嵌套记录 / 列表）；
//   - Optional 记录（折掉叶单元会丢 `T?` 标记）；
//   - 成员写目标（`r1.slot = e`）—— IR 今天对一切成员写目标 fail loud（`assign to non-identifier`，
//     独立于本刀的既有缺口）。
//
// 判据（撤实现必红）= `pkg/ir/record_fold_contract_test.go` ∪
// `pkg/ir/usage_prescan_test.go`（账单半）；端到端 = 生成器 `file_family` 族往返（`§42.4 E3`）。

// recordFold 决定（并缓存）一个可变记录本地是否折成叶单元，返回叶单元承载的字段名与字段类型。
// declType 是调用方已解析的声明类型（nil = 从已登记声明类型回查）。
func (g *Builder) recordFold(name string, declType *Type) (field string, fieldType *Type, ok bool) {
	if name == "" {
		return "", nil, false
	}
	if g.foldDecided[name] {
		f, folded := g.foldedRecordFields[name]
		if !folded {
			return "", nil, false
		}
		return f, g.foldedRecordTypes[name], true
	}
	g.foldDecided[name] = true
	//@guard-exempt: 2026-09-24 —— ① 本行是**声明类型的回查**（Type 轴），不是「仅当 X 形态才…」的
	// 适配守卫：它只是把调用方没给的类型从已登记表里取回来，不看任何平台/节点形态。
	// 退出条件 = 调用方一律带上 declType 后本回查整体删除。
	shape := declType
	if shape == nil {
		if t := g.varDeclTypes[name]; t != nil {
			shape = t
		} else if t := g.hoistedTypes[name]; t != nil {
			shape = t
		}
	}
	//@guard-exempt: 2026-09-24 —— ② 本行**是**形态前置（A 轴 S1「记录恰一个字段」+ S2「该字段是叶」），
	// 正本 = `docs/five-step/spec/J-join-canonical-form.md` §42.3 / §42.5。之所以仍算**正当**：
	// 判定对象是**语言层类型**（Type 轴，非平台/节点特判），且方向是 **fail-safe 默认不折**
	// （不满足 ⇒ 保持 `R-13` 整值记录，不静默降级、不猜）。S1 蕴含 P1–P4，故这里就是 A 轴的**全部**前置。
	// 退出条件 = A 轴前置扩容到多字段（C 轴「逐字段单元怎么命名」落地；登记 = `docs/five-step/residuals.yaml#R-92`）时，本行按新前置重写。
	if shape == nil || shape.Kind != "object" || len(shape.Props) != 1 {
		return "", nil, false
	}
	for k, v := range shape.Props {
		field, fieldType = k, v
	}
	// S2：字段是叶类型。
	if field == "" || fieldType == nil || fieldType.Props != nil || fieldType.Elem != nil {
		return "", nil, false
	}
	// P1 ∧ P2：整值语义不可观察（账单人判；无账单 = 不折，fail-safe）。
	if u := g.usages[name]; u == nil || !u.UsageAllowsWholeValueFold() {
		return "", nil, false
	}
	g.foldedRecordFields[name] = field
	g.foldedRecordTypes[name] = fieldType
	return field, fieldType, true
}

// recordLiteralFieldValue 取出记录字面量里某个字段的值表达式。
// 键按语言规则解析（`StaticMapKey`：标识符 = 标签，字符串 = 字面值；别的形态 = 动态键，不匹配）。
func recordLiteralFieldValue(ml *ast.MapLit, field string) (ast.Expr, bool) {
	if ml == nil {
		return nil, false
	}
	for _, e := range ml.Entries() {
		key, ok := ast.StaticMapKey(e.Key)
		if !ok || key != field {
			continue
		}
		return e.Value, true
	}
	return nil, false
}
