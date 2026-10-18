package binding

// Ports of the Python reference scope-chain suite
// (lang/tests/lang/unit/test_dsl_scope.py) plus self-built chain tests for
// parent-pointer correctness across nesting and siblings. Group and case
// names mirror the Python classes so parity is auditable.
//
// Deferred (not this layer): TestDSLBuiltinRegistry and TestSymbol in
// test_dsl_symbol_table.py belong to the symbol-table/binding-pass task
// (Symbol struct and builtin registry are not part of this task's files);
// see task-9 report.

import "testing"

func assertPanics(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("expected panic, got none")
		}
	}()
	fn()
}

// ── TestDSLScope (test_dsl_scope.py) ──

func TestScopeCreateGlobal(t *testing.T) {
	scope := NewScope(ScopeGlobal, "global", nil)
	if scope.Kind() != ScopeGlobal {
		t.Errorf("kind = %v, want GLOBAL", scope.Kind())
	}
	if scope.Name() != "global" {
		t.Errorf("name = %q, want %q", scope.Name(), "global")
	}
	if scope.Parent() != nil {
		t.Errorf("parent = %v, want nil", scope.Parent())
	}
}

func TestScopeDefineAndResolve(t *testing.T) {
	scope := NewScope(ScopeGlobal, "", nil)
	sym := SymbolID(7)
	scope.Define("x", sym)
	if got, ok := scope.Resolve("x"); !ok || got != sym {
		t.Errorf("resolve(x) = (%v, %v), want (%v, true)", got, ok, sym)
	}
}

func TestScopeResolveNotFound(t *testing.T) {
	scope := NewScope(ScopeGlobal, "", nil)
	if got, ok := scope.Resolve("missing"); ok || got != 0 {
		t.Errorf("resolve(missing) = (%v, %v), want (0, false)", got, ok)
	}
}

func TestScopeDuplicateReturnsExisting(t *testing.T) {
	scope := NewScope(ScopeGlobal, "", nil)
	sym1, sym2 := SymbolID(1), SymbolID(2)
	scope.Define("x", sym1)
	existing, dup := scope.Define("x", sym2)
	if dup != true || existing != sym1 {
		t.Errorf("duplicate define returned (%v, dup=%v), want (%v, dup=true)", existing, dup, sym1)
	}
	if got, ok := scope.Resolve("x"); !ok || got != sym1 {
		t.Errorf("resolve(x) = (%v, %v), want (%v, true)", got, ok, sym1)
	}
}

func TestScopeResolveLocal(t *testing.T) {
	scope := NewScope(ScopeGlobal, "", nil)
	if got, ok := scope.ResolveLocal("x"); ok || got != 0 {
		t.Errorf("resolve_local(x) = (%v, %v), want (0, false)", got, ok)
	}
	sym := SymbolID(9)
	scope.Define("x", sym)
	if got, ok := scope.ResolveLocal("x"); !ok || got != sym {
		t.Errorf("resolve_local(x) = (%v, %v), want (%v, true)", got, ok, sym)
	}
}

// ── TestDSLScopeChain (test_dsl_scope.py) ──

func TestChainInitialGlobal(t *testing.T) {
	chain := NewChain()
	if got := chain.Current(); got != chain.Global() {
		t.Errorf("current = %v, want global %v", got, chain.Global())
	}
	if chain.Depth() != 1 {
		t.Errorf("depth = %d, want 1", chain.Depth())
	}
	if chain.Scope(chain.Global()).Kind() != ScopeGlobal {
		t.Errorf("global kind = %v, want GLOBAL", chain.Scope(chain.Global()).Kind())
	}
}

func TestChainPushAndExit(t *testing.T) {
	chain := NewChain()
	chain.Enter(ScopeFunction, "myFunc")
	if chain.Scope(chain.Current()).Kind() != ScopeFunction {
		t.Errorf("current kind = %v, want FUNCTION", chain.Scope(chain.Current()).Kind())
	}
	if chain.Depth() != 2 {
		t.Errorf("depth = %d, want 2", chain.Depth())
	}
	chain.Exit()
	if chain.Current() != chain.Global() {
		t.Errorf("current = %v, want global %v", chain.Current(), chain.Global())
	}
	if chain.Depth() != 1 {
		t.Errorf("depth = %d, want 1", chain.Depth())
	}
}

func TestChainCannotExitGlobal(t *testing.T) {
	chain := NewChain()
	assertPanics(t, chain.Exit)
}

func TestChainResolveWalksParent(t *testing.T) {
	chain := NewChain()
	symGlobal := SymbolID(3)
	chain.Define("x", symGlobal)

	chain.Enter(ScopeFunction, "f")
	if got, ok := chain.Resolve("x"); !ok || got != symGlobal {
		t.Errorf("resolve(x) inside f = (%v, %v), want (%v, true)", got, ok, symGlobal)
	}
	chain.Exit()

	if got, ok := chain.Resolve("x"); !ok || got != symGlobal {
		t.Errorf("resolve(x) after exit = (%v, %v), want (%v, true)", got, ok, symGlobal)
	}
}

func TestChainLocalShadowsParent(t *testing.T) {
	chain := NewChain()
	symGlobal := SymbolID(3)
	chain.Define("x", symGlobal)

	chain.Enter(ScopeFunction, "f")
	symLocal := SymbolID(4)
	chain.Define("x", symLocal)
	if got, ok := chain.Resolve("x"); !ok || got != symLocal {
		t.Errorf("resolve(x) inside f = (%v, %v), want (%v, true) (local shadows global)", got, ok, symLocal)
	}
	chain.Exit()

	if got, ok := chain.Resolve("x"); !ok || got != symGlobal {
		t.Errorf("resolve(x) after exit = (%v, %v), want (%v, true) (global unchanged)", got, ok, symGlobal)
	}
}

func TestChainNestedScopes(t *testing.T) {
	chain := NewChain()
	chain.Enter(ScopeFunction, "f")
	chain.Enter(ScopeBlock, "if")
	if chain.Depth() != 3 {
		t.Errorf("depth = %d, want 3", chain.Depth())
	}
	chain.Exit()
	if chain.Depth() != 2 {
		t.Errorf("depth = %d, want 2", chain.Depth())
	}
	chain.Exit()
	if chain.Depth() != 1 {
		t.Errorf("depth = %d, want 1", chain.Depth())
	}
}

// ── Self-built: chain parent-pointer correctness ──

// TestChainParentPointersNested verifies that nested Enter/Exit builds a
// correct parent tree: each scope's parent is the scope that was current at
// its creation, and the global scope has no parent.
func TestChainParentPointersNested(t *testing.T) {
	chain := NewChain()
	global := chain.Global()

	fn := chain.Enter(ScopeFunction, "f")
	blk := chain.Enter(ScopeBlock, "if")
	chain.Exit() // if
	chain.Exit() // f

	if got, ok := chain.Parent(blk); !ok || got != fn {
		t.Errorf("parent(block) = (%v, %v), want (%v, true)", got, ok, fn)
	}
	if got, ok := chain.Parent(fn); !ok || got != global {
		t.Errorf("parent(fn) = (%v, %v), want (%v, true)", got, ok, global)
	}
	if got, ok := chain.Parent(global); ok || got != 0 {
		t.Errorf("parent(global) = (%v, %v), want (0, false)", got, ok)
	}
}

// TestChainParentPointersSiblings verifies sibling scopes never cross: two
// function scopes pushed sequentially are siblings under the global scope,
// and a scope nested in the first is not a parent of the second.
func TestChainParentPointersSiblings(t *testing.T) {
	chain := NewChain()
	global := chain.Global()

	fn1 := chain.Enter(ScopeFunction, "a")
	blk := chain.Enter(ScopeBlock, "inner")
	chain.Exit() // inner
	chain.Exit() // a
	fn2 := chain.Enter(ScopeFunction, "b")
	chain.Exit() // b

	if got, ok := chain.Parent(fn1); !ok || got != global {
		t.Errorf("parent(fn1) = (%v, %v), want (%v, true)", got, ok, global)
	}
	if got, ok := chain.Parent(fn2); !ok || got != global {
		t.Errorf("parent(fn2) = (%v, %v), want (%v, true)", got, ok, global)
	}
	if got, ok := chain.Parent(blk); !ok || got != fn1 {
		t.Errorf("parent(inner block) = (%v, %v), want (%v, true)", got, ok, fn1)
	}
	if got, ok := chain.Parent(blk); ok && got == fn2 {
		t.Errorf("sibling scope leaked into fn2: parent(block) = %v", got)
	}
}

// TestChainChildrenRegistry verifies the scope tree records children in
// creation order (Python DSLScope.children).
func TestChainChildrenRegistry(t *testing.T) {
	chain := NewChain()
	global := chain.Global()
	fn1 := chain.Enter(ScopeFunction, "a")
	chain.Exit()
	fn2 := chain.Enter(ScopeFunction, "b")
	chain.Exit()

	children := chain.Scope(global).Children()
	if len(children) != 2 || children[0] != chain.Scope(fn1) || children[1] != chain.Scope(fn2) {
		t.Errorf("global children = %d entries, want [fn1 fn2] in creation order", len(children))
	}
}

// ── R1 minors: contract hardening ──

// TestChainParentUnknownIDPanics locks the tri-state contract: an id never
// registered by the chain is a programmer error (panic), while the global
// scope's missing parent is a legitimate (0, false) result.
func TestChainParentUnknownIDPanics(t *testing.T) {
	chain := NewChain()
	assertPanics(t, func() { chain.Parent(ScopeID(999)) })
}

// TestChainEnterRejectsExtraNames locks the variadic contract: more than one
// name is a programmer error, not a silent ignore.
func TestChainEnterRejectsExtraNames(t *testing.T) {
	chain := NewChain()
	assertPanics(t, func() { chain.Enter(ScopeBlock, "a", "b") })
}
