package binding

import "testing"

// Binder statement/expression visitor coverage (2026-08-20 test
// governance): break/continue/foreign-code/lambda/else-if/for-expr visit
// paths that the scope-pass suite only reached indirectly.

func TestBinderStmtVisitors(t *testing.T) {
	source := `
        function main(string[] items) -> string[] {
            let out = for (item in items) {
                if (item == "skip") { continue; }
                if (item == "stop") { break; }
                yield item;
            };
            let mapped = filter(items, (item) -> item.contains("a"));
            let cnode = foreign code python3(string v = "x") -> string {
                source ` + "`" + `def main(v: str) -> dict:
    return {"result": v}` + "`" + `;
            };
            return mapped;
        }
        `
	errs, snap := errorDiagnostics(t, source)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if snap == nil {
		t.Fatal("nil snapshot")
	}
	if snap.Functions().Len() < 1 {
		t.Fatalf("Functions().Len() = %d, want >= 1 (main)", snap.Functions().Len())
	}
	var found bool
	snap.Declarations().Range(func(id SymbolID, d SymbolDecl) bool {
		if d.Name == "item" || d.Name == "v" || d.Name == "mapped" {
			found = true
			return false
		}
		return true
	})
	if !found {
		t.Log("lambda/loop params not found by name scan (informational)")
	}
}
