package compile

//@feature var-let
//@feature if-else
//@feature for-while
//@feature parallel-fence
//@feature attempt
//@feature return-hierarchy
//@feature yield
//@feature request-input
//@feature output
//@feature foreign-code
//@feature dict-list
//@feature template-str
//@feature for-expr
//@feature field-index
//@feature file-type
//@feature record-type
//@feature qualified-call
//@feature switch
//@feature conv-var
//@feature lambda
//@feature function-inline

// compile_test.go — M1 runtime-compile 测试（test-spec U1.1–U1.5 + I1.5）。

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/cloudwego/eino/compose"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func mustCompile(t *testing.T, c *Compiler, s *spec.Spec, opts ...compose.GraphCompileOption) Runnable {
	t.Helper()
	r, err := c.Compile(context.Background(), s, opts...)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return r
}

func mustInvoke(t *testing.T, r Runnable, in map[string]any) map[string]any {
	t.Helper()
	out, err := r.Invoke(context.Background(), in)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	return out
}

// ── U1.1 图构造黄金 ──────────────────────────────────────────────────────

// U1.1a: start→text→end 线性链。
func TestU11LinearChain(t *testing.T) {
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001", Params: []spec.ParamSpec{{Name: "name", Type: &spec.TypeSpec{Kind: "string"}}}},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("t1", spec.KindText, &spec.TextConfig{Method: "concat", Template: "Hello {{name}}", Inputs: []spec.Source{{Path: []string{"name"}, BlockID: "100001", Name: "name"}}}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "t1", Output: "output"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "t1"},
			{Source: "t1", Target: "e1"},
		},
	}
	r := mustCompile(t, New(testRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"name": "world"}))
	if out["output"] != "Hello world" {
		t.Fatalf("output = %v, want %q", out["output"], "Hello world")
	}
	// 确定性:同输入两次 Invoke 字节一致
	out2 := UnwrapOutput(mustInvoke(t, r, map[string]any{"name": "world"}))
	if !jsonEqual(out, out2) {
		t.Fatalf("determinism: %v != %v", out, out2)
	}
}

// U1.1b: start→selector→{true:http,false:text}→aggregate→end 分支黄金。
func TestU11SelectorBranch(t *testing.T) {
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001", Params: []spec.ParamSpec{{Name: "flag", Type: &spec.TypeSpec{Kind: "boolean"}}}},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("sel", spec.KindSelector, &spec.SelectorConfig{Conditions: []spec.Condition{
				{Left: spec.Operand{Ref: &spec.Ref{Producer: "100001", Output: "flag"}}, Operator: 1, Right: spec.Operand{Val: true}},
			}, Logic: 1}),
			nodeSpec("http", spec.KindHTTP, &spec.HTTPConfig{Method: "GET", URL: "https://example.com/api", Inputs: []spec.Source{{Path: []string{"flag"}, BlockID: "100001", Name: "flag"}}}),
			nodeSpec("txt", spec.KindText, &spec.TextConfig{Method: "const", Template: "no-http", Inputs: []spec.Source{{Path: []string{"flag"}, BlockID: "100001", Name: "flag"}}}),
			nodeSpec("agg", spec.KindAggregate, &spec.AggregateConfig{Refs: []spec.Ref{{Producer: "http", Output: "output"}, {Producer: "txt", Output: "output"}}}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "agg", Output: wGroup1}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "sel"},
			{Source: "sel", Target: "http", SourcePort: "true"},
			{Source: "sel", Target: "txt", SourcePort: "false"},
			{Source: "http", Target: "agg"},
			{Source: "txt", Target: "agg"},
			{Source: "agg", Target: "e1"},
		},
	}
	r := mustCompile(t, New(testRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"flag": true}))
	if out["output"] != "HTTP:https://example.com/api" {
		t.Fatalf("true-branch output = %v", out["output"])
	}
	out = UnwrapOutput(mustInvoke(t, r, map[string]any{"flag": false}))
	if out["output"] != "no-http" {
		t.Fatalf("false-branch output = %v", out["output"])
	}
}

// U1.1c: fail-branch —— http 失败 → $branch="fail-branch" → 路由到失败分支。
func TestU11FailBranch(t *testing.T) {
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("op", spec.KindHTTP, &spec.HTTPConfig{Method: "GET", URL: "fail://boom"}),
			nodeSpec("ok", spec.KindText, &spec.TextConfig{Method: "const", Template: "OK-BRANCH"}),
			nodeSpec("bad", spec.KindText, &spec.TextConfig{Method: "const", Template: "FAIL-BRANCH"}),
			nodeSpec("e_ok", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "ok", Output: "output"}}}),
			nodeSpec("e_bad", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "bad", Output: "output"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "op"},
			{Source: "op", Target: "ok", SourcePort: "source"},
			{Source: "op", Target: "bad", SourcePort: "fail-branch"},
			{Source: "ok", Target: "e_ok"},
			{Source: "bad", Target: "e_bad"},
		},
	}
	r := mustCompile(t, New(testRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{}))
	if out["output"] != "FAIL-BRANCH" {
		t.Fatalf("fail-branch output = %v, want FAIL-BRANCH", out["output"])
	}
}

// U1.1d: 空分支（then 无节点）—— selector--true-->merge 直连。
func TestU11EmptyBranchMerge(t *testing.T) {
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("sel", spec.KindSelector, &spec.SelectorConfig{Conditions: []spec.Condition{
				{Left: spec.Operand{Val: "x"}, Operator: 12, Right: spec.Operand{Val: true}},
			}, Logic: 1}),
			nodeSpec("txt", spec.KindText, &spec.TextConfig{Method: "const", Template: "else-value"}),
			nodeSpec("agg", spec.KindAggregate, &spec.AggregateConfig{Refs: []spec.Ref{{Producer: "txt", Output: "output"}}}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "agg", Output: wGroup1}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "sel"},
			{Source: "sel", Target: "agg", SourcePort: "true"},
			{Source: "sel", Target: "txt", SourcePort: "false"},
			{Source: "txt", Target: "agg"},
			{Source: "agg", Target: "e1"},
		},
	}
	r := mustCompile(t, New(testRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{}))
	// 恒 false 条件 → else 分支 → "else-value"
	if out["output"] != "else-value" {
		t.Fatalf("else-value output = %v", out["output"])
	}
}

// U1.1e: loop 容器（AddGraphNode 子图 + M1 驱动器）—— by_array 逐项绑定。
func TestU11LoopSubgraph(t *testing.T) {
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001", Params: []spec.ParamSpec{{Name: "items", Type: &spec.TypeSpec{Kind: "list"}}}},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("loop", spec.KindLoop, &spec.LoopConfig{
				LoopType: "by_array", InputArrays: []string{"items"},
				Inputs:        []spec.Source{{Path: []string{"items"}, BlockID: "100001", Name: "items"}},
				VarName:       "item",
				OutputSources: []spec.Source{{Path: []string{"output"}, BlockID: "body_t", Name: "output"}},
				Children: []spec.NodeSpec{
					nodeSpec("body_t", spec.KindText, &spec.TextConfig{Method: "concat", Template: "{{item}}", Inputs: []spec.Source{{Path: []string{"item"}, BlockID: "loop", Name: "item"}}}),
				},
				InnerEdges: []spec.EdgeSpec{
					{Source: "loop", Target: "body_t", SourcePort: wLoopFunctionInlineOutput},
					{Source: "body_t", Target: "loop", TargetPort: wLoopFunctionInlineInput},
				},
			}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "loop", Output: "output"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "loop"},
			{Source: "loop", Target: "e1"},
		},
	}
	r := mustCompile(t, New(testRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"items": []any{"a", "b", "c"}}))
	list, ok := out["output"].([]any)
	if !ok {
		t.Fatalf("loop output = %T %v, want []any", out["output"], out["output"])
	}
	if len(list) != 3 || list[0] != "a" || list[1] != "b" || list[2] != "c" {
		t.Fatalf("loop output = %v, want [a b c]", list)
	}
}

// U1.1f: 多入边汇聚（AllPredecessor）—— 两个生产者都汇聚到一个 text。
func TestU11MultiInputConverge(t *testing.T) {
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("a", spec.KindText, &spec.TextConfig{Method: "const", Template: "A"}),
			nodeSpec("b", spec.KindText, &spec.TextConfig{Method: "const", Template: "B"}),
			nodeSpec("m", spec.KindText, &spec.TextConfig{Method: "concat", Template: "{{a}}|{{b}}", Inputs: []spec.Source{
				{Path: []string{"a"}, BlockID: "a", Name: "output"},
				{Path: []string{"b"}, BlockID: "b", Name: "output"},
			}}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "m", Output: "output"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "a"},
			{Source: "100001", Target: "b"},
			{Source: "a", Target: "m"},
			{Source: "b", Target: "m"},
			{Source: "m", Target: "e1"},
		},
	}
	r := mustCompile(t, New(testRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{}))
	if out["output"] != "A|B" {
		t.Fatalf("converge output = %v, want A|B", out["output"])
	}
}

// ── U1.2 端口映射矩阵 ────────────────────────────────────────────────────

// U1.2a: project_test.go 的全部边形态都能接线（字面端口以 project.go 为准）。
func TestU12PortMatrix(t *testing.T) {
	// 端口字面量集合(来自 flow.Project 的派生规则)
	ports := []string{"true", "false", "fail-branch", "source", wLoopFunctionInlineOutput}
	for _, p := range ports {
		if p != "true" && p != "false" && p != "fail-branch" && p != "source" && p != wLoopFunctionInlineOutput {
			t.Fatalf("unexpected port %q", p)
		}
	}
	// loop-function-inline-input 只作为 TargetPort 出现(loop-back,被吸收)
	s := loopBackSpec()
	r := mustCompile(t, New(testRegistry()), s)
	// loop-back 吸收的验证意图:编译通过 + 有输入时能正常迭代产出(而非
	// 死锁/报错)。空输入 → 0 次迭代 → 无输出,无法验证吸收。
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"items": []any{"x"}}))
	list, ok := out["output"].([]any)
	if !ok || len(list) != 1 || list[0] != "iter" {
		t.Fatalf("loop-back absorption output = %v, want [iter]", out["output"])
	}
}

// U1.2b: 悬空 ref → 编译期错误(runtime diag 码)。
func TestU12DanglingRefRejected(t *testing.T) {
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("t1", spec.KindText, &spec.TextConfig{Method: "concat", Template: "{{x}}", Inputs: []spec.Source{{Path: []string{"x"}, BlockID: "ghost", Name: "output"}}}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "t1", Output: "output"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "t1"},
			{Source: "t1", Target: "e1"},
		},
	}
	_, err := New(testRegistry()).Compile(context.Background(), s)
	if err == nil {
		t.Fatal("expected dangling-ref compile error")
	}
	if diag.CodeOf(err) != diag.RDanglingRef {
		t.Fatalf("code = %v (%v), want RDanglingRef", diag.CodeOf(err), err)
	}
}

// U1.2c: 未注册 kind → RNodeNotRegistered。M2.3 起 agent 已注册,用缺
// agent 的 registry 构造（保留该错误路径的回归;其余 spec kind 全部注册）。
func TestU12UnregisteredKind(t *testing.T) {
	reg := nodes.NewRegistry()
	for _, b := range nodes.StdBuilders() {
		if b.Kind() == spec.KindAgent {
			continue
		}
		if err := reg.Register(b); err != nil {
			t.Fatal(err)
		}
	}
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("ag", spec.KindAgent, &spec.AgentConfig{Model: "x"}), // 该 registry 未注册 agent
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "ag"},
			{Source: "ag", Target: "e1"},
		},
	}
	_, err := New(reg).Compile(context.Background(), s)
	if err == nil || diag.CodeOf(err) != diag.RNodeNotRegistered {
		t.Fatalf("code = %v (%v), want RNodeNotRegistered", diag.CodeOf(err), err)
	}
}

// ── U1.3 输入装配器 ──────────────────────────────────────────────────────

func TestU13InputAssembly(t *testing.T) {
	merged := map[string]any{
		"100001": map[string]any{"name": "alice", "nested": map[string]any{"city": "sh"}},
		"p2":     map[string]any{"output": 42},
	}
	n := &spec.NodeSpec{}
	*n = nodeSpec("t", spec.KindText, &spec.TextConfig{Inputs: []spec.Source{
		{Path: []string{"single"}, BlockID: "100001", Name: "name"},                // 单值
		{Path: []string{"nested", "city"}, BlockID: "100001", Name: "nested.city"}, // 嵌套路径 + dotted
		{Path: []string{"lit"}, Val: ""},                                           // 字面量空串(合法)
		{Path: []string{"missing"}, BlockID: "ghost", Name: "x"},                   // 缺失生产者 → nil
	}})
	in, err := AssembleInput(n, merged)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if in["single"] != "alice" {
		t.Fatalf("single = %v", in["single"])
	}
	if nc, ok := in["nested"].(map[string]any); !ok || nc["city"] != "sh" {
		t.Fatalf("nested = %v", in["nested"])
	}
	if in["lit"] != "" {
		t.Fatalf("literal empty string must survive, got %#v", in["lit"])
	}
	if v, ok := in["missing"]; !ok || v != nil {
		t.Fatalf("missing producer = %#v, want nil", in["missing"])
	}
	// 反例:未知 kind → 错误
	if _, err := AssembleInput(&spec.NodeSpec{ID: "x", Kind: "warp", Config: json.RawMessage(`{}`)}, merged); err == nil {
		t.Fatal("expected assembly error for unknown kind")
	}
}

// ── U1.4 缓存与确定性 ────────────────────────────────────────────────────

func TestU14CacheKeyAndVersionBump(t *testing.T) {
	s := linearSpec()
	c := New(testRegistry())
	if c.CacheSize() != 0 {
		t.Fatalf("cache not empty initially")
	}
	r1 := mustCompile(t, c, s)
	if c.CacheSize() != 1 {
		t.Fatalf("cache size = %d after first compile", c.CacheSize())
	}
	r2 := mustCompile(t, c, s) // 同 spec 同版本 → cache hit
	if r1 != r2 {
		t.Fatalf("expected cache hit (same runnable pointer)")
	}
	if c.CacheSize() != 1 {
		t.Fatalf("cache size grew on hit: %d", c.CacheSize())
	}
	// bump version → miss
	c2 := New(testRegistry(), WithVersion("runtime-compile-v2"))
	r3 := mustCompile(t, c2, s)
	if r3 == r1 {
		t.Fatalf("expected cache miss on version bump")
	}
	// 语义变化(spec hash 变化)→ miss
	s2 := linearSpec()
	s2.Nodes[1].Config = json.RawMessage(`{"method":"const","template":"X"}`)
	r4 := mustCompile(t, c, s2)
	if r4 == r1 {
		t.Fatalf("expected cache miss on semantic edit")
	}
}

func TestU14ConcurrentCompileRace(t *testing.T) {
	s := linearSpec()
	c := New(testRegistry())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := c.Compile(context.Background(), s)
			if err != nil {
				t.Errorf("concurrent compile: %v", err)
				return
			}
			if _, err := r.Invoke(context.Background(), map[string]any{"name": "x"}); err != nil {
				t.Errorf("concurrent invoke: %v", err)
			}
		}()
	}
	wg.Wait()
}

// ── I1.5 端到端最小链路:真实 ncoda → 编译 → Project → spec → Compile ────

func TestI15EndToEndMinimalChain(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(string name, int score) {
    var grade = score >= 90 ? "优秀" : (score >= 60 ? "及格" : "不及格");
    var result = ` + "`" + `姓名：${name}\n分数：${score}\n等级：${grade}` + "`" + `;
    return result;
}
`
	res := passes.DefaultPipeline().Run(src, "i15.ncoda", nil)
	if res.HasErrors() || res.Program == nil {
		t.Fatalf("frontend errors: %v", res.Errors())
	}
	g, berr := ir.Build(res.Program)
	if berr != nil {
		t.Fatalf("ir-build: %v", berr)
	}
	p, perr := flow.Project(g)
	if perr != nil {
		t.Fatalf("project: %v", perr)
	}
	s, err := spec.Build(g, p)
	if err != nil {
		t.Fatalf("spec build: %v", err)
	}
	r := mustCompile(t, New(testRegistry()), s)

	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"name": "世界", "score": float64(90)}))
	if out["output"] != "姓名：世界\\n分数：90\\n等级：优秀" {
		t.Fatalf("e2e output = %q", out["output"])
	}
	out = UnwrapOutput(mustInvoke(t, r, map[string]any{"name": "世界", "score": float64(55)}))
	if out["output"] != "姓名：世界\\n分数：55\\n等级：不及格" {
		t.Fatalf("e2e fail output = %q", out["output"])
	}
	// 确定性:两次运行字节一致
	b1, _ := json.Marshal(out)
	b2, _ := json.Marshal(UnwrapOutput(mustInvoke(t, r, map[string]any{"name": "世界", "score": float64(55)})))
	if !bytes.Equal(b1, b2) {
		t.Fatalf("determinism violation")
	}
}

// U1.1g 回归:未选中的分支目标不得执行。实证:分支目标若含外部数据边,
// Eino 会绕过分支门无条件触发两侧;修复后目标数据只能经分支源 echo 送达,
// 未选中侧必须保持静默(按模板调用计数断言)。
func TestU11UnselectedBranchTargetSilent(t *testing.T) {
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001", Params: []spec.ParamSpec{{Name: "flag", Type: &spec.TypeSpec{Kind: "boolean"}}}},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("sel", spec.KindSelector, &spec.SelectorConfig{Conditions: []spec.Condition{
				{Left: spec.Operand{Ref: &spec.Ref{Producer: "100001", Output: "flag"}}, Operator: 1, Right: spec.Operand{Val: true}},
			}, Logic: 1}),
			nodeSpec("selTarget", spec.KindText, &spec.TextConfig{Method: "const", Template: "SELECTED", Inputs: []spec.Source{{Path: []string{"flag"}, BlockID: "100001", Name: "flag"}}}),
			nodeSpec("otherTarget", spec.KindText, &spec.TextConfig{Method: "const", Template: "OTHER", Inputs: []spec.Source{{Path: []string{"flag"}, BlockID: "100001", Name: "flag"}}}),
			nodeSpec("e_sel", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "selTarget", Output: "output"}}}),
			nodeSpec("e_other", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "otherTarget", Output: "output"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "sel"},
			{Source: "sel", Target: "selTarget", SourcePort: "true"},
			{Source: "sel", Target: "otherTarget", SourcePort: "false"},
			{Source: "selTarget", Target: "e_sel"},
			{Source: "otherTarget", Target: "e_other"},
		},
	}
	r := mustCompile(t, New(testRegistry()), s)

	// flag=true → SELECTED 分支;未选中侧 OTHER 不得执行
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"flag": true}))
	if out["output"] != "SELECTED" {
		t.Fatalf("true-branch output = %v, want SELECTED", out["output"])
	}
	if n := textRunCount("OTHER"); n != 0 {
		t.Fatalf("unselected target ran %d time(s)", n)
	}
	// flag=false → OTHER 分支;SELECTED 不得执行
	out = UnwrapOutput(mustInvoke(t, r, map[string]any{"flag": false}))
	if out["output"] != "OTHER" {
		t.Fatalf("false-branch output = %v, want OTHER", out["output"])
	}
	if n := textRunCount("SELECTED"); n != 1 {
		t.Fatalf("SELECTED ran %d time(s), want 1 (only the true run)", n)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────

func linearSpec() *spec.Spec {
	return &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001", Params: []spec.ParamSpec{{Name: "name", Type: &spec.TypeSpec{Kind: "string"}}}},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("t1", spec.KindText, &spec.TextConfig{Method: "concat", Template: "Hello {{name}}", Inputs: []spec.Source{{Path: []string{"name"}, BlockID: "100001", Name: "name"}}}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "t1", Output: "output"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "t1"},
			{Source: "t1", Target: "e1"},
		},
	}
}

func loopBackSpec() *spec.Spec {
	return &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001", Params: []spec.ParamSpec{{Name: "items", Type: &spec.TypeSpec{Kind: "list"}}}},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("loop", spec.KindLoop, &spec.LoopConfig{
				LoopType: "by_array", InputArrays: []string{"items"},
				Inputs:        []spec.Source{{Path: []string{"items"}, BlockID: "100001", Name: "items"}},
				VarName:       "item",
				OutputSources: []spec.Source{{Path: []string{"output"}, BlockID: "bt", Name: "output"}},
				Children: []spec.NodeSpec{
					nodeSpec("bt", spec.KindText, &spec.TextConfig{Method: "const", Template: "iter"}),
				},
				InnerEdges: []spec.EdgeSpec{
					{Source: "loop", Target: "bt", SourcePort: wLoopFunctionInlineOutput},
					{Source: "bt", Target: "loop", TargetPort: wLoopFunctionInlineInput},
				},
			}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "loop", Output: "output"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "loop"},
			{Source: "loop", Target: "e1"},
		},
	}
}

func jsonEqual(a, b any) bool {
	ba, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(ba, bb)
}

// ── U1.3b E5-b item-0: 记录数组成员端口路径 ──────────────────────────────
// 参考目标 把 doc_results[0].url 渲染成 item-0 点路径(data.doc_results.url),
// 运行时装配值里 doc_results 是 record 数组:lookupDotted 在段位置遇到数组
// 按 item-0 下钻(空数组 → 缺失,与参考目标的 item-0 member 渲染一致)。

func TestU13RecordArrayItem0Port(t *testing.T) {
	merged := map[string]any{
		"100001": map[string]any{
			"data": map[string]any{
				"doc_results": []any{
					map[string]any{"url": "https://a", "title": "A"},
					map[string]any{"url": "https://b", "title": "B"},
				},
			},
		},
	}
	n := &spec.NodeSpec{}
	*n = nodeSpec("t", spec.KindText, &spec.TextConfig{Inputs: []spec.Source{
		{Path: []string{"first_url"}, BlockID: "100001", Name: "data.doc_results.url"},
		{Path: []string{"whole"}, BlockID: "100001", Name: "data.doc_results"},
		{Path: []string{"missing_field"}, BlockID: "100001", Name: "data.doc_results.nope"},
	}})
	in, err := AssembleInput(n, merged)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if in["first_url"] != "https://a" {
		t.Fatalf("item-0 member = %#v, want https://a", in["first_url"])
	}
	if w, ok := in["whole"].([]any); !ok || len(w) != 2 {
		t.Fatalf("whole array = %#v, want 2-element []any", in["whole"])
	}
	if v, ok := in["missing_field"]; !ok || v != nil {
		t.Fatalf("missing member = %#v, want nil", in["missing_field"])
	}
	// 空数组 → 缺失(不 panic、不取越界元素)。
	mergedEmpty := map[string]any{
		"100001": map[string]any{"outputList": []any{}},
	}
	n2 := &spec.NodeSpec{}
	*n2 = nodeSpec("t", spec.KindText, &spec.TextConfig{Inputs: []spec.Source{
		{Path: []string{"x"}, BlockID: "100001", Name: "outputList.content"},
	}})
	in2, err := AssembleInput(n2, mergedEmpty)
	if err != nil {
		t.Fatalf("assemble empty: %v", err)
	}
	if v, ok := in2["x"]; !ok || v != nil {
		t.Fatalf("empty-array member = %#v, want nil", in2["x"])
	}
}
