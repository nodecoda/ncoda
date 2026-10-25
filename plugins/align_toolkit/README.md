# align_toolkit

按「音频时长列表」对齐字幕的纯函数，进程外实现（`ncoda-plugin/1`）。只依赖 args：
不读文件、不联网、不碰时钟。

覆盖 `audio_align_free.align_text_to_audio` 与
`audio_subtitle_align_alt.align_text_to_audio`。两者都只收 `duration_list` + `texts`，
不含音频 URL/凭证——这是把它们从 `remote-api` 里救出来的关键证据：节点没有可用于
语音对齐的音频内容，只能按时长分摊。

## 证据

语料里两个独立工作流各自带一份**同签名、同输出字段**的手写 JS：

| 文件 | 手写等价物 | 签名/输出 |
|---|---|---|
| `corpus/7560352905955033130_S60_xiaoshuotuiwenCE.ncoda` | `:281` `SUB_CONFIG`、`:287` `splitLongPhrase`、`:321` `processSubtitles` | `:153` 收 `duration_list`，返回 `text_captions` / `text_timielines` |
| `corpus/7567213001913139240_minjiangushi.ncoda` | `:168` `SUB_CONFIG`、`:174` `splitLongPhrase`、`:208` `processSubtitles` | `:62` 收 `duration_list`，返回 `text_captions` / `text_timelines` |

节点调用点：`corpus/7571406993198448703_s123maoxuan.ncoda:116,121`、
`corpus/7572879904859111459_S132dongtailishi.ncoda:183`、
`corpus/7581298340055400502_S133_menghe.ncoda`、
`corpus/7577324552163950635_S132menghe.ncoda`。

JS 的规则（本实现逐条复刻）：

1. `SUB_CONFIG.MAX_LINE_LENGTH = 25`；
2. `splitLongPhrase`：超过 25 时按
   `['。','！','？','，',',','：',':','、','；',';',' ']` 的**优先级**在窗口内找最后一个分隔符；
   否则向前找汉字边界；再否则硬切；
3. `processSubtitles`：切完先按 cleanRegex 删标点/全角符号并 trim，空串丢弃
   （全空记 `[无内容]`）；每条文本按各段字符数比例分时长，末段取余数；
4. 时间线从 0 开始顺序相接。

## 取舍（loss）

- **`text_max` 缺省值**：语料调用均未传 `text_max`，这里按 25（手写等价物的常量）。
  平台节点在 0/缺省时是否也是 25 没有独立证据。
- **切分/清洗规则**来自语料手写等价物，不是节点文档；若上游实现不同，需同时改判据。
- **非 BMP 字符**：JS 按 UTF-16 码元计长，可能从代理对中间切开；本实现按码元计长、
  但只在完整字符边界切分（emoji 不会被切成半个字符）。BMP 文本与 JS 完全一致。
- **对齐密度**：只做「时长比例分摊」，不做语音级 forced alignment；输入里也没有音频。
- `texts` 多于 `duration_list` 时显式报错；多余的 duration 忽略。

以上任一条被上游推翻时，改实现并同步 loss，**不要**把 partial 改成 real 而不改判据。

## 判据

```bash
python3 plugins/align_toolkit/test_server.py          # 函数级正控 + 负控
go test ./pkg/runner -run AudioAlign                 # CLI → 插件进程端到端
go test ./pkg/pluginhost                             # 清单/握手一致性
```
