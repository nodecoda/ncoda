package ir_test

// record_fold_contract_test.go —— 记录整值折叠（R-43 / §42.3 **A 值形态轴**）的契约。
//
// 正本 = `docs/five-step/spec/J-join-canonical-form.md` §42.3（规范形两条轴）·
// §42.5（实现切点）· §42.6（判据三件套）。实现 = `pkg/ir/record_fold.go`
// （判定）+ `build_call.go::emitVarDeclAs`（写重写 / 类型权威）+ `build.go::resolveValueRef`
// （成员读落在单元本身）+ `build_expr.go::arrayElemParamName`（C 轴命名在 shim 参数名上的投影）。
//
// **撤实现必红**（三条各自独立，撤任一条本文件即红）：
//  ① 撤 `recordFold` 判定（前置/账单）      ⇒ 正例退回整值记录（recordCarriers 1、有汇聚组）；
//  ② 撤 `emitVarDeclAs` 的字面量重写        ⇒ 写路径仍是记录字面量（recordCarriers 1）；
//  ③ 撤 `resolveValueRef` 的叶单元分支      ⇒ 成员读解析到记录口路径（出口引用不再是参数口）。
//
// 本文件只测**A 轴**（写什么形态）；B 轴（承载/落点）与 C 轴（单元名）各自的判据见
// §42.7 与 mid_branch_record_phi / d2_family4 等既有契约测试。

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
)

const recordFoldHeader = "@language nodecoda/1\n@mode workflow\n"

// buildIRForNativeTarget 走 **coze-biz** 前端 profile 建 IR：本文件的夹具用 `file<...>`
// （A 轴的代表形状就是文件族的单叶记录），dify 前端会以
// 「single-file Code input is not JSON-safe for the Dify target」拒收 —— 那是
// dify 的目标能力边界（本仓已裁定搁置），不是本契约的形状问题。
func buildIRForNativeTarget(t *testing.T, src string) *ir.Graph {
	t.Helper()
	base := passes.DefaultTargetProfile()
	profile := &passes.TargetProfile{
		Name:                                 "native-fixture",
		Operations:                           base.Operations,
		MaxParallelism:                       base.MaxParallelism,
		Capabilities:                         passes.DefaultCapabilityManifest(),
		AgentProfiles:                        base.AgentProfiles,
		ParameterExtractorFunctionCallModels: base.ParameterExtractorFunctionCallModels,
		ParameterExtractorMemoryMaxTurns:     base.ParameterExtractorMemoryMaxTurns,
		Codec:                                base.Codec,
	}
	res := passes.DefaultPipeline().Run(src, "record_fold.dc", profile)
	if res.HasErrors() {
		for _, d := range res.Diagnostics {
			t.Logf("pipeline %d %s: %s", d.Code, d.Severity, d.Message)
		}
		t.Fatalf("frontend pipeline reported errors for source:\n%s", src)
	}
	if res.Program == nil {
		t.Fatal("frontend pipeline produced no TypedProgram")
	}
	g, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("ir.Build: %v", err)
	}
	return g
}

// recordFoldSrc 是本族的**代表形状**（生成器 `file_family/d1/s2` 同构）：
// 单叶字段记录、只成员读、只字面量写、两条臂写同一个 producer。
const recordFoldSrc = `type FF = { file<audio> slot; };
function main(int n = 3, file<audio> fp1 = file("")) {
    FF r1;
    r1 = { "slot": fp1 };
    if (n == 2) {
        r1 = { "slot": fp1 };
    }
    return r1.slot;
}
`

// recordFoldLeafSpellingSrc = 同一语义的**叶标量拼写**（§42.3 等价判据的对照侧）。
const recordFoldLeafSpellingSrc = `function main(int n = 3, file<audio> fp1 = file("")) {
    file<audio> r1;
    r1 = fp1;
    if (n == 2) {
        r1 = fp1;
    }
    return r1;
}
`

// walkCodeRuns 收集结构树里的全部 CodeRun（含臂尾中继）。
func walkCodeRuns(g *ir.Graph) []*ir.CodeRun {
	var out []*ir.CodeRun
	var walkB func(b *ir.Block)
	var walkS func(s ir.Stmt)
	walkS = func(s ir.Stmt) {
		switch v := s.(type) {
		case *ir.NodeStmt:
			if cr, ok := v.Node.(*ir.CodeRun); ok {
				out = append(out, cr)
			}
		case *ir.IfStmt:
			if cr, ok := v.Relay.(*ir.CodeRun); ok {
				out = append(out, cr)
			}
			walkB(v.Then)
			walkB(v.Else)
		case *ir.LoopStmt:
			walkB(v.Body)
		case *ir.ParallelStmt:
			for _, b := range v.Branches {
				walkB(b)
			}
		case *ir.AttemptStmt:
			walkB(v.Success)
			walkB(v.Failure)
		case *ir.InlineStmt:
			// 内联用户函数的体在结构树里（inline 不是删除）：它内部的载体同样要算。
			walkB(v.Body)
		}
	}
	walkB = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			walkS(s)
		}
	}
	walkB(g.Root)
	return out
}

// recordCarriers 返回**记录构造载体**（返回 dict 的自建 shim：任一 OutType 是
// object 且有字段）。折叠生效 ⇒ 空；未折（R-13 整值记录）⇒ 非空。
func recordCarriers(g *ir.Graph) []*ir.CodeRun {
	var out []*ir.CodeRun
	for _, cr := range walkCodeRuns(g) {
		for _, t := range cr.OutTypes {
			if t != nil && len(t.Props) > 0 {
				out = append(out, cr)
				break
			}
		}
	}
	return out
}

// shimBodies 摘出载体的 code 首行（失败信息用，避免整段 shim 文本进日志）。
func shimBodies(crs []*ir.CodeRun) []string {
	out := make([]string, 0, len(crs))
	for _, cr := range crs {
		body := strings.TrimSpace(strings.TrimPrefix(cr.Code, "# ncoda:shim 1"))
		if i := strings.IndexByte(body, '\n'); i >= 0 {
			body = body[:i]
		}
		out = append(out, cr.ID+":"+body)
	}
	return out
}

// renderTextRelays 返回结构树里的 TextProcess 节点（带模板的原生文本节点）——
// 「臂尾中继」的识别形态（旧行为铸出的死载体）。
func renderTextRelays(g *ir.Graph) []string {
	var out []string
	var walkB func(b *ir.Block)
	var walkS func(s ir.Stmt)
	walkS = func(s ir.Stmt) {
		switch v := s.(type) {
		case *ir.NodeStmt:
			if tp, ok := v.Node.(*ir.TextProcess); ok {
				out = append(out, tp.ID+": "+tp.Template)
			}
		case *ir.IfStmt:
			if tp, ok := v.Relay.(*ir.TextProcess); ok {
				out = append(out, "relay "+tp.ID+": "+tp.Template)
			}
			walkB(v.Then)
			walkB(v.Else)
		case *ir.LoopStmt:
			walkB(v.Body)
		case *ir.ParallelStmt:
			for _, b := range v.Branches {
				walkB(b)
			}
		case *ir.InlineStmt:
			walkB(v.Body)
		}
	}
	walkB = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			walkS(s)
		}
	}
	walkB(g.Root)
	return out
}

// irShapeSignature 返回结构树的**规范签名**（节点种类 DFS 序 + 汇聚组名/候选数
// + 臂尾中继有无）—— 用于「两条拼写 ⇒ 同形」的等价判据（IR 面；产物面的更强
// 判据 = `internal/compiler/cozebiz_record_fold_equivalence_contract_test.go` 的
// canon 逐字节同产物）。
func irShapeSignature(g *ir.Graph) string {
	var parts []string
	var walkB func(b *ir.Block)
	var walkS func(s ir.Stmt)
	walkS = func(s ir.Stmt) {
		switch v := s.(type) {
		case *ir.NodeStmt:
			parts = append(parts, fmt.Sprintf("node:%T", v.Node))
		case *ir.IfStmt:
			relay := "-"
			if v.Relay != nil {
				relay = fmt.Sprintf("%T", v.Relay)
			}
			groups := []string{}
			if v.Merge != nil {
				for _, grp := range v.Merge.Groups {
					groups = append(groups, fmt.Sprintf("%s/%d", grp.Name, len(grp.Refs)))
				}
				if len(v.Merge.Groups) == 0 && len(v.Merge.Refs) > 0 {
					groups = append(groups, fmt.Sprintf("%s/%d", v.Merge.RefName, len(v.Merge.Refs)))
				}
			}
			sort.Strings(groups)
			parts = append(parts, fmt.Sprintf("if relay=%s groups=%s", relay, strings.Join(groups, ",")))
			walkB(v.Then)
			walkB(v.Else)
		case *ir.LoopStmt:
			walkB(v.Body)
		default:
			parts = append(parts, fmt.Sprintf("stmt:%T", s))
		}
	}
	walkB = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			walkS(s)
		}
	}
	walkB(g.Root)
	return strings.Join(parts, " | ")
}

// TestRecordFoldLeafUnitCanonicalForm：A 轴正例 —— 单叶字段记录 + 只成员读 +
// 只字面量写 ⇒ **不物化容器**，且**两条拼写同形**（§42.3 等价判据的 IR 面）。
func TestRecordFoldLeafUnitCanonicalForm(t *testing.T) {
	recGraph := buildIRForNativeTarget(t, recordFoldHeader+recordFoldSrc)
	leafGraph := buildIRForNativeTarget(t, recordFoldHeader+recordFoldLeafSpellingSrc)

	if got := recordCarriers(recGraph); len(got) != 0 {
		t.Fatalf("整值语义不可观察的记录不得物化容器（A 轴）；got %d 个记录载体：%v", len(got), shimBodies(got))
	}
	if got := collectAggregates(t, recGraph.Root); len(got) != 0 {
		t.Fatalf("两条臂写同一个 producer ⇒ 无汇聚（值保全，φ 无区分对象）；got %d 个汇聚", len(got))
	}
	if a, b := irShapeSignature(recGraph), irShapeSignature(leafGraph); a != b {
		t.Fatalf("同一语义两条拼写必须同形（§42.3 等价判据）：\n 记录字面量 = %s\n 叶标量     = %s", a, b)
	}
}

// TestRecordFoldWholeValueReadKeepsRecord：**反向锁** —— 整值被观察（`return r1;`）
// ⇒ 必须仍物化整值记录（R-13 形态）。撤这条锁 = 功能倒退。
func TestRecordFoldWholeValueReadKeepsRecord(t *testing.T) {
	g := buildIRForNativeTarget(t, recordFoldHeader+`type FF = { file<audio> slot; };
function main(int n = 3, file<audio> fp1 = file("")) {
    FF r1;
    r1 = { "slot": fp1 };
    if (n == 2) {
        r1 = { "slot": fp1 };
    }
    return r1;
}
`)
	if got := recordCarriers(g); len(got) == 0 {
		t.Fatalf("整值被读（出口整值）⇒ 必须保持整值记录（R-13）；实测无记录载体：%s", irShapeSignature(g))
	}
}

// TestRecordFoldMultiFieldNotFolded：前置 S1（单字段）—— 多字段记录的**命名**
// 属 C 轴未决项，本刀不折（登记，不静默）。
func TestRecordFoldMultiFieldNotFolded(t *testing.T) {
	g := buildIRForNativeTarget(t, recordFoldHeader+`type FF = { file<audio> slot; string tag; };
function main(int n = 3, file<audio> fp1 = file("")) {
    FF r1;
    r1 = { "slot": fp1, "tag": "t" };
    return r1.slot;
}
`)
	if got := recordCarriers(g); len(got) == 0 {
		t.Fatalf("多字段记录不在本刀折叠域（S1）⇒ 必须保持整值记录；实测无记录载体：%s", irShapeSignature(g))
	}
}

// TestRecordFoldNonLeafFieldNotFolded：前置 S2（字段是叶类型）—— 嵌套容器由它
// 自己那一层决定，本刀不折。
func TestRecordFoldNonLeafFieldNotFolded(t *testing.T) {
	g := buildIRForNativeTarget(t, recordFoldHeader+`type FF = { array<file<audio>> slots; };
function main(int n = 3, file<audio> fp1 = file("")) {
    FF r1;
    r1 = { "slots": [fp1] };
    return r1.slots[0];
}
`)
	if got := recordCarriers(g); len(got) == 0 {
		t.Fatalf("叶字段本身是容器 ⇒ 本刀不折（S2）⇒ 必须保持整值记录；实测无记录载体：%s", irShapeSignature(g))
	}
}

// TestRecordFoldNonLiteralWriteNotFolded：前置 P2（写全是记录字面量或成员写）
// —— 写来自调用返回值时不折（值形态不可静态观察）。
func TestRecordFoldNonLiteralWriteNotFolded(t *testing.T) {
	g := buildIRForNativeTarget(t, recordFoldHeader+`type FF = { file<audio> slot; };
function mk(file<audio> v) -> FF {
    return { "slot": v };
}
function main(int n = 3, file<audio> fp1 = file("")) {
    FF r1;
    r1 = mk(fp1);
    return r1.slot;
}
`)
	if got := recordCarriers(g); len(got) == 0 {
		t.Fatalf("写不是记录字面量 ⇒ P2 不成立 ⇒ 必须保持整值记录；实测无记录载体：%s", irShapeSignature(g))
	}
}

// TestRecordFoldOptionalRecordNotFolded：Optional 记录不折（折掉叶单元会丢
// `T?` 标记）—— 与 record_fold.go 的「本刀不做」一致。
func TestRecordFoldOptionalRecordNotFolded(t *testing.T) {
	g := buildIRForNativeTarget(t, recordFoldHeader+`type FF = { file<audio> slot; };
function main(int n = 3, file<audio> fp1 = file("")) {
    FF? r1;
    r1 = { "slot": fp1 };
    return r1.slot;
}
`)
	if got := recordCarriers(g); len(got) == 0 {
		t.Fatalf("Optional 记录不在本刀折叠域 ⇒ 必须保持整值记录；实测无记录载体：%s", irShapeSignature(g))
	}
}

// TestArmNoOpTailRelayNotMaterialized：**臂尾中继的第三条豁免**（纯 no-op 臂）——
// 臂内零节点 ∧ 零重绑定 ∧ 无值 return ⇒ 该臂在值上什么都没做（两侧绑定同一个
// producer）⇒ 不物化中继，与**真·空臂**同形（两臂都空合法，2026-09-13）。
//
// 一手（HEAD 即可复现，与记录折叠无关）：`file<audio> v = fp1; if (c) { v = fp1; } return v;`
// 旧行为铸出**无消费者**的 `text {{v}}` 中继 ⇒ `c0` 4 节点、`c1` 3 节点 ⇒ `C∘R` 无不动点。
// 撤实现（去掉 armNoOp 那条）⇒ 本测试红（中继回来）。
func TestArmNoOpTailRelayNotMaterialized(t *testing.T) {
	noop := buildIRForNativeTarget(t, recordFoldHeader+`function main(file<audio> fp1, bool c) {
    file<audio> v = fp1;
    if (c) {
        v = fp1;
    }
    return v;
}
`)
	empty := buildIRForNativeTarget(t, recordFoldHeader+`function main(file<audio> fp1, bool c) {
    file<audio> v = fp1;
    if (c) {
    }
    return v;
}
`)
	if sig := irShapeSignature(noop); sig != irShapeSignature(empty) {
		t.Fatalf("纯 no-op 臂必须与真·空臂同形（中继是死载体）：\n no-op = %s\n empty = %s", sig, irShapeSignature(empty))
	}
	for _, s := range noop.Root.Stmts {
		if h, ok := s.(*ir.IfStmt); ok && h.Relay != nil {
			t.Fatalf("纯 no-op 臂不得物化臂尾中继（消费者为零的死载体）：relay=%s", h.Relay.NodeID())
		}
	}
	if got := renderTextRelays(noop); len(got) != 0 {
		t.Fatalf("纯 no-op 臂不得铸出 text 中继：%v", got)
	}
}

// TestArmRebindingKeepsMergeCandidates：存活侧锚点 —— 臂**真的重绑定**时，
// 汇聚必须保住两条臂的候选（不得被 no-op 豁免误伤；数据丢失守卫）。
func TestArmRebindingKeepsMergeCandidates(t *testing.T) {
	g := buildIRForNativeTarget(t, recordFoldHeader+`function main(file<audio> fp1, file<audio> fp2, bool c) {
    file<audio> v = fp2;
    if (c) {
        v = fp1;
    }
    return v;
}
`)
	var host *ir.IfStmt
	for _, s := range g.Root.Stmts {
		if h, ok := s.(*ir.IfStmt); ok {
			host = h
		}
	}
	if host == nil || host.Merge == nil {
		t.Fatalf("臂重绑定了 ⇒ 必须有汇聚：%s", irShapeSignature(g))
	}
	refs := allGroupRefs(host)
	if len(refs) < 2 {
		t.Fatalf("汇聚必须保住两条臂的候选（数据丢失守卫）；got %v", refs)
	}
}

// TestArrayElemParamNameFromValueIdentity：C 轴命名在 shim 参数名上的投影 ——
// 参数名是**值身份**的函数：成员路径元素经折叠解析到叶单元后，参数名 = 该单元
// 的**端口名**（= 反编译侧回吐的名字），不是位置名 `argN`。
// 撤实现（回落到位置名）⇒ 本测试红（`s3` 族的 ARTIFACT_DRIFT/PARAM 漂移复发）。
func TestArrayElemParamNameFromValueIdentity(t *testing.T) {
	src := recordFoldHeader + `type FF = { file<audio> slot; };
function main(int n = 3, file<audio> fp1 = file("")) {
    FF r1;
    r1 = { "slot": fp1 };
    let a1 = [r1.slot];
    return a1[0];
}
`
	g2 := buildIRForNativeTarget(t, src)
	var arr *ir.CodeRun
	for _, cr := range walkCodeRuns(g2) {
		if strings.Contains(cr.Code, "[arg") || strings.Contains(cr.Code, "[fp1]") {
			arr = cr
		}
	}
	if arr == nil {
		t.Fatalf("夹具应有数组字面量 shim：%s", irShapeSignature(g2))
	}
	if !strings.Contains(arr.Code, "[fp1]") {
		t.Fatalf("成员路径元素经折叠后参数名必须取**端口名**（值身份），不得回落位置名；got code=%q inputs=%v", arr.Code, arr.Inputs)
	}
}
