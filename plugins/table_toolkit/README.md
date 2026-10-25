# super_table 纯 wire 助手插件（进程外，Python）

`super_table.get_url` / `super_table.str_list` 的进程外实现。表格服务本体
（`get_table` / `save_list` / `super_table`）需要平台状态与凭证，**故意保持
unbound**（归因见 `docs/UNBOUND-ATTRIBUTION.md`）。清单与协议见
`docs/PLUGIN-AUTHORING-SPEC.md`。

```bash
go build -o /tmp/ncoda-run ./cmd/ncoda-run
/tmp/ncoda-run --plugin-dir plugins --tool-path ./libraries --list-symbols
python3 plugins/table_toolkit/test_server.py
```

## 实现了什么

| 函数 | 语义 | 证据（语料调用点 → 消费面） |
|---|---|---|
| `get_url` | `{output}` → `{outputList:[{output}]}`（标量包成单元素 batch 记录；wire 键 = header 端口名 `$output`→`output`） | 8 个文件里入参都是**标量文本/ID/URL**：`item1.prompt`、`item1.text`、`task_id`、`data.downloadURL`（如 `7531679871563989002:48,58,77`） |
| `str_list` | `{outputs}` → `{infos:[str]}`：把 `outputList` 记录展平成字符串，1:1 保序，**空串保留** | `str_list({outputs: <parallel for 的 get_url yield>}).infos` 直接喂 `save_list(datas)`、`for` 迭代喂 `tts(text)`；`7531680424729952282:22` 用 `item === ''` 判定任务未就绪 |

> 归因纠正：原表按 header desc「超级多维表格」把整个库判为 `remote-api`。
> `get_url` 作用在纯文本上（prompt/text），不可能是远程取链 ⇒ 这两个是
> wire 助手；其余三个函数仍是平台服务。

## loss（清单声明）

- `str_list`：展平规则只对语料形态取证（`get_url` 结果 / 标量字符串）；
  其它记录形态未证实，未知结构按空串占位（与语料「未就绪」语义一致）。
- 库级：`get_table` / `save_list` / `super_table` 未实现。

## 边界

- 不发网络请求、不读写语料；纯函数。
- 不写产物，因此不依赖 `NCODA_ARTIFACT_DIR`。
