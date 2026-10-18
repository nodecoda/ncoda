package ir_test

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

// User foreign code is an opaque byte string.  The compiler may not strip
// leading blank lines while lowering a source template; doing so makes
// decompile→compile roundtrips consume one newline per cycle.
func TestBuildForeignCodePreservesLeadingSourceNewlines(t *testing.T) {
	for _, tc := range []struct {
		name string
		code string
		want string
	}{
		{"one newline", "\ndef main() -> dict:\n    return {}\n", "\ndef main() -> dict:\n    return {}\n"},
		{"two newlines", "\n\ndef main() -> dict:\n    return {}\n", "\n\ndef main() -> dict:\n    return {}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "@language nodecoda/1\n@mode workflow\nfunction main() {\n" +
				"    foreign code python3() -> { result: string; } { source `" + tc.code + "`; };\n" +
				"}\n"
			g := buildIR(t, src)
			nodes := nodesOf(g)["code"]
			if len(nodes) != 1 {
				t.Fatalf("code nodes = %d, want 1", len(nodes))
			}
			run := nodes[0].(*ir.CodeRun)
			if run.Code != tc.want {
				t.Fatalf("foreign source changed\nwant %q\ngot  %q", tc.want, run.Code)
			}
		})
	}
}
