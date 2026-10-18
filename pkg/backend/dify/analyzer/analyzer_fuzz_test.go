package analyzer

// FuzzAnalyze drives the analyzer over arbitrary workflow graphs. The
// compiler's FuzzCompile only reaches the analyzer for inputs that survive
// the frontend + lowering (most fuzz bytes die in the parser), so a direct
// fuzz target feeds malformed / adversarial graph structures straight into
// Analyze to enforce the no-panic contract on the ported analyzer
// (Task 36, ~1500 LOC). Run:
//
//	go test ./internal/backend/dify/analyzer -fuzz=FuzzAnalyze -fuzztime=60s

import (
	"encoding/binary"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
)

// lcg is a tiny deterministic PRNG seeded from the fuzz bytes so the same
// input always yields the same graph.
type lcg struct{ s uint64 }

func (r *lcg) next() uint64 {
	r.s = r.s*6364136223846793005 + 1442695040888963407
	return r.s
}

func (r *lcg) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.next() % uint64(n))
}

// randomAST builds a bounded, deterministic pseudo-random workflow graph
// from the fuzz bytes (node count 1..48, node data of the types the
// analyzer's inferNodeOutputs / passes dispatch on, random edges).
func randomAST(data []byte) *dify.WorkflowAST {
	var seed uint64
	if len(data) >= 8 {
		seed = binary.LittleEndian.Uint64(data[:8])
	} else {
		for i, b := range data {
			seed ^= uint64(b) << (8 * (i % 8))
		}
	}
	if seed == 0 {
		seed = 1
	}
	r := &lcg{s: seed}
	byteAt := func(i int) byte { return data[(i*7+3)%len(data)] } // may panic on len==0; guarded below
	_ = byteAt

	nodeCount := 1 + r.intn(48)
	ast := &dify.WorkflowAST{Graph: dify.GraphNode{}}
	ids := make([]string, 0, nodeCount)
	ntypes := []string{
		"start", "end", "code", "llm", "http-request", "if-else", "loop",
		"iteration", "iteration-start", "question-classifier",
		"parameter-extractor", "template-transform", "knowledge-retrieval",
		"variable-aggregator", "assigner", "agent", "tool", "list-operator",
		"human-input", "document-extractor", "custom-note",
	}
	for i := 0; i < nodeCount; i++ {
		id := "n" + string(rune('a'+i%26)) + string(rune('0'+i/26)) // unique-enough ids
		ids = append(ids, id)
		ntype := ntypes[r.intn(len(ntypes))]
		var data dify.NodeData
		switch ntype {
		case "start":
			data = &dify.StartDataNode{Variables: []dify.VariableDef{{Variable: "v", Label: "V", Type: "string", Required: true}}}
		case "end":
			data = &dify.OutputNode{}
		case "code":
			data = &dify.CodeDataNode{
				Code:         "def main() -> dict:\n    return {\"r\": 1}",
				CodeLanguage: "python3",
				Outputs: map[string]dify.OutputDefNode{
					"r": {Type: "number"},
				},
				OutputOrder: []string{"r"},
			}
		case "llm":
			data = &dify.LLMDataNode{}
		case "http-request":
			data = &dify.HTTPRequestDataNode{Method: "GET", URL: "https://x"}
		case "if-else":
			data = &dify.IfElseDataNode{}
		case "loop":
			data = &dify.LoopDataNode{LoopVariables: []dify.LoopVariable{{Variable: "i", ValueType: "number"}}}
		case "iteration":
			data = &dify.IterationDataNode{}
		case "iteration-start":
			data = &dify.IterationStartDataNode{}
		case "question-classifier":
			data = &dify.QuestionClassifierDataNode{}
		case "parameter-extractor":
			data = &dify.ParameterExtractorDataNode{}
		case "template-transform":
			data = &dify.TemplateTransformDataNode{}
		case "knowledge-retrieval":
			data = &dify.KnowledgeRetrievalDataNode{}
		case "variable-aggregator":
			data = &dify.VariableAggregatorDataNode{}
		case "assigner":
			data = &dify.AssignerDataNode{}
		case "agent":
			data = &dify.AgentDataNode{}
		case "tool":
			data = &dify.ToolDataNode{}
		case "list-operator":
			data = &dify.ListOperatorDataNode{}
		case "human-input":
			data = &dify.HumanInputDataNode{}
		case "document-extractor":
			data = &dify.DocumentExtractorDataNode{}
		default:
			data = &dify.CustomNoteDataNode{}
		}
		ast.Graph.Nodes = append(ast.Graph.Nodes, dify.NodeDecl{ID: id, NodeType: ntype, Data: data})
	}
	// Random edges: bounded, may be self-loops / dangling — the analyzer must
	// not panic on any graph shape.
	edgeCount := r.intn(2*nodeCount + 4)
	for i := 0; i < edgeCount; i++ {
		src := ids[r.intn(len(ids))]
		tgt := ids[r.intn(len(ids))]
		ast.Graph.Edges = append(ast.Graph.Edges, dify.EdgeDecl{
			ID:           "e" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Source:       src,
			Target:       tgt,
			SourceHandle: "",
		})
	}
	return ast
}

func FuzzAnalyze(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9})
	f.Add([]byte{0xff, 0x00, 0x7f, 0x80, 0x42, 0x42, 0x42, 0x42, 0x42})
	f.Add([]byte("start-end-chain"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			t.Skip() // empty input: trivial
		}
		ast := randomAST(data)
		res := Analyze(ast)
		if res == nil {
			t.Fatalf("Analyze returned nil for graph of %d nodes", len(ast.Graph.Nodes))
		}
	})
}
