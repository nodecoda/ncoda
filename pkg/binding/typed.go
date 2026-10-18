package binding

// TypeBind: publication of the total type table, aggregate-literal facts,
// entry resolution and BoundFunction records, ported from the tail of
// binder.py type_bound_program (binder.py:641-694) plus
// _build_aggregate_literal_facts (binder.py:694+). The skeleton fields
// (CodeFunctions, StdlibCalls) stay empty; code expressions, stdlib calls,
// call graph and effect facts are owned by later tasks.
//
// Determinism: node_types and aggregate facts are keyed by the snapshot's
// AstNodeID space (per-compile traversal ordinals), so two runs over the
// same logical input mint identical tables; map consumers sort when order
// matters (RoMap convention).

import (
	"fmt"
	"sort"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/types"
)

// TypeBind publishes the typed bound program for one compiled program,
// mirroring binder.py type_bound_program's signature shape. exprTypes is
// the per-node inferred-type table keyed in the snapshot's AstNodeID space
// (the Go snapshot's ast_nodes / expr_types keys are AstNodeID — see the
// task-11 report for the deviation from the brief's ast.NodeID spelling);
// nil exprTypes is valid and means "nothing inferred". requireAggregates
// mirrors require_complete_aggregate_facts: when true, every MapLit in the
// program must publish an aggregate fact or TypeBind panics with the
// RuntimeError analogue.
//
// TypeBind is the convenience form without the Task 15 overlays (varTypes /
// enumMembers) and the Task 25b structural code roots (nil); TypeBindFull
// carries them. Existing callers (typed_test.go) keep the four-argument
// shape.
func TypeBind(program *ast.Program, snap *Snapshot, exprTypes map[AstNodeID]*types.Descriptor, requireAggregates bool) *TypedBoundProgram {
	return TypeBindFull(program, snap, exprTypes, nil, nil, requireAggregates, nil)
}

// TypeBindFull publishes the typed bound program with the Task 15 type-pass
// overlays:
//
//   - varTypes (Context.VarTypes, T2): the symbol-type mutation surface the
//     type pass writes (Python sym.type_info = ...). Bound-function
//     parameter assembly prefers it over the snapshot's frozen declaration
//     types, preserving the Python-observable semantics that a var/param
//     symbol's final type is the type pass's inferred type;
//   - enumMembers (Context.EnumMembers, T3): the enum-member facts the type
//     pass publishes from enum field accesses. They merge into the typed
//     program's EnumMembers publication (the Go analogue of Python
//     BindingSnapshot.enum_members, which publish_bindings reads from
//     ctx.enum_members after the pass loop).
//
// structuralCodeRoots is the pass-final structural-code-root table
// (passes.Context.StructuralCodeRoots; Python bindings.py _build_code_
// signatures reads the same table at publish time). The Go snapshot is
// published before the pass loop, so this is the post-pass publication point
// for code_signatures: TypeBindFull builds them, publishes them onto the
// snapshot (SetCodeSignatures), and then assembles CodeExpressions and
// StdlibCalls, which both read the signatures (roots injection decision
// recorded in the task-25b report: option (a) — extend the signature).
func TypeBindFull(program *ast.Program, snap *Snapshot, exprTypes map[AstNodeID]*types.Descriptor, varTypes map[SymbolID]*types.Descriptor, enumMembers map[AstNodeID]EnumMemberFact, requireAggregates bool, structuralCodeRoots map[AstNodeID]bool) *TypedBoundProgram {
	// node_types: total table over every ast node, Any for uninferred nodes
	// (Python: freeze_type(expression_types.get(node_id, Any))).
	nodeTypes := make(map[AstNodeID]*types.Descriptor, snap.AstNodes().Len())
	snap.AstNodes().Range(func(id AstNodeID, _ NodeInfo) bool {
		if inferred, ok := exprTypes[id]; ok {
			nodeTypes[id] = inferred
		} else {
			nodeTypes[id] = types.AnyType()
		}
		return true
	})

	aggregateFacts := buildAggregateFacts(snap, nodeTypes)
	if requireAggregates {
		requireCompleteAggregateFacts(snap, aggregateFacts)
	}

	entryFunction := resolveEntryFunction(program, snap)
	functions := buildBoundFunctions(program, snap, varTypes)
	codeFunctions := buildBoundCodeFunctions(program, snap)
	publishedEnumMembers := mergeEnumMembers(snap, enumMembers)

	// Task 25b: build code signatures FIRST and publish them onto the
	// snapshot (Python build_bindings.py _build_code_signatures, read by
	// both BuildCodeExpressions and BuildStdlibCalls — the id minting in
	// newStdlibIDMint walks CodeSignatures to stay above the code value ids).
	codeSignatures := BuildCodeSignatures(program, snap, structuralCodeRoots)
	snap.SetCodeSignatures(codeSignatures)
	codeExpressions := BuildCodeExpressions(program, snap)

	// Task 24b: publish the bound stdlib calls (binder.py _build_stdlib_calls,
	// :195-260) so lowering can dispatch validated stdlib calls to their
	// expansion plans. Runs after SetCodeSignatures so code-root arguments
	// resolve to their ValueDefIds (binder.py:197,430-443).
	stdlibCalls := BuildStdlibCalls(program, snap)

	return &TypedBoundProgram{
		Program:         program,
		Snapshot:        snap,
		NodeTypes:       nodeTypes,
		EntryFunction:   entryFunction,
		Functions:       functions,
		CodeFunctions:   codeFunctions,
		AggregateFacts:  aggregateFacts,
		StdlibCalls:     stdlibCalls,
		CodeExpressions: codeExpressions,
		EnumMembers:     publishedEnumMembers,
	}
}

// mergeEnumMembers merges the Task 15 type-pass enum-member facts over the
// snapshot's published table (empty today — the binder never writes
// enumFacts; the merge keeps the publication future-proof). The result is a
// fresh map per call, so no caller-shared mutable state reaches the typed
// program (DESIGN D6 convention).
func mergeEnumMembers(snap *Snapshot, enumMembers map[AstNodeID]EnumMemberFact) map[AstNodeID]EnumMemberFact {
	out := make(map[AstNodeID]EnumMemberFact, snap.EnumFacts().Len()+len(enumMembers))
	snap.EnumFacts().Range(func(id AstNodeID, fact EnumMemberFact) bool {
		out[id] = fact
		return true
	})
	for id, fact := range enumMembers {
		out[id] = fact
	}
	return out
}

// astIDOf resolves the AstNodeID the binder assigned to the AST node n via
// the snapshot's published enumeration (Snapshot.NodeIDOf). The lookup is
// exact — including zero-span nodes such as ParamDecls — so it is fully
// deterministic, unlike a (kind, span) re-match over ast_nodes. Nodes that
// never entered the enumeration are not found; callers treat a miss as "no
// binding".
func astIDOf(snap *Snapshot, n ast.Node) (AstNodeID, bool) {
	if n == nil {
		return 0, false
	}
	return snap.NodeIDOf(n)
}

// buildAggregateFacts mirrors _build_aggregate_literal_facts: every MapLit
// whose inferred type carries a map or record shape publishes an
// AggregateFact; literals typed Any (or otherwise unshaped) publish nothing.
func buildAggregateFacts(snap *Snapshot, nodeTypes map[AstNodeID]*types.Descriptor) map[AstNodeID]AggregateFact {
	facts := make(map[AstNodeID]AggregateFact)
	snap.AstNodes().Range(func(id AstNodeID, info NodeInfo) bool {
		if info.Kind != "MapLit" {
			return true
		}
		d := nodeTypes[id]
		switch {
		case d.Kind == types.Map:
			facts[id] = NewAggregateFact(AggregateLiteralKindMap, d)
		case d.Kind == types.Record:
			facts[id] = NewAggregateFact(AggregateLiteralKindRecord, d)
		}
		return true
	})
	return facts
}

// requireCompleteAggregateFacts mirrors the reference guard: every MapLit
// must publish a fact, otherwise the publication is incomplete. Python
// raises RuntimeError; the Go form is a panic with the same message shape
// (missing/extra sorted ID lists). "extra" can only arise from future
// passes that fabricate facts for non-MapLit nodes; the check is
// symmetrical so those regressions are caught too.
func requireCompleteAggregateFacts(snap *Snapshot, facts map[AstNodeID]AggregateFact) {
	expected := map[AstNodeID]bool{}
	snap.AstNodes().Range(func(id AstNodeID, info NodeInfo) bool {
		if info.Kind == "MapLit" {
			expected[id] = true
		}
		return true
	})
	missing := []int{}
	extra := []int{}
	for id := range expected {
		if _, ok := facts[id]; !ok {
			missing = append(missing, int(id))
		}
	}
	for id := range facts {
		if !expected[id] {
			extra = append(extra, int(id))
		}
	}
	if len(missing) != 0 || len(extra) != 0 {
		sort.Ints(missing)
		sort.Ints(extra)
		panic(fmt.Sprintf("binding: aggregate literal facts are incomplete: missing=%v extra=%v", missing, extra))
	}
}

// resolveEntryFunction mirrors BoundProgramBuilder.publish: the entry
// FunctionDef's AstNodeID resolves through the snapshot's function_bindings
// to its FunctionID. The zero FunctionID (0) is the "none" sentinel, the
// Go form of the reference's entry_function: None.
func resolveEntryFunction(program *ast.Program, snap *Snapshot) FunctionID {
	if program == nil || program.Entry() == nil {
		return 0
	}
	eid, found := astIDOf(snap, program.Entry())
	if !found {
		return 0
	}
	fid, found := snap.Functions().Get(eid)
	if !found {
		return 0
	}
	return fid
}

// buildBoundFunctions assembles one BoundFunction per snapshot function
// binding from the snapshot's declarations and scope bindings:
//   - name/return type from the function symbol's declaration (a Function
//     descriptor carrying the resolved params and result);
//   - params from each ParamDecl node's declaration (name, resolved type,
//     symbol ID), in source order;
//   - scope from the function node's scope binding (the function scope);
//   - body from the AST node.
//
// The reference only publishes FunctionId -> AstNodeId; the richer record is
// the task-11 brief contract. varTypes (Task 15 T2) overlays the frozen
// declaration types for symbols the type pass re-typed; nil means no overlay.
func buildBoundFunctions(program *ast.Program, snap *Snapshot, varTypes map[SymbolID]*types.Descriptor) map[FunctionID]*BoundFunction {
	byID := map[FunctionID]*BoundFunction{}
	for _, fn := range programFunctions(program) {
		fid, ok := functionIDOf(snap, fn)
		if !ok {
			continue // not registered (duplicate name or unbound node)
		}
		if _, dup := byID[fid]; dup {
			continue // CodeFunctionDef + FunctionDef can share a name only via duplicate; skip
		}
		byID[fid] = assembleBoundFunction(fid, fn, snap, varTypes)
	}
	return byID
}

// functionIDOf resolves a function's FunctionID from the snapshot: the
// FunctionDef node's AstNodeID maps through function_bindings.
func functionIDOf(snap *Snapshot, fn *ast.FunctionDef) (FunctionID, bool) {
	nid, found := astIDOf(snap, fn)
	if !found {
		return 0, false
	}
	fid, found := snap.Functions().Get(nid)
	return fid, found
}

// assembleBoundFunction builds the record for one function definition.
func assembleBoundFunction(fid FunctionID, fn *ast.FunctionDef, snap *Snapshot, varTypes map[SymbolID]*types.Descriptor) *BoundFunction {
	bf := &BoundFunction{ID: fid, Name: fn.Name(), Body: fn.Body()}
	// The FunctionDef span backs the function-level TIE Locations exactly
	// like BoundInlineFunction.span (typed_lowerer.py:117).
	if span := fn.Span(); span != (ast.Span{}) {
		bf.Span = &span
	}

	// Function symbol declaration (global scope) carries the resolved
	// Function descriptor with params + result.
	if sid, ok := snap.DeclarationSymbols().Get(mustFoundID(snap, fn, "function")); ok {
		if decl, ok := snap.Declarations().Get(sid); ok && decl.Type != nil {
			if decl.Type.Kind == types.Function {
				bf.Return = decl.Type.Result
			}
		}
	}

	// Params: each ParamDecl node's declaration gives name, resolved type
	// and symbol ID, in source order. The collected SymbolIDs are published
	// as ParameterSymbols (typed_lowerer.py:108-111) for the function
	// inliner's BoundValues registration.
	for _, p := range fn.Params() {
		pid, found := astIDOf(snap, p)
		if !found {
			continue
		}
		sid, found := snap.DeclarationSymbols().Get(pid)
		if !found {
			continue
		}
		decl, found := snap.Declarations().Get(sid)
		if !found {
			continue
		}
		paramType := decl.Type
		if varTypes != nil {
			if inferred, ok := varTypes[sid]; ok {
				paramType = inferred
			}
		}
		bf.Params = append(bf.Params, BoundParameter{
			Name:     p.Name(),
			TypeInfo: paramType,
			SymbolID: sid,
		})
		bf.ParameterSymbols = append(bf.ParameterSymbols, sid)
	}

	// Function scope: the FunctionDef node is bound to the scope entered
	// for its body (registerScope), so its scope binding is the function
	// scope; the function symbol's declaration scope is global instead.
	if sid, ok := snap.ScopeBindings().Get(mustFoundID(snap, fn, "function")); ok {
		bf.Scope = sid
	}
	return bf
}

// programFunctions lists every function definition in source order:
// code functions, regular functions, then the entry (mirroring the binder's
// signature-scan order; the reference walks functions in source order).
func programFunctions(program *ast.Program) []*ast.FunctionDef {
	var out []*ast.FunctionDef
	if program == nil {
		return out
	}
	for _, f := range program.CodeFunctions() {
		out = append(out, &f.FunctionDef)
	}
	out = append(out, program.Functions()...)
	if e := program.Entry(); e != nil {
		out = append(out, e)
	}
	return out
}

// mustFoundID resolves n's AstNodeID or panics: the binder enumerates every
// parsed node, so a miss means the node was never part of this program's
// enumeration (programmer error), consistent with the binder's mustID.
func mustFoundID(snap *Snapshot, n ast.Node, what string) AstNodeID {
	id, found := astIDOf(snap, n)
	if !found {
		panic("binding: " + what + " node was not enumerated: " + nodeKindName(n))
	}
	return id
}

// buildBoundCodeFunctions mirrors binder.py _build_code_function_bodies
// (:103-115): every CodeFunctionDef with a registered FunctionID and a
// non-nil body publishes one BoundCodeFunction. Python additionally derives
// code_function_ids from the binding snapshot (bindings.py:277-281:
// function_bindings whose node kind is CodeFunctionDef); iterating
// program.CodeFunctions() covers exactly those nodes, and the FunctionID
// registration check mirrors `function_id in code_function_ids`.
func buildBoundCodeFunctions(program *ast.Program, snap *Snapshot) map[FunctionID]*BoundCodeFunction {
	result := map[FunctionID]*BoundCodeFunction{}
	if program == nil || snap == nil {
		return result
	}
	for _, def := range program.CodeFunctions() {
		fid, found := functionIDOf(snap, &def.FunctionDef)
		if !found {
			continue
		}
		body := def.Body()
		if body == nil {
			continue
		}
		// ParameterSymbols: each ParamDecl node's declaration SymbolID in
		// source order, mirroring the parameter_symbols derivation in
		// typed_lowerer.py:_function_records (:108-111) that Python's
		// BoundInlineFunction carries for _inline_code_function. Python
		// indexes declaration_symbols directly — a miss raises KeyError
		// (crash face); Go surfaces the same invariant as a panic instead
		// of silently dropping the parameter (mustFoundID precedent).
		var parameterSymbols []SymbolID
		for _, p := range def.Params() {
			pid := mustFoundID(snap, p, "code function parameter")
			sid, found := snap.DeclarationSymbols().Get(pid)
			if !found {
				panic(fmt.Sprintf("binding: code function parameter %q has no declaration symbol", p.Name()))
			}
			parameterSymbols = append(parameterSymbols, sid)
		}
		result[fid] = &BoundCodeFunction{ID: fid, Body: body, ParameterSymbols: parameterSymbols}
	}
	return result
}
