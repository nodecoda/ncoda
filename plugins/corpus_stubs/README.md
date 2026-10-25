# corpus_stubs — 未实现符号的可加载占位

## 为什么存在

语料里有些库函数**还没有实现**（需要网络/凭证、服务端草稿状态、未公开 schema、
本机没有的 ffmpeg/浏览器，或语义证据不足）。没有绑定时运行期报
`RBINDING_MISSING`，和「实现有 bug」混在一起，既不能加载也不能逐条核实。

本插件把这类符号注册成 `fidelity: stub`：

- `import "<lib>"` 能解析、`--list-symbols` 能列出（**链接层通**）；
- 调用时返回 JSON-RPC `error`，消息为
  `not-implemented: NOT_IMPLEMENTED <lib>.<fn>: <class>: <证据>`（**不返假值**）；
- 错误里带归因 class，可直接映射到工单。

## 唯一的真值来源

`ncoda-plugin.yaml` 与 `stub_symbols.json` 都是**生成物**，来源是
`docs/UNBOUND-ATTRIBUTION.md` + 当前语料调用面：

```bash
python3 scripts/gen_stub_plugin.py          # 重新生成
python3 scripts/gen_stub_plugin.py --check  # 漂移即 rc=1（语料不可读 rc=3）
python3 plugins/corpus_stubs/test_server.py # 契约判据
```

## 把一个符号升级为真实现

1. 在对应插件（或 `pkg/localtools/`）实现并写判据；
2. 重新运行 `scripts/gen_stub_plugin.py`，该符号会从本清单消失；
3. 两份账本重定基线：`symbols_bound_stub` 降、`complete_real`/`complete_exec` 升。

若两边同时声明同一个符号，`pluginhost` 的「一个符号只能有一个实现」判据会判红。

## 不变量

- `loss` 必须是 `<闭集 class>: <真实证据>`；`other`/空证据在加载期被拒。
- stub **只许降**（`symbols_bound_stub` 棘轮）；`complete_stub` 不计入
  `complete_exec` / `complete_real`。
- stub 永远只能产出 `error`；把它改成返回值，`test_server.py` 立即变红。
