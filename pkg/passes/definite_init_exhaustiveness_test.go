package passes

// ─────────────────────────────────────────────────────────────
// definite-init 覆盖穷尽性门（2026-09-12，复发拦截）
//
// 复发机制（实证两次）：为了给 **某些**语句种类做检查，checkStmt/
// checkExpr 用类型 switch 逐个列举 —— 于是「新加了一种语句/表达式」时
// 它**静默逃过** definite-init（编译器不报错、规则形同虚设），直到若干族
// 之后才被别的机制发现：
//   · 第一次：`*ast.AttemptStmt` / `*ast.RequestInputStmt` 缺失 ⇒
//     臂内赋值不并入 junction（CHAIN_BREAK 族）；
//   · 第二次：`*ast.YieldStmt` / `*ast.OutputStmt` 缺失 ⇒ yield/output 的
//     RHS 里的未初始化读完全不查。
// 本门把这个机制掐掉：`internal/ast` 里每一个 `stmtNode()` / `exprNode()`
// 实现都必须在 definite-init 里被**显式**处理（或在下方给出有理由的白名单）。
// 新增 ast 节点而不更新 definite-init ⇒ 本测试红。
// ─────────────────────────────────────────────────────────────

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// leafExprs 是没有子表达式的字面量：它们不需要递归，运行时 default 分支
// 同样显式放行这五种（见 definite_init.go checkExpr 的 default）。
var leafExprs = map[string]string{
	"StringLit": "字符串字面量（叶子，无子表达式）",
	"IntLit":    "整型字面量（叶子）",
	"FloatLit":  "浮点字面量（叶子）",
	"BoolLit":   "布尔字面量（叶子）",
	"NullLit":   "null 字面量（叶子）",
}

var nodeImplRe = regexp.MustCompile(`func \(\w+ \*(\w+)\) (stmtNode|exprNode)\(\)`)

// astNodeKinds 扫描 ../ast 源码，返回 {stmtNode: {类型…}, exprNode: {类型…}}。
func astNodeKinds(t *testing.T) map[string]map[string]bool {
	t.Helper()
	dir := filepath.Join("..", "ast")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	out := map[string]map[string]bool{"stmtNode": {}, "exprNode": {}}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, m := range nodeImplRe.FindAllStringSubmatch(string(raw), -1) {
			out[m[2]][m[1]] = true
		}
	}
	if len(out["stmtNode"]) == 0 || len(out["exprNode"]) == 0 {
		t.Fatalf("扫不到 ast 节点种类（扫描器失效）：%v", out)
	}
	return out
}

// funcBody 抽取 definite_init.go 里某个方法的函数体文本（按行首 `}` 收口）。
func funcBody(t *testing.T, src, signature string) string {
	t.Helper()
	i := strings.Index(src, signature)
	if i < 0 {
		t.Fatalf("找不到 %s", signature)
	}
	rest := src[i:]
	if j := strings.Index(rest, "\n}\n"); j >= 0 {
		return rest[:j]
	}
	t.Fatalf("%s 的函数体未收口", signature)
	return ""
}

// caseClauseRe 匹配一个 case 子句（允许 `case *ast.A, *ast.B:` 多类型），
// 再用 caseTypeRe 抽出子句里的所有 ast 类型。
var caseClauseRe = regexp.MustCompile(`(?m)^\s*case (.+):\s*$`)
var caseTypeRe = regexp.MustCompile(`\*ast\.(\w+)`)

func TestDefiniteInitCoversEveryAstNodeKind(t *testing.T) {
	raw, err := os.ReadFile("definite_init.go")
	if err != nil {
		t.Fatalf("read definite_init.go: %v", err)
	}
	src := string(raw)
	kinds := astNodeKinds(t)

	stmts := funcBody(t, src, "func (d *definiteInit) checkStmt(")
	exprs := funcBody(t, src, "func (d *definiteInit) checkExpr(")

	seen := func(body string) map[string]bool {
		got := map[string]bool{}
		for _, clause := range caseClauseRe.FindAllStringSubmatch(body, -1) {
			for _, ty := range caseTypeRe.FindAllStringSubmatch(clause[1], -1) {
				got[ty[1]] = true
			}
		}
		return got
	}

	for _, kind := range []string{"stmtNode", "exprNode"} {
		handled := seen(stmts)
		if kind == "exprNode" {
			handled = seen(exprs)
		}
		var missing []string
		for name := range kinds[kind] {
			if handled[name] {
				continue
			}
			if kind == "exprNode" {
				if _, ok := leafExprs[name]; ok {
					continue
				}
			}
			missing = append(missing, name)
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Fatalf("definite-init 未处理这些 %s 类型：%v\n"+
				"新增 ast 节点必须在 checkStmt/checkExpr 里显式处理（否则它将静默逃过 E1048 检查——"+
				"这正是 attempt/ask 与 yield/output 两次漏检的复发机制）。", kind, missing)
		}
	}

	// fail-loud 默认分支必须在线：它是「将来新增节点在运行期也炸出来」的兜底。
	if !strings.Contains(stmts, "default:") {
		t.Fatalf("checkStmt 的类型 switch 缺少 fail-loud default 分支")
	}
	if !strings.Contains(exprs, "default:") {
		t.Fatalf("checkExpr 的类型 switch 缺少 fail-loud default 分支")
	}
}
