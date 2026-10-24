package compile

import "testing"

// output_message_side_effect_contract_test.go — SEM-S10-10 的语言级判据。
//
// 规范：`output(expr)` 是**执行中消息**，发布后控制流继续；它不终止流程，
// 也不参与流程出口值。这里不问平台 wire 形状，只问源文本在本地 runtime 上
// 的值与可达性。

// 证伪面：把 output 的值误当出口值 ⇒ 命中分支时得到 "进度" / "迭代:…"；
// 把 output 当终止点 ⇒ 后面的 `return x` 不执行，同样得不到 KEEP。
// 两个相位（命中 / 不命中）同时防止「永远忽略 return」的假绿。
const outputMessageSideEffect = `@language nodecoda/1
@mode workflow

function main(array<string> xs, string x, bool announce) -> string {
    if (announce) {
        output("进度");
    }
    for (item in xs) {
        output("迭代:" + item);
    }
    return x;
}
`

func TestOutputMessageDoesNotTerminateOrBindExit(t *testing.T) {
	runConstructDiff(t, "output_message_side_effect", outputMessageSideEffect, []diffCase{
		{"命中 output 后继续到 return", map[string]any{
			"xs": []any{"a", "b"}, "x": "KEEP", "announce": true,
		}, "KEEP"},
		{"不命中 output 也返回同一出口值", map[string]any{
			"xs": []any{}, "x": "KEEP", "announce": false,
		}, "KEEP"},
	})
}

// outputOnly 是「没有 return」的相位：按 N2，流程出口值的唯一载体是
// return；只有 output 的流程是 void，output 的文本不得成为结构化结果。
const outputOnly = `@language nodecoda/1
@mode workflow

function main(string x) {
    output("only");
}
`

func TestOutputOnlyHasNoStructuredResult(t *testing.T) {
	runConstructDiff(t, "output_only", outputOnly, []diffCase{
		{"output 不充当出口值", map[string]any{"x": "ignored"}, nil},
	})
}

// advanced-chat 相位：同一段 output-only 源在 @mode advanced-chat 下，消息
// 就是该回合的答复（chatflow 的回合语义），所以绑定必须保留。这条同时是
// 上面 spec 过滤的**负控**：若把 N2 过滤做成对所有模式无条件生效，这里
// 立即红 —— 模式的差别是被判的，不是注释里的。
const outputOnlyAdvancedChat = `@language nodecoda/1
@mode advanced-chat

function main(string query) {
    output("only");
}
`

func TestAdvancedChatOutputRemainsTurnAnswer(t *testing.T) {
	checkValues(t, "output_only · advanced-chat", outputOnlyAdvancedChat, []valueCase{
		{name: "消息即该回合答复", in: map[string]any{"query": "ignored"}, want: "only"},
	})
}
