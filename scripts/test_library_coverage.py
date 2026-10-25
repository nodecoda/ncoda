#!/usr/bin/env python3
"""负控：library_coverage 的符号级判据必须能被打红。"""
import json
import os
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import library_coverage as lc  # noqa: E402


def write(path: str, body: str) -> None:
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(body)


class CoverageGateTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.root = self.tmp.name
        self.headers = os.path.join(self.root, "libraries")
        self.corpus = os.path.join(self.root, "corpus")
        self.bindings = os.path.join(self.root, "bindings.yaml")
        write(os.path.join(self.headers, "a.ncoda"),
              "@language nodecoda/1\nlibrary a @ 0.1.0;\nfunction f1(int x = 0) -> R {}\nfunction f2(int x = 0) -> R {}\nfunction f3(int x = 0) -> R {}\n")

    def tearDown(self) -> None:
        self.tmp.cleanup()

    def bind(self, rows: str) -> None:
        write(self.bindings, "bindings:\n" + rows)

    def test_symbol_level_not_library_level(self) -> None:
        write(os.path.join(self.corpus, "x.ncoda"),
              'import "a";\nfunction main() {\n  a.f1(1);\n  a.f2(2);\n}\n')
        # 只绑 f1：库级口径会说“a 已绑”，符号级必须仍判未完备。
        self.bind("""  - provider: a
    operation: f1
    capability: a.f1
    implementation: real
""")
        rep = lc.analyze(self.corpus, self.headers, self.bindings)
        self.assertEqual(rep["complete_exec"], 0, "unbound a.f2 must not count as complete")
        self.assertEqual(rep["symbols_unbound"], 1)
        self.assertEqual(rep["libraries"]["a"]["missing"], ["f2"])

        # 补上 f2 才完备。
        self.bind("""  - provider: a
    operation: f1
    capability: a.f1
    implementation: real
  - provider: a
    operation: f2
    capability: a.f2
    implementation: real
""")
        rep = lc.analyze(self.corpus, self.headers, self.bindings)
        self.assertEqual(rep["complete_exec"], 1)
        self.assertEqual(rep["complete_real"], 1)

    def test_partial_does_not_count_as_real(self) -> None:
        write(os.path.join(self.corpus, "x.ncoda"), 'import "a";\nfunction main() { a.f1(1); }\n')
        self.bind("""  - provider: a
    operation: f1
    capability: a.f1
    implementation: partial
    loss: "shape only"
""")
        rep = lc.analyze(self.corpus, self.headers, self.bindings)
        self.assertEqual(rep["complete_exec"], 1)
        self.assertEqual(rep["complete_real"], 0, "partial must not inflate complete_real")

    def test_code_block_imports_are_not_library_imports(self) -> None:
        write(os.path.join(self.corpus, "x.ncoda"),
              'function main() {\n  foreign code python3() -> { x: string; } { source `import json\nreturn json.dumps({})`; };\n}\n')
        self.bind("""  - provider: a
    operation: f1
    capability: a.f1
    implementation: real
""")
        rep = lc.analyze(self.corpus, self.headers, self.bindings)
        self.assertEqual(rep["libraries_used"], 0, "python `import json` must not be read as a library import")
        self.assertEqual(rep["complete_exec"], 1)

    def test_ratchet_is_red_on_regression(self) -> None:
        write(os.path.join(self.corpus, "x.ncoda"), 'import "a";\nfunction main() { a.f1(1); }\n')
        self.bind("""  - provider: a
    operation: f1
    capability: a.f1
    implementation: real
""")
        rep = lc.analyze(self.corpus, self.headers, self.bindings)
        base = os.path.join(self.root, "baseline.json")
        write(base, json.dumps({"schema": rep["schema"], "corpus_size": rep["corpus_size"],
                                "complete_exec": 99, "complete_real": 99, "symbols_unbound": 0}))
        old = lc.BASELINE
        lc.BASELINE = base
        try:
            problems = lc.check(rep)
        finally:
            lc.BASELINE = old
        self.assertTrue(problems, "a higher baseline must turn the ratchet red")

    def plugins(self, body: str) -> str:
        root = os.path.join(self.root, "plugins")
        write(os.path.join(root, "demo", "ncoda-plugin.yaml"), body)
        return root

    def test_plugin_symbol_counts_as_bound(self) -> None:
        write(os.path.join(self.corpus, "x.ncoda"), 'import "a";\nfunction main() { a.f1(1); }\n')
        self.bind("""  - provider: a
    operation: f1
    capability: a.f1
    implementation: real
""")
        root = self.plugins(
            "schema: ncoda.plugin/v1\nid: demo\nlibraries:\n"
            "  - library: a\n    fidelity: real\n    functions: [f2]\n")
        rep = lc.analyze(self.corpus, self.headers, self.bindings, root)
        self.assertEqual(rep["symbols_bound_plugin"], 1)
        self.assertEqual(rep["symbols_unbound"], 0, "plugin-covered symbol must not stay unbound")

    def test_stub_counts_as_linked_but_never_as_executable(self) -> None:
        write(os.path.join(self.corpus, "x.ncoda"), 'import "a";\nfunction main() { a.f1(1); }\n')
        self.bind("""  - provider: a
    operation: f2
    capability: a.f2
    implementation: real
""")
        root = self.plugins(
            "schema: ncoda.plugin/v1\nid: demo\nlibraries:\n"
            "  - library: a\n    fidelity: stub\n    loss: \"stub\"\n    functions: [f1]\n")
        rep = lc.analyze(self.corpus, self.headers, self.bindings, root)
        self.assertEqual(rep["symbols_unbound"], 0, "stub must be loadable (no RBINDING_MISSING)")
        self.assertEqual(rep["complete_any"], 1)
        self.assertEqual(rep["complete_stub"], 1)
        self.assertEqual(rep["symbols_bound_stub"], 1)
        self.assertEqual(rep["complete_exec"], 0, "stub must not count as executable")
        self.assertEqual(rep["complete_real"], 0, "stub must never count as real")

    def test_stub_ratchet_is_red_when_stubs_grow(self) -> None:
        write(os.path.join(self.corpus, "x.ncoda"), 'import "a";\nfunction main() { a.f1(1); }\n')
        self.bind("""  - provider: a
    operation: f2
    capability: a.f2
    implementation: real
""")
        root = self.plugins(
            "schema: ncoda.plugin/v1\nid: demo\nlibraries:\n"
            "  - library: a\n    fidelity: stub\n    loss: \"stub\"\n    functions: [f1]\n")
        rep = lc.analyze(self.corpus, self.headers, self.bindings, root)
        base = os.path.join(self.root, "baseline-stub.json")
        write(base, json.dumps({"schema": rep["schema"], "corpus_size": rep["corpus_size"],
                                "complete_exec": 0, "complete_real": 0,
                                "symbols_unbound": 0, "symbols_bound_plugin": 1,
                                "symbols_bound_stub": 0}))
        old = lc.BASELINE
        lc.BASELINE = base
        try:
            problems = lc.check(rep)
        finally:
            lc.BASELINE = old
        self.assertTrue(any("stub" in p for p in problems),
                        "growing the stub surface must turn the ratchet red: %s" % problems)

    def test_plugin_partial_requires_loss(self) -> None:
        root = self.plugins(
            "schema: ncoda.plugin/v1\nid: demo\nlibraries:\n"
            "  - library: a\n    fidelity: partial\n    functions: [f1]\n")
        with self.assertRaises(lc.CannotJudge):
            lc.analyze(self.corpus, self.headers, self.bindings, root)

    def test_plugin_partial_loss_can_be_function_level(self) -> None:
        # 库级 fidelity=partial 但不写库级 loss；每个函数都写了自己的 loss。
        write(os.path.join(self.corpus, "x.ncoda"), 'import "a";\nfunction main() { a.f3(1); }\n')
        self.bind("""  - provider: a
    operation: f3
    capability: a.f3
    implementation: real
""")
        root = self.plugins(
            "schema: ncoda.plugin/v1\nid: demo\nlibraries:\n"
            "  - library: a\n    fidelity: partial\n    functions:\n"
            "      - {name: f1, fidelity: partial, loss: \"f1 loss\"}\n"
            "      - {name: f2, fidelity: partial, loss: \"f2 loss\"}\n")
        rep = lc.analyze(self.corpus, self.headers, self.bindings, root)
        self.assertEqual(rep["symbols_bound_plugin"], 2)

    def test_plugin_inherited_partial_without_loss_is_still_red(self) -> None:
        write(os.path.join(self.corpus, "x.ncoda"), 'import "a";\nfunction main() { a.f3(1); }\n')
        self.bind("""  - provider: a
    operation: f3
    capability: a.f3
    implementation: real
""")
        root = self.plugins(
            "schema: ncoda.plugin/v1\nid: demo\nlibraries:\n"
            "  - library: a\n    fidelity: partial\n    functions:\n"
            "      - {name: f1, fidelity: partial, loss: \"ok\"}\n"
            "      - f2\n")
        with self.assertRaises(lc.CannotJudge):
            lc.analyze(self.corpus, self.headers, self.bindings, root)

    def test_plugin_partial_does_not_count_as_real(self) -> None:
        write(os.path.join(self.corpus, "x.ncoda"), 'import "a";\nfunction main() { a.f1(1); }\n')
        self.bind("""  - provider: a
    operation: f1
    capability: a.f1
    implementation: real
""")
        root = self.plugins(
            "schema: ncoda.plugin/v1\nid: demo\nlibraries:\n"
            "  - library: a\n    fidelity: partial\n    loss: shape only\n    functions: [f2]\n")
        rep = lc.analyze(self.corpus, self.headers, self.bindings, root)
        self.assertEqual(rep["complete_real"], 1)

    def test_plugin_function_level_fidelity(self) -> None:
        write(os.path.join(self.corpus, "x.ncoda"),
              'import "a";\nfunction main() { a.f1(1); a.f2(2); }\n')
        self.bind("""  - provider: a
    operation: f1
    capability: a.f1
    implementation: real
""")
        root = self.plugins(
            "schema: ncoda.plugin/v1\nid: demo\nlibraries:\n"
            "  - library: a\n    fidelity: real\n    functions:\n"
            "      - name: f2\n        fidelity: partial\n        loss: shape only\n")
        rep = lc.analyze(self.corpus, self.headers, self.bindings, root)
        self.assertEqual(rep["symbols_bound_plugin"], 1)
        self.assertEqual(rep["symbols_unbound"], 0)
        self.assertEqual(rep["complete_exec"], 1)
        self.assertEqual(rep["complete_real"], 0, "partial function must not inflate complete_real")

    def test_plugin_function_level_partial_needs_loss(self) -> None:
        root = self.plugins(
            "schema: ncoda.plugin/v1\nid: demo\nlibraries:\n"
            "  - library: a\n    fidelity: real\n    functions:\n"
            "      - name: f1\n        fidelity: partial\n")
        with self.assertRaises(lc.CannotJudge):
            lc.analyze(self.corpus, self.headers, self.bindings, root)

    def test_plugin_shadowing_local_binding_is_red(self) -> None:
        write(os.path.join(self.corpus, "x.ncoda"), 'import "a";\nfunction main() { a.f1(1); }\n')
        self.bind("""  - provider: a
    operation: f1
    capability: a.f1
    implementation: real
""")
        root = self.plugins(
            "schema: ncoda.plugin/v1\nid: demo\nlibraries:\n"
            "  - library: a\n    fidelity: real\n    functions: [f1]\n")
        rep = lc.analyze(self.corpus, self.headers, self.bindings, root)
        self.assertEqual(rep["symbols_declared_twice"], ["a.f1"])
        base = os.path.join(self.root, "baseline.json")
        write(base, json.dumps({"schema": rep["schema"], "corpus_size": rep["corpus_size"],
                                "complete_exec": 1, "complete_real": 1, "symbols_unbound": 0,
                                "symbols_bound_plugin": 1}))
        old = lc.BASELINE
        lc.BASELINE = base
        try:
            problems = lc.check(rep)
        finally:
            lc.BASELINE = old
        self.assertTrue(any("注册冲突" in p for p in problems), problems)

    def test_missing_corpus_is_cannot_judge(self) -> None:
        self.bind("""  - provider: a
    operation: f1
    capability: a.f1
    implementation: real
""")
        with self.assertRaises(lc.CannotJudge):
            lc.analyze(os.path.join(self.root, "missing"), self.headers, self.bindings)

    def test_undeclared_symbol_is_red(self) -> None:
        write(os.path.join(self.corpus, "x.ncoda"), 'import "a";\nfunction main() { a.ghost(1); }\n')
        self.bind("""  - provider: a
    operation: f1
    capability: a.f1
    implementation: real
""")
        rep = lc.analyze(self.corpus, self.headers, self.bindings)
        self.assertEqual(rep["undeclared_symbols"], ["a.ghost"])

    def test_human_output_path_does_not_crash(self) -> None:
        # 回归：人类可读输出曾引用未定义的 `base`，--json/--check 却是绿的。
        write(os.path.join(self.corpus, "x.ncoda"),
              'import "a";\nfunction main() { a.f1(1); }\n')
        self.bind("""  - provider: a
    operation: f1
    capability: a.f1
    implementation: real
""")
        rc = lc.main(["--corpus", self.corpus, "--headers", self.headers,
                      "--bindings", self.bindings, "--plugins", self.root])
        self.assertEqual(rc, 0)


if __name__ == "__main__":
    unittest.main()