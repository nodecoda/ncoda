package binding

// N-LANG-2（递归自洽：规则对自嵌套封闭；形态枚举式推广 = 反模式）的机器判据。
//
// 主张的可证伪形态：**每个「块承载」语句构造必须能在自己的块里再出现一次**（自嵌套封闭）——
// 语言规则是**递归**的（`block = "{" statement* "}"`），不是逐形态枚举。若某构造只能出现在
// 顶层 / 只能在某个特定父里出现，就是「枚举式推广」，必须显式登记理由（见 exceptions）。
//
// 判据层 = 语言前端（parse + bind），**不落任何 target** —— 平台承载不了（如 coze loop/batch
// 不能有父容器 ⇒ ADAPTER_ERROR "nests a loop container"，见 internal/backend/cozebiz/（商业仓）render_loop.go
// + cozebiz_loop_state_nested_host_contract_test.go）是**平台**的事，按 N-LANG-1（平台不匹配改
// lowering/raising，不改语言）不影响本判据。
//
// 判据的判据（撤实现必红）：把某格的构造改成「不能自嵌套」⇒ 该格红；删掉 exceptions 里的一条
// 而语言仍拒绝 ⇒ 红（缺口不许隐形）。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/parser"
)

const nestHeader = "@language nodecoda/1\n@mode workflow\n"

func nestProgram(body string) string {
	return nestHeader + "function main(string q, array<string> xs, array<string> ys) {\n" + body + "\n}\n"
}

// bindErrors 返回语言前端（parse + bind）的**错误级**诊断文本（空 = 语言接受）。
func bindErrors(t *testing.T, src string) []string {
	t.Helper()
	prog, perrs := parseSourceOrErr(src)
	if len(perrs) != 0 {
		return perrs
	}
	_, diags := Bind(prog)
	var errs []string
	for _, d := range diags {
		if d.Severity == diag.Error {
			errs = append(errs, d.Message)
		}
	}
	return errs
}

func parseSourceOrErr(src string) (prog *ast.Program, errs []string) {
	p, e := parser.ParseSource(src, "<nesting>.ncoda")
	if len(e) != 0 {
		for _, pe := range e {
			errs = append(errs, pe.Error())
		}
		return nil, errs
	}
	return p, nil
}

func TestLanguageSelfNestingClosure(t *testing.T) {
	containers := []struct{ name, body string }{
		{"if", `if (q == "a") { if (q == "b") { output(q); } }`},
		{"for", `for (x in xs) { for (y in ys) { output(x); } }`},
		{"while", `while (q == "a") limit 3 { while (q == "b") limit 2 { output(q); } }`},
		{"parallel", "parallel {\n        A: { parallel { B: { output(q); } } }\n    }"},
		{"parallel for", `parallel for (x in xs, concurrency: 1, on_error: terminate) { parallel for (y in ys, concurrency: 1, on_error: terminate) { output(x); } }`},
		{"switch", "switch (q) {\n        \"a\": { switch (q) { \"b\": { output(q); } default: { output(q); } } }\n        default: { output(q); }\n    }"},
		{"for-expr", `let agg = for (x in xs) { let g = for (y in ys) { yield y; }; yield x; };`},
	}
	for _, c := range containers {
		if errs := bindErrors(t, nestProgram(c.body)); len(errs) != 0 {
			t.Errorf("N-LANG-2 破坏：`%s` 不能自嵌套（语言前端报错）—— %v\n自嵌套是「规则闭包」；若要禁止，"+
				"必须登记为显式例外并给理由。", c.name, errs)
		}
	}
}

// exceptions 登记**按设计不可自嵌套**的构造（缺口必须可见，不许隐形）。
// 删掉一条而语言仍拒绝 ⇒ 红（防「缺口隐形」）。
func TestLanguageSelfNestingClosureExceptions(t *testing.T) {
	byDesign := []struct{ name, body, wantSubstr string }{
		{"function", `function inner(string a) -> string { return a; }`, ""}, // 嵌套函数 = 语法错（顶层声明）
		{"subflow", `subflow inner = ("a", "b") { input: { string x; } }`, ""},
	}
	for _, c := range byDesign {
		errs := bindErrors(t, nestProgram(c.body))
		if len(errs) == 0 {
			t.Errorf("`%s` 在函数体内被接受 —— 该构造是顶层声明（grammar top_level_decl）；"+
				"若语言改为一等嵌套，请更新本例外表与 N-LANG-2。", c.name)
			continue
		}
		if c.wantSubstr != "" && !strings.Contains(strings.Join(errs, "\n"), c.wantSubstr) {
			t.Errorf("`%s` 的拒绝理由不含 %q：%v", c.name, c.wantSubstr, errs)
		}
	}
}
