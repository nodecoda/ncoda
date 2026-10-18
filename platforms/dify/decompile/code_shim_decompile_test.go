package decompile

import (
	"strings"
	"testing"

	cd "github.com/nodecoda/ncoda/pkg/ncoda/ast4"
	"github.com/nodecoda/ncoda/pkg/ncoda/render"
)

// Compiler-owned single-expression shims are not user code. Raising must
// reverse them to the original ncoda value and alias every downstream port
// reference to that value; a literal return must not re-enter as foreign code.
func TestCompilerShimCodeIsReversedToLiteralValue(t *testing.T) {
	n := &nodeInfo{ID: "code_3", Type: "code", Data: map[string]any{
		"code_language": "python3",
		"code":          "# ncoda:shim 1\ndef main() -> dict:\n    return {\"result\": \"\"}",
		"variables":     []any{},
		"outputs": map[string]any{
			"result": map[string]any{"type": "string"},
		},
	}}
	c := &converter{
		g:       &wfGraph{nodes: map[string]*nodeInfo{"code_3": n}},
		startID: "start",
		used:    map[string]bool{},
	}

	stmts := c.convertCode(n)
	if len(stmts) != 0 {
		t.Fatalf("reversible shim must not materialize a code binding, got:\n%s", renderStmts(stmts))
	}
	got := render.RenderNCodaExpr(c.refExpr("code_3", "result", nil))
	if got != `""` {
		t.Fatalf("shim port reference = %s, want empty string literal", got)
	}
}

func TestUserCodeCodeRunnerRemainsForeignCode(t *testing.T) {
	n := &nodeInfo{ID: "code_3", Type: "code", Data: map[string]any{
		"code_language": "python3",
		"code":          "def main() -> dict:\n    return {\"result\": \"\"}",
		"variables":     []any{},
		"outputs": map[string]any{
			"result": map[string]any{"type": "string"},
		},
	}}
	c := &converter{
		g:       &wfGraph{nodes: map[string]*nodeInfo{"code_3": n}},
		startID: "start",
		used:    map[string]bool{},
	}

	stmts := c.convertCode(n)
	if len(stmts) != 1 {
		t.Fatalf("user code statement count = %d, want 1", len(stmts))
	}
	if !strings.Contains(render.RenderNCodaStmt(stmts[0], 0), "foreign code python3") {
		t.Fatalf("user code must remain foreign code, got:\n%s", render.RenderNCodaStmt(stmts[0], 0))
	}
}

func renderStmts(stmts []cd.Stmt) string {
	var b strings.Builder
	for _, s := range stmts {
		b.WriteString(render.RenderNCodaStmt(s, 0))
		b.WriteString("\n")
	}
	return b.String()
}
