package parser

// route 1a: ?? null-coalescing 解析测试（优先级、链、JS 式混用限制、
// 三目/括号边界）。审核裁决 1/5（2026-09-05）。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
)

func orBinary(t *testing.T, src string) *ast.BinaryExpr {
	t.Helper()
	prog := conformOK(t, "function main() { "+src+" }")
	stmts := prog.Entry().Body().Stmts()
	if len(stmts) == 0 {
		t.Fatalf("no stmts for %q", src)
	}
	var val ast.Expr
	switch s := stmts[0].(type) {
	case *ast.AssignStmt:
		val = s.Value()
	case *ast.VarDeclStmt:
		val = s.Init()
	default:
		t.Fatalf("stmt[0] type %T for %q", stmts[0], src)
	}
	b, ok := val.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("value not BinaryExpr for %q: %T", src, val)
	}
	return b
}

func TestNullCoalesceChainLeftAssoc(t *testing.T) {
	// a ?? b ?? c → (a ?? b) ?? c
	b := orBinary(t, "let x = a ?? b ?? c;")
	if b.Op() != "??" {
		t.Fatalf("root op = %q, want ??", b.Op())
	}
	l, ok := b.Left().(*ast.BinaryExpr)
	if !ok || l.Op() != "??" {
		t.Fatalf("left not ?? chain: %T %v", b.Left(), b.Left())
	}
}

func TestNullCoalesceLowerThanComparison(t *testing.T) {
	// a ?? b == c → a ?? (b == c)（?? 低于比较）
	b := orBinary(t, "let x = a ?? b == c;")
	if b.Op() != "??" {
		t.Fatalf("root op = %q, want ??", b.Op())
	}
	if r, ok := b.Right().(*ast.BinaryExpr); !ok || r.Op() != "==" {
		t.Fatalf("right not (b == c): %T %v", b.Right(), b.Right())
	}
}

func TestNullCoalesceHigherThanTernary(t *testing.T) {
	// a ?? b ? x : y → cond = (a ?? b)
	prog := conformOK(t, "function main() { let z = a ?? b ? x : y; }")
	stmts := prog.Entry().Body().Stmts()
	vd := stmts[0].(*ast.VarDeclStmt)
	ter, ok := vd.Init().(*ast.TernaryExpr)
	if !ok {
		t.Fatalf("value not TernaryExpr: %T", vd.Init())
	}
	if c, ok := ter.Condition().(*ast.BinaryExpr); !ok || c.Op() != "??" {
		t.Fatalf("ternary cond not (a ?? b): %T %v", ter.Condition(), ter.Condition())
	}
}

func TestNullCoalesceMixedWithLogicalRejected(t *testing.T) {
	cases := []string{
		"let x = a || b ?? c;",
		"let x = a ?? b || c;",
		"let x = a && b ?? c;",
		"let x = a ?? b && c;",
		"let x = a ?? b || c ?? d;",
	}
	for _, src := range cases {
		prog, errs := ParseSource("function main() { "+src+" }", "<coalesce-mix>")
		found := false
		for _, e := range errs {
			if strings.Contains(e.Message, "cannot be mixed") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ParseSource(%q): want 'cannot be mixed' error, got errs=%v prog=%v", src, errs, prog != nil)
		}
	}
}

func TestNullCoalesceMixedWithLogicalGroupedOK(t *testing.T) {
	// 括号分组隔离 → 合法
	conformOK(t, "function main() { let x = (a || b) ?? c; }")
	conformOK(t, "function main() { let x = a ?? (b && c); }")
	conformOK(t, "function main() { let x = (a ?? b) || c; }")
	conformOK(t, "function main() { let x = a || (b ?? c); }")
}

func TestNullCoalesceInNestedContexts(t *testing.T) {
	// 记录构造字段 / 参数 / if 条件表达式（递归自洽：嵌套环境仍成立）
	conformOK(t, "function main() { let r = { \"k\": a ?? b, \"m\": c ?? d ?? e }; }")
	conformOK(t, "function main() { if (empty(x) || x == \"无\") { let v = a ?? b; } }")
	conformOK(t, "function main() { let s = (a ?? b).field; }")
}
