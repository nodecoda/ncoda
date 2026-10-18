# PARITY — 与 Python 参考编译器的行为对齐契约

> 日期: 2026-08-22（落盘）| 范围: `本仓（拆分前）` 全链路
> 背景: ncoda（Go 生产编译器）逐函数移植自 Python 参考编译器
> （ncoda-py `src/nclang/` + ncoda-dify），移植纪律要求**可观察行为字节对齐**。
> 2026-08-19 **Go 毕业**：双 CLI `golden_diff` 退役，字节一致性由
> `scripts/shadow_parity.py`（Go/Python 双跑）与 `scripts/contract_test.sh`
> （契约快照 sha256 + 逐字节 cmp）接棒锁定。
>
> **本文是 parity 锁定的唯一索引**（此前散落在各文件头注释，被
> `parser.go` / `code_analysis.go` / `docs/ideas-todo/TOOL-DIRECT-CALL-FORM.md` 引用）。

## 1. 锁定面（不可随意改动，改 = 违约）

| 锁定面 | 约束 | 证据位置 |
|--------|------|----------|
| 诊断码与消息文本 | 全部错误/警告消息**逐字**等于 Python f-string 结果 | `passes/*`（E1035/E1036 parallel、scope/type 消息）、`verifier`（32 规则 E1099 逐字） |
| 诊断 wire 形态 | JSON code/severity/location 与 `compile_cli.py` 字节一致；**parser 路径诊断码为裸 int**（`NumericWire`），lexer/pass 为枚举名 | `internal/compiler/compile.go` |
| golden YAML | 产物与冻结快照 **sha256 + 逐字节 cmp** | `scripts/contract_test.sh`（`contracts/nclang-compile-v1.json` golden_snapshots） |
| Go/Python 双跑一致性 | 同一输入两实现产物字节一致 | `scripts/shadow_parity.py` |
| ID 序列 | `AstNodeID` 按源码 offset 排序的 DFS 前序枚举（每编译独立 mint）；`SymbolID`/`FunctionID` 链内确定性计数 | `pkg/binding/binder.go`（enumerate）、`pkg/binding/ids.go` |
| SymbolID 偏移 | 参考中 `extract` 未注册使 Go 侧 SymbolID 偏移偏差**窄化到 1**；**禁止任何 "+1" 补偿** | `binder.go:26-36` |
| 边顺序 | 后端边集合 = 结构深度优先 = legacy `AddEdge` 顺序；coze-biz 27 语料逐边一致（MATCH=27, DIFF=0） | `pkg/backend/flow/project.go`、`scripts/coze_biz_real_export.sh` |
| 深度边界 | 嵌套接受阈值与 Python `RecursionError` 边界一致：expr/paren/call ~88、stmt block ~128，超限 = `ADAPTER_ERROR` | `pkg/parser/parser.go`（depth guard）、`depth_guard_test.go` |
| 冻结消息 | 历史消息逐字保留（如 `TOOL_REGISTRY_HINT` 含已废弃 `--tools` 字样）——差分一致性优先于措辞现代化 | `internal/compiler/api.go`、`docs/DIFY-COMPILER-LINK.md` |
| `UnsupportedError` 前缀 | 错误消息保留历史 `coze: unsupported construct:` 前缀（wire 契约） | `pkg/ir/build.go` |
| 编译器版本 | `compilerVersion` 原镜像 `nclang.__version__="0.1.0"`；oracle 退役后改跟 v0.4.0 里程碑，**bump 是发布动作**（须与契约快照一起重冻结） | `internal/compiler/api.go` |

## 2. 诚实偏差（honest deviations，已声明并锁测试）

| 偏差 | 原因 | 锁定 |
|------|------|------|
| 深度守卫阈值硬编码 | Python 帧深度实现相关，Go 无解释器栈限制；用计数器在**同一边界**触发 `ADAPTER_ERROR` | `parser/depth_guard_test.go`（dp_88/89、deep_calls_88/89、ifp_121/122） |
| verifier V14 受限 Python token 扫描器 | 用小型 tokenizer 替代 `python_ast.parse`；只覆盖 Go 生成的代码形状，**未匹配即跳过（漏报不误报）** | `verifier/scan.go` 头注 + `verifier_test.go` |
| code FFI 无字节级 parity | Python AST 检查仅验证 `main` 参数与可见返回键，不推断/修改声明类型；Go 只要求确定性 | `binding/code_bind.go:22` |
| coze-biz 单遍投影 | `cfCollector` 单遍边收集替代独立双遍 `buildCFG`（后者丢失 `refsLoopVar` 信息）；双遍迁移被设计文档否决 | `docs/EDGE-CONTRACT.md` §5、`docs/COZEBIZ-SINGLE-PASS-PROJECTION.md` |
| Scope 双表（symbols/records） | 镜像 Python 双面（identity + record），binder 路径同步写保证一致 | `binding/scope.go` 头注 |
| 死保留字 entry / stream / \|> 移除（A1） | Go 生产侧删除无产生式的 `KW_ENTRY`/`KW_STREAM`/`PIPE` 及关键词表项；`entry`/`stream` 回归普通标识符，`\|>` 与裸 `\|` 改为词法错误（`\|>` 原提示消息指向已删除的管道记号，同步更新）。Python 参考（`tokens.py`）为冻结归档快照，不回溯修改；原 `dify-dsl.y` 已退役删除，语法事实源迁移至 [nodecoda-spec `spec/grammar.ebnf`](https://github.com/nodecoda/nodecoda-spec/blob/main/spec/grammar.ebnf) | `token/token.go`、`lexer/lexer.go` + 回归测试（`lexer_test.go`、`grammar_test.go`） |

## 3. 例外流程（改锁定面时）

1. **先声明**：在本文"诚实偏差"登记，说明为什么偏离参考；
2. **先冻结**：任何影响 golden YAML/诊断消息/ID 序列的改动，先跑
   `scripts/contract_test.sh`（sha256 断言）确认影响面；
3. **双跑验证**：涉及前端行为时跑 `scripts/shadow_parity.py`（oracle 可用时）；
4. **回归锁定**：改完补定向测试（最小片段），禁止"碰巧通过"的宽测试。
5. **版本联动**：若涉及 wire 契约/版本字段，`compilerVersion` bump 与
   `contracts/nclang-compile-v1.json` 重冻结必须同 commit。

## 4. 退役记录

- **2026-08-19 Go 毕业**：Python oracle 退役，双 CLI `golden_diff` 不再运行；
  产物 sha 稳定性由 `contract_test.sh` 契约注册表（`golden_snapshots` sha256）保证。
- `compilerVersion` 的 parity 理由消失，字符串语义改为里程碑跟踪
  （`docs/RELEASE-v0.4.0.md`）。
