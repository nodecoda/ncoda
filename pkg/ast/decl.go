package ast

// This file ports the declaration nodes and the Program root from
// src/nclang/lang/ast.py (declarations & Program sections). Field-for-field
// parity with the Python dataclasses.

// FieldDecl is a field inside a type definition: <type> <name>;.
type FieldDecl struct {
	base
	name    string
	typeRef TypeRef
}

// NewFieldDecl constructs a FieldDecl.
func NewFieldDecl(name string, typeRef TypeRef) *FieldDecl {
	if typeRef == nil {
		typeRef = NewPrimTypeRef("string")
	}
	return &FieldDecl{base: newBase(), name: name, typeRef: typeRef}
}

// Name returns the field name.
func (n *FieldDecl) Name() string { return n.name }

// TypeRef returns the field type.
func (n *FieldDecl) TypeRef() TypeRef { return n.typeRef }

func (n *FieldDecl) String() string { return reprNode(n) }

// OutputDecl is an output declaration: output <type> <name>;.
type OutputDecl struct {
	base
	name    string
	typeRef TypeRef
}

// NewOutputDecl constructs an OutputDecl.
func NewOutputDecl(name string, typeRef TypeRef) *OutputDecl {
	if typeRef == nil {
		typeRef = NewPrimTypeRef("string")
	}
	return &OutputDecl{base: newBase(), name: name, typeRef: typeRef}
}

// Name returns the output name.
func (n *OutputDecl) Name() string { return n.name }

// TypeRef returns the output type.
func (n *OutputDecl) TypeRef() TypeRef { return n.typeRef }

func (n *OutputDecl) String() string { return reprNode(n) }

// ConstDecl is a compile-time constant: const <name> = <literal>;. The value
// defaults to a NullLit (ast.py ConstDecl).
type ConstDecl struct {
	base
	name  string
	value Expr
}

// NewConstDecl constructs a ConstDecl.
func NewConstDecl(name string, value Expr) *ConstDecl {
	if value == nil {
		value = NewNullLit()
	}
	return &ConstDecl{base: newBase(), name: name, value: value}
}

// Name returns the constant name.
func (n *ConstDecl) Name() string { return n.name }

// Value returns the constant value expression.
func (n *ConstDecl) Value() Expr { return n.value }

func (n *ConstDecl) String() string { return reprNode(n) }

// ImportDecl declares a Tool capability package dependency:
// import "provider";  (TOOL-SYMBOL-IMPORT P3.5). The provider slug is the
// namespace of the qualified capability call p.op({...}) — the only
// capability-call spelling since the legacy tool(name, action, {...}) builtin
// was deleted (zero compat); the declaration makes the package's operations
// visible for strong argument checking without introducing a new symbol or
// changing scope.
type ImportDecl struct {
	base
	provider string
}

// NewImportDecl constructs an ImportDecl.
func NewImportDecl(provider string) *ImportDecl {
	return &ImportDecl{base: newBase(), provider: provider}
}

// Provider returns the imported provider slug.
func (n *ImportDecl) Provider() string { return n.provider }

func (n *ImportDecl) String() string { return reprNode(n) }

// LibraryDecl is the library-unit identity: library <name> @ <semver>;
// It carries no IR and no ABI input; it names the declaration unit so a file
// is auditable as one library without relying on the file path alone.
type LibraryDecl struct {
	base
	name    string
	version string
}

// NewLibraryDecl constructs a LibraryDecl.
func NewLibraryDecl(name, version string) *LibraryDecl {
	return &LibraryDecl{base: newBase(), name: name, version: version}
}

// Name returns the declared library name.
func (n *LibraryDecl) Name() string { return n.name }

// Version returns the declared semantic version.
func (n *LibraryDecl) Version() string { return n.version }

func (n *LibraryDecl) String() string { return reprNode(n) }

// TypeDecl is a type definition: type <Name> = { <fields> };.
type TypeDecl struct {
	base
	name   string
	fields []*FieldDecl
}

// NewTypeDecl constructs a TypeDecl.
func NewTypeDecl(name string, fields []*FieldDecl) *TypeDecl {
	return &TypeDecl{base: newBase(), name: name, fields: fields}
}

// Name returns the type name.
func (n *TypeDecl) Name() string { return n.name }

// Fields returns the declared fields.
func (n *TypeDecl) Fields() []*FieldDecl { return n.fields }

func (n *TypeDecl) String() string { return reprNode(n) }

// EnumMemberDecl is one declared member of an enum (ast.py EnumMemberDecl).
type EnumMemberDecl struct {
	base
	name string
}

// NewEnumMemberDecl constructs an EnumMemberDecl.
func NewEnumMemberDecl(name string) *EnumMemberDecl {
	return &EnumMemberDecl{base: newBase(), name: name}
}

// Name returns the member name.
func (n *EnumMemberDecl) Name() string { return n.name }

func (n *EnumMemberDecl) String() string { return reprNode(n) }

// EnumDecl is an enum definition: enum <Name> { <members> };.
type EnumDecl struct {
	base
	name    string
	members []*EnumMemberDecl
}

// NewEnumDecl constructs an EnumDecl.
func NewEnumDecl(name string, members []*EnumMemberDecl) *EnumDecl {
	return &EnumDecl{base: newBase(), name: name, members: members}
}

// Name returns the enum name.
func (n *EnumDecl) Name() string { return n.name }

// Members returns the declared members.
func (n *EnumDecl) Members() []*EnumMemberDecl { return n.members }

func (n *EnumDecl) String() string { return reprNode(n) }

// ConversationVarDecl is a conversation-scoped variable:
// @conversation <type> <name> = <default>;.
type ConversationVarDecl struct {
	base
	name     string
	typeRef  TypeRef
	default_ Expr
}

// NewConversationVarDecl constructs a ConversationVarDecl.
func NewConversationVarDecl(name string, typeRef TypeRef, default_ Expr) *ConversationVarDecl {
	if typeRef == nil {
		typeRef = NewPrimTypeRef("string")
	}
	return &ConversationVarDecl{base: newBase(), name: name, typeRef: typeRef, default_: default_}
}

// Name returns the variable name.
func (n *ConversationVarDecl) Name() string { return n.name }

// TypeRef returns the variable type.
func (n *ConversationVarDecl) TypeRef() TypeRef { return n.typeRef }

// Default returns the default value expression (nil = none).
func (n *ConversationVarDecl) Default() Expr { return n.default_ }

func (n *ConversationVarDecl) String() string { return reprNode(n) }

// AgentDecl is the top-level agent application declaration for @mode agent
// (coze SingleAgent / dify AgentEntity alignment). Fields mirror
// spec.AgentConfig: model is the provider/name string, instruction is the
// system persona, max_iteration is the tool-round cap (0 → runtime default
// 10), and tools lists logical tool aliases wired to the runtime registry.
type AgentDecl struct {
	base
	model        string
	instruction  string
	strategy     string
	maxIteration int
	memory       bool
	memoryWindow int
	tools        []string
	// knowledge 检索配置（对齐 dify agent-chat 数据集检索）:dataset_ids 是
	// 数据集白名单,top_k 是检索条数（0 = 后端默认）。空 = 不启用。
	knowledgeDatasetIDs []string
	knowledgeTopK       int
}

// NewAgentDecl constructs an AgentDecl.
func NewAgentDecl(model, instruction, strategy string, maxIteration int, memory bool, memoryWindow int, tools []string) *AgentDecl {
	return &AgentDecl{base: newBase(), model: model, instruction: instruction, strategy: strategy, maxIteration: maxIteration, memory: memory, memoryWindow: memoryWindow, tools: tools}
}

// SetKnowledge pins the dataset retrieval configuration.
func (n *AgentDecl) SetKnowledge(datasetIDs []string, topK int) *AgentDecl {
	n.knowledgeDatasetIDs = datasetIDs
	n.knowledgeTopK = topK
	return n
}

// KnowledgeDatasetIDs returns the dataset whitelist (nil = disabled).
func (n *AgentDecl) KnowledgeDatasetIDs() []string { return n.knowledgeDatasetIDs }

// KnowledgeTopK returns the retrieval cap (0 = backend default).
func (n *AgentDecl) KnowledgeTopK() int { return n.knowledgeTopK }

// Model returns the provider/name model string.
func (n *AgentDecl) Model() string { return n.model }

// Instruction returns the pinned system persona.
func (n *AgentDecl) Instruction() string { return n.instruction }

// MaxIteration returns the tool-round cap (0 = runtime default).
func (n *AgentDecl) MaxIteration() int { return n.maxIteration }

// Strategy returns the agent reasoning strategy ("" = function-calling).
func (n *AgentDecl) Strategy() string { return n.strategy }

// Memory reports whether cross-turn conversation memory is enabled.
func (n *AgentDecl) Memory() bool { return n.memory }

// MemoryWindow returns the message-pair window (0 = unlimited).
func (n *AgentDecl) MemoryWindow() int { return n.memoryWindow }

// Tools returns the logical tool aliases.
func (n *AgentDecl) Tools() []string { return n.tools }

func (n *AgentDecl) String() string { return reprNode(n) }

// ParamDecl is a function parameter: <type> <name> [= default]
// (ast.py ParamDecl).
type ParamDecl struct {
	base
	name     string
	typeRef  TypeRef
	default_ Expr
}

// NewParamDecl constructs a ParamDecl.
func NewParamDecl(name string, typeRef TypeRef, default_ Expr) *ParamDecl {
	if typeRef == nil {
		typeRef = NewPrimTypeRef("string")
	}
	return &ParamDecl{base: newBase(), name: name, typeRef: typeRef, default_: default_}
}

// Name returns the parameter name.
func (n *ParamDecl) Name() string { return n.name }

// TypeRef returns the parameter type.
func (n *ParamDecl) TypeRef() TypeRef { return n.typeRef }

// Default returns the parameter default expression (nil = required).
func (n *ParamDecl) Default() Expr { return n.default_ }

func (n *ParamDecl) String() string { return reprNode(n) }

// FunctionDef is a function definition:
//
//	function <name>(<params>) -> <return_type> { <body> }.
type FunctionDef struct {
	base
	name       string
	params     []*ParamDecl
	returnType TypeRef
	body       *Block
}

// NewFunctionDef constructs a FunctionDef. A nil params slice normalizes to
// an empty (non-nil) slice so callers can rely on Params() being iterable.
func NewFunctionDef(name string, params []*ParamDecl, returnType TypeRef, body *Block) *FunctionDef {
	if params == nil {
		params = []*ParamDecl{}
	}
	if body == nil {
		body = NewBlock(nil)
	}
	return &FunctionDef{base: newBase(), name: name, params: params, returnType: returnType, body: body}
}

// Name returns the function name.
func (n *FunctionDef) Name() string { return n.name }

// Params returns the parameter declarations (never nil).
func (n *FunctionDef) Params() []*ParamDecl { return n.params }

// ReturnType returns the return type (nil = none).
func (n *FunctionDef) ReturnType() TypeRef { return n.returnType }

// Body returns the function body block.
func (n *FunctionDef) Body() *Block { return n.body }

func (n *FunctionDef) String() string { return reprNode(n) }

// CodeFunctionDef is a compiler-analyzable calculation function declared
// with `code`. Mirrors ast.py `class CodeFunctionDef(FunctionDef)` by
// embedding FunctionDef.
type CodeFunctionDef struct {
	FunctionDef
}

// NewCodeFunctionDef constructs a CodeFunctionDef.
func NewCodeFunctionDef(name string, params []*ParamDecl, returnType TypeRef, body *Block) *CodeFunctionDef {
	if params == nil {
		params = []*ParamDecl{}
	}
	if body == nil {
		body = NewBlock(nil)
	}
	return &CodeFunctionDef{FunctionDef: FunctionDef{base: newBase(), name: name, params: params, returnType: returnType, body: body}}
}

func (n *CodeFunctionDef) String() string { return reprNode(n) }

// SubflowDecl 是顶层子工作流声明:
// subflow <name> = ("<workflowId>", "<version>") { input: { ... }, output: { ... } };
type SubflowDecl struct {
	base
	name            string
	workflowID      string
	workflowVersion string
	spaceID         string
	inputFields     []*FieldDecl
	outputFields    []*FieldDecl
}

// NewSubflowDecl constructs a SubflowDecl.
func NewSubflowDecl(name, workflowID, workflowVersion string, inputFields, outputFields []*FieldDecl) *SubflowDecl {
	if inputFields == nil {
		inputFields = []*FieldDecl{}
	}
	if outputFields == nil {
		outputFields = []*FieldDecl{}
	}
	return &SubflowDecl{base: newBase(), name: name, workflowID: workflowID, workflowVersion: workflowVersion, inputFields: inputFields, outputFields: outputFields}
}

// Name returns the subflow name.
func (n *SubflowDecl) Name() string { return n.name }

// WorkflowID returns the subworkflow ID.
func (n *SubflowDecl) WorkflowID() string { return n.workflowID }

// WorkflowVersion returns the subworkflow version.
func (n *SubflowDecl) WorkflowVersion() string { return n.workflowVersion }

// SpaceID returns the subworkflow space ID (coze-biz).
func (n *SubflowDecl) SpaceID() string { return n.spaceID }

// SetSpaceID sets the space ID (coze-biz).
func (n *SubflowDecl) SetSpaceID(id string) { n.spaceID = id }

// InputFields returns the input contract record fields.
func (n *SubflowDecl) InputFields() []*FieldDecl { return n.inputFields }

// OutputFields returns the output contract record fields.
func (n *SubflowDecl) OutputFields() []*FieldDecl { return n.outputFields }

func (n *SubflowDecl) String() string { return reprNode(n) }

// ProgramOption configures a Program at construction time.
type ProgramOption func(*Program)

// Program is the root of the AST, containing all top-level declarations
// (ast.py ProgramNode). Field order mirrors the Python dataclass:
// language_identity, mode, const_decls, outputs, enum_decls, type_decls,
// conversation_decls, code_functions, functions, entry.
type Program struct {
	base
	languageIdentity  string
	mode              string
	imports           []*ImportDecl
	constDecls        []*ConstDecl
	outputs           []*OutputDecl
	enumDecls         []*EnumDecl
	typeDecls         []*TypeDecl
	conversationDecls []*ConversationVarDecl
	agentDecl         *AgentDecl
	libraryDecl       *LibraryDecl
	codeFunctions     []*CodeFunctionDef
	subflowDecls      []*SubflowDecl
	functions         []*FunctionDef
	entry             *FunctionDef
}

// NewProgram constructs a Program. The mode defaults to "workflow"; the
// language identity defaults to empty (None in Python).
func NewProgram(opts ...ProgramOption) *Program {
	p := &Program{base: newBase(), mode: "workflow"}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// WithLanguageIdentity sets the program language identity.
func WithLanguageIdentity(identity string) ProgramOption {
	return func(p *Program) { p.languageIdentity = identity }
}

// WithLibraryDecl sets the top-level library declaration (nil = none).
func WithLibraryDecl(d *LibraryDecl) ProgramOption {
	return func(p *Program) { p.libraryDecl = d }
}

// WithAgentDecl sets the top-level @agent declaration (nil = none).
func WithAgentDecl(d *AgentDecl) ProgramOption {
	return func(p *Program) { p.agentDecl = d }
}

// AgentDecl returns the top-level agent declaration (nil for non-agent modes).
func (p *Program) AgentDecl() *AgentDecl { return p.agentDecl }

// WithMode sets the program mode ("workflow", "advanced-chat" or "agent").
func WithMode(mode string) ProgramOption {
	return func(p *Program) { p.mode = mode }
}

// WithImports sets the top-level capability import declarations.
func WithImports(decls []*ImportDecl) ProgramOption {
	return func(p *Program) { p.imports = decls }
}

// WithConstDecls sets the top-level constant declarations.
func WithConstDecls(decls []*ConstDecl) ProgramOption {
	return func(p *Program) { p.constDecls = decls }
}

// WithOutputs sets the top-level output declarations.
func WithOutputs(outputs []*OutputDecl) ProgramOption {
	return func(p *Program) { p.outputs = outputs }
}

// WithEnumDecls sets the top-level enum declarations.
func WithEnumDecls(decls []*EnumDecl) ProgramOption {
	return func(p *Program) { p.enumDecls = decls }
}

// WithTypeDecls sets the top-level type declarations.
func WithTypeDecls(decls []*TypeDecl) ProgramOption {
	return func(p *Program) { p.typeDecls = decls }
}

// WithConversationDecls sets the top-level conversation variable declarations.
func WithConversationDecls(decls []*ConversationVarDecl) ProgramOption {
	return func(p *Program) { p.conversationDecls = decls }
}

// WithCodeFunctions sets the top-level code function definitions.
func WithSubflowDecls(decls []*SubflowDecl) ProgramOption {
	return func(p *Program) { p.subflowDecls = decls }
}

// SubflowDecls returns the top-level subflow declarations.
func (p *Program) SubflowDecls() []*SubflowDecl { return p.subflowDecls }

func WithCodeFunctions(funcs []*CodeFunctionDef) ProgramOption {
	return func(p *Program) { p.codeFunctions = funcs }
}

// WithFunctions sets the top-level function definitions.
func WithFunctions(funcs []*FunctionDef) ProgramOption {
	return func(p *Program) { p.functions = funcs }
}

// WithEntry sets the entry function.
func WithEntry(entry *FunctionDef) ProgramOption {
	return func(p *Program) { p.entry = entry }
}

// LanguageIdentity returns the declared language identity ("" if none).
func (p *Program) LanguageIdentity() string { return p.languageIdentity }

// Library returns the top-level library declaration, or nil.
func (p *Program) Library() *LibraryDecl { return p.libraryDecl }

// Mode returns the program mode.
func (p *Program) Mode() string { return p.mode }

// Imports returns the top-level capability import declarations.
func (p *Program) Imports() []*ImportDecl { return p.imports }

// ConstDecls returns the top-level constant declarations.
func (p *Program) ConstDecls() []*ConstDecl { return p.constDecls }

// Outputs returns the top-level output declarations.
func (p *Program) Outputs() []*OutputDecl { return p.outputs }

// EnumDecls returns the top-level enum declarations.
func (p *Program) EnumDecls() []*EnumDecl { return p.enumDecls }

// TypeDecls returns the top-level type declarations.
func (p *Program) TypeDecls() []*TypeDecl { return p.typeDecls }

// ConversationDecls returns the top-level conversation variable declarations.
func (p *Program) ConversationDecls() []*ConversationVarDecl { return p.conversationDecls }

// CodeFunctions returns the top-level code function definitions.
func (p *Program) CodeFunctions() []*CodeFunctionDef { return p.codeFunctions }

// Functions returns the top-level function definitions.
func (p *Program) Functions() []*FunctionDef { return p.functions }

// Entry returns the entry function (nil if none).
func (p *Program) Entry() *FunctionDef { return p.entry }

func (p *Program) String() string { return reprNode(p) }
