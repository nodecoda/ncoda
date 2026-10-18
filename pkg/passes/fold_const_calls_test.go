package passes

// fold_const_calls_test.go —— 常量调用折叠的判据（R-146，2026-09-29）。
//
// 规则（一句话）：`int(<数字串字面量>)` / `float(<数字串字面量>)` **编译期算完**
// ⇒ 折成数值字面量；**有损 / 非数字 / 不可表示不折**（不发明值，原样留给运行期）。
//
// 为什么（一手）：coze-biz 的参数槽只接「变量引用或字面量」⇒ 不折的 `int("0")`
// 必须先物化一个 code shim 节点（`let vX = int("0");`）才能绑进槽位；折了则
// **零节点**（不可变绑定的标量字面量走 IR 的 litVars 常量折叠，直接内联进槽位）。
// 判据本体 = `NumericStringLiteralValue`（与反编译侧参数边界的常量归一同一函数）。
//
// 撤实现必红：删掉 `runPipeline` 里的 `FoldConstCalls(program)` ⇒ 折叠组全红
// （拿到的是 `*ast.CallExpr`，不是字面量）。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/parser"
)

// foldedInit 解析 `let m = <expr>;`（带一个 const 声明，把「代入后再折叠」这条
// 通路也覆盖进来），跑 FoldConstCalls，返回 init 的折叠结果。
func foldedInit(t *testing.T, expr string) ast.Expr {
	t.Helper()
	src := "@language nodecoda/1\n@mode workflow\nconst Z = \"0\";\n" +
		"function main(int n = 0) -> int {\n    let m = " + expr + ";\n    return n;\n}\n"
	prog, errs := parser.ParseSource(src, "fold_const_calls.ncoda")
	if len(errs) > 0 {
		t.Fatalf("parse %s: %v", expr, errs)
	}
	// 与 runPipeline 同一顺序：先代入常量、再折叠（`const Z = "0"; int(Z)` 靠这一步）。
	SubstituteConstants(prog)
	FoldConstCalls(prog)
	// `function main` 即入口：它在 Program.Entry()，不在 Program.Functions()。
	fn := prog.Entry()
	if fn == nil {
		t.Fatalf("无入口函数（%s）", expr)
	}
	stmts := fn.Body().Stmts()
	if len(stmts) == 0 {
		t.Fatalf("函数体为空（%s）", expr)
	}
	vd, ok := stmts[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("首语句应为 VarDeclStmt，得到 %T（%s）", stmts[0], expr)
	}
	return vd.Init()
}

// ① 无损数字串 ⇒ 折成数值字面量（含整数字符串落 float 位、`"0.0"` 落 int 位）。
func TestFoldConstCallsFoldsLosslessNumericStrings(t *testing.T) {
	cases := []struct {
		expr string
		kind string
		num  float64
	}{
		{`int("0")`, "int", 0},
		{`int("007")`, "int", 7},
		{`int("-3")`, "int", -3},
		{`int("0.0")`, "int", 0},
		{`float("1")`, "float", 1},
		{`float("0.5")`, "float", 0.5},
		{`float("-2.25")`, "float", -2.25},
		// 常量代入先行：`const Z = "0"` ⇒ `int(Z)` 代入后就是 `int("0")`，必须折。
		{`int(Z)`, "int", 0},
	}
	for _, c := range cases {
		init := foldedInit(t, c.expr)
		switch lit := init.(type) {
		case *ast.IntLit:
			if c.kind != "int" || float64(lit.Value()) != c.num {
				t.Errorf("%s 折叠结果 = int(%v)，期望 int %v", c.expr, lit.Value(), c.num)
			}
		case *ast.FloatLit:
			if c.kind != "float" || lit.Value() != c.num {
				t.Errorf("%s 折叠结果 = float(%v)，期望 float %v", c.expr, lit.Value(), c.num)
			}
		default:
			t.Errorf("%s 未折叠为数值字面量，得到 %T", c.expr, init)
		}
	}
}

// ② 有损 / 非数字 / 不可表示 ⇒ **不折**（原样留给运行期；不得发明值）。
func TestFoldConstCallsLeavesLossyAndNonNumericAlone(t *testing.T) {
	for _, expr := range []string{
		`int("0.5")`,                  // 有损：python int("0.5") 是 ValueError，折成 0 就是发明
		`int("abc")`,                  // 非数字
		`int("1e400")`,                // 不可表示（+Inf）
		`int("99999999999999999999")`, // 超 int64
		`float("abc")`,
	} {
		init := foldedInit(t, expr)
		if _, ok := init.(*ast.CallExpr); !ok {
			t.Errorf("%s 被折叠成 %T —— 有损/非数字/不可表示必须原样承载", expr, init)
		}
	}
}

// ③ 只折**语言内建** int/float：限定名调用（`json.stringify`）与其它内建不动。
func TestFoldConstCallsLeavesOtherCallsAlone(t *testing.T) {
	for _, expr := range []string{`trim("0")`, `json.stringify("0")`, `p.op("0")`} {
		init := foldedInit(t, expr)
		if _, ok := init.(*ast.CallExpr); !ok {
			t.Errorf("%s 不该被折叠（不是内建 int/float），得到 %T", expr, init)
		}
	}
}

// ④ 接线：折叠必须在**管线里**跑（`runPipeline` 里 SubstituteConstants 之后那次
// 调用）。①②③ 直接调 `FoldConstCalls` ⇒ 只证明函数对，**不证明接线在**；
// 撤掉管线里那次调用 ⇒ 只有本条红（假安全感的正是「判据测了函数、没测接线」）。
func TestFoldConstCallsIsWiredInPipeline(t *testing.T) {
	src := "@language nodecoda/1\n@mode workflow\n" +
		"function main(int n = 0) -> int {\n    let m = int(\"0\");\n    return m;\n}\n"
	res := DefaultPipeline().Run(src, "fold_wired.ncoda", nil)
	if res.HasErrors() {
		t.Fatalf("管线报错：%v", res.Diagnostics)
	}
	fn := res.Program.Program.Entry()
	if fn == nil {
		t.Fatal("无入口函数")
	}
	stmts := fn.Body().Stmts()
	vd, ok := stmts[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("首语句应为 VarDeclStmt，得到 %T", stmts[0])
	}
	if _, ok := vd.Init().(*ast.IntLit); !ok {
		t.Fatalf(`管线里没折叠（int("0") 仍是 %T）—— FoldConstCalls 没接在 runPipeline 上`, vd.Init())
	}
}
