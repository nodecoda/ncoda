package ir_test

// 执行语义 P2（HTTP host 切片）的 kernel 侧判据：`params` 进入语言词表后，
// 查询参数与 headers 走**同一条**模板槽规则（见 ir.HTTPConfigKeys 的 doc）。
// 三层各自红线：
//   · 词表单源 —— 表外的键仍然 fail loud（不得因为加了 params 就放宽为静默丢弃）；
//   · 条目成文 —— 字面量/裸引用/`${}` 模板三种写法都落到同一个 {name: value} 条目；
//   · 依赖单次 —— 同一 producer 端口在 Inputs 里只登记一次（两个通道共用去重表）。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

func TestHTTPConfigKeysSingleSource(t *testing.T) {
	want := []string{"body", "headers", "params"}
	if len(ir.HTTPConfigKeys) != len(want) {
		t.Fatalf("HTTPConfigKeys = %v, want %v", ir.HTTPConfigKeys, want)
	}
	for i, k := range want {
		if ir.HTTPConfigKeys[i] != k {
			t.Fatalf("HTTPConfigKeys[%d] = %q, want %q (表 = 词表唯一声明点)", i, ir.HTTPConfigKeys[i], k)
		}
	}
}

func TestBuildHTTPParams(t *testing.T) {
	src := workflowHeader + `
function main(string location, string apiKey) -> int {
    let r = http("GET", "https://api.example.com/search?lang=zh", {
        "headers": { "x-api-key": apiKey },
        "params": {
            "location": location,
            "key": ` + "`" + `k-${apiKey}` + "`" + `,
            "page": "1"
        }
    });
    return r.status_code;
}
`
	g := buildIR(t, src)
	http := firstOf(t, g, ir.KindHTTP).(*ir.HTTPCall)
	if len(http.Params) != 3 {
		t.Fatalf("params = %+v, want 3 ordered entries (location/key/page)", http.Params)
	}
	wantOrder := []string{"location", "key", "page"}
	for i, name := range wantOrder {
		if http.Params[i].Name != name {
			t.Fatalf("params[%d].Name = %q, want %q (作者序，不是 map 序)", i, http.Params[i].Name, name)
		}
	}
	apiKeySlot := ""
	for _, in := range http.Inputs {
		if in.Name == "apiKey" {
			apiKeySlot = "{{block_output_" + in.BlockID + ".apiKey}}"
		}
	}
	if apiKeySlot == "" {
		t.Fatalf("inputs = %+v, want an apiKey producer slot", http.Inputs)
	}
	if got, want := http.Params[1].Value, "k-"+apiKeySlot; got != want {
		t.Errorf("params[key] = %q, want %q（`${}` 与裸引用同形）", got, want)
	}
	if got, want := http.Params[0].Value, strings.Replace(apiKeySlot, "apiKey", "location", 1); got != want {
		t.Errorf("params[location] = %q, want %q（裸引用 = 退化模板）", got, want)
	}
	if http.Params[2].Value != "1" {
		t.Errorf("params[page] = %q, want the literal kept verbatim", http.Params[2].Value)
	}
	// 同一个 apiKey producer 被 header 与 param 各引一次 ⇒ 只登记一次。
	counts := map[string]int{}
	for _, in := range http.Inputs {
		counts[in.Name]++
	}
	if counts["apiKey"] != 1 {
		t.Fatalf("inputs = %+v, want apiKey registered exactly once (headers 与 params 共用去重表)", http.Inputs)
	}
	if counts["location"] != 1 {
		t.Fatalf("inputs = %+v, want location registered exactly once", http.Inputs)
	}
}

func TestBuildHTTPUnknownOptionStillFailsLoud(t *testing.T) {
	src := workflowHeader + `
function main(string q) -> int {
    let r = http("GET", "https://api.example.com/x", { "query": q });
    return r.status_code;
}
`
	msg := buildIRErr(t, src)
	if !strings.Contains(msg, `http option "query"`) {
		t.Errorf("error = %q, want it to name the offending key", msg)
	}
	for _, k := range []string{"body", "headers", "params"} {
		if !strings.Contains(msg, k) {
			t.Errorf("error = %q, want it to list the available key %q", msg, k)
		}
	}
}

func TestBuildHTTPParamsMustBeStaticMap(t *testing.T) {
	src := workflowHeader + `
function main(string q) -> int {
    let r = http("GET", "https://api.example.com/x", { "params": q });
    return r.status_code;
}
`
	msg := buildIRErr(t, src)
	if !strings.Contains(msg, "param") || !strings.Contains(msg, "map literal") {
		t.Errorf("error = %q, want the params shape diagnostic (静态 {name: value} 映射)", msg)
	}
}
