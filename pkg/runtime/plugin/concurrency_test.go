package plugin

// concurrency_test.go — B 测试治理:Manager 并发安全。
//
// Manager 用 sync.RWMutex 保护 plugins/tools 映射;本测试并发注册、
// 查询、调用、注销,配合 `go test -race` 验证无数据竞争。调用侧分派到
// callable（进程内）——不引入网络,聚焦注册表自身的并发契约。

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestManagerConcurrentRegisterCallLookup(t *testing.T) {
	m := New()
	const plugins = 8
	const toolsPerPlugin = 4
	const workers = 16
	const perWorkerOps = 200

	// 预先注册全部插件（并行注册同一批工具名会互相冲突,先行串行注册）。
	for i := 0; i < plugins; i++ {
		p := concurrencyPlugin(fmt.Sprintf("p%d", i), toolsPerPlugin)
		if err := m.Register(p); err != nil {
			t.Fatalf("register p%d: %v", i, err)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorkerOps)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for k := 0; k < perWorkerOps; k++ {
				pid := fmt.Sprintf("p%d", (w+k)%plugins)
				tool := fmt.Sprintf("t%d", (w+k)%toolsPerPlugin)
				logical := pid + "/" + tool
				switch k % 3 {
				case 0: // Call
					if _, err := m.Call(context.Background(), logical, map[string]any{"x": 1}); err != nil {
						errs <- fmt.Errorf("call %s: %v", logical, err)
					}
				case 1: // Lookup
					if entry, _ := m.Lookup(logical); entry == nil {
						errs <- fmt.Errorf("lookup %s: missing", logical)
					}
				case 2: // Names
					_ = m.Names()
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// 并发 Unregister + 查询不得 panic（条目可能已消失,Lookup 允许 miss）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < plugins; i++ {
			_ = m.Unregister(fmt.Sprintf("p%d", i))
		}
	}()
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < plugins; i++ {
				_ = m.Names()
				_, _ = m.Lookup(fmt.Sprintf("p%d/t0", i))
			}
		}()
	}
	wg.Wait()
}

func concurrencyPlugin(id string, nTools int) *Plugin {
	tools := make([]Tool, 0, nTools)
	for i := 0; i < nTools; i++ {
		name := fmt.Sprintf("t%d", i)
		tools = append(tools, Tool{
			Name: name,
			Call: func(ctx context.Context, args map[string]any) (any, error) {
				return args["x"], nil
			},
		})
	}
	return &Plugin{ID: id, Type: PluginCallable, Tools: tools}
}
