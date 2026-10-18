package ir_test

// 常量配置槽的统一尺（2026-09-24，用户问「ncoda 里还有带动态语义的语法元素吗」的
// 一手普查收口）。
//
// 规则（与 http method 槽同源，`http_arg_slot_contract_test.go` §method）：
// **平台侧是配置常量（非模板）的调用槽，只接受编译期字符串；解析失败 ⇒ fail loud，
// 绝不用平台默认值兜底。**
//
// 为什么：这些槽进 wire 后是常量字段（llm `modelType/modelName` 是
// `input: {type: string, value: ...}`；knowledge `datasetParam.datasetList` 是 id 列表
// 字面量），运行期表达式在平台上**没有承载面**。收下它只能换来一个假产物 ——
// 作者以为 `llm(m, {...})` 用了变量 m，产物却用平台默认模型（实测 modelType="1" /
// modelName=""，m 在产物里**一个字都没有**），且零诊断。
//
// 修复前 RED 复现（CLI 一手）：
//
//	printf '@language nodecoda/1\n@mode workflow\nfunction main(string m) -> string {\n
//	  let r = llm(m, { "messages": [{ "role": "user", "content": "hi" }] });\n  return r.text;\n}\n' \
//	  | nclang-compile -target coze-biz-1.0        # ⇒ SUCCEEDED（modelType 1 / modelName ""）
//	printf '... knowledge(m, "what is ncoda") ...' | nclang-compile -target coze-biz-1.0
//	  # ⇒ SUCCEEDED 且产物**没有 datasetParam**（检索面全空）

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

const constantConfigSlotHeader = workflowHeader + `
type Hit = {
  string content;
}

const M = "coze:1737521813";
const DS = "ds-001,ds-002";
`

// 正面锚：字面量与 const 在三个槽上都照旧解出（行为不变）。
func TestConstantConfigSlotsResolveLiteralsAndConsts(t *testing.T) {
	src := constantConfigSlotHeader + `
function main(string q) -> string {
    let a = llm("coze:1737521813", { "messages": [ { "role": "user", "content": q } ] });
    let b = llm(M, { "messages": [ { "role": "user", "content": q } ] });
    let k = knowledge(DS, q);
    return a.text + b.text;
}
`
	nodes := nodesOf(buildIR(t, src))
	llms := nodes[ir.KindLLM]
	if len(llms) != 2 {
		t.Fatalf("want 2 llm nodes, got %d", len(llms))
	}
	for i, n := range llms {
		if got := n.(*ir.LLMCall).Model; got != "coze:1737521813" {
			t.Fatalf("llm[%d] model = %q, want the literal/const value", i, got)
		}
	}
	ks := nodes[ir.KindKnowledge]
	if len(ks) != 1 {
		t.Fatalf("want 1 knowledge node, got %d", len(ks))
	}
	if got := ks[0].(*ir.KnowledgeCall).DatasetNames; len(got) != 2 {
		t.Fatalf("knowledge datasets = %v, want the two const-declared names", got)
	}
}

// 反面锚：三个槽写运行期值都必须 fail loud（今天是静默兜底）。
func TestConstantConfigSlotsFailLoudOnRuntimeValues(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		expect string
	}{
		{
			name: "llm model",
			body: `    let r = llm(m, { "messages": [ { "role": "user", "content": q } ] });
    return r.text;`,
			expect: "model",
		},
		{
			name: "extract model",
			body: `    let r = extract<Hit>(m, q, { "prompt": "p" });
    return r.value.content;`,
			expect: "model",
		},
		{
			name: "knowledge dataset ids",
			body: `    let k = knowledge(ids, q);
    return "x";`,
			expect: "dataset",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := constantConfigSlotHeader + `
function main(string q, string m, string ids) -> string {
` + tc.body + `
}
`
			msgs := buildIRErrs(t, src)
			if !containsAny(msgs, tc.expect) {
				t.Fatalf("runtime value in the %s slot must fail loud (want a diagnostic naming %q), got: %v",
					tc.name, tc.expect, msgs)
			}
		})
	}
}
