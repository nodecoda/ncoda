package ir

import (
	"strconv"
	"strings"
)

// 绑定名（binding label）= 编译器写进 wire 的那个名字：模板占位符的键
// （`{{name}}`）、`node_inputs[].name`、以及 python shim 的参数名，三处是
// **同一个名字**（同一个偏差会在三处同时显形）。
//
// 一条规则（无形态守卫）：**绑定名 = 语义锚 = 最终消费字段**（生产者的端口路径）。
// 不是源码局部变量名，也不是构造期的位置计数器。理由 = 可逆性（C∘R∘C）：
//
//   - 这条通路上 wire 只有 `{name, input.value:{path, ref_node}}`，**源码局部
//     变量名不在其中**；raising 按 (ref_node, path) 解析、不可能复原作者的变量名
//     ⇒ 拿源码名当绑定名，一次往返必然改名（实测：`{{value_1}}` → `{{body}}`、
//     `def main(html_text)` → `def main(v4_text)`）；
//   - 位置计数器（`var0` / `arg1` / `value_1`）同理：它的值由构造顺序给出，而构造
//     顺序不是 wire 事实。
//
// 端口名是 wire 事实（`input.value.path` 在两侧逐字相等）⇒ 以它为锚即得不动点。
// 平台侧名字是本地键（按 name 解析占位符，T1），改名不影响语义。
//
// 端口名可含 `.`（dotted record path），而占位符的**基名**在平台侧截到首个 `.`
// （T1 证据：2866/2930）⇒ 分隔符必须换成 `_`，否则 `{{data.content}}` 会被读成
// 基名 `data`（与别的输入撞键）。
func bindingLabel(port string) string {
	if port == "" {
		return "output"
	}
	var b strings.Builder
	for _, r := range port {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if s == "" {
		return "output"
	}
	if s[0] >= '0' && s[0] <= '9' {
		s = "_" + s // 不得以数字开头（python 形参 / 标识符）
	}
	return s
}

// takeBindingLabel 分配一个绑定名：以语义锚为基名，在**同一节点的绑定集合内**
// 取最小可用序号（`name` / `name_1` / `name_2`…）。
//
// 唯一性是平台要求（T1：占位符按 name 解析 ⇒ 键必须单射；语料非空重名 0 例，
// 平台自身用 `_N` 后缀消歧 43 例），序号约定与结局侧的 `dedupeInputNames`
// 逐字相同、且**同源同序**（文档序 = 绑定序）⇒ 两处不会错位。
func takeBindingLabel(bindings *[]textBinding, port string) string {
	return takeBindingLabelReserved(bindings, port, nil)
}

// takeBindingLabelReserved 是同一个分配器，外加一个**保留名集合**：该节点文本里
// 已经出现的、**不属于我们的**占位符名（作者手写的字面 `{{x}}`）。
//
// 为什么必须避开（C2-H4，一手实测）：平台按 name 解析 `{{name}}`，而作者的字面
// 占位符与我们的引用在 wire 上**同形**。取同一个名字 = 作者写的字面文本被我们的
// 引用**接管**（`a={{q}}` 静默变成引用值），零诊断。作者那一侧不能在语言层退让
// （他的 `{{q}}` 是文本，平台的模板语法又没有转义），所以让位的是我们的名字。
//
// 无形态守卫：保留名来自**同一个模板槽的字面文本**，与端口名/节点类型/表达式
// 形状无关（见 internal/backend/cozebiz/template_names.go 的另一半：渲染期合成
// 的绑定名遵守同一条规则）。
func takeBindingLabelReserved(bindings *[]textBinding, port string, reserved map[string]bool) string {
	base := bindingLabel(port)
	used := make(map[string]bool, len(*bindings)+len(reserved))
	for name := range reserved {
		used[name] = true
	}
	for _, b := range *bindings {
		used[b.name] = true
	}
	if !used[base] {
		return base
	}
	for k := 1; ; k++ {
		cand := base + "_" + strconv.Itoa(k)
		if !used[cand] {
			return cand
		}
	}
}
