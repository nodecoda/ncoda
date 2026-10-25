# 语言缺口登记簿（LANGUAGE-GAPS）

状态：**active** · 适用仓：`nodecoda/ncoda` · 判据账本：`../ncoda-commercial/docs/language/`（只读引用）

本文登记**本仓**发现的语言面缺口/待裁定项。它不是判据账本（账本权威在商业仓，
本会话只读），而是一份可执行的工单：每条都有机器复现、归因、出路与关闭条件。

## 0. 归因闭集（与 LIBRARY-BINDING-SPEC §8 同构）

`我们的 bug` · `规范未定义` · `规范已裁定（行为正确，缺文档/缺特性）` · `环境输入缺失` · `平台适配`
落不进就新立一类，**不许 other**。

## 1. 条目格式

| 字段 | 说明 |
|---|---|
| ID | `N-LANG-<n>`，不复用 |
| 标题 | 一句话 |
| 归因 | 闭集之一 |
| 现状 | 现在会怎样（含 diag code / rc） |
| 复现 | 机器判据（测试文件:函数） |
| 影响 | 谁被挡住 |
| 出路 | 选项 + 取舍 |
| 关闭条件 | 怎样算关掉（必须可判） |

---

## N-LANG-5 · record 值不能进入数组/容器字面量

| 字段 | 内容 |
|---|---|
| **归因** | **规范已裁定（A12-H4，2026-09-17 用户裁定 (b)）** —— 不是实现 bug |
| **现状** | 多端口 record（如插件/工具结果）放进数组字面量、或作为工具参数整体传参，IR 直接报 `RECORD_FIELD_REQUIRED`（diag 1056）：`record 值没有单一承载口…平台上没有「整个 record」这个端口`。rc=1（运行期 lane）/ 编译错误（语言 lane） |
| **复现** | `pkg/ir/record_in_container_gap_test.go`：`TestNLang5RecordInContainerIsRejected`（现状断言，修复即变红）+ `TestNLang5FieldAccessIsTheRemedy`（正控：按字段取用可编译） |
| **影响** | 写库调用链时无法把 `let u = lib.fn(...)` 直接塞进 `[u, u]` 或透传给下一个库；必须绕道 `let one = u.outputList; let us = [one];`。插件作者与语料迁移都会撞到 |
| **出路** | (a) **维持裁定**：补文档 + 文案（文案已指向字段/出口两种写法），并给 L2 侧一个「取字段」惯用法示例；(b) **新增语言特性 records-as-values**：需要 L1 条款（容器元素可为 record）+ IR 承载（单一载体端口）+ 至少一个平台的投影证据；代价是跨层改动，且与画布模型（record 成员=端口）冲突 |
| **关闭条件** | 选 (a) ⇒ 文档与文案落地，且本条目状态改为 `accepted-limitation`；选 (b) ⇒ L1 条款 + IR 承载判据 + 平台投影证据同时存在，`TestNLang5RecordInContainerIsRejected` 反向改写为 `…IsAccepted` |

### 裁定理由（为什么现在不是 bug）

IR 的整值引用判据（`pkg/ir/whole_value_refs.go`）已经在**有承载形式**的位置开了口子
（容器产出绑定、流程出口逐字段具名）。数组字面量在目标平台上没有「整 record」这个槽，
若前端放行，产物会出现无处落地的值 ⇒ 落成静默丢值，比报错更糟。
因此当前行为属于「规范已裁定」，登记目的是把「要不要新增 records-as-values」变成
可决策项，而不是把它当缺陷偷偷修掉。

---

## 已知相邻条目（账本在商业仓，只读）

| ID | 摘要 | 本仓状态 |
|---|---|---|
| N-LANG-3 | `64_subflow_decl` kernel IR 类型检查失败 | 未收口（IR typing 缺口） |
| N-LANG-4 | `--target __none__` 下 `TargetValidationPass` 用空 capability 表拒绝 for/loop/attempt | 未收口（平台能力泄进前端） |
