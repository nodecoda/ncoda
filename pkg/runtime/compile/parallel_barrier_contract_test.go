package compile

// parallel_barrier_contract_test.go — N-PAR-1 的语言级判据（三条）：
//
//	① `parallel { ... }` = 纯控制流栅栏/透明屏障：分支内的写是普通写，
//	   栅栏后按名字可读（不同分支写不同变量 ⇒ 各自的值都流出）；
//	② 同一绑定被两个分支写 = **编译期**错误（交错不确定 ⇒ 值无定义）；
//	③ 不产集合值：`parallel` 只是语句，不能当表达式用（语法层拒绝）。
//
// 为什么 ① 只跑源文本、不跑 coze-biz 往返：赋值型臂在 raising 侧会被折进
// 消费表达式（栅栏在读回文本里消失，见 testdata/obligations/parallel/
// assign.ncoda 的 known-gap 注）。那是**往返形状**的缺口，不是源语义；
// 本判据锁源语义，往返形状由 obligations 那条线单独记。
// 含副作用/foreign-code 臂的栅栏存活由 barrier.ncoda 覆盖。

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/passes"
)

const parallelDistinctWrites = `@language nodecoda/1
@mode workflow

function main(string u) -> string {
    string v1;
    v1 = "";
    string v2;
    v2 = "";
    parallel {
        { v1 = u + "!"; }
        { v2 = "B"; }
    }
    return v1 + v2;
}
`

func TestParallelBarrierWritesFlowOut(t *testing.T) {
	checkValues(t, "parallel · 源文本", parallelDistinctWrites, []valueCase{
		{"两个分支各写一个变量，栅栏后都可读", map[string]any{"u": "A"}, "A!B"},
		{"换一个入参", map[string]any{"u": "z"}, "z!B"},
	})
}

const parallelSameName = `@language nodecoda/1
@mode workflow

function main() -> string {
    string v;
    v = "";
    parallel {
        { v = "a"; }
        { v = "b"; }
    }
    return v;
}
`

func TestParallelSameNameIsCompileError(t *testing.T) {
	res := passes.DefaultPipeline().Run(parallelSameName, "parallel_same.ncoda", nil)
	if !res.HasErrors() {
		t.Fatal("同一绑定被两个分支写必须编译期拒绝（交错不确定 ⇒ 值无定义），实际通过")
	}
	var msgs []string
	for _, d := range res.Errors() {
		msgs = append(msgs, fmt.Sprintf("%s: %s", d.Code, d.Message))
	}
	joined := strings.Join(msgs, "\n")
	if !strings.Contains(joined, "PARALLEL_ACCESS_HAZARD") {
		t.Fatalf("拒绝理由必须是 write/write hazard，实际：\n%s", joined)
	}
}

const parallelAsValue = `@language nodecoda/1
@mode workflow

function main() -> string {
    string x;
    x = parallel {
        { x = "a"; }
    };
    return x;
}
`

func TestParallelProducesNoCollectionValue(t *testing.T) {
	res := passes.DefaultPipeline().Run(parallelAsValue, "parallel_value.ncoda", nil)
	if !res.HasErrors() {
		t.Fatal("parallel 不得作为表达式/集合值使用，实际通过")
	}
	var msgs []string
	for _, d := range res.Errors() {
		msgs = append(msgs, fmt.Sprintf("%s: %s", d.Code, d.Message))
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "SYNTAX_ERROR") {
		t.Fatalf("期望语法层拒绝，实际：%v", msgs)
	}
}
