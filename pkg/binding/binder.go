package binding

// Binder: scope building + symbol registration, ported from
// src/nclang/lang/passes/scope_pass.py (ScopePass.run + the per-node visit
// methods) with the node-identity enumeration from bindings.py
// BindingBuilder.__init__ (_iter_proc_nodes).
//
// Bind walks a Program once, builds the scope tree (global -> entry/function
// -> blocks), registers every symbol (builtins, std namespace, enums,
// records, conversation variables, functions, parameters, locals, loop
// variables), resolves identifier references, and reports undefined /
// duplicate diagnostics with parity to the Python scope pass (message text
// and diagnostic codes from passes/base.py DSLDiagnosticCode).
//
// AstNodeID assignment is a per-compile 1-based traversal enumeration
// (Python AstNodeId ordinals), deliberately NOT the process-global
// ast.NodeID construction counter, so two Bind runs over the same logical
// input mint identical ID sequences (determinism regression in
// binder_test.go).
//
// Deferred to later tasks (recorded in the task-10 report): target-profile
// operations (no TargetProfile port yet), the full stdlib namespace type
// (stdlib registry is its own subsystem), BoundProgram/call-graph building
// (binder.py), and code-signature/effect/loop-state facts.
//
// TargetProfile is a HARD dependency for full parity: the reference
// scope_pass registers the operation builtins (agent/extract_text/extract/
// classify) with operation function types BEFORE the std namespace, which
// (a) lets a user function shadow an operation builtin instead of reporting
// DUPLICATE_DEF (scope_pass.py:240-244), (b) mutates those builtins'
// type_info to the operation signature (scope_pass.py:62-66), and (c)
// shifts every later SymbolID by exactly 4 (Python: agent=23..classify=26,
// std=27). The Go binder registers extract_text (Task 25c), agent (Task
// 25d) and classify (Task 25e); extract stays unregistered until Task 25f,
// so the SymbolID offset deviation narrows to 1. No downstream pass may
// compensate with a literal "+1" offset.

import (
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/importns"
	"github.com/nodecoda/ncoda/pkg/token"
	"github.com/nodecoda/ncoda/pkg/types"
)

// Bind runs the scope pass over program and returns the immutable binding
// snapshot plus the collected diagnostics. The snapshot is always produced
// (even when errors are reported), mirroring the reference pipeline which
// publishes bindings after the pass loop regardless of errors.
func Bind(program *ast.Program) (*Snapshot, []diag.Diagnostic) {
	b := &binder{
		program:            program,
		chain:              NewChain(),
		b:                  NewBuilder(),
		namedTypes:         map[string]*types.Descriptor{},
		registered:         map[*Symbol]SymbolID{},
		symbolsByID:        map[SymbolID]*Symbol{},
		declared:           map[*Symbol]bool{},
		functionBySymbol:   map[SymbolID]FunctionID{},
		conversationVarIDs: map[SymbolID]bool{},
	}
	if m := program.Mode(); m != "" {
		b.mode = m
	} else {
		b.mode = "workflow"
	}
	b.enumerate(program)
	b.run()
	return b.b.Publish(), b.diags
}

// binder carries the per-compile state of one scope pass, mirroring
// ScopePass.run's locals plus the DSLPassContext surfaces the pass drives.
type binder struct {
	program *ast.Program
	mode    string

	chain *Chain
	b     *Builder

	// namedTypes mirrors DSLPassContext.named_types.
	namedTypes map[string]*types.Descriptor

	diags []diag.Diagnostic

	// registered / symbolsByID mirror BindingBuilder._symbol_entries /
	// _symbols (symbol identity -> SymbolID and SymbolID -> record).
	registered  map[*Symbol]SymbolID
	symbolsByID map[SymbolID]*Symbol

	// declared tracks symbols that already produced a SymbolDeclaration
	// record, so registerSymbol stays idempotent across the define-first
	// paths (defineIfFree/mintDefine mint the ID, registerSymbol writes the
	// declaration exactly once).
	declared map[*Symbol]bool

	// functionBySymbol mirrors BindingBuilder._function_entries: the
	// FunctionID minted for each registered function symbol (bind_call).
	functionBySymbol map[SymbolID]FunctionID

	// functionScopes / loopScopes mirror ScopePass._function_scopes /
	// _loop_scopes (for bind_exit on return/break/continue).
	functionScopes []ScopeID
	loopScopes     []ScopeID

	// conversationVarIDs mirrors DSLPassContext.conversation_var_ids. Not
	// published in the snapshot; consumed by later (unported) passes.
	conversationVarIDs map[SymbolID]bool
}

// ── node enumeration (bindings.py _iter_proc_nodes) ───────────────────

// enumerate assigns 1-based AstNodeIDs in Python _iter_proc_nodes order:
// pre-order DFS; Program yields itself then its declarations sorted by
// source offset (Python sorts with span None -> -1, which the Go zero Span
// mirrors); every other node yields itself then its dataclass fields in
// declaration order (the Go AST getters mirror those field orders).
func (b *binder) enumerate(program *ast.Program) {
	var order []ast.Node
	b.enumNode(program, &order)
	for i, n := range order {
		id := AstNodeID(i + 1)
		b.b.NodeIDs[n] = id
		b.b.AstNodes[id] = NodeInfo{Span: n.Span(), Kind: nodeKindName(n)}
		// Go models Python's single-object CodeFunctionDef(FunctionDef) as an
		// embedded struct: the wrapper and its embedded *FunctionDef are two
		// addresses for one logical node. Binder code works with the embedded
		// *ast.FunctionDef (functionDefOf), so alias it to the same AstNodeID
		// — ID count/order stays exactly Python's (_iter_proc_nodes yields
		// the wrapper once). Without the alias, mustID on a code function
		// panics ("binding: node was not enumerated: FunctionDef").
		if cf, ok := n.(*ast.CodeFunctionDef); ok {
			b.b.NodeIDs[&cf.FunctionDef] = id
		}
	}
}

// nodeKindName returns the Python-style class name for a node. The Go port
// renamed ProgramNode to Program, so that one entry differs (cosmetic; the
// NodeInfo.Kind strings are not part of any asserted contract).
func nodeKindName(n ast.Node) string {
	return reflect.TypeOf(n).Elem().Name()
}

func (b *binder) enumNode(n ast.Node, out *[]ast.Node) {
	if n == nil {
		return
	}
	if rv := reflect.ValueOf(n); rv.Kind() == reflect.Ptr && rv.IsNil() {
		return // typed-nil child (e.g. a nil *Block in an interface)
	}
	*out = append(*out, n)
	switch n := n.(type) {
	case *ast.Program:
		for _, d := range programDeclarations(n) {
			b.enumNode(d, out)
		}
	case *ast.FunctionDef, *ast.CodeFunctionDef:
		fn := functionDefOf(n)
		for _, p := range fn.Params() {
			b.enumNode(p, out)
		}
		b.enumNode(fn.ReturnType(), out)
		b.enumNode(fn.Body(), out)
	case *ast.ParamDecl:
		b.enumNode(n.TypeRef(), out)
		b.enumNode(n.Default(), out)
	case *ast.ConstDecl:
		b.enumNode(n.Value(), out)
	case *ast.OutputDecl:
		b.enumNode(n.TypeRef(), out)
	case *ast.EnumDecl:
		for _, m := range n.Members() {
			b.enumNode(m, out)
		}
	case *ast.EnumMemberDecl:
		// leaf
	case *ast.TypeDecl:
		for _, f := range n.Fields() {
			b.enumNode(f, out)
		}
	case *ast.FieldDecl:
		b.enumNode(n.TypeRef(), out)
	case *ast.SubflowDecl:
		for _, f := range n.InputFields() {
			b.enumNode(f, out)
		}
		for _, f := range n.OutputFields() {
			b.enumNode(f, out)
		}
	case *ast.ConversationVarDecl:
		b.enumNode(n.TypeRef(), out)
		b.enumNode(n.Default(), out)
	case *ast.Block:
		for _, s := range n.Stmts() {
			b.enumNode(s, out)
		}
	case *ast.VarDeclStmt:
		b.enumNode(n.TypeRef(), out)
		b.enumNode(n.Init(), out)
	case *ast.AssignStmt:
		b.enumNode(n.Target(), out)
		b.enumNode(n.Value(), out)
	case *ast.ExprStmt:
		b.enumNode(n.Expr(), out)
	case *ast.IfStmt:
		b.enumNode(n.Condition(), out)
		b.enumNode(n.ThenBody(), out)
		for _, ei := range n.ElseIfs() {
			b.enumNode(ei, out)
		}
		b.enumNode(n.ElseBody(), out)
	case *ast.ElseIfBranch:
		b.enumNode(n.Condition(), out)
		b.enumNode(n.Body(), out)
	case *ast.ForStmt:
		for i, src := range n.LoopSources() {
			b.enumNode(src, out)
			if i < len(n.LoopTypes()) {
				b.enumNode(n.LoopTypes()[i], out)
			}
		}
		b.enumNode(n.Body(), out)
	case *ast.WhileStmt:
		b.enumNode(n.Condition(), out)
		b.enumNode(n.Body(), out)
	case *ast.ReturnStmt:
		b.enumNode(n.Value(), out)
	case *ast.TemplateValue:
		// 名额值是表达式（节点枚举必须看见它们，否则 bindScope 的
		// mustID 直接 panic：枚举表 = 作用域快照的键空间）。
		for _, bd := range n.Bindings() {
			b.enumNode(bd.Value, out)
		}
	case *ast.YieldStmt:
		b.enumNode(n.Value(), out)
	case *ast.OutputStmt:
		b.enumNode(n.Value(), out)
	case *ast.ParallelStmt:
		for _, br := range n.Branches() {
			b.enumNode(br, out)
		}
	case *ast.AttemptStmt:
		b.enumNode(n.Operation(), out)
		b.enumNode(n.ResultBinding(), out)
		b.enumNode(n.SuccessBody(), out)
		b.enumNode(n.ErrorBinding(), out)
		b.enumNode(n.FailureBody(), out)
	case *ast.HumanActionBranch:
		b.enumNode(n.ActionExpr(), out)
		b.enumNode(n.Body(), out)
	case *ast.SwitchStmt:
		b.enumNode(n.Query(), out)
		b.enumNode(n.Config(), out)
		b.enumNode(n.Result(), out)
		for _, br := range n.Branches() {
			b.enumNode(br, out)
		}
		b.enumNode(n.DefaultBody(), out)
	case *ast.SwitchBranch:
		b.enumNode(n.Body(), out)
	case *ast.RequestInputStmt:
		b.enumNode(n.FormType(), out)
		b.enumNode(n.ActionType(), out)
		b.enumNode(n.Config(), out)
		b.enumNode(n.ResultBinding(), out)
		for _, br := range n.ActionBranches() {
			b.enumNode(br, out)
		}
		b.enumNode(n.TimeoutBody(), out)
	case *ast.BreakStmt, *ast.ContinueStmt:
		// leaf
	case *ast.TemplateString:
		for _, part := range n.Parts() {
			b.enumNode(part.Expr, out)
		}
	case *ast.ArrayLit:
		for _, e := range n.Elements() {
			b.enumNode(e, out)
		}
	case *ast.MapLit:
		for _, en := range n.Entries() {
			b.enumNode(en.Key, out)
			b.enumNode(en.Value, out)
		}
	case *ast.IdentExpr, *ast.IntLit, *ast.FloatLit, *ast.StringLit,
		*ast.BoolLit, *ast.NullLit:
		// leaf
	case *ast.FieldAccessExpr:
		b.enumNode(n.Obj(), out)
	case *ast.IndexExpr:
		b.enumNode(n.Obj(), out)
		b.enumNode(n.Index(), out)
	case *ast.CallExpr:
		b.enumNode(n.Func(), out)
		for _, t := range n.TypeArgs() {
			b.enumNode(t, out)
		}
		for _, a := range n.Args() {
			b.enumNode(a, out)
		}
	case *ast.OperationPolicyExpr:
		b.enumNode(n.Operation(), out)
		for _, p := range n.Policies() {
			b.enumNode(p, out)
		}
	case *ast.RetryPolicy, *ast.TimeoutPolicy:
		// leaf
	case *ast.DefaultPolicy:
		b.enumNode(n.Value(), out)
	case *ast.UnaryExpr:
		b.enumNode(n.Operand(), out)
	case *ast.BinaryExpr:
		b.enumNode(n.Left(), out)
		b.enumNode(n.Right(), out)
	case *ast.TernaryExpr:
		b.enumNode(n.Condition(), out)
		b.enumNode(n.ThenVal(), out)
		b.enumNode(n.ElseVal(), out)
	case *ast.LambdaExpr:
		b.enumNode(n.Body(), out)
	case *ast.ForExpr:
		for i, src := range n.LoopSources() {
			b.enumNode(src, out)
			if i < len(n.LoopTypes()) {
				b.enumNode(n.LoopTypes()[i], out)
			}
		}
		b.enumNode(n.Body(), out)
	case *ast.ParallelForExpr:
		for i, src := range n.LoopSources() {
			b.enumNode(src, out)
			if i < len(n.LoopTypes()) {
				b.enumNode(n.LoopTypes()[i], out)
			}
		}
		b.enumNode(n.Body(), out)
	case *ast.CodeInputBinding:
		b.enumNode(n.TypeRef(), out)
		b.enumNode(n.Value(), out)
	case *ast.CodeDirectOutput:
		b.enumNode(n.TypeRef(), out)
	case *ast.CodeOutputField:
		b.enumNode(n.TypeRef(), out)
	case *ast.CodeStructuralOutput:
		for _, f := range n.Fields() {
			b.enumNode(f, out)
		}
	case *ast.ForeignCodeExpr:
		for _, in := range n.Inputs() {
			b.enumNode(in, out)
		}
		b.enumNode(n.Output(), out)
		b.enumNode(n.Source(), out)
	case *ast.ArrayTypeRef:
		b.enumNode(n.Element(), out)
	case *ast.MapTypeRef:
		b.enumNode(n.Key(), out)
		b.enumNode(n.Value(), out)
	case *ast.OptionalTypeRef:
		b.enumNode(n.Value(), out)
	case *ast.PrimTypeRef, *ast.FileTypeRef, *ast.NamedTypeRef:
		// leaf
	}
}

// functionDefOf normalizes the *FunctionDef / *CodeFunctionDef union (Go
// embeds FunctionDef by value, Python subclasses it).
func functionDefOf(n ast.Node) *ast.FunctionDef {
	switch n := n.(type) {
	case *ast.FunctionDef:
		return n
	case *ast.CodeFunctionDef:
		return &n.FunctionDef
	}
	panic("binding: functionDefOf: not a function node")
}

// spanKey mirrors Python's `declaration.span.start.offset if declaration.span
// is not None else -1` used for Program-declaration and function sorting.
func spanKey(n ast.Node) int {
	if n == nil || n.Span() == (ast.Span{}) {
		return -1
	}
	return n.Span().Start.Offset
}

// programDeclarations mirrors _iter_proc_nodes' Program branch: every
// declaration list concatenated and sorted by source offset (Python sorts a
// flat list; entry joins the others).
func programDeclarations(p *ast.Program) []ast.Node {
	decls := []ast.Node{}
	for _, i := range p.Imports() {
		decls = append(decls, i)
	}
	for _, c := range p.ConstDecls() {
		decls = append(decls, c)
	}
	for _, o := range p.Outputs() {
		decls = append(decls, o)
	}
	for _, e := range p.EnumDecls() {
		decls = append(decls, e)
	}
	for _, t := range p.TypeDecls() {
		decls = append(decls, t)
	}
	for _, c := range p.ConversationDecls() {
		decls = append(decls, c)
	}
	for _, f := range p.CodeFunctions() {
		decls = append(decls, f)
	}
	for _, sd := range p.SubflowDecls() {
		decls = append(decls, sd)
	}
	for _, f := range p.Functions() {
		decls = append(decls, f)
	}
	if e := p.Entry(); e != nil {
		decls = append(decls, e)
	}
	sort.SliceStable(decls, func(i, j int) bool { return spanKey(decls[i]) < spanKey(decls[j]) })
	return decls
}

// ── scope pass (passes/scope_pass.py ScopePass.run) ───────────────────

func (b *binder) run() {
	// 1. Builtins in the global scope, then register every global symbol
	// (Python registers builtins + std through the builder so they receive
	// SymbolIDs before any user declaration).
	b.registerScope(b.chain.Global(), nil, 0)
	RegisterBuiltins(b.chain)
	// Operation builtins (scope_pass.py:61-73): registered after the builtins
	// and before the std namespace. extract_text (Task 25c), agent (Task
	// 25d) and classify (Task 25e) are registered so their calls bind as
	// CallBuiltin and reach the operation dispatch; extract stays
	// unregistered until Task 25f (Task 10 SymbolID-offset deviation
	// narrowed to 1).
	RegisterOperationBuiltins(b.chain)
	// Agent-mode entry builtin: run(query) -> string (@mode agent only).
	if b.mode == "agent" {
		DefineRunBuiltin(b.chain)
	}
	// Advanced-chat entry builtin: conversation_history() -> string
	// (@mode advanced-chat only; M13 chatflow semantics).
	if b.mode == "advanced-chat" {
		DefineConversationHistoryBuiltin(b.chain)
	}
	// std namespace: the nested std.<version>.<function> record type
	// (stdlib/registry.py stdlib_namespace_type, ported as
	// StdlibNamespaceType in Task 24b) so the type pass narrows
	// std.v1.<function> field accesses to the manifest function descriptors
	// and validates stdlib calls with their real signatures.
	b.defineGlobal("std", NewNamespaceSymbol("std", NamespaceBuiltin, StdlibNamespaceType()))
	// json namespace (G8): declarative JSON projection primitive
	// `json.stringify(value) -> string`. Same pattern as std: a SymbolNamespace
	// with a nested record type so json.stringify field accesses narrow to the
	// function descriptor and the generic function-application path types the
	// call (zero type-pass changes).
	b.defineGlobal("json", NewNamespaceSymbol("json", NamespaceBuiltin, JSONNamespaceType()))
	// Package namespaces (TOOL-DIRECT-CALL-FORM): every import provider is a
	// namespace symbol in the global scope, mirroring the `std` namespace
	// registration. Members (operations) are resolved by the type pass
	// through the tool registry; the binder only makes the name resolvable
	// and detects shadowing. Registered before the global re-read below so
	// the namespaces receive SymbolIDs before any user declaration.
	//
	// I2: an import whose provider name collides with a builtin namespace
	// (std, json) is an explicit error — the parser already rejects
	// duplicate imports, so this is the only remaining name-conflict
	// surface. defineIfFree would silently swallow the conflicting import
	// (returning the builtin), which is precisely the silent-preference
	// behavior decision 6 forbids: the type pass would route the name
	// through the builtin record type while IR's import-namespace set would
	// still contain it (divergent lowering). Rejecting it up front keeps
	// the three discriminants (binder / type pass / IR) agreeing by
	// construction.
	for _, imp := range b.program.Imports() {
		// Platform-qualified imports ("coze-biz.web_search") register the
		// bare provider as the source-level namespace — the qualifier only
		// selects the platform binding (docs/PLATFORM-MECHANISM.md §5.1).
		// A bare import is invalid: bindings are platform-specific, so the
		// import must declare its platform (no backward-compat target
		// derivation). Unknown qualifiers are rejected the same way — the
		// rule lives here (language layer) so it holds even without
		// --tool-path.
		raw := imp.Provider()
		platform, name, serr := importns.Split(raw)
		if serr != nil {
			b.emit(diag.IMPORT_PLATFORM_REQUIRED, serr.Error(), imp.Span())
			continue
		}
		if platform == "" {
			b.emit(diag.IMPORT_PLATFORM_REQUIRED,
				"import must specify a platform: "+strconv.Quote(raw)+
					" (allowed: "+strings.Join(importns.PlatformNames, ", ")+")", imp.Span())
			continue
		}
		if existing := b.chain.Scope(b.chain.Global()).ResolveSymbolLocal(name); existing != nil && existing.Kind == SymbolNamespace {
			b.emit(diag.DUPLICATE_DEF,
				"Import provider '"+name+"' conflicts with the builtin namespace '"+name+"'", imp.Span())
			continue
		}
		b.defineGlobal(name, NewNamespaceSymbol(name, NamespaceImport, types.AnyType()))
	}
	// Register every global symbol (builtins + std) through the builder so
	// they receive SymbolIDs before any user declaration, mirroring Python's
	// `for symbol in global_scope.symbols.values(): register_symbol(...)`.
	// RegisterBuiltins already minted the builtin IDs in the chain (dict
	// order, matching Python's register_symbol mint order), so re-read them
	// instead of re-minting (which would shift every later SymbolID).
	global := b.chain.Scope(b.chain.Global())
	for _, sym := range global.SymbolsInOrder() {
		id, found := global.ResolveLocal(sym.Name)
		if !found {
			panic("binding: global symbol missing id: " + sym.Name)
		}
		b.registered[sym] = id
		b.symbolsByID[id] = sym
		// registerSymbol records the SymbolDeclaration/Decls entry; the
		// pre-seeded registered entry prevents a second ID mint.
		b.registerSymbol(sym, b.chain.Global(), nil)
	}

	// 2. Enums before records so record fields may reference later enums.
	for _, ed := range b.program.EnumDecls() {
		if _, found := b.namedTypes[ed.Name()]; found {
			b.emit(diag.DUPLICATE_TYPE, "Duplicate type definition: '"+ed.Name()+"'", ed.Span())
			continue
		}
		if len(ed.Members()) == 0 {
			b.emit(diag.TYPE_MISMATCH, "Enum '"+ed.Name()+"' must declare at least one member", ed.Span())
			continue
		}
		memberNames := []string{}
		seen := map[string]bool{}
		duplicate := false
		for _, m := range ed.Members() {
			if seen[m.Name()] {
				duplicate = true
				b.emit(diag.DUPLICATE_DEF, "Duplicate enum member: '"+ed.Name()+"."+m.Name()+"'", m.Span())
				continue
			}
			seen[m.Name()] = true
			memberNames = append(memberNames, m.Name())
		}
		if duplicate {
			continue
		}
		typeDesc := types.EnumType(ed.Name(), memberNames)
		b.namedTypes[ed.Name()] = typeDesc
		sym := NewSymbol(ed.Name(), SymbolType, typeDesc, false, false)
		if existing := b.defineGlobal(ed.Name(), sym); existing != nil {
			b.emit(diag.DUPLICATE_DEF, "Duplicate type definition: '"+ed.Name()+"'", ed.Span())
		} else {
			b.registerSymbol(sym, b.chain.Global(), ed)
		}
	}

	// 2.1. Records.
	for _, td := range b.program.TypeDecls() {
		if _, found := b.namedTypes[td.Name()]; found {
			b.emit(diag.DUPLICATE_TYPE, "Duplicate type definition: '"+td.Name()+"'", td.Span())
			continue
		}
		fields := []types.RecordField{}
		for _, f := range td.Fields() {
			fields = append(fields, types.RecordField{Name: f.Name(), Type: b.typeRefToDescriptor(f.TypeRef())})
		}
		typeDesc := types.RecordType(td.Name(), fields)
		b.namedTypes[td.Name()] = typeDesc
		// Python: Symbol(name=..., kind=TYPE, type_info=...) — is_mutable
		// defaults to True in symbol_table.py; ported faithfully.
		sym := NewSymbol(td.Name(), SymbolType, typeDesc, true, false)
		if existing := b.defineGlobal(td.Name(), sym); existing != nil {
			b.emit(diag.DUPLICATE_DEF, "Duplicate type definition: '"+td.Name()+"'", td.Span())
		} else {
			b.registerSymbol(sym, b.chain.Global(), td)
		}
	}

	// 2.5. Conversation variables in the global scope.
	for _, cd := range b.program.ConversationDecls() {
		typeDesc := b.typeRefToDescriptor(cd.TypeRef())
		sym := NewSymbol(cd.Name(), SymbolVariable, typeDesc, true, false)
		if existing := b.defineGlobal(cd.Name(), sym); existing != nil {
			b.emit(diag.DUPLICATE_DEF, "Duplicate conversation variable: '"+cd.Name()+"'", cd.Span())
		} else {
			b.registerSymbol(sym, b.chain.Global(), cd)
			b.conversationVarIDs[b.registered[sym]] = true
		}
	}

	// 2.6. Subflow declarations (SUBWORKFLOW-DECLARATION): each subflow name
	// registers as a global function symbol whose signature is
	// (<input record>) -> <output record>. The single parameter is the
	// DECLARED input contract as a record (D-P31-1): the call-site map
	// literal is then checked by the ordinary record-literal rules, so a key
	// that is not declared is a language error instead of a silently emitted
	// node_inputs entry the subflow cannot receive. IR build_call lowers the
	// call to a SubWorkflowCall using the SubflowDecl identity
	// (workflowId/version).
	for _, sd := range b.program.SubflowDecls() {
		inFields := []types.RecordField{}
		for _, f := range sd.InputFields() {
			inFields = append(inFields, types.RecordField{Name: f.Name(), Type: b.typeRefToDescriptor(f.TypeRef())})
		}
		fields := []types.RecordField{}
		for _, f := range sd.OutputFields() {
			fields = append(fields, types.RecordField{Name: f.Name(), Type: b.typeRefToDescriptor(f.TypeRef())})
		}
		outType := types.RecordType("SubflowResult["+sd.Name()+"]", fields)
		inType := types.RecordType("SubflowInput["+sd.Name()+"]", inFields)
		funcType := types.FunctionType([]types.Descriptor{*inType}, outType)
		sym := NewSymbol(sd.Name(), SymbolFunction, funcType, true, false)
		if existing := b.defineGlobal(sd.Name(), sym); existing != nil {
			b.emit(diag.DUPLICATE_DEF, "Duplicate subflow definition: '"+sd.Name()+"'", sd.Span())
		} else {
			b.registerSymbol(sym, b.chain.Global(), sd)
		}
	}

	// Output declarations carry public NodeCoda types even though they do
	// not define symbols (Python resolves them for UNKNOWN_TYPE reporting).
	for _, o := range b.program.Outputs() {
		b.typeRefToDescriptor(o.TypeRef())
	}

	// 3. Function signatures (forward declarations), in source order.
	functions := []*ast.FunctionDef{}
	for _, f := range b.program.CodeFunctions() {
		functions = append(functions, &f.FunctionDef)
	}
	for _, f := range b.program.Functions() {
		functions = append(functions, f)
	}
	if e := b.program.Entry(); e != nil {
		functions = append(functions, e)
	}
	sort.SliceStable(functions, func(i, j int) bool { return spanKey(functions[i]) < spanKey(functions[j]) })
	for _, fn := range functions {
		paramTypes := []types.Descriptor{}
		// Python resolves signature params WITHOUT diagnostics (unknown
		// named types silently recover to ANY in this call).
		for _, p := range fn.Params() {
			paramTypes = append(paramTypes, *b.typeRefToDescriptorQuiet(p.TypeRef()))
		}
		retType := types.AnyType()
		if fn.ReturnType() != nil {
			retType = b.typeRefToDescriptor(fn.ReturnType())
		}
		funcType := types.FunctionType(paramTypes, retType)
		sym := NewSymbol(fn.Name(), SymbolFunction, funcType, true, false)
		existing := b.chain.Scope(b.chain.Global()).ResolveSymbolLocal(fn.Name())
		// Python additionally lets a user function overwrite a builtin when
		// target_profile.operations.lookup(name) matches
		// (scope_pass.py:240-244): a user function named llm/http/tool/
		// knowledge then REPLACES the builtin instead of reporting
		// DUPLICATE_DEF. No TargetProfile is ported yet, so that branch is
		// unreachable and a colliding user function is a duplicate here
		// (deviation recorded; TargetProfile is a hard dependency — see the
		// header note; do NOT add a "+4" SymbolID compensation).
		existing = b.defineGlobal(fn.Name(), sym)
		if existing != nil {
			b.emit(diag.DUPLICATE_DEF, "Duplicate function definition: '"+fn.Name()+"'", fn.Span())
		} else {
			b.registerSymbol(sym, b.chain.Global(), fn)
			b.registerFunction(fn, sym)
		}
	}

	// 4. Walk every function in lexical order (entry last in the sorted
	// list if it sorts there; Python walks entry with ENTRY scope kind).
	for _, fn := range functions {
		if fn == b.program.Entry() {
			b.walkEntry(fn)
		} else {
			b.walkFunction(fn)
		}
	}

	// 5. Publish the named-type environment (Task 15 T1): Python exposes the
	// same table as DSLPassContext.named_types / BindingSnapshot.named_types;
	// the Go Snapshot carries it so the scope pass can copy it into the
	// Context before TypePass runs.
	b.b.NamedTypes = b.namedTypes

	// 5.1. Publish the conversation-variable symbol set (Task 16 D1): Python
	// maintains ctx.conversation_var_ids in the scope pass (scope_pass.py:211);
	// the binder collects the same set while registering conversation
	// declarations, so it is published through the snapshot for the scope
	// pass to copy into Context.ConversationVarIDs before LoopStatePass runs.
	b.b.ConversationVarIDs = b.conversationVarIDs
}

// ── visitor (ScopePass.visit + per-node methods) ──────────────────────

// walk mirrors ScopePass.visit: every visited node is bound to the current
// scope, then the node-specific logic runs. Nodes without a scope-pass
// handler are leaves (Python generic_visit walks no children), which covers
// type references and top-level declarations — the walk only reaches nodes
// inside function bodies plus parameter default expressions.
func (b *binder) walk(n ast.Node) {
	if n == nil {
		return
	}
	if rv := reflect.ValueOf(n); rv.Kind() == reflect.Ptr && rv.IsNil() {
		return
	}
	b.bindScope(n)
	switch n := n.(type) {
	case *ast.Block:
		b.walkStmts(n.Stmts())
	case *ast.VarDeclStmt:
		b.visitVarDeclStmt(n)
	case *ast.AssignStmt:
		b.walk(n.Target())
		b.walk(n.Value())
	case *ast.ExprStmt:
		b.walk(n.Expr())
	case *ast.IfStmt:
		b.visitIfStmt(n)
	case *ast.ElseIfBranch:
		b.visitElseIfBranch(n)
	case *ast.ForStmt:
		b.visitForStmt(n)
	case *ast.WhileStmt:
		b.visitWhileStmt(n)
	case *ast.ReturnStmt:
		b.visitReturnStmt(n)
	case *ast.YieldStmt:
		b.walk(n.Value())
	case *ast.OutputStmt:
		b.walk(n.Value())
	case *ast.ParallelStmt:
		b.visitParallelStmt(n)
	case *ast.AttemptStmt:
		b.visitAttemptStmt(n)
	case *ast.HumanActionBranch:
		b.walk(n.ActionExpr())
		b.walk(n.Body())
	case *ast.RequestInputStmt:
		b.visitRequestInputStmt(n)
	case *ast.SwitchStmt:
		b.visitSwitchStmt(n)
	case *ast.BreakStmt:
		b.visitBreakStmt(n)
	case *ast.ContinueStmt:
		b.visitContinueStmt(n)
	case *ast.IdentExpr:
		b.visitIdentExpr(n)
	case *ast.FieldAccessExpr:
		b.walk(n.Obj())
	case *ast.IndexExpr:
		b.walk(n.Obj())
		b.walk(n.Index())
	case *ast.CallExpr:
		b.visitCallExpr(n)
	case *ast.OperationPolicyExpr:
		b.walk(n.Operation())
		for _, p := range n.Policies() {
			b.walk(p)
		}
	case *ast.RetryPolicy, *ast.TimeoutPolicy:
		// leaf
	case *ast.DefaultPolicy:
		b.walk(n.Value())
	case *ast.UnaryExpr:
		b.walk(n.Operand())
	case *ast.BinaryExpr:
		b.walk(n.Left())
		b.walk(n.Right())
	case *ast.TernaryExpr:
		b.walk(n.Condition())
		b.walk(n.ThenVal())
		b.walk(n.ElseVal())
	case *ast.LambdaExpr:
		b.visitLambdaExpr(n)
	case *ast.TemplateString:
		for _, part := range n.Parts() {
			b.walk(part.Expr)
		}
	case *ast.ArrayLit:
		for _, e := range n.Elements() {
			b.walk(e)
		}
	case *ast.MapLit:
		b.visitMapLit(n)
	case *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.BoolLit, *ast.NullLit:
		// leaf
	case *ast.TemplateValue:
		// `return template "…" [using {…}]`（W2）：模板文本是**不透明文本**
		// （不解析、不校验），名额值是**真实引用位置**（值路径引用上游节点
		// 输出口）⇒ 逐个走 binder。缺这条 = 名额里的名字解析不到 ⇒ 合法源被
		// 判 UNDEFINED_VAR（编译器的锅，不是语言错）。
		for _, bd := range n.Bindings() {
			if bd.Value != nil {
				b.walk(bd.Value)
			}
		}
	case *ast.ForExpr:
		b.visitForExpr(n, n.LoopTypes(), n.LoopSources(), n.LoopVars(), n.Body())
	case *ast.ParallelForExpr:
		// Python visit_parallel_for_expr delegates to visit_for_expr; the
		// ParallelForExpr node itself carries the loop-variable declaration
		// (DeclarationSymbols[parallel-for] -> loop symbol), matching the
		// ForExpr/ForStmt registration.
		b.visitForExpr(n, n.LoopTypes(), n.LoopSources(), n.LoopVars(), n.Body())
	case *ast.CodeInputBinding:
		if v := n.Value(); v != nil {
			b.walk(v)
		}
	case *ast.CodeDirectOutput, *ast.CodeOutputField:
		// leaf
	case *ast.CodeStructuralOutput:
		for _, f := range n.Fields() {
			b.walk(f)
		}
	case *ast.ForeignCodeExpr:
		b.visitForeignCodeExpr(n)
	default:
		// type refs and unreachable declaration nodes: generic_visit is a
		// no-op in the Python reference.
	}
}

func (b *binder) walkStmts(stmts []ast.Stmt) {
	for _, s := range stmts {
		b.walk(s)
	}
}

// ── entry / function scope setup ──────────────────────────────────────

func (b *binder) walkEntry(entry *ast.FunctionDef) {
	parent := b.chain.Current()
	sid := b.chain.Enter(ScopeEntry, entry.Name())
	b.registerScope(sid, entry, parent)
	b.functionScopes = append(b.functionScopes, sid)
	for _, param := range entry.Params() {
		b.checkNamespaceShadow(param.Name(), param.Span())
		desc := b.typeRefToDescriptor(param.TypeRef())
		sym := NewSymbol(param.Name(), SymbolInput, desc, false, false)
		if existing := b.defineInCurrent(param.Name(), sym); existing != nil {
			b.emit(diag.DUPLICATE_DEF, "Duplicate parameter name: '"+param.Name()+"'", param.Span())
		} else {
			b.registerSymbol(sym, sid, param)
		}
	}
	for _, param := range entry.Params() {
		if param.Default() != nil {
			b.walk(param.Default())
		}
	}
	b.walk(entry.Body())
	b.functionScopes = b.functionScopes[:len(b.functionScopes)-1]
	b.chain.Exit()
}

func (b *binder) walkFunction(fn *ast.FunctionDef) {
	parent := b.chain.Current()
	sid := b.chain.Enter(ScopeFunction, fn.Name())
	b.registerScope(sid, fn, parent)
	b.functionScopes = append(b.functionScopes, sid)
	for _, param := range fn.Params() {
		b.checkNamespaceShadow(param.Name(), param.Span())
		desc := b.typeRefToDescriptor(param.TypeRef())
		sym := NewSymbol(param.Name(), SymbolParameter, desc, false, false)
		if existing := b.defineInCurrent(param.Name(), sym); existing != nil {
			b.emit(diag.DUPLICATE_DEF, "Duplicate parameter name: '"+param.Name()+"'", param.Span())
		} else {
			b.registerSymbol(sym, sid, param)
		}
	}
	for _, param := range fn.Params() {
		if param.Default() != nil {
			b.walk(param.Default())
		}
	}
	b.walk(fn.Body())
	b.functionScopes = b.functionScopes[:len(b.functionScopes)-1]
	b.chain.Exit()
}

// ── statements with scope management ──────────────────────────────────

func (b *binder) visitVarDeclStmt(n *ast.VarDeclStmt) {
	b.checkNamespaceShadow(n.Name(), n.Span())
	desc := types.AnyType()
	if n.TypeRef() != nil {
		desc = b.typeRefToDescriptor(n.TypeRef())
	}
	sym := NewSymbol(n.Name(), SymbolVariable, desc, n.IsMutable(), false)
	existing := b.defineInCurrent(n.Name(), sym)
	if existing != nil {
		b.emit(diag.DUPLICATE_DEF, "Duplicate variable definition: '"+n.Name()+"'", n.Span())
	}
	if existing == nil {
		b.registerSymbol(sym, b.chain.Current(), n)
	}
	if n.Init() != nil {
		b.walk(n.Init())
	}
}

func (b *binder) visitIfStmt(n *ast.IfStmt) {
	b.walk(n.Condition())
	b.visitScopedBlock(n.ThenBody())
	for _, ei := range n.ElseIfs() {
		b.walk(ei)
	}
	if n.ElseBody() != nil {
		b.visitScopedBlock(n.ElseBody())
	}
}

func (b *binder) visitElseIfBranch(n *ast.ElseIfBranch) {
	b.walk(n.Condition())
	b.visitScopedBlock(n.Body())
}

func (b *binder) visitForStmt(n *ast.ForStmt) {
	for _, src := range n.LoopSources() {
		b.walk(src)
	}
	b.visitLoopBlock(n, n.LoopTypes(), n.LoopVars(), n.Body())
}

func (b *binder) visitForExpr(loopNode ast.Node, varTypes []ast.TypeRef, sources []ast.Expr, varNames []string, body *ast.Block) {
	for _, src := range sources {
		if src != nil {
			b.walk(src)
		}
	}
	// The loop node (ForExpr or ParallelForExpr) carries the loop-variable
	// declaration so DeclarationSymbols[loop] resolves for the type pass
	// (type_pass.py scope_pass._visit_loop_block registers node=node).
	b.visitLoopBlock(loopNode, varTypes, varNames, body)
}

func (b *binder) visitWhileStmt(n *ast.WhileStmt) {
	b.walk(n.Condition())
	parent := b.chain.Current()
	sid := b.chain.Enter(ScopeBlock)
	b.registerScope(sid, n.Body(), parent)
	b.loopScopes = append(b.loopScopes, sid)
	b.walkStmts(n.Body().Stmts())
	b.loopScopes = b.loopScopes[:len(b.loopScopes)-1]
	b.chain.Exit()
}

// visitLoopBlock mirrors ScopePass._visit_loop_block: a fresh BLOCK scope
// holding the loop variable, registered against the loop node
// (DeclarationSymbols[loop node] -> loop-variable symbol).
// visitLoopBlock mirrors ScopePass._visit_loop_block: a fresh BLOCK scope
// holding the loop variables (single or zip multi-var), registered against
// the loop node (DeclarationSymbols[loop node] -> first loop-variable
// symbol; zip 其余变量经 scope Decls 按名解析，type_pass zipVarSymbol 见)。
func (b *binder) visitLoopBlock(loopNode ast.Node, varTypes []ast.TypeRef, varNames []string, body *ast.Block) {
	parent := b.chain.Current()
	sid := b.chain.Enter(ScopeBlock)
	b.registerScope(sid, body, parent)
	for i, name := range varNames {
		desc := types.AnyType()
		if i < len(varTypes) && varTypes[i] != nil {
			desc = b.typeRefToDescriptor(varTypes[i])
		}
		b.checkNamespaceShadow(name, loopNode.Span())
		symbol := NewSymbol(name, SymbolVariable, desc, false, false)
		// Python: chain.define ignores the duplicate result and register_symbol
		// mints unconditionally, so a shadowed loop variable still receives a
		// SymbolID (the scope keeps the outer binding).
		b.mintDefineInCurrent(name, symbol)
		b.registerSymbol(symbol, sid, loopNode)
	}
	b.loopScopes = append(b.loopScopes, sid)
	b.walkStmts(body.Stmts())
	b.loopScopes = b.loopScopes[:len(b.loopScopes)-1]
	b.chain.Exit()
}

// branchExport is one (name, symbol) entry of a parallel branch's scope
// snapshot (Python dict(scope.symbols) item).
type branchExport struct {
	name string
	sym  *Symbol
}

func (b *binder) visitParallelStmt(n *ast.ParallelStmt) {
	// branchExports: per-branch ordered symbol snapshots (Python list of
	// dict(scope.symbols)).
	branchExports := [][]branchExport{}
	for _, branch := range n.Branches() {
		parent := b.chain.Current()
		sid := b.chain.Enter(ScopeBlock)
		b.registerScope(sid, branch, parent)
		b.walkStmts(branch.Stmts())
		scope := b.chain.Scope(sid)
		exports := []branchExport{}
		for _, name := range scope.order {
			exports = append(exports, branchExport{name, scope.records[name]})
		}
		branchExports = append(branchExports, exports)
		b.chain.Exit()
	}

	// A branch cannot observe a sibling's declarations. Publish all branch
	// exports only after every branch has been resolved, at the join.
	parentScope := b.chain.Scope(b.chain.Current())
	// Python collects exported names in a dict, so the conflict-report
	// order is insertion order (first appearance across branches in walk
	// order). Iterating a Go map here made E1035 order nondeterministic
	// across Bind runs (R1-Important-2); keep an ordered name slice to
	// mirror the Python dict exactly.
	allNames := map[string][]branchExport{}
	allNameOrder := []string{}
	for _, symbols := range branchExports {
		for _, e := range symbols {
			if parentScope.ResolveSymbol(e.name) != nil {
				continue
			}
			if _, seen := allNames[e.name]; !seen {
				allNameOrder = append(allNameOrder, e.name)
			}
			allNames[e.name] = append(allNames[e.name], e)
		}
	}
	for _, name := range allNameOrder {
		entries := allNames[name]
		if len(entries) < 2 {
			if parentScope.ResolveSymbolLocal(name) == nil {
				parentScope.PublishSymbol(name, entries[0].sym, b.registered[entries[0].sym])
			}
			continue
		}
		// Multiple branches export the same name: check type compatibility
		// (Python E1035 PARALLEL_EXPORT_CONFLICT).
		distinct := distinctDescriptors(entries)
		bases := map[types.DSLType]bool{}
		for _, e := range distinct {
			bases[e.sym.TypeInfo.Kind] = true
		}
		if len(bases) > 1 || (len(distinct) > 1 && !(len(bases) == 1 && bases[types.Any])) {
			typesStr := make([]string, 0, len(distinct))
			for _, e := range distinct {
				typesStr = append(typesStr, describeDescriptor(e.sym.TypeInfo))
			}
			sort.Strings(typesStr)
			b.emit(diag.PARALLEL_EXPORT_CONFLICT,
				"Variable '"+name+"' is exported from multiple parallel branches "+
					"with incompatible types: "+strings.Join(typesStr, ", "), n.Span())
		}
		if parentScope.ResolveSymbolLocal(name) == nil {
			parentScope.PublishSymbol(name, entries[0].sym, b.registered[entries[0].sym])
		}
	}
}

// distinctDescriptors dedupes a set of exported symbols by structural
// descriptor equality (Python builds a set of TypeDescriptor objects).
func distinctDescriptors(entries []branchExport) []branchExport {
	out := []branchExport{}
	for _, e := range entries {
		dup := false
		for _, o := range out {
			if e.sym.TypeInfo.Equal(o.sym.TypeInfo) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, e)
		}
	}
	return out
}

func (b *binder) visitAttemptStmt(n *ast.AttemptStmt) {
	b.walk(n.Operation())
	b.visitAttemptBranch(n.ResultBinding(), n.SuccessBody(), "success")
	b.visitAttemptBranch(n.ErrorBinding(), n.FailureBody(), "failure")
}

func (b *binder) visitAttemptBranch(binding *ast.VarDeclStmt, body *ast.Block, name string) {
	parent := b.chain.Current()
	sid := b.chain.Enter(ScopeBlock, name)
	b.registerScope(sid, body, parent)
	// Python calls visit_var_decl_stmt directly (no bind_scope on the
	// binding node).
	b.visitVarDeclStmt(binding)
	b.walkStmts(body.Stmts())
	b.chain.Exit()
}

func (b *binder) visitRequestInputStmt(n *ast.RequestInputStmt) {
	b.walk(n.Config())
	for _, branch := range n.ActionBranches() {
		b.walk(branch.ActionExpr())
	}
	formType := b.typeRefToDescriptor(n.FormType())
	b.checkNamespaceShadow(n.ResultBinding().Name(), n.ResultBinding().Span())
	resultSymbol := NewSymbol(n.ResultBinding().Name(), SymbolVariable, formType, false, false)
	parent := b.chain.Current()
	if n.ActionType() == nil {
		// **无动作形态**（S1）：臂集合为 ∅ ⇒ 「绑定可见范围 = 具名臂内 ∪ 直通
		// 出路后继」的前一项为空 ⇒ 绑定落**语句所在作用域**，语句之后可读。
		// 同族先例（不是新发明的规则）= switch 结果绑定：`switch … as it` 的
		// 绑定定义在语句所在作用域（`visitSwitchStmt` 同一行代码 + 用户
		// 2026-09-25 批准的 R-71「分支是执行作用域、不是数据作用域」）。
		b.mintDefineInCurrent(n.ResultBinding().Name(), resultSymbol)
		b.registerSymbol(resultSymbol, parent, n.ResultBinding())
		b.b.ScopeBindings[b.mustID(n.ResultBinding())] = parent
		if tb := n.TimeoutBody(); tb != nil {
			b.visitScopedBlock(tb)
		}
		return
	}
	actionSid := b.chain.Enter(ScopeBlock, "human-input-actions")
	b.registerScope(actionSid, nil, parent)
	// Python: action_scope.define then register_symbol unconditionally.
	b.mintDefineInCurrent(n.ResultBinding().Name(), resultSymbol)
	b.registerSymbol(resultSymbol, actionSid, n.ResultBinding())
	b.b.ScopeBindings[b.mustID(n.ResultBinding())] = actionSid
	for _, branch := range n.ActionBranches() {
		b.b.ScopeBindings[b.mustID(branch)] = actionSid
		innerSid := b.chain.Enter(ScopeBlock, "human-input-action")
		b.registerScope(innerSid, branch.Body(), actionSid)
		b.walkStmts(branch.Body().Stmts())
		b.chain.Exit()
	}
	b.chain.Exit()
	if tb := n.TimeoutBody(); tb != nil {
		b.visitScopedBlock(tb)
	}
}

func (b *binder) visitSwitchStmt(n *ast.SwitchStmt) {
	b.walk(n.Query())
	b.walk(n.Config())
	// 结果绑定类型：record{classificationId:int, reason:string}（平台
	// IntentDetector 数据输出）。绑定**定义在 switch 语句所在的作用域**（不是
	// switch 自身的作用域）⇒ 分支体内可访问 it.classificationId / it.reason，
	// 栅栏之后**同样**可访问（2026-09-25 用户批准，R-71：分支是执行作用域、
	// 不是数据作用域，与 P5 parallel「臂内声明在栅栏后可见」同一条语义；
	// 正本 = docs/SWITCH-SYNTAX.md §3）。
	name := n.Result().Name()
	resultType := types.RecordType("", []types.RecordField{
		{Name: "classificationId", Type: types.IntType()},
		{Name: "reason", Type: types.StringType()},
	})
	parent := b.chain.Current()
	if name != "" {
		b.checkNamespaceShadow(name, n.Result().Span())
		resultSymbol := NewSymbol(name, SymbolVariable, resultType, false, false)
		b.mintDefineInCurrent(name, resultSymbol)
		b.registerSymbol(resultSymbol, parent, n.Result())
		b.b.ScopeBindings[b.mustID(n.Result())] = parent
	}
	switchSid := b.chain.Enter(ScopeBlock, "switch")
	b.registerScope(switchSid, nil, parent)
	for _, br := range n.Branches() {
		b.b.ScopeBindings[b.mustID(br)] = switchSid
		innerSid := b.chain.Enter(ScopeBlock, "switch-branch")
		b.registerScope(innerSid, br.Body(), switchSid)
		b.walkStmts(br.Body().Stmts())
		b.chain.Exit()
	}
	if n.DefaultBody() != nil {
		innerSid := b.chain.Enter(ScopeBlock, "switch-default")
		b.registerScope(innerSid, n.DefaultBody(), switchSid)
		b.walkStmts(n.DefaultBody().Stmts())
		b.chain.Exit()
	}
	b.chain.Exit()
}

func (b *binder) visitReturnStmt(n *ast.ReturnStmt) {
	if len(b.functionScopes) > 0 {
		b.bindExit(n, ExitReturn, b.functionScopes[len(b.functionScopes)-1])
	}
	if n.Value() != nil {
		b.walk(n.Value())
	}
}

func (b *binder) visitBreakStmt(n *ast.BreakStmt) {
	if len(b.loopScopes) > 0 {
		b.bindExit(n, ExitBreak, b.loopScopes[len(b.loopScopes)-1])
	}
}

func (b *binder) visitContinueStmt(n *ast.ContinueStmt) {
	if len(b.loopScopes) > 0 {
		b.bindExit(n, ExitContinue, b.loopScopes[len(b.loopScopes)-1])
	}
}

func (b *binder) visitScopedBlock(block *ast.Block) {
	parent := b.chain.Current()
	sid := b.chain.Enter(ScopeBlock)
	b.registerScope(sid, block, parent)
	b.walkStmts(block.Stmts())
	b.chain.Exit()
}

// ── expressions ───────────────────────────────────────────────────────

func (b *binder) visitMapLit(n *ast.MapLit) {
	// Keys are labels, not references: only values are visited.
	for _, en := range n.Entries() {
		b.walk(en.Value)
	}
}

func (b *binder) visitForeignCodeExpr(n *ast.ForeignCodeExpr) {
	for _, in := range n.Inputs() {
		if in.Value() != nil {
			b.walk(in.Value())
		}
	}
	b.walk(n.Source())
}

func (b *binder) visitIdentExpr(n *ast.IdentExpr) {
	sym := b.chain.ResolveSymbol(n.Name())
	if sym == nil {
		b.emit(diag.UNDEFINED_VAR, "Undefined variable: '"+n.Name()+"'", n.Span())
		return
	}
	// NCODA-DOLLAR-ESCAPE-SPEC（$ 转义边界）：$xxx 是工作流/用户标识符，
	// 不是语言内建。escaped 关键字（$file/$int/$output/$code）解析到内建符号
	// = 静默别名漏洞（$file(...) 会当 file() cast 用）；必须硬报错——用户要
	// 内建就写无 $ 的关键字形式 file(...)，要标识符就得有同名的用户声明。
	if n.Escaped() && sym.Kind == SymbolBuiltinFunction {
		b.emit(diag.UNDEFINED_VAR, "'$"+n.Name()+"' is a $-escaped keyword, not a user identifier; "+
			"it cannot name the builtin '"+n.Name()+"' — use '"+n.Name()+"(...)' without '$' for the builtin, "+
			"or declare a user identifier with that name", n.Span())
		return
	}
	b.bindReference(n, sym)
}

func (b *binder) visitCallExpr(n *ast.CallExpr) {
	if b.mode == "workflow" || b.mode == "advanced-chat" {
		if id, ok := n.Func().(*ast.IdentExpr); ok && id.Name() == "datasource" &&
			b.chain.ResolveSymbol("datasource") == nil {
			for _, arg := range n.Args() {
				b.walk(arg)
			}
			b.emit(diag.TARGET_NOT_LOWERABLE,
				"Datasource is valid only for the rag-pipeline target; workflow and "+
					"advanced-chat cannot supply sys.datasource_type/sys.datasource_info or ROOT scheduling",
				n.Span())
			return
		}
	}
	b.walk(n.Func())
	for _, arg := range n.Args() {
		b.walk(arg)
	}
	if path := StdlibCallPath(n); path != "" {
		b.bindStdlibCall(n, path)
		return
	}
	if id, ok := n.Func().(*ast.IdentExpr); ok {
		// expr_symbols: the func ident was resolved during the walk above.
		if symID, found := b.b.ReferenceSymbols[b.mustID(id)]; found {
			b.bindCall(n, b.symbolsByID[symID])
		}
	}
}

// StdlibCallPath mirrors stdlib/registry.py stdlib_call_path: an exact
// public stdlib path std.<version>.<function>, or "" for any other shape.
// Exported for the semantic passes (Task 17A), which reuse the binder's
// path shape to classify std.* calls (semantic_pass.py:196-200, 231-236).
func StdlibCallPath(call *ast.CallExpr) string {
	var path []string
	for e := call.Func(); e != nil; {
		switch n := e.(type) {
		case *ast.IdentExpr:
			path = append([]string{n.Name()}, path...)
			e = nil
		case *ast.FieldAccessExpr:
			path = append([]string{n.FieldName()}, path...)
			e = n.Obj()
		default:
			return ""
		}
	}
	if len(path) == 3 && path[0] == "std" {
		return strings.Join(path, ".")
	}
	return ""
}

func (b *binder) visitLambdaExpr(n *ast.LambdaExpr) {
	parent := b.chain.Current()
	sid := b.chain.Enter(ScopeBlock, "lambda")
	b.registerScope(sid, n, parent)
	for _, name := range n.Params() {
		symbol := NewSymbol(name, SymbolParameter, types.AnyType(), false, false)
		if b.defineInCurrent(name, symbol) == nil {
			b.registerSymbol(symbol, sid, nil)
		}
	}
	if n.Body() != nil {
		b.walk(n.Body())
	}
	b.chain.Exit()
}

// ── binding accumulation (bindings.py BindingBuilder) ─────────────────

func (b *binder) registerScope(sid ScopeID, node ast.Node, parent ScopeID) {
	b.b.Scopes[sid] = ScopeInfo{Parent: parent, Decls: []SymbolID{}}
	if node != nil {
		b.b.ScopeBindings[b.mustID(node)] = sid
	}
}

func (b *binder) registerSymbol(sym *Symbol, scope ScopeID, node ast.Node) SymbolID {
	id, found := b.registered[sym]
	if !found {
		id = b.chain.NewSymbolID()
		b.registered[sym] = id
		b.symbolsByID[id] = sym
	}
	// The declaration record is written exactly once per symbol, whether the
	// ID was minted here or earlier (defineIfFree / mintDefineInCurrent /
	// RegisterBuiltins pre-seeded IDs). Python's register_symbol is
	// idempotent per symbol identity and never re-appends the record.
	if !b.declared[sym] {
		b.declared[sym] = true
		b.b.Declarations[id] = SymbolDecl{Name: sym.Name, Type: sym.TypeInfo, Scope: scope, Kind: sym.Kind, Mutable: sym.IsMutable, NS: sym.NS}
		info := b.b.Scopes[scope]
		info.Decls = append(info.Decls, id)
		b.b.Scopes[scope] = info
	}
	if node != nil {
		b.b.DeclarationSymbols[b.mustID(node)] = id
	}
	return id
}

func (b *binder) registerFunction(node ast.Node, sym *Symbol) {
	fid := b.b.NewFunctionID()
	b.b.Functions[b.mustID(node)] = fid
	b.functionBySymbol[b.registered[sym]] = fid
}

func (b *binder) bindScope(node ast.Node) {
	b.b.ScopeBindings[b.mustID(node)] = b.chain.Current()
}

func (b *binder) bindReference(node ast.Node, sym *Symbol) {
	b.b.ReferenceSymbols[b.mustID(node)] = b.registered[sym]
}

func (b *binder) bindCall(node ast.Node, sym *Symbol) {
	if fid, found := b.functionBySymbol[b.registered[sym]]; found {
		b.b.CallTargets[b.mustID(node)] = FunctionCallTarget(fid)
		return
	}
	b.b.CallTargets[b.mustID(node)] = BuiltinCallTarget(sym.Name)
}

func (b *binder) bindStdlibCall(node ast.Node, qualifiedName string) {
	b.b.CallTargets[b.mustID(node)] = StdlibCallTarget(qualifiedName)
}

func (b *binder) bindExit(node ast.Node, kind ExitKind, scope ScopeID) {
	b.b.ExitTargets[b.mustID(node)] = ExitTarget{Kind: kind, ScopeID: scope}
}

// ── symbol definition helpers ─────────────────────────────────────────

// defineGlobal defines name in the global scope, minting the SymbolID only
// when the name is free (Python defines first, then register_symbol mints —
// the mint order is identical because the two calls are adjacent).
func (b *binder) defineGlobal(name string, sym *Symbol) *Symbol {
	return b.defineIfFree(b.chain.Scope(b.chain.Global()), name, sym)
}

func (b *binder) defineInCurrent(name string, sym *Symbol) *Symbol {
	return b.defineIfFree(b.chain.Scope(b.chain.Current()), name, sym)
}

func (b *binder) defineIfFree(scope *Scope, name string, sym *Symbol) *Symbol {
	if existing := scope.ResolveSymbolLocal(name); existing != nil {
		return existing
	}
	id := b.chain.NewSymbolID()
	b.registered[sym] = id
	b.symbolsByID[id] = sym
	scope.DefineSymbol(name, sym, id)
	return nil
}

// mintDefineInCurrent mirrors Python's define-without-duplicate-check +
// unconditional register_symbol (loop variables, request-input result
// binding): a SymbolID is minted even when the name is already bound.
func (b *binder) mintDefineInCurrent(name string, sym *Symbol) *Symbol {
	id := b.chain.NewSymbolID()
	b.registered[sym] = id
	b.symbolsByID[id] = sym
	return b.chain.Scope(b.chain.Current()).DefineSymbol(name, sym, id)
}

// ── type resolution (types.py type_ref_to_descriptor) ─────────────────

// typeRefToDescriptor resolves a type reference against the binder's named
// types and reports UNKNOWN_TYPE for unknown named types, mirroring the
// Python call with diagnostics=ctx.diagnostics. types.py
// type_ref_to_descriptor (line 513) carries the diagnostics list through the
// array/map/optional recursion, so unknown types nested inside composite
// references are reported at their own span too (R1-Important-1); the
// pre-scan below walks in the same order as the Python recursion.
func (b *binder) typeRefToDescriptor(ref ast.TypeRef) *types.Descriptor {
	b.checkUnknownNamedTypes(ref)
	return types.TypeRefToDescriptor(ref, func(name string) *types.Descriptor {
		return b.namedTypes[name]
	})
}

// checkUnknownNamedTypes walks a type reference in the exact recursion order
// of types.py type_ref_to_descriptor (array element, then map key before
// value, then optional value, then the named ref itself) and emits
// UNKNOWN_TYPE for every named type missing from the binder's named-type
// environment. Unknown names recover to ANY via types.TypeRefToDescriptor,
// matching the Python behavior of returning TypeDescriptor.any().
func (b *binder) checkUnknownNamedTypes(ref ast.TypeRef) {
	switch r := ref.(type) {
	case *ast.ArrayTypeRef:
		b.checkUnknownNamedTypes(r.Element())
	case *ast.MapTypeRef:
		b.checkUnknownNamedTypes(r.Key())
		b.checkUnknownNamedTypes(r.Value())
	case *ast.OptionalTypeRef:
		b.checkUnknownNamedTypes(r.Value())
	case *ast.NamedTypeRef:
		if _, found := b.namedTypes[r.Name()]; !found {
			b.emit(diag.UNKNOWN_TYPE,
				"Unknown type: '"+r.Name()+"'. Declare it with 'type' or use a supported NodeCoda type.",
				r.Span())
		}
	}
}

// typeRefToDescriptorQuiet mirrors the Python call with diagnostics=None:
// unknown named types silently recover to ANY (function signature params).
func (b *binder) typeRefToDescriptorQuiet(ref ast.TypeRef) *types.Descriptor {
	return types.TypeRefToDescriptor(ref, func(name string) *types.Descriptor {
		return b.namedTypes[name]
	})
}

// ── diagnostics (passes/base.py Diagnostic + emit_diagnostic) ─────────

// checkNamespaceShadow emits SHADOWED_NAMESPACE when a variable
// declaration collides with an imported package namespace in the global
// scope (TOOL-DIRECT-CALL-FORM decision 6): the variable would silently
// shadow the namespace, which is a compile error — no silent preference.
// Only variable declarations shadow namespaces (let/var, function
// parameters, loop variables, parallel lets); function, type and enum
// declarations do not — they already collide through the existing
// DUPLICATE_DEF/DUPLICATE_TYPE paths against the global scope.
func (b *binder) checkNamespaceShadow(name string, span ast.Span) {
	if sym := b.chain.Scope(b.chain.Global()).ResolveSymbolLocal(name); sym != nil && sym.Kind == SymbolNamespace {
		b.emit(diag.SHADOWED_NAMESPACE,
			"Name '"+name+"' shadows a namespace; qualified calls ("+name+".op(...)) would be ambiguous", span)
	}
}

func (b *binder) emit(code diag.Code, message string, span ast.Span) {
	d := diag.Diagnostic{Code: code, Severity: diag.Error, Message: message}
	if span != (ast.Span{}) {
		d.SetSpan(token.Span(span))
	}
	b.diags = append(b.diags, d)
}

func (b *binder) mustID(n ast.Node) AstNodeID {
	id, ok := b.b.NodeIDs[n]
	if !ok {
		panic("binding: node was not enumerated: " + nodeKindName(n))
	}
	return id
}

// describeDescriptor renders a descriptor for the parallel-export conflict
// message. The Python message formats str(TypeDescriptor) (a verbose
// dataclass repr); the Go port emits a compact Type(<kind>) form. The exact
// text is not asserted by any ported test (approximation recorded).
func describeDescriptor(d *types.Descriptor) string {
	if d == nil {
		return "Type(any)"
	}
	return "Type(" + d.Kind.String() + ")"
}
