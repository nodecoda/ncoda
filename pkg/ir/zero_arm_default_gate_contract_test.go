package ir_test

// zero_arm_default_gate_contract_test.go — φ **默认位**的臂门控以「本单元有没有
// 作者写的类型零值」为条件（2026-09-28，R-125 同族收口）。
//
// 规则（一条，宿主/值类型无关）：
//
//	φ 的每条候选，其产出方必须**只在该候选所属的臂被执行时才存在**。这条对
//	**默认位**（最后一条候选）有例外：本单元**没有任何作者写的类型零值**时，臂外
//	产出方可以照旧充任默认值（J §8 U12：无零写 ⇒ 臂外基座停在无条件位正是规范位）。
//	一旦本单元有可证零值，豁免必须取消 —— 平台的汇聚策略让「没产出候选」与
//	「产出零值」同结果（零候选被跳过、继续往后取），臂外基座在每条路径上都存在
//	⇒ 恒被取中 ⇒ **作者写的零被静默丢掉**。
//
// 一手反例（修复前，`five-step-roundtrip`）= `I1a TEXT_DRIFT/STRUCTURAL@2` +
// `I1b ARTIFACT_DRIFT/TOPOLOGY@2`：
//
//	int v = k; if (q == "a") { v = 0; } return v;
//	⇒ 首编 merge = [零载体（条件前） , 入口参数 k]（两候选都恒存在）⇒ 恒取 k；
//	   反编译把这形态读成「空 if」⇒ 作者的 `v = 0;` 在文本里**整条消失**
//	   （撞「失配 ≠ 静默丢弃」不变量），且 5 节点 → 3 节点。
//
// 记录值是**单元级**判据：记录单元的成立前提是「各字段组**同位候选同生产者**」
// （record_unit.go），故门控必须整条记录**一起做或一起不做**；只按字段各判一次
// 会让一个组门控、另一个组直引 ⇒ 组间候选序列不一致 ⇒ 记录被读散成逐字段标量。
//
// 判据形状（四个子例 = 值类型 {标量, 记录} × 本单元有无作者零值）：
//  1. 有零值 ⇒ 默认位候选的产出方**在臂内**（标量单元 = 臂内恒等 relay；
//     记录单元 = 臂内**记录载体**，2026-09-28 起 —— 见 recordCarrierNodeIn 头注）；
//  2. 无零值 ⇒ 默认位候选的产出方**在臂外**（不物化载体 —— 反向锚点，
//     防判据过宽：这正是 §8 U12 的规范位，门控它只会改形状）。
//
// 本文件读的是 `ir.Build` 的产物（构造期结论）；`SinkBaseCarriers` 等优化 pass
// 在渲染管线里另跑，不由本文件覆盖。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

// directNodeIDsInArm 收集臂块**直接语句**上的节点 id（本文件的夹具臂都是平的；
// 记录形状判据只需要「产出方在不在该臂里」，不递归 —— 递归只会更宽）。
func directNodeIDsInArm(b *ir.Block) map[string]bool {
	out := map[string]bool{}
	if b == nil {
		return out
	}
	for _, s := range b.Stmts {
		ns, ok := s.(*ir.NodeStmt)
		if !ok || ns.Node == nil {
			continue
		}
		out[ns.Node.NodeID()] = true
	}
	return out
}

// defaultPositionGated 报告该组的**默认位**（最后一条）候选的产出方是否落在
// 臂内（= 被臂门控）。组内候选数必须 == 臂数（wire 契约：候选↔臂一一对应）。
func defaultPositionGated(t *testing.T, host *ir.IfStmt, group ir.AggregateGroup, armIDs map[string]bool) bool {
	t.Helper()
	if len(group.Refs) != 2 {
		t.Fatalf("候选数必须 == 臂数（2）：%#v", group.Refs)
	}
	return armIDs[group.Refs[len(group.Refs)-1].Producer]
}

func TestZeroArmDefaultGateScalarAuthoredZero(t *testing.T) {
	g := buildIR(t, zeroIdentityHeader+`function main(string q, int k) -> int {
    int v = k;
    if (q == "a") { v = 0; }
    return v;
}
`)
	host := firstIfIn(g.Root.Stmts)
	if host == nil || host.Merge == nil || len(host.Merge.Groups) != 1 {
		t.Fatalf("夹具应产生单组 junction：%#v", host)
	}
	elseIDs := directNodeIDsInArm(host.Else)
	if !defaultPositionGated(t, host, host.Merge.Groups[0], elseIDs) {
		t.Fatalf("单元内有作者写的零值 ⇒ 默认位必须门控进未写臂；refs=%#v else=%#v",
			host.Merge.Groups[0].Refs, host.Else)
	}
	if identityRelayIn(host.Else) == nil {
		t.Fatalf("默认位门控的载体必须是臂内恒等 relay：%#v", host.Else)
	}
}

func TestZeroArmDefaultGateScalarNoZeroAnchor(t *testing.T) {
	g := buildIR(t, zeroIdentityHeader+`function main(string q, int k) -> int {
    int v = k;
    if (q == "a") { v = 5; }
    return v;
}
`)
	host := firstIfIn(g.Root.Stmts)
	if host == nil || host.Merge == nil || len(host.Merge.Groups) != 1 {
		t.Fatalf("夹具应产生单组 junction：%#v", host)
	}
	elseIDs := directNodeIDsInArm(host.Else)
	if defaultPositionGated(t, host, host.Merge.Groups[0], elseIDs) {
		t.Fatalf("单元内没有作者写的零值 ⇒ 默认位不得门控（§8 U12 规范位）：refs=%#v",
			host.Merge.Groups[0].Refs)
	}
	if relay := identityRelayIn(host.Else); relay != nil {
		t.Fatalf("无零写时未写臂不得物化恒等 relay：%#v", relay)
	}
}

func TestZeroArmDefaultGateRecordUnitUnion(t *testing.T) {
	// 字段 a 写零、字段 b 写非零：单元级并集 ⇒ **两个组同解门控**；
	// 只按字段各判一次会让 b 组直引臂外基座 ⇒ 记录被读散。
	g := buildIR(t, zeroIdentityHeader+`type RC = { int a; string b; };
function main(string q, int k) -> int {
    RC r = { "a": k, "b": "x" };
    if (q == "a") { r = { "a": 0, "b": "y" }; }
    return r.a;
}
`)
	host := firstIfIn(g.Root.Stmts)
	if host == nil || host.Merge == nil || len(host.Merge.Groups) != 2 {
		t.Fatalf("夹具应产生双字段组 junction：%#v", host)
	}
	elseIDs := directNodeIDsInArm(host.Else)
	for _, grp := range host.Merge.Groups {
		if !defaultPositionGated(t, host, grp, elseIDs) {
			t.Fatalf("记录单元有作者零值 ⇒ 每个字段组的默认位都必须门控（同解）："+
				"组 %s refs=%#v else=%#v", grp.Name, grp.Refs, host.Else)
		}
	}
	// 载体形状 = **一条记录载体**（整值记录复制），不是逐字段标量 relay
	// （2026-09-28 退役拼写：各字段组同位候选生产者不同序 ⇒ 记录单元折不回）。
	if carrier := recordCarrierNodeIn(host.Else); carrier == nil {
		t.Fatalf("记录默认位门控的载体必须是臂内**记录载体**：%#v", host.Else)
	} else if relay := identityRelayIn(host.Else); relay != nil {
		t.Fatalf("记录单元不得退化成逐字段标量 relay（carrier=%s relay=%s）", carrier.ID, relay.ID)
	}
}

func TestZeroArmDefaultGateRecordNoZeroAnchor(t *testing.T) {
	g := buildIR(t, zeroIdentityHeader+`type RC = { int a; string b; };
function main(string q, int k, string u) -> int {
    RC r = { "a": k, "b": u };
    if (q == "a") { r = { "a": 5, "b": "u" }; }
    return r.a;
}
`)
	host := firstIfIn(g.Root.Stmts)
	if host == nil || host.Merge == nil || len(host.Merge.Groups) != 2 {
		t.Fatalf("夹具应产生双字段组 junction：%#v", host)
	}
	elseIDs := directNodeIDsInArm(host.Else)
	for _, grp := range host.Merge.Groups {
		if defaultPositionGated(t, host, grp, elseIDs) {
			t.Fatalf("记录单元无作者零值 ⇒ 每个字段组都不得门控（同解）："+
				"组 %s refs=%#v", grp.Name, grp.Refs)
		}
	}
	if relay := identityRelayIn(host.Else); relay != nil {
		t.Fatalf("无零写时未写臂不得物化恒等 relay：%#v", relay)
	}
}

// ── 臂尾值通道（值位置三元 / switch·QA·attempt 的整值汇聚）────────────────────
//
// 同一个规则的第二个通道：`armTailValues`（`pkg/ir/arm_gate_candidate.go`）与
// 赋值通道共用同一条默认位判据（「本单元没有作者写的零值」才豁免）。两通道各写一份
// 默认位政策正是这条缝第一次裂开的地方（一手反例 = 值位置三元，见下）。

// tailRefsOf 取值位置 junction 的整值汇聚候选（legacy 单组通道：`Groups` 为空、
// 候选在 `Refs`）。
func tailRefsOf(t *testing.T, host *ir.IfStmt) []ir.Ref {
	t.Helper()
	if host == nil || host.Merge == nil {
		t.Fatalf("夹具应产生值位置 junction：%#v", host)
	}
	if len(host.Merge.Groups) != 0 {
		t.Fatalf("值位置 junction 应走 legacy 单组通道（Groups 为空）：%#v", host.Merge.Groups)
	}
	if len(host.Merge.Refs) != 2 {
		t.Fatalf("候选数必须 == 臂数（2）：%#v", host.Merge.Refs)
	}
	return host.Merge.Refs
}

func TestZeroArmDefaultGateTailValueZeroAnchor(t *testing.T) {
	// 一手反例（修复前）：`(q=="a") ? "" : v` ⇒ 默认位（else 臂）的臂外产出方
	// `v`（非零基座）未被门控 ⇒ 两条路径都返回基座，作者写的 `""` 被吞掉。
	g := buildIR(t, zeroIdentityHeader+`function main(string q) -> string {
    string v = "z";
    string x = (q == "a") ? "" : v;
    return x;
}
`)
	host := firstIfIn(g.Root.Stmts)
	refs := tailRefsOf(t, host)
	elseIDs := directNodeIDsInArm(host.Else)
	if !elseIDs[refs[len(refs)-1].Producer] {
		t.Fatalf("单元内有作者写的零值 ⇒ 尾值默认位必须门控进该臂；refs=%#v else=%#v",
			refs, host.Else)
	}
	if identityRelayIn(host.Else) == nil {
		t.Fatalf("尾值默认位门控的载体必须是臂内恒等 relay：%#v", host.Else)
	}
}

func TestZeroArmDefaultGateTailValueNoZeroAnchor(t *testing.T) {
	// 反向锚：两条臂都写非零 ⇒ 本单元无作者零值 ⇒ 默认位保持既有形状
	// （臂外产出方 = 合法默认值；门控它只是多一个载体）。
	g := buildIR(t, zeroIdentityHeader+`function main(string q) -> string {
    string v = "z";
    string u = "y";
    string x = (q == "a") ? v : u;
    return x;
}
`)
	host := firstIfIn(g.Root.Stmts)
	refs := tailRefsOf(t, host)
	elseIDs := directNodeIDsInArm(host.Else)
	if elseIDs[refs[len(refs)-1].Producer] {
		t.Fatalf("本单元无作者零值 ⇒ 尾值默认位不得门控（§8 U12 规范位）：refs=%#v",
			refs)
	}
	if relay := identityRelayIn(host.Else); relay != nil {
		t.Fatalf("无零写时臂内不得物化恒等 relay：%#v", relay)
	}
}
