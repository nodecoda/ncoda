# ncoda 系统架构（开源内核）

> 本文描述**当前**开源仓的架构。平台适配器（编译/反编译、平台身份、平台夹具）
> 在商业仓；本仓不含任何平台名（判据：`scripts/audit_split.py` S6/S7）。
> 项目定位见 `README.md`；语言规范（语法/语义/IR）的正本见
> [nodecoda-spec](https://github.com/nodecoda/nodecoda-spec)。

## 1. 一句话定位

开源仓 = **可运行的 ncoda DSL**：语言前端 + 中立 IR + runtime + 公共库 +
语言级判据。`.ncoda` 源码在这里就能编译、检查、运行 —— 不需要任何平台。

商业价值在**互转**：平台适配器把中立 IR 投影成平台产物、把平台产物还成 ncoda，
那是商业仓的资产，不是语言定义。

## 2. 管线

```
.ncoda 源码
   │
   ├─ lexer / token          词法（$ 转义、关键字、字面量）
   ├─ parser / ast           语法（声明、语句、表达式、类型引用）
   ├─ binding / types        名字解析、作用域、类型检查、契约绑定
   ├─ passes                 语言判据（语义 / 类型 / 目标承载报账 / 能力门禁）
   ├─ ir                     ncoda-ir/v1：中立图（节点 + 端口 + 位置）
   │
   ├─ irdump                 IR 审计 dump（唯一正本，跨仓共用）
   ├─ codeparse              code 节点的 python 子集解析/校验
   ├─ pygen / pyast          code 节点的生成与翻译
   ├─ ncoda（stdlib 面）     内建节点、库、渲染公共面
   │
   ├─ backend/flow           图投影（节点/边/执行序）
   └─ runtime                执行器：spec.v1 的宿主语义
        ├─ spec               图规格（节点配置 DTO、连线、sidecar）
        ├─ compile            投影 → eino 图（唯一图执行引擎）
        ├─ nodes              节点语义（start/end/llm/http/code/loop/qa/agent/…）
        ├─ state / boundary   迭代状态、边界策略（prefill/merge/zero-nil）
        ├─ plugin / naming    工具注册与名字规则
        ├─ trace / diag       语义 trace、运行时诊断码（3xxx）
        └─ host               宿主物理字段投射
```

目标相关的承载事实（哪些轴装得下、线拼写是什么）由适配器通过
`passes.TargetProfile.Carrier` 与 `capability.Table` **显式声明**；未声明 =
什么都不声称承载（内核不按名字猜，也不认识平台名）。

## 3. 四层判据（分开判，不互相兜底）

| 层 | 问题 | 判据 | 红了怎么办 |
|---|---|---|---|
| L1 规范 | 语法/语义/类型/IR 定义清楚且自洽？ | `make conformance` + spec 条款 | 改规范或实现，不拿平台行为兜底 |
| L2 runtime | 一份 ncoda 不依赖平台能跑起来？ | `ncoda run` + runtime 语义判据 | 补 runtime，不许说「平台没有所以不支持」 |
| L3 语料 | 真实世界的形状扛不扛得住？ | 语料仓执行账（ok / with-input）+ 闭集归因 | 分清我们的 bug / 语料自身错 / 环境缺失 |
| L4 适配器 | 与平台互转是否保真？ | 适配器仓的 roundtrip 不动点 | 算适配器问题，不回写语言 |

一句话：**spec 是法律，runtime 是执行，语料是考试，适配器是翻译插件。**

## 4. 包地图

| 包 | 职责 |
|---|---|
| `pkg/lexer` `pkg/token` | 词法、记号、关键字与转义 |
| `pkg/parser` `pkg/ast` | 语法树 |
| `pkg/binding` `pkg/types` | 作用域、符号、类型系统、契约绑定 |
| `pkg/passes` | 语言判据管线；`CarrierProfile` 是适配器注入承载事实的唯一入口 |
| `pkg/ir` `pkg/irdump` | 中立 IR 与审计 dump（`ncoda-ir/v1`） |
| `pkg/capability` `pkg/target` `pkg/obligation` | 能力轴、类型映射面、lowering 义务注册表 |
| `pkg/codeparse` `pkg/pyast` `pkg/pygen` | code 节点的解析、生成、翻译 |
| `pkg/ncoda` `pkg/builtin` | 内建节点、库单元、公共渲染面 |
| `pkg/compiler` `pkg/cli` `cmd/nclang-compile` | 语言 lane：`--emit-ir --target __none__` |
| `pkg/runtime/...` | 执行器（L2）：spec.v1 → eino 图 → 运行；含节点语义、状态、trace |
| `pkg/observation` | 反向抽取视图（产物节点/边 → 义务报告），与目标无关 |
| `pkg/runner` `cmd/ncoda-run` | 运行时 lane：`.ncoda` 源码 → 执行结果（离线 echo 模型） |
| `pkg/localtools` | 公共库的本地确定性实现（database/matting/image_scale/image_generate），产物落 `--artifact-dir` |
| `pkg/toolbridge` | 插件注册表 → 语义注册表的投影（避免 `pkg/tool` ↔ `pkg/runtime/plugin` 成环） |
| `pkg/toolpath` | 能力头搜索路径：`--tool-path` > `NCLANG_TOOL_PATH` > `<exe>/libraries` > `~/.nodecoda/tools` |
| `libraries/` | 174 个公共能力头（语言级符号；不含平台物理 id） |
| `pkg/diag` | 诊断码与线格式契约 |

## 5. 执行引擎

runtime 的图执行基于 `github.com/cloudwego/eino`（Apache-2.0）：它是**通用图
执行库**，不含任何平台事实。语言语义（节点行为、状态、错误策略、trace）全部
在本仓 `pkg/runtime` 里实现；eino 只负责调度与 checkpoint。

这条边界是判据而不是口号：`scripts/audit_split.py` S2 禁止 runtime 的依赖闭包
出现商业模块；`go list -deps ./cmd/ncoda-run` 里没有任何 `ncoda-commercial`。
新增第三方依赖必须走 S5 许可闸门并进 `THIRD-PARTY-NOTICES.md`。

## 6. 工具链

- `make check`：fmt / vet / build / 契约 / 通告 / 测试。
- `make conformance`：语言级一致性判据（L1）。
- `make push`：语言 lane + 运行时 lane 烟测（L1 + L2 的最小闭环）。
- `ncoda-run <file.ncoda>`：不接平台、不联网，直接跑一份源码。
- 语料执行账：`ncoda-corpus` 仓 + 本仓的语言 lane 二进制（L3）。
