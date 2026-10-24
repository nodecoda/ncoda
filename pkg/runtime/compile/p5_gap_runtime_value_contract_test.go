package compile

// p5_gap_runtime_value_contract_test.go — P5 登记 gap 的运行期值判据。
//
// p5-gaps.json 记的是「value / structure signature 不同」，不是「跑出来
// 不一样」。对用户可见的只有后者：同一张输入表，源文本与 coze-biz 往返后
// 在本地 runtime 上必须得到同一个值（两相位，缺一不可）。
//
// 这些判据是「已裁定保真代价」的**证据面**：只有它绿，一条 gap 才允许被
// 标成 accepted-canonicalization；形状差本身仍留在 obligations 的账上，
// 不因为这里绿了就删除。

import (
	"os"
	"path/filepath"
	"testing"
)

// p5FixtureSource 读真实 P5 夹具（与 p5-gaps.json 的 fixture 字段同一文件），
// 避免判据与账面各写一份源码。
func p5FixtureSource(t *testing.T, construct, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "..", "tests", "fixtures", "obligations", construct, name+".ncoda")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fixture %s: %v", path, err)
	}
	return string(src)
}

// A9 类：源臂直接 return（open flow obligation，无 merge）；反编译把臂值
// 提升成 Group1 closed phi。运行期值必须逐个臂相同 —— 否则就不是形状差，
// 而是真的值错，ruling 不成立。
func TestP5GapIfSharedEndValue(t *testing.T) {
	runConstructDiff(t, "p5_if_shared_end", p5FixtureSource(t, "if", "shared_end"), []diffCase{
		{"then 臂", map[string]any{"input": "a"}, "pos"},
		{"else 臂", map[string]any{"input": "b"}, "neg"},
	})
}

func TestP5GapSwitchReturnArmsValue(t *testing.T) {
	runConstructDiff(t, "p5_switch_return_arms", p5FixtureSource(t, "switch", "return_arms"), []diffCase{
		{"branch_0", map[string]any{"kind": "a"}, "A"},
		{"branch_1", map[string]any{"kind": "b"}, "B"},
		{"default", map[string]any{"kind": "z"}, "?"},
	})
}

// parallel/assign 的 gap 是「构造消失」（纯赋值分支被折叠成 text concat），
// 比 A9 的 phi 形状更强。这里只证明**值**仍然相同；构造是否能从产物恢复
// 是另一条裁定，故本条 gap 仍保持 open，不随本判据转 ruled。
func TestP5GapParallelAssignValue(t *testing.T) {
	runConstructDiff(t, "p5_parallel_assign", p5FixtureSource(t, "parallel", "assign"), []diffCase{
		{"两分支都赋值", map[string]any{"seed": "x"}, "xx"},
	})
}

// ask 的结构 gap（源臂 terminal=return，产物给每臂 value exit 进 Group1
// merge）必须在运行期证伪：同一张 HITL 答复表，源文本与**往返后文本**都要
// 得到同一个臂值。只有这条绿，结构 gap 才允许被裁定为 canonical 形状差。
type askPhaseCase struct {
	name   string
	answer map[string]any
	want   string
}

// askRoundtripSource 是往返腿的注入点，由适配器 lane（商业仓的 L4 判据）
// 赋值。内核里为 nil：内核没有"写出平台产物再读回来"的能力，那属于翻译插件，
// 不属于语言。nil 时本判据只跑源文本腿，并在日志里说明往返腿没有运行。
var askRoundtripSource func(t *testing.T, name, src string) string

func checkAskFixtureBothPhases(t *testing.T, fixture string, cases []askPhaseCase) {
	t.Helper()
	src := p5FixtureSource(t, "ask", fixture)
	rt := ""
	if askRoundtripSource != nil {
		rt = askRoundtripSource(t, "p5_ask_"+fixture, src)
	} else {
		t.Logf("%s: 往返腿未运行（L4 判据在商业仓）", fixture)
	}
	for _, c := range cases {
		caseName := fixture + "-" + c.name
		gotSrc := runQASource(t, caseName+"-src", c.answer, func(s *qaMemStore) Runnable {
			return qaRunnableFromSource(t, src, s)
		})
		if gotSrc["output"] != c.want {
			t.Errorf("[%s · 源文本] output = %v, want %v", caseName, gotSrc["output"], c.want)
		}
		if rt == "" {
			continue
		}
		gotRT := runQASource(t, caseName+"-rt", c.answer, func(s *qaMemStore) Runnable {
			return qaRunnableFromSource(t, rt, s)
		})
		if gotRT["output"] != c.want {
			t.Errorf("[%s · 往返后] output = %v, want %v", caseName, gotRT["output"], c.want)
		}
	}
}

func TestP5GapAskReturnArmsRoundtripValue(t *testing.T) {
	checkAskFixtureBothPhases(t, "return_arms", []askPhaseCase{
		{"approve", map[string]any{"choice": "approve", "comment": "looks good"}, "looks good"},
		{"reject", map[string]any{"choice": "reject"}, "rejected"},
	})
}

func TestP5GapAskConcatReturnRoundtripValue(t *testing.T) {
	checkAskFixtureBothPhases(t, "concat_return", []askPhaseCase{
		{"approve", map[string]any{"choice": "approve", "comment": "looks good"}, "looks good!"},
		{"reject", map[string]any{"choice": "reject"}, "no"},
	})
}
