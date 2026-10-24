# python-code 统一翻译模式契约 —— ncoda 子语言 → python 子集

> **历史快照（2026-10-25 注）**：平台适配器、平台证据与平台判据已随适配器移出开源仓
> （商业仓 `internal/backend/*`、`platforms/*`）。本文只用于追溯当时的平台映射/契约，
> 不得据此把平台代码或平台判据加回开源仓；开源仓的定位见 `README.md`。

> 文档性质：**契约**（生成器与评审者共同遵守的规范）。回答"编译器自动插入的
> python-code 长什么样、为什么长这样、不允许长成什么样"。
> 配套：**`docs/SHIM-PYTHON-SUBSET-SPEC.md`（shim-python-subset 翻译模式规范 v2.0——语法/语义/翻译模式权威，A 契约，2026-09-03）**、
> `docs/ideas-todo/PYTHON-SUBSET-GENERATOR.md`（子集语法与迁移记录）、
> `docs/ideas-todo/TRANSLATION-PATTERN.md`（placement 模型）、
> `docs/PYTHON-CODE-NODE-GENERATOR.md`（专题回顾）。
>
> 状态：**v1.1（2026-08-26）**——v1 签名协议全量审计通过（136 编译器 shim
> 100% `-> dict`）；补充 buildMap 混合命名规则（§2.1，契约空白处实证）；残余
> legacy 回退逐项登记，收编路线见 §6。

---

## 0. 定位与边界（2026-09-02 确立的标准，摘要）

- **为什么引入**：coze-biz 等平台把"表达式计算"物理化为 CodeRunner（python code）
  节点——ncoda 的常量/算术/内建/聚合/json 守卫/三元等表达式在平台画布上没有直接
  节点类型，编译器**必须**生成 python 代码才能编译；这不是选择，是平台的物理通道。
- **做什么**：把编译器**自建**的表达式 shim 定义为一个**封闭 python 子集**
  （ncoda 维护、唯一生产者），前向（ncoda→python）与反向（python→ncoda）精确互逆，
  保证 roundtrip 闭合（compile(reverse(S))==S 字节级）。
- **不做什么**：**不解释用户手写的 foreign code**（不打开盒子——静态分析、语义翻译、
  运行期验证都不做）；不是通用 python 实现；不做语义猜测（篡改的 shim 一律透传）；
  不产生新 ncoda 语法；不解决平台 API 差异。
- 与 LLM「定义 T 即信任 T」同一哲学：**我们的 shim 我们负责精确互逆；用户的盒子
  我们不打开，责任在 runtime 和用户**。
- 全文：§9。

---

## 0. 一句话契约

**编译器生成的每个 python-code 节点 = 一个 `def main(<params>) -> dict:` 的
纯计算函数**：参数按绑定名（ident 引用用变量名，否则 argN），返回值按输出端口
（单端口 `{"result": ...}` / 多端口 `{k: v, ...}`），import 自足（`import json`
在 def 之前），平台沙箱内独立可执行。

---

## 1. 统一签名协议（已生效，pyast 第三后端）

```
def main(p1, p2, ...) -> dict:        ← 同步函数，唯一入口名 main
    <body>                             ← 零副作用纯计算
    return {...}                       ← 最后一条语句必须是 return dict
```

- **同步**：不使用 `async def`（编译器生成的 shim 无 IO；用户 foreign code 除外）。
- **返回注解固定 `-> dict`**（不写 `-> Output` 等平台别名，子集内只有 dict）。
- **import 自足**：`import json` 等 stdlib 依赖在 `def main` 之前，绝不放在
  def 之后（放后面是语法错误）；缺 import 是运行时 NameError，静态 gate 抓不到
  —— 这是 P5.12 用红绿测试锁住的纪律。
- **类型注解可选**：`items: list` 等仅在该形态需要时出现（listcomp shim）；
  默认不写注解（`namedParams` 输出无类型）。

## 2. 参数命名契约（argN vs 语义名）

| 场景 | 命名 | 依据 |
|---|---|---|
| **可解析引用**（ident 或成员路径 —— 只要解析出 `(产出方, 端口)`） | **被解析引用的端口名**（= 反编译侧回吐的名字，与值身份一一对应） | `arrayElemParamName` / `shimParamNames` |
| 端口名不可用（空 · 非标识符 · 整值标记 `@whole` · Python 关键字 · 同名已被占） | `argN`（N = 元素序号；自造名，末档） | `arrayElemParamName` |
| 无引用语义的绑定（子表达式物化） | `arg1`, `arg2`, … | `lowerArrayPyast` 元素、`json.parse`/`stringify` 单入参 |
| 无参数 shim | 空参 `def main() -> dict:` | `buildConst`（标量物化） |

**「裸标识符 ⇒ 用变量名」不是一档**（2026-09-26 R-104 删除）：变量名是**拼写**，不是身份 ——
同一份产物读回后数组元素从 `[x1.text, x2.text]`（成员访问）变成 `[v1, v2]`（提升后的裸标识符），
两个 ref 仍是 `(100002, text)` / `(100003, text)`（**同一个值身份**），旧档却铸出 `v1`/`v2`
⇒ 逐轮 `ARTIFACT_DRIFT/PARAM@r2`。规范形正本 = `docs/five-step/spec/J-join-canonical-form.md` §42.8。

参数顺序 = 绑定出现顺序（`bindings` 追加序）；同名绑定去重（`containsParam`）。

### 2.1 buildMap 混合命名（v1.1 实证补充）

`build_node.go` 的 record/map 物化 shim 参数名**按值形态分轨**（契约原空白处，
2026-08-26 语料审计实证 1 例混合形态，非违规）：

| 值形态 | 命名 | 原因 |
|---|---|---|
| plain resolvable ref（map 值直接是引用） | `argN`（N = map 条目序号） | 该 arg 同时是**输出 schema 属性名**（`Source.Path` 直连上游
  ref 端口），属性名需稳定简短，不能用长变量名 |
| declarative 绑定（coalesce 投影/字面量/表达式叶） | 变量名 | 遵循 §2 语义名规则 |

混合形态是规范（如 `def main(v_a, v_b, arg2)`），argN 编号与语义名共存，
不要求编号连续；`props[key]` 仅对平台标识符键建立输出属性。

## 3. 返回值契约（单端口 vs 多端口）

| 输出形态 | 模板 | 生成点 |
|---|---|---|
| 单端口标量/表达式 | `return {"result": <expr>}` | 条件物化、算术、calc builtin、enum member、标量常量 |
| 单端口列表 | `return {"result": [...]}` | array 字面量物化、listcomp |
| 单端口字符串序列化 | `import json` + `return {"result": json.dumps(arg1, ensure_ascii=False)}` | json.stringify |
| 多端口 record | `return {"k1": v1, "k2": v2}` | buildMap 对象字面量、json.parse<T> 字段提取 |

**端口名 = ncoda 输出字段名**（"result" 是单端口约定名，下游按
`singleTypeKey(OutTypes)` 解析）；coze 端保留 canonical "output" 端口。

## 4. 类型映射表（ncoda 类型 → 平台端口 / python 形态）

| ncoda 类型 | python/平台处理 |
|---|---|
| string | 端口 `type: string`；python 侧 `str(...)` 兜底 |
| file (image/audio/video) | code 节点按 string 端口（URL 字符串）—— `isCodePortType` |
| integer / float | 端口 numeric；python 字面量原样 |
| boolean | 端口 boolean；python `True`/`False` |
| list<T> | 端口 `items.type: T`；python list 字面量/推导式 |
| object<fields> | 多端口 record 或 `{"result": dict}` |
| any | JSON 值 → string 端口（参考 codec），或 `json.dumps` 序列化 |

## 5. 残余 legacy 回退登记（有意保留，非欠账）

| 位置 | 形态 | 保留原因 |
|---|---|---|
| `build_expr.go:388` | array 字面量 `def main(...) -> dict: return {"result": [...]}` | 部分元素不可证明字节对等时回退（pyast 路径 `lowerArrayPyast` 是主路径） |
| `build_expr.go:464` | `buildConst` 标量 `def main() -> dict: return {"result": <json.Marshal>}` | 字符串-LITERAL 转义差异家族（双引号 JSON vs pyast 引号）未上豁免白名单 |
| `build_expr.go:553` | enum member `def main() -> dict: return {"result": <QuoteToASCII>}` | 同上转义差异 |
| `build_expr.go:860` | 表达式回退 `def main(<bindings>) -> dict: return {"result": <renderConditionPython>}` | pyast 接受集外形态（map/array 字面量守卫、P4 边界） |
| `build_expr.go:1155` | inline 回退 `def main(<bindings>) -> dict: return {"result": <renderInlinePython>}` | 同上 |
| `build_expr.go:1505` | listcomp `def main(items: list) -> dict: return {"result": [pred for item in items]}` | lambda 谓词形态未上 pyast（pyast 主路径已接入 `lowerLambdaPyast`） |
| `build_condition.go:101` | 条件物化 `def main(<bindings>) -> dict: return {"result": <expr>}` | 同上（pyast 主路径 `lowerShimExpr` 已接入） |
| `build_node.go:617` | buildMap `def main(<names>) -> dict: return {kv...}` | quote-unsafe 键 / 值形态差异（pyast 主路径 `lowerMapPyast` 已接入） |
| `build_node.go:813` | json.parse `def main(arg1) -> dict: import json; data = json.loads(arg1); return {...}` | 字段默认值形态差异（pyast 主路径 `lowerJsonParseShim` 已接入） |
| `build_node.go:931` | json.stringify `def main(arg1) -> dict: import json; return {"result": json.dumps(...)}` | 低价值形态（coze 已原生 type-58） |

**回退纪律**：`(ok=false, err=nil)` 回退旧模板保证**字节 = 今天线上字节**；
`(ok=false, err!=nil)` 传播错误，绝不静默产出错误代码。`Synthetic=true` 标志
区分编译器生成与用户 foreign code（G7 透传契约）。

## 6. 收编路线（消除残余 legacy → 全 pyast）

1. **优先收编同构形态**：`build_expr.go:388`（array）、`build_condition.go:101`、
   `build_node.go:617`（buildMap）——pyast 主路径已存在，仅需扩大接受集
   （逐一验证字节对等，加 parity 测试）。
2. **转义差异家族**（`buildConst`/enum member/quote-unsafe 键）：在 pyast
   序列化器补 `\uXXXX` 全家族转义 + 白名单豁免测试后收编。
3. **低价值形态**（json.stringify）：维持现状（平台原生 `to_json` / 开源 type-58 优先），
   登记为"永不收编"，避免为平台已原生化的形态付出维护成本。
   **边界（2026-09-27）**：coze-biz 与 coze 一样 ⇒ 该 shim 只在**无承载面的目标**（参考目标）出现；
   「有没有承载面」由能力轴 `capability.Table.JSONSerial` 回答，不由本文件判
   （正本 = `docs/PLACEMENT-CONTRACT.md` §1.3）。
4. **门禁**：每收编一个 → 该形态的 parity 测试从"legacy 字节"改为
   "pyast 快照"，全语料字节审计（133 code 节点零漂移）保持绿。

## 6.5 全量合规审计（v1.1，2026-08-26）

工具：rt-matrix 全语料产物扫描（581 工作流正编译产物，提取全部 code 节点）。

| 类别 | 数量 | 合规 |
| --- | --- | --- |
| 编译器生成 shim（`def main(...) -> dict:`） | 136 | **返回注解 136/136 `dict`（100%）**；参数：语义名 75 /
  argN 35 / 空签名 25 / buildMap 混合 1（§2.1 规范形态） |
| 用户 python foreign（`async def main(args: Args) -> Output:`） | 155 | 平台标准模板，G7 透传不干预 |
| JS/TS foreign（`async function main(...)`） | 373 | 透传不干预（language 5） |

结论：编译器生成 shim 已全量落在契约 §0-§3 形态内；契约与实现的唯一
差异点是 buildMap 混合命名，已收编为 §2.1 规范。

## 7. 验证门禁（契约的守护）

| 层 | 工具 | 守什么 |
|---|---|---|
| 字节对等 | `pkg/ir/*_test.go`（Parity 系列） | 生成器输出 = 契约模板 |
| 运行时 | `pkg/ir/exec_shim_test.go`（7 形态 × 16 断言） | shim 在 CPython 独立可执行（import 自足） |
| **求值端同解** | `pkg/pygen/shim_eval_parity_test.go`（名册 = `byteEquivCases()`，16 内建 × 覆盖+语义） | **生产端能发的每个内建，`pkg/expr.EvalShim`（rt-l3 的求值器）都必须算出同一结果**（见 §10） |
| 平台接受性 | coze-probe 65/65 · flow-probe 27/27 · 对照校验 0 err | 平台 checker 接受 |
| 全语料 | `ncoda_build.py` 重编译审计 | 27 语料 133 code 节点零漂移 |

---

*归档：2026-08-24 · ncoda · 统一翻译模式契约 v1。*

---

## 8. 可逆机制：反向翻译契约 + marker + 封闭契约（2026-09-02 用户拍板）

> 配套：`docs/ideas-todo/SHIM-REVERSIBLE-DSL.md`（规划与实施阶段，已拍板）。
> 本章回答"编译器自建 shim 如何精确还原回 ncoda"——这是**小语言（python 子集 DSL）
> 的双向翻译**：前向（ncoda → 子集，§0-§7）+ 反向（子集 → ncoda，本章）。

### 8.1 封闭契约（不可违背）

- **shim-python-subset 由 ncoda 维护**：子集定义（文法/语义/类型）是唯一权威，
  **编译器是唯一生产者**（Synthetic / G7：编译器生成 vs 用户 foreign code 严格区分）。
- **不接受用户的随意修改**：编译产物中的 shim 被用户手改（字节/标记不符契约）即
  **契约破坏** —— 反编译端不做语义猜测、不反向，按用户 foreign code 透传处理。
- **修改责任在用户**：与 LLM "定义 T 即信任 T"（2026-08-31）同一哲学 —— 编译器不打开
  被篡改的盒子，残差责任归 runtime 与用户；不为此产生 MISMATCH/UNVERIFIED 阻断。
- **编译与反编译封闭**：互逆保证只在封闭子集内成立（标记 ∧ 形态符合 ⇒ 精确反向）；
  子集外一律视为用户代码（透传 + strict 模式警告，不阻断）。

### 8.2 marker 契约

- 常量 `# ncoda:shim 1`（`pkg/pyast.ShimMarker`，编译/反编译共用单源）。
- **打标范围**：编译器生成的**单表达式 shim**（`def main(<params>) -> dict:` +
  单条 `return {"result": <expr>}`，无 import、无中间语句）。多语句 shim
  （json.parse）、多端口 shim（buildMap）、listcomp（map-lambda，v2 候选）不打标。
- 打标即改字节：golden/parity/快照测试显式锁定，语义不变。

### 8.3 反向判定（反编译端，全部满足才反向，任一不满足 → 透传）

1. 代码以 `pyast.ShimMarker` 开头；
2. 模块形态严格符合 §1（`def main(...) -> dict:` + 单条 `return {"result": E}`）；
3. 表达式 E 完全落在 §2.2 产物全集（内建 15 + 复杂内建 3 + json 守卫 + 字面量/二元/
   三元/模板串/数组字面量）；
4. 歧义消解类型可判定（§8.4 表；类型不可知 → 透传）。

**闭集纪律（2026-09-26，R-94）**：逐片段**整体闭合** —— 只要 shim 里出现任一不可反向的片段，
整段**透传**（绝不半反向）；因此「前向会产出的形态」必须在 §8.4 有对应行，否则症状不是
「少一条映射」而是 `# ncoda:shim` 漏进 ncoda 文本。判据 =
`platforms/cozebiz/decompile/unary_minus_shim_roundtrip_contract_test.go`（正侧三形 + 负侧两锚）。

### 8.4 类型驱动歧义消解表（同一字节可能有多个 ncoda 源）

| python 字节 | 条件 | 反向 ncoda |
|---|---|---|
| `(a in b)` | b 类型 array | `a in b`（二元 in） |
| `(a in b)` | b 类型 string | `contains(b, a)`（内建子串） |
| `(not x)` | 任意 | `empty(x)`（`!` 要求 bool；empty 接受 any；真值否定语义等价） |
| `(-x)` | 任意 | `-x`（算术取反；ncoda 一元负号。前向**会**产出该形态的落点 = **可写绑定的字面量物化** `int v = -1;` / **变量取反** `let m = -n;` / **条件位的 ref 取反** `if (n > -m)`（三处一手，2026-09-26）；`-<字面量>` **不落这里** —— `let k = -2;` 折进 litVars 不建节点（R-95）、`if (n > -2)` 走原生条件槽（R-94）） |
| `(a or b)` | 任意 | `a \|\| b`（值合并；`(xs or [])` 同理） |
| `"a" + str(x) + "b"`（flat 无括号） | 首尾字面量 | 模板串 `` `a${x}b` `` |
| `(a + b)`（带括号） | 任意 | `a + b`（二元 +；concat 内建落此，语义等价） |
| `(a // b)` | 任意 | `div(a, b)` |
| `[v] * n` | 任意 | `repeat(v, n)` |
| `list(range(a, b))` | 任意 | `range(a, b)` |
| `next((str(u) for u in (xs or []) if u and str(u).strip()), "")` | 字节模式 | `first_non_empty(xs)` |
| `[str(x) for g in (xs or []) if isinstance(g, list) for x in g]` | 字节模式 | `flatten(xs)` |
| `[{"index": i, "value": v} for i, v in enumerate(xs)]` | 字节模式 | `enumerate(xs)` |
| json 守卫 `str((a[i] if len(a) > i else {}).get(f) or "")` / `(a[i] if len(a) > i else {})` | 结构模式 | `a[i].f` / `a[i]` |
| `len/str/int/s.strip()/sep.join(a)/a.replace(b,c)/s.split(d)` | 确定性 | `len/to_string/int/trim/join/replace/split` |

**打印规范形（一处，2026-09-26 补）**：ncoda 表达式 → python shim 体的打印是
**一个**打印器，既有字节约定 = **顶层二元恒加括号**（`{"result": (a + b)}`）—— 含
calc-builtin 降到二元的情形（`repeat(a,b)` → `[a] * b`；`div` 自带括号）：同一个
`[20] * (n // 20)` 子表达式的两种拼写（`concat(repeat(20, div(n,20)), [n % 20])` /
`repeat(20, div(n,20)) + [n % 20]`）必须同字节。分叉的症状**不是**编译错，是**产物
一轮不动点破裂**（`ARTIFACT_DRIFT/PARAM@r2`，首个差异正是这一行）。判据 =
`internal/compiler/cozebiz_shim_print_single_ruler_contract_test.go`（calc-builtin 面）。

### 8.5 互逆不变量（验证门禁）

```
对封闭子集表达式 E： compile(E) = S（带标记 shim 字节）
                    reverse(S) = E（ncoda 表达式）
                    compile(reverse(S)) == S（字节级）
                    reverse(compile(E)) == E（ncoda 级）
```

单元：`shim_reverse_test.go`（每 golden 形态双向闭合）；集成：decompile 含标记 shim 的
工作流 → ncoda 原生表达式；语料：657 全量 decompile→compile→redecompile 全部
SUCCEEDED/OK，round3 变化仅来自 shim 还原（行级 diff 总量不劣于基线；
round2 相似度指标含 difflib 长文件噪声，以行级 diff 为稳健度量）；平台：27 语料
边等价（基线 MATCH=9/27 + 白名单 DIFF）+ 快照冻结更新。

---

*增补：2026-09-02 · ncoda · 可逆机制 + marker + 封闭契约（v1.2）*

---

## 9. 定位与边界：为什么引入 shim-python-code？它做什么？它不做什么？（2026-09-02 标准）

> 本节是 shim 可逆小语言的**定位标准**（A 级契约）。回答三个问题：为什么引入、
> 做什么、不做什么。任何对 shim 的扩展/评审/修改都以此边界为准。

### 9.1 为什么引入？

1. **平台物理通道**：coze-biz（及 参考目标/coze-studio）画布把"表达式计算/标量操作"
   物化为 CodeRunner 节点。ncoda 的以下表达式在平台上没有直接节点类型，只能落进
   CodeRunner：
   - 常量物化（buildConst，`{"result": ""}`）
   - 标量表达式（算术、比较、内建：len/to_string/trim/join/split/replace/int/div/…）
   - 聚合变换（first_non_empty / flatten / enumerate / range / repeat）
   - json 守卫（`items[0].field` 数组元素字段访问）
   - 三元表达式、模板串、值合并（`||`）、数组字面量
   编译器**必须**生成 python 代码才能完成编译——不是实现选择，是平台约束。

2. **CodeRunner 节点有两类来源，必须区分**：
   | 来源 | 性质 | 反编译端待遇 |
   |---|---|---|
   | 编译器自建 shim（Synthetic） | 机器生成、形态封闭、纯计算无副作用 | **精确反向**回 ncoda |
   | 用户手写 foreign code | 用户意图、任意形态、可能含 IO/副作用 | **透传**，不打开盒子 |
   混淆二者的代价：把编译器自己的 shim 当用户代码透传 → roundtrip 漂移（编译→反编译
   →编译不闭合）；把用户代码当 shim 反向 → 语义猜测、破坏用户意图。

3. **所以需要一个"可逆小语言"**：把编译器自建的 python 定义为一个封闭子集
   （文法/语义/类型 + marker），编译器对自己的产物负责到底——生成它，就精确还原它。
   roundtrip 闭合由此成立；用户盒子由 runtime 与用户负责（哲学对齐 LLM §0）。

### 9.2 它做什么？

1. **定义子集**：一个由 ncoda 维护的封闭 python 子集（模块形态 §2.1、表达式全集、
   类型格），是编译器生成表达式代码的唯一形态；子集扩展 = 语言级变更，先设计后审核。
2. **打标**：编译器生成的**单表达式** shim 带 `# ncoda:shim 1` marker（单源常量
   `pkg/pyast.ShimMarker`）；多语句（json.parse）/多端口（buildMap）等形态不打标。
3. **前向翻译**：ncoda 表达式 → 子集 python（§0-§7；输出字节被 golden/parity/快照锁定）。
4. **反向翻译**：marker ∧ 模块形态严格符合 ∧ 表达式落在子集全集 ∧ 歧义点类型可判定
   → 还原为 ncoda 第一性表达式（§8）。
5. **互逆闭环**：`compile(reverse(S))==S`（字节级）∧ `reverse(compile(E))==E`（ncoda 级），
   golden 双向闭合测试锁定。
6. **判定与透传**：任一判定不满足 → 不反向、按用户 foreign code 透传（strict 模式
   警告，不阻断）。

### 9.3 它不做什么？（边界）

1. **不解释用户手写的 python**（不打开盒子）：不静态分析语义、不翻译成 ncoda、
   不做超出 MISMATCH/UNVERIFIED 静态核对的语义校验；用户代码质量与 ncoda 无关，
   责任在 runtime 与用户。
2. **不是通用 python 实现**：任意 python（import/类/异常/多语句/复杂控制流/IO 等
   子集外形态）一律不反向。
3. **不做语义猜测**：被用户篡改的 shim（字节/标记不符合契约）不反向、不猜测，
   按 foreign code 透传——**绝不半反向**（宁可透传，不输出可能错误的对象）。
4. **不做运行期验证**：shim 是否真能执行、行为是否正确是 runtime 的事；编译器只
   保证静态互逆，不保证 python 语义正确性（那不是编译器职责）。
5. **不产生新 ncoda 语法**：反向输出全为既有 ncoda 语法；语言级扩展仍走用户审核
   （AGENTS.md 语法扩展审核契约）。
6. **不覆盖所有表达式（渐进承诺）**：子集外形态保持透传，覆盖是渐进扩展的，
   不是一次性承诺；每扩一个形态 = 前向 golden + 反向 golden + 双向闭合测试。
7. **不解决平台 API 差异**：平台形状适配是渲染器的职责；shim 只解决"表达式在
   CodeRunner 里的双向翻译"，不承担平台间语义桥接。

### 9.4 判定一句话

**我们的 shim 我们负责精确互逆；用户的盒子我们不打开。**

### 9.5 目标不变量：ncoda 层不存在 shim-python-code（2026-09-02 用户拍板）

- **shim 只存在于编译输出的平台工作流中**（coze-biz yaml/json、参考目标、coze-studio）——
  它是编译器的内部通道形态（IR 表达式 → 平台 CodeRunner 物理通道），**不是语言的
  一部分**。
- **用户只操作 ncoda 时对 shim 完全无感**：
  - 手写 ncoda：自然没有 shim；
  - 反编译产物 ncoda：编译器自建 shim 被精确还原为原生表达式；用户 foreign code
    以 ncoda 既有 `foreign code` 语法呈现——**"shim"这个概念永不出现**。
- **可逆小语言存在的目的之一就是守住这条不变量**：没有反向机制，反编译时编译器
  shim 会以 foreign code 黑盒形式漏进 ncoda 层，破坏"ncoda 里不存在 shim"。
- **判定（测试锁定）**：任何 ncoda 文本（源码 / 反编译产物）中出现 `# ncoda:shim`
  标记 = 违反不变量（反向泄漏 / 编译器 bug）。657 语料全量验证证据（2026-09-02）：
  编译输出 201/657 含 shim（唯一出现处），round1/round3 反编译产物 657×2 零泄漏。

---

## 10. 求值端同解：pygen 名册 × `EvalShim`（2026-09-28 立）

### 10.1 这是一条**两端同解**判据，不是一个新契约

shim 的**生产端**（`pkg/pygen.LowerBuiltin`，经 `pkg/ir/pyast_lower.go` 进 IR）
和**求值端**（`pkg/expr.EvalShim`，rt-l3 跑的就是它）是**同一份闭集的两次实现**。
§8.1 只锁了「前向 ↔ 反向」这一对；本条锁的是**第三对**：生产端能发的东西，求值端必须能算
（且算出**同一个结果**）。

判据 = `pkg/pygen/shim_eval_parity_test.go::TestRuntimeEvaluatorCoversAllBuiltins`：
名册复用同一张表（`byteEquivCases()`，**一处定义**），逐个内建构造最小 shim
（`def main(<args>) -> dict: return {"result": <lowered>}`），送 `EvalShim` 求值并与
逐字期望值比对。任何一端加了内建而另一端没跟上，这条判据当场红。

### 10.2 为什么必须有它（一手代价，2026-09-28）

G14 给语言补了 `float(x)` cast，**编译端能发、求值端从没实现** ⇒ 半接线。代价不是
"少个功能"，是**产物里有节点、编译一路 SUCCEEDED、跑起来才炸**：

```
672 源跑批：leg1:run-err 138 → 275
  146 源的失败形状 = shim: unknown function "int"（155 处）+ "float"（10 处）
```

而这类缺陷是**纯静态事实**（名册 × 求值器支持集），本该秒级可判 —— 却要靠一次
12 分钟的全量跑批才暴露一次。

判据落地当天实测：**16 个内建里 9 个求值端算不了**（撤掉本节的两端实现即复现）：
`trim`（`.strip`）· `join`（`sep.join`）· `replace`（`.replace`）· `split`（`.split`）·
`int`（裸内置）· `float`（裸内置）· `repeat`（`[v] * n`：求值器只认数值乘法）·
`flatten`（多子句推导：parser 把 `for … in (x or []) if …` 的尾随 `if` 当成三元吞掉）·
`first_non_empty`（括号生成器表达式 + `next`：parser 不认 `(elt for …)`）。

### 10.3 修的方向（都落在求值端，不改生产端）

| 缺什么 | 修法 |
| --- | --- |
| 裸内置 `int`/`float` | 按 **Python 语义**实现（`int("1.5")` 是 ValueError，**不宽容** —— 宽容 = rt-l3 绿而平台 CodeRunner 红 = 假绿） |
| 方法 `.strip/.split/.join/.replace` | `callMethod` 补齐；`join` 要求元素全 str（Python 的 TypeError 语义） |
| `[v] * n` | `evalBinary` 的 `*` 前加「序列 × 整数」分支 |
| 多子句推导 | `parseCompClauses`：**iterable 用 `parseOr` 而不是 `parseExpr`**（Python 语法里 for 的 iterable 是 `or_test`；用完整 expression 会把尾随 `if` 吞成三元） |
| 括号生成器 + `next` | `parsePrimary` 的 `(` 分支识别 `(elt for …)` → `GeneratorExpr`；`GeneratorExpr` 物化求值（本子集无惰性需求） |

### 10.4 边界（登记，不掩盖）

- 本判据的名册 = **16 个 calcBuiltinPython 内建**。shim 还有别的发射形态
  （`json.loads` 守卫、聚合/merge 载体、`to_json` 系列）—— 它们不在本条覆盖内；
  扩名册 = 扩 `byteEquivCases()`，判据自动跟着长。
- `EvalShim` 是 rt-l3 的求值器，**不等于平台实现**：平台侧的 CPython 由 §7 的
  `exec_shim_test` 那一行守（真 python3 编译/执行）。
