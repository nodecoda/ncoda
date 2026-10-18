package ir

// R-44 判据：决策日志的**受控词表**必须真的被调用点使用，且词表**无死项**。
//
// 正本 = `docs/five-step/spec/SHIM-ESCAPE-CONTRACT.md` §5-1。
//
// 为什么是**静态**判据（读源码，不看运行期）：本刀要防的正是「调用点自己拼字符串」
// （`"calc:" + calleeName(...)`）—— 运行期读数**看不出**拼写是从哪来的（拼对了就
// 一样），只有源码面能看见「有没有绕开唯一拼写点」。所以判据 = 逐调用点读源码：
//
//	① 每个 `g.recordPlacement(...)` 的 construct 段必须是 `placementConstruct(`、
//	   reason 段必须是 `placementReason(`（绕开 ⇒ 红）；
//	② 调用点写死的**家族**必须来自 `placementFamilies`（表外 ⇒ 红）；
//	③ 调用点用的 cause 必须是那 4 个常量之一；
//	④ 表**无死项** —— 声明了却没人在用的族 = 本仓已识别的病（declare-only axis）。

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// placementCallSiteFiles = 决策记录的全部实现文件（新增文件必须加进来；
// 漏加 ⇒ 该文件的调用点不被扫 = 判据有洞，故本清单也由 ① 的「调用点总数」自锁）。
var placementCallSiteFiles = []string{
	"build.go",
	"build_call.go",
	"build_expr.go",
	"build_node.go",
	"build_stmt.go",
	"build_return.go",
	// placement_vocab.go：json 内建的**单一决策点**（`recordJSONBuiltinPlacement`
	// 按「能力轴 × 载荷」定落点并记账）——2026-09-27 加入；漏了它，那里的调用点
	// 就不被扫 ⇒ 判据有洞（本清单的自锁见下方 `sites == 0` 与死族检查）。
	"placement_vocab.go",
}

// splitTopLevelArgs 按**顶层逗号**切参数（引号 / 括号内不切）。
func splitTopLevelArgs(s string) []string {
	var out []string
	depth := 0
	var quote byte
	cur := strings.Builder{}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			cur.WriteByte(c)
			if c == '\\' && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '`':
			quote = c
			cur.WriteByte(c)
		case '(', '[', '{':
			depth++
			cur.WriteByte(c)
		case ')', ']', '}':
			depth--
			cur.WriteByte(c)
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(cur.String()))
				cur.Reset()
				continue
			}
			cur.WriteByte(c)
		default:
			cur.WriteByte(c)
		}
	}
	out = append(out, strings.TrimSpace(cur.String()))
	return out
}

// recordPlacementCalls 返回源码里每个 `g.recordPlacement(` 调用的参数文本。
func recordPlacementCalls(src string) []string {
	const marker = "g.recordPlacement("
	var out []string
	idx := 0
	for {
		i := strings.Index(src[idx:], marker)
		if i < 0 {
			return out
		}
		i += idx
		j := i + len(marker)
		depth := 1
		var quote byte
		for ; j < len(src); j++ {
			c := src[j]
			if quote != 0 {
				if c == '\\' {
					j++
					continue
				}
				if c == quote {
					quote = 0
				}
				continue
			}
			switch c {
			case '"', '`':
				quote = c
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					goto done
				}
			}
		}
	done:
		out = append(out, src[i+len(marker):j])
		idx = j
	}
}

// quotedLiteral 取一个「恰好是一个字符串字面量」的表达式的值；否则 ok=false。
func quotedLiteral(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", false
	}
	return s[1 : len(s)-1], true
}

func TestPlacementVocabCallSitesUseVocabulary(t *testing.T) {
	usedFamilies := map[string]bool{}
	causeConsts := map[string]string{
		"PlacementCauseCapability":   PlacementCauseCapability,
		"PlacementCauseNoNativeNode": PlacementCauseNoNativeNode,
		"PlacementCauseContract":     PlacementCauseContract,
		"PlacementCausePurity":       PlacementCausePurity,
	}
	sites := 0
	for _, f := range placementCallSiteFiles {
		raw, err := os.ReadFile(filepath.Join(".", f))
		if err != nil {
			t.Fatalf("读 %s: %v", f, err)
		}
		for _, args := range recordPlacementCalls(string(raw)) {
			parts := splitTopLevelArgs(args)
			if len(parts) != 4 {
				t.Errorf("%s: recordPlacement 参数应为 4 段，实得 %d：%q", f, len(parts), args)
				continue
			}
			sites++
			// ② / ① construct 段
			con := parts[1]
			if !strings.HasPrefix(con, "placementConstruct(") {
				t.Errorf("%s: construct 段绕开了唯一拼写点：%s", f, con)
				continue
			}
			inner := strings.TrimSuffix(strings.TrimPrefix(con, "placementConstruct("), ")")
			conArgs := splitTopLevelArgs(inner)
			if len(conArgs) != 2 {
				t.Errorf("%s: placementConstruct 参数应为 2 段：%s", f, con)
				continue
			}
			fam, ok := quotedLiteral(conArgs[0])
			if !ok {
				t.Errorf("%s: 家族必须是**字面量**（闭集，不得拼字符串）：%s", f, conArgs[0])
				continue
			}
			if _, declared := placementFamilies[fam]; !declared {
				t.Errorf("%s: 家族 %q 不在受控词表 placementFamilies 里（表外家族不得使用）", f, fam)
				continue
			}
			usedFamilies[fam] = true
			// ③ reason 段
			rea := parts[3]
			if !strings.HasPrefix(rea, "placementReason(") {
				t.Errorf("%s: reason 段绕开了唯一拼写点：%s", f, rea)
				continue
			}
			reaArgs := splitTopLevelArgs(strings.TrimSuffix(strings.TrimPrefix(rea, "placementReason("), ")"))
			if len(reaArgs) != 2 {
				t.Errorf("%s: placementReason 参数应为 2 段：%s", f, rea)
				continue
			}
			if _, ok := causeConsts[reaArgs[0]]; !ok {
				t.Errorf("%s: cause 必须是 4 个常量之一，实得 %q", f, reaArgs[0])
			}
		}
	}
	if sites == 0 {
		t.Fatal("一个 recordPlacement 调用点都没扫到 —— 判据空了（文件清单过期？）")
	}
	// ④ 表无死项
	var dead []string
	for fam := range placementFamilies {
		if !usedFamilies[fam] {
			dead = append(dead, fam)
		}
	}
	sort.Strings(dead)
	if len(dead) > 0 {
		t.Errorf("受控词表有**死族**（声明了没人用 ⇒ 要么接线、要么删）：%v", dead)
	}
}

func TestPlacementVocabClosedSurface(t *testing.T) {
	// cause 闭集 = 4 值，互不相同且非空
	causes := []string{PlacementCauseCapability, PlacementCauseNoNativeNode, PlacementCauseContract, PlacementCausePurity}
	seen := map[string]bool{}
	for _, c := range causes {
		if c == "" || seen[c] {
			t.Errorf("cause 闭集有重复 / 空值：%v", causes)
		}
		seen[c] = true
	}
	// ① 每个族常量都在表里（常量与表不得漂移）
	for _, fam := range []string{
		PlacementFamilyCondition, PlacementFamilyLoop, PlacementFamilyBinary,
		PlacementFamilyCalc, PlacementFamilyBuiltin, PlacementFamilyListOp,
		PlacementFamilyJSON, PlacementFamilyLLM, PlacementFamilyWorkflow,
		PlacementFamilyTool, PlacementFamilySubWorkflow, PlacementFamilyAgentMode,
		PlacementFamilyAdvancedChat, PlacementFamilyForeignCode,
	} {
		if _, ok := placementFamilies[fam]; !ok {
			t.Errorf("族常量 %q 不在 placementFamilies 表里", fam)
		}
	}
	// ② PlacementFamilyConstruct：表内 ⇒ 构造键；表外 ⇒ ok=false（fail loud，不得猜）
	for fam, want := range placementFamilies {
		got, ok := PlacementFamilyConstruct(fam)
		if !ok || got != want {
			t.Errorf("PlacementFamilyConstruct(%q) = (%q,%v)，期望 (%q,true)", fam, got, ok, want)
		}
	}
	if _, ok := PlacementFamilyConstruct("no-such-family"); ok {
		t.Error("表外家族必须 ok=false")
	}
	// ③ 拼写只有一处：detail == "" ⇒ 只有家族；否则 `<家族>:<细节>`
	if got := placementConstruct(PlacementFamilyForeignCode, ""); got != "foreign-code" {
		t.Errorf("空细节应只留家族，实得 %q", got)
	}
	if got := placementConstruct(PlacementFamilyCalc, "len"); got != "calc:len" {
		t.Errorf("实得 %q", got)
	}
	// ④ reason = `<cause>: <自由补充>`；空补充只留 cause
	if got := placementReason(PlacementCauseContract, ""); got != "contract" {
		t.Errorf("空补充应只留 cause，实得 %q", got)
	}
	if got := placementReason(PlacementCauseCapability, "op 9"); got != "capability: op 9" {
		t.Errorf("实得 %q", got)
	}
	// ⑤ 提取（Family / Cause）对**历史行**也要无歧义
	if got := (Placement{Construct: "condition:<"}).Family(); got != "condition" {
		t.Errorf("Family() = %q", got)
	}
	if got := (Placement{Construct: "foreign-code"}).Family(); got != "foreign-code" {
		t.Errorf("Family() = %q", got)
	}
	if got := (Placement{Reason: "contract: 纯变换"}).Cause(); got != "contract" {
		t.Errorf("Cause() = %q", got)
	}
	if got := (Placement{Reason: "purity"}).Cause(); got != "purity" {
		t.Errorf("Cause() = %q", got)
	}
}
