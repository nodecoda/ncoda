#!/usr/bin/env python3
"""corpus_stubs — 尚未实现的语料库函数：可链接、可加载、调用即显式失败。

这一层**故意不返回值**。它存在的意义是把 `RBINDING_MISSING`（这个库函数根本
没接线）和「实现有 bug」分开：前者是加载期缺口，后者是语义问题。语料跑不动时
应当看到 `not-implemented: NOT_IMPLEMENTED <lib>.<fn>: <class>: <证据>`，
而不是静默的空值把错误带到下游。

清单由 scripts/gen_stub_plugin.py 从 docs/UNBOUND-ATTRIBUTION.md 生成；
`stub_symbols.json` 与 `ncoda-plugin.yaml` 同源，改一处必须重新生成。

协议：stdin/stdout 一行一条 JSON-RPC 2.0。日志一律走 stderr。
"""
import json
import os
import sys

PROTOCOL = "ncoda-plugin/1"
PLUGIN_ID = "corpus-stubs"
PLUGIN_VERSION = "0.1.0"

HERE = os.path.dirname(os.path.abspath(__file__))
TABLE_PATH = os.path.join(HERE, "stub_symbols.json")

SCHEMA = "ncoda.corpus-stubs/v1"
# 归因闭集（与 docs/UNBOUND-ATTRIBUTION.md 一致）：不许出现 other。
CLASSES = {"local-todo", "remote-api", "server-state", "schema-unknown", "env-missing"}


class BadTable(RuntimeError):
    pass


class StubNotImplemented(RuntimeError):
    def __init__(self, symbol, cls, evidence):
        super().__init__("%s: %s: %s" % (symbol, cls, evidence))
        self.symbol = symbol
        self.cls = cls
        self.evidence = evidence


def load_table(path=TABLE_PATH):
    with open(path, encoding="utf-8") as fh:
        doc = json.load(fh)
    if doc.get("schema") != SCHEMA:
        raise BadTable("stub_symbols.json: schema = %r, want %r" % (doc.get("schema"), SCHEMA))
    symbols = doc.get("symbols")
    if not isinstance(symbols, dict) or not symbols:
        raise BadTable("stub_symbols.json: symbols must be a non-empty mapping")
    out = {}
    for sym, meta in symbols.items():
        lib, _, fn = sym.partition(".")
        if not lib or not fn:
            raise BadTable("%s: want <library>.<function>" % sym)
        cls = str((meta or {}).get("class") or "")
        ev = str((meta or {}).get("evidence") or "").strip()
        if cls not in CLASSES:
            raise BadTable("%s: class %r not in %s" % (sym, cls, sorted(CLASSES)))
        if not ev:
            raise BadTable("%s: evidence is required" % sym)
        out[sym] = {"class": cls, "evidence": ev,
                    "loss": "%s: %s" % (cls, ev)}
    return out


SYMBOLS = load_table()


def libraries():
    """清单里的 library -> [function]，顺序稳定（握手要与 yaml 完全一致）。"""
    libs = {}
    for sym in SYMBOLS:
        lib, _, fn = sym.partition(".")
        libs.setdefault(lib, []).append(fn)
    return [{"library": lib, "functions": sorted(fns)} for lib, fns in sorted(libs.items())]


LIBRARIES = libraries()


def invoke(symbol, args):
    """stub 的唯一合法行为：抛错。任何返回值都会让判据 test_server 变红。"""
    meta = SYMBOLS.get(symbol)
    if meta is None:
        raise StubNotImplemented(symbol, "not-implemented", "symbol is not declared by corpus_stubs")
    raise StubNotImplemented(symbol, meta["class"], meta["evidence"])


def envelope_for(mid, symbol):
    """把一次调用压成 JSON-RPC 响应：stub 必须产出 error，绝不产出 result。"""
    try:
        value = invoke(symbol, {})
    except StubNotImplemented as exc:
        return {"jsonrpc": "2.0", "id": mid, "error": {
            "code": -32001,
            "message": "NOT_IMPLEMENTED %s: %s: %s" % (exc.symbol, exc.cls, exc.evidence),
            "data": {
                "class": "not-implemented",
                "reason_class": exc.cls,
                "symbol": exc.symbol,
                "loss": "%s: %s" % (exc.cls, exc.evidence),
            },
        }}
    return {"jsonrpc": "2.0", "id": mid, "result": {"value": value}}


# ── JSON-RPC 2.0 传输 ─────────────────────────────────────────────────────

def send(msg):
    sys.stdout.write(json.dumps(msg, ensure_ascii=False) + "\n")
    sys.stdout.flush()


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
        send(envelope_for(mid, params.get("symbol")))
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
            print("corpus_stubs: bad json line", file=sys.stderr)
            continue
        if "id" not in msg:
            continue  # notification (cancel)
        if not handle(msg):
            return


if __name__ == "__main__":
    main()
