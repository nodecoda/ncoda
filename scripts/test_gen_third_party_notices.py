#!/usr/bin/env python3
"""The judge's judge for gen_third_party_notices.py (license classification)."""
from __future__ import annotations

import importlib.util
import os
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))


def load():
    spec = importlib.util.spec_from_file_location(
        "notices", os.path.join(HERE, "gen_third_party_notices.py"))
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


class ClassifyTest(unittest.TestCase):
    def classify(self, text: str) -> str:
        mod = load()
        fd, path = tempfile.mkstemp()
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as fh:
                fh.write(text)
            return mod.classify(path)
        finally:
            os.unlink(path)

    def test_mit(self):
        self.assertEqual(self.classify("MIT License\n\nPermission is hereby granted, free of charge"),
                         "MIT")

    def test_apache(self):
        self.assertEqual(self.classify("Apache License\nVersion 2.0, January 2004"), "Apache-2.0")

    def test_mit_and_apache(self):
        self.assertEqual(
            self.classify("Apache License\nVersion 2.0\nPermission is hereby granted, free of charge"),
            "MIT AND Apache-2.0")

    def test_bsd3(self):
        self.assertEqual(
            self.classify("Redistribution and use in source and binary forms, with or without\n"
                          "Neither the name of the copyright holder"),
            "BSD-3-Clause")

    def test_unknown_is_not_a_pass(self):
        self.assertEqual(self.classify("All rights reserved."), "UNKNOWN")


if __name__ == "__main__":
    unittest.main()
