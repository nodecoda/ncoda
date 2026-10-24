package nodes

// agent_knowledge_test.go — M19-2:agent 内置数据集检索工具。
//
// 对齐参考目标 agent-chat:knowledge 配置 → 数据集封装为内置检索工具注入
// prompt_messages_tools,模型自主调用;调用结果回填 observation。覆盖:
//   - 检索工具循环（调用 → 命中片段回填 → 最终答案）+ top_k 裁剪;
//   - 模型可见工具描述含 knowledge_retrieval;
//   - knowledge 配置但无检索后端 → Build 期 RNodeError（fail-fast）。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// TestAgentKnowledgeRetrievalToolLoop:模型调用内置检索工具 → 回填 → 最终答案。
func TestAgentKnowledgeRetrievalToolLoop(t *testing.T) {
	sk := &stubKnowledge{chunks: []KnowledgeChunk{
		{Content: "doc-about:北京天气", Title: "kb1", Score: 0.9},
		{Content: "doc-extra", Title: "kb2", Score: 0.5},
	}}
	fake := &fakeToolModel{results: []ChatResult{
		{
			Content:   "let me search",
			ToolCalls: []ToolCall{{ID: "c1", Name: "knowledge_retrieval", Arguments: `{"query":"北京天气"}`}},
		},
		{Content: "北京天气晴朗"},
	}}
	n := buildDeps(t, spec.KindAgent, &spec.AgentConfig{
		Model:     "m",
		Knowledge: &spec.AgentKnowledgeConfig{DatasetIDs: []string{"kb1"}, TopK: 1},
	}, Deps{Models: fake, Knowledge: sk})

	var steps []AgentStep
	an := n.(*agentNode)
	an.step = func(st AgentStep) { steps = append(steps, st) }

	out, err := n.Invoke(t.Context(), map[string]any{"Query": "北京天气?"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !strings.HasSuffix(out["output"].(string), "北京天气晴朗") {
		t.Fatalf("output = %v, want suffix 北京天气晴朗", out["output"])
	}
	// 检索轮 observation 含 top_k=1 裁剪后的首条命中（kb1,不含 kb2）。
	found := false
	for _, st := range steps {
		if st.ToolName == "knowledge_retrieval" {
			found = true
			if !strings.Contains(st.Observation, "doc-about:北京天气") {
				t.Fatalf("observation = %q, want doc-about:北京天气", st.Observation)
			}
			if strings.Contains(st.Observation, "doc-extra") {
				t.Fatalf("top_k=1 not applied: observation contains second chunk: %q", st.Observation)
			}
		}
	}
	if !found {
		t.Fatalf("no knowledge_retrieval step: %+v", steps)
	}
	// 数据集白名单透传到后端。
	if len(sk.lastIDs) != 1 || sk.lastIDs[0] != "kb1" {
		t.Fatalf("dataset ids = %v, want [kb1]", sk.lastIDs)
	}
	if sk.lastQuery != "北京天气" {
		t.Fatalf("query = %q, want 北京天气", sk.lastQuery)
	}
}

// TestAgentKnowledgeToolSpecVisible:模型收到的工具描述含内置检索工具。
func TestAgentKnowledgeToolSpecVisible(t *testing.T) {
	fake := &fakeToolModel{results: []ChatResult{{Content: "final"}}}
	n := buildDeps(t, spec.KindAgent, &spec.AgentConfig{
		Model:     "m",
		Knowledge: &spec.AgentKnowledgeConfig{DatasetIDs: []string{"kb1"}},
	}, Deps{Models: fake, Knowledge: &stubKnowledge{}})
	if _, err := n.Invoke(t.Context(), map[string]any{"Query": "q"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.got) == 0 {
		t.Fatal("model not called")
	}
	var names []string
	for _, s := range fake.got[0].tools {
		names = append(names, s.Name)
	}
	if !containsStr(names, "knowledge_retrieval") {
		t.Fatalf("tools = %v, want knowledge_retrieval", names)
	}
}

// TestAgentKnowledgeWithoutBackend:配置 knowledge 但无检索后端 → RNodeError。
func TestAgentKnowledgeWithoutBackend(t *testing.T) {
	cfg := &spec.AgentConfig{Model: "m", Knowledge: &spec.AgentKnowledgeConfig{DatasetIDs: []string{"kb1"}}}
	b, _ := json.Marshal(cfg)
	_, err := StdRegistry().Build(context.Background(), &spec.NodeSpec{ID: "n", Kind: spec.KindAgent, Config: b}, Deps{Models: &fakeToolModel{}})
	if err == nil || diag.CodeOf(err) != diag.RNodeError {
		t.Fatalf("err = %v, want RNodeError", err)
	}
}

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
