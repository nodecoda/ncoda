#!/usr/bin/env python3
"""super_table 的纯 wire 助手（进程外插件，ncoda-plugin/1）。

只实现能由语料调用点与消费面证实的两个函数：

  get_url($output)   → {outputList:[{$output}]}   把标量包成单元素 batch 记录
  str_list(outputs)  → {infos:[str]}              把这类记录展平成字符串数组

证据：语料里 `get_url` 的入参是**标量文本/ID/URL**（item1.prompt、item1.text、
task_id、downloadURL），紧跟 `str_list({outputs: <parallel for 的 yield>})`
展平后直接当 `array<string>` 用（save_list 的 datas、for 迭代喂 tts、空串检测），
所以它们是纯 wire 包装，不是远程取链。库内其余函数（get_table / save_list /
super_table）仍是服务端状态，属表格服务，保持 unbound。

协议：stdin/stdout 一行一条 JSON-RPC 2.0。日志一律走 stderr。
"""
import json
import sys

PROTOCOL = "ncoda-plugin/1"
PLUGIN_ID = "table-toolkit"
PLUGIN_VERSION = "0.1.0"

REAL_FUNCTIONS = ["get_url"]
PARTIAL_FUNCTIONS = ["str_list"]
FUNCTIONS = REAL_FUNCTIONS + PARTIAL_FUNCTIONS

LIBRARIES = [{"library": "super_table", "functions": FUNCTIONS}]


# ── 参数与值工具 ──────────────────────────────────────────────────────────

def arg(args, name, default=None):
    """header 参数名 → wire 键；`$` 前缀两种写法都接受。"""
    if name in args and args[name] is not None:
        return args[name]
    bare = name[1:] if name.startswith("$") else "$" + name
    if bare in args and args[bare] is not None:
        return args[bare]
    return default


def scalar(v):
    """标量 → 字符串；文件/记录取其中的 url 类字段。"""
    if v is None:
        return ""
    if isinstance(v, str):
        return v
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, (int, float)):
        return str(v)
    if isinstance(v, dict):
        for k in ("url", "output", "$output", "link", "value", "data"):
            inner = v.get(k)
            if isinstance(inner, (str, int, float)) and inner != "":
                return scalar(inner)
        return ""
    return ""


def _flatten(value, out):
    """展平成字符串数组；空串保留（语料用空串表示「该项未就绪」）。"""
    if value is None:
        return
    if isinstance(value, str):
        out.append(value)
        return
    if isinstance(value, bool):
        out.append("true" if value else "false")
        return
    if isinstance(value, (int, float)):
        out.append(str(value))
        return
    if isinstance(value, (list, tuple)):
        for item in value:
            _flatten(item, out)
        return
    if isinstance(value, dict):
        if "outputList" in value:
            _flatten(value["outputList"], out)
            return
        for k in ("output", "$output", "infos", "url", "link", "value"):
            if k in value:
                _flatten(value[k], out)
                return
        out.append("")
        return
    out.append("")


# ── 已证实语义的实现 ──────────────────────────────────────────────────────

def fn_get_url(args):
    """get_url($output) -> {outputList:[{$output}]}

    wire 键 = header 端口名（`$output` → `output`），与 capcut_data.get_url 同约定。
    """
    return {"outputList": [{"output": scalar(arg(args, "$output", ""))}]}


def fn_str_list(args):
    """str_list(outputs) -> {infos:[str]}

    证据：输入是 get_url 结果的列表（`yield outputList = <get_url>`），输出
    1:1 展平成字符串，直接喂 save_list(datas) 与 tts(text)；空串必须保留，
    语料用 `item === ''` 判定任务未完成。
    """
    out = []
    _flatten(arg(args, "outputs", []), out)
    return {"infos": out}


HANDLERS = {
    "super_table.get_url": fn_get_url,
    "super_table.str_list": fn_str_list,
}


# ── JSON-RPC 2.0 传输 ─────────────────────────────────────────────────────

def send(msg):
    sys.stdout.write(json.dumps(msg, ensure_ascii=False) + "\n")
    sys.stdout.flush()


def result(mid, value):
    send({"jsonrpc": "2.0", "id": mid, "result": {"value": value}})


def error(mid, message, cls="invalid-args"):
    send({"jsonrpc": "2.0", "id": mid, "error": {
        "code": -32000, "message": message, "data": {"class": cls}}})


def handle(msg):
    mid = msg["id"]
    method = msg.get("method")
    if method == "initialize":
        send({"jsonrpc": "2.0", "id": mid, "result": {
            "protocol": PROTOCOL,
            "id": PLUGIN_ID,
            "version": PLUGIN_VERSION,
            "libraries": LIBRARIES,
        }})
        return True
    if method == "invoke":
        params = msg.get("params") or {}
        symbol = params.get("symbol")
        args = params.get("args") or {}
        handler = HANDLERS.get(symbol)
        if handler is None:
            error(mid, "unknown symbol %s" % symbol, "not-implemented")
            return True
        if not isinstance(args, dict):
            error(mid, "args must be an object")
            return True
        try:
            result(mid, handler(args))
        except Exception as exc:  # 不让一次坏调用带走进程
            error(mid, "%s: %s" % (type(exc).__name__, exc), "runtime-error")
        return True
    if method == "ping":
        send({"jsonrpc": "2.0", "id": mid, "result": {"ok": True}})
        return True
    if method == "shutdown":
        send({"jsonrpc": "2.0", "id": mid, "result": {}})
        return False
    send({"jsonrpc": "2.0", "id": mid, "error": {
        "code": -32601, "message": "method %s" % method}})
    return True


def main():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except ValueError:
            print("table_toolkit: bad json line", file=sys.stderr)
            continue
        if "id" not in msg:
            continue  # notification (cancel)：实现都很快，直接忽略
        if not handle(msg):
            return


if __name__ == "__main__":
    main()
