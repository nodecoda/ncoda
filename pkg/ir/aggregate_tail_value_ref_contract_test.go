package ir

import "testing"

// aggregate_tail_value_ref_contract_test.go —— 尾值判据**一处定义**的行为面
// （N1 收口 · 2026-09-23）。
//
// 规则（一条，无形态守卫）：「零候选 junction（`Groups` 与 `Refs` 皆空）**没有值**」
// ——「没有值」不是「零值的值」；具名组形态（`Groups > 0`）同样不带尾值（平台 merge
// 只声明各组成员口，不声明合成 `Group1`）。唯一有值的形态是 legacy 单值通道
// （`Groups==0 && Refs>0`），口名 = 汇聚单元身份（`SingleGroupName()`）。
//
// 判据为什么需要：这条规则**包外**（渲染器 / 后端）也要用 —— attempt flow-tail
// 绑 `end.node_inputs` 就是一个消费点。2026-09-23 前它手写 `len(Exit.Groups) == 0`，
// 与统一判据 `junctionTailRef` **不同形**（缺 `Refs > 0`）⇒ 同一规则两份判据。
// 本判据锁的是**唯一出口** `Aggregate.TailValueRef()` 的行为；「包外不许重判」
// 由 `internal/backend/cozebiz` 的同名单一来源判据锁（两半各自可红）。

// TestAggregateTailValueRefContract 三种形态 + nil 聚合。
func TestAggregateTailValueRefContract(t *testing.T) {
	cases := []struct {
		name    string
		agg     *Aggregate
		wantNil bool
		wantOut string
	}{
		{
			name:    "nil_aggregate_has_no_value",
			agg:     nil,
			wantNil: true,
		},
		{
			name:    "zero_candidate_has_no_value",
			agg:     &Aggregate{ID: "100005"},
			wantNil: true,
		},
		{
			name:    "named_groups_carry_no_tail_value",
			agg:     &Aggregate{ID: "100005", Groups: []AggregateGroup{{Name: "v", Refs: []Ref{{Producer: "100003", Output: "body"}}}}},
			wantNil: true,
		},
		{
			name:    "legacy_single_group_carries_identity_port",
			agg:     &Aggregate{ID: "100005", RefName: "v", Refs: []Ref{{Producer: "100003", Output: "body"}}},
			wantOut: "v",
		},
		{
			name:    "legacy_single_group_without_identity_falls_back_to_platform_spelling",
			agg:     &Aggregate{ID: "100005", Refs: []Ref{{Producer: "100003", Output: "body"}}},
			wantOut: wGroup1,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := tc.agg.TailValueRef()
			if tc.wantNil {
				if got != nil {
					t.Fatalf("TailValueRef() = {%s.%s}, want nil（这条规则：没有值 ≠ 零值的值）", got.Producer, got.Output)
				}
				return
			}
			if got == nil {
				t.Fatalf("TailValueRef() = nil, want {%s.%s}", tc.agg.ID, tc.wantOut)
			}
			if got.Producer != tc.agg.ID || got.Output != tc.wantOut {
				t.Fatalf("TailValueRef() = {%s.%s}, want {%s.%s}", got.Producer, got.Output, tc.agg.ID, tc.wantOut)
			}
		})
	}
}

// TestAggregateTailValueRefContractDiscriminates 判据自检（**锁两半**）：
// 被收口掉的那份错判据（只问 `len(Groups) == 0`、不问 `Refs>0`）必须在本输入上
// 给出**不同**答案 —— 否则上面的「零候选 ⇒ nil」可能只是输入挑不出来（判据恒真）。
// 同时校验统一判据与内部形态 `junctionTailRef` 同源（不是两份实现）。
func TestAggregateTailValueRefContractDiscriminates(t *testing.T) {
	zero := &Aggregate{ID: "100005"}
	legacy := &Aggregate{ID: "100006", RefName: "v", Refs: []Ref{{Producer: "100003", Output: "body"}}}

	// 错判据的镜像（N1 收口前 render_stmt.go 的原样写法）。
	oldPredicate := func(a *Aggregate) bool { return len(a.Groups) == 0 }

	if !oldPredicate(zero) {
		t.Fatalf("自检失效：错判据在零候选输入上不成立 ⇒ 本判据挑不出它（输入不判别）")
	}
	if zero.TailValueRef() != nil {
		t.Fatalf("自检失效：统一判据在零候选输入上给了值")
	}
	if oldPredicate(zero) == (zero.TailValueRef() != nil) {
		t.Fatalf("自检失效：统一判据与错判据在零候选输入上同解 ⇒ 判据不判别该差异")
	}

	// 同源：对外方法必须与内部形态同解（不是第二份实现）。
	for _, agg := range []*Aggregate{nil, zero, legacy, {ID: "100007", Groups: []AggregateGroup{{Name: "v"}}}} {
		inner := junctionTailRef(agg)
		outer := agg.TailValueRef()
		if (inner == nil) != (outer == nil) {
			t.Fatalf("对外方法与内部判据不同解：inner=%v outer=%v", inner, outer)
		}
		if inner != nil && (inner.nodeID != outer.Producer || inner.output != outer.Output) {
			t.Fatalf("对外方法与内部判据不同源：inner={%s.%s} outer={%s.%s}",
				inner.nodeID, inner.output, outer.Producer, outer.Output)
		}
	}
}
