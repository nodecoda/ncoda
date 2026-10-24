package nodes

// http.go — http 节点正式实现（M2.2，PRD U2.3）。
//
// 语义:
//   - 执行通道:Deps.HTTP（注入/测试用）;nil 时回退包级默认 client
//     （net/http,零依赖、可离线跑 — 请求失败即节点错误）。
//   - URL / body 模板渲染:{{key}} 从装配输入取值（expr.RenderTemplate,
//     缺失 "<missing>";body 的 {{block_output_<id>.<port>}} 由 compile
//     层预装配为输入键）。
//   - P2 host 切片:源 headers/params（spec.HTTPConfig 有序列表）经
//     host.Adapter 投射为线上字段: header Value 与 URL/body 同样渲染,
//     Name 是静态名;query 只追加 URL 里尚不存在的同名键。
//   - ExceptionConfig:MaxRetry（重试次数,RetryIntervalMS 间隔）、
//     TimeoutMS + TimeoutAxis（P3 轴消费）:axis=per_attempt ⇒ 每次尝试各自
//     TimeoutMS;axis=total ⇒ 所有尝试共享一个 deadline（从第一次尝试起算）;
//     axis 缺失/none ⇒ 不设节点级超时（保留默认 client 的 120s 安全网）。
//     只有声明了重试的节点才发 retry trace（axis/attempt/deadline_ms）。
//   - 输出端口（IR/coze 契约,M14 对齐）:body（响应体字符串）、
//     statusCode（int）、headers;旧 runtime 引用 output/status 双写兼容。
//     失败分支（FailBranch）:error_type / error_message 语义端口
//     （coze fail-branch 的 OperationError 形态）。
//   - 分支路由（coze fail-branch 语义）:成功 → $branch="source"（走
//     source 分支）;非 2xx 且 FailBranch=true → $branch="fail-branch"。
//     带分支边的 http 节点依赖 $branch 标记选边,成功也必须有（M14
//     attempt_recovery 修复——此前成功分支不输出 $branch,分支门报
//     "node did not emit a $branch key"）;不带分支边时多出的键无害。

import (
	"context"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nodecoda/ncoda/pkg/builtin"
	"github.com/nodecoda/ncoda/pkg/expr"
	"github.com/nodecoda/ncoda/pkg/runtime/boundary"
	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/host"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/trace"
)

// HTTP executes an outbound HTTP request.
type HTTP struct{}

func (HTTP) Kind() string { return spec.KindHTTP }

func (HTTP) Build(ctx context.Context, n *spec.NodeSpec, deps Deps) (NodeImpl, error) {
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	// Adapter{FollowRedirects:true} keeps the historical net/http default
	// (redirects followed) for the fallback client; Adapter's zero value
	// stops at the first response per the Client contract.
	adapter := host.Adapter{FollowRedirects: true}
	hc := deps.HTTP
	if hc == nil {
		// 默认 client 兜底超时（120s,与 cli/models.go 一致）:节点未配
		// TimeoutMS 时上游挂死不会无限阻塞 run（Tier-1 review #4）。
		hc = &defaultHTTPClient{client: adapter.Client(120 * time.Second)}
	}
	// ES-017b: http shares the single ValueBoundary with code/llm/tool.
	pol, err := boundary.ProductionPolicy()
	if err != nil {
		return nil, diag.New(diag.RSpecInvalid, "node %s: execution policy: %v", n.ID, err)
	}
	vb := boundary.New(boundary.Options{
		Policy: pol,
		NodeID: n.ID,
		Kind:   spec.KindHTTP,
		Emit:   deps.SemTrace,
	})
	return &httpNode{
		cfg:      cfg.(*spec.HTTPConfig),
		hc:       hc,
		id:       n.ID,
		semTrace: deps.SemTrace,
		adapter:  adapter,
		now:      time.Now,
		b:        vb,
	}, nil
}

type httpNode struct {
	cfg      *spec.HTTPConfig
	hc       HTTPClient
	id       string
	semTrace func(trace.Record)
	adapter  host.Adapter
	// now is the clock used for the total-budget deadline. It is injectable
	// so the axis tests can advance time without sleeping (ES-113).
	now func() time.Time
	b   *boundary.ValueBoundary // single value boundary (ES-017b)
}

// requestDoer is the optional fast path: a client that can consume the fully
// built wire request (headers/query already projected) directly. HTTPClient
// itself is unchanged so existing fakes keep compiling.
type requestDoer interface {
	DoRequest(context.Context, host.Built) (int, []byte, error)
}

func (h *httpNode) Invoke(ctx context.Context, in map[string]any) (map[string]any, error) {
	// ES-017b: the single ValueBoundary is invoked at the top of every node.
	// Honest scope: spec.HTTPConfig has no `types` field today (only
	// code/loop/qa carry declarations), so decl is nil and FillInputs is a
	// no-op — absent/null template operands pass through verbatim, exactly
	// like a code node with no declaration (ES-044). The call site exists so
	// http consumes the boundary as soon as declarations land; it does not
	// pretend to fill.
	if h.b != nil {
		filled, ferr := h.b.FillInputs(in, nil, 1)
		if ferr != nil {
			return nil, diag.New(diag.RInputMissing, "node %s: %v", h.id, ferr)
		}
		in = filled
	}
	method := h.cfg.Method
	if method == "" {
		method = http.MethodGet
	}
	url := expr.RenderTemplate(h.cfg.URL, in)
	body := expr.RenderTemplate(h.cfg.Body, in)

	req := host.Request{Method: method, URL: url, Body: body}
	for _, kv := range h.cfg.Headers {
		req.Headers = append(req.Headers, host.KV{Name: kv.Name, Value: expr.RenderTemplate(kv.Value, in)})
	}
	for _, kv := range h.cfg.Params {
		req.Params = append(req.Params, host.KV{Name: kv.Name, Value: expr.RenderTemplate(kv.Value, in)})
	}
	built := h.adapter.Build(req)
	// P2: host physical-field carry record, emitted after build and before
	// the first request (retries reuse the same built request).
	h.emitHostTrace(len(req.Headers), len(req.Params))

	retries := 0
	interval := time.Duration(0)
	timeout := time.Duration(0)
	if ec := h.cfg.Exception; ec != nil {
		retries = ec.MaxRetry
		if retries < 0 {
			retries = 0
		}
		interval = time.Duration(ec.RetryIntervalMS) * time.Millisecond
		timeout = time.Duration(ec.TimeoutMS) * time.Millisecond
	}

	// ES-022b/ES-023b/ES-100/ES-113: the axis decides whether TimeoutMS is a
	// per-attempt slice or one budget shared by every attempt. total computes
	// the deadline once, before the first attempt, so retries cannot refill
	// it; per_attempt keeps the historical fresh-with-timeout behaviour; an
	// absent/none axis installs no node-level timeout at all.
	axis := exceptionTimeoutAxis(h.cfg.Exception)
	sharedTotal := axis == string(spec.TimeoutAxisTotal) && timeout > 0
	var deadline time.Time
	if sharedTotal {
		deadline = h.now().Add(timeout)
	}

	var status int
	var resp []byte
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		reqCtx := ctx
		cancel := func() {}
		switch {
		case sharedTotal:
			reqCtx, cancel = context.WithDeadline(ctx, deadline)
		case axis == string(spec.TimeoutAxisPerAttempt) && timeout > 0:
			reqCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		// Only nodes that declared retries emit attempt records: a single
		// call would add noise without an axis decision to report.
		if retries > 0 {
			h.emitRetryTrace(attempt+1, axis, axisBudgetMS(axis, timeout, deadline, h.now()))
		}
		status, resp, lastErr = h.do(reqCtx, built)
		cancel()
		if lastErr == nil && status >= 200 && status < 300 {
			return httpResultPayload(string(resp), status), nil
		}
		if lastErr == nil {
			// non-2xx: respect fail-branch routing (stub parity: coze
			// http error branch). No retry on a successful HTTP call.
			break
		}
		if attempt < retries && interval > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(interval):
			}
		}
	}

	// ES-046: one failure-policy home for all four kinds. http still carries
	// its status (the error-branch payload exposes it) and keeps the two
	// platform-observed error_type spellings inside nodeFailure.ErrorType.
	if payload, absorbed, perr := applyErrorPolicy(h.cfg.Exception, h.id,
		nodeFailure{Kind: spec.KindHTTP, Err: lastErr, Status: status, HasStatus: true}); perr != nil {
		return nil, perr
	} else if absorbed {
		return payload, nil
	}
	if lastErr != nil {
		return nil, diag.New(diag.RNodeError, "http %s %s: %v", method, url, lastErr)
	}
	return nil, diag.New(diag.RNodeError, "http %s %s: unexpected status %d", method, url, status)
}

// httpResultPayload 组装 http 成功输出表。
//
// 同一个结果口有两种合法拼写：语言字段名（body / status_code / headers）与
// 内建结果契约里的物理口名（如 statusCode）。IR 的端口词汇表随构建而变
// （开放构建只带语言契约，平台构建由适配器注入物理口名），下游按 IR 口名
// 取值，所以运行期必须把**每个已声明**的拼写都发出来。拼写全部来自契约
// （builtin.PortOf），不按名字形态猜。`output` / `status` 是旧 runtime 引用
// 的兼容键，不属于契约口。
func httpResultPayload(body string, status int) map[string]any {
	out := map[string]any{
		"output":     body,
		"status":     status,
		MarkerBranch: "source",
	}
	values := map[string]any{
		"body":        body,
		"status_code": status,
		"headers":     map[string]string{},
	}
	for lang, v := range values {
		out[lang] = v
		for _, target := range builtin.DeclaredTargets() {
			if p, ok := builtin.PortOf(target, "http", lang); ok && p.Port != lang {
				out[p.Port] = v
			}
		}
	}
	return out
}

// do dispatches to an injected client's optional DoRequest fast path, else
// falls back to the unchanged HTTPClient.Do contract (header map + body).
func (h *httpNode) do(ctx context.Context, built host.Built) (int, []byte, error) {
	if rd, ok := h.hc.(requestDoer); ok {
		return rd.DoRequest(ctx, built)
	}
	return h.hc.Do(ctx, built.Method, built.URL, headerMap(built.Headers), built.Body)
}

// emitHostTrace records the host physical-field carry decision. Each record
// passes trace.Record.Validate(); trailing fill records repeat the base fields
// because Validate checks them per record.
func (h *httpNode) emitHostTrace(nheaders, nparams int) {
	if h.semTrace == nil {
		return
	}
	id := h.id
	if id == "" {
		id = "?"
	}
	origin := trace.OriginWireAbsent
	if nheaders > 0 || nparams > 0 {
		origin = trace.OriginWirePresent
	}
	base := trace.Record{
		Stage:    "host",
		RuleID:   trace.RuleHost,
		Origin:   origin,
		Evidence: "platform_model.yaml#node_kinds.http.host_fields",
		Node:     id,
		Kind:     spec.KindHTTP,
	}
	h.semTrace(base)
	if nheaders > 0 {
		r := base
		r.FillRule = "header_carry"
		r.Output = strconv.Itoa(nheaders)
		h.semTrace(r)
	}
	if nparams > 0 {
		r := base
		r.FillRule = "query_carry"
		r.Output = strconv.Itoa(nparams)
		h.semTrace(r)
	}
}

// emitRetryTrace records one attempt's effective timeout axis/budget. It
// mirrors the code node's retry record (1-based attempt) and passes
// trace.Record.Validate().
func (h *httpNode) emitRetryTrace(attempt int, axis string, deadlineMS int) {
	if h.semTrace == nil {
		return
	}
	id := h.id
	if id == "" {
		id = "?"
	}
	origin := trace.OriginWireAbsent
	if h.cfg.Exception != nil {
		origin = trace.OriginWirePresent
	}
	h.semTrace(trace.Record{
		Stage:      "retry",
		RuleID:     trace.RuleRetry,
		Origin:     origin,
		Evidence:   "observed:p3:nodes." + id + ".retry",
		DeadlineMS: deadlineMS,
		Node:       id,
		Kind:       spec.KindHTTP,
		FillRule:   "retry_attempt",
		Attempt:    attempt,
		Axis:       axis,
		Input:      "setting_on_error.retry",
		Output:     strconv.Itoa(attempt),
	})
}

// axisBudgetMS is the budget actually available to one attempt: the full
// per-attempt slice, the remaining share of the shared deadline, or 0 when the
// node declared no timeout budget. Shared by http/llm/tool so the trace
// deadline_ms column means the same thing on every axis.
func axisBudgetMS(axis string, timeout time.Duration, deadline time.Time, now time.Time) int {
	switch axis {
	case string(spec.TimeoutAxisPerAttempt):
		return int(timeout / time.Millisecond)
	case string(spec.TimeoutAxisTotal):
		if deadline.IsZero() {
			return 0
		}
		rem := deadline.Sub(now)
		if rem < 0 {
			return 0
		}
		return int(rem / time.Millisecond)
	}
	return 0
}

// exceptionTimeoutAxis normalizes the spec axis for runtime/trace
// consumption: only total and per_attempt are consumable axes; "none" and the
// empty string both mean "no node-level timeout".
func exceptionTimeoutAxis(ec *spec.ExceptionConfig) string {
	if ec == nil {
		return ""
	}
	switch ec.TimeoutAxis {
	case string(spec.TimeoutAxisTotal), string(spec.TimeoutAxisPerAttempt):
		return ec.TimeoutAxis
	}
	return ""
}

// effectiveTimeoutAxis is exceptionTimeoutAxis with the family default applied:
// llm/tool carry their timeout in settingOnError.timeoutMs, which the platform
// applies as one total budget, so an absent axis defaults to total. An explicit
// "none" still disables the node-level timeout.
func effectiveTimeoutAxis(ec *spec.ExceptionConfig) string {
	if ec != nil && ec.TimeoutAxis == string(spec.TimeoutAxisNone) {
		return ""
	}
	if a := exceptionTimeoutAxis(ec); a != "" {
		return a
	}
	return string(spec.TimeoutAxisTotal)
}

func headerMap(h http.Header) map[string]string {
	if len(h) == 0 {
		return nil
	}
	m := make(map[string]string, len(h))
	for k, vs := range h {
		if len(vs) > 0 {
			m[k] = vs[0]
		}
	}
	return m
}

// defaultHTTPClient adapts net/http to the HTTPClient interface.
type defaultHTTPClient struct{ client *http.Client }

// DoRequest performs the fully built request as-is (host.Build already set
// Content-Type and appended query parameters).
func (d *defaultHTTPClient) DoRequest(ctx context.Context, built host.Built) (int, []byte, error) {
	var rd io.Reader
	if built.Body != "" {
		rd = strings.NewReader(built.Body)
	}
	req, err := http.NewRequestWithContext(ctx, built.Method, built.URL, rd)
	if err != nil {
		return 0, nil, err
	}
	if len(built.Headers) > 0 {
		req.Header = built.Headers.Clone()
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, b, nil
}

// Do is the legacy HTTPClient entry point. It projects its arguments onto
// host.Built (headers sorted for determinism; no query params) and delegates
// to DoRequest, so Content-Type handling lives in one place.
func (d *defaultHTTPClient) Do(ctx context.Context, method, url string, headers map[string]string, body any) (int, []byte, error) {
	req := host.Request{Method: method, URL: url}
	if s, ok := body.(string); ok {
		req.Body = s
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		req.Headers = append(req.Headers, host.KV{Name: k, Value: headers[k]})
	}
	return d.DoRequest(ctx, host.Adapter{}.Build(req))
}
