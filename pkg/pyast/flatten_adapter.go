package pyast

import "strings"

// flatten_adapter.go —— **循环展平垫片**的规范形单源（2026-09-27）。
//
// 为什么需要它：ncoda 的**展平值循环**（`var x = for (item in xs) { yield <list>; };`）
// 在平台侧没有对应物 —— coze 的 loop 容器「每次迭代收集一个值」⇒ 收集到的是
// `list<list<T>>`，而平台**没有** flatten 键（一手：语料 452 个 loop/batch 容器的
// 参数键并集 = batchSize/concurrentSize/loopCount/loopType/node_inputs/node_outputs/
// variableParameters；coze-studio 工作流域 `flatten` 零命中）。⇒ lowering 必须自己
// 物化这一层展平，否则语义静默丢失（产物运行期返回嵌套数组，而 ncoda 语义是平的）。
//
// 载体 = 一个编译器自建的 python code 节点（`# ncoda:shim 1` 标记）：输入 = loop 的
// 收集口（嵌套），输出 = 语义类型（展平）。反编译端识别这个**规范形**后把垫片摘掉、
// 把消费者重指回循环（并还原成展平值循环的 `var` 拼写）——两端共用本文件的构造，
// 所以发射与识别不可能各说各话。
//
// 与 `flatten(xs)` 内建的分工：内建是**语言级**调用（签名 array<array<string>> →
// array<string>，元素带 str()，反解回 `flatten(xs)`）；本垫片是 lowering 的**结构性**
// 物化（元素类型保持 T，不 str()），反解回展平值循环本身。两者形态不同、可逆路径
// 不同，不得互相冒充（`mapShimListComp` 只认带 str() 的内建形态）。

const (
	// FlattenAdapterInputParam 垫片的入参名（= 平台 node_inputs 名）。
	FlattenAdapterInputParam = "items"
	// FlattenAdapterOutputPort 垫片的输出口名（= 单键 dict 的键）。
	FlattenAdapterOutputPort = "result"
	// 两个推导变量：外层组、内层元素（形态本身参与识别，不可随便改）。
	flattenAdapterGroupVar = "group"
	flattenAdapterItemVar  = "item"
)

// FlattenAdapterModule 构造垫片 python 模块的 AST —— 发射与识别的**唯一**构造。
func FlattenAdapterModule() *Module {
	return &Module{
		Params: []Param{{Name: FlattenAdapterInputParam, Type: "list"}},
		Body: []Stmt{&ReturnStmt{Value: &DictExpr{Items: []DictItem{{
			Key: &StringExpr{Value: FlattenAdapterOutputPort},
			Value: &ListCompExpr{
				Elt: &NameExpr{Name: flattenAdapterItemVar},
				Clauses: []CompClause{
					{Vars: []string{flattenAdapterGroupVar}, Iter: &NameExpr{Name: FlattenAdapterInputParam}},
					{Vars: []string{flattenAdapterItemVar}, Iter: &NameExpr{Name: flattenAdapterGroupVar}},
				},
			},
		}}}}},
	}
}

// FlattenAdapterSource 返回垫片的规范源码（含 shim marker，行尾无换行）。
func FlattenAdapterSource() string {
	return ShimMarker + trimTrailingNewline(Serialize(FlattenAdapterModule()))
}

// IsFlattenAdapterSource 判定一段 code 节点源码是否就是垫片的规范形。
//
// 判据 = 带 shim marker + 去掉 marker 后**解析再序列化**逐字等于规范形（两端同源：
// 规范形只有 FlattenAdapterModule 一个来源，识别只是问「是不是它」）。用户手写
// python（无标记）与其它 shim（形态不同）一律不命中。
func IsFlattenAdapterSource(code string) bool {
	if !hasShimMarker(code) {
		return false
	}
	m, err := ParseModule(strings.TrimSpace(trimShimMarker(code)))
	if err != nil {
		return false
	}
	return Serialize(m) == Serialize(FlattenAdapterModule())
}

func trimTrailingNewline(s string) string {
	if n := len(s); n > 0 && s[n-1] == '\n' {
		return s[:n-1]
	}
	return s
}

func hasShimMarker(code string) bool {
	return len(code) >= len(ShimMarker) && code[:len(ShimMarker)] == ShimMarker
}

func trimShimMarker(code string) string { return code[len(ShimMarker):] }
