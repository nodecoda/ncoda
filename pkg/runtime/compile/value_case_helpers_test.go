package compile

// value_case_helpers_test.go — valueCase/checkValues 是 P0 语言判据的共享机械面。
// 它们只跑本地 runtime，不碰任何平台包；roundtrip 专属的
// compileToCozeBiz/roundtripOnce 留在 cozebiz-tagged 文件里。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
)

// diffCase 一条构造用例：输入 → 声明的期望值。
type diffCase struct {
	name string
	in   map[string]any
	want any
}

// roundtripCaseCheck is installed by the cozebiz-tagged adapter lane. It is nil
// in P0: roundtrip belongs to L4, not to language semantics.
var roundtripCaseCheck func(t *testing.T, construct, src string, cases []valueCase)

// runConstructDiff runs the source leg always and the roundtrip leg only when
// the adapter lane installed roundtripCaseCheck.
func runConstructDiff(t *testing.T, construct, src string, cases []diffCase) {
	t.Helper()
	table := make([]valueCase, len(cases))
	for i, c := range cases {
		table[i] = valueCase{name: c.name, in: c.in, want: c.want}
	}
	checkValues(t, construct+" · 源文本", src, table)
	if roundtripCaseCheck != nil {
		roundtripCaseCheck(t, construct, src, table)
	}
}

type valueCase struct {
	name string
	in   map[string]any
	want any
}

func checkValues(t *testing.T, label, src string, cases []valueCase) {
	t.Helper()
	got := runSourceValues(t, src, cases)
	for i, c := range cases {
		if got[i] != c.want {
			t.Errorf("[%s] 值与声明语义不符（%s）：输入 %v\n  实测 = %v\n  期望 = %v\n--- 被判的文本 ---\n%s",
				label, c.name, c.in, got[i], c.want, src)
		}
	}
}

// runSourceValues 把一段 ncoda 源跑在本地 runtime 上，逐条返回 `output` 值。
func runSourceValues(t *testing.T, src string, cases []valueCase) []any {
	t.Helper()
	s := qaLoopFromSource(t, src)
	r := mustCompile(t, New(nodes.StdRegistry()), s)
	out := make([]any, 0, len(cases))
	for _, c := range cases {
		out = append(out, UnwrapOutput(mustInvoke(t, r, c.in))["output"])
	}
	return out
}
