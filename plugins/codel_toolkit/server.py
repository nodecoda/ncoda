#!/usr/bin/env python3
"""codel_toolkit —— codel_video 纯列表函数的进程外实现（ncoda-plugin/1）。

只实现 split_list / merge_list；keyframe_ani_scene_v1 需要 api_token 与远程服务，
保持 unbound。两函数在语料中无调用点，因此一律 partial + loss（清单声明）。
"""
import json
import sys

PROTOCOL = "ncoda-plugin/1"
PLUGIN_ID = "codel-toolkit"
PLUGIN_VERSION = "0.1.0"

FUNCTIONS = ["merge_list", "split_list"]
LIBRARIES = [{"library": "codel_video", "functions": FUNCTIONS}]


def arg(args, name, default=None):
    if name in args and args[name] is not None:
        return args[name]
    bare = name[1:] if name.startswith("$") else "$" + name
    if bare in args and args[bare] is not None:
        return args[bare]
    return default


def as_items(v, name):
    if v in (None, "", 0):
        return []
    if isinstance(v, list):
        return v
    if isinstance(v, tuple):
        return list(v)
    raise ValueError("%s: want array, got %s" % (name, type(v).__name__))


def as_text(v):
    if v is None:
        return ""
    if isinstance(v, str):
        return v
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, (int, float)):
        return str(v)
    raise ValueError("item: want string, got %s" % type(v).__name__)


def as_int(v, default=0):
    if v in (None, "", 0):
        return default
    if isinstance(v, bool):
        return int(v)
    if isinstance(v, (int, float)):
        return int(v)
    try:
        return int(float(str(v).strip()))
    except (TypeError, ValueError):
        raise ValueError("size: want number, got %r" % (v,))


def fn_split_list(args):
    size = as_int(arg(args, "size", 0), 0)
    items = [as_text(x) for x in as_items(arg(args, "text_list", []), "text_list")]
    out = {"text_list%d" % i: [] for i in range(1, 11)}
    if not items:
        return {"result": out}
    if size <= 0:
        out["text_list1"] = items
        return {"result": out}
    groups = min(size, 10)
    base, rem = divmod(len(items), groups)
    cursor = 0
    for i in range(groups):
        n = base + (1 if i < rem else 0)
        out["text_list%d" % (i + 1)] = items[cursor:cursor + n]
        cursor += n
    return {"result": out}


def fn_merge_list(args):
    merged, groups = [], []
    for i in range(1, 11):
        lane = [as_text(x) for x in as_items(arg(args, "audio_list%d" % i, []),
                                              "audio_list%d" % i)]
        if not lane:
            continue
        merged.extend(lane)
        groups.append({"split_list_sub": lane})
    return {"all_list": merged, "remark": "", "split_list": groups}


HANDLERS = {
    "codel_video.split_list": fn_split_list,
    "codel_video.merge_list": fn_merge_list,
}


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
            "protocol": PROTOCOL, "id": PLUGIN_ID, "version": PLUGIN_VERSION,
            "libraries": LIBRARIES}})
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
            error(mid, "args must be an object", "invalid-args")
            return True
        try:
            result(mid, handler(args))
        except ValueError as exc:
            error(mid, str(exc), "invalid-args")
        except Exception as exc:
            error(mid, "%s: %s" % (type(exc).__name__, exc), "runtime-error")
        return True
    if method == "ping":
        send({"jsonrpc": "2.0", "id": mid, "result": {"ok": True}})
        return True
    if method == "shutdown":
        send({"jsonrpc": "2.0", "id": mid, "result": {}})
        return False
    send({"jsonrpc": "2.0", "id": mid, "error": {"code": -32601, "message": "method %s" % method}})
    return True


def main():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except ValueError:
            print("codel_toolkit: bad json line", file=sys.stderr)
            continue
        if "id" not in msg:
            continue
        if not handle(msg):
            return


if __name__ == "__main__":
    main()
