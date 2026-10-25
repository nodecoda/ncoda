#!/usr/bin/env python3
"""compose_toolkit —— 剪映/速创时间轴纯函数的进程外实现（ncoda-plugin/1）。

覆盖：capcut_helper（5）+ suchuang_video_compose 的纯函数子集（13）。
不触网、不读写草稿、不碰时钟；草稿状态机/媒体探测（audio_timelines /
get_audio_duration / video_timelines）保持 unbound。

语义证据见 README；未证实的规则一律 partial + loss（清单里声明）。
"""
import json
import re
import sys

PROTOCOL = "ncoda-plugin/1"
PLUGIN_ID = "compose-toolkit"
PLUGIN_VERSION = "0.1.0"

CAPCUT_HELPER = ["audio_link_collector", "rolling_effect", "text_splitter",
                 "timeline_merge", "wenan_timeline_range"]
SUCHUANG = ["add_list", "audio_link_collector", "dialog_merge", "dialog_split",
            "dialog_wenan_timeline", "media_tile", "text_empty_removal", "text_splitter",
            "timeline_align", "timeline_generator", "timeline_merge",
            "wenan_timeline_range", "wenan_timeline_range_merge"]
LIBRARIES = [
    {"library": "capcut_helper", "functions": CAPCUT_HELPER},
    {"library": "suchuang_video_compose", "functions": SUCHUANG},
]


# ── wire 工具 ────────────────────────────────────────────────────────────

def arg(args, name, default=None):
    if name in args and args[name] is not None:
        return args[name]
    bare = name[1:] if name.startswith("$") else "$" + name
    if bare in args and args[bare] is not None:
        return args[bare]
    return default


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


def as_items(v, name="arg"):
    if v in (None, "", 0):
        return []
    if isinstance(v, list):
        return v
    raise ValueError("%s: want array, got %s" % (name, type(v).__name__))


def as_num(v, default=0.0, name="arg"):
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


def as_int(v, default=0, name="arg"):
    return int(round(as_num(v, float(default), name)))


def text_of(v):
    if v is None:
        return ""
    if isinstance(v, str):
        return v
    if isinstance(v, dict):
        for k in ("content", "text", "wenan", "output", "value", "url"):
            if k in v:
                return text_of(v[k])
        return ""
    if isinstance(v, (int, float, bool)):
        return str(v)
    return ""


def segments(v, name="timelines"):
    """把 {start,end} 列表归一成整数微秒；坏元素跳过（partial 的显式缺口）。"""
    out = []
    for item in as_items(v, name):
        if not isinstance(item, dict):
            continue
        if "start" not in item or "end" not in item:
            continue
        s = as_int(item.get("start"), 0)
        e = as_int(item.get("end"), 0)
        if e < s:
            s, e = e, s
        out.append({"start": s, "end": e})
    return out


def links_from(v):
    """把 TTS/文件结果摊平成链接串；保持顺序，跳过空值。"""
    out = []
    if v in (None, "", 0):
        return out
    if isinstance(v, list):
        for item in v:
            out.extend(links_from(item))
        return out
    if isinstance(v, dict):
        for k in ("link", "url", "output", "outputList", "data", "value"):
            if k in v:
                out.extend(links_from(v[k]))
                return out
        return out
    if isinstance(v, str):
        if v.strip():
            out.append(v.strip())
    return out


# ── 文本切分 ─────────────────────────────────────────────────────────────

SENT_RE = re.compile(r"[^。！？!?\n]+[。！？!?]?")


def split_segments(text):
    text = text.replace("\r\n", "\n").replace("\r", "\n").strip()
    if not text:
        return []
    if "\n\n" in text:
        parts = re.split(r"\n{2,}", text)
    else:
        parts = text.split("\n")
    return [p.strip() for p in parts if p.strip()]


def split_sentences(text):
    return [s.strip() for s in SENT_RE.findall(text) if s.strip()]


def fn_text_splitter(args, with_output_list=False):
    text = as_text(arg(args, "text"), "text")
    segs = []
    for par in split_segments(text):
        sents = split_sentences(par)
        segs.extend(sents if len(sents) > 1 else [par])
    out = {"error": "", "segments": segs}
    if with_output_list:
        out["outputList"] = list(segs)
    return out


def fn_capcut_text_splitter(args):
    return fn_text_splitter(args)


def fn_suchuang_text_splitter(args):
    return fn_text_splitter(args, with_output_list=True)


# ── 音频链接 ─────────────────────────────────────────────────────────────

def fn_audio_link_collector(args):
    return {"error": "", "links": links_from(arg(args, "outputList"))}


# ── 文本清洗 / 列表 ──────────────────────────────────────────────────────

def fn_text_empty_removal(args):
    rows = [text_of(x).strip() for x in as_items(arg(args, "text"), "text")]
    return {"error": "", "wenan": [x for x in rows if x]}


def fn_add_list(args):
    base = [text_of(x) for x in as_items(arg(args, "add_list"), "add_list")]
    item = as_text(arg(args, "item"), "item")
    given = arg(args, "list")
    if given not in (None, "", 0, []):
        out = [text_of(x) for x in as_items(given, "list")]
    else:
        out = list(base)
        if item:
            out.append(item)
    return {"list": out}


# ── 时间轴 ───────────────────────────────────────────────────────────────

def merge_timelines(main, pre, gap_us, skip_us):
    gap = max(0, as_int(gap_us, 0, "gap_us"))
    skip = max(0, as_int(skip_us, 0, "skip_us"))
    pre_segs = segments(pre, "pre_timeline")
    main_segs = segments(main, "main_timeline")
    pre_end = max((s["end"] for s in pre_segs), default=0)
    shift = (pre_end + gap) if pre_segs else 0
    main_shifted = [{"start": s["start"] + shift, "end": s["end"] + shift} for s in main_segs]
    merged = pre_segs + main_shifted
    if skip > 0:
        # 已知偏差：skip_us 只作为段间空隙压缩，具体规则未证实（见 loss）。
        merged = [{"start": (s["start"] if i == 0 else s["start"] - skip * i),
                   "end": s["end"] - skip * i} for i, s in enumerate(merged)]
    last_end = max((s["end"] for s in merged), default=0)
    all_tl = [] if not merged else [{"start": min(s["start"] for s in merged), "end": last_end}]
    return {
        "all_complete_timeline": list(merged),
        "all_main_timeline": list(main_shifted),
        "all_pre_timeline": list(pre_segs),
        "all_timeline": all_tl,
        "error": "",
        "last_end_us": last_end,
        "main_timelines": list(main_shifted),
        "pre_timelines": list(pre_segs),
        "timelines": list(merged),
    }


def fn_timeline_merge(args):
    return merge_timelines(arg(args, "main_timeline"), arg(args, "pre_timeline"),
                           arg(args, "gap_us"), arg(args, "skip_us"))


def fn_timeline_align(args):
    """timeline_align(align, timelines) -> {align_timelines, all_timeline}   [partial]

    证据：两处调用都传 `align="end"`，结果分别喂 `add_captions(timelines)` 与
    `media_tile(all_timeline)`（corpus/7577283712418152458:412,569,748；
    7578888749272203273:196）。

    loss（清单声明）：align 的原始规则未由语料证实；这里把 "end" 解释为
    “每段结束对齐下一段开始（末段保留原结束）”，其它值原样返回。
    """
    align = as_text(arg(args, "align"), "align")
    segs = segments(arg(args, "timelines"), "timelines")
    if not segs:
        return {"align_timelines": [], "all_timeline": []}
    out = [dict(s) for s in segs]
    if align == "end":
        for i in range(len(out) - 1):
            out[i]["end"] = out[i + 1]["start"]
            if out[i]["end"] < out[i]["start"]:
                out[i]["end"] = out[i]["start"]
    last = out[-1]["end"]
    all_tl = [{"start": 0, "end": last}] if last >= 0 else []
    return {"align_timelines": out, "all_timeline": all_tl}



def _wenan_of(x):
    if isinstance(x, dict):
        return text_of(x)
    return text_of(x)


def wenan_range(args, float_ts=False):
    timelines = segments(arg(args, "timelines"), "timelines")
    wenan = [_wenan_of(x) for x in as_items(arg(args, "wenan"), "wenan")]
    start_index = max(0, as_int(arg(args, "startIndex"), 0, "startIndex"))
    if not timelines or not wenan:
        return {"error": "", "wenanTimeline": []}
    keep = timelines[start_index:]
    if not keep:
        return {"error": "", "wenanTimeline": []}
    merge = as_int(arg(args, "mergeCount"), 0, "mergeCount")
    group = merge if merge > 0 else max(1, len(keep) // len(wenan))
    out = []
    for i, content in enumerate(wenan):
        lo = i * group
        hi = min(lo + group - 1, len(keep) - 1)
        if lo > len(keep) - 1:
            break
        start = keep[lo]["start"]
        end = keep[hi]["end"]
        out.append({"content": content,
                    "start": float(start) if float_ts else start,
                    "end": float(end) if float_ts else end})
    return {"error": "", "wenanTimeline": out}


def fn_wenan_timeline_range(args):
    return wenan_range(args, float_ts=False)


def fn_wenan_timeline_range_merge(args):
    return wenan_range(args, float_ts=True)


def fn_media_tile(args):
    all_tl = segments(arg(args, "all_timeline"), "all_timeline")
    media = as_text(arg(args, "media_url"), "media_url")
    duration = max(0, as_int(arg(args, "duration"), 0, "duration"))
    out = []
    for seg in all_tl:
        if duration <= 0:
            out.append(dict(seg))
            continue
        cursor = seg["start"]
        while cursor < seg["end"]:
            end = min(cursor + duration, seg["end"])
            out.append({"start": cursor, "end": end})
            cursor = end
    return {"timelines": out, "urls": [media] * len(out) if media else []}


def parse_duration_us(raw, unit):
    val = as_num(raw, 0.0, "duration")
    u = (unit or "").strip().lower()
    if u in ("ms", "millisecond", "milliseconds"):
        return int(round(val * 1000))
    if u in ("us", "microsecond", "microseconds"):
        return int(round(val))
    return int(round(val * 1_000_000))


def fn_timeline_generator(args):
    dur = parse_duration_us(arg(args, "duration"), as_text(arg(args, "unit"), "unit"))
    items = as_items(arg(args, "items"), "items")
    items_tl, tl = [], []
    cursor = 0
    for item in items:
        end = cursor + dur
        items_tl.append({"content": text_of(item), "start": cursor, "end": end})
        tl.append({"start": cursor, "end": end})
        cursor = end
    all_tl = [] if not tl else [{"start": 0, "end": cursor}]
    return {"all_timeline": all_tl, "items_timelines": items_tl, "timelines": tl}


def fn_rolling_effect(args):
    durations = [max(0, as_int(x, 0, "duration_list")) for x in as_items(arg(args, "duration_list"), "duration_list")]
    texts = [text_of(x) for x in as_items(arg(args, "str_list"), "str_list")]
    tl, subject = [], []
    cursor = 0
    for i, dur in enumerate(durations):
        end = cursor + dur
        tl.append({"start": cursor, "end": end})
        subject.append(texts[i % len(texts)] if texts else "")
        cursor = end
    all_tl = [] if not tl else [{"start": 0, "end": cursor}]
    return {"all_timeline": all_tl, "error": "", "subject_arr": subject, "timelines": tl}


# ── 对话 ─────────────────────────────────────────────────────────────────

ROLE_RE = re.compile(r"^\s*([^:：]{1,12})\s*[:：]\s*(.*)$")


def dialogs_from(v):
    """把 dialog_split 的输入（字符串 / 文本切分结果 / 列表）摊平成行。"""
    lines = []
    if v in (None, "", 0):
        return lines
    if isinstance(v, list):
        for item in v:
            lines.extend(dialogs_from(item))
        return lines
    if isinstance(v, dict):
        for k in ("segments", "outputList", "wenan", "dialogs", "text"):
            if k in v:
                lines.extend(dialogs_from(v[k]))
                return lines
        return lines
    if isinstance(v, str):
        for line in v.replace("\\n", "\n").splitlines():
            if line.strip():
                lines.append(line.strip())
    return lines


def fn_dialog_split(args):
    roles = [r.strip() for r in as_text(arg(args, "roles"), "roles").split("|") if r.strip()]
    lines = dialogs_from(arg(args, "dialogs"))
    groups, order = {}, []
    idx = 0
    for line in lines:
        role = roles[0] if roles else ""
        content = line
        m = ROLE_RE.match(line)
        if m and (not roles or m.group(1) in roles):
            role, content = m.group(1), m.group(2).strip()
        if role not in groups:
            groups[role] = []
            order.append(role)
        groups[role].append({"content": content, "idx": idx, "role": role})
        idx += 1
    return {"error": "", "roleDialogs": [{"role": r, "dialogs": groups[r]} for r in order]}


def role_dialogs(args):
    out = []
    for item in as_items(arg(args, "roleDialogs"), "roleDialogs"):
        if not isinstance(item, dict):
            continue
        role = as_text(item.get("role"), "role")
        rows = []
        for d in item.get("dialogs") or []:
            if isinstance(d, dict):
                rows.append({"content": text_of(d), "idx": as_int(d.get("idx"), 0), "role": role})
        out.append({"role": role, "dialogs": rows})
    return out


def fn_dialog_merge(args):
    plain = arg(args, "audioLinks") or {}
    lanes = {}
    if isinstance(plain, dict):
        for role, v in plain.items():
            lanes[as_text(role)] = links_from(v)
    flat = []
    for grp in role_dialogs(args):
        for d in grp["dialogs"]:
            flat.append((d["idx"], grp["role"]))
    flat.sort(key=lambda t: t[0])
    used, merged = {}, []
    for _, role in flat:
        lane = lanes.get(role, [])
        i = used.get(role, 0)
        if i < len(lane):
            merged.append(lane[i])
            used[role] = i + 1
    return {"error": "", "mergedAudioLinks": merged}


def fn_dialog_wenan_timeline(args):
    tls = segments(arg(args, "timelines"), "timelines")
    flat = []
    for grp in role_dialogs(args):
        for d in grp["dialogs"]:
            flat.append((d["idx"], grp["role"], d["content"]))
    flat.sort(key=lambda t: t[0])
    per_role, order = {}, []
    for pos, (_, role, content) in enumerate(flat):
        if role not in per_role:
            per_role[role] = {"role": role, "timelines": [], "wenan": []}
            order.append(role)
        per_role[role]["wenan"].append(content)
        if pos < len(tls):
            per_role[role]["timelines"].append(tls[pos])
    return {"error": "", "roleWenanTimeline": [per_role[r] for r in order]}


HANDLERS = {
    "capcut_helper.audio_link_collector": fn_audio_link_collector,
    "capcut_helper.rolling_effect": fn_rolling_effect,
    "capcut_helper.text_splitter": fn_capcut_text_splitter,
    "capcut_helper.timeline_merge": fn_timeline_merge,
    "capcut_helper.wenan_timeline_range": fn_wenan_timeline_range,
    "suchuang_video_compose.add_list": fn_add_list,
    "suchuang_video_compose.audio_link_collector": fn_audio_link_collector,
    "suchuang_video_compose.dialog_merge": fn_dialog_merge,
    "suchuang_video_compose.dialog_split": fn_dialog_split,
    "suchuang_video_compose.dialog_wenan_timeline": fn_dialog_wenan_timeline,
    "suchuang_video_compose.media_tile": fn_media_tile,
    "suchuang_video_compose.text_empty_removal": fn_text_empty_removal,
    "suchuang_video_compose.text_splitter": fn_suchuang_text_splitter,
    "suchuang_video_compose.timeline_align": fn_timeline_align,
    "suchuang_video_compose.timeline_generator": fn_timeline_generator,
    "suchuang_video_compose.timeline_merge": fn_timeline_merge,
    "suchuang_video_compose.wenan_timeline_range": fn_wenan_timeline_range,
    "suchuang_video_compose.wenan_timeline_range_merge": fn_wenan_timeline_range_merge,
}


# ── 协议 ────────────────────────────────────────────────────────────────

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
            "protocol": PROTOCOL, "id": PLUGIN_ID, "version": PLUGIN_VERSION,
            "libraries": LIBRARIES}})
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
        except Exception as exc:
            error(mid, "%s: %s" % (type(exc).__name__, exc), "runtime-error")
        return True
    if method == "ping":
        send({"jsonrpc": "2.0", "id": mid, "result": {"ok": True}})
        return True
    if method == "shutdown":
        send({"jsonrpc": "2.0", "id": mid, "result": {}})
        return False
    send({"jsonrpc": "2.0", "id": mid, "error": {"code": -32601, "message": "method %s" % method}})
    return True


def main():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except ValueError:
            print("compose_toolkit: bad json line", file=sys.stderr)
            continue
        if "id" not in msg:
            continue
        if not handle(msg):
            return


if __name__ == "__main__":
    main()
