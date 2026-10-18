package ir

// on_error_mode.go —— `on_error:` 模式 → 平台拼写的**闭集只有一个定义处**。
//
// 背景（2026-09-21，「两处实现」普查）：`parallel for` 的语句形
// （`build_stmt.go`，`*ast.ForStmt`）与值形（`build_call.go`，`*ast.ParallelForExpr`）
// 各自把这张三项表**逐字写了一遍** —— 同一份映射的两份实现。今天它们字节相同是
// 巧合，不是结构（改一处、另一处静默保持旧拼写正是本仓反复复发的形态）。
// 现收口为**一张表 + 一个函数**：新增/改名模式只改本文件。
//
// 与相邻规则的关系：ncoda 侧的**语言闭集**（作者能写的三个模式名）归
// `internal/parser` 的 `ncodaOnErrorModes`；本表是**平台拼写**（lowering 的职责）。
// 两张表不合并 —— 语言语义不因平台拼写而变形。
var cozeOnErrorModeByNcoda = map[string]string{
	"terminate":     "terminated",
	"keep_null":     "continue-on-error",
	"remove_failed": "remove-abnormal-output",
}

// cozeOnErrorMode maps a language-level on_error mode to the coze spelling.
// ok=false means the mode is outside the closed set (caller reports it).
//
// 空串 = 作者**没写**该键 ⇒ 语言缺省（`ParallelForDefaultOnError`，见
// `parallel_for_defaults.go`）。缺省在**这里**落地而不是在 parser：AST 上
// 「写了缺省」与「没写」不可分辨，前端只记「未设」（一处定义，两处消费
// —— 解析与物化同源）。
func cozeOnErrorMode(name string) (string, bool) {
	if name == "" {
		name = ParallelForDefaultOnError
	}
	mode, ok := cozeOnErrorModeByNcoda[name]
	return mode, ok
}
