# compose_toolkit 插件（进程外，Python）

剪映小助手 / 速创视频合成的**纯函数**子集 + 本地音频时长探测：只做列表、文本、
时间轴变换与本地 WAV/MP3 解析；不触网、不读写草稿、不碰时钟。清单与协议见 `docs/PLUGIN-AUTHORING-SPEC.md`。

```bash
go build -o /tmp/ncoda-run ./cmd/ncoda-run
/tmp/ncoda-run --plugin-dir plugins --tool-path ./libraries --list-symbols
python3 plugins/compose_toolkit/test_server.py
```

## 实现了什么

| 库 | 函数 | fidelity | 语义 | 证据 |
|---|---|---|---|---|
| `capcut_helper` | `audio_link_collector` | real | 摊平 TTS 结果里的 `link`，保序去空 | `corpus/7580659724749865001:94,106` → `audio_timelines({links})` |
| `capcut_helper` | `text_splitter` | partial | 空行分段 → 句末标点切句 | `corpus/7580659724749865001:46` → `segments` 逐段 TTS |
| `capcut_helper` | `timeline_merge` | partial | `main` 后移 `max(pre.end)+gap_us`，输出 8 个时间轴字段 | `corpus/7580659724749865001:113,129` → `.timelines/.main_timelines/.all_*` |
| `capcut_helper` | `wenan_timeline_range` | partial | 文案 → 时间范围（按 wenan 条数均分 timelines） | `corpus/7580659724749865001:285` → LLM 的 `wenanTimeline` |
| `capcut_helper` | `rolling_effect` | partial | `duration_list` 累加成段，`str_list` 循环补齐 | `corpus/7580659724749865001:202` → `subject_timeline` |
| `suchuang_video_compose` | `audio_link_collector` | real | 同上 | `corpus/7576926169774653449:75` |
| `suchuang_video_compose` | `text_empty_removal` | real | 去空串/空值，保序 | `corpus/7571743700611629107:55` → `.wenan` |
| `suchuang_video_compose` | `add_list` | partial | `list` 优先；否则 `add_list + [item]` | `corpus/7599126239110774822:46` → `.list` 作 image_urls |
| `suchuang_video_compose` | `text_splitter` | partial | 同 capcut；`outputList == segments` | `corpus/7576926169774653449:66` → `.segments` |
| `suchuang_video_compose` | `timeline_merge` | partial | 同 capcut | `corpus/7577593277740089370:209` |
| `suchuang_video_compose` | `wenan_timeline_range` | partial | 同 capcut | `corpus/7576926169774653449:86` |
| `suchuang_video_compose` | `wenan_timeline_range_merge` | partial | 同 range，时间戳为 float | `corpus/7576926169774653449:86` |
| `suchuang_video_compose` | `media_tile` | partial | 按 `duration` 微秒切块，`urls` 按块数重复 | `corpus/7576926169774653449:82,143` → `add_audios(urls,timelines)` |
| `suchuang_video_compose` | `timeline_generator` | partial | `duration`（秒，可 ms/us）逐 item 等长排布 | `corpus/7576926169774653449:147` → `add_videos(timelines)` |
| `suchuang_video_compose` | `dialog_split` | partial | `roles="Q\|A"` + 行前缀 → `roleDialogs[{role,dialogs[{content,idx,role}]}]` | `corpus/7567583339583225856:76` |
| `suchuang_video_compose` | `dialog_merge` | partial | 按角色取链接、按 idx 重排 | `corpus/7567583339583225856:167` |
| `suchuang_video_compose` | `dialog_wenan_timeline` | partial | 按 idx 分配 timelines，再按角色分组 | `corpus/7567583339583225856:190` |
| `suchuang_video_compose` | `timeline_align` | partial | `align=end`：每段 end 对齐下一段 start，末段保留；`all_timeline=[0,末段end]` | `corpus/7577283712418152458:412,569,748`；`7578888749272203273:196` |
| `suchuang_video_compose` | `get_audio_duration` | partial | 本地 WAV → `data字节/byteRate`；本地 MP3 → Layer III 帧计数（Xing/Info 优先）；`unit=秒` 返回秒（消费方 ×1e6），`unit=微秒`/缺省返回 µs；http(s) 离线显式失败 | 39 个调用点全部显式传 unit（秒 9 / 微秒 30）；`corpus/7554469499115765795:191,214` 对秒值 ×1e6；`7560292374086385727:68,133` 微秒直接用 |

## 故意保持 unbound（2）

| 函数 | 原因 |
|---|---|
| `audio_timelines` | 需要音频时长/静音探测服务（网络/凭证），不是本地变换 |
| `video_timelines` | 需要视频元数据探测服务 |

`suchuang_video_compose` 在归因表里仍是库级 `server-state`；`audio_timelines` /
`video_timelines` 是剩余媒体探测函数，其余纯函数（含 `timeline_align` 与本地
WAV/MP3 的 `get_audio_duration`）已由本插件绑定。

## 已知 loss

全部写在 `ncoda-plugin.yaml` 的函数级 `loss` 里（切分阈值、`skip_us`、`mergeCount`、
`media_tile` 缺省时长、`timeline_generator` 的 fps/unit、角色前缀解析、
`get_audio_duration` 的 unit 缺省与远程 mock 边界等）。

## 判据

`test_server.py` 覆盖每个函数的正控、空输入、错类型（`invalid-args`）与
`initialize` 符号集合；宿主侧由 `pkg/runner` 的 `TestComposeToolkitPluginEndToEnd`
（真二进制 + `--plugin-dir`）与 `TestComposeToolkitStateMachineStaysRed` 锁住。

> 注意 N-LANG-5：多字段 record 不能直接进数组字面量。宿主判据里的时间轴数组
> 都来自 `capcut_data.timelines(...)` 的返回值，而不是手写数组。
