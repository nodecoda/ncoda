// Package builtin 是**内建节点结果契约**的唯一查询入口（数据源 = 注册表
// examples/coze-reference/builtins/*.yaml，编译产物 = contracts_gen.go）。
//
// # 它回答什么问题
//
// 内建函数（http / llm / code / knowledge …）在不同平台上物化为**平台原生
// 节点**，而语言层的结果原型（如共享原型 HTTPResponse）名字取自语言，平台
// 节点的物理口名取自平台——两者可以不同（status_code ⇄ statusCode）。
// 「语言字段 ⇄ 平台物理口」这条对应必须**声明一次、两侧共读**：
//
//	lowering（语言字段 → 平台口）：pkg/ir 建图 + 各后端渲染器
//	raising （平台口 → 语言字段）：platforms/*/decompile 反编译器
//
// 分成两份必然漂移。实测教训（2026-09-15）：同一事实曾散落 10 余处、
// 且**反向方向一份都没有** ⇒ 反编译把平台物理口名当语言字段名发出，
// 再编译 FIELD_NOT_FOUND、往返断裂。
//
// # 为什么不能"推断"
//
// 平台**不给**这个信息：coze-biz 的真实导出里 `type: http` 节点没有
// outputs/node_outputs 字段（② 语料 3/3）——端口词汇表是平台外部契约。
// 语言原型只给语言层的名字。所以对应关系只能声明，不能从 wire 推断，
// 也不能按名字形态猜（`statusCode` → `status_code` 的蛇形化是启发式，
// 语料无背书：平台口名并非都可由语言名形变得到）。
//
// # 缺省语义（不是兜底）
//
// 查询一律返回 (值, ok)。ok == false 只有一种含义：**该目标未声明该节点
// 的契约**。此时调用方各自保留其既有缺省路径（IR 建图保持端口名原样、
// 反编译不改名）——这是"没有声明"而非"猜了一个名字"，与禁止的
// 推断型静默 fallback 不同：这里不产生任何新的名字。
package builtin

// Port 是一个内建节点结果端口的声明：平台物理口名 ↔ 语言层字段名。
type Port struct {
	// Port 是平台物理口名（出现在 wire 的 path / node_outputs）。
	Port string
	// Lang 是语言层字段名（出现在 ncoda 源码的成员访问）。
	Lang string
	// Type 是平台物理口类型（string / integer / object …）。语言层的字段
	// 类型权威在结果原型（pkg/binding/symbol.go），不在此处。
	Type string
}

// NodeContract 是一个内建节点类型在某个目标平台上的结果契约。
type NodeContract struct {
	// Result 是语言层结果原型名（如 HTTPResponse）——交叉引用，不是权威。
	Result string
	// Ports 按语言字段的声明顺序排列。
	Ports []Port
}

// Contract 返回目标平台声明的内建节点契约；未声明返回 nil。
func Contract(target string) map[string]NodeContract { return contracts[target] }

// NodeOf 返回目标平台上某内建节点类型的契约；未声明返回零值 + false。
func NodeOf(target, nodeType string) (NodeContract, bool) {
	c, ok := contracts[target][nodeType]
	return c, ok
}

// PortsOf 返回目标平台上某内建节点类型的端口声明（按语言字段顺序）。
func PortsOf(target, nodeType string) []Port {
	c, ok := NodeOf(target, nodeType)
	if !ok {
		return nil
	}
	return c.Ports
}

// LangFields 返回目标平台上某内建节点类型的**语言层字段名**（声明顺序）。
func LangFields(target, nodeType string) []string {
	pts := PortsOf(target, nodeType)
	if len(pts) == 0 {
		return nil
	}
	out := make([]string, 0, len(pts))
	for _, p := range pts {
		out = append(out, p.Lang)
	}
	return out
}

// PortOf 把语言层字段名映射到平台物理口（lowering 方向）。
func PortOf(target, nodeType, lang string) (Port, bool) {
	for _, p := range PortsOf(target, nodeType) {
		if p.Lang == lang {
			return p, true
		}
	}
	return Port{}, false
}

// LangOf 把平台物理口映射回语言层字段名（raising 方向）。
//
// 语言字段名可能就是平台口名本身（如 plugin 族），此时返回 (port, true)
// ——那是**已声明**的恒等，不是"猜"。
func LangOf(target, nodeType, port string) (string, bool) {
	for _, p := range PortsOf(target, nodeType) {
		if p.Port == port {
			return p.Lang, true
		}
	}
	return "", false
}

// TypeOf 返回目标平台上某内建节点端口的**平台物理类型**。
func TypeOf(target, nodeType, port string) (string, bool) {
	for _, p := range PortsOf(target, nodeType) {
		if p.Port == port {
			return p.Type, true
		}
	}
	return "", false
}

// DeclaredTargets 返回已声明契约的目标名（有序，供门禁与诊断）。
func DeclaredTargets() []string {
	out := make([]string, 0, len(contracts))
	for t := range contracts {
		out = append(out, t)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// TypeByPortOrLang 按**平台口名或语言字段名**查平台口类型。
//
// 为什么两种都收：IR 的端口词汇表历史上两种名都出现过（IR 端口取自契约的
// 平台口名，但语言字段名也曾直接进入 IR 的若干路径）。本函数不改变既有接受面，
// 只把「接受哪两个名字」从各处的 switch 收敛到契约这一处。
func TypeByPortOrLang(target, nodeType, name string) (string, bool) {
	if t, ok := TypeOf(target, nodeType, name); ok {
		return t, true
	}
	if p, ok := PortOf(target, nodeType, name); ok {
		return p.Type, true
	}
	return "", false
}
