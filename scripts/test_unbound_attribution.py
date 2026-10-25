#!/usr/bin/env python3
"""负控：unbound_attribution 的归因判据必须能被打红。

不变量：归因不是注释，是闸门。
  - 缺库行 => unclassified（rc=1，不得静默通过）
  - 非法 class / 缺 evidence / 覆盖写错 => rc=2（表非法）
  - 语料不可读 / 计数漂移 => rc=3（判不了）
  - 符号级覆盖必须真的生效（local-todo → server-state）
"""
import json
import os
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import unbound_attribution as ua  # noqa: E402


def write(path: str, body: str) -> None:
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(body)


def table(*rows: str) -> str:
    return ("# t\n\n```yaml\nschema: ncoda.unbound-attribution/v1\nlibraries:\n"
            + "".join(rows) + "```\n")


def rep(**libs) -> dict:
    """构造一份最小 coverage 报告。libs: name=(missing, files)."""
    libraries = {}
    n = 0
    for name, (missing, files) in libs.items():
        n += len(missing)
        libraries[name] = {"missing": list(missing), "files": files}
    return {"libraries": libraries, "symbols_unbound": n}


class AttributeGateTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.root = self.tmp.name
        self.tpath = os.path.join(self.root, "table.md")

    def tearDown(self) -> None:
        self.tmp.cleanup()

    def load(self, body: str) -> dict:
        write(self.tpath, body)
        return ua.load_table(self.tpath)

    def test_missing_library_row_is_unclassified_and_check_red(self) -> None:
        t = self.load(table())
        r = ua.classify(rep(b=(["f"], 2)), t)
        self.assertEqual(r["unclassified"]["symbols"], 1)
        self.assertEqual(r["unclassified"]["symbol_rows"][0]["symbol"], "f")

        base = os.path.join(self.root, "base.json")
        write(base, json.dumps({"unclassified_symbols": 0, "by_class": {}}))
        old = ua.BASELINE
        ua.BASELINE = base
        try:
            problems = ua.check(r)
        finally:
            ua.BASELINE = old
        self.assertTrue(problems, "unclassified increase must be red")

    def test_invalid_class_is_rc2(self) -> None:
        with self.assertRaises(ua.BadTable):
            self.load(table("  b:\n    class: other\n    evidence: x\n"))

    def test_missing_evidence_is_rc2(self) -> None:
        with self.assertRaises(ua.BadTable):
            self.load(table("  b:\n    class: local-todo\n    evidence: ''\n"))

    def test_template_evidence_is_rc2(self) -> None:
        # 「DB 快照未收录」曾被当成 remote-api 的证据（kimi_chat_parser）。
        with self.assertRaises(ua.BadTable):
            self.load(table(
                "  b:\n    class: remote-api\n"
                "    evidence: \"header desc「(corpus fallback: DB 快照未收录)」表明需要远程服务\"\n"))

    def test_symbol_template_evidence_is_rc2(self) -> None:
        with self.assertRaises(ua.BadTable):
            self.load(table(
                "  b:\n    class: local-todo\n    evidence: lib\n"
                "    symbols:\n      f1:\n        class: remote-api\n"
                "        evidence: 快照未收录\n"))

    def test_symbol_override_wins_over_library_class(self) -> None:
        t = self.load(table(
            "  b:\n"
            "    class: local-todo\n"
            "    evidence: lib\n"
            "    symbols:\n"
            "      f1:\n"
            "        class: server-state\n"
            "        evidence: needs server\n"
        ))
        r = ua.classify(rep(b=(["f1", "f2"], 1)), t)
        self.assertEqual(r["by_class"]["server-state"]["symbols"], 1)
        self.assertEqual(r["by_class"]["local-todo"]["symbols"], 1)
        self.assertEqual(r["rows"][0]["symbol_classes"], {"f1": "server-state", "f2": "local-todo"})
        self.assertEqual(r["libraries_with_unbound"], 1)

    def test_symbol_override_requires_evidence_and_valid_class(self) -> None:
        with self.assertRaises(ua.BadTable):
            self.load(table(
                "  b:\n    class: local-todo\n    evidence: lib\n"
                "    symbols:\n      f1:\n        class: local-todo\n        evidence: ''\n"))
        with self.assertRaises(ua.BadTable):
            self.load(table(
                "  b:\n    class: local-todo\n    evidence: lib\n"
                "    symbols:\n      f1:\n        class: nope\n        evidence: x\n"))

    def test_override_symbol_must_exist_in_header(self) -> None:
        t = self.load(table(
            "  b:\n    class: local-todo\n    evidence: lib\n"
            "    symbols:\n      nope:\n        class: server-state\n        evidence: x\n"))
        with self.assertRaises(ua.BadTable):
            ua.validate_overrides(t, {"b": {"f1"}})
        with self.assertRaises(ua.BadTable):
            ua.validate_overrides(t, {"other": {"f1"}})

    def test_count_drift_is_cannot_judge(self) -> None:
        t = self.load(table("  b:\n    class: local-todo\n    evidence: x\n"))
        bad = rep(b=(["f1"], 1))
        bad["symbols_unbound"] = 99
        with self.assertRaises(ua.CannotJudge):
            ua.classify(bad, t)

    def test_unreadable_inputs_are_rc3(self) -> None:
        # 缺表
        with self.assertRaises(ua.CannotJudge):
            ua.load_table(os.path.join(self.root, "missing.md"))
        # 语料不可读
        tpath = os.path.join(self.root, "ok.md")
        write(tpath, table("  b:\n    class: local-todo\n    evidence: x\n"))
        with self.assertRaises(ua.CannotJudge):
            ua.analyze(os.path.join(self.root, "no-corpus"), tpath)

    def test_class_ratchet_is_red(self) -> None:
        t = self.load(table("  b:\n    class: local-todo\n    evidence: x\n"))
        r = ua.classify(rep(b=(["f"], 1)), t)
        base = os.path.join(self.root, "base.json")
        write(base, json.dumps({"unclassified_symbols": 0,
                                "by_class": {"local-todo": 0}}))
        old = ua.BASELINE
        ua.BASELINE = base
        try:
            problems = ua.check(r)
        finally:
            ua.BASELINE = old
        self.assertTrue(any("local-todo" in p for p in problems))

    def test_write_baseline_refuses_while_unclassified(self) -> None:
        """归因行被删时，--write-baseline 必须拒写（rc=2），基线文件保持原样。

        否则一次误删行会把「判不了」钉成新基线，之后 --check 永远绿。
        """
        tpath = os.path.join(self.root, "empty.md")
        write(tpath, table())
        base = os.path.join(self.root, "base.json")
        write(base, '{"unclassified_symbols": 0, "by_class": {}}')
        drift = {
            "schema": "ncoda.unbound-attribution-report/v1",
            "unbound_symbols": 2,
            "libraries_with_unbound": 1,
            "by_class": {c: {"libraries": 0, "symbols": 0} for c in ua.CLASSES},
            "unclassified": {
                "libraries": ["b"],
                "symbols": 2,
                "symbol_rows": [
                    {"library": "b", "symbol": "f1", "class": "unclassified",
                     "evidence": ""},
                    {"library": "b", "symbol": "f2", "class": "unclassified",
                     "evidence": ""},
                ],
            },
            "rows": [],
            "symbol_rows": [],
        }
        old_base, old_analyze = ua.BASELINE, ua.analyze
        ua.BASELINE = base
        ua.analyze = lambda *a, **k: drift
        try:
            rc = ua.main(["--table", tpath, "--corpus", self.root,
                          "--write-baseline", "--reason", "test"])
            self.assertEqual(rc, 2)
            with open(base, encoding="utf-8") as fh:
                self.assertEqual(json.load(fh)["unclassified_symbols"], 0,
                                 "baseline must not be rewritten")
        finally:
            ua.BASELINE, ua.analyze = old_base, old_analyze

    def test_write_baseline_succeeds_when_classified(self) -> None:
        """正控：unclassified=0 时闸门放行，基线被写入。"""
        tpath = os.path.join(self.root, "empty.md")
        write(tpath, table())
        base = os.path.join(self.root, "base2.json")
        clean = {
            "schema": "ncoda.unbound-attribution-report/v1",
            "unbound_symbols": 1,
            "libraries_with_unbound": 1,
            "by_class": {"local-todo": {"libraries": 1, "symbols": 1}},
            "unclassified": {"libraries": [], "symbols": 0, "symbol_rows": []},
            "rows": [],
            "symbol_rows": [],
        }
        old_base, old_analyze = ua.BASELINE, ua.analyze
        ua.BASELINE = base
        ua.analyze = lambda *a, **k: clean
        try:
            rc = ua.main(["--table", tpath, "--corpus", self.root,
                          "--write-baseline", "--reason", "test"])
            self.assertEqual(rc, 0)
            with open(base, encoding="utf-8") as fh:
                self.assertEqual(json.load(fh)["unbound_symbols"], 1)
        finally:
            ua.BASELINE, ua.analyze = old_base, old_analyze

    def test_main_rejects_write_baseline_without_reason(self) -> None:
        tpath = os.path.join(self.root, "ok.md")
        write(tpath, table())
        # 缺 --reason 必须在碰语料前就 rc=2；用不可读 corpus 也不该变成 3。
        rc = ua.main(["--table", tpath, "--corpus", os.path.join(self.root, "no-corpus"),
                      "--write-baseline"])
        self.assertEqual(rc, 2)


if __name__ == "__main__":
    unittest.main(verbosity=2)
