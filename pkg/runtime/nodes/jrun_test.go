package nodes

// jrun_test.go — coze 平台 JavaScript code 节点子进程执行器单元测试。
// 依赖系统 node（nodeAvailable 探测失败时跳过——平台无解释器不可验证）。

import (
	"context"
	"reflect"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
)

func requireNode(t *testing.T) {
	t.Helper()
	if !nodeAvailable() {
		t.Skipf("node not available: %s", jsErrMsg)
	}
}

// coze 模板：async function main({ params }: Args): Promise<Output>。
func TestJSRunArgsStyle(t *testing.T) {
	requireNode(t)
	code := `async function main({ params }: Args): Promise<Output> {
    return { result: params["text"] + ":" + params["num"] };
}`
	out, err := runJavaScript(context.Background(), code, map[string]any{
		"text": "x", "num": 7,
	}, "")
	if err != nil {
		t.Fatalf("runJavaScript: %v", err)
	}
	m := out.(map[string]any)
	if m["result"] != "x:7" {
		t.Fatalf("result = %#v, want x:7", m["result"])
	}
}

// 无 TS 注解的纯 JS + input 形参。
func TestJSRunPlainInput(t *testing.T) {
	requireNode(t)
	code := `function main(input) {
    return { result: input["a"] + input["b"] };
}`
	out, err := runJavaScript(context.Background(), code, map[string]any{
		"a": "1", "b": "2",
	}, "")
	if err != nil {
		t.Fatalf("runJavaScript: %v", err)
	}
	m := out.(map[string]any)
	if m["result"] != "12" {
		t.Fatalf("result = %#v, want 12", m["result"])
	}
}

// 模板字符串含冒号/花括号不被剥离误伤。
func TestJSRunTemplateStringUntouched(t *testing.T) {
	requireNode(t)
	code := "async function main({ params }: Args): Promise<Output> {\n" +
		"    return { output: `prefix:${params.v} {brace}` };\n" +
		"}"
	out, err := runJavaScript(context.Background(), code, map[string]any{"v": "ok"}, "")
	if err != nil {
		t.Fatalf("runJavaScript: %v", err)
	}
	m := out.(map[string]any)
	if m["output"] != "prefix:ok {brace}" {
		t.Fatalf("output = %#v, want prefix:ok {brace}", m["output"])
	}
}

// 同步 main + params:any 变体。
func TestJSRunParamsAnyVariant(t *testing.T) {
	requireNode(t)
	code := `function main(params: any) {
    return { out: params.x };
}`
	out, err := runJavaScript(context.Background(), code, map[string]any{"x": "v"}, "")
	if err != nil {
		t.Fatalf("runJavaScript: %v", err)
	}
	m := out.(map[string]any)
	if m["out"] != "v" {
		t.Fatalf("out = %#v, want v", m["out"])
	}
}

// TS 模板变体剥离（coze 语料实证形态）:返回类型可含嵌套泛型/对象字面量
// （Promise<{...}> / Promise<Record<string, any>>）,形参可多个注解（含
// 对象字面量注解 { params: Record<string, any> }）;剥离后必须能被 node
// 直接解析。这些形态之前击穿正则 `[^>]*` / `[^,)]+`（H39/S161 语料）。
func TestJSRunStripTSVariants(t *testing.T) {
	cases := []struct{ in, want string }{
		{`async function main({ params }: Args): Promise<{ image_url: string }> {`,
			`async function main({ params }){`},
		{`async function main({ params }: Args): Promise<Record<string, any>> {`,
			`async function main({ params }){`},
		{`async function main({ params }: Args, callback: any): Promise<Output> {`,
			`async function main({ params }, callback){`},
		{`async function main({ params }: { params: Record<string, any> }): Promise<Record<string, any>> {`,
			`async function main({ params }){`},
		{`async function main({ params }: Args): Promise<Output> {`,
			`async function main({ params }){`},
		// 对象字面量作为返回类型（非 Promise）:第一个 `{` 属于类型面,
		// 不能当成函数体。S110 语料曾因此被剥成非法 JS。
		{`function main(input: any): { processedText: string[], fontSize: number, $warning?: string } {`,
			`function main(input){`},
		{`function main(): { a: string } | { b: number } {`,
			`function main(){`},
	}
	for _, c := range cases {
		got := stripJSTypes(c.in)
		if got != c.want {
			t.Errorf("strip(%q)\n got=%q\nwant=%q", c.in, got, c.want)
		}
	}
}

// 裸数组返回（coze JS 常见形态:return ret; 声明输出 ret:array<T>）:
// runJavaScript 原样回传数组,由 codeNode.Invoke 映射到声明输出名。
func TestJSRunBareArray(t *testing.T) {
	requireNode(t)
	code := `async function main({ params }: Args): Promise<Output> {
  let ret = [params.v + "-a", params.v + "-b"];
  return ret;
}`
	out, err := runJavaScript(context.Background(), code, map[string]any{"v": "x"}, "")
	if err != nil {
		t.Fatalf("runJavaScript: %v", err)
	}
	arr, ok := out.([]any)
	if !ok {
		t.Fatalf("out = %#v, want []any", out)
	}
	if len(arr) != 2 || arr[0] != "x-a" || arr[1] != "x-b" {
		t.Fatalf("arr = %#v, want [x-a x-b]", arr)
	}
}

// 异常 → RCodeRuntimeError。
func TestJSRunErrorWrapped(t *testing.T) {
	requireNode(t)
	code := `function main(input) { throw new Error("boom"); }`
	_, err := runJavaScript(context.Background(), code, map[string]any{}, "")
	if err == nil {
		t.Fatal("runJavaScript: want error, got nil")
	}
	if err.(*diag.Error).Code != diag.RCodeRuntimeError {
		t.Fatalf("error code = %v, want RCodeRuntimeError", err.(*diag.Error).Code)
	}
	if err.(*diag.Error).Message == "" {
		t.Fatal("missing message")
	}
}

// TestJSRunEntropySeeded —— 离线参考执行的**确定性契约**（2026-09-20）。
//
// 为什么是语言级判据而不是「某个测试用例的宽容度」：门禁（rt-l3 的
// SEMANTIC 面）对同一语料必须给出同一数字，否则判据不可复现就不是判据。
// 语料 18/672 例的 code 节点用 Math.random 选下标，越界与否随抽取结果
// 摆动 ⇒ 全量两次跑出 match=535 / 534。
//
// 两把锁（缺一即假绿）：
//  1. 同种子两次执行 ⇒ 结果逐字相同（撤掉播种：真随机 ⇒ 必红）；
//  2. 换种子 ⇒ 序列必须变（否则「同种子同结果」是把随机桩成常量造出来的）；
//  3. 逃生门 off ⇒ 退回平台真随机（播种不等于禁用随机）。
func TestJSRunEntropySeeded(t *testing.T) {
	requireNode(t)
	// 多次抽取：单次抽取在 53 位浮点上「恰好相等」的概率不可忽略地小，
	// 但用向量把 flaky 概率压到可忽略（8 次抽取）。
	const code = `async function main({ params }: Args): Promise<Output> {
    let xs = [];
    for (let i = 0; i < 8; i++) xs.push(Math.random());
    return { xs: xs };
}`

	run := func() []any {
		t.Helper()
		out, err := runJavaScript(context.Background(), code, map[string]any{}, "")
		if err != nil {
			t.Fatalf("runJavaScript: %v", err)
		}
		m, ok := out.(map[string]any)
		if !ok {
			t.Fatalf("out = %#v, want map", out)
		}
		xs, ok := m["xs"].([]any)
		if !ok || len(xs) != 8 {
			t.Fatalf("xs = %#v, want 8 项数组", m["xs"])
		}
		return xs
	}

	a := run()
	b := run()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("默认种子两次执行结果不同（熵未播种，门禁不可复现）：\n a=%v\n b=%v", a, b)
	}

	// 字面量而非实现常量：撤掉实现后这里必须撞在**断言**上，而不是编译错误
	// （mutation_check 的口径：失败形态必须是断言失败——引用实现符号会让
	//  撤实现变成 undefined 的构建失败，那不是"测试锁住了规则"的证据）。
	t.Setenv("GODCC_JS_SEED", "godcc-js-2")
	c := run()
	if reflect.DeepEqual(a, c) {
		t.Fatalf("换种子后序列不变 ⇒ 随机被桩成常量（不是播种）：%v", c)
	}
	for i, v := range a {
		if v == c[i] {
			t.Fatalf("第 %d 个抽取值跨种子相同（%v）⇒ 疑似常量而非 PRNG", i, v)
		}
	}

	t.Setenv("GODCC_JS_SEED", "off")
	d := run()
	e := run()
	if reflect.DeepEqual(d, e) {
		t.Fatalf("off 逃生门未恢复真随机（两次相同）：%v", d)
	}
}

// 对象字面量返回类型的端到端回归（S110 语料形态）。
func TestJSRunObjectLiteralReturnType(t *testing.T) {
	requireNode(t)
	code := `function main(input: any): { processedText: string[], fontSize: number } {
    return { processedText: [String(input)], fontSize: 24 };
}`
	out, err := runJavaScript(context.Background(), code, map[string]any{"a": 1}, "")
	if err != nil {
		t.Fatalf("runJavaScript: %v", err)
	}
	m := out.(map[string]any)
	if m["fontSize"] != float64(24) {
		t.Fatalf("out = %#v, want fontSize 24", m)
	}
}

// coze 变体：main(input) 通过 input.params 访问平台包装。
func TestJSRunInputParamsWrapper(t *testing.T) {
	requireNode(t)
	code := `function main(input: InputParams): OutputResult {
    const { params } = input;
    return { out: params.a + params.b };
}`
	out, err := runJavaScript(context.Background(), code, map[string]any{"a": "x", "b": "y"}, "")
	if err != nil {
		t.Fatalf("runJavaScript: %v", err)
	}
	if out.(map[string]any)["out"] != "xy" {
		t.Fatalf("out = %#v, want xy", out)
	}
}
