package compile

import "testing"

// answer_endpoint_value_contract_test.go — SEM-S10-12 的语言级判据。
//
// 规范：`return template "<逐字模板>" [using { 名额: 值路径, … }]` 是**流程
// 答复端点**的值侧：模板按名额渲染成最终答复文本，并终止流程。它是 `string`
// 值（标准 §10 / langref 的 answer 构造），不是平台 wire 形状。

// 证伪面：
//   - 名额不参与渲染 ⇒ 输出仍含 "{{n}}"（或只得到 x 本身）；
//   - 模板被当普通表达式/具名返回 ⇒ 得到 x 而不是 "hi Bob"；
//   - 无名额的纯文本模板 ⇒ 必须逐字成为答复。
const answerWithSlot = `@language nodecoda/1
@mode workflow

function main(string x) {
    return template "hi {{n}}" using { n: x };
}
`

const answerLiteralOnly = `@language nodecoda/1
@mode workflow

function main(string x) {
    return template "已处理完成";
}
`

func TestReturnTemplateRendersAnswerSlot(t *testing.T) {
	runConstructDiff(t, "answer_slot", answerWithSlot, []diffCase{
		{"名额渲染", map[string]any{"x": "Bob"}, "hi Bob"},
		{"空名额值仍终止", map[string]any{"x": ""}, "hi "},
	})
}

func TestReturnTemplateLiteralIsAnswer(t *testing.T) {
	runConstructDiff(t, "answer_literal", answerLiteralOnly, []diffCase{
		{"无名额模板逐字承载", map[string]any{"x": "ignored"}, "已处理完成"},
	})
}
