package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPGroupingAndAuth(t *testing.T) {
	var gotPath, gotAuth, gotQuery, gotHeader, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotHeader = r.Header.Get("X-Trace")
		b := make([]byte, 4096)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"result":"ok","n":1}`)
	}))
	defer srv.Close()

	m := New()
	p := &Plugin{
		ID: "http.demo", Version: "1.0.0", Type: PluginHTTP,
		ServerURL: srv.URL,
		Auth:      &AuthConfig{Type: AuthBearer, Token: "tok-${HTTP_DEMO_TOKEN}"},
		Tools: []Tool{{
			Name: "call", Method: "POST", Path: "/v1/{kind}/go",
			Params: []ParamSchema{
				{Name: "kind", Type: "string", Required: true, In: InPath},
				{Name: "q", Type: "string", Required: true, In: InQuery},
				{Name: "X-Trace", Type: "string", In: InHeader},
				{Name: "payload", Type: "object", In: InBody},
			},
		}},
	}
	t.Setenv("HTTP_DEMO_TOKEN", "secret")
	if err := m.Register(p); err != nil {
		t.Fatal(err)
	}

	got, err := m.Call(context.Background(), "http.demo/call", map[string]any{
		"kind":    "ping",
		"q":       "hello world",
		"X-Trace": "t1",
		"payload": map[string]any{"a": 1},
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if gotPath != "/v1/ping/go" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "q=hello+world") {
		t.Fatalf("query = %q", gotQuery)
	}
	if gotAuth != "Bearer tok-secret" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotHeader != "t1" {
		t.Fatalf("header = %q", gotHeader)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	payload, ok := body["payload"].(map[string]any)
	if !ok || payload["a"] != float64(1) {
		t.Fatalf("body = %v", body)
	}
	// 结果：JSON 解析
	data, ok := got.(map[string]any)
	if !ok || data["result"] != "ok" {
		t.Fatalf("data = %#v", got)
	}
}

func TestHTTPDefaultParamsAndExtraToBody(t *testing.T) {
	var gotQuery, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		b := make([]byte, 4096)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	m := New()
	p := &Plugin{
		ID: "http.def", Type: PluginHTTP, ServerURL: srv.URL,
		Tools: []Tool{{
			Name: "call", Method: "POST", Path: "/x",
			Params: []ParamSchema{
				{Name: "max", Type: "int", Default: 5, In: InQuery},
				{Name: "named", Type: "string", In: InBody},
			},
		}},
	}
	if err := m.Register(p); err != nil {
		t.Fatal(err)
	}
	// 未传 max → 默认 5；extra 未声明参数 → body。
	if _, err := m.Call(context.Background(), "http.def/call", map[string]any{"extra": "e"}); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if gotQuery != "max=5" {
		t.Fatalf("query = %q (want max=5)", gotQuery)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body["extra"] != "e" {
		t.Fatalf("body = %v", body)
	}
}

func TestHTTPNon2xxError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, `upstream broke`)
	}))
	defer srv.Close()

	m := New()
	p := &Plugin{ID: "http.err", Type: PluginHTTP, ServerURL: srv.URL,
		Tools: []Tool{{Name: "call", Method: "GET", Path: "/x"}}}
	if err := m.Register(p); err != nil {
		t.Fatal(err)
	}
	_, err := m.Call(context.Background(), "http.err/call", nil)
	if err == nil || !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "upstream broke") {
		t.Fatalf("err = %v", err)
	}
}

func TestHTTPMissingAuthEnv(t *testing.T) {
	m := New()
	p := &Plugin{ID: "http.a", Type: PluginHTTP, ServerURL: "http://x",
		Auth:  &AuthConfig{Type: AuthAPIKey, KeyHeader: "X-Key", KeyValue: "${NOPE_NOPE}"},
		Tools: []Tool{{Name: "call", Method: "GET", Path: "/x"}}}
	if err := m.Register(p); err != nil {
		t.Fatal(err)
	}
	_, err := m.Call(context.Background(), "http.a/call", nil)
	if err == nil || !strings.Contains(err.Error(), "NOPE_NOPE") {
		t.Fatalf("err = %v", err)
	}
}
