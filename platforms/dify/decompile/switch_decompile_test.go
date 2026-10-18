//@feature switch — dify question-classifier 多出口反编译 → ncoda switch 语句

package decompile

// switch_decompile_test.go —— dify question-classifier 反编译多出口 switch
// 定向回归（T3.1）。断言形态：switch (query) { "label": {...} } 多出口 +
// 分支体提升 + compile_ok。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/passes"
)

const qcTwoClassYAML = `app:
  mode: workflow
workflow:
  graph:
    nodes:
    - id: start_1
      type: custom
      data:
        type: start
        variables:
        - variable: query
          label: query
          type: text-input
          required: true
    - id: classify_2
      type: custom
      data:
        type: question-classifier
        query_variable_selector: [start_1, query]
        model:
          provider: provider
          name: model
          mode: chat
        classes:
        - id: support
          name: support
        - id: billing
          name: billing
    - id: classify_support_3
      type: custom
      data:
        type: code
        code_language: python3
        code: "def main() -> dict:\n    return {\"result\": \"support\"}"
        outputs:
          result: {type: string, children: null}
    - id: classify_billing_5
      type: custom
      data:
        type: code
        code_language: python3
        code: "def main() -> dict:\n    return {\"result\": \"billing\"}"
        outputs:
          result: {type: string, children: null}
    - id: classify_result_6
      type: custom
      data:
        type: variable-aggregator
        variables: [[classify_support_3, result], [classify_billing_5, result]]
        output_type: string
    - id: end_7
      type: custom
      data:
        type: end
        outputs:
        - variable: output
          value_type: string
          value_selector: [classify_result_6, output]
    edges:
    - id: edge_1
      source: start_1
      sourceHandle: ''
      target: classify_2
    - id: edge_2
      source: classify_2
      sourceHandle: support
      target: classify_support_3
    - id: edge_4
      source: classify_2
      sourceHandle: billing
      target: classify_billing_5
    - id: edge_5
      source: classify_support_3
      sourceHandle: ''
      target: classify_result_6
    - id: edge_6
      source: classify_billing_5
      sourceHandle: ''
      target: classify_result_6
    - id: edge_8
      source: classify_result_6
      sourceHandle: ''
      target: end_7
`

func TestDecompileSwitchMultiExit(t *testing.T) {
	nc, err := DecompileYAML(qcTwoClassYAML)
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	t.Logf("OUTPUT:\n%s", nc)
	// 断言 switch 关键字
	if !strings.Contains(nc, "switch (") {
		t.Error("output should contain switch keyword")
	}
	// 断言 case 标签（多出口）
	if !strings.Contains(nc, `"support"`) {
		t.Error("output should contain support branch label")
	}
	if !strings.Contains(nc, `"billing"`) {
		t.Error("output should contain billing branch label")
	}
	// compile_ok 门禁
	res := passes.DefaultPipeline().Run(nc, "switch_decompile.ncoda", nil)
	if res.HasErrors() {
		for _, d := range res.Diagnostics {
			t.Errorf("pipeline %d %s: %s", d.Code, d.Severity, d.Message)
		}
	}
	// 无 classify 值形式残留
	if strings.Contains(nc, "classify<") {
		t.Error("output should not contain classify value form")
	}
}
