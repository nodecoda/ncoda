package parser

import (
	"os"
	"strings"
	"testing"
)

// on_error_single_implementation_test.go —— 「一份规则、一处定义」（2026-09-21）。
//
// 被判的对象：`parallel for` 的 `on_error:` **语言闭集**（`terminate` /
// `keep_null` / `remove_failed`）。
//
// 为什么这是判据：两个语法位置（语句形 `parseParallelForStmt`、值形
// `parseParallelForExpr`）曾各自把三个模式名写成一条 `!=` 判据 —— 同一份闭集的
// 两份实现（改一处、另一处静默保持旧行为）。现收口为 `on_error.go` 一张表。
//
// 判据（钝、可锁两半）：本包里**同时出现全部三个模式名**（带引号的字面量）的
// 非测试 .go 文件必须**恰好一个** = `onErrorFile`。
//
// 判据自检：另一个文件里塞进同样三个字面量必须被抓到；一个都没有必须判成
// 「判据失效」而不是「通过」。
const onErrorFile = "on_error.go"

var onErrorMarkers = []string{`"terminate"`, `"keep_null"`, `"remove_failed"`}

func onErrorFilesWithAllModes(srcs []namedSource) []string {
	var out []string
	for _, s := range srcs {
		hasAll := true
		for _, m := range onErrorMarkers {
			if !strings.Contains(s.src, m) {
				hasAll = false
				break
			}
		}
		if hasAll {
			out = append(out, s.name)
		}
	}
	return out
}

type namedSource struct {
	name string
	src  string
}

func parserPackageSources(t *testing.T) []namedSource {
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

func TestOnErrorModeClosedSetHasSingleImplementation(t *testing.T) {
	files := onErrorFilesWithAllModes(parserPackageSources(t))
	if len(files) == 0 {
		t.Fatalf("判据自检失败：本包里没有任何文件同时含三个模式名字面量（marker 写错 / 表被搬走 ⇒ 判据恒真）")
	}
	if len(files) != 1 || files[0] != onErrorFile {
		t.Fatalf("on_error 的语言闭集只允许在 %s（唯一实现），实测：%v\n"+
			"  ⇒ 两个语法位置只准调用 isNcodaOnErrorMode(...)，不得各自再写一遍 `!=` 判据\n"+
			"  ⇒ 若确要搬家：改 onErrorFile 常量并写明理由（本判据刻意钉位置）。",
			onErrorFile, files)
	}
}

func TestOnErrorModeClosedSetCheckerLocksBothHalves(t *testing.T) {
	dup := []namedSource{
		{onErrorFile, "package p\n\nvar m = []string{\"terminate\", \"keep_null\", \"remove_failed\"}\n"},
		{"expr.go", "package p\n\nif s != \"terminate\" && s != \"keep_null\" && s != \"remove_failed\" {}\n"},
	}
	if got := onErrorFilesWithAllModes(dup); len(got) != 2 {
		t.Fatalf("判据自检失败：另一处 `!=` 判据没有被抓到（got=%v）⇒ 判据对真违规不敏感", got)
	}
	half := []namedSource{{"type_pass.go", "package p\n\nif s == \"keep_null\" {}\n"}}
	if got := onErrorFilesWithAllModes(half); len(got) != 0 {
		t.Fatalf("判据自检失败：只消费一个模式的调用点被误伤（got=%v）", got)
	}
}
