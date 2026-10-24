package nodes

// http_host_test.go — P2 host slice mechanism proof (ES-030/031/099/107b).
//
// All servers are local httptest instances; no external network. A real
// httpNode runs end-to-end through defaultHTTPClient so the assertions cover
// the actual wire request (headers, query, body), not an intermediate map.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/trace"
)

type capturedRequest struct {
	Method   string
	Header   http.Header
	RawQuery string
	Body     string
}

func newEchoServer(t *testing.T) (*httptest.Server, *capturedRequest) {
	t.Helper()
	got := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.Method = r.Method
		got.Header = r.Header.Clone()
		got.RawQuery = r.URL.RawQuery
		got.Body = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func invokeHost(t *testing.T, cfg *spec.HTTPConfig, in map[string]any, semTrace func(trace.Record), client *http.Client) map[string]any {
	t.Helper()
	impl, err := (HTTP{}).Build(context.Background(), httpNodeSpec(cfg), Deps{
		HTTP:     &defaultHTTPClient{client: client},
		SemTrace: semTrace,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	out, err := impl.Invoke(context.Background(), in)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	return out
}

// ES-030/ES-107b: source headers (and rendered template values) reach the
// wire verbatim, and an explicit source content-type beats the JSON default.
func TestExecSemHTTPHostFieldsCarry(t *testing.T) {
	srv, got := newEchoServer(t)
	cfg := &spec.HTTPConfig{
		Method: "POST",
		URL:    srv.URL + "/echo",
		Body:   `{"k":"{{v}}"}`,
		Headers: []spec.HTTPKV{
			{Name: "x-requested-with", Value: "XMLHttpRequest"},
			{Name: "X-Custom", Value: "{{custom}}"},
			{Name: "content-type", Value: "application/x-www-form-urlencoded; charset=UTF-8"},
		},
	}
	out := invokeHost(t, cfg, map[string]any{"v": "1", "custom": "hello"}, nil, srv.Client())

	if got.Method != "POST" {
		t.Errorf("method = %q, want POST", got.Method)
	}
	if v := got.Header.Get("X-Requested-With"); v != "XMLHttpRequest" {
		t.Errorf("X-Requested-With = %q, want XMLHttpRequest", v)
	}
	if v := got.Header.Get("X-Custom"); v != "hello" {
		t.Errorf("X-Custom = %q, want rendered hello", v)
	}
	if v := got.Header.Get("Content-Type"); v != "application/x-www-form-urlencoded; charset=UTF-8" {
		t.Errorf("Content-Type = %q, want source value verbatim", v)
	}
	if got.Body != `{"k":"1"}` {
		t.Errorf("body = %q, want rendered JSON", got.Body)
	}
	if out["status_code"] != http.StatusOK {
		t.Errorf("status_code = %v", out["status_code"])
	}
}

// ES-031: query params append without overriding/duplicating an existing key.
func TestExecSemHTTPQueryCarry(t *testing.T) {
	t.Run("existing key preserved", func(t *testing.T) {
		srv, got := newEchoServer(t)
		cfg := &spec.HTTPConfig{
			Method: "GET",
			URL:    srv.URL + "/echo?a=1",
			Params: []spec.HTTPKV{
				{Name: "b", Value: "2"},
				{Name: "a", Value: "9"},
			},
		}
		invokeHost(t, cfg, nil, nil, srv.Client())
		if got.RawQuery != "a=1&b=2" {
			t.Fatalf("query = %q, want a=1&b=2", got.RawQuery)
		}
	})

	t.Run("param order preserved", func(t *testing.T) {
		srv, got := newEchoServer(t)
		cfg := &spec.HTTPConfig{
			Method: "GET",
			URL:    srv.URL + "/echo",
			Params: []spec.HTTPKV{{Name: "z", Value: "1"}, {Name: "a", Value: "{{v}}"}},
		}
		invokeHost(t, cfg, map[string]any{"v": "2"}, nil, srv.Client())
		if got.RawQuery != "z=1&a=2" {
			t.Fatalf("query = %q, want z=1&a=2", got.RawQuery)
		}
	})
}

// ES-099: the host carry decision is traced, valid against the record
// contract, and silent on the wire-absent path.
func TestExecSemHTTPHostTrace(t *testing.T) {
	t.Run("wire present", func(t *testing.T) {
		srv, _ := newEchoServer(t)
		var recs []trace.Record
		cfg := &spec.HTTPConfig{
			Method: "POST",
			URL:    srv.URL + "/echo",
			Body:   `{}`,
			Headers: []spec.HTTPKV{
				{Name: "x-requested-with", Value: "XMLHttpRequest"},
				{Name: "X-Custom", Value: "v"},
			},
			Params: []spec.HTTPKV{{Name: "q", Value: "1"}},
		}
		invokeHost(t, cfg, nil, func(r trace.Record) { recs = append(recs, r) }, srv.Client())

		base := findTrace(recs, "", "")
		if base == nil {
			t.Fatalf("no SEM-HOST record: %#v", recs)
		}
		if base.Stage != "host" || base.Kind != spec.KindHTTP || base.Node != "h1" {
			t.Errorf("base = %+v", *base)
		}
		if base.Origin != trace.OriginWirePresent {
			t.Errorf("origin = %q, want wire-present", base.Origin)
		}
		if base.Evidence != "platform_model.yaml#node_kinds.http.host_fields" {
			t.Errorf("evidence = %q", base.Evidence)
		}
		if base.Attempt != 0 {
			t.Errorf("attempt = %d, want unset (0)", base.Attempt)
		}

		if r := findTrace(recs, "header_carry", ""); r == nil || r.Output != "2" {
			t.Errorf("header_carry = %+v, want output 2", r)
		}
		if r := findTrace(recs, "query_carry", ""); r == nil || r.Output != "1" {
			t.Errorf("query_carry = %+v, want output 1", r)
		}
		for _, r := range recs {
			if err := r.Validate(); err != nil {
				t.Errorf("record invalid: %v (%+v)", err, r)
			}
		}
	})

	t.Run("wire absent", func(t *testing.T) {
		srv, _ := newEchoServer(t)
		var recs []trace.Record
		cfg := &spec.HTTPConfig{Method: "GET", URL: srv.URL + "/echo"}
		invokeHost(t, cfg, nil, func(r trace.Record) { recs = append(recs, r) }, srv.Client())

		base := findTrace(recs, "", "")
		if base == nil || base.Origin != trace.OriginWireAbsent {
			t.Fatalf("base = %+v, want wire-absent", base)
		}
		for _, r := range recs {
			if r.FillRule != "" {
				t.Errorf("unexpected fill record on wire-absent path: %+v", r)
			}
			if err := r.Validate(); err != nil {
				t.Errorf("record invalid: %v (%+v)", err, r)
			}
		}
	})
}

func findTrace(recs []trace.Record, fillRule, output string) *trace.Record {
	for i := range recs {
		r := recs[i]
		if r.RuleID != trace.RuleHost {
			continue
		}
		if r.FillRule != fillRule {
			continue
		}
		if output != "" && r.Output != output {
			continue
		}
		return &recs[i]
	}
	return nil
}
