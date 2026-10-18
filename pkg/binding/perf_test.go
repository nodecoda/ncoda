package binding

import (
	"fmt"
	"testing"
)

// Performance regression guard for the Snapshot accessors (Task 45 P3).
//
// Before the fix every accessor deep-copied its whole map on EACH call
// (passes and lowering call DeclarationSymbols().Get / Declarations().Get
// per node), turning per-node reads into O(n) copies — O(n^2) total on a
// chain (5000 nodes ≈ 3GB peak). Accessors now wrap the internal map in
// O(1); Publish already defensively clones and no setter mutates in
// place. This benchmark pins a 10k-entry snapshot read per-entry the way
// passes/lowering do, so a deep-copy regression shows up as seconds.
//
//	go test -bench=BenchmarkSnapshotAccessors -benchtime=1x ./internal/binding/
//
// Expected on the dev box (post-P3): ~2-8ms per 10k entries.

func BenchmarkSnapshotAccessors(b *testing.B) {
	const n = 10000
	bld := NewBuilder()
	for i := 0; i < n; i++ {
		id := AstNodeID(i + 1)
		bld.AstNodes[id] = NodeInfo{}
		bld.DeclarationSymbols[id] = SymbolID(i + 1)
		bld.Declarations[SymbolID(i+1)] = SymbolDecl{Name: fmt.Sprintf("s%d", i)}
		bld.Scopes[ScopeID(i+1)] = ScopeInfo{}
		bld.Functions[id] = FunctionID(i + 1)
	}
	snap := bld.Publish()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < n; j++ {
			id := AstNodeID(j + 1)
			if _, ok := snap.DeclarationSymbols().Get(id); !ok {
				b.Fatal("missing declaration symbol")
			}
			if _, ok := snap.Declarations().Get(SymbolID(j + 1)); !ok {
				b.Fatal("missing declaration")
			}
			if _, ok := snap.Scopes().Get(ScopeID(j + 1)); !ok {
				b.Fatal("missing scope")
			}
		}
	}
}
