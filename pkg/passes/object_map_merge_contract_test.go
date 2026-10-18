package passes

// object_map_merge_contract_test.go —— 源码层判据：`object` 与 `map<string, any>`
// 是同一个类型（2026-09-15 用户裁定）。
//
// 两条断言都是**存活侧 / 正面**成对：
//   - `llm<object>` 必须继续通过（合并不得让既有拼写回归）；
//   - `llm<map<string, any>>` 必须通过（合并前它与 `llm<object>` 是同一个物理类型
//     却判「拒绝」——判据分裂；撤实现 ⇒ 本条 RED）。
//
// 赋值方向（下面的 TestObjectMapAssignmentBothWays）覆盖 p5x 实测的失败形状：
// `map<string,any> m = o;` 与 `o = m;` 合并前都报 TYPE_MISMATCH。

import (
	"fmt"
	"strings"
	"testing"
)

func runPasses(t *testing.T, src string) {
	t.Helper()
	r := DefaultPipeline().Run(src, "object-map-merge.ncoda", nil)
	if r.HasErrors() {
		t.Fatalf("rejected: %v", r.Diagnostics)
	}
}

func TestLLMPayloadObjectAndMapSameVerdict(t *testing.T) {
	runPasses(t, `@language nodecoda/1
@mode workflow
function main(string prompt) -> string {
  let r = llm<object>("model", {"messages": [{"role":"user", "content":prompt}]});
  return to_string(r.text);
}`)
	runPasses(t, `@language nodecoda/1
@mode workflow
function main(string prompt) -> string {
  let r = llm<map<string, any>>("model", {"messages": [{"role":"user", "content":prompt}]});
  return to_string(r.text);
}`)
}

func TestObjectMapAssignmentBothWays(t *testing.T) {
	// 两个方向都必须在**声明/绑定**位置可赋值；用可变全局状态的写法会撞
	// 无关的 INVALID_STATE 规则，故只用 let / 具类型声明。
	src := `@language nodecoda/1
@mode workflow
function main(string u) -> string {
  object o = {};
  let m = o;
  map<string, any> n = m;
  object p = n;
  return to_string(p);
}`
	r := DefaultPipeline().Run(src, "object-map-assign.ncoda", nil)
	for _, d := range r.Diagnostics {
		// 合并的目的就是消灭这条：同一个值在两种拼写下互不可赋值。
		if strings.Contains(fmt.Sprint(d.Code), "TYPE_MISMATCH") {
			t.Fatalf("object/map 互相赋值被拒（合并未生效）: %s", d.Message)
		}
	}
	if r.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", r.Diagnostics)
	}
}
