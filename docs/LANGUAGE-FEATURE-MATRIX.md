# 语言特性矩阵（Language Feature Matrix）

> **历史快照（2026-10-25 注）**：平台适配器、平台证据与平台判据已随适配器移出开源仓
> （商业仓 `internal/backend/*`、`platforms/*`）。本文只用于追溯当时的平台映射/契约，
> 不得据此把平台代码或平台判据加回开源仓；开源仓的定位见 `README.md`。

> 状态：**机制已建立（2026-08-29）**。特性 × 语言层影响面追踪——改语言特性时
> 防漏判。A 级契约配套（维护 B 级：随实现演进）。
> **锚点规范**：每个 `✓` 单元格必须有 `pkg/` 里的 `//@feature <name>` 锚点
> （`pkg/featurematrix` 校验器自动核对，缺锚点 = 测试红）。没有锚点的格子
> 只允许 `~` / `✗` / `-` / `N/A` / `·`。
>
> **平台投影矩阵已随适配器移出开源仓**（ASSET-BOUNDARY）：本表只回答
> 「ncoda 语言自己支持到什么程度」；某平台能否承载由适配器在自己的仓里声明
> 与验证（本仓不再保留任何平台列）。

## 0. 项目总体定位

- **位置**：语言演进影响面追踪层。回答「这个特性在语言前端 / IR 里有没有
  完整语义与判据」，不回答「某个平台装不装得下」。
- **视角**：ncoda 为圆心。第一列 = "这是什么特性"，第二列 = 语义权威文档，
  第三列 = 语言层（AST / IR / passes）状态。
- **门禁**：修改任一语言特性（词法/语法/语义/类型）时必须更新本表并补
  `//@feature` 锚点；**改特性的 commit 必须同时提交矩阵更新**。

## 1. 标签语义

| 标签 | 含义 | 允许条件 |
| --- | --- | --- |
| **✓** | 完整支持，roundtrip/定向断言锁定 | **必须有 `//@feature` 锚点**，否则只许 ~ / ✗ |
| **~** | 部分支持 / 软降级（默认值/近似语义） | 在特性文档或 §2.1 记录缺什么、理由 |
| **✗** | 明确缺口（覆盖承诺：不沉默遗漏） | 引用覆盖清单对应条目 |
| **-** | 语言里无此概念（语义等价 null / 不存在） | 一句理由 |
| **N/A** | 本仓尚无实现 | — |
| **·** | 已登记、未评估（待回填标签与锚点） | 过渡态，回填时转 ✓/~ /✗/-/N/A |

## 2. 特性 × 语言层矩阵

列：`IR`=语言自身（AST/IR/前端 passes）。

| 特性 | 语义文档 | IR |
| --- | --- | --- |
| `var-let` | WORKFLOW-IR / AGENTS.md | ✓ |
| `if-else` | ALGORITHM-CONVERGENCE / 图语义对照（适配器仓） | ✓ |
| `for-while` | WORKFLOW-IR | ✓ |
| `parallel-fence` | AGENTS.md P5 / EDGE-CONTRACT | ✓ |
| `switch` | SWITCH-SYNTAX | ✓ |
| `attempt` | AGENTS.md return 分层 / 图语义对照（适配器仓）§6.2 | ✓ |
| `return-hierarchy` | AGENTS.md / 图语义对照（适配器仓）§6.2 | ✓ |
| `yield` | AGENTS.md P5 | ✓ |
| `request-input` | binder human_input_contract.go | ✓ |
| `output` | NCODA-OUTPUT-MODEL / SEMANTICS-output-answer | ✓ |
| `empty-check` | EMPTY-CHECK-DESIGN（已实施，2026-09-01 用户审核通过 B 方案） | ✓ |
| `foreign-code` | PYTHON-SHIM-TRANSLATION-CONTRACT / PYTHON-CODE-NODE-GENERATOR | ✓ |
| `conv-var` | WORKFLOW-IR | ✓ |
| `lambda` | DECOMPILER-PATTERN（list-operator lambda 形态） | ✓ |
| `ternary` | pkg/ast | ✓ |
| `dict-list` | pkg/ast | ✓ |
| `template-str` | WORKFLOW-IR | ✓ |
| `for-expr` | WORKFLOW-IR | ✓ |
| `field-index` | RUNTIME-ARRAY-INDEXING / NCODA-DOLLAR-ESCAPE-SPEC | ✓ |
| `null-coalesce` | route-1a P0b（R4 `??` first-not-null：编译物化 python `x if x is not None else y` shim；biz反 merge 多候选 → `??` 链精确逆；取代旧 truthy 值合并） | ✓ |
| `bool-or` | route-1a P0b（R1：布尔或运算符收紧为纯布尔逻辑（与 && 对称）；非 bool 操作数 = 编译错误 + use '??' hint；旧 truthy 值合并退役） | ✓ |
| `file-type` | file 类型契约（第一公民） | ✓ |
| `explicit-cast` | 显式类型转换契约 | ✓ |
| `record-type` | DECOMPILER-PATTERN / AGGREGATION-HOISTING（单字段 record/scalar 窄适配） | ✓ |
| `enum` | classify 枚举契约 | ✓ |
| `any-strategy` | ideas-todo/ANY-SHRINK-STRATEGY | ✓ |
| `hoist-zero-default` | 反编译模式对照（适配器仓）§0 | ✓ |
| `dollar-escape` | NCODA-DOLLAR-ESCAPE-SPEC / DOLLAR-ESCAPE-DESIGN | ✓ |
| `qualified-call` | TOOL-DIRECT-CALL-FORM（ideas-todo） | ✓ |
| `import` | TOOL-REGISTRY-LAYOUT | ✓ |
| `library` | lib-semantics-language-change（库单元身份：`library <name> @ <semver>;`；只是声明单元的名字与版本，不进 IR / ABI / 绑定） | ✓ |
| `function-inline` | AGENTS.md return 分层（子函数） | ✓ |
| `subflow` | SUBWORKFLOW-DECLARATION（子工作流声明） | ✓ |
| `env-const` | WORKFLOW-IR | ✓ |
| `llm-unified-envelope` | UNIFIED-LLM-SEMANTICS / target-proofs/llm | ✓ |
| `open-object` | LLM-OPEN-OBJECT / UNIFIED-LLM-SEMANTICS §2 | ✓ |
| `operation-policy` | 错误处理设计（ideas-todo/2026-09-10-ncoda-error-handling-semantics-and-bidirectional-design.md §1.3 + §10 N1 缺口表） | ✓ |

### 2.1 已知测试缺口（IR 层无定向测试，2026-08-29 盘点）

历史盘点时以下特性在 IR 构建路径（pkg/ir）缺定向测试锚点：
coalesce-or（2026-09-05 route-1a 拆分为 null-coalesce + bool-or）/ any-strategy /
import / env-const / explicit-cast。
**2026-08-31 全部补齐**（`pkg/ir/gap_test.go` 6 个定向测试 +
`//@feature` 锚点），IR 列已全部转 ✓，当前无缺口。
新特性补测试时转 ✓ 并加锚点。


### 2.2 平台投影与历史回填

平台投影矩阵、逐格回填注记与变更史**随适配器移出开源仓**：它们回答「某平台
装不装得下」，属于适配器仓的资产（含全部平台证据与锚点）。开源仓只保留上表
（语言自身）与 §2.1 的语言层缺口盘点。

语言特性变更时：更新本表 + 在 `pkg/` 补 `//@feature` 锚点；适配器侧的影响面
由各适配器在自己的仓里记录，不回写本表。
