# 语言特性矩阵（Language Feature Matrix）

> 状态：**机制已建立（2026-08-29）**。特性 × 平台影响面追踪——改语言特性时
> 防跨平台遗漏。A 级契约配套（维护 B 级：随实现演进）。
> 设计论证见 `docs/ideas-todo/LANGUAGE-FEATURE-MATRIX.md`（已并入本文，保留
> 为历史记录）。
> **锚点规范**：每个 `✓` 单元格必须有对应平台测试目录里的 `//@feature <name>`
> 锚点（`pkg/featurematrix` 校验器自动核对，缺锚点 = 测试红）。没有锚点
> 的格子只允许 `~` / `✗` / `-` / `N/A` / `·`。

## 0. 项目总体定位

- **位置**：语言演进影响面追踪层。编译链（.ncoda → 平台产物）与反编译链
  （平台产物 → .ncoda）共用同一矩阵——一个语言特性在每条链的每个平台都有
  独立状态。
- **视角**：ncoda 为圆心，平台是辐射面。第一列 = "ncoda 里这是什么特性"，
  各平台列 = 它的投影形态/状态（KNOWLEDGE-GOVERNANCE 08-29 文档视角原则）。
- **门禁**：修改任一语言特性（词法/语法/语义/类型/任一平台编译或反编译）时，
  必须更新矩阵 + 补测试锚点；**改特性的 commit 必须同时提交矩阵更新**。

## 1. 标签语义

| 标签 | 含义 | 允许条件 |
| --- | --- | --- |
| **✓** | 完整支持，roundtrip/定向断言锁定 | **必须有 `//@feature` 锚点**，否则只许 ~ / ✗ |
| **~** | 部分支持 / 软降级（默认值/近似语义） | 引用覆盖清单（COZE-EXPRESSIVENESS / coze-biz-schema-audit / DIFY-DECOMPILE）记录缺什么、理由 |
| **✗** | 明确缺口（覆盖承诺：不沉默遗漏） | 引用覆盖清单对应条目 |
| **-** | 平台无此概念（语义等价 null / 不存在） | 一句理由 |
| **N/A** | 平台/方向尚无实现目录 | — |
| **·** | 已登记、未评估（待回填标签与锚点） | 过渡态，回填时转 ✓/~ /✗/-/N/A |

## 2. 特性 × 平台矩阵

列：`IR`=语言自身（AST/IR/前端 passes）；`dify编`/`dify反`=dify 编译/反编译；
`st编`/`st反`=coze-studio 编译/反编译；`biz编`/`biz反`=coze-biz 编译/反编译；
`rt`=runtime；`eino`=eino-json。

| 特性 | 语义文档 | IR | dify编 | dify反 | st编 | st反 | biz编 | biz反 | rt | eino |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `var-let` | WORKFLOW-IR / AGENTS.md | ✓ | ✓ | ✓ | ✓ | N/A | ✓ | ✓ | ✓ | N/A |
| `if-else` | ALGORITHM-CONVERGENCE / DIFY-VS-COZE-GRAPH-SEMANTICS | ✓ | ✓ | ✓ | ✓ | N/A | ✓ | ✓ | ✓ | N/A |
| `for-while` | WORKFLOW-IR | ✓ | ✓ | ✓ | ✓ | N/A | ✓ | ✓ | ✓ | N/A |
| `parallel-fence` | AGENTS.md P5 / EDGE-CONTRACT | ✓ | ✓ | ✓ | ✓ | N/A | ✓ | ✓ | ✓ | N/A |
| `switch` | SWITCH-SYNTAX | ✓ | ✓ | ✓ | ✓ | N/A | ✓ | ✓ | ✓ | N/A |
| `attempt` | AGENTS.md return 分层 / DIFY-VS-COZE-GRAPH-SEMANTICS §6.2 | ✓ | ✓ | ✓ | ✓ | N/A | ✓ | ✓ | ✓ | N/A |
| `return-hierarchy` | AGENTS.md / DIFY-VS-COZE-GRAPH-SEMANTICS §6.2 | ✓ | ✓ | ✓ | ✓ | N/A | ✓ | ✓ | ✓ | N/A |
| `yield` | AGENTS.md P5 | ✓ | ✓ | ✓ | ✓ | N/A | ✓ | ✓ | ✓ | N/A |
| `request-input` | binder human_input_contract.go | ✓ | ✓ | ✓ | ✓ | N/A | ✓ | ✓ | ✓ | N/A |
| `output` | NCODA-OUTPUT-MODEL / SEMANTICS-output-answer | ✓ | ✓ | ✓ | ✓ | N/A | ✓ | ✓ | ✓ | N/A |
| `empty-check` | EMPTY-CHECK-DESIGN（已实施，2026-09-01 用户审核通过 B 方案） | ✓ | · | ~ | ✓ | N/A | ✓ | ✓ | · | N/A |
| `foreign-code` | PYTHON-SHIM-TRANSLATION-CONTRACT / PYTHON-CODE-NODE-GENERATOR | ✓ | ✓ | ✓ | ✓ | N/A | ✓ | ✓ | ✓ | N/A |
| `conv-var` | WORKFLOW-IR | ✓ | · | ✓ | · | N/A | · | · | ✓ | N/A |
| `lambda` | DECOMPILER-PATTERN（list-operator lambda 形态） | ✓ | ✓ | · | ~ | N/A | ✓ | ~ | ✓ | N/A |
| `ternary` | pkg/ast | ✓ | · | · | · | N/A | ✓ | ✓ | · | N/A |
| `dict-list` | pkg/ast | ✓ | ✓ | ✓ | ✓ | N/A | ✓ | ✓ | ✓ | N/A |
| `template-str` | WORKFLOW-IR | ✓ | ✓ | ✓ | ✓ | N/A | ✓ | ✓ | ✓ | N/A |
| `for-expr` | WORKFLOW-IR | ✓ | ✓ | ✓ | ✓ | N/A | ✓ | ✓ | ✓ | N/A |
| `field-index` | RUNTIME-ARRAY-INDEXING / NCODA-DOLLAR-ESCAPE-SPEC | ✓ | ✓ | ✓ | ✓ | N/A | ✓ | ✓ | ✓ | N/A |
| `null-coalesce` | route-1a P0b（R4 `??` first-not-null：编译物化 python `x if x is not None else y` shim；biz反 merge 多候选 → `??` 链精确逆；取代旧 truthy 值合并） | ✓ | · | · | · | N/A | ✓ | ✓ | · | N/A |
| `bool-or` | route-1a P0b（R1：布尔或运算符收紧为纯布尔逻辑（与 && 对称）；非 bool 操作数 = 编译错误 + use '??' hint；旧 truthy 值合并退役） | ✓ | · | · | · | N/A | ✓ | ✓ | · | N/A |
| `file-type` | file 类型契约（第一公民） | ✓ | ✓ | ✓ | ✓ | N/A | ✓ | ✓ | ✓ | N/A |
| `explicit-cast` | 显式类型转换契约 | ✓ | · | · | · | N/A | ✓ | ✓ | · | N/A |
| `record-type` | DECOMPILER-PATTERN / AGGREGATION-HOISTING（单字段 record/scalar 窄适配） | ✓ | ✓ | ✓ | ✓ | N/A | ✓ | ✓ | ✓ | N/A |
| `enum` | classify 枚举契约 | ✓ | ✓ | ✓ | · | N/A | ~ | ~ | · | N/A |
| `any-strategy` | ideas-todo/ANY-SHRINK-STRATEGY | ✓ | · | · | · | N/A | - | - | · | N/A |
| `hoist-zero-default` | DIFY-DECOMPILE §0（08-29） | ✓ | · | ✓ | · | N/A | ✓ | ✓ | · | N/A |
| `dollar-escape` | NCODA-DOLLAR-ESCAPE-SPEC / DOLLAR-ESCAPE-DESIGN | ✓ | · | · | · | N/A | ✓ | ✓ | · | N/A |
| `qualified-call` | TOOL-DIRECT-CALL-FORM（ideas-todo） | ✓ | ✓ | ✓ | ✓ | N/A | ✓ | ✓ | ✓ | N/A |
| `import` | TOOL-REGISTRY-LAYOUT | ✓ | · | ✓ | · | N/A | ✓ | ✓ | · | N/A |
| `library` | lib-semantics-language-change（库单元身份：`library <name> @ <semver>;`；只是声明单元的名字与版本，不进 IR / ABI / 绑定） | ✓ | - | - | - | - | - | - | - | N/A |
| `function-inline` | AGENTS.md return 分层（子函数） | ✓ | · | · | · | N/A | ✓ | ~ | ✓ | N/A |
| `subflow` | SUBWORKFLOW-DECLARATION（coze 子工作流声明） | ✓ | ✗ | - | ✓ | ✓ | ✓ | ✓ | ✓ | N/A |
| `env-const` | WORKFLOW-IR | ✓ | · | · | · | N/A | - | - | · | N/A |
| `llm-unified-envelope` | UNIFIED-LLM-SEMANTICS / target-proofs/llm | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | N/A |
| `open-object` | LLM-OPEN-OBJECT / UNIFIED-LLM-SEMANTICS §2 | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | N/A |
| `operation-policy` | 错误处理设计（ideas-todo/2026-09-10-ncoda-error-handling-semantics-and-bidirectional-design.md §1.3 + §10 N1 缺口表） | ✓ | · | · | · | N/A | ✓ | ~ | · | N/A |

### 2.1 已知测试缺口（IR 层无定向测试，2026-08-29 盘点）

历史盘点时以下特性在 IR 构建路径（pkg/ir）缺定向测试锚点：
coalesce-or（2026-09-05 route-1a 拆分为 null-coalesce + bool-or）/ any-strategy /
import / env-const / explicit-cast。
**2026-08-31 全部补齐**（`pkg/ir/gap_test.go` 6 个定向测试 +
`//@feature` 锚点），IR 列已全部转 ✓，当前无缺口。
新特性补测试时转 ✓ 并加锚点。


### 2.2 回填注记（2026-08-30 矩阵回填）

本次回填将大量已实现但未标注的平台列从 `·` 提升为 `✓`/`~`/`-`/`✗`，
并补全 `//@feature` 锚点。各 `~`/`✗`/`-` 格子的覆盖引用：

| 格子 | 标签 | 覆盖引用 / 理由 |
| --- | --- | --- |
| `switch` dify编 | `✓` | switch → question-classifier 多出口渲染（G005） |
| `switch` dify反 | `✓` | dify 反编译恢复多出口（G008） |
| `request-input` biz编 / biz反 | `✓` | **两种规范形**（2026-10-01 用户裁「甲 扩语言」并批准设计说明）：① 有动作 `ask<Form, Action>({…}) as r { action …[ timeout {…} ] }` ⇒ 平台 `answer_type: option` + `branch_0..N-1` [+ `default`]；② **无动作** `ask<Form>({content, fields}) as r;`（无臂 ⇒ 无块；绑定落**语句所在作用域**，与 switch 结果绑定同族 R-71）⇒ 平台 `answer_type: text` + **单条无标签推进口**（出边不带 `source_port`，**不写** `option_type`/`options`）。判据 = `internal/compiler/cozebiz_qa_text_form_contract_test.go`（产物形状 / 作用域 / 闭合不动点 / 负侧点名 / 目标面 fail loud）∪ `platforms/cozebiz/decompile/question_platform_wire_contract_test.go`（文本型不读 UI 残留 options）。正本 = `docs/ASK-SYNTAX.md` §2 · `docs/five-step/spec/QA-question-node-wire.md` · 裁决单 `…-r38`/`…-r39` |
| `request-input` dify反 | `✓` | dify 反编译 human-input → ask 已覆盖 4 例（hitl-*），含悬挂 action、__action_id 映射、timeout |
| `operation-policy` biz反 | `~` | 覆盖：`retryTimes>0` → `with retry(max:N)`（llm/http + 能力头声明 retry 的 qualified op）、llm `timeoutMs ≠ 180000` → config 选项 `timeout_ms`、http timeout → `with timeout(Ds)`。缺口（不发射，登记在错误处理设计文档 §10 N1 缺口表）：**qualified op 的 timeout**（编译端 type_ops.qualifiedOperationSpec 未开 Timeout 策略位 → 发射即 1045 硬错误）、**intent 节点的任何策略**（无 ncoda 表达面；**code 族已于 2026-09-11 闭环**：`foreign code` 能力位按结构开放 + 声明口记录 raising + 失败载荷转换关 required 检查，见 ideas-todo/2026-09-11-pt2-coderunner-policy-domain-decision.md §6）、**未注册插件 / 商业内建的 retry**（读不到能力位，不得臆测）。全量语料实测（2026-09-10）：retry 144 节点 → 发射 132（缺 5 节点：1 商业内建 + 4 未注册插件/反编译失败文件）；timeout 仅 llm/http 可表达。 |
| `lambda` st编 | `~` | COZE-EXPRESSIVENESS 第79行：list-op → Python CodeRunner（软降级） |
| `conv-var` st编/biz编/biz反 | `·` | coze 平台无 `@conversation` 变量声明映射（仅 conversation 操作节点，见 coze-biz-schema-audit §2.17），未评估 |
| `import` dify反 / biz反 | `✓` | 反编译 emit `import "dify.<name>"` / `import "coze-biz.<slug>"` 已测试锁定 |
| `ternary` biz编 | `✓` | render_loop_test.go 有 TestRenderLoopTernary* |
| `function-inline` biz编 / rt | `✓` | 子函数内联渲染 / runtime 测试已锁定 |


### 2.3 biz 列回填（2026-09-26，逐格一手探针）

**口径**（与 §1 标签语义一致，逐条可复核）：`✓` = 该链有**专门实现**且定向测试锁定
（**不要求形态逐字保留** —— 平台本来就用 code shim / loop 容器 / condition clause 承载）；
`~` = 以**另一规范形**承载（软降级，值/语义保真、形态不保真）；`-` = 平台**无此概念**，
产物不需承载且无信息损失；`·` = 仍未评估（有裁决挂起）。

探针 = `tools/five-step-roundtrip`（`--rounds 2/3`，`--compiler nclang-compile --target coze-biz-1.0`
`--decompiler coze-decompile --tool-path examples/coze-reference`）；十一个特性全部**往返闭环 +
零违规**，本轮只回填 biz 编 / biz 反两列。

| 格子 | 标签 | 覆盖引用 / 理由 |
| --- | --- | --- |
| `bool-or` biz编 | `✓` | `\|\|` 落到单个 `condition` 节点的 clause（两条子条件 + `logic=1`）；锚 `internal/backend/cozebiz/render_if_test.go` |
| `bool-or` biz反 | `✓` | `clause.logic` → `\|\|` / `&&` 两向锁；锚 `platforms/cozebiz/decompile/conditional_condition_contract_test.go` |
| `explicit-cast` biz编 | `✓` | `int(x)` / `float(x)`（G14，2026-09-28 用户批准）无平台原生节点 ⇒ 物化为**编译器自建 code shim**（`# ncoda:shim 1` + 正文逐字携带 `int(...)` / `float(...)`）；锚 `internal/backend/cozebiz/feature_matrix_projection_test.go` |
| `explicit-cast` biz反 | `✓` | shim 逆还原 `int(x)` / `float(x)`；锚 `platforms/cozebiz/decompile/shim_reverse_test.go` |
| `dollar-escape` biz编 | `✓` | `$` 是**纯词法**规则（剥除点 = `pkg/lexer/lexer.go::scanDollarEscape`）：产物里不许出现带 `$` 的拼写（`$code` → 平台端口 `code`）；本列锁的是**产物侧契约**，锚 `internal/backend/cozebiz/feature_matrix_projection_test.go`（撤词法剥除 ⇒ 本测试红，且既有产物门禁同时报非法端口） |
| `dollar-escape` biz反 | `✓` | 已 ✓（`platforms/cozebiz/decompile/keyword_field_test.go`：关键字字段名渲染为 `$type`/`$output`） |
| `import` biz编 | `✓` | import 声明的 biz 编面 = 已解析 qualified ToolCall → `plugin` 节点，物理身份/显示名逐项来自绑定表；锚 `internal/backend/cozebiz/render_tool_test.go`（import→ToolCall 的解析在 IR 层，锚 `pkg/ir/gap_test.go`） |
| `import` biz反 | `✓` | 已 ✓（emit `import "coze-biz.<slug>"`） |
| `ternary` biz反 | `✓` | `TernaryExpr` 渲染器 `platforms/cozebiz/decompile/render_ncoda.go`；锚 `shim_reverse_test.go`（`(out >= 90) ? "A" : "B"`）+ `roundtrip_property_test.go`（record 值选择一轮不动点） |
| `hoist-zero-default` IR | `✓` | P28 `ir.HoistZeroArmCarriers`：臂内可证恒零载体上提（「臂写零值」与「臂没写」归一）；锚 `internal/compiler/cozebiz_arm_zero_carrier_hoist_contract_test.go` |
| `hoist-zero-default` biz编 | `✓` | 作者显式写且被下游读的零值初始化物化为常量/恒等 shim（同块同值池化）；**臂未写的零候选不物化**（运行时 merge zero 语义兜底）；锚 `internal/backend/cozebiz/zz_f1_const_pool_test.go` |
| `hoist-zero-default` biz反 | `✓` | 提升声明形态全函数（可零值化 ⇒ `T v = <zero>;`；否则省略类型）+ `*ForExpr` 类型来源；锚 `platforms/cozebiz/decompile/hoisted_var_decl_form_contract_test.go` |
| `enum` biz编 / biz反 | `~` | **按载体分两种**（一手 2026-09-26；正本 = `docs/five-step/spec/type-identity-canonical-form.md` §4.3）：① 载体 = `ask` 的 **action 类型** ⇒ **值集保真、声明抬回**（`examples/nodecoda/61_request_input.ncoda` 往返 r1 还原 `enum RT_action_approve_reject { approve, reject };` —— 枚举**名** = 形状锚，B-H1 收口 2026-10-16 前是 `QaAction<平台 node id>`；成员名 = 选项表 id，且编译器强制「枚举成员 ↔ action 臂」穷尽 —— 少一条臂直接拒收 `TYPE_MISMATCH … missing action branches` ⇒ 不可能丢成员）；② 载体 = 普通字段类型 / 值位置 ⇒ 平台类型名册无枚举、塌成 `string`（`TravelMode.air` → `"air"`，IR 既定映射 `pkg/ir/build.go` "enums become `string`"）⇒ 声明与未用成员不落产物、raising 读不回 `enum`（一手：`enum Priority` 往返后消失、`Priority priority;` → `string priority;`、`kind` 由 `select` 降为 `paragraph`）。**两种载体的名字都不保真**（raising 按**形状锚**重建生成名 —— B-H1 收口 2026-10-16 后不含平台 node id，但也不是作者写的名字）⇒ 本格取 `~`；② 的降级有报账 `TARGET_CONTRACT_DEGRADED`（`pkg/passes/target_validation.go::reportEnumValueSetDegradations`），① 不报（值集有承载还报 = 噪音，会训练人忽略警告） |
| `lambda` biz反 | `~` | list-op 以带 comprehension 的 code 节点承载，载体**不带** `# ncoda:shim` 标记（标记只给单表达式恒等/常量 shim）⇒ 反编译按「用户手写 foreign code」呈现，**不还原 lambda 形态**（与 `lambda` st编 `~` 同族）。一手：`filter(xs,(item)->item>1)` 的产物 c0 里该节点无标记。**已登记** `residuals.yaml#R-102`（裁决单 r18） |
| `function-inline` biz反 | `~` | 内联**不可逆**：子函数声明不恢复（语义等价）。与 `function-inline` biz编 `✓` 合看 —— 编侧内联是特性本身，反侧只剩内联体 |
| `any-strategy` biz编 / biz反 | `-` | 平台类型名册无 `any`；策略 = **透传**（不产生收窄/物化）⇒ 产物无需承载、无信息损失（`pkg/passes` 的 any 诊断是语言面，IR 列已 ✓） |
| `env-const` biz编 / biz反 | `-` | `const` 是**前端编译期替换**（IR 列已 ✓，`pkg/ir/gap_test.go::TestBuildConstEnvReplacement`）；产物只见内联字面量、无声明载体 ⇒ 不构成平台缺口 |
| `conv-var` biz编 / biz反 | `·` | **搁置**（2026-09-26 用户裁定「先搁置，先把主流程跑通」）。一手事实见 `docs/coze-biz-schema-audit.md` §6.12：声明不落产物（平台变量在应用/bot 配置里声明，节点只能"选"）+ 作用域由会话级放宽为 `global_variable_app`（应用级）。**已登记** `residuals.yaml#R-103`（裁决单 r18，三项待定：作用域 / 报账 / `flowMode`） |

**残留（登记，不沉默）**：`lambda` biz反 的「载体不带 shim 标记」是本轮新发现的一手事实 ——
它使 list-op 的**首次往返即不可逆**（r1 起稳定，故 I1 判据天然看不见）。是否给 list-op 载体
加标记（= 扩 shim 闭集）属**契约级**决策 ⇒ 登记 `residuals.yaml#R-102` + 裁决单 r18，不在本轮改动。
本轮的 `~` / `-` / `·` 格**都不要求锚点**（门禁只查 `✓`），逐格理由都在上表与 §6.12。

## 3. 测试锚点校验

- 锚点格式：平台测试文件任意位置的注释行 `//@feature <name>`（name = 矩阵
  第一列标识符，可多行多个）。
- 校验器：`pkg/featurematrix`（`go test ./...` 自动跑）。解析矩阵所有
  `✓` 单元格 → 检查对应测试目录有锚点 → 缺则红。
- 目录映射（校验器内硬编码）：

| 列 | 测试目录 |
| --- | --- |
| IR | `internal/**/*_test.go`（parser/compiler/passes/ir/binding） |
| dify编 | `pkg/backend/dify/*_test.go` |
| dify反 | `platforms/dify/decompile/*_test.go` |
| st编 | `internal/backend/coze/*_test.go` |
| st反 | `platforms/cozebiz/decompile/`（Coze Studio/Coze-Biz 共享强类型反编译器） |
| biz编 | `internal/backend/cozebiz/*_test.go` |
| biz反 | `platforms/cozebiz/decompile/*_test.go` |
| rt | `internal/runtime/**/*_test.go` |
| eino | N/A（未实现） |

## 4. 修改语言特性的门禁（防遗漏）

1. 矩阵定位行（无则新增，含语义文档列）。
2. 全平台 × 方向逐个核对受影响状态，更新标签；`✗→~`、`~→✓`、`✓→~` 必须
   对应实际改动并写明原因。
3. 每个新 `✓` 格子补对应平台测试的 `//@feature` 锚点（fixture + 断言）。
4. 验证：`go build ./... && go vet ./... && go test ./...` 全绿 + 相关 roundtrip
   （涉及边：MATCH=27 门禁）。
5. 语义文档 / 覆盖清单（~ 与 ✗ 引用）同步，INDEX.md 登记。
6. 语言级扩展仍走 AGENTS.md 08-28 审核契约（矩阵更新不是审核替代）。

### 2026-09-05 coze-biz merge 物理编码补全

`null-coalesce` / `record-type` / `llm-unified-envelope` 的 biz 编反边界追加回归：
候选递归 canvas schema（list、record、Optional file）与 LLM 两种引用载体统一
投影；0–2 层嵌套 if 连续八轮编反可编译闭环（两层嵌套固定点诊断仍红，不能称收敛）。测试见 `merge_type_sync_test.go`、
`render_llm_test.go`、`merge_schema_roundtrip_test.go`。不改变语言语义或其它平台评级。
详见 `docs/ideas-todo/2026-09-05-cozebiz-stage8-merge-schema.md`。


### 2026-09-05 coze-biz 递归区域完成值修订（stage9）

上一条 stage8 的两层嵌套固定点红项已由结构归属/完成点修复取代。现在
`TestLLMListMergeRepeatedRoundtrip` 覆盖额外深度 0–4、八轮源码固定点；
`TestNestedRecordMergeInContainers` 覆盖多字段 record 与 for/parallel for。
`TestOptionalRecordLiteralRepeatedRoundtrip` / `TestOptionalLLMPayloadRecordRepeatedRoundtrip`
补 Optional record、array 元素、上下文类型与嵌套 LLM payload 闭环；
`nullable_roundtrip_test.go` 补 foreign-code Optional 接口及递归解码。
共享 IR 仅修类型信息传递，不改语法/类型规则、不改变其它平台覆盖评级。
详细结果及未验证边界见 stage9 记录。

### 2026-09-10 coze-biz 互斥臂汇聚（junction phi）N 臂宿主补全

`switch`（N 臂 + default）与 `request-input`（action 臂 + 可选 timeout 臂）的
**臂内赋值**此前不进入 phi 候选（只有臂显式 return 的尾值进），臂内赋值静默丢失
（候选空的 `variable_merge` + 下游读零值节点，编译仍 SUCCEEDED）；现与 `if` 双臂
走同一条臂数无知规则（候选 = 各臂真实写值直引 + 未写臂直引 junction 前绑定 +
下游绑定重定向）。**评级不变**（`switch` / `request-input` 的 biz编 原即 `✓`，
本条为覆盖补全 + 锚点补齐）：锚点
`pkg/ir/switch_arm_phi_convergence_contract_test.go`（`//@feature switch` /
`request-input` / `var-let` / `for-while`）、
`internal/backend/cozebiz/switch_arm_phi_lowering_contract_test.go`（同锚点）。
全量 672 语料 L4 非成功 3 → 2（Z78 转绿），逐文件状态 diff 无其它变化。
详见 `docs/AGGREGATION-HOISTING-ALGORITHM.md` §17、
`docs/ideas-todo/2026-09-10-junction-phi-n-arm-host-lowering-gap.md`。

### 2026-09-11 `return` 归属 / inline 边界容器上下文屏障（P13）

`return` 的归属由**词法容器所有权**决定（函数值回流 / M17 loop return / 流程终止 /
分支结果值）。此前后端 builder 的容器上下文（`loopStack`/`parallelDepth`/
`branchValueDepth` 等 13 项）**跨 inline 边界泄漏** ⇒ 同一助手函数换调用位置即换归属：
循环体内被夺权（fail loud `function "x" produces no value`）；`parallel` 分支内被误判
为「终止整个流程」（**静默** flow-end）；调用方循环状态同名时函数内赋值被静默写成调用方
`loop state`；switch/QA/attempt 臂内抑制助手自带循环的 M17。修复 = `inlineCall` 边界
**整族快照→清空→恢复**（一条规则，无逐字段守卫）。**评级不变**（`return-hierarchy` /
`function-inline` 原即 `✓`，本条为实现补全 + 锚点补齐）：锚点
`pkg/ir/inline_container_barrier_contract_test.go`（`//@feature function-inline`，
11 位置归属矩阵 + 5 类静默面 + 跨宿主幂等）、
`internal/compiler/cozebiz_inline_container_barrier_test.go`（产物 YAML 单 `end`）。
全量单测失败集 15 → 13（恰好减掉 2 条本族 RED 锚点，新增 0）。
详见 `docs/ideas-todo/2026-09-11-inline-container-barrier-p13.md`、
`docs/TRANSLATION-PATTERN-PUZZLE.md` P13。

### 2026-09-13 循环内 `return`（M17 物化）coze-biz 双向闭环（E15 / 拼图 P26）

coze-biz 此前对**循环体内 `return`** 是明确缺口：前端能力门禁以
`TARGET_NOT_LOWERABLE`（`class=internal` + `pending=true` + `capability=loop.function_return`）
拒绝（`46_loop_escape` / `13_loop_with_answer` 等）。现由 **IR 物化**实现（`return` 结束
当前函数/流程，循环不构成边界——D4）：循环状态对 `returned`/`return_value` + 体内写状态并
`break` + 循环后门控（真臂经值载体终止流程、假臂继续循环后语句）；**值侧（`return <v>`）与
void 侧（`return;`）都实现**，coze-biz 能力行改为**不拒绝**（残留仅 batch / `parallel for`
内 return = `internal + pending`，并发语义另案）。反编译侧把该合成物**折回** `return`（五条
wire 结构判据，不折叠 = 原样抬回）。**评级**：`return-hierarchy` 的 biz编 / biz反 由
「拒绝（缺口）」转 **`✓`**，`for-while` 不变（`✓`）：锚点
`internal/compiler/cozebiz_loop_return_lowering_contract_test.go`（`//@feature loop`，
值侧 7 形 + void 3 形）、`platforms/cozebiz/decompile/loop_return_fold_contract_test.go`
（13 形 + 1 负向）。同轮收口一条**既有**缺陷（标量字面量值位置物化退化成 string；最小复现
`function main() -> int { return 7; }`），锚点
`internal/compiler/cozebiz_scalar_literal_value_position_contract_test.go`。
`examples/nodecoda/46_loop_escape.ncoda` 一轮即不动点；252 例编译 A/B 无新增拒绝；
**671 份语料 A/B（前 `27e4929` / 后 `a92b4ce`）**：首编 SUCCEEDED **665 → 670**
（转绿的 5 份修前报的正是 `TARGET_NOT_LOWERABLE` + `capability=loop.function_return`）、
G-A 语料侧 **I1 成立 77 → 82**、漂移分类逐项零新增（`CHAIN_BREAK` 6 → 1）；
全仓失败集 31 = 31（零新增）。详见 `docs/TRANSLATION-PATTERN-PUZZLE.md` **P26**、
`docs/ideas-todo/2026-09-13-cozebiz-loop-return-lowering-design.md` §13。

### 2026-09-14 `parallel for` 迭代体作用域语义（迭代局部写 / 逃逸读 / 控制转移，拼图 P28）

`parallel for` 迭代体内对**循环外变量**的写此前被 IR 当成「平台循环状态」物化
（`ir.Assign.LoopState + LoopID` ⇒ 产物 `variableParameters` + 体内 `variable_assign`），
而 batch 容器**没有**该平台通道 ⇒ 产物不可导入（反编译硬失败
`loop variable assigner destination is not parent_intermediate`），且编译 SUCCEEDED、L1/L2 全绿。
现按**语言语义**收口（D-L1..D-L4，2026-09-14 用户拍板）：

- **写 = 迭代局部、不逃逸**（死写无害合法）⇒ `parallel for` 的 `stateVars = nil`
  （语句形态 + 值表达式形态同一入口；顺序 `for` 状态通道**保留**，有负向测试）；
- **循环外读**「被迭代写过」的名字 ⇒ **E1036**（拿到的只能是循环前的值 = 对作者写意图撒谎）；
- **`break`** 与 **带值 `return <v>`** ⇒ **E1037**（并发下「停整个循环 / 哪个迭代的值胜出」
  不确定，且平台无 break 通道）；**void `return;` 合法**（不选值 ⇒ 结果确定，
  平台通道 = 容器内节点直连根 `end`；真实导出 5 份在用 —— 初稿把 `return` 整体判错会
  误伤这 5 份，已按一手证据收窄）；
- 产物门禁 L1 新增独立判据：**batch 容器体内出现 `variable_assign` = 违规**。

**评级**：`parallel-fence` 行不变（三 target 仍 `✓`）；coze-biz 侧新增语言规则与其负向锚点。
验收：670 份语料 A/B **HEAD 670/670 → 本树 670/670**（零新增拒绝，语料 0 例 E1036/E1037）。
锚点：`pkg/passes/parallel_for_iteration_local_test.go`（`//@feature parallel-fence`）、
`internal/compiler/cozebiz_parallel_for_iteration_local_contract_test.go`。
详见拼图 **P28** + `docs/ideas-todo/2026-09-14-parallel-for-language-semantics.md`。

### 2026-09-21 switch 无 `default:` 的隐式臂（P1 实现面补齐，值丢失修复）

`switch` 源码**未写 `default:`** 时，未命中任何分类 ⇒ 平台仍走 `default` 端口（OSS 实测：
`backend/domain/workflow/internal/nodes/intentdetector/intent_detector.go:187` `ExpectPorts`
= `{default} ∪ {branch_i}`、`:179-181` `cID64 == 0` ⇒ default；本仓 `renderSwitch` 的
`unownedSwitchPorts` 也把「源码缺 default 臂」与「空臂」并成同一债），但 lowering **没有
合成隐式臂** ⇒ 汇聚组少一个候选 ⇒ **switch 前的绑定静默丢失**（`string v; v = "x";
switch (k) { "a": { v = "y"; } } return v;` 实测：merge 组仅 1 候选，一轮往返后
`v = "x"` 变 `v = ""`）。同义的 `if` 无 else（`synthElse`）与显式空 `default:` 两种拼写都
正确保留 ⇒ 属 **P1「臂集合含隐式 else 臂」在 switch 宿主未落地**（J 规范形 §9.3 step 0）。

现按既有规则补齐：`emitSwitch` 合成空 default 臂（`pkg/ir/build_condition.go:994`）。
QA 宿主不合成 —— 缺 timeout 时**根本不投影 default 端口**（`:1174`，平台无超时边 ⇒ 不存在
未写臂这条控制路径），差异在数据不在规则。

**评级**：`switch` 行不变（三 target 仍 `✓`；本刀修 coze-biz 产物语义，不改语法/类型）。
A/B：92 手写源 **0 产物变化** · 生成器 349 例 **0 变化**（65/65 违规源不变）· 语料 672 份
**逐源判定 0 不同**（68 违规源集合逐条相同）。锚点（矩阵已登记的同一文件）
`pkg/ir/switch_arm_phi_convergence_contract_test.go` 新增
`TestSwitchWithoutDefaultCarriesJunctionEntryBinding`（撤实现 ⇒ 候选 1 vs 2，规则级 RED）。
平台侧读数差异（OSS merge 只跳 `nil` vs 本仓 first_non_zero）登记为**待裁项**：
`docs/five-step/evidence/coze-merge-runtime-semantics.md`。

### 2026-10-01 `ask` 无动作形态（只问不选）—— 平台 `answer_type: text`

**变更点**：`ask` 补第二条规范形 `ask<Form>({content, fields}) as r;`（第二类型参数**缺席** ⇒
无臂、无块）；绑定落**语句所在作用域**（R-71 同族）；`question` 节点**是 φ 宿主 ⟺ 它的声明里
`answer_type == option`**（声明前置，与 `ErrorBranchNodes` 的 `processType == 3` 同模式）；
coze-biz 写面发 `answer_type: text`、**不写** `option_type`/`options`、出边**不带端口**；
读面 `options` 只在 `answer_type == option` 时读。

**评级**：`request-input` 行**不变**（三 target 仍 `✓`）—— 本刀是**形态补全**（原缺口被登记为
「语言面缺形态，待拍板」），不改既有语义规则；`ask<Form, Action>` 的语法 / 语义 / 产物**一字不改**。

**兼容性**：纯放宽（旧源与旧产物逐字不变）。**已实测**：语料 684 源 × 3 轮 **682 全绿**
（`calendar_helper` 转绿：`ok: true` / `violations: []` / r2→r3 不动点），其余 683 源逐字不变；
生成器 534 可编译例 · 违规 0；全仓 `go test ./...` 零新增失败。

**边界（登记，不掩盖）**：无动作 **+ 超时臂** 语言侧拒收（coze-biz 无载体，`residuals.yaml#R-179`）；
**dify / coze-studio** 的无动作载体未接（平台优先级搁置 ⇒ 点名拒收，`residuals.yaml#R-180`）。

**锚点**：`internal/compiler/cozebiz_qa_text_form_contract_test.go`（`//@feature request-input`）·
`platforms/cozebiz/decompile/question_platform_wire_contract_test.go`（同锚点）·
`platforms/cozestudio/parser/question_answer_read_face_contract_test.go`（读面正反两侧）·
`platforms/cozestudio/ir/junction_dispatch_single_source_contract_test.go`（`qatext` 反侧）。

**详见**：`docs/five-step/rulings/2026-10-01-ruling-sheet-r38.md`（设计说明，已批准）+
`…-r39.md`（落地记录：改动面 / 八条撤实现必红 / 读数）。
