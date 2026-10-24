package compile

// array_index_e2e_test.go — runtime 数组索引缺口探针 + 回归锁定。
//
// 编译器侧数组索引有三条路径（见 docs/RUNTIME-ARRAY-INDEXING.md）:
//   1. G12-2  json.parse<array<T>> 顶层数组 items[i].field → 带越界兜底的
//      提取 shim（M4 求值器 subscript,OOB 守卫）;
//   2. by_array loop 遍历 record 数组,body 内 item.field 元素字段点引用;
//   3. E5-b   record 字段数组 resp.data[0].author → output.data.author
//      item-0 点路径（assemble.go lookupDotted / llm.go lookupDottedPath
//      数组段 item-0 下钻,空数组 → 缺失）。
//
// 这三条路径编译→运行全链必须一致:任何一条 FAIL 即 runtime 数组索引缺口。

import (
	"encoding/json"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
)

// ── 路径 1:json.parse<array<T>> 顶层数组常量索引提取 shim ──────────────

const g12RunSrc = `@language nodecoda/1
@mode workflow
type Item = { string name; string title; };
function main(string raw) {
    let items = json.parse<array<Item>>(raw);
    return items[1].title;
}
`

func TestArrayIndexE2E_JsonParseShim(t *testing.T) {
	s := qaLoopFromSource(t, g12RunSrc)
	r := mustCompile(t, New(nodes.StdRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"raw": `[{"name":"a","title":"A"},{"name":"b","title":"B"}]`}))
	if got, _ := out["output"].(string); got != "B" {
		t.Fatalf("items[1].title = %#v, want \"B\"", out["output"])
	}
}

// 越界兜底:常量索引越界 → shim 守卫空串,不 panic。
const g12OobSrc = `@language nodecoda/1
@mode workflow
type Item = { string name; string title; };
function main(string raw) {
    let items = json.parse<array<Item>>(raw);
    return items[5].title;
}
`

func TestArrayIndexE2E_JsonParseShimOOB(t *testing.T) {
	s := qaLoopFromSource(t, g12OobSrc)
	r := mustCompile(t, New(nodes.StdRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"raw": `[{"name":"a","title":"A"}]`}))
	if got, _ := out["output"].(string); got != "" {
		t.Fatalf("OOB items[5].title = %#v, want empty string (guard)", out["output"])
	}
}

// ── 路径 2:by_array loop 遍历 record 数组 + 元素字段点引用 ──────────────

const g12LoopSrc = `@language nodecoda/1
@mode workflow
type Item = { string name; string title; };
function main(string raw) {
    let items = json.parse<array<Item>>(raw);
    var names = for (item in items) {
        yield item.name;
    };
    return names;
}
`

func TestArrayIndexE2E_LoopOverRecordArray(t *testing.T) {
	s := qaLoopFromSource(t, g12LoopSrc)
	r := mustCompile(t, New(nodes.StdRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"raw": `[{"name":"a","title":"A"},{"name":"b","title":"B"}]`}))
	b, _ := json.Marshal(out["output"])
	want := `["a","b"]`
	if string(b) != want {
		t.Fatalf("for(item in items) yield item.name = %s, want %s", b, want)
	}
}

// ── 路径 3:E5-b record 字段数组 item-0 点路径（runtime item-0 下钻）─────

const e5bRunSrc = `@language nodecoda/1
@mode workflow
type Song = { string author; string title; };
type Resp = { array<Song> data; };
function main(string raw) {
    let resp = json.parse<Resp>(raw);
    return resp.data[0].author;
}
`

func TestArrayIndexE2E_E5bRecordFieldItem0(t *testing.T) {
	s := qaLoopFromSource(t, e5bRunSrc)
	r := mustCompile(t, New(nodes.StdRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"raw": `{"data":[{"author":"the-a","title":"T1"},{"author":"the-b","title":"T2"}]}`}))
	if got, _ := out["output"].(string); got != "the-a" {
		t.Fatalf("resp.data[0].author = %#v, want \"the-a\" (item-0 descent)", out["output"])
	}
}

// 空数组 → 缺失（lookupDotted 不取越界元素、不 panic）;shim 路径空数组
// → 守卫空串。非零/动态索引在 record 数组上的 INDEX_TYPE 编译期拒绝边界
// 由 internal/compiler/g12_array_element_test.go 锁定（record 字段数组
// 索引 1 → COMPILE_ERROR、动态索引 → COMPILE_ERROR）。
const e5bEmptySrc = `@language nodecoda/1
@mode workflow
type Song = { string author; };
type Resp = { array<Song> data; };
function main(string raw) {
    let resp = json.parse<Resp>(raw);
    return resp.data[0].author;
}
`

func TestArrayIndexE2E_E5bEmptyArrayMissing(t *testing.T) {
	s := qaLoopFromSource(t, e5bEmptySrc)
	r := mustCompile(t, New(nodes.StdRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"raw": `{"data":[]}`}))
	// 端口必须存在（putPath 对 nil 也写键）且值为缺失（nil/空串）——不能
	// 因为"output 端口整个缺失"而平凡通过。
	v, ok := out["output"]
	if !ok {
		t.Fatalf("empty-array: output port missing entirely")
	}
	if v != nil && v != "" {
		t.Fatalf("empty-array item-0 = %#v, want missing (nil/empty)", v)
	}
}

// ── 回归:单字段 record 整值消费（keep_whole 防再折叠）──────────────────
// return resp（resp 为 json.parse<单字段 record>）→ output = 整体 dict
// 拷贝,而非折叠成 data 数组。

const wholeValSrc = `@language nodecoda/1
@mode workflow
type Song = { string author; };
type Resp = { array<Song> data; };
function main(string raw) {
    let resp = json.parse<Resp>(raw);
    return resp;
}
`

func TestArrayIndexE2E_WholeValueSingleFieldRecord(t *testing.T) {
	s := qaLoopFromSource(t, wholeValSrc)
	r := mustCompile(t, New(nodes.StdRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"raw": `{"data":[{"author":"a"}]}`}))
	m, ok := out["output"].(map[string]any)
	if !ok {
		t.Fatalf("whole-value output = %#v, want whole dict", out["output"])
	}
	arr, ok := m["data"].([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("whole-value data = %#v, want 1-element array", m["data"])
	}
	el, _ := arr[0].(map[string]any)
	if el["author"] != "a" {
		t.Fatalf("whole-value element = %#v, want {author:a}", arr[0])
	}
}

// ── 回归:多字段 record 的 output.<field> dotted ref（keep_whole 兼容既有
//    multi-key 整 dict 拷贝路径;单字段是本次修复的缺口,多字段本就正确）─────

const e5bMultiFieldSrc = `@language nodecoda/1
@mode workflow
type User = { string name; int age; };
function main(string raw) {
    let u = json.parse<User>(raw);
    return u.name;
}
`

func TestArrayIndexE2E_MultiFieldRecordDotted(t *testing.T) {
	s := qaLoopFromSource(t, e5bMultiFieldSrc)
	r := mustCompile(t, New(nodes.StdRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"raw": `{"name":"ada","age":37}`}))
	if got, _ := out["output"].(string); got != "ada" {
		t.Fatalf("u.name = %#v, want \"ada\"", out["output"])
	}
}
