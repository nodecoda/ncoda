package compile

// stub_test.go — 测试注册表。M2.1 起纯函数节点使用 nodes.StdBuilders()
// 的正式实现;本文件仅保留:(1) text 的调用计数装饰(分支门回归);
// (2) http stub(I/O 节点 M2.2 正式化)。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// ── stub builder ─────────────────────────────────────────────────────────

type stubBuilder struct {
	kind  string
	build func(ctx context.Context, n *spec.NodeSpec, deps nodes.Deps) (nodes.NodeImpl, error)
}

func (b *stubBuilder) Kind() string { return b.kind }
func (b *stubBuilder) Build(ctx context.Context, n *spec.NodeSpec, deps nodes.Deps) (nodes.NodeImpl, error) {
	return b.build(ctx, n, deps)
}

// testRegistry registers the formal pure-function node set (M2.1) plus:
//   - text wrapped with a call counter (branch-gate regression observes
//     const-template invocations — textRunCount, see compile_test.go);
//   - http stub (I/O nodes land in M2.2; the stub keeps fail:// routing
//     and URL templating for the U1.1c fail-branch test).
func testRegistry() *nodes.Registry {
	r := nodes.NewRegistry()
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	// formal nodes (M2.2: 含 llm/tool/qa —— deps 缺失在 Build 期才报错,
	// 测试 spec 不含它们即可)。http 保留 stub（fail:// 分支门语义）;
	// text 用计数装饰。
	for _, b := range nodes.StdBuilders() {
		if b.Kind() == spec.KindText || b.Kind() == spec.KindHTTP {
			continue
		}
		must(r.Register(b))
	}
	// text: formal impl + invocation counter (template-keyed)
	must(r.Register(&stubBuilder{kind: spec.KindText, build: func(ctx context.Context, n *spec.NodeSpec, deps nodes.Deps) (nodes.NodeImpl, error) {
		impl, err := (nodes.Text{}).Build(ctx, n, deps)
		if err != nil {
			return nil, err
		}
		cfg, derr := spec.DecodeConfig(n.Kind, n.Config)
		if derr != nil {
			return nil, derr
		}
		return &countingText{inner: impl, tpl: cfg.(*spec.TextConfig).Template}, nil
	}}))
	// http: M1 stub (M2.2 formalizes it)
	must(r.Register(&stubBuilder{kind: spec.KindHTTP, build: func(ctx context.Context, n *spec.NodeSpec, _ nodes.Deps) (nodes.NodeImpl, error) {
		cfg, err := spec.DecodeConfig(n.Kind, n.Config)
		if err != nil {
			return nil, err
		}
		return &stubHTTP{cfg: cfg.(*spec.HTTPConfig)}, nil
	}}))
	return r
}

// ── test-only node implementations (stubs kept only where the formal
// set is not yet available) ─────────────────────────────────────────────

// countingText wraps the formal text node and records const-template
// invocations (branch-gate regression: unselected targets stay silent).
type countingText struct {
	inner nodes.NodeImpl
	tpl   string
}

func (c *countingText) Invoke(ctx context.Context, in map[string]any) (map[string]any, error) {
	noteTextRun(c.tpl)
	return c.inner.Invoke(ctx, in)
}

// textRuns records const-template invocations (branch-gate regression:
// unselected targets must stay silent).
var textRuns struct {
	sync.Mutex
	m map[string]int
}

func noteTextRun(tpl string) {
	textRuns.Lock()
	if textRuns.m == nil {
		textRuns.m = map[string]int{}
	}
	textRuns.m[tpl]++
	textRuns.Unlock()
}

func textRunCount(tpl string) int {
	textRuns.Lock()
	defer textRuns.Unlock()
	return textRuns.m[tpl]
}

type stubHTTP struct{ cfg *spec.HTTPConfig }

func (h *stubHTTP) Invoke(_ context.Context, in map[string]any) (map[string]any, error) {
	url := h.cfg.URL
	for k, v := range in {
		url = strings.ReplaceAll(url, "{{"+k+"}}", fmt.Sprint(v))
	}
	if strings.Contains(h.cfg.URL, "fail://") {
		return map[string]any{nodes.MarkerBranch: "fail-branch", "error": "boom", "status": 500}, nil
	}
	return map[string]any{"output": "HTTP:" + url, "status": 200}, nil
}

// ── spec helpers for tests ───────────────────────────────────────────────

// nodeSpec builds a NodeSpec from a config DTO (canonical marshaling).
func nodeSpec(id, kind string, cfg any) spec.NodeSpec {
	b, err := json.Marshal(cfg)
	if err != nil {
		panic(err)
	}
	return spec.NodeSpec{ID: id, Kind: kind, Config: b}
}
