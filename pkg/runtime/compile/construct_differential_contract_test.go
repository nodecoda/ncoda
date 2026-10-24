package compile

// construct_differential_contract_test.go — **C3 双语义差分**（构造级，最小五条）。
//
// 判据形态：每条构造问两次，缺一不可（往返腿仅在 cozebiz 构建标签下启用）。
// P0 默认面只跑源文本（语言语义）；adapter lane `-tags cozebiz` 通过
// roundtripCaseCheck 钩子补上往返腿。两条 lane 分开判，不互相背书。
//
//	① 源文本在本地 runtime 上的值 == **期望值表**
//	② 源文本经一轮 `compile(coze-biz-1.0) → decompile` 后，仍然 == **同一张表**
//
// 为什么两问都要：只做 ②（往返前后一致）时，源侧与往返侧**同错**照样绿 ——
// 这是 roundtrip_value_preservation_contract_test.go 头注里的一手教训。
// 为什么用表：表里写的是「这个构造的语义是什么」，是被证伪的承诺；
// 「与另一个实现一致」不是判据（两个实现可以一起错）。
//
// 覆盖边界（诚实声明）：
//   - 只锁**值 / 路由**。端口形状、wire 字段、产物拓扑由 obligations/lowering 那条线锁。
//   - 未覆盖：for / parallel（源语法尚无夹具）、tool / llm / http（外部副作用，
//     不在本切片）、break / continue（无源夹具）。
//   - 每条构造的 case 表含「边界相位」（0 次迭代 / 两臂都不走），缺它则
//     「未执行臂的候选被误取」这一族看不见。

import "testing"

const diffIfElse = `@language nodecoda/1
@mode workflow

function main(string input) -> string {
    string out;
    out = "";
    if (input == "a") {
        out = "pos";
    } else {
        out = "neg";
    }
    return out;
}
`

const diffElseIfChain = `@language nodecoda/1
@mode workflow

function main(string input) -> string {
    string out;
    out = "?";
    if (input == "a") {
        out = "A";
    } else if (input == "b") {
        out = "B";
    } else {
        out = "?";
    }
    return out;
}
`

const diffSwitch = `@language nodecoda/1
@mode workflow

function main(string kind) -> string {
    string out;
    out = "base";
    switch (kind) {
        "a": { out = "A"; }
        "b": { out = "B"; }
        default: { out = "?"; }
    }
    return out;
}
`

const diffWhile = `@language nodecoda/1
@mode workflow

function main(int step) -> int {
    int num = 0;
    while (num < 3) limit 10 {
        num += step;
    }
    return num;
}
`

const diffNestedIf = `@language nodecoda/1
@mode workflow

function main(bool c1, bool c2) -> string {
    string out;
    out = "";
    if (c1) {
        out = "outer";
        if (c2) {
            out = "inner";
        }
    }
    return out;
}
`

func TestConstructDiffIfElse(t *testing.T) {
	runConstructDiff(t, "if_else", diffIfElse, []diffCase{
		{"命中 then", map[string]any{"input": "a"}, "pos"},
		{"命中 else", map[string]any{"input": "z"}, "neg"},
		{"空串边界", map[string]any{"input": ""}, "neg"},
	})
}

func TestConstructDiffElseIfChain(t *testing.T) {
	runConstructDiff(t, "elseif_chain", diffElseIfChain, []diffCase{
		{"第一支", map[string]any{"input": "a"}, "A"},
		{"第二支", map[string]any{"input": "b"}, "B"},
		{"兜底支", map[string]any{"input": "c"}, "?"},
	})
}

func TestConstructDiffSwitch(t *testing.T) {
	runConstructDiff(t, "switch", diffSwitch, []diffCase{
		{"分支 a", map[string]any{"kind": "a"}, "A"},
		{"分支 b", map[string]any{"kind": "b"}, "B"},
		{"default", map[string]any{"kind": "zzz"}, "?"},
	})
}

func TestConstructDiffWhile(t *testing.T) {
	runConstructDiff(t, "while", diffWhile, []diffCase{
		{"步长 0（不收敛，靠 limit 兜底）", map[string]any{"step": int64(0)}, int64(0)},
		{"步长 1", map[string]any{"step": int64(1)}, int64(3)},
		{"步长 2", map[string]any{"step": int64(2)}, int64(4)},
	})
}

func TestConstructDiffNestedIf(t *testing.T) {
	runConstructDiff(t, "nested_if", diffNestedIf, []diffCase{
		{"两臂都不走", map[string]any{"c1": false, "c2": false}, ""},
		{"只走外臂", map[string]any{"c1": true, "c2": false}, "outer"},
		{"两层都走", map[string]any{"c1": true, "c2": true}, "inner"},
	})
}
