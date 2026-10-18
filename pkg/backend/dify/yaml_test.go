//go:build shelved

// 搁置面（AGENTS.md「开发主线与平台优先级」2026-09-11 拍板）：dify / coze-studio /
// python golden 语料面的测试结果**不是**主线（coze-biz）回归判据。本文件按门禁治理
// `内部治理文档（G-B 条）` G-B「快照/搁置面不阻塞默认门禁」移到默认门禁之外，
// 以免"常驻红基线"淹没主线真信号（实测：30 条常驻红里 23 条是搁置面）。
//
// 跑它：`go test -tags shelved ./...`（或 `make shelved`）；分类：`scripts/gov/triage.py`。
// 摘掉本 tag 的条件 = 该面重新成为主线判据（届时先修红再摘）。

package dify_test

// yaml_test.go — Task 27 YAML emission tests. The target is BYTE parity with
// Python serializer.py serialize (:1111-1115):
//
//	pyyaml.dump(serialize_to_dict(ast), allow_unicode=True,
//	            default_flow_style=False, sort_keys=False)
//
// Oracle protocol: every expected YAML string below was captured from the
// Python reference FIRST (see /tmp/oracle27.py → /tmp/oracle26/*.yml and
// /tmp/t27_probe*.py → /tmp/t27_probe*_out.txt, 2026-08-14), never guessed.
// The extract/tool/agent fixtures embed the documented Task 26 deviation:
// Go emits map-backed payloads with SORTED keys (normalizeAny). tool and
// agent compare byte-for-byte against pyyaml's own output for the same
// sorted-key dicts (/tmp/oracle27-sorted/*.yml). extract's oracle
// (oracle27s_extract) is Go's own normalizeAny HYBRID (top-level outputs
// keys AND the nested `children` field dicts sorted — level-1 field dicts
// keep insertion order) that no single pyyaml dump reproduces, so that row
// is SEMANTIC-ONLY: byte-compared
// as a determinism/regression lock, with value equality against the true
// Python output verified via pyyaml.safe_load deep-compare
// (task-27-report.md R1).

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
)

// ── Harness ────────────────────────────────────────────────────────

func serializeYAML(t *testing.T, wf *dify.WorkflowAST) string {
	t.Helper()
	om, err := dify.SerializeToDict(wf)
	if err != nil {
		t.Fatalf("SerializeToDict: %v", err)
	}
	s, err := dify.SerializeYAML(om)
	if err != nil {
		t.Fatalf("SerializeYAML: %v", err)
	}
	return s
}

// ── Byte-parity over the 21 Task 26 fixtures ───────────────────────

type yamlFixture struct {
	name   string
	oracle string
	build  func(t *testing.T) *dify.WorkflowAST
}

// tplVarSrc assigns the template to a variable so BOTH Python and Go emit a
// template-transform node (byte parity). The templateSrc fixture returns the
// template literal, which Python inlines into the answer node (end_2) while
// Go emits template-transform + end — a pre-existing Task 26 lowering
// divergence locked by TestSerializeTemplate (JSON oracle) and documented in
// task-27-report.md.
const tplVarSrc = `function main(string name, int score) {
    let message = ` + "`Hello ${name}, score ${score}`" + `;
    output(message);
    return message;
}
`

// loopOracle substitutes the Python random uuid4 loop-variable id with the
// Go deterministic id (same pattern as TestSerializeLoop).
var loopOracle = strings.Replace(oracle27_loop,
	"40977302-1ce1-4a74-a07f-d09acd5811db",
	"f3036ebd-cc89-5d2a-97b6-293747878b90", 1)

func yamlFixtures() []yamlFixture {
	return []yamlFixture{
		{"minimal", oracle27_minimal, func(t *testing.T) *dify.WorkflowAST { return lowerSerialize(t, minimalSrc) }},
		{"start_params", oracle27_start_params, func(t *testing.T) *dify.WorkflowAST { return lowerSerialize(t, startParamsSrc) }},
		{"rag41", oracle27_rag41, func(t *testing.T) *dify.WorkflowAST { return lowerSerialize(t, rag41Src) }},
		{"http42", oracle27_http42, func(t *testing.T) *dify.WorkflowAST { return lowerSerialize(t, http42Src) }},
		{"tpl_var", oracle27_tpl_var, buildTplVarFixture},
		{"ifelse", oracle27_ifelse, func(t *testing.T) *dify.WorkflowAST { return lowerSerialize(t, ifElseSrc) }},
		{"extract_text", oracle27_extract_text, buildExtractTextFixture},
		{"for_expr", oracle27_for_expr, func(t *testing.T) *dify.WorkflowAST { return lowerSerialize(t, forExprSrc) }},
		{"classify", oracle27_classify, buildClassifyFixture},
		// tool/agent: map-backed payloads are SORTED in Go (Task 26 normalizeAny
		// deviation) → genuine pyyaml sorted-key oracles (oracle27s_tool/agent).
		// extract: oracle27s_extract is Go's own hybrid key order (see header)
		// → SEMANTIC-ONLY row (byte lock; values verified via pyyaml re-parse).
		{"extract", oracle27s_extract, func(t *testing.T) *dify.WorkflowAST { return lowerSerialize(t, extractSrc) }},
		// tool row migrated to the qualified direct-call form (Task 4): lowered
		// through the IR path (lowerSerializeIR) — the legacy ident-builtin
		// lowerer cannot dispatch google_search.search. The sorted-key oracle
		// is unchanged (IR render is byte-identical to legacy tool() output).
		{"tool", oracle27s_tool, func(t *testing.T) *dify.WorkflowAST { return lowerSerializeIR(t, toolSrc, googleSearchRegistry(t)) }},
		{"agent", oracle27s_agent, func(t *testing.T) *dify.WorkflowAST {
			return lowerSerializeProfile(t, agentSrc, agentSerializeProfile(t))
		}},
		{"loop", loopOracle, func(t *testing.T) *dify.WorkflowAST { return lowerSerialize(t, loopSrc) }},
		{"answer", oracle27_answer, func(t *testing.T) *dify.WorkflowAST { return buildAnswerFixture(t) }},
		{"human_input", oracle27_human_input, func(t *testing.T) *dify.WorkflowAST { return buildHumanInputFixture(t) }},
		{"datasource", oracle27_datasource, func(t *testing.T) *dify.WorkflowAST { return buildDatasourceFixture(t) }},
		{"list_operator", oracle27_list_operator, func(t *testing.T) *dify.WorkflowAST { return buildListOperatorFixture(t) }},
		{"custom_note", oracle27_custom_note, func(t *testing.T) *dify.WorkflowAST { return buildCustomNoteFixture(t) }},
		{"var_agg", oracle27_var_agg, func(t *testing.T) *dify.WorkflowAST { return buildVariableAggregatorFixture(t) }},
		{"edges_special", oracle27_edges_special, func(t *testing.T) *dify.WorkflowAST { return buildEdgesSpecialFixture(t) }},
		{"viewport_vars", oracle27_viewport_vars, func(t *testing.T) *dify.WorkflowAST { return buildViewportVarsFixture(t) }},
	}
}

func TestSerializeYAMLByteParity(t *testing.T) {
	for _, fx := range yamlFixtures() {
		fx := fx
		t.Run(fx.name, func(t *testing.T) {
			got := serializeYAML(t, fx.build(t))
			if got != fx.oracle {
				t.Errorf("%s: YAML byte mismatch\n--- want ---\n%s\n--- got ---\n%s", fx.name, fx.oracle, got)
			}
		})
	}
}

// ── Constructed AST fixtures (same constructions as serialize_test.go) ─

func buildAnswerFixture(t *testing.T) *dify.WorkflowAST {
	t.Helper()
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
	return wf
}

func buildHumanInputFixture(t *testing.T) *dify.WorkflowAST {
	t.Helper()
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
	return wf
}

func buildDatasourceFixture(t *testing.T) *dify.WorkflowAST {
	t.Helper()
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
	return wf
}

func buildListOperatorFixture(t *testing.T) *dify.WorkflowAST {
	t.Helper()
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
	return wf
}

func buildCustomNoteFixture(t *testing.T) *dify.WorkflowAST {
	t.Helper()
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
	return wf
}

func buildVariableAggregatorFixture(t *testing.T) *dify.WorkflowAST {
	t.Helper()
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
	return wf
}

func buildEdgesSpecialFixture(t *testing.T) *dify.WorkflowAST {
	t.Helper()
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
	return wf
}

// buildTplVarFixture constructs the tpl_var workflow (template + output
// ends) directly. The IR path allocates node ids in a different order than
// the frozen legacy lowering for this inline shape (template_3/output_2 vs
// template_2/output_3 — legacy emits the template node before the output
// end, IR emits the output end first); the constructed AST reproduces the
// legacy order byte-exactly so the serializer oracle keeps its lock. The
// serializer (not lowering) is the object under test here.
func buildTplVarFixture(t *testing.T) *dify.WorkflowAST {
	t.Helper()
	start := dify.MakeStartNode("start_1", []dify.VariableDef{
		{Variable: "name", Type: "text-input", Label: "name", Required: true},
		{Variable: "score", Type: "number", Label: "score", Required: true},
	})
	start.Data.(*dify.StartDataNode).Title = "User Input"
	tmpl := dify.MakeNodeDecl("template_2", "template-transform", &dify.TemplateTransformDataNode{
		Template:  "Hello {{#start_1.name#}}, score {{#start_1.score#}}",
		Variables: []dify.VariableSelectorNode{},
	})
	tmpl.Data.(*dify.TemplateTransformDataNode).Title = "Template"
	outputEnd := dify.MakeOutputNode("output_3", []dify.OutputMapping{
		{Variable: "message", ValueType: "string", ValueSelector: []string{"template_2", "output"}},
	})
	outputEnd.Data.(*dify.OutputNode).Title = "Output"
	end2 := dify.MakeOutputNode("end_4", []dify.OutputMapping{
		{Variable: "output", ValueType: "string", ValueSelector: []string{"template_2", "output"}},
	})
	end2.Data.(*dify.OutputNode).Title = "Output 2"
	wf := dify.NewWorkflowAST()
	wf.Graph.Nodes = []dify.NodeDecl{start, tmpl, outputEnd, end2}
	tgt := "target"
	wf.Graph.Edges = []dify.EdgeDecl{
		{ID: "edge_1", Source: "start_1", SourceHandle: "", Target: "template_2", TargetHandle: &tgt, Type: "custom"},
		{ID: "edge_2", Source: "template_2", SourceHandle: "", Target: "output_3", TargetHandle: &tgt, Type: "custom"},
		{ID: "edge_3", Source: "output_3", SourceHandle: "", Target: "end_4", TargetHandle: &tgt, Type: "custom"},
	}
	return wf
}

// buildExtractTextFixture constructs the extract_text workflow (start with a
// file input → document-extractor → end). The legacy lowering is the only
// producer of this shape and is being retired; the serializer oracle keeps
// its lock through the constructed AST.
func buildExtractTextFixture(t *testing.T) *dify.WorkflowAST {
	t.Helper()
	start := dify.MakeStartNode("start_1", []dify.VariableDef{
		{Variable: "doc", Type: "file", Label: "doc", Required: true,
			AllowedFileTypes:         []string{"document"},
			AllowedFileExtensions:    []string{".pdf"},
			AllowedFileUploadMethods: []string{}},
	})
	start.Data.(*dify.StartDataNode).Title = "User Input"
	sel := dify.VariableSelectorFromList([]string{"start_1", "doc"})
	ext := dify.MakeNodeDecl("extract_text_2", "document-extractor", &dify.DocumentExtractorDataNode{
		VariableSelector: &sel,
		IsArrayFile:      false,
	})
	ext.Data.(*dify.DocumentExtractorDataNode).Title = "Document Extractor"
	end := dify.MakeOutputNode("end_3", []dify.OutputMapping{
		{Variable: "output", ValueType: "string", ValueSelector: []string{"extract_text_2", "text"}},
	})
	end.Data.(*dify.OutputNode).Title = "Output"
	wf := dify.NewWorkflowAST()
	wf.Graph.Nodes = []dify.NodeDecl{start, ext, end}
	tgt := "target"
	wf.Graph.Edges = []dify.EdgeDecl{
		{ID: "edge_1", Source: "start_1", SourceHandle: "", Target: "extract_text_2", TargetHandle: &tgt, Type: "custom"},
		{ID: "edge_2", Source: "extract_text_2", SourceHandle: "", Target: "end_3", TargetHandle: &tgt, Type: "custom"},
	}
	return wf
}

// buildClassifyFixture constructs the classify workflow (start → question-
// classifier → per-class code adapters → variable-aggregator → end). Same
// retirement rationale as buildExtractTextFixture: the constructed AST
// locks the serializer against the legacy-produced oracle.
func buildClassifyFixture(t *testing.T) *dify.WorkflowAST {
	t.Helper()
	start := dify.MakeStartNode("start_1", []dify.VariableDef{
		{Variable: "query", Type: "text-input", Label: "query", Required: true},
	})
	start.Data.(*dify.StartDataNode).Title = "User Input"
	sel := dify.VariableSelectorFromList([]string{"start_1", "query"})
	model := dify.NewModelConfigNode()
	model.Provider = "provider"
	model.Name = "model"
	model.Mode = "chat"
	model.CompletionParams.Stop = []string{}
	model.CompletionParams.Extra = map[string]any{}
	instruction := "Classify the request"
	classes := []dify.ClassDef{
		{ID: "support", Name: "Technical support request", Label: ""},
		{ID: "unknown", Name: "Cannot determine intent", Label: ""},
		{ID: "billing", Name: "Billing request", Label: ""},
	}
	classifier := dify.MakeNodeDecl("classify_2", "question-classifier", &dify.QuestionClassifierDataNode{
		QueryVariableSelector: &sel,
		Model:                 &model,
		Classes:               classes,
		Instruction:           &instruction,
		Memory:                nil,
		Vision:                &dify.VisionConfig{Enabled: false, Configs: dify.NewOrderedMap()},
	})
	classifier.Data.(*dify.QuestionClassifierDataNode).Title = "Question Classifier"

	strOutput := func() dify.OutputDefNode {
		return dify.OutputDefNode{Type: "string", Children: nil}
	}
	adapters := []struct {
		id     string
		member string
		title  string
	}{
		{"classify_support_3", "support", "Code"},
		{"classify_unknown_4", "unknown", "Code 2"},
		{"classify_billing_5", "billing", "Code 3"},
	}
	adapterNodes := make([]dify.NodeDecl, 0, 3)
	adapterSelectors := make([]dify.VariableSelectorNode, 0, 3)
	for i, a := range adapters {
		code := "# ncoda:shim 1\ndef main() -> dict:\n    return {\"result\": \"" + a.member + "\"}"
		node := dify.MakeCodeNode(a.id, code, map[string]dify.OutputDefNode{"result": strOutput()}, []dify.VariableSelectorNode{})
		node.Data.(*dify.CodeDataNode).Title = a.title
		adapterNodes = append(adapterNodes, node)
		adapterSelectors = append(adapterSelectors, dify.VariableSelectorFromList([]string{a.id, "result"}))
		_ = i
	}
	outputType := "string"
	aggregator := dify.MakeVariableAggregatorNode("classify_result_6", adapterSelectors)
	aggregator.Data.(*dify.VariableAggregatorDataNode).OutputType = &outputType
	aggregator.Data.(*dify.VariableAggregatorDataNode).Title = "Variable Aggregator"
	end := dify.MakeOutputNode("end_7", []dify.OutputMapping{
		{Variable: "output", ValueType: "string", ValueSelector: []string{"classify_result_6", "output"}},
	})
	end.Data.(*dify.OutputNode).Title = "Output"

	wf := dify.NewWorkflowAST()
	wf.Graph.Nodes = []dify.NodeDecl{start, classifier}
	wf.Graph.Nodes = append(wf.Graph.Nodes, adapterNodes...)
	wf.Graph.Nodes = append(wf.Graph.Nodes, aggregator, end)
	tgt := "target"
	edges := []dify.EdgeDecl{
		{ID: "edge_1", Source: "start_1", SourceHandle: "", Target: "classify_2", TargetHandle: &tgt, Type: "custom"},
		{ID: "edge_2", Source: "classify_2", SourceHandle: "support", Target: "classify_support_3", TargetHandle: &tgt, Type: "custom"},
		{ID: "edge_3", Source: "classify_2", SourceHandle: "unknown", Target: "classify_unknown_4", TargetHandle: &tgt, Type: "custom"},
		{ID: "edge_4", Source: "classify_2", SourceHandle: "billing", Target: "classify_billing_5", TargetHandle: &tgt, Type: "custom"},
		{ID: "edge_5", Source: "classify_support_3", SourceHandle: "", Target: "classify_result_6", TargetHandle: &tgt, Type: "custom"},
		{ID: "edge_6", Source: "classify_unknown_4", SourceHandle: "", Target: "classify_result_6", TargetHandle: &tgt, Type: "custom"},
		{ID: "edge_7", Source: "classify_billing_5", SourceHandle: "", Target: "classify_result_6", TargetHandle: &tgt, Type: "custom"},
		{ID: "edge_8", Source: "classify_result_6", SourceHandle: "", Target: "end_7", TargetHandle: &tgt, Type: "custom"},
	}
	wf.Graph.Edges = edges
	return wf
}

func buildViewportVarsFixture(t *testing.T) *dify.WorkflowAST {
	t.Helper()
	wf := dify.NewWorkflowAST()
	wf.Graph.Viewport = &dify.ViewportNode{X: 2.0, Y: -1.5, Zoom: 0.75}
	wf.ConversationVariables = []dify.ConversationVariable{
		{ID: "cv_1", Name: "topic", ValueType: "number", Value: "42", Description: "desc", Selector: []string{"conversation", "topic"}},
	}
	wf.EnvironmentVariables = []dify.EnvironmentVariable{
		{ID: "ev_1", Name: "KEY", ValueType: "string", Value: "val", Description: "env desc"},
	}
	wf.RagPipelineVariables = []string{"{{#start_1.x#}}"}
	return wf
}

// ── Scalar style probes (pyyaml analyze_scalar / implicit resolver) ──

// oracle27Scalars is the byte-exact output of /tmp/t27_probe.py
// (pyyaml.dump of the probe dict, allow_unicode=True, default_flow_style=False,
// sort_keys=False). Values are all PYTHON STRINGS; keys are safe except the
// resolver-sensitive ones, which pyyaml quotes itself.
const oracle27Scalars = `empty: ''
space: ' '
leading space: '  hi'
trailing space: 'hi  '
'true': 'true'
'false': 'False'
'null': 'null'
'yes': 'yes'
'on': 'on'
'off': 'Off'
upper YES: 'YES'
intlike: '123'
floatlike: '1.5'
version: 0.6.0
exp: 1e5
hex: '0x1F'
leadzero: '0123'
neg: '-5'
plus: '+12'
colon space: 'a: b'
trailing colon: 'abc:'
hash: '#comment'
amp: '&anchor'
star: '*alias'
bang: '!tag'
pipe: '|x'
gt: '>x'
pct: '%x'
at: '@x'
backtick: '` + "`" + `x'
dquote: '"hi"'
squote mid: it's
squote start: '''quoted'''
comma: ',x'
qmark: ?x
lbrace: '{a: 1}'
rbrace: x}
lbracket: '[1, 2]'
rbracket: x]
dash space: '- item'
dash mid: a-b
under: _under
star mid: a*b
bang mid: a!b
hash mid: a#b
hash after space: 'a #b'
question mid: a?b
colon no space: a:b
unicode: 你好 Hello
html: a < b && c > d
quoted colon: a ':b'
single quote pair: ''''''
double quotes: say "hi" there
newline mid: 'line1

  line2'
newline blank: 'line1


  line2'
trailing newline: 'line1

  '
lead newline: '

  line1'
nested quotes: it's "fine"
yaml doc: '---

  foo'
ellipsis: '...

  foo'
tab: "a\tb"
date: '2024-01-01'
time: '12:30:45'
uuid: f3036ebd-cc89-5d2a-97b6-293747878b90
caret: ^x
tilda: ~x
tilde alone: '~'
brace mid: a{b
bracket mid: a[b
comma mid: a,b
semi: a;b
eq: a=b
`

func TestSerializeYAMLScalars(t *testing.T) {
	om := dify.NewOrderedMap()
	// Keys must be inserted in the SAME order as the Python probe dict
	// (sort_keys=False preserves insertion order).
	for _, kv := range []struct{ k, v string }{
		{"empty", ""},
		{"space", " "},
		{"leading space", "  hi"},
		{"trailing space", "hi  "},
		{"true", "true"},
		{"false", "False"},
		{"null", "null"},
		{"yes", "yes"},
		{"on", "on"},
		{"off", "Off"},
		{"upper YES", "YES"},
		{"intlike", "123"},
		{"floatlike", "1.5"},
		{"version", "0.6.0"},
		{"exp", "1e5"},
		{"hex", "0x1F"},
		{"leadzero", "0123"},
		{"neg", "-5"},
		{"plus", "+12"},
		{"colon space", "a: b"},
		{"trailing colon", "abc:"},
		{"hash", "#comment"},
		{"amp", "&anchor"},
		{"star", "*alias"},
		{"bang", "!tag"},
		{"pipe", "|x"},
		{"gt", ">x"},
		{"pct", "%x"},
		{"at", "@x"},
		{"backtick", "`x"},
		{"dquote", `"hi"`},
		{"squote mid", "it's"},
		{"squote start", "'quoted'"},
		{"comma", ",x"},
		{"qmark", "?x"},
		{"lbrace", "{a: 1}"},
		{"rbrace", "x}"},
		{"lbracket", "[1, 2]"},
		{"rbracket", "x]"},
		{"dash space", "- item"},
		{"dash mid", "a-b"},
		{"under", "_under"},
		{"star mid", "a*b"},
		{"bang mid", "a!b"},
		{"hash mid", "a#b"},
		{"hash after space", "a #b"},
		{"question mid", "a?b"},
		{"colon no space", "a:b"},
		{"unicode", "你好 Hello"},
		{"html", "a < b && c > d"},
		{"quoted colon", "a ':b'"},
		{"single quote pair", "''"},
		{"double quotes", `say "hi" there`},
		{"newline mid", "line1\nline2"},
		{"newline blank", "line1\n\nline2"},
		{"trailing newline", "line1\n"},
		{"lead newline", "\nline1"},
		{"nested quotes", `it's "fine"`},
		{"yaml doc", "---\nfoo"},
		{"ellipsis", "...\nfoo"},
		{"tab", "a\tb"},
		{"date", "2024-01-01"},
		{"time", "12:30:45"},
		{"uuid", "f3036ebd-cc89-5d2a-97b6-293747878b90"},
		{"caret", "^x"},
		{"tilda", "~x"},
		{"tilde alone", "~"},
		{"brace mid", "a{b"},
		{"bracket mid", "a[b"},
		{"comma mid", "a,b"},
		{"semi", "a;b"},
		{"eq", "a=b"},
	} {
		om.Set(kv.k, kv.v)
	}
	got, err := dify.SerializeYAML(om)
	if err != nil {
		t.Fatalf("SerializeYAML: %v", err)
	}
	if got != oracle27Scalars {
		t.Errorf("scalar probe byte mismatch\n--- want ---\n%s\n--- got ---\n%s", oracle27Scalars, got)
	}
}

// ── Structural probes: nesting, line breaks, 80-col wraps, floats ───

// oracle27Probes2 is the byte-exact output of /tmp/t27_probe2.py.
const oracle27Probes2 = `=== nested_multiline ===
m:
  k: 'line1


    line2'
=== seq_multiline ===
- 'line1


  line2'
=== seq_in_seq ===
- - a
  - b
- - c
=== lsep ===
k: 'line1   line2   '
=== long_plain ===
k: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  cccccccccccccccccccccccccccccc
=== long_sq ===
k: '''xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx yyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyy
  zzzzzzzzzzzzzzzzzzzzzzzzzzzzzz'''
=== long_sq_ml ===
k: 'line1 wwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwww line2

  line3 vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv'
=== key_special ===
'#k': 1
a:b: 2
=== floats ===
f1: 0.5
f2: -1.5
f3: 0.0
f4: 1500.0
f5: 1.0e+17
f6: 1.0e-05
f7: 0.75
f8: 9990000000000000.0
=== ints ===
i1: 0
i2: -5
i3: 42
i4: 9999999999999999
=== empties ===
a: []
b: {}
c:
- []
- {}
d:
  e: []
  f: {}
=== boolnull ===
t: true
f: false
n: null
=== unicode_key ===
你好: 世界
`

func TestSerializeYAMLProbes2(t *testing.T) {
	sections := splitOracleSections(oracle27Probes2)
	// 1. nested multiline mapping (depth-2 continuation indent).
	{
		inner := dify.NewOrderedMap()
		inner.Set("k", "line1\n\nline2")
		om := dify.NewOrderedMap()
		om.Set("m", inner)
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		want := sections["nested_multiline"]
		if got != want {
			t.Errorf("nested_multiline mismatch\n--- want ---\n%s\n--- got ---\n%s", want, got)
		}
	}
	// 2. sequence-item multiline scalar (dash at col 0).
	{
		om := dify.NewOrderedMap()
		om.Set("seq", []any{"line1\n\nline2"})
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		want := "seq:\n" + sections["seq_multiline"]
		if got != want {
			t.Errorf("seq_multiline mismatch\n--- want ---\n%s\n--- got ---\n%s", want, got)
		}
	}
	// 3. nested seq in seq.
	{
		om := dify.NewOrderedMap()
		om.Set("s", []any{[]any{"a", "b"}, []any{"c"}})
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		want := "s:\n" + sections["seq_in_seq"]
		if got != want {
			t.Errorf("seq_in_seq mismatch\n--- want ---\n%s\n--- got ---\n%s", want, got)
		}
	}
	// 4. U+2028/U+2029 in single-quoted (raw char + 2-space continuation).
	{
		om := dify.NewOrderedMap()
		om.Set("k", "line1\u2028line2\u2029")
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		want := sections["lsep"]
		if got != want {
			t.Errorf("lsep mismatch\n--- want ---\n%q\n--- got ---\n%q", want, got)
		}
	}
	// 5. long plain wraps at 80 with single-space continuation indent.
	{
		om := dify.NewOrderedMap()
		om.Set("k", strings.Repeat("a", 40)+" "+strings.Repeat("b", 50)+" "+strings.Repeat("c", 30))
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		want := sections["long_plain"]
		if got != want {
			t.Errorf("long_plain mismatch\n--- want ---\n%s\n--- got ---\n%s", want, got)
		}
	}
	// 6. long single-quoted wraps with no leading space on continuation.
	{
		om := dify.NewOrderedMap()
		om.Set("k", "'"+strings.Repeat("x", 40)+" "+strings.Repeat("y", 50)+" "+strings.Repeat("z", 30)+"'")
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		want := sections["long_sq"]
		if got != want {
			t.Errorf("long_sq mismatch\n--- want ---\n%s\n--- got ---\n%s", want, got)
		}
	}
	// 7. long single-quoted with an embedded newline.
	{
		om := dify.NewOrderedMap()
		om.Set("k", "line1 "+strings.Repeat("w", 60)+" line2\nline3 "+strings.Repeat("v", 60))
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		want := sections["long_sq_ml"]
		if got != want {
			t.Errorf("long_sq_ml mismatch\n--- want ---\n%s\n--- got ---\n%s", want, got)
		}
	}
	// 8. keys starting with specials / containing ':' stay plain-safe.
	{
		om := dify.NewOrderedMap()
		om.Set("#k", 1)
		om.Set("a:b", 2)
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		want := sections["key_special"]
		if got != want {
			t.Errorf("key_special mismatch\n--- want ---\n%s\n--- got ---\n%s", want, got)
		}
	}
	// 9. float emission (Representer.represent_float parity).
	{
		om := dify.NewOrderedMap()
		om.Set("f1", 0.5)
		om.Set("f2", -1.5)
		om.Set("f3", 0.0)
		om.Set("f4", 1500.0)
		om.Set("f5", 1e17)
		om.Set("f6", 1e-5)
		om.Set("f7", 0.75)
		om.Set("f8", 9.99e15)
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		want := sections["floats"]
		if got != want {
			t.Errorf("floats mismatch\n--- want ---\n%s\n--- got ---\n%s", want, got)
		}
	}
	// 10. int emission.
	{
		om := dify.NewOrderedMap()
		om.Set("i1", 0)
		om.Set("i2", -5)
		om.Set("i3", 42)
		om.Set("i4", 9999999999999999)
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		want := sections["ints"]
		if got != want {
			t.Errorf("ints mismatch\n--- want ---\n%s\n--- got ---\n%s", want, got)
		}
	}
	// 11. empty containers flow style.
	{
		om := dify.NewOrderedMap()
		om.Set("a", []any{})
		om.Set("b", dify.NewOrderedMap())
		om.Set("c", []any{[]any{}, dify.NewOrderedMap()})
		inner := dify.NewOrderedMap()
		inner.Set("e", []any{})
		inner.Set("f", dify.NewOrderedMap())
		om.Set("d", inner)
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		want := sections["empties"]
		if got != want {
			t.Errorf("empties mismatch\n--- want ---\n%s\n--- got ---\n%s", want, got)
		}
	}
	// 12. bool/null emission.
	{
		om := dify.NewOrderedMap()
		om.Set("t", true)
		om.Set("f", false)
		om.Set("n", nil)
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		want := sections["boolnull"]
		if got != want {
			t.Errorf("boolnull mismatch\n--- want ---\n%s\n--- got ---\n%s", want, got)
		}
	}
	// 13. unicode keys (allow_unicode=True).
	{
		om := dify.NewOrderedMap()
		om.Set("你好", "世界")
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		want := sections["unicode_key"]
		if got != want {
			t.Errorf("unicode_key mismatch\n--- want ---\n%s\n--- got ---\n%s", want, got)
		}
	}
}

// splitOracleSections splits the probe2 oracle text into per-section strings.
func splitOracleSections(text string) map[string]string {
	out := map[string]string{}
	var cur string
	var curName string
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1] // drop the trailing artifact of the final \n
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "=== ") && strings.HasSuffix(line, " ===") {
			if curName != "" {
				out[curName] = cur // keep the trailing newline (emission always ends with \n)
			}
			curName = line[4 : len(line)-4]
			cur = ""
			continue
		}
		if curName != "" {
			cur += line + "\n"
		}
	}
	if curName != "" {
		out[curName] = cur
	}
	return out
}

// oracle27ComplexKeys is the byte-exact pyyaml output of the R1 complex-key
// probes (/tmp/t27_r1_final.py, 2026-08-14): keys that fail PyYAML's
// check_simple_key (empty / multiline / length >= 123 with the implicit
// "!!str" tag) are emitted in the complex `? key` / `: value` form, with
// non-indentless values (nested mapping keys and sequence dashes at
// indent+2), and U+10FFFF is treated as special (double-quoted).
const oracle27ComplexKeys = `=== long128 ===
? kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk
: v
=== empty ===
? ''
: 1
=== multiline ===
? 'a

  b'
: 1
=== nested_map ===
m:
  ? kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk
  : a: 1
    b: 2
=== nested_seq ===
m:
  ? kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk
  : - 1
    - 2
=== u10ffff ===
k: "\U0010FFFF"
=== boundary_122 ===
kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk: v
=== boundary_123 ===
? kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk
: v
=== nl_key_short ===
? "a\n\n  b"
: 1`

// TestSerializeYAMLComplexKeys locks the PyYAML complex-key (`? key` / `: value`)
// form: keys that fail check_simple_key (empty, multiline, or plain length >=
// 123 = 128 minus the 5-char implicit "!!str" tag) switch to the complex form,
// and complex-form values are NON-indentless (nested mapping keys at indent+2,
// sequence dashes at indent+2). Oracles captured from pyyaml 2026-08-14 R1.
func TestSerializeYAMLComplexKeys(t *testing.T) {
	sections := splitOracleSections(oracle27ComplexKeys)
	k128 := strings.Repeat("k", 128)
	k122 := strings.Repeat("k", 122)
	k123 := strings.Repeat("k", 123)

	// 1. plain key at the simple-key length limit → complex form.
	{
		om := dify.NewOrderedMap()
		om.Set(k128, "v")
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		if got != sections["long128"] {
			t.Errorf("long128 mismatch\n--- want ---\n%s\n--- got ---\n%s", sections["long128"], got)
		}
	}
	// 2. empty key → complex form with '' key.
	{
		om := dify.NewOrderedMap()
		om.Set("", 1)
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		if got != sections["empty"] {
			t.Errorf("empty mismatch\n--- want ---\n%s\n--- got ---\n%s", sections["empty"], got)
		}
	}
	// 3. multiline key → complex form with single-quoted key.
	{
		om := dify.NewOrderedMap()
		om.Set("a\nb", 1)
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		if got != sections["multiline"] {
			t.Errorf("multiline mismatch\n--- want ---\n%s\n--- got ---\n%s", sections["multiline"], got)
		}
	}
	// 4. multiline key with space-after-break → double-quoted complex key
	// (space_break forbids single quotes).
	{
		om := dify.NewOrderedMap()
		om.Set("a\n\n  b", 1)
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		if got != sections["nl_key_short"] {
			t.Errorf("nl_key_short mismatch\n--- want ---\n%q\n--- got ---\n%q", sections["nl_key_short"], got)
		}
	}
	// 5. boundary: 122-char key stays a simple key; 123-char key goes complex.
	{
		om := dify.NewOrderedMap()
		om.Set(k122, "v")
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		if got != sections["boundary_122"] {
			t.Errorf("boundary_122 mismatch\n--- want ---\n%s\n--- got ---\n%s", sections["boundary_122"], got)
		}
	}
	{
		om := dify.NewOrderedMap()
		om.Set(k123, "v")
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		if got != sections["boundary_123"] {
			t.Errorf("boundary_123 mismatch\n--- want ---\n%s\n--- got ---\n%s", sections["boundary_123"], got)
		}
	}
	// 6. complex value: nested mapping keys at indent+2 (NOT indentless).
	{
		innerV := dify.NewOrderedMap()
		innerV.Set("a", 1)
		innerV.Set("b", 2)
		inner := dify.NewOrderedMap()
		inner.Set(k128, innerV)
		om := dify.NewOrderedMap()
		om.Set("m", inner)
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		if got != sections["nested_map"] {
			t.Errorf("nested_map mismatch\n--- want ---\n%s\n--- got ---\n%s", sections["nested_map"], got)
		}
	}
	// 7. complex value: nested sequence dashes at indent+2 (non-indentless).
	{
		inner := dify.NewOrderedMap()
		inner.Set(k128, []any{1, 2})
		om := dify.NewOrderedMap()
		om.Set("m", inner)
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		if got != sections["nested_seq"] {
			t.Errorf("nested_seq mismatch\n--- want ---\n%s\n--- got ---\n%s", sections["nested_seq"], got)
		}
	}
	// 8. U+10FFFF: PyYAML marks it special (ch < '\U0010ffff') → double-quoted.
	{
		om := dify.NewOrderedMap()
		om.Set("k", "\U0010FFFF")
		got, err := dify.SerializeYAML(om)
		if err != nil {
			t.Fatal(err)
		}
		if got != sections["u10ffff"] {
			t.Errorf("u10ffff mismatch\n--- want ---\n%q\n--- got ---\n%q", sections["u10ffff"], got)
		}
	}
}

// TestSerializeYAMLNil: SerializeYAML(nil) must return an error, never panic.
func TestSerializeYAMLNil(t *testing.T) {
	if _, err := dify.SerializeYAML(nil); err == nil {
		t.Fatal("SerializeYAML(nil) = nil error, want error")
	}
}

// ── Determinism (hard constraint) ──────────────────────────────────

// TestSerializeYAMLDeterminism renders the same AST twice and requires
// identical bytes. The agent/extract fixtures carry map-backed payloads
// (sorted-key emission) and the loop fixture the uuid5 id — all must be
// stable across runs.
func TestSerializeYAMLDeterminism(t *testing.T) {
	for _, name := range []string{"minimal", "agent", "extract", "tool", "loop", "rag41"} {
		name := name
		t.Run(name, func(t *testing.T) {
			wf := func() *dify.WorkflowAST {
				switch name {
				case "agent":
					return lowerSerializeProfile(t, agentSrc, agentSerializeProfile(t))
				case "tool":
					// Qualified direct-call form (Task 4): IR path, same as
					// the byte-parity row.
					return lowerSerializeIR(t, toolSrc, googleSearchRegistry(t))
				default:
					return lowerSerialize(t, fixtureSrcFor(name))
				}
			}()
			first := serializeYAML(t, wf)
			second := serializeYAML(t, wf)
			if first != second {
				t.Errorf("%s: YAML emission not deterministic\n--- first ---\n%s\n--- second ---\n%s", name, first, second)
			}
		})
	}
}

func fixtureSrcFor(name string) string {
	switch name {
	case "minimal":
		return minimalSrc
	case "extract":
		return extractSrc
	case "tool":
		return toolSrc
	case "loop":
		return loopSrc
	case "rag41":
		return rag41Src
	default:
		panic("unknown fixture " + name)
	}
}

// TestSerializeYAMLEmptyRoot: pyyaml yaml.dump({}) emits "{}\n" (empty
// mapping check → flow style), never a bare newline. Root documents with
// zero keys must match PyYAML's check_empty_mapping behavior.
func TestSerializeYAMLEmptyRoot(t *testing.T) {
	om := dify.NewOrderedMap()
	got, err := dify.SerializeYAML(om)
	if err != nil {
		t.Fatal(err)
	}
	if got != "{}\n" {
		t.Errorf("empty root mismatch\n--- want ---\n%q\n--- got ---\n%q", "{}\n", got)
	}
}
