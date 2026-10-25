#!/usr/bin/env python3
"""html_render 的判据（正控 + 负控）。

run: python3 plugins/html_render/test_server.py

要求本机有可用的 headless Chrome（没有则跳过渲染类判据，但协议/负控仍跑）。
"""
import json
import os
import struct
import subprocess
import sys
import unittest
import zlib

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import server  # noqa: E402

try:
    import yaml
except Exception:  # pragma: no cover
    yaml = None


def png_pixel(path, x, y):
    """解码 8bit RGB/RGBA 非隔行 PNG 的一个像素（不依赖 PIL）。"""
    with open(path, "rb") as fh:
        data = fh.read()
    assert data[:8] == b"\x89PNG\r\n\x1a\n", "not a PNG"
    pos, idat, w = 8, b"", 0
    while pos < len(data):
        ln = struct.unpack(">I", data[pos:pos + 4])[0]
        kind = data[pos + 4:pos + 8]
        body = data[pos + 8:pos + 8 + ln]
        if kind == b"IHDR":
            w, h, bit, color, _, _, interlace = struct.unpack(">IIBBBBB", body)
            assert bit == 8 and color in (2, 6) and interlace == 0, (bit, color, interlace)
            bpp = 3 if color == 2 else 4
        elif kind == b"IDAT":
            idat += body
        pos += 12 + ln
    raw = zlib.decompress(idat)
    stride = w * bpp
    prev = bytearray(stride)
    rows, off = [], 0
    for _ in range(h):
        f = raw[off]
        line = bytearray(raw[off + 1:off + 1 + stride])
        off += 1 + stride
        for i in range(stride):
            a = line[i - bpp] if i >= bpp else 0
            b = prev[i]
            c = prev[i - bpp] if i >= bpp else 0
            if f == 1:
                line[i] = (line[i] + a) & 0xFF
            elif f == 2:
                line[i] = (line[i] + b) & 0xFF
            elif f == 3:
                line[i] = (line[i] + (a + b) // 2) & 0xFF
            elif f == 4:
                p = a + b - c
                pa, pb, pc = abs(p - a), abs(p - b), abs(p - c)
                pr = a if pa <= pb and pa <= pc else (b if pb <= pc else c)
                line[i] = (line[i] + pr) & 0xFF
        rows.append(bytes(line))
        prev = line
    px = rows[y][x * bpp:(x + 1) * bpp]
    return tuple(px[:3])


RED = '<!doctype html><html><body style="margin:0;background:#ff0000"></body></html>'


class RenderTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not os.path.isfile(server.CHROME):
            raise unittest.SkipTest("headless chrome not available")

    def test_renders_requested_viewport_and_pixels(self):
        path, w, h = server.render(RED, 64, 48)
        self.assertEqual((w, h), (64, 48))
        self.assertEqual(server.png_size(path), (64, 48))
        self.assertEqual(png_pixel(path, 5, 5), (255, 0, 0))

    def test_default_viewport_is_documented_800x600(self):
        # 语料 16 个调用点都只传 html；默认尺寸是本实现的显式契约（见 loss）
        path, w, h = server.render(RED)
        self.assertEqual((w, h), (server.DEFAULT_WIDTH, server.DEFAULT_HEIGHT))
        self.assertEqual(server.png_size(path), (800, 600))

    def test_same_input_reuses_the_same_artifact(self):
        a, _, _ = server.render(RED, 32, 32)
        b, _, _ = server.render(RED, 32, 32)
        self.assertEqual(a, b)

    def test_empty_html_is_an_error_not_a_blank_success(self):
        with self.assertRaises(server.RenderError):
            server.render("", 32, 32)

    def test_missing_chrome_is_an_explicit_error(self):
        old = server.CHROME
        server.CHROME = "/nonexistent/google-chrome"
        try:
            with self.assertRaises(server.RenderError) as ctx:
                server.render(RED, 32, 32)
            self.assertIn("not found", str(ctx.exception))
        finally:
            server.CHROME = old

    def test_negative_control_non_png_output_is_rejected(self):
        # 负控：Chrome「成功退出」但没写出 PNG 时，不得当作渲染成功。
        with self.assertRaises(server.RenderError):
            server.png_size(os.path.join(HERE, "server.py"))


class LibraryShapeTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not os.path.isfile(server.CHROME):
            raise unittest.SkipTest("headless chrome not available")

    def test_gen_img_returns_file_url_and_message(self):
        out = server.fn_gen_img({"html": RED, "width": 40, "height": 30})
        self.assertEqual(out["message"], "ok")
        self.assertTrue(out["image_url"].startswith("file://"), out["image_url"])
        self.assertEqual(server.png_size(out["image_url"][len("file://"):]), (40, 30))

    def test_html_img_honours_viewport_map(self):
        out = server.fn_html_img({"htmlContent": RED,
                                  "viewport": {"width": 24, "height": 18}})
        self.assertEqual(out["status"], 0.0)
        self.assertEqual(out["$code"], 0.0)
        url = out["data"]["imageUrl"]
        self.assertEqual(server.png_size(url[len("file://"):]), (24, 18))

    def test_html_img_rejects_non_object_viewport(self):
        with self.assertRaises(server.RenderError):
            server.fn_html_img({"htmlContent": RED, "viewport": "big"})


class ProtocolTest(unittest.TestCase):
    def test_handshake_matches_manifest(self):
        if yaml is None:
            self.skipTest("PyYAML unavailable")
        doc = yaml.safe_load(open(os.path.join(HERE, "ncoda-plugin.yaml"), encoding="utf-8"))
        want = {lib["library"]: sorted(lib["functions"]) for lib in doc["libraries"]}
        proc = subprocess.run(
            [sys.executable, os.path.join(HERE, "server.py")],
            input=json.dumps({"jsonrpc": "2.0", "id": 1, "method": "initialize",
                              "params": {}}) + "\n",
            capture_output=True, text=True, timeout=10, check=True)
        res = json.loads(proc.stdout.splitlines()[0])["result"]
        got = {lib["library"]: sorted(lib["functions"]) for lib in res["libraries"]}
        self.assertEqual(got, want)
        self.assertEqual(res["id"], "html-render")


if __name__ == "__main__":
    unittest.main(verbosity=2)
