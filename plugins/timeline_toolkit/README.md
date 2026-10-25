# timeline_toolkit

纯时间线/字幕数据函数，进程外实现（`ncoda-plugin/1`）。只依赖 args、不联网、
不碰时钟；媒体时长只解析**调用方给出的**本地/`data:` 字节（WAV/MP3/MP4/WebM 容器头），
因此可以放在进程外插件里（见 `docs/LIBRARY-BINDING-SPEC.md` §1.1）。

## 覆盖

| 库 | 函数 | fidelity | 证据 |
|---|---|---|---|
| `capcut_draft_data` | `timelines` | partial | header 返回 `{start,end,timelines[]}`；语料 `capcut_draft_data.timelines({"duration": v8.end, "number": 1})`，消费 `.timelines` / `.end` |
| `capcut_draft_data` | `str_to_list` | partial | header 返回 `{result: array<string>}`；语料把单个 `bgm_url` 传进来，消费 `.result` 作为 urls 列表 |
| `capcut_draft_data` | `get_audio_duration` | partial | 语料 `7539744063333105727:96,107` 把结果 yield 成 `array<int>` 并与 `100000`（100ms）相加；`7566078917871140864:62,79-97` 直接用于 `start/end` ⇒ 单位是微秒。本地 WAV（data/byteRate）与 MP3（Layer III 帧计数，Xing/Info 优先）；http(s) 离线显式失败，测试用 mock 取数钩子验证 URL→字节→微秒 |
| `capcut_draft_data` | `audio_timelines` | partial | 语料 `7537506233444663359:96-105`：`.timelines` 与 urls 1:1 配对（audio_infos/image_infos 同形），`.end` 被当整段时长（`timelines({duration: v13.end})`）；本地 WAV/MP3 均可；远程默认显式报错 |
| `capcut_draft_data` | `caption_splitter` | partial | header 返回 `{texts[], timelines[]}`；语料 `max_char_count=12`，输出直接进 `text_infos(texts, timelines)` |
| `capcut_data` | `timeline_keyframe` | partial | header 返回 `{offsets: array<string>, values: array<string>}`；语料 `presetOffsets="0|100"`，输出 `json.stringify` 后进 `add_keyframe` |
| `capcut_data` | `asr_timelines` | partial | 语料 `7599126239110774822` 声明 chunk 形状 `{end_time:float, index:string, start_time:float, text:string}`，结果赋给 `array<{start:int,end:int}>`（µs） |
| `capcut_data` | `keyframes_infos` | partial | 语料手写等价结构（`7531641052663529518`/`7560352905955033130`）：`{offset:段内µs, property, segment_id, value}`，`offset=百分比×(end-start)`；`segment_infos={id,start,end}` 由 `add_captions` 输出 |
| `capcut_data_fast` | `keyframes_infos` | partial | 同构（额外 `error` 字段）；`pattern` 无语料出现，忽略 |
| `capcut` | `video_timelines` | partial | 只收 `links`；`timelines` 与 links 1:1 相接，`all_timelines` 是**单个整体区间**（`7531684846948007951:50` 用 `all_timelines[0].end + …` 求两组视频总时长，`7624531644146729003:242` 用 `all_timelines[0]` 求单条视频时长，`7577283712418152458:331` 手写 JS 也压成一个区间）。时长 = 容器头：MP4 `moov/mvhd`（`duration=0` 退回 `mvex/mehd` 或最长 `trak/mdhd`）、WebM `EBML TimecodeScale×Duration`、WAV `data/byteRate`、MP3 Layer III 帧计数；不调用 ffprobe |
| `suchuang_video_compose` | `video_timelines` | partial | 同 `capcut.video_timelines`（同一实现），额外回显 `links`（header 声明）；时间线单位微秒 |
| `capcut_draft_data` | `audio_infos` `image_infos` | partial | 消费面是 `capcut_draft_folder.add_audios/add_images({infos})`；语料 `7571389179980955699` / `7568679628961955850` 手写同参数结构：audio `{url,duration,start,end(,volume)}`、image `{url,start,end,transform_x/y,scale_x/y,动画}`；单素材铺满整体 |
| `capcut_data` / `capcut_data_fast` | `audio_infos` `caption_infos` `imgs_infos` `video_infos` `effect_infos` | partial | 输出 schema 由语料手写结构固定（`7598545768181841960`、`7551635971227664399`）：audio `{audio_url,duration,start,end,volume?}`、caption `{text,start,end,动画?}`、imgs `{image_url,start,end,width?,height?,动画?}`、video `{video_url,duration,start,end,width?,height?,transition?}`、effect `{effect_title,start,end}` |

## 取舍（loss）

三个 `capcut_draft_data` 函数的**形状**由 header + 消费面固定，**规则**没有语料证据：

- `timelines`：语料只有 `number=1`。这里对 `number>1` 采用整数等分、余数并入末段，
  保证 `[start, start+duration]` 被完全覆盖且段间无空隙；`$type` 直接忽略。
- `str_to_list`：只解析 JSON 数组字符串或按行拆分。**不**按空格/逗号切分——语料里
  也可能传入标题文本，切分会让单值变多值。
- `caption_splitter`：硬切到 `max_char_count`，按字符数比例分摊该条文本的时间段。
  原实现是否按标点断句、`texts`/`timelines` 数量不等时如何对齐，均未证实。
- `keyframes_infos`：`offset` 是**段内**微秒（不是绝对时间）；位置型 `KFTypePositionX/Y`
  是否按 `width/height` 归一化无证据，`value` 原样使用；不写 `easing`。
- `capcut_draft_data.audio_infos` / `image_infos`：键是 `url`（`capcut_draft_folder` 族），不是 `capcut_data` 族的 `audio_url`/`image_url`；样式/变换只在显式传入且非零时写入。
- `timeline_keyframe`：按每条时间线的百分比插值；返回字符串数组（header 是
  `array<string>`，语料随后还会 `json.stringify` 一次）。
- `video_timelines`：`all_timelines` 取整体单区间，不是 per-clip 的别名；容器是
  MP4/WebM/WAV/MP3，**没覆盖** HLS、fragmented-MP4 的 `moof` 时长与「无 Duration 的
  live WebM」；http(s) 默认显式 `REMOTE_VIDEO_UNSUPPORTED`（离线），远程分支由测试
  mock 取数钩子验证，不代表生产会联网。

以上任一条被语料/上游实现推翻时，改这里的实现并同步 loss，**不要**把 partial 改成
real 而不改判据。

## 判据

```bash
python3 plugins/timeline_toolkit/test_server.py   # 函数级正控 + 负控
go test ./pkg/runner -run TimelineToolkit        # CLI → 插件进程端到端
go test ./pkg/pluginhost                          # 清单/握手一致性
```
