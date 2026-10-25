#!/usr/bin/env python3
"""codel_toolkit 的函数级判据（正控 + 负控）。

run: python3 plugins/codel_toolkit/test_server.py
"""
import json
import os
import subprocess
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import server  # noqa: E402


class SplitListTest(unittest.TestCase):
    def test_even_distribution_keeps_order(self):
        got = server.fn_split_list({"size": 3, "text_list": list("abcdefg")})["result"]
        self.assertEqual(got["text_list1"], ["a", "b", "c"])
        self.assertEqual(got["text_list2"], ["d", "e"])
        self.assertEqual(got["text_list3"], ["f", "g"])
        self.assertEqual(got["text_list10"], [])

    def test_size_zero_puts_all_in_first(self):
        got = server.fn_split_list({"text_list": ["a", "b"]})["result"]
        self.assertEqual(got["text_list1"], ["a", "b"])
        self.assertEqual(got["text_list2"], [])

    def test_size_clamped_to_ten(self):
        got = server.fn_split_list({"size": 99, "text_list": ["a"]})["result"]
        self.assertEqual(sum(len(got["text_list%d" % i]) for i in range(1, 11)), 1)

    def test_empty(self):
        got = server.fn_split_list({})["result"]
        self.assertEqual([got["text_list%d" % i] for i in range(1, 11)], [[]] * 10)

    def test_bad_list_is_invalid_args(self):
        with self.assertRaises(ValueError):
            server.fn_split_list({"text_list": {"x": 1}})


class MergeListTest(unittest.TestCase):
    def test_concatenates_in_lane_order(self):
        got = server.fn_merge_list({"audio_list2": ["b"], "audio_list1": ["a"],
                                    "audio_list10": ["z"]})
        self.assertEqual(got["all_list"], ["a", "b", "z"])
        self.assertEqual(got["split_list"], [{"split_list_sub": ["a"]},
                                             {"split_list_sub": ["b"]},
                                             {"split_list_sub": ["z"]}])
        self.assertEqual(got["remark"], "")

    def test_empty_lanes_are_skipped(self):
        got = server.fn_merge_list({"audio_list1": [], "audio_list2": ["x"]})
        self.assertEqual(got["all_list"], ["x"])
        self.assertEqual(len(got["split_list"]), 1)

    def test_empty(self):
        got = server.fn_merge_list({})
        self.assertEqual(got["all_list"], [])
        self.assertEqual(got["split_list"], [])

    def test_bad_lane_is_invalid_args(self):
        with self.assertRaises(ValueError):
            server.fn_merge_list({"audio_list1": "nope"})


class ManifestDeclarationTest(unittest.TestCase):
    def test_partial_entries_match_server_and_have_loss(self):
        import yaml
        with open(os.path.join(HERE, "ncoda-plugin.yaml"), encoding="utf-8") as fh:
            doc = yaml.safe_load(fh)
        lib = doc["libraries"][0]
        entries = {}
        for item in lib["functions"]:
            if isinstance(item, str):
                entries["%s.%s" % (lib["library"], item)] = {
                    "fidelity": lib.get("fidelity", "real"), "loss": lib.get("loss", "")}
            else:
                entries["%s.%s" % (lib["library"], item["name"])] = {
                    "fidelity": item.get("fidelity") or lib.get("fidelity", "real"),
                    "loss": item.get("loss") or lib.get("loss", "")}
        self.assertEqual(set(entries), set(server.HANDLERS))
        for fn, e in entries.items():
            self.assertEqual(e["fidelity"], "partial", fn)
            self.assertTrue(e["loss"].strip(), "partial must document loss: " + fn)


class ProtocolTest(unittest.TestCase):
    def call(self, messages):
        proc = subprocess.run([sys.executable, os.path.join(HERE, "server.py")],
                              input="\n".join(json.dumps(m) for m in messages) + "\n",
                              capture_output=True, text=True, timeout=10, check=True)
        return [json.loads(l) for l in proc.stdout.splitlines() if l.strip()]

    def test_initialize_reports_exact_symbol_set(self):
        lines = self.call([{"jsonrpc": "2.0", "id": 1, "method": "initialize"}])
        got = {"%s.%s" % (lib["library"], fn)
               for lib in lines[0]["result"]["libraries"] for fn in lib["functions"]}
        self.assertEqual(got, set(server.HANDLERS))

    def test_unknown_symbol(self):
        lines = self.call([{"jsonrpc": "2.0", "id": 1, "method": "invoke",
                            "params": {"symbol": "codel_video.keyframe_ani_scene_v1",
                                       "args": {}, "deadline_ms": 1000}}])
        self.assertEqual(lines[0]["error"]["data"]["class"], "not-implemented")

    def test_ping_shutdown(self):
        lines = self.call([{"jsonrpc": "2.0", "id": 1, "method": "ping"},
                           {"jsonrpc": "2.0", "id": 2, "method": "shutdown"}])
        self.assertEqual(lines[0]["result"], {"ok": True})
        self.assertEqual(lines[1]["result"], {})


if __name__ == "__main__":
    unittest.main(verbosity=2)
