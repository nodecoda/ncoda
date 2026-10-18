package flow_test

// zz_wire_keys_test.go —— 平台 wire 键 / 词汇的**唯一拼写处**（G-D 断言预算专项清，2026-09-25）。
//
// 为什么收成一处：G-D 的口径是「同一条事实不要在多处重复拼写」（本仓「一处定义」原则在**测试面**
// 的应用）—— 平台若改键名，改动面是**这里 1 处**而不是散在几十个文件里几十处。
// 收的是**拼写点**，不是对 wire 词汇的依赖（那不可能也不需要消除）。
// @rule G-D.wire-spelling-single-source
const (
	wGroup1                   = "Group1"
	wLoopFunctionInlineInput  = "loop-function-inline-input"
	wLoopFunctionInlineOutput = "loop-function-inline-output"
)
