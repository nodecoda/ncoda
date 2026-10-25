# ncoda 插件编写规范（v0.1 DRAFT）

状态：**draft**（可实施，条款仍可改） · 适用仓：`nodecoda/ncoda` · 关联：
`docs/LIBRARY-BINDING-SPEC.md`（机制总纲 §14）· `pkg/pluginhost/`（实现）

> 定位：LIBRARY-BINDING-SPEC 回答「为什么要这条通道、账怎么记」；
> **本文回答「怎么写一个插件」**——清单、协议、实现契约、测试、发布。
> 两者冲突时以 LIBRARY-BINDING-SPEC 的不变量为准，本文只细化实现层。

---

## 0. 三条不可协商的前提

1. **插件不扩大语言面。** 能实现什么，完全由 `libraries/<lib>.ncoda` 决定。
   插件不能声明 header 里没有的 library/function；声明了直接拒绝加载。
2. **插件不是内置。** runtime 二进制不含实现代码；没有 `--plugin`/`--plugin-dir`
   时，插件符号不存在，调用会得到 `RBINDING_MISSING`（rc=1），不会静默降级。
3. **插件不得伪造保真度。** 行为与原语义有差距就必须 `partial`/`stub` 并写 `loss`。
   `real` 的含义是「这些输入下行为已由判据固定」，不是「看起来对」。

---

## 1. 五分钟上手

```
plugins/hello/
├── ncoda-plugin.yaml     # 清单：我是谁、我能实现什么
├── server.py             # 实现：stdio JSON-RPC
├── test_server.py        # 判据：函数级正/负控
└── README.md             # 证据：语义从哪来、loss 是什么
```

```yaml
# ncoda-plugin.yaml
schema: ncoda.plugin/v1
id: hello
version: 0.1.0
protocol: ncoda-plugin/1
command: ["python3", "server.py"]
libraries:
  - library: array_count
    fidelity: real
    functions: [array_count]
```

```python
# server.py — 最小骨架
import json, sys

def send(msg):
    sys.stdout.write(json.dumps(msg, ensure_ascii=False) + "\n")
    sys.stdout.flush()

def main():
    for line in sys.stdin:
        if not line.strip():
            continue
        msg = json.loads(line)
        if "id" not in msg:
            continue                      # notification: cancel
        mid, method = msg["id"], msg.get("method")
        if method == "initialize":
            send({"jsonrpc": "2.0", "id": mid, "result": {
                "protocol": "ncoda-plugin/1", "id": "hello", "version": "0.1.0",
                "libraries": [{"library": "array_count",
                               "functions": ["array_count"]}]}})
        elif method == "invoke":
            p = msg.get("params") or {}
            args = p.get("args") or {}
            send({"jsonrpc": "2.0", "id": mid,
                  "result": {"value": {"count": len(args.get("array") or [])}}})
        elif method == "ping":
            send({"jsonrpc": "2.0", "id": mid, "result": {"ok": True}})
        elif method == "shutdown":
            send({"jsonrpc": "2.0", "id": mid, "result": {}})
            return

if __name__ == "__main__":
    main()
```

```bash
go build -o /tmp/ncoda-run ./cmd/ncoda-run
/tmp/ncoda-run --plugin-dir plugins --tool-path ./libraries --list-symbols
/tmp/ncoda-run --plugin-dir plugins --tool-path ./libraries app.ncoda; echo rc=$?
```

---

## 2. 清单参考

| 字段 | 必填 | 类型 | 规则 |
|---|---|---|---|
| `schema` | ✅ | string | 精确 `ncoda.plugin/v1` |
| `id` | ✅ | string | `[a-z0-9][a-z0-9_-]*`，全局唯一；握手必须回报同一个 |
| `version` | ⭕ | string | 给了就与握手比对 |
| `protocol` | ✅ | string | 精确 `ncoda-plugin/1` |
| `type` | ⭕ | string | 只允许 `stdio`（默认） |
| `command` | ✅ | []string | argv，不经 shell；相对路径基于清单目录 |
| `cwd` | ⭕ | string | 相对清单目录 |
| `timeout_ms` | ⭕ | int | 单次 `invoke` 默认上限，默认 30000 |
| `max_restarts` | ⭕ | int | 60s 窗口内允许的重启次数，默认 3 |
| `env` | ⭕ | map | 追加到白名单环境变量（不覆盖宿主 `PATH` 等） |
| `libraries[].library` | ✅ | string | 必须存在于 `libraries/<lib>.ncoda` |
| `libraries[].version` | ⭕ | string | 仅记录 |
| `libraries[].fidelity` | ⭕ | enum | `real` / `partial` / `stub`，默认 `real` |
| `libraries[].loss` | 条件 | string | `partial`/`stub` 必填且非空 |
| `libraries[].functions` | ✅ | list | 每项是**函数名**或 `{name, fidelity, loss}`；必须在该 header 里声明，且清单内不重复 |

**函数级 fidelity（v1 扩展，库级为其默认值）**

```yaml
functions:
  - get_url                      # 继承库级 fidelity/loss
  - name: wenan_split
    fidelity: partial            # 覆盖库级
    loss: "5 段切分策略未证实"     # partial/stub 必须有（函数级或库级）
```

规则：函数级 > 库级；未写 `fidelity` 就继承；`partial`/`stub` 的 `loss`
可以写在函数项上，也可以写在库上（库级 loss 覆盖该库全部函数）。
同一个函数只能出现一次（字符串与映射重复也是错）。

**加载期校验失败的退出码是 2（用法错）**，不是 1。理由：清单是输入，不是程序错误。

---

## 3. 协议参考（`ncoda-plugin/1`）

- 传输：子进程 stdin/stdout，**UTF-8**，**一行一条 JSON**（`\n` 结尾，不允许跨行美化）。
- `stderr` 归插件写日志，宿主只在报错时附上末尾若干字节。**禁止写 stdout 日志。**
- 编号：`id` 为整数，由宿主单调递增；`cancel` 是 notification（无 `id`）。

### 3.1 时序

```
宿主                                  插件
 │  spawn(command, env=白名单)  →       │
 │  ── initialize {protocol,...} ──→    │
 │  ←── {protocol,id,version,libraries} │  ← 双向精确比对，少/多都拒
 │  ── invoke {symbol,args,deadline_ms}→│
 │  ←── {value} | {error:{data:{class}}}│
 │  ── cancel {id}（notification）──→   │  ← 仅超时时
 │  ── shutdown ──→                     │
 │  ←── {}                              │  → 进程应退出
```

### 3.2 方法表

| 方法 | 参数 | 结果 |
|---|---|---|
| `initialize` | `protocol`, `runtime{name,version}`, `artifact_dir`, `symbols[]` | `protocol`, `id`, `version`, `libraries[{library,functions[]}]` |
| `invoke` | `symbol`（`"lib.fn"`）, `args`（对象）, `deadline_ms` | `{"value": <任意 JSON>}` |
| `cancel` | `{"id": <invoke id>}`（notification） | 无 |
| `ping` | — | `{"ok": true}` |
| `shutdown` | — | `{}` |

### 3.3 错误模型

```json
{"jsonrpc":"2.0","id":9,"error":{"code":-32000,"message":"boom","data":{"class":"not-implemented"}}}
```

- `data.class` 会以 `class: message` 形式前置进宿主错误串，是**失败归因**的落点。
- 允许的 class 见 LIBRARY-BINDING-SPEC §8 闭集（`not-implemented` / `invalid-args` /
  `remote-url-unsupported` / `missing-input` / `runtime-error` …）。**禁止 `other`。**
- 可恢复的业务失败**不要**用 JSON-RPC error：放进 `value` 的声明形状
  （`isSuccess=false` / `errorBody`），让语言侧的 `failure_branch` 处理。

### 3.4 超时与取消

| 阶段 | 行为 |
|---|---|
| 计算 deadline | `ctx` deadline > 清单 `timeout_ms` > 30000ms |
| 下发 | `deadline_ms` 仅作**提示**；宿主自己计时 |
| 超时 | 发 `cancel` → 宽限 **300ms** → `kill` |
| 之后 | 本次 invoke 返回错误；**下次调用自动重启** |
| 崩溃循环 | 60s 内重启 > `max_restarts` ⇒ `disabled`，后续调用立即报错 |

插件应**尽力**响应 `cancel`（清理临时文件、提前返回）；不响应也不会挂住宿主。

---

## 4. 实现契约（P1–P7）

| 编号 | 约定 |
|---|---|
| P1 | `args` 键 = header 参数名；`$` 前缀去掉（`$output` → `output`）；缺省参数按 header 默认值补齐 |
| P2 | 只返回 header **声明过**的结果键；多发的键会成为隐式契约。wire 键用**端口名**：`$` 前缀剥掉（`$output` → `output`，`$code` → `code`），与 P1 的参数规则同源；返回 `"$output"` 会让语言侧 `v.$output` 读不到字段 |
| P3 | 类型对齐 header：`int` 不发浮点（`3.0` 要写成 `3`），`array<string>` 不发对象 |
| P4 | 可恢复失败 → 声明形状的失败值；必须触发错误策略才用 JSON-RPC error |
| P5 | 必须实现 `ping`；收到 `shutdown` 后 1s 内退出 |
| P6 | 不读写清单以外的东西（配置从 `args`/`env` 来）；不假设 cwd 是仓库根 |
| P7 | 确定性：同 `args` + 同 `artifact_dir` ⇒ 同 `value` + 同产物路径（文件名带内容哈希） |

### 4.1 类型映射

| header | JSON（wire） | Python | Go |
|---|---|---|---|
| `string` | string | `str` | `string` |
| `int` | number（整数） | `int` | `int64` |
| `float` | number | `float` | `float64` |
| `bool` | boolean | `bool` | `bool` |
| `array<T>` | array | `list` | `[]any` |
| `$name` 结果口 | `<name>`（剥 `$`） | `<name>` | `<name>` |
| 记录类型 | object | `dict` | `map[string]any` |
| 缺省 `= 0` / `= ""` | **可能缺失** | 用 `args.get(k) or default` 兜底 | 零值判断 |

> `= 0` 在 header 里是「无默认值」的写法，不是「默认零」。插件必须容忍键缺失。

### 4.2 环境输入

插件只能从三处获取环境输入：`args`、白名单 env、`NCODA_ARTIFACT_DIR`。
时钟、随机、网络**不得**隐式使用：需要时钟就接受参数；需要网络就保持 unbound。

---

## 5. 保真度与 loss

| 档 | 含义 | 允许的偏差 |
|---|---|---|
| `real` | 契约内行为由判据固定 | 无。未覆盖的输入必须报 `invalid-args`，不得猜 |
| `partial` | 主路径可用，某些能力缺失/近似 | 必须写 `loss`，且**不得**让调用方误以为完整 |
| `stub` | 只保证形状，不保证语义 | 必须写 `loss`；仅用于让错误尽早、可控地暴露 |

`loss` 写法：一句话说清「丢了什么 + 影响谁」。

```yaml
- library: capcut_data
  fidelity: partial
  loss: "wenan_split 按连续等分切 5 段；原实现的分配策略未由语料证实，长文分段边界可能不同"
```

**禁止**：为消灭 unbound 数字把 `stub` 写成 `real`；把 `loss` 写成 `""`；
用 `partial` 掩盖「其实没实现」。

---

## 6. 产物与确定性

- 产物只写 `NCODA_ARTIFACT_DIR`（宿主创建并传入）；不存在就报 `missing-input`。
- 文件名 = `<内容哈希>.<ext>`，同输入重复调用必须命中同一路径（幂等）。
- 不得写临时文件到 cwd；需要时用 `TMPDIR`。
- 返回给语言的应是**路径或 URL 字符串**，不是二进制。

---

## 7. 安全模型（务必按最坏情况读）

- **不是沙箱**：插件以当前用户权限运行，可读写该用户能访问的一切。
- 只加载可信插件；第三方插件建议放进容器/独立用户。
- env 白名单：`PATH HOME TMPDIR TEMP TMP LANG LC_ALL SYSTEMROOT USERPROFILE PYTHONPATH`
  + 清单 `env` + `NCODA_*`。**凭证不会自动传入**，需要就在清单 `env` 里显式给（不推荐）。
- 网络：runtime 不提供网络能力抽象；插件自己联网属插件行为，须在 README 声明。
- 插件不能改语言面，也不能改账本；它只回答 `invoke`。

---

## 8. 测试要求（合并前必须全绿）

**函数级（插件仓内）**

1. 每个 `real` 函数至少 1 正控 + 1 负控（缺参、错类型、越界）。
2. 每个 `partial` 函数的 `loss` 必须有对应判据证明「已知偏差就是这样」。
3. `initialize` 回报的符号集合必须与清单**完全相同**（有测试固定）。

**宿主级（本仓）**

4. `--list-symbols` 能看到 `lib/fn` 别名与 `lib.fn` 符号。
5. 端到端：真二进制跑一个 `import "<lib>"` 的源码，rc=0 且值正确。
6. 负控：清单非法（rc=2）、握手不符（rc=1）、与内建同符号（rc=1，不覆盖）。
7. 崩溃/超时：杀进程后下次调用能重启；超过 `max_restarts` 后 `disabled`。

```bash
go test -count=1 ./pkg/pluginhost/ ./pkg/runner/
python3 plugins/<id>/test_server.py
make plugin-selftest
```

---

## 9. 版本与兼容

- `ncoda-plugin/1` **冻结**：新增可选字段可以，改语义不行。破坏性变更 ⇒ `/2`。
- 清单 `schema` 同理：`ncoda.plugin/v2` 出现前，v1 解析器必须继续工作。
- 降级策略：宿主不认识的清单字段**忽略**，不认识的 `fidelity` 值**拒绝加载**。

---

## 10. 反模式（review 直接打回）

| 反模式 | 为什么不行 |
|---|---|
| 插件里读语料/仓库文件决定行为 | 把考题变成实现依赖，换语料就漂移 |
| `if platform == "coze"` 分支 | 平台名不得进内核（audit S1–S7） |
| 找不到实现就返回空值 | 假绿；应返回 `not-implemented` error |
| 改 header 让自己「能声明」 | 语言面由 L1 决定，不由实现决定 |
| 把 `partial` 写成 `real` | 账本失真，下游误判完备 |
| 在 stdout 打日志 | 破坏 framing，宿主解析失败 |
| 捕获所有异常后返回 `{}` | 失败不可见，违反 H3/P3 |

---

## 11. 检查表（提交前逐条打勾）

- [ ] 清单 `id`/`version` 与握手一致；协议字符串精确
- [ ] 每个 library 都在 `libraries/*.ncoda` 存在，每个 function 都已声明
- [ ] `partial`/`stub` 都写了非空 `loss`（函数级或库级）
- [ ] `args` 缺失键有兜底；未声明键不读取
- [ ] 结果只含声明键；整数不发浮点
- [ ] 可恢复失败走 `value`，契约失败走 `error` + 闭集 `class`
- [ ] 响应 `ping`；`shutdown` 后退出
- [ ] 产物写 `NCODA_ARTIFACT_DIR`，文件名带内容哈希
- [ ] README 写清：语义证据来源（语料调用点/消费面）、已知 loss、是否联网
- [ ] `python3 plugins/<id>/test_server.py` + `go test ./pkg/pluginhost ./pkg/runner` 全绿
- [ ] `--list-symbols` 数目与清单一致

---

## 12. 试点记录（已发布 5 个插件 / 58 个符号）

| 插件 | 实现的库 | 符号 | `real` | `partial` | 关键证据 |
|---|---|---:|---:|---:|---|
| `plugins/capcut_data` | `capcut_data` | 9 | 7 | 2 | 语料调用点 + 消费面（`get_url` 的 `outputList` 被 `yield` 成 batch 记录） |
| `plugins/text_toolkit` | `string_to_array` `link_extract` `url_get` `text_split_master` `text_split_tool` `null_fill` `array_split_merge` `kimi_chat_parser` | 10 | 4 | 6 | 纯文本/数组/解析变换，不触网；`kimi_chat_parser` 输入格式见 loss |
| `plugins/compose_toolkit` | `capcut_helper` `suchuang_video_compose` | 18 | 3 | 15 | `corpus/7580659724749865001`、`7576926169774653449` 等；纯列表/时间轴变换 |
| `plugins/codel_toolkit` | `codel_video` | 2 | 0 | 2 | 语料无调用；形状来自 header（纯列表变换） |
| `plugins/timeline_toolkit` | `capcut_draft_data` `capcut_data` `capcut_data_fast` | 19 | 0 | 19 | header 返回具体结构 + 语料调用点；`timelines` / `str_to_list` / `caption_splitter` / `timeline_keyframe` / `asr_timelines`，分配/断句规则未证实 |

**共享实现、独立契约**：`capcut_helper` / `suchuang_video_compose` 的同名函数
（`text_splitter` / `timeline_merge` / `wenan_timeline_range` / `audio_link_collector`）
复用同一份内部实现，但按各自 header 的输出形状适配；每个库有独立判据。
`partial` 一律用**函数级 fidelity** 声明（清单 `functions: [{name, fidelity, loss}]`），
与同库 `real` 共存；`complete_real` 不因 partial 上升。

**保持 unbound（有证据的拒绝，不是遗漏）**：
- `capcut_data` / `capcut_data_fast`：语料调用的符号已全部绑定（`*_infos` 的 schema
  由语料手写结构固定；样式字段不写入 infos，见 loss）；`search_sticker` 无语料调用，
  仍是网络服务；
- `capcut_draft_data` 的 `text_infos` / `video_infos` / `keyframe_infos`：消费面
  （`add_texts/add_videos/add_keyframes({infos})`）没有语料手写等价结构，键名未证实；
  `audio_infos` / `image_infos` 有，已绑定；
- `suchuang_video_compose` 的 `audio_timelines` / `video_timelines` /
  `get_audio_duration`：依赖服务端音频/视频探测；
- `codel_video.keyframe_ani_scene_v1`：收 `api_token`、返回 `tip_url`，远程服务。

**发布判据**：`pkg/pluginhost/plugins_manifest_test.go` 扫描 `plugins/`，校验
schema/protocol/type、id 与 `<lib>.<fn>` 全仓唯一、header 归属、`command` 引用的
`.py` 存在、`partial` 必带 `loss`、清单数只升不降；有 `python3` 时逐个真实
`initialize`，符号集合必须与清单完全一致。`make dist` 打包全部 `plugins/*`，
`dist-check` 逐目录断言清单 + `server.py`，`release-smoke` 从发行版
`--plugin-dir` 断言 `plugin_hosts` 等于 `plugins/*/ncoda-plugin.yaml` 数量并真跑一次调用。

## 13. 未决项

1. ~~`plugins/` 是否随发行版打包~~ **已定**：随 `make dist` 打包为源码（清单 + 参考实现），
   `dist-check` 断言文件存在、`release-smoke` 用发行版里的 `--plugin-dir` 真跑一次；
   需要系统 `python3`，缺失时该断言跳过而不是判过（日志留痕）。
2. ~~选择性关闭内建库~~ **已定**：`--local-plugins=<lib,…>`（裸 flag = 全开），
   用于让插件接管单库而不必放弃其他内建库。
3. 插件签名机制（当前无签名；`SHA256SUMS` 只保证发行包完整性，信任仍靠人工 review）。
4. `type: http` 是否纳入本规范（现在是另一条通道，接口不同）。
5. 函数级 fidelity 的 `loss` 是否允许按「已知偏差清单」分段（现在是单句）。
