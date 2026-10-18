package passes

// zip_loop_rebuild_contract_test.go —— zip 循环（多变量 / 多源）在任何 AST
// **重写面**都不许被截断成第一个变量/源（R-146 家族，2026-09-29）。
//
// 为什么单列（一手）：`const_substitution` 的 ForStmt 分支走单变量访问器
// （`NewForStmt(n.VarName(), n.VarType(), n.Iterable())`）⇒ **多源被截成第一个、
// 其余循环变量丢失**，binder 随后对丢掉的变量报 `UNDEFINED_VAR`：
//
//	`const N = 1; … for (a, b in zip(xs, ys)) { s = a + b; }`
//	⇒ `Undefined variable: 'b'`（修前实测）
//
// 而**没有常量声明**的程序不经过那个 pass ⇒ 同一段循环写得出来、编不过；
// `desugar` / `normalize` 早已有 zip 分支（`desugar.go` 的注释就是这条规则），
// 本判据把三处重写面钉在同一把尺子上。
//
// 撤实现必红（一手）：把 `const_substitution` 的 zip 分支换回单变量构造器 ⇒
// ① / ② 红（UNDEFINED_VAR 'b'）；把 `FoldConstCalls` 的 zip 分支换回 ⇒ 同样红
// （循环体里带 `int("0")` 时它也要重建这个循环）。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
)

// entryBody 取入口函数体（`function main` 即入口 —— 它不落在 Program.Functions()
// 里，而在 Program.Entry()）。
func entryBody(t *testing.T, res *FrontendResult, what string) *ast.Block {
	t.Helper()
	if res.HasErrors() {
		t.Fatalf("%s：管线报了错：%v", what, res.Diagnostics)
	}
	fn := res.Program.Program.Entry()
	if fn == nil {
		t.Fatalf("%s：无入口函数", what)
	}
	return fn.Body()
}

// zipLoopOf 取 `prog` 里第一个循环语句（`*ast.ForStmt`），断言它仍是 zip 形。
func zipLoopOf(t *testing.T, res *FrontendResult, what string) *ast.ForStmt {
	t.Helper()
	if res.HasErrors() {
		t.Fatalf("%s：管线报了错（zip 循环被截断的信号）：%v", what, res.Diagnostics)
	}
	body := entryBody(t, res, what)
	var found *ast.ForStmt
	for _, st := range body.Stmts() {
		if fs, ok := st.(*ast.ForStmt); ok {
			found = fs
		}
	}
	if found == nil {
		t.Fatalf("%s：未找到 for 语句", what)
	}
	if !found.IsZip() {
		t.Fatalf("%s：zip 形被截断（IsZip=false）——其余变量/源已丢失", what)
	}
	if len(found.LoopVars()) != 2 || len(found.LoopSources()) != 2 {
		t.Fatalf("%s：zip 形残缺 vars=%v sources=%d", what, found.LoopVars(), len(found.LoopSources()))
	}
	return found
}

// ① 语句级 zip + 常量声明（走 const_substitution）——循环体里再放一个可折叠调用，
// 让 FoldConstCalls 也必须重建同一个循环。
func TestZipLoopSurvivesConstSubstitution(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
const N = 1;
function main(array<int> xs, array<int> ys) -> int {
    int s = 0;
    for (a, b in zip(xs, ys)) {
        s = a + b + int("0") + N;
    }
    return s;
}
`
	res := DefaultPipeline().Run(src, "zip_stmt.ncoda", nil)
	zipLoopOf(t, res, "语句级 zip")
}

// ② 表达式级 zip（`let z = for (a, b in zip(…)) { yield … };`）——同为重写面。
func TestZipForExprSurvivesConstSubstitution(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
const N = 1;
function main(array<int> xs, array<int> ys) -> array<int> {
    let z = for (a, b in zip(xs, ys)) {
        yield a + b + int("0") + N;
    };
    return z;
}
`
	res := DefaultPipeline().Run(src, "zip_expr.ncoda", nil)
	if res.HasErrors() {
		t.Fatalf("表达式级 zip：管线报了错：%v", res.Diagnostics)
	}
	var found *ast.ForExpr
	ast.Walk(&forExprFinder{out: &found}, entryBody(t, res, "表达式级 zip"))
	if found == nil {
		t.Fatal("表达式级 zip：未找到 for 表达式")
	}
	if !found.IsZip() || len(found.LoopVars()) != 2 || len(found.LoopSources()) != 2 {
		t.Fatalf("表达式级 zip 被截断：vars=%v sources=%d", found.LoopVars(), len(found.LoopSources()))
	}
}

type forExprFinder struct{ out **ast.ForExpr }

func (f *forExprFinder) Visit(n ast.Node) ast.Visitor {
	if fe, ok := n.(*ast.ForExpr); ok && *f.out == nil {
		*f.out = fe
	}
	return f
}
