# html_render — HTML → PNG（本机 headless Chrome）

## 语义证据

| 符号 | 语料调用点 | 消费面 | 依据 |
|---|---|---|---|
| `html2img.gen_img` | 16 例，全部 `{ "html": ... }`（如 `7531359451385036841:46`、`7531673552438263834:14`） | `v.image_url` → 返回/拼装 | header `Html2imgGenImgResult{image_url,message}`；`width/height` 为平台真实参数但语料未传 |
| `htmlcode_to_img.html_img` | 2 例 `{ "htmlContent": ... }`（`7575542061173375030:19,56`、`7581727017687679011:57,64`） | `v.data.imageUrl`（一处调用丢弃结果） | header `HtmlImgResult{data{imageUrl},message,status,$code}` |

实现用 Chrome 的 `--window-size` + `--screenshot`，并读 IHDR 校验尺寸；
判据还解码 PNG 像素，确认 HTML 真的被渲染（不是空白成功）。

## 已知 loss（partial）

- 返回**本机** `file://` 路径，不是线上 OSS URL；
- HTML 里的远程图片/字体需要联网，离线时渲染为空白占位；
- `width/height` 缺省 800×600（Chrome 默认窗口；原服务默认未取证）；
- `viewport` 只解释 `width/height/deviceScaleFactor`，字段集未取证；
- 字体族/抗锯齿随本机 Chrome 版本，像素级结果不保证与线上一致。

## 运行要求

需要 `google-chrome`（或设 `NCODA_CHROME`）。没有 Chrome 时：

- 插件加载正常，调用返回 `RenderError: headless chrome not found`（rc=1，不是静默空值）；
- `make plugin-selftest` 的渲染判据会 skip，其余判据照跑。

## 判据

```bash
python3 plugins/html_render/test_server.py   # 渲染尺寸/像素/默认 viewport/负控
go test ./pkg/runner -run TestHTMLRenderPluginEndToEnd
```
