package compile

// null_semantics_contract_test.go — N-NULL-1 的语言级判据（三条，缺一不可）：
//
//	① 语义模型不含 undefined：读可能未赋值的变量 = **编译期**错误，
//	   不是运行期 null/undefined；
//	② 未执行分支由类型零值吸收：分支未写 ⇒ 变量保持类型零值（""/0/false），
//	   不是 nil、也不是"缺失"；
//	③ null 只在显式产生处：`return null` ⇒ 结果 nil；未执行分支**不得**
//	   以 nil 顶替类型零值。
//
// 平台侧原有判据（TestCozeBizZeroCarrierCandidateAbsorbedOnAllPaths）锁的是
// **wire 形状**（零值 relay 不得被物化成 merge 候选）；本文件锁的是同一条语义
// 在"源文本 → runtime 值"上的表现。两者互不替代。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/passes"
)

// ── ① undefined 不存在：未初始化读必须编译期拒绝 ────────────────────────────
func TestUndefinedIsNotInSemanticModel(t *testing.T) {
	const src = `@language nodecoda/1
@mode workflow

function main(bool c) -> string {
    string s;
    if (c) { s = "x"; }
    return s;
}
`
	res := passes.DefaultPipeline().Run(src, "null_undefined.ncoda", nil)
	if !res.HasErrors() {
		t.Fatal("读可能未赋值的变量必须编译期拒绝（语义模型不含 undefined），实际通过")
	}
	var msgs []string
	for _, d := range res.Errors() {
		msgs = append(msgs, d.Message)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "definitely assigned") {
		t.Fatalf("拒绝理由必须是 definite-assignment，实际：%v", msgs)
	}
}

// ── ② 未执行分支由类型零值吸收 + ③ 不得以 nil 顶替 ─────────────────────────
const nullZeroAbsorbRecord = `@language nodecoda/1
@mode workflow

type RT = { string a; int n; };

function main(bool cond) -> string {
    RT v;
    v = { "a": "", "n": 0 };
    if (cond) {
        v = { "a": "x", "n": 1 };
    }
    return v.a;
}
`

func TestUnexecutedArmAbsorbedByTypeZero(t *testing.T) {
	runConstructDiff(t, "null_zero_absorb_record", nullZeroAbsorbRecord, []diffCase{
		{"写臂：值流出", map[string]any{"cond": true}, "x"},
		{"未写臂：保持类型零值（不是 nil）", map[string]any{"cond": false}, ""},
	})
}

const nullZeroAbsorbInt = `@language nodecoda/1
@mode workflow

function main(bool cond) -> int {
    int n;
    n = 0;
    if (cond) { n = 7; }
    return n;
}
`

func TestUnexecutedArmAbsorbedByIntZero(t *testing.T) {
	runConstructDiff(t, "null_zero_absorb_int", nullZeroAbsorbInt, []diffCase{
		{"写臂", map[string]any{"cond": true}, int64(7)},
		{"未写臂：0（不是 nil）", map[string]any{"cond": false}, int64(0)},
	})
}

// ── ③ null 只在显式产生处 ─────────────────────────────────────────────────
const nullExplicit = `@language nodecoda/1
@mode workflow

function main() -> any {
    return null;
}
`

func TestNullOnlyWhereExplicit(t *testing.T) {
	runConstructDiff(t, "null_explicit", nullExplicit, []diffCase{
		{"显式 return null ⇒ nil", map[string]any{}, nil},
	})
}

// TestNullThroughAnyVariable —— known gap（删掉 Skip 即红）。
//
// 期望：显式 `x = null` 经变量返回 ⇒ nil。
// 实测：`ir: coze: unsupported construct: value expression *ast.IdentExpr`
// —— 直接 `return null` 已绿；经 any 变量中转在 IR lowering 阶段被拒。
// 归属：IR（语言仓 value-expression 物化），不是 runtime 值语义；
// 失败是 fail loud，不会产出伪 null。
func TestNullThroughAnyVariable(t *testing.T) {
	t.Skip("known gap：any x; x = null; return x; ⇒ IR 'unsupported construct: value expression *ast.IdentExpr'；直接 return null 已绿")
	const src = `@language nodecoda/1
@mode workflow

function main() -> any {
    any x;
    x = null;
    return x;
}
`
	runConstructDiff(t, "null_via_any", src, []diffCase{
		{"显式 null 经变量中转", map[string]any{}, nil},
	})
}
