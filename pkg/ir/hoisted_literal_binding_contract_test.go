// 语言特性矩阵锚点声明（docs/LANGUAGE-FEATURE-MATRIX.md）：
//@feature var-let — 提升变量（可变态量）的字面量写必须可寻址（TestBuildHoistedLiteralWriteIsAddressable）
//@feature if-else — if/else 分支 phi 汇聚候选（字面量基座 / 双写臂）——同上

package ir_test

// hoisted_literal_binding_contract_test.go —— S2-gate D1 根因的 RED 锚
// （2026-09-11）。
//
// 规则（无例外，写入 AGENTS.md 的语言递归自洽口径）：
//
//	提升变量（hoisted mutable var）的**每一次写**都是一次"绑定变更"，
//	必须可寻址 —— 无论写的是 producer 表达式还是 int/float/bool 字面量。
//	交汇点 phi 规划消费的"前绑定 / 臂末绑定"视图（varToOutput 快照）必须
//	对该类变量封闭。
//
// 违反时的症状 = **静默语义丢失**（不是编译错误）：字面量只进 litVars
// （无 producer 地址），交汇点看不到基座 ⇒ 候选集为空 ⇒ 产物里出现
// `variable_merge.{variables: []}` 或整条 merge 被剪掉，分支语义凭空消失，
// 再编译的文本退化成"只在臂内取常量"。string 一直是对的（StringLit 走
// emitTextConst 物化常量 shim），int/float/bool 走 litVars 无地址 ——
// 这个**不对称**就是出生处（D1）。
//
// 与 P04/X23 的边界：这里物化的是**真实写的值**（可寻址 = 可被 phi 消费），
// 不是"为没写的臂物化转发载体"（那是禁用项）。string 侧早已如此，
// 本契约要求 int/float/bool 与之对称。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

// hoistedPhiOf 返回函数体第一个 if 的 phi 汇聚（无则 fatal）。
func hoistedPhiOf(t *testing.T, src string) *ir.Aggregate {
	t.Helper()
	g := buildIR(t, src)
	for _, stmt := range g.Root.Stmts {
		if st, ok := stmt.(*ir.IfStmt); ok {
			if st.Merge == nil {
				t.Fatalf("if must carry a phi aggregate, got nil:\n%s", src)
			}
			return st.Merge
		}
	}
	t.Fatalf("no if statement in graph:\n%s", src)
	return nil
}

// phiRefs 把汇聚的所有候选（legacy refs + 具名组 refs）摊平。
func phiRefs(a *ir.Aggregate) []ir.Ref {
	out := append([]ir.Ref{}, a.Refs...)
	for _, grp := range a.Groups {
		out = append(out, grp.Refs...)
	}
	return out
}

func TestBuildHoistedLiteralWriteIsAddressable(t *testing.T) {
	// 孤立最小形：两臂都写 int 字面量。修复前 ⇒ 候选为空（`variables: []`）；
	// 修复后 ⇒ 候选 = 两臂的真值（各一个常量 producer）。
	src := workflowHeader + `function main(string c, string d) -> int {
    int g;
    g = 0;
    if (c == d) {
        g = 7;
    } else {
        g = 9;
    }
    let r = g + 1;
    return r;
}
`
	phi := hoistedPhiOf(t, src)
	if refs := phiRefs(phi); len(refs) != 2 {
		t.Fatalf("both-arm literal writes must produce two phi candidates, got %d: %#v", len(refs), phi)
	}
}

func TestBuildHoistedLiteralBaseSurvivesUnwrittenArm(t *testing.T) {
	// 一层：字面量基座 + 单臂写。未写臂的候选必须是**基座字面量本身**
	// （5，不是类型零值、不是臂内字面量）——这是"字面量基座可寻址"的
	// 直接判据（修复前基座不在视图里 ⇒ 候选为空）。
	src := workflowHeader + `function main(string c) -> int {
    int g;
    g = 5;
    if (c == "") {
        g = 7;
    }
    let r = g + 1;
    return r;
}
`
	phi := hoistedPhiOf(t, src)
	refs := phiRefs(phi)
	if len(refs) != 2 {
		t.Fatalf("literal base + one arm write must produce two candidates, got %d: %#v", len(refs), phi)
	}
	// 两候选互不相同：一个来自臂内（7），一个来自基座（5）。
	if refs[0].Producer == refs[1].Producer {
		t.Fatalf("the unwritten arm must contribute the base literal, got the arm twice: %#v", refs)
	}
}

func TestBuildHoistedLiteralTypesAreSymmetric(t *testing.T) {
	// 递归/类型对称：int/float/bool/string 四种字面量基座走**同一条规则**
	// （无"仅当 string"的隐式例外）。string 是既有正确性参照。
	cases := []struct {
		name   string
		typ    string
		base   string
		armVal string
		ret    string
	}{
		{"int", "int", "5", "7", "int"},
		{"float", "float", "0.5", "0.75", "float"},
		{"bool", "bool", "false", "true", "bool"},
		{"string", "string", `"base"`, `"arm"`, "string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := workflowHeader + `function main(string c) -> ` + tc.ret + ` {
    ` + tc.typ + ` g;
    g = ` + tc.base + `;
    if (c == "") {
        g = ` + tc.armVal + `;
    }
    return g;
}
`
			phi := hoistedPhiOf(t, src)
			refs := phiRefs(phi)
			if len(refs) != 2 {
				t.Fatalf("%s: literal base + arm write must produce two candidates, got %d: %#v",
					tc.name, len(refs), phi)
			}
		})
	}
}

func TestBuildHoistedLiteralInitSpellingIsSymmetric(t *testing.T) {
	// 语言判定（A 级，2026-09-19 用户拍板）：`T v = <init>;` ≡ `T v; v = <init>;`
	// 是本语言的**同一构造**（本语言不区分构造与赋值）⇒ 两条拼写在 IR 上也必须
	// 同形：字面量初值同样是"一次写"，必须可寻址（producer 背书）。
	// 违反时的症状与 `g = <lit>;` 那次（D1）同类，但发生在**声明位**：
	// 初值只进 litVars ⇒ 基座不在 phi 视图里 ⇒ 未写臂的候选消失 = 静默丢值
	// （实测 HEAD：`int v = 5; if (c=="") { v = 7; }` 汇聚仅一候选；string 无此病
	// 因为它早就物化常量节点——这个不对称正是出生处）。
	// int/float/bool/string 四种字面量同一把尺子（无"仅当 string"的隐式例外）。
	cases := []struct {
		name, typ, base, armVal, ret string
	}{
		{"int", "int", "5", "7", "int"},
		{"float", "float", "0.5", "0.75", "float"},
		{"bool", "bool", "false", "true", "bool"},
		{"string", "string", `"base"`, `"arm"`, "string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			head := workflowHeader + `function main(string c) -> ` + tc.ret + ` {
`
			body := `
    if (c == "") {
        g = ` + tc.armVal + `;
    }
    return g;
}
`
			joined := hoistedPhiOf(t, head+`    `+tc.typ+` g = `+tc.base+`;`+body)
			separate := hoistedPhiOf(t, head+`    `+tc.typ+` g;
    g = `+tc.base+`;`+body)
			jRefs, sRefs := phiRefs(joined), phiRefs(separate)
			if len(sRefs) != 2 {
				t.Fatalf("baseline spelling (bare decl + assign) must produce two candidates, got %d: %#v", len(sRefs), separate)
			}
			if len(jRefs) != len(sRefs) {
				t.Fatalf("`T g = <lit>;` and `T g; g = <lit>;` must share the same phi view: got %d vs %d candidates: %#v",
					len(jRefs), len(sRefs), joined)
			}
			if jRefs[0].Producer == jRefs[1].Producer {
				t.Fatalf("the unwritten arm must contribute the base literal, got the arm twice: %#v", jRefs)
			}
		})
	}
}
