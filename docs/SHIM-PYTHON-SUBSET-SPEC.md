# shim-python-subset 翻译模式规范（ncoda ⇄ python 子集，语法/语义/翻译模式定死）

> 文档性质：**A 契约**（语言语义 / 平台契约层级；改动须审核）。本文件是
> shim-python-subset **语法、语义与双向翻译模式的单一权威**——回答三个问题：
> **它长什么样（语法）、它什么意思（语义）、ncoda 与它如何互译（翻译模式）**。
>
> 配套（不冲突，后者为细则）：
> - `docs/PYTHON-SHIM-TRANSLATION-CONTRACT.md`——前向签名协议 / 参数命名 / 返回值 /
>   类型映射 / legacy 回退登记（§0-§7）+ 可逆机制（§8）+ 定位标准（§9）
> - `docs/ideas-todo/SHIM-REVERSIBLE-DSL.md`——可逆小语言立项规划（P1-P5 已完成）
> - `docs/ideas-todo/PYTHON-SUBSET-GENERATOR.md`——子集生成器与迁移记录
> - `外部设计文档/NCoda-TRANSLATION-CONTRACT.md`——ncoda ↔ 全平台翻译契约总纲
>
> 状态：**v2.0（2026-09-03）**——把散落在规划文档与实现注释里的文法/语义/翻译表
> 收敛为正式规范；与 `pkg/pyast`（lexer/parser/serialize，编译+反编译共用单源
> AST）逐条对应；互逆不变量、封闭契约、验证门禁在本文件定死。
>
> 实现锚点：`pkg/pyast/lexer.go` `parser.go` `node.go` `serialize.go`
> `shim_marker.go`、`pkg/pygen/pygen.go`（前向 builtin 表）、
> `platforms/cozebiz/decompile/shim_reverse.go`（反向引擎）、
> `pkg/ir/pyast_lower.go`（ncoda → pyast lowering）。

---

## 1. 定位与封闭契约（不可违背）

### 1.1 一句话定位

**shim-python-subset 是编译器自建的封闭 python 子集**：ncoda 的表达式（常量/算术/
内建/聚合/json 守卫/三元等）在 coze-biz 等平台画布上没有直接节点类型，编译器
**必须**生成 CodeRunner（python code）节点才能编译——这是平台的物理通道，不是
实现选择。shim 由 **ncoda 维护、编译器是唯一生产者**，前向（ncoda→python）与
反向（python→ncoda）**精确互逆**，保证 roundtrip 闭合。

### 1.2 目标不变量（2026-09-02 用户拍板）

- **shim 只存在于编译输出的平台工作流中**；ncoda 层（源码与反编译产物）**不存在
  shim-python-code**——任何 ncoda 文本出现 `# ncoda:shim` = 违反不变量。
- 用户只操作 ncoda 时对 shim **完全无感**：手写天然无 shim；反编译产物中编译器
  shim 精确还原为原生表达式，用户代码以 foreign code 语法呈现。

### 1.3 封闭契约（2026-09-02 用户拍板）

1. **子集定义（本文件）是唯一权威**；**编译器是唯一生产者**（Synthetic/G7）。
2. **不接受用户的随意修改**：编译产物中的 shim 被用户手改（字节/标记不符契约）即
   **契约破坏**——反编译端不做语义猜测、不反向，按用户 foreign code 透传处理。
3. **修改责任在用户**：与 LLM「定义 T 即信任 T」（2026-08-31）同一哲学——编译器
   不打开被篡改的盒子，残差责任归 runtime 与用户。
4. **编译与反编译封闭**：互逆保证只在封闭子集内成立（标记 ∧ 字节符合契约 ⇒ 精确
   反向）；子集外一律视为用户代码（透传 + strict 模式警告，不阻断）。

### 1.4 子集扩展流程（语言级变更）

**任何子集扩展（新增表达式形态 / 新增 builtin / 改变既有字节形态）都是语言级变更**，
必须先提交设计说明（变更点、动机、影响面、roundtrip 影响、与既有语义兼容性），
经用户审核确认后才能实施；实施必须同时更新：本文件文法/翻译表、前向 lowering、
反向引擎、golden/parity/双向闭合测试。**禁止未经审核悄悄扩子集。**

---

## 2. 语法（EBNF 定死）

> 权威 = 本 EBNF。实现 = `pkg/pyast`（lexer.go 词法 + parser.go 递归下降 +
> serialize.go 唯一序列化规则）。**语法以外的一切字节都不是子集**；序列化器不存在
> 第二个展开规则，禁止手写字符串绕过。

### 2.1 模块形态（Module）

```
module   := [ marker ] def_stmt
marker   := "# ncoda:shim 1" NEWLINE          # 常量单源 pkg/pyast.ShimMarker
def_stmt := "def" "main" "(" params? ")" "->" "dict" ":" NEWLINE body
params   := param ("," param)*
param    := NAME [ ":" type_annotation ]
type_annotation := NAME                        # 契约：编译器产物当前仅 'list'（listcomp shim）；反向解析宽容任意 NAME
body     := stmt*
stmt     := import_stmt | return_stmt | assign_stmt | if_stmt | for_stmt | pass_stmt
import_stmt := "import" NAME NEWLINE           # 仅在 def main 之前（import 自足契约）
return_stmt := "return" dict_expr NEWLINE      # 最后一条语句必须是 return
assign_stmt := NAME "=" expr NEWLINE
if_stmt  := "if" expr ":" NEWLINE stmt+ [ "else" ":" NEWLINE stmt+ ]
for_stmt := "for" NAME "in" expr ":" NEWLINE stmt+
pass_stmt:= "pass" NEWLINE
```

- **缩进**：固定 4 空格（序列化器 `Repeat("    ")`；解析器按行结构处理）。
- **单表达式 shim**（可逆承诺形态）：body = 仅一条 `return {"result": E}`，
  无 import、无中间语句，头部带 marker（§5 打标范围）。
- **多语句 / 多端口形态**（json.parse / buildMap / json.stringify）：不承诺可逆，
  不加 marker（§5.2）。

### 2.2 表达式文法（Expr，优先级从低到高）

```
expr        := ternary
ternary     := or ( "if" or "else" or )?         # 序列化器恒包括号：(T if C else E)
or          := and ( "or" and )*
and         := not ( "and" not )*
not         := "not" not | comparison            # 序列化器恒包括号：(not x)
comparison  := arith ( comp_op arith )*
comp_op     := "==" | "!=" | "<" | "<=" | ">" | ">=" | "in" | "not" "in"
arith       := term ( ( "+" | "-" ) term )*
term        := unary ( ( "*" | "/" | "//" | "%" ) unary )*
unary       := "-" unary | postfix               # 序列化器恒包括号：(-x)
postfix     := atom ( "(" args? ")" | "[" expr "]" | "." NAME [ "(" args? ")" ] )*
atom        := NUMBER | STRING | "True" | "False" | "None"
             | NAME | "(" expr ")"
             | "[" [ expr ("," expr)* [","] ] "]"       # list 字面量
             | "{" [ STRING ":" expr ("," STRING ":" expr)* [","] ] "}"  # dict 字面量（键必须字符串）
             | listcomp | dictcomp | genexpr
args        := arg ("," arg)*
arg         := expr | NAME "=" expr               # 位置参数 + 关键字参数（ensure_ascii= 等）
listcomp    := "[" expr comp_clause+ "]"
dictcomp    := "{" expr ":" expr comp_clause+ "}"
genexpr     := "(" expr comp_clause+ ")"          # 仅在 first_non_empty 模式内
comp_clause := "for" NAME ("," NAME)* "in" expr ( "if" expr )*
```

- **串接（ConcatExpr）**：模板串 lowering 产出 flat `"a" + str(x) + "b"` 链
  （无括号、parts 不嵌套），是独立节点类型（`ConcatExpr`），**不是** BinaryExpr。
- **括号纪律**：二元节点子节点若也是二元 → 序列化器加括号（复刻 legacy 字节
  `(a + b) + c`）；其余子节点自带定界（一元/三元/调用/下标/属性/dict/list 解析
  天然无歧义，不加多余括号）。`ParenExpr` 仅在 legacy 字节需要显式外层括号时
  （div/concat/contains）出现，序列化恒发射括号。
- **dict 键必须是字符串字面量**（子集契约；`buildMap` 平台标识符键形态另见
  PYTHON-SHIM-TRANSLATION-CONTRACT §2.1）。

### 2.3 词法（Token）

| Token | 形态 |
|---|---|
| NAME | `[_a-zA-Z][_a-zA-Z0-9]*` |
| NUMBER | 十进制整数（允许 `_` 分隔）/ 浮点（含指数 `e±N`） |
| STRING | `"..."` 或 `'...'`，转义族见 §2.4 |
| OP | `+ - * / // % == != < <= > >= ->` |
| PUNCT | `( ) [ ] { } , : . =` |
| NEWLINE | `\n`（解析器按行结构处理缩进） |

**注释**：`#` 到行尾（marker 行是唯一正文注释；正文无注释）。

### 2.4 字符串转义族（与 quoteString 严格镜像）

```
\"  \\  \b  \f  \n  \r  \t  \uXXXX
\uXXXX 支持 surrogate pair 组合（\uD800-\uDBFF + \uDC00-\uDFFF → 增补平面字符）
```

- 序列化（quoteString）：ASCII 可打印（0x20-0x7e）原样；控制字符 `\uXXXX`；
  增补平面字符 → surrogate pair `\uXXXX\uXXXX`。
- 词法（lexString）：与 quoteString 一一对应解码；**未知转义 / 非法字节一律报错**
  （子集外 = 不反向、透传）。

---

## 3. 语义（类型格 + 求值规则定死）

### 3.1 类型格（子集类型 ↔ ncoda ↔ canvas 声明）

| 子集类型 | ncoda 类型 | canvas 声明 | 说明 |
|---|---|---|---|
| str | string | string | `str(x)` / `.strip()` 产物 |
| int / float | integer / float | number | 数值字面量 / `int(x)` |
| bool | boolean | boolean | `True`/`False`、比较、`not` |
| list[T] | array<T> | list + items | 数组字面量、推导式、`split` |
| dict | object / record | object | 守卫 `{}`、`.get` |
| None | null（仅显式 null 字面量） | — | `None` ↔ `null` 字面量互逆 |
| any | any | — | 类型不可知 → 歧义消解时**保守透传**（不误伤） |

### 3.2 值语义

- **纯计算、零副作用**：shim 是表达式物化，无 IO / 无随机 / 无时间（random/sleep
  是记录在案的真残集 E9/E10，不进子集）。
- **确定性**：序列化字节稳定（缩进 4 空格、引号风格统一、dict 键排序发射）。
- **falsy 规则 = 平台沙箱 python 语义 ∩ ncoda 语义**：空串 / 空表 / 空字典 / None
  均 falsy；`not x` ↔ `empty(x)`（真值否定语义等价）。
- **null ↔ None**：`None` 字面量 ↔ ncoda `null` 字面量（仅显式 null；ncoda 语义
  模型不含 undefined，见 AGENTS.md null 契约）。
- **输入契约权威 = canvas inputParameters**（反编译端 `node.GetInputTypes()`）；
  **输出契约 = dict 键**（单键 `result` → 单端口；多键 → record）。
- **守卫语义**：越界兜底（`x[i] if len(x) > i else {}`）、类型守卫（isinstance）、
  防空（`(xs or [])`）都是编译器**自建**的确定性模式，反向按结构模式匹配还原。

---

## 4. 翻译模式·前向（ncoda → shim python）

> 权威表 = `pkg/pygen/pygen.go`（`LowerBuiltin` / `IsLowerable`）+
> `pkg/ir/pyast_lower.go`（lowerExpr / lowerFieldAccess / lowerCalcBuiltin /
> lowerArrayPyast / lowerMapPyast / lowerLambdaPyast / lowerJsonParseShim）。
> **输出字节被 golden / parity / 快照测试显式锁定**；表中"字节形态"即契约字节
> （`pkg/ir/testdata/golden/*.py`）。

### 4.1 确定性内建（13 个；+ §4.2 复杂内建 3 = pygen.LowerBuiltin 全集 16）

| ncoda | python 字节 | 反向 |
|---|---|---|
| `len(x)` | `len(x)` | `len` |
| `trim(x)` | `x.strip()` | `trim` |
| `join(xs, sep)` | `sep.join(xs)` | `join(items, sep)` |
| `to_string(x)` | `str(x)` | `to_string` |
| `int(x)` | `int(x)` | `int` |
| `float(x)` | `float(x)` | `float` |
| `replace(s, o, n)` | `s.replace(o, n)` | `replace(a, b, c)` |
| `split(s, d)` | `s.split(d)` | `split(s, d)` |
| `div(a, b)` | `(a // b)` | `div` |
| `repeat(v, n)` | `[v] * n` | `repeat` |
| `concat(a, b)` | `(a + b)` | `a + b`（二元 +，语义等价） |
| `contains(xs, x)` | `(x in xs)` | `in`（array）或 `contains`（string） |
| `range(a, b)` | `list(range(a, b))` | `range` |
| `empty(x)` | `(not x)` | `empty`（ncoda `!` 要求 bool，empty 接受 any） |

### 4.2 复杂内建 3（结构模式，反向按字节模式匹配）

| ncoda | python 字节 |
|---|---|
| `first_non_empty(xs)` | `next((str(u) for u in (xs or []) if u and str(u).strip()), "")` |
| `flatten(xs)` | `[str(x) for g in (xs or []) if isinstance(g, list) for x in g]` |
| `enumerate(xs)` | `[{"index": i, "value": v} for i, v in enumerate(xs)]` |

### 4.3 字面量 / 运算符 / 结构

| ncoda | python 字节 | 备注 |
|---|---|---|
| 整数 / 浮点 / 布尔 / null / 字符串 | `1` / `1.5` / `True` / `False` / `None` / `"..."` | 字符串转义族 §2.4 |
| 标识符引用 | `NameExpr` | 参数名 = 绑定名（ident）或 argN（无 ident 语义） |
| 数组字面量 | `[a, b, c]` | `lowerArrayPyast` |
| map / record 字面量 | `{"k": v, ...}` | `lowerMapPyast`；dict 键排序发射 |
| 字段访问 | `x.f`（Attribute）或扁平化 Name | `lowerFieldAccessPyast` |
| 索引 | `x[k]`（Subscript） | `lowerArrayPyast` 元素 |
| 三元 | `(T if C else E)` | `lowerShimExpr` TernaryExpr |
| 值合并 `a \|\| b` | `(a or b)` | coalesce；`(xs or [])` 防空同族 |
| 模板串 `` `a${x}b` `` | `"a" + str(x) + "b"` | flat ConcatExpr，无括号 |
| json.stringify | `import json` + `json.dumps(arg1, ensure_ascii=False)` | 多语句形态，不打标 |
| json.parse<T> | `import json` + `data = json.loads(arg1)` + 字段提取 | 多语句形态，不打标 |

### 4.4 json 守卫（数组元素 / 字段提取）

| ncoda | python 字节 |
|---|---|
| `arr[i]` | `(arr[i] if len(arr) > i else {})` |
| `arr[i].field` | `str((arr[i] if len(arr) > i else {}).get("field") or "")` |

### 4.5 包装协议（模块头）

```
def main(p1, p2, ...) -> dict:      ← 同步函数，唯一入口 main，注解固定 -> dict
    <body>                          ← 零副作用纯计算
    return {...}                    ← 单端口 {"result": E} / 多端口 {k: v, ...}
```

- **import 自足**：`import json` 在 `def main` 之前（之后是语法错误）；缺 import 是
  运行时 NameError，静态 gate 抓不到——P5.12 红绿测试锁定的纪律。
- **参数命名**：ident 引用用变量名；无 ident 语义的绑定（子表达式物化 / json 单入参）
  用 `arg1, arg2, ...`；buildMap 混合命名见 PYTHON-SHIM-TRANSLATION-CONTRACT §2.1。
- **端口名 = ncoda 输出字段名**；"result" 是单端口约定名（下游按
  `singleTypeKey(OutTypes)` 解析）；coze 端保留 canonical "output" 端口。

---

## 5. 翻译模式·反向（shim python → ncoda）

> 权威实现 = `platforms/cozebiz/decompile/shim_reverse.go`（`tryReverseShim` →
> `mapShimExpr` 递归映射）。反向引擎是**判定器**：全部满足才反向，任一不满足 →
> 透传（按用户 foreign code 处理，不打开盒子）。

### 5.1 反向判定链（全部满足才反向）

1. 代码以 `pyast.ShimMarker`（`# ncoda:shim 1\n`）开头；
2. 模块形态严格符合 §2.1（**无 import、body 仅一条 return**；多语句 / 多端口 /
   函数内 import 一律不透传反向）；
3. 表达式完全落在 §2.2 产物全集（§4 前向表镜像）；
4. 歧义点类型可判定（§5.4 表；类型不可知 → 透传，保守正确不误伤）。

> 注：`tryReverseShim` 的多键 / 非 `result` 键 dict 也走反向（G8 buildMap：dict
> IS the record，整体逆译为 record 字面量）——这是**多端口 record 反向**，与
> "多语句 shim 不承诺可逆"不冲突（§5.3 边界）。

### 5.2 打标范围（marker 契约）

- **打标**：编译器生成的**单表达式 shim**（`def main(<params>) -> dict:` + 单条
  `return {"result": <expr>}`，无 import、无中间语句）。
- **不打标**：多语句 shim（json.parse / json.stringify）、多端口 shim（buildMap）、
  listcomp（map-lambda，v2 候选）——不承诺可逆或暂不纳入，**不保证打标 / 打标但
  反向失败时透传**。
- 打标即改字节：golden / parity / 快照测试显式锁定，语义不变。

### 5.3 反向边界（透传清单）

| 形态 | 处理 |
|---|---|
| 无 marker | 透传（用户 foreign code） |
| marker 但 ParseModule 失败 | 透传 |
| marker 但有 import / 非单条 return | 透传（多语句形态） |
| 表达式落在子集外（GeneratorExpr 裸现、`not in`、未知方法、`isinstance`/`enumerate` 裸现等） | 透传 |
| 歧义点类型不可知 | 透传（保守正确，不误伤） |
| dict 键非字符串字面量 | 透传 |
| 多键 / 非 result 键 dict | 反向为 record 字面量（buildMap 形态，§5.1 注） |

### 5.4 逐条反向映射表（§4 的镜像）

| python 字节 | 条件 | 反向 ncoda |
|---|---|---|
| 字面量（Int/Float/String/Bool/None） | 确定 | 对应 ncoda 字面量（None ↔ null） |
| `NAME` | 参数已绑定 | 上游绑定表达式（反编译环境变量） |
| `NAME` | 未绑定 | `IdentExpr{Name}`（编译器产物外的自由名保守透传） |
| `(a in b)` | b: array | `a in b`（二元 in） |
| `(a in b)` | b: string | `contains(b, a)`（内建子串） |
| `(not x)` | 任意 | `empty(x)` |
| `(a or b)` / `(a and b)` | 任意 | `a \|\| b` / `a && b` |
| flat `"a" + str(x) + "b"`（ConcatExpr） | 首尾字面量 | 模板串 `` `a${x}b` `` |
| `(a + b)`（BinaryExpr） | 任意 | `a + b`（二元 +；concat 内建落此，语义等价） |
| `(a // b)` | 任意 | `div(a, b)` |
| `[v] * n` | 任意 | `repeat(v, n)` |
| `list(range(a, b))` | 任意 | `range(a, b)` |
| `next((str(u) for u in (xs or []) if u and str(u).strip()), "")` | 字节模式 | `first_non_empty(xs)` |
| `[str(x) for g in (xs or []) if isinstance(g, list) for x in g]` | 字节模式 | `flatten(xs)` |
| `[{"index": i, "value": v} for i, v in enumerate(xs)]` | 字节模式 | `enumerate(xs)` |
| json 守卫 `str((arr[i] if len(arr) > i else {}).get("f") or "")` | 结构模式 | `arr[i].f`（MemberExpr） |
| json 守卫 `(arr[i] if len(arr) > i else {})` | 结构模式 | `arr[i]`（IndexExpr） |
| `len(x)` / `str(x)` / `int(x)` / `float(x)` | 确定性 | `len` / `to_string` / `int` / `float` |
| `x.strip()` / `sep.join(xs)` / `s.split(d)` / `s.replace(o, n)` | 确定性 | `trim` / `join` / `split` / `replace` |
| dict 字面量（多键） | 键均字符串 | record 字面量（buildMap 形态） |

### 5.5 类型驱动歧义消解表（同一字节可有多个 ncoda 源）

| python 字节 | 条件（canvas 输入类型） | 反向 ncoda |
|---|---|---|
| `(a in b)` | b: array | `a in b`（二元 in） |
| `(a in b)` | b: string | `contains(b, a)` |
| `(a in b)` | b: 类型不可知 / any | **透传** |
| `not x` | 任意 | `empty(x)`（`!` 要求 bool；empty 接受 any；真值否定语义等价） |
| 静态数组字面量 `(a in [..])` | 右操作数静态 list | `a in b`（二元 in） |
| 静态字符串 `(a in "..")` | 右操作数静态 string | `contains(b, a)` |

---

## 6. 互逆不变量（固定点，验证门禁）

```
对封闭子集表达式 E：
    compile(E)  = S（带标记 shim 字节）
    reverse(S)  = E（ncoda 表达式）
    compile(reverse(S)) == S    字节级（golden 锁定）
    reverse(compile(E)) == E    ncoda 级（shim_reverse_test.go）
```

| 层 | 工具 | 守什么 |
|---|---|---|
| 单元·前向 | `pkg/ir/*_test.go`（Parity 系列 / golden） | compile 输出 = 契约字节（含 marker） |
| 单元·反向 | `platforms/cozebiz/decompile/shim_reverse_test.go` | 每 golden 形态 reverse → 期望 ncoda 表达式 |
| 固定点 | 同上 | compile(reverse(S)) == S（字节级双向闭合） |
| 运行时 | `pkg/ir/exec_shim_test.go`（7 形态 × 16 断言） | shim 在 CPython 独立可执行（import 自足） |
| 集成 | decompile 单测（构造 CodeRunner 图） | 标记 shim → ncoda 原生表达式；未标记 → 透传 |
| 语料 | `scripts/cozebiz_full_verify.py`（657 全量） | decompile→compile→redecompile 全部 SUCCEEDED/OK；round3 变化仅来自 shim 还原 |
| 平台 | 27 语料边等价（MATCH=27, DIFF=0）+ 快照冻结更新 | 边集合不变、代码字节漂移被测试显式锁定 |
| 不变量 | 657 语料全量扫描：`# ncoda:shim` 只出现在编译输出（201/657），round1/round3 反编译产物 657×2 零泄漏 | ncoda 层不存在 shim-python-code |

---

## 7. 与既有契约的关系（无冲突声明）

- 本文件是 **shim-python-subset 的语法/语义/翻译模式权威**；
- `docs/PYTHON-SHIM-TRANSLATION-CONTRACT.md` 是**前向签名协议与 legacy 回退登记**
  （§0-§7）+ **可逆机制与 marker 契约**（§8）+ **定位标准**（§9）——其中 §8 的
  歧义消解表与本文件 §5.4/§5.5 一致（本文件收敛升级）；
- `docs/ideas-todo/SHIM-REVERSIBLE-DSL.md` 是**立项规划**（P1-P5 已完成，历史文档）；
- `docs/ideas-todo/PYTHON-SUBSET-GENERATOR.md` 是**生成器迁移记录**（P5.1-5.13，
  历史文档）；
- `语言标准仓/spec/docs/user/CONCEPTS.md` 是**语言规范层的用户视角
  陈述**（shim 对用户无感；shim 不在 grammar.ebnf 内）——与本文件不冲突：
  nodecoda-spec 是 ncoda 语言的规范（不含 shim 语法），本文件是编译器内部子集的
  规范（shim 语法定死处）。

---

*定稿：2026-09-03 · godcc · shim-python-subset 翻译模式规范 v2.0（语法 EBNF 定死 +
语义 + 前向/反向翻译表 + 互逆不变量 + 封闭契约）*
