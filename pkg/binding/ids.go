// Package binding defines the Phase 1 semantic identity contracts for the
// NodeCoda DSL frontend. It is a direct port of the immutable identity
// dataclasses and BindingSnapshot in src/nclang/lang/bindings.py and the
// scope chain in src/nclang/lang/scope.py (read-only oracles).
//
// Layering: pkg/ast (SourceAST) is consumed for node identity only;
// pkg/types (Descriptor) is consumed for declared types. The package
// owns scope/symbol/function identity and the read-only snapshot surface;
// the binding passes (binder/scope pass) arrive in later tasks and
// accumulate into binding.Builder before publishing a Snapshot.
//
// Immutability strategy (DESIGN D6): the Snapshot does not export raw map
// fields. Accessors return a RoMap (read-only view, Set panics, mirroring
// Python MappingProxyType), and Publish defensively copies every map so no
// mutation path survives publication. Value structs (SymbolDecl, ScopeInfo,
// ...) follow the pkg/types convention: exported fields, treated as
// immutable after construction.
package binding

import "github.com/nodecoda/ncoda/pkg/ast"

// ScopeID identifies one scope in the scope tree, mirroring the frozen
// ScopeId dataclass in bindings.py. IDs are assigned from a per-builder
// counter starting at 1 (Python BindingBuilder._next_scope_ordinal), so the
// sequence is deterministic per compile. The zero value (0) is never a
// valid ID and is used as the "no parent / none" sentinel.
type ScopeID int

// SymbolID identifies one declared symbol, mirroring the frozen SymbolId
// dataclass. Counters start at 1 per builder (Python
// BindingBuilder._next_symbol_ordinal).
type SymbolID int

// FunctionID identifies one function binding, mirroring the frozen
// FunctionId dataclass. Counters start at 1 per builder (Python
// BindingBuilder._next_function_ordinal).
type FunctionID int

// AstNodeID identifies one semantic node in the binding layer, mirroring the
// frozen AstNodeId dataclass. Python assigns these as 1-based traversal
// ordinals distinct from the AST node's own ProcNode identity; the Go port
// keeps the same separation from ast.NodeID (which is a process-global
// construction counter and therefore not deterministic across compiles).
type AstNodeID int

// FromAST bridges an ast.NodeID into the binding layer's AstNodeID space.
// The binder task decides whether to reuse the AST counter directly or
// re-enumerate nodes in traversal order (Python AstNodeId semantics); the
// conversion is lossless either way.
func FromAST(id ast.NodeID) AstNodeID { return AstNodeID(id) }

// AST converts the binding-layer node ID back to the ast.NodeID space.
func (id AstNodeID) AST() ast.NodeID { return ast.NodeID(id) }
