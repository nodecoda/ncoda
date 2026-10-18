// 语言特性矩阵锚点声明（docs/LANGUAGE-FEATURE-MATRIX.md）：
//@feature switch — switch 多出口分类；互斥臂写提升变量 = N 臂 phi（候选 = 臂内真实写值直引）
//@feature request-input — request_input action/timeout 臂写提升变量 = N 臂 phi
//@feature var-let — 提升变量（`T v;` 在 junction 前声明）跨互斥臂赋值的 phi 汇聚
//@feature for-while — 循环容器内 switch 的臂内赋值（loop-carried 内层 phi）

package ir_test

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

// P01 正向（lowering / IR 构建）覆盖扩展（2026-09-10）：互斥臂集合
// （switch N 臂 / request_input action 臂）与 if 双臂遵守**同一条** phi 规则——
// 「junction 前声明的提升变量在臂内被赋值 ⇒ 臂内真实写值成为汇聚候选，
//  junction 之后的下游引用解析到汇聚单元」。旧实现只把**显式 return 的臂尾值**
// 当候选，臂内赋值（无 return）完全丢失：候选空的 merge + 下游绑零值节点
// （静默数据丢失；Z78/H41/Z21 等语料 + 首编失败实证）。

// switch N 臂（含 default）臂内赋值 → 一个汇聚单元，候选 = 各臂 producer。
func TestSwitchArmWriteConvergesToPhi(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(string leibie) -> string {
    string agg;
    agg = "";
    switch (leibie) {
        "a": { agg = "A"; }
        "b": { agg = "B"; }
        default: { agg = "D"; }
    }
    return agg;
}
`
	g := buildIR(t, src)
	aggs := aggregatesUnder(g)
	if len(aggs) != 1 {
		t.Fatalf("switch junction aggregates = %d, want 1", len(aggs))
	}
	if n := len(aggs[0].AllRefs()); n != 3 {
		t.Fatalf("switch junction candidates = %d (%v), want 3 (one per arm)", n, aggs[0].AllRefs())
	}
	// junction 之后的下游引用必须解析到汇聚单元（不是臂绑定/零值节点）。
	ret := returnStmtsOf(g)
	if len(ret) == 0 || ret[0].Value == nil {
		t.Fatalf("missing return carrying the converged value")
	}
	if ret[0].Value.NodeID() != aggs[0].ID {
		t.Fatalf("post-switch binding = {%s.%s}, want the junction aggregate %s",
			ret[0].Value.NodeID(), ret[0].Value.Output(), aggs[0].ID)
	}
}

// switch **源码未写 `default:`** 时，平台端口仍在（`renderSwitch` 的 unowned
// 同一债把「源码缺 default 臂」与「空臂」并成一列）——**值侧必须同构**：
// 未写臂按语言矩阵的规则「候选 = 各臂真实写值直引 + **未写臂直引 junction 前
// 绑定**」贡献一个候选，而不是没有候选。
//
// 失效模式（一手实测，2026-09-21）：`string v; v = "x"; switch (k) { "a": { v = "y"; } }
// return v;` —— merge 组只有臂内那 1 个候选 ⇒ k≠"a" 时汇聚取零值，
// **`"x"` 静默丢失**（一轮往返后 `r1` 里 `v = "x"` 变成 `v = ""`，而同样语义的
// `if (n) { v = "y"; }`（无 else）与 `switch {… default: { }}`（显式空 default）
// 都正确保留）。判据取**关系式**：「无 default」与「显式空 default」两种拼写必须
// 同构（同一候选数），且候选数 = 臂写 + 未写臂前绑定。
func TestSwitchWithoutDefaultCarriesJunctionEntryBinding(t *testing.T) {
	const head = `@language nodecoda/1
@mode workflow
function main(string k) -> string {
    string v;
    v = "x";
    switch (k) {
        "a": { v = "y"; }
`
	tail := `    }
    return v;
}
`
	noDefault := buildIR(t, head+tail)
	emptyDefault := buildIR(t, head+"        default: { }\n"+tail)

	aggsA, aggsB := aggregatesUnder(noDefault), aggregatesUnder(emptyDefault)
	if len(aggsA) != 1 || len(aggsB) != 1 {
		t.Fatalf("junction aggregates = %d / %d, want 1 / 1", len(aggsA), len(aggsB))
	}
	gotA, gotB := len(aggsA[0].AllRefs()), len(aggsB[0].AllRefs())
	if gotA != gotB {
		t.Fatalf("「源码没写 default」与「显式空 default」不同构：候选 %d vs %d —— 未写臂必须直引 junction 前绑定（前者的值静默丢失）", gotA, gotB)
	}
	if gotA != 2 {
		t.Fatalf("候选 = %d, want 2（臂内写值 1 + 未写臂直引 junction 前绑定 1）", gotA)
	}
}

// array/list 形提升变量（Z78 形状）不得走 legacy 单标量整值通道：
// 逐变量具名组（组名 = 变量名，名字有界），候选 = 臂内真实写值直引。
func TestSwitchArmWriteListShapeUsesNamedGroup(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(string leibie) -> array<string> {
    array<string> agg;
    agg = [];
    switch (leibie) {
        "a": { agg = ["A"]; }
        default: { agg = ["B"]; }
    }
    return agg;
}
`
	g := buildIR(t, src)
	aggs := aggregatesUnder(g)
	if len(aggs) != 1 {
		t.Fatalf("switch junction aggregates = %d, want 1", len(aggs))
	}
	a := aggs[0]
	if len(a.Groups) == 0 {
		t.Fatalf("list-shaped hoist must converge through a named group, got legacy Refs=%v", a.Refs)
	}
	if a.Groups[0].Name != "agg" {
		t.Fatalf("named group = %q, want the bounded variable name %q", a.Groups[0].Name, "agg")
	}
	if n := len(a.Groups[0].Refs); n != 2 {
		t.Fatalf("named group candidates = %d (%v), want 2", n, a.Groups[0].Refs)
	}
}

// request_input 的 action/timeout 臂与 switch 臂同族：臂内赋值同样必须汇聚。
func TestRequestInputArmWriteConvergesToPhi(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
enum ReviewAction { approve, reject }
type ReviewForm = { string comment; }

function main() -> string {
    string agg;
    agg = "seed";
    ask<ReviewForm, ReviewAction>({
        content: "Review",
        fields: { comment: { kind: "paragraph" } },
        actions: {
            approve: { title: "Approve", style: "primary" },
            reject: { title: "Reject", style: "default" }
        }
    }) as review {
        action ReviewAction.approve { agg = review.comment; }
        action ReviewAction.reject { agg = "rejected"; }
    }
    return agg;
}
`
	g := buildIR(t, src)
	aggs := aggregatesUnder(g)
	if len(aggs) != 1 {
		t.Fatalf("ask junction aggregates = %d, want 1", len(aggs))
	}
	// ask 的臂集合 = 每条 action 臂 + 可选 timeout 臂（缺省 timeout 仍投影一条
	// 空 default 臂）；两条 action 臂写值 + 未写臂回落 junction 前绑定 ⇒ 3 候选。
	// **前绑定非零**（"seed"）⇒ 零值豁免不适用（见下方 zero-entry 两例）。
	refs := aggs[0].AllRefs()
	if len(refs) != 3 {
		t.Fatalf("ask junction candidates = %d (%v), want 3 (2 action arms + unwritten timeout arm)", len(refs), refs)
	}
	if refs[0].Producer == refs[2].Producer && refs[0].Output == refs[2].Output {
		t.Fatalf("candidates = %v: unwritten timeout arm must fall back to the pre-junction binding", refs)
	}
}

// 循环容器内的 switch（loop-carried 内层 phi）：臂内赋值仍是汇聚候选，且
// 循环产出绑定到汇聚单元（不是臂内/零值节点）。
func TestSwitchArmWriteInsideLoopConverges(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(array<string> items, string leibie) -> array<string> {
    let out = for (item in items) {
        string agg;
        agg = "";
        switch (leibie) {
            "a": { agg = item; }
            default: { agg = "x"; }
        }
        yield value = agg;
    };
    return out;
}
`
	g := buildIR(t, src)
	aggs := aggregatesUnder(g)
	if len(aggs) != 1 {
		t.Fatalf("loop-body switch junction aggregates = %d, want 1", len(aggs))
	}
	if n := len(aggs[0].AllRefs()); n != 2 {
		t.Fatalf("loop-body switch candidates = %d (%v), want 2", n, aggs[0].AllRefs())
	}
}

// 禁形：任何汇聚单元都不得是空候选（空组 = 静默数据丢失）。
func TestJunctionAggregatesNeverEmpty(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(string leibie, bool ok) -> string {
    string agg;
    agg = "";
    if (ok) {
        switch (leibie) {
            "a": { agg = "A"; }
            default: { agg = "D"; }
        }
    } else {
        agg = "B";
    }
    return agg;
}
`
	g := buildIR(t, src)
	aggs := aggregatesUnder(g)
	if len(aggs) == 0 {
		t.Fatal("no junction aggregate produced")
	}
	for _, a := range aggs {
		if len(a.AllRefs()) == 0 {
			t.Errorf("aggregate %s has no candidates (groups=%d) — silent data loss", a.ID, len(a.Groups))
		}
	}
}

// aggregatesUnder collects every Aggregate in the structured tree.
func aggregatesUnder(g *ir.Graph) []*ir.Aggregate {
	var out []*ir.Aggregate
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			switch st := s.(type) {
			case *ir.IfStmt:
				if st.Merge != nil {
					out = append(out, st.Merge)
				}
				walk(st.Then)
				walk(st.Else)
			case *ir.SwitchStmt:
				if st.Merge != nil {
					out = append(out, st.Merge)
				}
				for _, br := range st.Branches {
					walk(br.Body)
				}
			case *ir.QaStmt:
				if st.Merge != nil {
					out = append(out, st.Merge)
				}
				for _, br := range st.Branches {
					walk(br.Body)
				}
			case *ir.AttemptStmt:
				walk(st.Success)
				walk(st.Failure)
			case *ir.LoopStmt:
				walk(st.Body)
			case *ir.ParallelStmt:
				for _, br := range st.Branches {
					walk(br)
				}
			}
		}
	}
	walk(g.Root)
	return out
}

// returnStmtsOf collects the top-level returns of the root block.
func returnStmtsOf(g *ir.Graph) []*ir.ReturnStmt {
	var out []*ir.ReturnStmt
	for _, s := range g.Root.Stmts {
		if r, ok := s.(*ir.ReturnStmt); ok {
			out = append(out, r)
		}
	}
	return out
}

// **零值前绑定豁免**（2026-10-12，R-205 族）：junction 前绑定**可证类型零**、且仍
// 有 ≥2 真写臂时，未写臂**不贡献**零候选 —— merge 的 zero 语义
// （first_non_zero / first_not_null）运行时本就跳过零候选，「缺省 = 类型零」与
// 「候选是零值」取值相同 ⇒ 物化它是**冗余候选**。
//
// 失效模式（一手，响亮→静默红线）：反编译产出的 `string G; G = "";` 零初值 +
// **无 default 臂**的 switch（空 default 臂直落汇聚点 ⇒ 反编译省略该臂以保往返；
// 反编译器**必须**发这条零初值，否则 `UNINITIALIZED_USE`：无 default 臂时 G 未定
// 赋值）—— 重编时空 default 臂把零初值继承成额外候选（+ 合成零载体）⇒
// c0(2 候选) ≠ c1(3 候选) = `ARTIFACT_DRIFT/TOPOLOGY` + `MATERIALIZED_GROWTH`。
// 豁免后零初值成死载体被清扫 ⇒ c1 == c0（复算 = `scripts/five-step/corpus_roundtrip.py`
// 684/684 绿 + `five-step-rt --src` 该形状 6/6/6）。撤实现 ⇒ 本两例 RED。
//
// 守 `writeArms ≥ 2`：豁免**不得**把 merge 打到单候选（非 φ，无承载位）——
// 1 写臂 + 零值未写臂时保留零候选（存活侧锚点 = 第 ③ 例）。
func TestRequestInputZeroEntryBindingExempted(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
enum ReviewAction { approve, reject }
type ReviewForm = { string comment; }

function main() -> string {
    string agg;
    agg = "";
    ask<ReviewForm, ReviewAction>({
        content: "Review",
        fields: { comment: { kind: "paragraph" } },
        actions: {
            approve: { title: "Approve", style: "primary" },
            reject: { title: "Reject", style: "default" }
        }
    }) as review {
        action ReviewAction.approve { agg = review.comment; }
        action ReviewAction.reject { agg = "rejected"; }
    }
    return agg;
}
`
	g := buildIR(t, src)
	aggs := aggregatesUnder(g)
	if len(aggs) != 1 {
		t.Fatalf("ask junction aggregates = %d, want 1", len(aggs))
	}
	if n := len(aggs[0].AllRefs()); n != 2 {
		t.Fatalf("零值前绑定 + 2 真写臂 ⇒ 候选 = %d, want 2（零候选豁免，merge zero 语义承担）", n)
	}
}

func TestSwitchZeroEntryBindingExemptedWithTwoWriteArms(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(string k) -> string {
    string v;
    v = "";
    switch (k) {
        "a": { v = "y"; }
        "b": { v = "z"; }
    }
    return v;
}
`
	g := buildIR(t, src)
	aggs := aggregatesUnder(g)
	if len(aggs) != 1 {
		t.Fatalf("junction aggregates = %d, want 1", len(aggs))
	}
	if n := len(aggs[0].AllRefs()); n != 2 {
		t.Fatalf("零值前绑定 + 2 真写臂 ⇒ 候选 = %d, want 2（零候选豁免）", n)
	}
}

// ③ 存活侧锚点：1 真写臂 + 零值未写臂 ⇒ **不得**豁免（豁免会把 merge 打到单候选
// = 非 φ）。零候选保留，保 φ 且不改语义。
func TestSwitchZeroEntryBindingKeptWhenOnlyOneWriteArm(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(string k) -> string {
    string v;
    v = "";
    switch (k) {
        "a": { v = "y"; }
    }
    return v;
}
`
	g := buildIR(t, src)
	aggs := aggregatesUnder(g)
	if len(aggs) != 1 {
		t.Fatalf("junction aggregates = %d, want 1", len(aggs))
	}
	if n := len(aggs[0].AllRefs()); n != 2 {
		t.Fatalf("1 写臂 + 零值未写臂 ⇒ 候选 = %d, want 2（零候选必须保留以保 φ）", n)
	}
}

// ② R-205 锁（2026-10-12）：**嵌套无 default 臂 switch** 的内层零值前绑定候选
// 随**折叠**展开进外层汇聚单元后必须删除。内层 junction（单写臂 "x" + 零值未写臂
// default）因 writeArms < 2 未能在 build 期豁免其零候选（保 φ，见上方 ③ 存活侧锚点），
// 该候选随折叠原位展开进外层 A ⇒ 外层候选 3 ≠ 平台真源 2（canon 漂移）。
// 修法 = 豁免再加「junction 位于 switch 臂内」（switchBranchDepth ≥ 1）。
func TestNestedSwitchNoDefaultArmFoldDropsZeroPrebind(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(string q) -> string {
    string Group1;
    Group1 = "";
    switch (q) {
        "a": { switch (q) { "x": { Group1 = "X"; } } }
        default: { Group1 = "D"; }
    }
    return Group1;
}
`
	g := buildIR(t, src)
	ir.FlattenChainAggregates(g)
	aggs := aggregatesUnder(g)
	if len(aggs) != 1 {
		t.Fatalf("post-flatten aggregates = %d, want 1（嵌套 switch 折叠成单 merge）", len(aggs))
	}
	if n := len(aggs[0].AllRefs()); n != 2 {
		t.Fatalf("嵌套无 default 臂 switch 折叠后候选 = %d, want 2（内层零值前绑定候选随折叠删除）: %#v", n, aggs[0])
	}
}
