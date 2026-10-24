package spec

// exec_sem_p3_axis_test.go — P3 gate evidence for the timeout-axis carrier.
//
// ES-022a: IR→spec projection of BuildOptions.TimeoutAxes into
// ExceptionConfig.TimeoutAxis for every exception-carrying kind (LLM/HTTP/
// Tool/Agent/Code) plus HTTPConfig.SettingTimeoutSec (the http per-attempt
// `setting.timeout` carrier, seconds).
//
// Falsification contract: each projection is asserted as a **positive**
// non-empty axis. If any fromException call site drops nodeID/axes (e.g.
// reverting to `fromException(e)`), the axis becomes "" and this file goes
// red; no temporary source edit is needed to demonstrate it.

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
)

// p3AxisIR builds one four-family IR (plus agent, the fifth fromException call
// site, and an exception-less code node) without going through the frontend:
// axes are a BuildOptions fact, not derivable from the graph.
func p3AxisIR() (*ir.Graph, *flow.Projection) {
	nodes := []ir.Node{
		&ir.CodeRun{ID: "code_1", Exception: &ir.ExceptionConfig{MaxRetry: 3, TimeoutMS: 5000}},
		&ir.HTTPCall{ID: "http_1", Method: "GET", URL: "https://example.com", Exception: &ir.ExceptionConfig{MaxRetry: 3, TimeoutMS: 2000}},
		&ir.LLMCall{ID: "llm_1", Model: "m", Exception: &ir.ExceptionConfig{MaxRetry: 1, TimeoutMS: 180000}},
		&ir.ToolCall{ID: "tool_1", ToolName: "t", ProviderID: "p", Exception: &ir.ExceptionConfig{TimeoutMS: 60000}},
		&ir.AgentCall{ID: "agent_1", Model: "m", Exception: &ir.ExceptionConfig{TimeoutMS: 60000}},
		&ir.CodeRun{ID: "code_no_exc"},
	}
	return &ir.Graph{Entry: &ir.Start{ID: "1"}}, &flow.Projection{Nodes: nodes}
}

func p3BuildSpec(t *testing.T, axes map[string]ExceptionTimeoutAxis) (*Spec, error) {
	t.Helper()
	g, p := p3AxisIR()
	return Build(g, p, BuildOptions{TimeoutAxes: axes})
}

func p3MustBuild(t *testing.T, axes map[string]ExceptionTimeoutAxis) *Spec {
	t.Helper()
	s, err := p3BuildSpec(t, axes)
	if err != nil {
		t.Fatalf("Build(TimeoutAxes=%v): %v", axes, err)
	}
	return s
}

func p3Node(t *testing.T, s *Spec, id string) *NodeSpec {
	t.Helper()
	for i := range s.Nodes {
		if s.Nodes[i].ID == id {
			return &s.Nodes[i]
		}
	}
	t.Fatalf("spec has no node %q", id)
	return nil
}

func p3Exception(t *testing.T, s *Spec, id string) *ExceptionConfig {
	t.Helper()
	ns := p3Node(t, s, id)
	cfg, err := DecodeConfig(ns.Kind, ns.Config)
	if err != nil {
		t.Fatalf("decode %s (%s): %v", id, ns.Kind, err)
	}
	switch c := cfg.(type) {
	case *LLMConfig:
		return c.Exception
	case *HTTPConfig:
		return c.Exception
	case *ToolConfig:
		return c.Exception
	case *AgentConfig:
		return c.Exception
	case *CodeConfig:
		return c.Exception
	}
	t.Fatalf("node %s: kind %s carries no exception config", id, ns.Kind)
	return nil
}

func p3HTTPConfig(t *testing.T, s *Spec, id string) *HTTPConfig {
	t.Helper()
	ns := p3Node(t, s, id)
	cfg, err := DecodeConfig(ns.Kind, ns.Config)
	if err != nil {
		t.Fatalf("decode %s: %v", id, err)
	}
	hc, ok := cfg.(*HTTPConfig)
	if !ok {
		t.Fatalf("node %s decoded as %T, want *HTTPConfig", id, cfg)
	}
	return hc
}

// TestExecSemP3ES022aAxisProjection locks all five fromException call sites:
// the injected axis reaches the spec, and the http per-attempt carrier is
// projected as whole seconds while its TimeoutMS bytes stay intact.
func TestExecSemP3ES022aAxisProjection(t *testing.T) {
	axes := map[string]ExceptionTimeoutAxis{
		"code_1":  TimeoutAxisTotal,
		"http_1":  TimeoutAxisPerAttempt,
		"llm_1":   TimeoutAxisTotal,
		"tool_1":  TimeoutAxisTotal,
		"agent_1": TimeoutAxisTotal,
	}
	s := p3MustBuild(t, axes)

	for id, want := range axes {
		ec := p3Exception(t, s, id)
		if ec == nil {
			t.Fatalf("node %s: exception config dropped entirely", id)
		}
		if ec.TimeoutAxis != string(want) {
			t.Fatalf("node %s: TimeoutAxis = %q, want %q (fromException must consume nodeID+axes)", id, ec.TimeoutAxis, want)
		}
	}

	hc := p3HTTPConfig(t, s, "http_1")
	if hc.Exception.TimeoutMS != 2000 {
		t.Fatalf("http TimeoutMS = %d, want the carrier bytes 2000", hc.Exception.TimeoutMS)
	}
	if hc.SettingTimeoutSec != 2 {
		t.Fatalf("http per_attempt SettingTimeoutSec = %d, want TimeoutMS/1000 = 2", hc.SettingTimeoutSec)
	}
}

// TestExecSemP3ES022aTotalHasNoSettingTimeoutSec locks the mutual exclusion:
// the total axis never projects a second (per-attempt) carrier, so
// SettingTimeoutSec != 0 under total is impossible by construction.
func TestExecSemP3ES022aTotalHasNoSettingTimeoutSec(t *testing.T) {
	s := p3MustBuild(t, map[string]ExceptionTimeoutAxis{"http_1": TimeoutAxisTotal})
	hc := p3HTTPConfig(t, s, "http_1")
	if hc.Exception.TimeoutAxis != string(TimeoutAxisTotal) {
		t.Fatalf("http TimeoutAxis = %q, want total", hc.Exception.TimeoutAxis)
	}
	if hc.Exception.TimeoutMS != 2000 {
		t.Fatalf("http total TimeoutMS = %d, want 2000", hc.Exception.TimeoutMS)
	}
	if hc.SettingTimeoutSec != 0 {
		t.Fatalf("http total SettingTimeoutSec = %d, want 0 (carriers must not stack)", hc.SettingTimeoutSec)
	}
}

// TestExecSemP3ES022aMissingAxisIsNone locks the absent-axis default: no entry
// means "none" (""), an explicit none stays "none", and an axis keyed to a node
// without an exception stanza never invents one (e == nil ⇒ nil).
func TestExecSemP3ES022aMissingAxisIsNone(t *testing.T) {
	s := p3MustBuild(t, nil)
	for _, id := range []string{"code_1", "http_1", "llm_1", "tool_1", "agent_1"} {
		if ec := p3Exception(t, s, id); ec == nil || ec.TimeoutAxis != "" {
			t.Fatalf("node %s without an axis entry: exception = %+v, want TimeoutAxis \"\"", id, ec)
		}
	}
	if hc := p3HTTPConfig(t, s, "http_1"); hc.SettingTimeoutSec != 0 {
		t.Fatalf("http without an axis: SettingTimeoutSec = %d, want 0", hc.SettingTimeoutSec)
	}

	s = p3MustBuild(t, map[string]ExceptionTimeoutAxis{"http_1": TimeoutAxisNone})
	if ec := p3Exception(t, s, "http_1"); ec == nil || ec.TimeoutAxis != string(TimeoutAxisNone) {
		t.Fatalf("explicit none axis = %+v, want %q", ec, TimeoutAxisNone)
	}

	s = p3MustBuild(t, map[string]ExceptionTimeoutAxis{"code_no_exc": TimeoutAxisTotal})
	if ec := p3Exception(t, s, "code_no_exc"); ec != nil {
		t.Fatalf("e == nil must stay nil, got %+v", ec)
	}
}

// TestExecSemP3ES022aAxisErrors locks fail-loud validation: an unknown axis
// value, an id that is not a node of this graph, and a per-attempt carrier
// that is not a whole number of seconds.
func TestExecSemP3ES022aAxisErrors(t *testing.T) {
	cases := []struct {
		name string
		axes map[string]ExceptionTimeoutAxis
		want []string
	}{
		{"invalid value", map[string]ExceptionTimeoutAxis{"http_1": "sometimes"}, []string{"http_1", "sometimes"}},
		{"unknown node id", map[string]ExceptionTimeoutAxis{"ghost_1": TimeoutAxisTotal}, []string{"ghost_1", "unknown node"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p3BuildSpec(t, tc.axes)
			if err == nil {
				t.Fatalf("Build(TimeoutAxes=%v) = nil error, want fail-loud", tc.axes)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Fatalf("error %q does not name %q", err, w)
				}
			}
		})
	}

	g := &ir.Graph{Entry: &ir.Start{ID: "1"}}
	p := &flow.Projection{Nodes: []ir.Node{&ir.HTTPCall{
		ID: "http_1", Method: "GET", URL: "https://example.com",
		Exception: &ir.ExceptionConfig{TimeoutMS: 2500},
	}}}
	_, err := Build(g, p, BuildOptions{TimeoutAxes: map[string]ExceptionTimeoutAxis{"http_1": TimeoutAxisPerAttempt}})
	if err == nil {
		t.Fatal("non-whole-second per_attempt carrier must fail, not truncate")
	}
	for _, w := range []string{"http_1", "2500"} {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("error %q does not name %q", err, w)
		}
	}
}

// TestExecSemP3ES046DataOnErrCarried locks the PT2 fallback payload projection:
// `with default(<record>)` is an IR fact, so spec must carry every declared
// field verbatim (ES-046 falsification: an empty payload would make the runtime
// fail loud instead of returning what the author wrote).
func TestExecSemP3ES046DataOnErrCarried(t *testing.T) {
	g := &ir.Graph{Entry: &ir.Start{ID: "1"}}
	p := &flow.Projection{Nodes: []ir.Node{&ir.CodeRun{
		ID: "code_1",
		Exception: &ir.ExceptionConfig{
			ProcessType: p3IntPT(2),
			DefaultFields: []ir.DefaultField{
				{Name: "result", Value: "fallback"},
				{Name: "count", Value: int64(0)},
			},
		},
	}}}
	s, err := Build(g, p, BuildOptions{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ec := p3Exception(t, s, "code_1")
	if ec == nil {
		t.Fatal("code_1: exception dropped")
	}
	if ec.ProcessType == nil || *ec.ProcessType != 2 {
		t.Fatalf("process_type = %v, want 2", ec.ProcessType)
	}
	if len(ec.DataOnErr) != 2 {
		t.Fatalf("data_on_err = %+v, want 2 fields in author order", ec.DataOnErr)
	}
	if ec.DataOnErr[0].Name != "result" || ec.DataOnErr[0].Value != "fallback" {
		t.Fatalf("data_on_err[0] = %+v, want result=fallback", ec.DataOnErr[0])
	}
	// The literal's Go width may be normalised on the IR→projection hop
	// (int64 vs int); the JSON value is the same. Assert the value, not the
	// incidental Go type.
	if ec.DataOnErr[1].Name != "count" || !p3NumericZero(ec.DataOnErr[1].Value) {
		t.Fatalf("data_on_err[1] = %+v (%T), want count=0", ec.DataOnErr[1], ec.DataOnErr[1].Value)
	}

	// No payload stays nil: PT2-with-no-payload must be distinguishable.
	g2 := &ir.Graph{Entry: &ir.Start{ID: "1"}}
	p2 := &flow.Projection{Nodes: []ir.Node{&ir.CodeRun{
		ID: "code_2", Exception: &ir.ExceptionConfig{ProcessType: p3IntPT(2)},
	}}}
	s2, err := Build(g2, p2, BuildOptions{})
	if err != nil {
		t.Fatalf("Build(no payload): %v", err)
	}
	if ec := p3Exception(t, s2, "code_2"); len(ec.DataOnErr) != 0 {
		t.Fatalf("data_on_err = %+v, want empty", ec.DataOnErr)
	}
}

func p3IntPT(v int) *int { return &v }

func p3NumericZero(v any) bool {
	switch n := v.(type) {
	case int:
		return n == 0
	case int64:
		return n == 0
	case float64:
		return n == 0
	default:
		return false
	}
}
