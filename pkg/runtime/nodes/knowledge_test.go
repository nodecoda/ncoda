package nodes

// knowledge_test.go — M14 RAG:knowledge 节点语义测试。
//
// 覆盖:注入后端的 outputList 输出（content/title/score）、query 透传、
// dataset IDs 透传、未注入后端的编译期 RKindRejected。

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

var errKnowledgeBoom = errors.New("retrieval backend exploded")

type stubKnowledge struct {
	lastQuery string
	lastIDs   []string
	chunks    []KnowledgeChunk
	err       error
}

func (s *stubKnowledge) Retrieve(ctx context.Context, query string, datasetIDs []string) ([]KnowledgeChunk, error) {
	s.lastQuery = query
	s.lastIDs = datasetIDs
	return s.chunks, s.err
}

func knowledgeSpec(datasetIDs []string, queryName string) *spec.NodeSpec {
	raw, _ := json.Marshal(&spec.KnowledgeConfig{
		DatasetIDs: datasetIDs,
		Inputs:     []spec.Source{{Path: []string{"Query"}, BlockID: "100001", Name: "query"}},
		QueryName:  queryName,
	})
	return &spec.NodeSpec{ID: "100002", Kind: spec.KindKnowledge, Config: raw}
}

func TestKnowledgeInvokeOutputList(t *testing.T) {
	sk := &stubKnowledge{chunks: []KnowledgeChunk{
		{DocumentID: 1001, Content: "doc-a", Title: "t1", Score: 0.9},
		{DocumentID: 1002, Content: "doc-b", Title: "t2", Score: 0.5},
	}}
	impl, err := (Knowledge{}).Build(context.Background(), knowledgeSpec([]string{"ds-1"}, "query"), Deps{Knowledge: sk})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	out, err := impl.Invoke(context.Background(), map[string]any{"Query": "hello"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if sk.lastQuery != "hello" {
		t.Errorf("query = %q, want hello", sk.lastQuery)
	}
	if len(sk.lastIDs) != 1 || sk.lastIDs[0] != "ds-1" {
		t.Errorf("dataset ids = %v, want [ds-1]", sk.lastIDs)
	}
	list, ok := out["outputList"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("outputList = %#v, want 2 chunks", out["outputList"])
	}
	first := list[0].(map[string]any)
	// 元素字段集 = 平台事实 {documentId, output}（K-H2 收口）。
	if first["documentId"] != int64(1001) || first["output"] != "doc-a" {
		t.Errorf("first chunk = %#v, want {1001 doc-a}", first)
	}
	if _, exists := first["content"]; exists {
		t.Errorf("旧字段 content 必须退场（无平台承载），got %#v", first)
	}
}

func TestKnowledgeBuildRejectsWithoutBackend(t *testing.T) {
	_, err := (Knowledge{}).Build(context.Background(), knowledgeSpec(nil, ""), Deps{})
	if err == nil {
		t.Fatal("expected RKindRejected without a retrieval backend")
	}
	if diag.CodeOf(err) != diag.RKindRejected {
		t.Fatalf("code = %v, want RKindRejected", diag.CodeOf(err))
	}
	if !strings.Contains(err.Error(), "Deps.Knowledge") {
		t.Errorf("error should mention the missing dependency, got %v", err)
	}
}

func TestKnowledgeInvokeBackendError(t *testing.T) {
	sk := &stubKnowledge{err: errKnowledgeBoom}
	impl, err := (Knowledge{}).Build(context.Background(), knowledgeSpec(nil, ""), Deps{Knowledge: sk})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	_, err = impl.Invoke(context.Background(), map[string]any{"Query": "q"})
	if err == nil {
		t.Fatal("expected backend error to propagate")
	}
	if diag.CodeOf(err) != diag.RNodeError {
		t.Fatalf("code = %v, want RNodeError", diag.CodeOf(err))
	}
}
