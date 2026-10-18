package decompile

//@feature var-let
//@feature if-else
//@feature for-while
//@feature parallel-fence
//@feature attempt
//@feature return-hierarchy
//@feature yield
//@feature output
//@feature foreign-code
//@feature dict-list
//@feature template-str
//@feature for-expr
//@feature field-index
//@feature file-type
//@feature record-type
//@feature enum
//@feature qualified-call
//@feature import
//@feature conv-var
//@feature request-input — dify human-input → ncoda ask 反编译（TestDecompileYAMLCompiles hitl-* 4 例 + convertHumanInput 定向断言）

// decompile_test.go — dify YAML → ncoda 反编译验证。
//
// 验证方式:反编译产物必须能被前端管线（lexer→parser→passes）完整接受
// （compile_ok）,这是反编译器的第一性门禁——产物是合法 ncoda。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/passes"
)

// fixtures 回归门禁:examples/yaml 全量 47 个语料全部反编译 → 前端管线
// compile_ok + 无 UNDEFINED_VAR（覆盖盘点见 TestDecompileAllFixturesCoverage;
// UNKNOWN 注释仅剩 human-input,原因见 docs/DIFY-DECOMPILE.md §3）。
var fixtures = allFixtures

// allFixtures 全量语料（examples/yaml）——用于覆盖盘点:编译失败的 fixture
// 暴露尚未覆盖的节点类型,逐步扩展后加入 fixtures。
var allFixtures = []string{
	"00_minimal_start_llm_end.yml",
	"01_simple_chat.yml",
	"02_rag_workflow.yml",
	"03_conditional_branch.yml",
	"04_code_execution.yml",
	"05_http_request.yml",
	"06_tool_use.yml",
	"07_template_transform.yml",
	"08_variable_aggregator.yml",
	"09_iteration.yml",
	"10_complex_multi_step.yml",
	"99_dead_nodes.yml",
	"answer_end_with_text.yml",
	"array_iteration_formatting_workflow.yml",
	"basic_chatflow.yml",
	"basic_llm_chat_workflow.yml",
	"chatflow_time_tool_static_output_workflow.yml",
	"conditional_hello_branching_workflow.yml",
	"conditional_parallel_code_execution_workflow.yml",
	"conditional_streaming_vs_template_workflow.yml",
	"dual_switch_variable_aggregator_workflow.yml",
	"echo-chat.yml",
	"echo-workflow.yml",
	"end_node_without_value_type_field_workflow.yml",
	"file-chat.yml",
	"file-upload.yml",
	"hitl-external.yml",
	"hitl-main.yml",
	"hitl-multi-node.yml",
	"hitl-single-action.yml",
	"http_request_with_json_tool_workflow.yml",
	"increment_loop_with_break_condition_workflow.yml",
	"iteration_flatten_output_disabled_workflow.yml",
	"iteration_flatten_output_enabled_workflow.yml",
	"loop_contains_answer.yml",
	"multilingual_parallel_llm_streaming_workflow.yml",
	"parameter_extractor_memory_compat.yml",
	"reasoning-chat.yml",
	"response_stream_filter_issue_170_workflow.yml",
	"search_dify_from_2023_to_2025.yml",
	"simple_passthrough_workflow.yml",
	"test-answer-order.yml",
	"test_complex_branch.yml",
	"test_streaming_conversation_variables.yml",
	"test_streaming_conversation_variables_v1_overwrite.yml",
	"update-conversation-variable-in-iteration.yml",
	"ws2-workflow.yml",
}

func TestDecompileYAMLCompiles(t *testing.T) {
	dir := os.Getenv("DIFY_YAML_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "..", "examples", "yaml")
	}
	for _, name := range fixtures {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: read: %v", name, err)
		}
		nc, err := DecompileYAML(string(b))
		if err != nil {
			t.Errorf("%s: decompile: %v", name, err)
			continue
		}
		res := frontendRun(nc, name, string(b))
		if res.HasErrors() {
			for _, d := range res.Errors() {
				t.Errorf("%s: frontend: %s: %s", name, d.Code, d.Message)
			}
			continue
		}
		// 反编译器契约:产物不允许未定义变量引用（UNDEFINED_VAR 在默认
		// profile 是 Warning,对反编译产物按 Error 对待——引用必须可解析）。
		for _, d := range res.Diagnostics {
			if d.Code == diag.UNDEFINED_VAR {
				t.Errorf("%s: frontend: %s: %s", name, d.Code, d.Message)
			}
		}
		if !strings.Contains(nc, "function main") {
			t.Errorf("%s: output missing main", name)
		}
		t.Logf("%s OK (%d bytes)", name, len(nc))
	}
}

// difyRuntimeProfile 宽松 target（不校验 Dify 专属形状,反编译产物
// 面向平台无关执行）。
// frontendRun 用 fixture 的 tool 注册表 + 无目标验证管线跑前端（限定名
// 工具调用是闭集契约,注册表 = dify YAML 就地取材的能力契约,见
// tool_registry.go）。
func frontendRun(nc, name, yamlText string) *passes.FrontendResult {
	return verifyNcoda(nc, name, yamlText)
}

// TestDecompileAllFixturesCoverage 全量语料盘点:统计可编译 / 编译失败
// （失败 = 未覆盖节点类型或缺口）,失败项打印前 3 条诊断,方便渐进扩展。
// 不阻断（盘点用）;fixtures 里的才算回归门禁。
func TestDecompileAllFixturesCoverage(t *testing.T) {
	dir := os.Getenv("DIFY_YAML_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "..", "examples", "yaml")
	}
	var pass, fail int
	for _, name := range allFixtures {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Logf("%s: read: %v", name, err)
			fail++
			continue
		}
		nc, err := DecompileYAML(string(b))
		if err != nil {
			t.Logf("FAIL %s: decompile: %v", name, err)
			fail++
			continue
		}
		res := frontendRun(nc, name, string(b))
		if res.HasErrors() {
			msgs := make([]string, 0, 3)
			for i, d := range res.Diagnostics {
				if d.Severity != diag.Error {
					continue
				}
				msgs = append(msgs, fmt.Sprintf("%s: %s", d.Code, d.Message))
				if len(msgs) == 3 {
					break
				}
				_ = i
			}
			t.Logf("FAIL %s: %v", name, msgs)
			fail++
			continue
		}
		pass++
	}
	t.Logf("coverage: %d/%d pass, %d fail", pass, len(allFixtures), fail)
}
