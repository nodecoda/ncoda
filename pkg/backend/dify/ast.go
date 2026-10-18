// Package dify defines the Dify WorkflowAST node surface — the Go port of the
// authoritative reference 上游 Python 参考实现（nclang）/src/nclang/ast_nodes.py (790
// lines). This is the build-phase AST: lowering (Task 20+) constructs and
// mutates it freely, so structs use exported fields (JSON-model style) with
// constructors as pure default-value convenience, unlike the source AST's
// immutable contract. Serialization to the Dify wire format is a later task
// (Task 26/27); yaml tags here are plain struct tags (no yaml import).
package dify

// ── Top-level AST (ast_nodes.py:722-729) ───────────────────────────

// WorkflowAST is the root of a Dify workflow program.
type WorkflowAST struct {
	Envelope              EnvelopeNode           `yaml:"envelope"`
	Graph                 GraphNode              `yaml:"graph"`
	Features              WorkflowFeatures       `yaml:"features"`
	ConversationVariables []ConversationVariable `yaml:"conversation_variables"`
	EnvironmentVariables  []EnvironmentVariable  `yaml:"environment_variables"`
	Dependencies          []map[string]any       `yaml:"dependencies"`
	RagPipelineVariables  []string               `yaml:"rag_pipeline_variables"`
}

// EnvelopeNode mirrors ast_nodes.py EnvelopeNode (ast_nodes.py:44-47):
// version/kind/app, with kind defaulting to "app".
type EnvelopeNode struct {
	Version string  `yaml:"version"`
	Kind    string  `yaml:"kind"`
	App     AppNode `yaml:"app"`
}

// AppNode mirrors ast_nodes.py AppNode (ast_nodes.py:33-40).
type AppNode struct {
	Name                string `yaml:"name"`
	Mode                string `yaml:"mode"`
	Icon                string `yaml:"icon"`
	IconType            string `yaml:"icon_type"`
	IconBackground      string `yaml:"icon_background"`
	Description         string `yaml:"description"`
	UseIconAsAnswerIcon bool   `yaml:"use_icon_as_answer_icon"`
}

// ── Graph structure (ast_nodes.py:621-658) ─────────────────────────

// GraphNode mirrors ast_nodes.py GraphNode (ast_nodes.py:656-658).
type GraphNode struct {
	Nodes    []NodeDecl    `yaml:"nodes"`
	Edges    []EdgeDecl    `yaml:"edges"`
	Viewport *ViewportNode `yaml:"viewport"`
}

// NodeDecl is one React-Flow-style node declaration
// (ast_nodes.py NodeDecl, :621-637). Data holds the concrete per-type data
// node (start/llm/if-else/...) as a NodeData.
type NodeDecl struct {
	ID               string        `yaml:"id"`
	NodeType         string        `yaml:"node_type"`
	Data             NodeData      `yaml:"data"`
	Position         PositionNode  `yaml:"position"`
	Width            *float64      `yaml:"width"`
	Height           *float64      `yaml:"height"`
	SourcePosition   string        `yaml:"source_position"`
	TargetPosition   string        `yaml:"target_position"`
	PositionAbsolute *PositionNode `yaml:"position_absolute"`
	Selected         bool          `yaml:"selected"`
	ZIndex           *int          `yaml:"z_index"`
	ParentID         *string       `yaml:"parent_id"`
	Extent           *string       `yaml:"extent"`
	Draggable        *bool         `yaml:"draggable"`
	Selectable       *bool         `yaml:"selectable"`
	Dragging         *bool         `yaml:"dragging"`
}

// EdgeDecl is one React-Flow-style edge declaration
// (ast_nodes.py EdgeDecl, :641-652).
type EdgeDecl struct {
	ID            string  `yaml:"id"`
	Source        string  `yaml:"source"`
	Target        string  `yaml:"target"`
	Type          string  `yaml:"type"`
	SourceHandle  string  `yaml:"source_handle"`
	TargetHandle  *string `yaml:"target_handle"`
	ZIndex        int     `yaml:"z_index"`
	Selected      *bool   `yaml:"selected"`
	IsInIteration *bool   `yaml:"is_in_iteration"` // nil = not present in original
	IsInLoop      *bool   `yaml:"is_in_loop"`
	LoopID        string  `yaml:"loop_id"`
	// IterationID is the iteration-container owner of an inner edge, mirroring
	// the Python `edge._iteration_id` dynamic attribute (flow_lowerer.py:1008,
	// serializer.py:976-977). It is only meaningful when IsInIteration is set.
	IterationID string `yaml:"iteration_id"`
}

// ── Viewport / position (ast_nodes.py:19-29) ───────────────────────

// PositionNode mirrors ast_nodes.py PositionNode (ast_nodes.py:20-21).
type PositionNode struct {
	X float64 `yaml:"x"`
	Y float64 `yaml:"y"`
}

// ViewportNode mirrors ast_nodes.py ViewportNode (ast_nodes.py:26-29).
type ViewportNode struct {
	X    float64 `yaml:"x"`
	Y    float64 `yaml:"y"`
	Zoom float64 `yaml:"zoom"`
}

// ── Workflow features (ast_nodes.py:662-714) ───────────────────────

// FeatureToggle mirrors ast_nodes.py FeatureToggle (ast_nodes.py:663-664).
type FeatureToggle struct {
	Enabled bool `yaml:"enabled"`
}

// ImageUploadConfig mirrors ast_nodes.py ImageUploadConfig (ast_nodes.py:668-671).
type ImageUploadConfig struct {
	Enabled         bool     `yaml:"enabled"`
	NumberLimits    int      `yaml:"number_limits"`
	TransferMethods []string `yaml:"transfer_methods"`
}

// FileSizeLimits mirrors ast_nodes.py FileSizeLimits (ast_nodes.py:675-685).
type FileSizeLimits struct {
	AttachmentImageFileSizeLimit int `yaml:"attachment_image_file_size_limit"`
	AudioFileSizeLimit           int `yaml:"audio_file_size_limit"`
	BatchCountLimit              int `yaml:"batch_count_limit"`
	FileSizeLimit                int `yaml:"file_size_limit"`
	FileUploadLimit              int `yaml:"file_upload_limit"`
	ImageFileBatchLimit          int `yaml:"image_file_batch_limit"`
	ImageFileSizeLimit           int `yaml:"image_file_size_limit"`
	SingleChunkAttachmentLimit   int `yaml:"single_chunk_attachment_limit"`
	VideoFileSizeLimit           int `yaml:"video_file_size_limit"`
	WorkflowFileUploadLimit      int `yaml:"workflow_file_upload_limit"`
}

// FileUploadConfig mirrors ast_nodes.py FileUploadConfig (ast_nodes.py:689-696).
// FileLimits carries the Python field fileUploadConfig (ast_nodes.py:696) — a
// Dify wire name that is deliberately camelCase; the Go name is FileLimits.
type FileUploadConfig struct {
	Enabled                  bool               `yaml:"enabled"`
	AllowedFileExtensions    []string           `yaml:"allowed_file_extensions"`
	AllowedFileTypes         []string           `yaml:"allowed_file_types"`
	AllowedFileUploadMethods []string           `yaml:"allowed_file_upload_methods"`
	NumberLimits             int                `yaml:"number_limits"`
	Image                    *ImageUploadConfig `yaml:"image"`
	FileLimits               *FileSizeLimits    `yaml:"fileUploadConfig"`
}

// TextToSpeechConfig mirrors ast_nodes.py TextToSpeechConfig (ast_nodes.py:700-703).
type TextToSpeechConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Language string `yaml:"language"`
	Voice    string `yaml:"voice"`
}

// WorkflowFeatures mirrors ast_nodes.py WorkflowFeatures (ast_nodes.py:707-714).
type WorkflowFeatures struct {
	FileUpload                    *FileUploadConfig   `yaml:"file_upload"`
	OpeningStatement              string              `yaml:"opening_statement"`
	RetrieverResource             FeatureToggle       `yaml:"retriever_resource"`
	SensitiveWordAvoidance        FeatureToggle       `yaml:"sensitive_word_avoidance"`
	SpeechToText                  FeatureToggle       `yaml:"speech_to_text"`
	SuggestedQuestions            []string            `yaml:"suggested_questions"`
	SuggestedQuestionsAfterAnswer FeatureToggle       `yaml:"suggested_questions_after_answer"`
	TextToSpeech                  *TextToSpeechConfig `yaml:"text_to_speech"`
}

// ── Workflow variables (ast_nodes.py:94-115) ───────────────────────

// ConversationVariable mirrors ast_nodes.py ConversationVariable
// (ast_nodes.py:94-102). Value defaults to "" (Python Any default), not nil.
type ConversationVariable struct {
	ID          string         `yaml:"id"`
	Name        string         `yaml:"name"`
	ValueType   string         `yaml:"value_type"`
	Value       any            `yaml:"value"`
	Description string         `yaml:"description"`
	Selector    []string       `yaml:"selector"`
	Extra       map[string]any `yaml:"extra"`
}

// EnvironmentVariable mirrors ast_nodes.py EnvironmentVariable
// (ast_nodes.py:107-114).
type EnvironmentVariable struct {
	ID          string         `yaml:"id"`
	Name        string         `yaml:"name"`
	ValueType   string         `yaml:"value_type"`
	Value       string         `yaml:"value"`
	Description string         `yaml:"description"`
	Extra       map[string]any `yaml:"extra"`
}

// ── Constructors (Python default-value parity) ─────────────────────

// NewAppNode builds an AppNode with Python defaults (ast_nodes.py:33-40).
func NewAppNode() AppNode { return AppNode{Mode: "workflow"} }

// NewEnvelopeNode builds an EnvelopeNode with Python defaults
// (ast_nodes.py:44-47): kind "app" and a default AppNode.
func NewEnvelopeNode() EnvelopeNode { return EnvelopeNode{Kind: "app", App: NewAppNode()} }

// NewViewportNode builds a ViewportNode with Python defaults (ast_nodes.py:26-29).
func NewViewportNode() ViewportNode { return ViewportNode{Zoom: 1.0} }

// NewGraphNode builds an empty GraphNode (ast_nodes.py:656-658).
func NewGraphNode() GraphNode { return GraphNode{Nodes: []NodeDecl{}, Edges: []EdgeDecl{}} }

// NewWorkflowAST builds an empty workflow program (ast_nodes.py:722-729).
func NewWorkflowAST() *WorkflowAST {
	return &WorkflowAST{Envelope: NewEnvelopeNode(), Graph: NewGraphNode(), Features: WorkflowFeatures{}}
}

// NewNodeDecl builds a NodeDecl with Python defaults (ast_nodes.py:621-637):
// right/left handle positions and a base DataNode payload.
func NewNodeDecl() NodeDecl {
	return NodeDecl{Data: &DataNode{}, SourcePosition: "right", TargetPosition: "left"}
}

// NewEdgeDecl builds an EdgeDecl with Python defaults (ast_nodes.py:641-652):
// custom edge type with a "source" source handle.
func NewEdgeDecl() EdgeDecl { return EdgeDecl{Type: "custom", SourceHandle: "source"} }

// NewConversationVariable builds a ConversationVariable with Python defaults
// (ast_nodes.py:94-102).
func NewConversationVariable() ConversationVariable {
	return ConversationVariable{ValueType: "string", Value: ""}
}

// NewEnvironmentVariable builds an EnvironmentVariable with Python defaults
// (ast_nodes.py:107-114).
func NewEnvironmentVariable() EnvironmentVariable { return EnvironmentVariable{ValueType: "string"} }

// NewImageUploadConfig builds an ImageUploadConfig with Python defaults
// (ast_nodes.py:668-671).
func NewImageUploadConfig() ImageUploadConfig {
	return ImageUploadConfig{NumberLimits: 3, TransferMethods: []string{"local_file", "remote_url"}}
}

// NewFileSizeLimits builds a FileSizeLimits with the Python numeric defaults
// (ast_nodes.py:676-685).
func NewFileSizeLimits() FileSizeLimits {
	return FileSizeLimits{
		AttachmentImageFileSizeLimit: 2,
		AudioFileSizeLimit:           50,
		BatchCountLimit:              5,
		FileSizeLimit:                15,
		FileUploadLimit:              20,
		ImageFileBatchLimit:          10,
		ImageFileSizeLimit:           10,
		SingleChunkAttachmentLimit:   10,
		VideoFileSizeLimit:           100,
		WorkflowFileUploadLimit:      10,
	}
}

// NewFileUploadConfig builds a FileUploadConfig with Python defaults
// (ast_nodes.py:689-696).
func NewFileUploadConfig() FileUploadConfig { return FileUploadConfig{NumberLimits: 3} }
