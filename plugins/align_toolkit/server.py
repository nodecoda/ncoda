#!/usr/bin/env python3
"""align_toolkit —— 按「音频时长列表」对齐字幕的纯函数实现（ncoda-plugin/1）。

覆盖：
  audio_align_free.align_text_to_audio
  audio_subtitle_align_alt.align_text_to_audio

两者都只收 duration_list + texts（无音频 URL、无凭证），因此可以在本地按语料
手写等价物重放：语料里两个独立工作流各带一份同算法 JS——`splitLongPhrase` /
`processSubtitles`（MAX_LINE_LENGTH=25）。见 README「证据」。

约束（PLUGIN-AUTHORING-SPEC §4/§5）：
  - 只依赖 args；不读文件、不联网、不碰时钟；
  - 只返回 header 声明过的键；整数不发浮点；
  - partial：切分/清洗/比例分摊规则取自语料手写等价物；平台节点在
    text_max 缺省时的行为未独立取证（这里按 25）。
"""
import json
import math
import sys

PROTOCOL = "ncoda-plugin/1"
PLUGIN_ID = "align-toolkit"
PLUGIN_VERSION = "0.1.0"

LIBRARIES = [
    {"library": "audio_align_free", "functions": ["align_text_to_audio"]},
    {"library": "audio_subtitle_align_alt", "functions": ["align_text_to_audio"]},
]

# 语料手写等价物 SUB_CONFIG.MAX_LINE_LENGTH。text_max<=0 时用它。
DEFAULT_MAX_LINE = 25
# 语料 SUB_CONFIG.SPLIT_PRIORITY，顺序即优先级。
SPLIT_PRIORITY = ["。", "！", "？", "，", ",", "：", ":", "、", "；", ";", " "]
# 语料 cleanRegex 的字符类：\u3000、\u3002-\u303F、\uFF00-\uFFEF、
# \u2000-\u206F，以及下列 ASCII 标点（不含 , : ; 与空格）。
CLEAN_RANGES = ((0x3000, 0x3000), (0x3002, 0x303F), (0xFF00, 0xFFEF), (0x2000, 0x206F))
CLEAN_ASCII = set("!\"#$%&'()*+-./<=>?@\\^_`{|}~")
# JS \p{Unified_Ideograph} 的 BMP 区段（代理对不在单 code unit 上命中）。
IDEOGRAPH_RANGES = ((0x3400, 0x4DBF), (0x4E00, 0x9FFF))
IDEOGRAPH_EXTRA = {0xFA0E, 0xFA0F, 0xFA11, 0xFA13, 0xFA14,
                   0xFA1F, 0xFA21, 0xFA23, 0xFA24, 0xFA27, 0xFA28, 0xFA29}


# ── wire 工具 ────────────────────────────────────────────────────────────

def arg(args, name, default=None):
    """header 参数名 → wire 键；`$` 前缀两种写法都接受（P1）。"""
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
        raise ValueError("%s: want int, got bool" % name)
    if isinstance(v, int):
        return v
    if isinstance(v, float):
        if not math.isfinite(v) or v != int(v):
            raise ValueError("%s: want int, got %r" % (name, v))
        return int(v)
    try:
        return int(float(str(v).strip()))
    except (TypeError, ValueError):
        raise ValueError("%s: want int, got %r" % (name, v))


def as_text_list(v, name="texts"):
    if not isinstance(v, list):
        raise ValueError("%s: want array, got %s" % (name, type(v).__name__))
    out = []
    for item in v:
        if isinstance(item, str):
            out.append(item)
        elif isinstance(item, bool) or isinstance(item, (int, float)):
            out.append(str(item))
        else:
            raise ValueError("%s: array item must be string, got %s"
                             % (name, type(item).__name__))
    return out


def as_duration_list(v, name="duration_list"):
    if not isinstance(v, list):
        raise ValueError("%s: want array, got %s" % (name, type(v).__name__))
    out = []
    for item in v:
        if isinstance(item, bool):
            raise ValueError("%s: duration must be a number, got bool" % name)
        if isinstance(item, (int, float)):
            value = float(item)
        elif isinstance(item, str):
            try:
                value = float(item.strip())
            except ValueError:
                raise ValueError("%s: duration must be a number, got %r" % (name, item))
        else:
            raise ValueError("%s: duration must be a number, got %s"
                             % (name, type(item).__name__))
        if not math.isfinite(value):
            raise ValueError("%s: duration must be finite, got %r" % (name, item))
        if value < 0:
            raise ValueError("%s: duration must be >= 0, got %r" % (name, item))
        out.append(value)
    return out


# ── JS 字符串语义（UTF-16 code unit）──────────────────────────────────────
#
# 语料 JS 用的是 String.length / substring / lastIndexOf，单位是 UTF-16 码元。
# 这里用码元计长度与定位，但只在完整字符边界切分：非 BMP 字符（emoji）在 JS
# 里可能被从代理对中间切开，本实现宁可整字保留（见 README loss）。

def u16len(s):
    return len(s.encode("utf-16-le", "surrogatepass")) // 2


def u16_prefix(cps):
    """前缀码元长度表：pre[i] = u16len(cps[:i])。"""
    pre = [0]
    total = 0
    for ch in cps:
        total += 2 if ord(ch) > 0xFFFF else 1
        pre.append(total)
    return pre


def is_ideograph(cp):
    if cp in IDEOGRAPH_EXTRA:
        return True
    return any(lo <= cp <= hi for lo, hi in IDEOGRAPH_RANGES)


def clean_caption(text):
    """语料 cleanRegex：删标点/全角符号后 trim。"""
    kept = []
    for ch in text:
        cp = ord(ch)
        if any(lo <= cp <= hi for lo, hi in CLEAN_RANGES):
            continue
        if ch in CLEAN_ASCII:
            continue
        kept.append(ch)
    return "".join(kept).strip()


def split_long_phrase(text, max_len):
    """语料 splitLongPhrase：优先标点，其次汉字边界，最后硬切。"""
    out = []
    pending = text
    while u16len(pending) > max_len:
        cps = list(pending)
        pre = u16_prefix(cps)
        # JS lastIndexOf(delim, maxLen-1)：只在前 maxLen 个码元里找；
        # 码元位置随 code point 单调，故先求最后一个合法位置。
        p = len(cps) - 1
        while p > 0 and pre[p] > max_len - 1:
            p -= 1
        last_ok = p

        cut = 0
        for delim in SPLIT_PRIORITY:
            for q in range(last_ok, 0, -1):
                if cps[q] == delim:
                    cut = q + 1
                    break
            if cut:
                break
        if not cut:
            for q in range(last_ok, 0, -1):
                if is_ideograph(ord(cps[q])):
                    cut = q + 1
                    break
        if not cut:
            q = len(cps)
            while q > 1 and pre[q] > max_len:
                q -= 1
            cut = max(q, 1)

        out.append(pending[:cut].strip())
        pending = pending[cut:].strip()
        if not pending:
            break
    if pending:
        out.append(pending)
    return out


def align(texts, durations, text_max):
    """返回 [(caption, duration_us)]，与语料 processSubtitles 同序。"""
    if text_max < 0:
        raise ValueError("text_max: want >= 0, got %d" % text_max)
    max_len = text_max or DEFAULT_MAX_LINE
    if len(durations) < len(texts):
        raise ValueError(
            "duration_list has %d entries but texts has %d; they must be 1:1"
            % (len(durations), len(texts)))
    caps = []
    for i, text in enumerate(texts):
        total = durations[i]
        phrases = [clean_caption(p) for p in split_long_phrase(text, max_len)]
        phrases = [p for p in phrases if p]
        if not phrases:
            caps.append(("[无内容]", total))
            continue
        total_chars = sum(u16len(p) for p in phrases)
        acc = 0
        for j, phrase in enumerate(phrases):
            if j == len(phrases) - 1:
                span = total - acc
            else:
                span = math.floor(total * u16len(phrase) / total_chars + 0.5)
            caps.append((phrase, span))
            acc += span
    return caps


def timelines_of(caps):
    out = []
    cursor = 0
    for _, span in caps:
        span = int(math.floor(span + 0.5))
        out.append({"start": cursor, "end": cursor + span})
        cursor += span
    return out


# ── 两个库的入口 ─────────────────────────────────────────────────────────

def _run(args):
    texts = as_text_list(arg(args, "texts"), "texts")
    durations = as_duration_list(arg(args, "duration_list"), "duration_list")
    text_max = as_int(arg(args, "text_max"), 0, "text_max")
    return align(texts, durations, text_max)


def fn_audio_align_free(args):
    caps = _run(args)
    return {
        "text_captions": [c for c, _ in caps],
        "text_timielines": timelines_of(caps),  # header 的原拼写
    }


def fn_audio_subtitle_align_alt(args):
    caps = _run(args)
    return {
        "text_captions": [c for c, _ in caps],
        "text_timelines": timelines_of(caps),
    }


HANDLERS = {
    "audio_align_free.align_text_to_audio": fn_audio_align_free,
    "audio_subtitle_align_alt.align_text_to_audio": fn_audio_subtitle_align_alt,
}


# ── 协议 ────────────────────────────────────────────────────────────────

def send(msg):
    sys.stdout.write(json.dumps(msg, ensure_ascii=False) + "\n")
    sys.stdout.flush()


def result(mid, value):
    send({"jsonrpc": "2.0", "id": mid, "result": {"value": value}})


def error(mid, message, cls=None):
    data = {"class": cls} if cls else {}
    send({"jsonrpc": "2.0", "id": mid, "error": {
        "code": -32000, "message": message, "data": data}})


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
            print("align_toolkit: bad json line", file=sys.stderr)
            continue
        if "id" not in msg:
            continue  # notification (cancel)：实现都很快，直接忽略
        if not handle(msg):
            return


if __name__ == "__main__":
    main()
