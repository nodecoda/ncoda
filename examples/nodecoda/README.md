# NodeCoda Workflow Language 示例库

编号 `00` 至 `38` 的 `.ncoda` Source 是从真实工作流抽象出的语言样例，展示 Source 到 IR / 目标产物的映射规则。
`39` 至 `44` 是语言和 lowering 契约样例，`45` 起是部署能力与 reviewed contract 示例。完整语法见 [nodecoda-spec `spec/grammar.ebnf`](https://github.com/nodecoda/nodecoda-spec/blob/main/spec/grammar.ebnf)(单一事实源)。

> 验收语料（869 例）是独立仓 `ncoda-corpus`；本目录只放语言样例，不放平台夹具。
> 能力头文件在仓根 `libraries/`（`import "database";` 由它解析）。
>
> **平台无关（2026-10-25）**：本目录全部示例只使用语言构造。需要能力头时写**裸
> provider 名**（`import "google_search";`）—— 平台限定符由适配器在构建时解析，
> 开源仓不认识任何平台名。平台夹具、往返证据与目标产物示例住在适配器仓
> （商业仓 `internal/backend/*`）。

## 编译状态

| 文件 | 模式 | 语法 | 语义 | 说明 |
|------|------|------|------|------|
| 00_minimal | Start→LLM→End | ✓ | ✓ | 可编译 |
| 01_simple | Start(var)→LLM→Answer | ✓ | ✓ | 可编译 |
| 02_rag | Knowledge→LLM | ✓ | ✓ | 可编译 |
| 03_conditional | IF→LLM | ✓ | ✓ | 可编译 |
| 04_code | Code→LLM | ✓ | ✓ | 可编译 |
| 05_http | HTTP→LLM | ✓ | ✓ | 可编译 |
| 06_tool | Tool→LLM | ✓ | ✓ | 平台无关的 tool 绑定示例（裸 provider 名） |
| 07_template | Template+Conditionals | ✓ | ✓ | 可编译 |
| 08_parallel | Parallel { LLM, LLM } | ✓ | ✓ | 普通 DAG fan-out/fan-in |
| 09_iteration | FOR→LLM | ✓ | ✓ | plain ForStmt 生产启用 |
| 39_code_typed_direct_output | Code direct result | ✓ | ✓ | 单输出直接值 |
| 40_code_typed_structural_output | Code structural result | ✓ | ✓ | 多物理输出端口 |
| 45_iteration_yield | value For + yield | ✓ | ✓ | 三次 runtime 运行输出 `[2,4,6]` |
| 46_loop_escape | For break/continue/return | ✓ | 关闭 | API 输出正确但 Console 证据不干净，返回 E1034 |
| 47_parallel_product | named parallel product | ✓ | ✓ | 五次 runtime 运行稳定 |
| 48_response_accumulation | 多 End response | ✓ | ✓ | 三次 runtime 运行通过 |
| 49_for_statement_plain | plain statement For | ✓ | ✓ | 三次 runtime 运行，每项执行一次 body |
| 50_condition_direct | direct condition | ✓ | ✓ | 原生 condition operator |
| 51_condition_calculation | calculated condition | ✓ | ✓ | typed Code materialization 后判断 |
| 57_parameter_extractor | schema-dependent extraction | ✓ | ✓ | flat required prompt shape；必须检查 `.ok` |
| 62_local_library_roundtrip | database insert → select | ✓ | ✓ | 只用公共库；`ncoda-run --local-plugins --tool-path ../../libraries` |

**已实现语义功能**：
1. ✓ Start 输入映射为 `function main(...)` 的强类型参数
2. ✓ `knowledge()` 内置函数
3. ✓ 强类型 `foreign code python3(...) -> type { source ...; }` 表达式
4. ✓ `http()` 内置函数
5. ✓ 限定名工具调用 `p.op({...})`（`import "p"` 注册命名空间；能力调用不再使用内置函数形态）
6. ✓ `LLMResponse.text` 真实输出端口
7. ✓ `HTTPResponse` 结构体（body, status_code, headers）
8. ✓ 隔离 named/plain `parallel` lowering；生产 target 使用普通 DAG，不生成 synthetic 节点
9. ✓ `extract<T>` schema-dependent operation，返回 `value`/`ok`/`reason`/`usage`
10. ✓ `request_input<T, Action>` 挂起 + 穷尽恢复分支（approve/reject/timeout，Task 38；语料 61_request_input）

## 映射规则

### 节点类型映射

| YAML node.type | DSL 构造 |
|----------------|----------|
| `start` | `function main(type name, ...)` 参数 |
| `llm` | `llm("provider/model", { messages, ... })` |
| `if-else` | `if (cond) { ... } else { ... }` |
| `iteration` | `for (item in items) { ... }` |
| `knowledge-retrieval` | `knowledge("dataset_ids", query, opts)` |
| `code` | ``foreign code python3(type name = value) -> output-type { source `...`; }`` |
| `http-request` | `http("method", url, opts)` |
| `tool` | `p.op({...})`（`import "p"` 注册命名空间后限定调用） |
| `template-transform` | 模板字符串 + 条件表达式 |
| `variable-aggregator` | 仅用于互斥分支值合并 |
| `output` | 中间消息发布 `output(expr)`（非终止；`answer(...)` 语句已移除，2026-08-23） |
| `end` | `return value`（结构化返回） |

### 强类型 Code FFI

Code 输入必须同时声明端口类型和绑定表达式；声明类型是静态分析、目标端口和本地运行时校验的唯一依据：

```dc
let values = foreign code python3(int limit = max_items) -> array<int> {
    source `def main(limit: int) -> dict:
    return {"result": list(range(limit))}`;
};
```

普通输出类型声明一个隐式 `result` 端口，表达式直接得到该值，因此使用 `values` 而不是
`values.result`。多输出使用至少包含两个字段的匿名结构契约：

```dc
let stats = foreign code python3(string text = query) -> {
    count: int;
    summary: string;
} {
    source `def main(text: str) -> dict:
    return {"count": len(text), "summary": text}`;
};

return stats.summary;
```

每个结构字段映射到同一个 Code 节点上的独立输出端口。Python AST 检查仅验证 `main`
参数和可见返回键，不推断或修改声明类型。当前只支持 `python3`，输入和输出契约均不支持
`file`。旧 `code(...)` 和 `code python3(...)` 语法均不存在。

可用下面的命令验证整个样例目录的前端语法、类型和 lowering：

```bash
pytest -q tests/lang/regression/test_dc_examples.py
```

### 变量引用映射

| YAML 引用 | DSL 引用 |
|-----------|----------|
| `{{#sys.query#}}` | 函数参数 `query` |
| `{{#start.var#}}` | `var` (input 变量) |
| `{{#llm.text#}}` | `response.text` |
| `{{#retrieval.result#}}` | `context.result` |
| `{{#code.summary#}}` | `stats.summary` |
| `{{#http.body#}}` | `page.body` |
| `{{#http.status_code#}}` | `page.status_code` |
| `{{#tool.result#}}` | `search.result` |
| `{{#conversation.var#}}` | `var` (input 变量) |

