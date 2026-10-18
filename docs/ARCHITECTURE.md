# ncoda 系统架构

> 本文档是仓库**最新架构**的精炼导读（基于 git HEAD `314316d`，v0.6.0 + P5#4 多出口/throw_error 数组等后续提交），
> 面向 AI / 新读者快速建立心智模型。逐条架构论证见文末「深度文档」指针；顶层不可违背契约见 `AGENTS.md`。

## 1. 一句话定位

Go 实现的 **ncoda 语言编译器 + 工作流工具**。**ncoda 语言是圆心**，dify /
coze-studio / coze-biz / runtime 是它的四个目标（backend target）：`.ncoda`
源码 → 各目标产物（Dify YAML / Coze Studio Canvas JSON / Coze 商业版 ZIP /
运行时 eino 图），反方向各目标产物 → ncoda。v0.6.0 起重塑为统一 CLI 的
**写 → 跑 → 部署**闭环，单二进制交付。

## 2. 总览管线

```
.ncoda 源码
  │
  ▼ L1 词法  pkg/lexer                      tokens（含 span/literal）
  ▼ L2 语法  pkg/parser                      SourceAST（58 节点类）
  ▼ L3 语义  pkg/binding + pkg/passes   TypedBoundProgram（强类型绑定视图）
  │           15 阶段管线（Desugar→Normalize→Scope→DefiniteInit→Effect→Type→Parallel→
  │           LoopState→YieldContract→ResponseContract→CodeAnalysis→Syntax→Semantic→
  │           TargetValidation→CapabilityGate），错误短路
  │           （顺序唯一权威 = pkg/passes/pipeline.go 的 stageOrder 数组；
  │            本行 2026-09-19 按该数组实测校正，此前误写成 Scope 在前）
  ▼ L4 降级  pkg/ir.Build                    结构化 Workflow IR（Block/If/Loop/Parallel/Ref…）
  ▼ L4.5 优化 pkg/ir（IR 优化阶段，平台无关） 折叠等价形态 / 删除无用节点（死 phi、无消费聚合）
  ▼ L5 投影  pkg/backend/flow.Project        节点/边图（边只表达控制流）
  │
  │  ┌─► 目标1 Dify      pkg/backend/dify      WorkflowAST → verifier → analyzer → YAML
  ├──┼─► 目标2 Coze-studio internal/backend/coze    Canvas AST → Studio 运行时校验 → JSON / .flow
  │  └─► 目标3 Coze-biz   internal/backend/cozebiz  WorkflowYAML → MANIFEST + workflow 的 ZIP 包
  │
  └──► 目标4 Runtime     internal/runtime（spec.v1/v2 → eino compose 图 → 本地运行）

  反编译方向（各目标 → ncoda）：platforms/dify/decompile、platforms/cozestudio/decompile、
  platforms/cozebiz/decompile —— 平台图先规范化为结构化 cd AST，再经过结构识别、
  raising semantic gate（Region + Raised Contract 两道集中语义检查）、AST 转换/优化，
  最后渲染为 ncoda；平台差异在适配层。
```

**以 ncoda 为中心的编译 + 反编译双框架**：编译方向（ncoda → 四目标）与反编译方向
（四目标 → ncoda）共用同一套 ncoda 语言语义与类型系统；编译侧严格按
`IR → IR 优化 → lowering`，反编译侧严格按 `结构化翻译 → AST 转换/优化 → ncoda`。
平台差异只出现在 lowering/raising 适配层。框架圆心是 ncoda，四个目标平台
（dify / coze-studio / coze-biz / runtime）是它的辐射面。

## 3. 分层与包地图

| 层 | 包 | 职责 |
|---|---|---|
| 前端 | `token` `lexer` `parser` `ast` `types` `diag` | 词法/语法/类型/58 诊断码 |
| 语义 | `binding` `passes` | 作用域符号表 + 15 阶段语义管线（含 capability_gate） |
| LLM 语义 | `llm` | `LLMSchema`、严格 payload 解码和平台无关 `LLMResponse<T>` 契约 |
| LLM 审计 | `llmaudit` | AST/codemod 驱动的 corpus 语义指纹与差异分类；不进入语言/runtime 核心 |
| IR | `ir` | 平台无关结构化工作流语义（**不存边**，只有 Block/Node/Ref/Source） |
| 投影 | `backend/flow` | 由 IR 结构树**确定性派生**节点/边图（两阶段投影第一段） |
| Dify | `backend/dify`（+`analyzer` `verifier` `graphfacts` `codec`） | IR→WorkflowAST→8 图分析 pass→23 条图后验证→YAML 序列化 |
| Coze | `backend/coze` | IR→Canvas AST→JSON/.flow 序列化；IR 直发，不经 Dify 降级 |
| Coze-biz | `backend/cozebiz` | IR→商业版 Workflow YAML→ZIP 打包；形状由 `platforms/coze-biz` oracle 把关 |
| 执行 | `runtime/`（`spec` `compile` `nodes` `plugin` `state` `expr`） | spec.v1 兼容加载、spec.v2 编译输出 → eino 图 → 节点实现 |
| 平台能力 | `tool` `capability` `importns` `foundry` `target/codec` `pyast` `pygen` | 工具闭集/原生能力表/命名空间/铸造厂/类型编解码/Python 子集 shim |
| CLI | `cli` `cmd/*` | 统一 `ncoda` + 旧命令 argv[0] 兼容别名 |

## 4. 核心语义契约（不可违背，详见 AGENTS.md / EDGE-CONTRACT.md）

- **P1 边只表达控制流**：`Ref`/`Source`（数据依赖）永不投影为图边，只进节点参数
  （`node_inputs {path,ref_node}`、模板 ref、mergeGroups）。渲染器不得重新引入数据边。
- **P2 引用合法性 = 控制流可达**：词法可见（编译期）+ 生产者沿控制流可达引用者（产物层），双过才合法。
- **P3 边集合 = IR 结构树的确定性投影**：边数量是结构的确定函数；`guarded()` 去重是防御性不变量，
  不是生成机制；修复冗余边要回结构层，不靠渲染后过滤。
- **P4 分支端口边永远显式**：`true`/`false`/`branch_error` 出边即使端点连通也必须存在。
- **P5 parallel v4**：`parallel{...}` 是纯控制流栅栏（不产生集合值）；分支透明透出变量；
  同名跨分支导出 = R2 编译错误；分支内 `return` = 流程结束直连 end；多 return 出口是 C 风格语言特性
  （类型一致即合法）；flow-end 后不可达路径必须 dead-path cut；`merge` 只服务 if 分支收敛，绝不用于 parallel。
- **多出口平台形态**：coze/dify 单 end —— **带值多出口经汇聚 merge/aggregator → end**、
  无值（仅副作用）出口直连 end；runtime 首个到达 END 短路。
  （旧措辞 ensureEnd first-write-wins = 已修复的丢值缺陷，2026-09-12 用户拍板收口。）
- **flow-end 逃逸点**：一臂主流程 flow-end 的 live if 不在该层物化 phi；继续臂绑定透出，
  真正互斥汇聚只在外层 escape point 投影。inline/QA/attempt/ternary 的值回流不是 flow-end。
- **错误文本绝不经过 merge**（N3）：每个失败点独立 throw_error、自带错误文本、直连 end（900001）。
- **ncoda 自洽优先**：语言语义是第一性，平台差异是渲染器适配；未覆盖特性记入覆盖清单
  （`docs/COZE-EXPRESSIVENESS.md` / `docs/coze-biz-schema-audit.md`），软降级取平台默认值，硬缺口报错。

## 5. 平台后端形态（四个目标）

| | Dify | Coze Studio | Coze 商业版 | Runtime |
|---|---|---|---|
| profile | `dify-1.16-graphon-0.6` | `coze-1.0` / `coze-studio-flow-1.0` | `coze-biz-1.0` | `spec.v2`（兼容读 v1） |
| 产物 | Workflow YAML | Canvas JSON / `.flow` 信封 | ZIP（`MANIFEST.yml` + `workflow/<name>-draft.yaml`） | eino compose 图（本地运行） |
| 后处理 | verifier(23 规则)→analyzer(8 pass)→serialize | `ValidateStudioRuntime`（编译期暴露平台运行时拒绝项） | `platforms/coze-biz` oracle 零诊断解析 + roundtrip | Normalize 往返 gate + `SpecHash` |
| 降级驱动 | `capability.For(target)` → NATIVE / SHIM / INLINE / REJECT / PASSTHROUGH（PLACEMENT 记录可审计） | 同左 | 同左 | 同左 |

Dify 是唯一走完整「降级→验证→分析→序列化」链路的后端；两个 Coze 后端从 IR 直发（语义在前端，
平台结构在后端，互不依赖）。

## 6. 运行时执行链（v0.5 毕业，可选面）

- **spec.v2**：编译器发出的规范执行格式，携带 `LLMSchema`、metadata 与验证策略；
  **spec.v1** 继续兼容加载并归一化为 string payload/no metadata。Normalize 往返 gate +
  `SpecHash`（缓存 key）。
- **compile**：spec → eino `compose.Runnable`。每个节点 = lambda（输入装配 → 实现 → 按节点 ID
  命名空间包装输出）；整图 AllPredecessor；带端口出边 → `AddBranch`；loop 容器 = 驱动器（内建 body
  子图递归编译，loop-back 边被吸收）。
- **nodes**：21 种节点 kind（start/end/llm/http/tool/agent/knowledge/text/list_op/code/selector/
  aggregate/assign/loop/loop_assign/qa/output/conversation/break/continue/loop_return）。
- **agent**：function-calling / react 双策略 + 内置数据集检索工具 + 插件运行机制 + HITL（interrupt）恢复
  + 会话记忆（advanced-chat）与 RAG。

## 7. 工具 / 插件体系（closed world）

- `tool.Registry`：平台无关能力声明（provider/operation/参数/输出/`interface_hash` ABI 指纹）。
- `capability-manifest/v1`：canonical 语义能力 → 物理 provider/operation 的身份/别名索引。
  四层分工为：语言内核通用调用 → T1 标准能力闭集 → T2 语义领域能力 → T3 平台物理
  provider/operation；`coze-biz.*` 平台命名长期保留为 alias。manifest 只做 identity
  gate，不复制 schema，不改变 wire shape。
- 绑定清单（dify/coze/cozebiz 各一份 `ToolBindingManifest`）：逻辑操作 → 物理插件/API 标识；
  绑定↔registry 两层校验（collection 不匹配 = 硬错误，unpinned = 警告）。coze-biz 商业
  builtin 的 node_type wire 形状由平台适配层的声明式 registry 承载。
- `importns`：平台限定命名空间（`coze-biz.web_search`）；裸 provider slug 路径穿越防护。
- `foundry`：从平台真实导出**采集**（collect）→ **生成**能力契约（generate）→ 注册表合并/校验/存储。
- `pyast`/`pygen`：Python 子集 AST，计算 builtin shim 字节等价生成（len/trim/join/…15 个 builtin）。
- **S7 架构门禁**：`internal/archgate` 三条判据锁定**语言层不被平台绑架**（2026-10-01 补第 3 条）：
  ① 语言内核不依赖 `tool` / platform backend / decompiler 包；
  ② 语言内核不得出现平台**能力身份**字面量（`TestLanguageCoreDoesNotKnowPlatformCapabilities`）；
  ③ **语言层**（lexer/parser/ast/types/binding/token/ir/passes）的**代码**不得出现 **target 名**
  （`dify`/`coze`）—— 注释豁免（引平台证据是纪律），台账逐条带理由且必须与实时仓库一致
  （`TestLanguageKernelCarriesNoUnregisteredTargetNames`）。理由 = 语言（语法/语义/合法性）
  不得随目标平台变：平台事实只住目标层与 lowering（`docs/BACKEND-ARCHITECTURE.md`「编译管线三段式」）。
  IR 只保留通用 qualified capability call。新增能力只能扩展 registry、manifest、binding、
  adapter/wire mapping，不得污染语言内核。入口 = `make gate-precommit`（提交面，2026-10-01 接线）。

## 8. CLI 面

```
ncoda run     <file>   编译 + 本地运行（eino graph；-p/-stream/-memory/checkpoint/RAG）
ncoda deploy  <file>   --target dify|coze-studio|coze-studio-flow|coze-biz → 产物落盘
ncoda new [--project] / plugin / foundry / doctor / version   # --project 生成完整项目目录
```
旧命令 `ncoda-run` / `nclang-compile` / `ncoda-foundry` 为软链别名，按 `os.Args[0]` 分派。
`make dist` 交叉编译 6 平台矩阵（linux/darwin/windows × amd64/arm64，`MATRIX` 可覆盖）+ SHA256SUMS；
每平台目录内置可运行 `starter/` 项目（由 `ncoda new --project` 生成，模板与二进制单一事实源）。

## 9. 验证与质量门（改动后必须全绿）

- `go build ./... && go vet ./... && go test ./...`
- 27 个 `examples/coze-reference` 真实导出可全部编译（27/27 OK）；边审计已转为
  语义重写基线（当前 exact=7/27、topo=7/27、known-semantic=18、isolated=0），
  不再使用旧 “MATCH=27/DIFF=0” 口径；详见 `2026-09-07-s114-single-candidate-condition-phi.md`。
- 图不变量 `TestCozeBizGraphInvariants`（两叶子/无孤儿/端口显式）+ refs 连通性 `TestCheckRefs*`
- 离线平台 gate：`make coze-probe` / `coze-flow-probe` / `coze-biz-probe` / `closure-probe` /
  `dify-verify` / `drift-audit` / `package-demo-check`（corpus 65/65 级）
- Go fuzz 全链路 6 目标 + `scripts/scale_guard.sh` 万级规模护栏
- 契约快照 `contracts/` + `scripts/contract_test.sh`（schema + golden + 负向冒烟）
- 测试治理规范（分层模型/门禁矩阵/质量数字基线/治理流程）：`docs/TEST-GOVERNANCE.md`；
  覆盖率底线 ≥ 60%（基线 72.9%）、runtime `-race` 每次 + 全量 nightly，均在 CI 强制。

## 10. 深度文档指针

| 主题 | 文档 |
|---|---|
| 顶层契约（改代码必读） | `AGENTS.md` |
| 边契约设计与论证 | `docs/EDGE-CONTRACT.md`、`docs/EDGE-CORRECTNESS.md` |
| 工作流 IR 决策（D1–D6） | `docs/WORKFLOW-IR.md` |
| 平台机制/降级/绑定闭集 | `docs/PLATFORM-MECHANISM.md`、`docs/PLACEMENT-CONTRACT.md` |
| 覆盖清单（缺口=违约） | `docs/COZE-EXPRESSIVENESS.md`、`docs/coze-biz-schema-audit.md` |
| 最新里程碑 | `docs/RELEASE-v0.6.0.md`（v0.5 见 `RELEASE-v0.5.0.md`） |
| 工具/插件/铸造厂 | `docs/PLUGIN-ARCHITECTURE.md`、`docs/TOOL-REGISTRY-LAYOUT.md`、`docs/FOUNDRY.md` |
