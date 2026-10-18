//go:build shelved

// 搁置面（AGENTS.md「开发主线与平台优先级」2026-09-11 拍板）：dify / coze-studio /
// python golden 语料面的测试结果**不是**主线（coze-biz）回归判据。本文件按门禁治理
// `内部治理文档（G-B 条）` G-B「快照/搁置面不阻塞默认门禁」移到默认门禁之外，
// 以免"常驻红基线"淹没主线真信号（实测：30 条常驻红里 23 条是搁置面）。
//
// 跑它：`go test -tags shelved ./...`（或 `make shelved`）；分类：`scripts/gov/triage.py`。
// 摘掉本 tag 的条件 = 该面重新成为主线判据（届时先修红再摘）。

package dify_test

//@feature var-let
//@feature if-else
//@feature for-while
//@feature output
//@feature template-str
//@feature for-expr
//@feature dict-list
//@feature qualified-call
//@feature record-type
//@feature enum
//@feature foreign-code
//@feature request-input
//@feature lambda
//@feature yield
//@feature field-index
//@feature file-type
//@feature return-hierarchy
//@feature parallel-fence

// SerializeToDict tests (Task 26), oracle-locked against
// 上游 Python 参考实现（nclang） compile_nodecoda_result -> serialize_to_dict
// (json.dumps(sort_keys=False), run 2026-08-14; see /tmp/oracle26/*.json
// and task-26-report.md). Every fixture's Go lowering output is compared
// against the embedded Python oracle: byte-for-byte where the Go AST has no
// map-backed payloads (byte order = Python dict insertion order), and by
// value plus structural key-order assertions where Go maps force a sorted
// key order (recorded deviation) or where the Python output embeds a random
// uuid4 id.

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/tool"
)

// ── Fixture sources (lowering test parity) ─────────────────────────

const minimalSrc = `
function main() {
    let response = llm("gpt4", {"messages": [{"role": "user", "content": "Hello"}]});
    return response.text;
}
`

const startParamsSrc = `
function main(string query) {
    let r = llm("gpt4", {"messages": [{"role": "user", "content": query}]});
    return r.text;
}
`

const rag41Src = `@language nodecoda/1
@mode workflow

function main(string query) -> string {
    return std.v1.rag_answer(query, "ds-001,ds-002", "openai/gpt-4o");
}
`

const http42Src = `@language nodecoda/1
@mode workflow

function main() -> string {
    return std.v1.fetch_and_summarize(
        "https://example.com/article",
        "openai/gpt-4o"
    );
}
`

const templateSrc = `function main(string name, int score) {
    var grade = score >= 90 ? "A" : (score >= 60 ? "B" : "C");
    var result = ` + "`Name: ${name}\\nScore: ${score}\\nGrade: ${grade}`" + `;
    return result;
}
`

const toolSrc = `@language nodecoda/1
@mode workflow
import "dify.google_search";
function main(string query) {
    let search = google_search.search({
        "query": query,
        "max_results": 5
    });
    return search.result;
}
`

const extractSrc = `@language nodecoda/1
@mode workflow
type TravelRequest = {
    string city;
    float days;
    bool flexible;
    string[] tags;
}
function main(string text) -> TravelRequest {
    let extracted = extract<TravelRequest>("provider/model", text, {
        instruction: "Extract a travel request",
        descriptions: {
            city: "Destination city",
            days: "Trip duration in days",
            flexible: "Whether travel dates are flexible",
            tags: "Travel preferences"
        },
        strategy: "prompt"
    });
    return extracted.value;
}
`

const classifySrc = `@language nodecoda/1
@mode workflow
enum Intent { unknown, support, billing }
function main(string query) -> Intent {
    let clasif = classify<Intent>("provider/model", query, {
        instruction: "Classify the request",
        labels: {
            unknown: "Cannot determine intent",
            support: "Technical support request",
            billing: "Billing request"
        },
        fallback: Intent.support
    });
    return clasif;
}
`

const agentSrc = `@language nodecoda/1
@mode workflow
function main(string query) -> string {
    let result = agent("provider/model", query, {
        profile: "function-calling-v1",
        instruction: "Answer the user",
        tools: ["clock"]
    });
    return result.text;
}
`

const loopSrc = `@language nodecoda/1
@mode workflow

function main() {
    var num = 1;
    while (num < 5) limit 10 {
        num += 1;
    }
    return num;
}
`

const forExprSrc = `@language nodecoda/1
@mode workflow

function main(string item) {
    let items = [item];
    var results = for (item in items) {
        yield item;
    };

    return results;
}
`

const extractTextSrc = `@language nodecoda/1
@mode workflow
function main(file<document; .pdf> doc) -> string {
    let result = extract_text(doc);
    return result.text;
}
`

const ifElseSrc = `function main(string query, string language = "Chinese") {
    if (language == "Chinese" || language == "Chinese") {
        let response = llm("openai_api_compatible/gpt-5.4", {
            "messages": [ { "role": "user", "content": "Chinese" } ],
            "temperature": 0.5,
            "max_tokens": 2048
        });
        return response.text;
    } else {
        let response = llm("openai_api_compatible/gpt-5.4", {
            "messages": [ { "role": "user", "content": "English" } ],
            "temperature": 0.5,
            "max_tokens": 1024
        });
        return response.text;
    }
}
`

// ── Harness ────────────────────────────────────────────────────────

// lowerSerialize runs the full frontend pipeline and lowers to a WorkflowAST
// with the default target, failing on any diagnostic.
func lowerSerialize(t *testing.T, src string) *dify.WorkflowAST {
	t.Helper()
	return lowerSerializeProfile(t, src, nil)
}

func lowerSerializeProfile(t *testing.T, src string, profile *passes.TargetProfile) *dify.WorkflowAST {
	t.Helper()
	res := passes.DefaultPipeline().Run(src, "t.dc", profile)
	if res.HasErrors() {
		for _, d := range res.Diagnostics {
			t.Logf("pipeline diagnostic %d %s: %s", d.Code, d.Severity, d.Message)
		}
		t.Fatalf("frontend pipeline reported errors for source:\n%s", src)
	}
	if res.Program == nil {
		t.Fatal("frontend pipeline produced no TypedBoundProgram")
	}
	// Legacy-retirement (2026-08-20): lower through the production IR path
	// (ir.Build + RenderFromIR) instead of the frozen legacy oracle. The
	// parity harness proved IR output byte-identical to legacy lowering on
	// the corpus; the inline fixtures that differ structurally (tpl_var
	// id-order, extract_text/classify legacy-only nodes) use constructed
	// AST fixtures instead, so every lowering path here is IR-native.
	graph, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("IR build: %v", err)
	}
	astOut, err := dify.RenderFromIR(graph, res.Program.Program.Mode())
	if err != nil {
		t.Fatalf("RenderFromIR: %v", err)
	}
	return astOut
}

// lowerSerializeIR runs the frontend pipeline with the closed-world
// capability registry and renders through the production IR path. Task 4:
// the qualified direct-call form p.op({params}) has no legacy ident-builtin
// lowering (the legacy oracle dispatches only llm/http/tool/knowledge/
// extract_text by name), so fixtures migrated to qualified calls use this
// path; the IR render is byte-identical to the pre-migration legacy output
// (Task 4 artifact-stability gate). The legacy oracle was retired
// (2026-08-20); this is now the only lowering path.
func lowerSerializeIR(t *testing.T, src string, registry *tool.Registry) *dify.WorkflowAST {
	t.Helper()
	res := passes.DefaultPipeline().WithToolRegistry(registry).Run(src, "t.dc", nil)
	if res.HasErrors() {
		for _, d := range res.Diagnostics {
			t.Logf("pipeline diagnostic %d %s: %s", d.Code, d.Severity, d.Message)
		}
		t.Fatalf("frontend pipeline reported errors for source:\n%s", src)
	}
	if res.Program == nil {
		t.Fatal("frontend pipeline produced no TypedBoundProgram")
	}
	graph, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("IR build: %v", err)
	}
	astOut, err := dify.RenderFromIR(graph, res.Program.Program.Mode())
	if err != nil {
		t.Fatalf("RenderFromIR: %v", err)
	}
	return astOut
}

// googleSearchRegistry mirrors the Task 4 capability header
// internal/compiler/testdata/examples/capabilities/google_search.ncoda
// (query/max_results parameters), so the qualified google_search.search
// fixture strong-checks closed-world.
func googleSearchRegistry(t *testing.T) *tool.Registry {
	t.Helper()
	registry, err := tool.NewRegistry(tool.Definition{
		Provider:   "google_search",
		Operation:  "search",
		Parameters: []tool.Parameter{{Name: "query", Type: "any"}, {Name: "max_results", Type: "int"}},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return registry
}

// serializeBytes serializes and returns the MarshalJSON bytes.
func serializeBytes(t *testing.T, wf *dify.WorkflowAST) []byte {
	t.Helper()
	om, err := dify.SerializeToDict(wf)
	if err != nil {
		t.Fatalf("SerializeToDict: %v", err)
	}
	b, err := om.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	return b
}

// assertBytesEqual compares Go output bytes with the compacted embedded
// oracle bytes (key order preserved via json.Compact).
func assertBytesEqual(t *testing.T, name string, got []byte, oracle string) {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(oracle)); err != nil {
		t.Fatalf("%s: compact oracle: %v", name, err)
	}
	if string(got) != buf.String() {
		t.Errorf("%s: serialized bytes differ from oracle\n got  %s\n want %s", name, got, buf.String())
	}
}

// assertValueEqual compares Go output with the oracle by value semantics
// (both unmarshaled as any → float64 numbers), independent of key order.
func assertValueEqual(t *testing.T, name string, got []byte, oracle string) {
	t.Helper()
	var gv, ov any
	if err := json.Unmarshal(got, &gv); err != nil {
		t.Fatalf("%s: unmarshal got: %v", name, err)
	}
	if err := json.Unmarshal([]byte(oracle), &ov); err != nil {
		t.Fatalf("%s: unmarshal oracle: %v", name, err)
	}
	if !reflect.DeepEqual(gv, ov) {
		t.Errorf("%s: value mismatch vs oracle\n got  %s\n want %s", name, got, oracle)
	}
}

// nodeKeys returns the data-level key list of the i-th node.
func nodeDataKeys(t *testing.T, wf *dify.WorkflowAST, i int) []string {
	t.Helper()
	om, err := dify.SerializeToDict(wf)
	if err != nil {
		t.Fatalf("SerializeToDict: %v", err)
	}
	graph, _ := om.Get("workflow")
	wfMap := graph.(*dify.OrderedMap)
	g, _ := wfMap.Get("graph")
	gm := g.(*dify.OrderedMap)
	nodes, _ := gm.Get("nodes")
	node := nodes.([]any)[i].(*dify.OrderedMap)
	data, _ := node.Get("data")
	return data.(*dify.OrderedMap).Keys()
}

// nodeDataKeysByID is nodeDataKeys keyed by node id instead of a positional
// index, so structural assertions survive node reordering.
func nodeDataKeysByID(t *testing.T, wf *dify.WorkflowAST, id string) []string {
	t.Helper()
	om, err := dify.SerializeToDict(wf)
	if err != nil {
		t.Fatalf("SerializeToDict: %v", err)
	}
	graph, _ := om.Get("workflow")
	wfMap := graph.(*dify.OrderedMap)
	g, _ := wfMap.Get("graph")
	gm := g.(*dify.OrderedMap)
	nodes, _ := gm.Get("nodes")
	for _, n := range nodes.([]any) {
		node := n.(*dify.OrderedMap)
		nid, _ := node.Get("id")
		if nid == id {
			data, _ := node.Get("data")
			return data.(*dify.OrderedMap).Keys()
		}
	}
	t.Fatalf("node %q not found", id)
	return nil
}

// ── OrderedMap unit tests ──────────────────────────────────────────

func TestOrderedMapBasics(t *testing.T) {
	om := dify.NewOrderedMap()
	om.Set("b", 1)
	om.Set("a", 2)
	om.Set("c", 3)
	if got := om.Len(); got != 3 {
		t.Errorf("Len = %d, want 3", got)
	}
	if got := strings.Join(om.Keys(), ","); got != "b,a,c" {
		t.Errorf("Keys = %s, want b,a,c (insertion order)", got)
	}
	if got := om.Values(); !reflect.DeepEqual(got, []any{1, 2, 3}) {
		t.Errorf("Values = %v, want [1 2 3]", got)
	}
	if v, ok := om.Get("a"); !ok || v != 2 {
		t.Errorf("Get(a) = %v/%v, want 2/true", v, ok)
	}
	if _, ok := om.Get("missing"); ok {
		t.Error("Get(missing) = ok, want false")
	}

	// Duplicate Set keeps the first insertion position (Python dict).
	om.Set("a", 42)
	if got := strings.Join(om.Keys(), ","); got != "b,a,c" {
		t.Errorf("Keys after dup = %s, want b,a,c (first position kept)", got)
	}
	if v, _ := om.Get("a"); v != 42 {
		t.Errorf("Get(a) after dup = %v, want 42 (value replaced)", v)
	}
}

func TestOrderedMapMarshalJSON(t *testing.T) {
	om := dify.NewOrderedMap()
	om.Set("z", "tail")
	om.Set("a", 1)
	nested := dify.NewOrderedMap()
	nested.Set("inner", true)
	nested.Set("first", "x")
	om.Set("mid", nested)
	om.Set("arr", []any{1, "two", nested})
	b, err := om.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	want := `{"z":"tail","a":1,"mid":{"inner":true,"first":"x"},"arr":[1,"two",{"inner":true,"first":"x"}]}`
	if string(b) != want {
		t.Errorf("MarshalJSON = %s, want %s", b, want)
	}

	// Empty and nil maps.
	empty := dify.NewOrderedMap()
	eb, err := empty.MarshalJSON()
	if err != nil || string(eb) != "{}" {
		t.Errorf("empty marshal = %s/%v, want {}", eb, err)
	}
	var nilMap *dify.OrderedMap
	// A nil OrderedMap embedded in a plain map marshals as null (Python None).
	outer := map[string]any{"k": nilMap}
	ob, err := json.Marshal(outer)
	if err != nil || string(ob) != `{"k":null}` {
		t.Errorf("nil nested marshal = %s/%v, want {\"k\":null}", ob, err)
	}

	// No HTML escaping (json.dumps(ensure_ascii=False) parity).
	html := dify.NewOrderedMap()
	html.Set("code", "a < b && c > d")
	hb, err := html.MarshalJSON()
	if err != nil || string(hb) != `{"code":"a < b && c > d"}` {
		t.Errorf("html marshal = %s/%v, want raw < > & unescaped", hb, err)
	}
}

// ── Byte-compare fixtures (no map-backed payloads) ─────────────────

func TestSerializeMinimal(t *testing.T) {
	wf := lowerSerialize(t, minimalSrc)
	assertBytesEqual(t, "minimal", serializeBytes(t, wf), oracle26_minimal)
	assertValueEqual(t, "minimal", serializeBytes(t, wf), oracle26_minimal)
}

func TestSerializeStartParams(t *testing.T) {
	wf := lowerSerialize(t, startParamsSrc)
	assertBytesEqual(t, "start_params", serializeBytes(t, wf), oracle26_start_params)
}

func TestSerializeRag41(t *testing.T) {
	wf := lowerSerialize(t, rag41Src)
	assertBytesEqual(t, "rag41", serializeBytes(t, wf), oracle26_rag41)
}

func TestSerializeHttp42(t *testing.T) {
	wf := lowerSerialize(t, http42Src)
	assertBytesEqual(t, "http42", serializeBytes(t, wf), oracle26_http42)
}

func TestSerializeTemplate(t *testing.T) {
	wf := lowerSerialize(t, templateSrc)
	assertBytesEqual(t, "template", serializeBytes(t, wf), oracle26_template)
}

func TestSerializeIfElse(t *testing.T) {
	wf := lowerSerialize(t, ifElseSrc)
	assertBytesEqual(t, "ifelse", serializeBytes(t, wf), oracle26_ifelse)
}

func TestSerializeExtractText(t *testing.T) {
	// Constructed (legacy-retirement): see buildExtractTextFixture.
	wf := buildExtractTextFixture(t)
	assertBytesEqual(t, "extract_text", serializeBytes(t, wf), oracle26_extract_text)
}

func TestSerializeForExpr(t *testing.T) {
	wf := lowerSerialize(t, forExprSrc)
	assertBytesEqual(t, "for_expr", serializeBytes(t, wf), oracle26_for_expr)
}

func TestSerializeClassify(t *testing.T) {
	// Constructed (legacy-retirement): see buildClassifyFixture.
	wf := buildClassifyFixture(t)
	assertBytesEqual(t, "classify", serializeBytes(t, wf), oracle26_classify)
}

// ── Value-compare fixtures (map-backed key-order deviation) ────────

// TestSerializeExtract covers the parameter-extractor node, the extraction
// code adapter and the chained output nodes. The code node's `outputs`
// preserves the Python dict insertion order {value, ok, reason, usage}
// (CodeDataNode.OutputOrder, Task 40); the lock below asserts it.
func TestSerializeExtract(t *testing.T) {
	wf := lowerSerialize(t, extractSrc)
	got := serializeBytes(t, wf)
	assertValueEqual(t, "extract", got, oracle26_extract)

	// Structural key order: data keys of the code adapter (extract_result_3).
	keys := nodeDataKeysByID(t, wf, "extract_result_3")
	wantKeys := []string{"type", "title", "code_language", "code", "variables", "outputs"}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Errorf("code node data keys = %v, want %v (serializer.py:525-540)", keys, wantKeys)
	}

	// The outputs sub-object keeps the Python insertion order
	// {value, ok, reason, usage} (node_factory.py:275-283); assert it.
	om, _ := dify.SerializeToDict(wf)
	workflow, _ := om.Get("workflow")
	graph, _ := workflow.(*dify.OrderedMap).Get("graph")
	nodes, _ := graph.(*dify.OrderedMap).Get("nodes")
	codeNode := nodes.([]any)[2].(*dify.OrderedMap)
	codeData, _ := codeNode.Get("data")
	outputs, _ := codeData.(*dify.OrderedMap).Get("outputs")
	gotKeys := outputs.(*dify.OrderedMap).Keys()
	wantOrder := []string{"value", "ok", "reason", "usage"}
	if !reflect.DeepEqual(gotKeys, wantOrder) {
		t.Errorf("code outputs keys = %v, want Python insertion order %v", gotKeys, wantOrder)
	}
}

// TestSerializeTool covers the tool node. tool_parameters is a Go map
// (Python insertion {query, max_results}); Go emits sorted — deviation.
func TestSerializeTool(t *testing.T) {
	// Migrated to the qualified direct-call form (Task 4); lowered through
	// the IR path (lowerSerializeIR) because the legacy ident-builtin
	// lowerer cannot dispatch google_search.search. The oracle26_tool
	// oracle must still match: IR render is byte-identical to the legacy
	// tool() output.
	wf := lowerSerializeIR(t, toolSrc, googleSearchRegistry(t))
	got := serializeBytes(t, wf)
	assertValueEqual(t, "tool", got, oracle26_tool)

	keys := nodeDataKeys(t, wf, 1)
	wantKeys := []string{"type", "title", "tool_name", "provider_id", "tool_parameters",
		"tool_configuration", "tool_configurations", "output_schema", "paramSchemas", "params"}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Errorf("tool node data keys = %v, want %v (serializer.py:573-603)", keys, wantKeys)
	}
}

// TestSerializeAgent covers the agent node. agent_parameters is a Go map
// (Python insertion {model, query, instruction, tools}); Go emits sorted —
// deviation, values equal.
func TestSerializeAgent(t *testing.T) {
	wf := lowerSerializeProfile(t, agentSrc, agentSerializeProfile(t))
	got := serializeBytes(t, wf)
	assertValueEqual(t, "agent", got, oracle26_agent)

	keys := nodeDataKeys(t, wf, 1)
	wantKeys := []string{"type", "title", "output_schema", "agent_strategy_name",
		"agent_strategy_provider_name", "agent_strategy_label", "agent_parameters",
		"plugin_unique_identifier", "tool_node_version"}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Errorf("agent node data keys = %v, want %v (serializer.py:765-793)", keys, wantKeys)
	}
}

// TestSerializeLoop covers loop/loop-start/loop-end/assigner. The
// loop-variable id is a deterministic uuid5-style id on both sides since
// P3 (Python serializer.py mirrors Go's loopVariableID); values are
// compared after normalizing the id, and the deterministic id is locked.
func TestSerializeLoop(t *testing.T) {
	wf := lowerSerialize(t, loopSrc)
	got := serializeBytes(t, wf)

	// The Go output must be stable: loop-variable id is deterministic.
	wantLoopID := "f3036ebd-cc89-5d2a-97b6-293747878b90"
	if !strings.Contains(string(got), wantLoopID) {
		t.Errorf("loop output missing deterministic id %s (got %s)", wantLoopID, got)
	}

	// Structural comparison: replace the Python random id with the Go
	// deterministic id, then the documents must be value-equal.
	oracle := strings.Replace(oracle26_loop, "30bc6405-56de-47aa-8180-db2a88a6d7c7", wantLoopID, 1)
	assertValueEqual(t, "loop", got, oracle)

	// Data key order of the loop node.
	keys := nodeDataKeysByID(t, wf, "loop_2")
	wantKeys := []string{"type", "title", "loop_variables", "break_conditions",
		"loop_count", "logical_operator", "start_node_id"}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Errorf("loop node data keys = %v, want %v (serializer.py:650-684)", keys, wantKeys)
	}
}

// ── Constructed AST fixtures (nodes not reachable from workflow mode) ─

func TestSerializeConstructedAnswer(t *testing.T) {
	ans := dify.NewAnswerNode()
	ans.Title = "Answer"
	ans.Answer = "hi"
	ans.Variables = []dify.VariableSelectorNode{}
	nd := dify.NewNodeDecl()
	nd.ID = "answer_2"
	nd.NodeType = "answer"
	nd.Data = ans
	wf := dify.NewWorkflowAST()
	wf.Graph.Nodes = []dify.NodeDecl{nd}
	got := serializeBytes(t, wf)
	assertBytesEqual(t, "answer", got, oracle26_answer)
	assertValueEqual(t, "answer", got, oracle26_answer)
}

func TestSerializeConstructedHumanInput(t *testing.T) {
	hi := dify.NewHumanInputDataNode()
	hi.Title = "Human Input"
	hi.FormContent = "Please fill in"
	dfltV := dify.NewStringSourceNode()
	dfltV.Selector = []string{"n_1", "x"}
	dfltV.Value = "hi"
	para := dify.NewParagraphHumanInput("note")
	para.Default = &dfltV
	opt := dify.NewStringListSourceNode()
	opt.Selector = []string{"n_1", "x"}
	opt.Value = []string{"a", "b"}
	sel := dify.NewSelectHumanInput("pick", opt)
	fh := dify.NewFileHumanInput("doc")
	fh.AllowedFileTypes = []string{"document"}
	fh.AllowedFileExtensions = []string{".pdf"}
	fh.AllowedFileUploadMethods = []string{"local_file"}
	fl := dify.NewFileListHumanInput("docs")
	fl.AllowedFileTypes = []string{"document"}
	fl.AllowedFileExtensions = []string{".pdf"}
	fl.AllowedFileUploadMethods = []string{"local_file"}
	fl.NumberLimits = 5
	hi.Inputs = []dify.HumanInputField{para, sel, fh, fl}
	hi.UserActions = []dify.UserAction{{ID: "ua_1", Title: "Confirm", ButtonStyle: "primary"}}
	hi.DeliveryMethods = []any{map[string]any{"id": "dm_1", "type": "webapp", "enabled": true}}
	nd := dify.NewNodeDecl()
	nd.ID = "n_1"
	nd.NodeType = "human-input"
	nd.Data = hi
	wf := dify.NewWorkflowAST()
	wf.Graph.Nodes = []dify.NodeDecl{nd}
	got := serializeBytes(t, wf)
	assertBytesEqual(t, "human_input", got, oracle26_human_input)
	assertValueEqual(t, "human_input", got, oracle26_human_input)
}

func TestSerializeConstructedDatasource(t *testing.T) {
	ds := dify.NewDatasourceDataNode()
	ds.Title = "Datasource"
	ds.PluginID = "langgenius/notion"
	ds.ProviderName = "notion"
	ds.ProviderType = "plugin"
	ds.DatasourceConfigurations = map[string]any{"page_size": 10}
	pui := "langgenius/notion:0.0.1"
	ds.PluginUniqueIdentifier = &pui
	typ := "string"
	ds.DatasourceParameters = map[string]dify.DatasourceInputNode{"db": {Value: "pages", Type: &typ}}
	nd := dify.NewNodeDecl()
	nd.ID = "n_1"
	nd.NodeType = "datasource"
	nd.Data = ds
	wf := dify.NewWorkflowAST()
	wf.Graph.Nodes = []dify.NodeDecl{nd}
	got := serializeBytes(t, wf)
	assertBytesEqual(t, "datasource", got, oracle26_datasource)
	assertValueEqual(t, "datasource", got, oracle26_datasource)
}

func TestSerializeConstructedListOperator(t *testing.T) {
	lo := dify.NewListOperatorDataNode()
	lo.Title = "List Operator"
	lo.Variable = []string{"n_1", "items"}
	lo.FilterBy = dify.ListFilterConfig{Enabled: true, Conditions: []dify.ListFilterItem{{Key: "price", ComparisonOperator: ">", Value: 10}}}
	lo.ExtractBy = dify.ListExtractConfig{Enabled: true, Serial: "1"}
	lo.OrderBy = dify.ListOrderConfig{Enabled: true, Key: "name", Value: "asc"}
	lo.Limit = dify.ListLimitConfig{Enabled: true, Size: 5}
	lo.VarType = "array[string]"
	lo.ItemVarType = "string"
	nd := dify.NewNodeDecl()
	nd.ID = "n_1"
	nd.NodeType = "list-operator"
	nd.Data = lo
	wf := dify.NewWorkflowAST()
	wf.Graph.Nodes = []dify.NodeDecl{nd}
	got := serializeBytes(t, wf)
	assertBytesEqual(t, "list_operator", got, oracle26_list_operator)
	assertValueEqual(t, "list_operator", got, oracle26_list_operator)
}

func TestSerializeConstructedCustomNote(t *testing.T) {
	cn := dify.NewCustomNoteDataNode()
	cn.Text = "Remember"
	cn.Author = "alice"
	cn.Theme = "blue"
	cn.NoteHeight = 200
	cn.NoteWidth = 300
	nd := dify.NewNodeDecl()
	nd.ID = "n_1"
	nd.NodeType = "custom-note"
	nd.Data = cn
	z := 1001
	nd.ZIndex = &z
	wf := dify.NewWorkflowAST()
	wf.Graph.Nodes = []dify.NodeDecl{nd}
	got := serializeBytes(t, wf)
	assertBytesEqual(t, "custom_note", got, oracle26_custom_note)
	assertValueEqual(t, "custom_note", got, oracle26_custom_note)
}

func TestSerializeConstructedVariableAggregator(t *testing.T) {
	va := dify.NewVariableAggregatorDataNode()
	va.Title = "Aggregate"
	va.Variables = []dify.VariableSelectorNode{
		dify.VariableSelectorFromList([]string{"a_1", "x"}),
		dify.VariableSelectorFromList([]string{"b_1", "y"}),
	}
	ot := "array[string]"
	va.OutputType = &ot
	va.AdvancedSettings = map[string]any{"group_enabled": true}
	nd := dify.NewNodeDecl()
	nd.ID = "agg_1"
	nd.NodeType = "variable-aggregator"
	nd.Data = va
	wf := dify.NewWorkflowAST()
	wf.Graph.Nodes = []dify.NodeDecl{nd}
	got := serializeBytes(t, wf)
	assertBytesEqual(t, "var_agg", got, oracle26_var_agg)
	assertValueEqual(t, "var_agg", got, oracle26_var_agg)
}

// TestSerializeEdgesSpecial locks edge canonicalization (variable-assigner →
// custom), the iteration_id/loop_id conditions, sourceType/targetType lookup
// and zIndex emission (serializer.py:957-985).
func TestSerializeEdgesSpecial(t *testing.T) {
	le := dify.NewLoopEndDataNode()
	le.Title = "Loop End"
	le.LoopID = "loop_1"
	le.IsInLoop = true
	le2 := dify.NewLoopEndDataNode()
	le2.Title = "Loop End"
	le2.LoopID = "loop_1"
	le2.IsInLoop = true
	outer := dify.NewNodeDecl()
	outer.ID = "outer_1"
	outer.NodeType = "loop-end"
	outer.Data = le
	inner := dify.NewNodeDecl()
	inner.ID = "inner_1"
	inner.NodeType = "loop-end"
	inner.Data = le2
	wf := dify.NewWorkflowAST()
	wf.Graph.Nodes = []dify.NodeDecl{outer, inner}
	inIter, inLoop := true, true
	th := "target"
	wf.Graph.Edges = []dify.EdgeDecl{
		{ID: "e_1", Source: "n_1", Target: "n_2", Type: "variable-assigner", SourceHandle: "source", TargetHandle: &th, IsInIteration: &inIter, IsInLoop: &inLoop, LoopID: "loop_1", ZIndex: 1002},
		{ID: "e_2", Source: "outer_1", Target: "inner_1", Type: "custom", SourceHandle: "source", IsInIteration: &inIter, IsInLoop: &inLoop, LoopID: "loop_1", IterationID: "iter_9"},
	}
	got := serializeBytes(t, wf)
	assertBytesEqual(t, "edges_special", got, oracle26_edges_special)
	assertValueEqual(t, "edges_special", got, oracle26_edges_special)
}

// TestSerializeViewportVars locks viewport normalization (integral float →
// int, nil → {0,0,1}) and conversation/environment variable coercion
// (_coerce_variable_value + _normalize_value_type: "42"/number → 42/integer).
func TestSerializeViewportVars(t *testing.T) {
	wf := dify.NewWorkflowAST()
	wf.Graph.Viewport = &dify.ViewportNode{X: 2.0, Y: -1.5, Zoom: 0.75}
	wf.ConversationVariables = []dify.ConversationVariable{
		{ID: "cv_1", Name: "topic", ValueType: "number", Value: "42", Description: "desc", Selector: []string{"conversation", "topic"}},
	}
	wf.EnvironmentVariables = []dify.EnvironmentVariable{
		{ID: "ev_1", Name: "KEY", ValueType: "string", Value: "val", Description: "env desc"},
	}
	wf.RagPipelineVariables = []string{"{{#start_1.x#}}"}
	got := serializeBytes(t, wf)
	assertBytesEqual(t, "viewport_vars", got, oracle26_viewport_vars)
	assertValueEqual(t, "viewport_vars", got, oracle26_viewport_vars)
}

// ── Error faces (Python ValueError → Go error, never swallowed) ────

func TestSerializeErrorUnknownNode(t *testing.T) {
	nd := dify.NewNodeDecl()
	nd.ID = "n_1"
	nd.Data = &dify.DataNode{} // bare base — unmodeled
	wf := dify.NewWorkflowAST()
	wf.Graph.Nodes = []dify.NodeDecl{nd}
	_, err := dify.SerializeToDict(wf)
	if err == nil {
		t.Fatal("SerializeToDict on unmodeled node: want error, got nil")
	}
	want := "Cannot serialize unmodeled node type 'DataNode'. Add a dedicated serializer to _DATA_NODE_SERIALIZERS."
	if err.Error() != want {
		t.Errorf("error = %q, want %q (serializer.py:951-955)", err.Error(), want)
	}
}

func TestSerializeErrorAgentToolNodeVersion(t *testing.T) {
	agent := dify.NewAgentDataNode()
	agent.Title = "agent"
	v1 := "1"
	agent.ToolNodeVersion = &v1
	nd := dify.NewNodeDecl()
	nd.ID = "n_1"
	nd.NodeType = "agent"
	nd.Data = agent
	wf := dify.NewWorkflowAST()
	wf.Graph.Nodes = []dify.NodeDecl{nd}
	_, err := dify.SerializeToDict(wf)
	if err == nil {
		t.Fatal("agent tool_node_version '1': want error, got nil")
	}
	want := "agent export requires explicit tool_node_version '2'"
	if err.Error() != want {
		t.Errorf("error = %q, want %q (serializer.py:766-768)", err.Error(), want)
	}
}

func TestSerializeErrorQuestionClassifierModel(t *testing.T) {
	qc := dify.NewQuestionClassifierDataNode()
	qc.Title = "Question Classifier"
	qc.QueryVariableSelector = &dify.VariableSelectorNode{Path: []string{"start_1", "query"}}
	nd := dify.NewNodeDecl()
	nd.ID = "n_1"
	nd.NodeType = "question-classifier"
	nd.Data = qc
	wf := dify.NewWorkflowAST()
	wf.Graph.Nodes = []dify.NodeDecl{nd}
	_, err := dify.SerializeToDict(wf)
	if err == nil {
		t.Fatal("question-classifier without model: want error, got nil")
	}
	want := "question-classifier export requires model"
	if err.Error() != want {
		t.Errorf("error = %q, want %q (serializer.py:630-631)", err.Error(), want)
	}
}

func TestSerializeErrorQuestionClassifierSelector(t *testing.T) {
	qc := dify.NewQuestionClassifierDataNode()
	qc.Title = "Question Classifier"
	nd := dify.NewNodeDecl()
	nd.ID = "n_1"
	nd.NodeType = "question-classifier"
	nd.Data = qc
	wf := dify.NewWorkflowAST()
	wf.Graph.Nodes = []dify.NodeDecl{nd}
	_, err := dify.SerializeToDict(wf)
	if err == nil {
		t.Fatal("question-classifier without query_variable_selector: want error, got nil")
	}
	want := "question-classifier export requires query_variable_selector"
	if err.Error() != want {
		t.Errorf("error = %q, want %q (serializer.py:628-629)", err.Error(), want)
	}
}

// ── Determinism ────────────────────────────────────────────────────

// TestSerializeDeterminism locks the hard determinism rule: the same input
// lowered and serialized twice must be byte-identical (the loop fixture also
// exercises the deterministic uuid5 loop-variable id).
func TestSerializeDeterminism(t *testing.T) {
	for name, src := range map[string]string{
		"minimal": minimalSrc,
		"extract": extractSrc,
		"loop":    loopSrc,
		"agent":   agentSrc,
	} {
		wf := lowerSerializeProfile(t, src, agentSerializeProfile(t))
		first := serializeBytes(t, wf)
		wf2 := lowerSerializeProfile(t, src, agentSerializeProfile(t))
		second := serializeBytes(t, wf2)
		if !bytes.Equal(first, second) {
			t.Errorf("%s: two serializations differ (nondeterministic output)", name)
		}
	}
}

// ── Agent profile helper ───────────────────────────────────────────

func agentSerializeProfile(t *testing.T) *passes.TargetProfile {
	t.Helper()
	param := func(name, typ string, required bool) *binding.AgentParameterTarget {
		p, err := binding.NewAgentParameterTarget(name, typ, required)
		if err != nil {
			t.Fatalf("NewAgentParameterTarget: %v", err)
		}
		return p
	}
	clock, err := binding.NewAgentToolTargetProfile(
		"clock", "builtin-time", "time", "builtin",
		"langgenius/time:1.0.0@sha256:time", "current_time", nil,
	)
	if err != nil {
		t.Fatalf("NewAgentToolTargetProfile: %v", err)
	}
	profile, err := binding.NewAgentTargetProfile(
		"function-calling-v1", "langgenius/agent", "function_calling", "Function Calling",
		"langgenius/agent:1.0.0@sha256:agent", "2",
		[]binding.AgentParameterTarget{
			*param("model", "model-selector", true),
			*param("query", "string", true),
			*param("instruction", "string", false),
			*param("tools", "tools-selector", true),
		},
		map[string]*binding.AgentToolTargetProfile{"clock": clock},
	)
	if err != nil {
		t.Fatalf("NewAgentTargetProfile: %v", err)
	}
	tp := passes.DefaultTargetProfile()
	tp.AgentProfiles = map[string]*binding.AgentTargetProfile{"function-calling-v1": profile}
	return tp
}

// TestSerializeFeaturesNonDefault locks a constructed-AST workflow with every
// non-default feature branch: FileUploadConfig with a full FileSizeLimits
// block (all 10 fields), an ImageUploadConfig, NumberLimits, TextToSpeech,
// all FeatureToggles and suggested questions. Oracle captured from Python
// (construct the same WorkflowFeatures in nclang.ast_nodes, serialize_to_dict,
// json.dumps(sort_keys=False); /tmp/oracle26/features_non_default.json).
func TestSerializeFeaturesNonDefault(t *testing.T) {
	fsc := &dify.FileSizeLimits{
		AttachmentImageFileSizeLimit: 3,
		AudioFileSizeLimit:           60,
		BatchCountLimit:              6,
		FileSizeLimit:                20,
		FileUploadLimit:              25,
		ImageFileBatchLimit:          12,
		ImageFileSizeLimit:           12,
		SingleChunkAttachmentLimit:   15,
		VideoFileSizeLimit:           120,
		WorkflowFileUploadLimit:      15,
	}
	wf := dify.NewWorkflowAST()
	wf.Features = dify.WorkflowFeatures{
		FileUpload: &dify.FileUploadConfig{
			Enabled:                  true,
			AllowedFileExtensions:    []string{".pdf", ".docx"},
			AllowedFileTypes:         []string{"document"},
			AllowedFileUploadMethods: []string{"local_file", "remote_url"},
			NumberLimits:             5,
			Image:                    &dify.ImageUploadConfig{Enabled: true, NumberLimits: 4, TransferMethods: []string{"local_file"}},
			FileLimits:               fsc,
		},
		OpeningStatement:              "Welcome",
		RetrieverResource:             dify.FeatureToggle{Enabled: true},
		SensitiveWordAvoidance:        dify.FeatureToggle{Enabled: true},
		SpeechToText:                  dify.FeatureToggle{Enabled: true},
		SuggestedQuestions:            []string{"q1", "q2"},
		SuggestedQuestionsAfterAnswer: dify.FeatureToggle{Enabled: true},
		TextToSpeech:                  &dify.TextToSpeechConfig{Enabled: true, Language: "zh-Hans", Voice: "voice-1"},
	}
	got := serializeBytes(t, wf)
	assertBytesEqual(t, "features_non_default", got, oracle26_features_non_default)
	assertValueEqual(t, "features_non_default", got, oracle26_features_non_default)
}

// TestSerializeConstructedLoopZero locks the Python dataclass defaults for a
// directly-built &LoopDataNode{} (R1/Important 2a): the Go zero value means
// "unset", so the serializer applies ast_nodes.py:538-540 defaults — loop_count
// 0→100, logical_operator ""→"and", error_handle_mode ""/"terminated" omitted.
// Go lowering always uses NewLoopDataNode (which carries the same defaults),
// so this only guards direct construction. Oracle: /tmp/oracle26/r1_loop_zero.json.
func TestSerializeConstructedLoopZero(t *testing.T) {
	nd := dify.NewNodeDecl()
	nd.ID = "loop_1"
	nd.NodeType = "loop"
	nd.Data = &dify.LoopDataNode{}
	wf := dify.NewWorkflowAST()
	wf.Graph.Nodes = []dify.NodeDecl{nd}
	got := serializeBytes(t, wf)
	assertBytesEqual(t, "loop_zero", got, "{\"app\":{\"mode\":\"workflow\",\"name\":\"\"},\"kind\":\"app\",\"version\":\"0.6.0\",\"workflow\":{\"conversation_variables\":[],\"environment_variables\":[],\"rag_pipeline_variables\":[],\"features\":{\"file_upload\":{},\"sensitive_word_avoidance\":{\"enabled\":false},\"text_to_speech\":{\"enabled\":false,\"language\":\"\",\"voice\":\"\"},\"opening_statement\":\"\",\"retriever_resource\":{\"enabled\":false},\"speech_to_text\":{\"enabled\":false},\"suggested_questions\":[],\"suggested_questions_after_answer\":{\"enabled\":false}},\"graph\":{\"nodes\":[{\"id\":\"loop_1\",\"type\":\"custom\",\"data\":{\"type\":\"loop\",\"loop_count\":100,\"logical_operator\":\"and\"}}],\"edges\":[],\"viewport\":{\"x\":0,\"y\":0,\"zoom\":1}}},\"dependencies\":[]}")
}

// TestSerializeConstructedCustomNoteShowAuthor locks the show_author pointer
// semantics (R1/Important 2b): nil → Python default True (ast_nodes.py:758);
// an explicit false is preserved. Oracle: /tmp/oracle26/r1_custom_note_zero.json
// and r1_custom_note_false.json.
func TestSerializeConstructedCustomNoteShowAuthor(t *testing.T) {
	build := func(nd dify.NodeData) []byte {
		decl := dify.NewNodeDecl()
		decl.ID = "n_1"
		decl.NodeType = "custom-note"
		decl.Data = nd
		wf := dify.NewWorkflowAST()
		wf.Graph.Nodes = []dify.NodeDecl{decl}
		return serializeBytes(t, wf)
	}
	got := build(&dify.CustomNoteDataNode{})
	assertBytesEqual(t, "custom_note_zero", got, "{\"app\":{\"mode\":\"workflow\",\"name\":\"\"},\"kind\":\"app\",\"version\":\"0.6.0\",\"workflow\":{\"conversation_variables\":[],\"environment_variables\":[],\"rag_pipeline_variables\":[],\"features\":{\"file_upload\":{},\"sensitive_word_avoidance\":{\"enabled\":false},\"text_to_speech\":{\"enabled\":false,\"language\":\"\",\"voice\":\"\"},\"opening_statement\":\"\",\"retriever_resource\":{\"enabled\":false},\"speech_to_text\":{\"enabled\":false},\"suggested_questions\":[],\"suggested_questions_after_answer\":{\"enabled\":false}},\"graph\":{\"nodes\":[{\"id\":\"n_1\",\"type\":\"custom-note\",\"data\":{\"type\":\"\",\"selected\":false,\"text\":\"\",\"author\":\"\",\"showAuthor\":true,\"theme\":\"\",\"height\":0,\"width\":0}}],\"edges\":[],\"viewport\":{\"x\":0,\"y\":0,\"zoom\":1}}},\"dependencies\":[]}")
	f := false
	got = build(&dify.CustomNoteDataNode{ShowAuthor: &f})
	assertBytesEqual(t, "custom_note_false", got, "{\"app\":{\"mode\":\"workflow\",\"name\":\"\"},\"kind\":\"app\",\"version\":\"0.6.0\",\"workflow\":{\"conversation_variables\":[],\"environment_variables\":[],\"rag_pipeline_variables\":[],\"features\":{\"file_upload\":{},\"sensitive_word_avoidance\":{\"enabled\":false},\"text_to_speech\":{\"enabled\":false,\"language\":\"\",\"voice\":\"\"},\"opening_statement\":\"\",\"retriever_resource\":{\"enabled\":false},\"speech_to_text\":{\"enabled\":false},\"suggested_questions\":[],\"suggested_questions_after_answer\":{\"enabled\":false}},\"graph\":{\"nodes\":[{\"id\":\"n_1\",\"type\":\"custom-note\",\"data\":{\"type\":\"\",\"selected\":false,\"text\":\"\",\"author\":\"\",\"showAuthor\":false,\"theme\":\"\",\"height\":0,\"width\":0}}],\"edges\":[],\"viewport\":{\"x\":0,\"y\":0,\"zoom\":1}}},\"dependencies\":[]}")
}

// TestSerializeConstructedModelCompletionParams locks the CompletionParams
// constructor contract (R1/Important 2c): NewCompletionParams sets
// temperature/top_p to 1.0 (Python defaults, ast_nodes.py:82-84), so a fresh
// model emits NO completion_params (serializer.py:102-114 skips the 1.0
// defaults). An explicitly-set temperature is a MEANINGFUL Python value and
// IS emitted. Serializer defaults are deliberately NOT applied to zero-value
// CompletionParams{} — Go lowering always builds models via the constructors,
// and Python's 0.0 (explicit) must survive. Oracle: /tmp/oracle26/r1_model_default.json
// and r1_model_temp.json.
func TestSerializeConstructedModelCompletionParams(t *testing.T) {
	build := func(m dify.ModelConfigNode) []byte {
		llm := dify.NewLLMDataNode()
		mm := m
		llm.Model = &mm
		llm.PromptTemplate = []dify.PromptMessageNode{{Role: "user", Text: "hi"}}
		decl := dify.NewNodeDecl()
		decl.ID = "llm_1"
		decl.NodeType = "llm"
		decl.Data = llm
		wf := dify.NewWorkflowAST()
		wf.Graph.Nodes = []dify.NodeDecl{decl}
		return serializeBytes(t, wf)
	}
	m := dify.NewModelConfigNode()
	m.Provider = "openai"
	m.Name = "gpt-4o"
	got := build(m)
	assertBytesEqual(t, "model_default", got, "{\"app\":{\"mode\":\"workflow\",\"name\":\"\"},\"kind\":\"app\",\"version\":\"0.6.0\",\"workflow\":{\"conversation_variables\":[],\"environment_variables\":[],\"rag_pipeline_variables\":[],\"features\":{\"file_upload\":{},\"sensitive_word_avoidance\":{\"enabled\":false},\"text_to_speech\":{\"enabled\":false,\"language\":\"\",\"voice\":\"\"},\"opening_statement\":\"\",\"retriever_resource\":{\"enabled\":false},\"speech_to_text\":{\"enabled\":false},\"suggested_questions\":[],\"suggested_questions_after_answer\":{\"enabled\":false}},\"graph\":{\"nodes\":[{\"id\":\"llm_1\",\"type\":\"custom\",\"data\":{\"type\":\"llm\",\"model\":{\"provider\":\"openai\",\"name\":\"gpt-4o\",\"mode\":\"chat\"},\"prompt_template\":[{\"role\":\"user\",\"text\":\"hi\"}]}}],\"edges\":[],\"viewport\":{\"x\":0,\"y\":0,\"zoom\":1}}},\"dependencies\":[]}")

	m2 := dify.NewModelConfigNode()
	m2.Provider = "openai"
	m2.Name = "gpt-4o"
	m2.CompletionParams.Temperature = 0.5
	got = build(m2)
	assertBytesEqual(t, "model_temp", got, "{\"app\":{\"mode\":\"workflow\",\"name\":\"\"},\"kind\":\"app\",\"version\":\"0.6.0\",\"workflow\":{\"conversation_variables\":[],\"environment_variables\":[],\"rag_pipeline_variables\":[],\"features\":{\"file_upload\":{},\"sensitive_word_avoidance\":{\"enabled\":false},\"text_to_speech\":{\"enabled\":false,\"language\":\"\",\"voice\":\"\"},\"opening_statement\":\"\",\"retriever_resource\":{\"enabled\":false},\"speech_to_text\":{\"enabled\":false},\"suggested_questions\":[],\"suggested_questions_after_answer\":{\"enabled\":false}},\"graph\":{\"nodes\":[{\"id\":\"llm_1\",\"type\":\"custom\",\"data\":{\"type\":\"llm\",\"model\":{\"provider\":\"openai\",\"name\":\"gpt-4o\",\"mode\":\"chat\",\"completion_params\":{\"temperature\":0.5}},\"prompt_template\":[{\"role\":\"user\",\"text\":\"hi\"}]}}],\"edges\":[],\"viewport\":{\"x\":0,\"y\":0,\"zoom\":1}}},\"dependencies\":[]}")
}

// TestOrderedMapLineSeparators locks Python byte parity for U+2028/U+2029
// (R1/Minor 4): json.dumps(ensure_ascii=False) emits them raw, while Go's
// encoding/json always escapes them; noEscapeMarshal rewrites the escapes
// back to UTF-8. A literal "\\u2028" text (escaped backslash) must survive
// untouched.
func TestOrderedMapLineSeparators(t *testing.T) {
	om := dify.NewOrderedMap()
	om.Set("code", "line\u2028sep\u2029")
	om.Set("literal", "\\u2028 not a real escape")
	b, err := om.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	want := "{\"code\":\"line\u2028sep\u2029\",\"literal\":\"\\\\u2028 not a real escape\"}"
	if string(b) != want {
		t.Errorf("MarshalJSON = %q, want %q (raw U+2028/U+2029)", b, want)
	}
}
