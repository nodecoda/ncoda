package nodes

// llm.go — llm 节点正式实现（M2.2，PRD U2.2）。
//
// 语义:
//   - 执行通道:Deps.Models（ModelFactory）。nil → RNodeError（依赖缺失,
//     CLI/测试注入 fake 即可离线确定性断言）。
//   - 输入约定:装配 map 中取消息数组（in["input"] 或 in["messages"] 为
//     []any 时直接作为对话;元素 {role,content} 或裸字符串 → user 消息）。
//     否则把装配 map 折叠为单条 user 消息（JSON 序列化,确定性）。
//   - 输出端口:"output" = 模型回复文本（与 demo2/coze 端口契约一致）。
//   - ExceptionConfig:MaxRetry 重试（幂等性由调用方保证——v1 契约）。
//   - TimeoutAxis（P3）:llm 的 timeout 载体是 settingOnError.timeoutMs，平台
//     语义 = **total**（docs/five-step/platform_model.yaml#node_kinds）
//     ⇒ 所有尝试共享一个 deadline；axis=per_attempt 时按次；none 不设。
//     只有声明了重试的节点发 retry trace（axis/attempt/deadline_ms）。

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nodecoda/ncoda/pkg/expr"
	"github.com/nodecoda/ncoda/pkg/runtime/boundary"
	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/trace"
)

// LLM invokes a chat model via the injected ModelFactory.
type LLM struct{}

func (LLM) Kind() string { return spec.KindLLM }

func (LLM) Build(ctx context.Context, n *spec.NodeSpec, deps Deps) (NodeImpl, error) {
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	if deps.Models == nil {
		return nil, diag.New(diag.RNodeError, "node %s: llm node requires a ModelFactory (WithDeps)", n.ID)
	}
	// ES-017b/ES-044: llm and tool share the single ValueBoundary with code.
	pol, err := boundary.ProductionPolicy()
	if err != nil {
		return nil, diag.New(diag.RSpecInvalid, "node %s: execution policy: %v", n.ID, err)
	}
	vb := boundary.New(boundary.Options{
		Policy: pol,
		NodeID: n.ID,
		Kind:   spec.KindLLM,
		Emit:   deps.SemTrace,
	})
	return &llmNode{cfg: cfg.(*spec.LLMConfig), mf: deps.Models, id: n.ID, trace: deps.LLMTrace, semTrace: deps.SemTrace, b: vb}, nil
}

type llmNode struct {
	cfg   *spec.LLMConfig
	mf    ModelFactory
	id    string
	trace func(nodeID, model string, msgs []Message)
	// semTrace records retry axis/attempt decisions (P3).
	semTrace func(trace.Record)
	b        *boundary.ValueBoundary // single value boundary (ES-017b)
}

func (l *llmNode) Invoke(ctx context.Context, in map[string]any) (map[string]any, error) {
	// ES-017b/ES-044: the single ValueBoundary is invoked at the top of every
	// node. Honest scope: spec.LLMConfig has no `types` field today (only
	// code/loop/qa carry input declarations), so decl is nil and FillInputs is
	// a no-op — absent/null pass through verbatim, exactly like a code node
	// with no declaration (ES-044). The call site exists so llm consumes the
	// boundary as soon as declarations land; it does not pretend to fill.
	var decl map[string]*spec.TypeSpec // nil by construction: no llm declaration field
	if l.b != nil {
		filled, ferr := l.b.FillInputs(in, decl, 1)
		if ferr != nil {
			return nil, diag.New(diag.RInputMissing, "node %s: %v", l.id, ferr)
		}
		in = filled
	}
	msgs := messagesFrom(in)
	// 装配输入里没有显式消息数组 → 从配置字面量重建（messages 在
	// ConfigPair,内容里的 {{block_output_<id>.<port>}} 引用替换为装配值）。
	if msgs == nil {
		msgs = messagesFromConfig(l.cfg, in)
	}
	// extract(..., vision: img)：把视觉操作数挂到最后一条 user 消息上。
	msgs = attachVision(l.cfg.Vision, msgs, in)
	// M15 调试:实际发往模型的 prompt（块引用已替换,可读）。
	if l.trace != nil {
		l.trace(l.id, l.cfg.Model, msgs)
	}
	retries := 0
	interval := time.Duration(0)
	timeout := time.Duration(0)
	if ec := l.cfg.Exception; ec != nil {
		retries = ec.MaxRetry
		if retries < 0 {
			retries = 0
		}
		interval = time.Duration(ec.RetryIntervalMS) * time.Millisecond
		timeout = time.Duration(ec.TimeoutMS) * time.Millisecond
	}
	// settingOnError.timeoutMs is the total carrier; an absent axis therefore
	// defaults to total (family semantics), not to the old per-attempt slice.
	axis := effectiveTimeoutAxis(l.cfg.Exception)
	var deadline time.Time
	if axis == string(spec.TimeoutAxisTotal) && timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	result := l.cfg.Result
	if result == nil || result.PayloadSchema == nil {
		return nil, diag.New(diag.RNodeError, "llm node %s model %s: payload schema is missing", l.id, l.cfg.Model)
	}
	if err := result.PayloadSchema.ValidateDefinition(); err != nil {
		return nil, diag.New(diag.RNodeError, "llm node %s model %s: %v", l.id, l.cfg.Model, err)
	}
	// Per-call options are assembled once: retries reuse the same params and
	// schema (an attempt is one model call + one decode).
	callOpts := CallOptions{Params: llmCallParams(l.cfg), Schema: result.PayloadSchema}
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		callCtx := ctx
		cancel := func() {}
		switch {
		case axis == string(spec.TimeoutAxisTotal) && timeout > 0:
			// One deadline computed before the first attempt: retries share
			// the budget instead of each refilling it (ES-023a analogue).
			callCtx, cancel = context.WithDeadline(ctx, deadline)
		case axis == string(spec.TimeoutAxisPerAttempt) && timeout > 0:
			callCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		if retries > 0 {
			l.emitRetryTrace(attempt+1, axis, axisBudgetMS(axis, timeout, deadline, time.Now()))
		}
		var response ModelResponse
		if optionsAware, ok := l.mf.(OptionsModelFactory); ok {
			response, lastErr = optionsAware.ChatWithOptions(callCtx, l.cfg.Model, msgs, callOpts)
		} else if schemaAware, ok := l.mf.(SchemaAwareModelFactory); ok {
			response, lastErr = schemaAware.ChatWithSchema(callCtx, l.cfg.Model, msgs, result.PayloadSchema)
		} else if rich, ok := l.mf.(RichModelFactory); ok {
			response, lastErr = rich.ChatWithMetadata(callCtx, l.cfg.Model, msgs)
		} else if rich, ok := l.mf.(RichChatModelFactory); ok {
			response, lastErr = rich.ChatRich(callCtx, l.cfg.Model, msgs)
		} else {
			var text string
			text, lastErr = l.mf.Chat(callCtx, l.cfg.Model, msgs)
			response = ModelResponse{Text: text, TextPresent: true}
		}
		cancel()
		if lastErr == nil {
			text := response.Text
			if text == "" && !response.TextPresent {
				text = response.Content
			}
			decoded, decodeErr := result.PayloadSchema.DecodePayload([]byte(text))
			if decodeErr == nil {
				return normalizedLLMOutput(decoded, response, result), nil
			}
			lastErr = fmt.Errorf("decode: %w", decodeErr)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if attempt < retries && interval > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(interval):
			}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("operation failed")
	}
	// ES-046: llm shares the one failure-policy home (throw / return /
	// error-branch); before P3 the policy was dropped here entirely.
	if payload, absorbed, perr := applyErrorPolicy(l.cfg.Exception, l.id, nodeFailure{Kind: spec.KindLLM, Err: lastErr}); perr != nil {
		return nil, perr
	} else if absorbed {
		return payload, nil
	}
	return nil, diag.New(diag.RNodeError, "llm node %s model %s attempt %d: %v", l.id, l.cfg.Model, retries+1, lastErr)
}

// emitRetryTrace records one attempt under a declared retry policy. It
// mirrors the code/http record shape (1-based attempt) and passes
// trace.Record.Validate().
func (l *llmNode) emitRetryTrace(attempt int, axis string, deadlineMS int) {
	if l.semTrace == nil {
		return
	}
	id := l.id
	if id == "" {
		id = "?"
	}
	origin := trace.OriginWireAbsent
	if l.cfg.Exception != nil {
		origin = trace.OriginWirePresent
	}
	l.semTrace(trace.Record{
		Stage:      "retry",
		RuleID:     trace.RuleRetry,
		Origin:     origin,
		Evidence:   "observed:p3:nodes." + id + ".retry",
		DeadlineMS: deadlineMS,
		Node:       id,
		Kind:       spec.KindLLM,
		FillRule:   "retry_attempt",
		Attempt:    attempt,
		Axis:       axis,
		Input:      "setting_on_error.retry",
		Output:     strconv.Itoa(attempt),
	})
}

func normalizedLLMOutput(decoded any, response ModelResponse, result *spec.LLMResultConfig) map[string]any {
	// `text` is semantic. `output` remains a physical compatibility alias for
	// v1 graphs and existing compile consumers; it never changes the payload.
	out := map[string]any{"text": decoded, "output": decoded}
	if result != nil {
		if result.Metadata.ReasoningContent {
			if response.ReasoningContent != nil {
				out["reasoning_content"] = *response.ReasoningContent
			} else {
				out["reasoning_content"] = nil
			}
		} else {
			out["reasoning_content"] = nil
		}
		if result.Metadata.Usage.PromptTokens || result.Metadata.Usage.CompletionTokens || result.Metadata.Usage.TotalTokens {
			usage := normalizeUsage(response.Usage, result.Metadata.Usage)
			if usage == nil {
				out["usage"] = nil
			} else {
				out["usage"] = usage
			}
		} else {
			out["usage"] = nil
		}
		if result.Metadata.FinishReason {
			if response.FinishReason != nil {
				out["finish_reason"] = *response.FinishReason
			} else {
				out["finish_reason"] = nil
			}
		} else {
			out["finish_reason"] = nil
		}
	}
	out["response"] = map[string]any{
		"text":              out["text"],
		"reasoning_content": out["reasoning_content"],
		"usage":             out["usage"],
		"finish_reason":     out["finish_reason"],
	}
	return out
}

func normalizeUsage(u *LLMUsage, want spec.LLMUsageConfig) *LLMUsage {
	if u == nil {
		return nil
	}
	out := &LLMUsage{}
	if want.PromptTokens {
		out.PromptTokens = u.PromptTokens
	}
	if want.CompletionTokens {
		out.CompletionTokens = u.CompletionTokens
	}
	if want.TotalTokens {
		out.TotalTokens = u.TotalTokens
	}
	return out
}

// messagesFrom extracts a chat turn list from the assembled input map.
// Priority: in["input"] / in["messages"] list; otherwise one user message
// from the whole map (JSON — deterministic).
func messagesFrom(in map[string]any) []Message {
	for _, key := range []string{"input", "messages"} {
		arr := toAnySlice(in[key])
		if len(arr) > 0 {
			return coerceMessages(arr)
		}
	}
	return nil
}

// llmConfigNonProviderKeys 是 llm() config 中**不属于** provider 参数的键。
//
// 转发规则（R-73 同一条纪律：作者写的东西要么生效、要么报账）：
//   - `messages` / `systemPrompt` / `userPrompt` 是提示词表面，runtime 已重建为
//     chat turns；
//   - `model` 是第一参数的别名，`model_name` 是平台显示名（不是参数）；
//   - `timeout_ms` / `stream` 是调用控制，由 runtime 拥有；
//   - `response_format` 在 IR/spec 里是平台数值码（coze 0/1/2），provider 侧走
//     声明式 Schema；原样转发会把平台编码发给上游；
//   - `reasoning_format` 是 参考目标专有旋钮（如 "separated"），不是 Chat Completions
//     字段；
//   - `generation_diversity` / `model_name` 是 coze 概念（新规范名 `coze:*`）；
//     `coze:` / `参考目标:` 前缀的键声明给别的目标 ⇒ 不转发；
//   - `openai:` 前缀在运行时这里**剥掉前缀**后转发（命名空间的实际语义：
//     `openai:seed` 就是给 provider 的 `seed`）；
//   - `x-*` 供应商自定义参数原样转发。
//
// 其余键（含未知键）原样转发：gateway 负责报「目标没消费」的差额，而不是在
// runtime 这里二次静默丢弃。
// targetParamNamespaces 是"声明给别的目标"的参数命名空间（小写前缀）。
// 内核默认不带任何平台命名空间：它们属于适配器，由 SetTargetParamNamespaces
// 在适配器 init() 时登记（与 builtin.RegisterContracts 同一模式）。
var targetParamNamespaces = map[string]bool{}

// SetTargetParamNamespaces 登记目标参数命名空间。重复登记是幂等的；
// 空串是程序员错误（会让每个 ":" 键都被吞掉），当场 panic。
func SetTargetParamNamespaces(namespaces ...string) {
	for _, ns := range namespaces {
		n := strings.ToLower(strings.TrimSpace(ns))
		if n == "" {
			panic("nodes: empty target param namespace")
		}
		targetParamNamespaces[n] = true
	}
}

// IsTargetNamespacedParam reports whether key is declared for another target
// (`<namespace>:<name>` where the namespace was registered by an adapter).
// Keyed facts only: an unregistered namespace is never guessed.
func IsTargetNamespacedParam(key string) bool {
	ns, _, ok := strings.Cut(key, ":")
	return ok && targetParamNamespaces[strings.ToLower(ns)]
}

var llmConfigNonProviderKeys = map[string]bool{
	"messages":             true,
	"systemPrompt":         true,
	"userPrompt":           true,
	"model":                true,
	"model_name":           true,
	"timeout_ms":           true,
	"stream":               true,
	"response_format":      true,
	"reasoning_format":     true,
	"generation_diversity": true,
}

// llmCallParams extracts provider-facing parameters from the llm() config.
// Returns nil when the config carries none, so legacy factories see no change.
func llmCallParams(cfg *spec.LLMConfig) map[string]any {
	if cfg == nil || len(cfg.Config) == 0 {
		return nil
	}
	var out map[string]any
	for _, cp := range cfg.Config {
		key := cp.Key
		// 命名空间路由（2026-10-20）：
		//   <target>:* 声明给某个目标平台 ⇒ 不发给 provider（登记表见
		//   SetTargetParamNamespaces：内核不带平台名）；
		//   openai:* 是给 provider 的 ⇒ 剥前缀后转发；
		//   x-* 供应商自定义参数 ⇒ 原样转发。
		if IsTargetNamespacedParam(key) {
			continue
		}
		if rest, ok := strings.CutPrefix(key, "openai:"); ok {
			key = rest
		}
		if llmConfigNonProviderKeys[key] {
			continue
		}
		if out == nil {
			out = make(map[string]any, len(cfg.Config))
		}
		out[key] = cp.Value
	}
	return out
}

// messagesFromConfig rebuilds the chat turn list from the config's
// "messages" ConfigPair (IR puts literal messages there; dataflow refs are
// {{block_output_<id>.<port>}} placeholders resolved against the assembled
// input map). Returns nil when the config carries no messages.
func messagesFromConfig(cfg *spec.LLMConfig, in map[string]any) []Message {
	resolve := func(v string) string { return resolveBlockTemplate(v, in) }
	for _, cp := range cfg.Config {
		if cp.Key != "messages" {
			continue
		}
		arr := toAnySlice(cp.Value)
		if len(arr) == 0 {
			return nil
		}
		out := make([]Message, 0, len(arr))
		for _, el := range arr {
			m, ok := el.(map[string]any)
			if !ok {
				continue
			}
			if msg, ok := messageFromMap(m, resolve); ok {
				out = append(out, msg)
			}
		}
		return out
	}
	// 废弃提示词表面（systemPrompt / userPrompt）的迁移窗口：messages 缺席时
	// 把它们当成单条 system / user 消息。修前这两个键只被排除出 provider
	// 参数，runtime 不读 ⇒ `llm(m, {"userPrompt": "hi"})` 编译成功但**没有
	// 用户消息**。语言层对这两个键发 LLM_CONFIG_DEPRECATED，迁移期行为正确。
	var out []Message
	for _, alias := range []struct{ key, role string }{
		{"systemPrompt", "system"},
		{"userPrompt", "user"},
	} {
		for _, cp := range cfg.Config {
			if cp.Key != alias.key {
				continue
			}
			text, ok := cp.Value.(string)
			if !ok || text == "" {
				continue
			}
			out = append(out, Message{Role: alias.role, Content: resolve(text)})
			break
		}
	}
	return out
}

// resolveBlockTemplate replaces {{block_output_<id>.<port>}} and
// {{global_variable_app.<name>}} placeholders with values from the
// assembled input map (M14: compile 层把 conversation 变量当前值以
// "global_variable_app.<name>" 键注入 inputs). Missing refs render as
// "<missing>" (RenderTemplate contract).
func resolveBlockTemplate(tmpl string, in map[string]any) string {
	s := blockRefRe.ReplaceAllStringFunc(tmpl, func(m string) string {
		sm := blockRefRe.FindStringSubmatch(m)
		if sm == nil {
			return m
		}
		val, ok := lookupDottedPath(in, strings.Split(sm[2], "."))
		if !ok {
			return "<missing>"
		}
		return expr.FormatTemplateValue(val)
	})
	return globalRefRe.ReplaceAllStringFunc(s, func(m string) string {
		sm := globalRefRe.FindStringSubmatch(m)
		if sm == nil {
			return m
		}
		if v, ok := in["global_variable_app."+sm[1]]; ok {
			return fmt.Sprint(v)
		}
		return "<missing>"
	})
}

// blockRefRe matches IR block references inside LLM prompt templates.
var blockRefRe = regexp.MustCompile(`\{\{block_output_([0-9]+)\.([^}]+)\}\}`)

// globalRefRe matches IR conversation-variable references inside LLM
// prompt templates (value injected by compile 层 as a flat input key).
var globalRefRe = regexp.MustCompile(`\{\{global_variable_app\.([^}]+)\}\}`)

// lookupDottedPath walks a nested map along path segments.
func lookupDottedPath(m map[string]any, path []string) (any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	cur := any(m)
	for _, seg := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			// E5-b item-0: a record-array member chain (data.doc_results.url)
			// — the constant-0 index was dropped at IR, so an array at a
			// segment position descends via item 0 (empty array → missing).
			// Mirrors the reference-target item-0 member access rendering and the
			// assemble.go lookupDotted fallback.
			if arr, isArr := cur.([]any); isArr {
				if len(arr) == 0 {
					return nil, false
				}
				if m0, ok2 := arr[0].(map[string]any); ok2 {
					if v, ok3 := m0[seg]; ok3 {
						cur = v
						continue
					}
				}
			}
			if arr, isArr := cur.([]map[string]any); isArr {
				if len(arr) == 0 {
					return nil, false
				}
				if v, ok2 := arr[0][seg]; ok2 {
					cur = v
					continue
				}
			}
			return nil, false
		}
		cur, ok = mm[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func coerceMessages(arr []any) []Message {
	out := make([]Message, 0, len(arr))
	for _, el := range arr {
		switch v := el.(type) {
		case map[string]any:
			if msg, ok := messageFromMap(v, nil); ok {
				out = append(out, msg)
			}
		default:
			out = append(out, Message{Role: "user", Content: expr.FormatTemplateValue(v)})
		}
	}
	return out
}

// messageFromMap normalizes one chat message. content may be a plain string
// or an OpenAI-style content-part array:
//
//	"content": [{"type":"text","text":"..."},
//	            {"type":"image_url","image_url":{"url":"..."}}]
//
// `parts` is accepted as a direct spec-level spelling for the same array.
func messageFromMap(m map[string]any, resolveText func(string) string) (Message, bool) {
	if resolveText == nil {
		resolveText = func(s string) string { return s }
	}
	role, _ := m["role"].(string)
	if role == "" {
		role = "user"
	}
	msg := Message{Role: role}
	switch content := m["content"].(type) {
	case nil:
		if t, ok := m["text"].(string); ok {
			msg.Content = resolveText(t)
		}
	case string:
		msg.Content = resolveText(content)
	case []any:
		msg.Parts = partsFromAny(content, resolveText)
	default:
		if t, ok := m["text"].(string); ok {
			msg.Content = resolveText(t)
		}
	}
	if raw, ok := m["parts"].([]any); ok {
		msg.Parts = partsFromAny(raw, resolveText)
	} else if raw, ok := m["parts"].([]Part); ok {
		msg.Parts = append([]Part(nil), raw...)
	}
	if len(msg.Parts) > 0 && msg.Content == "" {
		msg.Content = textFromParts(msg.Parts)
	}
	if role != "" || msg.Content != "" || len(msg.Parts) > 0 {
		return msg, true
	}
	return Message{}, false
}

func partsFromAny(arr []any, resolveText func(string) string) []Part {
	out := make([]Part, 0, len(arr))
	for _, el := range arr {
		switch v := el.(type) {
		case string:
			if v != "" {
				out = append(out, Part{Type: PartText, Text: resolveText(v)})
			}
		case map[string]any:
			if p, ok := partFromMap(v, resolveText); ok {
				out = append(out, p)
			}
		case Part:
			out = append(out, v)
		}
	}
	return out
}

func partFromMap(m map[string]any, resolveText func(string) string) (Part, bool) {
	typ := strings.ToLower(strings.TrimSpace(stringValue(m["type"])))
	switch typ {
	case "", "text":
		text := stringValue(m["text"])
		if text == "" {
			text = stringValue(m["content"])
		}
		if text == "" {
			return Part{}, false
		}
		return Part{Type: PartText, Text: resolveText(text)}, true
	case "image_url", "image":
		url, mime := partURL(m["image_url"])
		if url == "" {
			return Part{}, false
		}
		return Part{Type: PartImageURL, URL: resolveText(url), MIME: mime}, true
	case "video_url", "video":
		url, mime := partURL(m["video_url"])
		if url == "" {
			return Part{}, false
		}
		return Part{Type: PartVideoURL, URL: resolveText(url), MIME: mime}, true
	case "input_audio", "audio":
		obj, _ := m["input_audio"].(map[string]any)
		if obj == nil {
			obj = m
		}
		data := stringValue(obj["data"])
		if data == "" {
			data = stringValue(obj["url"])
		}
		format := strings.ToLower(strings.TrimSpace(stringValue(obj["format"])))
		p := Part{Type: PartInputAudio, MIME: audioMIME(format)}
		if b, ok := decodeBase64(data); ok {
			p.Data = b
		} else {
			p.URL = data
		}
		return p, true
	case "file":
		obj, _ := m["file"].(map[string]any)
		if obj == nil {
			obj = m
		}
		p := Part{
			Type:     PartFile,
			MIME:     stringValue(obj["mime_type"]),
			Filename: stringValue(obj["filename"]),
		}
		if fd := stringValue(obj["file_data"]); fd != "" {
			if mime, payload, ok := splitDataURL(fd); ok {
				fd = payload
				if p.MIME == "" {
					p.MIME = mime
				}
			}
			if b, ok := decodeBase64(fd); ok {
				p.Data = b
			} else {
				p.URL = fd
			}
		} else if u := stringValue(obj["url"]); u != "" {
			p.URL, p.MIME = partURL(u)
		} else if id := stringValue(obj["file_id"]); id != "" {
			p.URL = id
		} else {
			return Part{}, false
		}
		return p, true
	default:
		// Unknown part type: keep the textual payload so the request stays
		// sendable rather than silently dropping content.
		if text := stringValue(m["text"]); text != "" {
			return Part{Type: PartText, Text: resolveText(text)}, true
		}
		return Part{}, false
	}
}

func partURL(v any) (url, mime string) {
	switch x := v.(type) {
	case string:
		url = x
	case map[string]any:
		url = stringValue(x["url"])
		if url == "" {
			url = stringValue(x["image_url"])
		}
		mime = stringValue(x["mime_type"])
	}
	if m, _, ok := splitDataURL(url); ok && mime == "" {
		mime = m
	}
	return url, mime
}

func stringValue(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func audioMIME(format string) string {
	switch format {
	case "":
		return ""
	case "wav", "mp3", "ogg", "webm", "flac", "aac", "m4a":
		return "audio/" + format
	default:
		return format
	}
}

func decodeBase64(s string) ([]byte, bool) {
	if s == "" {
		return nil, false
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		if b, err = base64.RawStdEncoding.DecodeString(s); err != nil {
			return nil, false
		}
	}
	return b, true
}

func splitDataURL(s string) (mime, payload string, ok bool) {
	if !strings.HasPrefix(s, "data:") {
		return "", s, false
	}
	i := strings.Index(s, ",")
	if i < 0 {
		return "", s, false
	}
	meta := s[len("data:"):i]
	mime = strings.Split(meta, ";")[0]
	return mime, s[i+1:], true
}

func textFromParts(parts []Part) string {
	texts := make([]string, 0, len(parts))
	for _, p := range parts {
		if p.Type == PartText && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// attachVision turns the extraction vision operand into an image part on the
// last user message, creating a user turn when the prompt has none.
func attachVision(src *spec.Source, msgs []Message, in map[string]any) []Message {
	if src == nil {
		return msgs
	}
	val := visionValue(src, in)
	part, ok := visionPart(val)
	if !ok {
		return msgs
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			msgs[i].Parts = append(msgs[i].Parts, part)
			return msgs
		}
	}
	return append(msgs, Message{Role: "user", Parts: []Part{part}})
}

func visionValue(src *spec.Source, in map[string]any) any {
	if v, ok := in["vision"]; ok {
		return v
	}
	if src.Val != nil {
		return src.Val
	}
	if src.Name != "" {
		if v, ok := in[src.Name]; ok {
			return v
		}
	}
	return nil
}

func visionPart(val any) (Part, bool) {
	switch v := val.(type) {
	case nil:
		return Part{}, false
	case Part:
		if v.Type == "" {
			v.Type = PartImageURL
		}
		return v, true
	case string:
		if strings.TrimSpace(v) == "" {
			return Part{}, false
		}
		mime := ""
		if m, _, ok := splitDataURL(v); ok {
			mime = m
		}
		return Part{Type: PartImageURL, URL: v, MIME: mime}, true
	case []string:
		if len(v) == 0 {
			return Part{}, false
		}
		return visionPart(v[0])
	case []any:
		for _, el := range v {
			if p, ok := visionPart(el); ok {
				return p, true
			}
		}
		return Part{}, false
	case map[string]any:
		mime := stringValue(v["mime_type"])
		for _, key := range []string{"url", "image_url", "file_url", "data_url", "path", "file_data", "data"} {
			if s, ok := v[key].(string); ok && s != "" {
				p, ok := visionPart(s)
				if !ok {
					continue
				}
				if p.MIME == "" {
					p.MIME = mime
				}
				return p, true
			}
		}
		if nested, ok := v["image_url"]; ok {
			return visionPart(nested)
		}
		return Part{}, false
	default:
		return Part{}, false
	}
}
