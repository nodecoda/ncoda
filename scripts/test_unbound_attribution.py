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

    def test_main_rejects_write_baseline_without_reason(self) -> None:
        tpath = os.path.join(self.root, "ok.md")
        write(tpath, table())
        # 缺 --reason 必须在碰语料前就 rc=2；用不可读 corpus 也不该变成 3。
        rc = ua.main(["--table", tpath, "--corpus", os.path.join(self.root, "no-corpus"),
                      "--write-baseline"])
        self.assertEqual(rc, 2)


if __name__ == "__main__":
    unittest.main(verbosity=2)
