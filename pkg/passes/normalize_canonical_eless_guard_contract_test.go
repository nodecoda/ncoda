package passes

// normalize_canonical_eless_guard_contract_test.go —— 规范形判据：**嵌套
// else-less 守卫的合取融合**（`pkg/passes/normalize.go::canonicalElessGuardFusion`）。
//
// 为什么这是判据而不是「测试迁就实现」：`if (a) { if (b) { X } }` 与
// `if (a && b) { X }` 是**同一个意思的两种拼写**，而两者产物**不同构**（前者物化两道门
// ⇒ 帧/流程出口的公共落点被两条门端口边命中 ⇒ 反编译判不出候选互斥 ⇒ `RS-MRG-001`）。
// 本仓原则「同一个意思能写出两种产物 = 一个必须钉死的坑」⇒ 规范化阶段钉死**单门形态**。
// 产品面判据（往返 + 两种拼写同形状签名）= `internal/compiler/
// cozebiz_nested_eless_frame_exit_contract_test.go`。
//
// 锁两半（判据契约要求）：
//   · 正侧：折（两层 / 链长 ≥3 / 宿主块内递归 / 幂等 / 已折拼写是不动点）；
//   · 负侧：**不折**（内层带 else / 外层带 else / 内层守卫含调用 / then 臂不止一条语句）
//     —— 只锁正侧会被「无条件把两个 if 拼成一个」满足，那会把 else 归属与节点执行面
//     一起改掉（R-172 的三个前置正是为此存在）。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
)

// guardOf returns the entry body's first statement as an IfStmt.
func guardOf(t *testing.T, prog *ast.Program) *ast.IfStmt {
	t.Helper()
	stmts := prog.Entry().Body().Stmts()
	if len(stmts) == 0 {
		t.Fatal("entry body is empty")
	}
	s, ok := stmts[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("entry body[0] is %T, want *ast.IfStmt", stmts[0])
	}
	return s
}

// assertFolded requires stmts[0] to be the folded guard: its condition is a
// conjunction of wantOperands comparisons and its then-body is the INNER
// then-body (the inner IfStmt is gone) — the positive half's shape lock.
func assertFolded(t *testing.T, prog *ast.Program, wantOperands int) *ast.IfStmt {
	t.Helper()
	got := guardOf(t, prog)
	ops := cmpOps(t, got.Condition())
	if len(ops) != wantOperands {
		t.Fatalf("fused condition has %d comparison operands (%v), want %d joined by &&",
			len(ops), ops, wantOperands)
	}
	if _, stillNested := got.ThenBody().Stmts()[0].(*ast.IfStmt); stillNested {
		t.Fatalf("then-body still starts with an *ast.IfStmt — the inner guard was not folded in")
	}
	if len(got.ElseIfs()) != 0 || !emptyOrNilBlock(got.ElseBody()) {
		t.Fatalf("fused guard must stay else-less (else_ifs=%d)", len(got.ElseIfs()))
	}
	return got
}

// assertNotFolded requires stmts[0] to still be the ORIGINAL outer guard: its
// condition is not a conjunction and its then-body still holds the inner if.
func assertNotFolded(t *testing.T, prog *ast.Program) *ast.IfStmt {
	t.Helper()
	got := guardOf(t, prog)
	if b, ok := got.Condition().(*ast.BinaryExpr); ok && b.Op() == "&&" {
		t.Fatalf("guard was folded (`&&` condition) but must NOT be: the precondition is unmet")
	}
	return got
}

// andChainOperands flattens a `&&` tree into its operand list.
func andChainOperands(e ast.Expr) []ast.Expr {
	b, ok := e.(*ast.BinaryExpr)
	if !ok || b.Op() != "&&" {
		return []ast.Expr{e}
	}
	return append(andChainOperands(b.Left()), andChainOperands(b.Right())...)
}

// cmpOps returns the comparison operator of every `&&` operand, in order.
func cmpOps(t *testing.T, e ast.Expr) []string {
	t.Helper()
	var out []string
	for _, op := range andChainOperands(e) {
		b, ok := op.(*ast.BinaryExpr)
		if !ok {
			t.Fatalf("conjunction operand is %T, want *ast.BinaryExpr (comparison)", op)
		}
		out = append(out, b.Op())
	}
	return out
}

const fuseTwoLevelsSrc = `
function main(int n) -> string {
    if (n > 0) {
        if (n > 1) {
            return "H";
        }
    }
    return "L";
}
`

func TestCanonicalElessGuardFusionFoldsNested(t *testing.T) {
	prog := parseSourceOrFail(t, fuseTwoLevelsSrc)
	normalizeProgram(t, prog)

	got := assertFolded(t, prog, 2)
	if n := len(prog.Entry().Body().Stmts()); n != 2 {
		t.Fatalf("entry body has %d statements, want 2 (folded guard + fallback return)", n)
	}
	if n := len(got.ThenBody().Stmts()); n != 1 {
		t.Fatalf("fused then-body must be the INNER then-body, got %d statements", n)
	}
}

func TestCanonicalElessGuardFusionFoldsChain(t *testing.T) {
	const src = `
function main(int n) -> string {
    if (n > 0) {
        if (n > 1) {
            if (n > 2) {
                return "H";
            }
        }
    }
    return "L";
}
`
	prog := parseSourceOrFail(t, src)
	normalizeProgram(t, prog)

	assertFolded(t, prog, 3)
	// 幂等：折出来的规范形再跑一次规范化必须原样不动。
	normalizeProgram(t, prog)
	assertFolded(t, prog, 3)
}

func TestCanonicalElessGuardFusionRecursesIntoHostBlocks(t *testing.T) {
	const src = `
function main(int n) -> string {
    while (n > 0) limit 10 {
        if (n > 0) {
            if (n > 1) {
                return "H";
            }
        }
    }
    return "L";
}
`
	prog := parseSourceOrFail(t, src)
	normalizeProgram(t, prog)

	host, ok := prog.Entry().Body().Stmts()[0].(*ast.WhileStmt)
	if !ok {
		t.Fatalf("entry body[0] is %T, want *ast.WhileStmt", prog.Entry().Body().Stmts()[0])
	}
	body := host.Body().Stmts()
	if len(body) != 1 {
		t.Fatalf("while body has %d statements, want 1 (host blocks must fuse too)", len(body))
	}
	inner, ok := body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("while body[0] is %T, want *ast.IfStmt", body[0])
	}
	if ops := cmpOps(t, inner.Condition()); len(ops) != 2 {
		t.Fatalf("guard inside a host block must fold, got operands %v", ops)
	}
}

func TestCanonicalElessGuardFusionSkipsInnerElse(t *testing.T) {
	const src = `
function main(int n) -> string {
    if (n > 0) {
        if (n > 1) { return "H"; } else { return "M"; }
    }
    return "L";
}
`
	prog := parseSourceOrFail(t, src)
	normalizeProgram(t, prog)

	got := assertNotFolded(t, prog)
	if _, ok := got.ThenBody().Stmts()[0].(*ast.IfStmt); !ok {
		t.Fatalf("then-body[0] must remain the inner *ast.IfStmt")
	}
}

func TestCanonicalElessGuardFusionSkipsOuterElse(t *testing.T) {
	const src = `
function main(int n) -> string {
    if (n > 0) {
        if (n > 1) { return "H"; }
    } else {
        return "L";
    }
    return "L";
}
`
	prog := parseSourceOrFail(t, src)
	normalizeProgram(t, prog)

	assertNotFolded(t, prog)
}

func TestCanonicalElessGuardFusionSkipsNodeBearingInnerCondition(t *testing.T) {
	const src = `
function main(int n) -> string {
    if (n > 0) {
        if (fmt(n) != "") { return "H"; }
    }
    return "L";
}
`
	prog := parseSourceOrFail(t, src)
	normalizeProgram(t, prog)

	assertNotFolded(t, prog)
}

// 前置 4（`||` / 三元守卫 ⇒ 不折）：规范形是**一种拼写**，而反编译端只发文本、
// 不造括号 ⇒ 折出来需要括号才成义的守卫（`(a || b) && c`）它就发不出同一个拼写，
// 两侧准入集合会不等 ⇒ 往返破裂。**两侧同一份准入集合**是这条前置的全部理由。
func TestCanonicalElessGuardFusionSkipsDisjunctionOperand(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"outer_or", `
function main(int n, int m) -> string {
    if (n > 0 || m > 0) {
        if (n > 1) { return "H"; }
    }
    return "L";
}
`},
		{"inner_or", `
function main(int n, int m) -> string {
    if (n > 0) {
        if (m > 0 || n > 1) { return "H"; }
    }
    return "L";
}
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog := parseSourceOrFail(t, tc.src)
			normalizeProgram(t, prog)
			assertNotFolded(t, prog)
		})
	}
}

func TestCanonicalElessGuardFusionSkipsMultiStatementThenArm(t *testing.T) {
	const src = `
function main(int n) -> string {
    if (n > 0) {
        let w = n + 1;
        if (n > 1) { return "H"; }
    }
    return "L";
}
`
	prog := parseSourceOrFail(t, src)
	normalizeProgram(t, prog)

	got := assertNotFolded(t, prog)
	if n := len(got.ThenBody().Stmts()); n != 2 {
		t.Fatalf("then-arm must keep both statements, got %d", n)
	}
}

// TestCanonicalElessGuardFusionIsFixedPointOnFusedSpelling —— 存活侧锚点：作者
// 直接写出来的合取拼写必须原样保留（规范化不得反向拆开，也不得二次包一层）。
func TestCanonicalElessGuardFusionIsFixedPointOnFusedSpelling(t *testing.T) {
	const src = `
function main(int n) -> string {
    if (n > 0 && n > 1) { return "H"; }
    return "L";
}
`
	prog := parseSourceOrFail(t, src)
	normalizeProgram(t, prog)

	if ops := cmpOps(t, guardOf(t, prog).Condition()); len(ops) != 2 {
		t.Fatalf("already-fused spelling must be a fixed point, got operands %v", ops)
	}
}
