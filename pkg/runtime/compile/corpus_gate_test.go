package compile

// corpus_gate_test.go — 全语料运行时门禁（M14）。
//
// 对 internal/compiler/testdata/examples/nodecoda 下每个 example 跑完整
// 链路（frontend → ir → flow.Project → spec.v1 → eino 编译 → Invoke），
// 按三态断言:pass（期望跑通）/ reject（D4 刻意裁剪,锁定错误码）/
// skip（closed-world tool 需要 capabilities,runtime 链路不提供）。
//
// 确定性: echo 模型（离线）+ stub HTTP（不真出网）+ 空输入。
// 设置 CORPUS_DUMP=1 打印全量实际状态（用于基线刷新）。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// ── 确定性依赖 ──────────────────────────────────────────────────────

// gateEchoModel 是离线确定性的模型工厂:llm 返回固定文本,agent 无工具调用。
type gateEchoModel struct{}

func (gateEchoModel) Chat(ctx context.Context, model string, msgs []nodes.Message) (string, error) {
	return "echo:" + model, nil
}

func (gateEchoModel) ChatWithTools(ctx context.Context, model string, msgs []nodes.Message, tools []nodes.ToolSpec) (nodes.ChatResult, error) {
	return nodes.ChatResult{Content: "echo:" + model}, nil
}

// gateStubHTTP 是离线 HTTP stub:任何请求返回 200 + 固定 JSON。
type gateStubHTTP struct{}

func (gateStubHTTP) Do(ctx context.Context, method, url string, headers map[string]string, body any) (int, []byte, error) {
	return 200, []byte(`{"ok":true}`), nil
}

// gateStubKnowledge 是离线知识检索 stub:对任何 query 返回一条固定命中
// （M14 解锁 RAG 运行时链路;片段文本含 query 回显以便断言）。
type gateStubKnowledge struct{}

func (gateStubKnowledge) Retrieve(ctx context.Context, query string, datasetIDs []string) ([]nodes.KnowledgeChunk, error) {
	return []nodes.KnowledgeChunk{
		{Content: "chunk-for:" + query, Title: "gate-doc", Score: 0.9},
	}, nil
}

// ── 期望表 ──────────────────────────────────────────────────────────

// gateWant 是单个 example 的期望状态。
type gateWant struct {
	want      string // pass | reject | skip | interrupt
	errCode   string // reject 时锁定的错误码前缀
	extraPass string // pass 时输出必须包含的子串（空=只要求 Invoke 成功）
}

// gateExpect 在 CORPUS_DUMP=1 基线后固化。interrupt = request_input 的
// HITL 挂起（无恢复器时确定性地以 interrupt 错误结束,runtime 语义正确）。

var gateSkip = map[string]bool{
	// closed-world 工具 example:import "search.google_search" / "clock.time"
	// 需要 capabilities 目录,runtime 门禁链路（passes.Run）不提供。
	"06_tool_use.ncoda":           true,
	"38_chatflow_time_tool.ncoda": true,
	// Needs the public capability registry (libraries/) to resolve
	// `import "database"`; this gate runs the closed-world chain on purpose.
	// Positive evidence: pkg/localtools database_e2e_test.go + `make push`.
	"62_local_library_roundtrip.ncoda": true,
}

var gateExpect = map[string]gateWant{
	"00_minimal_start_llm_end.ncoda": {want: "pass"},
	"01_simple_chat.ncoda":           {want: "pass"},
	"03_conditional_branch.ncoda":    {want: "pass"},
	"05_http_request.ncoda":          {want: "pass", extraPass: "网页状态码：200"},
	"07_template_transform.ncoda":    {want: "pass"},
	"08_variable_aggregator.ncoda":   {want: "pass"},
	"09_iteration.ncoda":             {want: "pass"},
	"12_loop_with_break.ncoda":       {want: "pass", extraPass: "\"output\":5"},
	// 13_loop_with_answer: the open corpus keeps it; its int/string cross-exit
	// return is accepted by the language type pass and runs with i=1. The
	// commercial corpus moved the same source to its type-error lane.
	"13_loop_with_answer.ncoda":                  {want: "pass", extraPass: "\"output\":1"},
	"15_conditional_hello_branching.ncoda":       {want: "pass"},
	"16_basic_chatflow.ncoda":                    {want: "pass"},
	"18_file_upload.ncoda":                       {want: "pass"},
	"22_conditional_streaming_vs_template.ncoda": {want: "pass"},
	"23_hitl_workflow.ncoda":                     {want: "pass"},
	"25_echo_chat.ncoda":                         {want: "pass"},
	"26_simple_passthrough.ncoda":                {want: "pass"},
	"27_reasoning_chat.ncoda":                    {want: "pass"},
	"28_file_chat.ncoda":                         {want: "pass"},
	"29_ws2_workflow.ncoda":                      {want: "pass"},
	"30_workflow_llm.ncoda":                      {want: "pass"},
	"31_end_node_without_value_type.ncoda":       {want: "pass"},
	"32_answer_end_with_text.ncoda":              {want: "pass"},
	"33_test_answer_order.ncoda":                 {want: "pass"},
	"34_test_complex_branch.ncoda":               {want: "pass"},
	"35_multilingual_parallel_llm.ncoda":         {want: "pass"},
	"36_response_stream_filter.ncoda":            {want: "pass"},
	"42_stdlib_http_summary.ncoda":               {want: "pass"},
	"48_response_accumulation.ncoda":             {want: "pass", extraPass: "\"output\":\"probe:final\""},
	"50_condition_direct.ncoda":                  {want: "pass"},
	"51_condition_calculation.ncoda":             {want: "pass"},
	"53_assigner_v2.ncoda":                       {want: "pass", extraPass: "\"output\":\"updated\""},
	"54_variable_assigner_e2e.ncoda":             {want: "pass", extraPass: "\"output\":\"hello\""},
	"55_variable_assigner_closed_loop.ncoda":     {want: "pass", extraPass: "\"output\":\"welcome\""},
	"56_list_operator.ncoda":                     {want: "pass"},
	"57_parameter_extractor.ncoda":               {want: "pass"},
	"59_operation_retry_policy.ncoda":            {want: "pass"},
	"60_typed_var_decl.ncoda":                    {want: "pass"},
	"99_echo_test.ncoda":                         {want: "pass"},
	"attempt_recovery.ncoda":                     {want: "pass", extraPass: "unexpected-success"},
	"customer-support.ncoda":                     {want: "pass"},
	"for_expression.ncoda":                       {want: "pass"},
	"test.ncoda":                                 {want: "pass"},
	"02_rag_workflow.ncoda":                      {want: "pass"},
	"04_code_execution.ncoda":                    {want: "pass"},
	"10_complex_multi_step.ncoda":                {want: "pass"},
	"11_dual_switch_variable_aggregator.ncoda":   {want: "pass"},
	"14_conditional_parallel_code.ncoda":         {want: "pass"},
	"17_echo_workflow.ncoda":                     {want: "pass"},
	"19_http_request_json_tool.ncoda":            {want: "pass"},
	"20_array_iteration_formatting.ncoda":        {want: "pass"},
	"21_iteration_flatten_output.ncoda":          {want: "pass"},
	"24_llm_with_knowledge_retrieval.ncoda":      {want: "pass"},
	"37_iteration_flatten_disabled.ncoda":        {want: "pass"},
	"39_code_typed_direct_output.ncoda":          {want: "pass"},
	"40_code_typed_structural_output.ncoda":      {want: "pass"},
	"41_stdlib_rag.ncoda":                        {want: "pass"},
	"44_parallel_barrier.ncoda":                  {want: "pass"},
	"45_iteration_yield.ncoda":                   {want: "pass"},
	"46_loop_escape.ncoda":                       {want: "pass", extraPass: "\"output\":4"},
	"47_parallel_product.ncoda":                  {want: "pass"},
	"49_for_statement_plain.ncoda":               {want: "pass"},
	"parallel_for_keep_null.ncoda":               {want: "pass", extraPass: "\"output\":[\"a\",null,\"c\"]"}, // `keep_null` = 留空位（正本 §①）：失败迭代保序、值为 null（2026-09-29 修：此前压缩成 ["a","c"]，与正本/类型层/参考目标 出处三方相反）
	"61_request_input.ncoda":                     {want: "interrupt"},
}

var errRe = regexp.MustCompile(`^[A-Z_]+`)

func TestCorpusRuntimeGate(t *testing.T) {
	corpus := "../../../examples/nodecoda"
	files, err := filepath.Glob(filepath.Join(corpus, "*.ncoda"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	dump := os.Getenv("CORPUS_DUMP") != ""
	deps := nodes.Deps{Models: gateEchoModel{}, HTTP: gateStubHTTP{}, Knowledge: gateStubKnowledge{}}
	var nPass, nReject, nSkip, nFail int
	for _, f := range files {
		name := filepath.Base(f)
		if gateSkip[name] {
			nSkip++
			t.Logf("SKIP %-42s closed-world", name)
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		got, status := runGate(string(src), name, deps)
		if dump {
			t.Logf("%-42s %s :: %s :: OUT=%s", name, status, got.err, got.output)
			continue
		}
		want, ok := gateExpect[name]
		if !ok {
			t.Errorf("%s: no expectation recorded (run with CORPUS_DUMP=1)", name)
			nFail++
			continue
		}
		switch want.want {
		case "pass":
			if status != "pass" {
				t.Errorf("%s: want pass, got %s (%s)", name, status, got.err)
				nFail++
			} else if want.extraPass != "" && !strings.Contains(got.output, want.extraPass) {
				t.Errorf("%s: output missing %q (got %q)", name, want.extraPass, got.output)
				nFail++
			} else {
				nPass++
			}
		case "reject":
			if status != "reject" {
				t.Errorf("%s: want reject(%s), got %s (%s)", name, want.errCode, status, got.err)
				nFail++
			} else if want.errCode != "" {
				code := errRe.FindString(got.err)
				if !strings.HasPrefix(code, want.errCode) {
					t.Errorf("%s: reject code %s, want prefix %s", name, code, want.errCode)
					nFail++
				} else {
					nReject++
				}
			} else {
				nReject++
			}
		case "skip":
			nSkip++
		case "interrupt":
			// request_input HITL:run-err + "interrupt happened" 是确定性的
			// 期望行为（无恢复器;恢复链路属 checkpoint 里程碑）。
			if status == "run-err" && strings.Contains(got.err, "interrupt happened") {
				nPass++
			} else {
				t.Errorf("%s: want interrupt, got %s (%s)", name, status, got.err)
				nFail++
			}
		default:
			t.Errorf("%s: bad want %q", name, want.want)
			nFail++
		}
	}
	t.Logf("=== corpus gate: pass=%d reject=%d skip=%d fail=%d (corpus=%d)", nPass, nReject, nSkip, nFail, len(files))
	if !dump && nFail > 0 {
		t.Errorf("corpus gate not green")
	}
}

// gateResult 是一次运行的实际结果。
type gateResult struct {
	err    string
	output string
}

func runGate(src, name string, deps nodes.Deps) (gr gateResult, gs string) {
	defer func() {
		if r := recover(); r != nil {
			gr = gateResult{err: "PANIC: " + fmt.Sprint(r)}
			gs = "panic"
		}
	}()
	res := passes.DefaultPipeline().Run(src, name, nil)
	if res.HasErrors() || res.Program == nil {
		parts := make([]string, 0, len(res.Errors()))
		for _, d := range res.Errors() {
			parts = append(parts, d.Code.String()+": "+d.Message)
		}
		return gateResult{err: strings.Join(parts, "; ")}, "frontend-err"
	}
	g, berr := ir.Build(res.Program)
	if berr != nil {
		return gateResult{err: berr.Error()}, "ir-err"
	}
	p, perr := flow.Project(g)
	if perr != nil {
		return gateResult{err: perr.Error()}, "project-err"
	}
	s, serr := spec.Build(g, p)
	if serr != nil {
		return gateResult{err: serr.Error()}, "spec-err"
	}
	c := New(nodes.StdRegistry(), WithDeps(deps))
	r, cerr := c.Compile(context.Background(), s)
	if cerr != nil {
		return gateResult{err: cerr.Error()}, "reject"
	}
	out, ierr := r.Invoke(context.Background(), map[string]any{})
	if ierr != nil {
		return gateResult{err: ierr.Error()}, "run-err"
	}
	output := UnwrapOutput(out)
	b, _ := json.Marshal(output)
	return gateResult{output: string(b)}, "pass"
}
