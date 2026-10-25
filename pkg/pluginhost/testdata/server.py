#!/usr/bin/env python3
"""Test plugin server — 最小 ncoda-plugin/1 实现（也作为协议参考）。"""
import json
import os
import sys
import time

PROTOCOL = "ncoda-plugin/1"
BAD_HANDSHAKE = os.environ.get("TEST_HANDSHAKE") == "bad"


def send(msg):
    sys.stdout.write(json.dumps(msg, ensure_ascii=False) + "\n")
    sys.stdout.flush()


def result(mid, value):
    send({"jsonrpc": "2.0", "id": mid, "result": {"value": value}})


def error(mid, message, cls="test-error"):
    send({"jsonrpc": "2.0", "id": mid,
          "error": {"code": -32000, "message": message, "data": {"class": cls}}})


def main():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        msg = json.loads(line)
        if "id" not in msg:
            continue  # notification (cancel)
        mid = msg["id"]
        method = msg.get("method")
        try:
            if method == "initialize":
                send({"jsonrpc": "2.0", "id": mid, "result": {
                    "protocol": "ncoda-plugin/0" if BAD_HANDSHAKE else PROTOCOL,
                    "id": "test-python", "version": "0.1.0",
                    "libraries": [
                        {"library": "array_count", "functions": ["array_count"]},
                        {"library": "common_code_block",
                         "functions": ["list_sli_trim", "get_one_by_num"]},
                    ],
                }})
            elif method == "invoke":
                params = msg.get("params") or {}
                args = params.get("args") or {}
                if args.get("__crash"):
                    os._exit(3)
                if args.get("__sleep_ms"):
                    time.sleep(float(args["__sleep_ms"]) / 1000.0)
                if args.get("__error"):
                    error(mid, "boom")
                    continue
                symbol = params.get("symbol")
                if symbol == "array_count.array_count":
                    result(mid, {"count": len(args.get("array") or [])})
                elif symbol == "common_code_block.list_sli_trim":
                    data = [x.strip() if isinstance(x, str) else x
                            for x in (args.get("list") or [])]
                    result(mid, {"data": data, "message": ""})
                elif symbol == "common_code_block.get_one_by_num":
                    lst = args.get("list") or []
                    idx = len(lst) - 1 if args.get("end") and lst else int(args.get("num") or 0)
                    item = lst[idx] if 0 <= idx < len(lst) else None
                    result(mid, {"item": item, "list": lst, "message": ""})
                else:
                    error(mid, f"unknown symbol {symbol}", "not-implemented")
            elif method == "ping":
                send({"jsonrpc": "2.0", "id": mid, "result": {"ok": True}})
            elif method == "shutdown":
                send({"jsonrpc": "2.0", "id": mid, "result": {}})
                return
            else:
                send({"jsonrpc": "2.0", "id": mid,
                      "error": {"code": -32601, "message": f"method {method}"}})
        except Exception as exc:  # never die on a bad call
            error(mid, f"{type(exc).__name__}: {exc}")


if __name__ == "__main__":
    main()
