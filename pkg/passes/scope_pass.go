package passes

// ScopePass is the thin wrapper mandated by Task 14 D2: the scope-chain
// traversal, symbol registration, reference resolution and all scope
// diagnostics were fully ported into binding.Bind by Task 10 (35-case
// FULL_MATCH against scope_pass.py). This pass must NOT re-implement any of
// that traversal; it runs Bind, appends the diagnostics, and — only when no
// errors were reported — performs the E1033 recursive-call-cycle check that
// the reference pipeline executes right after ScopePass (base.py:426-433).
// The short-circuit semantics are preserved by the pipeline's
// HasErrors → return contract.
//
// ScopePass is also the default StageScope implementation: the pipeline
// falls back to ScopePass{} when no stage pass is registered (D1), keeping
// the New().Run() frontend contract that the scope phase always runs and
// Bindings is never nil.

import (
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/types"
)

// ScopePass implements the scope stage (base.py ScopePass, scope_pass.py:44).
type ScopePass struct{}

// Run binds program, publishes the snapshot and bind diagnostics, then
// detects recursive call cycles (E1033) when binding was clean.
func (ScopePass) Run(p *ast.Program, ctx *Context) {
	snap, diags := binding.Bind(p)
	ctx.Bindings = snap
	// Task 15 T1: publish the named-type environment before later passes run
	// (Python DSLPassContext.named_types is populated by the scope pass; the
	// Go Snapshot carries it and ScopePass copies it into the Context).
	ctx.NamedTypes = copyNamedTypes(snap.NamedTypes())
	// Task 16 D1: publish the conversation-variable symbol set (Python
	// scope_pass.py:211 maintains ctx.conversation_var_ids directly; the Go
	// binder collects it and the snapshot carries it here).
	ctx.ConversationVarIDs = copyConversationVarIDs(snap.ConversationVarIDs())
	ctx.Diagnostics = append(ctx.Diagnostics, diags...)
	if ctx.HasErrors() {
		return // base.py:424-425 short-circuit; the E1033 check requires a clean bind
	}

	// base.py:426-433: build the user call graph and reject the lexically
	// canonical recursive cycle with one E1033 at the cycle's call site.
	graph, sites := binding.BuildUserCallGraph(p, snap)
	cycle := binding.FindRecursiveCycle(graph, sites)
	if cycle == nil {
		return
	}
	path := recursiveCyclePath(snap, cycle)
	info, found := snap.AstNodes().Get(cycle.CallSite)
	if !found {
		panic("passes: recursive-cycle call site was not enumerated")
	}
	ctx.Emit(diag.RECURSIVE_CALL, diag.Error,
		"Recursive function call cycle is not supported: "+path, info.Span)
}

// recursiveCyclePath renders "a -> b -> a" for one cycle, mirroring the
// base.py:428-437 name resolution: reverse Functions() (AstNodeID→FunctionID),
// then DeclarationSymbols() → Declarations().Name, joining the cycle
// functions plus a repeat of the first name with " -> ".
func recursiveCyclePath(snap *binding.Snapshot, cycle *binding.RecursiveCycle) string {
	nodeByFunction := map[binding.FunctionID]binding.AstNodeID{}
	snap.Functions().Range(func(nodeID binding.AstNodeID, fid binding.FunctionID) bool {
		nodeByFunction[fid] = nodeID
		return true
	})
	nameOf := func(fid binding.FunctionID) string {
		nodeID, ok := nodeByFunction[fid]
		if !ok {
			panic("passes: cycle member has no function binding node")
		}
		symID, ok := snap.DeclarationSymbols().Get(nodeID)
		if !ok {
			panic("passes: cycle member has no declaration symbol")
		}
		decl, ok := snap.Declarations().Get(symID)
		if !ok {
			panic("passes: cycle member declaration symbol missing")
		}
		return decl.Name
	}
	names := make([]string, 0, len(cycle.Functions)+1)
	for _, fid := range cycle.Functions {
		names = append(names, nameOf(fid))
	}
	names = append(names, names[0]) // close the cycle: a -> b -> a
	return strings.Join(names, " -> ")
}

// copyNamedTypes copies the snapshot's named-type table into a plain map
// (Python DSLPassContext.named_types is a mutable dict; the snapshot side is
// the read-only MappingProxyType).
func copyNamedTypes(types_ *binding.RoMap[string, *types.Descriptor]) map[string]*types.Descriptor {
	out := make(map[string]*types.Descriptor, types_.Len())
	types_.Range(func(name string, d *types.Descriptor) bool {
		out[name] = d
		return true
	})
	return out
}

// copyConversationVarIDs copies the snapshot's conversation-variable symbol
// set into a plain map (Python DSLPassContext.conversation_var_ids is a
// mutable set).
func copyConversationVarIDs(ids *binding.RoMap[binding.SymbolID, bool]) map[binding.SymbolID]bool {
	out := make(map[binding.SymbolID]bool, ids.Len())
	ids.Range(func(id binding.SymbolID, _ bool) bool {
		out[id] = true
		return true
	})
	return out
}
