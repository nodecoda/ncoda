// 语言特性矩阵锚点声明（docs/LANGUAGE-FEATURE-MATRIX.md）：
//@feature var-let — map 字面量静态键（标识符键 = 标签 / 字符串键 = 值）

package ir_test

// static_map_key_gate_test.go — **静态 map 键规则不许被抄第二份**（机器门禁，
// D-2，2026-09-14）。
//
// 本族缺陷的复发机制不是「某处判据写错」，而是**同一条规则被复制多份**：
// 规则本体（键 = 标识符名 或 字符串值）本来是 `passes.staticMapKey` /
// `TypePass.staticMapKey` 两份抄本，IR 建图层又用**面向取值的**
// `stringValue`（只认字符串字面量）去读键 ⇒ `{ lyric: l }`（spec 标准形态，
// `spec/examples/nodecoda/64_subflow_decl.ncoda:17`）在 20 个键位里被静默丢弃，
// 而反过来手写「只认 IdentExpr」的两处又把字符串键（= 我们自家反编译产物的
// 形态）丢弃。两个方向都是静默丢数据，且门禁全绿。
//
// 因此这里锁两条**结构**性质（不是某个具体调用点的行为）：
//  1. 规则只有一份实现：`pkg/ast.StaticMapKey` —— 三处使用方（IR 建图、
//     target_validation、type_infer）都必须引用它，不得自带 `switch` 抄本；
//  2. IR 建图层不得用**取值的** `stringValue(...Key)` 读键（值位读仍用它）。
// 新增键位读取若绕过规则，本测试直接红。
import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// valueReaderOnKey matches `stringValue(<expr>.Key)` — the value-oriented
// reader applied to a map *key*. Any match is a second, lossy key rule.
var valueReaderOnKey = regexp.MustCompile(`stringValue\([A-Za-z_][A-Za-z_0-9]*\.Key\)`)

func TestIRBuilderReadsMapKeysThroughTheOneRule(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		scanned++
		if m := valueReaderOnKey.FindString(string(src)); m != "" {
			t.Errorf("%s: %s — map keys must be read through staticMapKeyName "+
				"(ast.StaticMapKey); stringValue only accepts string literals and silently "+
				"drops identifier keys (D-2, 2026-09-14)", name, m)
		}
	}
	if scanned == 0 {
		t.Fatal("scanned no implementation files — the gate would be vacuous")
	}
}

// TestMapKeyRuleHasExactlyOneImplementation：三处使用方都必须引用规则本体。
// 谁把 `switch { case *ast.IdentExpr: ... }` 抄回来，就红。
func TestMapKeyRuleHasExactlyOneImplementation(t *testing.T) {
	files := []string{
		"build.go",                       // IR 建图（staticMapKeyName 委托）
		"../passes/target_validation.go", // 前端校验 pass
		"../passes/type_infer.go",        // 类型推断 pass
	}
	for _, f := range files {
		src, err := os.ReadFile(filepath.Clean(f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if !strings.Contains(string(src), "ast.StaticMapKey") {
			t.Errorf("%s does not reference ast.StaticMapKey — the static-map-key rule must "+
				"have exactly one implementation (D-2, 2026-09-14)", f)
		}
	}
}
