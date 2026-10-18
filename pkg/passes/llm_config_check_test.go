package passes

// ANY-SHRINK L1-a 回归测试: llm() config 已知键显式结构化 + Optional 化。
//
// 已知键类型表（语料实证 + 统一 LLM 语义契约）:
//   temperature/top_p → float   （int 字面量经 IsAssignable 拓宽通过）
//   max_tokens/timeout_ms → int
//   model/systemPrompt/userPrompt/reasoning_format → string
//   stream → bool
//   messages → array（元素 role/content 必须 string, 保留 checkLLMMessageContents）
//
// 可选性: 键缺失合法（OptionalType 表达）; 未知键保持开放（map<string,any>, L2 边界）。
// 验收: 语料全量无回归（rt-matrix compile_ok 577/581 基线不变）。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/diag"
)

// runLLMConfigTypeCheck 编译含 llm() config 的源, 返回 TYPE_MISMATCH 错误条数。
func runLLMConfigTypeCheck(t *testing.T, body string) int {
	t.Helper()
	src := `@language nodecoda/1
@mode workflow
function main() -> string {
` + body + `
    return "";
}`
	res := DefaultPipeline().Run(src, "llm-config-check.ncoda", nil)
	n := 0
	for _, d := range res.Diagnostics {
		if d.Severity == diag.Error && d.Code == diag.TYPE_MISMATCH {
			n++
		}
	}
	return n
}

func TestLLMConfigKnownKeyTypes(t *testing.T) {
	// 每个已知键: 合法值 0 错, 非法值 >0 错。
	cases := []struct {
		name  string
		body  string // llm() config 调用体
		wantN int    // 期望 TYPE_MISMATCH 条数
	}{
		{"temperature-float", `let r = llm("m", { "temperature": 0.8 });`, 0},
		{"temperature-int-widen", `let r = llm("m", { "temperature": 1 });`, 0},
		{"temperature-string-bad", `let r = llm("m", { "temperature": "hot" });`, 1},
		{"max-tokens-int", `let r = llm("m", { "max_tokens": 16384 });`, 0},
		{"max-tokens-string-bad", `let r = llm("m", { "max_tokens": "16384" });`, 1},
		{"max-tokens-float-bad", `let r = llm("m", { "max_tokens": 4096.5 });`, 1},
		{"top-p-float", `let r = llm("m", { "top_p": 0.7 });`, 0},
		{"top-p-int-widen", `let r = llm("m", { "top_p": 1 });`, 0},
		{"timeout-ms-int", `let r = llm("m", { "timeout_ms": 600000 });`, 0},
		{"timeout-ms-bool-bad", `let r = llm("m", { "timeout_ms": true });`, 1},
		{"model-string", `let r = llm("m", { "model": "gpt-4o" });`, 0},
		{"system-prompt-string", `let r = llm("m", { "systemPrompt": "sys" });`, 0},
		{"user-prompt-string", `let r = llm("m", { "userPrompt": "usr" });`, 0},
		{"reasoning-format-string", `let r = llm("m", { "reasoning_format": "separated" });`, 0},
		{"reasoning-format-int-bad", `let r = llm("m", { "reasoning_format": 3 });`, 1},
		{"stream-bool", `let r = llm("m", { "stream": true });`, 0},
		{"stream-int-bad", `let r = llm("m", { "stream": 1 });`, 1},
		{"messages-string-content", `let r = llm("m", { "messages": [ { "role": "user", "content": "hi" } ] });`, 0},
		{"messages-int-content-bad", `let r = llm("m", { "messages": [ { "role": "user", "content": 42 } ] });`, 1},
		{"unknown-key-open", `let r = llm("m", { "custom_key": "anything" });`, 0},
		{"missing-keys-optional", `let r = llm("m", { "messages": [ { "role": "user", "content": "hi" } ] });`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runLLMConfigTypeCheck(t, tc.body)
			if got != tc.wantN {
				t.Errorf("llm config: got %d TYPE_MISMATCH, want %d (body=%s)", got, tc.wantN, tc.body)
			}
		})
	}
}

func TestLLMConfigMessagesRoleEnforced(t *testing.T) {
	// messages 元素 role 也必须是 string（与 content 同级）。
	if n := runLLMConfigTypeCheck(t, `let r = llm("m", { "messages": [ { "role": 7, "content": "hi" } ] });`); n == 0 {
		t.Error("messages role=int 应报 TYPE_MISMATCH")
	}
}
