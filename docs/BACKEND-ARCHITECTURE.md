# 编译器后端架构原则（AGENTS.md 正文搬迁，2026-09-20）

> **来源**：`AGENTS.md` 同名节及其 14 个子节（2026-09-20 整段搬出，**逐字未改**）。
> **条款级别不变**：各子节的「不可违背 / 纪律」标注与拍板日期保持原样；
> 冲突时以用户最新裁定为准，并回改此文件（纪律二）。
> **为什么搬**：原单节 917 行（其中 `### P4 分支端口边永远显式` 单节 333 行），
> 把 AGENTS.md 压成「读不完的散文堆」。AGENTS.md 保留同名标题作索引。

---

## 编译器后端架构原则（不可违背）

### 编译管线三段式：规范化编译 → IR 优化 → lowering（不可违背，2026-09-05 用户拍板）

- **编译不是"源码 → 直接 lowering"**：管线是显式三段，**IR 优化是一等阶段**：
  1. **规范化编译**：ncoda → TypedBoundProgram → 结构化 IR（词法/语法/绑定/类型/
     规范化/definite-init/效果等 pass，错误短路；IR = 语义定锚的结构视图）；
  2. **IR 优化**：IR 建成后、lowering 前，先做**平台无关的 IR 级优化**——等价
     形态折叠、无用节点删除（死 phi 组 / 无消费聚合等，删除判据按真实消费点）；
  3. **lowering（适配）**：优化后的 IR 才投影/lowering 到目标平台形状
     （flow.Project / cfCollector / 渲染器）。平台适配只活在这一层与 raising 边界。
- **禁止"一开始就尝试 lowering 成目标"**：结构问题必须在 IR 层解决，不得靠
  lowering 产物上的渲染后过滤掩盖（P3），也不得为回避 lowering 缺陷反向修改
  ncoda 语义（语言第一性）。
- **IR 优化规则要求**：通用、无例外、递归自洽——过语言递归自洽检查单（孤立
  最小红绿 → 嵌套/容器/循环同一规则仍成立、不加"仅当 X 形态才…"守卫、名字
  有界、×N 轮 roundtrip 不膨胀）；只做语义等价变换与死结构删除，不改变 ncoda
  语义、不丢弃 raising 所需结构信息；推断失败 fail loud，禁止推断型静默 fallback。
- **动机与教训**：X23 家族（无用聚合再生：compile 物化的死 phi/聚合被 raising
  原样抬回、逐轮 +1）根因之一 = 无 IR 级清理阶段、死结构直接进 lowering；
  此类结构应在 IR 优化阶段删除，而不是在 lowering/raising 两侧反复拉锯。
  历史折叠 pass 阶段 1/2 因违反递归自洽被否——**优化规则本身同样过递归自洽
  验收**，不得以形态枚举式推广替代普遍规则。
- **删除必须对 id 空间中性（A 级，2026-09-13 落地）**：IR 优化阶段删除节点时，
  被删节点**已经占用过一个 id** —— 只删不回收会在产物里留下空洞，同一语义在
  「有空洞的那一轮」与「没有该节点的那一轮」之间**整体错位一格** ⇒ 跨轮
  roundtrip 漂移（G-A 一轮不动点门 +1）、`C∘R∘C` 字节闭环破裂。规则：删除后
  **闭合本次删除的足迹**（幸存节点按「被本次删除且 id 数值更小」的个数下移，
  同步重写全部引用：`ref_node` / `blockID` / 模板 `{{block_output_<id>.<port>}}`）；
  **其它来源的既有空洞原样保留**——闭合全部空洞 = 重编号整个 id 空间，须单独立项。
  推论（测试纪律）：**产物 id 不是契约**，测试不得钉死具体数字 id，按角色/形状/
  语义定位（教训：`TestA1CompileDialectVariableAssignParsesAsWithinLoop` 曾以
  `100010` 钉死 loop 状态回声节点，id 闭合后转红）。实现 =
  `pkg/ir/prune_dead_pure_nodes.go` + `internal/backend/cozebiz/compact_pruned_ids.go`。
- **空臂是合法结构，「块会变空」不是删除判据（A 级，2026-09-13 用户裁定）**：
  「在极端情况下，**两臂都空也不能算错**，语义上无错误，语法上无错误」——空臂
  （if/switch/QA/attempt 的臂渲染不出任何节点）是合法 ncoda 结构，编译器自建的
  死纯节点只是开发期脚手架。故 IR 优化的删除判据是**一条、无形态守卫**：
  `删 n ⟺ pure(n) ∧ ¬consumed(n) ∧ ¬structuralCarrier(n)`；
  **禁止**再加「删了这块就空了 ⇒ 保守保留」这类守卫（复发机制）。依据：
  ① lowering 早有空臂规则（`armEntry(arm)==""` ⇒ `env.owe(sel,port)`，义务表五销点，
  P15）——端口边不需要死节点当靶子；② 该守卫在 671 份首编里保下 **251 个**死纯节点，
  100% 原因是它自己，**没有一个**是容器体；③ 平台一手实证：X97
  `cond --true--> merge` 与 `cond --false--> merge`（两臂都空、两端口指向同一节点）。
  残余（登记，不夹带）：容器体被清空 ⇒ 零子节点容器（1324 份真实导出 892 个
  loop/batch 容器 **0 空容器**，平台校验器无此校验 ⇒ 无反证也无正面证据）；
  switch/intent 空臂 raise 读回失败属**既有** N5 缺口（守卫开/关都失败，零回归）。
  实现 = `pkg/ir/prune_dead_pure_nodes.go`（头注「空臂条款」）；
  合同 = `internal/compiler/cozebiz_dead_carrier_contract_test.go`（4 条）。
- **消费普查按槽位穷举，不按记忆（A 级，2026-09-13）**：任何「节点是否被消费」
  的判据必须覆盖**全部真实数据依赖槽位**，包括**模板槽**（`http` 的 url / body、
  text-process template、switch systemPrompt、end answer 等里的
  `{{block_output_<id>.<port>}}`）。漏一个槽位 = 把活节点判死 ⇒ 合法源码编译失败
  （实证：漏 `*HTTPCall.URL` ⇒ `ARTIFACT_GATE … ref does not resolve`）。
- **汇聚活性 = 被数据引用；宿主指针不是消费者（A 级，2026-09-13 第三族收口）**：
  汇聚节点（`*Aggregate`：`variable_merge` / 条件收敛 junction）的删除判据只有一条
  `删除 ⟺ ¬consumed`，`consumed` = `collectIRConsumers` 里存在一条真实数据引用读它
  的**某个输出端口**。
  - **宿主指针不是消费者**：`IfStmt.Merge` / `SwitchStmt.Merge` / `QaStmt.Merge` /
    `AttemptStmt.Exit` / `End.FlowExitMerge` 只是**结构宿主**；四类宿主的渲染器都有
    merge-less 路径（臂尾 + 显式端口边闭到续接点，P4/P15），宿主指针本身不构成消费。
  - **无形状豁免**：`Refs`（legacy 单组）与 `Groups`（多端口 phi）两种编码同一条判据，
    **禁止**「某类汇聚永远保留」的 carve-out（第三族删的就是它：它让每个「写而无人读」
    的提升变量都留一个 `variable_merge`，也让 `compile∘raise` 在该形状上不收敛）。
  - **nil 契约**：`junctionTailRef(agg)` 接受 `agg == nil`（宿主指针在上一轮被清空 /
    本就无汇聚 = 合法状态），判据下沉在该函数，所有调用点一致；**不得 panic、不得猜**。
  - **普查的输入必须穷举（本族真正的战场）**：漏登记一个引用通道 = 静默删活值
    （产物悬空 `ref_node`，且可能 L1/L2 全绿）。除模板槽外还必须覆盖：
    内置工具参数**值树叶子**（`{path, ref_node}`，与渲染器 `wireRefValue` 逐字同判据）、
    `Switch.Query`（分类查询本身是数据引用）、`AgentCall.Query`、`LoopReturnNode.Value`、
    `Loop.{Inputs,StateValues,OutputSources}`、以及**渲染期才产生的引用**
    （`AttemptStmt.FlowExit` 时 exit junction 尾值会被渲染器绑进规范 End 的
    `node_inputs` —— IR 侧没有这个字段，必须由普查补登记）。
  - **机器门禁**（不是记忆）：`pkg/ir/optimize_census_test.go` = 类型级（每个 Node
    实现都要在普查里交代）+ **字段级**（每个 `Source`/`*Source`/`[]Source`/`Ref`/`*Ref`/
    `[]Ref` 字段与模板槽字符串都必须被读，或在 `censusFieldExemptions` 里写明理由；
    豁免表过期同样红）。新增引用字段/新节点类型漏登记 = 门禁直接红。
  - **每条删除规则都要有存活侧锚点**：只锁「不该留」会被「一律删」的错规则满足，
    只锁「该留」会被「从不删」满足——正负两侧必须同时有契约测试。
  实现 = `pkg/ir/optimize.go`（`optimizeGraph` + `collectIRConsumers`）+
  `pkg/ir/build.go`（`junctionTailRef`）；合同 =
  `internal/compiler/cozebiz_dead_junction_contract_test.go`、
  `pkg/ir/optimize_census_test.go`。

- **IR 内部载体名不得穿过 lowering 边界（A 级，2026-09-28 收口，R-139 第一刀）**：IR 的
  **整值标记**（`@whole`，`pkg/ir/whole_value_refs.go` 的 `WholeValuePortName()`）是
  IR 内部的**载体名**，不是任何目标的端口名。它到达 lowering 目标之前**必须**翻译成该目标
  自己的整值表达 —— 两处投影**同解**：
  - **runtime spec** = **空名**（`Source.Name == ""` 即「整个节点输出」，`compile.lookupDotted`
    的既有约定：`lookupDotted(producer, "")` 返回整个输出 map）；翻译点**一处** =
    `internal/runtime/spec/build.go::fromSource`（`Source.Name` 的唯一写点）。
  - **coze-biz 产物** = `{path: "", ref_node: …}`（渲染器 `render_loop.go`「裸名未命中 ⇒
    不是端口 ⇒ 整节点绑定」那一支；平台 wire 上该绑定就是空 `path`）。
  - **一手（672 源跑批）**：标记原名穿透时，循环驱动器按**字段名**去查
    `resolveRef(bodyOut, blockID, "@whole")` ⇒ 落空 ⇒ 每次迭代收集到 `nil` ⇒ 下游用户
    code 读 `item.code` 崩（`TypeError: Cannot read properties of null (reading 'code')`）。
    翻译后定点样本 16 例：6 例转绿（同族 `null (reading …)` 变 `undefined (reading …)`
    或 pass）、**0 回归**。
  - 判据 = `internal/compiler/cozebiz_whole_value_runtime_carrier_contract_test.go`
    （① spec 里不得出现标记 ② 整值绑定必须以空名表达且 BlockID 不丢 ③ 字段路径绑定保留
    真实口名）+ `internal/runtime/compile/loop_whole_value_contract_test.go`（空名必须取到
    整节点输出，不是 nil）。
  - **同一形态的推广**：凡 IR 内部名（整值标记 / 合成端口名 / 内部占位）都不得出现在
    目标端口名空间 —— 目标的端口名空间只认「产出方声明的端口」与「该目标自己的整值表达」。

### 根因优先原则

该原则的四条判据即本节下方各条（先分析语言语义与根因、定位所属层的通用算法、
最小定向回归 + 生成器闭环、fail loud 不静默兜底）；**执行入口 / 开工收工闸门一律
以 `scripts/five-step/verify.sh` 为唯一入口**（2026-09-14）——
⚠ **2026-09-19 退役**（用户拍板）：`scripts/five-step/verify.sh` 不再是提交 / CI 门禁，
本仓现行提交门禁 = 本地 `make gate-precommit`；本条只作历史设计依据，见 AGENTS.md
「五步法门禁退役」节。
方法论总纲保留一句——**先模式 → 归因 → 可逆推演 → 论证 → 动码**：遇漂移/错误先
定位是哪两种语法形状的翻译，再分析 raising 逆与 lowering 正（要求可逆，写清结构可逆
or 语义等价），论证 OK 才动码。

- **禁止打补丁：先结构与设计分析，再定位根因，从根子上解决（纪律；2026-09-15 用户
  拍板固化）**。**「现象消失」不算修好**，也不构成接受该改动的理由；以「先让这个
  语料过了 / 先让这个测试绿了」为目标的改动一律不接受。
  - **必须先在结构与设计层回答**（分析写在产物里，不是口头）：① 语言语义上正确的
    行为是什么；② 结构不变量 / 数据流 / 模块边界上，差异**出生在哪一层**（ncoda
    语义 / AST pass / IR 结构 / IR 优化 / lowering / raising / 平台物理 / 配置）；
    ③ 该层的**通用规则**是什么，它对嵌套与递归自洽（无例外、无形态枚举）。
    三问答不出来 = 还没定位根因，**不得动码**（允许多花时间问，不允许多打一个补丁）。
  - **补丁的形态（一律视为违规）**：针对单个语料 / 单个节点 id / 单条错误文本的特判；
    形态枚举式守卫（「仅当 X 形态才…」）；结果兜底（出错后写死一个值）；丢节点、
    丢参数、静默降级；渲染后过滤掩盖结构错误；把「我们没实现」说成「平台不支持」；
    为保住既有产物形状而反向修改语言语义。根治之后才允许存在的兼容适配，必须有
    **明确契约 + 记录在案**，且不得作为根因修复的替代。
  - **可判定判据（退出码入口）**：`scripts/five-step/verify.sh` 的 **R-REPAIR**
    （撤掉本次实现改动 ⇒ 新契约测试必须 RED，且 RED 精确落在修复面；编译失败不算）
    与 **R-MUTATE**（撤掉判据实现 ⇒ 契约测试必须红）。据此，一次「修好」的最小
    交付 = 契约测试（先 RED）+ 实现（使 RED 转 GREEN）+ 实物登记
    （探针 / 账本格 / 修复记录）——缺一即未完成。
  - **反例（历史复发机制）**：以「生成器造不出这个形状 / 语料里只有一例 / 改语言
    改动面大」为由缩小改动边界；把症状族拆成互不相关的小修（每次一个特判）。
    正确顺序恒为：**结构分析 → 通用规则 → 先红后绿 → 用规模证据证明它真的普遍**
    （生成器矩阵 + 语料做验收与误伤护栏）。

- **先归位形状空间，后归因定位（A 级，2026-09-09 用户拍板固化，防造轮子；
  2026-09-14 归位源换成五步法形状空间台账）**：**归位判据 = 形状空间台账
  `docs/five-step/shape_space.yaml` 的格**（机器可核对：
  `scripts/five-step/shape_space_check.py`，成员未归属 ⇒ 红）——
  **禁止**以「任何散文里没有」作为「这是新模式」的依据。
  每次遇漂移/错误/发散，第一步归位：命中既有格 → 本次任务 = **更新/完善**该格
  （补合同条款、扩覆盖形态、加深递归深度，合同测试先 RED），**禁止另起炉灶
  从零重推**；确属新形状对 / 新出生点 → 才建新格（登记 + 形状空间归属）。
  "前面改过的模式，后面不得再当新问题修一遍"（merge 总体即此纪律：不零打碎敲、
  不逐例打补丁）。**复发拦截（同一症状族第 3 次动手）= 五步法尚无退出码入口**，
  过渡期靠人执行（见「机器权威路由」节「已知缺口」）。

- **收敛验证门禁口径（A 级，2026-09-06 用户拍板固化）**：编反收敛验证分两段，
  不得混淆——
  1. **候选名单 = 生成器报告，不是语料**（2026-09-14 换源）：发现机制 = 生成器集
     （`scripts/five-step/gen_shapes.py` + `gen_run.py`，秒级；撞到新形状**先抽象进
     生成器**再改规则）；**语料 = 验收 + 误伤护栏**（按分类报数、禁止裸计数，见
     「收敛优先」节）。首轮全绿只证明语法合法，不证明语义一致（发散 = 一定存在语义问题）。
  2. **分析只比 r0 ≍ c0**（= A0 ≍ B1）：对报告中的形状/文件只做
     **第一轮原始输入（r0）≍ 第一轮编译输出（c0）**的精确比较来定位语义差异；
     不做逐轮文本漂移分析——逐轮差异是症状不是根因（对齐「ncoda 文本漂移是症状非判据」裁定）。
  3. **修好第一轮差异，后续自然收敛**：发散 = 第一轮就存在语义差异；修复 r0≍c0
     差异后反复处理自然到达固定点。禁止针对深轮产物逐轮打补丁、禁止把 r1/r2/r3
     的逐轮翻新当独立缺陷修。

- 解决问题时不得以临时补丁为目标；必须先分析语言语义、数据结构、控制流和
  模块边界，明确不变量与根因，再从设计和结构上修复。局部绕过、结果兜底、
  特判堆叠只能在根因已经修复后作为有明确契约的兼容适配，不能替代根因修复。
- 处理缺陷时必须先从语言语义、结构化 IR、投影规则和平台契约中定位根因，
  再修改所属层的通用算法；不得针对单个语料、节点编号或错误文本增加特判，
  也不得用丢节点、丢参数、静默降级或渲染后过滤来掩盖结构错误。
- 每个修复必须用最小定向回归锁定语义，再用**生成器集 + 语料**闭环验证证明通用规则。
  修复产出的**实物**（契约测试 + 撤实现必红证据 `docs/five-step/repairs/` + 形状空间
  台账 cell）**即登记本身**：**散文登记不构成完成**（2026-09-14 重写，取代旧
  「同步更新算法文档 / 覆盖清单」义务）。只有「为什么这样选 / 否决了什么」这类
  **无法从实物反推**的信息才写散文，且只写一处（见下方「知识地图与决策分级」）。
- 禁止推断型静默 fallback：类型/契约推断失败不得悄悄兜底写死
  （如 `if t == nil { t = T("string") }` 或直接写死 string）。有错误就爆发出来——
  吐编译错误、fail loud、早死早超生；伪造的类型是隐藏炸弹，会让下游反编译信任
  错误契约而在 L3/L4 断链、且极难排查。推断只从权威源（TypePass / 注册表）取，
  取不到就报错，允许的降级只有记录在案的平台显式默认值。

### 错误分类契约：语言错 / 平台不支持 / 编译器缺陷 / 配置（不可违背，2026-09-13 用户拍板）

- **四类，按「谁该改」划分**（用户决策问题只有这一个）：`class` 字段取值——
  - `language`（**缺省**，空串）：ncoda 源码违反语言规则（语法/绑定/类型/语义，
    码表 1000-1054）。用户动作 = 改自己的代码。缺省即 language 是**兼容契约**：
    语言错诊断的 JSON 输出逐字节不变（不新增 class 键）。
  - `platform`：**不可约**的平台物理限制（平台连通道都没有）。用户动作 = 改写法
    （永久）。message **必须**带平台出处（源码 file:line 或语料计数）+ 官方变通。
  - `internal`：**编译器的锅**——lowering 可约未实现（带 `pending: true`）或产物
    缺陷。用户动作 = 等修/提 issue，可临时绕行。
  - `config`：绑定/注册表/工具路径等输入配置缺失（既不是语言错，也不是平台限制，
    更不是编译器 bug）。
- **类别由「发出诊断的一方」声明，不能从错误码反推**：同一个 `ADAPTER_ERROR` 既
  可能盖「平台没有通道」（platform）也可能盖「我们没实现」（internal+pending）。
  后端用 `cozebiz.ErrUnsupported` 的 `Class()`（**零值缺省 = internal**：渲染器
  拒绝一个构造，在证明「平台没有通道」之前默认是我们的缺口），平台限制必须经
  `ErrPlatformUnsupported` 显式构造；前端门禁用 `CapabilityGatePass.emitTargetNotLowerable`
  （类别取自 `capabilityShapeVerdict`）。**禁止把「我们没做」说成「平台不支持」**——
  那会骗用户永久改掉本来没问题的代码（首次适用：coze 循环内 `return` =
  `internal + pending + capability=loop.function_return`）。
- **可约未实现 vs 不可约必须分开**（`pending` 旗标）：平台有物理通道、编译器尚未
  实现 ⇒ `internal + pending:true`，message 必须写清「编译器未实现」+ 临时绕行；
  平台无通道 ⇒ `platform`，message 必须写清「平台表达不了」+ 官方变通。判据是
  **平台源码 + 语料实证**，不是猜测（coze 循环内 `return` 判定为可约，依据 =
  `break` 写 parent intermediate store + 语料 36/198 循环带 `variableParameters`、
  7 个 body 含 break、10 个把状态变量暴露成循环输出）。
- **能力清单是单一声明点**：`docs/PLATFORM-CAPABILITY.md`（人读）+
  `passes.CapabilityManifestForTarget(target)`（代码）。每条能力 = 平台证据 +
  判定点 + 用户指引 + 类别，且**必须配一个负向契约测试**（撤掉判据即 RED）；
  没有测试的能力条目不算数（同「IR 优化规则的每条消费者/宿主声明必须有负向
  测试」纪律）。
- **检测点三段覆盖，缺一段就有盲区**：① 前端能力门禁（最早、带源码位置）；
  ② lowering 兜底 `ErrUnsupported`（应不可达，仍按同一类别报）；③ 产物门禁
  （`ARTIFACT_GATE` 类 = `internal`，必须独立成立）。**平台物理不变量必须在
  产物门禁里独立复验**，不能只靠上游：`start`/`end` 不得在容器内、容器不得嵌套、
  每条边的 source/target 必须是已声明节点（`exit node cannot have parent` /
  `loop node cannot have parent` / 可达性 BFS `node not found in nodeMap`）。
- **`return` 的作用域 = 当前函数 / 当前流程，循环不构成边界**（2026-09-13 用户
  拍板 D4）：循环内 `return` 穿透循环（M17 = 该语义的 lowering 手段，不是另一套
  语义）。判据必须**与臂种类无关**（裸体 / if 臂 / switch 臂 / intent 臂 / 任意嵌套
  同规则）——`build_return.go` 曾按 `branchValueDepth == 0` 判定，而 intent/switch
  臂也递增该计数，导致同一构造在 if 臂走 M17、在 switch 臂走「流程终止」把共享
  `end` 建进循环体（编译 SUCCEEDED + 门禁绿 + 产物不可导入）。唯一例外 = QA
  action / attempt 臂的 return 是**分支结果值**（P5 subscope return），不是函数返回。

### 节点自带错误策略 stanza 的每一轴必须端到端同解（2026-09-29 一手）

**规则（一句话）**：平台节点自带的 `settingOnError{processType, retryTimes, timeoutMs,
switch, dataOnErr}` 是一个 **stanza**；它的**每一轴**都必须在链路两端同解 —— 编译端
写多少，运行端（`internal/runtime`）就**必须读多少**。**「一轴两处两种行为」= 缺陷**
（本仓「一处定义」在**数据轴**上的镜像；与「失配 ≠ 静默丢弃」同源：丢了不报 = 静默改语义）。

**一手（2026-09-29）**：超时轴在 **code** 节点上被丢了一整跳——
· 编译侧**一直是对的**：作者写 `with timeout(300s)` ⇒ 产物
  `settingOnError.timeoutMs: 300000`（一手探针，`nclang-compile` ⇒ 解包 yaml 读数）；
· 运行侧**断了**：`spec.CodeConfig` 不带 `Exception`、`codeNode` 不读它 ⇒ 子进程执行器
  退回固定 `30s` 常量；而 llm / http / tool / agent **四处都读** `Exception.TimeoutMS`。
· 量级（只读语料 672 源）：`timeoutMs` **6622 处**，值域 `180000 / 60000 / 600000 /
  300000 / 360000 / 120000 / 10000` ⇒ **平台允许跑 3 分钟的节点，我们 30 秒就掐**。
⇒ 判据 = `internal/runtime/compile/code_node_timeout_contract_test.go`（3 格：传输面 /
不发明 / **消费面端到端**）+ `internal/runtime/nodes/code_timeout_budget_contract_test.go`
（3 格：标记 / 人类单位 / 不发明）。**撤实现必红（实测两处）**：撤 `spec/build.go` 的
`Exception:` ⇒ 格 ①③ 红；撤 `code.go::Invoke` 的 `WithTimeout` 下发 ⇒ 格 ③ 红。

**诊断的「归属」必须自报**：节点**没声明** `timeoutMs`（平台语义 = 不超时）时那份预算是
**我们自加的安全网** ⇒ 诊断必须带 `nodes.StubBudgetTimeoutMarker`。否则分诊只能按错误码
猜主体，会把桩的预算记成平台的账（**2026-09-29 修掉的假信号**：`cmd/rt-l3` 曾把整格
`RCODE_TIMEOUT` 记成 `stub-gap`「桩缺能力」，而其中两条一手是用户代码自己 `time.sleep(30)`）。

**同族第二轴：`retryTimes`（2026-09-29 同日收口，R-149）**。与超时轴**同一 stanza、
同一根因**：编译侧照发（`errorPolicySetting` 是 `processType` / `retryTimes` 的唯一发射点），
运行期 code 是**唯一**不读 `Exception.MaxRetry` 的节点族（llm / http / tool / agent 四处都读）
⇒ 产物里写着 `retryTimes: 3`，执行时只跑一次。量级一手（只读语料 322 份 yaml）：非零
**105** 处 / 6573（`1`×65 · `3`×35 · `2`×5）。

- **预算语义 = 跨尝试共享**（一手 coze-studio OSS `vo/canvas.go` 的 SettingOnError.TimeoutMs
  注释「If any retry happens, ALL retry attempts accumulates to the same timeout threshold」+
  `internal/compose/node_runner.go` 把 deadline 建在重试循环**之外**）⇒ 截止设在循环外、
  预算耗尽后不起新尝试。**别把这两条读成一条**：`with timeout(D)` 的语言裁定是**每次尝试**
  （载体轴报账见 `spec/F3-error-policy-value-axes.md`），运行期这两个轴**各按各的载体语义**。
- **`RetryIntervalMS` 在 code 上不读**（不是漏）：coze 家族的产物里没有该轴
  （`residuals.yaml#R-90`）⇒ 平台不会等，我们多睡一觉反而是发明语义。
- 判据 = `internal/runtime/compile/code_node_retry_contract_test.go`（4 格：IR→spec 传输面 /
  **端到端消费**（前 1 次失败、第 2 次成功 ⇒ 节点成功且 python 体被跑 2 次）/ 不发明（无策略
  ⇒ 恰 1 次）/ 总预算跨尝试共享（1s 预算 + 2s 体内睡 ⇒ 恰 1 次尝试））。**撤实现必红（实测
  5 处，各格可分辨）**：重试循环失效 ⇒ 格②；发明默认 ⇒ 格③；截止改「每次尝试一份」⇒ 格④；
  `spec/build.go` 丢 `Exception` ⇒ 格①；发射端不写 `retryTimes` ⇒ 红在**既有**产物面判据
  `internal/compiler/cozebiz_timeout_axis_contract_test.go`（**不为同一事实造第二份判据**）。

### 独立编译器专家审查 gate（纪律；触发点 2026-09-14 与机器门禁划界）

- **分工：机器管「有没有」，本 gate 管「对不对」**（2026-09-14 划界）。五步法机械
  核对**证据是否存在且可复跑**（`R-REPAIR` 撤实现必红 + `R-MUTATE` 撤判据必红）；
  本 gate 只回答机器答不了的那一问——**该规则 / 该归因本身对不对、模式是否完整**
  （覆盖当前形态与递归深度）。它是**抗锚定的第二意见**，不是每族固定仪式。
- **触发 = 下列任一；不命中即跳过，且跳过不构成违规**：
  ① 同一症状族**第 3 次**动手；② 准备新增**形态守卫**、或怀疑「同族第二次同类守卫」；
  ③ 归因**候选 ≥2**、或与既有契约测试的规则文本冲突；
  ④ 时间盒触顶（同一症状族 2 天未收敛）。**最低触发仍成立**：归属（语义 / lowering /
  raising / canonical）尚未定案时必做。
- **门槛仍固定**：写任何修复代码**之前**（S2 出口后、S3 前）。gate 职责 = 归因复核 +
  **模式完整性检查**——对照**已有契约测试**与 `docs/five-step/shape_space.yaml` 的格，
  看其规则是否覆盖当前形态与递归深度（教训：record-in-for-body 缺 loop-state 条款、
  OptionalLLM depth-2 缺调用型 producer 提升判据，均属「归属清楚 ≠ 模式完整」）。
- **机制**：起一个**独立 subagent**（独立上下文，不继承主 agent 的推断链，
  防锚定），角色定位 = **编译器专家**：把「模式」与「问题」发给它，任务是
  **只读审查该翻译模式、分析为什么会出现这个错误**（差异在哪一侧出生）。
- **必须随问题一起发给 subagent 的材料**：
  1. **模式**：翻译模式定位与声称的合同（类型 / 控制流 / 数据引用 / 节点
     形状 / 语句结构 / canonical 类别，如 nested array / loop output family）；
  2. **问题**：最小语料片段（ncoda）+ 平台 A ≍ B 差异证据 + N 语义桥 +
     关键 wire 片段；
  3. **证据指针**：权威（本文件契约节 / 契约测试 / `docs/five-step/shape_space.yaml`）
     与疑似代码位置。
- **subagent 纪律**：只读分析，不写修复代码（除非显式授权实现切片）；必须
  按语言递归自洽检查单审查模式本身（孤立→嵌套→循环容器→递归深度、无
  形态枚举守卫、名字有界、×N roundtrip 不膨胀），并回答审查三问：ncoda
  语义错误 / 平台投影不匹配 / raising 硬映射平台物理。
- **产出与汇合**：subagent 返回独立根因结论 + 证据链 + 修复方向（归属层）；
  主 agent 复核证据后决定采纳或否决；分歧以证据为准并记录，不得以角色权威
  压过证据。该 gate 是防锚定的第二意见，不替代主 agent 的最终验证与全量闭环。

### 收敛优先：不变量先行 + 机器前置（I1–I3 = 不可违背；其余为纪律，2026-09-12 拍板）

本节是「收敛优先」的**唯一契约处**（= 五步法第 1 步的判据正文）：

- **收敛的定义 = 不变量，不是现象**：`decompile ∘ compile = normalize`（幂等）。
  三条可执行推论：**I1 一轮即不动点**（`N2 == N3` 且 `compile(N2)` 与
  `compile(N3)` 的**图**（节点类型多重集 + 边四元集）相等）、**I2 不膨胀/不振荡**、
  **I3 命名有界**。禁止用「深轮最终会停」代替「一轮即停」。
- **开门必齐报（硬门）**：**R-SHAPE 第 2 步形状空间**、**R-RT 第 1 步跑批棘轮**、
  **R-GEN 第 4 步生成器** 三层数字必须齐报；
  **ncoda 文本判据与图判据分开报**，禁止混成一个数字。
- **反例必须分类后报数**（禁止裸计数）：`DEAD_NODE_RESIDUE` /
  `UNDER_MATERIALIZED` / `RENAME_ONLY`（不计缺陷数，单列）/ `STRUCTURAL` /
  `COMPILE_FAIL`·`DECOMPILE_FAIL`。人只对**首次出现的类别**做归因。
- **机器前置**：任何 roundtrip 修复**先有生成器覆盖该形状**，后有修复；
  语料撞到新形状 → **先抽象进生成器**再改规则；只对单一语料打补丁、
  生成器造不出的形状 = **不接受**。
- **快循环**：日常迭代只跑生成器集（秒级），全量语料只在提交前验收。
- **补丁债务**：新增「仅当 X 形态才…」守卫 = 红灯；确无法普遍化的守卫必须
  就地 `//@guard-exempt:` 标注 + 登记，未登记守卫视为违规。**该检查五步法尚未重造**
  （属已登记缺口，过渡期靠评审）。
- **契约级只有一条，其余全是它的推论**：
  **「修好了」的判据是「规则成立」，不是「现象变小」**——机械形式 = 撤掉实现改动后
  新契约测试必须 RED（由 `R-REPAIR` 实测：`scripts/five-step/repair_check.py`）；
  **单点绿不算完成**。撤**判据**本身必须红（由 `R-MUTATE` 实测：
  `scripts/five-step/mutation_check.py`）。

- **闸门是退出码，不是建议（A 级，2026-09-14 用户拍板落地；⚠ 账本部分 2026-09-19 已退役）**：
  ⚠ **2026-09-19 起**：下面的「五步法门禁」**不再作为提交门禁**（理由见「工作索引」节的退役清单）；
  **现行提交门禁 = `scripts/verify.sh`（build + vet）**。本条其余内容保留作历史与设计依据。
  原文：纪律早已成文，但实测仍在违反 ⇒ 把机器能判定的部分变成脚本，**提交前必须过闸**：
  - **收工**：`scripts/five-step/verify.sh`（钩子模式）——检查项 = R-SELF 判据自检 ·
    R-MUTATE 撤判据必红 · R-AUDIT 元层体检 · R-SHAPE 形状空间 · R-ISO 遗产隔离 ·
    R-REPAIR 撤实现必红证据（快模式只查指纹新鲜度）；
  - **CI / 全量**：`scripts/five-step/verify.sh --full`——另加 R-RT 第 1 步跑批棘轮 ·
    R-GEN 第 4 步生成器 · R-BUILD `go build` + `go vet`；
  - **开工**：五步法**尚无独立开工闸门**（已登记缺口）——过渡期靠人执行：同一症状族
    第 3 次动手先回炉归因 / 拆子症状；时间盒触顶（同族 2 天未收敛）先拆再动。
  - **绕过只在明确有意时**：绕过必须留痕（`scripts/five-step/track.py --record` 写
    `.git/five-step/events.jsonl`），**放行是留痕不是静音**；拦截次数与绕过率由
    `scripts/five-step/track.py --report` 复算。
  - **闸门自己也要被验**：每条判据必须实测「会红 + 会绿」两侧（撤掉判据即失效的负向
    测试纪律，与 IR 优化规则同源）——`R-SELF` + `R-MUTATE` 就是这条纪律的机械形式。

### 循环状态的收集必须子树闭包，禁止按容器类型枚举（不可违背，2026-09-13 用户授权收口）

`while` / `for` 循环**状态**（`nl.StateValues`/`StateTypes` → 平台 `variableParameters`
= 反馈口 + 入参）的判据只有一条：**该循环体的整棵子树里出现过对「循环外已绑定变量」
的赋值**。实现只能走通用遍历器（`ast.Walk` + visitor），**禁止手写 `switch st.(type)`
枚举语句容器**。

- **教训（2026-09-13，§10.6-3 族）**：`pkg/ir/build.go` `collectAssignedVars`
  曾只枚举 `Assign` / `If` / `For` / `While` ⇒ `switch` / `attempt` / `parallel` /
  `ask` 分支体与**表达式位** loop-expr 里的赋值**整体不可见** ⇒ 变量不被判为循环状态
  ⇒ 循环内写入不进反馈口（循环后读到循环前初值，**编译仍 SUCCEEDED = 假绿**），
  且该汇聚无消费者被 IR 优化删除 ⇒ raising 读回宿主臂为空、出码**语法非法**。
  `if` 当时能工作**仅仅因为它恰好被枚举了**，不是设计使然。
- **为什么是「禁止枚举」而不是「补齐第四个 case」**：语句容器家族是**开放的**
  （现含 if / for / while / parallel / attempt / switch / ask，以及出现在**表达式**里的
  loop-expr / parallel-for）；每漏一个 = 一条静默丢值通道。`ast.Walk` 对**未来新增
  成员自动封闭**——这才是「一条规则自始至终有效」（语言递归自洽·最少例外）。
- **同机制先例与孪生体**：`pkg/passes/loop_state.go` `LoopStatePass.collectWrites`
  早已用 `ast.Walk`（IR 层必须与其对齐）；同族反模式孪生体 =
  `platforms/cozebiz/decompile/generate.go` `hoistEscapedBranchVars` 手枚举**漏
  `While`/`AttemptStmt`**（**已登记残余 R4**，当日无实测症状，单独跟进）。
  更早的同类教训 = S107 desugar 手枚举漏容器 ⇒ elif 中臂丢失。
- **方向上的安全性**：收集集合**从子集变超集**是安全侧——漏收 = 丢值（不可逆），
  多收 = 多一个反馈口（若无人读，由「汇聚活性 = 被数据引用」清理）。因此泛化遍历
  是本规则唯一正确形态。
- **验收要求（先红后绿 + 递归自洽）**：撤掉实现改动后新契约测试必须 RED（R-F3）；
  RED 必须**精确落在修复面**（旧枚举已覆盖的 `direct`/`if` 应保持绿，其余宿主红）；
  覆盖须含**嵌套/递归深度**（宿主内再嵌宿主）与**负向**（`@conversation` 变量不得
  进循环状态；循环**外**的赋值不得进循环状态）。锚：
  `pkg/ir/loop_state_nested_host_test.go`、
  `internal/compiler/cozebiz_loop_state_nested_host_contract_test.go`。
- **本族已知残余（清单不在本文件维护 —— 实物 = 契约测试 + 差异账的 `findings` 表，索引见 `docs/INDEX.md`）**：
  2026-09-27 逐条复核，四条收口：① **空臂不变量**（曾记「raising 死清扫清掉臂内唯一纯写
  ⇒ 臂读回为空 ⇒ 出码语法非法」）= **前提被本文件「空臂是合法结构」条款（2026-09-13 用户
  裁定）推翻 ⇒ 退役**（一手：官方判据 `tools/five-step-roundtrip` 五形状 —— if / attempt /
  循环内嵌 if / parallel / switch，臂内唯一纯写且无人读 ⇒ 全 `ok=true`、`violations=[]`；
  r1 保留空臂且可再编译）；② `attempt` 往返 `RS-ERR-002`、③ `ask` 往返 `QaForm<id>`（**2026-10-16 已收口**：QA 类型名改形状锚，不再携带平台 node id；见 `docs/five-step/rules/B-record-name-anchor.yaml` 洞 B-H1）
  UNKNOWN_TYPE = 均**已闭**（2026-09-23 r10 证据）；④ 逃逸提升孪生体（`hoistEscapedBranchVars`
  漏 `While` / `AttemptStmt`）= **已修**（收集与重写同源到 `stmtChildBlocks`；判据
  `platforms/cozebiz/decompile/hoist_escape_subtree_contract_test.go` + 撤实现必红 2 格）；
  ⑤ 外层作用域变量**只由内层 while 写**时 pass 层（最内层 owner）与 IR 层（子树闭包）
  **归属分歧** = **触发条件登记**（触发 = adapter 开始接受嵌套 loop 容器；今日不可观测），
  已由 characterization 测试钉住。
- **分层断言纪律（本族 S2-gate 建议）**：**产物层**（`variableParameters` 与 loop
  `node_outputs` 同时含该变量）/ **往返层**（语法合法 + marker 存活 + 1 轮不动点）/
  **既有缺口层** 必须分开断言；别案缺口（如 attempt/ask 的 raising 断链）**不得**
  混作本族回归判据，也不得让「别案永远红」的断言伪装成本族契约。

### 语言第一性与平台适配边界（不可违背，2026-09-05 用户拍板）

- **可逆性是「当前阶段的强约束」，不是永久承诺；内核大小与可逆性同进退
  （不可违背·判据分层，2026-09-17 用户拍板）**：用户原话——「我们现在要求的
  可逆，反编译其实是一个很强的条件，编译输出的过程中，很多类型信息会丢失的，
  反编译的时候其实是很难找回来的，在语言的早期阶段，我们通过缩小 ncoda 的
  内核，来保证这个。在语言内核稳定后，未来，我们再逐步放弃可逆的强要求。」
  - **理由（不可从实物反推，故写在这里）**：lowering 是**有损**投影——平台 wire
    不承载 ncoda 的全部类型信息（物理端口名、零值缺失、required 废弃、merge
    的控制流编码……）。要求 `decompile ∘ compile = normalize`（I1–I3）在**任意
    语言表面**成立，等价于要求「凡不可从 wire 恢复的形状，都不得进语言」。
  - **当前阶段的策略 = 缩内核**：早期靠**收窄语言表面**（少一种形状、少一个内建、
    少一个关键字）来把可逆性买下来。故遇到「这个形状不可逆」时，**首选处置是
    评估能否从语言表面移除或收窄**（先例：`print` 逐出语言 = A12-H17；
    `tool()` 动态内建让位于 import 的强类型化），而不是在 lowering/raising 两侧
    互相堆适配去追可逆。
  - **阶段切换的条件与纪律**：放弃强可逆要求须**用户拍板**（属语言级决策），
    且切换后第 3 步的判据（R-RT / R-REV 棘轮）必须同步改写——**不得**在
    强约束仍然生效时用「反正以后会放弃」为由放宽某条判据（那是静默降低判据）。
  - **不可逆形状的处置通道 = 提出 → 分析 → 用户裁决 → 缩小语言规格（A 级，
    2026-09-17 用户拍板）**：用户原话——「针对遇到的不可逆模式提出来，我们分析
    裁决，在我批准后可以缩小语言规格。」故遇不可逆形状时的动作顺序是固定的：
    ① **提出来**（登记在机器面：`docs/five-step/reversibility_baseline.yaml` 的
    欠账棘轮 + `docs/five-step/rulings/` 的裁决页，**不得只写在对话里**）；
    ② **分析**（作为编译器专家：是哪一层的信息在 lowering 丢了、能否从语言表面
    收窄掉、收窄的代价 = 哪些语料/产物形状要改）；
    ③ **用户裁决**（语言级决策，agent 不得自行缩小规格）；
    ④ **批准后**缩小语言规格（`docs/nodecoda-spec` 侧 + 实现 + 判据 + 语料清单）。
    **未获批准时不得动语言表面**；也不得用「以后会放弃可逆」为理由放宽判据。
  - **与「语言第一性」的关系**：本分层不削弱语言第一性——它是**同一件事的另一
    半**：语言自身定义什么该进内核，可逆性给出「什么时候内核可以长大」的门槛。
    两阶段责任（先证明语言语义正确、再证明适配正确）不变。
- **语言先自洽，平台后适配**：ncoda 的语法、类型、控制流、null/Optional
  和表达式语义是第一性的，必须先作为一门独立语言定义并验证自洽；不能因为
  coze-biz / dify / coze-studio 的节点形状、merge 机制或运行时限制，反向修改
  ncoda 的既有语义。
- **语义不向平台妥协**：平台与 ncoda 语义不匹配时，优先修复所属平台的
  lowering/raising（适配、编码、解码、结构投影）边界；不得把平台物理机制伪装
  成 ncoda 语言实体，也不得以平台方便为理由硬改 ncoda 语法或类型规则。
- **两阶段责任**：先证明 ncoda → TypedBoundProgram → 结构化 IR 的语言语义
  正确，再证明 IR → 平台产物以及平台产物 → IR/ncoda 的适配正确。平台的
  `variable_merge`、CodeRunner、端口包装、零值物化等，只是 lowering/raising 的
  实现手段，不自动获得 ncoda 语义地位。
- **修改语言的门槛**：只有在独立于任何平台的最小案例、嵌套/递归案例和
  roundtrip 证明都显示 ncoda 本身不自洽时，才允许修改语言语义；若仅有某个平台
  无法表达或错误表达，必须留在平台适配层解决，并记录平台契约和残差风险。
- **审查问题**：每个修复必须先回答“这是 ncoda 语义错误，还是平台投影不匹配？”；
  未完成该归因，不得通过增加平台特判、零值兜底、merge 重写或语法放宽来掩盖
  根因。
- **方向判据：raising 不硬映射平台物理；lowering 考虑平台现实（A 级，
  2026-09-05 用户拍板）**：错误复盘 = 我们曾把根本不区分 nil/null/zero 的平台
  物理形状硬着向 ncoda 映射（required:false→`?`、VA 全 nil→null、批量缺失→
  Optional<array>），制造 84 首编失败与 X23 式反复拉锯。裁定：ncoda 语言
  完整性第一，由 ncoda 自身定义并自洽证明；**raising（平台产物→ncoda）不把
  平台物理机制伪装成 ncoda 语言实体**（平台缺失/nil/required:false 按 null
  契约「静默缺省吸收」处理，不反向制造 Optional/null；可判空只来自作者显式
  书写）；**lowering（ncoda→平台）按平台现实输出物理形状**（LLM 输出
  required:false、VA/merge 作为 if 收敛物理节点、CodeRunner shim 等是 lowering
  适配责任，不从类型 Optional 推导平台物理字段）。审查问题升级为三问：ncoda
  语义错误 / 平台投影不匹配 / raising 方向硬映射平台物理。**平台语义权威 =
  runtime 代码行为（行动），不是帮助文档/产品文案/注释（嘴上）**——merge 文档
  "全空返回 null" 与 runtime FillZero 兜底的不一致曾是最误导源；文档与实现
  冲突时以实现为准并显式记录。

### 语言递归自洽原则（不可违背，2026-09-04 用户拍板）

语言语义与编译器/反编译器表示规则的任何修复或新特性必须满足递归第一性及其
两条推论（正式论证与检查单 = 本节 0-2 条）：

0. **递归第一性（根本）**：语言 = 有限规则经**递归**展开覆盖无限结构——
   只有递归才能把有限规则扩展到无限语言，是语言设计的最高优先级考虑。
   设计/处理语言相关编程问题（语法/语义/表示规则/编译与反编译算法/覆盖
   清单）时，必须显式考虑递归情形：规则必须对递归（自嵌套、任意深度）
   封闭；把递归规则落到具体结构上的一次次实例化 = 嵌套（「把递归缩窄
   就是要考虑嵌套」），验证嵌套 = 验证规则在该结构上的递归展开。任何不
   支持递归自应用的规则/特性 = **语言的断点**，语料会在更深结构撞上它。
1. **嵌套自洽（推论·验证维度）**：规则在孤立、单层情形验证通过后，必须加
   嵌套/递归环境验证——if 内 if / 循环（batch/loop）容器内 / 候选再入
   （merge 组候选 = 内层 merge 组端口）。特性在递归展开任意深度仍自洽才
   算成立；单层成立 ≠ 特性成立。
2. **最少例外（推论·设计维度）**：自洽 = 一条规则自始至终有效、没有例外；
   自洽的语言包含最少例外。例外 = 规则在某一深度/形态失效 = 递归断点。
   规则若需守卫/形态枚举/特判才能安全生效 = 规则没找对，不得以新增守卫
   作为修复手段（形态枚举式推广「单层→容器→多字段→…」是反模式，语料
   总会在更深嵌套撞进例外区）。例外只允许 = 记录在案的平台显式默认/兼容适配。

修复验收检查单（自洽判据，随根因优先原则执行）：
- 孤立最小案例红绿 → 同一规则 + 一层嵌套仍成立（不靠新守卫）；
- 实现中出现「仅当 X 形态才…」的新分支 = 红灯，回头找无例外的普遍规则；
- 名字空间有界：表示实体的名字/字段集不得携带逐轮再生的局部 id（语义锚名 =
  最终消费字段，或可逆折叠回写，两种达标路径）；
- ×N 轮 roundtrip 不膨胀（嵌套形态下多轮漂移不增长，而非仅单轮可编译）。

> 教训来源（违反二原则的复发史）：drop 五刀、X4 冗余零值赋值消除、折叠 pass
> 阶段 1/2 均以「同根因在更深嵌套的新形态」复发——X23（漂移 delta 827×2）
> 即 X4 已修特性（分支赋值/零值吸收/phi 物化）在嵌套 if + batch 循环内的
> 不自洽实例；其修复不得再走「多字段 consumer 特判」，须按无例外普遍规则
> （最外逃逸点按语义字段 flat merge、内层只赋值不汇聚）设计并先红后绿。


### P1 边只表达控制流；数据依赖只走节点引用

- IR 的 `Source` / `Ref`（`BlockID` / `Producer` + `path`）是**数据依赖**，
  **永不投影为图边**。它们只进节点参数：`node_inputs` 的 `{path, ref_node}`、
  模板 ref、`mergeGroups` 的 `{blockID, name}`。
- 图边（`edges`）只表达控制流：线性推进、分支端口（`true` / `false` /
  `branch_error` / `default`）、循环端口（`loop-function-inline-output` /
  `loop-function-inline-input`）、聚合汇入。
- 任何"数据边"（producer → consumer 的引用连线）都是**架构错误**——即使两个
  节点在控制流上连通也不需要额外的边，连通性本身已保证执行顺序。
- 修改渲染器时不得重新引入数据边；发现"多余边"要回到结构投影层修复，
  不是靠渲染后过滤兜底。

### 条件 merge 翻译门禁（A 级，2026-09-06 用户拍板）

- 条件分支中的 `merge` / `VariableAggregator` 必须统一反编译为**提升变量 +
  分支内赋值**：先在条件外声明聚合变量，再在 true/false 分支写入各自候选，
  下游引用该提升变量。
- 禁止用“空 `if` + 相邻 `x ?? fallback`”代替该翻译，也禁止把条件 merge
  退化为 `??`/`||` 值合并。`??` 只用于没有互斥控制流证据的值选择；条件
  merge 的控制流语义必须保留。
- 当平台把两个直接边候选都接到 merge、候选没有显式分支标签时，只有能由
  条件操作数和候选顺序无歧义恢复 true/false 候选才允许提升；推断不足必须
  保守回退，不能猜测或硬编码工作流/节点。
- 该门禁适用于单层、嵌套、循环容器和 record 字段；验证单层通过不代表规则
  成立，必须验证递归应用与多轮 roundtrip 不膨胀。

### P2 引用合法性 = 控制流可达（连通性契约）

- 下游节点可以引用上游节点的 output，因为它们在一条控制流路径上连通。
- 跨路径 / 后向引用是编译错误：IR 词法作用域在编译期拒绝（`Undefined variable`）；
  渲染产物层由 refs 连通性校验拦截（L2 `checkRefsResolve` 的可达性检查）。
- 新增引用路径时，必须同时满足：词法可见（编译期）+ 生产者沿控制流可达引用者
  （产物层）。两条都过才算合法引用。

### P3 边集合是 IR 结构树的确定性投影

- 每条边必须能追溯到 IR 结构（`Block` 顺序 / `IfStmt` 分支 / `LoopStmt` body /
  `ParallelStmt` 分支 / `ReturnStmt` 终点）的固定投影规则；边数量是结构的确定函数。
- 渲染器不手写"额外"边；看起来多余的边应在结构层消失，而不是发射后过滤。
- `guarded()`（连通性去重）是防御性不变量，不是生成机制——修复冗余边要回到
  结构投影，不能依赖过滤兜底。
- 目标架构：两阶段投影——先由 IR 结构树构建逻辑控制流图（边带语义分类），
  再映射到平台 YAML（端口名 + 容器嵌套）。见 `docs/EDGE-CONTRACT.md` §3。
- 平台落地形态差异是适配不是债务：dify/coze 用双遍 `flow.Project`（边集合是
  IR 纯函数）；coze-biz 用单遍 `cfCollector`（`refsLoopVar` 依赖渲染产物，
  双遍会丢信息——评审否决 #10 迁移，论证见 `docs/COZEBIZ-SINGLE-PASS-PROJECTION.md`）。

### P4 分支端口边永远显式

- `true` / `false` 等分支端口边即使端点已连通也必须显式存在（真实导出契约：
  每个 condition 的出边都带分支端口）。
- **condition 的 `true` 与 `false` 两个端口都必须存在**（不只是「存在的边端口名
  合法」）：语料实证 **577 个 condition**（coze-biz 363 = 135 根 + 228 嵌套；
  coze-studio 214 = 98 根 + 116 嵌套）**0 例缺端口**；平台自身校验器同规则
  （expected port 集中每一个都必须有出边，`canvas_validate.go`）。L1 门禁按
  此收口（收口范围 = true/false），缺任一端口 = 不可导入的残缺产物。
  多路条件的 `true_1` / `true_2` 序号端口只出现在真实导出（coze-biz 8 例 /
  coze-studio 9 例），我们**从不生成**（自产物 591 个 condition 全为 {false,true}）；
  日后支持多路条件投影时门禁需同步扩展。
- **`break` / `continue` 是迭代终结子（非流程叶子）**：结束的是**当前迭代**，
  由容器在迭代边界检查标志后跳出（runtime `loop/loop.go`），因此按设计**零出边**
  且**不通往 `end`**（语料 40 个 break + 26 个 continue，**66/66 出边数 = 0**，
  其中 coze-biz 一手导出占 53 个——**非跨平台推断**）。平台校验器逐字豁免该
  出度检查（`canvas_validate.go`「Break, continue without checking out degrees」）。
  可达性口径 = 「每个节点都能到达 `end` **或**某个迭代终结子」；门禁 L1 对其
  豁免「必须有出边」判据（仍要求有入边），**且豁免有位置前提：必须在容器内**
  ——根画布的终结子会被平台 `PruneIsolatedNodes` 静默裁剪（`parentNode == nil`），
  门禁必须 fail loud（我们前端另有 `NOT_IN_LOOP` 拦截，但那是前端契约，
  产物门禁须独立成立）。与 `throw_error` 判据相反且都成立：`throw_error` 的
  终止语义不由节点名给出，终结子的终止语义由平台物理给出。
- **端口义务是数据结构，不是各宿主各销账点的记忆（A 级，2026-09-11 用户授权
  的结构性收口；同日 S2-gate 复核判定 REJECT 后二次收口）**：平台分支端口词汇不止
  condition 的 `true`/`false`——PT3 节点是 `default`/`branch_error`、question 节点是
  `branch_0..N`/`default`。lowering 对「臂渲染不出**入口节点**」的宿主用**一条**
  义务表建模：`renderEnv.pendingPorts []portObligation{Selector, Port}` +
  `owe/oweAll/takePendingPorts/hasPendingPorts` + **一个** `drainPorts`。
  - **两个问题两个谓词，不得互相顶替（S2-gate B1/B2 教训）**：
    ① **渲染闸门 = `len(arm.Stmts) > 0`**（臂有没有语句）——拿「臂有没有入口节点」
    当闸门会把「臂首语句渲染不出节点、后续语句能渲染」的臂**整条跳过**：节点从产物
    里消失而 L1/L2 全绿 = 静默非法产物（比硬报错危险得多）；
    ② **入口 / 义务判据 = `armEntry(arm)`**（臂**跳过前置无节点语句后**的第一个实际
    渲染节点；为空 ⇒ 该臂欠该端口一条显式边）。端口边必须指向**臂的真实入口**——
    钉在 `Stmts[0]` 时该臂端口边根本不发，后续节点被**无端口线边**接出（P4 违规，
    L1 报 `source_port ""`）。
  - **销账点五个，形状一个**：下一个节点（常规路径 **与共享 End 早退路径**）、
    分支汇聚（`junction` → `emitPorts` 到 own merge）、容器尾（`closeBodyPorts`，
    按平台现实把**任意**端口词汇闭合回容器：语料 15 例
    `cond --true/false/default/branch_error--> <container>[*-inline-input]`
    ——2026-09-11 实测 true×7 / false×6 / default×1 / branch_error×1，旧记的
    「23 例」不可复现且分项自相矛盾，已按实测更正）、
    **控制节点（break/continue；节点入口即销账点）**、文档尾。**任何销账点的入场
    条件只允许是 `hasPendingPorts()`**——手写 OR 枚举端口词汇是复发机制（文档尾漏
    true、共享 End 路径两个都漏，皆为实证）。控制节点漏销账的后果最隐蔽：宿主端口
    义务闭到容器回环口（语义从「跳出循环」变成「走到本轮迭代末尾」）、控制节点成
    orphan、raising **静默丢掉 `break;`**（S2-gate B3 实测）。语料支持：35 条
    `condition --端口--> break|continue`，控制节点入度 1（49/53）或 3（4/53）、出度 0。
  - **fail loud**：文档尾是**最后一个**销账点，之后仍有义务 ⇒ `assertNoPendingPorts`
    编译失败并列出 `selector.port`，不得静默丢端口（门禁瞎眼比报错危险：两处产物
    非法而编译 SUCCEEDED 已被实证）。该 fail-loud 分支由
    `branch_port_obligation_tail_contract_test.go` 用手工 IR（无 end 的文档）钉住。
  - **宿主完整性**：`if` / `switch` / `QA` / `attempt` **四类宿主都必须有
    merge-less 路径**——IR 优化阶段会删除「零候选 + 无数据消费者」的 junction，
    而 `IfStmt.Merge` / `SwitchStmt.Merge` / `QaStmt.Merge` / `AttemptStmt.Exit`
    **不是消费者**；宿主缺 merge-less 路径 = 硬报错（`missing exit aggregate`）
    或静默非法产物（丢臂尾 + 不重置 `prev`）。IR 优化规则的每条「消费者/宿主」
    声明必须有对应负向测试（本轮教训：声明「两个渲染宿主都有 merge-less 路径」
    曾为假，两轮无人验证）。
  - **门禁与规则同构（S2-gate 加固）**：L1 端口完整性检查不止 condition——
    intent `{default} ∪ {branch_i}`（语料 8/8）、question `{default} ∪ {branch_i}`
    （**参数齐备才派生**：answer_type=option 且 option_type=static 且有
    dynamic_option；语料 0 例，不猜）、PT3 `{default, branch_error}`（语料 9/9），
    并新增**通用判据「分支宿主的出边必须带端口」**（空端口出边 = P4 违规）——否则
    B1/B2 那类缺陷永远 L1-绿逃逸。宿主×臂形态矩阵（臂首无节点 × 6 宿主）与
    「宿主紧跟 break/continue」及 `break` 往返契约见
    `internal/compiler/cozebiz_branch_arm_entry_contract_test.go`。
  - **平台证据等级**：condition 577/577、intent 8/8、PT3 9/9 有语料实证；
    **question 端口无语料实证**（coze-biz `workflow.yaml` 中 `type: question` 节点
    0 个）⇒ 其判据是「端口由我们自己声明 ⇒ 必须自洽闭合」（结构必然），置信度
    低于前三者。
  - **raising 镜像：空臂的读回判据是「作用域」，不是落点形状（A 级，
    2026-09-11 第三次 S2-gate 后收口）**：义务模型（lowering）说「臂渲染不出
    节点 ⇒ 端口边闭到**续接点**」，续接点包含「汇聚类」（merge / End / 容器
    回边）**与「续接语句类」（下一句 / `break` / `continue`）**两种。raising 侧
    `recognizeCondition` 曾按**目标形状枚举**实现前者、把后者**静默丢弃**——
    该臂在结构树里凭空消失；全部端口都落续接语句时 `trueGroups` 为空，
    `buildNestedCondition` 索引 `trueGroups[0]` **panic**（触发形状 = 嵌套空臂
    `if` + 尾句 / break / continue，深度 ≥2；与循环无关，顶层嵌套 + 尾句同样
    触发）。**普遍规则（无形态枚举）**：臂体 ⊆ `nodeSet` 是识别器的定义
    （`collectBetween` 只收 nodeSet 内节点），所以「后继 ∉ nodeSet」只有一种
    读法 = **该臂在此收束（空臂）**，续接由拥有该落点的祖先作用域发射。
    「作用域外 = 空臂」是 wire 蕴含的**结构事实**，不是对值/类型/谓词的推断
    ⇒ 不属「推断型静默 fallback」；真正的静默兜底（只保证不崩、返回空
    `ConditionStructure`）会伪造 `if (true)` 谓词，实测已被否。
    残余 fail loud：条件节点**一条分支端口边都没有**（破损 wire）时显式
    fail loud（`panic` 带 condID + nodeSet 规模），不得静默降级。
    **同族同洞未处理（另案）**：`recognizeSwitch` / `recognizeQA` 的后继循环
    仍是「无 catch-all 的静默丢弃」。语料实证：coze-biz **1 例**
    （`S113_guoxue_jieqian`，`default → 汇聚 hub`，填充后反编译**逐字节不变**）；
    coze-studio **2 例**（`X166_Vgushici_doubao_1_1` / `gushici_shipin_1280_720_video`：
    switch 分支边指向**容器节点本身**的 `batch-function-inline-input`（分支跳进
    容器体），臂内无 nodeSet 节点 ⇒ `generateSwitch` 发 `Dangling` ⇒ raised 出现
    **假** `FAILED(SWITCH-DANGLING)`）——coze-studio 侧按「搁置、只记录」处理。
  - **空 `default` 臂的读回判据 = 「落点是不是欠账端口的收口点」，不是落点
    形状、也不是被作用域守卫改写过的量（A 级，2026-09-11 第四次收口（含第四次
    S2-gate REJECT 的阻断项修正）/ 第三次 S2-gate 阻断项）**：平台对
    selector/intent 的 default 口只有**一种**物理形状，两种语义 wire 同形：
    ①**端口欠账**（源码没有 `default:` 臂，lowering 把恒声明的 default 口闭到
    **欠账收口点**）；②**源码显式终止**（`default: { return; }` 落 End/终端
    join，S188）。判别只能靠 lowering 的镜像——欠账端口由 lowering 闭到收口点，
    故 **`default` 落点 == `owedDrain`** ⇒ 该 wire 与「欠账」**不可区分** ⇒ 按
    直通读（省略该臂；在不可区分的两种源上这是**值保全**的一侧，也是
    `compile∘raise` 的不动点）；落别处 ⇒ 发射 `default: { return; }`（S188）。
    - **收口点定义**：`owedDrain` = `mergeNode`（有汇聚聚合器 → 聚合器）否则
      回退 `rawMerge`（ipdom 汇合点），**必须在两道作用域守卫之前取值**——
      它和 `cfMerge`（= 分支体 stop，用途不同）不是同一个量。判据**与落点
      形状无关**（聚合器 / ipdom 汇合点 / raising 合成的终端 join 一视同仁），
      也**与作用域无关**（收口点可以在祖先作用域）。
      **但判据有管辖范围（第五次 S2-gate B2 收窄，不可再写成「无形状守卫」）**：
      判据只在落点 ∈ 收口点记录表（End / join / merge / owned-merge）时**被咨询**；
      落**普通节点**（如续接语句/值节点）或**控制节点（`break`/`continue`）**时
      落点根本不进记录表 ⇒ 判据不参与，该两类是**已登记缺口 N5 / N4**
      （N5 = 硬失败级：续接为字面量返回时 r2 出码拒绝「空分支 = 悬挂」；N4 = 形状
      漂移、1 轮松弛稳定；最小源与产物以契约测试为实物）。
      扩展判据到该两类需自己的 S2-gate（`collectBetween` 的 stop 是另一半根因，
      不是一行改动），**禁止**以「再加一个形状守卫」的方式推广。**禁止**「落 End/join 即
      S188」这类形状枚举：switch 是流程最后一条语句时 default 被闭到**承载
      返回值的终端 join**，按形状读成 S188 后重编 `end.node_inputs` 由 `[y]`
      变 `[]`（**返回值静默丢失**）并稳定在该坏固定点，而 L1/L2 全绿（门禁全瞎）。
    - **递归封闭性（第四次 gate 的 REJECT 教训，不可违背）**：判据一度写成
      「落点 == `cfMerge`」——`cfMerge` 被 `nodeSet` 成员检查与
      `entrySetContains` 两道**作用域守卫**清空，而嵌套时欠账收口点正是**祖先
      作用域的逃逸点**（多前驱 End 归一化出的 `@pre-end`）⇒ `cfMerge=""` ⇒ 判据
      恒假 ⇒ 退化成旧行为（一律发射 `default: { return; }` ⇒ `end` 绑定静默丢失）。
      **少一层嵌套就复发**是语言递归自洽检查单的典型红灯：任何「把某量当另一
      用途的镜像」的写法都必须检查该量有没有被别处的守卫改写。本条的验收形状
      必须含嵌套（if 臂末 / else 臂 / switch 臂内 / 2-3 层 if）。
    - **不是充要判据**：这是「wire 可区分性」判据，不是「源码有没有 default 臂」
      的充要条件——源码显式写的 default 臂若落点恰等于收口点，同样会被读成直通
      （**无害**：两种源在该 wire 上本就不可区分，直通一侧保住了值）。措辞不得
      写成 ⟺。
    - 锚 `TestCozeBizSwitchDefaultArmOwedPortVsExplicitTermination`（两半必须
      **同时**锁：只断言「不长 default 臂」会被「一律省略」的错规则满足，只断言
      「保留 default 臂」会被「一律发射」的错规则满足）+
      `TestCozeBizSwitchDefaultArmNestedScopesKeepEndValue`（嵌套 5 例；
      断言**必须含 `endInputPaths`**——纯 `shapeOf` 只比节点类型 + 带端口边，
      抓不到这类丢值）。
  - **空臂的判据只有一条：端口落点 vs 收口点 —— 四臂共用、三层同修（A 级，
    2026-09-13 用户裁定「两臂都空也不能算错，语义上无错误，语法上无错误」后收口；
    正是上一族 P24「空臂不变量」残余 ① 的正解）**：
    `switch` 的 case/default 臂与 `ask` 的 action/timeout 臂在 lowering 侧本来就是
    **同一张** `pendingPorts` 义务表 ⇒ raise 侧判据**不得分裂**。规则只有一条：
    **端口有边且臂内无节点 ⇒ 落点 == 收口点 `owedDrain` 则该臂为空（直通）；
    落点在别处（End / 终端 join / 外层汇聚）则该臂在此终止（`return;`）。
    端口**无**边 = 真悬挂**（`Dangling → FAILED(SWITCH-DANGLING)`）——与「空臂」
    是两回事，判据是**端口边在不在**，不是**臂体在不在**。
    - **三层同修（缺一层就断链）**：① **parser**（`pkg/parser/stmt.go`）删除
      「switch 分支体不能为空」的**文本层代理检查**（全仓仅 case + default 两处）：
      parser 看不到端口连线 ⇒ 既拦不住真悬挂（wire 层才是判据），又误杀反编译
      产物里的合法空臂（HEAD 上 `raise(compile(r0)) == r0` 断链，r0 是自家 dec
      的产物）；② **识别器**（`platforms/cozestudio/ir/structure_recognizer.go`）
      删除**无条件**的 `if entrySetContains(cfMerge, branchEntries) { cfMerge = "" }`
      ——该守卫把截断点清空 ⇒ `collectBetween` 失去 stop ⇒ **每条臂一路吞到图尾**，
      共享续接点与它携带的 `return` 被**复制进每一条臂**（一个副作用变成多个；
      K/CV/SWD/MIX 第一手实证）；新判据 = 「端口 targets **全部** == `cfMerge`
      ⇒ 该臂为空」，并新增 `armJoinNode`（臂的汇合点 = 拓扑序最早的、被 ≥2 条臂
      可达集共同覆盖的节点；**每条端口取一个代表入口，不去重同址**——去重会让
      「两个端口落同一续接点」退化）；`entrySetContains` 收窄为**单链无汇聚**的
      唯一回退（`joinNode == ""` 时）；③ **生成器**
      （`platforms/cozebiz/decompile/generate.go`）：`!hasBody`（真悬挂）与
      `hasBody && len(body)==0`（空臂）分离，新增 `emptyArmBodyStmts` 供
      switch case/default + ask action/timeout **四臂共用**；
      `DefaultFallsThrough` 改由 `EmptyArmDrain[-1]` 驱动（缺表 = 直通）。
    - **可判定验收（全部是计数，不钉节点号）**：续接语句在 r0 中出现**恰好 1 次**
      （`strings.Count(back, "<url>") == 1`）；QA 臂内 `return;` 计数为 0
      （P5：QA 的 `return` = 分支结果值，一律补 `return;` 是语义改变）；
      `r0 ≍ r1` 文本级不动点 + 1 轮 wire 形状固定。锚：
      `internal/compiler/cozebiz_switch_ask_empty_arm_contract_test.go`、
      `platforms/cozebiz/decompile/switch_convergence_arm_contract_test.go`。
    - **旧说法的更正**：曾有「另一半根因 = `collectBetween` 在 `cfMerge == ""` 时无
      stop」的说法 = **症状**；真根因是上面 ② 的无条件守卫，且它在 HEAD 上对
      **普通形状**（不嵌套、无 default 臂）也活着。N5「落普通节点」判据缺口仍然
      登记在案（本轮未动，需自己的 S2-gate）。
    - **残余（登记，不得夹带）**：
      （含「循环体内的提前终止臂」= **非法产物 + 门禁瞎眼**（既有）：最小复现
      `for { switch { "a": { return "early"; } "b": { http(…); } } } return y;` ⇒
      **c0 SUCCEEDED、L1/L2 PASS，而反编译 rc=1** `node 100008 does not exist but
      appears as an edge target`（`end` 落进循环容器 + 顶层 `loop-output` 边悬空）；
      门禁瞎眼点 = `verify.go` `IndexGraph` 两侧互不校验且 L1 按节点表遍历。
      **HEAD 与修后 c0 逐字节同** ⇒ 既有，与本族正交，须自己的 S0-S6）。
      独立确认 gate 判定 ACCEPT-with-conditions，四条条件已全部落实（§6.0）；
      其提出的「周期 2 振荡」经第一手复测**未获复现**，故不按振荡登记。
  - **同族假前提更正（2026-09-11 独立 gate 实测；同日已收口，2026-09-12 更新）**：
    `emitSwitch` 的尾值判据与 `emitIf`（`len(agg.Groups)>0 || len(aggPlans)>0`，
    `build_return.go`）/ `emitQA`（`len(agg.Groups)>0`，`build_condition.go`）
    **并非逐字同构**——if/QA 家族曾把**零候选 junction 的 `{agg}.Group1` 当伪尾值**：
    `if (x=="a"){http}else{http}`（void）当时 c0 含空 `variable_merge`
    （`mergeGroups=[{name:Group1,variables:[]}]`）+ `end.node_inputs=[{path:Group1}]`，
    raise 凭空造出 `type RT1{string Group1}` + `v1 = {"Group1": ""}` + `return v1.Group1`，
    c1 该 merge 变 `# ncoda:shim` code 节点（**wire 真变**）= `r0 ≍ c0` 违反。
    **该缺陷已由 `4f14824` 收口**（四宿主统一 `junctionTailRef` = 「零候选 ⇒ 没有值」+
    共享投影器 merge-less 路径；用户 2026-09-11 批准的「第 1 条 if/QA 伪尾值」）。
    复测该形状：coze-biz `SUCCEEDED`、**无 `variable_merge`**、`end.node_inputs=[]`；
    dify-1.16 同源产物亦无 `variable_merge`/`Group1`（第一手 + 第九次 gate 24 形状 ×
    2 target 全负）。用户的「dify / coze-studio 连带变化 = 接受」裁定（本文件「开发主线
    与平台优先级」）正是解除「必须先证 dify 形状不变」阻塞的依据。规则残差（同一条普遍
    规则的其余未收口通道）= 全通道清单（**2 条已收口** = 尾值统一 + junction phi
    成员判据；另有若干未收口通道 + N1 + **RT-EVAL**）。
    **同规则在第四个目标（runtime）上的求值侧遗漏 = RT-EVAL（已证可达，2026-09-12 第一手）**：
    `internal/runtime/nodes/aggregate.go:55-58` 对零候选聚合返回 `{"Group1": nil}`（把「没有值」
    当值源），`ncoda-run -debug` 上可观察为值丢失（`{"output":null}`；对照 `string v;` 版
    `{"output":"A"}`）；出生侧仍在 IR（本例 = §16.2 行 2 / M2），runtime 侧按「只保
    coze-biz 主线、其余只登记」**搁置登记**，改动需自己的 S0-S6。
  - **junction phi 成员判据 = 两轴（**声明**先行 ∨ **绑定**先行），宿主无知（A 级，
    2026-09-12 主线 C7 收口；独立 S2-gate 判 REJECT as written → 三条阻断项全部落实后
    采纳核心规则）**：语言规则是「junction 之前**声明**的提升变量（`T v;` / `var v;`），
    在任一臂内被重新赋值 ⇒ 该变量是一个 phi 单元 = 一个汇聚候选集合；junction 之后
    的下游引用解析到汇聚单元」——其中「junction 之前」必须按**声明**判定
    （`outerDeclared` = 进臂前的 `hoistedVars` 快照），不是按**绑定**
    （`outerVars` = 进臂前的 `varToOutput` 快照）。`T v;`（带类型无初值）是 ncoda 的
    提升 idiom：只登记 `hoistedVars`/`hoistedTypes`，既不物化节点也不登记绑定
    （`build_call.go` emitVarDecl `init == nil` 分支）⇒ 只按绑定判成员会把「声明过」
    误判成「不是逃逸变量」⇒ 零候选 junction ⇒ 下游直引**首臂** producer、其余臂的写值
    **静默消失**（臂内 producer 成孤儿），而 L1/L2 全绿（**门禁瞎眼**）。旧实现只认绑定轴，
    且 if 宿主另有一条**宿主专属**尾值通道（`scalarHoistedCandidates`）侥幸正确 =
    同一规则四种形状（形态枚举反模式）；「臂**中段**写」（绑定不是臂尾值）更连 if 宿主
    一起丢值（第一手实证）。修 = 共享核心 `changedHoistedByShapeArms` 的成员门改为
    `hasOuter || outerDeclared[name]`（「改变量」比较只在 `hasOuter` 时生效；无绑定 ⇒
    零基，与 merge zero 语义同口径、**不为「没写臂」物化零值载体**）+ if/switch/QA/attempt
    四宿主一律传入进臂前的声明快照，**无形态守卫、无宿主特判**。验收：新契约测试 20 子例
    （候选集合本身 + 下游消费者确实读该组端口 + 嵌套 2/3 层 + record 逐字段 + list +
    串行双 junction + **臂内声明不得逃逸** + Optional + ×3 轮名字有界/不动点）；撤修复即
    **16 子例 RED**、另 **4 例双侧绿**（**总 20 子例**；4 例 = ① `if_else_typed_decl_no_init`
    **宿主一致性**——if 宿主靠宿主专属尾值通道 `scalarHoistedCandidates` 旧实现即正确，契约
    锁其不因统一规则退化；② `UntypedResidualPinned/{untyped_var_if_else, untyped_var_switch}`
    钉住无类型 `var v;` 现状；③ `DeadVarNotConsumedByEnd` **负向**——基线零 merge ⇒ 判据平凡
    成立。**独立 gate 实测 16/4，初稿所记 17/3 为假计数，已勘误**）；27 条 coze-biz 参考语料
    编译 A/B **逐字节不变**；全量 `go test ./... -p 1`
    失败集 = 基线（**零新增**），且既有基线红 `TestConditionalMergeSingleCandidatePhiNestedRoundtrip`
    由本修复**转绿**。锚：`internal/compiler/cozebiz_junction_phi_membership_contract_test.go`。
    **残余（已登记，不得夹带）**：① 无类型 `var v;` 的类型权威**已由下一条（M2 三级链）补上**
    ⇒ 该残余关闭，转为 M2 的新残余（ToolCall 无声明端口表 / ② 路径 C8 不对称 / definite-init
    不对称）；② **已收口（2026-09-12）**：`T v;` 在 QA/attempt 宿主**已可达** ——
    `passes/definite_init.go` 补 `case *ast.AttemptStmt`（两臂物理穷尽 ⇒ 无条件 join）+
    `case *ast.RequestInputStmt`（action 臂 + **恒存在的 default(timeout) 臂** ⇒ 只写 action 臂
    不算穷尽），绑定用 `checkArmBlock` 发布进臂局部作用域。实测：G-A 生成器 N=300
    `CHAIN_BREAK@r1 33 + @r2 2 → 0`；语料 L1→L4 与基线逐项相同（**零新增拒绝**）；
    全仓失败集 A/B 34→26（**新增 0**）；撤修复即 6 子例 RED。
    **新登记残余（另案，不得夹带）**：a) ~~终止臂过严（`if` 臂 `return` 后仍被要求赋值）~~
    → **已收口（2026-09-27）**。**规范版口径（可判真假）**：`T v;` 的确定赋值义务只落在
    **可达路径**上（标准 LANGUAGE-REFERENCE §3.1 原文「**所有可达路径**上都必须先赋值、再读」），
    而「可达」的判定在本 pass 里是**语法可判定**的：`return` / `break` / `continue` 是终结子，
    以及**全臂终结**的复合语句（`if` 有 else 且全臂终结 / `switch` 有 default 且全臂终结 /
    `attempt` 两臂全终结 / `ask` action+default 全终结 / `parallel` 全臂终结）⇒ 该臂**不落到
    汇聚点** ⇒ **不参与 `joinBranches` 的交集**（`if` 无 else / `switch` 无 default 仍算「有一条
    落入路径」，不因本刀放宽）。**明确不做**：路径可行性求解（一般不可判定）——本 pass 是
    CFG must-analysis，不是语义可达性 oracle（口径 = `docs/five-step/reviews/2026-09-14-formalization-review.md`
    §2「应定义 CFG must-analysis，而非承诺实际可达路径上的完美判断」）。
    落地 = `pkg/passes/definite_init.go`：`blockExit.fallsThrough`（`checkBlock`/`checkArmBlock`
    累加）+ `anyFalls` + `joinBranches` 只并「可达臂」+ `checkStmt` 返回 falls。判据 =
    `pkg/passes/definite_init_test.go`（`TestDefiniteInitTerminatingArmExcludedFromJoin{,Switch,ReversedOrder}`
    3 正例**改前红 / 改后绿**；`TestDefiniteInitFallingArmStillRequiresAssignment` +
    `TestDefiniteInitNoElseStillUnassigned` 2 反例**双向绿**（防过宽））；撤实现必红 =
    `python3 scripts/five-step/mutation_check.py --only 终止臂` **2/2**。定向一手（2026-09-27，
    `nclang-compile` 重建后）：`if` / `switch` 两形状改前 E1048 → 改后 SUCCEEDED；产物自洽
    （两带值出口 `merge(G1:[…]) → end`，符合 P5「带值出口经汇聚 merge 到 end」）；往返
    `decode ∘ encode` r1 == r2 不动点。**残余（登记，本刀不扩面）**：全臂终结后的**死代码**
    读点仍按「所有语句都检查」的保守口径报 E1048（精确化需另立「不可达代码」判据，当前不存在）；
    **陈旧格登记**：`docs/TRANSLATION-PATTERN-PUZZLE.md` 的 P18/P19 残余行仍写「终止臂过严」
    ——该文件在遗产隔离面内（未解禁）⇒ 不回写，待解禁时对齐。
    b) ~~`checkBlock` 每层新建 `declared`、外层声明不进嵌套块 ⇒ 嵌套内未初始化**引用**漏检~~
    → **已收口（2026-09-12，用户批准）**，规则见下条「definite-init 词法可见性」；
    c) **出口①已落地（2026-09-28）；出口②仍 open**：口径 2026-09-27 一手精确化，报账 2026-09-28 落地。
    原记「L4 `generateAttempt` 的条件零值守卫
    （`attemptPrecededByZeroCarrier`）在零值载体被共享/提升时丢掉作者显式写的零值写」——该符号今天在
    仓里**已不存在**（守卫经后续修复改写），但**同一根因仍在**。一手最小复现（`nclang-compile` →
    `coze-decompile`）：
    `string v = "z"; attempt http(...) as r { success { v = ""; } failure(e) { } } return v;`
    ⇒ 编译 **SUCCEEDED / 零诊断**（**2026-09-28 起在该形状上发一条 `TARGET_CONTRACT_DEGRADED`
    warning**，见下）；产物 = `text("z") → code#shim{return {"result": ""}} → <op>`
    （两个候选都在 attempt 之前 = 恒执行；**这一层上提不是根因**，理由见下）+ `variable_merge[v] = [<零值载体>, text("z")]`。
    **后果（真缺口，不是文本瑕疵）**：平台 merge = first_non_zero 且「零值与未执行同权」（见本文件
    「平台形状契约」节）⇒ 该组在**两条路径上都返回 `"z"`**，而语言语义要求 success 路径 = `""`
    （作者显式写的零值被平台吸收掉）。即 **ncoda 可表达、平台承载不了「某条路径显式写零值且要赢过
    非零基线」** ⇒ 撞「**失配 ≠ 静默丢弃**」不变量。
    **往返面读数（一手）**：回读 = `let v1 = "z"; string v2 = ""; attempt … { success { return v2; }
    failure(e1) { return v1; } }`；**c1 == c2 且 r1 == r2（第 1 轮即不动点 ⇒ 不是漂移，是语义缺口）**；
    c0 ≠ c1 的差 = id 分配序 + 组名 `v` → `Group1`（属命名/id 面既有族）。
    **人口 = 0（一手）**：语料 1340 份（`私有全量语料仓`）扫「同组既含静态零值候选、
    又含静态非零候选、且零值候选在分支内」⇒ **0 命中**（潜伏缺口，非现网问题）。
    **出口①：报账已于同日（2026-09-28）退役 ⇒ 下面这段是历史**（报账实现与词法下限口径，保留为索引）：它与 lowering 是**同一事实的两份定义**（按源码宿主形态**预测**平台承载），一手实测已与产物面结论**相反**（`switch` 唯一 case 未写 + 零值在 default、`attempt` success 未写：产物**已承载**而报账照报 = 假红，HEAD 起即有；另有一形状随本段收口转假红）⇒ 退役报账，不变量改由两条**读事实**的判据锁：构造期 `pkg/ir/zero_arm_default_gate_contract_test.go` + 产物面 `internal/compiler/cozebiz_zero_path_arm_gating_contract_test.go`。退役裁决 / 一手读数 / 替代判据 = `docs/five-step/rulings/2026-09-28-ruling-sheet-r26.md`（证据页 `docs/five-step/evidence/nested-junction-base-gating.md` §8）。<br>**以下为退役前的落地记录**：
报账（`platform` 类 warning，码 = 现有的 `TARGET_CONTRACT_DEGRADED`，
    与 file 三轴 / 枚举值集 / 显式 null **同一条纪律、同一个码**）—— 依既有政策「合法源码不拒收 ⇒
    软降级必须报账（warning，不是 error）」；用户变通 = 把零值写改成非零哨兵并在读点映回。实现 =
    `pkg/passes/zero_path_merge_report.go`（接线 = `TargetValidationPass.Run`）。能力表
    `zeroPathMergeCapacityOf`：**coze-biz / coze-studio = 无承载面**（`variable_merge` 的值语义 =
    first_non_zero，见本文件「平台形状契约」节 A 级定义）；**其余目标本判据不主张**（取法同
    `nonHTTPTimeoutAxisOf`：**只对已证载体错位的目标报账**，不发明能力 —— dify 的聚合器是
    first-**not-null**、零值不是 null ⇒「零值被当成缺席」这个失效模式在 dify 上不成立；dify 侧 junction
    形状另有其自身的历史问题，属搁置面）。
    判据（**词法下限**，不是类型检查、也不是常量折叠 —— 取不到权威就不报）：junction 宿主（if / switch /
    attempt / ask / parallel，与 `definite_init.go` 的 join 宿主**同集**）收敛的提升变量 v 同时满足：
    (a) 进宿主时 v 是**非零标量字面量**；(b) 某条臂内**显式写了零值字面量**（`""` / `0` / `false` / `-0` /
    `[]` / `{}`）；(c) 存在一条路径让 v **保持进宿主时的值**（某条臂没写 v，或无 else 的 if / 无 default
    的 switch；parallel 里所有臂都执行 ⇒ 只要有一条臂写了 v 基线就死了，故 (c) 在那里等价于「没有臂写 v」）。
    账的分母 = **(宿主, 变量)**，位点 = 该变量的第一个显式零值写（宿主 + 变量一条账，不是每处零值写一条）。
    **根因归属（一手修正，2026-09-28）**：`pkg/ir/hoist_zero_arm_carriers.go` 的上提**不是**根因 ——
    它只上提**恒零**载体，而恒零候选在 first_non_zero 下**永不胜出**（该 pass 头注的等价性证明成立）；
    根因在 junction 的**值语义**层：「这条路径显式写了零值」与「这条路径没执行」在平台上**同值**，任何非零
    候选都能赢它。判据的**负侧**由此而来：`string v; if (c) { v = ""; } else { v = "b"; }`（**没有非零
    基线**）产物是**对的**（first_non_zero 恰好给对）⇒ 一条账都不许报。
    **判据 = `internal/compiler/cozebiz_zero_path_merge_report_contract_test.go`**：
    正例（未承载，必须报）= switch **≥2 case**（含 int / bool / 带 default / 缺 default）、switch 的零值
    只写在 default 里、**attempt 的 success 臂未写** —— 全部由**产物面**独立复核（基线载体的入边来自
    `start`，不在宿主兜底口上）；负侧 5 格（每条臂都写 v / 无非零基线 / 零基线 / 没有显式零值写 / 中间
    语句覆盖了基线）+ 分母格（同宿主同变量两处零值写 = **1** 条账）+ R1（dify 一条不许有，且同一源在
    coze 家族上**确实报** —— 否则那格测不出承载面差异）+ R3（`status` 仍 `SUCCEEDED`）+ **已承载 9 格一律
    静默**。撤实现必红 = `python3 scripts/five-step/mutation_check.py --only 零值路径` **2/2**
    （① 撤能力表 ⇒ 报账永不发出；② 撤 (c) ⇒ 每臂都写的负侧被误报）。
    **一手验收（全量语料，2026-09-28）**：反编译 672/672 · 编译 672/672 `SUCCEEDED`，本判据报账 **0** 条
    ⇒ 人口仍为 **0**（潜伏缺口，非现网问题）**且零假阳性**（判据不在真实语料上制造噪音）。
    **已知边界（登记，不掩盖）**：臂内 `v = null;`（nil-less 平台同样把它吸收为零 ⇒ 同一失效模式）、
    「全字段零值的记录字面量」与「非空容器基线」——判这三者需要**类型信息**（record 的零值 = 全字段零值，
    与 map 的零值 `{}` 在词法上不可分，见 `pkg/ir/build_return.go::isZeroRecordLiteral` 对 shape 的
    依赖）；循环宿主（`for` / `while` 体的写入）不在宿主闭集内（那里的写入只用于清空顺序状态）；臂内写入按
    **子树闭包**判 ⇒ 若那条写落在臂内的嵌套循环里（可能一次都不执行）会判成「该臂写了 v」⇒ **漏报**（保守
    方向：假红会训练人绕过判据）。
    **出口②仍 open（正解）** = 改 lowering：把**基线值**复制进「未写臂」（则候选 = {success 分支内的 `""`,
    failure 分支内的 `"z"`}，每条路径各自只剩一个候选 ⇒ first_non_zero 恰好给对），但它与本文件
    「**不为「没写臂」物化载体**」（消灭死载体，X23 复发根因）条款**直接相抵** ⇒ 属独立立项 + S2-gate。
    **出口③（产物形状 / 可逆性）= 方向已定（A 类一手，2026-09-28；证据页 =
    `docs/five-step/evidence/zero-arm-base-canonicity.md`）**：同一条规则的另一半是**产物形状** ——
    今天该形状 `five-step-rt` 判 `ok:false`（`I1a TEXT_DRIFT/STRUCTURAL@2` + `I1b
    ARTIFACT_DRIFT/TOPOLOGY@2`，c0 6 节点 → c1 4 节点），且回读**凭空合成记录类型**
    `type RT_vString = { string v; }`。根因**不是**上提（§「根因归属」已修正），而是三处：
    ① `sink_base_carriers.go::isSinkableEmitterNode`（2026-09-28 分列：**relay 看穿路径**（`isSinkableRelayEmitterNode`）= 纯 shim，**不要求无输入** —— 输入生产者在本宿主之前 ⇒ 搬进臂内仍可读，一手 = 含变量字段的记录字面量基座由 8→6 漂移转为 ok；**直接候选路径**（`isSinkableDirectEmitterNode`）= 无输入的纯发射器 **或** 本组结局里可能出现作者写的类型零值（无零写时基座停在无条件位正是 J §8 U12 的规范位 —— 一手：既有 `f1f_alias_arm_merge_claim_contract_test.go` 所锁的 C 型形状放宽即红）） 只认 CodeRun 纯发射器，字符串字面量的载体是
    `ir.TextProcess` ⇒ 基座不下沉；② P28 在 `render.go` 里**先于**本 pass 运行 ⇒「哪条臂没写」的证据被
    上提抹掉（两条臂都判未写 ⇒ 规则④不成立）；③ raising 的 `convertVariableAggregator(cond=true)` 单组
    兜底按端口声明物化 `RT_*` 记录，缺 `record_unit.go` 的那条单组前置（候选生产者 = 臂写产出方）。
    **修法 = 把基线载体下沉进「未写臂」（出口② 的语言侧落地）—— 已落地（2026-09-28）**：
    ① `pkg/ir/sink_base_carriers.go::isSinkableEmitterNode`（2026-09-28 分列：**relay 看穿路径**（`isSinkableRelayEmitterNode`）= 纯 shim，**不要求无输入** —— 输入生产者在本宿主之前 ⇒ 搬进臂内仍可读，一手 = 含变量字段的记录字面量基座由 8→6 漂移转为 ok；**直接候选路径**（`isSinkableDirectEmitterNode`）= 无输入的纯发射器 **或** 本组结局里可能出现作者写的类型零值（无零写时基座停在无条件位正是 J §8 U12 的规范位 —— 一手：既有 `f1f_alias_arm_merge_claim_contract_test.go` 所锁的 C 型形状放宽即红）） 补 `*TextProcess` 支（字符串基座在
    coze-biz lowering 落成 `text` 节点、不是 CodeRun shim ⇒ 此前**整支字符串基座不下沉**）；
    ② `internal/backend/cozebiz/render.go` 的 pass 顺序改为 **Sink 先 / Hoist 后**（此前 Hoist 先跑把
    「哪条臂没写」的证据上提抹掉 ⇒ sink 规则④不成立）。两手实测（同证据页 §3）：基座下沉到未写臂后
    **c1 ≡ c0（6→6，边集逐条同构）** ⇒ I1b 转绿，且 `str_if_zero` / `bool_base` 首编=二编=三编不动点；
    ⇒ 该轴在 coze 家族上**在这些形状上可承载**（兜底路径上非零候选被臂门控 ⇒ 不在场 ⇒ 零值胜出 = 作者写
    的值）⇒ **出口①的承载判定随之改为宿主形态判据**（`zeroPathAxisCarriedBySink`）：未写的必须是
    **兜底位**（if 的 else 侧 / attempt 的出错臂 / switch 单 case 的落空位），具名臂未写（如 switch ≥2 case
    或 attempt 的 success 臂）**不承接** ⇒ 照报。【**该宿主形态判据已随出口①退役（2026-09-28）**：它按源码形态**猜** lowering 结果，而 lowering 认的是产物事实（今天：基线原件留入口 + 臂内恒值 relay）⇒ 两者对同一形状结论相反（一手见 r26 §1.2/§1.3）。不变量现由 `pkg/ir/zero_arm_default_gate_contract_test.go`（构造期）+ `internal/compiler/cozebiz_zero_path_arm_gating_contract_test.go`（产物面）锁。】判据与产物面**双向**互锁（报账 ⟺ 产物里基线载体的入边
    不来自宿主兜底口），见
    `TestCozeBizZeroPathReportFiresOnlyWhereProductIsUncarried`；**旧结论「attempt 空 failure 臂不承接」
    已被产物面推翻**（2026-09-28 一手：基线落在 `http:branch_error` 口，raising 读回 =
    `failure(e1) { let v3 = "z"; return v3; }`）—— 该形状移入「已承载」格，正是 ①（字符串基座下沉）
    把承接面从 CodeRun 基座推广到字符串基座的结果。**残余（登记）**：乙形下
    `I1a@2` 仍在（臂内写值两种拼写 `v1 = "z";` ⇄ `let v2 = "z"; v1 = v2;`）+ 一处 `return v1.v`
    （标量组上带成员余段）—— 属「臂内写值/读值拼写」层，与三处根因不同层，**独立销账**。
    人口（一手，2026-09-28）：语料 672 份 r0 词法扫描 ⇒ 真实人口 **0**（2 命中都在用户手写 foreign code
    块内）；生成器形状 `if_base_junction` 自注「零载体是 P28 的域，方向相反」⇒ 本支**生成器与语料双 0 覆盖**。
    ③ 「写而无人读」的变量长出**无消费 merge** = **已收口（2026-09-28 一手销账；收口提交 =
    `593e6650`，2026-09-13 第三族「汇聚活性 = 被数据引用」）**。原句判「属既有优化阶段缺口」的
    形状今日不复现：一手 5 例纯死写（`if` 无 / 有 else · 基线来自变量 · 嵌套 `if` · 无类型 `var v;`
    两臂写）**全部 merge=0**；原句点名的「尾值通道形态」（09-11 记 `if_mid` 的 `w`）今日读数 =
    **1 merge 且只给活的 `v`**、死的 `w` 已删、09-11 记录的「`v` 静默丢失」已修
    （`end.node_inputs.output ← merge.v`，非首臂 producer）。判据 = `internal/compiler/
    cozebiz_dead_junction_contract_test.go::TestCozeBizDeadJunction*`（**6/6 绿**）+ 机器门禁
    `pkg/ir/optimize_census_test.go`（类型级 + 字段级通道穷举，撤修复直接指到 `Switch.Query`）。
  （本条款 = junction phi 成员判据；相邻条款 = 分支端口义务。）
- **记录字段组的臂门控载体 = 一条整值记录载体（A 级，2026-09-28 用户裁定「python-code-shim 不可滥用」）**：
  记录值的门控粒度是**整值记录** —— `gateArmCandidate` 的 `rec != nil` 支对点分成员口（`<node>.base.field`）
  与**扁平成员口**（平台导出把记录口展开成逐字段口，`<node>.field`）都走 `recordRelayInArm`（物化一条
  2 输入同源的整值记录复制）；**物化失败 ⇒ 返回 src，绝不回退「逐字段标量 relay」**。理由 = 逐字段标量拼写
  （口名 `result`）下各组同位候选的生产者**不同序** ⇒ 反编译的**记录单元判据①**（口名末段 == 组名）不成立
  ⇒ 读成裹层记录 `RT_…`（一手：`rnz_foreign_zero` HEAD `ok:false` 13 → 0；`R-125` 最小例 HEAD `ok:false` 8 → 6）
  —— 同一语义的第二种产物且读不回。判据 =
  `platforms/cozebiz/decompile/record_unit_nonzero_base_arm_relay_contract_test.go::TestRecordUnitNonzeroBaseForeignZeroCarrierShape`；
  正本 = `docs/five-step/rulings/2026-09-28-ruling-sheet-r26.md`。
- **definite-init 的词法可见性：`declared` 向下继承 / `own` 只用于 join 退休（A 级，
  2026-09-12 用户批准后落地；独立 S2-gate 判 BLOCK→补三项阻断后收口）**：
  E1048 的判据是「**可见** ∧ 在**每条到达该点的路径**上已确定赋值」——两个轴都必须与
  binder 的词法作用域**同构**。`checkBlock`/`checkArmBlock` 此前只把 `initialized` 向下传、
  `declared` 每层从零新建 ⇒ 闸门 `declared[key] && !initialized[key]` 在**任何嵌套块内恒假**，
  规则在最常见位置（if/for/循环/臂内部的读）形同不存在，症状落到渲染层变成与语言无关的
  `ADAPTER_ERROR`（首轮即断链）。修 = **一条规则**：`declared := copy(inherited)`（可见集继承）
  + 出口只回传 `own = declared − inherited`（**必须是 copy**：`joinBranches` 用它做分支局部
  符号退休，回传完整 `declared` 会退休外层符号 ⇒ 灾难性假阳性）。**禁止**函数级共享
  `declared`、**禁止**「仅当嵌套深度 ≥ 2 / 仅当宿主是 X」的形式守卫。15 个容器调用点全部传
  inherited（含 for-expr / parallel-for-expr 体；lambda 体按同一条规则继承——binder 把 lambda
  scope parent 设为当前 scope，属内嵌词法表达式而非特例；coze-biz 上 lambda 不可达
  ⇒ 未验证边界）。**连带掐掉复发机制（R-F2）**：`checkStmt`/`checkExpr` 类型 switch 加
  fail-loud `default`，并配**机器化穷尽门**（`pkg/passes/definite_init_exhaustiveness_test.go`：
  `pkg/ast` 每个 `stmtNode()`/`exprNode()` 实现都必须被显式处理，新增节点不更新 ⇒ 测试红；
  该机制已造成 `AttemptStmt`/`RequestInputStmt` 与 `YieldStmt`/`OutputStmt` **两次**漏检）。
  验证：G-C 全量语料 L1→L4 **逐文件对照新增拒绝 0 / 诊断码变化 0**（用户批准的编译错误面
  变化在语料侧实测为零：反编译器 `hoistedVarDecl` 自带零值补偿，语料 857 处裸类型声明 0 处缺
  零值赋值）；G-A 生成器 N=48/N=300 修复前后逐类相同；G-A 语料侧与基线逐项相同（553/82/589）；
  全仓失败集 26 条**新增 0 / 转绿 0**；撤修复即 8 子例 RED。（本条款与「junction phi
  成员判据」为姊妹条款：前者管「join 该并哪些臂」，后者管「引用该看见哪些声明」。）
  **残余（另案）**：~~终止臂过严~~（**已于 2026-09-27 收口**，见上条 a）；P5 parallel 屏障透明未建模（分支内声明不透出
  ⇒ 漏检保留，非本族引入）；lambda 捕获在 coze-biz 不可达（未验证边界）；I3 命名有界。
- **junction phi 的类型权威三级链 + 两条臂级不变量（A 级，2026-09-12 用户批准「按推荐的做」
  后落地；两轮独立 S2-gate：gate#1 ACCEPT / gate#2 REJECT 修法 A、推荐修法 B）**：
  上一条（成员判据）回答「哪些变量是 phi 单元」，本条回答「**没有类型权威时怎么办**」与
  「**臂的视图是谁的视图**」。三处根因 = ① `var v;`（无注解）⇒ `hoistedTypes` 与
  前绑定端口皆 nil ⇒ `shape == nil` 守卫跳过 ⇒ **不建 phi 单元** ⇒ 零候选 junction ⇒
  其余臂写值静默消失、L1/L2 全绿（门禁瞎眼）。**缺陷在产物上分两族宿主形状**：**if 宿主**
  = `end` 消费该聚合 ⇒ IR 优化阶段（只删「零候选 **且无消费者**」）**不删**，产物 = **空壳**
  `variable_merge`（`mergeGroups:[{Group1, []}]`）+ `end → {junction, Group1}`
  ⇒ runtime 可观察 `{"output": null}`；**switch / QA / attempt 宿主** = 聚合无消费者 ⇒ 被删
  ⇒ **无 merge** + `end` 绑**首臂** producer。（第十二次 gate 修正：不可统一写成「优化器删除
  + 绑首臂」。）；
  ② 臂视图曾取**出口作用域快照**（含本 junction 其它臂的绑定）⇒ 未写臂被误判成写过 ⇒
  幻影候选指向**兄弟臂 producer**（同组重复候选，语料 232 组 0 例 = 非平台形状；
  raising RS-MRG-001 拒收自家产物）；③ 臂的抬出 delta 曾写进「本臂入口快照对象」，
  而那个对象正是**下一个臂的入口** ⇒ 臂内嵌套 junction 把互斥兄弟臂的 producer 当
  前绑定 ⇒ **跨互斥臂引用（P2 违规）**。
  - **修法（一条规则，四宿主 if/switch/QA/attempt 同构，无宿主/臂数/形态守卫；**2026-09-20 扩为五宿主** = + 值位置三元 `? :`，裁定与四段见 `docs/five-step/spec/J-join-canonical-form.md` §3.4 / §9）**：
    ① **类型权威三级链** `junctionPhiType` = ①声明注解 `hoistedTypes` → ②junction 前绑定
    端口 `portTypeOf` → ③**臂内真写值的声明端口** `declaredPortType`；**③ 只在 ①② 皆 nil 时
    咨询 = 严格增量**（今日存在的 phi 单元的通道/组类型结构性不受影响），且 ③ **只参与通道
    选择与组类型，不参与候选物化**。`declaredPortType` **只认声明端口**；**禁止**补任何
    形态枚举式来源（ToolCall 无关端口 / `TextProcess` 非 `output` / `singleOutputKey` /
    LLM `output|result` 别名 / 通用 dotted 首段回退 / `CodeRun` 容器捷径 / 零候选 legacy
    `Group1`）——**取不到权威就 continue（不虚构 phi）**。
    ② **臂视图 = 臂相对重绑定集合**（入口 `armEntry` / 出口 diff，**不是**出口作用域快照）；
    ③ **junctionScope 提交模型**：宿主在臂循环前捕获「包围作用域对象」+「入口快照」，
    每臂入口重置为入口快照，臂的抬出 delta 提交进**同一个** junctionScope 对象
    （跨臂 first-write-wins）；**禁止**每臂各持一个 map（实测 3 例硬回归 + 2 例 wire 改写：
    junction 后退化成「最后一臂的 map」⇒ 未被规划的名字只剩最后一臂写过才活着）。
    ④ **交付政策对齐 merge zero 语义（P04）**：无 junction 前绑定 ⇒ 该臂**不贡献候选**，
    删除零值载体物化路径（不为「没写臂」物化零值/转发载体）。
  - **验收**：473 源 STATUS 差异 0 / YAML 差异 5（全为目标形状）/ 27 条 coze-reference
    逐字节相同；322 语料反编译→重编译 HEAD 与 CUR **逐行一致**；全量 `go test ./...`
    **零新增失败**且既有红 `TestOptionalLLMPayloadRecordRepeatedRoundtrip` 转绿。
    锚：`internal/compiler/cozebiz_untyped_decl_type_authority_contract_test.go`。
  - **残余（已登记，不得夹带）**：
    ① **ToolCall 无声明端口表**（capability registry 的 declared outputs 未挂到 IR 节点）
    ⇒ 两臂都只有 ToolCall 时仍停留旧形态。**定级 = 结构性丢值（不是「只影响形状」）**：
    产物是零候选空壳 merge 且 plugin 输出**无任何消费点**，L1/L2 仍全绿（独立 gate 实测）。
    ② ② 路径（`portTypeOf`）仍保留 **C8** 零候选 legacy 类型静默兜底（§16.2 行 7 未收口）、
    ③ 路径显式禁掉 ⇒ **来源表不对称**。
    ③ definite-init 不对称（第一手实测，HEAD 与 CUR 逐项相同）：`T v;` + if/switch **全臂写**
    ⇒ 通过；`T v;` + if/switch **存在不写臂** ⇒ `UNINITIALIZED_USE`；`var v;`（无类型）⇒ 通过；
    `T v;` + **QA/attempt** ⇒ 恒 `UNINITIALIZED_USE`（`passes/definite_init.go` join 无 Qa/Attempt）。
    改动属编译错误面变化 ⇒ 须用户可见批准。
    ④ 容器/循环维度 out-of-scope（loop 家族 L1-L16 另行负责）。
    ⑤ raising 记录裹层（QA form 类型声明缺口，`r1 != r2` 收敛于 r2，**与 HEAD 逐项相同**）。
    ⑥ **【具回归性质，非 coze-biz】runtime / `flow.Project` 系目标在 attempt 宿主上由可用变
    硬失败**：`pkg/backend/flow/project.go` 的 `case *ir.AttemptStmt` **从不投影
    `s.Exit`**（`.Exit` 出现 0 次），而本族让 `Exit` 成为真聚合 ⇒ `end` 绑定悬空 ⇒
    `ncoda-run: compile: RDANGLING_REF`（最小复现 = gate12 `S14_attempt`：HEAD `{"output":"bad"}`
    → 本树硬失败）。coze-biz 不受影响（`render_stmt.go` 会渲染 `s.Exit`）。
    **归属 = lowering（`flow.Project`）+ 本改动使能，非 ncoda 语义问题**；按平台优先级
    ~~搁置登记、本轮不修~~ —— **已修（2026-09-28）**：`case *ir.AttemptStmt` 现投影
    `s.Exit`（三分支 = nil 透传臂尾 / 有臂尾 ⇒ 汇聚单元落地并续接 / 无臂尾 ⇒ 死路），
    臂端 End 只在「臂里真有节点」时物化；判据 = `internal/compiler/cozebiz_projection_wellformed_contract_test.go`
    规则②（refs closed）+ 本提交的运行期面。
    ⑦ 未写臂在 runtime 上返回 `null` 而非类型零值（既有 RT-EVAL 家族；本族使「写臂」正确后
    曝光面上升，一并记账）。
    ⑧ 「写而无人读」的无消费 merge：本族**曾扩大出现面**（`var v;` 两臂写 v 而 `return "x"`：
    当时本树多一个 2 候选无消费者 merge）—— **已收口（2026-09-28，收口提交 = `593e6650`
    「汇聚活性 = 被数据引用」）**：该形状今日一手 **merge=0**（4 节点，`start/condition/text/end`）；
    判据 `TestCozeBizDeadJunction*` **6/6 绿** + `pkg/ir/optimize_census_test.go` 通道穷举门禁。

- **义务表的第二处投影 = runtime spec（`pkg/backend/flow/project.go`，A 级，2026-09-28
  收口）**：上面那张义务表是 **lowering（coze-biz 渲染器）** 的投影；同一事实还有第二处投影 ——
  runtime 的 `flow.Project → spec.Build`（`rt-l3` 的 leg1/leg2 跑的就是它）。两处投影**必须同解**，
  否则同一 ncoda 程序在「产物」与「直跑」两条路上给出不同的图。
  - **此处曾只覆盖 `if` 宿主**：`intent` / `QA` / `attempt` 三宿主的 merge-less 路径仍把宿主当
    **裸尾** ⇒ 空臂的端口标签在续接时被吃掉。一手读数（2026-09-28）：`Switch -[]-> 续接节点`、
    `HTTPCall -[]-> 续接节点` ⇒ 运行到该臂 `RSPEC_INVALID: $branch "branch_1" / "fail-branch"
    has no declared target`（`if` 那一半的 10 源同族，一手见 672 源判决面）。
  - **规则（无宿主豁免）**：`deriveCtx` 一条 `pendingPorts []portObligation` + `owePort` /
    `drainPorts` / `drainPortsFrom`，四个落点 —— **下一个节点** / **分支汇聚** / **容器尾**
    （容器内的义务只许落在容器内，`drainPortsFrom(portMark, s.ID, inlineIn)`）/ **文档尾**
    （之后仍有剩余 ⇒ 投影 **fail loud**，不静默丢端口）。四类宿主 `if` / `switch(intent)` /
    `QA` / `attempt` **全覆盖**。
  - **同源推论：空臂的端口边绝不指向臂端 End**。① 语料事实：`processType=3` 节点的端口边
    **9/9 指向具体节点，没有一个指向 end**；② 投影若替空臂把臂端 End 物化出来，它**没有任何
    路径进入** ⇒ runtime 静态检查 `RUNREACHABLE_NODE` 拒收（一手：`100007 (end) has no incoming
    edges`）。⇒ 空臂的端口边落**汇聚单元**（`AttemptStmt.Exit` / `Merge`），merge-less 时记义务；
    渲染器同解（`if !succHasEntry { addPort(opID, merge.ID, …) }`）。
  - **判据（两面，缺一面有洞）**：投影面 =
    `internal/compiler/cozebiz_arm_junction_obligation_contract_test.go`（三宿主 × 三落点的端口义务
    对账 + 「宿主无无端口出边」；三刀撤实现各自红一次）；运行期面 =
    `internal/runtime/compile/branch_port_obligation_e2e_test.go`（五个形状、**两个方向都真跑** —— 只声明
    一个口在投影层看不出来，必须按 `$branch` 路由才暴露）。
  - **同族的 raising 镜像仍是残余**：`recognizeSwitch` / `recognizeQA` 的后继循环是「无 catch-all 的
    静默丢弃」（语料 coze-biz 1 例 / coze-studio 2 例），登记见上文「raising 镜像」条，本轮不动。

- **文档尾的残留臂尾必须接进流程出口入口（A 级，2026-09-28 收口，R-137）**：IR 的**最后一条语句**
  以「臂尾」收尾时（`if (c) { return; } else { throw_error.throw_error(…); }` —— else 臂的尾是
  **插件调用**而不是 `return`），臂尾留在投影的 `exits` 里、后面没有任何语句 ⇒ 不接就是**死端节点**
  ⇒ runtime `GraphRunError: no tasks to execute`（一手 672 源：17 源 `leg1:run-err`）。
  - **同一事实的三处形状对照**（同一语料 `7531294550921674779_X3_Tdouyinshuju_table_1`）：平台 wire
    节点 `122633 → 900001`（end）· 我们的 coze-biz 产物 `100021 → 900001` · runtime spec
    `100023 →（零出边）` ⇒ 缺的只是**第三处投影**（`flow.Project`）。
  - **修法 = 与协作渲染器文档尾同形**：`render.go` 的文档尾是「残留尾 `prev` 接进 `entry`」+
    「drain 端口义务」**两步**；`flow.Project` 此前**只有第二步**。补上第一步。
  - **图论守卫（不是形态守卫）**：补边前查 `reachesFrom(entry, [tid])` —— `entry` 已能到达 `tid`
    时补边即**闭环** ⇒ `RSPEC_INVALID: DAG is invalid, has loop`（`Compile` 面 = ADAPTER_ERROR）。
    一手形状 = 死代码夹具 `UNREACHABLE_CODE`（`return "ok"; output("never");` ⇒ IR 把不可达的
    `output` 挂在 End 之后）—— 与同文件 `exitWireTarget` 的逆向边判据**同一条**。
  - 判据 = `internal/compiler/cozebiz_doc_tail_residual_arm_contract_test.go`
    （`TestCozeBizProjectionNoDeadEnd`：除 `End` / `break` / `continue` 外**每个节点至少一条出边**，
    逐层查含 `ProjectedLoop` 子层；三个夹具 = 残留臂尾 / 两臂都 return / 臂尾有续接语句）。

- **「臂必须有汇聚点」的两种读法：控制汇聚恒真、值汇聚按需（A 级，2026-09-28 用户校准）**：
  「if 的两个臂 / intent 的多臂都必须汇聚」这句直觉**方向对**，但**不能逐字**当判据 ——
  它有两个层次，混起来一侧永远绿、另一侧误报：
  - **控制汇聚**（共同后继）：**恒真**。结构化语言单出口 ⇒ 每个臂的出口必有落点，落点四类 =
    **下游节点 / φ（merge）/ 容器回流口 / `end`**。两臂都 `return` 时汇聚点**就是 `end`**
    （不是「没有汇聚点」）——**`end` 是合法落点，判据漏掉它就是误报**（本条的校准来源）。
  - **值汇聚**（φ/merge）：**按需**，只在该分支的值有下游读者时存在。分支值没人读 ⇒
    IR 优化删 junction（merge-less）；**未写臂不产生候选** ⇒ 单候选也不造 φ。
  - ⇒ 逐字读法两侧都废：读作**控制汇聚** = 永远绿（无信息量）；读作**值汇聚** = 误报
    （「值没人读」是合法程序）。
  - **有齿的四句**（这才是判据）：
    ① 每个**声明的**分支口 ⇒ ≥1 条边，**且边带口**（P4）；
    ② 每条臂出口边有**确定**落点，落点 ∈ {下游节点, φ, 容器回流口, `end`}；
    ③ 值被下游读 **⟺** 承接单元存在（φ 或直引 relay）；
    ④ 反面：产物里**不得**有无入边的 φ —— 这是「两臂都 `return` 时不许造 merge」的机器形态，
    造了就是孤立节点，runtime 静态检查 `RUNREACHABLE_NODE` 拒收。
  - **三例（同一语言，汇聚点身份不同）**：
    ① `if (c) { return "a"; } else { return "b"; }` ⇒ 两条边都指向 `end`，**不造 merge**；
    ② `if (c) { x = 1; }` + 尾句 ⇒ `true` 口 → 赋值节点、`false` 口 → **直接指向尾句**（空臂
    那条边是该臂**唯一**控制边 —— 这就是「空臂欠一条端口边」为什么必须落地的原因）；
    ③ `if (c) { x = 1; } else { x = 2; }` 且 `x` 被读 ⇒ 两臂边都进 merge。
  - **空臂判据（A 级，R-141，2026-09-28）**：「这个臂是不是空的」= **该臂有没有产生控制
    贡献**（物化了节点 **或** 产生了边）；**不得**用「本块新增了几个节点对象」当代理 ——
    节点物化带 `endSeen` 去重：共享 `end`（900001）由**第一条**流出口的 `return` 物化，
    第二条流的 `return` 再物化时被去重 ⇒ 节点数不变 ⇒ 该臂被判成空臂 ⇒ 补一条端口义务边，
    而它的端口边**早由 `*ir.ReturnStmt` 的 `connect` 落过** ⇒ 同一
    `(selector, port, end)` 三元组出现**两次**。一手 = `if (c) { return; } else { return; }`
    的投影：`100002 --false--> 900001` ×2。**产物面看不出来**（渲染器侧另有逻辑去重 ⇒
    同一事实的两处投影**判据不同、结果相同**，正是它十天内没被发现的原因）；投影面是
    runtime spec 的输入，重复边 = 同一控制关系登记两次（P3：边集合是**集合**）。
    判据 = `internal/compiler/cozebiz_arm_junction_obligation_contract_test.go` 的
    `TestCozeBizBothArmsReturnConvergeOnEnd`（每个臂端口**恰好一条**落点边 + 两臂落
    **同一个** `*ir.End` + 无孤儿节点 + 此形状**不得**物化 φ）。
  - 上面「端口义务 / 空臂端口边落汇聚单元」几条就是 ①② 的机械实现；本条新增的**「`end` 属
    合法落点」**是 2026-09-28 用户校准的收口点。

### P5 parallel 语义契约（v4，不可违背）

- `parallel { ... }` 是**纯控制流栅栏语句**：所有分支执行完毕才继续；**从不
  产生集合值**（并行分支无序，无位置语义——要数组自己写 `[a.x, b.x]`，作者
  定序）。
- **读回方向的对偶：臂的呈现序 = 结构键，不是 wire 的 `edges:` 列顺序（2026-09-23
  R-25 一手；依据 = 本条上半段「臂无序、无位置语义」—— 既有的「无序」是**双向**义务，
  不是新的语言级改动）**：正变换侧臂序不可观察，读回侧
  就**不得**把 wire 里边的先后当成语义来源。同一份产物只把 `edges:` 反转 / 轮转（内容
  一条不动）⇒ 读回文本必须**逐字节相同**，否则同一语义两种文本 = 规范形缺口。
  实现 = 三处**入口集合**就地定序（`platforms/cozestudio/ir/structure_recognizer.go`：
  `fanoutEntries` 的 fan 列表 · `buildAttemptArm` 的多入口臂 · 条件 else 分组），定序键 =
  **同一份产物内**的 id（只作定序用，不是契约值、不参与判据比较 —— 与 `topologicalSort`
  的 tie-break / `successorSetKey` 同源）。**不得**改全局 `addEdge` 去重建边列顺序
  （blast radius 大且掩盖真顺序依赖）。
  判据 = `platforms/cozebiz/decompile/parallel_arm_order_wire_independence_contract_test.go`
  （**三个种子各对应一个泄漏源，撤任一实现 ⇒ 该种子红**）；全样本随机打乱复现 =
  `python3 scripts/five-step/evidence_wire_order_stability.py <产物目录> --shuffles 5 --only edges
  --decompiler <已建反编译二进制>`。证据正本 =
  `docs/five-step/evidence/2026-09-23-r03-wire-order-stability.md` §4。
- 分支是**不封闭的作用域（透明屏障）**：向内**绑定**（兄弟分支互相看不见），但不
  构成**逃逸边界**（臂块自身声明的名字在栅栏之后可读）。精确形式 = 该名字的可见域 =
  从声明点到臂末 ∪ 栅栏之后的全部后继。**不要**读成「parallel 没有作用域」——若臂与
  外层同属一个作用域，臂 A 的声明对臂 B 就也可见，与「兄弟互不可见」直接矛盾。
- **逃逸判据 = definite assignment**（既不是「容器种类」白名单，也不是「有没有顺序」）：
  一个声明能否逃出某个汇合点，取决于**到达该汇合点的每一条路径上该声明是否都执行过**。
  - **臂块本身**：所有臂都执行（AND 汇合）⇒ 臂块内**直接**写的声明 definite ⇒ **逃逸**，
    且**不需要零值兜底**（天然只有一个定义到达 join）。
  - **臂内嵌套块**（`if` / `while` / `attempt`）：各自存在「不执行」的路径 ⇒ 其中声明的
    名字**不 definite** ⇒ **不逃逸**（栅栏之后引用 = `UNDEFINED_VAR`）。⇒ 「臂块透明」
    **不等于**「臂内任意深度透明」：`if` 是作用域且互斥，与它在臂内还是臂外无关。
  - **对照 `if` 宿主**：`if` 的臂互斥 ⇒ 臂内声明同样不 definite ⇒ 同样不逃逸（作者要跨臂
    传值必须在外层声明 + 臂内赋值 = 物化一次 Φ）。两者命运不同的**唯一**原因是
    「全臂执行 vs 互斥」，不是任意规定。
  - **图论三判据（可机械核对）**：① **向上可见** = 臂内定义在图上到达 join 的后继
    （可达性）；② **侧向不可见** = 臂 A 的任一节点到臂 B 的任一节点**无路径**（B 的节点
    全在 fork…join 区间内）⇒ 引用 = `UNDEFINED_VAR`；③ **同名跨分支** = join 处该名字的
    **可达定义集合不是单元素**（两个无序前驱各贡献一个定义）⇒ join 的值函数不良定义 ⇒
    编译错误（R2/E1035）——这是 AND-join 良定义性的**必要条件**，不是额外的安全闸门。
  - **实现现状（2026-09-29 回写）**：raising 侧的抬出器 `hoistEscapedBranchVars` 与
    parallel 侧的 `hoistParallelEscapes`（均在 `platforms/cozebiz/decompile/generate.go`）的
    「收集」与「重写」两段**共用同一递归定义域**：收集 = **子树闭包**（`stmtChildBlocks` 是容器
    递归的唯一实现）**去掉循环体**（`loopScopeStmt` —— 自成一体的作用域），且与重写侧的改写点
    **逐字相同**。历史上的**手枚举**（`walk` 进 `For` 而 `rewrite` 不进；两段都漏 `While` /
    `AttemptStmt`）**已于 U2 / U20 收口**（`5b3a06d1` / `696a286e`）：漏收集 ⇒ 体外引用无声明
    ⇒ 首编 `UNDEFINED_VAR`；收集进而重写不进 ⇒ 同名两份声明（内层遮蔽外层）⇒ 体外读到的永远是
    零值 = **值静默丢失**。判据 = `platforms/cozebiz/decompile/hoist_escape_subtree_contract_test.go`
    （每种容器语句各一例「体内声明 + 体外引用」，要求 ① 体外发出声明 ∧ ② 体内原件被搬走；
    退回手枚举或去掉重写侧递归 ⇒ 必红）。⇒ 本条**不再是登记中的缺口**。
- **同名跨分支导出 = 编译错误**（R2）：每个分支有自己的 producer，裸名引用
  无法解析到唯一值——即使同类型也冲突，必须改分支局部变量名。
- 分支内 `return` = **流程结束**（terminate whole flow，直连 end），不是分支
  退出；栅栏不等待 return 分支。
  - **本条的「流程结束」不适用于子函数（2026-09-30 用户裁 甲；R15）**：子函数
    （`function` + inline call）内 `parallel` 臂里的**带值** `return <v>` 判**语言错**
    `E1037 PARALLEL_EXTERNAL_EXIT`。理由 = 两条「不可违背」在此相交且**都未加例外**
    （本条「终止整个流程」× 下方 return 语义分层 #2「子函数内 return = 值回流」），
    而栅栏的所有臂**都执行**（AND 汇合，非互斥）⇒「哪条臂的值是函数的值」**没有语言级
    答案**（候选序 = 产物序，是实现细节）。姊妹构造 `parallel for` 的同枚未定义性已先例
    同判（D-L3(a)，2026-09-14）—— 不为同一个问题造第二套规则。
    边界（三条都必须继续 SUCCEEDED）：**主流程（`main`）内维持本条不变**（main 无「函数
    返回值」概念）；子函数内 **void** `return;` 不动（不选值 ⇒ 结果确定）；臂内**三元
    表达式**的合成 return 与 **QA/attempt 臂**的分支结果值属宿主汇聚，不在此列。
    修前该形状 **静默 SUCCEEDED** 且产物丢掉作者写的调用点续段（撞「失配 ≠ 静默丢弃」）。
    判据 = `go test ./internal/compiler/ -run TestCozeBizParallelSubfunction`（四条半边：
    恰一条 E1037 · 任意嵌套深度 · `main` 维持绿 · 无 return / void return 均绿）；
    撤实现必红 = `python3 scripts/five-step/mutation_check.py --only parallel-R15`。
    裁决单 = `docs/five-step/rulings/2026-09-30-ruling-sheet-r15.md`。
- **return 语义分层（08-27 用户拍板，全局不可违背）**：
  1. **主流程（main）内任何 return = 流程终止** → 终止于唯一 end 900001——
     含 if/attempt/intent/parallel 分支内的 return（P5 多出口契约）。**不是**
     值回流/并入汇聚。**带值出口的值不直连 end，必须经汇聚 merge 到 end**
     （2026-09-12 用户拍板；end 只承载**一个**值绑定 ⇒ N 个带值出口物化
     `merge(G1:[cand…]) → end`，`end.node_inputs=[{path:Group1,ref_node:merge}]`）；
     **仅副作用、没有值的出口直连 end**（`cond --port--> end`，不物化 merge、
     不合成值消费点）。
  2. **子函数（function + inline call）内 return = 结束该函数 → 值回流**
     （inline 边界消费，不终止主流程）。
  3. **QA action / attempt success-failure 分支 return = 分支结果值**
     （subscope，返回给 QA/attempt 节点而非函数，不参与函数级锚定）。
  - 判定实现在 `pkg/ir/build_return.go`；投影约束见
    `docs/DIFY-VS-COZE-GRAPH-SEMANTICS.md` §6.2（merge 仅双分支有出口
    才投影；FlowEnd 分支不汇入 merge 直连 end；dead-path cut）。
- **多 return 出口是语言特性（C 风格）**：同一函数可以有任意多个 `return`
  出口（if/attempt/parallel 分支……），**不因"多出口"而拒绝**。约束只有两条：
  1. **返回值类型一致**——有 `-> T` 时每个出口值可赋值给 T；无 `->` 时第一个
     值出口锚定隐式返回类型，后续出口必须与之互相可赋值（int/float 不互化）。
     subscope return（for/while/for-expr body、ask action/timeout
     分支，值返回给循环/QA 节点而非函数）不参与函数级锚定。
  2. **投影为平台形态**：coze 单出口（所有出口汇入唯一 end 900001；**值侧**：
     多带值出口经汇聚 merge 汇聚由 end 读 `Group1`，单带值出口由 end 直绑该
     producer，无带值出口 end 不绑值）；dify 同规则（多出口经 variable-aggregator
     → end，单 end 多入边）；runtime 首个到达 END 的出口短路（first-to-end wins）。
     旧措辞「ensureEnd first-write-wins」描述的是**已修复的缺陷**（第一条出口
     静态占位、后续出口值静默丢失，违反 r0 ≍ c0）。
- **flow-end 后的不可达路径必须剪掉**（P3 投影 = 真实控制流）：parallel 所有
  分支都以 flow-end return 收尾时，后续语句不可达——flow.Project 与三个
  渲染器都必须跳过（dead-path cut），产物不得出现无入边的幽灵节点；runtime
  可达性门禁（RUnreachableNode）拒绝无入边节点。
- `merge` / `variable_merge` 服务两类**互斥收敛**（zero 语义 2026-09-09，
  nil/null 不参与值选择）：① **if 分支 phi**（条件收敛，见「条件 merge 翻译
  门禁」）；② **流程出口汇聚**（主流程多带值出口 → 唯一 end 的值绑定，
  2026-09-12 用户拍板）。**绝不用于
  parallel**——parallel 是 AND 栅栏汇合，无聚合语义。
- `yield` 在 parallel 内**废弃**（无收集目标），只保留于 `for` / `parallel for`
  （循环产出）。
- 分支命名标签（`r1:` / `u:`）保留但仅源码级，语义忽略；重复分支名 = 语法错误。
- **表达式形态 `let x = parallel {...}` 已删除**——不存在，也不得重新引入。

- **`parallel { A }{ B }` vs `A; B`：差异只有两条，且都由「分支之间没有顺序」推出
  （A 级，2026-09-19 用户拍板）**：用户原话——「parallel 其实类似把原先的串行执行提升到
  并行执行……除了这个并发，不改变原有的程序语义」；随后补一条——「还是有差异的，
  **兄弟分支不可见**，我漏了这个语义差别」。故：
  - **差异 ①（效果序）**：分支之间**无顺序**（只有 runtime 在乎交错）；`A; B` 里
    「A 的效果 happens-before B 的效果」这条保证在栅栏里**不存在**。
  - **差异 ②（可见性）**：**兄弟分支互不可见**（`A` 里声明的名字在 `B` 里不在作用域内，
    反之亦然）——顺序组合里 `B` 看得见 `A` 的声明，栅栏里看不见。**判据是名字解析**
    （引用兄弟分支的声明 = `UNDEFINED_VAR`），不是额外闸门。
    **镜像（raising 侧，同一判据的另一半）**：wire 上「链 A 的输出被链 B 引用」的扇出
    **不得**识别为栅栏 ⇒ **回退顺序**（`recognizeParallelFanout` 条件 4）；这不是保守回退，
    而是**唯一自洽读法**——跨链数据依赖本身要求 happens-before，顺序是满足它的唯一形式
    （否则读回文本再编译必落 `UNDEFINED_VAR` / `PARALLEL_ACCESS_HAZARD`）。
    边界（防过度推广）：**汇合点**读链输出是合法共享消费（X9 实证），不构成回退理由。
    锚 = `platforms/cozestudio/ir/parallel_cross_chain_dependency_contract_test.go`（三例：
    跨链 ⇒ 回退且不丢节点 / 独立 ⇒ 仍 3 臂 / 汇合点消费 ⇒ 仍 3 臂）。
  - **推论（三类，且只有这三类**，全部是「值取决于交错」这一条原理的实例）：
    ① **分支内写体外绑定 = 普通写**（合法；栅栏是 AND 汇合，栅栏后按名字可读——
    这正是语料规范形「体外声明 + 零值载体 + 分支内写 + 栅栏后读」，672 份 r0 里
    **92 份**含带赋值的 parallel 块）；② **同一绑定被两个分支触碰**（写/写、写/读）
    = 编译错误（`PARALLEL_ACCESS_HAZARD`；交错未定 ⇒ 值无定义，fail loud 而不是静默挑一个），
    **读/读合法**；③ **同名跨分支导出** = 编译错误（E1035 / R2，见上）。
  - **分支内声明对「外侧」透明、对「侧向」不可见**：判据 = **definite assignment**（见上
    「逃逸判据」）——臂块全臂执行 ⇒ 臂块内**直接**写的声明在续接点可读；臂内嵌套块
    （`if`/`while`/`attempt`）有不执行路径 ⇒ 其中声明在续接点**不可读**；横向（兄弟分支之间）
    无路径 ⇒ 不可读。「有没有顺序」只是**必要**条件（解释侧向），**不足以**推出向上透明
    （臂内 `if` 有顺序却仍不逃逸）。实测（2026-09-20，编译器面）：臂块直接声明 + 栅栏后读 ⇒
    `SUCCEEDED`；臂内 `if` 里声明 + 栅栏后读 ⇒ `Undefined variable`；兄弟引用 ⇒
    `Undefined variable`；两臂同名 ⇒ `exported from multiple parallel branches`（R2/E1035）；
    跨臂写/读 ⇒ `PARALLEL_ACCESS_HAZARD`。
  - **纯透传分支写会折进消费表达式（登记边界）**：`{ v1 = u; }` 这类不产生任何计算的
    臂写在读回文本里会与消费表达式合并（栅栏在文本上消失）；一手实测该折法是**值保全**
    且规范形稳定（折后再往返逐字节不动点），语料的臂体是副作用调用（http / 工具）**不可折**。
  - 实现锚：`internal/compiler/cozebiz_var_decl_equiv_contract_test.go`（产物面）+
    `pkg/passes/parallel_scope_contract_test.go`（pass 面）+
    `platforms/cozestudio/ir/parallel_cross_chain_dependency_contract_test.go`（raising 面，
    规则表 `docs/five-step/rules/PE-parallel-barrier.yaml` 规则面 4）。
- **`T v = <init>;` ≡ `T v; v = <init>;`（A 级，2026-09-19 用户拍板；上面 ② 的落地前提）**：
  用户原话——「我们这个简单语言，不像 c++，构造和赋值分开的，我们判定语言上等价」。
  推论：① 提升变量的 phi 成员门必须**同时**认这两条拼写（带初值的声明也要进
  `hoistedVars`），否则臂内写值静默丢失（产物绑零值载体、L1/L2 仍全绿）；②
  loop-state 的 `INVALID_STATE` 判据的**域** = 有 owning `while` 的写 —— 域外（顶层 /
  条件臂 / 栅栏臂）的写不得借该判据的文案拒绝（HEAD 实测：该形状被
  「points at a non-existent while」的文案拒掉，而同语义的另一条拼写编译通过）。
  实测：HEAD 上 `string v = ""; if (u=="a"){ v="A"; } return v;` ⇒ `INVALID_STATE`，
  `string v; v = ""; if …` ⇒ SUCCEEDED；两条拼写在修复后产物**逐字节相同**。

- **「拼接只剩一段」≡ 该段本身（A 级，2026-09-28 用户裁**甲**；取代 2026-09-26 的乙）**：
  模板串 / 拼接的四种拼写是**同一个程序** —— `raw` ≡ `` `${raw}` `` ≡ `"" + raw` ≡ `raw + ""`
  （`raw: string`）⇒ 必须落**同一条 wire**（不生成恒等 `text` 载体）。
  **前置（两半共用的同一把尺）= 值类型必须已经是 `string`**：`${n}`（`int`）是
  int→string 的**转换**，与裸值语义不同 ⇒ 不折；类型取不到 / `any` ⇒ 保守不折
  （fail-safe：宁可留一个节点，不发错产物）。**少了这条前置 = 把转换静默折没。**
  - **为什么**：同语义两种产物 ⇒ 反编译读回再编译得到**另一条**链（`C∘R∘C` 结构面不等价），
    且平台侧多一个恒等节点（纯噪声载体，`R-86` 影响面：语料 `text` 节点 741 / 单参 88 /
    **纯引用单参 2**）。同族另两半（多段模板 ≡ 拼接 · 空算元拼接）**早已闭**，孤点只有裸值这一侧。
  - **两半必须同时存在**（写不出逆 ⇒ 规范形还没找对）：③ lowering =
    `pkg/ir/build_expr.go::singleOperandConcatRef`（`emitTextConcat` ∪ `emitTextTemplate`
    共用同一个判据）+ 落点记账守卫（`emitBinaryValue` 的 `materializedKind(ref.kind)` ——
    折叠后没物化任何节点，就不许给上游记一条 native concat 落点）；
    ④ raising = `platforms/cozebiz/decompile/convert.go::soleTemplatePlaceholder`
    （模板恰一个占位 ∧ 恰一个输入绑定 ∧ 该输入**声明类型** = `string`）。
  - **同族的另一个位点 = loop 尾值口名**：`yield` 引用容器口时自建 TextProcess 的占位符名必须取
    **容器端口名**（`ref.output`），不是源码标识符拼写 —— 别名写法（`let v1 = item; yield v1;`）
    用拼写会发射 `{{v1}}` + `path:item` 的**混合形状**（占位符名 ≠ 端口名，绑定名与端口各说各话）
    ⇒ 读回再编译漂移。实现 = `pkg/ir/build_stmt.go::emitYield`。
  - 判据 = `internal/compiler/cozebiz_single_segment_concat_contract_test.go`
    ∪ `platforms/cozebiz/decompile/single_segment_concat_raising_contract_test.go`
    （各自带前置反锚：int / 字面前后缀 / 两占位 / 多绑定）；撤实现必红 =
    `scripts/five-step/mutation_check.py` 三格「拼接只剩一段①/②/③」。
  - **值位置的「行名 + 子路径」（2026-09-28 收口）**：文本节点模板的 token 可以是「名额行名 +
    行值上的导航」（`{{String1[2].book_image}}`；语料 13 token / 536 纯行名，且**绑定路径带下标 =
    0 / 14091** ⇒ 平台侧导航**只写在文本里**，绑定表是扁平的「行名 → 整口值」）。值位置的名字
    **没有消费者**（正本 §4.1 / R-78）⇒ **名字与文本同进同退**折成值导航
    （`` `${v148580.outputList[2].book_image}` ``）；**只改一半**（文本留原文、名额行照旧挂着）
    = 平台按名解析不到 ⇒ 整段原样打印、**值静默丢失**（撞「失配 ≠ 静默丢弃」）。
    **承载位不开这一档**（那里行名是作者写的、有 `using` 承载它 ⇒ 文本逐字保留）；形态不认识
    ⇒ 退回字面量（不发明拼写）。判据 = `platforms/cozebiz/decompile/text_subpath_nav_contract_test.go`
    （fold 三档 + 折了不得残留 `{{` + 不发明拼写 + 承载位负锚 + R-111 现状锚）；撤实现必红 =
    `scripts/five-step/mutation_check.py` 两格「模板子路径①/②」。

- **`parallel for` 迭代体 = 迭代局部作用域（A 级，2026-09-14 用户拍板 D-L1..D-L4；
  账 B P28）**：
  - **写 = 迭代局部、不逃逸**：迭代体内对循环外名字的赋值不计入循环出口的
    reaching definitions ⇒ **死写合法**、体内写后读读**本迭代** producer；
    IR 建图时 `parallel for` 的 `stateVars = nil`（**平台状态通道是 lowering 适配，
    不进 IR**）；顺序 `for` / `while` 的状态通道**保留**（有负向测试防「一律去状态」）。
  - **读**：循环**之外**读「被迭代写过」的名字 = **语言错 E1036**
    （`PARALLEL_ACCESS_HAZARD`）——拿到的只能是循环前的值，与作者写意图不符 ⇒ fail loud。
  - **控制转移**：`yield` / `continue` / **void `return;`** 合法；`break`（目标 = 该
    并发循环）与 **带值** `return <v>` = **语言错 E1037**（`PARALLEL_EXTERNAL_EXIT`）。
    判据是「**是否选值 / 是否停循环**」，不是「关键字是不是 return」。
  - **平台依据（先机器证据、后定规则）**：batch 容器**无**状态通道（语料 463 个
    batch：0 例 `variableParameters` / 0 例体内 set-variable / 0 例 break；
    `InitIntermediateVars` 只由 loop 调用），但**有**「容器内节点直连根 `end`」的通道
    （真实导出 **5 份**：`X19_Tdouyin3` 等的 batch 体内 `throw_error → 900001 end`）
    ⇒ **void `return;` 必须保留**——初稿把 `return` 整体判成语言错会误伤这 5 份
    （实测从「往返不动点」变「编译失败」），已收窄。

