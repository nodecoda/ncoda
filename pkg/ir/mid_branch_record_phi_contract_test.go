package ir_test

// RED A（merge 专项，2026-09-08）：mid-branch record 汇聚缺失（数据丢失）。
// 种子实证（merge_roundtrip_matrix_test.go seed-1 同类）：hoisted record 变量
// rec 在 if 双臂**中段**（非分支尾）整记录赋值、分支尾是标量赋值时——
// emitIf 的 record 汇聚规划（aggPlans）只认 tail-hoisted 名字，标量侧早有
// changedHoistedScalars 处理中段、record 侧缺同款 → rec 无逐字段汇聚组 →
// 分支内真 producer 成孤儿（YAML 保留节点但零消费）、post-if `rec.f0` 读到
// if 前零值绑定（数据丢失）。本测试锁定：中段 record 写 + 分支尾标量写的
// 交汇点必须同时存在 record 字段组（f0，双臂候选齐全）与标量组/legacy 通道
// （scalar 不丢），且 rec 分支写入不产生孤儿（每组 ≥2 候选 = 双臂 producer
// 直引，而非单臂 + 零值默认）。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

// midBranchRecordSrc：rec 双臂整记录赋值（中段），agg 双臂标量赋值（分支尾），
// post-if 字段读 rec.f0 + 标量读 agg。与 merge_roundtrip_matrix seed-1 同构。
const midBranchRecordSrc = `@language nodecoda/1
@mode workflow
type RT1 = {
  string f0;
};
function main(bool cond) {
    RT1 rec;
    rec = { "f0": "" };
    string agg;
    agg = "";
    if (cond) {
        rec = { "f0": "then-f0" };
        agg = "then-scalar";
    } else {
        rec = { "f0": "else-f0" };
        agg = "else-scalar";
    }
    return { "f0": rec.f0, "s": agg };
}
`

// 迁移记录（2026-09-24，记录整值折叠 R-43 §42.3 A 轴）：`RT1{string f0}` 是
// **单叶字段**记录，且整值语义不可观察（只被 `rec.f0` 成员读、只被记录字面量写）
// ⇒ 折叠把绑定折成**叶单元**，组名按 C 轴档位取**源变量名**（`rec`）而不是字段名
// （`f0`）。断言的对象（「两条臂的生产者都必须进候选」= 数据丢失守卫）逐字不变，
// 变的只是单元名的档位 —— 正是 §42.3 C 轴「单元名 = 源变量名」。
func TestBuildMidBranchRecordEscapeConverges(t *testing.T) {
	g := buildIR(t, midBranchRecordSrc)
	as := collectAggregates(t, g.Root)
	// record（已折成叶单元）= 源变量名组 `rec` 必须存在（双臂中段写 → 汇聚）。
	var recGroup *ir.AggregateGroup
	var scalarOK bool
	for _, a := range as {
		for i := range a.Groups {
			grp := &a.Groups[i]
			if grp.Name == "rec" {
				recGroup = grp
			}
			if grp.Name == "agg" || (len(a.Groups) == 0 && len(a.Refs) > 0) {
				scalarOK = true
			}
		}
	}
	if recGroup == nil {
		t.Fatalf("mid-branch record escape lost its convergence unit: no aggregate group %q among %v", "rec", groupsSummary(as))
	}
	if len(recGroup.Refs) < 2 {
		t.Fatalf("record unit group rec must carry BOTH branch producers (data loss = one arm bound to zero default); got %d refs: %#v", len(recGroup.Refs), recGroup)
	}
	if !scalarOK {
		t.Fatalf("scalar convergence lost alongside record unit; aggregates = %v", groupsSummary(as))
	}
}
