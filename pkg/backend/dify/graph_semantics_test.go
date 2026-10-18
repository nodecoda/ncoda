//go:build shelved

// 搁置面（AGENTS.md「开发主线与平台优先级」2026-09-11 拍板）：dify / coze-studio /
// python golden 语料面的测试结果**不是**主线（coze-biz）回归判据。本文件按门禁治理
// `内部治理文档（G-B 条）` G-B「快照/搁置面不阻塞默认门禁」移到默认门禁之外，
// 以免"常驻红基线"淹没主线真信号（实测：30 条常驻红里 23 条是搁置面）。
//
// 跑它：`go test -tags shelved ./...`（或 `make shelved`）；分类：`scripts/gov/triage.py`。
// 摘掉本 tag 的条件 = 该面重新成为主线判据（届时先修红再摘）。

package dify_test

// Graph-semantics regression suite — execution A of
// docs/DIFY-VS-COZE-GRAPH-SEMANTICS.md §5.
//
// Renders the full nodecoda corpus through the production pipeline
// (passes.DefaultPipeline → ir.Build → dify.RenderFromIR) and asserts the
// structural invariants that the renderer must uphold for every product:
//
//   §5.1  no edge targets a start node          (emitStart only emits)
//   §5.2  no dangling edges                     (every source/target exists)
//   §5.3  exactly one start, always reachable   (root has ≥1 outgoing edge)
//   §5.4  the graph is a DAG                    (loops live inside containers)
//   §5.5  Response contract: end/answer nodes are reachable (≥1 in-edge),
//         a product with any Response node has a terminal Response leaf, and
//         terminal end/answer leaves carry real output (no empty responders)
//   §5.6  answer execution order: if answer A reaches answer B by edges,
//         A precedes B in the node array (rendering order == execution
//         order); the 33_test_answer_order fixture additionally asserts the
//         semantic chain "answer 1 → answer 2" with literal text
//   §5.7  leaf audit: a non-Response leaf outside a loop/iteration container
//         is only legal for side-effect programs (no end/answer anywhere);
//         a program with explicit output must terminate every branch in an
//         end/answer leaf
//
// 06_tool_use.ncoda and 38_chatflow_time_tool.ncoda are the closed-world
// qualified-call forms (frontend fails without a tool registry) — same skip
// as parity_test.go.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
)

const graphSemanticsCorpus = "../../compiler/testdata/examples/nodecoda"

var graphSemanticsSkip = map[string]bool{
	"06_tool_use.ncoda":           true,
	"38_chatflow_time_tool.ncoda": true,
}

// ── harness ────────────────────────────────────────────────────────

type renderedGraph struct {
	ast      *dify.WorkflowAST
	kind     map[string]string // node id → "start" | "end" | "answer" | "other"
	startIDs []string
	nodes    []dify.NodeDecl
	edges    []dify.EdgeDecl
	out      map[string][]string // id → downstream ids
	index    map[string]int      // id → position in nodes array
}

// renderCorpusFiles renders every corpus file through the production
// pipeline. Files that fail the frontend/ir/render chain are hard errors
// (a corpus file must render) except the two closed-world forms.
func renderCorpusFiles(t *testing.T) map[string]*renderedGraph {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(graphSemanticsCorpus, "*.ncoda"))
	if err != nil {
		t.Fatalf("glob corpus: %v", err)
	}
	rendered := map[string]*renderedGraph{}
	for _, f := range files {
		name := filepath.Base(f)
		if graphSemanticsSkip[name] {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		res := passes.DefaultPipeline().Run(string(src), name, nil)
		if res.HasErrors() || res.Program == nil {
			t.Errorf("%s: frontend failed: %v", name, res.Errors())
			continue
		}
		irg, ierr := ir.Build(res.Program)
		if ierr != nil {
			t.Errorf("%s: ir-build: %v", name, ierr)
			continue
		}
		ast, rerr := dify.RenderFromIR(irg, res.Program.Program.Mode())
		if rerr != nil {
			t.Errorf("%s: render: %v", name, rerr)
			continue
		}
		rendered[name] = buildGraph(t, name, ast)
	}
	return rendered
}

func buildGraph(t *testing.T, name string, ast *dify.WorkflowAST) *renderedGraph {
	t.Helper()
	g := &renderedGraph{
		ast:   ast,
		kind:  map[string]string{},
		out:   map[string][]string{},
		index: map[string]int{},
	}
	for i, nd := range ast.Graph.Nodes {
		g.nodes = append(g.nodes, nd)
		g.index[nd.ID] = i
		switch nd.Data.(type) {
		case *dify.StartDataNode:
			g.kind[nd.ID] = "start"
		case *dify.OutputNode:
			g.kind[nd.ID] = "end"
		case *dify.AnswerNode:
			g.kind[nd.ID] = "answer"
		default:
			g.kind[nd.ID] = "other"
		}
		if g.kind[nd.ID] == "start" {
			g.startIDs = append(g.startIDs, nd.ID)
		}
	}
	for _, e := range ast.Graph.Edges {
		g.edges = append(g.edges, e)
		g.out[e.Source] = append(g.out[e.Source], e.Target)
	}
	return g
}

func (g *renderedGraph) inDegree(id string) int {
	n := 0
	for _, e := range g.edges {
		if e.Target == id {
			n++
		}
	}
	return n
}

func (g *renderedGraph) reachable(from string, visit map[string]bool) {
	if visit[from] {
		return
	}
	visit[from] = true
	for _, nxt := range g.out[from] {
		g.reachable(nxt, visit)
	}
}

// dataFlags reads the promoted DataNode.IsInIteration / IsInLoop fields via
// reflection (the concrete data node embeds DataNode; baseData is private).
func dataFlags(d dify.NodeData) (inIter, inLoop bool) {
	v := reflect.ValueOf(d)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if f := v.FieldByName("IsInIteration"); f.IsValid() && f.Kind() == reflect.Bool {
		inIter = f.Bool()
	}
	if f := v.FieldByName("IsInLoop"); f.IsValid() && f.Kind() == reflect.Bool {
		inLoop = f.Bool()
	}
	return inIter, inLoop
}

// isContainerNode reports whether a node is a loop/iteration container or a
// container-boundary marker; such nodes may legally be leaves (the container
// itself owns the exit wiring).
func isContainerNode(nd dify.NodeDecl) bool {
	switch nd.Data.(type) {
	case *dify.IterationDataNode,
		*dify.IterationStartDataNode,
		*dify.LoopDataNode,
		*dify.LoopStartDataNode,
		*dify.LoopEndDataNode:
		return true
	}
	return false
}

// isResponseLeaf reports whether nd is a terminal end/answer carrying real
// output (no empty responder).
func isResponseLeaf(nd dify.NodeDecl) bool {
	switch d := nd.Data.(type) {
	case *dify.OutputNode:
		return len(d.Outputs) > 0
	case *dify.AnswerNode:
		return strings.TrimSpace(d.Answer) != ""
	}
	return false
}

// ── §5.1 — no edge into start ─────────────────────────────────────

func TestGraphSemantics_NoEdgeIntoStart(t *testing.T) {
	for name, g := range renderCorpusFiles(t) {
		for _, e := range g.edges {
			for _, sid := range g.startIDs {
				if e.Target == sid {
					t.Errorf("%s: edge %s (%s→%s) targets start node %s", name, e.ID, e.Source, e.Target, sid)
				}
			}
		}
	}
}

// ── §5.2 — no dangling edges ──────────────────────────────────────

func TestGraphSemantics_NoDanglingEdges(t *testing.T) {
	for name, g := range renderCorpusFiles(t) {
		for _, e := range g.edges {
			if _, ok := g.index[e.Source]; !ok {
				t.Errorf("%s: edge %s references unknown source %q", name, e.ID, e.Source)
			}
			if _, ok := g.index[e.Target]; !ok {
				t.Errorf("%s: edge %s references unknown target %q", name, e.ID, e.Target)
			}
		}
	}
}

// ── §5.3 — exactly one start, always reachable ────────────────────

func TestGraphSemantics_StartUniqueConnected(t *testing.T) {
	for name, g := range renderCorpusFiles(t) {
		if len(g.startIDs) != 1 {
			t.Errorf("%s: expected exactly 1 start node, got %d (%v)", name, len(g.startIDs), g.startIDs)
			continue
		}
		start := g.startIDs[0]
		if len(g.out[start]) == 0 {
			t.Errorf("%s: start node %s has no outgoing edge (root unreachable)", name, start)
		}
	}
}

// ── §5.4 — DAG (loops live inside containers) ─────────────────────

func TestGraphSemantics_Acyclic(t *testing.T) {
	for name, g := range renderCorpusFiles(t) {
		const (
			white, gray, black = 0, 1, 2
		)
		color := map[string]int{}
		for id := range g.index {
			color[id] = white
		}
		var dfs func(string) bool
		dfs = func(id string) bool {
			color[id] = gray
			for _, nxt := range g.out[id] {
				switch color[nxt] {
				case gray:
					return true
				case white:
					if dfs(nxt) {
						return true
					}
				}
			}
			color[id] = black
			return false
		}
		for id := range g.index {
			if color[id] == white && dfs(id) {
				t.Errorf("%s: rendered graph contains a cycle through %s (loops must live inside containers)", name, id)
				break
			}
		}
	}
}

// ── §5.5 — Response contract ──────────────────────────────────────

func TestGraphSemantics_ResponseNodes(t *testing.T) {
	for name, g := range renderCorpusFiles(t) {
		var responseIDs []string
		var responseLeaves int
		for id, k := range g.kind {
			if k != "end" && k != "answer" {
				continue
			}
			responseIDs = append(responseIDs, id)
			if g.inDegree(id) == 0 {
				t.Errorf("%s: response node %s has no incoming edge (unreachable output)", name, id)
			}
			if len(g.out[id]) == 0 {
				responseLeaves++
				if nd := g.ast.Graph.Nodes[g.index[id]]; !isResponseLeaf(nd) {
					t.Errorf("%s: terminal response node %s carries no output", name, id)
				}
			}
		}
		if len(responseIDs) > 0 && responseLeaves == 0 {
			t.Errorf("%s: %d response node(s) but no terminal end/answer leaf — no branch returns output", name, len(responseIDs))
		}
	}
}

// ── §5.6 — answer execution order ─────────────────────────────────

func TestGraphSemantics_AnswerOrder(t *testing.T) {
	for name, g := range renderCorpusFiles(t) {
		var answers []string
		for id, k := range g.kind {
			if k == "answer" {
				answers = append(answers, id)
			}
		}
		if len(answers) < 2 {
			continue
		}
		// If answer A reaches answer B by edges, A executes first and must
		// appear earlier in the node array (rendering order == execution).
		for _, a := range answers {
			reach := map[string]bool{}
			g.reachable(a, reach)
			for _, b := range answers {
				if a == b || !reach[b] {
					continue
				}
				if g.index[a] >= g.index[b] {
					t.Errorf("%s: answer %s reaches answer %s but appears after it (idx %d >= %d); rendering order diverges from execution order",
						name, a, b, g.index[a], g.index[b])
				}
			}
		}
	}

	// Fixture: 33_test_answer_order — chained answers with literal text.
	// Start → LLM → Answer1("--- answer 1 ---\nfoo\n") → Answer2(LLM text).
	t.Run("chained_answer_literals", func(t *testing.T) {
		g := renderCorpusFiles(t)["33_test_answer_order.ncoda"]
		if g == nil {
			t.Fatal("33_test_answer_order.ncoda did not render")
		}
		var answers []dify.NodeDecl
		for _, nd := range g.ast.Graph.Nodes {
			if _, ok := nd.Data.(*dify.AnswerNode); ok {
				answers = append(answers, nd)
			}
		}
		if len(answers) != 2 {
			t.Fatalf("33: expected 2 answer nodes, got %d", len(answers))
		}
		first, second := answers[0].Data.(*dify.AnswerNode), answers[1].Data.(*dify.AnswerNode)
		if !strings.Contains(first.Answer, "--- answer 1 ---") {
			t.Errorf("33: first answer text %q does not contain %q", first.Answer, "--- answer 1 ---")
		}
		if !strings.Contains(second.Answer, "--- answer 2 ---") {
			t.Errorf("33: second answer text %q does not contain %q", second.Answer, "--- answer 2 ---")
		}
		chained := false
		for _, e := range g.edges {
			if e.Source == answers[0].ID && e.Target == answers[1].ID {
				chained = true
			}
		}
		if !chained {
			t.Errorf("33: expected edge %s → %s (chained answers in execution order)", answers[0].ID, answers[1].ID)
		}
	})
}

// ── §5.7 — leaf audit (branch termination discipline) ─────────────

func TestGraphSemantics_LeafAudit(t *testing.T) {
	for name, g := range renderCorpusFiles(t) {
		var hasResponse bool
		for _, k := range g.kind {
			if k == "end" || k == "answer" {
				hasResponse = true
				break
			}
		}
		for _, nd := range g.nodes {
			if len(g.out[nd.ID]) != 0 {
				continue
			}
			if _, isResp := g.kind[nd.ID]; isResp { // end/answer leaf
				continue
			}
			if isContainerNode(nd) { // container boundary marker
				continue
			}
			inIter, inLoop := dataFlags(nd.Data)
			if inIter || inLoop { // inside a container: exit owned by container
				continue
			}
			if !hasResponse {
				// Side-effect program (natural fallthrough, no explicit
				// output): any leaf is legal in dify semantics.
				continue
			}
			t.Errorf("%s: non-response leaf %s (kind %q) outside loop/iteration while the product has explicit output — branch not terminated in end/answer",
				name, nd.ID, g.kind[nd.ID])
		}
	}
}
