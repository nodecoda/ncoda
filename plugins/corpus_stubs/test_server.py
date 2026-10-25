#!/usr/bin/env python3
"""corpus_stubs 的判据（正控 + 负控）。

run: python3 plugins/corpus_stubs/test_server.py

判据意图：
  - 链接面：清单里的每个 library/function 都真实存在于 capability header；
  - 同源面：ncoda-plugin.yaml 与 stub_symbols.json 完全一致（不漂移）；
  - 证据面：每个符号都有闭集 class + 非空证据；
  - 不假绿：stub 永远产出 JSON-RPC error，**绝不**产出 result。
    负控通过「把 invoke 改成返回值」证明最后一条能被打红。
"""
import glob
import json
import os
import re
import subprocess
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))
sys.path.insert(0, HERE)
import server  # noqa: E402

try:
    import yaml
except Exception as exc:  # pragma: no cover
    raise SystemExit("corpus_stubs 测试需要 PyYAML（仓库脚本已依赖）：%s" % exc)

HEADERS = os.path.join(ROOT, "libraries")
RE_LIBRARY = re.compile(r"(?m)^library\s+([A-Za-z_][A-Za-z0-9_]*)\s*@")
RE_FUNCTION = re.compile(r"(?m)^function\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(")


def manifest():
    with open(os.path.join(HERE, "ncoda-plugin.yaml"), encoding="utf-8") as fh:
        return yaml.safe_load(fh)


def headers():
    out = {}
    for path in sorted(glob.glob(os.path.join(HEADERS, "*.ncoda"))):
        with open(path, encoding="utf-8") as fh:
            body = fh.read()
        m = RE_LIBRARY.search(body)
        out[m.group(1)] = {x.group(1) for x in RE_FUNCTION.finditer(body)}
    return out


def assert_stub_fails(test, symbol):
    """stub 调用的唯一合法形状：error 带符号与归因，且没有 result。"""
    env = server.envelope_for(7, symbol)
    test.assertNotIn("result", env, "%s 用返回值伪装成了实现" % symbol)
    err = env.get("error") or {}
    test.assertEqual(err.get("data", {}).get("class"), "not-implemented")
    test.assertIn("NOT_IMPLEMENTED " + symbol, err.get("message", ""))
    test.assertTrue(err.get("data", {}).get("loss"), "%s: loss 不能为空" % symbol)
    test.assertIn(err["data"]["reason_class"], server.CLASSES)


class LinkageTest(unittest.TestCase):
    def test_every_declared_symbol_is_in_a_capability_header(self):
        hdrs = headers()
        for entry in manifest()["libraries"]:
            self.assertIn(entry["library"], hdrs, "no capability header for %s" % entry["library"])
            for item in entry["functions"]:
                fn = item if isinstance(item, str) else item["name"]
                self.assertIn(fn, hdrs[entry["library"]],
                              "%s.%s not declared in libraries/%s.ncoda"
                              % (entry["library"], fn, entry["library"]))

    def test_no_symbol_is_declared_by_two_plugins(self):
        mine = set(server.SYMBOLS)
        dupes = []
        for path in glob.glob(os.path.join(ROOT, "plugins", "*", "ncoda-plugin.yaml")):
            if os.path.abspath(path) == os.path.abspath(os.path.join(HERE, "ncoda-plugin.yaml")):
                continue
            with open(path, encoding="utf-8") as fh:
                doc = yaml.safe_load(fh) or {}
            for lib in doc.get("libraries") or []:
                for item in lib.get("functions") or []:
                    fn = item if isinstance(item, str) else item["name"]
                    sym = "%s.%s" % (lib["library"], fn)
                    if sym in mine:
                        dupes.append((sym, path))
        self.assertEqual(dupes, [], "符号被重复声明：%s" % dupes[:3])


class SameSourceTest(unittest.TestCase):
    def test_manifest_and_stub_table_agree(self):
        want = {}
        for entry in manifest()["libraries"]:
            fns = []
            for item in entry["functions"]:
                fn = item if isinstance(item, str) else item["name"]
                fns.append(fn)
            want[entry["library"]] = sorted(fns)
        got = {}
        for sym in server.SYMBOLS:
            lib, _, fn = sym.partition(".")
            got.setdefault(lib, []).append(fn)
        got = {k: sorted(v) for k, v in got.items()}
        self.assertEqual(got, want)

    def test_every_loss_is_class_prefixed_and_non_empty(self):
        for sym, meta in server.SYMBOLS.items():
            self.assertIn(meta["class"], server.CLASSES, sym)
            self.assertTrue(meta["evidence"].strip(), sym)
            self.assertEqual(meta["loss"], "%s: %s" % (meta["class"], meta["evidence"]))


class NoFakeValueTest(unittest.TestCase):
    def test_all_stubs_fail_explicitly(self):
        for sym in sorted(server.SYMBOLS):
            assert_stub_fails(self, sym)

    def test_unknown_symbol_is_not_implemented_too(self):
        env = server.envelope_for(1, "no_such_library.no_such_function")
        self.assertNotIn("result", env)
        self.assertIn("NOT_IMPLEMENTED no_such_library.no_such_function", env["error"]["message"])

    def test_negative_control_value_returning_stub_is_red(self):
        # 负控：把 invoke 换成一个「返回值」的实现，判据必须立刻变红。
        original = server.invoke

        def fake(symbol, args):
            return {"image_url": "https://example.invalid/x.png"}

        server.invoke = fake
        try:
            with self.assertRaises(AssertionError):
                assert_stub_fails(self, sorted(server.SYMBOLS)[0])
        finally:
            server.invoke = original


class ProtocolTest(unittest.TestCase):
    def test_handshake_matches_manifest(self):
        proc = subprocess.run(
            [sys.executable, os.path.join(HERE, "server.py")],
            input=json.dumps({"jsonrpc": "2.0", "id": 1, "method": "initialize",
                              "params": {}}) + "\n",
            capture_output=True, text=True, timeout=10, check=True)
        res = json.loads(proc.stdout.splitlines()[0])["result"]
        self.assertEqual(res["protocol"], "ncoda-plugin/1")
        self.assertEqual(res["id"], "corpus-stubs")
        got = {lib["library"]: sorted(lib["functions"]) for lib in res["libraries"]}
        want = {}
        for entry in manifest()["libraries"]:
            want[entry["library"]] = sorted(
                item if isinstance(item, str) else item["name"] for item in entry["functions"])
        self.assertEqual(got, want)

    def test_invoke_over_the_wire_returns_error(self):
        symbol = "arxiv_search.search"
        proc = subprocess.run(
            [sys.executable, os.path.join(HERE, "server.py")],
            input="\n".join([
                json.dumps({"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}}),
                json.dumps({"jsonrpc": "2.0", "id": 2, "method": "invoke",
                            "params": {"symbol": symbol, "args": {}}}),
            ]) + "\n",
            capture_output=True, text=True, timeout=10, check=True)
        lines = [json.loads(x) for x in proc.stdout.splitlines() if x.strip()]
        self.assertNotIn("result", lines[1])
        self.assertIn("NOT_IMPLEMENTED " + symbol, lines[1]["error"]["message"])
        self.assertEqual(lines[1]["error"]["data"]["reason_class"], "remote-api")


if __name__ == "__main__":
    unittest.main(verbosity=2)
