package compile

// parallel_loop_e2e_test.go — 并行循环（is_parallel 容器）e2e。
//
// 覆盖:
//   - 位置保序:并发执行不改变输出序（round3 语义不动点前提）;
//   - 真并发:ParallelNums 上限生效（同步探针证据）;
//   - terminate 快速失败:首个错误取消其余迭代,不悬挂;
//   - continue-on-error:失败迭代输出**留空位**（位置保留、值为 nil —— 正本
//     `docs/five-step/spec/F3-error-policy-container-axis.md` §① 的「留空位」）;
//   - remove-abnormal-output:失败迭代输出**被压缩**（位置不保留,结果变短）;
//   - 构建期拒绝:并行 + state / break 特性 fail-fast（RKindRejected）;
//     continue 由 parallel_continue_e2e_test.go 覆盖。
//
// 常规 body 用 synthetic python shim（subprocess）,并发证据用同步探针。

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// parallelLoopSpec 构造 spec:
// start → src(values=[1,2,3,4]) → LOOP(by_array, var=value, is_parallel,
// concurrency=conc) → body(code: value→result) → end。
// bodyCode 允许注入自定义 body（错误/计时场景）。
func parallelLoopSpec(bodyCode string, conc int, errMode string, values []any) *spec.Spec {
	py := "["
	for i, v := range values {
		if i > 0 {
			py += ", "
		}
		py += fmt.Sprintf("%v", v)
	}
	py += "]"
	return &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("src", spec.KindCode, &spec.CodeConfig{
				Code:      "def main() -> dict:\n    return {\"values\": " + py + "}",
				Language:  "python",
				Synthetic: true,
				OutOrder:  []string{"values"},
			}),
			nodeSpec("loop", spec.KindLoop, &spec.LoopConfig{
				VarName:         "value",
				LoopType:        "by_array",
				Inputs:          []spec.Source{{Path: []string{"values"}, BlockID: "src", Name: "values"}},
				Types:           map[string]*spec.TypeSpec{"values": {Kind: "list"}},
				IsParallel:      true,
				ParallelNums:    conc,
				ErrorHandleMode: errMode,
				OutputSources:   []spec.Source{{Path: []string{"output"}, BlockID: "body", Name: "result"}},
				Children: []spec.NodeSpec{
					nodeSpec("body", spec.KindCode, &spec.CodeConfig{
						Code:      bodyCode,
						Language:  "python",
						Synthetic: false,
						OutOrder:  []string{"result"},
						Inputs:    []spec.Source{{Path: []string{"value"}, BlockID: "loop", Name: "value"}},
					}),
				},
				InnerEdges: []spec.EdgeSpec{
					{Source: "loop", Target: "body", SourcePort: wLoopFunctionInlineOutput},
					{Source: "body", Target: "loop", TargetPort: wLoopFunctionInlineInput},
				},
			}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "loop", Output: "output"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "src"},
			{Source: "src", Target: "loop"},
			{Source: "loop", Target: "e1"},
		},
	}
}

const pbodyX10 = "def main(value) -> dict:\n    return {\"result\": value * 10}"
const pbodyErr = "def main(value) -> dict:\n    if value == 3:\n        raise ValueError(\"boom\")\n    return {\"result\": value}"

type parallelProbe struct {
	mu        sync.Mutex
	active    int
	maxActive int
	want      int
	release   chan struct{}
	once      sync.Once
}

func newParallelProbe(want int) *parallelProbe {
	return &parallelProbe{want: want, release: make(chan struct{})}
}

func (p *parallelProbe) Invoke(ctx context.Context, in map[string]any) (map[string]any, error) {
	p.mu.Lock()
	p.active++
	if p.active > p.maxActive {
		p.maxActive = p.active
	}
	ready := p.active == p.want
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.active--
		p.mu.Unlock()
	}()

	if ready {
		p.once.Do(func() { close(p.release) })
	}
	select {
	case <-p.release:
		return map[string]any{"result": in["value"]}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *parallelProbe) maximum() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxActive
}

func parallelProbeSpec(conc int, values []any) *spec.Spec {
	s := parallelLoopSpec(pbodyX10, conc, "terminated", values)
	for i := range s.Nodes {
		if s.Nodes[i].ID != "loop" {
			continue
		}
		var cfg spec.LoopConfig
		if err := json.Unmarshal(s.Nodes[i].Config, &cfg); err != nil {
			panic(err)
		}
		cfg.Children = []spec.NodeSpec{nodeSpec("body", spec.KindHTTP, &spec.HTTPConfig{
			Method: "GET",
			URL:    "probe://parallel",
			Inputs: []spec.Source{{Path: []string{"value"}, BlockID: "loop", Name: "value"}},
		})}
		s.Nodes[i] = nodeSpec("loop", spec.KindLoop, &cfg)
	}
	return s
}

func parallelProbeRegistry(probe *parallelProbe) *nodes.Registry {
	r := nodes.NewRegistry()
	for _, b := range nodes.StdBuilders() {
		if b.Kind() != spec.KindHTTP {
			if err := r.Register(b); err != nil {
				panic(err)
			}
		}
	}
	if err := r.Register(&stubBuilder{kind: spec.KindHTTP, build: func(context.Context, *spec.NodeSpec, nodes.Deps) (nodes.NodeImpl, error) {
		return probe, nil
	}}); err != nil {
		panic(err)
	}
	return r
}

// TestParallelLoopOrderPreserved:并发 2 跑 [1,2,3,4] → 输出精确 [10,20,30,40]
// （位置保序,无丢槽/乱序）。
func TestParallelLoopOrderPreserved(t *testing.T) {
	r := mustCompile(t, New(testRegistry()), parallelLoopSpec(pbodyX10, 2, "terminated", []any{1, 2, 3, 4}))
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{}))
	got := fmt.Sprint(out["output"])
	if got != "[10 20 30 40]" {
		t.Fatalf("output = %v, want [10 20 30 40] (位置保序)", got)
	}
}

// TestParallelLoopActuallyConcurrent:前 4 个迭代在同步屏障处全部进入后才
// 放行。串行或低于 ParallelNums 的执行会超时；maxActive 精确证明 worker 池
// 真正按配置并发，而不依赖 subprocess 启动速度或主机墙钟负载。
func TestParallelLoopActuallyConcurrent(t *testing.T) {
	const concurrency = 4
	probe := newParallelProbe(concurrency)
	r := mustCompile(t, New(parallelProbeRegistry(probe)), parallelProbeSpec(concurrency, []any{1, 2, 3, 4, 5, 6, 7, 8}))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := r.Invoke(ctx, map[string]any{}); err != nil {
		t.Fatalf("parallel invoke: %v", err)
	}
	if got := probe.maximum(); got != concurrency {
		t.Fatalf("max active iterations = %d, want %d", got, concurrency)
	}
}

// TestParallelLoopTerminateFastFail:value==3 抛错 → 快速失败返回错误
// （不悬挂,其余迭代被取消）。
func TestParallelLoopTerminateFastFail(t *testing.T) {
	r := mustCompile(t, New(testRegistry()), parallelLoopSpec(pbodyErr, 2, "terminated", []any{1, 2, 3, 4}))
	_, err := r.Invoke(context.Background(), map[string]any{})
	if err == nil {
		t.Fatalf("want error (terminate fast-fail), got nil")
	}
	if !containsStr(err.Error(), "boom") {
		t.Fatalf("err = %v, want boom", err)
	}
}

// TestParallelLoopContinueOnErrorKeepsPosition:continue-on-error 下失败迭代
// **留空位** —— 位置保留、值为 nil（[1 2 <nil> 4]，长度为输入长度）。
//
// 这条与下一条共同锁住「`keep_null` ≠ `remove_failed`」：两者在 ncoda 自己的
// 执行面必须是**可区分**的行为，否则三值闭集在执行层只剩两态、`keep_null`
// 就退化成 参考目标 按钮的别名（2026-09-29 一手发现并修；此前本用例名
// `…Compacted` 把压缩断言成了规范，与正本 / 类型层 / 平台出处三方相反）。
func TestParallelLoopContinueOnErrorKeepsPosition(t *testing.T) {
	r := mustCompile(t, New(testRegistry()), parallelLoopSpec(pbodyErr, 2, "continue-on-error", []any{1, 2, 3, 4}))
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{}))
	got := fmt.Sprint(out["output"])
	if got != "[1 2 <nil> 4]" {
		t.Fatalf("output = %v, want [1 2 <nil> 4] (失败迭代留空位、保序)", got)
	}
}

// TestParallelLoopRemoveAbnormalOutputCompacted:remove-abnormal-output 下失败
// 迭代**被压缩**（[1 2 4]，结果比输入短）。
func TestParallelLoopRemoveAbnormalOutputCompacted(t *testing.T) {
	r := mustCompile(t, New(testRegistry()), parallelLoopSpec(pbodyErr, 2, "remove-abnormal-output", []any{1, 2, 3, 4}))
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{}))
	got := fmt.Sprint(out["output"])
	if got != "[1 2 4]" {
		t.Fatalf("output = %v, want [1 2 4] (失败迭代压缩)", got)
	}
}

// TestParallelLoopRejectState:并行 + state_values → 构建期 RKindRejected。
func TestParallelLoopRejectState(t *testing.T) {
	s := parallelLoopSpec(pbodyX10, 2, "terminated", []any{1, 2})
	for i := range s.Nodes {
		if s.Nodes[i].ID == "loop" {
			lc := &spec.LoopConfig{}
			if err := json.Unmarshal(s.Nodes[i].Config, lc); err != nil {
				t.Fatal(err)
			}
			lc.StateValues = map[string]spec.Source{"acc": {Val: 0}}
			s.Nodes[i] = nodeSpec("loop", spec.KindLoop, lc)
		}
	}
	_, err := New(testRegistry()).Compile(context.Background(), s)
	if err == nil || diag.CodeOf(err) != diag.RKindRejected {
		t.Fatalf("want RKindRejected, got %v", err)
	}
}

// TestParallelLoopRejectBreak:并行 body 含 break → 构建期 RKindRejected。
func TestParallelLoopRejectBreak(t *testing.T) {
	s := parallelLoopSpec(pbodyX10, 2, "terminated", []any{1, 2})
	for i := range s.Nodes {
		if s.Nodes[i].ID == "loop" {
			lc := &spec.LoopConfig{}
			if err := json.Unmarshal(s.Nodes[i].Config, lc); err != nil {
				t.Fatal(err)
			}
			lc.Children = append(lc.Children, nodeSpec("brk", spec.KindBreak, &spec.BreakConfig{}))
			s.Nodes[i] = nodeSpec("loop", spec.KindLoop, lc)
		}
	}
	_, err := New(testRegistry()).Compile(context.Background(), s)
	if err == nil || diag.CodeOf(err) != diag.RKindRejected {
		t.Fatalf("want RKindRejected, got %v", err)
	}
}

func containsStr(hay, needle string) bool {
	return len(hay) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
