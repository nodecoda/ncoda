#!/usr/bin/env python3
"""table_toolkit 的函数级判据（正控 + 负控）。

run: python3 plugins/table_toolkit/test_server.py
"""
import json
import os
import subprocess
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import server  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))


class GetUrlTest(unittest.TestCase):
    def test_wraps_scalar_into_output_list(self):
        # 语料：`yield outputList = v3_inner;` 把整个结果当 batch 记录
        self.assertEqual(server.fn_get_url({"output": "https://x/y.mp4"}),
                         {"outputList": [{"output": "https://x/y.mp4"}]})

    def test_dollar_prefixed_key_is_accepted(self):
        self.assertEqual(server.fn_get_url({"$output": "p"}),
                         {"outputList": [{"output": "p"}]})

    def test_numbers_become_strings(self):
        # 语料 get_url({output: item1.data.id})：id 是数字，输出是 array<string>
        self.assertEqual(server.fn_get_url({"output": 123}),
                         {"outputList": [{"output": "123"}]})

    def test_file_like_record_yields_its_url(self):
        self.assertEqual(server.fn_get_url({"output": {"url": "u"}}),
                         {"outputList": [{"output": "u"}]})

    def test_empty_output_keeps_placeholder(self):
        # 语料用 infos 里的空串判断「任务还未完成」，不能把空值吞掉
        self.assertEqual(server.fn_get_url({}), {"outputList": [{"output": ""}]})


class StrListTest(unittest.TestCase):
    def test_flattens_output_list_records_in_order(self):
        outputs = [{"outputList": [{"output": "a"}]},
                   {"outputList": [{"output": "b"}]}]
        self.assertEqual(server.fn_str_list({"outputs": outputs}), {"infos": ["a", "b"]})

    def test_keeps_empty_placeholder(self):
        # 7531680424729952282：`for item of infos: if(item === '') -> 未完成`
        outputs = [{"outputList": [{"output": "a"}]},
                   {"outputList": [{"output": ""}]}]
        self.assertEqual(server.fn_str_list({"outputs": outputs}), {"infos": ["a", ""]})

    def test_plain_strings_pass_through(self):
        self.assertEqual(server.fn_str_list({"outputs": ["a", "b"]}), {"infos": ["a", "b"]})

    def test_single_value_is_wrapped(self):
        self.assertEqual(server.fn_str_list({"outputs": "solo"}), {"infos": ["solo"]})

    def test_empty_is_empty(self):
        self.assertEqual(server.fn_str_list({"outputs": []}), {"infos": []})

    def test_negative_control_unknown_symbol_is_not_implemented(self):
        self.assertIsNone(server.HANDLERS.get("super_table.get_table"))


class ProtocolTest(unittest.TestCase):
    def test_handshake_matches_manifest_functions(self):
        proc = subprocess.run(
            [sys.executable, os.path.join(HERE, "server.py")],
            input=json.dumps({"jsonrpc": "2.0", "id": 1, "method": "initialize",
                              "params": {}}) + "\n",
            capture_output=True, text=True, timeout=10, check=True)
        res = json.loads(proc.stdout.splitlines()[0])["result"]
        self.assertEqual(res["protocol"], "ncoda-plugin/1")
        self.assertEqual(res["id"], "table-toolkit")
        self.assertEqual(res["libraries"][0]["library"], "super_table")
        self.assertEqual(sorted(res["libraries"][0]["functions"]), sorted(server.FUNCTIONS))


if __name__ == "__main__":
    unittest.main(verbosity=2)
