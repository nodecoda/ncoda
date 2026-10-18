#!/usr/bin/env python3
"""The judge's judge for audit_split.py: planted violations must be named.

Without this, a broken check could report "✓" forever (empty set = green trap).
"""
from __future__ import annotations

import importlib.util
import os
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))


def load():
    spec = importlib.util.spec_from_file_location("audit_split", os.path.join(HERE, "audit_split.py"))
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


class AuditSplit(unittest.TestCase):
    def setUp(self):
        self.mod = load()
        self.tmp = tempfile.mkdtemp(prefix="audit-split-")

    def touch(self, rel: str, body: str = "") -> None:
        path = os.path.join(self.tmp, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(body)

    def test_structure(self):
        self.touch("internal/secret.go", "package secret\n")
        self.touch("platforms/cozebiz/x.go", "package x\n")
        problems = self.mod.check_structure(self.tmp)
        self.assertTrue(any("internal/" in p for p in problems), problems)
        self.assertTrue(any("cozebiz" in p for p in problems), problems)

    def test_mentions(self):
        self.touch("pkg/x.go", 'import _ "github.com/nodecoda/ncoda-commercial/internal/x"\n')
        problems = self.mod.check_mentions(self.tmp)
        self.assertEqual(len(problems), 1, problems)
        self.assertIn("pkg/x.go:1", problems[0])

    def test_license(self):
        self.touch("LICENSE", "Apache License 2.0\n")
        self.touch("NOTICE", "ncoda\n")
        self.assertEqual(self.mod.check_license(self.tmp), [])
        os.remove(os.path.join(self.tmp, "NOTICE"))
        self.assertTrue(any("NOTICE" in p for p in self.mod.check_license(self.tmp)))

    def test_clean_tree_has_no_structure_or_mention_problems(self):
        self.touch("LICENSE", "Apache License 2.0\n")
        self.touch("NOTICE", "ncoda\n")
        self.touch("pkg/ok.go", "package ok\n")
        self.assertEqual(self.mod.check_structure(self.tmp), [])
        self.assertEqual(self.mod.check_mentions(self.tmp), [])
        self.assertEqual(self.mod.check_license(self.tmp), [])


if __name__ == "__main__":
    unittest.main()
