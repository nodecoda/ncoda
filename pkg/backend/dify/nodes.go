package dify

// Per-type data nodes and shared component models — the Go port of the
// ast_nodes.py DataNode base (:399-411), the shared config nodes
// (:121-153), the strongly-typed sub-structures (:159-392) and the 25
// DataNode subclasses (:418-758). NODE_TYPE_MAP parity lives at the bottom
// (ast_nodes.py:764-787).

import "github.com/nodecoda/ncoda/pkg/types"

// ── NodeData union ──────────────────────────────────────────────────

// NodeData is the static type of NodeDecl.Data. Every concrete data node
// (DataNode and the 25 subclasses, via embedded DataNode) implements it;
// serializers and lowering type-switch on the concrete pointer. Go struct
// embedding promotes DataNode's fields, so each subclass's fields are flat —
// matching the Dify wire shape (data.title/data.model/...).
type NodeData interface{ nodeData() }

// ── DataNode base (ast_nodes.py:399-411) ───────────────────────────

// DataNode is the base embedded in every concrete data node. Python fields:
// title, desc (str|None), selected, iteration_id, loop_id, is_in_iteration,
// is_in_loop, raw_used, raw, error_strategy (str|None), default_value
// (list[dict]|None). 11 fields (the plan's "12" is approximate).
type DataNode struct {
	Title         string            `yaml:"title"`
	Desc          *string           `yaml:"desc"`
	Selected      bool              `yaml:"selected"`
	IterationID   string            `yaml:"iteration_id"`
	LoopID        string            `yaml:"loop_id"`
	IsInIteration bool              `yaml:"is_in_iteration"`
	IsInLoop      bool              `yaml:"is_in_loop"`
	RawUsed       map[string]any    `yaml:"raw_used"`
	Raw           map[string]any    `yaml:"raw"`
	ErrorStrategy *string           `yaml:"error_strategy"`
	DefaultValue  *[]map[string]any `yaml:"default_value"`
}

func (*DataNode) nodeData() {}

// baseData gives renderer helpers uniform access to the container ownership
// fields promoted into every concrete data node through embedding.
func (d *DataNode) baseData() *DataNode { return d }

// ── Variable references (ast_nodes.py:54-67) ───────────────────────

// VariableSelectorNode is a reference like ["nodeId", "variable_name"]
// (ast_nodes.py:54-67). Python runs the __post_init__ derivation below at
// construction; Go fields are mutable, so call Derive() after editing Path /
// NodeID / Key (the constructors call it for you).
type VariableSelectorNode struct {
	NodeID    string   `yaml:"node_id"`
	Key       string   `yaml:"key"`
	Path      []string `yaml:"path"`
	Variable  string   `yaml:"variable"`   // optional: variable name in {value_selector, variable} format
	ValueType string   `yaml:"value_type"` // optional: type hint in {value_selector, value_type, variable} format
}

// Derive ports VariableSelectorNode.__post_init__ (ast_nodes.py:60-64):
// path≥2 → node_id,key = path[:2]; else node_id&&key set → path=[node_id,key].
func (v *VariableSelectorNode) Derive() {
	if len(v.Path) >= 2 {
		v.NodeID, v.Key = v.Path[0], v.Path[1]
	} else if v.NodeID != "" && v.Key != "" {
		v.Path = []string{v.NodeID, v.Key}
	}
}

// NewVariableSelectorNode builds a selector from node_id+key, backfilling path.
func NewVariableSelectorNode(nodeID, key string) VariableSelectorNode {
	return VariableSelectorNode{NodeID: nodeID, Key: key, Path: []string{nodeID, key}}
}

// VariableSelectorFromList is the Go form of VariableSelectorNode.from_list
// (ast_nodes.py:66-67): path drives node_id/key derivation.
func VariableSelectorFromList(path []string) VariableSelectorNode {
	v := VariableSelectorNode{Path: path}
	v.Derive()
	return v
}

// ── Shared config nodes (ast_nodes.py:121-153) ─────────────────────

// ModelConfigNode mirrors ast_nodes.py ModelConfigNode (ast_nodes.py:122-126).
type ModelConfigNode struct {
	Provider         string           `yaml:"provider"`
	Name             string           `yaml:"name"`
	Mode             string           `yaml:"mode"`
	CompletionParams CompletionParams `yaml:"completion_params"`
}

// CompletionParams mirrors ast_nodes.py CompletionParams (ast_nodes.py:79-90).
type CompletionParams struct {
	Temperature      float64        `yaml:"temperature"`
	MaxTokens        *int           `yaml:"max_tokens"`
	TopP             float64        `yaml:"top_p"`
	TopK             *int           `yaml:"top_k"`
	FrequencyPenalty float64        `yaml:"frequency_penalty"`
	PresencePenalty  float64        `yaml:"presence_penalty"`
	ResponseFormat   string         `yaml:"response_format"`
	Stop             []string       `yaml:"stop"`
	Extra            map[string]any `yaml:"extra"`
}

// ConditionNode mirrors ast_nodes.py ConditionNode (ast_nodes.py:130-132).
type ConditionNode struct {
	VariableSelector   VariableSelectorNode `yaml:"variable_selector"`
	ComparisonOperator string               `yaml:"comparison_operator"`
	Value              string               `yaml:"value"`
}

// CaseNode mirrors ast_nodes.py CaseNode (ast_nodes.py:137-140).
type CaseNode struct {
	CaseID          string          `yaml:"case_id"`
	LogicalOperator string          `yaml:"logical_operator"`
	Conditions      []ConditionNode `yaml:"conditions"`
}

// PromptMessageNode mirrors ast_nodes.py PromptMessageNode (ast_nodes.py:144-147).
type PromptMessageNode struct {
	ID   string `yaml:"id"`
	Role string `yaml:"role"`
	Text string `yaml:"text"`
}

// OutputDefNode mirrors ast_nodes.py OutputDefNode (ast_nodes.py:151-153).
type OutputDefNode struct {
	Type     string                    `yaml:"type"`
	Children map[string]*OutputDefNode `yaml:"children"`
	// ChildOrder preserves the Python dict insertion order for Children
	// (record field order). Empty falls back to sorted keys in serialize.go
	// for determinism. Never serialized.
	ChildOrder []string `yaml:"-"`
}

// ── Strongly-typed sub-structures (ast_nodes.py:159-392) ───────────

// VariableDef mirrors ast_nodes.py VariableDef (ast_nodes.py:160-173).
type VariableDef struct {
	Variable                 string   `yaml:"variable"`
	Type                     string   `yaml:"type"`
	Label                    string   `yaml:"label"`
	Required                 bool     `yaml:"required"`
	Options                  []string `yaml:"options"` // list[str] (ast_nodes.py:165) — never None; nil == default empty
	MaxLength                *int     `yaml:"max_length"`
	Default                  any      `yaml:"default"`
	Hint                     string   `yaml:"hint"`
	Placeholder              string   `yaml:"placeholder"`
	AllowedFileTypes         []string `yaml:"allowed_file_types"`
	AllowedFileExtensions    []string `yaml:"allowed_file_extensions"`
	AllowedFileUploadMethods []string `yaml:"allowed_file_upload_methods"`
	Hide                     bool     `yaml:"hide"`
}

// OutputMapping mirrors ast_nodes.py OutputMapping (ast_nodes.py:177-179).
type OutputMapping struct {
	Variable      string   `yaml:"variable"`
	ValueType     string   `yaml:"value_type"`
	ValueSelector []string `yaml:"value_selector"`
}

// MemoryWindowConfig mirrors ast_nodes.py MemoryWindowConfig (ast_nodes.py:184-186).
type MemoryWindowConfig struct {
	Enabled bool `yaml:"enabled"`
	Size    *int `yaml:"size"`
}

// MemoryConfig mirrors ast_nodes.py MemoryConfig (ast_nodes.py:190-194).
// Window is MemoryWindowConfig | int | None — faithfully ported as any.
type MemoryConfig struct {
	Enabled             bool              `yaml:"enabled"`
	QueryPromptTemplate string            `yaml:"query_prompt_template"`
	Window              any               `yaml:"window"`
	RolePrefix          map[string]string `yaml:"role_prefix"`
}

// ContextConfig mirrors ast_nodes.py ContextConfig (ast_nodes.py:198-200).
type ContextConfig struct {
	Enabled          bool     `yaml:"enabled"`
	VariableSelector []string `yaml:"variable_selector"`
}

// VisionConfig mirrors ast_nodes.py VisionConfig (ast_nodes.py:204-206).
type VisionConfig struct {
	Enabled bool        `yaml:"enabled"`
	Configs *OrderedMap `yaml:"configs"`
}

// StructuredOutputConfig mirrors ast_nodes.py StructuredOutputConfig
// (ast_nodes.py:210-212).
type StructuredOutputConfig struct {
	Enabled   bool           `yaml:"enabled"`
	SchemaDef map[string]any `yaml:"schema_def"`
}

// HTTPBody mirrors ast_nodes.py HTTPBody (ast_nodes.py:217-218).
type HTTPBody struct {
	Type string `yaml:"type"`
	Data string `yaml:"data"`
}

// TimeoutConfig mirrors ast_nodes.py TimeoutConfig (ast_nodes.py:222-224).
type TimeoutConfig struct {
	MaxConnectTimeout int `yaml:"max_connect_timeout"`
	MaxReadTimeout    int `yaml:"max_read_timeout"`
}

// AuthConfigDetail mirrors ast_nodes.py AuthConfigDetail (ast_nodes.py:229-231).
type AuthConfigDetail struct {
	Type   string `yaml:"type"`
	APIKey string `yaml:"api_key"`
	Header string `yaml:"header"`
}

// AuthConfig mirrors ast_nodes.py AuthConfig (ast_nodes.py:235-237).
type AuthConfig struct {
	Type   string            `yaml:"type"`
	Config *AuthConfigDetail `yaml:"config"`
}

// RetryConfig mirrors ast_nodes.py RetryConfig (ast_nodes.py:241-244).
type RetryConfig struct {
	MaxRetries    int  `yaml:"max_retries"`
	RetryInterval int  `yaml:"retry_interval"`
	RetryEnabled  bool `yaml:"retry_enabled"`
}

// AssignmentItem mirrors ast_nodes.py AssignmentItem (ast_nodes.py:248-252).
type AssignmentItem struct {
	VariableSelector VariableSelectorNode `yaml:"variable_selector"`
	Operation        string               `yaml:"operation"`
	Value            any                  `yaml:"value"`
	InputType        string               `yaml:"input_type"`
}

// ParameterDef mirrors ast_nodes.py ParameterDef (ast_nodes.py:257-261).
type ParameterDef struct {
	Name        string    `yaml:"name"`
	Type        string    `yaml:"type"`
	Description string    `yaml:"description"`
	Required    bool      `yaml:"required"`
	Options     *[]string `yaml:"options"` // list[str] | None
}

// ClassDef mirrors ast_nodes.py ClassDef (ast_nodes.py:265-268).
type ClassDef struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Label       string `yaml:"label"`
	Description string `yaml:"description"`
}

// LoopVariable mirrors ast_nodes.py LoopVariable (ast_nodes.py:274-278).
type LoopVariable struct {
	Variable  string `yaml:"variable"`
	ValueType string `yaml:"value_type"`
	Value     any    `yaml:"value"`
	VarType   string `yaml:"var_type"`
	ID        string `yaml:"id"`
}

// LoopMetadata mirrors ast_nodes.py LoopMetadata (ast_nodes.py:282-284):
// metadata attached to loop/loop-start/loop-end nodes for cross-linking.
type LoopMetadata struct {
	StartNodeID string `yaml:"start_node_id"`
	LoopID      string `yaml:"loop_id"`
	IsInLoop    bool   `yaml:"is_in_loop"`
}

// ListFilterItem mirrors ast_nodes.py ListFilterItem (ast_nodes.py:292-294).
type ListFilterItem struct {
	Key                string `yaml:"key"`
	ComparisonOperator string `yaml:"comparison_operator"`
	Value              any    `yaml:"value"`
}

// ListFilterConfig mirrors ast_nodes.py ListFilterConfig (ast_nodes.py:298-300).
type ListFilterConfig struct {
	Enabled    bool             `yaml:"enabled"`
	Conditions []ListFilterItem `yaml:"conditions"`
}

// ListExtractConfig mirrors ast_nodes.py ListExtractConfig (ast_nodes.py:305-306).
type ListExtractConfig struct {
	Enabled bool   `yaml:"enabled"`
	Serial  string `yaml:"serial"`
}

// ListOrderConfig mirrors ast_nodes.py ListOrderConfig (ast_nodes.py:311-313).
type ListOrderConfig struct {
	Enabled bool   `yaml:"enabled"`
	Key     string `yaml:"key"`
	Value   string `yaml:"value"`
}

// ListLimitConfig mirrors ast_nodes.py ListLimitConfig (ast_nodes.py:318-319).
type ListLimitConfig struct {
	Enabled bool `yaml:"enabled"`
	Size    int  `yaml:"size"`
}

// StringSourceNode mirrors ast_nodes.py StringSourceNode (ast_nodes.py:324-326).
type StringSourceNode struct {
	Type     string   `yaml:"type"`
	Selector []string `yaml:"selector"`
	Value    string   `yaml:"value"`
}

// StringListSourceNode mirrors ast_nodes.py StringListSourceNode (ast_nodes.py:331-333).
type StringListSourceNode struct {
	Type     string   `yaml:"type"`
	Selector []string `yaml:"selector"`
	Value    []string `yaml:"value"`
}

// ── HumanInputField union (ast_nodes.py:337-367) ───────────────────

// HumanInputField is the union of the four human-input field shapes:
// ParagraphHumanInput | SelectHumanInput | FileHumanInput | FileListHumanInput
// (ast_nodes.py:369). Serialization type-switches on the concrete type.
type HumanInputField interface{ humanInputField() }

// ParagraphHumanInput mirrors ast_nodes.py ParagraphHumanInput
// (ast_nodes.py:338-340): output_variable_name is required in Python.
type ParagraphHumanInput struct {
	OutputVariableName string            `yaml:"output_variable_name"`
	Default            *StringSourceNode `yaml:"default"`
	Type               string            `yaml:"type"`
}

func (ParagraphHumanInput) humanInputField() {}

// SelectHumanInput mirrors ast_nodes.py SelectHumanInput
// (ast_nodes.py:345-347): output_variable_name and option_source are required.
type SelectHumanInput struct {
	OutputVariableName string               `yaml:"output_variable_name"`
	OptionSource       StringListSourceNode `yaml:"option_source"`
	Type               string               `yaml:"type"`
}

func (SelectHumanInput) humanInputField() {}

// FileHumanInput mirrors ast_nodes.py FileHumanInput (ast_nodes.py:352-356).
type FileHumanInput struct {
	OutputVariableName       string   `yaml:"output_variable_name"`
	AllowedFileTypes         []string `yaml:"allowed_file_types"`
	AllowedFileExtensions    []string `yaml:"allowed_file_extensions"`
	AllowedFileUploadMethods []string `yaml:"allowed_file_upload_methods"`
	Type                     string   `yaml:"type"`
}

func (FileHumanInput) humanInputField() {}

// FileListHumanInput mirrors ast_nodes.py FileListHumanInput
// (ast_nodes.py:361-366).
type FileListHumanInput struct {
	OutputVariableName       string   `yaml:"output_variable_name"`
	AllowedFileTypes         []string `yaml:"allowed_file_types"`
	AllowedFileExtensions    []string `yaml:"allowed_file_extensions"`
	AllowedFileUploadMethods []string `yaml:"allowed_file_upload_methods"`
	NumberLimits             int      `yaml:"number_limits"`
	Type                     string   `yaml:"type"`
}

func (FileListHumanInput) humanInputField() {}

// DatasourceInputNode mirrors ast_nodes.py DatasourceInputNode
// (ast_nodes.py:373-374).
type DatasourceInputNode struct {
	Value any     `yaml:"value"`
	Type  *string `yaml:"type"`
}

// AgentInputNode mirrors ast_nodes.py AgentInputNode (ast_nodes.py:379-380):
// value and type are required in Python.
type AgentInputNode struct {
	Value any    `yaml:"value"`
	Type  string `yaml:"type"`
}

// UserAction mirrors ast_nodes.py UserAction (ast_nodes.py:385-392).
type UserAction struct {
	ID          string `yaml:"id"`
	Label       string `yaml:"label"`
	Title       string `yaml:"title"`
	Value       string `yaml:"value"`
	ButtonStyle string `yaml:"button_style"` // Dify field name
	Disabled    bool   `yaml:"disabled"`
	ShowInput   bool   `yaml:"show_input"`
}

// ── The 25 data node subclasses (ast_nodes.py:418-758) ─────────────

// StartDataNode mirrors ast_nodes.py StartDataNode (ast_nodes.py:419).
type StartDataNode struct {
	DataNode
	Variables []VariableDef `yaml:"variables"`
}

// OutputNode mirrors ast_nodes.py OutputNode (ast_nodes.py:424).
type OutputNode struct {
	DataNode
	Outputs []OutputMapping `yaml:"outputs"`
}

// AnswerNode mirrors ast_nodes.py AnswerNode (ast_nodes.py:429-431).
// Variables has a Python default_factory of list, so factory construction
// (makeAnswerNode) must supply a non-nil empty slice; a nil Variables would
// serialize as null instead of [].
type AnswerNode struct {
	DataNode
	Answer    string                 `yaml:"answer"`
	Variables []VariableSelectorNode `yaml:"variables"`
}

// LLMDataNode mirrors ast_nodes.py LLMDataNode (ast_nodes.py:435-443).
type LLMDataNode struct {
	DataNode
	Model            *ModelConfigNode        `yaml:"model"`
	PromptTemplate   []PromptMessageNode     `yaml:"prompt_template"`
	Memory           *MemoryConfig           `yaml:"memory"`
	Context          *ContextConfig          `yaml:"context"`
	Vision           *VisionConfig           `yaml:"vision"`
	Variables        []VariableSelectorNode  `yaml:"variables"`
	StructuredOutput *StructuredOutputConfig `yaml:"structured_output"`
	ReasoningFormat  *string                 `yaml:"reasoning_format"`
	RetryConfig      *RetryConfig            `yaml:"retry_config"`
}

// IfElseDataNode mirrors ast_nodes.py IfElseDataNode (ast_nodes.py:448).
type IfElseDataNode struct {
	DataNode
	Cases []CaseNode `yaml:"cases"`
}

// CodeDataNode mirrors ast_nodes.py CodeDataNode (ast_nodes.py:454-459).
type CodeDataNode struct {
	DataNode
	Code         string                   `yaml:"code"`
	CodeLanguage string                   `yaml:"code_language"`
	Outputs      map[string]OutputDefNode `yaml:"outputs"`
	// OutputOrder preserves the Python dict insertion order for Outputs
	// (Python builds outputs dicts in declaration/literal order and dicts
	// keep it; Go maps do not). Empty falls back to sortedKeys in
	// serialize.go for determinism.
	OutputOrder            []string                     `yaml:"-"`
	Variables              []VariableSelectorNode       `yaml:"variables"`
	Dependencies           []string                     `yaml:"dependencies"`
	DeclaredOutputContract map[string]*types.Descriptor `yaml:"declared_output_contract"` // dict[str, TypeDescriptor] | None
}

// HTTPRequestDataNode mirrors ast_nodes.py HTTPRequestDataNode
// (ast_nodes.py:464-472). Params is dict[str, Any] | str — ported as any.
type HTTPRequestDataNode struct {
	DataNode
	Method        string                 `yaml:"method"`
	URL           string                 `yaml:"url"`
	Headers       string                 `yaml:"headers"`
	Params        any                    `yaml:"params"`
	Body          *HTTPBody              `yaml:"body"`
	Timeout       *TimeoutConfig         `yaml:"timeout"`
	Authorization *AuthConfig            `yaml:"authorization"`
	RetryConfig   *RetryConfig           `yaml:"retry_config"`
	Variables     []VariableSelectorNode `yaml:"variables"`
}

// ToolDataNode mirrors ast_nodes.py ToolDataNode (ast_nodes.py:476-490).
type ToolDataNode struct {
	DataNode
	ToolName       string         `yaml:"tool_name"`
	ToolLabel      string         `yaml:"tool_label"`
	ProviderID     string         `yaml:"provider_id"`
	ProviderType   string         `yaml:"provider_type"`
	ToolParameters map[string]any `yaml:"tool_parameters"`
	// ToolParameterOrder preserves the Python dict insertion order for
	// ToolParameters (DSL map-literal order). Empty falls back to
	// sortedKeys in serialize.go for determinism.
	ToolParameterOrder []string         `yaml:"-"`
	ToolConfiguration  map[string]any   `yaml:"tool_configuration"`
	ProviderName       *string          `yaml:"provider_name"`
	ToolConfigurations map[string]any   `yaml:"tool_configurations"`
	ToolDescription    *string          `yaml:"tool_description"`
	ToolNodeVersion    *string          `yaml:"tool_node_version"`
	OutputSchema       map[string]any   `yaml:"output_schema"`
	ParamSchemas       []map[string]any `yaml:"param_schemas"`
	Params             map[string]any   `yaml:"params"`
	// Variables is legacy-oracle metadata carried for structural parity
	// with the retired Python lowerer (collectVarRefs). serializeTool does
	// NOT emit it (I5): the dify tool node artifact contract carries
	// dataflow exclusively through the tool_parameters {{#...#}}
	// interpolations — the frozen oracle 06_tool_use.yml has no variables
	// key on the tool node, and dify-compile accepts the omission. The IR
	// path passes an empty list by design.
	Variables   []VariableSelectorNode `yaml:"variables"`
	RetryConfig *RetryConfig           `yaml:"retry_config"`
}

// TemplateTransformDataNode mirrors ast_nodes.py TemplateTransformDataNode
// (ast_nodes.py:495-496).
type TemplateTransformDataNode struct {
	DataNode
	Template  string                 `yaml:"template"`
	Variables []VariableSelectorNode `yaml:"variables"`
}

// KnowledgeRetrievalDataNode mirrors ast_nodes.py KnowledgeRetrievalDataNode
// (ast_nodes.py:501-508).
type KnowledgeRetrievalDataNode struct {
	DataNode
	DatasetIDs              []string               `yaml:"dataset_ids"`
	QueryVariableSelector   *VariableSelectorNode  `yaml:"query_variable_selector"`
	RetrievalMode           *string                `yaml:"retrieval_mode"`
	Query                   *string                `yaml:"query"`
	MultipleRetrievalConfig map[string]any         `yaml:"multiple_retrieval_config"`
	SingleRetrievalConfig   map[string]any         `yaml:"single_retrieval_config"`
	Variables               []VariableSelectorNode `yaml:"variables"`
	RetrievalModel          map[string]any         `yaml:"retrieval_model"`
}

// QuestionClassifierDataNode mirrors ast_nodes.py QuestionClassifierDataNode
// (ast_nodes.py:513-518).
type QuestionClassifierDataNode struct {
	DataNode
	QueryVariableSelector *VariableSelectorNode `yaml:"query_variable_selector"`
	Model                 *ModelConfigNode      `yaml:"model"`
	Classes               []ClassDef            `yaml:"classes"`
	Instruction           *string               `yaml:"instruction"`
	Memory                *MemoryConfig         `yaml:"memory"`
	Vision                *VisionConfig         `yaml:"vision"`
}

// ParameterExtractorDataNode mirrors ast_nodes.py ParameterExtractorDataNode
// (ast_nodes.py:523-530).
type ParameterExtractorDataNode struct {
	DataNode
	Model         *ModelConfigNode       `yaml:"model"`
	Parameters    []ParameterDef         `yaml:"parameters"`
	Query         []string               `yaml:"query"`
	Instruction   string                 `yaml:"instruction"`
	Memory        *MemoryConfig          `yaml:"memory"`
	ReasoningMode *string                `yaml:"reasoning_mode"`
	Vision        *VisionConfig          `yaml:"vision"`
	Variables     []VariableSelectorNode `yaml:"variables"`
}

// LoopDataNode mirrors ast_nodes.py LoopDataNode (ast_nodes.py:535-541).
type LoopDataNode struct {
	DataNode
	LoopVariables   []LoopVariable  `yaml:"loop_variables"`
	BreakConditions []ConditionNode `yaml:"break_conditions"`
	LoopCount       int             `yaml:"loop_count"`
	ErrorHandleMode string          `yaml:"error_handle_mode"`
	LogicalOperator string          `yaml:"logical_operator"`
	Metadata        LoopMetadata    `yaml:"metadata"`
}

// LoopStartDataNode mirrors ast_nodes.py LoopStartDataNode (ast_nodes.py:545).
type LoopStartDataNode struct {
	DataNode
	Metadata LoopMetadata `yaml:"metadata"`
}

// LoopEndDataNode mirrors ast_nodes.py LoopEndDataNode (ast_nodes.py:550).
type LoopEndDataNode struct {
	DataNode
	Metadata LoopMetadata `yaml:"metadata"`
}

// VariableAggregatorDataNode mirrors ast_nodes.py VariableAggregatorDataNode
// (ast_nodes.py:555-557).
type VariableAggregatorDataNode struct {
	DataNode
	Variables        []VariableSelectorNode `yaml:"variables"`
	OutputType       *string                `yaml:"output_type"`
	AdvancedSettings map[string]any         `yaml:"advanced_settings"`
}

// AssignerDataNode mirrors ast_nodes.py AssignerDataNode (ast_nodes.py:562).
type AssignerDataNode struct {
	DataNode
	Items []AssignmentItem `yaml:"items"`
}

// DocumentExtractorDataNode mirrors ast_nodes.py DocumentExtractorDataNode
// (ast_nodes.py:567-568).
type DocumentExtractorDataNode struct {
	DataNode
	VariableSelector *VariableSelectorNode `yaml:"variable_selector"`
	IsArrayFile      bool                  `yaml:"is_array_file"`
}

// ListOperatorDataNode mirrors ast_nodes.py ListOperatorDataNode
// (ast_nodes.py:573-579).
type ListOperatorDataNode struct {
	DataNode
	Variable    []string          `yaml:"variable"`
	FilterBy    ListFilterConfig  `yaml:"filter_by"`
	ExtractBy   ListExtractConfig `yaml:"extract_by"`
	OrderBy     ListOrderConfig   `yaml:"order_by"`
	Limit       ListLimitConfig   `yaml:"limit"`
	VarType     string            `yaml:"var_type"`
	ItemVarType string            `yaml:"item_var_type"`
}

// AgentDataNode mirrors ast_nodes.py AgentDataNode (ast_nodes.py:584-592).
type AgentDataNode struct {
	DataNode
	AgentStrategyName         string                    `yaml:"agent_strategy_name"`
	AgentStrategyProviderName string                    `yaml:"agent_strategy_provider_name"`
	AgentStrategyLabel        string                    `yaml:"agent_strategy_label"`
	AgentParameters           map[string]AgentInputNode `yaml:"agent_parameters"`
	Model                     *ModelConfigNode          `yaml:"model"`
	Memory                    *MemoryConfig             `yaml:"memory"`
	OutputSchema              map[string]any            `yaml:"output_schema"`
	PluginUniqueIdentifier    string                    `yaml:"plugin_unique_identifier"`
	ToolNodeVersion           *string                   `yaml:"tool_node_version"`
}

// HumanInputDataNode mirrors ast_nodes.py HumanInputDataNode
// (ast_nodes.py:597-603).
type HumanInputDataNode struct {
	DataNode
	FormContent string            `yaml:"form_content"`
	Inputs      []HumanInputField `yaml:"inputs"`
	UserActions []UserAction      `yaml:"user_actions"`
	Timeout     int               `yaml:"timeout"`
	TimeoutUnit string            `yaml:"timeout_unit"`
	// DeliveryMethods mirrors ast_nodes.py delivery_methods (list[dict]):
	// one map[string]any per method with id/type/enabled keys (the webapp
	// delivery method the human-input lowerer emits). Go map[string]any
	// mirrors the Python dict; serialization preserves the Python key
	// insertion order (id, type, enabled).
	DeliveryMethods []any `yaml:"delivery_methods"`
}

// DatasourceDataNode mirrors ast_nodes.py DatasourceDataNode
// (ast_nodes.py:608-614).
type DatasourceDataNode struct {
	DataNode
	PluginID                 string                         `yaml:"plugin_id"`
	ProviderName             string                         `yaml:"provider_name"`
	ProviderType             string                         `yaml:"provider_type"`
	DatasourceName           string                         `yaml:"datasource_name"`
	DatasourceConfigurations map[string]any                 `yaml:"datasource_configurations"`
	PluginUniqueIdentifier   *string                        `yaml:"plugin_unique_identifier"`
	DatasourceParameters     map[string]DatasourceInputNode `yaml:"datasource_parameters"`
}

// IterationDataNode mirrors ast_nodes.py IterationDataNode
// (ast_nodes.py:733-744).
type IterationDataNode struct {
	DataNode
	IteratorSelector  []string `yaml:"iterator_selector"`
	OutputSelector    []string `yaml:"output_selector"`
	OutputType        string   `yaml:"output_type"`
	IteratorInputType string   `yaml:"iterator_input_type"`
	StartNodeID       string   `yaml:"start_node_id"`
	IsParallel        bool     `yaml:"is_parallel"`
	ParallelNums      int      `yaml:"parallel_nums"`
	ErrorHandleMode   string   `yaml:"error_handle_mode"`
	FlattenOutput     *bool    `yaml:"flatten_output"`
	Height            int      `yaml:"height"`
	Width             int      `yaml:"width"`
}

// IterationStartDataNode mirrors ast_nodes.py IterationStartDataNode
// (ast_nodes.py:748): no extra fields.
type IterationStartDataNode struct {
	DataNode
}

// CustomNoteDataNode mirrors ast_nodes.py CustomNoteDataNode
// (ast_nodes.py:753-762): sticky note annotation — non-executable, purely for
// documentation.
type CustomNoteDataNode struct {
	DataNode
	Text   string `yaml:"text"`
	Author string `yaml:"author"`
	// ShowAuthor mirrors ast_nodes.py show_author (default True, ast_nodes.py:758).
	// A pointer distinguishes "unset" (nil → serialized as True) from an
	// explicit False — Python's bool default cannot be represented by Go's
	// zero bool. Custom-note is never emitted by lowering; only constructed
	// ASTs (tests / tooling) reach it.
	ShowAuthor *bool  `yaml:"show_author"`
	Theme      string `yaml:"theme"`
	NoteHeight int    `yaml:"note_height"`
	NoteWidth  int    `yaml:"note_width"`
}

// ── Constructors (Python default-value parity) ─────────────────────

// NewCompletionParams builds CompletionParams with Python defaults
// (ast_nodes.py:82-84). This constructor IS the serializer contract (Task 26
// R1/Important 2c): _serialize_completion_params omits values equal to the
// Python defaults (temperature/top_p 1.0) and emits anything else — including
// an explicit 0.0, which is a meaningful Python value. Go lowering always
// builds CompletionParams through this constructor, so serialization must NOT
// re-apply defaults to a zero-value CompletionParams{} (that struct shape only
// occurs via direct construction and is intentionally not serializer-adjusted).
func NewCompletionParams() CompletionParams {
	return CompletionParams{Temperature: 1.0, TopP: 1.0}
}

// NewModelConfigNode builds a ModelConfigNode whose completion_params carry the
// Python defaults (ast_nodes.py:122-126 + :82-84).
func NewModelConfigNode() ModelConfigNode {
	return ModelConfigNode{CompletionParams: NewCompletionParams()}
}

// NewCaseNode builds a CaseNode with Python defaults (ast_nodes.py:139).
func NewCaseNode() CaseNode { return CaseNode{LogicalOperator: "and"} }

// NewPromptMessageNode builds a PromptMessageNode with Python defaults
// (ast_nodes.py:146).
func NewPromptMessageNode() PromptMessageNode { return PromptMessageNode{Role: "user"} }

// NewOutputDefNode builds an OutputDefNode with Python defaults (ast_nodes.py:152).
func NewOutputDefNode() OutputDefNode { return OutputDefNode{Type: "string"} }

// NewVariableDef builds a VariableDef with Python defaults (ast_nodes.py:162).
func NewVariableDef() VariableDef { return VariableDef{Type: "string"} }

// NewMemoryConfig builds a MemoryConfig with Python defaults (ast_nodes.py:191).
func NewMemoryConfig() MemoryConfig { return MemoryConfig{Enabled: true} }

// NewHTTPBody builds an HTTPBody with Python defaults (ast_nodes.py:217).
func NewHTTPBody() HTTPBody { return HTTPBody{Type: "none"} }

// NewTimeoutConfig builds a TimeoutConfig with Python defaults (ast_nodes.py:223-224).
func NewTimeoutConfig() TimeoutConfig {
	return TimeoutConfig{MaxConnectTimeout: 10, MaxReadTimeout: 60}
}

// NewAuthConfigDetail builds an AuthConfigDetail with Python defaults
// (ast_nodes.py:229).
func NewAuthConfigDetail() AuthConfigDetail { return AuthConfigDetail{Type: "basic"} }

// NewAuthConfig builds an AuthConfig with Python defaults (ast_nodes.py:236).
func NewAuthConfig() AuthConfig { return AuthConfig{Type: "no-auth"} }

// NewRetryConfig builds a RetryConfig with Python defaults (ast_nodes.py:243).
func NewRetryConfig() RetryConfig { return RetryConfig{RetryInterval: 1000} }

// NewAssignmentItem builds an AssignmentItem with Python defaults
// (ast_nodes.py:250-252).
func NewAssignmentItem() AssignmentItem {
	return AssignmentItem{Operation: "set", InputType: "variable"}
}

// NewParameterDef builds a ParameterDef with Python defaults (ast_nodes.py:258-260).
func NewParameterDef() ParameterDef { return ParameterDef{Type: "string", Required: true} }

// NewLoopVariable builds a LoopVariable with Python defaults (ast_nodes.py:275).
func NewLoopVariable() LoopVariable { return LoopVariable{ValueType: "constant"} }

// NewListFilterItem builds a ListFilterItem with Python defaults
// (ast_nodes.py:293).
func NewListFilterItem() ListFilterItem {
	return ListFilterItem{ComparisonOperator: "contains"}
}

// NewListExtractConfig builds a ListExtractConfig with Python defaults
// (ast_nodes.py:306).
func NewListExtractConfig() ListExtractConfig { return ListExtractConfig{Serial: "1"} }

// NewListOrderConfig builds a ListOrderConfig with Python defaults
// (ast_nodes.py:313).
func NewListOrderConfig() ListOrderConfig { return ListOrderConfig{Value: "asc"} }

// NewListLimitConfig builds a ListLimitConfig with Python defaults
// (ast_nodes.py:319).
func NewListLimitConfig() ListLimitConfig { return ListLimitConfig{Size: -1} }

// NewStringSourceNode builds a StringSourceNode with Python defaults
// (ast_nodes.py:324).
func NewStringSourceNode() StringSourceNode { return StringSourceNode{Type: "constant"} }

// NewStringListSourceNode builds a StringListSourceNode with Python defaults
// (ast_nodes.py:331).
func NewStringListSourceNode() StringListSourceNode { return StringListSourceNode{Type: "constant"} }

// NewParagraphHumanInput builds a ParagraphHumanInput (ast_nodes.py:338-340).
func NewParagraphHumanInput(output string) ParagraphHumanInput {
	return ParagraphHumanInput{OutputVariableName: output, Type: "paragraph"}
}

// NewSelectHumanInput builds a SelectHumanInput (ast_nodes.py:345-347).
func NewSelectHumanInput(output string, optionSource StringListSourceNode) SelectHumanInput {
	return SelectHumanInput{OutputVariableName: output, OptionSource: optionSource, Type: "select"}
}

// NewFileHumanInput builds a FileHumanInput (ast_nodes.py:352-356).
func NewFileHumanInput(output string) FileHumanInput {
	return FileHumanInput{OutputVariableName: output, Type: "file"}
}

// NewFileListHumanInput builds a FileListHumanInput (ast_nodes.py:361-366).
func NewFileListHumanInput(output string) FileListHumanInput {
	return FileListHumanInput{OutputVariableName: output, Type: "file-list"}
}

// NewAgentInputNode builds an AgentInputNode from the Python-required value and
// type arguments (ast_nodes.py:379-380). "typ" mirrors the Python parameter
// name "type" (a Go keyword).
func NewAgentInputNode(value any, typ string) *AgentInputNode {
	return &AgentInputNode{Value: value, Type: typ}
}

// NewUserAction builds a UserAction with Python defaults (ast_nodes.py:390).
func NewUserAction() UserAction { return UserAction{ButtonStyle: "default"} }

// NewStartDataNode builds a zero-valued start node (ast_nodes.py:419).
func NewStartDataNode() *StartDataNode { return &StartDataNode{} }

// NewOutputNode builds a zero-valued output node (ast_nodes.py:424).
func NewOutputNode() *OutputNode { return &OutputNode{} }

// NewAnswerNode builds a zero-valued answer node (ast_nodes.py:429-430).
func NewAnswerNode() *AnswerNode { return &AnswerNode{} }

// NewLLMDataNode builds a zero-valued llm node (ast_nodes.py:435-443).
func NewLLMDataNode() *LLMDataNode { return &LLMDataNode{} }

// NewIfElseDataNode builds a zero-valued if-else node (ast_nodes.py:448).
func NewIfElseDataNode() *IfElseDataNode { return &IfElseDataNode{} }

// NewCodeDataNode builds a code node with Python defaults (ast_nodes.py:455).
func NewCodeDataNode() *CodeDataNode { return &CodeDataNode{CodeLanguage: "python3"} }

// NewHTTPRequestDataNode builds an http-request node with Python defaults
// (ast_nodes.py:464-467).
func NewHTTPRequestDataNode() *HTTPRequestDataNode {
	return &HTTPRequestDataNode{Method: "GET", Params: map[string]any{}}
}

// NewToolDataNode builds a zero-valued tool node (ast_nodes.py:476-490).
func NewToolDataNode() *ToolDataNode { return &ToolDataNode{} }

// NewTemplateTransformDataNode builds a zero-valued template-transform node
// (ast_nodes.py:495-496).
func NewTemplateTransformDataNode() *TemplateTransformDataNode { return &TemplateTransformDataNode{} }

// NewKnowledgeRetrievalDataNode builds a zero-valued knowledge-retrieval node
// (ast_nodes.py:501-508).
func NewKnowledgeRetrievalDataNode() *KnowledgeRetrievalDataNode {
	return &KnowledgeRetrievalDataNode{}
}

// NewQuestionClassifierDataNode builds a zero-valued question-classifier node
// (ast_nodes.py:513-518).
func NewQuestionClassifierDataNode() *QuestionClassifierDataNode {
	return &QuestionClassifierDataNode{}
}

// NewParameterExtractorDataNode builds a zero-valued parameter-extractor node
// (ast_nodes.py:523-530).
func NewParameterExtractorDataNode() *ParameterExtractorDataNode {
	return &ParameterExtractorDataNode{}
}

// NewLoopDataNode builds a loop node with Python defaults (ast_nodes.py:538-540).
func NewLoopDataNode() *LoopDataNode {
	return &LoopDataNode{LoopCount: 100, ErrorHandleMode: "terminated", LogicalOperator: "and"}
}

// NewLoopStartDataNode builds a zero-valued loop-start node (ast_nodes.py:545).
func NewLoopStartDataNode() *LoopStartDataNode { return &LoopStartDataNode{} }

// NewLoopEndDataNode builds a zero-valued loop-end node (ast_nodes.py:550).
func NewLoopEndDataNode() *LoopEndDataNode { return &LoopEndDataNode{} }

// NewVariableAggregatorDataNode builds a zero-valued variable-aggregator node
// (ast_nodes.py:555-557).
func NewVariableAggregatorDataNode() *VariableAggregatorDataNode {
	return &VariableAggregatorDataNode{}
}

// NewAssignerDataNode builds a zero-valued assigner node (ast_nodes.py:562).
func NewAssignerDataNode() *AssignerDataNode { return &AssignerDataNode{} }

// NewDocumentExtractorDataNode builds a zero-valued document-extractor node
// (ast_nodes.py:567-568).
func NewDocumentExtractorDataNode() *DocumentExtractorDataNode { return &DocumentExtractorDataNode{} }

// NewListOperatorDataNode builds a zero-valued list-operator node
// (ast_nodes.py:573-579).
func NewListOperatorDataNode() *ListOperatorDataNode { return &ListOperatorDataNode{} }

// NewAgentDataNode builds a zero-valued agent node (ast_nodes.py:584-592).
func NewAgentDataNode() *AgentDataNode { return &AgentDataNode{} }

// NewHumanInputDataNode builds a human-input node with Python defaults
// (ast_nodes.py:601-602).
func NewHumanInputDataNode() *HumanInputDataNode {
	return &HumanInputDataNode{Timeout: 36, TimeoutUnit: "hour"}
}

// NewDatasourceDataNode builds a datasource node with Python defaults
// (ast_nodes.py:611).
func NewDatasourceDataNode() *DatasourceDataNode {
	return &DatasourceDataNode{DatasourceName: "local_file"}
}

// NewIterationDataNode builds an iteration node with Python defaults
// (ast_nodes.py:740-741).
func NewIterationDataNode() *IterationDataNode {
	return &IterationDataNode{ParallelNums: 10, ErrorHandleMode: "terminated"}
}

// NewIterationStartDataNode builds a zero-valued iteration-start node
// (ast_nodes.py:748).
func NewIterationStartDataNode() *IterationStartDataNode { return &IterationStartDataNode{} }

// NewCustomNoteDataNode builds a custom-note node with Python defaults
// (ast_nodes.py:758).
func NewCustomNoteDataNode() *CustomNoteDataNode {
	return &CustomNoteDataNode{ShowAuthor: boolPtr(true)}
}

// boolPtr builds a *bool for fields whose nil means "unset" (the Python
// dataclass default then applies at serialization time).
func boolPtr(v bool) *bool { return &v }

// ── NodeTypeMap (ast_nodes.py:764-787) ─────────────────────────────

// NodeTypeMap mirrors the Python NODE_TYPE_MAP exactly: 25 entries mapping the
// Dify node-type string to a constructor producing that node's zero-ish value
// struct. Keys must stay in sync with Python — TestNodeTypeMapKeys locks it.
var NodeTypeMap = map[string]func() any{
	"start":               func() any { return NewStartDataNode() },
	"end":                 func() any { return NewOutputNode() },
	"answer":              func() any { return NewAnswerNode() },
	"llm":                 func() any { return NewLLMDataNode() },
	"if-else":             func() any { return NewIfElseDataNode() },
	"code":                func() any { return NewCodeDataNode() },
	"http-request":        func() any { return NewHTTPRequestDataNode() },
	"tool":                func() any { return NewToolDataNode() },
	"template-transform":  func() any { return NewTemplateTransformDataNode() },
	"knowledge-retrieval": func() any { return NewKnowledgeRetrievalDataNode() },
	"question-classifier": func() any { return NewQuestionClassifierDataNode() },
	"parameter-extractor": func() any { return NewParameterExtractorDataNode() },
	"loop":                func() any { return NewLoopDataNode() },
	"loop-start":          func() any { return NewLoopStartDataNode() },
	"loop-end":            func() any { return NewLoopEndDataNode() },
	"variable-aggregator": func() any { return NewVariableAggregatorDataNode() },
	"assigner":            func() any { return NewAssignerDataNode() },
	"document-extractor":  func() any { return NewDocumentExtractorDataNode() },
	"list-operator":       func() any { return NewListOperatorDataNode() },
	"agent":               func() any { return NewAgentDataNode() },
	"human-input":         func() any { return NewHumanInputDataNode() },
	"datasource":          func() any { return NewDatasourceDataNode() },
	"iteration":           func() any { return NewIterationDataNode() },
	"iteration-start":     func() any { return NewIterationStartDataNode() },
	"custom-note":         func() any { return NewCustomNoteDataNode() },
}

// ContainerFlags exposes the Python DataNode base container-ownership fields
// (ast_nodes.py:399-411): is_in_iteration / iteration_id / is_in_loop /
// loop_id. Every concrete data node embeds DataNode, so a type assertion to
// the flagsProvider interface in pkg/backend/dify/verifier resolves for all of them.
func (d *DataNode) ContainerFlags() (isInIteration bool, iterationID string, isInLoop bool, loopID string) {
	return d.IsInIteration, d.IterationID, d.IsInLoop, d.LoopID
}
