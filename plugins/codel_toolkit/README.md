# codel_toolkit 插件（进程外，Python）

`codel_video` 的**纯列表函数**：`split_list` / `merge_list`。清单与协议见
`docs/PLUGIN-AUTHORING-SPEC.md`。

```bash
go build -o /tmp/ncoda-run ./cmd/ncoda-run
/tmp/ncoda-run --plugin-dir plugins --tool-path ./libraries --list-symbols
python3 plugins/codel_toolkit/test_server.py
```

## 实现了什么

| 函数 | fidelity | 语义 | 证据 |
|---|---|---|---|
| `split_list(size, text_list)` | partial | 连续均分到 `text_list1..10`；`size<=0` 全放第一组；最多 10 组 | 语料无调用；形状来自 `libraries/codel_video.ncoda` |
| `merge_list(audio_list1..10)` | partial | 按 1→10 拼接非空列表；`split_list` 每个非空输入一组；`remark=""` | 语料无调用；形状来自 header |

## 故意保持 unbound

`keyframe_ani_scene_v1`：收 `api_token`、返回 `tip_url`，是远程服务
（`corpus/7576180544199933987:838`），本插件不实现。

## 已知 loss

两函数都无语料调用点，分组/合并策略无法由消费面证实；清单里声明为 `partial`
并写清 loss。`complete_exec` 不因 partial 上涨。

## 判据

`test_server.py` 覆盖均分/越界/空输入/错类型，以及 `initialize` 符号集合；
宿主清单一致性由 `pkg/pluginhost/plugins_manifest_test.go` 锁住。
