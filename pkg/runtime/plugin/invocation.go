package plugin

// invocation.go — 运行管理（Manager.Call）。
//
// 职责：
//   - Call(ctx, name, args)：查注册表 → 超时包装 → 按 PluginType 分派
//     Invocation → 广播观察者事件（start/success/error）。
//   - Invocation 抽象：Do(ctx, *CallArgs) (*Result, error)，返回审计用
//     Request 串与工具数据 Data。
//
// 实现 nodes.ToolRegistry（Call 签名对齐），tool 节点与 agent 节点直接可用。

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Invocation 执行一个已注册工具。Do 返回审计串 Request 与结果 Data。
type Invocation interface {
	Do(ctx context.Context, args *CallArgs) (*Result, error)
}

// CallArgs 是 Invocation 的输入：条目上下文 + 已装配参数。
type CallArgs struct {
	Entry *ToolEntry
	Args  map[string]any
}

// Result 是一次工具调用的产物。
type Result struct {
	// Request 是序列化后的请求（HTTP 方法/路径/头/体；callable 为参数 JSON），
	// 供审计与调试（对齐 coze-studio 的 request string）。
	Request string
	// Data 是工具返回值（http 型为解析后的响应体）。
	Data any
}

// Phase 是工具调用生命周期阶段。
type Phase string

const (
	PhaseStart   Phase = "start"
	PhaseSuccess Phase = "success"
	PhaseError   Phase = "error"
)

// Event 是观察者收到的调用事件。
type Event struct {
	PluginID string
	Tool     string // 逻辑名（id/name）
	Phase    Phase
	Request  string
	Data     any
	Err      error
	Duration time.Duration
}

// Observer 接收调用事件（日志/审计/指标）。
type Observer func(Event)

// ErrToolNotFound 表示逻辑名未注册。
type ErrToolNotFound struct{ Name string }

func (e *ErrToolNotFound) Error() string {
	return fmt.Sprintf("plugin: tool %q not registered", e.Name)
}

// ErrAmbiguousTool 表示退化名命中多个插件工具。
type ErrAmbiguousTool struct{ Name string }

func (e *ErrAmbiguousTool) Error() string {
	return fmt.Sprintf("plugin: tool %q is ambiguous across plugins; use fully-qualified name", e.Name)
}

// callableInvocation 执行 CallFunc 闭包。
type callableInvocation struct{}

func (callableInvocation) Do(ctx context.Context, args *CallArgs) (*Result, error) {
	t := args.Entry.Tool
	data, err := t.Call(ctx, args.Args)
	if err != nil {
		return nil, err
	}
	return &Result{Data: data}, nil
}

// AddObserver 注册一个全局观察者（调用事件广播，串行通知）。
func (m *Manager) AddObserver(fn Observer) {
	if fn == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.observers = append(m.observers, fn)
}

// Call 是 nodes.ToolRegistry 入口：查表 → 超时 → 分派 → 广播事件。
func (m *Manager) Call(ctx context.Context, name string, args map[string]any) (any, error) {
	entry, err := m.lookupLenient(name)
	if err != nil {
		return nil, err
	}

	callCtx, cancel := m.applyTimeout(ctx, entry.Plugin.TimeoutMS)
	if cancel != nil {
		defer cancel()
	}

	inv := m.invocationFor(entry.Plugin.Type)
	start := time.Now()
	m.emit(Event{PluginID: entry.Plugin.ID, Tool: logicalName(entry.Plugin.ID, entry.Tool.Name), Phase: PhaseStart})

	res, err := inv.Do(callCtx, &CallArgs{Entry: entry, Args: args})
	dur := time.Since(start)
	if err != nil {
		m.emit(Event{PluginID: entry.Plugin.ID, Tool: logicalName(entry.Plugin.ID, entry.Tool.Name), Phase: PhaseError, Err: err, Duration: dur})
		return nil, err
	}
	m.emit(Event{PluginID: entry.Plugin.ID, Tool: logicalName(entry.Plugin.ID, entry.Tool.Name), Phase: PhaseSuccess, Request: res.Request, Data: res.Data, Duration: dur})
	return res.Data, nil
}

// applyTimeout 在插件声明超时内包装 ctx；ctx 已有更早 deadline 时保留之。
func (m *Manager) applyTimeout(ctx context.Context, timeoutMS int64) (context.Context, context.CancelFunc) {
	if timeoutMS <= 0 {
		return ctx, nil
	}
	to := time.Duration(timeoutMS) * time.Millisecond
	if d, ok := ctx.Deadline(); ok && time.Until(d) <= to {
		return ctx, nil
	}
	return context.WithTimeout(ctx, to)
}

// invocationFor 按插件类型分派实现。
func (m *Manager) invocationFor(t PluginType) Invocation {
	switch t {
	case PluginCallable:
		return callableInvocation{}
	default: // PluginHTTP
		return httpInvocation{}
	}
}

// lookupLenient 宽容匹配：完整逻辑名 → 后缀匹配 tool 名。
// 后缀匹配命中多个候选 → 歧义错误（提示使用完整逻辑名）。
func (m *Manager) lookupLenient(name string) (*ToolEntry, error) {
	if e, ok := m.Lookup(name); ok {
		return e, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var hit *ToolEntry
	for _, e := range m.tools {
		if MatchName(e, name) {
			if hit != nil {
				return nil, &ErrAmbiguousTool{Name: name}
			}
			hit = e
		}
	}
	if hit == nil {
		return nil, &ErrToolNotFound{Name: name}
	}
	return hit, nil
}

var obsMu sync.Mutex

func (m *Manager) emit(ev Event) {
	m.mu.RLock()
	obs := append([]Observer(nil), m.observers...)
	m.mu.RUnlock()
	obsMu.Lock()
	defer obsMu.Unlock()
	for _, fn := range obs {
		fn(ev)
	}
}
