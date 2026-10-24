package ir

// parallel_for_defaults.go —— `parallel for` 配置子句两条轴的**语言缺省**，
// **单一定义处**（2026-09-29 用户批准的语言级放宽配套）。
//
// 语言面（`docs/five-step/spec/F3-error-policy-container-axis.md` §②）：
// 配置子句**整体可省**、键序任意；**省略 = 用缺省**，且**缺省不上显**——
// 「写了缺省值」与「没写」必须同产物（规范形 = 缺省不写）。
//
// 为什么缺省值在这里、而不在 parser：AST 上「写了缺省」与「没写」**不可分辨**
// （都落 `concurrency=0` / `onError=""`）—— 缺省是**降级/物化**阶段的事，由本文件
// 给出唯一取值，前端只记「未设」。四个消费面全部读这里，改一处即全链路一致：
//
//	① `pkg/ir/on_error_mode.go`  —— `on_error` 空 = 缺省 ⇒ 解析成平台拼写
//	② 各目标 lowering 物化           —— `concurrency` 空 ⇒ 写该目标缺省并发度
//	③ 反编译规范形                   —— 等于缺省 ⇒ 不写进 ncoda 文本
//	④ 判据                           —— `internal/compiler/cozebiz_parallel_for_default_config_contract_test.go`
//
// 取值来历（一手，2026-09-29）：并发度**无法从语料反推**——254 个 batch 节点的
// `concurrentSize` 分布为 1→85 / 3→66 / 2→60 / 10→37 / 4·5→6，**无占多数的值**；
// 但三个目标的**平台自身缺省都是 10**：coze-studio 开源实现
// （oss:backend/domain/workflow/internal/nodes/batch/batch.go，`concurrency == 0 → 10`）·
// 参考目标（`get_default_config` 的 `parallel_nums: 10`，且 `serialize.go` 写侧省略 10）·
// runtime（`internal/runtime/（商业仓）compile/loop.go`，`workers <= 0 → 10`）。
// ⇒ 「省」只发生在**语言表面**；产物里省不掉（平台缺键 = 硬报错，必须物化）。
const (
	// ParallelForDefaultConcurrency 是 `parallel for` 省略 `concurrency:` 时的并发度。
	ParallelForDefaultConcurrency = 10

	// ParallelForDefaultOnError 是 `parallel for` 省略 `on_error:` 时的策略：
	// 整批终止（= 无载体目标 coze 家族的实际行为）。
	ParallelForDefaultOnError = "terminate"
)
