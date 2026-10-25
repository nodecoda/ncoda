#!/usr/bin/env python3
"""timeline_toolkit 的函数级判据（正控 + 负控）。

run: python3 plugins/timeline_toolkit/test_server.py
"""
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import server  # noqa: E402


class TimelinesTest(unittest.TestCase):
    def test_single_segment_covers_duration(self):
        got = server.fn_timelines({"duration": 3000000, "number": 1})
        self.assertEqual(got["start"], 0)
        self.assertEqual(got["end"], 3000000)
        self.assertEqual(got["timelines"], [{"start": 0, "end": 3000000}])

    def test_equal_split_is_contiguous_and_exact(self):
        got = server.fn_timelines({"duration": 10, "number": 3})
        segs = got["timelines"]
        self.assertEqual(len(segs), 3)
        self.assertEqual(segs[0]["start"], 0)
        self.assertEqual(segs[-1]["end"], 10)
        for prev, cur in zip(segs, segs[1:]):
            self.assertEqual(prev["end"], cur["start"], "段间不允许空隙/重叠")

    def test_start_offset_is_honored(self):
        got = server.fn_timelines({"duration": 100, "number": 2, "start": 50})
        self.assertEqual(got["start"], 50)
        self.assertEqual(got["end"], 150)
        self.assertEqual(got["timelines"][0]["start"], 50)
        self.assertEqual(got["timelines"][-1]["end"], 150)

    def test_zero_duration_and_bad_number_degrade_to_one(self):
        self.assertEqual(server.fn_timelines({"duration": 0, "number": 5})["timelines"],
                         [{"start": 0, "end": 0}])
        self.assertEqual(len(server.fn_timelines({"duration": 9, "number": -3})["timelines"]), 1)

    def test_type_flag_is_ignored_not_crashing(self):
        got = server.fn_timelines({"duration": 10, "number": 2, "$type": 1})
        self.assertEqual(got["timelines"][-1]["end"], 10)


class StrToListTest(unittest.TestCase):
    def test_single_url_stays_single(self):
        self.assertEqual(server.fn_str_to_list({"value": "https://x/a.mp3"})["result"],
                         ["https://x/a.mp3"])

    def test_newlines_split(self):
        self.assertEqual(server.fn_str_to_list({"value": "a\n b \n\nc\n"})["result"],
                         ["a", "b", "c"])

    def test_json_array_string(self):
        self.assertEqual(server.fn_str_to_list({"value": '["u1",{"url":"u2"}]'})["result"],
                         ["u1", "u2"])

    def test_negative_control_spaces_are_not_separators(self):
        # 语料里也可能是标题文本：按空格切会把一条文案拆成多条。
        self.assertEqual(server.fn_str_to_list({"value": "a b  c"})["result"], ["a b  c"])

    def test_empty(self):
        for v in ({}, {"value": ""}, {"value": 0}):
            self.assertEqual(server.fn_str_to_list(v)["result"], [])


class CaptionSplitterTest(unittest.TestCase):
    def test_chunks_respect_max_chars_and_cover_timeline(self):
        got = server.fn_caption_splitter({
            "max_char_count": 4,
            "texts": ["abcdefgh"],
            "timelines": [{"start": 0, "end": 1000000}],
        })
        self.assertEqual(got["texts"], ["abcd", "efgh"])
        spans = got["timelines"]
        self.assertEqual(len(spans), 2)
        self.assertEqual(spans[0]["start"], 0)
        self.assertEqual(spans[-1]["end"], 1000000)
        self.assertEqual(spans[0]["end"], spans[1]["start"])

    def test_multiple_texts_keep_index_pairing(self):
        got = server.fn_caption_splitter({
            "max_char_count": 0,
            "texts": ["aa", "bb"],
            "timelines": [{"start": 0, "end": 10}, {"start": 20, "end": 40}],
        })
        self.assertEqual(got["texts"], ["aa", "bb"])
        self.assertEqual(got["timelines"],
                         [{"start": 0, "end": 10}, {"start": 20, "end": 40}])

    def test_unpaired_text_gets_zero_span(self):
        got = server.fn_caption_splitter({"max_char_count": 2, "texts": ["abcd"]})
        self.assertEqual(got["timelines"], [{"start": 0, "end": 0}, {"start": 0, "end": 0}])

    def test_string_texts_is_accepted(self):
        got = server.fn_caption_splitter({"max_char_count": 2, "texts": "abcd"})
        self.assertEqual(got["texts"], ["ab", "cd"])

    def test_empty(self):
        self.assertEqual(server.fn_caption_splitter({})["texts"], [])


class TimelineKeyframeTest(unittest.TestCase):
    def test_percent_endpoints_map_to_segment_bounds(self):
        got = server.fn_timeline_keyframe({
            "presetOffsets": "0|100",
            "presetValues": "-1540|1540",
            "timelines": [{"start": 1000, "end": 3000}],
        })
        # header 是 array<string>：发字符串数字，不发浮点。
        self.assertEqual(got["offsets"], ["1000", "3000"])
        self.assertEqual(got["values"], ["-1540", "1540"])

    def test_multi_segment_concatenates_in_order(self):
        got = server.fn_timeline_keyframe({
            "presetOffsets": "0|100",
            "presetValues": "1|2",
            "timelines": [{"start": 0, "end": 10}, {"start": 10, "end": 30}],
        })
        self.assertEqual(got["offsets"], ["0", "10", "10", "30"])
        self.assertEqual(got["values"], ["1", "2", "1", "2"])

    def test_values_are_reused_when_shorter(self):
        got = server.fn_timeline_keyframe({
            "presetOffsets": "0|50|100", "presetValues": "7",
            "timelines": [{"start": 0, "end": 100}],
        })
        self.assertEqual(got["offsets"], ["0", "50", "100"])
        self.assertEqual(got["values"], ["7", "7", "7"])

    def test_empty_timelines(self):
        self.assertEqual(server.fn_timeline_keyframe({}), {"offsets": [], "values": []})


if __name__ == "__main__":
    unittest.main(verbosity=2)
