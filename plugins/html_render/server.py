#!/usr/bin/env python3
"""html_render — 用本机 headless Chrome 把 HTML 渲染成 PNG（进程外插件）。

覆盖两个库：
  html2img.gen_img(html, width, height)   -> {image_url, message}
  htmlcode_to_img.html_img(htmlContent, viewport) -> {data:{imageUrl}, message, status, $code}

为什么是 partial（不是 real）：
  - 返回的是**本机** `file://` 路径，不是公网 URL；原服务返回 OSS 链接；
  - HTML 里引用的远程图片/字体要联网才能出现，离线时渲染成空白占位；
  - 默认 viewport 取 Chrome 的 800×600——语料 16 个调用点都只传 html，
    原服务的默认尺寸没有证据；
  - 字体族/抗锯齿与线上 Chrome 版本相关，像素级结果不保证一致。

协议：stdin/stdout 一行一条 JSON-RPC 2.0。日志一律走 stderr。
"""
import base64
import hashlib
import json
import os
import shutil
import struct
import subprocess
import sys
import tempfile

PROTOCOL = "ncoda-plugin/1"
PLUGIN_ID = "html-render"
PLUGIN_VERSION = "0.1.0"

LIBRARIES = [
    {"library": "html2img", "functions": ["gen_img"]},
    {"library": "htmlcode_to_img", "functions": ["html_img"]},
]

DEFAULT_WIDTH = 800
DEFAULT_HEIGHT = 600
CHROME = os.environ.get("NCODA_CHROME") or shutil.which("google-chrome") or "/usr/bin/google-chrome"
CHROME_TIMEOUT = 60


class RenderError(RuntimeError):
    pass


# ── 参数与产物 ────────────────────────────────────────────────────────────

def arg(args, name, default=None):
    """header 参数名 → wire 键；`$` 前缀两种写法都接受。"""
    if name in args and args[name] is not None:
        return args[name]
    bare = name[1:] if name.startswith("$") else "$" + name
    if bare in args and args[bare] is not None:
        return args[bare]
    return default


def as_int(v, default=0):
    try:
        n = int(float(v))
    except (TypeError, ValueError):
        return default
    return n


def artifact_dir():
    d = os.environ.get("NCODA_ARTIFACT_DIR") or os.path.join(tempfile.gettempdir(), "ncoda-html-render")
    os.makedirs(d, exist_ok=True)
    return d


def png_size(path):
    """读 IHDR：返回 (w, h)；不是 PNG 则抛错（不把空文件当成功）。"""
    with open(path, "rb") as fh:
        head = fh.read(33)
    if len(head) < 33 or head[:8] != b"\x89PNG\r\n\x1a\n" or head[12:16] != b"IHDR":
        raise RenderError("chrome did not produce a PNG: %s" % path)
    return struct.unpack(">II", head[16:24])


def render(html, width=0, height=0, scale=1.0):
    """渲染 HTML 到 PNG，返回 (file_path, w, h)。壳依赖 / 失败 => RenderError。"""
    if not isinstance(html, str) or not html.strip():
        raise RenderError("html is empty")
    w = as_int(width, 0) or DEFAULT_WIDTH
    h = as_int(height, 0) or DEFAULT_HEIGHT
    if w <= 0 or h <= 0:
        raise RenderError("viewport must be positive: %dx%d" % (w, h))
    if not os.path.isfile(CHROME):
        raise RenderError("headless chrome not found: %s (set NCODA_CHROME)" % CHROME)

    d = artifact_dir()
    key = hashlib.sha256(
        ("%s|%d|%d|%s|%s" % (CHROME, w, h, scale, html)).encode("utf-8")).hexdigest()[:24]
    out = os.path.join(d, "html-%s-%dx%d.png" % (key, w, h))
    if os.path.isfile(out) and os.path.getsize(out) > 0:
        png_size(out)  # 复用前也要验一遍，坏文件不能被当成缓存命中
        return out, w, h

    src = os.path.join(d, "html-%s.html" % key)
    with open(src, "w", encoding="utf-8") as fh:
        fh.write(html)
    profile = tempfile.mkdtemp(prefix="ncoda-chrome-", dir=d)
    args = [
        CHROME,
        "--headless=new",
        "--disable-gpu",
        "--hide-scrollbars",
        "--no-first-run",
        "--no-default-browser-check",
        "--disable-extensions",
        "--disable-background-networking",
        "--allow-file-access-from-files",
        "--run-all-compositor-stages-before-draw",
        "--virtual-time-budget=3000",
        "--window-size=%d,%d" % (w, h),
        "--force-device-scale-factor=%s" % max(1.0, float(scale or 1.0)),
        "--user-data-dir=%s" % profile,
        "--screenshot=%s" % out,
        "file://" + src,
    ]
    try:
        proc = subprocess.run(args, capture_output=True, text=True, timeout=CHROME_TIMEOUT)
    except subprocess.TimeoutExpired:
        raise RenderError("chrome timed out after %ss" % CHROME_TIMEOUT)
    finally:
        shutil.rmtree(profile, ignore_errors=True)
    if proc.returncode != 0 or not os.path.isfile(out):
        tail = (proc.stderr or proc.stdout or "").strip().splitlines()[-3:]
        raise RenderError("chrome failed (rc=%s): %s" % (proc.returncode, " / ".join(tail)))
    got_w, got_h = png_size(out)
    if (got_w, got_h) != (w, h):
        raise RenderError("chrome produced %dx%d, want %dx%d" % (got_w, got_h, w, h))
    return out, w, h


def file_url(path):
    return "file://" + path


# ── 库实现 ────────────────────────────────────────────────────────────────

def fn_gen_img(args):
    path, _, _ = render(arg(args, "html", ""),
                        as_int(arg(args, "width", 0)),
                        as_int(arg(args, "height", 0)))
    return {"image_url": file_url(path), "message": "ok"}


def fn_html_img(args):
    viewport = arg(args, "viewport", {}) or {}
    if not isinstance(viewport, dict):
        raise RenderError("viewport must be an object")
    scale = viewport.get("deviceScaleFactor") or viewport.get("scale") or 1.0
    try:
        scale = float(scale)
    except (TypeError, ValueError):
        scale = 1.0
    path, _, _ = render(arg(args, "htmlContent", "") or arg(args, "html", ""),
                        as_int(viewport.get("width", 0)),
                        as_int(viewport.get("height", 0)),
                        scale)
    return {"data": {"imageUrl": file_url(path)}, "message": "ok",
            "status": 0.0, "$code": 0.0}


HANDLERS = {
    "html2img.gen_img": fn_gen_img,
    "htmlcode_to_img.html_img": fn_html_img,
}


# ── JSON-RPC 2.0 传输 ─────────────────────────────────────────────────────

def send(msg):
    sys.stdout.write(json.dumps(msg, ensure_ascii=False) + "\n")
    sys.stdout.flush()


def result(mid, value):
    send({"jsonrpc": "2.0", "id": mid, "result": {"value": value}})


def error(mid, message, cls="runtime-error"):
    send({"jsonrpc": "2.0", "id": mid, "error": {
        "code": -32000, "message": message, "data": {"class": cls}}})


def handle(msg):
    mid = msg["id"]
    method = msg.get("method")
    if method == "initialize":
        send({"jsonrpc": "2.0", "id": mid, "result": {
            "protocol": PROTOCOL,
            "id": PLUGIN_ID,
            "version": PLUGIN_VERSION,
            "libraries": LIBRARIES,
        }})
        return True
    if method == "invoke":
        params = msg.get("params") or {}
        symbol = params.get("symbol")
        args = params.get("args") or {}
        handler = HANDLERS.get(symbol)
        if handler is None:
            error(mid, "unknown symbol %s" % symbol, "not-implemented")
            return True
        if not isinstance(args, dict):
            error(mid, "args must be an object", "invalid-args")
            return True
        try:
            result(mid, handler(args))
        except Exception as exc:  # 一次坏调用不带走进程
            error(mid, "%s: %s" % (type(exc).__name__, exc))
        return True
    if method == "ping":
        send({"jsonrpc": "2.0", "id": mid, "result": {"ok": True}})
        return True
    if method == "shutdown":
        send({"jsonrpc": "2.0", "id": mid, "result": {}})
        return False
    send({"jsonrpc": "2.0", "id": mid, "error": {
        "code": -32601, "message": "method %s" % method}})
    return True


def main():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except ValueError:
            print("html_render: bad json line", file=sys.stderr)
            continue
        if "id" not in msg:
            continue  # notification (cancel)
        if not handle(msg):
            return


if __name__ == "__main__":
    main()
