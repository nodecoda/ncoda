package nodes

// pyrun.go — 用户 foreign code python3() 的子进程执行器（M17）。
//
// 语义（对齐参考目标 code 节点:平台在沙箱中执行用户 python 源码）:
//   - Build 期:语言必须为 "python"（IR 对 `foreign code python3(...)` 固定
//     产出 Language="python",见 pkg/ir/build.go buildCode）;探测
//     python3 可执行文件（exec.LookPath）,缺失 → RCodeLanguageUnsupported
//     （错误码语义从 D4 的"语言不支持"迁移为"运行时无解释器"）。
//   - Invoke 期:启动 `python3 -I -`（isolated 模式:忽略 PYTHONPATH /
//     user site / 环境导入）,用户源码 + runner 经 stdin 喂入;输入参数
//     JSON 序列化后经环境变量 PYRUN_INPUT 注入（key = main 形参名）;
//     runner 以 main(**kwargs) 调用,结果 dict JSON 序列化后以
//     `__PYRUN_RESULT__:<json>` 单行 marker 写到 stdout。
//   - 错误传播:python 异常 / 非 dict 返回 → traceback 到 stderr + 非零
//     退出 → RCodeRuntimeError（异常文本截断 4KB）;stdout 无 marker →
//     RCodeRuntimeError。
//   - 超时:默认 defaultPyRunTimeout（外部 ctx 更早截止则随之外部截止）→
//     RCodeTimeout。
//   - 沙箱边界:v1 仅 isolated 模式 + 超时,不隔离网络/文件系统 —— 与
//     参考目标 本地开发环境运行 code 节点同等风险,见 docs/RELEASE-v0.4.0.md。
//   - 输出契约:与 synthetic shim 求值器一致（单 key dict 补 "output"
//     端口、多 key dict "output" = 整体拷贝,M14 双端口语义）,由
//     normalizeCodeOutput 统一处理。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
)

// defaultPyRunTimeout 是用户 python 节点的默认执行超时（图未携带更早截止时）。
const defaultPyRunTimeout = 30 * time.Second

// pyRunInputEnvMax 是 PYRUN_INPUT env 注入的上限阈值:单个 env 字符串受
// MAX_ARG_STRLEN（128KB）限制,超出会 E2BIG（"argument list too long"）。
// 大输入改走 PYRUN_INPUT_FILE 临时文件注入,规避 exec 上限。
const pyRunInputEnvMax = 100 * 1024

// pyrunResultMarker 是 runner 输出结果行的行首 marker（与用户 print 输出区分）。
const pyrunResultMarker = "__PYRUN_RESULT__:"

// maxPyErrLen 截断 python 异常文本的字节上限（防 stderr 爆炸打爆日志）。
const maxPyErrLen = 4096

// pyRunner 是拼接在用户源码之后的 runner:wrapper 调 main(**kwargs) 并
// 以 marker 行输出结果 dict。用户源码 + 空行 + runner 整体作为脚本执行
// （与 参考目标 沙箱"执行整个 code 节点源码"同构）。
const pyRunner = `
import asyncio, inspect, json, os, sys

if __name__ == "__main__":
    try:
        raw = os.environ.get("PYRUN_INPUT", "")
        if not raw:
            p = os.environ.get("PYRUN_INPUT_FILE", "")
            if p:
                with open(p, "r", encoding="utf-8") as f:
                    raw = f.read()
                try:
                    os.remove(p)
                except OSError:
                    pass
        kwargs = json.loads(raw) if raw.strip() else {}
        # coze 平台风格适配：单形参名 "args"（async def main(args: Args)）时
        # 输入经 args.params 访问（平台注入 Args 类型）；否则 参考目标 风格
        # main(**kwargs) 直接展开。async 结果经 asyncio.run 驱动。
        sig = inspect.signature(main)
        params = list(sig.parameters.values())
        pnames = [p.name for p in params]
        positional = [p for p in params if p.kind in (p.POSITIONAL_ONLY, p.POSITIONAL_OR_KEYWORD)]
        single_dict = (len(params) == 1 and len(positional) == 1
                       and pnames[0] != "args" and pnames[0] not in kwargs)
        if len(pnames) == 1 and pnames[0] == "args":
            # coze-biz 风格 args['params']（dict 下标）与 coze-studio 风格
            # args.params（属性）双兼容:dict 子类 + params property +
            # __getattr__（args.xxx 兜底属性访问）。语料实证两套写法
            # （X53/X63/X70 等 19 例:async def main(args: Dict) → args['params']）。
            class _Args(dict):
                @property
                def params(self):
                    return self["params"]
                def __getattr__(self, name):
                    try:
                        return self[name]
                    except KeyError:
                        raise AttributeError(name) from None
            out = main(_Args(params=kwargs))
        elif single_dict:
            # coze 单 dict 形参变体：main(inputs) / main(params) / main(data)。
            # 形参名不在输入键里 ⇒ 平台语义是「把整个输入对象交给 main」，
            # 不能展开成 main(**kwargs)（会 TypeError: missing argument）。
            out = main(kwargs)
        else:
            out = main(**kwargs)
        if inspect.iscoroutine(out):
            out = asyncio.run(out)
        if not isinstance(out, dict):
            raise TypeError("main() must return a dict, got %s" % type(out).__name__)
        sys.stdout.write("__PYRUN_RESULT__:" + json.dumps(out, ensure_ascii=False) + "\n")
        sys.stdout.flush()
    except BaseException:
        sys.stderr.write(sys.exc_info()[1].__class__.__name__ + ": " + str(sys.exc_info()[1]) + "\n")
        sys.exit(1)
`

// pyTypeShims 在用户源码前注入 coze 平台 code 节点依赖的桩类型：
// Args（main 参数容器，runner 填 .params）/ Output（返回注解，运行时未
// 强制结构——与 coze 沙箱一致只作注解标记）。参考目标 风格 main(**kwargs)
// 不受影响。
// pySeedEnv / pyDefaultSeed —— python 侧同一条熵播种规则（载体二/二，
// 与 jrun.go 的 jsSeedEnv 同源：**离线参考执行器的熵必须播种**）。
//
// 语料 10 例的用户 python code 节点用 `random` 取随机（`random.choice` /
// `random.randint` 等），不播种同样让 rt-l3 的 SEMANTIC 面每次跑出不同数字。
// `GODCC_PY_SEED=off` 恢复真随机。
const (
	pySeedEnv     = "GODCC_PY_SEED"
	pyDefaultSeed = "godcc-py-1"
)

// pyEntropyPrelude 注入在**用户源码之前**（紧随桩类型之后）：按种子播种
// 标准库 `random` 的全局状态。用户代码若自己调 random.seed(...) 覆盖，
// 以其为准（显式行为优先）。
const pyEntropyPrelude = `
import os as _godcc_os, random as _godcc_random
_godcc_seed = _godcc_os.environ.get("GODCC_PY_SEED", "godcc-py-1")
if _godcc_seed != "off":
    try:
        _godcc_random.seed(int(_godcc_seed))
    except ValueError:
        _godcc_random.seed(_godcc_seed)

`

// pySeed 返回本次执行的熵种子：环境未设 ⇒ 固定默认（确定性契约）。
func pySeed() string {
	if v := os.Getenv(pySeedEnv); v != "" {
		return v
	}
	return pyDefaultSeed
}

const pyTypeShims = `
class Args:
    pass

class Output:
    pass

`

var (
	pyOnce   sync.Once
	pyFound  bool
	pyErrMsg string
)

// pythonAvailable 探测一次 python3 可执行文件（Build 期 fail-fast 用）。
// 结果进程级缓存:运行时平台假设解释器版本在进程生命周期内不变。
func pythonAvailable() bool {
	pyOnce.Do(func() {
		p, err := exec.LookPath("python3")
		if err != nil {
			pyErrMsg = err.Error()
			return
		}
		// 顺带验证可执行（LookPath 可能命中不可执行文件）。
		if fi, serr := os.Stat(p); serr != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
			pyErrMsg = fmt.Sprintf("%s is not executable", p)
			return
		}
		pyFound = true
	})
	return pyFound
}

// runPython executes user python source against the assembled inputs and
// returns the result dict. Errors are wrapped runtime diagnostics.
func runPython(ctx context.Context, code string, in map[string]any) (map[string]any, error) {
	inputJSON, merr := json.Marshal(in)
	if merr != nil {
		return nil, diag.New(diag.RCodeRuntimeError, "code: encode python inputs: %v", merr)
	}
	// 预算来源（一处定义 = code.go::declaredCodeTimeout / codeTimeoutMessage）：
	//   有截止 ⇒ 那**就是**预算（= 节点声明的 settingOnError.timeoutMs，或图级更早的
	//   截止）；**不得再被安全网截短** —— 平台值域 60s–600s 比安全网长，截短就是用
	//   我们的常量替平台的值（2026-09-29 修掉的缺陷：llm/http/tool/agent 都读声明，
	//   只有 code 不读）。
	//   没有 ⇒ 平台未声明（平台语义「不超时」）⇒ 退到安全网，且诊断**标出来**
	//   （stubBudget），否则分诊会把桩自加的预算记成「平台超时」。
	timeout := defaultPyRunTimeout
	stubBudget := true
	if dl, ok := ctx.Deadline(); ok {
		timeout = time.Until(dl)
		stubBudget = false
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// coze 平台 code 节点约定 async def main(args: Args) -> Output 的注解
	// Args/Output 在 def 语句求值时就必须存在——在用户源码前注入桩类型
	//（runner 的 _Args 适配在 __main__ 里构造 args.params）。
	script := pyTypeShims + pyEntropyPrelude + code + "\n\n" + pyRunner
	cmd := exec.CommandContext(cctx, "python3", "-I", "-")
	if len(inputJSON) > pyRunInputEnvMax {
		// 大输入走临时文件:env 单串 128KB 上限（MAX_ARG_STRLEN）会 E2BIG。
		// python 侧 runner 读后自删;Go 侧 defer 兜底（remove 幂等）。
		f, cerr := os.CreateTemp("", "pyrun-input-*.json")
		if cerr != nil {
			return nil, diag.New(diag.RCodeRuntimeError, "code: create input temp file: %v", cerr)
		}
		if _, werr := f.Write(inputJSON); werr != nil {
			f.Close()
			os.Remove(f.Name())
			return nil, diag.New(diag.RCodeRuntimeError, "code: write input temp file: %v", werr)
		}
		if ferr := f.Close(); ferr != nil {
			os.Remove(f.Name())
			return nil, diag.New(diag.RCodeRuntimeError, "code: close input temp file: %v", ferr)
		}
		defer os.Remove(f.Name())
		cmd.Env = append(os.Environ(), "PYRUN_INPUT_FILE="+f.Name(), pySeedEnv+"="+pySeed())
	} else {
		cmd.Env = append(os.Environ(), "PYRUN_INPUT="+string(inputJSON), pySeedEnv+"="+pySeed())
	}
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			budget, _ := codeTimeoutBudget(ctx, timeout)
			return nil, diag.New(diag.RCodeTimeout, "%s",
				codeTimeoutMessage("python", budget, stubBudget))
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if len(msg) > maxPyErrLen {
			msg = msg[:maxPyErrLen] + " …(truncated)"
		}
		return nil, diag.New(diag.RCodeRuntimeError, "code: python execution failed: %s", msg)
	}
	all := stdout.String()
	idx := strings.LastIndex(all, pyrunResultMarker)
	if idx < 0 {
		return nil, diag.New(diag.RCodeRuntimeError,
			"code: python produced no result marker (main() must return a dict); stdout=%q", truncateStr(all, 512))
	}
	line := strings.TrimSpace(all[idx+len(pyrunResultMarker):])
	var out map[string]any
	if uerr := json.Unmarshal([]byte(line), &out); uerr != nil {
		return nil, diag.New(diag.RCodeRuntimeError,
			"code: python result is not a JSON dict: %v (line=%q)", uerr, truncateStr(line, 512))
	}
	return out, nil
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// normalizeCodeOutput 应用 M14 双端口契约:keepWhole=false 时单 key dict
// 补 "output" 端口 = 该值（旧语义,用户 python / json.parse<array<T>> shim）;
// keepWhole=true（json.parse<Record> shim）或多 key dict 时 "output" =
// 整体 dict 拷贝 —— 否则单字段 record 的 output.<field> dotted ref 会因
// 层级丢失而断链。注意:record 字段名不可能为 "output"（ncoda 保留字,
// 解析期拒绝,见 parser "Expected field name, got KW_OUTPUT"）,因此
// keepWhole 路径不存在"单 key 恰为 output 时被短路"的例外。
func normalizeCodeOutput(out map[string]any, keepWhole bool) map[string]any {
	if len(out) == 1 && !keepWhole {
		for k, v := range out {
			if k != "output" {
				out["output"] = v
			}
		}
	} else if _, has := out["output"]; !has && len(out) > 0 {
		clone := make(map[string]any, len(out))
		for k, v := range out {
			clone[k] = v
		}
		out["output"] = clone
	}
	return out
}
