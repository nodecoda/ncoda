package nodes

// exec_sem_p3_axis_test.go — P3 runtime axis consumption.
//
// Covers:
//   ES-100  axis swap: code sharing one total budget vs http per-attempt
//   ES-113  http retry-only total budget (second attempt has the remainder)
//   ES-022b trace axis/attempt/deadline_ms matches the spec axis
//   ES-023b http retries keep their own setting.timeout slice
//   ES-044  llm/tool ValueBoundary is a no-op (no declared types yet):
//           absent/null pass through verbatim, exactly like code with no decl
//
// Determinism: no network, no real sleeps. The http node's clock is injected
// for the shared-budget arithmetic (ES-113 advances it 100s per attempt), and
// every fake only records ctx deadlines.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/trace"
)

// ── fakes ────────────────────────────────────────────────────────────────

// p3HTTPClient records the ctx deadline of every call. before() runs after the
// record so it can advance a fake clock between attempts.
type p3HTTPClient struct {
	calls     int
	deadlines []time.Time
	fail      bool
	before    func()
}

func (c *p3HTTPClient) Do(ctx context.Context, method, url string, headers map[string]string, body any) (int, []byte, error) {
	c.calls++
	dl, ok := ctx.Deadline()
	if !ok {
		dl = time.Time{}
	}
	c.deadlines = append(c.deadlines, dl)
	if c.before != nil {
		c.before()
	}
	if c.fail {
		return 0, nil, errors.New("boom")
	}
	return 200, []byte("ok"), nil
}

type p3Model struct {
	calls     int
	failUntil int
	deadlines []time.Time
	got       []Message
}

func (m *p3Model) Chat(ctx context.Context, model string, msgs []Message) (string, error) {
	m.calls++
	m.got = msgs
	dl, ok := ctx.Deadline()
	if !ok {
		dl = time.Time{}
	}
	m.deadlines = append(m.deadlines, dl)
	if m.calls <= m.failUntil {
		return "", errors.New("boom")
	}
	return "ok", nil
}

type p3ToolReg struct {
	names     []string
	calls     int
	failUntil int
	deadlines []time.Time
	args      map[string]any
}

func (r *p3ToolReg) Names() []string { return r.names }

func (r *p3ToolReg) Call(ctx context.Context, name string, args map[string]any) (any, error) {
	r.calls++
	r.args = args
	dl, ok := ctx.Deadline()
	if !ok {
		dl = time.Time{}
	}
	r.deadlines = append(r.deadlines, dl)
	if r.calls <= r.failUntil {
		return nil, errors.New("boom")
	}
	return "ok", nil
}

// ── helpers ──────────────────────────────────────────────────────────────

func p3Build(t *testing.T, id, kind string, cfg any, deps Deps) NodeImpl {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal %s config: %v", kind, err)
	}
	impl, err := StdRegistry().Build(context.Background(), &spec.NodeSpec{ID: id, Kind: kind, Config: raw}, deps)
	if err != nil {
		t.Fatalf("build %s %s: %v", kind, id, err)
	}
	return impl
}

func p3RetryRecords(recs []trace.Record) []trace.Record {
	var out []trace.Record
	for _, r := range recs {
		if r.Stage == "retry" {
			out = append(out, r)
		}
	}
	return out
}

// ── ES-100: code total vs http per-attempt ───────────────────────────────

// TestExecSemP3ES100CodeSharedTotalAxis locks the code half of the axis swap:
// settingOnError.retryTimes=3 + timeoutMs=5000 is one budget shared by every
// attempt (code.go sets the deadline once, outside the retry loop). code's
// trace reports the declared total per attempt, so the assertion is
// non-increasing (never refilled upward), not strictly decreasing.
func TestExecSemP3ES100CodeSharedTotalAxis(t *testing.T) {
	emit, recs := collectTrace(t)
	cfg := &spec.CodeConfig{
		Code:      "def main(x) -> dict:\n    return {\"result\": missing_name}",
		Language:  "python",
		Synthetic: true,
		Exception: &spec.ExceptionConfig{MaxRetry: 3, TimeoutMS: 5000, TimeoutAxis: "total"},
		OutOrder:  []string{"result"},
	}
	n := buildP1a2Code(t, "code_100", cfg, emit)
	if _, err := n.Invoke(context.Background(), map[string]any{"x": "v"}); err == nil {
		t.Fatal("undefined shim name must fail after retries")
	}
	retries := p3RetryRecords(*recs)
	// retryTimes=3 ⇒ 1 + 3 attempts (existing code contract, ES-023a).
	if len(retries) != 4 {
		t.Fatalf("retry records = %d, want 4 (1 + 3 retries)", len(retries))
	}
	prev := 0
	for i, r := range retries {
		if r.Axis != "total" {
			t.Fatalf("attempt %d axis = %q, want total", i+1, r.Axis)
		}
		if r.Attempt != i+1 {
			t.Fatalf("attempt = %d, want %d (1-based)", r.Attempt, i+1)
		}
		if r.DeadlineMS != 5000 {
			t.Fatalf("attempt %d deadline_ms = %d, want declared total 5000", i+1, r.DeadlineMS)
		}
		if i > 0 && r.DeadlineMS > prev {
			t.Fatalf("attempt %d refilled the budget (%d > %d)", i+1, r.DeadlineMS, prev)
		}
		prev = r.DeadlineMS
	}
}

// TestExecSemP3ES100HTTPPerAttemptAxis locks the http half: setting.timeout=2s
// is a fresh slice per attempt (axis=per_attempt), never a shared budget.
func TestExecSemP3ES100HTTPPerAttemptAxis(t *testing.T) {
	emit, recs := collectTrace(t)
	client := &p3HTTPClient{fail: true}
	cfg := &spec.HTTPConfig{
		Method: "GET", URL: "https://example.com",
		Exception:         &spec.ExceptionConfig{MaxRetry: 3, TimeoutMS: 2000, TimeoutAxis: "per_attempt"},
		SettingTimeoutSec: 2,
	}
	n := p3Build(t, "http_100", spec.KindHTTP, cfg, Deps{HTTP: client, SemTrace: emit})
	if _, err := n.Invoke(context.Background(), nil); err == nil {
		t.Fatal("always-failing client must surface a node error")
	}
	if client.calls != 4 {
		t.Fatalf("Do calls = %d, want 4 (1 + 3 retries)", client.calls)
	}
	retries := p3RetryRecords(*recs)
	if len(retries) != 4 {
		t.Fatalf("retry records = %d, want 4 (1 + 3 retries)", len(retries))
	}
	for i, r := range retries {
		if r.Axis != "per_attempt" {
			t.Fatalf("attempt %d axis = %q, want per_attempt", i+1, r.Axis)
		}
		if r.Attempt != i+1 {
			t.Fatalf("attempt = %d, want %d (1-based)", r.Attempt, i+1)
		}
		if r.DeadlineMS != 2000 {
			t.Fatalf("attempt %d deadline_ms = %d, want per-attempt 2000", i+1, r.DeadlineMS)
		}
	}
	// A fresh WithTimeout per attempt ⇒ strictly later absolute deadlines.
	for i := 1; i < len(client.deadlines); i++ {
		if client.deadlines[i].IsZero() || !client.deadlines[i].After(client.deadlines[i-1]) {
			t.Fatalf("attempt %d deadline %v is not a fresh slice after %v", i+1, client.deadlines[i], client.deadlines[i-1])
		}
	}
}

// ── ES-113: http retry-only total budget ─────────────────────────────────

// TestExecSemP3ES113HTTPTotalBudgetSharedAcrossRetries: setting{retryTimes:2} +
// settingOnError.timeoutMs=180000 and each attempt "takes" 100s. The shared
// deadline is computed once, so attempt 2 has ~80s left and attempt 3 none.
// Time is advanced through the injected clock — nothing sleeps here.
func TestExecSemP3ES113HTTPTotalBudgetSharedAcrossRetries(t *testing.T) {
	emit, recs := collectTrace(t)
	clock := time.Now()
	client := &p3HTTPClient{fail: true}
	client.before = func() { clock = clock.Add(100 * time.Second) }
	cfg := &spec.HTTPConfig{
		Method: "GET", URL: "https://example.com",
		Exception: &spec.ExceptionConfig{MaxRetry: 2, TimeoutMS: 180000, TimeoutAxis: "total"},
	}
	impl := p3Build(t, "http_113", spec.KindHTTP, cfg, Deps{HTTP: client, SemTrace: emit})
	hn, ok := impl.(*httpNode)
	if !ok {
		t.Fatalf("http build returned %T, want *httpNode", impl)
	}
	hn.now = func() time.Time { return clock }

	if _, err := hn.Invoke(context.Background(), nil); err == nil {
		t.Fatal("always-failing client must surface a node error")
	}
	if client.calls != 3 {
		t.Fatalf("Do calls = %d, want 3 (1 + 2 retries)", client.calls)
	}
	retries := p3RetryRecords(*recs)
	want := []int{180000, 80000, 0}
	if len(retries) != len(want) {
		t.Fatalf("retry records = %d, want %d", len(retries), len(want))
	}
	for i, r := range retries {
		if r.Axis != "total" {
			t.Fatalf("attempt %d axis = %q, want total", i+1, r.Axis)
		}
		if r.Attempt != i+1 {
			t.Fatalf("attempt = %d, want %d (1-based)", r.Attempt, i+1)
		}
		if r.DeadlineMS != want[i] {
			t.Fatalf("attempt %d deadline_ms = %d, want %d (shared budget remainder)", i+1, r.DeadlineMS, want[i])
		}
	}
	// Shared budget: every attempt receives the same absolute deadline.
	for i := 1; i < len(client.deadlines); i++ {
		if !client.deadlines[i].Equal(client.deadlines[0]) {
			t.Fatalf("attempt %d deadline %v != attempt 1 deadline %v (budget was refilled)",
				i+1, client.deadlines[i], client.deadlines[0])
		}
	}
}

// ── http axis edge cases ─────────────────────────────────────────────────

// TestExecSemP3HTTPAxisNoneInstallsNoNodeTimeout: axis=none/"" leaves the node
// without a node-level deadline; retry records still pass Validate with an
// empty axis and a zero budget.
func TestExecSemP3HTTPAxisNoneInstallsNoNodeTimeout(t *testing.T) {
	emit, recs := collectTrace(t)
	client := &p3HTTPClient{fail: true}
	cfg := &spec.HTTPConfig{
		Method: "GET", URL: "https://example.com",
		Exception: &spec.ExceptionConfig{MaxRetry: 1, TimeoutMS: 2000, TimeoutAxis: "none"},
	}
	n := p3Build(t, "http_none", spec.KindHTTP, cfg, Deps{HTTP: client, SemTrace: emit})
	if _, err := n.Invoke(context.Background(), nil); err == nil {
		t.Fatal("always-failing client must surface a node error")
	}
	for i, dl := range client.deadlines {
		if !dl.IsZero() {
			t.Fatalf("attempt %d got deadline %v, want none", i+1, dl)
		}
	}
	retries := p3RetryRecords(*recs)
	if len(retries) != 2 {
		t.Fatalf("retry records = %d, want 2", len(retries))
	}
	for _, r := range retries {
		if r.Axis != "" || r.DeadlineMS != 0 {
			t.Fatalf("none-axis record = axis %q / deadline %d, want \"\"/0", r.Axis, r.DeadlineMS)
		}
	}
}

// TestExecSemP3HTTPTotalSingleAttempt: AC5(b) — axis=total with retryTimes=0
// still gives the single attempt the full total budget, and emits no retry
// record (single calls stay quiet).
func TestExecSemP3HTTPTotalSingleAttempt(t *testing.T) {
	emit, recs := collectTrace(t)
	client := &p3HTTPClient{}
	cfg := &spec.HTTPConfig{
		Method: "GET", URL: "https://example.com",
		Exception: &spec.ExceptionConfig{TimeoutMS: 5000, TimeoutAxis: "total"},
	}
	n := p3Build(t, "http_single", spec.KindHTTP, cfg, Deps{HTTP: client, SemTrace: emit})
	if _, err := n.Invoke(context.Background(), nil); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if got := p3RetryRecords(*recs); len(got) != 0 {
		t.Fatalf("single attempt emitted %d retry records, want 0", len(got))
	}
	if len(client.deadlines) != 1 || client.deadlines[0].IsZero() {
		t.Fatalf("single attempt deadlines = %v, want one total-budget deadline", client.deadlines)
	}
	if rem := time.Until(client.deadlines[0]); rem <= 4900*time.Millisecond || rem > 5*time.Second {
		t.Fatalf("single attempt remaining budget = %v, want ~5s", rem)
	}
}

// ── llm: total / per-attempt / family default ────────────────────────────

func TestExecSemP3LLMSharedTotalAxis(t *testing.T) {
	emit, recs := collectTrace(t)
	m := &p3Model{failUntil: 2}
	cfg := &spec.LLMConfig{
		Model: "m", Result: stringLLMResult(),
		Exception: &spec.ExceptionConfig{MaxRetry: 2, TimeoutMS: 60000, TimeoutAxis: "total"},
	}
	n := p3Build(t, "llm_total", spec.KindLLM, cfg, Deps{Models: m, SemTrace: emit})
	if _, err := n.Invoke(context.Background(), map[string]any{"input": "q"}); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if m.calls != 3 {
		t.Fatalf("chat calls = %d, want 3 (1 + 2 retries)", m.calls)
	}
	assertP3SharedDeadlines(t, m.deadlines)
	assertP3RetryAxis(t, p3RetryRecords(*recs), "total", 60000)
}

// TestExecSemP3LLMAbsentAxisDefaultsToTotal: the llm family carrier is
// settingOnError.timeoutMs (platform total), so an absent axis must behave as
// total instead of the old per-attempt slice.
func TestExecSemP3LLMAbsentAxisDefaultsToTotal(t *testing.T) {
	emit, recs := collectTrace(t)
	m := &p3Model{failUntil: 1}
	cfg := &spec.LLMConfig{
		Model: "m", Result: stringLLMResult(),
		Exception: &spec.ExceptionConfig{MaxRetry: 1, TimeoutMS: 60000},
	}
	n := p3Build(t, "llm_default", spec.KindLLM, cfg, Deps{Models: m, SemTrace: emit})
	if _, err := n.Invoke(context.Background(), map[string]any{"input": "q"}); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	assertP3SharedDeadlines(t, m.deadlines)
	assertP3RetryAxis(t, p3RetryRecords(*recs), "total", 60000)
}

func TestExecSemP3LLMPerAttemptAxis(t *testing.T) {
	emit, recs := collectTrace(t)
	m := &p3Model{failUntil: 2}
	cfg := &spec.LLMConfig{
		Model: "m", Result: stringLLMResult(),
		Exception: &spec.ExceptionConfig{MaxRetry: 2, TimeoutMS: 30000, TimeoutAxis: "per_attempt"},
	}
	n := p3Build(t, "llm_per", spec.KindLLM, cfg, Deps{Models: m, SemTrace: emit})
	if _, err := n.Invoke(context.Background(), map[string]any{"input": "q"}); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	assertP3FreshDeadlines(t, m.deadlines)
	assertP3RetryAxis(t, p3RetryRecords(*recs), "per_attempt", 30000)
}

// ── tool: total (previously unconsumed) / per-attempt ────────────────────

func TestExecSemP3ToolSharedTotalAxis(t *testing.T) {
	emit, recs := collectTrace(t)
	reg := &p3ToolReg{names: []string{"p/t"}, failUntil: 1}
	cfg := &spec.ToolConfig{
		ToolName: "t", ProviderID: "p",
		Exception: &spec.ExceptionConfig{MaxRetry: 2, TimeoutMS: 60000, TimeoutAxis: "total"},
	}
	n := p3Build(t, "tool_total", spec.KindTool, cfg, Deps{Tools: reg, SemTrace: emit})
	out, err := n.Invoke(context.Background(), nil)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out["output"] != "ok" {
		t.Fatalf("tool output = %v, want ok", out["output"])
	}
	if reg.calls != 2 {
		t.Fatalf("tool calls = %d, want 2", reg.calls)
	}
	assertP3SharedDeadlines(t, reg.deadlines)
	assertP3RetryAxis(t, p3RetryRecords(*recs), "total", 60000)
}

func TestExecSemP3ToolPerAttemptAxis(t *testing.T) {
	emit, recs := collectTrace(t)
	reg := &p3ToolReg{names: []string{"p/t"}, failUntil: 1}
	cfg := &spec.ToolConfig{
		ToolName: "t", ProviderID: "p",
		Exception: &spec.ExceptionConfig{MaxRetry: 1, TimeoutMS: 15000, TimeoutAxis: "per_attempt"},
	}
	n := p3Build(t, "tool_per", spec.KindTool, cfg, Deps{Tools: reg, SemTrace: emit})
	if _, err := n.Invoke(context.Background(), nil); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	assertP3FreshDeadlines(t, reg.deadlines)
	assertP3RetryAxis(t, p3RetryRecords(*recs), "per_attempt", 15000)
}

func assertP3SharedDeadlines(t *testing.T, deadlines []time.Time) {
	t.Helper()
	if len(deadlines) < 2 {
		t.Fatalf("need at least two attempts, got %d", len(deadlines))
	}
	for i, dl := range deadlines {
		if dl.IsZero() {
			t.Fatalf("attempt %d has no deadline, want a shared total budget", i+1)
		}
		if !dl.Equal(deadlines[0]) {
			t.Fatalf("attempt %d deadline %v != attempt 1 deadline %v (budget refilled)", i+1, dl, deadlines[0])
		}
	}
}

func assertP3FreshDeadlines(t *testing.T, deadlines []time.Time) {
	t.Helper()
	if len(deadlines) < 2 {
		t.Fatalf("need at least two attempts, got %d", len(deadlines))
	}
	for i := 1; i < len(deadlines); i++ {
		if deadlines[i-1].IsZero() || deadlines[i].IsZero() || !deadlines[i].After(deadlines[i-1]) {
			t.Fatalf("attempt %d deadline %v is not a fresh slice after %v", i+1, deadlines[i], deadlines[i-1])
		}
	}
}

func assertP3RetryAxis(t *testing.T, recs []trace.Record, axis string, budget int) {
	t.Helper()
	if len(recs) == 0 {
		t.Fatalf("no retry records for axis %s", axis)
	}
	for i, r := range recs {
		if r.Axis != axis {
			t.Fatalf("attempt %d axis = %q, want %q", i+1, r.Axis, axis)
		}
		if r.Attempt != i+1 {
			t.Fatalf("attempt = %d, want %d (1-based)", r.Attempt, i+1)
		}
		if r.DeadlineMS > budget || r.DeadlineMS < 0 {
			t.Fatalf("attempt %d deadline_ms = %d, want <= %d", i+1, r.DeadlineMS, budget)
		}
	}
}

// ── ES-044: llm/tool ValueBoundary is honest no-op today ─────────────────

// TestExecSemP3ES044LLMToolBoundaryPassThrough locks the honest scope of the
// ES-017b migration: spec.LLMConfig / spec.ToolConfig have no `types` field,
// so FillInputs receives nil declarations and must pass absent/null through
// verbatim (no per-family zero fill, no invented inputs).
func TestExecSemP3ES044LLMToolBoundaryPassThrough(t *testing.T) {
	t.Run("tool", func(t *testing.T) {
		emit, recs := collectTrace(t)
		reg := &p3ToolReg{names: []string{"p/t"}}
		cfg := &spec.ToolConfig{
			ToolName: "t", ProviderID: "p",
			Params: []spec.ToolParam{
				{Key: "nullish", Ref: &spec.Source{BlockID: "x", Name: "x"}},
				{Key: "absent", Ref: &spec.Source{BlockID: "y", Name: "y"}},
			},
		}
		in := map[string]any{"nullish": nil}
		n := p3Build(t, "tool_044", spec.KindTool, cfg, Deps{Tools: reg, SemTrace: emit})
		if _, err := n.Invoke(context.Background(), in); err != nil {
			t.Fatalf("invoke: %v", err)
		}
		for _, key := range []string{"nullish", "absent"} {
			v, ok := reg.args[key]
			if !ok {
				t.Fatalf("args[%q] key missing (boundary must not drop it)", key)
			}
			if v != nil {
				t.Fatalf("args[%q] = %#v, want nil pass-through", key, v)
			}
		}
		if len(p3FillRecords(*recs)) != 0 {
			t.Fatalf("tool emitted fill records without declarations: %+v", p3FillRecords(*recs))
		}
		if len(in) != 1 {
			t.Fatalf("Invoke mutated the caller input map: %v", in)
		}
	})

	t.Run("llm", func(t *testing.T) {
		emit, recs := collectTrace(t)
		m := &p3Model{}
		cfg := &spec.LLMConfig{Model: "m", Result: stringLLMResult()}
		in := map[string]any{
			"input":   []any{map[string]any{"role": "user", "content": "q"}},
			"nullish": nil,
		}
		n := p3Build(t, "llm_044", spec.KindLLM, cfg, Deps{Models: m, SemTrace: emit})
		if _, err := n.Invoke(context.Background(), in); err != nil {
			t.Fatalf("invoke: %v", err)
		}
		if len(m.got) != 1 || m.got[0].Content != "q" {
			t.Fatalf("messages = %+v, want the caller's single user message", m.got)
		}
		if len(p3FillRecords(*recs)) != 0 {
			t.Fatalf("llm emitted fill records without declarations: %+v", p3FillRecords(*recs))
		}
		if len(in) != 2 {
			t.Fatalf("Invoke mutated the caller input map: %v", in)
		}
	})
}

func p3FillRecords(recs []trace.Record) []trace.Record {
	var out []trace.Record
	for _, r := range recs {
		if r.Stage == "fill" {
			out = append(out, r)
		}
	}
	return out
}
