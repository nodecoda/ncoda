package passes

// S107 族（if-elif-else 中臂丢失）容器闭包回归：desugar 必须递归**所有**含块
// 容器，保证 canonical AST 进入 IR build 前 elseIfs 必已清空（emitIf 只消费
// ElseBody；pkg/ir/build.go collectAssignedVars 同处读 ElseIfs = IR 层
// 假定该不变量）。历史盲区：desugarStmt 只递归 If/For/While/Block/ParallelStmt，
// 漏 loop-expr（VarDecl.Init/Assign.Value/ExprStmt 里的 ForExpr/ParallelForExpr
// body）与 Attempt/Switch/RequestInput 分支体 —— elif 在其中静默丢中臂。
// 根因：docs/ideas-todo/2026-09-08-d2-familyC-rt-renumber-progress.md（S107 S2 +
// 编译器专家 gate 裁决）。封闭性判据 = ast.Walk 全树 elseIfs 计数为 0（visitor
// Walk 枚举全部块字段，见 pkg/ast/visitor.go）。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
)

// elseIfProbe 用 ast.Walk 枚举全树：统计 IfStmt 总数与残留 elseIfs 总数。
// 返回后者的同时记录命中容器，保证断言非空走（若 walker/语料没进容器，
// ifCount 为 0 则测试自曝）。
type elseIfProbe struct {
	ifCount   int
	elseIfs   int
	loopExprs int
	attempts  int
	switches  int
	requests  int
}

func (p *elseIfProbe) Visit(n ast.Node) ast.Visitor {
	switch v := n.(type) {
	case *ast.IfStmt:
		p.ifCount++
		p.elseIfs += len(v.ElseIfs())
	case *ast.ForExpr:
		p.loopExprs++
	case *ast.ParallelForExpr:
		p.loopExprs++
	case *ast.AttemptStmt:
		p.attempts++
	case *ast.SwitchStmt:
		p.switches++
	case *ast.RequestInputStmt:
		p.requests++
	}
	return p
}

func probeElseIfs(t *testing.T, prog *ast.Program) elseIfProbe {
	t.Helper()
	var p elseIfProbe
	ast.Walk(&p, prog)
	return p
}

// desugarCounts parses src, runs the desugar pass, returns pre/post probes.
func desugarCounts(t *testing.T, src string) (elseIfProbe, elseIfProbe) {
	t.Helper()
	prog := parseSourceOrFail(t, src)
	pre := probeElseIfs(t, prog)
	desugarProgram(t, prog)
	post := probeElseIfs(t, prog)
	return pre, post
}

// assertClosed 断言：容器确实被语料命中（containerCount>0、ifCount>0），
// desugar 后全树 elseIfs 清零。
func assertClosed(t *testing.T, name string, pre, post elseIfProbe) {
	t.Helper()
	if pre.ifCount == 0 {
		t.Fatalf("%s: fixture 未命中任何 IfStmt（语料/解析问题），测试空走", name)
	}
	if pre.elseIfs == 0 {
		t.Fatalf("%s: fixture 未命中任何 else-if（语料/解析问题），测试空走", name)
	}
	if post.elseIfs != 0 {
		t.Errorf("%s: desugar 后仍有 %d 个 elseIfs 残留（中臂将静默丢失）", name, post.elseIfs)
	}
}

// ── 盲区 1：loop-expr 表达式体（parallel for / for） ────────────────

func TestDesugarElifInParallelForExprBody(t *testing.T) {
	src := `
function main() {
    let items = [1];
    let agg = parallel for (it in items, concurrency: 1, on_error: terminate) {
        if (it == 1) { var x = 1; } else if (it == 2) { var y = 2; } else { var z = 3; }
        yield out = it;
    };
}
`
	pre, post := desugarCounts(t, src)
	assertClosed(t, "parallel-for-expr", pre, post)
	if post.loopExprs == 0 {
		t.Fatal("parallel-for-expr: ast.Walk 未命中 ParallelForExpr")
	}
}

func TestDesugarElifInForExprBody(t *testing.T) {
	src := `
function main() {
    let items = [1];
    let ys = for (x in items) {
        if (x == 1) { var a = 1; } else if (x == 2) { var b = 2; } else { var c = 3; }
        yield r = x;
    };
}
`
	pre, post := desugarCounts(t, src)
	assertClosed(t, "for-expr", pre, post)
	if post.loopExprs == 0 {
		t.Fatal("for-expr: ast.Walk 未命中 ForExpr")
	}
}

func TestDesugarElifInAssignValueLoopExpr(t *testing.T) {
	// loop-expr 是 parser primary（可出现在赋值 RHS）：`x = for(...)`。
	src := `
function main() {
    var xs;
    let items = [1];
    xs = for (x in items) {
        if (x == 1) { var a = 1; } else if (x == 2) { var b = 2; } else { var c = 3; }
        yield r = x;
    };
}
`
	pre, post := desugarCounts(t, src)
	assertClosed(t, "assign-value loop-expr", pre, post)
}

// ── 盲区 2：Attempt success/failure body ───────────────────────────

func TestDesugarElifInAttemptBodies(t *testing.T) {
	src := `
function main() {
    attempt web_search.search({"q": "x"}) as r {
        success {
            if (true) { var a = 1; } else if (false) { var b = 2; } else { var c = 3; }
        }
        failure (err) {
            if (true) { var d = 1; } else if (false) { var e = 2; } else { var f = 3; }
        }
    }
}
`
	pre, post := desugarCounts(t, src)
	assertClosed(t, "attempt bodies", pre, post)
	if post.attempts == 0 {
		t.Fatal("attempt: ast.Walk 未命中 AttemptStmt")
	}
}

// ── 盲区 3：Switch 分支/default body ────────────────────────────────

func TestDesugarElifInSwitchBranchBodies(t *testing.T) {
	src := `
function main() {
    switch (q) {
        "a": { if (true) { var a = 1; } else if (false) { var b = 2; } else { var c = 3; } }
        default: { if (true) { var d = 1; } else if (false) { var e = 2; } else { var f = 3; } }
    }
}
`
	pre, post := desugarCounts(t, src)
	assertClosed(t, "switch branches", pre, post)
	if post.switches == 0 {
		t.Fatal("switch: ast.Walk 未命中 SwitchStmt")
	}
}

// ── 盲区 4：RequestInput action/timeout body ────────────────────────

func TestDesugarElifInRequestInputBodies(t *testing.T) {
	src := `
function main() {
    request_input<ReviewForm, ReviewAction>({}) as review {
        action ReviewAction.approve {
            if (true) { var a = 1; } else if (false) { var b = 2; } else { var c = 3; }
        }
        timeout {
            if (true) { var d = 1; } else if (false) { var e = 2; } else { var f = 3; }
        }
    }
}
`
	pre, post := desugarCounts(t, src)
	assertClosed(t, "request_input bodies", pre, post)
	if post.requests == 0 {
		t.Fatal("request_input: ast.Walk 未命中 RequestInputStmt")
	}
}

// ── 递归深度与幂等 ──────────────────────────────────────────────────

func TestDesugarElifRecursiveDepthLoopExprContainsAttempt(t *testing.T) {
	// 容器套容器 + elif 内 elif：loop-expr 体内 attempt 体内 elif 链。
	src := `
function main() {
    let items = [1];
    let agg = parallel for (it in items, concurrency: 1, on_error: terminate) {
        if (it == 1) {
            var x = 1;
        } else if (it == 2) {
            if (it == 2) { var y = 1; } else if (it == 3) { var y2 = 2; } else { var y3 = 3; }
        } else {
            attempt web_search.search({"q": "x"}) as r {
                success { if (true) { var s = 1; } else if (false) { var s2 = 2; } else { var s3 = 3; } }
                failure (err) { if (true) { var f = 1; } else if (false) { var f2 = 2; } else { var f3 = 3; } }
            }
        }
        yield out = it;
    };
}
`
	pre, post := desugarCounts(t, src)
	assertClosed(t, "recursive depth loop-expr+attempt", pre, post)
	if post.ifCount < 4 {
		t.Fatalf("递归语料期望 ≥4 个 IfStmt（含嵌套），got %d", post.ifCount)
	}
}

func TestDesugarContainerClosureIsIdempotent(t *testing.T) {
	src := `
function main() {
    let items = [1];
    let agg = parallel for (it in items, concurrency: 1, on_error: terminate) {
        if (it == 1) { var x = 1; } else if (it == 2) { var y = 2; } else { var z = 3; }
        yield out = it;
    };
    attempt web_search.search({"q": "x"}) as r {
        success { if (true) { var a = 1; } else if (false) { var b = 2; } else { var c = 3; } }
        failure (err) { if (true) { var d = 1; } else if (false) { var e = 2; } else { var f = 3; } }
    }
}
`
	_, post1 := desugarCounts(t, src)
	if post1.elseIfs != 0 {
		t.Fatalf("第一遍后仍有 elseIfs=%d", post1.elseIfs)
	}
	// 第二遍（幂等）：不复活 elseIfs、不改变 if 总数。
	prog := parseSourceOrFail(t, src)
	desugarProgram(t, prog)
	desugarProgram(t, prog)
	post2 := probeElseIfs(t, prog)
	if post2.elseIfs != 0 {
		t.Fatalf("第二遍后复活 elseIfs=%d", post2.elseIfs)
	}
	if post2.ifCount != post1.ifCount {
		t.Errorf("第二遍改变 if 总数：%d → %d", post1.ifCount, post2.ifCount)
	}
}
