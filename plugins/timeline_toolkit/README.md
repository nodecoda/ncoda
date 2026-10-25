# timeline_toolkit

纯时间线/字幕数据函数，进程外实现（`ncoda-plugin/1`）。只依赖 args：不读文件、
不联网、不碰时钟，因此可以放在进程外插件里（见 `docs/LIBRARY-BINDING-SPEC.md` §1.1）。

## 覆盖

| 库 | 函数 | fidelity | 证据 |
|---|---|---|---|
| `capcut_draft_data` | `timelines` | partial | header 返回 `{start,end,timelines[]}`；语料 `capcut_draft_data.timelines({"duration": v8.end, "number": 1})`，消费 `.timelines` / `.end` |
| `capcut_draft_data` | `str_to_list` | partial | header 返回 `{result: array<string>}`；语料把单个 `bgm_url` 传进来，消费 `.result` 作为 urls 列表 |
| `capcut_draft_data` | `caption_splitter` | partial | header 返回 `{texts[], timelines[]}`；语料 `max_char_count=12`，输出直接进 `text_infos(texts, timelines)` |
| `capcut_data` | `timeline_keyframe` | partial | header 返回 `{offsets: array<string>, values: array<string>}`；语料 `presetOffsets="0|100"`，输出 `json.stringify` 后进 `add_keyframe` |

## 取舍（loss）

三个 `capcut_draft_data` 函数的**形状**由 header + 消费面固定，**规则**没有语料证据：

- `timelines`：语料只有 `number=1`。这里对 `number>1` 采用整数等分、余数并入末段，
  保证 `[start, start+duration]` 被完全覆盖且段间无空隙；`$type` 直接忽略。
- `str_to_list`：只解析 JSON 数组字符串或按行拆分。**不**按空格/逗号切分——语料里
  也可能传入标题文本，切分会让单值变多值。
- `caption_splitter`：硬切到 `max_char_count`，按字符数比例分摊该条文本的时间段。
  原实现是否按标点断句、`texts`/`timelines` 数量不等时如何对齐，均未证实。
- `timeline_keyframe`：按每条时间线的百分比插值；返回字符串数组（header 是
  `array<string>`，语料随后还会 `json.stringify` 一次）。

以上任一条被语料/上游实现推翻时，改这里的实现并同步 loss，**不要**把 partial 改成
real 而不改判据。

## 判据

```bash
python3 plugins/timeline_toolkit/test_server.py   # 函数级正控 + 负控
go test ./pkg/runner -run TimelineToolkit        # CLI → 插件进程端到端
go test ./pkg/pluginhost                          # 清单/握手一致性
```
