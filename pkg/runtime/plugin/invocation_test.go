package plugin

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func callablePlugin(id string, fn CallFunc) *Plugin {
	return &Plugin{ID: id, Type: PluginCallable, Tools: []Tool{{Name: "echo", Call: fn}}}
}

func TestCallCallable(t *testing.T) {
	m := New()
	var calls atomic.Int32
	if err := m.Register(callablePlugin("p.one", func(ctx context.Context, args map[string]any) (any, error) {
		calls.Add(1)
		return args["x"], nil
	})); err != nil {
		t.Fatal(err)
	}
	got, err := m.Call(context.Background(), "p.one/echo", map[string]any{"x": "hello"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got != "hello" || calls.Load() != 1 {
		t.Fatalf("got=%v calls=%d", got, calls.Load())
	}
}

func TestCallLenientNameMatch(t *testing.T) {
	m := New()
	if err := m.Register(callablePlugin("p.one", func(ctx context.Context, args map[string]any) (any, error) {
		return "ok", nil
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Call(context.Background(), "echo", nil); err != nil {
		t.Fatalf("Call(echo): %v", err)
	}
}

func TestCallUnknownTool(t *testing.T) {
	m := New()
	_, err := m.Call(context.Background(), "nope/x", nil)
	var nf *ErrToolNotFound
	if !errors.As(err, &nf) {
		t.Fatalf("want ErrToolNotFound, got %v", err)
	}
}

func TestCallableErrorPropagates(t *testing.T) {
	m := New()
	if err := m.Register(callablePlugin("p.err", func(ctx context.Context, args map[string]any) (any, error) {
		return nil, errors.New("boom")
	})); err != nil {
		t.Fatal(err)
	}
	_, err := m.Call(context.Background(), "p.err/echo", nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want boom, got %v", err)
	}
}

func TestCallTimeout(t *testing.T) {
	m := New()
	p := callablePlugin("p.slow", func(ctx context.Context, args map[string]any) (any, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			return "late", nil
		}
	})
	p.TimeoutMS = 50
	if err := m.Register(p); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := m.Call(context.Background(), "p.slow/echo", nil)
	if err == nil {
		t.Fatal("want timeout error")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("timeout not enforced: %v", time.Since(start))
	}
}

func TestObserverEvents(t *testing.T) {
	m := New()
	if err := m.Register(callablePlugin("p.evt", func(ctx context.Context, args map[string]any) (any, error) {
		return "data", nil
	})); err != nil {
		t.Fatal(err)
	}
	var phases []Phase
	m.AddObserver(func(ev Event) { phases = append(phases, ev.Phase) })
	if _, err := m.Call(context.Background(), "p.evt/echo", nil); err != nil {
		t.Fatal(err)
	}
	if len(phases) != 2 || phases[0] != PhaseStart || phases[1] != PhaseSuccess {
		t.Fatalf("phases = %v", phases)
	}
}

func TestObserverErrorEvent(t *testing.T) {
	m := New()
	if err := m.Register(callablePlugin("p.everr", func(ctx context.Context, args map[string]any) (any, error) {
		return nil, errors.New("x")
	})); err != nil {
		t.Fatal(err)
	}
	var phases []Phase
	m.AddObserver(func(ev Event) {
		if ev.Phase == PhaseError {
			phases = append(phases, ev.Phase)
			if ev.Err == nil {
				t.Fatal("error event missing err")
			}
		}
	})
	_, _ = m.Call(context.Background(), "p.everr/echo", nil)
	if len(phases) != 1 || phases[0] != PhaseError {
		t.Fatalf("phases = %v", phases)
	}
}
