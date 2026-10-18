# 平台能力清单（Platform Capability Table）— 目标平台不可表达之物的单一声明点

> **等级**：**B 设计**（代码侧的对应物 = `pkg/passes/CapabilityManifestForTarget`
> 与 `capabilityShapeVerdict`；契约语义见 AGENTS.md「错误分类契约」）。
> **纪律**：本表每新增一行，**必须**同时给出：① 平台证据（源码 `file:line` 或语料
> 实测计数）；② 判定点（前端门禁 / lowering 兜底 / 产物门禁）；③ 用户指引（改写法
> 还是等编译器）；④ **一个负向契约测试**（撤掉判据即 RED）。没有测试的条目不算数。
> 最后核对：2026-09-13。

## 0. 怎么用

报错里带 `class` / `capability` / `pending` / `target` 四个字段（语言错不带，保持
旧线格式）。按 `(target, capability)` 分流即可定位到本表某一行。

| class | 含义 | 用户动作 |
| --- | --- | --- |
| `language`（缺省） | ncoda 源码违反语言规则 | 改自己的代码 |
| `platform` | **不可约**的平台物理限制 | 改写法（永久） |
| `internal` | 编译器的锅（`pending:true` = 可约未实现） | 等修/提 issue，可临时绕行 |
| `config` | 绑定/注册表/工具路径等输入缺失 | 补配置 |

## 1. coze-biz（主线 target）

| capability | 形状 | 类别 / pending | 平台证据 | 判定点 | 用户指引 |
| --- | --- | --- | --- | --- | --- |
| `container.nested` | 容器内再放 loop/batch | `platform` / false | `internal/nodes/loop/loop.go:56` `loop node cannot have parent`；`batch.go:51` 同形；语料 **452 容器 / 嵌套 0** | lowering（`render_loop.go`）+ 产物门禁 L1 | 抽成子工作流（平台官方变通），在循环体里调用 |
| `container.flow_terminate` | `start`/`end` 落在容器内 | `platform` / false | `internal/nodes/exit/exit.go:42` `exit node cannot have parent` + exit id 全局唯一 `900001`；`entry.go:35` 同形；语料 **322 个 end 全在根画布、容器内 0** | 产物门禁 L1 | 流程终止只能在根画布；循环内要「提前结束整流程」需改写成「置标志 + break + 循环后条件路由」 |
| `loop.function_return` | 循环体内 `return`（`for`/`while`，**带值** `return <v>` 或 **void** `return;`） | **已实现（2026-09-13，不再拒绝）**；batch（`parallel for`）侧**不是 lowering 缺口**：**带值** `return <v>` = **语言错 E1037**（并发下值选择不确定，2026-09-14 D-L3(a)），**void** `return;` **合法**（见下） | 平台**有通道**：`set_variable` 执行即写 parent intermediate store（`loop/break/break.go:37-48` 置 `$break`）+ 循环状态变量（`loop.go` intermediateVars / outputVars）；语料 198 个 loop 里 **36 个带 `variableParameters`**（7 个 body 含 break、10 个把状态变量暴露成循环输出） | 前端能力门禁（`CapabilityShapeLoopFunctionReturn`，coze-biz 清单**不再拒绝**）；IR 物化 `pkg/ir/loop_return_materialize.go`；渲染器兜底 `render_node.go:318`；前端 `parallel_iteration_local.go`（batch 内**带值** return = E1037 语言错）；raising 逆 `platforms/cozebiz/decompile/loop_return_fold.go` | 直接写 `return <v>` / `return;` 即可（编译为「状态对 + break + 循环后门控」，反编译**折回**成同一个 `return`）。改为：**void `return;` 可用**（迭代体内「终止整个流程」，产物形状 = 容器内节点直连根 `end` —— 真实导出 **5 份**（`X19_Tdouyin3`/`X12_Tdouyin4`/`X20_Tred2`/`X22_T_piliang2`/`X23_Tpiliang_table_2`）就是这个形状）；**带值** `return <v>` 在并发迭代内**不可用**（哪个迭代的值胜出不确定 = 语言层不定义，E1037），collect 请用 `yield` + 循环后 `return` |
| `config.bindings_missing` / `config.registry_missing` | 工具调用缺绑定/注册表 | `config` / false | 闭集契约（`docs/PLATFORM-MECHANISM.md`）：绝不用占位 id 静默兜底 | lowering 前置校验 | 补 `--tool-path` / `--coze-biz-bindings` |

### 1.1 已核验**不是**平台限制的形状（避免误判成能力缺口）

| 形状 | 实测 | 结论 |
| --- | --- | --- |
| `for { break; }` / `for { continue; }` | coze-biz 编译 `SUCCEEDED` | 平台支持（`Break`/`Continue` 节点类型 19/20；语料容器内 30 break + 23 continue） |
| `while (...) limit N { ... }`（直接条件） | `SUCCEEDED` | 支持；只有**不可直接 lower 的条件**才报 `condition.*`（见下） |
| 循环体内 `return`（`for` / `while`，带值与 void 两形） | 编译 `SUCCEEDED` 且**一轮即不动点**（`46_loop_escape.ncoda`） | 支持：平台**有通道**（循环状态变量 + `break`）⇒ 由 IR 物化实现（不再报 `TARGET_NOT_LOWERABLE`） |
| `condition.calculation` 等条件形状 | `while (true)` → `TARGET_NOT_LOWERABLE` | 平台守卫条件只接受「选择子 / !选择子 / 选择子 <比较> 字面量」形态 ⇒ `platform` |

## 2. 其他 target

- `dify-1.16-graphon-0.6`：默认清单（除 `condition.effectful` 外全 ENABLED）；dify 侧
  的循环早退由 dify 自己的 lowering（`__returning` / `__return_value`）承载，故
  `loop.function_return` **不拒绝**。
- `coze-1.0` / `coze-studio-flow-1.0`：与 coze-biz 同族、同一能力事实；当前只在
  lowering 兜底层报（未在前端门禁装载），按「dify / coze-studio 搁置」纪律只登记、
  不在主线改动。

## 3. 代码位置索引

| 东西 | 位置 |
| --- | --- |
| 类别常量 | `pkg/diag/diag.go`（`ClassLanguage` / `ClassPlatform` / `ClassInternal`；`ClassConfig` 走字面量） |
| 诊断字段 | `pkg/diag.Diagnostic.{Class,Capability,Pending}`；线格式 `internal/compiler.DiagJSON` + `resultToJSON` |
| 能力清单（代码） | `pkg/passes/capability.go`：`CapabilityManifestForTarget`、`capabilityShapeVerdict` |
| 前端门禁 | `pkg/passes/capability_gate.go`：`emitTargetNotLowerable` |
| 后端错误类别 | `internal/backend/cozebiz/render.go`：`ErrUnsupported.Class()`、`ErrPlatformUnsupported`、`ErrPendingLowering` |
| 产物门禁 | `internal/backend/cozebiz/verify.go`（L1 图不变量 + 容器归属 + 边端点存在性；L2 引用解析） |
| 契约测试 | `internal/compiler/platform_error_class_contract_test.go`；`internal/backend/cozebiz/{edge_endpoint_existence,container_placement}_contract_test.go`；`pkg/ir/loop_return_arm_kind_contract_test.go`；`internal/compiler/cozebiz_loop_return_lowering_contract_test.go`（循环内 return 物化正方向，值侧 7 形 + void 3 形）；`platforms/cozebiz/decompile/loop_return_fold_contract_test.go`（raising 逆 + 负向）；`internal/compiler/cozebiz_scalar_literal_value_position_contract_test.go`（标量字面量值位置物化） |
