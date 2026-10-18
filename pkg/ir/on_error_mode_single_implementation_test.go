package ir

import (
	"strings"
	"testing"
)

// on_error_mode_single_implementation_test.go —— 「一份规则、一处定义」（2026-09-21）。
//
// 被判的对象：`on_error:` 模式的**平台拼写表**（`terminate → terminated` /
// `keep_null → continue-on-error` / `remove_failed → remove-abnormal-output`）。
//
// 为什么这是判据而不是风格：同表曾在**两个宿主**（`build_stmt.go` 的语句形、
// `build_call.go` 的值形）逐字各写一遍；两处当天字节相同纯属巧合 —— 改一处、
// 另一处静默保持旧拼写，正是本仓 20 天里反复复发的「一份规则多份实现」形态
// （同类教训：`f11c3ec5` 的族 carrier 两端各一次实现 ⇒ 672 源跑批 39 源
// COMPILE_FAIL + 23 源 DECOMPILE_FAIL）。
//
// 判据（钝、可锁两半）：本包里**同时出现全部三个平台拼写**的非测试 .go 文件
// 必须**恰好一个** = `onErrorModeFile`。
//
// 判据自检（另一半）：另一个文件里塞进同样的三个拼写必须被抓到；一个都没有
// 必须判成「判据失效」而不是「通过」。
const onErrorModeFile = "on_error_mode.go"

var onErrorModeMarkers = []string{`"terminated"`, `"continue-on-error"`, `"remove-abnormal-output"`}

// onErrorModeFilesWithAllTokens 返回同时含全部三个拼写的非测试 .go 文件名（按出现序）。
func onErrorModeFilesWithAllTokens(srcs []namedSource) []string {
	var out []string
	for _, s := range srcs {
		hasAll := true
		for _, m := range onErrorModeMarkers {
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

func TestOnErrorModeTableHasSingleImplementation(t *testing.T) {
	srcs := junctionPackageSources(t)
	files := onErrorModeFilesWithAllTokens(srcs)
	if len(files) == 0 {
		t.Fatalf("判据自检失败：本包里没有任何文件同时含三个平台拼写（marker 写错 / 表被搬走 ⇒ 判据恒真）")
	}
	if len(files) != 1 || files[0] != onErrorModeFile {
		t.Fatalf("on_error 的平台拼写表只允许在 %s（唯一实现），实测：%v\n"+
			"  ⇒ 宿主只准调用 cozeOnErrorMode(...)，不得各自再写一遍这张表\n"+
			"  ⇒ 若确要搬家：改 onErrorModeFile 常量并写明理由（本判据刻意钉位置）。",
			onErrorModeFile, files)
	}
}

func TestOnErrorModeSingleImplementationCheckerLocksBothHalves(t *testing.T) {
	// 正例：把三个拼写塞进另一个文件 ⇒ 必须被抓到。
	dup := []namedSource{
		{name: onErrorModeFile, src: "package p\n\nvar m = map[string]string{\n" +
			`	"terminate": "terminated", "keep_null": "continue-on-error", "remove_failed": "remove-abnormal-output",` + "\n}\n"},
		{name: "build_stmt.go", src: "package p\n\nvar m = map[string]string{\n" +
			`	"terminate": "terminated", "keep_null": "continue-on-error", "remove_failed": "remove-abnormal-output",` + "\n}\n"},
	}
	if got := onErrorModeFilesWithAllTokens(dup); len(got) != 2 {
		t.Fatalf("判据自检失败：另一个文件里的同表没有被抓到（got=%v）⇒ 判据对真违规不敏感", got)
	}
	// 半表（只有两个拼写）不算一份实现：不误伤只消费其中之一的调用点。
	half := []namedSource{{name: "verifier.go", src: "package p\n\nvar ok = map[string]bool{\"terminated\": true, \"continue-on-error\": true}\n"}}
	if got := onErrorModeFilesWithAllTokens(half); len(got) != 0 {
		t.Fatalf("判据自检失败：半表被当成一份实现（got=%v）", got)
	}
	// marker 失效 ⇒ 0 命中必须可被看见（由 TestOnErrorModeTableHasSingleImplementation 的前置检查兜住）。
	if got := onErrorModeFilesWithAllTokens([]namedSource{{name: onErrorModeFile, src: "package p\n"}}); len(got) != 0 {
		t.Fatalf("判据自检失败：空源被判成命中（got=%v）", got)
	}
}
