package ir

import "github.com/nodecoda/ncoda/pkg/capability"

// on_error_mode.go —— `on_error:` 模式 → IR 线拼写的**唯一落点**。
//
// 两层分开：
//   - **语言闭集**（作者能写的三个模式名）归 parser；本文件只校验「在闭集里」。
//   - **线拼写**由目标声明（`capability.Table.OnErrorWire`）。未声明的目标原样
//     携带语言名 —— 这就是「IR 只带语义名」的缺省，平台拼写不进语言。
//
// 两个宿主（`build_stmt.go` 的语句形 / `build_call.go` 的值形）都只调用本函数，
// 不得各写一份表（判据 on_error_mode_single_implementation_test.go）。
//
// 空串 = 作者**没写**该键 ⇒ 语言缺省（`ParallelForDefaultOnError`）。缺省在
// **这里**落地而不是在 parser：AST 上「写了缺省」与「没写」不可分辨，前端只记
// 「未设」（一处定义，两处消费 —— 解析与物化同源）。
var languageOnErrorModes = map[string]bool{
	"terminate":     true,
	"keep_null":     true,
	"remove_failed": true,
}

// onErrorWire maps a language-level on_error mode to the target's wire
// spelling. ok=false means the mode is outside the language closed set
// (caller reports it). A nil table, or a table without OnErrorWire, carries
// the language spelling unchanged.
func onErrorWire(caps *capability.Table, name string) (string, bool) {
	if name == "" {
		name = ParallelForDefaultOnError
	}
	if !languageOnErrorModes[name] {
		return "", false
	}
	if caps != nil && caps.OnErrorWire != nil {
		if wire, ok := caps.OnErrorWire[name]; ok {
			return wire, true
		}
	}
	if caps != nil {
		if wire, ok := legacyOnErrorWire(caps.Target, name); ok {
			return wire, true
		}
	}
	return name, true
}
