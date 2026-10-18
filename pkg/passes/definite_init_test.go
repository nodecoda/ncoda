package passes

// Ports of lang/tests/lang/unit/test_definite_init.py (5 cases): the
// definite-initialization check (E1048) runs after the scope phase so the
// reference symbols are available.

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/diag"
)

func runDefiniteInit(t *testing.T, src string) *Context {
	t.Helper()
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	DefiniteInitPass{}.Run(prog, ctx)
	return ctx
}

func uninitializedErrors(ctx *Context) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range ctx.Diagnostics {
		if d.Severity == diag.Error && d.Code == diag.UNINITIALIZED_USE {
			out = append(out, d)
		}
	}
	return out
}

func TestDefiniteInitUninitializedUseDetected(t *testing.T) {
	ctx := runDefiniteInit(t, "function main() -> int { int x; return x; }")
	errs := uninitializedErrors(ctx)
	if len(errs) != 1 {
		t.Fatalf("want exactly one E1048, got %v", ctx.Diagnostics)
	}
	want := "Variable 'x' is used before being definitely assigned"
	if errs[0].Message != want {
		t.Fatalf("message = %q, want %q", errs[0].Message, want)
	}
	if errs[0].Line < 1 || errs[0].Column < 1 {
		t.Fatalf("E1048 must carry the identifier span: %+v", errs[0])
	}
}

func TestDefiniteInitInitializedUseOK(t *testing.T) {
	ctx := runDefiniteInit(t, "function main() -> int { int x = 5; return x; }")
	if errs := uninitializedErrors(ctx); len(errs) != 0 {
		t.Fatalf("unexpected E1048: %v", errs)
	}
}

func TestDefiniteInitParameterAlwaysInitialized(t *testing.T) {
	ctx := runDefiniteInit(t, "function main(int value) -> int { return value; }")
	if errs := uninitializedErrors(ctx); len(errs) != 0 {
		t.Fatalf("unexpected E1048 for a parameter: %v", errs)
	}
}

func TestDefiniteInitLetDeclWithInit(t *testing.T) {
	ctx := runDefiniteInit(t, "function main() -> int { let x = 5; return x; }")
	if errs := uninitializedErrors(ctx); len(errs) != 0 {
		t.Fatalf("unexpected E1048: %v", errs)
	}
}

func TestDefiniteInitBuiltinFunctionNoError(t *testing.T) {
	ctx := runDefiniteInit(t, `function main(string prompt) -> string {
            let r = llm("openai/gpt-4o", {"messages": [{"role": "user", "content": prompt}]});
            return r.text;
        }`)
	if errs := uninitializedErrors(ctx); len(errs) != 0 {
		t.Fatalf("builtins must never trigger E1048: %v", errs)
	}
}

// ── 提升 idiom（R-01 收口 · 2026-09-23）────────────────────────────

// 提升 idiom 的**语言侧**规则：`var v;`（外层声明，**类型省略**）+ 臂内赋值 +
// 臂外读。三条拼写在这同一形态下**不同判**：
//
//	· `var v;`（类型省略）⇒ 豁免「已声明但未确定赋值」（本文件的 IsMutable &&
//	  TypeRef==nil 分支）—— 这正是 raising 的降级形态**唯一合法**的原因；
//	· `any v;` / `int v;` / `string v;`（带类型裸声明）⇒ 必报 E1048。
//
// 因此 **不得**把 raising 的无类型出口「补成 any」：一手实测（coze-biz 往返）
// 该改动让 r0 首编直接 `UNINITIALIZED_USE`（`any v;` 与 `int v;` 同判）。
func TestDefiniteInitHoistIdiomUntypedExemptTypedBareDeclRed(t *testing.T) {
	// if 无 else ⇒ 两臂集合不穷尽 ⇒ 分支后读点「未确定赋值」；类型省略是唯一豁免。
	src := func(decl string) string {
		return "function main(int n) -> int { " + decl + " v; if (n > 0) { v = 1; } return v; }"
	}
	if errs := uninitializedErrors(runDefiniteInit(t, src("var"))); len(errs) != 0 {
		t.Fatalf("类型省略（`var v;`）必须豁免 E1048（raising 的唯一合法降级），got %v", errs)
	}
	for _, typ := range []string{"any", "int", "string"} {
		errs := uninitializedErrors(runDefiniteInit(t, src(typ)))
		if len(errs) != 1 {
			t.Fatalf("带类型裸声明（`%s v;`）必须恰好 1 条 E1048，got %v", typ, errs)
		}
	}
}

// ── self-built structure coverage ───────────────────────────────────

func TestDefiniteInitAssignRHSCheckedBeforeLHSPublished(t *testing.T) {
	// x is declared+initialized above; the RHS x is initialized, so the
	// assignment is legal and must not produce E1048.
	ctx := runDefiniteInit(t, "function main() -> int { int x = 1; x = x; return x; }")
	if errs := uninitializedErrors(ctx); len(errs) != 0 {
		t.Fatalf("unexpected E1048: %v", errs)
	}
}

func TestDefiniteInitForLoopVariableInitialized(t *testing.T) {
	// The loop variable is initialized by the for construct itself, so
	// referencing it inside the body must not flag E1048.
	ctx := runDefiniteInit(t, "function main() { for (var item in items) { output(item); } }")
	if errs := uninitializedErrors(ctx); len(errs) != 0 {
		t.Fatalf("loop variable must be considered initialized: %v", errs)
	}
}

func TestDefiniteInitIfBranchesFreshBlocks(t *testing.T) {
	// 两条规则分开（2026-09-12 重述，原注释是缺陷的书面化）：
	//   ① **可见性向下继承**：嵌套块看得见外层声明（checkBlock 的 inherited），
	//      所以「declared 但未确定赋值」在嵌套块内照样报 E1048；
	//   ② **声明不向外逃逸**：本块直接声明的符号只在 own 里返回，joinBranches
	//      只会退休 own —— 分支内声明的 y 在分支外既不合法也不参与汇聚。
	// 本测试锁的是 ②（y 在 then 块内声明+读取 ⇒ 恰好 1 条 E1048，且指名 y）。
	ctx := runDefiniteInit(t, `
        function main() -> int {
            int x = 1;
            if (true) {
                int y;
                x = y;
            }
            return x;
        }`)
	errs := uninitializedErrors(ctx)
	if len(errs) != 1 {
		t.Fatalf("want exactly one E1048 (y), got %v", ctx.Diagnostics)
	}
	if !strings.Contains(errs[0].Message, "'y'") {
		t.Fatalf("E1048 must name y, got %q", errs[0].Message)
	}
}

// ── 缺陷 A 回归:复合表达式 receiver 递归检查（2026-08-29）──

// 03_conditional_branch 实证:typed 声明 + 分支赋值 + 分支后字段访问,
// 未初始化 receiver 藏在 FieldAccessExpr 里——旧实现 checkExpr 只查
// IdentExpr,`return v.text;` 里的 v 从未被检查,编译通过（语义错误形态）。
func TestDefiniteInitFieldAccessReceiverChecked(t *testing.T) {
	ctx := runDefiniteInit(t, `
        type R = { string text; };
        function main(string language = "中文") -> string {
            R v;
            if (language == "中文") {
                v = llm("openai/gpt-4o", {"messages": [{"role": "user", "content": "x"}]});
            }
            return v.text;
        }`)
	errs := uninitializedErrors(ctx)
	if len(errs) != 1 {
		t.Fatalf("want exactly one E1048 for uninitialized receiver v, got %v", ctx.Diagnostics)
	}
	if !strings.Contains(errs[0].Message, "'v'") {
		t.Fatalf("E1048 must name v, got %q", errs[0].Message)
	}
}

// 索引/调用/二元/模板插值里的 receiver 同样递归检查。
func TestDefiniteInitIndexCallBinaryRecurse(t *testing.T) {
	ctx := runDefiniteInit(t, `
        type R = { string text; };
        function main(string q) -> string {
            R v;
            if (q == "a") {
                v = llm("openai/gpt-4o", {"messages": [{"role": "user", "content": "x"}]});
            }
            let s = v.text + "!" + v.text;
            return s;
        }`)
	errs := uninitializedErrors(ctx)
	// v.text 出现三处（binary 两处 + template 一处）,但都是同一个未初始化
	// 变量——同块分析对每个引用发射一条;这里只断言至少抓住 v 未初始化。
	if len(errs) < 1 {
		t.Fatalf("want E1048 for uninitialized v inside composite expressions, got none:\n%v", ctx.Diagnostics)
	}
	for _, e := range errs {
		if !strings.Contains(e.Message, "'v'") {
			t.Fatalf("E1048 must name v, got %q", e.Message)
		}
	}
}

// 初始化的 receiver 递归后不误报（let x = llm(...) 初始化,字段访问合法）。
func TestDefiniteInitInitializedReceiverOK(t *testing.T) {
	ctx := runDefiniteInit(t, `
        function main(string prompt) -> string {
            let r = llm("openai/gpt-4o", {"messages": [{"role": "user", "content": prompt}]});
            return r.text;
        }`)
	if errs := uninitializedErrors(ctx); len(errs) != 0 {
		t.Fatalf("initialized receiver must not trigger E1048: %v", errs)
	}
}

func TestDefiniteInitOptionalExplicitNullIsInitialized(t *testing.T) {
	ctx := runDefiniteInit(t, `function main() -> string? {
        string? value;
        value = null;
        return value;
    }`)
	if errs := uninitializedErrors(ctx); len(errs) != 0 {
		t.Fatalf("explicit null initializes Optional value: %v", errs)
	}
}

func TestDefiniteInitOptionalBareDeclarationStillErrors(t *testing.T) {
	ctx := runDefiniteInit(t, `function main() -> string? {
        string? value;
        return value;
    }`)
	errs := uninitializedErrors(ctx)
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "'value'") {
		t.Fatalf("bare Optional declaration must still trigger E1048: %v", ctx.Diagnostics)
	}
}

// Classic control-flow merge semantics: a typed hoisted variable assigned on
// both sides of an exhaustive if/else is definitely assigned after the join.
// It must not require a dummy zero initializer in source text.
func TestDefiniteInitIfElseBothBranchesAssignOuterVariable(t *testing.T) {
	ctx := runDefiniteInit(t, `
        function main(bool choose) -> string {
            string query;
            if (choose == true) {
                query = "yes";
            } else {
                query = "no";
            }
            return query;
        }`)
	if errs := uninitializedErrors(ctx); len(errs) != 0 {
		t.Fatalf("both branch assignments must satisfy definite initialization: %v", ctx.Diagnostics)
	}
}

// The merge rule is the intersection, not last-wins: assignment on only one
// branch leaves the variable undefined on the other path.
func TestDefiniteInitIfWithoutElseRemainsUnassigned(t *testing.T) {
	ctx := runDefiniteInit(t, `
        function main(bool choose) -> string {
            string query;
            if (choose == true) {
                query = "yes";
            }
            return query;
        }`)
	errs := uninitializedErrors(ctx)
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "'query'") {
		t.Fatalf("want E1048 for one-arm assignment, got %v", ctx.Diagnostics)
	}
}

// Symbol-table semantics: a branch-local declaration shadows, but does not
// retire, the outer variable.  The join must remove only the branch symbol,
// not every variable with the same source name.
func TestDefiniteInitBranchShadowDoesNotRetireOuterVariable(t *testing.T) {
	ctx := runDefiniteInit(t, `
        function main(bool choose) -> string {
            string value = "outer";
            if (choose == true) {
                let value = "inner";
            } else {
                let value = "fallback";
            }
            return value;
        }`)
	if errs := uninitializedErrors(ctx); len(errs) != 0 {
		t.Fatalf("branch-local shadow must not retire outer definite assignment: %v", ctx.Diagnostics)
	}
}

// ── 词法可见性向下继承（2026-09-12，definite-init 嵌套作用域族）──────────

// nestedReadSrc 构造「在 depth 层嵌套块内读外层声明且未确定赋值的 v」。
func nestedReadSrc(depth int, assignBefore bool) string {
	inner := "return v;"
	for i := 1; i < depth; i++ {
		inner = "if (cond) { " + inner + " }"
	}
	pre := ""
	if assignBefore {
		pre = ` v = "a";`
	}
	return `
        function main(bool cond) -> string {
            string v;` + pre + `
            if (cond) { ` + inner + ` }
            return "z";
        }`
}

// TestDefiniteInitNestedScopesInheritVisibility 锁「可见性向下继承」：同一
// 规则在深度 1..4 的嵌套块内都成立（递归第一性），每条恰好一条 E1048。
func TestDefiniteInitNestedScopesInheritVisibility(t *testing.T) {
	for depth := 1; depth <= 4; depth++ {
		ctx := runDefiniteInit(t, nestedReadSrc(depth, false))
		errs := uninitializedErrors(ctx)
		if len(errs) != 1 {
			t.Fatalf("depth=%d: want exactly one E1048, got %v", depth, ctx.Diagnostics)
		}
		if !strings.Contains(errs[0].Message, "'v'") {
			t.Fatalf("depth=%d: E1048 must name v, got %q", depth, errs[0].Message)
		}
	}
}

// TestDefiniteInitNestedReadOfAssignedVarOK 是**假阳性护栏**：变量在进入嵌套
// 块之前已确定赋值，块内读它在任意深度都不得报 E1048。
func TestDefiniteInitNestedReadOfAssignedVarOK(t *testing.T) {
	for depth := 1; depth <= 4; depth++ {
		ctx := runDefiniteInit(t, nestedReadSrc(depth, true))
		if errs := uninitializedErrors(ctx); len(errs) != 0 {
			t.Fatalf("depth=%d: 已赋值变量的嵌套读不得报 E1048: %v", depth, errs)
		}
	}
}

// TestDefiniteInitNestedShadowIsIndependent 锁遮蔽的身份安全：内层同名声明
// 有自己的 SymbolID，内层读内层、外层读外层，互不干扰。
func TestDefiniteInitNestedShadowIsIndependent(t *testing.T) {
	// 内层遮蔽已初始化 ⇒ 0 条；外层仍可读。
	ctx := runDefiniteInit(t, `
        function main(bool cond) -> string {
            string v = "outer";
            if (cond) {
                string v = "inner";
                if (cond) { output(v); }
            }
            return v;
        }`)
	if errs := uninitializedErrors(ctx); len(errs) != 0 {
		t.Fatalf("遮蔽已初始化变量不得报 E1048: %v", errs)
	}
	// 内层遮蔽未初始化 ⇒ 恰好 1 条，且指名内层的 v（不是外层）。
	ctx = runDefiniteInit(t, `
        function main(bool cond) -> string {
            string v = "outer";
            if (cond) {
                string v;
                if (cond) { output(v); }
            }
            return v;
        }`)
	errs := uninitializedErrors(ctx)
	if len(errs) != 1 {
		t.Fatalf("want exactly one E1048 (inner v), got %v", ctx.Diagnostics)
	}
	if !strings.Contains(errs[0].Message, "'v'") {
		t.Fatalf("E1048 must name v, got %q", errs[0].Message)
	}
}

// TestDefiniteInitJoinDoesNotRetireOuterSymbols 是 B4 反例（灾难性假阳性）：
// 外层变量在两个穷尽臂里被赋值后，join 必须保留它——own 只含本块直接声明的
// 符号，不得把继承来的可见符号也当成本地符号退休掉。
func TestDefiniteInitJoinDoesNotRetireOuterSymbols(t *testing.T) {
	ctx := runDefiniteInit(t, `
        function main(bool cond) -> int {
            int x = 1;
            if (cond) { x = 2; } else { x = 3; }
            if (cond) { output(to_string(x)); }
            return x;
        }`)
	if errs := uninitializedErrors(ctx); len(errs) != 0 {
		t.Fatalf("穷尽臂赋值后 join 必须保留外层符号（不得报 E1048）: %v", errs)
	}
}

// TestDefiniteInitYieldAndOutputRHSChecked 锁语句覆盖：yield/output 的 RHS
// 是表达式，未初始化读必须报 E1048（此前这两个语句种类根本没被访问）。
func TestDefiniteInitYieldAndOutputRHSChecked(t *testing.T) {
	for name, src := range map[string]string{
		"yield":  `function main() { string x; let r = for (it in [1, 2]) { yield to_string(it) + x; }; }`,
		"output": `function main() { string x; output(x); }`,
	} {
		ctx := runDefiniteInit(t, src)
		if errs := uninitializedErrors(ctx); len(errs) == 0 {
			t.Fatalf("%s 的 RHS 未初始化读必须报 E1048，实得 %v", name, ctx.Diagnostics)
		}
	}
}

// ── 终止臂不参与 junction 交集（D-P18-1 ① / D-P19-1 ① · 2026-09-27）─────
//
// 权威依据 = 标准 LANGUAGE-REFERENCE.md §3.1 原文「`T v;` …… **所有可达路径**
// 上都必须先赋值、再读，否则报 E1048」。以 `return` 终结的臂，其路径**不可达**
// 汇聚点之后的读点 ⇒ 把它算进交集 = 要求一条不存在的路径赋值（过严的假红）。
// 判据先红后绿：改前 `if (c) { return "A"; } else { v = "B"; } return v;` 报
// E1048，改后必须不报；**但**非穷尽 / 未终结的臂仍必须报（防过宽规则）。
//
// 残余（登记，不在本刀）：全臂 terminate 后的**死代码**读点仍按保守口径检查
// （报 E1048）——这是本仓「所有语句都检查」的既有口径，不由本刀改变；精确化
// 需要另立「不可达代码」判据（当前不存在）。

func TestDefiniteInitTerminatingArmExcludedFromJoin(t *testing.T) {
	// 正侧 1：if 的 then 臂 return，else 臂赋值 ⇒ 汇聚点可达路径只有 else ⇒ 已赋值。
	ctx := runDefiniteInit(t, `
        function main(string y) -> string {
            string v;
            if (y == "a") { return "A"; } else { v = "B"; }
            return v;
        }`)
	if errs := uninitializedErrors(ctx); len(errs) != 0 {
		t.Fatalf("终止臂不得参与 junction 交集（标准 §3.1「所有可达路径」）: %v", ctx.Diagnostics)
	}
}

func TestDefiniteInitTerminatingArmExcludedFromJoinSwitch(t *testing.T) {
	// 正侧 2：switch 某臂 return，default 臂赋值 ⇒ 同上。
	ctx := runDefiniteInit(t, `
        function main(string y) -> string {
            string v;
            switch (y) {
                "a": { return "A"; }
                default: { v = "B"; }
            }
            return v;
        }`)
	if errs := uninitializedErrors(ctx); len(errs) != 0 {
		t.Fatalf("switch 终止臂同样不得参与 junction 交集: %v", ctx.Diagnostics)
	}
}

func TestDefiniteInitTerminatingArmExcludedFromJoinReversedOrder(t *testing.T) {
	// 正侧 3：把 return 放到 else 臂（形态对称，判据不得依赖臂的顺序）。
	ctx := runDefiniteInit(t, `
        function main(string y) -> string {
            string v;
            if (y == "a") { v = "B"; } else { return "A"; }
            return v;
        }`)
	if errs := uninitializedErrors(ctx); len(errs) != 0 {
		t.Fatalf("终止臂在 else 侧时同样不得参与交集: %v", ctx.Diagnostics)
	}
}

func TestDefiniteInitFallingArmStillRequiresAssignment(t *testing.T) {
	// 反例 1（防过宽）：then 臂 return，else 臂**既不 return 也不赋值** ⇒
	// else 路径可达且未赋值 ⇒ 必须报 E1048。
	ctx := runDefiniteInit(t, `
        function main(string y) -> string {
            string v;
            if (y == "a") { return "A"; } else { output("B"); }
            return v;
        }`)
	errs := uninitializedErrors(ctx)
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "'v'") {
		t.Fatalf("未赋值的落入臂仍必须报 E1048，实得 %v", ctx.Diagnostics)
	}
}

func TestDefiniteInitNoElseStillUnassigned(t *testing.T) {
	// 反例 2（防过宽）：无 else ⇒ 「条件为假」那条路总是存在且未赋值 ⇒ 报。
	ctx := runDefiniteInit(t, `
        function main(string y) -> string {
            string v;
            if (y == "a") { return "A"; }
            return v;
        }`)
	errs := uninitializedErrors(ctx)
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "'v'") {
		t.Fatalf("无 else 时条件为假路径可达，仍必须报 E1048，实得 %v", ctx.Diagnostics)
	}
}
