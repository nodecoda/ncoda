#!/usr/bin/env python3
"""align_toolkit 的函数级判据（正控 + 负控）。

run: python3 plugins/align_toolkit/test_server.py
"""
import os
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import server  # noqa: E402


def free(texts, durations, **kw):
    args = {"texts": texts, "duration_list": durations}
    args.update(kw)
    return server.fn_audio_align_free(args)


def alt(texts, durations, **kw):
    args = {"texts": texts, "duration_list": durations}
    args.update(kw)
    return server.fn_audio_subtitle_align_alt(args)


class OneToOneTest(unittest.TestCase):
    def test_no_split_keeps_text_and_concatenates(self):
        got = free(["你好世界", "再见!"], [1000000, 500000])
        # 语料 cleanRegex 删 ASCII '!'；中文内容不变。
        self.assertEqual(got["text_captions"], ["你好世界", "再见"])
        self.assertEqual(got["text_timielines"],
                         [{"start": 0, "end": 1000000},
                          {"start": 1000000, "end": 1500000}])

    def test_total_duration_is_preserved(self):
        got = free(["a", "b", "c"], [1, 2, 3])
        self.assertEqual(got["text_timielines"][-1]["end"], 6)
        self.assertEqual(len(got["text_captions"]), 3)


class SplitTest(unittest.TestCase):
    def test_punctuation_split_then_proportional_duration(self):
        # 10 字 + '，' + 15 字：maxLen=25 时在逗号处切，逗号被 clean 删除。
        text = "一二三四五六七八九十，后面还有很长的一段文字没有标点"
        got = free([text], [1000])
        self.assertEqual(got["text_captions"],
                         ["一二三四五六七八九十", "后面还有很长的一段文字没有标点"])
        # 10:15 分摊 1000µs → 400 + 600（末段取余数）。
        self.assertEqual(got["text_timielines"],
                         [{"start": 0, "end": 400}, {"start": 400, "end": 1000}])

    def test_cjk_boundary_split_without_punctuation(self):
        got = free(["字" * 30], [3000])
        self.assertEqual([len(c) for c in got["text_captions"]], [25, 5])
        self.assertEqual(got["text_timielines"][-1]["end"], 3000)

    def test_priority_beats_position(self):
        # '。' 在更后面但不在窗口内；'，' 优先于汉字边界。
        got = free(["ab，cd。ef"], [100], text_max=5)
        self.assertEqual(got["text_captions"], ["ab", "cdef"])
        self.assertEqual(got["text_timielines"][-1]["end"], 100)

    def test_ascii_comma_is_not_cleaned(self):
        # 语料 cleanRegex 不含 ASCII 逗号：切在逗号后但保留逗号。
        got = free(["ab,cd"], [100], text_max=3)
        self.assertEqual(got["text_captions"], ["ab,", "cd"])
        self.assertEqual(got["text_timielines"],
                         [{"start": 0, "end": 60}, {"start": 60, "end": 100}])

    def test_fullwidth_punctuation_is_cleaned(self):
        got = free(["你好，世界！"], [1000])
        self.assertEqual(got["text_captions"], ["你好世界"])

    def test_empty_after_clean_is_explicit_marker(self):
        got = free(["！！！"], [1000])
        self.assertEqual(got["text_captions"], ["[无内容]"])
        self.assertEqual(got["text_timielines"], [{"start": 0, "end": 1000}])

    def test_text_max_is_honoured(self):
        got = free(["abcdefgh"], [1000], text_max=4)
        self.assertEqual(got["text_captions"], ["abcd", "efgh"])
        self.assertEqual(got["text_timielines"],
                         [{"start": 0, "end": 500}, {"start": 500, "end": 1000}])

    def test_default_max_line_is_25(self):
        one = free(["a" * 25], [100])
        self.assertEqual(one["text_captions"], ["a" * 25])
        two = free(["a" * 26], [100])
        self.assertEqual([len(c) for c in two["text_captions"]], [25, 1])

    def test_non_bmp_stays_on_char_boundary(self):
        # JS 按 UTF-16 码元计长（emoji=2）；本实现不把代理对切成半个字符。
        got = free(["😀" * 14], [2800])
        self.assertEqual([len(c) for c in got["text_captions"]], [12, 2])
        self.assertEqual(got["text_timielines"][-1]["end"], 2800)


class ShapeTest(unittest.TestCase):
    def test_alt_uses_header_key_without_typo(self):
        got = alt(["甲", "乙"], [10, 20])
        self.assertEqual(got["text_timelines"],
                         [{"start": 0, "end": 10}, {"start": 10, "end": 30}])
        self.assertNotIn("text_timielines", got)

    def test_free_uses_header_typo_key(self):
        got = free(["甲"], [10])
        self.assertIn("text_timielines", got)
        self.assertNotIn("text_timelines", got)

    def test_integers_are_not_floats(self):
        got = free(["甲乙丙"], [10])
        for span in got["text_timielines"]:
            self.assertIsInstance(span["start"], int)
            self.assertIsInstance(span["end"], int)


class InvalidArgsTest(unittest.TestCase):
    def test_more_texts_than_durations_is_explicit_error(self):
        with self.assertRaises(ValueError):
            free(["甲", "乙"], [10])

    def test_negative_duration_is_rejected(self):
        with self.assertRaises(ValueError):
            free(["甲"], [-1])

    def test_non_numeric_duration_is_rejected(self):
        with self.assertRaises(ValueError):
            free(["甲"], [{"duration": 1}])

    def test_negative_text_max_is_rejected(self):
        with self.assertRaises(ValueError):
            free(["甲"], [10], text_max=-1)

    def test_texts_must_be_array(self):
        with self.assertRaises(ValueError):
            free("甲乙", [10])


if __name__ == "__main__":
    unittest.main(verbosity=2)
