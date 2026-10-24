package compile

import (
	"context"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
)

//@feature llm-unified-envelope
//@feature open-object

const llmV2Source = `@language nodecoda/1
@mode workflow
type Answer = { string answer; int score; };
function main(string prompt) -> string {
  let r = llm<Answer>("model", {"messages": [{"role":"user", "content":prompt}]});
  return r.text.answer;
}`

const llmV2EnvelopeSource = `@language nodecoda/1
@mode workflow
type Answer = { string answer; int score; };
function main(string prompt) {
  let r = llm<Answer>("model", {"messages": [{"role":"user", "content":prompt}]});
  return r;
}`

type structuredModel struct{ calls int }

func (m *structuredModel) Chat(context.Context, string, []nodes.Message) (string, error) {
	m.calls++
	return `{"answer":"decoded","score":7}`, nil
}

func TestLLMV2CompileAndRunStructuredMember(t *testing.T) {
	s := qaLoopFromSource(t, llmV2Source)
	model := &structuredModel{}
	r := mustCompile(t, New(nodes.StdRegistry(), WithDeps(nodes.Deps{Models: model})), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"prompt": "go"}))
	if out["output"] != "decoded" {
		t.Fatalf("output = %#v", out)
	}
	if model.calls != 1 {
		t.Fatalf("model calls = %d, want 1", model.calls)
	}
}

func TestLLMV2CompileAndRunWholeEnvelope(t *testing.T) {
	s := qaLoopFromSource(t, llmV2EnvelopeSource)
	model := &structuredModel{}
	r := mustCompile(t, New(nodes.StdRegistry(), WithDeps(nodes.Deps{Models: model})), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"prompt": "go"}))
	envelope, ok := out["output"].(map[string]any)
	if !ok {
		t.Fatalf("output = %#v, want normalized LLM response envelope", out)
	}
	text, ok := envelope["text"].(map[string]any)
	if !ok || text["answer"] != "decoded" || text["score"] != int64(7) {
		t.Fatalf("envelope text = %#v", envelope["text"])
	}
	for _, field := range []string{"reasoning_content", "usage", "finish_reason"} {
		if value, exists := envelope[field]; !exists || value != nil {
			t.Fatalf("envelope[%q] = %#v, exists=%v; want literal nil", field, value, exists)
		}
	}
	if model.calls != 1 {
		t.Fatalf("model calls = %d, want 1", model.calls)
	}
}
