package compile

// demo2_test.go — start → llm → end 三节点工作流的执行过程实录。
// 展示:spec.v1 指令文本 → 编译成的 Eino 图 → 运行时每个节点的真实
// 输入/输出(打印顺序 = Eino 调度器的真实执行顺序)。

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/nodecoda/ncoda/pkg/llm"
	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// ── 带日志的演示节点 ────────────────────────────────────────────────────

type logStart struct{}

func (s *logStart) Invoke(_ context.Context, in map[string]any) (map[string]any, error) {
	fmt.Printf(">>> [100001 start ] 收到输入 %#v (entry 节点:wrap 直接透传原始图输入)\n", in)
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	fmt.Printf("<<< [100001 start ] 返回 %#v → wrap 命名空间化为 {100001: %#v}\n", out, out)
	return out, nil
}

type logLLM struct{}

func (l *logLLM) Invoke(_ context.Context, in map[string]any) (map[string]any, error) {
	fmt.Printf(">>> [100002 llm  ] 收到已装配输入 %#v (AssembleInput 从合并 map 按 Source.Path 取值)\n", in)
	out := map[string]any{"output": "LLM-echo:" + fmt.Sprint(in["query"])}
	fmt.Printf("<<< [100002 llm  ] 返回 %#v → wrap 命名空间化为 {100002: %#v}\n", out, out)
	return out, nil
}

type logEnd struct{}

func (e *logEnd) Invoke(_ context.Context, in map[string]any) (map[string]any, error) {
	fmt.Printf(">>> [900001 end  ] 收到已装配输入 %#v\n", in)
	fmt.Printf("<<< [900001 end  ] 返回 %#v → wrap 命名空间化为 {900001: %#v}\n", in, in)
	return in, nil
}

// demoRegistry 注册 start/llm/end 三个带日志的节点。
func demoRegistry() *nodes.Registry {
	r := nodes.NewRegistry()
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	must(r.Register(&stubBuilder{kind: spec.KindStart, build: func(context.Context, *spec.NodeSpec, nodes.Deps) (nodes.NodeImpl, error) {
		return &logStart{}, nil
	}}))
	must(r.Register(&stubBuilder{kind: spec.KindLLM, build: func(context.Context, *spec.NodeSpec, nodes.Deps) (nodes.NodeImpl, error) {
		return &logLLM{}, nil
	}}))
	must(r.Register(&stubBuilder{kind: spec.KindEnd, build: func(context.Context, *spec.NodeSpec, nodes.Deps) (nodes.NodeImpl, error) {
		return &logEnd{}, nil
	}}))
	return r
}

func TestDemoStartLLMEnd(t *testing.T) {
	// ── 1) 指令文本:spec.v1(手工构造,与 IR→spec.Build 产物同构)──
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001", Params: []spec.ParamSpec{{Name: "query", Type: &spec.TypeSpec{Kind: "string"}}}},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("100002", spec.KindLLM, &spec.LLMConfig{Model: "demo-model", Inputs: []spec.Source{{Path: []string{"query"}, BlockID: "100001", Name: "query"}}, Result: &spec.LLMResultConfig{PayloadSchema: llm.String()}}),
			nodeSpec("900001", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "100002", Output: "output"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "100002"},
			{Source: "100002", Target: "900001"},
		},
	}
	norm, _ := s.Normalize()
	jb, _ := json.MarshalIndent(norm, "", "  ")
	fmt.Println("═══════ 1) spec.v1 指令文本 ═══════")
	fmt.Println(string(jb))

	// ── 2) 编译成 Eino 图 ──
	fmt.Println("═══════ 2) 编译层(compile.go)做了什么 ═══════")
	fmt.Println("  · 每条指令 → 一个 wrap 闭包(输入装配 + 节点实现 + 命名空间化输出)")
	fmt.Println("  · AddEdge: START→100001, 100001→100002, 100002→900001")
	fmt.Println("  · AddLambdaNode: 100001/100002/900001;compile with AllPredecessor")
	r := mustCompile(t, New(demoRegistry()), s)

	// ── 3) 运行:Invoke 一次,打印真实调度顺序 ──
	fmt.Println("═══════ 3) 执行 trace(超步调度) ═══════")
	fmt.Println("  Invoke({query: \"你好\"}) → Eino runner 从 compose.START 投递输入")
	out := mustInvoke(t, r, map[string]any{"query": "你好"})
	fmt.Println("═══════ 4) 图原始输出 ═══════")
	fmt.Printf("  Eino 返回 %#v\n", out)
	fmt.Printf("  UnwrapOutput → %#v\n", UnwrapOutput(out))

	if got := UnwrapOutput(out)["output"]; got != "LLM-echo:你好" {
		t.Fatalf("output = %v, want LLM-echo:你好", got)
	}
}
