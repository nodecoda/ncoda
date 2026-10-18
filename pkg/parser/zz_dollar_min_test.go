package parser

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
)

func TestDollarEscapeMin(t *testing.T) {
	cases := []string{
		"function main() { }",
		"type RT1 = { string $type; };\nfunction main() { }",
		"type RT1 = { string $type; string name; };\nfunction main(string input) { RT1 a = {\"$type\": \"x\", \"name\": \"y\"}; var t = a.$type; return t; }",
	}
	for i, src := range cases {
		p := New(lex(src))
		_, errs := p.Parse()
		if len(errs) > 0 {
			t.Logf("case %d src=%q", i, src)
			for _, e := range errs {
				t.Logf("  err: %v", e)
			}
			t.Fatalf("case %d failed", i)
		}
	}
}

// 2026-08-29：IDENTIFIER token 的 lexeme 若是关键字拼写，只可能来自 '$' 转义
// （裸 file→KW_FILE、裸 type→KW_TYPE）。parsePrimary 必须打 Escaped 标记，
// binder 才能拒绝 escaped 关键字解析到内建（$file(...) 静默别名漏洞）。
func TestDollarEscapeMarksEscapedIdent(t *testing.T) {
	src := `function main() { let a = $type; let b = $output; let c = $foo; }`
	prog, errs := ParseSource(src, "escaped-probe")
	if len(errs) > 0 {
		for _, e := range errs {
			t.Logf("  err: %v", e)
		}
		t.Fatalf("parse failed: %v", errs[0])
	}
	fn := prog.Entry()
	escaped := map[string]bool{}
	for _, st := range fn.Body().Stmts() {
		v, ok := st.(*ast.VarDeclStmt)
		if !ok {
			continue
		}
		id, ok := v.Init().(*ast.IdentExpr)
		if !ok {
			continue
		}
		escaped[id.Name()] = id.Escaped()
	}
	for name, want := range map[string]bool{"type": true, "output": true, "foo": false} {
		if got := escaped[name]; got != want {
			t.Errorf("$%s/%s Escaped = %v, want %v", name, name, got, want)
		}
	}
}
