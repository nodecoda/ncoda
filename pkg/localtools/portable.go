// portable.go — 纯函数/本地语义库的实现（real 保真度）。
//
// 范围：不触网、不读全局时钟、不写共享状态的库。它们的语义可以从
// capability header 的签名 + 语料调用面直接确定，因此按 real 绑定：
//
//	array_count.array_count
//	array_length.Array_Count
//	common_code_block.{get_one_by_num,is_list_trim,list_sli_trim,remove_list_empty}
//	throw_error.throw_error
//	datetime.datetime          （时钟是 Options.Clock 注入的环境输入）
//	timer_wait.time_wait       （等待上限是 Options.MaxSleep；partial 见 bindings.yaml）
//
// 结果只发 header 声明过的键：多发的键会变成调用方可见的隐式契约。
package localtools

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
)

func registerPortable(m *plugin.Manager, opts Options) error {
	plugins := []struct {
		lib string
		p   *plugin.Plugin
	}{
		{"array_count", &plugin.Plugin{
			ID: "local_array_count", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{{
				Name: "array_count", Desc: "数组元素个数（本地纯函数）",
				Capabilities: []string{"array_count.array_count"},
				Call:         arrayCount,
			}},
		}},
		{"array_length", &plugin.Plugin{
			ID: "local_array_length", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{{
				Name: "Array_Count", Desc: "数组元素个数（float 端口，本地纯函数）",
				Capabilities: []string{"array_length.Array_Count"},
				Call:         arrayLength,
			}},
		}},
		{"common_code_block", &plugin.Plugin{
			ID: "local_common_code_block", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{
				{Name: "get_one_by_num", Desc: "按序号取一项（end=true 取末项）", Capabilities: []string{"common_code_block.get_one_by_num"}, Call: getOneByNum},
				{Name: "is_list_trim", Desc: "列表是否已全部去空白", Capabilities: []string{"common_code_block.is_list_trim"}, Call: isListTrim},
				{Name: "list_sli_trim", Desc: "逐个元素去首尾空白", Capabilities: []string{"common_code_block.list_sli_trim"}, Call: listSliTrim},
				{Name: "remove_list_empty", Desc: "移除空/全空白元素", Capabilities: []string{"common_code_block.remove_list_empty"}, Call: removeListEmpty},
			},
		}},
		{"throw_error", &plugin.Plugin{
			ID: "local_throw_error", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{{
				Name: "throw_error", Desc: "把声明消息抛成节点错误（failure_branch 可接）",
				Capabilities: []string{"throw_error.throw_error"},
				Call:         throwError,
			}},
		}},
		{"datetime", &plugin.Plugin{
			ID: "local_datetime", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{{
				Name: "datetime", Desc: "日期时间分解（时钟从 Options.Clock 注入）",
				Capabilities: []string{"datetime.datetime"},
				Call:         datetimeCall(opts.Clock),
			}},
		}},
		{"timer_wait", &plugin.Plugin{
			ID: "local_timer_wait", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{{
				Name: "time_wait", Desc: "定时等待 {actual_delay,success,message}",
				Capabilities: []string{"timer_wait.time_wait"},
				Call:         timerWait(opts.MaxSleep),
			}},
		}},
		{"timer", &plugin.Plugin{
			ID: "local_timer", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{{
				Name: "time_wait", Desc: "定时等待 {actual_delay,success,message}",
				Capabilities: []string{"timer.time_wait"},
				Call:         timerWait(opts.MaxSleep),
			}},
		}},
		{"time_wait", &plugin.Plugin{
			ID: "local_time_wait", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{{
				Name: "time_wait", Desc: "定时等待 {value}（秒）",
				Capabilities: []string{"time_wait.time_wait"},
				Call:         timerWaitValue(opts.MaxSleep),
			}},
		}},
		{"wait_cat_timer", &plugin.Plugin{
			ID: "local_wait_cat_timer", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{{
				Name: "time_wait", Desc: "定时等待 {value}（秒）",
				Capabilities: []string{"wait_cat_timer.time_wait"},
				Call:         timerWaitValue(opts.MaxSleep),
			}},
		}},
		{"toolbox", &plugin.Plugin{
			ID: "local_toolbox", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{{
				Name: "time_wait", Desc: "定时等待 {result:实际毫秒}",
				Capabilities: []string{"toolbox.time_wait"},
				Call:         toolboxWait(opts.MaxSleep),
			}},
		}},
		{"timer_pro", &plugin.Plugin{
			ID: "local_timer_pro", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{{
				Name: "time_waiter", Desc: "定时等待（delay_ms，void）",
				Capabilities: []string{"timer_pro.time_waiter"},
				Call:         timerProWait(opts.MaxSleep),
			}},
		}},
		{"time_toolkit", &plugin.Plugin{
			ID: "local_time_toolkit", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{{
				Name: "timewait", Desc: "定时等待 {message}（waitTime 按秒）",
				Capabilities: []string{"time_toolkit.timewait"},
				Call:         timeToolkitWait(opts.MaxSleep),
			}},
		}},
	}
	for _, e := range plugins {
		if !opts.allows(e.lib) {
			continue
		}
		if err := m.Register(e.p); err != nil {
			return err
		}
	}
	return nil
}

// ── 参数读取 ────────────────────────────────────────────────────────────────

// listArg accepts the shapes the IR can deliver: []any (JSON), []string,
// or a single scalar (treated as a one-element list). nil -> empty list.
func listArg(args map[string]any, key string) []any {
	switch v := args[key].(type) {
	case nil:
		return nil
	case []any:
		return v
	case []string:
		out := make([]any, len(v))
		for i := range v {
			out[i] = v[i]
		}
		return out
	default:
		return []any{v}
	}
}

func numberArg(args map[string]any, key string, def float64) float64 {
	switch v := args[key].(type) {
	case nil:
		return def
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return def
}

func boolArg(args map[string]any, key string, def bool) bool {
	switch v := args[key].(type) {
	case bool:
		return v
	case string:
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	case float64:
		return v != 0
	case int:
		return v != 0
	}
	return def
}

// ── 数组长度 ────────────────────────────────────────────────────────────────

func arrayCount(_ context.Context, args map[string]any) (any, error) {
	return map[string]any{"count": len(listArg(args, "array"))}, nil
}

func arrayLength(_ context.Context, args map[string]any) (any, error) {
	return map[string]any{"count": float64(len(listArg(args, "array")))}, nil
}

// ── common_code_block ──────────────────────────────────────────────────────

func getOneByNum(_ context.Context, args map[string]any) (any, error) {
	list := listArg(args, "list")
	if len(list) == 0 {
		return map[string]any{"item": nil, "list": []any{}, "message": "empty list"}, nil
	}
	idx := intArg(args, "num", 0)
	if boolArg(args, "end", false) {
		idx = len(list) - 1
	}
	if idx < 0 || idx >= len(list) {
		return map[string]any{"item": nil, "list": list, "message": fmt.Sprintf("index %d out of range (len=%d)", idx, len(list))}, nil
	}
	return map[string]any{"item": list[idx], "list": list, "message": ""}, nil
}

func isListTrim(_ context.Context, args map[string]any) (any, error) {
	for _, item := range listArg(args, "list") {
		if s, ok := item.(string); ok && s != strings.TrimSpace(s) {
			return map[string]any{"done": false}, nil
		}
	}
	return map[string]any{"done": true}, nil
}

func listSliTrim(_ context.Context, args map[string]any) (any, error) {
	in := listArg(args, "list")
	out := make([]any, len(in))
	for i, item := range in {
		if s, ok := item.(string); ok {
			out[i] = strings.TrimSpace(s)
		} else {
			out[i] = item
		}
	}
	return map[string]any{"data": out, "message": ""}, nil
}

func removeListEmpty(_ context.Context, args map[string]any) (any, error) {
	in := listArg(args, "list")
	out := make([]any, 0, len(in))
	for _, item := range in {
		if strings.TrimSpace(fmt.Sprint(item)) == "" {
			continue
		}
		out = append(out, item)
	}
	return map[string]any{"data": out, "message": ""}, nil
}

// ── throw_error ────────────────────────────────────────────────────────────

// throwError 是**有意**返回 Go error 的实现：tool 节点的错误策略
// （throw / return / failure_branch）只在 error 通道上生效。把错误包装成
// 一个“成功的结果”会让 failure_branch 永远不触发。
func throwError(_ context.Context, args map[string]any) (any, error) {
	msg := stringArg(args, "error_message")
	if strings.TrimSpace(msg) == "" {
		msg = "throw_error"
	}
	return nil, errors.New(msg)
}

// ── datetime ───────────────────────────────────────────────────────────────

// DefaultClock 是 datetime 的确定性默认时钟。选 epoch 而不是 time.Now：
// 同输入必须同输出；真实时钟由宿主 / --clock 显式注入。
func DefaultClock() time.Time { return time.Unix(0, 0).UTC() }

// ParseClock 解析宿主的 --clock：""/epoch = 确定性默认，now = 真实时钟，
// 其余按 RFC3339。返回 nil = 使用 DefaultClock。
func ParseClock(spec string) (func() time.Time, error) {
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "", "epoch", "pinned":
		return nil, nil
	case "now":
		return time.Now, nil
	}
	t, err := time.Parse(time.RFC3339, spec)
	if err != nil {
		return nil, fmt.Errorf("--clock: want \"now\", epoch, or RFC3339: %w", err)
	}
	return func() time.Time { return t }, nil
}

func datetimeCall(clock func() time.Time) plugin.CallFunc {
	if clock == nil {
		clock = DefaultClock
	}
	return func(_ context.Context, args map[string]any) (any, error) {
		raw := strings.TrimSpace(stringArg(args, "datetime"))
		t, err := parseDateTime(raw, clock)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"date":             float64(t.Day()),
			"dateOnly":         t.Format("2006-01-02"),
			"dateTime":         t.Format("2006-01-02 15:04:05"),
			"filenameFriendly": t.Format("20060102_150405"),
			"hour":             float64(t.Hour()),
			"millisecond":      float64(t.Nanosecond() / int(time.Millisecond)),
			"minute":           float64(t.Minute()),
			"month":            float64(int(t.Month())),
			"prompt":           t.Format("2006年01月02日 15:04:05"),
			"second":           float64(t.Second()),
			"timeOnly":         t.Format("15:04:05"),
			"timestamp":        t.Unix(),
			"year":             float64(t.Year()),
		}, nil
	}
}

// parseDateTime 接受 RFC3339、常见人类格式、unix 秒/毫秒；空串 = 时钟。
func parseDateTime(raw string, clock func() time.Time) (time.Time, error) {
	if raw == "" {
		return clock().UTC(), nil
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02",
		"2006/01/02 15:04:05",
	} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), nil
		}
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		// 13 位以上按毫秒读：语料里两种都出现过。
		if len(raw) >= 13 {
			return time.UnixMilli(n).UTC(), nil
		}
		return time.Unix(n, 0).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("datetime: cannot parse %q", raw)
}

// ── timer_wait ─────────────────────────────────────────────────────────────

func timerWait(maxSleep time.Duration) plugin.CallFunc {
	return func(ctx context.Context, args map[string]any) (any, error) {
		want := numberArg(args, "seconds", 0)
		delay, capped, canceled := sleepCapped(ctx, want, maxSleep)
		msg := fmt.Sprintf("waited %.3fs", delay.Seconds())
		if capped {
			msg = fmt.Sprintf("waited %.3fs (requested %.3fs, capped at max_sleep=%.3fs)",
				delay.Seconds(), want, maxSleep.Seconds())
		}
		if canceled {
			msg = "canceled: " + ctx.Err().Error()
		}
		return map[string]any{
			"actual_delay": delay.Seconds(),
			"success":      !canceled,
			"message":      msg,
		}, nil
	}
}

// timerWaitValue 对 `timer_wait` / `time_wait` / `wait_cat_timer` 的单端口
// `{any value}`：value = 实际等待秒数。原实现的 value 语义未证实，见 loss。
func timerWaitValue(maxSleep time.Duration) plugin.CallFunc {
	return func(ctx context.Context, args map[string]any) (any, error) {
		want := numberArg(args, "seconds", 0)
		delay, _, _ := sleepCapped(ctx, want, maxSleep)
		return map[string]any{"value": delay.Seconds()}, nil
	}
}

// toolboxWait 对 `toolbox.time_wait -> {result:int}`：result = 实际等待毫秒数
// （原语义未证实，见 bindings loss）。
func toolboxWait(maxSleep time.Duration) plugin.CallFunc {
	return func(ctx context.Context, args map[string]any) (any, error) {
		want := numberArg(args, "seconds", 0)
		delay, _, _ := sleepCapped(ctx, want, maxSleep)
		return map[string]any{"result": delay.Milliseconds()}, nil
	}
}

// timerProWait 对 `timer_pro.time_waiter(delay_ms) -> void`。
func timerProWait(maxSleep time.Duration) plugin.CallFunc {
	return func(ctx context.Context, args map[string]any) (any, error) {
		ms := numberArg(args, "delay_ms", 0)
		_, _, _ = sleepCapped(ctx, ms/1000.0, maxSleep)
		return nil, nil
	}
}

// timeToolkitWait 对 `time_toolkit.timewait(waitTime) -> {message}`。
// waitTime 的单位未由语料证实；按秒处理并在 loss 里声明。
func timeToolkitWait(maxSleep time.Duration) plugin.CallFunc {
	return func(ctx context.Context, args map[string]any) (any, error) {
		want := numberArg(args, "waitTime", 0)
		delay, capped, canceled := sleepCapped(ctx, want, maxSleep)
		msg := fmt.Sprintf("waited %.3fs", delay.Seconds())
		if capped {
			msg = fmt.Sprintf("waited %.3fs (requested %.3fs, capped at max_sleep=%.3fs)",
				delay.Seconds(), want, maxSleep.Seconds())
		}
		if canceled {
			msg = "canceled: " + ctx.Err().Error()
		}
		return map[string]any{"message": msg}, nil
	}
}

// sleepCapped 是所有 timer 实现的唯一等待规则：负数归零、超过 MaxSleep 截断、
// 可被 ctx 取消。返回实际等待、是否截断、是否取消。
func sleepCapped(ctx context.Context, want float64, maxSleep time.Duration) (time.Duration, bool, bool) {
	if maxSleep <= 0 {
		maxSleep = time.Second
	}
	if want < 0 {
		want = 0
	}
	delay := time.Duration(want * float64(time.Second))
	capped := false
	if delay > maxSleep {
		delay = maxSleep
		capped = true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return delay, capped, true
	case <-timer.C:
		return delay, capped, false
	}
}
