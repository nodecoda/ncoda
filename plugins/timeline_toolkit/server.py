#!/usr/bin/env python3
"""timeline_toolkit —— 时间线/字幕数据的纯函数实现（ncoda-plugin/1）。

覆盖：
  capcut_draft_data.timelines / str_to_list / caption_splitter
  capcut_data.timeline_keyframe

约束（PLUGIN-AUTHORING-SPEC §4/§5）：
  - 只依赖 args；不读文件、不联网、不碰时钟；
  - 只返回 header 声明过的键；整数不发浮点；
  - 四个函数都是 partial：形状由 header + 语料消费面固定，分配/断句规则未证实，
    loss 逐条写在 ncoda-plugin.yaml 与 README。

协议：stdin/stdout 一行一条 JSON-RPC 2.0；日志一律走 stderr。
"""
import base64
import json
import struct
import sys
import urllib.parse
import urllib.request

PROTOCOL = "ncoda-plugin/1"
PLUGIN_ID = "timeline-toolkit"
PLUGIN_VERSION = "0.1.0"

LIBRARIES = [
    {"library": "capcut_draft_data",
     "functions": ["timelines", "str_to_list", "caption_splitter",
                    "get_audio_duration", "audio_timelines", "audio_infos", "image_infos"]},
    {"library": "capcut_data",
     "functions": ["timeline_keyframe", "asr_timelines", "keyframes_infos",
                    "audio_infos", "caption_infos", "imgs_infos", "video_infos", "effect_infos"]},
    {"library": "capcut_data_fast",
     "functions": ["keyframes_infos", "audio_infos", "caption_infos", "imgs_infos",
                    "video_infos", "effect_infos"]},
    {"library": "capcut", "functions": ["video_timelines"]},
    {"library": "suchuang_video_compose", "functions": ["video_timelines"]},
]


# ── wire 工具 ─────────────────────────────────────────────────────────────

def arg(args, name, default=None):
    """header 参数名 → wire 键；`$` 前缀两种写法都接受。"""
    if name in args and args[name] is not None:
        return args[name]
    bare = name[1:] if name.startswith("$") else "$" + name
    if bare in args and args[bare] is not None:
        return args[bare]
    return default


def as_int(v, default=0, name="arg"):
    if v in (None, "", 0):
        return default
    if isinstance(v, bool):
        return int(v)
    if isinstance(v, (int, float)):
        return int(v)
    try:
        return int(float(str(v).strip()))
    except (TypeError, ValueError):
        raise ValueError("%s: want int, got %r" % (name, v))


def as_text(v, name="arg"):
    if v in (None, "", 0):
        return ""
    if isinstance(v, str):
        return v
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, (int, float)):
        return str(v)
    raise ValueError("%s: want string, got %s" % (name, type(v).__name__))


def as_float(v, default=0.0, name="arg"):
    if v in (None, "", 0):
        return default
    if isinstance(v, bool):
        return float(v)
    if isinstance(v, (int, float)):
        return float(v)
    try:
        return float(str(v).strip())
    except (TypeError, ValueError):
        raise ValueError("%s: want number, got %r" % (name, v))


def as_items(v, name="arg"):
    if v in (None, "", 0):
        return []
    if isinstance(v, list):
        return v
    raise ValueError("%s: want array, got %s" % (name, type(v).__name__))


def span(item):
    """把 {start,end} 归一成 (int, int)，允许字符串数字。"""
    if not isinstance(item, dict):
        if isinstance(item, (list, tuple)) and len(item) == 2:
            item = {"start": item[0], "end": item[1]}
        else:
            raise ValueError("timeline item: want {start,end}, got %r" % (item,))
    start = as_int(item.get("start"), name="timeline.start")
    end = as_int(item.get("end"), name="timeline.end")
    if end < start:
        start, end = end, start
    return start, end


def pipe_numbers(text, name):
    """`"0|100"` → [0.0, 100.0]；空串 → []。"""
    raw = as_text(text, name).strip()
    if not raw:
        return []
    out = []
    for part in raw.split("|"):
        part = part.strip()
        if not part:
            continue
        try:
            out.append(float(part))
        except ValueError:
            raise ValueError("%s: want number in %r" % (name, part))
    return out


# ── capcut_draft_data ────────────────────────────────────────────────────

def fn_timelines(args):
    """duration/number/start → 等分时间线；余数并入末段，保证完整覆盖。"""
    duration = max(0, as_int(arg(args, "duration"), name="duration"))
    number = as_int(arg(args, "number"), name="number")
    start = as_int(arg(args, "start"), name="start")
    n = number if number > 0 else 1
    end = start + duration
    if n == 1 or duration == 0:
        segments = [{"start": start, "end": end}]
    else:
        segments = []
        prev = start
        for i in range(1, n + 1):
            cur = end if i == n else start + duration * i // n
            segments.append({"start": prev, "end": cur})
            prev = cur
    # header: start/end 是整体区间，timelines 是分段
    return {"start": start, "end": end, "timelines": segments}


def scalar_text(v):
    if isinstance(v, dict):
        for key in ("url", "output", "value", "infos"):
            if v.get(key) not in (None, ""):
                return scalar_text(v[key])
        return ""
    return as_text(v)


def fn_str_to_list(args):
    """字符串 → 列表：JSON 数组字符串，或按行拆分；单行原样作为单元素。"""
    value = arg(args, "value")
    if isinstance(value, list):
        return {"result": [s for s in (scalar_text(v) for v in value) if s]}
    text = as_text(value, "value").strip()
    if not text:
        return {"result": []}
    if text.startswith("["):
        try:
            parsed = json.loads(text)
        except ValueError:
            parsed = None
        if isinstance(parsed, list):
            return {"result": [s for s in (scalar_text(v) for v in parsed) if s]}
    lines = [ln.strip() for ln in text.splitlines()]
    return {"result": [ln for ln in lines if ln]}


def chunk_text(text, max_chars):
    """硬切（不按标点）；max_chars<=0 表示不切。"""
    if not text:
        return []
    if max_chars <= 0 or len(text) <= max_chars:
        return [text]
    return [text[i:i + max_chars] for i in range(0, len(text), max_chars)]


def allocate(start, end, weights):
    """按字符数比例把 [start,end] 分给各 chunk；末段吸收余数。"""
    n = len(weights)
    if n == 0:
        return []
    total = sum(weights)
    if total <= 0:
        total = n
        weights = [1] * n
    out = []
    prev = start
    span_us = end - start
    acc = 0
    for i, w in enumerate(weights):
        acc += w
        cur = end if i == n - 1 else start + span_us * acc // total
        out.append({"start": prev, "end": cur})
        prev = cur
    return out


def fn_caption_splitter(args):
    max_chars = as_int(arg(args, "max_char_count"), name="max_char_count")
    raw = arg(args, "texts")
    if isinstance(raw, str):
        texts = [raw]
    elif raw in (None, "", 0):
        texts = []
    elif isinstance(raw, list):
        texts = [scalar_text(t) for t in raw]
    else:
        raise ValueError("texts: want string or array, got %s" % type(raw).__name__)
    timelines = [span(t) for t in as_items(arg(args, "timelines"), "timelines")]
    out_texts, out_spans = [], []
    for i, text in enumerate(texts):
        start, end = timelines[i] if i < len(timelines) else (0, 0)
        chunks = chunk_text(text, max_chars)
        out_texts.extend(chunks)
        out_spans.extend(allocate(start, end, [len(c) for c in chunks]))
    return {"texts": out_texts, "timelines": out_spans}


# ── capcut_data ──────────────────────────────────────────────────────────

def fn_timeline_keyframe(args):
    """presetOffsets 百分比 + presetValues → 逐段插值的 offsets/values。

    header 的返回类型是 array<string>（语料随后还会 json.stringify 一次），
    所以这里发字符串数字，不发浮点。
    """
    pcts = pipe_numbers(arg(args, "presetOffsets"), "presetOffsets")
    vals = pipe_numbers(arg(args, "presetValues"), "presetValues")
    if not pcts:
        pcts = [0.0, 100.0]
    timelines = [span(t) for t in as_items(arg(args, "timelines"), "timelines")]
    offsets, values = [], []
    for start, end in timelines:
        span_us = end - start
        for j, pct in enumerate(pcts):
            offsets.append(str(int(round(start + span_us * pct / 100.0))))
            if j < len(vals):
                values.append(str(int(round(vals[j]))))
            elif vals:
                values.append(str(int(round(vals[-1]))))
            else:
                values.append("0")
    return {"offsets": offsets, "values": values}


# ── capcut_data.asr_timelines ────────────────────────────────────────────

ASR_US_PER_SECOND = 1_000_000


def fn_asr_timelines(args):
    """ASR content_chunks（秒）→ texts + 微秒时间线。

    证据：语料 `7599126239110774822` 声明了 chunk 形状
    `{end_time: float, index: string, start_time: float, text: string}`，
    并把 `asr_timelines(...).timelines` 赋给 `array<{start:int,end:int}>`。
    """
    chunks = as_items(arg(args, "content_chunks"), "content_chunks")
    texts, timelines = [], []
    for i, ch in enumerate(chunks):
        if not isinstance(ch, dict):
            raise ValueError("content_chunks[%d]: want object, got %s" % (i, type(ch).__name__))
        if "start_time" in ch or "end_time" in ch:
            start = int(round(as_float(ch.get("start_time"), name="start_time") * ASR_US_PER_SECOND))
            end = int(round(as_float(ch.get("end_time"), name="end_time") * ASR_US_PER_SECOND))
        else:
            # 兜底：已经是微秒的 start/end（语料未见，loss 已写明）
            start = as_int(ch.get("start"), name="start")
            end = as_int(ch.get("end"), name="end")
        if end < start:
            start, end = end, start
        texts.append(as_text(ch.get("text", ""), "text"))
        timelines.append({"start": start, "end": end})
    # all_timelines 无语料证据表明与 timelines 不同 ⇒ 别名（清单写 loss）。
    return {"texts": texts, "timelines": timelines,
            "all_timelines": [dict(t) for t in timelines]}


# ── keyframes_infos ──────────────────────────────────────────────────────

def number_out(v):
    """整数不带小数点（JSON 里 1 不是 1.0）。"""
    if isinstance(v, float) and v.is_integer():
        return int(v)
    return v


def keyframes_payload(ctype, offsets, values, segments):
    """段内百分比 → 关键帧条目。

    证据：语料 7531641052663529518 / 7560352905955033130 手写同类结构
      {"offset": 段内µs, "property": ctype, "segment_id": id, "value": n}
    且 offset 以 end-start 计算（不是绝对时间）。
    """
    entries = []
    for seg in segments:
        if not isinstance(seg, dict):
            raise ValueError("segment_infos: want object, got %s" % type(seg).__name__)
        sid = as_text(seg.get("id", seg.get("segment_id", "")), "segment id")
        start = as_int(seg.get("start"), name="segment start")
        end = as_int(seg.get("end"), name="segment end")
        dur = end - start if end > start else 0
        for i, pct in enumerate(offsets):
            off = int(round(dur * pct / 100.0))
            if i < len(values):
                val = values[i]
            elif values:
                val = values[-1]
            else:
                val = 0.0
            entries.append({
                "offset": off,
                "property": ctype,
                "segment_id": sid,
                "value": number_out(val),
            })
    return json.dumps(entries, ensure_ascii=False, separators=(",", ":"))


def fn_keyframes_infos(args):
    """capcut_data.keyframes_infos：offsets 是百分比，offset 落成段内微秒。"""
    ctype = as_text(arg(args, "ctype"), "ctype")
    offsets = pipe_numbers(arg(args, "presetOffsets") or arg(args, "offsets"), "offsets")
    values = pipe_numbers(arg(args, "presetValues") or arg(args, "values"), "values")
    segments = as_items(arg(args, "segment_infos"), "segment_infos")
    payload = keyframes_payload(ctype, offsets, values, segments)
    body = {"errorCode": "", "errorMessage": ""}
    return {
        "errorBody": body,
        "isSuccess": True,
        "keyframes_infos": payload,
        "outputList": [{"keyframes_infos": payload}],
    }


def fn_keyframes_infos_fast(args):
    """capcut_data_fast.keyframes_infos：同构，额外带 error 字段；pattern 忽略。"""
    got = fn_keyframes_infos(args)
    got["error"] = ""
    got["outputList"] = [{"error": "", "keyframes_infos": got["keyframes_infos"]}]
    return got


# ── *_infos（草稿片段 JSON）─────────────────────────────────────────────
#
# schema 证据：语料 JS/Python 手写的同类结构，喂给与 *_infos 完全相同的消费参数：
#   7598545768181841960: texts {text,start,end,in_animation,out_animation}
#                        audios {audio_url,duration,volume,start,end}
#                        images {image_url,width,height,start,end,in/out/loop_animation,…}
#                        effect_infos {effect_title,start,end}
#   7551635971227664399: videos {video_url,duration,start,end,width,height,transition,transition_duration}
#   7531641052663529518: segment_infos {id,start,end}（add_captions 的输出）

def text_of(v):
    if isinstance(v, dict):
        for k in ("text", "content", "wenan", "value", "caption"):
            if v.get(k) not in (None, ""):
                return as_text(v[k], k)
        return ""
    return as_text(v)


def norm_list(v, coerce):
    if v in (None, "", 0):
        return []
    if isinstance(v, list):
        return [coerce(x) for x in v]
    return [coerce(v)]


def pair_spans(items, timelines, broadcast=True):
    """按索引配对；单元素 + 多时间段时铺满整体（bgm/整段素材的常见语义）。"""
    ts = [span(t) for t in as_items(timelines, "timelines")]
    if not items:
        return []
    if broadcast and len(items) == 1 and len(ts) != 1:
        if not ts:
            return []
        ts = [(min(s for s, _ in ts), max(e for _, e in ts))]
    out = []
    for i, it in enumerate(items):
        if i >= len(ts):
            break
        out.append((it, ts[i][0], ts[i][1]))
    return out


def opt_field(ent, args, name, kind="text"):
    """只在调用方真的传了该参数时写入；避免把 header 的 0/"" 默认写成限制值。"""
    raw = arg(args, name)
    if raw is None:
        return
    if kind == "text":
        val = as_text(raw, name)
        if val:
            ent[name] = val
    elif kind == "int":
        val = as_int(raw, name=name)
        if val:
            ent[name] = val
    elif kind == "float":
        val = as_float(raw, name=name)
        if val:
            ent[name] = number_out(val)


ANIM_FIELDS = ("in_animation", "out_animation", "loop_animation")
ANIM_DURATIONS = ("in_animation_duration", "out_animation_duration", "loop_animation_duration")


def build_audio(args):
    urls = norm_list(arg(args, "mp3_urls") if arg(args, "mp3_urls") is not None
                     else arg(args, "urls"), scalar_text)
    entries = []
    for url, s, e in pair_spans(urls, arg(args, "timelines")):
        ent = {"audio_url": url, "duration": e - s, "start": s, "end": e}
        opt_field(ent, args, "volume", "float")
        opt_field(ent, args, "audio_effect")
        entries.append(ent)
    return entries


def build_caption(args):
    texts = norm_list(arg(args, "texts"), text_of)
    entries = []
    for text, s, e in pair_spans(texts, arg(args, "timelines")):
        ent = {"text": text, "start": s, "end": e}
        for f in ANIM_FIELDS:
            opt_field(ent, args, f)
        for f in ANIM_DURATIONS:
            opt_field(ent, args, f, "int")
        entries.append(ent)
    return entries


def build_images(args):
    urls = norm_list(arg(args, "imgs"), scalar_text)
    entries = []
    for url, s, e in pair_spans(urls, arg(args, "timelines")):
        ent = {"image_url": url, "start": s, "end": e}
        opt_field(ent, args, "width", "int")
        opt_field(ent, args, "height", "int")
        for f in ANIM_FIELDS:
            opt_field(ent, args, f)
        for f in ANIM_DURATIONS:
            opt_field(ent, args, f, "int")
        opt_field(ent, args, "transition")
        opt_field(ent, args, "transition_duration", "int")
        entries.append(ent)
    return entries


def build_videos(args):
    urls = norm_list(arg(args, "video_urls"), scalar_text)
    entries = []
    for url, s, e in pair_spans(urls, arg(args, "timelines")):
        ent = {"video_url": url, "duration": e - s, "start": s, "end": e}
        opt_field(ent, args, "width", "int")
        opt_field(ent, args, "height", "int")
        opt_field(ent, args, "transition")
        opt_field(ent, args, "transition_duration", "int")
        opt_field(ent, args, "volume", "float")
        opt_field(ent, args, "mask")
        entries.append(ent)
    return entries


def build_effects(args):
    names = norm_list(arg(args, "effects"), text_of)
    entries = []
    for name, s, e in pair_spans(names, arg(args, "timelines")):
        entries.append({"effect_title": name, "start": s, "end": e})
    return entries


def infos_result(entries, with_error):
    payload = json.dumps(entries, ensure_ascii=False, separators=(",", ":"))
    out = {"infos": payload}
    if with_error:
        out["error"] = ""
    return out


def fn_audio_infos(args):
    return infos_result(build_audio(args), False)


def fn_caption_infos(args):
    return infos_result(build_caption(args), False)


def fn_imgs_infos(args):
    return infos_result(build_images(args), False)


def fn_video_infos(args):
    return infos_result(build_videos(args), False)


def fn_effect_infos(args):
    return infos_result(build_effects(args), False)


def fn_audio_infos_fast(args):
    return infos_result(build_audio(args), True)


def fn_caption_infos_fast(args):
    return infos_result(build_caption(args), True)


def fn_imgs_infos_fast(args):
    return infos_result(build_images(args), True)


def fn_video_infos_fast(args):
    return infos_result(build_videos(args), False)


def fn_effect_infos_fast(args):
    return infos_result(build_effects(args), False)


# ── capcut_draft_data.*_infos（另一族：键是 url）────────────────────────
#
# 证据：同一 infos 参数在语料里被 JS 手写结构喂给
# `capcut_draft_folder.add_audios/add_images`：
#   7571389179980955699 audioData {url,duration,start,end}
#                      bgImageData/iconImageData {url,start,end,transform_x,
#                      transform_y,scale_x,scale_y,in/out_animation(+duration)}
#   7568679628961955850 audioData {url,duration,start,end,volume}
#                      contentImageData {url,start,end}
# 与 capcut_data 族不同：消费者不同（draft_folder），键是 url 而非 *_url。

def build_draft_audio(args):
    urls = norm_list(arg(args, "urls"), scalar_text)
    entries = []
    for url, s, e in pair_spans(urls, arg(args, "timelines")):
        ent = {"url": url, "duration": e - s, "start": s, "end": e}
        opt_field(ent, args, "volume", "float")
        opt_field(ent, args, "audio_effect")
        entries.append(ent)
    return entries


def build_draft_images(args):
    urls = norm_list(arg(args, "urls"), scalar_text)
    entries = []
    for url, s, e in pair_spans(urls, arg(args, "timelines")):
        ent = {"url": url, "start": s, "end": e}
        opt_field(ent, args, "transform_x", "int")
        opt_field(ent, args, "transform_y", "int")
        opt_field(ent, args, "scale_x", "float")
        opt_field(ent, args, "scale_y", "float")
        opt_field(ent, args, "in_animation")
        opt_field(ent, args, "out_animation")
        opt_field(ent, args, "in_animation_duration", "int")
        opt_field(ent, args, "out_animation_duration", "int")
        entries.append(ent)
    return entries


def fn_draft_audio_infos(args):
    return infos_result(build_draft_audio(args), False)


def fn_draft_image_infos(args):
    return infos_result(build_draft_images(args), False)


# ── capcut_draft_data.audio_timelines（本地 WAV）─────────────────────────
#
# 证据（7537506233444663359:96,101,105）：`timelines` 与 urls 1:1 配对
# （audio_infos / image_infos 同形），`end` 被当作整段时长
# （`timelines({duration: v13.end, number: 1})`）。与 capcut_data 族的差别：
# 顶层有 start/end，另有一个无语料消费点的 total_timelines。
# WAV 解析与 plugins/capcut_data/server.py 同构（各自独立进程，保持插件边界）。

def _audio_scalar(v):
    if isinstance(v, str):
        return v.strip()
    if isinstance(v, dict):
        for k in ("link", "url", "$output", "output", "value", "data", "src", "path"):
            inner = v.get(k)
            if isinstance(inner, str) and inner.strip():
                return inner.strip()
    return ""


class RemoteUnsupported(ValueError):
    """离线实现的远程取数拒绝；由调用方补上 AUDIO/VIDEO 前缀。"""


def _fetch_remote(url):
    """远程取数的默认策略：离线显式失败。

    这是唯一的网络入口，测试用 monkeypatch 替换成合成媒体字节（mock），
    从而在不触网、不依赖 ffmpeg 的前提下验证「URL → 字节 → 微秒」这一段。
    """
    raise RemoteUnsupported("offline implementation does not fetch %s" % url)


def _media_bytes(raw, prefix="REMOTE_AUDIO_UNSUPPORTED"):
    s = raw.strip()
    low = s.lower()
    if low.startswith(("http://", "https://")):
        try:
            return _fetch_remote(s)
        except RemoteUnsupported as exc:
            raise ValueError("%s: %s" % (prefix, exc))
    if low.startswith("data:"):
        comma = s.find(",")
        if comma < 0 or ";base64" not in low[:comma]:
            raise ValueError("bad data url (want data:<type>;base64,)")
        return base64.b64decode(s[comma + 1:])
    if low.startswith("file://"):
        path = urllib.request.url2pathname(urllib.parse.urlparse(s).path)
    else:
        path = s
    with open(path, "rb") as fh:
        return fh.read()


def _audio_bytes(raw):
    return _media_bytes(raw, "REMOTE_AUDIO_UNSUPPORTED")


def wav_duration_us(blob):
    if len(blob) < 12 or blob[:4] != b"RIFF" or blob[8:12] != b"WAVE":
        raise ValueError("not a WAV container (only local WAV is supported; MP3 needs decoding)")
    pos, byte_rate, data_size, fmt = 12, 0, None, None
    while pos + 8 <= len(blob):
        cid = blob[pos:pos + 4]
        size = struct.unpack("<I", blob[pos + 4:pos + 8])[0]
        body = blob[pos + 8:pos + 8 + size]
        if cid == b"fmt " and len(body) >= 16:
            fmt, _channels, _rate, byte_rate = struct.unpack("<HHII", body[:12])
        elif cid == b"data":
            data_size = len(body)
            break
        pos += 8 + size + (size & 1)
    if fmt is None or not byte_rate or data_size is None:
        raise ValueError("WAV is missing fmt/data chunks")
    if fmt not in (1, 3):
        raise ValueError("unsupported WAV format tag %d (want PCM/IEEE float)" % fmt)
    return int(round(data_size * 1000000.0 / byte_rate))


def fn_get_audio_duration(args):
    """capcut_draft_data.get_audio_duration：url → {duration}（微秒整数）。

    证据：7539744063333105727:96,107 把结果直接 yield 成 `array<int>`，并在手写
    JS 里与 100000（100ms）相加/比较 ⇒ 单位是微秒；7566078917871140864:62,79-97
    同样把 duration 当微秒用于 start+end。WAV 用 data 字节数/byteRate 精确换算。
    """
    url = _audio_scalar(arg(args, "url", ""))
    if not url:
        raise ValueError("empty audio url")
    dur = wav_duration_us(_audio_bytes(url))
    if dur <= 0:
        raise ValueError("non-positive audio duration for %s" % url)
    return {"duration": dur}


def fn_audio_timelines(args):
    urls = arg(args, "urls", []) or []
    if not isinstance(urls, list):
        urls = [urls]
    timelines, cursor = [], 0
    for item in urls:
        s = _audio_scalar(item)
        if not s:
            raise ValueError("empty audio url")
        dur = wav_duration_us(_audio_bytes(s))
        if dur <= 0:
            raise ValueError("non-positive audio duration for %s" % s)
        timelines.append({"start": cursor, "end": cursor + dur})
        cursor += dur
    total = [{"start": 0, "end": cursor}] if timelines else []
    return {"start": 0, "end": cursor, "timelines": timelines,
            "total_timelines": total}


# ── 媒体容器时长（video_timelines）──────────────────────────────────────
#
# capcut.video_timelines / suchuang_video_compose.video_timelines 只收 links，
# 返回逐链接相接的 timelines 与「整体单区间」all_timelines。
# 证据：7531684846948007951:50 用 `all_timelines[0].end + all_timelines_1[0].end`
# 求两组视频的总时长；7624531644146729003:242 用 all_timelines[0] 求单个视频
# 时长；7577283712418152458:331 的手写 JS 也把 all_timelines 压成一个区间。
# 时长探测不调用 ffprobe：容器头（MP4 mvhd / WebM EBML / WAV data）足够。

def mp4_duration_us(blob):
    """MP4/MOV：moov/mvhd；duration=0 时退回 mvex/mehd 或最长 trak/mdhd。"""

    def children(start, end):
        pos = start
        while pos + 8 <= end:
            size = struct.unpack(">I", blob[pos:pos + 4])[0]
            typ = blob[pos + 4:pos + 8]
            header = 8
            if size == 1:
                if pos + 16 > end:
                    raise ValueError("truncated MP4 box header")
                size = struct.unpack(">Q", blob[pos + 8:pos + 16])[0]
                header = 16
            elif size == 0:
                size = end - pos
            if size < header or pos + size > end:
                raise ValueError("truncated MP4 box %r" % typ)
            yield typ, pos + header, pos + size
            pos += size

    def time_header(body, end):
        """mvhd/mdhd/mehd 的 (version, timescale, duration, duration_offset)。"""
        if body + 4 > end:
            raise ValueError("truncated MP4 time header")
        version = blob[body]
        if version == 1:
            if body + 32 > end:
                raise ValueError("truncated MP4 v1 time header")
            timescale = struct.unpack(">I", blob[body + 20:body + 24])[0]
            duration = struct.unpack(">Q", blob[body + 24:body + 32])[0]
        elif version == 0:
            if body + 20 > end:
                raise ValueError("truncated MP4 v0 time header")
            timescale = struct.unpack(">I", blob[body + 12:body + 16])[0]
            duration = struct.unpack(">I", blob[body + 16:body + 20])[0]
        else:
            raise ValueError("unsupported MP4 time header version %d" % version)
        return timescale, duration

    mvhd = mehd = None
    track_max = 0
    for typ, start, end in children(0, len(blob)):
        if typ != b"moov":
            continue
        for typ2, s2, e2 in children(start, end):
            if typ2 == b"mvhd":
                mvhd = time_header(s2, e2)
            elif typ2 == b"mvex":
                for typ3, s3, e3 in children(s2, e2):
                    if typ3 == b"mehd":
                        mehd = time_header(s3, e3)
            elif typ2 == b"trak":
                for typ3, s3, e3 in children(s2, e2):
                    if typ3 != b"mdia":
                        continue
                    for typ4, s4, e4 in children(s3, e3):
                        if typ4 == b"mdhd":
                            ts, dur = time_header(s4, e4)
                            if ts:
                                track_max = max(track_max, dur * 1000000 // ts)
    if mvhd and mvhd[0] and mvhd[1]:
        return int(round(mvhd[1] * 1000000.0 / mvhd[0]))
    if mehd and mehd[0] and mehd[1]:
        return int(round(mehd[1] * 1000000.0 / mehd[0]))
    if track_max:
        return track_max
    raise ValueError("MP4 has no usable mvhd/mehd/mdhd duration")


def _ebml_id(blob, pos):
    first = blob[pos]
    length, mask = 1, 0x80
    while length <= 4 and not first & mask:
        mask >>= 1
        length += 1
    if not first & mask or pos + length > len(blob):
        raise ValueError("bad EBML element id")
    return blob[pos:pos + length], pos + length


def _ebml_size(blob, pos):
    first = blob[pos]
    length, mask = 1, 0x80
    while length <= 8 and not first & mask:
        mask >>= 1
        length += 1
    if length > 8 or pos + length > len(blob):
        raise ValueError("bad EBML element size")
    value = first & (mask - 1)
    for i in range(1, length):
        value = (value << 8) | blob[pos + i]
    unknown = value == mask - 1
    return None if unknown else value, pos + length


def webm_duration_us(blob):
    """WebM/Matroska：Segment/Info 的 TimecodeScale × Duration。"""
    scale, duration = 1000000, None

    def walk(pos, end):
        nonlocal scale, duration
        while pos < end:
            eid, pos = _ebml_id(blob, pos)
            size, body = _ebml_size(blob, pos)
            body_end = end if size is None else body + size
            if body_end > end:
                raise ValueError("truncated EBML element %r" % eid)
            if eid in (b"\x18\x53\x80\x67", b"\x15\x49\xa9\x66"):
                walk(body, body_end)
            elif eid == b"\x2a\xd7\xb1":
                scale = int.from_bytes(blob[body:body_end], "big")
            elif eid == b"\x44\x89":
                n = body_end - body
                if n == 4:
                    duration = struct.unpack(">f", blob[body:body_end])[0]
                elif n == 8:
                    duration = struct.unpack(">d", blob[body:body_end])[0]
                else:
                    raise ValueError("bad WebM Duration width %d" % n)
            pos = body_end

    walk(0, len(blob))
    if duration is None:
        raise ValueError("WebM has no Duration (unfinalized/live stream?)")
    return int(round(duration * scale / 1000.0))


def media_duration_us(blob):
    if len(blob) >= 12 and blob[:4] == b"RIFF" and blob[8:12] == b"WAVE":
        return wav_duration_us(blob)
    if len(blob) >= 12 and blob[4:8] in (b"ftyp", b"moov", b"styp"):
        return mp4_duration_us(blob)
    if blob[:4] == b"\x1a\x45\xdf\xa3":
        return webm_duration_us(blob)
    raise ValueError("unsupported media container (want WAV/MP4/WebM)")


def _video_links(args):
    links = []
    for item in as_items(arg(args, "links"), "links"):
        link = _audio_scalar(item)
        if not link:
            raise ValueError("empty video link")
        links.append(link)
    return links


def _build_video_timelines(links):
    timelines, cursor = [], 0
    for link in links:
        dur = media_duration_us(_media_bytes(link, "REMOTE_VIDEO_UNSUPPORTED"))
        if dur <= 0:
            raise ValueError("non-positive video duration for %s" % link)
        timelines.append({"start": cursor, "end": cursor + dur})
        cursor += dur
    all_timelines = [{"start": 0, "end": cursor}] if timelines else []
    return timelines, all_timelines


def fn_video_timelines(args):
    """capcut.video_timelines：逐链接 timelines + 整体 all_timelines。"""
    timelines, all_timelines = _build_video_timelines(_video_links(args))
    return {"timelines": timelines, "all_timelines": all_timelines}


def fn_suchuang_video_timelines(args):
    """suchuang_video_compose.video_timelines：同上，并回显 links。"""
    links = _video_links(args)
    timelines, all_timelines = _build_video_timelines(links)
    return {"links": links, "timelines": timelines, "all_timelines": all_timelines}


HANDLERS = {
    "capcut.video_timelines": fn_video_timelines,
    "suchuang_video_compose.video_timelines": fn_suchuang_video_timelines,
    "capcut_draft_data.timelines": fn_timelines,
    "capcut_draft_data.str_to_list": fn_str_to_list,
    "capcut_draft_data.caption_splitter": fn_caption_splitter,
    "capcut_draft_data.audio_infos": fn_draft_audio_infos,
    "capcut_draft_data.image_infos": fn_draft_image_infos,
    "capcut_draft_data.get_audio_duration": fn_get_audio_duration,
    "capcut_draft_data.audio_timelines": fn_audio_timelines,
    "capcut_data.timeline_keyframe": fn_timeline_keyframe,
    "capcut_data.asr_timelines": fn_asr_timelines,
    "capcut_data.keyframes_infos": fn_keyframes_infos,
    "capcut_data_fast.keyframes_infos": fn_keyframes_infos_fast,
    "capcut_data.audio_infos": fn_audio_infos,
    "capcut_data.caption_infos": fn_caption_infos,
    "capcut_data.imgs_infos": fn_imgs_infos,
    "capcut_data.video_infos": fn_video_infos,
    "capcut_data.effect_infos": fn_effect_infos,
    "capcut_data_fast.audio_infos": fn_audio_infos_fast,
    "capcut_data_fast.caption_infos": fn_caption_infos_fast,
    "capcut_data_fast.imgs_infos": fn_imgs_infos_fast,
    "capcut_data_fast.video_infos": fn_video_infos_fast,
    "capcut_data_fast.effect_infos": fn_effect_infos_fast,
}


# ── 协议 ─────────────────────────────────────────────────────────────────

def send(obj):
    sys.stdout.write(json.dumps(obj, ensure_ascii=False, separators=(",", ":")) + "\n")
    sys.stdout.flush()


def result(mid, value):
    send({"jsonrpc": "2.0", "id": mid, "result": {"value": value}})


def error(mid, message, code="invalid-args"):
    send({"jsonrpc": "2.0", "id": mid,
          "error": {"code": -32000, "message": message, "data": {"code": code}}})


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
        except ValueError as exc:
            error(mid, str(exc), "invalid-args")
        except Exception as exc:  # 一次坏调用不能带走进程
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
            print("timeline_toolkit: bad json line", file=sys.stderr)
            continue
        if "id" not in msg:
            continue  # notification (cancel)：实现都很快，直接忽略
        if not handle(msg):
            return


if __name__ == "__main__":
    main()
