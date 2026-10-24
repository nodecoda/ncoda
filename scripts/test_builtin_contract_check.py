#!/usr/bin/env python3
"""The judge's judge for builtin_contract_check.py (open side)."""
from __future__ import annotations

import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.path.join(HERE, "builtin_contract_check.py")
YAML = """schema_version: 1
target: ncoda-language
builtins:
  http:
    result: HTTPResponse
    ports:
      - {port: body, lang: body, type: string}
      - {port: status_code, lang: status_code, type: integer}
"""


class BuiltinContract(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp(prefix="builtin-contract-")
        os.makedirs(os.path.join(self.root, "examples", "builtins"))
        with open(os.path.join(self.root, "go.mod"), "w", encoding="utf-8") as fh:
            fh.write("module example.com/x\n\ngo 1.26.5\n")
        self.yaml = os.path.join(self.root, "examples", "builtins", "language.yaml")
        with open(self.yaml, "w", encoding="utf-8") as fh:
            fh.write(YAML)

    def run_script(self, *args: str) -> int:
        return subprocess.run([sys.executable, SCRIPT, "--root", self.root, *args],
                              capture_output=True, text=True).returncode

    def test_write_then_check_passes(self):
        self.assertEqual(self.run_script("--write"), 0)
        self.assertEqual(self.run_script("--check"), 0)

    def test_tampered_artifact_is_red(self):
        self.assertEqual(self.run_script("--write"), 0)
        path = os.path.join(self.root, "pkg", "builtin", "contracts_language_gen.go")
        with open(path, "a", encoding="utf-8") as fh:
            fh.write("// hand edit\n")
        self.assertEqual(self.run_script("--check"), 1)

    def test_empty_source_is_cannot_judge(self):
        os.remove(self.yaml)
        self.assertEqual(self.run_script("--check"), 3)

    def test_missing_required_target_is_red(self):
        with open(self.yaml, "w", encoding="utf-8") as fh:
            fh.write(YAML.replace("ncoda-language", "some-other-target"))
        self.assertEqual(self.run_script("--check"), 1)


if __name__ == "__main__":
    unittest.main()
