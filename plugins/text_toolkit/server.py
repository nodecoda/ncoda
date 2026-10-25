#!/usr/bin/env python3
"""text_toolkit —— 纯文本/数组库的进程外实现（ncoda-plugin/1）。

覆盖：string_to_array / link_extract / url_get / text_split_master /
text_split_tool / null_fill / array_split_merge。

约束（PLUGIN-AUTHORING-SPEC §4/§5）：
  - 只依赖 args；不读文件、不联网、不碰时钟；
  - 只返回 header 声明过的键；整数不发浮点；
  - 未证实的语义用 partial + loss 声明，不冒充 real。

协议：stdin/stdout 一行一条 JSON-RPC 2.0；日志一律走 stderr。
"""
import json
import re
import sys

PROTOCOL = "ncoda-plugin/1"
PLUGIN_ID = "text-toolkit"
PLUGIN_VERSION = "0.1.0"

LIBRARIES = [
    {"library": "string_to_array", "functions": ["string_2_array"]},
    {"library": "link_extract", "functions": ["extract_all_links", "extract_first_link"]},
    {"library": "url_get", "functions": ["get_url"]},
    {"library": "text_split_master", "functions": ["TextSplittingMaster"]},
    {"library": "text_split_tool", "functions": ["Paragraph_splitting"]},
    {"library": "null_fill", "functions": ["null_value_filling"]},
    {"library": "array_split_merge", "functions": ["Str_list", "Str_merge"]},
    {"library": "kimi_chat_parser", "functions": ["kimi_chat_parser"]},
]


# ── wire 工具 ────────────────────────────────────────────────────────────

def arg(args, name, default=None):
    """header 参数名 → wire 键；`$` 前缀两种写法都接受（P1）。"""
    if name in args and args[name] not in (None,):
        return args[name]
    bare = name[1:] if name.startswith("$") else "$" + name
    if bare in args and args[bare] not in (None,):
        return args[bare]
    return default


def as_text(v, name="arg"):
    """把 header 的 `= 0` 缺省与常见标量归一成 str；容器是契约错误。"""
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
    """array<T> 归一成 list；`= 0` / 缺省 / 空串 = 空数组。"""
    if v in (None, "", 0):
        return []
    if isinstance(v, list):
        return v
    raise ValueError("%s: want array, got %s" % (name, type(v).__name__))


def as_number(v, default=0.0, name="arg"):
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


# ── link_extract / url_get ───────────────────────────────────────────────

URL_RE = re.compile(r"https?://[^\s<>\"'\u3000\u3002\uff0c\uff1b\uff1f\uff01\uff09\uff3d\uff5d]+")
TRAIL = ".,;:!?)]}\u3002\uff0c\uff1b\uff1a\uff01\uff1f\u300d\u300f\uff09"


def find_links(text):
    out = []
    for m in URL_RE.findall(text):
        url = m.rstrip(TRAIL)
        if url and url not in out:
            out.append(url)
    return out


def fn_extract_all_links(args):
    text = as_text(arg(args, "text"), "text")
    return {"data": find_links(text), "message": "", "prompt": "", "code": 0}


def fn_extract_first_link(args):
    text = as_text(arg(args, "text"), "text")
    links = find_links(text)
    return {"data": links[0] if links else "", "message": "", "prompt": "", "code": 0}


def fn_get_url(args):
    """提取首个链接；没有链接时原样返回去空白后的内容（语料里也传裸 URL）。"""
    content = as_text(arg(args, "content"), "content")
    links = find_links(content)
    return {"url": links[0] if links else content.strip()}


# ── string_to_array ─────────────────────────────────────────────────────

def fn_string_2_array(args):
    content = as_text(arg(args, "content"), "content")
    splitter = as_text(arg(args, "splitter"), "splitter")
    if not content:
        data = []
    elif splitter:
        data = [p.strip() for p in content.split(splitter)]
    else:
        lines = [p.strip() for p in content.splitlines()]
        if len(lines) <= 1:
            # 单行：语料里的 share_links 可能是空格/制表符分隔的一串链接。
            lines = re.split(r"[\s,;]+", content)
        data = [p.strip() for p in lines]
    data = [p for p in data if p]
    return {"data": data, "message": "", "prompt": "", "code": 0}


# ── 文本切分 ─────────────────────────────────────────────────────────────

SENT_RE = re.compile(r"[^。！？!?\n]+[。！？!?]?")


def split_paragraphs(text):
    text = text.replace("\r\n", "\n").replace("\r", "\n").strip()
    if not text:
        return []
    if "\n\n" in text:
        parts = re.split(r"\n{2,}", text)
    else:
        parts = text.split("\n")
    return [p.strip() for p in parts if p.strip()]


def split_sentences(par):
    return [s.strip() for s in SENT_RE.findall(par) if s.strip()]


def fn_TextSplittingMaster(args):
    text = as_text(arg(args, "input"), "input")
    chunks = []
    for par in split_paragraphs(text):
        buf = ""
        for sent in split_sentences(par):
            if buf and len(buf) + len(sent) > 200:
                chunks.append(buf)
                buf = sent
            else:
                buf += sent
        if buf:
            chunks.append(buf)
    return {"output": chunks}


def fn_Paragraph_splitting(args):
    text = as_text(arg(args, "text"), "text")
    return {"segments": split_paragraphs(text)}


# ── null_fill ───────────────────────────────────────────────────────────

def fn_null_value_filling(args):
    rows = as_items(arg(args, "array"), "array")
    filled = ["" if v is None else (v if isinstance(v, str) else str(v)) for v in rows]
    return {"filled_data": filled}


# ── array_split_merge ───────────────────────────────────────────────────

MAX_GROUPS = 10


def fn_Str_list(args):
    rows = [x if isinstance(x, str) else str(x) for x in as_items(arg(args, "Str_list"), "Str_list")]
    limit = as_number(arg(args, "num"), 0.0, "num")
    if not rows:
        groups = []
    elif limit <= 0:
        groups = [list(rows)]
    else:
        groups, cur, size = [], [], 0
        for item in rows:
            if cur and size + len(item) > limit:
                groups.append(cur)
                cur, size = [], 0
            cur.append(item)
            size += len(item)
        if cur:
            groups.append(cur)
        if len(groups) > MAX_GROUPS:
            # 不丢数据：超出 10 组的部分并进最后一组。
            tail = groups[MAX_GROUPS - 1:]
            groups = groups[:MAX_GROUPS - 1] + [[x for g in tail for x in g]]
    out = {"contact": ""}
    for i in range(1, MAX_GROUPS + 1):
        out["Str_list%d" % i] = groups[i - 1] if i <= len(groups) else []
    return out


def fn_Str_merge(args):
    lists = []
    for i in range(1, 6):
        lists.append([x if isinstance(x, str) else str(x)
                      for x in as_items(arg(args, "Str_list%d" % i), "Str_list%d" % i)])
    split = [{"Str_list": xs} for xs in lists if xs]
    all_items = [x for xs in lists for x in xs]
    return {"All_Str_list": all_items, "Split_str": split, "contact": ""}


# ── kimi_chat_parser ────────────────────────────────────────────────────

QUESTION_KEYS = ("用户", "user", "human", "我", "q", "问")
ANSWER_KEYS = ("kimi", "助手", "assistant", "ai", "a", "答")

SPEAKER_RE = re.compile(
    r"^[*\s]*(用户|user|human|我|q|问|kimi|助手|assistant|ai|a|答)[*\s]*[:：]\s*(.*)$",
    re.I,
)


def role_of(name):
    low = name.strip().lower()
    if low in QUESTION_KEYS:
        return "question"
    return "answer"


def messages_from_records(items):
    out = []
    for it in items:
        if not isinstance(it, dict):
            continue
        role = it.get("role") or it.get("from") or it.get("speaker") or ""
        text = ""
        for key in ("content", "text", "message", "value"):
            if it.get(key) not in (None, ""):
                text = as_text(it[key], key)
                break
        if not role:
            # 兼容 {question, answer} 直出形态
            if it.get("question") is not None or it.get("answer") is not None:
                out.append(("question", as_text(it.get("question", ""), "question")))
                out.append(("answer", as_text(it.get("answer", ""), "answer")))
                continue
            role = "user"
        out.append((role_of(str(role)), text))
    return out


def messages_from_text(text):
    out, cur_role, cur_lines = [], None, []

    def flush():
        if cur_role is not None:
            out.append((cur_role, "\n".join(cur_lines).strip()))

    for line in text.replace("\r\n", "\n").replace("\r", "\n").split("\n"):
        m = SPEAKER_RE.match(line)
        if m:
            flush()
            cur_role, cur_lines = role_of(m.group(1)), [m.group(2)]
        elif cur_role is not None:
            cur_lines.append(line)
    flush()
    return out


def pair_messages(messages):
    questions, answers, chunks = [], [], []
    cur = None
    for role, text in messages:
        if role == "question":
            if cur is not None:
                chunks.append(cur)
            cur = {"question": text, "answer": ""}
        else:
            if cur is None:
                cur = {"question": "", "answer": text}
            else:
                cur["answer"] = text
    if cur is not None:
        chunks.append(cur)
    for ch in chunks:
        questions.append(ch["question"])
        answers.append(ch["answer"])
    return {"questions": questions, "answers": answers, "chunks": chunks}


def fn_kimi_chat_parser(args):
    """Kimi 聊天记录 → 成对的 questions/answers/chunks。

    语料只有一个字符串入口（corpus/7593650941091184692），输入格式未由语料证实：
    接受 JSON 消息数组（role/content）与「用户：/Kimi：」文本前缀两种形态。
    """
    raw = arg(args, "kimi_chathistory")
    if raw in (None, "", 0):
        return {"questions": [], "answers": [], "chunks": []}
    if isinstance(raw, list):
        return pair_messages(messages_from_records(raw))
    if not isinstance(raw, str):
        raise ValueError("kimi_chathistory: want string or array, got %s" % type(raw).__name__)
    text = raw.strip()
    if not text:
        return {"questions": [], "answers": [], "chunks": []}
    if text.startswith("{") or text.startswith("["):
        try:
            parsed = json.loads(text)
        except ValueError:
            parsed = None
        if isinstance(parsed, dict) and isinstance(parsed.get("messages"), list):
            return pair_messages(messages_from_records(parsed["messages"]))
        if isinstance(parsed, list):
            return pair_messages(messages_from_records(parsed))
    return pair_messages(messages_from_text(text))


HANDLERS = {
    "string_to_array.string_2_array": fn_string_2_array,
    "link_extract.extract_all_links": fn_extract_all_links,
    "link_extract.extract_first_link": fn_extract_first_link,
    "url_get.get_url": fn_get_url,
    "text_split_master.TextSplittingMaster": fn_TextSplittingMaster,
    "text_split_tool.Paragraph_splitting": fn_Paragraph_splitting,
    "null_fill.null_value_filling": fn_null_value_filling,
    "array_split_merge.Str_list": fn_Str_list,
    "array_split_merge.Str_merge": fn_Str_merge,
    "kimi_chat_parser.kimi_chat_parser": fn_kimi_chat_parser,
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
            print("text_toolkit: bad json line", file=sys.stderr)
            continue
        if "id" not in msg:
            continue  # notification (cancel)：实现都很快，直接忽略
        if not handle(msg):
            return


if __name__ == "__main__":
    main()
