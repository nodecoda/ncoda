#!/usr/bin/env python3
"""timeline_toolkit 的函数级判据（正控 + 负控）。

run: python3 plugins/timeline_toolkit/test_server.py
"""
import json
import os
import struct
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


class DraftDataInfosTest(unittest.TestCase):
    """capcut_draft_data.{audio,image}_infos。

    消费面是 `capcut_draft_folder.add_audios/add_images({infos})`；语料里同一
    infos 参数被手写结构喂过（7571389179980955699 的 audioData / bgImageData /
    iconImageData、7568679628961955850 的 audioData，均为 add_* 的直接入参），
    所以这一族用 `url`（不是 capcut_data 族的 audio_url/image_url）。
    """

    SEGS = [{"start": 0, "end": 1000000}, {"start": 1000000, "end": 3000000}]

    def test_audio_entries_use_url_and_duration(self):
        got = server.fn_draft_audio_infos({"urls": ["u1", "u2"], "timelines": self.SEGS})
        self.assertEqual(json.loads(got["infos"]), [
            {"url": "u1", "duration": 1000000, "start": 0, "end": 1000000},
            {"url": "u2", "duration": 2000000, "start": 1000000, "end": 3000000},
        ])

    def test_audio_single_url_covers_whole_timeline(self):
        # 语料 7568679628961955850 的 bgm 手写项：单曲 start=0、end=总时长
        got = server.fn_draft_audio_infos({"urls": ["bgm"], "timelines": self.SEGS})
        self.assertEqual(json.loads(got["infos"]),
                         [{"url": "bgm", "duration": 3000000, "start": 0, "end": 3000000}])

    def test_audio_volume_only_when_given(self):
        # 语料手写项带 volume: 0.4；没传就不要写默认值
        got = server.fn_draft_audio_infos({"urls": ["u"], "timelines": self.SEGS,
                                           "volume": 0.4})
        self.assertEqual(json.loads(got["infos"])[0]["volume"], 0.4)
        bare = json.loads(server.fn_draft_audio_infos(
            {"urls": ["u"], "timelines": self.SEGS})["infos"])[0]
        self.assertNotIn("volume", bare)

    def test_images_carry_transform_scale_and_animation(self):
        got = server.fn_draft_image_infos({
            "urls": ["i1", "i2"], "timelines": self.SEGS,
            "transform_x": -771, "transform_y": -515, "scale_x": 0.33, "scale_y": 0.33,
            "in_animation": "向上滑动", "in_animation_duration": 500000,
            "out_animation": "向下滑动", "out_animation_duration": 500000})
        entries = json.loads(got["infos"])
        self.assertEqual(entries[0], {
            "url": "i1", "start": 0, "end": 1000000,
            "transform_x": -771, "transform_y": -515, "scale_x": 0.33, "scale_y": 0.33,
            "in_animation": "向上滑动", "in_animation_duration": 500000,
            "out_animation": "向下滑动", "out_animation_duration": 500000})
        self.assertEqual(entries[1]["start"], 1000000)

    def test_image_single_url_covers_whole_timeline(self):
        # 语料 bgImageData.push({url: bg_image, start: 0, end: 总时长})
        got = server.fn_draft_image_infos({"urls": ["bg"], "timelines": self.SEGS,
                                           "in_animation": "轻微放大",
                                           "in_animation_duration": 100000})
        self.assertEqual(json.loads(got["infos"]),
                         [{"url": "bg", "start": 0, "end": 3000000,
                           "in_animation": "轻微放大", "in_animation_duration": 100000}])

    def test_negative_control_bad_timeline_is_invalid_args(self):
        with self.assertRaises(ValueError):
            server.fn_draft_audio_infos({"urls": ["u"], "timelines": ["x"]})
        with self.assertRaises(ValueError):
            server.fn_draft_image_infos({"urls": ["u"], "timelines": ["x"]})


class DraftAudioTimelinesTest(unittest.TestCase):
    """capcut_draft_data.audio_timelines：本地 WAV → {start,end,timelines,total_timelines}。

    证据：7537506233444663359:96-105 —— `timelines` 与 urls 1:1 配对，
    `end` 被当作整段时长（timelines({duration: v13.end})）。
    """

    def setUp(self):
        import tempfile
        self.tmp = tempfile.TemporaryDirectory()

    def tearDown(self):
        self.tmp.cleanup()

    def wav(self, seconds, rate=16000):
        import io
        import wave
        buf = io.BytesIO()
        with wave.open(buf, "wb") as w:
            w.setnchannels(1)
            w.setsampwidth(2)
            w.setframerate(rate)
            w.writeframes(b"\x00\x00" * int(seconds * rate))
        return buf.getvalue()

    def path(self, blob, name):
        p = os.path.join(self.tmp.name, name)
        with open(p, "wb") as fh:
            fh.write(blob)
        return p

    def test_sequential_spans_and_total(self):
        a = self.path(self.wav(1.0), "a.wav")
        b = self.path(self.wav(0.5), "b.wav")
        out = server.fn_audio_timelines({"urls": [a, b]})
        self.assertEqual(out["timelines"], [
            {"start": 0, "end": 1000000},
            {"start": 1000000, "end": 1500000},
        ])
        self.assertEqual((out["start"], out["end"]), (0, 1500000))
        self.assertEqual(out["total_timelines"], [{"start": 0, "end": 1500000}])

    def test_file_url_and_record_are_unwrapped(self):
        p = self.path(self.wav(0.25), "c.wav")
        out = server.fn_audio_timelines({"urls": [{"link": "file://" + p}]})
        self.assertEqual(out["end"], 250000)

    def test_empty_urls(self):
        self.assertEqual(server.fn_audio_timelines({"urls": []}),
                         {"start": 0, "end": 0, "timelines": [], "total_timelines": []})

    def test_remote_and_truncated_mp3_are_explicit_errors(self):
        with self.assertRaises(ValueError) as ctx:
            server.fn_audio_timelines({"urls": ["https://example.com/a.mp3"]})
        self.assertIn("REMOTE_AUDIO_UNSUPPORTED", str(ctx.exception))
        bad = self.path(b"ID3\x04\x00", "d.mp3")
        with self.assertRaises(ValueError):
            server.fn_audio_timelines({"urls": [bad]})

    def test_negative_control_end_must_match_last_segment(self):
        p = self.path(self.wav(0.75), "e.wav")
        out = server.fn_audio_timelines({"urls": [p]})
        self.assertEqual(out["end"], out["timelines"][-1]["end"])


class DraftGetAudioDurationTest(unittest.TestCase):
    """capcut_draft_data.get_audio_duration：本地 WAV → {duration}（微秒）。

    单位证据：7539744063333105727:96,107 把结果声明成 array<int> 后直接与
    100000（100ms）相加；7566078917871140864:62,79-97 把 duration 当微秒用于
    start/end。测试用合成 WAV（mock TTS 产物），不依赖 ffmpeg/网络。
    """

    def setUp(self):
        import tempfile
        self.tmp = tempfile.TemporaryDirectory()

    def tearDown(self):
        self.tmp.cleanup()

    def wav(self, frames, rate=16000, channels=1, sampwidth=2):
        import io
        import wave
        buf = io.BytesIO()
        with wave.open(buf, "wb") as w:
            w.setnchannels(channels)
            w.setsampwidth(sampwidth)
            w.setframerate(rate)
            w.writeframes(b"\x00" * (frames * channels * sampwidth))
        return buf.getvalue()

    def path(self, blob, name="a.wav"):
        p = os.path.join(self.tmp.name, name)
        with open(p, "wb") as fh:
            fh.write(blob)
        return p

    def test_one_second_is_exactly_one_million_us(self):
        # 负控：若实现返回毫秒/秒，这里必红。
        got = server.fn_get_audio_duration({"url": self.path(self.wav(16000))})
        self.assertEqual(got, {"duration": 1000000})

    def test_rate_and_frames_are_respected(self):
        # 11025 帧 @22050Hz = 0.5s；不是按文件大小猜。
        got = server.fn_get_audio_duration(
            {"url": self.path(self.wav(11025, rate=22050), "b.wav")})
        self.assertEqual(got["duration"], 500000)

    def test_data_url_is_accepted(self):
        import base64
        blob = self.wav(8000, rate=8000)
        url = "data:audio/wav;base64," + base64.b64encode(blob).decode()
        self.assertEqual(server.fn_get_audio_duration({"url": url})["duration"], 1000000)

    def test_record_url_is_unwrapped(self):
        p = self.path(self.wav(4000, rate=8000), "c.wav")
        got = server.fn_get_audio_duration({"url": {"link": "file://" + p}})
        self.assertEqual(got["duration"], 500000)

    def test_remote_branch_can_be_mocked_without_network(self):
        # mock 取数钩子：证明 http(s) → 字节 → 微秒这一段，不触网。
        old = server._fetch_remote
        seen = []

        def fake(url):
            seen.append(url)
            return self.wav(16000)

        server._fetch_remote = fake
        try:
            got = server.fn_get_audio_duration({"url": "https://example.com/tts.wav"})
        finally:
            server._fetch_remote = old
        self.assertEqual(got["duration"], 1000000)
        self.assertEqual(seen, ["https://example.com/tts.wav"])

    def test_remote_default_is_explicit_offline_error(self):
        with self.assertRaises(ValueError) as ctx:
            server.fn_get_audio_duration({"url": "https://example.com/tts.wav"})
        self.assertIn("REMOTE_AUDIO_UNSUPPORTED", str(ctx.exception))

    def test_non_wav_is_explicit_error(self):
        with self.assertRaises(ValueError):
            server.fn_get_audio_duration({"url": self.path(b"ID3\x04\x00", "d.mp3")})

    def test_zero_frame_wav_is_non_positive_error(self):
        with self.assertRaises(ValueError):
            server.fn_get_audio_duration({"url": self.path(self.wav(0), "e.wav")})

    def test_empty_url_is_explicit_error(self):
        with self.assertRaises(ValueError):
            server.fn_get_audio_duration({"url": ""})




def _box(typ, payload):
    return struct.pack(">I", 8 + len(payload)) + typ + payload


def mp4_blob(seconds, timescale=1000):
    """最小 MP4：ftyp + moov/mvhd（version 0）。"""
    dur = int(round(seconds * timescale))
    mvhd = (b"\x00" + b"\x00\x00\x00"
            + struct.pack(">II", 0, 0)
            + struct.pack(">II", timescale, dur)
            + b"\x00" * 80)
    return (_box(b"ftyp", b"isom" + struct.pack(">I", 512) + b"isomiso2mp41")
            + _box(b"moov", _box(b"mvhd", mvhd)))


def _ebml_vint(n):
    for length in range(1, 9):
        if n < (1 << (7 * length)) - 1:
            return bytes([(1 << (8 - length)) | (n >> (8 * (length - 1)))]) + \
                n.to_bytes(length, "big")[1:]
    raise ValueError("too large")


def _ebml(eid, payload):
    return eid + _ebml_vint(len(payload)) + payload


def webm_blob(seconds, timecode_scale=1000000):
    """最小 WebM：EBML 头 + Segment/Info(TimecodeScale, Duration=ms)。"""
    header = _ebml(b"\x1a\x45\xdf\xa3", _ebml(b"\x42\x82", b"webm"))
    info = _ebml(b"\x15\x49\xa9\x66",
                 _ebml(b"\x2a\xd7\xb1", timecode_scale.to_bytes(3, "big"))
                 + _ebml(b"\x44\x89", struct.pack(">d", seconds * 1000.0)))
    return header + _ebml(b"\x18\x53\x80\x67", info)


class VideoTimelinesTest(unittest.TestCase):
    """capcut.video_timelines / suchuang_video_compose.video_timelines。

    证据：
      - `timelines` 与 links 1:1、从 0 起相接；
      - `all_timelines` 是**单个整体区间**：7531684846948007951:50 用
        `all_timelines[0].end + all_timelines_1[0].end` 求两组视频总时长，
        7624531644146729003:242 用 `all_timelines[0]` 求单个视频时长，
        7577283712418152458:331 的手写 JS 也把 all_timelines 压成一个区间。
    """

    def setUp(self):
        import tempfile
        self.tmp = tempfile.TemporaryDirectory()

    def tearDown(self):
        self.tmp.cleanup()

    def path(self, blob, name):
        p = os.path.join(self.tmp.name, name)
        with open(p, "wb") as fh:
            fh.write(blob)
        return p

    def wav(self, seconds, rate=16000):
        import io
        import wave
        buf = io.BytesIO()
        with wave.open(buf, "wb") as w:
            w.setnchannels(1)
            w.setsampwidth(2)
            w.setframerate(rate)
            w.writeframes(b"\x00\x00" * int(seconds * rate))
        return buf.getvalue()

    def test_mp4_mvhd_duration_in_microseconds(self):
        p = self.path(mp4_blob(5.0), "a.mp4")
        out = server.fn_video_timelines({"links": [p]})
        self.assertEqual(out["timelines"], [{"start": 0, "end": 5000000}])
        self.assertEqual(out["all_timelines"], [{"start": 0, "end": 5000000}])

    def test_two_mp4_links_concatenate_and_all_is_single_span(self):
        a = self.path(mp4_blob(5.0), "a.mp4")
        b = self.path(mp4_blob(4.5), "b.mp4")
        out = server.fn_video_timelines({"links": [a, b]})
        self.assertEqual(out["timelines"], [
            {"start": 0, "end": 5000000},
            {"start": 5000000, "end": 9500000},
        ])
        # 负控：若 all_timelines 退化成 timelines 的别名，这里会得到 2 段而变红。
        self.assertEqual(out["all_timelines"], [{"start": 0, "end": 9500000}])

    def test_webm_duration(self):
        p = self.path(webm_blob(2.5), "a.webm")
        out = server.fn_video_timelines({"links": [p]})
        self.assertEqual(out["timelines"], [{"start": 0, "end": 2500000}])

    def test_wav_link_is_supported(self):
        p = self.path(self.wav(0.75), "a.wav")
        self.assertEqual(server.fn_video_timelines({"links": [p]})["timelines"],
                         [{"start": 0, "end": 750000}])

    def test_suchuang_variant_echoes_links(self):
        p = self.path(mp4_blob(1.0), "a.mp4")
        out = server.fn_suchuang_video_timelines({"links": [p]})
        self.assertEqual(out["links"], [p])
        self.assertEqual(out["timelines"], [{"start": 0, "end": 1000000}])
        self.assertEqual(out["all_timelines"], [{"start": 0, "end": 1000000}])

    def test_empty_links(self):
        self.assertEqual(server.fn_video_timelines({"links": []}),
                         {"timelines": [], "all_timelines": []})

    def test_remote_default_is_explicit_offline_error(self):
        with self.assertRaises(ValueError) as ctx:
            server.fn_video_timelines({"links": ["https://example.com/a.mp4"]})
        self.assertIn("REMOTE_VIDEO_UNSUPPORTED", str(ctx.exception))

    def test_remote_branch_can_be_mocked_without_network(self):
        old = server._fetch_remote
        seen = []
        server._fetch_remote = lambda url: (seen.append(url), mp4_blob(3.0))[1]
        try:
            out = server.fn_video_timelines({"links": ["https://example.com/a.mp4"]})
        finally:
            server._fetch_remote = old
        self.assertEqual(out["timelines"], [{"start": 0, "end": 3000000}])
        self.assertEqual(seen, ["https://example.com/a.mp4"])

    def test_unknown_container_is_explicit_error(self):
        p = self.path(b"not a media container at all", "a.bin")
        with self.assertRaises(ValueError):
            server.fn_video_timelines({"links": [p]})

def mp3_frame(bitrate_kbps=128, rate=44100):
    """MPEG1 Layer III 单帧（无 CRC、立体声），返回原始字节。"""
    idx = {32: 1, 40: 2, 48: 3, 56: 4, 64: 5, 80: 6, 96: 7, 112: 8,
           128: 9, 160: 10, 192: 11, 224: 12, 256: 13, 320: 14}[bitrate_kbps]
    srate = {44100: 0, 48000: 1, 32000: 2}[rate]
    length = 144 * bitrate_kbps * 1000 // rate
    return bytes([0xFF, 0xFB, (idx << 4) | (srate << 2), 0x00]) + b"\x00" * (length - 4)


def mp3_blob(frames, bitrate_kbps=128, rate=44100):
    frame = mp3_frame(bitrate_kbps, rate)
    return frame * frames


def mp3_blob_with_xing(frames, bitrate_kbps=128, rate=44100):
    """首帧带 Xing/Info 帧数（VBR 的常见形态）；帧体本身只有 1 帧。"""
    f = bytearray(mp3_frame(bitrate_kbps, rate))
    off = 4 + 32  # MPEG1 立体声 side info
    f[off:off + 4] = b"Info"
    f[off + 4:off + 8] = (1).to_bytes(4, "big")
    f[off + 8:off + 12] = frames.to_bytes(4, "big")
    return bytes(f)


class Mp3DurationTest(unittest.TestCase):
    """MP3 时长：TTS 产物是 mp3（语料 229 处 get_audio_duration）。

    不引入解码器：按 MPEG1 Layer III 帧头累计采样数；带 Xing/Info 时用其帧数。
    """

    def setUp(self):
        import tempfile
        self.tmp = tempfile.TemporaryDirectory()

    def tearDown(self):
        self.tmp.cleanup()

    def path(self, blob, name):
        p = os.path.join(self.tmp.name, name)
        with open(p, "wb") as fh:
            fh.write(blob)
        return p

    def test_cbr_frame_count_to_microseconds(self):
        p = self.path(mp3_blob(100), "a.mp3")
        self.assertEqual(server.fn_get_audio_duration({"url": p}), {"duration": 2612245})

    def test_xing_frame_count_is_preferred(self):
        p = self.path(mp3_blob_with_xing(200), "b.mp3")
        self.assertEqual(server.fn_get_audio_duration({"url": p})["duration"], 5224490)

    def test_id3v2_header_is_skipped(self):
        id3 = b"ID3\x03\x00\x00\x00\x00\x00\x14" + b"\x00" * 20
        p = self.path(id3 + mp3_blob(50), "c.mp3")
        self.assertEqual(server.fn_get_audio_duration({"url": p})["duration"], 1306122)

    def test_audio_timelines_accepts_mp3(self):
        p = self.path(mp3_blob(100), "d.mp3")
        got = server.fn_audio_timelines({"urls": [p]})
        self.assertEqual(got["timelines"], [{"start": 0, "end": 2612245}])

    def test_video_timelines_accepts_mp3(self):
        p = self.path(mp3_blob(100), "e.mp3")
        self.assertEqual(server.fn_video_timelines({"links": [p]})["timelines"],
                         [{"start": 0, "end": 2612245}])

    def test_truncated_mp3_is_explicit_error(self):
        p = self.path(b"\xff\xfb\x90\x00", "f.mp3")
        with self.assertRaises(ValueError):
            server.fn_get_audio_duration({"url": p})


if __name__ == "__main__":
    unittest.main(verbosity=2)
