#!/usr/bin/env python3
"""timeline_toolkit 的函数级判据（正控 + 负控）。

run: python3 plugins/timeline_toolkit/test_server.py
"""
import json
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


class AsrTimelinesTest(unittest.TestCase):
    def test_seconds_become_microseconds(self):
        got = server.fn_asr_timelines({"content_chunks": [
            {"start_time": 0.5, "end_time": 1.25, "text": "你好", "index": "1"},
        ]})
        self.assertEqual(got["timelines"], [{"start": 500000, "end": 1250000}])
        self.assertEqual(got["texts"], ["你好"])

    def test_order_and_texts_preserved(self):
        got = server.fn_asr_timelines({"content_chunks": [
            {"start_time": 0, "end_time": 1, "text": "a"},
            {"start_time": 1, "end_time": 2, "text": "b"},
        ]})
        self.assertEqual(got["texts"], ["a", "b"])
        self.assertEqual([t["start"] for t in got["timelines"]], [0, 1000000])

    def test_all_timelines_is_alias_but_not_shared_object(self):
        got = server.fn_asr_timelines({"content_chunks": [
            {"start_time": 0, "end_time": 1, "text": "a"},
        ]})
        self.assertEqual(got["all_timelines"], got["timelines"])
        self.assertIsNot(got["all_timelines"][0], got["timelines"][0])

    def test_fallback_to_microsecond_start_end(self):
        got = server.fn_asr_timelines({"content_chunks": [
            {"start": 10, "end": 20, "text": "x"},
        ]})
        self.assertEqual(got["timelines"], [{"start": 10, "end": 20}])

    def test_reversed_chunk_is_normalised(self):
        got = server.fn_asr_timelines({"content_chunks": [
            {"start_time": 2, "end_time": 1, "text": "x"},
        ]})
        self.assertEqual(got["timelines"], [{"start": 1000000, "end": 2000000}])

    def test_missing_text_is_empty_not_error(self):
        got = server.fn_asr_timelines({"content_chunks": [{"start_time": 0, "end_time": 1}]})
        self.assertEqual(got["texts"], [""])

    def test_negative_control_non_object_chunk_is_invalid_args(self):
        with self.assertRaises(ValueError):
            server.fn_asr_timelines({"content_chunks": ["x"]})

    def test_empty(self):
        self.assertEqual(server.fn_asr_timelines({}),
                         {"texts": [], "timelines": [], "all_timelines": []})


class KeyframesInfosTest(unittest.TestCase):
    def test_offsets_are_relative_to_each_segment(self):
        got = server.fn_keyframes_infos({
            "ctype": "UNIFORM_SCALE", "offsets": "0|100", "values": "1|1.5",
            "segment_infos": [{"id": "s1", "start": 1000000, "end": 3000000}],
        })
        self.assertTrue(got["isSuccess"])
        entries = json.loads(got["keyframes_infos"])
        self.assertEqual([e["offset"] for e in entries], [0, 2000000])
        self.assertEqual([e["value"] for e in entries], [1, 1.5])
        self.assertEqual(entries[0]["property"], "UNIFORM_SCALE")
        self.assertEqual(entries[0]["segment_id"], "s1")
        self.assertEqual(got["outputList"], [{"keyframes_infos": got["keyframes_infos"]}])

    def test_each_segment_gets_its_own_offsets(self):
        got = server.fn_keyframes_infos({
            "ctype": "KFTypeAlpha", "offsets": "0|80|100", "values": "0.9|0.5|0",
            "segment_infos": [
                {"id": "a", "start": 0, "end": 1000000},
                {"id": "b", "start": 1000000, "end": 1500000},
            ],
        })
        entries = json.loads(got["keyframes_infos"])
        self.assertEqual([(e["segment_id"], e["offset"]) for e in entries], [
            ("a", 0), ("a", 800000), ("a", 1000000),
            ("b", 0), ("b", 400000), ("b", 500000),
        ])

    def test_short_values_reuse_last(self):
        got = server.fn_keyframes_infos({
            "ctype": "KFTypePositionY", "offsets": "0|50|100", "values": "7",
            "segment_infos": [{"id": "s", "start": 0, "end": 100}],
        })
        self.assertEqual([e["value"] for e in json.loads(got["keyframes_infos"])], [7, 7, 7])

    def test_fast_variant_declares_error_fields(self):
        got = server.fn_keyframes_infos_fast({
            "ctype": "UNIFORM_SCALE", "offsets": "0|100", "values": "1|1.2",
            "segment_infos": [{"id": "s", "start": 0, "end": 10}],
        })
        self.assertEqual(got["error"], "")
        self.assertEqual(got["outputList"][0]["error"], "")
        self.assertTrue(got["outputList"][0]["keyframes_infos"].startswith("["))

    def test_empty_segments_is_empty_list_not_error(self):
        got = server.fn_keyframes_infos({"ctype": "X", "offsets": "0|100", "values": "1|2"})
        self.assertEqual(got["keyframes_infos"], "[]")
        self.assertTrue(got["isSuccess"])

    def test_negative_control_bad_offsets_is_invalid_args(self):
        with self.assertRaises(ValueError):
            server.fn_keyframes_infos({"ctype": "X", "offsets": "a|b", "values": "1|2",
                                       "segment_infos": [{"id": "s", "start": 0, "end": 1}]})

    def test_negative_control_non_object_segment_is_invalid_args(self):
        with self.assertRaises(ValueError):
            server.fn_keyframes_infos({"ctype": "X", "offsets": "0", "values": "1",
                                       "segment_infos": ["s"]})


class InfosBuildersTest(unittest.TestCase):
    SEGS = [{"start": 0, "end": 1000000}, {"start": 1000000, "end": 3000000}]

    def test_audio_single_url_spans_all_timelines(self):
        got = server.fn_audio_infos({"mp3_urls": ["u"], "timelines": self.SEGS})
        entries = json.loads(got["infos"])
        self.assertEqual(len(entries), 1)
        self.assertEqual(entries[0], {"audio_url": "u", "duration": 3000000,
                                      "start": 0, "end": 3000000})

    def test_audio_writes_volume_and_effect_only_when_given(self):
        got = server.fn_audio_infos({"mp3_urls": ["u"], "timelines": self.SEGS,
                                     "volume": 0.5, "audio_effect": "人声增强3"})
        ent = json.loads(got["infos"])[0]
        self.assertEqual(ent["volume"], 0.5)
        self.assertEqual(ent["audio_effect"], "人声增强3")
        bare = json.loads(server.fn_audio_infos(
            {"mp3_urls": ["u"], "timelines": self.SEGS})["infos"])[0]
        self.assertNotIn("volume", bare)
        self.assertNotIn("audio_effect", bare)

    def test_caption_pairs_by_index_and_omits_styling(self):
        got = server.fn_caption_infos({"texts": ["a", "b"], "timelines": self.SEGS,
                                       "font_size": 8, "in_animation": "飞入"})
        entries = json.loads(got["infos"])
        self.assertEqual([e["text"] for e in entries], ["a", "b"])
        self.assertEqual([e["start"] for e in entries], [0, 1000000])
        self.assertEqual(entries[0]["in_animation"], "飞入")
        # 样式参数不写入 infos（loss 已声明）
        self.assertNotIn("font_size", entries[0])

    def test_images_carry_size_and_animation(self):
        got = server.fn_imgs_infos({"imgs": ["i1", "i2"], "timelines": self.SEGS,
                                    "width": 1920, "height": 1080,
                                    "in_animation": "向右滑动",
                                    "in_animation_duration": 500000})
        entries = json.loads(got["infos"])
        self.assertEqual(entries[0]["image_url"], "i1")
        self.assertEqual(entries[0]["width"], 1920)
        self.assertEqual(entries[0]["height"], 1080)
        self.assertEqual(entries[0]["in_animation_duration"], 500000)

    def test_videos_carry_duration_and_transition(self):
        got = server.fn_video_infos({"video_urls": ["v1"], "timelines": [self.SEGS[0]],
                                     "transition": "叠化", "transition_duration": 1000000,
                                     "width": 576, "height": 1024})
        ent = json.loads(got["infos"])[0]
        self.assertEqual(ent["video_url"], "v1")
        self.assertEqual(ent["duration"], 1000000)
        self.assertEqual(ent["transition"], "叠化")
        self.assertEqual(ent["transition_duration"], 1000000)

    def test_effects_are_titles_with_spans(self):
        got = server.fn_effect_infos({"effects": ["金粉闪闪"], "timelines": [self.SEGS[1]]})
        self.assertEqual(json.loads(got["infos"]),
                         [{"effect_title": "金粉闪闪", "start": 1000000, "end": 3000000}])

    def test_fast_variants_declare_error_only_where_header_does(self):
        self.assertIn("error", server.fn_audio_infos_fast({"mp3_urls": ["u"], "timelines": self.SEGS}))
        self.assertIn("error", server.fn_caption_infos_fast({"texts": ["a"], "timelines": self.SEGS}))
        self.assertIn("error", server.fn_imgs_infos_fast({"imgs": ["i"], "timelines": self.SEGS}))
        self.assertNotIn("error", server.fn_video_infos_fast({"video_urls": ["v"], "timelines": self.SEGS}))
        self.assertNotIn("error", server.fn_effect_infos_fast({"effects": ["e"], "timelines": self.SEGS}))

    def test_negative_control_bad_timeline_is_invalid_args(self):
        with self.assertRaises(ValueError):
            server.fn_audio_infos({"mp3_urls": ["u"], "timelines": ["x"]})


if __name__ == "__main__":
    unittest.main(verbosity=2)
