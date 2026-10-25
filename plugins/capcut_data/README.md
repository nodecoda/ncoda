# capcut_data 插件（进程外，Python）

`import "capcut_data"; capcut_data.fn(...)` 的进程外实现。清单与协议见
`docs/PLUGIN-AUTHORING-SPEC.md`。

```bash
go build -o /tmp/ncoda-run ./cmd/ncoda-run
/tmp/ncoda-run --plugin-dir plugins --tool-path ./libraries --list-symbols
/tmp/ncoda-run --plugin-dir plugins --tool-path ./libraries app.ncoda; echo rc=$?
python3 plugins/capcut_data/test_server.py
```

## 实现了什么（fidelity: real）

| 函数 | 语义 | 证据（语料调用点 → 消费面） |
|---|---|---|
| `get_url` | `{output}` → `{outputList:[{output}], output}` | `let v13_inner = ...get_url({output: item.data.link}); yield outputList = v13_inner;` 之后整体作为 batch 记录被展开 |
| `objs_to_str_list` | 展开 `outputs`（含 `outputList` / `$output` / 字符串）→ `infos:[str]` | 输出直接喂 `caption_infos({texts: v61.infos})` 与视频 URL 列表 |
| `str_to_list` | 单值/文件/数组 → `infos:[str]` | `str_to_list({obj: v5.url}).infos` 作为 `image_urls`；`{obj: v1.data}`（`file<image>`）同形 |
| `array_pair` | 相邻两项成组 → `groups` / `total_groups` | `for (lens, prompt in zip(v8.groups, ...))` + 每组再喂 `extract_first_end_frame` |
| `extract_first_end_frame` | 组内首/尾 → `first_item` / `end_item` | `start_image_url: v10.first_item`, `end_image_url: v10.end_item` |
| `timelines` | 微秒整数：`all_timelines=[{start,start+duration}]`，`timelines` 等分 `num` 段（余数并入末段） | `timelines({duration:5000000,num:3}).all_timelines` 整体作为一段；`.timelines` 按图逐段 |
| `wenan_links_merge` | 5 路音频按下标合组 → `links_list:[{links:[...]}]` | `for (links in v14.links_list) { ...audio_timelines({links: links.links}) }` |

> wire 契约：结果键是 header 的**端口名**（`$output` → `output`）。返回 `"$output"`
> 会让语言侧 `v.$output` 读到整条 record（已用 `TestCapcutDataPluginEndToEnd` 锁住）。

## partial（2 个）

| 函数 | 实现 | loss |
|---|---|---|
| `wenan_split` | 按连续等分切 5 段（保序） | 原实现的分配策略（连续/轮转/按长度均衡）未由语料证实，长文分段边界可能不同 |
| `timelines_format` | 取 timeline 的 text/content/wenan/value/output 首个非空串，start/end 归一为整数微秒 | 文本字段名与原始格式化规则未由语料证实（`corpus/7566695004548612130:520,804` 只证明消费面） |

清单用**函数级 fidelity** 声明（`- name: wenan_split / fidelity: partial / loss: …`），
同库其余函数仍是 `real`；账本按函数记账，`partial` 不会冒充 `real`。

## 故意保持 unbound

| 函数 | 原因 |
|---|---|
| `audio_infos` `caption_infos` `effect_infos` `imgs_infos` `video_infos` `keyframes_infos` | 输出是 CapCut 草稿 JSON 片段；本仓无语料可证其 schema，写出来就是假绿 |
| `audio_timelines` | 需要音频时长来源，`links` 参数里没有时长 |
| `asr_timelines` | `content_chunks` 的时间单位（秒/毫秒）未证实 |
| `search_sticker` | 需要剪映素材库网络接口 |
| `timeline_keyframe` | 需要按时间轴生成关键帧曲线，预设曲线语义未由语料证实 |

这些函数在 `libraries/capcut_data.ncoda` 里仍有声明（L1 完整），只是 L2 未绑：
调用会得到 `RBINDING_MISSING`，账本里计为覆盖缺口，不计为失败。

## 边界

- 不发网络请求，不读写语料；纯函数。
- 不写产物，因此不依赖 `NCODA_ARTIFACT_DIR`。
- 整数一律输出 `int`（`timelines` 的 `start`/`end`）。
