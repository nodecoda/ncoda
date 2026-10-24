package nodes

// jrun.go — coze 平台 JavaScript code 节点子进程执行器（M17 对称扩展）。
//
// coze 平台 JS code 模板是 TypeScript 形态：
//
//	async function main({ params }: Args): Promise<Output> {
//	    return { ... };
//	}
//
// v1 执行路径：
//  1. 轻量 TS 剥离（只处理 main 签名层的类型注解——`{ params }: Args` →
//     `{ params }`、`): Promise<Output>` → `)`），不碰函数体/字符串内容；
//     语料实证 293/318（92%）是同一模板，剥离规则覆盖主流形态。
//  2. node 子进程执行（`node -` stdin），输入 JSON 经环境变量注入，
//     runner 按 main 形参形态适配调用（`{ params }` 解构 → 包 params；
//     其余形参名 → 直接传参数对象），async 结果 await 后 marker 行回传。
//  3. 结果 dict 走 normalizeCodeOutput（与 python 同输出契约）。
//
// 与 pyrun.go 同构：Build 期探测 node（缺失 → RCodeLanguageUnsupported），
// Invoke 期 `node -` 沙箱执行，超时/异常 → RCodeTimeout/RCodeRuntimeError。

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nodecoda/ncoda/pkg/codeparse"
	"github.com/nodecoda/ncoda/pkg/runtime/diag"
)

// jsRunTimeout 用户 JS 节点的默认执行超时（与 python 同档）。
const jsRunTimeout = 30 * time.Second

// jsRunResultMarker runner 输出结果行的行首 marker。
const jsRunResultMarker = "__JSRUN_RESULT__:"

// maxJSErrLen 截断 node 异常文本的字节上限。
const maxJSErrLen = 4096

// jsSeedEnv / jsDefaultSeed —— 离线参考执行的**熵播种**（2026-09-20）。
//
// 判据（一条，无形态枚举）：参考执行器对同一输入必须给出同一结果 ——
// 否则门禁的数字每次跑都不一样，判据就不可复现，也就不是判据。
//
// 其余外部依赖早已是确定性桩（echo 模型 / stub HTTP / stub knowledge），
// 唯余 `Math.random` 一处**无界熵**。第一手证据：语料 18/672 例的 code
// 节点直接用它选下标（`arr[Math.floor(Math.random()*arr.length)]`，越界
// 与否随抽取结果摆动）⇒ 单例连跑 8 次出现 pass/run-err 翻转
// （X9_Wgongzhonghao_word_1：leg1_nonpass 0/1 交替），
// 全量 672 例两次跑出 match=535 / 534。
//
// 一条规则、两处载体（本文件 + pyrun.go）：**播种，不桩死** —— 序列仍是
// PRNG（用户的随机逻辑照旧执行），只是种子固定；`GODCC_JS_SEED=off`
// 恢复平台真随机（需要真随机行为时用，例如人工复现线上抖动）。
const (
	jsSeedEnv     = "GODCC_JS_SEED"
	jsDefaultSeed = "godcc-js-1"
)

// jsEntropyPrelude 拼在**用户代码之前**：把 Math.random 换成固定种子的
// xorshift32。必须在前，才能覆盖用户代码的调用点；用户代码若自己显式
// 覆盖 Math.random，则以它为准（那是程序的显式行为，不是熵源）。
const jsEntropyPrelude = `
(() => {
  const seedStr = process.env.GODCC_JS_SEED || 'godcc-js-1';
  if (seedStr === 'off') return;
  let h = 2166136261 >>> 0;
  for (let i = 0; i < seedStr.length; i++) { h ^= seedStr.charCodeAt(i); h = Math.imul(h, 16777619) >>> 0; }
  let s = h || 1;
  Math.random = function () {
    s ^= s << 13; s >>>= 0;
    s ^= s >>> 17;
    s ^= s << 5; s >>>= 0;
    return s / 4294967296;
  };
})();
`

// jsSeed 返回本次执行的熵种子：环境未设 ⇒ 固定默认（确定性契约）。
func jsSeed() string {
	if v := os.Getenv(jsSeedEnv); v != "" {
		return v
	}
	return jsDefaultSeed
}

// jsRunner 拼接在用户代码后的 runner：按 main 形参形态调用 + marker 输出。
// 形参适配：`main({ params })` 解构 → 包 params；其余（input/args/params 等）
// → 直接传整个参数对象（coze 变体语义）。
const jsRunner = `
(async () => {
  try {
    const raw = process.env.JSRUN_INPUT || '{}';
    const kwargs = JSON.parse(raw || '{}');
    const src = main.toString();
    let out;
    // 形参适配（coze 变体）：
    //   main({ params })                    → 解构，注入 { params }
    //   main(input) 且体内 input.params /    → 平台 Args 包装，注入 { params }
    //     const { params } = input
    //   其余单形参                            → 直接传输入对象
    const destructuredParams = /main\s*\(\s*\{[^}]*\bparams\b[^}]*\}\s*\)/.test(src);
    const single = /main\s*\(\s*([A-Za-z_$][\w$]*)\s*\)/.exec(src);
    let usesParamWrapper = false;
    if (single) {
      const n = single[1];
      usesParamWrapper = new RegExp('\\b' + n + '\\.params\\b').test(src) ||
        new RegExp('\\{[^}]*\\bparams\\b[^}]*\\}\\s*=\\s*' + n + '\\b').test(src);
    }
    if (destructuredParams || usesParamWrapper) {
      out = await main({ params: kwargs });
    } else {
      out = await main(kwargs);
    }
    // 裸返回值（数组/标量）也是合法 coze 形态:数组等对象直接输出,
    // 由 Go 侧 codeNode.Invoke 映射到唯一声明输出名;null/标量拒绝。
    if (out === null || (typeof out !== 'object' && typeof out !== 'string' && typeof out !== 'number' && typeof out !== 'boolean')) {
      throw new TypeError('main() must return an object or array, got ' + typeof out);
    }
    console.log('__JSRUN_RESULT__:' + JSON.stringify(out));
  } catch (e) {
    console.error((e && e.name || 'Error') + ': ' + (e && e.message || String(e)));
    process.exit(1);
  }
})();
`

var (
	jsOnce   sync.Once
	jsFound  bool
	jsErrMsg string
)

// nodeAvailable 探测一次 node 可执行文件（Build 期 fail-fast 用）。
func nodeAvailable() bool {
	jsOnce.Do(func() {
		p, err := exec.LookPath("node")
		if err != nil {
			jsErrMsg = "node executable not found on PATH"
			return
		}
		if fi, serr := os.Stat(p); serr != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
			jsErrMsg = p + " is not executable"
			return
		}
		jsFound = true
	})
	return jsFound
}

// stripJSTypes 是 codeparse.StripJSTypes 的包内别名：剥离规则只有一份
// （internal/codeparse），runtime 执行与加载期预检共用。
func stripJSTypes(code string) string { return codeparse.StripJSTypes(code) }

// runJavaScript 执行用户 JS/TS 源码并返回结果 dict。
func runJavaScript(ctx context.Context, code string, in map[string]any, root string) (any, error) {
	inputJSON, merr := json.Marshal(in)
	if merr != nil {
		return nil, diag.New(diag.RCodeRuntimeError, "code: encode javascript inputs: %v", merr)
	}
	// 预算来源（一处定义 = code.go::declaredCodeTimeout / codeTimeoutMessage）：
	//   有截止 ⇒ 那**就是**预算（= 节点声明的 settingOnError.timeoutMs，或图级更早的
	//   截止）；**不得再被安全网截短** —— 平台值域 60s–600s 比安全网长，截短就是用
	//   我们的常量替平台的值（2026-09-29 修掉的缺陷：llm/http/tool/agent 都读声明，
	//   只有 code 不读）。
	//   没有 ⇒ 平台未声明（平台语义「不超时」）⇒ 退到安全网，且诊断**标出来**
	//   （stubBudget），否则分诊会把桩自加的预算记成「平台超时」。
	timeout := jsRunTimeout
	stubBudget := true
	if dl, ok := ctx.Deadline(); ok {
		timeout = time.Until(dl)
		stubBudget = false
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	script := jsEntropyPrelude + "\n" + stripJSTypes(code) + "\n\n" + jsRunner
	cmd := exec.CommandContext(cctx, "node", "-")
	env := []string{"JSRUN_INPUT=" + string(inputJSON), jsSeedEnv + "=" + jsSeed()}
	if root != "" {
		// Bare specifiers resolve from the process cwd for `node -`; the
		// module root is what makes node_modules visible. NODE_PATH is kept
		// for CJS require() (ESM ignores it).
		cmd.Dir = root
		env = append(env, "NODE_PATH="+filepath.Join(root, "node_modules"))
	}
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			budget, _ := codeTimeoutBudget(ctx, timeout)
			return nil, diag.New(diag.RCodeTimeout, "%s",
				codeTimeoutMessage("javascript", budget, stubBudget))
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if len(msg) > maxJSErrLen {
			msg = msg[:maxJSErrLen] + " …(truncated)"
		}
		return nil, diag.New(diag.RCodeRuntimeError, "code: javascript execution failed: %s", msg)
	}
	all := stdout.String()
	idx := strings.LastIndex(all, jsRunResultMarker)
	if idx < 0 {
		return nil, diag.New(diag.RCodeRuntimeError,
			"code: javascript produced no result marker (main() must return an object); stdout=%q",
			truncateStr(all, 512))
	}
	raw := strings.TrimSpace(all[idx+len(jsRunResultMarker):])
	var out any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, diag.New(diag.RCodeRuntimeError,
			"code: javascript result is not valid JSON: %v", err)
	}
	return out, nil
}
