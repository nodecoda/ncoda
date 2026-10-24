package compile

// branch_port_obligation_e2e_test.go —— 空臂 if 的分支端口边必须**显式落地**（P4）。
//
// 一手现场（2026-09-28，672 源判决面 `branch invoke run error` 一族 10 源）：
//
//	if (!empty(v.url)) { } else { <插件调用> }     // 空 then 臂
//	<续接语句 / 循环体收尾>
//
// 旧投影把渲染不出节点的那个臂的 selector 当**裸尾**塞进 junction，续接时被连成一条
// **无端口**边 ⇒ 该 selector 只声明了另一个口，运行到这条臂即
// `RSPEC_INVALID: branch source …: $branch "true" has no declared target (have [false])`。
//
// 判据分两面（同一事实，缺一面就有洞）：
//
//	规范形面 = 投影层的 `TestCozeBizProjection{SelectorPortEdgesExplicit,EdgesStayInLayer}`
//	           （端口显式 + 边不跨层）
//	运行期面 = 本文件：**两个分支方向都真跑一遍**。只声明一个口在投影层看不出来
//	           （没那条边就没得判），只有运行时按 `$branch` 路由才暴露。
//
// 三个落点各一形（对应投影里三个 drain 点）：
//
//	① 空 then 臂 + 续接语句      → 端口边落到续接语句
//	② 两臂都渲染不出节点          → true/false 两口都落到续接语句
//	③ 空臂在**容器（loop）体内**收尾 → 端口边落到容器（inline-input 口），
//	   一手对账 = coze-biz 产物同一形状：
//	   `condNode --"true"--> loop (target_port loop-function-inline-input)`

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
)

// ① 空 then 臂 + 续接语句。
const branchPortEmptyThenSrc = `@language nodecoda/1
@mode workflow
function main(int d = 1) -> string {
    output("a");
    if (d == 1) {
    } else {
        output("b");
    }
    output("c");
    return "done";
}
`

// ② 两臂都渲染不出节点（`let` 被折叠 ⇒ 两臂都没有节点）。
const branchPortBothArmsEmptySrc = `@language nodecoda/1
@mode workflow
function main(int d = 1) -> string {
    output("a");
    if (d == 1) {
        let q = 1;
    } else {
        let r = 2;
    }
    output("c");
    return "done";
}
`

// ③ 空臂在 loop 体内收尾（体内最后一个产出节点的语句 = 那个 if）。
const branchPortInLoopBodySrc = `@language nodecoda/1
@mode workflow
function main(string raw, int d = 1) -> array<string> {
    let xs = json.parse<array<string>>(raw);
    var outs = for (x in xs) {
        output("in:" + x);
        let v = http("GET", "https://example.com", {});
        if (d == 1) {
        } else {
            output("else:" + x);
        }
        yield output = v.body;
    };
    return outs;
}
`

// TestBranchPortObligationEmptyThenArm —— ①。
//
// 撤实现必红（2026-09-28 实测）：去掉 `deriveCtx.owePort/drainPorts`（空臂 selector
// 退回裸尾进 junction）⇒ 本判据在 `d=1`（空臂方向）报
// `$branch "true" has no declared target (have [false])`。
func TestBranchPortObligationEmptyThenArm(t *testing.T) {
	s := qaLoopFromSource(t, branchPortEmptyThenSrc)
	r := mustCompile(t, New(nodes.StdRegistry()), s)
	// 两个方向都要走：只声明一个口时，漏的那一侧才会炸。
	mustInvoke(t, r, map[string]any{"d": int64(1)})
	mustInvoke(t, r, map[string]any{"d": int64(2)})
}

// TestBranchPortObligationBothArmsEmpty —— ②：两口都必须声明（缺一个就错一半）。
//
// 撤实现必红（2026-09-28 实测）：只声明 true 口（去掉 `!elseHad` 那半）⇒
// `d=2` 一侧报 `$branch "false" has no declared target (have [true])`。
// 注意「**一个口都不声明**」在运行期**不会报错**（没有端口边就没注册分支，selector
// 退化成普通节点、无脑走唯一后继）—— 那种退化只有**投影层**判据 ③ 抓得住
// （`…SelectorPortEdgesExplicit`），这也是两面判据缺一不可的原因。
func TestBranchPortObligationBothArmsEmpty(t *testing.T) {
	s := qaLoopFromSource(t, branchPortBothArmsEmptySrc)
	r := mustCompile(t, New(nodes.StdRegistry()), s)
	mustInvoke(t, r, map[string]any{"d": int64(1)})
	mustInvoke(t, r, map[string]any{"d": int64(2)})
}

// TestBranchPortObligationInLoopBody —— ③：端口边必须落在容器**内部**。
//
// 撤实现必红（2026-09-28 实测，两个独立变异各红一次）：
//
//	· 去掉 `deriveLoop` 的容器尾 drain（`drainPortsFrom(portMark, s.ID, inlineIn)`）
//	  ⇒ 义务穿出容器 ⇒ `Compile` 报 `RSPEC_INVALID: edge from unknown node "100006"`
//	  （建图段就红，不必到 Invoke）；
//	· 去掉空臂端口义务本身 ⇒ `Invoke` 报 `$branch "true" has no declared target`。
func TestBranchPortObligationInLoopBody(t *testing.T) {
	s := qaLoopFromSource(t, branchPortInLoopBodySrc)
	r := mustCompile(t, New(nodes.StdRegistry(), WithDeps(nodes.Deps{HTTP: gateStubHTTP{}})), s)
	mustInvoke(t, r, map[string]any{"raw": `["a","b"]`, "d": int64(1)})
	mustInvoke(t, r, map[string]any{"raw": `["a","b"]`, "d": int64(2)})
}

// ④ 空臂在 **intent（switch）** 的多臂里（2026-09-28 第二刀）。
//
// 与 ①②③ 同一条规则、**不同的宿主**：上一刀只修了 `IfStmt` 的条件节点，
// `SwitchStmt` 的 merge-less 路径仍把宿主当裸尾 ⇒ 空臂的 `branch_N` 没有 target。
const branchPortEmptyIntentArmSrc = `@language nodecoda/1
@mode workflow
function main(string q) -> string {
    output("a");
    intent (q) -> it {
        "图片地址": { output("hit-image"); }
        "null": { }
    }
    output("c");
    return "done";
}
`

// ⑤ 空臂在 **attempt** 的失败臂里，且该 attempt 的值**逃逸**（有出口 φ）。
//
// 两个缺陷叠在一起：① 失败臂渲染不出节点 ⇒ 旧投影给的是**无端口**边
// （`$branch "fail-branch" has no declared target (have [source])`）；
// ② 该臂的臂端 End 与出口 φ 都在，端口边必须落在 φ 上（落到臂端 End
// 就让失败路径直接走到 END，续接语句永不执行）。
const branchPortEmptyAttemptFailSrc = `@language nodecoda/1
@mode workflow
function main() -> string {
    output("a");
    string g;
    g = "";
    string h;
    h = "before";
    attempt http("GET", "https://example.com", {}) as resp {
        success {
            g = "ok";
        }
        failure(e) {
        }
    }
    output("c");
    h = "after";
    return h;
}
`

// zzFailHTTP 让 HTTP 走**失败**分支（非 2xx + FailBranch ⇒ $branch="fail-branch"）。
type zzFailHTTP struct{}

func (zzFailHTTP) Do(_ context.Context, _, _ string, _ map[string]string, _ any) (int, []byte, error) {
	return 500, []byte(`{"error":"boom"}`), nil
}

// TestBranchPortObligationEmptyIntentArm —— ④：intent 空臂的 `branch_N` 必须有 target。
//
// 撤实现必红（2026-09-28 实测）：去掉 `SwitchStmt` merge-less 路径的 `owePort`
// ⇒ `q="null"`（命中空臂 branch_1）一侧报
// `$branch "branch_1" has no declared target (have [branch_0])`。
func TestBranchPortObligationEmptyIntentArm(t *testing.T) {
	s := qaLoopFromSource(t, branchPortEmptyIntentArmSrc)
	r := mustCompile(t, New(nodes.StdRegistry()), s)
	// 三个方向都要走：命中 branch_0（有节点）/ branch_1（空臂）/ default（无命中）。
	for _, q := range []string{"图片地址", "null", "谁都不是"} {
		out := mustInvoke(t, r, map[string]any{"q": q})
		raw, _ := json.Marshal(out)
		// 空臂也必须**越过** intent 走到续接语句（`return "done"`）——
		// 端口边缺失时该方向要么报错、要么在这里看不见结果。
		if !strings.Contains(string(raw), `"done"`) {
			t.Fatalf("q=%q 没有走到续接语句：%s", q, raw)
		}
		t.Logf("q=%q → %s", q, raw)
	}
}

// TestBranchPortObligationEmptyAttemptFailureArm —— ⑤：attempt 空失败臂。
//
// 撤实现必红（2026-09-28 实测）：去掉 `AttemptStmt` 空臂的端口义务 ⇒ 走失败路径
// 时 `$branch "fail-branch" has no declared target (have [source])`。
func TestBranchPortObligationEmptyAttemptFailureArm(t *testing.T) {
	s := qaLoopFromSource(t, branchPortEmptyAttemptFailSrc)
	r := mustCompile(t, New(nodes.StdRegistry(), WithDeps(nodes.Deps{HTTP: zzFailHTTP{}})), s)
	out := mustInvoke(t, r, map[string]any{})
	raw, _ := json.Marshal(out)
	// 失败路径必须**越过** attempt 继续执行到续接语句（`h = "after"`）——
	// 端口边若落到臂端 End，失败路径就直接走到 END，续接永不执行。
	if !strings.Contains(string(raw), `"after"`) {
		t.Fatalf("失败路径没有走到续接语句：%s", raw)
	}
	t.Logf("failure path → %s", raw)
}
