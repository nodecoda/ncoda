package nodes

// tool.go — tool 节点正式实现（M2.2，PRD U2.4）。
//
// 语义:
//   - 执行通道:Deps.Tools（ToolRegistry）。nil → RNodeError。
//   - 工具标识:ProviderID+"/"+ToolName 唯一名（同 provider 内 tool_name
//     唯一;ProviderID 空时退化为 ToolName）。Call 由注册表实现。
//   - 参数:ToolParam 有序装配 —— Ref 从装配输入解析（dotted port）;
//     Value 字面量直通。参数 map 键 = Key。
//   - 输出端口:"output" = 调用结果。
//   - 绑定缺失（注册表不认识的工具名）→ RBindingMissing（U2.4 反例）。
//   - ExceptionConfig:MaxRetry 重试;FailBranch → $branch="fail-branch"。
//   - TimeoutAxis（P3）:tool 平台的 timeout 载体同为 settingOnError.timeoutMs
//     = **total**;此前完全未消费超时 ⇒ 现在所有尝试共享一个 deadline。
//     axis=per_attempt 时按次;none 不设。只有声明了重试才发 retry trace。

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/nodecoda/ncoda/pkg/runtime/boundary"
	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/naming"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/trace"
)

// Tool invokes a registered logical tool.
type Tool struct{}

func (Tool) Kind() string { return spec.KindTool }

func (Tool) Build(ctx context.Context, n *spec.NodeSpec, deps Deps) (NodeImpl, error) {
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	if deps.Tools == nil {
		return nil, diag.New(diag.RBindingMissing,
			"node %s: tool node has no implementation registry — run with --local-plugins or --plugin <manifest>", n.ID)
	}
	// ES-017b/ES-044: tool shares the single ValueBoundary with code/llm.
	pol, err := boundary.ProductionPolicy()
	if err != nil {
		return nil, diag.New(diag.RSpecInvalid, "node %s: execution policy: %v", n.ID, err)
	}
	vb := boundary.New(boundary.Options{
		Policy: pol,
		NodeID: n.ID,
		Kind:   spec.KindTool,
		Emit:   deps.SemTrace,
	})
	return &toolNode{cfg: cfg.(*spec.ToolConfig), tr: deps.Tools, id: n.ID, semTrace: deps.SemTrace, b: vb}, nil
}

type toolNode struct {
	cfg *spec.ToolConfig
	tr  ToolRegistry
	id  string
	// semTrace records retry axis/attempt decisions (P3).
	semTrace func(trace.Record)
	b        *boundary.ValueBoundary // single value boundary (ES-017b)
}

// opName 解析工具逻辑名末段：优先 Operation（IR 语义标识），
// 退化为 ToolName（旧 spec 兼容：工具名即末段）。
func (t *toolNode) opName() string {
	if t.cfg.Operation != "" {
		return t.cfg.Operation
	}
	return t.cfg.ToolName
}

func (t *toolNode) name() string {
	op := t.opName()
	if t.cfg.ProviderID != "" {
		return t.cfg.ProviderID + "/" + op
	}
	return op
}

func (t *toolNode) Invoke(ctx context.Context, in map[string]any) (map[string]any, error) {
	// ES-017b/ES-044: the single ValueBoundary is invoked at the top of every
	// node. Honest scope: spec.ToolConfig has no `types` field today (only
	// code/loop/qa carry declarations), so decl is nil and FillInputs is a
	// no-op — absent/null pass through verbatim, exactly like a code node with
	// no declaration (ES-044). The call site exists so tool consumes the
	// boundary as soon as declarations land; it does not pretend to fill.
	var decl map[string]*spec.TypeSpec // nil by construction: no tool declaration field
	if t.b != nil {
		filled, ferr := t.b.FillInputs(in, decl, 1)
		if ferr != nil {
			return nil, diag.New(diag.RInputMissing, "node %s: %v", t.id, ferr)
		}
		in = filled
	}
	regName, ok := t.resolve(t.tr.Names())
	if !ok {
		hint := ""
		if naming.IsPlatformName(t.cfg.ProviderID) {
			hint = "; platform-named library is a legacy form — bind it to a symbol (local/backend binding), never resolve by operation name alone"
		}
		return nil, diag.New(diag.RBindingMissing, "tool %q: no binding in registry (have %v)%s", t.name(), t.tr.Names(), hint)
	}
	args := map[string]any{}
	for _, p := range t.cfg.Params {
		if p.Ref != nil {
			// assembled under p.Key (spec.Sources path fix)
			args[p.Key] = in[p.Key]
			continue
		}
		args[p.Key] = resolveNestedToolRefs(p.Value, in)
	}

	retries := 0
	var timeout time.Duration
	if ec := t.cfg.Exception; ec != nil {
		retries = ec.MaxRetry
		if retries < 0 {
			retries = 0
		}
		timeout = time.Duration(ec.TimeoutMS) * time.Millisecond
	}
	// settingOnError.timeoutMs is the total carrier for the tool/plugin
	// family; an absent axis defaults to total.
	axis := effectiveTimeoutAxis(t.cfg.Exception)
	var deadline time.Time
	if axis == string(spec.TimeoutAxisTotal) && timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	var res any
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		callCtx := ctx
		cancel := func() {}
		switch {
		case axis == string(spec.TimeoutAxisTotal) && timeout > 0:
			callCtx, cancel = context.WithDeadline(ctx, deadline)
		case axis == string(spec.TimeoutAxisPerAttempt) && timeout > 0:
			callCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		if retries > 0 {
			t.emitRetryTrace(attempt+1, axis, axisBudgetMS(axis, timeout, deadline, time.Now()))
		}
		res, lastErr = t.tr.Call(callCtx, regName, args)
		cancel()
		if lastErr == nil {
			return toolOutput(res), nil
		}
	}
	// ES-046: tool shares the one failure-policy home (throw / return /
	// error-branch). throw keeps the original binding diagnostic.
	if payload, absorbed, perr := applyErrorPolicy(t.cfg.Exception, t.id, nodeFailure{Kind: spec.KindTool, Err: lastErr}); perr != nil {
		return nil, perr
	} else if absorbed {
		return payload, nil
	}
	return nil, diag.New(diag.RNodeError, "tool %s: %v", regName, lastErr)
}

// emitRetryTrace records one attempt under a declared retry policy. It
// mirrors the code/http/llm record shape (1-based attempt) and passes
// trace.Record.Validate().
func (t *toolNode) emitRetryTrace(attempt int, axis string, deadlineMS int) {
	if t.semTrace == nil {
		return
	}
	id := t.id
	if id == "" {
		id = "?"
	}
	origin := trace.OriginWireAbsent
	if t.cfg.Exception != nil {
		origin = trace.OriginWirePresent
	}
	t.semTrace(trace.Record{
		Stage:      "retry",
		RuleID:     trace.RuleRetry,
		Origin:     origin,
		Evidence:   "observed:p3:nodes." + id + ".retry",
		DeadlineMS: deadlineMS,
		Node:       id,
		Kind:       spec.KindTool,
		FillRule:   "retry_attempt",
		Attempt:    attempt,
		Axis:       axis,
		Input:      "setting_on_error.retry",
		Output:     strconv.Itoa(attempt),
	})
}

// tailCandidates 是工具逻辑名的可能末段：Operation（IR 语义标识）优先，
// ToolName（旧 spec 工具名）其次。工具声明同时携带两者时都参与匹配。
func (t *toolNode) tailCandidates() []string {
	var out []string
	if t.cfg.Operation != "" {
		out = append(out, t.cfg.Operation)
	}
	if t.cfg.ToolName != "" && t.cfg.ToolName != t.cfg.Operation {
		out = append(out, t.cfg.ToolName)
	}
	return out
}

// resolve 在注册表中定位本工具,返回注册逻辑名。匹配顺序：
//  1. 完整候选（ProviderID/末段）精确命中；
//  2. 任一段名后缀匹配（注册名以 "/"+段名 结尾）。
//
// 返回的注册名用于调用（Call），保证 registry 侧零歧义。
func (t *toolNode) resolve(names []string) (string, bool) {
	if t.cfg.ProviderID != "" {
		// 库已知 ⇒ 只接受精确符号（provider/operation 或绑定别名）。
		// 不再按末段猜：那会把「A 库的函数」静默接到「B 库的同名函数」上，
		// 库名就失去意义（RUNTIME-LIBRARY-SPEC §3.2 R2）。
		for _, tl := range t.tailCandidates() {
			full := t.cfg.ProviderID + "/" + tl
			for _, n := range names {
				if n == full {
					return n, true
				}
			}
		}
		return "", false
	}
	// 无库（旧 spec / 内建 wire）：先精确工具名，再要求唯一末段匹配。
	for _, tl := range t.tailCandidates() {
		for _, n := range names {
			if n == tl {
				return n, true
			}
		}
	}
	var hit string
	for _, n := range names {
		for _, tl := range t.tailCandidates() {
			if strings.HasSuffix(n, "/"+tl) {
				if hit != "" && hit != n {
					return "", false // 歧义：不猜
				}
				hit = n
			}
		}
	}
	return hit, hit != ""
}

// toolOutput 组装工具节点输出端口（对齐 IR fieldPort 语义）：
//   - 结果对象（map）按顶层字段展开为端口（"result" 等，供
//     {{block_output_<id>.<field>}} 引用）；
//   - 始终保留 "output" 端口 = 整个结果（end 节点 / 整体消费）。

// resolveNestedToolRefs substitutes structured tool-argument refs. Spec build
// tags frontend {path, ref_node} leaves with spec.NestedRefMarker and wires
// them through ToolConfig.NestedRefs; AssembleInput resolves producers into
// in[NestedRefMapKey][token].
func resolveNestedToolRefs(v any, in map[string]any) any {
	switch x := v.(type) {
	case map[string]any:
		if token, ok := x[spec.NestedRefMarker].(string); ok {
			if _, hasNode := x["ref_node"]; hasNode {
				if refs, ok := in[spec.NestedRefMapKey].(map[string]any); ok {
					if val, ok := refs[token]; ok {
						return val
					}
				}
				return nil
			}
		}
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = resolveNestedToolRefs(vv, in)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = resolveNestedToolRefs(vv, in)
		}
		return out
	default:
		return v
	}
}

func toolOutput(res any) map[string]any {
	out := map[string]any{"output": res}
	if obj, ok := res.(map[string]any); ok {
		for k, v := range obj {
			out[k] = v
		}
	}
	// 成功也发 `$branch="source"`：与 http 节点同一条（http.go 头注 M14 —— 带
	// 分支边的节点**成功也要选边**，否则分支门报 "node did not emit a $branch
	// key"；tool 此前只在 FailBranch 时发 `fail-branch` ⇒ `attempt { <工具调用> }`
	// 的**成功臂**在运行期直接炸）。不带分支边时多出的键无害（同 http 口径）。
	out[MarkerBranch] = "source"
	return out
}
