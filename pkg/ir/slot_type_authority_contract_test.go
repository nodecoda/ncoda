package ir

import (
	"os"
	"strings"
	"testing"
)

// slot_type_authority_contract_test.go —— 「汇聚槽的类型由谁说了算」这一条规则的
// 机器判据（正本 = `docs/five-step/spec/J-join-canonical-form.md` §31）。
//
// 规则（一句话）：槽的类型必须来自**声明**；权威链全缺 ⇒ **fail loud**。
// 判据分三层，缺一层就只锁一半：
//
//	① 判定本身（顺序语义 + 全缺 ⇒ nil）—— `firstSlotType`；
//	② **决定**（无权威时怎么办）—— 消费点必须 fail loud，不得静默跳过；
//	③ 判别自检 —— 判定写成「最后一个胜出」/「跳过声明级」必须被判据区分出来，
//	   否则上面两条可能只是恒真。

// ── ① 判定：顺序即优先级，首个非 nil 胜出 ───────────────────────────────

func TestFirstSlotTypeOrderIsTheOnlyJudgment(t *testing.T) {
	cases := []struct {
		label  string
		levels []slotTypeLevel
		want   string // 期望的 Kind；"" = 无权威
	}{
		{"首个非 nil 胜出（声明级压过后面的生产者口）", []slotTypeLevel{
			{name: "声明类型", typ: func() *Type { return T("number") }},
			{name: "生产者口", typ: func() *Type { return T("string") }},
		}, "number"},
		{"宿主没有的级别（typ == nil）自动下移，不参与判定", []slotTypeLevel{
			{name: "字面量 kind", typ: nil},
			{name: "声明类型", typ: func() *Type { return T("integer") }},
		}, "integer"},
		{"级别存在但取不到类型（typ() == nil）也下移", []slotTypeLevel{
			{name: "声明类型", typ: func() *Type { return nil }},
			{name: "生产者口", typ: func() *Type { return T("boolean") }},
		}, "boolean"},
		{"全缺 ⇒ nil（链的唯一合法 nil 出口）", []slotTypeLevel{
			{name: "声明类型", typ: func() *Type { return nil }},
			{name: "生产者口", typ: nil},
		}, ""},
		{"空级别序列 ⇒ nil", nil, ""},
	}
	for _, c := range cases {
		got := firstSlotType(c.levels)
		if c.want == "" {
			if got != nil {
				t.Fatalf("%s：want nil, got %v", c.label, got)
			}
			continue
		}
		if got == nil || got.Kind != c.want {
			t.Fatalf("%s：want %q, got %v", c.label, c.want, got)
		}
	}
}

// 判别自检（锁两半）：把判定写成「最后一个非 nil 胜出」在**有分歧的输入**上必然
// 与 `firstSlotType` 给出不同答案 ⇒ 本判据不是恒真（标记写错/顺序被反转会被抓）。
func TestFirstSlotTypeJudgmentDiscriminatesOrder(t *testing.T) {
	levels := []slotTypeLevel{
		{name: "声明类型", typ: func() *Type { return T("number") }},
		{name: "生产者口", typ: func() *Type { return T("string") }},
	}
	lastWins := func(ls []slotTypeLevel) *Type {
		var out *Type
		for _, lv := range ls {
			if lv.typ == nil {
				continue
			}
			if v := lv.typ(); v != nil {
				out = v
			}
		}
		return out
	}
	got, wrong := firstSlotType(levels), lastWins(levels)
	if got == nil || wrong == nil || got.Kind == wrong.Kind {
		t.Fatalf("判别自检失败：两种判定的答案必须不同（声明级 number vs 末级 string），实测 %v / %v", got, wrong)
	}
	// 反向：把声明级整个删掉（「跳过声明级」的错实现）也必须在有分歧输入上暴露。
	if skipDecl := firstSlotType(levels[1:]); skipDecl == nil || skipDecl.Kind == got.Kind {
		t.Fatalf("判别自检失败：跳过声明级必须在有分歧输入上给出不同答案，实测 %v", skipDecl)
	}
}

// ── ② 失败形态：唯一、点名构造 + 槽 + 试过的级别 ────────────────────────

func TestSlotFailureErrorsNameTheMissingThing(t *testing.T) {
	levels := []slotTypeLevel{
		{name: "声明类型", typ: nil},
		{name: "生产者口（汇聚前绑定）", typ: nil},
		{name: "生产者口（臂内写的声明端口）", typ: nil},
	}
	msg := missingSlotTypeError("convergence slot", "v1", levels).Error()
	for _, want := range []string{"convergence slot", `"v1"`, "声明类型", "生产者口（汇聚前绑定）", "生产者口（臂内写的声明端口）"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("诊断必须点名 %q，实测：%s", want, msg)
		}
	}
	if nameMsg := unspellableSlotNameError("convergence slot", "v1").Error(); !strings.Contains(nameMsg, "convergence slot") || !strings.Contains(nameMsg, `"v1"`) {
		t.Fatalf("槽名不可拼写的诊断必须点名构造与槽名，实测：%s", nameMsg)
	}
	// 同一规则一个失败形态：空名字的级别不进诊断（否则作者看到的是空档）。
	if m := missingSlotTypeError("c", "s", []slotTypeLevel{{name: "", typ: nil}, {name: "声明类型", typ: nil}}).Error(); strings.Contains(m, "/ /") || strings.HasPrefix(m, "tried:  ") {
		t.Fatalf("空名字级别不得产生空档：%s", m)
	}
}

// ── ② 同一个数据源：门与组装侧对同一个槽必须给出同一个答案 ─────────────
//
// 门的成员判定（`changedHoistedByShapeArms`）与组装点的通道/组型判定
// （`planArmJunctionPhi` → `junctionPhiType`）今天读的是**同一条链**。契约就是
// 「同解」：任何一边另写一份判定（本仓复发的「一份规则、多份实现」），本判据红。
//
// **无权威时的处置 = fail loud**（2026-09-24 用户拍板「甲先行」，`J §31.7`）：
// 「同一把尺」的推论就是「两边都看得见同一把尺**量不出**」，门的答案必须是
// 拒收，而不是「不做 phi 单元」——后者会让该臂写值静默消失（编译 SUCCEEDED、
// L1/L2 全绿 = AGENTS.md「失配 ≠ 静默丢弃」）。端到端活复现的钉子 =
// `internal/compiler/cozebiz_untyped_decl_type_authority_contract_test.go` §3。
func TestJunctionSlotAuthorityIsTheSameRulerOnBothSides(t *testing.T) {
	binding := map[string]varRef{"v": {nodeID: "n1", output: "out", kind: "code"}}
	arms := []junctionArm{{Port: "true", Vars: binding}}
	declared := map[string]bool{"v": true}
	noOuter := map[string]varRef{}

	// 无权威（无声明注解 / 无汇聚前绑定 / 臂内 producer 的声明端口解析不出）：
	// 链给 nil ⇒ 门不得把这变量当 phi 单元。
	g := &Builder{hoistedVars: map[string]bool{"v": true}}
	if got := g.junctionPhiType("v", noOuter, armVarsOf(arms)); got != nil {
		t.Fatalf("链无权威时 `junctionPhiType` 必须返回 nil，实测 %v", got)
	}
	if got, err := g.changedHoistedByShapeArms(declared, noOuter, []map[string]varRef{binding}, false); err == nil || len(got) != 0 {
		t.Fatalf("链无权威 ⇒ 门必须 fail loud（不得静默跳过、也不得判定为 phi 单元），实测 got=%v err=%v", got, err)
	} else if !strings.Contains(err.Error(), `"v"`) || !strings.Contains(err.Error(), "convergence slot") {
		t.Fatalf("拒收诊断必须点名构造与槽名，实测：%s", err.Error())
	}

	// 有权威（①声明注解）：链给 string ⇒ 门判定为 phi 单元，且 record 槽不收它。
	g2 := &Builder{
		hoistedVars:  map[string]bool{"v": true},
		hoistedTypes: map[string]*Type{"v": T("string")},
	}
	if got := g2.junctionPhiType("v", noOuter, armVarsOf(arms)); got == nil || got.Kind != "string" {
		t.Fatalf("有声明注解时链必须给出 string，实测 %v", got)
	}
	got, err := g2.changedHoistedByShapeArms(declared, noOuter, []map[string]varRef{binding}, false)
	if err != nil {
		t.Fatalf("有权威时门不得拒收，实测 err=%v", err)
	}
	if len(got) != 1 || got[0] != "v" {
		t.Fatalf("有权威的臂写变量必须是 phi 单元，实测 %v", got)
	}
	if rec, err := g2.changedHoistedByShapeArms(declared, noOuter, []map[string]varRef{binding}, true); err != nil || len(rec) != 0 {
		t.Fatalf("标量形不得进 record 槽，实测 rec=%v err=%v", rec, err)
	}
}

// 判定只有一处：`junctionPhiType` 必须是薄壳（委托 `firstSlotType` + 链数据），
// 不得就地手写「按序取第一个非 nil」——那正是第二份判定的出生形态。
func TestJunctionPhiTypeDelegatesToTheSingleJudgment(t *testing.T) {
	src, err := os.ReadFile("junction_phi.go")
	if err != nil {
		t.Fatalf("read junction_phi.go: %v", err)
	}
	body := funcBodyText(string(src), "func (g *Builder) junctionPhiType(")
	if body == "" {
		t.Fatalf("找不到 junctionPhiType 的函数体")
	}
	if !strings.Contains(body, "firstSlotType(") {
		t.Fatalf("junctionPhiType 必须委托 firstSlotType（一处判定），实测函数体：\n%s", body)
	}
	for _, forbidden := range []string{"for ", "range "} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("junctionPhiType 不得就地遍历级别（那是第二份判定，实测含 %q）：\n%s", forbidden, body)
		}
	}
}

// 同一把尺的**另一个消费点**：循环状态槽（`build_stmt.go::emitLoopNode`）。
// 它消费同一条链的判定与失败形态；若有人退回手写 if-else 级联（第二份判定），
// 本判据红。判定口径 = 「构造级别序列 ∧ 交给共享判定 ∧ 失败走共享形态」三件缺一即红。
func loopStateChainViolations(src string) []string {
	body := funcBodyText(src, "func (g *Builder) emitLoopNode(")
	var missing []string
	for _, want := range []string{"levels := []slotTypeLevel{", "firstSlotType(levels)", "missingSlotTypeError("} {
		if !strings.Contains(body, want) {
			missing = append(missing, want)
		}
	}
	return missing
}

func TestLoopStateSlotConsumesTheSharedJudgment(t *testing.T) {
	src, err := os.ReadFile("build_stmt.go")
	if err != nil {
		t.Fatalf("read build_stmt.go: %v", err)
	}
	if m := loopStateChainViolations(string(src)); len(m) > 0 {
		t.Fatalf("循环状态槽必须消费共享判定（缺 %v）：级别序列 = 本宿主的数据，判定与失败形态在 slot_type_authority.go（J §31）", m)
	}
	// 判据自检（锁两半）：重构前的写法（手写 if-else 级联 + 就地 fail loud 文案）
	// 必须被判成违规 —— 否则上面那条可能只是「文件里恰好有这些字符串」的恒真。
	const before = `func (g *Builder) emitLoopNode() {
		var ti *Type
		if dt := g.varDeclTypes[v]; dt != nil {
			ti = dt
		} else if dt := g.hoistedTypes[v]; dt != nil {
			ti = dt
		}
		if ti == nil {
			return nil, firstPort, varRef{}, &UnsupportedError{Construct: fmt.Sprintf(
				"loop state %q: no authoritative type (declared type / literal / producer port all missing)", v)}
		}
	}`
	if m := loopStateChainViolations(before); len(m) != 3 {
		t.Fatalf("判据自检失败：重构前的写法必须三项全缺，实测缺 %v", m)
	}
}

// funcBody 取顶层函数（含多行）的形参行到同缩进的第一个 `}`；解析口径钝，
// 与 `junction_core_single_implementation_test.go` 同风格。
func funcBodyText(src, head string) string {
	i := strings.Index(src, head)
	if i < 0 {
		return ""
	}
	lines := strings.Split(src[i:], "\n")
	var body []string
	for _, line := range lines[1:] {
		if line == "}" {
			break
		}
		body = append(body, line)
	}
	return strings.Join(body, "\n")
}

// ── ② 组装点（`planArmJunctionPhi`）：同一把尺的第二消费点，不得静默 continue ──

// 本判据刻意是**结构判据**（不是行为判据）：组装点今天被上游的门挡成不可达
// （门用同一条链过滤），所以「行为上跑不到」不等于「可以退回静默 continue」——
// 一旦门的过滤条件变了，静默 continue 会让同一个缺陷原样复发。
//
// 判据口径 = `junction_phi.go` 里两个「槽成立前提不满足」的分支体必须是 fail loud
// 返回，且不得含 `continue`。
var silentSkipBranches = []struct {
	head string // 分支头（代码文本，不含注释）
	fail string // 该分支体必须出现的 fail-loud 出口
}{
	{"if shape == nil {", "missingSlotTypeError("},
	{`if port == "" {`, "unspellableSlotNameError("},
}

// silentSkipViolations 返回 src 里「以 continue 静默跳过」的分支（`行号: 分支头`）。
// 解析口径 = 分支头行起、到同缩进的第一个 `}` 止；只找 fail-loud 出口与 `continue`。
func silentSkipViolations(src string) []string {
	var out []string
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		for _, b := range silentSkipBranches {
			if line != b.head {
				continue
			}
			indent := len(lines[i]) - len(strings.TrimLeft(lines[i], " \t"))
			var body []string
			for j := i + 1; j < len(lines); j++ {
				t := strings.TrimSpace(lines[j])
				if t == "}" && len(lines[j])-len(strings.TrimLeft(lines[j], " \t")) == indent {
					break
				}
				if strings.HasPrefix(t, "//") {
					continue // 注释不计数（本仓口径：判据只看代码）
				}
				body = append(body, t)
			}
			joined := strings.Join(body, "\n")
			if strings.Contains(joined, "continue") || !strings.Contains(joined, b.fail) {
				out = append(out, strings.TrimSpace(lines[i]))
			}
		}
	}
	return out
}

func TestJunctionAssemblyNeverSkipsSilentlyOnMissingSlotPrerequisite(t *testing.T) {
	src, err := os.ReadFile("junction_phi.go")
	if err != nil {
		t.Fatalf("read junction_phi.go: %v", err)
	}
	if v := silentSkipViolations(string(src)); len(v) > 0 {
		t.Fatalf("φ 组装点不得以 `continue` 静默跳过（槽成立前提不满足 ⇒ fail loud，J §31）：实测 %v", v)
	}
	// 判据自检（锁两半）：旧写法（静默 continue）必须被抓；无 fail-loud 出口也算违规；
	// 正确写法必须放行 —— 三条都过，判据才不是恒真/恒假。
	cases := []struct {
		label string
		src   string
		want  int
	}{
		{"旧写法：静默 continue", "func f() {\n\tif shape == nil {\n\t\tcontinue\n\t}\n}\n", 1},
		{"按注释说的等形状：没有 fail loud 出口", "func f() {\n\tif shape == nil {\n\t\treturn nil\n\t}\n}\n", 1},
		{"正确写法：fail loud", "func f() {\n\tif shape == nil {\n\t\treturn plan, missingSlotTypeError(\"c\", n, levels)\n\t}\n}\n", 0},
		{"正确写法：槽名不可拼写也 fail loud", "func f() {\n\tif port == \"\" {\n\t\treturn plan, unspellableSlotNameError(\"c\", n)\n\t}\n}\n", 0},
	}
	for _, c := range cases {
		if got := silentSkipViolations(c.src); len(got) != c.want {
			t.Fatalf("%s：want %d, got %d (%v)", c.label, c.want, len(got), got)
		}
	}
}
