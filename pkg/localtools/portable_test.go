package localtools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
)

func portableManager(t *testing.T, opts Options) *plugin.Manager {
	t.Helper()
	if opts.ArtifactDir == "" {
		opts.ArtifactDir = t.TempDir()
	}
	m := plugin.New()
	if _, err := Register(m, opts); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return m
}

func callAlias(t *testing.T, m *plugin.Manager, name string, args map[string]any) any {
	t.Helper()
	got, err := m.Call(context.Background(), name, args)
	if err != nil {
		t.Fatalf("Call(%q): %v", name, err)
	}
	return got
}

func TestArrayCountAndLength(t *testing.T) {
	m := portableManager(t, Options{})
	for _, tc := range []struct {
		alias string
		want  any
	}{
		{"array_count/array_count", 3},
		{"array_length/Array_Count", float64(3)},
	} {
		got := callAlias(t, m, tc.alias, map[string]any{"array": []any{"a", "b", "c"}})
		if got.(map[string]any)["count"] != tc.want {
			t.Fatalf("%s count = %v, want %v", tc.alias, got.(map[string]any)["count"], tc.want)
		}
	}
	// 空/缺失输入不是错误：长度 0。
	got := callAlias(t, m, "array_count/array_count", nil)
	if got.(map[string]any)["count"] != 0 {
		t.Fatalf("nil array count = %v", got)
	}
}

func TestCommonCodeBlockListOps(t *testing.T) {
	m := portableManager(t, Options{})
	list := []any{"  a ", "", "b\t", "   "}

	trimmed := callAlias(t, m, "common_code_block/list_sli_trim", map[string]any{"list": list})
	if got := trimmed.(map[string]any)["data"].([]any); len(got) != 4 || got[0] != "a" || got[2] != "b" {
		t.Fatalf("list_sli_trim = %v", got)
	}
	notTrim := callAlias(t, m, "common_code_block/is_list_trim", map[string]any{"list": list})
	if notTrim.(map[string]any)["done"] != false {
		t.Fatalf("is_list_trim on untrimmed list = %v", notTrim)
	}
	yesTrim := callAlias(t, m, "common_code_block/is_list_trim", map[string]any{"list": []any{"a", "b"}})
	if yesTrim.(map[string]any)["done"] != true {
		t.Fatalf("is_list_trim on trimmed list = %v", yesTrim)
	}
	removed := callAlias(t, m, "common_code_block/remove_list_empty", map[string]any{"list": list})
	if got := removed.(map[string]any)["data"].([]any); len(got) != 2 {
		t.Fatalf("remove_list_empty = %v", got)
	}

	last := callAlias(t, m, "common_code_block/get_one_by_num", map[string]any{"list": []any{"x", "y", "z"}, "end": true})
	if last.(map[string]any)["item"] != "z" {
		t.Fatalf("get_one_by_num(end) = %v", last)
	}
	second := callAlias(t, m, "common_code_block/get_one_by_num", map[string]any{"list": []any{"x", "y", "z"}, "num": 1})
	if second.(map[string]any)["item"] != "y" {
		t.Fatalf("get_one_by_num(1) = %v", second)
	}
	// 越界是声明的结果（item=nil + message），不是 panic。
	out := callAlias(t, m, "common_code_block/get_one_by_num", map[string]any{"list": []any{"x"}, "num": 9})
	if out.(map[string]any)["item"] != nil || !strings.Contains(out.(map[string]any)["message"].(string), "out of range") {
		t.Fatalf("out-of-range result = %v", out)
	}
}

func TestThrowErrorReturnsGoError(t *testing.T) {
	m := portableManager(t, Options{})
	if _, err := m.Call(context.Background(), "throw_error/throw_error", map[string]any{"error_message": "boom"}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("throw_error err = %v, want boom", err)
	}
	// 空消息也必须抛（不能静默成功）。
	if _, err := m.Call(context.Background(), "throw_error/throw_error", nil); err == nil {
		t.Fatal("throw_error with empty message must still error")
	}
}

func TestDatetimeUsesInjectedClock(t *testing.T) {
	fixed := time.Date(2026, 10, 25, 13, 45, 6, 123_000_000, time.UTC)
	m := portableManager(t, Options{Clock: func() time.Time { return fixed }})

	got := callAlias(t, m, "datetime/datetime", map[string]any{})
	want := map[string]any{
		"dateTime":         "2026-10-25 13:45:06",
		"dateOnly":         "2026-10-25",
		"timeOnly":         "13:45:06",
		"timestamp":        fixed.Unix(),
		"year":             float64(2026),
		"month":            float64(10),
		"date":             float64(25),
		"hour":             float64(13),
		"minute":           float64(45),
		"second":           float64(6),
		"millisecond":      float64(123),
		"filenameFriendly": "20261025_134506",
	}
	for k, v := range want {
		if got.(map[string]any)[k] != v {
			t.Fatalf("datetime[%s] = %v, want %v", k, got.(map[string]any)[k], v)
		}
	}

	// 显式输入优先于时钟；非法输入是错误（不静默回退到 now）。
	parsed := callAlias(t, m, "datetime/datetime", map[string]any{"datetime": "2020-01-02T03:04:05Z"})
	if parsed.(map[string]any)["dateTime"] != "2020-01-02 03:04:05" {
		t.Fatalf("parsed datetime = %v", parsed)
	}
	if _, err := m.Call(context.Background(), "datetime/datetime", map[string]any{"datetime": "not-a-date"}); err == nil {
		t.Fatal("invalid datetime must error")
	}
}

func TestDatetimeDefaultClockIsPinned(t *testing.T) {
	m := portableManager(t, Options{})
	got := callAlias(t, m, "datetime/datetime", map[string]any{})
	if got.(map[string]any)["timestamp"] != int64(0) {
		t.Fatalf("default clock timestamp = %v, want 0 (pinned epoch)", got)
	}
}

func TestTimerWaitCapsAndReports(t *testing.T) {
	m := portableManager(t, Options{MaxSleep: 10 * time.Millisecond})
	start := time.Now()
	got := callAlias(t, m, "timer_wait/time_wait", map[string]any{"seconds": 30.0})
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("capped wait took %v", elapsed)
	}
	body := got.(map[string]any)
	if body["success"] != true {
		t.Fatalf("time_wait = %v", body)
	}
	if body["actual_delay"].(float64) > 0.05 {
		t.Fatalf("actual_delay = %v, want capped ~0.01", body["actual_delay"])
	}
	if !strings.Contains(body["message"].(string), "capped") {
		t.Fatalf("message must disclose the cap: %v", body["message"])
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m2 := portableManager(t, Options{MaxSleep: time.Second})
	got, err := m2.Call(ctx, "timer_wait/time_wait", map[string]any{"seconds": 1.0})
	if err != nil {
		t.Fatalf("canceled wait must be a declared failure, got error %v", err)
	}
	if got.(map[string]any)["success"] != false {
		t.Fatalf("canceled wait = %v", got)
	}
}

// timer 家族：每个库的结果形状按各自 header，不共享一个“万能 record”。
func TestTimerFamilyShapes(t *testing.T) {
	m := portableManager(t, Options{MaxSleep: 5 * time.Millisecond})

	verbose := callAlias(t, m, "timer_wait/time_wait", map[string]any{"seconds": 1.0}).(map[string]any)
	if verbose["success"] != true || verbose["actual_delay"].(float64) > 0.05 {
		t.Fatalf("timer_wait = %v", verbose)
	}
	same := callAlias(t, m, "time_wait/time_wait", map[string]any{"seconds": 1.0}).(map[string]any)
	if _, ok := same["value"]; !ok || same["value"].(float64) > 0.05 {
		t.Fatalf("time_wait = %v", same)
	}
	tmr := callAlias(t, m, "timer/time_wait", map[string]any{"seconds": 1.0}).(map[string]any)
	if tmr["success"] != true {
		t.Fatalf("timer = %v", tmr)
	}
	cat := callAlias(t, m, "wait_cat_timer/time_wait", map[string]any{"seconds": 1.0}).(map[string]any)
	if _, ok := cat["value"]; !ok {
		t.Fatalf("wait_cat_timer = %v", cat)
	}
	box := callAlias(t, m, "toolbox/time_wait", map[string]any{"seconds": 1.0}).(map[string]any)
	if _, ok := box["result"]; !ok || box["result"].(int64) > 50 {
		t.Fatalf("toolbox = %v", box)
	}
	if got := callAlias(t, m, "timer_pro/time_waiter", map[string]any{"delay_ms": 1000}); got != nil {
		t.Fatalf("timer_pro must be void, got %v", got)
	}
	tk := callAlias(t, m, "time_toolkit/timewait", map[string]any{"waitTime": 1}).(map[string]any)
	if !strings.Contains(tk["message"].(string), "capped") {
		t.Fatalf("time_toolkit message = %v", tk)
	}
}

// 负控：绑定表指向未注册的实现 ⇒ Register 必须失败，而不是静默放过。
func TestRegisterRejectsUnregisteredCapability(t *testing.T) {
	m := plugin.New()
	if err := m.Register(&plugin.Plugin{ID: "only", Version: "1", Type: plugin.PluginCallable,
		Tools: []plugin.Tool{{Name: "t", Capabilities: []string{"only.t"}, Call: func(context.Context, map[string]any) (any, error) { return nil, nil }}}}); err != nil {
		t.Fatal(err)
	}
	if err := m.Alias("ghost/op", "ghost.op"); err == nil {
		t.Fatal("alias to unregistered capability accepted")
	}
}
