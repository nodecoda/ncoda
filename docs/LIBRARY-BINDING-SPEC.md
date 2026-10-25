# 库函数绑定与 runtime 实现机制（v1）

状态：**active** · 适用仓：`nodecoda/ncoda`（开源内核） · 账本：`docs/library-coverage-baseline.json`

本文定义「语料里出现的 `import "<lib>";` + `lib.fn(...)` 如何在开源 runtime 上得到实现」。
它回答四个问题：

1. 缺的到底是什么（语言声明？runtime 实现？平台适配？）
2. 一个实现要满足什么，才能被记为 `real`
3. 近似实现如何登记，才不会把 `ok` 伪装成 `ok_real`
4. 新增一个库的固定流程与可打红的判据

---

## 0. 一句话

**语言层只声明，runtime 层才实现。** capability header（`libraries/*.ncoda`）是签名正本；
`pkg/localtools/bindings.yaml` 是「符号 → 实现」的唯一注册表；实现代码只在开源仓，
平台物理 id 只在商业仓。任何一层缺失都必须显式表现为「未绑 / 判不了」，不得降级成通过。

---

## 1. 四层定位

| 层 | 本机制中的角色 | 正本 |
|---|---|---|
| L1 语言 | `library` + `function` 声明（签名、结果类型、`@capability` 政策位） | `libraries/*.ncoda` |
| L2 runtime | 符号 → 进程内实现；参数解包、结果成形、产物落盘 | `pkg/localtools/**` + `bindings.yaml` |
| L3 语料 | 考题：真实调用面与结果字段消费 | `../ncoda-corpus/corpus`（只读） |
| L4 平台 | 平台物理 id → 符号的别名 | 商业仓 `internal/localtools/platform/` |

**禁止反向依赖**：L2 不得知道平台 id；L1 不得知道实现是否存在；L3 不得定义语言。
平台不支持 ≠ 语言错误。文件「未绑」是 L3 覆盖缺口，不是失败。

### 1.1 实测缺口（2026-10-25，开工前 HEAD `0118c98` = v0.7.2）

```
语料            869 例（有 import 800）
被 import 的库   169
被调用的符号     310
头文件已声明     310 / 310   ← L1 无缺口
已绑定符号        21
未绑定符号       289（分布在 154 个库）
```

插件补齐后（同一语料，工作树）：

```
已绑定符号        88（内建 32 + 进程外 56）
未绑定符号       224（remote-api 154 / server-state 38 / schema-unknown 6 / env-missing 23 / local-todo 3）
local-todo         3（audio_timeline_merge.timeline_merge + video_image_split 的 2 个：纯算术，gap 插入位置/槽位顺序待证）
```

结论：**「缺失的库-函数」全部是 L2 实现缺口，不是语言声明缺口。**
这正是本机制要解决的对象。

---

## 2. 术语

| 术语 | 定义 |
|---|---|
| **library** | header 里的 `library <name> @ <ver>;`，也是源码 `import "<name>";` 的名字 |
| **symbol** | `<library>.<function>`，如 `tts.speech_synthesis`；**绑定与记账的最小单位** |
| **capability** | 绑定表的 `capability` 列，通常是符号本身；也允许已有的 logical name（如 `local_database/insert`） |
| **binding** | 一行 `provider/operation → capability + implementation [+ loss]` |
| **implementation** | `real` / `partial` / `stub` 三档保真度（见 §3） |
| **loss** | `partial`/`stub` 丢掉了什么；**必填**，空字符串即 schema 违规 |
| **environment input** | 时钟、随机、网络、凭证、外部文件等不属于“纯函数”的输入；必须显式注入（见 §6） |
| **covered file** | 文件调用的**每个** symbol 都有绑定（`complete_exec`） |
| **real-covered file** | 文件调用的每个 symbol 都是 `real`（`complete_real`） |

> 注意：商业仓 `corpus_runtime_sweep.py` 目前的 `complete` 是**库级**（某 provider
> 至少一条绑定即算完备）。本规范要求以**符号级**为准；跨仓切换需先报批，
> 见 §10。

---

## 3. 保真度分级（唯一允许的三档）

| 档 | 定义 | 允许的典型场景 | 记账 |
|---|---|---|---|
| `real` | 按 header 语义完整实现；确定性；不触网 | 纯函数、本地文件解析、SQLite、边界 keying | 计入 `complete_real` / `ok_real` |
| `partial` | 结果形状/类型满足，但语义是可复现的**近似**；loss 必填 | 模型/服务类（TTS、图像、视频、画布渲染）、被截断的等待 | 计入 `complete_exec`，**不计入** `complete_real` |
| `stub` | 只保证形状，不保证语义；用于迁移/占位 | 仅限显式标注的实验路径 | 不计入 `ok`；复跑命中记 `recovered` |

**红线**

- `partial`/`stub` 不写 `loss` ⇒ 注册失败（`validateBindings`）。
- 把 `partial` 写成 `real` 来抬高 `ok_real` ⇒ 违规；判据见 §9 NC3。
- 用平台行为给 `real` 背书 ⇒ 违规；`real` 只由开源仓实现 + 本地判据证明。

### 3.1 当前绑定分布

| 库 | 符号 | 档 | loss 摘要 |
|---|---|---|---|
| `database` / `matting` / `image_scale` / `image_generate` / `image_generate_fast` | 8 | real | — |
| `array_count` / `array_length` | 2 | real | — |
| `common_code_block` | 4 | real | — |
| `throw_error` | 1 | real | — |
| `datetime` | 1 | real | 时钟由 `Options.Clock` 注入 |
| `timer_wait` | 1 | partial | 等待被 `MaxSleep` 截断 |
| `audio_duration` | 1 | partial | WAV 精确 / MP3 估算 / 不联网 |
| `tts` | 1 | partial | 正弦提示音，非人声 |
| `jimeng_image_gen` | 1 | partial | prompt 哈希展陈图，非模型 |
| `drawing_board` | 1 | partial | 几何 + 文本盒，无字体/外链图/滤镜 |

---

## 4. 绑定表 schema（v2）

`pkg/localtools/bindings.yaml`：

```yaml
bindings:
  - provider: tts                 # 必须是 header 的 library 名
    operation: speech_synthesis   # 必须是该 header 声明过的 function
    capability: tts.speech_synthesis
    implementation: partial       # real | partial | stub
    loss: "不是人声模型：输出确定性正弦提示音 WAV"   # partial/stub 必填
```

### 4.1 校验规则（V1–V8，全部可红）

| 编号 | 规则 | 落点 |
|---|---|---|
| V1 | `provider` 是某个 header 的 `library` 名 | `TestBindingsNameDeclaredFunctions` |
| V2 | `operation` 是该 header 声明的 `function` | 同上 |
| V3 | `implementation ∈ {real, partial, stub}` | `validateBindings` |
| V4 | `partial`/`stub` 必须有非空 `loss` | `validateBindings` + NC1 |
| V5 | `(provider, operation)` 唯一 | `validateBindings` + NC2 |
| V6 | `capability` 已被 runtime 注册（`Alias` 目标存在） | `plugin.Manager.Alias` |
| V7 | 语言层不得出现平台物理 id（`plugin_*` / 裸数字） | `TestBindingsNameDeclaredFunctions` + 商业仓 B3/B4 |
| V8 | 同一符号只有一条注册路径（`func Register` 唯一） | 商业仓 B6 + 本仓 code review |

### 4.2 兼容性

- v1 行（只有 `provider/operation/capability/implementation`）继续合法。
- 新增键只有 `loss`；未知键不被读取，也不得改变语义。
- 平台层 `bindings-platform.yaml` 继续只收平台 id，且只能指向已注册的 capability。

---

## 5. 运行时调度

```
源码 lib.fn(args)
  → 前端：import 解析 + 类型检查（header 提供签名）
  → IR：tool 节点（provider=lib, operation=fn）
  → runtime：plugin.Manager.Call("lib/fn", args)
       ├─ 命中：Alias("lib/fn") → capability → Tool.Call
       └─ 未命中：RBINDING_MISSING（明确错误，绝不回退到 stub）
```

**解析顺序**（`pkg/runtime/plugin`）

1. 逻辑名 `<plugin-id>/<tool-name>`（存量）
2. 符号 `<library>.<function>`（`Tool.Capabilities`）
3. 绑定层别名 `<library>/<function>`（`bindings.yaml`）

三者必须在同一张表里可见：`Manager.Names()` 是排障入口。
未绑定时错误信息要给出可用名字与平台名提示，不允许只报 “not found”。

---

## 6. Handler 契约（H1–H8）

每个实现是一个 `plugin.CallFunc`：`func(ctx, map[string]any) (any, error)`。

| 编号 | 约定 |
|---|---|
| H1 | **参数键 = header 的参数名**（`$array` 归一为 `array`）。不得猜平台私有扁平键 |
| H2 | **只发 header 声明过的结果键**；多发的键会成为隐式契约 |
| H3 | 不 panic；可恢复失败返回**声明形状的失败**（`isSuccess=false` / `error` 字段） |
| H4 | 只有“调用方必须走错误策略”的失败才返回 Go error（如 `throw_error`，以触发 `failure_branch`） |
| H5 | 确定性：同输入 + 同环境输入 ⇒ 同输出、同产物路径。文件名必须带内容哈希 |
| H6 | 不触网；远程 URL 返回 `REMOTE_URL_UNSUPPORTED`，由调用方失败分支处理 |
| H7 | 尊重 `ctx`（超时/取消）；长等待必须可截断并如实报告 `actual_delay` |
| H8 | 实现包不知道平台名；平台 id 只出现在商业仓绑定表 |

### 6.1 环境输入

不属于纯函数的输入必须从 `Options` 注入，不得读全局状态：

| 输入 | 注入面 | 默认 |
|---|---|---|
| 时钟 | `Options.Clock` / `ncoda-run --clock ""|now|<RFC3339>` | 钉在 Unix epoch |
| 等待上限 | `Options.MaxSleep` / `--max-sleep` | 1s |
| 数据库文件 | `Options.DBPath` / `--local-db` | 进程内 `:memory:` |
| 产物目录 | `Options.ArtifactDir` / `--artifact-dir` | `$TMPDIR/ncoda-local-artifacts` |
| 网络/凭证 | **不提供** | 不联网；需要凭证的库只能 `unsupported` |

---

## 7. 覆盖度量与账本

工具：`scripts/library_coverage.py`（新增）· 基线：`docs/library-coverage-baseline.json`

```bash
python3 scripts/library_coverage.py            # 人读表
python3 scripts/library_coverage.py --json     # 机器读
python3 scripts/library_coverage.py --check    # 基线棘轮（rc 1 = 退化；rc 3 = 判不了）
make library-coverage                          # 同上
```

### 7.1 口径

**绑定来源有两处，语料账合并计算**：`pkg/localtools/bindings.yaml`（内建）与
`plugins/*/ncoda-plugin.yaml`（进程外，见 §14）。同一符号不得同时出现在两处
（运行期注册冲突，账本判红 `symbols_declared_twice`）。

| 字段 | 定义 |
|---|---|
| `complete_exec` | 所有被调用符号都有绑定（real/partial/stub）的文件数 |
| `complete_real` | 所有被调用符号都是 `real` 的文件数 |
| `symbols_unbound` | 被调用但无绑定的符号数 |
| `undeclared_symbols` | 被调用但 header 未声明的符号（L1 缺口，必须为 0） |
| `next_bindings` | 再绑一个符号能解锁的文件数 |

### 7.2 当前基线（进程外插件 + D-1 裁定后，2026-10-25）

```
before:      complete_exec 86  · complete_real 86 · symbols_bound 8  · unbound 302
after(local):complete_exec 168 · complete_real 86 · symbols_bound 21 · unbound 289
after(plugin):plugin_bound 8（capcut_data 7 real + 1 partial）        · unbound 281
after(D-1):  local 22（+ capcut_video_compose.get_audio_duration）    · unbound 280
after(fill): local 32 · plugin 38（4 个插件 / 38 符号）               · unbound 242
after(text): plugin 44（5 个插件 / 44 符号）                          · unbound 236
after(timeline): plugin 56（5 个插件 / 56 符号）                      · unbound 224
             complete_exec 171 · complete_real 86 · local-todo 3
```

- `complete_exec` 在插件/D-1 两步**没有变化**：这些文件同时依赖仍未绑的
  `*_infos` / `create_draft`，只多一个符号不足以让文件完备。这是正确读数。

- `complete_exec +82`：`drawing_board` + `jimeng_image_gen` + `tts` + `audio_duration`
  等绑定解锁的真实文件。
- `complete_real +0`：新增媒体类库是 `partial`，**不冒充 real**。这是预期结果，
  也是判据（见 NC3）。
- 语料规模不变（869），基线可比；规模变化 ⇒ rc 3，必须重钉。
- **测量环境必须写清**：本账用工作树 + `scripts/library_coverage.py`；
  商业仓 `corpus_runtime_sweep.py` 的基线 `ok=64` 是用商业 `cmd/ncoda-run`
  （schema-aware echo）跑出来的。开源 `ncoda-run` 的 echo 只回显文本，
  结构化 LLM 节点会 decode 失败 ⇒ 用开源二进制跑 sweep 得到的是另一组数，
  两组数**不可直接比较**。切换测量口径前先报批。

### 7.3 棘轮规则

- `complete_exec` / `complete_real` 只许升；`symbols_unbound` 只许降。
- 基线只能通过 `--write-baseline --reason "<证据>"` 重定，且必须在 PR 说明里引用。
- 语料规模变化 ⇒ 基线不可比 ⇒ rc 3（判不了 ≠ 通过）。

---

## 8. 失败归因（闭集扩展）

新增实现后，语料失败仍必须落进既有闭集；本机制新增/明确两类：

| 类别 | 含义 | 例子 |
|---|---|---|
| `binding-missing` | 调用了未绑符号 | 224 个未绑符号（原 289） |
| `binding-partial` | 已绑但近似实现的输出不足以满足下游 | TTS 时长/音色下游断言 |

原有 `code-runtime` / `code-syntax` / `deps-missing` / `external-http` /
`external-subworkflow` / `shim-value` 不变。**禁止新增 `other` 桶**。

---

## 9. 负控（必须能被打红）

| 编号 | 负控 | 期望 |
|---|---|---|
| NC1 | `partial` 绑定删掉 `loss` | 注册失败 / 测试红 |
| NC2 | 重复 `(provider, operation)` | 注册失败 |
| NC3 | 把 `tts` 改成 `real` | `ok_real` 假涨；评审必须拒绝（规范红线） |
| NC4 | 绑定指向未注册 capability | `Alias` 失败 |
| NC5 | 删除一个 `real` 绑定 | `TestBindingsNameDeclaredFunctions` 或覆盖账下降 |
| NC6 | 调用未声明函数 | `undeclared_symbols` 非空 ⇒ rc 1 |
| NC7 | 远程 URL 静默成功 | `TestAudioDurationLocalAndRemote` 红 |
| NC8 | 同时钟输入两次调用产物不同 | `TestTTSSpeechSynthesis...` / `TestJimeng...` 红 |
| NC9 | 坏 `canvas_schema` 触发 panic | `TestDrawingBoard...` 红 |
| NC10 | 语料缺失 / 头文件缺失 | rc 3，不得 0 |
| NC11 | 只绑 `a.f1`、调用同时含 `a.f2` | 文件不得算完备（`scripts/test_library_coverage.py`） |
| NC12 | `partial` 绑定 | `complete_real` 不得上涨 |

---

## 10. 跨仓状态（只读引用，未修改）

商业仓 `scripts/corpus_runtime_sweep.py`：

- `bound_libraries()` 以 provider 为粒度；绑一个函数即整库“完备”。
- `complete` 因此会**高估**可运行面；运行期仍按符号报 `RBINDING_MISSING`。
- 建议（需报批后实施）：把 `complete` 改成符号级，并引入 `complete_exec` /
  `complete_real` 两列，与本规范 §7 对齐。

在切换前，本仓以 `scripts/library_coverage.py` 为准；两套口径的差异必须显式记录。

---

## 11. 新增一个库的 SOP

1. **确认声明**：`libraries/<lib>.ncoda` 存在，且 `function` 与语料调用一致。
2. **取调用面**：`python3 scripts/library_coverage.py --json`，看该库的 `called/missing`。
3. **定档**：按 §3 判定 `real` / `partial` / `stub`；`partial` 先写 `loss`。
4. **写实现**：`pkg/localtools/<lib>.go`（或并入 `portable.go` / `media.go`）。
   - 参数键跟 header；结果键只发声明字段；产物带内容哈希。
5. **注册**：`plugin.Plugin{ID: "local_<lib>", Tools: [{Name, Capabilities: ["<lib>.<fn>"], Call}]}`。
6. **绑定**：`bindings.yaml` 加行；`implementation` 与实现一致；`partial` 写 `loss`。
7. **判据**：正向（形状/值/确定性）+ 负向（坏输入、远程 URL、缺失文件、law 违规）。
8. **跑账**：`python3 scripts/library_coverage.py --check`；确认 `complete_exec` 升、
   `symbols_unbound` 降、`complete_real` 不假涨。
9. **全量**：`go test ./...` + `make check`。
10. **语料抽验**：挑 1–2 个只依赖该库（或其闭包）的例子，用真二进制跑：
    ```bash
    /tmp/ncoda-run --local-plugins --tool-path ./libraries --artifact-dir /tmp/art \
      -p input=... <file>.ncoda; echo rc=$?
    ```
    产物要用独立工具复核（WAV/PNG 头、时长、尺寸），不能只看 rc=0。

---

## 12. 路线图（按符号数与解锁面排序）

> **P0 `capcut_data` 已试点**：进程外插件 `plugins/capcut_data/` 落地 8 个符号——
> 7 个 `real`（get_url / objs_to_str_list / str_to_list / array_pair /
> extract_first_end_frame / timelines / wenan_links_merge）+ 1 个 `partial`
> （wenan_split，loss 已声明；函数级 fidelity），unbound 289→281。
> 其余 11 个符号（`*_infos` / `audio_timelines` / `asr_timelines` /
> `keyframes_infos` / `search_sticker` / `timelines_format` /
> `timeline_keyframe`）**故意保持 unbound**：需要 CapCut 草稿 JSON 或网络，
> 本仓无证据，写出来就是假绿。详见 `plugins/capcut_data/README.md`。
>
> **同批已落地**：`plugins/text_toolkit/`（8 库 10 符号）、
> `plugins/compose_toolkit/`（2 库 18 符号）、`plugins/codel_toolkit/`（1 库 2 符号）；
> 内建 timer 家族 + 本地文档读取（`file_read` / `text_file_reader` /
> `document_to_text` / `document_reader`）共 10 个符号；`plugins/timeline_toolkit/`
> （3 库 17 符号，全部 partial）补时间线/字幕/关键帧/草稿片段函数。插件符号合计 56，全部
> 登记在 `plugins/*/ncoda-plugin.yaml`。


| 优先级 | 目标 | 档 | 说明 |
|---|---|---|---|
| P0 | `capcut_data`（19 fn） | real | 纯数据组装（timelines/infos）；语料 384 例，语义需逐函数对照 |
| P0 | `capcut_video_compose` / `capcut_video_fast` / `suchuang_video_compose`（39 fn） | **裁定：unsupported（草稿状态机）** + 1 fn partial | 见 §15 D-1：`draft_url` 是服务端句柄；只实现纯本地的 `get_audio_duration`（微秒） |
| P1 | 图像/视频生成族（jimeng/doubao/na/seedream…） | partial | 复用确定性 PNG；视频先做首帧/占位 MP4，loss 写清 |
| P1 | TTS 族（manbo/zh/fish/dubbingx…） | partial | 复用正弦 WAV；时长公式统一 |
| P1 | `prompt_optimize.sd_better_prompt` | partial | echo 模型；loss = 无提示词优化 |
| P2 | ~~`string_to_array` / `null_fill` / `array_split_merge`~~ | partial（已落地） | `plugins/text_toolkit/`；loss 已声明，`complete_real` 不涨 |
| P2 | `document_to_text` / `file_read`（已落地 partial）· `html2img`（env-missing） | partial | 只读本地 txt/md/xlsx；远程 URL 明确 `REMOTE_URL_UNSUPPORTED`；html2img 需 headless 浏览器 |
| P3 | 搜索/SaaS（feishu/super_table/toutiao/xhs/link_reader…） | unsupported | 需要网络/凭证；不绑，保持 unbound |

**unsupported 的记账约定**：不写进 `bindings.yaml`。文件保持 unbound，
账本显示为 L3 覆盖缺口；不得为了“消灭 unbound”而加 stub。

---

## 15. 裁定记录（Decisions）

### D-1 · `capcut_video_compose.draft_url` = 服务端草稿句柄（2026-10-25）

**证据（全部来自语料消费面，一手）**

| 观察 | 位置 | 推论 |
|---|---|---|
| 上一次结果作为下一次输入继续变更 | `add_images({draft_id: v13.draft_id}); add_images({draft_id: v14.draft_url}); add_captions({draft_id: v15.draft_url}) …` | `draft_url`/`draft_id` 是**可变草稿的句柄**，不是不可变文件地址 |
| 最终把**最早**的 `draft_id` 交出去 | `return v13.draft_id;` + `output(template "视频草稿地址…请使用米核剪映小助手下载")` | 句柄由外部剪映助手消费；本地文件没有意义 |
| 创建结果带 `tip_url`，渲染要 `api_token` | `create_draft → tip_url`；`gen_video(api_token, draft_url)` | 存在服务端状态与渲染服务 |

**裁定**：这 12 个函数（`create_draft` / `add_videos` / `add_audios` / `add_images` /
`add_captions` / `add_effects` / `add_masks` / `add_keyframes` / `add_sticker` /
`easy_create_material` / `save_draft` / `gen_video`）是**服务端草稿状态机**，
开源 runtime 没有可验证的本地等价物 ⇒ 保持 **unbound**（`RBINDING_MISSING`），
不计入 ok，也不写 stub 冒充。

**唯一例外**：`get_audio_duration(mp3_url) -> {duration:int, message, outputList}`
是纯本地解码，已实现为 `partial`（WAV 精确 / MP3 CBR 估算 / 不联网）。
单位是**微秒整数**：语料把它直接当时间线端点与关键帧 `offset` 用，没有 `×1e6` 换算；
对照同一语料里 `audio_duration.get_audio_duration` 返回**秒**且显式 `× 1000000`。
判据：`pkg/localtools/compose_test.go`（4 条）。

**关闭条件**：出现可移植的草稿格式规范 + 至少一个离线可验证的导出器时，
重新评估这 12 个函数的 `partial` 实现。

---

## 13. 未决问题

1. `capcut_data` 19 个函数的精确语义（时间线单位、字段命名）需要逐条从语料消费面反推，
   不能用“看起来像”的实现。
2. ~~`capcut_video_compose` 的 `draft_url` 是服务端状态还是纯文件~~ **已裁定（见 §15 D-1）**：
   服务端句柄，本地无法等价 ⇒ 12 个函数保持 unbound，仅 `get_audio_duration` 实现。
3. `string_to_array` 的默认分隔符、`null_fill` 的填充策略，必须先取证再定 `real`。
4. 商业仓 `complete` 口径是否切换到符号级（跨仓改动，需报批）。
5. `drawing_board` 是否引入字体栅格化（会新增依赖；当前只用标准库）。

---

## 14. 进程外插件：语言的第二个扩展点（escape hatch）

### 14.1 定位

§1–§13 的实现都**编译进 runtime**（Go 闭包，`localtools.Register`）。这条路线的好处是零延迟、
强类型、可测；代价是**每加一个库都要改 runtime 并重新发版**，且只能用 Go 写。

进程外插件把「符号 → 实现」这一层挪到**独立进程**：清单声明、stdio JSON-RPC 通信，
实现语言任意。它与内建实现**共用同一个 `plugin.Manager`**：

```
import "<lib>"; lib.fn(args)
  → 前端：header 签名 + 类型检查（完全不知道实现在哪）
  → IR：tool 节点（provider=lib, operation=fn）
  → plugin.Manager.Call("lib/fn", args)
       ├─ 内建：local_<lib> 的 Go 闭包
       └─ 进程外：ext_<pluginid>_<lib> → Host.Invoke → 子进程
```

**不变的部分**：语言、header、IR、tool 节点、错误策略、账本口径。
**变的部分**：只有「谁执行」——这是纯 runtime 侧扩展，不新增语言构造，也不放宽类型检查。

因此它是**第二个逃逸点**，与内建库并列而不是替代：

| 需求 | 该用哪层 |
|---|---|
| 核心库（database / matting / image_scale / portable / media），要求确定性、零外部依赖 | L0 内建 Go |
| 快速扩库、原型验证、非 Go 生态（Python ML/FFmpeg/Node SDK） | **L1 进程外（本节）** |
| 已有 HTTP 服务，只想接进来 | L2 `type: http`（`plugin.ManifestFromFile`） |
| 平台物理 id → 符号 | L3 商业仓适配器（不在本仓） |

### 14.2 清单（`ncoda-plugin.yaml`）

```yaml
schema: ncoda.plugin/v1          # 必填，精确匹配
id: demo-python                  # 必填，[a-z0-9][a-z0-9_-]*
version: 0.1.0                   # 可选；给了就要和握手一致
protocol: ncoda-plugin/1         # 必填，精确匹配
type: stdio                      # 只支持 stdio（默认）
command: ["python3", "server.py"]# 必填；cwd = 清单所在目录
cwd: .                           # 可选
timeout_ms: 30000                # 可选，默认 30s
max_restarts: 3                  # 可选，默认 3 次/60s 窗口
env:                             # 可选，追加到白名单 env
  EXTRA: value
libraries:
  - library: array_count         # 必须存在于 libraries/<lib>.ncoda
    version: 0.1.0
    fidelity: real               # real | partial | stub
    loss: ""                     # partial/stub 必填非空（也可写在函数项上）
    functions: [array_count]     # 名字，或 {name, fidelity, loss} 做函数级覆盖
```

**静态校验（全部可红，`pluginhost.Load` 阶段，rc=2 用法错）**

| 规则 | 违规示例 |
|---|---|
| `schema` 精确等于 `ncoda.plugin/v1` | 写成 `v2` |
| `id` 字符集/非空 | `id: Demo Plugin` |
| `protocol` 精确等于 `ncoda-plugin/1` | 写成 `ncoda-plugin/2` |
| `type` 只允许 `stdio` | `type: unix` |
| `command` 非空 | 空数组 |
| `library` 在 header 索引里存在 | `library: no_such_lib` |
| `function` 在对应 header 里声明过 | `functions: [no_such_fn]` |
| `partial`/`stub` 必须写 `loss` | 只写 `fidelity: partial` |
| 同一符号不得在本清单内重复声明 | 同一 fn 出现两次 |

header 索引来自 `pluginhost.LoadHeaders(searchPaths)`，**与前端用同一批 `libraries/*.ncoda`**，
所以「插件能声明什么」严格受 L1 约束：插件不能凭空发明语言面。

### 14.3 协议（`ncoda-plugin/1`）

JSON-RPC 2.0，**stdin/stdout 一行一条 JSON**（`\n` 分隔，UTF-8，不允许多行美化）。
stderr 归插件自由使用（宿主只保留末尾若干字节用于报错），**不得**写 stdout。

| 方法 | 方向 | 参数 | 结果 |
|---|---|---|---|
| `initialize` | 宿主 → 插件 | `protocol`, `runtime{name,version}`, `artifact_dir`, `symbols[]` | `protocol`, `id`, `version`, `libraries[{library,functions[]}]` |
| `invoke` | 宿主 → 插件 | `symbol`（`"lib.fn"`）, `args`（对象）, `deadline_ms` | `{ "value": <任意 JSON> }` |
| `cancel` | 宿主 → 插件（**notification**，无 id） | `{"id": <被取消的 invoke id>}` | 无 |
| `ping` | 宿主 → 插件 | — | `{"ok": true}` |
| `shutdown` | 宿主 → 插件 | — | `{}`，随后进程应退出 |

**握手是双向精确匹配**：插件少报或**多报**符号都拒绝注册（`rc=1`）。
理由：少报 → 调用会走到未实现分支；多报 → 清单与实现漂移，账本失真。

**错误形状**

```json
{"jsonrpc":"2.0","id":7,"error":{"code":-32000,"message":"boom","data":{"class":"not-implemented"}}}
```

`data.class` 会被宿主前置进错误串（`not-implemented: boom`），是失败归因（§8）的落点，
必须落在闭集里，不许用 `other`。

**超时与取消**

1. `ctx` 的 deadline 换算成 `deadline_ms` 下发；没 deadline 用清单 `timeout_ms`；
2. 宿主等待 `deadline_ms + 500ms`；
3. 超时先发 `cancel` notification，**宽限 300ms**；
4. 仍不返回 → `kill` 进程，本次调用返回错误；**下次调用自动重启**；
5. 重启计数在 60s 滑动窗口内超过 `max_restarts` → 插件 `disabled`，后续调用直接报错，
   不再重启（防止崩溃循环把 CPU 打满）。

### 14.4 环境与安全边界

- **不是沙箱**。插件以当前用户权限运行，能读能写当前用户能碰的一切。只加载你信任的插件。
- env 白名单：`PATH HOME TMPDIR TEMP TMP LANG LC_ALL SYSTEMROOT USERPROFILE PYTHONPATH`
  + 清单 `env` + `NCODA_PLUGIN_PROTOCOL / NCODA_PLUGIN_ID / NCODA_ARTIFACT_DIR`。
  **其余一律不传**（凭证不会泄漏给插件，插件也不能靠环境偷宿主状态）。
- 网络**默认不提供能力**；需要网络的库应保持 unbound（§12），除非显式立项。
- stdout 是协议专用；插件把日志写到 stderr。
- 产物只写 `NCODA_ARTIFACT_DIR`，文件名带内容哈希（H5）。

### 14.5 实现契约（P1–P7，与 H1–H8 同构）

| 编号 | 约定 |
|---|---|
| P1 | `invoke` 的 `args` 键 = header 参数名（`$array` → `array`）；不得读平台私有扁平键 |
| P2 | 只返回 header 声明过的结果键，包在 `{"value": {...}}` 里 |
| P3 | 可恢复失败 → `result.value` 里给声明形状的失败（`isSuccess=false` / `error` 字段） |
| P4 | 只有必须触发错误策略的失败才返回 JSON-RPC `error`（如 `throw_error`） |
| P5 | 必须响应 `ping`；`shutdown` 后干净退出；不响应就视为超时 |
| P6 | 不得在 `invoke` 里改清单声明；运行时能力集是固定的 |
| P7 | 确定性：同 args + 同 `artifact_dir` ⇒ 同 value + 同产物路径 |

### 14.6 CLI（`ncoda-run`）

```bash
# 单个清单
ncoda-run --plugin ./plugins/demo/ncoda-plugin.yaml --tool-path ./libraries app.ncoda

# 目录扫描（扫 <dir>/ncoda-plugin.yaml 与 <dir>/*/ncoda-plugin.yaml）
ncoda-run --plugin-dir ./plugins --tool-path ./libraries app.ncoda

# 与内建库共存（全开）
ncoda-run --local-plugins --plugin-dir ./plugins --tool-path ./libraries app.ncoda

# 让插件接管某个库：只注册其余内建库，避免同符号注册冲突
ncoda-run --local-plugins=database,matting --plugin-dir ./plugins --tool-path ./libraries app.ncoda

# 查看注册面（不读 stdin、不执行源码）
ncoda-run --plugin-dir ./plugins --tool-path ./libraries --list-symbols
# {"plugin_hosts":1,"symbols":["array_count.array_count","array_count/array_count",...]}
```

退出码沿用契约：`0` 跑通 · `1` 语言/运行期失败（含握手不一致、符号冲突）·
`2` 用法错（清单非法、目录不可读）。

**符号冲突不静默覆盖**：进程外插件与内建库声明同一符号时 `Register` 直接失败：

```
ncoda-run: pluginhost: test-python: register array_count: plugin "ext_test-python_array_count":
tool "array_count" conflicts with existing tool "array_count.array_count" (plugin "local_array_count")
```

想让插件接管某个库，用子集注册关掉对应内建库：
`--local-plugins=database,matting`（裸 flag = 全部）。未识别的库名是用法错（rc=2），
不会静默变成「什么都没注册」。

### 14.7 最小可运行参考实现

`pkg/pluginhost/testdata/server.py` 既是测试夹具，也是**协议正本**：实现
`initialize / invoke / ping / shutdown`、忽略 notification、异常不退出。
新增插件建议直接复制它改 `invoke` 分支。

```bash
go build -o /tmp/ncoda-run ./cmd/ncoda-run
mkdir -p /tmp/plg && cp pkg/pluginhost/testdata/server.py /tmp/plg/
cat > /tmp/plg/ncoda-plugin.yaml <<'YAML'
schema: ncoda.plugin/v1
id: test-python
version: 0.1.0
protocol: ncoda-plugin/1
command: ["python3", "server.py"]
libraries:
  - library: array_count
    fidelity: real
    functions: [array_count]
YAML
/tmp/ncoda-run --plugin /tmp/plg/ncoda-plugin.yaml --tool-path ./libraries --list-symbols
```

### 14.8 负控（缺一不可）

| 负控 | 期望 |
|---|---|
| 清单 `fidelity: partial` 无 `loss` | `Load` 失败，rc=2 |
| 清单声明 header 没有的 function | `Load` 失败，rc=2 |
| 握手少报 / 多报符号 | `Register` 失败，rc=1 |
| 握手 `protocol` 不符 | `Register` 失败，rc=1 |
| 插件与内建库同符号 | `Register` 失败，rc=1（不覆盖） |
| 插件进程 `os._exit` | 本次调用失败；下次调用重启成功 |
| `invoke` 超时不返回 | 发 cancel → 300ms 后 kill；下次调用重启 |
| 60s 内重启超过 `max_restarts` | `disabled`，后续调用立即报错 |

对应 Go 判据：`pkg/pluginhost/host_test.go`；CLI 级复现见上表前五行。

### 14.9 何时不要用插件

- 核心库、对启动延迟敏感、需要严格确定性 → 用 L0 内建。
- 沙箱/多租户隔离 → 本机制**不提供**；需要容器/子进程权限收敛，属平台层（L4）。
- 需要网络或凭证 → 保持 unbound，不要用插件绕过 §12 的 unsupported 约定。
- 想新增**语言构造** → 这不是语言扩展：header 未声明的库/函数，插件也无法声明。
