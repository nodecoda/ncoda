#!/usr/bin/env python3
"""capcut_data 进程外插件 —— ncoda-plugin/1 参考实现（非 stub）。

只实现能由语料调用点与消费面证实的函数；`*_infos` / `asr_timelines` /
`keyframes_infos` / `search_sticker` 依赖 CapCut 草稿 JSON 或网络，本仓没有
证据，故意保持 stub（见 README）。`audio_timelines` 只支持**本地 WAV**
（file:// 或路径）；远程 URL/MP3 需要下载与解码，显式报错而不是猜时长。

协议：stdin/stdout 一行一条 JSON-RPC 2.0。日志一律走 stderr。
"""
import base64
import json
import os
import struct
import sys
import urllib.parse
import urllib.request

PROTOCOL = "ncoda-plugin/1"
PLUGIN_ID = "capcut-data"
PLUGIN_VERSION = "0.1.0"

REAL_FUNCTIONS = [
    "get_url",
    "objs_to_str_list",
    "str_to_list",
    "array_pair",
    "extract_first_end_frame",
    "timelines",
    "wenan_links_merge",
]
PARTIAL_FUNCTIONS = ["wenan_split", "timelines_format", "audio_timelines"]
FUNCTIONS = REAL_FUNCTIONS + PARTIAL_FUNCTIONS


# ── 参数与值工具 ──────────────────────────────────────────────────────────

def arg(args, name, default=None):
    """header 参数名 → wire 键。/ `$` 前缀两种写法都接受。"""
    if name in args and args[name] not in (None, "", 0):
        return args[name]
    bare = name[1:] if name.startswith("$") else "$" + name
    if bare in args and args[bare] not in (None, "", 0):
        return args[bare]
    return default


def as_int(v, default=0):
    if v in (None, "", 0):
        return default
    if isinstance(v, bool):
        return int(v)
    if isinstance(v, (int, float)):
        return int(v)
    try:
        return int(float(str(v).strip()))
    except (TypeError, ValueError):
        return default


def as_seq(v):
    """把 header 的 `= 0` 缺省与各种容器归一成 list。"""
    if v in (None, "", 0):
        return []
    if isinstance(v, list):
        return v
    if isinstance(v, tuple):
        return list(v)
    return [v]


def url_of(v):
    """从字符串/文件对象里取 URL 字符串；取不到返回 None。"""
    if isinstance(v, str):
        return v if v != "" else None
    if isinstance(v, dict):
        for k in ("url", "output", "$output", "link", "value", "data", "src"):
            inner = v.get(k)
            if isinstance(inner, str) and inner != "":
                return inner
        return None
    if isinstance(v, (int, float)) and not isinstance(v, bool):
        # header 的 `= 0` 缺省会以 0 出现；不能变成字符串 "0"。
        return None if v == 0 else str(v)
    return None


# ── 已证实语义的实现 ──────────────────────────────────────────────────────

def fn_get_url(args):
    """get_url($output) -> {outputList:[{$output}], $output}

    证据：语料里 `yield outputList = v13_inner;` 把整个结果当成 batch 记录，
    随后 `objs_to_str_list({outputs: agg})` 再展开 `outputList[].$output`。
    """
    value = arg(args, "$output", "")
    s = value if isinstance(value, str) else (url_of(value) or "")
    return {"outputList": [{"output": s}], "output": s}


def _flatten_outputs(value, out):
    if value is None:
        return
    if isinstance(value, str):
        if value != "":
            out.append(value)
        return
    if isinstance(value, (list, tuple)):
        for item in value:
            _flatten_outputs(item, out)
        return
    if isinstance(value, dict):
        if "outputList" in value:
            _flatten_outputs(value["outputList"], out)
            return
        for k in ("$output", "output", "link", "url"):
            if k in value:
                _flatten_outputs(value[k], out)
                return
        return


def fn_objs_to_str_list(args):
    """objs_to_str_list(outputs) -> {infos:[str]}

    证据：输入是 `get_url` 结果（或 `yield outputList = <get_url>` 的包装），
    输出直接喂给 `caption_infos(texts=...)` / 视频 URL 列表。
    """
    out = []
    _flatten_outputs(arg(args, "outputs", []), out)
    return {"infos": out}


def fn_str_to_list(args):
    """str_to_list(obj) -> {infos:[str]}

    证据：`str_to_list({obj: <file|url|string>})` 的 `infos` 被当作
    `image_urls` / `imageUrls` / 音频链接数组使用 ⇒ 单个值展开为单元素数组。
    """
    out = []
    _flatten_outputs(arg(args, "obj", None), out)
    return {"infos": out}


def fn_array_pair(args):
    """array_pair(items) -> {groups:[{group:[a,b]},...], total_groups}

    证据：`for (lens, prompt in zip(v8.groups, ...))` 后
    `extract_first_end_frame({items: lens.group})` ⇒ 相邻两项成组，
    且组是 `{group: [...]}` 记录（header: ArrayPairGroupsItem）。
    """
    items = as_seq(arg(args, "items", []))
    # header: groups 的元素是 {group: [...]}（语料用 lens.group 取值）。
    groups = [{"group": items[i:i + 2]} for i in range(0, len(items), 2)]
    return {"groups": groups, "total_groups": len(groups)}


def fn_extract_first_end_frame(args):
    """extract_first_end_frame(items) -> {first_item, end_item}

    证据：`start_image_url: v10.first_item` / `end_image_url: v10.end_item`。
    """
    items = as_seq(arg(args, "items", []))
    if not items:
        return {"first_item": "", "end_item": ""}
    first = url_of(items[0]) or ""
    end = url_of(items[-1]) or ""
    return {"first_item": first, "end_item": end}


def fn_timelines(args):
    """timelines(duration, num, start, type) -> {timelines, all_timelines}

    证据：`timelines({duration:5000000, num:3})` 的 `all_timelines` 被整体
    当作一段（video_infos/audio_infos 的 timelines），`timelines` 被按数量
    逐条消费（每张图一段）。单位是微秒整数。

    切分规则：floor 等分，余数并入最后一段（保证 sum == duration）。
    """
    duration = as_int(arg(args, "duration", 0), 0)
    num = as_int(arg(args, "num", 0), 0)
    start = as_int(arg(args, "start", 0), 0)
    if duration < 0:
        duration = 0
    all_timelines = [{"start": start, "end": start + duration}]
    if num <= 0:
        return {"timelines": [], "all_timelines": all_timelines}
    base = duration // num
    segments = []
    cursor = start
    for i in range(num):
        step = base if i < num - 1 else duration - base * (num - 1)
        segments.append({"start": cursor, "end": cursor + step})
        cursor += step
    return {"timelines": segments, "all_timelines": all_timelines}


def _scalar(v):
    """links 元素 → 字符串；记录取 url/link/output 类字段。"""
    if isinstance(v, str):
        return v.strip()
    if isinstance(v, dict):
        for k in ("link", "url", "$output", "output", "value", "data", "src", "path"):
            inner = v.get(k)
            if isinstance(inner, str) and inner.strip():
                return inner.strip()
    return ""


def _read_wav_bytes(raw):
    """返回 WAV 字节；只接受本地路径 / file:// / data:audio/wav;base64。"""
    s = raw.strip()
    low = s.lower()
    if low.startswith(("http://", "https://")):
        raise ValueError("REMOTE_AUDIO_UNSUPPORTED: offline implementation does not fetch %s" % s)
    if low.startswith("data:"):
        comma = s.find(",")
        if comma < 0 or ";base64" not in low[:comma]:
            raise ValueError("bad data url (want data:audio/...;base64,)")
        return base64.b64decode(s[comma + 1:])
    if low.startswith("file://"):
        path = urllib.request.url2pathname(urllib.parse.urlparse(s).path)
    else:
        path = s
    with open(path, "rb") as fh:
        return fh.read()


def wav_duration_us(blob):
    """RIFF/WAVE chunk 解析：duration = data 字节数 / byteRate（精确）。"""
    if len(blob) < 12 or blob[:4] != b"RIFF" or blob[8:12] != b"WAVE":
        raise ValueError("not a WAV container (only local WAV is supported; MP3 needs decoding)")
    pos, byte_rate, data_size, fmt = 12, 0, None, None
    while pos + 8 <= len(blob):
        cid = blob[pos:pos + 4]
        size = struct.unpack("<I", blob[pos + 4:pos + 8])[0]
        body = blob[pos + 8:pos + 8 + size]
        if cid == b"fmt " and len(body) >= 16:
            fmt, channels, rate, byte_rate = struct.unpack("<HHII", body[:12])
        elif cid == b"data":
            data_size = len(body)
            break
        pos += 8 + size + (size & 1)
    if fmt is None or not byte_rate or data_size is None:
        raise ValueError("WAV is missing fmt/data chunks")
    if fmt not in (1, 3):
        raise ValueError("unsupported WAV format tag %d (want PCM/IEEE float)" % fmt)
    return int(round(data_size * 1000000.0 / byte_rate))


def fn_audio_timelines(args):
    """audio_timelines(links) -> {timelines, all_timelines, outputList}

    证据：语料把 `timelines` 与 mp3_urls/图片**逐条**配对（X52、X65），
    把 `all_timelines` 与**单个** BGM/图片/字幕配对（`audio_infos(mp3_urls:
    bgm[1], timelines: all_timelines)`）；同库 `timelines()` 的约定也是
    `all_timelines` = 整段 [start, start+duration]。因此这里：
      timelines     = 每个 link 一段，自 0 起首尾相接；
      all_timelines = 整段 [0, total]（无 link 时为空）。
    outputList 无语料消费点，返回 []（见 loss）。
    """
    links = as_seq(arg(args, "links", []))
    timelines = []
    cursor = 0
    for item in links:
        s = _scalar(item)
        if not s:
            raise ValueError("empty audio link")
        dur = wav_duration_us(_read_wav_bytes(s))
        if dur <= 0:
            raise ValueError("non-positive audio duration for %s" % s)
        timelines.append({"start": cursor, "end": cursor + dur})
        cursor += dur
    all_timelines = [{"start": 0, "end": cursor}] if timelines else []
    return {"timelines": timelines, "all_timelines": all_timelines, "outputList": []}


def fn_wenan_links_merge(args):
    """wenan_links_merge(link1..5) -> {links_list:[{links:[...]}]}

    证据：`for (links in v14.links_list)` 后 `suchuang_video_compose.audio_timelines(
    {links: links.links})` ⇒ 同一镜头的 5 路音频按**下标**合到一组。
    """
    lanes = [as_seq(arg(args, "link%d" % i, [])) for i in range(1, 6)]
    depth = max((len(lane) for lane in lanes), default=0)
    out = []
    for i in range(depth):
        merged = []
        for lane in lanes:
            if i < len(lane):
                s = url_of(lane[i])
                if s:
                    merged.append(s)
        if merged:
            out.append({"links": merged})
    return {"links_list": out}


def fn_wenan_split(args):
    """wenan_split(wenan) -> {wenan1..5}   [partial]

    loss（清单里声明）：原实现的 5 段分配策略未证实；这里按**连续等分**切，
    保持原序，空输入返回 5 个空数组。
    """
    items = as_seq(arg(args, "wenan", []))
    out = {}
    if not items:
        for i in range(1, 6):
            out["wenan%d" % i] = []
        return out
    size = -(-len(items) // 5)  # ceil
    for i in range(1, 6):
        out["wenan%d" % i] = items[(i - 1) * size:i * size]
    return out


def fn_timelines_format(args):
    """timelines_format(timelines) -> {all_formatted_texts, all_timelines}   [partial]

    证据：`capcut_data.timelines_format({timelines: v42.all_timelines})` 的结果
    直接喂 `add_captions({texts: v9.all_formatted_texts, timelines: v9.all_timelines})`
    （corpus/7566695004548612130:520,804）。

    loss（清单声明）：输入 timeline 的文本字段名（text/content/wenan）与原始格式化
    规则未由语料证实；这里按 text → content → wenan → value → output 取第一个非空
    字符串，start/end 归一成整数微秒，保序、不合并。
    """
    segs, texts = [], []
    for item in as_seq(arg(args, "timelines", [])):
        if not isinstance(item, dict):
            continue
        if "start" not in item or "end" not in item:
            continue
        start = as_int(item.get("start"), 0)
        end = as_int(item.get("end"), 0)
        if end < start:
            start, end = end, start
        text = ""
        for k in ("text", "content", "wenan", "value", "output"):
            v = item.get(k)
            if isinstance(v, str) and v.strip():
                text = v.strip()
                break
        segs.append({"start": start, "end": end})
        texts.append(text)
    return {"all_formatted_texts": texts, "all_timelines": segs}


HANDLERS = {
    "capcut_data.get_url": fn_get_url,
    "capcut_data.objs_to_str_list": fn_objs_to_str_list,
    "capcut_data.str_to_list": fn_str_to_list,
    "capcut_data.array_pair": fn_array_pair,
    "capcut_data.extract_first_end_frame": fn_extract_first_end_frame,
    "capcut_data.timelines": fn_timelines,
    "capcut_data.wenan_links_merge": fn_wenan_links_merge,
    "capcut_data.wenan_split": fn_wenan_split,
    "capcut_data.timelines_format": fn_timelines_format,
    "capcut_data.audio_timelines": fn_audio_timelines,
}


# ── 协议层 ────────────────────────────────────────────────────────────────

def send(msg):
    sys.stdout.write(json.dumps(msg, ensure_ascii=False) + "\n")
    sys.stdout.flush()


def result(mid, value):
    send({"jsonrpc": "2.0", "id": mid, "result": {"value": value}})


def error(mid, message, cls="invalid-args"):
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
            "libraries": [{"library": "capcut_data", "functions": FUNCTIONS}],
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
            error(mid, "args must be an object")
            return True
        try:
            result(mid, handler(args))
        except Exception as exc:  # 不让一次坏调用带走进程
            error(mid, "%s: %s" % (type(exc).__name__, exc), "runtime-error")
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
            print("capcut_data: bad json line", file=sys.stderr)
            continue
        if "id" not in msg:
            continue  # notification (cancel): 这些实现都很快，直接忽略
        if not handle(msg):
            return


if __name__ == "__main__":
    main()
