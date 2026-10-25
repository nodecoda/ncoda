#!/usr/bin/env python3
"""capcut_data 插件的函数级判据（正控 + 负控）。

run: python3 plugins/capcut_data/test_server.py
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
    def test_wraps_single_output(self):
        got = server.fn_get_url({"output": "https://x/a.png"})
        self.assertEqual(got["output"], "https://x/a.png")
        self.assertEqual(got["outputList"], [{"output": "https://x/a.png"}])

    def test_accepts_dollar_arg(self):
        got = server.fn_get_url({"output": "u"})
        self.assertEqual(got["output"], "u")

    def test_missing_is_empty_not_error(self):
        got = server.fn_get_url({})
        self.assertEqual(got["outputList"], [{"output": ""}])
        self.assertEqual(got["output"], "")


class ObjsToStrListTest(unittest.TestCase):
    def test_flattens_get_url_results(self):
        # 语料形态：yield outputList = <get_url 结果>
        rows = [{"outputList": server.fn_get_url({"output": "a"})},
                {"outputList": server.fn_get_url({"output": "b"})}]
        self.assertEqual(server.fn_objs_to_str_list({"outputs": rows})["infos"], ["a", "b"])

    def test_accepts_raw_strings(self):
        self.assertEqual(
            server.fn_objs_to_str_list({"outputs": ["a", "", "b"]})["infos"], ["a", "b"])

    def test_empty(self):
        self.assertEqual(server.fn_objs_to_str_list({})["infos"], [])


class StrToListTest(unittest.TestCase):
    def test_string_becomes_single_element(self):
        self.assertEqual(server.fn_str_to_list({"obj": "u"})["infos"], ["u"])

    def test_file_object_uses_url(self):
        self.assertEqual(
            server.fn_str_to_list({"obj": {"url": "u", "other": 1}})["infos"], ["u"])

    def test_list_flattens(self):
        self.assertEqual(
            server.fn_str_to_list({"obj": ["a", {"url": "b"}]})["infos"], ["a", "b"])

    def test_default_zero_is_empty(self):
        self.assertEqual(server.fn_str_to_list({"obj": 0})["infos"], [])


class ArrayPairTest(unittest.TestCase):
    def test_pairs_consecutive(self):
        got = server.fn_array_pair({"items": ["a", "b", "c", "d"]})
        self.assertEqual(got["groups"], [{"group": ["a", "b"]}, {"group": ["c", "d"]}])
        self.assertEqual(got["total_groups"], 2)

    def test_odd_tail(self):
        got = server.fn_array_pair({"items": ["a", "b", "c"]})
        self.assertEqual(got["groups"], [{"group": ["a", "b"]}, {"group": ["c"]}])

    def test_empty(self):
        self.assertEqual(server.fn_array_pair({})["groups"], [])


class ExtractFirstEndFrameTest(unittest.TestCase):
    def test_first_and_last(self):
        got = server.fn_extract_first_end_frame({"items": ["a", "b", "c"]})
        self.assertEqual((got["first_item"], got["end_item"]), ("a", "c"))

    def test_single(self):
        got = server.fn_extract_first_end_frame({"items": ["a"]})
        self.assertEqual((got["first_item"], got["end_item"]), ("a", "a"))

    def test_empty(self):
        got = server.fn_extract_first_end_frame({})
        self.assertEqual((got["first_item"], got["end_item"]), ("", ""))


class TimelinesTest(unittest.TestCase):
    def test_even_split(self):
        got = server.fn_timelines({"duration": 5000000, "num": 3})
        self.assertEqual(got["all_timelines"], [{"start": 0, "end": 5000000}])
        self.assertEqual(got["timelines"], [
            {"start": 0, "end": 1666666},
            {"start": 1666666, "end": 3333332},
            {"start": 3333332, "end": 5000000},
        ])
        self.assertEqual(got["timelines"][-1]["end"], 5000000)

    def test_start_offset(self):
        got = server.fn_timelines({"duration": 1000000, "num": 1, "start": 2000000})
        self.assertEqual(got["timelines"], [{"start": 2000000, "end": 3000000}])
        self.assertEqual(got["all_timelines"], [{"start": 2000000, "end": 3000000}])

    def test_num_zero_has_only_all(self):
        got = server.fn_timelines({"duration": 100, "num": 0})
        self.assertEqual(got["timelines"], [])
        self.assertEqual(got["all_timelines"], [{"start": 0, "end": 100}])

    def test_ints_not_floats(self):
        got = server.fn_timelines({"duration": 3, "num": 3})
        for seg in got["timelines"]:
            self.assertIsInstance(seg["start"], int)
            self.assertIsInstance(seg["end"], int)
        self.assertEqual([s["end"] - s["start"] for s in got["timelines"]], [1, 1, 1])


class WenanLinksMergeTest(unittest.TestCase):
    def test_column_merge(self):
        got = server.fn_wenan_links_merge({
            "link1": ["a1", "a2"], "link2": ["b1"], "link3": []})
        self.assertEqual(got["links_list"], [{"links": ["a1", "b1"]}, {"links": ["a2"]}])

    def test_empty_lanes(self):
        self.assertEqual(server.fn_wenan_links_merge({})["links_list"], [])

    def test_skips_blank_entries(self):
        got = server.fn_wenan_links_merge({"link1": ["", "a"], "link2": [0, "b"]})
        self.assertEqual(got["links_list"], [{"links": ["a", "b"]}])


class WenanSplitTest(unittest.TestCase):
    def test_contiguous_even_split(self):
        got = server.fn_wenan_split({"wenan": [str(i) for i in range(10)]})
        self.assertEqual([len(got["wenan%d" % i]) for i in range(1, 6)], [2, 2, 2, 2, 2])
        self.assertEqual(got["wenan1"], ["0", "1"])
        self.assertEqual(got["wenan5"], ["8", "9"])

    def test_tail_keeps_order(self):
        got = server.fn_wenan_split({"wenan": [str(i) for i in range(11)]})
        self.assertEqual([len(got["wenan%d" % i]) for i in range(1, 6)], [3, 3, 3, 2, 0])
        flat = [x for i in range(1, 6) for x in got["wenan%d" % i]]
        self.assertEqual(flat, [str(i) for i in range(11)])

    def test_empty(self):
        got = server.fn_wenan_split({})
        self.assertEqual([got["wenan%d" % i] for i in range(1, 6)], [[]] * 5)


class TimelinesFormatTest(unittest.TestCase):
    def test_keeps_order_and_extracts_text(self):
        got = server.fn_timelines_format({"timelines": [
            {"start": 0, "end": 1000000, "text": "第一句"},
            {"start": 1000000.9, "end": 2500000, "content": "第二句"},
        ]})
        self.assertEqual(got["all_timelines"], [{"start": 0, "end": 1000000},
                                                {"start": 1000000, "end": 2500000}])
        self.assertEqual(got["all_formatted_texts"], ["第一句", "第二句"])

    def test_missing_text_is_empty_string(self):
        got = server.fn_timelines_format({"timelines": [{"start": 0, "end": 5}]})
        self.assertEqual(got["all_formatted_texts"], [""])

    def test_skips_items_without_start_end(self):
        got = server.fn_timelines_format({"timelines": [{"text": "x"}, "bad", {"start": 0, "end": 1}]})
        self.assertEqual(got["all_timelines"], [{"start": 0, "end": 1}])

    def test_empty(self):
        got = server.fn_timelines_format({})
        self.assertEqual(got["all_timelines"], [])
        self.assertEqual(got["all_formatted_texts"], [])


class ManifestDeclarationTest(unittest.TestCase):
    """实现与清单必须一致：fidelity/loss 不是注释，而是被判据消费的输入。"""

    def test_wenan_split_is_declared_partial_with_loss(self):
        import yaml
        with open(os.path.join(HERE, "ncoda-plugin.yaml"), encoding="utf-8") as fh:
            doc = yaml.safe_load(fh)
        lib = doc["libraries"][0]
        self.assertEqual(lib["library"], "capcut_data")
        entries = {}
        for item in lib["functions"]:
            if isinstance(item, str):
                entries[item] = {"fidelity": lib.get("fidelity", "real"), "loss": lib.get("loss", "")}
            else:
                entries[item["name"]] = {
                    "fidelity": item.get("fidelity") or lib.get("fidelity", "real"),
                    "loss": item.get("loss") or lib.get("loss", ""),
                }
        self.assertEqual(sorted(entries), sorted(server.FUNCTIONS))
        for fn in server.PARTIAL_FUNCTIONS:
            self.assertEqual(entries[fn]["fidelity"], "partial", fn)
            self.assertTrue(entries[fn]["loss"].strip(), "partial must document loss: " + fn)
        for fn in server.REAL_FUNCTIONS:
            self.assertEqual(entries[fn]["fidelity"], "real")


class AudioTimelinesTest(unittest.TestCase):
    """audio_timelines：本地 WAV 的时长 → 首尾相接的时间线。

    证据：语料把 timelines 与 mp3_urls 逐条配对、把 all_timelines 与单个
    BGM 配对（X52/X65）；同库 timelines() 的 all_timelines 也是整段。
    """

    def wav(self, seconds, rate=16000):
        import io
        import struct as st
        import wave
        buf = io.BytesIO()
        with wave.open(buf, "wb") as w:
            w.setnchannels(1)
            w.setsampwidth(2)
            w.setframerate(rate)
            w.writeframes(b"\x00\x00" * int(seconds * rate))
        return buf.getvalue()

    def path(self, blob, name="a.wav"):
        p = os.path.join(self.tmp.name, name)
        with open(p, "wb") as fh:
            fh.write(blob)
        return p

    def setUp(self):
        import tempfile
        self.tmp = tempfile.TemporaryDirectory()

    def tearDown(self):
        self.tmp.cleanup()

    def test_two_wavs_are_sequential_from_zero(self):
        a = self.path(self.wav(1.0), "a.wav")
        b = self.path(self.wav(0.5), "b.wav")
        out = server.fn_audio_timelines({"links": [a, b]})
        self.assertEqual(out["timelines"], [
            {"start": 0, "end": 1000000},
            {"start": 1000000, "end": 1500000},
        ])
        # all_timelines 是整段（语料只拿它配单个 BGM）
        self.assertEqual(out["all_timelines"], [{"start": 0, "end": 1500000}])
        self.assertEqual(out["outputList"], [])

    def test_file_url_and_data_url_are_accepted(self):
        blob = self.wav(0.25)
        p = self.path(blob, "c.wav")
        out1 = server.fn_audio_timelines({"links": ["file://" + p]})
        import base64
        out2 = server.fn_audio_timelines(
            {"links": ["data:audio/wav;base64," + base64.b64encode(blob).decode()]})
        self.assertEqual(out1, out2)
        self.assertEqual(out1["timelines"], [{"start": 0, "end": 250000}])

    def test_records_are_unwrapped(self):
        p = self.path(self.wav(0.5), "d.wav")
        out = server.fn_audio_timelines({"links": [{"link": "file://" + p}]})
        self.assertEqual(out["timelines"], [{"start": 0, "end": 500000}])

    def test_empty_links_yield_empty_arrays(self):
        self.assertEqual(server.fn_audio_timelines({"links": []}),
                         {"timelines": [], "all_timelines": [], "outputList": []})

    def test_remote_url_is_an_explicit_error(self):
        with self.assertRaises(ValueError) as ctx:
            server.fn_audio_timelines({"links": ["https://example.com/a.mp3"]})
        self.assertIn("REMOTE_AUDIO_UNSUPPORTED", str(ctx.exception))

    def test_mp3_bytes_are_rejected_not_guessed(self):
        p = self.path(b"ID3\x04\x00\x00\x00", "e.mp3")
        with self.assertRaises(ValueError) as ctx:
            server.fn_audio_timelines({"links": [p]})
        self.assertIn("WAV", str(ctx.exception))

    def test_negative_control_wrong_duration_is_red(self):
        # 负控：把 1s 的 WAV 报成 0.5s 会立刻破坏“时间线总长 == 音频总长”。
        p = self.path(self.wav(1.0), "f.wav")
        out = server.fn_audio_timelines({"links": [p]})
        self.assertEqual(out["timelines"][-1]["end"], 1000000)


class ProtocolTest(unittest.TestCase):
    def test_handshake_matches_manifest_functions(self):
        proc = subprocess.run(
            [sys.executable, os.path.join(HERE, "server.py")],
            input=json.dumps({"jsonrpc": "2.0", "id": 1, "method": "initialize",
                              "params": {}}) + "\n",
            capture_output=True, text=True, timeout=10, check=True)
        res = json.loads(proc.stdout.splitlines()[0])["result"]
        self.assertEqual(res["protocol"], "ncoda-plugin/1")
        self.assertEqual(res["id"], "capcut-data")
        self.assertEqual(res["libraries"][0]["library"], "capcut_data")
        self.assertEqual(sorted(res["libraries"][0]["functions"]), sorted(server.FUNCTIONS))

    def test_unknown_symbol_is_contract_error(self):
        got = server.HANDLERS.get("capcut_data.nope")
        self.assertIsNone(got)
        # 未实现的函数不许悄悄返回空值：必须 not-implemented
        proc = subprocess.run(
            [sys.executable, os.path.join(HERE, "server.py")],
            input="\n".join([
                json.dumps({"jsonrpc": "2.0", "id": 1, "method": "invoke",
                            "params": {"symbol": "capcut_data.search_sticker",
                                       "args": {}, "deadline_ms": 1000}}),
            ]) + "\n",
            capture_output=True, text=True, timeout=10, check=True)
        err = json.loads(proc.stdout.splitlines()[0])["error"]
        self.assertEqual(err["data"]["class"], "not-implemented")

    def test_shutdown_exits(self):
        proc = subprocess.run(
            [sys.executable, os.path.join(HERE, "server.py")],
            input="\n".join([
                json.dumps({"jsonrpc": "2.0", "id": 1, "method": "ping"}),
                json.dumps({"jsonrpc": "2.0", "id": 2, "method": "shutdown"}),
            ]) + "\n",
            capture_output=True, text=True, timeout=10, check=True)
        lines = [json.loads(l) for l in proc.stdout.splitlines() if l.strip()]
        self.assertEqual(lines[0]["result"], {"ok": True})
        self.assertEqual(lines[1]["result"], {})


if __name__ == "__main__":
    unittest.main(verbosity=2)
