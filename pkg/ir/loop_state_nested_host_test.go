package ir_test

// loop_state_nested_host_test.go — 循环状态（loop state）收集的**宿主闭包**契约
// （2026-09-13，§10.6-3 族）。
//
// 规则（一条，无形态枚举）：`while` / `for` 体内对**循环外已绑定变量**的赋值 =
// 循环状态写；该变量必须进 loop 的 `StateValues`（→ 后端 `variableParameters`
// = 平台循环的反馈口 + 入参），值才能跨迭代存活、循环结束后才能读到。
//
// 历史缺陷：`collectAssignedVars`（pkg/ir/build.go）**手枚举**语句容器，
// 只认 Assign / If / For / While —— `switch`（臂体 / default 体）、`attempt`
// （success / failure 体）、`parallel`（各分支）里的赋值被**看不见** ⇒ 该变量
// 不被判为循环状态 ⇒ 循环内写入静默丢失（循环后读到循环前的值）、产物里该
// 汇聚无消费者（进而被 IR 优化删除）⇒ 反编译读回时宿主臂为空、出码语法非法。
// 这是「形态枚举式推广」反模式的又一实例（同族：S107 desugar 漏容器 ⇒ elif 中臂
// 丢失，见 pkg/passes/desugar_container_closure_test.go 头注；passes 层的
// LoopStatePass.collectWrites 早已用 ast.Walk 泛化遍历 —— IR 层必须对齐）。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

// loopStateHostForms: 同一规则在各类嵌套宿主上的实例化。每条的循环外变量都叫
// `out`（循环前已绑定、循环后被 return），宿主里对它赋值。
var loopStateHostForms = map[string]string{
	"direct": workflowHeader + `
function main() -> string {
    int w0 = 0;
    string out;
    out = "init";
    while (w0 < 3) limit 3 {
        w0 = w0 + 1;
        out = "direct";
    }
    return out;
}
`,
	"if": workflowHeader + `
function main() -> string {
    int w0 = 0;
    string out;
    out = "init";
    while (w0 < 3) limit 3 {
        w0 = w0 + 1;
        if (w0 > 1) { out = "if-a"; } else { out = "if-b"; }
    }
    return out;
}
`,
	"switch": workflowHeader + `
function main() -> string {
    int w0 = 0;
    string out;
    out = "init";
    while (w0 < 3) limit 3 {
        w0 = w0 + 1;
        string kind;
        if (w0 > 1) { kind = "a"; } else { kind = "b"; }
        switch (kind) {
            "a": { out = "sw-a"; }
            default: { out = "sw-d"; }
        }
    }
    return out;
}
`,
	"attempt": workflowHeader + `
function main() -> string {
    int w0 = 0;
    string out;
    out = "init";
    while (w0 < 3) limit 3 {
        w0 = w0 + 1;
        attempt http("GET", "https://example.com/e", {}) as r {
            success { out = "at-ok"; }
            failure(e) { out = "at-err"; }
        }
    }
    return out;
}
`,
	// parallel 宿主：分支是透明屏障（P5），分支内写循环外变量同样是循环状态写。
	"parallel": workflowHeader + `
function main() -> string {
    int w0 = 0;
    string out;
    out = "init";
    while (w0 < 3) limit 3 {
        w0 = w0 + 1;
        parallel {
            r1: { out = "p-a"; }
            r2: { let z = "p-b"; let _u = z; }
        }
    }
    return out;
}
`,
	// ask（request_input）宿主：action 分支体里写循环外变量。
	"ask": workflowHeader + `
enum ReviewAction { approve, reject }
type ReviewForm = { string comment; }
function main() -> string {
    int w0 = 0;
    string out;
    out = "init";
    while (w0 < 3) limit 3 {
        w0 = w0 + 1;
        ask<ReviewForm, ReviewAction>({
            content: "Review",
            fields: { comment: { kind: "paragraph" } },
            actions: {
                approve: { title: "Approve", style: "primary" },
                reject: { title: "Reject", style: "default" },
            },
        }) as review {
            action ReviewAction.approve { out = "ask-ok"; }
            action ReviewAction.reject { out = "ask-no"; }
        }
    }
    return out;
}
`,
	// 递归第二层：宿主内再嵌套宿主（if 的**两个臂都**只经 switch 写 —— 两臂
	// 都不能有直接赋值，否则该臂会被 If 递归收下、测试「蒙对」而覆盖不到 switch）。
	"switch-in-both-if-arms": workflowHeader + `
function main() -> string {
    int w0 = 0;
    string out;
    out = "init";
    while (w0 < 3) limit 3 {
        w0 = w0 + 1;
        if (w0 > 1) {
            string k1;
            k1 = "a";
            switch (k1) {
                "a": { out = "n-a"; }
                default: { out = "n-d"; }
            }
        } else {
            string k2;
            k2 = "b";
            switch (k2) {
                "b": { out = "n-b"; }
                default: { out = "n-e"; }
            }
        }
    }
    return out;
}
`,
}

// TestBuildLoopStateNestedWhileOwnershipFacts：**嵌套 while 的两条事实**（第一手
// 实测，2026-09-13）。这一族**不能**简单写成「嵌套循环一律被语言拒绝」——
// `passes.LoopStatePass` 的 owner 判据取**最内层 while**（`whileOwners[-1]`，
// loop_state_pass.go:82-88），因此「外层作用域变量只由内层循环写」这条**不报错**。
//
// 本测试把两条事实**都**钉住（哪条变了都必须有人重看）：
//
//	① both_loops_write：同一变量被外层 **和** 内层 while 写 ⇒ 两个不同的最内层
//	   owner ⇒ E1042「Mutable workflow state …」（语言拒绝，IR 不可达）。
//	② inner_only_write：外层作用域变量**只**由内层 while 写 ⇒ 零诊断，且 IR 里
//	   `out` 同时出现在**两个**循环的 StateValues（`collectAssignedVars` 是子树
//	   闭包，外层的子树包含内层）。二者是**已知潜伏分歧**：pass 层认最内层 owner，
//	   IR 层认子树闭包。今日**不可观测**——coze-biz lowering 对嵌套 loop 容器硬
//	   报错（`ADAPTER_ERROR … nests a loop container`，见
//	   cozebiz_loop_state_nested_host_contract_test.go 的 adapter 子测试），产物
//	   根本生成不出来；且该分歧在本次修复前就存在（旧 `collectAssignedVars` 也
//	   递归 `WhileStmt`），**不是**本族引入。
func TestBuildLoopStateNestedWhileOwnershipFacts(t *testing.T) {
	t.Run("both_loops_write", func(t *testing.T) {
		const src = workflowHeader + `
function main() -> string {
    int w0 = 0;
    int w1 = 0;
    string out;
    out = "init";
    while (w0 < 3) limit 3 {
        w0 = w0 + 1;
        w1 = 0;
        while (w1 < 2) limit 2 {
            w1 = w1 + 1;
            out = "nested";
        }
    }
    return out;
}
`
		msgs := buildIRErrs(t, src)
		if len(msgs) == 0 {
			t.Fatalf("同一变量被外层与内层 while 写（两个最内层 owner）必须被前端拒绝（E1042），实际零诊断")
		}
		found := false
		for _, m := range msgs {
			if strings.Contains(m, "Mutable workflow state") {
				found = true
			}
		}
		if !found {
			t.Fatalf("期望 E1042「Mutable workflow state …」，实际诊断 = %v", msgs)
		}
	})

	t.Run("inner_only_write", func(t *testing.T) {
		const src = workflowHeader + `
function main() -> string {
    int w0 = 0;
    string out;
    out = "init";
    while (w0 < 3) limit 3 {
        w0 = w0 + 1;
        int w1 = 0;
        while (w1 < 2) limit 2 {
            w1 = w1 + 1;
            out = "nested";
        }
    }
    return out;
}
`
		msgs := buildIRErrs(t, src)
		for _, m := range msgs {
			if strings.Contains(m, "Mutable workflow state") {
				t.Fatalf("只由内层 while 写外层作用域变量今日**不**触发 E1042（pass 取最内层 owner）；若此断言开始失败，说明 owner 判据变了，需重审本条与 IR 子树闭包的相互关系：%v", msgs)
			}
		}
		g := buildIR(t, src)
		var layers []*ir.LoopStmt
		layers = append(layers, collectLoops(g.Root)...)
		if len(layers) != 2 {
			t.Fatalf("期望 2 个循环，实际 %d", len(layers))
		}
		for i, lp := range layers {
			if _, ok := lp.StateValues["out"]; !ok {
				t.Fatalf("loop[%d] 缺 out（子树闭包：外层循环的子树含内层写点）；实际 = %v", i, stateKeys(lp))
			}
		}
	})
}

// TestBuildLoopStateExcludesConversationVars：负向 —— `@conversation` 变量由平台
// 会话状态承载（不进 workflow 的绑定/producer 表），即使循环体内对它赋值也
// **不是**循环状态；收进 loop 反馈口 = 伪造一条并不存在的「迭代携带」语义。
// （第一手：`visit_count` 不进 StateValues，且零诊断。）
func TestBuildLoopStateExcludesConversationVars(t *testing.T) {
	const src = `@language nodecoda/1
@mode advanced-chat
@conversation int visit_count = 0;
function main() -> int {
    int w0 = 0;
    while (w0 < 3) limit 3 {
        w0 = w0 + 1;
        visit_count = 5;
    }
    return w0;
}
`
	g := buildIR(t, src)
	loop := firstLoop(t, g.Root)
	if _, ok := loop.StateValues["visit_count"]; ok {
		t.Errorf("`@conversation` 变量不得进循环状态（它是会话状态，不是迭代携带值）；实际 = %v", stateKeys(loop))
	}
	if _, ok := loop.StateValues["w0"]; !ok {
		t.Errorf("对照组：迭代变量 w0 必须在循环状态里；实际 = %v", stateKeys(loop))
	}
}

// TestBuildLoopStateCoversNestedHosts：一条规则必须在所有宿主形态上成立
// （孤立 → 嵌套 → 双层），缺任一 = 该宿主的写入静默丢失。
func TestBuildLoopStateCoversNestedHosts(t *testing.T) {
	for name, src := range loopStateHostForms {
		t.Run(name, func(t *testing.T) {
			g := buildIR(t, src)
			loops := collectLoops(g.Root)
			if len(loops) == 0 {
				t.Fatalf("%s: no loop", name)
			}
			for i, lp := range loops {
				if _, ok := lp.StateValues["out"]; !ok {
					t.Fatalf("%s: loop[%d] StateValues = %v（缺 out ⇒ 循环内写入不进反馈口 ⇒ 值静默丢失）",
						name, i, stateKeys(lp))
				}
				if lp.StateTypes["out"] == nil {
					t.Fatalf("%s: loop[%d] StateTypes 缺 out（端口类型必须随状态一起发）", name, i)
				}
			}
		})
	}
}

// TestBuildLoopStateExcludesNonState：负向 —— 「一律提升成循环状态」是错规则。
// 三条不得进入 StateValues：① 循环体内新声明的局部变量；② 只读不写的外层变量；
// ③ 迭代变量本身（它是 node_inputs 端口，不是状态）。
func TestBuildLoopStateExcludesNonState(t *testing.T) {
	const src = workflowHeader + `
function main(string item) -> string {
    let items = [item];
    string outer;
    outer = "keep";
    string readOnly;
    readOnly = "r";
    for (item in items) {
        let local = item;
        let copy = local;
        let seen = readOnly;
        let _unused = copy;
        let _unused2 = seen;
    }
    return outer;
}
`
	g := buildIR(t, src)
	loop := firstLoop(t, g.Root)
	for _, bad := range []string{"local", "copy", "seen", "readOnly", "item", "items", "outer"} {
		if _, ok := loop.StateValues[bad]; ok {
			t.Errorf("StateValues 不应含 %q（状态 = 「循环外已绑定 且 体内被写」；实际 = %v）", bad, stateKeys(loop))
		}
	}
}

// stateKeys 取 StateValues 的键（排序无关，仅用于失败信息）。
func stateKeys(lp *ir.LoopStmt) []string {
	out := make([]string, 0, len(lp.StateValues))
	for k := range lp.StateValues {
		out = append(out, k)
	}
	return out
}

// collectLoops 深度优先收集块内所有 LoopStmt（嵌套循环按遇到的顺序）。
func collectLoops(b *ir.Block) []*ir.LoopStmt {
	var out []*ir.LoopStmt
	var walk func(b *ir.Block, d int)
	walk = func(b *ir.Block, d int) {
		if b == nil || d > 6 {
			return
		}
		for _, s := range b.Stmts {
			switch v := s.(type) {
			case *ir.LoopStmt:
				out = append(out, v)
				walk(v.Body, d+1)
			case *ir.IfStmt:
				walk(v.Then, d+1)
				walk(v.Else, d+1)
			case *ir.SwitchStmt:
				for _, br := range v.Branches {
					walk(br.Body, d+1)
				}
			case *ir.AttemptStmt:
				walk(v.Success, d+1)
				walk(v.Failure, d+1)
			}
		}
	}
	walk(b, 0)
	return out
}
