package plugin

// http.go — HTTP Invocation（PluginHTTP 型）。
//
// 语义（对齐 coze-studio invocation_http.go 的 OpenAPI location 分组）：
//   - 参数按 ParamSchema.In 分组：path（URL 模板 {name}）/ query / header / body。
//   - 缺省参数取 schema.Default；未声明的额外参数忽略（宽容）。
//   - Auth 注入：none / api_key / bearer（值支持 ${ENV_VAR}）。
//   - 非 2xx 视为错误，错误信息携带 status 与响应体前若干字节。
//   - Result.Request 为审计串（method + path + header + query + body）。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// httpInvocation 实现 Invocation。
type httpInvocation struct{}

var _ Invocation = httpInvocation{}

func (httpInvocation) Do(ctx context.Context, args *CallArgs) (*Result, error) {
	entry := args.Entry
	p := entry.Plugin
	t := entry.Tool

	auth, err := p.Auth.Resolve()
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", p.ID, err)
	}

	// 1) 分组参数。
	grouped := groupHTTPArgs(t.Params, args.Args)

	// 2) URL：模板路径 + query。
	raw := strings.TrimRight(p.ServerURL, "/") + t.Path
	pathVals := map[string]string{}
	for k, v := range grouped[InPath] {
		pathVals[k] = stringify(v)
	}
	for k, v := range pathVals {
		raw = strings.ReplaceAll(raw, "{"+k+"}", url.PathEscape(v))
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: tool %s: bad url %q: %w", p.ID, t.Name, raw, err)
	}
	q := u.Query()
	for _, k := range sortedKeys(grouped[InQuery]) {
		switch v := grouped[InQuery][k].(type) {
		case []any:
			for _, e := range v {
				q.Add(k, stringify(e))
			}
		case []string:
			for _, e := range v {
				q.Add(k, e)
			}
		default:
			q.Add(k, stringify(grouped[InQuery][k]))
		}
	}
	u.RawQuery = q.Encode()

	// 3) body（JSON）。
	var bodyBytes []byte
	if len(grouped[InBody]) > 0 {
		bodyBytes, err = json.Marshal(grouped[InBody])
		if err != nil {
			return nil, fmt.Errorf("plugin %s: tool %s: marshal body: %w", p.ID, t.Name, err)
		}
	}

	// 4) 请求。
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(t.Method), u.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	if len(bodyBytes) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range grouped[InHeader] {
		req.Header.Set(k, stringify(v))
	}
	if auth != nil {
		switch auth.Type {
		case AuthAPIKey:
			req.Header.Set(auth.KeyHeader, auth.KeyValue)
		case AuthBearer:
			req.Header.Set("Authorization", "Bearer "+auth.Token)
		}
	}

	// 5) 发送。
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: tool %s: request failed: %w", p.ID, t.Name, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("plugin %s: tool %s: read response: %w", p.ID, t.Name, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body := string(respBody)
		if len(body) > 512 {
			body = body[:512] + "..."
		}
		return nil, fmt.Errorf("plugin %s: tool %s: http %d: %s", p.ID, t.Name, resp.StatusCode, body)
	}

	// 6) 结果：优先 JSON 解析，失败退回原始字符串。
	var data any = string(respBody)
	if len(respBody) > 0 {
		var parsed any
		if json.Unmarshal(respBody, &parsed) == nil {
			data = parsed
		}
	}

	return &Result{Request: auditRequest(req, grouped, bodyBytes), Data: data}, nil
}

// groupHTTPArgs 按 schema.In 分组；缺省取 Default；未声明参数放入 body（宽容）。
func groupHTTPArgs(params []ParamSchema, args map[string]any) map[ParamIn]map[string]any {
	grouped := map[ParamIn]map[string]any{
		InQuery:  {},
		InHeader: {},
		InPath:   {},
		InBody:   {},
	}
	declared := map[string]ParamSchema{}
	for _, ps := range params {
		declared[ps.Name] = ps
		if _, ok := grouped[ps.In]; !ok {
			grouped[InBody] = map[string]any{} // 防御：未知位置归 body
		}
	}
	for name, ps := range declared {
		if v, ok := args[name]; ok {
			grouped[ps.In][name] = v
		} else if ps.Default != nil {
			grouped[ps.In][name] = ps.Default
		}
	}
	// 额外未声明参数 → body（便于透传 payload）。
	for k, v := range args {
		if _, ok := declared[k]; !ok {
			grouped[InBody][k] = v
		}
	}
	return grouped
}

func auditRequest(req *http.Request, grouped map[ParamIn]map[string]any, body []byte) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", req.Method, req.URL.String())
	if len(grouped[InHeader]) > 0 {
		keys := sortedKeys(grouped[InHeader])
		for _, k := range keys {
			fmt.Fprintf(&b, "\nheader %s=%s", k, stringify(grouped[InHeader][k]))
		}
	}
	if len(body) > 0 {
		fmt.Fprintf(&b, "\nbody %s", string(body))
	}
	return b.String()
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}
