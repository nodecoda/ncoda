package verifier

// FuzzVerify drives the post-lowering graph verifier (Task 37, ~3800 LOC)
// over arbitrary workflow graphs. Like FuzzAnalyze, this covers shapes the
// compiler fuzz target never reaches (most fuzz sources die before
// lowering), enforcing the no-panic contract on the verifier's 23 rules
// with malformed graphs + a nil/empty facts. Run:
//
//	go test ./internal/backend/dify/verifier -fuzz=FuzzVerify -fuzztime=60s

import (
	"encoding/binary"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
	"github.com/nodecoda/ncoda/pkg/backend/dify/graphfacts"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/target/codec"
)

type lcgV struct{ s uint64 }

func (r *lcgV) next() uint64 {
	r.s = r.s*6364136223846793005 + 1442695040888963407
	return r.s
}

func (r *lcgV) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.next() % uint64(n))
}

func randomGraph(data []byte) *dify.WorkflowAST {
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
	r := &lcgV{s: seed}
	nodeCount := 1 + r.intn(48)
	ast := &dify.WorkflowAST{Graph: dify.GraphNode{}}
	ids := make([]string, 0, nodeCount)
	ntypes := []string{
		"start", "end", "code", "llm", "http-request", "if-else", "loop",
		"iteration", "iteration-start", "question-classifier",
		"parameter-extractor", "template-transform", "knowledge-retrieval",
		"variable-aggregator", "assigner", "agent", "tool", "list-operator",
		"human-input", "document-extractor", "datasource", "custom-note",
	}
	for i := 0; i < nodeCount; i++ {
		id := "n" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		ids = append(ids, id)
		ntype := ntypes[r.intn(len(ntypes))]
		var data dify.NodeData
		switch ntype {
		case "start":
			data = &dify.StartDataNode{Variables: []dify.VariableDef{{Variable: "v", Label: "V", Type: "string", Required: true}}}
		case "code":
			data = &dify.CodeDataNode{
				Code:         "def main() -> dict:\n    return {\"r\": 1}",
				CodeLanguage: "python3",
				Outputs:      map[string]dify.OutputDefNode{"r": {Type: "number"}},
				OutputOrder:  []string{"r"},
			}
		case "llm":
			data = &dify.LLMDataNode{}
		case "http-request":
			data = &dify.HTTPRequestDataNode{Method: "GET", URL: "https://x"}
		case "loop":
			data = &dify.LoopDataNode{LoopVariables: []dify.LoopVariable{{Variable: "i", ValueType: "number"}}}
		case "iteration":
			data = &dify.IterationDataNode{}
		case "iteration-start":
			data = &dify.IterationStartDataNode{}
		default:
			data = &dify.CustomNoteDataNode{}
		}
		ast.Graph.Nodes = append(ast.Graph.Nodes, dify.NodeDecl{ID: id, NodeType: ntype, Data: data})
	}
	edgeCount := r.intn(2*nodeCount + 4)
	for i := 0; i < edgeCount; i++ {
		ast.Graph.Edges = append(ast.Graph.Edges, dify.EdgeDecl{
			ID:     "e" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Source: ids[r.intn(len(ids))],
			Target: ids[r.intn(len(ids))],
		})
	}
	return ast
}

func FuzzVerify(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9})
	f.Add([]byte{0xff, 0x00, 0x7f, 0x80, 0x42, 0x42, 0x42, 0x42, 0x42})
	f.Add([]byte("graph-shape-probe"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			t.Skip()
		}
		ast := randomGraph(data)
		// Cycle through facts variants: empty facts, nil facts, nil profiles.
		switch data[0] % 3 {
		case 0:
			_ = Verify(ast, graphfacts.NewSemanticGraphFacts(), map[string]*binding.AgentTargetProfile{}, codec.Default())
		case 1:
			_ = Verify(ast, nil, nil, nil)
		case 2:
			_ = Verify(ast, graphfacts.NewSemanticGraphFacts(), nil, codec.Default())
		}
	})
}
