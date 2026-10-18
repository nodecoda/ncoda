package ir

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// junction_core_single_implementation_test.go —— 「完成一个构造」第 2 条
// （`docs/five-step/spec/C-construct-algorithms.md` §2.1）的机器判据：
// **实现 = 算法的一个函数**。
//
// 判据（钝、可锁两半）：本包（shared 层）里，φ 核心的**逐变量汇聚组装序列**
// —— 一个函数体里同时出现下列**全部**标志调用 —— 必须在**恰好一个**函数里出现，
// 且那个函数在 `junctionCoreFile`（核心文件，不是任何宿主文件）里。
//
// 为什么这是判据而不是风格：`join` 的算法是**一份**（`docs/five-step/spec/
// J-join-canonical-form.md` §9），宿主能影响结果的唯一途径是四项**数据**（同文 §4）。
// 同一序列在多个宿主里各写一遍 = 「一份规则、多份实现」—— 本仓 20 天里反复复发的
// 那件事（同文 §0 规模证据 95/131 处、§10 改动边界清单）。
//
// 与同族判据的分工：R-P1（`scripts/guard_audit.py`）管「新增的形态守卫必须登记」；
// 本判据管「φ 核心的组装序列不许出现第二份实现」。**两者都不查行为** ——
// 行为由各宿主的契约测试与 `C∘R∘C` 闭环承担。
const junctionCoreFile = "junction_phi.go"

// junctionCoreMarkers：一条组装序列的**全部**标志调用（缺一不算一份实现）。
var junctionCoreMarkers = []string{"newValueAggregate(", "planArmJunctionPhi("}

// junctionCoreImplementationsIn 返回 src 里「含全部标志调用」的顶层函数名（按出现序）。
// 解析口径 = gofmt 后的顶层 `func ` 行起、到列 0 的 `}` 止：钝，不引第三方解析器。
func junctionCoreImplementationsIn(src string) []string {
	var out []string
	var name string
	var body []string
	inFunc := false
	for _, line := range strings.Split(src, "\n") {
		if !inFunc {
			if strings.HasPrefix(line, "func ") {
				inFunc = true
				name = funcNameOf(line)
				body = nil
			}
			continue
		}
		body = append(body, line)
		if line != "}" {
			continue
		}
		inFunc = false
		joined := strings.Join(body, "\n")
		hasAll := true
		for _, m := range junctionCoreMarkers {
			if !strings.Contains(joined, m) {
				hasAll = false
				break
			}
		}
		if hasAll {
			out = append(out, name)
		}
	}
	return out
}

// funcNameOf 取 `func (recv *T) name(` / `func name(` 里的 name。
func funcNameOf(decl string) string {
	rest := strings.TrimPrefix(decl, "func ")
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

func TestJunctionCoreHasSingleImplementation(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var found []string
	scanned := 0
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		b, err := os.ReadFile(n)
		if err != nil {
			t.Fatalf("read %s: %v", n, err)
		}
		scanned++
		for _, fn := range junctionCoreImplementationsIn(string(b)) {
			found = append(found, n+":"+fn)
		}
	}
	if scanned == 0 {
		t.Fatalf("判据自检失败：一个非测试 .go 文件都没扫到（判据指向错的目标 ⇒ 不允许算「通过」）")
	}
	if len(found) != 1 {
		t.Fatalf("φ 核心的逐变量汇聚组装序列（%s）必须**恰好一个**函数体：实测 %d 个\n"+
			"  实测：%s\n"+
			"  ⇒ 同一算法在多个宿主里各写一遍 = 一份规则、多份实现（J §4 / J §10）。\n"+
			"  ⇒ 修法：把组装序列收回核心函数，宿主只准调用；宿主差异走数据（J §4 四项）。",
			strings.Join(junctionCoreMarkers, " + "), len(found), strings.Join(found, " · "))
	}
	if !strings.HasPrefix(found[0], junctionCoreFile+":") {
		t.Fatalf("φ 核心的唯一实现必须落在 %s（核心文件），实测在 %s\n"+
			"  ⇒ 若确要搬家，改 junctionCoreFile 常量并写明理由（本判据刻意钉位置：核心不许藏在宿主文件里）。",
			junctionCoreFile, found[0])
	}
}

// TestJunctionCoreCheckerLocksBothHalves：判据自检 —— 0 份与 2 份都必须被判成「不是 1」。
// 没有这一条，「恰好一个」可能只是**恒真**（标记写错 ⇒ 永远扫不到东西也算通过）。
func TestJunctionCoreCheckerLocksBothHalves(t *testing.T) {
	cases := []struct {
		label string
		src   string
		want  int
	}{
		{"两份实现（把核心复制回宿主）",
			"package p\n\nfunc a() {\n\tg.newValueAggregate(x)\n\tg.planArmJunctionPhi(y)\n}\n\nfunc b() {\n\tg.newValueAggregate(x)\n\tg.planArmJunctionPhi(y)\n}\n", 2},
		{"零份实现（标记被拆开 / 判据指向错的目标）",
			"package p\n\nfunc a() {\n\tg.newValueAggregate(x)\n}\n\nfunc b() {\n\tg.planArmJunctionPhi(y)\n}\n", 0},
		{"恰好一份",
			"package p\n\nfunc core() {\n\tg.newValueAggregate(x)\n\tg.planArmJunctionPhi(y)\n}\n", 1},
		{"带接收者的一份",
			"package p\n\nfunc (g *Builder) core() {\n\tg.newValueAggregate(x)\n\tg.planArmJunctionPhi(y)\n}\n", 1},
	}
	for _, c := range cases {
		got := junctionCoreImplementationsIn(c.src)
		if len(got) != c.want {
			t.Fatalf("%s：want %d, got %d (%v)", c.label, c.want, len(got), got)
		}
	}
	got := junctionCoreImplementationsIn("package p\n\nfunc (g *Builder) assembleArmJunction() {\n\tg.newValueAggregate(1)\n\tg.planArmJunctionPhi(2)\n}\n")
	if len(got) != 1 || got[0] != "assembleArmJunction" {
		t.Fatalf("函数名解析失败（接收者形态不许取成 \"(g\"）：got %v", got)
	}
}

// ─────────────────────────────────────────────────────────────
// 同一纪律的**文本面第二半**：φ 汇聚**节点本身的裸构造**只许出现在核心文件。
//
// 与上面的「组装序列恰好一个函数体」互补：
//
//	· 上面查**序列**（`newValueAggregate` + `planArmJunctionPhi` 同时出现）
//	  —— 能抓到「把整段组装抄回宿主」，但宿主可以只抄**最后那一步**构造
//	  （HEAD 上 `emitIf` 自己写 `&Aggregate{ID: aggID, Groups: …}`，序列标志
//	  一个都不含）⇒ 序列判据看不见它。2026-09-20 第三刀实测：HEAD 的
//	  `build_return.go:1553/1564` 正是这两条字面量（收编后才消失）。
//	· 本判据查**节点构造字面量**，覆盖那一步。
//
// 覆盖范围（刻意写清，不夸大）：本判据只钉 `phi` 宿主的**臂汇聚节点**构造。
// `exit`（`joinKind=exit`，第 2 步）与 `loopBack` 的载体构造不在此列
// （实测今日：`build_return.go` 的 `end.FlowExitMerge` 与
// `loop_return_materialize.go:187`）。
var junctionNodeConstructions = []string{
	"&Aggregate{ID: aggID, Groups:",
	"&Aggregate{ID: id, Groups:",
}

// namedSource 是一份「有名字的源码」，供判据与其自测共用（避免自测另写一套解析）。
type namedSource struct {
	name string
	src  string
}

// junctionNodeConstructionHits 返回 marker → 核心文件**之外**的命中（`file:line`）
// 与 marker → 全包总命中数（用于「判据恒真」自检）。
func junctionNodeConstructionHits(srcs []namedSource) (map[string][]string, map[string]int) {
	outside := map[string][]string{}
	total := map[string]int{}
	for _, s := range srcs {
		for _, m := range junctionNodeConstructions {
			for i, line := range strings.Split(s.src, "\n") {
				if !strings.Contains(line, m) {
					continue
				}
				total[m]++
				if s.name != junctionCoreFile {
					outside[m] = append(outside[m], fmt.Sprintf("%s:%d", s.name, i+1))
				}
			}
		}
	}
	return outside, total
}

// junctionPackageSources 读本包全部非测试 .go（与上面那条判据同一扫描口径）。
func junctionPackageSources(t *testing.T) []namedSource {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var out []namedSource
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		b, err := os.ReadFile(n)
		if err != nil {
			t.Fatalf("read %s: %v", n, err)
		}
		out = append(out, namedSource{n, string(b)})
	}
	if len(out) == 0 {
		t.Fatalf("判据自检失败：一个非测试 .go 文件都没扫到（判据指向错的目标 ⇒ 不允许算「通过」）")
	}
	return out
}

func TestJunctionNodeConstructionOnlyInCore(t *testing.T) {
	outside, total := junctionNodeConstructionHits(junctionPackageSources(t))
	for _, m := range junctionNodeConstructions {
		if total[m] == 0 {
			t.Fatalf("判据自检失败：marker %q 在本包里一次都没命中（marker 写错 ⇒ 判据恒真）", m)
		}
		if hits := outside[m]; len(hits) > 0 {
			t.Fatalf("φ 汇聚节点的裸构造只允许在 %s（核心），实测还在宿主里：%s\n"+
				"  marker = %q\n"+
				"  ⇒ 宿主只准把臂集合 / 臂视图 / 臂尾绑定交给 assembleArmJunction，节点由核心构造（J §4：宿主差异 = 数据，不是代码分支）。\n"+
				"  ⇒ 若确要搬家：改 junctionCoreFile 常量并写明理由（本判据刻意钉位置）。",
				junctionCoreFile, strings.Join(hits, " · "), m)
		}
	}
}

// TestJunctionNodeConstructionCheckerLocksBothHalves：判据自检 —— 宿主里出现命中必须被抓；
// 只在核心里出现必须放行；两条都不命中必须判成「判据失效」而不是「通过」。
func TestJunctionNodeConstructionCheckerLocksBothHalves(t *testing.T) {
	mk := func(name, body string) namedSource {
		return namedSource{name, "package p\n\nfunc f() {\n\t" + body + "\n}\n"}
	}

	hostCase := []namedSource{mk("build_return.go", "a := "+junctionNodeConstructions[0]+" g.groups}")}
	if hits, _ := junctionNodeConstructionHits(hostCase); len(hits[junctionNodeConstructions[0]]) != 1 {
		t.Fatalf("自检失败：宿主里的裸构造没被抓到（hits=%v）⇒ 判据对真违规不敏感", hits)
	}

	coreCase := []namedSource{mk(junctionCoreFile, "a := "+junctionNodeConstructions[0]+" g.groups}")}
	if hits, total := junctionNodeConstructionHits(coreCase); len(hits[junctionNodeConstructions[0]]) != 0 || total[junctionNodeConstructions[0]] != 1 {
		t.Fatalf("自检失败：核心里的裸构造被误判成违规（hits=%v total=%v）", hits, total)
	}

	noneCase := []namedSource{mk(junctionCoreFile, "a := &Aggregate{ID: x}")}
	if _, total := junctionNodeConstructionHits(noneCase); total[junctionNodeConstructions[0]] != 0 {
		t.Fatalf("自检失败：marker 失效的情况没有被暴露成「0 命中」")
	}
}
