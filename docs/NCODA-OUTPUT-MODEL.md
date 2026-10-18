# ncoda `output` 语义模型（v1 提案）

> 状态：语义模型提案，基于 `docs/SEMANTICS-output-answer.md`（§2 平台证据 / §6 语义设计）
> 提炼。语法与平台投影待语义定稿后落 spec/grammar.ebnf 与渲染层。
> 日期：2026-08-23

---

## 0. 设计原则（先行）

**ncoda 语义独立，投影不承诺全覆盖**（规范侧落盘：nodecoda-spec `CONCEPTS.md`）：
ncoda 语义由语言自身定义，不从任何平台节点行为推导；平台渲染是**投影**，允许不完整/
近似/降级，但必须**显式化**（诊断、告警、文档化），绝不静默改变语义。某平台缺少
ncoda 原语的等价节点（如 `output` 在 Dify）是投影差异，不是语言缺陷。

## 1. 一句话模型



`output(expr)` 在流程**执行中**向响应流发布一条**面向用户的中间消息**（进度/安抚/事件），
发布后**流程继续**，不终止、不产生结构化返回数据。

它是 ncoda 语言中唯一的"执行中消息发射"原语。

## 2. 语义定义（正交维度）

| 维度 | 取值 | 说明 |
|---|---|---|
| 面向对象 | 用户（对话响应流） | 不是给调用方/下游的结构化数据 |
| 时机 | 执行中 | 任意流程位置，可多次 |
| 终止性 | **非终止** | 发布后控制流继续（对比：`return` 终止流程、答复端点的 `return template …` 终止回合） |
| 累积 | 按序发布 | 消息流语义：先执行先发布 |
| 产物 | 文本消息 | 表达式求值渲染为文本；非结构化字段 |

## 3. 语法形式

```
output_stmt = "output" "(" expression ")" ";" ;
```

- 单值（**无 key**）：`output("正在生成报告…")`、`` output(`进度 ${pct}%`) ``、`output(status_text)`。
- 表达式可为字面量字符串、模板字符串（含插值）、或求值结果为可渲染文本的表达式。

## 4. 运行时语义

1. 对 `expression` 求值；
2. 将结果渲染为文本消息；
3. 发布到响应流（若求值来源是流式生成，消息可流式发布）；
4. 控制流不变，继续执行后续语句。

## 5. 约束

- `expression` 必须可渲染为文本（string literal / template string / 可字符串化的值）；
  **「可渲染」在 coze-biz 上是三条承载路线**（R-77 收口，2026-09-25）——`output(<expr>)` 与
  同一语义的内插拼写 `` output(`${<expr>}`) `` **必须同产物**（I1），故两条路共用同一实现
  （`pkg/ir/build_stmt.go::templateExpr`）：
  1. **常量折叠**：字面量（int / float / bool / string）直接成逐字文本（`output(3)` ⇒ content `3`，
     **零节点**，不为常量造空信封）；
  2. **值载体物化**：有运行时语义的值（builtin / 算术 / 比较）落一个 producer
     （Synthetic shim `# ncoda:shim 1`），模板按块口引用它的结果口（content `{{result}}`）；
  3. **引用**：已是 producer 口的值（`output(r.text)`）直接引用，**不**物化
     （不为引用造 shim —— 那条 shim 是纯死载体）。
  判据 = `internal/compiler/cozebiz_output_bare_value_carrier_contract_test.go`（同语义两拼写逐字节相同 /
  载体形状 / 引用路径 shim 数 = 0 / `output(template …)` 承载位不受影响）。
  **这三条路共用一个入口**（R-80 收口，2026-09-25）：值物化 = `emitValueExpr`（值位置**唯一入口**），
  由它自挑载体 —— 封闭 shim 子集能表达的落 Synthetic shim（`trim` / 算术 / 比较），
  表达不了的落原生载体：`split(...)` ⇒ **原生 text 节点**（零 shim）、`c ? a : b` ⇒ **junction**
  （与 `let x = c ? a : b` 同形）、数组 / 记录字面量 ⇒ 数组 shim / 记录 shim（与「绑定写法」
  `let a = [1,2,3]; output(a);` **同一段 shim code**）。修前直调 `emitExprCode`（只认封闭 shim 子集）⇒
  字面量在模板位置红而经绑定绿、`split` / 三元两种拼写两种载体，且这两条形状的**往返本来就坏**
  （一轮后拓扑漂移 / 第 2 轮 `UNDEFINED_VAR`）。判据 = 同上文件（四锁含三轮往返 + 存活侧锚点）；
  语料验收 = 672 源 × 3 轮 0 违规、A/C 对拍 672/672 逐字节相同。
  **登记缺口（R-81）**：模板 / `return` 位置的**无名 junction** 反编译后，平台侧 merge 组名
  （`Group1`）被当成 ncoda 变量名写回 —— 既存缺陷、稳定可收敛，属反编译侧命名口径 ⇒
  见 `docs/five-step/residuals.yaml` R-81。
- `output` **不**产生结构化返回数据——最终结果一律走 `return`（结构体已支持）；
- **流程位置不限（2026-09-18 用户裁定；`output` 是普通节点，没有特殊性）**：
  `output` 可出现在 `main` 的任意位置——容器内、以及 **parallel 臂内**。
  平台证据 = 扇出（无互斥端口）的后继里含消息节点的真实导出 24 处 / 15 源，
  其中「两臂汇入同一答复端点」2 处 / 2 源（X77 / X184）——
  复算脚本 `scripts/five-step/evidence_output_message_fanout.py`，
  页 = `docs/five-step/evidence/output-message-node.md`。
  契约 pass（`RESPONSE_CONTEXT` E1044）只保留两类拒绝：非 workflow/advanced-chat
  模式、非 `main` 函数体。（**已作废**：旧文本此处曾列「parallel 分支内」——该
  子句出自 dify 语境，随本条裁定作废；旧文本举的 S110/S82/jianggushi1/S111
  「output 嵌于 loop」四例经**一手实测全部为假**：四份导出的消息节点（type 13）
  数各为 0（即根本没有 output 节点，谈不上在不在 loop 里）。）
- **parallel 臂内的次序语义**：每臂内保序，**跨臂顺序未指定**（wire 也不给顺序）。
- 与「最终答复」的边界：`output` 是中间消息（多次、非终止）；**最终答复不是独立原语**，
  它是**端点的一种取值**（平台 `terminatePlan: useAnswerContent`），语言载体 = 流程出口语句
  `return template "模板文本" using { 名额: 值路径, … };`（用户 2026-09-17 拍板 W1-D6/W2）。
  旧构造 `@answer "模板" k=v;` 已**整条从语言删除**（现在解析报 `Unknown @-declaration`），
  `output` 与 `end` 之间也**没有任何关系**。端点与 `return` 的默认通路（`returnVariables`）
  二选一。

## 6. 与相邻原语的边界

| 原语 | 语义 | 面向 | 终止性 | 次数 |
|---|---|---|---|---|
| `output(expr)` | 中间消息发射 | 用户 | 非终止 | 多次，按序 |
| `return template "模板" [using {…}]` | 最终答复文本（端点 = `useAnswerContent`） | 用户 | 终止（回合） | 至多 1 |
| `return expr` | 最终结构化结果 | 调用方 | 终止（流程） | 分支各 1 |
| ~~`answer(...)`~~ | 删除——中间投递语义由 `output` 承担 | — | — | — |

区分线索：`output` vs `return template` = 中间 vs 最终；`return template` vs `return` =
用户（答复端点）vs 调用方（结构化结果）——两者**共用 `return` 这一条语句形态**，
端点开关决定值侧身份（W2：模板逐字承载、名额表保序逐条承载、零解析；模板槽只能是字符串字面量）。

## 7. 平台投影：渲染层策略谱系（投影是选择，不是宿命）

**原则**：ncoda 语义是定死的；渲染层对每个平台/模式选择一种投影策略，并**显式化**
（编译诊断/文档记录）。谱系：

| 策略 | 含义 | 何时用 |
|---|---|---|
| **忠实实现** | 平台有原生等价节点/机制，直接映射，语义完整 | 平台原生承载（如 coze OutputEmitter） |
| **转义（近似）** | 平台无原生等价，映射到"最接近"形态，语义有可解释偏差 | chatflow 类场景（answer 内容投递 ≈ 中间消息） |
| **模拟** | 平台无原生等价，用现有机制组合出该语义（节点存在但抑制输出 / 默认插件承载） | workflow 无对话流时保留语义载体 |
| **忽略** | 语义在目标平台无意义，显式丢弃 + 编译告警 | workflow 模式的进度提示 |

**选择权在渲染层**，一个原语 × 一个平台可以单独定；但**不得静默**——任何非忠实
投影必须落入目标诊断（REJECTED/PENDING/告警）或覆盖清单（COZE-EXPRESSIVENESS 等）。

### 7.1 三语义 × 平台投影矩阵（当前决策）

| 语义 | coze / coze-biz | dify chatflow | dify workflow | runtime |
|---|---|---|---|---|
| 中间消息（`output`） | **忠实实现**：OutputEmitter（content=模板 + streamingOutput，A2 参数） | **转义**：Answer 节点（内容投递 ≈ 中间消息；语义偏差：answer 拼接进最终 outputs——需显式告警或抑制） | **模拟/忽略**：无对话流——选项 (a) 渲染 output 节点但产物不输出 / (b) 默认 output 插件模拟 / (c) 忽略+告警；**当前实现是静默渲染 answer = 违反显式化，待修** | **忠实实现**：响应流消息事件（不进最终结果，已验证） |
| 最终答复（`return template`，端点 = `useAnswerContent`） | 忠实：Exit·`useAnswerContent` + 模板 + `node_inputs`（逐条承载） | 忠实：Answer（chatflow 内容投递） | —（无对话流；转义为 Output 字段并告警） | 回合终止消息 |
| 最终结果（`return`） | 忠实：Exit·返回变量（ReturnVariables） | 忠实：Output（原 End） | 忠实：Output（累积合并） | 结构化结果 |

### 7.2 失真边界（workflow 模式，§7.1 依据）

Dify workflow 模式承载 `output` 时的语义失真（详析见 `docs/SEMANTICS-output-answer.md` §6.6 或本文件 §7.3）：
1. **平台合法性**：Answer 节点仅 chatflow，workflow 产物导入即失败风险；
2. **结果污染**：graphon `merge_response_outputs` 把 answer 文本**拼接进最终 outputs**——中间消息错误地成为结构化结果；
3. **累积漂移**：多次 output 的独立消息 → answer 键拼接成一段（消息边界丢失）；
4. **流式无处可去**：workflow 无对话通道，answer 流式无消费者。

→ 结论：workflow 模式**不应**走"渲染为 answer"（静默污染）；在 模拟 / 忽略 中显式选择。

## 7.3 四层架构：输出转换层（渲染 AST → 输出工作流）

第四层定位在**渲染 AST** 与**序列化输出**之间：`IR → 渲染 → AST →【输出转换】→ 工作流产物`。

```
ncoda 源码 → IR → 平台 AST（语义载体层:output 节点真实存在）→【输出转换层】→ YAML/zip
                     └──────── 结构投影，不动 ────────┘   └ 裁剪/修改/策略选择,在此层做 ┘
```

**职责**：按 平台×模式 的输出策略，对 AST 做**裁剪**（prune）、**修改**（rewrite）、
**标注**（annotate），并产出诊断（显式化义务）。语义载体（AST 中的 output 节点）始终保留，
**策略决策延迟到输出层**——后续加平台能力/插件时只改本层，不回头改渲染层。

**裁剪规则**（删节点保持图连通）：
- 链式节点（入边 1 / 出边 1）：删节点 → 入边源直接接出边目标（边重连，P1/P3 语义在
  IR 层不变量不变；本层只改输出 AST 的连通性）；
- 多入边（汇聚）：删节点 → 各入边源接出边目标；
- 无出边（叶）：删节点 → 入边源终止（若成为新叶且平台要求 end，补/指向 end）；
- 裁剪后无输出节点的图（如 `output("hello")` 单语句）：按策略补隐式 end 或拒绝。

**机制 ↔ 输出策略映射**（§7 谱系的落地形态）：

| 机制 | 输出转换层动作 | 诊断 |
|---|---|---|
| 禁止（默认） | 裁剪节点 + 重连边 | ERROR/WARNING（显式） |
| 忽略 | 裁剪节点 + 重连边 | WARNING |
| 注释 | 节点序列化为 YAML 注释标记 | WARNING |
| 插件 | 节点替换为默认 output 插件占位 | WARNING |
| 忠实实现 | 不改（原样输出） | 无 |

**当前实现状态**：dify workflow 模式**尚无此层**——`RenderFromIR → SerializeToDict` 直连，
output 静默渲染为 answer 节点（违反显式化义务，§7.2）。修正 = 实现本层，dify workflow
默认"禁止 + 诊断"。

## 8. 设计依据（证据索引）

1. **Coze 平台无 key**：coze-studio `OutputEmitter` 参数面仅 `{content, streamingOutput}`
   （`vo/canvas.go` `Inputs`），运行时 `Invoke` = 模板渲染 → `{output: 文本}`
   （`nodes/emitter/emitter.go`）——X26/X29 的 `"progress"` key 是编译器自造（源自动 Dify
   输出变量名），coze-biz 丢 key 是忠实于平台的。
2. **中间消息与最终文本在 Coze 运行时同构**：`Exit` Build 双线形——`ReturnVariables`→
   纯透传 `Exit`；返回文本→**复用 `OutputEmitter`**（`nodes/exit/exit.go`）→ 渲染机制共享，
   **但共享渲染机制不构成语言载体**（W1-D6）：`output` 是中间消息（`output` 节点，
   与 `end` 无任何关系），最终答复是**端点取值**、语言载体 = `return template … using {…}`。
3. **Dify Output ≠ 中间消息**：Dify Output(原 End) 是"最终结果的结构化收集"
   （文档 + graphon `EndNode` 纯输出收集 + `merge_response_outputs` 覆盖/拼接），
   与 Coze 输出节点是两种语义——ncoda 语义对齐 Coze（本会话已确立）。
4. **现状双语义混用即错位根源**：`output(key, value)` 在 coze 侧丢 key、dify 侧把 key 当
   响应字段名（`render.go` L1631）——同一语法两平台语义不同。单值化 + 结构化归 `return`
   消除该错位。

## 9. 状态与待定项

**已定稿**（2026-08-23）：
- 语义模型 v1（§1-§6）；`output_stmt` 单值化、`answer` 语句移除。
  **2026-09-17 更新（W1-D6 / W2 落地）**：`@answer` 声明**也已删除** —— 最终答复的表达改用
  端点载体语句 `return template "模板" using { 名额: 值路径 };`（模板逐字承载、名额表保序
  逐条承载、零解析；位置判据与臂种类无关，契约测试与探针为实物）。
- 投影策略谱系（§7）：忠实/转义/模拟/忽略；选择权在渲染层，非忠实投影必须显式化。

**待定**：
- **D3-执行**：dify workflow 模式 `output` 的投影落地——在 模拟（a/b）或 忽略（c）中选定
  并实现 + 显式诊断（当前"静默渲染 answer"待修，§7.2）。
- **D3-chatflow 显式化**：dify chatflow 走"转义=Answer"时，answer 拼接进最终 outputs 的
  偏差需要告警或抑制机制（§7.1）。
- 流式表达：`output` 的表达式来自流式 LLM 时如何声明/投影（Coze `RequireStreamingInput`）。
