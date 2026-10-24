package nodes

// code.go — code 节点正式实现（M2.1 骨架，M4 落地求值器，M17 解锁用户 python）。
//
// 双路径语义:
//   - Synthetic shim（IR 编译器生成的受限 Python 子集）→ M4 表达式求值器
//     （expr.EvalShim）。Build 期先解析 shim（fail-fast:语法不支持 →
//     RExpressionUnsupported,编译期即报错）;Invoke 期对装配输入求值,
//     返回 dict（key = 输出端口名）。
//   - 用户 foreign code（!Synthetic）→ 子进程执行器（pyrun.go / jrun.go）。
//     语言面 = python + javascript（coze-biz 语料实证两种），其余语言
//     → RCodeLanguageUnsupported。Build 期 fail-fast：宿主解释器缺失
//     （python3 / node 不在 PATH）→ RCodeLanguageUnsupported；
//     javascript 的裸模块 import 解析不到 → RCodeModuleUnresolved，
//     与调用分支是否执行无关（见 jsimports.go）。Invoke 期以
//     `python3 -I -` / node 沙箱执行，参数 JSON 注入、结果 marker 行回传。
//
// 输出契约:结果 dict 的 key 直接作为命名输出端口（OutOrder 声明顺序）。
// M14 对齐 coze code 节点双端口语义（platform code nodes always expose the
// default "output" port IN ADDITION to one port per return key）:
//   - 单 key dict（如 {"result": v}）→ 额外补 "output" 端口 = 该值
//   - 多 key dict（json.parse<T> 字段提取）→ "output" 端口 = 整体 dict 的
//     拷贝,使 "output.<field>" dotted ref 经 lookupDotted 命中字段。
// 统一由 normalizeCodeOutput 处理（shim / 子进程共用）。

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nodecoda/ncoda/pkg/expr"
	"github.com/nodecoda/ncoda/pkg/runtime/boundary"
	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/trace"
)

// Code executes embedded code: synthetic shims via the M4 evaluator, or user
// foreign code (python / javascript) via the subprocess executors.
type Code struct{}

func (Code) Kind() string { return spec.KindCode }

func (Code) Build(ctx context.Context, n *spec.NodeSpec, deps Deps) (NodeImpl, error) {
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	cc := cfg.(*spec.CodeConfig)
	// P1a-2: the execution-boundary policy is a per-node projection. It is
	// loaded through the production constructor so a variant/missing fixture
	// fails at the consumer, not in a test-only copy (ES-071/ES-090).
	pol, err := boundary.ProductionPolicy()
	if err != nil {
		return nil, diag.New(diag.RSpecInvalid, "node %s: execution policy: %v", n.ID, err)
	}
	vb := boundary.New(boundary.Options{
		Policy: pol,
		NodeID: n.ID,
		Kind:   spec.KindCode,
		Emit:   deps.SemTrace,
	})
	if !cc.Synthetic {
		// M17: 用户 foreign code python3() / javascript 解锁（子进程执行器
		// pyrun.go / jrun.go）。语言面收窄到 python + javascript（coze-biz
		// 语料实证两种语言）;解释器缺失在 Build 期 fail-fast。
		switch cc.Language {
		case "python":
			if !pythonAvailable() {
				return nil, diag.New(diag.RCodeLanguageUnsupported,
					"node %s: user python requires a python3 interpreter on PATH (%s)", n.ID, pyErrMsg)
			}
			return &codeNode{cfg: cc, py: true, id: n.ID, semTrace: deps.SemTrace, b: vb}, nil
		case "javascript":
			if !nodeAvailable() {
				return nil, diag.New(diag.RCodeLanguageUnsupported,
					"node %s: user javascript requires a node executable on PATH (%s)", n.ID, jsErrMsg)
			}
			// Load-time dependency contract (jsimports.go): a bare specifier
			// can only resolve when the runtime has a module root, so fail
			// before execution instead of surfacing a node stack from inside
			// the node — and regardless of whether the calling branch runs.
			root := normalizeNodeRoot(deps.NodeModules)
			if root != "" {
				if verr := validateNodeRoot(root); verr != nil {
					return nil, diag.New(diag.RCodeModuleUnresolved,
						"node %s: --node-modules %q is not a usable directory: %v", n.ID, deps.NodeModules, verr)
				}
			}
			mods := scanJSBareImports(cc.Code)
			if missing := missingNodeModules(root, mods); len(missing) > 0 {
				hint := "remove the import or pass --node-modules <dir> containing node_modules"
				return nil, diag.New(diag.RCodeModuleUnresolved,
					"node %s: javascript imports module(s) the runtime does not provide: %s (%s)",
					n.ID, strings.Join(missing, ", "), hint)
			}
			// Only re-root the process when a bare specifier actually needs it:
			// changing cwd also changes how relative paths resolve.
			jsRoot := root
			if len(mods) == 0 {
				jsRoot = ""
			}
			return &codeNode{cfg: cc, js: true, jsRoot: jsRoot, id: n.ID, semTrace: deps.SemTrace, b: vb}, nil
		default:
			return nil, diag.New(diag.RCodeLanguageUnsupported,
				"node %s: foreign code language %q is not supported (v1 supports python, javascript)", n.ID, cc.Language)
		}
	}
	// fail-fast: 求值器只保证编译器生成的封闭子集。Build 期解析,不支持的
	// 构造在编译期报 RExpressionUnsupported,而不是运行到一半才失败。
	if _, err := expr.ParseShim(cc.Code); err != nil {
		return nil, diag.New(diag.RExpressionUnsupported,
			"node %s: synthetic shim outside the evaluator subset: %v", n.ID, err)
	}
	return &codeNode{cfg: cc, id: n.ID, semTrace: deps.SemTrace, b: vb}, nil
}

type codeNode struct {
	cfg      *spec.CodeConfig
	py       bool   // 用户 python:子进程执行器路径（M17）
	js       bool   // 用户 javascript:子进程执行器路径（jrun.go）
	jsRoot   string // 模块根（--node-modules;空 = 无外部模块）
	id       string // spec node id（trace evidence 的 join key）
	semTrace func(trace.Record)
	b        *boundary.ValueBoundary // single value boundary (P1a-2)
}

// emitSemTrace emits the P1a-1 minimum timeout-decision record. It is a
// no-op when tracing is disabled, so closed tracing changes nothing.
func (c *codeNode) emitSemTrace() {
	if c.semTrace == nil {
		return
	}
	origin := trace.OriginWireAbsent
	if c.cfg.Exception != nil {
		origin = trace.OriginWirePresent
	}
	id := c.id
	if id == "" {
		id = "?"
	}
	deadline := int(declaredCodeTimeout(c.cfg) / time.Millisecond)
	c.semTrace(trace.Record{
		Stage:      "timeout",
		RuleID:     trace.RuleTimeout,
		Origin:     origin,
		Evidence:   "observed:provenance:nodes." + id + ".config.exception.timeout_ms",
		DeadlineMS: deadline,
		Node:       id,
		Kind:       spec.KindCode,
		FillRule:   "timeout_axis",
		Attempt:    1,
		Axis:       "total",
		Input:      "config.exception.timeout_ms",
		Output:     strconv.Itoa(deadline),
	})
}

// emitRetryTrace records one attempt of a declared retry policy. Attempt is
// 1-based (schema minimum 1); axis=total because code's settingOnError timeout
// is shared across attempts.
func (c *codeNode) emitRetryTrace(attempt int) {
	if c.semTrace == nil {
		return
	}
	id := c.id
	if id == "" {
		id = "?"
	}
	origin := trace.OriginWireAbsent
	if c.cfg.Exception != nil {
		origin = trace.OriginWirePresent
	}
	c.semTrace(trace.Record{
		Stage:      "retry",
		RuleID:     trace.RuleRetry,
		Origin:     origin,
		Evidence:   "observed:p1a2:nodes." + id + ".retry",
		DeadlineMS: int(declaredCodeTimeout(c.cfg) / time.Millisecond),
		Node:       id,
		Kind:       spec.KindCode,
		FillRule:   "retry_attempt",
		Attempt:    attempt,
		Axis:       "total",
		Input:      "config.exception.retry_times",
		Output:     strconv.Itoa(attempt),
	})
}

func (c *codeNode) Invoke(ctx context.Context, in map[string]any) (map[string]any, error) {
	// P1a-1: single minimal timeout trace emission point.
	c.emitSemTrace()
	// P1a-2: the single ValueBoundary handles the former M20 shaping plus the
	// execution-boundary fill: resolve → coerce → fill(required check inside).
	// Containers publish null (not []/{}); scalar zeros are filled. Undeclared
	// keys pass through; identical declarations are a no-op.
	if c.b != nil {
		filled, ferr := c.b.FillInputs(in, c.cfg.Types, 1)
		if ferr != nil {
			return nil, diag.New(diag.RInputMissing, "node %s: %v", c.id, ferr)
		}
		in = filled
	}
	// 节点自带 `settingOnError.timeoutMs` 下发成 ctx 截止（**唯一**下发点；
	// 子进程执行器按「有截止 ⇒ 它就是预算」消费，见 pyrun.go / jrun.go）。
	if ms := declaredCodeTimeout(c.cfg); ms > 0 {
		// 记录**声明预算**供诊断自报（唯一记录点）。诊断要报的必须是**声明的**
		// 值，不是执行器到达时的残差 —— `time.Until(deadline)` 会少掉几毫秒，
		// 让 `timeout(1s)` 印成 `998ms`（判据 `TestCodeNodeDeclaredTimeoutIsEnforced`
		// 因此时红时绿）。实际超时**仍**按下面的 ctx 截止（保「跨重试共享预算」语义）。
		budget := ms
		if dl, ok := ctx.Deadline(); ok {
			if rem := time.Until(dl); rem < budget {
				budget = rem // 图级更早的截止更紧 ⇒ 它才是生效预算
			}
		}
		ctx = context.WithValue(ctx, codeBudgetKey{}, budget)
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, ms)
		defer cancel()
	}
	// 同一 stanza 的**另一轴**：`settingOnError.retryTimes`。llm / http / tool /
	// agent 四处都在体内读 `Exception.MaxRetry`，code 此前是**唯一**不读的 ⇒
	// 作者写的 `with retry(max: N)` 编译侧照发（发射点
	// `internal/backend/cozebiz/render_node.go::errorPolicySetting`）、运行期却只
	// 跑一次 = 「一轴两处两种行为」。量级一手（只读语料 322 份 yaml）：`retryTimes`
	// 非零 **105** 处 / 6573（1×65 · 3×35 · 2×5）。
	//
	// 预算语义（**跨尝试共享**，一手 coze-studio OSS `vo/canvas.go` 的
	// SettingOnError.TimeoutMs 注释：「If any retry happens, ALL retry attempts
	// accumulates to the same timeout threshold」）⇒ 上面那份截止设在循环
	// **外面**，且预算耗尽后不再起新尝试（再试只会立刻再失败）。
	//
	// `RetryIntervalMS` **不读**（不是漏）：coze 家族的产物里没有该轴
	// （`residuals.yaml#R-90`）⇒ 平台不会等，我们多睡一觉反而是发明语义。
	retries := declaredCodeRetries(c.cfg)
	var outRaw any
	var err error
	successAttempt := 1
	for attempt := 1; attempt <= retries+1; attempt++ {
		if attempt > 1 && ctx.Err() != nil {
			break
		}
		// ES-023a: retry records carry the 1-based attempt and the shared
		// total axis. Emitted only when retries are declared, so the
		// single-attempt trace stays the P1a-1 minimal record.
		if retries > 0 {
			c.emitRetryTrace(attempt)
		}
		outRaw, err = c.evalOnce(ctx, in)
		if err == nil {
			successAttempt = attempt
			break
		}
	}
	if err != nil {
		// ES-046: the declared failure policy decides what downstream sees.
		// throw keeps the original execution diagnostic; return publishes the
		// carried fallback payload; error-branch transfers control.
		if payload, absorbed, perr := applyErrorPolicy(c.cfg.Exception, c.id, nodeFailure{Kind: spec.KindCode, Err: err}); perr != nil {
			return nil, perr
		} else if absorbed {
			return payload, nil
		}
		return nil, err
	}
	out, ok := outRaw.(map[string]any)
	if !ok {
		// 裸返回值（coze JS 常见:return ret; 而声明输出为 ret:array<T>）:
		// 平台语义 = 映射到唯一声明输出端口（语料实证 X170/X97 等）;
		// 无声明时退化为 "output"。
		name := "output"
		if len(c.cfg.OutOrder) == 1 {
			name = c.cfg.OutOrder[0]
		}
		out = map[string]any{name: outRaw}
	}
	out = normalizeCodeOutput(out, c.cfg.KeepWhole)
	if c.b != nil {
		out = c.b.PostFillOutputs(out, c.cfg.OutTypes, c.cfg.OutOrder, successAttempt)
	}
	return out, nil
}

// evalOnce 执行**一次**（重试循环的体）。三路执行器的**诊断拼写原样保留**：
// 这些串是分诊的输入（`cmd/rt-l3` 的 stub-value 签名 = `shim evaluation
// failed: …`、预算超时的 `RCODE_TIMEOUT`），加重试不是改诊断的理由。
func (c *codeNode) evalOnce(ctx context.Context, in map[string]any) (any, error) {
	switch {
	case c.py:
		return runPython(ctx, c.cfg.Code, in)
	case c.js:
		return runJavaScript(ctx, c.cfg.Code, in, c.jsRoot)
	default:
		out, err := expr.EvalShim(c.cfg.Code, in)
		if err != nil {
			return nil, diag.New(diag.RExpressionType,
				"code %s: shim evaluation failed: %v", c.cfg.Code, err)
		}
		return out, nil
	}
}

// ShapeAbsorbsNil reports whether a nil value is converted to a non-nil value
// by the value boundary. P1a-2 flipped container semantics: list/object zero is
// published as null, so those kinds do NOT absorb nil anymore. The single
// definition lives in internal/runtime/boundary (ES-017a/ES-077); this symbol
// is only the triage-facing alias.
func ShapeAbsorbsNil(kind string) bool { return boundary.AbsorbsNil(kind) }

// declaredCodeTimeout 读节点自带 `settingOnError.timeoutMs`，经
// `ir.CodeRun.Exception` → `spec.CodeConfig.Exception` 到达运行期。
//
// 一手（2026-09-29）：平台导出 6622 处 `timeoutMs`（值域 180000 / 60000 /
// 600000 / 300000 / …）；**编译侧本来就对**（`with timeout(300s)` ⇒ 产物
// `settingOnError.timeoutMs: 300000`）—— 丢的只在 IR→spec→执行这一跳，
// 而 llm / http / tool / agent 各自都在体内读同一个字段（同一事实，四处同解）。
//
// 0 / nil ⇒ 平台**没有**声明（平台语义 = 不超时，`node_runner.go:547`
// `if cfg.timeoutMS > 0`），由子进程执行器的安全网兜底并**标注**出来。
func declaredCodeTimeout(cc *spec.CodeConfig) time.Duration {
	if cc == nil || cc.Exception == nil {
		return 0
	}
	return time.Duration(cc.Exception.TimeoutMS) * time.Millisecond
}

// declaredCodeRetries 读同一 stanza（`settingOnError`）的 `retryTimes` 轴，
// 经 `ir.CodeRun.Exception` → `spec.CodeConfig.Exception` 到达运行期（与
// `declaredCodeTimeout` 同源同载体；与 llm / http / tool / agent 体内那次读
// 同一个字段）。
//
// 语义 = **额外尝试次数**：尝试总数 = MaxRetry + 1（与 llm / tool 的
// `for attempt := 0; attempt <= retries; attempt++` 同解）。0 / nil ⇒ 平台没有
// 声明重试 ⇒ 只跑一次，**不发明**。
func declaredCodeRetries(cc *spec.CodeConfig) int {
	if cc == nil || cc.Exception == nil || cc.Exception.MaxRetry <= 0 {
		return 0
	}
	return cc.Exception.MaxRetry
}

// codeTimeoutMessage 一处定义 code 超时诊断的拼写（python / javascript 共用，
// 两种语言不得各写一份）。
//
// stubBudget=true 的**语义**：这份预算是**我们自加**的安全网（平台未声明
// timeoutMs）⇒ 必须说出来。不说的话，分诊只能按 `RCODE_TIMEOUT` 这个码去
// 猜主体，而「桩自加的预算」会被记成「平台超时」或「桩缺能力」= 把桩的
// 产物算到别人账上（假信号比不判更坏：它会训练人绕过判据）。
// codeBudgetKey 是「下发到下执行器的**声明预算**」在 ctx 里的键（唯一写点 =
// codeNode.Invoke；唯一读点 = codeTimeoutBudget）。
type codeBudgetKey struct{}

// codeTimeoutBudget —— 超时诊断要报的**预算**（一处定义，pyrun / jrun 共用）。
//
//	优先：下发时记录的**声明预算**（`code.go::codeNode.Invoke`，= settingOnError.timeoutMs，
//	      或更紧的图级截止）；
//	次之：ctx 截止的**剩余**（调用方直接调执行器、没走 code 节点）；
//	都没有：调用方给的 fallback（桩自加的安全网），ok=false ⇒ 诊断须标出来。
func codeTimeoutBudget(ctx context.Context, fallback time.Duration) (time.Duration, bool) {
	if d, ok := ctx.Value(codeBudgetKey{}).(time.Duration); ok {
		return d, true
	}
	if dl, ok := ctx.Deadline(); ok {
		return time.Until(dl), true
	}
	return fallback, false
}

func codeTimeoutMessage(lang string, d time.Duration, stubBudget bool) string {
	// `d` 是**生效预算**（不是误差量）；`Round(ms)` 只为让诊断与人脸一致 ——
	// 不取整时 `time.Until(deadline)` 的纳秒残差会印成 `999.987046ms`（声明的是
	// `timeout(1s)`），读者得自己做除法才能对回声明值。残差 <1µs，不是语义差异。
	base := fmt.Sprintf("code: %s execution timed out after %s", lang, d.Round(time.Millisecond))
	if stubBudget {
		return base + " (" + StubBudgetTimeoutMarker + "; the node declares no timeout_ms)"
	}
	// ES-024: a declared budget that ran out must say so, and must never claim
	// the node declared no timeout_ms.
	return base + " (budget exhausted)"
}

// StubBudgetTimeoutMarker 是「这份预算由**我们自加**（节点未声明 `timeout_ms`）」
// 的机器标记。**一处定义**：运行期写它（`codeTimeoutMessage`），分诊读它
// （`cmd/rt-l3` 的 stubGapMarkers 引用本常量）。
//
// 为什么必须有：诊断里只写「timed out after 30s」时，分诊只能按错误码
// `RCODE_TIMEOUT` 猜主体 —— 平台声明的超时（语料值域 60s–600s）与桩自加的安全网
// 是**两件事**，混在一格就是把桩的产物算到别人账上（假信号比不判更坏）。
const StubBudgetTimeoutMarker = "stub safety budget"
