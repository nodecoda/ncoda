package binding

import "fmt"

// Scope chain and scope tree, ported from src/nclang/lang/scope.py (113
// lines). Supports GLOBAL -> FUNCTION -> BLOCK (and ENTRY) nesting.
//
// The Python oracle stores plain DSLScope objects in a stack and mints
// ScopeId/SymbolId inside BindingBuilder; the Go port folds the identity
// counters into the Chain (which is what passes actually drive) while
// keeping the Scope node structure identical to DSLScope. Scope IDs are
// deterministic per Chain (start at 1, global is always 1), which is the
// observable "ID counter semantics" the reference publishes.

// ScopeKind enumerates the scope chain levels, mirroring DSLScopeKind.
type ScopeKind int

const (
	// ScopeGlobal is the top level: type declarations, function names.
	ScopeGlobal ScopeKind = iota
	// ScopeFunction is a function body: parameters + local variables.
	ScopeFunction
	// ScopeBlock is a block scope: if/for/while body.
	ScopeBlock
	// ScopeEntry is an entry block: inputs + local variables.
	ScopeEntry
)

// String returns the Python enum name (DSLScopeKind.X.name).
func (k ScopeKind) String() string {
	switch k {
	case ScopeGlobal:
		return "GLOBAL"
	case ScopeFunction:
		return "FUNCTION"
	case ScopeBlock:
		return "BLOCK"
	case ScopeEntry:
		return "ENTRY"
	}
	return "UNKNOWN"
}

// Scope is a single node in the scope tree, mirroring DSLScope. The name
// binding is stored twice, mirroring the two sides of the Python oracle:
// symbols maps names to the minted SymbolID (the identity the binding layer
// publishes) and records maps names to the full Symbol record
// (symbol_table.py DSLScope.symbols: dict[str, Symbol], which the binder
// task populates). Both maps are written together by DefineSymbol /
// PublishSymbol, so they never diverge on a binder path.
type Scope struct {
	kind     ScopeKind
	name     string
	parent   *Scope
	children []*Scope
	symbols  map[string]SymbolID
	records  map[string]*Symbol
	order    []string // insertion order of names (Python dict order)
}

// NewScope builds a scope node. parent may be nil (global).
func NewScope(kind ScopeKind, name string, parent *Scope) *Scope {
	return &Scope{kind: kind, name: name, parent: parent, symbols: map[string]SymbolID{}, records: map[string]*Symbol{}}
}

// Kind reports the scope level.
func (s *Scope) Kind() ScopeKind { return s.kind }

// Name reports the scope name ("" when unnamed, e.g. plain blocks).
func (s *Scope) Name() string { return s.name }

// Parent reports the enclosing scope, or nil for the global scope.
func (s *Scope) Parent() *Scope { return s.parent }

// Children reports the scopes pushed directly inside this one, in creation
// order (Python DSLScope.children). The returned slice is a copy; the tree
// remains owned by the chain.
func (s *Scope) Children() []*Scope {
	out := make([]*Scope, len(s.children))
	copy(out, s.children)
	return out
}

// Define binds sym to name in this scope. If name is already bound, the
// existing symbol is returned with ok=true and sym is discarded (Python
// DSLScope.define returns the existing symbol on duplicate).
func (s *Scope) Define(name string, sym SymbolID) (existing SymbolID, ok bool) {
	if prev, found := s.symbols[name]; found {
		return prev, true
	}
	s.symbols[name] = sym
	return 0, false
}

// Resolve looks up name in this scope, then walks up the parent chain
// (Python DSLScope.resolve).
func (s *Scope) Resolve(name string) (SymbolID, bool) {
	if sym, found := s.symbols[name]; found {
		return sym, true
	}
	if s.parent != nil {
		return s.parent.Resolve(name)
	}
	return 0, false
}

// ResolveLocal looks up name in this scope only, with no parent walk
// (Python DSLScope.resolve_local).
func (s *Scope) ResolveLocal(name string) (SymbolID, bool) {
	sym, found := s.symbols[name]
	return sym, found
}

// DefineSymbol binds sym (with its already-minted id) to name in this scope,
// mirroring DSLScope.define: if name is already bound, the existing symbol is
// returned and nothing is overwritten (Python DSLScope.define returns the
// existing symbol on duplicate). The caller mints the SymbolID via the chain
// so ID order stays observable (Python mints inside BindingBuilder.
// register_symbol; the binder interleaves define+mint identically).
func (s *Scope) DefineSymbol(name string, sym *Symbol, id SymbolID) *Symbol {
	if existing, found := s.records[name]; found {
		return existing
	}
	s.symbols[name] = id
	s.records[name] = sym
	s.order = append(s.order, name)
	return nil
}

// ResolveSymbol looks up the Symbol record for name in this scope, then
// walks up the parent chain (Python DSLScope.resolve).
func (s *Scope) ResolveSymbol(name string) *Symbol {
	if sym, found := s.records[name]; found {
		return sym
	}
	if s.parent != nil {
		return s.parent.ResolveSymbol(name)
	}
	return nil
}

// ResolveSymbolLocal looks up the Symbol record for name in this scope only
// (Python DSLScope.resolve_local).
func (s *Scope) ResolveSymbolLocal(name string) *Symbol {
	return s.records[name]
}

// PublishSymbol binds an existing registered symbol into this scope without
// minting a new ID, mirroring the direct dict assignment
// `parent_scope.symbols[name] = sym` in the scope pass's parallel-branch
// export join. If name is already bound locally it is a no-op.
func (s *Scope) PublishSymbol(name string, sym *Symbol, id SymbolID) {
	if _, found := s.records[name]; found {
		return
	}
	s.symbols[name] = id
	s.records[name] = sym
	s.order = append(s.order, name)
}

// SymbolsInOrder returns the Symbol records of this scope in insertion
// order (Python dict .values() iteration order on DSLScope.symbols).
func (s *Scope) SymbolsInOrder() []*Symbol {
	out := make([]*Symbol, 0, len(s.order))
	for _, name := range s.order {
		out = append(out, s.records[name])
	}
	return out
}

// Chain manages scope nesting during semantic analysis, mirroring
// DSLScopeChain with ScopeID-backed identity. A fresh Chain starts at depth
// 1 with the global scope (ID 1); Enter pushes, Exit pops back to the
// previous depth, and Parent queries the recorded scope tree.
type Chain struct {
	nextScope  ScopeID
	nextSymbol SymbolID
	global     ScopeID
	stack      []ScopeID
	scopes     map[ScopeID]*Scope
	ids        map[*Scope]ScopeID
}

// NewChain builds a chain whose global scope is already entered
// (Python DSLScopeChain.__init__). The global scope always has ID 1.
func NewChain() *Chain {
	c := &Chain{
		nextScope:  2, // global consumes ScopeId(1) at construction (Python register_scope), so Enter starts at 2
		nextSymbol: 1,
		global:     1,
		scopes:     map[ScopeID]*Scope{},
		ids:        map[*Scope]ScopeID{},
	}
	global := NewScope(ScopeGlobal, "global", nil)
	c.scopes[c.global] = global
	c.ids[global] = c.global
	c.stack = []ScopeID{c.global}
	return c
}

// Enter creates and enters a new scope with the given kind under the current
// scope, returning its fresh ID (Python DSLScopeChain.push). name is
// optional and defaults to "". Passing more than one name is a programmer
// error and panics (extra arguments would otherwise be silently ignored).
func (c *Chain) Enter(kind ScopeKind, name ...string) ScopeID {
	if len(name) > 1 {
		panic("binding: Enter accepts at most one scope name")
	}
	n := ""
	if len(name) == 1 {
		n = name[0]
	}
	current := c.scopes[c.Current()]
	scope := NewScope(kind, n, current)
	id := c.nextScope
	c.nextScope++
	c.scopes[id] = scope
	c.ids[scope] = id
	current.children = append(current.children, scope)
	c.stack = append(c.stack, id)
	return id
}

// Exit pops the current scope, restoring the previous depth. Popping the
// global scope panics, mirroring Python DSLScopeChain.pop's RuntimeError
// ("Cannot pop global scope").
func (c *Chain) Exit() {
	if len(c.stack) <= 1 {
		panic("binding: cannot exit global scope")
	}
	c.stack = c.stack[:len(c.stack)-1]
}

// Parent reports the enclosing scope of id. The global scope has no parent:
// ok is false and the returned ID is the zero sentinel (Python
// scope_parents[global] is None). An id that was never registered by this
// chain is a programmer error and panics, mirroring the Python dict-lookup
// KeyError on an unknown scope id.
func (c *Chain) Parent(id ScopeID) (ScopeID, bool) {
	scope, found := c.scopes[id]
	if !found {
		panic("binding: Parent: unknown scope id " + fmt.Sprint(id))
	}
	if scope.parent == nil {
		return 0, false
	}
	return c.ids[scope.parent], true
}

// Current reports the innermost entered scope.
func (c *Chain) Current() ScopeID { return c.stack[len(c.stack)-1] }

// Global reports the chain's global scope (always ScopeID 1).
func (c *Chain) Global() ScopeID { return c.global }

// Depth reports the number of scopes currently entered (>= 1).
func (c *Chain) Depth() int { return len(c.stack) }

// Scope returns the scope node for id, or nil when id is not registered.
func (c *Chain) Scope(id ScopeID) *Scope { return c.scopes[id] }

// Define binds sym to name in the current scope (Python DSLScopeChain.define).
// Returns the pre-existing binding if name is a duplicate.
func (c *Chain) Define(name string, sym SymbolID) (existing SymbolID, ok bool) {
	return c.Scope(c.Current()).Define(name, sym)
}

// Resolve looks up name starting at the current scope and walking parents
// (Python DSLScopeChain.resolve).
func (c *Chain) Resolve(name string) (SymbolID, bool) {
	return c.Scope(c.Current()).Resolve(name)
}

// ResolveLocal looks up name in the current scope only.
func (c *Chain) ResolveLocal(name string) (SymbolID, bool) {
	return c.Scope(c.Current()).ResolveLocal(name)
}

// ResolveSymbol looks up the Symbol record for name starting at the current
// scope and walking parents (Python DSLScopeChain.resolve, which returns the
// Symbol object).
func (c *Chain) ResolveSymbol(name string) *Symbol {
	return c.Scope(c.Current()).ResolveSymbol(name)
}

// NewSymbolID mints the next symbol ID (1-based, deterministic per chain),
// mirroring the per-builder symbol counter in Python BindingBuilder.
func (c *Chain) NewSymbolID() SymbolID {
	id := c.nextSymbol
	c.nextSymbol++
	return id
}
