package ir

// json_builtin_placement_contract_test.go — **一处决策点**的判据（2026-09-27）。
//
// 规则（用户口径 2026-09-27）：「json 是 ncoda 内置的功能，coze-biz **恰巧**有这个节点；
// ncoda 有独立语义和语法，不被平台绑架」。⇒ 落点由**两条独立事实**共同决定，缺一不可：
//
//	① 平台**有没有**原生 json 节点 —— 能力轴 `capability.Table.JSONSerial/JSONDeserial`；
//	② 这个**载荷**能不能逐字放进那个节点 —— `JSON*NativeHostable`。
//
// 本判据锁的就是这句话：**只改能力轴、或只改载荷，结论必须跟着动**（两条都被读到）。
// 撤实现必红（实测）：
//   · `recordJSONBuiltinPlacement` 里把 `hosts && hostable` 改成 `hosts`
//     ⇒ 第 3 格（不可声明载荷必须留 shim）转红；
//   · 把 `hosts` 恒置 true ⇒ 第 4 格（dify 没有承载面）与第 5 格（无能力表）转红。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/capability"
)

// jsonParseShimFor 造一个「载荷可承载」的 json.parse shim（成员名全是平台标识符）。
func jsonParseShimFor(props map[string]*Type) *CodeRun {
	return &CodeRun{
		ID:        "100002",
		Code:      ShimMarker + "import json\ndef main(arg1) -> dict:\n    return {\"output\": json.loads(arg1)}",
		Language:  "python",
		Inputs:    []Source{{Path: []string{"arg1"}, BlockID: "100001", Name: "raw"}},
		OutTypes:  map[string]*Type{"output": TObject(props)},
		OutOrder:  []string{"output"},
		Synthetic: true,
	}
}

// jsonStringifyShim 造一个「载荷可承载」的 json.stringify shim。
func jsonStringifyShim() *CodeRun {
	return &CodeRun{
		ID:        "100003",
		Code:      ShimMarker + "import json\ndef main(arg1) -> dict:\n    return {\"result\": json.dumps(arg1, ensure_ascii=False)}",
		Language:  "python",
		Inputs:    []Source{{Path: []string{"arg1"}, BlockID: "100002", Name: "output"}},
		OutTypes:  map[string]*Type{"result": T("string")},
		OutOrder:  []string{"result"},
		Synthetic: true,
	}
}

func TestRecordJSONBuiltinPlacementFollowsCapabilityAndPayload(t *testing.T) {
	native := nativeCapabilityFixture("native-fixture")
	// A target with no native JSON node: the conservative table is the same
	// surface an adapter must declare when its platform lacks the node.
	conservative := capability.Conservative("uncertified-platform")
	cases := []struct {
		name         string
		caps         *capability.Table
		op           string
		node         Node
		wantDecision string
		wantCause    string
	}{
		{
			"native target · parse · payload hostable ⇒ native node", native, "parse",
			jsonParseShimFor(map[string]*Type{"title": T("string")}),
			PlacementNative, PlacementCauseCapability,
		},
		{
			"native target · stringify · payload hostable ⇒ native node", native, "stringify",
			jsonStringifyShim(),
			PlacementNative, PlacementCauseCapability,
		},
		{
			// 事实②单独生效：平台有节点，但这个载荷放不进去（成员名不是平台标识符，
			// 声明出去会被平台静默丢字段）⇒ 必须留 shim，不许发一个丢字段的原生节点。
			"native target · parse · member name undeclarable ⇒ keep shim", native, "parse",
			jsonParseShimFor(map[string]*Type{"题": T("string")}),
			PlacementShim, PlacementCauseNoNativeNode,
		},
		{
			// 事实①单独生效：载荷再干净，目标没有承载面 ⇒ shim（这正是「平台只有
			// 恰巧才有这个节点」的反面）。
			"无承载面 · parse ⇒ shim", conservative, "parse",
			jsonParseShimFor(map[string]*Type{"title": T("string")}),
			PlacementShim, PlacementCauseNoNativeNode,
		},
		{
			"无能力表 ⇒ 保守留 shim", nil, "parse",
			jsonParseShimFor(map[string]*Type{"title": T("string")}),
			PlacementShim, PlacementCauseNoNativeNode,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := &Builder{caps: tc.caps}
			g.recordJSONBuiltinPlacement("100002", tc.op, tc.node)
			if len(g.placements) != 1 {
				t.Fatalf("期望恰 1 条决策，实得 %d：%#v", len(g.placements), g.placements)
			}
			p := g.placements[0]
			if want := PlacementFamilyJSON + ":" + tc.op; p.Construct != want {
				t.Errorf("construct = %q，期望 %q", p.Construct, want)
			}
			if p.Decision != tc.wantDecision {
				t.Errorf("decision = %q，期望 %q（reason=%q）", p.Decision, tc.wantDecision, p.Reason)
			}
			if p.Cause() != tc.wantCause {
				t.Errorf("cause = %q，期望 %q（reason=%q）", p.Cause(), tc.wantCause, p.Reason)
			}
		})
	}
}

// TestJSONNativeHostableRejectsNonIdentMembers：谓词本身的两面（成员名必须可逐字声明）。
func TestJSONNativeHostableRejectsNonIdentMembers(t *testing.T) {
	ok := jsonParseShimFor(map[string]*Type{"title": T("string")})
	if !ok.JSONParseNativeHostable() {
		t.Fatal("标识符成员应可承载")
	}
	bad := jsonParseShimFor(map[string]*Type{"题": T("string")})
	if bad.JSONParseNativeHostable() {
		t.Fatal("非平台标识符成员不可承载（声明出去会被静默丢字段）")
	}
	// stringify 的载荷条件：单 ref 入参 + 单 string 输出口。
	if !jsonStringifyShim().JSONSerializeNativeHostable() {
		t.Fatal("单 ref 入参 + string 输出应可承载")
	}
	literal := jsonStringifyShim()
	literal.Inputs[0].BlockID = ""
	if literal.JSONSerializeNativeHostable() {
		t.Fatal("字面量直喂（无 ref 绑定）无先例 ⇒ 不得走原生")
	}
}
