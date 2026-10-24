# 项目契约（存档）— nodecoda-compiler

> **本文件 = 2026-10-04 版仓根 `AGENTS.md` 的全文存档**（正文**一字未改**，只加了本抬头）。
>
> 本文件是产品契约存档；治理/门禁细节由本仓的 CONTRIBUTING 与 Makefile 承载。
>
> **定位已换代（2026-10-25）**：自 P2c 起，**唯一正本 = 商业仓
> `docs/PROJECT-POSITIONING.md`**（可运行 DSL：语言 + runtime 是 P0，coze-biz 是
> 第一个双向适配器 P1，其余平台 P2）。本文件里凡以「平台是 backend target」
> 为前提的段落一律**只作追溯**，不得据此把平台代码加回开源仓。
## 逐节「活 / 死」标注（2026-10-09）

> 行号按**原文件**（未加抬头时）计。

| 原行 | 节 | 状态 | 说明 |
| --- | --- | --- | --- |
| 9 | 项目定位 | **死（已取代）** | 旧表述把平台当成本仓的 backend target。现行定位 = 可运行 DSL，平台适配器全在商业仓；正本 = `docs/PROJECT-POSITIONING.md`（商业仓）。 |
| 24 | 拍板分级 | **活**（已搬进仓根 `AGENTS.md`） | 四类用户拍板；但第 2 类的**机器面**已随隔离体系消失（见下）。 |
| 41 | 方法论重启：两轮遗产隔离（+ 全部解禁记录） | **死** | 隔离体系（`scripts/five-step/quarantine_check.py` · `docs/five-step/quarantine.yaml` · 五条解禁记录）已随支架一并卸下。 |
| 150 | 工作索引：按构造（48 行固定） | **活** | 产品侧的工作组织方式（48 构造 × 算法四元组：语义 / 规范形 / lowering / raising）。 |
| 166 | 五步法门禁退役 | **半活** | 「什么**不再是**门禁」这一声明今天仍成立；但它点名的入口（`scripts/five-step/verify.sh` 等）已不存在。 |
| 192 | 开发主线与平台优先级 | **死（已取代）** | 平台优先级属商业仓路线；开源仓主线 = L1 规范 + L2 runtime + L3 语料验收，不按平台排期。 |
| 239 | 注册表数据权威 | **活** | DB 唯一真相 + 能力头文件生成链路。 |
| 289 | 编译器后端架构原则 | **指针** | 正文 = `docs/BACKEND-ARCHITECTURE.md`（**存在**）。本节的子标题只是索引。 |
| 359 | null / undefined 语义契约 | **活** | |
| 382 | shim-python-subset 定位标准 | **活** | 正文也在 `docs/PYTHON-SHIM-TRANSLATION-CONTRACT.md`。 |
| 408 | 注解侧车 | **活** | 正文也在 `docs/five-step/spec/ANNOTATION-SIDECAR-CONTRACT.md`。 |
| 420 | ncoda 语法扩展审核契约 | **活** | |
| 446 | 统一 LLM 语义契约 | **活** | 正文也在 `docs/UNIFIED-LLM-SEMANTICS.md`。 |
| 480 | ncoda 覆盖承诺 | **活** | |
| 496 | 平台形状契约（coze-biz 后端） | **死（已随适配器移出）** | 正文随适配器进商业仓；开源仓只保留语言级形状判据。 | |
| 613 | 验证契约 | **半死** | 五步法棘轮 / 指纹 / 基线重钉义务已于 2026-09-19 退役；「判据与指针」那一半仍有效，但其中部分入口已不存在。 |
| 727 | 机器权威路由 | **死** | 表内五步法判据已退役；**替代入口 = 仓根 `AGENTS.md` 的索引表 + `gov help`**。 |
| 788 | 知识地图与决策分级 | **活** | 两级：规则内容落机器可核对处 / 决策理由落散文且只写一处。 |
| 809 | 语料改动授权边界 | **活** | 语料仓只读；任何改动须先提交清单并取得逐次批准。 |
| 822 | 开发环境约定 | **活** | ⚠️ 含**明文 API key**（用户已明确接受泄露后果）—— 注意本仓有 GitHub remote，**push 即公开且不可回收**。 |

---

# AGENTS.md — nodecoda-compiler 项目契约

> **等级用语约定（2026-09-14；2026-10-03 加机器面）**：`不可违背` **只用于不变量** —— 语言语义 /
> 平台物理契约 / 安全 · 授权边界 / 产物必填形状。**流程与纪律不写「不可违背」**，写「纪律」并给出
> 退出码入口。**这条约定不由人维护**：判据 = `python3 scripts/spec_matrix_check.py` 的 **MM-8**
> （`tier: invariant` ⇔ 锚定节含 marker；纪律节带 marker ⇒ 红「等级用语通胀」），分档与口径正本 =
> `docs/governance/spec-matrix-contract.md` §4。⇒「有几处在通胀」是**读数**，不是维护义务。

## 项目定位（2026-08-29 用户拍板）

**ncoda 语言是圆心，参考目标 / coze-studio / coze-biz / runtime 是它的四个目标
（backend target）。** 编译方向：`.ncoda` → TypedBoundProgram → 结构化 IR →
（IR 优化：折叠 / 删除无用节点）→ lowering → 各目标平台产物（YAML/JSON/ZIP/图）。
反编译方向：各目标平台产物 → ncoda。
ncoda 语言语义是第一性的；平台形状差异是渲染器的适配问题，不是让语言向平台
妥协的理由（ncoda 覆盖承诺、P1-P5 见下方）。

本文件是**索引 + 操作面**：固化代码修改必须遵守的顶层契约。2026-09-20 起，
**编译器后端架构原则**（917 行）与方法论授权边界（151 行）的**正文**搬到
`docs/BACKEND-ARCHITECTURE.md` 与 `docs/governance/methodology-restart-authorization.md`，
本文件保留同名标题 + 指针（条款级别不变）——一个规则不两处定义。
快速上手：系统架构总览、分层包地图与验证门见 `docs/ARCHITECTURE.md`。

## 拍板分级：agent 自决 vs 用户拍板（纪律，2026-10-03 用户拍板）

- **默认 = agent 自决**：凡能由**技术事实 / 项目契约 / 一手证据**推出的决策，自己拍板并继续 ——
  包括按既有约定收口措辞、归属、账目对齐、判据与文档的机械同步。**这类决策不得推给用户**
  （「我不确定 ⇒ 问一下」不是理由：先读正本与判据 —— 决策带宽是最稀缺的资源）。
- **只有四类必须用户拍板**（不在此列 ⇒ 默认 agent 自决）：
  1. **语言级扩展 / 变更**（词法 · 语法 · 语义 · 类型系统）—— 依「`ncoda 语法扩展审核契约`」：
     先交设计说明（变更点 / 动机 / 影响面 / roundtrip 影响 / 兼容性），用户确认后才写代码。
  2. **授权边界**：遗产隔离面的**解禁 / 收编 / 搬迁** —— 机器面 =
     `python3 scripts/five-step/quarantine_check.py --staged`（已接进 `make gate-precommit`；
     它只拦「不许引用 / 不许改动」，「要不要解禁」只能人裁）。
  3. **语料改动** —— 依「`语料改动授权边界`」节（逐次批准）。
  4. **破坏性 / 不可逆**（删数据、改历史、对外发布）与**产品价值取舍 / 范围裁定**
     （做不做某件事、优先级反转、覆盖范围的扩缩）。
- **无机器面（已知缺口）**：「谁拍板」不可机械判定 ⇒ 本条与规范矩阵的「人工纪律」同列登记
  （残余 `R-189`）。它的自证方式 = 上面四类**每一类都指向一个既有入口**，其余一律自决。

## 方法论重启：两轮遗产隔离（不可违背·授权边界，2026-09-14 用户拍板）

> **正文**：`docs/governance/methodology-restart-authorization.md` 同名子节（2026-09-20 搬出；条款与级别不变，本节只作索引 —— 同一规则不两处定义）
> **索引**（机械摘录：本节正文里出现、且被跨文件指针引用的名字，供指针命中）：`方法论重启` · `语言第一性` · `失配 ≠ 静默丢弃` · `方法论重启：两轮遗产隔离` · `读代码的口径` · `最少例外` · `读代码的口径：代码 = 现状，不是标准` · `权威阶梯`
- **解禁记录（2026-09-22 用户点名）**：09-20 §3 裁定「保留」的那批流程判据，其载体在隔离面内
  ⇒ 裁定不可执行（09-22 复核实测：35 处违规**全是「谈论/登记它」**，0 处变更它）。用户裁定
  解禁**执行身份 + 登记身份**（9 项：`commit_gate` / `lib_gates` / `family_gate` / `install_gates` /
  `mutation_check` / `catalog_check` / `test_catalog_check` / `frozen_red` / `docs/governance/`），
  **A 层（结论权威）不变** —— 逐条 scope 见 `docs/five-step/quarantine.yaml#unlock`（机器权威），
  事由与继续隔离清单见 `docs/governance/methodology-restart-authorization.md`「当前解禁」节。
  **解禁是否真生效**有验活入口 = `python3 scripts/five-step/quarantine_check.py --audit-unlocks`
  （2026-09-22 实测 **36/36 生效**）：初版 9 条写成 `expires: None` —— YAML 里裸写 `None` 是
  **字符串**不是 null ⇒ `date.fromisoformat` 抛错被吞 ⇒ 9 条**全被静默跳过**（解禁等于没解禁，
  现场只报「R1 未解禁」）。已修：无期限写 `expires: null`；写坏 ⇒ **fail loud**；判据自测锁住。
- **追加解禁一条（2026-09-24）**：`internal/compiler/cozebiz_zero_identity_candidate_contract_test.go`
  单文件、**仅** `TestCozeBizZeroIdentityCandidateNegativeNonZeroBaseKept` 的夹具（单字段叶记录 →
  非叶字段记录；判据语义不变：未写臂必须保留恒等转发 relay）。事由 = R-43「记录整值折叠」使原夹具
  变成**空锚**（该形状不再产生 relay）。逐条 scope 见 `docs/five-step/quarantine.yaml#unlock`。
  **授权留痕**：改动内容与 unlock 条目文本在提交前已逐条披露；用户 2026-09-24 回
  「我要休息一下，继续，直到达到我们最初的目标」。
- **追加解禁一条（2026-09-25，用户点名）**：`docs/pattern-catalog/catalog.yaml` **单文件写权限** ——
  事由 = R-CAT 的**机械后果**（上一轮 AST 值形态改名 `ast.TemplateReturn` → `ast.TemplateValue`，
  账本成陈旧格 ⇒ 门禁永久红）。scope = 仅两处字面量对齐（形状格 + 2 个测试名锚点），判据本体
  一字未改；逐条 scope 见 `docs/five-step/quarantine.yaml#unlock`。
  **授权留痕**：改动内容在提交前已逐条披露；用户 2026-09-25 点名「解禁 `docs/pattern-catalog/`
  （单文件写权限）」。
- **追加解禁一条（2026-09-25）**：快照层**整体退役**（`placement_snapshot_test.go`，原
  `internal/compiler/`，`//go:build snapshot`）—— 用户裁决（乙）。处置 = 内容一字不改移入
  `legacy/`（`legacy/README.md` 记退役理由 + 复活条款），`quarantine.yaml` 的清单前缀同步迁移。
  理由 = 该层**不在任何门禁**（`make snapshot` 手动；`gate.sh` / `gate-precommit` /
  `gate-self-tests` 都不含），而「冻结 sha256」正是 2026-09-19 已退役的**指纹型判据**形态
  （改实现 ⇒ 证据批量过期，修法只是重钉哈希）。逐条 scope 见 `docs/five-step/quarantine.yaml#unlock`。
- **追加解禁 13 条（2026-09-25，用户点名）**：`internal/compiler/` 下 13 个契约/门禁测试文件
  （清单 + scope 见 `docs/five-step/quarantine.yaml#unlock`，`expires: 2026-10-09`）。事由 = G-D 的
  `wire_lit` 专项清：测试面 951 处 wire 词裸拼写点收成**每包一张** `zz_wire_keys_test.go` 常量表
  ⇒ 平台改键名从「几十个文件几十处」变成「每包 1 处」；这 13 个文件在隔离面内，故按授权边界点名。
  scope = **仅**「双引号裸字面量 → 本包常量名」的机械替换（13 文件合计 **+50/-50 行**，逐文件
  核对为纯替换；断言 / 夹具 / 判据语义一字未改；口径同步收口为「裸拼写点」——反引号夹具原文
  = 平台导出原文，不计）。**授权留痕**：改动内容与 unlock 条目文本在提交前已逐条披露；
  用户 2026-09-25 回「确认解禁这13个文件」。
- **解禁记录（2026-09-27 用户点名）**：`docs/pattern-catalog/catalog.yaml` 的 **`findings[]` 已有条目
  销账**写权限（单文件 / 仅 `findings[].{status,title,note}` / 不增删条目、不动别的字段）—— 事由 =
  2026-09-26 那条只放行「**追加**新条目」，而差异账里已有陈旧 / 已答条目需要销账。逐条 scope 与
  处置见 `docs/five-step/quarantine.yaml#unlock`（机器权威）；09-26 的三条同此，不在此复制。

- **解禁记录（2026-09-30，用户裁「甲」后的机械后果）**：`internal/compiler/cozebiz_branch_port_obligation_contract_test.go`
  **单文件**、**仅** `TestCozeBizNestedConditionArmEscapeRaisesFixedPoint` 的四个用例各加一条**臂内活语句**
  （`http("GET", …)`）⇒ 外层 then 臂两条语句 ⇒ 不匹配 R-172 合取融合的前置。事由 = 该融合（用户
  2026-09-30 裁 **甲**，正本 `J §53.7`）使「then 臂**恰好一条** else-less 守卫」不再抵达 lowering，而该用例
  正是拿这个形状当前题 ⇒ `if (` 计数断言**假红**（臂的逃逸口一条没丢，一手复算见 unlock 条目）。
  **判据本体一字未改**（+12 行，全是新增语句与说明注释）。逐条 scope 与处置见
  `docs/five-step/quarantine.yaml#unlock`（机器权威）；本条不在此复制。**授权留痕**：改动内容与 unlock
  条目文本（完整 YAML）在提交前已逐条披露；用户 2026-09-24 的持续性指令「我要休息一下，继续，直到达到
  我们最初的目标」仍在生效（同形先例 = 2026-09-24 那条解禁），且 2026-09-30 裁「甲」时的影响面已写明
  覆盖「else-less if 的 then 臂里恰好只有一个 else-less if」的全部嵌套守卫 ⇒ 本条 = 该裁定的**机械载体**。

- **解禁记录（2026-09-30，用户点名「解禁」）**：`scripts/gen_missing_headers.py`
  **单文件**，scope = ① **执行身份**（运行它补 RS-REG-002 `header_missing` 类头文件）
  ② **登记身份**（允许在 `docs/coze-biz-schema-audit.md` §2c 与本条解禁记录里点名它）；
  **不含**改写脚本语义、接进常驻门禁。事由 = 该类（DB 有 op、本仓缺头文件）的**唯一补 op 实现**：
  不引用它只剩「fail loud 不修」或「手工改头文件」（后者被「不手工修改能力头文件」禁止）。
  口径核对（2026-09-30 一手）：脚本取参数类型走 DB、取参数名与**输出 schema** 走语料 ——
  输出侧族唯一权威本就是语料（DB 无输出 schema），与「DB 唯一真相」不冲突。逐条 scope 与
  处置见 `docs/five-step/quarantine.yaml#unlock`（机器权威）；本条不在此复制。**授权留痕**：
  改动内容与 unlock 条目文本（完整 YAML）在提交前已逐条披露；用户 2026-09-30 回「解禁」。

- **解禁记录（2026-10-04，用户点名）**：`docs/governance/judge-contract.md` **单文件**，scope = ① §2 键表
  +1 行 `@enforces`（含骨架 + **A13 依附律**条款）② §0 / §7 / §8 与头部指针的 A1–A11 → A1–A13 同步
  ③ 既有条款（A1–A12 判据语义 / §1 退出码 / §5 锁两半）**一字不改**；**明文排除**把 `@enforces` 改成必填
  （v1 宽限 = 只判「写了的必须合法」+ 报数）。事由 = 《活性纳入律》的「反查活着的判据依附的规范」缺一条
  **机器可读的边**（一手底数：gov 在册 21 条判据里**有机器可查依附边的 = 0 条**）。同轮：执行面 =
  `scripts/judge_admission_check.sh` 的 A13 判定段 + `scripts/test_judge_admission.sh`（**两者不在隔离面内**）；
  `docs/governance/ledger-contract.md` 同前缀，仅加一条 LK-7 ↔ `@enforces` 的**消歧**（防两个键共用一个名）。
  逐条 scope 见 `docs/five-step/quarantine.yaml#unlock`（机器权威）；本条不在此复制。**授权留痕**：改动内容与
  unlock 条目文本（完整 YAML）在提交前已逐条披露；用户 2026-10-04 回「解禁 judge-contract.md（加 @enforces + A13）」。

- **解禁记录（2026-10-04，第二批收口；用户同日两条拍板）**：①「基础判据，可以成为元判据的：
  规范判据 / 账本判据 / 判据判据」②「元规范、账本、判据规范和三类判据，应该作为 gov 的默认产物，
  因为这是业务无关的」。机械后果三件：**a. 元判据闭集 = 3 类**落判据契约页 §0（三行表 + 三条
  不变量 + 「业务无关层 ⇒ gov 默认产物」的裁定 + **现状未落地**的标注与残余 `R-192`）；**b. A13 的
  `none` 语义由一档扩为三档**（实现层不变量 / 规则只住判据头注 / 正本在隔离面内）——两处都在
  `docs/governance/judge-contract.md`（单文件；**明文排除**新增 A 编号、把 `@enforces` 变必填）；
  **c. gov 在册 21 条判据逐条补 `@enforces`**（实测 21/21 = 15 条正本锚串 + 6 条 `none` + 理由），
  其中两条载体在**门禁脚本隔离面**内（`scripts/catalog_check.py` · `scripts/commit_gate.sh`，
  各只插 2 行声明、判定段一字不改）⇒ 三条一并点名。逐条 scope 见
  `docs/five-step/quarantine.yaml#unlock`（机器权威）；本条不在此复制。**授权留痕**：改动内容与
  unlock 条目文本（完整 YAML）在提交前已逐条披露；用户 2026-10-04 两条拍板即该点名。

- **解禁记录（2026-10-04，A14 落地；用户「继续」）**：闭集的**集合级判据** = **A14**
  （`scripts/judge_admission_check.sh`）—— 读 §0 闭集表（哨兵 `META-JUDGES:START/END`）四条硬判：
  恰好 3 行 / 元判据存在 / 该判据的 `@enforces` 路径逐字等于该行正本（自指归属）/ 正本存在。
  为此动 `docs/governance/judge-contract.md` **单文件**：表加哨兵 + 第 3 行正本列改显式路径 +
  §2 新增 A14 条款 + §7/§8 同步；**明文排除**改 A1–A13 语义与 §0 三条不变量正文。
  锁 = `scripts/test_judge_admission.sh`（A14-1 表 2 行 ⇒ 红 / A14-2 自指归属不符 ⇒ 红 /
  A14-3 撤段 ⇒ 负例不再红）。逐条 scope 见 `docs/five-step/quarantine.yaml#unlock`（机器权威）；
  本条不在此复制。**授权留痕**：改动内容与 unlock 条目文本在提交前已逐条披露；用户 2026-10-04 回「继续」。

- **解禁记录（2026-10-04，机械后果型 · 两条）**：① `docs/governance/band-plan.yaml`（**单文件**：档位计划
  正本的写入与维护）—— 事由 = 框架契约条款 `default-artifacts-scaffolded`（现 `gov-frame@12`）把「档位计划」
  列为默认产物 ⇒ `gov init` / `gov scaffold` 按同一份层清单**机械地**把它铺到该路径（层清单唯一处 =
  `gov-impl/scaffold.go`），而 2026-09-22 那条前缀解禁的 scope 只列了「设施清单 / 处置正本 / 冻结红清单」
  ⇒ 按「逐条 scope」补。② `docs/governance/judge-contract.md` **§0「现状（不掩盖）」段两处** —— 事由 =
  刀 AF / 刀 AH 落地后原文变成假话（「gov 今天**不产出**这一层」「残余 `R-192` 剩下的只有 gov 侧 scaffold
  这一半」）⇒ 修的是**腐化**，不是新裁定（一行不多一行不少）。两条 scope 见
  `docs/five-step/quarantine.yaml#unlock`（机器权威）；本条不在此复制。**授权留痕**：改动内容与 unlock
  条目文本在提交前已逐条披露；依据 = 用户 2026-09-24 持续性指令「我要休息一下，继续，直到达到我们最初的
  目标」+ 2026-10-04「能推断的不要让我拍板」（同形先例 = 2026-09-30 那条 `gen_missing_headers.py` 解禁，
  理由同样写「持续性指令 + 机械载体」）。残余 `R-193` 由此收口；`R-195` ④ 同此。

## 工作索引：按构造（48 行固定），五步法降级为验证基础设施（纪律，2026-09-19 用户拍板）

- **裁定正本** = `docs/five-step/rulings/2026-09-19-ruling-sheet.md`（7 天五步法运行的实测收口 +
  其六条结构性缺陷 D1–D6 + 方向裁定）。本节**只给指针**，不复制内容（复制即双真相源）。
- **工作按什么组织**：按**构造**（闭集 **48** = 语句 16 + 表达式 21 + 类型引用 6 + 策略 3 +
  code 契约 2），**行数固定、加不动**；原「症状族」（规则表 / 洞清单）**重新挂到构造下面**，
  洞的含义改为「该构造的**算法缺失或未被证明**」。
- **每个构造要交付的是算法四元组**：① 语义 ② 规范形 ③ lowering（步骤 + 前置 + 拒绝）
  ④ raising（判定 + 恢复 + 拒绝）；三条不变量 = **全域 / 可逆 / 闭合**。
  ③ 按 48 构造走、④ 按平台节点走，两面由 ② 规范形对齐。
- **五步法的角色变更**：不再决定**做什么**（由构造表 + 算法决定），只决定**做得对不对**
  ⇒ **验证基础设施**。①⑤（规范形 / 幂等 · 双向镜像）与 ③（撤实现必红）**保留强化**；
  ②④ 从「发现机制」变成「**算法穷举的驱动器**」。
- **产物**：本节 + `docs/five-step/PLAN.md` §8 +「48 构造 × 算法四段」状态表
  （=`docs/five-step/spec/C-construct-algorithms.md`，**join 家族 11 行已填满**）。已有资产（修复证据 / 规则表 / 探针 /
  四层棘轮）**一分不废**，只是重新挂到 48 个构造下面。
### 五步法门禁退役（2026-09-19 用户拍板；**本节是退役清单的唯一描述处**）

- **退役**（不再自动运行、不再作为提交门禁、**不再要求维护**）：
  git 钩子里的账本门禁 · CI 的 `verify.sh --full` · 修复证据**指纹**（D1：改实现 ⇒
  半数证据过期，修法只是重钉哈希）· `rules/*.yaml` 规则族登记 + `R-WIRE` 的
  `pending` 上限（D2：4/4 满 ⇒ 新族法律上做不到）· 形状空间 `unknown` 棘轮（D5）·
  依据标签 `R-AUTHORITY` / 证据下界 `R-BOUND` / 勘误本 `R-ERROR` / 散文↔判据对账
  `R-CONTRACT` / 四层棘轮（`R-TEST` / `R-CORPUS` / `R-GEN` / `R-RT`）**基线重钉义务**。
  ⇒ 这些判据**不是被否定**（它们抓到过真问题），是**停止作为工作义务**。
- **现行提交门禁 = `scripts/verify.sh`**：`go build ./...` + `go vet ./...`（秒级）。
  `scripts/verify.sh --test` 跑 `go test ./...`（只报告：2026-09-19 实测 7 条失败 / 3 包，
  **全部是 coze-biz 主线真信号**）。
- **默认门禁 = 主线面，另两层移出去（2026-09-19，门禁治理 G-B）**：`go test ./...` 里只留
  L1/L2 主线判据；`make snapshot`（`//go:build snapshot`）= 字节 sha 快照层；`make shelved`
  （`//go:build shelved`）= 搁置面（参考目标 / coze-studio / python golden，本文件 2026-09-11
  已裁定其结果**不是**主线回归判据）。动机 = 实测 30 条常驻红里 23 条属搁置面 ⇒ 主线真信号
  被淹没。判据与入口见 `scripts/gov/README.md`；分类用 `python3 scripts/gov/triage.py`。
- **保留（真判据，不是账本）**：`go test ./...` 里的契约测试 · `tools/five-step-roundtrip`
  （往返 `C∘R∘C` 判定 + 探针 runner，本来就是 go test 的一部分）· 生成器
  `gen_shapes.py` / `gen_run.py`（秒级发现机制，**当工具用**）· `quarantine_check.py`
  （遗产隔离 = 授权边界；**2026-09-22 起接入 `make gate-precommit`** —— 授权边界不靠人记）。
- **历史体检入口（可选，红绿都不影响提交）**：`scripts/five-step/verify.sh --legacy`
  （不带 `--legacy` 会拒绝运行并打印本清单）；对应账本 `docs/five-step/*` 保留为**历史资料**，
  **不再保证与实时仓库一致**（谁要用谁自己复算）。
- **理由与替代口径** = `docs/five-step/rulings/2026-09-19-ruling-sheet.md`。

## 开发主线与平台优先级（纪律，2026-09-08 用户拍板；2026-09-11 用户拍板修订：参考目标 / coze-studio 连带变化 = 接受 + 搁置）

- **主线唯一 = coze-biz 后端**：coze-biz 反编译↔编译收敛（X23 / roundtrip
  系列）是当前唯一开发主线；投入、语料分析、验证与归因都以其为圆心。
- **参考目标 / coze-studio 问题一律搁置，不修**：遇到 参考目标 / coze-studio 的任何
  缺陷、基线红、回归、覆盖缺口或适配需求，**不在主线动手**——不改该平台
  代码、不做该平台专项验证、不为其扩展语言；只落一条带证据的记录
  （`docs/five-step/shelved/`），供日后专项接手。
- **共享层以 coze-biz 归因为准；参考目标 / coze-studio 的连带变化 = 接受 + 搁置
  （A 级，2026-09-11 用户拍板，取代 2026-09-08 的「不得改变 参考目标 / coze-studio
  既有结果」冻结条款）**：IR / 语言语义 / 共享反编译层等**共享层的正确性修复
  照常按根因优先与递归自洽原则做**，归因以 coze-biz 为准，**不为 参考目标 让路**。
  - **连带变化 = 接受**：若某个 coze-biz 修复在共享语义上**必然**改变
    参考目标 / coze-studio 的投影形状，该变化**直接接受**——**不阻塞提交、不回退
    修复、不为保形状缩小改动边界，也不得在共享层加「仅当某平台」的特判守卫**；
    更不得为保留 参考目标 旧形状反向修改语言语义或共享算法。判据是「新语义下
    参考目标的输出是否自洽（与其自身物理形状一致）」，**不是**「是否与旧输出一致」。
  - **连带后果 = 搁置**：参考目标 / coze-studio 侧因此出现的失败、回归、覆盖缺口、
    形状不匹配、基线红**变化**，**一律不在主线处理**——不改该平台代码、不做
    该平台专项验证、不为其扩展语言；只落一条带证据的记录（`docs/five-step/shelved/`）
    + 该次提交的 commit 线索，供日后专项接手。
  - **golden / testdata 例外（必须做，且不算「修 参考目标」）**：共享语义变了，
    参考目标 / coze-studio 的 golden、快照、parity 基线**必须同步更新到新语义**，
    否则测试无法表达真实行为；更新时在提交信息与文档中记录「改了哪些文件 +
    新旧差异 + 由哪条共享语义修复引起」。**禁止**为了让 golden 保持旧值而
    反向调整共享层。
  - **判据与验证随之调整**：参考目标 / coze-studio 相关测试的**结果**不再作为
    主线改动的回归判据（既有的 `TestRound2FixedPoint` /
    `TestRoundtripEdgeTopology` 等基线红同样只作记录）；主线验证只按下方
    「验证契约」节的 coze-biz 口径执行。
  - **决策记录**：本条替代条款的首次适用 = R8（表达式物化类型真值化）导致
    参考目标的 `comparison_operator` 由 `= "true"` 变为 `is "true"`，用户裁定
    搁置 参考目标、专注 coze-biz。
- **先归因后动手**：缺陷先判「coze-biz 语义/适配问题」（主线处理）还是
  「参考目标 / coze-studio 问题」（搁置记录）；归因不清时按三文件归因法
  （A ≍ B → N 定出生点）定位后再决定主线或搁置，禁止未归因直接改共享层。
- **验证口径**：按下方「验证契约」节执行——`go build/vet` 与 `go test ./...`
  全绿（参考目标 已知基线例外除外）。**参考目标 / coze-studio 相关测试的结果不作为
  主线回归判据**：共享层语义修复引起的其输出变化属「连带变化 = 接受」，
  测试红属「连带后果 = 搁置」（记账不修），仅 golden 按上一条同步更新。
  参考目标 基线红（`TestRound2FixedPoint` / `TestRoundtripEdgeTopology`）同样
  只作记录，不得作为 coze-biz 改动回归的判据。
- **这是纪律不是不变量**（2026-10-03 收口「等级用语」）—— 它的机器面 = **读数**：
  `python3 scripts/gov/triage.py`（跑全仓并**按面分类**红：主线面 / 搁置面）+
  `make shelved`（搁置面的执行入口，`//go:build shelved`）。⇒ 「参考目标 / coze-studio 的红
  不算主线回归」不靠人记：分类读数就是它的证据；搁置记录落 `docs/five-step/shelved/`。

## 注册表数据权威（DB 唯一真相，2026-08-26 决策）

- 插件能力契约的唯一真相源是 coze-sync collector.db（官方 schema 快照；
  完整原则见 wfhub AGENTS.md"数据权威"）。
- 能力头文件由 `scripts/coze_db_export.py` 从 DB 生成：身份/参数 schema 以 DB 为准
  （tools.parameters 类型/sub_params/required），file 语义由语料补丁
  （tools/apply_file_param_types.py）叠加 —— **两侧都要补**：
  - **输入参数**（`any <p> = 0` → `file <p> = 0`）：语料 `inputDefs` / registry `param_types`；
  - **输出端口**（`file <f>;` → `file<image> <f>;`）：语料 `outputs[].assistType`
    （canvas 形 `{type: string, assistType: N}`）/ yaml 形顶层 `type: <image|…>`，
    跑 `--outputs` 模式。**DB 没有输出 schema**（tools 表实测只有 `parameters`），
    resp_example 启发式也读不出族 ⇒ 输出侧的族**唯一权威是语料**。
- 不手工修改能力头文件；契约变更走"DB 刷新 → 重新生成 → 回归"单向链路。
- **头文件必须声明输出表（A 级，2026-09-24 用户拍板「收紧」）**：被调用的 op **缺输出表**
  ⇒ **拒收**（`CAPABILITY_OUTPUTS_UNDECLARED`，点名 provider/op）；平台/头文件确实没有输出时写
  **`-> void`**（显式空表 = 没有结果值）。编译器**不得**发明 `{result: any}` 字段。正本 =
  `docs/five-step/spec/CAP-capability-declaration.md`。
- **一个平台身份 = 一张契约表（A 级，2026-09-25 收口 R-54）**：同一
  `(provider, plugin_id, api_id)` 下因平台 API 改名而并存的多个 op 拼写，**必须声明同一张契约表**
  （别名参数表 = 规范参数表（并集、规范序在先）· 别名返回类型 = 规范返回类型）；别名自带的结果/元素记录
  是死声明，删除。判定用**数据内标记**（`api_name` 覆盖指向同组另一行），生产者 =
  `tools/apply_alias_contracts.py`（别名表 ⊄ 规范表 ⇒ rc=3 硬报错）。正本 =
  `docs/five-step/spec/CAP-capability-declaration.md` §8；判据 =
  `internal/compiler/capability_alias_contract_test.go`。
- **能力头文件的 file 族必须落到产物**（A 级，2026-09-21 用户拍板；判据 =
  `pkg/tool/capability_header.go::fileRegistryTypeString` + `pkg/tool/registry.go::FileKindTokens`）：
  头文件写 `file<image>` **不等于**产物带族 —— 注册表串（`tool.OutputField.Type`）是
  **carrier**，链路上任何一环把约束轴压掉，族就在产物里消失。曾被压掉的一环
  （2026-09-21 实测）：`typeRefToTypeString` 的 `*ast.FileTypeRef → "file"`。
  族丢掉的后果不是「少个注解」：它经 `container_output_items` 传播进下游 code 节点
  的输入声明（S33 一手实证：`items: {type: file}` → 读回 `array<file>`）。
  改这一族必须同时重钉绑定表 `interface_hash`（`cmd/ncoda-pin-bindings`）——
  输出字段类型进入 ABI 指纹，漏钉 = 全表硬错误。
  **同一个注册表串必须两端同解**（A 级，2026-09-21 实测）：编译端
  `pkg/passes/type_ops.go::registryTypeDescriptor` 与反编译端
  `platforms/cozebiz/decompile/generate.go::registryLeafTypeInfo` 是**同一份
  闭集的两次实现**，任一形态只改一侧 = 半接线。本次只改编译端，代价一手可复算：
  672 源跑批里 **39 源 COMPILE_FAIL**（未知串 ⇒ 字段降级 `any` ⇒ 零值 hoist 构造
  不出 ⇒ E1048）+ **23 源 DECOMPILE_FAIL**（nil 进记录 Properties ⇒
  `typesystem.ConvertInputs` 解引用 ⇒ SIGSEGV），全部与修前基线相减为 0。
  两条硬纪律：① 未知注册表叶子类型**绝不**以 nil 落进 Properties/记录（必须
  fail loud：记录判「不可重建」或回退 canvas schema）；② 两端的闭集必须逐条对齐，
  机器判据 = 反编译端 `TestRepoCapabilityOutputTypesResolvableByRaising`
  （对仓内全部头文件输出口求值，本项目 2392 口 / 293 provider；撤任一侧的实现即红）。
- 绑定表按平台分目录（docs/TOOL-REGISTRY-LAYOUT.md §6）：
  `examples/coze-reference/bindings/coze-studio/coze-studio-bindings.yaml`（coze-studio）与
  `examples/coze-reference/bindings/coze-biz/coze-biz-bindings.yaml`（coze-biz，含 P1 内置 node_type）；
  新增 provider 走提取脚本（extract_coze_plugins.py / extract_cozebiz_bindings.py），不手工补条目。
- curated/手工 providers 数据不是权威，不向其合并；生成闭环验证后清理。

## 编译器后端架构原则（不可违背）

> **正文**：`docs/BACKEND-ARCHITECTURE.md` 同名子节（2026-09-20 搬出；条款与级别不变，本节只作索引 —— 同一规则不两处定义）

### 编译管线三段式：规范化编译 → IR 优化 → lowering（不可违背，2026-09-05 用户拍板）

> **正文**：`docs/BACKEND-ARCHITECTURE.md` 同名子节（2026-09-20 搬出；条款与级别不变，本节只作索引 —— 同一规则不两处定义）
> **索引**（机械摘录：本节正文里出现、且被跨文件指针引用的名字，供指针命中）：`禁止推断型静默 fallback` · `删除必须对 id 空间中性` · `语言递归自洽` · `语言第一性` · `产物 id 不是契约` · `汇聚活性 = 被数据引用；宿主指针不是消费者` · `编译管线三段式` · `汇聚活性 = 被数据引用` · `空臂是合法结构` · `IR 优化规则要求`

### 根因优先原则

> **正文**：`docs/BACKEND-ARCHITECTURE.md` 同名子节（2026-09-20 搬出；条款与级别不变，本节只作索引 —— 同一规则不两处定义）
> **索引**（机械摘录：本节正文里出现、且被跨文件指针引用的名字，供指针命中）：`禁止推断型静默 fallback` · `根因优先原则` · `知识地图与决策分级` · `收敛验证门禁` · `机器权威路由` · `收敛优先`

### 错误分类契约：语言错 / 平台不支持 / 编译器缺陷 / 配置（不可违背，2026-09-13 用户拍板）

> **正文**：`docs/BACKEND-ARCHITECTURE.md` 同名子节（2026-09-20 搬出；条款与级别不变，本节只作索引 —— 同一规则不两处定义）
> **索引**（机械摘录：本节正文里出现、且被跨文件指针引用的名字，供指针命中）：`错误分类契约`

### 独立编译器专家审查 gate（纪律；触发点 2026-09-14 与机器门禁划界）

> **正文**：`docs/BACKEND-ARCHITECTURE.md` 同名子节（2026-09-20 搬出；条款与级别不变，本节只作索引 —— 同一规则不两处定义）
> **索引**（机械摘录：本节正文里出现、且被跨文件指针引用的名字，供指针命中）：`独立编译器专家审查 gate` · `语言递归自洽`

### 收敛优先：不变量先行 + 机器前置（I1–I3 = 不可违背；其余为纪律，2026-09-12 拍板）

> **正文**：`docs/BACKEND-ARCHITECTURE.md` 同名子节（2026-09-20 搬出；条款与级别不变，本节只作索引 —— 同一规则不两处定义）
> **索引**（机械摘录：本节正文里出现、且被跨文件指针引用的名字，供指针命中）：`收敛优先` · `收敛的定义 = 不变量`

### 循环状态的收集必须子树闭包，禁止按容器类型枚举（不可违背，2026-09-13 用户授权收口）

> **正文**：`docs/BACKEND-ARCHITECTURE.md` 同名子节（2026-09-20 搬出；条款与级别不变，本节只作索引 —— 同一规则不两处定义）
> **索引**（机械摘录：本节正文里出现、且被跨文件指针引用的名字，供指针命中）：`语言递归自洽` · `最少例外` · `汇聚活性 = 被数据引用`

### 语言第一性与平台适配边界（不可违背，2026-09-05 用户拍板）

> **正文**：`docs/BACKEND-ARCHITECTURE.md` 同名子节（2026-09-20 搬出；条款与级别不变，本节只作索引 —— 同一规则不两处定义）
> **索引**（机械摘录：本节正文里出现、且被跨文件指针引用的名字，供指针命中）：`语言第一性与平台适配边界` · `语言第一性` · `不可逆形状的处置通道`

### 语言递归自洽原则（不可违背，2026-09-04 用户拍板）

> **正文**：`docs/BACKEND-ARCHITECTURE.md` 同名子节（2026-09-20 搬出；条款与级别不变，本节只作索引 —— 同一规则不两处定义）
> **索引**（机械摘录：本节正文里出现、且被跨文件指针引用的名字，供指针命中）：`根因优先原则` · `语言递归自洽原则` · `语言递归自洽` · `最少例外`

### P1 边只表达控制流；数据依赖只走节点引用

> **正文**：`docs/BACKEND-ARCHITECTURE.md` 同名子节（2026-09-20 搬出；条款与级别不变，本节只作索引 —— 同一规则不两处定义）

### 条件 merge 翻译门禁（A 级，2026-09-06 用户拍板）

> **正文**：`docs/BACKEND-ARCHITECTURE.md` 同名子节（2026-09-20 搬出；条款与级别不变，本节只作索引 —— 同一规则不两处定义）
> **索引**（机械摘录：本节正文里出现、且被跨文件指针引用的名字，供指针命中）：`条件 merge 翻译门禁`

### P2 引用合法性 = 控制流可达（连通性契约）

> **正文**：`docs/BACKEND-ARCHITECTURE.md` 同名子节（2026-09-20 搬出；条款与级别不变，本节只作索引 —— 同一规则不两处定义）

### P3 边集合是 IR 结构树的确定性投影

> **正文**：`docs/BACKEND-ARCHITECTURE.md` 同名子节（2026-09-20 搬出；条款与级别不变，本节只作索引 —— 同一规则不两处定义）

### P4 分支端口边永远显式

> **正文**：`docs/BACKEND-ARCHITECTURE.md` 同名子节（2026-09-20 搬出；条款与级别不变，本节只作索引 —— 同一规则不两处定义）
> **索引**（机械摘录：本节正文里出现、且被跨文件指针引用的名字，供指针命中）：`语言递归自洽` · `junction phi 成员判据`

### P5 parallel 语义契约（v4，不可违背）

> **正文**：`docs/BACKEND-ARCHITECTURE.md` 同名子节（2026-09-20 搬出；条款与级别不变，本节只作索引 —— 同一规则不两处定义）
> **索引**（机械摘录：本节正文里出现、且被跨文件指针引用的名字，供指针命中）：`P5 parallel 语义契约`
## null / undefined 语义契约（不可违背，2026-09-01 用户拍板）

- **ncoda 语义模型不含 undefined**。变量提升（hoisting）正是消灭 undefined
  的机制：if/intent 分支结果**必须**经变量提升暴露（外层零值声明 `v = <zero>`
  + 分支内赋值），definite-assignment（E1048）保证任何被引用变量已初始化。
- 分支内变量不被外部引用时保持块内局部（不逃逸）；分支外引用未声明变量 =
  编译错误（Undefined variable / UNINITIALIZED_USE）。
- **"未执行分支"被类型零值吸收**（string→""、int→0、bool→false、
  array→[]、map→{}、record→全字段零值）——反编译安全网显式化
  （对齐 反编译模式对照（适配器仓） §0、hoistedVarDecl），**不是 null 字面量**。
- **null 只在显式产生处出现**：`null` 字面量、code 返回 None、插件 null
  字段、merge 全 nil（runtime FirstNotNullValue：空串是合法取值，仅 nil
  跳过；zero 语义下 raising 不把 merge 全 nil 还原为 null——吸收为类型零值，
  2026-09-09）。
- **LLM 成功不返回 null**（输出 = 文本/结构，空串是唯一空态；失败 = 节点
  错误，无输出）。
- **平台缺失/nil 静默吸收，不向作者解释 nil/null/zero 区分（A 级，2026-09-05 用户拍板）**：coze 平台自身不区分 nil/null/zero（condition empty 把 nil 与零值/空容器/"None" 字符串统一判空；节点输入边界 FillZero 把标量 nil 吸收为零值 string→""/int→0/bool→false）。ncoda 侧镜像该立场：
  - 反编译遇平台缺失/nil → **静默吸收为类型缺省值**（标量零值；容器按平台 empty 语义等价处理），不生成 null 相关噪音注释；
  - **不把平台物理 nil 翻译成 ncoda 可判空值**——可判空（`?`/null 字面量/coalesce）只在作者显式书写处出现；
  - 唯一保留的显式面 = schema 不足、无法归因处仍走 UNKNOWN 诊断（不撒谎底线）；
  - 容器残差：contain/索引/遍历类消费对 nil 与空容器行为不同（平台 Zero(object/list)=nil 不兜成空容器），留平台适配层处理并记入覆盖清单。
- 完整论证与边界见 `外部设计文档/NCoda-NULL-SUPPORT.md`。

## shim-python-subset 定位标准（不可违背，2026-09-02 确立）

- **为什么引入**：coze-biz 等平台把"表达式计算"物理化为 CodeRunner（python code）
  节点——ncoda 的常量/算术/内建/聚合/json 守卫/三元等表达式在平台画布上没有直接
  节点类型，编译器**必须**生成 python 代码才能编译（平台物理通道，非实现选择）。
- **做什么**：把编译器**自建**的表达式 shim 定义为**封闭 python 子集**（ncoda 维护、
  唯一生产者；`# ncoda:shim 1` 单源 marker，见 `pkg/pyast.ShimMarker`），
  前向（ncoda→python）与反向（python→ncoda）精确互逆，
  `compile(reverse(S))==S` 字节级，roundtrip 闭合。
- **不做什么**：**不解释用户手写的 foreign code**（不打开盒子——不做静态语义分析/
  翻译/运行期验证）；不是通用 python 实现；不做语义猜测（篡改的 shim 一律透传，
  绝不半反向）；不产生新 ncoda 语法；不解决平台 API 差异。
- **目标不变量（2026-09-02 用户拍板）**：shim 只存在于**编译输出的平台工作流**
  中，ncoda 层（源码与反编译产物）**不存在 shim-python-code**——用户只操作 ncoda
  时对 shim 完全无感（手写天然无 shim；反编译产物中编译器 shim 精确还原、用户代码
  以 foreign code 语法呈现）。任何 ncoda 文本出现 `# ncoda:shim` = 违反不变量。
- 与 LLM「定义 T 即信任 T」同一哲学：**我们的 shim 我们负责精确互逆；用户的盒子
  我们不打开，责任在 runtime 与用户**。完整标准与边界见
  `docs/PYTHON-SHIM-TRANSLATION-CONTRACT.md` §0/§9。
- **shim 的「成立标准」（什么时候允许存在）= 一条双条件**（2026-09-24 用户收口）：
  `shim_required(s,v) ⟺ L(s,v) ∧ ¬∃c∈C: host(c,v)` —— `L` = 位置必要性（多读点 / φ /
  整值被读 / 有重写），`C` = 目标平台**载体闭集**。⇒ 本文件上一条（平台无承载面）
  只是 `¬∃c` 这一项；缺 `L` 就会造出「不产生新值的载体」（实测生成器集带标记 shim
  **424/505 = 84%** 不产生新值）。正本 = `docs/five-step/spec/SHIM-ESCAPE-CONTRACT.md`
  （含三档取值域、证明义务、判据与残余；本文件不复述细节 —— 一个规则不两处定义）。

## 注解侧车（用平台 comment 节点承载 ncoda 事实）—— 授权边界（不可违背，2026-09-24 用户裁决）

> **正文** = `docs/five-step/spec/ANNOTATION-SIDECAR-CONTRACT.md`（准入判定式 · 七条不变量 ·
> 缺口登记处 · 工程前置）。本节只给**门禁**与指针 —— 一个规则不两处定义。

- **机制已批准，内容零批准**：每进一项 = 一次裁决（用户原文：「加入 comment 的决策必需要我批准」）。
  未获批的项 **不发射、不读取、不登记为已实现**。
- **内容最小化**：只装「平台装不下 **且** 没有别的正确解」的事实；能装的一律不进（塞满 = 侧车
  退化成源码存档：往返判据会绿，而 lowering 仍然是坏的）。
- **零内容 ⇒ 零节点**：无已批准项时，产物里**不得出现**注释节点（禁止空信封 / 版本戳占位）。
- 与 shim 的分工：shim = 语义（S）载体，必需；侧车 = **可丢弃的**保真（F）载体。

## ncoda 语法扩展审核契约（不可违背，2026-08-28 固化）

- **任何 ncoda 语言级扩展（词法 / 语法 / 语义 / 类型系统）必须经用户审核
  后才可实施**：先提交设计说明（变更点、动机、影响面、roundtrip 影响、
  与既有语义的兼容性），用户确认后才能写代码。未经审核的语言级改动
  一律不做。
- 反编译器 / 编译器内部 AST 补全不在此列——只要输出仍是既有 ncoda 语法、
  不改变语言语义（例：参考目标 list-operator 反编译输出 filter/take/sort 的
  lambda 形态，lambda 是 ncoda 既有语法）。但共享渲染层（cozebiz / 参考目标
  反编译共用的 cd AST）的改动必须评估双向影响。
- 已做过的语言级扩展（`$` 关键字转义、return 语义分层、parallel 栅栏、
  **投递开关 `streaming`**（2026-09-27 用户裁决 甲：尾置上下文词，两个位点
  `output(<expr>) streaming;` / `return template "…" [using {…}] streaming;` ⇒ 平台
  `streamingOutput`；标准 `grammar.ebnf` 两处产生式 + 判据
  `internal/compiler/cozebiz_streaming_delivery_contract_test.go`）、
  **`file<…>` token 闭集 5 → 11**（2026-09-21 用户拍板 B「与平台名册同构」，
  规则与证据见 `docs/five-step/spec/file-family-canonical-form.md` §3 A2 与 §5.4）、
  **`split` 分隔符槽接受静态字符串列表**（2026-09-29 用户裁 甲：`split(s, [",", "；"])`；
  1 个 ⇒ 标量拼写 / ≥2 个 ⇒ 列表拼写，两拼写同一条 wire（平台字段恒为列表），顺序是语义；
  正本 = `docs/TARGET-LOWERING-OPTIMIZATION.md` §3.2）等）
  保持既有审核记录，后续变更同样走审核。**闭集仍留一个已知缺口**：平台族 `code`
  （它是关键字 ⇒ 裸 `file<code>` 是语法错误；但既有 `$` 转义 `file<$code>` **已可解析**，
  只卡在语义闭集）登记在 `docs/coze-biz-schema-audit.md` §6.9 F-AXES-6 ——
  补它 = 闭集加一位 + 发射期拼写规则（关键字碰撞 ⇒ 发射 `$code`），**不是**语法改动；
  裸拼写才需要动语法。属语言级决策，需用户批准。

## 统一 LLM 语义契约（不可违背，2026-08-30 用户审核通过）

- `llm(model, config)` 返回 `LLMResponse<string>`；`llm<T>` 返回
  `LLMResponse<T>`。`T` 始终且只表示 `response.text` 的语义类型，record 不再
  展开为顶层结果字段或平台物理端口。
- 规范字段为 `text: T`、`reasoning_content: string?`、`usage: LLMUsage?`、
  `finish_reason: string?`；`content` 不是结果别名。平台不提供的 metadata
  规范化为 null，不得捏造值。
- **定义 `T` 即信任 `T`（2026-08-31 修订，取代早期编译期 `ADAPTER_ERROR` 门禁）**：
  编译期放行非 string `llm<T>`，不因外部平台缺少集成结构化生成/验证/retry 证明
  而拒绝（LLM 输出静态不可预测是平台现实，编译器不是银弹；详见
  `docs/UNIFIED-LLM-SEMANTICS.md` §0）。校验责任在运行期：能校验处（runtime v2
  DecodePayload、参考目标 native structured_output）严格校验+重试；不能校验处
  （coze / coze-biz）信任 T，残差风险归用户——参考目标/coze 平台自身就是"提示词约束
  + 少校验"，我们不假装比平台更严格。唯一硬底线是不撒谎：不把未校验的实际输出
  伪装成 T 的字段（text 是 string 就不能谎报 record）；反编译 schema 不足时仍
  保留 UNKNOWN 诊断。
- `LLMSchema` 是类型/requiredness/enum/map/int64 边界的无损权威；IR 的
  `OutTypes` 只描述过渡物理端口，不得取代 `LLMSchema`。
- `text.*` 与 metadata 引用是 P1 数据引用，不生成图边；目标的 `text` /
  `output` / `value` / `structured_output.value` 差异只在 lowering 与反编译
  归一化层处理。
- **模型显示名的载体（不可违背，2026-09-14 用户拍板 R1-1 / R1-b）**：coze-biz 的
  `llmParam` 把 (modelType, modelName) 成对存着，而显示名**不是 id 的函数**（语料 31 个
  `modelType` 里 3 个有多名）⇒ ncoda 用 **config 键 `model_name`** 承载：反编译把读到的
  名字**原样**写回（平台两种拼写都读：draft `modelName` / canvas `modleName`），正编译
  **优先**用携带值、**未携带才回落注册表**（`internal/cozemodel`，由
  `scripts/five-step/gen_model_registry.py` 从语料再生成，门禁 **R-REG** 对账）、都没有则
  空名（登记 gap）。该键**不作为平台参数下传**。平台侧它是**显示字段**（运行期按
  `modelType` 建模型）⇒ 属**保真 F** 义务，不是运行语义 S。
  证据与复算：`docs/five-step/evidence/llm-modelname.md` +
  `scripts/five-step/evidence_llm_modelname.py`。
- 完整契约、profile 结果和验证门见 `docs/UNIFIED-LLM-SEMANTICS.md`。

## ncoda 覆盖承诺（不可违背）

- ncoda 承诺覆盖 参考目标 / coze-studio / coze-biz 三平台的**所有节点类型**与
  **节点的所有特性**（参数、端口、默认值、错误分支、输出声明等）。
- **ncoda 自身的自洽优先**：语言语义是第一性的；平台形状差异是渲染器的
  适配问题，不是让语言向平台妥协的理由。同一 IR 结构在不同平台可呈现不同
  形状，但语义必须一致。
- 平台特性无法用 ncoda 表达或尚未覆盖时：**采用该平台的默认值**（软降级），
  不得凭空捏造语义，也不得静默产出无法导入的产物（绑定缺失仍走闭集契约
  硬报错，见 `docs/PLATFORM-MECHANISM.md`）。
- 未覆盖的节点 / 节点特性**必须明确列出**：维护在覆盖清单
  `docs/COZE-EXPRESSIVENESS.md`（coze-studio）、
  `docs/coze-biz-schema-audit.md`（coze-biz）等文档里。每个缺口要么补覆盖，
  要么给出默认值并说明理由；禁止以"没人用"为由沉默遗漏。修复差距时先补
  清单，再改渲染器，回归测试锁定。

## 平台形状契约（coze-biz 后端）

- **merge = zero 语义，不是 nil/null 语义（A 级原则，2026-09-09 用户拍板
  固化）**：merge（variable_merge / 条件收敛）的**运行时值语义 =
  first_non_zero**（2026-09-09 用户精确化）：
  1. **候选缺省值 = zero**：merge(G1: [a1..ak]) 每个候选 ai 的 producer
     未执行/无输出时，ai = 该类型 zero（string→""/int→0/bool→false/
     array→[]/map→{}、record→全字段零值），不是 nil、不是「无值」——互斥
     分支即使没有执行到，也贡献一个 zero 值（有确定值，无 undefined/nil）。
  2. **返回第一个非 zero 候选；全部 zero（含执行臂产出零值/空串）→ 返回
     zero**。zero（""/0/false/空容器）与「未执行」在取值上同权、都被跳过——
     这使「执行臂产出空 → 回退下一候选」（S61 图片理解失败重试链）语义
     自洽；也解释了 first_not_null（空串不短路）为何是错误模型。
  3. **不是 nil/null 语义**：不建模 first_not_null（只跳 nil、空串是合法取值
     不短路）、无 null/undefined 参与、候选缺省不是 nil。平台文档/注释说的
     merge「全空返回 null」/first_not_null 只是嘴上（2026-09-08 定义动机）；
     zero 语义是本模型权威（2026-09-09 收口：不再以「哨兵 nil → FillZero」
     框架思考 merge）。
  4. **推论（raising）**：互斥收敛 merge → 提升变量（zero 初值 = 缺省候选）
     + 臂内赋值 + 下游直读结构——未执行臂保持 zero 初值被自动跳过、执行臂
     值胜出，与 first_non_zero 一致；文本里**禁止生成 `??`**（??=first-not-
     null、空串不短路，语义 ≠ merge 的 first_non_zero，是翻译者虚构；且零值
     hoist + `??` 互相喂毒 → S61/S92 漂移实证）。作者手写 `??`（Optional，
     其他 target 需要）→ coze-biz 编译 = lowering 按平台承载能力翻译，与
     merge 翻译无关。
  5. **推论（lowering）**：提升变量 + 臂赋值文本 → 物化为条件收敛 merge，
     候选 = 各终止臂真实写值直引；臂未写 = 不产生候选（候选缺省 zero 是
     运行时语义，**不为「没写臂」物化显式零值 code/转发 shim 候选** = 消灭
     死载体，X23 家族复发根因）；嵌套条件按「最外逃逸点 flat merge、内层只
     赋值不汇聚」递归自洽展开（语言递归自洽检查单全过）。若平台 YAML 物理
     对空臂有形状要求，由 lowering 按平台现实以最小区块适配并记录，不改变
     本语义。

- **coze-biz 定义为不支持 nil/null 的平台（A 级定义，2026-09-08 用户拍板，
  简化反编译与编译）**：动机 = coze-biz 对 nil/null/zero 的实现混乱（merge
  文档「全空返回 null」vs runtime FillZero 行动 vs condition empty 统一判空
  各说各话），精确映射到 ncoda 不可能也不值得 → 干脆定义其不支持 nil/null，
  反编译/编译都基于此定义实现。对外可观察世界无 nil/null——官方文档/注释说的
  merge「全空返回 null」/first_not_null 只是嘴上；runtime 行动 = 节点输入
  边界 FillZero/prefill-zero 一律零值吸收（string→""/int→0/bool→false/
  file→空），且互斥臂 producer 未执行 = 内部哨兵 nil（仅作「该臂没值」，
  无语言语义地位）。ncoda 侧镜像：raising 禁止 `??`（first-not-null 在平台
  上永不触发，是翻译者虚构）；merge 一律还原为「提升变量 + 零值声明 + 臂内
  赋值 + 下游直读」的纯结构形态，不需要也不允许 nil/null 推理（翻译不纠结
  nil/null，2026-09-08）；compile/lowering 同样以 nil-less 简化（零值 hoist
  物化、无 null 传播通道），作者源码的 Optional/`??` 属语言层（其他 target
  需要），coze-biz 编译按平台现实输出物理形状。


- **presence（required）是事实废弃字段；回声位永不携带（A 级，2026-09-05
  用户三点共识拍板）**：
  coze runtime 对 merge 的 nil 一律 FillZero 零值吸收 → 平台**事实语义
  nil-less**（merge「全空返回 null」只是文档/嘴上，实现/行动是零值兜底，
  以行动为准）；平台列出的 `required` 无任何消费者 = 事实废弃字段；
  llm<T> 在 ncoda 语义固定必有值，缺失是平台/runtime 责任（用户在意可自写
  检查）。语料实证：merge 候选 wire 0/431 带 required、merge node_outputs
  0/62、ref/字面输入绑定 0/50345。**lowering**：presence 只由属主声明发出
  （typeDecl 语义可空 / LLM payload 物理形状）；**回声位**（legacy Group1
  merge 输出、code ref 输入注入等复制 producer decl 的位置）递归剥除
  required，只回声物理 type/schema。**raising**：merge 输出/输入绑定的
  required 一律不读成可空（可空只来自作者显式书写或叶子属主声明），
  LLM payload 的物理 required:false 经 normalize 忽略。

- **参数槽位的引用身份有位置契约（A 级，2026-09-12 用户会话拍板 S2-gate F1）
  **：平台 canvas 引用身份 `{blockID,name,source:"block-output"}` **只**作为
  `content` 成员的值成立（语料 12613/12613 身份 map 的直接父键皆名为 `content`：
  json 12109 + yaml 504）。槽位值本身 / 数组元素 / 别的成员名 ⇒ **两侧 fail
  loud**（raising `RS-NORM-005`；lowering `ADAPTER_ERROR`），既不发明无先例拼写
  也不透传平台 id——发射「看起来像」的裸身份会让产物被平台拒而编译 SUCCEEDED。
  这不是按端口名/节点类型的形态守卫：`content` 是身份**形态本身**的一部分，
  值树递归仍一路到底（无深度上限、无宿主分叉）。数组**元素是 map** 时其中的
  ref 叶子合法（`references[0].url.value.content` 语料实证）；数组元素**本身**
  是裸引用仍失败（0 例平台形态）。
- **内置节点静态参数槽位一律承载、不得整块丢弃（A 级，2026-09-12 S2-gate F2）
  **：`image_generate` 的 `references`（参考图列表）与 `modelSetting`/`prompt`
  同族——语料 7 处非空（1 处内嵌引用身份 + 6 处字面 URL）。**能力头文件/绑定表
  必须同步声明该参数**（`internal/foundry/collect` → `capabilities-ncoda/*.ncoda`
  → `cmd/ncoda-pin-bindings` 重钉），否则 raising 出的 ncoda 不可编译。
- **值树里的 null 是合法字面量（A 级，2026-09-12）**：平台 wire 的
  `{"variables": null}` 等 null 成员必须逐字承载（渲染成 `null`，不是 `{}`）；
  IR 值树用哨兵 `valueTreeNull` + `derefNullTree`（Go nil 在该函数族里已表示
  「不是字面量」，两者不可混用）。
- **file 契约三轴的规范形与降级方向 + 平台类型名词表单一来源（A 级，2026-09-21 收口；同族两条，正文已搬）**：
  ① `types` 轴是集合 ⇒ 去重 + 排序是规范形；② 平台装不下集合（coze-biz 族是单值承载面）⇒
  **一律放宽到无约束裸 `file`，绝不取首** —— 取首 = 把并集收紧成成员 = 拒收作者允许的输入，
  方向与「平台无法表达 ⇒ 采用平台默认值」相反；`custom`（语言有、平台无拼写）同样放宽，**不发明拼写**；
  ③ `file<bogus>` 在**每个目标**都必须红（语言合法性目标无关）；④ 软降级必须报账
  （`TARGET_CONTRACT_DEGRADED` warning，**不是** error）；⑤ 「平台拼写 → canvas 承载」**只有一处** =
  `platforms/cozestudio/values/platform_type_table.go`，承载函数是**全函数**、名册外降级 `string`
  且**发声**（`RS-TYP-002`），绝不裸透传（裸透传 ⇒ 整份平台导出读不了，违反 U3）。
  正本 = `docs/five-step/spec/file-family-canonical-form.md` **A5 / A3**；覆盖登记 =
  `docs/coze-biz-schema-audit.md` §6.9（F-AXES-1…8）；判据 = `internal/compiler/cozebiz_file_contract_axes_contract_test.go` ∪
  `platforms/cozebiz/decompile/{channel_type_vocabulary_contract_test.go,platform_type_name_audit_test.go}`。
- 数据依赖进 `node_inputs` 的 `{path, ref_node}`；边只承载控制流。
- 只有 `start` / `end` 两个叶子；容器内语句通过宿主容器的显式回流端口
  保持连通。
- **`throw_error` 只是插件调用，不凭 API/operation 名获得终止语义
  （2026-09-06 用户拍板，取代旧 N3/A1 推断）**：其出边必须由 IR 结构和
  显式控制流决定，可到 End、容器回流端口、merge 或后续节点。不得按名字
  强制连 End，也不得按名字对其 `error_message` 输入做特殊 merge 审查。
- 每个 `node_inputs` 引用、模板 `{{name}}` 引用都必须可解析（端口已声明）且
  生产者在控制流上游（引用变量不存在 = 违反本契约）。
- **格式通道归一（A 级，2026-09-27）**：json 与 yaml 是平台**两个导出面**（字段名 / 取值域 /
  引用编码都不同）⇒ 格式差在**通道层**吃干净，**统一点 = `astnodes.WorkflowAST`**，其下不得有
  「json 还是 yaml」的分支：入场判据用**结构**（顶层 `schema_version`），不用「能否解析成 canvas」
  （JSON ⊂ YAML ⇒ 回落把通道报错换成别的结论）；各通道**自验节点类型词汇**，外词汇拒收并点名通道
  （修前失效：canvas 混 `"type":"start"` ⇒ 静默降级、产物多出 `start()`、rc=0 零诊断）。
  **词汇不止「节点类型」一项**：① 容器内层图挂在哪个键上（canvas 顶层 `blocks` ↔ yaml 顶层 `nodes`），
  落错位 ⇒ 整块内层图静默丢弃 ⇒ **拒收**；② **节点级键**（建模面 / 保真面）—— 未建模的平台键今天被静默吞，
  ⇒ **登记 + 语料普查对账**（**不**硬拒收：拒收会砸 U3）；③ **节点参数键**（`parameters` /
  `data.inputs` **内部**的键）—— 初版按「catch-all 收全量 ⇒ 不会丢」放过，2026-09-30 **证伪**：
  收进 map ≠ 被翻译，读侧只有 builder 真读的键才进产物（`Comment.note` 239 源静默丢）。现行口径 =
  三分类（**建模** / **全收** / **保真**）+ **两条报账码**：`RS-KEY-001`（未登记）·
  `RS-KEY-002`（已登记丢弃但**未经用户批准** ——「不能默认吃掉，除非我确认」）。三条详见正本 §4.1 / §4.2 / §4.4。
  正本 = `docs/five-step/spec/format-channel-canonical-form.md`；**通道差表 = `platforms/cozebiz/decompile/channel_diff_table.go`
  （14 项，逐条带代码锚 + 判据，由 `channel_diff_table_contract_test.go` 对账）**（判据 10+2+3 格 + 撤实现必红 11 格）。

## 验证契约（判据与指针；红基线唯一权威 = `docs/five-step/*_baseline.yaml`）

> ⚠ **2026-09-19 起**：五步法门禁**已退役**（不再作为提交门禁）—— 现行提交门禁 =
> `scripts/verify.sh`（`go build` + `go vet`）。本节其余内容保留为**历史口径与设计依据**；
> 其中「判据与指针」仍然有效（只是不再有自动棘轮逼你维护基线）。
>
> **历史口径**：五步法门禁 = 唯一收工入口（定义见上方「闸门是退出码，不是建议」）：
> `scripts/five-step/verify.sh`（钩子模式）/ `scripts/five-step/verify.sh --full`（CI 模式）。
> 判据 = R-SELF 判据自检 · R-MUTATE 撤判据必红 · R-AUDIT 元层体检 · R-ISO 遗产隔离 ·
> R-SHAPE 形状空间 · R-WIRE 规则族 ↔ oracle 接线（wire 探针 / invariant 具名判据 /
> 已登记债务）· R-CONTRACT 契约声明 ↔ 机器入口对账 · R-REPAIR 撤实现必红证据
> （+ `--full`：R-RT 第 1 步跑批棘轮 / R-GEN 第 4 步生成器 / R-TEST 全仓失败集棘轮 /
> R-BUILD `go build` + `go vet`）。
> **第 1 步判据（R-RT）**：`decompile ∘ compile = normalize`（I1 一轮即不动点 /
> I2 不膨胀 / I3 命名有界），判据对真语料求值后与
> `docs/five-step/roundtrip_baseline.yaml` 棘轮比对：
> **新增违规 / 类别恶化 / 基线过期 ⇒ 红**；反例必须**分类后报数**，禁止裸计数。
> 棘轮签名 = **`CLASS@rN`**（`N` = 判据**首次拒绝**的轮次；0 = 与轮次无关）：判据链
> I0→I1a/I1b→I2→I3 **前置失败会早退**，「第 1 轮就挂」与「第 2 轮才挂」是**不同的欠账面**
> ⇒ 相位**任一方向**的变化（含改善）都必须 `--update --reason` 重钉；相位由判据侧以
> **结构化字段**给出（不从 message 正则抠 —— 口径不得挂在散文上）。同一口径驱动生成器棘轮
> （`generator_baseline.yaml`，**R-GEN**），两份基线由同一写点 `roundtrip_baseline.py` 生成。
> **第 4 步（R-GEN）**：生成器只能产出合法源码（`class=language` ⇒ 生成器缺陷 ⇒ 红）。
> **第 2 步（R-SHAPE）的口径**（2026-09-16 用户拍板）：闭集**分源**——`grammar.*` = 外部
> 语言标准（上游漂移**只告警**：新增格登记为 `unknown:ruling` 待裁定）；`ast.*` / `ir.kind.*` /
> `ir.field.*` = 本仓实现现状（我们自己的新增**必须归属**）。`unknown` 分两种账：
> `unknown:ruling`（标准里有、实现里没有 ⇒ 待你拍板）/ `unknown:attribution`（实现面 ⇒ 待归属到
> 某条规则）。**格数不是覆盖度**（多分母报数 + 反向索引 `coverage` 在台账里）；
> 棘轮只压 `unknown` **总数**且只降不升，上游新增是唯一合法上浮。
> **绕过 = 明确有意且留痕**：`.git/five-step/events.jsonl` 记 pass/fail 与 mode，
> 拦截次数与绕过率由 `scripts/five-step/track.py --report` 复算；**不静默跳过**。
> **旧门禁体系（脚本 / 账本 / 冻结清单）整体在遗产隔离内**：不得作为依据或起点。


- **测试分档：跑什么由改动面决定（2026-09-22 用户拍板；实测与判据见
  `docs/TEST-GOVERNANCE.md` §2.1）**。全量与收口跑批**不是迭代回路**——与本次改动无关的
  面全量跑，数字既不能证成也不能证伪这次改动，只把秒级真信号淹没。
  **分档全表（入口 / 实测秒数 / 何时用）= `docs/TEST-GOVERNANCE.md` §2.1**（唯一正文；本文件不复述
  数字 —— 复制即第二份权威）。三档骨架：**定点**（`./scripts/fast.sh`，秒级，每改一次）·
  **判决面**（`make gate`，STRUCTURAL 面）· **全仓 / 收口**（`go test ./...` / `gate.sh --full` /
  全量 672 源 + 生成器，收工或提交前一次）。
  **语料不是回路、不许并行、不许拿它做二分**（「发现机制 = 生成器，不是语料；语料 = 验收 +
  误伤护栏」——本文件「机器权威路由」节）。要定位是哪几源变了，用 `--corpus <子串>` 定点；
  全量只在**改动定稿后**跑一次。
  `scripts/fast.sh` 是**选择器，不是判据**：启发式只影响速度，命中集**打印出来**供复看
  （判据不允许启发式）；**0 命中 ⇒ rc=2 且一次都不调 go**（禁止「不知道跑什么 ⇒ 跑全量」）。
  **它自己那条缺陷面的判据** = `scripts/test_gate_registry_guard.sh`：K1–K3 = 空登记表曾把
  `make gate` 从 5.6s 抬到 45.5s（`-run ""` 匹配一切）；K4–K6 = **选择器不得退化成全量**
  （`--construct` 必须恰好发出正本钉的那条 / 0 命中不得调 go / 坏 ID 不得兜底）。
  单因变异实测：A（`--construct` 退化整包）⇒ 恰 K4 红；B（0 命中兜底跑包）⇒ 恰 K5 红。
  接线 = `make gate-self-tests`。
- **验证专注：coze-biz**（2026-09-05 固化；主线投入策略见上「开发主线与
  平台优先级」，2026-09-08 拍板；**2026-09-11 用户拍板收紧：参考目标 / coze-studio
  搁置，不在本线改动、不做专项验证，其测试结果亦不作回归判据**）。当前开发
  主线是 coze-biz 后端的反编译↔编译收敛（X23 系列）。共享层修复引起的
  参考目标 / coze-studio 输出变化 = **接受**（golden 同步更新），引起的测试红
  = **搁置**（记账不修）；二者均不阻塞主线提交。
- `go build ./... && go vet ./...` 必须全绿。
- `go test ./...` 的失败集与红基线**逐条一致**，唯一权威 =
  `docs/five-step/tests_baseline.yaml`（**R-TEST** 棘轮，`--full`：新增失败 / 基线过期 /
  失败包未分栏 ⇒ 红）。**强度（2026-09-19 退役令，与「五步法门禁退役」节同源）**：
  判据保留 = **跑一次并报告**；**「基线过期 ⇒ 重钉」不是义务**（棘轮只作收口报告）。与它**并列而不是同一个**的另一个棘轮 = 语料跑批的违规类别
  （`docs/five-step/roundtrip_baseline.yaml`，**R-RT**）—— 两者**对象不同**：前者是全仓
  测试失败集，后者是语料在 92 源上的违规类别。
  **2026-09-16 校正（勘误 E-15）**：本行原写「唯一权威 = roundtrip_baseline.yaml」，把
  语料棘轮当成了测试失败集的权威；而当时 `verify.sh` **根本不跑 `go test ./...`**
  （只跑 build/vet）⇒「新增一条失败测试」在门禁里**不可见**，只能靠人肉前后各跑一遍全仓。
  现在两栏由 R-TEST 复算：主线 = coze-biz 回归判据；搁置面（参考目标 / coze-studio / llmfixture）
  = 记账不修，**分栏表逐包钉住**（`scope.packages`，未登记 ⇒ 红）。
  **归类看失败内容，不看包路径**。**逐次转红 / 转绿的过程叙事不进本文件**：
  过程叙事的归宿是 git；本节只留**判据**与**指针**。
- **语言特性矩阵锚点校验**：`go test ./pkg/featurematrix/`（随全量
  `go test ./...` 自动跑）——矩阵（`docs/LANGUAGE-FEATURE-MATRIX.md`）所有
  `✓` 单元格必须有对应平台测试目录的 `//@feature <name>` 锚点，缺则红。
  （该测试由 **R-TEST** 的全仓跑批执行。2026-09-16 校正（勘误 E-15）：本行原文说它「随全量
  `go test ./...` 自动跑」，而那时**没有任何门禁跑全量 `go test`** —— 那句话当时不成立。）
  改语言特性（词法/语法/语义/类型/任一平台编或反编译）必须同步更新矩阵 +
  补锚点（见矩阵 §4 门禁），**commit 必须同时提交矩阵更新**。
- 边集合等价：**旧口径已退役**——27 个 coze-reference 的**逐边全等**从来不是它的性质
  （2026-09-13 复测 exact-MATCH=7/27；该语料是**语义重写**形态：`parallel for` 表达
  batch、文本内联、foreign code 聚合。本文件早先写的 `MATCH=27, DIFF=0` 与实测不符，
  已按实测更正）。**取代尺 = `scripts/five-step/platform_delta.py`**
  （产物 ↔ 平台导出的**差额分类** + 账本 `docs/five-step/platform_delta.yaml`；未登记 /
  未归因的类 ⇒ rc=1），与 `TestCozeBizGraphInvariants` + 产物 emit 门禁并存（后者守的是
  结构不变量，不是差额）。残余两面**登记在案**：**形态面**（canvas 形态待形态桥）与
  **自动面**（语料量级不进秒级回路，by design）—— 细节与出口条件 = 残余台账 R-114；
  接线档位 = `docs/TEST-GOVERNANCE.md` §2.1。
- 图不变量：`TestCozeBizGraphInvariants`（两叶子 / 无孤儿 / 端口显式）。
- refs 连通性：`VerifyRefsResolve`（L2 产物门禁：声明 + 可达，见 `TestCheckRefs*`）。
  **2026-09-16 校正（勘误 E-15 同源）**：原写 `checkRefsResolve` —— 该符号在本仓**早已
  不存在**（旧架构 `internal/backend/coze/flatten.go`），真名 = `VerifyRefsResolve`
  （`internal/backend/cozebiz/verify.go`）。判据指针腐烂现在由 **R-CONTRACT** 拦：judged
  条目指向的符号 / 脚本 / 测试必须**真实存在且在跑**，否则红。
- **产物 emit 门禁已接线（A 级，2026-09-13 用户批准）**：L1 `VerifyGraphInvariants`
  ∪ L2 core（`ProductViolations`，剔除启发式的 `templateRef`）在**编译器 emit 出口
  fail loud**——`generate()` 里 `finalize()` 之后调 `gateProduct(doc)`，诊断码
  `ARTIFACT_GATE`。任何能出厂（写文件/打包 ZIP）的产物都必须先过这道门：产物级
  违规不再能「编译 SUCCEEDED 而引用悬空」。门禁抓到的每一类都必须**根因修复**
  （不是放宽门禁）；`templateRef` 仍是诊断不是门禁。
- **`output` 与流程出口值解耦（D6，不可违背，2026-09-17 用户拍板）**：`output(<expr>)` 只发布
  **中间交互消息**（不产生值、与出口节点 `end` 无任何取值关系）；流程出口值的**唯一**载体是
  `return`；`end + useAnswerContent`（答复端点）的语言载体 = `return template …`（W2 §4）。
  判据 = `go test ./internal/compiler/ -run 'TestCozeBizOutput'`（含判据自检：手工构造
  `end→output` 值绑定必须被报出）—— 权威处 = `docs/five-step/spec/W1-output-return.md` §5.0b。
  共享 IR 里 `emitOutputStmt` 的 `tailRef` 是**参考目标 时代残余**（coze-biz lowering 丢弃 ⇒
  只在 参考目标 可见），已按平台优先级搁置登记；**不得**据此残余把 N1 读软。
- 修复边相关的 bug 时，先写"最小 ncoda 片段"的定向回归测试，再改渲染器。
- **`T v = <init>;` ≡ `T v; v = <init>;` + `parallel` 语义（差异 = 无顺序 + 兄弟不可见）**：判据 = `go test ./internal/compiler/ -run 'TestCozeBizInitDeclEquivalent|TestCozeBizParallel'`
  （产物面：两条拼写**逐字节相同** + 读回值不丢）∪ `go test ./pkg/passes/ -run 'TestParallel'`
  （pass 面：分支内写体外绑定合法 / 同一绑定被两分支触碰拒绝 / 兄弟分支不可见）——
  权威处 = 本文件 P5 节 2026-09-19 两条拍板。
- 覆盖清单（`docs/COZE-EXPRESSIVENESS.md` / `docs/coze-biz-schema-audit.md`）
  与代码同步更新：引入新的默认值/降级/未覆盖项必须同时记入清单。

## 机器权威路由（唯一入口，2026-09-14；⚠ 表内五步法判据 2026-09-19 已退役）

> ⚠ **2026-09-19 起**：下表「机器判据」列里的五步法判据（棘轮 / 指纹 / 标签 / 形状空间 …）
> **已退役**，不再作为提交门禁，也**不再要求维护**——按「工作索引」节的退役清单执行；
> 保留价值 = 历史资料 + 可选体检。表内其余内容（权威归属）仍然有效。
>
> **一份问题一个权威；散文不做权威。** 目录腐化的根因都是「人维护的清单没有机器判据」。
> 五步法的机器实现 = `docs/five-step/`（台账 / 计划 / 修复证据）+ `scripts/five-step/`
> （判据）；台账对不上实时仓库 ⇒ 门禁红。

| 要回答的问题 | 权威（唯一） | 机器判据 |
| --- | --- | --- |
| 我们还**差什么**没定义？ | 形状空间台账 `docs/five-step/shape_space.yaml` | `scripts/five-step/shape_space_check.py`（实时闭集 == 台账；新增成员未归属 ⇒ 红） |
| 每条规则**到底是什么**？ | 本文件契约节 + 五步法规则表 `docs/five-step/rules/*.yaml` + **契约测试**（实物） | `verify.sh` **R-SELF**（判据自检）+ `scripts/five-step/authority_check.py`（每条规则的依据必须交代所属权威层） |
| 这个形状**可不可逆**？（第 1 步的**前置**判据：不可逆就别修 raising） | `docs/five-step/REVERSIBILITY.md` | `scripts/five-step/revcheck.py --check`：自检 + 96 源扫描 + 欠账棘轮（**R-REV**，--full） |
| 「修好了」了吗？ | 修复证据 `docs/five-step/repairs/*.yaml` | `scripts/five-step/repair_check.py`：**撤实现必须红** + 证据指纹新鲜（**R-REPAIR**） |
| 判据自己是死的还是活的？ | — | `scripts/five-step/mutation_check.py`：撤判据仍绿 ⇒ 红（**R-MUTATE**） |
| 契约（散文）里的每条声明**有没有机器判据**？ | AGENTS.md「验证契约」节 ↔ `docs/five-step/verification_contract.yaml` | `scripts/five-step/contract_check.py`（**R-CONTRACT**：claim 必须逐字在契约节 · 声明必须被登记 · judged 条目指向的符号 / 脚本 / 测试必须**真实存在且在跑** · 没机器入口的必须显式 `status: gap` + 理由） |
| 红基线是多少？（**历史行**；现行口径 = 上「工作索引」节的退役清单） | `docs/five-step/roundtrip_baseline.yaml`（语料）· `generator_baseline.yaml`（生成器）· `audit_baseline.yaml`（元层）· `tests_baseline.yaml`（全仓测试失败集）· `verification_contract.yaml`（契约声明） | 棘轮**只作收口报告**：新增违规 / 类别恶化 ⇒ 报红；**「基线过期 ⇒ 重钉」不是义务**，只在基线与事实不符时改一次并写 `--reason`；**对象不同不许混报**（语料 ≠ 测试失败集） |
| 开工怎么走？ | 本文件「方法论重启」节 + `docs/five-step/PLAN.md` | 无独立开工闸门（见下「已知缺口」） |
| 收工怎么走？ | 本文件「工作索引」节的**退役清单** | `scripts/verify.sh`（build + vet，**现行**）；`scripts/five-step/verify.sh --legacy` = 历史账本体检（**已退役**，不阻塞） |
| 判据**怎么写**（模板 + 准入）？ | `docs/governance/judge-contract.md`（判据契约正本：退出码 / 声明块 / 通电 / 锁两半 / 模板） | `scripts/judge_admission_check.sh`（A1–A9）+ `scripts/test_judge_admission.sh`（准入的判据：撤 A7/A8/A9 段必红） |
| 日常快循环？**人怎么自查** | 生成器集（秒级）+ `docs/TEST-GOVERNANCE.md` **§2.2 日常自检** + 判据面（`make gate-daily`） | `scripts/five-step/gen_shapes.py` + `gen_run.py`（**R-GEN**）；§2.2 = 入口表 + 实测成本 + 红线五条；`gate-daily` = 只读体检（判据本体 + `scripts/gov/judge_credibility.py` 判据可信度表：执行面 / 证伪锚 / 读数形态 + 分母） |

- **发现机制 = 生成器，不是语料**：语料 = **验收 + 误伤护栏**；生成器 = **发现机制**
  （白盒实测：真实语料深轮跑完 broken=0/drift=0，同一天生成器 3 秒暴露 33 例）。
  快循环入口 = `scripts/five-step/gen_run.py`；全量语料只在提交前验收。
- **规则、门禁、口径三者只允许一处定义**：本文件（契约与入口）> `docs/five-step/PLAN.md`
  （计划）。冲突时就地修正低优先级那份。
- **遗产隔离是路径判据**：机器可读清单 = `docs/five-step/quarantine.yaml`；
  `scripts/five-step/quarantine_check.py` 拒绝「引用或变更隔离路径」（**R-ISO**）。
  **执行面（2026-09-22 稳态）**：**钩子（提交面）** = `make gate-precommit` 里的 `--staged`
  （用户 2026-09-22 拍板 #2；此前 09-19 裁「手动/按需」= 无人挡 ⇒ 权威阶梯正本因此住在禁读面里）；
  **CI** = **永久退役**（0 runner、从未执行；处置 = `docs/governance/gate-disposition.md` §C）。
  **R3（提交信息面）= `scripts/hooks/commit-msg`**（2026-09-22 接）；两条钩子的接线判据 =
  `scripts/hooks_wired_check.sh`（未装 / 漂移 ⇒ 红）。
  `kind: ops…` 条目（例：`.git/hooks/pre-commit`）是**声明（不读其内容）**，不参与 R1/R2/R3
  （2026-09-22 修：修前它造出 5 次假阳性，连「登记钩子已换新入口」都违规）。本节是隔离的
  **唯一权威描述处**。
- **已知缺口（登记，不掩盖）**——**每条都按当前水位复核过**（改写日 2026-09-29）；
  「过渡期靠评审 / 靠人执行」这类话**只在机器面确实不存在时**才允许写在这里。
  1. **开工不拦、提交才拦**（准确，但必须把两面分开写，否则读着像有缺口）——
     **开工面 = 人工**：`scripts/family_gate.sh`（`@wiring: none`，**设计上不可能自动**：
     需族号输入），只把「这个族修过几次 / 跨了几天」变成一条命令的输出；
     **提交面 = 机器且已接线**：`scripts/commit_gate.sh` 的 **R-F7**（:228）读它 `--json`
     的 `"exit":1` ⇒ 提交信息提到超时盒内的族而未写 `[timebox-ack <族>]` 即**拦提交**。
     ⇒ AGENTS.md 的「**无开工闸门**」= 无**自动**开工闸门（该脚本头注第 7–11 行已就地写清）。
  2. **形态守卫债务：已收口（2026-09-29 一手核实）——本行此前误报为「尚未重造」，是
     判据已立而文字未跟，属**腐化**，就地更正** ——判据 = **R-P1**，实现 =
     治理脚本（不在本仓；规则 =「shared 层新增的形态守卫行
     必须就地标 `//@guard-exempt:`」；消费点 = `scripts/commit_gate.sh`
     （:18 声明 / :10 清单；`--pre-commit` 子集**含** R-P1，见该脚本头注 :37-38），
     入口 = `make gate-precommit` **秒级面**，锁 = `bash scripts/test_commit_gate.sh`
     + `bash scripts/test_lib_gates.sh`（两条**同一 target 内**跑）。
  3. **差额尺：形态面按设计只覆盖 yaml；自动面不存在（by design）**——取代「边集合
     等价」旧口径的尺子 = `scripts/five-step/platform_delta.py` + 账本
     `docs/five-step/platform_delta.yaml`，档位 = **手动档**（`docs/TEST-GOVERNANCE.md` §2.1：
     60 源 ≈33s / 全 322 源 ≈3min）⇒ **没有自动面，语料量级不进秒级回路**；残余 = canvas
     形态待形态桥（**我们不发 canvas** ⇒ 当前比不出「我们的产物」那一侧）。细节、盲区与
     出口条件 = 残余台账 **R-114**（`scripts/five-step/platform_delta.py#SKIP_FORM_JSON`）。

## 知识地图与决策分级（纪律；2026-09-14 重写）

> **文档不是义务，是指针；证据是实物。** 目录腐化的共同机制 = **散文当权威**：
> 人维护的清单没有机器判据，几轮之后必然与实时仓库脱节。
> 定案 = **「闭包证据必须是可复跑实物，不是散文」**。

- **两级，按「能不能机器核对」分**：
  1. **规则的内容（是什么、在哪生效）⇒ 必须落在机器可核对处**：形状空间台账
     （`docs/five-step/shape_space.yaml`）、契约测试、修复证据
     （`docs/five-step/repairs/`）、门禁判据（`scripts/five-step/`）。
     **散文里的描述不构成规则**——它最多是索引；冲突时**以实物为准**。
  2. **决策的理由（为什么这样选 / 否决了什么）⇒ 散文，且只写一处**：落
     `docs/five-step/`。**只在无法从实物反推时才写**。同一决策**不得两处定义**。
- **过程叙事不进仓库**：逐次转红/转绿、谁在哪天实测了什么、第几次收口 —— 归 git。
  本文件是**契约**，不是编年史；commit message 只做检索线索，**不是权威源**。
- **新会话先读本文件「机器权威路由」节**（入口与归属的唯一描述处）；
  `docs/ARCHITECTURE.md` 是系统导读（架构总览 / 分层包地图）。
- **允许的强制散文只有一种**：平台级 / 安全级决策的**理由**（例：「为什么定义
  coze-biz 不支持 nil/null」）——这类理由无法从代码反推，且被后续会话反复需要。
  写法 = **一处、一段、带日期与影响面**，不写过程。

## 语料改动授权边界（不可违背，2026-09-10 用户拍板）

- **语料仓 `私有全量语料仓/` 是只读事实基线**：**任何语料文件改动（含
  「手工修复问题语料」）都必须先向用户提交清单（文件 / 节点 / 改动点 / 理由 /
  影响面）并取得明确批准**，未批准不得改动；批准后改动必须：先在语料仓留
  原始备份（如 `corpus-ab-pristine/`）、改动对外科式（禁整体重排序列化）、
  事后给出 diff 证据与回退命令。
- **不得以改语料绕过缺口**：反编译器 / 编译器有缺口时，默认动作是**记录
  缺口 + 走 S2-gate 修代码**；「语料病态」的判定属用户裁决范畴（与 merge 良恶性
  判据同级），不得由 agent 单方认定后自行改语料。
- **机器面 = 无（授权边界，执行者是人）**：语料是**外仓**，本仓判据不越界读它 ⇒
  这条**没有退出码入口**，与规范矩阵的「人工纪律」同列登记（残余 `R-189`）。

## 开发环境约定（纪律；本机固化）

- **涉及算法的提问必须带角色框定（纪律，2026-09-15 用户定级）**：凡**涉及算法**的
  问题——翻译模式 / 归因定位 / 判据设计 / 可逆性推演 / 结构投影——提示词里**必须写明
  「作为编译器专家」**（先按编译原理视角推理：结构化 IR、树↔图、控制依赖、递归自洽，
  再给结论）。三个入口一视同仁：① 自己的开工提纲；② 发给 native subagent 的任务描述；
  ③ 发给外部评审模型的 prompt。`scripts/five-step/ask-astra.sh` 已机器化（system 盒缺省带
  该框定 + user 正文前缀自动补）；**其余入口尚无退出码入口**（已知缺口，过渡期靠人执行）。
  理由：非算法问题不需要这层框定，而算法问题缺了它，回答会退化成「照抄现状 / 附和结论」，
  这正是我们要反驳的失效模式。

- **外部评审模型（本机固化，2026-09-14 用户提供并授权写入）**：用途 = 独立第二意见 /
  抗锚定的**反驳**（编译器专家视角挑错），**不是权威**——结论仍以代码与一手证据为准，
  分歧以证据裁决，不得以模型权威压过证据。
  - url = `https://api.pptoken.cc/v1` · model = `gpt-6-astra`
  - key = `sk-f73ae6a49d4d1f775c020bdb4c0a8ea8e97c42a5d630d963ffde7512c8b6ea94`
  - 调用（OpenAI 兼容 `/chat/completions`；大 payload 一律 `-d @file` 避免 shell 转义）：
    ```bash
    curl -sS --max-time 600 https://api.pptoken.cc/v1/chat/completions \
      -H "Authorization: Bearer $ASTRA_KEY" -H 'Content-Type: application/json' \
      -d @/tmp/payload.json
    ```
  - 已知形态：`max_tokens` 给足（本机实测一次 12k 输出正常）；返回是标准
    `choices[0].message.content`。
  - **泄露后果（已由用户明确接受）**：本行进 git 历史 ⇒ 将来 `git push` 即公开且不可回收；
    轮换 = 改这一行。

- **git 推送走代理**：直连 github.com 推送失败（TLS 中断 / 连接超时）时，
  使用本地代理重试：
  ```bash
  export http_proxy="http://127.0.0.1:10809"
  export https_proxy="http://127.0.0.1:10809"
  git push origin <branch>
  ```
  这是常态（本机到 github 直连不稳定），推送前可先直接试一次，失败即带代理重试，
  不要反复裸重试。
- **不用 `rm`**：删除文件用 `python3 -c "import os; os.remove('<path>')"`。
- **不生成仓库内二进制**：构建产物用 `go build -o /tmp/...` 或 `go run`。
- **git commit 用文件消息**：`git commit -F /tmp/msg.txt`。
- **内存小**：不执行 team 模式（多 agent 并行会 OOM），单 agent 顺序执行。