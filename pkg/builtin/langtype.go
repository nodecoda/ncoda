package builtin

// langtype.go —— 「平台物理口 → **语言字段类型**」的查询实现（raising 方向）。
//
// # 为什么类型要单独一条查询，而不复用契约表的 Type
//
// 契约表（contracts_gen.go / builtins/*.yaml）里的 `Type` 按定义是**平台物理口
// 类型**（见 builtin.go 的 Port 注释：`http.headers = string`，② 语料 3/3 逐字
// 如此）。同一字段两侧的类型可以不同——coze-biz 的 `headers` 物理口声明
// `string`，而语言层 `HTTPResponse.headers` 是 `map<string, any>`。两者的桥
// 是**契约表的对应关系**（物理口 ↔ 语言字段名），桥的另一端类型来自语言原型。
//
// # 权威归属（不是"再声明一处"）
//
// 语言层字段类型的唯一权威 = 语言结果原型 `pkg/binding/symbol.go`。
// 本文件**读**它（经 binding 的符号链解析内建函数签名），不复制、不推断；
// 契约键（平台节点类型）与语言内建函数名同拼写，这条恒等由门禁核对
// （见 contract_prototype_test.go：契约每个键都必须有同名内建函数，且字段集一致）。
//
// # 为什么不能取 wire 的 node_outputs 类型
//
// 那是平台物理声明。把它当语言类型用，会在一轮往返后
// `TYPE_MISMATCH: Cannot assign Type(map, …) to Type(string)`（A12-H13 实测）。
//
// # 缺省语义（不是兜底）
//
// 查询一律返回 (值, ok)。ok == false 只有一种含义：**该目标 / 节点 / 端口未声明，
// 或语言原型里没有该字段**。此时调用方保留其既有路径——这是"没有声明"，
// 与"猜了一个类型"不同：这里不产生任何新的类型。

import (
	"sync"

	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/types"
)

var (
	protoOnce  sync.Once
	protoChain *binding.Chain
)

// protoSymbols 惰性构造语言原型的符号链（内建函数签名，权威在
// pkg/binding/symbol.go）。全过程只读：RegisterBuiltins 只写这张私有链。
func protoSymbols() *binding.Chain {
	protoOnce.Do(func() {
		protoChain = binding.NewChain()
		binding.RegisterBuiltins(protoChain)
	})
	return protoChain
}

// ResultType 返回语言原型里内建函数 name 的**结果类型**（record / array / …）。
// 未注册 / 无结果类型 ⇒ (nil, false)。
//
// 契约键与语言内建函数名同拼写（http / knowledge）；两者不一致时本查询
// 返回 false（调用方保留既有路径），并由门禁把"不一致"判红——不在运行时猜。
func ResultType(name string) (*types.Descriptor, bool) {
	sym := protoSymbols().ResolveSymbol(name)
	if sym == nil || sym.TypeInfo == nil || sym.TypeInfo.Kind != types.Function {
		return nil, false
	}
	ret := sym.TypeInfo.Result
	if ret == nil {
		return nil, false
	}
	return ret, true
}

// ResultRecordAgrees 返回 (target, nodeType) 的**语言结果 record** —— 仅当
// **两个权威描述同一个语言类型**时成立：
//
//	契约表  该 (target, nodeType) 声明的语言字段集 L（平台物理口 ↔ 语言字段名）
//	语言原型 该内建函数的结果 record R 的字段集
//
// 判据 = L == R 的字段集（元素级）。这是"两处声明同一件事"的一致性检查，
// **不是**形状守卫：任一权威单独变化（契约加一个口 / 原型改一个字段）都会让
// 判据翻转，由门禁（contract_prototype_test.go）报红。
//
// 不一致 ⇒ (nil, false)：不猜（default deny）。实例 = `knowledge`（契约声明的
// 语言字段是 `result`，而语言原型 `knowledge()` 的结果是数组 ⇒ 两者不是同一个
// 类型，不咨询原型）。调用方保留其既有路径。
func ResultRecordAgrees(target, nodeType string) (*types.Descriptor, bool) {
	langs := LangFields(target, nodeType)
	if len(langs) == 0 {
		return nil, false
	}
	rec, ok := ResultType(nodeType)
	if !ok || rec.Kind != types.Record || len(rec.Fields) != len(langs) {
		return nil, false
	}
	have := make(map[string]bool, len(rec.Fields))
	for _, f := range rec.Fields {
		have[f.Name] = true
	}
	for _, l := range langs {
		if !have[l] {
			return nil, false
		}
	}
	return rec, true
}

// LangTypeOf 返回 (target, nodeType) 契约里某**平台物理口**对应的
// **语言字段类型**描述符：物理口 --契约表--> 语言字段名 --原型--> 语言类型。
func LangTypeOf(target, nodeType, port string) (*types.Descriptor, bool) {
	lang, ok := LangOf(target, nodeType, port)
	if !ok {
		return nil, false
	}
	rec, ok := ResultRecordAgrees(target, nodeType)
	if !ok {
		return nil, false
	}
	for _, f := range rec.Fields {
		if f.Name == lang {
			return f.Type, true
		}
	}
	return nil, false
}
