package ir_test

// D2 family4（96 专项族 4 首样例 X6，2026-09-09，TDD RED 锁）：
// emitIf 多记录汇聚走廊（len(aggPlans) > 1 早退路径）漏规划跨同交汇点逃逸的
// 标量 hoisted 变量 → varToOutput[agg] 滞留 if 前零值绑定、下游 return 直引
// 常量零 shim、分支内真 producer 成孤儿（B2 语义丢失：X6 字数输出变恒 ""）。
//
// 单记录/零记录路径早已按「汇聚单元不变量」（record 字段组 + 标量逐变量组
// 并存、record 组不得掐死标量通道）实现（V1/V2/V6）；corridor 是该规则的
// 第二落点（路径分裂例外）。F1 = corridor + 不逃逸 record；F2 = corridor +
// 真逃逸 record（排除「过近似规划」干扰）；F3 = corridor 嵌外层 if 臂
// （递归自洽：无深度守卫）。修复前全红。

import (
	"testing"
)

// family4CorridorSrc：2 个 record 在 then 臂被写（不逃逸，仅触发 corridor），
// 标量 agg 双臂写 + post-if 读。corridor 触发阈 = 规划出的 record plan ≥ 2。
const family4CorridorSrc = `@language nodecoda/1
@mode workflow
type RT1 = {
  string f0;
};
function main(bool cond) {
    RT1 v1;
    v1 = { "f0": "" };
    RT1 v2;
    v2 = { "f0": "" };
    string agg;
    agg = "";
    if (cond) {
        v1 = { "f0": "a1" };
        v2 = { "f0": "a2" };
        agg = "a";
    } else {
        agg = "b";
    }
    return { "s": agg };
}
`

// TestBuildMultiRecordCorridorKeepsScalarConvergence: corridor（record plan ≥2）
// 上标量 agg 的逐变量汇聚组必须存在且双臂候选齐全（语义保真：else 真 producer
// 不得被丢成零值/常量）。
func TestBuildMultiRecordCorridorKeepsScalarConvergence(t *testing.T) {
	g := buildIR(t, family4CorridorSrc)
	as := collectAggregates(t, g.Root)
	var aggGroup *groupRefs
	for _, a := range as {
		for i := range a.Groups {
			if a.Groups[i].Name == "agg" {
				aggGroup = &groupRefs{name: "agg", n: len(a.Groups[i].Refs)}
			}
		}
	}
	if aggGroup == nil {
		t.Fatalf("corridor dropped the scalar convergence unit: no aggregate group %q among %v", "agg", groupsSummary(as))
	}
	if aggGroup.n < 2 {
		t.Fatalf("scalar unit group agg must carry BOTH branch producers (then=%q else=%q); got %d refs", "a", "b", aggGroup.n)
	}
	// 语义锚：agg 汇聚组端口必须可被下游 return 解析（varToOutput 重定向到
	// 结构 Merge 具名端口），不得滞留 if 前零值绑定。F2 锁定 record 单元本身
	// 不回归（corridor 的 record merge 存活/逃逸形态）。
}

// family4CorridorTrueEscapeSrc：v1/v2 双臂写 + post-if 真逃逸（读 v1.f0/v2.f0），
// 标量 agg 双臂写 + post-if 读。排除「不逃逸 record 过近似规划」干扰——即使
// record 全为真逃逸（各自 merge 存活），corridor 也不得丢标量。
const family4CorridorTrueEscapeSrc = `@language nodecoda/1
@mode workflow
type RT1 = {
  string f0;
};
function main(bool cond) {
    RT1 v1;
    v1 = { "f0": "" };
    RT1 v2;
    v2 = { "f0": "" };
    string agg;
    agg = "";
    if (cond) {
        v1 = { "f0": "a1" };
        v2 = { "f0": "a2" };
        agg = "a";
    } else {
        v1 = { "f0": "b1" };
        v2 = { "f0": "b2" };
        agg = "b";
    }
    return { "x": v1.f0, "y": v2.f0, "s": agg };
}
`

func TestBuildMultiRecordCorridorTrueEscapeKeepsScalar(t *testing.T) {
	g := buildIR(t, family4CorridorTrueEscapeSrc)
	as := collectAggregates(t, g.Root)
	// 迁移记录（2026-09-24，记录整值折叠 R-43 §42.3 A 轴）：`RT1{string f0}` 是
	// 单叶字段记录且整值语义不可观测（只 `v1.f0` / `v2.f0` 成员读 + 只字面量写）
	// ⇒ 两个绑定各折成一个**叶单元**，单元名 = 源变量名（`v1` / `v2`，C 轴档位）。
	// 断言对象逐字不变：**两个逃逸记录必须各自存活一个独立单元**（不得被并成一个），
	// 且该单元双臂候选齐全；「单元落在几张 merge 上」属 B 轴（§42.7-1），本测试不管。
	unitAggs := 0
	var aggOnFirst bool
	for _, a := range as {
		names := groupNames(a)
		if names["v1"] {
			unitAggs++
		}
		if names["v2"] {
			unitAggs++
		}
		if names["agg"] {
			aggOnFirst = true
			for i := range a.Groups {
				if a.Groups[i].Name == "agg" && len(a.Groups[i].Refs) < 2 {
					t.Fatalf("scalar unit group agg must carry both branch producers; got %#v", a.Groups[i])
				}
			}
		}
	}
	if unitAggs < 2 {
		t.Fatalf("two escaping records must keep their own convergence units (unit name = source variable, C 轴); aggregates = %v", groupsSummary(as))
	}
	if !aggOnFirst {
		t.Fatalf("corridor with truly escaping records still dropped the scalar unit; aggregates = %v", groupsSummary(as))
	}
}

// family4CorridorNestedSrc：corridor 交汇点嵌进外层 if 的 then 臂（一层嵌套）。
// 规则不得有深度守卫——内层 corridor 的 record + 标量单元必须同样并存。
const family4CorridorNestedSrc = `@language nodecoda/1
@mode workflow
type RT1 = {
  string f0;
};
function main(bool c1, bool c2) {
    if (c1) {
        RT1 v1;
        v1 = { "f0": "" };
        RT1 v2;
        v2 = { "f0": "" };
        string agg;
        agg = "";
        if (c2) {
            v1 = { "f0": "a1" };
            v2 = { "f0": "a2" };
            agg = "a";
        } else {
            agg = "b";
        }
        return { "s": agg };
    }
    return { "s": "" };
}
`

func TestBuildNestedMultiRecordCorridorRecursive(t *testing.T) {
	g := buildIR(t, family4CorridorNestedSrc)
	as := collectAggregates(t, g.Root)
	var aggRefs int
	for _, a := range as {
		for i := range a.Groups {
			if a.Groups[i].Name == "agg" {
				aggRefs = len(a.Groups[i].Refs)
			}
		}
	}
	if aggRefs == 0 {
		t.Fatalf("nested corridor dropped the scalar unit (rule must be depth-closed); aggregates = %v", groupsSummary(as))
	}
	if aggRefs < 2 {
		t.Fatalf("nested corridor scalar group agg must carry both branch producers; got %d refs", aggRefs)
	}
}

type groupRefs struct {
	name string
	n    int
}
