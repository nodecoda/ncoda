package ast

// FlowExitDefaultKey 是**语言层**为「无名流程出口」定义的键（标准 §5.6，W1-D2′
// 「语言默认键」，2026-09-16 用户裁定 (II)；类型层的等价 = A12-H20，2026-09-17
// 用户裁定 (a)）。
//
// 语言模型：流程的结果 = 一组**具名输出**（平台 exit 就是一张 name→value 表）。
// 于是 `return <e>` 的含义 = 把 e 当成那张表：
//
//	e 的类型是 record 且字段承载口可枚举 ⇒ 逐字段具名（键 = 语言字段名）；
//	否则                                ⇒ **一个**输出，键 = 本常量。
//
// **唯一声明点 = 语言包（本处）**；`pkg/ir.FlowExitDefaultKey` 只是它的别名。
// 之所以必须单一声明：这条等价有三处消费者 —— lowering 的出口归一化
// （pkg/ir/ports.go `soleDefaultKeyedExit`）、raising 的读回糖形
// （platforms/cozebiz/decompile/convert.go）、以及**类型检查**
// （pkg/passes/type_pass.go 的 `exitDefaultKeyedSugar`）——三处若各写一份
// 拼写，「糖 ≡ 显式」就会在某一侧分叉（A12-H20 实测：类型层不认 ⇒ 同一函数里
// 两种写法并列即 RETURN_TYPE、`-> T` 下显式形也报错）。
//
// 为什么键必须是语言常量、不能由值的生产者端口推断：键是**对外契约** —— 旧实现
// 拿物理口名当键（实测 `return len(u);` ⇒ `(result, result)`、`return v1;`(llm) ⇒
// `(response, response)`），同一个意图换个写法契约就改名。本常量与实现无关，且与
// 平台惯用法逐字一致（语料全扫 398 条 end 绑定里 233 条名为 `output`，其中 175 条
// 的端口根本不是 `output` —— `(output, draft_id)` 78 例；数字出处 =
// docs/five-step/spec/W1-output-return.md §8.5 (3)）。
//
// 键的比较对象是**名字**，不是拼写：源码里 `$output` 的 `$` 由词法剥掉
// （internal/lexer），`{ $output: x }` 与 `{ output: x }` 是同一个键。
const FlowExitDefaultKey = "output"
