#!/usr/bin/env python3
"""compose_toolkit 的函数级判据（正控 + 负控）。

run: python3 plugins/compose_toolkit/test_server.py
"""
import json
import os
import subprocess
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import server  # noqa: E402


class TextSplitterTest(unittest.TestCase):
    def test_capcut_splits_paragraphs_and_sentences(self):
        got = server.fn_capcut_text_splitter({"text": "甲。乙。\n\n丙。"})
        self.assertEqual(got["segments"], ["甲。", "乙。", "丙。"])
        self.assertEqual(got["error"], "")

    def test_suchuang_has_output_list(self):
        got = server.fn_suchuang_text_splitter({"text": "甲。乙。"})
        self.assertEqual(got["segments"], ["甲。", "乙。"])
        self.assertEqual(got["outputList"], got["segments"])

    def test_empty(self):
        self.assertEqual(server.fn_capcut_text_splitter({})["segments"], [])

    def test_container_is_invalid_args(self):
        with self.assertRaises(ValueError):
            server.fn_capcut_text_splitter({"text": {"x": 1}})


class AudioLinkCollectorTest(unittest.TestCase):
    def test_flattens_nested_tts_results(self):
        # 语料形态：yield outputList = tts.speech_synthesis(...)
        rows = [{"outputList": {"data": {"link": "a.mp3"}}},
                {"data": {"link": "b.mp3"}}]
        self.assertEqual(server.fn_audio_link_collector({"outputList": rows})["links"],
                         ["a.mp3", "b.mp3"])

    def test_raw_strings_and_empty(self):
        self.assertEqual(server.fn_audio_link_collector({"outputList": ["x", "", "y"]})["links"],
                         ["x", "y"])
        self.assertEqual(server.fn_audio_link_collector({})["links"], [])


class TextCleanTest(unittest.TestCase):
    def test_empty_removal(self):
        got = server.fn_text_empty_removal({"text": ["a", "", None, " ", "b"]})
        self.assertEqual(got["wenan"], ["a", "b"])

    def test_add_list_only(self):
        self.assertEqual(server.fn_add_list({"add_list": ["a", "b"]})["list"], ["a", "b"])

    def test_add_list_item_and_list_priority(self):
        self.assertEqual(server.fn_add_list({"add_list": ["a"], "item": "b"})["list"], ["a", "b"])
        self.assertEqual(server.fn_add_list({"add_list": ["a"], "item": "b", "list": ["z"]})["list"],
                         ["z"])


class TimelineMergeTest(unittest.TestCase):
    def test_shifts_main_after_pre_plus_gap(self):
        got = server.fn_timeline_merge({
            "pre_timeline": [{"start": 0, "end": 1000}],
            "main_timeline": [{"start": 0, "end": 500}],
            "gap_us": 200,
        })
        self.assertEqual(got["main_timelines"], [{"start": 1200, "end": 1700}])
        self.assertEqual(got["timelines"], [{"start": 0, "end": 1000},
                                            {"start": 1200, "end": 1700}])
        self.assertEqual(got["all_main_timeline"], got["main_timelines"])
        self.assertEqual(got["all_pre_timeline"], got["pre_timelines"])
        self.assertEqual(got["all_complete_timeline"], got["timelines"])
        self.assertEqual(got["last_end_us"], 1700)
        self.assertEqual(got["all_timeline"], [{"start": 0, "end": 1700}])

    def test_no_pre_keeps_main(self):
        got = server.fn_timeline_merge({"main_timeline": [{"start": 10, "end": 20}], "gap_us": 99})
        self.assertEqual(got["main_timelines"], [{"start": 10, "end": 20}])

    def test_empty(self):
        got = server.fn_timeline_merge({})
        self.assertEqual(got["timelines"], [])
        self.assertEqual(got["last_end_us"], 0)


class WenanRangeTest(unittest.TestCase):
    TL = [{"start": 0, "end": 100}, {"start": 100, "end": 200},
          {"start": 200, "end": 300}, {"start": 300, "end": 400}]

    def test_one_to_one(self):
        got = server.fn_wenan_timeline_range({"timelines": self.TL,
                                              "wenan": ["a", "b", "c", "d"]})
        self.assertEqual([x["content"] for x in got["wenanTimeline"]], ["a", "b", "c", "d"])
        self.assertEqual(got["wenanTimeline"][2], {"content": "c", "start": 200, "end": 300})

    def test_merge_count_two(self):
        got = server.fn_wenan_timeline_range({"timelines": self.TL, "wenan": ["a", "b"],
                                              "mergeCount": 2})
        self.assertEqual(got["wenanTimeline"],
                         [{"content": "a", "start": 0, "end": 200},
                          {"content": "b", "start": 200, "end": 400}])

    def test_start_index(self):
        got = server.fn_wenan_timeline_range({"timelines": self.TL, "wenan": ["x"],
                                              "startIndex": 2})
        self.assertEqual(got["wenanTimeline"], [{"content": "x", "start": 200, "end": 400}])

    def test_range_merge_uses_float(self):
        got = server.fn_wenan_timeline_range_merge({"timelines": self.TL, "wenan": ["a"]})
        self.assertIsInstance(got["wenanTimeline"][0]["start"], float)

    def test_empty(self):
        self.assertEqual(server.fn_wenan_timeline_range({})["wenanTimeline"], [])


class MediaTileTest(unittest.TestCase):
    def test_no_duration_keeps_timeline(self):
        got = server.fn_media_tile({"all_timeline": [{"start": 0, "end": 1000}], "media_url": "u"})
        self.assertEqual(got["timelines"], [{"start": 0, "end": 1000}])
        self.assertEqual(got["urls"], ["u"])

    def test_duration_splits(self):
        got = server.fn_media_tile({"all_timeline": [{"start": 0, "end": 1000}],
                                    "media_url": "u", "duration": 400})
        self.assertEqual(got["timelines"], [{"start": 0, "end": 400},
                                            {"start": 400, "end": 800},
                                            {"start": 800, "end": 1000}])
        self.assertEqual(got["urls"], ["u", "u", "u"])

    def test_empty(self):
        self.assertEqual(server.fn_media_tile({}), {"timelines": [], "urls": []})


class TimelineGeneratorTest(unittest.TestCase):
    def test_seconds(self):
        got = server.fn_timeline_generator({"duration": "5", "items": ["a", "b"]})
        self.assertEqual(got["timelines"], [{"start": 0, "end": 5_000_000},
                                            {"start": 5_000_000, "end": 10_000_000}])
        self.assertEqual(got["items_timelines"][0]["content"], "a")
        self.assertEqual(got["all_timeline"], [{"start": 0, "end": 10_000_000}])

    def test_unit_ms(self):
        got = server.fn_timeline_generator({"duration": "5", "unit": "ms", "items": ["a"]})
        self.assertEqual(got["timelines"], [{"start": 0, "end": 5000}])

    def test_empty(self):
        self.assertEqual(server.fn_timeline_generator({})["timelines"], [])


class RollingEffectTest(unittest.TestCase):
    def test_cumulative_and_cycles_texts(self):
        got = server.fn_rolling_effect({"duration_list": [100, 200, 300],
                                        "str_list": ["a", "b"]})
        self.assertEqual(got["timelines"], [{"start": 0, "end": 100},
                                            {"start": 100, "end": 300},
                                            {"start": 300, "end": 600}])
        self.assertEqual(got["subject_arr"], ["a", "b", "a"])
        self.assertEqual(got["all_timeline"], [{"start": 0, "end": 600}])

    def test_empty(self):
        self.assertEqual(server.fn_rolling_effect({})["timelines"], [])


class DialogTest(unittest.TestCase):
    def test_split_by_role(self):
        got = server.fn_dialog_split({"roles": "Q|A",
                                      "dialogs": ["Q: 问一\nA: 答一", "Q: 问二"]})
        self.assertEqual([g["role"] for g in got["roleDialogs"]], ["Q", "A"])
        q = got["roleDialogs"][0]["dialogs"]
        self.assertEqual([d["content"] for d in q], ["问一", "问二"])
        self.assertEqual([d["idx"] for d in q], [0, 2])

    def test_split_from_text_splitter_records(self):
        got = server.fn_dialog_split({"roles": "Q|A",
                                      "dialogs": [{"segments": ["Q: x", "A: y"]}]})
        self.assertEqual(sum(len(g["dialogs"]) for g in got["roleDialogs"]), 2)

    def test_merge_by_idx(self):
        role_dialogs = [{"role": "Q", "dialogs": [{"content": "q1", "idx": 0, "role": "Q"},
                                                  {"content": "q2", "idx": 2, "role": "Q"}]},
                        {"role": "A", "dialogs": [{"content": "a1", "idx": 1, "role": "A"}]}]
        got = server.fn_dialog_merge({"audioLinks": {"Q": ["q1.mp3", "q2.mp3"], "A": ["a1.mp3"]},
                                      "roleDialogs": role_dialogs})
        self.assertEqual(got["mergedAudioLinks"], ["q1.mp3", "a1.mp3", "q2.mp3"])

    def test_dialog_wenan_timeline(self):
        role_dialogs = [{"role": "Q", "dialogs": [{"content": "q1", "idx": 0, "role": "Q"}]},
                        {"role": "A", "dialogs": [{"content": "a1", "idx": 1, "role": "A"}]}]
        got = server.fn_dialog_wenan_timeline({
            "roleDialogs": role_dialogs,
            "timelines": [{"start": 0, "end": 10}, {"start": 10, "end": 20}]})
        self.assertEqual(got["roleWenanTimeline"][0]["wenan"], ["q1"])
        self.assertEqual(got["roleWenanTimeline"][1]["timelines"], [{"start": 10, "end": 20}])


class TimelineAlignTest(unittest.TestCase):
    def test_align_end_matches_next_start(self):
        got = server.fn_timeline_align({"align": "end", "timelines": [
            {"start": 0, "end": 10}, {"start": 4, "end": 20}, {"start": 20, "end": 30}]})
        self.assertEqual(got["align_timelines"], [
            {"start": 0, "end": 4}, {"start": 4, "end": 20}, {"start": 20, "end": 30}])
        self.assertEqual(got["all_timeline"], [{"start": 0, "end": 30}])

    def test_other_align_keeps_input(self):
        rows = [{"start": 5, "end": 9}]
        got = server.fn_timeline_align({"align": "start", "timelines": rows})
        self.assertEqual(got["align_timelines"], rows)

    def test_empty(self):
        got = server.fn_timeline_align({"align": "end"})
        self.assertEqual(got["align_timelines"], [])
        self.assertEqual(got["all_timeline"], [])

    def test_bad_timelines_raise(self):
        with self.assertRaises(ValueError):
            server.fn_timeline_align({"align": "end", "timelines": "nope"})



def mp3_frame(bitrate_kbps=128, rate=44100):
    idx = {32: 1, 40: 2, 48: 3, 56: 4, 64: 5, 80: 6, 96: 7, 112: 8,
           128: 9, 160: 10, 192: 11, 224: 12, 256: 13, 320: 14}[bitrate_kbps]
    srate = {44100: 0, 48000: 1, 32000: 2}[rate]
    length = 144 * bitrate_kbps * 1000 // rate
    return bytes([0xFF, 0xFB, (idx << 4) | (srate << 2), 0x00]) + b"\x00" * (length - 4)


def mp3_blob(frames, bitrate_kbps=128, rate=44100):
    return mp3_frame(bitrate_kbps, rate) * frames


class SuchuangAudioDurationTest(unittest.TestCase):
    """suchuang_video_compose.get_audio_duration：本地 WAV → unit 决定的数值。

    证据：39 个调用点全部显式传 unit；秒值在 7554469499115765795 的手写 JS 里
    ×1000000，微秒值在 7560292374086385727 直接进 array<int> 时间线。
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

    def test_microseconds_unit_returns_exact_us(self):
        got = server.fn_suchuang_get_audio_duration(
            {"mp3_url": self.path(self.wav(16000)), "unit": "微秒"})
        self.assertEqual(got["duration"], 1000000)
        self.assertEqual(got["outputList"], [{"duration": 1000000}])

    def test_seconds_unit_returns_seconds(self):
        # 负控：消费方在 JS 里对它 ×1e6；若这里误返回 µs，会放大 1e6 倍。
        got = server.fn_suchuang_get_audio_duration(
            {"mp3_url": self.path(self.wav(16000)), "unit": "秒"})
        self.assertEqual(got["duration"], 1.0)

    def test_missing_unit_defaults_to_microseconds(self):
        got = server.fn_suchuang_get_audio_duration({"mp3_url": self.path(self.wav(8000, rate=8000))})
        self.assertEqual(got["duration"], 1000000)

    def test_rate_and_frames_are_respected(self):
        got = server.fn_suchuang_get_audio_duration(
            {"mp3_url": self.path(self.wav(11025, rate=22050), "b.wav"), "unit": "微秒"})
        self.assertEqual(got["duration"], 500000)

    def test_data_url_is_accepted(self):
        import base64
        blob = self.wav(8000, rate=8000)
        url = "data:audio/wav;base64," + base64.b64encode(blob).decode()
        got = server.fn_suchuang_get_audio_duration({"mp3_url": url, "unit": "秒"})
        self.assertEqual(got["duration"], 1.0)

    def test_record_url_is_unwrapped(self):
        p = self.path(self.wav(4000, rate=8000), "c.wav")
        got = server.fn_suchuang_get_audio_duration(
            {"mp3_url": {"link": "file://" + p}, "unit": "微秒"})
        self.assertEqual(got["duration"], 500000)

    def test_mp3_uses_frame_count_in_microseconds(self):
        got = server.fn_suchuang_get_audio_duration(
            {"mp3_url": self.path(mp3_blob(100), "m.mp3"), "unit": "微秒"})
        self.assertEqual(got["duration"], 2612245)

    def test_mp3_seconds_unit(self):
        # 负控：若 mp3 分支忘记换单位，这里会是 2612244.9 而不是 2.61。
        got = server.fn_suchuang_get_audio_duration(
            {"mp3_url": self.path(mp3_blob(100), "m2.mp3"), "unit": "秒"})
        self.assertAlmostEqual(got["duration"], 2.6122449, places=5)

    def test_mp3_with_id3v2_header(self):
        id3 = b"ID3\x03\x00\x00\x00\x00\x00\x14" + b"\x00" * 20
        got = server.fn_suchuang_get_audio_duration(
            {"mp3_url": self.path(id3 + mp3_blob(50), "m3.mp3"), "unit": "微秒"})
        self.assertEqual(got["duration"], 1306122)

    def test_remote_branch_can_be_mocked_without_network(self):
        old = server._fetch_remote
        seen = []

        def fake(url):
            seen.append(url)
            return self.wav(16000)

        server._fetch_remote = fake
        try:
            got = server.fn_suchuang_get_audio_duration(
                {"mp3_url": "https://example.com/tts.wav", "unit": "秒"})
        finally:
            server._fetch_remote = old
        self.assertEqual(got["duration"], 1.0)
        self.assertEqual(seen, ["https://example.com/tts.wav"])

    def test_remote_default_is_explicit_offline_error(self):
        with self.assertRaises(ValueError) as ctx:
            server.fn_suchuang_get_audio_duration(
                {"mp3_url": "https://example.com/tts.wav", "unit": "微秒"})
        self.assertIn("REMOTE_AUDIO_UNSUPPORTED", str(ctx.exception))

    def test_unsupported_unit_is_explicit_error(self):
        with self.assertRaises(ValueError) as ctx:
            server.fn_suchuang_get_audio_duration(
                {"mp3_url": self.path(self.wav(100)), "unit": "帧"})
        self.assertIn("unsupported unit", str(ctx.exception))

    def test_non_wav_is_explicit_error(self):
        with self.assertRaises(ValueError):
            server.fn_suchuang_get_audio_duration(
                {"mp3_url": self.path(b"ID3\x04\x00", "d.mp3"), "unit": "微秒"})

    def test_zero_frame_wav_is_non_positive_error(self):
        with self.assertRaises(ValueError):
            server.fn_suchuang_get_audio_duration(
                {"mp3_url": self.path(self.wav(0), "e.wav"), "unit": "微秒"})

    def test_empty_url_is_explicit_error(self):
        with self.assertRaises(ValueError) as ctx:
            server.fn_suchuang_get_audio_duration({"mp3_url": "", "unit": "微秒"})
        self.assertIn("MISSING_INPUT", str(ctx.exception))


class ManifestDeclarationTest(unittest.TestCase):
    def test_partial_entries_match_server_and_have_loss(self):
        import yaml
        with open(os.path.join(HERE, "ncoda-plugin.yaml"), encoding="utf-8") as fh:
            doc = yaml.safe_load(fh)
        entries = {}
        for lib in doc["libraries"]:
            base = lib.get("fidelity", "real")
            for item in lib["functions"]:
                if isinstance(item, str):
                    entries["%s.%s" % (lib["library"], item)] = {
                        "fidelity": base, "loss": lib.get("loss", "")}
                else:
                    entries["%s.%s" % (lib["library"], item["name"])] = {
                        "fidelity": item.get("fidelity") or base,
                        "loss": item.get("loss") or lib.get("loss", "")}
        self.assertEqual(set(entries), set(server.HANDLERS))
        for sym, e in entries.items():
            if e["fidelity"] in ("partial", "stub"):
                self.assertTrue(e["loss"].strip(), "partial must document loss: " + sym)


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
                            "params": {"symbol": "capcut_helper.nope", "args": {},
                                       "deadline_ms": 1000}}])
        self.assertEqual(lines[0]["error"]["data"]["class"], "not-implemented")

    def test_invalid_args(self):
        lines = self.call([{"jsonrpc": "2.0", "id": 1, "method": "invoke",
                            "params": {"symbol": "capcut_helper.text_splitter",
                                       "args": {"text": {"x": 1}}, "deadline_ms": 1000}}])
        self.assertEqual(lines[0]["error"]["data"]["class"], "invalid-args")

    def test_ping_shutdown(self):
        lines = self.call([{"jsonrpc": "2.0", "id": 1, "method": "ping"},
                           {"jsonrpc": "2.0", "id": 2, "method": "shutdown"}])
        self.assertEqual(lines[0]["result"], {"ok": True})
        self.assertEqual(lines[1]["result"], {})


if __name__ == "__main__":
    unittest.main(verbosity=2)
