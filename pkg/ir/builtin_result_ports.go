package ir

import "github.com/nodecoda/ncoda/pkg/builtin"

// http() 结果端口的**唯一声明点**（IR 侧）。
//
// 一个内建节点的结果端口词汇表有两层权威，不可混用：
//
//	名字对应（口名 ⇄ 语言字段名） = 内建节点结果契约 pkg/builtin
//	  （声明源：开放侧 examples/builtins/<target>.yaml；闭源侧由商业仓注入）；
//	语言类型（语言字段名 → IR 中性类型） = 语言原型，即本文件。
//
// 为什么不把契约的 `ports[].type` 当语言类型用：契约里的 type 是**平台物理
// 类型**（coze-biz 把 headers 声明成 `string`，见 builtins/coze-biz-1.0.yaml），
// 而语言原型 HTTPResponse 的 headers 是 `map<string, any>`。把物理类型回流进
// IR 有实测后果（build_condition.go 的 *HTTPCall 注释）：同一语义在
// 「糖展开 `return v`」与「显式 `return {f: v.f, …}`」两条路径上得出不同的 wire。
//
// 本文件存在的理由 = **消灭副本**：同一个 http 结果口词汇表此前在
// portTypeOfNode（build_condition.go）与 declaredPortType（junction_phi.go）
// 各写一份，且两份**已经分歧**（前者 headers = map、后者 headers = string，
// 后者是平台物理类型的回流）⇒ 同一个 (节点, 端口) 有两个答案。一处声明后，
// 两份消费端读同一条规则，分歧不可能再发生；字段集与契约的一致性由
// builtin_result_port_gate_test.go 对账（新增/删除字段不接线 ⇒ 红）。

// httpResultPortType 返回 http 结果端口的**语言层**类型。
//
// 接受**口名或语言字段名**两种拼写（IR 的端口词汇表历史上两种名都出现过：
// `statusCode` 是契约的口名、`status_code` 是契约的语言名）。归一化走契约，
// 不按名字形态猜（蛇形化是启发式、语料无背书，见 pkg/builtin 包注）。
func httpResultPortType(output string) (*Type, bool) {
	lang, ok := httpResultLangField(output)
	if !ok {
		return nil, false
	}
	switch lang {
	case "body":
		return T("string"), true
	case "status_code":
		return T("integer"), true
	case "headers":
		return TObject(map[string]*Type{}), true
	}
	// 契约声明了新字段但本文件没给语言类型 ⇒ 返回 false（fail loud：
	// 调用方按其既有缺省路径走），由门禁测试在提交前拦下。
	return nil, false
}

// httpResultLangField 把 http 结果端口的**口名或语言名**归一到契约的语言字段名。
// 未声明 ⇒ false（不产生新名字，调用方保留既有行为）。
func httpResultLangField(name string) (string, bool) {
	for _, lang := range builtin.LangFields(IRPortVocabularyTarget(), "http") {
		if name == lang {
			return lang, true
		}
	}
	return builtin.LangOf(IRPortVocabularyTarget(), "http", name)
}

// httpResultPortName 返回 http 结果的**语言字段名**对应的 IR 端口名
// （= 契约里的平台物理口名；IR 的端口词汇表口径见 IRPortVocabularyTarget）。
// 契约未声明 ⇒ 原样返回（不发明名字，与 pkg/builtin 的缺省语义一致）。
func httpResultPortName(lang string) string {
	if p, ok := builtin.PortOf(IRPortVocabularyTarget(), "http", lang); ok {
		return p.Port
	}
	return lang
}
