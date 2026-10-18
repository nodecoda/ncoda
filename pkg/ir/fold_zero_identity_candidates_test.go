package ir_test

// fold_zero_identity_candidates_test.go — IR 优化阶段「恒等转发零值候选重定向」
// 的语义锁（2026-09-13，见 pkg/ir/fold_zero_identity_candidates.go 头注）。
//
// 两个方向都必须锁住（只锁一侧会被错规则满足）：
//   · 该折叠 ⇒ 候选直引零值真相（relay 不再被登记为候选）；
//   · 共享零值真相（同一真相被**多个**汇聚点登记）**同样折叠** —— 曾按
//     「raising 无法证明候选角色（互斥臂）」收窄过，2026-09-13 以 671 份语料
//     A/B **证伪**（撤掉收窄：真拓扑差异 36 → 33，零新增，c2 仍 670/670
//     SUCCEEDED）；收窄已删除。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

const zeroIdentityHeader = "@language nodecoda/1\n@mode workflow\n"

// identityRelayIn 返回块内（不递归）的恒等转发 relay CodeRun。
func identityRelayIn(block *ir.Block) *ir.CodeRun {
	if block == nil {
		return nil
	}
	for _, s := range block.Stmts {
		ns, ok := s.(*ir.NodeStmt)
		if !ok {
			continue
		}
		if cr, ok := ns.Node.(*ir.CodeRun); ok && cr.Identity {
			return cr
		}
	}
	return nil
}

// recordCarrierNodeIn 返回块内（不递归）的**记录载体** CodeRun —— 编译器自建的
// 整值记录恒等复制（`recordRelayInArm` 的产物：Synthetic、非 Identity、**带容器口**
// （口类型带字段描述））。
//
// 为什么它与 identityRelayIn 分列两个判据：记录字段组的臂门控载体**必须**是整值
// 记录（一条载体承载全部字段）—— 逐字段标量 relay（口名 `result`）会让各字段组
// **同位候选的生产者不同序** ⇒ 反编译侧记录单元判据①/②不成立 ⇒ 记录被读散成逐字段
// 标量（R-13；该拼写 2026-09-28 退役，正本 =
// `platforms/cozebiz/decompile/record_unit.go` 判据①）。
func recordCarrierNodeIn(block *ir.Block) *ir.CodeRun {
	if block == nil {
		return nil
	}
	for _, s := range block.Stmts {
		ns, ok := s.(*ir.NodeStmt)
		if !ok || ns.Node == nil {
			continue
		}
		cr, ok := ns.Node.(*ir.CodeRun)
		if !ok || !cr.Synthetic || cr.Identity || cr.ZeroOf != nil {
			continue
		}
		for _, port := range cr.OutOrder {
			if t := cr.OutTypes[port]; t != nil && len(t.Props) > 0 {
				return cr
			}
		}
	}
	return nil
}

// zeroCarrierNodeIn 返回块内（不递归）的零值载体 CodeRun（`ZeroOf != nil` 的
// 纯常量 shim —— 判据与 pkg/ir 的 isZeroCarrierNode 同尺）。
func zeroCarrierNodeIn(stmts []ir.Stmt) *ir.CodeRun {
	for _, s := range stmts {
		ns, ok := s.(*ir.NodeStmt)
		if !ok {
			continue
		}
		if cr, ok := ns.Node.(*ir.CodeRun); ok && cr.ZeroOf != nil {
			return cr
		}
	}
	return nil
}

// armGroupRefs 返回 if 宿主 junction 第一个组的候选（测试形状只有一个组）。
func armGroupRefs(t *testing.T, stmt *ir.IfStmt) []ir.Ref {
	t.Helper()
	if stmt == nil || stmt.Merge == nil {
		t.Fatalf("宿主没有 junction：%#v", stmt)
	}
	if len(stmt.Merge.Groups) == 0 {
		t.Fatalf("junction 没有组：%#v", stmt.Merge)
	}
	return stmt.Merge.Groups[0].Refs
}

func refProducer(refs []ir.Ref, id string) bool {
	for _, r := range refs {
		if r.Producer == id {
			return true
		}
	}
	return false
}

func firstIfIn(stmts []ir.Stmt) *ir.IfStmt {
	for _, s := range stmts {
		if v, ok := s.(*ir.IfStmt); ok {
			return v
		}
	}
	return nil
}

// 迁移记录（2026-09-24，记录整值折叠 R-43 §42.3 A 轴落地）：
// 本文件三个夹具的 RT 从**单字段**改为**双字段**。原因不是放宽断言，而是：
// 单字段且仅成员读 ⇒ 记录整值折叠（`pkg/ir/record_fold.go`）把绑定折成
// **叶单元**，该形状从此走**标量**通道（junction_phi.go「标量形未写臂：继承
// junction 前绑定」），恒等转发 relay 在这一侧**本来就不产生** ⇒ 本文件要钉的
// 「record 形未写臂的 relay / 零值直引」这一侧会变成**空锚**（守着一个不存在的
// 形状）。加第二个字段 = 让该形状落在折叠域**之外**（A 轴前置 S1「单字段」），
// 于是 record 通道 + relay 存活，锚点重新指向**真实**存在的形状。
// 两个字段的读数（本刀实测）：非零前绑定 ⇒ else 臂 2 条恒等 relay（逐字段一条，
// 口名 result）；可证零值前绑定 ⇒ else 臂空、候选直引零值真相的**同名字段口**。
//
// 迁移记录（2026-09-28，R-125 同族「默认位是否门控」收口）：
// 两个「存活侧」夹具（NegativeNonZeroBase / IdentityRelayCodeMatchesCanonicalShim）
// 的**臂内写值**由 `{ "a": "x", "b": "y" }`（两字段皆非零）改为
// `{ "a": "", "b": "y" }`（首字段写类型零值）。原因不是放宽断言，而是：这两条
// 判据**自己写明的理由**是「`v = "z"` + true 臂 `v = ""` 时会把 "z" 误当该臂的值」
// —— 即 relay 的语义承载性来自**臂写了零**；旧夹具两字段皆非零 ⇒ relay 在取值上
// 与「候选直引前绑定」**同值**，夹具与理由脱节（成了**空锚**：钉着一个不需要
// 存在的形状）。判据落地成「默认位的臂门控以**本单元有没有作者写的零值**为条件」
// （`armGateCandidates(cands, authoredZero)`）后，该脱节被机械暴露。
// **判据语义一字未改**：仍是「非零前绑定 + 未写臂 ⇒ 该臂必须物化恒等 relay、
// 且折叠不得把它折成直引」——只是夹具回到该理由真正覆盖的形状（含零写）。
// 零写侧仍在同文件 RedirectsToZeroTruth 里相对钉住（可证零值前绑定 ⇒ 不物化 relay）。
//
// TestFoldZeroIdentityCandidatesRedirectsToZeroTruth：record 的零值前绑定 +
// 未写臂 ⇒ 不物化恒等转发 relay，该臂的候选**直引零值真相**（前绑定的同名字段口）。
//
// 迁移记录（2026-09-19）：本测试原先钉「未写臂里应有恒等转发 relay，再由本 pass
// 重定向到零值真相」。零值豁免落地后（materializeBranchDefault / gateArmCandidate
// 同一条判据）relay 不再被物化 —— **重定向被提前到了构造期**，折叠终点成了
// 构造期的原地形态；本 pass 在该形状上因此必须是**幂等**的。
//
// 候选**仍按臂数**（此处 2 条），不是 1 条 —— 这一侧有机器判据，实测：
// 删掉零值候选 ⇒ 单候选 merge ⇒ `TestRenderIfAggregateNoPseudoPort` 红
// （"a hoisted if/else phi must list the value source of EVERY path"）。
// 零值由 merge 的「候选缺省 = 类型零」在运行期承担，**不是**靠删候选承担。
// 存活侧锚点（非零前绑定仍必须物化 relay 且不得折叠）见
// TestFoldZeroIdentityCandidatesNegativeNonZeroBase；折叠正向判据由手工 IR
// 精确钉住，见 TestFoldZeroIdentityCandidatesSharedZeroTruthAlsoFolds。
func TestFoldZeroIdentityCandidatesRedirectsToZeroTruth(t *testing.T) {
	g := buildIR(t, zeroIdentityHeader+`type RT = { string a; string b; };
function main(bool cond) -> string {
    RT v;
    v = { "a": "", "b": "" };
    if (cond) {
        v = { "a": "x", "b": "y" };
    }
    return v.a;
}
`)
	host := firstIfIn(g.Root.Stmts)
	// ① 可证零值前绑定 ⇒ 不物化恒等转发 relay（P04 零值豁免）。
	if relay := identityRelayIn(host.Else); relay != nil {
		t.Fatalf("可证零值前绑定不得物化恒等转发 relay（P04 零值豁免）：%#v", host.Else)
	}
	truth := zeroCarrierNodeIn(g.Root.Stmts)
	if truth == nil {
		t.Fatalf("夹具应有零值前绑定载体（RT 全零记录字面量）：%#v", g.Root.Stmts)
	}
	// ② 候选数 == 臂数（2）：未写臂直引零值真相的同名字段口。
	refs := armGroupRefs(t, host)
	if len(refs) != 2 {
		t.Fatalf("候选数必须 == 臂数（2）；单候选 merge 不是 φ（merge 的候选↔臂对应是 wire 契约）：%#v", refs)
	}
	if refs[1].Producer != truth.ID || refs[1].Output != "a" {
		t.Fatalf("未写臂候选应直引零值真相的字段口 %s.a，got %#v（truth=%s）", truth.ID, refs[1], truth.ID)
	}
	// ③ 重定向已提前到构造期 ⇒ 本 pass 在该形状上幂等。
	ir.FoldZeroIdentityCandidates(g)
	after := armGroupRefs(t, host)
	if len(after) != 2 || after[1] != refs[1] {
		t.Fatalf("折叠在零值豁免形状上必须幂等：before=%#v after=%#v", refs, after)
	}
}

// TestFoldZeroIdentityCandidatesNegativeNonZeroBase：**存活侧锚点** —— 前绑定
// 非零时 relay 是语义承载的（merge 不得在错误路径上看到 junction 前的绑定），
// 折叠它就是改语义（`v = "z"` + true 臂 `v = ""` 时会把 "z" 误当该臂的值）。
func TestFoldZeroIdentityCandidatesNegativeNonZeroBase(t *testing.T) {
	g := buildIR(t, zeroIdentityHeader+`type RT = { string a; string b; };
function main(bool cond) -> string {
    RT v;
    v = { "a": "z", "b": "w" };
    if (cond) {
        v = { "a": "", "b": "y" };
    }
    return v.a;
}
`)
	host := firstIfIn(g.Root.Stmts)
	// 双字段夹具 ⇒ 臂门控载体是**一条记录载体**（整值复制，2026-09-28 起；旧的
	// 「逐字段标量 relay」拼写已退役 —— 见 recordCarrierNodeIn 头注）。
	carrier := recordCarrierNodeIn(host.Else)
	if carrier == nil {
		t.Fatalf("非零前绑定 + 臂写零的未写臂必须物化**记录载体**（整值记录复制）：%#v", host.Else)
	}
	ir.FoldZeroIdentityCandidates(g)
	// 候选分散在**各字段组**里：判据 = 至少一条候选仍指向该载体（不得被折叠成
	// 「直引前绑定」—— 那会把臂外的非零基座放到错误路径上）。
	refs := allGroupRefs(host)
	if !refProducer(refs, carrier.ID) {
		t.Fatalf("非零前绑定的候选被折叠了（改语义）：carrier=%s refs=%#v", carrier.ID, refs)
	}
}

// allGroupRefs 收集该 junction 全部字段组的候选（迁移记录见文件头：双字段夹具
// 下候选按字段分散在多个组里）。
func allGroupRefs(stmt *ir.IfStmt) []ir.Ref {
	if stmt == nil || stmt.Merge == nil {
		return nil
	}
	var out []ir.Ref
	for _, grp := range stmt.Merge.Groups {
		out = append(out, grp.Refs...)
	}
	return out
}

// TestFoldZeroIdentityCandidatesSharedZeroTruthAlsoFolds：**共享零值真相也折叠**
// —— 同一零值真相被两个汇聚点登记时，两者的候选**都**重定向到真相。手工 IR
// 精确钉住该判据（不依赖任何前端形状）；负向（非零源保留）见
// TestFoldZeroIdentityCandidatesNegativeNonZeroBase。
func TestFoldZeroIdentityCandidatesSharedZeroTruthAlsoFolds(t *testing.T) {
	shim := func(id, code string) *ir.CodeRun {
		return &ir.CodeRun{
			ID: id, Code: code, Language: "python", Synthetic: true,
			OutTypes: map[string]*ir.Type{"result": ir.T("string")}, OutOrder: []string{"result"},
		}
	}
	zero := shim("100002", "# ncoda:shim 1\ndef main() -> dict:\n    return {\"result\": \"\"}")
	zero.ZeroOf = ir.T("string")
	relay := shim("100003", "# ncoda:shim 1\ndef main(arg1) -> dict:\n    return {\"result\": arg1}")
	relay.Identity = true
	relay.Inputs = []ir.Source{{Path: []string{"arg1"}, BlockID: "100002", Name: "result"}}
	group := func(producer string) []ir.AggregateGroup {
		return []ir.AggregateGroup{{Name: "a", Refs: []ir.Ref{{Producer: producer, Output: "result"}}, Type: ir.T("string")}}
	}
	armWithRelay := func() *ir.Block { return &ir.Block{Stmts: []ir.Stmt{&ir.NodeStmt{Node: relay}}} }

	// 共享：汇聚点 A 的候选 = relay（→真相 100002）；汇聚点 B **直接**登记 100002。
	aggA := &ir.Aggregate{ID: "100010", Groups: group("100003")}
	aggB := &ir.Aggregate{ID: "100012", Groups: group("100002")}
	g := &ir.Graph{Entry: &ir.Start{ID: "100001"}, Root: &ir.Block{Stmts: []ir.Stmt{
		&ir.NodeStmt{Node: zero},
		&ir.IfStmt{ID: "100004", Cond: &ir.Selector{ID: "100004"}, Then: armWithRelay(), Else: &ir.Block{}, Merge: aggA},
		&ir.IfStmt{ID: "100011", Cond: &ir.Selector{ID: "100011"}, Then: &ir.Block{}, Else: &ir.Block{}, Merge: aggB},
	}}}
	ir.FoldZeroIdentityCandidates(g)
	if got := aggA.Groups[0].Refs[0].Producer; got != "100002" {
		t.Fatalf("共享零值真相也应折叠到真相：候选 producer = %s, want 100002", got)
	}
	if got := aggB.Groups[0].Refs[0].Producer; got != "100002" {
		t.Fatalf("共享零值真相的另一个登记点应保持直引真相：候选 producer = %s, want 100002", got)
	}

	// 独占：同一个 relay 的零值真相只被一个汇聚点登记 ⇒ 同样折叠到真相。
	solo := &ir.Aggregate{ID: "100020", Groups: group("100003")}
	g2 := &ir.Graph{Entry: &ir.Start{ID: "100001"}, Root: &ir.Block{Stmts: []ir.Stmt{
		&ir.NodeStmt{Node: zero},
		&ir.IfStmt{ID: "100021", Cond: &ir.Selector{ID: "100021"}, Then: armWithRelay(), Else: &ir.Block{}, Merge: solo},
	}}}
	ir.FoldZeroIdentityCandidates(g2)
	if got := solo.Groups[0].Refs[0].Producer; got != "100002" {
		t.Fatalf("独占零值真相应折叠到真相：候选 producer = %s, want 100002", got)
	}
}

// TestIdentityRelayCodeMatchesCanonicalShim：**机器门禁** —— `Identity` 是本 pass
// 识别恒等转发的结构标注；标注打错（标在非恒等节点上）会让折叠改语义。门禁断言
// 「标了 Identity ⇒ Code 逐字等于规范恒等 shim」，并且**必须不是瞎的**（负向例：
// 手工篡改文本 ⇒ 门禁报出该节点）。
func TestIdentityRelayCodeMatchesCanonicalShim(t *testing.T) {
	// 非零前绑定 ⇒ 未写臂必须门控（零值豁免只对可证零值生效），本测试要的正是
	// 「真实产物里真有 Identity 节点」这一侧。
	// **夹具必须是标量单元**：记录单元的臂门控载体自 2026-09-28 起是**记录载体**
	// （整值复制，非 Identity —— 见 recordCarrierNodeIn 头注），那条路上的
	// 「恒等 relay」拼写已退役；标量单元仍走 `identityRelayInArm`。
	g := buildIR(t, zeroIdentityHeader+`function main(string q, int k) -> int {
    int v = k;
    if (q == "a") { v = 0; }
    return v;
}
`)
	host := firstIfIn(g.Root.Stmts)
	relay := identityRelayIn(host.Else)
	if relay == nil {
		t.Fatalf("非零前绑定 + 臂写零的未写臂里应有恒等转发 relay：%#v", host.Else)
	}
	if got := ir.IdentityRelayInvariantViolations(g); len(got) != 0 {
		t.Fatalf("真实产物的 Identity 节点文本不是规范恒等 shim：%v", got)
	}
	// 负向：篡改后门禁必须报出该节点（否则门禁是瞎的）。
	relay.Code = strings.Replace(relay.Code, "arg1", "arg9", 1)
	got := ir.IdentityRelayInvariantViolations(g)
	if len(got) != 1 || got[0] != relay.ID {
		t.Fatalf("门禁未报出被篡改的 Identity 节点：got=%v want=[%s]", got, relay.ID)
	}
}

// TestIdentityRelayInvariantFlagsArityMismatch：**门禁负向锚点（arity）** ——
// `Identity` 节点必须是「恰好一个输入（arg1）」的恒等转发；输入数 ≠ 1 的节点带
// Identity 标注 = 误标（`identityRelaySource` 会拒绝折叠它 ⇒ 它既不被折叠、
// 又不被门禁报出 = **静默盲点**）。本测试钉住「误标必须被门禁报出」。
func TestIdentityRelayInvariantFlagsArityMismatch(t *testing.T) {
	cr := &ir.CodeRun{
		ID: "100002", Code: "# ncoda:shim 1\ndef main(arg1) -> dict:\n    return {\"result\": arg1}",
		Language: "python", Synthetic: true, Identity: true,
		OutTypes: map[string]*ir.Type{"result": ir.T("string")}, OutOrder: []string{"result"},
	}
	cr.Inputs = []ir.Source{
		{Path: []string{"arg1"}, BlockID: "100001", Name: "result"},
		{Path: []string{"arg2"}, BlockID: "100001", Name: "result"},
	}
	g := &ir.Graph{Entry: &ir.Start{ID: "100001"}, Root: &ir.Block{Stmts: []ir.Stmt{&ir.NodeStmt{Node: cr}}}}
	got := ir.IdentityRelayInvariantViolations(g)
	if len(got) != 1 || got[0] != "100002" {
		t.Fatalf("Identity 但输入数 != 1 的节点必须被门禁报出（误标 = 既不折叠也不报 = 静默盲点）：got=%v", got)
	}
}

// TestFoldZeroIdentityCandidatesRelayChainDepth2：**relay 链深 ≥2** ——
// `zeroTruthOf` 沿唯一输入链回溯（上限 64），所以「relay 的输入仍是 relay」时
// 也必须一路走到零值真相。当前前端**产不出**这样的链（每个 junction 的
// pre-binding 是零值载体本身，不是另一个 relay），故按手工 IR 精确钉住（同
// 共享真相测试的做法）——S2-gate B3 的覆盖项。
func TestFoldZeroIdentityCandidatesRelayChainDepth2(t *testing.T) {
	relayShim := func(id, src string) *ir.CodeRun {
		cr := &ir.CodeRun{
			ID: id, Code: "# ncoda:shim 1\ndef main(arg1) -> dict:\n    return {\"result\": arg1}",
			Language: "python", Synthetic: true, Identity: true,
			OutTypes: map[string]*ir.Type{"result": ir.T("string")}, OutOrder: []string{"result"},
		}
		cr.Inputs = []ir.Source{{Path: []string{"arg1"}, BlockID: src, Name: "result"}}
		return cr
	}
	zero := &ir.CodeRun{
		ID: "100002", Code: "# ncoda:shim 1\ndef main() -> dict:\n    return {\"result\": \"\"}",
		Language: "python", Synthetic: true,
		OutTypes: map[string]*ir.Type{"result": ir.T("string")}, OutOrder: []string{"result"},
	}
	zero.ZeroOf = ir.T("string")
	inner := relayShim("100003", "100002") // 第一跳
	outer := relayShim("100004", "100003") // 第二跳（输入仍是 relay）
	agg := &ir.Aggregate{ID: "100010", Groups: []ir.AggregateGroup{{
		Name: "a", Type: ir.T("string"),
		Refs: []ir.Ref{{Producer: "100004", Output: "result"}},
	}}}
	g := &ir.Graph{Entry: &ir.Start{ID: "100001"}, Root: &ir.Block{Stmts: []ir.Stmt{
		&ir.NodeStmt{Node: zero},
		&ir.NodeStmt{Node: inner},
		&ir.NodeStmt{Node: outer},
		&ir.IfStmt{ID: "100011", Cond: &ir.Selector{ID: "100011"}, Then: &ir.Block{}, Else: &ir.Block{}, Merge: agg},
	}}}
	ir.FoldZeroIdentityCandidates(g)
	if got := agg.Groups[0].Refs[0].Producer; got != "100002" {
		t.Fatalf("两跳 relay 链应一路折叠到零值真相：候选 producer = %s, want 100002", got)
	}
}
