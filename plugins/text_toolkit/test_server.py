#!/usr/bin/env python3
"""text_toolkit 的函数级判据（正控 + 负控）。

run: python3 plugins/text_toolkit/test_server.py
"""
import json
import os
import subprocess
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import server  # noqa: E402


class StringToArrayTest(unittest.TestCase):
    def test_newline_separated(self):
        got = server.fn_string_2_array({"content": "a\n b \n\nc"})
        self.assertEqual(got["data"], ["a", "b", "c"])
        self.assertEqual(got["code"], 0)

    def test_single_line_splits_whitespace(self):
        got = server.fn_string_2_array({"content": "a b\tc"})
        self.assertEqual(got["data"], ["a", "b", "c"])

    def test_explicit_splitter_is_literal(self):
        got = server.fn_string_2_array({"content": "a|b||c", "splitter": "|"})
        self.assertEqual(got["data"], ["a", "b", "c"])

    def test_missing_content_is_empty_not_error(self):
        self.assertEqual(server.fn_string_2_array({})["data"], [])

    def test_container_is_invalid_args(self):
        with self.assertRaises(ValueError):
            server.fn_string_2_array({"content": {"x": 1}})


class LinkExtractTest(unittest.TestCase):
    def test_all_links_dedup_and_trim(self):
        text = "看 https://a.com/x?q=1，还有 http://b.cn/y。重复 https://a.com/x?q=1"
        self.assertEqual(server.fn_extract_all_links({"text": text})["data"],
                         ["https://a.com/x?q=1", "http://b.cn/y"])

    def test_first_link(self):
        got = server.fn_extract_first_link({"text": "前言 https://a.com/p 后记"})
        self.assertEqual(got["data"], "https://a.com/p")

    def test_no_link_is_empty_string(self):
        self.assertEqual(server.fn_extract_first_link({"text": "没有链接"})["data"], "")

    def test_non_string_is_invalid_args(self):
        with self.assertRaises(ValueError):
            server.fn_extract_all_links({"text": ["https://a.com"]})


class GetUrlTest(unittest.TestCase):
    def test_extracts_first(self):
        self.assertEqual(server.fn_get_url({"content": "见 https://a.com/i.png 谢谢"})["url"],
                         "https://a.com/i.png")

    def test_bare_url_passthrough(self):
        self.assertEqual(server.fn_get_url({"content": "  https://a.com/i.png  "})["url"],
                         "https://a.com/i.png")

    def test_empty(self):
        self.assertEqual(server.fn_get_url({})["url"], "")

    def test_non_string_is_invalid_args(self):
        with self.assertRaises(ValueError):
            server.fn_get_url({"content": {"x": 1}})


class TextSplitTest(unittest.TestCase):
    def test_paragraphs_blank_line(self):
        got = server.fn_Paragraph_splitting({"text": "第一段\n\n第二段\n\n\n第三段"})
        self.assertEqual(got["segments"], ["第一段", "第二段", "第三段"])

    def test_paragraphs_single_newline_fallback(self):
        got = server.fn_Paragraph_splitting({"text": "一\n二\n三"})
        self.assertEqual(got["segments"], ["一", "二", "三"])

    def test_master_keeps_sentences_and_respects_200(self):
        text = "甲。" * 120  # 240 字，必须切成两段
        out = server.fn_TextSplittingMaster({"input": text})["output"]
        self.assertEqual(len(out), 2)
        self.assertTrue(all(len(x) <= 200 for x in out))
        self.assertEqual("".join(out), text)

    def test_master_empty(self):
        self.assertEqual(server.fn_TextSplittingMaster({})["output"], [])

    def test_master_non_string_is_invalid_args(self):
        with self.assertRaises(ValueError):
            server.fn_TextSplittingMaster({"input": {"x": 1}})

    def test_paragraph_non_string_is_invalid_args(self):
        with self.assertRaises(ValueError):
            server.fn_Paragraph_splitting({"text": {"x": 1}})


class NullFillTest(unittest.TestCase):
    def test_fills_none(self):
        got = server.fn_null_value_filling({"array": ["a", None, "b"]})
        self.assertEqual(got["filled_data"], ["a", "", "b"])

    def test_dollar_arg(self):
        self.assertEqual(server.fn_null_value_filling({"$array": [None]})["filled_data"], [""])

    def test_non_array_is_invalid_args(self):
        with self.assertRaises(ValueError):
            server.fn_null_value_filling({"array": "abc"})


class ArraySplitMergeTest(unittest.TestCase):
    def test_split_by_total_chars(self):
        got = server.fn_Str_list({"Str_list": ["aaaa", "bbbb", "cc", "dddd"], "num": 8})
        self.assertEqual(got["Str_list1"], ["aaaa", "bbbb"])
        self.assertEqual(got["Str_list2"], ["cc", "dddd"])
        self.assertEqual(got["Str_list3"], [])

    def test_split_no_limit(self):
        got = server.fn_Str_list({"Str_list": ["a", "b"], "num": 0})
        self.assertEqual(got["Str_list1"], ["a", "b"])

    def test_split_caps_at_ten_groups_without_loss(self):
        rows = ["x" * 5 for _ in range(20)]
        got = server.fn_Str_list({"Str_list": rows, "num": 5})
        flat = [x for i in range(1, 11) for x in got["Str_list%d" % i]]
        self.assertEqual(flat, rows, "must not drop rows beyond group 10")

    def test_merge_rebuilds_groups_and_order(self):
        got = server.fn_Str_merge({"Str_list1": ["a1", "a2"], "Str_list2": ["b1"],
                                   "Str_list3": []})
        self.assertEqual(got["All_Str_list"], ["a1", "a2", "b1"])
        self.assertEqual(got["Split_str"], [{"Str_list": ["a1", "a2"]}, {"Str_list": ["b1"]}])

    def test_merge_empty(self):
        self.assertEqual(server.fn_Str_merge({}),
                         {"All_Str_list": [], "Split_str": [], "contact": ""})


class ProtocolTest(unittest.TestCase):
    def call(self, messages):
        proc = subprocess.run(
            [sys.executable, os.path.join(HERE, "server.py")],
            input="\n".join(json.dumps(m) for m in messages) + "\n",
            capture_output=True, text=True, timeout=10, check=True)
        return [json.loads(l) for l in proc.stdout.splitlines() if l.strip()]

    def test_initialize_reports_exact_symbol_set(self):
        lines = self.call([{"jsonrpc": "2.0", "id": 1, "method": "initialize"}])
        got = {"%s.%s" % (lib["library"], fn)
               for lib in lines[0]["result"]["libraries"] for fn in lib["functions"]}
        self.assertEqual(got, set(server.HANDLERS))

    def test_unknown_symbol_is_not_implemented(self):
        lines = self.call([{"jsonrpc": "2.0", "id": 1, "method": "invoke",
                            "params": {"symbol": "text_split_master.nope",
                                       "args": {}, "deadline_ms": 1000}}])
        self.assertEqual(lines[0]["error"]["data"]["class"], "not-implemented")

    def test_invalid_args_class(self):
        lines = self.call([{"jsonrpc": "2.0", "id": 1, "method": "invoke",
                            "params": {"symbol": "null_fill.null_value_filling",
                                       "args": {"array": "abc"}, "deadline_ms": 1000}}])
        self.assertEqual(lines[0]["error"]["data"]["class"], "invalid-args")

    def test_ping_and_shutdown(self):
        lines = self.call([{"jsonrpc": "2.0", "id": 1, "method": "ping"},
                           {"jsonrpc": "2.0", "id": 2, "method": "shutdown"}])
        self.assertEqual(lines[0]["result"], {"ok": True})
        self.assertEqual(lines[1]["result"], {})


class KimiChatParserTest(unittest.TestCase):
    def test_json_messages(self):
        raw = json.dumps([
            {"role": "user", "content": "你好"},
            {"role": "assistant", "content": "你好呀"},
        ])
        got = server.fn_kimi_chat_parser({"kimi_chathistory": raw})
        self.assertEqual(got["questions"], ["你好"])
        self.assertEqual(got["answers"], ["你好呀"])
        self.assertEqual(got["chunks"], [{"question": "你好", "answer": "你好呀"}])

    def test_text_prefixes_multiline(self):
        raw = "用户：问一\nKimi：答一第一行\n答一第二行\n用户：问二\nKimi：答二"
        got = server.fn_kimi_chat_parser({"kimi_chathistory": raw})
        self.assertEqual(got["questions"], ["问一", "问二"])
        self.assertEqual(got["answers"], ["答一第一行\n答一第二行", "答二"])

    def test_answer_without_question(self):
        got = server.fn_kimi_chat_parser({"kimi_chathistory": "Kimi：只有回答"})
        self.assertEqual(got["chunks"], [{"question": "", "answer": "只有回答"}])

    def test_records_with_question_answer_keys(self):
        got = server.fn_kimi_chat_parser({"kimi_chathistory": [{"question": "q", "answer": "a"}]})
        self.assertEqual(got["questions"], ["q"])
        self.assertEqual(got["answers"], ["a"])

    def test_unknown_text_yields_empty_not_garbage(self):
        got = server.fn_kimi_chat_parser({"kimi_chathistory": "没有说话人前缀的一段话"})
        self.assertEqual(got, {"questions": [], "answers": [], "chunks": []})

    def test_negative_control_non_text_input_is_invalid_args(self):
        with self.assertRaises(ValueError):
            server.fn_kimi_chat_parser({"kimi_chathistory": 5})

    def test_empty(self):
        self.assertEqual(server.fn_kimi_chat_parser({})["chunks"], [])


class StringToListTest(unittest.TestCase):
    """capcut_draft.string_to_list(obj) -> {result:[str]}

    证据：7567224704114540550:75 / 7531636692056047667:108 等把单个 URL 传进来，
    `.result` 直接当 array<string> 喂 audio_infos/video_infos（与
    capcut_draft_data.str_to_list 同形，库级 server-state 不覆盖这个纯函数）。
    """

    def test_single_url_becomes_single_element(self):
        self.assertEqual(server.fn_string_to_list({"obj": "https://x/a.mp4"}),
                         {"result": ["https://x/a.mp4"]})

    def test_json_array_string(self):
        self.assertEqual(server.fn_string_to_list({"obj": "[\"a\",\"b\"]"}),
                         {"result": ["a", "b"]})

    def test_newlines_split(self):
        self.assertEqual(server.fn_string_to_list({"obj": "a\n b \n\n"}),
                         {"result": ["a", "b"]})

    def test_existing_list_is_normalised(self):
        self.assertEqual(server.fn_string_to_list({"obj": ["a", "", "b"]}),
                         {"result": ["a", "b"]})

    def test_empty(self):
        self.assertEqual(server.fn_string_to_list({}), {"result": []})

    def test_negative_control_container_is_invalid_args(self):
        with self.assertRaises(ValueError):
            server.fn_string_to_list({"obj": {"nested": 1}})


if __name__ == "__main__":
    unittest.main(verbosity=2)
