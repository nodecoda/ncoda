package ir_test

// 循环容器「类型全局性」合同测试（loop 翻译完整算法 §3 不变量 2，2026-09-11）。
//
// 见 docs/ideas-todo/2026-09-11-loop-translation-complete-algorithm.md §3/§5/§8-P2
// 与 docs/ideas-todo/2026-09-11-for-statement-roundtrip-reversibility-review.md
// （RED 表 R1 / R3 / R4）。
//
// 不变量：**producer 端口类型是全局知识；容器隔离只作用于结构归属**。
// ⇒ 同一引用在循环体内 / 容器外、不同调用点（直接赋值 RHS / 类型化调用参数 /
//    合成表达式 shim 输入 / 条件操作数）必须走**同一条**类型解析规则；
//    可解析性不得依赖「该引用是否落在容器体内」或「ref 是否被某个调用点补过
//    类型戳」（HEAD 上的 R1/R3/R4 正是这两种位置差异）。
//
// 这些测试在 P1（类型全局索引）之前是 RED：`emitLoopNode` 把循环体建成隔离子图
// （`g.nodes = nil`）后，`portTypeOf` 的作用域查找对**外层 producer / 循环状态口**
// 一律落空，`refType` 于是 fail loud（`cannot resolve type of reference …`）。
// 修复方向必须是「一条无例外规则」（类型全局索引），不是逐点补 ref.typ 形态特例。
//
//@feature for-while — 语句 for 体内读循环状态 / 外层可寻址变量（类型全局解析）

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
)

// loopStmtsIn 收集图中所有 LoopStmt（含嵌套容器内的），按源码顺序。
func loopStmtsIn(g *ir.Graph) []*ir.LoopStmt {
	var out []*ir.LoopStmt
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			switch st := s.(type) {
			case *ir.LoopStmt:
				out = append(out, st)
				walk(st.Body)
			case *ir.IfStmt:
				walk(st.Then)
				walk(st.Else)
			case *ir.AttemptStmt:
				walk(st.Success)
				walk(st.Failure)
			case *ir.ParallelStmt:
				for _, br := range st.Branches {
					walk(br)
				}
			}
		}
	}
	if g != nil && g.Root != nil {
		walk(g.Root)
	}
	return out
}

// TestLoopBodyStateReadResolvesTypeGlobally — V1（R1 最小形态）：
// 语句 for 的**循环状态**在体内以「合成表达式输入」位置被读（`num + 1` 走
// shim 物化），必须能解析出状态口类型。HEAD 上此处 fail loud：
// `cannot resolve type of reference 100003.num (producer kind "")`——
// 状态变量注册（build_stmt.go:716）不带类型戳，而体内子图又查不到容器节点。
func TestLoopBodyStateReadResolvesTypeGlobally(t *testing.T) {
	src := workflowHeader + `
function main() -> string {
    int num;
    num = 1;
    for (x in [1, 2, 3]) {
        let v1 = num + 1;
        num = v1;
    }
    return "x";
}
`
	g := buildIR(t, src)
	loops := loopStmtsIn(g)
	if len(loops) != 1 {
		t.Fatalf("want 1 loop, got %d", len(loops))
	}
	st := loops[0].StateTypes["num"]
	if st == nil {
		t.Fatalf("loop StateTypes 缺状态口 num（容器口集 = 状态集，两侧必须互为镜像）")
	}
	if st.Kind != "integer" {
		t.Errorf("state port num type = %q, want integer", st.Kind)
	}
}

// TestLoopBodyOuterAddressableReadEqualsOutside — V3（R4）：
// **同一条规则**在容器外与容器内必须给出相同结论。左半 = 循环外读外层可寻址
// 变量（HEAD 上即绿，作对照）；右半 = 同一表达式放进循环体内（HEAD 红）。
// 两者都绿才说明「类型可解析性不随容器边界改变」。
func TestLoopBodyOuterAddressableReadEqualsOutside(t *testing.T) {
	outside := workflowHeader + `
function main() -> string {
    int num;
    num = 1;
    let v1 = num + 1;
    return "x";
}
`
	// 对照面：容器外。HEAD 上即为绿——不得因本次修复变成红。
	gOut := buildIR(t, outside)
	if len(loopStmtsIn(gOut)) != 0 {
		t.Fatalf("对照源码不应含循环容器")
	}

	inside := workflowHeader + `
function main() -> string {
    int num;
    num = 1;
    let items = [1, 2, 3];
    for (x in items) {
        let v1 = num + 1;
    }
    return "x";
}
`
	gIn := buildIR(t, inside)
	loops := loopStmtsIn(gIn)
	if len(loops) != 1 {
		t.Fatalf("want 1 loop, got %d", len(loops))
	}
}

// TestLoopBodyReadsIterItemAndStateBySameRule — 轴 11/轴 6 对偶：
// 迭代变量口与状态口是**同一种**「容器口 = 类型源」规则，不得一个带类型戳
// 一个不带（HEAD 上 :711 补戳 / :716 不补 = 相隔 5 行的不一致）。
// 体内同时对二者做表达式读，两者都必须可解析。
func TestLoopBodyReadsIterItemAndStateBySameRule(t *testing.T) {
	// 迭代源用**声明类型**（`int[] nums` → Start 口 list<integer>），使元素类型
	// 有权威来源；数组字面量迭代源的 shim 会把元素类型塌成 string（见 §轴11 残差，
	// 属「表达式 shim 声明类型」族，不在本族断言）。
	src := workflowHeader + `
function main(int[] nums) -> string {
    int total;
    total = 0;
    for (x in nums) {
        let v1 = x + 1;
        let v2 = total + v1;
        total = v2;
    }
    return "x";
}
`
	g := buildIR(t, src)
	loops := loopStmtsIn(g)
	if len(loops) != 1 {
		t.Fatalf("want 1 loop, got %d", len(loops))
	}
	if st := loops[0].StateTypes["total"]; st == nil || st.Kind != "integer" {
		t.Errorf("state port total = %v, want integer", st)
	}
	// 迭代口：array 源 [1,2,3] → 元素类型 integer。item 口与 state 口同表
	// （StateTypes），正是「口分类对偶」的实现面：两者必须都解析到。
	if it := loops[0].StateTypes["x"]; it == nil || it.Kind != "integer" {
		t.Errorf("item port x = %v, want integer", it)
	}
}

// TestLoopBodyStateReadInsideIfOperand — 轴 6 位置维度：条件操作数是**另一条**
// 解析路径（build_condition.go 传 refType(ref, nil)）。状态读落在 `if` 操作数
// 位置同样必须可解析（一条规则覆盖所有位置，无调用点例外）。
// 注：此例在 HEAD 已是绿（条件路径的 reachable 集合恰好覆盖外层 producer），
// 因此它是**防回退**锚点而非 RED——保留它是为了锁住「位置不影响可解析性」。
func TestLoopBodyStateReadInsideIfOperand(t *testing.T) {
	src := workflowHeader + `
function main() -> string {
    int num;
    num = 1;
    for (x in [1, 2, 3]) {
        if (num < 5) {
            num = num + 1;
        } else {
            break;
        }
    }
    return "x";
}
`
	g := buildIR(t, src)
	if len(loopStmtsIn(g)) != 1 {
		t.Fatalf("want 1 loop")
	}
}

// TestNestedLoopBodyStateReadResolvesTypeGlobally — V7（递归自洽）：
// 同一规则在内层容器（loop 内 loop）内仍然成立——单层成立 ≠ 特性成立。
func TestNestedLoopBodyStateReadResolvesTypeGlobally(t *testing.T) {
	src := workflowHeader + `
function main() -> string {
    int num;
    num = 1;
    for (a in [1, 2]) {
        for (b in [3, 4]) {
            let v1 = num + 1;
            num = v1;
        }
    }
    return "x";
}
`
	g := buildIR(t, src)
	loops := loopStmtsIn(g)
	if len(loops) != 2 {
		t.Fatalf("want 2 loops (nested), got %d", len(loops))
	}
	for i, lp := range loops {
		if st := lp.StateTypes["num"]; st == nil || st.Kind != "integer" {
			t.Errorf("loop[%d] (%s) state port num = %v, want integer", i, lp.ID, st)
		}
	}
}

// ── 循环状态 RHS 物化（loop 完整算法 §5 轴 6 / 物化方向 §4-3，2026-09-11）──
//
// 规则（无例外）：**循环状态赋值的 RHS 是「值位置」，按与任何其它值位置相同的
// 规则物化**——整个表达式一起物化，操作数全部绑定。
//
// HEAD 上该位置有两条形态特例，都造成**静默语义损失**：
//   - `total = total + x`：只把 `be.Right()`（x）当值、假定左操作数由平台
//     assigner 隐式携带 ⇒ wire `variable_assign.right` 只剩元素口，**累加器
//     操作数被整段丢弃**（r1 变成 `total = item_1;` 且此后文本“收敛”在错语义上）；
//   - `total = x + total`：`be.Right()` 解析成**同容器同名状态口** ⇒ 命中
//     「状态自赋 = no-op」的 skip 分支 ⇒ **整条赋值消失**（下一轮再编译
//     `COMPILE_ERROR value expression *ast.IdentExpr`）。
//
// 第三条不健全处：`状态 + <int 字面量>` 的 increment 形态特例在左操作数**不
// 可解析**时仍然发形参却不绑定（shim 引用不存在的输入 = 静默 null）。

// loopBodyAssign 返回循环体内唯一的 loop-state 赋值节点。
func loopBodyAssign(t *testing.T, g *ir.Graph, loopID string) *ir.Assign {
	t.Helper()
	var found *ir.Assign
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			switch st := s.(type) {
			case *ir.NodeStmt:
				if as, ok := st.Node.(*ir.Assign); ok && as.LoopState && as.LoopID == loopID {
					if found != nil {
						t.Fatalf("want 1 loop-state assign, got more")
					}
					found = as
				}
			case *ir.IfStmt:
				walk(st.Then)
				walk(st.Else)
			}
		}
	}
	for _, ls := range loopStmtsIn(g) {
		if ls.ID == loopID {
			walk(ls.Body)
		}
	}
	if found == nil {
		t.Fatalf("loop %s 体内缺 loop-state 赋值（整条赋值被静默丢弃？）", loopID)
	}
	return found
}

// codeRunIn 在图里按 ID 找 CodeRun。
func codeRunIn(g *ir.Graph, id string) *ir.CodeRun {
	var found *ir.CodeRun
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			switch st := s.(type) {
			case *ir.NodeStmt:
				if c, ok := st.Node.(*ir.CodeRun); ok && c.ID == id {
					found = c
				}
			case *ir.IfStmt:
				walk(st.Then)
				walk(st.Else)
			case *ir.LoopStmt:
				walk(st.Body)
			}
		}
	}
	if g != nil && g.Root != nil {
		walk(g.Root)
	}
	return found
}

// TestLoopStateAssignAccumulatorKeepsBothOperandsLeftFirst — `total = total + x`：
// 累加器操作数（状态口）与迭代元素口**都必须**绑定到 RHS 物化节点。
func TestLoopStateAssignAccumulatorKeepsBothOperandsLeftFirst(t *testing.T) {
	src := workflowHeader + `
function main(int[] nums) -> int {
    int total;
    total = 0;
    for (x in nums) {
        total = total + x;
    }
    return total;
}
`
	g := buildIR(t, src)
	loops := loopStmtsIn(g)
	if len(loops) != 1 {
		t.Fatalf("want 1 loop, got %d", len(loops))
	}
	loopID := loops[0].ID
	as := loopBodyAssign(t, g, loopID)
	if as.Value.BlockID == "" {
		t.Fatalf("loop-state assign 的 RHS 未绑定任何 producer（值被丢空）")
	}
	shim := codeRunIn(g, as.Value.BlockID)
	if shim == nil {
		t.Fatalf("RHS producer %s 不是 CodeRun（RHS 必须整体物化）", as.Value.BlockID)
	}
	stateBound, elemBound := false, false
	for _, in := range shim.Inputs {
		if in.BlockID == loopID && len(in.Path) == 1 {
			if in.Path[0] == "total" {
				stateBound = true
			}
			if in.Path[0] == "x" {
				elemBound = true
			}
		}
	}
	if !stateBound {
		t.Errorf("RHS 物化缺**累加器**操作数（状态口 total）——`total +` 被丢弃: inputs=%+v code=%q", shim.Inputs, shim.Code)
	}
	if !elemBound {
		t.Errorf("RHS 物化缺迭代元素口 x: inputs=%+v code=%q", shim.Inputs, shim.Code)
	}
}

// TestLoopStateAssignAccumulatorKeepsAssignmentRightFirst — `total = x + total`：
// 累加器在右侧时**不得**命中「状态自赋 no-op」而整条赋值消失。
func TestLoopStateAssignAccumulatorKeepsAssignmentRightFirst(t *testing.T) {
	src := workflowHeader + `
function main(int[] nums) -> int {
    int total;
    total = 0;
    for (x in nums) {
        total = x + total;
    }
    return total;
}
`
	g := buildIR(t, src)
	loops := loopStmtsIn(g)
	if len(loops) != 1 {
		t.Fatalf("want 1 loop, got %d", len(loops))
	}
	as := loopBodyAssign(t, g, loops[0].ID)
	if as.Value.BlockID == "" {
		t.Fatalf("loop-state assign 的 RHS 未绑定任何 producer")
	}
	shim := codeRunIn(g, as.Value.BlockID)
	if shim == nil {
		t.Fatalf("RHS producer %s 不是 CodeRun", as.Value.BlockID)
	}
	stateBound := false
	for _, in := range shim.Inputs {
		if in.BlockID == loops[0].ID && len(in.Path) == 1 && in.Path[0] == "total" {
			stateBound = true
		}
	}
	if !stateBound {
		t.Errorf("RHS 物化缺累加器操作数（状态口 total）: inputs=%+v code=%q", shim.Inputs, shim.Code)
	}
}

// TestLoopStateAssignIncrementShimBindsLeftOperand — `total = (x + 1) + 1` 形态：
// increment 形态特例在左操作数**不可直接解析**时不得发出未绑定的形参
// （旧实现在这里产出引用不存在输入的 shim = 静默 null）。
func TestLoopStateAssignIncrementShimBindsLeftOperand(t *testing.T) {
	src := workflowHeader + `
function main(int[] nums) -> int {
    int total;
    total = 0;
    for (x in nums) {
        total = (x + 1) + 1;
    }
    return total;
}
`
	g := buildIR(t, src)
	loops := loopStmtsIn(g)
	if len(loops) != 1 {
		t.Fatalf("want 1 loop, got %d", len(loops))
	}
	as := loopBodyAssign(t, g, loops[0].ID)
	if as.Value.BlockID == "" {
		t.Fatalf("loop-state assign 的 RHS 未绑定任何 producer")
	}
	shim := codeRunIn(g, as.Value.BlockID)
	if shim == nil {
		t.Fatalf("RHS producer %s 不是 CodeRun", as.Value.BlockID)
	}
	if len(shim.Inputs) == 0 {
		t.Fatalf("RHS 物化节点没有任何输入绑定（shim 形参会在运行期悬空）: code=%q", shim.Code)
	}
	for _, in := range shim.Inputs {
		if in.BlockID == "" {
			t.Errorf("RHS 输入 %q 未绑定 producer（悬空形参）", in.Path)
		}
	}
}

// TestYieldPortNameMayNotCollideWithLoopState — 口名撞门的 lowering 侧门禁。
//
// ncoda 只有**一个标识符空间**：`yield acc = …` 的口名与循环状态变量名同名
// 时，平台产物会同时出现 `variableParameters.acc`（状态）与 `node_outputs.acc`
// （收集口）——平台侧两个命名空间可同名并存，反编译侧却无法无歧义还原
// （该名字在 wire 上既指状态又指收集口，静默择一 = R0≍C0 语义 + 类型双漂移，
// 见 cozebiz RS-LOOP-003）。故在 lowering 边界拒绝（与反编译侧对称），
// 要求作者改名。
func TestYieldPortNameMayNotCollideWithLoopState(t *testing.T) {
	src := workflowHeader + `
function main(int[] nums) -> string[] {
    int acc;
    acc = 0;
    let o = for (x in nums) {
        acc = x;
        yield acc = "v";
    };
    return o;
}
`
	res := passes.DefaultPipeline().Run(src, "t.dc", nil)
	if res.HasErrors() {
		t.Fatalf("frontend pipeline 不应在 lowering 之前报错")
	}
	if res.Program == nil {
		t.Fatal("frontend pipeline produced no TypedBoundProgram")
	}
	_, err := ir.Build(res.Program)
	if err == nil {
		t.Fatalf("yield 口名与循环状态名撞名必须 fail loud（不可还原形态）")
	}
	if !strings.Contains(err.Error(), "collides with the loop state variable") {
		t.Errorf("诊断必须说明撞名，实际: %v", err)
	}
}

// TestYieldPortNameDistinctFromStateStaysGreen — 防回退对照：口名与状态名**不同**
// 时（同一容器既有状态又有收集口，合法形态）必须照常构建。
func TestYieldPortNameDistinctFromStateStaysGreen(t *testing.T) {
	src := workflowHeader + `
function main(int[] nums) -> string[] {
    int acc;
    acc = 0;
    let o = for (x in nums) {
        acc = x;
        yield pieces = "v";
    };
    return o;
}
`
	g := buildIR(t, src)
	loops := loopStmtsIn(g)
	if len(loops) != 1 {
		t.Fatalf("want 1 loop, got %d", len(loops))
	}
	if loops[0].StateTypes["acc"] == nil {
		t.Errorf("状态口 acc 应仍在容器口集内: %+v", loops[0].StateTypes)
	}
}
