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
        self.touch("platforms/dify/x.go", "package x\n")
        self.touch("pkg/ncoda/render/x.go", "package render\n")
        problems = self.mod.check_structure(self.tmp)
        self.assertTrue(any("internal/" in p for p in problems), problems)
        self.assertTrue(any("cozebiz" in p for p in problems), problems)
        self.assertTrue(any("platforms/dify" in p for p in problems), problems)
        self.assertTrue(any("pkg/ncoda" in p for p in problems), problems)

    def test_mentions(self):
        self.touch("pkg/x.go", 'import _ "github.com/nodecoda/ncoda-commercial/internal/x"\n')
        problems = self.mod.check_mentions(self.tmp)
        self.assertEqual(len(problems), 1, problems)
        self.assertIn("pkg/x.go:1", problems[0])

    def write_ledger(self, idents: list[str]) -> None:
        body = "schema: ncoda.open.platform-identity-residuals/v1\nitems:\n"
        body += "  - id: OI-T\n    file: pkg/x.go\n    what: test\n    why: test\n    unblock: test\n    identifiers:\n"
        for ident in idents:
            body += f"      - {ident}\n"
        self.touch("docs/PLATFORM-IDENTITY-RESIDUALS.yaml", body)

    def test_platform_identity_flags_an_unregistered_identifier(self):
        self.write_ledger([])
        self.touch("pkg/x.go", "package x\n\nfunc difyHelper() {}\n")
        problems = self.mod.check_platform_identity(self.tmp)
        self.assertEqual(len(problems), 1, problems)
        self.assertIn("difyHelper", problems[0])

    def test_platform_identity_ignores_comments_and_literals(self):
        # Prose and message strings are not dependencies; identifiers are.
        self.write_ledger([])
        self.touch("pkg/x.go", 'package x\n\n// coze is mentioned here\nfunc f() string { return "dify" }\n')
        self.assertEqual(self.mod.check_platform_identity(self.tmp), [])

    def test_platform_identity_accepts_a_registered_residual(self):
        self.write_ledger(["cozeAxes"])
        self.touch("pkg/x.go", "package x\n\nvar cozeAxes = 1\n")
        self.assertEqual(self.mod.check_platform_identity(self.tmp), [])

    def test_platform_identity_flags_a_platform_named_path(self):
        self.write_ledger([])
        self.touch("tests/fixtures/dify-skill.ncoda", "function main() {}\n")
        problems = self.mod.check_platform_identity(self.tmp)
        self.assertEqual(len(problems), 1, problems)
        self.assertIn("路径名", problems[0])

    def test_platform_identity_flags_a_stale_ledger_entry(self):
        # The ledger must shrink with the code, else it silently becomes a
        # blanket permission for the next platform-named identifier.
        self.write_ledger(["cozeGone"])
        self.touch("pkg/x.go", "package x\n")
        problems = self.mod.check_platform_identity(self.tmp)
        self.assertEqual(len(problems), 1, problems)
        self.assertIn("台账过期", problems[0])

    def test_purged_word_flags_a_comment_and_a_doc(self):
        self.touch("pkg/x.go", "package x\n\n// the Dify lane used to live here\n")
        self.touch("docs/NOTE.md", "Dify is gone.\n")
        problems = self.mod.check_purged_words(self.tmp)
        self.assertEqual(len(problems), 2, problems)
        self.assertTrue(any("pkg/x.go:3" in p for p in problems), problems)
        self.assertTrue(any("docs/NOTE.md:1" in p for p in problems), problems)

    def test_purged_word_ignores_substrings_and_the_gate_itself(self):
        self.touch("pkg/x.go", "package x\n\nfunc modify() {}\n")
        self.touch("scripts/audit_split.py", "# dify\n")
        self.assertEqual(self.mod.check_purged_words(self.tmp), [])

    def test_purged_word_flags_a_path(self):
        self.touch("tests/fixtures/dify-thing.ncoda", "function main() {}\n")
        problems = self.mod.check_purged_words(self.tmp)
        self.assertTrue(any("路径名" in p for p in problems), problems)

    def test_license(self):
        self.touch("LICENSE", "Apache License 2.0\n")
        self.touch("NOTICE", "ncoda\n")
        self.assertEqual(self.mod.check_license(self.tmp), [])
        os.remove(os.path.join(self.tmp, "NOTICE"))
        self.assertTrue(any("NOTICE" in p for p in self.mod.check_license(self.tmp)))

    def test_deps_flags_a_platform_adapter(self):
        # The rule is "the open binary links no adapter", not a hand-kept list:
        # a dify/coze package reappearing in the tree must fail by construction.
        self.mod.go_deps = lambda root, pkg: {
            "fmt",
            "internal/abi",  # stdlib internal: must NOT be flagged
            "github.com/nodecoda/ncoda/pkg/ir",
            "github.com/nodecoda/ncoda/pkg/backend/dify/render",
        }
        self.mod.DEP_PACKAGES = ("./cmd/nclang-compile",)
        problems = self.mod.check_deps(self.tmp)
        self.assertTrue(any("backend/dify" in p for p in problems), problems)
        self.assertFalse(any("internal/abi" in p for p in problems), problems)

    def test_deps_clean_tree_passes(self):
        self.mod.go_deps = lambda root, pkg: {
            "fmt",
            "internal/abi",
            "github.com/nodecoda/ncoda/pkg/ir",
            "github.com/nodecoda/ncoda/pkg/irdump",
        }
        self.mod.DEP_PACKAGES = ("./cmd/nclang-compile",)
        self.assertEqual(self.mod.check_deps(self.tmp), [])

    def test_clean_tree_has_no_structure_or_mention_problems(self):
        self.touch("LICENSE", "Apache License 2.0\n")
        self.touch("NOTICE", "ncoda\n")
        self.touch("pkg/ok.go", "package ok\n")
        self.assertEqual(self.mod.check_structure(self.tmp), [])
        self.assertEqual(self.mod.check_mentions(self.tmp), [])
        self.assertEqual(self.mod.check_license(self.tmp), [])


if __name__ == "__main__":
    unittest.main()
