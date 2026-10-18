// List-op 参数位的**静态性**契约（2026-09-29，D-P19-1 ③ 一手发现）。
//
// 规则（一条）：`take` / `sort` / `sort_by` 的**参数位是静态位** —— 平台载体
// （`renderListOpNode` 生成的 python 源码）把限额 / 方向 / 键**内联成字面量**，
// 动态值没有承载面 ⇒ 必须 **fail loud**。**静默落默认值**（`op.Limit` 保持 0）
// 会让作者写的前 k 项变成 `items[:0]`（空数组）、写的降序变成升序 —— 撞
// 「失配 ≠ 静默丢弃」不变量。
//
// 同族既有先例（同一条口径）：`filter value` / `filter target` /
// `filter lambda body`（`filter` 的谓词与值位）同样走 `UnsupportedError`。
//
// 为什么「非字面量 = 真动态值」：`const K = 3;` 在**前端**已替换成字面量
// （下面的正向锚 `const_limit_is_substituted_before_ir` 钉住这条机制），
// 所以到 IR 构建期仍不是字面量的，只能是运行期才知道的绑定。
//
// **撤实现必红**：把 `take` 位的 `if !ok { return … }` 删掉（回到「不是字面量
// 就跳过赋值」）⇒ 三条负向格当场红（编译变成功 + `items[:0]`）。
package ir_test

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

const listOpPrefix = workflowHeader + "function main(string csv, int k) {\n    let items = split(csv, \",\");\n"

// ── 正向锚：字面量与前端常量替换仍照常承载 ────────────────────────────

func TestListOpStaticParamLiteralLimit(t *testing.T) {
	g := buildIR(t, listOpPrefix+"    let sel = take(items, 3);\n    output(sel);\n}\n")
	op := firstOf(t, g, ir.KindListOp).(*ir.ListOp)
	if op.Operation != "take" || op.Limit != 3 {
		t.Fatalf("take = %q limit %d, want take 3", op.Operation, op.Limit)
	}
}

func TestListOpStaticParamConstLimitIsSubstitutedBeforeIR(t *testing.T) {
	// `const` 替换发生在前端（AST 级）⇒ 到 IR 构建期它已经是 `*ast.IntLit`。
	src := workflowHeader + "const K = 3;\n" +
		"function main(string csv) {\n    let items = split(csv, \",\");\n    let sel = take(items, K);\n    output(sel);\n}\n"
	g := buildIR(t, src)
	op := firstOf(t, g, ir.KindListOp).(*ir.ListOp)
	if op.Limit != 3 {
		t.Fatalf("const 替换后的 take limit = %d, want 3（非字面量才是真动态值）", op.Limit)
	}
}

// ── 负向锚：动态值必须拒收（旧行为 = 静默落默认值 = 静默丢值） ──────────

func TestListOpStaticParamDynamicTakeLimitRejected(t *testing.T) {
	got := buildIRErr(t, listOpPrefix+"    let sel = take(items, k);\n    output(sel);\n}\n")
	if !strings.Contains(got, "take limit") {
		t.Fatalf("dynamic take limit err = %q, want it to name the `take limit` position", got)
	}
}

// `sort` / `sort_by` 今天在 IR 层就被整条拒收（`listOpNames` 不含它们，
// `build_call.go` 判 `call "sort" in let`）⇒ 其参数位**没有**静默默认值的风险面。
// 本格钉住「拒收是响的」这一事实；`emitListOp` 里 sort/sort_by 位的 fail loud 判据
// 是**对称保留**（接线 sort 时即刻生效，不需要重新发明）。
func TestListOpSortFamilyRejectedLoudToday(t *testing.T) {
	src := workflowHeader + "function main(string csv) {\n    let items = split(csv, \",\");\n" +
		"    let sel = sort(items, \"desc\");\n    output(sel);\n}\n"
	got := buildIRErr(t, src)
	if !strings.Contains(got, "sort") {
		t.Fatalf("sort call err = %q, want a loud rejection naming `sort`", got)
	}
}
