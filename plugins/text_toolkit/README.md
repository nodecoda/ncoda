# text_toolkit 插件（进程外，Python）

`import "<lib>"; <lib>.fn(...)` 的进程外实现，覆盖 7 个纯文本/数组库。
清单与协议见 `docs/PLUGIN-AUTHORING-SPEC.md`；本目录不含网络、文件、时钟依赖。

```bash
go build -o /tmp/ncoda-run ./cmd/ncoda-run
/tmp/ncoda-run --plugin-dir plugins --tool-path ./libraries --list-symbols
python3 plugins/text_toolkit/test_server.py
```

## 实现了什么

| 库 | 函数 | fidelity | 语义 | 证据（语料调用点 → 消费面） |
|---|---|---|---|---|
| `string_to_array` | `string_2_array` | partial | 按换行切分；单行再按空白/逗号切分；显式 `splitter` 按字面量 | `corpus/7754048787143695370:22-23`（`.data` 直接 `for-in`）；`splitter` 未在语料出现，见 loss |
| `link_extract` | `extract_all_links` | real | 提取全部 `http(s)` URL，去重、去尾部标点 | `corpus/7531294550921674779:127` |
| `link_extract` | `extract_first_link` | real | 首个 URL；没有则空串 | `corpus/7495018022874267683:86` |
| `url_get` | `get_url` | real | 首个 URL；没有 URL 时返回去空白后的原文（语料也传裸 URL） | `corpus/7534652126854709294:28` |
| `text_split_master` | `TextSplittingMaster` | partial | 空行分段 → 句末标点切句 → 200 字上限贪心合并 | `corpus/7570206619293548584:21-22`（`.$output` 逐段 `for-in`） |
| `text_split_tool` | `Paragraph_splitting` | real | 空行分段；无空行时按单换行分段 | `corpus/7554965613270204479:38` |
| `null_fill` | `null_value_filling` | partial | `None` → `""`，其余转字符串 | `corpus/7624531644146729003:101`（只调用、不消费，填充策略未证实） |
| `array_split_merge` | `Str_list` | partial | 按“每组字符串总长度 ≤ num”保序贪心分组，最多 10 组，超出并入末组不丢数据 | `corpus/7535503584906215458:64`（消费 `Str_list1..5`） |
| `array_split_merge` | `Str_merge` | partial | 5 路按参数顺序合并 → `All_Str_list` + 每组一项的 `Split_str` | `corpus/7535503584906215458:103`（`Split_str` 再喂 `audio_timelines`） |

## 已知 loss（清单里逐条声明）

- `string_2_array`：`splitter` 的正则/多分隔符语义未证实；缺省规则由本插件的判据固定。
- `TextSplittingMaster`：原实现的切分阈值、段落合并策略未证实；200 字上限是本仓判据。
- `null_value_filling`：是否原地修改、填充值是否为空串未证实。
- `array_split_merge`：`num` 的取整/余数分配、`contact` 字段语义未证实（`contact` 返回空串）。

## 负控

- 错类型（如 `array: "abc"`）→ JSON-RPC `invalid-args`（宿主侧也可能先被静态类型检查拦成 `TYPE_MISMATCH`）。
- 未知符号 → `not-implemented`；`initialize` 报告的符号集合必须与 `HANDLERS` 完全一致。
