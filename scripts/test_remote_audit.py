#!/usr/bin/env python3
"""负控：remote_audit 的取证账必须能被打红。

不变量：结论不是注释，是带指针的证据。
  - 实参扫描：map 字面量的键要认出来，模板字符串里的花括号不能算
  - verdict：`keyword` 是检索不是凭据（防「关键词里带 key 就算凭证」）
  - verify：证据指针不命中调用 => 判红；没有调用点 => 判红
  - --check：文档漂移 => rc=1；一致 => rc=0；没有文档 => rc=1
"""
import os
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import remote_audit as ra  # noqa: E402


def row(lib="demo", fn="run", first="a.ncoda:2", keys=("api_key",),
        cls="remote-api", verdict=None, ret_handles=()):
    return {"lib": lib, "class": cls, "desc": "demo",
            "verdict": verdict or ("keep-state" if cls == "server-state" else "keep-remote(凭据)"),
            "symbols": [{"fn": fn, "calls": 1, "keys": list(keys), "positional": False,
                         "first": first, "sig": "", "ret": "R", "class": cls,
                         "ret_handles": list(ret_handles)}]}


class ScanArgs(unittest.TestCase):
    def test_map_keys(self):
        body = 'let v = lib.f({ "b": 1, "a": g(x), "c": "}}" })'
        i = body.index("f(") + 1
        _, keys, _ = ra.scan_args(body, i)
        self.assertEqual(keys, ["b", "a", "c"])

    def test_template_string_braces_ignored(self):
        body = 'let v = lib.f({ "t": template "x {{y}} z" using { "k": v } })'
        i = body.index("f(") + 1
        _, keys, _ = ra.scan_args(body, i)
        self.assertEqual(keys, ["t"])

    def test_positional_is_none(self):
        body = "let v = lib.f(v1)"
        i = body.index("f(") + 1
        _, keys, _ = ra.scan_args(body, i)
        self.assertIsNone(keys)


class Verdict(unittest.TestCase):
    def test_keyword_is_search_not_credential(self):
        self.assertIn("检索", ra.verdict("remote-api", {"keyword"}))

    def test_api_key_is_credential(self):
        self.assertIn("凭据", ra.verdict("remote-api", {"api_key"}))

    def test_server_state_wins(self):
        self.assertEqual(ra.verdict("server-state", {"draft_url"}), "keep-state")


class Verify(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        with open(os.path.join(self.dir, "a.ncoda"), "w", encoding="utf-8") as fh:
            fh.write("x\ny = demo.run({})\nz\n")

    def test_pointer_hits(self):
        self.assertEqual(ra.verify([row()], self.dir), [])

    def test_line_drift_is_red(self):
        problems = ra.verify([row(first="a.ncoda:1")], self.dir)
        self.assertTrue(problems and "不含该调用" in problems[0])

    def test_empty_pointer_is_red(self):
        problems = ra.verify([row(first="")], self.dir)
        self.assertTrue(problems and "没有语料调用点" in problems[0])

    def test_server_state_needs_handle(self):
        problems = ra.verify([row(cls="server-state", keys=("height", "width"))], self.dir)
        self.assertTrue(problems and "无句柄证据" in problems[0])

    def test_server_state_with_returned_handle_passes(self):
        problems = ra.verify(
            [row(cls="server-state", keys=("height", "width"), ret_handles=("draft_url",))],
            self.dir)
        self.assertEqual(problems, [])

    def test_verdict_must_match_class(self):
        problems = ra.verify([row(verdict="keep-state")], self.dir)
        self.assertTrue(problems and "不自洽" in problems[0])


class CheckGate(unittest.TestCase):
    def test_drift_and_match(self):
        d = tempfile.mkdtemp()
        old_doc, old_build, old_verify = ra.DOC, ra.build, ra.verify
        ra.DOC = os.path.join(d, "dossier.md")
        ra.build = lambda corpus=None: [row()]
        ra.verify = lambda rows, corpus=None: []
        try:
            self.assertEqual(ra.main(["--check"]), 1, "缺文档必须判红")
            self.assertEqual(ra.main([]), 0, "首次写入应通过")
            self.assertEqual(ra.main(["--check"]), 0, "一致应通过")
            with open(ra.DOC, "a", encoding="utf-8") as fh:
                fh.write("drift\n")
            self.assertEqual(ra.main(["--check"]), 1, "漂移必须判红")
        finally:
            ra.DOC, ra.build, ra.verify = old_doc, old_build, old_verify


if __name__ == "__main__":
    unittest.main(verbosity=2)
