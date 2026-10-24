package nodes

// pyrun_test.go — M17 用户 python 子进程执行器单元测试。
// 依赖系统 python3（与 corpus 门禁同条件;pythonAvailable 探测失败时
// 测试直接跳过 —— 平台环境无解释器,子进程路径不可验证）。

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func requirePython(t *testing.T) {
	t.Helper()
	if !pythonAvailable() {
		t.Skipf("python3 not available: %s", pyErrMsg)
	}
}

// 成功:多参数 main + 单 key dict → 端口 + 补 "output"。
func TestPyRunMultiParamSingleKey(t *testing.T) {
	requirePython(t)
	code := `def main(text: str, num: int, flag: bool) -> dict:
    return {"result": text + str(num) + str(flag)}`
	out, err := runPython(context.Background(), code, map[string]any{
		"text": "x", "num": 7, "flag": true,
	})
	if err != nil {
		t.Fatalf("runPython: %v", err)
	}
	if out["result"] != "x7True" { // python str(True) == "True"
		t.Fatalf("result = %#v, want x7True", out["result"])
	}
	// 双端口契约在节点层 normalizeCodeOutput 应用（runPython 只出原始 dict）。
	norm := normalizeCodeOutput(out, false) // 用户 python:旧折叠语义
	if norm["output"] != "x7True" {
		t.Fatalf("output port = %#v, want same as result", norm["output"])
	}
}

// 成功:多 key dict + 嵌套结构 → dotted 字段经 "output" 拷贝可达。
func TestPyRunMultiKeyOutputClone(t *testing.T) {
	requirePython(t)
	code := `def main(v: str) -> dict:
    return {"a": v + ":a", "obj": {"field": v + ":f"}}`
	raw, err := runPython(context.Background(), code, map[string]any{"v": "k"})
	if err != nil {
		t.Fatalf("runPython: %v", err)
	}
	if raw["a"] != "k:a" {
		t.Fatalf("a = %#v", raw["a"])
	}
	obj, ok := raw["obj"].(map[string]any)
	if !ok || obj["field"] != "k:f" {
		t.Fatalf("obj = %#v, want {field: k:f}", raw["obj"])
	}
	out := normalizeCodeOutput(raw, false) // 用户 python:旧折叠语义
	clone, ok := out["output"].(map[string]any)
	if !ok {
		t.Fatalf("output port = %T, want map copy", out["output"])
	}
	want := map[string]any{"a": "k:a", "obj": map[string]any{"field": "k:f"}}
	if !reflect.DeepEqual(clone, want) {
		t.Fatalf("output clone %#v must equal pre-output dict %#v", clone, want)
	}
}

// 大输入（> pyRunInputEnvMax）走 PYRUN_INPUT_FILE 临时文件注入:
// env 单串 128KB 上限（MAX_ARG_STRLEN）会 E2BIG,文件路径必须完整往返。
func TestPyRunLargeInputFileInjection(t *testing.T) {
	requirePython(t)
	big := strings.Repeat("abcdefgh", 40000) // 320KB,超过 env 单串上限
	code := `def main(v: str) -> dict:
    return {"n": len(v), "head": v[:8], "tail": v[-8:]}`
	raw, err := runPython(context.Background(), code, map[string]any{"v": big})
	if err != nil {
		t.Fatalf("runPython(large input): %v", err)
	}
	if raw["n"] != float64(len(big)) {
		t.Fatalf("n = %v, want %d", raw["n"], len(big))
	}
	if raw["head"] != "abcdefgh" || raw["tail"] != "abcdefgh" {
		t.Fatalf("content corrupted: head=%v tail=%v", raw["head"], raw["tail"])
	}
}

// 错误传播:python 异常 → RCodeRuntimeError,消息含异常类型。
func TestPyRunExceptionPropagates(t *testing.T) {
	requirePython(t)
	code := `def main(v: str) -> dict:
    raise ValueError("boom-" + v)`
	_, err := runPython(context.Background(), code, map[string]any{"v": "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if diag.CodeOf(err) != diag.RCodeRuntimeError {
		t.Fatalf("code = %v, want RCodeRuntimeError", diag.CodeOf(err))
	}
	if got := err.Error(); !strings.Contains(got, "ValueError") || !strings.Contains(got, "boom-x") {
		t.Fatalf("error message %q missing exception detail", got)
	}
}

// 非 dict 返回 → RCodeRuntimeError。
func TestPyRunNonDictResult(t *testing.T) {
	requirePython(t)
	code := `def main(v: str) -> dict:
    return v`
	_, err := runPython(context.Background(), code, map[string]any{"v": "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if diag.CodeOf(err) != diag.RCodeRuntimeError {
		t.Fatalf("code = %v, want RCodeRuntimeError", diag.CodeOf(err))
	}
}

// 参数缺失 → python TypeError → RCodeRuntimeError。
func TestPyRunMissingArg(t *testing.T) {
	requirePython(t)
	code := `def main(a: str, b: str) -> dict:
    return {"ok": a + b}`
	_, err := runPython(context.Background(), code, map[string]any{"a": "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if diag.CodeOf(err) != diag.RCodeRuntimeError {
		t.Fatalf("code = %v, want RCodeRuntimeError", diag.CodeOf(err))
	}
}

// 用户 print 输出不干扰结果 marker（marker 行取最后出现）。
func TestPyRunUserPrintIgnored(t *testing.T) {
	requirePython(t)
	code := `def main(v: str) -> dict:
    print("noise: " + v)
    print("__PYRUN_RESULT__:not-json")
    return {"result": v.upper()}`
	out, err := runPython(context.Background(), code, map[string]any{"v": "hi"})
	if err != nil {
		t.Fatalf("runPython: %v", err)
	}
	if out["result"] != "HI" {
		t.Fatalf("result = %#v, want HI", out["result"])
	}
}

// 超时:外部 ctx 更早截止 → RCodeTimeout（sleep 5s 但截止 200ms）。
func TestPyRunTimeout(t *testing.T) {
	requirePython(t)
	code := `import time
def main(v: str) -> dict:
    time.sleep(5)
    return {"v": v}`
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := runPython(ctx, code, map[string]any{"v": "x"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if diag.CodeOf(err) != diag.RCodeTimeout {
		t.Fatalf("code = %v, want RCodeTimeout", diag.CodeOf(err))
	}
}

// 非 python/javascript 语言仍在 Build 期 REJECT（语言面收窄到 python + javascript）。
func TestPyRunNonPythonLanguageRejected(t *testing.T) {
	b, _ := json.Marshal(&spec.CodeConfig{Code: "def main(v)\n  v\nend", Language: "ruby"})
	_, err := StdRegistry().Build(context.Background(),
		&spec.NodeSpec{ID: "c1", Kind: spec.KindCode, Config: b}, Deps{})
	if err == nil {
		t.Fatal("ruby must be rejected")
	}
	if diag.CodeOf(err) != diag.RCodeLanguageUnsupported {
		t.Fatalf("code = %v, want RCodeLanguageUnsupported", diag.CodeOf(err))
	}
}

// 用户 python 语言但系统无解释器 → Build 期 fail-fast（pythonAvailable
// 缓存探测;直接清缓存强制重探不可行,此路径由 code_e2e 覆盖主路径）。
func TestPyRunPythonAvailableCached(t *testing.T) {
	// 幂等性:多次调用返回一致结果（进程级缓存,不会反复 LookPath 变卦）。
	a, b := pythonAvailable(), pythonAvailable()
	if a != b {
		t.Fatalf("pythonAvailable unstable: %v vs %v", a, b)
	}
}

// coze 平台风格：async def main(args: Args)，输入经 args.params 访问。
// runner 智能适配：单参数名 "args" → 注入 _Args 包装 + asyncio 执行。
func TestPyRunArgsStyle(t *testing.T) {
	requirePython(t)
	code := `async def main(args: Args) -> Output:
    params = args.params
    return {"result": params["text"] + ":" + str(params["num"])}`
	out, err := runPython(context.Background(), code, map[string]any{
		"text": "x", "num": 7,
	})
	if err != nil {
		t.Fatalf("runPython: %v", err)
	}
	if out["result"] != "x:7" {
		t.Fatalf("result = %#v, want x:7", out["result"])
	}
}

// coze 平台风格（同步变体）:def main(args) → args.params。
func TestPyRunArgsStyleSync(t *testing.T) {
	requirePython(t)
	code := `def main(args):
    params = args.params
    return {"result": params["a"] + params["b"]}`
	out, err := runPython(context.Background(), code, map[string]any{
		"a": "1", "b": "2",
	})
	if err != nil {
		t.Fatalf("runPython: %v", err)
	}
	if out["result"] != "12" {
		t.Fatalf("result = %#v, want 12", out["result"])
	}
}

// coze async + 无参数（参数全走 args.params）。
func TestPyRunAsyncNoKwargs(t *testing.T) {
	requirePython(t)
	code := `async def main(args: Args):
    return {"ok": "yes"}`
	out, err := runPython(context.Background(), code, map[string]any{})
	if err != nil {
		t.Fatalf("runPython: %v", err)
	}
	if out["ok"] != "yes" {
		t.Fatalf("result = %#v, want yes", out["ok"])
	}
}

// coze-biz 风格:async def main(args: Dict) → args['params']（dict 下标,
// 语料实证 19 例——X53/X63/X70 等）。_Args 必须是 dict 子类。
func TestPyRunArgsSubscriptStyle(t *testing.T) {
	requirePython(t)
	code := `import json
from typing import Dict, Any

async def main(args: Dict[str, Any]) -> Dict[str, Any]:
    params = args['params']
    return {"result": params['text'] + ":" + str(params['num'])}`
	out, err := runPython(context.Background(), code, map[string]any{
		"text": "y", "num": 3,
	})
	if err != nil {
		t.Fatalf("runPython: %v", err)
	}
	if out["result"] != "y:3" {
		t.Fatalf("result = %#v, want y:3", out["result"])
	}
}

// TestPyRunEntropySeeded —— python 侧的**确定性契约**（载体二/二，与
// TestJSRunEntropySeeded 同一条规则：离线参考执行器的熵必须播种）。
//
// 语料 10 例的用户 python code 节点用 `random` 取随机，不播种会让 rt-l3 的
// SEMANTIC 面每次跑出不同数字。三条锁同 JS：同种子 ⇒ 逐字相同 / 换种子 ⇒
// 序列变 / off ⇒ 退回真随机。
func TestPyRunEntropySeeded(t *testing.T) {
	requirePython(t)
	code := `import random
def main() -> dict:
    return {"xs": [random.random() for _ in range(8)]}`

	run := func() []any {
		t.Helper()
		out, err := runPython(context.Background(), code, map[string]any{})
		if err != nil {
			t.Fatalf("runPython: %v", err)
		}
		xs, ok := out["xs"].([]any)
		if !ok || len(xs) != 8 {
			t.Fatalf("xs = %#v, want 8 项数组", out["xs"])
		}
		return xs
	}

	a := run()
	b := run()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("默认种子两次执行结果不同（熵未播种，门禁不可复现）：\n a=%v\n b=%v", a, b)
	}

	// 同上：字面量，保证撤实现后撞断言而不是构建失败。
	t.Setenv("GODCC_PY_SEED", "godcc-py-2")
	c := run()
	if reflect.DeepEqual(a, c) {
		t.Fatalf("换种子后序列不变 ⇒ 随机被桩成常量（不是播种）：%v", c)
	}

	t.Setenv("GODCC_PY_SEED", "off")
	d := run()
	e := run()
	if reflect.DeepEqual(d, e) {
		t.Fatalf("off 逃生门未恢复真随机（两次相同）：%v", d)
	}
}

// coze 单 dict 形参变体：async def main(inputs) 收到整个输入对象。
func TestPyRunSingleDictParam(t *testing.T) {
	requirePython(t)
	code := `def main(inputs) -> dict:
    return {"out": inputs.get("a", "") + inputs.get("b", "")}`
	out, err := runPython(context.Background(), code, map[string]any{"a": "x", "b": "y"})
	if err != nil {
		t.Fatalf("runPython: %v", err)
	}
	if out["out"] != "xy" {
		t.Fatalf("out = %#v, want xy", out["out"])
	}
}
