package dify

// Ports of the Dify workflow AST node surface (reference:
// 上游 Python 参考实现（nclang）/src/nclang/ast_nodes.py, 790 lines — authoritative).
// These tests assert structural parity (types, defaults, NODE_TYPE_MAP keys)
// so that lowering (Task 20+) and serialization (Task 26/27) can consume the
// Go AST with confidence. Every non-zero default assertion cites its Python
// ast_nodes.py line.

import (
	"reflect"
	"sort"
	"testing"
)

// TestNodeTypeMapKeys asserts the NodeTypeMap has exactly the 25 keys of the
// Python NODE_TYPE_MAP (ast_nodes.py:764-787), including iteration-start and
// custom-note (the plan document's "23 种" is approximate).
func TestNodeTypeMapKeys(t *testing.T) {
	want := []string{
		"start",
		"end",
		"answer",
		"llm",
		"if-else",
		"code",
		"http-request",
		"tool",
		"template-transform",
		"knowledge-retrieval",
		"question-classifier",
		"parameter-extractor",
		"loop",
		"loop-start",
		"loop-end",
		"variable-aggregator",
		"assigner",
		"document-extractor",
		"list-operator",
		"agent",
		"human-input",
		"datasource",
		"iteration",
		"iteration-start",
		"custom-note",
	}
	got := make([]string, 0, len(NodeTypeMap))
	for k := range NodeTypeMap {
		got = append(got, k)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NodeTypeMap keys mismatch:\n got %v\nwant %v", got, want)
	}
	// Every constructor must return a non-nil value implementing NodeData.
	for k, ctor := range NodeTypeMap {
		d := ctor()
		if d == nil {
			t.Errorf("NodeTypeMap[%q]: constructor returned nil", k)
			continue
		}
		if _, ok := d.(NodeData); !ok {
			t.Errorf("NodeTypeMap[%q]: %T does not implement NodeData", k, d)
		}
	}
}

// TestVariableSelectorNodePathDerivation ports VariableSelectorNode.__post_init__
// (ast_nodes.py:60-64) and from_list (ast_nodes.py:66-67).
func TestVariableSelectorNodePathDerivation(t *testing.T) {
	// path >= 2 → node_id, key = path[:2] (path kept intact).
	v := VariableSelectorNode{Path: []string{"llm-1", "output", "extra"}}
	v.Derive()
	if v.NodeID != "llm-1" || v.Key != "output" {
		t.Errorf("Derive from path: got node_id=%q key=%q, want %q/%q", v.NodeID, v.Key, "llm-1", "output")
	}
	if !reflect.DeepEqual(v.Path, []string{"llm-1", "output", "extra"}) {
		t.Errorf("Derive must keep full path, got %v", v.Path)
	}

	// node_id && key non-empty with empty path → path = [node_id, key].
	u := VariableSelectorNode{NodeID: "code-1", Key: "result"}
	u.Derive()
	if !reflect.DeepEqual(u.Path, []string{"code-1", "result"}) {
		t.Errorf("backfill path: got %v, want [code-1 result]", u.Path)
	}

	// path present but < 2, no node_id/key → untouched (matches Python elif).
	x := VariableSelectorNode{Path: []string{"solo"}}
	x.Derive()
	if !reflect.DeepEqual(x.Path, []string{"solo"}) || x.NodeID != "" || x.Key != "" {
		t.Errorf("short path with no node_id/key must stay untouched, got %+v", x)
	}

	// from_list equivalence (ast_nodes.py:66-67).
	w := VariableSelectorFromList([]string{"llm-1", "output"})
	if w.NodeID != "llm-1" || w.Key != "output" {
		t.Errorf("from_list: got node_id=%q key=%q", w.NodeID, w.Key)
	}
	if !reflect.DeepEqual(w.Path, []string{"llm-1", "output"}) {
		t.Errorf("from_list path: got %v", w.Path)
	}
	if a, b := VariableSelectorFromList([]string{"x", "y"}), NewVariableSelectorNode("x", "y"); !reflect.DeepEqual(a, b) {
		t.Errorf("from_list and NewVariableSelectorNode must be equivalent:\n%+v\n%+v", a, b)
	}
}

// TestDataNodeDefaults asserts constructor defaults for every non-zero Python
// default (ast_nodes.py field lines cited per row).
func TestDataNodeDefaults(t *testing.T) {
	tests := []struct {
		name string
		got  any
		want any
		py   string // ast_nodes.py reference
	}{
		// ── Envelope / app ──
		{"EnvelopeNode.kind", NewEnvelopeNode().Kind, "app", "ast_nodes.py:46"},
		{"AppNode.mode", NewAppNode().Mode, "workflow", "ast_nodes.py:35"},
		{"ViewportNode.zoom", NewViewportNode().Zoom, 1.0, "ast_nodes.py:29"},
		{"ConversationVariable.value_type", NewConversationVariable().ValueType, "string", "ast_nodes.py:99"},
		{"ConversationVariable.value (default empty string)", NewConversationVariable().Value, "", "ast_nodes.py:100"},
		{"EnvironmentVariable.value_type", NewEnvironmentVariable().ValueType, "string", "ast_nodes.py:112"},

		// ── Shared config nodes ──
		{"CompletionParams.temperature", NewCompletionParams().Temperature, 1.0, "ast_nodes.py:82"},
		{"CompletionParams.top_p", NewCompletionParams().TopP, 1.0, "ast_nodes.py:84"},
		{"ModelConfigNode.completion_params.temperature", NewModelConfigNode().CompletionParams.Temperature, 1.0, "ast_nodes.py:82"},
		{"MemoryConfig.enabled", NewMemoryConfig().Enabled, true, "ast_nodes.py:191"},
		{"MemoryConfig.window (union, default None)", NewMemoryConfig().Window, nil, "ast_nodes.py:193"},
		{"CaseNode.logical_operator", NewCaseNode().LogicalOperator, "and", "ast_nodes.py:139"},
		{"PromptMessageNode.role", NewPromptMessageNode().Role, "user", "ast_nodes.py:146"},
		{"OutputDefNode.type", NewOutputDefNode().Type, "string", "ast_nodes.py:152"},
		{"VariableDef.type", NewVariableDef().Type, "string", "ast_nodes.py:162"},
		{"VariableDef.options (default empty list)", len(NewVariableDef().Options), 0, "ast_nodes.py:165"},
		{"HTTPBody.type", NewHTTPBody().Type, "none", "ast_nodes.py:217"},
		{"TimeoutConfig.max_connect_timeout", NewTimeoutConfig().MaxConnectTimeout, 10, "ast_nodes.py:223"},
		{"TimeoutConfig.max_read_timeout", NewTimeoutConfig().MaxReadTimeout, 60, "ast_nodes.py:224"},
		{"AuthConfigDetail.type", NewAuthConfigDetail().Type, "basic", "ast_nodes.py:229"},
		{"AuthConfig.type", NewAuthConfig().Type, "no-auth", "ast_nodes.py:236"},
		{"RetryConfig.retry_interval", NewRetryConfig().RetryInterval, 1000, "ast_nodes.py:243"},
		{"AssignmentItem.operation", NewAssignmentItem().Operation, "set", "ast_nodes.py:250"},
		{"AssignmentItem.input_type", NewAssignmentItem().InputType, "variable", "ast_nodes.py:252"},
		{"ParameterDef.type", NewParameterDef().Type, "string", "ast_nodes.py:258"},
		{"ParameterDef.required", NewParameterDef().Required, true, "ast_nodes.py:260"},
		{"LoopVariable.value_type", NewLoopVariable().ValueType, "constant", "ast_nodes.py:275"},
		{"ListFilterItem.comparison_operator", NewListFilterItem().ComparisonOperator, "contains", "ast_nodes.py:293"},
		{"ListExtractConfig.serial", NewListExtractConfig().Serial, "1", "ast_nodes.py:306"},
		{"ListOrderConfig.value", NewListOrderConfig().Value, "asc", "ast_nodes.py:313"},
		{"ListLimitConfig.size", NewListLimitConfig().Size, -1, "ast_nodes.py:319"},
		{"StringSourceNode.type", NewStringSourceNode().Type, "constant", "ast_nodes.py:324"},
		{"StringListSourceNode.type", NewStringListSourceNode().Type, "constant", "ast_nodes.py:331"},
		{"ParagraphHumanInput.type", NewParagraphHumanInput("o").Type, "paragraph", "ast_nodes.py:340"},
		{"SelectHumanInput.type", NewSelectHumanInput("o", StringListSourceNode{}).Type, "select", "ast_nodes.py:347"},
		{"FileHumanInput.type", NewFileHumanInput("o").Type, "file", "ast_nodes.py:356"},
		{"FileListHumanInput.type", NewFileListHumanInput("o").Type, "file-list", "ast_nodes.py:366"},
		{"UserAction.button_style", NewUserAction().ButtonStyle, "default", "ast_nodes.py:390"},
		{"AgentInputNode (ctor required args)", NewAgentInputNode("x", "string"), &AgentInputNode{Value: "x", Type: "string"}, "ast_nodes.py:379-380"},

		// ── DataNode base ──
		{"DataNode.error_strategy (nil default)", NewStartDataNode().ErrorStrategy, (*string)(nil), "ast_nodes.py:410"},

		// ── Node data nodes ──
		{"CodeDataNode.code_language", NewCodeDataNode().CodeLanguage, "python3", "ast_nodes.py:455"},
		{"HTTPRequestDataNode.method", NewHTTPRequestDataNode().Method, "GET", "ast_nodes.py:464"},
		{"HTTPRequestDataNode.params (default dict)", NewHTTPRequestDataNode().Params, map[string]any{}, "ast_nodes.py:467"},
		{"LoopDataNode.loop_count", NewLoopDataNode().LoopCount, 100, "ast_nodes.py:538"},
		{"LoopDataNode.error_handle_mode", NewLoopDataNode().ErrorHandleMode, "terminated", "ast_nodes.py:539"},
		{"LoopDataNode.logical_operator", NewLoopDataNode().LogicalOperator, "and", "ast_nodes.py:540"},
		{"HumanInputDataNode.timeout", NewHumanInputDataNode().Timeout, 36, "ast_nodes.py:601"},
		{"HumanInputDataNode.timeout_unit", NewHumanInputDataNode().TimeoutUnit, "hour", "ast_nodes.py:602"},
		{"DatasourceDataNode.datasource_name", NewDatasourceDataNode().DatasourceName, "local_file", "ast_nodes.py:611"},
		{"IterationDataNode.parallel_nums", NewIterationDataNode().ParallelNums, 10, "ast_nodes.py:740"},
		{"IterationDataNode.error_handle_mode", NewIterationDataNode().ErrorHandleMode, "terminated", "ast_nodes.py:741"},
		{"CustomNoteDataNode.show_author", NewCustomNoteDataNode().ShowAuthor, boolPtr(true), "ast_nodes.py:758"},

		// ── Graph / features components ──
		{"EdgeDecl.type", NewEdgeDecl().Type, "custom", "ast_nodes.py:645"},
		{"EdgeDecl.source_handle", NewEdgeDecl().SourceHandle, "source", "ast_nodes.py:646"},
		{"NodeDecl.source_position", NewNodeDecl().SourcePosition, "right", "ast_nodes.py:628"},
		{"NodeDecl.target_position", NewNodeDecl().TargetPosition, "left", "ast_nodes.py:629"},
		{"ImageUploadConfig.number_limits", NewImageUploadConfig().NumberLimits, 3, "ast_nodes.py:670"},
		{"ImageUploadConfig.transfer_methods", NewImageUploadConfig().TransferMethods, []string{"local_file", "remote_url"}, "ast_nodes.py:671"},
		{"FileUploadConfig.number_limits", NewFileUploadConfig().NumberLimits, 3, "ast_nodes.py:694"},
		{"FileSizeLimits.attachment_image_file_size_limit", NewFileSizeLimits().AttachmentImageFileSizeLimit, 2, "ast_nodes.py:676"},
		{"FileSizeLimits.audio_file_size_limit", NewFileSizeLimits().AudioFileSizeLimit, 50, "ast_nodes.py:677"},
		{"FileSizeLimits.batch_count_limit", NewFileSizeLimits().BatchCountLimit, 5, "ast_nodes.py:678"},
		{"FileSizeLimits.file_size_limit", NewFileSizeLimits().FileSizeLimit, 15, "ast_nodes.py:679"},
		{"FileSizeLimits.file_upload_limit", NewFileSizeLimits().FileUploadLimit, 20, "ast_nodes.py:680"},
		{"FileSizeLimits.image_file_batch_limit", NewFileSizeLimits().ImageFileBatchLimit, 10, "ast_nodes.py:681"},
		{"FileSizeLimits.image_file_size_limit", NewFileSizeLimits().ImageFileSizeLimit, 10, "ast_nodes.py:682"},
		{"FileSizeLimits.single_chunk_attachment_limit", NewFileSizeLimits().SingleChunkAttachmentLimit, 10, "ast_nodes.py:683"},
		{"FileSizeLimits.video_file_size_limit", NewFileSizeLimits().VideoFileSizeLimit, 100, "ast_nodes.py:684"},
		{"FileSizeLimits.workflow_file_upload_limit", NewFileSizeLimits().WorkflowFileUploadLimit, 10, "ast_nodes.py:685"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !reflect.DeepEqual(tc.got, tc.want) {
				t.Errorf("%s: got %#v, want %#v (%s)", tc.name, tc.got, tc.want, tc.py)
			}
		})
	}
}

// TestWorkflowASTDefaults asserts NewWorkflowAST produces the Python defaults of
// ast_nodes.py WorkflowAST (ast_nodes.py:722-729): kind "app" envelope, empty
// graph, zero-value features.
func TestWorkflowASTDefaults(t *testing.T) {
	ast := NewWorkflowAST()
	if ast.Envelope.Kind != "app" {
		t.Errorf("envelope.kind = %q, want %q", ast.Envelope.Kind, "app")
	}
	if ast.Envelope.App.Mode != "workflow" {
		t.Errorf("envelope.app.mode = %q, want %q", ast.Envelope.App.Mode, "workflow")
	}
	if len(ast.Graph.Nodes) != 0 || len(ast.Graph.Edges) != 0 {
		t.Errorf("graph must start empty, got %d nodes %d edges", len(ast.Graph.Nodes), len(ast.Graph.Edges))
	}
	if ast.Graph.Viewport != nil {
		t.Errorf("graph.viewport must be nil, got %+v", ast.Graph.Viewport)
	}
	if !reflect.DeepEqual(ast.Features, WorkflowFeatures{}) {
		t.Errorf("features must be the zero value, got %+v", ast.Features)
	}
	if len(ast.ConversationVariables) != 0 || len(ast.EnvironmentVariables) != 0 ||
		len(ast.Dependencies) != 0 || len(ast.RagPipelineVariables) != 0 {
		t.Errorf("variable/dependency collections must start empty")
	}
}

// TestNodeDeclEdgeDeclDefaults asserts the graph-structure defaults of
// NodeDecl (ast_nodes.py:621-637) and EdgeDecl (ast_nodes.py:641-652).
func TestNodeDeclEdgeDeclDefaults(t *testing.T) {
	nd := NewNodeDecl()
	if nd.SourcePosition != "right" || nd.TargetPosition != "left" {
		t.Errorf("NodeDecl handles: source=%q target=%q, want right/left", nd.SourcePosition, nd.TargetPosition)
	}
	if nd.Data == nil {
		t.Error("NodeDecl.data must default to a base DataNode (ast_nodes.py:623)")
	} else if _, ok := nd.Data.(*DataNode); !ok {
		t.Errorf("NodeDecl.data default must be *DataNode, got %T", nd.Data)
	}
	if nd.Width != nil || nd.Height != nil || nd.ZIndex != nil || nd.PositionAbsolute != nil {
		t.Error("optional NodeDecl fields must default to nil")
	}

	ed := NewEdgeDecl()
	if ed.Type != "custom" || ed.SourceHandle != "source" {
		t.Errorf("EdgeDecl: type=%q source_handle=%q, want custom/source", ed.Type, ed.SourceHandle)
	}
	if ed.TargetHandle != nil || ed.Selected != nil || ed.IsInIteration != nil || ed.IsInLoop != nil {
		t.Error("optional EdgeDecl fields must default to nil")
	}
}

// TestBuildSampleGraph is the Task 20 smoke baseline: build a start → llm →
// answer workflow programmatically and verify ids/types/edges are reachable.
func TestBuildSampleGraph(t *testing.T) {
	ast := NewWorkflowAST()

	start := NewNodeDecl()
	start.ID = "start-1"
	start.NodeType = "start"
	start.Data = NewStartDataNode()
	start.Data.(*StartDataNode).Variables = []VariableDef{{Variable: "query"}}

	llm := NewNodeDecl()
	llm.ID = "llm-1"
	llm.NodeType = "llm"
	llm.Data = NewLLMDataNode()
	llm.Data.(*LLMDataNode).Title = "Translate" // embedded DataNode field via promotion

	answer := NewNodeDecl()
	answer.ID = "answer-1"
	answer.NodeType = "answer"
	answer.Data = NewAnswerNode()
	answer.Data.(*AnswerNode).Answer = "{{#llm-1.text#}}"

	ast.Graph.Nodes = []NodeDecl{start, llm, answer}
	ast.Graph.Edges = []EdgeDecl{
		{ID: "edge-1", Source: "start-1", Target: "llm-1", Type: "custom", SourceHandle: "source"},
		{ID: "edge-2", Source: "llm-1", Target: "answer-1", Type: "custom", SourceHandle: "source"},
	}

	if len(ast.Graph.Nodes) != 3 || len(ast.Graph.Edges) != 2 {
		t.Fatalf("graph: got %d nodes %d edges, want 3/2", len(ast.Graph.Nodes), len(ast.Graph.Edges))
	}
	if got := ast.Graph.Nodes[0]; got.ID != "start-1" || got.NodeType != "start" {
		t.Errorf("node[0]: id=%q type=%q, want start-1/start", got.ID, got.NodeType)
	}
	if got := ast.Graph.Nodes[1]; got.ID != "llm-1" || got.Data.(*LLMDataNode).Title != "Translate" {
		t.Errorf("node[1]: id=%q title=%q", got.ID, got.Data.(*LLMDataNode).Title)
	}
	if got := ast.Graph.Edges[1]; got.Source != "llm-1" || got.Target != "answer-1" {
		t.Errorf("edge[1]: %s -> %s, want llm-1 -> answer-1", got.Source, got.Target)
	}
	if ast.Envelope.Kind != "app" {
		t.Errorf("envelope.kind = %q", ast.Envelope.Kind)
	}
}
