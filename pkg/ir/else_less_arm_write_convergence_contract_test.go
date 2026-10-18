package ir_test

// RED C/D（merge 专项，2026-09-08）：else-less 单臂写汇聚缺失（数据丢失）。
// 种子实证：`if (cond) { agg = "x"; } use(agg);`（live then 臂写 hoisted 变量、
// 无 else）——旧 emitIf 把 if 当 statement-only 走 pendingElse 早退路径，
// 汇聚机制根本没走到 → 分支写变孤儿、post-if 读绑 pre-if 零值（数据丢失）。
// 标量与 record 探针实证同一断点（junction 上 0 Aggregate）= if 翻译断点，
// 非 record 语义问题。统一规则：凡 if junction 上某臂末绑定 ≠ if 前绑定，
// 就必须建 phi 汇聚单元。本测试锁定：else-less live 臂单臂写的交汇点必须
// 存在汇聚单元（标量与 record 双形态），且不得依赖「仅当双臂」的守卫。

import "testing"

// elseLessScalarSrc：else-less 标量单臂写（then 臂写 agg，false 路径走
// pre-if 绑定 ""）。
const elseLessScalarSrc = `@language nodecoda/1
@mode workflow
function main(bool cond) -> string {
    string agg;
    agg = "";
    if (cond) {
        agg = "then-scalar";
    }
    return agg;
}
`

// elseLessRecordSrc：else-less record 单臂整记录写（then 臂写 rec，false
// 路径走 pre-if 零值 record）。
const elseLessRecordSrc = `@language nodecoda/1
@mode workflow
type RT1 = {
  string a;
  string b;
};
function main(bool cond) -> RT1 {
    RT1 rec;
    rec = { "a": "", "b": "" };
    if (cond) {
        rec = { "a": "then-a", "b": "then-b" };
    }
    return rec;
}
`

func TestElseLessSingleArmWriteConvergesScalar(t *testing.T) {
	g := buildIR(t, elseLessScalarSrc)
	as := collectAggregates(t, g.Root)
	if len(as) == 0 {
		t.Fatalf("else-less scalar arm write lost: no convergence unit at junction")
	}
}

func TestElseLessSingleArmWriteConvergesRecord(t *testing.T) {
	g := buildIR(t, elseLessRecordSrc)
	as := collectAggregates(t, g.Root)
	if len(as) == 0 {
		t.Fatalf("else-less record arm write lost: no convergence unit at junction")
	}
}
