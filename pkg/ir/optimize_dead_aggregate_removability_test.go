package ir

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// A12-H8 契约：**判死 ⇒ 必须摘得掉**（optimizeGraph 的不动点收敛不变量）。
//
// 机制：optimizeGraph 是「重算消费视图 → 判死 → 删除」的不动点循环。判死集合来自
// `collectIRNodes` 的**节点视图**（沿结构树收节点，含挂在语句上的汇聚载体），删除由
// `pruneDeadAggregates*` 的**结构树遍历**执行（按语句种类下降、清宿主指针）——
// 两者必须覆盖同一个集合。视图里有一类聚合**不挂在结构树的语句上**，而是挂在
// 某个节点的字段上（图级载体：`ReturnStmt.End.FlowExitMerge`）：删除器漏掉这类
// 载体时，该聚合每轮都被判死、每轮都摘不掉 ⇒ `changed` 恒真 ⇒ **挂死**
// （一手触发路径：一个流程里第二条带值记录出口另建同 id 的 end ⇒ `collectIRNodes`
// 按 id 取最后一个 ⇒ 流程出口 phi 的消费者在视图里看不见 ⇒ 判死却摘不掉；
// 已由 `A12-flow-exit-multi-record` 修掉「同 id 双 end」的出生面，本条守的是
// 机制本身：**不得靠「不会再发生」**）。
//
// 四条断言（正侧 + 收敛侧 + 存活侧 + 递归侧，缺一条就会被错规则满足）：
//  ① 正侧：图级载体上的死聚合 ⇒ 必须**收敛**（摘掉，且不挂死、不报错）；
//  ② 收敛侧：结构树上的死聚合仍必须被摘掉、不报错 —— 只锁 ① 会被「一律报错」满足；
//  ③ 存活侧：被真实数据引用消费的聚合必须**保留** —— 只锁 ①② 会被「一律删」满足；
//  ④ 递归侧：同一判据在**臂内**（结构树更深一层）载体上同样成立；
//  ⑤ 不变量自身：删除面漏掉视图里的载体（视图/删除面**不对称**）⇒ fail loud 并
//     点名该聚合，**不得挂死**（这是 A12-H8 的历史表现：静默不收敛）。

// runOptimize 以**签名无关**的方式调用 optimizeGraph，并把「挂死」变成可判定的失败。
//
// 为什么必须签名无关：本族判据的「撤实现必红」要落在**断言**上。撤实现
// （`git show <commit>:pkg/ir/optimize.go`）会把函数换回**不带返回值**的版本，
// 于是任何 `err := optimizeGraph(g)` 写法的测试在撤实现后只会**编译失败**——而编译
// 失败不构成「撤实现后测试抓到了」（repair_check / mutation_check 的共同口径：
// 那证明的是代码坏，不是判据抓到了规则）。以**语句形式**调用（Go 允许丢弃返回值）
// 加一层 recover，两个版本都能编译。
//
// 为什么还要超时：本条缺陷的历史表现就是**不返回**（不动点循环 `changed` 恒真）。
// 没有超时，撤实现后的 RED 会退化成整包 `go test` 超时（现场看不出是哪条判据），
// 有超时才是「测试自己抓到了挂死」。
func runOptimize(t *testing.T, g *Graph) (panicked string) {
	t.Helper()
	done := make(chan string, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Sprint(r)
				return
			}
			done <- ""
		}()
		optimizeGraph(g)
	}()
	select {
	case msg := <-done:
		return msg
	case <-time.After(15 * time.Second):
		t.Fatalf("optimizeGraph 未在 15s 内收敛：判死的聚合摘不掉 ⇒ changed 恒真 ⇒ 挂死（A12-H8 复发）")
		return ""
	}
}

// oneCandidatePhi 造一个「有一组 phi 候选」的聚合（非零候选 ⇒ 走 Groups 分支，
// 判死走消费普查：没有任何下游数据引用读它的输出口 ⇒ 死）。
func oneCandidatePhi(id, producer string) *Aggregate {
	return &Aggregate{ID: id, Groups: []AggregateGroup{{
		Name:  wGroup1,
		Owner: "v1",
		Refs:  []Ref{{Producer: producer, Output: "output"}},
	}}}
}

// ① 图级载体上的死聚合必须被摘掉（收敛侧正例：不得挂死、不得报错）。
func TestOptimizeDeadFlowExitMergeConvergesInsteadOfHanging(t *testing.T) {
	end := &End{ID: "900001"}
	// FlowExitMerge 挂着，但 End.Inputs 没有引用它 —— 这正是「视图看见节点、
	// 删除器必须也够得着载体」的形状（漏掉它 = 每轮判死 ⇒ 挂死）。
	end.FlowExitMerge = oneCandidatePhi("100009", "100004")
	g := &Graph{Root: &Block{Stmts: []Stmt{&ReturnStmt{ID: "900001", End: end}}}}

	if msg := runOptimize(t, g); msg != "" {
		t.Fatalf("可摘的图级死聚合不得 fail loud（它是脚手架，摘掉即可）：%s", msg)
	}
	if end.FlowExitMerge != nil {
		t.Fatalf("判死的流程出口 phi 必须从图级载体上摘掉，实得 %#v", end.FlowExitMerge)
	}
}

// ② 结构树上的死聚合仍必须被摘掉，且不报错（收敛侧锚点：只锁 ① 会被「一律报错」满足）。
func TestOptimizeStillRemovesDeadAggregateOnTheTree(t *testing.T) {
	g := &Graph{Root: &Block{Stmts: []Stmt{
		&NodeStmt{Node: oneCandidatePhi("100009", "100004")},
	}}}
	if msg := runOptimize(t, g); msg != "" {
		t.Fatalf("结构树上的死聚合是可摘的，不得 fail loud：%s", msg)
	}
	if len(g.Root.Stmts) != 0 {
		t.Fatalf("结构树上的死聚合必须被摘掉，实得 %d 条语句", len(g.Root.Stmts))
	}
}

// ③ 被真实数据引用消费的聚合必须保留（存活侧锚点：只锁 ①② 会被「一律删」满足）。
func TestOptimizeKeepsFlowExitMergeConsumedByEnd(t *testing.T) {
	end := &End{ID: "900001", Inputs: []Ref{{Producer: "100009", Output: wGroup1}}}
	end.FlowExitMerge = oneCandidatePhi("100009", "100004")
	g := &Graph{Root: &Block{Stmts: []Stmt{&ReturnStmt{ID: "900001", End: end}}}}

	if msg := runOptimize(t, g); msg != "" {
		t.Fatalf("被消费的流程出口 phi 是活的，不得 fail loud：%s", msg)
	}
	rs, ok := g.Root.Stmts[0].(*ReturnStmt)
	if !ok || rs.End == nil {
		t.Fatalf("返回语句结构被改动：%#v", g.Root.Stmts[0])
	}
	if rs.End.FlowExitMerge == nil {
		t.Fatal("被 end 消费的流程出口 phi 被误删（判死不成立：它有真实数据消费者）")
	}
}

// ④ 递归侧：同一判据在臂内（结构树更深一层）载体上成立 ——
// 可摘载体清指针、图级载体同样要收敛（不得因为「深了一层」就退化成挂死）。
func TestOptimizeDeadAggregateRemovabilityIsRecursive(t *testing.T) {
	// 4.1 臂内宿主的**结构树**载体：死 ⇒ 清指针。
	inner := &IfStmt{ID: "100003", Merge: oneCandidatePhi("100009", "100004"),
		Then: &Block{}, Else: &Block{}}
	g := &Graph{Root: &Block{Stmts: []Stmt{inner}}}
	if msg := runOptimize(t, g); msg != "" {
		t.Fatalf("臂/嵌套位置的可摘死聚合不得 fail loud：%s", msg)
	}
	if inner.Merge != nil {
		t.Fatalf("臂内死聚合的宿主指针必须被清空，实得 %#v", inner.Merge)
	}

	// 4.2 同一深度上的**图级**载体（臂内 return 的 End.FlowExitMerge）：
	// 同一条判据、同一结果（收敛）。漏掉它 = 深度一变就挂死。
	end := &End{ID: "900002"}
	end.FlowExitMerge = oneCandidatePhi("100010", "100004")
	g2 := &Graph{Root: &Block{Stmts: []Stmt{
		&IfStmt{ID: "100005", Then: &Block{Stmts: []Stmt{
			&ReturnStmt{ID: "900002", End: end},
		}}, Else: &Block{}},
	}}}
	if msg := runOptimize(t, g2); msg != "" {
		t.Fatalf("臂内图级载体的死聚合同样可摘，不得 fail loud：%s", msg)
	}
	if end.FlowExitMerge != nil {
		t.Fatalf("臂内判死的流程出口 phi 必须被摘掉，实得 %#v", end.FlowExitMerge)
	}
}

// ⑤ 不变量自身：视图看得见、删除面够不着的聚合 ⇒ **fail loud 并点名**，不得挂死。
//
// 构造方式：把死聚合放进一个 `Node` 型字段（`IfStmt.Relay`）。节点视图沿语句收
// 「语句上的节点」（`add(v.Relay)`），而删除面只摘**声明的聚合载体槽位**
// （`*Aggregate` 字段与 `*End` 的 FlowExitMerge）——于是这条输入在视图里**可见**、
// 在删除面上**够不着**，正是 A12-H8 的不对称形态本身。
//
// 诚实口径：这是**手工构造**的不对称输入（编译器今天不产出把它放进 Relay 的图；
// 真实触发路径是「同 id 双 end」，出生面已由 A12-flow-exit-multi-record 修掉）。
// 它证明的是**不变量本身**成立（不对称 ⇒ 硬报错而不是静默挂死），不是某个真实
// 产物的形状。撤掉守卫后本用例以**挂死**（15s 超时）红——与 A12-H8 的历史表现一致。
func TestOptimizeFailsLoudWhenTheDeletePathMissesAVisibleCarrier(t *testing.T) {
	g := &Graph{Root: &Block{Stmts: []Stmt{
		&IfStmt{ID: "100003", Relay: oneCandidatePhi("100011", "100004"),
			Then: &Block{}, Else: &Block{}},
	}}}

	msg := runOptimize(t, g)
	if msg == "" {
		t.Fatalf("判死却摘不掉的聚合必须 fail loud（静默通过 = 把「判死」当成了「已删」）")
	}
	if !strings.Contains(msg, "100011") || !strings.Contains(msg, "*ir.Aggregate") {
		t.Fatalf("诊断必须点名判死却仍可见的聚合（id + 类型），实得：%s", msg)
	}
}
