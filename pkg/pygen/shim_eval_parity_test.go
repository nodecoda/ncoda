package pygen

// shim_eval_parity_test.go — **两端同解判据**：`pygen`（shim 的**生产者**，16 个
// calcBuiltinPython 内建）与 `pkg/expr.EvalShim`（shim 的**求值者**，
// rt-l3 跑的就是它）必须对同一张名册给出**同一个结果**。
//
// 为什么必须有这条判据（一手，2026-09-28）：G14 给语言补了 `float(x)` cast（编译端
// 能发），而求值侧从没实现 `float`/`int` ⇒ **半接线**。代价不是"少个功能"：672 源
// 跑批里 146 源在**运行期**报 `shim: unknown function "int"`（leg1:run-err 138 → 275），
// 而**编译端一路 SUCCEEDED**（产物里有节点、跑起来才炸）。这类缺陷靠全量跑批才发现
// 一次要 12 分钟，而它本身是**纯静态事实**（名册 × 求值器支持集），必须秒级可判。
//
// 名册 = 复用同一张表 `byteEquivCases()`（**一处定义**：内置名单册在这里、
// 字节等价在 TestLowerBuiltin_byteEquivalent、两端同解在本判据），不另立清单 ——
// 新增内建若不补 witness，本判据当场红。
//
// 断言的是**结果相等**（不是"不报错"）：覆盖 + 语义两半一起锁。

import (
	"sort"
	"strings"
	"testing"

	rtexpr "github.com/nodecoda/ncoda/pkg/expr"
	"github.com/nodecoda/ncoda/pkg/pyast"
)

// shimWitness：每个内建的**代表实参取值** + 期望结果。实参名必须与
// byteEquivCases 里该内建的 args 逐字相同（否则签名对不上 ⇒ 判据自曝）。
type shimWitness struct {
	scope map[string]any
	want  any
}

var shimWitnesses = map[string]shimWitness{
	"len":             {map[string]any{"x": "abcd"}, int64(4)},
	"trim":            {map[string]any{"s": "  a  "}, "a"},
	"join":            {map[string]any{"items": []any{"a", "b"}, "sep": ","}, "a,b"},
	"to_string":       {map[string]any{"x": int64(3)}, "3"},
	"int":             {map[string]any{"x": "764"}, int64(764)},
	"float":           {map[string]any{"x": "764"}, float64(764)},
	"replace":         {map[string]any{"s": "aba", "old": "a", "new": "X"}, "XbX"},
	"split":           {map[string]any{"s": "a,b", "delim": ","}, []any{"a", "b"}},
	"repeat":          {map[string]any{"v": "a", "n": int64(2)}, []any{"a", "a"}},
	"div":             {map[string]any{"a": int64(7), "b": int64(2)}, int64(3)},
	"concat":          {map[string]any{"a": "x", "b": "y"}, "xy"},
	"contains":        {map[string]any{"xs": []any{int64(1), int64(2)}, "x": int64(2)}, true},
	"range":           {map[string]any{"a": int64(1), "b": int64(4)}, []any{int64(1), int64(2), int64(3)}},
	"enumerate":       {map[string]any{"xs": []any{"a"}}, []any{map[string]any{"index": int64(0), "value": "a"}}},
	"flatten":         {map[string]any{"xs": []any{[]any{"a"}, []any{"b"}}}, []any{"a", "b"}},
	"first_non_empty": {map[string]any{"xs": []any{"", "   ", "c"}}, "c"},
}

func TestRuntimeEvaluatorCoversAllBuiltins(t *testing.T) {
	all := byteEquivCases()
	if len(all) == 0 {
		t.Fatal("判据空转：名册为空")
	}
	for _, c := range all {
		t.Run(c.name, func(t *testing.T) {
			w, ok := shimWitnesses[c.builtin]
			if !ok {
				t.Fatalf("名册里的 %q 没有 witness —— 新增内建必须同时补求值侧 witness（名册 = byteEquivCases，一处定义）", c.builtin)
			}
			expr, ok := LowerBuiltin(c.builtin, c.args)
			if !ok {
				t.Fatalf("LowerBuiltin(%q) = false", c.builtin)
			}
			params := make([]string, 0, len(c.args))
			for _, a := range c.args {
				n, ok := a.(*pyast.NameExpr)
				if !ok {
					t.Fatalf("参数不是 NameExpr：%T", a)
				}
				if _, ok := w.scope[n.Name]; !ok {
					t.Fatalf("witness 缺 %q 的取值（args 名变了要同步 witness）", n.Name)
				}
				params = append(params, n.Name)
			}
			src := "def main(" + strings.Join(params, ", ") + ") -> dict:\n" +
				"    return {\"result\": " + pyast.SerializeExpr(expr) + "}\n"
			got, err := rtexpr.EvalShim(src, w.scope)
			if err != nil {
				t.Fatalf("求值器不覆盖 %s（shim 生产端能发、求值端算不了 = 半接线）：\n%s\n%v", c.builtin, src, err)
			}
			if len(got) != 1 {
				t.Fatalf("返回 %v，期望单键 dict", got)
			}
			if !deepEqualAny(got["result"], w.want) {
				t.Fatalf("%s 两端不同解：求值得 %#v，逐字语义期望 %#v", c.builtin, got["result"], w.want)
			}
		})
	}
	// witness 里不得有名册外的孤儿（改名/删除内建时逼你同步）。
	names := make([]string, 0, len(all))
	for _, c := range all {
		names = append(names, c.builtin)
	}
	sort.Strings(names)
	extra := []string{}
	for k := range shimWitnesses {
		if !containsStr(names, k) {
			extra = append(extra, k)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		t.Fatalf("witness 有名册外的孤儿：%v（名册 = %v）", extra, names)
	}
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// deepEqualAny 是本地最小深比较（避免为判据引入依赖）：覆盖本判据用到的
// int64/float64/bool/string/[]any/map[string]any。
func deepEqualAny(a, b any) bool {
	switch bv := b.(type) {
	case []any:
		av, ok := a.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !deepEqualAny(av[i], bv[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		av, ok := a.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range bv {
			if !deepEqualAny(av[k], v) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}
