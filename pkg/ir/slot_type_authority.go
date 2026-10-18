package ir

// slot_type_authority.go —— 「汇聚槽的类型由谁说了算」= **一处实现**
// （`docs/five-step/spec/J-join-canonical-form.md` §31；J §9.3 步骤 3 的算法面）。
//
// 为什么需要它：同一条权威链今天有两份实现 —— 循环状态槽（`build_stmt.go`）与
// φ 槽（`junction_phi.go::junctionPhiType`），而且**失败行为不同**：循环侧全缺
// ⇒ fail loud（RS-LOOP-002 先例）；φ 侧全缺 ⇒ 静默 `continue` ⇒ 不建组 ⇒ 零候选
// junction ⇒ 该臂写值静默消失（下游读单臂 producer，L1/L2 全绿）。
//
// 规则（一条，无形态守卫）：「槽的类型必须来自**声明**；三级都拿不到 ⇒ **fail loud**」。
// 静默兜底（伪造 `string`）与静默跳过（不建组）都是 AGENTS.md 明令禁止的
// 「推断型静默 fallback / 静默丢弃」形态。
//
// 宿主差异只能是**数据**（J §4）：每个宿主送进来的只是**它自己的级别序列**
// —— 顺序即优先级，判定与失败形态在下面两个函数里各一次。

import (
	"fmt"
	"strings"
)

// slotTypeLevel 是权威链上的**一级**：一个有名字的、惰性求值的类型来源。
//
// `name` 只进诊断（「试过：声明 / 字面量 / 生产者口的声明类型」），不参与判定；
// `typ` 为 nil 表示宿主没有这一级（例如无名目标的值位置三元没有赋值目标类型）。
type slotTypeLevel struct {
	name string
	typ  func() *Type
}

// firstSlotType 返回权威链上**第一个给出类型**的级别；全缺 ⇒ nil。
//
// 这是链的**唯一**判定：按序求值、首个非 nil 胜出。禁止在调用点再写一遍顺序
// （那正是「同一规则两份实现」的出生形态）。
func firstSlotType(levels []slotTypeLevel) *Type {
	for _, lv := range levels {
		if lv.typ == nil {
			continue
		}
		if t := lv.typ(); t != nil {
			return t
		}
	}
	return nil
}

// missingSlotTypeError 是「整条链都没给出类型」的**唯一**失败形态：fail loud，
// 诊断里点名构造、槽名与**试过的级别**（作者与维护者都能一眼看出缺哪一级）。
//
// 与循环侧既有先例同形（`loop state %q: no authoritative type …`）：同一规则
// 的一个失败形态，不是两套消息。
func missingSlotTypeError(construct, slot string, levels []slotTypeLevel) error {
	return &UnsupportedError{Construct: fmt.Sprintf(
		"%s %q: no authoritative type (tried: %s — all missing)", construct, slot,
		levelNames(levels))}
}

// unspellableSlotNameError 是同一把尺的另一半（spec `J §9.3` R2 的第二项）：
// **槽成立的前提不满足 ⇒ fail loud**。槽名在平台上不可拼写成端口名（不是标识符）
// 时，今天的处置是静默跳过 —— 与「槽形状取不到」同一后果：不建组 ⇒ 零候选
// ⇒ 该臂写值静默消失（编译 SUCCEEDED、L1/L2 全绿）。
//
// 修法**不是**虚构一个端口名（名字有界：反编译侧要按名字恢复同一个变量），
// 而是拒绝：这个产物无法承载作者写的语义。
func unspellableSlotNameError(construct, slot string) error {
	return &UnsupportedError{Construct: fmt.Sprintf(
		"%s %q: not spellable as a platform port name (identifier required)", construct, slot)}
}

// levelNames 把级别序列渲染成诊断里的「试过：a / b / c」——顺序即优先级，
// 诊断与判定同源（同一 slice，不是两处各写一遍）。
func levelNames(levels []slotTypeLevel) string {
	names := make([]string, 0, len(levels))
	for _, lv := range levels {
		if lv.name != "" {
			names = append(names, lv.name)
		}
	}
	return strings.Join(names, " / ")
}
