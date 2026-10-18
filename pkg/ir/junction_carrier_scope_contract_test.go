package ir

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// junction_carrier_scope_contract_test.go —— 「**载体按 joinKind 各有实物**」的机器判据
// （正本 = `docs/five-step/spec/J-join-canonical-form.md` §56；J §3 四 kind 表 · J §4
// 「宿主差异必须是数据」 · `platforms/cozestudio/ir/junction_kinds.go` 头注同一条立场）。
//
// 为什么这是判据而不是风格：`J §7` 第 2 步的原文写成「`loopBack` / `exit` 各有一条自己的
// 通路 ⇒ **收进 φ 核心 `assembleArmJunction`**」。那句话把「核心」定义错了 ——
// `assembleArmJunction` 是 **`phi` 这一个 kind 的载体构造器**（它建 `Aggregate`），
// 而 `loopBack` 的载体是**平台容器状态口**、`exit` 的是 **`End.FlowExitMerge`**。
// 载体是**平台物理**，按 kind 各有实物；把它强行归一到 φ 的构造器 = 违反 J §4
// （宿主差异必须是数据，不是代码分支），并会逼出一层「公共载体抽象」（本仓明禁的新层）。
//
// 真正必须共享的三面（kind 判定 / 槽规划 / 家族语义）另有判据：
//   · 槽类型权威 —— `slot_type_authority_contract_test.go`（含循环状态槽那一半）；
//   · φ 组装序列唯一 —— `junction_core_single_implementation_test.go`；
//   · raising 侧 kind 名册唯一 —— `platforms/cozestudio/ir/junction_dispatch_single_source_contract_test.go`。
//
// 本判据补的正是「载体边界」这第四面，锁两半：
//
//	① **φ 载体的消费面** = 恰好那 4 个 φ 宿主（`emitIf` / `emitSwitch` / `emitRequestInput` /
//	   `emitAttempt`；三元结构上走 `emitIf` ⇒ 是第 5 宿主但不是第 5 个调用点）。
//	   多一个 ⇒ 有非 φ 宿主借了 φ 的载体；少一个 ⇒ 有 φ 宿主绕过了核心。
//	② **loopBack 载体的写点** ⊆ {`emitLoopNode`（作者槽）, `materializeLoopReturn`（M17 合成槽）}
//	   —— 容器状态口只由 loopBack 的载体代码写，别的宿主不得往里塞槽。

// phiCarrierConstructor 是 φ kind 的载体构造器（唯一实现，`junction_phi.go`）。
const phiCarrierConstructor = "assembleArmJunction("

// phiCarrierHosts 是允许调用 φ 载体构造器的函数（J §3 `phi` 行的五个宿主在代码上的落点）。
// 三元（第 5 宿主）由 `emitTernaryJunction` 合成 `IfStmt` 后调 `emitIf` ⇒ 它不是独立调用点；
// 它的「宿主身份」由判据 `TestJunctionCore*` 与各宿主契约测试承担。
var phiCarrierHosts = map[string]bool{
	"emitIf":           true,
	"emitSwitch":       true,
	"emitRequestInput": true,
	"emitAttempt":      true,
}

// loopBackCarrierWriters 是允许写容器状态口（`Loop.StateTypes` / `Loop.StateValues`）的函数。
// 两者都是 loopBack 这一 kind 的载体代码：前者装配**作者写的**状态槽，后者物化 M17
// （循环体内 `return`）需要的**编译器合成槽**（`returned` / `return_value`）。
var loopBackCarrierWriters = map[string]bool{
	"emitLoopNode":          true,
	"materializeLoopReturn": true,
}

// goFuncNameOf 取 `func (g *Builder) name(` / `func name(` 的 name（与
// `junction_core_single_implementation_test.go::funcNameOf` 同一口径）。
func goFuncNameOf(decl string) string {
	rest := strings.TrimPrefix(strings.TrimSpace(decl), "func ")
	if strings.HasPrefix(rest, "(") {
		if i := strings.Index(rest, ")"); i >= 0 {
			rest = strings.TrimSpace(rest[i+1:])
		}
	}
	if i := strings.Index(rest, "("); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest)
}

// callSitesOf 返回 src 里含 needle 的**行**所在顶层函数名（`func ` 行本身不计 —— 那是定义）。
// 解析口径钝（go 源码按行扫、`func ` 必须顶格），与同族判据同风格：不引第三方解析器。
func callSitesOf(name, src, needle string) []string {
	var out []string
	fn := ""
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(line, "func ") {
			fn = goFuncNameOf(line)
			continue // 定义行含 needle（函数名本身），不计为调用点
		}
		if strings.Contains(line, needle) {
			out = append(out, fn)
		}
	}
	return out
}

// carrierWriteSitesOf 返回 src 里**写**容器状态口的行所在顶层函数名。
// 写 = `StateTypes[...] =` / `StateValues[...] =`（`OutTypes[x] = StateTypes[x]` 这类**读**不算）。
var (
	stateTypesWriteRe  = regexp.MustCompile(`StateTypes\[[^\]]*\]\s*=[^=]`)
	stateValuesWriteRe = regexp.MustCompile(`StateValues\[[^\]]*\]\s*=[^=]`)
)

func carrierWriteSitesOf(src string) []string {
	var out []string
	fn := ""
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(line, "func ") {
			fn = goFuncNameOf(line)
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue // 注释不计数（本仓口径：判据只看代码）
		}
		if stateTypesWriteRe.MatchString(line) || stateValuesWriteRe.MatchString(line) {
			out = append(out, fn)
		}
	}
	return out
}

// ── ① φ 载体的消费面 = 恰好那 4 个 φ 宿主 ──────────────────────────────────

func TestPhiCarrierIsConsumedOnlyByPhiHosts(t *testing.T) {
	srcs := junctionPackageSources(t)
	var sites []string
	for _, s := range srcs {
		for _, fn := range callSitesOf(s.name, s.src, phiCarrierConstructor) {
			sites = append(sites, fmt.Sprintf("%s:%s", s.name, fn))
		}
	}
	if len(sites) == 0 {
		t.Fatalf("判据自检失败：本包里 %q 一个调用点都没扫到（marker 写错 / 载体被搬走 ⇒ 判据恒真）",
			phiCarrierConstructor)
	}
	if len(sites) != len(phiCarrierHosts) {
		t.Fatalf("φ 载体的调用点必须**恰好**是 %d 个 φ 宿主，实测 %d 个：%s\n"+
			"  ⇒ 多一个 = 有非 φ 宿主（loopBack / exit / barrier）借了 φ 的载体；\n"+
			"  ⇒ 少一个 = 有 φ 宿主绕过核心自建载体（J §7 第 1 步的收口会退回去）。\n"+
			"  ⇒ 载体按 joinKind 各有实物：loopBack = 容器状态口，exit = End.FlowExitMerge（J §56）。",
			len(phiCarrierHosts), len(sites), strings.Join(sites, " · "))
	}
	for _, s := range sites {
		i := strings.LastIndex(s, ":")
		if !phiCarrierHosts[s[i+1:]] {
			t.Fatalf("φ 载体被非 φ 宿主调用：%s\n"+
				"  ⇒ 允许的宿主 = %v（J §3 `phi` 行）", s, keysOf(phiCarrierHosts))
		}
	}
}

// ── ② loopBack 载体的写点 ⊆ loopBack 的载体代码 ────────────────────────────

func TestLoopBackCarrierIsWrittenOnlyInLoopBackLowering(t *testing.T) {
	srcs := junctionPackageSources(t)
	var hits []string
	for _, s := range srcs {
		for _, fn := range carrierWriteSitesOf(s.src) {
			hits = append(hits, fmt.Sprintf("%s:%s", s.name, fn))
		}
	}
	if len(hits) == 0 {
		t.Fatalf("判据自检失败：一个容器状态口写点都没扫到（正则失效 ⇒ 判据恒真）")
	}
	for _, h := range hits {
		i := strings.LastIndex(h, ":")
		if !loopBackCarrierWriters[h[i+1:]] {
			t.Fatalf("容器状态口（loopBack 的载体）被本 kind 之外的代码写：%s\n"+
				"  ⇒ 允许的写点 = %v\n"+
				"  ⇒ loopBack 的载体是容器状态口（platform `variableParameters` + `node_outputs`）；\n"+
				"     别的 joinKind 的槽不得塞进容器（J §56 / J §4）。", h, keysOf(loopBackCarrierWriters))
		}
	}
}

// ── 两条判据的自检（锁两半：抓得住违规 + 判得出「没扫到」）──────────────────

func TestJunctionCarrierScopeCheckerLocksBothHalves(t *testing.T) {
	// ① 非 φ 宿主借 φ 载体 ⇒ 必须被抓。
	borrow := []namedSource{{name: "build_stmt.go", src: "package p\n\nfunc (g *Builder) emitLoopNode() {\n" +
		"\tg.assembleArmJunction(arms, nil, nil, nil, \"\")\n}\n"}}
	if got := callSitesOf(borrow[0].name, borrow[0].src, phiCarrierConstructor); len(got) != 1 || got[0] != "emitLoopNode" {
		t.Fatalf("自检失败：非 φ 宿主借载体没被抓到（got=%v）⇒ 判据对真违规不敏感", got)
	}
	// ② φ 宿主正常调用 ⇒ 放行；定义行不得被算成调用点（否则永远多一个）。
	ok := []namedSource{{name: "junction_phi.go", src: "package p\n\nfunc (g *Builder) assembleArmJunction(arms []junctionArm) {}\n\n" +
		"func (g *Builder) emitIf() {\n\tg.assembleArmJunction(nil)\n}\n"}}
	got := callSitesOf(ok[0].name, ok[0].src, phiCarrierConstructor)
	if len(got) != 1 || got[0] != "emitIf" {
		t.Fatalf("自检失败：定义行被误算成调用点（got=%v）", got)
	}
	// ③ marker 失效 ⇒ 0 命中必须可被看见（由 TestPhiCarrierIsConsumedOnlyByPhiHosts 的前置兜住）。
	if got := callSitesOf("x.go", "package p\n", phiCarrierConstructor); len(got) != 0 {
		t.Fatalf("自检失败：空源被判成命中（got=%v）", got)
	}
	// ④ 状态口写点正则：写要抓、读不许抓（`OutTypes[x] = StateTypes[x]` 是读）。
	writes := "package p\n\nfunc (g *Builder) emitLoopNode() {\n" +
		"\tloop.StateTypes[\"v\"] = ti\n\tloop.StateValues[\"v\"] = src\n\tloop.OutTypes[\"v\"] = loop.StateTypes[\"v\"]\n}\n\n" +
		"func (g *Builder) someoneElse() {\n\tloop.StateTypes[\"x\"] = ti\n}\n"
	gotW := carrierWriteSitesOf(writes)
	if len(gotW) != 3 || gotW[0] != "emitLoopNode" || gotW[2] != "someoneElse" {
		t.Fatalf("自检失败：状态口写点扫描口径不对（want emitLoopNode/emitLoopNode/someoneElse，got=%v）", gotW)
	}
	// 注释里的写点不计数（否则文档示例会让判据假红）。
	if got := carrierWriteSitesOf("package p\n\nfunc f() {\n\t// loop.StateTypes[\"v\"] = ti\n}\n"); len(got) != 0 {
		t.Fatalf("自检失败：注释被算成写点（got=%v）", got)
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
