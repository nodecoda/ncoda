package parser

// on_error.go —— `on_error:` 模式的**闭集只有一个定义处**。
//
// 背景（2026-09-21，「两处实现」普查）：`parallel for` 的配置子句在两个语法位置
// 各解析一遍（语句形 `parseParallelForStmt`（`stmt.go`）与值形
// `parseParallelForExpr`（`expr.go`）），两处各自把三个模式名写成一条
// `!= "terminate" && != "keep_null" && != "remove_failed"` 的判据 —— 同一份闭集
// 的**两份实现**（本仓 20 天里反复复发的形态：改一处、另一处静默保持旧行为）。
// 现收口为**一张表**：新增/改名模式只改本文件。
//
// 与相邻规则的关系：本表是**语言词法闭集**（作者能写的三个模式名）；平台拼写
// （`terminated` / `continue-on-error` / `remove-abnormal-output`）是**另一张表**，
// 归 `internal/ir` 的 `cozeOnErrorMode`（lowering 侧，同样是单一实现）。
// 两张表之间的方向是 lowering 的职责，不是把两张表合成一张（语言侧不得平台绑架）。
var ncodaOnErrorModes = []string{"terminate", "keep_null", "remove_failed"}

// isNcodaOnErrorMode reports whether s is one of the modes the language accepts.
func isNcodaOnErrorMode(s string) bool {
	for _, m := range ncodaOnErrorModes {
		if s == m {
			return true
		}
	}
	return false
}

// onErrorModeExpected is the diagnostic for a token outside the closed set
// (wording kept byte-identical to the two sites it replaces).
const onErrorModeExpected = "Expected terminate, keep_null, or remove_failed"
