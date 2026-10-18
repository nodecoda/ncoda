package ir_test

// loop_return_arm_kind_contract_test.go — 「循环内 return」的判据必须与**臂种类
// 无关**（2026-09-13 用户拍板 D4/D5-A；形态枚举反模式收口）。
//
// 语义定案（D4）：`return` 结束**当前函数 / 当前流程**，循环不构成 return 的
// 边界（只有函数/流程是）——循环内 `return` 穿透循环。M17（LoopReturnNode +
// 循环状态变量 + loop 后检查分支）是该语义的**实现手段**，不是另一套语义。
//
// 缺陷（实测，2026-09-13）：`for { switch { "a": { return "early"; } } }` 不走
// M17，而走「主流程分支 return = 流程终止」⇒ `ensureEnd` 把共享 `end` 放进
// **循环容器内**，顶层再发 `loop --loop-output--> <从未发射的节点>`；产物
// 编译 SUCCEEDED + L1/L2 双 PASS，反编译 rc=1。同形状的 if 臂路径正确
// （走 M17、fail loud）。根因 = build_return.go 的 `branchValueDepth == 0`
// 守卫把「臂种类」当成了判据（switch/intent 臂也递增 branchValueDepth）。
//
// 唯一例外保持有效：QA action / attempt 臂里的 return 是**分支结果值**
// （P5 subscope return），不是函数返回 ⇒ 不得变成 loop_return。该例外由
// `inBranchValueArm()`（branchValueDepth>0 且 switchBranchDepth==0）判定。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

// loopBodyContainsEnd 递归检查循环体（含嵌套控制结构）里是否落了共享 `end`。
// 共享 end 落进循环体 = 平台不可导入产物（`exit node cannot have parent`），
// 必须为 false。
func loopBodyContainsEnd(stmts []ir.Stmt) bool {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ir.ReturnStmt:
			if v.End != nil {
				return true
			}
		case *ir.LoopStmt:
			if v.Body != nil && loopBodyContainsEnd(v.Body.Stmts) {
				return true
			}
		case *ir.IfStmt:
			if v.Then != nil && loopBodyContainsEnd(v.Then.Stmts) {
				return true
			}
			if v.Else != nil && loopBodyContainsEnd(v.Else.Stmts) {
				return true
			}
		case *ir.SwitchStmt:
			for _, br := range v.Branches {
				if br.Body != nil && loopBodyContainsEnd(br.Body.Stmts) {
					return true
				}
			}
		case *ir.QaStmt:
			for _, br := range v.Branches {
				if br.Body != nil && loopBodyContainsEnd(br.Body.Stmts) {
					return true
				}
			}
		case *ir.AttemptStmt:
			if v.Success != nil && loopBodyContainsEnd(v.Success.Stmts) {
				return true
			}
			if v.Failure != nil && loopBodyContainsEnd(v.Failure.Stmts) {
				return true
			}
		}
	}
	return false
}

func firstLoopStmt(t *testing.T, g *ir.Graph) *ir.LoopStmt {
	t.Helper()
	var found *ir.LoopStmt
	var walk func(stmts []ir.Stmt)
	walk = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			if ls, ok := s.(*ir.LoopStmt); ok && found == nil {
				found = ls
				return
			}
		}
	}
	if g.Root == nil {
		t.Fatal("graph has no structured root")
	}
	walk(g.Root.Stmts)
	if found == nil {
		t.Fatal("no loop statement in graph")
	}
	return found
}

// TestBuildLoopReturnIsArmKindIndependent 锁定 D5-A：同一个「循环内 return」
// 在裸体 / if 臂 / switch 臂 / 嵌套臂里都必须走 M17（LoopReturnNode +
// loop.ReturnValue），且共享 end 一律不得落进循环体。
//
// 撤掉修复（把判据退回 `branchValueDepth == 0`）时，switch 与嵌套两例 RED：
// LoopReturnNode 数量为 0 且循环体内出现 end。
func TestBuildLoopReturnIsArmKindIndependent(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "裸体 return",
			body: `        return 42;`,
		},
		{
			name: "if 臂 return",
			body: `        if (v == "x") { return 42; }`,
		},
		{
			name: "switch 臂 return（漏网形状）",
			body: `        switch (v) {
            "x": { return 42; }
            "y": { http("GET", "https://n.example/b"); }
        }`,
		},
		{
			name: "嵌套：if 在 switch 臂内",
			body: `        switch (v) {
            "x": {
                if (v == "x") { return 42; }
            }
            "y": { http("GET", "https://n.example/b"); }
        }`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := workflowHeader + `
function main(string csv) -> int {
    let values = split(csv, ",");
    for (v in values) {
` + tc.body + `
    }
    return 0;
}
`
			g := buildIR(t, src)
			if lrs := nodesOf(g)[ir.KindLoopReturn]; len(lrs) != 1 {
				t.Errorf("loop_return 节点数 = %d, want 1 —— 形状：%s", len(lrs), tc.name)
			}
			loop := firstLoopStmt(t, g)
			if loop.ReturnValue == nil {
				t.Errorf("loop.ReturnValue 未登记（M17 未生效）—— 形状：%s", tc.name)
			}
			if loopBodyContainsEnd(loop.Body.Stmts) {
				t.Errorf("共享 end 落进了循环体（平台不可导入：exit node cannot have parent）—— 形状：%s", tc.name)
			}
		})
	}
}

// TestBuildLoopReturnKeepsQaAttemptBranchValueSemantics 是负面作用的守门测试：
// QA action / attempt success-failure 臂里的 return 是**分支结果值**
// （P5 subscope return），不是函数返回 ⇒ 循环体内这层语义不得被 D5-A 一并
// 改成 loop_return。没有这条，「循环内一律 loop_return」也能让上面的测试变绿。
func TestBuildLoopReturnKeepsQaAttemptBranchValueSemantics(t *testing.T) {
	src := workflowHeader + `
function main(string csv) -> int {
    let values = split(csv, ",");
    for (v in values) {
        attempt http("GET", "https://n.example/b", {}) as response {
            success { return 1; }
            failure(error) { return 2; }
        }
    }
    return 0;
}
`
	g := buildIR(t, src)
	if lrs := nodesOf(g)[ir.KindLoopReturn]; len(lrs) != 0 {
		t.Errorf("attempt 臂 return 被误判为 loop_return（%d 个）—— 分支结果值语义回归", len(lrs))
	}
}
